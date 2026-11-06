/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// ProcessState proves the exact command and start time before signaling a PID.
type ProcessState struct {
	PID                        int
	Binary, ReadyFile, Service string
	StartTime, Command, Node   string
}

// ErrProviderIdentityChanged means a recorded PID now belongs to another process.
var ErrProviderIdentityChanged = errors.New("provider identity changed")

// RunState contains no credentials or access-bearing URLs.
type RunState struct {
	Schema                           int
	Owner, RunID, Namespace, Console string
	RunDir, ResourcePolicy           string
	Services, Skills                 []string
	Processes                        []ProcessState
	CompletedCases                   []string
	Successful, Cleaned              bool
}

func processID() int { return os.Getpid() }

// WriteJSON replaces state atomically with owner-only permissions.
func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// LoadState verifies all ownership and path constraints before cleanup.
func LoadState(path string) (*RunState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state RunState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	actualRoot, err := filepath.EvalSymlinks(state.RunDir)
	if err != nil {
		return nil, err
	}
	stateRoot, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	stateRoot, err = filepath.EvalSymlinks(stateRoot)
	if err != nil {
		return nil, err
	}
	if actualRoot != stateRoot {
		return nil, errors.New("state file must reside inside its owned run directory")
	}
	return &state, nil
}

// Validate rejects fabricated broad cleanup targets and path traversal.
func (s *RunState) Validate() error {
	if s.Schema != 1 || s.Owner != Owner || !safeSegment.MatchString(s.RunID) || len(s.RunID) > 32 ||
		!safeSegment.MatchString(s.Namespace) || strings.EqualFold(s.Namespace, "default") ||
		!filepath.IsAbs(s.RunDir) || filepath.Base(s.RunDir) != s.RunID {
		return errors.New("invalid or unowned E2E state")
	}
	if err := ValidateConsole(s.Console); err != nil {
		return err
	}
	services := make(map[string]bool, len(s.Services))
	for _, name := range s.Services {
		if !safeSegment.MatchString(name) || !strings.HasPrefix(name, ServicePrefix+s.RunID+"-") {
			return errors.New("state contains an unowned service")
		}
		services[name] = true
	}
	for _, name := range s.Skills {
		if !safeSegment.MatchString(name) || !strings.HasPrefix(name, SkillPrefix+s.RunID+"-") {
			return errors.New("state contains an unowned Skill")
		}
	}
	for _, process := range s.Processes {
		if process.PID <= 1 || !services[process.Service] || !safeSegment.MatchString(process.Node) ||
			!filepath.IsAbs(process.Binary) || !pathWithin(s.RunDir, process.ReadyFile) ||
			process.StartTime == "" || process.Command == "" {
			return errors.New("state contains an unowned provider")
		}
		for _, argument := range []string{"-mode=provider", "-run-id=" + s.RunID, "-service=" + process.Service, "-ready=" + process.ReadyFile} {
			if !hasArgument(process.Command, argument) {
				return errors.New("provider command does not prove run ownership")
			}
		}
	}
	return nil
}

