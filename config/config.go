/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package config defines the Polaris AI Registry adapter configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/a2apath"
	adaptererrors "trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/errors"
)

const (
	defaultA2APath          = "/a2a"
	defaultTransport        = "JSONRPC"
	defaultRequestTimeout   = 5 * time.Second
	defaultRegistryTTL      = 5 * time.Second
	defaultRegistrationPort = 1
	defaultA2AVersion       = "1.0"
	defaultSkillListLimit   = 100
	defaultSkillMaxSkills   = 1000
)

const (
	// A2AVersionV03 identifies the A2A v0.3 AgentCard schema.
	A2AVersionV03 = "0.3"
	// A2AVersionV10 identifies the A2A v1.0 AgentCard schema.
	A2AVersionV10 = "1.0"
)

const (
	// SkillModeNames installs only explicitly named Skill references.
	SkillModeNames = "names"
	// SkillModePublished lists and installs published Skills.
	SkillModePublished = "published"
	// SkillModeMounted installs Skills mounted on the Agent service.
	SkillModeMounted = "mounted"
)

// SkillRef identifies one published Polaris Skill version.
type SkillRef struct {
	Namespace string `json:"namespace" yaml:"namespace"`
	Name      string `json:"name" yaml:"name"`
	Version   string `json:"version" yaml:"version"`
}

// ID returns the canonical namespace:name@version identifier.
func (r SkillRef) ID() string {
	return fmt.Sprintf("%s:%s@%s", r.Namespace, r.Name, r.Version)
}

// Config configures the Polaris AI Registry adapter.
type Config struct {
	Namespace             string        `json:"namespace" yaml:"namespace"`
	ServiceName           string        `json:"serviceName" yaml:"serviceName"`
	AgentName             string        `json:"agentName" yaml:"agentName"`
	AgentVersion          string        `json:"agentVersion" yaml:"agentVersion"`
	Description           string        `json:"description" yaml:"description"`
	Scheme                string        `json:"scheme" yaml:"scheme"`
	AdvertiseHost         string        `json:"advertiseHost" yaml:"advertiseHost"`
	AdvertisePort         int           `json:"advertisePort" yaml:"advertisePort"`
	RegistrationPort      int           `json:"registrationPort" yaml:"registrationPort"`
	A2AVersion            string        `json:"a2aVersion" yaml:"a2aVersion"`
	A2APath               string        `json:"a2aPath" yaml:"a2aPath"`
	Transport             string        `json:"transport" yaml:"transport"`
	Streaming             bool          `json:"streaming" yaml:"streaming"`
	RegistryEnabled       bool          `json:"registryEnabled" yaml:"registryEnabled"`
	RegistryTTL           time.Duration `json:"registryTTL" yaml:"registryTTL"`
	DiscoveryEnabled      bool          `json:"discoveryEnabled" yaml:"discoveryEnabled"`
	SkillCacheDir         string        `json:"skillCacheDir" yaml:"skillCacheDir"`
	SkillMode             string        `json:"skillMode" yaml:"skillMode"`
	SkillRefs             []SkillRef    `json:"skillRefs" yaml:"skillRefs"`
	SkillListLimit        int           `json:"skillListLimit" yaml:"skillListLimit"`
	SkillMaxSkills        int           `json:"skillMaxSkills" yaml:"skillMaxSkills"`
	SkillListRefresh      time.Duration `json:"skillListRefreshInterval" yaml:"skillListRefreshInterval"`
	SkillFailureThreshold int           `json:"skillFailureThreshold" yaml:"skillFailureThreshold"`
	SkillCacheMaxAge      time.Duration `json:"skillCacheMaxAge" yaml:"skillCacheMaxAge"`
	MountSkills           bool          `json:"mountSkills" yaml:"mountSkills"`
	MountSkillsReplace    bool          `json:"mountSkillsReplace" yaml:"mountSkillsReplace"`
	UseServiceSkills      bool          `json:"useServiceSkills" yaml:"useServiceSkills"`
	EnsureService         bool          `json:"ensureService" yaml:"ensureService"`
	PolarisAddresses      []string      `json:"polarisAddresses" yaml:"polarisAddresses"`
	PolarisToken          string        `json:"polarisToken" yaml:"polarisToken"`
	PolarisRequestTimeout time.Duration `json:"polarisRequestTimeout" yaml:"polarisRequestTimeout"`
}

