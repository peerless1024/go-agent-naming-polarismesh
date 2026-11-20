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
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/polarismesh/polaris-go/pkg/model"
	a2aprotocol "trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/a2apath"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
)

const (
	// MetadataAgentName is the instance metadata key for the Agent name.
	MetadataAgentName = model.MetadataKeyAIAgentName
	// MetadataAgentCard is the instance metadata key for the serialized AgentCard.
	MetadataAgentCard = model.MetadataKeyAIAgentCard
	// MetadataA2AVersion is the AgentCard schema version metadata key.
	MetadataA2AVersion = "ai-a2a-version"

	maxInstanceMetadataEntries = model.MaxInstanceMetadataEntries
	maxMetadataValueCharacters = model.MaxMetadataValueRunes
)

// SchemaVersion is an A2A AgentCard schema version.
type SchemaVersion string

const (
	// SchemaVersionV03 identifies the A2A v0.3 card schema.
	SchemaVersionV03 SchemaVersion = adapterconfig.A2AVersionV03
	// SchemaVersionV10 identifies the A2A v1.0 card schema.
	SchemaVersionV10 SchemaVersion = adapterconfig.A2AVersionV10
)

// AgentInstanceMetadata is the adapter's typed view of registration metadata.
type AgentInstanceMetadata struct {
	AgentName  string
	CardJSON   string
	A2AVersion SchemaVersion
	Card       a2aprotocolserver.AgentCard
}

// CardDocument keeps a typed AgentCard and its source JSON. Keeping Raw is
// required for unknown-field and signature preservation.
type CardDocument struct {
	Version SchemaVersion
	Card    a2aprotocolserver.AgentCard
	raw     json.RawMessage
}

// NewCardDocument returns a document backed by the canonical JSON of card.
func NewCardDocument(version SchemaVersion, card a2aprotocolserver.AgentCard) (CardDocument, error) {
	card = canonicalCard(version, card)
	raw, err := marshalCanonicalCard(version, card)
	if err != nil {
		return CardDocument{}, fmt.Errorf("marshal AgentCard: %w", err)
	}
	return CardDocument{Version: version, Card: card, raw: append(json.RawMessage(nil), raw...)}, nil
}

// ParseCardDocument parses raw using exactly one AgentCard schema.
func ParseCardDocument(
	config adapterconfig.Config,
	version SchemaVersion,
	raw []byte,
) (CardDocument, error) {
	if err := validateSchemaShape(version, raw); err != nil {
		return CardDocument{}, err
	}
	var card a2aprotocolserver.AgentCard
	if err := json.Unmarshal(raw, &card); err != nil {
		return CardDocument{}, fmt.Errorf("decode AgentCard: %w", err)
	}
	if err := ValidateAgentCard(config, &card, version); err != nil {
		return CardDocument{}, err
	}
	return CardDocument{
		Version: version,
		Card:    card,
		raw:     append(json.RawMessage(nil), raw...),
	}, nil
}

// Raw returns a deep copy of the original JSON.
func (d CardDocument) Raw() json.RawMessage {
	return append(json.RawMessage(nil), d.raw...)
}

// Metadata builds the three-key instance metadata while preserving Raw.
func (d CardDocument) Metadata(config adapterconfig.Config) (map[string]string, error) {
	if len(d.raw) == 0 {
		return nil, errors.New("AgentCard document has no raw JSON")
	}
	return BuildAgentInstanceMetadataFromRaw(config, d.Version, d.raw)
}

