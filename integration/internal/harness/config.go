/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package harness contains shared real-Polaris fixtures for integration tests.
package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	polarisconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/internal/serviceadmin"
)

const (
	owner     = "trpc-agent-naming-polarismesh-e2e"
	ownerKey  = "examples.owner"
	runIDKey  = "examples.run.id"
	skillType = "skill"
)

// Config is the real Polaris endpoint and identity for one test process.
type Config struct {
	Server         string
	GRPCPort       int
	ConsolePort    int
	SkillPort      int
	Namespace      string
	RunID          string
	Token          string
	ConsoleToken   string
	SkillToken     string
	ServiceToken   string
	RequestTimeout time.Duration
	http           *http.Client
}

// Load reads the required Polaris environment, creates the shared namespace,
// and skips the test when POLARIS_SERVER is absent.
func Load(t *testing.T) *Config {
	t.Helper()
	server := strings.TrimSpace(os.Getenv("POLARIS_SERVER"))
	if server == "" {
		t.Skip("POLARIS_SERVER is required for real integration tests")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	cfg := &Config{
		Server:         server,
		GRPCPort:       envInt("POLARIS_GRPC_PORT", 8091),
		ConsolePort:    envInt("POLARIS_HTTP_PORT", 8090),
		SkillPort:      envInt("POLARIS_SKILL_GRPC_PORT", 8094),
		Namespace:      envString("POLARIS_E2E_NAMESPACE", "go-agent-naming-e2e"),
		RunID:          envString("POLARIS_E2E_RUN_ID", newRunID()),
		Token:          os.Getenv("POLARIS_TOKEN"),
		ConsoleToken:   firstEnv("POLARIS_CONSOLE_TOKEN", "POLARIS_TOKEN"),
		SkillToken:     firstEnv("POLARIS_SKILL_TOKEN", "POLARIS_TOKEN"),
		ServiceToken:   firstEnv("POLARIS_SERVICE_TOKEN", "POLARIS_TOKEN"),
		RequestTimeout: envDuration("POLARIS_E2E_TIMEOUT", 30*time.Second),
		http: &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	if err := cfg.ensureNamespace(context.Background()); err != nil {
		t.Fatalf("ensure namespace %s: %v", cfg.Namespace, err)
	}
	t.Logf("Polaris integration run: server=%s namespace=%s run=%s",
		cfg.Server, cfg.Namespace, cfg.RunID)
	return cfg
}

// DiscoverAddress returns the Polaris gRPC endpoint.
func (c *Config) DiscoverAddress() string {
	return fmt.Sprintf("%s:%d", c.Server, c.GRPCPort)
}

// ConsoleAddress returns the Polaris Console HTTP origin.
func (c *Config) ConsoleAddress() string {
	return fmt.Sprintf("http://%s:%d", c.Server, c.ConsolePort)
}

// SkillAddress returns the Polaris Skill gRPC endpoint.
func (c *Config) SkillAddress() string {
	return fmt.Sprintf("%s:%d", c.Server, c.SkillPort)
}

// Unique returns a service or Skill name scoped to this run.
func (c *Config) Unique(prefix string) string {
	return fmt.Sprintf("gan-e2e-%s-%s", c.RunID, prefix)
}

// SDK creates one Polaris SDK context. Callers own and destroy it.
func (c *Config) SDK(enableSkill bool) (api.SDKContext, error) {
	cfg := polarisconfig.NewDefaultConfiguration([]string{c.DiscoverAddress()})
	cfg.GetGlobal().GetAPI().SetTimeout(c.RequestTimeout)
	cfg.GetGlobal().GetAPI().SetMaxRetryTimes(0)
	cfg.GetGlobal().GetAdmin().SetPort(0)
	cfg.GetGlobal().GetStatReporter().SetEnable(false)
	cfg.GetConsumer().GetLocalCache().SetPersistEnable(false)
	if c.Token != "" {
		cfg.GetGlobal().GetServerConnector().SetToken(c.Token)
	}
	if enableSkill {
		cfg.GetSkill().SetEnable(true)
		cfg.GetSkill().SetAddresses([]string{c.SkillAddress()})
		cfg.GetSkill().SetConnectTimeout(c.RequestTimeout)
		cfg.GetSkill().SetMessageTimeout(c.RequestTimeout)
		cfg.GetSkill().SetPersistEnable(false)
		cfg.GetSkill().SetFallbackToLocalCache(false)
		if c.SkillToken != "" {
			cfg.GetSkill().SetToken(c.SkillToken)
		}
	}
	return api.InitContextByConfig(cfg)
}

// Admin returns a Console service writer. The caller must close it.
func (c *Config) Admin() (*serviceadmin.Client, error) {
	timeout := c.RequestTimeout
	cfg := &serviceadmin.ServiceWriterConfigImpl{
		Addresses: []string{c.ConsoleAddress()},
		Token:     c.ConsoleToken,
		Timeout:   &timeout,
	}
	cfg.SetDefault()
	if err := cfg.Verify(); err != nil {
		return nil, err
	}
	return serviceadmin.New(cfg), nil
}

// EnsureService creates a service owned by this integration run.
func (c *Config) EnsureService(service string, serviceType model.ServiceType) error {
	admin, err := c.Admin()
	if err != nil {
		return err
	}
	defer admin.Close()
	metadata := map[string]string{
		ownerKey: owner,
		runIDKey: c.RunID,
	}
	_, err = admin.EnsureService(&serviceadmin.EnsureServiceRequest{
		ServiceWriteRequest: serviceadmin.ServiceWriteRequest{
			Namespace: c.Namespace,
			Service:   service,
			Type:      serviceType,
			Metadata:  &metadata,
		},
	})
	return err
}

// DeleteService removes one service owned by this run.
func (c *Config) DeleteService(service string, serviceType model.ServiceType) error {
	admin, err := c.Admin()
	if err != nil {
		return err
	}
	defer admin.Close()
	return admin.DeleteService(&serviceadmin.DeleteServiceRequest{
		Namespace: c.Namespace,
		Service:   service,
		Type:      &serviceType,
		Force:     true,
	})
}

// CreatePublishedSkill creates and publishes a multi-file Skill, then returns
// an SDK connection verified against a Skill data-plane instance that can read
// and download it. The caller owns and must destroy the returned SDK context.
func (c *Config) CreatePublishedSkill(name, version, marker string) (api.SDKContext, error) {
	var verifiedSDK api.SDKContext
	if err := c.createPublishedSkill(name, version, marker, &verifiedSDK); err != nil {
		return nil, err
	}
	if verifiedSDK == nil {
		return nil, errors.New("verified Skill SDK is nil")
	}
	return verifiedSDK, nil
}

func (c *Config) createPublishedSkill(
	name, version, marker string,
	verifiedSDK *api.SDKContext,
) error {
	payload := map[string]any{
		"namespace":   c.Namespace,
		"name":        name,
		"version":     version,
		"description": "Polaris integration Skill",
		"scope":       "PRIVATE",
		"author":      owner,
		"tags":        []string{"trpc-agent-go", skillType},
		"ext": map[string]string{
			ownerKey: owner,
			runIDKey: c.RunID,
		},
		"files": []map[string]any{
			{
				"path":        "SKILL.md",
				"contentType": "text",
				"content": []byte(fmt.Sprintf(`---
name: %s
description: Integration Skill published to Polaris
---

# Integration Skill

When asked for the integration mark, answer exactly:

POLARIS_SKILL_MARKER=%s
`, name, marker)),
			},
			{
				"path":        "references/data.md",
				"contentType": "text",
				"content":     []byte("reference file from the real Polaris Skill package\n"),
			},
		},
	}
	query := url.Values{"namespace": {c.Namespace}, "name": {name}, "version": {version}}
	existing, err := c.consoleJSON(
		context.Background(),
		http.MethodGet,
		"/skill/v1/skills?"+query.Encode(),
		nil,
	)
	if err != nil {
		return err
	}
	alreadyPublished := false
	switch existing.Code {
	case 200000:
		if err := c.validateSkillOwnership(existing, name); err != nil {
			return err
		}
		alreadyPublished = skillVersionActive(existing, version)
	case 400202:
		response, err := c.consoleJSON(context.Background(), http.MethodPost, "/skill/v1/skills", payload)
		if err != nil {
			return err
		}
		if response.Code != 200000 && response.Code != 400201 {
			// A concurrent or previous attempt may have inserted the same
			// namespace/name/version after the preflight query.
			owned, queryErr := c.consoleJSON(
				context.Background(),
				http.MethodGet,
				"/skill/v1/skills?"+query.Encode(),
				nil,
			)
			if queryErr != nil || owned.Code != 200000 {
				return fmt.Errorf("create Skill code=%d info=%s", response.Code, response.Info)
			}
			if err := c.validateSkillOwnership(owned, name); err != nil {
				return err
			}
			alreadyPublished = skillVersionActive(owned, version)
		}
	default:
		return fmt.Errorf("query existing Skill code=%d info=%s", existing.Code, existing.Info)
	}
	if err := Eventually(context.Background(), c.RequestTimeout, 200*time.Millisecond, func() error {
		item, err := c.consoleJSON(context.Background(), http.MethodGet, "/skill/v1/skills?"+query.Encode(), nil)
		if err != nil {
			return err
		}
		if item.Code == 400202 {
			return errors.New("skill version is not visible yet")
		}
		if item.Code != 200000 {
			return fmt.Errorf("query created Skill code=%d info=%s", item.Code, item.Info)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("wait for created Skill: %w", err)
	}
	if !alreadyPublished {
		// Older Polaris servers update the Skill cache with second-granularity
		// mtime and use "mtime > last". Publishing in the same second as create
		// can leave the cache permanently on the draft state.
		time.Sleep(time.Until(skillPublishNotBefore(time.Now())))
		response, err := c.consoleJSON(context.Background(), http.MethodPut, "/skill/v1/skills/publish", map[string]string{
			"namespace": c.Namespace,
			"name":      name,
			"version":   version,
			"comment":   "integration test",
		})
		if err != nil {
			return err
		}
		if response.Code != 200000 && response.Code != 200002 {
			current, queryErr := c.consoleJSON(
				context.Background(),
				http.MethodGet,
				"/skill/v1/skills?"+query.Encode(),
				nil,
			)
			if queryErr != nil || current.Code != 200000 ||
				!skillVersionActive(current, version) {
				return fmt.Errorf("publish Skill code=%d info=%s", response.Code, response.Info)
			}
			if err := c.validateSkillOwnership(current, name); err != nil {
				return err
			}
		}
	}
	return Eventually(context.Background(), c.RequestTimeout, 250*time.Millisecond, func() error {
		// A load-balanced Skill endpoint can expose replicas with different
		// cache freshness. Keep only the SDK that can read and download the
		// published package, then reuse it for the Runtime under test.
		sdk, err := c.SDK(true)
		if err != nil {
			return err
		}
		skills := api.NewSkillAPIByContext(sdk)
		resource, err := skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
			Namespace: c.Namespace,
			Name:      name,
			Version:   version,
		}})
		if err != nil {
			sdk.Destroy()
			return err
		}
		if resource == nil || resource.Name != name || resource.Namespace != c.Namespace {
			sdk.Destroy()
			return errors.New("skill identity is not visible")
		}
		pkg, err := skills.DownloadSkill(&api.SkillDownloadRequest{SkillDownloadRequest: model.SkillDownloadRequest{
			Namespace: c.Namespace,
			Name:      name,
			Version:   version,
			Format:    model.SkillFormatZIP,
		}})
		if err != nil {
			sdk.Destroy()
			return err
		}
		if pkg == nil || len(pkg.Content) == 0 {
			sdk.Destroy()
			return errors.New("skill ZIP is not visible")
		}
		*verifiedSDK = sdk
		return nil
	})
}

func skillPublishNotBefore(now time.Time) time.Time {
	return now.Truncate(time.Second).Add(time.Second + 200*time.Millisecond)
}

func (c *Config) validateSkillOwnership(response *consoleResponse, name string) error {
	if response == nil || response.Resource == nil {
		return errors.New("existing Skill resource is missing")
	}
	resource := response.Resource
	if resource.Name != name || resource.Namespace != c.Namespace {
		return fmt.Errorf(
			"existing Skill identity is %s/%s, want %s/%s",
			resource.Namespace, resource.Name, c.Namespace, name,
		)
	}
	if resource.Ext[ownerKey] != owner || resource.Ext[runIDKey] != c.RunID {
		return fmt.Errorf(
			"existing Skill %s/%s is not owned by this run",
			c.Namespace, name,
		)
	}
	return nil
}

func skillVersionActive(response *consoleResponse, version string) bool {
	return response != nil &&
		response.Resource != nil &&
		response.Resource.VersionInfo != nil &&
		response.Resource.VersionInfo.ActiveVersion == version
}

// DeleteSkill removes every version of a Skill owned by this run.
func (c *Config) DeleteSkill(name string) error {
	query := url.Values{"namespace": {c.Namespace}, "name": {name}}
	response, err := c.consoleJSON(
		context.Background(),
		http.MethodDelete,
		"/skill/v1/skills?"+query.Encode(),
		nil,
	)
	if err != nil {
		return err
	}
	if response.Code != 200000 && response.Code != 400202 {
		return fmt.Errorf("delete Skill code=%d info=%s", response.Code, response.Info)
	}
	return nil
}

func (c *Config) ensureNamespace(ctx context.Context) error {
	response, err := c.consoleJSON(ctx, http.MethodPost, "/naming/v1/namespaces", []map[string]string{{
		"name": c.Namespace, "comment": owner,
	}})
	if err != nil {
		return err
	}
	if response.Code != 200000 && response.Code != 400201 {
		return fmt.Errorf("create namespace code=%d info=%s", response.Code, response.Info)
	}
	return nil
}

type skillResource struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Ext         map[string]string `json:"ext"`
	VersionInfo *skillVersionInfo `json:"versionInfo"`
}

type skillVersionInfo struct {
	ActiveVersion string `json:"activeVersion"`
}

type consoleResponse struct {
	Code     uint32         `json:"code"`
	Info     string         `json:"info"`
	Resource *skillResource `json:"resource"`
}

func (c *Config) consoleJSON(
	ctx context.Context,
	method, path string,
	payload any,
) (*consoleResponse, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.ConsoleAddress()+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.ConsoleToken != "" {
		request.Header.Set("X-Polaris-Token", c.ConsoleToken)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}
	var result consoleResponse
	decodeErr := json.Unmarshal(raw, &result)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if decodeErr != nil || result.Code == 0 {
			return nil, fmt.Errorf("console %s %s returned %d: %s",
				method, path, response.StatusCode, strings.TrimSpace(string(raw)))
		}
		return &result, nil
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("decode Console response: %w", decodeErr)
	}
	return &result, nil
}

func newRunID() string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(suffix[:])
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
