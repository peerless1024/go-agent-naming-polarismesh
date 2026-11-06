/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "missing namespace", mutate: func(c *Config) { c.Namespace = "" }, wantErr: true},
		{name: "missing service", mutate: func(c *Config) { c.ServiceName = "" }, wantErr: true},
		{name: "agent mismatch", mutate: func(c *Config) { c.AgentName = "other" }, wantErr: true},
		{name: "invalid port", mutate: func(c *Config) { c.AdvertisePort = 65536 }, wantErr: true},
		{name: "invalid path", mutate: func(c *Config) { c.A2APath = "a2a" }, wantErr: true},
		{name: "parent path", mutate: func(c *Config) { c.A2APath = "/a2a/../admin" }, wantErr: true},
		{name: "encoded parent path", mutate: func(c *Config) { c.A2APath = "/a2a/%2e%2e/admin" }, wantErr: true},
		{name: "dot path", mutate: func(c *Config) { c.A2APath = "/a2a/./rpc" }, wantErr: true},
		{name: "empty path segment", mutate: func(c *Config) { c.A2APath = "/a2a//rpc" }, wantErr: true},
		{name: "mux wildcard", mutate: func(c *Config) { c.A2APath = "/a2a/{id}/{id}" }, wantErr: true},
		{name: "encoded path", mutate: func(c *Config) { c.A2APath = "/a2a/%20" }, wantErr: true},
		{name: "negative timeout", mutate: func(c *Config) { c.PolarisRequestTimeout = -1 }, wantErr: true},
		{name: "unsupported transport", mutate: func(c *Config) { c.Transport = "GRPC" }, wantErr: true},
		{name: "unsupported scheme", mutate: func(c *Config) { c.Scheme = "ftp" }, wantErr: true},
		{name: "skill without version", mutate: func(c *Config) {
			c.SkillRefs = []SkillRef{{Namespace: "Production", Name: "demo"}}
		}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := providerConfig()
			test.mutate(&config)
			err := config.Validate()
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSkillRefValidate(t *testing.T) {
	require.NoError(t, SkillRef{Namespace: "Production", Name: "weather", Version: "1.0.0"}.Validate())
	require.Error(t, SkillRef{Namespace: "Production", Name: "weather"}.Validate())
	require.Error(t, SkillRef{Namespace: "Production", Name: "weather", Version: "1/2"}.Validate())
}

func TestConfigNormalizeDoesNotMutateOriginalSlice(t *testing.T) {
	config := providerConfig()
	config.SkillRefs = []SkillRef{{Namespace: " Production ", Name: " weather ", Version: " 1.0.0 "}}
	normalized := config.Normalize()
	require.Equal(t, " Production ", config.SkillRefs[0].Namespace)
	require.Equal(t, "Production", normalized.SkillRefs[0].Namespace)
}

func providerConfig() Config {
	return Config{
		Namespace:     "Production",
		ServiceName:   "customer-agent",
		AgentName:     "customer-agent",
		AgentVersion:  "1.2.3",
		AdvertiseHost: "127.0.0.1",
		AdvertisePort: 18080,
		A2APath:       "/a2a",
		Transport:     "JSONRPC",
	}
}
