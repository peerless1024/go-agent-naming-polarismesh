/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main discovers the quickstart Agent through Polaris and calls it.
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
	Mode     string `json:"mode"`
	Success  bool   `json:"success"`
	Events   int    `json:"events"`
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

var (
	server      string
	grpcPort    int
	namespace   string
	serviceName string
	caller      string
	timeout     time.Duration
	debug       bool
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
	results, err := run(ctx)
	if err != nil {
		log.Fatal(err)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
	for _, item := range results {
		if !item.Success {
			log.Fatalf("%s call failed: %s", item.Mode, item.Error)
		}
	}
}

func run(ctx context.Context) ([]result, error) {
	discover := net.JoinHostPort(server, fmt.Sprint(grpcPort))
	sdk, err := demo.NewSDK(discover, "", false, timeout)
	if err != nil {
		return nil, fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()

	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = serviceName
	cfg.PolarisRequestTimeout = timeout
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(sdk))
	if err != nil {
		return nil, fmt.Errorf("create Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return nil, fmt.Errorf("prepare Runtime: %w", err)
	}

	results := make([]result, 0, 2)
	for _, mode := range []struct {
		name   string
		stream bool
		input  string
	}{
		{name: "unary", input: "hello"},
		{name: "stream", stream: true, input: "stream"},
	} {
		remote, err := runtime.NewRemoteAgent(ctx, caller+"-"+mode.name,
			demo.ClientFactory(timeout, mode.stream))
		if err != nil {
			return nil, fmt.Errorf("create %s remote agent: %w", mode.name, err)
		}
		text, events, callErr := demo.Invoke(ctx, remote, mode.input)
		item := result{Mode: mode.name, Events: events, Response: text}
		if callErr != nil {
			item.Error = callErr.Error()
		} else if mode.name == "unary" {
			item.Success = strings.Contains(text, "unary:hello")
		} else {
			item.Success = events > 1 && strings.Contains(text, "stream")
		}
		if !item.Success && item.Error == "" {
			item.Error = "response did not match the quickstart contract"
		}
		results = append(results, item)
	}
	return results, nil
}

func initArgs() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&grpcPort, "polaris-port", 8091, "Polaris gRPC port")
	flag.StringVar(&namespace, "namespace", "go-naming-polarismesh-quickstart", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.demo.quickstart", "A2A service name")
	flag.StringVar(&caller, "caller", "quickstart-client", "Sticky caller key")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Polaris operation timeout")
	flag.BoolVar(&debug, "debug", false, "enable Polaris debug logs")
}
