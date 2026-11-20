/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package agentcard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	polarismodel "github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// AgentCardSchemaVersion03 是 A2A AgentCard 0.3 schema 的规范化版本值。
	AgentCardSchemaVersion03 = "0.3"
	// AgentCardSchemaVersion10 是 A2A AgentCard 1.0 schema 的规范化版本值。
	AgentCardSchemaVersion10 = "1.0"
)

const defaultAgentInterfaceBinding = "JSONRPC"

// AgentProvider 是 AgentCard 中声明的提供方信息。
type AgentProvider struct {
	Organization string
	URL          string
}

// AgentCapabilityExtension 是 AgentCard capabilities.extensions 中的一项声明。
type AgentCapabilityExtension struct {
	URI         string
	Description string
	Required    bool
	Params      json.RawMessage
}

// AgentCapabilities 是 AgentCard 声明的可选能力。
type AgentCapabilities struct {
	Streaming              bool
	PushNotifications      bool
	StateTransitionHistory bool
	ExtendedAgentCard      bool
	Extensions             []AgentCapabilityExtension
}

// AgentCardSignature 是 AgentCard 中的一项 JWS 签名声明。
//
// 本类型只保留签名内容，不执行签名校验。
type AgentCardSignature struct {
	Header    json.RawMessage
	Protected string
	Signature string
}

// AgentInterface 是 AgentCard 中声明的协议无关 endpoint。
type AgentInterface struct {
	URL             string
	ProtocolBinding string
	ProtocolVersion string
	Tenant          string
	// Transport 是 ProtocolBinding 的兼容镜像，仅用于旧调用方读取。
	//
	// Deprecated: use ProtocolBinding.
	Transport string
}

// AgentCard 是 A2A AgentCard 的协议无关视图。
//
// RawJSON 保存调用方传入的原始 JSON；注册路径必须使用 RawJSON，不得重新序列化。
// 0.3 的 URL/AdditionalInterfaces 会被映射到 SupportedInterfaces，调用方统一按
// ProtocolBinding 选择 endpoint。
type AgentCard struct {
	SchemaVersion        string
	Name                 string
	Description          string
	Version              string
	Provider             *AgentProvider
	IconURL              string
	DocumentationURL     string
	Capabilities         AgentCapabilities
	SecuritySchemes      map[string]json.RawMessage
	SecurityRequirements json.RawMessage
	DefaultInputModes    []string
	DefaultOutputModes   []string
	Skills               []polarismodel.AgentSkill
	Signatures           []AgentCardSignature
	SupportedInterfaces  []AgentInterface

	// URL、PreferredTransport、ProtocolVersion、AdditionalInterfaces 和 Security
	// 是 A2A 0.3 的原始字段。
	URL                  string
	PreferredTransport   string
	ProtocolVersion      string
	AdditionalInterfaces []AgentInterface
	Security             json.RawMessage
	// SupportsAuthenticatedExtendedCard 是 A2A 0.3 的扩展卡片声明。
	SupportsAuthenticatedExtendedCard bool

	// RawJSON 是解析时的原始 JSON，保证签名和未知字段不被重写。
	RawJSON string
	// UnknownFields 保存当前模型未覆盖的顶层字段。
	UnknownFields map[string]json.RawMessage
}

// NormalizeAgentCardSchemaVersion 将支持的版本别名规范化为 0.3 或 1.0。
func NormalizeAgentCardSchemaVersion(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case AgentCardSchemaVersion03, "0.3.0":
		return AgentCardSchemaVersion03, nil
	case AgentCardSchemaVersion10, "1.0.0":
		return AgentCardSchemaVersion10, nil
	default:
		return "", fmt.Errorf("AgentCard schema version must be one of %q, %q",
			AgentCardSchemaVersion03, AgentCardSchemaVersion10)
	}
}

// ParseAgentCard 按卡片结构推断 schema 版本并严格解析。
//
// 新注册路径应优先使用 ParseAgentCardByVersion，明确传入 schema。
func ParseAgentCard(cardJSON string) (*AgentCard, error) {
	fields, err := decodeAgentCardFields(cardJSON)
	if err != nil {
		return nil, err
	}
	_, hasURL := fields["url"]
	_, hasSupportedInterfaces := fields["supportedInterfaces"]
	var version string
	switch {
	case hasSupportedInterfaces && !hasURL:
		version = AgentCardSchemaVersion10
	case hasURL && !hasSupportedInterfaces:
		version = AgentCardSchemaVersion03
	default:
		return nil, fmt.Errorf("AgentCard must contain exactly one of url or supportedInterfaces")
	}
	return buildAgentCard(cardJSON, fields, version)
}

