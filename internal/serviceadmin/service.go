/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package serviceadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
	modelpb "github.com/polarismesh/polaris-go/pkg/model/pb"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentmodel"
)

type serviceRecord struct {
	Service *apiservice.Service
	Raw     map[string]json.RawMessage
}

type serviceWriteState struct {
	confirmed   bool
	metadata    map[string]string
	hasMetadata bool
	extended    []json.RawMessage
	hasExtended bool
	pending     *serviceWritePayload
	method      string
	uncertain   bool
	revision    string
	superseded  map[string]struct{}
}

const serviceReadAfterWriteInterval = 100 * time.Millisecond

type serviceWritePayload struct {
	Name                 string             `json:"name"`
	Namespace            string             `json:"namespace"`
	ServiceType          string             `json:"service_type,omitempty"`
	Metadata             *map[string]string `json:"metadata,omitempty"`
	ExtendedMetadata     *[]json.RawMessage `json:"extended_metadata,omitempty"`
	Revision             string             `json:"revision,omitempty"`
	Owners               string             `json:"owners,omitempty"`
	ExportTo             []string           `json:"export_to,omitempty"`
	SyncToGlobalRegistry bool               `json:"sync_to_global_registry"`
}

type serviceDeletePayload struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Revision  string `json:"revision,omitempty"`
}

type extendedMetadataPayload struct {
	Type            json.RawMessage `json:"type"`
	AgentSkill      json.RawMessage `json:"agent_skill,omitempty"`
	AgentSkillCamel json.RawMessage `json:"agentSkill,omitempty"`
}

