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
	"fmt"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	adapteragentapi "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentapi"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/serviceadmin"
)

const examplesOwner = "trpc-agent-naming-polarismesh-examples"

// ConsoleAddress returns the Console HTTP origin for a Polaris host.
func ConsoleAddress(server string, port int) string {
	return fmt.Sprintf("http://%s:%d", strings.TrimSpace(server), port)
}

// NewAgentAPI returns the Console-backed Agent service API used by provider
// examples. It lets Runtime.Prepare create the AI_AGENT service automatically.
func NewAgentAPI(
	sdk api.SDKContext,
	server string,
	consolePort int,
	timeout time.Duration,
) adapteragentapi.AgentAPI {
	return adapteragentapi.New(
		sdk,
		adapteragentapi.WithConsole(
			[]string{ConsoleAddress(server, consolePort)},
			firstEnvironment("POLARIS_CONSOLE_TOKEN", "POLARIS_TOKEN"),
			timeout,
		),
	)
}

// EnsureMCPService creates the MCP_SERVER service used by the MCP example.
// The returned client owns idle HTTP connections and must be closed.
func EnsureMCPService(
	namespace, service, server string,
	consolePort int,
	timeout time.Duration,
) (*serviceadmin.Client, error) {
	cfg := &serviceadmin.ServiceWriterConfigImpl{
		Addresses: []string{ConsoleAddress(server, consolePort)},
		Token:     firstEnvironment("POLARIS_CONSOLE_TOKEN", "POLARIS_TOKEN"),
		Timeout:   &timeout,
	}
	cfg.SetDefault()
	if err := cfg.Verify(); err != nil {
		return nil, fmt.Errorf("configure Console client: %w", err)
	}
	client := serviceadmin.New(cfg)
	metadata := map[string]string{OwnerLabel: examplesOwner}
	if _, err := client.EnsureService(&serviceadmin.EnsureServiceRequest{
		ServiceWriteRequest: serviceadmin.ServiceWriteRequest{
			Namespace: namespace,
			Service:   service,
			Type:      model.ServiceTypeMCPServer,
			Metadata:  &metadata,
		},
	}); err != nil {
		client.Close()
		return nil, fmt.Errorf("ensure MCP service %s/%s: %w", namespace, service, err)
	}
	return client, nil
}
