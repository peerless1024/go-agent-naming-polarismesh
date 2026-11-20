# tRPC-Agent-Go 接入北极星 AI Registry

[English](README.md) | [简体中文](README.zh_CN.md)

`trpc-agent-naming-polarismesh` 负责把 tRPC-Agent-Go 接入北极星 AI Registry。
它生成并冻结 A2A AgentCard，通过 `go-naming-polarismesh` 注入实例 metadata，
装配 Skill Repository，解析健康 A2A 实例、创建 A2A Client，并可选适配 MCP
Server、Service Contract、Client Pool 和 Watch。

职责边界：

- `polaris-go` 负责北极星协议、AI Agent 模型和 Skill 传输。
- `go-naming-polarismesh` 负责实例注册、心跳、服务发现和治理。
- 本仓库只负责 tRPC-Agent-Go 适配、AgentCard、Skill 装配和 A2A 生命周期。

## 目录结构

代码按领域分包，结构参考 `trpc-agent-go`：

- `agent/`：Runtime、生命周期、Option 和 Provider 集成。
- `agentcard/`：A2A AgentCard 构建、校验和 metadata 编码。
- `config/`：适配层配置和 Skill 引用。
- `naming/`：北极星发现、粘性选择和 A2A Client 解析。
- `skill/`：Skill 安装与可执行 Repository 装配。
- `mcp/`：可选 MCP metadata、Service Contract、Client Pool 和 Watcher。
- `server/a2a/`：A2A Server 构建。
- `internal/`：错误、指标和测试工具。
- 根包：面向领域包的轻量兼容 facade。

根包保留 `trpcagentpolaris.New(...)` 等兼容入口，便于已有调用方平滑迁移；新代码建议直接引用领域包。

## 环境要求

- Go 1.21 或更新版本
- tRPC-Agent-Go v1.11.0 或更新版本
- tRPC-Go v1.1.0 或更新版本
- A2A v1 与 `trpc-a2a-go/v2`
- 支持动态 `MetadataProvider` 的 `go-naming-polarismesh`
- 使用可选 `mcp/` 包时需要 `trpc-mcp-go` v0.0.17

## Provider

必须在 tRPC Server `Serve` 前完成 `Prepare`：

```go
import (
	polarisagent "trpc.group/trpc-go/trpc-agent-naming-polarismesh/agent"
	polarisconfig "trpc.group/trpc-go/trpc-agent-naming-polarismesh/config"
)

config := polarisconfig.DefaultConfig()
config.Namespace = "Production"
config.ServiceName = "customer-agent"
config.AgentName = "customer-agent"
config.AgentVersion = "1.2.3"
config.AdvertiseHost = "127.0.0.1"
config.AdvertisePort = 18080
config.RegistrationPort = 1
config.A2APath = "/a2a"
config.A2AVersion = polarisconfig.A2AVersionV10

runtime, err := polarisagent.New(
	polarisagent.WithConfig(config),
	polarisagent.WithSDKContext(registry.SDKContext()),
)
if err != nil {
	return err
}
if err := runtime.Prepare(ctx); err != nil {
	return err
}
defer runtime.Close(ctx)

// 只有 Prepare 成功后才启动 tRPC Server Serve。
```

`Prepare` 会写入以下实例 metadata：

```text
ai-agent-name  = <服务名>
ai-agent-card  = <完整 A2A AgentCard JSON>
ai-a2a-version = 0.3 | 1.0
```

适配层负责维护这三个 AgentCard 元数据键。当 `polaris-go` 把结构化实例字段提升到
`Instance.GetMetadata()` 时，校验和解析也会接受 `version`、`region`、`zone`
和 `campus`。这些字段属于平台元数据，不是额外的 AgentCard 契约键；其他未知键
仍会被拒绝。

实例 host 和 version 来自 AgentCard；实例 port 使用 `RegistrationPort`
（默认 `1`），protocol 固定为空。即使卡片同时声明 JSON-RPC、HTTP+JSON 和
gRPC interface，也只生成一个实例。所有 interface 必须使用同一 IP；业务
interface 端口不会被 registration port 覆盖。

`Prepare` 安装完整 host/port/version/protocol/metadata provider，go-naming 每次
注册只读取一次。应用只需要提供 TTL 等通用生命周期配置。`RegistrationSnapshot()`
用于检查或显式集成，其中 `SetProtocol=true` 明确清空实例 protocol。
go-naming 不再解析 AgentCard 或执行 AI 业务规则。
`UpdateAgentCard` 与 `UpdateAgentCardJSON` 只有新卡片完整校验通过才整体替换；
失败时保留上一份完整快照。