// ParseAgentCardByVersion 按显式声明的 schema 版本严格解析 AgentCard。
func ParseAgentCardByVersion(cardJSON, version string) (*AgentCard, error) {
	normalized, err := NormalizeAgentCardSchemaVersion(version)
	if err != nil {
		return nil, err
	}
	fields, err := decodeAgentCardFields(cardJSON)
	if err != nil {
		return nil, err
	}
	return buildAgentCard(cardJSON, fields, normalized)
}

// Interfaces 返回统一的 interface 列表。
func (c *AgentCard) Interfaces() []AgentInterface {
	if c == nil {
		return nil
	}
	if len(c.SupportedInterfaces) > 0 {
		result := make([]AgentInterface, 0, len(c.SupportedInterfaces))
		for _, item := range c.SupportedInterfaces {
			result = append(result, normalizeAgentInterface(item))
		}
		return result
	}
	result := make([]AgentInterface, 0, len(c.AdditionalInterfaces)+1)
	if c.URL != "" {
		binding := c.PreferredTransport
		if binding == "" {
			binding = defaultAgentInterfaceBinding
		}
		item := AgentInterface{
			URL:             c.URL,
			ProtocolBinding: binding,
			ProtocolVersion: c.ProtocolVersion,
			Transport:       binding,
		}
		result = append(result, normalizeAgentInterface(item))
	}
	for _, item := range c.AdditionalInterfaces {
		result = append(result, normalizeAgentInterface(item))
	}
	return result
}

// PrimaryInterface 返回首选接口。
func (c *AgentCard) PrimaryInterface() (AgentInterface, bool) {
	interfaces := c.Interfaces()
	if len(interfaces) == 0 {
		return AgentInterface{}, false
	}
	return interfaces[0], true
}

// SelectInterface 按 protocolBinding 选择 interface；binding 为空时返回首选接口。
func (c *AgentCard) SelectInterface(binding string) (AgentInterface, bool) {
	binding = strings.TrimSpace(binding)
	if binding == "" {
		return c.PrimaryInterface()
	}
	for _, item := range c.Interfaces() {
		if strings.EqualFold(item.ProtocolBinding, binding) {
			return item, true
		}
	}
	return AgentInterface{}, false
}

// Host 返回全部 interface 共用的规范化 IP。
func (c *AgentCard) Host() (string, error) {
	if c == nil {
		return "", fmt.Errorf("AgentCard is required")
	}
	return validateAgentCardInterfaceHosts(c.Interfaces())
}

// NormalizeAgentInterfaceHost 校验并规范化 interface URL 中的 IP。
func NormalizeAgentInterfaceHost(rawURL string) (string, error) {
	parsed, err := parseAgentInterfaceURL(rawURL)
	if err != nil {
		return "", err
	}
	return normalizeAgentIPAddress(parsed.Hostname())
}

// MarshalAgentCard 返回卡片 JSON。
//
// 解析得到的卡片始终返回原始 JSON，保证未知字段和签名不被重写。仅对程序化构造的
// 卡片（RawJSON 为空）按 SchemaVersion 生成 JSON。
func MarshalAgentCard(card *AgentCard) ([]byte, error) {
	if card == nil {
		return nil, fmt.Errorf("AgentCard is required")
	}
	if card.RawJSON != "" {
		return []byte(card.RawJSON), nil
	}
	version := card.SchemaVersion
	if version == "" {
		version = AgentCardSchemaVersion10
	}
	normalized, err := NormalizeAgentCardSchemaVersion(version)
	if err != nil {
		return nil, err
	}
	payload := make(map[string]interface{}, 16)
	for key, value := range card.UnknownFields {
		payload[key] = append(json.RawMessage(nil), value...)
	}
	payload["name"] = card.Name
	payload["description"] = card.Description
	payload["version"] = card.Version
	if card.Provider != nil {
		payload["provider"] = map[string]interface{}{
			"organization": card.Provider.Organization,
			"url":          card.Provider.URL,
		}
	}
	if card.IconURL != "" {
		payload["iconUrl"] = card.IconURL
	}
	if card.DocumentationURL != "" {
		payload["documentationUrl"] = card.DocumentationURL
	}
	payload["capabilities"] = marshalAgentCapabilities(card.Capabilities, normalized)
	if len(card.SecuritySchemes) > 0 {
		payload["securitySchemes"] = card.SecuritySchemes
	}
	payload["defaultInputModes"] = nonNilStrings(card.DefaultInputModes)
	payload["defaultOutputModes"] = nonNilStrings(card.DefaultOutputModes)
	payload["skills"] = marshalAgentCardSkills(card.Skills, normalized)
	if len(card.Signatures) > 0 {
		payload["signatures"] = marshalAgentCardSignatures(card.Signatures)
	}

	interfaces := card.Interfaces()
	if len(interfaces) == 0 {
		return nil, fmt.Errorf("AgentCard must contain at least one interface")
	}
	if normalized == AgentCardSchemaVersion03 {
		main := interfaces[0]
		payload["url"] = main.URL
		if main.ProtocolBinding != "" {
			payload["preferredTransport"] = main.ProtocolBinding
		}
		if card.ProtocolVersion != "" {
			payload["protocolVersion"] = card.ProtocolVersion
		}
		if len(interfaces) > 1 {
			payload["additionalInterfaces"] = marshalAgentCardInterfaces(interfaces[1:], normalized)
		}
		if len(card.Security) > 0 {
			payload["security"] = card.Security
		}
		if card.SupportsAuthenticatedExtendedCard {
			payload["supportsAuthenticatedExtendedCard"] = true
		}
	} else {
		payload["supportedInterfaces"] = marshalAgentCardInterfaces(interfaces, normalized)
		if len(card.SecurityRequirements) > 0 {
			payload["securityRequirements"] = card.SecurityRequirements
		}
	}
	data, err := marshalJSONNoEscape(payload)
	if err != nil {
		return nil, err
	}
	if _, err = ParseAgentCardByVersion(string(data), normalized); err != nil {
		return nil, err
	}
	return data, nil
}

