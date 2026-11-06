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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	adapterskill "trpc.group/trpc-go/trpc-agent-naming-polarismesh/skill"
)

func (s *suite) install(name, version string, force bool) (*model.InstalledSkill, error) {
	return s.skills.Install(&api.SkillInstallRequest{SkillInstallRequest: model.SkillInstallRequest{
		Namespace: s.opts.Namespace, Name: name, Version: version, CacheDir: filepath.Join(s.opts.RunDir, "skill-cache"), Force: force,
	}})
}

func (s *suite) createSkill(ctx context.Context, name, version string, publish, extra bool) error {
	s.state.Skills = appendUnique(s.state.Skills, name)
	if err := s.save(); err != nil {
		return err
	}
	if err := s.console.CreateSkill(ctx, s.opts, name, version, false, extra); err != nil {
		return err
	}
	if !publish {
		return nil
	}
	// Separate fixture draft and publish generations on second-resolution stores.
	timer := time.NewTimer(1200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if err := s.console.Publish(ctx, s.opts.Namespace, name, version); err != nil {
		return err
	}
	return s.waitPublishedSkill(ctx, name, version)
}

func (s *suite) waitPublishedSkill(ctx context.Context, name, version string) error {
	return Eventually(ctx, 30*time.Second, func() error {
		resource, err := s.skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
			Namespace: s.opts.Namespace, Name: name, Version: version,
		}})
		if err != nil {
			return err
		}
		if resource == nil || resource.Namespace != s.opts.Namespace || resource.Name != name || resource.Version != version {
			return errors.New("published Skill identity is not visible yet")
		}
		pkg, err := s.skills.DownloadSkill(&api.SkillDownloadRequest{SkillDownloadRequest: model.SkillDownloadRequest{
			Namespace: s.opts.Namespace, Name: name, Version: version, Format: model.SkillFormatZIP,
		}})
		if err != nil {
			return err
		}
		if pkg == nil || pkg.Format != model.SkillFormatZIP || len(pkg.Content) == 0 || len(pkg.Digest) != 64 {
			return errors.New("published Skill ZIP is not visible yet")
		}
		return nil
	})
}

func (s *suite) publishedSkills(ctx context.Context) error {
	var result error
	fixtures := []struct {
		name, version    string
		published, extra bool
	}{
		{s.skillA, "1.0.0", true, false}, {s.skillA, "2.0.0", false, false},
		{s.skillB, "1.0.0", true, false}, {s.skillDraft, "1.0.0", false, false},
		{s.skillMulti, "1.0.0", true, true},
	}
	for _, fixture := range fixtures {
		result = errors.Join(result, s.createSkill(ctx, fixture.name, fixture.version, fixture.published, fixture.extra))
	}
	resource, err := s.skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
		Namespace: s.opts.Namespace, Name: s.skillA, Version: "1.0.0",
	}})
	if err != nil {
		return errors.Join(result, err)
	}
	if resource.Name != s.skillA || resource.Namespace != s.opts.Namespace || resource.Version != "1.0.0" {
		return errors.New("real Skill metadata identity differs")
	}
	pkg, err := s.skills.DownloadSkill(&api.SkillDownloadRequest{SkillDownloadRequest: model.SkillDownloadRequest{
		Namespace: s.opts.Namespace, Name: s.skillA, Version: "1.0.0", Format: model.SkillFormatZIP,
	}})
	if err != nil {
		return err
	}
	if pkg.Format != model.SkillFormatZIP || pkg.ContentType != model.SkillContentTypeZIP || len(pkg.Content) == 0 || len(pkg.Digest) != 64 {
		return errors.New("real Skill ZIP content or digest is invalid")
	}
	s.firstInstall, err = s.install(s.skillA, "1.0.0", false)
	if err != nil {
		return err
	}
	if s.firstInstall.CacheHit || s.firstInstall.Version != "1.0.0" {
		return errors.New("first real install did not download the pinned version")
	}
	data, err := os.ReadFile(filepath.Join(s.firstInstall.Dir, s.firstInstall.EntryFile))
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "POLARIS_SKILL_VERSION=1.0.0") {
		return errors.New("installed Skill body differs from real published content")
	}
	_, err = os.Stat(filepath.Join(s.firstInstall.Dir, "references", "data.json"))
	return errors.Join(result, err)
}

func (s *suite) ref(name, version string) adapterconfig.SkillRef {
	return adapterconfig.SkillRef{Namespace: s.opts.Namespace, Name: name, Version: version}
}

