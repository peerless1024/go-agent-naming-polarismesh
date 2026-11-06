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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	adapterskill "trpc.group/trpc-go/trpc-agent-naming-polarismesh/skill"
)

type child struct {
	process ProcessState
	ready   Ready
	exit    chan struct{}
	err     error
}

type suite struct {
	opts                                   Options
	state                                  RunState
	sdk                                    api.SDKContext
	agents                                 api.AgentAPI
	skills                                 api.SkillAPI
	consumer                               api.ConsumerAPI
	console                                *Console
	http                                   *http.Client
	transport                              *http.Transport
	loader                                 *adapterskill.Loader
	children                               []*child
	first, second                          *child
	invalid                                []*child
	runtime                                *adapteragent.Runtime
	resolver                               *naming.Resolver
	remote                                 agent.Agent
	boundID                                string
	main                                   string
	skillA, skillB, skillDraft, skillMulti string
	firstInstall                           *model.InstalledSkill
}

// CaseResult is the actual result of one non-skipped E2E case.
type CaseResult struct {
	ID, Name, Status, Error string
	Duration                string
}

// Results is a timestamped full-run result rather than a stale summary.
type Results struct {
	RunID, Namespace, StartedAt, FinishedAt string
	Cases                                   []CaseResult
	Passed, Failed                          int
}

// Verify runs every defined case against real dependencies and records failures.
func Verify(ctx context.Context, o Options) (result error) {
	ctx, cancel := context.WithTimeout(ctx, o.RunTimeout)
	defer cancel()
	if err := os.MkdirAll(o.RunDir, 0o700); err != nil {
		return err
	}
	statePath := filepath.Join(o.RunDir, "state.json")
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		return errors.New("run state already exists; use a new run-id")
	}
	s := &suite{opts: o, state: RunState{Schema: 1, Owner: Owner, RunID: o.RunID, Namespace: o.Namespace,
		Console: o.Console, RunDir: o.RunDir, ResourcePolicy: o.ResourcePolicy}}
	if err := s.save(); err != nil {
		return err
	}
	defer func() {
		if s.runtime != nil {
			result = errors.Join(result, s.runtime.Close(context.Background()))
		}
		if s.console != nil {
			s.console.Close()
		}
		if s.transport != nil {
			s.transport.CloseIdleConnections()
		}
		if s.sdk != nil {
			s.sdk.Destroy()
		}
		if o.ResourcePolicy == "cleanup" {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			for _, child := range s.children {
				result = errors.Join(result, s.stop(cleanupCtx, child))
			}
			cleanupOptions := o
			cleanupOptions.StateFile, cleanupOptions.Apply = statePath, true
			cleanupOptions.DeleteServices = true
			result = errors.Join(result, Cleanup(cleanupCtx, cleanupOptions))
		} else {
			log.Printf("KEEP run resources; state=%s", statePath)
		}
	}()
	cfg, err := LoadConfiguration(o, true)
	if err != nil {
		return err
	}
	s.console = NewConsole(o, cfg)
	if err := s.console.EnsureNamespace(ctx, o.Namespace); err != nil {
		return err
	}
	s.sdk, err = api.InitContextByConfig(cfg)
	if err != nil {
		return err
	}
	s.agents, s.skills, s.consumer = api.NewAgentAPIByContext(s.sdk), api.NewSkillAPIByContext(s.sdk), api.NewConsumerAPIByContext(s.sdk)
	s.loader = adapterskill.NewLoader(s.agents, s.skills)
	s.http, s.transport = HTTPClient(o.Timeout)
	s.main = s.service("main")
	s.skillA, s.skillB = SkillPrefix+o.RunID+"-a", SkillPrefix+o.RunID+"-b"
	s.skillDraft, s.skillMulti = SkillPrefix+o.RunID+"-draft", SkillPrefix+o.RunID+"-multi"
	if err := s.ensureService(ctx, s.main); err != nil {
		return err
	}
	log.Printf("REAL E2E run=%s namespace=%s discover=%s console=%s skill=%s", o.RunID, o.Namespace, o.Discover, o.Console, o.SkillAddress)
	cases := []struct {
		id, name string
		run      func(context.Context) error
	}{
		{"1.1", "registration-metadata-and-heartbeat", s.registration},
		{"1.2", "unary-authentication-and-tenant", s.unaryAuth},
		{"1.3", "streaming-output-and-cancellation", s.streaming},
		{"1.4", "exact-root-and-unicode-endpoints", s.exactEndpoints},
		{"2.1", "two-instance-sticky-discovery", s.sticky},
		{"2.2", "invalid-card-and-host-port-filtering", s.invalidCards},
		{"2.3", "isolation-and-unhealthy-filtering", s.healthFiltering},
		{"2.4", "concurrent-real-clients", s.concurrent},
		{"2.5", "deregistration-and-sticky-failover", s.failover},
		{"2.6", "empty-set-invalidates-remote-agent", s.emptySet},
		{"2.7", "real-network-fallback-expiry-and-recovery", s.fallback},
		{"3.1", "published-skill-download-and-install", s.publishedSkills},
		{"3.2", "missing-draft-and-wrong-version-fail-fast", s.invalidSkills},
		{"3.3", "cache-hit-and-content-repair", s.skillCache},
		{"3.4", "active-version-and-pinned-cache", s.skillVersions},
		{"3.5", "multiple-skill-dedup-and-sorting", s.skillRepository},
		{"3.6", "merge-mount-retains-latest-references", s.mergeMount},
		{"3.7", "replace-and-empty-mount-remove-old-skills", s.replaceMount},
		{"3.8", "multiple-entry-skill-is-rejected", s.extraEntry},
		{"3.9", "provider-executes-published-skill", s.skillProvider},
		{"4.1", "ordinary-service-type-conflict", s.typeConflict},
		{"4.2", "prepare-retry-and-borrowed-context-lifecycle", s.prepareRetry},
		{"4.3", "ensure-existing-service-is-nondestructive", s.ensureExisting},
		{"4.4", "cleanup-state-and-ownership-safeguards", s.cleanupSafety},
	}
	report := Results{RunID: o.RunID, Namespace: o.Namespace, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, test := range cases {
		start := time.Now()
		log.Printf("[case %s %s] START", test.id, test.name)
		err := test.run(ctx)
		item := CaseResult{ID: test.id, Name: test.name, Duration: time.Since(start).String(), Status: "PASS"}
		if err != nil {
			item.Status, item.Error = "FAIL", err.Error()
			report.Failed++
			result = errors.Join(result, fmt.Errorf("case %s %s: %w", test.id, test.name, err))
			log.Printf("[case %s %s] FAIL: %v", test.id, test.name, err)
		} else {
			report.Passed++
			s.state.CompletedCases = append(s.state.CompletedCases, test.id)
			log.Printf("[case %s %s] PASS", test.id, test.name)
		}
		report.Cases = append(report.Cases, item)
		report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if err := WriteJSON(filepath.Join(o.RunDir, "results.json"), report); err != nil {
			return errors.Join(result, err)
		}
		if err := s.save(); err != nil {
			return errors.Join(result, err)
		}
	}
	s.state.Successful = report.Failed == 0 && report.Passed == len(cases)
	result = errors.Join(result, s.save())
	log.Printf("REAL E2E RESULT passed=%d failed=%d total=%d state=%s", report.Passed, report.Failed, len(cases), statePath)
	return result
}

