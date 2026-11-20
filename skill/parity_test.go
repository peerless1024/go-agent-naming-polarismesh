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
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	agentapitest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentapitest"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
)

func TestNamesModeSkipsPublishedList(t *testing.T) {
	skillAPI := &testutil.FakeSkillAPI{}
	loader := NewLoader(nil, skillAPI)
	repository, err := loader.Load(context.Background(), Options{
		Namespace:    "Production",
		CacheDir:     t.TempDir(),
		Mode:         adapterconfig.SkillModeNames,
		ExplicitRefs: []adapterconfig.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}},
	})
	require.NoError(t, err)
	require.NotNil(t, repository)
	require.Empty(t, skillAPI.ListRequests)
	require.Len(t, skillAPI.Requests, 1)
	require.Equal(t, "weather", skillAPI.Requests[0].Name)
}

func TestPublishedModePaginatesAndCapsSkills(t *testing.T) {
	skillAPI := &testutil.FakeSkillAPI{
		ListResults: []*api.SkillListResult{
			{SkillListResult: model.SkillListResult{Total: 3, Resources: []*model.SkillResource{
				{Name: "a", Namespace: "Production", VersionInfo: &model.SkillVersionInfo{ActiveVersion: "1.0.0"}},
				{Name: "b", Namespace: "Production", VersionInfo: &model.SkillVersionInfo{ActiveVersion: "1.0.0"}},
			}}},
			{SkillListResult: model.SkillListResult{Total: 3, Resources: []*model.SkillResource{
				{Name: "c", Namespace: "Production", VersionInfo: &model.SkillVersionInfo{ActiveVersion: "2.0.0"}},
			}}},
		},
	}
	loader := NewLoader(nil, skillAPI)
	repository, err := loader.Load(context.Background(), Options{
		Namespace: "Production",
		CacheDir:  t.TempDir(),
		Mode:      adapterconfig.SkillModePublished,
		ListLimit: 2,
		MaxSkills: 2,
	})
	require.NoError(t, err)
	require.NotNil(t, repository)
	require.Len(t, skillAPI.ListRequests, 1)
	require.Equal(t, uint32(2), skillAPI.ListRequests[0].Limit)
	require.Len(t, skillAPI.Requests, 2)
	require.Equal(t, "a", skillAPI.Requests[0].Name)
	require.Equal(t, "b", skillAPI.Requests[1].Name)
}

func TestSkillFailureThresholdAndCacheMaxAge(t *testing.T) {
	t.Run("threshold allows partial repository", func(t *testing.T) {
		skillAPI := &testutil.FakeSkillAPI{FailNames: map[string]error{"broken": errors.New("broken package")}}
		loader := NewLoader(nil, skillAPI)
		repository, err := loader.Load(context.Background(), Options{
			Namespace: "Production",
			CacheDir:  t.TempDir(),
			Mode:      adapterconfig.SkillModeNames,
			ExplicitRefs: []adapterconfig.SkillRef{
				{Namespace: "Production", Name: "broken", Version: "1.0.0"},
				{Namespace: "Production", Name: "good", Version: "1.0.0"},
			},
			FailureThreshold: 1,
		})
		require.NoError(t, err)
		require.Len(t, repository.Summaries(), 1)
		require.Equal(t, "good", repository.Summaries()[0].Name)
	})

	t.Run("zero threshold fails fast", func(t *testing.T) {
		skillAPI := &testutil.FakeSkillAPI{FailNames: map[string]error{"broken": errors.New("broken package")}}
		loader := NewLoader(nil, skillAPI)
		_, err := loader.Load(context.Background(), Options{
			Namespace:    "Production",
			CacheDir:     t.TempDir(),
			Mode:         adapterconfig.SkillModeNames,
			ExplicitRefs: []adapterconfig.SkillRef{{Namespace: "Production", Name: "broken", Version: "1.0.0"}},
		})
		require.Error(t, err)
	})

	t.Run("stale cache is forced", func(t *testing.T) {
		skillAPI := &testutil.FakeSkillAPI{CacheHit: true, Downloaded: time.Now().Add(-2 * time.Hour)}
		loader := NewLoader(nil, skillAPI)
		_, err := loader.Load(context.Background(), Options{
			Namespace:    "Production",
			CacheDir:     t.TempDir(),
			Mode:         adapterconfig.SkillModeNames,
			ExplicitRefs: []adapterconfig.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}},
			CacheMaxAge:  time.Hour,
		})
		require.NoError(t, err)
		require.Len(t, skillAPI.Requests, 2)
		require.True(t, skillAPI.Requests[1].Force)
	})
}

