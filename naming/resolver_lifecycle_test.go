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
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-go/event"
	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	agentcardtest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil/agentcard"
)

type instanceListerFunc func(*api.GetInstancesRequest) (*model.InstancesResponse, error)

func (f instanceListerFunc) GetInstances(request *api.GetInstancesRequest) (*model.InstancesResponse, error) {
	return f(request)
}

func TestResolverPermanentErrorSupersedesOlderSuccess(t *testing.T) {
	for _, code := range []model.ErrCode{model.ErrCodeUnauthorized, model.ErrCodeServiceNotFound, model.ErrCodeInvalidStateError} {
		t.Run(strconv.Itoa(int(code)), func(t *testing.T) {
			instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18086, true, false,
				agentcardtest.CardMetadata(t, "127.0.0.1", 18086))
			old := testutil.InstancesResponse(instance)
			cause := model.NewSDKError(code, nil, "access revoked")
			var calls atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			lister := instanceListerFunc(func(*api.GetInstancesRequest) (*model.InstancesResponse, error) {
				switch calls.Add(1) {
				case 1:
					return old, nil
				case 2:
					close(started)
					<-release
					return old, nil
				case 3:
					return nil, cause
				default:
					return nil, errors.New("temporary outage")
				}
			})
			resolver, err := newTestResolver(lister)
			require.NoError(t, err)
			_, err = resolver.Resolve(context.Background(), "caller")
			require.NoError(t, err)
			result := make(chan *ResolvedAgent, 1)
			failures := make(chan error, 1)
			go func() {
				resolved, err := resolver.Resolve(context.Background(), "caller")
				result <- resolved
				failures <- err
			}()
			<-started
			_, err = resolver.Resolve(context.Background(), "caller")
			require.ErrorIs(t, err, cause)
			close(release)
			require.Nil(t, <-result)
			require.ErrorIs(t, <-failures, cause)
			fallback, err := resolver.Resolve(context.Background(), "caller")
			require.Error(t, err)
			require.Nil(t, fallback)
		})
	}
}

func TestResolverOldPermanentErrorDoesNotInvalidateNewSuccess(t *testing.T) {
	instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18086, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086))
	cause := model.NewSDKError(model.ErrCodeUnauthorized, nil, "old access failure")
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	lister := instanceListerFunc(func(*api.GetInstancesRequest) (*model.InstancesResponse, error) {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
			return nil, cause
		case 2:
			return testutil.InstancesResponse(instance), nil
		default:
			return nil, errors.New("temporary outage")
		}
	})
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := resolver.Resolve(context.Background(), "caller"); done <- err }()
	<-started
	fresh, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	close(release)
	require.ErrorIs(t, <-done, cause)
	fallback, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, fresh.InstanceID, fallback.InstanceID)
}

func TestResolverStaleEmptyResponseCannotClearNewSuccess(t *testing.T) {
	instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18086, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086))
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	lister := instanceListerFunc(func(*api.GetInstancesRequest) (*model.InstancesResponse, error) {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
			return testutil.InstancesResponse(), nil
		case 2:
			return testutil.InstancesResponse(instance), nil
		default:
			return nil, errors.New("temporary outage")
		}
	})
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := resolver.Resolve(context.Background(), "caller"); done <- err }()
	<-started
	_, err = resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	close(release)
	require.NoError(t, <-done)
	fallback, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, instance.GetId(), fallback.InstanceID)
}

func clientOptionsFactory(transport http.RoundTripper) RemoteAgentOptionsFactory {
	return func(context.Context) ([]a2aagent.Option, error) {
		return []a2aagent.Option{
			a2aagent.WithCustomA2AConverter(testA2AMessageConverter{}),
			a2aagent.WithA2AClientExtraOptions(
				a2aclient.WithHTTPClient(&http.Client{Transport: transport}),
				a2aclient.WithTimeout(time.Second),
			),
		}, nil
	}
}

func TestResolverRemoteAgentFactoriesOwnClients(t *testing.T) {
	instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18086, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086))
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	transport := jsonRPCResponseTransport{}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			remote, err := resolver.NewRemoteAgent(context.Background(), "caller-"+strconv.Itoa(index),
				clientOptionsFactory(transport))
			if err != nil {
				t.Error(err)
				return
			}
			stream, err := remote.Run(context.Background(), agent.NewInvocation())
			if err != nil {
				t.Error(err)
				return
			}
			checkSuccessfulEvents(t, stream)
		}(i)
	}
	group.Wait()
}

