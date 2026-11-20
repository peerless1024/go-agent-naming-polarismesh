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
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	adapteragentapi "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentapi"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	agentapitest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentapitest"
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

func TestRuntimeRegistrationSnapshotAndHotCardUpdate(t *testing.T) {
	config := testutil.ProviderConfig()
	config.RegistrationPort = 18080
	registrar := namingtest.NewFakeMetadataRegistrar()
	runtime, err := New(
		WithConfig(config),
		WithSDKContext(&testutil.FakeSDKContext{}),
		WithMetadataRegistrar(registrar),
	)
	require.NoError(t, err)
	require.NoError(t, runtime.Prepare(context.Background()))

	snapshot, err := runtime.RegistrationSnapshot()
	require.NoError(t, err)
	require.Len(t, snapshot.Metadata, 3)
	require.Equal(t, config.AgentVersion, snapshot.Version)
	require.Equal(t, 18080, snapshot.RegistrationPort)
	require.Equal(t, config.AdvertiseHost, snapshot.Host)
	require.True(t, snapshot.SetProtocol)
	require.Empty(t, snapshot.Protocol)

	card, err := runtime.AgentCard()
	require.NoError(t, err)
	card.Description = "updated description"
	require.NoError(t, runtime.UpdateAgentCard(context.Background(), card))
	updated, err := runtime.RegistrationSnapshot()
	require.NoError(t, err)
	require.NotEqual(t, snapshot.Metadata[agentcard.MetadataAgentCard],
		updated.Metadata[agentcard.MetadataAgentCard])

	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(updated.Metadata[agentcard.MetadataAgentCard]), &raw))
	raw["x-vendor-field"] = "preserved"
	raw["signatures"] = []map[string]any{{"protected": "p", "signature": "s"}}
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, runtime.UpdateAgentCardJSON(context.Background(), "1.0", encoded))
	latest, err := runtime.InstanceMetadata()
	require.NoError(t, err)
	require.Contains(t, latest[agentcard.MetadataAgentCard], "x-vendor-field")
	require.Contains(t, latest[agentcard.MetadataAgentCard], "protected")
	require.ErrorContains(
		t,
		runtime.UpdateAgentCardJSON(context.Background(), "0.3", encoded),
		"must match configured schema version",
	)
}

func TestRuntimeUpdatedCardOwnsNestedFields(t *testing.T) {
	config := testutil.ProviderConfig()
	registrar := namingtest.NewFakeMetadataRegistrar()
	runtime, err := New(
		WithConfig(config),
		WithSDKContext(&testutil.FakeSDKContext{}),
		WithMetadataRegistrar(registrar),
	)
	require.NoError(t, err)
	require.NoError(t, runtime.Prepare(context.Background()))

	card, err := runtime.AgentCard()
	require.NoError(t, err)
	require.NotEmpty(t, card.SupportedInterfaces)
	card.Description = "updated card owned by runtime"
	require.NoError(t, runtime.UpdateAgentCard(context.Background(), card))

	updatedJSON, _, err := runtime.AgentCardJSON()
	require.NoError(t, err)
	updatedMetadata, err := runtime.InstanceMetadata()
	require.NoError(t, err)
	card.SupportedInterfaces[0].URL = "http://127.0.0.2:18080/a2a"

	updated, err := runtime.AgentCard()
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:18080/a2a", updated.SupportedInterfaces[0].URL)
	afterMutationJSON, _, err := runtime.AgentCardJSON()
	require.NoError(t, err)
	require.Equal(t, updatedJSON, afterMutationJSON)
	afterMutationMetadata, err := runtime.InstanceMetadata()
	require.NoError(t, err)
	require.Equal(t, updatedMetadata, afterMutationMetadata)
}

func TestRuntimeRegistryAndDiscoveryDisable(t *testing.T) {
	providerConfig := testutil.ProviderConfig()
	providerConfig.RegistryEnabled = false
	registrar := namingtest.NewFakeMetadataRegistrar()
	provider, err := New(
		WithConfig(providerConfig),
		WithSDKContext(&testutil.FakeSDKContext{}),
		WithMetadataRegistrar(registrar),
	)
	require.NoError(t, err)
	require.NoError(t, provider.Prepare(context.Background()))
	_, registered := registrar.Provider(providerConfig.ServiceName)
	require.False(t, registered)

	consumerConfig := adapterconfig.DefaultConfig()
	consumerConfig.Namespace = "Production"
	consumerConfig.ServiceName = "customer-agent"
	consumerConfig.DiscoveryEnabled = false
	consumer, err := New(
		WithConfig(consumerConfig),
		WithConsumerAPI(&testutil.FakeInstanceLister{}),
	)
	require.NoError(t, err)
	require.NoError(t, consumer.Prepare(context.Background()))
	_, err = consumer.Resolver()
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), "discovery is disabled")
}

