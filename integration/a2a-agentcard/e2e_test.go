//go:build integration

/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package a2aagentcard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"trpc.group/trpc-go/trpc-agent-naming-polarismesh/integration/internal/harness"
)

func TestPrebuiltAgentCardExamples(t *testing.T) {
	tests := []struct {
		name   string
		envKey string
		binary string
	}{
		{
			name:   "provider",
			envKey: "POLARIS_E2E_AGENTCARD_PROVIDER_BIN",
			binary: "agentcard-provider",
		},
		{
			name:   "client",
			envKey: "POLARIS_E2E_AGENTCARD_CLIENT_BIN",
			binary: "agentcard-client",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exampleBinary(t, test.envKey, test.binary)
		})
	}
}

func TestA2AAgentCardExampleAcrossProcesses(t *testing.T) {
	cfg := harness.Load(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	serviceA := cfg.Unique("agent-a")
	serviceB := cfg.Unique("agent-b")
	for _, service := range []string{serviceA, serviceB} {
		if err := cfg.EnsureService(service, model.ServiceTypeAIAgent); err != nil {
			t.Fatalf("ensure AI_AGENT service %s: %v", service, err)
		}
	}
	t.Cleanup(func() {
		for _, service := range []string{serviceA, serviceB} {
			if err := cfg.DeleteService(service, model.ServiceTypeAIAgent); err != nil {
				t.Logf("delete service %s: %v", service, err)
			}
		}
	})

	providerBinary := exampleBinary(
		t, "POLARIS_E2E_AGENTCARD_PROVIDER_BIN", "agentcard-provider",
	)
	clientBinary := exampleBinary(
		t, "POLARIS_E2E_AGENTCARD_CLIENT_BIN", "agentcard-client",
	)

	baseArgs := []string{
		"-server", cfg.Server,
		"-polaris-port", strconv.Itoa(cfg.GRPCPort),
		"-namespace", cfg.Namespace,
		"-timeout", cfg.RequestTimeout.String(),
	}
	b1 := startExampleProcess(t, "agent-b-v1", providerBinary, withArgs(baseArgs,
		"-role", "agent-b",
		"-service", serviceB,
		"-version", "v1.0.0",
		"-port", "0",
		"-registration-port", "1",
	)...)
	b2 := startExampleProcess(t, "agent-b-v2", providerBinary, withArgs(baseArgs,
		"-role", "agent-b",
		"-service", serviceB,
		"-version", "v2.0.0",
		"-port", "0",
		"-registration-port", "2",
	)...)

	consumer := api.NewConsumerAPIByContext(mustSDK(t, cfg, false))
	instances := waitHealthyInstances(t, cfg, consumer, serviceB, 2, b1, b2)
	seen := make(map[string]uint32)
	for _, instance := range instances {
		seen[instance.GetVersion()] = instance.GetPort()
		metadata := instance.GetMetadata()
		if got := metadata[model.MetadataKeyAIAgentName]; got != serviceB {
			t.Errorf("ai-agent-name=%q, want %q", got, serviceB)
		}
		if got := metadata[model.MetadataKeyAIAgentCard]; got == "" {
			t.Error("ai-agent-card is missing")
		}
		if got := metadata[model.MetadataKeyA2AVersion]; got != "1.0" {
			t.Errorf("ai-a2a-version=%q, want 1.0", got)
		}
		if got := instance.GetProtocol(); got != "" {
			t.Errorf("instance protocol=%q, want empty", got)
		}
	}
	if seen["v1.0.0"] != 1 || seen["v2.0.0"] != 2 {
		t.Fatalf("versioned registration ports=%v, want v1=1 v2=2", seen)
	}

	a := startExampleProcess(t, "agent-a", providerBinary, withArgs(baseArgs,
		"-role", "agent-a",
		"-service", serviceA,
		"-target-service", serviceB,
		"-target-versions", "v1.0.0,v2.0.0",
		"-version", "v1.0.0",
		"-port", "0",
		"-registration-port", "1",
	)...)
	waitHealthyInstances(t, cfg, consumer, serviceA, 1, a)

	output := runClient(t, ctx, clientBinary, withArgs(baseArgs,
		"-service", serviceA,
		"-target-versions", "v1.0.0,v2.0.0",
	)...)
	if !output.Success {
		t.Fatalf("example client reported failure: %+v", output)
	}
	if !strings.Contains(output.Response, "b=v1.0.0:agent-b|v1.0.0|echo:hello-from-agent-a") {
		t.Fatalf("response is missing Agent B v1: %s", output.Response)
	}
	if !strings.Contains(output.Response, "b=v2.0.0:agent-b|v2.0.0|echo:hello-from-agent-a") {
		t.Fatalf("response is missing Agent B v2: %s", output.Response)
	}
	t.Logf("a2a-agentcard response: %s", output.Response)
	harness.KeepIfRequested(t)
}

func TestAgentCardMultiProtocolProvider(t *testing.T) {
	cfg := harness.Load(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	providerBinary := exampleBinary(
		t, "POLARIS_E2E_AGENTCARD_PROVIDER_BIN", "agentcard-provider",
	)
	baseArgs := []string{
		"-server", cfg.Server,
		"-polaris-port", strconv.Itoa(cfg.GRPCPort),
		"-namespace", cfg.Namespace,
		"-timeout", cfg.RequestTimeout.String(),
	}

	for _, schema := range []string{"1.0", "0.3"} {
		schema := schema
		t.Run("schema-"+strings.ReplaceAll(schema, ".", ""), func(t *testing.T) {
			service := cfg.Unique("agentcard-" + strings.ReplaceAll(schema, ".", ""))
			if err := cfg.EnsureService(service, model.ServiceTypeAIAgent); err != nil {
				t.Fatalf("ensure AI_AGENT service %s: %v", service, err)
			}
			t.Cleanup(func() {
				if err := cfg.DeleteService(service, model.ServiceTypeAIAgent); err != nil {
					t.Logf("delete service %s: %v", service, err)
				}
			})
			sdk := mustSDK(t, cfg, false)
			consumer := api.NewConsumerAPIByContext(sdk)
			providerArgs := []string{
				"-a2a-version", schema,
				"-port", "0",
				"-registration-port", "1",
			}
			if schema == "1.0" {
				raw := writeMultiProtocolCardFile(
					t, service, "1.0.0",
					"Polaris AI Registry AgentCard integration fixture", "a2a-e2e",
				)
				cardPath := filepath.Join(t.TempDir(), "agent-card.json")
				if err := os.WriteFile(cardPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				providerArgs = append(providerArgs, "-card", cardPath)
			} else {
				providerArgs = append(providerArgs,
					"-service", service,
					"-version", "1.0.0",
				)
			}
			process := startExampleProcess(t, "agentcard-"+strings.ReplaceAll(schema, ".", ""),
				providerBinary, withArgs(baseArgs, providerArgs...)...)
			instances := waitHealthyInstances(t, cfg, consumer, service, 1, process)
			instance := instances[0]
			metadata := instance.GetMetadata()
			if got := metadata[model.MetadataKeyAIAgentName]; got != service {
				t.Errorf("ai-agent-name=%q, want %q", got, service)
			}
			if got := metadata[model.MetadataKeyA2AVersion]; got != schema {
				t.Errorf("ai-a2a-version=%q, want %q", got, schema)
			}
			if got := instance.GetVersion(); got != "1.0.0" {
				t.Errorf("instance version=%q, want 1.0.0", got)
			}
			if got := instance.GetPort(); got != 1 {
				t.Errorf("instance port=%d, want 1", got)
			}
			if got := instance.GetProtocol(); got != "" {
				t.Errorf("instance protocol=%q, want empty", got)
			}
			cardRaw := metadata[model.MetadataKeyAIAgentCard]
			var card multiProtocolCard
			if err := json.Unmarshal([]byte(cardRaw), &card); err != nil {
				t.Fatalf("decode AgentCard: %v\n%s", err, cardRaw)
			}
			if schema == "1.0" {
				if card.Description != "Polaris AI Registry AgentCard integration fixture" ||
					len(card.Skills) != 1 || card.Skills[0].ID != "a2a-e2e" {
					t.Fatalf("live card did not preserve file content: %+v", card)
				}
			}
			endpoints, endpointHost := multiProtocolEndpoints(t, card, schema)

			client := newMultiProtocolClient()
			servedCard := getMultiProtocolCard(t, ctx, client, endpointHost)
			if strings.TrimSpace(servedCard) != strings.TrimSpace(cardRaw) {
				t.Fatalf("served AgentCard differs from instance metadata\nserved:\n%s\nmetadata:\n%s",
					servedCard, cardRaw)
			}

			v1 := postMultiProtocol(t, ctx, client, endpoints["jsonrpc_v1"], map[string]any{
				"jsonrpc": "2.0",
				"id":      "v1",
				"method":  "SendMessage",
				"params": map[string]any{"message": map[string]any{
					"messageId": "v1-request",
					"role":      "ROLE_USER",
					"parts":     []any{map[string]any{"text": "v1"}},
				}},
			})
			assertMultiProtocol1Message(t, v1["result"], "A2A 1.0 JSONRPC echo: v1")

			legacy := postMultiProtocol(t, ctx, client, endpoints["jsonrpc_v03"], map[string]any{
				"jsonrpc": "2.0",
				"id":      "v03",
				"method":  "message/send",
				"params": map[string]any{"message": map[string]any{
					"kind":      "message",
					"messageId": "v03-request",
					"role":      "user",
					"parts":     []any{map[string]any{"kind": "text", "text": "v03"}},
				}},
			})
			assertMultiProtocol03Message(t, legacy["result"], "A2A 0.3 JSONRPC echo: v03")

			rest := postMultiProtocol(t, ctx, client,
				strings.TrimSuffix(endpoints["http_json_v1"], "/")+"/message:send",
				map[string]any{"message": map[string]any{
					"messageId": "rest-request",
					"role":      "ROLE_USER",
					"parts":     []any{map[string]any{"text": "rest"}},
				}})
			assertMultiProtocolRESTMessage(t, rest, "A2A 1.0 HTTP+JSON echo: rest")
			harness.KeepIfRequested(t)
		})
	}

	t.Run("no-serve", func(t *testing.T) {
		service := cfg.Unique("agentcard-noserve")
		if err := cfg.EnsureService(service, model.ServiceTypeAIAgent); err != nil {
			t.Fatalf("ensure AI_AGENT service %s: %v", service, err)
		}
		t.Cleanup(func() {
			if err := cfg.DeleteService(service, model.ServiceTypeAIAgent); err != nil {
				t.Logf("delete service %s: %v", service, err)
			}
		})
		cardPath := filepath.Join(t.TempDir(), "agent-card.json")
		cardJSON := writeMultiProtocolCardFile(
			t, service, "1.0.0",
			"external AgentCard A2A agent", "echo",
		)
		if err := os.WriteFile(cardPath, cardJSON, 0o600); err != nil {
			t.Fatal(err)
		}
		sdk := mustSDK(t, cfg, false)
		consumer := api.NewConsumerAPIByContext(sdk)
		process := startExampleProcess(t, "agentcard-noserve", providerBinary, withArgs(baseArgs,
			"-a2a-version", "1.0",
			"-no-serve",
			"-card", cardPath,
			"-registration-port", "1",
		)...)
		instances := waitHealthyInstances(t, cfg, consumer, service, 1, process)
		if got := instances[0].GetMetadata()[model.MetadataKeyAIAgentCard]; got != string(cardJSON) {
			t.Fatalf("no-serve AgentCard was not preserved exactly\nwant:\n%s\ngot:\n%s", cardJSON, got)
		}
		harness.KeepIfRequested(t)
	})
}

type clientOutput struct {
	Success  bool   `json:"success"`
	Events   int    `json:"events"`
	Response string `json:"response"`
	Error    string `json:"error"`
}

func runClient(
	t *testing.T,
	ctx context.Context,
	binary string,
	args ...string,
) clientOutput {
	t.Helper()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = exampleRoot(t)
	command.Env = os.Environ()
	raw, err := command.Output()
	if err != nil {
		var stderr []byte
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = exitErr.Stderr
		}
		t.Fatalf("run example client: %v\nstdout:\n%s\nstderr:\n%s", err, raw, stderr)
	}
	var output clientOutput
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("decode example client output: %v\n%s", err, raw)
	}
	return output
}

func exampleBinary(
	t *testing.T,
	envKey string,
	name string,
) string {
	t.Helper()
	path := strings.TrimSpace(os.Getenv(envKey))
	if path == "" {
		target := runtime.GOOS + "-" + runtime.GOARCH
		path = filepath.Join(exampleRoot(t), "integration", "bin", target, name)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", envKey, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		t.Fatalf("stat %s=%s: %v; run make -C integration build", envKey, absolute, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("%s=%s is not an executable file", envKey, absolute)
	}
	return absolute
}

func waitHealthyInstances(
	t *testing.T,
	cfg *harness.Config,
	consumer api.ConsumerAPI,
	service string,
	expected int,
	processes ...*exampleProcess,
) []model.Instance {
	t.Helper()
	deadline := time.Now().Add(cfg.RequestTimeout)
	var last string
	for {
		for _, process := range processes {
			if !process.alive() {
				t.Fatalf("process %s exited before %s became healthy\n%s",
					process.name, service, process.tail())
			}
		}
		timeout := 2 * time.Second
		request := &api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{
			Namespace:       cfg.Namespace,
			Service:         service,
			SkipRouteFilter: true,
			Timeout:         &timeout,
		}}
		request.SetRetryCount(0)
		response, err := consumer.GetInstances(request)
		if err == nil && response != nil {
			var healthy []model.Instance
			for _, instance := range response.GetInstances() {
				if instance != nil && instance.IsHealthy() && !instance.IsIsolated() {
					healthy = append(healthy, instance)
				}
			}
			if len(healthy) == expected {
				return healthy
			}
			last = fmt.Sprintf("%d healthy instances", len(healthy))
		} else if err == nil {
			last = "empty instance response"
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			t.Fatalf("wait for %d healthy instances of %s: %s", expected, service, last)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func mustSDK(t *testing.T, cfg *harness.Config, enableSkill bool) api.SDKContext {
	t.Helper()
	sdk, err := cfg.SDK(enableSkill)
	if err != nil {
		t.Fatalf("create Polaris SDK: %v", err)
	}
	t.Cleanup(sdk.Destroy)
	return sdk
}

func exampleRoot(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("could not find repository root from working directory")
		}
		root = parent
	}
}

func withArgs(base []string, extra ...string) []string {
	result := append([]string(nil), base...)
	return append(result, extra...)
}

type exampleProcess struct {
	name   string
	cmd    *exec.Cmd
	log    string
	done   chan error
	stopMu sync.Once
}

func startExampleProcess(t *testing.T, name, binary string, args ...string) *exampleProcess {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create %s log: %v", name, err)
	}
	command := exec.Command(binary, args...)
	command.Dir = exampleRoot(t)
	command.Env = os.Environ()
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start %s: %v", name, err)
	}
	process := &exampleProcess{
		name: name,
		cmd:  command,
		log:  logPath,
		done: make(chan error, 1),
	}
	go func() {
		err := command.Wait()
		_ = logFile.Close()
		process.done <- err
	}()
	t.Cleanup(process.stop)
	return process
}

