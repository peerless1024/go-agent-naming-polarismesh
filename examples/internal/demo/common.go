/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package demo contains small helpers shared by runnable examples.
package demo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go/api"
	polarisconfig "github.com/polarismesh/polaris-go/pkg/config"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-go/event"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	trpcregistry "trpc.group/trpc-go/trpc-go/naming/registry"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

var invocationSequence atomic.Uint64

// OwnerLabel marks resources created by the runnable examples.
const OwnerLabel = "examples.owner"

// ConfigureLogging keeps normal CLI output parseable; -debug enables Agent logs.
func ConfigureLogging(debug bool) {
	if debug {
		agentlog.SetLevel(agentlog.LevelDebug)
		return
	}
	agentlog.SetLevel(agentlog.LevelError)
}

// RegisterProvider registers the demo provider after Runtime.Prepare has
// installed its dynamic AgentCard metadata provider.
func RegisterProvider(
	sdk api.SDKContext,
	namespace, service, version, host string,
	port int,
) (*polarisregistry.Registry, error) {
	registry, err := polarisregistry.NewRegistry(api.NewProviderAPIByContext(sdk), &polarisregistry.Config{
		Namespace:      namespace,
		ServiceName:    service,
		Version:        version,
		EnableRegister: true,
		HeartBeat:      1,
		TTL:            2,
		Protocol:       "http",
		Metadata:       map[string]string{OwnerLabel: "trpc-agent-naming-polarismesh-demo"},
	})
	if err != nil {
		return nil, err
	}
	address := net.JoinHostPort(host, fmt.Sprint(port))
	if err := registry.Register(service, trpcregistry.WithAddress(address)); err != nil {
		return nil, err
	}
	return registry, nil
}

// NewSDK creates one Polaris SDK context for a demo process.
func NewSDK(discover, skillAddress string, enableSkills bool, timeout time.Duration) (api.SDKContext, error) {
	cfg := polarisconfig.NewDefaultConfiguration([]string{discover})
	cfg.GetGlobal().GetAPI().SetTimeout(timeout)
	cfg.GetGlobal().GetAPI().SetMaxRetryTimes(0)
	cfg.GetGlobal().GetAdmin().SetPort(0)
	cfg.GetGlobal().GetStatReporter().SetEnable(false)
	cfg.GetConsumer().GetLocalCache().SetPersistEnable(false)
	if token := os.Getenv("POLARIS_TOKEN"); token != "" {
		cfg.GetGlobal().GetServerConnector().SetToken(token)
	}
	if enableSkills {
		cfg.GetSkill().SetEnable(true)
		cfg.GetSkill().SetAddresses([]string{skillAddress})
		cfg.GetSkill().SetConnectTimeout(timeout)
		cfg.GetSkill().SetMessageTimeout(timeout)
		if token := firstEnvironment("POLARIS_SKILL_TOKEN", "POLARIS_TOKEN"); token != "" {
			cfg.GetSkill().SetToken(token)
		}
	}
	return api.InitContextByConfig(cfg)
}

// SignalContext cancels on SIGINT or SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// HTTPClient returns a direct HTTP client and its transport.
func HTTPClient(timeout time.Duration) (*http.Client, *http.Transport) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client, transport
}

// ClientFactory creates an isolated mutable A2A client for every remote agent.
func ClientFactory(timeout time.Duration, stream bool) adapternaming.RemoteAgentOptionsFactory {
	return func(context.Context) ([]a2aagent.Option, error) {
		client, _ := HTTPClient(timeout)
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
	id := fmt.Sprintf("demo-%d", invocationSequence.Add(1))
	invocation := agent.NewInvocation()
	invocation.InvocationID = id
	invocation.Message = model.NewUserMessage(message)
	invocation.Session = &session.Session{ID: id, UserID: "demo-user"}
	return invocation
}

// Collect drains an event stream and requires successful completion and output.
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
					return "", count, errors.New("A2A stream closed without successful completion")
				}
				return text, count, nil
			}
			if evt == nil || evt.Response == nil {
				return "", count, errors.New("A2A returned an event without a response")
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

// Invoke runs one remote agent request and collects its result.
func Invoke(ctx context.Context, remote agent.Agent, message string) (string, int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := remote.Run(ctx, Invocation(message))
	if err != nil {
		return "", 0, err
	}
	return Collect(ctx, events)
}

func firstEnvironment(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
