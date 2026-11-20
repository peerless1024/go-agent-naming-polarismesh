//go:build integration

/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package mcp_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/integration/internal/harness"
	adaptermcp "trpc.group/trpc-go/trpc-agent-naming-polarismesh/mcp"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	trpcregistry "trpc.group/trpc-go/trpc-go/naming/registry"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

func TestMCPMetadataContractDiscoveryAndPool(t *testing.T) {
	cfg := harness.Load(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	service := cfg.Unique("mcp")
	if err := cfg.EnsureService(service, model.ServiceTypeMCPServer); err != nil {
		t.Fatalf("ensure MCP service: %v", err)
	}
	t.Cleanup(func() {
		if err := cfg.DeleteService(service, model.ServiceTypeMCPServer); err != nil {
			t.Logf("delete MCP service %s: %v", service, err)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port

	sdk, err := cfg.SDK(false)
	if err != nil {
		t.Fatalf("create Polaris SDK: %v", err)
	}
	t.Cleanup(sdk.Destroy)
	sdk.GetConfig().GetProvider().SetReportServiceContractEnable(true)

	mcpConfig := adaptermcp.DefaultConfig()
	mcpConfig.Namespace = cfg.Namespace
	mcpConfig.Service = service
	mcpConfig.ServerName = service
	mcpConfig.ServerVersion = "1.0.0"
	mcpConfig.ProtocolVersion = trpcmcp.ProtocolVersion_2025_03_26
	mcpConfig.Transport = adaptermcp.TransportStreamableHTTP
	mcpConfig.Host = "127.0.0.1"
	mcpConfig.Port = port
	mcpConfig.Timeout = cfg.RequestTimeout
	prepared, err := adaptermcp.PrepareServer(ctx, mcpConfig)
	if err != nil {
		t.Fatalf("prepare MCP server metadata: %v", err)
	}
	registered, err := prepared.RegisterMetadata(registrar{})
	if err != nil {
		t.Fatalf("register MCP metadata: %v", err)
	}
	if !registered {
		t.Fatal("MCP metadata provider was not registered")
	}
	t.Cleanup(func() { polarisregistry.UnregisterMetadataProvider(service) })

	server := trpcmcp.NewServer(
		service,
		"1.0.0",
		trpcmcp.WithServerPath(adaptermcp.DefaultStreamableHTTPPath),
		trpcmcp.WithStatelessMode(true),
	)
	server.RegisterTool(
		trpcmcp.NewTool(
			"echo",
			trpcmcp.WithDescription("Echo a value"),
			trpcmcp.WithInputStruct[echoInput](),
		),
		func(_ context.Context, request *trpcmcp.CallToolRequest) (*trpcmcp.CallToolResult, error) {
			value := "hello"
			if candidate, ok := request.Params.Arguments["value"].(string); ok && candidate != "" {
				value = candidate
			}
			return trpcmcp.NewTextResult("mcp|" + service + "|" + value), nil
		},
	)
	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() {
		shutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		_ = httpServer.Shutdown(shutdown)
	})

	registry, err := polarisregistry.NewRegistry(
		api.NewProviderAPIByContext(sdk),
		&polarisregistry.Config{
			Namespace:      cfg.Namespace,
			ServiceName:    service,
			EnableRegister: true,
			HeartBeat:      1,
			TTL:            2,
			ServiceToken:   cfg.ServiceToken,
		},
	)
	if err != nil {
		t.Fatalf("create MCP registry: %v", err)
	}
	if err := registry.Register(
		service,
		trpcregistry.WithAddress(net.JoinHostPort("127.0.0.1", fmt.Sprint(port))),
	); err != nil {
		t.Fatalf("register MCP instance: %v", err)
	}
	t.Cleanup(func() { _ = registry.Deregister(service) })

	connector, err := adaptermcp.NewClient(ctx, mcpConfig, prepared.Endpoint)
	if err != nil {
		t.Fatalf("connect local MCP server: %v", err)
	}
	contract, err := adaptermcp.CollectContract(ctx, connector, mcpConfig)
	_ = connector.Close()
	if err != nil {
		t.Fatalf("collect MCP Service Contract: %v", err)
	}
	reported, err := adaptermcp.ReportContractWithConfig(
		ctx,
		api.NewProviderAPIByContext(sdk),
		contract,
		mcpConfig,
	)
	if err != nil {
		t.Fatalf("report MCP Service Contract: %v", err)
	}
	if reported.ContentDigest == "" {
		t.Fatal("reported MCP Service Contract has no digest")
	}

	consumer := api.NewConsumerAPIByContext(sdk)
	var instance model.Instance
	err = harness.Eventually(ctx, cfg.RequestTimeout, 250*time.Millisecond, func() error {
		timeout := 2 * time.Second
		get := &api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{
			Namespace:       cfg.Namespace,
			Service:         service,
			SkipRouteFilter: true,
			Timeout:         &timeout,
		}}
		get.SetRetryCount(0)
		response, err := consumer.GetInstances(get)
		if err != nil {
			return err
		}
		var healthy []model.Instance
		for _, item := range response.GetInstances() {
			if item != nil && item.IsHealthy() && !item.IsIsolated() {
				healthy = append(healthy, item)
			}
		}
		if len(healthy) != 1 {
			return fmt.Errorf("healthy MCP instances=%d, want 1", len(healthy))
		}
		instance = healthy[0]
		return nil
	})
	if err != nil {
		t.Fatalf("wait for MCP instance: %v", err)
	}
	metadata := instance.GetMetadata()
	if metadata[model.MCPMetadataKeyServerName] != service {
		t.Errorf("mcp-server-name=%q, want %q", metadata[model.MCPMetadataKeyServerName], service)
	}
	if metadata[model.MCPMetadataKeyProtocolVersion] == "" {
		t.Error("mcp-protocol-version is missing")
	}
	if metadata[model.MCPMetadataKeyEndpointPath] != adaptermcp.DefaultStreamableHTTPPath {
		t.Errorf("mcp-endpoint-path=%q", metadata[model.MCPMetadataKeyEndpointPath])
	}

	pool, err := adaptermcp.NewPool(adaptermcp.PoolConfig{
		Config:   mcpConfig,
		Consumer: consumer,
	})
	if err != nil {
		t.Fatalf("create MCP pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	err = harness.Eventually(ctx, cfg.RequestTimeout, 250*time.Millisecond, func() error {
		_, err := pool.Refresh(ctx)
		return err
	})
	if err != nil {
		t.Fatalf("refresh MCP pool: %v", err)
	}
	request := &trpcmcp.CallToolRequest{}
	request.Params.Name = "echo"
	request.Params.Arguments = map[string]any{"value": "integration"}
	var result *trpcmcp.CallToolResult
	err = harness.Eventually(ctx, cfg.RequestTimeout, 250*time.Millisecond, func() error {
		response, err := pool.CallTool(ctx, request)
		if err != nil {
			return err
		}
		result = response
		return nil
	})
	if err != nil {
		t.Fatalf("call MCP tool: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("MCP tool returned no content")
	}
	text, ok := result.Content[0].(trpcmcp.TextContent)
	if !ok || !strings.Contains(text.Text, "mcp|"+service+"|integration") {
		t.Fatalf("unexpected MCP tool result: %#v", result.Content)
	}

	var queried *model.ServiceContract
	err = harness.Eventually(ctx, cfg.RequestTimeout, 250*time.Millisecond, func() error {
		rule, err := adaptermcp.GetContractWithConfig(ctx, consumer, mcpConfig)
		if err != nil {
			return err
		}
		contract, ok := rule.Value.(*model.ServiceContract)
		if !ok || contract == nil {
			return fmt.Errorf("Service Contract query returned %T", rule.Value)
		}
		if contract.Type != "mcp" || contract.ContentDigest == "" ||
			len(contract.Interfaces) == 0 {
			return fmt.Errorf("incomplete MCP contract: type=%q digest=%q interfaces=%d",
				contract.Type, contract.ContentDigest, len(contract.Interfaces))
		}
		queried = contract
		return nil
	})
	if err != nil {
		t.Fatalf("query MCP Service Contract: %v", err)
	}
	t.Logf("queried MCP Service Contract: digest=%s interfaces=%d features=%d",
		queried.ContentDigest, len(queried.Interfaces), len(queried.ServiceFeatures))
	harness.KeepIfRequested(t)
}

type echoInput struct {
	Value string `json:"value" description:"Value to echo"`
}

type registrar struct{}

func (registrar) RegisterMetadataProvider(
	service string,
	provider adapternaming.MetadataProvider,
) error {
	return polarisregistry.RegisterMetadataProvider(service, provider)
}

func (registrar) UnregisterMetadataProvider(service string) {
	polarisregistry.UnregisterMetadataProvider(service)
}
