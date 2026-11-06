[English](README.md) | 中文

# Skill 示例

该示例从真实 Polaris Skill Registry 下载并安装 published Skill，然后通过
确定性 A2A Agent 执行并返回 Skill 内容。

不需要 LLM key。Agent 直接把下载到的 Skill 正文通过 A2A 返回，用来证明内容确实
来自 Polaris。

## 手动运行

设置 Polaris 地址：

```bash
export POLARIS_SERVER=your-polaris-host
```

Skill gRPC 默认使用 `8094` 端口。部署使用其他端口时，`verify.sh` 可设置
`POLARIS_SKILL_GRPC_PORT`，直接运行命令可传 `-skill-address`。

发布示例 Skill：

```bash
go run ./examples/skill/publish \
  -server "$POLARIS_SERVER" \
  -namespace go-naming-polarismesh-skill-demo \
  -skill trpc-agent-naming-demo-skill \
  -skill-version 1.0.0
```

启动 Provider：

```bash
go run ./examples/skill/provider \
  -server "$POLARIS_SERVER" \
  -namespace go-naming-polarismesh-skill-demo \
  -service trpc.polaris.demo.skill \
  -skill trpc-agent-naming-demo-skill \
  -skill-version 1.0.0
```

运行 Client：

```bash
go run ./examples/skill/client \
  -server "$POLARIS_SERVER" \
  -namespace go-naming-polarismesh-skill-demo \
  -service trpc.polaris.demo.skill \
  -skill trpc-agent-naming-demo-skill \
  -skill-version 1.0.0
```

Provider 会：

- 使用 `SkillRefs` 和 `SkillAPI.Install`；
- 从 Polaris 下载真实 ZIP；
- 安装到 `-skill-cache`；
- 打开安装后的 `skill.Repository`；
- 通过 A2A 返回下载到的 `SKILL.md` 正文。

## 验证

```bash
./examples/skill/verify.sh --polaris-server "$POLARIS_SERVER"
./examples/skill/cleanup.sh --dry-run
./examples/skill/cleanup.sh --force
./examples/skill/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

验证脚本会创建共享 E2E namespace、发布 Skill、启动 Provider，并通过 A2A 检查
安装内容，随后保留 Provider。默认 cleanup 只停止已记录的进程并删除本地产物；
增加 `--delete-service` 才会删除示例 Skill 和远端 service。

发布命令会等待 Skill 数据面可查询、可下载，而不只等待 Console 显示 published。

用例见 [test.zh_CN.md](test.zh_CN.md)。
