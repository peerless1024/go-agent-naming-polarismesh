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
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentmodel"

	"github.com/polarismesh/polaris-go/api"
	polarisconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapteragentapi "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentapi"
	adapteragentcard "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/registration"
	adapternaming "trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
	adapterskill "trpc.group/trpc-go/trpc-agent-naming-polarismesh/skill"
)

type runtimeState uint8

const (
	runtimeStateNew runtimeState = iota
	runtimeStatePrepared
	runtimeStateClosed
)

var (
	// ErrClosed indicates that the runtime has been closed.
	ErrClosed = adaptererrors.ErrClosed
	// ErrNotPrepared indicates that provider resources are not ready.
	ErrNotPrepared = adaptererrors.ErrNotPrepared
	// ErrInvalidConfig indicates an invalid adapter configuration.
	ErrInvalidConfig = adaptererrors.ErrInvalidConfig
	// ErrAgentServiceAPIUnsupported indicates that the adapter AgentAPI is unavailable.
	ErrAgentServiceAPIUnsupported = adaptererrors.ErrAgentServiceAPIUnsupported
	// ErrSkillInstallerUnsupported indicates that Polaris SkillAPI is unavailable.
	ErrSkillInstallerUnsupported = adaptererrors.ErrSkillInstallerUnsupported
	// ErrMetadataRegistrarUnsupported indicates that dynamic metadata registration is unavailable.
	ErrMetadataRegistrarUnsupported = adaptererrors.ErrMetadataRegistrarUnsupported
)

// Runtime owns the adapter lifecycle while borrowing externally supplied
// Polaris resources.
type Runtime struct {
	prepareMu sync.Mutex
	stateMu   sync.RWMutex
	state     runtimeState

	config             adapterconfig.Config
	sdkContext         api.SDKContext
	ownsSDKContext     bool
	agentAPI           adapteragentapi.AgentAPI
	skillAPI           api.SkillAPI
	metadataRegistrar  adapternaming.MetadataRegistrar
	consumerAPI        adapternaming.InstanceLister
	configuredCard     *a2aprotocolserver.AgentCard
	configuredCardJSON []byte
	cardBuilder        AgentCardBuilder
	cardTools          []tool.Tool

	card               a2aprotocolserver.AgentCard
	cardDocument       adapteragentcard.CardDocument
	instanceMetadata   map[string]string
	metadataProvider   *frozenMetadataProvider
	metadataRegistered bool
	skillRepository    agentskill.Repository
	skillRefresh       *adapterskill.PeriodicRepository
	resolver           *adapternaming.Resolver
}

// New creates an adapter runtime. It does not register an instance or start an
// A2A server; call Prepare before the tRPC server starts serving.
func New(options ...Option) (*Runtime, error) {
	opts := defaultRuntimeOptions()
	for _, option := range options {
		if option != nil {
			option(opts)
		}
	}

	normalized := opts.config.Clone().Normalize()
	if normalized.AgentName == "" {
		normalized.AgentName = normalized.ServiceName
	}
	if opts.agentMetadata != nil {
		if opts.agentCard != nil || opts.agentCardBuilder != nil || len(opts.agentCardJSON) != 0 {
			return nil, fmt.Errorf("%w: static instance metadata must not be combined with another AgentCard source", ErrInvalidConfig)
		}
		prepared, err := adapteragentcard.BuildAgentInstanceRegistrationFromMetadata(adapteragentcard.AgentInstanceRegistrationRequest{
			Namespace: normalized.Namespace, Service: normalized.ServiceName,
			A2AVersion: opts.metadataVersion, RegistrationPort: &normalized.RegistrationPort,
		}, opts.agentMetadata)
		if err != nil {
			return nil, adaptererrors.WrapError("prepare static AgentCard metadata", err)
		}
		opts.agentCardJSON = []byte(prepared.Metadata[adapteragentcard.MetadataAgentCard])
		opts.agentCardVersion = prepared.A2AVersion
	}
	if opts.agentCardVersion != "" {
		normalized.A2AVersion = opts.agentCardVersion
	}
	if err := normalized.Validate(); err != nil {
		return nil, err
	}
	if opts.metadataRegistrar == nil {
		opts.metadataRegistrar = polarisMetadataRegistrar{}
	}
	if opts.agentCard != nil {
		card, err := adapteragentcard.CloneAgentCard(*opts.agentCard)
		if err != nil {
			return nil, adaptererrors.WrapError("copy AgentCard", err)
		}
		opts.agentCard = &card
	}

	return &Runtime{
		state:              runtimeStateNew,
		config:             normalized,
		sdkContext:         opts.sdkContext,
		ownsSDKContext:     false,
		agentAPI:           opts.agentAPI,
		skillAPI:           opts.skillAPI,
		metadataRegistrar:  opts.metadataRegistrar,
		consumerAPI:        opts.consumerAPI,
		configuredCard:     opts.agentCard,
		configuredCardJSON: append([]byte(nil), opts.agentCardJSON...),
		cardBuilder:        opts.agentCardBuilder,
		cardTools:          append([]tool.Tool(nil), opts.cardTools...),
	}, nil
}

