/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/stateless"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
	namingtest "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil/naming"
	adapterserver "trpc.group/trpc-go/trpc-agent-naming-polarismesh/server/a2a"
)

func TestRuntimeNewA2AServerRequiresPrepare(t *testing.T) {
	runtime, err := adapteragent.New(
		adapteragent.WithConfig(testutil.ProviderConfig()),
		adapteragent.WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	_, err = adapterserver.NewA2AServer(runtime, &fakeRunner{})
	require.ErrorIs(t, err, adapteragent.ErrNotPrepared)
}

func TestRuntimeNewA2AServer(t *testing.T) {
	runtime, err := adapteragent.New(
		adapteragent.WithConfig(testutil.ProviderConfig()),
		adapteragent.WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	require.NoError(t, runtime.Prepare(context.Background()))

	server := newTestServer(t, runtime, &fakeRunner{})
	require.NotNil(t, server)
}

func TestRuntimeNewA2AServerKeepsFrozenCard(t *testing.T) {
	runtime, err := adapteragent.New(
		adapteragent.WithConfig(testutil.ProviderConfig()),
		adapteragent.WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
	)
	require.NoError(t, err)
	require.NoError(t, runtime.Prepare(context.Background()))
	defer func() { _ = runtime.Close(context.Background()) }()

	card, err := runtime.AgentCard()
	require.NoError(t, err)
	card.Name = "different-agent"
	server := newTestServer(
		t,
		runtime,
		&fakeRunner{},
		a2aserver.WithExtraA2AOptions(a2aprotocolserver.WithAgentCard(card)),
	)

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet,
		"/a2a/.well-known/agent-card.json",
		nil,
	))
	require.Equal(t, http.StatusOK, recorder.Code)
	var served a2aprotocolserver.AgentCard
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &served))
	require.Equal(t, testutil.ProviderConfig().ServiceName, served.Name)
}

func TestRuntimeNewA2AServerServesAdvertisedExactPath(t *testing.T) {
	paths := []string{"/", "/a2a", "/a2a/", "/api/a2a", "/\u667a\u80fd\u4f53/a2a"}
	for _, a2aPath := range paths {
		t.Run(a2aPath, func(t *testing.T) {
			config := testutil.ProviderConfig()
			config.A2APath = a2aPath
			runtime, err := adapteragent.New(
				adapteragent.WithConfig(config),
				adapteragent.WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()),
			)
			require.NoError(t, err)
			require.NoError(t, runtime.Prepare(context.Background()))
			defer func() { _ = runtime.Close(context.Background()) }()

			server := newTestServer(t, runtime, &fakeRunner{})
			card, err := runtime.AgentCard()
			require.NoError(t, err)

			response := sendUnary(t, server, card.PrimaryURL())
			task := response.GetTask()
			require.NotNil(t, task)
			require.Equal(t, protocol.TaskStateCompleted, task.Status.State)
			require.Len(t, task.Artifacts, 1)
			require.Len(t, task.Artifacts[0].Parts, 1)
			require.Equal(t, "ok", task.Artifacts[0].Parts[0].TextContent())
		})
	}
}

func TestRuntimeNewA2AServerCannotDisableAdvertisedBinding(t *testing.T) {
	runtime, err := adapteragent.New(adapteragent.WithConfig(testutil.ProviderConfig()),
		adapteragent.WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()))
	require.NoError(t, err)
	require.NoError(t, runtime.Prepare(context.Background()))
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	server := newTestServer(t, runtime, &fakeRunner{},
		a2aserver.WithExtraA2AOptions(a2aprotocolserver.WithV1JSONRPCEnabled(false)))
	card, err := runtime.AgentCard()
	require.NoError(t, err)
	response := sendUnary(t, server, card.PrimaryURL())
	require.NotNil(t, response.GetTask())
	require.Equal(t, protocol.TaskStateCompleted, response.GetTask().Status.State)
}

func TestRuntimeNewA2AServerIgnoresNilOption(t *testing.T) {
	runtime, err := adapteragent.New(adapteragent.WithConfig(testutil.ProviderConfig()),
		adapteragent.WithMetadataRegistrar(namingtest.NewFakeMetadataRegistrar()))
	require.NoError(t, err)
	require.NoError(t, runtime.Prepare(context.Background()))
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	require.NotPanics(t, func() {
		require.NotNil(t, newTestServer(t, runtime, &fakeRunner{}, nil))
	})
}

func newTestServer(
	t *testing.T,
	provider adapterserver.CardProvider,
	agentRunner runner.Runner,
	options ...a2aserver.Option,
) *a2aprotocolserver.A2AServer {
	t.Helper()
	options = append(options, a2aserver.WithTaskManagerBuilder(func(processor taskmanager.MessageProcessor) (taskmanager.TaskManager, error) {
		manager, err := stateless.NewTaskManager(processor)
		if err != nil {
			return nil, err
		}
		// Handler-only tests own the task manager; A2AServer.Start was not called.
		t.Cleanup(func() { require.NoError(t, manager.Close()) })
		return manager, nil
	}))
	server, err := adapterserver.NewA2AServer(provider, agentRunner, options...)
	require.NoError(t, err)
	return server
}

func sendUnary(t *testing.T, server *a2aprotocolserver.A2AServer, endpoint string) protocol.SendMessageResponse {
	t.Helper()
	payload, err := json.Marshal(struct {
		JSONRPC string                     `json:"jsonrpc"`
		ID      string                     `json:"id"`
		Method  string                     `json:"method"`
		Params  protocol.SendMessageParams `json:"params"`
	}{"2.0", "request", "SendMessage", protocol.SendMessageParams{
		Message: protocol.NewMessage(protocol.MessageRoleUser, []*protocol.Part{protocol.NewTextPart("hi")}),
	}})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("A2A-Version", "1.0")
	request.Header.Set("X-User-ID", "test-user")
	server.Handler().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Header().Get("Location"))
	var envelope struct {
		JSONRPC string                       `json:"jsonrpc"`
		ID      string                       `json:"id"`
		Error   json.RawMessage              `json:"error"`
		Result  protocol.SendMessageResponse `json:"result"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, "2.0", envelope.JSONRPC)
	require.Equal(t, "request", envelope.ID)
	require.True(t, len(envelope.Error) == 0 || string(envelope.Error) == "null", "JSON-RPC error: %s", envelope.Error)
	return envelope.Result
}

type fakeRunner struct{}

func (r *fakeRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event, 2)
	events <- event.NewResponseEvent("test-invocation", "test-agent", &model.Response{
		Object:  model.ObjectTypeChatCompletion,
		Done:    true,
		Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}},
	})
	events <- event.NewResponseEvent("test-invocation", "test-agent", &model.Response{
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	})
	close(events)
	return events, nil
}

func (r *fakeRunner) Close() error {
	return nil
}
