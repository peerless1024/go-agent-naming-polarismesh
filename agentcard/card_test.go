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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	polarismodel "github.com/polarismesh/polaris-go/pkg/model"
)

const fullAgentCardV10 = `{
  "name": "customer-agent",
  "description": "customer support agent",
  "version": "1.0.0",
  "supportedInterfaces": [
    {"url": "http://127.0.0.1:18080/a2a", "protocolBinding": "JSONRPC", "protocolVersion": "1.0", "tenant": "tenant-a"},
    {"url": "https://127.0.0.1:18443/grpc", "protocolBinding": "GRPC", "protocolVersion": "1.0"}
  ],
  "provider": {"organization": "Tencent", "url": "https://agent.example.com"},
  "documentationUrl": "https://agent.example.com/docs",
  "iconUrl": "https://agent.example.com/icon.png",
  "capabilities": {
    "streaming": true,
    "pushNotifications": true,
    "extendedAgentCard": true,
    "extensions": [
      {"uri": "https://example.com/ext", "description": "example", "required": true, "params": {"mode": "fast"}}
    ]
  },
  "securitySchemes": {
    "bearer": {"apiKeySecurityScheme": {"location": "header", "name": "Authorization"}}
  },
  "securityRequirements": [
    {"schemes": {"bearer": {"list": ["read"]}}}
  ],
  "defaultInputModes": ["text/plain"],
  "defaultOutputModes": ["text/plain"],
  "skills": [
    {
      "id": "answer",
      "name": "answer",
      "description": "answer questions",
      "tags": ["support"],
      "examples": ["hello"],
      "inputModes": ["text/plain"],
      "outputModes": ["text/plain"],
      "securityRequirements": [{"schemes": {"bearer": {"list": ["read"]}}}]
    }
  ],
  "signatures": [
    {"header": {"kid": "key-1"}, "protected": "eyJhbGciOiJFUzI1NiJ9", "signature": "c2lnbmF0dXJl"}
  ],
  "x-vendor-field": {"enabled": true}
}`

const fullAgentCardV03 = `{
  "name": "legacy-agent",
  "description": "legacy agent",
  "version": "2.0.0",
  "url": "http://127.0.0.1:18080/a2a",
  "preferredTransport": "JSONRPC",
  "protocolVersion": "0.3.0",
  "additionalInterfaces": [
    {"url": "https://127.0.0.1:18443/grpc", "transport": "GRPC"}
  ],
  "provider": {"organization": "Tencent", "url": "https://agent.example.com"},
  "documentationUrl": "https://agent.example.com/docs",
  "iconUrl": "https://agent.example.com/icon.png",
  "capabilities": {
    "streaming": true,
    "pushNotifications": true,
    "stateTransitionHistory": true
  },
  "securitySchemes": {
    "bearer": {"type": "http", "scheme": "bearer"}
  },
  "security": [
    {"bearer": ["read"]}
  ],
  "defaultInputModes": ["text/plain"],
  "defaultOutputModes": ["text/plain"],
  "skills": [
    {
      "id": "answer",
      "name": "answer",
      "description": "answer questions",
      "tags": ["support"],
      "version": "1.2.3",
      "security": [{"bearer": ["read"]}]
    }
  ],
  "supportsAuthenticatedExtendedCard": true,
  "signatures": [
    {"header": {"kid": "key-1"}, "protected": "eyJhbGciOiJFUzI1NiJ9", "signature": "c2lnbmF0dXJl"}
  ],
  "x-vendor-field": "legacy"
}`

