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
	a2aprotocol "trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
)

func TestNewAgentCardUsesThreeKeyMetadata(t *testing.T) {
	for _, test := range []struct {
		name, host, scheme, path string
		port                     int
	}{
		{name: "ipv4", host: "127.0.0.1", port: 18080, scheme: "http", path: "/a2a"},
		{name: "ipv6", host: "::1", port: 18443, scheme: "https", path: "/agent/v1"},
		{name: "unicode path", host: "127.0.0.1", port: 18080, scheme: "http", path: "/智能体/a2a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := providerConfig()
			config.AdvertiseHost = test.host
			config.AdvertisePort = test.port
			config.RegistrationPort = 1
			config.Scheme = test.scheme
			config.A2APath = test.path

			card, err := NewAgentCard(config)
			require.NoError(t, err)
			metadata, err := BuildAgentInstanceMetadata(config, card)
			require.NoError(t, err)
			require.Len(t, metadata, 3)
			require.Equal(t, config.ServiceName, metadata[MetadataAgentName])
			require.Equal(t, "1.0", metadata[MetadataA2AVersion])

			parsed, parsedCard, err := ParseAgentInstanceMetadata(metadata)
			require.NoError(t, err)
			require.Equal(t, SchemaVersionV10, parsed.A2AVersion)
			require.Equal(t, card.Name, parsedCard.Name)
			require.Equal(t, card.Version, parsedCard.Version)
			require.Equal(t, card.PrimaryURL(), parsedCard.PrimaryURL())
			require.Equal(t, card.SupportedInterfaces[0].Tenant, parsedCard.SupportedInterfaces[0].Tenant)
		})
	}
}

func TestParseCardDocumentSupportsV03AndPreservesRawJSON(t *testing.T) {
	config := providerConfig()
	config.A2AVersion = "0.3"
	raw := []byte(`{
		"name":"customer-agent",
		"description":"Customer support agent",
		"version":"1.2.3",
		"url":"http://127.0.0.1:18080/a2a",
		"preferredTransport":"JSONRPC",
		"x-vendor":"keep-me",
		"signatures":[{"protected":"header","signature":"signed"}],
		"capabilities":{},
		"defaultInputModes":[],
		"defaultOutputModes":[],
		"skills":[]
	}`)
	document, err := ParseCardDocument(config, SchemaVersionV03, raw)
	require.NoError(t, err)
	require.Equal(t, string(raw), string(document.Raw()))
	metadata, err := document.Metadata(config)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), metadata[MetadataAgentCard])
	require.Equal(t, "0.3", metadata[MetadataA2AVersion])
}

func TestParseCardDocumentRejectsCrossSchemaShape(t *testing.T) {
	config := providerConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	v10Raw, err := json.Marshal(card)
	require.NoError(t, err)
	_, err = ParseCardDocument(config, SchemaVersionV03, v10Raw)
	require.ErrorContains(t, err, "must not use v1 supportedInterfaces")

	v03Raw := []byte(`{"name":"customer-agent","version":"1.2.3","url":"http://127.0.0.1:18080/a2a","preferredTransport":"JSONRPC"}`)
	_, err = ParseCardDocument(config, SchemaVersionV10, v03Raw)
	require.ErrorContains(t, err, "must not use legacy field")
}

func TestValidateAgentCardEnforcesSameIPAndIPOnlyInterfaces(t *testing.T) {
	config := providerConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	card.SupportedInterfaces = append(card.SupportedInterfaces, a2aprotocolserver.AgentInterface{
		URL:             "https://127.0.0.1:18443/grpc",
		ProtocolBinding: "GRPC",
	})
	require.NoError(t, ValidateAgentCard(config, &card, SchemaVersionV10))

	card.SupportedInterfaces[1].URL = "https://127.0.0.2:18443/grpc"
	require.ErrorContains(t, ValidateAgentCard(config, &card, SchemaVersionV10), "same IP")

	card.SupportedInterfaces[1].URL = "https://agent.example.com/grpc"
	require.ErrorContains(t, ValidateAgentCard(config, &card, SchemaVersionV10), "must be an IP address")
}

func TestInterfaceSelectionUsesBindingNotInstancePort(t *testing.T) {
	config := providerConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	card.SupportedInterfaces = append(card.SupportedInterfaces, a2aprotocolserver.AgentInterface{
		URL:             "http://127.0.0.1:19000/grpc",
		ProtocolBinding: "GRPC",
		Tenant:          "grpc-tenant",
		ProtocolVersion: "1.0",
	})
	item, ok := SelectInterface(card, "GRPC")
	require.True(t, ok)
	require.Equal(t, "grpc-tenant", item.Tenant)
	endpoint, err := Endpoint(item)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:19000/grpc", endpoint)
}

