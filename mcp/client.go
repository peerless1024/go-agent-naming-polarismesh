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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

// NewClient creates and initializes one MCP client for a network endpoint.
// Connector operations are serialized with cancellable, bounded admission.
// Independent Streamable HTTP GET SSE is disabled because the pinned SDK
// accesses its session and event state without synchronization. POST SSE
// responses and the legacy SSE transport remain supported.
// Synchronous callbacks must enqueue requests instead of issuing them through
// the same connector while its current request is still in progress.
func NewClient(
	ctx context.Context,
	config Config,
	endpoint string,
) (trpcmcp.Connector, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.Transport == TransportStdio {
		return nil, errors.New("use NewStdioClient for mcp-stdio")
	}
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("MCP endpoint is required")
	}
	var (
		connector trpcmcp.Connector
		err       error
	)
	options := []trpcmcp.ClientOption{
		trpcmcp.WithHTTPReqHandler(&boundedHTTPHandler{timeout: config.Timeout}),
	}
	options = append(options, config.ClientOptions...)
	options = append(options, trpcmcp.WithProtocolVersion(config.ProtocolVersion))
	if config.Transport == TransportStreamableHTTP {
		options = append(options, trpcmcp.WithClientGetSSEEnabled(false))
	}
	switch config.Transport {
	case TransportSSE:
		connector, err = trpcmcp.NewSSEClient(endpoint, config.ClientInfo, options...)
	case TransportStreamableHTTP:
		connector, err = trpcmcp.NewClient(endpoint, config.ClientInfo, options...)
	default:
		return nil, fmt.Errorf("MCP transport %q is not a network transport", config.Transport)
	}
	if err != nil {
		return nil, fmt.Errorf("create MCP client: %w", err)
	}
	connector = manageConnector(connector, config.Timeout)
	_, err = initializeConnectorBounded(ctx, config, connector)
	if err != nil {
		_ = closeConnectorBounded(connector, config.Timeout)
		return nil, err
	}
	return connector, nil
}

func initializeConnectorBounded(
	ctx context.Context,
	config Config,
	connector trpcmcp.Connector,
) (*trpcmcp.InitializeResult, error) {
	initCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	return initializeConnector(initCtx, config, connector)
}

