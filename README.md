# Polaris AI Registry integration for tRPC-Agent-Go

[English](README.md) | [简体中文](README.zh_CN.md)

`trpc-agent-naming-polarismesh` connects tRPC-Agent-Go to Polaris AI
Registry. It builds and freezes the A2A AgentCard, injects instance metadata
through `go-naming-polarismesh`, assembles Skill repositories, resolves healthy
A2A instances, creates A2A clients, and optionally registers and discovers MCP
servers and clients.

The module keeps the same ownership boundary as the broader AI Registry design:

- `polaris-go` owns Polaris protocols, AI Agent models, and Skill transport.
- `go-naming-polarismesh` owns instance registration, heartbeat, discovery, and
  governance.
- This module owns the tRPC-Agent-Go adapter and A2A assembly.

## Package layout

The implementation is organized by domain, following the layout used by
`trpc-agent-go`:

- `agent/`: Runtime, lifecycle, options, and provider integration.
- `agentcard/`: A2A AgentCard building, validation, and metadata encoding.
- `config/`: adapter configuration and Skill references.
- `naming/`: Polaris discovery, sticky selection, and A2A client resolution.
- `skill/`: Skill installation and executable repository assembly.
- `mcp/`: Optional MCP metadata, Service Contract, client pool, and watcher.
- `server/a2a/`: A2A server construction.
- `internal/`: errors, metrics, and test helpers.
- Root package: a thin compatibility facade over the domain packages.

## Requirements

- Go 1.21 or newer
- tRPC-Agent-Go v1.11.0 or newer
- tRPC-Go v1.1.0 or newer
- A2A v1 and `trpc-a2a-go/v2`
- `go-naming-polarismesh` with dynamic `MetadataProvider` support
- `trpc-mcp-go` v0.0.17 when using the optional `mcp/` package

## Provider quick start

Create one Runtime before the tRPC server starts, call `Prepare`, and let
`go-naming-polarismesh` register the instance. The adapter does not register the
instance itself.

```go
import (
	polarisagent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	polarisconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
)

config := polarisconfig.DefaultConfig()
config.Namespace = "Production"
config.ServiceName = "customer-agent"
config.AgentName = "customer-agent"
config.AgentVersion = "1.2.3"
config.Description = "Customer support agent"
config.AdvertiseHost = "127.0.0.1"
config.AdvertisePort = 18080
config.RegistrationPort = 1
config.A2APath = "/a2a"
config.A2AVersion = polarisconfig.A2AVersionV10

runtime, err := polarisagent.New(
	polarisagent.WithConfig(config),
	polarisagent.WithSDKContext(registry.SDKContext()),
)
if err != nil {
	return err
}
if err := runtime.Prepare(ctx); err != nil {
	return err
}
defer runtime.Close(ctx)

// Start Serve only after Prepare succeeds.
```

`Prepare` builds an immutable AgentCard and registers this metadata provider
before `Serve`:

```text
ai-agent-name  = <service name>
ai-agent-card  = <complete A2A AgentCard JSON>
ai-a2a-version = 0.3 | 1.0
```

The adapter owns these three AgentCard keys. When `polaris-go` promotes
structured instance fields into `Instance.GetMetadata()`, validation and
parsing also accept `version`, `region`, `zone`, and `campus`. Those fields are
platform metadata rather than additional AgentCard contract keys; any other
unexpected key is rejected.

The instance host and version come from the AgentCard. Its port comes from
`RegistrationPort` (default `1`), and the instance protocol is the empty
string. One card produces one instance even when it advertises JSON-RPC,
HTTP+JSON, and gRPC interfaces. Every advertised interface must use the same
IP; business interface ports are never replaced by the registration port.

`Prepare` installs a complete host/port/version/protocol/metadata provider.
Go-naming reads it once for each registration; the application supplies only
generic lifecycle settings such as TTL. `RegistrationSnapshot()` is available
for inspection and explicit integration, with `SetProtocol=true` to clear the
instance protocol. No AgentCard parsing or AI policy remains in go-naming.
`UpdateAgentCard` and `UpdateAgentCardJSON` replace the full frozen snapshot;
a validation failure preserves the previous one.

