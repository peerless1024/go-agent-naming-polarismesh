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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func clearOptionEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"POLARIS_SERVER", "POLARIS_SERVER_ADDRESS", "POLARIS_SKILL_ADDRESS",
		"POLARIS_CONSOLE_ADDRESS", "POLARIS_E2E_CONFIG", "POLARIS_E2E_NAMESPACE", "POLARIS_E2E_RESOURCES"} {
		t.Setenv(name, "")
	}
}

func TestParseOptionsReturnsNormalizedValues(t *testing.T) {
	clearOptionEnvironment(t)
	root := t.TempDir()
	o, err := parseOptions("verify", []string{"--resources=keep", "--provider-binary=/tmp/provider",
		"--server=127.0.0.1", "--run-id=normalized-run", "--run-dir=" + filepath.Join(root, "normalized-run")})
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8091", o.Discover)
	require.Equal(t, "127.0.0.1:8094", o.SkillAddress)
	require.Equal(t, "http://127.0.0.1:8090", o.Console)
	require.True(t, filepath.IsAbs(o.RunDir))
	require.Equal(t, DefaultNamespace, o.Namespace)
}

func TestParseOptionsRejectsUnsafeModesAndArguments(t *testing.T) {
	clearOptionEnvironment(t)
	for _, args := range [][]string{
		{"--resources=keep", "--provider-binary=/tmp/provider", "--dry-run"},
		{"--resources=keep", "--provider-binary=/tmp/provider", "--apply"},
		{"--resources=keep", "--provider-binary=/tmp/provider", "--artifacts"},
		{"--resources=keep", "--provider-binary=/tmp/provider", "--delete-services"},
		{"--resources=keep", "--provider-binary=/tmp/provider", "--namespace=default"},
		{"--resources=keep", "--provider-binary=/tmp/provider", "--run-id=../unowned"},
		{"--resources=keep", "--provider-binary=/tmp/provider", "unexpected"},
		{"--provider-binary=/tmp/provider"},
	} {
		_, err := parseOptions("verify", args)
		require.Error(t, err, "%v", args)
	}
	o, err := parseOptions("cleanup", []string{"--state=/tmp/state.json", "--apply", "--dry-run"})
	require.NoError(t, err)
	require.True(t, o.Apply)
	require.True(t, o.DryRun)
}

func TestParseOptionsAllowsCleanupEmptyServicesWithoutState(t *testing.T) {
	clearOptionEnvironment(t)
	o, err := parseOptions("cleanup", []string{"--delete-services", "--force", "--dry-run",
		"--server=127.0.0.1", "--namespace=" + DefaultNamespace})
	require.NoError(t, err)
	require.True(t, o.DeleteServices)
	require.True(t, o.Apply)
	require.True(t, o.DryRun)
	require.Equal(t, "http://127.0.0.1:8090", o.Console)
	o, err = parseOptions("cleanup", []string{"--delete-service", "--force", "--dry-run",
		"--server=127.0.0.1", "--namespace=" + DefaultNamespace})
	require.NoError(t, err)
	require.True(t, o.DeleteServices)

	t.Setenv("POLARIS_SERVER", "polaris.example.test")
	o, err = parseOptions("cleanup", []string{"--delete-services", "--dry-run"})
	require.NoError(t, err)
	require.Equal(t, "http://polaris.example.test:8090", o.Console)
	t.Setenv("POLARIS_SERVER", "")

	_, err = parseOptions("verify", []string{"--resources=keep", "--provider-binary=/tmp/provider"})
	require.ErrorContains(t, err, "server must be a host")

	_, err = parseOptions("cleanup", []string{"--delete-services", "--namespace=default"})
	require.Error(t, err)
	_, err = parseOptions("cleanup", []string{"--artifacts", "--delete-services"})
	require.Error(t, err)
}

func TestOptionsRejectAccessBearingAddresses(t *testing.T) {
	for _, value := range []string{"http://user:synthetic-secret@127.0.0.1:8090", "http://127.0.0.1:8090?",
		"http://127.0.0.1:8090/?token=synthetic-secret", "http://127.0.0.1:8090/a2a", "ftp://127.0.0.1"} {
		err := ValidateConsole(value)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "synthetic-secret")
	}
	for _, value := range []string{"user@127.0.0.1:8091", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:abc"} {
		o := Options{Mode: "consumer", Namespace: DefaultNamespace, Server: "polaris.example.test", Discover: value,
			Timeout: time.Second, RunTimeout: time.Minute, Service: "service", RunID: "run"}
		require.Error(t, o.Validate(), value)
	}
	for _, value := range []string{"../run", strings.Repeat("x", 33)} {
		o := Options{Mode: "consumer", Namespace: DefaultNamespace, Server: "polaris.example.test",
			Timeout: time.Second, RunTimeout: time.Minute, Service: "service", RunID: value}
		require.Error(t, o.Validate())
	}
}

func TestParseOptionsCannotOverrideExecutableMode(t *testing.T) {
	clearOptionEnvironment(t)
	for _, mode := range []string{"provider", "consumer"} {
		_, err := parseOptions(mode, []string{"--mode=cleanup", "--state=/tmp/state.json", "--apply", "--dry-run"})
		require.Error(t, err, mode)
	}
	for _, mode := range []string{"provider", "consumer"} {
		_, err := parseOptions("verify", []string{"--mode=" + mode, "--service=test", "--ready=/tmp/ready.json"})
		require.Error(t, err, mode)
	}
	_, err := parseOptions("verify", []string{"--mode=cleanup", "--state=/tmp/state.json", "--dry-run"})
	require.NoError(t, err)
}
