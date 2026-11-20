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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	agentcardtest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil/agentcard"
)

func TestResolverStickyAndRemoval(t *testing.T) {
	first := testutil.InstanceWithMetadata(
		"instance-a",
		"127.0.0.1",
		18080,
		true,
		false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080),
	)
	second := testutil.InstanceWithMetadata(
		"instance-b",
		"127.0.0.1",
		18081,
		true,
		false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18081),
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(first, second),
		testutil.InstancesResponse(second, first),
		{},
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	chosen, err := resolver.Resolve(context.Background(), "tenant-a")
	require.NoError(t, err)
	sticky, err := resolver.Resolve(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Equal(t, chosen.InstanceID, sticky.InstanceID)

	remainingID := "instance-a"
	remaining := first
	if chosen.InstanceID == "instance-a" {
		remainingID = "instance-b"
		remaining = second
	}
	lister.Responses[2] = testutil.InstancesResponse(remaining)
	switched, err := resolver.Resolve(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Equal(t, remainingID, switched.InstanceID)
}

func TestResolverAcceptsPromotedPlatformMetadata(t *testing.T) {
	metadata := agentcardtest.CardMetadata(t, "127.0.0.1", 18086)
	metadata["version"] = "1.0.0"
	metadata["region"] = ""
	metadata["zone"] = ""
	metadata["campus"] = ""
	instance := testutil.InstanceWithMetadata(
		"platform-metadata",
		"127.0.0.1",
		18086,
		true,
		false,
		metadata,
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	resolved, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, "platform-metadata", resolved.InstanceID)
	require.Equal(t, "http://127.0.0.1:18086/a2a", resolved.Endpoint)
	require.Equal(t, "1.0.0", resolved.Metadata["version"])
}

func TestResolverSkipsBadCardsAndUnhealthyInstances(t *testing.T) {
	bad := testutil.InstanceWithMetadata(
		"bad-card",
		"127.0.0.1",
		18082,
		true,
		false,
		map[string]string{agentcard.MetadataAgentCard: "{"},
	)
	unhealthy := testutil.InstanceWithMetadata(
		"unhealthy",
		"127.0.0.1",
		18083,
		false,
		false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18083),
	)
	good := testutil.InstanceWithMetadata(
		"good",
		"127.0.0.1",
		18084,
		true,
		false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18084),
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(bad, unhealthy, good),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	resolved, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, "good", resolved.InstanceID)
	require.Equal(t, "http://127.0.0.1:18084/a2a", resolved.Endpoint)
}

func TestResolverRejectsEndpointMismatch(t *testing.T) {
	instance := testutil.InstanceWithMetadata(
		"mismatch",
		"127.0.0.1",
		18085,
		true,
		false,
		agentcardtest.CardMetadata(t, "127.0.0.2", 19085),
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), "caller")
	require.ErrorIs(t, err, ErrNoAvailableInstance)
}

func TestResolverAllowsBusinessPortDifferentFromRegistrationPort(t *testing.T) {
	instance := testutil.InstanceWithMetadata(
		"port-independent",
		"127.0.0.1",
		1,
		true,
		false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 19085),
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	resolved, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Contains(t, resolved.Endpoint, ":19085")
}

func TestResolverRejectsExplicitGRPCEvenWhenCardDeclaresIt(t *testing.T) {
	config := testutil.ProviderConfig()
	card, err := agentcard.NewAgentCard(config)
	require.NoError(t, err)
	card.SupportedInterfaces = append(card.SupportedInterfaces, a2aprotocolserver.AgentInterface{
		URL:             "http://127.0.0.1:19086/grpc",
		ProtocolBinding: "GRPC",
		ProtocolVersion: "1.0",
		Tenant:          "grpc-tenant",
	})
	metadata, err := agentcard.BuildAgentInstanceMetadata(config, card)
	require.NoError(t, err)
	instance := testutil.InstanceWithMetadata(
		"binding",
		"127.0.0.1",
		1,
		true,
		false,
		metadata,
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	_, err = resolver.ResolveWithBinding(context.Background(), "caller", "GRPC")
	require.ErrorIs(t, err, ErrUnsupportedProtocolBinding)
	require.Zero(t, lister.Calls)
}

func instanceWithRawAgentCard(t *testing.T, id, a2aVersion, cardJSON string) model.Instance {
	t.Helper()
	config := testutil.ProviderConfig()
	config.A2AVersion = a2aVersion
	metadata, err := agentcard.BuildAgentInstanceMetadataFromRaw(
		config,
		agentcard.SchemaVersion(a2aVersion),
		[]byte(cardJSON),
	)
	require.NoError(t, err)
	return testutil.InstanceWithMetadata(id, "127.0.0.1", 1, true, false, metadata)
}

func TestResolverDefaultSelectionSkipsIncompatibleInterfaces(t *testing.T) {
	instance := instanceWithRawAgentCard(t, "mixed", "1.0", `{
		"name": "customer-agent",
		"version": "1.2.3",
		"supportedInterfaces": [
			{"url": "http://127.0.0.1:19088/a2a", "protocolBinding": "JSONRPC", "protocolVersion": "0.3"},
			{"url": "http://127.0.0.1:19089/http-json", "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0.7", "tenant": "tenant-b"}
		]
	}`)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })

	resolved, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, "HTTP+JSON", resolved.ProtocolBinding)
	require.Equal(t, "1.0.7", resolved.ProtocolVersion)
	require.Equal(t, "tenant-b", resolved.Tenant)
	require.Equal(t, "http://127.0.0.1:19089/http-json", resolved.Endpoint)
}

