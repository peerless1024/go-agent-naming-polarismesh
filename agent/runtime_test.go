/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	namingtest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil/naming"
)

func TestRuntimePrepareAndClose(t *testing.T) {
	registrar := namingtest.NewFakeMetadataRegistrar()
	sdkContext := &testutil.FakeSDKContext{}
	runtime, err := New(
		WithConfig(testutil.ProviderConfig()),
		WithSDKContext(sdkContext),
		WithMetadataRegistrar(registrar),
	)
	require.NoError(t, err)

	require.NoError(t, runtime.Prepare(context.Background()))
	require.NoError(t, runtime.Prepare(context.Background()))

	provider, ok := registrar.Provider(testutil.ProviderConfig().ServiceName)
	require.True(t, ok)
	metadata, err := provider.Metadata()
	require.NoError(t, err)
	require.NoError(t, agentcard.ValidateAgentInstanceMetadata(metadata))

	card, err := runtime.AgentCard()
	require.NoError(t, err)
	require.Equal(t, testutil.ProviderConfig().ServiceName, card.Name)

	require.NoError(t, runtime.Close(context.Background()))
	require.NoError(t, runtime.Close(context.Background()))
	require.Equal(t, 1, registrar.UnregisterCalls)
	require.Equal(t, 0, sdkContext.DestroyCount())
	require.ErrorIs(t, runtime.Prepare(context.Background()), ErrClosed)
}

func TestRuntimeEnsureServiceRejectsTypeConflict(t *testing.T) {
	config := testutil.ProviderConfig()
	config.EnsureService = true
	agentAPI := &testutil.FakeAgentAPI{
		Service: &model.AgentService{
			Namespace: config.Namespace,
			Name:      config.ServiceName,
			Type:      model.ServiceTypeMicroservice,
		},
	}
	registrar := namingtest.NewFakeMetadataRegistrar()
	runtime, err := New(
		WithConfig(config),
		WithAgentAPI(agentAPI),
		WithMetadataRegistrar(registrar),
	)
	require.NoError(t, err)

	err = runtime.Prepare(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected SERVICE_TYPE_AI_AGENT")
	_, registered := registrar.Provider(config.ServiceName)
	require.False(t, registered)
}

func TestRuntimePrepareRollsBackMetadataOnSkillFailure(t *testing.T) {
	config := testutil.ProviderConfig()
	config.SkillCacheDir = t.TempDir()
	config.SkillRefs = []adapterconfig.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}}
	registrar := namingtest.NewFakeMetadataRegistrar()
	installErr := errors.New("download failed")
	runtime, err := New(
		WithConfig(config),
		WithMetadataRegistrar(registrar),
		WithSkillAPI(&testutil.FakeSkillAPI{Err: installErr}),
	)
	require.NoError(t, err)

	err = runtime.Prepare(context.Background())
	require.ErrorIs(t, err, installErr)
	_, ok := registrar.Provider(config.ServiceName)
	require.False(t, ok)
	require.Equal(t, 1, registrar.UnregisterCalls)
	require.Equal(t, runtimeStateNew, runtime.state)
}

func TestRuntimeRequiresSkillInstaller(t *testing.T) {
	config := testutil.ProviderConfig()
	config.SkillCacheDir = t.TempDir()
	config.SkillRefs = []adapterconfig.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}}
	runtime, err := New(
		WithConfig(config),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)

	err = runtime.Prepare(context.Background())
	require.ErrorIs(t, err, ErrSkillInstallerUnsupported)
}

func TestRuntimeNotPrepared(t *testing.T) {
	runtime, err := New(
		WithConfig(testutil.ProviderConfig()),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)

	_, err = runtime.AgentCard()
	require.ErrorIs(t, err, ErrNotPrepared)
	_, err = runtime.InstanceMetadata()
	require.ErrorIs(t, err, ErrNotPrepared)
	_, err = runtime.SkillRepository()
	require.ErrorIs(t, err, ErrNotPrepared)
}

