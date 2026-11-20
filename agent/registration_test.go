/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	namingtest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil/naming"
	trpcregistry "trpc.group/trpc-go/trpc-go/naming/registry"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

type registrationRecorder struct {
	api.ProviderAPI
	requests    []*api.InstanceRegisterRequest
	deregisters []*api.InstanceDeRegisterRequest
	err         error
}

func (p *registrationRecorder) Register(req *api.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return nil, p.err
	}
	return &model.InstanceRegisterResponse{InstanceID: "agent-instance"}, nil
}

func (p *registrationRecorder) Deregister(req *api.InstanceDeRegisterRequest) error {
	p.deregisters = append(p.deregisters, req)
	return nil
}

func rawRegistrationCard(service, version, host, schema string) []byte {
	if schema == "0.3" {
		return []byte(fmt.Sprintf(`{
  "name":%q,"version":%q,"url":"http://%s:8080/a2a",
  "protocolVersion":"0.3.0","preferredTransport":"JSONRPC",
  "additionalInterfaces":[{"url":"http://%s:8443/a2a","transport":"HTTP+JSON"}],
  "x-vendor":{"untouched":true},"signatures":[{"protected":"p","signature":"s"}]
}`, service, version, host, host))
	}
	return []byte(fmt.Sprintf(`{
  "name":%q,"version":%q,"supportedInterfaces":[
    {"url":"http://%s:8080/a2a","protocolBinding":"JSONRPC"},
    {"url":"http://%s:8443/a2a","protocolBinding":"HTTP+JSON"}],
  "x-vendor":{"untouched":true},"signatures":[{"protected":"p","signature":"s"}]
}`, service, version, host, host))
}

func TestRuntimeRegistersRawCardThroughGenericNaming(t *testing.T) {
	for _, schema := range []string{"0.3", "1.0"} {
		t.Run(schema, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Namespace, cfg.ServiceName = "Production", t.Name()
			cfg.A2AVersion = schema
			raw := rawRegistrationCard(cfg.ServiceName, "v1", "127.0.0.1", schema)
			runtime, err := New(WithConfig(cfg), WithAgentCardJSON(schema, raw))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
			require.NoError(t, runtime.Prepare(context.Background()))
			snapshot, err := runtime.RegistrationSnapshot()
			require.NoError(t, err)
			require.Equal(t, "127.0.0.1", snapshot.Host)
			require.Equal(t, 1, snapshot.RegistrationPort)
			require.True(t, snapshot.SetProtocol)
			require.Empty(t, snapshot.Protocol)
			require.Equal(t, string(raw), snapshot.Metadata[agentcard.MetadataAgentCard])
			p := &registrationRecorder{}
			registry, err := polarisregistry.NewRegistry(p, &polarisregistry.Config{
				Namespace: cfg.Namespace, ServiceName: cfg.ServiceName,
				EnableRegister: true, DisableHealthCheck: true,
			})
			require.NoError(t, err)
			require.NoError(t, registry.Register("", trpcregistry.WithAddress("127.0.0.2:9999")))
			require.Len(t, p.requests, 1)
			req := p.requests[0]
			require.Equal(t, snapshot.Host, req.Host)
			require.Equal(t, 1, req.Port)
			require.NotNil(t, req.Protocol)
			require.Empty(t, *req.Protocol)
			require.Equal(t, "v1", *req.Version)
			require.Equal(t, snapshot.Metadata, req.Metadata)
			require.NoError(t, req.Validate())
			require.NoError(t, runtime.Close(context.Background()))
			require.Empty(t, p.deregisters, "runtime does not own instance deregistration")
			require.NoError(t, registry.Deregister(""))
			require.Len(t, p.deregisters, 1)
		})
	}
}

func TestRuntimeStaticMetadataUsesGenericNaming(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Namespace, cfg.ServiceName = "Production", t.Name()
	raw := string(rawRegistrationCard(cfg.ServiceName, "v2", "127.0.0.1", "1.0"))
	metadata := map[string]string{
		agentcard.MetadataAgentName:  cfg.ServiceName,
		agentcard.MetadataAgentCard:  raw,
		agentcard.MetadataA2AVersion: "1.0",
	}
	runtime, err := New(WithConfig(cfg), WithAgentInstanceMetadata("", metadata))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	for key := range metadata {
		metadata[key] = "changed"
	}
	require.NoError(t, runtime.Prepare(context.Background()))
	p := &registrationRecorder{}
	reg, err := polarisregistry.NewRegistry(p, &polarisregistry.Config{Namespace: cfg.Namespace,
		ServiceName: cfg.ServiceName, EnableRegister: true, DisableHealthCheck: true})
	require.NoError(t, err)
	require.NoError(t, reg.Register(""))
	require.Len(t, p.requests, 1)
	require.Len(t, p.requests[0].Metadata, 3)
	require.Equal(t, raw, p.requests[0].Metadata[agentcard.MetadataAgentCard])
	require.Equal(t, "v2", *p.requests[0].Version)
	require.Equal(t, 1, p.requests[0].Port)
	require.NoError(t, reg.Deregister(""))
}

