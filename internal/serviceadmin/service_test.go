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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/agentmodel"
)

func TestEnsureAgentServiceCreatesTypedService(t *testing.T) {
	var postBody string
	getCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			getCount++
			if getCount <= 2 {
				writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Info: wrapperspb.String("success"),
				})
				return
			}
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Info: wrapperspb.String("success"),
				Services: []*apiservice.Service{{
					Namespace:   wrapperspb.String("Production"),
					Name:        wrapperspb.String("customer-agent"),
					ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
					Metadata:    map[string]string{"owner": "agent-team"},
					Revision:    wrapperspb.String("rev-2"),
					ExtendedMetadata: []*apiservice.ExtendedMetadata{{
						Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
						AgentSkill: &apiservice.AgentSkill{Id: "Production:demo@1.0.0"},
					}},
				}},
			})
		case http.MethodPost:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			postBody = string(body)
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Info: wrapperspb.String("success"),
				Responses: []*apiservice.Response{{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Info: wrapperspb.String("success"),
					Service: &apiservice.Service{
						Namespace: wrapperspb.String("Production"),
						Name:      wrapperspb.String("customer-agent"),
						Token:     wrapperspb.String("token"),
					},
				}},
			})
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	record, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production",
		Service:   "customer-agent",
		Metadata:  map[string]string{"owner": "agent-team"},
		Skills: []model.AgentSkill{{
			ID: "Production:demo@1.0.0",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, apiservice.ServiceType_SERVICE_TYPE_AI_AGENT, record.Service.GetServiceType())
	assert.Equal(t, "rev-2", record.Service.GetRevision().GetValue())
	assert.Contains(t, postBody, `"service_type":"SERVICE_TYPE_AI_AGENT"`)
	assert.Contains(t, postBody, `"extended_metadata":[`)
	assert.Contains(t, postBody, `"version":"1.0.0"`)

	var rawServices []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(postBody), &rawServices))
	require.Len(t, rawServices, 1)
	written := &apiservice.Service{}
	require.NoError(t, protojson.Unmarshal(rawServices[0], written))
	assert.Equal(t, apiservice.ServiceType_SERVICE_TYPE_AI_AGENT, written.GetServiceType())
	require.Len(t, written.GetExtendedMetadata(), 1)
	assert.Equal(t, "1.0.0", written.GetExtendedMetadata()[0].GetAgentSkill().GetVersion())
}

func TestEnsureAgentServiceRejectsTypeConflict(t *testing.T) {
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
			Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			Info: wrapperspb.String("success"),
			Services: []*apiservice.Service{{
				Namespace:   wrapperspb.String("Production"),
				Name:        wrapperspb.String("customer-agent"),
				ServiceType: apiservice.ServiceType_SERVICE_TYPE_MICROSERVICE,
			}},
		})
	}))

	_, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production",
		Service:   "customer-agent",
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceTypeConflict, model.GetErrorCodeFromError(err))
}

func TestMountSkillsPreservesUnknownMetadata(t *testing.T) {
	var putBody string
	getCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			getCount++
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Info:     wrapperspb.String("success"),
				Services: []*apiservice.Service{updatedService()},
			})
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			putBody = string(body)
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Info: wrapperspb.String("success"),
				Responses: []*apiservice.Response{{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Service: &apiservice.Service{
						Namespace:   wrapperspb.String("Production"),
						Name:        wrapperspb.String("customer-agent"),
						ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
						Revision:    wrapperspb.String("rev-1"),
					},
				}},
			})
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:new@1.0.0"}}, false)
	require.NoError(t, err)
	assert.Equal(t, 2, getCount)
	assert.Contains(t, putBody, "Production:old@1.0.0")
	assert.Contains(t, putBody, "Production:new@1.0.0")
	assert.Contains(t, putBody, `"type":999`)
	assert.NotContains(t, putBody, `"metadata"`)
	assert.Contains(t, putBody, `"revision":"rev-2"`)

	var rawServices []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(putBody), &rawServices))
	require.Len(t, rawServices, 1)
	written := &apiservice.Service{}
	require.NoError(t, protojson.Unmarshal(rawServices[0], written))
	require.Len(t, written.GetExportTo(), 1)
	assert.Equal(t, "Consumers", written.GetExportTo()[0].GetValue())
	assert.True(t, written.GetSyncToGlobalRegistry())
}

func TestMountSkillsReplaceEmptyReportsUnsupported(t *testing.T) {
	var putBody string
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{skillOnlyService()},
			})
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			putBody = string(body)
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Info: wrapperspb.String("success"),
				Responses: []*apiservice.Response{{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_NoNeedUpdate)),
					Info: wrapperspb.String("no need update"),
				}},
			})
			return
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	_, err := client.MountAgentSkills("Production", "customer-agent", nil, true)
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceWriteUnsupported, model.GetErrorCodeFromError(err))
	assert.NotContains(t, putBody, "Production:old@1.0.0")
	assert.Contains(t, putBody, `"extended_metadata":[]`)
}

func TestAlreadyEmptySkillWritesAreIdempotent(t *testing.T) {
	for _, operation := range []string{"replace-empty", "unmount-absent"} {
		t.Run(operation, func(t *testing.T) {
			putCount := 0
			client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.Method {
				case http.MethodGet:
					writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
						Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						Services: []*apiservice.Service{serviceWithSkills("rev-empty")},
					})
				case http.MethodPut:
					putCount++
					writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
						Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						Responses: []*apiservice.Response{{
							Code: wrapperspb.UInt32(uint32(apimodel.Code_NoNeedUpdate)),
							Info: wrapperspb.String("no need update"),
						}},
					})
				default:
					t.Fatalf("unexpected method %s", request.Method)
				}
			}))

			var (
				record *serviceRecord
				err    error
			)
			if operation == "replace-empty" {
				record, err = client.MountAgentSkills("Production", "customer-agent", nil, true)
			} else {
				record, err = client.UnmountAgentSkills(
					"Production", "customer-agent", []string{"Production:absent@1.0.0"})
			}
			require.NoError(t, err)
			require.NotNil(t, record)
			assert.Equal(t, "rev-empty", record.Service.GetRevision().GetValue())
			assert.Zero(t, putCount)
		})
	}
}

func TestMountSkillsIdenticalListIsIdempotent(t *testing.T) {
	var putBody string
	getCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			getCount++
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{updatedService()},
			})
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			putBody = string(body)
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Responses: []*apiservice.Response{{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_NoNeedUpdate)),
					Info: wrapperspb.String("no need update"),
				}},
			})
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	record, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:old@1.0.0"}}, false)
	require.NoError(t, err)
	assert.Equal(t, 2, getCount)
	assert.Contains(t, putBody, "Production:old@1.0.0")
	assert.Equal(t, "rev-2", record.Service.GetRevision().GetValue())
}

func updatedService() *apiservice.Service {
	return &apiservice.Service{
		Namespace:            wrapperspb.String("Production"),
		Name:                 wrapperspb.String("customer-agent"),
		ServiceType:          apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		Revision:             wrapperspb.String("rev-2"),
		ExportTo:             []*wrapperspb.StringValue{wrapperspb.String("Consumers")},
		SyncToGlobalRegistry: true,
		ExtendedMetadata: []*apiservice.ExtendedMetadata{
			{
				Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
				AgentSkill: &apiservice.AgentSkill{Id: "Production:old@1.0.0"},
			},
			{
				Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
				AgentSkill: &apiservice.AgentSkill{Id: "Production:keep@1.0.0"},
			},
			{
				Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
				AgentSkill: &apiservice.AgentSkill{Id: "Production:new@1.0.0"},
			},
			{
				Type: apiservice.ExtendedMetadata_ExtendedMetadataType(999),
			},
		},
	}
}

