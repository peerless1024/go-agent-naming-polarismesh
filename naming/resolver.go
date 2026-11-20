/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package naming

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
	adaptermetrics "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/metrics"
	"trpc.group/trpc-go/trpc-go/log"
)

const (
	maxResolverCallerEntries = 1024
	resolverFallbackTTL      = 30 * time.Second
)

var (
	// ErrClosed indicates that the resolver has been closed.
	ErrClosed = adaptererrors.ErrClosed
	// ErrNotPrepared indicates that provider resources are not ready.
	ErrNotPrepared = adaptererrors.ErrNotPrepared
	// ErrNoAvailableInstance indicates that no usable instance is available.
	ErrNoAvailableInstance = adaptererrors.ErrNoAvailableInstance
	// ErrUnsupportedProtocolBinding indicates that the fixed A2A client cannot use a binding.
	ErrUnsupportedProtocolBinding = adaptererrors.ErrUnsupportedProtocolBinding
)

// ResolvedAgent is one healthy, validated remote A2A instance.
type ResolvedAgent struct {
	InstanceID      string
	Endpoint        string
	Card            a2aprotocolserver.AgentCard
	CardJSON        string
	A2AVersion      agentcard.SchemaVersion
	Metadata        map[string]string
	ProtocolBinding string
	ProtocolVersion string
	Tenant          string
}

// Resolver discovers healthy A2A instances from Polaris.
type Resolver struct {
	namespace       string
	service         string
	timeout         time.Duration
	lister          InstanceLister
	protocolBinding string

	mu       sync.RWMutex
	closed   bool
	closedCh chan struct{}
	now      func() time.Time

	resolveSequence uint64
	bindingStates   map[string]*resolverBindingState
}

// ResolverOptions configures a Resolver.
type ResolverOptions struct {
	Namespace       string
	Service         string
	Timeout         time.Duration
	Lister          InstanceLister
	ProtocolBinding string
}

type resolverCaller struct {
	instanceID string
	lastSeen   time.Time
	lastUsed   time.Time
}

type resolverBindingState struct {
	callers                map[string]resolverCaller
	agents                 map[string]ResolvedAgent
	lastCommittedIteration uint64
	lastCommittedError     error
	latestCandidates       []*ResolvedAgent
	latestSeen             time.Time
}

// NewResolver creates a Polaris A2A instance resolver.
func NewResolver(options ResolverOptions) *Resolver {
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Second
	}
	return &Resolver{
		namespace:       options.Namespace,
		service:         options.Service,
		timeout:         options.Timeout,
		lister:          options.Lister,
		protocolBinding: options.ProtocolBinding,
		bindingStates:   make(map[string]*resolverBindingState),
		now:             time.Now,
		closedCh:        make(chan struct{}),
	}
}

func (r *Resolver) bindingStateReadOnlyLocked(protocolBinding string) *resolverBindingState {
	return r.bindingStates[resolverBindingKey(protocolBinding)]
}

func (r *Resolver) bindingStateLocked(protocolBinding string) *resolverBindingState {
	key := resolverBindingKey(protocolBinding)
	state := r.bindingStates[key]
	if state == nil {
		state = &resolverBindingState{
			callers: make(map[string]resolverCaller),
			agents:  make(map[string]ResolvedAgent),
		}
		r.bindingStates[key] = state
	}
	return state
}

func resolverBindingKey(protocolBinding string) string {
	return strings.ToLower(strings.TrimSpace(protocolBinding))
}

// Resolve returns the sticky healthy instance for callerKey.
func (r *Resolver) Resolve(ctx context.Context, callerKey string) (*ResolvedAgent, error) {
	return r.ResolveWithBinding(ctx, callerKey, r.protocolBinding)
}

