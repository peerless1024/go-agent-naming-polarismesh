[English](README.md) | 中文

# 通过北极星使用 MCP 工具

这个示例沿用 `trpc-agent-go` 官方 MCP Tool 示例，并补上北极星 AI Registry
链路：

```text
MCP Server -> PrepareServer -> mcp-server-name / protocol / path
           -> CollectContract + ReportContract -> MCP Service Contract
           -> go-naming-polarismesh

Agent      -> Polaris Discover -> 健康 MCP 实例 -> 官方 MCP 工具
           -> LLM Agent -> A2A Server -> 北极星 AI_AGENT 注册
```

## 前置条件

- Go 1.21 或更新版本。
- 支持 MCP Service Contract 的北极星环境。
- 一个 OpenAI 兼容的模型服务。

```bash
export POLARIS_SERVER=your-polaris-host
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://your-openai-compatible-endpoint/v1
```

## 运行

终端 1 启动 MCP Server：

```bash
go run ./examples/mcp/server
```

它暴露两个工具：

| 工具 | 说明 |
| --- | --- |
| `get_weather` | 返回指定城市的确定性天气结果 |
| `add` | 计算两个数之和 |

终端 2 启动 Agent。Agent 会通过北极星发现 MCP 实例、读取工具声明，并把
自己以 A2A 服务暴露：

```bash
go run ./examples/mcp/agent
```

终端 3 调用 Agent：

```bash
go run ./examples/mcp/client
```

也可以测试加法：

```bash
go run ./examples/mcp/client -message "Use the add tool to calculate 19 + 23."
```

## MCP 与北极星的结合点

MCP Server 注册三个实例 metadata：

```text
mcp-server-name
mcp-protocol-version
mcp-endpoint-path
```

同时上报 type 为 `mcp` 的真实 Service Contract。Agent 使用适配层
`mcp.Pool` 和 `ConsumerAPI` 发现健康实例；示例 ToolSet 从真实 MCP endpoint
读取工具声明，并通过 Pool 执行每次工具调用。这样服务发现、健康过滤、调用
上报和连接生命周期都不需要进入模型提示词。

Agent 自身仍是一个普通 A2A 服务，调用方只需要知道 Agent 服务名，不需要
知道最终选中了哪个 MCP 实例。
示例还会把发现的 MCP 工具声明传给 `adapteragent.WithCardTools`，因此
AgentCard 会把它们公布为 A2A skills。

## 清理

```bash
./examples/mcp/cleanup.sh --dry-run
./examples/mcp/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

脚本会删除 MCP 服务、其 Service Contract 以及 Agent 服务；如果 MCP 服务
上存在不属于该服务的实例，脚本会拒绝删除。

## 常用参数

| 参数 | 默认值 |
| --- | --- |
| `-namespace` | `go-agent-naming-examples` |
| `-service`（server） | `trpc.polaris.examples.mcp` |
| `-agent-service`（agent） | `trpc.polaris.examples.mcp-agent` |
| `-model` | `deepseek-chat` |
| `-port`（MCP server） | `18494` |
| `-port`（Agent） | `18192` |
| `-mcp-path` | `/mcp` |

metadata、Contract、发现和 Pool 调用的自动验证见
[MCP 集成测试](../../integration/mcp/e2e_test.go)。
