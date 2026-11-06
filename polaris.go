/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package trpcagentpolaris is a compatibility facade for the domain packages.
// New code can import the agent, naming, and server/a2a packages directly.
package trpcagentpolaris

import (
	"github.com/polarismesh/polaris-go/api"
	a2a "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	adapterserver "trpc.group/trpc-go/trpc-agent-naming-polarismesh/server/a2a"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/version"
)

type (
	// Config is the adapter configuration.
	Config = config.Config
	// SkillRef identifies a Polaris Skill version.
	SkillRef = config.SkillRef
	// Runtime owns the adapter lifecycle.
	Runtime = agent.Runtime
	// Option configures a Runtime.
	Option = agent.Option
	// AgentCardBuilder builds the immutable registration card.
	AgentCardBuilder = agent.AgentCardBuilder
	// AgentInstanceMetadata is the registration metadata view.
	AgentInstanceMetadata = agentcard.AgentInstanceMetadata
	// ResolvedAgent is a validated remote A2A instance.
	ResolvedAgent = naming.ResolvedAgent
	// Resolver discovers healthy A2A instances.
	Resolver = naming.Resolver
	// RemoteAgentOptionsFactory creates independently owned client options.
	RemoteAgentOptionsFactory = naming.RemoteAgentOptionsFactory
	// MetadataProvider supplies instance metadata at registration.
	MetadataProvider = naming.MetadataProvider
	// MetadataProviderFunc adapts a function to MetadataProvider.
	MetadataProviderFunc = naming.MetadataProviderFunc
	// MetadataRegistrar installs and removes metadata providers.
	MetadataRegistrar = naming.MetadataRegistrar
	// InstanceLister is the ConsumerAPI subset used by discovery.
	InstanceLister = naming.InstanceLister
)

var (
	// ErrClosed indicates that a runtime or resolver is closed.
	ErrClosed = errors.ErrClosed
	// ErrNotPrepared indicates that provider resources are not ready.
	ErrNotPrepared = errors.ErrNotPrepared
	// ErrInvalidConfig indicates an invalid adapter configuration.
	ErrInvalidConfig = errors.ErrInvalidConfig
	// ErrNoAvailableInstance indicates that no usable A2A instance exists.
	ErrNoAvailableInstance = errors.ErrNoAvailableInstance
	// ErrAgentServiceAPIUnsupported indicates that AgentAPI is unavailable.
	ErrAgentServiceAPIUnsupported = errors.ErrAgentServiceAPIUnsupported
	// ErrSkillInstallerUnsupported indicates that SkillAPI is unavailable.
	ErrSkillInstallerUnsupported = errors.ErrSkillInstallerUnsupported
	// ErrMetadataRegistrarUnsupported indicates that metadata registration is unavailable.
	ErrMetadataRegistrarUnsupported = errors.ErrMetadataRegistrarUnsupported
)

const (
	// Version is the adapter version.
	Version = version.Version

	// MetadataAgentName is the instance metadata key for the Agent name.
	MetadataAgentName = agentcard.MetadataAgentName
	// MetadataAgentCard is the instance metadata key for the serialized AgentCard.
	MetadataAgentCard = agentcard.MetadataAgentCard
	// MetadataTransport is the instance metadata key for the A2A transport.
	MetadataTransport = agentcard.MetadataTransport
	// MetadataPath is the instance metadata key for the A2A base path.
	MetadataPath = agentcard.MetadataPath
)

// New creates an adapter runtime.
func New(options ...Option) (*Runtime, error) { return agent.New(options...) }

// DefaultConfig returns the adapter defaults.
func DefaultConfig() Config { return config.DefaultConfig() }

// NewA2AServer builds an A2A v1 server with the Runtime AgentCard.
func NewA2AServer(r *Runtime, agentRunner runner.Runner, options ...a2aserver.Option) (*a2a.A2AServer, error) {
	return adapterserver.NewA2AServer(r, agentRunner, options...)
}

// NewAgentCard builds an A2A v1 AgentCard from provider configuration.
func NewAgentCard(cfg Config, tools ...tool.Tool) (a2a.AgentCard, error) {
	return agentcard.NewAgentCard(cfg, tools...)
}

