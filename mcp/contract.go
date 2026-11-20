/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package mcp

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-go/log"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

const (
	mcpContractType = "mcp"

	// Polaris stores interface methods in VARCHAR(32). MCP notification method
	// names can exceed that limit, so they are preserved in contract content.
	maxContractInterfaceMethodRunes = 32
)

// CollectContract reads Tools, Resources, and Prompts from an initialized MCP
// connector and converts them to a protocol-independent Service Contract.
func CollectContract(
	ctx context.Context,
	connector trpcmcp.Connector,
	config Config,
) (*model.ServiceContract, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if connector == nil {
		return nil, errors.New("MCP connector is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	capabilities, err := contractCapabilities(connector, config)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	features, err := collectFeatures(ctx, connector, capabilities)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	interfaces, notifications := contractInterfaces(config, capabilities)
	contract := &model.ServiceContract{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Name:      mcpContractType,
			Namespace: config.Namespace,
			Service:   config.Service,
			Protocol:  string(config.Transport),
			Version:   config.ServerVersion,
			Type:      mcpContractType,
		},
		ServiceFeatures: features,
		Interfaces:      interfaces,
		Metadata: map[string]string{
			model.MCPMetadataKeyServerName:      config.Service,
			model.MCPMetadataKeyProtocolVersion: config.ProtocolVersion,
			"mcp-server-version":                config.ServerVersion,
		},
	}
	encoded, err := json.Marshal(struct {
		Features      []*model.ServiceFeature      `json:"features"`
		Interfaces    []*model.InterfaceDescriptor `json:"interfaces"`
		Notifications []string                     `json:"notifications,omitempty"`
	}{Features: features, Interfaces: interfaces, Notifications: notifications})
	if err != nil {
		return nil, err
	}
	contract.Content = string(encoded)
	contract.ContentDigest = contentDigest(encoded)
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	return contract, nil
}

// ReportContract creates or updates the MCP Service Contract.
func ReportContract(
	ctx context.Context,
	provider api.ProviderAPI,
	contract *model.ServiceContract,
) (*model.ServiceContract, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, errors.New("polaris ProviderAPI is required")
	}
	if contract == nil {
		return nil, errors.New("MCP Service Contract is required")
	}
	if _, err := provider.ReportServiceContract(&api.ReportServiceContractRequest{
		ServiceContract: contract,
	}); err != nil {
		return nil, err
	}
	return model.CloneServiceContract(contract), nil
}

// ReportContractWithConfig applies the configured fail-fast or best-effort
// Service Contract reporting policy.
func ReportContractWithConfig(
	ctx context.Context,
	provider api.ProviderAPI,
	contract *model.ServiceContract,
	config Config,
) (*model.ServiceContract, error) {
	if contract == nil {
		return nil, errors.New("MCP Service Contract is required")
	}
	policy := config.ContractReportPolicy
	if policy == "" {
		policy = ContractReportFailFast
	}
	if policy != ContractReportFailFast && policy != ContractReportWarn {
		return nil, fmt.Errorf("MCP contract report policy %q is not supported", policy)
	}
	reported, err := ReportContract(ctx, provider, contract)
	if err == nil || policy == ContractReportFailFast {
		return reported, err
	}
	log.Warnf("Report MCP Service Contract %s/%s failed; continuing in best-effort mode: %v",
		contract.Namespace, contract.Service, err)
	fallback := model.CloneServiceContract(contract)
	fallback.ID = ""
	return fallback, nil
}

// GetContract queries one MCP Service Contract through ConsumerAPI.
func GetContract(
	ctx context.Context,
	consumer api.ConsumerAPI,
	req *api.GetServiceContractRequest,
) (*model.ServiceRuleResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if consumer == nil {
		return nil, errors.New("polaris ConsumerAPI is required")
	}
	if req == nil {
		return nil, errors.New("service contract query is required")
	}
	response, err := consumer.GetServiceContract(req)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("GetServiceContract returned an empty response")
	}
	return response, nil
}