func skillOnlyService() *apiservice.Service {
	return &apiservice.Service{
		Namespace:   wrapperspb.String("Production"),
		Name:        wrapperspb.String("customer-agent"),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		ExtendedMetadata: []*apiservice.ExtendedMetadata{
			{
				Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
				AgentSkill: &apiservice.AgentSkill{Id: "Production:old@1.0.0"},
			},
			{
				Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
				AgentSkill: &apiservice.AgentSkill{Id: "Production:keep@1.0.0"},
			},
		},
	}
}

func serviceWithSkills(revision string, skillIDs ...string) *apiservice.Service {
	service := &apiservice.Service{
		Namespace:   wrapperspb.String("Production"),
		Name:        wrapperspb.String("customer-agent"),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		Revision:    wrapperspb.String(revision),
	}
	for _, skillID := range skillIDs {
		service.ExtendedMetadata = append(service.ExtendedMetadata, &apiservice.ExtendedMetadata{
			Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
			AgentSkill: &apiservice.AgentSkill{Id: skillID},
		})
	}
	return service
}

func successfulWriteResponse() *apiservice.BatchWriteResponse {
	return &apiservice.BatchWriteResponse{
		Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
		Responses: []*apiservice.Response{{
			Code:    wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			Service: &apiservice.Service{},
		}},
	}
}

func TestUnmountSkillsKeepsRemainingSkill(t *testing.T) {
	var putBody string
	getCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			getCount++
			if getCount > 1 {
				writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Services: []*apiservice.Service{{
						Namespace:   wrapperspb.String("Production"),
						Name:        wrapperspb.String("customer-agent"),
						ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
						Revision:    wrapperspb.String("rev-3"),
						ExtendedMetadata: []*apiservice.ExtendedMetadata{
							{
								Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
								AgentSkill: &apiservice.AgentSkill{Id: "Production:keep@1.0.0"},
							},
							{
								Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
								AgentSkill: &apiservice.AgentSkill{Id: "Production:new@1.0.0"},
							},
							{Type: apiservice.ExtendedMetadata_ExtendedMetadataType(999)},
						},
					}},
				})
				return
			}
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{updatedService()},
			})
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			putBody = string(body)
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Responses: []*apiservice.Response{{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				}},
			})
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	record, err := client.UnmountAgentSkills("Production", "customer-agent",
		[]string{"Production:old@1.0.0"})
	require.NoError(t, err)
	assert.Equal(t, 2, getCount)
	assert.NotContains(t, putBody, "Production:old@1.0.0")
	assert.Contains(t, putBody, "Production:keep@1.0.0")
	assert.Equal(t, "rev-3", record.Service.GetRevision().GetValue())
}

func TestMountSkillsSequentialWritesSurviveStaleReadBack(t *testing.T) {
	var putBodies []string
	getCount := 0
	putCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			getCount++
			var services []*apiservice.Service
			switch getCount {
			case 1, 2:
				services = []*apiservice.Service{serviceWithSkills("rev-1", "Production:old@1.0.0")}
			case 3, 4, 5:
				services = []*apiservice.Service{serviceWithSkills("rev-2",
					"Production:old@1.0.0", "Production:first@1.0.0")}
			default:
				services = []*apiservice.Service{serviceWithSkills("rev-3",
					"Production:old@1.0.0", "Production:first@1.0.0", "Production:second@1.0.0")}
			}
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: services,
			})
		case http.MethodPut:
			putCount++
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			putBodies = append(putBodies, string(body))
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
	require.NoError(t, err)
	_, err = client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
	require.NoError(t, err)

	require.Len(t, putBodies, 2)
	assert.Contains(t, putBodies[0], "Production:first@1.0.0")
	assert.NotContains(t, putBodies[0], "Production:second@1.0.0")
	assert.Contains(t, putBodies[1], "Production:first@1.0.0")
	assert.Contains(t, putBodies[1], "Production:second@1.0.0")
	assert.Equal(t, 6, getCount)
	assert.Equal(t, 2, putCount)
}

func TestMountSkillsWaitsForUpdatedSkillFields(t *testing.T) {
	oldSkill := &apiservice.AgentSkill{
		Id:          "Production:demo@1.0.0",
		Description: "old-description",
		Tags:        []string{"old-tag"},
		Examples:    []string{"old example"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
	}
	newSkill := &apiservice.AgentSkill{
		Id:          oldSkill.Id,
		Description: "new-description",
		Tags:        []string{"new-tag"},
		Examples:    []string{"new example"},
		InputModes:  []string{"text", "image"},
		OutputModes: []string{"text", "audio"},
	}
	var putBodies []string
	getCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			getCount++
			skill := oldSkill
			if getCount >= 3 {
				skill = newSkill
			}
			extended := []*apiservice.ExtendedMetadata{{
				Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
				AgentSkill: skill,
			}}
			if getCount >= 5 {
				extended = append(extended, &apiservice.ExtendedMetadata{
					Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
					AgentSkill: &apiservice.AgentSkill{Id: "Production:another@1.0.0"},
				})
			}
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{{
					Namespace:        wrapperspb.String("Production"),
					Name:             wrapperspb.String("customer-agent"),
					ServiceType:      apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
					Revision:         wrapperspb.String("rev-2"),
					ExtendedMetadata: extended,
				}},
			})
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			putBodies = append(putBodies, string(body))
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	record, err := client.MountAgentSkills("Production", "customer-agent", []*model.AgentSkill{{
		ID:          "Production:demo@1.0.0",
		Description: "new-description",
		Tags:        []string{"new-tag"},
		Examples:    []string{"new example"},
		InputModes:  []string{"text", "image"},
		OutputModes: []string{"text", "audio"},
	}}, false)
	require.NoError(t, err)
	require.Len(t, record.Service.GetExtendedMetadata(), 1)
	assert.Equal(t, "new-description",
		record.Service.GetExtendedMetadata()[0].GetAgentSkill().GetDescription())

	_, err = client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:another@1.0.0"}}, false)
	require.NoError(t, err)
	require.Len(t, putBodies, 2)
	assert.Contains(t, putBodies[1], "new-description")
	assert.Contains(t, putBodies[1], "Production:another@1.0.0")
}

func TestMountSkillsDoesNotUseStaleConsoleNodeAfterConfirmedWrite(t *testing.T) {
	latest := serviceWithSkills("rev-1", "Production:old@1.0.0")
	var putBodies []string
	putCount := 0
	writePut := func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		putBodies = append(putBodies, string(body))
		putCount++
		switch putCount {
		case 1:
			latest = serviceWithSkills("rev-2",
				"Production:old@1.0.0", "Production:first@1.0.0")
		case 2:
			latest = serviceWithSkills("rev-3", "Production:old@1.0.0",
				"Production:first@1.0.0", "Production:second@1.0.0")
		}
		writeProtoJSON(t, writer, successfulWriteResponse())
	}
	nodeA := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			writePut(writer, request)
			return
		}
		writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
			Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			Services: []*apiservice.Service{serviceWithSkills("rev-1", "Production:old@1.0.0")},
		})
	})
	nodeB := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			writePut(writer, request)
			return
		}
		writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
			Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			Services: []*apiservice.Service{latest},
		})
	})
	client := &Client{
		addresses:     []string{"http://node-a.test", "http://node-b.test"},
		timeout:       time.Second,
		serviceStates: make(map[string]serviceWriteState),
		http: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			if request.URL.Host == "node-a.test" {
				nodeA.ServeHTTP(recorder, request)
			} else {
				nodeB.ServeHTTP(recorder, request)
			}
			return recorder.Result(), nil
		})},
	}

	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
	require.NoError(t, err)
	_, err = client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
	require.NoError(t, err)

	require.Len(t, putBodies, 2)
	assert.Contains(t, putBodies[0], "Production:first@1.0.0")
	assert.Contains(t, putBodies[1], "Production:first@1.0.0")
	assert.Contains(t, putBodies[1], "Production:second@1.0.0")
}