// Prepare validates configuration and freezes provider metadata before Serve.
func (r *Runtime) Prepare(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.prepareMu.Lock()
	defer r.prepareMu.Unlock()

	r.stateMu.RLock()
	state := r.state
	r.stateMu.RUnlock()
	switch state {
	case runtimeStatePrepared:
		return nil
	case runtimeStateClosed:
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if !r.providerMode() {
		r.stateMu.Lock()
		r.state = runtimeStatePrepared
		r.stateMu.Unlock()
		return nil
	}
	if r.configuredCard == nil && len(r.configuredCardJSON) == 0 && r.cardBuilder == nil {
		if err := r.config.ValidateProvider(); err != nil {
			return err
		}
	}

	document, err := r.buildCardDocument(ctx)
	if err != nil {
		return err
	}
	card := document.Card
	snapshot, err := r.buildRegistration(document)
	if err != nil {
		return err
	}
	metadata := snapshot.Metadata
	r.adoptFrameworkSDKContext()
	if err := r.ensureRequiredPolarisAPIs(); err != nil {
		return err
	}
	if r.agentAPI != nil {
		if err := r.agentAPI.ValidateInstanceMetadata(metadata); err != nil {
			return adaptererrors.WrapError("validate instance metadata with polaris-go", err)
		}
	}
	if !r.config.RegistryEnabled {
		repository, err := r.loadSkills(ctx)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		r.stateMu.Lock()
		r.card = card
		r.cardDocument = document
		r.instanceMetadata = metadata
		r.skillRepository = r.startSkillRefresh(repository)
		r.state = runtimeStatePrepared
		r.stateMu.Unlock()
		return nil
	}

	if r.config.EnsureService {
		if err := ctx.Err(); err != nil {
			return err
		}
		refs, err := mergeSkillRefs(nil, r.config.SkillRefs)
		if err != nil {
			return err
		}
		skills := agentSkillsFromRefs(refs)
		if len(skills) == 0 {
			skills = nil
		}
		service, err := r.agentAPI.EnsureService(&adapteragentapi.EnsureServiceRequest{
			EnsureAgentServiceRequest: agentmodel.EnsureAgentServiceRequest{
				Namespace: r.config.Namespace,
				Service:   r.config.ServiceName,
				Skills:    skills,
			},
		})
		if err != nil {
			return adaptererrors.WrapError("ensure AI_AGENT service", err)
		}
		if err := validateAgentServiceType(service); err != nil {
			return err
		}
	}

	if r.metadataRegistrar == nil {
		return ErrMetadataRegistrarUnsupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	provider := newFrozenMetadataProvider(snapshot.Namespace, snapshot.Service, metadata, snapshot.Host, snapshot.Version, snapshot.Port)
	if err := r.metadataRegistrar.RegisterMetadataProvider(r.config.ServiceName, provider); err != nil {
		return adaptererrors.WrapError("register metadata provider", err)
	}

	repository, err := r.loadSkills(ctx)
	if err != nil {
		r.metadataRegistrar.UnregisterMetadataProvider(r.config.ServiceName)
		return err
	}
	if err := ctx.Err(); err != nil {
		r.metadataRegistrar.UnregisterMetadataProvider(r.config.ServiceName)
		return err
	}

	r.stateMu.Lock()
	r.card = card
	r.cardDocument = document
	r.instanceMetadata = metadata
	r.metadataProvider = provider
	r.metadataRegistered = true
	r.skillRepository = r.startSkillRefresh(repository)
	r.state = runtimeStatePrepared
	r.stateMu.Unlock()
	return nil
}

// Close is idempotent. It never deregisters the tRPC-managed instance and never
// destroys a borrowed SDK context.
func (r *Runtime) Close(ctx context.Context) error {
	_ = ctx
	r.prepareMu.Lock()
	defer r.prepareMu.Unlock()

	r.stateMu.Lock()
	if r.state == runtimeStateClosed {
		r.stateMu.Unlock()
		return nil
	}
	registered := r.metadataRegistered
	r.metadataRegistered = false
	r.state = runtimeStateClosed
	r.stateMu.Unlock()

	if registered && r.metadataRegistrar != nil {
		r.metadataRegistrar.UnregisterMetadataProvider(r.config.ServiceName)
	}
	if r.resolver != nil {
		_ = r.resolver.Close()
	}
	if r.skillRefresh != nil {
		_ = r.skillRefresh.Close()
	}
	if r.ownsSDKContext && r.sdkContext != nil && !r.sdkContext.IsDestroyed() {
		r.sdkContext.Destroy()
	}
	return nil
}

// Config returns a copy of the effective configuration.
func (r *Runtime) Config() adapterconfig.Config {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.config.Clone()
}

// Resolver returns the runtime resolver, creating it lazily.
func (r *Runtime) Resolver() (*adapternaming.Resolver, error) {
	r.stateMu.RLock()
	discoveryEnabled := r.config.DiscoveryEnabled
	r.stateMu.RUnlock()
	if !discoveryEnabled {
		return nil, fmt.Errorf("%w: discovery is disabled", ErrInvalidConfig)
	}
	if err := r.ensureConsumerAPI(); err != nil {
		return nil, err
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.state == runtimeStateClosed {
		return nil, ErrClosed
	}
	if r.state != runtimeStatePrepared {
		return nil, ErrNotPrepared
	}
	if r.resolver == nil {
		r.resolver = adapternaming.NewResolver(adapternaming.ResolverOptions{
			Namespace: r.config.Namespace,
			Service:   r.config.ServiceName,
			Timeout:   r.config.PolarisRequestTimeout,
			Lister:    r.consumerAPI,
		})
	}
	return r.resolver, nil
}

// RegistrationSnapshot returns the atomic metadata/host/version/port/protocol
// values that go-naming should apply in one registration request.
func (r *Runtime) RegistrationSnapshot() (adapternaming.RegistrationSnapshot, error) {
	r.stateMu.RLock()
	if r.state == runtimeStateClosed {
		r.stateMu.RUnlock()
		return adapternaming.RegistrationSnapshot{}, ErrClosed
	}
	if r.state != runtimeStatePrepared || r.metadataProvider == nil {
		r.stateMu.RUnlock()
		return adapternaming.RegistrationSnapshot{}, ErrNotPrepared
	}
	provider := r.metadataProvider
	r.stateMu.RUnlock()
	snapshot, err := provider.Snapshot()
	if err != nil {
		return adapternaming.RegistrationSnapshot{}, err
	}
	return adapternaming.RegistrationSnapshot{
		Metadata:         snapshot.Metadata,
		Host:             snapshot.Host,
		Version:          snapshot.Version,
		RegistrationPort: snapshot.RegistrationPort,
		Protocol:         snapshot.Protocol,
		SetProtocol:      snapshot.SetProtocol,
		TTL:              r.config.RegistryTTL,
	}, nil
}

// AgentCard returns the frozen AgentCard after Prepare.
func (r *Runtime) AgentCard() (a2aprotocolserver.AgentCard, error) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	if r.state == runtimeStateClosed {
		return a2aprotocolserver.AgentCard{}, ErrClosed
	}
	if r.state != runtimeStatePrepared {
		return a2aprotocolserver.AgentCard{}, ErrNotPrepared
	}
	card, err := adapteragentcard.CloneAgentCard(r.card)
	if err != nil {
		return a2aprotocolserver.AgentCard{}, adaptererrors.WrapError("copy AgentCard", err)
	}
	return card, nil
}

// AgentCardJSON returns the exact AgentCard JSON used for instance metadata.
func (r *Runtime) AgentCardJSON() (string, adapteragentcard.SchemaVersion, error) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	if r.state == runtimeStateClosed {
		return "", "", ErrClosed
	}
	if r.state != runtimeStatePrepared || len(r.cardDocument.Raw()) == 0 {
		return "", "", ErrNotPrepared
	}
	return string(r.cardDocument.Raw()), r.cardDocument.Version, nil
}

// UpdateAgentCard atomically replaces the AgentCard used by the next
// registration. A failed validation leaves the previous snapshot untouched.
func (r *Runtime) UpdateAgentCard(
	ctx context.Context,
	card a2aprotocolserver.AgentCard,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	card, err := adapteragentcard.CloneAgentCard(card)
	if err != nil {
		return adaptererrors.WrapError("copy AgentCard", err)
	}
	version := adapteragentcard.SchemaVersion(r.config.A2AVersion)
	if err := adapteragentcard.ValidateAgentCard(r.config, &card, version); err != nil {
		return err
	}
	document, err := adapteragentcard.NewCardDocument(version, card)
	if err != nil {
		return err
	}
	return r.updateAgentCardDocument(ctx, document)
}

// UpdateAgentCardJSON atomically replaces the AgentCard while preserving the
// supplied JSON bytes and signatures.
func (r *Runtime) UpdateAgentCardJSON(
	ctx context.Context,
	version string,
	raw []byte,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(version) != r.config.A2AVersion {
		return fmt.Errorf(
			"AgentCard schema version %q must match configured schema version %q",
			version,
			r.config.A2AVersion,
		)
	}
	document, err := adapteragentcard.ParseCardDocument(
		r.config,
		adapteragentcard.SchemaVersion(version),
		raw,
	)
	if err != nil {
		return err
	}
	return r.updateAgentCardDocument(ctx, document)
}

func (r *Runtime) updateAgentCardDocument(ctx context.Context, document adapteragentcard.CardDocument) error {
	r.prepareMu.Lock()
	defer r.prepareMu.Unlock()
	r.stateMu.RLock()
	state := r.state
	r.stateMu.RUnlock()
	if state == runtimeStateClosed {
		return ErrClosed
	}
	if state != runtimeStatePrepared {
		return ErrNotPrepared
	}
	snapshot, err := r.buildRegistration(document)
	if err != nil {
		return err
	}
	metadata := snapshot.Metadata
	if r.agentAPI != nil {
		if err := r.agentAPI.ValidateInstanceMetadata(metadata); err != nil {
			return adaptererrors.WrapError("validate instance metadata with polaris-go", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.state == runtimeStateClosed {
		return ErrClosed
	}
	if r.state != runtimeStatePrepared || r.metadataProvider == nil {
		return ErrNotPrepared
	}
	r.metadataProvider.update(metadata, snapshot.Host, snapshot.Version, snapshot.Port)
	r.card = document.Card
	r.cardDocument = document
	r.instanceMetadata = metadata
	return nil
}

func (r *Runtime) buildRegistration(document adapteragentcard.CardDocument) (*adapteragentcard.AgentInstanceRegistration, error) {
	snapshot, err := adapteragentcard.BuildAgentInstanceRegistration(adapteragentcard.AgentInstanceRegistrationRequest{
		Namespace:        r.config.Namespace,
		Service:          r.config.ServiceName,
		CardJSON:         string(document.Raw()),
		A2AVersion:       string(document.Version),
		RegistrationPort: &r.config.RegistrationPort,
	})
	if err != nil {
		return nil, err
	}
	if r.config.RegistryEnabled {
		if err := registration.ValidateAgent(snapshot.Namespace, snapshot.Service, snapshot.Version); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

// InstanceMetadata returns a copy of the metadata injected at registration.
func (r *Runtime) InstanceMetadata() (map[string]string, error) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	if r.state == runtimeStateClosed {
		return nil, ErrClosed
	}
	if r.state != runtimeStatePrepared || r.instanceMetadata == nil {
		return nil, ErrNotPrepared
	}
	copied := make(map[string]string, len(r.instanceMetadata))
	for key, value := range r.instanceMetadata {
		copied[key] = value
	}
	return copied, nil
}

// SkillRepository returns the repository assembled by Prepare.
func (r *Runtime) SkillRepository() (agentskill.Repository, error) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	if r.state == runtimeStateClosed {
		return nil, ErrClosed
	}
	if r.state != runtimeStatePrepared {
		return nil, ErrNotPrepared
	}
	return r.skillRepository, nil
}

// SDKContext returns the borrowed or internally created SDK context.
func (r *Runtime) SDKContext() api.SDKContext {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.sdkContext
}

func (r *Runtime) providerMode() bool {
	return r.configuredCard != nil ||
		r.cardBuilder != nil ||
		len(r.configuredCardJSON) > 0 ||
		r.config.AgentVersion != "" ||
		r.config.AdvertiseHost != "" ||
		r.config.AdvertisePort != 0 ||
		r.config.EnsureService ||
		r.config.MountSkills ||
		r.config.UseServiceSkills ||
		r.config.SkillMode != "" ||
		len(r.config.SkillRefs) > 0 ||
		r.config.SkillCacheDir != ""
}

func (r *Runtime) buildCardDocument(ctx context.Context) (adapteragentcard.CardDocument, error) {
	version := adapteragentcard.SchemaVersion(r.config.A2AVersion)
	switch {
	case len(r.configuredCardJSON) > 0:
		return adapteragentcard.ParseCardDocument(r.config, version, r.configuredCardJSON)
	case r.configuredCard != nil:
		card := *r.configuredCard
		if err := adapteragentcard.ValidateAgentCard(r.config, &card, version); err != nil {
			return adapteragentcard.CardDocument{}, err
		}
		return adapteragentcard.NewCardDocument(version, card)
	case r.cardBuilder != nil:
		card, err := r.cardBuilder(ctx)
		if err != nil {
			return adapteragentcard.CardDocument{}, adaptererrors.WrapError("build AgentCard", err)
		}
		card, err = adapteragentcard.CloneAgentCard(card)
		if err != nil {
			return adapteragentcard.CardDocument{}, adaptererrors.WrapError("copy AgentCard", err)
		}
		if err := adapteragentcard.ValidateAgentCard(r.config, &card, version); err != nil {
			return adapteragentcard.CardDocument{}, err
		}
		return adapteragentcard.NewCardDocument(version, card)
	default:
		card, err := adapteragentcard.NewAgentCard(r.config, r.cardTools...)
		if err != nil {
			return adapteragentcard.CardDocument{}, err
		}
		return adapteragentcard.NewCardDocument(version, card)
	}
}

func (r *Runtime) loadSkills(ctx context.Context) (agentskill.Repository, error) {
	if !r.config.UseServiceSkills &&
		len(r.config.SkillRefs) == 0 &&
		!r.config.MountSkills &&
		r.config.SkillCacheDir == "" &&
		r.config.SkillMode != adapterconfig.SkillModePublished &&
		r.config.SkillMode != adapterconfig.SkillModeMounted {
		return nil, nil
	}
	loader := adapterskill.NewLoader(r.agentAPI, r.skillAPI)
	options := adapterskill.Options{
		Namespace:        r.config.Namespace,
		Service:          r.config.ServiceName,
		CacheDir:         r.config.SkillCacheDir,
		RequestTimeout:   r.config.PolarisRequestTimeout,
		Mode:             r.config.SkillMode,
		ExplicitRefs:     r.config.SkillRefs,
		UseServiceSkills: r.config.UseServiceSkills,
		Mount:            r.config.MountSkills,
		MountReplace:     r.config.MountSkillsReplace,
		ListLimit:        r.config.SkillListLimit,
		MaxSkills:        r.config.SkillMaxSkills,
		FailureThreshold: r.config.SkillFailureThreshold,
		CacheMaxAge:      r.config.SkillCacheMaxAge,
	}
	load := func(loadCtx context.Context) (agentskill.Repository, error) {
		return loader.Load(loadCtx, options)
	}
	repository, err := load(ctx)
	if err != nil {
		return nil, adaptererrors.WrapError("load Skills", err)
	}
	return repository, nil
}

// startSkillRefresh transfers lifecycle ownership only when Prepare commits.
// The caller holds stateMu and has finished all fallible preparation steps.
func (r *Runtime) startSkillRefresh(repository agentskill.Repository) agentskill.Repository {
	if !r.config.UseServiceSkills && len(r.config.SkillRefs) == 0 &&
		!r.config.MountSkills && r.config.SkillCacheDir == "" &&
		r.config.SkillMode != adapterconfig.SkillModePublished &&
		r.config.SkillMode != adapterconfig.SkillModeMounted {
		return repository
	}
	if r.config.SkillListRefresh > 0 {
		refresh := adapterskill.NewPeriodicRepository(
			context.Background(),
			repository,
			r.config.SkillListRefresh,
			r.config.PolarisRequestTimeout,
			r.loadSkills,
		)
		r.skillRefresh = refresh
		return refresh
	}
	return repository
}

func (r *Runtime) ensureConsumerAPI() error {
	r.stateMu.RLock()
	state := r.state
	consumer := r.consumerAPI
	sdkContext := r.sdkContext
	r.stateMu.RUnlock()
	if state == runtimeStateClosed {
		return ErrClosed
	}
	if state != runtimeStatePrepared {
		return ErrNotPrepared
	}
	if consumer != nil {
		return nil
	}
	if sdkContext == nil {
		var err error
		sdkContext, err = r.ensureSDKContext()
		if err != nil {
			return err
		}
	}
	consumer = api.NewConsumerAPIByContext(sdkContext)
	r.stateMu.Lock()
	if r.state == runtimeStateClosed {
		r.stateMu.Unlock()
		return ErrClosed
	}
	r.consumerAPI = consumer
	r.stateMu.Unlock()
	return nil
}

func (r *Runtime) ensureRequiredPolarisAPIs() error {
	needsAgentAPI := r.config.EnsureService ||
		r.config.MountSkills ||
		r.config.UseServiceSkills ||
		r.config.SkillMode == adapterconfig.SkillModeMounted
	needsSkillAPI := len(r.config.SkillRefs) > 0 ||
		r.config.UseServiceSkills ||
		r.config.SkillMode == adapterconfig.SkillModePublished ||
		r.config.SkillMode == adapterconfig.SkillModeMounted
	if !needsAgentAPI && !needsSkillAPI {
		return nil
	}

	r.stateMu.RLock()
	agentAPI := r.agentAPI
	skillAPI := r.skillAPI
	sdkContext := r.sdkContext
	r.stateMu.RUnlock()
	if (!needsAgentAPI || agentAPI != nil) && (!needsSkillAPI || skillAPI != nil) {
		return nil
	}

	if sdkContext == nil {
		switch {
		case agentAPI != nil:
			sdkContext = agentAPI.SDKContext()
		case skillAPI != nil:
			sdkContext = skillAPI.SDKContext()
		}
	}
	if sdkContext == nil {
		var err error
		sdkContext, err = r.ensureSDKContext()
		if err != nil {
			if needsAgentAPI && agentAPI == nil {
				return fmt.Errorf("%w: %v", ErrAgentServiceAPIUnsupported, err)
			}
			if needsSkillAPI && skillAPI == nil {
				return fmt.Errorf("%w: %v", ErrSkillInstallerUnsupported, err)
			}
			return err
		}
	}

	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.agentAPI == nil && needsAgentAPI {
		r.agentAPI = adapteragentapi.NewByContext(sdkContext)
	}
	if r.skillAPI == nil && needsSkillAPI {
		r.skillAPI = api.NewSkillAPIByContext(sdkContext)
	}
	return nil
}

func (r *Runtime) adoptFrameworkSDKContext() {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.sdkContext != nil || r.state == runtimeStateClosed {
		return
	}
	if sdkContext, ok := SDKContextFromFramework(r.config.ServiceName); ok {
		r.sdkContext = sdkContext
	}
}

func (r *Runtime) ensureSDKContext() (api.SDKContext, error) {
	r.stateMu.RLock()
	if r.state == runtimeStateClosed {
		r.stateMu.RUnlock()
		return nil, ErrClosed
	}
	if r.sdkContext != nil {
		context := r.sdkContext
		r.stateMu.RUnlock()
		return context, nil
	}
	cfg := r.config
	r.stateMu.RUnlock()

	if frameworkContext, ok := SDKContextFromFramework(cfg.ServiceName); ok {
		r.stateMu.Lock()
		if r.state == runtimeStateClosed {
			r.stateMu.Unlock()
			return nil, ErrClosed
		}
		if r.sdkContext == nil {
			r.sdkContext = frameworkContext
		}
		actual := r.sdkContext
		r.stateMu.Unlock()
		return actual, nil
	}
	if len(cfg.PolarisAddresses) == 0 {
		return nil, fmt.Errorf(
			"%w: polaris addresses are required when no framework registry context exists",
			ErrInvalidConfig,
		)
	}

	polarisConfig := polarisconfig.NewDefaultConfiguration(cfg.PolarisAddresses)
	if token := strings.TrimSpace(cfg.PolarisToken); token != "" {
		polarisConfig.GetGlobal().GetServerConnector().SetToken(token)
	}
	if cfg.PolarisRequestTimeout > 0 {
		polarisConfig.GetGlobal().GetAPI().SetTimeout(cfg.PolarisRequestTimeout)
		polarisConfig.GetSkill().SetMessageTimeout(cfg.PolarisRequestTimeout)
		polarisConfig.GetSkill().SetDownloadTimeout(cfg.PolarisRequestTimeout)
	}
	created, err := api.InitContextByConfig(polarisConfig)
	if err != nil {
		return nil, adaptererrors.WrapError("create SDK context", err)
	}

	r.stateMu.Lock()
	if r.state == runtimeStateClosed {
		r.stateMu.Unlock()
		created.Destroy()
		return nil, ErrClosed
	}
	if r.sdkContext != nil {
		existing := r.sdkContext
		r.stateMu.Unlock()
		created.Destroy()
		return existing, nil
	}
	r.sdkContext = created
	r.ownsSDKContext = true
	r.stateMu.Unlock()
	return created, nil
}

func validateAgentServiceType(service *model.AgentService) error {
	if service == nil {
		return errors.New("ensure service returned an empty service")
	}
	if service.Type == model.ServiceTypeAIAgent {
		return nil
	}
	return fmt.Errorf("service %q has type %s, expected %s", service.Name, service.Type, model.ServiceTypeAIAgent)
}

func mergeSkillRefs(base, explicit []adapterconfig.SkillRef) ([]adapterconfig.SkillRef, error) {
	byID := make(map[string]adapterconfig.SkillRef, len(base)+len(explicit))
	for _, ref := range append(base, explicit...) {
		ref = ref.Normalize()
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf("invalid Skill reference %q: %w", ref.ID(), err)
		}
		byID[ref.ID()] = ref
	}
	result := make([]adapterconfig.SkillRef, 0, len(byID))
	for _, ref := range byID {
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID() < result[j].ID()
	})
	return result, nil
}

func agentSkillsFromRefs(refs []adapterconfig.SkillRef) []model.AgentSkill {
	skills := make([]model.AgentSkill, 0, len(refs))
	for _, ref := range refs {
		ref = ref.Normalize()
		skills = append(skills, model.AgentSkill{
			ID:      ref.ID(),
			Name:    ref.Name,
			Version: ref.Version,
		})
	}
	return skills
}
