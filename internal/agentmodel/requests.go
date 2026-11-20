/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agentmodel

import (
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// EnsureAgentServiceRequest 确保 AI Agent 服务存在；已存在时按三态语义更新 metadata/Skill。
type EnsureAgentServiceRequest struct {
	Namespace string
	Service   string
	// Metadata 为 nil 时保持原值；非 nil（包括空 map）时整体替换。
	Metadata map[string]string
	// Skills 为 nil 时保持原值；非 nil（包括空 slice）时整体替换。
	Skills []model.AgentSkill
}

// Validate 校验 EnsureAgentServiceRequest。
func (r *EnsureAgentServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "EnsureAgentServiceRequest can not be nil")
	}
	if r.Namespace == "" || r.Service == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"EnsureAgentServiceRequest namespace and service must not be empty")
	}
	_, err := model.NormalizeAgentSkillValues(r.Skills)
	return err
}

// MountAgentSkillsRequest 挂载 AI Agent Skill。
type MountAgentSkillsRequest struct {
	Namespace string
	Service   string
	Skills    []model.AgentSkill
	Replace   bool
}

// Validate 校验 MountAgentSkillsRequest。
func (r *MountAgentSkillsRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "MountAgentSkillsRequest can not be nil")
	}
	if r.Namespace == "" || r.Service == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"MountAgentSkillsRequest namespace and service must not be empty")
	}
	_, err := model.NormalizeAgentSkillValues(r.Skills)
	return err
}

// UnmountAgentSkillsRequest 卸载 AI Agent Skill。
type UnmountAgentSkillsRequest struct {
	Namespace string
	Service   string
	SkillIDs  []string
}

// DeleteAgentServiceRequest 删除 AI Agent 服务。
type DeleteAgentServiceRequest struct {
	Namespace string
	Service   string
	// Revision 和 Owner 非空时作为删除前置条件。
	Revision string
	Owner    string
	// Force 跳过 SDK 的空服务预检查，但不会绕过 owner/type/revision 校验或服务端保护。
	Force bool
}

// Validate 校验 DeleteAgentServiceRequest。
func (r *DeleteAgentServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"DeleteAgentServiceRequest can not be nil")
	}
	if r.Namespace == "" || r.Service == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"DeleteAgentServiceRequest namespace and service must not be empty")
	}
	if strings.TrimSpace(r.Revision) != r.Revision || strings.TrimSpace(r.Owner) != r.Owner {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"DeleteAgentServiceRequest revision and owner must not contain surrounding whitespace")
	}
	return nil
}

// Validate 校验 UnmountAgentSkillsRequest。
func (r *UnmountAgentSkillsRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "UnmountAgentSkillsRequest can not be nil")
	}
	if r.Namespace == "" || r.Service == "" || len(r.SkillIDs) == 0 {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"UnmountAgentSkillsRequest namespace, service and skill ids must not be empty")
	}
	for _, id := range r.SkillIDs {
		if _, _, _, err := model.ParseSkillRef(id); err != nil {
			return err
		}
	}
	return nil
}

// GetAgentServiceRequest 精确获取单个 AI Agent 服务。
type GetAgentServiceRequest struct {
	// 可选，流水号，用于跟踪用户的请求。
	FlowID uint64
	// 必选，命名空间。
	Namespace string
	// 必选，服务名。
	Service string
	// 可选，单次查询超时时间。
	Timeout *time.Duration
	// 可选，重试次数。
	RetryCount *int
}

// SetTimeout 设置超时时间。
func (g *GetAgentServiceRequest) SetTimeout(duration time.Duration) {
	g.Timeout = model.ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数。
func (g *GetAgentServiceRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

// GetTimeoutPtr 获取超时时间。
func (g *GetAgentServiceRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

// GetRetryCountPtr 获取重试次数。
func (g *GetAgentServiceRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

// Validate 校验请求。
func (g *GetAgentServiceRequest) Validate() error {
	if g == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "GetAgentServiceRequest can not be nil")
	}
	if g.Namespace == "" || g.Service == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetAgentServiceRequest namespace and service must not be empty")
	}
	return nil
}