func TestResolverRejectsV03WireProtocolForFixedV1Client(t *testing.T) {
	instance := instanceWithRawAgentCard(t, "legacy", "0.3", `{
		"name": "customer-agent",
		"version": "1.2.3",
		"url": "http://127.0.0.1:19088/a2a",
		"preferredTransport": "JSONRPC",
		"protocolVersion": "0.3"
	}`)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })

	_, err = resolver.Resolve(context.Background(), "caller")
	require.ErrorIs(t, err, ErrNoAvailableInstance)
	require.ErrorContains(t, err, "no available A2A agent instance")
}

func TestResolverSelectsV03CardWithV10AdditionalInterface(t *testing.T) {
	instance := instanceWithRawAgentCard(t, "legacy-mixed", "0.3", `{
		"name": "customer-agent",
		"version": "1.2.3",
		"url": "http://127.0.0.1:19088/a2a",
		"preferredTransport": "JSONRPC",
		"protocolVersion": "0.3",
		"additionalInterfaces": [
			{
				"url": "http://127.0.0.1:19088/a2a",
				"protocolBinding": "JSONRPC",
				"transport": "JSONRPC",
				"protocolVersion": "1.0"
			}
		]
	}`)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })

	resolved, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, "1.0", resolved.ProtocolVersion)
	require.Equal(t, "http://127.0.0.1:19088/a2a", resolved.Endpoint)
}

func TestResolverRejectsUnsupportedBindingBeforeDiscovery(t *testing.T) {
	lister := &testutil.FakeInstanceLister{
		Errors: []error{errors.New("discovery must not run")},
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })

	_, err = resolver.ResolveWithBinding(context.Background(), "caller", "GRPC")
	require.ErrorIs(t, err, ErrUnsupportedProtocolBinding)
	require.Zero(t, lister.Calls)
}

func TestResolverFallbackPreservesRequestedProtocolBinding(t *testing.T) {
	config := testutil.ProviderConfig()
	card, err := agentcard.NewAgentCard(config)
	require.NoError(t, err)
	card.SupportedInterfaces = append(card.SupportedInterfaces, a2aprotocolserver.AgentInterface{
		URL:             "http://127.0.0.1:19087/http-json",
		ProtocolBinding: "HTTP+JSON",
		ProtocolVersion: "1.0",
	})
	metadata, err := agentcard.BuildAgentInstanceMetadata(config, card)
	require.NoError(t, err)
	instance := testutil.InstanceWithMetadata(
		"binding-fallback",
		"127.0.0.1",
		1,
		true,
		false,
		metadata,
	)
	lister := &testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
		Errors: []error{
			nil,
			nil,
			errors.New("temporary discovery failure"),
			errors.New("temporary discovery failure"),
		},
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	_, err = resolver.ResolveWithBinding(context.Background(), "caller", "JSONRPC")
	require.NoError(t, err)
	resolved, err := resolver.ResolveWithBinding(context.Background(), "caller", "HTTP+JSON")
	require.NoError(t, err)
	require.Equal(t, "HTTP+JSON", resolved.ProtocolBinding)
	require.Equal(t, "http://127.0.0.1:19087/http-json", resolved.Endpoint)
	remote, err := resolver.NewRemoteAgentWithBinding(context.Background(), "caller", "HTTP+JSON")
	require.NoError(t, err)
	require.NotNil(t, remote)
}