func TestMountSkillsStaleSnapshotTimesOutAsUnconfirmedWrite(t *testing.T) {
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{serviceWithSkills(
					"rev-1", "Production:old@1.0.0")},
			})
		case http.MethodPut:
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))
	client.timeout = 250 * time.Millisecond

	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:new@1.0.0"}}, false)
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceWriteError, model.GetErrorCodeFromError(err))
	assert.Contains(t, err.Error(), "was not confirmed before timeout")
}

func TestMountSkillsRecoversAppliedWriteAfterConfirmationTimeout(t *testing.T) {
	visible := serviceWithSkills("rev-1", "Production:old@1.0.0")
	putCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{visible},
			})
			return
		}
		putCount++
		if putCount == 1 {
			visible = serviceWithSkills("rev-2", "Production:old@1.0.0", "Production:first@1.0.0")
		}
		writeProtoJSON(t, writer, successfulWriteResponse())
	}))
	client.timeout = 100 * time.Millisecond
	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
	require.NoError(t, err)
	_, err = client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
	require.Error(t, err)
	visible = serviceWithSkills("rev-3", "Production:old@1.0.0",
		"Production:first@1.0.0", "Production:second@1.0.0")
	_, err = client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
	require.NoError(t, err)
	assert.Equal(t, 3, putCount)
}

func TestConcurrentMountSkillsPreservesSameOwnerWrites(t *testing.T) {
	var mutex sync.Mutex
	visible := serviceWithSkills("rev-1", "Production:old@1.0.0")
	getCount, putCount := 0, 0
	firstRead := make(chan struct{})
	secondRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			mutex.Lock()
			getCount++
			index := getCount
			beforeWrite := putCount == 0
			snapshot := proto.Clone(visible).(*apiservice.Service)
			mutex.Unlock()
			var wait <-chan struct{}
			switch index {
			case 1:
				close(firstRead)
				wait = releaseFirst
			case 2:
				if beforeWrite {
					close(secondRead)
					wait = firstDone
				}
			}
			if wait != nil {
				select {
				case <-wait:
				case <-request.Context().Done():
					writer.WriteHeader(http.StatusRequestTimeout)
					return
				}
			}
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{snapshot},
			})
			return
		}
		var payloads []json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&payloads); err != nil || len(payloads) != 1 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		written := &apiservice.Service{}
		if err := protojson.Unmarshal(payloads[0], written); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mutex.Lock()
		putCount++
		written.ServiceType = apiservice.ServiceType_SERVICE_TYPE_AI_AGENT
		written.Revision = wrapperspb.String(fmt.Sprintf("rev-%d", putCount+1))
		visible = written
		mutex.Unlock()
		writeProtoJSON(t, writer, successfulWriteResponse())
	}))
	client.timeout = time.Second
	results := make(chan error, 2)
	go func() {
		_, err := client.MountAgentSkills("Production", "customer-agent",
			[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
		results <- err
		close(firstDone)
	}()
	select {
	case <-firstRead:
	case <-time.After(2 * time.Second):
		t.Fatal("first write did not reach its initial read")
	}
	go func() {
		_, err := client.MountAgentSkills("Production", "customer-agent",
			[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
		results <- err
	}()
	select {
	case <-secondRead:
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	for index := 0; index < 2; index++ {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent write did not finish")
		}
	}
	mutex.Lock()
	defer mutex.Unlock()
	var ids []string
	for _, entry := range visible.GetExtendedMetadata() {
		ids = append(ids, entry.GetAgentSkill().GetId())
	}
	assert.ElementsMatch(t, []string{"Production:old@1.0.0", "Production:first@1.0.0", "Production:second@1.0.0"}, ids)
	assert.Equal(t, 2, putCount)
}

func TestMountSkillsSharesOneOperationDeadline(t *testing.T) {
	getCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
			writer.WriteHeader(http.StatusRequestTimeout)
			return
		case <-time.After(40 * time.Millisecond):
		}
		if request.Method == http.MethodGet {
			getCount++
			visible := serviceWithSkills("rev-1", "Production:old@1.0.0")
			if getCount > 1 {
				visible = serviceWithSkills("rev-2", "Production:old@1.0.0", "Production:new@1.0.0")
			}
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{visible},
			})
			return
		}
		writeProtoJSON(t, writer, successfulWriteResponse())
	}))
	client.timeout = 100 * time.Millisecond
	start := time.Now()
	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:new@1.0.0"}}, false)
	require.Error(t, err, "read, write, and confirmation must not each receive a new timeout")
	assert.Equal(t, model.ErrCodeServiceWriteError, model.GetErrorCodeFromError(err))
	assert.Less(t, time.Since(start), 200*time.Millisecond)
}

func TestServiceWriteLockHonorsDeadlineAndServiceIsolation(t *testing.T) {
	client := &Client{}
	unlock, err := client.lockServiceWrite(context.Background(), "Production", "customer-agent")
	require.NoError(t, err)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = client.lockServiceWrite(ctx, "Production", "customer-agent")
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceWriteError, model.GetErrorCodeFromError(err))
	assert.Contains(t, err.Error(), "waiting for another write")
	other, err := client.lockServiceWrite(context.Background(), "Production", "other-agent")
	require.NoError(t, err)
	other()
	other, err = client.lockServiceWrite(context.Background(), "Other", "customer-agent")
	require.NoError(t, err)
	other()
}

func TestFailedServiceWriteReleasesLock(t *testing.T) {
	getCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		getCount++
		if getCount == 1 {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
			Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			Services: []*apiservice.Service{serviceWithSkills("rev-1")},
		})
	}))
	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:new@1.0.0"}}, false)
	require.Error(t, err)
	_, err = client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{Namespace: "Production", Service: "customer-agent"})
	require.NoError(t, err)
	assert.Equal(t, 2, getCount)
}

func TestMountSkillsReconcilesUncertainWriteBeforeNextMerge(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(fmt.Sprintf("initially_applied_%t", applied), func(t *testing.T) {
			visible := serviceWithSkills("rev-1", "Production:old@1.0.0")
			var putBodies []string
			client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodGet {
					writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
						Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						Services: []*apiservice.Service{visible},
					})
					return
				}
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				putBodies = append(putBodies, string(body))
				if len(putBodies) <= 2 {
					visible = serviceWithSkills("rev-2", "Production:old@1.0.0", "Production:first@1.0.0")
				} else {
					visible = serviceWithSkills("rev-3", "Production:old@1.0.0",
						"Production:first@1.0.0", "Production:second@1.0.0")
				}
				writeProtoJSON(t, writer, successfulWriteResponse())
			}))
			transport := client.http.Transport
			client.http.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodPut && len(putBodies) == 0 {
					if applied {
						response, err := transport.RoundTrip(request)
						require.NoError(t, err)
						require.NoError(t, response.Body.Close())
						visible = serviceWithSkills("rev-1", "Production:old@1.0.0")
					} else {
						body, err := io.ReadAll(request.Body)
						require.NoError(t, err)
						putBodies = append(putBodies, string(body))
					}
					return nil, errors.New("response lost")
				}
				return transport.RoundTrip(request)
			})
			_, err := client.MountAgentSkills("Production", "customer-agent",
				[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
			require.Error(t, err)
			_, err = client.MountAgentSkills("Production", "customer-agent",
				[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
			require.NoError(t, err)
			require.Len(t, putBodies, 3)
			assert.Equal(t, putBodies[0], putBodies[1], "replay the same target before merging another request")
			assert.Contains(t, putBodies[2], "Production:first@1.0.0")
			assert.Contains(t, putBodies[2], "Production:second@1.0.0")
		})
	}
}