func TestRuntimeStaticMetadataRejectsAmbiguousSources(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Namespace, cfg.ServiceName = "Production", t.Name()
	raw := string(rawRegistrationCard(cfg.ServiceName, "v1", "127.0.0.1", "1.0"))
	metadata := map[string]string{
		agentcard.MetadataAgentName:  cfg.ServiceName,
		agentcard.MetadataAgentCard:  raw,
		agentcard.MetadataA2AVersion: "1.0",
	}
	_, err := New(WithConfig(cfg), WithAgentInstanceMetadata("1.0", metadata), WithAgentCardJSON("1.0", []byte(raw)))
	require.ErrorContains(t, err, "another AgentCard source")
}

func TestRuntimeRejectsFrameworkConflictsBeforePublishingProvider(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*polarisregistry.Config)
		want   string
	}{
		{"endpoints", func(c *polarisregistry.Config) { c.Endpoints = []polarisregistry.Endpoint{{Address: "127.0.0.1:8080"}} }, "does not support endpoints"},
		{"bind", func(c *polarisregistry.Config) { c.BindAddress = "127.0.0.1:8080" }, "bind_address"},
		{"prefer bind", func(c *polarisregistry.Config) { c.PreferBindAddress = true }, "prefer_bind_address"},
		{"protocol", func(c *polarisregistry.Config) { c.InstanceProtocol = "http" }, "instance_protocol"},
		{"version", func(c *polarisregistry.Config) { c.Version = "other" }, "framework instance version"},
		{"namespace", func(c *polarisregistry.Config) { c.Namespace = "other" }, "framework namespace"},
		{"reserved", func(c *polarisregistry.Config) { c.Metadata = map[string]string{agentcard.MetadataAgentName: "static"} }, "reserved agent key"},
		{"mixed MCP", func(c *polarisregistry.Config) {
			c.Metadata = map[string]string{model.MCPMetadataKeyServerName: "static"}
		}, "reserved agent key"},
		{"reserved a2a namespace", func(c *polarisregistry.Config) {
			c.Metadata = map[string]string{"a2a.custom": "value"}
		}, "reserved agent key"},
		{"extra metadata", func(c *polarisregistry.Config) { c.Metadata = map[string]string{"extra": "value"} }, "exactly three"},
		{"heartbeat only", func(c *polarisregistry.Config) { c.EnableRegister = false }, "enabled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testutil.ProviderConfig()
			cfg.ServiceName, cfg.AgentName = t.Name(), t.Name()
			registryCfg := &polarisregistry.Config{Namespace: cfg.Namespace, ServiceName: cfg.ServiceName, EnableRegister: true, DisableHealthCheck: true}
			tt.mutate(registryCfg)
			p := &registrationRecorder{}
			reg, err := polarisregistry.NewRegistry(p, registryCfg)
			require.NoError(t, err)
			trpcregistry.Register(cfg.ServiceName, reg)
			registrar := namingtest.NewFakeMetadataRegistrar()
			runtime, err := New(WithConfig(cfg), WithMetadataRegistrar(registrar))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
			require.ErrorContains(t, runtime.Prepare(context.Background()), tt.want)
			_, ok := registrar.Provider(cfg.ServiceName)
			require.False(t, ok)
			require.Empty(t, p.requests)
		})
	}
}

func TestRuntimeProviderValidatesRegistryCreatedAfterPrepare(t *testing.T) {
	cfg := testutil.ProviderConfig()
	cfg.ServiceName, cfg.AgentName = t.Name(), t.Name()
	runtime, err := New(WithConfig(cfg))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	require.NoError(t, runtime.Prepare(context.Background()))
	p := &registrationRecorder{}
	reg, err := polarisregistry.NewRegistry(p, &polarisregistry.Config{
		Namespace: cfg.Namespace, ServiceName: cfg.ServiceName, EnableRegister: true,
		Metadata: map[string]string{agentcard.MetadataA2AVersion: "1.0"},
	})
	require.NoError(t, err)
	require.ErrorContains(t, reg.Register(""), "reserved agent key")
	require.Empty(t, p.requests)
}

