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

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
)

// Options configures Skill installation and service-level mounting.
type Options struct {
	Namespace        string
	Service          string
	CacheDir         string
	RequestTimeout   time.Duration
	ExplicitRefs     []adapterconfig.SkillRef
	UseServiceSkills bool
	Mount            bool
	MountReplace     bool
}

// Loader installs configured Skills and builds the executable repository.
type Loader struct {
	agentAPI api.AgentAPI
	skillAPI api.SkillAPI
}

// NewLoader creates a Skill loader with the supplied Polaris APIs.
func NewLoader(agentAPI api.AgentAPI, skillAPI api.SkillAPI) *Loader {
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
	var serviceRefs []loadedSkillRef
	var service *model.AgentService
	if options.UseServiceSkills && !options.Mount {
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
		service, err = l.agentAPI.GetService(&api.GetAgentServiceRequest{
			GetAgentServiceRequest: model.GetAgentServiceRequest{
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
	if options.UseServiceSkills && !options.Mount {
		if service != nil {
			refs, err := skillRefsFromService(service)
			if err != nil {
				return nil, err
			}
			serviceRefs = append(serviceRefs, refs...)
		}
	}

	var installRefs []loadedSkillRef
	if options.UseServiceSkills {
		installRefs = append(installRefs, serviceRefs...)
	}
	installRefs, err := mergeLoadedSkillRefs(installRefs, options.ExplicitRefs)
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
		service, err = l.agentAPI.MountSkills(&api.MountAgentSkillsRequest{
			MountAgentSkillsRequest: model.MountAgentSkillsRequest{
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
		if options.UseServiceSkills && !options.MountReplace {
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
	for _, loaded := range installRefs {
		ref := loaded.ref
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := l.skillAPI.Install(&api.SkillInstallRequest{
			SkillInstallRequest: model.SkillInstallRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Version:   ref.Version,
				CacheDir:  root,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("install Skill %s: %w", ref.ID(), err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateInstalledSkill(root, ref, result); err != nil {
			return nil, err
		}
		installed = append(installed, *result)
	}
	roots, err := validateInstallLayouts(installRefs, installed)
	if err != nil {
		return nil, err
	}

	repository, err := agentskill.NewFSRepository(roots...)
	if err != nil {
		return nil, fmt.Errorf("scan installed Skill directories: %w", err)
	}
	if len(repository.Summaries()) != len(installed) {
		refs := make([]string, 0, len(installRefs))
		for _, loaded := range installRefs {
			refs = append(refs, loaded.ref.ID())
		}
		return nil, fmt.Errorf(
			"installed Skills expose %d repository entries for %d references (%s); Skill names must be unique",
			len(repository.Summaries()),
			len(installed),
			strings.Join(refs, ", "),
		)
	}
	for _, loaded := range installRefs {
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
		namespace, name, version, err := model.ParseSkillRef(extended.AgentSkill.ID)
		if err != nil {
			return nil, fmt.Errorf(
				"parse service Skill reference %q: %w",
				extended.AgentSkill.ID,
				err,
			)
		}
		skill := cloneAgentSkill(*extended.AgentSkill)
		refs = append(refs, loadedSkillRef{
			ref:   adapterconfig.SkillRef{Namespace: namespace, Name: name, Version: version},
			skill: &skill,
		})
	}
	return refs, nil
}