type manyEventsConverter struct {
	converted chan struct{}
	once      sync.Once
}

func (converter *manyEventsConverter) ConvertToEvents(
	_ protocol.SendMessageResponse, name string, invocation *agent.Invocation,
) ([]*event.Event, error) {
	converter.once.Do(func() { close(converter.converted) })
	events := make([]*event.Event, 96)
	for index := range events {
		events[index] = event.NewResponseEvent(invocation.InvocationID, name, &agentmodel.Response{
			IsPartial: true,
			Choices:   []agentmodel.Choice{{Message: agentmodel.NewAssistantMessage("ok")}},
		})
	}
	return events, nil
}

func (*manyEventsConverter) ConvertStreamingToEvents(
	protocol.StreamResponse, string, *agent.Invocation,
) ([]*event.Event, error) {
	return nil, nil
}

func TestResolverCancelUnconsumedRunDoesNotBlockCardChange(t *testing.T) {
	first := testutil.InstanceWithMetadata("first", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080))
	second := testutil.InstanceWithMetadata("second", "127.0.0.1", 18081, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18081))
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(first), testutil.InstancesResponse(first), testutil.InstancesResponse(second),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	converter := &manyEventsConverter{converted: make(chan struct{})}
	factory := func(ctx context.Context) ([]a2aagent.Option, error) {
		options, err := clientOptionsFactory(jsonRPCResponseTransport{})(ctx)
		return append(options, a2aagent.WithCustomEventConverter(converter)), err
	}
	remote, err := resolver.NewRemoteAgent(context.Background(), "caller", factory)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := remote.Run(ctx, agent.NewInvocation())
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		for range stream {
		}
	})
	select {
	case <-converter.converted:
	case <-time.After(3 * time.Second):
		t.Fatal("A2A response was not converted")
	}
	cancel()

	nextCtx, nextCancel := context.WithTimeout(context.Background(), time.Second)
	defer nextCancel()
	next, err := remote.Run(nextCtx, agent.NewInvocation())
	require.NoError(t, err)
	for range next {
	}
	require.NoError(t, nextCtx.Err())
}

type blockedFirstTransport struct {
	started, release chan struct{}
	once             sync.Once
}

func (transport *blockedFirstTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Port() == "18080" {
		transport.once.Do(func() { close(transport.started) })
		select {
		case <-transport.release:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	}
	return jsonRPCResponseTransport{}.RoundTrip(request)
}

func TestResolverCardChangeDoesNotWaitForInFlightRequest(t *testing.T) {
	first := testutil.InstanceWithMetadata("first", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080))
	second := testutil.InstanceWithMetadata("second", "127.0.0.1", 18081, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18081))
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(first), testutil.InstancesResponse(first), testutil.InstancesResponse(second),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	transport := &blockedFirstTransport{started: make(chan struct{}), release: make(chan struct{})}
	remote, err := resolver.NewRemoteAgent(context.Background(), "caller", clientOptionsFactory(transport))
	require.NoError(t, err)
	stream, err := remote.Run(context.Background(), agent.NewInvocation())
	require.NoError(t, err)
	t.Cleanup(func() {
		close(transport.release)
		for range stream {
		}
	})
	select {
	case <-transport.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first A2A request did not start")
	}
	nextCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	next, err := remote.Run(nextCtx, agent.NewInvocation())
	require.NoError(t, err)
	checkSuccessfulEvents(t, next)
	require.NoError(t, nextCtx.Err())
}

func checkSuccessfulEvents(t *testing.T, stream <-chan *event.Event) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	count := 0
	for {
		select {
		case evt, ok := <-stream:
			if !ok {
				if count == 0 {
					t.Error("A2A response produced no events")
				}
				return
			}
			count++
			if evt != nil && evt.Response != nil && evt.Error != nil {
				t.Errorf("A2A error event: %+v", evt.Error)
			}
		case <-timer.C:
			t.Error("A2A event stream did not close")
			return
		}
	}
}

func TestResolverRemoteAgentFactoryErrorsRemainInspectable(t *testing.T) {
	instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18086, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086))
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	cause := errors.New("factory failed")
	_, err = resolver.NewRemoteAgent(context.Background(), "caller",
		func(context.Context) ([]a2aagent.Option, error) { return nil, cause })
	require.ErrorIs(t, err, cause)
}