// DefaultConfig returns the adapter defaults.
func DefaultConfig() Config {
	return Config{
		A2APath:               defaultA2APath,
		A2AVersion:            defaultA2AVersion,
		RegistrationPort:      defaultRegistrationPort,
		Transport:             defaultTransport,
		Scheme:                "http",
		RegistryEnabled:       true,
		RegistryTTL:           defaultRegistryTTL,
		DiscoveryEnabled:      true,
		SkillListLimit:        defaultSkillListLimit,
		SkillMaxSkills:        defaultSkillMaxSkills,
		MountSkillsReplace:    true,
		PolarisRequestTimeout: defaultRequestTimeout,
	}
}

// Validate validates provider-independent configuration.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Namespace) == "" {
		return fmt.Errorf("%w: namespace is required", adaptererrors.ErrInvalidConfig)
	}
	if strings.TrimSpace(c.ServiceName) == "" {
		return fmt.Errorf("%w: service name is required", adaptererrors.ErrInvalidConfig)
	}
	if c.AgentName != "" && c.AgentName != c.ServiceName {
		return fmt.Errorf(
			"%w: agent name %q must match service name %q",
			adaptererrors.ErrInvalidConfig,
			c.AgentName,
			c.ServiceName,
		)
	}
	if c.AdvertisePort < 0 || c.AdvertisePort > 65535 {
		return fmt.Errorf(
			"%w: advertise port %d is invalid",
			adaptererrors.ErrInvalidConfig,
			c.AdvertisePort,
		)
	}
	if c.RegistrationPort < 1 || c.RegistrationPort > 65535 {
		return fmt.Errorf(
			"%w: registration port %d must be in 1..65535",
			adaptererrors.ErrInvalidConfig,
			c.RegistrationPort,
		)
	}
	switch strings.TrimSpace(c.A2AVersion) {
	case A2AVersionV03, A2AVersionV10:
	default:
		return fmt.Errorf(
			"%w: ai-a2a-version %q must be %q or %q",
			adaptererrors.ErrInvalidConfig,
			c.A2AVersion,
			A2AVersionV03,
			A2AVersionV10,
		)
	}
	if err := a2apath.Validate(c.A2APath); err != nil {
		return fmt.Errorf("%w: %v", adaptererrors.ErrInvalidConfig, err)
	}
	if c.PolarisRequestTimeout < 0 {
		return fmt.Errorf("%w: polaris request timeout must not be negative", adaptererrors.ErrInvalidConfig)
	}
	if c.RegistryTTL <= 0 {
		return fmt.Errorf("%w: registry TTL must be positive", adaptererrors.ErrInvalidConfig)
	}
	if strings.ToUpper(strings.TrimSpace(c.Transport)) != defaultTransport {
		return fmt.Errorf(
			"%w: transport %q is not supported",
			adaptererrors.ErrInvalidConfig,
			c.Transport,
		)
	}
	switch strings.ToLower(strings.TrimSpace(c.Scheme)) {
	case "", "http", "https":
	default:
		return fmt.Errorf(
			"%w: URL scheme %q is not supported",
			adaptererrors.ErrInvalidConfig,
			c.Scheme,
		)
	}
	if c.AdvertiseHost != "" {
		if _, _, err := net.SplitHostPort(c.AdvertiseHost); err == nil {
			return fmt.Errorf(
				"%w: advertise host %q must not include a port",
				adaptererrors.ErrInvalidConfig,
				c.AdvertiseHost,
			)
		}
	}
	if c.SkillCacheDir != "" {
		if err := validateWritableDirectory(c.SkillCacheDir); err != nil {
			return fmt.Errorf(
				"%w: skill cache directory: %v",
				adaptererrors.ErrInvalidConfig,
				err,
			)
		}
	}
	for _, ref := range c.SkillRefs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("%w: %v", adaptererrors.ErrInvalidConfig, err)
		}
	}
	switch strings.TrimSpace(c.SkillMode) {
	case "", SkillModeNames, SkillModePublished, SkillModeMounted:
	default:
		return fmt.Errorf("%w: Skill mode %q is not supported", adaptererrors.ErrInvalidConfig, c.SkillMode)
	}
	if c.SkillListLimit <= 0 || c.SkillListLimit > 1000 {
		return fmt.Errorf("%w: Skill list limit must be in 1..1000", adaptererrors.ErrInvalidConfig)
	}
	if c.SkillMaxSkills <= 0 || c.SkillMaxSkills > 10000 {
		return fmt.Errorf("%w: Skill max skills must be in 1..10000", adaptererrors.ErrInvalidConfig)
	}
	if c.SkillListRefresh < 0 || c.SkillCacheMaxAge < 0 {
		return fmt.Errorf("%w: Skill refresh and cache max age must not be negative", adaptererrors.ErrInvalidConfig)
	}
	if c.SkillFailureThreshold < 0 {
		return fmt.Errorf("%w: Skill failure threshold must not be negative", adaptererrors.ErrInvalidConfig)
	}
	return nil
}

