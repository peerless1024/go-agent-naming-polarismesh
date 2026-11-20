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
	"net/http"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
)

func TestDiscoveredStreamingCapabilityControlsWireCall(t *testing.T) {
	for _, unary := range []bool{false, true} {
		t.Run(map[bool]string{false: "card default", true: "explicit unary"}[unary], func(t *testing.T) {
			cfg := testutil.ProviderConfig()
			cfg.Streaming = true
			card, err := agentcard.NewAgentCard(cfg)
			require.NoError(t, err)
			card.SupportedInterfaces[0].ProtocolBinding = "HTTP+JSON"
			metadata, err := agentcard.BuildAgentInstanceMetadata(cfg, card)
			require.NoError(t, err)
			instance := testutil.InstanceWithMetadata("streaming-instance", "127.0.0.1", 18080, true, false, metadata)
			resolver, err := newTestResolver(&testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, resolver.Close()) })
			tracker := &requestURLTracker{urls: make(chan string, 1)}
			remote, err := resolver.NewRemoteAgentWithBinding(context.Background(), "caller", "HTTP+JSON", func(context.Context) ([]a2aagent.Option, error) {
				options := []a2aagent.Option{
					a2aagent.WithCustomA2AConverter(testA2AMessageConverter{}),
					a2aagent.WithA2AClientExtraOptions(a2aclient.WithHTTPClient(&http.Client{Transport: tracker})),
				}
				if unary {
					options = append(options, a2aagent.WithEnableStreaming(false))
				}
				return options, nil
			})
			require.NoError(t, err)
			stream, err := remote.Run(context.Background(), agent.NewInvocation())
			require.NoError(t, err)
			for range stream {
			}
			select {
			case got := <-tracker.urls:
				method := "/message:stream"
				if unary {
					method = "/message:send"
				}
				require.Contains(t, got, method)
			case <-time.After(time.Second):
				t.Fatal("no outbound A2A request")
			}
		})
	}
}

func TestResolverSkipsMalformedInterfaceVersions(t *testing.T) {
	for _, version := range []string{"x.0", "1.x"} {
		t.Run(version, func(t *testing.T) {
			metadata := map[string]string{
				agentcard.MetadataAgentName: "customer-agent", agentcard.MetadataA2AVersion: "1.0",
				agentcard.MetadataAgentCard: `{"name":"customer-agent","version":"1.2.3","supportedInterfaces":[{"url":"http://127.0.0.1:18080/a2a","protocolBinding":"JSONRPC","protocolVersion":"` + version + `"}]}`,
			}
			instance := testutil.InstanceWithMetadata("malformed-version", "127.0.0.1", 18080, true, false, metadata)
			resolver, err := newTestResolver(&testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, resolver.Close()) })
			require.NotPanics(t, func() { _, err = resolver.Resolve(context.Background(), "caller") })
			require.ErrorIs(t, err, ErrNoAvailableInstance)
		})
	}
}
