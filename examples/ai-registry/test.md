English | [Chinese](test.zh_CN.md)

# AI Registry Real E2E

This document matches the cases implemented by `verify_all.sh`, `cleanup.sh`,
and `internal/demo/`.

## Prerequisites

You need Go 1.21+, bash, `ps`, and a real Polaris deployment with AI_AGENT,
AgentCard, Console Service Writer, and Skill Registry support.
Case 3.7, clearing Skill references with explicit empty `extended_metadata`,
requires Polaris `v2.2.0.0` or newer. `v2.1.1.0` returns `200002` and keeps the
old references.

The current development dependencies are resolved from the root module's local
replace directives. The examples do not define separate modules and do not
modify the dependency repositories.

No LLM API key is required. The Provider executes a deterministic Agent through
a real tRPC-Agent Runner, and Skill content is downloaded from the real
Registry and assembled into a Repository.

The Polaris host must be supplied through `--server` or `POLARIS_SERVER`; there
is no default address in the code. Discovery defaults to port `8091`, Console
to `8090`, and Skill gRPC to `8094`. Override them with
`--discover=host:port`, `--console=http://host:port`, and
`--skill-address=host:port`.

The Provider listens on a random `127.0.0.1` port reachable by the Consumer;
the cloud server does not need to connect back to the local machine. Use
`--advertise-host` for a LAN address when needed. A2A authentication uses the
public test-fixture key, not a deployment credential.

Real authentication reads only the following environment variables, or the
existing SDK configuration selected by `--config-file` /
`POLARIS_E2E_CONFIG`:

- `POLARIS_TOKEN`: Discovery SDK token.
- `POLARIS_CONSOLE_TOKEN`: Console / Service Writer token.
- `POLARIS_SKILL_TOKEN`: Skill token.
- `POLARIS_SERVICE_TOKEN`: registration and heartbeat token.

Tokens never enter command lines, state, results, or test logs. Do not put an
access-bearing URL in `--console`.

## Run

Every run must explicitly choose a resource policy. The suite always uses the
non-`default` namespace `go-agent-naming-e2e`; service and Skill names include
a unique run ID.

```bash
export POLARIS_SERVER=your-polaris-host
./verify_all.sh --server="$POLARIS_SERVER" --resources=cleanup
./verify_all.sh --server="$POLARIS_SERVER" --resources=keep
```

The script builds the Provider, Consumer, and E2E binaries with the race
detector enabled. The default per-operation timeout is 5 seconds and the full
suite timeout is 12 minutes; override them with `--timeout` and
`--run-timeout`.

Use `--debug` to enable SDK debug logs. `GO_BIN` selects another Go toolchain,
and `GOCACHE` selects the writable build cache.

The E2E binary runs all 24 cases in order with no mocks and no skips. Failures
are still written to the current run's `results.json`, and the process exits
with a non-zero status.

Each run directory is `.logs/<run-id>/` and contains `state.json`,
`results.json`, isolated Provider working directories, and logs. `--run-id` and
`--run-dir` can be specified explicitly. A verification run directory must end
with its run ID so cleanup cannot expand beyond the owned scope.

`keep` retains this run's services, Skills, and final Skill Provider. Temporary
nodes needed by individual cases are still stopped by those cases; other
resources are not deleted.

`cleanup` stops all recorded processes, verifies server-side ownership labels,
and deletes this run's instances, services, and Skills after either success or
failure. The shared namespace is always retained.

Cleanup failure makes the run non-zero and is never recorded as success. Build
artifacts are not removed after a single run because another run may still be
using them.

```bash
./cleanup.sh --state=.logs/<run-id>/state.json --dry-run
./cleanup.sh --state=.logs/<run-id>/state.json --apply
./cleanup.sh --state=.logs/<run-id>/state.json --apply --delete-service
./cleanup.sh --state=.logs/<run-id>/state.json --apply --delete-service --artifacts
./cleanup.sh --delete-service --dry-run
./cleanup.sh --delete-service --apply
```

