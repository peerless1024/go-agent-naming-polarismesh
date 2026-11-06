[English](test.md) | 中文

# AI Registry 真实 E2E

本文档与 `verify_all.sh`、`cleanup.sh` 和 `internal/demo/` 的用例保持一致。

## 前置条件

需要 Go 1.21+、bash、`ps`，以及已支持 AI_AGENT、AgentCard、Console Service Writer 和 Skill Registry 的真实 Polaris。
用例 3.7 的“显式空 extended_metadata 清除 Skill 引用”要求 Polaris
`v2.2.0.0` 或更新版本；`v2.1.1.0` 会返回 `200002` 且保留旧引用。
当前开发依赖通过根模块的本地 replace 解析；示例不另建模块，不改动依赖仓库。
不需要 LLM API key：Provider 使用真正的 tRPC-Agent Runner 执行确定性 Agent，Skill 内容来自真实 Registry 下载并装配的 Repository。

服务端地址必须通过 `--server` 或 `POLARIS_SERVER` 提供，代码中没有默认地址。Discovery 默认使用 `8091`，Console 默认使用 `8090`，Skill gRPC 默认使用 `8094`。
端口通过 `--discover=host:port`、`--console=http://host:port`、`--skill-address=host:port` 覆盖。
Provider 监听消费者可访问的 `127.0.0.1` 随机端口；不要求云端主动访问本机。
可用 `--advertise-host` 改为本机局域网地址。A2A 认证使用公开的测试夹具 key，不是部署凭据。

真实鉴权配置只读以下环境变量，或读取 `--config-file` / `POLARIS_E2E_CONFIG` 指向的已有 SDK 配置：

- `POLARIS_TOKEN`：Discovery SDK token。
- `POLARIS_CONSOLE_TOKEN`：Console / Service Writer token。
- `POLARIS_SKILL_TOKEN`：Skill token。
- `POLARIS_SERVICE_TOKEN`：注册和心跳 token。

token 不进入命令行、state、results 或测试日志。不要把 access-bearing URL 写入 `--console`。

## 运行

每轮必须显式选择资源策略。命名空间固定使用非 `default` 的 `go-agent-naming-e2e`，服务和 Skill 都包含随机 run ID。

```bash
export POLARIS_SERVER=your-polaris-host
./verify_all.sh --server="$POLARIS_SERVER" --resources=cleanup
./verify_all.sh --server="$POLARIS_SERVER" --resources=keep
```

脚本构建 Provider、Consumer 和 E2E 二进制，均启用 race detector。
默认单次操作超时 5 秒，完整套件超时 12 分钟；用 `--timeout` 和 `--run-timeout` 覆盖。
`--debug` 开启 SDK debug 日志。`GO_BIN` 可选择另一套 Go 工具链；`GOCACHE` 可选写缓存位置。

E2E 按顺序执行所有 24 个用例，无 mock、无 skip，失败仍写入本轮 `results.json` 并以非零码退出。
本轮目录为 `.logs/<run-id>/`，包含 `state.json`、`results.json`、每个 Provider 的独立工作目录及日志。
`--run-id` 和 `--run-dir` 可显式指定；verify 的 run-dir 必须以该 run ID 结尾，防止清理扩大范围。

`keep` 保留本轮服务、Skill 和最后的 Skill Provider。验证摘除所需的节点仍会按用例停止；其他资源不额外删除。
`cleanup` 在成功或失败后停止本轮已记录进程、检查服务器所有权标签，再删除本轮实例、服务和 Skill；共享命名空间始终保留。
清理失败会使本轮退出码非零，不会记为成功。构建产物不随单轮清理删除，以免影响其他轮正在使用的二进制。

```bash
./cleanup.sh --state=.logs/<run-id>/state.json --dry-run
./cleanup.sh --state=.logs/<run-id>/state.json --apply
./cleanup.sh --state=.logs/<run-id>/state.json --apply --delete-service
./cleanup.sh --state=.logs/<run-id>/state.json --apply --delete-service --artifacts
./cleanup.sh --delete-service --dry-run
./cleanup.sh --delete-service --apply
```