func decodeAgentCardFields(cardJSON string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(cardJSON) == "" {
		return nil, fmt.Errorf("AgentCard JSON is empty")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cardJSON), &fields); err != nil {
		return nil, fmt.Errorf("AgentCard JSON is invalid: %w", err)
	}
	if fields == nil {
		return nil, fmt.Errorf("AgentCard JSON must be an object")
	}
	return fields, nil
}

func buildAgentCard(cardJSON string, fields map[string]json.RawMessage, version string) (*AgentCard, error) {
	if err := validateAgentCardVersionShape(fields, version); err != nil {
		return nil, err
	}
	identity, err := decodeAgentCardIdentity(fields)
	if err != nil {
		return nil, err
	}
	card := &AgentCard{
		SchemaVersion:    version,
		Name:             identity.name,
		Description:      identity.description,
		Version:          identity.version,
		IconURL:          identity.iconURL,
		DocumentationURL: identity.documentationURL,
		RawJSON:          cardJSON,
	}
	if err = decodeAgentCardOptionalFields(card, fields); err != nil {
		return nil, err
	}
	if version == AgentCardSchemaVersion03 {
		if err = buildAgentCardV03(card, fields); err != nil {
			return nil, err
		}
	} else if err = buildAgentCardV10(card, fields); err != nil {
		return nil, err
	}
	if _, err = validateAgentCardInterfaceHosts(card.SupportedInterfaces); err != nil {
		return nil, err
	}
	card.UnknownFields = unknownAgentCardFields(fields, version)
	return card, nil
}

func validateAgentCardVersionShape(fields map[string]json.RawMessage, version string) error {
	if version == AgentCardSchemaVersion03 {
		if _, ok := fields["supportedInterfaces"]; ok {
			return fmt.Errorf("AgentCard 0.3 must not use supportedInterfaces")
		}
		if _, ok := fields["securityRequirements"]; ok {
			return fmt.Errorf("AgentCard 0.3 must not use securityRequirements")
		}
		return nil
	}
	for _, key := range []string{
		"url", "preferredTransport", "protocolVersion", "additionalInterfaces",
		"security", "supportsAuthenticatedExtendedCard",
	} {
		if _, ok := fields[key]; ok {
			return fmt.Errorf("AgentCard 1.0 must not use legacy field %q", key)
		}
	}
	return nil
}

type agentCardIdentity struct {
	name             string
	version          string
	description      string
	iconURL          string
	documentationURL string
}

