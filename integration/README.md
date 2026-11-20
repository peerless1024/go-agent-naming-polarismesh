English | [Chinese](README.zh_CN.md)

# Integration Tests

This directory contains real-service checks for the AI Registry paths used by
the examples. The tests are guarded by the `integration` build tag, so normal
`go test ./...` stays offline and does not require Polaris.

| Suite | Real dependencies | Assertions |
| --- | --- | --- |
| [a2a](./a2a) | Polaris, loopback A2A | AgentCard metadata, discovery, unary call, streaming, cancellation |
| [mcp](./mcp) | Polaris, loopback MCP | MCP metadata, Service Contract, discovery, Pool tool call |
| [skill](./skill) | Polaris Skill data plane, loopback A2A | Publish, install, repository assembly, `skill_load` model injection |
| [a2a-agentcard](./a2a-agentcard) | Polaris, loopback A2A | Registered Agent A calls two versioned Agent B instances; the provider's default mode validates the Polaris AI Registry AgentCard 1.0/0.3 multi-protocol enhancement |

## Case Documentation

Each suite documents its processes, registered services, call relationships,
and verification method:

- [a2a/test.md](./a2a/test.md)
- [mcp/test.md](./mcp/test.md)
- [skill/test.md](./skill/test.md)
- [a2a-agentcard/test.md](./a2a-agentcard/test.md)

The A2A, A2A AgentCard, and Skill suites use deterministic or scripted
models, not an external LLM. The MCP suite calls the MCP tool directly through
the adapter Pool. No LLM API key is required.

## Requirements

- Go 1.21+.
- A reachable Polaris deployment.
- Discovery gRPC, Console HTTP, and Skill gRPC endpoints.
- Permission to create services, instances, and Skills in a non-`default`
  namespace.

Environment variables:

| Variable | Purpose | Default |
| --- | --- | --- |
| `POLARIS_SERVER` | Polaris host | required |
| `POLARIS_GRPC_PORT` | Discovery gRPC port | `8091` |
| `POLARIS_HTTP_PORT` | Console HTTP port | `8090` |
| `POLARIS_SKILL_GRPC_PORT` | Skill gRPC port | `8094` |
| `POLARIS_TOKEN` | Discovery token | empty |
| `POLARIS_CONSOLE_TOKEN` | Console token | fallback to `POLARIS_TOKEN` |
| `POLARIS_SKILL_TOKEN` | Skill token | fallback to `POLARIS_TOKEN` |
| `POLARIS_SERVICE_TOKEN` | Registration token | fallback to `POLARIS_TOKEN` |
| `POLARIS_E2E_NAMESPACE` | Test namespace | `go-agent-naming-e2e` |
| `POLARIS_E2E_RUN_ID` | Run identifier | timestamp + random suffix |
| `POLARIS_E2E_TIMEOUT` | Per-operation timeout | `30s` |

## Run

```bash
export POLARIS_SERVER=your-polaris-host
bash integration/verify_all.sh
```

Run one suite:

```bash
POLARIS_SERVER=your-polaris-host \
  go test -tags=integration -count=1 -v -timeout=10m ./integration/a2a
```

Compile without a server:

```bash
go test -tags=integration -run '^$' ./integration/...
```

`verify_all.sh` also accepts:

| Flag | Effect |
| --- | --- |
| `--server=host` | Sets `POLARIS_SERVER` |
| `--namespace=name` | Sets `POLARIS_E2E_NAMESPACE` |
| `--run-id=id` | Sets `POLARIS_E2E_RUN_ID` |
| `--timeout=30s` | Sets per-operation timeout |
| `-v` | Verbose `go test` output |

## Retain Processes and Resources

Use the retained runner when services, Skills, or provider processes must stay
available for inspection after the assertions pass:

```bash
POLARIS_SERVER=your-polaris-host \
  bash integration/run_retained.sh \
  --run-id inspect-01 \
  --suite=a2a,a2a-agentcard,skill
```

Each selected suite runs in a detached process and blocks in keep mode after
its checks succeed. The run ID and retained process manifest are stored below
`integration/.retained/<run-id>/`. This command never performs cleanup.

Stop retained processes and delete the run-scoped remote resources together:

```bash
bash integration/cleanup.sh \
  --run-id inspect-01 \
  --delete-service \
  --force \
  --polaris-server "$POLARIS_SERVER"
```

## Cleanup Abandoned Runs

Normal tests clean their own services and Skills in `t.Cleanup`. If a test
process is killed before cleanup, the run ID printed at startup identifies the
remaining `gan-e2e-<run-id>-*` resources:

```bash
./integration/cleanup.sh \
  --run-id <run-id> \
  --delete-service \
  --dry-run \
  --polaris-server "$POLARIS_SERVER"

./integration/cleanup.sh \
  --run-id <run-id> \
  --delete-service \
  --force \
  --polaris-server "$POLARIS_SERVER"
```

The run-ID form removes the A2A service, MCP service and its Service Contract,
Skill Agent service, A2A AgentCard services, AgentCard multi-protocol services,
and published Skill. Explicit `--service` and `--skill` arguments can be added
for a custom run.

## Resource Ownership

Every run creates uniquely named services and Skills containing the run ID.
The shared namespace is created if needed and is retained.

Each test deregisters its provider and deletes its own AI_AGENT or MCP_SERVER
service. The Skill test deletes the published Skill. Cleanup runs through
`t.Cleanup`, including after assertion failures, so a later unrelated run is
not affected.

These tests are not a replacement for a deployment release gate, remote CI,
or cross-version compatibility testing. A failed real-service precondition is
reported as a test failure, not hidden with a mock or skip.