func TestParseAgentCardV10Full(t *testing.T) {
	card, err := ParseAgentCardByVersion(fullAgentCardV10, AgentCardSchemaVersion10)
	require.NoError(t, err)
	assert.Equal(t, AgentCardSchemaVersion10, card.SchemaVersion)
	assert.Equal(t, "customer-agent", card.Name)
	assert.Equal(t, "1.0.0", card.Version)
	assert.Equal(t, "https://agent.example.com", card.Provider.URL)
	assert.Equal(t, "https://agent.example.com/docs", card.DocumentationURL)
	assert.Equal(t, "https://agent.example.com/icon.png", card.IconURL)
	assert.True(t, card.Capabilities.Streaming)
	assert.True(t, card.Capabilities.PushNotifications)
	assert.True(t, card.Capabilities.ExtendedAgentCard)
	require.Len(t, card.Capabilities.Extensions, 1)
	assert.Equal(t, "https://example.com/ext", card.Capabilities.Extensions[0].URI)
	assert.JSONEq(t, `{"mode":"fast"}`, string(card.Capabilities.Extensions[0].Params))
	require.Len(t, card.SupportedInterfaces, 2)
	assert.Equal(t, "JSONRPC", card.SupportedInterfaces[0].ProtocolBinding)
	assert.Equal(t, "GRPC", card.SupportedInterfaces[1].ProtocolBinding)
	selected, ok := card.SelectInterface("grpc")
	require.True(t, ok)
	assert.Equal(t, "https://127.0.0.1:18443/grpc", selected.URL)
	host, err := card.Host()
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	require.Len(t, card.Skills, 1)
	assert.Equal(t, "answer", card.Skills[0].ID)
	assert.JSONEq(t, `[{"schemes":{"bearer":{"list":["read"]}}}]`, string(card.Skills[0].SecurityRequirements))
	require.Len(t, card.Signatures, 1)
	assert.Equal(t, "c2lnbmF0dXJl", card.Signatures[0].Signature)
	assert.JSONEq(t, `{"enabled":true}`, string(card.UnknownFields["x-vendor-field"]))
	assert.Equal(t, fullAgentCardV10, card.RawJSON)
	marshaled, err := MarshalAgentCard(card)
	require.NoError(t, err)
	assert.Equal(t, fullAgentCardV10, string(marshaled))
}

func TestParseAgentCardV03Full(t *testing.T) {
	card, err := ParseAgentCardByVersion(fullAgentCardV03, AgentCardSchemaVersion03)
	require.NoError(t, err)
	assert.Equal(t, AgentCardSchemaVersion03, card.SchemaVersion)
	assert.Equal(t, "legacy-agent", card.Name)
	assert.Equal(t, "2.0.0", card.Version)
	assert.Equal(t, "JSONRPC", card.PreferredTransport)
	assert.Equal(t, "0.3.0", card.ProtocolVersion)
	assert.Equal(t, "http://127.0.0.1:18080/a2a", card.URL)
	require.Len(t, card.SupportedInterfaces, 2)
	assert.Equal(t, "JSONRPC", card.SupportedInterfaces[0].ProtocolBinding)
	assert.Equal(t, "GRPC", card.SupportedInterfaces[1].ProtocolBinding)
	assert.Equal(t, "GRPC", card.SupportedInterfaces[1].Transport)
	assert.True(t, card.Capabilities.StateTransitionHistory)
	assert.True(t, card.Capabilities.ExtendedAgentCard)
	assert.True(t, card.SupportsAuthenticatedExtendedCard)
	require.Len(t, card.Skills, 1)
	assert.Equal(t, "1.2.3", card.Skills[0].Version)
	assert.JSONEq(t, `[{"bearer":["read"]}]`, string(card.Skills[0].Security))
	assert.JSONEq(t, `[{"bearer":["read"]}]`, string(card.Security))
	assert.JSONEq(t, `"legacy"`, string(card.UnknownFields["x-vendor-field"]))
	host, err := card.Host()
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	assert.Equal(t, fullAgentCardV03, card.RawJSON)
	marshaled, err := MarshalAgentCard(card)
	require.NoError(t, err)
	assert.Equal(t, fullAgentCardV03, string(marshaled))
}

