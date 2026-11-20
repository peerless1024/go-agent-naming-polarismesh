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

- Go 1.21+ on the machine that builds and packages the tests.
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

Build every integration suite and AgentCard example before running:

```bash
make -C integration build
```

This writes `integration/bin/<os>-<arch>/`. Runtime scripts only execute these
binaries; they do not call `go test` or `go build`.

```bash
export POLARIS_SERVER=your-polaris-host
bash integration/verify_all.sh
```

Run one suite:

```bash
make -C integration build
POLARIS_SERVER=your-polaris-host \
  "integration/bin/$(go env GOOS)-$(go env GOARCH)/a2a.test" \
  -test.count=1 -test.v -test.timeout=10m
```

Build without a server:

```bash
make -C integration build
```

`verify_all.sh` also accepts:

| Flag | Effect |
| --- | --- |
| `--server=host` | Sets `POLARIS_SERVER` |
| `--namespace=name` | Sets `POLARIS_E2E_NAMESPACE` |
| `--run-id=id` | Sets `POLARIS_E2E_RUN_ID` |
| `--timeout=30s` | Sets per-operation timeout |
| `--bin-dir=DIR` | Uses a specific prebuilt binary directory |
| `-v` | Verbose test output |

## Cloud Package

This repository provides only the packaging step. Upload, deployment,
execution, and cleanup are owned by the cloud test workflow.

```bash
# Build and package all tests for the cloud:
make -C integration package TARGET=linux/amd64

# Equivalent direct script:
./integration/build-materials.sh --target=linux/amd64
```

The binary package is
`integration/dist/go-agent-naming-integration-linux-amd64-bin.zip`. It contains
all integration test executables and the AgentCard provider/client examples,
so the cloud machine does not need Go. The target OS/architecture must match
the cloud machine. Binaries are statically built with `CGO_ENABLED=0`.

The package contains the integration tree, `PACKAGE-INFO.txt`, `Makefile`, and
these cloud entrypoints:

- `integration/verify_all.sh`
- `integration/run_retained.sh`
- `integration/cleanup.sh`
- `integration/pack-logs.sh`

The cloud machine needs `bash`, `curl`, `jq`, and `unzip`, plus network access
to Polaris. It does not need Go.

After cloud-side unpacking, use the existing entrypoints:

```bash
unzip go-agent-naming-integration-linux-amd64-bin.zip
cd go-agent-naming-integration
export POLARIS_SERVER=your-polaris-host
bash integration/verify_all.sh --run-id cloud-verify-01

# Or retain selected suites:
bash integration/run_retained.sh \
  --run-id cloud-inspect-01 \
  --suite=a2a,mcp,skill,a2a-agentcard

# Package logs for return to the local machine:
bash integration/pack-logs.sh --run-id cloud-verify-01

# Cloud-side cleanup:
bash integration/cleanup.sh \
  --run-id <run-id> \
  --delete-service \
  --force \
  --polaris-server "$POLARIS_SERVER"
```

`verify_all.sh` writes `integration/.logs/<run-id>.log`.
`run_retained.sh` writes `integration/.retained/<run-id>/`.
`pack-logs.sh` collects both into
`integration/dist/go-agent-naming-logs-<run-id>.tar.gz`, adds run metadata, and
excludes retained token env files.

Polaris tokens must be configured on the cloud machine. No token or access
URL is embedded by `build-materials.sh`.

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
Re-running the same run ID and suite reuses an existing KEEP job and an
already-published owned Skill; use a fresh run ID for an independent run.

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
