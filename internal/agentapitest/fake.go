/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package agentapitest provides AgentAPI fakes without creating a test import
// cycle with the agentcard package.
package agentapitest

import (
	"sync"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentapi"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentmodel"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/testutil"
)

// FakeAgentAPI records AgentAPI calls for Runtime and Skill loader tests.
type FakeAgentAPI struct {
	agentapi.AgentAPI
	mu            sync.Mutex
	Service       *model.AgentService
	EnsureRequest *agentapi.EnsureServiceRequest
	MountRequest  *agentapi.MountSkillsRequest
	Metadata      map[string]string
	EnsureErr     error
	GetErr        error
	MountErr      error
	ValidateErr   error
	ValidateCalls int
	sdkContext    api.SDKContext
}

func (f *FakeAgentAPI) SDKContext() api.SDKContext { return f.sdkContext }

func (f *FakeAgentAPI) GetService(
	_ *agentapi.GetServiceRequest,
) (*model.AgentService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Service, f.GetErr
}

func (f *FakeAgentAPI) EnsureService(
	request *agentapi.EnsureServiceRequest,
) (*model.AgentService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.EnsureRequest = copyEnsureServiceRequest(request)
	if f.EnsureErr != nil {
		return nil, f.EnsureErr
	}
	if f.Service == nil {
		f.Service = &model.AgentService{
			Namespace: request.Namespace,
			Name:      request.Service,
			Type:      model.ServiceTypeAIAgent,
			Metadata:  testutil.CloneStringMap(request.Metadata),
		}
	}
	return f.Service, nil
}

func (f *FakeAgentAPI) MountSkills(
	request *agentapi.MountSkillsRequest,
) (*model.AgentService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MountRequest = &agentapi.MountSkillsRequest{
		MountAgentSkillsRequest: agentmodel.MountAgentSkillsRequest{
			Namespace: request.Namespace,
			Service:   request.Service,
			Skills:    append([]model.AgentSkill(nil), request.Skills...),
			Replace:   request.Replace,
		},
		Revision: request.Revision,
	}
	return f.Service, f.MountErr
}

func (f *FakeAgentAPI) ValidateInstanceMetadata(metadata map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ValidateCalls++
	f.Metadata = testutil.CloneStringMap(metadata)
	return f.ValidateErr
}

func (*FakeAgentAPI) Destroy() {}

func copyEnsureServiceRequest(input *agentapi.EnsureServiceRequest) *agentapi.EnsureServiceRequest {
	if input == nil {
		return nil
	}
	return &agentapi.EnsureServiceRequest{
		EnsureAgentServiceRequest: agentmodel.EnsureAgentServiceRequest{
			Namespace: input.Namespace,
			Service:   input.Service,
			Metadata:  testutil.CloneStringMap(input.Metadata),
			Skills:    append([]model.AgentSkill(nil), input.Skills...),
		},
		Revision: input.Revision,
	}
}
