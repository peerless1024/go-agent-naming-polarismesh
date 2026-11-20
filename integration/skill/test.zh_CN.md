[English](test.md) | 中文

# published Skill 安装与模型请求注入

## 测试函数

[e2e_test.go](e2e_test.go) 中的
`TestPublishedSkillInstallAndModelInjection`。

## 拉起的进程

| 进程 | 数量 | 作用 |
| --- | --- | --- |
| 预构建测试二进制 | 1 | 发布 Skill、通过 SkillAPI 安装、启动进程内 A2A Server、发现 Agent 并运行脚本化模型 |
| A2A Server | 进程内 | 在 loopback listener 上提供真实 A2A handler |
| Polaris | 外部 | 通过 `POLARIS_SERVER` 提供发现、Console 和 Skill Registry |
| LLM | 不使用 | 使用脚本化 `model.Model` 驱动真实 `llmagent` Skill 路径 |

该用例不启动独立 Provider、Client 或模型进程。真实 Skill 数据面、
`llmagent`、`skill_load` 和 A2A wire 都在测试进程内运行。

## 注册的服务与资源

| 资源 | 值 |
| --- | --- |
| Namespace | 默认 `go-agent-naming-e2e` |
| Agent Service | `gan-e2e-<run-id>-skill-agent` |
| 服务类型 | `AI_AGENT` |
| Agent 实例 | 1 个健康实例 |
| 实例 host | `127.0.0.1` |
| Registration port | `1` |
| 实例 version | `v1.0.0` |
| 实例 protocol | 空 |
| Skill | `gan-e2e-<run-id>-skill@1.0.0` |
| Skill 文件 | `SKILL.md` 和 `references/data.md` |
| Skill marker | 发布正文中包含 `marker-<run-id>` |

## 调用关系

```text
测试进程
   │
   ├─► Console Skill API ─► 创建并发布 Skill
   │
   ├─► SkillAPI ─► GetSkill / DownloadSkill ─► 安装到缓存
   │                                      │
   │                                      ▼
   │                              Skill Repository
   │                                      │
   │                                      ▼
   │                              llmagent.WithSkills
   │
   └─► Consumer Runtime ─► Polaris Discover ─► A2A Agent
                                                  │
                                                  ├─► 模型第一次调用：skill_load
                                                  ├─► 注入 Skill 正文
                                                  └─► 模型第二次调用：skill-loaded
                                                         │
   ◄──────────────────── A2A response ───────────────────┘
```

第一次脚本化模型调用请求 `skill_load`。真实 `llmagent` processor 随后加载
已安装的 Skill 正文，并把 marker 放入下一次模型请求；第二次调用返回
`skill-loaded`。

## 用例

### [用例 1.1 发布与数据面就绪]

操作：通过 Console Skill API 创建 Skill，并发布 `1.0.0`。

预期：`GetSkill` 和 `DownloadSkill` 都能读取已发布 Skill，且 ZIP 内容非空。

### [用例 1.2 安装与 Repository 组装]

操作：使用 `SkillModeNames` 和显式 Skill 引用准备 Provider Runtime。

预期：Skill 从 Polaris 安装成功，`runtime.SkillRepository()` 返回非空
Repository。

### [用例 1.3 Skill 加载与 Prompt 注入]

操作：用脚本化模型调用 Agent。

预期：模型先调用 `skill_load`；下一次模型请求包含
`POLARIS_SKILL_MARKER=marker-<run-id>`；模型至少被调用两次。

### [用例 1.4 发现与 A2A 响应]

操作：通过 Polaris 发现 Skill Agent，并通过 JSONRPC A2A 接口调用。

预期：Agent 返回 `skill-loaded`，实例 metadata 包含三个 AgentCard 键，
protocol 为空。

## 验证方式

测试断言：

- Console 发布成功，Skill 数据面可读。
- 安装后的 Repository 非空。
- 真实框架执行了 `skill_load`。
- 已发布 Skill 的 marker 进入模型请求。
- 模型调用次数至少为两次。
- A2A 发现和最终 `skill-loaded` 响应。
- 服务与 Skill 的清理所有权正确。

## 清理

在 `t.Cleanup` 中反注册 Agent Runtime、关闭 A2A Server、删除 AI_AGENT
服务并删除已发布 Skill；共享 namespace 保留。

使用秒级 Skill 缓存时间戳的 Polaris 版本要求 `create` 与 `publish` 跨过一秒
边界。测试会在发布前等待下一个时间窗口，避免服务端 `mtime > last` 增量缓存
漏掉同一秒内的 published 状态更新。

如果 Skill 地址负载均衡到多个缓存新鲜度不同的实例，测试会为每次校验创建
新 SDK，并保留第一条既能读取又能下载 Skill 的连接；Provider Runtime 后续
复用这条已验证连接完成安装。

## 运行

```bash
make -C integration build
POLARIS_SERVER=your-polaris-host \
  "integration/bin/$(go env GOOS)-$(go env GOARCH)/skill.test" \
  -test.count=1 -test.v -test.timeout=10m
```
