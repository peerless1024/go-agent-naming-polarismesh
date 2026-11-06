/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package main

import (
	"log"
	"os"

	"github.com/polarismesh/polaris-go/api"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/ai-registry/internal/demo"
)

func main() {
	o, err := demo.ParseOptions("provider")
	if err == nil && o.Debug {
		err = api.SetLoggersLevel(api.DebugLog)
	}
	if err == nil {
		ctx, stop := demo.SignalContext()
		err = demo.ServeProvider(ctx, o)
		stop()
	}
	if err != nil {
		log.Printf("FAIL: %v", err)
		os.Exit(1)
	}
}