// ValidateAgentCard verifies the card's required fields and endpoint.
func ValidateAgentCard(cfg Config, card *a2a.AgentCard) error {
	return agentcard.ValidateAgentCard(cfg, card)
}

// BuildAgentInstanceMetadata serializes a validated AgentCard into metadata.
func BuildAgentInstanceMetadata(cfg Config, card a2a.AgentCard) (map[string]string, error) {
	return agentcard.BuildAgentInstanceMetadata(cfg, card)
}

// ValidateAgentInstanceMetadata validates the four-key metadata contract.
func ValidateAgentInstanceMetadata(metadata map[string]string) error {
	return agentcard.ValidateAgentInstanceMetadata(metadata)
}

// ParseAgentInstanceMetadata decodes and validates AgentCard metadata.
func ParseAgentInstanceMetadata(
	metadata map[string]string,
) (AgentInstanceMetadata, a2a.AgentCard, error) {
	return agentcard.ParseAgentInstanceMetadata(metadata)
}

// SDKContextFromFramework returns the SDK context for an initialized registry.
func SDKContextFromFramework(service string) (api.SDKContext, bool) {
	return agent.SDKContextFromFramework(service)
}

// FrameworkInstanceVersion returns the framework instance version.
func FrameworkInstanceVersion(service string) (string, bool) {
	return agent.FrameworkInstanceVersion(service)
}

// WithConfig replaces the adapter configuration.
func WithConfig(cfg Config) Option { return agent.WithConfig(cfg) }

// WithSDKContext borrows a Polaris SDK context.
func WithSDKContext(sdkContext api.SDKContext) Option {
	return agent.WithSDKContext(sdkContext)
}

// WithService sets the Polaris namespace and service.
func WithService(namespace, service string) Option {
	return agent.WithService(namespace, service)
}

// WithAgentCard supplies a fully built A2A v1 AgentCard.
func WithAgentCard(card a2a.AgentCard) Option { return agent.WithAgentCard(card) }

// WithAgentCardBuilder supplies a card builder.
func WithAgentCardBuilder(builder AgentCardBuilder) Option {
	return agent.WithAgentCardBuilder(builder)
}

// WithCardTools advertises tools as AgentCard skills.
func WithCardTools(tools ...tool.Tool) Option { return agent.WithCardTools(tools...) }

// WithSkillRefs sets explicitly configured Skills.
func WithSkillRefs(refs ...SkillRef) Option { return agent.WithSkillRefs(refs...) }

// WithSkillCacheDir sets the local Skill installation root.
func WithSkillCacheDir(directory string) Option {
	return agent.WithSkillCacheDir(directory)
}

// WithMountSkills enables service-level Skill mounting.
func WithMountSkills(enabled bool) Option { return agent.WithMountSkills(enabled) }

// WithAgentAPI injects a Polaris AgentAPI implementation.
func WithAgentAPI(agentAPI api.AgentAPI) Option { return agent.WithAgentAPI(agentAPI) }

// WithSkillAPI injects a Polaris SkillAPI implementation.
func WithSkillAPI(skillAPI api.SkillAPI) Option { return agent.WithSkillAPI(skillAPI) }

// WithMetadataRegistrar injects the dynamic metadata registrar.
func WithMetadataRegistrar(registrar MetadataRegistrar) Option {
	return agent.WithMetadataRegistrar(registrar)
}

// WithConsumerAPI injects a custom instance lister.
func WithConsumerAPI(consumerAPI InstanceLister) Option {
	return agent.WithConsumerAPI(consumerAPI)
}

// WithDescription sets the generated AgentCard description.
func WithDescription(description string) Option {
	return agent.WithDescription(description)
}

// WithStreaming sets the generated AgentCard streaming capability.
func WithStreaming(enabled bool) Option { return agent.WithStreaming(enabled) }

// WithScheme sets the generated AgentCard URL scheme.
func WithScheme(scheme string) Option { return agent.WithScheme(scheme) }