type agentSkillPayload struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"input_modes,omitempty"`
	OutputModes []string `json:"output_modes,omitempty"`
	Version     string   `json:"version,omitempty"`
}

type queryEnvelope struct {
	Code     uint32            `json:"code"`
	Info     string            `json:"info"`
	Services []json.RawMessage `json:"services"`
}

type writeEnvelope struct {
	Code      uint32            `json:"code"`
	Info      string            `json:"info"`
	Responses []json.RawMessage `json:"responses"`
}

type responseEnvelope struct {
	Code    uint32          `json:"code"`
	Info    string          `json:"info"`
	Service json.RawMessage `json:"service"`
}

type consoleServicePayload struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Namespace            string            `json:"namespace"`
	Metadata             map[string]string `json:"metadata"`
	Revision             string            `json:"revision"`
	ServiceType          json.RawMessage   `json:"service_type"`
	Owners               string            `json:"owners"`
	TotalInstanceCount   *uint32           `json:"total_instance_count"`
	ExportTo             []string          `json:"export_to"`
	SyncToGlobalRegistry bool              `json:"sync_to_global_registry"`
}

// GetService 从 Console 精确读取最新服务。
func (c *Client) GetService(namespace, service string) (*serviceRecord, error) {
	ctx, cancel := c.context()
	defer cancel()
	return c.getService(ctx, namespace, service)
}

// GetServiceWithRequest reads one service using request-level deadline settings.
func (c *Client) GetServiceWithRequest(
	req *GetServiceRequest,
) (*serviceRecord, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := c.contextWithTimeout(req.Timeout)
	defer cancel()
	return c.getService(ctx, req.Namespace, req.Service)
}

func (c *Client) getService(ctx context.Context, namespace, service string) (*serviceRecord, error) {
	query := url.Values{}
	query.Set("namespace", namespace)
	query.Set("name", service)
	body, status, err := c.doJSON(ctx, http.MethodGet, serviceRequestPath, query, nil)
	if err != nil {
		if status == http.StatusNotFound {
			return nil, model.NewSDKError(model.ErrCodeServiceNotFound, err,
				"service %s/%s not found", namespace, service)
		}
		return nil, err
	}
	var envelope queryEnvelope
	if err = json.Unmarshal(body, &envelope); err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode Console service query response")
	}
	if envelope.Code == uint32(apimodel.Code_NotFoundResource) {
		return nil, model.NewSDKError(model.ErrCodeServiceNotFound, nil,
			"service %s/%s not found", namespace, service)
	}
	if envelope.Code != uint32(apimodel.Code_ExecuteSuccess) {
		return nil, model.NewSDKErrorWithServerInfo(model.ErrCodeServiceWriteError, nil,
			envelope.Code, envelope.Info, "Console service query returned an error")
	}
	for _, rawService := range envelope.Services {
		record, recordErr := decodeService(rawService)
		if recordErr != nil {
			return nil, recordErr
		}
		if record.Service.GetNamespace().GetValue() == namespace &&
			record.Service.GetName().GetValue() == service {
			return record, nil
		}
	}
	return nil, model.NewSDKError(model.ErrCodeServiceNotFound, nil,
		"service %s/%s not found", namespace, service)
}

// EnsureService creates a service or applies a generic metadata update.
func (c *Client) EnsureService(req *EnsureServiceRequest) (*serviceRecord, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := c.context()
	defer cancel()
	unlock, err := c.lockServiceWrite(ctx, req.Namespace, req.Service)
	if err != nil {
		return nil, err
	}
	defer unlock()

	record, err := c.getServiceForUpdate(ctx, req.Namespace, req.Service)
	if err == nil {
		if typeErr := validateServiceType(record, req.Namespace, req.Service, &req.Type); typeErr != nil {
			return nil, typeErr
		}
		return c.updateExistingService(ctx, record, req.ServiceWriteRequest)
	}
	if model.GetErrorCodeFromError(err) != model.ErrCodeServiceNotFound {
		return nil, err
	}
	if req.Revision != "" {
		return nil, model.NewSDKError(model.ErrCodeServicePreconditionFailed, nil,
			"service %s/%s does not exist for requested revision %q",
			req.Namespace, req.Service, req.Revision)
	}
	return c.createService(ctx, req.ServiceWriteRequest)
}

// UpdateService updates an existing service without creating it.
func (c *Client) UpdateService(req *UpdateServiceRequest) (*serviceRecord, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := c.context()
	defer cancel()
	unlock, err := c.lockServiceWrite(ctx, req.Namespace, req.Service)
	if err != nil {
		return nil, err
	}
	defer unlock()

	record, err := c.getServiceForUpdate(ctx, req.Namespace, req.Service)
	if err != nil {
		return nil, err
	}
	if err = validateServiceType(record, req.Namespace, req.Service, &req.Type); err != nil {
		return nil, err
	}
	return c.updateExistingService(ctx, record, req.ServiceWriteRequest)
}

// DeleteService deletes a generic service with optional type/revision/owner guards.
func (c *Client) DeleteService(req *DeleteServiceRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	ctx, cancel := c.context()
	defer cancel()
	unlock, err := c.lockServiceWrite(ctx, req.Namespace, req.Service)
	if err != nil {
		return err
	}
	defer unlock()
	record, err := c.getServiceForUpdate(ctx, req.Namespace, req.Service)
	if err != nil {
		if model.GetErrorCodeFromError(err) == model.ErrCodeServiceNotFound {
			return nil
		}
		return err
	}
	if err = validateServiceType(record, req.Namespace, req.Service, req.Type); err != nil {
		return err
	}
	revision := record.Service.GetRevision().GetValue()
	if req.Revision != "" && req.Revision != revision {
		return model.NewSDKError(model.ErrCodeServicePreconditionFailed, nil,
			"service %s/%s revision %q does not match requested revision %q",
			req.Namespace, req.Service, revision, req.Revision)
	}
	if req.Owner != "" && !serviceHasOwner(record.Service.GetOwners().GetValue(), req.Owner) {
		return model.NewSDKError(model.ErrCodeServicePreconditionFailed, nil,
			"service %s/%s owners do not contain requested owner %q",
			req.Namespace, req.Service, req.Owner)
	}
	instances := record.Service.GetTotalInstanceCount().GetValue()
	if instances > 0 && !req.Force {
		return model.NewSDKError(model.ErrCodeServiceNotEmpty, nil,
			"service %s/%s still has %d instances; set Force to allow deletion attempt",
			req.Namespace, req.Service, instances)
	}
	if err = c.writeDeleteService(ctx, serviceDeletePayload{
		Name: req.Service, Namespace: req.Namespace, Revision: revision,
	}); err != nil {
		return err
	}
	if err = c.getServiceAfterDelete(ctx, req.Namespace, req.Service); err != nil {
		return err
	}
	c.clearServiceWriteState(req.Namespace, req.Service)
	return nil
}

func (c *Client) createService(
	ctx context.Context,
	req ServiceWriteRequest,
) (*serviceRecord, error) {
	var metadata *map[string]string
	if req.Metadata != nil {
		values := cloneMap(*req.Metadata)
		metadata = &values
	}
	var extended *[]json.RawMessage
	if req.ExtendedMetadata != nil {
		values := cloneRawMessages(*req.ExtendedMetadata)
		extended = &values
	}
	payload := serviceWritePayload{
		Name:             req.Service,
		Namespace:        req.Namespace,
		ServiceType:      req.Type.String(),
		Metadata:         metadata,
		ExtendedMetadata: extended,
	}
	idempotentNoNeed := extended == nil || len(*extended) > 0
	writeErr := c.writeService(ctx, http.MethodPost, payload, idempotentNoNeed)
	if writeErr != nil {
		if model.GetErrorCodeFromError(writeErr) == model.ErrCodeServiceWriteError {
			existing, getErr := c.getServiceForUpdate(ctx, req.Namespace, req.Service)
			if getErr == nil {
				if typeErr := validateServiceType(
					existing, req.Namespace, req.Service, &req.Type); typeErr != nil {
					return nil, typeErr
				}
				return c.updateExistingService(ctx, existing, req)
			}
		}
		return nil, writeErr
	}
	record, err := c.getServiceAfterWrite(ctx, req.Namespace, req.Service, metadata, extended)
	if err != nil {
		return nil, err
	}
	c.rememberConfirmedServiceState(
		req.Namespace, req.Service, metadata, extended, serviceRecordRevision(record))
	if err = validateServiceType(record, req.Namespace, req.Service, &req.Type); err != nil {
		return nil, err
	}
	return record, nil
}

func (c *Client) updateExistingService(
	ctx context.Context,
	record *serviceRecord,
	req ServiceWriteRequest,
) (*serviceRecord, error) {
	revision := serviceRecordRevision(record)
	if req.Revision != "" && req.Revision != revision {
		return nil, model.NewSDKError(model.ErrCodeServicePreconditionFailed, nil,
			"service %s/%s revision %q does not match requested revision %q",
			req.Namespace, req.Service, revision, req.Revision)
	}
	if req.Metadata == nil && req.ExtendedMetadata == nil {
		return record, nil
	}
	var metadata *map[string]string
	if req.Metadata != nil {
		values := cloneMap(*req.Metadata)
		metadata = &values
	}
	var extended *[]json.RawMessage
	if req.ExtendedMetadata != nil {
		values := cloneRawMessages(*req.ExtendedMetadata)
		extended = &values
	}
	return c.updateServiceState(ctx, record, metadata, extended, true)
}

// EnsureAgentService 确保 AI Agent 服务存在。
func (c *Client) EnsureAgentService(req *agentmodel.EnsureAgentServiceRequest) (*serviceRecord, error) {
	ctx, cancel := c.context()
	defer cancel()
	unlock, err := c.lockServiceWrite(ctx, req.Namespace, req.Service)
	if err != nil {
		return nil, err
	}
	defer unlock()
	record, err := c.getServiceForUpdate(ctx, req.Namespace, req.Service)
	if err == nil {
		if typeErr := validateAgentServiceType(record, req.Namespace, req.Service); typeErr != nil {
			return nil, typeErr
		}
		return c.updateExistingAgentService(ctx, record, req)
	}
	if model.GetErrorCodeFromError(err) != model.ErrCodeServiceNotFound {
		return nil, err
	}
	skills, normalizeErr := model.NormalizeAgentSkillValues(req.Skills)
	if normalizeErr != nil {
		return nil, normalizeErr
	}
	extended, err := buildSkillExtendedMetadata(skills)
	if err != nil {
		return nil, err
	}
	metadata := cloneMap(req.Metadata)
	payload := serviceWritePayload{
		Name:             req.Service,
		Namespace:        req.Namespace,
		ServiceType:      apiservice.ServiceType_SERVICE_TYPE_AI_AGENT.String(),
		ExtendedMetadata: &extended,
	}
	if req.Metadata != nil {
		payload.Metadata = &metadata
	}
	writeErr := c.writeService(ctx, http.MethodPost, payload, false)
	if writeErr != nil {
		if model.GetErrorCodeFromError(writeErr) == model.ErrCodeServiceWriteError {
			existing, getErr := c.getServiceForUpdate(ctx, req.Namespace, req.Service)
			if getErr == nil {
				if existing.Service.GetServiceType() != apiservice.ServiceType_SERVICE_TYPE_AI_AGENT {
					return nil, model.NewSDKError(model.ErrCodeServiceTypeConflict, writeErr,
						"service %s/%s already exists with type %s",
						req.Namespace, req.Service, existing.Service.GetServiceType())
				}
				return c.updateExistingAgentService(ctx, existing, req)
			}
		}
		return nil, writeErr
	}
	record, err = c.getServiceAfterWrite(ctx, req.Namespace, req.Service, payload.Metadata, &extended)
	if err != nil {
		return nil, err
	}
	c.rememberConfirmedServiceState(
		req.Namespace, req.Service, payload.Metadata, &extended, serviceRecordRevision(record))
	return record, validateAgentServiceType(record, req.Namespace, req.Service)
}

func (c *Client) updateExistingAgentService(
	ctx context.Context, record *serviceRecord, req *agentmodel.EnsureAgentServiceRequest,
) (*serviceRecord, error) {
	if req.Metadata == nil && req.Skills == nil {
		return record, nil
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
	return c.updateServiceState(ctx, record, metadata, extended, true)
}

// MountAgentSkills 合并或整体替换服务级 Skill。
func (c *Client) MountAgentSkills(
	namespace, service string, skills []*model.AgentSkill, replace bool,
) (*serviceRecord, error) {
	ctx, cancel := c.context()
	defer cancel()
	unlock, err := c.lockServiceWrite(ctx, namespace, service)
	if err != nil {
		return nil, err
	}
	defer unlock()
	record, err := c.getServiceForUpdate(ctx, namespace, service)
	if err != nil {
		return nil, err
	}
	if record.Service.GetServiceType() != apiservice.ServiceType_SERVICE_TYPE_AI_AGENT {
		return nil, model.NewSDKError(model.ErrCodeServiceTypeConflict, nil,
			"service %s/%s is not AI_AGENT", namespace, service)
	}
	normalized, err := model.NormalizeAgentSkills(skills)
	if err != nil {
		return nil, err
	}
	extended, err := mergeExtendedMetadata(record.Raw, normalized, replace, nil)
	if err != nil {
		return nil, err
	}
	return c.updateExtendedMetadata(ctx, record, extended)
}

// UnmountAgentSkills 删除指定 ID 的 Skill 引用。
func (c *Client) UnmountAgentSkills(
	namespace, service string, skillIDs []string,
) (*serviceRecord, error) {
	ctx, cancel := c.context()
	defer cancel()
	unlock, err := c.lockServiceWrite(ctx, namespace, service)
	if err != nil {
		return nil, err
	}
	defer unlock()
	record, err := c.getServiceForUpdate(ctx, namespace, service)
	if err != nil {
		return nil, err
	}
	if record.Service.GetServiceType() != apiservice.ServiceType_SERVICE_TYPE_AI_AGENT {
		return nil, model.NewSDKError(model.ErrCodeServiceTypeConflict, nil,
			"service %s/%s is not AI_AGENT", namespace, service)
	}
	extended, err := mergeExtendedMetadata(record.Raw, nil, false, skillIDs)
	if err != nil {
		return nil, err
	}
	return c.updateExtendedMetadata(ctx, record, extended)
}

// DeleteAgentService 删除 AI Agent 服务。
func (c *Client) DeleteAgentService(req *agentmodel.DeleteAgentServiceRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	ctx, cancel := c.context()
	defer cancel()
	unlock, err := c.lockServiceWrite(ctx, req.Namespace, req.Service)
	if err != nil {
		return err
	}
	defer unlock()
	record, err := c.getServiceForUpdate(ctx, req.Namespace, req.Service)
	if err != nil {
		if model.GetErrorCodeFromError(err) == model.ErrCodeServiceNotFound {
			return nil
		}
		return err
	}
	if err = validateAgentServiceType(record, req.Namespace, req.Service); err != nil {
		return err
	}
	revision := record.Service.GetRevision().GetValue()
	if req.Revision != "" && req.Revision != revision {
		return model.NewSDKError(model.ErrCodeServicePreconditionFailed, nil,
			"service %s/%s revision %q does not match requested revision %q",
			req.Namespace, req.Service, revision, req.Revision)
	}
	if req.Owner != "" && !serviceHasOwner(record.Service.GetOwners().GetValue(), req.Owner) {
		return model.NewSDKError(model.ErrCodeServicePreconditionFailed, nil,
			"service %s/%s owners do not contain requested owner %q",
			req.Namespace, req.Service, req.Owner)
	}
	instances := record.Service.GetTotalInstanceCount().GetValue()
	if instances > 0 && !req.Force {
		return model.NewSDKError(model.ErrCodeServiceNotEmpty, nil,
			"service %s/%s still has %d instances; set Force to allow deletion attempt",
			req.Namespace, req.Service, instances)
	}
	if err = c.writeDeleteService(ctx, serviceDeletePayload{
		Name: req.Service, Namespace: req.Namespace, Revision: revision,
	}); err != nil {
		return err
	}
	if err = c.getServiceAfterDelete(ctx, req.Namespace, req.Service); err != nil {
		return err
	}
	c.clearServiceWriteState(req.Namespace, req.Service)
	return nil
}

func (c *Client) updateExtendedMetadata(
	ctx context.Context, record *serviceRecord, extended []json.RawMessage,
) (*serviceRecord, error) {
	return c.updateServiceState(ctx, record, nil, &extended, len(extended) == 0)
}

func (c *Client) updateServiceState(
	ctx context.Context, record *serviceRecord, metadata *map[string]string,
	extended *[]json.RawMessage, checkMatched bool,
) (*serviceRecord, error) {
	if record == nil || record.Service == nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, nil,
			"service update record is empty")
	}
	if checkMatched {
		matched, err := serviceTargetMatches(record, metadata, extended)
		if err != nil {
			return nil, err
		}
		if matched {
			namespace := record.Service.GetNamespace().GetValue()
			service := record.Service.GetName().GetValue()
			c.rememberConfirmedServiceState(
				namespace, service, metadata, extended, serviceRecordRevision(record))
			return record, nil
		}
	}
	payload := serviceWritePayload{
		Name:                 record.Service.GetName().GetValue(),
		Namespace:            record.Service.GetNamespace().GetValue(),
		ServiceType:          record.Service.GetServiceType().String(),
		Revision:             record.Service.GetRevision().GetValue(),
		ExportTo:             serviceExportTo(record.Service.GetExportTo()),
		SyncToGlobalRegistry: record.Service.GetSyncToGlobalRegistry(),
	}
	if metadata != nil {
		values := cloneMap(*metadata)
		payload.Metadata = &values
	}
	if extended != nil {
		values := cloneRawMessages(*extended)
		payload.ExtendedMetadata = &values
	}
	idempotentNoNeed := extended == nil || len(*extended) > 0
	if err := c.writeService(ctx, http.MethodPut, payload, idempotentNoNeed); err != nil {
		return nil, err
	}
	record, err := c.getServiceAfterWrite(
		ctx, payload.Namespace, payload.Name, payload.Metadata, payload.ExtendedMetadata)
	if err != nil {
		return nil, err
	}
	c.rememberConfirmedServiceState(
		payload.Namespace, payload.Name, payload.Metadata, payload.ExtendedMetadata,
		serviceRecordRevision(record))
	return record, nil
}

func (c *Client) getServiceForUpdate(
	ctx context.Context, namespace, service string,
) (*serviceRecord, error) {
	state, ok := c.serviceWriteStateForUpdate(namespace, service)
	if !ok {
		return c.getService(ctx, namespace, service)
	}
	expectedMetadata := metadataFromState(state)
	expectedExtended := extendedFromState(state)
	if state.pending != nil {
		expectedMetadata = cloneMetadataPtr(state.pending.Metadata)
		expectedExtended = cloneRawMessagePtr(state.pending.ExtendedMetadata)
	}
	var lastErr error
	for {
		record, err := c.getService(ctx, namespace, service)
		if err == nil {
			matched, matchErr := serviceTargetMatches(record, expectedMetadata, expectedExtended)
			if matchErr != nil {
				return nil, matchErr
			}
			if matched {
				c.rememberConfirmedServiceState(
					namespace, service, expectedMetadata, expectedExtended, serviceRecordRevision(record))
				return record, nil
			}
			// A successful write can still lose a race with an external writer
			// before read-after-write observes the target state. Accept a new,
			// unseen revision so the next explicit update can start from it
			// instead of blocking this service forever on a stale pending target.
			if !state.uncertain &&
				serviceStateAcceptsExternalRevision(state, serviceRecordRevision(record)) {
				metadata := cloneMap(record.Service.GetMetadata())
				extended, extendedErr := recordExtendedMetadata(record)
				if extendedErr != nil {
					return nil, extendedErr
				}
				c.rememberConfirmedServiceState(
					namespace, service, &metadata, &extended, serviceRecordRevision(record))
				return record, nil
			}
			lastErr = model.NewSDKError(model.ErrCodeServiceWriteError, nil,
				"service %s/%s has not reached the last confirmed state", namespace, service)
		} else if model.GetErrorCodeFromError(err) != model.ErrCodeServiceNotFound {
			return nil, err
		} else {
			if state.pending == nil {
				c.clearServiceWriteState(namespace, service)
				return nil, err
			}
			lastErr = err
		}
		if state.pending != nil && state.uncertain {
			idempotentNoNeed := state.pending.ExtendedMetadata == nil ||
				len(*state.pending.ExtendedMetadata) > 0
			if err = c.writeService(ctx, state.method, *state.pending, idempotentNoNeed); err != nil {
				return nil, err
			}
			state.uncertain = false
			continue
		}
		select {
		case <-ctx.Done():
			confirmErr := ctx.Err()
			if lastErr != nil {
				confirmErr = lastErr
			}
			return nil, model.NewSDKError(model.ErrCodeServiceWriteError, confirmErr,
				"previous service write for %s/%s was not confirmed before timeout", namespace, service)
		case <-time.After(serviceReadAfterWriteInterval):
		}
	}
}

func (c *Client) serviceWriteStateForUpdate(namespace, service string) (serviceWriteState, bool) {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	state, ok := c.serviceStates[serviceStateKey(namespace, service)]
	if !ok || (!state.confirmed && state.pending == nil) {
		return serviceWriteState{}, false
	}
	state.extended = cloneRawMessages(state.extended)
	state.metadata = cloneMap(state.metadata)
	state.superseded = cloneStringSet(state.superseded)
	if state.pending != nil {
		payload := *state.pending
		payload.Metadata = cloneMetadataPtr(payload.Metadata)
		payload.ExtendedMetadata = cloneRawMessagePtr(payload.ExtendedMetadata)
		state.pending = &payload
	}
	return state, true
}

func (c *Client) rememberPendingServiceWrite(method string, payload serviceWritePayload, uncertain bool) {
	if payload.ExtendedMetadata == nil && payload.Metadata == nil {
		return
	}
	payload.Metadata = cloneMetadataPtr(payload.Metadata)
	payload.ExtendedMetadata = cloneRawMessagePtr(payload.ExtendedMetadata)
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	if c.serviceStates == nil {
		c.serviceStates = make(map[string]serviceWriteState)
	}
	key := serviceStateKey(payload.Namespace, payload.Name)
	state := c.serviceStates[key]
	state.pending = &payload
	state.method = method
	state.uncertain = uncertain
	if payload.Revision != "" {
		state.revision = payload.Revision
	}
	if payload.Metadata != nil {
		state.hasMetadata = true
		state.metadata = cloneMap(*payload.Metadata)
	}
	if payload.ExtendedMetadata != nil {
		state.hasExtended = true
		state.extended = cloneRawMessages(*payload.ExtendedMetadata)
	}
	c.serviceStates[key] = state
}

func (c *Client) rememberConfirmedExtendedMetadata(
	namespace, service string, extended []json.RawMessage,
) {
	c.rememberConfirmedServiceState(namespace, service, nil, &extended, "")
}

func (c *Client) rememberConfirmedServiceState(
	namespace, service string, metadata *map[string]string, extended *[]json.RawMessage,
	revision string,
) {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	if c.serviceStates == nil {
		c.serviceStates = make(map[string]serviceWriteState)
	}
	key := serviceStateKey(namespace, service)
	state := c.serviceStates[key]
	superseded := cloneStringSet(state.superseded)
	if state.pending != nil {
		superseded = addSupersededRevision(superseded, state.pending.Revision)
	}
	if revision != "" && state.revision != "" && state.revision != revision {
		superseded = addSupersededRevision(superseded, state.revision)
	}
	state.confirmed = true
	state.pending = nil
	state.method = ""
	state.uncertain = false
	state.superseded = superseded
	if revision != "" {
		state.revision = revision
	}
	if metadata != nil {
		state.hasMetadata = true
		state.metadata = cloneMap(*metadata)
	}
	if extended != nil {
		state.hasExtended = true
		state.extended = cloneRawMessages(*extended)
	}
	c.serviceStates[key] = state
}

func serviceRecordRevision(record *serviceRecord) string {
	if record == nil || record.Service == nil || record.Service.GetRevision() == nil {
		return ""
	}
	return record.Service.GetRevision().GetValue()
}

func serviceStateAcceptsExternalRevision(state serviceWriteState, revision string) bool {
	if revision == "" || state.revision == "" || revision == state.revision {
		return false
	}
	_, stale := state.superseded[revision]
	return !stale
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(values))
	for value := range values {
		result[value] = struct{}{}
	}
	return result
}

func addSupersededRevision(values map[string]struct{}, revision string) map[string]struct{} {
	if revision == "" {
		return values
	}
	if values == nil {
		values = make(map[string]struct{})
	}
	values[revision] = struct{}{}
	return values
}

func serviceStateKey(namespace, service string) string {
	return namespace + "\x00" + service
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	if values == nil {
		return nil
	}
	result := make([]json.RawMessage, len(values))
	for index := range values {
		result[index] = append(json.RawMessage(nil), values[index]...)
	}
	return result
}

func cloneRawMessagePtr(values *[]json.RawMessage) *[]json.RawMessage {
	if values == nil {
		return nil
	}
	cloned := cloneRawMessages(*values)
	return &cloned
}

func cloneMetadataPtr(values *map[string]string) *map[string]string {
	if values == nil {
		return nil
	}
	cloned := cloneMap(*values)
	return &cloned
}

func metadataFromState(state serviceWriteState) *map[string]string {
	if !state.hasMetadata {
		return nil
	}
	values := cloneMap(state.metadata)
	return &values
}

func extendedFromState(state serviceWriteState) *[]json.RawMessage {
	if !state.hasExtended {
		return nil
	}
	values := cloneRawMessages(state.extended)
	return &values
}

func serviceTargetMatches(
	record *serviceRecord, expectedMetadata *map[string]string,
	expectedExtended *[]json.RawMessage,
) (bool, error) {
	if expectedMetadata != nil {
		if record == nil || record.Service == nil ||
			!stringMapsEqual(record.Service.GetMetadata(), *expectedMetadata) {
			return false, nil
		}
	}
	if expectedExtended != nil {
		return extendedMetadataMatches(record, *expectedExtended)
	}
	return true, nil
}

func stringMapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func (c *Client) getServiceAfterWrite(
	ctx context.Context, namespace, service string, expectedMetadata *map[string]string,
	expectedExtended *[]json.RawMessage,
) (*serviceRecord, error) {
	var lastErr error
	for {
		record, err := c.getService(ctx, namespace, service)
		if err == nil {
			matched, matchErr := serviceTargetMatches(record, expectedMetadata, expectedExtended)
			if matchErr != nil {
				return nil, matchErr
			}
			if matched {
				return record, nil
			}
			lastErr = nil
		} else if model.GetErrorCodeFromError(err) != model.ErrCodeServiceNotFound {
			return nil, err
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			confirmErr := ctx.Err()
			if lastErr != nil {
				confirmErr = lastErr
			}
			return nil, model.NewSDKError(model.ErrCodeServiceWriteError, confirmErr,
				"service write for %s/%s was not confirmed before timeout", namespace, service)
		case <-time.After(serviceReadAfterWriteInterval):
		}
	}
}

func serviceExportTo(values []*wrapperspb.StringValue) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.GetValue())
	}
	return result
}

func (c *Client) writeService(ctx context.Context, method string, payload serviceWritePayload, idempotentNoNeed bool) error {
	result := c.doJSONWithAttempts(ctx, method, serviceRequestPath, nil, []serviceWritePayload{payload})
	if result.err != nil {
		if result.uncertain || result.status >= http.StatusInternalServerError {
			c.rememberPendingServiceWrite(method, payload, true)
		}
		return result.err
	}
	var envelope writeEnvelope
	if err := json.Unmarshal(result.body, &envelope); err != nil {
		c.rememberPendingServiceWrite(method, payload, true)
		return model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode Console service write response")
	}
	if envelope.Code == uint32(apimodel.Code_NoNeedUpdate) {
		if idempotentNoNeed {
			c.rememberPendingServiceWrite(method, payload, result.uncertain)
			return nil
		}
		if result.uncertain {
			c.rememberPendingServiceWrite(method, payload, true)
		}
		return model.NewSDKErrorWithServerInfo(model.ErrCodeServiceWriteUnsupported, nil,
			envelope.Code, envelope.Info,
			"Console service write was not applied; explicit empty extended_metadata requires server support")
	}
	if envelope.Code != uint32(apimodel.Code_ExecuteSuccess) {
		if result.uncertain {
			c.rememberPendingServiceWrite(method, payload, true)
		}
		return model.NewSDKErrorWithServerInfo(model.ErrCodeServiceWriteError, nil,
			envelope.Code, envelope.Info, "Console service write returned an error")
	}
	if len(envelope.Responses) == 0 {
		c.rememberPendingServiceWrite(method, payload, true)
		return model.NewSDKError(model.ErrCodeServiceWriteError, nil,
			"Console service write returned no item response")
	}
	var response responseEnvelope
	if err := json.Unmarshal(envelope.Responses[0], &response); err != nil {
		c.rememberPendingServiceWrite(method, payload, true)
		return model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode Console service write item")
	}
	if response.Code == uint32(apimodel.Code_NoNeedUpdate) {
		if idempotentNoNeed {
			c.rememberPendingServiceWrite(method, payload, result.uncertain)
			return nil
		}
		if result.uncertain {
			c.rememberPendingServiceWrite(method, payload, true)
		}
		return model.NewSDKErrorWithServerInfo(model.ErrCodeServiceWriteUnsupported, nil,
			response.Code, response.Info,
			"Console service write was not applied; explicit empty extended_metadata requires server support")
	}
	if response.Code != uint32(apimodel.Code_ExecuteSuccess) {
		if result.uncertain {
			c.rememberPendingServiceWrite(method, payload, true)
		}
		return model.NewSDKErrorWithServerInfo(model.ErrCodeServiceWriteError, nil,
			response.Code, response.Info, "Console service write item returned an error")
	}
	c.rememberPendingServiceWrite(method, payload, false)
	return nil
}

func (c *Client) writeDeleteService(ctx context.Context, payload serviceDeletePayload) error {
	result := c.doJSONWithAttempts(
		ctx, http.MethodPost, serviceDeleteRequestPath, nil, []serviceDeletePayload{payload})
	if result.err != nil {
		if result.status == http.StatusNotFound {
			return nil
		}
		return result.err
	}
	var envelope writeEnvelope
	if err := json.Unmarshal(result.body, &envelope); err != nil {
		return model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode Console service delete response")
	}
	if err := validateDeleteResponseCode(envelope.Code, envelope.Info); err != nil {
		return err
	}
	if len(envelope.Responses) == 0 {
		return model.NewSDKError(model.ErrCodeServiceWriteError, nil,
			"Console service delete returned no item response")
	}
	var response responseEnvelope
	if err := json.Unmarshal(envelope.Responses[0], &response); err != nil {
		return model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode Console service delete item")
	}
	return validateDeleteResponseCode(response.Code, response.Info)
}

func validateDeleteResponseCode(code uint32, info string) error {
	switch code {
	case uint32(apimodel.Code_ExecuteSuccess), uint32(apimodel.Code_NoNeedUpdate),
		uint32(apimodel.Code_NotFoundResource):
		return nil
	default:
		return model.NewSDKErrorWithServerInfo(model.ErrCodeServiceWriteError, nil,
			code, info, "Console service delete returned an error")
	}
}

func (c *Client) getServiceAfterDelete(ctx context.Context, namespace, service string) error {
	var lastErr error
	for {
		_, err := c.getService(ctx, namespace, service)
		if model.GetErrorCodeFromError(err) == model.ErrCodeServiceNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		lastErr = model.NewSDKError(model.ErrCodeServiceWriteError, nil,
			"service %s/%s is still visible after delete", namespace, service)
		select {
		case <-ctx.Done():
			confirmErr := ctx.Err()
			if lastErr != nil {
				confirmErr = lastErr
			}
			return model.NewSDKError(model.ErrCodeServiceWriteError, confirmErr,
				"service delete for %s/%s was not confirmed before timeout", namespace, service)
		case <-time.After(serviceReadAfterWriteInterval):
		}
	}
}

func (c *Client) clearServiceWriteState(namespace, service string) {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	delete(c.serviceStates, serviceStateKey(namespace, service))
}

func serviceHasOwner(actual, requested string) bool {
	if actual == requested {
		return true
	}
	for _, owner := range strings.Split(actual, ",") {
		if strings.TrimSpace(owner) == requested {
			return true
		}
	}
	return false
}

func decodeService(rawService json.RawMessage) (*serviceRecord, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawService, &raw); err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode raw Console service")
	}
	var decoded consoleServicePayload
	if err := json.Unmarshal(rawService, &decoded); err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode Console service")
	}
	serviceType, err := decodeServiceType(decoded.ServiceType)
	if err != nil {
		return nil, err
	}
	service := &apiservice.Service{
		Id:                   wrapperspb.String(decoded.ID),
		Name:                 wrapperspb.String(decoded.Name),
		Namespace:            wrapperspb.String(decoded.Namespace),
		Metadata:             cloneMap(decoded.Metadata),
		Revision:             wrapperspb.String(decoded.Revision),
		ServiceType:          serviceType,
		Owners:               wrapperspb.String(decoded.Owners),
		SyncToGlobalRegistry: decoded.SyncToGlobalRegistry,
	}
	service.ExportTo = make([]*wrapperspb.StringValue, 0, len(decoded.ExportTo))
	for _, value := range decoded.ExportTo {
		service.ExportTo = append(service.ExportTo, wrapperspb.String(value))
	}
	if decoded.TotalInstanceCount != nil {
		service.TotalInstanceCount = wrapperspb.UInt32(*decoded.TotalInstanceCount)
	}
	service.ExtendedMetadata, err = decodeKnownExtendedMetadata(raw)
	if err != nil {
		return nil, err
	}
	return &serviceRecord{Service: service, Raw: raw}, nil
}

func decodeServiceType(raw json.RawMessage) (apiservice.ServiceType, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return apiservice.ServiceType_SERVICE_TYPE_MICROSERVICE, nil
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		if value, ok := apiservice.ServiceType_value[name]; ok {
			return apiservice.ServiceType(value), nil
		}
		if name == "" {
			return apiservice.ServiceType_SERVICE_TYPE_MICROSERVICE, nil
		}
		return 0, model.NewSDKError(model.ErrCodeServiceWriteError, nil,
			"Console service_type %q is invalid", name)
	}
	var number int32
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"Console service_type must be an enum name or integer")
	}
	switch apiservice.ServiceType(number) {
	case apiservice.ServiceType_SERVICE_TYPE_MICROSERVICE,
		apiservice.ServiceType_SERVICE_TYPE_MCP_SERVER,
		apiservice.ServiceType_SERVICE_TYPE_AI_AGENT:
		return apiservice.ServiceType(number), nil
	default:
		return 0, model.NewSDKError(model.ErrCodeServiceWriteError, nil,
			"Console service_type %d is invalid", number)
	}
}

func decodeKnownExtendedMetadata(raw map[string]json.RawMessage) ([]*apiservice.ExtendedMetadata, error) {
	value, ok := raw["extended_metadata"]
	if !ok {
		value, ok = raw["extendedMetadata"]
	}
	if !ok || len(value) == 0 || string(value) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(value, &entries); err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode extended_metadata")
	}
	result := make([]*apiservice.ExtendedMetadata, 0, len(entries))
	for _, entry := range entries {
		envelope, _, isSkill, err := decodeExtendedMetadata(entry)
		if err != nil {
			return nil, err
		}
		if !isSkill {
			continue
		}
		agentSkill, err := decodeAgentSkillPayload(envelope.AgentSkill, envelope.AgentSkillCamel)
		if err != nil {
			return nil, err
		}
		result = append(result, &apiservice.ExtendedMetadata{
			Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
			AgentSkill: agentSkill,
		})
	}
	return result, nil
}

func decodeAgentSkillPayload(values ...json.RawMessage) (*apiservice.AgentSkill, error) {
	var raw json.RawMessage
	for _, value := range values {
		if len(value) > 0 && string(value) != "null" {
			raw = value
			break
		}
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode extended AgentSkill")
	}
	// The specification pins snake_case JSON names; accept A2A mode aliases too.
	for _, alias := range []struct{ original, alternate string }{
		{original: "input_modes", alternate: "inputModes"},
		{original: "output_modes", alternate: "outputModes"},
	} {
		if _, exists := fields[alias.original]; !exists {
			if value, ok := fields[alias.alternate]; ok {
				fields[alias.original] = value
			}
		}
		delete(fields, alias.alternate)
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to normalize extended AgentSkill")
	}
	var decoded agentSkillPayload
	if err = json.Unmarshal(normalized, &decoded); err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode extended AgentSkill")
	}
	return &apiservice.AgentSkill{
		Id:          decoded.ID,
		Name:        decoded.Name,
		Description: decoded.Description,
		Tags:        append([]string(nil), decoded.Tags...),
		Examples:    append([]string(nil), decoded.Examples...),
		InputModes:  append([]string(nil), decoded.InputModes...),
		OutputModes: append([]string(nil), decoded.OutputModes...),
		Version:     decoded.Version,
	}, nil
}

func extendedMetadataMatches(record *serviceRecord, expected []json.RawMessage) (bool, error) {
	actual, err := recordExtendedMetadata(record)
	if err != nil {
		return false, err
	}
	if len(expected) == 0 && len(actual) == 0 {
		return true, nil
	}
	if len(expected) != len(actual) {
		return false, nil
	}
	canonicalExpected, err := normalizeExtendedMetadataForCompare(expected)
	if err != nil {
		return false, err
	}
	canonicalActual, err := normalizeExtendedMetadataForCompare(actual)
	if err != nil {
		return false, err
	}
	for index := range canonicalExpected {
		if canonicalExpected[index] != canonicalActual[index] {
			return false, nil
		}
	}
	return true, nil
}

func recordExtendedMetadata(record *serviceRecord) ([]json.RawMessage, error) {
	if record == nil {
		return nil, nil
	}
	value, ok := record.Raw["extended_metadata"]
	if !ok {
		value, ok = record.Raw["extendedMetadata"]
	}
	if !ok || len(value) == 0 || string(value) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(value, &entries); err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode extended_metadata for write confirmation")
	}
	return entries, nil
}

func normalizeExtendedMetadataForCompare(values []json.RawMessage) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		envelope, _, isSkill, err := decodeExtendedMetadata(value)
		if err != nil {
			return nil, err
		}
		var normalized interface{}
		prefix := "raw:"
		if isSkill {
			prefix = "skill:"
			skill, skillErr := decodeAgentSkillPayload(envelope.AgentSkill, envelope.AgentSkillCamel)
			if skillErr != nil {
				return nil, skillErr
			}
			if skill != nil {
				skill, skillErr = normalizeAgentSkillForCompare(skill)
				if skillErr != nil {
					return nil, skillErr
				}
			}
			normalized = skill
		} else {
			decoder := json.NewDecoder(strings.NewReader(string(value)))
			decoder.UseNumber()
			if err = decoder.Decode(&normalized); err != nil {
				return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
					"fail to normalize extended metadata")
			}
		}
		canonical, err := json.Marshal(normalized)
		if err != nil {
			return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
				"fail to encode normalized extended metadata")
		}
		result = append(result, prefix+string(canonical))
	}
	// Storage may sort metadata by type and key instead of retaining write order.
	sort.Strings(result)
	return result, nil
}

func normalizeAgentSkillForCompare(skill *apiservice.AgentSkill) (*apiservice.AgentSkill, error) {
	_, name, version, err := model.ParseSkillRef(skill.Id)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to normalize extended AgentSkill %q", skill.Id)
	}
	result := proto.Clone(skill).(*apiservice.AgentSkill)
	if result.Name == "" {
		result.Name = name
	}
	if result.Version == "" {
		result.Version = version
	}
	return result, nil
}

func mergeExtendedMetadata(
	raw map[string]json.RawMessage, skills []*model.AgentSkill, replace bool, removeIDs []string,
) ([]json.RawMessage, error) {
	var existing []json.RawMessage
	value, ok := raw["extended_metadata"]
	if !ok {
		value, ok = raw["extendedMetadata"]
	}
	if ok && string(value) != "null" {
		if err := json.Unmarshal(value, &existing); err != nil {
			return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
				"fail to decode extended_metadata")
		}
	}
	result := make([]json.RawMessage, 0, len(existing)+len(skills))
	skillIndex := make(map[string]int)
	removed := make(map[string]struct{}, len(removeIDs))
	for _, id := range removeIDs {
		removed[id] = struct{}{}
	}
	for _, item := range existing {
		envelope, id, isSkill, err := decodeExtendedMetadata(item)
		if err != nil {
			return nil, err
		}
		_ = envelope
		if !isSkill {
			result = append(result, item)
			continue
		}
		if _, ok := removed[id]; ok {
			continue
		}
		if replace {
			continue
		}
		skillIndex[id] = len(result)
		result = append(result, item)
	}
	for _, skill := range skills {
		item, err := encodeSkillExtendedMetadata(skill)
		if err != nil {
			return nil, err
		}
		if index, ok := skillIndex[skill.ID]; ok {
			result[index] = item
			continue
		}
		skillIndex[skill.ID] = len(result)
		result = append(result, item)
	}
	return result, nil
}

func decodeExtendedMetadata(raw json.RawMessage) (*extendedMetadataPayload, string, bool, error) {
	var envelope extendedMetadataPayload
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, "", false, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to decode extended metadata item")
	}
	typeValue := strings.Trim(string(envelope.Type), `"`)
	if typeValue != "1" && !strings.EqualFold(typeValue, "EXTENDED_METADATA_SKILL") {
		return &envelope, "", false, nil
	}
	skill, err := decodeAgentSkillPayload(envelope.AgentSkill, envelope.AgentSkillCamel)
	if err != nil {
		return nil, "", false, err
	}
	return &envelope, skill.GetId(), true, nil
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

