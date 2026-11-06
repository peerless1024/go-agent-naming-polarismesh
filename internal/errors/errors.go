/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package errors

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed indicates that the runtime or resolver has been closed.
	ErrClosed = errors.New("trpc agent polaris runtime is closed")
	// ErrNotPrepared indicates that provider resources are not ready.
	ErrNotPrepared = errors.New("trpc agent polaris runtime is not prepared")
	// ErrInvalidConfig indicates an invalid adapter configuration.
	ErrInvalidConfig = errors.New("invalid trpc agent polaris configuration")
	// ErrNoAvailableInstance indicates that no healthy instance has a usable AgentCard.
	ErrNoAvailableInstance = errors.New("no available A2A agent instance")
	// ErrAgentServiceAPIUnsupported indicates that Polaris AgentAPI is unavailable.
	ErrAgentServiceAPIUnsupported = errors.New("polaris AgentAPI is not available")
	// ErrSkillInstallerUnsupported indicates that Polaris SkillAPI is unavailable.
	ErrSkillInstallerUnsupported = errors.New("polaris SkillAPI is not available")
	// ErrMetadataRegistrarUnsupported indicates that dynamic metadata registration is unavailable.
	ErrMetadataRegistrarUnsupported = errors.New("polaris metadata registrar is not available")
)

type runtimeError struct {
	op  string
	err error
}

func (e *runtimeError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %v", e.op, e.err)
}

func (e *runtimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// WrapError wraps err with an adapter operation name.
func WrapError(op string, err error) error {
	if err == nil {
		return nil
	}
	return &runtimeError{op: op, err: err}
}
