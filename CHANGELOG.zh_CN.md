# 更新日志

[English](CHANGELOG.md) | [简体中文](CHANGELOG.zh_CN.md)

本文件记录项目的重要变更。

## v0.1.0 - 未发布

- 增加 Runtime 配置、可选的 SDK Context 所有权和生命周期管理。
- 构建并校验 A2A 0.3/1.0 AgentCard、完整的多接口 Card、原始 JSON
  和签名保留，以及三个 AgentCard 元数据键。
- 接受 SDK 实例 metadata 中提升的 `version`、`region`、`zone` 和 `campus`
  平台字段，同时拒绝其他未知 metadata 键。
- 将动态元数据 Provider 与 `go-naming-polarismesh` 集成。
- 增加元数据、host、版本、注册端口、协议和 TTL 的原子快照，并将默认注册端口
  设为 `1`，实例协议保持为空。
- 扩展层独占 AgentCard/A2A/MCP 准备及静态配置冲突策略，通过通用快照接入 naming
  唯一实例生命周期 owner；naming 不再解析 Card 或分类 metadata。
- 增加 `WithAgentInstanceMetadata`，仅接受静态三个 AgentCard 元数据键并保留
  原始 Card JSON。
- 按官方示例风格重写可运行示例，覆盖 LLM Agent 的 A2A、MCP Service Contract
  与工具发现，以及 Skill 发布、安装和加载流程。
- 将单体 E2E 运行器替换为 A2A、MCP、Skill 和 A2A AgentCard 四个真实服务
  集成套件，使用确定性或脚本化模型并只清理本轮创建的资源。
- 将 `polaris-go` 和 `go-naming-polarismesh` 固定到可获取的 main 快照，
  升级 tRPC-Go 到 v1.1.0，并移除 filesystem replace。
- 增加由适配层维护的 `AgentAPI`，使用 ConsumerAPI 完成默认服务读取，并
  通过借用的 SDK Context 集成 SkillAPI。移除 Core 中的
  ServiceAdminAPI 和 AgentAPI 后，将 Console 管理、请求模型、配置和回归
  测试迁移到适配层内部，不保留 Core 兼容层。
- 可选服务管理必须显式启用 `agentapi.WithConsole`。保留三态更新、
  revision/owner/Force 防护、未知元数据、结果不确定的写入恢复，以及
  服务端对最终 Skill 空列表的限制。
- 当资源缺少 revision 时保持为空，并保留借用 SDKContext 的所有权约定。
- 增加 `names`、`published` 和 `mounted` Skill 模式、分页加载已发布
  Skill、`namespace:name` 服务解析、失败阈值、容量上限、过期缓存修复
  和原子周期刷新。
- 增加健康实例发现、AgentCard schema/host 校验、按
  `protocolBinding` 选择接口、粘性选择、跳过错误 Card、指标上报，以及
  绑定到请求接口的缓存 Card 兜底。
- 将固定 tRPC-Agent A2A 客户端限制为 JSONRPC 和 HTTP+JSON，在服务发现前
  拒绝不支持的绑定，跳过不兼容的 wire 版本，同时允许 0.3 Card 声明
  可消费的 1.0 附加接口。
- 将 A2A 客户端调用路由到选定接口的 URL，并从其声明的接口路径提供
  JSON-RPC 服务，包括多接口 Card。
- 增加可选的 MCP 服务端元数据、Contract 采集与上报、SSE、Streamable
  HTTP、stdio、客户端池/Watch 替换检测和 `InstanceTypeMCP` 调用上报。
  Contract 上报使用 `ProviderAPI.ReportServiceContract`，查询使用
  `ConsumerAPI.GetServiceContract`。
- 为 MCP 初始化和工具调用增加超时限制，在连接池刷新或关闭期间保护正在
  使用的连接器，并将 MCP 结果级错误上报为失败。
- MCP Connector 请求串行执行，排队支持取消与超时，保留初始化能力，并仅采集、
  投影已声明的契约能力。禁用不安全的独立 Streamable HTTP GET SSE，保留
  POST SSE 及旧版 SSE；使用 MCP v0.0.17 修正 stdio 进程关闭竞争。
- 发现结果保留完整 AgentCard 和默认流式行为，非法接口版本不再触发 panic；
  Skill 分页按原始窗口推进，刷新器的启动、关闭和任务排空由 Runtime 统一管理。
- Resolver 的回退、粘性 caller 和候选实例状态按协议 binding 隔离，避免一个
  binding 的失败清除另一个 binding 的缓存。
- published Skill 查询和安装受剩余加载 deadline 限制，自有 SDK 的 Skill
  message/download 超时与配置的 Polaris 请求超时保持一致。
- 保留旧的 mounted 与显式 Skill 合并行为，并让空或重叠的 Skill 刷新
  以确定的方式收敛。
- MCP 通知名称超过 Polaris 32 字符 InterfaceDescriptor 方法名限制时，
  在 Contract JSON 中保留完整名称而不截断。
- 增加 A2A 客户端和服务端辅助工具。
- 增加 `a2a-agentcard` 示例：已注册 Agent A 调用两个不同版本的已注册
  Agent B，并提供双语指南和受保护的清理流程。
- 增加按 build tag 隔离的 A2A、MCP、Skill 和已注册 Agent A/B 链路集成
  套件、共享真实北极星 harness，以及按测试清理自有资源的机制。
- 将用户可运行程序统一放在 `examples/`，将完整真实服务验证放在
  `integration/`，并为两个目录维护双语指南。
- 增加示例级和按 run ID 的清理脚本，支持 dry-run、实例身份、
  Service Contract 和 Skill owner 校验。
- 将 a2a-agentcard Provider 默认切换为北极星 AI 注册中心的 1.0/0.3
  AgentCard 多协议增强和 no-serve 注册；显式 `-role` 保留多版本 Agent A/B 流程。
- 增加 `integration/verify_all.sh`，支持显式指定服务端、namespace、run ID
  和单次操作超时。
- 在可运行示例和独立的真实 a2a-agentcard 集成套件中覆盖已注册 Agent A
  调用 Agent B 的嵌套链路。
- 记录使用显式空 `extended_metadata` 清理服务 Skill 引用时要求 Polaris
  `v2.2.0.0` 或更新版本。
- 增加测试、race 验证、README 文档和 CI。
- 要求 Go 1.21。