func decodeAgentCardIdentity(fields map[string]json.RawMessage) (agentCardIdentity, error) {
	name, _, err := decodeJSONString(fields, "name")
	if err != nil {
		return agentCardIdentity{}, err
	}
	if name == "" || name != strings.TrimSpace(name) {
		return agentCardIdentity{}, fmt.Errorf(
			"AgentCard name must be non-empty and must not contain surrounding whitespace")
	}
	cardVersion, _, err := decodeJSONString(fields, "version")
	if err != nil {
		return agentCardIdentity{}, err
	}
	if cardVersion == "" || cardVersion != strings.TrimSpace(cardVersion) {
		return agentCardIdentity{}, fmt.Errorf(
			"AgentCard version must be non-empty and must not contain surrounding whitespace")
	}
	description, _, err := decodeJSONString(fields, "description")
	if err != nil {
		return agentCardIdentity{}, err
	}
	iconURL, _, err := decodeJSONString(fields, "iconUrl")
	if err != nil {
		return agentCardIdentity{}, err
	}
	if iconURL != "" {
		if err = validateAgentCardExternalURL("iconUrl", iconURL); err != nil {
			return agentCardIdentity{}, err
		}
	}
	documentationURL, _, err := decodeJSONString(fields, "documentationUrl")
	if err != nil {
		return agentCardIdentity{}, err
	}
	if documentationURL != "" {
		if err = validateAgentCardExternalURL("documentationUrl", documentationURL); err != nil {
			return agentCardIdentity{}, err
		}
	}
	return agentCardIdentity{
		name:             name,
		version:          cardVersion,
		description:      description,
		iconURL:          iconURL,
		documentationURL: documentationURL,
	}, nil
}

func decodeAgentCardOptionalFields(card *AgentCard, fields map[string]json.RawMessage) error {
	var err error
	if raw, ok := fields["provider"]; ok && !isJSONNull(raw) {
		provider, providerErr := parseAgentProvider(raw)
		if providerErr != nil {
			return providerErr
		}
		card.Provider = provider
	}
	if raw, ok := fields["capabilities"]; ok && !isJSONNull(raw) {
		capabilities, capabilitiesErr := parseAgentCapabilities(raw)
		if capabilitiesErr != nil {
			return capabilitiesErr
		}
		card.Capabilities = capabilities
	}
	if raw, ok := fields["securitySchemes"]; ok && !isJSONNull(raw) {
		var schemes map[string]json.RawMessage
		if err = json.Unmarshal(raw, &schemes); err != nil || schemes == nil {
			return fmt.Errorf("AgentCard securitySchemes must be an object")
		}
		card.SecuritySchemes = cloneRawMessageMap(schemes)
	}
	if raw, ok := fields["defaultInputModes"]; ok {
		values, decodeErr := decodeJSONStringSlice(raw, "defaultInputModes")
		if decodeErr != nil {
			return decodeErr
		}
		card.DefaultInputModes = values
	}
	if raw, ok := fields["defaultOutputModes"]; ok {
		values, decodeErr := decodeJSONStringSlice(raw, "defaultOutputModes")
		if decodeErr != nil {
			return decodeErr
		}
		card.DefaultOutputModes = values
	}
	if raw, ok := fields["skills"]; ok && !isJSONNull(raw) {
		skills, skillErr := parseAgentCardSkills(raw)
		if skillErr != nil {
			return skillErr
		}
		card.Skills = skills
	}
	if raw, ok := fields["signatures"]; ok && !isJSONNull(raw) {
		signatures, signatureErr := parseAgentCardSignatures(raw)
		if signatureErr != nil {
			return signatureErr
		}
		card.Signatures = signatures
	}
	return nil
}

func buildAgentCardV03(card *AgentCard, fields map[string]json.RawMessage) error {
	mainURL, _, err := decodeJSONString(fields, "url")
	if err != nil {
		return err
	}
	if strings.TrimSpace(mainURL) == "" {
		return fmt.Errorf("AgentCard 0.3 requires a non-empty top-level url")
	}
	preferredTransport, _, err := decodeJSONString(fields, "preferredTransport")
	if err != nil {
		return err
	}
	if preferredTransport != strings.TrimSpace(preferredTransport) {
		return fmt.Errorf("AgentCard 0.3 preferredTransport must not contain surrounding whitespace")
	}
	if preferredTransport == "" {
		preferredTransport = defaultAgentInterfaceBinding
	}
	protocolVersion, _, err := decodeJSONString(fields, "protocolVersion")
	if err != nil {
		return err
	}
	if protocolVersion != strings.TrimSpace(protocolVersion) {
		return fmt.Errorf("AgentCard 0.3 protocolVersion must not contain surrounding whitespace")
	}
	if protocolVersion != "" {
		normalized, normalizeErr := NormalizeAgentCardSchemaVersion(protocolVersion)
		if normalizeErr != nil {
			return fmt.Errorf("AgentCard 0.3 protocolVersion is invalid: %w", normalizeErr)
		}
		if normalized != AgentCardSchemaVersion03 {
			return fmt.Errorf("AgentCard 0.3 protocolVersion %q is not compatible with schema 0.3",
				protocolVersion)
		}
	}
	card.URL = mainURL
	card.PreferredTransport = preferredTransport
	card.ProtocolVersion = protocolVersion
	main := AgentInterface{
		URL:             mainURL,
		ProtocolBinding: preferredTransport,
		ProtocolVersion: protocolVersion,
		Transport:       preferredTransport,
	}
	card.SupportedInterfaces = append(card.SupportedInterfaces, main)
	if raw, ok := fields["additionalInterfaces"]; ok && !isJSONNull(raw) {
		additional, additionalErr := parseAgentCardInterfaces(raw, AgentCardSchemaVersion03, false)
		if additionalErr != nil {
			return additionalErr
		}
		for index := range additional {
			if additional[index].ProtocolVersion == "" {
				additional[index].ProtocolVersion = protocolVersion
			}
		}
		card.AdditionalInterfaces = additional
		card.SupportedInterfaces = append(card.SupportedInterfaces, additional...)
	}
	if raw, ok := fields["security"]; ok && !isJSONNull(raw) {
		if err = validateJSONArray(raw, "security"); err != nil {
			return err
		}
		card.Security = append(json.RawMessage(nil), raw...)
	}
	if raw, ok := fields["supportsAuthenticatedExtendedCard"]; ok && !isJSONNull(raw) {
		var supported bool
		if err = json.Unmarshal(raw, &supported); err != nil {
			return fmt.Errorf("AgentCard supportsAuthenticatedExtendedCard must be a boolean")
		}
		card.SupportsAuthenticatedExtendedCard = supported
		if supported {
			card.Capabilities.ExtendedAgentCard = true
		}
	}
	return nil
}

