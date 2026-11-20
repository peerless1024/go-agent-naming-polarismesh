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
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/stateless"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
)

// ProviderOptions identifies the Polaris instance registered for an A2A
// provider.
type ProviderOptions struct {
	Namespace        string
	Service          string
	Version          string
	Host             string
	RegistrationPort int
	TTL              time.Duration
}

// ServeA2A starts a real A2A HTTP server, registers the provider through
// go-naming-polarismesh, and shuts both down when ctx is canceled.
func ServeA2A(
	ctx context.Context,
	sdk api.SDKContext,
	runtime *adapteragent.Runtime,
	agentRunner runner.Runner,
	listener net.Listener,
	options ProviderOptions,
) error {
	var manager *stateless.TaskManager
	server, err := runtime.NewA2AServer(
		agentRunner,
		a2aserver.WithTaskManagerBuilder(
			func(processor taskmanager.MessageProcessor) (taskmanager.TaskManager, error) {
				var buildErr error
				manager, buildErr = stateless.NewTaskManager(processor)
				return manager, buildErr
			},
		),
	)
	if err != nil {
		return fmt.Errorf("create A2A server: %w", err)
	}
	if manager == nil {
		return errors.New("A2A TaskManager was not created")
	}
	defer func() { _ = manager.Close() }()

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()

	registry, err := RegisterProvider(
		sdk,
		options.Namespace,
		options.Service,
		options.Version,
		options.Host,
		options.RegistrationPort,
		options.TTL,
	)
	if err != nil {
		_ = httpServer.Close()
		return fmt.Errorf("register A2A provider: %w", err)
	}
	defer func() { _ = registry.Deregister(options.Service) }()

	select {
	case <-ctx.Done():
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}
