/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package polaristoolset exposes MCP tools discovered through Polaris as a
// trpc-agent-go ToolSet.
package polaristoolset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adaptermcp "trpc.group/trpc-go/trpc-agent-naming-polarismesh/mcp"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

// ToolSet lists tools from a Polaris-discovered MCP server and routes calls
// through the adapter Pool.
type ToolSet struct {
	name  string
	pool  *adaptermcp.Pool
	tools []tool.Tool
}

// New discovers the MCP instance, initializes one connector to read the tool
// declarations, and returns a ToolSet whose calls use the healthy client pool.
func New(
	ctx context.Context,
	pool *adaptermcp.Pool,
	consumer api.ConsumerAPI,
	config adaptermcp.Config,
) (*ToolSet, error) {
	if pool == nil {
		return nil, errors.New("MCP pool is required")
	}
	if consumer == nil {
		return nil, errors.New("polaris ConsumerAPI is required")
	}
	endpoint, err := discoverEndpoint(ctx, consumer, config, 30*time.Second)
	if err != nil {
		return nil, err
	}
	refreshDeadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := pool.Refresh(ctx); err == nil {
			break
		} else if time.Now().After(refreshDeadline) {
			return nil, fmt.Errorf("refresh MCP pool: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse discovered MCP endpoint: %w", err)
	}
	connectorConfig := config
	connectorConfig.Host = parsed.Hostname()
	connectorConfig.Port, err = strconv.Atoi(parsed.Port())
	if err != nil {
		return nil, fmt.Errorf("parse discovered MCP port: %w", err)
	}
	connectorConfig.EndpointPath = parsed.Path
	connector, err := adaptermcp.NewClient(ctx, connectorConfig, endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect discovered MCP server %s: %w", endpoint, err)
	}
	defer func() { _ = connector.Close() }()
	listed, err := connector.ListTools(ctx, &trpcmcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list discovered MCP tools: %w", err)
	}
	if listed == nil || len(listed.Tools) == 0 {
		return nil, errors.New("discovered MCP server exposes no tools")
	}
	set := &ToolSet{
		name:  "polaris-mcp",
		pool:  pool,
		tools: make([]tool.Tool, 0, len(listed.Tools)),
	}
	for _, item := range listed.Tools {
		set.tools = append(set.tools, &remoteTool{
			name:        item.Name,
			description: item.Description,
			schema:      convertSchema(item.InputSchema),
			pool:        pool,
		})
	}
	return set, nil
}

// Tools implements tool.ToolSet.
func (s *ToolSet) Tools(context.Context) []tool.Tool {
	return append([]tool.Tool(nil), s.tools...)
}

// Close implements tool.ToolSet. The pool is owned by the caller.
func (s *ToolSet) Close() error { return nil }

// Name implements tool.ToolSet.
func (s *ToolSet) Name() string { return s.name }

type remoteTool struct {
	name        string
	description string
	schema      *tool.Schema
	pool        *adaptermcp.Pool
}

func (t *remoteTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: t.description,
		InputSchema: t.schema,
	}
}

func (t *remoteTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	arguments := make(map[string]any)
	if len(jsonArgs) > 0 {
		if err := json.Unmarshal(jsonArgs, &arguments); err != nil {
			return nil, fmt.Errorf("decode MCP tool arguments: %w", err)
		}
	}
	request := &trpcmcp.CallToolRequest{}
	request.Params.Name = t.name
	request.Params.Arguments = arguments
	result, err := t.pool.CallTool(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("MCP tool returned an empty result")
	}
	if result.IsError {
		return nil, fmt.Errorf("MCP tool %s reported an error", t.name)
	}
	var parts []string
	for _, content := range result.Content {
		switch value := content.(type) {
		case trpcmcp.TextContent:
			parts = append(parts, value.Text)
		default:
			raw, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				return nil, fmt.Errorf("encode MCP content: %w", marshalErr)
			}
			parts = append(parts, string(raw))
		}
	}
	return strings.Join(parts, "\n"), nil
}

func convertSchema(source any) *tool.Schema {
	if source == nil {
		return nil
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil
	}
	var schema tool.Schema
	if json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	return &schema
}

func discoverEndpoint(
	ctx context.Context,
	consumer api.ConsumerAPI,
	config adaptermcp.Config,
	wait time.Duration,
) (string, error) {
	deadline := time.Now().Add(wait)
	var lastErr error
	for {
		endpoint, err := getEndpoint(ctx, consumer, config)
		if err == nil {
			return endpoint, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return "", fmt.Errorf("discover MCP endpoint: %w", lastErr)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func getEndpoint(
	ctx context.Context,
	consumer api.ConsumerAPI,
	config adaptermcp.Config,
) (string, error) {
	timeout := config.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	request := &api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{
		Namespace:       config.Namespace,
		Service:         config.Service,
		SkipRouteFilter: true,
		Timeout:         &timeout,
	}}
	request.SetRetryCount(0)
	response, err := consumer.GetInstances(request)
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", errors.New("polaris returned an empty MCP instance response")
	}
	for _, instance := range response.GetInstances() {
		if instance == nil || !instance.IsHealthy() || instance.IsIsolated() {
			continue
		}
		path := instance.GetMetadata()[model.MCPMetadataKeyEndpointPath]
		if path == "" {
			path = config.EndpointPath
		}
		if path == "" {
			path = adaptermcp.DefaultStreamableHTTPPath
		}
		return (&url.URL{
			Scheme: config.Scheme,
			Host:   net.JoinHostPort(instance.GetHost(), fmt.Sprint(instance.GetPort())),
			Path:   path,
		}).String(), nil
	}
	return "", errors.New("no healthy MCP instances are available")
}
