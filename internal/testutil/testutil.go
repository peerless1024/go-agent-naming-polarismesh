/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package testutil provides shared fakes for adapter package tests.
package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/api"
	polarisconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	modelpb "github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	polarissdk "github.com/polarismesh/polaris-go/pkg/sdk"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
)

type FakeSDKContext struct {
	mu           sync.Mutex
	destroyCalls int
}

func (f *FakeSDKContext) Destroy() {
	f.mu.Lock()
	f.destroyCalls++
	f.mu.Unlock()
}

func (f *FakeSDKContext) IsDestroyed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.destroyCalls > 0
}

func (f *FakeSDKContext) GetConfig() polarisconfig.Configuration { return nil }
func (f *FakeSDKContext) GetPlugins() plugin.Manager             { return nil }
func (f *FakeSDKContext) GetEngine() polarissdk.Engine           { return nil }
func (f *FakeSDKContext) GetValueContext() polarissdk.ValueContext {
	return nil
}

func (f *FakeSDKContext) DestroyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.destroyCalls
}

type FakeSkillAPI struct {
	api.SkillAPI
	mu           sync.Mutex
	Requests     []*api.SkillInstallRequest
	ListRequests []*api.GetSkillListRequest
	ListResults  []*api.SkillListResult
	ListErr      error
	Err          error
	FailNames    map[string]error
	CacheHit     bool
	Downloaded   time.Time
	Path         string
}

func (f *FakeSkillAPI) GetSkillListResult(
	request *api.GetSkillListRequest,
) (*api.SkillListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := *request
	f.ListRequests = append(f.ListRequests, &copied)
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	index := len(f.ListRequests) - 1
	if index >= len(f.ListResults) {
		return &api.SkillListResult{}, nil
	}
	return f.ListResults[index], nil
}

func (f *FakeSkillAPI) Install(
	request *api.SkillInstallRequest,
) (*model.InstalledSkill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := *request
	f.Requests = append(f.Requests, &copied)
	if err := f.FailNames[request.Name]; err != nil {
		return nil, err
	}
	if f.Err != nil {
		return nil, f.Err
	}

	path := f.Path
	if path == "" {
		path = filepath.Join(request.CacheDir, request.Namespace, request.Name+"@"+request.Version)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	content := []byte("---\nname: " + request.Name + "\n---\n# " + request.Name + "\n")
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), content, 0o644); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	return &model.InstalledSkill{
		Namespace:  request.Namespace,
		Name:       request.Name,
		Version:    request.Version,
		Dir:        path,
		Digest:     hex.EncodeToString(digest[:]),
		EntryFile:  "SKILL.md",
		CacheHit:   f.CacheHit,
		Downloaded: f.Downloaded,
	}, nil
}

type FakeInstanceLister struct {
	mu        sync.Mutex
	Responses []*model.InstancesResponse
	Errors    []error
	Calls     int
}

func (f *FakeInstanceLister) GetInstances(
	_ *api.GetInstancesRequest,
) (*model.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := f.Calls
	f.Calls++
	if index < len(f.Errors) && f.Errors[index] != nil {
		return nil, f.Errors[index]
	}
	if index >= len(f.Responses) {
		if len(f.Responses) == 0 {
			return &model.InstancesResponse{}, nil
		}
		return f.Responses[len(f.Responses)-1], nil
	}
	return f.Responses[index], nil
}

func ProviderConfig() config.Config {
	cfg := config.DefaultConfig()
	cfg.Namespace = "Production"
	cfg.ServiceName = "customer-agent"
	cfg.AgentName = "customer-agent"
	cfg.AgentVersion = "1.2.3"
	cfg.Description = "Customer support agent"
	cfg.AdvertiseHost = "127.0.0.1"
	cfg.AdvertisePort = 18080
	cfg.A2APath = "/a2a"
	cfg.Transport = "JSONRPC"
	return cfg
}

func InstanceWithMetadata(
	id, host string,
	port uint32,
	healthy, isolated bool,
	metadata map[string]string,
) model.Instance {
	return InstanceWithProtocolMetadata(id, host, "", port, healthy, isolated, metadata)
}

// InstanceWithProtocolMetadata creates an instance with protocol and metadata.
func InstanceWithProtocolMetadata(
	id, host, protocol string,
	port uint32,
	healthy, isolated bool,
	metadata map[string]string,
) model.Instance {
	instance := &apiservice.Instance{
		Id:       wrapperspb.String(id),
		Host:     wrapperspb.String(host),
		Port:     wrapperspb.UInt32(port),
		Healthy:  wrapperspb.Bool(healthy),
		Isolate:  wrapperspb.Bool(isolated),
		Protocol: wrapperspb.String(protocol),
		Metadata: CloneStringMap(metadata),
	}
	return modelpb.NewInstanceInProto(
		instance,
		&model.ServiceKey{Namespace: "Production", Service: "customer-agent"},
		local.NewInstanceLocalValue(),
	)
}

func InstancesResponse(instances ...model.Instance) *model.InstancesResponse {
	return &model.InstancesResponse{Instances: instances}
}

// CloneStringMap copies a string map.
func CloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func AgentServiceWithSkills(refs ...config.SkillRef) *model.AgentService {
	extended := make([]*model.ExtendedMetadata, 0, len(refs))
	for _, ref := range refs {
		ref = ref.Normalize()
		extended = append(extended, &model.ExtendedMetadata{
			Type: model.ExtendedMetadataSkill,
			AgentSkill: &model.AgentSkill{
				ID:      ref.ID(),
				Name:    ref.Name,
				Version: ref.Version,
			},
		})
	}
	return &model.AgentService{
		Namespace:        "Production",
		Name:             "customer-agent",
		Type:             model.ServiceTypeAIAgent,
		ExtendedMetadata: extended,
	}
}
