/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package demo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	a2aclient "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
)

var invocationSequence atomic.Uint64

// ClientFactory creates isolated mutable clients while sharing a safe transport.
func ClientFactory(transport http.RoundTripper, timeout time.Duration, authenticated, stream bool) naming.RemoteAgentOptionsFactory {
	return func(context.Context) ([]a2aagent.Option, error) {
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		opts := []a2aclient.Option{a2aclient.WithHTTPClient(client), a2aclient.WithTimeout(timeout)}
		if authenticated {
			opts = append(opts, a2aclient.WithAPIKeyAuth(FixtureAPIKey, FixtureKeyHeader))
		}
		return []a2aagent.Option{a2aagent.WithEnableStreaming(stream), a2aagent.WithA2AClientExtraOptions(opts...)}, nil
	}
}

// Invocation supplies a unique session for every request, including concurrent ones.
func Invocation(message string) *agent.Invocation {
	key := fmt.Sprintf("e2e-%d", invocationSequence.Add(1))
	invocation := agent.NewInvocation()
	invocation.InvocationID = key
	invocation.Message = model.NewUserMessage(message)
	invocation.Session = &session.Session{ID: key, UserID: "e2e-user"}
	return invocation
}

// Collect checks errors, completion and nonempty output on the actual event stream.
func Collect(ctx context.Context, events <-chan *event.Event) (string, int, error) {
	var final, deltas strings.Builder
	var count int
	var completed bool
	if events == nil {
		return "", 0, errors.New("A2A returned no event stream")
	}
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
					return "", count, errors.New("A2A stream closed without successful completion and output")
				}
				return text, count, nil
			}
			if evt == nil || evt.Response == nil {
				return "", count, errors.New("A2A returned an event without a response")
			}
			if evt.Error != nil {
				return "", count, fmt.Errorf("A2A error type=%s: %s", evt.Error.Type, evt.Error.Message)
			}
			count++
			completed = completed || evt.Done
			for _, choice := range evt.Choices {
				if choice.Message.Content != "" {
					final.WriteString(choice.Message.Content)
				}
				if choice.Delta.Content != "" {
					deltas.WriteString(choice.Delta.Content)
				}
			}
		}
	}
}

// Invoke follows the advertised card through the adapter's dynamic agent.
func Invoke(ctx context.Context, remote agent.Agent, message string) (string, int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := remote.Run(ctx, Invocation(message))
	if err != nil {
		return "", 0, err
	}
	return Collect(ctx, events)
}

// RunConsumer is a standalone real Polaris discovery and A2A invocation example.
func RunConsumer(ctx context.Context, o Options) error {
	sdk, err := NewSDK(o, false)
	if err != nil {
		return err
	}
	defer sdk.Destroy()
	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace, cfg.ServiceName, cfg.PolarisRequestTimeout = o.Namespace, o.Service, o.Timeout
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(sdk))
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return err
	}
	_, transport := HTTPClient(o.Timeout)
	defer transport.CloseIdleConnections()
	remote, err := runtime.NewRemoteAgent(ctx, o.Node, ClientFactory(transport, o.Timeout, true, o.Stream))
	if err != nil {
		return err
	}
	text, count, err := Invoke(ctx, remote, o.Message)
	if err != nil {
		return err
	}
	fmt.Printf("events=%d reply=%s\n", count, text)
	return nil
}