func (s *suite) invalidSkills(ctx context.Context) error {
	for _, ref := range []adapterconfig.SkillRef{s.ref(SkillPrefix+s.opts.RunID+"-missing", "1.0.0"), s.ref(s.skillDraft, "1.0.0"), s.ref(s.skillA, "9.9.9")} {
		_, err := s.skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
			Namespace: ref.Namespace, Name: ref.Name, Version: ref.Version,
		}})
		if sdkErrorCode(err) != model.ErrCodeSkillNotFound {
			return fmt.Errorf("expected unavailable Skill for %s, got %v", ref.ID(), err)
		}
		repository, err := s.loader.Load(ctx, adapterskill.Options{ExplicitRefs: []adapterconfig.SkillRef{s.ref(s.skillA, "1.0.0"), ref},
			CacheDir: filepath.Join(s.opts.RunDir, "skill-cache"), RequestTimeout: s.opts.Timeout})
		if err == nil || repository != nil {
			return errors.New("failed real Skill load returned a partial repository")
		}
	}
	return nil
}

func (s *suite) skillCache(ctx context.Context) error {
	if s.firstInstall == nil {
		return errors.New("successful install prerequisite is missing")
	}
	hit, err := s.install(s.skillA, "1.0.0", false)
	if err != nil {
		return err
	}
	if !hit.CacheHit || hit.Dir != s.firstInstall.Dir || hit.Digest != s.firstInstall.Digest || !hit.Downloaded.Equal(s.firstInstall.Downloaded) {
		return errors.New("cache hit changed pinned installed content or download identity")
	}
	entry := filepath.Join(hit.Dir, hit.EntryFile)
	if err := os.WriteFile(entry, []byte("corrupted local cache"), 0o600); err != nil {
		return err
	}
	repaired, err := s.install(s.skillA, "1.0.0", false)
	if err != nil {
		return err
	}
	if repaired.CacheHit || repaired.Digest != hit.Digest {
		return errors.New("tampered real cache was not redownloaded and repaired")
	}
	data, err := os.ReadFile(filepath.Join(repaired.Dir, repaired.EntryFile))
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "POLARIS_SKILL_VERSION=1.0.0") {
		return errors.New("repaired cache still contains tampered content")
	}
	forced, err := s.install(s.skillA, "1.0.0", true)
	if err != nil {
		return err
	}
	if forced.CacheHit {
		return errors.New("forced install incorrectly returned a cache hit")
	}
	return ctx.Err()
}

func (s *suite) skillVersions(ctx context.Context) error {
	if err := s.console.Publish(ctx, s.opts.Namespace, s.skillA, "2.0.0"); err != nil {
		return err
	}
	var active *model.InstalledSkill
	if err := Eventually(ctx, 30*time.Second, func() error {
		installed, err := s.install(s.skillA, "", false)
		if err != nil {
			return err
		}
		if installed == nil || installed.Version != "2.0.0" {
			return errors.New("active install has not converged to 2.0.0")
		}
		data, err := os.ReadFile(filepath.Join(installed.Dir, installed.EntryFile))
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "POLARIS_SKILL_VERSION=2.0.0") {
			return errors.New("active Skill body differs from published version")
		}
		active = installed
		return nil
	}); err != nil {
		return err
	}
	pinned, err := s.install(s.skillA, "1.0.0", false)
	if err != nil {
		return err
	}
	if !pinned.CacheHit || active.Dir == pinned.Dir || pinned.Version != "1.0.0" {
		return errors.New("active version update changed or collided with the pinned cache")
	}
	return nil
}

