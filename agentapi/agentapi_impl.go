/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agentapi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"

	adapteragentcard "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/serviceadmin"
)

type agentAPI struct {
	context api.SDKContext
	admin   serviceStore
}

func newWithStore(admin serviceStore) AgentAPI {
	var context api.SDKContext
	if admin != nil {
		context = admin.SDKContext()
	}
	return &agentAPI{context: context, admin: admin}
}

// New creates the adapter-owned AgentAPI using a borrowed SDK context.
func New(sdkContext api.SDKContext, options ...Option) AgentAPI {
	store := &backend{context: sdkContext}
	if sdkContext != nil {
		store.consumer = api.NewConsumerAPIByContext(sdkContext)
	}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return newWithStore(store)
}

// NewByContext reads service metadata through ConsumerAPI; writes require WithConsole.
func NewByContext(sdkContext api.SDKContext, options ...Option) AgentAPI {
	return New(sdkContext, options...)
}

func (a *agentAPI) SDKContext() api.SDKContext {
	return a.context
}

func (a *agentAPI) ValidateInstanceMetadata(metadata map[string]string) error {
	return adapteragentcard.ValidateAgentInstanceMetadata(metadata)
}

func (a *agentAPI) Destroy() {
	if a != nil && a.admin != nil {
		a.admin.Destroy()
	}
}

