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
	"sync"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/registration"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

type frozenMetadataProvider struct {
	mu               sync.RWMutex
	namespace        string
	service          string
	metadata         map[string]string
	host             string
	version          string
	registrationPort int
	protocol         string
}

var _ polarisregistry.RegistrationSnapshotProvider = (*frozenMetadataProvider)(nil)
var _ polarisregistry.RegistrationConfigValidator = (*frozenMetadataProvider)(nil)

func newFrozenMetadataProvider(
	namespace, service string,
	metadata map[string]string,
	host string,
	version string,
	registrationPort int,
) *frozenMetadataProvider {
	copied := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return &frozenMetadataProvider{
		namespace:        namespace,
		service:          service,
		metadata:         copied,
		host:             host,
		version:          version,
		registrationPort: registrationPort,
		protocol:         "",
	}
}

func (p *frozenMetadataProvider) ValidateRegistration(cfg *polarisregistry.Config, snapshot polarisregistry.RegistrationSnapshot) error {
	return registration.ValidateAgentConfig(p.namespace, p.service, snapshot.Version, cfg)
}

func (p *frozenMetadataProvider) Metadata() (map[string]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copied := make(map[string]string, len(p.metadata))
	for key, value := range p.metadata {
		copied[key] = value
	}
	return copied, nil
}

func (p *frozenMetadataProvider) Snapshot() (polarisregistry.RegistrationSnapshot, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copied := make(map[string]string, len(p.metadata))
	for key, value := range p.metadata {
		copied[key] = value
	}
	return polarisregistry.RegistrationSnapshot{
		Metadata:         copied,
		Host:             p.host,
		Version:          p.version,
		RegistrationPort: p.registrationPort,
		Protocol:         p.protocol,
		SetProtocol:      true,
	}, nil
}

func (p *frozenMetadataProvider) update(
	metadata map[string]string,
	host string,
	version string,
	registrationPort int,
) {
	copied := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	p.mu.Lock()
	p.metadata = copied
	p.host = host
	p.version = version
	p.registrationPort = registrationPort
	p.protocol = ""
	p.mu.Unlock()
}
