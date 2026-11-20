English | [Chinese](test.zh_CN.md)

# A2A Registration, Discovery, Streaming and Cancellation

## Test Function

`TestA2ARegistrationDiscoveryStreamingAndCancellation` in
[e2e_test.go](e2e_test.go).

## Processes

| Process | Count | Purpose |
| --- | --- | --- |
| `go test` test binary | 1 | Creates the Polaris Context, starts the in-process A2A HTTP listener, registers the provider instance, runs the consumer, and performs assertions |
| A2A server | In-process | Uses the real HTTP handler and a loopback listener; no separate provider subprocess |
| Polaris | External | Supplied by `POLARIS_SERVER`; the test does not start a Polaris server |

There is no standalone Provider or Client process. The test intentionally runs
the provider server in the test process so the full A2A wire path can be
verified without shell orchestration.

## Services

| Resource | Value |
| --- | --- |
| Namespace | `go-agent-naming-e2e` by default |
| Service | `gan-e2e-<run-id>-a2a` |
| Type | `AI_AGENT` |
| Instances | 1 healthy instance |
| Instance host | `127.0.0.1` |
| Registration port | `1` |
| Instance version | `v1.0.0` |
| Instance protocol | Empty |
| Metadata | `ai-agent-name`, `ai-agent-card`, `ai-a2a-version=1.0` |
| Business A2A port | Random loopback port |
| A2A path | `/a2a` |

The service is created through the Console service writer before registration
and removed after the case.

## Call Relationship

```text
test consumer Runtime
        │
        ├─► Polaris Discover ─► AI_AGENT instance
        │
        └─► AgentCard JSONRPC endpoint ─► in-process A2A server
                                             │
                                             └─► EchoAgent
                                                   │
        ◄──────────── A2A events ──────────────────┘
```

The consumer selects the JSONRPC interface from the discovered AgentCard; it
does not construct the business URL from `instance.host:instance.port`.

## Cases

### [case 1.1 registration and discovery]

Operation: create the AI_AGENT service, prepare the Runtime, register the
instance through `go-naming-polarismesh`, and wait for a healthy instance.

Expected: exactly one healthy instance has the three AgentCard metadata keys,
version `v1.0.0`, registration port `1`, and empty protocol.

### [case 1.2 unary invocation]

Operation: resolve the remote Agent through Polaris with binding `JSONRPC` and
send `hello`.

Expected: the response is `echo|v1.0.0|hello`.

### [case 1.3 streaming invocation]

Operation: send `stream` through the streaming A2A path.

Expected: the event stream contains at least four real events and the final
text contains the stream response.

### [case 1.4 cancellation]

Operation: start the continuous `hold` stream, consume one event, and cancel
the run context.

Expected: the provider observes cancellation (`Canceled > 0`) and returns to
zero active invocations within the bounded wait.

## Verification

The test asserts:

- `ai-agent-name`, `ai-agent-card`, and `ai-a2a-version=1.0`.
- Instance version `v1.0.0`, registration port `1`, and empty protocol.
- AgentCard discovery through Polaris.
- Exact unary response.
- Streaming output and event count.
- Provider cancellation and active-invocation cleanup.

## Cleanup

The provider is deregistered and the AI_AGENT service is deleted in
`t.Cleanup`. The shared namespace is retained.

## Run

```bash
POLARIS_SERVER=your-polaris-host \
  go test -tags=integration -count=1 -v -timeout=10m ./integration/a2a
```