func TestMountSkillsKeepsUncertainWriteBeforeLaterAddressRejection(t *testing.T) {
	old := serviceWithSkills("rev-1", "Production:old@1.0.0")
	visible := old
	getCount, putCount := 0, 0
	client := newTestClient(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client.addresses = []string{"http://console-a.test", "http://console-b.test"}
	client.next = 1
	client.http.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		if request.Method == http.MethodGet {
			getCount++
			snapshot := visible
			if getCount <= 2 {
				snapshot = old
			}
			writeProtoJSON(t, recorder, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{snapshot},
			})
			return recorder.Result(), nil
		}
		require.Equal(t, http.MethodPut, request.Method)
		putCount++
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		var payloads []serviceWritePayload
		require.NoError(t, json.Unmarshal(body, &payloads))
		require.Len(t, payloads, 1)
		if putCount == 2 {
			require.Equal(t, "console-b.test", request.URL.Host)
			recorder.WriteHeader(http.StatusForbidden)
			return recorder.Result(), nil
		}
		require.NotNil(t, payloads[0].ExtendedMetadata)
		extendedJSON, err := json.Marshal(*payloads[0].ExtendedMetadata)
		require.NoError(t, err)
		next := serviceWithSkills("rev-applied")
		next.ExtendedMetadata, err = decodeKnownExtendedMetadata(map[string]json.RawMessage{
			"extended_metadata": extendedJSON,
		})
		require.NoError(t, err)
		visible = next
		if putCount == 1 {
			require.Equal(t, "console-a.test", request.URL.Host)
			return nil, errors.New("response lost after service write applied")
		}
		writeProtoJSON(t, recorder, successfulWriteResponse())
		return recorder.Result(), nil
	})

	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
	require.Error(t, err)
	require.Equal(t, 2, putCount)
	require.Contains(t, err.Error(), "403")

	record, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
	require.NoError(t, err)
	var ids []string
	for _, entry := range record.Service.GetExtendedMetadata() {
		ids = append(ids, entry.GetAgentSkill().GetId())
	}
	require.Contains(t, ids, "Production:first@1.0.0",
		"later endpoint rejection hid a preceding applied uncertain write")
	require.Contains(t, ids, "Production:second@1.0.0")
	require.Equal(t, 4, putCount,
		"the uncertain target must be replayed before a separate merge write")
}

func TestMountSkillsReconcilesServerErrorBeforeNextMerge(t *testing.T) {
	visible := serviceWithSkills("rev-1", "Production:old@1.0.0")
	var putBodies []string
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{visible},
			})
			return
		}
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		putBodies = append(putBodies, string(body))
		if len(putBodies) == 1 {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payloads []serviceWritePayload
		require.NoError(t, json.Unmarshal(body, &payloads))
		require.Len(t, payloads, 1)
		extendedJSON, err := json.Marshal(*payloads[0].ExtendedMetadata)
		require.NoError(t, err)
		visible = serviceWithSkills(fmt.Sprintf("rev-%d", len(putBodies)+1))
		visible.ExtendedMetadata, err = decodeKnownExtendedMetadata(map[string]json.RawMessage{
			"extended_metadata": extendedJSON,
		})
		require.NoError(t, err)
		writeProtoJSON(t, writer, successfulWriteResponse())
	}))

	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
	require.Error(t, err)
	require.Equal(t, 1, len(putBodies))
	record, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
	require.NoError(t, err)
	require.Len(t, putBodies, 3)
	require.Equal(t, putBodies[0], putBodies[1],
		"an HTTP 5xx must preserve the original target for recovery")
	var ids []string
	for _, entry := range record.Service.GetExtendedMetadata() {
		ids = append(ids, entry.GetAgentSkill().GetId())
	}
	require.Contains(t, ids, "Production:first@1.0.0")
	require.Contains(t, ids, "Production:second@1.0.0")
}

func TestMountSkillsRejectedWriteKeepsConfirmedState(t *testing.T) {
	visible := serviceWithSkills("rev-1", "Production:old@1.0.0")
	putCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{visible},
			})
			return
		}
		putCount++
		if putCount == 1 {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		visible = serviceWithSkills("rev-2", "Production:old@1.0.0", "Production:second@1.0.0")
		writeProtoJSON(t, writer, successfulWriteResponse())
	}))
	confirmed, err := buildSkillExtendedMetadata([]*model.AgentSkill{{ID: "Production:old@1.0.0"}})
	require.NoError(t, err)
	client.rememberConfirmedExtendedMetadata("Production", "customer-agent", confirmed)
	_, err = client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
	require.Error(t, err)
	_, err = client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
	require.NoError(t, err)
}

func TestUnmountSkillsRecoversConfirmedEmptyWrite(t *testing.T) {
	visible := serviceWithSkills("rev-1", "Production:old@1.0.0")
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{visible},
			})
			return
		}
		writeProtoJSON(t, writer, successfulWriteResponse())
	}))
	client.timeout = 100 * time.Millisecond
	_, err := client.UnmountAgentSkills("Production", "customer-agent", []string{"Production:old@1.0.0"})
	require.Error(t, err)
	visible = serviceWithSkills("rev-2")
	_, err = client.UnmountAgentSkills("Production", "customer-agent", []string{"Production:old@1.0.0"})
	require.NoError(t, err)
}

func TestMountSkillsPreservesFutureExtendedMetadataEnum(t *testing.T) {
	var putBody string
	getCount := 0
	initial := `{"code":200000,"info":"success","services":[` +
		`{"namespace":"Production","name":"customer-agent",` +
		`"service_type":"SERVICE_TYPE_AI_AGENT","revision":"rev-1",` +
		`"extended_metadata":[{"type":"EXTENDED_METADATA_FUTURE","payload":{"future":"keep"}}]}]}`
	updated := `{"code":200000,"info":"success","services":[` +
		`{"namespace":"Production","name":"customer-agent",` +
		`"service_type":"SERVICE_TYPE_AI_AGENT","revision":"rev-2",` +
		`"extended_metadata":[{"type":"EXTENDED_METADATA_FUTURE","payload":{"future":"keep"}},` +
		`{"type":1,"agent_skill":{"id":"Production:new@1.0.0"}}]}]}`
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			getCount++
			writer.Header().Set("Content-Type", "application/json")
			if getCount == 1 {
				_, _ = writer.Write([]byte(initial))
				return
			}
			_, _ = writer.Write([]byte(updated))
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			putBody = string(body)
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	record, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:new@1.0.0"}}, false)
	require.NoError(t, err)
	assert.Equal(t, 2, getCount)
	assert.Contains(t, putBody, "EXTENDED_METADATA_FUTURE")
	assert.Contains(t, putBody, `"future":"keep"`)
	assert.Contains(t, putBody, "Production:new@1.0.0")
	assert.Equal(t, "rev-2", record.Service.GetRevision().GetValue())
}

