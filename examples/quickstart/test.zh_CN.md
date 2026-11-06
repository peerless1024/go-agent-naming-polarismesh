[English](test.md) | 中文

# 快速开始测试

验证脚本运行 [README.zh_CN.md](README.zh_CN.md) 中列出的同一组 Provider 和
Client 程序。

### [case 1.1 unary discovery and call]

操作：通过 Polaris 注册确定性 Agent，由 Runtime Resolver 发现，并发送一次非流式
请求。

预期：调用成功，响应包含非空的 `quickstart|...|unary:hello`。

### [case 1.2 streaming discovery and call]

操作：启用 streaming 发送一次请求。

预期：收到多个 Agent 事件，最终响应包含 `quickstart|...|stream`。

## 清理

verify 不停止进程。`cleanup.sh --force` 停止已记录的 Provider 并删除本地产物；
追加 `--delete-service` 才会删除远端 service。