func TestMountedSkillVersionOverridesReferenceVersion(t *testing.T) {
	agentAPI := &agentapitest.FakeAgentAPI{Service: &model.AgentService{
		Namespace: "Production",
		Name:      "customer-agent",
		Type:      model.ServiceTypeAIAgent,
		ExtendedMetadata: []*model.ExtendedMetadata{{
			Type: model.ExtendedMetadataSkill,
			AgentSkill: &model.AgentSkill{
				ID:      "Production:weather@1.0.0",
				Name:    "weather",
				Version: "2.0.0",
			},
		}},
	}}
	skillAPI := &testutil.FakeSkillAPI{}
	loader := NewLoader(agentAPI, skillAPI)
	_, err := loader.Load(context.Background(), Options{
		Namespace:        "Production",
		Service:          "customer-agent",
		CacheDir:         t.TempDir(),
		Mode:             adapterconfig.SkillModeMounted,
		UseServiceSkills: true,
	})
	require.NoError(t, err)
	require.Len(t, skillAPI.Requests, 1)
	require.Equal(t, "2.0.0", skillAPI.Requests[0].Version)
}

func TestPeriodicRepositorySwapsCompleteSnapshot(t *testing.T) {
	rootA := writeSkill(t, "a")
	rootB := writeSkill(t, "b")
	repoA, err := agentskill.NewFSRepository(rootA)
	require.NoError(t, err)
	repoB, err := agentskill.NewFSRepository(rootB)
	require.NoError(t, err)
	var calls int
	repository := NewPeriodicRepository(
		context.Background(),
		repoA,
		0,
		time.Second,
		func(context.Context) (agentskill.Repository, error) {
			calls++
			return repoB, nil
		},
	)
	require.Equal(t, []agentskill.Summary{{Name: "a"}}, repository.Summaries())
	require.NoError(t, repository.Refresh(context.Background()))
	require.Equal(t, []agentskill.Summary{{Name: "b"}}, repository.Summaries())
	require.Equal(t, 1, calls)
	require.NoError(t, repository.Close())
}

func TestPeriodicRepositoryCommitsSuccessfulEmptySnapshot(t *testing.T) {
	ref := adapterconfig.SkillRef{Namespace: "Production", Name: "old-skill", Version: "1.0.0"}
	agentAPI := &agentapitest.FakeAgentAPI{Service: testutil.AgentServiceWithSkills(ref)}
	loader := NewLoader(agentAPI, &testutil.FakeSkillAPI{})
	options := Options{
		Namespace: "Production",
		Service:   "customer-agent",
		CacheDir:  t.TempDir(),
		Mode:      adapterconfig.SkillModeMounted,
	}
	initial, err := loader.Load(context.Background(), options)
	require.NoError(t, err)
	require.NotNil(t, initial)
	repository := NewPeriodicRepository(
		context.Background(),
		initial,
		0,
		time.Second,
		func(ctx context.Context) (agentskill.Repository, error) {
			return loader.Load(ctx, options)
		},
	)
	t.Cleanup(func() { require.NoError(t, repository.Close()) })

	agentAPI.Service = testutil.AgentServiceWithSkills()
	require.NoError(t, repository.Refresh(context.Background()))
	require.Empty(t, repository.Summaries())
	_, err = repository.Get(ref.Name)
	require.Error(t, err)
	_, err = repository.Path(ref.Name)
	require.Error(t, err)
}