// ValidateProvider validates the fields required by provider mode.
func (c Config) ValidateProvider() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.AgentVersion == "" {
		return fmt.Errorf(
			"%w: agent version is required for provider mode",
			adaptererrors.ErrInvalidConfig,
		)
	}
	if c.AdvertiseHost == "" {
		return fmt.Errorf(
			"%w: advertise host is required for provider mode",
			adaptererrors.ErrInvalidConfig,
		)
	}
	if c.AdvertisePort == 0 {
		return fmt.Errorf(
			"%w: advertise port is required for provider mode",
			adaptererrors.ErrInvalidConfig,
		)
	}
	return nil
}

// Normalize trims fields and fills defaults without mutating the receiver.
func (c Config) Normalize() Config {
	c = c.Clone()
	c.Namespace = strings.TrimSpace(c.Namespace)
	c.ServiceName = strings.TrimSpace(c.ServiceName)
	c.AgentName = strings.TrimSpace(c.AgentName)
	c.AgentVersion = strings.TrimSpace(c.AgentVersion)
	c.AdvertiseHost = strings.Trim(strings.TrimSpace(c.AdvertiseHost), "[]")
	c.A2AVersion = strings.TrimSpace(c.A2AVersion)
	c.A2APath = strings.TrimSpace(c.A2APath)
	c.Transport = strings.TrimSpace(c.Transport)
	c.Scheme = strings.TrimSpace(c.Scheme)
	c.SkillMode = strings.TrimSpace(c.SkillMode)
	c.SkillCacheDir = strings.TrimSpace(c.SkillCacheDir)
	for index := range c.SkillRefs {
		c.SkillRefs[index] = c.SkillRefs[index].Normalize()
	}
	if c.A2APath == "" {
		c.A2APath = defaultA2APath
	}
	if c.A2AVersion == "" {
		c.A2AVersion = defaultA2AVersion
	}
	if c.Transport == "" {
		c.Transport = defaultTransport
	}
	if c.Scheme == "" {
		c.Scheme = "http"
	}
	if c.PolarisRequestTimeout == 0 {
		c.PolarisRequestTimeout = defaultRequestTimeout
	}
	if c.RegistryTTL == 0 {
		c.RegistryTTL = defaultRegistryTTL
	}
	if c.SkillListLimit == 0 {
		c.SkillListLimit = defaultSkillListLimit
	}
	if c.SkillMaxSkills == 0 {
		c.SkillMaxSkills = defaultSkillMaxSkills
	}
	if c.SkillMode == "" {
		switch {
		case c.UseServiceSkills:
			c.SkillMode = SkillModeMounted
		case len(c.SkillRefs) > 0:
			c.SkillMode = SkillModeNames
		}
	}
	return c
}

// Clone returns a deep copy of slice fields.
func (c Config) Clone() Config {
	c.SkillRefs = append([]SkillRef(nil), c.SkillRefs...)
	c.PolarisAddresses = append([]string(nil), c.PolarisAddresses...)
	return c
}

// Validate validates one Skill reference.
func (r SkillRef) Validate() error {
	if err := model.ValidateSkillRefParts(r.Namespace, r.Name, r.Version); err != nil {
		return err
	}
	if strings.ContainsAny(r.Namespace+r.Name+r.Version, ":@") {
		return errors.New("skill reference fields must not contain : or @")
	}
	return nil
}

// Normalize trims all reference fields.
func (r SkillRef) Normalize() SkillRef {
	r.Namespace = strings.TrimSpace(r.Namespace)
	r.Name = strings.TrimSpace(r.Name)
	r.Version = strings.TrimSpace(r.Version)
	return r
}

func validateWritableDirectory(directory string) error {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(absolute, ".polaris-write-check-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