Cleanup is preview-only by default. `--apply` or `--force` is explicit
authorization, and `--dry-run` always wins. Verify, Provider, and Consumer
modes reject cleanup flags so a dry-run request cannot accidentally become a
real write.

State-scoped `--apply` only stops identity-matched processes from that run and
keeps services and Skills unless `--delete-service` is also specified.

`--delete-service` additionally deletes the state's services and Skills and
scans the selected non-`default` namespace for historical empty services.
`--delete-services` is an alias. Historical cleanup only selects names with the
`go-agent-e2e-` prefix whose owner matches and whose real instance count is
zero.

The Provider and Consumer executables cannot switch roles through `--mode`.
The E2E executable only accepts verify, cleanup, and inspect; the entrypoint
and mode must agree.

`--artifacts` removes only this run's Provider work directories, caches, and
logs after successful cleanup. State and results are retained.

Process identity must match PID, start time, complete command, run ID, service,
and ready-file path. Reused PIDs are never signaled. Services and Skills must
match both `e2e.owner` and `e2e.run.id`; a service containing any unowned
instance is not deleted.

Invoke a retained Provider directly:

```bash
./.build/consumer --namespace=go-agent-naming-e2e \
  --service=go-agent-e2e-<run-id>-skill-provider --message=skill
./.build/consumer --namespace=go-agent-naming-e2e \
  --service=go-agent-e2e-<run-id>-skill-provider --message=hello --stream
```

## Cases

### [case 1.1 registration-metadata-and-heartbeat]

Operation: register a Provider through the real go-naming Registry and Runtime
metadata hook; query the four metadata keys, version, and Tenant through the
SDK; wait 7 seconds across several TTL periods; wait up to 15 seconds for the
real `AgentAPI.GetService` service projection.

Expected: an AI_AGENT service with a non-empty service-level revision, exact
endpoint, four matching metadata keys, and a healthy TTL=2s instance proving
periodic heartbeats. A projection failure retains the independent real A2A
fixture for later cases but does not mark this case as passed.

### [case 1.2 unary-authentication-and-tenant]

Operation: the Consumer resolves an AgentCard from real Discovery and sends a
unary request, then sends another request without authentication.

Expected: the authenticated request succeeds with node-a's echo and the
unauthenticated request fails; the Provider records both. The business
Processor asserts that the actual Tenant equals the advertised interface
Tenant and rejects missing or mismatched values.

### [case 1.3 streaming-output-and-cancellation]

Operation: invoke streaming echo, then start a continuous stream and cancel the
context after the first text event.

Expected: full echo and multiple events succeed; the client stream closes
within 3 seconds and Provider `active=0` with increased `canceled` within 5
seconds.

### [case 1.4 exact-root-and-unicode-endpoints]

Operation: register real Providers at `/`, `/a2a/`, `/api/a2a`, and
`/智能体/a2a`, then POST to each advertised URL.

Expected: every call returns a successful business response without following
redirects; every temporary node exits through the normal test flow.

### [case 2.1 two-instance-sticky-discovery]

Operation: add a second real instance, repeatedly resolve and call the sticky
caller 12 times, then use 32 new callers to verify stable hashing.

Expected: the original caller remains on node-a, while new callers can select
both healthy instances.

### [case 2.2 invalid-card-and-host-port-filtering]

Operation: register a healthy instance with missing card metadata and another
whose card endpoint port differs from the instance, then resolve 16 callers.

Expected: both bad nodes are actually registered, and the Consumer selects
only the valid instance.

### [case 2.3 isolation-and-unhealthy-filtering]

Operation: isolate this run's instance through Console; verify PID, start time,
and complete command before sending SIGSTOP to pause node-a's heartbeat; clear
isolation; wait up to 30 seconds for real Console and Discovery projections;
then send SIGCONT.