func TestParseAgentCardRejectsCrossVersionShape(t *testing.T) {
	tests := []struct {
		name    string
		version string
		card    string
	}{
		{
			name:    "v03_with_supported_interfaces",
			version: AgentCardSchemaVersion03,
			card:    `{"name":"a","version":"1","url":"http://127.0.0.1:1/a","supportedInterfaces":[]}`,
		},
		{
			name:    "v10_with_url",
			version: AgentCardSchemaVersion10,
			card:    `{"name":"a","version":"1","url":"http://127.0.0.1:1/a","supportedInterfaces":[{"url":"http://127.0.0.1:1/a","protocolBinding":"JSONRPC"}]}`,
		},
		{
			name:    "v10_with_protocol_version",
			version: AgentCardSchemaVersion10,
			card:    `{"name":"a","version":"1","protocolVersion":"1.0.0","supportedInterfaces":[{"url":"http://127.0.0.1:1/a","protocolBinding":"JSONRPC"}]}`,
		},
		{
			name:    "v10_with_transport",
			version: AgentCardSchemaVersion10,
			card:    `{"name":"a","version":"1","supportedInterfaces":[{"url":"http://127.0.0.1:1/a","transport":"JSONRPC"}]}`,
		},
		{
			name:    "v03_with_protocol_binding",
			version: AgentCardSchemaVersion03,
			card:    `{"name":"a","version":"1","url":"http://127.0.0.1:1/a","additionalInterfaces":[{"url":"http://127.0.0.1:2/a","protocolBinding":"GRPC"}]}`,
		},
		{
			name:    "v10_with_legacy_security",
			version: AgentCardSchemaVersion10,
			card:    `{"name":"a","version":"1","security":[],"supportedInterfaces":[{"url":"http://127.0.0.1:1/a","protocolBinding":"JSONRPC"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseAgentCardByVersion(tt.card, tt.version)
			require.Error(t, err)
		})
	}
}

func TestParseAgentCardRejectsWrongDeclaredVersion(t *testing.T) {
	_, err := ParseAgentCardByVersion(fullAgentCardV10, AgentCardSchemaVersion03)
	require.Error(t, err)
	_, err = ParseAgentCardByVersion(fullAgentCardV03, AgentCardSchemaVersion10)
	require.Error(t, err)
}

func TestParseAgentCardInfersVersion(t *testing.T) {
	card, err := ParseAgentCard(fullAgentCardV10)
	require.NoError(t, err)
	assert.Equal(t, AgentCardSchemaVersion10, card.SchemaVersion)
	card, err = ParseAgentCard(fullAgentCardV03)
	require.NoError(t, err)
	assert.Equal(t, AgentCardSchemaVersion03, card.SchemaVersion)
	_, err = ParseAgentCard(`{"name":"a","version":"1","url":"http://127.0.0.1:1/a","supportedInterfaces":[{"url":"http://127.0.0.1:1/a","protocolBinding":"JSONRPC"}]}`)
	require.Error(t, err)
}

func TestParseAgentCardInterfaceHostRules(t *testing.T) {
	v03IPv6 := `{
	  "name": "agent",
	  "description": "agent",
	  "version": "1.0.0",
	  "url": "//[::1]:18080/a2a",
	  "additionalInterfaces": [{"url": "https://[0:0:0:0:0:0:0:1]/a2a/grpc", "transport": "GRPC"}]
	}`
	card, err := ParseAgentCardByVersion(v03IPv6, AgentCardSchemaVersion03)
	require.NoError(t, err)
	host, err := card.Host()
	require.NoError(t, err)
	assert.Equal(t, "::1", host)

	v10BareAuthority := `{
	  "name": "agent",
	  "description": "agent",
	  "version": "1.0.0",
	  "supportedInterfaces": [
	    {"url": "127.0.0.1:18080/a2a", "protocolBinding": "JSONRPC"},
	    {"url": "https://127.0.0.1/a2a/grpc", "protocolBinding": "GRPC"}
	  ]
	}`
	card, err = ParseAgentCardByVersion(v10BareAuthority, AgentCardSchemaVersion10)
	require.NoError(t, err)
	host, err = card.Host()
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)

	tests := []struct {
		name string
		url  string
	}{
		{name: "domain", url: "http://agent.example.com:18080/a2a"},
		{name: "unspecified", url: "http://0.0.0.0:18080/a2a"},
		{name: "multicast", url: "http://224.0.0.1:18080/a2a"},
		{name: "ipv6_zone", url: "http://[fe80::1%25eth0]:18080/a2a"},
		{name: "bad_scheme", url: "ftp://127.0.0.1:18080/a2a"},
		{name: "userinfo", url: "http://user:password@127.0.0.1:18080/a2a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeAgentInterfaceHost(tt.url)
			require.Error(t, err)
		})
	}
}

func TestParseAgentCardRejectsCrossIP(t *testing.T) {
	card := `{
	  "name": "agent",
	  "description": "agent",
	  "version": "1.0.0",
	  "supportedInterfaces": [
	    {"url": "http://127.0.0.1:18080/a2a", "protocolBinding": "JSONRPC"},
	    {"url": "http://127.0.0.2:18080/a2a", "protocolBinding": "GRPC"}
	  ]
	}`
	_, err := ParseAgentCardByVersion(card, AgentCardSchemaVersion10)
	require.Error(t, err)
}

func TestParseAgentCardV03ProtocolVersionMatchesSchema(t *testing.T) {
	for _, version := range []string{"0.3", "0.3.0"} {
		card := `{"name":"agent","version":"1","url":"http://127.0.0.1:8080/invoke",` +
			`"protocolVersion":"` + version + `"}`
		parsed, err := ParseAgentCardByVersion(card, AgentCardSchemaVersion03)
		require.NoError(t, err)
		assert.Equal(t, version, parsed.ProtocolVersion)
	}
	for _, version := range []string{"1.0", "1.0.0", "2.0"} {
		card := `{"name":"agent","version":"1","url":"http://127.0.0.1:8080/invoke",` +
			`"protocolVersion":"` + version + `"}`
		_, err := ParseAgentCardByVersion(card, AgentCardSchemaVersion03)
		require.Error(t, err, "0.3 schema must not accept protocol version %q", version)
	}

	card, err := ParseAgentCardByVersion(fullAgentCardV10, AgentCardSchemaVersion10)
	require.NoError(t, err)
	require.Len(t, card.SupportedInterfaces, 2)
	assert.Equal(t, "1.0", card.SupportedInterfaces[0].ProtocolVersion,
		"1.0 interface protocolVersion is independent from the card schema version")
}

func TestParseAgentCardRejectsSurroundingWhitespaceInInterfaces(t *testing.T) {
	tests := []struct {
		name    string
		version string
		card    string
	}{
		{
			name:    "v03_primary_url",
			version: AgentCardSchemaVersion03,
			card: `{"name":"agent","version":"1",` +
				`"url":" http://127.0.0.1:8080/invoke "}`,
		},
		{
			name:    "v03_preferred_transport",
			version: AgentCardSchemaVersion03,
			card: `{"name":"agent","version":"1","url":"http://127.0.0.1:8080/invoke",` +
				`"preferredTransport":" JSONRPC "}`,
		},
		{
			name:    "v03_additional_url",
			version: AgentCardSchemaVersion03,
			card: `{"name":"agent","version":"1","url":"http://127.0.0.1:8080/invoke",` +
				`"additionalInterfaces":[{"url":" http://127.0.0.1:8080/grpc ","transport":"GRPC"}]}`,
		},
		{
			name:    "v03_additional_transport",
			version: AgentCardSchemaVersion03,
			card: `{"name":"agent","version":"1","url":"http://127.0.0.1:8080/invoke",` +
				`"additionalInterfaces":[{"url":"http://127.0.0.1:8080/grpc","transport":" GRPC "}]}`,
		},
		{
			name:    "v10_url",
			version: AgentCardSchemaVersion10,
			card: `{"name":"agent","version":"1",` +
				`"supportedInterfaces":[{"url":" http://127.0.0.1:8080/invoke ","protocolBinding":"JSONRPC"}]}`,
		},
		{
			name:    "v10_binding",
			version: AgentCardSchemaVersion10,
			card: `{"name":"agent","version":"1",` +
				`"supportedInterfaces":[{"url":"http://127.0.0.1:8080/invoke","protocolBinding":" JSONRPC "}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseAgentCardByVersion(test.card, test.version)
			require.Error(t, err)
		})
	}

	cardJSON := `{"name":"agent","version":"1","supportedInterfaces":[` +
		`{"url":"http://127.0.0.1:8080/智能体/invoke","protocolBinding":"JSONRPC"}],` +
		`"signatures":[{"header":{"kid":"key-1"},"protected":"cHJvdGVjdGVk","signature":"c2ln"}]}`
	card, err := ParseAgentCardByVersion(cardJSON, AgentCardSchemaVersion10)
	require.NoError(t, err)
	marshaled, err := MarshalAgentCard(card)
	require.NoError(t, err)
	assert.Equal(t, cardJSON, string(marshaled))
}

func TestParseAgentCardInterfaceArrayBoundaries(t *testing.T) {
	v03 := `{
	  "name": "agent",
	  "description": "agent",
	  "version": "1.0.0",
	  "url": "http://127.0.0.1:18080/a2a",
	  "additionalInterfaces": []
	}`
	card, err := ParseAgentCardByVersion(v03, AgentCardSchemaVersion03)
	require.NoError(t, err)
	assert.Len(t, card.SupportedInterfaces, 1)

	v10 := `{
	  "name": "agent",
	  "description": "agent",
	  "version": "1.0.0",
	  "supportedInterfaces": []
	}`
	_, err = ParseAgentCardByVersion(v10, AgentCardSchemaVersion10)
	require.Error(t, err)
}

func TestParseAgentCardErrorDoesNotEchoCredentials(t *testing.T) {
	card := `{
	  "name": "agent",
	  "description": "agent",
	  "version": "1.0.0",
	  "supportedInterfaces": [
	    {"url": "http://review-user:review-password@127.0.0.1:18080/a2a", "protocolBinding": "JSONRPC"}
	  ]
	}`
	_, err := ParseAgentCardByVersion(card, AgentCardSchemaVersion10)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "review-user")
	assert.NotContains(t, err.Error(), "review-password")
}

