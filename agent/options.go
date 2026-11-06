/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package agent provides the Polaris AI Registry adapter runtime.
package agent

import (
	"context"

	"github.com/polarismesh/polaris-go/api"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
)

// AgentCardBuilder builds the immutable AgentCard used for registration.
type AgentCardBuilder func(ctx context.Context) (a2aprotocolserver.AgentCard, error)

// Option configures a Runtime.
type Option func(*runtimeOptions)

type runtimeOptions struct {
	config            adapterconfig.Config
	sdkContext        api.SDKContext
	agentAPI          api.AgentAPI
	skillAPI          api.SkillAPI
	metadataRegistrar adapternaming.MetadataRegistrar
	consumerAPI       adapternaming.InstanceLister
	agentCard         *a2aprotocolserver.AgentCard
	agentCardBuilder  AgentCardBuilder
	cardTools         []tool.Tool
}

func defaultRuntimeOptions() *runtimeOptions {
	return &runtimeOptions{config: adapterconfig.DefaultConfig()}
}

// WithConfig replaces the adapter configuration.
func WithConfig(config adapterconfig.Config) Option {
	return func(options *runtimeOptions) {
		options.config = config
	}
}

// WithSDKContext borrows a Polaris SDK context. The runtime does not destroy it.
func WithSDKContext(sdkContext api.SDKContext) Option {
	return func(options *runtimeOptions) {
		options.sdkContext = sdkContext
	}
}

// WithService sets the Polaris namespace and service name.
func WithService(namespace, service string) Option {
	return func(options *runtimeOptions) {
		options.config.Namespace = namespace
		options.config.ServiceName = service
		options.config.AgentName = service
	}
}

// WithAgentCard supplies a fully built A2A v1 AgentCard.
func WithAgentCard(card a2aprotocolserver.AgentCard) Option {
	return func(options *runtimeOptions) {
		options.agentCard = &card
	}
}

// WithAgentCardBuilder supplies a builder for the immutable registration card.
func WithAgentCardBuilder(builder AgentCardBuilder) Option {
	return func(options *runtimeOptions) {
		options.agentCardBuilder = builder
	}
}

// WithCardTools advertises tools as AgentCard skills.
func WithCardTools(tools ...tool.Tool) Option {
	return func(options *runtimeOptions) {
		options.cardTools = append([]tool.Tool(nil), tools...)
	}
}

// WithSkillRefs sets explicitly configured Skill references.
func WithSkillRefs(refs ...adapterconfig.SkillRef) Option {
	return func(options *runtimeOptions) {
		options.config.SkillRefs = append([]adapterconfig.SkillRef(nil), refs...)
	}
}

// WithSkillCacheDir sets the local Skill installation root.
func WithSkillCacheDir(directory string) Option {
	return func(options *runtimeOptions) {
		options.config.SkillCacheDir = directory
	}
}

// WithMountSkills enables replacing service-level Skill references.
func WithMountSkills(enabled bool) Option {
	return func(options *runtimeOptions) {
		options.config.MountSkills = enabled
	}
}

// WithAgentAPI injects a Polaris AgentAPI implementation.
func WithAgentAPI(agentAPI api.AgentAPI) Option {
	return func(options *runtimeOptions) {
		options.agentAPI = agentAPI
	}
}

// WithSkillAPI injects a Polaris SkillAPI implementation.
func WithSkillAPI(skillAPI api.SkillAPI) Option {
	return func(options *runtimeOptions) {
		options.skillAPI = skillAPI
	}
}

// WithMetadataRegistrar injects the dynamic metadata registrar.
func WithMetadataRegistrar(registrar adapternaming.MetadataRegistrar) Option {
	return func(options *runtimeOptions) {
		options.metadataRegistrar = registrar
	}
}

// WithConsumerAPI injects a custom instance lister.
func WithConsumerAPI(consumerAPI adapternaming.InstanceLister) Option {
	return func(options *runtimeOptions) {
		options.consumerAPI = consumerAPI
	}
}

// WithDescription sets the generated AgentCard description.
func WithDescription(description string) Option {
	return func(options *runtimeOptions) {
		options.config.Description = description
	}
}

// WithStreaming sets the generated AgentCard streaming capability.
func WithStreaming(enabled bool) Option {
	return func(options *runtimeOptions) {
		options.config.Streaming = enabled
	}
}

// WithScheme sets the generated AgentCard URL scheme.
func WithScheme(scheme string) Option {
	return func(options *runtimeOptions) {
		options.config.Scheme = scheme
	}
}
