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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

func TestPrepareServerMetadataAndStdioBoundary(t *testing.T) {
	config := testConfig()
	prepared, err := PrepareServer(context.Background(), config)
	require.NoError(t, err)
	require.True(t, prepared.Network)
	require.Equal(t, config.Service, prepared.Metadata[model.MCPMetadataKeyServerName])
	require.Equal(t, config.ProtocolVersion, prepared.Metadata[model.MCPMetadataKeyProtocolVersion])
	require.Equal(t, DefaultStreamableHTTPPath, prepared.Metadata[model.MCPMetadataKeyEndpointPath])

	registrar := &fakeRegistrar{}
	registered, err := prepared.RegisterMetadata(registrar)
	require.NoError(t, err)
	require.True(t, registered)
	require.Equal(t, 1, registrar.count)

	stdio := config
	stdio.Transport = TransportStdio
	stdio.Host = ""
	stdio.Port = 0
	stdio.EndpointPath = ""
	stdinPrepared, err := PrepareServer(context.Background(), stdio)
	require.NoError(t, err)
	require.False(t, stdinPrepared.Network)
	registered, err = stdinPrepared.RegisterMetadata(registrar)
	require.NoError(t, err)
	require.False(t, registered)
	require.Equal(t, 1, registrar.count)
}

func TestCollectAndReportContractMapsFeaturesAndInterfaces(t *testing.T) {
	connector := &fakeConnector{}
	config := testConfig()
	contract, err := CollectContract(context.Background(), connector, config)
	require.NoError(t, err)
	require.Len(t, contract.ServiceFeatures, 3)
	require.Equal(t, model.ServiceFeatureTypeMCPTool, contract.ServiceFeatures[0].Type)
	require.Equal(t, model.ServiceFeatureTypeMCPResource, contract.ServiceFeatures[1].Type)
	require.Equal(t, model.ServiceFeatureTypeMCPPrompt, contract.ServiceFeatures[2].Type)
	require.NotEmpty(t, contract.Interfaces)
	require.Len(t, contract.ContentDigest, 40)
	require.Equal(t, mcpContractType, contract.Type)
	for _, descriptor := range contract.Interfaces {
		require.LessOrEqual(t, utf8.RuneCountInString(descriptor.Method), maxContractInterfaceMethodRunes)
		require.Equal(t, mcpContractType, descriptor.Type)
	}
	var content struct {
		Notifications []string `json:"notifications"`
	}
	require.NoError(t, json.Unmarshal([]byte(contract.Content), &content))
	require.ElementsMatch(t, []string{
		"notifications/resources/list_changed",
		"notifications/prompts/list_changed",
	}, content.Notifications)

	provider := &fakeProviderAPI{}
	reported, err := ReportContract(context.Background(), provider, contract)
	require.NoError(t, err)
	require.Empty(t, reported.ID)
	require.Equal(t, contract.Service, provider.reported.Service)
}

func TestContractReportFailurePolicy(t *testing.T) {
	contract, err := CollectContract(context.Background(), &fakeConnector{}, testConfig())
	require.NoError(t, err)
	provider := &fakeProviderAPI{reportErr: errors.New("contract persistence unavailable")}

	strict := testConfig()
	_, err = ReportContractWithConfig(context.Background(), provider, contract, strict)
	require.ErrorContains(t, err, "contract persistence unavailable")

	bestEffort := testConfig()
	bestEffort.ContractReportPolicy = ContractReportWarn
	reported, err := ReportContractWithConfig(context.Background(), provider, contract, bestEffort)
	require.NoError(t, err)
	require.Empty(t, reported.ID)

	bestEffort.ContractReportPolicy = "unsupported"
	_, err = ReportContractWithConfig(context.Background(), provider, contract, bestEffort)
	require.ErrorContains(t, err, "not supported")
}

