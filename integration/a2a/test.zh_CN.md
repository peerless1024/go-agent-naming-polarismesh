[English](test.md) | 中文

# A2A 注册、发现、流式调用与取消

## 测试函数

[e2e_test.go](e2e_test.go) 中的
`TestA2ARegistrationDiscoveryStreamingAndCancellation`。

## 拉起的进程

| 进程 | 数量 | 作用 |
| --- | --- | --- |
| 预构建测试二进制 | 1 | 创建 Polaris Context、启动进程内 A2A HTTP listener、注册 Provider、运行 Consumer 并执行断言 |
| A2A Server | 进程内 | 使用真实 HTTP handler 和 loopback listener，不启动独立 Provider 子进程 |
| Polaris | 外部 | 通过 `POLARIS_SERVER` 提供，测试不启动 Polaris Server |

该用例没有独立 Provider/Client 进程。A2A 业务服务和调用方都在测试进程内，
以便直接验证真实 A2A wire 行为。

## 注册的服务

| 资源 | 值 |
| --- | --- |
| Namespace | 默认 `go-agent-naming-e2e` |
| Service | `gan-e2e-<run-id>-a2a` |
| 类型 | `AI_AGENT` |
| 实例 | 1 个健康实例 |
| 实例 host | `127.0.0.1` |
| Registration port | `1` |
| 实例 version | `v1.0.0` |
| 实例 protocol | 空 |
| Metadata | `ai-agent-name`、`ai-agent-card`、`ai-a2a-version=1.0` |
| A2A 业务端口 | 随机 loopback 端口 |
| A2A 路径 | `/a2a` |

服务在注册前通过 Console Service Writer 创建，用例结束后删除。

## 调用关系

```text
测试 Consumer Runtime
        │
        ├─► Polaris Discover ─► AI_AGENT 实例
        │
        └─► AgentCard JSONRPC endpoint ─► 进程内 A2A Server
                                             │
                                             └─► EchoAgent
                                                   │
        ◄──────────── A2A events ──────────────────┘
```

Consumer 从发现到的 AgentCard 选择 JSONRPC 接口，不使用
`instance.host:instance.port` 拼装业务地址。

## 用例

### [用例 1.1 注册与发现]

操作：创建 AI_AGENT 服务、准备 Runtime、通过 `go-naming-polarismesh`
注册实例，并等待健康实例。

预期：恰好一个健康实例，包含三个 AgentCard metadata、version
`v1.0.0`、registration port `1` 和空 protocol。

### [用例 1.2 单次调用]

操作：通过 Polaris 以 `JSONRPC` 绑定解析远端 Agent，并发送 `hello`。

预期：响应为 `echo|v1.0.0|hello`。

### [用例 1.3 流式调用]

操作：通过流式 A2A 路径发送 `stream`。

预期：事件流至少包含四个真实事件，最终文本包含流式结果。

### [用例 1.4 取消]

操作：启动持续输出的 `hold` 流，读取一个事件后取消 Run context。

预期：Provider 观察到取消（`Canceled > 0`），并在限定时间内回到
零活跃调用。

## 验证方式

测试断言：

- `ai-agent-name`、`ai-agent-card` 和 `ai-a2a-version=1.0`。
- 实例 version `v1.0.0`、registration port `1`、空 protocol。
- 通过 Polaris 成功发现 AgentCard。
- 单次调用返回精确结果。
- 流式输出和事件数量。
- Provider 取消计数和活跃调用清理。

## 清理

Provider 在 `t.Cleanup` 中反注册，AI_AGENT 服务随后删除；共享 namespace
保留。

## 运行

```bash
make -C integration build
POLARIS_SERVER=your-polaris-host \
  "integration/bin/$(go env GOOS)-$(go env GOARCH)/a2a.test" \
  -test.count=1 -test.v -test.timeout=10m
```