func TestRuntimePublishedSkillModeInitializesSkillAPI(t *testing.T) {
	config := testutil.ProviderConfig()
	config.SkillMode = adapterconfig.SkillModePublished
	config.SkillCacheDir = t.TempDir()
	skillAPI := &testutil.FakeSkillAPI{
		ListResults: []*api.SkillListResult{{
			SkillListResult: model.SkillListResult{
				Total: 1,
				Resources: []*model.SkillResource{{
					Name:        "weather",
					Namespace:   config.Namespace,
					VersionInfo: &model.SkillVersionInfo{ActiveVersion: "1.0.0"},
				}},
			},
		}},
	}
	runtime, err := New(
		WithConfig(config),
		WithSDKContext(&testutil.FakeSDKContext{}),
		WithSkillAPI(skillAPI),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)
	require.NoError(t, runtime.Prepare(context.Background()))

	repository, err := runtime.SkillRepository()
	require.NoError(t, err)
	require.NotNil(t, repository)
	require.Len(t, repository.Summaries(), 1)
	require.Len(t, skillAPI.ListRequests, 1)
}

func TestRuntimePreservesLegacyServiceSkillMerge(t *testing.T) {
	config := testutil.ProviderConfig()
	config.UseServiceSkills = true
	config.SkillMode = ""
	config.SkillCacheDir = t.TempDir()
	config.SkillRefs = []adapterconfig.SkillRef{{
		Namespace: "Production", Name: "explicit", Version: "1.0.0",
	}}
	mounted := adapterconfig.SkillRef{
		Namespace: "Production", Name: "mounted", Version: "1.0.0",
	}
	runtime, err := New(
		WithConfig(config),
		WithAgentAPI(&agentapitest.FakeAgentAPI{
			Service: testutil.AgentServiceWithSkills(mounted),
		}),
		WithSkillAPI(&testutil.FakeSkillAPI{}),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)
	require.NoError(t, runtime.Prepare(context.Background()))
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	require.Equal(t, adapterconfig.SkillModeMounted, runtime.Config().SkillMode)
	repository, err := runtime.SkillRepository()
	require.NoError(t, err)
	require.NotNil(t, repository)
	_, err = repository.Get("explicit")
	require.NoError(t, err)
	_, err = repository.Get("mounted")
	require.NoError(t, err)
}

func TestRuntimeEnsureServiceRejectsTypeConflict(t *testing.T) {
	config := testutil.ProviderConfig()
	config.EnsureService = true
	agentAPI := &agentapitest.FakeAgentAPI{
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

type ensureSkillsRecorder struct {
	agentapitest.FakeAgentAPI
	skillsNil bool
}

func (a *ensureSkillsRecorder) EnsureService(
	request *adapteragentapi.EnsureServiceRequest,
) (*model.AgentService, error) {
	a.skillsNil = request.Skills == nil
	return a.FakeAgentAPI.EnsureService(request)
}

func TestRuntimeEnsureOnlyPreservesMountedSkills(t *testing.T) {
	config := testutil.ProviderConfig()
	config.EnsureService = true
	config.UseServiceSkills = true
	config.SkillMode = ""
	config.SkillCacheDir = t.TempDir()
	ref := adapterconfig.SkillRef{
		Namespace: config.Namespace, Name: "mounted", Version: "1.0.0",
	}
	agentAPI := &ensureSkillsRecorder{
		FakeAgentAPI: agentapitest.FakeAgentAPI{
			Service: testutil.AgentServiceWithSkills(ref),
		},
	}
	runtime, err := New(
		WithConfig(config),
		WithAgentAPI(agentAPI),
		WithSkillAPI(&testutil.FakeSkillAPI{}),
		WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)
	require.NoError(t, runtime.Prepare(context.Background()))
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	require.True(t, agentAPI.skillsNil)
	repository, err := runtime.SkillRepository()
	require.NoError(t, err)
	require.NotNil(t, repository)
	_, err = repository.Get(ref.Name)
	require.NoError(t, err)
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