静态实例 metadata 必须直接使用三个标准 AgentCard 键：

```go
runtime, err := polarisagent.New(
    polarisagent.WithService("Development", "customer-agent"),
    polarisagent.WithAgentInstanceMetadata("1.0", metadata),
)
```

从 naming YAML 删除 Card metadata 和 `registration_port`，改为扩展层的
`WithRegistrationPort`（默认 `1`，范围 `1..65535`）。新 metadata 必须有
`ai-a2a-version`，并且只使用三个标准 AgentCard 元数据键；Card JSON 字节保持
不变。多 Card 来源、静态冲突和框架配置冲突在注册前失败，naming 仍是实例注册、
心跳和反注册的唯一 owner。

如果当前 tRPC 进程已经初始化 `go-naming-polarismesh`，Runtime 可以显式复用注册侧
SDKContext：

```go
sdkContext, ok := polarisagent.SDKContextFromFramework("customer-agent")
if !ok {
	return errors.New("polaris registry context is not available")
}
```

适配层不直接注册实例，也不会在 `Close` 时反注册由 tRPC 管理的实例。通过
`WithSDKContext` 传入的 SDKContext 属于调用方，适配层不会销毁它。

## A2A Server

```go
import polarisa2a "trpc.group/trpc-go/trpc-agent-naming-polarismesh/server/a2a"

a2aServer, err := polarisa2a.NewA2AServer(runtime, agentRunner)
if err != nil {
	return err
}
// Start 阻塞到退出；应用退出处理在 Start 开始后调用
// a2aServer.Stop(shutdownCtx)。
if err := a2aServer.Start("127.0.0.1:18080"); err != nil {
	return err
}
```

构建入口本身不启动监听器。使用 `Start` 时只在启动后调用 `Stop`；直接挂载 Handler
时，由应用拥有 HTTP Server 和 TaskManager 的关闭流程，完整范例见 examples。
Runtime 不会关闭借用的 SDKContext。

## Consumer

```go
runtime, err := polarisagent.New(
	polarisagent.WithSDKContext(sdkContext),
	polarisagent.WithService("Production", "customer-agent"),
)
if err != nil {
	return err
}
if err := runtime.Prepare(ctx); err != nil {
	return err
}
defer runtime.Close(ctx)

remoteAgent, err := runtime.NewRemoteAgentWithBinding(ctx, "tenant-a", "JSONRPC")
```

Resolver 会过滤不健康和被隔离的实例，按 `ai-a2a-version` 分别解析 0.3/1.0
AgentCard，校验 interface host 与实例 IP 一致，并按调用方 key 保持粘性。
调用方传入的 `protocolBinding` 用于选择卡片 interface，不再用实例 port 拼接
业务地址。只有北极星发现失败时才回退到最近一次成功解析的 AgentCard，回退结果
同样会绑定到本次请求的 interface。Resolver 的回退、粘性 caller 和候选实例状态
按协议 binding 隔离。坏实例和卡片回退都会记录 tRPC metrics 与日志。
已经向调用方输出流式事件后，适配层不会静默切换实例重试。
实例发现及服务 Skill 读取显式关闭借用 SDK 的重试，并传入单次剩余预算。
同步 SDK 操作仍不能强制中断，调用前后都会检查取消状态。

自定义 A2A options 通过 `naming.RemoteAgentOptionsFactory` 提供。构建期间发现快照变化时，
适配层丢弃旧构建并最多尝试三次，工厂必须可重复调用；空集或永久错误不会安装旧客户端。每次工厂调用
必须独立持有会被修改的客户端配置；可以复用线程安全的 Transport，但在应用
`WithTimeout` 等选项前要创建新的 `http.Client`。不同 remote Agent 可能并发调用
工厂；工厂的 context 只用于构造预算，不代表后续请求的生命周期。
同一张卡片复用客户端，卡片更新不会等待旧事件流，旧流仍使用原实例。
调用方需要持续消费事件 channel，或取消该次 Run 的 context。

```go
remoteAgent, err := runtime.NewRemoteAgent(ctx, "tenant-a",
	func(ctx context.Context) ([]a2aagent.Option, error) {
		return []a2aagent.Option{
			a2aagent.WithA2AClientExtraOptions(
				a2aclient.WithHTTPClient(&http.Client{Transport: http.DefaultTransport}),
				a2aclient.WithTimeout(30 * time.Second),
			),
		}, nil
	},
)
```