func buildAgentCardV10(card *AgentCard, fields map[string]json.RawMessage) error {
	raw, ok := fields["supportedInterfaces"]
	if !ok || isJSONNull(raw) {
		return fmt.Errorf("AgentCard 1.0 requires supportedInterfaces")
	}
	interfaces, err := parseAgentCardInterfaces(raw, AgentCardSchemaVersion10, true)
	if err != nil {
		return err
	}
	card.SupportedInterfaces = interfaces
	if security, exists := fields["securityRequirements"]; exists && !isJSONNull(security) {
		if err = validateJSONArray(security, "securityRequirements"); err != nil {
			return err
		}
		card.SecurityRequirements = append(json.RawMessage(nil), security...)
	}
	return nil
}

func parseAgentProvider(raw json.RawMessage) (*AgentProvider, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("AgentCard provider must be an object")
	}
	organization, _, err := decodeJSONString(fields, "organization")
	if err != nil {
		return nil, err
	}
	providerURL, _, err := decodeJSONString(fields, "url")
	if err != nil {
		return nil, err
	}
	if providerURL != "" {
		if err = validateAgentCardExternalURL("provider url", providerURL); err != nil {
			return nil, err
		}
	}
	return &AgentProvider{Organization: organization, URL: providerURL}, nil
}

func parseAgentCapabilities(raw json.RawMessage) (AgentCapabilities, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return AgentCapabilities{}, fmt.Errorf("AgentCard capabilities must be an object")
	}
	streaming, _, err := decodeJSONBool(fields, "streaming")
	if err != nil {
		return AgentCapabilities{}, err
	}
	pushNotifications, _, err := decodeJSONBool(fields, "pushNotifications")
	if err != nil {
		return AgentCapabilities{}, err
	}
	stateTransitionHistory, _, err := decodeJSONBool(fields, "stateTransitionHistory")
	if err != nil {
		return AgentCapabilities{}, err
	}
	extendedAgentCard, _, err := decodeJSONBool(fields, "extendedAgentCard")
	if err != nil {
		return AgentCapabilities{}, err
	}
	result := AgentCapabilities{
		Streaming:              streaming,
		PushNotifications:      pushNotifications,
		StateTransitionHistory: stateTransitionHistory,
		ExtendedAgentCard:      extendedAgentCard,
	}
	if extensions, ok := fields["extensions"]; ok && !isJSONNull(extensions) {
		var items []json.RawMessage
		if err = json.Unmarshal(extensions, &items); err != nil {
			return AgentCapabilities{}, fmt.Errorf("AgentCard capabilities.extensions must be an array")
		}
		result.Extensions = make([]AgentCapabilityExtension, 0, len(items))
		for _, item := range items {
			extension, extensionErr := parseAgentCapabilityExtension(item)
			if extensionErr != nil {
				return AgentCapabilities{}, extensionErr
			}
			result.Extensions = append(result.Extensions, extension)
		}
	}
	return result, nil
}