func TestGetContractUsesConsumerServiceRuleResponse(t *testing.T) {
	config := testConfig()
	consumer := &fakeConsumerAPI{
		response: &model.ServiceRuleResponse{
			Type: model.EventServiceContract,
			Service: model.ServiceKey{
				Namespace: config.Namespace,
				Service:   config.Service,
			},
			Revision: "revision-1",
			Value: &model.ServiceContract{
				GetServiceContractRequest: model.GetServiceContractRequest{
					Namespace: config.Namespace,
					Service:   config.Service,
					Name:      mcpContractType,
					Type:      mcpContractType,
					Protocol:  string(config.Transport),
					Version:   config.ServerVersion,
				},
				Revision: "revision-1",
			},
		},
	}

	response, err := GetContractWithConfig(context.Background(), consumer, config)
	require.NoError(t, err)
	require.Equal(t, "revision-1", response.Revision)
	contract, ok := response.Value.(*model.ServiceContract)
	require.True(t, ok)
	require.Equal(t, mcpContractType, contract.Name)
	require.Equal(t, mcpContractType, contract.Type)
	require.NotNil(t, consumer.request)
	require.Equal(t, config.Namespace, consumer.request.Namespace)
	require.Equal(t, config.Service, consumer.request.Service)
	require.Equal(t, mcpContractType, consumer.request.Name)
	require.Equal(t, mcpContractType, consumer.request.Type)
	require.Equal(t, string(config.Transport), consumer.request.Protocol)
	require.Equal(t, config.ServerVersion, consumer.request.Version)
	require.NotNil(t, consumer.request.Timeout)
	require.Equal(t, config.Timeout, *consumer.request.Timeout)
}

func TestPaginationRejectsRepeatedAndCyclicCursors(t *testing.T) {
	seen := make(map[trpcmcp.Cursor]struct{})
	_, _, err := nextPageCursor("tools", "same", "same", seen)
	require.ErrorContains(t, err, "repeatedly")

	next, done, err := nextPageCursor("tools", "", "a", seen)
	require.NoError(t, err)
	require.False(t, done)
	next, done, err = nextPageCursor("tools", next, "b", seen)
	require.NoError(t, err)
	require.False(t, done)
	_, _, err = nextPageCursor("tools", next, "a", seen)
	require.ErrorContains(t, err, "cursor cycle")
}

func TestClientProtocolVersionStrictness(t *testing.T) {
	config := testConfig()
	config.ProtocolVersion = trpcmcp.ProtocolVersion_2025_03_26

	strict := config
	initialized := &trpcmcp.InitializeResult{
		ProtocolVersion: trpcmcp.ProtocolVersion_2024_11_05,
	}
	err := validateInitializedProtocol(strict, initialized)
	require.ErrorContains(t, err, "protocol version mismatch")

	strict.StrictVersion = false
	require.NoError(t, validateInitializedProtocol(strict, initialized))
}

func TestPoolRefreshAndCallTool(t *testing.T) {
	config := testConfig()
	config.Host = ""
	config.Port = 0
	lister := &listerSequence{responses: []*model.InstancesResponse{
		testutil.InstancesResponse(mcpInstance()),
		testutil.InstancesResponse(),
	}}
	var mu sync.Mutex
	var created, closed int
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Consumer: lister,
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			mu.Lock()
			created++
			mu.Unlock()
			return &fakeConnector{onClose: func() {
				mu.Lock()
				closed++
				mu.Unlock()
			}}, nil
		},
	})
	require.NoError(t, err)

	event, err := pool.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"mcp-1"}, event.Added)
	result, err := pool.CallTool(context.Background(), &trpcmcp.CallToolRequest{})
	require.NoError(t, err)
	require.NotNil(t, result)

	event, err = pool.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"mcp-1"}, event.Removed)
	require.NoError(t, pool.Close())
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, created)
	require.Equal(t, 1, closed)
}

type slowCallConnector struct {
	*fakeConnector
	started chan struct{}
	release chan struct{}
}

func (c *slowCallConnector) CallTool(
	ctx context.Context,
	_ *trpcmcp.CallToolRequest,
) (*trpcmcp.CallToolResult, error) {
	close(c.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.release:
		return &trpcmcp.CallToolResult{}, nil
	}
}

func TestPoolCallToolTimeoutUnblocksClose(t *testing.T) {
	config := testConfig()
	config.Host, config.Port = "", 0
	config.Timeout = 20 * time.Millisecond
	connector := &slowCallConnector{
		fakeConnector: &fakeConnector{},
		started:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Consumer: &listerSequence{responses: []*model.InstancesResponse{testutil.InstancesResponse(mcpInstance())}},
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			return connector, nil
		},
	})
	require.NoError(t, err)
	_, err = pool.Refresh(context.Background())
	require.NoError(t, err)

	callDone := make(chan error, 1)
	go func() {
		_, err := pool.CallTool(context.Background(), &trpcmcp.CallToolRequest{})
		callDone <- err
	}()
	<-connector.started
	closeDone := make(chan error, 1)
	go func() { closeDone <- pool.Close() }()

	select {
	case err := <-callDone:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(150 * time.Millisecond):
		close(connector.release)
		<-callDone
		t.Fatal("MCP CallTool did not honor Config.Timeout")
	}
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(150 * time.Millisecond):
		close(connector.release)
		<-closeDone
		t.Fatal("MCP Close did not complete after the active call timed out")
	}
}