func (p *exampleProcess) stop() {
	p.stopMu.Do(func() {
		if p.cmd == nil || p.cmd.Process == nil {
			return
		}
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
}

func (p *exampleProcess) alive() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	return p.cmd.Process.Signal(syscall.Signal(0)) == nil
}

func (p *exampleProcess) tail() string {
	raw, err := os.ReadFile(p.log)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	return strings.Join(lines, "\n")
}

type multiProtocolCard struct {
	Name                 string                   `json:"name"`
	Description          string                   `json:"description"`
	Version              string                   `json:"version"`
	SupportedInterfaces  []multiProtocolInterface `json:"supportedInterfaces"`
	URL                  string                   `json:"url"`
	ProtocolVersion      string                   `json:"protocolVersion"`
	PreferredTransport   string                   `json:"preferredTransport"`
	AdditionalInterfaces []multiProtocolInterface `json:"additionalInterfaces"`
	Skills               []multiProtocolSkill     `json:"skills"`
}

type multiProtocolInterface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	Transport       string `json:"transport"`
	ProtocolVersion string `json:"protocolVersion"`
}

type multiProtocolSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

func writeMultiProtocolCardFile(
	t *testing.T,
	name, version, description, skillID string,
) []byte {
	t.Helper()
	card := map[string]any{
		"name":        name,
		"description": description,
		"version":     version,
		"supportedInterfaces": []map[string]any{
			{
				"url":             "http://127.0.0.1:8080/a2a/v1",
				"protocolBinding": "JSONRPC",
				"protocolVersion": "1.0",
			},
			{
				"url":             "http://127.0.0.1:8080/a2a/legacy",
				"protocolBinding": "JSONRPC",
				"protocolVersion": "0.3",
			},
			{
				"url":             "http://127.0.0.1:8080/rest",
				"protocolBinding": "HTTP+JSON",
				"protocolVersion": "1.0",
			},
		},
		"capabilities":       map[string]any{"streaming": false},
		"defaultInputModes":  []string{"text/plain"},
		"defaultOutputModes": []string{"text/plain"},
		"skills": []map[string]any{{
			"id":          skillID,
			"name":        "A2A E2E",
			"description": "Polaris AI Registry AgentCard multi-protocol skill",
			"tags":        []string{"polaris", "a2a", "e2e"},
		}},
	}
	raw, err := json.MarshalIndent(card, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (i multiProtocolInterface) binding() string {
	if i.ProtocolBinding != "" {
		return i.ProtocolBinding
	}
	return i.Transport
}

func multiProtocolEndpoints(
	t *testing.T,
	card multiProtocolCard,
	schema string,
) (map[string]string, string) {
	t.Helper()
	type candidate struct {
		url, binding, version string
	}
	var candidates []candidate
	if schema == "1.0" {
		if card.URL != "" {
			t.Fatalf("1.0 card must not contain top-level url: %q", card.URL)
		}
		if len(card.SupportedInterfaces) != 3 {
			t.Fatalf("1.0 supportedInterfaces=%d, want 3", len(card.SupportedInterfaces))
		}
		for _, item := range card.SupportedInterfaces {
			candidates = append(candidates, candidate{item.URL, item.binding(), item.ProtocolVersion})
		}
	} else {
		if card.URL == "" || card.ProtocolVersion != "0.3.0" ||
			card.PreferredTransport != "JSONRPC" {
			t.Fatalf("0.3 card fields: url=%q protocolVersion=%q preferredTransport=%q",
				card.URL, card.ProtocolVersion, card.PreferredTransport)
		}
		candidates = append(candidates, candidate{
			card.URL, card.PreferredTransport, card.ProtocolVersion,
		})
		for _, item := range card.AdditionalInterfaces {
			candidates = append(candidates, candidate{item.URL, item.binding(), item.ProtocolVersion})
		}
		if len(candidates) != 3 {
			t.Fatalf("0.3 card interfaces=%d, want 3", len(candidates))
		}
	}

	endpoints := make(map[string]string)
	origin := ""
	for _, item := range candidates {
		parsed, err := url.Parse(item.url)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			t.Fatalf("invalid interface URL %q", item.url)
		}
		currentOrigin := parsed.Scheme + "://" + parsed.Host
		if origin == "" {
			origin = currentOrigin
		} else if origin != currentOrigin {
			t.Fatalf("interfaces use different origins: %q and %q", origin, currentOrigin)
		}
		switch parsed.Path {
		case "/a2a/v1":
			if item.binding != "JSONRPC" || !strings.HasPrefix(item.version, "1.0") {
				t.Fatalf("1.0 JSONRPC interface binding=%q version=%q", item.binding, item.version)
			}
			endpoints["jsonrpc_v1"] = item.url
		case "/a2a/legacy":
			if item.binding != "JSONRPC" || !strings.HasPrefix(item.version, "0.3") {
				t.Fatalf("0.3 JSONRPC interface binding=%q version=%q", item.binding, item.version)
			}
			endpoints["jsonrpc_v03"] = item.url
		case "/rest":
			if item.binding != "HTTP+JSON" || !strings.HasPrefix(item.version, "1.0") {
				t.Fatalf("HTTP+JSON interface binding=%q version=%q", item.binding, item.version)
			}
			endpoints["http_json_v1"] = item.url
		default:
			t.Fatalf("unexpected interface path %q", parsed.Path)
		}
	}
	if len(endpoints) != 3 {
		t.Fatalf("endpoints=%v, want three interfaces", endpoints)
	}
	return endpoints, origin
}

func newMultiProtocolClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func getMultiProtocolCard(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	origin string,
) string {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, origin+"/.well-known/agent-card.json", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("get AgentCard: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get AgentCard status=%d body=%s", response.StatusCode, raw)
	}
	return string(raw)
}