func TestConsoleClientRejectsRedirectWithoutForwardingToken(t *testing.T) {
	cfg := &ServiceWriterConfigImpl{
		Addresses: []string{"http://console.test"},
		Token:     "secret-token",
	}
	cfg.SetDefault()
	client := New(cfg)
	var requestURLs []string
	client.http.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requestURLs = append(requestURLs, request.URL.String())
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": []string{"http://other.test/collect"}},
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})

	_, status, err := client.doJSON(context.Background(), http.MethodPost, serviceRequestPath, nil,
		map[string]string{"body": "kept"})
	require.Error(t, err)
	assert.Equal(t, http.StatusTemporaryRedirect, status)
	require.Len(t, requestURLs, 1)
	assert.Equal(t, "http://console.test"+serviceRequestPath, requestURLs[0])
	assert.Contains(t, err.Error(), "307")
}

func TestServiceWriterHTTPErrorsAndToken(t *testing.T) {
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "test-token", request.Header.Get(headerToken))
		assert.Empty(t, request.Header.Get("Polaris-Token"))
		assert.NotEmpty(t, request.Header.Get(headerRequestID))
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"code":401,"info":"unauthorized"}`))
	}))

	client.token = "test-token"
	_, err := client.GetService("Production", "customer-agent")
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceWriteError, model.GetErrorCodeFromError(err))
	assert.True(t, strings.Contains(err.Error(), "401"))
}

func TestServiceWriterTruncatesLargeHTTPErrorBody(t *testing.T) {
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(strings.Repeat("x", 16*1024)))
	}))
	_, status, err := client.doJSON(context.Background(), http.MethodGet, serviceRequestPath, nil, nil)
	require.Error(t, err)
	require.Equal(t, http.StatusInternalServerError, status)
	require.Less(t, len(err.Error()), 2*1024)
	require.Contains(t, err.Error(), "xxxxx")
	require.Contains(t, err.Error(), "...")
}

func TestServiceWriterMalformedResponseAndUnsupported(t *testing.T) {
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{`))
	}))
	_, err := client.GetService("Production", "customer-agent")
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceWriteError, model.GetErrorCodeFromError(err))

	unsupported := &Client{addresses: nil, timeout: time.Second}
	_, err = unsupported.GetService("Production", "customer-agent")
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceWriteUnsupported, model.GetErrorCodeFromError(err))
}

func TestMountSkillsConfirmsReorderedExtendedMetadata(t *testing.T) {
	current := &apiservice.Service{
		Namespace:   wrapperspb.String("Production"),
		Name:        wrapperspb.String("customer-agent"),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		ExtendedMetadata: []*apiservice.ExtendedMetadata{{
			Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
			AgentSkill: &apiservice.AgentSkill{Id: "Production:z-last@1.0.0"},
		}},
	}
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{current},
			})
		case http.MethodPut:
			var payloads []serviceWritePayload
			require.NoError(t, json.NewDecoder(request.Body).Decode(&payloads))
			require.Len(t, payloads, 1)
			current.ExtendedMetadata = nil
			for index := len(*payloads[0].ExtendedMetadata) - 1; index >= 0; index-- {
				entry := &apiservice.ExtendedMetadata{}
				require.NoError(t, protojson.Unmarshal((*payloads[0].ExtendedMetadata)[index], entry))
				current.ExtendedMetadata = append(current.ExtendedMetadata, entry)
			}
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Responses: []*apiservice.Response{{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				}},
			})
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))
	client.timeout = 150 * time.Millisecond
	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:a-first@1.0.0"}}, false)
	require.NoError(t, err)
	_, err = client.UnmountAgentSkills("Production", "customer-agent", []string{"Production:z-last@1.0.0"})
	require.NoError(t, err)
	require.Len(t, current.ExtendedMetadata, 1)
	assert.Equal(t, "Production:a-first@1.0.0", current.ExtendedMetadata[0].GetAgentSkill().GetId())
}

func TestExtendedMetadataConfirmationRetainsMultiplicityAndFields(t *testing.T) {
	first := json.RawMessage(`{"type":1,"agent_skill":{"id":"Production:a@1","description":"current"}}`)
	second := json.RawMessage(`{"type":1,"agent_skill":{"id":"Production:b@1"}}`)
	unknown := json.RawMessage(`{"type":"EXTENDED_METADATA_FUTURE","future":{"id":9007199254740993}}`)
	tests := []struct {
		name     string
		expected []json.RawMessage
		actual   []json.RawMessage
		match    bool
	}{
		{name: "reordered", expected: []json.RawMessage{first, second, unknown}, actual: []json.RawMessage{unknown, second, first}, match: true},
		{name: "duplicates preserved", expected: []json.RawMessage{first, first, second}, actual: []json.RawMessage{second, first, first}, match: true},
		{name: "duplicate count changed", expected: []json.RawMessage{first, first, second}, actual: []json.RawMessage{second, second, first}},
		{name: "old fields", expected: []json.RawMessage{first, second}, actual: []json.RawMessage{second, json.RawMessage(`{"type":1,"agent_skill":{"id":"Production:a@1","description":"old"}}`)}},
		{name: "unknown integer changed", expected: []json.RawMessage{unknown}, actual: []json.RawMessage{json.RawMessage(`{"type":"EXTENDED_METADATA_FUTURE","future":{"id":9007199254740992}}`)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.actual)
			require.NoError(t, err)
			match, err := extendedMetadataMatches(&serviceRecord{Raw: map[string]json.RawMessage{"extended_metadata": raw}}, test.expected)
			require.NoError(t, err)
			assert.Equal(t, test.match, match)
		})
	}
}

func TestDecodeAgentSkillPayloadAcceptsProtoJSONNames(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"id":"Production:demo@1","input_modes":["text","image"],"output_modes":["text","audio"],"future_field":"retained in raw"}`),
		json.RawMessage(`{"id":"Production:demo@1","inputModes":["text","image"],"outputModes":["text","audio"],"futureField":"retained in raw"}`),
	} {
		skill, err := decodeAgentSkillPayload(raw)
		require.NoError(t, err)
		require.NotNil(t, skill)
		assert.Equal(t, []string{"text", "image"}, skill.InputModes)
		assert.Equal(t, []string{"text", "audio"}, skill.OutputModes)
	}
}

func TestExtendedMetadataConfirmationIncludesCamelCaseModes(t *testing.T) {
	camel := json.RawMessage(`{"type":1,"agentSkill":{"id":"Production:demo@1","inputModes":["text","image"],"outputModes":["text","audio"]}}`)
	for _, test := range []struct {
		name   string
		actual json.RawMessage
		match  bool
	}{
		{name: "equivalent snake case", actual: json.RawMessage(`{"type":1,"agent_skill":{"id":"Production:demo@1","input_modes":["text","image"],"output_modes":["text","audio"]}}`), match: true},
		{name: "changed input modes", actual: json.RawMessage(`{"type":1,"agentSkill":{"id":"Production:demo@1","inputModes":["text"],"outputModes":["text","audio"]}}`)},
		{name: "changed output modes", actual: json.RawMessage(`{"type":1,"agentSkill":{"id":"Production:demo@1","inputModes":["text","image"],"outputModes":["text"]}}`)},
		{name: "missing modes", actual: json.RawMessage(`{"type":1,"agentSkill":{"id":"Production:demo@1"}}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			values, err := json.Marshal([]json.RawMessage{test.actual})
			require.NoError(t, err)
			matched, err := extendedMetadataMatches(&serviceRecord{Raw: map[string]json.RawMessage{"extendedMetadata": values}}, []json.RawMessage{camel})
			require.NoError(t, err)
			assert.Equal(t, test.match, matched)
		})
	}
}

