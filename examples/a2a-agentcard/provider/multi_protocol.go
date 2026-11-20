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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	adapteragent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	adapterconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/examples/internal/demo"
)

const multiProtocolContentType = "application/a2a+json"

func runMultiInterfaceA2A(ctx context.Context) error {
	sdk, err := demo.NewSDK(
		net.JoinHostPort(server, fmt.Sprint(grpcPort)),
		"",
		false,
		timeout,
	)
	if err != nil {
		return fmt.Errorf("create Polaris SDK context: %w", err)
	}
	defer sdk.Destroy()

	effectiveService := serviceName
	effectiveVersion := version
	var rawCard []byte
	if cardPath != "" {
		rawCard, err = os.ReadFile(cardPath)
		if err != nil {
			return fmt.Errorf("read AgentCard %s: %w", cardPath, err)
		}
		var identity struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(rawCard, &identity); err != nil {
			return fmt.Errorf("decode AgentCard %s: %w", cardPath, err)
		}
		if identity.Name != "" {
			effectiveService = identity.Name
		}
		if identity.Version != "" {
			effectiveVersion = identity.Version
		}
	}

	cfg := adapterconfig.DefaultConfig()
	cfg.Namespace = namespace
	cfg.ServiceName = effectiveService
	cfg.AgentName = effectiveService
	cfg.AgentVersion = effectiveVersion
	cfg.RegistrationPort = registrationPort
	cfg.A2AVersion = a2aVersion
	cfg.A2APath = "/a2a/v1"
	cfg.Streaming = false
	cfg.RegistryEnabled = true
	cfg.DiscoveryEnabled = false
	cfg.PolarisRequestTimeout = timeout

	options := []adapteragent.Option{
		adapteragent.WithConfig(cfg),
		adapteragent.WithSDKContext(sdk),
	}
	var (
		listener  net.Listener
		httpSrv   *http.Server
		serveDone chan error
		card      a2aprotocolserver.AgentCard
	)
	if noServe {
		options = append(options, adapteragent.WithAgentCardJSON(a2aVersion, rawCard))
	} else {
		listener, err = net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		defer func() { _ = listener.Close() }()
		actualPort := listener.Addr().(*net.TCPAddr).Port
		if cardPath != "" {
			card, err = rewriteMultiProtocolCard(
				cardPath, effectiveService, effectiveVersion, host, actualPort, a2aVersion,
			)
		} else {
			card, err = buildMultiProtocolCard(
				effectiveService, effectiveVersion, host, actualPort, a2aVersion,
			)
		}
		if err != nil {
			return err
		}
		options = append(options, adapteragent.WithAgentCard(card))
	}

	runtime, err := adapteragent.New(options...)
	if err != nil {
		return fmt.Errorf("create multi-interface A2A Runtime: %w", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	if err := runtime.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare multi-interface A2A Runtime: %w", err)
	}

	if !noServe {
		cardJSON, _, err := runtime.AgentCardJSON()
		if err != nil {
			return fmt.Errorf("read frozen AgentCard JSON: %w", err)
		}
		httpSrv = &http.Server{
			Handler:           newMultiProtocolHandler(cardJSON),
			ReadHeaderTimeout: 5 * time.Second,
		}
		serveDone = make(chan error, 1)
		go func() { serveDone <- httpSrv.Serve(listener) }()
	}

	snapshot, err := runtime.RegistrationSnapshot()
	if err != nil {
		return fmt.Errorf("read registration snapshot: %w", err)
	}
	registry, err := demo.RegisterProvider(
		sdk,
		namespace,
		effectiveService,
		effectiveVersion,
		host,
		snapshot.RegistrationPort,
		snapshot.TTL,
	)
	if err != nil {
		if httpSrv != nil {
			_ = httpSrv.Close()
		}
		return fmt.Errorf("register multi-interface A2A provider: %w", err)
	}
	defer func() { _ = registry.Deregister(effectiveService) }()

	if noServe {
		log.Printf("multi-interface A2A provider registered without serving: service=%s schema=%s version=%s",
			effectiveService, a2aVersion, effectiveVersion)
		<-ctx.Done()
		return nil
	}

	log.Printf("multi-interface A2A provider ready: service=%s schema=%s version=%s endpoint=http://%s",
		effectiveService, a2aVersion, effectiveVersion,
		net.JoinHostPort(host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)))
	select {
	case <-ctx.Done():
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdown)
}

