/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main discovers an A2A Agent through Polaris and calls it.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
)

func main() {
	var (
		server      = flag.String("server", os.Getenv("POLARIS_SERVER"), "Polaris host")
		polarisPort = flag.Int("polaris-port", 8091, "Polaris Discovery gRPC port")
		namespace   = flag.String("namespace", "go-agent-naming-examples", "Polaris namespace")
		service     = flag.String("service", "trpc.polaris.examples.a2a", "A2A service name")
		message     = flag.String("message", "What is the weather in Shenzhen?", "Message to send")
		binding     = flag.String("binding", "JSONRPC", "A2A protocol binding")
		stream      = flag.Bool("stream", true, "Enable streaming")
		interactive = flag.Bool("interactive", false, "Start an interactive chat")
		timeout     = flag.Duration("timeout", 30*time.Second, "Request timeout")
	)
	flag.Parse()
	if *server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
	}
	ctx, stop := demo.SignalContext()
	defer stop()
	err := demo.RunRemoteChat(ctx, demo.RemoteClientOptions{
		PolarisAddress: net.JoinHostPort(*server, fmt.Sprint(*polarisPort)),
		Namespace:      *namespace,
		Service:        *service,
		CallerKey:      "a2a-example-client",
		Binding:        *binding,
		Message:        *message,
		Stream:         *stream,
		Interactive:    *interactive,
		Timeout:        *timeout,
	})
	if err != nil {
		log.Fatal(err)
	}
}
