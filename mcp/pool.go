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
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

// InstanceLister lists Polaris instances.
type InstanceLister interface {
	GetInstances(req *api.GetInstancesRequest) (*model.InstancesResponse, error)
}

// PoolConfig configures a Polaris MCP client pool.
type PoolConfig struct {
	Config
	Consumer        InstanceLister
	Reporter        api.ConsumerAPI
	RefreshInterval time.Duration
	// ClientFactory returns an owned, initialized connector. Custom connectors
	// must honor request contexts and synchronize their background callbacks.
	ClientFactory func(context.Context, Config, string) (trpcmcp.Connector, error)
}

// InstanceEvent describes one pool watch cycle.
type InstanceEvent struct {
	Added    []string
	Removed  []string
	Replaced []string
}

type pooledClient struct {
	instance        model.Instance
	endpoint        string
	path            string
	protocolVersion string
	transport       model.MCPTransport
	client          trpcmcp.Connector

	lifecycleMu sync.Mutex
	activeCalls int
	retiring    bool
	idle        chan struct{}
	closeOnce   sync.Once
	closeDone   chan struct{}
	closeErr    error
}

type mcpTarget struct {
	instance        model.Instance
	endpoint        string
	path            string
	protocolVersion string
	transport       model.MCPTransport
}

type retiredClient struct {
	id     string
	client *pooledClient
}

// Pool maintains one MCP client per healthy instance and selects clients with
// round-robin.
type Pool struct {
	config PoolConfig

	refreshMu sync.Mutex
	mu        sync.RWMutex
	clients   map[string]*pooledClient
	order     []string
	cursor    atomic.Uint64
	closed    bool
}

// NewPool creates an empty pool. Call Refresh or Watch to populate it.
func NewPool(config PoolConfig) (*Pool, error) {
	if err := config.ValidateClient(); err != nil {
		return nil, err
	}
	if config.Transport == TransportStdio {
		return nil, errors.New("MCP client pool does not manage mcp-stdio")
	}
	if config.Consumer == nil {
		return nil, errors.New("polaris instance lister is required")
	}
	if config.RefreshInterval < 0 {
		return nil, errors.New("MCP refresh interval must not be negative")
	}
	return &Pool{
		config:  config,
		clients: make(map[string]*pooledClient),
	}, nil
}

// Refresh reconciles healthy instances and returns add/remove/replace deltas.
func (p *Pool) Refresh(ctx context.Context) (InstanceEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	p.mu.RLock()
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return InstanceEvent{}, errors.New("MCP client pool is closed")
	}
	if err := ctx.Err(); err != nil {
		return InstanceEvent{}, err
	}
	targets, err := p.discover(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return InstanceEvent{}, ctxErr
	}
	if err != nil {
		return InstanceEvent{}, err
	}
	replacements := make(map[string]*pooledClient)
	var buildErrs []error
	p.mu.RLock()
	existing := make(map[string]*pooledClient, len(p.clients))
	for id, client := range p.clients {
		existing[id] = client
	}
	p.mu.RUnlock()
	for id, target := range targets {
		if current := existing[id]; current != nil && target.sameTarget(current) {
			continue
		}
		replacement, err := p.clientForTarget(ctx, target)
		if err != nil {
			buildErrs = append(buildErrs, fmt.Errorf("connect MCP instance %s: %w", id, err))
			continue
		}
		replacements[id] = replacement
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		closeErr := p.retireClients(valuesToRetiredClients(replacements))
		return InstanceEvent{}, errors.Join(ctxErr, closeErr)
	}

	var retired []retiredClient
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		replacementClients := make([]retiredClient, 0, len(replacements))
		for id, replacement := range replacements {
			replacementClients = append(
				replacementClients,
				retiredClient{id: id, client: replacement},
			)
		}
		_ = p.retireClients(replacementClients)
		return InstanceEvent{}, errors.New("MCP client pool is closed")
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		p.mu.Unlock()
		_ = p.retireClients(valuesToRetiredClients(replacements))
		return InstanceEvent{}, ctxErr
	}
	event := InstanceEvent{}
	for id, entry := range p.clients {
		target, ok := targets[id]
		if !ok {
			delete(p.clients, id)
			retired = append(retired, retiredClient{id: id, client: entry})
			event.Removed = append(event.Removed, id)
			continue
		}
		if !target.sameTarget(entry) {
			delete(p.clients, id)
			retired = append(retired, retiredClient{id: id, client: entry})
			if replacement := replacements[id]; replacement != nil {
				p.clients[id] = replacement
				delete(replacements, id)
				event.Replaced = append(event.Replaced, id)
			} else {
				event.Removed = append(event.Removed, id)
			}
		}
	}
	for id, entry := range replacements {
		p.clients[id] = entry
		event.Added = append(event.Added, id)
	}
	p.rebuildOrderLocked()
	p.mu.Unlock()
	sort.Strings(event.Added)
	sort.Strings(event.Removed)
	sort.Strings(event.Replaced)
	return event, errors.Join(append([]error{p.retireClients(retired)}, buildErrs...)...)
}