func TestResolverBindingFailurePreservesOtherCallerFallback(t *testing.T) {
	config := testutil.ProviderConfig()
	config.AdvertisePort = 19089
	card, err := agentcard.NewAgentCard(config)
	require.NoError(t, err)
	metadata, err := agentcard.BuildAgentInstanceMetadata(config, card)
	require.NoError(t, err)
	instance := testutil.InstanceWithMetadata(
		"jsonrpc-only",
		"127.0.0.1",
		1,
		true,
		false,
		metadata,
	)
	lister := &testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
		Errors: []error{
			nil,
			nil,
			errors.New("temporary discovery failure"),
		},
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	_, err = resolver.ResolveWithBinding(context.Background(), "caller-a", "JSONRPC")
	require.NoError(t, err)
	_, err = resolver.ResolveWithBinding(context.Background(), "caller-b", "HTTP+JSON")
	require.ErrorIs(t, err, ErrNoAvailableInstance)

	fallback, err := resolver.ResolveWithBinding(context.Background(), "caller-a", "JSONRPC")
	require.NoError(t, err)
	require.Equal(t, "JSONRPC", fallback.ProtocolBinding)
	require.Equal(t, "http://127.0.0.1:19089/a2a", fallback.Endpoint)
}

func TestResolverMatchesEquivalentIPv6Representations(t *testing.T) {
	config := testutil.ProviderConfig()
	config.AdvertiseHost = "::1"
	card, err := agentcard.NewAgentCard(config)
	require.NoError(t, err)
	metadata, err := agentcard.BuildAgentInstanceMetadata(config, card)
	require.NoError(t, err)
	instance := testutil.InstanceWithMetadata(
		"ipv6",
		"0:0:0:0:0:0:0:1",
		1,
		true,
		false,
		metadata,
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	resolved, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, "ipv6", resolved.InstanceID)
}

func TestResolverFallsBackToLastCard(t *testing.T) {
	instance := testutil.InstanceWithMetadata(
		"instance",
		"127.0.0.1",
		18086,
		true,
		false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086),
	)
	discoveryErr := errors.New("polaris unavailable")
	lister := &testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
		Errors:    []error{nil, discoveryErr},
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	first, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	fallback, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, first.InstanceID, fallback.InstanceID)
	require.Equal(t, first.Card.Name, fallback.Card.Name)
}

func TestResolverDoesNotResurrectRejectedInstance(t *testing.T) {
	goodMetadata := agentcardtest.CardMetadata(t, "127.0.0.1", 18086)
	good := testutil.InstanceWithMetadata("instance", "127.0.0.1", 18086, true, false, goodMetadata)
	cases := []struct {
		name      string
		instances []model.Instance
	}{
		{name: "removed"},
		{name: "unhealthy", instances: []model.Instance{
			testutil.InstanceWithMetadata("instance", "127.0.0.1", 18086, false, false, goodMetadata),
		}},
		{name: "invalid-card", instances: []model.Instance{
			testutil.InstanceWithMetadata("instance", "127.0.0.1", 18086, true, false,
				map[string]string{agentcard.MetadataAgentCard: "{"}),
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lister := &testutil.FakeInstanceLister{
				Responses: []*model.InstancesResponse{
					testutil.InstancesResponse(good),
					testutil.InstancesResponse(testCase.instances...),
				},
				Errors: []error{nil, nil, errors.New("polaris unavailable")},
			}
			resolver, err := newTestResolver(lister)
			require.NoError(t, err)

			_, err = resolver.Resolve(context.Background(), "caller")
			require.NoError(t, err)
			_, err = resolver.Resolve(context.Background(), "caller")
			require.ErrorIs(t, err, ErrNoAvailableInstance)
			_, err = resolver.Resolve(context.Background(), "caller")
			require.ErrorIs(t, err, ErrNoAvailableInstance)
		})
	}
}

type slowInstanceLister struct {
	timeout time.Duration
}

func (l *slowInstanceLister) GetInstances(request *api.GetInstancesRequest) (*model.InstancesResponse, error) {
	l.timeout = *request.Timeout
	time.Sleep(20 * time.Millisecond)
	return &model.InstancesResponse{}, nil
}

func TestResolverLimitsDiscoveryToCallerDeadline(t *testing.T) {
	lister := &slowInstanceLister{}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = resolver.Resolve(ctx, "caller")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.LessOrEqual(t, lister.timeout, 10*time.Millisecond)
}

type requestHostTracker struct {
	hosts chan string
}