func parseAgentCapabilityExtension(raw json.RawMessage) (AgentCapabilityExtension, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return AgentCapabilityExtension{}, fmt.Errorf("AgentCard capability extension must be an object")
	}
	uri, _, err := decodeJSONString(fields, "uri")
	if err != nil {
		return AgentCapabilityExtension{}, err
	}
	if strings.TrimSpace(uri) == "" {
		return AgentCapabilityExtension{}, fmt.Errorf("AgentCard capability extension uri is required")
	}
	description, _, err := decodeJSONString(fields, "description")
	if err != nil {
		return AgentCapabilityExtension{}, err
	}
	required, _, err := decodeJSONBool(fields, "required")
	if err != nil {
		return AgentCapabilityExtension{}, err
	}
	return AgentCapabilityExtension{
		URI:         uri,
		Description: description,
		Required:    required,
		Params:      append(json.RawMessage(nil), fields["params"]...),
	}, nil
}

func parseAgentCardInterfaces(raw json.RawMessage, version string, requireNonEmpty bool) ([]AgentInterface, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("AgentCard interfaces must be an array")
	}
	if requireNonEmpty && len(items) == 0 {
		return nil, fmt.Errorf("AgentCard must contain at least one interface")
	}
	result := make([]AgentInterface, 0, len(items))
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("AgentCard interface must be an object")
		}
		interfaceURL, _, err := decodeJSONString(fields, "url")
		if err != nil {
			return nil, err
		}
		if interfaceURL == "" || interfaceURL != strings.TrimSpace(interfaceURL) {
			return nil, fmt.Errorf(
				"AgentCard interface url must be non-empty and must not contain surrounding whitespace")
		}
		var binding string
		if version == AgentCardSchemaVersion03 {
			protocolBinding, hasProtocolBinding, err := decodeJSONString(fields, "protocolBinding")
			if err != nil {
				return nil, err
			}
			binding, _, err = decodeJSONString(fields, "transport")
			if err != nil {
				return nil, err
			}
			if hasProtocolBinding && binding != "" && !strings.EqualFold(protocolBinding, binding) {
				return nil, fmt.Errorf(
					"AgentCard 0.3 interface protocolBinding %q must match transport %q",
					protocolBinding, binding)
			}
			if hasProtocolBinding && binding == "" {
				return nil, fmt.Errorf("AgentCard 0.3 interface must declare transport")
			}
		} else {
			if _, ok := fields["transport"]; ok {
				return nil, fmt.Errorf("AgentCard 1.0 interface must not use transport")
			}
			binding, _, err = decodeJSONString(fields, "protocolBinding")
		}
		if err != nil {
			return nil, err
		}
		if binding == "" || binding != strings.TrimSpace(binding) {
			return nil, fmt.Errorf(
				"AgentCard interface protocolBinding must be non-empty and must not contain surrounding whitespace")
		}
		protocolVersion, _, err := decodeJSONString(fields, "protocolVersion")
		if err != nil {
			return nil, err
		}
		if protocolVersion != strings.TrimSpace(protocolVersion) {
			return nil, fmt.Errorf("AgentCard interface protocolVersion must not contain surrounding whitespace")
		}
		tenant, _, err := decodeJSONString(fields, "tenant")
		if err != nil {
			return nil, err
		}
		result = append(result, AgentInterface{
			URL:             interfaceURL,
			ProtocolBinding: binding,
			ProtocolVersion: protocolVersion,
			Tenant:          tenant,
			Transport:       binding,
		})
	}
	return result, nil
}

func parseAgentCardSkills(raw json.RawMessage) ([]polarismodel.AgentSkill, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("AgentCard skills must be an array")
	}
	result := make([]polarismodel.AgentSkill, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("AgentCard skill must be an object")
		}
		id, _, err := decodeJSONString(fields, "id")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("AgentCard skill id is required")
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("AgentCard skill %q is duplicated", id)
		}
		seen[id] = struct{}{}
		name, _, err := decodeJSONString(fields, "name")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("AgentCard skill name is required")
		}
		description, _, err := decodeJSONString(fields, "description")
		if err != nil {
			return nil, err
		}
		skillVersion, _, err := decodeJSONString(fields, "version")
		if err != nil {
			return nil, err
		}
		tags, err := decodeOptionalJSONStringSlice(fields, "tags")
		if err != nil {
			return nil, err
		}
		examples, err := decodeOptionalJSONStringSlice(fields, "examples")
		if err != nil {
			return nil, err
		}
		inputModes, err := decodeOptionalJSONStringSlice(fields, "inputModes")
		if err != nil {
			return nil, err
		}
		outputModes, err := decodeOptionalJSONStringSlice(fields, "outputModes")
		if err != nil {
			return nil, err
		}
		result = append(result, polarismodel.AgentSkill{
			ID:                   id,
			Name:                 name,
			Description:          description,
			Tags:                 tags,
			Examples:             examples,
			InputModes:           inputModes,
			OutputModes:          outputModes,
			Version:              skillVersion,
			Security:             append(json.RawMessage(nil), fields["security"]...),
			SecurityRequirements: append(json.RawMessage(nil), fields["securityRequirements"]...),
		})
	}
	return result, nil
}

