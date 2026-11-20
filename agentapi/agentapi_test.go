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
	"testing"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentmodel"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/serviceadmin"
)

type fakeServiceAdmin struct {
	record    *serviceadmin.ServiceRecord
	getReq    *ServiceLookupRequest
	ensureReq *ServiceEnsureRequest
	updateReq *ServiceUpdateRequest
	deleteReq *ServiceDeleteRequest
	destroyed bool
	getErr    error
}

func (f *fakeServiceAdmin) GetService(
	req *ServiceLookupRequest,
) (*serviceadmin.ServiceRecord, error) {
	f.getReq = req
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.record, nil
}

func (f *fakeServiceAdmin) EnsureService(
	req *ServiceEnsureRequest,
) (*serviceadmin.ServiceRecord, error) {
	f.ensureReq = req
	return f.record, nil
}

func (f *fakeServiceAdmin) UpdateService(
	req *ServiceUpdateRequest,
) (*serviceadmin.ServiceRecord, error) {
	f.updateReq = req
	if req.ExtendedMetadata != nil {
		f.record.ExtendedMetadata = append([]json.RawMessage(nil), (*req.ExtendedMetadata)...)
	}
	return f.record, nil
}

func (f *fakeServiceAdmin) DeleteService(req *ServiceDeleteRequest) error {
	f.deleteReq = req
	return nil
}

func (*fakeServiceAdmin) SDKContext() api.SDKContext { return nil }

func (f *fakeServiceAdmin) Destroy() { f.destroyed = true }

func TestAgentAPIEnsureAndMetadataValidation(t *testing.T) {
	rawSkill := json.RawMessage(
		`{"type":1,"agent_skill":{"id":"Production:weather@1.0.0","name":"weather","version":"1.0.0"}}`,
	)
	admin := &fakeServiceAdmin{record: &serviceadmin.ServiceRecord{
		Namespace: "Production",
		Name:      "customer-agent",
		Type:      model.ServiceTypeAIAgent,
		Metadata:  map[string]string{"owner": "agent-team"},
		ExtendedMetadata: []json.RawMessage{
			rawSkill,
		},
		Revision: "rev-1",
	}}
	client := newWithStore(admin)
	service, err := client.EnsureService(&EnsureServiceRequest{
		EnsureAgentServiceRequest: agentmodel.EnsureAgentServiceRequest{
			Namespace: "Production",
			Service:   "customer-agent",
			Skills: []model.AgentSkill{{
				ID: "Production:weather@1.0.0",
			}},
		},
		Revision: "rev-1",
	})
	require.NoError(t, err)
	require.Equal(t, "rev-1", service.Revision)
	require.Equal(t, model.ServiceTypeAIAgent, admin.ensureReq.Type)
	require.Equal(t, "rev-1", admin.ensureReq.Revision)
	require.NotNil(t, admin.ensureReq.ExtendedMetadata)
	require.Nil(t, admin.ensureReq.Metadata)

	require.NoError(t, client.ValidateInstanceMetadata(map[string]string{
		"ai-agent-name": "customer-agent",
		"ai-agent-card": `{"name":"customer-agent","version":"1.0.0","supportedInterfaces":[` +
			`{"url":"http://127.0.0.1:18080/a2a","protocolBinding":"JSONRPC","protocolVersion":"1.0"}]}`,
		"ai-a2a-version": "1.0",
	}))
	require.Error(t, client.ValidateInstanceMetadata(map[string]string{}))
}

func TestAgentAPIMountPreservesUnknownMetadataAndRevision(t *testing.T) {
	oldSkill := json.RawMessage(
		`{"type":1,"agent_skill":{"id":"Production:old@1.0.0","name":"old","version":"1.0.0"}}`,
	)
	unknown := json.RawMessage(`{"type":999,"future":{"id":1}}`)
	admin := &fakeServiceAdmin{record: &serviceadmin.ServiceRecord{
		Namespace:        "Production",
		Name:             "customer-agent",
		Type:             model.ServiceTypeAIAgent,
		ExtendedMetadata: []json.RawMessage{oldSkill, unknown},
		Revision:         "rev-1",
	}}
	client := newWithStore(admin)
	service, err := client.MountSkills(&MountSkillsRequest{
		MountAgentSkillsRequest: agentmodel.MountAgentSkillsRequest{
			Namespace: "Production",
			Service:   "customer-agent",
			Skills: []model.AgentSkill{{
				ID: "Production:new@1.0.0",
			}},
		},
		Revision: "rev-1",
	})
	require.NoError(t, err)
	require.Equal(t, "rev-1", admin.updateReq.Revision)
	require.Len(t, *admin.updateReq.ExtendedMetadata, 3)
	require.Contains(t, string((*admin.updateReq.ExtendedMetadata)[1]), `"type":999`)
	require.Len(t, service.ExtendedMetadata, 3)

	_, err = client.MountSkills(&MountSkillsRequest{
		MountAgentSkillsRequest: agentmodel.MountAgentSkillsRequest{
			Namespace: "Production",
			Service:   "customer-agent",
		},
		Revision: "stale",
	})
	require.Error(t, err)
	require.Equal(t, model.ErrCodeServicePreconditionFailed, model.GetErrorCodeFromError(err))
}

func TestAgentAPIDeleteUsesAITypeAndGuards(t *testing.T) {
	admin := &fakeServiceAdmin{}
	client := newWithStore(admin)
	require.NoError(t, client.DeleteService(&DeleteServiceRequest{
		DeleteAgentServiceRequest: agentmodel.DeleteAgentServiceRequest{
			Namespace: "Production",
			Service:   "customer-agent",
			Revision:  "rev-1",
			Owner:     "agent-team",
			Force:     true,
		},
	}))
	require.NotNil(t, admin.deleteReq.Type)
	require.Equal(t, model.ServiceTypeAIAgent, *admin.deleteReq.Type)
	require.Equal(t, "rev-1", admin.deleteReq.Revision)
	require.Equal(t, "agent-team", admin.deleteReq.Owner)
	require.True(t, admin.deleteReq.Force)
}
