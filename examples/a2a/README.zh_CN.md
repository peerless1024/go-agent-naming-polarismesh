[English](README.md) | 中文

# 通过北极星发现 A2A Agent

这个示例对应 `trpc-agent-go` 官方 A2A 示例的写法：一个带 function tool 的
LLM Agent 通过 A2A 暴露，并注册到北极星。Client 从北极星解析 AgentCard，
再调用真实的 A2A 服务。

```text
Provider -> Runtime.Prepare -> ai-agent-name / ai-agent-card / ai-a2a-version
         -> go-naming-polarismesh -> A2A HTTP Server

Client   -> Polaris Discover -> AgentCard -> JSONRPC /a2a -> Agent
```

## 前置条件

- Go 1.21 或更新版本。
- 支持 AI Registry 的北极星环境。
- 一个 OpenAI 兼容的模型服务。

```bash
export POLARIS_SERVER=your-polaris-host
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://your-openai-compatible-endpoint/v1
```

## 运行

终端 1 启动 Provider：

```bash
go run ./examples/a2a/provider
```

终端 2 通过北极星调用：

```bash
go run ./examples/a2a/client
```

交互式聊天：

```bash
go run ./examples/a2a/client -interactive
```

Provider 会通过 Console Service Writer 自动创建 AI_AGENT 服务，监听
`127.0.0.1:18191`，并在 AgentCard 中公布 `/a2a`。

## 北极星提供什么

`Runtime.Prepare` 会冻结 AgentCard，并写入三个实例 metadata：

```text
ai-agent-name  = trpc.polaris.examples.a2a
ai-agent-card  = <完整的 A2A 1.0 AgentCard JSON>
ai-a2a-version = 1.0
```

注册端口固定为 `1`，实例 protocol 为空；真实业务地址来自 AgentCard 中的
interface URL。Client 按 `protocolBinding` 选择接口，并按 caller key 保持
粘性实例。

## 清理

```bash
./examples/a2a/cleanup.sh --dry-run
./examples/a2a/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

脚本会先确认所有实例都属于该 Agent，再删除配置的 A2A 服务。

## 常用参数

| 参数 | 默认值 |
| --- | --- |
| `-namespace` | `go-agent-naming-examples` |
| `-service` | `trpc.polaris.examples.a2a` |
| `-model` | `deepseek-chat` |
| `-port` | `18191` |
| `-registration-port` | `1` |
| `-polaris-port` | `8091` |
| `-console-port` | `8090` |

自动化的注册、发现、流式输出和取消验证见
[集成测试](../../integration/a2a/e2e_test.go)。
