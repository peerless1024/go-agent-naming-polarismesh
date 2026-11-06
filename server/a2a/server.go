/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package a2a builds an A2A v1 server from the Polaris adapter runtime.
package a2a

import (
	"errors"
	"net/url"

	a2a "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/a2apath"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
)

// CardProvider supplies the frozen A2A AgentCard.
type CardProvider interface {
	AgentCard() (a2a.AgentCard, error)
}

// NewA2AServer builds an A2A v1 server with the Runtime AgentCard.
func NewA2AServer(
	r CardProvider,
	agentRunner runner.Runner,
	options ...a2aserver.Option,
) (*a2a.A2AServer, error) {
	if r == nil || agentRunner == nil {
		return nil, errors.New("card provider and agent runner are required")
	}
	card, err := r.AgentCard()
	if err != nil {
		return nil, err
	}
	var allOptions []a2aserver.Option
	for _, option := range options {
		if option != nil {
			allOptions = append(allOptions, option)
		}
	}
	allOptions = append(allOptions, a2aserver.WithRunner(agentRunner))
	allOptions = append(allOptions, a2aserver.WithAgentCard(card))
	endpoint, err := url.Parse(card.PrimaryURL())
	if err != nil {
		return nil, errors.New("AgentCard endpoint is invalid")
	}
	if endpoint.RawPath != "" {
		return nil, errors.New("AgentCard endpoint must not contain noncanonical path encoding")
	}
	if err := a2apath.Validate(endpoint.Path); err != nil {
		return nil, err
	}
	// trpc-agent-go appends WithExtraA2AOptions after the top-level card, so
	// inject the frozen card and its exact JSON-RPC path once more. Using the
	// card-derived base path alone would redirect /a2a to /a2a/.
	allOptions = append(
		allOptions,
		a2aserver.WithExtraA2AOptions(
			a2a.WithAgentCard(card),
			a2a.WithV1JSONRPCEnabled(true),
			a2a.WithJSONRPCEndpoint(endpoint.Path),
		),
	)
	server, err := a2aserver.New(allOptions...)
	if err != nil {
		return nil, adaptererrors.WrapError("create A2A server", err)
	}
	return server, nil
}
