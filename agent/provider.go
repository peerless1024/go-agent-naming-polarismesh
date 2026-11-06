/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agent

type frozenMetadataProvider struct {
	metadata map[string]string
}

func newFrozenMetadataProvider(metadata map[string]string) *frozenMetadataProvider {
	copied := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return &frozenMetadataProvider{metadata: copied}
}

func (p *frozenMetadataProvider) Metadata() (map[string]string, error) {
	copied := make(map[string]string, len(p.metadata))
	for key, value := range p.metadata {
		copied[key] = value
	}
	return copied, nil
}
