/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main runs a slow deterministic Agent used by the cancellation demo.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"
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
)

var (
	server      string
	grpcPort    int
	namespace   string
	serviceName string
	version     string
	host        string
	port        int
	timeout     time.Duration
	debug       bool
)

func main() {
	initArgs()
	flag.Parse()
	if server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
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
	sdk, err := demo.NewSDK(discover, "", false, timeout)
	if err != nil {
		return fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()

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
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(sdk))
	if err != nil {
		return fmt.Errorf("create Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare Runtime: %w", err)
	}

	stats := &providerStats{}
	localAgent := &slowAgent{name: serviceName, stats: stats}
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

	mux := http.NewServeMux()
	mux.HandleFunc("/e2e/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stats.snapshot())
	})
	mux.Handle("/", a2a.Handler())
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()

	registry, err := demo.RegisterProvider(sdk, namespace, serviceName, version, host, actualPort)
	if err != nil {
		_ = httpServer.Close()
		return fmt.Errorf("register provider: %w", err)
	}
	defer func() { _ = registry.Deregister(serviceName) }()
	log.Printf("cancel provider ready: service=%s address=%s",
		serviceName, net.JoinHostPort(host, fmt.Sprint(actualPort)))

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
	flag.StringVar(&namespace, "namespace", "go-naming-polarismesh-cancel-demo", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.demo.cancel", "A2A service name")
	flag.StringVar(&version, "version", "v1.0.0", "Agent and instance version")
	flag.StringVar(&host, "host", "127.0.0.1", "Provider listen and advertise host")
	flag.IntVar(&port, "port", 18393, "Provider listen port")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Polaris operation timeout")
	flag.BoolVar(&debug, "debug", false, "enable Polaris debug logs")
}

type statsSnapshot struct {
	Active    int64 `json:"active"`
	Completed int64 `json:"completed"`
	Canceled  int64 `json:"canceled"`
}

type providerStats struct {
	active, completed, canceled atomic.Int64
}

func (s *providerStats) snapshot() statsSnapshot {
	return statsSnapshot{Active: s.active.Load(), Completed: s.completed.Load(), Canceled: s.canceled.Load()}
}

type slowAgent struct {
	name  string
	stats *providerStats
}

func (a *slowAgent) Info() agent.Info {
	return agent.Info{Name: a.name, Description: "Slow deterministic Agent for cancellation"}
}

func (*slowAgent) Tools() []tool.Tool { return nil }

func (*slowAgent) SubAgents() []agent.Agent { return nil }

func (*slowAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *slowAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is required")
	}
	out := make(chan *event.Event, 4)
	go func() {
		defer close(out)
		a.stats.active.Add(1)
		defer a.stats.active.Add(-1)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		for index := 0; ; index++ {
			response := &model.Response{
				ID: invocation.InvocationID, IsPartial: true,
				Choices: []model.Choice{{Delta: model.NewAssistantMessage(
					fmt.Sprintf("cancel-demo|tick-%d|", index+1),
				)}},
			}
			select {
			case out <- event.NewResponseEvent(invocation.InvocationID, a.name, response):
			case <-ctx.Done():
				a.stats.canceled.Add(1)
				return
			}
			select {
			case <-ctx.Done():
				a.stats.canceled.Add(1)
				return
			case <-deadline.C:
				return
			case <-ticker.C:
			}
		}
	}()
	return out, nil
}
