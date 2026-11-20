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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoveredCardPreservesSupportedFields(t *testing.T) {
	for _, version := range []SchemaVersion{SchemaVersionV03, SchemaVersionV10} {
		t.Run(string(version), func(t *testing.T) {
			cfg := providerConfig()
			cfg.A2AVersion = string(version)
			endpoint := `"supportedInterfaces":[{"url":"http://127.0.0.1:18080/a2a","protocolBinding":"JSONRPC","protocolVersion":"1.0"}]`
			if version == SchemaVersionV03 {
				endpoint = `"url":"http://127.0.0.1:18080/a2a","preferredTransport":"JSONRPC","protocolVersion":"0.3"`
			}
			raw := []byte(`{"name":"customer-agent","description":"Customer support agent","version":"1.2.3",` + endpoint + `,
				"capabilities":{"streaming":true,"pushNotifications":true},
				"defaultInputModes":["text"],"defaultOutputModes":["text"],
				"provider":{"organization":"Example","url":"https://example.com"},
				"securitySchemes":{"bearer":{"type":"http","scheme":"bearer"}},
				"skills":[{"id":"lookup","name":"Lookup","tags":["lookup"]}],
				"signatures":[{"protected":"header","signature":"signed"}],"x-vendor":"unchanged"}`)
			document, err := ParseCardDocument(cfg, version, raw)
			require.NoError(t, err)
			metadata, err := document.Metadata(cfg)
			require.NoError(t, err)
			parsed, discovered, err := ParseAgentInstanceMetadata(metadata)
			require.NoError(t, err)
			require.Equal(t, document.Card.Capabilities, discovered.Capabilities)
			require.Equal(t, document.Card.Skills, discovered.Skills)
			require.Equal(t, document.Card.SecuritySchemes, discovered.SecuritySchemes)
			require.Equal(t, document.Card.Provider, discovered.Provider)
			require.Equal(t, document.Card.Signatures, discovered.Signatures)
			require.Equal(t, document.Card.DefaultInputModes, discovered.DefaultInputModes)
			require.Equal(t, document.Card.DefaultOutputModes, discovered.DefaultOutputModes)
			require.Equal(t, metadata[MetadataAgentCard], parsed.CardJSON)
			require.JSONEq(t, string(raw), parsed.CardJSON)
			if version == SchemaVersionV03 {
				require.Equal(t, "0.3", discovered.SupportedInterfaces[0].ProtocolVersion)
				_, err = SelectConsumableInterface(discovered, version, "JSONRPC")
				require.Error(t, err)
			}
		})
	}
}

func TestDiscoveredInterfaceVersionValidation(t *testing.T) {
	for _, test := range []struct {
		version string
		valid   bool
	}{
		{"x.0", false}, {"1.x", false}, {"x.0.1", false}, {"1.x.1", false},
		{"1.0.x", false}, {"1.0.-1", false}, {"1", false}, {"1.0.0.0", false},
		{"0.3", false}, {"2.0", false}, {"1.1", false},
		{"1.0", true}, {"1.0.0", true}, {"1.0.7", true}, {"", true},
	} {
		t.Run(test.version, func(t *testing.T) {
			raw := `{"name":"customer-agent","version":"1.2.3","supportedInterfaces":[{"url":"http://127.0.0.1:18080/a2a","protocolBinding":"JSONRPC","protocolVersion":"` + test.version + `"}]}`
			metadata := map[string]string{
				MetadataAgentName: "customer-agent", MetadataAgentCard: raw, MetadataA2AVersion: "1.0",
			}
			_, card, err := ParseAgentInstanceMetadata(metadata)
			require.NoError(t, err)
			require.NotPanics(t, func() {
				_, err = SelectConsumableInterface(card, SchemaVersionV10, "JSONRPC")
			})
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
