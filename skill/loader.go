/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package skill installs and assembles Polaris Agent Skills.
package skill

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentmodel"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	adapteragentapi "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentapi"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
	"trpc.group/trpc-go/trpc-go/log"
)

// Options configures Skill installation and service-level mounting.
type Options struct {
	Namespace        string
	Service          string
	CacheDir         string
	RequestTimeout   time.Duration
	Mode             string
	ExplicitRefs     []adapterconfig.SkillRef
	UseServiceSkills bool
	Mount            bool
	MountReplace     bool
	ListLimit        int
	MaxSkills        int
	FailureThreshold int
	CacheMaxAge      time.Duration
}

// Loader installs configured Skills and builds the executable repository.
type Loader struct {
	agentAPI adapteragentapi.AgentAPI
	skillAPI api.SkillAPI
}

// NewLoader creates a Skill loader with the supplied Polaris APIs.
func NewLoader(agentAPI adapteragentapi.AgentAPI, skillAPI api.SkillAPI) *Loader {
	return &Loader{agentAPI: agentAPI, skillAPI: skillAPI}
}

type loadedSkillRef struct {
	ref   adapterconfig.SkillRef
	skill *model.AgentSkill
}

// Load installs Skills and returns a repository containing only this run's
// selected entries.
func (l *Loader) Load(
	ctx context.Context,
	options Options,
) (agentskill.Repository, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mode, err := normalizedMode(options)
	if err != nil {
		return nil, err
	}
	var serviceRefs []loadedSkillRef
	var service *model.AgentService
	if mode == adapterconfig.SkillModeMounted && !options.Mount {
		if l.agentAPI == nil {
			return nil, adaptererrors.ErrAgentServiceAPIUnsupported
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		timeout, err := serviceRequestTimeout(ctx, options.RequestTimeout)
		if err != nil {
			return nil, err
		}
		service, err = l.agentAPI.GetService(&adapteragentapi.GetServiceRequest{
			GetAgentServiceRequest: agentmodel.GetAgentServiceRequest{
				Namespace:  options.Namespace,
				Service:    options.Service,
				Timeout:    &timeout,
				RetryCount: new(int),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("read service Skill references: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateAgentServiceType(service); err != nil {
			return nil, err
		}
	}
	if mode == adapterconfig.SkillModeMounted && !options.Mount {
		if service != nil {
			refs, err := skillRefsFromService(service)
			if err != nil {
				return nil, err
			}
			serviceRefs = append(serviceRefs, refs...)
		}
	}

	var installRefs []loadedSkillRef
	switch mode {
	case adapterconfig.SkillModeNames:
		installRefs, err = mergeLoadedSkillRefs(nil, options.ExplicitRefs)
	case adapterconfig.SkillModeMounted:
		installRefs = append(installRefs, serviceRefs...)
		installRefs, err = mergeLoadedSkillRefs(installRefs, options.ExplicitRefs)
	case adapterconfig.SkillModePublished:
		installRefs, err = l.loadPublishedRefs(ctx, options)
	}
	if err != nil {
		return nil, err
	}
	mountRefs, err := mergeLoadedSkillRefs(nil, options.ExplicitRefs)
	if err != nil {
		return nil, err
	}

	if options.Mount {
		if l.agentAPI == nil {
			return nil, adaptererrors.ErrAgentServiceAPIUnsupported
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		service, err = l.agentAPI.MountSkills(&adapteragentapi.MountSkillsRequest{
			MountAgentSkillsRequest: agentmodel.MountAgentSkillsRequest{
				Namespace: options.Namespace,
				Service:   options.Service,
				Skills:    agentSkillsFromLoadedRefs(mountRefs),
				Replace:   options.MountReplace,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("mount service Skill references: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateAgentServiceType(service); err != nil {
			return nil, err
		}
		if mode == adapterconfig.SkillModeMounted && !options.MountReplace {
			serviceRefs, err = skillRefsFromService(service)
			if err != nil {
				return nil, err
			}
			installRefs, err = mergeLoadedSkillRefs(serviceRefs, options.ExplicitRefs)
			if err != nil {
				return nil, err
			}
		}
	}

	if len(installRefs) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if options.CacheDir == "" {
		return nil, errors.New("skill cache directory is required to install Skill references")
	}
	root, err := filepath.Abs(options.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("resolve skill cache directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create skill cache directory: %w", err)
	}
	if len(installRefs) > 0 && l.skillAPI == nil {
		return nil, adaptererrors.ErrSkillInstallerUnsupported
	}

	installed := make([]model.InstalledSkill, 0, len(installRefs))
	installedRefs := make([]loadedSkillRef, 0, len(installRefs))
	var installFailures []error
	for _, loaded := range installRefs {
		ref := loaded.ref
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := l.installSkill(ctx, options, ref, root, false)
		if err != nil {
			installFailures = append(installFailures, fmt.Errorf("install Skill %s: %w", ref.ID(), err))
			log.Warnf("skip failed Skill %s: %v", ref.ID(), err)
			if options.FailureThreshold == 0 || len(installFailures) > options.FailureThreshold {
				return nil, errors.Join(installFailures...)
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateInstalledSkill(root, ref, result); err != nil {
			installFailures = append(installFailures, err)
			log.Warnf("skip invalid Skill installation %s: %v", ref.ID(), err)
			if options.FailureThreshold == 0 || len(installFailures) > options.FailureThreshold {
				return nil, errors.Join(installFailures...)
			}
			continue
		}
		if options.CacheMaxAge > 0 && result.CacheHit && !result.Downloaded.IsZero() &&
			time.Since(result.Downloaded) > options.CacheMaxAge {
			result, err = l.installSkill(ctx, options, ref, root, true)
			if err != nil {
				installFailures = append(installFailures, fmt.Errorf("refresh stale Skill %s: %w", ref.ID(), err))
				log.Warnf("skip stale Skill %s after refresh failure: %v", ref.ID(), err)
				if options.FailureThreshold == 0 || len(installFailures) > options.FailureThreshold {
					return nil, errors.Join(installFailures...)
				}
				continue
			}
			if err := validateInstalledSkill(root, ref, result); err != nil {
				return nil, err
			}
		}
		installed = append(installed, *result)
		installedRefs = append(installedRefs, loaded)
	}
	if len(installed) == 0 {
		if len(installFailures) > 0 {
			return nil, errors.Join(installFailures...)
		}
		return nil, nil
	}
	roots, err := validateInstallLayouts(installedRefs, installed)
	if err != nil {
		return nil, err
	}

	repository, err := agentskill.NewFSRepository(roots...)
	if err != nil {
		return nil, fmt.Errorf("scan installed Skill directories: %w", err)
	}
	if len(repository.Summaries()) != len(installed) {
		refs := make([]string, 0, len(installedRefs))
		for _, loaded := range installedRefs {
			refs = append(refs, loaded.ref.ID())
		}
		return nil, fmt.Errorf(
			"installed Skills expose %d repository entries for %d references (%s); Skill names must be unique",
			len(repository.Summaries()),
			len(installed),
			strings.Join(refs, ", "),
		)
	}
	for _, loaded := range installedRefs {
		if _, err := repository.Get(loaded.ref.Name); err != nil {
			return nil, fmt.Errorf(
				"installed Skill %s does not expose repository entry %q: %w",
				loaded.ref.ID(),
				loaded.ref.Name,
				err,
			)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return repository, nil
}

func serviceRequestTimeout(ctx context.Context, configured time.Duration) (time.Duration, error) {
	timeout := configured
	if timeout <= 0 {
		timeout = adapterconfig.DefaultConfig().PolarisRequestTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 0, context.DeadlineExceeded
	}
	return timeout, nil
}

func normalizedMode(options Options) (string, error) {
	mode := strings.TrimSpace(options.Mode)
	if mode == "" {
		switch {
		case options.UseServiceSkills:
			mode = adapterconfig.SkillModeMounted
		case len(options.ExplicitRefs) > 0:
			mode = adapterconfig.SkillModeNames
		default:
			mode = adapterconfig.SkillModeNames
		}
	}
	switch mode {
	case adapterconfig.SkillModeNames, adapterconfig.SkillModePublished, adapterconfig.SkillModeMounted:
		return mode, nil
	default:
		return "", fmt.Errorf("skill mode %q is not supported", mode)
	}
}

func (l *Loader) loadPublishedRefs(
	ctx context.Context,
	options Options,
) ([]loadedSkillRef, error) {
	if l.skillAPI == nil {
		return nil, adaptererrors.ErrSkillInstallerUnsupported
	}
	limit := options.ListLimit
	if limit <= 0 {
		limit = adapterconfig.DefaultConfig().SkillListLimit
	}
	maxSkills := options.MaxSkills
	if maxSkills <= 0 {
		maxSkills = adapterconfig.DefaultConfig().SkillMaxSkills
	}
	byID := make(map[string]loadedSkillRef)
	var offset uint32
	for len(byID) < maxSkills {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := l.listPublishedSkills(ctx, options, offset, limit)
		if err != nil {
			return nil, fmt.Errorf("list published Skills: %w", err)
		}
		if result == nil {
			return nil, errors.New("list published Skills returned an empty result")
		}
		for _, resource := range result.Resources {
			if resource == nil || resource.VersionInfo == nil {
				continue
			}
			version := strings.TrimSpace(resource.VersionInfo.ActiveVersion)
			if version == "" {
				version = strings.TrimSpace(resource.Version)
			}
			if resource.Name == "" || version == "" {
				continue
			}
			ref := adapterconfig.SkillRef{
				Namespace: options.Namespace,
				Name:      resource.Name,
				Version:   version,
			}
			byID[ref.ID()] = loadedSkillRef{ref: ref}
			if len(byID) >= maxSkills {
				break
			}
		}
		// Total counts the raw page; polaris-go filters unpublished resources.
		// Advance by that raw window, including pages filtered to zero entries.
		if result.Total <= offset || result.Total-offset <= uint32(limit) {
			break
		}
		offset += uint32(limit)
	}
	refs := make([]loadedSkillRef, 0, len(byID))
	for _, ref := range byID {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].ref.ID() < refs[j].ref.ID()
	})
	return refs, nil
}

func (l *Loader) listPublishedSkills(
	ctx context.Context,
	options Options,
	offset uint32,
	limit int,
) (*api.SkillListResult, error) {
	callCtx, cancel := skillCallContext(ctx, options.RequestTimeout)
	defer cancel()
	return callSkill(callCtx, func() (*api.SkillListResult, error) {
		return l.skillAPI.GetSkillListResult(&api.GetSkillListRequest{
			GetSkillListRequest: model.GetSkillListRequest{
				Namespace: options.Namespace,
				Offset:    offset,
				Limit:     uint32(limit),
			},
		})
	})
}

func (l *Loader) installSkill(
	ctx context.Context,
	options Options,
	ref adapterconfig.SkillRef,
	root string,
	force bool,
) (*model.InstalledSkill, error) {
	callCtx, cancel := skillCallContext(ctx, options.RequestTimeout)
	defer cancel()
	return callSkill(callCtx, func() (*model.InstalledSkill, error) {
		return l.skillAPI.Install(&api.SkillInstallRequest{
			SkillInstallRequest: model.SkillInstallRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Version:   ref.Version,
				CacheDir:  root,
				Force:     force,
			},
		})
	})
}

func skillCallContext(
	ctx context.Context,
	configured time.Duration,
) (context.Context, context.CancelFunc) {
	timeout, err := serviceRequestTimeout(ctx, configured)
	if err != nil {
		callCtx, cancel := context.WithCancel(ctx)
		cancel()
		return callCtx, cancel
	}
	return context.WithTimeout(ctx, timeout)
}

// polaris-go's SkillAPI is synchronous and has no Context parameters. This
// boundary makes Skill loading honor the caller's remaining deadline; the SDK's
// own message and download timeouts continue to bound an abandoned request.
func callSkill[T any](ctx context.Context, call func() (T, error)) (T, error) {
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := call()
		done <- result{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case output := <-done:
		if err := ctx.Err(); err != nil {
			var zero T
			return zero, err
		}
		return output.value, output.err
	}
}

func validateAgentServiceType(service *model.AgentService) error {
	if service == nil {
		return errors.New("polaris returned an empty Agent service")
	}
	if service.Type != model.ServiceTypeAIAgent {
		return fmt.Errorf(
			"service %q has type %s, expected %s",
			service.Name,
			service.Type,
			model.ServiceTypeAIAgent,
		)
	}
	return nil
}

func mergeLoadedSkillRefs(
	base []loadedSkillRef,
	explicit []adapterconfig.SkillRef,
) ([]loadedSkillRef, error) {
	byID := make(map[string]loadedSkillRef, len(base)+len(explicit))
	for _, item := range base {
		ref := trimSkillRef(item.ref)
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf("invalid service Skill reference %q: %w", ref.ID(), err)
		}
		item.ref = ref
		byID[ref.ID()] = item
	}
	for _, ref := range explicit {
		ref = trimSkillRef(ref)
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf("invalid configured Skill reference %q: %w", ref.ID(), err)
		}
		item := loadedSkillRef{ref: ref}
		if existing, ok := byID[ref.ID()]; ok && existing.skill != nil {
			item.skill = existing.skill
		}
		byID[ref.ID()] = item
	}

	result := make([]loadedSkillRef, 0, len(byID))
	for _, item := range byID {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ref.ID() < result[j].ref.ID()
	})
	return result, nil
}

func trimSkillRef(ref adapterconfig.SkillRef) adapterconfig.SkillRef {
	ref.Namespace = strings.TrimSpace(ref.Namespace)
	ref.Name = strings.TrimSpace(ref.Name)
	ref.Version = strings.TrimSpace(ref.Version)
	return ref
}

func validateInstalledSkill(
	root string,
	requested adapterconfig.SkillRef,
	installed *model.InstalledSkill,
) error {
	if installed == nil {
		return fmt.Errorf("installer returned an empty result for Skill %s", requested.ID())
	}
	if err := installedSkillMatches(requested, *installed); err != nil {
		return err
	}
	if len(installed.Digest) != 64 {
		return fmt.Errorf("installed Skill %s digest must be a SHA-256 hex string", requested.ID())
	}
	if _, err := hex.DecodeString(installed.Digest); err != nil {
		return fmt.Errorf("installed Skill %s digest must be a SHA-256 hex string: %w", requested.ID(), err)
	}
	path, err := filepath.Abs(installed.Dir)
	if err != nil {
		return fmt.Errorf("resolve installed Skill %s path: %w", requested.ID(), err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve Skill cache directory %q: %w", root, err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve installed Skill %s path: %w", requested.ID(), err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return fmt.Errorf("validate installed Skill %s path: %w", requested.ID(), err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("installed Skill %s path escapes cache directory", requested.ID())
	}
	installed.Dir = resolvedPath
	entryFile := installed.EntryFile
	if entryFile == "" {
		entryFile = agentskill.SkillFile
	}
	if filepath.IsAbs(entryFile) {
		return fmt.Errorf("installed Skill %s entry file must be relative", requested.ID())
	}
	entryFile = filepath.Clean(filepath.FromSlash(entryFile))
	installed.EntryFile = entryFile
	if filepath.Base(entryFile) != agentskill.SkillFile {
		return fmt.Errorf(
			"installed Skill %s entry file must be named %s",
			requested.ID(),
			agentskill.SkillFile,
		)
	}
	if entryFile == ".." || strings.HasPrefix(entryFile, ".."+string(filepath.Separator)) {
		return fmt.Errorf("installed Skill %s entry file escapes Skill directory", requested.ID())
	}
	skillPath := filepath.Join(resolvedPath, entryFile)
	info, err := os.Lstat(skillPath)
	if err != nil {
		return fmt.Errorf(
			"installed Skill %s is missing %s: %w",
			requested.ID(),
			agentskill.SkillFile,
			err,
		)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf(
			"installed Skill %s %s must be a regular file",
			requested.ID(),
			agentskill.SkillFile,
		)
	}
	return nil
}

func installedSkillMatches(
	requested adapterconfig.SkillRef,
	installed model.InstalledSkill,
) error {
	if installed.Namespace != requested.Namespace ||
		installed.Name != requested.Name ||
		installed.Version != requested.Version {
		return fmt.Errorf(
			"installer returned %s, expected %s",
			adapterconfig.SkillRef{
				Namespace: installed.Namespace,
				Name:      installed.Name,
				Version:   installed.Version,
			}.ID(),
			requested.ID(),
		)
	}
	return nil
}

func validateInstallLayouts(
	refs []loadedSkillRef,
	installed []model.InstalledSkill,
) ([]string, error) {
	if len(refs) != len(installed) {
		return nil, fmt.Errorf(
			"installer returned %d Skills for %d references",
			len(installed),
			len(refs),
		)
	}
	seen := make(map[string]struct{}, len(installed))
	for _, item := range installed {
		path, err := filepath.Abs(item.Dir)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("multiple Skill references resolve to %q", path)
		}
		seen[path] = struct{}{}
	}

	roots := make([]string, 0, len(installed))
	for index, item := range installed {
		found, err := findSkillFiles(item.Dir)
		if err != nil {
			return nil, err
		}
		entryPath := filepath.Join(item.Dir, item.EntryFile)
		if len(found) != 1 || found[0] != entryPath {
			return nil, fmt.Errorf(
				"installed Skill %s contains unexpected executable Skill entries; only %s is selected",
				item.Name,
				item.EntryFile,
			)
		}
		entryRoot := filepath.Dir(entryPath)
		entryRepository, err := agentskill.NewFSRepository(entryRoot)
		if err != nil {
			return nil, fmt.Errorf("read installed Skill %s entry: %w", refs[index].ref.ID(), err)
		}
		if len(entryRepository.Summaries()) != 1 {
			return nil, fmt.Errorf("installed Skill %s must expose one valid repository entry", refs[index].ref.ID())
		}
		actualPath, err := entryRepository.Path(refs[index].ref.Name)
		if err != nil || actualPath != entryRoot {
			return nil, fmt.Errorf("installed Skill %s entry name does not match requested name %q",
				refs[index].ref.ID(), refs[index].ref.Name)
		}
		roots = append(roots, entryRoot)
	}
	return roots, nil
}

func findSkillFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == agentskill.SkillFile {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan installed Skill directory %q: %w", root, err)
	}
	return files, nil
}

func agentSkillsFromRefs(refs []adapterconfig.SkillRef) []model.AgentSkill {
	skills := make([]model.AgentSkill, 0, len(refs))
	for _, ref := range refs {
		ref = trimSkillRef(ref)
		skills = append(skills, model.AgentSkill{
			ID:      ref.ID(),
			Name:    ref.Name,
			Version: ref.Version,
		})
	}
	return skills
}

func agentSkillsFromLoadedRefs(refs []loadedSkillRef) []model.AgentSkill {
	skills := make([]model.AgentSkill, 0, len(refs))
	for _, item := range refs {
		if item.skill != nil {
			skills = append(skills, cloneAgentSkill(*item.skill))
			continue
		}
		ref := trimSkillRef(item.ref)
		skills = append(skills, model.AgentSkill{
			ID:      ref.ID(),
			Name:    ref.Name,
			Version: ref.Version,
		})
	}
	return skills
}

func cloneAgentSkill(input model.AgentSkill) model.AgentSkill {
	input.Tags = append([]string(nil), input.Tags...)
	input.Examples = append([]string(nil), input.Examples...)
	input.InputModes = append([]string(nil), input.InputModes...)
	input.OutputModes = append([]string(nil), input.OutputModes...)
	return input
}

func skillRefsFromService(service *model.AgentService) ([]loadedSkillRef, error) {
	if service == nil {
		return nil, nil
	}
	refs := make([]loadedSkillRef, 0, len(service.ExtendedMetadata))
	for _, extended := range service.ExtendedMetadata {
		if extended == nil || extended.Type != model.ExtendedMetadataSkill {
			continue
		}
		if extended.AgentSkill == nil {
			return nil, fmt.Errorf("service %s contains an empty Skill extended metadata entry", service.Name)
		}
		namespace, name, parsedVersion, err := parseMountedSkillRef(*extended.AgentSkill)
		if err != nil {
			return nil, fmt.Errorf(
				"parse service Skill reference %q: %w",
				extended.AgentSkill.ID,
				err,
			)
		}
		version := strings.TrimSpace(extended.AgentSkill.Version)
		if version == "" {
			version = parsedVersion
		}
		if err := model.ValidateSkillRefParts(namespace, name, version); err != nil {
			return nil, fmt.Errorf(
				"validate service Skill reference %q version %q: %w",
				extended.AgentSkill.ID,
				version,
				err,
			)
		}
		skill := cloneAgentSkill(*extended.AgentSkill)
		skill.Version = version
		refs = append(refs, loadedSkillRef{
			ref:   adapterconfig.SkillRef{Namespace: namespace, Name: name, Version: version},
			skill: &skill,
		})
	}
	return refs, nil
}

func parseMountedSkillRef(skill model.AgentSkill) (string, string, string, error) {
	if strings.Contains(skill.ID, "@") {
		return model.ParseSkillRef(skill.ID)
	}
	namespace, name, ok := strings.Cut(skill.ID, ":")
	if !ok || strings.Contains(name, ":") {
		return "", "", "", fmt.Errorf("mounted Skill id %q must use namespace:name or namespace:name@version", skill.ID)
	}
	version := strings.TrimSpace(skill.Version)
	if err := model.ValidateSkillRefParts(namespace, name, version); err != nil {
		return "", "", "", err
	}
	return namespace, name, version, nil
}
