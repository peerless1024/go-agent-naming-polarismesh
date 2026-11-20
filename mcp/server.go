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
	"net"
	"strconv"
	"strings"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/registration"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

// MetadataRegistrar registers dynamic instance metadata with go-naming.
type MetadataRegistrar interface {
	RegisterMetadataProvider(service string, provider adapternaming.MetadataProvider) error
	UnregisterMetadataProvider(service string)
}

// PreparedServer contains the metadata injected into one MCP registration.
type PreparedServer struct {
	Config   Config
	Metadata map[string]string
	Endpoint string
	Network  bool
}

// PrepareServer builds and validates MCP server metadata.
func PrepareServer(ctx context.Context, config Config) (*PreparedServer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metadata, err := config.BuildServerMetadata()
	if err != nil {
		return nil, err
	}
	if err := config.ValidateServer(metadata); err != nil {
		return nil, err
	}
	endpoint, err := config.Endpoint()
	if err != nil {
		return nil, err
	}
	return &PreparedServer{
		Config:   config,
		Metadata: metadata,
		Endpoint: endpoint,
		Network:  config.Transport != TransportStdio,
	}, nil
}

// RegisterMetadata installs an MCP metadata provider for network transports.
// Stdio is intentionally local-only and returns false without registering.
func (s *PreparedServer) RegisterMetadata(registrar MetadataRegistrar) (bool, error) {
	if s == nil {
		return false, errors.New("prepared MCP server is required")
	}
	if err := s.Config.ValidateServer(s.Metadata); err != nil {
		return false, err
	}
	if s.Config.Transport == TransportStdio {
		return false, nil
	}
	if registrar == nil {
		return false, errors.New("metadata registrar is required")
	}
	if err := registration.ValidateMCP(s.Config.Namespace, s.Config.Service, s.Config.ServerVersion); err != nil {
		return false, err
	}
	metadata := cloneStringMap(s.Metadata)
	provider := &metadataProvider{config: s.Config, metadata: metadata}
	if err := registrar.RegisterMetadataProvider(s.Config.Service, provider); err != nil {
		return false, err
	}
	return true, nil
}

// RegistrationEndpoint returns a validated generic endpoint for static or
// multi-transport registry configuration. Do not install a metadata provider
// for the same service when using these endpoint metadata values.
func (s *PreparedServer) RegistrationEndpoint() (polarisregistry.Endpoint, error) {
	if s == nil {
		return polarisregistry.Endpoint{}, errors.New("prepared MCP server is required")
	}
	if err := s.Config.ValidateServer(s.Metadata); err != nil {
		return polarisregistry.Endpoint{}, err
	}
	if s.Config.Transport == TransportStdio {
		return polarisregistry.Endpoint{}, errors.New("stdio does not have a registration endpoint")
	}
	return polarisregistry.Endpoint{
		Address:  net.JoinHostPort(canonicalHost(s.Config.Host), strconv.Itoa(s.Config.Port)),
		Protocol: string(s.Config.Transport), Version: s.Config.ServerVersion,
		Metadata: cloneStringMap(s.Metadata),
	}, nil
}

// PrepareRegistryConfig prepares network transports as ordinary registry
// endpoints. AI policy is validated here; naming only executes the result.
func PrepareRegistryConfig(base polarisregistry.Config, servers ...Config) (*polarisregistry.Config, error) {
	if len(servers) == 0 {
		return nil, errors.New("at least one network MCP server configuration is required")
	}
	if len(base.Endpoints) != 0 || base.BindAddress != "" || base.PreferBindAddress {
		return nil, errors.New("MCP transports must not be combined with preconfigured endpoints or bind_address")
	}
	if err := registration.ValidateMCPConfig(base.Namespace, base.ServiceName, servers[0].ServerVersion, &base); err != nil {
		return nil, err
	}
	endpoints := make([]polarisregistry.Endpoint, 0, len(servers))
	addresses := make(map[string]bool, len(servers))
	for _, cfg := range servers {
		if cfg.Namespace != base.Namespace || cfg.Service != base.ServiceName {
			return nil, fmt.Errorf("MCP transport namespace and service must match registry configuration")
		}
		if base.Version != "" && base.Version != cfg.ServerVersion {
			return nil, fmt.Errorf("MCP transport version must match framework instance version")
		}
		prepared, err := PrepareServer(context.Background(), cfg)
		if err != nil {
			return nil, err
		}
		endpoint, err := prepared.RegistrationEndpoint()
		if err != nil {
			return nil, err
		}
		if addresses[endpoint.Address] {
			return nil, fmt.Errorf("MCP transports require distinct registration addresses: %s", endpoint.Address)
		}
		addresses[endpoint.Address] = true
		endpoints = append(endpoints, endpoint)
	}
	base.Metadata = cloneStringMap(base.Metadata)
	base.Endpoints = endpoints
	return &base, nil
}

type metadataProvider struct {
	config   Config
	metadata map[string]string
}

var _ polarisregistry.RegistrationSnapshotProvider = (*metadataProvider)(nil)
var _ polarisregistry.RegistrationConfigValidator = (*metadataProvider)(nil)

func (p *metadataProvider) Metadata() (map[string]string, error) {
	return cloneStringMap(p.metadata), nil
}

func (p *metadataProvider) Snapshot() (polarisregistry.RegistrationSnapshot, error) {
	return polarisregistry.RegistrationSnapshot{
		Host:             canonicalHost(p.config.Host),
		RegistrationPort: p.config.Port,
		Version:          p.config.ServerVersion,
		Protocol:         string(p.config.Transport),
		SetProtocol:      true,
		Metadata:         cloneStringMap(p.metadata),
	}, nil
}

func canonicalHost(host string) string {
	return net.ParseIP(strings.Trim(host, "[]")).String()
}

func (p *metadataProvider) ValidateRegistration(cfg *polarisregistry.Config, snapshot polarisregistry.RegistrationSnapshot) error {
	return registration.ValidateMCPConfig(p.config.Namespace, p.config.Service, snapshot.Version, cfg)
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
