/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	protocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/stateless"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	trpcregistry "trpc.group/trpc-go/trpc-go/naming/registry"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

// Ready is a token-free record for one real provider process.
type Ready struct {
	RunID, Namespace, Service, Node, Host, URL, InstanceID string
	Port, PID                                              int
	BorrowedContextAlive, CloseIdempotent, ProviderRemoved bool
	Stopped                                                bool
}

// Stats lets E2E check cancellation and auth without exposing any key value.
type Stats struct {
	Active, Completed, Canceled, Authenticated, Denied int64
	TenantAccepted, TenantRejected                     int64
}

type providerStats struct {
	active, completed, canceled, authenticated, denied atomic.Int64
	tenantAccepted, tenantRejected                     atomic.Int64
}

func (s *providerStats) snapshot() Stats {
	return Stats{Active: s.active.Load(), Completed: s.completed.Load(), Canceled: s.canceled.Load(),
		Authenticated: s.authenticated.Load(), Denied: s.denied.Load(),
		TenantAccepted: s.tenantAccepted.Load(), TenantRejected: s.tenantRejected.Load()}
}

type tenantProcessor struct {
	next   taskmanager.MessageProcessor
	stats  *providerStats
	tenant string
}

func (p tenantProcessor) ProcessMessage(ctx context.Context, invocation *taskmanager.ExecContext) (<-chan protocol.StreamEvent, error) {
	if invocation == nil || invocation.Tenant != p.tenant {
		p.stats.tenantRejected.Add(1)
		return nil, errors.New("A2A request tenant does not match the advertised interface")
	}
	p.stats.tenantAccepted.Add(1)
	return p.next.ProcessMessage(ctx, invocation)
}

type fixtureAuth struct{ stats *providerStats }

func (m fixtureAuth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(FixtureKeyHeader) != FixtureAPIKey {
			m.stats.denied.Add(1)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		m.stats.authenticated.Add(1)
		next.ServeHTTP(w, r)
	})
}

type deterministicAgent struct {
	name, node string
	repository agentskill.Repository
	stats      *providerStats
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	closed     bool
}

func (a *deterministicAgent) Info() agent.Info {
	return agent.Info{Name: a.name, Description: "Deterministic real E2E provider"}
}
func (*deterministicAgent) Tools() []tool.Tool              { return nil }
func (*deterministicAgent) SubAgents() []agent.Agent        { return nil }
func (*deterministicAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *deterministicAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if invocation == nil {
		return nil, errors.New("invocation is required")
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, errors.New("provider Agent is closed")
	}
	a.wg.Add(1)
	a.mu.Unlock()
	started := false
	defer func() {
		if !started {
			a.wg.Done()
		}
	}()
	text := a.node + "|echo:" + invocation.Message.Content
	if invocation.Message.Content == "skill" {
		if a.repository == nil || len(a.repository.Summaries()) == 0 {
			return nil, errors.New("provider has no installed Skill")
		}
		skill, err := a.repository.Get(a.repository.Summaries()[0].Name)
		if err != nil {
			return nil, err
		}
		text = a.node + "|" + skill.Summary.Name + "|" + skill.Body
	}
	out := make(chan *event.Event, 1)
	started = true
	go func() {
		defer a.wg.Done()
		defer close(out)
		a.stats.active.Add(1)
		defer a.stats.active.Add(-1)
		send := func(evt *event.Event) bool {
			select {
			case out <- evt:
				return true
			case <-ctx.Done():
				a.stats.canceled.Add(1)
				return false
			case <-a.ctx.Done():
				return false
			}
		}
		if invocation.Message.Content == "slow" {
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			deadline := time.NewTimer(20 * time.Second)
			defer deadline.Stop()
			for {
				select {
				case <-ctx.Done():
					a.stats.canceled.Add(1)
					return
				case <-a.ctx.Done():
					return
				case <-deadline.C:
					return
				case <-ticker.C:
					if !send(event.NewResponseEvent(invocation.InvocationID, a.name, &agentmodel.Response{
						ID: invocation.InvocationID, IsPartial: true,
						Choices: []agentmodel.Choice{{Delta: agentmodel.NewAssistantMessage(a.node + "|tick")}},
					})) {
						return
					}
				}
			}
		}
		runes := []rune(text)
		cut := len(runes) / 2
		for _, part := range []string{string(runes[:cut]), string(runes[cut:])} {
			if !send(event.NewResponseEvent(invocation.InvocationID, a.name, &agentmodel.Response{
				ID: invocation.InvocationID, IsPartial: true,
				Choices: []agentmodel.Choice{{Delta: agentmodel.NewAssistantMessage(part)}},
			})) {
				return
			}
		}
		if send(event.NewResponseEvent(invocation.InvocationID, a.name, &agentmodel.Response{
			Object: agentmodel.ObjectTypeRunnerCompletion, Done: true,
		})) {
			a.stats.completed.Add(1)
		}
	}()
	return out, nil
}

