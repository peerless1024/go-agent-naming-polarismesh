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
	"fmt"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// ServiceRecord is the protocol-neutral snapshot returned by the optional Console backend.
type ServiceRecord struct {
	Namespace          string
	Name               string
	Type               model.ServiceType
	Metadata           map[string]string
	ExtendedMetadata   []json.RawMessage
	Owners             string
	Revision           string
	TotalInstanceCount uint32
}

// GetServiceRequest identifies one service for the Service Admin API.
type GetServiceRequest struct {
	FlowID     uint64
	Namespace  string
	Service    string
	Timeout    *time.Duration
	RetryCount *int
}

// Validate checks a Service Admin get request.
func (r *GetServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "GetServiceRequest can not be nil")
	}
	if strings.TrimSpace(r.Namespace) == "" || strings.TrimSpace(r.Service) == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetServiceRequest namespace and service must not be empty")
	}
	if r.Timeout != nil && *r.Timeout <= 0 {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetServiceRequest timeout must be positive")
	}
	if r.RetryCount != nil && *r.RetryCount < 0 {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetServiceRequest retry count must not be negative")
	}
	return nil
}

// ServiceWriteRequest carries generic service metadata updates.
type ServiceWriteRequest struct {
	Namespace string
	Service   string
	Type      model.ServiceType
	// Metadata is nil to preserve, or non-nil to replace the complete map.
	Metadata *map[string]string
	// ExtendedMetadata is nil to preserve, or non-nil to replace the complete list.
	// An empty, non-nil slice explicitly clears all extended metadata.
	ExtendedMetadata *[]json.RawMessage
	// Revision is an optional optimistic concurrency precondition.
	Revision string
}

// EnsureServiceRequest asks the optional Console backend to create or update a service.
type EnsureServiceRequest struct {
	ServiceWriteRequest
}

// UpdateServiceRequest asks the optional Console backend to update an existing service.
type UpdateServiceRequest struct {
	ServiceWriteRequest
}

// DeleteServiceRequest asks the optional Console backend to delete an existing service.
type DeleteServiceRequest struct {
	Namespace string
	Service   string
	Type      *model.ServiceType
	Revision  string
	Owner     string
	// Force skips the SDK empty-service precheck; the server may still reject it.
	Force bool
}

// Validate checks a generic service write request.
func (r *ServiceWriteRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "ServiceWriteRequest can not be nil")
	}
	if strings.TrimSpace(r.Namespace) == "" || strings.TrimSpace(r.Service) == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"ServiceWriteRequest namespace and service must not be empty")
	}
	if !validServiceType(r.Type) {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"ServiceWriteRequest service type %d is not supported", r.Type)
	}
	if strings.TrimSpace(r.Revision) != r.Revision {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"ServiceWriteRequest revision must not contain surrounding whitespace")
	}
	if r.Metadata != nil && *r.Metadata == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"ServiceWriteRequest metadata must use an empty map, not a nil map, to clear")
	}
	if r.ExtendedMetadata != nil {
		if *r.ExtendedMetadata == nil {
			return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
				"ServiceWriteRequest extended metadata must use an empty slice, not nil, to clear")
		}
		for index, item := range *r.ExtendedMetadata {
			if len(item) == 0 || !json.Valid(item) {
				return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
					"ServiceWriteRequest extended metadata item %d is not valid JSON", index)
			}
		}
	}
	return nil
}

// Validate checks an EnsureServiceRequest.
func (r *EnsureServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "EnsureServiceRequest can not be nil")
	}
	return r.ServiceWriteRequest.Validate()
}

// Validate checks an UpdateServiceRequest.
func (r *UpdateServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "UpdateServiceRequest can not be nil")
	}
	return r.ServiceWriteRequest.Validate()
}

// Validate checks a generic service delete request.
func (r *DeleteServiceRequest) Validate() error {
	if r == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "DeleteServiceRequest can not be nil")
	}
	if strings.TrimSpace(r.Namespace) == "" || strings.TrimSpace(r.Service) == "" {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"DeleteServiceRequest namespace and service must not be empty")
	}
	if r.Type != nil && !validServiceType(*r.Type) {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"DeleteServiceRequest service type %d is not supported", *r.Type)
	}
	if strings.TrimSpace(r.Revision) != r.Revision || strings.TrimSpace(r.Owner) != r.Owner {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"DeleteServiceRequest revision and owner must not contain surrounding whitespace")
	}
	return nil
}

func validServiceType(value model.ServiceType) bool {
	switch value {
	case model.ServiceTypeMicroservice, model.ServiceTypeMCPServer, model.ServiceTypeAIAgent:
		return true
	default:
		return false
	}
}

// String returns a stable description for diagnostics.
func (r *ServiceRecord) String() string {
	if r == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s/%s type=%s revision=%s", r.Namespace, r.Name, r.Type, r.Revision)
}