func (a *agentAPI) GetService(req *GetServiceRequest) (*model.AgentService, error) {
	if err := a.checkAvailable(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	record, err := a.admin.GetService(&ServiceLookupRequest{
		GetServiceRequest: serviceadmin.GetServiceRequest{
			FlowID:     req.FlowID,
			Namespace:  req.Namespace,
			Service:    req.Service,
			Timeout:    req.Timeout,
			RetryCount: req.RetryCount,
		},
	})
	if err != nil {
		return nil, err
	}
	return serviceFromRecord(record)
}

func (a *agentAPI) EnsureService(req *EnsureServiceRequest) (*model.AgentService, error) {
	if err := a.checkAvailable(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	var metadata *map[string]string
	if req.Metadata != nil {
		values := cloneMap(req.Metadata)
		metadata = &values
	}
	var extended *[]json.RawMessage
	if req.Skills != nil {
		skills, err := model.NormalizeAgentSkillValues(req.Skills)
		if err != nil {
			return nil, err
		}
		values, err := buildSkillExtendedMetadata(skills)
		if err != nil {
			return nil, err
		}
		extended = &values
	}
	record, err := a.admin.EnsureService(&ServiceEnsureRequest{
		EnsureServiceRequest: serviceadmin.EnsureServiceRequest{
			ServiceWriteRequest: serviceadmin.ServiceWriteRequest{
				Namespace:        req.Namespace,
				Service:          req.Service,
				Type:             model.ServiceTypeAIAgent,
				Metadata:         metadata,
				ExtendedMetadata: extended,
				Revision:         req.Revision,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return serviceFromRecord(record)
}

func (a *agentAPI) MountSkills(req *MountSkillsRequest) (*model.AgentService, error) {
	if err := a.checkAvailable(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	record, err := a.currentAgentService(req.Namespace, req.Service, req.Revision)
	if err != nil {
		return nil, err
	}
	skills, err := model.NormalizeAgentSkillValues(req.Skills)
	if err != nil {
		return nil, err
	}
	extended, err := mergeSkills(record.ExtendedMetadata, skills, req.Replace, nil)
	if err != nil {
		return nil, err
	}
	return a.updateExtendedMetadata(record, extended)
}

func (a *agentAPI) UnmountSkills(req *UnmountSkillsRequest) (*model.AgentService, error) {
	if err := a.checkAvailable(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	record, err := a.currentAgentService(req.Namespace, req.Service, req.Revision)
	if err != nil {
		return nil, err
	}
	extended, err := mergeSkills(record.ExtendedMetadata, nil, false, req.SkillIDs)
	if err != nil {
		return nil, err
	}
	return a.updateExtendedMetadata(record, extended)
}

func (a *agentAPI) DeleteService(req *DeleteServiceRequest) error {
	if err := a.checkAvailable(); err != nil {
		return err
	}
	if err := req.Validate(); err != nil {
		return err
	}
	serviceType := model.ServiceTypeAIAgent
	return a.admin.DeleteService(&ServiceDeleteRequest{
		DeleteServiceRequest: serviceadmin.DeleteServiceRequest{
			Namespace: req.Namespace,
			Service:   req.Service,
			Type:      &serviceType,
			Revision:  req.Revision,
			Owner:     req.Owner,
			Force:     req.Force,
		},
	})
}

func (a *agentAPI) currentAgentService(
	namespace, service, revision string,
) (*serviceadmin.ServiceRecord, error) {
	record, err := a.admin.GetService(&ServiceLookupRequest{
		GetServiceRequest: serviceadmin.GetServiceRequest{Namespace: namespace, Service: service},
	})
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"service admin API returned an empty service for %s/%s", namespace, service)
	}
	if record.Type != model.ServiceTypeAIAgent {
		return nil, model.NewSDKError(model.ErrCodeServiceTypeConflict, nil,
			"service %s/%s is not AI_AGENT", namespace, service)
	}
	if revision != "" && revision != record.Revision {
		return nil, model.NewSDKError(model.ErrCodeServicePreconditionFailed, nil,
			"service %s/%s revision %q does not match requested revision %q",
			namespace, service, record.Revision, revision)
	}
	return record, nil
}

func (a *agentAPI) checkAvailable() error {
	if a == nil || a.admin == nil {
		return model.NewSDKError(model.ErrCodeServiceWriteUnsupported, nil,
			"service admin API is not available")
	}
	return nil
}

func (a *agentAPI) updateExtendedMetadata(
	record *serviceadmin.ServiceRecord, extended []json.RawMessage,
) (*model.AgentService, error) {
	updated, err := a.admin.UpdateService(&ServiceUpdateRequest{
		UpdateServiceRequest: serviceadmin.UpdateServiceRequest{
			ServiceWriteRequest: serviceadmin.ServiceWriteRequest{
				Namespace:        record.Namespace,
				Service:          record.Name,
				Type:             model.ServiceTypeAIAgent,
				ExtendedMetadata: &extended,
				Revision:         record.Revision,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return serviceFromRecord(updated)
}

const (
	extendedMetadataSkillType = 1
)

type extendedMetadataWire struct {
	Type            json.RawMessage `json:"type"`
	AgentSkill      json.RawMessage `json:"agent_skill,omitempty"`
	AgentSkillCamel json.RawMessage `json:"agentSkill,omitempty"`
}

type agentSkillWire struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name,omitempty"`
	Description          string          `json:"description,omitempty"`
	Tags                 []string        `json:"tags,omitempty"`
	Examples             []string        `json:"examples,omitempty"`
	InputModes           []string        `json:"input_modes,omitempty"`
	OutputModes          []string        `json:"output_modes,omitempty"`
	InputModesCamel      []string        `json:"inputModes,omitempty"`
	OutputModesCamel     []string        `json:"outputModes,omitempty"`
	Version              string          `json:"version,omitempty"`
	Security             json.RawMessage `json:"security,omitempty"`
	SecurityRequirements json.RawMessage `json:"securityRequirements,omitempty"`
}

func serviceFromRecord(record *serviceadmin.ServiceRecord) (*model.AgentService, error) {
	if record == nil {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"service admin API returned an empty service")
	}
	if record.Type != model.ServiceTypeAIAgent {
		return nil, model.NewSDKError(model.ErrCodeServiceTypeConflict, nil,
			"service %s/%s has type %s", record.Namespace, record.Name, record.Type)
	}
	extended := make([]*model.ExtendedMetadata, 0, len(record.ExtendedMetadata))
	for _, raw := range record.ExtendedMetadata {
		wire, id, isSkill, err := decodeExtendedMetadata(raw)
		if err != nil {
			return nil, err
		}
		if !isSkill {
			extended = append(extended, &model.ExtendedMetadata{Type: extendedMetadataType(wire.Type)})
			continue
		}
		skill, err := decodeAgentSkill(wire.AgentSkill, wire.AgentSkillCamel)
		if err != nil {
			return nil, err
		}
		if skill.ID == "" {
			skill.ID = id
		}
		extended = append(extended, &model.ExtendedMetadata{
			Type:       model.ExtendedMetadataSkill,
			AgentSkill: skill,
		})
	}
	return &model.AgentService{
		Namespace:        record.Namespace,
		Name:             record.Name,
		Type:             record.Type,
		Metadata:         cloneMap(record.Metadata),
		ExtendedMetadata: extended,
		Owners:           record.Owners,
		Revision:         record.Revision,
	}, nil
}

func buildSkillExtendedMetadata(skills []*model.AgentSkill) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0, len(skills))
	for _, skill := range skills {
		item, err := encodeSkillExtendedMetadata(skill)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func mergeSkills(
	existing []json.RawMessage, skills []*model.AgentSkill, replace bool, removeIDs []string,
) ([]json.RawMessage, error) {
	removed := make(map[string]struct{}, len(removeIDs))
	for _, id := range removeIDs {
		removed[id] = struct{}{}
	}
	result := make([]json.RawMessage, 0, len(existing)+len(skills))
	indexes := make(map[string]int)
	for _, raw := range existing {
		_, id, isSkill, err := decodeExtendedMetadata(raw)
		if err != nil {
			return nil, err
		}
		if !isSkill {
			result = append(result, raw)
			continue
		}
		if _, ok := removed[id]; ok {
			continue
		}
		if replace {
			continue
		}
		indexes[id] = len(result)
		result = append(result, raw)
	}
	for _, skill := range skills {
		item, err := encodeSkillExtendedMetadata(skill)
		if err != nil {
			return nil, err
		}
		if index, ok := indexes[skill.ID]; ok {
			result[index] = item
			continue
		}
		indexes[skill.ID] = len(result)
		result = append(result, item)
	}
	return result, nil
}

func decodeExtendedMetadata(raw json.RawMessage) (*extendedMetadataWire, string, bool, error) {
	var wire extendedMetadataWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, "", false, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode extended metadata item")
	}
	typeValue := strings.Trim(string(wire.Type), `"`)
	if typeValue != fmt.Sprint(extendedMetadataSkillType) &&
		!strings.EqualFold(typeValue, "EXTENDED_METADATA_SKILL") {
		return &wire, "", false, nil
	}
	skill, err := decodeAgentSkill(wire.AgentSkill, wire.AgentSkillCamel)
	if err != nil {
		return nil, "", false, err
	}
	return &wire, skill.ID, true, nil
}

func decodeAgentSkill(values ...json.RawMessage) (*model.AgentSkill, error) {
	var raw json.RawMessage
	for _, value := range values {
		if len(value) > 0 && string(value) != "null" {
			raw = value
			break
		}
	}
	if len(raw) == 0 {
		return &model.AgentSkill{}, nil
	}
	var skill agentSkillWire
	if err := json.Unmarshal(raw, &skill); err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode AgentSkill")
	}
	inputModes := skill.InputModes
	if inputModes == nil {
		inputModes = skill.InputModesCamel
	}
	outputModes := skill.OutputModes
	if outputModes == nil {
		outputModes = skill.OutputModesCamel
	}
	return &model.AgentSkill{
		ID:                   skill.ID,
		Name:                 skill.Name,
		Description:          skill.Description,
		Tags:                 append([]string(nil), skill.Tags...),
		Examples:             append([]string(nil), skill.Examples...),
		InputModes:           append([]string(nil), inputModes...),
		OutputModes:          append([]string(nil), outputModes...),
		Version:              skill.Version,
		Security:             append(json.RawMessage(nil), skill.Security...),
		SecurityRequirements: append(json.RawMessage(nil), skill.SecurityRequirements...),
	}, nil
}

func encodeSkillExtendedMetadata(skill *model.AgentSkill) (json.RawMessage, error) {
	if skill == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "AgentSkill can not be nil")
	}
	payload, err := json.Marshal(agentSkillWire{
		ID:                   skill.ID,
		Name:                 skill.Name,
		Description:          skill.Description,
		Tags:                 skill.Tags,
		Examples:             skill.Examples,
		InputModes:           skill.InputModes,
		OutputModes:          skill.OutputModes,
		Version:              skill.Version,
		Security:             skill.Security,
		SecurityRequirements: skill.SecurityRequirements,
	})
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to encode AgentSkill")
	}
	raw, err := json.Marshal(struct {
		Type       int             `json:"type"`
		AgentSkill json.RawMessage `json:"agent_skill"`
	}{
		Type:       extendedMetadataSkillType,
		AgentSkill: payload,
	})
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to encode extended metadata")
	}
	return raw, nil
}

func extendedMetadataType(raw json.RawMessage) model.ExtendedMetadataType {
	value := strings.Trim(string(raw), `"`)
	if value == "" {
		return model.ExtendedMetadataUnknown
	}
	number, err := strconv.ParseInt(value, 10, 32)
	if err == nil {
		return model.ExtendedMetadataType(number)
	}
	return model.ExtendedMetadataUnknown
}

func cloneMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