Expected: both isolation and TTL-expired unhealthy state are observed; isolated
and unhealthy nodes are never selected; heartbeats resume and the instance
becomes healthy at the end; no other service is modified.

### [case 2.4 concurrent-real-clients]

Operation: 24 callers concurrently build independent mutable Clients from real
Registry data, share a Transport, and mix unary and streaming requests.

Expected: every call receives a completion event and full output with no race
detector warning. A closed stream or partial text alone is not success.

### [case 2.5 deregistration-and-sticky-failover]

Operation: send SIGTERM to node-a, deregister it through the registry, observe
Discovery removal, then invoke through the existing dynamic Agent.

Expected: calls fail over to node-b. Provider readiness proves Close is
idempotent, the provider is removed, and the borrowed SDK remains alive.

### [case 2.6 empty-set-invalidates-remote-agent]

Operation: stop the remaining main-service node, observe a real empty instance
set, then call through the existing dynamic Agent.

Expected: `ErrNoAvailableInstance`; no old endpoint or cached card is revived.

### [case 2.7 real-network-fallback-expiry-and-recovery]

Operation: a dedicated Consumer reaches real Polaris Discovery through a
transparent loopback TCP gate; wait up to 25 seconds for the initial connection
with the query budget equal to `--timeout`; disconnect the gate without
stopping the remote service; wait 31 seconds; restore the gate; remove the
Provider and read an empty set; disconnect again.

Expected: the initial network failure is counted and fallback can complete a
call; the endpoint becomes unavailable after the 30-second TTL; real Discovery
recovery works; after an empty set invalidates the Agent, a later network
failure cannot restore the old card.

### [case 3.1 published-skill-download-and-install]

Operation: create this run's Skill as a draft through Console, wait 1.2 seconds,
publish it explicitly, wait up to 30 seconds for the selected version's real
metadata and ZIP to become visible, then query through SDK, download the ZIP,
and install it into an isolated cache.

Expected: matching identity and version, a non-empty ZIP, SHA-256 digest,
selected entrypoint, and complete reference files; the first install is not a
cache hit.

### [case 3.2 missing-draft-and-wrong-version-fail-fast]

Operation: query and assemble non-existent, draft, and wrong-version real
Skills with the Loader.

Expected: `ErrCodeSkillNotFound`; the Loader never returns a partial
Repository.

### [case 3.3 cache-hit-and-content-repair]

Operation: install a pinned Skill repeatedly, tamper with this run's cache
entry, install again, and finally force-install.

Expected: repeated installs preserve digest and download identity; tampering
triggers a real download and repair; Force never reports a cache hit.

### [case 3.4 active-version-and-pinned-cache]

Operation: publish 2.0.0, wait up to 30 seconds for the active-version install
and body to converge, then install pinned 1.0.0.

Expected: active version is 2.0.0 with matching body; the old version is a
cache hit and the directories do not conflict.

### [case 3.5 multiple-skill-dedup-and-sorting]

Operation: submit two real Skill references to the Loader in reverse order with
duplicates.

Expected: the Repository exposes only the two selected entrypoints with stable
ordering and readable content.

### [case 3.6 merge-mount-retains-latest-references]

Operation: capture an old Discovery service snapshot, update real Console
references, then run Loader merge with `UseServiceSkills`.

Expected: the complete set comes from the latest Writer response and does not
overwrite updated references.

### [case 3.7 replace-and-empty-mount-remove-old-skills]

Operation: mount two real Skills to the service, replace them with one through
the Loader, then replace with an empty set.

Expected: only the new set executes, cached old entrypoints are not exposed
after install, and Console accepts and persists the explicit empty reference
set.

### [case 3.8 multiple-entry-skill-is-rejected]

Operation: publish a real ZIP containing an extra `SKILL.md` and invoke the
Loader.

Expected: download or entrypoint validation fails and no extra executable
Repository is exposed.

### [case 3.9 provider-executes-published-skill]