func (s *suite) service(label string) string { return ServicePrefix + s.opts.RunID + "-" + label }
func (s *suite) save() error                 { return WriteJSON(filepath.Join(s.opts.RunDir, "state.json"), s.state) }

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (s *suite) ensureService(ctx context.Context, name string) error {
	s.state.Services = appendUnique(s.state.Services, name)
	if err := s.save(); err != nil {
		return err
	}
	service, err := s.agents.EnsureService(&api.EnsureAgentServiceRequest{EnsureAgentServiceRequest: model.EnsureAgentServiceRequest{
		Namespace: s.opts.Namespace, Service: name, Metadata: map[string]string{OwnerLabel: Owner, RunLabel: s.opts.RunID},
	}})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if service.Metadata[OwnerLabel] != Owner || service.Metadata[RunLabel] != s.opts.RunID {
		return errors.New("refuse to reuse an unowned service")
	}
	return ValidateAIService(service)
}

func (s *suite) start(ctx context.Context, service, node, path, metadataMode, skillName string) (*child, error) {
	if err := s.ensureService(ctx, service); err != nil {
		return nil, err
	}
	workdir := filepath.Join(s.opts.RunDir, "providers", node)
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		return nil, err
	}
	readyFile := filepath.Join(workdir, "ready.json")
	binary, err := filepath.Abs(s.opts.ProviderBinary)
	if err != nil {
		return nil, err
	}
	args := []string{"-mode=provider", "-server=" + s.opts.Server, "-discover=" + s.opts.Discover,
		"-skill-address=" + s.opts.SkillAddress, "-console=" + s.opts.Console, "-namespace=" + s.opts.Namespace,
		"-run-id=" + s.opts.RunID, "-run-dir=" + workdir, "-service=" + service, "-node=" + node,
		"-ready=" + readyFile, "-a2a-path=" + path, "-advertise-host=" + s.opts.Host, "-metadata-mode=" + metadataMode,
		"-skill-name=" + skillName, "-skill-version=1.0.0", "-timeout=" + s.opts.Timeout.String(), "-tenant=" + s.opts.Tenant}
	if s.opts.ConfigFile != "" {
		args = append(args, "-config-file="+s.opts.ConfigFile)
	}
	if s.opts.Debug {
		args = append(args, "-debug")
	}
	command := exec.Command(binary, args...)
	command.Dir = workdir
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	file, err := os.OpenFile(filepath.Join(workdir, "provider.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	command.Stdout, command.Stderr = file, file
	if err := command.Start(); err != nil {
		_ = file.Close()
		return nil, err
	}
	_ = file.Close()
	c := &child{process: ProcessState{PID: command.Process.Pid, Binary: binary, ReadyFile: readyFile, Service: service, Node: node}, exit: make(chan struct{})}
	s.children = append(s.children, c)
	go func() { c.err = command.Wait(); close(c.exit) }()
	start, actual, exists, err := processIdentity(command.Process.Pid)
	if err != nil || !exists {
		_ = command.Process.Signal(syscall.SIGTERM)
		<-c.exit
		return nil, errors.Join(err, c.err, errors.New("provider exited before identity was recorded"))
	}
	c.process.StartTime, c.process.Command = start, actual
	s.state.Processes = append(s.state.Processes, c.process)
	if err := s.save(); err != nil {
		_ = command.Process.Signal(syscall.SIGTERM)
		<-c.exit
		return nil, err
	}
	err = Eventually(ctx, 40*time.Second, func() error {
		select {
		case <-c.exit:
			return fmt.Errorf("provider exited before readiness: %v; log=%s", c.err, filepath.Join(workdir, "provider.log"))
		default:
		}
		data, err := os.ReadFile(readyFile)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &c.ready); err != nil {
			return err
		}
		if c.ready.PID != c.process.PID || c.ready.Service != service || c.ready.RunID != s.opts.RunID ||
			c.ready.InstanceID == "" || c.ready.Stopped {
			return errors.New("provider readiness identity is invalid")
		}
		return nil
	})
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return nil, errors.Join(err, s.stop(stopCtx, c))
	}
	return c, nil
}