Static instance metadata can be supplied explicitly with the canonical keys:

```go
runtime, err := polarisagent.New(
    polarisagent.WithService("Development", "customer-agent"),
    polarisagent.WithAgentInstanceMetadata("1.0", metadata),
)
```

Remove card metadata and `registration_port` from the naming YAML. Configure
the extension with `WithRegistrationPort` instead (default `1`, range
`1..65535`). Registration metadata must use the canonical three AgentCard keys
and `ai-a2a-version`; the card JSON bytes are preserved. Mixed sources and
conflicting framework configuration fail before registration. Naming remains
the sole registration/heartbeat owner.

If the provider is already installed by the current tRPC process, the Runtime
can reuse its SDK context explicitly:

```go
sdkContext, ok := polarisagent.SDKContextFromFramework("customer-agent")
if !ok {
	return errors.New("polaris registry context is not available")
}
```

## A2A server

`NewA2AServer` injects the frozen AgentCard. Start the server on the advertised
address, and call `Stop` after it has started. The example owns its TaskManager
and HTTP lifecycle when mounting `Handler` directly.

```go
import polarisa2a "trpc.group/trpc-go/trpc-agent-naming-polarismesh/server/a2a"

a2aServer, err := polarisa2a.NewA2AServer(runtime, agentRunner)
if err != nil {
	return err
}
// Start blocks until shutdown; an application shutdown handler calls
// a2aServer.Stop(shutdownCtx) after Start has begun.
if err := a2aServer.Start("127.0.0.1:18080"); err != nil {
	return err
}
```

The Runtime does not start a listener, manage a tRPC server, or close a borrowed
SDK context.

## Consumer quick start

The resolver filters out unhealthy and isolated instances, parses the instance
AgentCard with the `ai-a2a-version` schema, validates interface hosts against
the instance IP, and keeps a sticky instance for each caller key.

```go
runtime, err := polarisagent.New(
	polarisagent.WithSDKContext(sdkContext),
	polarisagent.WithService("Production", "customer-agent"),
)
if err != nil {
	return err
}
if err := runtime.Prepare(ctx); err != nil {
	return err
}
defer runtime.Close(ctx)

remoteAgent, err := runtime.NewRemoteAgentWithBinding(ctx, "tenant-a", "JSONRPC")
if err != nil {
	return err
}
_ = remoteAgent
```

The requested `protocolBinding` selects an interface from the card; the
instance port is not used to build the business request URL. The most recent
successfully resolved card is used only when Polaris discovery fails, and the
fallback is rebound to the requested interface. Invalid cards and host
mismatches are skipped before selection. Resolver fallback, sticky-caller, and
candidate state is isolated by protocol binding. The adapter does not silently
retry a streaming call after an event has been delivered. Invalid instances and
discovery fallback are reported through tRPC metrics and logs.

Custom A2A options are supplied by a `naming.RemoteAgentOptionsFactory`. Each
factory call owns a new client configuration; it can reuse a concurrency-safe
`http.RoundTripper`, but must create a new `http.Client` before applying options
such as `WithTimeout`. The factory may run concurrently for different remote
agents. Its context is only the construction budget, not the lifetime of later
requests. The adapter reuses the client while the card is unchanged. Card
updates do not wait for older event streams, which remain on their original
instance. Callers must consume each event channel or cancel its Run context.
Instance discovery and service-Skill reads use one bounded SDK attempt and
explicitly disable inherited retries. Synchronous SDK operations still cannot
be forcibly interrupted; cancellation is checked before and after each call.
If discovery changes during construction, the adapter discards the stale build
and retries at most three times. Factories must be safe to invoke again; empty
or permanently rejected snapshots fail without installing a stale client.

```go
remoteAgent, err := runtime.NewRemoteAgent(ctx, "tenant-a",
	func(ctx context.Context) ([]a2aagent.Option, error) {
		return []a2aagent.Option{
			a2aagent.WithA2AClientExtraOptions(
				a2aclient.WithHTTPClient(&http.Client{Transport: http.DefaultTransport}),
				a2aclient.WithTimeout(30 * time.Second),
			),
		}, nil
	},
)
```

