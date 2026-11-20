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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/sdk"
	"github.com/stretchr/testify/require"
)

func TestAgentAPIWithConsoleCRUDThroughPublicAPI(t *testing.T) {
	const namespace, serviceName = "AIRegistryTest", "agent"
	var mutex sync.Mutex
	var current map[string]json.RawMessage
	var generation, writes int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if request.Header.Get("X-Polaris-Token") != "test-token" || request.Header.Get("Request-Id") == "" {
			t.Error("Console request must contain its token and request ID")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writeJSON := func(value interface{}) {
			if err := json.NewEncoder(writer).Encode(value); err != nil {
				t.Error(err)
			}
		}
		if request.Method == http.MethodGet && request.URL.Path == "/naming/v1/services" {
			if request.URL.Query().Get("namespace") != namespace || request.URL.Query().Get("name") != serviceName {
				t.Error("Console lookup must retain the requested service identity")
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			services := []map[string]json.RawMessage{}
			if current != nil {
				services = append(services, current)
			}
			writeJSON(map[string]interface{}{"code": 200000, "services": services})
			return
		}
		var payloads []map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&payloads); err != nil || len(payloads) != 1 {
			t.Errorf("invalid Console write payload: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		payload := payloads[0]
		if string(payload["namespace"]) != `"AIRegistryTest"` || string(payload["name"]) != `"agent"` {
			t.Error("Console write must retain the requested service identity")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/naming/v1/services/delete":
			if current == nil || string(payload["revision"]) != string(current["revision"]) {
				t.Error("Console delete must guard the current revision")
				writer.WriteHeader(http.StatusConflict)
				return
			}
			current = nil
		case request.Method == http.MethodPost && request.URL.Path == "/naming/v1/services":
			current = payload
			current["owners"] = json.RawMessage(`"team"`)
		case request.Method == http.MethodPut && request.URL.Path == "/naming/v1/services":
			if current == nil || string(payload["revision"]) != string(current["revision"]) {
				t.Error("Console update must guard the current revision")
				writer.WriteHeader(http.StatusConflict)
				return
			}
			for key, value := range payload {
				current[key] = value
			}
		default:
			t.Errorf("unexpected Console request %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writes++
		if current != nil {
			generation++
			current["revision"] = json.RawMessage(fmt.Sprintf(`"rev-%d"`, generation))
		}
		writeJSON(map[string]interface{}{
			"code": 200000, "responses": []map[string]int{{"code": 200000}},
		})
	}))
	defer server.Close()

	context := &backendTestContext{engine: &backendTestEngine{}}
	client := NewByContext(context, WithConsole([]string{server.URL}, "test-token", time.Second))
	defer client.Destroy()
	created, err := client.EnsureService(&EnsureServiceRequest{EnsureAgentServiceRequest: EnsureAgentServiceRequest{
		Namespace: namespace, Service: serviceName, Metadata: map[string]string{"owner": "team"},
		Skills: []model.AgentSkill{{ID: namespace + ":weather@1.0.0"}},
	}})
	require.NoError(t, err)
	require.Equal(t, model.ServiceTypeAIAgent, created.Type)
	require.Equal(t, "rev-1", created.Revision)
	require.Len(t, created.ExtendedMetadata, 1)

	retained, err := client.EnsureService(&EnsureServiceRequest{EnsureAgentServiceRequest: EnsureAgentServiceRequest{
		Namespace: namespace, Service: serviceName,
	}})
	require.NoError(t, err)
	require.Equal(t, created.Revision, retained.Revision)
	require.Len(t, retained.ExtendedMetadata, 1)

	mounted, err := client.MountSkills(&MountSkillsRequest{
		MountAgentSkillsRequest: MountAgentSkillsRequest{Namespace: namespace, Service: serviceName,
			Skills: []model.AgentSkill{{ID: namespace + ":calendar@2.0.0"}},
		}, Revision: retained.Revision,
	})
	require.NoError(t, err)
	require.Len(t, mounted.ExtendedMetadata, 2)
	require.Equal(t, "team", mounted.Metadata["owner"])
	require.NotEqual(t, retained.Revision, mounted.Revision)

	unmounted, err := client.UnmountSkills(&UnmountSkillsRequest{
		UnmountAgentSkillsRequest: UnmountAgentSkillsRequest{Namespace: namespace, Service: serviceName,
			SkillIDs: []string{namespace + ":weather@1.0.0"},
		}, Revision: mounted.Revision,
	})
	require.NoError(t, err)
	require.Len(t, unmounted.ExtendedMetadata, 1)
	require.Equal(t, namespace+":calendar@2.0.0", unmounted.ExtendedMetadata[0].AgentSkill.ID)
	read, err := client.GetService(&GetServiceRequest{GetAgentServiceRequest: GetAgentServiceRequest{
		Namespace: namespace, Service: serviceName,
	}})
	require.NoError(t, err)
	require.Equal(t, unmounted.Revision, read.Revision)

	require.NoError(t, client.DeleteService(&DeleteServiceRequest{DeleteAgentServiceRequest: DeleteAgentServiceRequest{
		Namespace: namespace, Service: serviceName, Revision: read.Revision, Owner: "team",
	}}))
	_, err = client.GetService(&GetServiceRequest{GetAgentServiceRequest: GetAgentServiceRequest{
		Namespace: namespace, Service: serviceName,
	}})
	require.Equal(t, model.ErrCodeServiceNotFound, model.GetErrorCodeFromError(err))
	client.Destroy()
	require.False(t, context.destroyed)
	mutex.Lock()
	writeCount := writes
	mutex.Unlock()
	require.Equal(t, 4, writeCount)
}

type backendTestContext struct {
	api.SDKContext
	engine    sdk.Engine
	destroyed bool
}

func (c *backendTestContext) IsDestroyed() bool     { return c.destroyed }
func (c *backendTestContext) GetEngine() sdk.Engine { return c.engine }
func (c *backendTestContext) Destroy()              { c.destroyed = true }

type backendTestEngine struct {
	sdk.Engine
	response *model.InstancesResponse
	request  *model.GetAllInstancesRequest
}

func (e *backendTestEngine) SyncGetAllInstances(req *model.GetAllInstancesRequest) (*model.InstancesResponse, error) {
	e.request = req
	return e.response, nil
}

func TestAgentAPIReadsMountedSkillsThroughConsumerWithoutConsole(t *testing.T) {
	engine := &backendTestEngine{response: &model.InstancesResponse{
		ServiceInfo: model.ServiceInfo{Namespace: "Production", Service: "agent", Metadata: map[string]string{"owner": "team"}},
		ServiceType: model.ServiceTypeAIAgent, ServiceRevision: "service-revision", Revision: "instance-revision",
		ExtendedMetadata: []*model.ExtendedMetadata{{Type: model.ExtendedMetadataSkill,
			AgentSkill: &model.AgentSkill{ID: "Production:weather@1.0.0", Tags: []string{"original"}},
		}},
	}}
	context := &backendTestContext{engine: engine}
	client := NewByContext(context)
	request := &GetServiceRequest{GetAgentServiceRequest: GetAgentServiceRequest{Namespace: "Production", Service: "agent"}}
	request.SetTimeout(time.Second)
	request.SetRetryCount(0)
	service, err := client.GetService(request)
	require.NoError(t, err)
	require.Equal(t, "service-revision", service.Revision)
	require.Equal(t, request.Timeout, engine.request.Timeout)
	require.Equal(t, request.RetryCount, engine.request.RetryCount)
	require.Len(t, service.ExtendedMetadata, 1)
	service.Metadata["owner"] = "mutation"
	service.ExtendedMetadata[0].AgentSkill.Tags[0] = "mutation"
	require.Equal(t, "team", engine.response.Metadata["owner"])
	require.Equal(t, "original", engine.response.ExtendedMetadata[0].AgentSkill.Tags[0])

	engine.response.ServiceRevision = ""
	service, err = client.GetService(request)
	require.NoError(t, err)
	require.Empty(t, service.Revision)

	_, err = client.EnsureService(&EnsureServiceRequest{EnsureAgentServiceRequest: EnsureAgentServiceRequest{
		Namespace: "Production", Service: "agent",
	}})
	require.Equal(t, model.ErrCodeServiceWriteUnsupported, model.GetErrorCodeFromError(err))
	client.Destroy()
	client.Destroy()
	require.False(t, context.destroyed)
	_, err = client.GetService(request)
	require.Equal(t, model.ErrCodeInvalidStateError, model.GetErrorCodeFromError(err))
}

func TestAgentAPIContextAndConsoleValidation(t *testing.T) {
	request := &GetServiceRequest{GetAgentServiceRequest: GetAgentServiceRequest{Namespace: "Production", Service: "agent"}}
	_, err := NewByContext(nil).GetService(request)
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
	context := &backendTestContext{destroyed: true}
	_, err = NewByContext(context).GetService(request)
	require.Equal(t, model.ErrCodeInvalidStateError, model.GetErrorCodeFromError(err))
	context.destroyed = false
	_, err = NewByContext(context, WithConsole([]string{"http://console.test?tenant=x"}, "", time.Second)).GetService(request)
	require.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
}

func TestAgentAPIConsumerResponseErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		response *model.InstancesResponse
		code     model.ErrCode
	}{
		{name: "empty_response", code: model.ErrCodeInvalidResponse},
		{name: "wrong_service", response: &model.InstancesResponse{
			ServiceInfo: model.ServiceInfo{Namespace: "Production", Service: "other"},
		}, code: model.ErrCodeInvalidResponse},
		{name: "wrong_namespace", response: &model.InstancesResponse{
			ServiceInfo: model.ServiceInfo{Namespace: "Staging", Service: "agent"},
		}, code: model.ErrCodeInvalidResponse},
		{name: "not_found", response: &model.InstancesResponse{
			ServiceInfo: model.ServiceInfo{Namespace: "Production", Service: "agent"}, NotExists: true,
		}, code: model.ErrCodeServiceNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewByContext(&backendTestContext{engine: &backendTestEngine{response: test.response}})
			_, err := client.GetService(&GetServiceRequest{GetAgentServiceRequest: GetAgentServiceRequest{
				Namespace: "Production", Service: "agent",
			}})
			require.Equal(t, test.code, model.GetErrorCodeFromError(err))
		})
	}
}

