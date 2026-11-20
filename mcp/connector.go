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
	"sync"
	"time"

	"github.com/mohae/deepcopy"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

// ServerCapabilitiesProvider exposes the capabilities of a completed MCP
// initialization. The boolean is false until initialization succeeds.
type ServerCapabilitiesProvider interface {
	ServerCapabilities() (trpcmcp.ServerCapabilities, bool)
}

type managedConnector struct {
	connector trpcmcp.Connector
	timeout   time.Duration
	gate      chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc

	mu           sync.RWMutex
	state        trpcmcp.State
	sessionID    string
	capabilities *trpcmcp.ServerCapabilities
	closed       bool
	closeOnce    sync.Once
	closeDone    chan struct{}
	closeErr     error
}

func manageConnector(connector trpcmcp.Connector, timeout time.Duration) trpcmcp.Connector {
	if _, ok := connector.(interface{ managedMCPConnector() }); ok {
		return connector
	}
	ctx, cancel := context.WithCancel(context.Background())
	managed := &managedConnector{
		connector: connector, timeout: timeout, gate: make(chan struct{}, 1),
		ctx: ctx, cancel: cancel, state: connector.GetState(), closeDone: make(chan struct{}),
	}
	managed.gate <- struct{}{}
	if provider, ok := connector.(ServerCapabilitiesProvider); ok {
		if capabilities, initialized := provider.ServerCapabilities(); initialized {
			copied := cloneServerCapabilities(capabilities)
			managed.capabilities = &copied
		}
	}
	if session, ok := connector.(trpcmcp.SessionClient); ok {
		managed.sessionID = session.GetSessionID()
		return &managedSessionConnector{managedConnector: managed, session: session}
	}
	if process, ok := connector.(trpcmcp.ProcessClient); ok {
		return &managedProcessConnector{managedConnector: managed, process: process}
	}
	return managed
}

func (*managedConnector) managedMCPConnector() {}

func (c *managedConnector) acquire(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	stopClose := context.AfterFunc(c.ctx, cancel)
	select {
	case <-ctx.Done():
		stopClose()
		cancel()
		return nil, nil, ctx.Err()
	case <-c.gate:
	}
	release := func() {
		stopClose()
		cancel()
		c.gate <- struct{}{}
	}
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		release()
		return nil, nil, errors.New("MCP connector is closed")
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, nil, err
	}
	return ctx, release, nil
}

func connectorCall[T any](c *managedConnector, ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	callCtx, release, err := c.acquire(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	defer release()
	result, err := call(callCtx)
	if session, ok := c.connector.(trpcmcp.SessionClient); ok {
		id := session.GetSessionID()
		c.mu.Lock()
		c.sessionID = id
		c.mu.Unlock()
	}
	return result, err
}

func (c *managedConnector) Initialize(ctx context.Context, req *trpcmcp.InitializeRequest) (*trpcmcp.InitializeResult, error) {
	return connectorCall(c, ctx, func(ctx context.Context) (*trpcmcp.InitializeResult, error) {
		result, err := c.connector.Initialize(ctx, req)
		state := c.connector.GetState()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.state = state
		if err == nil && result != nil {
			capabilities := cloneServerCapabilities(result.Capabilities)
			c.capabilities = &capabilities
		} else if state != trpcmcp.StateInitialized {
			c.capabilities = nil
		}
		return result, err
	})
}

func (c *managedConnector) ServerCapabilities() (trpcmcp.ServerCapabilities, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.capabilities == nil || c.closed {
		return trpcmcp.ServerCapabilities{}, false
	}
	return cloneServerCapabilities(*c.capabilities), true
}

func cloneServerCapabilities(capabilities trpcmcp.ServerCapabilities) trpcmcp.ServerCapabilities {
	return deepcopy.Copy(capabilities).(trpcmcp.ServerCapabilities)
}

func (c *managedConnector) GetState() trpcmcp.State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return trpcmcp.StateDisconnected
	}
	return c.state
}

func (c *managedConnector) ListTools(ctx context.Context, req *trpcmcp.ListToolsRequest) (*trpcmcp.ListToolsResult, error) {
	if req == nil {
		return nil, errors.New("MCP list tools request is required")
	}
	return connectorCall(c, ctx, func(ctx context.Context) (*trpcmcp.ListToolsResult, error) {
		return c.connector.ListTools(ctx, req)
	})
}

func (c *managedConnector) CallTool(ctx context.Context, req *trpcmcp.CallToolRequest) (*trpcmcp.CallToolResult, error) {
	if req == nil {
		return nil, errors.New("MCP tool request is required")
	}
	return connectorCall(c, ctx, func(ctx context.Context) (*trpcmcp.CallToolResult, error) {
		return c.connector.CallTool(ctx, req)
	})
}

func (c *managedConnector) ListPrompts(ctx context.Context, req *trpcmcp.ListPromptsRequest) (*trpcmcp.ListPromptsResult, error) {
	if req == nil {
		return nil, errors.New("MCP list prompts request is required")
	}
	return connectorCall(c, ctx, func(ctx context.Context) (*trpcmcp.ListPromptsResult, error) {
		return c.connector.ListPrompts(ctx, req)
	})
}

