/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package mcp adapts tRPC MCP servers and clients to Polaris metadata,
// Service Contract, and governance APIs.
package mcp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

const (
	// DefaultSSEPath is the default MCP SSE endpoint.
	DefaultSSEPath = model.MCPDefaultSSEPath
	// DefaultStreamableHTTPPath is the default streamable HTTP endpoint.
	DefaultStreamableHTTPPath = model.MCPDefaultStreamableHTTPPath

	// ContractReportFailFast aborts startup when a Service Contract report fails.
	ContractReportFailFast = "fail-fast"
	// ContractReportWarn logs a report failure and starts in best-effort mode.
	ContractReportWarn = "warn"
)

// Transport identifies an MCP transport.
type Transport string

const (
	// TransportSSE identifies the legacy HTTP+SSE transport.
	TransportSSE Transport = Transport(model.MCPTransportSSE)
	// TransportStreamableHTTP identifies streamable HTTP.
	TransportStreamableHTTP Transport = Transport(model.MCPTransportStreamableHTTP)
	// TransportStdio identifies local stdio.
	TransportStdio Transport = Transport(model.MCPTransportStdio)
)

// Config configures MCP metadata, contract, and client behavior.
type Config struct {
	Namespace            string
	Service              string
	ServerName           string
	ServerVersion        string
	ProtocolVersion      string
	Transport            Transport
	Scheme               string
	Host                 string
	Port                 int
	EndpointPath         string
	ContractReportPolicy string `json:"contractReportPolicy" yaml:"contractReportPolicy"`
	StrictVersion        bool
	Timeout              time.Duration
	ClientInfo           trpcmcp.Implementation
	ClientOptions        []trpcmcp.ClientOption
	// ServerCapabilities supplies initialization capabilities for an external
	// connector that does not implement ServerCapabilitiesProvider.
	ServerCapabilities *trpcmcp.ServerCapabilities
}

// DefaultConfig returns MCP adapter defaults.
func DefaultConfig() Config {
	return Config{
		Transport:            TransportStreamableHTTP,
		Scheme:               "http",
		ServerVersion:        "1.0.0",
		ContractReportPolicy: ContractReportFailFast,
		StrictVersion:        true,
		Timeout:              5 * time.Second,
		ClientInfo: trpcmcp.Implementation{
			Name:    "trpc-agent-naming-polarismesh",
			Version: "1.0.0",
		},
	}
}

// Validate validates the MCP configuration.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Namespace) == "" {
		return errors.New("MCP namespace is required")
	}
	if strings.TrimSpace(c.Service) == "" {
		return errors.New("MCP service is required")
	}
	if c.ServerName != "" && c.ServerName != c.Service {
		return fmt.Errorf("MCP server name %q must equal service %q", c.ServerName, c.Service)
	}
	if strings.TrimSpace(c.ServerVersion) == "" {
		return errors.New("MCP server version is required")
	}
	if !trpcmcp.IsProtocolVersionSupported(c.ProtocolVersion) {
		return fmt.Errorf("MCP protocol version %q is not supported", c.ProtocolVersion)
	}
	if !c.Transport.valid() {
		return fmt.Errorf("MCP transport %q is not supported", c.Transport)
	}
	if c.Timeout <= 0 {
		return errors.New("MCP timeout must be positive")
	}
	switch c.ContractReportPolicy {
	case "", ContractReportFailFast, ContractReportWarn:
	default:
		return fmt.Errorf("MCP contract report policy %q is not supported", c.ContractReportPolicy)
	}
	if c.Transport == TransportStdio {
		if c.Host != "" || c.Port != 0 || c.EndpointPath != "" {
			return errors.New("mcp-stdio must not expose a network host, port, or path")
		}
		return nil
	}
	if c.Scheme != "http" && c.Scheme != "https" {
		return fmt.Errorf("MCP scheme %q must be http or https", c.Scheme)
	}
	if c.Host == "" {
		return errors.New("MCP host is required for network transports")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("MCP port %d must be in 1..65535", c.Port)
	}
	if net.ParseIP(strings.Trim(c.Host, "[]")) == nil {
		return fmt.Errorf("MCP host %q must be an IP address", c.Host)
	}
	path := c.effectiveEndpointPath()
	if err := validateEndpointPath(path); err != nil {
		return err
	}
	return nil
}

