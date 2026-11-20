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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

func sessionTestInstance(t *testing.T, endpoint string, cfg Config) model.Instance {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	metadata, err := cfg.BuildServerMetadata()
	require.NoError(t, err)
	return testutil.InstanceWithMetadata("session-client", host, uint32(port), true, false, metadata)
}

func TestPoolRealSessionClientSerializesRequestsNotificationsAndRetirement(t *testing.T) {
	cfg := testConfig()
	cfg.Timeout = 3 * time.Second
	// The adapter must not allow the SDK's unsynchronized background GET SSE.
	cfg.ClientOptions = []trpcmcp.ClientOption{trpcmcp.WithClientGetSSEEnabled(true)}
	var gets, tools, outbound, inbound atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			gets.Add(1)
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
		w.Header().Set("Mcp-Session-Id", "test-session")
		if len(request.ID) == 0 {
			outbound.Add(1)
			if r.Header.Get("Mcp-Session-Id") != "test-session" {
				t.Error("notification lost the initialized session")
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if request.Method == trpcmcp.MethodInitialize {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": request.ID,
				"result": trpcmcp.InitializeResult{ProtocolVersion: cfg.ProtocolVersion,
					ServerInfo: cfg.ClientInfo, Capabilities: trpcmcp.ServerCapabilities{Tools: &trpcmcp.ToolsCapability{}}},
			})
			return
		}
		if r.Header.Get("Mcp-Session-Id") != "test-session" {
			t.Error("tool call lost the initialized session")
		}
		tools.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "id: notification\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/test\"}\n\n")
		result, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID,
			"result": map[string]any{"content": []map[string]string{{"type": "text", "text": "ok"}}}})
		_, _ = fmt.Fprintf(w, "id: result\ndata: %s\n\n", result)
	}))
	t.Cleanup(server.Close)
	lister := &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{
		testutil.InstancesResponse(sessionTestInstance(t, server.URL, cfg)), testutil.InstancesResponse(),
	}}
	pool, err := NewPool(PoolConfig{Config: cfg, Consumer: lister})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	_, err = pool.Refresh(context.Background())
	require.NoError(t, err)
	connector := pool.clients["session-client"].client
	session, ok := connector.(trpcmcp.SessionClient)
	require.True(t, ok)
	connector.RegisterNotificationHandler("notifications/test", func(*trpcmcp.JSONRPCNotification) error {
		connector.UnregisterNotificationHandler("notifications/unused")
		connector.SetRootsProvider(nil)
		if session.GetSessionID() != "test-session" {
			t.Error("notification callback lost the initialized session")
		}
		inbound.Add(1)
		return nil
	})
	var calls sync.WaitGroup
	start := make(chan struct{})
	const parallel = 24
	for i := 0; i < parallel; i++ {
		calls.Add(3)
		go func() {
			defer calls.Done()
			<-start
			request := &trpcmcp.CallToolRequest{}
			request.Params.Name = "lookup"
			if _, err := pool.CallTool(context.Background(), request); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer calls.Done()
			<-start
			if err := connector.SendRootsListChangedNotification(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer calls.Done()
			<-start
			if session.GetSessionID() != "test-session" {
				t.Error("session getter lost the initialized session")
			}
		}()
	}
	close(start)
	calls.Wait()
	require.EqualValues(t, parallel, tools.Load())
	require.EqualValues(t, parallel, inbound.Load())
	require.EqualValues(t, parallel+1, outbound.Load())
	event, err := pool.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"session-client"}, event.Removed)
	require.Equal(t, trpcmcp.StateDisconnected, connector.GetState())
	require.Error(t, connector.SendRootsListChangedNotification(context.Background()))
	require.Zero(t, gets.Load())
}

func TestManagedConnectorBoundsQueuedCallsAndCancelsOnClose(t *testing.T) {
	cfg := testConfig()
	cfg.Timeout = time.Second
	started, stopped := make(chan struct{}), make(chan struct{})
	var tools atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Mcp-Session-Id", "test-session")
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if request.Method == trpcmcp.MethodToolsCall {
			tools.Add(1)
			close(started)
			<-r.Context().Done()
			close(stopped)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID,
			"result": trpcmcp.InitializeResult{ProtocolVersion: cfg.ProtocolVersion, ServerInfo: cfg.ClientInfo}})
	}))
	t.Cleanup(server.Close)
	connector, err := NewClient(context.Background(), cfg, server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	request := &trpcmcp.CallToolRequest{}
	request.Params.Name = "lookup"
	firstDone := make(chan error, 1)
	go func() { _, err := connector.CallTool(context.Background(), request); firstDone <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first call did not start")
	}
	queuedCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = connector.CallTool(queuedCtx, request)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.EqualValues(t, 1, tools.Load(), "canceled queue entry reached the server")
	start := time.Now()
	require.NoError(t, connector.Close())
	require.Less(t, time.Since(start), 500*time.Millisecond)
	require.Error(t, <-firstDone)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Close left an HTTP call running")
	}
}

