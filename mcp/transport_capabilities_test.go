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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

func TestLegacySSEClientRetainsInitializationCapabilities(t *testing.T) {
	cfg := testConfig()
	cfg.Transport = TransportSSE
	cfg.ProtocolVersion = trpcmcp.ProtocolVersion_2024_11_05
	cfg.Timeout = time.Second
	responses := make(chan []byte, 8)
	streamClosed := make(chan struct{})
	var initialized atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			defer close(streamClosed)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: endpoint\ndata: /messages\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case <-r.Context().Done():
					return
				case response := <-responses:
					_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", response)
					w.(http.Flusher).Flush()
				}
			}
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
			initialized.Add(1)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch request.Method {
		case trpcmcp.MethodInitialize:
			result = trpcmcp.InitializeResult{ProtocolVersion: cfg.ProtocolVersion,
				ServerInfo: cfg.ClientInfo, Capabilities: trpcmcp.ServerCapabilities{Tools: &trpcmcp.ToolsCapability{}}}
		case trpcmcp.MethodToolsList:
			result = map[string]any{"tools": []map[string]any{{"name": "lookup", "inputSchema": map[string]string{"type": "object"}}}}
		default:
			t.Errorf("unexpected request for undeclared capability: %s", request.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		response, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		if err != nil {
			t.Error(err)
			return
		}
		responses <- response
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	connector, err := NewClient(context.Background(), cfg, server.URL+"/sse")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	contract, err := CollectContract(context.Background(), connector, cfg)
	require.NoError(t, err)
	require.Len(t, contract.ServiceFeatures, 1)
	require.EqualValues(t, 1, initialized.Load())
	require.NoError(t, connector.Close())
	select {
	case <-streamClosed:
	case <-time.After(time.Second):
		t.Fatal("legacy SSE stream did not stop on Close")
	}
}

func TestStdioClientRetainsInitializationCapabilities(t *testing.T) {
	cfg := testConfig()
	cfg.Transport = TransportStdio
	cfg.Host, cfg.Port, cfg.EndpointPath = "", 0, ""
	cfg.Timeout = 3 * time.Second
	executable, err := os.Executable()
	require.NoError(t, err)
	connector, err := NewStdioClient(cfg, trpcmcp.StdioTransportConfig{
		Timeout: cfg.Timeout,
		ServerParams: trpcmcp.StdioServerParameters{
			Command: executable, Args: []string{"-test.run=^TestMCPStdioCapabilitiesHelper$"},
			Env: map[string]string{"MCP_CAPABILITIES_HELPER": "1"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	provider, ok := connector.(ServerCapabilitiesProvider)
	require.True(t, ok)
	_, initialized := provider.ServerCapabilities()
	require.False(t, initialized)
	_, err = CollectContract(context.Background(), connector, cfg)
	require.ErrorContains(t, err, "initialization capabilities are unavailable")
	result, err := connector.Initialize(context.Background(), &trpcmcp.InitializeRequest{
		Params: trpcmcp.InitializeParams{ProtocolVersion: cfg.ProtocolVersion, ClientInfo: cfg.ClientInfo},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Capabilities.Tools)
	process, ok := connector.(trpcmcp.ProcessClient)
	require.True(t, ok)
	require.Positive(t, process.GetProcessID())
	require.True(t, process.IsProcessRunning())
	require.Equal(t, executable, process.GetCommandLine()[0])
	contract, err := CollectContract(context.Background(), connector, cfg)
	require.NoError(t, err)
	require.Len(t, contract.ServiceFeatures, 1)
	require.NoError(t, connector.Close())
	require.False(t, process.IsProcessRunning())
}

func TestMCPStdioCapabilitiesHelper(t *testing.T) {
	if os.Getenv("MCP_CAPABILITIES_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.ID) == 0 {
			continue
		}
		var result any
		switch request.Method {
		case trpcmcp.MethodInitialize:
			result = trpcmcp.InitializeResult{ProtocolVersion: request.Params.ProtocolVersion,
				ServerInfo:   trpcmcp.Implementation{Name: "stdio-helper", Version: "1.0.0"},
				Capabilities: trpcmcp.ServerCapabilities{Tools: &trpcmcp.ToolsCapability{}}}
		case trpcmcp.MethodToolsList:
			result = map[string]any{"tools": []map[string]any{{"name": "lookup", "inputSchema": map[string]string{"type": "object"}}}}
		default:
			os.Exit(2)
		}
		if encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
			os.Exit(3)
		}
	}
	os.Exit(0)
}
