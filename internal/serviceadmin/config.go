/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package serviceadmin

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

const DefaultServiceWriterTimeout = 5 * time.Second

// ServiceWriterConfig 是 Console HTTP 服务写入配置。
type ServiceWriterConfig interface {
	// GetAddresses 获取 Console HTTP 地址。
	GetAddresses() []string
	// SetAddresses 设置 Console HTTP 地址。
	SetAddresses([]string)
	// GetToken 获取 Console token。
	GetToken() string
	// SetToken 设置 Console token。
	SetToken(string)
	// GetTimeout 获取完整写操作超时，包含同服务锁等待、HTTP 读改写及确认。
	GetTimeout() time.Duration
	// SetTimeout 设置完整写操作超时。
	SetTimeout(time.Duration)
}

// ServiceWriterConfigImpl 是 ServiceWriterConfig 的默认实现。
type ServiceWriterConfigImpl struct {
	Addresses []string       `yaml:"addresses" json:"addresses"`
	Token     string         `yaml:"token" json:"token"`
	Timeout   *time.Duration `yaml:"timeout" json:"timeout"`
}

// Init 初始化配置。
func (c *ServiceWriterConfigImpl) Init() {}

// SetDefault 设置默认值。
func (c *ServiceWriterConfigImpl) SetDefault() {
	if c.Timeout == nil {
		c.Timeout = model.ToDurationPtr(DefaultServiceWriterTimeout)
	}
}

// Verify 校验配置。
func (c *ServiceWriterConfigImpl) Verify() error {
	if c == nil {
		return fmt.Errorf("serviceWriter config is nil")
	}
	if c.Timeout == nil || *c.Timeout <= 0 {
		return fmt.Errorf("serviceWriter.timeout must be positive")
	}
	for _, address := range c.Addresses {
		if err := validateServiceWriterAddress(address); err != nil {
			return err
		}
	}
	return nil
}

func validateServiceWriterAddress(address string) error {
	if address == "" {
		return fmt.Errorf("serviceWriter.addresses contains an empty value")
	}
	if strings.TrimSpace(address) != address {
		return fmt.Errorf("serviceWriter address %q must not contain surrounding whitespace", address)
	}
	if !strings.Contains(address, "://") {
		host, portValue, err := net.SplitHostPort(address)
		if err != nil || strings.TrimSpace(host) == "" {
			return fmt.Errorf("serviceWriter address %q must use host:port format", address)
		}
		port, portErr := strconv.Atoi(portValue)
		if portErr != nil || port <= 0 || port > 65535 {
			return fmt.Errorf("serviceWriter address %q has invalid port %q", address, portValue)
		}
		return nil
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("serviceWriter address %q must be a valid http(s) URL or host:port", address)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(address, "#") ||
		parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf(
			"serviceWriter address %q must not contain a query, fragment, or opaque component", address)
	}
	return nil
}

// GetAddresses 获取 Console HTTP 地址。
func (c *ServiceWriterConfigImpl) GetAddresses() []string {
	if c == nil {
		return nil
	}
	return c.Addresses
}

// SetAddresses 设置 Console HTTP 地址。
func (c *ServiceWriterConfigImpl) SetAddresses(addresses []string) {
	c.Addresses = addresses
}

// GetToken 获取 Console token。
func (c *ServiceWriterConfigImpl) GetToken() string {
	if c == nil {
		return ""
	}
	return c.Token
}

// SetToken 设置 Console token。
func (c *ServiceWriterConfigImpl) SetToken(token string) {
	c.Token = token
}

// GetTimeout 获取完整写操作超时。
func (c *ServiceWriterConfigImpl) GetTimeout() time.Duration {
	return *c.Timeout
}

// SetTimeout 设置完整写操作超时。
func (c *ServiceWriterConfigImpl) SetTimeout(timeout time.Duration) {
	c.Timeout = model.ToDurationPtr(timeout)
}
