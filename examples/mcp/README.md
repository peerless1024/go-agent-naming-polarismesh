English | [Chinese](README.zh_CN.md)

# MCP Tools Through Polaris

This example follows the official `trpc-agent-go` MCP tool demo and adds the
Polaris AI Registry path:

```text
MCP Server -> PrepareServer -> mcp-server-name / protocol / path
           -> CollectContract + ReportContract -> MCP Service Contract
           -> go-naming-polarismesh

Agent      -> Polaris Discover -> healthy MCP instance -> official MCP tools
           -> LLM Agent -> A2A Server -> Polaris AI_AGENT registration
```

## Prerequisites

- Go 1.21+.
- A Polaris deployment with MCP Service Contract support.
- An OpenAI-compatible model endpoint.

```bash
export POLARIS_SERVER=your-polaris-host
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://your-openai-compatible-endpoint/v1
```

## Run

Terminal 1 starts the MCP server:

```bash
go run ./examples/mcp/server
```

It exposes:

| Tool | Description |
| --- | --- |
| `get_weather` | Returns a deterministic weather result for a city |
| `add` | Adds two numbers |

Terminal 2 starts the Agent. It discovers the MCP instance through Polaris,
lists its tools, and exposes the Agent over A2A:

```bash
go run ./examples/mcp/agent
```

Terminal 3 calls the Agent:

```bash
go run ./examples/mcp/client
```

Try an arithmetic prompt too:

```bash
go run ./examples/mcp/client -message "Use the add tool to calculate 19 + 23."
```

## How MCP Meets Polaris

The MCP server registers three instance keys:

```text
mcp-server-name
mcp-protocol-version
mcp-endpoint-path
```

It also reports a real Service Contract whose type is `mcp`. The Agent creates
an adapter `mcp.Pool` with a `ConsumerAPI`; the example ToolSet discovers a
healthy instance, reads the tool declarations from the native MCP endpoint, and
routes every tool call through the Pool. This keeps discovery, health
filtering, call reporting, and connection lifecycle outside the model prompt.

The Agent itself is a normal A2A service, so a caller only needs the Agent
service name. It does not need to know which MCP instance was selected.
The discovered MCP tool declarations are also passed to
`adapteragent.WithCardTools`, so the AgentCard advertises them as A2A skills.

## Cleanup

```bash
./examples/mcp/cleanup.sh --dry-run
./examples/mcp/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

The script removes the MCP service, its Service Contract, and the Agent
service. It refuses to delete an MCP service with foreign instances.

## Useful Flags

| Flag | Default |
| --- | --- |
| `-namespace` | `go-agent-naming-examples` |
| `-service` (server) | `trpc.polaris.examples.mcp` |
| `-agent-service` (agent) | `trpc.polaris.examples.mcp-agent` |
| `-model` | `deepseek-chat` |
| `-port` (MCP server) | `18494` |
| `-port` (Agent) | `18192` |
| `-mcp-path` | `/mcp` |

See the [MCP integration suite](../../integration/mcp/e2e_test.go) for
metadata, contract, discovery, and pool-call assertions.
