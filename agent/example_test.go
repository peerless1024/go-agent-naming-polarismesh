/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agent_test

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
)

func ExampleRuntime_Prepare() {
	config := config.DefaultConfig()
	config.Namespace = "Production"
	config.ServiceName = "customer-agent"
	config.AgentName = "customer-agent"
	config.AgentVersion = "1.2.3"
	config.AdvertiseHost = "127.0.0.1"
	config.AdvertisePort = 18080
	config.A2APath = "/a2a"

	runtime, err := agent.New(agent.WithConfig(config))
	if err != nil {
		return
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	_ = runtime.Prepare(context.Background())
}

func ExampleRuntime_NewRemoteAgent() {
	runtime, err := agent.New(agent.WithService("Production", "customer-agent"))
	if err != nil {
		return
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(context.Background()); err != nil {
		return
	}
	_, _ = runtime.NewRemoteAgent(context.Background(), "tenant-a")
}