The example uses `net/http`, `time`,
`trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1`, and
`trpc.group/trpc-go/trpc-a2a-go/v2/client`. Passing saved, mutable options directly
is replaced by this factory contract in the unreleased adapter API.

## Skills

Skill loading supports three modes:

| Mode | Behavior |
| --- | --- |
| `names` | Install only explicit `SkillRefs`; skip List. |
| `published` | Page through published Skills, bounded by list/max limits, and install each active version. |
| `mounted` | Read `extended_metadata` from an AI Agent service and install its `namespace:name` references. |

`SkillRefs` take precedence over service-level references. References are
deduplicated by `namespace:name@version` and sorted before installation.

```go
config.SkillCacheDir = "./var/polaris-skills"
config.SkillMode = polarisconfig.SkillModeMounted
config.SkillRefs = []polarisconfig.SkillRef{
	{Namespace: "Production", Name: "weather", Version: "1.0.0"},
}
config.UseServiceSkills = true
config.MountSkills = true
```

The Runtime installs every selected Skill through the Polaris `SkillAPI`,
verifies that each result stays inside the cache root and contains `SKILL.md`,
then creates a `skill.Repository`.
Published Skill list and install calls honor the remaining load or refresh
deadline; an SDK context created by the Runtime also aligns Skill message and
download timeouts with `PolarisRequestTimeout`.

Published and mounted repositories can refresh atomically through
`SkillListRefresh`. A failed refresh keeps the previous complete repository.
`SkillFailureThreshold`, `SkillListLimit`, `SkillMaxSkills`, and
`SkillCacheMaxAge` bound failures, pagination, capacity, and stale cache repair.

`MountSkillsReplace` defaults to true in `DefaultConfig`: replacement mounts and
installs only the explicit set, including an empty set to clear old references.
Merge mounts submit only the configured ID, name, and version. An explicit
reference to an existing ID replaces its summary with those fields, matching
the Polaris Writer contract; unmentioned IDs retain their latest Console data.
With `UseServiceSkills` and merge enabled, installation uses the service returned
by the mount operation. Each package must expose exactly its selected `SKILL.md`,
whose repository name matches the reference. Mounting is a service mutation and
is not rolled back if a later installation fails.

## MCP

The optional `mcp/` package adapts `trpc-mcp-go` without moving MCP framework
types into the root package or `polaris-go`.

- `PrepareServer` builds and validates `mcp-server-name`,
  `mcp-protocol-version`, and the transport-specific endpoint path.
- `CollectContract` reads only the initialized server's declared Tools,
  Resources, and Prompts capabilities and reports contract
  type `mcp`, feature types `101/102/103`, request methods, and notification
  metadata. Contract reporting uses `ProviderAPI.ReportServiceContract`; queries use
  `ConsumerAPI.GetServiceContract`. Reporting defaults to fail-fast and can use
  `warn` for an explicit best-effort mode.
- `NewClient` supports SSE and Streamable HTTP; `NewStdioClient` keeps stdio
  local and never creates a network instance.
- `Pool` and `Watcher` maintain one initialized client per healthy instance,
  detect endpoint/path/protocol replacements, and report call results as
  `InstanceTypeMCP`.

Adapter clients retain initialization capabilities. External connectors must
implement `ServerCapabilitiesProvider` or supply the actual initialization
capabilities through `Config.ServerCapabilities` before collecting a contract.
Errors from declared capabilities remain failures.

Client operations are serialized per connector; queueing is cancellable and
included in `Timeout`. Streamable HTTP disables the SDK's independent GET SSE
connection because its session/event fields are not concurrency-safe. POST SSE
responses and legacy SSE remain supported. Custom Streamable HTTP pool factories
must return an adapter `NewClient` or another concurrency-safe connector, not a
bare `trpc-mcp-go.Client`.
Custom connectors must honor request contexts and synchronize background work.
Synchronous notification callbacks must enqueue requests to the same connector
instead of waiting for them inside the active request.

Polaris currently stores `service_contract_detail.method` as `VARCHAR(32)`.
MCP notification names longer than that are retained in the contract JSON
`notifications` field instead of being truncated or sent as an invalid
InterfaceDescriptor. Core request methods remain stored as server interfaces.

## Polaris APIs

