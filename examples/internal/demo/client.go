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
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
)

// RemoteClientOptions configures one Polaris-discovered A2A client.
type RemoteClientOptions struct {
	PolarisAddress string
	Namespace      string
	Service        string
	CallerKey      string
	Binding        string
	Message        string
	Stream         bool
	Interactive    bool
	Timeout        time.Duration
}

// RunRemoteChat discovers an A2A agent through Polaris and sends one message
// or starts an interactive session.
func RunRemoteChat(ctx context.Context, options RemoteClientOptions) error {
	sdk, err := NewSDK(options.PolarisAddress, "", false, options.Timeout)
	if err != nil {
		return fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()

	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = options.Namespace
	cfg.ServiceName = options.Service
	cfg.AgentName = options.Service
	cfg.RegistryEnabled = false
	cfg.DiscoveryEnabled = true
	cfg.PolarisRequestTimeout = options.Timeout

	runtime, err := adapteragent.New(
		adapteragent.WithConfig(cfg),
		adapteragent.WithSDKContext(sdk),
	)
	if err != nil {
		return fmt.Errorf("create consumer Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare consumer Runtime: %w", err)
	}

	callerKey := options.CallerKey
	if callerKey == "" {
		callerKey = "example-client"
	}
	remote, err := runtime.NewRemoteAgentWithBinding(
		ctx,
		callerKey,
		options.Binding,
		ClientFactory(options.Timeout, options.Stream),
	)
	if err != nil {
		return fmt.Errorf("discover %s/%s: %w", options.Namespace, options.Service, err)
	}
	return runRemoteMessages(ctx, remote, callerKey, options)
}

func runRemoteMessages(
	ctx context.Context,
	remote agent.Agent,
	callerKey string,
	options RemoteClientOptions,
) error {
	if !options.Interactive {
		return printRemoteResponse(ctx, remote, options.Message)
	}

	fmt.Printf("Connected to Polaris service %s/%s as caller %q\n",
		options.Namespace, options.Service, callerKey)
	fmt.Println("Commands: /new starts a new caller key, /exit quits.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		message := strings.TrimSpace(scanner.Text())
		switch strings.ToLower(message) {
		case "":
			continue
		case "/exit", "exit":
			return nil
		case "/new", "new":
			fmt.Println("Start a new caller key by restarting the client.")
			continue
		}
		if err := printRemoteResponse(ctx, remote, message); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	return scanner.Err()
}

func printRemoteResponse(ctx context.Context, remote agent.Agent, message string) error {
	events, err := remote.Run(ctx, Invocation(message))
	if err != nil {
		return err
	}
	wrote := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt, ok := <-events:
			if !ok {
				if !wrote {
					return errors.New("remote agent returned no text")
				}
				fmt.Println()
				return nil
			}
			if evt == nil || evt.Response == nil {
				continue
			}
			if evt.Error != nil {
				return errors.New(evt.Error.Message)
			}
			for _, choice := range evt.Choices {
				text := choice.Message.Content
				if evt.IsPartial {
					text = choice.Delta.Content
				}
				if text == "" {
					continue
				}
				if !wrote {
					fmt.Print("Assistant: ")
					wrote = true
				}
				fmt.Print(text)
			}
			if evt.IsFinalResponse() {
				fmt.Println()
				return nil
			}
		}
	}
}
