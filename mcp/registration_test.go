/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	namingtest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil/naming"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

type instanceRecorder struct {
	api.ProviderAPI
	requests    []*api.InstanceRegisterRequest
	deregisters int
	failAt      int
}

func (p *instanceRecorder) Register(req *api.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	p.requests = append(p.requests, req)
	if p.failAt == len(p.requests) {
		return nil, errors.New("registration failed")
	}
	return &model.InstanceRegisterResponse{InstanceID: req.Host + ":" + fmt.Sprint(req.Port)}, nil
}

func (p *instanceRecorder) Deregister(*api.InstanceDeRegisterRequest) error {
	p.deregisters++
	return nil
}

func TestMCPMetadataRejectsA2AReservedKeysInExtension(t *testing.T) {
	cfg := testConfig()
	for _, key := range []string{
		agentcard.MetadataAgentName,
		agentcard.MetadataAgentCard,
		agentcard.MetadataA2AVersion,
		"a2a.custom",
	} {
		metadata, err := cfg.BuildServerMetadata()
		require.NoError(t, err)
		metadata[key] = "value"
		require.ErrorContains(t, cfg.ValidateServer(metadata), "A2A reserved key")
		prepared, err := PrepareServer(context.Background(), cfg)
		require.NoError(t, err)
		prepared.Metadata = metadata
		registrar := &fakeRegistrar{}
		registered, err := prepared.RegisterMetadata(registrar)
		require.Error(t, err)
		require.False(t, registered)
		require.Zero(t, registrar.count)
	}
}

func TestMCPProviderEmitsCompleteFrozenSnapshot(t *testing.T) {
	cfg := testConfig()
	prepared, err := PrepareServer(context.Background(), cfg)
	require.NoError(t, err)
	registrar := namingtest.NewFakeMetadataRegistrar()
	registered, err := prepared.RegisterMetadata(registrar)
	require.NoError(t, err)
	require.True(t, registered)
	provider, ok := registrar.Provider(cfg.Service)
	require.True(t, ok)
	snapshotProvider, ok := provider.(polarisregistry.RegistrationSnapshotProvider)
	require.True(t, ok)
	snapshot, err := snapshotProvider.Snapshot()
	require.NoError(t, err)
	require.Equal(t, cfg.Host, snapshot.Host)
	require.Equal(t, cfg.Port, snapshot.RegistrationPort)
	require.Equal(t, cfg.ServerVersion, snapshot.Version)
	require.Equal(t, string(cfg.Transport), snapshot.Protocol)
	require.True(t, snapshot.SetProtocol)
	prepared.Config.Host = "127.0.0.2"
	prepared.Metadata[model.MCPMetadataKeyServerName] = "changed"
	snapshot.Metadata[model.MCPMetadataKeyServerName] = "changed"
	again, err := snapshotProvider.Snapshot()
	require.NoError(t, err)
	require.Equal(t, cfg.Host, again.Host)
	require.Equal(t, cfg.Service, again.Metadata[model.MCPMetadataKeyServerName])
}

func TestMCPGenericRegistryRequestsForBothTransports(t *testing.T) {
	for _, transport := range []Transport{TransportStreamableHTTP, TransportSSE} {
		t.Run(string(transport), func(t *testing.T) {
			cfg := testConfig()
			service := strings.ReplaceAll(t.Name(), "/", "-")
			cfg.Service, cfg.ServerName, cfg.Transport = service, service, transport
			prepared, err := PrepareServer(context.Background(), cfg)
			require.NoError(t, err)
			provider := &metadataProvider{config: cfg, metadata: prepared.Metadata}
			require.NoError(t, polarisregistry.RegisterMetadataProvider(cfg.Service, provider))
			t.Cleanup(func() { polarisregistry.UnregisterMetadataProvider(cfg.Service) })
			p := &instanceRecorder{}
			reg, err := polarisregistry.NewRegistry(p, &polarisregistry.Config{
				Namespace: cfg.Namespace, ServiceName: cfg.Service, EnableRegister: true,
				DisableHealthCheck: true, InstanceProtocol: "trpc", Metadata: map[string]string{"env": "test"},
			})
			require.NoError(t, err)
			require.NoError(t, reg.Register(""))
			require.Len(t, p.requests, 1)
			req := p.requests[0]
			require.Equal(t, cfg.Host, req.Host)
			require.Equal(t, cfg.Port, req.Port)
			require.Equal(t, string(transport), *req.Protocol)
			require.Equal(t, cfg.ServerVersion, *req.Version)
			require.Equal(t, cfg.Service, req.Metadata[model.MCPMetadataKeyServerName])
			require.Equal(t, cfg.effectiveEndpointPath(), req.Metadata[model.MCPMetadataKeyEndpointPath])
			require.Equal(t, "test", req.Metadata["env"])
			require.NoError(t, reg.Deregister(""))
			require.Equal(t, 1, p.deregisters)
		})
	}
}

