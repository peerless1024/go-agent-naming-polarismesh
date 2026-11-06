[English](test.md) | 中文

# 流式取消示例测试

### [case 3.1 Polaris discovery and streaming]

操作：通过 Polaris 发现 Provider，并发起流式请求。

预期：至少收到一个非空 partial 事件。

### [case 3.2 cancel discovered remote Agent stream]

操作：收到第一个事件后 cancel run context，并继续 drain。

预期：客户端事件 channel 在 3 秒内关闭。

### [case 3.3 Provider releases canceled run]

操作：通过发现到的 Provider endpoint 查询 `/e2e/stats`。

预期：`active=0` 且 `canceled>0`。

## 清理

verify 不停止进程。默认 `cleanup.sh --force` 停止 Provider 并删除本地产物；
追加 `--delete-service` 才会删除远端 service。