func initializeConnector(
	ctx context.Context,
	config Config,
	connector trpcmcp.Connector,
) (*trpcmcp.InitializeResult, error) {
	initialized, err := connector.Initialize(ctx, &trpcmcp.InitializeRequest{
		Params: trpcmcp.InitializeParams{
			ProtocolVersion: config.ProtocolVersion,
			ClientInfo:      config.ClientInfo,
			Capabilities:    trpcmcp.ClientCapabilities{},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize MCP client: %w", err)
	}
	if initialized == nil {
		return nil, errors.New("initialize MCP client returned an empty result")
	}
	if err := validateInitializedProtocol(config, initialized); err != nil {
		return nil, err
	}
	if sender, ok := connector.(interface {
		SendInitialized(context.Context) error
	}); ok && connector.GetState() != trpcmcp.StateInitialized {
		if err := sender.SendInitialized(ctx); err != nil {
			return nil, fmt.Errorf("send MCP initialized notification: %w", err)
		}
	}
	return initialized, nil
}

type boundedHTTPHandler struct {
	timeout time.Duration

	mu      sync.Mutex
	clients map[*http.Client]*http.Client
}

func (h *boundedHTTPHandler) Handle(
	ctx context.Context,
	client *http.Client,
	request *http.Request,
) (*http.Response, error) {
	if client == nil {
		return nil, errors.New("MCP HTTP client is required")
	}
	bounded := h.client(client)
	return bounded.Do(request.WithContext(ctx))
}

func (h *boundedHTTPHandler) client(client *http.Client) *http.Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients == nil {
		h.clients = make(map[*http.Client]*http.Client)
	}
	if existing := h.clients[client]; existing != nil {
		return existing
	}
	bounded := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if standard, ok := transport.(*http.Transport); ok {
		cloned := standard.Clone()
		if cloned.ResponseHeaderTimeout <= 0 || cloned.ResponseHeaderTimeout > h.timeout {
			cloned.ResponseHeaderTimeout = h.timeout
		}
		bounded.Transport = cloned
	} else {
		bounded.Transport = responseHeaderTimeoutTransport{
			next:    transport,
			timeout: h.timeout,
		}
	}
	h.clients[client] = &bounded
	return &bounded
}

type responseHeaderTimeoutTransport struct {
	next    http.RoundTripper
	timeout time.Duration
}

func (t responseHeaderTimeoutTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	requestCtx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(requestCtx)
	parentDone := request.Context().Done()
	done := make(chan responseHeaderResult, 1)
	go func() {
		response, err := t.next.RoundTrip(request)
		done <- responseHeaderResult{response: response, err: err}
	}()
	timer := time.NewTimer(t.timeout)
	defer timer.Stop()
	select {
	case completed := <-done:
		if completed.err != nil {
			cancel()
			return nil, completed.err
		}
		if completed.response == nil {
			cancel()
			return nil, errors.New("MCP HTTP transport returned an empty response")
		}
		if completed.response.Body == nil {
			cancel()
			completed.response.Body = io.NopCloser(strings.NewReader(""))
			return completed.response, nil
		}
		completed.response.Body = newCancelableBody(
			completed.response.Body,
			cancel,
			requestCtx,
		)
		return completed.response, nil
	case <-parentDone:
		cancel()
		drainLateResponse(done)
		return nil, request.Context().Err()
	case <-timer.C:
		cancel()
		go func() {
			completed := <-done
			if completed.response != nil && completed.response.Body != nil {
				_ = completed.response.Body.Close()
			}
		}()
		return nil, context.DeadlineExceeded
	}
}

func drainLateResponse(done chan responseHeaderResult) {
	go func() {
		completed := <-done
		if completed.response != nil && completed.response.Body != nil {
			_ = completed.response.Body.Close()
		}
	}()
}

type responseHeaderResult struct {
	response *http.Response
	err      error
}

func newCancelableBody(
	body io.ReadCloser,
	cancel context.CancelFunc,
	ctx context.Context,
) io.ReadCloser {
	closed := make(chan struct{})
	result := &cancelableBody{
		ReadCloser: body,
		cancel:     cancel,
		closed:     closed,
	}
	go func() {
		select {
		case <-ctx.Done():
			result.close()
		case <-closed:
		}
	}()
	return result
}

type cancelableBody struct {
	io.ReadCloser
	once     sync.Once
	cancel   context.CancelFunc
	closed   chan struct{}
	closeErr error
}

func (b *cancelableBody) close() {
	b.once.Do(func() {
		b.closeErr = b.ReadCloser.Close()
		close(b.closed)
		b.cancel()
	})
}

func (b *cancelableBody) Close() error {
	b.close()
	return b.closeErr
}

func closeConnectorBounded(connector trpcmcp.Connector, timeout time.Duration) error {
	if connector == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- connector.Close() }()
	if timeout <= 0 {
		return <-done
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

func validateInitializedProtocol(config Config, initialized *trpcmcp.InitializeResult) error {
	if initialized == nil {
		return errors.New("initialize MCP client returned an empty result")
	}
	if config.StrictVersion && initialized.ProtocolVersion != config.ProtocolVersion {
		return fmt.Errorf(
			"MCP protocol version mismatch: requested %q, server selected %q",
			config.ProtocolVersion,
			initialized.ProtocolVersion,
		)
	}
	return nil
}

// NewStdioClient creates a local stdio MCP client. Stdio never creates a
// Polaris network instance.
func NewStdioClient(
	config Config,
	transportConfig trpcmcp.StdioTransportConfig,
) (trpcmcp.Connector, error) {
	if config.Transport != TransportStdio {
		return nil, errors.New("MCP config transport must be mcp-stdio")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	client, err := trpcmcp.NewStdioClient(
		transportConfig,
		config.ClientInfo,
		trpcmcp.WithStdioProtocolVersion(config.ProtocolVersion),
	)
	if err != nil {
		return nil, err
	}
	return manageConnector(client, config.Timeout), nil
}