type noCancellationConnector struct {
	*fakeConnector
	started chan struct{}
	release chan struct{}
}

func (c *noCancellationConnector) CallTool(
	context.Context,
	*trpcmcp.CallToolRequest,
) (*trpcmcp.CallToolResult, error) {
	close(c.started)
	<-c.release
	return &trpcmcp.CallToolResult{}, nil
}

func TestPoolCloseWaitsForActiveCallBeforeClosingConnector(t *testing.T) {
	config := testConfig()
	config.Host, config.Port = "", 0
	config.Timeout = time.Second
	started := make(chan struct{})
	release := make(chan struct{})
	closed := make(chan struct{})
	connector := &noCancellationConnector{
		fakeConnector: &fakeConnector{onClose: func() { close(closed) }},
		started:       started,
		release:       release,
	}
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Consumer: &listerSequence{responses: []*model.InstancesResponse{testutil.InstancesResponse(mcpInstance())}},
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			return connector, nil
		},
	})
	require.NoError(t, err)
	_, err = pool.Refresh(context.Background())
	require.NoError(t, err)

	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = pool.CallTool(context.Background(), &trpcmcp.CallToolRequest{})
	}()
	<-connector.started
	closeDone := make(chan error, 1)
	go func() { closeDone <- pool.Close() }()
	select {
	case <-closed:
		t.Fatal("connector closed while a tool call was still active")
	case <-time.After(30 * time.Millisecond):
	}
	close(connector.release)
	<-callDone
	require.NoError(t, <-closeDone)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connector was not closed after the active call completed")
	}
}

func TestPoolCloseTimeoutDefersConnectorCloseUntilActiveCallReturns(t *testing.T) {
	config := testConfig()
	config.Host, config.Port = "", 0
	config.Timeout = 20 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	closed := make(chan struct{})
	connector := &noCancellationConnector{
		fakeConnector: &fakeConnector{onClose: func() { close(closed) }},
		started:       started,
		release:       release,
	}
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Consumer: &listerSequence{responses: []*model.InstancesResponse{testutil.InstancesResponse(mcpInstance())}},
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			return connector, nil
		},
	})
	require.NoError(t, err)
	_, err = pool.Refresh(context.Background())
	require.NoError(t, err)

	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = pool.CallTool(context.Background(), &trpcmcp.CallToolRequest{})
	}()
	<-connector.started
	require.ErrorIs(t, pool.Close(), context.DeadlineExceeded)
	select {
	case <-closed:
		t.Fatal("connector Close started while CallTool was still active")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-callDone
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connector was not closed after the active call returned")
	}
}

type toolErrorConnector struct {
	*fakeConnector
}

func (c *toolErrorConnector) CallTool(
	context.Context,
	*trpcmcp.CallToolRequest,
) (*trpcmcp.CallToolResult, error) {
	return &trpcmcp.CallToolResult{IsError: true}, nil
}

type callResultReporter struct {
	api.ConsumerAPI
	report *api.ServiceCallResult
}

func (r *callResultReporter) UpdateServiceCallResult(report *api.ServiceCallResult) error {
	r.report = report
	return nil
}

func TestPoolReportsMCPResultErrorAsFailure(t *testing.T) {
	config := testConfig()
	config.Host, config.Port = "", 0
	reporter := &callResultReporter{}
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Reporter: reporter,
		Consumer: &listerSequence{responses: []*model.InstancesResponse{testutil.InstancesResponse(mcpInstance())}},
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			return &toolErrorConnector{fakeConnector: &fakeConnector{}}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	_, err = pool.Refresh(context.Background())
	require.NoError(t, err)

	result, err := pool.CallTool(context.Background(), &trpcmcp.CallToolRequest{})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.NotNil(t, reporter.report)
	require.Equal(t, model.RetFail, reporter.report.RetStatus)
	require.Equal(t, int32(-1), *reporter.report.RetCode)
}

