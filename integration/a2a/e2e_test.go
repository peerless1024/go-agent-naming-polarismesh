//go:build integration

/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package a2a_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/integration/internal/harness"
)

func TestA2ARegistrationDiscoveryStreamingAndCancellation(t *testing.T) {
	cfg := harness.Load(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	service := cfg.Unique("a2a")
	if err := cfg.EnsureService(service, model.ServiceTypeAIAgent); err != nil {
		t.Fatalf("ensure AI_AGENT service: %v", err)
	}
	t.Cleanup(func() {
		if err := cfg.DeleteService(service, model.ServiceTypeAIAgent); err != nil {
			t.Logf("delete service %s: %v", service, err)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port

	sdk, err := cfg.SDK(false)
	if err != nil {
		t.Fatalf("create Polaris SDK: %v", err)
	}
	t.Cleanup(sdk.Destroy)

	providerConfig := adapterconfig.DefaultConfig()
	providerConfig.Namespace = cfg.Namespace
	providerConfig.ServiceName = service
	providerConfig.AgentName = service
	providerConfig.AgentVersion = "v1.0.0"
	providerConfig.AdvertiseHost = "127.0.0.1"
	providerConfig.AdvertisePort = port
	providerConfig.A2APath = "/a2a"
	providerConfig.Streaming = true
	providerConfig.PolarisRequestTimeout = cfg.RequestTimeout

	echo := &harness.EchoAgent{Name: service, Version: "v1.0.0"}
	localRunner := runner.NewRunner(service, echo)
	t.Cleanup(func() { _ = localRunner.Close() })

	runtime, err := adapteragent.New(
		adapteragent.WithConfig(providerConfig),
		adapteragent.WithSDKContext(sdk),
	)
	if err != nil {
		t.Fatalf("create provider Runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if err := runtime.Prepare(ctx); err != nil {
		t.Fatalf("prepare provider Runtime: %v", err)
	}
	harness.StartA2A(t, runtime, localRunner, listener)

	snapshot, err := runtime.RegistrationSnapshot()
	if err != nil {
		t.Fatalf("read registration snapshot: %v", err)
	}
	harness.RegisterProvider(
		t, sdk, cfg.Namespace, service, "v1.0.0",
		"127.0.0.1", snapshot.RegistrationPort, snapshot.TTL,
	)

	consumer := api.NewConsumerAPIByContext(sdk)
	var instance model.Instance
	err = harness.Eventually(ctx, cfg.RequestTimeout, 250*time.Millisecond, func() error {
		timeout := 2 * time.Second
		request := &api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{
			Namespace:       cfg.Namespace,
			Service:         service,
			SkipRouteFilter: true,
			Timeout:         &timeout,
		}}
		request.SetRetryCount(0)
		response, err := consumer.GetInstances(request)
		if err != nil {
			return err
		}
		var healthy []model.Instance
		for _, item := range response.GetInstances() {
			if item != nil && item.IsHealthy() && !item.IsIsolated() {
				healthy = append(healthy, item)
			}
		}
		if len(healthy) != 1 {
			return fmt.Errorf("healthy instances for %s=%d, want 1", service, len(healthy))
		}
		instance = healthy[0]
		return nil
	})
	if err != nil {
		t.Fatalf("wait for healthy instance: %v", err)
	}
	metadata := instance.GetMetadata()
	if got := metadata[model.MetadataKeyAIAgentName]; got != service {
		t.Errorf("ai-agent-name=%q, want %q", got, service)
	}
	if got := metadata[model.MetadataKeyAIAgentCard]; !strings.Contains(got, service) {
		t.Errorf("ai-agent-card does not contain %q", service)
	}
	if got := metadata[model.MetadataKeyA2AVersion]; got != "1.0" {
		t.Errorf("ai-a2a-version=%q, want 1.0", got)
	}
	if got := instance.GetVersion(); got != "v1.0.0" {
		t.Errorf("instance version=%q, want v1.0.0", got)
	}
	if got := instance.GetPort(); got != uint32(snapshot.RegistrationPort) {
		t.Errorf("instance port=%d, want %d", got, snapshot.RegistrationPort)
	}
	if got := instance.GetProtocol(); got != "" {
		t.Errorf("instance protocol=%q, want empty", got)
	}

	consumerConfig := adapterconfig.DefaultConfig()
	consumerConfig.Namespace = cfg.Namespace
	consumerConfig.ServiceName = service
	consumerConfig.AgentName = service
	consumerConfig.RegistryEnabled = false
	consumerConfig.DiscoveryEnabled = true
	consumerConfig.PolarisRequestTimeout = cfg.RequestTimeout
	consumerRuntime, err := adapteragent.New(
		adapteragent.WithConfig(consumerConfig),
		adapteragent.WithSDKContext(sdk),
	)
	if err != nil {
		t.Fatalf("create consumer Runtime: %v", err)
	}
	t.Cleanup(func() { _ = consumerRuntime.Close(context.Background()) })
	if err := consumerRuntime.Prepare(ctx); err != nil {
		t.Fatalf("prepare consumer Runtime: %v", err)
	}

	var remoteAgent agent.Agent
	err = harness.Eventually(ctx, cfg.RequestTimeout, 250*time.Millisecond, func() error {
		created, err := consumerRuntime.NewRemoteAgentWithBinding(
			ctx,
			"e2e-caller",
			"JSONRPC",
			harness.ClientFactory(cfg.RequestTimeout, true),
		)
		if err != nil {
			return err
		}
		remoteAgent = created
		return nil
	})
	if err != nil {
		t.Fatalf("discover remote Agent: %v", err)
	}

	var unary string
	err = harness.Eventually(ctx, cfg.RequestTimeout, 250*time.Millisecond, func() error {
		text, _, err := harness.Invoke(ctx, remoteAgent, "hello")
		if err != nil {
			return err
		}
		if text != "echo|v1.0.0|hello" {
			return fmt.Errorf("unexpected unary response %q", text)
		}
		unary = text
		return nil
	})
	if err != nil {
		t.Fatalf("invoke unary: %v", err)
	}
	t.Logf("unary response: %s", unary)

	streamText, events, err := harness.Invoke(ctx, remoteAgent, "stream")
	if err != nil {
		t.Fatalf("invoke streaming: %v", err)
	}
	if !strings.Contains(streamText, "stream") || events < 4 {
		t.Fatalf("streaming response=%q events=%d", streamText, events)
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	stream, err := remoteAgent.Run(runCtx, harness.Invocation("hold"))
	if err != nil {
		cancelRun()
		t.Fatalf("start hold stream: %v", err)
	}
	select {
	case <-stream:
	case <-time.After(10 * time.Second):
		cancelRun()
		t.Fatal("hold stream produced no event")
	}
	cancelRun()
	err = harness.Eventually(ctx, 10*time.Second, 100*time.Millisecond, func() error {
		if echo.Canceled.Load() == 0 {
			return fmt.Errorf("provider has not observed cancellation")
		}
		if echo.Active.Load() != 0 {
			return fmt.Errorf("provider still has %d active invocation(s)", echo.Active.Load())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("wait for cancellation: %v", err)
	}
	harness.KeepIfRequested(t)
}
