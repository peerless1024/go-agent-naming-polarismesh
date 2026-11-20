/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package naming adapts Polaris service discovery to A2A agents.
package naming

import (
	"errors"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// MetadataProvider supplies instance metadata at registration time.
type MetadataProvider interface {
	Metadata() (map[string]string, error)
}

// MetadataProviderFunc adapts a function to MetadataProvider.
type MetadataProviderFunc func() (map[string]string, error)

// Metadata implements MetadataProvider.
func (f MetadataProviderFunc) Metadata() (map[string]string, error) {
	if f == nil {
		return nil, errors.New("metadata provider func is nil")
	}
	return f()
}

// MetadataRegistrar installs and removes dynamic metadata providers.
type MetadataRegistrar interface {
	RegisterMetadataProvider(service string, provider MetadataProvider) error
	UnregisterMetadataProvider(service string)
}

// InstanceLister is the subset of ConsumerAPI used by the resolver.
type InstanceLister interface {
	GetInstances(req *api.GetInstancesRequest) (*model.InstancesResponse, error)
}