func encodeSkillExtendedMetadata(skill *model.AgentSkill) (json.RawMessage, error) {
	skillPayload, err := json.Marshal(agentSkillPayload{
		ID:          skill.ID,
		Name:        skill.Name,
		Description: skill.Description,
		Tags:        skill.Tags,
		Examples:    skill.Examples,
		InputModes:  skill.InputModes,
		OutputModes: skill.OutputModes,
		Version:     skill.Version,
	})
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to encode AgentSkill")
	}
	raw, err := json.Marshal(struct {
		Type       int             `json:"type"`
		AgentSkill json.RawMessage `json:"agent_skill"`
	}{
		Type:       int(model.ExtendedMetadataSkill),
		AgentSkill: skillPayload,
	})
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
			"fail to encode extended AgentSkill")
	}
	return raw, nil
}

func cloneMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func validateAgentServiceType(record *serviceRecord, namespace, service string) error {
	if record == nil || record.Service == nil {
		return model.NewSDKError(model.ErrCodeServiceWriteError, nil,
			"Console returned an empty service for %s/%s", namespace, service)
	}
	if record.Service.GetServiceType() != apiservice.ServiceType_SERVICE_TYPE_AI_AGENT {
		return model.NewSDKError(model.ErrCodeServiceTypeConflict, nil,
			"service %s/%s already exists with type %s",
			namespace, service, record.Service.GetServiceType())
	}
	return nil
}