func parseAgentCardSignatures(raw json.RawMessage) ([]AgentCardSignature, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("AgentCard signatures must be an array")
	}
	result := make([]AgentCardSignature, 0, len(items))
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("AgentCard signature must be an object")
		}
		protected, _, err := decodeJSONString(fields, "protected")
		if err != nil {
			return nil, err
		}
		signature, _, err := decodeJSONString(fields, "signature")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(protected) == "" || strings.TrimSpace(signature) == "" {
			return nil, fmt.Errorf("AgentCard signature protected and signature are required")
		}
		result = append(result, AgentCardSignature{
			Header:    append(json.RawMessage(nil), fields["header"]...),
			Protected: protected,
			Signature: signature,
		})
	}
	return result, nil
}

func validateAgentCardInterfaceHosts(interfaces []AgentInterface) (string, error) {
	if len(interfaces) == 0 {
		return "", fmt.Errorf("AgentCard must contain at least one interface")
	}
	var normalizedHost string
	for _, item := range interfaces {
		if item.ProtocolBinding == "" || item.ProtocolBinding != strings.TrimSpace(item.ProtocolBinding) {
			return "", fmt.Errorf(
				"AgentCard interface protocolBinding must be non-empty and must not contain surrounding whitespace")
		}
		host, err := NormalizeAgentInterfaceHost(item.URL)
		if err != nil {
			return "", err
		}
		if normalizedHost == "" {
			normalizedHost = host
			continue
		}
		if normalizedHost != host {
			return "", fmt.Errorf("all AgentCard interfaces must use the same IP address")
		}
	}
	return normalizedHost, nil
}

func parseAgentInterfaceURL(raw string) (*url.URL, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return nil, fmt.Errorf(
			"agent interface URL must be non-empty and must not contain surrounding whitespace")
	}
	candidate := raw
	if !strings.Contains(candidate, "://") && !strings.HasPrefix(candidate, "//") {
		candidate = "//" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return nil, fmt.Errorf("agent interface URL is invalid")
	}
	if parsed.Scheme == "" {
		parsed, err = url.Parse("http:" + candidate)
		if err != nil {
			return nil, fmt.Errorf("agent interface URL is invalid")
		}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("agent interface URL scheme is not supported; only http and https are allowed")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("agent interface URL must not contain userinfo")
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("agent interface URL must contain a host")
	}
	if err = validateURLPort(parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateAgentCardExternalURL(field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return fmt.Errorf("AgentCard %s must be an absolute URL", field)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("AgentCard %s must use http or https", field)
	}
	if parsed.User != nil {
		return fmt.Errorf("AgentCard %s must not contain userinfo", field)
	}
	return validateURLPort(parsed)
}

func validateURLPort(parsed *url.URL) error {
	portValue := parsed.Port()
	if portValue == "" {
		if strings.HasSuffix(parsed.Host, ":") {
			return fmt.Errorf("AgentCard URL port must not be empty")
		}
		return nil
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("AgentCard URL port must be in range 1..65535")
	}
	return nil
}

func normalizeAgentIPAddress(value string) (string, error) {
	host := strings.TrimSpace(value)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if strings.Contains(host, "%") {
		return "", fmt.Errorf("agent interface host must not contain an IPv6 zone")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("agent interface host must be an IP address")
	}
	if ip.IsUnspecified() {
		return "", fmt.Errorf("agent interface host must not be an unspecified address")
	}
	if ip.IsMulticast() {
		return "", fmt.Errorf("agent interface host must not be a multicast address")
	}
	return ip.String(), nil
}

func normalizeAgentInterface(item AgentInterface) AgentInterface {
	if item.ProtocolBinding == "" {
		item.ProtocolBinding = item.Transport
	}
	if item.Transport == "" {
		item.Transport = item.ProtocolBinding
	}
	return item
}

func decodeJSONString(fields map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := fields[key]
	if !ok || isJSONNull(raw) {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, fmt.Errorf("AgentCard field %q must be a string", key)
	}
	return value, true, nil
}

func decodeJSONBool(fields map[string]json.RawMessage, key string) (bool, bool, error) {
	raw, ok := fields[key]
	if !ok || isJSONNull(raw) {
		return false, false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, true, fmt.Errorf("AgentCard field %q must be a boolean", key)
	}
	return value, true, nil
}

