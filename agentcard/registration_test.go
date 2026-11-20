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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	polarismodel "github.com/polarismesh/polaris-go/pkg/model"
)

func TestStaticRegistrationMetadataUsesCanonicalKeys(t *testing.T) {
	for _, tt := range []struct {
		name, service, schema, card string
	}{
		{"1.0", "customer-agent", "1.0", fullAgentCardV10},
		{"0.3", "legacy-agent", "0.3", fullAgentCardV03},
	} {
		t.Run(tt.name, func(t *testing.T) {
			metadata := map[string]string{MetadataAgentName: tt.service, MetadataAgentCard: tt.card, MetadataA2AVersion: tt.schema}
			snapshot, err := BuildAgentInstanceRegistrationFromMetadata(AgentInstanceRegistrationRequest{
				Namespace: "Production", Service: tt.service,
			}, metadata)
			require.NoError(t, err)
			require.Equal(t, metadata, snapshot.Metadata)
			require.Equal(t, 1, snapshot.Port)
			require.Empty(t, snapshot.Protocol)
			require.Equal(t, tt.card, snapshot.Metadata[MetadataAgentCard])
		})
	}
}

func TestStaticRegistrationMetadataRejectsAmbiguousInputs(t *testing.T) {
	base := map[string]string{MetadataAgentName: "customer-agent", MetadataAgentCard: fullAgentCardV10, MetadataA2AVersion: "1.0"}
	for _, mutate := range []func(map[string]string){
		func(m map[string]string) { delete(m, MetadataA2AVersion) },
		func(m map[string]string) { m[MetadataA2AVersion] = "0.3" },
		func(m map[string]string) { m[MetadataAgentName] = "other" },
		func(m map[string]string) { m["mcp-server-name"] = "mcp" },
		func(m map[string]string) { m["extra"] = "value" },
	} {
		metadata := cloneStringMap(base)
		mutate(metadata)
		_, err := BuildAgentInstanceRegistrationFromMetadata(AgentInstanceRegistrationRequest{Namespace: "Production", Service: "customer-agent"}, metadata)
		require.Error(t, err)
	}
	_, err := BuildAgentInstanceRegistrationFromMetadata(AgentInstanceRegistrationRequest{
		Namespace: "Production", Service: "customer-agent", A2AVersion: "0.3",
	}, base)
	require.Error(t, err)
	_, err = BuildAgentInstanceRegistrationFromMetadata(AgentInstanceRegistrationRequest{
		Namespace: "Production", Service: "customer-agent",
	}, map[string]string{
		MetadataAgentName: "customer-agent", MetadataA2AVersion: AgentCardSchemaVersion10,
	})
	require.ErrorContains(t, err, MetadataAgentCard)
}

func TestAgentRegistrationRejectsCrossIPAndInvalidPorts(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(fullAgentCardV10, "127.0.0.1:18443", "127.0.0.2:18443", 1),
		strings.ReplaceAll(fullAgentCardV10, "127.0.0.1", "example.internal"),
		strings.Replace(fullAgentCardV10, "127.0.0.1:18080", "127.0.0.1:0", 1),
		strings.Replace(fullAgentCardV10, "127.0.0.1:18080", "127.0.0.1:65536", 1),
		strings.Replace(fullAgentCardV10, "127.0.0.1:18080", "127.0.0.1:", 1),
	} {
		_, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
			Namespace: "Production", Service: "customer-agent", A2AVersion: "1.0", CardJSON: raw,
		})
		require.Error(t, err)
	}
}

func TestAgentRegistrationCanonicalIPv6AndCredentials(t *testing.T) {
	raw := `{"name":"ipv6-agent","version":"v1","url":"http://[2001:db8::1]:8080/a2a",` +
		`"protocolVersion":"0.3.0","preferredTransport":"JSONRPC","additionalInterfaces":[` +
		`{"url":"[2001:db8:0:0:0:0:0:1]:8443/second","transport":"HTTP+JSON"}]}`
	snapshot, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
		Namespace: "Production", Service: "ipv6-agent", A2AVersion: "0.3", CardJSON: raw,
	})
	require.NoError(t, err)
	require.Equal(t, "2001:db8::1", snapshot.Host)
	require.Equal(t, raw, snapshot.Metadata[MetadataAgentCard])
	for _, endpoint := range []string{"http://private-account:private-password@bad host/a2a", "http://private-account:private-password@127.0.0.1/a2a"} {
		_, err := NormalizeAgentInterfaceHost(endpoint)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-account")
		require.NotContains(t, err.Error(), "private-password")
		_, _, err = parseInterfaceEndpoint(endpoint)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-password")
	}
}