cleanup 默认只展示，不执行删除。`--apply` 或 `--force` 是显式授权，`--dry-run` 始终优先；verify/provider/consumer 模式拒绝 cleanup 参数，避免 dry-run 意图下仍执行真实写入。
按 state 执行 `--apply` 默认只停止本轮记录且身份匹配的进程；不带 `--delete-service` 时服务和 Skill 保留。
`--delete-service` 额外删除该 state 的服务和 Skill，并扫描所选非 `default` 命名空间中的历史零实例服务；`--delete-services` 是别名。历史扫描只选择名称以 `go-agent-e2e-` 开头、owner 匹配且真实实例数为 0 的服务。
Provider/Consumer 二进制不允许通过 `--mode` 切换角色；E2E 二进制只接受 verify、cleanup、inspect，入口和模式必须一致。
`--artifacts` 在成功清理后仅删除当前 run 的 Provider 工作目录、缓存和日志，保留 state 和 results。
进程必须匹配 PID、启动时间、完整命令、run ID、service 和 ready 路径；拒绝 PID 复用。
服务或 Skill 必须同时匹配 `e2e.owner` 和 `e2e.run.id`；包含其他实例的服务不删除。

独立调用保留中的 Provider：

```bash
./.build/consumer --namespace=go-agent-naming-e2e \
  --service=go-agent-e2e-<run-id>-skill-provider --message=skill
./.build/consumer --namespace=go-agent-naming-e2e \
  --service=go-agent-e2e-<run-id>-skill-provider --message=hello --stream
```

## 用例编号

### [用例 1.1 registration-metadata-and-heartbeat]

操作：通过真实 go-naming Registry、Runtime metadata hook 注册 Provider；经 SDK 查询四键 metadata、版本、Tenant；等待 7 秒，跨过多个 TTL 周期；有界等待 15 秒验证真实 AgentAPI.GetService 的服务投影。
预期：AI_AGENT 服务、非空服务级 revision、精确 endpoint、四键一致，TTL=2 秒的实例仍健康，证明周期心跳有效。投影失败仍保留已建立的独立真实 A2A 夹具用于后续测试，不将本例记为通过。

### [用例 1.2 unary-authentication-and-tenant]

操作：Consumer 从真实 Discovery 解析 AgentCard，发送 unary 请求；再用无认证 Client 发送请求。
预期：认证请求成功并返回 node-a 的 echo，未认证失败；Provider 统计两种请求。业务 Processor 断言实际 Tenant 等于广告接口 Tenant，记录 accepted/rejected，缺失或不符拒绝。

### [用例 1.3 streaming-output-and-cancellation]

操作：调用流式 echo；再发持续输出请求，观察首个文本事件后取消 context。
预期：完整 echo、多事件成功；3 秒内客户端事件流关闭，5 秒内 Provider active=0 且 canceled 增加。

### [用例 1.4 exact-root-and-unicode-endpoints]

操作：分别注册 `/`、`/a2a/`、`/api/a2a`、`/智能体/a2a` 实际 Provider，经广告 URL POST。
预期：均得到成功业务响应，不跟随重定向；每个临时节点按测试流程退出。

### [用例 2.1 two-instance-sticky-discovery]

操作：增加第二真实实例，重复解析并调用 sticky caller 12 次，使用 32 个新 caller 检查稳定散列。
预期：原 caller 仍选 node-a，新 caller 能选到两个健康实例。

### [用例 2.2 invalid-card-and-host-port-filtering]

操作：通过真实注册器添加缺失 card、card endpoint 端口不符的健康实例，再解析 16 个 caller。
预期：两种坏节点确实已注册，Consumer 只选择合法实例。

### [用例 2.3 isolation-and-unhealthy-filtering]