func TestClientInitializationHonorsTimeoutWhileWaitingForHeaders(t *testing.T) {
	for _, transport := range []Transport{TransportStreamableHTTP, TransportSSE} {
		t.Run(string(transport), func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				select {
				case <-started:
				default:
					close(started)
				}
				<-release
				http.Error(w, "released test endpoint", http.StatusBadRequest)
			}))
			defer server.Close()

			config := testConfig()
			config.Transport = transport
			config.Timeout = 20 * time.Millisecond
			done := make(chan error, 1)
			go func() {
				client, err := NewClient(context.Background(), config, server.URL)
				if client != nil {
					_ = client.Close()
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("test server did not receive the initialization request")
			}

			finished := false
			select {
			case err := <-done:
				finished = true
				require.Error(t, err)
			case <-time.After(150 * time.Millisecond):
			}
			close(release)
			if !finished {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("MCP initialization remained blocked after releasing the test endpoint")
				}
			}
			if !finished {
				t.Fatalf("%s initialization exceeded Config.Timeout", transport)
			}
		})
	}
}

func TestBoundedHTTPHandlerDoesNotCutSuccessfulStreamBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(60 * time.Millisecond)
		_, _ = io.WriteString(w, "stream-body")
	}))
	defer server.Close()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := (&boundedHTTPHandler{timeout: 20 * time.Millisecond}).Handle(
		context.Background(),
		&http.Client{},
		request,
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "stream-body", string(body))
}

type lateResponseTransport struct {
	release    chan struct{}
	bodyClosed chan struct{}
}

func (t *lateResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	<-t.release
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: &trackingBody{
			ReadCloser: io.NopCloser(strings.NewReader("late")),
			closed:     t.bodyClosed,
		},
		Request: request,
	}, nil
}

type trackingBody struct {
	io.ReadCloser
	closed chan struct{}
}

func (b *trackingBody) Close() error {
	err := b.ReadCloser.Close()
	close(b.closed)
	return err
}

func TestBoundedHTTPHandlerCancelsAndCleansLateCustomTransportResponse(t *testing.T) {
	transport := &lateResponseTransport{
		release:    make(chan struct{}),
		bodyClosed: make(chan struct{}),
	}
	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:18080/mcp", nil)
	require.NoError(t, err)

	_, err = (&boundedHTTPHandler{timeout: 20 * time.Millisecond}).Handle(
		context.Background(),
		&http.Client{Transport: transport},
		request,
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(transport.release)
	select {
	case <-transport.bodyClosed:
	case <-time.After(time.Second):
		t.Fatal("late custom-transport response body was not closed")
	}
}

func TestPoolCanceledRefreshPreservesClientAndClosesReplacement(t *testing.T) {
	config := testConfig()
	config.Host, config.Port = "", 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lister := &listerSequence{responses: []*model.InstancesResponse{
		testutil.InstancesResponse(mcpInstance()),
		testutil.InstancesResponse(mcpInstanceWithPath("/changed")),
	}}
	var created, closed atomic.Int32
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Consumer: lister,
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			if created.Add(1) == 2 {
				cancel()
			}
			return &fakeConnector{onClose: func() { closed.Add(1) }}, nil
		},
	})
	require.NoError(t, err)

	_, err = pool.Refresh(context.Background())
	require.NoError(t, err)
	_, err = pool.Refresh(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, pool.order, 1)
	require.EqualValues(t, 2, created.Load())
	require.EqualValues(t, 1, closed.Load())
	require.NoError(t, pool.Close())
	require.EqualValues(t, 2, closed.Load())
}

func TestPoolRefreshClosesUnusedUnchangedClient(t *testing.T) {
	config := testConfig()
	config.Host = ""
	config.Port = 0
	lister := &listerSequence{responses: []*model.InstancesResponse{
		testutil.InstancesResponse(mcpInstance()),
		testutil.InstancesResponse(mcpInstance()),
	}}
	var mu sync.Mutex
	var created, closed int
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Consumer: lister,
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			mu.Lock()
			created++
			mu.Unlock()
			return &fakeConnector{onClose: func() {
				mu.Lock()
				closed++
				mu.Unlock()
			}}, nil
		},
	})
	require.NoError(t, err)
	_, err = pool.Refresh(context.Background())
	require.NoError(t, err)
	event, err := pool.Refresh(context.Background())
	require.NoError(t, err)
	require.Empty(t, event.Added)
	require.Empty(t, event.Removed)
	require.Empty(t, event.Replaced)
	mu.Lock()
	require.Equal(t, 1, created)
	require.Equal(t, 0, closed)
	mu.Unlock()
	require.NoError(t, pool.Close())
	mu.Lock()
	require.Equal(t, 1, closed)
	mu.Unlock()
}

