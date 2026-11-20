/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package harness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/stateless"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/session"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	trpcregistry "trpc.group/trpc-go/trpc-go/naming/registry"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

var invocationSequence atomic.Uint64

// StartA2A starts the real A2A HTTP server and cleans it up with the test.
func StartA2A(
	t *testing.T,
	runtime *adapteragent.Runtime,
	agentRunner runner.Runner,
	listener net.Listener,
) *http.Server {
	t.Helper()
	var manager *stateless.TaskManager
	server, err := runtime.NewA2AServer(
		agentRunner,
		a2aserver.WithTaskManagerBuilder(
			func(processor taskmanager.MessageProcessor) (taskmanager.TaskManager, error) {
				var buildErr error
				manager, buildErr = stateless.NewTaskManager(processor)
				return manager, buildErr
			},
		),
	)
	if err != nil {
		t.Fatalf("create A2A server: %v", err)
	}
	if manager == nil {
		t.Fatal("A2A TaskManager was not created")
	}
	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = httpServer.Serve(listener)
	}()
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
		_ = manager.Close()
	})
	return httpServer
}

// RegisterProvider registers one provider and cleans up the instance.
func RegisterProvider(
	t *testing.T,
	sdk api.SDKContext,
	namespace, service, version, host string,
	registrationPort int,
	ttl time.Duration,
) *polarisregistry.Registry {
	t.Helper()
	ttlSeconds := int(ttl / time.Second)
	if ttlSeconds <= 0 {
		ttlSeconds = 2
	}
	registry, err := polarisregistry.NewRegistry(
		api.NewProviderAPIByContext(sdk),
		&polarisregistry.Config{
			Namespace:      namespace,
			ServiceName:    service,
			Version:        version,
			EnableRegister: true,
			HeartBeat:      1,
			TTL:            ttlSeconds,
			ServiceToken:   os.Getenv("POLARIS_SERVICE_TOKEN"),
		},
	)
	if err != nil {
		t.Fatalf("create Polaris registry: %v", err)
	}
	address := net.JoinHostPort(host, fmt.Sprint(registrationPort))
	if err := registry.Register(service, trpcregistry.WithAddress(address)); err != nil {
		t.Fatalf("register %s: %v", service, err)
	}
	t.Cleanup(func() { _ = registry.Deregister(service) })
	return registry
}

// ClientFactory creates an isolated A2A client.
func ClientFactory(timeout time.Duration, stream bool) adapternaming.RemoteAgentOptionsFactory {
	return func(context.Context) ([]a2aagent.Option, error) {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client := &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		return []a2aagent.Option{
			a2aagent.WithEnableStreaming(stream),
			a2aagent.WithA2AClientExtraOptions(
				a2aclient.WithHTTPClient(client),
				a2aclient.WithTimeout(timeout),
			),
		}, nil
	}
}

// Invocation creates a standalone invocation with a unique session.
func Invocation(message string) *agent.Invocation {
	id := fmt.Sprintf("e2e-%d", invocationSequence.Add(1))
	invocation := agent.NewInvocation()
	invocation.InvocationID = id
	invocation.Message = model.NewUserMessage(message)
	invocation.Session = &session.Session{ID: id, UserID: "e2e-user"}
	return invocation
}

// Collect drains one A2A event stream and requires non-empty completion.
func Collect(ctx context.Context, events <-chan *event.Event) (string, int, error) {
	if events == nil {
		return "", 0, errors.New("A2A returned no event stream")
	}
	var final, deltas strings.Builder
	var count int
	var completed bool
	for {
		select {
		case <-ctx.Done():
			return "", count, ctx.Err()
		case evt, ok := <-events:
			if !ok {
				text := final.String()
				if text == "" {
					text = deltas.String()
				}
				if count == 0 || text == "" || !completed {
					return "", count, errors.New("A2A stream closed without completion and output")
				}
				return text, count, nil
			}
			if evt == nil || evt.Response == nil {
				return "", count, errors.New("A2A event has no response")
			}
			if evt.Error != nil {
				return "", count, fmt.Errorf("A2A error: %s", evt.Error.Message)
			}
			count++
			completed = completed || evt.Done
			for _, choice := range evt.Choices {
				final.WriteString(choice.Message.Content)
				deltas.WriteString(choice.Delta.Content)
			}
		}
	}
}

// Invoke sends one message through a discovered Agent.
func Invoke(ctx context.Context, remote agent.Agent, message string) (string, int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := remote.Run(ctx, Invocation(message))
	if err != nil {
		return "", 0, err
	}
	return Collect(ctx, events)
}
