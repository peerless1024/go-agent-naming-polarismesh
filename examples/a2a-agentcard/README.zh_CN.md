[English](README.md) | 中文

# A2A AgentCard 多接口与多版本调用

这个示例展示已注册的 Agent A 调用已注册的 Agent B 服务。Agent B 有两个
存活实例，并分别运行不同版本：

```text
Client ──► Polaris Discover ──► Agent A
                                  │
                                  │ 通过 Polaris 发现 B
                    ┌─────────────┴─────────────┐
                    ▼                           ▼
             Agent B v1.0.0              Agent B v2.0.0
                    │                           │
                    └───────── 响应 ────────────┘
                                  │
                                  ▼
                           Agent A 聚合
                                  │
                                  ▼
                                Client
```

Agent A 自身也会通过 Polaris 注册。启动时，它会解析 Agent B，并为每个
目标版本选择一个稳定 caller key。因此每次请求 Agent A 都会调用两个不同
版本的 Agent B 实例，并返回聚合响应。不需要 LLM key。

不传 `-role` 时，Provider 默认启动多接口 A2A 模式；需要多版本 Agent A/B
链路时显式设置 `-role agent-a` 或 `-role agent-b`。

## 运行

先设置 Polaris 地址：

```bash
export POLARIS_SERVER=your-polaris-host
```

终端 1 启动 `v1.0.0` 的 Agent B：

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -role agent-b \
  -service trpc.polaris.demo.agent.b \
  -version v1.0.0 \
  -port 28221
```

终端 2 启动 `v2.0.0` 的另一个 Agent B 实例：

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -role agent-b \
  -service trpc.polaris.demo.agent.b \
  -version v2.0.0 \
  -port 28222 \
  -registration-port 2
```

两个 Agent B 实例就绪后，终端 3 启动已注册的 Agent A：

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -role agent-a \
  -service trpc.polaris.demo.agent.a \
  -target-service trpc.polaris.demo.agent.b \
  -target-versions v1.0.0,v2.0.0 \
  -port 28220
```

终端 4 调用 Agent A：

```bash
go run ./examples/a2a-agentcard/client \
  -server "$POLARIS_SERVER" \
  -service trpc.polaris.demo.agent.a \
  -target-versions v1.0.0,v2.0.0
```

Client 输出：

```json
{
  "success": true,
  "events": 1,
  "response": "trpc.polaris.demo.agent.a|fanout|b=v1.0.0:agent-b|v1.0.0|echo:hello-from-agent-a;b=v2.0.0:agent-b|v2.0.0|echo:hello-from-agent-a"
}
```

默认值：

| 项目 | 值 |
| --- | --- |
| Namespace | `go-agent-naming-e2e` |
| Agent A 服务 | `trpc.polaris.demo.agent.a` |
| Agent B 服务 | `trpc.polaris.demo.agent.b` |
| Agent B 版本 | `v1.0.0` 和 `v2.0.0` |
| Agent A 端口 | `28220` |
| Agent B 端口 | `28221` 和 `28222` |
| Registration port | Agent A 和 B v1 使用 `1`；B v2 使用 `2` |
| Protocol binding | `JSONRPC` |

## 验证

```bash
./examples/a2a-agentcard/verify.sh --polaris-server "$POLARIS_SERVER"
./examples/a2a-agentcard/cleanup.sh --dry-run
./examples/a2a-agentcard/cleanup.sh --force
./examples/a2a-agentcard/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

`verify.sh` 会保留三个 Provider 进程。`cleanup.sh --force` 停止被跟踪的
进程并删除本地构建与日志产物；追加 `--delete-service` 时，会在确认实例
归属后删除两个远端服务。用例说明见
[test.zh_CN.md](../../integration/a2a-agentcard/test.zh_CN.md)。

同一链路也由真实
[a2a-agentcard 集成测试](../../integration/a2a-agentcard/e2e_test.go)
覆盖。

## 默认 AgentCard 多协议增强场景

Provider 默认启用北极星 AI 注册中心的 AgentCard 多协议增强能力：
同一个 Agent 服务实例可声明多套 A2A 协议接口，并通过一张 AgentCard
对外发布。需要多版本链路时改为显式设置 `-role agent-a` 或
`-role agent-b`：

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -a2a-version 1.0 \
  -service document-agent \
  -version 1.0.0 \
  -port 0 \
  -registration-port 1
```

默认模式在同一 IP 和业务端口上提供一张 AgentCard、一个 Polaris 实例和三个接口：

| 接口 | 路径 | 绑定 |
| --- | --- | --- |
| A2A 1.0 JSON-RPC | `/a2a/v1` | `JSONRPC` |
| A2A 0.3 JSON-RPC | `/a2a/legacy` | `JSONRPC` |
| A2A 1.0 HTTP+JSON | `/rest` | `HTTP+JSON` |

Token 可通过 `POLARIS_TOKEN` / `POLARIS_SERVICE_TOKEN` 或
`-token` / `-service-token` 提供。

AgentCard 也可从 `/.well-known/agent-card.json` 读取。使用
`-a2a-version 0.3` 选择 0.3 卡片 schema；增加
`-no-serve -card /path/to/agent-card.json` 可以只注册外部卡片而不绑定服务。
不带 `-no-serve` 时，`-card` 会保留文件中的 description、skills 等字段，
只把三个 interface URL 改写为实际绑定的 host 和 port。
使用 `-card` 时，服务名和实例版本以卡片中的 `name`、`version` 为准，
`-service`、`-version` 只作为兜底值。

```bash
go run ./examples/a2a-agentcard/provider \
  -server "$POLARIS_SERVER" \
  -a2a-version 1.0 \
  -card /path/to/agent-card.json \
  -host 127.0.0.1 \
  -port 0 \
  -registration-port 1
```

集成套件会分别启动 `1.0` 和 `0.3` 卡片，调用三个接口，并验证 no-serve
模式原样保留 AgentCard 字节。

手工启动多协议增强服务后，可通过 `--service` 让清理脚本删除对应服务：

```bash
./examples/a2a-agentcard/cleanup.sh \
  --force --delete-service \
  --polaris-server "$POLARIS_SERVER" \
  --service document-agent
```
