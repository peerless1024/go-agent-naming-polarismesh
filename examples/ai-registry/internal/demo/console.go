/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/polarismesh/polaris-go/pkg/config"
)

// Console performs fixture writes and independent server-side assertions.
type Console struct {
	origin, token string
	http          *http.Client
	transport     *http.Transport
}

// ServiceView is the non-sensitive Console projection used by the suite.
type ServiceView struct {
	Name, Namespace      string
	ServiceType          json.RawMessage   `json:"service_type"`
	Metadata             map[string]string `json:"metadata"`
	ExtendedMetadata     []json.RawMessage `json:"extended_metadata"`
	TotalInstanceCount   int               `json:"total_instance_count"`
	HealthyInstanceCount int               `json:"healthy_instance_count"`
}

// InstanceView includes only identity, health and metadata, never service tokens.
type InstanceView struct {
	ID, Namespace, Service, Host string
	Port                         int
	Healthy, Isolate             bool
	Metadata                     map[string]string
}

// Envelope deliberately excludes token fields returned by Console APIs.
type Envelope struct {
	Code      uint32 `json:"code"`
	Responses []struct {
		Code uint32 `json:"code"`
	} `json:"responses"`
	Namespaces []struct{ Name string } `json:"namespaces"`
	Services   []ServiceView           `json:"services"`
	Instances  []InstanceView          `json:"instances"`
	Resource   *struct {
		Name, Namespace string
		Ext             map[string]string `json:"ext"`
	} `json:"resource"`
	Version *struct{ Version, Status string } `json:"version"`
}

// NewConsole borrows configuration only to read its token in memory.
func NewConsole(o Options, cfg config.Configuration) *Console {
	token := Environment("POLARIS_CONSOLE_TOKEN", "POLARIS_TOKEN", "POLARIS_SERVICE_TOKEN")
	if token == "" && cfg != nil {
		token = cfg.GetServiceWriter().GetToken()
	}
	client, transport := HTTPClient(o.Timeout)
	return &Console{origin: strings.TrimRight(o.Console, "/"), token: token, http: client, transport: transport}
}

// Close releases idle connections owned by this fixture client.
func (c *Console) Close() { c.transport.CloseIdleConnections() }

// Request checks both the envelope and every batch result.
func (c *Console) Request(ctx context.Context, method, path string, payload any) (*Envelope, error) {
	var data []byte
	if payload != nil {
		var err error
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cannot construct Console request")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("X-Polaris-Token", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Console %s %s transport failed: %w", method, strings.Split(path, "?")[0], err)
	}
	defer func() { _ = resp.Body.Close() }()
	var envelope Envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("Console %s %s: HTTP %d malformed response", method, strings.Split(path, "?")[0], resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (envelope.Code != 200000 && envelope.Code != 200002) {
		return &envelope, fmt.Errorf("Console %s %s: HTTP %d code=%d", method, strings.Split(path, "?")[0], resp.StatusCode, envelope.Code)
	}
	for _, result := range envelope.Responses {
		if result.Code != 200000 && result.Code != 200002 {
			return &envelope, fmt.Errorf("Console %s %s item code=%d", method, strings.Split(path, "?")[0], result.Code)
		}
	}
	return &envelope, nil
}

// EnsureNamespace never changes or deletes an existing namespace.
func (c *Console) EnsureNamespace(ctx context.Context, name string) error {
	result, err := c.Request(ctx, http.MethodGet, "/naming/v1/namespaces?"+url.Values{"name": {name}}.Encode(), nil)
	if err != nil {
		return err
	}
	for _, item := range result.Namespaces {
		if item.Name == name {
			return nil
		}
	}
	_, err = c.Request(ctx, http.MethodPost, "/naming/v1/namespaces", []map[string]string{{"name": name, "comment": Owner}})
	return err
}

// Service returns one exact service for ownership verification.
func (c *Console) Service(ctx context.Context, namespace, name string) (*ServiceView, error) {
	result, err := c.Request(ctx, http.MethodGet, "/naming/v1/services?"+url.Values{"namespace": {namespace}, "name": {name}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	for index := range result.Services {
		item := &result.Services[index]
		if item.Namespace == namespace && item.Name == name {
			return item, nil
		}
	}
	return nil, nil
}

// Services returns one page of Console service projections for exact filtering.
func (c *Console) Services(ctx context.Context, namespace string) ([]ServiceView, error) {
	query := url.Values{"namespace": {namespace}, "offset": {"0"}, "limit": {"1000"}}
	result, err := c.Request(ctx, http.MethodGet, "/naming/v1/services?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return result.Services, nil
}

// Instances independently reads server-side instance state.
func (c *Console) Instances(ctx context.Context, namespace, service string) ([]InstanceView, error) {
	result, err := c.Request(ctx, http.MethodGet, "/naming/v1/instances?"+url.Values{"namespace": {namespace}, "service": {service}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return result.Instances, nil
}

// CreateSkill creates a unique per-run resource or adds a version to it.
func (c *Console) CreateSkill(ctx context.Context, o Options, name, version string, publish, extraEntry bool) error {
	type file struct {
		Path        string `json:"path"`
		Content     []byte `json:"content"`
		ContentType string `json:"contentType"`
	}
	files := []file{
		{Path: "SKILL.md", ContentType: "text", Content: []byte(fmt.Sprintf(
			"---\nname: %s\ndescription: Deterministic Polaris A2A integration fixture\n---\n\nPOLARIS_SKILL_VERSION=%s\nAnswer with this exact installed Skill content.\n", name, version))},
		{Path: "references/data.json", ContentType: "text", Content: []byte(`{"source":"real-polaris-skill-registry","value":42}`)},
	}
	if extraEntry {
		files = append(files, file{Path: "extra/SKILL.md", ContentType: "text", Content: []byte("---\nname: unselected\ndescription: Unselected entry\n---\nMust never become executable.\n")})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	_, err := c.Request(ctx, http.MethodPost, "/skill/v1/skills", map[string]any{
		"namespace": o.Namespace, "name": name, "version": version,
		"description": "Per-run real A2A Skill fixture", "scope": "PRIVATE", "author": Owner,
		"tags": []string{"go-agent-naming-e2e"}, "ext": map[string]string{OwnerLabel: Owner, RunLabel: o.RunID},
		"files": files, "publish": publish,
	})
	return err
}

// Publish activates the specified fixture version.
func (c *Console) Publish(ctx context.Context, namespace, name, version string) error {
	_, err := c.Request(ctx, http.MethodPut, "/skill/v1/skills/publish", map[string]string{
		"namespace": namespace, "name": name, "version": version, "comment": "real E2E version transition",
	})
	return err
}
