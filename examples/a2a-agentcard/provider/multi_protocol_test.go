/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adapteragentcard "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agentcard"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
)

func TestBuildMultiProtocolCard(t *testing.T) {
	t.Run("1.0", func(t *testing.T) {
		card, err := buildMultiProtocolCard("document-agent", "1.0.0", "127.0.0.1", 18080, "1.0")
		if err != nil {
			t.Fatal(err)
		}
		if len(card.SupportedInterfaces) != 3 {
			t.Fatalf("interfaces=%d, want 3", len(card.SupportedInterfaces))
		}
		if got := card.SupportedInterfaces[0].URL; !strings.HasSuffix(got, "/a2a/v1") {
			t.Errorf("preferred interface=%q", got)
		}
		if got := card.SupportedInterfaces[1].ProtocolVersion; got != "0.3" {
			t.Errorf("legacy interface version=%q", got)
		}
		if got := card.SupportedInterfaces[2].ProtocolBinding; got != "HTTP+JSON" {
			t.Errorf("REST interface binding=%q", got)
		}
	})
	t.Run("0.3", func(t *testing.T) {
		card, err := buildMultiProtocolCard("document-agent", "0.9.0", "127.0.0.1", 18080, "0.3")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(card.URL, "/a2a/legacy") {
			t.Errorf("legacy card URL=%q", card.URL)
		}
		if card.ProtocolVersion == nil || *card.ProtocolVersion != "0.3.0" {
			t.Errorf("legacy card protocolVersion=%v", card.ProtocolVersion)
		}
		if len(card.AdditionalInterfaces) != 2 {
			t.Errorf("additional interfaces=%d, want 2", len(card.AdditionalInterfaces))
		}
	})
}

func TestMultiProtocolHandler(t *testing.T) {
	card, err := buildMultiProtocolCard("document-agent", "1.0.0", "127.0.0.1", 18080, "1.0")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	handler := newMultiProtocolHandler(string(raw))

	t.Run("agent card", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"supportedInterfaces"`) {
			t.Fatalf("card body=%s", recorder.Body.String())
		}
	})

	t.Run("JSON-RPC 1.0", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":"v1","method":"SendMessage","params":{"message":{"messageId":"request","role":"ROLE_USER","parts":[{"text":"v1"}]}}}`
		response := serveMultiProtocolRequest(t, handler, "/a2a/v1", body)
		message := response["result"].(map[string]any)["message"].(map[string]any)
		assertMultiProtocolMessage(t, message, "ROLE_AGENT", "", "A2A 1.0 JSONRPC echo: v1")
	})

	t.Run("JSON-RPC 0.3", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":"v03","method":"message/send","params":{"message":{"kind":"message","messageId":"request","role":"user","parts":[{"kind":"text","text":"v03"}]}}}`
		response := serveMultiProtocolRequest(t, handler, "/a2a/legacy", body)
		message := response["result"].(map[string]any)
		assertMultiProtocolMessage(t, message, "agent", "text", "A2A 0.3 JSONRPC echo: v03")
	})

	t.Run("HTTP+JSON 1.0", func(t *testing.T) {
		body := `{"message":{"messageId":"request","role":"ROLE_USER","parts":[{"text":"rest"}]}}`
		response := serveMultiProtocolRequest(t, handler, "/rest/message:send", body)
		message := response["message"].(map[string]any)
		assertMultiProtocolMessage(t, message, "ROLE_AGENT", "", "A2A 1.0 HTTP+JSON echo: rest")
	})

	t.Run("version method isolation", func(t *testing.T) {
		cases := []struct {
			path, method string
		}{
			{path: "/a2a/v1", method: "message/send"},
			{path: "/a2a/legacy", method: "SendMessage"},
		}
		for _, item := range cases {
			body := `{"jsonrpc":"2.0","id":"wrong","method":"` + item.method +
				`","params":{"message":{"parts":[{"text":"wrong"}]}}}`
			response := serveMultiProtocolRequest(t, handler, item.path, body)
			errorBody, ok := response["error"].(map[string]any)
			if !ok || errorBody["code"] != float64(-32601) {
				t.Fatalf("%s accepted %s: %v", item.path, item.method, response)
			}
		}
	})
}