func TestRuntimeProviderSettingsAreNotIgnored(t *testing.T) {
	config := testutil.ProviderConfig()
	config.AgentVersion = ""
	config.AdvertiseHost = ""
	config.AdvertisePort = 0
	config.SkillRefs = []adapterconfig.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}}
	config.SkillCacheDir = t.TempDir()
	runtime, err := New(
		WithConfig(config),
		WithSkillAPI(&testutil.FakeSkillAPI{}),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)

	err = runtime.Prepare(context.Background())
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), "agent version is required")
}

func TestRuntimeReturnsFrozenCopies(t *testing.T) {
	config := testutil.ProviderConfig()
	config.SkillRefs = []adapterconfig.SkillRef{{Namespace: "Production", Name: "weather", Version: "1.0.0"}}
	config.SkillCacheDir = t.TempDir()
	config.PolarisAddresses = []string{"127.0.0.1:8091"}
	runtime, err := New(
		WithConfig(config),
		WithSkillAPI(&testutil.FakeSkillAPI{}),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)

	config.SkillRefs[0].Name = "mutated"
	config.PolarisAddresses[0] = "mutated"
	gotConfig := runtime.Config()
	require.Equal(t, "weather", gotConfig.SkillRefs[0].Name)
	require.Equal(t, "127.0.0.1:8091", gotConfig.PolarisAddresses[0])

	require.NoError(t, runtime.Prepare(context.Background()))
	card, err := runtime.AgentCard()
	require.NoError(t, err)
	originalName := card.Name
	card.Name = "mutated"
	card.SupportedInterfaces[0].URL = "http://invalid"
	cardAgain, err := runtime.AgentCard()
	require.NoError(t, err)
	require.Equal(t, originalName, cardAgain.Name)
	require.NotEqual(t, "http://invalid", cardAgain.SupportedInterfaces[0].URL)
}

func TestRuntimeFreezesBuilderAgentCard(t *testing.T) {
	config := testutil.ProviderConfig()
	shared, err := agentcard.NewAgentCard(config)
	require.NoError(t, err)
	expectedURL := shared.PrimaryURL()
	runtime, err := New(
		WithConfig(config),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
		WithAgentCardBuilder(func(context.Context) (a2aprotocolserver.AgentCard, error) {
			return shared, nil
		}),
	)
	require.NoError(t, err)
	defer func() { _ = runtime.Close(context.Background()) }()

	require.NoError(t, runtime.Prepare(context.Background()))
	shared.SupportedInterfaces[0].URL = "http://127.0.0.1:19000/changed"
	frozen, err := runtime.AgentCard()
	require.NoError(t, err)
	require.Equal(t, expectedURL, frozen.PrimaryURL())
}

func TestRuntimePrepareRejectsCancelledContext(t *testing.T) {
	registrar := namingtest.NewFakeMetadataRegistrar()
	runtime, err := New(
		WithConfig(testutil.ProviderConfig()),
		WithMetadataRegistrar(registrar),
	)
	require.NoError(t, err)
	defer func() { _ = runtime.Close(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, runtime.Prepare(ctx), context.Canceled)
	_, registered := registrar.Provider(testutil.ProviderConfig().ServiceName)
	require.False(t, registered)
}

func TestRuntimePrepareAndCloseConcurrent(t *testing.T) {
	runtime, err := New(
		WithConfig(testutil.ProviderConfig()),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)

	var waitGroup sync.WaitGroup
	errs := make(chan error, 17)
	for i := 0; i < 16; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			err := runtime.Prepare(context.Background())
			if err != nil && !errors.Is(err, ErrClosed) {
				errs <- err
			}
		}()
	}
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		if err := runtime.Close(context.Background()); err != nil {
			errs <- err
		}
	}()
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestRuntimeCreatesPolarisAPIsFromBorrowedContext(t *testing.T) {
	config := testutil.ProviderConfig()
	config.UseServiceSkills = true
	sdkContext := &testutil.FakeSDKContext{}
	runtime, err := New(
		WithConfig(config),
		WithSDKContext(sdkContext),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)

	require.NoError(t, runtime.ensureRequiredPolarisAPIs())
	require.NotNil(t, runtime.agentAPI)
	require.NotNil(t, runtime.skillAPI)
	require.Same(t, sdkContext, runtime.agentAPI.SDKContext())
	require.Same(t, sdkContext, runtime.skillAPI.SDKContext())
}
