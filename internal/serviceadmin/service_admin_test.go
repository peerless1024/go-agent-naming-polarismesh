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
	"encoding/json"
	"io"
	"net/http"
	"testing"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
)

func TestGenericServiceAdminEnsureUpdateDelete(t *testing.T) {
	var current *apiservice.Service
	deleted := false
	client := newTestClient(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			response := &apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
			}
			if current != nil && !deleted {
				response.Services = []*apiservice.Service{current}
			}
			writeProtoJSON(t, writer, response)
		case http.MethodPost:
			if request.URL.Path == serviceDeleteRequestPath {
				deleted = true
				writeProtoJSON(t, writer, successfulWriteResponse())
				return
			}
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var payloads []serviceWritePayload
			require.NoError(t, json.Unmarshal(body, &payloads))
			require.Len(t, payloads, 1)
			current = serviceFromWritePayload(t, payloads[0], "rev-created")
			writeProtoJSON(t, writer, successfulWriteResponse())
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var payloads []serviceWritePayload
			require.NoError(t, json.Unmarshal(body, &payloads))
			require.Len(t, payloads, 1)
			updated := serviceFromWritePayload(t, payloads[0], "rev-updated")
			if current != nil {
				if payloads[0].Metadata == nil {
					updated.Metadata = cloneMap(current.Metadata)
				}
				if payloads[0].ExtendedMetadata == nil {
					updated.ExtendedMetadata = append(
						[]*apiservice.ExtendedMetadata(nil), current.ExtendedMetadata...)
				}
			}
			current = updated
			writeProtoJSON(t, writer, successfulWriteResponse())
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
	}))

	metadata := map[string]string{"owner": "team"}
	extended := []json.RawMessage{json.RawMessage(
		`{"type":1,"agent_skill":{"id":"Production:demo@1.0.0","name":"demo","version":"1.0.0"}}`,
	)}
	created, err := client.EnsureService(&EnsureServiceRequest{
		ServiceWriteRequest: ServiceWriteRequest{
			Namespace:        "Production",
			Service:          "agent",
			Type:             model.ServiceTypeAIAgent,
			Metadata:         &metadata,
			ExtendedMetadata: &extended,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "rev-created", created.Service.GetRevision().GetValue())
	require.Equal(t, apiservice.ServiceType_SERVICE_TYPE_AI_AGENT, current.GetServiceType())

	updatedMetadata := map[string]string{"owner": "team", "generation": "2"}
	updated, err := client.UpdateService(&UpdateServiceRequest{
		ServiceWriteRequest: ServiceWriteRequest{
			Namespace: "Production",
			Service:   "agent",
			Type:      model.ServiceTypeAIAgent,
			Metadata:  &updatedMetadata,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "rev-updated", updated.Service.GetRevision().GetValue())
	require.Equal(t, "2", current.GetMetadata()["generation"])

	serviceType := model.ServiceTypeAIAgent
	require.NoError(t, client.DeleteService(&DeleteServiceRequest{
		Namespace: "Production",
		Service:   "agent",
		Type:      &serviceType,
		Force:     true,
	}))
	require.True(t, deleted)
}

func serviceFromWritePayload(
	t *testing.T, payload serviceWritePayload, revision string,
) *apiservice.Service {
	t.Helper()
	serviceType := apiservice.ServiceType_SERVICE_TYPE_MICROSERVICE
	if payload.ServiceType != "" {
		value, ok := apiservice.ServiceType_value[payload.ServiceType]
		require.True(t, ok)
		serviceType = apiservice.ServiceType(value)
	}
	service := &apiservice.Service{
		Namespace:   wrapperspb.String(payload.Namespace),
		Name:        wrapperspb.String(payload.Name),
		ServiceType: serviceType,
		Revision:    wrapperspb.String(revision),
		Metadata:    map[string]string{},
	}
	if payload.Metadata != nil {
		service.Metadata = cloneMap(*payload.Metadata)
	}
	if payload.ExtendedMetadata != nil {
		extendedJSON, err := json.Marshal(*payload.ExtendedMetadata)
		require.NoError(t, err)
		service.ExtendedMetadata, err = decodeKnownExtendedMetadata(
			map[string]json.RawMessage{"extended_metadata": extendedJSON})
		require.NoError(t, err)
	}
	return service
}
