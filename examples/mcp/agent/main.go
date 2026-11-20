/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main runs an LLM Agent that uses MCP tools discovered through Polaris.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/mcp/internal/polaristoolset"
	adaptermcp "trpc.group/trpc-go/trpc-agent-naming-polarismesh/mcp"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

var (
	server           string
	polarisPort      int
	consolePort      int
	namespace        string
	mcpService       string
	agentService     string
	version          string
	protocolVersion  string
	mcpServerVersion string
	transport        string
	mcpPath          string
	host             string
	port             int
	registrationPort int
	modelName        string
	timeout          time.Duration
	debug            bool
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

	mcpConfig := adaptermcp.DefaultConfig()
	mcpConfig.Namespace = namespace
	mcpConfig.Service = mcpService
	mcpConfig.ServerName = mcpService
	mcpConfig.ServerVersion = mcpServerVersion
	mcpConfig.ProtocolVersion = protocolVersion
	mcpConfig.Transport = adaptermcp.Transport(transport)
	mcpConfig.Scheme = "http"
	mcpConfig.EndpointPath = mcpPath
	mcpConfig.Timeout = timeout
	pool, err := adaptermcp.NewPool(adaptermcp.PoolConfig{
		Config:   mcpConfig,
		Consumer: api.NewConsumerAPIByContext(sdk),
	})
	if err != nil {
		return fmt.Errorf("create MCP pool: %w", err)
	}
	defer func() { _ = pool.Close() }()
	tools, err := polaristoolset.New(
		ctx,
		pool,
		api.NewConsumerAPIByContext(sdk),
		mcpConfig,
	)
	if err != nil {
		return err
	}
	defer func() { _ = tools.Close() }()
	cardTools := tools.Tools(ctx)

	agentAPI := demo.NewAgentAPI(sdk, server, consolePort, timeout)
	defer agentAPI.Destroy()
	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = agentService
	cfg.AgentName = agentService
	cfg.AgentVersion = version
	cfg.Description = "An Agent that uses MCP tools registered in Polaris"
	cfg.AdvertiseHost = host
	cfg.AdvertisePort = actualPort
	cfg.RegistrationPort = registrationPort
	cfg.A2APath = "/a2a"
	cfg.Streaming = true
	cfg.EnsureService = true
	cfg.PolarisRequestTimeout = timeout

	localAgent := llmagent.New(
		agentService,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithDescription(cfg.Description),
		llmagent.WithInstruction(
			"You are a helpful assistant. Use the discovered MCP tools when they "+
				"are relevant. Prefer get_weather for weather questions and add for "+
				"arithmetic.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPointer(1024),
			Temperature: floatPointer(0.2),
			Stream:      true,
		}),
		llmagent.WithToolSets([]tool.ToolSet{tools}),
	)
	localRunner := runner.NewRunner(agentService, localAgent)
	defer func() { _ = localRunner.Close() }()

	runtime, err := adapteragent.New(
		adapteragent.WithConfig(cfg),
		adapteragent.WithSDKContext(sdk),
		adapteragent.WithAgentAPI(agentAPI),
		adapteragent.WithCardTools(cardTools...),
	)
	if err != nil {
		return fmt.Errorf("create Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare Runtime: %w", err)
	}
	snapshot, err := runtime.RegistrationSnapshot()
	if err != nil {
		return fmt.Errorf("read registration snapshot: %w", err)
	}
	log.Printf("MCP Agent ready: namespace=%s agent=%s mcp=%s endpoint=http://%s/a2a",
		namespace, agentService, mcpService,
		net.JoinHostPort(host, fmt.Sprint(actualPort)))
	return demo.ServeA2A(
		ctx,
		sdk,
		runtime,
		localRunner,
		listener,
		demo.ProviderOptions{
			Namespace:        namespace,
			Service:          agentService,
			Version:          version,
			Host:             host,
			RegistrationPort: snapshot.RegistrationPort,
			TTL:              snapshot.TTL,
		},
	)
}

func initFlags() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&polarisPort, "polaris-port", 8091, "Polaris Discovery gRPC port")
	flag.IntVar(&consolePort, "console-port", 8090, "Polaris Console HTTP port")
	flag.StringVar(&namespace, "namespace", "go-agent-naming-examples", "Polaris namespace")
	flag.StringVar(&mcpService, "mcp-service", "trpc.polaris.examples.mcp", "MCP service name")
	flag.StringVar(&agentService, "agent-service", "trpc.polaris.examples.mcp-agent", "A2A Agent service name")
	flag.StringVar(&version, "version", "v1.0.0", "Agent and instance version")
	flag.StringVar(&protocolVersion, "protocol-version", trpcmcp.ProtocolVersion_2025_03_26, "MCP protocol version")
	flag.StringVar(&mcpServerVersion, "mcp-server-version", "1.0.0", "MCP server version")
	flag.StringVar(&transport, "transport", string(adaptermcp.TransportStreamableHTTP), "MCP transport")
	flag.StringVar(&mcpPath, "mcp-path", adaptermcp.DefaultStreamableHTTPPath, "MCP endpoint path")
	flag.StringVar(&host, "host", "127.0.0.1", "Listen and advertise host")
	flag.IntVar(&port, "port", 18192, "A2A listen port; 0 chooses a free port")
	flag.IntVar(&registrationPort, "registration-port", 1, "Polaris registration port")
	flag.StringVar(&modelName, "model", envOrDefault("MODEL_NAME", "deepseek-chat"), "Model name")
	flag.DurationVar(&timeout, "timeout", 10*time.Second, "Polaris and MCP timeout")
	flag.BoolVar(&debug, "debug", false, "Enable debug logs")
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intPointer(value int) *int {
	return &value
}

func floatPointer(value float64) *float64 {
	return &value
}
