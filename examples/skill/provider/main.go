/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main installs a real Polaris Skill and serves a deterministic Agent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/stateless"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
)

var (
	server       string
	grpcPort     int
	skillAddress string
	namespace    string
	serviceName  string
	version      string
	skillName    string
	skillVersion string
	host         string
	port         int
	cacheDir     string
	timeout      time.Duration
	debug        bool
)

func main() {
	initArgs()
	flag.Parse()
	if server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
	}
	if skillName == "" {
		log.Fatal("--skill is required")
	}
	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] enable debug logs: %v", err)
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

	discover := net.JoinHostPort(server, fmt.Sprint(grpcPort))
	if skillAddress == "" {
		skillAddress = net.JoinHostPort(server, "8094")
	}
	sdk, err := demo.NewSDK(discover, skillAddress, true, timeout)
	if err != nil {
		return fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()

	if cacheDir == "" {
		cacheDir, err = os.MkdirTemp("", "trpc-polaris-skill-demo-*")
		if err != nil {
			return fmt.Errorf("create temporary Skill cache: %w", err)
		}
		defer func() { _ = os.RemoveAll(cacheDir) }()
	}
	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = serviceName
	cfg.AgentName = serviceName
	cfg.AgentVersion = version
	cfg.AdvertiseHost = host
	cfg.AdvertisePort = actualPort
	cfg.A2APath = "/a2a"
	cfg.Streaming = true
	cfg.PolarisRequestTimeout = timeout
	cfg.SkillRefs = []adapterconfig.SkillRef{{
		Namespace: namespace,
		Name:      skillName,
		Version:   skillVersion,
	}}
	cfg.SkillCacheDir = cacheDir

	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(sdk))
	if err != nil {
		return fmt.Errorf("create Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare Runtime and install Skill: %w", err)
	}
	repository, err := runtime.SkillRepository()
	if err != nil {
		return fmt.Errorf("read installed Skill repository: %w", err)
	}
	if repository == nil || len(repository.Summaries()) == 0 {
		return errors.New("installed Skill repository is empty")
	}

	localAgent := &skillAgent{name: serviceName, repo: repository, expected: skillName, version: skillVersion}
	localRunner := runner.NewRunner(serviceName, localAgent)
	defer func() { _ = localRunner.Close() }()

	var manager *stateless.TaskManager
	a2a, err := runtime.NewA2AServer(localRunner,
		a2aserver.WithTaskManagerBuilder(func(processor taskmanager.MessageProcessor) (taskmanager.TaskManager, error) {
			var buildErr error
			manager, buildErr = stateless.NewTaskManager(processor)
			return manager, buildErr
		}),
	)
	if err != nil {
		return fmt.Errorf("create A2A server: %w", err)
	}
	if manager == nil {
		return errors.New("A2A TaskManager was not created")
	}
	defer func() { _ = manager.Close() }()

	httpServer := &http.Server{Handler: a2a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()

	registry, err := demo.RegisterProvider(sdk, namespace, serviceName, version, host, actualPort)
	if err != nil {
		_ = httpServer.Close()
		return fmt.Errorf("register provider: %w", err)
	}
	defer func() { _ = registry.Deregister(serviceName) }()
	log.Printf("skill provider ready: service=%s skill=%s@%s cache=%s",
		serviceName, skillName, skillVersion, cacheDir)

	select {
	case <-ctx.Done():
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

func initArgs() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&grpcPort, "polaris-port", 8091, "Polaris gRPC port")
	flag.StringVar(&skillAddress, "skill-address", "", "Polaris Skill gRPC host:port")
	flag.StringVar(&namespace, "namespace", "go-naming-polarismesh-skill-demo", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.demo.skill", "A2A service name")
	flag.StringVar(&version, "version", "v1.0.0", "Agent and instance version")
	flag.StringVar(&skillName, "skill", "", "Published Skill name")
	flag.StringVar(&skillVersion, "skill-version", "1.0.0", "Published Skill version")
	flag.StringVar(&host, "host", "127.0.0.1", "Provider listen and advertise host")
	flag.IntVar(&port, "port", 18292, "Provider listen port")
	flag.StringVar(&cacheDir, "skill-cache", "", "Skill installation directory")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Polaris operation timeout")
	flag.BoolVar(&debug, "debug", false, "enable Polaris debug logs")
}

type skillAgent struct {
	name, expected, version string
	repo                    agentskill.Repository
}

func (a *skillAgent) Info() agent.Info {
	return agent.Info{Name: a.name, Description: "Deterministic Agent backed by a real Polaris Skill"}
}

func (*skillAgent) Tools() []tool.Tool { return nil }

func (*skillAgent) SubAgents() []agent.Agent { return nil }

func (*skillAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *skillAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is required")
	}
	skill, err := a.repo.Get(a.expected)
	if err != nil {
		return nil, err
	}
	out := make(chan *event.Event, 2)
	go func() {
		defer close(out)
		body := strings.TrimSpace(skill.Body)
		if runes := []rune(body); len(runes) > 500 {
			body = string(runes[:500])
		}
		content := fmt.Sprintf("skill|%s|%s|%s", skill.Summary.Name, a.version, body)
		response := &model.Response{
			ID: invocation.InvocationID, Done: true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage(content)}},
		}
		select {
		case out <- event.NewResponseEvent(invocation.InvocationID, a.name, response):
		case <-ctx.Done():
			return
		}
		select {
		case out <- event.NewResponseEvent(invocation.InvocationID, a.name, &model.Response{
			Object: model.ObjectTypeRunnerCompletion, Done: true,
		}):
		case <-ctx.Done():
		}
	}()
	return out, nil
}