The Runtime creates this adapter's local `AgentAPI` and the `polaris-go` `SkillAPI`
from the same borrowed SDK context used by `go-naming-polarismesh`. Default Agent
service reads use ConsumerAPI, including mounted Skill metadata, without Console.
Service Contracts are reported through ProviderAPI and queried through ConsumerAPI.
Core has no `ServiceAdminAPI`, `AgentAPI`, or `MCPAPI`.

`WithAgentAPI` and `WithSkillAPI` are available for custom implementations and
isolated tests. Production callers normally only provide `WithSDKContext`.

Optional service writes require an explicitly configured adapter API:

```go
agentAPI := agentapi.NewByContext(sdkCtx,
    agentapi.WithConsole([]string{"http://127.0.0.1:8090"}, consoleToken, 5*time.Second),
)
defer agentAPI.Destroy()
```

Pass it through `agent.WithAgentAPI` when `EnsureService` is enabled. Without
Console, writes return `ErrCodeServiceWriteUnsupported`; instance registration
through go-naming remains available. Console request models, configuration, and
transport are adapter-owned. Destroy releases only adapter-owned resources, not
the borrowed SDK context. Final-Skill clearing still requires server support for
explicit empty extended metadata.

## Lifecycle

The supported order is:

```text
New -> Prepare -> Serve -> stop serving -> Runtime.Close
```

- `Prepare` is idempotent.
- `Close` is idempotent.
- Methods reject use before `Prepare` or after `Close`.
- A context passed with `WithSDKContext` is borrowed and never destroyed.
- The instance remains owned by tRPC and `go-naming-polarismesh`; `Close` does
  not deregister it.

## Development

Core is pinned to the `polaris-go` main snapshot
`v1.7.3-0.20261118135400-4fe9e1e0aca7`.
The generic naming adapter is pinned to the `go-naming-polarismesh` main snapshot
`trpc-naming-polarismesh v1.0.1-0.20261119025216-6b54006e7aff`. Both use
fetchable module versions and there are no filesystem replacements.

```bash
go test -race -count=1 ./...
go vet ./...
golangci-lint run ./...
```

## Runnable Examples

The examples follow the runnable style of `trpc-agent-go`: a real LLM Agent,
a real Agent Runner, and a real Polaris AI Registry.

| Example | What it demonstrates | LLM key |
| --- | --- | --- |
| [examples/a2a](examples/a2a) | AgentCard registration, Polaris discovery, A2A unary/streaming | Yes |
| [examples/mcp](examples/mcp) | MCP metadata, Service Contract, discovery, and tool calls | Yes |
| [examples/skill](examples/skill) | Skill publish, install, repository assembly, and `skill_load` | Yes |
| [examples/a2a-agentcard](examples/a2a-agentcard) | Registered Agent A calls two versioned Agent B instances | No |

The A2A, MCP, and Skill examples require an OpenAI-compatible model endpoint.
Every example requires a Polaris host at runtime.
Each example has a `cleanup.sh` with `--dry-run`, `--force`, and
`--delete-service` support.
The `a2a-agentcard` provider defaults to the Polaris AI Registry AgentCard
1.0/0.3 multi-protocol enhancement; explicit `-role agent-a|agent-b` selects
the multi-version flow.

## Full Integration Tests

`integration/` contains focused real-service suites for A2A, MCP, Skill, and
the registered `a2a-agentcard` flow. They use deterministic or scripted
models, so no LLM API key is required. The Polaris host must be supplied
through `POLARIS_SERVER`; Discovery, Console, and Skill ports default to 8091,
8090, and 8094. Tests use a dedicated
`go-agent-naming-e2e` namespace and unique per-run resources.

```bash
export POLARIS_SERVER=your-polaris-host
bash integration/verify_all.sh
```

Each test creates uniquely named services and Skills, then deregisters and
deletes only its own resources in `t.Cleanup`. The shared namespace is
retained. See the [integration guide](integration/README.md) for suite details
and environment variables.

For a killed run, [`integration/cleanup.sh`](integration/cleanup.sh) removes
resources by run ID.

## License

Copyright (C) 2026 Tencent. All rights reserved.

Licensed under the [Apache License, Version 2.0](LICENSE).