func buildMultiProtocolCard(name, version, host string, port int, schema string) (a2aprotocolserver.AgentCard, error) {
	if net.ParseIP(strings.Trim(host, "[]")) == nil {
		return a2aprotocolserver.AgentCard{}, fmt.Errorf("multi-interface A2A host %q must be an IP address", host)
	}
	base := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	streaming := false
	description := "Polaris AI Registry AgentCard multi-protocol provider"
	skillDescription := "Echo messages over A2A JSON-RPC 1.0, JSON-RPC 0.3 and HTTP+JSON 1.0"
	card := a2aprotocolserver.AgentCard{
		Name:        name,
		Description: description,
		Version:     version,
		Capabilities: a2aprotocolserver.AgentCapabilities{
			Streaming: &streaming,
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []a2aprotocolserver.AgentSkill{{
			ID:          "echo",
			Name:        "Echo",
			Description: &skillDescription,
			Tags:        []string{"echo"},
		}},
	}
	modern := a2aprotocolserver.AgentInterface{
		URL:             base + "/a2a/v1",
		ProtocolBinding: "JSONRPC",
		ProtocolVersion: "1.0",
	}
	legacy := a2aprotocolserver.AgentInterface{
		URL:             base + "/a2a/legacy",
		ProtocolBinding: "JSONRPC",
		ProtocolVersion: "0.3",
	}
	rest := a2aprotocolserver.AgentInterface{
		URL:             base + "/rest",
		ProtocolBinding: "HTTP+JSON",
		ProtocolVersion: "1.0",
	}
	if schema == "0.3" {
		protocolVersion := "0.3.0"
		preferred := "JSONRPC"
		card.URL = legacy.URL
		card.ProtocolVersion = &protocolVersion
		card.PreferredTransport = &preferred
		card.AdditionalInterfaces = []a2aprotocolserver.AgentInterface{modern, rest}
	} else {
		card.SupportedInterfaces = []a2aprotocolserver.AgentInterface{modern, legacy, rest}
	}
	return card, nil
}

func rewriteMultiProtocolCard(
	path, name, version, host string,
	port int,
	schema string,
) (a2aprotocolserver.AgentCard, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return a2aprotocolserver.AgentCard{}, fmt.Errorf("read AgentCard %s: %w", path, err)
	}
	var card a2aprotocolserver.AgentCard
	if err := json.Unmarshal(raw, &card); err != nil {
		return a2aprotocolserver.AgentCard{}, fmt.Errorf("decode AgentCard %s: %w", path, err)
	}
	card.Name = name
	card.Version = version
	card.Signatures = nil
	base := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	modern := a2aprotocolserver.AgentInterface{
		URL:             base + "/a2a/v1",
		ProtocolBinding: "JSONRPC",
		ProtocolVersion: "1.0",
	}
	legacy := a2aprotocolserver.AgentInterface{
		URL:             base + "/a2a/legacy",
		ProtocolBinding: "JSONRPC",
		ProtocolVersion: "0.3",
	}
	rest := a2aprotocolserver.AgentInterface{
		URL:             base + "/rest",
		ProtocolBinding: "HTTP+JSON",
		ProtocolVersion: "1.0",
	}
	if schema == "0.3" {
		protocolVersion := "0.3.0"
		preferred := "JSONRPC"
		card.SupportedInterfaces = nil
		card.URL = legacy.URL
		card.ProtocolVersion = &protocolVersion
		card.PreferredTransport = &preferred
		card.AdditionalInterfaces = []a2aprotocolserver.AgentInterface{modern, rest}
	} else {
		card.URL = ""
		card.ProtocolVersion = nil
		card.PreferredTransport = nil
		card.AdditionalInterfaces = nil
		card.SupportedInterfaces = []a2aprotocolserver.AgentInterface{modern, legacy, rest}
	}
	return card, nil
}

type multiProtocolHandler struct {
	cardJSON string
}

