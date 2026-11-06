/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package namingtest provides fakes for metadata registration.
package namingtest

import (
	"errors"
	"sync"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
)

type FakeMetadataRegistrar struct {
	mu              sync.Mutex
	providers       map[string]naming.MetadataProvider
	Err             error
	UnregisterCalls int
}

func NewFakeMetadataRegistrar() *FakeMetadataRegistrar {
	return &FakeMetadataRegistrar{providers: make(map[string]naming.MetadataProvider)}
}

func (r *FakeMetadataRegistrar) RegisterMetadataProvider(
	service string,
	provider naming.MetadataProvider,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	if _, exists := r.providers[service]; exists {
		return errors.New("provider already registered")
	}
	r.providers[service] = provider
	return nil
}

func (r *FakeMetadataRegistrar) UnregisterMetadataProvider(service string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.UnregisterCalls++
	delete(r.providers, service)
}

func (r *FakeMetadataRegistrar) Provider(service string) (naming.MetadataProvider, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	provider, ok := r.providers[service]
	return provider, ok
}
