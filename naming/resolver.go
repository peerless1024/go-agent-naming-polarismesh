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
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
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
)

// ResolvedAgent is one healthy, validated remote A2A instance.
type ResolvedAgent struct {
	InstanceID string
	Endpoint   string
	Card       a2aprotocolserver.AgentCard
	Metadata   map[string]string
}

// Resolver discovers healthy A2A instances from Polaris.
type Resolver struct {
	namespace string
	service   string
	timeout   time.Duration
	lister    InstanceLister

	mu       sync.RWMutex
	closed   bool
	closedCh chan struct{}
	callers  map[string]resolverCaller
	agents   map[string]ResolvedAgent
	now      func() time.Time

	resolveSequence        uint64
	lastCommittedIteration uint64
	lastCommittedError     error
	latestCandidates       []*ResolvedAgent
	latestSeen             time.Time
}

// ResolverOptions configures a Resolver.
type ResolverOptions struct {
	Namespace string
	Service   string
	Timeout   time.Duration
	Lister    InstanceLister
}

type resolverCaller struct {
	instanceID string
	lastSeen   time.Time
	lastUsed   time.Time
}

// NewResolver creates a Polaris A2A instance resolver.
func NewResolver(options ResolverOptions) *Resolver {
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Second
	}
	return &Resolver{
		namespace: options.Namespace,
		service:   options.Service,
		timeout:   options.Timeout,
		lister:    options.Lister,
		callers:   make(map[string]resolverCaller),
		agents:    make(map[string]ResolvedAgent),
		now:       time.Now,
		closedCh:  make(chan struct{}),
	}
}

// Resolve returns the sticky healthy instance for callerKey.
func (r *Resolver) Resolve(ctx context.Context, callerKey string) (*ResolvedAgent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if callerKey == "" {
		callerKey = "default"
	}
	for attempt := 0; attempt < 3; attempt++ {
		resolved, retry, err := r.resolveOnce(ctx, callerKey)
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
	if state, ok := r.callers[callerKey]; ok {
		stickyID = state.instanceID
	}
	r.mu.RUnlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, false, ErrClosed
	}
	r.resolveSequence++
	sequence := r.resolveSequence
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
		resolved, fallbackErr := r.resolveFallback(sequence, callerKey, err)
		return resolved, false, fallbackErr
	}
	if response == nil {
		return r.commitInvalidResolve(sequence, callerKey,
			fmt.Errorf("%w: polaris returned an empty instance response", ErrNoAvailableInstance))
	}

	candidates := make([]*ResolvedAgent, 0, len(response.GetInstances()))
	var rejected []error
	for _, instance := range response.GetInstances() {
		if instance == nil || !instance.IsHealthy() || instance.IsIsolated() {
			continue
		}
		resolved, err := resolveInstance(instance)
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
			return r.commitInvalidResolve(sequence, callerKey,
				fmt.Errorf("%w: %w", ErrNoAvailableInstance, errors.Join(rejected...)))
		}
		return r.commitInvalidResolve(sequence, callerKey, ErrNoAvailableInstance)
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
	if r.isStaleResolveLocked(sequence) {
		resolved, retry, err := r.recoverCurrentResolveLocked(callerKey)
		r.mu.Unlock()
		return resolved, retry, err
	}
	if err := r.commitSuccessfulResolveLocked(sequence, callerKey, selected); err != nil {
		r.mu.Unlock()
		return nil, false, err
	}
	r.latestCandidates = candidates
	r.latestSeen = r.now()
	r.pruneUnavailableAgentsLocked(candidates)
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
	if ctx == nil {
		ctx = context.Background()
	}
	remote := &dynamicRemoteAgent{
		resolver:  r,
		callerKey: callerKey,
		factories: append([]RemoteAgentOptionsFactory(nil), factories...),
		build:     make(chan struct{}, 1),
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
	r.callers = nil
	r.agents = nil
	r.latestCandidates = nil
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
	state, ok := r.callers[callerKey]
	delete(r.callers, callerKey)
	if ok && !r.agentHasOtherCallerLocked(state.instanceID) {
		delete(r.agents, state.instanceID)
	}
}