func TestMCPGenericRegistryRejectsStaticAndMixedKeysBeforeRPC(t *testing.T) {
	for _, key := range []string{model.MCPMetadataKeyEndpointPath, agentcard.MetadataAgentName} {
		cfg := testConfig()
		cfg.Service, cfg.ServerName = t.Name()+key, t.Name()+key
		prepared, err := PrepareServer(context.Background(), cfg)
		require.NoError(t, err)
		require.NoError(t, polarisregistry.RegisterMetadataProvider(cfg.Service, &metadataProvider{config: cfg, metadata: prepared.Metadata}))
		t.Cleanup(func() { polarisregistry.UnregisterMetadataProvider(cfg.Service) })
		p := &instanceRecorder{}
		reg, err := polarisregistry.NewRegistry(p, &polarisregistry.Config{Namespace: cfg.Namespace, ServiceName: cfg.Service,
			EnableRegister: true, Metadata: map[string]string{key: "static"}})
		require.NoError(t, err)
		require.ErrorContains(t, reg.Register(""), "reserved agent key")
		require.Empty(t, p.requests)
	}
}

func TestMCPPreparedMultiTransportRegistryLifecycle(t *testing.T) {
	first, second := testConfig(), testConfig()
	first.Service, first.ServerName = t.Name(), t.Name()
	second.Service, second.ServerName = t.Name(), t.Name()
	second.Transport, second.Port, second.ServerVersion = TransportSSE, first.Port+1, "v2"
	base := polarisregistry.Config{Namespace: first.Namespace, ServiceName: first.Service, EnableRegister: true,
		DisableHealthCheck: true, Metadata: map[string]string{"env": "test"}}
	cfg, err := PrepareRegistryConfig(base, first, second)
	require.NoError(t, err)
	require.Empty(t, base.Endpoints)
	cfg.Metadata["env"] = "copied"
	require.Equal(t, "test", base.Metadata["env"])
	p := &instanceRecorder{}
	reg, err := polarisregistry.NewRegistry(p, cfg)
	require.NoError(t, err)
	require.NoError(t, reg.Register(""))
	require.Len(t, p.requests, 2)
	for i, expected := range []Config{first, second} {
		req := p.requests[i]
		require.Equal(t, expected.Host, req.Host)
		require.Equal(t, expected.Port, req.Port)
		require.Equal(t, string(expected.Transport), *req.Protocol)
		require.Equal(t, expected.ServerVersion, *req.Version)
		require.Equal(t, expected.effectiveEndpointPath(), req.Metadata[model.MCPMetadataKeyEndpointPath])
		require.Equal(t, first.Service, req.Metadata[model.MCPMetadataKeyServerName])
		require.Equal(t, "copied", req.Metadata["env"])
	}
	require.NoError(t, reg.Deregister(""))
	require.Equal(t, 2, p.deregisters)
	p.failAt = len(p.requests) + 2
	require.ErrorContains(t, reg.Register(""), "registration failed")
	require.Equal(t, 3, p.deregisters, "the first successful endpoint is rolled back by naming")
	require.NoError(t, reg.Deregister(""))
	require.Equal(t, 3, p.deregisters)
}

func TestMCPPrepareRegistryConfigRejectsInvalidTransports(t *testing.T) {
	cfg := testConfig()
	base := polarisregistry.Config{Namespace: cfg.Namespace, ServiceName: cfg.Service, EnableRegister: true}
	_, err := PrepareRegistryConfig(base)
	require.Error(t, err)
	_, err = PrepareRegistryConfig(base, cfg, cfg)
	require.ErrorContains(t, err, "distinct")
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Service = "other" },
		func(c *Config) { c.Namespace = "other" },
		func(c *Config) { c.ProtocolVersion = "unsupported" },
		func(c *Config) { c.Transport, c.Host, c.Port = TransportStdio, "", 0 },
	} {
		other := cfg
		mutate(&other)
		_, err := PrepareRegistryConfig(base, other)
		require.Error(t, err)
	}
	for _, mutate := range []func(*polarisregistry.Config){
		func(c *polarisregistry.Config) { c.Metadata = map[string]string{agentcard.MetadataAgentName: "other"} },
		func(c *polarisregistry.Config) {
			c.Metadata = map[string]string{model.MCPMetadataKeyServerName: cfg.Service}
		},
		func(c *polarisregistry.Config) { c.Endpoints = []polarisregistry.Endpoint{{Address: "127.0.0.1:8080"}} },
		func(c *polarisregistry.Config) { c.BindAddress = "127.0.0.1:8080" },
		func(c *polarisregistry.Config) { c.PreferBindAddress = true },
		func(c *polarisregistry.Config) { c.EnableRegister = false },
	} {
		other := base
		mutate(&other)
		_, err := PrepareRegistryConfig(other, cfg)
		require.Error(t, err)
	}
}

func TestMCPProviderCanonicalIPv6AndPreparedMutation(t *testing.T) {
	cfg := testConfig()
	cfg.Host = "[0:0:0:0:0:0:0:1]"
	prepared, err := PrepareServer(context.Background(), cfg)
	require.NoError(t, err)
	endpoint, err := prepared.RegistrationEndpoint()
	require.NoError(t, err)
	require.Equal(t, "[::1]:"+fmt.Sprint(cfg.Port), endpoint.Address)
	provider := &metadataProvider{config: cfg, metadata: prepared.Metadata}
	snapshot, err := provider.Snapshot()
	require.NoError(t, err)
	require.Equal(t, "::1", snapshot.Host)
	prepared.Config.ProtocolVersion = "unsupported"
	prepared.Network = false
	registered, err := prepared.RegisterMetadata(&fakeRegistrar{})
	require.Error(t, err)
	require.False(t, registered)
}
