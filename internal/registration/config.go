/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package registration validates AI-specific framework registration conflicts.
package registration

import (
	"fmt"
	"strings"

	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	polarisregistry "trpc.group/trpc-go/trpc-naming-polarismesh/registry"
)

// ValidateAgent validates framework configuration before an Agent provider is installed.
func ValidateAgent(namespace, service, version string) error {
	return ValidateAgentConfig(namespace, service, version, frameworkConfig(service))
}

// ValidateAgentConfig validates the exact registry configuration used by Register.
func ValidateAgentConfig(namespace, service, version string, cfg *polarisregistry.Config) error {
	if cfg == nil {
		return nil
	}
	if cfg.ServiceName != service {
		return fmt.Errorf("agent service %q must match framework service %q", service, cfg.ServiceName)
	}
	if err := validateIdentity(cfg, namespace, version); err != nil {
		return err
	}
	if len(cfg.Endpoints) != 0 {
		return fmt.Errorf("A2A AgentCard registration does not support endpoints; define interfaces in the AgentCard")
	}
	if cfg.BindAddress != "" || cfg.PreferBindAddress {
		return fmt.Errorf("A2A AgentCard registration does not support bind_address or prefer_bind_address")
	}
	if cfg.InstanceProtocol != "" {
		return fmt.Errorf("A2A instance_protocol must be empty")
	}
	if !cfg.EnableRegister {
		return fmt.Errorf("A2A metadata provider requires framework instance registration to be enabled")
	}
	if err := validateStaticMetadata(cfg); err != nil {
		return err
	}
	if len(cfg.Metadata) != 0 {
		return fmt.Errorf("A2A registration requires exactly three metadata keys; static metadata must be empty")
	}
	return nil
}

// ValidateMCP validates framework configuration before a network MCP provider is installed.
func ValidateMCP(namespace, service, version string) error {
	return ValidateMCPConfig(namespace, service, version, frameworkConfig(service))
}

// ValidateMCPConfig validates the exact registry configuration used by Register.
func ValidateMCPConfig(namespace, service, version string, cfg *polarisregistry.Config) error {
	if cfg == nil {
		return nil
	}
	if cfg.ServiceName != service {
		return fmt.Errorf("MCP service %q must match framework service %q", service, cfg.ServiceName)
	}
	if err := validateIdentity(cfg, namespace, version); err != nil {
		return err
	}
	if len(cfg.Endpoints) > 1 {
		return fmt.Errorf("MCP registration snapshot requires exactly one endpoint; prepare metadata for each transport separately")
	}
	if !cfg.EnableRegister {
		return fmt.Errorf("MCP metadata provider requires framework instance registration to be enabled")
	}
	return validateStaticMetadata(cfg)
}

func frameworkConfig(service string) *polarisregistry.Config {
	registry, ok := polarisregistry.FromFramework(service)
	if !ok {
		return nil
	}
	return registry.Config()
}

func validateIdentity(cfg *polarisregistry.Config, namespace, version string) error {
	if cfg.Namespace != namespace {
		return fmt.Errorf("registration namespace %q must match framework namespace %q", namespace, cfg.Namespace)
	}
	if cfg.Version != "" && cfg.Version != version {
		return fmt.Errorf("registration version %q must match framework instance version %q", version, cfg.Version)
	}
	return nil
}

func validateStaticMetadata(cfg *polarisregistry.Config) error {
	metadata := []map[string]string{cfg.Metadata}
	for _, endpoint := range cfg.Endpoints {
		metadata = append(metadata, endpoint.Metadata)
	}
	keys := []string{
		agentcard.MetadataAgentName, agentcard.MetadataAgentCard, agentcard.MetadataA2AVersion,
		model.MCPMetadataKeyServerName, model.MCPMetadataKeyProtocolVersion, model.MCPMetadataKeyEndpointPath,
	}
	for _, values := range metadata {
		for key := range values {
			if strings.HasPrefix(key, "a2a.") {
				return fmt.Errorf("static register metadata for service %q contains reserved agent key %q while a metadata provider is registered",
					cfg.ServiceName, key)
			}
		}
		for _, key := range keys {
			if _, exists := values[key]; exists {
				return fmt.Errorf("static register metadata for service %q contains reserved agent key %q while a metadata provider is registered",
					cfg.ServiceName, key)
			}
		}
	}
	return nil
}