// ResolveWithBinding returns the sticky healthy instance and selects an
// interface matching protocolBinding.
func (r *Resolver) ResolveWithBinding(
	ctx context.Context,
	callerKey string,
	protocolBinding string,
) (*ResolvedAgent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if callerKey == "" {
		callerKey = "default"
	}
	if err := agentcard.ValidateConsumerBinding(protocolBinding); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		resolved, retry, err := r.resolveOnce(ctx, callerKey, protocolBinding)
		if !retry {
			return resolved, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: discovery result was superseded", ErrNoAvailableInstance)
}

func (r *Resolver) resolveOnce(
	ctx context.Context,
	callerKey string,
	protocolBinding string,
) (*ResolvedAgent, bool, error) {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return nil, false, ErrClosed
	}
	if r.lister == nil {
		r.mu.RUnlock()
		return nil, false, errors.New("polaris instance lister is required")
	}
	var stickyID string
	if state := r.bindingStateReadOnlyLocked(protocolBinding); state != nil {
		if caller, ok := state.callers[callerKey]; ok {
			stickyID = caller.instanceID
		}
	}
	r.mu.RUnlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, false, ErrClosed
	}
	r.resolveSequence++
	sequence := r.resolveSequence
	bindingState := r.bindingStateLocked(protocolBinding)
	r.mu.Unlock()

	timeout := r.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return nil, false, context.DeadlineExceeded
	}
	request := &api.GetInstancesRequest{
		GetInstancesRequest: model.GetInstancesRequest{
			Namespace:       r.namespace,
			Service:         r.service,
			SkipRouteFilter: true,
			Timeout:         &timeout,
		},
	}
	request.SetRetryCount(0)
	response, err := r.lister.GetInstances(request)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, false, ctxErr
	}
	if err != nil {
		resolved, fallbackErr := r.resolveFallback(sequence, callerKey, protocolBinding, err)
		return resolved, false, fallbackErr
	}
	if response == nil {
		return r.commitInvalidResolve(sequence, callerKey, protocolBinding,
			fmt.Errorf("%w: polaris returned an empty instance response", ErrNoAvailableInstance))
	}

	candidates := make([]*ResolvedAgent, 0, len(response.GetInstances()))
	var rejected []error
	for _, instance := range response.GetInstances() {
		if instance == nil || !instance.IsHealthy() || instance.IsIsolated() {
			continue
		}
		resolved, err := resolveInstance(instance, protocolBinding)
		if err != nil {
			rejected = append(rejected, fmt.Errorf("instance %s: %w", instance.GetId(), err))
			adaptermetrics.ReportInvalidAgentInstance(r.namespace, r.service, instance.GetId())
			log.Warnf("skip invalid A2A Agent instance %s: %v", instance.GetId(), err)
			continue
		}
		candidates = append(candidates, resolved)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].InstanceID < candidates[j].InstanceID
	})
	if len(candidates) == 0 {
		if len(rejected) > 0 {
			return r.commitInvalidResolve(sequence, callerKey, protocolBinding,
				fmt.Errorf("%w: %w", ErrNoAvailableInstance, errors.Join(rejected...)))
		}
		return r.commitInvalidResolve(sequence, callerKey, protocolBinding, ErrNoAvailableInstance)
	}

	selected := candidates[stableIndex(callerKey, len(candidates))]
	for _, candidate := range candidates {
		if candidate.InstanceID == stickyID {
			selected = candidate
			break
		}
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, false, ErrClosed
	}
	if r.isStaleResolveLocked(sequence, bindingState) {
		resolved, retry, err := r.recoverCurrentResolveLocked(bindingState, callerKey, protocolBinding)
		r.mu.Unlock()
		return resolved, retry, err
	}
	if err := r.commitSuccessfulResolveLocked(sequence, bindingState, callerKey, selected); err != nil {
		r.mu.Unlock()
		return nil, false, err
	}
	bindingState.latestCandidates = candidates
	bindingState.latestSeen = r.now()
	r.pruneUnavailableAgentsLocked(bindingState, candidates)
	result, err := cloneResolvedAgent(*selected)
	r.mu.Unlock()
	if err != nil {
		return nil, false, err
	}
	return &result, false, nil
}

// NewRemoteAgent returns an agent.Agent that rediscovers the target instance
// before each new invocation. One Run call keeps one instance for its whole
// event stream; partial-output retries remain the caller's responsibility.
// Factories run when a client is built, and must return options whose mutable
// client configuration is owned by that build. Transports may be shared.
func (r *Resolver) NewRemoteAgent(
	ctx context.Context,
	callerKey string,
	factories ...RemoteAgentOptionsFactory,
) (agent.Agent, error) {
	return r.NewRemoteAgentWithBinding(ctx, callerKey, r.protocolBinding, factories...)
}