// GetContractWithConfig builds an MCP Service Contract query from Config.
func GetContractWithConfig(
	ctx context.Context,
	consumer api.ConsumerAPI,
	config Config,
) (*model.ServiceRuleResponse, error) {
	if err := config.ValidateClient(); err != nil {
		return nil, err
	}
	req := &api.GetServiceContractRequest{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Namespace: config.Namespace,
			Service:   config.Service,
			Name:      mcpContractType,
			Type:      mcpContractType,
			Protocol:  string(config.Transport),
			Version:   config.ServerVersion,
		},
	}
	req.SetTimeout(config.Timeout)
	req.SetRetryCount(0)
	return GetContract(ctx, consumer, req)
}

func contractCapabilities(connector trpcmcp.Connector, config Config) (trpcmcp.ServerCapabilities, error) {
	if provider, ok := connector.(ServerCapabilitiesProvider); ok {
		if capabilities, initialized := provider.ServerCapabilities(); initialized {
			return capabilities, nil
		}
		return trpcmcp.ServerCapabilities{}, errors.New("MCP initialization capabilities are unavailable: connector is not initialized or is closed")
	}
	if config.ServerCapabilities != nil {
		return cloneServerCapabilities(*config.ServerCapabilities), nil
	}
	return trpcmcp.ServerCapabilities{}, errors.New("MCP initialization capabilities are unavailable; use an initialized adapter client or set Config.ServerCapabilities for an external connector")
}

