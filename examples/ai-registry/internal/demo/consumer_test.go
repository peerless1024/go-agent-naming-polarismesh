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
	"net/http"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func responseEvent(text string, done bool) *event.Event {
	return event.NewResponseEvent("test", "provider", &model.Response{Done: done,
		Choices: []model.Choice{{Message: model.NewAssistantMessage(text)}},
	})
}

func TestCollectRequiresCompletionAndRejectsInvalidEvents(t *testing.T) {
	for _, events := range [][]*event.Event{nil, {responseEvent("partial", false)}, {nil},
		{event.New("test", "provider")}, {responseEvent("", true)}} {
		stream := make(chan *event.Event, len(events))
		for _, evt := range events {
			stream <- evt
		}
		close(stream)
		_, _, err := Collect(context.Background(), stream)
		require.Error(t, err)
	}
	_, _, err := Collect(context.Background(), nil)
	require.Error(t, err)
	stream := make(chan *event.Event, 1)
	stream <- responseEvent("completed", true)
	close(stream)
	text, count, err := Collect(context.Background(), stream)
	require.NoError(t, err)
	require.Equal(t, "completed", text)
	require.Equal(t, 1, count)
}

func TestCollectCanceledEmptyStreamReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Collect(ctx, make(chan *event.Event))
	require.ErrorIs(t, err, context.Canceled)
}

func newDeterministicAgent() *deterministicAgent {
	ctx, cancel := context.WithCancel(context.Background())
	return &deterministicAgent{name: "provider", node: "node", stats: &providerStats{}, ctx: ctx, cancel: cancel}
}

func TestProviderUnicodeChunksAndClosedRuns(t *testing.T) {
	a := newDeterministicAgent()
	defer func() { require.NoError(t, a.Close()) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events, err := a.Run(ctx, Invocation("你好智能体"))
	require.NoError(t, err)
	var text string
	for evt := range events {
		for _, choice := range evt.Choices {
			require.True(t, utf8.ValidString(choice.Delta.Content))
			text += choice.Delta.Content
		}
	}
	require.Equal(t, "node|echo:你好智能体", text)
	require.NoError(t, a.Close())
	_, err = a.Run(ctx, Invocation("late"))
	require.Error(t, err)
}

func TestProviderCloseStopsUnconsumedAndConcurrentRuns(t *testing.T) {
	a := newDeterministicAgent()
	_, err := a.Run(context.Background(), Invocation("slow"))
	require.NoError(t, err)
	require.NoError(t, runConcurrent(24, func(int) error {
		events, err := a.Run(context.Background(), Invocation("echo"))
		if err != nil {
			return err
		}
		for range events {
		}
		return nil
	}))
	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("provider Close did not stop an unconsumed stream")
	}
}

func TestClientFactoryCreatesIndependentClients(t *testing.T) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	factory := ClientFactory(transport, time.Second, true, false)
	first, err := factory(context.Background())
	require.NoError(t, err)
	second, err := factory(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, first)
	require.NotEmpty(t, second)
}
