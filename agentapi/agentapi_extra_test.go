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
	"errors"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentmodel"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/serviceadmin"
)

func TestAgentAPIGetServicePropagatesNotFound(t *testing.T) {
	admin := &fakeServiceAdmin{
		getErr: model.NewSDKError(model.ErrCodeServiceNotFound, errors.New("missing"), "missing"),
	}
	client := newWithStore(admin)
	_, err := client.GetService(&GetServiceRequest{
		GetAgentServiceRequest: agentmodel.GetAgentServiceRequest{
			Namespace: "Production",
			Service:   "agent",
		},
	})
	require.Error(t, err)
	require.Equal(t, model.ErrCodeServiceNotFound, model.GetErrorCodeFromError(err))
}

func TestAgentAPIMountRejectsNonAgentService(t *testing.T) {
	admin := &fakeServiceAdmin{record: &serviceadmin.ServiceRecord{
		Namespace: "Production",
		Name:      "service",
		Type:      model.ServiceTypeMicroservice,
	}}
	client := newWithStore(admin)
	_, err := client.MountSkills(&MountSkillsRequest{
		MountAgentSkillsRequest: agentmodel.MountAgentSkillsRequest{
			Namespace: "Production",
			Service:   "service",
			Skills:    []model.AgentSkill{{ID: "Production:demo@1.0.0"}},
		},
	})
	require.Error(t, err)
	require.Equal(t, model.ErrCodeServiceTypeConflict, model.GetErrorCodeFromError(err))
	require.Nil(t, admin.updateReq)
}

func TestAgentAPIUnmountRemovesSelectedSkill(t *testing.T) {
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
	_, err := client.UnmountSkills(&UnmountSkillsRequest{
		UnmountAgentSkillsRequest: agentmodel.UnmountAgentSkillsRequest{
			Namespace: "Production",
			Service:   "customer-agent",
			SkillIDs:  []string{"Production:old@1.0.0"},
		},
		Revision: "rev-1",
	})
	require.NoError(t, err)
	require.NotNil(t, admin.updateReq)
	require.Len(t, *admin.updateReq.ExtendedMetadata, 1)
	require.Contains(t, string((*admin.updateReq.ExtendedMetadata)[0]), `"type":999`)
	require.NotContains(t, string((*admin.updateReq.ExtendedMetadata)[0]), "Production:old")
}

func TestAgentAPIDestroyDelegatesToServiceAdmin(t *testing.T) {
	admin := &fakeServiceAdmin{}
	client := newWithStore(admin)
	client.Destroy()
	require.True(t, admin.destroyed)
}

func TestAgentAPIReadsCamelCaseSkillFieldsAndPreservesSecurity(t *testing.T) {
	rawSkill := json.RawMessage(`{"type":1,"agent_skill":{` +
		`"id":"Production:demo@1.0.0","name":"demo","version":"1.0.0",` +
		`"inputModes":["text"],"outputModes":["audio"],"security":{"scheme":"bearer"}}}`)
	admin := &fakeServiceAdmin{record: &serviceadmin.ServiceRecord{
		Namespace:        "Production",
		Name:             "agent",
		Type:             model.ServiceTypeAIAgent,
		ExtendedMetadata: []json.RawMessage{rawSkill},
		Revision:         "rev-1",
	}}
	client := newWithStore(admin)
	service, err := client.GetService(&GetServiceRequest{
		GetAgentServiceRequest: agentmodel.GetAgentServiceRequest{Namespace: "Production", Service: "agent"},
	})
	require.NoError(t, err)
	require.Len(t, service.ExtendedMetadata, 1)
	require.Equal(t, []string{"text"}, service.ExtendedMetadata[0].AgentSkill.InputModes)
	require.Equal(t, []string{"audio"}, service.ExtendedMetadata[0].AgentSkill.OutputModes)
	require.JSONEq(t, `{"scheme":"bearer"}`, string(service.ExtendedMetadata[0].AgentSkill.Security))

	_, err = client.MountSkills(&MountSkillsRequest{
		MountAgentSkillsRequest: agentmodel.MountAgentSkillsRequest{
			Namespace: "Production",
			Service:   "agent",
			Replace:   true,
			Skills: []model.AgentSkill{{
				ID:         "Production:new@1.0.0",
				InputModes: []string{"text"},
				Security:   json.RawMessage(`{"scheme":"bearer"}`),
			}},
		},
		Revision: "rev-1",
	})
	require.NoError(t, err)
	require.NotNil(t, admin.updateReq)
	require.Contains(t, string((*admin.updateReq.ExtendedMetadata)[0]), `"input_modes":["text"]`)
	require.Contains(t, string((*admin.updateReq.ExtendedMetadata)[0]), `"security":{"scheme":"bearer"}`)
}

func TestAgentAPIRejectsNilRequests(t *testing.T) {
	client := newWithStore(&fakeServiceAdmin{})

	_, err := client.GetService(nil)
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
	_, err = client.EnsureService(nil)
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
	_, err = client.MountSkills(nil)
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
	_, err = client.UnmountSkills(nil)
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
	err = client.DeleteService(nil)
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
}

func TestAgentAPIRequestValidationRejectsRevisionWhitespace(t *testing.T) {
	ensure := &EnsureServiceRequest{
		EnsureAgentServiceRequest: agentmodel.EnsureAgentServiceRequest{
			Namespace: "Production",
			Service:   "agent",
		},
		Revision: " rev-1 ",
	}
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(ensure.Validate()))

	mount := &MountSkillsRequest{
		MountAgentSkillsRequest: agentmodel.MountAgentSkillsRequest{
			Namespace: "Production",
			Service:   "agent",
		},
		Revision: " rev-1 ",
	}
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(mount.Validate()))

	unmount := &UnmountSkillsRequest{
		UnmountAgentSkillsRequest: agentmodel.UnmountAgentSkillsRequest{
			Namespace: "Production",
			Service:   "agent",
			SkillIDs:  []string{"Production:demo@1.0.0"},
		},
		Revision: " rev-1 ",
	}
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(unmount.Validate()))
}

func TestAgentAPIMountRejectsEmptyServiceResponse(t *testing.T) {
	client := newWithStore(&fakeServiceAdmin{})
	_, err := client.MountSkills(&MountSkillsRequest{
		MountAgentSkillsRequest: agentmodel.MountAgentSkillsRequest{
			Namespace: "Production",
			Service:   "agent",
			Skills:    []model.AgentSkill{{ID: "Production:demo@1.0.0"}},
		},
	})
	require.Equal(t, model.ErrCodeInvalidResponse, model.GetErrorCodeFromError(err))
}