func (tracker *requestHostTracker) RoundTrip(request *http.Request) (*http.Response, error) {
	tracker.hosts <- request.URL.Host
	return nil, errors.New("request captured")
}

type requestURLTracker struct {
	urls chan string
}

func (tracker *requestURLTracker) RoundTrip(request *http.Request) (*http.Response, error) {
	tracker.urls <- request.URL.String()
	return nil, errors.New("request captured")
}

type testA2AMessageConverter struct{}

func (testA2AMessageConverter) ConvertToA2AMessage(
	string,
	*agent.Invocation,
) (*protocol.Message, error) {
	message := protocol.NewMessage(protocol.MessageRoleUser, []*protocol.Part{protocol.NewTextPart("hello")})
	message.MessageID = "test-message"
	return &message, nil
}

func TestResolverRemoteAgentReResolvesBeforeNewRun(t *testing.T) {
	first := testutil.InstanceWithMetadata(
		"instance-a", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080),
	)
	second := testutil.InstanceWithMetadata(
		"instance-b", "127.0.0.1", 18081, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18081),
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(first, second),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	tracker := &requestHostTracker{hosts: make(chan string, 1)}
	remote, err := resolver.NewRemoteAgent(
		context.Background(),
		"caller",
		func(context.Context) ([]a2aagent.Option, error) {
			return []a2aagent.Option{
				a2aagent.WithCustomA2AConverter(testA2AMessageConverter{}),
				a2aagent.WithA2AClientExtraOptions(a2aclient.WithHTTPClient(&http.Client{Transport: tracker})),
			}, nil
		},
	)
	require.NoError(t, err)

	defaultState := resolver.bindingStateReadOnlyLocked("")
	require.NotNil(t, defaultState)
	chosenID := defaultState.callers["caller"].instanceID
	remaining := second
	expectedHost := "127.0.0.1:18081"
	if chosenID == "instance-b" {
		remaining = first
		expectedHost = "127.0.0.1:18080"
	}
	lister.Responses = []*model.InstancesResponse{testutil.InstancesResponse(remaining)}

	events, err := remote.Run(context.Background(), agent.NewInvocation())
	require.NoError(t, err)
	for range events {
	}
	require.Equal(t, expectedHost, <-tracker.hosts)
}

func TestResolverCallerCacheIsBoundedAndForgettable(t *testing.T) {
	instance := testutil.InstanceWithMetadata(
		"instance", "127.0.0.1", 18086, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086),
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	for i := 0; i < maxResolverCallerEntries*2; i++ {
		_, err := resolver.Resolve(context.Background(), "caller-"+strconv.Itoa(i))
		require.NoError(t, err)
	}
	defaultState := resolver.bindingStateReadOnlyLocked("")
	require.NotNil(t, defaultState)
	require.LessOrEqual(t, len(defaultState.callers), maxResolverCallerEntries)
	require.LessOrEqual(t, len(defaultState.agents), maxResolverCallerEntries)

	resolver.Forget("caller-0")
	lister.Responses = nil
	lister.Errors = make([]error, lister.Calls+1)
	lister.Errors[lister.Calls] = errors.New("polaris unavailable")
	_, err = resolver.Resolve(context.Background(), "caller-0")
	require.ErrorIs(t, err, ErrNoAvailableInstance)
}

func TestResolverFallbackExpires(t *testing.T) {
	instance := testutil.InstanceWithMetadata(
		"instance", "127.0.0.1", 18086, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086),
	)
	lister := &testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
		Errors:    []error{nil, errors.New("polaris unavailable")},
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	resolver.now = func() time.Time {
		return time.Now().Add(resolverFallbackTTL + time.Second)
	}
	_, err = resolver.Resolve(context.Background(), "caller")
	require.ErrorIs(t, err, ErrNoAvailableInstance)
	require.NotContains(t, err.Error(), "using cached AgentCard")
}

func TestResolverRejectsPermanentDiscoveryErrors(t *testing.T) {
	instance := testutil.InstanceWithMetadata(
		"instance", "127.0.0.1", 18086, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086),
	)
	codes := []model.ErrCode{
		model.ErrCodeServiceNotFound,
		model.ErrCodeUnauthorized,
		model.ErrCodeInvalidStateError,
	}
	for _, code := range codes {
		t.Run(model.ErrCodeToString(code), func(t *testing.T) {
			cause := model.NewSDKError(code, nil, "permanent discovery error")
			lister := &testutil.FakeInstanceLister{
				Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
				Errors:    []error{nil, cause},
			}
			resolver, err := newTestResolver(lister)
			require.NoError(t, err)

			_, err = resolver.Resolve(context.Background(), "caller")
			require.NoError(t, err)
			fallback, err := resolver.Resolve(context.Background(), "caller")
			require.ErrorIs(t, err, cause)
			require.Nil(t, fallback)
		})
	}
}