func TestValidateAgentInstanceMetadataRejectsInvalidCards(t *testing.T) {
	metadata := cardMetadata(t)
	for name, mutate := range map[string]func(map[string]string){
		"invalid json": func(value map[string]string) { value[MetadataAgentCard] = "{" },
		"missing version": func(value map[string]string) {
			delete(value, MetadataA2AVersion)
		},
		"unknown version": func(value map[string]string) { value[MetadataA2AVersion] = "2.0" },
		"name mismatch":   func(value map[string]string) { value[MetadataAgentName] = "other-agent" },
		"credential key": func(value map[string]string) {
			value["authorization"] = "bearer value"
		},
		"unexpected key": func(value map[string]string) {
			value["extra"] = "value"
		},
	} {
		t.Run(name, func(t *testing.T) {
			copied := cloneStringMap(metadata)
			mutate(copied)
			require.Error(t, ValidateAgentInstanceMetadata(copied))
		})
	}
}

func TestValidateAgentInstanceMetadataAllowsPromotedPlatformFields(t *testing.T) {
	metadata := cardMetadata(t)
	metadata["version"] = "1.0.0"
	metadata["region"] = ""
	metadata["zone"] = ""
	metadata["campus"] = ""
	metadata["internal-lastheartbeat"] = "1795058745"
	metadata["internal-empty-protection"] = "true"

	require.Len(t, metadata, 9)
	require.NoError(t, ValidateAgentInstanceMetadata(metadata))

	parsed, card, err := ParseAgentInstanceMetadata(metadata)
	require.NoError(t, err)
	require.Equal(t, SchemaVersionV10, parsed.A2AVersion)
	require.Equal(t, metadata[MetadataAgentName], card.Name)
	require.Equal(t, metadata[MetadataAgentCard], parsed.CardJSON)
}

func TestValidateAgentCardRejectsCredentialsAndNonHTTPSchemes(t *testing.T) {
	config := providerConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	card.SupportedInterfaces[0].URL = "ftp://127.0.0.1:18080/a2a"
	require.ErrorContains(t, ValidateAgentCard(config, &card, SchemaVersionV10), "not supported")

	card.SupportedInterfaces[0].URL =
		"http://review-user:review-password@127.0.0.1:18080/a2a?access_token=review-secret"
	err = ValidateAgentCard(config, &card, SchemaVersionV10)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "review-user")
	require.NotContains(t, err.Error(), "review-password")
	require.NotContains(t, err.Error(), "review-secret")
}

func TestNewAgentCardAdvertisesTools(t *testing.T) {
	card, err := NewAgentCard(providerConfig(), testTool{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(card.Skills), 2)
	require.Equal(t, "tool-lookup", card.Skills[1].ID)
}

func TestCanonicalV1CardPreservesSecurityRequirementsWithoutLegacySecurity(t *testing.T) {
	config := providerConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	card.SecurityRequirements = a2aprotocol.SecurityRequirements{
		{"oauth2": {"agent.read"}},
	}
	metadata, err := BuildAgentInstanceMetadata(config, card)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(metadata[MetadataAgentCard]), &raw))
	require.Contains(t, raw, "securityRequirements")
	require.NotContains(t, raw, "security")
	require.NoError(t, ValidateAgentInstanceMetadata(metadata))
}

func TestMetadataValueLimit(t *testing.T) {
	config := providerConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	card.Description = strings.Repeat("x", maxMetadataValueCharacters)
	raw, err := json.Marshal(card)
	require.NoError(t, err)
	metadata, err := BuildAgentInstanceMetadataFromRaw(config, SchemaVersionV10, raw)
	require.Error(t, err)
	require.Nil(t, metadata)
}

type testTool struct{}

func (testTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "lookup", Description: "Look up a record"}
}

func cardMetadata(t *testing.T) map[string]string {
	t.Helper()
	config := providerConfig()
	card, err := NewAgentCard(config)
	require.NoError(t, err)
	metadata, err := BuildAgentInstanceMetadata(config, card)
	require.NoError(t, err)
	return metadata
}

func providerConfig() adapterconfig.Config {
	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = "Production"
	cfg.ServiceName = "customer-agent"
	cfg.AgentName = "customer-agent"
	cfg.AgentVersion = "1.2.3"
	cfg.Description = "Customer support agent"
	cfg.AdvertiseHost = "127.0.0.1"
	cfg.AdvertisePort = 18080
	cfg.A2APath = "/a2a"
	cfg.Transport = "JSONRPC"
	return cfg
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
