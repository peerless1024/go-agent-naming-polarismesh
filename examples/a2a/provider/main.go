/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main runs an LLM Agent over A2A and registers it in Polaris.
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
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
)

var (
	server           string
	polarisPort      int
	consolePort      int
	namespace        string
	serviceName      string
	version          string
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

	agentAPI := demo.NewAgentAPI(sdk, server, consolePort, timeout)
	defer agentAPI.Destroy()

	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = serviceName
	cfg.AgentName = serviceName
	cfg.AgentVersion = version
	cfg.Description = "A tRPC-Agent-Go demo agent registered in Polaris AI Registry"
	cfg.AdvertiseHost = host
	cfg.AdvertisePort = actualPort
	cfg.RegistrationPort = registrationPort
	cfg.A2APath = "/a2a"
	cfg.Streaming = true
	cfg.EnsureService = true
	cfg.PolarisRequestTimeout = timeout

	weatherTool := function.NewFunctionTool(
		getWeather,
		function.WithName("get_weather"),
		function.WithDescription("Get the current weather for a city"),
	)
	localAgent := llmagent.New(
		serviceName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithDescription(cfg.Description),
		llmagent.WithInstruction(
			"You are a helpful weather assistant. Use get_weather before answering "+
				"questions about current weather.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPointer(1024),
			Temperature: floatPointer(0.2),
			Stream:      true,
		}),
		llmagent.WithTools([]tool.Tool{weatherTool}),
	)
	localRunner := runner.NewRunner(serviceName, localAgent)
	defer func() { _ = localRunner.Close() }()

	runtime, err := adapteragent.New(
		adapteragent.WithConfig(cfg),
		adapteragent.WithSDKContext(sdk),
		adapteragent.WithAgentAPI(agentAPI),
		adapteragent.WithCardTools(weatherTool),
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
	log.Printf("A2A provider ready: namespace=%s service=%s endpoint=http://%s a2a",
		namespace, serviceName, net.JoinHostPort(host, fmt.Sprint(actualPort)))
	return demo.ServeA2A(
		ctx,
		sdk,
		runtime,
		localRunner,
		listener,
		demo.ProviderOptions{
			Namespace:        namespace,
			Service:          serviceName,
			Version:          version,
			Host:             host,
			RegistrationPort: snapshot.RegistrationPort,
			TTL:              snapshot.TTL,
		},
	)
}

type weatherRequest struct {
	City string `json:"city" description:"City name"`
}

type weatherResponse struct {
	City        string `json:"city"`
	Temperature int    `json:"temperature_c"`
	Condition   string `json:"condition"`
}

func getWeather(_ context.Context, request weatherRequest) (weatherResponse, error) {
	city := request.City
	if city == "" {
		city = "Shenzhen"
	}
	return weatherResponse{
		City:        city,
		Temperature: 26,
		Condition:   "partly cloudy",
	}, nil
}

func initFlags() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&polarisPort, "polaris-port", 8091, "Polaris Discovery gRPC port")
	flag.IntVar(&consolePort, "console-port", 8090, "Polaris Console HTTP port")
	flag.StringVar(&namespace, "namespace", "go-agent-naming-examples", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.examples.a2a", "A2A service name")
	flag.StringVar(&version, "version", "v1.0.0", "Agent and instance version")
	flag.StringVar(&host, "host", "127.0.0.1", "Listen and advertise host")
	flag.IntVar(&port, "port", 18191, "A2A listen port; 0 chooses a free port")
	flag.IntVar(&registrationPort, "registration-port", 1, "Polaris registration port")
	flag.StringVar(&modelName, "model", envOrDefault("MODEL_NAME", "deepseek-chat"), "Model name")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "Polaris request timeout")
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
