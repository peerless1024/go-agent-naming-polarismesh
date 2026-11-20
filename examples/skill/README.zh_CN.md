[English](README.md) | 中文

# 使用北极星发布的 Agent Skill

这个示例沿用 `trpc-agent-go` 官方 Skill 示例：

```text
publish -> 北极星 Skill Registry
agent   -> SkillAPI.Install -> 安装文件 -> Runtime.SkillRepository
        -> llmagent.WithSkills -> skill_load -> A2A Server -> 北极星
```

发布的 Skill 包含 `SKILL.md`、参考文档和脚本。Agent 通过标准 `skill_load`
工具按需加载 Skill。

## 前置条件

- Go 1.21 或更新版本。
- 支持 Skill Registry 的北极星环境。
- 一个 OpenAI 兼容的模型服务。

```bash
export POLARIS_SERVER=your-polaris-host
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://your-openai-compatible-endpoint/v1
```

## 运行

发布 Skill：

```bash
go run ./examples/skill/publish
```

启动 Agent：

```bash
go run ./examples/skill/agent
```

通过北极星调用：

```bash
go run ./examples/skill/client
```

Client 会请求 Polaris greeting，Agent 应加载 `polaris-greeting` 并返回
已发布 `SKILL.md` 中的标记。

## Runtime.Prepare 做了什么

Agent 使用 `SkillMode=names` 指定一个明确的 Skill：

```go
cfg.SkillMode = adapterconfig.SkillModeNames
cfg.SkillRefs = []adapterconfig.SkillRef{{
    Namespace: "go-agent-naming-examples",
    Name:      "polaris-greeting",
    Version:   "1.0.0",
}}
cfg.SkillCacheDir = "./.skill-cache"
```

`Prepare` 会调用真实 Skill 数据面，安装固定版本，并返回标准的
`skill.Repository`。示例直接把它交给 `llmagent.WithSkills(repo)`，发布后
不会再从示例源码里复制 Skill 正文。

适配层还支持 `published` 模式（分页加载 namespace 内所有已发布 Skill）和
`mounted` 模式（读取 AI_AGENT 服务上的 `extended_metadata`）。本示例使用
`names`，让安装集合保持隔离且可复现。

## 清理

```bash
./examples/skill/cleanup.sh --dry-run
./examples/skill/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

脚本会删除 Agent 服务和 Skill；删除 Skill 前会校验 `examples.owner`。

## 常用参数

| 参数 | 默认值 |
| --- | --- |
| `-namespace` | `go-agent-naming-examples` |
| `-skill` | `polaris-greeting` |
| `-skill-version` | `1.0.0` |
| `-service` | `trpc.polaris.examples.skill-agent` |
| `-skill-cache` | 系统临时目录 |
| `-port` | `18193` |

发布、安装、`skill_load` 和模型请求注入的自动验证见
[Skill 集成测试](../../integration/skill/e2e_test.go)。