操作：Console 更新本轮实例 isolate；核验 PID、启动时间和完整命令后 SIGSTOP 暂停本轮 node-a 的心跳，取消隔离；有界等待 30 秒观察 Console 与真实 Discovery 的非隔离、不健康投影；SIGCONT 恢复。
预期：隔离和 TTL 过期的不健康状态分别被真实观察，被隔离/不健康节点不被选中；结束时恢复心跳并等待健康，不修改其他服务。

### [用例 2.4 concurrent-real-clients]

操作：24 个 caller 同时从真实 Registry 构建独立 mutable Client，共享 Transport，混合 unary/stream 请求。
预期：所有调用得到完成事件和完整输出，race detector 无告警；仅关闭流或 partial 文本不能算成功。

### [用例 2.5 deregistration-and-sticky-failover]

操作：SIGTERM node-a，经注册器反注册，观察 Discovery 摘除，用原动态 Agent 调用。
预期：切换到 node-b；Provider ready 证明 Close 幂等、provider 移除且借用 SDK 未被销毁。

### [用例 2.6 empty-set-invalidates-remote-agent]

操作：停止剩余本轮主服务节点，观察真实空实例集，再用已创建动态 Agent 发请求。
预期：`ErrNoAvailableInstance`，不恢复旧 endpoint 或从旧缓存发请求。

### [用例 2.7 real-network-fallback-expiry-and-recovery]

操作：专用消费者通过透明 loopback TCP gate 使用真实 Polaris Discover stream；有界等待 25 秒建立初始连接，查询预算等于 --timeout；断开此 gate，不停止远端服务；等待 31 秒；恢复；随后摘除 Provider、读取空集，再断网。
预期：初始网络故障确实计数且可回退调用；30 秒 TTL 后不可用；恢复真实发现后成功；空集失效后断网也不能恢复旧卡片。

### [用例 3.1 published-skill-download-and-install]

操作：Console 为本轮 Skill 创建 draft，分隔 1.2 秒后明确 publish，有界等待 30 秒至指定版本的真实元数据和 ZIP 可见，再 SDK 查询、下载 ZIP、Install 到隔离 cache。
预期：身份、版本、非空 ZIP、SHA-256、入口和 references 文件完整，首次非 cache hit。

### [用例 3.2 missing-draft-and-wrong-version-fail-fast]

操作：查询和 Loader 装配不存在、draft、错误版本的真实 Skill。
预期：`ErrCodeSkillNotFound`；Loader 不返回部分 Repository。

### [用例 3.3 cache-hit-and-content-repair]

操作：重复安装 pinned Skill，篡改本轮缓存入口，再安装，最后 Force 安装。
预期：重复命中不变更 digest/download identity；篡改触发重新下载修复；Force 不返回 cache hit。

### [用例 3.4 active-version-and-pinned-cache]

操作：发布 2.0.0，有界等待 30 秒安装 active version 及正文收敛，再安装 pinned 1.0.0。
预期：active=2.0.0 且正文版本一致，旧版本命中且目录不冲突。

### [用例 3.5 multiple-skill-dedup-and-sorting]

操作：倒序并重复提交两个真实 Skill 引用给 Loader。
预期：Repository 只暴露两个所选入口，排序稳定且内容可读取。

### [用例 3.6 merge-mount-retains-latest-references]

操作：有界等待取得旧 Discovery 服务快照，更新真实 Console 引用，再进行 Loader merge + UseServiceSkills。
预期：基于最新 Writer 返回值组装完整集合，不覆盖已更新引用。

### [用例 3.7 replace-and-empty-mount-remove-old-skills]

操作：真实服务挂载两个 Skill；Loader replace 成单个，再 replace 空集合。
预期：只执行新集合，空集合不暴露缓存中的旧入口，Console 显式空引用写入成功。

### [用例 3.8 multiple-entry-skill-is-rejected]

操作：发布包含额外 `SKILL.md` 的真实 ZIP，调用 Loader。
预期：下载/入口校验失败，不提供额外可执行 Repository。

