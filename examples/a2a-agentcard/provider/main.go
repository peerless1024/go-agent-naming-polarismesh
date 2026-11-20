/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main runs Agent A and versioned Agent B providers.
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
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/stateless"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
)

var (
	server             string
	grpcPort           int
	namespace          string
	role               string
	serviceName        string
	targetServiceName  string
	targetVersionsText string
	version            string
	host               string
	port               int
	registrationPort   int
	timeout            time.Duration
	debug              bool
	detach             bool
	a2aVersion         string
	noServe            bool
	cardPath           string
	token              string
	serviceToken       string
)

func main() {
	initArgs()
	flag.Parse()
	if server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
	}
	if token != "" {
		_ = os.Setenv("POLARIS_TOKEN", token)
	}
	if serviceToken != "" {
		_ = os.Setenv("POLARIS_SERVICE_TOKEN", serviceToken)
	}
	if role != "" && role != "agent-a" && role != "agent-b" {
		log.Fatal("--role must be agent-a or agent-b when set")
	}
	if role == "" {
		if a2aVersion != "1.0" && a2aVersion != "0.3" {
			log.Fatal("--a2a-version must be 1.0 or 0.3")
		}
		if noServe && cardPath == "" {
			log.Fatal("--no-serve requires --card")
		}
	} else if role == "agent-a" && targetServiceName == "" {
		log.Fatal("--target-service is required for agent-a")
	}
	if detach {
		if _, err := syscall.Setsid(); err != nil {
			log.Fatalf("detach provider: %v", err)
		}
	}
	demo.ConfigureLogging(debug)
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
	if role == "" {
		return runMultiInterfaceA2A(ctx)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = listener.Close() }()
	actualPort := listener.Addr().(*net.TCPAddr).Port

	discover := net.JoinHostPort(server, fmt.Sprint(grpcPort))
	sdk, err := demo.NewSDK(discover, "", false, timeout)
	if err != nil {
		return fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()

	var targetRuntime *adapteragent.Runtime
	var localAgent agent.Agent
	if role == "agent-a" {
		targetRuntime, localAgent, err = buildAgentA(ctx, sdk, targetServiceName)
		if err != nil {
			return err
		}
		defer func() { _ = targetRuntime.Close(context.Background()) }()
	} else {
		localAgent = &agentB{name: serviceName, version: version}
	}

	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = serviceName
	cfg.AgentName = serviceName
	cfg.AgentVersion = version
	cfg.AdvertiseHost = host
	cfg.AdvertisePort = actualPort
	cfg.RegistrationPort = registrationPort
	cfg.A2APath = "/a2a"
	cfg.Streaming = true
	cfg.PolarisRequestTimeout = timeout

	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(sdk))
	if err != nil {
		return fmt.Errorf("create Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare Runtime: %w", err)
	}

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

	snapshot, err := runtime.RegistrationSnapshot()
	if err != nil {
		_ = httpServer.Close()
		return fmt.Errorf("read registration snapshot: %w", err)
	}
	registry, err := demo.RegisterProvider(
		sdk, namespace, serviceName, version, host, snapshot.RegistrationPort, snapshot.TTL,
	)
	if err != nil {
		_ = httpServer.Close()
		return fmt.Errorf("register provider: %w", err)
	}
	defer func() { _ = registry.Deregister(serviceName) }()

	log.Printf("a2a-agentcard provider ready: role=%s service=%s address=%s",
		role, serviceName, net.JoinHostPort(host, fmt.Sprint(actualPort)))
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

func buildAgentA(
	ctx context.Context,
	sdk api.SDKContext,
	targetService string,
) (*adapteragent.Runtime, agent.Agent, error) {
	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = targetService
	cfg.PolarisRequestTimeout = timeout
	targetRuntime, err := adapteragent.New(
		adapteragent.WithConfig(cfg),
		adapteragent.WithSDKContext(sdk),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create target Runtime: %w", err)
	}
	if err := targetRuntime.Prepare(ctx); err != nil {
		_ = targetRuntime.Close(context.Background())
		return nil, nil, fmt.Errorf("prepare target Runtime: %w", err)
	}

	remotes, versions, err := versionedRemoteAgents(ctx, targetRuntime)
	if err != nil {
		_ = targetRuntime.Close(context.Background())
		return nil, nil, err
	}
	return targetRuntime, &agentA{name: serviceName, remotes: remotes, versions: versions}, nil
}

func versionedRemoteAgents(
	ctx context.Context,
	targetRuntime *adapteragent.Runtime,
) ([]agent.Agent, []string, error) {
	versions := strings.Split(targetVersionsText, ",")
	if len(versions) == 0 {
		return nil, nil, errors.New("target versions are required")
	}
	resolver, err := targetRuntime.Resolver()
	if err != nil {
		return nil, nil, fmt.Errorf("create target Resolver: %w", err)
	}

	remotes := make([]agent.Agent, 0, len(versions))
	selected := make([]string, 0, len(versions))
	for _, version := range versions {
		version = strings.TrimSpace(version)
		if version == "" {
			return nil, nil, errors.New("target versions must not be empty")
		}
		remote, err := remoteForVersion(ctx, resolver, version)
		if err != nil {
			return nil, nil, err
		}
		remotes = append(remotes, remote)
		selected = append(selected, version)
	}
	return remotes, selected, nil
}

