/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main runs an MCP server registered in Polaris with a Service Contract.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
	adaptermcp "trpc.group/trpc-go/trpc-agent-naming-polarismesh/mcp"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	trpcregistry "trpc.group/trpc-go/trpc-go/naming/registry"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

var (
	server          string
	polarisPort     int
	consolePort     int
	namespace       string
	serviceName     string
	host            string
	port            int
	protocolVersion string
	serverVersion   string
	mcpPath         string
	timeout         time.Duration
	debug           bool
)

func main() {
	initFlags()
	flag.Parse()
	if server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
	}
	demo.ConfigureLogging(debug)
	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] enable Polaris debug logs: %v", err)
		}
	}
	ctx, stop := demo.SignalContext()
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = listener.Close() }()
	actualPort := listener.Addr().(*net.TCPAddr).Port

	sdk, err := demo.NewSDK(
		net.JoinHostPort(server, fmt.Sprint(polarisPort)),
		"",
		false,
		timeout,
	)
	if err != nil {
		return fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()
	sdk.GetConfig().GetProvider().SetReportServiceContractEnable(true)

	admin, err := demo.EnsureMCPService(namespace, serviceName, server, consolePort, timeout)
	if err != nil {
		return err
	}
	defer admin.Close()

	cfg := adaptermcp.DefaultConfig()
	cfg.Namespace = namespace
	cfg.Service = serviceName
	cfg.ServerName = serviceName
	cfg.ServerVersion = serverVersion
	cfg.ProtocolVersion = protocolVersion
	cfg.Transport = adaptermcp.TransportStreamableHTTP
	cfg.Scheme = "http"
	cfg.Host = host
	cfg.Port = actualPort
	cfg.EndpointPath = mcpPath
	cfg.Timeout = timeout
	prepared, err := adaptermcp.PrepareServer(ctx, cfg)
	if err != nil {
		return err
	}
	registered, err := prepared.RegisterMetadata(registrar{})
	if err != nil {
		return err
	}
	if !registered {
		return errors.New("MCP metadata provider was not registered")
	}
	defer polarisregistry.UnregisterMetadataProvider(serviceName)

	mcpServer := newMCPServer(serviceName, serverVersion, mcpPath)
	httpServer := &http.Server{Handler: mcpServer.Handler(), ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	registry, err := polarisregistry.NewRegistry(
		api.NewProviderAPIByContext(sdk),
		&polarisregistry.Config{
			Namespace:      namespace,
			ServiceName:    serviceName,
			EnableRegister: true,
			HeartBeat:      1,
			TTL:            2,
			ServiceToken:   os.Getenv("POLARIS_SERVICE_TOKEN"),
		},
	)
	if err != nil {
		return err
	}
	if err := registry.Register(
		serviceName,
		trpcregistry.WithAddress(net.JoinHostPort(host, fmt.Sprint(actualPort))),
	); err != nil {
		return fmt.Errorf("register MCP server: %w", err)
	}
	defer func() { _ = registry.Deregister(serviceName) }()

	contract, err := reportContract(ctx, sdk, cfg)
	if err != nil {
		return err
	}
	log.Printf("MCP server ready: namespace=%s service=%s endpoint=%s contract=%s",
		namespace, serviceName, prepared.Endpoint, contract.ContentDigest)

	select {
	case <-ctx.Done():
		return nil
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func reportContract(
	ctx context.Context,
	sdk api.SDKContext,
	cfg adaptermcp.Config,
) (*model.ServiceContract, error) {
	connector, err := adaptermcp.NewClient(ctx, cfg, (&url.URL{
		Scheme: cfg.Scheme,
		Host:   net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port)),
		Path:   cfg.EndpointPath,
	}).String())
	if err != nil {
		return nil, fmt.Errorf("connect local MCP server: %w", err)
	}
	defer func() { _ = connector.Close() }()
	contract, err := adaptermcp.CollectContract(ctx, connector, cfg)
	if err != nil {
		return nil, fmt.Errorf("collect MCP Service Contract: %w", err)
	}
	reported, err := adaptermcp.ReportContractWithConfig(
		ctx,
		api.NewProviderAPIByContext(sdk),
		contract,
		cfg,
	)
	if err != nil {
		return nil, fmt.Errorf("report MCP Service Contract: %w", err)
	}
	return reported, nil
}

func newMCPServer(name, version, path string) *trpcmcp.Server {
	server := trpcmcp.NewServer(
		name,
		version,
		trpcmcp.WithServerPath(path),
		trpcmcp.WithStatelessMode(true),
	)
	server.RegisterTool(
		trpcmcp.NewTool(
			"get_weather",
			trpcmcp.WithDescription("Get the current weather for a city"),
			trpcmcp.WithInputStruct[weatherInput](),
		),
		func(_ context.Context, request *trpcmcp.CallToolRequest) (*trpcmcp.CallToolResult, error) {
			city := "Shenzhen"
			if len(request.Params.Arguments) > 0 {
				if value, ok := request.Params.Arguments["city"].(string); ok && value != "" {
					city = value
				}
			}
			return trpcmcp.NewTextResult("weather|" + city + "|26C|partly cloudy"), nil
		},
	)
	server.RegisterTool(
		trpcmcp.NewTool(
			"add",
			trpcmcp.WithDescription("Add two numbers"),
			trpcmcp.WithInputStruct[addInput](),
		),
		func(_ context.Context, request *trpcmcp.CallToolRequest) (*trpcmcp.CallToolResult, error) {
			a, _ := request.Params.Arguments["a"].(float64)
			b, _ := request.Params.Arguments["b"].(float64)
			return trpcmcp.NewTextResult(fmt.Sprintf("sum|%g", a+b)), nil
		},
	)
	return server
}

type weatherInput struct {
	City string `json:"city" description:"City name"`
}

type addInput struct {
	A float64 `json:"a" description:"First number"`
	B float64 `json:"b" description:"Second number"`
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

func initFlags() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&polarisPort, "polaris-port", 8091, "Polaris Discovery gRPC port")
	flag.IntVar(&consolePort, "console-port", 8090, "Polaris Console HTTP port")
	flag.StringVar(&namespace, "namespace", "go-agent-naming-examples", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.examples.mcp", "MCP service name")
	flag.StringVar(&host, "host", "127.0.0.1", "Listen and advertise host")
	flag.IntVar(&port, "port", 18494, "MCP listen port; 0 chooses a free port")
	flag.StringVar(&protocolVersion, "protocol-version", trpcmcp.ProtocolVersion_2025_03_26, "MCP protocol version")
	flag.StringVar(&serverVersion, "server-version", "1.0.0", "MCP server version")
	flag.StringVar(&mcpPath, "mcp-path", adaptermcp.DefaultStreamableHTTPPath, "MCP endpoint path")
	flag.DurationVar(&timeout, "timeout", 10*time.Second, "Polaris and MCP timeout")
	flag.BoolVar(&debug, "debug", false, "Enable debug logs")
}
