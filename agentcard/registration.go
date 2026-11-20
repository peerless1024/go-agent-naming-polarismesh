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
	"encoding/json"
	"fmt"
	"strings"

	polarismodel "github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// DefaultAgentRegistrationPort 是 A2A 实例未显式配置 registration port 时的默认值。
	DefaultAgentRegistrationPort = 1
)

// PreparedAgentCard 是一张经过校验、可直接用于实例注册的 AgentCard 快照。
type PreparedAgentCard struct {
	Card       *AgentCard
	Payload    string
	A2AVersion string
	Host       string
}

// PrepareAgentCard 按显式 schema 版本解析并校验一张 AgentCard。
func PrepareAgentCard(cardJSON, a2aVersion string) (*PreparedAgentCard, error) {
	card, err := ParseAgentCardByVersion(cardJSON, a2aVersion)
	if err != nil {
		return nil, err
	}
	host, err := card.Host()
	if err != nil {
		return nil, err
	}
	return &PreparedAgentCard{
		Card:       card,
		Payload:    cardJSON,
		A2AVersion: card.SchemaVersion,
		Host:       host,
	}, nil
}

// AgentInstanceRegistrationRequest 描述一张 AgentCard 对应的实例注册输入。
type AgentInstanceRegistrationRequest struct {
	Namespace string
	Service   string
	CardJSON  string
	// A2AVersion 是 AgentCard schema 版本，支持 0.3/0.3.0/1.0/1.0.0。
	A2AVersion string
	// RegistrationPort 为 nil 时使用 DefaultAgentRegistrationPort；显式 0 会失败。
	RegistrationPort *int
}

// AgentInstanceRegistration 是一张 AgentCard 对应的原子实例注册快照。
//
// Metadata、Version、Port、Protocol 和 Host 必须来自同一次 Build 调用，调用方不得
// 分别从多个 getter 重新拼装后注册。
type AgentInstanceRegistration struct {
	Namespace  string
	Service    string
	Host       string
	Port       int
	Version    string
	Protocol   string
	Metadata   map[string]string
	A2AVersion string
	Card       *AgentCard
}

// NormalizeAgentRegistrationPort 校验并返回 registration port。
func NormalizeAgentRegistrationPort(port *int) (int, error) {
	if port == nil {
		return DefaultAgentRegistrationPort, nil
	}
	if *port < 1 || *port > 65535 {
		return 0, fmt.Errorf("A2A registration port must be in range 1..65535")
	}
	return *port, nil
}

// BuildAgentInstanceRegistration 构建一张 AgentCard 对应的唯一实例注册快照。
func BuildAgentInstanceRegistration(req AgentInstanceRegistrationRequest) (*AgentInstanceRegistration, error) {
	if strings.TrimSpace(req.Namespace) == "" {
		return nil, polarismodel.NewSDKError(polarismodel.ErrCodeAPIInvalidArgument, nil,
			"AgentInstanceRegistration namespace must not be empty")
	}
	if strings.TrimSpace(req.Service) == "" {
		return nil, polarismodel.NewSDKError(polarismodel.ErrCodeAPIInvalidArgument, nil,
			"AgentInstanceRegistration service must not be empty")
	}
	prepared, err := PrepareAgentCard(req.CardJSON, req.A2AVersion)
	if err != nil {
		return nil, polarismodel.NewSDKError(polarismodel.ErrCodeAPIInvalidArgument, err, "invalid AgentCard")
	}
	if prepared.Card.Name != req.Service {
		return nil, polarismodel.NewSDKError(polarismodel.ErrCodeAPIInvalidArgument, nil,
			"AgentCard name %q must match service %q", prepared.Card.Name, req.Service)
	}
	port, err := NormalizeAgentRegistrationPort(req.RegistrationPort)
	if err != nil {
		return nil, polarismodel.NewSDKError(polarismodel.ErrCodeAPIInvalidArgument, err, "invalid A2A registration port")
	}
	metadata := map[string]string{
		MetadataAgentName:  req.Service,
		MetadataAgentCard:  prepared.Payload,
		MetadataA2AVersion: prepared.A2AVersion,
	}
	if err := ValidateAgentInstanceMetadata(metadata); err != nil {
		return nil, err
	}
	return &AgentInstanceRegistration{
		Namespace:  req.Namespace,
		Service:    req.Service,
		Host:       prepared.Host,
		Port:       port,
		Version:    prepared.Card.Version,
		Protocol:   "",
		Metadata:   metadata,
		A2AVersion: prepared.A2AVersion,
		Card:       cloneAgentCardModel(prepared.Card),
	}, nil
}