func newMultiProtocolHandler(cardJSON string) http.Handler {
	return &multiProtocolHandler{cardJSON: cardJSON}
}

func (h *multiProtocolHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/.well-known/agent-card.json":
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", multiProtocolContentType)
		_, _ = writer.Write([]byte(h.cardJSON))
	case "/a2a/v1":
		h.handleJSONRPC(writer, request, "1.0")
	case "/a2a/legacy":
		h.handleJSONRPC(writer, request, "0.3")
	case "/rest", "/rest/message:send", "/rest/v1/message:send":
		h.handleREST(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

type multiProtocolJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type multiProtocolSendParams struct {
	Message map[string]any `json:"message"`
}

func (h *multiProtocolHandler) handleJSONRPC(
	writer http.ResponseWriter,
	request *http.Request,
	schema string,
) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var payload multiProtocolJSONRPCRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeMultiProtocolJSONRPCError(writer, nil, -32700, "Parse error", http.StatusBadRequest)
		return
	}
	expectedMethod := "SendMessage"
	if schema == "0.3" {
		expectedMethod = "message/send"
	}
	if payload.JSONRPC != "2.0" || payload.Method != expectedMethod {
		writeMultiProtocolJSONRPCError(writer, payload.ID, -32601, "Method not found", http.StatusOK)
		return
	}
	var params multiProtocolSendParams
	if err := json.Unmarshal(payload.Params, &params); err != nil || params.Message == nil {
		writeMultiProtocolJSONRPCError(writer, payload.ID, -32602, "Missing message parameter", http.StatusOK)
		return
	}
	message := multiProtocolMessage(params.Message, schema, "JSONRPC")
	result := any(message)
	if schema != "0.3" {
		result = map[string]any{"message": message}
	}
	id := any(nil)
	if len(payload.ID) > 0 {
		id = payload.ID
	}
	writeMultiProtocolJSON(writer, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func (h *multiProtocolHandler) handleREST(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeMultiProtocolJSON(writer, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	message, ok := payload["message"].(map[string]any)
	if !ok {
		message = payload
	}
	if message == nil {
		writeMultiProtocolJSON(writer, http.StatusBadRequest, map[string]any{"error": "message must be an object"})
		return
	}
	writeMultiProtocolJSON(writer, http.StatusOK, map[string]any{
		"message": multiProtocolMessage(message, "1.0", "HTTP+JSON"),
	})
}

func multiProtocolMessage(message map[string]any, schema, transport string) map[string]any {
	text := multiProtocolMessageText(message)
	contextID := multiProtocolStringField(message, "contextId", "context_id")
	taskID := multiProtocolStringField(message, "taskId", "task_id")
	if schema == "0.3" {
		result := map[string]any{
			"kind":      "message",
			"role":      "agent",
			"parts":     []any{map[string]any{"kind": "text", "text": "A2A 0.3 JSONRPC echo: " + text}},
			"messageId": uuid.NewString(),
		}
		if contextID != "" {
			result["contextId"] = contextID
		}
		if taskID != "" {
			result["taskId"] = taskID
		}
		return result
	}
	if contextID == "" {
		contextID = uuid.NewString()
	}
	result := map[string]any{
		"role":      "ROLE_AGENT",
		"parts":     []any{map[string]any{"text": "A2A 1.0 " + transport + " echo: " + text}},
		"messageId": uuid.NewString(),
		"contextId": contextID,
	}
	if taskID != "" {
		result["taskId"] = taskID
	}
	return result
}

func multiProtocolMessageText(message map[string]any) string {
	parts, _ := message["parts"].([]any)
	for _, raw := range parts {
		part, _ := raw.(map[string]any)
		if part == nil {
			continue
		}
		if text, ok := part["text"].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func multiProtocolStringField(message map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := message[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func writeMultiProtocolJSONRPCError(
	writer http.ResponseWriter,
	id json.RawMessage,
	code int,
	message string,
	status int,
) {
	value := any(nil)
	if len(id) > 0 {
		value = id
	}
	writeMultiProtocolJSON(writer, status, map[string]any{
		"jsonrpc": "2.0",
		"id":      value,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeMultiProtocolJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", multiProtocolContentType)
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}
