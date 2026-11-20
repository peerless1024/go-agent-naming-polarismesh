/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package agentapi provides the AI Agent service management API owned by this adapter.
package agentapi

import (
	"strings"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentmodel"
)

type (
	GetAgentServiceRequest    = agentmodel.GetAgentServiceRequest
	EnsureAgentServiceRequest = agentmodel.EnsureAgentServiceRequest
	MountAgentSkillsRequest   = agentmodel.MountAgentSkillsRequest
	UnmountAgentSkillsRequest = agentmodel.UnmountAgentSkillsRequest
	DeleteAgentServiceRequest = agentmodel.DeleteAgentServiceRequest
)

type (
	// GetServiceRequest identifies one AI Agent service.
	GetServiceRequest struct {
		agentmodel.GetAgentServiceRequest
	}
	// EnsureServiceRequest creates or updates one AI Agent service.
	EnsureServiceRequest struct {
		agentmodel.EnsureAgentServiceRequest
		// Revision is an optional optimistic concurrency precondition.
		Revision string
	}
	// MountSkillsRequest mounts service-level Skills.
	MountSkillsRequest struct {
		agentmodel.MountAgentSkillsRequest
		// Revision is an optional optimistic concurrency precondition.
		Revision string
	}
	// UnmountSkillsRequest removes service-level Skills.
	UnmountSkillsRequest struct {
		agentmodel.UnmountAgentSkillsRequest
		// Revision is an optional optimistic concurrency precondition.
		Revision string
	}
	// DeleteServiceRequest deletes one AI Agent service.
	DeleteServiceRequest struct {
		agentmodel.DeleteAgentServiceRequest
	}
)

// Validate checks a GetService request.
func (r *GetServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetServiceRequest can not be nil")
	}
	return r.GetAgentServiceRequest.Validate()
}

// Validate checks an EnsureService request.
func (r *EnsureServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"EnsureServiceRequest can not be nil")
	}
	if err := r.EnsureAgentServiceRequest.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Revision) != r.Revision {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"EnsureServiceRequest revision must not contain surrounding whitespace")
	}
	return nil
}

// Validate checks a MountSkills request.
func (r *MountSkillsRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"MountSkillsRequest can not be nil")
	}
	if err := r.MountAgentSkillsRequest.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Revision) != r.Revision {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"MountSkillsRequest revision must not contain surrounding whitespace")
	}
	return nil
}

// Validate checks an UnmountSkills request.
func (r *UnmountSkillsRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"UnmountSkillsRequest can not be nil")
	}
	if err := r.UnmountAgentSkillsRequest.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Revision) != r.Revision {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"UnmountSkillsRequest revision must not contain surrounding whitespace")
	}
	return nil
}

// Validate checks a DeleteService request.
func (r *DeleteServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"DeleteServiceRequest can not be nil")
	}
	return r.DeleteAgentServiceRequest.Validate()
}

// AgentAPI reads AI Agent service metadata and optionally manages it through Console.
type AgentAPI interface {
	// GetService reads one AI Agent service.
	GetService(req *GetServiceRequest) (*model.AgentService, error)
	// EnsureService creates or updates one AI Agent service.
	EnsureService(req *EnsureServiceRequest) (*model.AgentService, error)
	// MountSkills merges or replaces service-level Skill references.
	MountSkills(req *MountSkillsRequest) (*model.AgentService, error)
	// UnmountSkills removes selected service-level Skill references.
	UnmountSkills(req *UnmountSkillsRequest) (*model.AgentService, error)
	// DeleteService deletes one AI Agent service.
	DeleteService(req *DeleteServiceRequest) error
	// ValidateInstanceMetadata validates the three-key Agent instance metadata.
	ValidateInstanceMetadata(metadata map[string]string) error
	// SDKContext returns the Polaris SDK context used by this API.
	SDKContext() api.SDKContext
	// Destroy closes adapter-owned resources without destroying a borrowed SDKContext.
	Destroy()
}
