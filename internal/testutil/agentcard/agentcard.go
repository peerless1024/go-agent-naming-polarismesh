/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package agentcardtest provides AgentCard metadata helpers.
package agentcardtest

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
)

// CardMetadata builds valid instance metadata for an endpoint.
func CardMetadata(t *testing.T, host string, port int) map[string]string {
	t.Helper()
	cfg := testutil.ProviderConfig()
	cfg.AdvertiseHost = host
	cfg.AdvertisePort = port
	card, err := agentcard.NewAgentCard(cfg)
	if err != nil {
		t.Fatalf("NewAgentCard() error = %v", err)
	}
	metadata, err := agentcard.BuildAgentInstanceMetadata(cfg, card)
	if err != nil {
		t.Fatalf("BuildAgentInstanceMetadata() error = %v", err)
	}
	return metadata
}
