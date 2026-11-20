/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package harness

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Eventually retries an assertion until it succeeds or the timeout expires.
func Eventually(
	ctx context.Context,
	timeout time.Duration,
	interval time.Duration,
	fn func() error,
) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		if err := fn(); err == nil {
			return nil
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("condition was not met within %s: %w", timeout, last)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("condition canceled: %w", last)
		case <-time.After(interval):
		}
	}
}

// KeepIfRequested blocks after a successful case when POLARIS_E2E_KEEP=1.
// While it blocks, the test does not return, so t.Cleanup does not run and the
// in-process server, instance heartbeat, and child provider processes remain
// alive for manual inspection.
func KeepIfRequested(t *testing.T) {
	if strings.TrimSpace(os.Getenv("POLARIS_E2E_KEEP")) != "1" {
		return
	}
	t.Helper()
	runID := strings.TrimSpace(os.Getenv("POLARIS_E2E_RUN_ID"))
	if readyFile := strings.TrimSpace(os.Getenv("POLARIS_E2E_KEEP_READY_FILE")); readyFile != "" {
		if err := os.WriteFile(readyFile, []byte(runID+"\n"), 0o600); err != nil {
			t.Fatalf("write KEEP ready file: %v", err)
		}
	}
	_, _ = fmt.Fprintf(os.Stdout,
		"KEEP: assertions passed; run_id=%s; waiting for cleanup signal\n", runID)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	sig := <-signals
	_, _ = fmt.Fprintf(os.Stdout, "KEEP: received %s; running test cleanup\n", sig)
}