func remoteForVersion(
	ctx context.Context,
	resolver *adapternaming.Resolver,
	version string,
) (agent.Agent, error) {
	var lastSeen string
	var lastErr error
	for attempt := 0; attempt < 64; attempt++ {
		caller := fmt.Sprintf("agent-a-%s-%d", version, attempt)
		resolved, err := resolver.Resolve(ctx, caller)
		if err != nil {
			lastErr = err
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		lastSeen = resolved.Card.Version
		if lastSeen != version {
			continue
		}
		return resolver.NewRemoteAgent(ctx, caller, demo.ClientFactory(timeout, false))
	}
	if lastErr != nil {
		return nil, fmt.Errorf("discover agent B version %s: %w", version, lastErr)
	}
	return nil, fmt.Errorf("agent B version %s was not discovered; last seen %s", version, lastSeen)
}

func initArgs() {
	registrationDefault, err := demo.RegistrationPortEnvironment(1)
	if err != nil {
		log.Fatal(err)
	}
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&grpcPort, "polaris-port", 8091, "Polaris gRPC port")
	flag.StringVar(&namespace, "namespace", "go-agent-naming-e2e", "Polaris namespace")
	flag.StringVar(&role, "role", "",
		"Leave empty for the default multi-interface A2A provider; set agent-a or agent-b for the multi-version flow")
	flag.StringVar(&serviceName, "service", "trpc.polaris.demo.agent.b", "This provider's Polaris service")
	flag.StringVar(&targetServiceName, "target-service", "", "Agent B service called by Agent A")
	flag.StringVar(&targetVersionsText, "target-versions", "v1.0.0,v2.0.0",
		"Comma-separated Agent B instance versions required by Agent A")
	flag.StringVar(&version, "version", "v1.0.0", "Agent and instance version")
	flag.StringVar(&host, "host", "127.0.0.1", "Provider listen and advertise host")
	flag.IntVar(&port, "port", 28221, "Provider listen port")
	flag.IntVar(&registrationPort, "registration-port", registrationDefault, "Polaris A2A registration port")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Polaris operation timeout")
	flag.BoolVar(&debug, "debug", false, "enable Polaris and Agent debug logs")
	flag.BoolVar(&detach, "detach", false, "run in a new session so verify.sh can retain the provider")
	flag.StringVar(&a2aVersion, "a2a-version", "1.0",
		"AgentCard schema for the default multi-interface A2A provider: 1.0 or 0.3")
	flag.BoolVar(&noServe, "no-serve", false,
		"default A2A provider: register an external AgentCard without binding or serving")
	flag.StringVar(&cardPath, "card", "",
		"default A2A provider: AgentCard JSON file for --no-serve or URL rewriting")
	flag.StringVar(&token, "token", os.Getenv("POLARIS_TOKEN"), "Polaris SDK token")
	flag.StringVar(&serviceToken, "service-token", os.Getenv("POLARIS_SERVICE_TOKEN"),
		"Polaris service registration token")
}

type agentA struct {
	name     string
	remotes  []agent.Agent
	versions []string
}

func (a *agentA) Info() agent.Info {
	return agent.Info{Name: a.name, Description: "Agent A calling versioned Agent B instances"}
}

func (*agentA) Tools() []tool.Tool { return nil }

func (*agentA) SubAgents() []agent.Agent { return nil }

func (*agentA) FindSubAgent(string) agent.Agent { return nil }

func (a *agentA) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is required")
	}
	responses := make([]string, 0, len(a.remotes))
	for index, remote := range a.remotes {
		text, _, err := demo.Invoke(ctx, remote, "hello-from-agent-a")
		if err != nil {
			return nil, fmt.Errorf("call Agent B version %s: %w", a.versions[index], err)
		}
		responses = append(responses, fmt.Sprintf("b=%s:%s", a.versions[index], text))
	}
	content := a.name + "|fanout|" + strings.Join(responses, ";")
	out := make(chan *event.Event, 2)
	go func() {
		defer close(out)
		emit := func(evt *event.Event) bool {
			select {
			case out <- evt:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !emit(event.NewResponseEvent(invocation.InvocationID, a.name, &model.Response{
			ID: invocation.InvocationID, Done: true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage(content)}},
		})) {
			return
		}
		emit(event.NewResponseEvent(invocation.InvocationID, a.name, &model.Response{
			Object: model.ObjectTypeRunnerCompletion, Done: true,
		}))
	}()
	return out, nil
}

type agentB struct {
	name, version string
}

func (a *agentB) Info() agent.Info {
	return agent.Info{Name: a.name, Description: "Versioned Agent B provider"}
}

func (*agentB) Tools() []tool.Tool { return nil }

func (*agentB) SubAgents() []agent.Agent { return nil }

func (*agentB) FindSubAgent(string) agent.Agent { return nil }

func (a *agentB) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is required")
	}
	out := make(chan *event.Event, 2)
	go func() {
		defer close(out)
		content := fmt.Sprintf("agent-b|%s|echo:%s", a.version, invocation.Message.Content)
		emit := func(evt *event.Event) bool {
			select {
			case out <- evt:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !emit(event.NewResponseEvent(invocation.InvocationID, a.name, &model.Response{
			ID: invocation.InvocationID, Done: true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage(content)}},
		})) {
			return
		}
		emit(event.NewResponseEvent(invocation.InvocationID, a.name, &model.Response{
			Object: model.ObjectTypeRunnerCompletion, Done: true,
		}))
	}()
	return out, nil
}