// CallTool invokes a tool through the next healthy MCP client and reports the
// result with InstanceTypeMCP.
func (p *Pool) CallTool(
	ctx context.Context,
	request *trpcmcp.CallToolRequest,
) (*trpcmcp.CallToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if request == nil {
		return nil, errors.New("MCP tool request is required")
	}
	p.mu.RLock()
	entry, err := p.nextLocked()
	if err == nil && !entry.acquire() {
		err = errors.New("selected MCP client is retiring")
	}
	if err != nil {
		p.mu.RUnlock()
		return nil, err
	}
	p.mu.RUnlock()
	callCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()
	start := time.Now()
	result, callErr := entry.client.CallTool(callCtx, request)
	entry.release()
	if p.config.Reporter != nil {
		retCode := int32(0)
		status := model.RetSuccess
		if callErr != nil || result != nil && result.IsError {
			retCode = -1
			status = model.RetFail
		}
		delay := time.Since(start)
		report := &api.ServiceCallResult{ServiceCallResult: model.ServiceCallResult{
			CalledInstance: entry.instance,
			Method:         trpcmcp.MethodToolsCall,
			RetStatus:      status,
			RetCode:        &retCode,
			Delay:          &delay,
			Timestamp:      start,
			InstanceType:   model.InstanceTypeMCP,
		}}
		_ = p.config.Reporter.UpdateServiceCallResult(report)
	}
	return result, callErr
}

// Close closes every pooled MCP client.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	clients := make([]retiredClient, 0, len(p.clients))
	for id, entry := range p.clients {
		clients = append(clients, retiredClient{id: id, client: entry})
	}
	p.clients = nil
	p.order = nil
	p.mu.Unlock()

	return p.retireClients(clients)
}

func (p *Pool) nextLocked() (*pooledClient, error) {
	if p.closed {
		return nil, errors.New("MCP client pool is closed")
	}
	if len(p.order) == 0 {
		return nil, errors.New("no healthy MCP instances are available")
	}
	index := p.cursor.Add(1) - 1
	return p.clients[p.order[index%uint64(len(p.order))]], nil
}

func (t mcpTarget) sameTarget(other *pooledClient) bool {
	if other == nil {
		return false
	}
	return t.endpoint == other.endpoint &&
		t.path == other.path &&
		t.protocolVersion == other.protocolVersion &&
		t.transport == other.transport
}

func (p *Pool) discover(ctx context.Context) (map[string]mcpTarget, error) {
	timeout := p.config.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	request := &api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{
		Namespace:       p.config.Namespace,
		Service:         p.config.Service,
		SkipRouteFilter: true,
		Timeout:         &timeout,
	}}
	request.SetRetryCount(0)
	response, err := p.config.Consumer.GetInstances(request)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("polaris returned an empty MCP instance response")
	}
	discovered := make(map[string]mcpTarget)
	for _, instance := range response.GetInstances() {
		if instance == nil || !instance.IsHealthy() || instance.IsIsolated() {
			continue
		}
		target, err := p.targetForInstance(instance)
		if err != nil {
			continue
		}
		discovered[instance.GetId()] = target
	}
	return discovered, nil
}