type dynamicRemoteAgent struct {
	resolver  *Resolver
	callerKey string
	factories []RemoteAgentOptionsFactory

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
	resolved, err := a.resolver.Resolve(ctx, a.callerKey)
	if err != nil {
		return nil, err
	}
	key, err := resolvedAgentKey(*resolved)
	if err != nil {
		return nil, adaptererrors.WrapError("serialize resolved AgentCard", err)
	}
	a.resolver.mu.RLock()
	current, err := a.resolver.resolvedKeyIsCurrentLocked(ctx, a.callerKey, key)
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
		resolved, err = a.resolver.currentResolvedForBuild(ctx, a.callerKey)
		if err != nil {
			return nil, err
		}
		key, err = resolvedAgentKey(*resolved)
		if err != nil {
			return nil, adaptererrors.WrapError("serialize resolved AgentCard", err)
		}
		a.resolver.mu.RLock()
		current, err = a.resolver.resolvedKeyIsCurrentLocked(ctx, a.callerKey, key)
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
		current, err = a.resolver.resolvedKeyIsCurrentLocked(ctx, a.callerKey, key)
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
	card := resolved.Card
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
	buildOptions = append(buildOptions, a2aagent.WithAgentCard(&card))
	if tenant := selectedInterfaceTenant(card); tenant != "" {
		buildOptions = append(
			buildOptions,
			a2aagent.WithA2AClientExtraOptions(a2aclient.WithTenant(tenant)),
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

func (r *Resolver) currentResolvedForBuild(ctx context.Context, callerKey string) (*ResolvedAgent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if callerKey == "" {
		callerKey = "default"
	}
	r.mu.Lock()
	resolved, retry, err := r.recoverCurrentResolveLocked(callerKey)
	r.mu.Unlock()
	if retry {
		return r.Resolve(ctx, callerKey)
	}
	return resolved, err
}

func (r *Resolver) resolvedKeyIsCurrentLocked(ctx context.Context, callerKey, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r.closed {
		return false, ErrClosed
	}
	if r.lastCommittedError != nil {
		return false, r.lastCommittedError
	}
	if callerKey == "" {
		callerKey = "default"
	}
	state, ok := r.callers[callerKey]
	if !ok {
		return false, nil
	}
	if r.now().Sub(state.lastSeen) > resolverFallbackTTL {
		return false, fmt.Errorf("%w: cached AgentCard expired during client build", ErrNoAvailableInstance)
	}
	for _, candidate := range r.latestCandidates {
		if candidate.InstanceID == state.instanceID {
			currentKey, err := resolvedAgentKey(*candidate)
			return currentKey == key, err
		}
	}
	return false, nil
}

func selectedInterfaceTenant(card a2aprotocolserver.AgentCard) string {
	if len(card.SupportedInterfaces) == 0 {
		return ""
	}
	return strings.TrimSpace(card.SupportedInterfaces[0].Tenant)
}

func resolvedAgentKey(resolved ResolvedAgent) (string, error) {
	cardJSON, err := json.Marshal(resolved.Card)
	if err != nil {
		return "", err
	}
	return resolved.InstanceID + "\n" + resolved.Endpoint + "\n" + string(cardJSON), nil
}

func resolveInstance(instance model.Instance) (*ResolvedAgent, error) {
	metadata := instance.GetMetadata()
	_, card, err := agentcard.ParseAgentInstanceMetadata(metadata)
	if err != nil {
		return nil, adaptererrors.WrapError("parse AgentCard metadata", err)
	}
	endpoint := card.PrimaryURL()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("AgentCard endpoint is invalid")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("AgentCard endpoint must not include credentials, query, or fragment")
	}
	if !strings.EqualFold(parsed.Hostname(), instance.GetHost()) {
		return nil, fmt.Errorf(
			"AgentCard host %q does not match instance host %q",
			parsed.Hostname(),
			instance.GetHost(),
		)
	}
	actualPort := parsed.Port()
	if actualPort == "" {
		switch parsed.Scheme {
		case "http":
			actualPort = "80"
		case "https":
			actualPort = "443"
		}
	}
	expectedPort := fmt.Sprintf("%d", instance.GetPort())
	if actualPort != expectedPort {
		return nil, fmt.Errorf(
			"AgentCard port %q does not match instance port %s",
			actualPort,
			expectedPort,
		)
	}

	copiedMetadata := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copiedMetadata[key] = value
	}
	return &ResolvedAgent{
		InstanceID: instance.GetId(),
		Endpoint:   endpoint,
		Card:       card,
		Metadata:   copiedMetadata,
	}, nil
}

func stableIndex(key string, count int) int {
	if count <= 1 {
		return 0
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(key))
	return int(hasher.Sum64() % uint64(count))
}

func (r *Resolver) resolveFallback(sequence uint64, callerKey string, cause error) (*ResolvedAgent, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrClosed
	}
	if !canFallbackDiscoveryError(cause) {
		if !r.isStaleResolveLocked(sequence) {
			r.lastCommittedIteration = sequence
			r.lastCommittedError = cause
			r.latestCandidates = nil
			r.pruneUnavailableAgentsLocked(nil)
		}
		r.mu.Unlock()
		return nil, cause
	}
	if r.lastCommittedError != nil {
		err := fmt.Errorf("%w: discover A2A instances: %w: %w", ErrNoAvailableInstance, cause, r.lastCommittedError)
		r.mu.Unlock()
		return nil, err
	}
	state, ok := r.callers[callerKey]
	expired := ok && r.now().Sub(state.lastSeen) > resolverFallbackTTL
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: discover A2A instances: %w", ErrNoAvailableInstance, cause)
	}
	if expired {
		delete(r.callers, callerKey)
		if !r.agentHasOtherCallerLocked(state.instanceID) {
			delete(r.agents, state.instanceID)
		}
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: cached AgentCard expired: %w", ErrNoAvailableInstance, cause)
	}
	fallback, hasFallback := r.agents[state.instanceID]
	if hasFallback {
		state.lastUsed = r.now()
		r.callers[callerKey] = state
	}
	r.mu.Unlock()
	if !hasFallback {
		return nil, fmt.Errorf("%w: discover A2A instances: %w", ErrNoAvailableInstance, cause)
	}
	copied, err := cloneResolvedAgent(fallback)
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