type jsonRPCResponseTransport struct{}

func (jsonRPCResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	reader := request.Body
	if request.Body != nil {
		defer func() { _ = request.Body.Close() }()
	}
	if request.GetBody != nil {
		var err error
		reader, err = request.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = reader.Close() }()
	}
	var payload struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return nil, err
	}
	message := protocol.NewMessage(protocol.MessageRoleAgent, []*protocol.Part{protocol.NewTextPart("ok")})
	body, err := json.Marshal(struct {
		JSONRPC string                        `json:"jsonrpc"`
		ID      json.RawMessage               `json:"id"`
		Result  *protocol.SendMessageResponse `json:"result"`
	}{"2.0", payload.ID, protocol.NewSendMessageResponseMessage(&message)})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Request:    request,
	}, nil
}

func TestResolverRemoteAgentReusesClientForSameCard(t *testing.T) {
	instance := testutil.InstanceWithMetadata(
		"instance", "127.0.0.1", 18086, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18086),
	)
	lister := &testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	var builds atomic.Int32
	remote, err := resolver.NewRemoteAgent(
		context.Background(),
		"caller",
		func(context.Context) ([]a2aagent.Option, error) {
			builds.Add(1)
			return []a2aagent.Option{
				a2aagent.WithCustomA2AConverter(testA2AMessageConverter{}),
				a2aagent.WithA2AClientExtraOptions(
					a2aclient.WithHTTPClient(&http.Client{Transport: jsonRPCResponseTransport{}}),
					a2aclient.WithTimeout(time.Second),
				),
			}, nil
		},
	)
	require.NoError(t, err)

	var group sync.WaitGroup
	for i := 0; i < 24; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			stream, err := remote.Run(context.Background(), agent.NewInvocation())
			if err != nil {
				t.Error(err)
				return
			}
			checkSuccessfulEvents(t, stream)
		}()
	}
	group.Wait()
	require.EqualValues(t, 1, builds.Load())
}

func TestResolverPreservesDiscoveryCause(t *testing.T) {
	cause := errors.New("transport failure")
	lister := &testutil.FakeInstanceLister{Errors: []error{cause}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), "caller")
	require.ErrorIs(t, err, cause)
	require.ErrorIs(t, err, ErrNoAvailableInstance)
}

type orderedInstanceLister struct {
	calls        atomic.Int32
	started      chan struct{}
	release      chan struct{}
	old, current *model.InstancesResponse
}

func (l *orderedInstanceLister) GetInstances(
	*api.GetInstancesRequest,
) (*model.InstancesResponse, error) {
	switch l.calls.Add(1) {
	case 1:
		return l.old, nil
	case 2:
		close(l.started)
		<-l.release
		return l.old, nil
	case 3:
		return l.current, nil
	default:
		return nil, errors.New("temporary outage")
	}
}

func TestResolverStaleResolveCannotRestoreRemovedInstance(t *testing.T) {
	first := testutil.InstanceWithMetadata(
		"instance-a", "127.0.0.1", 18080, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18080),
	)
	second := testutil.InstanceWithMetadata(
		"instance-b", "127.0.0.1", 18081, true, false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18081),
	)
	lister := &orderedInstanceLister{
		started: make(chan struct{}),
		release: make(chan struct{}),
		old:     testutil.InstancesResponse(first, second),
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	chosen, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	remaining := second
	if chosen.InstanceID == "instance-b" {
		remaining = first
	}
	lister.current = testutil.InstancesResponse(remaining)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := resolver.Resolve(context.Background(), "caller"); err != nil {
			t.Error(err)
		}
	}()
	<-lister.started
	fresh, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, remaining.GetId(), fresh.InstanceID)
	close(lister.release)
	<-done

	fallback, err := resolver.Resolve(context.Background(), "caller")
	require.NoError(t, err)
	require.Equal(t, remaining.GetId(), fallback.InstanceID)
}

type tenantCaptureTransport struct {
	tenants chan string
}

func (transport tenantCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var payload struct {
		Params protocol.SendMessageParams `json:"params"`
	}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		return nil, err
	}
	transport.tenants <- payload.Params.Tenant
	return jsonRPCResponseTransport{}.RoundTrip(request)
}

