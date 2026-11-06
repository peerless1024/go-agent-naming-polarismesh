/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package agentcard builds and validates the A2A registration card.
package agentcard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/polarismesh/polaris-go/pkg/model"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/a2apath"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
)

const (
	// MetadataAgentName is the instance metadata key for the Agent name.
	MetadataAgentName = "ai-agent-name"
	// MetadataAgentCard is the instance metadata key for the serialized AgentCard.
	MetadataAgentCard = "a2a.agent.card"
	// MetadataTransport is the instance metadata key for the A2A transport.
	MetadataTransport = "a2a.transport"
	// MetadataPath is the instance metadata key for the A2A base path.
	MetadataPath = "a2a.path"

	defaultTransport = "JSONRPC"

	maxInstanceMetadataEntries = 64
	maxMetadataValueCharacters = 65535
)

// AgentInstanceMetadata is the adapter's typed view of registration metadata.
type AgentInstanceMetadata struct {
	AgentName string
	CardJSON  string
	Transport string
	Path      string
}

// NewAgentCard builds an A2A v1 AgentCard from provider configuration.
func NewAgentCard(config adapterconfig.Config, tools ...tool.Tool) (a2aprotocolserver.AgentCard, error) {
	if err := config.ValidateProvider(); err != nil {
		return a2aprotocolserver.AgentCard{}, err
	}

	scheme := strings.ToLower(strings.TrimSpace(config.Scheme))
	if scheme == "" {
		scheme = "http"
	}
	endpoint := (&url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(config.AdvertiseHost, fmt.Sprintf("%d", config.AdvertisePort)),
		Path:   config.A2APath,
	}).String()

	card, err := a2aserver.NewAgentCard(
		config.ServiceName,
		config.Description,
		config.AgentVersion,
		endpoint,
		config.Streaming,
		a2aserver.WithCardTools(tools...),
	)
	if err != nil {
		return a2aprotocolserver.AgentCard{}, adaptererrors.WrapError("build AgentCard", err)
	}
	if err := ValidateAgentCard(config, &card); err != nil {
		return a2aprotocolserver.AgentCard{}, err
	}
	return card, nil
}

// ValidateAgentCard verifies the card's required fields and endpoint.
func ValidateAgentCard(config adapterconfig.Config, card *a2aprotocolserver.AgentCard) error {
	if card == nil {
		return errors.New("AgentCard is required")
	}
	if strings.TrimSpace(card.Name) == "" {
		return errors.New("AgentCard name is required")
	}
	if card.Name != config.ServiceName {
		return fmt.Errorf("AgentCard name %q must match service name %q", card.Name, config.ServiceName)
	}
	if strings.TrimSpace(card.Version) == "" {
		return errors.New("AgentCard version is required")
	}
	if config.AgentVersion != "" && card.Version != config.AgentVersion {
		return fmt.Errorf(
			"AgentCard version %q must match configured agent version %q",
			card.Version,
			config.AgentVersion,
		)
	}

	normalized := *card
	normalized.NormalizeInterfaces()
	if len(normalized.SupportedInterfaces) == 0 && normalized.URL == "" {
		return errors.New("AgentCard must define supportedInterfaces or url")
	}
	primaryURL := normalized.PrimaryURL()
	parsed, err := parseAgentCardEndpoint(primaryURL)
	if err != nil {
		return err
	}
	expectedScheme := strings.ToLower(strings.TrimSpace(config.Scheme))
	if expectedScheme == "" {
		expectedScheme = "http"
	}
	if !strings.EqualFold(parsed.Scheme, expectedScheme) {
		return fmt.Errorf(
			"AgentCard endpoint scheme %q must match configured scheme %q",
			parsed.Scheme,
			expectedScheme,
		)
	}
	if config.AdvertiseHost != "" && !strings.EqualFold(parsed.Hostname(), config.AdvertiseHost) {
		return fmt.Errorf(
			"AgentCard endpoint host %q must match advertised host %q",
			parsed.Hostname(),
			config.AdvertiseHost,
		)
	}
	if config.AdvertisePort != 0 {
		port := parsed.Port()
		if port == "" {
			switch parsed.Scheme {
			case "http":
				port = "80"
			case "https":
				port = "443"
			}
		}
		if port != fmt.Sprintf("%d", config.AdvertisePort) {
			return fmt.Errorf(
				"AgentCard endpoint port %q must match advertised port %d",
				port,
				config.AdvertisePort,
			)
		}
	}
	if normalizeA2APath(parsed.Path) != normalizeA2APath(config.A2APath) {
		return fmt.Errorf(
			"AgentCard endpoint path %q must match configured path %q",
			parsed.Path,
			config.A2APath,
		)
	}

	binding := primaryBinding(&normalized)
	if binding == "" {
		return errors.New("AgentCard primary interface transport is required")
	}
	if !strings.EqualFold(binding, defaultTransport) {
		return fmt.Errorf("AgentCard transport %q is not supported", binding)
	}
	if len(normalized.SupportedInterfaces) > 0 &&
		normalizeA2APath(mustParsePath(normalized.SupportedInterfaces[0].URL)) != normalizeA2APath(config.A2APath) {
		return fmt.Errorf(
			"AgentCard primary interface path must match configured path %q",
			config.A2APath,
		)
	}

	raw, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal AgentCard: %w", err)
	}
	if utf8.RuneCount(raw) > maxMetadataValueCharacters {
		return fmt.Errorf(
			"AgentCard JSON contains %d characters, maximum is %d",
			utf8.RuneCount(raw),
			maxMetadataValueCharacters,
		)
	}
	return nil
}