// NewRemoteAgentWithBinding returns a dynamic Agent restricted to one
// AgentCard protocol binding.
func (r *Resolver) NewRemoteAgentWithBinding(
	ctx context.Context,
	callerKey string,
	protocolBinding string,
	factories ...RemoteAgentOptionsFactory,
) (agent.Agent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	remote := &dynamicRemoteAgent{
		resolver:        r,
		callerKey:       callerKey,
		protocolBinding: protocolBinding,
		factories:       append([]RemoteAgentOptionsFactory(nil), factories...),
		build:           make(chan struct{}, 1),
	}
	if _, err := remote.remoteForRun(ctx); err != nil {
		return nil, err
	}
	return remote, nil
}

// RemoteAgentOptionsFactory supplies options for one newly constructed A2A
// client. Return a new http.Client if options change its timeout or transport;
// sharing an immutable Transport or authentication provider is supported.
// A superseded build can invoke the factory again, so it must be replay-safe.
type RemoteAgentOptionsFactory func(context.Context) ([]a2aagent.Option, error)

// RegistrationSnapshot is the optional atomic registration contract exposed
// by metadata providers. The naming layer must evaluate it once per
// registration so metadata, host, version, port, and protocol cannot diverge.
type RegistrationSnapshot struct {
	Metadata         map[string]string
	Host             string
	Version          string
	RegistrationPort int
	Protocol         string
	SetProtocol      bool
	TTL              time.Duration
}

// Close releases resolver state. It does not close SDK contexts or clients
// owned by callers.
func (r *Resolver) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	close(r.closedCh)
	for _, state := range r.bindingStates {
		state.callers = nil
		state.agents = nil
		state.latestCandidates = nil
	}
	r.bindingStates = nil
	return nil
}

// Forget removes caller-scoped resolver state when a caller session ends.
func (r *Resolver) Forget(callerKey string) {
	if callerKey == "" {
		callerKey = "default"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	for _, state := range r.bindingStates {
		caller, ok := state.callers[callerKey]
		delete(state.callers, callerKey)
		if ok && !state.agentHasOtherCallerLocked(caller.instanceID) {
			delete(state.agents, caller.instanceID)
		}
	}
}

type dynamicRemoteAgent struct {
	resolver        *Resolver
	callerKey       string
	protocolBinding string
	factories       []RemoteAgentOptionsFactory

	build      chan struct{}
	mu         sync.RWMutex
	current    *a2aagent.A2AAgent
	currentKey string
}

func (a *dynamicRemoteAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	remote, err := a.remoteForRun(ctx)
	if err != nil {
		return nil, err
	}
	return remote.Run(ctx, invocation)
}

func (a *dynamicRemoteAgent) remoteForRun(ctx context.Context) (*a2aagent.A2AAgent, error) {
	resolved, err := a.resolver.ResolveWithBinding(ctx, a.callerKey, a.protocolBinding)
	if err != nil {
		return nil, err
	}
	key, err := resolvedAgentKey(*resolved)
	if err != nil {
		return nil, adaptererrors.WrapError("serialize resolved AgentCard", err)
	}
	a.resolver.mu.RLock()
	current, err := a.resolver.resolvedKeyIsCurrentLocked(ctx, a.callerKey, a.protocolBinding, key)
	if err != nil {
		a.resolver.mu.RUnlock()
		return nil, err
	}
	a.mu.RLock()
	remote, currentKey := a.current, a.currentKey
	a.mu.RUnlock()
	a.resolver.mu.RUnlock()
	if current && remote != nil && currentKey == key {
		return remote, nil
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.resolver.closedCh:
		return nil, ErrClosed
	case a.build <- struct{}{}:
	}
	defer func() { <-a.build }()
	for attempt := 0; attempt < 3; attempt++ {
		// Both queued builds and factories can outlive a discovery snapshot.
		resolved, err = a.resolver.currentResolvedForBuild(ctx, a.callerKey, a.protocolBinding)
		if err != nil {
			return nil, err
		}
		key, err = resolvedAgentKey(*resolved)
		if err != nil {
			return nil, adaptererrors.WrapError("serialize resolved AgentCard", err)
		}
		a.resolver.mu.RLock()
		current, err = a.resolver.resolvedKeyIsCurrentLocked(
			ctx,
			a.callerKey,
			a.protocolBinding,
			key,
		)
		if err != nil {
			a.resolver.mu.RUnlock()
			return nil, err
		}
		a.mu.RLock()
		remote, currentKey = a.current, a.currentKey
		a.mu.RUnlock()
		a.resolver.mu.RUnlock()
		if !current {
			continue
		}
		if remote != nil && currentKey == key {
			return remote, nil
		}
		remote, err = a.resolver.buildRemoteAgentForResolved(ctx, resolved, a.factories)
		if err != nil {
			return nil, err
		}
		a.resolver.mu.RLock()
		current, err = a.resolver.resolvedKeyIsCurrentLocked(
			ctx,
			a.callerKey,
			a.protocolBinding,
			key,
		)
		if err != nil {
			a.resolver.mu.RUnlock()
			return nil, err
		}
		if !current {
			a.resolver.mu.RUnlock()
			continue
		}
		a.mu.Lock()
		a.current, a.currentKey = remote, key
		a.mu.Unlock()
		a.resolver.mu.RUnlock()
		return remote, nil
	}
	return nil, fmt.Errorf("%w: client build was superseded", ErrNoAvailableInstance)
}

func (a *dynamicRemoteAgent) Tools() []tool.Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.current == nil {
		return []tool.Tool{}
	}
	return a.current.Tools()
}

