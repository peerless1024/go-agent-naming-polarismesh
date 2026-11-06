/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package naming

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	agentcardtest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil/agentcard"
)

func buildTestInstance(t *testing.T, id string, port uint32) model.Instance {
	t.Helper()
	return testutil.InstanceWithMetadata(id, "127.0.0.1", port, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", int(port)))
}

func TestResolverInitialBuildRejectsInvalidatedSnapshot(t *testing.T) {
	for _, cause := range []error{ErrNoAvailableInstance, model.NewSDKError(model.ErrCodeUnauthorized, nil, "revoked")} {
		t.Run(cause.Error(), func(t *testing.T) {
			first := buildTestInstance(t, "first", 18080)
			var calls atomic.Int32
			resolver, err := newTestResolver(instanceListerFunc(func(*api.GetInstancesRequest) (*model.InstancesResponse, error) {
				if calls.Add(1) == 1 {
					return testutil.InstancesResponse(first), nil
				}
				if errors.Is(cause, ErrNoAvailableInstance) {
					return testutil.InstancesResponse(), nil
				}
				return nil, cause
			}))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, resolver.Close()) })
			remote, err := resolver.NewRemoteAgent(context.Background(), "caller",
				func(ctx context.Context) ([]a2aagent.Option, error) {
					_, discoveryErr := resolver.Resolve(ctx, "other")
					if !errors.Is(discoveryErr, cause) {
						return nil, errors.New("invalidation fixture did not commit")
					}
					return nil, nil
				})
			require.ErrorIs(t, err, cause)
			require.Nil(t, remote)
		})
	}
}

func TestResolverInitialBuildUsesNewCardCommittedByFactory(t *testing.T) {
	first := buildTestInstance(t, "first", 18080)
	second := buildTestInstance(t, "second", 18081)
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(first), testutil.InstancesResponse(second),
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })
	var builds atomic.Int32
	remote, err := resolver.NewRemoteAgent(context.Background(), "caller",
		func(ctx context.Context) ([]a2aagent.Option, error) {
			if builds.Add(1) == 1 {
				if _, err := resolver.Resolve(ctx, "other"); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
	require.NoError(t, err)
	resolved, err := resolveInstance(second)
	require.NoError(t, err)
	key, err := resolvedAgentKey(*resolved)
	require.NoError(t, err)
	require.Equal(t, key, remote.(*dynamicRemoteAgent).currentKey)
	require.EqualValues(t, 2, builds.Load())
}

func TestResolverRebuildRechecksSnapshotAfterFactory(t *testing.T) {
	for _, unavailable := range []bool{true, false} {
		t.Run(strconv.FormatBool(unavailable), func(t *testing.T) {
			first := buildTestInstance(t, "first", 18080)
			second := buildTestInstance(t, "second", 18081)
			third := buildTestInstance(t, "third", 18082)
			latest := testutil.InstancesResponse(third)
			if unavailable {
				latest = testutil.InstancesResponse()
			}
			resolver, err := newTestResolver(&testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
				testutil.InstancesResponse(first), testutil.InstancesResponse(second), latest,
			}})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			t.Cleanup(func() { require.NoError(t, resolver.Close()) })
			started, release := make(chan struct{}), make(chan struct{})
			var builds atomic.Int32
			remote, err := resolver.NewRemoteAgent(ctx, "caller",
				func(ctx context.Context) ([]a2aagent.Option, error) {
					if builds.Add(1) == 2 {
						close(started)
						select {
						case <-release:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					return nil, nil
				})
			require.NoError(t, err)
			dynamic := remote.(*dynamicRemoteAgent)
			done := make(chan error, 1)
			go func() { _, err := dynamic.remoteForRun(ctx); done <- err }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("rebuild did not reach the factory")
			}
			_, err = resolver.Resolve(ctx, "other")
			if unavailable {
				require.ErrorIs(t, err, ErrNoAvailableInstance)
			} else {
				require.NoError(t, err)
			}
			close(release)
			select {
			case err = <-done:
			case <-ctx.Done():
				t.Fatal("rebuild did not finish")
			}
			if unavailable {
				require.ErrorIs(t, err, ErrNoAvailableInstance)
				return
			}
			require.NoError(t, err)
			resolved, err := resolveInstance(third)
			require.NoError(t, err)
			key, err := resolvedAgentKey(*resolved)
			require.NoError(t, err)
			require.Equal(t, key, dynamic.currentKey)
			require.EqualValues(t, 3, builds.Load())
		})
	}
}

func TestResolverCloseWakesQueuedRebuild(t *testing.T) {
	first := buildTestInstance(t, "first", 18080)
	second := buildTestInstance(t, "second", 18081)
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(first), testutil.InstancesResponse(second),
	}})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var builds atomic.Int32
	remote, err := resolver.NewRemoteAgent(ctx, "caller", func(ctx context.Context) ([]a2aagent.Option, error) {
		if builds.Add(1) == 2 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, nil
	})
	require.NoError(t, err)
	dynamic := remote.(*dynamicRemoteAgent)
	active, waiter := make(chan error, 1), make(chan error, 1)
	go func() { _, err := dynamic.remoteForRun(ctx); active <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("rebuild did not reach the factory")
	}
	go func() { _, err := dynamic.remoteForRun(ctx); waiter <- err }()
	require.NoError(t, resolver.Close())
	select {
	case err := <-waiter:
		require.ErrorIs(t, err, ErrClosed)
	case <-time.After(200 * time.Millisecond):
		t.Error("queued build did not wake on Close")
	}
	close(release)
	select {
	case err := <-active:
		require.ErrorIs(t, err, ErrClosed)
	case <-ctx.Done():
		t.Fatal("active factory did not finish")
	}
}

