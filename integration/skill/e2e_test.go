//go:build integration

/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package skill_test

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
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/integration/internal/harness"
)

func TestPublishedSkillInstallAndModelInjection(t *testing.T) {
	cfg := harness.Load(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	skillName := cfg.Unique("skill")
	marker := "marker-" + cfg.RunID
	sdk, err := cfg.CreatePublishedSkill(skillName, "1.0.0", marker)
	if err != nil {
		t.Fatalf("publish Skill: %v", err)
	}
	t.Cleanup(sdk.Destroy)
	t.Cleanup(func() {
		if err := cfg.DeleteSkill(skillName); err != nil {
			t.Logf("delete Skill %s: %v", skillName, err)
		}
	})

	service := cfg.Unique("skill-agent")
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

	cfgProvider := adapterconfig.DefaultConfig()
	cfgProvider.Namespace = cfg.Namespace
	cfgProvider.ServiceName = service
	cfgProvider.AgentName = service
	cfgProvider.AgentVersion = "v1.0.0"
	cfgProvider.AdvertiseHost = "127.0.0.1"
	cfgProvider.AdvertisePort = port
	cfgProvider.A2APath = "/a2a"
	cfgProvider.Streaming = true
	cfgProvider.SkillMode = adapterconfig.SkillModeNames
	cfgProvider.SkillRefs = []adapterconfig.SkillRef{{
		Namespace: cfg.Namespace,
		Name:      skillName,
		Version:   "1.0.0",
	}}
	cfgProvider.SkillCacheDir = t.TempDir()
	cfgProvider.PolarisRequestTimeout = cfg.RequestTimeout

	runtime, err := adapteragent.New(
		adapteragent.WithConfig(cfgProvider),
		adapteragent.WithSDKContext(sdk),
	)
	if err != nil {
		t.Fatalf("create provider Runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if err := runtime.Prepare(ctx); err != nil {
		t.Fatalf("prepare provider Runtime: %v", err)
	}
	repository, err := runtime.SkillRepository()
	if err != nil {
		t.Fatalf("read Skill repository: %v", err)
	}
	if repository == nil {
		t.Fatal("Skill repository is nil")
	}

	scripted := &harness.ScriptedSkillModel{SkillName: skillName, Marker: marker}
	localAgent := llmagent.New(
		service,
		llmagent.WithModel(scripted),
		llmagent.WithDescription("Skill integration Agent"),
		llmagent.WithInstruction("Load the matching Skill before answering."),
		llmagent.WithSkills(repository),
	)
	localRunner := runner.NewRunner(service, localAgent)
	t.Cleanup(func() { _ = localRunner.Close() })
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
			return fmt.Errorf("healthy Skill Agent instances=%d, want 1", len(healthy))
		}
		instance = healthy[0]
		return nil
	})
	if err != nil {
		t.Fatalf("wait for Skill Agent instance: %v", err)
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
			"skill-e2e-caller",
			"JSONRPC",
			harness.ClientFactory(cfg.RequestTimeout, false),
		)
		if err != nil {
			return err
		}
		remoteAgent = created
		return nil
	})
	if err != nil {
		t.Fatalf("discover Skill Agent: %v", err)
	}

	var reply string
	err = harness.Eventually(ctx, cfg.RequestTimeout, 250*time.Millisecond, func() error {
		text, _, err := harness.Invoke(ctx, remoteAgent, "Give me the integration mark.")
		if err != nil {
			return err
		}
		if text != "skill-loaded" {
			return fmt.Errorf("unexpected Skill Agent reply %q", text)
		}
		reply = text
		return nil
	})
	if err != nil {
		t.Fatalf("invoke Skill Agent: %v", err)
	}
	if !scripted.SawMarker.Load() {
		t.Fatal("published Skill body did not reach the model request")
	}
	if scripted.Calls.Load() < 2 {
		t.Fatalf("model was called %d time(s), want at least 2", scripted.Calls.Load())
	}
	t.Logf("Skill Agent reply: %s, model calls=%d", reply, scripted.Calls.Load())
	harness.KeepIfRequested(t)
}
