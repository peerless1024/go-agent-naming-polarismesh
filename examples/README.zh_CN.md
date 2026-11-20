[English](README.md) | 中文

# 示例

这些示例沿用 `trpc-agent-go` 官方示例的写法：每个角色一个小型 Go 程序，
使用真实的 Agent Runner 和真实的北极星 AI Registry，重点展示 A2A、MCP 和
Agent Skill 如何组合使用，而不是用确定性桩替代 Agent。

| 示例 | 展示内容 | 是否需要 LLM key |
| --- | --- | --- |
| [a2a](./a2a) | LLM Agent 通过 A2A 暴露；写入 AgentCard 元数据；Client 经北极星发现并调用 | 需要 |
| [mcp](./mcp) | MCP Server 上报真实 Service Contract；Agent 经北极星发现 MCP 并通过 A2A 提供工具型 Agent | 需要 |
| [skill](./skill) | 向北极星发布 Skill；LLM Agent 安装后用 `skill_load` 加载，并通过 A2A 提供服务 | 需要 |
| [a2a-agentcard](./a2a-agentcard) | 已注册 Agent A 调用两个不同版本的已注册 Agent B，不需要 LLM | 不需要 |

`a2a-agentcard` 的 Provider 默认验证北极星 AI 注册中心的 AgentCard
多协议增强场景；显式设置 `-role agent-a|agent-b` 才进入多版本调用流程。

## 前置条件

- Go 1.21 或更新版本。
- 支持 AI Registry、MCP Service Contract 和 Skill Registry 的北极星环境。
- `a2a`、`mcp`、`skill` 需要一个 OpenAI 兼容的模型服务。

常用环境变量：

| 变量 | 用途 | 默认值 |
| --- | --- | --- |
| `POLARIS_SERVER` | 北极星地址 | 必填 |
| `POLARIS_GRPC_PORT` | 服务发现 gRPC 端口 | `8091` |
| `POLARIS_HTTP_PORT` | Console HTTP 端口 | `8090` |
| `POLARIS_SKILL_GRPC_PORT` | Skill gRPC 端口 | `8094` |
| `POLARIS_TOKEN` | 服务发现 token | 空 |
| `POLARIS_CONSOLE_TOKEN` | Console token | 回退到 `POLARIS_TOKEN` |
| `POLARIS_SKILL_TOKEN` | Skill token | 回退到 `POLARIS_TOKEN` |
| `OPENAI_API_KEY` | 模型 API key | LLM 示例必填 |
| `OPENAI_BASE_URL` | OpenAI 兼容地址 | 模型提供方默认值 |
| `MODEL_NAME` | 模型名 | `deepseek-chat` |

LLM 示例统一使用 `trpc-agent-go` 的 `openai.New`，因此标准 `OPENAI_*`
环境变量直接生效。

## 运行

每个示例的 README 都给出了完整命令。例如：

```bash
export POLARIS_SERVER=your-polaris-host
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://your-openai-compatible-endpoint/v1

go run ./examples/mcp/server
go run ./examples/mcp/agent
go run ./examples/mcp/client
```

`a2a-agentcard` 不依赖模型：

```bash
./examples/a2a-agentcard/verify.sh --polaris-server "$POLARIS_SERVER"
```

## 清理

每个示例目录都有 `cleanup.sh`。`--dry-run` 只预览配置的远端资源范围，不写入；
`--delete-service` 删除示例创建的服务，Skill 示例还会删除已发布 Skill；
`--force` 跳过确认提示。

`a2a`、`mcp` 和 `skill` 程序以前台方式运行，删除远端资源前请先用 `Ctrl+C`
停止进程。`a2a-agentcard/verify.sh` 会记录分离运行的 Agent A/B 进程，
`a2a-agentcard/cleanup.sh --force` 负责停止这些进程。

```bash
./examples/a2a/cleanup.sh --dry-run
./examples/a2a/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"

./examples/mcp/cleanup.sh --dry-run
./examples/mcp/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"

./examples/skill/cleanup.sh --dry-run
./examples/skill/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

脚本会先校验服务名、实例的 Agent/MCP 身份和 Skill owner，再执行删除。
`--dry-run` 只查询真实 Console，不写入。

## 集成测试

[`integration/`](../integration) 提供 A2A、MCP、Skill 以及保留的
`a2a-agentcard` 链路的真实服务自动验证。测试使用确定性或脚本化模型，
因此不需要 LLM key。运行方式见
[集成测试说明](../integration/README.zh_CN.md)。