func TestAgentAPITypeInferenceBelongsToAdapter(t *testing.T) {
	for _, test := range []struct {
		name      string
		typeValue model.ServiceType
		skillID   string
		wantAgent bool
	}{
		{name: "ordinary_service"},
		{name: "blank_skill", skillID: " "},
		{name: "mounted_skill", skillID: "Production:weather@1.0.0", wantAgent: true},
		{name: "explicit_agent", typeValue: model.ServiceTypeAIAgent, wantAgent: true},
		{name: "explicit_other_type", typeValue: model.ServiceType(99), skillID: "Production:weather@1.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := &model.InstancesResponse{
				ServiceInfo: model.ServiceInfo{Namespace: "Production", Service: "agent"},
				ServiceType: test.typeValue,
				ExtendedMetadata: []*model.ExtendedMetadata{{
					Type: model.ExtendedMetadataSkill, AgentSkill: &model.AgentSkill{ID: test.skillID},
				}},
			}
			client := NewByContext(&backendTestContext{engine: &backendTestEngine{response: response}})
			service, err := client.GetService(&GetServiceRequest{GetAgentServiceRequest: GetAgentServiceRequest{
				Namespace: "Production", Service: "agent",
			}})
			if test.wantAgent {
				require.NoError(t, err)
				require.Equal(t, model.ServiceTypeAIAgent, service.Type)
			} else {
				require.Equal(t, model.ErrCodeServiceTypeConflict, model.GetErrorCodeFromError(err))
			}
			require.Equal(t, test.typeValue, response.ServiceType)
		})
	}
}