func decodeOptionalJSONStringSlice(fields map[string]json.RawMessage, key string) ([]string, error) {
	raw, ok := fields[key]
	if !ok {
		return nil, nil
	}
	return decodeJSONStringSlice(raw, key)
}

func decodeJSONStringSlice(raw json.RawMessage, key string) ([]string, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("AgentCard field %q must be an array of strings", key)
	}
	return value, nil
}

func validateJSONArray(raw json.RawMessage, key string) error {
	var value []json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return fmt.Errorf("AgentCard field %q must be an array", key)
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func cloneRawMessageMap(value map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(value))
	for key, item := range value {
		result[key] = append(json.RawMessage(nil), item...)
	}
	return result
}

func unknownAgentCardFields(fields map[string]json.RawMessage, version string) map[string]json.RawMessage {
	known := map[string]struct{}{
		"name": {}, "description": {}, "version": {}, "provider": {},
		"iconUrl": {}, "documentationUrl": {}, "capabilities": {},
		"securitySchemes": {}, "defaultInputModes": {}, "defaultOutputModes": {},
		"skills": {}, "signatures": {},
	}
	if version == AgentCardSchemaVersion03 {
		for _, key := range []string{
			"url", "preferredTransport", "protocolVersion", "additionalInterfaces",
			"security", "supportsAuthenticatedExtendedCard",
		} {
			known[key] = struct{}{}
		}
	} else {
		known["supportedInterfaces"] = struct{}{}
		known["securityRequirements"] = struct{}{}
	}
	result := make(map[string]json.RawMessage)
	for key, value := range fields {
		if _, ok := known[key]; ok {
			continue
		}
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func marshalAgentCapabilities(capabilities AgentCapabilities, version string) map[string]interface{} {
	result := map[string]interface{}{
		"streaming":         capabilities.Streaming,
		"pushNotifications": capabilities.PushNotifications,
	}
	if version == AgentCardSchemaVersion03 && capabilities.StateTransitionHistory {
		result["stateTransitionHistory"] = true
	}
	if version == AgentCardSchemaVersion10 && capabilities.ExtendedAgentCard {
		result["extendedAgentCard"] = true
	}
	if len(capabilities.Extensions) > 0 {
		extensions := make([]map[string]interface{}, 0, len(capabilities.Extensions))
		for _, item := range capabilities.Extensions {
			extension := map[string]interface{}{
				"uri":         item.URI,
				"description": item.Description,
				"required":    item.Required,
			}
			if len(item.Params) > 0 {
				extension["params"] = item.Params
			}
			extensions = append(extensions, extension)
		}
		result["extensions"] = extensions
	}
	return result
}

func marshalAgentCardSkills(skills []polarismodel.AgentSkill, version string) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(skills))
	for _, skill := range skills {
		item := map[string]interface{}{
			"id":          skill.ID,
			"name":        skill.Name,
			"description": skill.Description,
			"tags":        nonNilStrings(skill.Tags),
		}
		if len(skill.Examples) > 0 {
			item["examples"] = skill.Examples
		}
		if len(skill.InputModes) > 0 {
			item["inputModes"] = skill.InputModes
		}
		if len(skill.OutputModes) > 0 {
			item["outputModes"] = skill.OutputModes
		}
		if version == AgentCardSchemaVersion03 {
			if skill.Version != "" {
				item["version"] = skill.Version
			}
			if len(skill.Security) > 0 {
				item["security"] = skill.Security
			}
		} else if len(skill.SecurityRequirements) > 0 {
			item["securityRequirements"] = skill.SecurityRequirements
		}
		result = append(result, item)
	}
	return result
}

func marshalAgentCardInterfaces(interfaces []AgentInterface, version string) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(interfaces))
	for _, item := range interfaces {
		normalized := normalizeAgentInterface(item)
		value := map[string]interface{}{
			"url": normalized.URL,
		}
		if version == AgentCardSchemaVersion03 {
			value["transport"] = normalized.ProtocolBinding
		} else {
			value["protocolBinding"] = normalized.ProtocolBinding
		}
		if normalized.ProtocolVersion != "" {
			value["protocolVersion"] = normalized.ProtocolVersion
		}
		if normalized.Tenant != "" {
			value["tenant"] = normalized.Tenant
		}
		result = append(result, value)
	}
	return result
}

func marshalAgentCardSignatures(signatures []AgentCardSignature) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(signatures))
	for _, item := range signatures {
		value := map[string]interface{}{
			"protected": item.Protected,
			"signature": item.Signature,
		}
		if len(item.Header) > 0 {
			value["header"] = item.Header
		}
		result = append(result, value)
	}
	return result
}

func marshalJSONNoEscape(value interface{}) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
