/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main calls registered Agent A, which calls versioned Agent B instances.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
)

type result struct {
	Success  bool   `json:"success"`
	Events   int    `json:"events"`
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

var (
	server          string
	grpcPort        int
	namespace       string
	serviceName     string
	caller          string
	protocolBinding string
	targetVersions  string
	timeout         time.Duration
	debug           bool
)

func main() {
	initArgs()
	flag.Parse()
	if server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
	}
	demo.ConfigureLogging(debug)
	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] enable debug logs: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := run(ctx)
	if err != nil {
		log.Fatal(err)
	}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
	if !output.Success {
		log.Fatalf("a2a-agentcard call failed: %s", output.Error)
	}
}

func run(ctx context.Context) (result, error) {
	discover := net.JoinHostPort(server, fmt.Sprint(grpcPort))
	sdk, err := demo.NewSDK(discover, "", false, timeout)
	if err != nil {
		return result{}, fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()

	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = serviceName
	cfg.PolarisRequestTimeout = timeout
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(sdk))
	if err != nil {
		return result{}, fmt.Errorf("create Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return result{}, fmt.Errorf("prepare Runtime: %w", err)
	}

	remote, err := runtime.NewRemoteAgentWithBinding(
		ctx, caller, protocolBinding, demo.ClientFactory(timeout, false),
	)
	if err != nil {
		return result{}, fmt.Errorf("create Agent A remote agent: %w", err)
	}
	text, events, callErr := demo.Invoke(ctx, remote, "fanout")
	output := result{Events: events, Response: text}
	if callErr != nil {
		output.Error = callErr.Error()
		return output, nil
	}
	output.Success = strings.Contains(text, "|fanout|")
	for _, version := range strings.Split(targetVersions, ",") {
		version = strings.TrimSpace(version)
		if version != "" {
			output.Success = output.Success && strings.Contains(text, "b="+version+":")
		}
	}
	if !output.Success && output.Error == "" {
		output.Error = "Agent A did not report every required Agent B version"
	}
	return output, nil
}

func initArgs() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&grpcPort, "polaris-port", 8091, "Polaris gRPC port")
	flag.StringVar(&namespace, "namespace", "go-agent-naming-e2e", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.demo.agent.a", "Agent A service name")
	flag.StringVar(&caller, "caller", "a2a-agentcard-client", "Sticky caller key")
	flag.StringVar(&protocolBinding, "protocol-binding", "JSONRPC", "A2A interface protocol binding")
	flag.StringVar(&targetVersions, "target-versions", "v1.0.0,v2.0.0",
		"Comma-separated Agent B versions required in the response")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Polaris operation timeout")
	flag.BoolVar(&debug, "debug", false, "enable Polaris and Agent debug logs")
}