该示例使用 `net/http`、`time`、
`trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1` 和
`trpc.group/trpc-go/trpc-a2a-go/v2/client`。尚未发布的适配层 API 将直接传入
可变 options 的方式调整为工厂，默认不传 options 的调用方式保持一致。

## Skill

Skill 支持三种加载模式：

| 模式 | 行为 |
| --- | --- |
| `names` | 只安装显式 `SkillRefs`，跳过 published List。 |
| `published` | 按 list/max 限制分页查询 published Skill，并安装每个 active version。 |
| `mounted` | 读取 AI Agent 服务 `extended_metadata` 中的 `namespace:name` 引用。 |

`DefaultConfig` 中 `MountSkillsReplace` 默认为 true：替换挂载与安装都只使用
显式引用，空列表可以清空旧引用。merge 挂载只提交本次显式配置的 ID、name 和
version；显式配置已有 ID 会按 Polaris Writer 的完整对象更新语义替换其摘要，
未提及的 ID 保留最新 Console 数据。merge 与 `UseServiceSkills` 同时开启时，
安装集合来自挂载操作返回的服务。每个包必须只暴露选中的 `SKILL.md`，且入口
名称与对应引用一致。挂载会修改服务数据，后续安装失败不会自动回滚该写入。

```go
config.SkillCacheDir = "./var/polaris-skills"
config.SkillMode = polarisconfig.SkillModeMounted
config.SkillRefs = []polarisconfig.SkillRef{
	{Namespace: "Production", Name: "weather", Version: "1.0.0"},
}
config.UseServiceSkills = true
config.MountSkills = true
```

显式 `SkillRefs` 优先于服务级引用，最终按 `namespace:name@version` 去重并稳定
排序。安装结果必须位于缓存目录内且包含 `SKILL.md`，随后才会构建
`skill.Repository`。Repository 只包含本次安装选中的 Skill，不会加载缓存目录中的
历史版本或未选择资产。
published Skill 查询和安装会遵守剩余加载或刷新 deadline；由 Runtime 创建的
SDK Context 也会将 Skill message/download 超时对齐到 `PolarisRequestTimeout`。

`SkillListRefresh` 可周期刷新 published/mounted Repository，刷新成功后原子替换
完整快照，失败则保留上一份。`SkillFailureThreshold`、`SkillListLimit`、
`SkillMaxSkills` 和 `SkillCacheMaxAge` 分别限制单项失败、分页、容量和陈旧缓存修复。

## MCP

可选 `mcp/` 包负责 `trpc-mcp-go` 适配，不把 MCP 框架类型下沉到根包或
`polaris-go`。

- `PrepareServer` 构建并校验 `mcp-server-name`、`mcp-protocol-version`
  和 transport endpoint path。
- `CollectContract` 仅采集初始化响应中声明的 Tools、Resources、Prompts 能力，
  上报 contract type `mcp`、
  feature type `101/102/103`、request method 和 notification metadata。Contract
  上报使用 `ProviderAPI.ReportServiceContract`，查询使用
  `ConsumerAPI.GetServiceContract`。上报默认 fail-fast，也可显式配置 `warn`
  进入 best-effort 模式。
- `NewClient` 支持 SSE 与 Streamable HTTP；`NewStdioClient` 只做本地连接，
  不注册网络实例。
- `Pool`/`Watcher` 为每个健康实例维护已初始化 Client，识别 endpoint、path 和
  protocol 替换，并把调用结果按 `InstanceTypeMCP` 上报。

适配层 Client 保留初始化能力。外部 Connector 在采集契约前必须实现
`ServerCapabilitiesProvider`，或通过 `Config.ServerCapabilities` 提供真实的
初始化能力；已声明能力的调用错误仍返回失败。

每个 Connector 的操作串行执行，排队支持取消且计入 `Timeout`。Streamable HTTP
禁用 SDK 独立的 GET SSE 连接，避免其未同步的 session/event 字段发生竞争；
POST SSE 响应及旧版 SSE 传输仍受支持。自定义 Streamable HTTP Pool factory 必须
返回适配层 `NewClient` 或其他并发安全 Connector，不能返回裸的
`trpc-mcp-go.Client`。
自定义 Connector 必须遵循请求 context，并同步其后台任务。同步 notification
回调中需要向同一 Connector 发起请求时，应交给队列异步执行，不能在当前请求
内部等待新请求完成。

当前 Polaris 的 `service_contract_detail.method` 仍是 `VARCHAR(32)`。超过长度限制的
MCP notification 名称不会截断或作为非法 InterfaceDescriptor 上报，而是保留在
Service Contract JSON 的 `notifications` 字段中；核心 request method 仍写入服务端
interface 表。

