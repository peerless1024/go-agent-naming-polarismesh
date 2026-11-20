English | [Chinese](README.zh_CN.md)

# A2A AgentCard: Multi-Interface and Multi-Version Calls

This example shows a registered Agent A calling a registered Agent B service
with two live instances running different versions:

```text
Client ──► Polaris Discover ──► Agent A
                                  │
                                  │ discovers B via Polaris
                    ┌─────────────┴─────────────┐
                    ▼                           ▼
             Agent B v1.0.0              Agent B v2.0.0
                    │                           │
                    └───────── responses ───────┘
                                  │
                                  ▼
                           Agent A aggregates
                                  │
                                  ▼
                                Client
```

Agent A is itself registered through Polaris. On startup it resolves Agent B
and selects one stable caller key for each required version. Every request to
Agent A therefore calls both versioned Agent B instances and returns their
combined responses. No LLM key is required.

The provider starts in the default multi-interface A2A mode when `-role` is
empty. Set `-role agent-a` or `-role agent-b` for the multi-version flow below.

## Run

Set the Polaris host once:

```bash
export POLARIS_SERVER=your-polaris-host
```

Terminal 1 starts Agent B version `v1.0.0`:

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -role agent-b \
  -service trpc.polaris.demo.agent.b \
  -version v1.0.0 \
  -port 28221
```

Terminal 2 starts another Agent B instance with version `v2.0.0`:

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -role agent-b \
  -service trpc.polaris.demo.agent.b \
  -version v2.0.0 \
  -port 28222 \
  -registration-port 2
```

Terminal 3 starts registered Agent A after both Agent B instances are ready:

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -role agent-a \
  -service trpc.polaris.demo.agent.a \
  -target-service trpc.polaris.demo.agent.b \
  -target-versions v1.0.0,v2.0.0 \
  -port 28220
```

Terminal 4 calls Agent A:

```bash
go run ./examples/a2a-agentcard/client \
  -server "$POLARIS_SERVER" \
  -service trpc.polaris.demo.agent.a \
  -target-versions v1.0.0,v2.0.0
```

The Client prints:

```json
{
  "success": true,
  "events": 1,
  "response": "trpc.polaris.demo.agent.a|fanout|b=v1.0.0:agent-b|v1.0.0|echo:hello-from-agent-a;b=v2.0.0:agent-b|v2.0.0|echo:hello-from-agent-a"
}
```

Defaults:

| Item | Value |
| --- | --- |
| Namespace | `go-agent-naming-e2e` |
| Agent A service | `trpc.polaris.demo.agent.a` |
| Agent B service | `trpc.polaris.demo.agent.b` |
| Agent B versions | `v1.0.0` and `v2.0.0` |
| Agent A port | `28220` |
| Agent B ports | `28221` and `28222` |
| Registration port | Agent A and B v1 use `1`; B v2 uses `2` |
| Protocol binding | `JSONRPC` |

## Verify

```bash
./examples/a2a-agentcard/verify.sh --polaris-server "$POLARIS_SERVER"
./examples/a2a-agentcard/cleanup.sh --dry-run
./examples/a2a-agentcard/cleanup.sh --force
./examples/a2a-agentcard/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

`verify.sh` leaves all three Providers running. `cleanup.sh --force` stops the
tracked processes and removes local build/log artifacts. Add `--delete-service`
to delete both remote services after their owned instances are removed. See
[test.md](../../integration/a2a-agentcard/test.md) for the cases.

The same flow is covered by the real
[a2a-agentcard integration suite](../../integration/a2a-agentcard/e2e_test.go).

## Default AgentCard Multi-Protocol Enhancement

By default, the provider enables the Polaris AI Registry AgentCard
multi-protocol enhancement. One Agent service instance can declare multiple
A2A protocol interfaces and publish them through one AgentCard. Set
`-role agent-a` or `-role agent-b` to use the multi-version flow instead:

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -a2a-version 1.0 \
  -service document-agent \
  -version 1.0.0 \
  -port 0 \
  -registration-port 1
```

The default mode serves one AgentCard and one Polaris instance with three interfaces
on the same IP and business port:

| Interface | Path | Binding |
| --- | --- | --- |
| A2A 1.0 JSON-RPC | `/a2a/v1` | `JSONRPC` |
| A2A 0.3 JSON-RPC | `/a2a/legacy` | `JSONRPC` |
| A2A 1.0 HTTP+JSON | `/rest` | `HTTP+JSON` |

Tokens can come from `POLARIS_TOKEN` / `POLARIS_SERVICE_TOKEN` or from
`-token` / `-service-token`.

The card is also served at `/.well-known/agent-card.json`. Use
`-a2a-version 0.3` for a 0.3 card schema. To register an external card without
binding a server, add `-no-serve -card /path/to/agent-card.json`.
Without `-no-serve`, `-card` keeps the file's description, skills, and other
card fields while rewriting the three interface URLs to the bound host and
port.
When `-card` is present, the card's `name` and `version` determine the
service and instance identity; `-service` and `-version` are fallbacks.

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -a2a-version 1.0 \
  -card /path/to/agent-card.json \
  -host 127.0.0.1 \
  -port 0 \
  -registration-port 1
```

The integration suite starts the default mode with both `1.0` and `0.3` cards,
invokes all three interfaces, and verifies that no-serve preserves the card
bytes exactly.

For a manually started multi-protocol service, pass its service name to the
cleanup script:

```bash
./examples/a2a-agentcard/cleanup.sh \
  --force --delete-service \
  --polaris-server "$POLARIS_SERVER" \
  --service document-agent
```
