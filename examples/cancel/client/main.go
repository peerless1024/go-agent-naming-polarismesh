/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main cancels a stream discovered through Polaris and verifies release.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"trpc.group/trpc-go/trpc-agent-go/event"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
)

type stats struct {
	Active    int64 `json:"active"`
	Completed int64 `json:"completed"`
	Canceled  int64 `json:"canceled"`
}

type result struct {
	Success        bool   `json:"success"`
	FirstEvent     string `json:"first_event"`
	StreamClosedMS int64  `json:"stream_closed_ms"`
	Active         int64  `json:"active"`
	Canceled       int64  `json:"canceled"`
	Error          string `json:"error,omitempty"`
}

var (
	server      string
	grpcPort    int
	namespace   string
	serviceName string
	caller      string
	timeout     time.Duration
	debug       bool
)

func main() {
	initArgs()
	flag.Parse()
	if server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
	}
	demo.ConfigureLogging(debug)
	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] enable debug logs: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	item, err := run(ctx)
	if err != nil {
		log.Fatal(err)
	}
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
	if !item.Success {
		log.Fatal(item.Error)
	}
}

func run(ctx context.Context) (result, error) {
	discover := net.JoinHostPort(server, fmt.Sprint(grpcPort))
	sdk, err := demo.NewSDK(discover, "", false, timeout)
	if err != nil {
		return result{}, fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()

	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = serviceName
	cfg.PolarisRequestTimeout = timeout
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(sdk))
	if err != nil {
		return result{}, fmt.Errorf("create Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return result{}, fmt.Errorf("prepare Runtime: %w", err)
	}
	resolver, err := runtime.Resolver()
	if err != nil {
		return result{}, err
	}
	resolved, err := resolver.Resolve(ctx, caller)
	if err != nil {
		return result{}, fmt.Errorf("resolve provider: %w", err)
	}
	remote, err := runtime.NewRemoteAgent(ctx, caller, demo.ClientFactory(timeout, true))
	if err != nil {
		return result{}, fmt.Errorf("create streaming remote Agent: %w", err)
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	events, err := remote.Run(runCtx, demo.Invocation("slow"))
	if err != nil {
		cancelRun()
		return result{}, fmt.Errorf("start stream: %w", err)
	}
	first, err := waitForFirstText(ctx, events)
	if err != nil {
		cancelRun()
		return result{}, err
	}
	startedClosing := time.Now()
	cancelRun()
	if err := drainUntilClosed(ctx, 3*time.Second, events); err != nil {
		return result{}, err
	}
	closedMS := time.Since(startedClosing).Milliseconds()

	endpoint, err := url.Parse(resolved.Endpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return result{}, fmt.Errorf("invalid resolved endpoint %q", resolved.Endpoint)
	}
	statsURL := endpoint.Scheme + "://" + endpoint.Host + "/e2e/stats"
	snapshot, err := waitForRelease(ctx, statsURL)
	if err != nil {
		return result{}, err
	}
	item := result{
		FirstEvent: first, StreamClosedMS: closedMS,
		Active: snapshot.Active, Canceled: snapshot.Canceled,
	}
	item.Success = closedMS <= 3000 && snapshot.Active == 0 && snapshot.Canceled > 0
	if !item.Success {
		item.Error = "stream did not close promptly or provider did not report cancellation"
	}
	return item, nil
}

func waitForFirstText(ctx context.Context, events <-chan *event.Event) (string, error) {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", fmt.Errorf("stream produced no text before timeout")
		case evt, ok := <-events:
			if !ok {
				return "", fmt.Errorf("stream closed before producing text")
			}
			if evt != nil && evt.Response != nil {
				for _, choice := range evt.Choices {
					text := choice.Delta.Content
					if text == "" {
						text = choice.Message.Content
					}
					if strings.TrimSpace(text) != "" {
						return text, nil
					}
				}
			}
		}
	}
}

func drainUntilClosed(ctx context.Context, timeout time.Duration, events <-chan *event.Event) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("event stream did not close after cancellation")
		case _, ok := <-events:
			if !ok {
				return nil
			}
		}
	}
}

func waitForRelease(ctx context.Context, statsURL string) (stats, error) {
	client, transport := demo.HTTPClient(timeout)
	defer transport.CloseIdleConnections()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var snapshot stats
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statsURL, nil)
		if err == nil {
			var response *http.Response
			response, err = client.Do(req)
			if err == nil {
				err = json.NewDecoder(response.Body).Decode(&snapshot)
				_ = response.Body.Close()
			}
		}
		if err == nil && snapshot.Active == 0 && snapshot.Canceled > 0 {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return stats{}, ctx.Err()
		case <-deadline.C:
			return stats{}, fmt.Errorf("provider did not release canceled run: %v", err)
		case <-ticker.C:
		}
	}
}

func initArgs() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&grpcPort, "polaris-port", 8091, "Polaris gRPC port")
	flag.StringVar(&namespace, "namespace", "go-naming-polarismesh-cancel-demo", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.demo.cancel", "A2A service name")
	flag.StringVar(&caller, "caller", "cancel-client", "Sticky caller key")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Polaris operation timeout")
	flag.BoolVar(&debug, "debug", false, "enable Polaris debug logs")
}
