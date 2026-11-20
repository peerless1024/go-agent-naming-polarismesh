/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package skill

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-go/log"
)

// ReloadFunc rebuilds a repository without mutating the current snapshot.
type ReloadFunc func(context.Context) (agentskill.Repository, error)

// PeriodicRepository atomically swaps complete Skill repository snapshots.
// Reload failures keep the previous snapshot available.
type PeriodicRepository struct {
	mu      sync.RWMutex
	current agentskill.Repository

	interval time.Duration
	timeout  time.Duration
	reload   ReloadFunc

	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	closeDone   chan struct{}
	refreshGate chan struct{}
	refreshes   sync.WaitGroup
	closed      bool
}

// NewPeriodicRepository wraps initial with a bounded periodic reload loop.
func NewPeriodicRepository(
	parent context.Context,
	initial agentskill.Repository,
	interval time.Duration,
	timeout time.Duration,
	reload ReloadFunc,
) *PeriodicRepository {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	repository := &PeriodicRepository{
		current:   initial,
		interval:  interval,
		timeout:   timeout,
		reload:    reload,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		closeDone: make(chan struct{}),
	}
	repository.refreshGate = make(chan struct{}, 1)
	repository.refreshGate <- struct{}{}
	if interval <= 0 || reload == nil {
		close(repository.done)
		return repository
	}
	go repository.run()
	return repository
}

// Summaries returns the current complete snapshot.
func (r *PeriodicRepository) Summaries() []agentskill.Summary {
	repository := r.snapshot()
	if repository == nil {
		return []agentskill.Summary{}
	}
	return repository.Summaries()
}

// Get reads one Skill from the current snapshot.
func (r *PeriodicRepository) Get(name string) (*agentskill.Skill, error) {
	repository := r.snapshot()
	if repository == nil {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	return repository.Get(name)
}

// Path returns the current snapshot directory for one Skill.
func (r *PeriodicRepository) Path(name string) (string, error) {
	repository := r.snapshot()
	if repository == nil {
		return "", fmt.Errorf("skill %q not found", name)
	}
	return repository.Path(name)
}

// Roots returns the current snapshot roots when available.
func (r *PeriodicRepository) Roots() []string {
	repository := r.snapshot()
	rooted, ok := repository.(agentskill.RootedRepository)
	if !ok {
		return nil
	}
	return rooted.Roots()
}

// Refresh synchronously reloads and swaps the repository.
func (r *PeriodicRepository) Refresh(ctx context.Context) error {
	if r == nil || r.reload == nil {
		return errors.New("skill repository reload function is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-r.refreshGate:
	}
	defer func() { r.refreshGate <- struct{}{} }()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("skill repository is closed")
	}
	// Admission and Close use the same lock so Wait cannot miss this reload.
	r.refreshes.Add(1)
	r.mu.Unlock()
	defer r.refreshes.Done()

	reloadCtx, cancelReload, stopCallerWatch, err := r.reloadContext(ctx)
	if err != nil {
		return err
	}
	defer stopCallerWatch()
	defer cancelReload()

	next, err := r.reload(reloadCtx)
	if err != nil {
		return err
	}
	if err := refreshContextError(ctx, r.ctx, reloadCtx); err != nil {
		return err
	}
	if next == nil {
		next = emptyRepository{}
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("skill repository is closed")
	}
	if err := refreshContextError(ctx, r.ctx, reloadCtx); err != nil {
		r.mu.Unlock()
		return err
	}
	r.current = next
	r.mu.Unlock()
	return nil
}

func refreshContextError(caller, repository, reload context.Context) error {
	if err := caller.Err(); err != nil {
		return err
	}
	if err := repository.Err(); err != nil {
		return err
	}
	return reload.Err()
}

// Close stops periodic refresh. It does not close SkillAPI or SDKContext.
func (r *PeriodicRepository) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	alreadyClosed := r.closed
	r.closed = true
	r.mu.Unlock()
	if alreadyClosed {
		<-r.closeDone
		return nil
	}
	r.cancel()
	r.refreshes.Wait()
	<-r.done
	close(r.closeDone)
	return nil
}

func (r *PeriodicRepository) reloadContext(
	caller context.Context,
) (context.Context, context.CancelFunc, func(), error) {
	if err := caller.Err(); err != nil {
		return nil, nil, nil, err
	}
	if err := r.ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	mergedCtx, mergeCancel := context.WithCancel(r.ctx)
	stopCallerWatch := context.AfterFunc(caller, mergeCancel)
	reloadCtx := mergedCtx
	cancelReload := mergeCancel
	if r.timeout > 0 {
		reloadCtx, cancelReload = context.WithTimeout(mergedCtx, r.timeout)
	}
	return reloadCtx, cancelReload, func() {
		stopCallerWatch()
		mergeCancel()
	}, nil
}

func (r *PeriodicRepository) run() {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			if err := r.Refresh(r.ctx); err != nil {
				log.Warnf("refresh Polaris Skill repository failed: %v", err)
			}
		}
	}
}

func (r *PeriodicRepository) snapshot() agentskill.Repository {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

type emptyRepository struct{}

func (emptyRepository) Summaries() []agentskill.Summary {
	return []agentskill.Summary{}
}

func (emptyRepository) Get(name string) (*agentskill.Skill, error) {
	return nil, fmt.Errorf("skill %q not found", name)
}

func (emptyRepository) Path(name string) (string, error) {
	return "", fmt.Errorf("skill %q not found", name)
}