func TestMarshalAgentCardProgrammatic(t *testing.T) {
	v10 := &AgentCard{
		SchemaVersion: AgentCardSchemaVersion10,
		Name:          "programmatic",
		Description:   "programmatic card",
		Version:       "1.0.0",
		SupportedInterfaces: []AgentInterface{
			{URL: "http://127.0.0.1:18080/a2a", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		},
		Capabilities:       AgentCapabilities{Streaming: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []polarismodel.AgentSkill{{
			ID: "answer", Name: "answer", Description: "answer", Tags: []string{"support"},
		}},
	}
	data, err := MarshalAgentCard(v10)
	require.NoError(t, err)
	parsed, err := ParseAgentCardByVersion(string(data), AgentCardSchemaVersion10)
	require.NoError(t, err)
	assert.Equal(t, "programmatic", parsed.Name)
	host, err := parsed.Host()
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)

	v03 := &AgentCard{
		SchemaVersion:      AgentCardSchemaVersion03,
		Name:               "programmatic",
		Description:        "programmatic card",
		Version:            "1.0.0",
		URL:                "http://127.0.0.1:18080/a2a",
		PreferredTransport: "JSONRPC",
		ProtocolVersion:    "0.3.0",
		AdditionalInterfaces: []AgentInterface{
			{URL: "http://127.0.0.1:18081/grpc", ProtocolBinding: "GRPC"},
		},
		Capabilities:       AgentCapabilities{Streaming: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []polarismodel.AgentSkill{{
			ID: "answer", Name: "answer", Description: "answer", Tags: []string{"support"},
		}},
	}
	data, err = MarshalAgentCard(v03)
	require.NoError(t, err)
	parsed, err = ParseAgentCardByVersion(string(data), AgentCardSchemaVersion03)
	require.NoError(t, err)
	assert.Equal(t, AgentCardSchemaVersion03, parsed.SchemaVersion)
	assert.Len(t, parsed.SupportedInterfaces, 2)

	v03Default := &AgentCard{
		SchemaVersion:      AgentCardSchemaVersion03,
		Name:               "programmatic",
		Description:        "programmatic card",
		Version:            "1.0.0",
		URL:                "http://127.0.0.1:18080/a2a",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
	}
	data, err = MarshalAgentCard(v03Default)
	require.NoError(t, err)
	parsed, err = ParseAgentCardByVersion(string(data), AgentCardSchemaVersion03)
	require.NoError(t, err)
	assert.Equal(t, defaultAgentInterfaceBinding, parsed.SupportedInterfaces[0].ProtocolBinding)
}

func TestNormalizeAgentCardSchemaVersion(t *testing.T) {
	for _, value := range []string{"0.3", "0.3.0"} {
		actual, err := NormalizeAgentCardSchemaVersion(value)
		require.NoError(t, err)
		assert.Equal(t, AgentCardSchemaVersion03, actual)
	}
	for _, value := range []string{"1.0", "1.0.0"} {
		actual, err := NormalizeAgentCardSchemaVersion(value)
		require.NoError(t, err)
		assert.Equal(t, AgentCardSchemaVersion10, actual)
	}
	for _, value := range []string{"", "0.2", "2.0", "v1"} {
		_, err := NormalizeAgentCardSchemaVersion(value)
		require.Error(t, err)
	}
}

func TestParseAgentCardRejectsTrailingJSON(t *testing.T) {
	_, err := ParseAgentCardByVersion(fullAgentCardV10+" trailing", AgentCardSchemaVersion10)
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "trailing"))
}

func TestMarshalAgentCardPreservesUnknownFields(t *testing.T) {
	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(fullAgentCardV10), &parsed))
	assert.Contains(t, parsed, "x-vendor-field")
}
