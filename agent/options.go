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
	"time"

	"github.com/polarismesh/polaris-go/api"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapteragentapi "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentapi"
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
	agentAPI          adapteragentapi.AgentAPI
	skillAPI          api.SkillAPI
	metadataRegistrar adapternaming.MetadataRegistrar
	consumerAPI       adapternaming.InstanceLister
	agentCard         *a2aprotocolserver.AgentCard
	agentCardBuilder  AgentCardBuilder
	agentCardJSON     []byte
	agentCardVersion  string
	agentMetadata     map[string]string
	metadataVersion   string
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

// WithAgentCardJSON supplies the exact serialized AgentCard. The bytes are
// reused unchanged for instance metadata so unknown fields and signatures are
// preserved.
func WithAgentCardJSON(version string, raw []byte) Option {
	return func(options *runtimeOptions) {
		options.agentCardJSON = append([]byte(nil), raw...)
		options.agentCardVersion = version
	}
}

// WithAgentInstanceMetadata supplies canonical three-key static registration
// metadata. Remove these keys from the naming configuration before installing
// the runtime.
func WithAgentInstanceMetadata(version string, metadata map[string]string) Option {
	return func(options *runtimeOptions) {
		options.agentMetadata = make(map[string]string, len(metadata))
		for key, value := range metadata {
			options.agentMetadata[key] = value
		}
		options.metadataVersion = version
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

// WithAgentAPI injects an AI Agent Service API implementation.
func WithAgentAPI(agentAPI adapteragentapi.AgentAPI) Option {
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

// WithRegistrationPort sets the Polaris instance port used for registration.
func WithRegistrationPort(port int) Option {
	return func(options *runtimeOptions) {
		options.config.RegistrationPort = port
	}
}

// WithA2AVersion sets the AgentCard schema version.
func WithA2AVersion(version string) Option {
	return func(options *runtimeOptions) {
		options.config.A2AVersion = version
	}
}

// WithRegistryEnabled toggles provider-side instance registration metadata.
func WithRegistryEnabled(enabled bool) Option {
	return func(options *runtimeOptions) {
		options.config.RegistryEnabled = enabled
	}
}

// WithRegistryTTL sets the registration TTL exposed to the naming layer.
func WithRegistryTTL(ttl time.Duration) Option {
	return func(options *runtimeOptions) {
		options.config.RegistryTTL = ttl
	}
}

// WithDiscoveryEnabled toggles consumer-side instance discovery.
func WithDiscoveryEnabled(enabled bool) Option {
	return func(options *runtimeOptions) {
		options.config.DiscoveryEnabled = enabled
	}
}

// WithSkillMode selects names, published, or mounted Skill loading.
func WithSkillMode(mode string) Option {
	return func(options *runtimeOptions) {
		options.config.SkillMode = mode
	}
}

// WithSkillLimits sets published Skill pagination and capacity limits.
func WithSkillLimits(listLimit, maxSkills int) Option {
	return func(options *runtimeOptions) {
		options.config.SkillListLimit = listLimit
		options.config.SkillMaxSkills = maxSkills
	}
}

// WithSkillRefresh configures periodic mounted/published Skill refresh.
func WithSkillRefresh(interval time.Duration) Option {
	return func(options *runtimeOptions) {
		options.config.SkillListRefresh = interval
	}
}

// WithSkillFailureThreshold sets how many individual Skill failures are tolerated.
func WithSkillFailureThreshold(threshold int) Option {
	return func(options *runtimeOptions) {
		options.config.SkillFailureThreshold = threshold
	}
}

// WithSkillCacheMaxAge sets the maximum age of an installed Skill before repair.
func WithSkillCacheMaxAge(maxAge time.Duration) Option {
	return func(options *runtimeOptions) {
		options.config.SkillCacheMaxAge = maxAge
	}
}
