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
	"github.com/polarismesh/polaris-go/pkg/model"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceAdminRequestsValidateTypeAndPresence(t *testing.T) {
	require.NoError(t, (&GetServiceRequest{Namespace: "Production", Service: "agent"}).Validate())
	require.Error(t, (&GetServiceRequest{Namespace: " ", Service: "agent"}).Validate())

	write := &ServiceWriteRequest{
		Namespace: "Production",
		Service:   "agent",
		Type:      model.ServiceTypeAIAgent,
	}
	require.NoError(t, write.Validate())

	invalidType := *write
	invalidType.Type = model.ServiceType(99)
	require.Error(t, invalidType.Validate())

	nilMap := map[string]string(nil)
	withNilMap := *write
	withNilMap.Metadata = &nilMap
	require.Error(t, withNilMap.Validate())

	nilExtended := []json.RawMessage(nil)
	withNilExtended := *write
	withNilExtended.ExtendedMetadata = &nilExtended
	require.Error(t, withNilExtended.Validate())

	invalidJSON := []json.RawMessage{json.RawMessage(`{`)}
	withInvalidJSON := *write
	withInvalidJSON.ExtendedMetadata = &invalidJSON
	require.Error(t, withInvalidJSON.Validate())

	emptyExtended := []json.RawMessage{}
	withEmptyExtended := *write
	withEmptyExtended.ExtendedMetadata = &emptyExtended
	require.NoError(t, withEmptyExtended.Validate())
}

func TestDeleteServiceRequestGuards(t *testing.T) {
	serviceType := model.ServiceTypeAIAgent
	req := &DeleteServiceRequest{
		Namespace: "Production",
		Service:   "agent",
		Type:      &serviceType,
		Revision:  "rev-1",
		Owner:     "team",
		Force:     true,
	}
	require.NoError(t, req.Validate())

	req.Revision = " rev-1 "
	require.Error(t, req.Validate())

	unknownType := model.ServiceType(99)
	req.Revision = "rev-1"
	req.Type = &unknownType
	require.Error(t, req.Validate())
}
