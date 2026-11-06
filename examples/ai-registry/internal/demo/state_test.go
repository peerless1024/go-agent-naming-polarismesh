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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
)

func ownedState(t *testing.T) RunState {
	t.Helper()
	return RunState{Schema: 1, Owner: Owner, RunID: "unit-run", Namespace: DefaultNamespace,
		Console: "http://127.0.0.1:8090", ResourcePolicy: "keep", RunDir: filepath.Join(t.TempDir(), "unit-run"),
		Services: []string{ServicePrefix + "unit-run-test"}, Skills: []string{SkillPrefix + "unit-run-test"}}
}

func TestStateAtomicPermissionsAndLocation(t *testing.T) {
	state := ownedState(t)
	path := filepath.Join(state.RunDir, "state.json")
	require.NoError(t, WriteJSON(path, state))
	loaded, err := LoadState(path)
	require.NoError(t, err)
	require.Equal(t, state, *loaded)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	entries, err := os.ReadDir(state.RunDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	elsewhere := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, WriteJSON(elsewhere, state))
	_, err = LoadState(elsewhere)
	require.ErrorContains(t, err, "owned run directory")
}

func TestStateRejectsUnownedTargets(t *testing.T) {
	state := ownedState(t)
	for _, change := range []func(*RunState){
		func(s *RunState) { s.Namespace = "default" },
		func(s *RunState) { s.Services = []string{"production"} },
		func(s *RunState) { s.Skills = []string{"production"} },
		func(s *RunState) { s.RunDir = "/" },
		func(s *RunState) { s.Owner = "another-owner" },
		func(s *RunState) { s.Console = "http://user:synthetic-secret@localhost" },
		func(s *RunState) { s.Processes = []ProcessState{{PID: 1}} },
	} {
		bad := state
		change(&bad)
		err := bad.Validate()
		require.Error(t, err)
		require.NotContains(t, err.Error(), "synthetic-secret")
	}
	require.False(t, pathWithin(state.RunDir, filepath.Join(state.RunDir, "..", "outside")))
	require.False(t, hasArgument("/tmp/provider --run-id=unit-run-more", "--run-id=unit-run"))
}

func TestStopProviderRejectsCurrentProcessWithWrongIdentity(t *testing.T) {
	start, command, exists, err := processIdentity(os.Getpid())
	require.NoError(t, err)
	require.True(t, exists)
	err = StopProvider(context.Background(), ProcessState{PID: os.Getpid(), StartTime: start + "-different",
		Command: command, Binary: "/tmp/provider"})
	require.ErrorContains(t, err, "refuse to signal")
	require.ErrorIs(t, err, ErrProviderIdentityChanged)
	require.Error(t, StopProvider(context.Background(), ProcessState{PID: 0}))
}

func TestCleanupDryRunMakesNoRemoteCalls(t *testing.T) {
	state := ownedState(t)
	state.Console = "http://127.0.0.1:1"
	path := filepath.Join(state.RunDir, "state.json")
	require.NoError(t, WriteJSON(path, state))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, Cleanup(context.Background(), Options{StateFile: path, Apply: true, DryRun: true}))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestCleanupStateWithoutDeleteServiceStopsLocallyOnly(t *testing.T) {
	state := ownedState(t)
	state.Console = "http://127.0.0.1:1"
	path := filepath.Join(state.RunDir, "state.json")
	require.NoError(t, WriteJSON(path, state))
	require.NoError(t, Cleanup(context.Background(), Options{
		StateFile: path,
		Apply:     true,
	}))
	loaded, err := LoadState(path)
	require.NoError(t, err)
	require.False(t, loaded.Cleaned)
}

func TestEmptyOwnedServiceSelection(t *testing.T) {
	services := []ServiceView{
		{Name: ServicePrefix + "run-owned", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: Owner}},
		{Name: ServicePrefix + "run-running", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: Owner}, TotalInstanceCount: 1},
		{Name: ServicePrefix + "run-other-owner", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: "other"}},
		{Name: "production-service", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: Owner}},
		{Name: ServicePrefix + "run-other-namespace", Namespace: "other",
			Metadata: map[string]string{OwnerLabel: Owner}},
	}
	require.Equal(t, []string{ServicePrefix + "run-owned"}, filterOwnedEmptyServiceNames(DefaultNamespace, services))
}

func TestCleanupEmptyServicesRequiresFreshOwnershipAndEmptyInstances(t *testing.T) {
	services := []ServiceView{
		{Name: ServicePrefix + "run-empty", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: Owner}},
		{Name: ServicePrefix + "run-running", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: Owner}, TotalInstanceCount: 1},
		{Name: ServicePrefix + "run-other-owner", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: "other"}},
		{Name: "production-service", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: Owner}},
		{Name: ServicePrefix + "run-stale-count", Namespace: DefaultNamespace,
			Metadata: map[string]string{OwnerLabel: Owner}},
	}
	byName := make(map[string]ServiceView, len(services))
	for _, service := range services {
		byName[service.Name] = service
	}
	var deleted []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/naming/v1/services":
			name := r.URL.Query().Get("name")
			response := map[string]any{"code": 200000}
			if name == "" {
				response["services"] = services
			} else if service, ok := byName[name]; ok {
				response["services"] = []ServiceView{service}
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Errorf("encode services response: %v", err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/naming/v1/instances":
			instances := []InstanceView{}
			if r.URL.Query().Get("service") == ServicePrefix+"run-stale-count" {
				instances = append(instances, InstanceView{ID: "late-instance", Namespace: DefaultNamespace,
					Service: ServicePrefix + "run-stale-count", Metadata: map[string]string{OwnerLabel: Owner}})
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"code": 200000, "instances": instances}); err != nil {
				t.Errorf("encode instances response: %v", err)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/naming/v1/services/delete":
			if err := json.NewDecoder(r.Body).Decode(&deleted); err != nil {
				t.Errorf("decode delete payload: %v", err)
			}
			if err := json.NewEncoder(w).Encode(map[string]any{
				"code": 200000, "responses": []map[string]any{{"code": 200000}},
			}); err != nil {
				t.Errorf("encode delete response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base := Options{Mode: "cleanup", Console: server.URL, Namespace: DefaultNamespace,
		DeleteServices: true, Apply: true, Timeout: 2 * time.Second}
	dry := base
	dry.DryRun = true
	require.NoError(t, Cleanup(context.Background(), dry))
	require.Empty(t, deleted)

	require.NoError(t, Cleanup(context.Background(), base))
	require.Equal(t, []map[string]string{{
		"namespace": DefaultNamespace, "name": ServicePrefix + "run-empty",
	}}, deleted)
}

func TestEventuallyRetriesAndHonorsCancellation(t *testing.T) {
	attempts := 0
	require.NoError(t, Eventually(context.Background(), time.Second, func() error {
		attempts++
		if attempts < 2 {
			return errors.New("not ready")
		}
		return nil
	}))
	require.Equal(t, 2, attempts)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, Eventually(ctx, time.Second, func() error {
		t.Fatal("canceled assertion was executed")
		return nil
	}), context.Canceled)
}

func TestSDKErrorClassificationPreservesWrappedCause(t *testing.T) {
	cause := model.NewSDKError(model.ErrCodeServiceTypeConflict, nil, "type conflict")
	require.Equal(t, model.ErrCodeServiceTypeConflict, sdkErrorCode(errors.Join(errors.New("prepare"), cause)))
	require.Equal(t, model.ErrCodeSuccess, sdkErrorCode(nil))
	require.Equal(t, model.ErrCodeUnknown, sdkErrorCode(errors.New("not an SDK error")))
}
