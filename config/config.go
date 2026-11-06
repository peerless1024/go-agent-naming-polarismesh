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
	defaultA2APath        = "/a2a"
	defaultTransport      = "JSONRPC"
	defaultRequestTimeout = 5 * time.Second
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
	A2APath               string        `json:"a2aPath" yaml:"a2aPath"`
	Transport             string        `json:"transport" yaml:"transport"`
	Streaming             bool          `json:"streaming" yaml:"streaming"`
	SkillCacheDir         string        `json:"skillCacheDir" yaml:"skillCacheDir"`
	SkillRefs             []SkillRef    `json:"skillRefs" yaml:"skillRefs"`
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
		Transport:             defaultTransport,
		Scheme:                "http",
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
	if err := a2apath.Validate(c.A2APath); err != nil {
		return fmt.Errorf("%w: %v", adaptererrors.ErrInvalidConfig, err)
	}
	if c.PolarisRequestTimeout < 0 {
		return fmt.Errorf("%w: polaris request timeout must not be negative", adaptererrors.ErrInvalidConfig)
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
	c.A2APath = strings.TrimSpace(c.A2APath)
	c.Transport = strings.TrimSpace(c.Transport)
	c.Scheme = strings.TrimSpace(c.Scheme)
	c.SkillCacheDir = strings.TrimSpace(c.SkillCacheDir)
	for index := range c.SkillRefs {
		c.SkillRefs[index] = c.SkillRefs[index].Normalize()
	}
	if c.A2APath == "" {
		c.A2APath = defaultA2APath
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