func TestPoolRefreshReplacesChangedMCPTarget(t *testing.T) {
	config := testConfig()
	config.Host = ""
	config.Port = 0
	lister := &listerSequence{responses: []*model.InstancesResponse{
		testutil.InstancesResponse(mcpInstanceWithPath(DefaultStreamableHTTPPath)),
		testutil.InstancesResponse(mcpInstanceWithPath("/mcp-v2")),
	}}
	var mu sync.Mutex
	var created, closed int
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Consumer: lister,
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			mu.Lock()
			created++
			mu.Unlock()
			return &fakeConnector{onClose: func() {
				mu.Lock()
				closed++
				mu.Unlock()
			}}, nil
		},
	})
	require.NoError(t, err)
	_, err = pool.Refresh(context.Background())
	require.NoError(t, err)
	event, err := pool.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"mcp-1"}, event.Replaced)
	mu.Lock()
	require.Equal(t, 2, created)
	require.Equal(t, 1, closed)
	mu.Unlock()
	require.NoError(t, pool.Close())
	mu.Lock()
	require.Equal(t, 2, closed)
	mu.Unlock()
}

func TestPoolRejectsMismatchedInstanceProtocol(t *testing.T) {
	config := testConfig()
	config.Host = ""
	config.Port = 0
	instance := testutil.InstanceWithProtocolMetadata(
		"mcp-sse",
		"127.0.0.1",
		"mcp-sse",
		18080,
		true,
		false,
		mcpInstance().GetMetadata(),
	)
	pool, err := NewPool(PoolConfig{
		Config:   config,
		Consumer: &listerSequence{responses: []*model.InstancesResponse{testutil.InstancesResponse(instance)}},
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			t.Fatal("client factory must not run for a mismatched protocol")
			return nil, nil
		},
	})
	require.NoError(t, err)
	event, err := pool.Refresh(context.Background())
	require.NoError(t, err)
	require.Empty(t, event.Added)
	_, err = pool.CallTool(context.Background(), &trpcmcp.CallToolRequest{})
	require.ErrorContains(t, err, "no healthy MCP instances")
	require.NoError(t, pool.Close())
}

