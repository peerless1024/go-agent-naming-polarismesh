/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package mcp

import (
	"context"
	"errors"
	"time"
)

// Watcher periodically reconciles a Pool and reports instance deltas.
type Watcher struct {
	Pool     *Pool
	Interval time.Duration
	OnError  func(error)
}

// Watch performs one initial refresh, then polls until ctx is canceled.
func (w *Watcher) Watch(ctx context.Context, onChange func(InstanceEvent)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if w == nil || w.Pool == nil {
		return errors.New("MCP watcher pool is required")
	}
	if w.Interval <= 0 {
		return errors.New("MCP watcher interval must be positive")
	}
	sweep := func() {
		event, err := w.Pool.Refresh(ctx)
		if err != nil {
			if w.OnError != nil {
				w.OnError(err)
			}
		}
		if onChange != nil && (len(event.Added)+len(event.Removed)+len(event.Replaced) > 0) {
			onChange(event)
		}
	}
	sweep()
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			sweep()
		}
	}
}