func (a *dynamicRemoteAgent) Info() agent.Info {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.current == nil {
		return agent.Info{}
	}
	return a.current.Info()
}

func (a *dynamicRemoteAgent) SubAgents() []agent.Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.current == nil {
		return []agent.Agent{}
	}
	return a.current.SubAgents()
}

func (a *dynamicRemoteAgent) FindSubAgent(name string) agent.Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.current == nil {
		return nil
	}
	return a.current.FindSubAgent(name)
}

func (r *Resolver) buildRemoteAgentForResolved(
	ctx context.Context,
	resolved *ResolvedAgent,
	factories []RemoteAgentOptionsFactory,
) (*a2aagent.A2AAgent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.checkOpen(ctx); err != nil {
		return nil, err
	}
	var buildOptions []a2aagent.Option
	for _, factory := range factories {
		if factory == nil {
			continue
		}
		options, err := factory(ctx)
		if err != nil {
			return nil, adaptererrors.WrapError("configure A2A client", err)
		}
		for _, option := range options {
			if option != nil {
				buildOptions = append(buildOptions, option)
			}
		}
	}
	if err := r.checkOpen(ctx); err != nil {
		return nil, err
	}
	clientCard, err := clientCardForResolved(*resolved)
	if err != nil {
		return nil, err
	}
	buildOptions = append(buildOptions, a2aagent.WithAgentCard(&clientCard))
	if tenant := strings.TrimSpace(resolved.Tenant); tenant != "" {
		buildOptions = append(
			buildOptions,
			a2aagent.WithA2AClientExtraOptions(a2aclient.WithTenant(tenant)),
		)
	}
	if binding := strings.TrimSpace(resolved.ProtocolBinding); binding != "" {
		buildOptions = append(
			buildOptions,
			a2aagent.WithA2AClientExtraOptions(a2aclient.WithProtocolBinding(binding)),
		)
	}
	remote, err := a2aagent.New(buildOptions...)
	if err != nil {
		return nil, adaptererrors.WrapError("create A2A client", err)
	}
	if err := r.checkOpen(ctx); err != nil {
		return nil, err
	}
	return remote, nil
}

func clientCardForResolved(resolved ResolvedAgent) (a2aprotocolserver.AgentCard, error) {
	card, err := agentcard.CloneAgentCard(resolved.Card)
	if err != nil {
		return a2aprotocolserver.AgentCard{}, adaptererrors.WrapError("copy AgentCard", err)
	}
	card.NormalizeInterfaces()
	selected, err := agentcard.SelectConsumableInterface(
		card,
		resolved.A2AVersion,
		resolved.ProtocolBinding,
	)
	if err != nil {
		return a2aprotocolserver.AgentCard{}, err
	}
	selectedURL := selected.URL
	selected.URL = resolved.Endpoint
	interfaces := make([]a2aprotocolserver.AgentInterface, 0, len(card.SupportedInterfaces))
	interfaces = append(interfaces, selected)
	removed := false
	for _, item := range card.SupportedInterfaces {
		if !removed &&
			strings.EqualFold(item.ProtocolBinding, selected.ProtocolBinding) &&
			item.URL == selectedURL {
			removed = true
			continue
		}
		interfaces = append(interfaces, item)
	}
	card.SupportedInterfaces = interfaces
	card.URL = resolved.Endpoint
	return card, nil
}

