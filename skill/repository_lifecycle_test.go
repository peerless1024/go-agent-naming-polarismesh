/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package skill

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
)

type afterFuncGateContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*afterFuncGateContext) Value(any) any { return nil }

func (c *afterFuncGateContext) AfterFunc(func()) func() bool {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return func() bool { return true }
}

func TestRepositoryCloseWaitsForAdmittedRefreshContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	caller := &afterFuncGateContext{Context: parent, entered: make(chan struct{}), release: make(chan struct{})}
	reloadStarted, reloadRelease := make(chan struct{}), make(chan struct{})
	repository := NewPeriodicRepository(context.Background(), nil, 0, 0, func(context.Context) (agentskill.Repository, error) {
		close(reloadStarted)
		<-reloadRelease
		return nil, nil
	})
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- repository.Refresh(caller) }()
	select {
	case <-caller.entered:
	case <-time.After(time.Second):
		t.Fatal("manual refresh did not reach context initialization")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- repository.Close() }()
	otherCloseDone := make(chan error, 1)
	go func() { otherCloseDone <- repository.Close() }()
	returnedEarly, otherReturnedEarly := false, false
	select {
	case <-closeDone:
		returnedEarly = true
	case <-otherCloseDone:
		otherReturnedEarly = true
	case <-time.After(30 * time.Millisecond):
	}
	close(caller.release)
	select {
	case <-reloadStarted:
	case <-time.After(time.Second):
		t.Fatal("admitted refresh did not resume")
	}
	select {
	case <-closeDone:
		returnedEarly = true
	case <-otherCloseDone:
		otherReturnedEarly = true
	case <-time.After(30 * time.Millisecond):
	}
	close(reloadRelease)
	require.ErrorIs(t, <-refreshDone, context.Canceled)
	if !returnedEarly {
		require.NoError(t, <-closeDone)
	}
	if !otherReturnedEarly {
		require.NoError(t, <-otherCloseDone)
	}
	require.False(t, returnedEarly, "Close returned before context initialization and reload drained")
	require.False(t, otherReturnedEarly, "concurrent Close returned before context initialization and reload drained")
	require.Error(t, repository.Refresh(context.Background()))
}