func validateServiceType(
	record *serviceRecord,
	namespace, service string,
	expected *model.ServiceType,
) error {
	if record == nil || record.Service == nil {
		return model.NewSDKError(model.ErrCodeServiceWriteError, nil,
			"Console returned an empty service for %s/%s", namespace, service)
	}
	if expected == nil {
		return nil
	}
	actual := model.ServiceType(record.Service.GetServiceType())
	if actual != *expected {
		return model.NewSDKError(model.ErrCodeServiceTypeConflict, nil,
			"service %s/%s already exists with type %s", namespace, service, actual)
	}
	return nil
}

func (r *serviceRecord) AgentService() *model.AgentService {
	if r == nil || r.Service == nil {
		return nil
	}
	return modelpb.NewAgentServiceFromProto(r.Service)
}

// ServiceRecord converts the Console record into the protocol-neutral SDK model.
func (r *serviceRecord) ServiceRecord() (*ServiceRecord, error) {
	if r == nil || r.Service == nil {
		return nil, nil
	}
	extended, err := recordExtendedMetadata(r)
	if err != nil {
		return nil, err
	}
	return &ServiceRecord{
		Namespace:          r.Service.GetNamespace().GetValue(),
		Name:               r.Service.GetName().GetValue(),
		Type:               model.ServiceType(r.Service.GetServiceType()),
		Metadata:           cloneMap(r.Service.GetMetadata()),
		ExtendedMetadata:   cloneRawMessages(extended),
		Owners:             r.Service.GetOwners().GetValue(),
		Revision:           r.Service.GetRevision().GetValue(),
		TotalInstanceCount: r.Service.GetTotalInstanceCount().GetValue(),
	}, nil
}
