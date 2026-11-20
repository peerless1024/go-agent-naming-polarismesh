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
	"strings"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"

	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/serviceadmin"
)

type ServiceLookupRequest struct{ serviceadmin.GetServiceRequest }
type ServiceEnsureRequest struct {
	serviceadmin.EnsureServiceRequest
}
type ServiceUpdateRequest struct {
	serviceadmin.UpdateServiceRequest
}
type ServiceDeleteRequest struct {
	serviceadmin.DeleteServiceRequest
}

type serviceStore interface {
	GetService(*ServiceLookupRequest) (*serviceadmin.ServiceRecord, error)
	EnsureService(*ServiceEnsureRequest) (*serviceadmin.ServiceRecord, error)
	UpdateService(*ServiceUpdateRequest) (*serviceadmin.ServiceRecord, error)
	DeleteService(*ServiceDeleteRequest) error
	SDKContext() api.SDKContext
	Destroy()
}

// Option configures the adapter-owned Agent service API.
type Option func(*backend)

// WithConsole enables optional service management using an explicit Console endpoint.
func WithConsole(addresses []string, token string, timeout time.Duration) Option {
	return func(b *backend) {
		cfg := &serviceadmin.ServiceWriterConfigImpl{
			Addresses: append([]string(nil), addresses...), Token: token,
		}
		if timeout != 0 {
			cfg.SetTimeout(timeout)
		}
		cfg.SetDefault()
		b.configErr = cfg.Verify()
		if b.configErr == nil && len(addresses) > 0 {
			b.console = serviceadmin.New(cfg)
		}
	}
}

type backend struct {
	context   api.SDKContext
	consumer  api.ConsumerAPI
	console   *serviceadmin.Client
	configErr error
	closed    uint32
}

func (b *backend) SDKContext() api.SDKContext { return b.context }

func (b *backend) Destroy() {
	if atomic.CompareAndSwapUint32(&b.closed, 0, 1) && b.console != nil {
		b.console.Close()
	}
}

func (b *backend) checkAvailable() error {
	if atomic.LoadUint32(&b.closed) != 0 {
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "Agent service API is closed")
	}
	if b.context == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "SDKContext is required")
	}
	if b.context.IsDestroyed() {
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "SDKContext is destroyed")
	}
	if b.configErr != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, b.configErr, "invalid Console configuration")
	}
	return nil
}

func (b *backend) requireConsole() error {
	if err := b.checkAvailable(); err != nil {
		return err
	}
	if b.console == nil {
		return model.NewSDKError(model.ErrCodeServiceWriteUnsupported, nil,
			"Agent service writes require the adapter WithConsole option")
	}
	return nil
}

func (b *backend) GetService(req *ServiceLookupRequest) (*serviceadmin.ServiceRecord, error) {
	if err := b.checkAvailable(); err != nil {
		return nil, err
	}
	if b.console != nil {
		record, err := b.console.GetServiceWithRequest(&req.GetServiceRequest)
		if err != nil {
			return nil, err
		}
		return record.ServiceRecord()
	}
	response, err := b.consumer.GetAllInstances(&api.GetAllInstancesRequest{
		GetAllInstancesRequest: model.GetAllInstancesRequest{
			FlowID: req.FlowID, Namespace: req.Namespace, Service: req.Service,
			Timeout: req.Timeout, RetryCount: req.RetryCount,
		},
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil, "Discover returned an empty response")
	}
	if response.Namespace != req.Namespace || response.Service != req.Service {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"Discover returned service %s/%s for requested service %s/%s",
			response.Namespace, response.Service, req.Namespace, req.Service)
	}
	if response.NotExists {
		return nil, model.NewSDKError(model.ErrCodeServiceNotFound, nil,
			"service %s/%s not found", req.Namespace, req.Service)
	}
	serviceType := response.ServiceType
	if serviceType == model.ServiceTypeMicroservice {
		for _, item := range response.ExtendedMetadata {
			if item != nil && item.Type == model.ExtendedMetadataSkill && item.AgentSkill != nil &&
				strings.TrimSpace(item.AgentSkill.ID) != "" {
				serviceType = model.ServiceTypeAIAgent
				break
			}
		}
		for _, instance := range response.Instances {
			if instance != nil && strings.TrimSpace(instance.GetMetadata()[model.MetadataKeyAIAgentName]) != "" {
				serviceType = model.ServiceTypeAIAgent
				break
			}
		}
	}
	extended := make([]json.RawMessage, 0, len(response.ExtendedMetadata))
	for _, item := range response.ExtendedMetadata {
		if item != nil && item.Type == model.ExtendedMetadataSkill && item.AgentSkill != nil {
			raw, err := encodeSkillExtendedMetadata(item.AgentSkill)
			if err != nil {
				return nil, err
			}
			extended = append(extended, raw)
		} else if item != nil {
			raw, err := json.Marshal(struct {
				Type model.ExtendedMetadataType `json:"type"`
			}{Type: item.Type})
			if err != nil {
				return nil, err
			}
			extended = append(extended, raw)
		}
	}
	return &serviceadmin.ServiceRecord{
		Namespace: response.Namespace, Name: response.Service, Type: serviceType,
		Metadata: cloneMap(response.Metadata), ExtendedMetadata: extended,
		Revision: response.ServiceRevision,
	}, nil
}

func (b *backend) EnsureService(req *ServiceEnsureRequest) (*serviceadmin.ServiceRecord, error) {
	if err := b.requireConsole(); err != nil {
		return nil, err
	}
	record, err := b.console.EnsureService(&req.EnsureServiceRequest)
	if err != nil {
		return nil, err
	}
	return record.ServiceRecord()
}

func (b *backend) UpdateService(req *ServiceUpdateRequest) (*serviceadmin.ServiceRecord, error) {
	if err := b.requireConsole(); err != nil {
		return nil, err
	}
	record, err := b.console.UpdateService(&req.UpdateServiceRequest)
	if err != nil {
		return nil, err
	}
	return record.ServiceRecord()
}

func (b *backend) DeleteService(req *ServiceDeleteRequest) error {
	if err := b.requireConsole(); err != nil {
		return err
	}
	return b.console.DeleteService(&req.DeleteServiceRequest)
}
