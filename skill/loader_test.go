/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
)

func TestRuntimeLoadsAndMountsSkills(t *testing.T) {
	cacheDir := t.TempDir()
	serviceSkill := config.SkillRef{Namespace: "Production", Name: "weather", Version: "1.0.0"}
	explicitSkill := config.SkillRef{Namespace: "Production", Name: "calendar", Version: "2.0.0"}
	agentAPI := &testutil.FakeAgentAPI{Service: testutil.AgentServiceWithSkills(serviceSkill, serviceSkill)}
	skillAPI := &testutil.FakeSkillAPI{}
	loader := &Loader{agentAPI: agentAPI, skillAPI: skillAPI}

	repository, err := loader.Load(context.Background(), Options{
		Namespace:        "Production",
		Service:          "customer-agent",
		CacheDir:         cacheDir,
		ExplicitRefs:     []config.SkillRef{explicitSkill, serviceSkill},
		UseServiceSkills: true,
		Mount:            true,
		MountReplace:     true,
	})
	require.NoError(t, err)
	require.NotNil(t, repository)
	require.Len(t, repository.Summaries(), 2)
	require.Len(t, skillAPI.Requests, 2)
	require.Equal(t, explicitSkill.Name, skillAPI.Requests[0].Name)
	require.Equal(t, serviceSkill.Name, skillAPI.Requests[1].Name)

	require.NotNil(t, agentAPI.MountRequest)
	require.True(t, agentAPI.MountRequest.Replace)
	require.Equal(t, agentSkillsFromRefs([]config.SkillRef{explicitSkill, serviceSkill}), agentAPI.MountRequest.Skills)
}

func TestSkillRepositoryUsesRequestedVersionOnly(t *testing.T) {
	root := t.TempDir()
	installer := &testutil.FakeSkillAPI{}
	_, err := installer.Install(&api.SkillInstallRequest{
		SkillInstallRequest: model.SkillInstallRequest{
			Namespace: "Production", Name: "weather", Version: "1.0.0", CacheDir: root,
		},
	})
	require.NoError(t, err)
	loader := &Loader{skillAPI: installer}

	repository, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		CacheDir:     root,
		ExplicitRefs: []config.SkillRef{{Namespace: "Production", Name: "weather", Version: "2.0.0"}},
	})
	require.NoError(t, err)

	actual, err := repository.Path("weather")
	require.NoError(t, err)
	expected, err := filepath.EvalSymlinks(filepath.Join(root, "Production", "weather@2.0.0"))
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestSkillRepositoryExcludesUnselectedCacheEntries(t *testing.T) {
	root := t.TempDir()
	installer := &testutil.FakeSkillAPI{}
	_, err := installer.Install(&api.SkillInstallRequest{
		SkillInstallRequest: model.SkillInstallRequest{
			Namespace: "Production", Name: "weather", Version: "1.0.0", CacheDir: root,
		},
	})
	require.NoError(t, err)
	loader := &Loader{skillAPI: installer}

	repository, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		CacheDir:     root,
		ExplicitRefs: []config.SkillRef{{Namespace: "Production", Name: "calendar", Version: "2.0.0"}},
	})
	require.NoError(t, err)

	summaries := repository.Summaries()
	require.Len(t, summaries, 1)
	require.Equal(t, "calendar", summaries[0].Name)
}

