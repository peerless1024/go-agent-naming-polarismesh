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
	"github.com/polarismesh/polaris-go/api"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

type polarisMetadataRegistrar struct{}

func (polarisMetadataRegistrar) RegisterMetadataProvider(
	service string,
	provider adapternaming.MetadataProvider,
) error {
	return polarisregistry.RegisterMetadataProvider(service, provider)
}

func (polarisMetadataRegistrar) UnregisterMetadataProvider(service string) {
	polarisregistry.UnregisterMetadataProvider(service)
}

// SDKContextFromFramework returns the SDK context owned by the initialized
// go-naming-polarismesh registry for service.
func SDKContextFromFramework(service string) (api.SDKContext, bool) {
	registry, ok := polarisregistry.FromFramework(service)
	if !ok || registry.SDKContext() == nil {
		return nil, false
	}
	return registry.SDKContext(), true
}

// FrameworkInstanceVersion returns the instance version configured in the
// go-naming-polarismesh registry for service.
func FrameworkInstanceVersion(service string) (string, bool) {
	registry, ok := polarisregistry.FromFramework(service)
	if !ok {
		return "", false
	}
	return registry.Version(), true
}