func collectFeatures(
	ctx context.Context,
	connector trpcmcp.Connector,
	capabilities trpcmcp.ServerCapabilities,
) ([]*model.ServiceFeature, error) {
	var features []*model.ServiceFeature
	var toolsCursor trpcmcp.Cursor
	toolsCursors := make(map[trpcmcp.Cursor]struct{})
	for capabilities.Tools != nil {
		request := &trpcmcp.ListToolsRequest{}
		request.Params.Cursor = toolsCursor
		result, err := connector.ListTools(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		if result == nil {
			return nil, errors.New("list MCP tools returned an empty result")
		}
		for index := range result.Tools {
			content, err := json.Marshal(result.Tools[index])
			if err != nil {
				return nil, err
			}
			features = append(features, &model.ServiceFeature{
				ID:          "tool:" + result.Tools[index].Name,
				Type:        model.ServiceFeatureTypeMCPTool,
				Name:        result.Tools[index].Name,
				Description: result.Tools[index].Description,
				Content:     string(content),
				Status:      model.ServiceFeatureEnabled,
			})
			if len(features) > model.MaxServiceContractFeatures {
				return nil, fmt.Errorf("MCP server exposes more than %d contract features",
					model.MaxServiceContractFeatures)
			}
		}
		next, done, err := nextPageCursor("tools", toolsCursor, result.NextCursor, toolsCursors)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		toolsCursor = next
	}
	var resourcesCursor trpcmcp.Cursor
	resourcesCursors := make(map[trpcmcp.Cursor]struct{})
	for capabilities.Resources != nil {
		request := &trpcmcp.ListResourcesRequest{}
		request.Params.Cursor = resourcesCursor
		result, err := connector.ListResources(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("list MCP resources: %w", err)
		}
		if result == nil {
			return nil, errors.New("list MCP resources returned an empty result")
		}
		for index := range result.Resources {
			content, err := json.Marshal(result.Resources[index])
			if err != nil {
				return nil, err
			}
			features = append(features, &model.ServiceFeature{
				ID:          "resource:" + result.Resources[index].URI,
				Type:        model.ServiceFeatureTypeMCPResource,
				Name:        result.Resources[index].Name,
				Description: result.Resources[index].Description,
				Content:     string(content),
				Status:      model.ServiceFeatureEnabled,
			})
			if len(features) > model.MaxServiceContractFeatures {
				return nil, fmt.Errorf("MCP server exposes more than %d contract features",
					model.MaxServiceContractFeatures)
			}
		}
		next, done, err := nextPageCursor(
			"resources",
			resourcesCursor,
			result.NextCursor,
			resourcesCursors,
		)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		resourcesCursor = next
	}
	var promptsCursor trpcmcp.Cursor
	promptsCursors := make(map[trpcmcp.Cursor]struct{})
	for capabilities.Prompts != nil {
		request := &trpcmcp.ListPromptsRequest{}
		request.Params.Cursor = promptsCursor
		result, err := connector.ListPrompts(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("list MCP prompts: %w", err)
		}
		if result == nil {
			return nil, errors.New("list MCP prompts returned an empty result")
		}
		for index := range result.Prompts {
			content, err := json.Marshal(result.Prompts[index])
			if err != nil {
				return nil, err
			}
			features = append(features, &model.ServiceFeature{
				ID:          "prompt:" + result.Prompts[index].Name,
				Type:        model.ServiceFeatureTypeMCPPrompt,
				Name:        result.Prompts[index].Name,
				Description: result.Prompts[index].Description,
				Content:     string(content),
				Status:      model.ServiceFeatureEnabled,
			})
			if len(features) > model.MaxServiceContractFeatures {
				return nil, fmt.Errorf("MCP server exposes more than %d contract features",
					model.MaxServiceContractFeatures)
			}
		}
		next, done, err := nextPageCursor(
			"prompts",
			promptsCursor,
			result.NextCursor,
			promptsCursors,
		)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		promptsCursor = next
	}
	sort.Slice(features, func(i, j int) bool {
		if features[i].Type == features[j].Type {
			return features[i].ID < features[j].ID
		}
		return features[i].Type < features[j].Type
	})
	return features, nil
}

func nextPageCursor(
	kind string,
	current, next trpcmcp.Cursor,
	seen map[trpcmcp.Cursor]struct{},
) (trpcmcp.Cursor, bool, error) {
	if next == "" {
		return "", true, nil
	}
	if next == current {
		return "", false, fmt.Errorf("list MCP %s returned the current cursor repeatedly", kind)
	}
	if _, exists := seen[next]; exists {
		return "", false, fmt.Errorf("list MCP %s returned a cursor cycle", kind)
	}
	seen[next] = struct{}{}
	return next, false, nil
}

func contractInterfaces(config Config, capabilities trpcmcp.ServerCapabilities) ([]*model.InterfaceDescriptor, []string) {
	var methods []string
	if capabilities.Tools != nil {
		methods = append(methods, trpcmcp.MethodToolsList, trpcmcp.MethodToolsCall)
		if capabilities.Tools.ListChanged {
			methods = append(methods, "notifications/tools/list_changed")
		}
	}
	if capabilities.Resources != nil {
		methods = append(methods, trpcmcp.MethodResourcesList, trpcmcp.MethodResourcesRead, trpcmcp.MethodResourcesTemplatesList)
		if capabilities.Resources.ListChanged {
			methods = append(methods, "notifications/resources/list_changed")
		}
	}
	if capabilities.Prompts != nil {
		methods = append(methods, trpcmcp.MethodPromptsList, trpcmcp.MethodPromptsGet)
		if capabilities.Prompts.ListChanged {
			methods = append(methods, "notifications/prompts/list_changed")
		}
	}
	descriptors := make([]*model.InterfaceDescriptor, 0, len(methods))
	notifications := make([]string, 0, len(methods))
	for _, method := range methods {
		if utf8.RuneCountInString(method) > maxContractInterfaceMethodRunes {
			notifications = append(notifications, method)
			continue
		}
		descriptors = append(descriptors, &model.InterfaceDescriptor{
			Name:      method,
			Method:    method,
			Path:      config.effectiveEndpointPath(),
			Namespace: config.Namespace,
			Service:   config.Service,
			Protocol:  string(config.Transport),
			Version:   config.ServerVersion,
			Type:      mcpContractType,
			Source:    0,
		})
	}
	return descriptors, notifications
}

func contentDigest(content []byte) string {
	digest := sha1.Sum(content)
	return hex.EncodeToString(digest[:])
}