func (s *suite) stop(ctx context.Context, c *child) error {
	if c == nil {
		return nil
	}
	select {
	case <-c.exit:
		return c.err
	default:
	}
	if err := StopProvider(ctx, c.process); err != nil {
		return err
	}
	select {
	case <-c.exit:
		return c.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *suite) all(service string) (*model.InstancesResponse, error) {
	req := &api.GetAllInstancesRequest{GetAllInstancesRequest: model.GetAllInstancesRequest{Namespace: s.opts.Namespace, Service: service}}
	req.SetTimeout(s.opts.Timeout)
	req.SetRetryCount(0)
	return s.consumer.GetAllInstances(req)
}

func (s *suite) wait(ctx context.Context, c *child, present bool) error {
	if c == nil {
		return errors.New("provider prerequisite is missing")
	}
	return Eventually(ctx, 30*time.Second, func() error {
		response, err := s.all(c.ready.Service)
		if err != nil {
			return err
		}
		for _, instance := range response.GetInstances() {
			if instance.GetId() == c.ready.InstanceID {
				if !present {
					return errors.New("deregistered instance remains visible")
				}
				if instance.GetHost() != c.ready.Host || instance.GetPort() != uint32(c.ready.Port) ||
					instance.GetMetadata()[RunLabel] != s.opts.RunID || !instance.IsHealthy() || instance.IsIsolated() {
					return errors.New("registered instance identity or health differs")
				}
				return nil
			}
		}
		if present {
			return errors.New("registered instance is not visible")
		}
		return nil
	})
}

func (s *suite) providerConfig(service string) adapterconfig.Config {
	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace, cfg.ServiceName, cfg.AgentName = s.opts.Namespace, service, service
	cfg.AgentVersion, cfg.AdvertiseHost, cfg.AdvertisePort = "1.0.0", "127.0.0.1", 18080
	cfg.PolarisRequestTimeout = s.opts.Timeout
	return cfg
}

func (s *suite) newRuntime(service string) (*adapteragent.Runtime, error) {
	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace, cfg.ServiceName, cfg.PolarisRequestTimeout = s.opts.Namespace, service, s.opts.Timeout
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(s.sdk))
	if err != nil {
		return nil, err
	}
	if err := runtime.Prepare(context.Background()); err != nil {
		_ = runtime.Close(context.Background())
		return nil, err
	}
	return runtime, nil
}

func (s *suite) invoke(ctx context.Context, remote agent.Agent, message, expected string) error {
	if remote == nil {
		return errors.New("remote Agent prerequisite is missing")
	}
	callCtx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()
	text, _, err := Invoke(callCtx, remote, message)
	if err != nil {
		return err
	}
	if !strings.Contains(text, expected) {
		return fmt.Errorf("A2A output differs: got %q expected substring %q", text, expected)
	}
	return nil
}

func sortedIDs(service *model.AgentService) []string {
	var ids []string
	for _, item := range service.ExtendedMetadata {
		if item != nil && item.AgentSkill != nil {
			ids = append(ids, item.AgentSkill.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// Concurrent uses one shared immutable transport and independently built clients.
func runConcurrent(count int, run func(int) error) error {
	var group sync.WaitGroup
	failures := make(chan error, count)
	for i := 0; i < count; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			if err := run(index); err != nil {
				failures <- err
			}
		}(i)
	}
	group.Wait()
	close(failures)
	var result []error
	for err := range failures {
		result = append(result, err)
	}
	return errors.Join(result...)
}