func TestResolverRemoteAgentForwardsInterfaceTenant(t *testing.T) {
	cfg := testutil.ProviderConfig()
	card, err := agentcard.NewAgentCard(cfg)
	require.NoError(t, err)
	card.SupportedInterfaces[0].Tenant = "tenant-a"
	metadata, err := agentcard.BuildAgentInstanceMetadata(cfg, card)
	require.NoError(t, err)
	instance := testutil.InstanceWithMetadata(
		"instance", "127.0.0.1", 18080, true, false, metadata,
	)
	lister := &testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
	}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)
	transport := tenantCaptureTransport{tenants: make(chan string, 1)}
	remote, err := resolver.NewRemoteAgent(
		context.Background(),
		"caller",
		func(context.Context) ([]a2aagent.Option, error) {
			return []a2aagent.Option{
				a2aagent.WithCustomA2AConverter(testA2AMessageConverter{}),
				a2aagent.WithA2AClientExtraOptions(a2aclient.WithHTTPClient(&http.Client{Transport: transport})),
			}, nil
		},
	)
	require.NoError(t, err)

	stream, err := remote.Run(context.Background(), agent.NewInvocation())
	require.NoError(t, err)
	for range stream {
	}
	require.Equal(t, "tenant-a", <-transport.tenants)
}

func TestResolverRemoteAgentUsesSelectedInterfaceURL(t *testing.T) {
	cfg := testutil.ProviderConfig()
	card, err := agentcard.NewAgentCard(cfg)
	require.NoError(t, err)
	wanted := "http://127.0.0.1:19001/http-json"
	card.SupportedInterfaces = append(card.SupportedInterfaces, protocol.AgentInterface{
		URL:             wanted,
		ProtocolBinding: "HTTP+JSON",
		ProtocolVersion: "1.0",
	})
	metadata, err := agentcard.BuildAgentInstanceMetadata(cfg, card)
	require.NoError(t, err)
	instance := testutil.InstanceWithMetadata(
		"instance", "127.0.0.1", 18080, true, false, metadata,
	)
	resolver, err := newTestResolver(&testutil.FakeInstanceLister{
		Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resolver.Close()) })

	for _, testCase := range []struct {
		name       string
		streaming  bool
		wantedPath string
	}{
		{name: "unary", wantedPath: "/message:send"},
		{name: "stream", streaming: true, wantedPath: "/message:stream"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tracker := &requestURLTracker{urls: make(chan string, 1)}
			remote, err := resolver.NewRemoteAgentWithBinding(
				context.Background(),
				"caller",
				"HTTP+JSON",
				func(context.Context) ([]a2aagent.Option, error) {
					return []a2aagent.Option{
						a2aagent.WithEnableStreaming(testCase.streaming),
						a2aagent.WithCustomA2AConverter(testA2AMessageConverter{}),
						a2aagent.WithA2AClientExtraOptions(
							a2aclient.WithHTTPClient(&http.Client{Transport: tracker}),
						),
					}, nil
				},
			)
			require.NoError(t, err)

			stream, err := remote.Run(context.Background(), agent.NewInvocation())
			require.NoError(t, err)
			for range stream {
			}
			select {
			case got := <-tracker.urls:
				require.Equal(t, wanted+testCase.wantedPath, got)
			case <-time.After(time.Second):
				t.Fatal("remote agent emitted no request")
			}
		})
	}
}

func TestResolverNewRemoteAgent(t *testing.T) {
	instance := testutil.InstanceWithMetadata(
		"instance",
		"127.0.0.1",
		18087,
		true,
		false,
		agentcardtest.CardMetadata(t, "127.0.0.1", 18087),
	)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(instance),
	}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	remote, err := resolver.NewRemoteAgent(context.Background(), "caller")
	require.NoError(t, err)
	require.NotNil(t, remote)
}

func TestResolverCloseAndNoInstances(t *testing.T) {
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{{}}}
	resolver, err := newTestResolver(lister)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), "caller")
	require.ErrorIs(t, err, ErrNoAvailableInstance)
	require.NoError(t, resolver.Close())
	_, err = resolver.Resolve(context.Background(), "caller")
	require.ErrorIs(t, err, ErrClosed)
}

func newTestResolver(lister InstanceLister) (*Resolver, error) {
	return NewResolver(ResolverOptions{
		Namespace: "Production",
		Service:   "customer-agent",
		Timeout:   5 * time.Second,
		Lister:    lister,
	}), nil
}