// ValidateClient validates fields required to create a discovered MCP client.
// Host and port are supplied by each discovered instance.
func (c Config) ValidateClient() error {
	if strings.TrimSpace(c.Namespace) == "" {
		return errors.New("MCP namespace is required")
	}
	if strings.TrimSpace(c.Service) == "" {
		return errors.New("MCP service is required")
	}
	if !trpcmcp.IsProtocolVersionSupported(c.ProtocolVersion) {
		return fmt.Errorf("MCP protocol version %q is not supported", c.ProtocolVersion)
	}
	if c.Transport == TransportStdio || !c.Transport.valid() {
		return fmt.Errorf("MCP client transport %q is not a network transport", c.Transport)
	}
	if c.Timeout <= 0 {
		return errors.New("MCP timeout must be positive")
	}
	if c.Scheme != "http" && c.Scheme != "https" {
		return fmt.Errorf("MCP scheme %q must be http or https", c.Scheme)
	}
	return validateEndpointPath(c.effectiveEndpointPath())
}

// Endpoint returns the MCP network endpoint. Stdio returns an empty string.
func (c Config) Endpoint() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if c.Transport == TransportStdio {
		return "", nil
	}
	return (&url.URL{
		Scheme: c.Scheme,
		Host:   net.JoinHostPort(canonicalHost(c.Host), fmt.Sprintf("%d", c.Port)),
		Path:   c.effectiveEndpointPath(),
	}).String(), nil
}

// BuildServerMetadata builds standard MCP instance metadata.
func (c Config) BuildServerMetadata() (map[string]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return model.BuildMCPServerMetadata(&model.MCPServerMetadataRequest{
		ServerName:      c.Service,
		ProtocolVersion: c.ProtocolVersion,
		Transport:       model.MCPTransport(c.Transport),
		EndpointPath:    c.effectiveEndpointPath(),
	})
}

// ValidateServer verifies server metadata.
func (c Config) ValidateServer(metadata map[string]string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	for key := range metadata {
		if key == agentcard.MetadataAgentName || key == agentcard.MetadataAgentCard ||
			key == agentcard.MetadataA2AVersion || strings.HasPrefix(key, "a2a.") {
			return fmt.Errorf("MCP metadata must not contain A2A reserved key %q", key)
		}
	}
	return model.ValidateMCPServerMetadata(&model.MCPServerValidationRequest{
		Namespace:       c.Namespace,
		Service:         c.Service,
		ServerName:      c.Service,
		ProtocolVersion: c.ProtocolVersion,
		Transport:       model.MCPTransport(c.Transport),
		EndpointPath:    c.effectiveEndpointPath(),
		Metadata:        metadata,
	})
}

func (c Config) effectiveEndpointPath() string {
	if c.Transport == TransportStdio {
		return ""
	}
	if path := strings.TrimSpace(c.EndpointPath); path != "" {
		return path
	}
	switch c.Transport {
	case TransportSSE:
		return DefaultSSEPath
	default:
		return DefaultStreamableHTTPPath
	}
}

func (t Transport) valid() bool {
	switch t {
	case TransportSSE, TransportStreamableHTTP, TransportStdio:
		return true
	default:
		return false
	}
}

func validateEndpointPath(value string) error {
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#") {
		return fmt.Errorf("MCP endpoint path %q must be a rooted path without query or fragment", value)
	}
	if strings.Contains(value, "..") || strings.Contains(value, `\`) {
		return fmt.Errorf("MCP endpoint path %q is invalid", value)
	}
	return nil
}