func postMultiProtocol(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	endpoint string,
	payload any,
) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, bytes.NewReader(raw),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("post %s: %v", endpoint, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("post %s status=%d body=%s", endpoint, response.StatusCode, body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s response: %v body=%s", endpoint, err, body)
	}
	return decoded
}

func assertMultiProtocol1Message(t *testing.T, raw any, expected string) {
	t.Helper()
	result, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC result=%T", raw)
	}
	message, ok := result["message"].(map[string]any)
	if !ok {
		t.Fatalf("SendMessageResponse.message=%T", result["message"])
	}
	assertMultiProtocolParts(t, message, "ROLE_AGENT", "", expected)
	if _, exists := message["kind"]; exists {
		t.Fatalf("1.0 Message must not contain kind: %v", message)
	}
	if message["contextId"] == nil || message["contextId"] == "" {
		t.Fatalf("1.0 Message is missing contextId: %v", message)
	}
}

func assertMultiProtocol03Message(t *testing.T, raw any, expected string) {
	t.Helper()
	message, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("0.3 message=%T", raw)
	}
	if message["kind"] != "message" {
		t.Fatalf("0.3 kind=%v, want message", message["kind"])
	}
	assertMultiProtocolParts(t, message, "agent", "text", expected)
}

func assertMultiProtocolRESTMessage(t *testing.T, raw map[string]any, expected string) {
	t.Helper()
	message, ok := raw["message"].(map[string]any)
	if !ok {
		t.Fatalf("SendMessageResponse.message=%T", raw["message"])
	}
	assertMultiProtocolParts(t, message, "ROLE_AGENT", "", expected)
	if _, exists := message["kind"]; exists {
		t.Fatalf("1.0 Message must not contain kind: %v", message)
	}
	if message["contextId"] == nil || message["contextId"] == "" {
		t.Fatalf("1.0 Message is missing contextId: %v", message)
	}
}

func assertMultiProtocolParts(
	t *testing.T,
	message map[string]any,
	role, partKind, expected string,
) {
	t.Helper()
	if message["role"] != role {
		t.Fatalf("role=%v, want %q", message["role"], role)
	}
	if message["messageId"] == nil || message["messageId"] == "" {
		t.Fatalf("messageId is missing: %v", message)
	}
	parts, ok := message["parts"].([]any)
	if !ok || len(parts) == 0 {
		t.Fatalf("parts=%v", message["parts"])
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("part=%T", parts[0])
	}
	if part["text"] != expected {
		t.Fatalf("text=%v, want %q", part["text"], expected)
	}
	if partKind == "" {
		if _, exists := part["kind"]; exists {
			t.Fatalf("1.0 text part must not contain kind: %v", part)
		}
		if _, exists := part["type"]; exists {
			t.Fatalf("1.0 text part must not contain type: %v", part)
		}
	} else if part["kind"] != partKind {
		t.Fatalf("part kind=%v, want %q", part["kind"], partKind)
	}
}