func TestResolverStaleResolveBindsNewCallerToLatestCandidates(t *testing.T) {
	first := testutil.InstanceWithMetadata("first", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080))
	second := testutil.InstanceWithMetadata("second", "127.0.0.1", 18081, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18081))
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	lister := instanceListerFunc(func(*api.GetInstancesRequest) (*model.InstancesResponse, error) {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
			return testutil.InstancesResponse(first), nil
		case 2:
			return testutil.InstancesResponse(second), nil
		default:
			return nil, errors.New("temporary outage")
		}
	})
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	result := make(chan *ResolvedAgent, 1)
	done := make(chan error, 1)
	go func() {
		resolved, err := resolver.Resolve(context.Background(), "new-caller")
		result <- resolved
		done <- err
	}()
	<-started
	_, err = resolver.Resolve(context.Background(), "other-caller")
	require.NoError(t, err)
	close(release)
	require.NoError(t, <-done)
	require.Equal(t, second.GetId(), (<-result).InstanceID)
	require.EqualValues(t, 2, calls.Load())
	_, err = resolver.Resolve(context.Background(), "never-resolved")
	require.ErrorIs(t, err, ErrNoAvailableInstance)
}

func TestResolverLatestSnapshotCannotBeChangedByCaller(t *testing.T) {
	instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080))
	lister := &testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
		Errors:    []error{nil, errors.New("temporary outage")},
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	resolved, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	resolved.Metadata["changed"] = "value"
	resolved.Card.SupportedInterfaces[0].URL = "http://invalid.example/a2a"
	fallback, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.NotContains(t, fallback.Metadata, "changed")
	require.Equal(t, "http://127.0.0.1:18080/a2a", fallback.Endpoint)
	require.Equal(t, fallback.Endpoint, fallback.Card.PrimaryURL())
}

func TestResolverRemoteAgentFactoryGetsNonNilContext(t *testing.T) {
	instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080))
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
	})
	require.NoError(t, err)
	_, err = resolver.NewRemoteAgent(nil, "caller", func(ctx context.Context) ([]a2aagent.Option, error) { //nolint:staticcheck // Exercise the documented nil-context boundary.
		if ctx == nil {
			return nil, errors.New("factory received nil context")
		}
		return nil, ctx.Err()
	})
	require.NoError(t, err)
}

func TestResolverRemoteAgentIgnoresNilOptions(t *testing.T) {
	instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080))
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
	})
	require.NoError(t, err)
	require.NotPanics(t, func() {
		_, err := resolver.NewRemoteAgent(context.Background(), "caller",
			func(context.Context) ([]a2aagent.Option, error) { return []a2aagent.Option{nil}, nil })
		require.NoError(t, err)
	})
}

func TestResolverReportsMissingListerWithoutPanic(t *testing.T) {
	resolver := NewResolver(ResolverOptions{Namespace: "Production", Service: "customer-agent"})
	require.NotPanics(t, func() {
		_, err := resolver.Resolve(context.Background(), "caller")
		require.Error(t, err)
	})
}

func TestResolverRemoteAgentRejectsCloseDuringFactory(t *testing.T) {
	instance := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080))
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
	})
	require.NoError(t, err)
	_, err = resolver.NewRemoteAgent(context.Background(), "caller",
		func(context.Context) ([]a2aagent.Option, error) { return nil, resolver.Close() })
	require.ErrorIs(t, err, ErrClosed)
}

func TestResolverRemoteAgentBuildWaitRespectsDeadline(t *testing.T) {
	first := testutil.InstanceWithMetadata("first", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080))
	second := testutil.InstanceWithMetadata("second", "127.0.0.1", 18081, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18081))
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(first), testutil.InstancesResponse(second)},
	})
	require.NoError(t, err)
	started, release := make(chan struct{}), make(chan struct{})
	var builds atomic.Int32
	remote, err := resolver.NewRemoteAgent(context.Background(), "caller", func(ctx context.Context) ([]a2aagent.Option, error) {
		if builds.Add(1) == 2 {
			close(started)
			<-release
		}
		return clientOptionsFactory(jsonRPCResponseTransport{})(ctx)
	})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		stream, err := remote.Run(context.Background(), agent.NewInvocation())
		if err == nil {
			checkSuccessfulEvents(t, stream)
		}
		done <- err
	}()
	<-started
	t.Cleanup(func() { close(release); require.NoError(t, <-done) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	waiter := make(chan error, 1)
	go func() { _, err := remote.Run(ctx, agent.NewInvocation()); waiter <- err }()
	select {
	case err := <-waiter:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(200 * time.Millisecond):
		t.Error("client build wait outlived caller deadline")
	}
}
