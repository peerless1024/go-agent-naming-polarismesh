English | [Chinese](test.zh_CN.md)

# A2A AgentCard Multi-Interface and Multi-Version Test

## Integration Test Processes

The integration package builds the example binaries into `t.TempDir()` and
starts them as child processes. It does not invoke `examples/a2a-agentcard/verify.sh`.
The `go test` process itself is the orchestrator and consumes Polaris through
`ConsumerAPI`; the Polaris server is external.

| Test | Child processes | Registered services |
| --- | --- | --- |
| `TestA2AAgentCardExampleAcrossProcesses` | 3 providers: Agent B v1, Agent B v2, Agent A; 1 client | `gan-e2e-<run-id>-agent-a`, `gan-e2e-<run-id>-agent-b` |
| `TestAgentCardMultiProtocolProvider/schema-10` | 1 provider | `gan-e2e-<run-id>-agentcard-10` |
| `TestAgentCardMultiProtocolProvider/schema-03` | 1 provider | `gan-e2e-<run-id>-agentcard-03` |
| `TestAgentCardMultiProtocolProvider/no-serve` | 1 provider, no business listener | `gan-e2e-<run-id>-agentcard-noserve` |

Agent A and Agent B v1 use registration port `1`; Agent B v2 uses registration
port `2`. AgentCard multi-protocol instances use registration port `1`. Business
A2A ports are randomly allocated.

## Integration Call Relationships

Agent A/B:

```text
test process ─► client subprocess
                    │
                    └─► Polaris Discover ─► Agent A
                                              │
                                              ├─► Agent B v1.0.0
                                              └─► Agent B v2.0.0
                    ◄──── aggregated response ─┘
test process ─► ConsumerAPI ─► wait for healthy instances and verify metadata
```

AgentCard multi-protocol:

```text
test process ─► provider subprocess ─► /.well-known/agent-card.json
                    │                        │
                    ├─► /a2a/v1              └─► verify one card / three interfaces
                    ├─► /a2a/legacy
                    └─► /rest/message:send

test process ─► ConsumerAPI ─► discover one instance and verify metadata
```

## Manual Example Flow

The integration suite does not execute the example verify script. The following
section documents the separate manual flow that runs the Provider and Client
programs from [README.md](../../examples/a2a-agentcard/README.md). Its fixed
service names differ from the run-scoped integration service names above.

### Manual Example Services, Processes, and Calls

- Registered services in `go-agent-naming-e2e`:
  `trpc.polaris.demo.agent.a` with one `v1.0.0` instance, and
  `trpc.polaris.demo.agent.b` with two live instances:
  `v1.0.0` on registration port `1` and business port `28221`, and `v2.0.0`
  on registration port `2` and business port `28222`.
- `verify.sh` builds and starts three `.build/provider` processes: two
  `agent-b` roles and one `agent-a` role. They run in detached sessions so they
  remain after verification. One `.build/client` process then runs to
  completion.
- The Client is not registered. It discovers Agent A and calls
  `127.0.0.1:28220/a2a`. Agent A discovers Agent B through Polaris, selects one
  stable caller for each required version, and calls both B endpoints directly.
  Agent B returns to A, and A aggregates both responses before returning to the
  Client.

### [case 1.1 register two versioned Agent B instances]

Operation: create both AI_AGENT services and register two Agent B providers
under the same service name with Agent versions `v1.0.0` and `v2.0.0`.

Expected: Polaris exposes two healthy Agent B instances. Each instance carries
the three AgentCard metadata keys, an empty protocol, and the AgentCard version
matching its provider process. The v1 instance uses registration port `1`; the
v2 instance uses registration port `2` so both localhost instances remain
distinct while their A2A endpoints stay on ports `28221` and `28222`.

### [case 1.2 registered Agent A calls both Agent B versions]

Operation: register Agent A, resolve Agent B from inside Agent A, and call one
stable remote Agent for each required version. A standalone Client then invokes
Agent A once.

Expected: Agent A returns one completed response containing both
`b=v1.0.0:agent-b|v1.0.0|` and
`b=v2.0.0:agent-b|v2.0.0|` sections. The Client reports `success: true`.

## Default AgentCard Multi-Protocol Enhancement Cases

When `-role` is empty, the provider enables the Polaris AI Registry AgentCard
multi-protocol enhancement. These cases are implemented by the real
integration suite in
[e2e_test.go](e2e_test.go).

### [case 2.1 multi-interface 1.0 card]

Operation: start the provider without `-role` and with
`-a2a-version 1.0`, optionally with a `-card` file.

Expected: one AgentCard contains `/a2a/v1` JSON-RPC 1.0, `/a2a/legacy`
JSON-RPC 0.3, and `/rest` HTTP+JSON 1.0 on the same IP/business port. The
instance metadata contains the three AgentCard keys, registration port `1`,
empty protocol, and the card version. The card endpoint returns the same JSON.
The three calls return the expected echo Message and `SendMessageResponse`
shapes.

### [case 2.2 multi-interface 0.3 card]

Operation: start the provider without `-role` and with `-a2a-version 0.3`.

Expected: the card uses top-level `url`/`protocolVersion`/`preferredTransport`
and `additionalInterfaces` for the modern and REST bindings. The same three
interfaces remain callable and the instance is still one registration with
`ai-a2a-version=0.3`.

### [case 2.3 no-serve card preservation]

Operation: start without `-role` and with
`-no-serve -card /path/to/agent-card.json`.

Expected: no business listener is started, the card bytes in
`ai-agent-card` exactly match the file, and only the configured registration
port and instance fields are applied.

## Cleanup

Integration tests follow the surrounding `go test` process. They deregister
instances and delete their run-scoped services through `t.Cleanup`.

The manual example flow is independent: `verify.sh` does not stop its detached
processes. `examples/a2a-agentcard/cleanup.sh --force` stops the tracked Agent A
and Agent B processes and removes local artifacts. Add `--delete-service` to
delete the two manual example services.