Operation: a real Provider installs a pinned Skill during Prepare; its
deterministic Runner reads `summary.Name` and original body from the
Repository and returns them through real A2A.

Expected: the response contains the installed Skill name and pinned 1.0.0
body. It is not a fabricated local install result and does not require an LLM
key.

### [case 4.1 ordinary-service-type-conflict]

Operation: create this run's ordinary microservice through Console and enable
`EnsureService` in Runtime.

Expected: an explicit type conflict; Prepare does not expose an AgentCard and
does not convert the existing service into AI_AGENT.

### [case 4.2 prepare-retry-and-borrowed-context-lifecycle]

Operation: fail Prepare for a missing Skill, publish the real Skill and retry,
then repeat Prepare and Close.

Expected: failure removes its provider without a partial Repository; retry
succeeds; the borrowed SDK can still query the real Skill; `NewRemoteAgent` is
rejected after Close.

### [case 4.3 ensure-existing-service-is-nondestructive]

Operation: call Ensure on a real service that already has a Skill reference,
using different metadata and a different Skill.

Expected: existing metadata and Skill references remain unchanged.

### [case 4.4 cleanup-state-and-ownership-safeguards]

Operation: validate invalid states containing the default namespace, a
production service name, an access-bearing URL, and the filesystem root; then
run this run's dry-run.

Expected: invalid states are rejected without leaking the synthetic credential;
dry-run preserves actual resources.

## Troubleshooting

- Requests fail through a corporate proxy: example HTTP Clients connect
  directly to explicit addresses. If the SDK Writer is affected by environment
  proxies, add the Polaris host to `NO_PROXY`.
- Discovery or Skill timeout: confirm ports 8091/8094 are reachable and the
  server capabilities and tokens are valid. A transport failure is never a
  pass.
- `AgentAPI.GetService` reports no service-level revision: each `SERVICES`
  entry must project its own revision, AI_AGENT type, and extended metadata.
  A list-level revision is not a substitute. Read-only inspect records both
  `SERVICES` and `INSTANCE`; if `INSTANCE` also lacks type or extended
  metadata, upgrade the server. Do not infer service type from instance
  metadata or use Console as a substitute for Discovery. Keep the failure for
  a server-capability gap.
- Console says published but the client returns SkillNotFound: verify the real
  read path. A second-granularity mtime cache can miss a create/publish in the
  same second. The example uses an explicit draft/publish flow and still waits
  for client visibility; a successful Console write is not a successful
  download.
- Provider is not ready: read this run's `providers/<node>/provider.log` and
  check the explicit Prepare, Skill-install, and registration errors.
- Explicit empty extended metadata is not applied: the server must support the
  clear-write contract. Do not weaken the empty-set assertion to make it pass.
- Historical empty services remain: run
  `./cleanup.sh --delete-service --dry-run`, review the namespace, owner, and
  empty-instance filter, then use `--apply`. Do not bypass the owner/prefix
  checks and delete by service-name pattern.
- Cleanup rejects ownership or PID: run dry-run and inspect state. Only touch
  this run's resources. Never delete by broad service-name or process-name
  matching.

This document describes coverage, not execution success. The actual result for
a run is in that run's `results.json` and the project `cr.md`.

Read-only protocol diagnostics:

```bash
./.build/e2e --mode=inspect --namespace=go-agent-naming-e2e \
  --run-id=<run-id> --run-dir=.logs/<run-id> \
  --service=go-agent-e2e-<run-id>-main \
  --skill-name=gan-e2e-<run-id>-b --skill-version=1.0.0
```

This writes `service-projection.json` and `skill-projection.json`. The service
file records whether the `SERVICES` list and `INSTANCE` response contain their
own revision, AI_AGENT type, run metadata, and extended metadata; one
projection cannot substitute for the other. The files contain only identity,
field capability, and status, never tokens, raw metadata, or Skill bodies.
This diagnostic is not an E2E pass.