func (c *managedConnector) GetPrompt(ctx context.Context, req *trpcmcp.GetPromptRequest) (*trpcmcp.GetPromptResult, error) {
	if req == nil {
		return nil, errors.New("MCP prompt request is required")
	}
	return connectorCall(c, ctx, func(ctx context.Context) (*trpcmcp.GetPromptResult, error) {
		return c.connector.GetPrompt(ctx, req)
	})
}

func (c *managedConnector) ListResources(ctx context.Context, req *trpcmcp.ListResourcesRequest) (*trpcmcp.ListResourcesResult, error) {
	if req == nil {
		return nil, errors.New("MCP list resources request is required")
	}
	return connectorCall(c, ctx, func(ctx context.Context) (*trpcmcp.ListResourcesResult, error) {
		return c.connector.ListResources(ctx, req)
	})
}

func (c *managedConnector) ReadResource(ctx context.Context, req *trpcmcp.ReadResourceRequest) (*trpcmcp.ReadResourceResult, error) {
	if req == nil {
		return nil, errors.New("MCP resource request is required")
	}
	return connectorCall(c, ctx, func(ctx context.Context) (*trpcmcp.ReadResourceResult, error) {
		return c.connector.ReadResource(ctx, req)
	})
}

func (c *managedConnector) configure(configure func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	// SDK configuration methods have their own handler/roots locks. Do not
	// queue them behind a request that may be invoking a notification handler.
	configure()
}

func (c *managedConnector) RegisterNotificationHandler(method string, handler trpcmcp.NotificationHandler) {
	c.configure(func() { c.connector.RegisterNotificationHandler(method, handler) })
}

func (c *managedConnector) UnregisterNotificationHandler(method string) {
	c.configure(func() { c.connector.UnregisterNotificationHandler(method) })
}

func (c *managedConnector) SetRootsProvider(provider trpcmcp.RootsProvider) {
	c.configure(func() { c.connector.SetRootsProvider(provider) })
}

func (c *managedConnector) SendRootsListChangedNotification(ctx context.Context) error {
	_, err := connectorCall(c, ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, c.connector.SendRootsListChangedNotification(ctx)
	})
	return err
}

func (c *managedConnector) SendInitialized(ctx context.Context) error {
	_, err := connectorCall(c, ctx, func(ctx context.Context) (struct{}, error) {
		sender, ok := c.connector.(interface{ SendInitialized(context.Context) error })
		if !ok {
			return struct{}{}, errors.New("MCP connector does not expose initialized notifications")
		}
		return struct{}{}, sender.SendInitialized(ctx)
	})
	return err
}

func (c *managedConnector) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		c.cancel()
		// A timeout never closes the underlying SDK in parallel with a call.
		go func() {
			<-c.gate
			c.closeErr = c.connector.Close()
			close(c.closeDone)
		}()
	})
	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	select {
	case <-c.closeDone:
		return c.closeErr
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

type managedSessionConnector struct {
	*managedConnector
	session trpcmcp.SessionClient
}

func (c *managedSessionConnector) GetSessionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return ""
	}
	// A notification may inspect the last completed session while its POST
	// request still owns the operation gate.
	return c.sessionID
}

func (c *managedSessionConnector) TerminateSession(ctx context.Context) error {
	_, err := connectorCall(c.managedConnector, ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, c.session.TerminateSession(ctx)
	})
	return err
}

type managedProcessConnector struct {
	*managedConnector
	process trpcmcp.ProcessClient
}

func (c *managedProcessConnector) GetProcessID() int {
	id, _ := connectorCall(c.managedConnector, context.Background(), func(context.Context) (int, error) {
		return c.process.GetProcessID(), nil
	})
	return id
}

func (c *managedProcessConnector) GetCommandLine() []string {
	command, _ := connectorCall(c.managedConnector, context.Background(), func(context.Context) ([]string, error) {
		return c.process.GetCommandLine(), nil
	})
	return command
}

func (c *managedProcessConnector) IsProcessRunning() bool {
	running, _ := connectorCall(c.managedConnector, context.Background(), func(context.Context) (bool, error) {
		return c.process.IsProcessRunning(), nil
	})
	return running
}

func (c *managedProcessConnector) GetTransportInfo() trpcmcp.TransportInfo {
	info, _ := connectorCall(c.managedConnector, context.Background(), func(context.Context) (trpcmcp.TransportInfo, error) {
		if aware, ok := c.process.(trpcmcp.TransportAware); ok {
			return aware.GetTransportInfo(), nil
		}
		return trpcmcp.TransportInfo{}, nil
	})
	return info
}

func (c *managedProcessConnector) RestartProcess(ctx context.Context) error {
	_, err := connectorCall(c.managedConnector, ctx, func(ctx context.Context) (struct{}, error) {
		err := c.process.RestartProcess(ctx)
		c.mu.Lock()
		c.state = c.process.GetState()
		c.capabilities = nil
		c.mu.Unlock()
		return struct{}{}, err
	})
	return err
}
