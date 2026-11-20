/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package main publishes the demo Skill used by the Skill example.
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
	owner    = "trpc-agent-naming-polarismesh-examples"
	ownerKey = "examples.owner"
)

var (
	server      string
	polarisPort int
	consolePort int
	skillPort   int
	namespace   string
	name        string
	version     string
	timeout     time.Duration
)

func main() {
	flag.StringVar(&server, "server", os.Getenv("POLARIS_SERVER"), "Polaris host")
	flag.IntVar(&polarisPort, "polaris-port", 8091, "Polaris Discovery gRPC port")
	flag.IntVar(&consolePort, "console-port", 8090, "Polaris Console HTTP port")
	flag.IntVar(&skillPort, "skill-port", 8094, "Polaris Skill gRPC port")
	flag.StringVar(&namespace, "namespace", "go-agent-naming-examples", "Polaris namespace")
	flag.StringVar(&name, "skill", "polaris-greeting", "Skill name")
	flag.StringVar(&version, "skill-version", "1.0.0", "Skill version")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "Console and Skill timeout")
	flag.Parse()
	if server == "" {
		log.Fatal("--server or POLARIS_SERVER is required")
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
	defer client.CloseIdleConnections()
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
			if err := createSkill(client, origin); err != nil {
				return err
			}
		}
		if err := publishVersion(client, origin); err != nil {
			return err
		}
	}
	return waitForDataPlane()
}

func createSkill(client *http.Client, origin string) error {
	payload := map[string]any{
		"namespace":   namespace,
		"name":        name,
		"version":     version,
		"description": "Polaris AI Registry example Skill",
		"scope":       "PRIVATE",
		"author":      owner,
		"tags":        []string{"trpc-agent-go", "polaris-example"},
		"ext":         map[string]string{ownerKey: owner},
		"files": []map[string]any{
			{
				"path":        "SKILL.md",
				"contentType": "text",
				"content": []byte(fmt.Sprintf(`---
name: %s
description: Answers with the Polaris example greeting
---

# Polaris Greeting

When the user asks for the Polaris greeting, answer exactly:

POLARIS_SKILL_GREETING=hello-from-%s
`, name, version)),
			},
			{
				"path":        "references/details.md",
				"contentType": "text",
				"content":     []byte("This reference file proves the example packages multiple Skill files.\n"),
			},
			{
				"path":        "scripts/echo.sh",
				"contentType": "text",
				"content":     []byte("#!/bin/sh\necho \"polaris-skill-script\"\n"),
			},
		},
	}
	response, err := request(client, http.MethodPost, origin+"/skill/v1/skills", payload)
	if err != nil {
		return err
	}
	if response.Code != 200000 && response.Code != 400201 {
		return fmt.Errorf("create Skill: code=%d info=%s", response.Code, response.Info)
	}
	return waitForVersion(client, origin, 10*time.Second)
}

func waitForVersion(client *http.Client, origin string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		item, err := getVersion(client, origin)
		if err == nil && item != nil {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return errors.New("skill version did not become visible before timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func publishVersion(client *http.Client, origin string) error {
	response, err := request(client, http.MethodPut, origin+"/skill/v1/skills/publish", map[string]string{
		"namespace": namespace,
		"name":      name,
		"version":   version,
		"comment":   "published by the go-agent-naming-polarismesh example",
	})
	if err != nil {
		return err
	}
	if response.Code != 200000 && response.Code != 200002 {
		return fmt.Errorf("publish Skill: code=%d info=%s", response.Code, response.Info)
	}
	deadline := time.Now().Add(timeout)
	for {
		item, err := getVersion(client, origin)
		if err == nil && item != nil && strings.EqualFold(item.Status, "published") {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("skill did not become published before timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitForDataPlane() error {
	address := net.JoinHostPort(server, fmt.Sprint(skillPort))
	sdk, err := demo.NewSDK(
		net.JoinHostPort(server, fmt.Sprint(polarisPort)),
		address,
		true,
		timeout,
	)
	if err != nil {
		return fmt.Errorf("create Skill data-plane client: %w", err)
	}
	defer sdk.Destroy()
	skills := api.NewSkillAPIByContext(sdk)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var last error
	for {
		resource, err := skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
			Namespace: namespace,
			Name:      name,
			Version:   version,
		}})
		if err != nil {
			last = err
		} else if resource == nil || resource.Namespace != namespace ||
			resource.Name != name || resource.Version != version {
			last = errors.New("published Skill identity is not visible")
		} else {
			pkg, err := skills.DownloadSkill(&api.SkillDownloadRequest{
				SkillDownloadRequest: model.SkillDownloadRequest{
					Namespace: namespace,
					Name:      name,
					Version:   version,
					Format:    model.SkillFormatZIP,
				},
			})
			if err == nil && pkg != nil && len(pkg.Content) > 0 {
				log.Printf("Skill published and downloadable: %s/%s@%s", namespace, name, version)
				return nil
			}
			if err != nil {
				last = err
			} else {
				last = errors.New("published Skill package is empty")
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("skill data plane did not become ready: %w; last error: %v", ctx.Err(), last)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

type versionInfo struct {
	Version string `json:"version"`
	Status  string `json:"status"`
}

type envelope struct {
	Code     uint32 `json:"code"`
	Info     string `json:"info"`
	Resource *struct {
		Ext map[string]string `json:"ext"`
	} `json:"resource"`
	Version *versionInfo `json:"version"`
}

func getVersion(client *http.Client, origin string) (*versionInfo, error) {
	query := url.Values{"namespace": {namespace}, "name": {name}, "version": {version}}
	response, err := request(client, http.MethodGet, origin+"/skill/v1/skills?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if response.Code == 400202 {
		return nil, nil
	}
	if response.Code != 200000 {
		return nil, fmt.Errorf("get Skill: code=%d info=%s", response.Code, response.Info)
	}
	if response.Resource == nil || response.Resource.Ext[ownerKey] != owner {
		return nil, fmt.Errorf("skill %s/%s is not owned by this example", namespace, name)
	}
	return response.Version, nil
}

func ensureNamespace(client *http.Client, origin string) error {
	response, err := request(client, http.MethodPost, origin+"/naming/v1/namespaces", []map[string]string{{
		"name": namespace, "comment": owner,
	}})
	if err != nil {
		return err
	}
	if response.Code != 200000 && response.Code != 400201 {
		return fmt.Errorf("create namespace: code=%d info=%s", response.Code, response.Info)
	}
	return nil
}

func request(client *http.Client, method, target string, payload any) (*envelope, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := firstEnv("POLARIS_CONSOLE_TOKEN", "POLARIS_TOKEN"); token != "" {
		req.Header.Set("X-Polaris-Token", token)
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("console HTTP %s %s returned %d: %s",
			method, target, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	var result envelope
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode Console response: %w", err)
	}
	return &result, nil
}

func newClient() (*http.Client, string, error) {
	origin := demo.ConsoleAddress(server, consolePort)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: timeout}, origin, nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