func (r *Resolver) recoverCurrentResolveLocked(callerKey string) (*ResolvedAgent, bool, error) {
	if r.closed {
		return nil, false, ErrClosed
	}
	if r.lastCommittedError != nil {
		return nil, false, r.lastCommittedError
	}
	if len(r.latestCandidates) == 0 || r.now().Sub(r.latestSeen) > resolverFallbackTTL {
		return nil, true, nil
	}
	selected := r.latestCandidates[stableIndex(callerKey, len(r.latestCandidates))]
	if state, ok := r.callers[callerKey]; ok {
		for _, candidate := range r.latestCandidates {
			if candidate.InstanceID == state.instanceID {
				selected = candidate
				break
			}
		}
	}
	copied, err := cloneResolvedAgent(*selected)
	if err != nil {
		return nil, false, err
	}
	now := r.now()
	r.evictCallerLocked(callerKey, now)
	r.callers[callerKey] = resolverCaller{
		instanceID: selected.InstanceID,
		lastSeen:   r.latestSeen,
		lastUsed:   now,
	}
	r.agents[selected.InstanceID] = *selected
	return &copied, false, nil
}

func (r *Resolver) isStaleResolveLocked(sequence uint64) bool {
	return sequence <= r.lastCommittedIteration
}

func (r *Resolver) commitInvalidResolve(
	sequence uint64,
	callerKey string,
	cause error,
) (*ResolvedAgent, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, false, ErrClosed
	}
	if r.isStaleResolveLocked(sequence) {
		return r.recoverCurrentResolveLocked(callerKey)
	}
	r.lastCommittedIteration = sequence
	r.lastCommittedError = cause
	r.latestCandidates = nil
	r.pruneUnavailableAgentsLocked(nil)
	return nil, false, cause
}

func (r *Resolver) commitSuccessfulResolveLocked(
	sequence uint64,
	callerKey string,
	selected *ResolvedAgent,
) error {
	now := r.now()
	fallback, err := cloneResolvedAgent(*selected)
	if err != nil {
		return err
	}
	r.lastCommittedIteration = sequence
	r.lastCommittedError = nil
	r.evictCallerLocked(callerKey, now)
	r.callers[callerKey] = resolverCaller{
		instanceID: selected.InstanceID,
		lastSeen:   now,
		lastUsed:   now,
	}
	r.agents[selected.InstanceID] = fallback
	return nil
}

func (r *Resolver) evictCallerLocked(callerKey string, now time.Time) {
	if _, exists := r.callers[callerKey]; exists || len(r.callers) < maxResolverCallerEntries {
		return
	}
	oldestKey := ""
	var oldestTime time.Time
	for key, state := range r.callers {
		if oldestKey == "" || state.lastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = state.lastUsed
		}
	}
	if oldestKey == "" {
		return
	}
	evicted := r.callers[oldestKey]
	delete(r.callers, oldestKey)
	if !r.agentHasOtherCallerLocked(evicted.instanceID) {
		delete(r.agents, evicted.instanceID)
	}
}

func (r *Resolver) pruneUnavailableAgentsLocked(candidates []*ResolvedAgent) {
	available := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		available[candidate.InstanceID] = struct{}{}
		if _, cached := r.agents[candidate.InstanceID]; cached {
			r.agents[candidate.InstanceID] = *candidate
		}
	}
	for id := range r.agents {
		if _, ok := available[id]; !ok {
			delete(r.agents, id)
		}
	}
	for key, state := range r.callers {
		if _, ok := available[state.instanceID]; !ok {
			delete(r.callers, key)
		}
	}
}

func (r *Resolver) agentHasOtherCallerLocked(instanceID string) bool {
	for _, state := range r.callers {
		if state.instanceID == instanceID {
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