### [用例 3.9 provider-executes-published-skill]

操作：真实 Provider 在 Prepare 安装 pinned Skill，Runner 的业务 Agent 从 Repository 读取 summary.Name 与原正文，经真实 A2A 回传。
预期：返回所安装的 Skill 名及 pinned 1.0.0 正文；不是本地伪造安装结果，不依赖 LLM key。

### [用例 4.1 ordinary-service-type-conflict]

操作：Console 创建本轮普通微服务，Runtime 启用 EnsureService。
预期：明确 type conflict，Prepare 不暴露 AgentCard，不把已有服务转换成 AI_AGENT。

### [用例 4.2 prepare-retry-and-borrowed-context-lifecycle]

操作：缺失 Skill 的 Prepare 失败；发布真实 Skill 后重试；重复 Prepare、Close。
预期：失败撤销 provider，无部分 Repository；重试成功；借用 SDK 仍能查询真实 Skill；Close 后拒绝 NewRemoteAgent。

### [用例 4.3 ensure-existing-service-is-nondestructive]

操作：真实服务已有 Skill 引用，以不同 metadata 和 Skill 再 Ensure。
预期：已有 metadata 和引用不变。

### [用例 4.4 cleanup-state-and-ownership-safeguards]

操作：校验 default namespace、生产服务名、access-bearing URL、根目录等非法状态；执行本轮 dry-run。
预期：非法状态拒绝且错误不泄露合成凭据；dry-run 保留实际资源。

## 失败排查

- 请求走公司代理失败：示例 HTTP Client 对已指定地址直接连接；SDK Writer 如受环境代理影响，请设置该主机的 `NO_PROXY`。
- Discovery/Skill 超时：确认 8091/8094 开放、服务端能力和 token；连接失败不能算作通过。
- AgentAPI.GetService 报 no service-level revision：服务端 SERVICES 列表每项必须投影自身 revision、AI_AGENT 类型和 extended metadata；列表级 revision 不能代替。只读 inspect 同时记录 SERVICES 与 INSTANCE；若 INSTANCE 也缺少类型或 extended metadata，应升级服务端，不能从实例 metadata 反推服务类型或改用 Console 冒充 Discover。属于服务端能力问题时保留失败，不绕过契约。
- Console published 但客户端 SkillNotFound：对照真实读路径；按秒 mtime 增量缓存可能漏掉同秒创建/发布。示例采用明确 draft/publish，仍必须等待客户端可读，不将 Console 写成功算作下载成功。
- Provider 未就绪：读取本轮 `providers/<node>/provider.log`，确认 Prepare、Skill 安装和注册的明确错误。
- explicit empty extended metadata 未应用：服务端必须支持清空 Writer 语义；不得降低空集合断言以求通过。
- 历史空服务残留：先执行 `./cleanup.sh --delete-service --dry-run`，核对命名空间、owner 和零实例过滤结果，再使用 `--apply`。不要绕过 owner/prefix 检查直接按服务名批量删除。
- cleanup 拒绝所有权/PID：先 dry-run 并检查 state，只处理本轮资源，禁止广泛匹配服务名或进程名删除。

本文件描述覆盖范围，不代表执行成功。最新实际结果以对应 run 的 `results.json` 和项目 `cr.md` 为准。

只读协议诊断：

```bash
./.build/e2e --mode=inspect --namespace=go-agent-naming-e2e \
  --run-id=<run-id> --run-dir=.logs/<run-id> \
  --service=go-agent-e2e-<run-id>-main \
  --skill-name=gan-e2e-<run-id>-b --skill-version=1.0.0
```

生成 `service-projection.json` / `skill-projection.json`。其中服务文件分别记录 `SERVICES` 列表和 `INSTANCE` 响应是否含有自身 revision、AI_AGENT 类型、run metadata 及 extended metadata；两种投影不能互相替代。文件仅记录身份、字段能力和状态，不输出 token、原始 metadata 或 Skill 正文；此诊断不算 E2E 用例通过。