func (s *suite) skillRepository(ctx context.Context) error {
	repository, err := s.loader.Load(ctx, adapterskill.Options{ExplicitRefs: []adapterconfig.SkillRef{
		s.ref(s.skillB, "1.0.0"), s.ref(s.skillA, "1.0.0"), s.ref(s.skillA, "1.0.0"),
	}, CacheDir: filepath.Join(s.opts.RunDir, "skill-cache"), RequestTimeout: s.opts.Timeout})
	if err != nil {
		return err
	}
	if repository == nil {
		return errors.New("real selected Skills returned no repository")
	}
	var names []string
	for _, summary := range repository.Summaries() {
		names = append(names, summary.Name)
		if _, err := repository.Get(summary.Name); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(names, []string{s.skillA, s.skillB}) {
		return fmt.Errorf("real repository did not deduplicate and sort selected Skills: %v", names)
	}
	return nil
}

func (s *suite) mount(service string, refs []adapterconfig.SkillRef, replace bool) (*model.AgentService, error) {
	values := make([]model.AgentSkill, 0, len(refs))
	for _, ref := range refs {
		values = append(values, model.AgentSkill{ID: ref.ID(), Name: ref.Name, Version: ref.Version})
	}
	return s.agents.MountSkills(&api.MountAgentSkillsRequest{MountAgentSkillsRequest: model.MountAgentSkillsRequest{
		Namespace: s.opts.Namespace, Service: service, Skills: values, Replace: replace,
	}})
}

func (s *suite) mergeMount(ctx context.Context) error {
	service := s.service("mount")
	if err := s.ensureService(ctx, service); err != nil {
		return err
	}
	if _, err := s.mount(service, []adapterconfig.SkillRef{s.ref(s.skillA, "1.0.0")}, true); err != nil {
		return err
	}
	// Read a discovery snapshot, then change Console through the real Writer.
	// Service creation confirms through Console before the separate Discover
	// stream has necessarily observed the new service.
	if err := Eventually(ctx, 15*time.Second, func() error {
		request := &api.GetAgentServiceRequest{GetAgentServiceRequest: model.GetAgentServiceRequest{
			Namespace: s.opts.Namespace, Service: service,
		}}
		request.SetTimeout(s.opts.Timeout)
		_, err := s.agents.GetService(request)
		return err
	}); err != nil {
		return fmt.Errorf("wait for real Discover service snapshot: %w", err)
	}
	if _, err := s.mount(service, []adapterconfig.SkillRef{s.ref(s.skillB, "1.0.0")}, true); err != nil {
		return err
	}
	repository, err := s.loader.Load(ctx, adapterskill.Options{Namespace: s.opts.Namespace, Service: service,
		CacheDir: filepath.Join(s.opts.RunDir, "skill-cache"), Mount: true, MountReplace: false, UseServiceSkills: true,
		ExplicitRefs: []adapterconfig.SkillRef{s.ref(s.skillA, "1.0.0")}, RequestTimeout: s.opts.Timeout})
	if err != nil {
		return err
	}
	if repository == nil || len(repository.Summaries()) != 2 {
		return errors.New("merge load did not use latest Console Skill references")
	}
	record, err := s.mount(service, nil, false)
	if err != nil {
		return err
	}
	want := []string{s.ref(s.skillA, "1.0.0").ID(), s.ref(s.skillB, "1.0.0").ID()}
	sort.Strings(want)
	if !reflect.DeepEqual(sortedIDs(record), want) {
		return errors.New("real merge mount lost latest references")
	}
	return nil
}

func (s *suite) replaceMount(ctx context.Context) error {
	service := s.service("replace")
	if err := s.ensureService(ctx, service); err != nil {
		return err
	}
	if _, err := s.mount(service, []adapterconfig.SkillRef{s.ref(s.skillA, "1.0.0"), s.ref(s.skillB, "1.0.0")}, true); err != nil {
		return err
	}
	options := adapterskill.Options{Namespace: s.opts.Namespace, Service: service,
		CacheDir: filepath.Join(s.opts.RunDir, "skill-cache"), Mount: true, MountReplace: true, UseServiceSkills: true,
		ExplicitRefs: []adapterconfig.SkillRef{s.ref(s.skillB, "1.0.0")}, RequestTimeout: s.opts.Timeout}
	repository, err := s.loader.Load(ctx, options)
	if err != nil {
		return err
	}
	if repository == nil || len(repository.Summaries()) != 1 || repository.Summaries()[0].Name != s.skillB {
		return errors.New("replace mount still exposes a removed Skill")
	}
	options.ExplicitRefs = nil
	repository, err = s.loader.Load(ctx, options)
	if err != nil {
		return err
	}
	if repository != nil {
		return errors.New("empty replace mount exposes stale cached Skills")
	}
	record, err := s.mount(service, nil, false)
	if err != nil {
		return err
	}
	if len(sortedIDs(record)) != 0 {
		return errors.New("real Console did not persist explicit empty Skill references")
	}
	return nil
}

func (s *suite) extraEntry(ctx context.Context) error {
	repository, err := s.loader.Load(ctx, adapterskill.Options{
		ExplicitRefs: []adapterconfig.SkillRef{s.ref(s.skillMulti, "1.0.0")}, CacheDir: filepath.Join(s.opts.RunDir, "skill-cache"), RequestTimeout: s.opts.Timeout})
	if err == nil || repository != nil {
		return errors.New("real multi-entry ZIP was accepted as an executable Skill repository")
	}
	if !strings.Contains(err.Error(), "SKILL.md") && !strings.Contains(err.Error(), "entry") {
		return fmt.Errorf("multi-entry failure was not a layout validation: %w", err)
	}
	return nil
}

func (s *suite) skillProvider(ctx context.Context) error {
	service := s.service("skill-provider")
	c, err := s.start(ctx, service, "skill-provider", "/a2a", "valid", s.skillA)
	if err != nil {
		return err
	}
	if err := s.wait(ctx, c, true); err != nil {
		return err
	}
	runtime, err := s.newRuntime(service)
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	remote, err := runtime.NewRemoteAgent(ctx, "skill-consumer", ClientFactory(s.transport, s.opts.Timeout, true, false))
	if err != nil {
		return err
	}
	if err := s.invoke(ctx, remote, "skill", "POLARIS_SKILL_VERSION=1.0.0"); err != nil {
		return err
	}
	return s.invoke(ctx, remote, "skill", s.skillA)
}
