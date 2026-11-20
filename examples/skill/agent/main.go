/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main runs an LLM Agent with a Skill installed from Polaris.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
)

var (
	server           string
	polarisPort      int
	consolePort      int
	skillPort        int
	namespace        string
	serviceName      string
	version          string
	skillName        string
	skillVersion     string
	skillCache       string
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
		net.JoinHostPort(server, fmt.Sprint(skillPort)),
		true,
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
	cfg.Description = "An Agent using a Skill installed from Polaris"
	cfg.AdvertiseHost = host
	cfg.AdvertisePort = actualPort
	cfg.RegistrationPort = registrationPort
	cfg.A2APath = "/a2a"
	cfg.Streaming = true
	cfg.EnsureService = true
	cfg.SkillMode = adapterconfig.SkillModeNames
	cfg.SkillRefs = []adapterconfig.SkillRef{{
		Namespace: namespace,
		Name:      skillName,
		Version:   skillVersion,
	}}
	cfg.SkillCacheDir = skillCache
	cfg.PolarisRequestTimeout = timeout

	runtime, err := adapteragent.New(
		adapteragent.WithConfig(cfg),
		adapteragent.WithSDKContext(sdk),
		adapteragent.WithAgentAPI(agentAPI),
	)
	if err != nil {
		return fmt.Errorf("create Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare Runtime: %w", err)
	}
	repository, err := runtime.SkillRepository()
	if err != nil {
		return fmt.Errorf("read installed Skill repository: %w", err)
	}
	if repository == nil {
		return fmt.Errorf("skill %s/%s@%s was not installed", namespace, skillName, skillVersion)
	}

	localAgent := llmagent.New(
		serviceName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithDescription(cfg.Description),
		llmagent.WithInstruction(
			"You have access to Agent Skills installed from Polaris. Load the "+
				"matching Skill before following its instructions.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPointer(1024),
			Temperature: floatPointer(0.2),
			Stream:      true,
		}),
		llmagent.WithSkills(repository),
	)
	localRunner := runner.NewRunner(serviceName, localAgent)
	defer func() { _ = localRunner.Close() }()

	snapshot, err := runtime.RegistrationSnapshot()
	if err != nil {
		return fmt.Errorf("read registration snapshot: %w", err)
	}
	log.Printf("Skill Agent ready: namespace=%s service=%s skill=%s@%s",
		namespace, serviceName, skillName, skillVersion)
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

func initFlags() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&polarisPort, "polaris-port", 8091, "Polaris Discovery gRPC port")
	flag.IntVar(&consolePort, "console-port", 8090, "Polaris Console HTTP port")
	flag.IntVar(&skillPort, "skill-port", 8094, "Polaris Skill gRPC port")
	flag.StringVar(&namespace, "namespace", "go-agent-naming-examples", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.examples.skill-agent", "A2A Agent service name")
	flag.StringVar(&version, "version", "v1.0.0", "Agent and instance version")
	flag.StringVar(&skillName, "skill", "polaris-greeting", "Skill name")
	flag.StringVar(&skillVersion, "skill-version", "1.0.0", "Skill version")
	defaultSkillCache := filepath.Join(os.TempDir(), "trpc-agent-naming-polarismesh-skill-cache")
	flag.StringVar(&skillCache, "skill-cache", defaultSkillCache, "Skill installation directory")
	flag.StringVar(&host, "host", "127.0.0.1", "Listen and advertise host")
	flag.IntVar(&port, "port", 18193, "A2A listen port; 0 chooses a free port")
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