// BuildAgentInstanceRegistrationFromMetadata converts canonical three-key
// static instance metadata to a validated registration snapshot. The card JSON
// bytes are preserved, so noncanonical signed cards are not rewritten here.
func BuildAgentInstanceRegistrationFromMetadata(
	req AgentInstanceRegistrationRequest,
	metadata map[string]string,
) (*AgentInstanceRegistration, error) {
	if req.CardJSON != "" {
		return nil, fmt.Errorf("CardJSON must not be supplied together with static metadata")
	}
	if _, exists := metadata[MetadataAgentCard]; !exists {
		return nil, fmt.Errorf("AgentCard metadata requires %q", MetadataAgentCard)
	}
	if err := ValidateAgentInstanceMetadata(metadata); err != nil {
		return nil, err
	}
	version := metadata[MetadataA2AVersion]
	if req.A2AVersion != "" {
		configured, err := NormalizeAgentCardSchemaVersion(req.A2AVersion)
		if err != nil {
			return nil, err
		}
		if configured != version {
			return nil, fmt.Errorf("static AgentCard schema version does not match configured A2AVersion")
		}
	}
	req.CardJSON, req.A2AVersion = metadata[MetadataAgentCard], version
	if metadata[MetadataAgentName] != req.Service {
		return nil, fmt.Errorf("agent metadata name must match registered service")
	}
	return BuildAgentInstanceRegistration(req)
}

// NewInstanceRegisterRequest 将注册快照转换为 Polaris 实例注册请求。
//
// 返回的 Protocol 显式指向空字符串，Version 使用 AgentCard.version。
func (r *AgentInstanceRegistration) NewInstanceRegisterRequest() *polarismodel.InstanceRegisterRequest {
	if r == nil {
		return nil
	}
	protocol := r.Protocol
	version := r.Version
	metadata := make(map[string]string, len(r.Metadata))
	for key, value := range r.Metadata {
		metadata[key] = value
	}
	return &polarismodel.InstanceRegisterRequest{
		Namespace: r.Namespace,
		Service:   r.Service,
		Host:      r.Host,
		Port:      r.Port,
		Protocol:  &protocol,
		Version:   &version,
		Metadata:  metadata,
	}
}

func cloneAgentCardModel(card *AgentCard) *AgentCard {
	if card == nil {
		return nil
	}
	result := *card
	if card.Provider != nil {
		provider := *card.Provider
		result.Provider = &provider
	}
	result.Capabilities.Extensions = cloneAgentCapabilityExtensions(card.Capabilities.Extensions)
	if card.SecuritySchemes != nil {
		result.SecuritySchemes = cloneRawMessageMap(card.SecuritySchemes)
	}
	result.SecurityRequirements = cloneAgentRawMessage(card.SecurityRequirements)
	result.DefaultInputModes = append([]string(nil), card.DefaultInputModes...)
	result.DefaultOutputModes = append([]string(nil), card.DefaultOutputModes...)
	result.Skills = cloneAgentSkills(card.Skills)
	result.Signatures = cloneAgentCardSignatures(card.Signatures)
	result.SupportedInterfaces = append([]AgentInterface(nil), card.SupportedInterfaces...)
	result.AdditionalInterfaces = append([]AgentInterface(nil), card.AdditionalInterfaces...)
	result.Security = cloneAgentRawMessage(card.Security)
	if card.UnknownFields != nil {
		result.UnknownFields = cloneRawMessageMap(card.UnknownFields)
	}
	return &result
}

func cloneAgentCapabilityExtensions(values []AgentCapabilityExtension) []AgentCapabilityExtension {
	if values == nil {
		return nil
	}
	result := make([]AgentCapabilityExtension, len(values))
	for index, value := range values {
		value.Params = cloneAgentRawMessage(value.Params)
		result[index] = value
	}
	return result
}

func cloneAgentSkills(skills []polarismodel.AgentSkill) []polarismodel.AgentSkill {
	if skills == nil {
		return nil
	}
	result := make([]polarismodel.AgentSkill, len(skills))
	for index, skill := range skills {
		skill.Tags = append([]string(nil), skill.Tags...)
		skill.Examples = append([]string(nil), skill.Examples...)
		skill.InputModes = append([]string(nil), skill.InputModes...)
		skill.OutputModes = append([]string(nil), skill.OutputModes...)
		skill.Security = cloneAgentRawMessage(skill.Security)
		skill.SecurityRequirements = cloneAgentRawMessage(skill.SecurityRequirements)
		result[index] = skill
	}
	return result
}

func cloneAgentCardSignatures(signatures []AgentCardSignature) []AgentCardSignature {
	if signatures == nil {
		return nil
	}
	result := make([]AgentCardSignature, len(signatures))
	for index, signature := range signatures {
		signature.Header = cloneAgentRawMessage(signature.Header)
		result[index] = signature
	}
	return result
}

func cloneAgentRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
