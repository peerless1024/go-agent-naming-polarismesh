/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	namingtest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil/naming"
)

type cancelAfterErrCheck struct {
	context.Context
	cancel context.CancelFunc
	armed  atomic.Bool
}

func (c *cancelAfterErrCheck) Err() error {
	err := c.Context.Err()
	if c.armed.CompareAndSwap(true, false) {
		c.cancel()
	}
	return err
}

type emptyPublishedAPI struct {
	api.SkillAPI
	once  sync.Once
	edge  *cancelAfterErrCheck
	calls atomic.Int32
}

func (a *emptyPublishedAPI) GetSkillListResult(*api.GetSkillListRequest) (*api.SkillListResult, error) {
	a.calls.Add(1)
	a.once.Do(func() { a.edge.armed.Store(true) })
	return &api.SkillListResult{SkillListResult: model.SkillListResult{}}, nil
}

func TestRuntimeStartsSkillRefreshOnlyAfterPrepareCommits(t *testing.T) {
	for _, registryEnabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "registered", false: "unregistered"}[registryEnabled], func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			edge := &cancelAfterErrCheck{Context: parent, cancel: cancel}
			cfg := testutil.ProviderConfig()
			cfg.RegistryEnabled = registryEnabled
			cfg.SkillMode = adapterconfig.SkillModePublished
			cfg.SkillCacheDir = t.TempDir()
			cfg.SkillListRefresh = 10 * time.Millisecond
			installer := &emptyPublishedAPI{edge: edge}
			registrar := namingtest.NewFakeMetadataRegistrar()
			runtime, err := New(WithConfig(cfg), WithSDKContext(&testutil.FakeSDKContext{}),
				WithSkillAPI(installer), WithMetadataRegistrar(registrar))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
			require.ErrorIs(t, runtime.Prepare(edge), context.Canceled)
			require.Nil(t, runtime.skillRefresh)
			_, registered := registrar.Provider(cfg.ServiceName)
			require.False(t, registered)
			time.Sleep(40 * time.Millisecond)
			require.EqualValues(t, 1, installer.calls.Load(), "failed Prepare retained a refresh loop")
			require.NoError(t, runtime.Prepare(context.Background()))
			require.NotNil(t, runtime.skillRefresh)
			require.Eventually(t, func() bool { return installer.calls.Load() > 2 }, time.Second, time.Millisecond)
			require.NoError(t, runtime.Close(context.Background()))
			calls := installer.calls.Load()
			time.Sleep(40 * time.Millisecond)
			require.Equal(t, calls, installer.calls.Load(), "Close left a refresh loop active")
			require.Error(t, runtime.skillRefresh.Refresh(context.Background()))
		})
	}
}

type retryPublishedAPI struct {
	api.SkillAPI
	calls atomic.Int32
	err   error
}

func (a *retryPublishedAPI) GetSkillListResult(*api.GetSkillListRequest) (*api.SkillListResult, error) {
	a.calls.Add(1)
	return &api.SkillListResult{}, a.err
}

func TestRuntimeSkillRefreshNotStartedOnLoadFailureOrEarlyCancellation(t *testing.T) {
	for _, registryEnabled := range []bool{true, false} {
		for _, earlyCancellation := range []bool{true, false} {
			name := map[bool]string{true: "registered", false: "unregistered"}[registryEnabled]
			name += "/" + map[bool]string{true: "early cancellation", false: "load failure"}[earlyCancellation]
			t.Run(name, func(t *testing.T) {
				cfg := testutil.ProviderConfig()
				cfg.RegistryEnabled = registryEnabled
				cfg.SkillMode = adapterconfig.SkillModePublished
				cfg.SkillCacheDir = t.TempDir()
				cfg.SkillListRefresh = 10 * time.Millisecond
				installer := &retryPublishedAPI{}
				registrar := namingtest.NewFakeMetadataRegistrar()
				runtime, err := New(WithConfig(cfg), WithSDKContext(&testutil.FakeSDKContext{}),
					WithSkillAPI(installer), WithMetadataRegistrar(registrar))
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wantErr, wantCalls := errors.New("list failed"), int32(1)
				if earlyCancellation {
					cancel()
					wantErr, wantCalls = context.Canceled, 0
				} else {
					installer.err = wantErr
				}
				require.ErrorIs(t, runtime.Prepare(ctx), wantErr)
				require.Nil(t, runtime.skillRefresh)
				require.Equal(t, runtimeStateNew, runtime.state)
				_, registered := registrar.Provider(cfg.ServiceName)
				require.False(t, registered)
				time.Sleep(30 * time.Millisecond)
				require.Equal(t, wantCalls, installer.calls.Load())
				installer.err = nil
				require.NoError(t, runtime.Prepare(context.Background()))
				require.NotNil(t, runtime.skillRefresh)
				require.NoError(t, runtime.Close(context.Background()))
				calls := installer.calls.Load()
				time.Sleep(30 * time.Millisecond)
				require.Equal(t, calls, installer.calls.Load(), "Close retained a retry refresh loop")
			})
		}
	}
}