func TestPeriodicRepositorySerializesOverlappingRefreshes(t *testing.T) {
	repoA, err := agentskill.NewFSRepository(writeSkill(t, "a"))
	require.NoError(t, err)
	repoB, err := agentskill.NewFSRepository(writeSkill(t, "b"))
	require.NoError(t, err)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	repository := NewPeriodicRepository(
		context.Background(),
		repoA,
		0,
		time.Second,
		func(context.Context) (agentskill.Repository, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-release
				return repoA, nil
			}
			return repoB, nil
		},
	)
	t.Cleanup(func() { require.NoError(t, repository.Close()) })

	firstDone := make(chan error, 1)
	go func() { firstDone <- repository.Refresh(context.Background()) }()
	<-started
	secondDone := make(chan error, 1)
	go func() { secondDone <- repository.Refresh(context.Background()) }()
	select {
	case <-secondDone:
		t.Fatal("overlapping refresh bypassed the commit order gate")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	require.Equal(t, []agentskill.Summary{{Name: "b"}}, repository.Summaries())
}

func TestPeriodicRepositoryKeepsSnapshotOnReloadFailure(t *testing.T) {
	repoA, err := agentskill.NewFSRepository(writeSkill(t, "a"))
	require.NoError(t, err)
	reloadErr := errors.New("reload failed")
	repository := NewPeriodicRepository(
		context.Background(),
		repoA,
		0,
		time.Second,
		func(context.Context) (agentskill.Repository, error) {
			return nil, reloadErr
		},
	)
	t.Cleanup(func() { require.NoError(t, repository.Close()) })
	require.ErrorIs(t, repository.Refresh(context.Background()), reloadErr)
	require.Equal(t, []agentskill.Summary{{Name: "a"}}, repository.Summaries())
}

func TestPeriodicRepositoryCancelsQueuedRefresh(t *testing.T) {
	repoA, err := agentskill.NewFSRepository(writeSkill(t, "a"))
	require.NoError(t, err)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	repository := NewPeriodicRepository(
		context.Background(),
		repoA,
		0,
		time.Second,
		func(context.Context) (agentskill.Repository, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-release
			}
			return repoA, nil
		},
	)
	t.Cleanup(func() { require.NoError(t, repository.Close()) })
	firstDone := make(chan error, 1)
	go func() { firstDone <- repository.Refresh(context.Background()) }()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, repository.Refresh(ctx), context.DeadlineExceeded)
	close(release)
	require.NoError(t, <-firstDone)
	require.EqualValues(t, 1, calls.Load())
}

func TestPeriodicRepositoryCloseCancelsAndDrainsManualRefresh(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	repository := NewPeriodicRepository(
		context.Background(),
		nil,
		time.Hour,
		time.Minute,
		func(ctx context.Context) (agentskill.Repository, error) {
			close(started)
			select {
			case <-ctx.Done():
				close(canceled)
				return nil, ctx.Err()
			case <-release:
				return nil, nil
			}
		},
	)
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- repository.Refresh(context.Background()) }()
	<-started

	require.NoError(t, repository.Close())
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Close returned without canceling the active manual refresh")
	}
	require.ErrorIs(t, <-refreshDone, context.Canceled)
}

func TestPeriodicRepositoryCanceledReloadKeepsSnapshot(t *testing.T) {
	repoA, err := agentskill.NewFSRepository(writeSkill(t, "a"))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	repository := NewPeriodicRepository(
		context.Background(),
		repoA,
		0,
		time.Minute,
		func(context.Context) (agentskill.Repository, error) {
			cancel()
			return nil, nil
		},
	)
	t.Cleanup(func() { require.NoError(t, repository.Close()) })

	require.ErrorIs(t, repository.Refresh(ctx), context.Canceled)
	require.Equal(t, []agentskill.Summary{{Name: "a"}}, repository.Summaries())
}

func writeSkill(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(root, 0o755))
	content := []byte("---\nname: " + name + "\n---\n# " + name + "\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, agentskill.SkillFile), content, 0o644))
	return root
}