func TestWatcherReportsChangesAndHonorsCancellation(t *testing.T) {
	pool, err := NewPool(PoolConfig{
		Config: func() Config {
			config := testConfig()
			config.Host, config.Port = "", 0
			return config
		}(),
		Consumer: &listerSequence{responses: []*model.InstancesResponse{
			testutil.InstancesResponse(mcpInstance()),
		}},
		ClientFactory: func(context.Context, Config, string) (trpcmcp.Connector, error) {
			return &fakeConnector{}, nil
		},
	})
	require.NoError(t, err)
	watcher := &Watcher{Pool: pool, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	changed := make(chan InstanceEvent, 1)
	done := make(chan error, 1)
	go func() {
		done <- watcher.Watch(ctx, func(event InstanceEvent) {
			select {
			case changed <- event:
			default:
			}
		})
	}()
	select {
	case event := <-changed:
		require.Equal(t, []string{"mcp-1"}, event.Added)
	case <-time.After(time.Second):
		t.Fatal("watcher did not report initial instance")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.NoError(t, pool.Close())
}

func testConfig() Config {
	config := DefaultConfig()
	config.Namespace = "Production"
	config.Service = "weather-mcp"
	config.ServerName = "weather-mcp"
	config.ProtocolVersion = trpcmcp.ProtocolVersion_2025_03_26
	config.Host = "127.0.0.1"
	config.Port = 18080
	return config
}

func mcpInstance() model.Instance {
	return mcpInstanceWithPath(DefaultStreamableHTTPPath)
}

func mcpInstanceWithPath(path string) model.Instance {
	return testutil.InstanceWithMetadata(
		"mcp-1",
		"127.0.0.1",
		18080,
		true,
		false,
		map[string]string{
			model.MCPMetadataKeyServerName:      "weather-mcp",
			model.MCPMetadataKeyProtocolVersion: trpcmcp.ProtocolVersion_2025_03_26,
			model.MCPMetadataKeyEndpointPath:    path,
		},
	)
}

type fakeProviderAPI struct {
	api.ProviderAPI
	reported  *model.ServiceContract
	reportErr error
}

type fakeConsumerAPI struct {
	api.ConsumerAPI
	request  *api.GetServiceContractRequest
	response *model.ServiceRuleResponse
	err      error
}

func (f *fakeConsumerAPI) GetServiceContract(
	req *api.GetServiceContractRequest,
) (*model.ServiceRuleResponse, error) {
	copied := *req
	f.request = &copied
	return f.response, f.err
}

func (f *fakeProviderAPI) ReportServiceContract(
	req *api.ReportServiceContractRequest,
) (*model.ReportServiceContractResponse, error) {
	if f.reportErr != nil {
		return nil, f.reportErr
	}
	f.reported = model.CloneServiceContract(req.ServiceContract)
	return &model.ReportServiceContractResponse{}, nil
}

type fakeRegistrar struct {
	count int
}

func (f *fakeRegistrar) RegisterMetadataProvider(
	string,
	adapternaming.MetadataProvider,
) error {
	f.count++
	return nil
}

func (*fakeRegistrar) UnregisterMetadataProvider(string) {}

type fakeConnector struct {
	trpcmcp.Connector
	onClose func()
}

func (*fakeConnector) ServerCapabilities() (trpcmcp.ServerCapabilities, bool) {
	return trpcmcp.ServerCapabilities{
		Tools:     &trpcmcp.ToolsCapability{ListChanged: true},
		Resources: &trpcmcp.ResourcesCapability{ListChanged: true},
		Prompts:   &trpcmcp.PromptsCapability{ListChanged: true},
	}, true
}

func (*fakeConnector) Initialize(
	context.Context,
	*trpcmcp.InitializeRequest,
) (*trpcmcp.InitializeResult, error) {
	return &trpcmcp.InitializeResult{ProtocolVersion: trpcmcp.ProtocolVersion_2025_03_26}, nil
}

func (f *fakeConnector) Close() error {
	if f.onClose != nil {
		f.onClose()
	}
	return nil
}

func (*fakeConnector) GetState() trpcmcp.State { return trpcmcp.StateInitialized }

func (*fakeConnector) ListTools(
	context.Context,
	*trpcmcp.ListToolsRequest,
) (*trpcmcp.ListToolsResult, error) {
	return &trpcmcp.ListToolsResult{Tools: []trpcmcp.Tool{{Name: "lookup", Description: "Lookup"}}}, nil
}

func (*fakeConnector) CallTool(
	context.Context,
	*trpcmcp.CallToolRequest,
) (*trpcmcp.CallToolResult, error) {
	return &trpcmcp.CallToolResult{}, nil
}

func (*fakeConnector) ListPrompts(
	context.Context,
	*trpcmcp.ListPromptsRequest,
) (*trpcmcp.ListPromptsResult, error) {
	return &trpcmcp.ListPromptsResult{Prompts: []trpcmcp.Prompt{{Name: "summary"}}}, nil
}

func (*fakeConnector) GetPrompt(
	context.Context,
	*trpcmcp.GetPromptRequest,
) (*trpcmcp.GetPromptResult, error) {
	return nil, errors.New("not implemented")
}

func (*fakeConnector) ListResources(
	context.Context,
	*trpcmcp.ListResourcesRequest,
) (*trpcmcp.ListResourcesResult, error) {
	return &trpcmcp.ListResourcesResult{Resources: []trpcmcp.Resource{{
		Name: "weather", URI: "file:///weather.json",
	}}}, nil
}

func (*fakeConnector) ReadResource(
	context.Context,
	*trpcmcp.ReadResourceRequest,
) (*trpcmcp.ReadResourceResult, error) {
	return nil, errors.New("not implemented")
}

func (*fakeConnector) RegisterNotificationHandler(string, trpcmcp.NotificationHandler) {}
func (*fakeConnector) UnregisterNotificationHandler(string)                            {}
func (*fakeConnector) SetRootsProvider(trpcmcp.RootsProvider)                          {}
func (*fakeConnector) SendRootsListChangedNotification(context.Context) error          { return nil }

type listerSequence struct {
	mu        sync.Mutex
	responses []*model.InstancesResponse
	calls     int
}

func (l *listerSequence) GetInstances(*api.GetInstancesRequest) (*model.InstancesResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	index := l.calls
	l.calls++
	if len(l.responses) == 0 {
		return &model.InstancesResponse{}, nil
	}
	if index >= len(l.responses) {
		return l.responses[len(l.responses)-1], nil
	}
	return l.responses[index], nil
}
