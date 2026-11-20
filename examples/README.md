English | [Chinese](README.zh_CN.md)

# Examples

These examples follow the runnable style of `trpc-agent-go`: one small Go
program per role, a real Agent Runner, and a real Polaris AI Registry. They
show how A2A, MCP, and Agent Skills work together rather than replacing the
Agent with a deterministic stub.

| Example | What it demonstrates | LLM key |
| --- | --- | --- |
| [a2a](./a2a) | An LLM Agent served over A2A, registered with AgentCard metadata, discovered and called through Polaris | Yes |
| [mcp](./mcp) | An MCP server with a real Service Contract; an Agent discovers it through Polaris and uses its tools over A2A | Yes |
| [skill](./skill) | Publish a Skill to Polaris; an LLM Agent installs it, loads it with `skill_load`, and serves A2A | Yes |
| [a2a-agentcard](./a2a-agentcard) | Registered Agent A calls two live versions of registered Agent B without an LLM | No |

The `a2a-agentcard` provider defaults to the Polaris AI Registry AgentCard
multi-protocol enhancement; explicit `-role agent-a|agent-b` selects the
multi-version flow.

## Prerequisites

- Go 1.21 or newer.
- A Polaris deployment with AI Registry, MCP Service Contract, and Skill
  Registry support.
- An OpenAI-compatible model endpoint for `a2a`, `mcp`, and `skill`.

Common environment variables:

| Variable | Purpose | Default |
| --- | --- | --- |
| `POLARIS_SERVER` | Polaris host | required |
| `POLARIS_GRPC_PORT` | Discovery gRPC port | `8091` |
| `POLARIS_HTTP_PORT` | Console HTTP port | `8090` |
| `POLARIS_SKILL_GRPC_PORT` | Skill gRPC port | `8094` |
| `POLARIS_TOKEN` | Discovery token | empty |
| `POLARIS_CONSOLE_TOKEN` | Console token | fallback to `POLARIS_TOKEN` |
| `POLARIS_SKILL_TOKEN` | Skill token | fallback to `POLARIS_TOKEN` |
| `OPENAI_API_KEY` | Model API key | required by LLM examples |
| `OPENAI_BASE_URL` | OpenAI-compatible endpoint | provider default |
| `MODEL_NAME` | Model name | `deepseek-chat` |

All LLM examples use `openai.New` from `trpc-agent-go`, so the standard
`OPENAI_*` environment variables apply.

## Run

Every example has a README with the exact commands. For example:

```bash
export POLARIS_SERVER=your-polaris-host
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://your-openai-compatible-endpoint/v1

go run ./examples/mcp/server
go run ./examples/mcp/agent
go run ./examples/mcp/client
```

The `a2a-agentcard` example has no model dependency:

```bash
./examples/a2a-agentcard/verify.sh --polaris-server "$POLARIS_SERVER"
```

## Cleanup

Each example directory has a `cleanup.sh`. `--dry-run` previews the configured
remote scope without writing. `--delete-service` removes the example's services
and, for the Skill example, the published Skill. `--force` skips the
confirmation prompt.

The `a2a`, `mcp`, and `skill` programs run in the foreground; stop them with
`Ctrl+C` before deleting remote resources. `a2a-agentcard/verify.sh` tracks its
detached Agent A/B processes, and `a2a-agentcard/cleanup.sh --force` stops them.

```bash
./examples/a2a/cleanup.sh --dry-run
./examples/a2a/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"

./examples/mcp/cleanup.sh --dry-run
./examples/mcp/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"

./examples/skill/cleanup.sh --dry-run
./examples/skill/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

The scripts verify service names, instance Agent/MCP identity, and the Skill
owner before deleting anything. `--dry-run` queries the real Console and never
writes.

## Integration

[`integration/`](../integration) contains automated real-service checks for
the three protocols and the retained `a2a-agentcard` flow. They use
deterministic or scripted models, so they do not need an LLM key. See the
[integration guide](../integration/README.md).
