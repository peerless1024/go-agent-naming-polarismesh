# Changelog

[English](CHANGELOG.md) | [简体中文](CHANGELOG.zh_CN.md)

All notable changes to this project will be documented in this file.

## v0.1.0 - Unreleased

- Add Runtime configuration, optional SDK context ownership, and lifecycle
  management.
- Build and validate A2A 0.3/1.0 AgentCards, complete multi-interface cards,
  raw JSON/signature preservation, and the three AgentCard metadata keys.
- Accept platform fields promoted into SDK instance metadata (`version`,
  `region`, `zone`, and `campus`) while rejecting unexpected metadata keys.
- Integrate dynamic metadata providers with `go-naming-polarismesh`.
- Add atomic metadata/host/version/registration-port/protocol/TTL snapshots and
  registration port `1` defaults with empty instance protocol.
- Own all AgentCard/A2A/MCP preparation and static configuration conflicts in
  the extension; go-naming consumes only generic frozen fields.
- Add explicit `WithAgentInstanceMetadata` support for canonical three-key
  static metadata.
- Rewrite the runnable examples around official-style LLM Agents for A2A, MCP
  Service Contract and tool discovery, and Skill publish/install/load flows.
- Replace the monolithic E2E runner with focused A2A, MCP, Skill, and A2A
  AgentCard real-service integration suites using deterministic or scripted
  models and owned-resource cleanup.
- Pin fetchable `polaris-go` and `go-naming-polarismesh` main snapshots, upgrade
  tRPC-Go to v1.1.0, and remove filesystem replacements.
- Add the adapter-owned `AgentAPI` using ConsumerAPI for default service reads
  and integrate SkillAPI through the borrowed SDK context. Migrate Console
  management, request models, configuration, and regression tests into adapter
  internals after removing Core ServiceAdminAPI and AgentAPI, with no Core shim.
- Require explicit `agentapi.WithConsole` for optional service management.
  Preserve tri-state updates, revision/owner/Force guards, unknown metadata,
  uncertain-write recovery, and the final-Skill empty-list server limitation.
- Keep missing resource revisions empty and preserve borrowed SDKContext ownership.
- Add `names`, `published`, and `mounted` Skill modes, paginated published
  loading, service `namespace:name` parsing, failure thresholds, bounded
  capacity, stale-cache repair, and atomic periodic repository refresh.
- Add healthy-instance discovery, AgentCard schema/host validation, interface
  selection by `protocolBinding`, sticky selection, bad-card skipping, metrics,
  and cached-card fallback rebound to the requested interface.
- Restrict the fixed tRPC-Agent A2A client to JSONRPC and HTTP+JSON, reject
  unsupported bindings before discovery, and skip incompatible wire versions
  while allowing a 0.3 card to advertise a consumable 1.0 additional interface.
- Route A2A client calls to the selected interface URL and serve JSON-RPC from
  its advertised interface path, including multi-interface cards.
- Add optional MCP server metadata, Contract collection/reporting, SSE,
  Streamable HTTP, stdio, client pool/watch replacement detection, and
  `InstanceTypeMCP` call reporting. Contract reporting uses
  `ProviderAPI.ReportServiceContract`; queries use `ConsumerAPI.GetServiceContract`.
- Bound MCP initialization and tool calls, protect in-flight connectors during
  pool refresh/close, and report MCP result-level errors as failures.
- Serialize MCP connector requests with bounded, cancellable admission, retain
  initialization capabilities, and collect/project only declared contract
  capabilities. Disable unsafe independent Streamable HTTP GET SSE while keeping
  POST SSE and legacy SSE. Use MCP v0.0.17 for safe stdio process shutdown.
- Preserve complete discovered AgentCards and default streaming behavior, reject
  malformed interface versions without panic, advance Skill pagination by raw
  windows, and start/drain Skill refreshes within Runtime lifecycle ownership.
- Isolate Resolver fallback, sticky-caller, and candidate state by protocol
  binding so one binding's failure cannot evict another binding's cache.
- Bound published Skill list and install operations by the remaining load
  deadline, and align owned SDK Skill message/download timeouts with the
  configured Polaris request timeout.
- Preserve legacy mounted-plus-explicit Skill merging and make empty or
  overlapping Skill refreshes converge deterministically.
- Preserve MCP notification names longer than the Polaris 32-character
  InterfaceDescriptor method limit in contract JSON instead of truncating
  them.
- Add A2A client and server helpers.
- Add an `a2a-agentcard` demo where a registered Agent A calls two versioned
  registered Agent B instances with bilingual guides and guarded cleanup.
- Add build-tag-isolated integration suites for A2A, MCP, Skill, and registered
  Agent A/B flows, with a shared real-Polaris harness and per-test
  owned-resource cleanup.
- Reorganize user-facing programs under `examples/` and complete real-service
  verification under `integration/`, with bilingual guides for each directory.
- Add per-example and run-ID cleanup scripts with dry-run, instance identity,
  Service Contract, and Skill owner checks.
- Make the a2a-agentcard provider default to the Polaris AI Registry AgentCard
  1.0/0.3 multi-protocol enhancement and no-serve registration; explicit
  `-role` retains the multi-version Agent A/B flow.
- Add `integration/verify_all.sh` for running the real suites with explicit
  server, namespace, run ID, and per-operation timeout settings.
- Cover the nested Agent A to registered Agent B invocation path in both the
  runnable example and the dedicated real a2a-agentcard integration suite.
- Document that clearing service Skill references with explicit empty
  `extended_metadata` requires Polaris `v2.2.0.0` or newer.
- Add tests, race validation, README documentation, and CI.
- Require Go 1.21.