func (r *Resolver) checkOpen(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return ErrClosed
	}
	return nil
}

func (r *Resolver) currentResolvedForBuild(
	ctx context.Context,
	callerKey string,
	protocolBinding string,
) (*ResolvedAgent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if callerKey == "" {
		callerKey = "default"
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrClosed
	}
	bindingState := r.bindingStateLocked(protocolBinding)
	resolved, retry, err := r.recoverCurrentResolveLocked(bindingState, callerKey, protocolBinding)
	r.mu.Unlock()
	if retry {
		return r.ResolveWithBinding(ctx, callerKey, protocolBinding)
	}
	return resolved, err
}

func (r *Resolver) resolvedKeyIsCurrentLocked(
	ctx context.Context,
	callerKey string,
	protocolBinding string,
	key string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r.closed {
		return false, ErrClosed
	}
	if callerKey == "" {
		callerKey = "default"
	}
	state := r.bindingStateReadOnlyLocked(protocolBinding)
	if state == nil {
		return false, nil
	}
	if state.lastCommittedError != nil {
		return false, state.lastCommittedError
	}
	caller, ok := state.callers[callerKey]
	if !ok {
		return false, nil
	}
	if r.now().Sub(caller.lastSeen) > resolverFallbackTTL {
		return false, fmt.Errorf("%w: cached AgentCard expired during client build", ErrNoAvailableInstance)
	}
	for _, candidate := range state.latestCandidates {
		if candidate.InstanceID == caller.instanceID {
			bound, err := bindResolvedAgent(*candidate, protocolBinding)
			if err != nil {
				return false, nil
			}
			currentKey, err := resolvedAgentKey(bound)
			return currentKey == key, err
		}
	}
	return false, nil
}

func resolvedAgentKey(resolved ResolvedAgent) (string, error) {
	return resolved.InstanceID + "\n" + resolved.Endpoint + "\n" +
		resolved.CardJSON + "\n" + resolved.ProtocolBinding + "\n" +
		resolved.ProtocolVersion + "\n" + resolved.Tenant, nil
}

func resolveInstance(instance model.Instance, protocolBinding string) (*ResolvedAgent, error) {
	metadata := instance.GetMetadata()
	parsedMetadata, card, err := agentcard.ParseAgentInstanceMetadata(metadata)
	if err != nil {
		return nil, adaptererrors.WrapError("parse AgentCard metadata", err)
	}
	item, err := agentcard.SelectConsumableInterface(card, parsedMetadata.A2AVersion, protocolBinding)
	if err != nil {
		return nil, err
	}
	endpoint, err := agentcard.Endpoint(item)
	if err != nil {
		return nil, adaptererrors.WrapError("parse AgentCard interface", err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("AgentCard interface is invalid")
	}
	cardIP := net.ParseIP(parsed.Hostname())
	instanceIP := net.ParseIP(instance.GetHost())
	if cardIP == nil || instanceIP == nil || !cardIP.Equal(instanceIP) {
		return nil, fmt.Errorf(
			"AgentCard interface host %q does not match instance host %q",
			parsed.Hostname(),
			instance.GetHost(),
		)
	}

	copiedMetadata := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copiedMetadata[key] = value
	}
	return &ResolvedAgent{
		InstanceID:      instance.GetId(),
		Endpoint:        endpoint,
		Card:            card,
		CardJSON:        parsedMetadata.CardJSON,
		A2AVersion:      parsedMetadata.A2AVersion,
		Metadata:        copiedMetadata,
		ProtocolBinding: item.ProtocolBinding,
		ProtocolVersion: item.ProtocolVersion,
		Tenant:          strings.TrimSpace(item.Tenant),
	}, nil
}

