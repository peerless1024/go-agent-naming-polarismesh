/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agentcard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
)

func TestNewAgentCardAndMetadata(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		port   int
		scheme string
		path   string
	}{
		{name: "ipv4", host: "127.0.0.1", port: 18080, scheme: "http", path: "/a2a"},
		{name: "ipv6", host: "::1", port: 18443, scheme: "https", path: "/agent/v1"},
		{name: "domain non-root", host: "agent.example.com", port: 443, scheme: "https", path: "/api/a2a"},
		{name: "unicode path", host: "agent.example.com", port: 18080, scheme: "http", path: "/智能体/a2a"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testutil.ProviderConfig()
			config.AdvertiseHost = test.host
			config.AdvertisePort = test.port
			config.Scheme = test.scheme
			config.A2APath = test.path

			card, err := NewAgentCard(config)
			require.NoError(t, err)
			require.Equal(t, config.ServiceName, card.Name)
			require.NotEmpty(t, card.PrimaryURL())

			metadata, err := BuildAgentInstanceMetadata(config, card)
			require.NoError(t, err)
			require.Equal(t, config.ServiceName, metadata[MetadataAgentName])
			require.Equal(t, "JSONRPC", metadata[MetadataTransport])
			require.Equal(t, config.A2APath, metadata[MetadataPath])
			require.NoError(t, ValidateAgentInstanceMetadata(metadata))

			parsed, parsedCard, err := ParseAgentInstanceMetadata(metadata)
			require.NoError(t, err)
			require.Equal(t, config.ServiceName, parsed.AgentName)
			require.Equal(t, card.Name, parsedCard.Name)
		})
	}
}

func TestValidateAgentInstanceMetadataRejectsInvalidCards(t *testing.T) {
	metadata := cardMetadata(t, "127.0.0.1", 18080)

	t.Run("invalid json", func(t *testing.T) {
		copied := testutil.CloneStringMap(metadata)
		copied[MetadataAgentCard] = "{"
		require.Error(t, ValidateAgentInstanceMetadata(copied))
	})

	t.Run("trailing json", func(t *testing.T) {
		copied := testutil.CloneStringMap(metadata)
		copied[MetadataAgentCard] += " trailing"
		require.Error(t, ValidateAgentInstanceMetadata(copied))
	})

	t.Run("name mismatch", func(t *testing.T) {
		copied := testutil.CloneStringMap(metadata)
		copied[MetadataAgentName] = "other-agent"
		require.Error(t, ValidateAgentInstanceMetadata(copied))
	})

	t.Run("path mismatch", func(t *testing.T) {
		copied := testutil.CloneStringMap(metadata)
		copied[MetadataPath] = "/other"
		require.Error(t, ValidateAgentInstanceMetadata(copied))
	})

	t.Run("transport missing", func(t *testing.T) {
		copied := testutil.CloneStringMap(metadata)
		delete(copied, MetadataTransport)
		require.Error(t, ValidateAgentInstanceMetadata(copied))
	})

	t.Run("sensitive key", func(t *testing.T) {
		copied := testutil.CloneStringMap(metadata)
		copied["authorization"] = "bearer value"
		require.Error(t, ValidateAgentInstanceMetadata(copied))
	})

	t.Run("too many entries", func(t *testing.T) {
		copied := testutil.CloneStringMap(metadata)
		for i := 0; i < maxInstanceMetadataEntries; i++ {
			copied["key-"+strings.Repeat("x", i+1)] = "value"
		}
		require.Error(t, ValidateAgentInstanceMetadata(copied))
	})

	t.Run("oversized card", func(t *testing.T) {
		var card a2aprotocolserver.AgentCard
		require.NoError(t, json.Unmarshal([]byte(metadata[MetadataAgentCard]), &card))
		card.Description = strings.Repeat("x", maxMetadataValueCharacters)
		raw, err := json.Marshal(card)
		require.NoError(t, err)
		copied := testutil.CloneStringMap(metadata)
		copied[MetadataAgentCard] = string(raw)
		require.Error(t, ValidateAgentInstanceMetadata(copied))
	})
}

func TestValidateAgentCardRejectsEndpointMismatch(t *testing.T) {
	config := testutil.ProviderConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)

	card.Name = "other"
	require.Error(t, ValidateAgentCard(config, &card))

	card.Name = config.ServiceName
	card.SupportedInterfaces[0].URL = "http://127.0.0.1:18081/a2a"
	require.Error(t, ValidateAgentCard(config, &card))

	card.SupportedInterfaces[0].URL = "http://127.0.0.1:18080/a2a?token=value"
	require.Error(t, ValidateAgentCard(config, &card))
}

func TestNewAgentCardAdvertisesTools(t *testing.T) {
	card, err := NewAgentCard(testutil.ProviderConfig(), testTool{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(card.Skills), 2)
	require.Equal(t, "tool-lookup", card.Skills[1].ID)
}

func TestBuildAgentInstanceMetadataPreservesLegacyCardShape(t *testing.T) {
	config := testutil.ProviderConfig()
	transport := "JSONRPC"
	card := a2aprotocolserver.AgentCard{
		Name:               config.ServiceName,
		Version:            config.AgentVersion,
		URL:                "http://127.0.0.1:18080/a2a",
		PreferredTransport: &transport,
	}
	metadata, err := BuildAgentInstanceMetadata(config, card)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(metadata[MetadataAgentCard]), &raw))
	_, hasSupportedInterfaces := raw["supportedInterfaces"]
	require.False(t, hasSupportedInterfaces)
	require.Equal(t, "JSONRPC", metadata[MetadataTransport])
}

func TestValidateAgentCardRejectsNonHTTPScheme(t *testing.T) {
	config := testutil.ProviderConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	card.SupportedInterfaces[0].URL = "ftp://127.0.0.1:18080/a2a"

	_, err = BuildAgentInstanceMetadata(config, card)
	require.Error(t, err)
	require.Contains(t, err.Error(), "only http and https are allowed")
}

func TestValidateAgentCardErrorDoesNotEchoCredentials(t *testing.T) {
	config := testutil.ProviderConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	card.SupportedInterfaces[0].URL =
		"http://review-user:review-password@127.0.0.1:18080/a2a?access_token=review-secret"

	_, err = BuildAgentInstanceMetadata(config, card)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "review-user")
	require.NotContains(t, err.Error(), "review-password")
	require.NotContains(t, err.Error(), "review-secret")
}

type testTool struct{}

func (testTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "lookup", Description: "Look up a record"}
}

func cardMetadata(t *testing.T, host string, port int) map[string]string {
	t.Helper()
	cfg := testutil.ProviderConfig()
	cfg.AdvertiseHost = host
	cfg.AdvertisePort = port
	card, err := NewAgentCard(cfg)
	if err != nil {
		t.Fatalf("NewAgentCard() error = %v", err)
	}
	metadata, err := BuildAgentInstanceMetadata(cfg, card)
	if err != nil {
		t.Fatalf("BuildAgentInstanceMetadata() error = %v", err)
	}
	return metadata
}
