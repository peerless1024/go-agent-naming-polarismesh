/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

func TestRealClientCollectsOnlyDeclaredContractCapabilities(t *testing.T) {
	all := trpcmcp.ServerCapabilities{
		Tools:     &trpcmcp.ToolsCapability{ListChanged: true},
		Resources: &trpcmcp.ResourcesCapability{ListChanged: true},
		Prompts:   &trpcmcp.PromptsCapability{ListChanged: true},
	}
	for _, test := range []struct {
		name         string
		capabilities trpcmcp.ServerCapabilities
		lists        []string
		methods      []string
		features     int
		fail         bool
		missing      bool
	}{
		{"tools", trpcmcp.ServerCapabilities{Tools: &trpcmcp.ToolsCapability{}},
			[]string{trpcmcp.MethodToolsList}, []string{trpcmcp.MethodToolsList, trpcmcp.MethodToolsCall}, 1, false, false},
		{"resources", trpcmcp.ServerCapabilities{Resources: &trpcmcp.ResourcesCapability{}},
			[]string{trpcmcp.MethodResourcesList}, []string{trpcmcp.MethodResourcesList, trpcmcp.MethodResourcesRead, trpcmcp.MethodResourcesTemplatesList}, 1, false, false},
		{"prompts", trpcmcp.ServerCapabilities{Prompts: &trpcmcp.PromptsCapability{}},
			[]string{trpcmcp.MethodPromptsList}, []string{trpcmcp.MethodPromptsList, trpcmcp.MethodPromptsGet}, 1, false, false},
		{"all", all, []string{trpcmcp.MethodToolsList, trpcmcp.MethodResourcesList, trpcmcp.MethodPromptsList},
			[]string{trpcmcp.MethodToolsList, trpcmcp.MethodToolsCall, trpcmcp.MethodResourcesList,
				trpcmcp.MethodResourcesRead, trpcmcp.MethodResourcesTemplatesList, trpcmcp.MethodPromptsList,
				trpcmcp.MethodPromptsGet, "notifications/tools/list_changed"}, 3, false, false},
		{"no capabilities", trpcmcp.ServerCapabilities{}, nil, nil, 0, false, false},
		{"declared capability error", trpcmcp.ServerCapabilities{Tools: &trpcmcp.ToolsCapability{}},
			[]string{trpcmcp.MethodToolsList}, nil, 0, true, false},
		{"declared capability missing", trpcmcp.ServerCapabilities{Tools: &trpcmcp.ToolsCapability{}},
			[]string{trpcmcp.MethodToolsList}, nil, 0, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Timeout = time.Second
			var mu sync.Mutex
			var lists []string
			initialized := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if len(request.ID) == 0 {
					mu.Lock()
					if request.Method == trpcmcp.MethodNotificationsInitialized {
						initialized++
					}
					mu.Unlock()
					w.WriteHeader(http.StatusAccepted)
					return
				}
				response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
				if request.Method == trpcmcp.MethodInitialize {
					response["result"] = trpcmcp.InitializeResult{ProtocolVersion: cfg.ProtocolVersion,
						ServerInfo: trpcmcp.Implementation{Name: cfg.Service, Version: cfg.ServerVersion}, Capabilities: test.capabilities}
				} else {
					mu.Lock()
					lists = append(lists, request.Method)
					mu.Unlock()
					switch {
					case test.missing:
						response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
					case test.fail:
						response["error"] = map[string]any{"code": -32001, "message": "permission denied"}
					case request.Method == trpcmcp.MethodToolsList && test.capabilities.Tools != nil:
						response["result"] = map[string]any{"tools": []map[string]any{{"name": "lookup", "inputSchema": map[string]string{"type": "object"}}}}
					case request.Method == trpcmcp.MethodResourcesList && test.capabilities.Resources != nil:
						response["result"] = map[string]any{"resources": []map[string]string{{"name": "weather", "uri": "file:///weather.json"}}}
					case request.Method == trpcmcp.MethodPromptsList && test.capabilities.Prompts != nil:
						response["result"] = map[string]any{"prompts": []map[string]string{{"name": "summary"}}}
					default:
						response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			connector, err := NewClient(context.Background(), cfg, server.URL+"/mcp")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connector.Close()) })
			contract, err := CollectContract(context.Background(), connector, cfg)
			mu.Lock()
			observed := append([]string(nil), lists...)
			count := initialized
			mu.Unlock()
			require.Equal(t, test.lists, observed)
			require.Equal(t, 1, count, "initialization must notify exactly once")
			if test.fail {
				if test.missing {
					require.ErrorContains(t, err, "Method not found")
				} else {
					require.ErrorContains(t, err, "permission denied")
				}
				require.Nil(t, contract)
				return
			}
			require.NoError(t, err)
			require.Len(t, contract.ServiceFeatures, test.features)
			var methods []string
			for _, descriptor := range contract.Interfaces {
				methods = append(methods, descriptor.Method)
			}
			require.ElementsMatch(t, test.methods, methods)
			var content struct {
				Notifications []string `json:"notifications"`
			}
			require.NoError(t, json.Unmarshal([]byte(contract.Content), &content))
			if test.name == "all" {
				require.Equal(t, []string{"notifications/resources/list_changed", "notifications/prompts/list_changed"}, content.Notifications)
			} else {
				require.Empty(t, content.Notifications)
			}
		})
	}
}