func bindResolvedAgent(resolved ResolvedAgent, protocolBinding string) (ResolvedAgent, error) {
	item, err := agentcard.SelectConsumableInterface(
		resolved.Card,
		resolved.A2AVersion,
		protocolBinding,
	)
	if err != nil {
		return ResolvedAgent{}, err
	}
	endpoint, err := agentcard.Endpoint(item)
	if err != nil {
		return ResolvedAgent{}, err
	}
	resolved.Endpoint = endpoint
	resolved.ProtocolBinding = item.ProtocolBinding
	resolved.ProtocolVersion = item.ProtocolVersion
	resolved.Tenant = strings.TrimSpace(item.Tenant)
	return resolved, nil
}

func stableIndex(key string, count int) int {
	if count <= 1 {
		return 0
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(key))
	return int(hasher.Sum64() % uint64(count))
}

func (r *Resolver) resolveFallback(
	sequence uint64,
	callerKey string,
	protocolBinding string,
	cause error,
) (*ResolvedAgent, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrClosed
	}
	bindingState := r.bindingStateLocked(protocolBinding)
	if !canFallbackDiscoveryError(cause) {
		if !r.isStaleResolveLocked(sequence, bindingState) {
			bindingState.lastCommittedIteration = sequence
			bindingState.lastCommittedError = cause
			bindingState.latestCandidates = nil
			r.pruneUnavailableAgentsLocked(bindingState, nil)
		}
		r.mu.Unlock()
		return nil, cause
	}
	if bindingState.lastCommittedError != nil {
		err := fmt.Errorf("%w: discover A2A instances: %w: %w",
			ErrNoAvailableInstance, cause, bindingState.lastCommittedError)
		r.mu.Unlock()
		return nil, err
	}
	caller, ok := bindingState.callers[callerKey]
	expired := ok && r.now().Sub(caller.lastSeen) > resolverFallbackTTL
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: discover A2A instances: %w", ErrNoAvailableInstance, cause)
	}
	if expired {
		delete(bindingState.callers, callerKey)
		if !bindingState.agentHasOtherCallerLocked(caller.instanceID) {
			delete(bindingState.agents, caller.instanceID)
		}
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: cached AgentCard expired: %w", ErrNoAvailableInstance, cause)
	}
	fallback, hasFallback := bindingState.agents[caller.instanceID]
	if hasFallback {
		caller.lastUsed = r.now()
		bindingState.callers[callerKey] = caller
	}
	r.mu.Unlock()
	if !hasFallback {
		return nil, fmt.Errorf("%w: discover A2A instances: %w", ErrNoAvailableInstance, cause)
	}
	bound, err := bindResolvedAgent(fallback, protocolBinding)
	if err != nil {
		return nil, fmt.Errorf("%w: cached AgentCard is incompatible with protocol binding: %w",
			ErrNoAvailableInstance, err)
	}
	copied, err := cloneResolvedAgent(bound)
	if err != nil {
		return nil, fmt.Errorf("copy cached AgentCard: %w", err)
	}
	adaptermetrics.ReportAgentDiscoveryFallback(r.namespace, r.service)
	log.Warnf("Polaris discovery failed for service %s, using cached AgentCard: %v", r.service, cause)
	return &copied, nil
}

func canFallbackDiscoveryError(cause error) bool {
	var sdkError model.SDKError
	if errors.As(cause, &sdkError) {
		switch sdkError.ErrorCode() {
		case model.ErrCodeAPITimeoutError,
			model.ErrCodeNetworkError,
			model.ErrCodeServerException,
			model.ErrCodeConnectError,
			model.ErrCodeServerError,
			model.ErrorCodeRpcTimeout:
			return true
		default:
			return false
		}
	}
	return true
}

