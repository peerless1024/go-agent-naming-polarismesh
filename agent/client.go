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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/naming"
)

// NewRemoteAgent discovers one A2A Agent for the runtime service.
func (r *Runtime) NewRemoteAgent(
	ctx context.Context,
	callerKey string,
	factories ...naming.RemoteAgentOptionsFactory,
) (agent.Agent, error) {
	resolver, err := r.Resolver()
	if err != nil {
		return nil, err
	}
	return resolver.NewRemoteAgent(ctx, callerKey, factories...)
}