func TestPoolRejectsUnsafeBareStreamableFactory(t *testing.T) {
	cfg := testConfig()
	pool, err := NewPool(PoolConfig{
		Config:   cfg,
		Consumer: &testutil.FakeInstanceLister{Responses: []*model.InstancesResponse{testutil.InstancesResponse(mcpInstance())}},
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			return trpcmcp.NewClient("http://127.0.0.1:18080/mcp", cfg.ClientInfo)
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	_, err = pool.Refresh(context.Background())
	require.ErrorContains(t, err, "factories must use mcp.NewClient")
	require.Empty(t, pool.clients)
}

func TestManagedConnectorPreservesCapabilitiesSnapshot(t *testing.T) {
	connector := manageConnector(&fakeConnector{}, time.Second)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	provider, ok := connector.(ServerCapabilitiesProvider)
	require.True(t, ok)
	caps, initialized := provider.ServerCapabilities()
	require.True(t, initialized)
	caps.Tools.ListChanged = false
	copy, initialized := provider.ServerCapabilities()
	require.True(t, initialized)
	require.True(t, copy.Tools.ListChanged)
	_, err := CollectContract(context.Background(), connector, testConfig())
	require.NoError(t, err)
	require.Equal(t, trpcmcp.StateInitialized, connector.GetState())
}

type initializationOutcomeConnector struct {
	*fakeConnector
	state  trpcmcp.State
	result *trpcmcp.InitializeResult
	err    error
}

func (c *initializationOutcomeConnector) GetState() trpcmcp.State { return c.state }

func (c *initializationOutcomeConnector) Initialize(context.Context, *trpcmcp.InitializeRequest) (*trpcmcp.InitializeResult, error) {
	return c.result, c.err
}

func TestManagedConnectorClearsCapabilitiesAfterFailedInitialization(t *testing.T) {
	raw := &initializationOutcomeConnector{fakeConnector: &fakeConnector{}, state: trpcmcp.StateInitialized}
	connector := manageConnector(raw, time.Second)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	provider := connector.(ServerCapabilitiesProvider)
	_, initialized := provider.ServerCapabilities()
	require.True(t, initialized)
	raw.state = trpcmcp.StateDisconnected
	raw.err = errors.New("initialization failed")
	_, err := connector.Initialize(context.Background(), &trpcmcp.InitializeRequest{})
	require.ErrorIs(t, err, raw.err)
	_, initialized = provider.ServerCapabilities()
	require.False(t, initialized, "failed initialization retained stale capabilities")
	raw.state = trpcmcp.StateInitialized
	raw.err = nil
	raw.result = &trpcmcp.InitializeResult{Capabilities: trpcmcp.ServerCapabilities{Prompts: &trpcmcp.PromptsCapability{}}}
	_, err = connector.Initialize(context.Background(), &trpcmcp.InitializeRequest{})
	require.NoError(t, err)
	caps, initialized := provider.ServerCapabilities()
	require.True(t, initialized)
	require.Nil(t, caps.Tools)
	require.NotNil(t, caps.Prompts)
	raw.err = errors.New("already initialized")
	_, err = connector.Initialize(context.Background(), &trpcmcp.InitializeRequest{})
	require.Error(t, err)
	caps, initialized = provider.ServerCapabilities()
	require.True(t, initialized, "duplicate initialization discarded the live session")
	require.NotNil(t, caps.Prompts)
}

func TestManagedConnectorCloseDefersUnsafeCloseAndDrainsOnce(t *testing.T) {
	started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var closes atomic.Int32
	raw := &noCancellationConnector{
		fakeConnector: &fakeConnector{onClose: func() { closes.Add(1); close(closed) }},
		started:       started, release: release,
	}
	connector := manageConnector(raw, 30*time.Millisecond)
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = connector.CallTool(context.Background(), &trpcmcp.CallToolRequest{})
	}()
	<-started
	closeResults := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { closeResults <- connector.Close() }()
	}
	require.ErrorIs(t, <-closeResults, context.DeadlineExceeded)
	require.ErrorIs(t, <-closeResults, context.DeadlineExceeded)
	require.Zero(t, closes.Load(), "raw Close overlapped a context-ignoring request")
	close(release)
	<-callDone
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("raw Close did not resume after the active request")
	}
	require.NoError(t, connector.Close())
	require.EqualValues(t, 1, closes.Load())
}
