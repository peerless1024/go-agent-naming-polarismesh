[English](test.md) | 中文

# MCP metadata、Service Contract、发现与 Pool

## 测试函数

[e2e_test.go](e2e_test.go) 中的
`TestMCPMetadataContractDiscoveryAndPool`。

## 拉起的进程

| 进程 | 数量 | 作用 |
| --- | --- | --- |
| `go test` 测试进程 | 1 | 启动 MCP Streamable HTTP Server、注册 metadata、采集并上报 Service Contract、发现实例并调用工具 |
| MCP Server | 进程内 | 使用真实 `trpc-mcp-go` Streamable HTTP handler 和 loopback listener |
| Polaris | 外部 | 通过 `POLARIS_SERVER` 提供，测试不启动 Polaris Server |

该用例没有独立 MCP Server 或 Client 进程。测试在同一进程内保留 Server 和
Pool，便于稳定验证 wire、注册写入、发现和清理。

## 注册的服务与资源

| 资源 | 值 |
| --- | --- |
| Namespace | 默认 `go-agent-naming-e2e` |
| Service | `gan-e2e-<run-id>-mcp` |
| 类型 | `MCP_SERVER` |
| 实例 | 1 个健康实例 |
| 实例 host | `127.0.0.1` |
| 业务端口 | 随机 loopback MCP 端口 |
| 实例 protocol | `mcp-streamable-http` |
| 实例 version | `1.0.0` |
| Metadata | `mcp-server-name`、`mcp-protocol-version`、`mcp-endpoint-path` |
| Endpoint path | `/mcp` |
| Service Contract | type `mcp`，包含 interfaces 和 content digest |

## 调用关系

```text
测试 MCP Server ──► 本地 MCP listener
        │
        ├─► MCP initialize / ListTools ─► CollectContract
        │                                      │
        │                                      ▼
        │                              ProviderAPI.ReportServiceContract
        │
        └─► go-naming-polarismesh ─► Polaris MCP 实例

测试 ConsumerAPI / MCP Pool
        │
        ├─► Polaris Discover ─► MCP 实例 ─► tools/call(echo)
        │
        └─► ConsumerAPI.GetServiceContract ─► 校验 contract type/digest/interfaces
```

Pool 独立通过服务发现选择 MCP 实例并调用原生 `echo` 工具；Contract 查询通过
`ConsumerAPI` 完成，不经过本地 MCP listener。

测试会先启用 `provider.reportServiceContractEnable`，确保走真实的
`ProviderAPI.ReportServiceContract` 路径。

## 用例

### [用例 1.1 metadata 与注册]

操作：创建 MCP_SERVER 服务、准备服务端 metadata、安装 metadata provider，
并注册 MCP 实例。

预期：能看到一个健康实例，包含 MCP 服务名、protocol version、endpoint
path 和 `mcp-streamable-http` protocol。

### [用例 1.2 Contract 采集与上报]

操作：连接真实 MCP endpoint，完成初始化、列出已声明能力、采集 Contract，
并通过 `ProviderAPI` 上报。

预期：上报的 Contract type 为 `mcp`，content digest 非空。

### [用例 1.3 Pool 发现与工具调用]

操作：使用真实 `ConsumerAPI` 创建适配层 MCP Pool，刷新到实例可用，并用
`integration` 调用 `echo`。

预期：工具结果包含 `mcp|<service>|integration`。

### [用例 1.4 Contract 查询]

操作：通过 `ConsumerAPI.GetServiceContract` 查询 Contract。

预期：返回的 Contract type 为 `mcp`，digest 非空，且至少包含一个 interface。

## 验证方式

测试断言：

- 实例 metadata `mcp-server-name`、`mcp-protocol-version` 和
  `mcp-endpoint-path`。
- MCP 实例 protocol 和健康状态。
- Contract type、digest 和 interfaces。
- Pool 发现和工具调用成功。
- 服务类型与所有权清理正确。

## 清理

在 `t.Cleanup` 中反注册 MCP Registry、关闭 HTTP Server、移除 metadata
provider，并删除 MCP 服务及其 Contract；共享 namespace 保留。

## 运行

```bash
POLARIS_SERVER=your-polaris-host \
  go test -tags=integration -count=1 -v -timeout=10m ./integration/mcp
```