func TestMultiProtocolCardValidation(t *testing.T) {
	for _, schema := range []string{"1.0", "0.3"} {
		t.Run(schema, func(t *testing.T) {
			version := "1.0.0"
			cfg := adapterconfig.DefaultConfig()
			cfg.Namespace = "ns"
			cfg.ServiceName = "document-agent"
			cfg.AgentName = "document-agent"
			cfg.AgentVersion = version
			cfg.AdvertiseHost = "127.0.0.1"
			cfg.AdvertisePort = 18080
			cfg.A2APath = "/a2a/v1"
			cfg.A2AVersion = schema
			card, err := buildMultiProtocolCard(cfg.ServiceName, version, cfg.AdvertiseHost, cfg.AdvertisePort, schema)
			if err != nil {
				t.Fatal(err)
			}
			if err := adapteragentcard.ValidateAgentCard(
				cfg, &card, adapteragentcard.SchemaVersion(schema),
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRewriteMultiProtocolCard(t *testing.T) {
	raw := `{
		"name": "old-name",
		"description": "keep this description",
		"version": "0.0.1",
		"supportedInterfaces": [
			{"url": "http://127.0.0.1:8080/a2a", "protocolBinding": "JSONRPC"}
		],
		"capabilities": {"streaming": false},
		"defaultInputModes": ["text/plain"],
		"defaultOutputModes": ["text/plain"],
		"skills": [{"id": "search", "name": "Search", "description": "Find documents", "tags": ["search"]}]
	}`
	path := filepath.Join(t.TempDir(), "agent-card.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	card, err := rewriteMultiProtocolCard(
		path, "document-agent", "1.0.0", "127.0.0.1", 18080, "1.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "document-agent" || card.Version != "1.0.0" {
		t.Fatalf("identity=%s/%s", card.Name, card.Version)
	}
	if card.Description != "keep this description" || len(card.Skills) != 1 {
		t.Fatalf("card content was not preserved: %+v", card)
	}
	if len(card.SupportedInterfaces) != 3 {
		t.Fatalf("interfaces=%d, want 3", len(card.SupportedInterfaces))
	}
	if !strings.HasSuffix(card.SupportedInterfaces[0].URL, "/a2a/v1") {
		t.Fatalf("preferred URL=%s", card.SupportedInterfaces[0].URL)
	}

	legacy, err := rewriteMultiProtocolCard(
		path, "document-agent", "0.9.0", "127.0.0.1", 18080, "0.3",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(legacy.URL, "/a2a/legacy") ||
		legacy.ProtocolVersion == nil || *legacy.ProtocolVersion != "0.3.0" ||
		len(legacy.AdditionalInterfaces) != 2 {
		t.Fatalf("legacy card was not rewritten: %+v", legacy)
	}
	if legacy.Description != "keep this description" || len(legacy.Skills) != 1 {
		t.Fatalf("legacy card content was not preserved: %+v", legacy)
	}
}

func serveMultiProtocolRequest(t *testing.T, handler http.Handler, path, body string) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}
	return response
}

func assertMultiProtocolMessage(
	t *testing.T,
	message map[string]any,
	role, partKind, text string,
) {
	t.Helper()
	if got := message["role"]; got != role {
		t.Errorf("role=%v, want %q", got, role)
	}
	if message["messageId"] == "" || message["messageId"] == nil {
		t.Error("messageId is missing")
	}
	parts, ok := message["parts"].([]any)
	if !ok || len(parts) == 0 {
		t.Fatalf("parts=%v", message["parts"])
	}
	part := parts[0].(map[string]any)
	if part["text"] != text {
		t.Errorf("text=%v, want %q", part["text"], text)
	}
	if partKind == "" {
		if _, exists := part["kind"]; exists {
			t.Errorf("1.0 text part must not contain kind: %v", part)
		}
	} else if part["kind"] != partKind {
		t.Errorf("part kind=%v, want %q", part["kind"], partKind)
	}
}