func TestSkillRepositoryRejectsNameCollision(t *testing.T) {
	loader := &Loader{skillAPI: &testutil.FakeSkillAPI{}}
	_, err := loader.Load(context.Background(), Options{
		Namespace: "Production",
		Service:   "customer-agent",
		CacheDir:  t.TempDir(),
		ExplicitRefs: []config.SkillRef{
			{Namespace: "Production", Name: "weather", Version: "1.0.0"},
			{Namespace: "Staging", Name: "weather", Version: "1.0.0"},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Skill names must be unique")
}

func TestSkillLoaderMountsWithoutInstallingServiceSkills(t *testing.T) {
	existing := config.SkillRef{Namespace: "Production", Name: "existing", Version: "1.0.0"}
	agentAPI := &testutil.FakeAgentAPI{Service: testutil.AgentServiceWithSkills(existing)}
	loader := &Loader{agentAPI: agentAPI}

	repository, err := loader.Load(context.Background(), Options{
		Namespace: "Production",
		Service:   "customer-agent",
		Mount:     true,
	})
	require.NoError(t, err)
	require.Nil(t, repository)
	require.Empty(t, agentAPI.MountRequest.Skills)
}

func TestSkillLoaderReplaceMountUsesOnlyExplicitRefs(t *testing.T) {
	existing := config.SkillRef{Namespace: "Production", Name: "old-skill", Version: "1.0.0"}
	explicit := config.SkillRef{Namespace: "Production", Name: "new-skill", Version: "2.0.0"}
	agentAPI := &testutil.FakeAgentAPI{Service: testutil.AgentServiceWithSkills(existing)}
	loader := &Loader{agentAPI: agentAPI, skillAPI: &testutil.FakeSkillAPI{}}

	_, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		CacheDir:     t.TempDir(),
		ExplicitRefs: []config.SkillRef{explicit},
		Mount:        true,
		MountReplace: true,
	})
	require.NoError(t, err)
	require.Equal(t, agentSkillsFromRefs([]config.SkillRef{explicit}), agentAPI.MountRequest.Skills)
}

func TestSkillLoaderMountDoesNotReplayCachedSummary(t *testing.T) {
	existing := config.SkillRef{Namespace: "Production", Name: "weather", Version: "1.0.0"}
	service := testutil.AgentServiceWithSkills(existing)
	service.ExtendedMetadata[0].AgentSkill.Description = "Weather lookup"
	service.ExtendedMetadata[0].AgentSkill.Tags = []string{"weather", "forecast"}
	agentAPI := &testutil.FakeAgentAPI{Service: service}
	loader := &Loader{agentAPI: agentAPI, skillAPI: &testutil.FakeSkillAPI{}}

	_, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		CacheDir:     t.TempDir(),
		ExplicitRefs: []config.SkillRef{existing},
		Mount:        true,
	})
	require.NoError(t, err)
	require.Empty(t, agentAPI.MountRequest.Skills[0].Description)
	require.Empty(t, agentAPI.MountRequest.Skills[0].Tags)
}

func TestSkillLoaderRejectsInvalidServiceReference(t *testing.T) {
	loader := &Loader{}
	_, err := loader.Load(context.Background(), Options{
		Namespace:        "Production",
		Service:          "customer-agent",
		CacheDir:         t.TempDir(),
		UseServiceSkills: true,
	})
	require.ErrorIs(t, err, adaptererrors.ErrAgentServiceAPIUnsupported)
}

func TestSkillLoaderRejectsInvalidServiceRef(t *testing.T) {
	agentAPI := &testutil.FakeAgentAPI{Service: testutil.AgentServiceWithSkills(config.SkillRef{Namespace: "Production", Name: "weather"})}
	loader := &Loader{agentAPI: agentAPI}
	_, err := loader.Load(context.Background(), Options{
		Namespace:        "Production",
		Service:          "customer-agent",
		CacheDir:         t.TempDir(),
		UseServiceSkills: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse service Skill reference")
}

func TestSkillLoaderRejectsEscapingInstallPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	skillAPI := &testutil.FakeSkillAPI{Path: outside}
	loader := &Loader{skillAPI: skillAPI}
	_, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		CacheDir:     root,
		ExplicitRefs: []config.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes cache directory")
}

func TestSkillLoaderRejectsSymlinkSkillFile(t *testing.T) {
	root := t.TempDir()
	skillAPI := &symlinkSkillInstaller{}
	loader := &Loader{skillAPI: skillAPI}
	_, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		CacheDir:     root,
		ExplicitRefs: []config.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be a regular file")
}

func TestSkillLoaderAcceptsNestedSkillLayout(t *testing.T) {
	root := t.TempDir()
	skillAPI := &nestedSkillAPI{}
	loader := &Loader{skillAPI: skillAPI}
	repository, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		CacheDir:     root,
		ExplicitRefs: []config.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}},
	})
	require.NoError(t, err)
	require.Len(t, repository.Summaries(), 1)
}

func TestSkillRefsFromServiceIgnoresUnknownExtendedMetadata(t *testing.T) {
	known := config.SkillRef{Namespace: "Production", Name: "weather", Version: "1.0.0"}
	service := testutil.AgentServiceWithSkills(known)
	service.ExtendedMetadata = append([]*model.ExtendedMetadata{{
		Type: model.ExtendedMetadataUnknown,
	}}, service.ExtendedMetadata...)

	refs, err := skillRefsFromService(service)
	require.NoError(t, err)
	require.Equal(t, known, refs[0].ref)
}

type extraEntrySkillInstaller struct {
	api.SkillAPI
}

func (installer extraEntrySkillInstaller) Install(
	request *api.SkillInstallRequest,
) (*model.InstalledSkill, error) {
	dir := filepath.Join(request.CacheDir, request.Namespace, request.Name+"@"+request.Version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	content := []byte("---\nname: weather\n---\n# Weather\n")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), content, 0o644); err != nil {
		return nil, err
	}
	if request.Namespace == "Production" {
		extra := filepath.Join(dir, "unselected")
		if err := os.MkdirAll(extra, 0o755); err != nil {
			return nil, err
		}
		extraContent := []byte("---\nname: unselected\n---\n# Extra\n")
		if err := os.WriteFile(filepath.Join(extra, "SKILL.md"), extraContent, 0o644); err != nil {
			return nil, err
		}
	}
	digest := sha256.Sum256(content)
	return &model.InstalledSkill{
		Namespace: request.Namespace,
		Name:      request.Name,
		Version:   request.Version,
		Dir:       dir,
		Digest:    hex.EncodeToString(digest[:]),
		EntryFile: "SKILL.md",
	}, nil
}

