[English](README.md) | 中文

# 流式取消示例

该示例展示从 Polaris 发现远端 Agent 后，正确的 A2A 流式取消顺序：

```text
Runner.Run(ctx, ...)
    -> 收到 Agent 事件
    -> cancel ctx
    -> 继续 drain，直到事件 channel 关闭
```

不需要 LLM key。

## 运行

```bash
export POLARIS_SERVER=your-polaris-host
```

终端 1：

```bash
go run ./examples/cancel/provider
```

终端 2：

```bash
go run ./examples/cancel/client
```

Client 会通过 Polaris 发现 Provider，读取第一个流式事件，取消 run context，
drain 事件流，并查询 Provider 的 `/e2e/stats`。

成功输出类似：

```json
{
  "success": true,
  "first_event": "cancel-demo|tick-1|",
  "stream_closed_ms": 0,
  "active": 0,
  "canceled": 1
}
```

Provider 暴露：

```text
GET http://127.0.0.1:18393/e2e/stats
```

统计数据用于证明被取消的 run 已释放 active 计数。

## 验证

```bash
./examples/cancel/verify.sh --polaris-server "$POLARIS_SERVER"
./examples/cancel/cleanup.sh --dry-run
./examples/cancel/cleanup.sh --force
./examples/cancel/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

verify 会保留 Provider 供检查或单独清理。默认 cleanup 只停止已记录的进程并删除
本地产物；增加 `--delete-service` 才会删除远端 service。
