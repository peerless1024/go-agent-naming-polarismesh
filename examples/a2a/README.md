English | [Chinese](README.zh_CN.md)

# A2A Agent With Polaris

This example is the A2A counterpart of the official `trpc-agent-go` examples:
an LLM Agent with a function tool is served over A2A and registered in Polaris.
The Client resolves the AgentCard and calls the Agent through Polaris
discovery.

```text
Provider -> Runtime.Prepare -> ai-agent-name / ai-agent-card / ai-a2a-version
         -> go-naming-polarismesh -> A2A HTTP Server

Client   -> Polaris Discover -> AgentCard -> JSONRPC /a2a -> Agent
```

## Prerequisites

- Go 1.21+.
- A Polaris deployment with AI Registry support.
- An OpenAI-compatible model endpoint.

```bash
export POLARIS_SERVER=your-polaris-host
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://your-openai-compatible-endpoint/v1
```

## Run

Terminal 1 starts the provider:

```bash
go run ./examples/a2a/provider
```

Terminal 2 calls it through Polaris:

```bash
go run ./examples/a2a/client
```

Use `-interactive` for a chat loop:

```bash
go run ./examples/a2a/client -interactive
```

The provider creates the AI_AGENT service automatically through the Console
service writer. It listens on `127.0.0.1:18191` and advertises the A2A path
`/a2a` in the AgentCard.

## What Polaris Adds

`Runtime.Prepare` freezes the AgentCard and installs three instance metadata
keys:

```text
ai-agent-name  = trpc.polaris.examples.a2a
ai-agent-card  = <complete A2A 1.0 AgentCard JSON>
ai-a2a-version = 1.0
```

The registration port is `1` and the instance protocol is empty. The actual
business endpoint comes from the AgentCard interface URL. The Client selects an
interface by `protocolBinding` and keeps a sticky instance for the caller key.

## Cleanup

```bash
./examples/a2a/cleanup.sh --dry-run
./examples/a2a/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

The script deletes only the configured A2A service after verifying that every
instance belongs to that Agent.

## Useful Flags

| Flag | Default |
| --- | --- |
| `-namespace` | `go-agent-naming-examples` |
| `-service` | `trpc.polaris.examples.a2a` |
| `-model` | `deepseek-chat` |
| `-port` | `18191` |
| `-registration-port` | `1` |
| `-polaris-port` | `8091` |
| `-console-port` | `8090` |

See the [integration suite](../../integration/a2a/e2e_test.go) for the
automated registration, discovery, streaming, and cancellation checks.
