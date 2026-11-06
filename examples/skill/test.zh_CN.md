[English](test.md) | 中文

# Skill 示例测试

### [case 2.1 publish real Skill]

操作：通过真实 Polaris Skill Console API 发布
`trpc-agent-naming-demo-skill@1.0.0`，随后用 `SkillAPI.GetSkill` 和真实 ZIP
`SkillAPI.DownloadSkill` 等待数据面可见。

预期：Console 状态为 `published`，Skill 数据面返回一致的身份、ZIP content type、
非空内容和 64 位 SHA-256 digest。

### [case 2.2 install and execute real Skill]

操作：使用显式 SkillRef 启动 Provider，等待注册，通过 Polaris 发现后调用 Agent。

预期：响应包含安装的 Skill 名称、版本，以及来自 `SKILL.md` 的
`POLARIS_DEMO_SKILL=1.0.0` 标记。

## 清理

verify 不停止进程。默认 `cleanup.sh --force` 停止 Provider 并删除本地产物；
追加 `--delete-service` 才会删除示例 Skill 和 service。