// NewAgentCard builds an A2A v1 AgentCard from provider configuration.
func NewAgentCard(config adapterconfig.Config, tools ...tool.Tool) (a2aprotocolserver.AgentCard, error) {
	if err := config.ValidateProvider(); err != nil {
		return a2aprotocolserver.AgentCard{}, err
	}
	if SchemaVersion(config.A2AVersion) != SchemaVersionV10 {
		return a2aprotocolserver.AgentCard{}, errors.New("generated AgentCards require schema version 1.0")
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
	card.NormalizeInterfaces()
	if err := ValidateAgentCard(config, &card, SchemaVersion(config.A2AVersion)); err != nil {
		return a2aprotocolserver.AgentCard{}, err
	}
	return card, nil
}

// ValidateAgentCard verifies the complete card contract.
func ValidateAgentCard(
	config adapterconfig.Config,
	card *a2aprotocolserver.AgentCard,
	versions ...SchemaVersion,
) error {
	if card == nil {
		return errors.New("AgentCard is required")
	}
	version := SchemaVersion(config.A2AVersion)
	if len(versions) > 0 {
		version = versions[0]
	}
	if !validSchemaVersion(version) {
		return fmt.Errorf("AgentCard schema version %q is not supported", version)
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
	if version == SchemaVersionV03 && normalized.URL == "" {
		return errors.New("A2A 0.3 AgentCard requires top-level url")
	}
	if version == SchemaVersionV10 && len(normalized.SupportedInterfaces) == 0 {
		return errors.New("A2A 1.0 AgentCard requires supportedInterfaces")
	}

	var cardIP net.IP
	for index, item := range normalized.SupportedInterfaces {
		parsed, address, err := parseInterfaceEndpoint(item.URL)
		if err != nil {
			return fmt.Errorf("AgentCard interface %d: %w", index, err)
		}
		if strings.TrimSpace(item.ProtocolBinding) == "" {
			return fmt.Errorf("AgentCard interface %d protocolBinding is required", index)
		}
		if cardIP == nil {
			cardIP = address
		} else if !cardIP.Equal(address) {
			return fmt.Errorf(
				"AgentCard interface %d host %q must use the same IP as interface 0",
				index,
				parsed.Hostname(),
			)
		}
	}
	if cardIP == nil {
		return errors.New("AgentCard must define supportedInterfaces or url")
	}
	if config.AdvertiseHost != "" {
		advertise := net.ParseIP(strings.Trim(config.AdvertiseHost, "[]"))
		if advertise == nil {
			return fmt.Errorf("advertise host %q must be an IP address", config.AdvertiseHost)
		}
		if !cardIP.Equal(advertise) {
			return fmt.Errorf(
				"AgentCard interface IP %q must match advertised IP %q",
				cardIP,
				advertise,
			)
		}
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

// BuildAgentInstanceMetadata serializes a validated AgentCard into the
// three-key instance metadata.
func BuildAgentInstanceMetadata(
	config adapterconfig.Config,
	card a2aprotocolserver.AgentCard,
) (map[string]string, error) {
	version := SchemaVersion(config.A2AVersion)
	if err := ValidateAgentCard(config, &card, version); err != nil {
		return nil, err
	}
	raw, err := marshalCanonicalCard(version, canonicalCard(version, card))
	if err != nil {
		return nil, adaptererrors.WrapError("marshal AgentCard", err)
	}
	return BuildAgentInstanceMetadataFromRaw(config, version, raw)
}

// BuildAgentInstanceMetadataFromRaw preserves the supplied AgentCard JSON.
func BuildAgentInstanceMetadataFromRaw(
	config adapterconfig.Config,
	version SchemaVersion,
	raw []byte,
) (map[string]string, error) {
	document, err := ParseCardDocument(config, version, raw)
	if err != nil {
		return nil, err
	}
	metadata := map[string]string{
		MetadataAgentName:  config.ServiceName,
		MetadataAgentCard:  string(document.Raw()),
		MetadataA2AVersion: string(version),
	}
	if err := validateMetadataValues(metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

// ValidateAgentInstanceMetadata validates the three AgentCard keys. The SDK
// also promotes structured instance fields into GetMetadata(), so platform
// fields such as version and location are allowed but are not part of the
// three-key contract.
func ValidateAgentInstanceMetadata(metadata map[string]string) error {
	if err := validateMetadataValues(metadata); err != nil {
		return err
	}
	for key := range metadata {
		if key == MetadataAgentName || key == MetadataAgentCard || key == MetadataA2AVersion ||
			isPromotedInstanceMetadataKey(key) {
			continue
		}
		return fmt.Errorf("instance metadata contains unsupported key %q", key)
	}
	agentName := strings.TrimSpace(metadata[MetadataAgentName])
	if agentName == "" {
		return fmt.Errorf("instance metadata %q is required", MetadataAgentName)
	}
	cardJSON := metadata[MetadataAgentCard]
	if strings.TrimSpace(cardJSON) == "" {
		return fmt.Errorf("instance metadata %q is required", MetadataAgentCard)
	}
	version := SchemaVersion(strings.TrimSpace(metadata[MetadataA2AVersion]))
	if !validSchemaVersion(version) {
		return fmt.Errorf("instance metadata %q must be 0.3 or 1.0", MetadataA2AVersion)
	}
	if err := validateSchemaShape(version, []byte(cardJSON)); err != nil {
		return err
	}
	card, err := ParseAgentCardByVersion(cardJSON, string(version))
	if err != nil {
		return fmt.Errorf("decode AgentCard metadata: %w", err)
	}
	if card.Name != agentName {
		return fmt.Errorf("AgentCard name %q must match %q metadata %q", card.Name, MetadataAgentName, agentName)
	}
	if strings.TrimSpace(card.Version) == "" {
		return errors.New("AgentCard version is required")
	}
	if _, err = card.Host(); err != nil {
		return fmt.Errorf("invalid AgentCard interface: %w", err)
	}
	return nil
}

// isPromotedInstanceMetadataKey reports structured instance fields that
// polaris-go promotes into Instance.GetMetadata().
func isPromotedInstanceMetadataKey(key string) bool {
	switch key {
	case "version", "region", "zone", "campus",
		"internal-lastheartbeat", "internal-empty-protection":
		return true
	default:
		return false
	}
}

// ParseAgentInstanceMetadata decodes the canonical three-key metadata.
func ParseAgentInstanceMetadata(
	metadata map[string]string,
) (AgentInstanceMetadata, a2aprotocolserver.AgentCard, error) {
	if err := ValidateAgentInstanceMetadata(metadata); err != nil {
		return AgentInstanceMetadata{}, a2aprotocolserver.AgentCard{}, err
	}
	version := SchemaVersion(metadata[MetadataA2AVersion])
	cardJSON := []byte(metadata[MetadataAgentCard])
	document, err := parseDocumentWithoutConfig(version, cardJSON)
	if err != nil {
		return AgentInstanceMetadata{}, a2aprotocolserver.AgentCard{}, err
	}
	parsed, err := ParseAgentCardByVersion(string(cardJSON), string(version))
	if err != nil {
		return AgentInstanceMetadata{}, a2aprotocolserver.AgentCard{}, err
	}
	// Keep the complete typed document; only normalize the endpoint fields.
	document.Card.URL = parsed.URL
	document.Card.PreferredTransport = stringPointer(parsed.PreferredTransport)
	document.Card.ProtocolVersion = stringPointer(parsed.ProtocolVersion)
	document.Card.SupportedInterfaces = a2aInterfacesFromParsed(parsed.Interfaces())
	return AgentInstanceMetadata{
		AgentName:  metadata[MetadataAgentName],
		CardJSON:   metadata[MetadataAgentCard],
		A2AVersion: version,
		Card:       document.Card,
	}, document.Card, nil
}

func a2aInterfacesFromParsed(
	interfaces []AgentInterface,
) []a2aprotocolserver.AgentInterface {
	if interfaces == nil {
		return nil
	}
	result := make([]a2aprotocolserver.AgentInterface, 0, len(interfaces))
	for _, item := range interfaces {
		result = append(result, a2aprotocolserver.AgentInterface{
			URL:             item.URL,
			ProtocolBinding: item.ProtocolBinding,
			ProtocolVersion: item.ProtocolVersion,
			Tenant:          item.Tenant,
		})
	}
	return result
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
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

// SelectInterface returns the first interface matching protocolBinding.
func SelectInterface(
	card a2aprotocolserver.AgentCard,
	protocolBinding string,
) (a2aprotocol.AgentInterface, bool) {
	card.NormalizeInterfaces()
	protocolBinding = strings.TrimSpace(protocolBinding)
	for _, item := range card.SupportedInterfaces {
		if protocolBinding == "" {
			if strings.EqualFold(item.ProtocolBinding, a2aprotocol.ProtocolBindingJSONRPC) ||
				strings.EqualFold(item.ProtocolBinding, a2aprotocol.ProtocolBindingHTTPJSON) {
				return item, true
			}
			continue
		}
		if strings.EqualFold(item.ProtocolBinding, protocolBinding) {
			return item, true
		}
	}
	return a2aprotocol.AgentInterface{}, false
}

// ValidateConsumerBinding verifies that the fixed trpc-agent A2A client can
// use protocolBinding. An empty binding means the resolver selects one.
func ValidateConsumerBinding(protocolBinding string) error {
	binding := strings.TrimSpace(protocolBinding)
	if binding == "" ||
		strings.EqualFold(binding, a2aprotocol.ProtocolBindingJSONRPC) ||
		strings.EqualFold(binding, a2aprotocol.ProtocolBindingHTTPJSON) {
		return nil
	}
	return fmt.Errorf(
		"%w: %q; supported bindings are %q and %q",
		adaptererrors.ErrUnsupportedProtocolBinding,
		binding,
		a2aprotocol.ProtocolBindingJSONRPC,
		a2aprotocol.ProtocolBindingHTTPJSON,
	)
}

// SelectConsumableInterface returns the first interface the fixed A2A v1
// client can call. It never normalizes a 0.3 card into a 1.0 interface.
func SelectConsumableInterface(
	card a2aprotocolserver.AgentCard,
	a2aVersion SchemaVersion,
	protocolBinding string,
) (a2aprotocol.AgentInterface, error) {
	if err := ValidateConsumerBinding(protocolBinding); err != nil {
		return a2aprotocol.AgentInterface{}, err
	}
	binding := strings.TrimSpace(protocolBinding)
	for _, item := range card.SupportedInterfaces {
		if binding != "" && !strings.EqualFold(item.ProtocolBinding, binding) {
			continue
		}
		if binding == "" &&
			!strings.EqualFold(item.ProtocolBinding, a2aprotocol.ProtocolBindingJSONRPC) &&
			!strings.EqualFold(item.ProtocolBinding, a2aprotocol.ProtocolBindingHTTPJSON) {
			continue
		}
		if consumableProtocolVersion(item.ProtocolVersion, a2aVersion) {
			return item, nil
		}
	}
	return a2aprotocol.AgentInterface{}, adaptererrors.ErrNoAvailableInstance
}

func consumableProtocolVersion(version string, a2aVersion SchemaVersion) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		// A 1.0 card may omit the per-interface wire version; the fixed client
		// still sends A2A 1.0. A 0.3 card does not provide that guarantee.
		return a2aVersion == SchemaVersionV10
	}
	parts := strings.Split(version, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil {
		return false
	}
	if len(parts) == 3 {
		if _, patchErr := strconv.ParseUint(parts[2], 10, 32); patchErr != nil {
			return false
		}
	}
	return major == 1 && minor == 0
}

// Endpoint returns the normalized request URL for an interface.
func Endpoint(item a2aprotocol.AgentInterface) (string, error) {
	parsed, _, err := parseInterfaceEndpoint(item.URL)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func parseDocumentWithoutConfig(version SchemaVersion, raw []byte) (CardDocument, error) {
	if err := validateSchemaShape(version, raw); err != nil {
		return CardDocument{}, err
	}
	var card a2aprotocolserver.AgentCard
	if err := json.Unmarshal(raw, &card); err != nil {
		return CardDocument{}, err
	}
	return CardDocument{
		Version: version,
		Card:    card,
		raw:     append(json.RawMessage(nil), raw...),
	}, nil
}

func validateSchemaShape(version SchemaVersion, raw []byte) error {
	if len(raw) == 0 {
		return errors.New("AgentCard JSON is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("decode AgentCard JSON: %w", err)
	}
	_, hasURL := fields["url"]
	interfaces, hasInterfaces := fields["supportedInterfaces"]
	switch version {
	case SchemaVersionV03:
		if !hasURL {
			return errors.New("A2A 0.3 AgentCard must use top-level url")
		}
		if hasInterfaces {
			var items []json.RawMessage
			if err := json.Unmarshal(interfaces, &items); err != nil || len(items) > 0 {
				return errors.New("A2A 0.3 AgentCard must not use v1 supportedInterfaces")
			}
		}
	case SchemaVersionV10:
		for _, legacy := range []string{
			"url",
			"protocolVersion",
			"preferredTransport",
			"additionalInterfaces",
			"supportsAuthenticatedExtendedCard",
			"security",
		} {
			if _, ok := fields[legacy]; ok {
				return fmt.Errorf("A2A 1.0 AgentCard must not use legacy field %q", legacy)
			}
		}
		if !hasInterfaces {
			return errors.New("A2A 1.0 AgentCard must use supportedInterfaces")
		}
		var items []json.RawMessage
		if err := json.Unmarshal(interfaces, &items); err != nil || len(items) == 0 {
			return errors.New("A2A 1.0 supportedInterfaces must be a non-empty array")
		}
		for index, item := range items {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(item, &fields); err != nil {
				return fmt.Errorf("decode A2A 1.0 supportedInterfaces[%d]: %w", index, err)
			}
			if _, ok := fields["transport"]; ok {
				return fmt.Errorf("A2A 1.0 interface %d must not use transport", index)
			}
		}
	default:
		return fmt.Errorf("AgentCard schema version %q is not supported", version)
	}
	return nil
}

func canonicalCard(version SchemaVersion, card a2aprotocolserver.AgentCard) a2aprotocolserver.AgentCard {
	card.NormalizeInterfaces()
	if version == SchemaVersionV10 {
		card.URL = ""
		card.ProtocolVersion = nil
		card.PreferredTransport = nil
		card.AdditionalInterfaces = nil
		card.SupportsAuthenticatedExtendedCard = nil
		card.Security = nil
	} else if version == SchemaVersionV03 && card.URL != "" {
		card.SupportedInterfaces = nil
	}
	return card
}

func marshalCanonicalCard(version SchemaVersion, card a2aprotocolserver.AgentCard) ([]byte, error) {
	raw, err := json.Marshal(card)
	if err != nil {
		return nil, err
	}
	if version != SchemaVersionV10 {
		return raw, nil
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	interfacesRaw, ok := document["supportedInterfaces"]
	if !ok {
		return nil, errors.New("A2A 1.0 AgentCard has no supportedInterfaces")
	}
	var interfaces []map[string]json.RawMessage
	if err := json.Unmarshal(interfacesRaw, &interfaces); err != nil {
		return nil, err
	}
	for index := range interfaces {
		delete(interfaces[index], "transport")
	}
	document["supportedInterfaces"], err = json.Marshal(interfaces)
	if err != nil {
		return nil, err
	}
	return json.Marshal(document)
}

func validateMetadataValues(metadata map[string]string) error {
	if len(metadata) > maxInstanceMetadataEntries {
		return fmt.Errorf(
			"instance metadata has %d entries, maximum is %d",
			len(metadata),
			maxInstanceMetadataEntries,
		)
	}
	for key, value := range metadata {
		if key == "" {
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
	return nil
}

func validSchemaVersion(version SchemaVersion) bool {
	return version == SchemaVersionV03 || version == SchemaVersionV10
}

func parseInterfaceEndpoint(raw string) (*url.URL, net.IP, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil, errors.New("AgentCard interface URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, nil, errors.New("AgentCard interface URL is invalid")
	}
	if parsed.Scheme == "" || parsed.Hostname() == "" {
		return nil, nil, errors.New("AgentCard interface URL must include scheme and host")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, nil, fmt.Errorf("AgentCard interface scheme %q is not supported", parsed.Scheme)
	}
	parsed.Scheme = scheme
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, nil, errors.New("AgentCard interface URL must not include credentials, query, or fragment")
	}
	if parsed.RawPath != "" {
		return nil, nil, errors.New("AgentCard interface URL must not contain noncanonical path encoding")
	}
	if err := a2apath.Validate(parsed.Path); err != nil {
		return nil, nil, err
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, nil, errors.New("AgentCard interface port must be in 1..65535")
		}
	} else if strings.HasSuffix(parsed.Host, ":") {
		return nil, nil, errors.New("AgentCard interface port must not be empty")
	}
	address := net.ParseIP(parsed.Hostname())
	if address == nil {
		return nil, nil, fmt.Errorf(
			"AgentCard interface host %q must be an IP address",
			parsed.Hostname(),
		)
	}
	return parsed, address, nil
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
