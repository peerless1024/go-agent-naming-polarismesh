[English](README.md) | 中文

# 快速开始

该示例将确定性的 tRPC-Agent-Go Agent 接入 Polaris：

```text
Provider -> Runtime.Prepare -> Polaris Registry -> A2A v1
Client   -> Runtime.Resolver -> Polaris Discover -> unary + streaming
```

不需要 LLM key。

## 运行

先设置 Polaris 地址：

```bash
export POLARIS_SERVER=your-polaris-host
```

终端 1：

```bash
go run ./examples/quickstart/provider
```

终端 2：

```bash
go run ./examples/quickstart/client
```

Client 会分别输出 unary 和 streaming 两种结果：

```json
[
  {
    "mode": "unary",
    "success": true,
    "events": 2,
    "response": "quickstart|v1.0.0|unary:hello"
  },
  {
    "mode": "stream",
    "success": true,
    "events": 5,
    "response": "quickstart|v1.0.0|stream"
  }
]
```

默认配置：

| 项目 | 值 |
| --- | --- |
| Namespace | `go-naming-polarismesh-quickstart` |
| Service | `trpc.polaris.demo.quickstart` |
| A2A path | `/a2a` |
| Provider 端口 | `18191` |
| Polaris gRPC | `8091` |

可通过 `-polaris-port`、`-namespace`、`-service` 或 `-port` 覆盖。
在 Provider 终端按 `Ctrl+C` 可验证优雅反注册。

## 验证

```bash
./examples/quickstart/verify.sh --polaris-server "$POLARIS_SERVER"
./examples/quickstart/cleanup.sh --dry-run
./examples/quickstart/cleanup.sh --force
./examples/quickstart/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

`verify.sh` 不负责清理进程，运行成功后会保留 Provider。`cleanup.sh` 默认只停止
已记录的进程并删除本地产物；增加 `--delete-service` 才会删除远端服务。
用例见 [test.zh_CN.md](test.zh_CN.md)。