func TestBuildAgentInstanceRegistrationDefaults(t *testing.T) {
	snapshot, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
		Namespace:  "Production",
		Service:    "customer-agent",
		CardJSON:   fullAgentCardV10,
		A2AVersion: AgentCardSchemaVersion10,
	})
	require.NoError(t, err)
	assert.Equal(t, "Production", snapshot.Namespace)
	assert.Equal(t, "customer-agent", snapshot.Service)
	assert.Equal(t, "127.0.0.1", snapshot.Host)
	assert.Equal(t, DefaultAgentRegistrationPort, snapshot.Port)
	assert.Equal(t, "1.0.0", snapshot.Version)
	assert.Equal(t, "", snapshot.Protocol)
	assert.Equal(t, AgentCardSchemaVersion10, snapshot.A2AVersion)
	assert.Equal(t, map[string]string{
		MetadataAgentName:  "customer-agent",
		MetadataAgentCard:  fullAgentCardV10,
		MetadataA2AVersion: AgentCardSchemaVersion10,
	}, snapshot.Metadata)

	request := snapshot.NewInstanceRegisterRequest()
	require.NotNil(t, request)
	assert.Equal(t, "127.0.0.1", request.Host)
	assert.Equal(t, DefaultAgentRegistrationPort, request.Port)
	require.NotNil(t, request.Protocol)
	assert.Equal(t, "", *request.Protocol)
	require.NotNil(t, request.Version)
	assert.Equal(t, "1.0.0", *request.Version)
	assert.NoError(t, request.Validate())
}

func TestBuildAgentInstanceRegistrationExplicitPort(t *testing.T) {
	port := 18080
	snapshot, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
		Namespace:        "Production",
		Service:          "customer-agent",
		CardJSON:         fullAgentCardV10,
		A2AVersion:       "1.0.0",
		RegistrationPort: &port,
	})
	require.NoError(t, err)
	assert.Equal(t, 18080, snapshot.Port)
	assert.Equal(t, AgentCardSchemaVersion10, snapshot.A2AVersion)
}

func TestBuildAgentInstanceRegistrationRejectsInvalidPort(t *testing.T) {
	for _, value := range []int{0, -1, 65536} {
		port := value
		_, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
			Namespace:        "Production",
			Service:          "customer-agent",
			CardJSON:         fullAgentCardV10,
			A2AVersion:       AgentCardSchemaVersion10,
			RegistrationPort: &port,
		})
		require.Error(t, err, "port %d", value)
	}
}

func TestBuildAgentInstanceRegistrationRejectsNameMismatch(t *testing.T) {
	_, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
		Namespace:  "Production",
		Service:    "other-agent",
		CardJSON:   fullAgentCardV10,
		A2AVersion: AgentCardSchemaVersion10,
	})
	require.Error(t, err)
}

func TestBuildAgentInstanceRegistrationV03(t *testing.T) {
	snapshot, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
		Namespace:  "Production",
		Service:    "legacy-agent",
		CardJSON:   fullAgentCardV03,
		A2AVersion: AgentCardSchemaVersion03,
	})
	require.NoError(t, err)
	assert.Equal(t, AgentCardSchemaVersion03, snapshot.A2AVersion)
	assert.Equal(t, "127.0.0.1", snapshot.Host)
	assert.Equal(t, "2.0.0", snapshot.Version)
	assert.Equal(t, fullAgentCardV03, snapshot.Metadata[MetadataAgentCard])
}

func TestBuildAgentInstanceRegistrationV03RejectsV10ProtocolVersion(t *testing.T) {
	cardJSON := `{"name":"legacy-agent","version":"1","url":"http://127.0.0.1:8080/invoke",` +
		`"protocolVersion":"1.0"}`
	_, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
		Namespace:  "Production",
		Service:    "legacy-agent",
		CardJSON:   cardJSON,
		A2AVersion: AgentCardSchemaVersion03,
	})
	require.Error(t, err)
	assert.Equal(t, polarismodel.ErrCodeAPIInvalidArgument, polarismodel.GetErrorCodeFromError(err))
}

func TestBuildAgentInstanceRegistrationFreezesCard(t *testing.T) {
	prepared, err := PrepareAgentCard(fullAgentCardV10, AgentCardSchemaVersion10)
	require.NoError(t, err)
	snapshot, err := BuildAgentInstanceRegistration(AgentInstanceRegistrationRequest{
		Namespace:  "Production",
		Service:    "customer-agent",
		CardJSON:   fullAgentCardV10,
		A2AVersion: AgentCardSchemaVersion10,
	})
	require.NoError(t, err)
	require.NotSame(t, prepared.Card, snapshot.Card)

	snapshot.Card.Name = "mutated"
	snapshot.Card.SupportedInterfaces[0].URL = "http://mutated/invoke"
	snapshot.Card.Skills[0].Tags[0] = "mutated"
	snapshot.Card.UnknownFields["x-vendor-field"][0] = 'x'

	assert.Equal(t, "customer-agent", prepared.Card.Name)
	assert.Equal(t, "http://127.0.0.1:18080/a2a", prepared.Card.SupportedInterfaces[0].URL)
	assert.Equal(t, "support", prepared.Card.Skills[0].Tags[0])
	assert.JSONEq(t, `{"enabled":true}`, string(prepared.Card.UnknownFields["x-vendor-field"]))
}

func TestInstanceRegisterRequestRejectsNonEmptyA2AProtocol(t *testing.T) {
	protocol := "a2a"
	version := "1.0.0"
	request := &polarismodel.InstanceRegisterRequest{
		Namespace: "Production",
		Service:   "customer-agent",
		Host:      "127.0.0.1",
		Port:      1,
		Protocol:  &protocol,
		Version:   &version,
		Metadata: map[string]string{
			MetadataAgentName:  "customer-agent",
			MetadataAgentCard:  fullAgentCardV10,
			MetadataA2AVersion: AgentCardSchemaVersion10,
		},
	}
	require.Error(t, request.Validate())

	empty := ""
	request.Protocol = &empty
	require.NoError(t, request.Validate())
	request.Protocol = nil
	require.NoError(t, request.Validate())
}
