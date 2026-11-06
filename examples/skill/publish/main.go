/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main publishes the deterministic Skill used by the Skill demo.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
)

const (
	owner     = "trpc-agent-naming-polarismesh-demo"
	ownerKey  = "examples.owner"
	kindKey   = "examples.kind"
	skillKind = "skill"
)

var (
	server    string
	httpPort  int
	grpcPort  int
	skillAddr string
	namespace string
	name      string
	version   string
	timeout   time.Duration
)

func main() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&httpPort, "console-port", 8090, "Polaris Console port")
	flag.IntVar(&grpcPort, "polaris-port", 8091, "Polaris Discovery gRPC port")
	flag.StringVar(&skillAddr, "skill-address", "", "Polaris Skill gRPC host:port")
	flag.StringVar(&namespace, "namespace", "go-naming-polarismesh-skill-demo", "Polaris namespace")
	flag.StringVar(&name, "skill", "trpc-polaris-demo-skill", "Skill name")
	flag.StringVar(&version, "skill-version", "1.0.0", "Skill version")
	flag.DurationVar(&timeout, "timeout", 10*time.Second, "Console and Skill operation timeout")
	flag.Parse()
	if server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
	}
	if httpPort < 1 || httpPort > 65535 || grpcPort < 1 || grpcPort > 65535 {
		log.Fatal("Polaris ports must be between 1 and 65535")
	}
	if timeout <= 0 {
		log.Fatal("--timeout must be positive")
	}
	if err := publish(); err != nil {
		log.Fatal(err)
	}
}

func publish() error {
	client, origin, err := newClient()
	if err != nil {
		return err
	}
	if err := ensureNamespace(client, origin); err != nil {
		return err
	}
	existing, err := getVersion(client, origin)
	if err != nil {
		return err
	}
	if existing != nil && strings.EqualFold(existing.Status, "published") {
		log.Printf("Skill already published: %s@%s", name, version)
	} else {
		if existing == nil {
			payload := map[string]any{
				"namespace": namespace, "name": name, "version": version,
				"description": "Deterministic Polaris Skill demo",
				"scope":       "PRIVATE", "author": owner,
				"tags": []string{"trpc-agent-go", "polaris-demo"},
				"ext":  map[string]string{ownerKey: owner, kindKey: skillKind},
				"files": []map[string]any{{
					"path": "SKILL.md", "contentType": "text",
					"content": []byte(fmt.Sprintf(`---
name: %s
description: Deterministic Skill downloaded from Polaris
---

POLARIS_DEMO_SKILL=%s
This content is installed from the real Polaris Skill Registry.
`, name, version)),
				}},
			}
			response, err := request(client, http.MethodPost, origin+"/skill/v1/skills", payload)
			if err != nil {
				return err
			}
			if response.Code != 200000 && response.Code != 400201 {
				return fmt.Errorf("create Skill failed: code=%d info=%s", response.Code, response.Info)
			}
			if _, err := waitForVersion(client, origin, 5*time.Second); err != nil {
				return err
			}
		}
		response, err := request(client, http.MethodPut, origin+"/skill/v1/skills/publish", map[string]string{
			"namespace": namespace, "name": name, "version": version, "comment": "runnable example",
		})
		if err != nil {
			return err
		}
		if response.Code != 200000 && response.Code != 200002 {
			return fmt.Errorf("publish Skill failed: code=%d info=%s", response.Code, response.Info)
		}

		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			item, err := getVersion(client, origin)
			if err == nil && item != nil && strings.EqualFold(item.Status, "published") {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		item, err := getVersion(client, origin)
		if err != nil {
			return err
		}
		if item == nil || !strings.EqualFold(item.Status, "published") {
			return errors.New("skill was not visible as published before timeout")
		}
	}
	if err := waitForSkillDataPlane(); err != nil {
		return err
	}
	log.Printf("Skill published and downloadable: %s@%s", name, version)
	return nil
}

type envelope struct {
	Code     uint32 `json:"code"`
	Info     string `json:"info"`
	Resource *struct {
		Ext map[string]string `json:"ext"`
	} `json:"resource"`
	Version *struct {
		Version string `json:"version"`
		Status  string `json:"status"`
	} `json:"version"`
}

func getVersion(client *http.Client, origin string) (*struct {
	Version string `json:"version"`
	Status  string `json:"status"`
}, error) {
	query := url.Values{"namespace": {namespace}, "name": {name}, "version": {version}}
	response, err := request(client, http.MethodGet, origin+"/skill/v1/skills?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if response.Code == 400202 {
		return nil, nil
	}
	if response.Code != 200000 {
		return nil, fmt.Errorf("get Skill failed: code=%d info=%s", response.Code, response.Info)
	}
	if response.Resource == nil || response.Resource.Ext[ownerKey] != owner {
		return nil, fmt.Errorf("skill %s is not owned by this example", name)
	}
	return response.Version, nil
}

func waitForVersion(client *http.Client, origin string, wait time.Duration) (*struct {
	Version string `json:"version"`
	Status  string `json:"status"`
}, error) {
	deadline := time.Now().Add(wait)
	for {
		item, err := getVersion(client, origin)
		if err == nil && item != nil {
			return item, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("skill version was not visible before timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitForSkillDataPlane() error {
	discover := net.JoinHostPort(server, fmt.Sprint(grpcPort))
	address := skillAddr
	if address == "" {
		address = net.JoinHostPort(server, "8094")
	}
	sdk, err := demo.NewSDK(discover, address, true, timeout)
	if err != nil {
		return fmt.Errorf("create Skill data-plane client: %w", err)
	}
	defer sdk.Destroy()
	skills := api.NewSkillAPIByContext(sdk)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		resource, err := skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
			Namespace: namespace, Name: name, Version: version,
		}})
		if err != nil {
			last = err
		} else if resource == nil || resource.Namespace != namespace || resource.Name != name || resource.Version != version {
			last = errors.New("published Skill identity is not visible on the data plane")
		} else {
			pkg, err := skills.DownloadSkill(&api.SkillDownloadRequest{SkillDownloadRequest: model.SkillDownloadRequest{
				Namespace: namespace, Name: name, Version: version, Format: model.SkillFormatZIP,
			}})
			if err != nil {
				last = err
			} else if pkg == nil || pkg.Format != model.SkillFormatZIP || pkg.ContentType != model.SkillContentTypeZIP ||
				len(pkg.Content) == 0 || len(pkg.Digest) != 64 {
				last = errors.New("published Skill package is not visible on the data plane")
			} else {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("skill data plane did not become ready: %w; last error: %v", ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

func ensureNamespace(client *http.Client, origin string) error {
	response, err := request(client, http.MethodPost, origin+"/naming/v1/namespaces", []map[string]string{{
		"name": namespace, "comment": owner,
	}})
	if err != nil {
		return err
	}
	if response.Code != 200000 && response.Code != 400201 {
		return fmt.Errorf("create namespace failed: code=%d info=%s", response.Code, response.Info)
	}
	return nil
}

func newClient() (*http.Client, string, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		"http://" + net.JoinHostPort(server, fmt.Sprint(httpPort)),
		nil
}

func request(client *http.Client, method, target string, payload any) (*envelope, error) {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := firstToken(); token != "" {
		req.Header.Set("X-Polaris-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read Console response: %w", err)
	}
	var result envelope
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("decode Console response: %w", err)
	}
	return &result, nil
}

func firstToken() string {
	for _, key := range []string{"POLARIS_CONSOLE_TOKEN", "POLARIS_TOKEN", "POLARIS_SERVICE_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
