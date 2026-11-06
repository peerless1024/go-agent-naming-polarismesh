/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main discovers the Skill-backed Agent and verifies its real content.
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
	server       string
	grpcPort     int
	namespace    string
	serviceName  string
	skillName    string
	skillVersion string
	caller       string
	timeout      time.Duration
	debug        bool
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
	item, err := run(ctx)
	if err != nil {
		log.Fatal(err)
	}
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
	if !item.Success {
		log.Fatal(item.Error)
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

	remote, err := runtime.NewRemoteAgent(ctx, caller, demo.ClientFactory(timeout, false))
	if err != nil {
		return result{}, fmt.Errorf("create remote Agent: %w", err)
	}
	text, events, callErr := demo.Invoke(ctx, remote, "skill")
	item := result{Events: events, Response: text}
	if callErr != nil {
		item.Error = callErr.Error()
		return item, nil
	}
	item.Success = strings.Contains(text, "skill|"+skillName+"|") &&
		strings.Contains(text, "POLARIS_DEMO_SKILL="+skillVersion)
	if !item.Success {
		item.Error = "response does not contain the installed Polaris Skill"
	}
	return item, nil
}

func initArgs() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&grpcPort, "polaris-port", 8091, "Polaris gRPC port")
	flag.StringVar(&namespace, "namespace", "go-naming-polarismesh-skill-demo", "Polaris namespace")
	flag.StringVar(&serviceName, "service", "trpc.polaris.demo.skill", "A2A service name")
	flag.StringVar(&skillName, "skill", "", "Published Skill name")
	flag.StringVar(&skillVersion, "skill-version", "1.0.0", "Published Skill version")
	flag.StringVar(&caller, "caller", "skill-client", "Sticky caller key")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Polaris operation timeout")
	flag.BoolVar(&debug, "debug", false, "enable Polaris debug logs")
}
