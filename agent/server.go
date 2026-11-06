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
	"trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	adapterserver "trpc.group/trpc-go/trpc-agent-naming-polarismesh/server/a2a"
)

// NewA2AServer builds an A2A v1 server with the Runtime AgentCard.
func (r *Runtime) NewA2AServer(
	agentRunner runner.Runner,
	options ...a2aserver.Option,
) (*server.A2AServer, error) {
	return adapterserver.NewA2AServer(r, agentRunner, options...)
}