// BuildAgentInstanceMetadata serializes a validated AgentCard into metadata.
func BuildAgentInstanceMetadata(
	config adapterconfig.Config,
	card a2aprotocolserver.AgentCard,
) (map[string]string, error) {
	if err := ValidateAgentCard(config, &card); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return nil, adaptererrors.WrapError("marshal AgentCard", err)
	}
	metadata := map[string]string{
		MetadataAgentName: config.ServiceName,
		MetadataAgentCard: string(raw),
		MetadataTransport: strings.ToUpper(primaryBinding(&card)),
		MetadataPath:      config.A2APath,
	}
	if err := ValidateAgentInstanceMetadata(metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

// ValidateAgentInstanceMetadata validates the frozen four-key metadata contract.
func ValidateAgentInstanceMetadata(metadata map[string]string) error {
	if len(metadata) > maxInstanceMetadataEntries {
		return fmt.Errorf(
			"instance metadata has %d entries, maximum is %d",
			len(metadata),
			maxInstanceMetadataEntries,
		)
	}
	for key, value := range metadata {
		if strings.TrimSpace(key) == "" {
			return errors.New("instance metadata key must not be empty")
		}
		if isSensitiveMetadataKey(key) {
			return fmt.Errorf("instance metadata key %q may contain a credential", key)
		}
		if utf8.RuneCountInString(value) > maxMetadataValueCharacters {
			return fmt.Errorf(
				"instance metadata value for key %q contains %d characters, maximum is %d",
				key,
				utf8.RuneCountInString(value),
				maxMetadataValueCharacters,
			)
		}
	}

	agentName := strings.TrimSpace(metadata[MetadataAgentName])
	if agentName == "" {
		return fmt.Errorf("instance metadata %q is required", MetadataAgentName)
	}
	cardJSON := metadata[MetadataAgentCard]
	if strings.TrimSpace(cardJSON) == "" {
		return fmt.Errorf("instance metadata %q is required", MetadataAgentCard)
	}
	transport := strings.ToUpper(strings.TrimSpace(metadata[MetadataTransport]))
	if transport == "" {
		return fmt.Errorf("instance metadata %q is required", MetadataTransport)
	}
	if transport != defaultTransport {
		return fmt.Errorf("instance metadata transport %q is not supported", transport)
	}
	configuredPath := strings.TrimSpace(metadata[MetadataPath])
	if err := a2apath.Validate(configuredPath); err != nil {
		return fmt.Errorf("invalid instance metadata %q: %w", MetadataPath, err)
	}

	var card a2aprotocolserver.AgentCard
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		return fmt.Errorf("decode AgentCard metadata: %w", err)
	}
	card.NormalizeInterfaces()
	if card.Name != agentName {
		return fmt.Errorf("AgentCard name %q must match %q metadata %q", card.Name, MetadataAgentName, agentName)
	}
	if strings.TrimSpace(card.Version) == "" {
		return errors.New("AgentCard version is required")
	}
	if len(card.SupportedInterfaces) == 0 && card.URL == "" {
		return errors.New("AgentCard must define supportedInterfaces or url")
	}
	primaryURL := card.PrimaryURL()
	parsed, err := parseAgentCardEndpoint(primaryURL)
	if err != nil {
		return err
	}
	if normalizeA2APath(parsed.Path) != normalizeA2APath(configuredPath) {
		return fmt.Errorf(
			"AgentCard endpoint path %q must match %q metadata %q",
			parsed.Path,
			MetadataPath,
			configuredPath,
		)
	}
	binding := primaryBinding(&card)
	if !strings.EqualFold(binding, transport) {
		return fmt.Errorf(
			"AgentCard transport %q must match %q metadata %q",
			binding,
			MetadataTransport,
			transport,
		)
	}
	return model.ValidateAgentInstanceMetadata(metadata)
}

