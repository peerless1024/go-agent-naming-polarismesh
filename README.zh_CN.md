# tRPC-Agent-Go 接入北极星 AI Registry

[English](README.md) | [简体中文](README.zh_CN.md)

`trpc-agent-naming-polarismesh` 负责把 tRPC-Agent-Go 接入北极星 AI Registry。
它生成并冻结 A2A AgentCard，通过 `go-naming-polarismesh` 注入实例 metadata，
装配 Skill Repository，解析健康 A2A 实例并创建 A2A Client。

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
- `server/a2a/`：A2A Server 构建。
- `internal/`：错误、指标和测试工具。
- 根包：面向领域包的轻量兼容 facade。

根包保留 `trpcagentpolaris.New(...)` 等兼容入口，便于已有调用方平滑迁移；新代码建议直接引用领域包。

## 环境要求

- Go 1.21 或更新版本
- tRPC-Agent-Go v1.11.0 或更新版本
- A2A v1 与 `trpc-a2a-go/v2`
- 支持动态 `MetadataProvider` 的 `go-naming-polarismesh`

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
config.A2APath = "/a2a"

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
a2a.agent.card = <A2A v1 AgentCard JSON>
a2a.transport  = JSONRPC
a2a.path       = <配置路径>
```

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

remoteAgent, err := runtime.NewRemoteAgent(ctx, "tenant-a")
```

Resolver 会过滤不健康和被隔离的实例，跳过非法 AgentCard 与 endpoint
不一致的实例，并按调用方 key 保持粘性。只有北极星发现失败时才回退到最近
一次成功解析的 AgentCard。坏实例和卡片回退都会记录 tRPC metrics 与日志。
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

`DefaultConfig` 中 `MountSkillsReplace` 默认为 true：替换挂载与安装都只使用
显式引用，空列表可以清空旧引用。merge 挂载只提交本次显式配置的 ID、name 和
version；显式配置已有 ID 会按 Polaris Writer 的完整对象更新语义替换其摘要，
未提及的 ID 保留最新 Console 数据。merge 与 `UseServiceSkills` 同时开启时，
安装集合来自挂载操作返回的服务。每个包必须只暴露选中的 `SKILL.md`，且入口
名称与对应引用一致。挂载会修改服务数据，后续安装失败不会自动回滚该写入。

```go
config.SkillCacheDir = "./var/polaris-skills"
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

## 北极星 API

Runtime 使用同一个借用的 SDKContext 创建 `polaris-go` 的 `AgentAPI` 和
`SkillAPI`，不会创建第二套北极星客户端。

`WithAgentAPI` 和 `WithSkillAPI` 可用于自定义实现和隔离测试，生产调用通常只需
提供 `WithSDKContext`。

## 生命周期

```text
New -> Prepare -> Serve -> 停止服务 -> Runtime.Close
```

- `Prepare` 和 `Close` 均幂等。
- `Prepare` 前或 `Close` 后调用业务方法会返回明确错误。
- `WithSDKContext` 传入的 SDKContext 由调用方持有，适配层不会销毁。
- 适配层不注册、不反注册实例。

## 开发

当前仓库为尚未发布的 `polaris-go` Agent/Skill API 与
`go-naming-polarismesh` metadata hook 使用本地 replace。发布前需要替换成正式版本。

```bash
go test -race ./...
go vet ./...
```

## 可运行示例

建议先看 [examples/quickstart](examples/quickstart)。其中包含可直接运行的 Go
Provider/Client，用确定性 Agent 验证注册、Polaris 发现和 A2A unary/streaming。
其他重点示例：

- [examples/skill](examples/skill)：下载并执行真实 published Skill；
- [examples/cancel](examples/cancel)：取消已发现的远端流并验证 Provider 释放。

三个示例都不需要 LLM key，但运行时必须提供 Polaris 地址。

## 完整集成测试

`examples/ai-registry/` 提供可独立运行的 Provider、Consumer，以及 24 项真实 E2E。
服务端地址必须通过 `--server` 或 `POLARIS_SERVER` 提供；Discovery、Console、Skill
端口默认分别为 8091、8090、8094。测试使用隔离的 `go-agent-naming-e2e` 命名空间
和逐轮资源名。显式空 `extended_metadata` 需要 Polaris `v2.2.0.0` 或更新版本；
不需要 LLM 密钥。

```bash
cd examples/ai-registry
export POLARIS_SERVER=your-polaris-host
bash verify_all.sh --server="$POLARIS_SERVER" --resources=cleanup
bash verify_all.sh --server="$POLARIS_SERVER" --resources=keep
bash cleanup.sh --state=.logs/<run-id>/state.json --dry-run
bash cleanup.sh --state=.logs/<run-id>/state.json --apply
bash cleanup.sh --state=.logs/<run-id>/state.json --apply --delete-service
bash cleanup.sh --delete-service --dry-run
bash cleanup.sh --delete-service --apply
```

必须显式选择 `keep` 或 `cleanup`。凭据仅从环境变量或已有 SDK 配置文件读取，
不写入 state；按 state 执行 `--apply` 默认只停止本轮记录且身份匹配的进程，
服务与 Skill 保留。增加 `--delete-service` 才会删除该 state 的服务/Skill，
并扫描历史零实例资源；`--delete-services` 是别名。清理前会校验 owner、
精确进程身份和真实实例状态。
用例、运行参数和失败排查见 [真实 E2E 文档](examples/ai-registry/test.zh_CN.md)。

## 许可证

Copyright (C) 2026 Tencent. All rights reserved.

采用 [Apache License 2.0](LICENSE)。