func TestResolverBuildChurnIsBounded(t *testing.T) {
	instances := []model.Instance{buildTestInstance(t, "first", 18080), buildTestInstance(t, "second", 18081)}
	var discoveries atomic.Int32
	resolver, err := newTestResolver(instanceListerFunc(func(*api.GetInstancesRequest) (*model.InstancesResponse, error) {
		return testutil.InstancesResponse(instances[int(discoveries.Add(1)-1)%len(instances)]), nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })
	var builds atomic.Int32
	remote, err := resolver.NewRemoteAgent(context.Background(), "caller",
		func(ctx context.Context) ([]a2aagent.Option, error) {
			builds.Add(1)
			_, err := resolver.Resolve(ctx, "other")
			return nil, err
		})
	require.ErrorIs(t, err, ErrNoAvailableInstance)
	require.Nil(t, remote)
	require.EqualValues(t, 3, builds.Load())
}

func TestResolverFactoryDoesNotExtendFallbackTTL(t *testing.T) {
	first := buildTestInstance(t, "first", 18080)
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(first),
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })
	now := time.Now()
	resolver.now = func() time.Time { return now }
	remote, err := resolver.NewRemoteAgent(context.Background(), "caller",
		func(context.Context) ([]a2aagent.Option, error) {
			now = now.Add(resolverFallbackTTL + time.Second)
			return nil, nil
		})
	require.ErrorIs(t, err, ErrNoAvailableInstance)
	require.Nil(t, remote)
}

func TestResolverFactorySameCardRefreshDoesNotRebuild(t *testing.T) {
	first := buildTestInstance(t, "first", 18080)
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(first),
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })
	var builds atomic.Int32
	remote, err := resolver.NewRemoteAgent(context.Background(), "caller",
		func(ctx context.Context) ([]a2aagent.Option, error) {
			builds.Add(1)
			_, err := resolver.Resolve(ctx, "other")
			return nil, err
		})
	require.NoError(t, err)
	require.NotNil(t, remote)
	require.EqualValues(t, 1, builds.Load())
}

func TestResolverUsesOneBoundedDiscoveryAttempt(t *testing.T) {
	first := buildTestInstance(t, "first", 18080)
	resolver := NewResolver(ResolverOptions{Namespace: "Production", Service: "customer-agent",
		Lister: instanceListerFunc(func(request *api.GetInstancesRequest) (*model.InstancesResponse, error) {
			require.NotNil(t, request.Timeout)
			require.Positive(t, *request.Timeout)
			require.LessOrEqual(t, *request.Timeout, 50*time.Millisecond)
			require.NotNil(t, request.RetryCount)
			if request.RetryCount != nil {
				require.Zero(t, *request.RetryCount)
			}
			return testutil.InstancesResponse(first), nil
		})})
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := resolver.Resolve(ctx, "caller")
	require.NoError(t, err)
}