## 北极星 API

Runtime 使用同一个借用的 SDKContext 创建本仓库的本地 `AgentAPI` 和 `polaris-go` 的
`SkillAPI`。默认 Agent 服务读取通过 ConsumerAPI 获取服务与挂载 Skill metadata，
不要求 Console。Service Contract 上报走 ProviderAPI，查询走 ConsumerAPI；
Core 不提供 `ServiceAdminAPI`、`AgentAPI` 或 `MCPAPI`。

`WithAgentAPI` 和 `WithSkillAPI` 可用于自定义实现和隔离测试，生产调用通常只需
提供 `WithSDKContext`。

可选服务写入须显式配置扩展层 API：

```go
agentAPI := agentapi.NewByContext(sdkCtx,
    agentapi.WithConsole([]string{"http://127.0.0.1:8090"}, consoleToken, 5*time.Second),
)
defer agentAPI.Destroy()
```

启用 `EnsureService` 时通过 `agent.WithAgentAPI` 注入。没有 Console 时写入返回
`ErrCodeServiceWriteUnsupported`，经 go-naming 注册实例仍然可用。Console 请求模型、
配置和 HTTP 实现归本扩展层；Destroy 只关闭扩展层资源，不销毁借用的 SDKContext。
清空最后一个 Skill 仍依赖服务端支持显式空 extended metadata。

## 生命周期

```text
New -> Prepare -> Serve -> 停止服务 -> Runtime.Close
```

- `Prepare` 和 `Close` 均幂等。
- `Prepare` 前或 `Close` 后调用业务方法会返回明确错误。
- `WithSDKContext` 传入的 SDKContext 由调用方持有，适配层不会销毁。
- 适配层不注册、不反注册实例。

## 开发

Core 固定为 `polaris-go` main 快照
`v1.7.3-0.20261118135400-4fe9e1e0aca7`，通用 naming 适配层固定为
`trpc-naming-polarismesh v1.0.1-0.20261119025216-6b54006e7aff`。两者均使用
可获取的模块版本，不再使用 filesystem replace。

```bash
go test -race -count=1 ./...
go vet ./...
golangci-lint run ./...
```

## 可运行示例

示例沿用 `trpc-agent-go` 的运行方式：真实 LLM Agent、真实 Agent Runner 和
真实北极星 AI Registry。

| 示例 | 展示内容 | 是否需要 LLM key |
| --- | --- | --- |
| [examples/a2a](examples/a2a) | AgentCard 注册、北极星发现、A2A 单次与流式调用 | 需要 |
| [examples/mcp](examples/mcp) | MCP metadata、Service Contract、发现和工具调用 | 需要 |
| [examples/skill](examples/skill) | Skill 发布、安装、Repository 组装和 `skill_load` | 需要 |
| [examples/a2a-agentcard](examples/a2a-agentcard) | 已注册 Agent A 调用两个版本的 Agent B | 不需要 |

`a2a`、`mcp`、`skill` 需要一个 OpenAI 兼容的模型服务；所有示例运行时都必须
提供北极星地址。
每个示例都提供支持 `--dry-run`、`--force` 和 `--delete-service` 的
`cleanup.sh`。
`a2a-agentcard` Provider 默认验证北极星 AI 注册中心的 AgentCard 1.0/0.3
多协议增强；显式设置 `-role agent-a|agent-b` 才进入多版本调用流程。

## 完整集成测试

`integration/` 提供 A2A、MCP、Skill 以及已注册 `a2a-agentcard` 链路的
真实服务套件。测试使用确定性或脚本化模型，不需要 LLM key。北极星地址必须
通过 `POLARIS_SERVER` 提供；Discovery、Console、Skill 端口默认分别为
8091、8090、8094。测试使用隔离的
`go-agent-naming-e2e` namespace 和逐轮唯一资源名。

```bash
export POLARIS_SERVER=your-polaris-host
bash integration/verify_all.sh
```

每个测试创建带 run ID 的服务和 Skill，并在 `t.Cleanup` 中只反注册、删除
自己创建的资源；共享 namespace 保留。套件说明和环境变量见
[集成测试说明](integration/README.zh_CN.md)。

测试进程被中断时，可用 [`integration/cleanup.sh`](integration/cleanup.sh)
按 run ID 清理资源。

## 许可证

Copyright (C) 2026 Tencent. All rights reserved.

采用 [Apache License 2.0](LICENSE)。