func TestMountSkillsPreservesProtoJSONModes(t *testing.T) {
	getCount := 0
	client := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			writeProtoJSON(t, w, successfulWriteResponse())
			return
		}
		getCount++
		if getCount == 1 {
			_, _ = fmt.Fprint(w, `{"code":200000,"services":[{"namespace":"Production","name":"customer-agent","service_type":"SERVICE_TYPE_AI_AGENT","revision":"rev-1"}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"code":200000,"services":[{"namespace":"Production","name":"customer-agent","service_type":"SERVICE_TYPE_AI_AGENT","revision":"rev-2","extendedMetadata":[{"type":1,"agentSkill":{"id":"Production:demo@1","inputModes":["text","image"],"outputModes":["text","audio"]}}]}]}`)
	}))
	client.timeout = 150 * time.Millisecond
	record, err := client.MountAgentSkills("Production", "customer-agent", []*model.AgentSkill{{
		ID: "Production:demo@1", InputModes: []string{"text", "image"}, OutputModes: []string{"text", "audio"},
	}}, false)
	require.NoError(t, err)
	require.Len(t, record.Service.ExtendedMetadata, 1)
	assert.Equal(t, []string{"text", "image"}, record.Service.ExtendedMetadata[0].AgentSkill.InputModes)
	assert.Equal(t, []string{"text", "audio"}, record.Service.ExtendedMetadata[0].AgentSkill.OutputModes)
}

func TestNormalizeAgentSkillForComparePreservesSource(t *testing.T) {
	skill, err := decodeAgentSkillPayload(json.RawMessage(`{"id":"Production:demo@1","input_modes":["text"],"output_modes":["audio"]}`))
	require.NoError(t, err)
	original := proto.Clone(skill)
	normalized, err := normalizeAgentSkillForCompare(skill)
	require.NoError(t, err)
	assert.Equal(t, "demo", normalized.Name)
	assert.Equal(t, "1", normalized.Version)
	assert.True(t, proto.Equal(original, skill))
	normalized.InputModes[0] = "image"
	assert.Equal(t, []string{"text"}, skill.InputModes)
}