type externalContractConnector struct{ trpcmcp.Connector }

func TestExternalContractRequiresExplicitCapabilities(t *testing.T) {
	connector := externalContractConnector{Connector: &fakeConnector{}}
	_, err := CollectContract(context.Background(), connector, testConfig())
	require.ErrorContains(t, err, "initialization capabilities are unavailable")
	cfg := testConfig()
	cfg.ServerCapabilities = &trpcmcp.ServerCapabilities{Tools: &trpcmcp.ToolsCapability{}}
	contract, err := CollectContract(context.Background(), connector, cfg)
	require.NoError(t, err)
	require.Len(t, contract.ServiceFeatures, 1)
	require.Equal(t, model.ServiceFeatureTypeMCPTool, contract.ServiceFeatures[0].Type)
}

type failingCapabilityConnector struct{ *fakeConnector }

func (*failingCapabilityConnector) ListTools(context.Context, *trpcmcp.ListToolsRequest) (*trpcmcp.ListToolsResult, error) {
	return nil, context.DeadlineExceeded
}

func TestDeclaredContractCapabilityErrorIsNotIgnored(t *testing.T) {
	_, err := CollectContract(context.Background(), &failingCapabilityConnector{fakeConnector: &fakeConnector{}}, testConfig())
	require.True(t, errors.Is(err, context.DeadlineExceeded))
}

type emptyCapabilityConnector struct{ *fakeConnector }

func (*emptyCapabilityConnector) ServerCapabilities() (trpcmcp.ServerCapabilities, bool) {
	return trpcmcp.ServerCapabilities{}, true
}

func TestContractWithNoCapabilitiesHonorsCancellationAndClose(t *testing.T) {
	connector := manageConnector(&emptyCapabilityConnector{fakeConnector: &fakeConnector{}}, time.Second)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	contract, err := CollectContract(ctx, connector, testConfig())
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, contract)
	contract, err = CollectContract(context.Background(), connector, testConfig())
	require.NoError(t, err)
	require.Empty(t, contract.ServiceFeatures)
	require.NoError(t, connector.Close())
	cfg := testConfig()
	cfg.ServerCapabilities = &trpcmcp.ServerCapabilities{}
	contract, err = CollectContract(context.Background(), connector, cfg)
	require.ErrorContains(t, err, "not initialized or is closed")
	require.Nil(t, contract, "explicit capabilities must not revive a closed adapter")
}