// ParseAgentInstanceMetadata decodes and validates AgentCard metadata.
func ParseAgentInstanceMetadata(
	metadata map[string]string,
) (AgentInstanceMetadata, a2aprotocolserver.AgentCard, error) {
	if err := ValidateAgentInstanceMetadata(metadata); err != nil {
		return AgentInstanceMetadata{}, a2aprotocolserver.AgentCard{}, err
	}
	var card a2aprotocolserver.AgentCard
	if err := json.Unmarshal([]byte(metadata[MetadataAgentCard]), &card); err != nil {
		return AgentInstanceMetadata{}, a2aprotocolserver.AgentCard{}, err
	}
	card.NormalizeInterfaces()
	parsed := AgentInstanceMetadata{
		AgentName: metadata[MetadataAgentName],
		CardJSON:  metadata[MetadataAgentCard],
		Transport: metadata[MetadataTransport],
		Path:      metadata[MetadataPath],
	}
	return parsed, card, nil
}

// CloneAgentCard returns an independent copy of card, including nested fields.
func CloneAgentCard(card a2aprotocolserver.AgentCard) (a2aprotocolserver.AgentCard, error) {
	raw, err := json.Marshal(card)
	if err != nil {
		return a2aprotocolserver.AgentCard{}, err
	}
	var copied a2aprotocolserver.AgentCard
	if err := json.Unmarshal(raw, &copied); err != nil {
		return a2aprotocolserver.AgentCard{}, err
	}
	return copied, nil
}

func primaryBinding(card *a2aprotocolserver.AgentCard) string {
	if card == nil {
		return ""
	}
	if len(card.SupportedInterfaces) > 0 {
		return card.SupportedInterfaces[0].ProtocolBinding
	}
	if card.PreferredTransport != nil {
		return *card.PreferredTransport
	}
	return ""
}

func normalizeA2APath(value string) string {
	if value == "" {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func mustParsePath(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Path
}

func parseAgentCardEndpoint(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("AgentCard endpoint is invalid")
	}
	if parsed.Scheme == "" || parsed.Hostname() == "" {
		return nil, errors.New("AgentCard endpoint must include scheme and host")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf(
			"AgentCard endpoint scheme %q is not supported; only http and https are allowed",
			parsed.Scheme,
		)
	}
	parsed.Scheme = scheme
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("AgentCard endpoint must not include credentials, query, or fragment")
	}
	if parsed.RawPath != "" {
		return nil, errors.New("AgentCard endpoint must not contain noncanonical path encoding")
	}
	if err := a2apath.Validate(parsed.Path); err != nil {
		return nil, err
	}
	return parsed, nil
}

func isSensitiveMetadataKey(key string) bool {
	lower := strings.ToLower(key)
	for _, fragment := range []string{"token", "secret", "password", "authorization"} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}