func (p *Pool) targetForInstance(instance model.Instance) (mcpTarget, error) {
	metadata := instance.GetMetadata()
	transport := model.MCPTransport(p.config.Transport)
	if protocol := strings.TrimSpace(instance.GetProtocol()); protocol != "" && protocol != string(transport) {
		return mcpTarget{}, fmt.Errorf("MCP instance protocol %q does not match %q", protocol, transport)
	}
	path := metadata[model.MCPMetadataKeyEndpointPath]
	if path == "" {
		path = p.config.effectiveEndpointPath()
	}
	protocolVersion := metadata[model.MCPMetadataKeyProtocolVersion]
	if protocolVersion == "" {
		protocolVersion = p.config.ProtocolVersion
	}
	if err := model.ValidateMCPServerMetadata(&model.MCPServerValidationRequest{
		Namespace:       p.config.Namespace,
		Service:         p.config.Service,
		ServerName:      p.config.Service,
		ProtocolVersion: protocolVersion,
		Transport:       transport,
		EndpointPath:    path,
		Metadata:        metadata,
	}); err != nil {
		return mcpTarget{}, err
	}
	endpoint := (&url.URL{
		Scheme: p.config.Scheme,
		Host:   net.JoinHostPort(instance.GetHost(), fmt.Sprint(instance.GetPort())),
		Path:   path,
	}).String()
	return mcpTarget{
		instance:        instance,
		endpoint:        endpoint,
		path:            path,
		protocolVersion: protocolVersion,
		transport:       transport,
	}, nil
}

func (p *Pool) clientForTarget(ctx context.Context, target mcpTarget) (*pooledClient, error) {
	config := p.config.Config
	config.Host = target.instance.GetHost()
	config.Port = int(target.instance.GetPort())
	config.EndpointPath = target.path
	config.ProtocolVersion = target.protocolVersion
	factory := p.config.ClientFactory
	if factory == nil {
		factory = NewClient
	}
	client, err := factory(ctx, config, target.endpoint)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("MCP client factory returned a nil connector")
	}
	if _, bareHTTPClient := client.(*trpcmcp.Client); bareHTTPClient && config.Transport == TransportStreamableHTTP {
		_ = closeConnectorBounded(client, config.Timeout)
		return nil, errors.New("MCP streamable HTTP factories must use mcp.NewClient to protect SDK session state")
	}
	client = manageConnector(client, config.Timeout)
	return &pooledClient{
		instance:        target.instance,
		endpoint:        target.endpoint,
		path:            target.path,
		protocolVersion: target.protocolVersion,
		transport:       target.transport,
		client:          client,
		closeDone:       make(chan struct{}),
	}, nil
}

func (p *Pool) retireClient(entry retiredClient) error {
	timeout := 2 * p.config.Timeout
	if err := entry.client.retire(timeout); err != nil {
		return fmt.Errorf("close MCP client %s: %w", entry.id, err)
	}
	return nil
}

func (p *Pool) retireClients(entries []retiredClient) error {
	if len(entries) == 0 {
		return nil
	}
	errs := make(chan error, len(entries))
	for _, entry := range entries {
		go func(entry retiredClient) { errs <- p.retireClient(entry) }(entry)
	}
	all := make([]error, 0, len(entries))
	for range entries {
		if err := <-errs; err != nil {
			all = append(all, err)
		}
	}
	return errors.Join(all...)
}

func (c *pooledClient) acquire() bool {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.retiring {
		return false
	}
	c.activeCalls++
	return true
}

func (c *pooledClient) release() {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.activeCalls > 0 {
		c.activeCalls--
	}
	if c.activeCalls == 0 && c.retiring && c.idle != nil {
		close(c.idle)
		c.idle = nil
	}
}

func (c *pooledClient) retire(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c.lifecycleMu.Lock()
	c.retiring = true
	if c.activeCalls > 0 && c.idle == nil {
		c.idle = make(chan struct{})
	}
	idle := c.idle
	c.lifecycleMu.Unlock()

	if idle != nil {
		select {
		case <-idle:
		case <-ctx.Done():
			// Closing may be deferred past the caller-visible timeout, but it
			// must never start while a Connector call is still active.
			go func() {
				<-idle
				_ = c.closeBounded(timeout)
			}()
			return fmt.Errorf("timed out waiting for active calls: %w", ctx.Err())
		}
	}
	return c.closeBounded(timeout)
}

func (c *pooledClient) closeBounded(timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- c.close() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("timed out closing connector: %w", context.DeadlineExceeded)
	}
}

func (c *pooledClient) close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.client.Close()
		close(c.closeDone)
	})
	<-c.closeDone
	return c.closeErr
}

func (p *Pool) rebuildOrderLocked() {
	ids := make([]string, 0, len(p.clients))
	for id := range p.clients {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	p.order = ids
}

func valuesToRetiredClients(
	values map[string]*pooledClient,
) []retiredClient {
	clients := make([]retiredClient, 0, len(values))
	for id, client := range values {
		clients = append(clients, retiredClient{id: id, client: client})
	}
	return clients
}