func TestRuntimeInvalidUpdateKeepsCompleteSnapshot(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Namespace, cfg.ServiceName = "Production", t.Name()
	raw := rawRegistrationCard(cfg.ServiceName, "v1", "127.0.0.1", "1.0")
	runtime, err := New(WithConfig(cfg), WithAgentCardJSON("1.0", raw))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	require.NoError(t, runtime.Prepare(context.Background()))
	before, err := runtime.RegistrationSnapshot()
	require.NoError(t, err)
	var card map[string]any
	require.NoError(t, json.Unmarshal(raw, &card))
	card["supportedInterfaces"].([]any)[1].(map[string]any)["url"] = "http://127.0.0.2:8443/a2a"
	bad, err := json.Marshal(card)
	require.NoError(t, err)
	require.ErrorContains(t, runtime.UpdateAgentCardJSON(context.Background(), "1.0", bad), "same IP")
	after, err := runtime.RegistrationSnapshot()
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestFrozenRegistrationProviderAtomicConcurrentUpdates(t *testing.T) {
	provider := newFrozenMetadataProvider("Production", t.Name(), map[string]string{"generation": "0", "host": "10.0.0.1", "port": "1"}, "10.0.0.1", "0", 1)
	var failed atomic.Bool
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for index := 0; index < 500; index++ {
				generation := fmt.Sprint(worker*500 + index)
				host, port := fmt.Sprintf("10.0.0.%d", worker+1), index+1
				provider.update(map[string]string{"generation": generation, "host": host, "port": fmt.Sprint(port)}, host, generation, port)
				snapshot, err := provider.Snapshot()
				if err != nil || snapshot.Version != snapshot.Metadata["generation"] || snapshot.Host != snapshot.Metadata["host"] || fmt.Sprint(snapshot.RegistrationPort) != snapshot.Metadata["port"] || !snapshot.SetProtocol || snapshot.Protocol != "" {
					failed.Store(true)
				}
				snapshot.Metadata["generation"] = "mutated"
			}
		}(worker)
	}
	wg.Wait()
	require.False(t, failed.Load())
}

func TestRuntimeUpdatesAndCloseShareSnapshotLifecycle(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Namespace, cfg.ServiceName = "Production", t.Name()
	runtime, err := New(WithConfig(cfg), WithAgentCardJSON("1.0", rawRegistrationCard(cfg.ServiceName, "initial", "127.0.0.1", "1.0")))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	require.NoError(t, runtime.Prepare(context.Background()))
	var failed atomic.Bool
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for index := 0; index < 25; index++ {
				version, host := fmt.Sprintf("v-%d-%d", worker, index), fmt.Sprintf("127.0.0.%d", worker+1)
				if err := runtime.UpdateAgentCardJSON(context.Background(), "1.0", rawRegistrationCard(cfg.ServiceName, version, host, "1.0")); err != nil {
					failed.Store(true)
					return
				}
				snapshot, err := runtime.RegistrationSnapshot()
				if err != nil {
					failed.Store(true)
					return
				}
				card, err := agentcard.PrepareAgentCard(snapshot.Metadata[agentcard.MetadataAgentCard], snapshot.Metadata[agentcard.MetadataA2AVersion])
				if err != nil || card.Host != snapshot.Host || card.Card.Version != snapshot.Version || snapshot.RegistrationPort != 1 || !snapshot.SetProtocol || snapshot.Protocol != "" {
					failed.Store(true)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	require.False(t, failed.Load(), "host, card and version must be from the same update")
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		err := runtime.UpdateAgentCardJSON(context.Background(), "1.0", rawRegistrationCard(cfg.ServiceName, "last", "127.0.0.1", "1.0"))
		if err != nil && !errors.Is(err, ErrClosed) {
			failed.Store(true)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if err := runtime.Close(context.Background()); err != nil {
			failed.Store(true)
		}
	}()
	close(start)
	wg.Wait()
	require.False(t, failed.Load())
	_, err = runtime.RegistrationSnapshot()
	require.ErrorIs(t, err, ErrClosed)
	require.ErrorIs(t, runtime.UpdateAgentCardJSON(context.Background(), "1.0", rawRegistrationCard(cfg.ServiceName, "after-close", "127.0.0.1", "1.0")), ErrClosed)
}

func TestRuntimeCancelledRegistrationKeepsNamingAsCleanupOwner(t *testing.T) {
	cfg := testutil.ProviderConfig()
	cfg.ServiceName, cfg.AgentName = t.Name(), t.Name()
	runtime, err := New(WithConfig(cfg))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	require.NoError(t, runtime.Prepare(context.Background()))
	p := &registrationRecorder{err: context.Canceled}
	reg, err := polarisregistry.NewRegistry(p, &polarisregistry.Config{Namespace: cfg.Namespace, ServiceName: cfg.ServiceName,
		EnableRegister: true, InstanceID: "owned-id"})
	require.NoError(t, err)
	require.ErrorIs(t, reg.Register(""), context.Canceled)
	require.Len(t, p.requests, 1)
	require.Len(t, p.deregisters, 1)
	require.Equal(t, "owned-id", p.deregisters[0].InstanceID)
	require.NoError(t, runtime.Close(context.Background()))
	require.Len(t, p.deregisters, 1)
}
