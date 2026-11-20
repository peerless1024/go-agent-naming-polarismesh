[English](README.md) | 中文

# 集成测试

本目录验证示例使用的 AI Registry 链路，并通过 `integration` build tag
与普通 `go test ./...` 隔离。默认测试不依赖北极星。

| 套件 | 真实依赖 | 断言 |
| --- | --- | --- |
| [a2a](./a2a) | 北极星、loopback A2A | AgentCard metadata、发现、单次调用、流式输出、取消 |
| [mcp](./mcp) | 北极星、loopback MCP | MCP metadata、Service Contract、发现、Pool 工具调用 |
| [skill](./skill) | 北极星 Skill 数据面、loopback A2A | 发布、安装、Repository、`skill_load` 注入模型请求 |
| [a2a-agentcard](./a2a-agentcard) | 北极星、loopback A2A | 已注册 Agent A 调用两个版本的 Agent B；Provider 默认模式验证北极星 AI 注册中心的 AgentCard 1.0/0.3 多协议增强 |

## 用例文档

每个套件都说明拉起的进程、注册的服务、调用关系和验证方式：

- [a2a/test.zh_CN.md](./a2a/test.zh_CN.md)
- [mcp/test.zh_CN.md](./mcp/test.zh_CN.md)
- [skill/test.zh_CN.md](./skill/test.zh_CN.md)
- [a2a-agentcard/test.zh_CN.md](./a2a-agentcard/test.zh_CN.md)

`a2a`、`a2a-agentcard` 和 `skill` 使用确定性或脚本化模型，不需要外部
LLM；`mcp` 直接通过适配层 Pool 调用 MCP 工具。因此整套测试不需要 LLM API key。

## 前置条件

- Go 1.21 或更新版本。
- 可访问的北极星环境。
- Discovery gRPC、Console HTTP 和 Skill gRPC 均可用。
- 允许在非 `default` namespace 创建服务、实例和 Skill。

环境变量：

| 变量 | 用途 | 默认值 |
| --- | --- | --- |
| `POLARIS_SERVER` | 北极星地址 | 必填 |
| `POLARIS_GRPC_PORT` | Discovery gRPC 端口 | `8091` |
| `POLARIS_HTTP_PORT` | Console HTTP 端口 | `8090` |
| `POLARIS_SKILL_GRPC_PORT` | Skill gRPC 端口 | `8094` |
| `POLARIS_TOKEN` | Discovery token | 空 |
| `POLARIS_CONSOLE_TOKEN` | Console token | 回退到 `POLARIS_TOKEN` |
| `POLARIS_SKILL_TOKEN` | Skill token | 回退到 `POLARIS_TOKEN` |
| `POLARIS_SERVICE_TOKEN` | 注册 token | 回退到 `POLARIS_TOKEN` |
| `POLARIS_E2E_NAMESPACE` | 测试 namespace | `go-agent-naming-e2e` |
| `POLARIS_E2E_RUN_ID` | 运行标识 | 时间戳 + 随机后缀 |
| `POLARIS_E2E_TIMEOUT` | 单次操作超时 | `30s` |

## 运行

```bash
export POLARIS_SERVER=your-polaris-host
bash integration/verify_all.sh
```

只跑一个套件：

```bash
POLARIS_SERVER=your-polaris-host \
  go test -tags=integration -count=1 -v -timeout=10m ./integration/a2a
```

没有服务端时只编译：

```bash
go test -tags=integration -run '^$' ./integration/...
```

`verify_all.sh` 支持：

| 参数 | 作用 |
| --- | --- |
| `--server=host` | 设置 `POLARIS_SERVER` |
| `--namespace=name` | 设置 `POLARIS_E2E_NAMESPACE` |
| `--run-id=id` | 设置 `POLARIS_E2E_RUN_ID` |
| `--timeout=30s` | 设置单次操作超时 |
| `-v` | 输出详细 `go test` 日志 |

## 保留进程和资源

需要在断言通过后继续检查服务、Skill 或 Provider 进程时，使用 retained
运行器：

```bash
POLARIS_SERVER=your-polaris-host \
  bash integration/run_retained.sh \
  --run-id inspect-01 \
  --suite=a2a,a2a-agentcard,skill
```

每个选中的套件在独立分离进程中运行，断言成功后进入 keep 模式并保持阻塞。
run ID 和进程 manifest 保存在 `integration/.retained/<run-id>/`。该命令不会
自动清理。

统一停止 retained 进程并删除本轮远端资源：

```bash
bash integration/cleanup.sh \
  --run-id inspect-01 \
  --delete-service \
  --force \
  --polaris-server "$POLARIS_SERVER"
```

## 清理被中断的运行

正常测试会在 `t.Cleanup` 中清理自己的服务和 Skill。如果测试进程被强制
终止，可以用启动日志里的 run ID 清理剩余的 `gan-e2e-<run-id>-*` 资源：

```bash
./integration/cleanup.sh \
  --run-id <run-id> \
  --delete-service \
  --dry-run \
  --polaris-server "$POLARIS_SERVER"

./integration/cleanup.sh \
  --run-id <run-id> \
  --delete-service \
  --force \
  --polaris-server "$POLARIS_SERVER"
```

按 run ID 清理会删除 A2A 服务、MCP 服务及其 Service Contract、Skill Agent
服务、A2A AgentCard 服务、AgentCard 多协议场景服务和已发布 Skill。自定义运行
可以额外传 `--service` 和 `--skill`。

## 资源归属

每次运行创建的服务和 Skill 名称都包含 run ID。共享 namespace 不存在时
自动创建，测试结束后保留。

每个测试都会反注册自己的 Provider，并删除自己的 AI_AGENT 或 MCP_SERVER
服务；Skill 测试会删除已发布 Skill。清理挂在 `t.Cleanup` 上，因此断言失败
后也会执行，不会影响后续无关运行。

本目录不能替代部署发布门禁、远端 CI 或跨版本兼容测试。真实服务前置条件
不满足时测试会失败，不会用 mock、skip 或降级断言隐藏。