func pathWithin(root, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func hasArgument(command, argument string) bool {
	return strings.Contains(command, " "+argument+" ") || strings.HasSuffix(command, " "+argument)
}

func processIdentity(pid int) (start, command string, exists bool, err error) {
	read := func(field string) (string, error) {
		out, err := exec.Command("ps", "-p", fmt.Sprint(pid), "-o", field+"=").Output()
		return strings.TrimSpace(string(out)), err
	}
	start, err = read("lstart")
	if exit := new(exec.ExitError); errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	command, err = read("command")
	return start, command, err == nil, err
}

// StopProvider refuses PID reuse and waits for process exit, not just a ready flag.
func StopProvider(ctx context.Context, process ProcessState) error {
	if err := signalProvider(process, syscall.SIGTERM); err != nil {
		return err
	}
	return Eventually(ctx, 25*time.Second, func() error {
		start, command, exists, err := processIdentity(process.PID)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if start != process.StartTime || command != process.Command {
			return ErrProviderIdentityChanged
		}
		return fmt.Errorf("provider PID %d has not exited", process.PID)
	})
}

func signalProvider(process ProcessState, signal syscall.Signal) error {
	if process.PID <= 1 || process.StartTime == "" || process.Command == "" || !filepath.IsAbs(process.Binary) {
		return errors.New("refuse to signal an incomplete provider identity")
	}
	start, command, exists, err := processIdentity(process.PID)
	if err != nil || !exists {
		return err
	}
	if start != process.StartTime || command != process.Command || !strings.HasPrefix(command, process.Binary+" ") {
		return fmt.Errorf("%w: refuse to signal reused or unowned PID %d", ErrProviderIdentityChanged, process.PID)
	}
	p, err := os.FindProcess(process.PID)
	if err != nil {
		return err
	}
	defer func() { _ = p.Release() }()
	if err := p.Signal(signal); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// Eventually retries assertions until a bounded convergence deadline.
func Eventually(ctx context.Context, timeout time.Duration, check func() error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("convergence deadline: %w; last assertion: %v", err, last)
		}
		last = check()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

// Cleanup performs state-scoped remote and local cleanup; namespaces are retained.
func Cleanup(ctx context.Context, o Options) error {
	var state *RunState
	if o.StateFile != "" {
		loaded, err := LoadState(o.StateFile)
		if err != nil {
			return err
		}
		state = loaded
		o.Console = state.Console
		o.Namespace = state.Namespace
		log.Printf("cleanup run=%s namespace=%s services=%d skills=%d processes=%d apply=%t delete-service=%t",
			state.RunID, state.Namespace, len(state.Services), len(state.Skills), len(state.Processes),
			o.Apply && !o.DryRun, o.DeleteServices)
	}
	if state == nil && !o.DeleteServices {
		return errors.New("cleanup requires -state or -delete-service")
	}
	if state == nil {
		log.Printf("cleanup empty services namespace=%s apply=%t", o.Namespace, o.Apply && !o.DryRun)
	}
	if !o.Apply || o.DryRun {
		if state != nil {
			log.Printf("would stop %d recorded providers", len(state.Processes))
		}
		if o.DeleteServices {
			if err := previewRemoteCleanup(ctx, o, state); err != nil {
				return err
			}
		}
		if o.RemoveArtifacts {
			log.Printf("would remove known run-local artifacts")
		}
		return nil
	}
	if state != nil {
		for _, process := range state.Processes {
			if err := StopProvider(ctx, process); err != nil {
				if errors.Is(err, ErrProviderIdentityChanged) {
					log.Printf("skip stale provider identity PID=%d service=%s: %v",
						process.PID, process.Service, err)
					continue
				}
				return err
			}
		}
		if !o.DeleteServices {
			log.Printf("service and Skill deletion skipped; use -delete-service")
			return removeRunArtifacts(state, o.RemoveArtifacts)
		}
	}
	cfg, err := LoadConfigurationForCleanup(o)
	if err != nil {
		return err
	}
	console := NewConsole(o, cfg)
	defer console.Close()
	if state == nil {
		deleted, err := deleteEmptyOwnedServices(ctx, console, o.Namespace)
		if err != nil {
			return err
		}
		for _, name := range deleted {
			log.Printf("deleted empty service: %s/%s", o.Namespace, name)
		}
		return nil
	}
	var failures []error
	for _, name := range state.Services {
		if err := cleanupService(ctx, console, state, name); err != nil {
			failures = append(failures, err)
		}
	}
	for _, name := range state.Skills {
		query := url.Values{"namespace": {state.Namespace}, "name": {name}}
		result, err := console.Request(ctx, http.MethodGet, "/skill/v1/skills?"+query.Encode(), nil)
		if result != nil && result.Code == 400202 {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if result.Resource == nil || result.Resource.Ext[OwnerLabel] != Owner || result.Resource.Ext[RunLabel] != state.RunID {
			failures = append(failures, fmt.Errorf("refuse to delete unowned Skill %s", name))
			continue
		}
		_, err = console.Request(ctx, http.MethodDelete, "/skill/v1/skills?"+query.Encode(), nil)
		if err != nil {
			failures = append(failures, err)
		}
	}
	deleted, err := deleteEmptyOwnedServices(ctx, console, o.Namespace)
	if err != nil {
		failures = append(failures, err)
	} else {
		for _, name := range deleted {
			log.Printf("deleted empty service: %s/%s", o.Namespace, name)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	state.Cleaned = true
	if err := WriteJSON(o.StateFile, state); err != nil {
		return err
	}
	return removeRunArtifacts(state, o.RemoveArtifacts)
}

func previewRemoteCleanup(ctx context.Context, o Options, state *RunState) error {
	if state != nil {
		log.Printf("would delete %d services and %d Skills for run=%s",
			len(state.Services), len(state.Skills), state.RunID)
	}
	cfg, err := LoadConfigurationForCleanup(o)
	if err != nil {
		return err
	}
	console := NewConsole(o, cfg)
	defer console.Close()
	names, err := emptyOwnedServiceNames(ctx, console, o.Namespace)
	if err != nil {
		return err
	}
	log.Printf("empty owned services namespace=%s count=%d", o.Namespace, len(names))
	for _, name := range names {
		log.Printf("empty owned service: %s/%s", o.Namespace, name)
	}
	return nil
}

func removeRunArtifacts(state *RunState, remove bool) error {
	if !remove {
		return nil
	}
	// Preserve state/results while removing only known run-local artifacts.
	for _, name := range []string{"providers", "skill-cache", "fault-cache"} {
		if err := os.RemoveAll(filepath.Join(state.RunDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func emptyOwnedServiceNames(ctx context.Context, console *Console, namespace string) ([]string, error) {
	services, err := console.Services(ctx, namespace)
	if err != nil {
		return nil, err
	}
	return filterOwnedEmptyServiceNames(namespace, services), nil
}

func filterOwnedEmptyServiceNames(namespace string, services []ServiceView) []string {
	names := make(map[string]struct{})
	for _, service := range services {
		if !ownedEmptyService(namespace, &service) {
			continue
		}
		names[service.Name] = struct{}{}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func ownedEmptyService(namespace string, service *ServiceView) bool {
	return service != nil && service.Namespace == namespace &&
		strings.HasPrefix(service.Name, ServicePrefix) &&
		safeSegment.MatchString(service.Name) &&
		service.Metadata[OwnerLabel] == Owner &&
		service.TotalInstanceCount == 0
}

func deleteEmptyOwnedServices(ctx context.Context, console *Console, namespace string) ([]string, error) {
	names, err := emptyOwnedServiceNames(ctx, console, namespace)
	if err != nil {
		return nil, err
	}
	verified := make([]string, 0, len(names))
	for _, name := range names {
		service, err := console.Service(ctx, namespace, name)
		if err != nil {
			return nil, err
		}
		if !ownedEmptyService(namespace, service) {
			continue
		}
		instances, err := console.Instances(ctx, namespace, name)
		if err != nil {
			return nil, err
		}
		if len(instances) != 0 {
			continue
		}
		verified = append(verified, name)
	}
	if len(verified) == 0 {
		return nil, nil
	}
	payload := make([]map[string]string, 0, len(verified))
	for _, name := range verified {
		payload = append(payload, map[string]string{"namespace": namespace, "name": name})
	}
	if _, err := console.Request(ctx, http.MethodPost, "/naming/v1/services/delete", payload); err != nil {
		return nil, err
	}
	return verified, nil
}

func cleanupService(ctx context.Context, console *Console, state *RunState, name string) error {
	service, err := console.Service(ctx, state.Namespace, name)
	if err != nil || service == nil {
		return err
	}
	if service.Metadata[OwnerLabel] != Owner || service.Metadata[RunLabel] != state.RunID {
		return fmt.Errorf("refuse to delete unowned service %s", name)
	}
	instances, err := console.Instances(ctx, state.Namespace, name)
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if instance.Metadata[OwnerLabel] != Owner || instance.Metadata[RunLabel] != state.RunID ||
			instance.Namespace != state.Namespace || instance.Service != name {
			return fmt.Errorf("refuse to delete service containing unowned instance: %s", name)
		}
		if _, err := console.Request(ctx, http.MethodPost, "/naming/v1/instances/delete", []map[string]string{
			{"id": instance.ID, "namespace": state.Namespace, "service": name},
		}); err != nil {
			return err
		}
	}
	_, err = console.Request(ctx, http.MethodPost, "/naming/v1/services/delete", []map[string]string{{"namespace": state.Namespace, "name": name}})
	return err
}