func (a *deterministicAgent) Close() error {
	a.mu.Lock()
	a.closed = true
	a.cancel()
	a.mu.Unlock()
	a.wg.Wait()
	return nil
}

// ServeProvider uses the real go-naming registry, metadata hook, SDK and A2A server.
func ServeProvider(ctx context.Context, o Options) (result error) {
	var finalReady *Ready
	defer func() {
		if finalReady != nil {
			finalReady.Stopped = result == nil
			result = errors.Join(result, WriteJSON(o.ReadyFile, finalReady))
		}
	}()
	sdk, err := NewSDK(o, o.SkillName != "")
	if err != nil {
		return err
	}
	defer sdk.Destroy()
	listener, err := net.Listen("tcp", net.JoinHostPort(o.Host, "0"))
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace, cfg.ServiceName, cfg.AgentName = o.Namespace, o.Service, o.Service
	cfg.AgentVersion, cfg.AdvertiseHost, cfg.AdvertisePort = "1.0.0", o.Host, port
	cfg.A2APath, cfg.Streaming, cfg.PolarisRequestTimeout = o.Path, true, o.Timeout
	if o.SkillName != "" {
		cfg.SkillRefs = []adapterconfig.SkillRef{{Namespace: o.Namespace, Name: o.SkillName, Version: o.SkillVersion}}
		cfg.SkillCacheDir = filepath.Join(o.RunDir, "skill-cache")
	}
	card, err := agentcard.NewAgentCard(cfg)
	if err != nil {
		return err
	}
	if len(card.SupportedInterfaces) > 0 {
		card.SupportedInterfaces[0].Tenant = o.Tenant
	}
	registryConfig := &polarisregistry.Config{Namespace: o.Namespace, ServiceName: o.Service, Version: cfg.AgentVersion,
		EnableRegister: true, Protocol: "http", HeartBeat: 1, TTL: 2,
		ServiceToken: Environment("POLARIS_SERVICE_TOKEN", "POLARIS_TOKEN"),
		Metadata:     map[string]string{OwnerLabel: Owner, RunLabel: o.RunID, "e2e.node": o.Node}}
	reg, err := polarisregistry.NewRegistry(api.NewProviderAPIByContext(sdk), registryConfig)
	if err != nil {
		return err
	}
	trpcregistry.Register(o.Service, reg)
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithAgentCard(card))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, runtime.Close(context.Background())) }()
	if err := runtime.Prepare(ctx); err != nil {
		return err
	}
	if err := runtime.Prepare(ctx); err != nil {
		return err
	}
	if runtime.SDKContext() != sdk && o.SkillName != "" {
		return errors.New("provider did not reuse framework SDK context")
	}
	repository, err := runtime.SkillRepository()
	if err != nil {
		return err
	}
	stats := &providerStats{}
	agentCtx, cancel := context.WithCancel(context.Background())
	localAgent := &deterministicAgent{name: o.Service, node: o.Node, repository: repository, stats: stats, ctx: agentCtx, cancel: cancel}
	defer func() { result = errors.Join(result, localAgent.Close()) }()
	localRunner := runner.NewRunner(o.Service, localAgent)
	defer func() { result = errors.Join(result, localRunner.Close()) }()
	var manager *stateless.TaskManager
	server, err := runtime.NewA2AServer(localRunner,
		a2aserver.WithTaskManagerBuilder(func(processor taskmanager.MessageProcessor) (taskmanager.TaskManager, error) {
			var err error
			manager, err = stateless.NewTaskManager(tenantProcessor{next: processor, stats: stats, tenant: o.Tenant})
			return manager, err
		}),
		a2aserver.WithExtraA2AOptions(protocolserver.WithMiddleware(fixtureAuth{stats: stats})),
	)
	// Handler is hosted by our HTTP server; the upstream Server was never started.
	if manager != nil {
		defer func() { result = errors.Join(result, manager.Close()) }()
	}
	if err != nil {
		return err
	}
	if o.MetadataMode != "valid" {
		if err := runtime.Close(ctx); err != nil {
			return err
		}
		metadata, err := agentcard.BuildAgentInstanceMetadata(cfg, card)
		if err != nil {
			return err
		}
		switch o.MetadataMode {
		case "missing":
			delete(metadata, agentcard.MetadataAgentCard)
		case "mismatch":
			card.SupportedInterfaces[0].URL = "http://" + net.JoinHostPort(o.Host, fmt.Sprint(port+1)) + o.Path
			raw, err := json.Marshal(card)
			if err != nil {
				return err
			}
			metadata[agentcard.MetadataAgentCard] = string(raw)
		default:
			return errors.New("unknown metadata fixture mode")
		}
		if err := polarisregistry.RegisterMetadataProvider(o.Service, polarisregistry.MetadataProviderFunc(
			func() (map[string]string, error) { return metadata, nil },
		)); err != nil {
			return err
		}
		defer polarisregistry.UnregisterMetadataProvider(o.Service)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/e2e/stats", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(stats.snapshot()) })
	mux.Handle("/", server.Handler())
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()
	defer func() {
		result = errors.Join(result, manager.Close())
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result = errors.Join(result, httpServer.Shutdown(shutdown))
		if result != nil {
			_ = httpServer.Close()
		}
	}()
	if err := reg.Register(o.Service, trpcregistry.WithAddress(net.JoinHostPort(o.Host, fmt.Sprint(port)))); err != nil {
		return err
	}
	defer func() { result = errors.Join(result, reg.Deregister(o.Service)) }()
	ready := Ready{RunID: o.RunID, Namespace: o.Namespace, Service: o.Service, Node: o.Node, Host: o.Host,
		Port: port, URL: card.PrimaryURL(), InstanceID: registryConfig.InstanceID, PID: processID()}
	finalReady = &ready
	if err := WriteJSON(o.ReadyFile, ready); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
	}
	result = errors.Join(result, runtime.Close(context.Background()))
	if o.MetadataMode != "valid" {
		polarisregistry.UnregisterMetadataProvider(o.Service)
	}
	ready.CloseIdempotent = runtime.Close(context.Background()) == nil
	ready.BorrowedContextAlive = !sdk.IsDestroyed()
	ready.ProviderRemoved = polarisregistry.RegisterMetadataProvider(o.Service,
		polarisregistry.MetadataProviderFunc(func() (map[string]string, error) { return nil, nil })) == nil
	polarisregistry.UnregisterMetadataProvider(o.Service)
	if !ready.CloseIdempotent || !ready.BorrowedContextAlive || !ready.ProviderRemoved {
		result = errors.Join(result, errors.New("provider lifecycle ownership check failed"))
	}
	return result
}

// ValidateAIService checks service type without treating ordinary services as agents.
func ValidateAIService(service *model.AgentService) error {
	if service == nil || service.Type != model.ServiceTypeAIAgent {
		return errors.New("expected a complete AI_AGENT service")
	}
	return nil
}
