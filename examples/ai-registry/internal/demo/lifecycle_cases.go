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
	"net/http"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

func (s *suite) typeConflict(ctx context.Context) error {
	name := s.service("ordinary")
	s.state.Services = appendUnique(s.state.Services, name)
	if err := s.save(); err != nil {
		return err
	}
	if _, err := s.console.Request(ctx, http.MethodPost, "/naming/v1/services", []map[string]any{{
		"namespace": s.opts.Namespace, "name": name, "service_type": "SERVICE_TYPE_MICROSERVICE",
		"metadata": map[string]string{OwnerLabel: Owner, RunLabel: s.opts.RunID},
	}}); err != nil {
		return err
	}
	cfg := s.providerConfig(name)
	cfg.EnsureService = true
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(s.sdk))
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	err = runtime.Prepare(ctx)
	if sdkErrorCode(err) != model.ErrCodeServiceTypeConflict {
		return fmt.Errorf("expected ordinary-service type conflict, got %v", err)
	}
	if _, err := runtime.AgentCard(); !errors.Is(err, adapteragent.ErrNotPrepared) {
		return errors.New("failed type-conflict Prepare exposed a card")
	}
	return nil
}

func (s *suite) prepareRetry(ctx context.Context) error {
	name := s.service("prepare-retry")
	if err := s.ensureService(ctx, name); err != nil {
		return err
	}
	skill := SkillPrefix + s.opts.RunID + "-retry"
	cfg := s.providerConfig(name)
	cfg.SkillCacheDir = filepath.Join(s.opts.RunDir, "skill-cache")
	cfg.SkillRefs = []adapterconfig.SkillRef{s.ref(skill, "1.0.0")}
	runtime, err := adapteragent.New(adapteragent.WithConfig(cfg), adapteragent.WithSDKContext(s.sdk))
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); sdkErrorCode(err) != model.ErrCodeSkillNotFound {
		return fmt.Errorf("prepare with a missing real Skill returned %v", err)
	}
	if _, err := runtime.SkillRepository(); !errors.Is(err, adapteragent.ErrNotPrepared) {
		return errors.New("failed Prepare returned a repository")
	}
	if err := polarisregistry.RegisterMetadataProvider(name, polarisregistry.MetadataProviderFunc(func() (map[string]string, error) { return nil, nil })); err != nil {
		return errors.New("failed Prepare left metadata provider registered")
	}
	polarisregistry.UnregisterMetadataProvider(name)
	if err := s.createSkill(ctx, skill, "1.0.0", true, false); err != nil {
		return err
	}
	if err := runtime.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare could not retry after publishing the missing Skill: %w", err)
	}
	if err := runtime.Prepare(ctx); err != nil {
		return err
	}
	repository, err := runtime.SkillRepository()
	if err != nil || repository == nil || len(repository.Summaries()) != 1 {
		return errors.Join(err, errors.New("retry did not assemble the published Skill"))
	}
	if err := runtime.Close(ctx); err != nil {
		return err
	}
	if err := runtime.Close(ctx); err != nil {
		return err
	}
	if s.sdk.IsDestroyed() {
		return errors.New("Close destroyed the borrowed SDK context")
	}
	if _, err := runtime.NewRemoteAgent(ctx, "closed"); !errors.Is(err, adapteragent.ErrClosed) {
		return errors.New("closed Runtime accepted a new consumer")
	}
	_, err = s.skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{Namespace: s.opts.Namespace, Name: skill, Version: "1.0.0"}})
	return err
}

func (s *suite) ensureExisting(ctx context.Context) error {
	name := s.service("ensure-idempotent")
	if err := s.ensureService(ctx, name); err != nil {
		return err
	}
	before, err := s.mount(name, []adapterconfig.SkillRef{s.ref(s.skillB, "1.0.0")}, true)
	if err != nil {
		return err
	}
	after, err := s.agents.EnsureService(&api.EnsureAgentServiceRequest{EnsureAgentServiceRequest: model.EnsureAgentServiceRequest{
		Namespace: s.opts.Namespace, Service: name, Metadata: map[string]string{"must-not-write": "true"},
		Skills: []model.AgentSkill{{ID: s.ref(s.skillA, "1.0.0").ID(), Name: s.skillA, Version: "1.0.0"}},
	}})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before.Metadata, after.Metadata) || !reflect.DeepEqual(sortedIDs(before), sortedIDs(after)) {
		return errors.New("ensure overwrote existing service metadata or Skill references")
	}
	return ctx.Err()
}

func (s *suite) cleanupSafety(ctx context.Context) error {
	statePath := filepath.Join(s.opts.RunDir, "state.json")
	state, err := LoadState(statePath)
	if err != nil {
		return err
	}
	bad := *state
	bad.Namespace = "default"
	if bad.Validate() == nil {
		return errors.New("cleanup accepted the default namespace")
	}
	bad = *state
	bad.Services = []string{"existing-production-service"}
	if bad.Validate() == nil {
		return errors.New("cleanup accepted an unowned service")
	}
	bad = *state
	bad.Console = "http://username:synthetic-test-secret@127.0.0.1:8090?access_token=test"
	if err := bad.Validate(); err == nil || strings.Contains(err.Error(), "synthetic-test-secret") {
		return errors.New("cleanup accepted or leaked access-bearing Console URL")
	}
	bad = *state
	bad.RunDir = "/"
	if bad.Validate() == nil {
		return errors.New("cleanup accepted an unconstrained artifact path")
	}
	dry := s.opts
	dry.Mode, dry.StateFile, dry.Apply, dry.DryRun = "cleanup", statePath, false, true
	if err := Cleanup(ctx, dry); err != nil {
		return err
	}
	service, err := s.console.Service(ctx, s.opts.Namespace, s.service("skill-provider"))
	if err != nil {
		return err
	}
	if service == nil || service.Metadata[RunLabel] != s.opts.RunID {
		return errors.New("dry-run modified or lost the owned service")
	}
	return nil
}

func sdkErrorCode(err error) model.ErrCode {
	if err == nil {
		return model.ErrCodeSuccess
	}
	var sdkErr model.SDKError
	if errors.As(err, &sdkErr) {
		return sdkErr.ErrorCode()
	}
	return model.ErrCodeUnknown
}