func TestSkillRepositoryRejectsExtraExecutableEntry(t *testing.T) {
	loader := NewLoader(nil, extraEntrySkillInstaller{})
	_, err := loader.Load(context.Background(), Options{
		Namespace: "Production",
		Service:   "customer-agent",
		CacheDir:  t.TempDir(),
		ExplicitRefs: []config.SkillRef{
			{Namespace: "Production", Name: "weather", Version: "1.0.0"},
			{Namespace: "Staging", Name: "weather", Version: "1.0.0"},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected executable Skill entries")
}

func TestSkillLoaderMergeMountSubmitsOnlyExplicitChanges(t *testing.T) {
	old := config.SkillRef{Namespace: "Production", Name: "removed", Version: "1.0.0"}
	next := config.SkillRef{Namespace: "Production", Name: "calendar", Version: "2.0.0"}
	agentAPI := &testutil.FakeAgentAPI{Service: testutil.AgentServiceWithSkills(old)}
	loader := NewLoader(agentAPI, &testutil.FakeSkillAPI{})

	_, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		CacheDir:     t.TempDir(),
		ExplicitRefs: []config.SkillRef{next},
		Mount:        true,
	})
	require.NoError(t, err)
	require.Equal(t, agentSkillsFromRefs([]config.SkillRef{next}), agentAPI.MountRequest.Skills)
}

func TestSkillLoaderReplaceMountRepairsInvalidReference(t *testing.T) {
	invalid := config.SkillRef{Namespace: "Production", Name: "old"}
	agentAPI := &testutil.FakeAgentAPI{Service: testutil.AgentServiceWithSkills(invalid)}
	loader := NewLoader(agentAPI, nil)

	_, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		Service:      "customer-agent",
		Mount:        true,
		MountReplace: true,
	})
	require.NoError(t, err)
	require.NotNil(t, agentAPI.MountRequest)
	require.Empty(t, agentAPI.MountRequest.Skills)
}

type deadlineRecordingAgentAPI struct {
	api.AgentAPI
	timeout *time.Duration
}

func (agentAPI *deadlineRecordingAgentAPI) GetService(
	request *api.GetAgentServiceRequest,
) (*model.AgentService, error) {
	agentAPI.timeout = request.Timeout
	time.Sleep(75 * time.Millisecond)
	return testutil.AgentServiceWithSkills(), nil
}

func TestSkillLoaderBoundsServiceReadByDeadline(t *testing.T) {
	agentAPI := &deadlineRecordingAgentAPI{}
	loader := NewLoader(agentAPI, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := loader.Load(ctx, Options{
		Namespace:        "Production",
		Service:          "customer-agent",
		UseServiceSkills: true,
	})
	require.NotNil(t, agentAPI.timeout)
	require.LessOrEqual(t, *agentAPI.timeout, 5*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

type nestedSkillAPI struct {
	api.SkillAPI
}

func (s *nestedSkillAPI) Install(
	request *api.SkillInstallRequest,
) (*model.InstalledSkill, error) {
	dir := filepath.Join(request.CacheDir, request.Namespace, request.Name+"@"+request.Version)
	nested := filepath.Join(dir, "weather")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		return nil, err
	}
	content := []byte("---\nname: weather\n---\n# Weather\n")
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), content, 0o644); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	return &model.InstalledSkill{
		Namespace: request.Namespace,
		Name:      request.Name,
		Version:   request.Version,
		Dir:       dir,
		Digest:    hex.EncodeToString(digest[:]),
		EntryFile: "weather/SKILL.md",
	}, nil
}

type symlinkSkillInstaller struct {
	api.SkillAPI
}

func (s *symlinkSkillInstaller) Install(
	request *api.SkillInstallRequest,
) (*model.InstalledSkill, error) {
	path := filepath.Join(request.CacheDir, request.Namespace, request.Name+"@"+request.Version)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	target := filepath.Join(request.CacheDir, "outside.md")
	if err := os.WriteFile(target, []byte("# outside\n"), 0o644); err != nil {
		return nil, err
	}
	if err := os.Symlink(target, filepath.Join(path, "SKILL.md")); err != nil {
		return nil, err
	}
	return &model.InstalledSkill{
		Namespace: request.Namespace,
		Name:      request.Name,
		Version:   request.Version,
		Dir:       path,
		Digest:    strings.Repeat("a", 64),
		EntryFile: "SKILL.md",
	}, nil
}
