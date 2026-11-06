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
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
)

type swappedSkillInstaller struct{ api.SkillAPI }

func (swappedSkillInstaller) Install(request *api.SkillInstallRequest) (*model.InstalledSkill, error) {
	dir := filepath.Join(request.CacheDir, request.Name+"@"+request.Version)
	name := "weather"
	if request.Name == "weather" {
		name = "calendar"
	}
	content := []byte("---\nname: " + name + "\n---\n# " + name + "\n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), content, 0o644); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	return &model.InstalledSkill{
		Namespace: request.Namespace, Name: request.Name, Version: request.Version, Dir: dir,
		EntryFile: "SKILL.md", Digest: hex.EncodeToString(digest[:]),
	}, nil
}

func TestSkillRepositoryChecksEachReferenceEntryIdentity(t *testing.T) {
	repository, err := NewLoader(nil, swappedSkillInstaller{}).Load(context.Background(), Options{
		CacheDir: t.TempDir(),
		ExplicitRefs: []config.SkillRef{
			{Namespace: "Production", Name: "weather", Version: "1.0.0"},
			{Namespace: "Production", Name: "calendar", Version: "1.0.0"},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "entry name does not match")
	require.Nil(t, repository)
}

type mountingAgentAPI struct {
	api.AgentAPI
	before, after *model.AgentService
	readCalls     int
	request       *api.MountAgentSkillsRequest
}

type boundedReadAgentAPI struct{ api.AgentAPI }

func (boundedReadAgentAPI) GetService(request *api.GetAgentServiceRequest) (*model.AgentService, error) {
	if request.Timeout == nil || *request.Timeout <= 0 || *request.Timeout > 50*time.Millisecond ||
		request.RetryCount == nil || *request.RetryCount != 0 {
		return nil, errors.New("service read did not carry one bounded attempt")
	}
	return testutil.AgentServiceWithSkills(), nil
}

func TestSkillLoaderServiceReadDisablesInheritedRetries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := NewLoader(boundedReadAgentAPI{}, nil).Load(ctx, Options{
		Namespace: "Production", Service: "customer-agent", UseServiceSkills: true,
	})
	require.NoError(t, err)
}

func (api *mountingAgentAPI) GetService(*api.GetAgentServiceRequest) (*model.AgentService, error) {
	api.readCalls++
	return api.before, nil
}

func (agentAPI *mountingAgentAPI) MountSkills(request *api.MountAgentSkillsRequest) (*model.AgentService, error) {
	agentAPI.request = request
	return agentAPI.after, nil
}

func TestSkillLoaderReplaceMountDoesNotInstallRemovedReferences(t *testing.T) {
	old := config.SkillRef{Namespace: "Production", Name: "old-skill", Version: "1.0.0"}
	next := config.SkillRef{Namespace: "Production", Name: "new-skill", Version: "1.0.0"}
	agentAPI := &mountingAgentAPI{
		before: testutil.AgentServiceWithSkills(old),
		after:  testutil.AgentServiceWithSkills(next),
	}
	installer := &testutil.FakeSkillAPI{}
	repository, err := NewLoader(agentAPI, installer).Load(context.Background(), Options{
		Namespace: "Production", Service: "customer-agent", CacheDir: t.TempDir(),
		ExplicitRefs: []config.SkillRef{next}, UseServiceSkills: true, Mount: true, MountReplace: true,
	})
	require.NoError(t, err)
	require.Len(t, repository.Summaries(), 1)
	require.Equal(t, next.Name, repository.Summaries()[0].Name)
	require.Len(t, installer.Requests, 1)
	require.Zero(t, agentAPI.readCalls)
}

func TestSkillLoaderReplaceMountCanClearMalformedHistoryWithServiceSkillsEnabled(t *testing.T) {
	old := config.SkillRef{Namespace: "Production", Name: "old-skill"}
	agentAPI := &mountingAgentAPI{
		before: testutil.AgentServiceWithSkills(old),
		after:  testutil.AgentServiceWithSkills(),
	}
	repository, err := NewLoader(agentAPI, nil).Load(context.Background(), Options{
		Namespace: "Production", Service: "customer-agent",
		UseServiceSkills: true, Mount: true, MountReplace: true,
	})
	require.NoError(t, err)
	require.Nil(t, repository)
	require.Zero(t, agentAPI.readCalls)
	require.Empty(t, agentAPI.request.Skills)
}

func TestSkillLoaderMergeInstallsLatestMountedServiceSnapshot(t *testing.T) {
	stale := config.SkillRef{Namespace: "Production", Name: "removed", Version: "1.0.0"}
	current := config.SkillRef{Namespace: "Production", Name: "latest", Version: "2.0.0"}
	explicit := config.SkillRef{Namespace: "Production", Name: "calendar", Version: "1.0.0"}
	agentAPI := &mountingAgentAPI{
		before: testutil.AgentServiceWithSkills(stale),
		after:  testutil.AgentServiceWithSkills(current, explicit),
	}
	installer := &testutil.FakeSkillAPI{}
	repository, err := NewLoader(agentAPI, installer).Load(context.Background(), Options{
		Namespace: "Production", Service: "customer-agent", CacheDir: t.TempDir(),
		ExplicitRefs: []config.SkillRef{explicit}, UseServiceSkills: true, Mount: true,
	})
	require.NoError(t, err)
	require.Len(t, repository.Summaries(), 2)
	for _, request := range installer.Requests {
		require.NotEqual(t, stale.Name, request.Name)
	}
	require.Zero(t, agentAPI.readCalls)
	require.Equal(t, agentSkillsFromRefs([]config.SkillRef{explicit}), agentAPI.request.Skills)
}

func TestSkillLoaderMountRejectsMissingServiceResponse(t *testing.T) {
	_, err := NewLoader(&mountingAgentAPI{}, nil).Load(context.Background(), Options{
		Namespace: "Production", Service: "customer-agent", Mount: true,
	})
	require.ErrorContains(t, err, "empty Agent service")
}