func (r *Resolver) recoverCurrentResolveLocked(
	state *resolverBindingState,
	callerKey string,
	protocolBinding string,
) (*ResolvedAgent, bool, error) {
	if r.closed {
		return nil, false, ErrClosed
	}
	if state.lastCommittedError != nil {
		return nil, false, state.lastCommittedError
	}
	if len(state.latestCandidates) == 0 || r.now().Sub(state.latestSeen) > resolverFallbackTTL {
		return nil, true, nil
	}
	selected := state.latestCandidates[stableIndex(callerKey, len(state.latestCandidates))]
	bound, err := bindResolvedAgent(*selected, protocolBinding)
	if err != nil {
		return nil, true, nil
	}
	selected = &bound
	if caller, ok := state.callers[callerKey]; ok {
		for _, candidate := range state.latestCandidates {
			if candidate.InstanceID == caller.instanceID {
				bound, bindErr := bindResolvedAgent(*candidate, protocolBinding)
				if bindErr != nil {
					return nil, true, nil
				}
				selected = &bound
				break
			}
		}
	}
	copied, err := cloneResolvedAgent(*selected)
	if err != nil {
		return nil, false, err
	}
	now := r.now()
	r.evictCallerLocked(state, callerKey, now)
	state.callers[callerKey] = resolverCaller{
		instanceID: selected.InstanceID,
		lastSeen:   state.latestSeen,
		lastUsed:   now,
	}
	state.agents[selected.InstanceID] = *selected
	return &copied, false, nil
}

func (r *Resolver) isStaleResolveLocked(sequence uint64, state *resolverBindingState) bool {
	return sequence <= state.lastCommittedIteration
}

func (r *Resolver) commitInvalidResolve(
	sequence uint64,
	callerKey string,
	protocolBinding string,
	cause error,
) (*ResolvedAgent, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, false, ErrClosed
	}
	bindingState := r.bindingStateLocked(protocolBinding)
	if r.isStaleResolveLocked(sequence, bindingState) {
		return r.recoverCurrentResolveLocked(bindingState, callerKey, protocolBinding)
	}
	bindingState.lastCommittedIteration = sequence
	bindingState.lastCommittedError = cause
	bindingState.latestCandidates = nil
	r.pruneUnavailableAgentsLocked(bindingState, nil)
	return nil, false, cause
}

func (r *Resolver) commitSuccessfulResolveLocked(
	sequence uint64,
	state *resolverBindingState,
	callerKey string,
	selected *ResolvedAgent,
) error {
	now := r.now()
	fallback, err := cloneResolvedAgent(*selected)
	if err != nil {
		return err
	}
	state.lastCommittedIteration = sequence
	state.lastCommittedError = nil
	r.evictCallerLocked(state, callerKey, now)
	state.callers[callerKey] = resolverCaller{
		instanceID: selected.InstanceID,
		lastSeen:   now,
		lastUsed:   now,
	}
	state.agents[selected.InstanceID] = fallback
	return nil
}

func (r *Resolver) evictCallerLocked(state *resolverBindingState, callerKey string, now time.Time) {
	if _, exists := state.callers[callerKey]; exists || len(state.callers) < maxResolverCallerEntries {
		return
	}
	oldestKey := ""
	var oldestTime time.Time
	for key, caller := range state.callers {
		if oldestKey == "" || caller.lastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = caller.lastUsed
		}
	}
	if oldestKey == "" {
		return
	}
	evicted := state.callers[oldestKey]
	delete(state.callers, oldestKey)
	if !state.agentHasOtherCallerLocked(evicted.instanceID) {
		delete(state.agents, evicted.instanceID)
	}
}

func (r *Resolver) pruneUnavailableAgentsLocked(
	state *resolverBindingState,
	candidates []*ResolvedAgent,
) {
	available := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		available[candidate.InstanceID] = struct{}{}
		if _, cached := state.agents[candidate.InstanceID]; cached {
			state.agents[candidate.InstanceID] = *candidate
		}
	}
	for id := range state.agents {
		if _, ok := available[id]; !ok {
			delete(state.agents, id)
		}
	}
	for key, caller := range state.callers {
		if _, ok := available[caller.instanceID]; !ok {
			delete(state.callers, key)
		}
	}
}

func (state *resolverBindingState) agentHasOtherCallerLocked(instanceID string) bool {
	for _, caller := range state.callers {
		if caller.instanceID == instanceID {
			return true
		}
	}
	return false
}

func cloneResolvedAgent(agent ResolvedAgent) (ResolvedAgent, error) {
	metadata := make(map[string]string, len(agent.Metadata))
	for key, value := range agent.Metadata {
		metadata[key] = value
	}
	agent.Metadata = metadata
	card, err := agentcard.CloneAgentCard(agent.Card)
	if err != nil {
		return ResolvedAgent{}, err
	}
	agent.Card = card
	return agent, nil
}