func TestEnsureAgentServiceUpdatesExistingMetadataTriState(t *testing.T) {
	tests := []struct {
		name            string
		metadata        map[string]string
		expectedCurrent map[string]string
		wantPut         bool
		wantBody        string
	}{
		{name: "nil_preserves", metadata: nil, expectedCurrent: map[string]string{"old": "value"}, wantPut: false},
		{name: "empty_clears", metadata: map[string]string{}, expectedCurrent: map[string]string{}, wantPut: true, wantBody: `"metadata":{}`},
		{
			name: "non_empty_replaces", metadata: map[string]string{"team": "search"},
			expectedCurrent: map[string]string{"team": "search"},
			wantPut:         true, wantBody: `"metadata":{"team":"search"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &apiservice.Service{
				Namespace:   wrapperspb.String("Production"),
				Name:        wrapperspb.String("search-agent"),
				ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
				Revision:    wrapperspb.String("rev-1"),
				Metadata:    map[string]string{"old": "value"},
			}
			putBody := ""
			putCount := 0
			client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.Method {
				case http.MethodGet:
					writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
						Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						Services: []*apiservice.Service{current},
					})
				case http.MethodPut:
					body, err := io.ReadAll(request.Body)
					require.NoError(t, err)
					putBody, putCount = string(body), putCount+1
					current.Metadata = test.metadata
					current.Revision = wrapperspb.String("rev-2")
					writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
						Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						Responses: []*apiservice.Response{{
							Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						}},
					})
				default:
					t.Fatalf("unexpected method %s", request.Method)
				}
			}))
			record, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
				Namespace: "Production", Service: "search-agent", Metadata: test.metadata,
			})
			require.NoError(t, err)
			if len(test.expectedCurrent) == 0 {
				assert.Empty(t, record.Service.GetMetadata())
			} else {
				assert.Equal(t, test.expectedCurrent, record.Service.GetMetadata())
			}
			assert.Equal(t, map[bool]int{true: 1, false: 0}[test.wantPut], putCount)
			if test.wantBody != "" {
				assert.Contains(t, putBody, test.wantBody)
			}
		})
	}
}

func TestEnsureAgentServiceReplacesSkillExtendedMetadataTriState(t *testing.T) {
	tests := []struct {
		name      string
		skills    []model.AgentSkill
		wantPut   bool
		wantSkill string
		wantEmpty bool
	}{
		{name: "nil_preserves", skills: nil, wantPut: false},
		{name: "empty_clears", skills: []model.AgentSkill{}, wantPut: true, wantEmpty: true},
		{
			name: "non_empty_replaces", skills: []model.AgentSkill{{ID: "Production:new@1.0.0"}},
			wantPut: true, wantSkill: "Production:new@1.0.0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := serviceWithSkills("rev-1", "Production:old@1.0.0")
			current.ServiceType = apiservice.ServiceType_SERVICE_TYPE_AI_AGENT
			current.ExtendedMetadata = append(current.ExtendedMetadata, &apiservice.ExtendedMetadata{
				Type: apiservice.ExtendedMetadata_ExtendedMetadataType(999),
			})
			putBody := ""
			putCount := 0
			client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.Method {
				case http.MethodGet:
					writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
						Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						Services: []*apiservice.Service{current},
					})
				case http.MethodPut:
					body, err := io.ReadAll(request.Body)
					require.NoError(t, err)
					putBody, putCount = string(body), putCount+1
					if len(test.skills) == 0 {
						current.ExtendedMetadata = nil
					} else {
						current.ExtendedMetadata = []*apiservice.ExtendedMetadata{{
							Type: apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
							AgentSkill: &apiservice.AgentSkill{
								Id: test.skills[0].ID, Name: "new", Version: "1.0.0",
							},
						}}
					}
					current.Revision = wrapperspb.String("rev-2")
					writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
						Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						Responses: []*apiservice.Response{{
							Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
						}},
					})
				default:
					t.Fatalf("unexpected method %s", request.Method)
				}
			}))
			record, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
				Namespace: "Production", Service: "customer-agent", Skills: test.skills,
			})
			require.NoError(t, err)
			assert.Equal(t, map[bool]int{true: 1, false: 0}[test.wantPut], putCount)
			if test.wantEmpty {
				assert.Contains(t, putBody, `"extended_metadata":[]`)
				assert.NotContains(t, putBody, `"type":999`)
			}
			if test.wantSkill != "" {
				assert.Contains(t, putBody, test.wantSkill)
				assert.NotContains(t, putBody, `"type":999`)
			}
			if test.wantEmpty {
				assert.Empty(t, record.Service.GetExtendedMetadata())
			}
		})
	}
}

func TestEnsureAgentServiceReconcilesUncertainMetadataBeforeNextWrite(t *testing.T) {
	visible := &apiservice.Service{
		Namespace:   wrapperspb.String("Production"),
		Name:        wrapperspb.String("customer-agent"),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		Revision:    wrapperspb.String("rev-1"),
		Metadata:    map[string]string{"state": "old"},
	}
	putCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{
					proto.Clone(visible).(*apiservice.Service),
				},
			})
			return
		}
		putCount++
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		var payloads []serviceWritePayload
		require.NoError(t, json.Unmarshal(body, &payloads))
		require.Len(t, payloads, 1)
		require.NotNil(t, payloads[0].Metadata)
		visible.Metadata = cloneMap(*payloads[0].Metadata)
		visible.Revision = wrapperspb.String(fmt.Sprintf("rev-%d", putCount+1))
		writeProtoJSON(t, writer, successfulWriteResponse())
	}))
	transport := client.http.Transport
	client.http.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPut && putCount == 0 {
			response, err := transport.RoundTrip(request)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			return nil, errors.New("response lost after metadata update")
		}
		return transport.RoundTrip(request)
	})

	_, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "customer-agent",
		Metadata: map[string]string{"state": "uncertain-new"},
	})
	require.Error(t, err)
	record, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "customer-agent",
	})
	require.NoError(t, err)
	assert.Equal(t, "uncertain-new", record.Service.GetMetadata()["state"])
	assert.Equal(t, 1, putCount, "confirmed uncertain metadata must not be replayed again")

	record, err = client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "customer-agent",
		Metadata: map[string]string{"state": "next"},
	})
	require.NoError(t, err)
	assert.Equal(t, "next", record.Service.GetMetadata()["state"])
	assert.Equal(t, 2, putCount)
}

func TestServiceStateAcceptsOnlyUnseenExternalRevisions(t *testing.T) {
	state := serviceWriteState{
		revision: "rev-3",
		superseded: map[string]struct{}{
			"rev-1": {},
			"rev-2": {},
		},
	}
	assert.False(t, serviceStateAcceptsExternalRevision(state, "rev-1"))
	assert.False(t, serviceStateAcceptsExternalRevision(state, "rev-2"))
	assert.False(t, serviceStateAcceptsExternalRevision(state, "rev-3"))
	assert.True(t, serviceStateAcceptsExternalRevision(state, "rev-4"))
	assert.False(t, serviceStateAcceptsExternalRevision(state, ""))
}

func TestEnsureAgentServiceAcceptsExternalRevisionAfterConfirmedWrite(t *testing.T) {
	current := &apiservice.Service{
		Namespace:   wrapperspb.String("Production"),
		Name:        wrapperspb.String("agent"),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		Revision:    wrapperspb.String("revision-1"),
		Metadata:    map[string]string{"business": "old"},
	}
	putCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{proto.Clone(current).(*apiservice.Service)},
			})
		case http.MethodPut:
			putCount++
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var payloads []serviceWritePayload
			require.NoError(t, json.Unmarshal(body, &payloads))
			require.Len(t, payloads, 1)
			if payloads[0].Metadata != nil {
				current.Metadata = cloneMap(*payloads[0].Metadata)
			}
			current.Revision = wrapperspb.String(fmt.Sprintf("revision-sdk-%d", putCount+1))
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))
	client.timeout = 250 * time.Millisecond

	_, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "agent",
		Metadata: map[string]string{"business": "sdk-confirmed"},
	})
	require.NoError(t, err)

	current.Revision = wrapperspb.String("revision-3-external")
	current.Metadata = map[string]string{"business": "console-update"}
	record, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "agent",
	})
	require.NoError(t, err, "nil metadata must accept a newer external revision")
	assert.Equal(t, "console-update", record.Service.GetMetadata()["business"])

	record, err = client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "agent",
		Metadata: map[string]string{"business": "sdk-next"},
	})
	require.NoError(t, err)
	assert.Equal(t, "sdk-next", record.Service.GetMetadata()["business"])
	assert.Equal(t, 2, putCount, "the next write must start from the external revision")
}

func TestEnsureAgentServiceRecoversAfterReadbackObservesExternalRevision(t *testing.T) {
	current := &apiservice.Service{
		Namespace:   wrapperspb.String("Production"),
		Name:        wrapperspb.String("agent"),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		Revision:    wrapperspb.String("revision-1"),
		Metadata:    map[string]string{"business": "old"},
	}
	putCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{proto.Clone(current).(*apiservice.Service)},
			})
		case http.MethodPut:
			putCount++
			current.Revision = wrapperspb.String("revision-sdk")
			current.Metadata = map[string]string{"business": "sdk-target"}
			// Simulate an external writer racing with read-after-write.
			current.Revision = wrapperspb.String("revision-external")
			current.Metadata = map[string]string{"business": "console-update"}
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))
	client.timeout = 250 * time.Millisecond

	_, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "agent",
		Metadata: map[string]string{"business": "sdk-target"},
	})
	require.Error(t, err, "the first call must report the unconfirmed target")

	record, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "agent",
	})
	require.NoError(t, err, "an unseen external revision must release the pending write")
	assert.Equal(t, "console-update", record.Service.GetMetadata()["business"])
	assert.Equal(t, 1, putCount)
}

func TestEnsureAgentServiceRecreatesAfterExternalDeletion(t *testing.T) {
	current := &apiservice.Service{
		Namespace:   wrapperspb.String("Production"),
		Name:        wrapperspb.String("agent"),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		Revision:    wrapperspb.String("revision-1"),
		Metadata:    map[string]string{"business": "old"},
	}
	deleted := false
	putCount, postCount := 0, 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			response := &apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			}
			if !deleted {
				response.Services = []*apiservice.Service{proto.Clone(current).(*apiservice.Service)}
			}
			writeProtoJSON(t, writer, response)
		case http.MethodPut:
			putCount++
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var payloads []serviceWritePayload
			require.NoError(t, json.Unmarshal(body, &payloads))
			require.Len(t, payloads, 1)
			if payloads[0].Metadata != nil {
				current.Metadata = cloneMap(*payloads[0].Metadata)
			}
			current.Revision = wrapperspb.String("revision-2-sdk")
			writeProtoJSON(t, writer, successfulWriteResponse())
		case http.MethodPost:
			postCount++
			deleted = false
			current = &apiservice.Service{
				Namespace:   wrapperspb.String("Production"),
				Name:        wrapperspb.String("agent"),
				ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
				Revision:    wrapperspb.String("revision-created"),
				Metadata:    map[string]string{},
			}
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))
	client.timeout = 250 * time.Millisecond

	_, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "agent",
		Metadata: map[string]string{"business": "sdk-confirmed"},
	})
	require.NoError(t, err)

	deleted = true
	record, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "agent",
	})
	require.NoError(t, err, "an external deletion must not keep the confirmed write barrier")
	assert.Empty(t, record.Service.GetMetadata())
	assert.Equal(t, 1, putCount)
	assert.Equal(t, 1, postCount)
}

func TestMountAgentSkillsAcceptsExternalRevisionAfterConfirmedWrite(t *testing.T) {
	current := serviceWithSkills("rev-1", "Production:old@1.0.0")
	putCount := 0
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code:     wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Services: []*apiservice.Service{proto.Clone(current).(*apiservice.Service)},
			})
		case http.MethodPut:
			putCount++
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var payloads []serviceWritePayload
			require.NoError(t, json.Unmarshal(body, &payloads))
			require.Len(t, payloads, 1)
			if payloads[0].ExtendedMetadata != nil {
				extendedJSON, marshalErr := json.Marshal(*payloads[0].ExtendedMetadata)
				require.NoError(t, marshalErr)
				current.ExtendedMetadata, err = decodeKnownExtendedMetadata(map[string]json.RawMessage{
					"extended_metadata": extendedJSON,
				})
				require.NoError(t, err)
			}
			current.Revision = wrapperspb.String(fmt.Sprintf("rev-%d", putCount+2))
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	_, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:first@1.0.0"}}, false)
	require.NoError(t, err)

	current.ExtendedMetadata = append(current.ExtendedMetadata, &apiservice.ExtendedMetadata{
		Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
		AgentSkill: &apiservice.AgentSkill{Id: "Production:external@1.0.0"},
	})
	current.Revision = wrapperspb.String("rev-4-external")

	record, err := client.MountAgentSkills("Production", "customer-agent",
		[]*model.AgentSkill{{ID: "Production:second@1.0.0"}}, false)
	require.NoError(t, err)
	var ids []string
	for _, entry := range record.Service.GetExtendedMetadata() {
		ids = append(ids, entry.GetAgentSkill().GetId())
	}
	assert.ElementsMatch(t, []string{
		"Production:old@1.0.0",
		"Production:first@1.0.0",
		"Production:external@1.0.0",
		"Production:second@1.0.0",
	}, ids)
	assert.Equal(t, 2, putCount)
}

func TestDeleteAgentServiceAcceptsExternalRevisionAfterConfirmedWrite(t *testing.T) {
	current := &apiservice.Service{
		Namespace:   wrapperspb.String("Production"),
		Name:        wrapperspb.String("search-agent"),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		Revision:    wrapperspb.String("rev-1"),
		Metadata:    map[string]string{"business": "old"},
	}
	deleted := false
	deleteRevision := ""
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			response := &apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			}
			if !deleted {
				response.Services = []*apiservice.Service{proto.Clone(current).(*apiservice.Service)}
			}
			writeProtoJSON(t, writer, response)
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var payloads []serviceWritePayload
			require.NoError(t, json.Unmarshal(body, &payloads))
			require.Len(t, payloads, 1)
			if payloads[0].Metadata != nil {
				current.Metadata = cloneMap(*payloads[0].Metadata)
			}
			current.Revision = wrapperspb.String("rev-2-sdk")
			writeProtoJSON(t, writer, successfulWriteResponse())
		case http.MethodPost:
			require.Equal(t, serviceDeleteRequestPath, request.URL.Path)
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var payloads []serviceDeletePayload
			require.NoError(t, json.Unmarshal(body, &payloads))
			require.Len(t, payloads, 1)
			deleteRevision = payloads[0].Revision
			deleted = true
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	_, err := client.EnsureAgentService(&agentmodel.EnsureAgentServiceRequest{
		Namespace: "Production", Service: "search-agent",
		Metadata: map[string]string{"business": "sdk-confirmed"},
	})
	require.NoError(t, err)

	current.Metadata = map[string]string{"business": "console-update"}
	current.Revision = wrapperspb.String("rev-3-external")
	require.NoError(t, client.DeleteAgentService(&agentmodel.DeleteAgentServiceRequest{
		Namespace: "Production", Service: "search-agent", Force: true,
	}))
	assert.Equal(t, "rev-3-external", deleteRevision)
}

func TestDeleteAgentServiceGuardsAndIsIdempotent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		deleted := false
		deleteBody := ""
		client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet {
				if deleted {
					writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
						Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					})
					return
				}
				writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Services: []*apiservice.Service{{
						Namespace:   wrapperspb.String("Production"),
						Name:        wrapperspb.String("search-agent"),
						ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
						Revision:    wrapperspb.String("rev-1"),
						Owners:      wrapperspb.String("team-a,team-b"),
					}},
				})
				return
			}
			require.Equal(t, serviceDeleteRequestPath, request.URL.Path)
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			deleteBody = string(body)
			deleted = true
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Responses: []*apiservice.Response{{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				}},
			})
		}))
		err := client.DeleteAgentService(&agentmodel.DeleteAgentServiceRequest{
			Namespace: "Production", Service: "search-agent", Revision: "rev-1", Owner: "team-b",
		})
		require.NoError(t, err)
		assert.Contains(t, deleteBody, `"name":"search-agent"`)
		assert.Contains(t, deleteBody, `"revision":"rev-1"`)
	})

	t.Run("not_found_is_idempotent", func(t *testing.T) {
		posts := 0
		client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			posts++
			writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_NotFoundResource)),
			})
		}))
		require.NoError(t, client.DeleteAgentService(&agentmodel.DeleteAgentServiceRequest{
			Namespace: "Production", Service: "search-agent",
		}))
		assert.Equal(t, 1, posts)
	})

	t.Run("preconditions", func(t *testing.T) {
		posts := 0
		client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet {
				writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Services: []*apiservice.Service{{
						Namespace:          wrapperspb.String("Production"),
						Name:               wrapperspb.String("search-agent"),
						ServiceType:        apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
						Revision:           wrapperspb.String("rev-1"),
						Owners:             wrapperspb.String("team-a"),
						TotalInstanceCount: wrapperspb.UInt32(2),
					}},
				})
				return
			}
			posts++
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			})
		}))
		for _, request := range []*agentmodel.DeleteAgentServiceRequest{
			{Namespace: "Production", Service: "search-agent", Revision: "rev-2"},
			{Namespace: "Production", Service: "search-agent", Owner: "team-z"},
			{Namespace: "Production", Service: "search-agent"},
		} {
			err := client.DeleteAgentService(request)
			require.Error(t, err)
		}
		assert.Zero(t, posts)
	})

	t.Run("type_conflict", func(t *testing.T) {
		posts := 0
		client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet {
				writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Services: []*apiservice.Service{{
						Namespace:   wrapperspb.String("Production"),
						Name:        wrapperspb.String("ordinary"),
						ServiceType: apiservice.ServiceType_SERVICE_TYPE_MICROSERVICE,
					}},
				})
				return
			}
			posts++
		}))
		err := client.DeleteAgentService(&agentmodel.DeleteAgentServiceRequest{
			Namespace: "Production", Service: "ordinary",
		})
		require.Error(t, err)
		assert.Equal(t, model.ErrCodeServiceTypeConflict, model.GetErrorCodeFromError(err))
		assert.Zero(t, posts)
	})

	t.Run("server_error", func(t *testing.T) {
		client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet {
				writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Services: []*apiservice.Service{{
						Namespace:   wrapperspb.String("Production"),
						Name:        wrapperspb.String("search-agent"),
						ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
						Revision:    wrapperspb.String("rev-1"),
					}},
				})
				return
			}
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteException)),
				Info: wrapperspb.String("delete failed"),
			})
		}))
		err := client.DeleteAgentService(&agentmodel.DeleteAgentServiceRequest{
			Namespace: "Production", Service: "search-agent",
		})
		require.Error(t, err)
		assert.Equal(t, model.ErrCodeServiceWriteError, model.GetErrorCodeFromError(err))
	})

	t.Run("force_attempts_non_empty_delete", func(t *testing.T) {
		deleted := false
		posts := 0
		client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet {
				if deleted {
					writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
						Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					})
					return
				}
				writeProtoJSON(t, writer, &apiservice.BatchQueryResponse{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
					Services: []*apiservice.Service{{
						Namespace:          wrapperspb.String("Production"),
						Name:               wrapperspb.String("search-agent"),
						ServiceType:        apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
						Revision:           wrapperspb.String("rev-1"),
						TotalInstanceCount: wrapperspb.UInt32(1),
					}},
				})
				return
			}
			posts++
			deleted = true
			writeProtoJSON(t, writer, &apiservice.BatchWriteResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				Responses: []*apiservice.Response{{
					Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
				}},
			})
		}))
		require.NoError(t, client.DeleteAgentService(&agentmodel.DeleteAgentServiceRequest{
			Namespace: "Production", Service: "search-agent", Force: true,
		}))
		assert.Equal(t, 1, posts)
	})
}

type roundTripperFunc func(request *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newTestClient(handler http.Handler) *Client {
	return &Client{
		addresses: []string{"http://console.test"},
		timeout:   time.Second,
		http: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			return recorder.Result(), nil
		})},
	}
}

func writeProtoJSON(t *testing.T, writer http.ResponseWriter, message proto.Message) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	raw, err := protojson.Marshal(message)
	require.NoError(t, err)
	_, err = writer.Write(raw)
	require.NoError(t, err)
}
