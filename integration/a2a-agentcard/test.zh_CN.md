[English](test.md) | 中文

# A2A AgentCard 多接口与多版本测试

## Integration 测试进程

测试启动前由 `make -C integration build` 生成示例二进制；测试只启动预构建文件，
不会调用 `go build` 或 `examples/a2a-agentcard/verify.sh`。
`a2a-agentcard.test` 进程作为编排器，通过 `ConsumerAPI` 访问北极星；北极星
服务端为外部部署。

| 测试 | 拉起的子进程 | 注册的服务 |
| --- | --- | --- |
| `TestA2AAgentCardExampleAcrossProcesses` | 3 个 Provider：Agent B v1、Agent B v2、Agent A；1 个 Client | `gan-e2e-<run-id>-agent-a`、`gan-e2e-<run-id>-agent-b` |
| `TestAgentCardMultiProtocolProvider/schema-10` | 1 个 Provider | `gan-e2e-<run-id>-agentcard-10` |
| `TestAgentCardMultiProtocolProvider/schema-03` | 1 个 Provider | `gan-e2e-<run-id>-agentcard-03` |
| `TestAgentCardMultiProtocolProvider/no-serve` | 1 个 Provider，不启动业务 listener | `gan-e2e-<run-id>-agentcard-noserve` |

Agent A 和 Agent B v1 使用 registration port `1`，Agent B v2 使用
registration port `2`；AgentCard 多协议场景使用 registration port `1`。A2A
业务端口均动态分配。

## Integration 调用关系

Agent A/B：

```text
测试进程 ─► Client 子进程
                 │
                 └─► Polaris Discover ─► Agent A
                                           │
                                           ├─► Agent B v1.0.0
                                           └─► Agent B v2.0.0
                 ◄──── 聚合响应 ───────────┘
测试进程 ─► ConsumerAPI ─► 等待健康实例并校验 metadata
```

AgentCard 多协议场景：

```text
测试进程 ─► Provider 子进程 ─► /.well-known/agent-card.json
                 │                        │
                 ├─► /a2a/v1              └─► 校验一张卡 / 三个接口
                 ├─► /a2a/legacy
                 └─► /rest/message:send

测试进程 ─► ConsumerAPI ─► 发现一个实例并校验 metadata
```

## 手工示例流程

Integration 套件不会执行示例的 verify 脚本。以下内容说明
[README.zh_CN.md](../../examples/a2a-agentcard/README.zh_CN.md) 中独立运行的
Provider/Client 手工流程；它使用固定服务名，与上面的 run-scoped integration
服务名不同。

### 手工示例的服务、进程与调用关系

- 在 `go-agent-naming-e2e` 中注册两个服务：
  `trpc.polaris.demo.agent.a` 包含一个 `v1.0.0` 实例；
  `trpc.polaris.demo.agent.b` 包含两个存活实例，`v1.0.0` 使用
  registration port `1`、业务端口 `28221`，`v2.0.0` 使用 registration port
  `2`、业务端口 `28222`。
- `verify.sh` 构建并拉起三个 `.build/provider` 进程，其中两个为 `agent-b`
  角色、一个为 `agent-a` 角色；它们运行在分离会话中，验证结束后保留。随后
  执行一个 `.build/client` 进程。
- Client 不注册到 Polaris。它发现 Agent A 并调用
  `127.0.0.1:28220/a2a`。Agent A 通过 Polaris 发现 Agent B，为每个目标版本
  选择稳定 caller，再直接调用两个 B endpoint；B 将结果返回 A，A 聚合后再返回
  Client。

### [用例 1.1 注册两个不同版本的 Agent B 实例]

操作：创建两个 AI_AGENT 服务，并以同一服务名注册两个 Agent B Provider，
Agent version 分别为 `v1.0.0` 和 `v2.0.0`。

预期：Polaris 暴露两个健康的 Agent B 实例。每个实例包含三个 AgentCard
metadata 键、空实例 protocol，且实例 version 与 Provider 进程的 AgentCard
version 一致。v1 实例使用 registration port `1`，v2 实例使用 registration
port `2`，确保两个 localhost 实例保持独立；A2A endpoint 仍分别在
`28221` 和 `28222`。

### [用例 1.2 已注册 Agent A 调用两个 Agent B 版本]

操作：注册 Agent A；在 Agent A 内部解析 Agent B，并为每个目标版本调用一个
稳定 Remote Agent；最后由独立 Client 请求一次 Agent A。

预期：Agent A 返回一个成功完成的响应，同时包含
`b=v1.0.0:agent-b|v1.0.0|` 与 `b=v2.0.0:agent-b|v2.0.0|` 两段结果，
Client 输出 `success: true`。

## 默认 AgentCard 多协议增强用例

不传 `-role` 时，Provider 默认启用北极星 AI 注册中心的 AgentCard
多协议增强能力，真实验证位于
[e2e_test.go](e2e_test.go)。

### [用例 2.1 多接口 1.0 卡片]

操作：不传 `-role`，使用 `-a2a-version 1.0` 启动 Provider，可选传入
`-card` 文件。

预期：一张 AgentCard 在同一 IP/业务端口上包含 `/a2a/v1` JSON-RPC 1.0、
`/a2a/legacy` JSON-RPC 0.3 和 `/rest` HTTP+JSON 1.0。实例包含三个
AgentCard metadata 键、registration port `1`、空 protocol 和卡片 version；
card endpoint 返回相同 JSON；三个调用返回统一的 Message 和
`SendMessageResponse` 结构。

### [用例 2.2 多接口 0.3 卡片]

操作：不传 `-role`，使用 `-a2a-version 0.3` 启动 Provider。

预期：卡片使用顶层 `url`/`protocolVersion`/`preferredTransport`，并通过
`additionalInterfaces` 声明 modern 和 REST 绑定。三个接口仍可调用，实例仍
只有一个注册，`ai-a2a-version=0.3`。

### [用例 2.3 no-serve 卡片保真]

操作：不传 `-role`，使用 `-no-serve -card /path/to/agent-card.json` 启动。

预期：不启动业务 listener；`ai-agent-card` 与文件字节完全一致，只应用配置的
registration port 和实例字段。

## 运行

```bash
make -C integration build
POLARIS_SERVER=your-polaris-host \
  "integration/bin/$(go env GOOS)-$(go env GOARCH)/a2a-agentcard.test" \
  -test.count=1 -test.v -test.timeout=10m
```

## 清理

Integration 测试跟随测试二进制进程生命周期，在 `t.Cleanup` 中反注册实例并删除
本轮的 run-scoped 服务。

手工示例流程独立运行：`verify.sh` 不停止已分离的进程；
`examples/a2a-agentcard/cleanup.sh --force` 停止被跟踪的 Agent A/B 进程并删除
本地产物，追加 `--delete-service` 时删除手工示例的两个远端服务。
