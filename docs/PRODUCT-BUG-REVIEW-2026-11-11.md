# ZeoKeel v0.2.23 全链路产品缺陷评审（2026-11-11）

> **评审基线**：`56da072`（v0.2.23），工作区干净。
> **方式**：Lead + 5 路独立只读评审（Agent loop / Prompt·上下文注入 / MCP·工具·权限 / 记忆·技能 / 产品·UX），Lead 复核合并。**未修改任何源码、测试或既有文档**；新增的只有本报告与 `.cache/review/`（gitignored）下的评审报告/探针。
> **基线验证**：`go build ./...`、`go vet ./...`、`go test ./... -count=1` 全绿；`ui-drive` 91/91 PASS、`ui-snapshot` 102 帧、`designlint --strict` 0 new/0 blocking、`design-check.sh` 6/6 PASS。
> **完整证据**（每条的复现命令、探针源码、原始输出）：
> - `.cache/review/loop-engineer.md`（loop）
> - `.cache/review/injection-engineer.md`（prompt/context injection）
> - `.cache/review/mcp-tools-engineer.md`（MCP/tools/permissions）
> - `.cache/review/memory-skills-engineer.md`（memory/skills）
> - `.cache/review/product-manager.md`（product/UX + 文档落差）
> - 探针：`.cache/review/probes/`、`.cache/review/repro/`、`.cache/review/work/`、`.cache/review/lead/`（全部通过 `go test -overlay` 注入，仓库零改动）。

---

## 0. 结论摘要

## 0.1 v0.2.24 修复状态（2026-11-11）

本报告的修复在 v0.2.24 落地，TDD：每项先补会失败的回归测试再改实现。逐项证据见 `.cache/review/fix-*.md` 与 `CHANGELOG.md` 的 `[0.2.24]`。

| 状态 | 条目 |
|---|---|
| **已修复** | **P0**：FR-01。**P1**：FR-02、FR-03、FR-04、FR-05、FR-06、FR-07。**P2（除 FR-28 外全部）**：FR-08～FR-27、FR-29～FR-31（其中 FR-12 的 Fork 部分按产品语义保留“复制可见 transcript”，非缺陷；FR-26 保留装配默认模型、UI 文案对齐；FR-27 通过 per-run 工作区根 + 会话切换 + ACP `cwd` + 自动化绑定实现）。**P3**：LOOP-P3-1/2/3、MCP-04、MCP-05、M-04～M-09、M-11、S-02～S-06、PM-11、PM-12、PM-13、PM-14、PM-15（文案/恢复动作部分）、FR-20（工作区技能信任标记）、INJ-06/07/08/10/11/13（随 FR-13/14/17/19/21 一并修复）。 |
| **明确 Deferred（v0.2.24 未修，已记录在 CHANGELOG「已知限制」）** | FR-28（goal 仍宿主级单例：跨会话显示/自动续跑/不持久）；PM-10（后台/自动化完成失败无系统通知）；LSP-03（LSP 不随工作区重绑）；MCP-06（workspace `mcp.json` 全局 opt-in、无按仓库信任）；INJ-09/INJ-12 余项/INJ-14（headless 自定义审批文案、headless identity/readOnly 文件策略、引用未声明省略工具输出）；M-10 的跨进程文件锁；PM-09 记忆术语完全统一；PM-15 的“端点为空不注册 web_search”注册逻辑；FR-12 的压缩感知 Fork。 |
| **验证** | `go test ./...` 全绿（含 `-race`、regression、uimodtest、design-contract）；`ui-drive` 91/91 PASS；designlint 0 new / 0 blocking；`scripts/design-check.sh` 6/6；`go build`/`go vet`/`gofmt`/`go tool mygo vet` 干净；`scripts/verify.sh` 全量（含 snapshot/smokes）。 |


**整体 loop、审批安全默认、UI 基础交互是稳的**（全量测试绿、91/91 驱动 PASS、审批无默认动作/Esc=拒绝/危险确认取消为 Default 均真实成立）。问题集中在四类系统面：

1. **失败路径会放大成产品不可用**：配置两台可连通的 MCP server 会让下一次启动直接退出；`memory.json` / `workspaces.json` / `automations.json` 任一损坏都会让整个装配回滚、桌面端静默退出；一台挂起的 MCP server 让启动白屏 30s。
2. **权限矩阵分类错误**：`settings_get` 被当作"信息类"自动放行，模型可免审批读出 LLM provider / 搜索 API Key 明文；自动化任务的"只读"会话级 override 会落穿到全局 workspace 策略，`write_file` 被静默放行；只读预设下 `memory` 的 add/remove/clear 也免审批。
3. **模型上下文与真实执行面不同步**：plan/minimal/creator 模式只在 gate 执行，system prompt 与工具 schema 完全不知情；桌面模型可见 70 个工具却只有 14 条工具指引；AGENTS.md 裸拼进 system prompt（可 symlink/`..` 越界、可伪造系统小节）；预设工具白名单只约束主 run，子代理/team 全量可用；`tools.disabled` 开关对后注册工具无效。
4. **"记忆/技能"没有注入，产品门面过度承诺**：memory 条目与 skill 正文从不进入 system prompt，只有工具定义；用户保存记忆后，模型不主动 `memory list` 就永远看不到。同时首启没有任何"缺模型/API Key/工作区"的有效引导，模型/工作区/目标/审批的会话语义存在多处"只是标签"的落差。

**数量（合并去重后）**：P0 ×1、P1 ×6、P2 ×23、P3 ×24。

---

## 1. P0 · 阻断级（1）

### FR-01 两台可连通 MCP server → 启动时 registry 重名 panic → 应用静默退出

- **证据**：`internal/modules/toolkit/toolkit.go:262-264` 对每个 server 都用空前缀注册 `mcp.ResourceTools(client, "")`；rig `tools/mcp/resource_tools.go:14-20` 的 5 个资源工具名是**全局常量**（`mcp_list_resources` 等，不带 server 身份）；rig `tools/registry.go:30` 的 `NewRegistry` 遇重名直接 **panic**；`internal/host/host.go:568-575` 装配失败 → `App.Quit()`，窗口从未创建。
- **复现**：`go test -overlay=/data/mygo/.cache/review/repro/overlay.json ./internal/modules/toolkit -run TestReproTwoStdioMCPServers -v`
  → `toolkit activation failed: ... panic: tool: NewRegistry: tool: duplicate tool name "mcp_list_resources"`
  （Lead 已独立复跑确认）。
- **影响**：用户配置第二台可连通 MCP server（或单台 server 返回 sanitize 后撞名的两个工具）后，**下次启动应用直接退出**；GUI 构建无控制台，用户只看到"窗口没了"，只能手工改 settings 文件才能恢复。headless/ACP 同样失败。
- **修复**：① 资源工具按 server 命名空间加前缀；② MCP 装配改为逐个 `Register` + 重名跳过 + 日志（LSP 路径已有此模式 `toolkit/lsp.go:119-138`）；③ 激活期整体 `recover`，把单 server 问题降级为失败 plugin 条目而非整模块失败。

---

## 2. P1 · 立即修（6）

| ID | 缺陷 | 核心证据 | 影响 |
|---|---|---|---|
| FR-02 | `settings_get` 在 read-only/workspace 预设下**自动放行**且返回原值：模型免审批读出 `chat.apiKey`、`web.search.apiKey`、`mcp.servers` 明文 | `internal/contracts/subagents.go:105`；rig `tools/settings/settings.go:80-110`；复现 `.cache/review/repro` → `settings_get(chat.apiKey) => "sk-provider-SECRET-123"` | 被提示注入的模型可直接取得用户凭据并外带；既有 AUDIT-P3-13 只修了诊断导出，模型侧路径未覆盖 |
| FR-03 | 自动化任务「只读」→ `SetSessionPermission(read-only)`，但 gate 对非只读工具不 deny 而是落穿到全局 workspace 策略；`write_file` 被 `WorkspaceAutoAllowedTools` **静默 allow**，而 prompt 同时声称"写文件会询问" | `internal/modules/agent/plan_mode.go:178-191,211-235`；`internal/contracts/subagents.go:136-144`；`internal/modules/approvals/approvals.go:250-258`；`internal/modules/automation/automation.go:708-712`；探测输出 `gate(write_file) with session read-only override = "allow"` | "只读"任务可无弹窗写工作区文件；权限语义与模型上下文双重错误（若判定"落穿"是有意设计，至少必须改文案/标签并降级为 P2） |
| FR-04 | LSP 接线缺 `Initialize`：`lspStartFunc` 只 `lsp.Start` 就返回，合规 LSP server 全部 `-32002 Server not initialized` → `lsp_*` 功能整体不可用 | `internal/modules/toolkit/lsp.go:45-51,106-113`；rig `tools/lsp/client.go:152,501-536`；探测：`server received initialize: false`，工具结果 `Server not initialized` | 配置 LSP 后所有 `lsp_*` 工具必然失败；既有测试用 fake session 注入，未覆盖真实 client |
| FR-05 | `lsp_*` 工具未安装 sandbox `PathPolicy`：可读取工作区外任意文件并 `didOpen` 给语言服务器，绕过 roots/`deniedGlobs`，且这些工具在只读/工作区预设下自动放行 | `internal/modules/toolkit/lsp.go:124-126`；rig `tools/lsp/tools.go:22-24,135-136,308-323`；探测：工作区外文件被读入并同步，`deniedGlobs=["**/*.secret"]` 未拦截 | 沙箱"所有文件工具受 roots/deniedGlobs 约束"的承诺被 LSP 打破，且无需审批 |
| FR-06 | 任一用户数据文件损坏都会让**整个产品启动失败**：`memory.json`、`workspaces.json`、`automations.json` 的 parse 错误直接使对应模块激活失败 → assembly 整树回滚 → 桌面端 `App.Quit()`，无对话框/无回退 UI | `internal/modules/memory/memory.go:173-229`（及 workspace/automation 同类 load）；`internal/host/host.go:568-575,643-644`；Lead 探针 `.cache/review/lead/zz_lead_resilience_test.go` 三种文件全部复现 `assembly failed to start` | 记忆/工作区/自动化都是辅助功能，却共享"一个字节写坏 = 应用打不开"的失败半径；普通用户只能手工去数据目录删文件 |
| FR-07 | 首次运行没有当前有效的引导：Home 自 v0.2.6 起已不是导航目的地，其 `buildSetupCard`/`buildNextSteps` 成为**不可达代码**；真实落地页（session 空态）不提示缺模型/API Key/工作区；未开工作区时工具直接报英文 `sandbox: denied: no roots configured` | `internal/ui/sidebar/sidebar.go:404-411`；`internal/uiroot/frame.go:280`；`internal/ui/session/view.go:995-1012`；`internal/modules/toolkit/rebind.go:57-77`；`CHANGELOG.md:237` | 新用户第一条消息失败、第一条工具报英文错误，不知道下一步去哪；等于默认拒绝新用户 |

> FR-03 与 FR-02 同属"审批矩阵分类错误"；FR-04/FR-05 同属 LSP 接线，建议同一 PR 修。

---

## 3. P2 · 下一轮（23）

### A. Loop / 会话生命周期（5）

| ID | 缺陷 | 证据 | 影响 |
|---|---|---|---|
| FR-08 | `/compact` 用压缩开始时的**陈旧快照**重写历史，压缩期间（LLM 调用最长约 20s）开始的新回合并发写入后被覆盖 → durable history 静默丢回合，而进程内读模型仍显示 | `internal/modules/agent/compact_now.go:30-35,45-104`；`agent.go:1312-1315`；`internal/ui/session/compact_command.go:36-48`；复现 `TestReproCompactRaceLosesConcurrentTurn` | 重启后该回合消失；live ≠ durable |
| FR-09 | 删除**运行中**会话后，rig 用 `context.WithoutCancel` 的落盘写回会 `ensureSessionTx` 重建 session 行 → 重启后出现空标题幽灵会话 | `internal/modules/agent/agent.go:1148-1183`；rig `agent/agent.go:328-340`；`internal/modules/sessionstore/store.go:529-552,825-833`；真实 SQLite 复现 | 幽灵会话回到侧栏；其孤儿 tool 消息还可能让后续 provider 400 |
| FR-10 | 在工具执行前按停止：工具行显示红色「失败」并携带 `rig: tool call cancelled: the approval gate was interrupted` 英文内部串，且已写进 durable `tool_result` 事件 | `agent.go:1807-1822,2297-2315`；rig `agent/agent.go:562-586,657-670`；`uikit/status.go:160`；复现 `TestReproCancelBeforeApprovalLeavesToolError` | 与 CHANGELOG "已取消不再显示红色失败"的承诺相反，重启后仍复现 |
| FR-11 | `Regenerate` 不重新展开 `@session` 引用，重试请求只剩字面 token，模型看不到被引用会话内容 | `agent.go:1383-1405`（Send 展开） vs `:1421-1510`（Regenerate）；`references.go:35-102`；复现 `TestReproRegenerateDropsSessionReferenceExpansion` | 重试的上下文与原回合不同却无任何提示 |
| FR-12 | `/compact` 只替换 durable history，**不更新进程内读模型**；随后 `ForkSession` / `Regenerate` 从读模型重建历史，把被压缩掉的消息全部"复活" | `compact_now.go:95-104`；`agent.go:1483-1486`（Regenerate）、`998-1015`（Fork）；Lead 探针：`after compact: durable=2, read model=4`；`forked history=4` | 压缩被静默撤销；UI/durable 长期不一致（重启后才看到压缩结果） |

### B. 工具注入 / 审批 / 模式（6）

| ID | 缺陷 | 证据 | 影响 |
|---|---|---|---|
| FR-13 | 桌面模型可见 **70 个工具**，system prompt 只有 **14 条工具指引**；memory/skill/team/team_task/交互工具/schedule/settings/session/editor/worktree/LSP/MCP 全无"何时用"提示（代码注释却声称"每个工具族一行"） | `internal/modules/agent/system_prompt.go:12-14,117-173`；desktop catalog 探针输出 70 tools；`TestReviewToolGuidanceCoverage` → 20 个族缺失 | README 主推的 memory/skill/team 发现性极差；MCP/LSP 动态工具对模型隐形 |
| FR-14 | 工具目录同时存在 hosttools 的 **stub `team`**（内存假 manager，`spawn` 返回成功但不启动任何东西）与真实 `spawn_teammate/team_*/team_task_*`（约 22 个团队相关工具） | `internal/modules/hosttools/tools.go:330-345`；`internal/modules/agent/team.go:399-418`；catalog 探针：`team` + `spawn_teammate` 同时存在；`team spawn` 后真实成员数 = 0 | 模型选名字最直白的 `team`，得到假成功回执后不再跟进；用户侧团队面板永远为空 |
| FR-15 | `tools.disabled` / `mcp.disabledTools` 只过滤 toolkit 激活期构建的列表；hosttools、agent 的 session/subagent/team、memory/automation 等**后注册工具全部无视开关**（UI 显示"已禁用（重启生效）"，重启后仍在模型 schema） | `internal/modules/toolkit/toolkit.go:231-249,338`；`hosttools/module.go:95-103`；`agent/agent.go:161-205`；`internal/ui/plugins/view.go:513`；探针 69 tools 中 `goal/ask_user/team/subagent/session_search` 仍注册 | 安全/降噪开关是假开关，与 COVERAGE「全工具级启停」承诺不符 |
| FR-16 | plan / minimal / creator 模式**完全不告知模型**：请求仍下发全部工具 schema，指引仍教用被拒工具，plan 模式的审批行甚至写 "full — no approval prompts"；deny 只有无理由的 `denied by approval gate` | `system_prompt.go:198-210`（无 mode 参数）；`plan_mode.go:160-176`；`preset.go:142-159`（不按 mode 过滤）；探针 `TestReviewPlanModePromptBlind` / `TestReviewChatModePromptBlind` | 计划模式退化为试错；"先规划后执行"的交互语义无法被模型主动遵守 |
| FR-17 | 会话「Agent 预设」的工具 allowlist 只作用于主 run；同步 subagent/team 成员用 `s.registry.Tools()` 全量（仅排除编排工具），子提示词还宣传被禁工具 | `agent.go:1645` vs `subagent.go:124`、`team.go:91,95`；探针：父 run `[bash]`、子代理 `[bash read_file write_file]` | 用户用预设收窄工具面（只读评审 persona 等）可被子代理绕过 |
| FR-18 | 只读预设把**可写/可清空持久记忆**的 `memory` 工具归入"只读/信息类"自动放行；plan/minimal/creator 也直接放行 | `internal/contracts/subagents.go:88-107,144`；`plan_mode.go:20-36,150-191`；`memorytools/memory.go`（add/update/remove/clear）；UI 侧同样操作却必须 `ConfirmDanger` | 一次误调用或被注入即可无确认清空/污染全部记忆；与 UI 安全默认不一致 |

### C. Prompt / 上下文注入与信任边界（3）

| ID | 缺陷 | 证据 | 影响 |
|---|---|---|---|
| FR-19 | AGENTS.md 读取无围栏：workspace 内指向工作区外的 symlink、或 `agent.agentsFile="../outside.md"` 可把**宿主机文件**注入 system prompt；内容裸拼无信任框，可伪造 `## Tool guidance` 等系统小节并排在用户配置 prompt 之后；32KiB 按字节截断会切断 UTF-8 rune 且 prompt 内无 `[truncated]` 标记 | `agent.go:439-460,447-450`；对比 `attachmentText` 的 UTF-8 安全截断与 `web_*` 的 untrusted 措辞；探针 `TestReviewAgentsSymlinkEscapesWorkspace` / `TestReviewAgentsFileNameTraversal` / `TestReviewAgentsUTF8SplitOnTruncation`（尾部 `e6 b1`，invalid UTF-8） | 打开不受信仓库即可让宿主机任意可读文件（私钥/.env 等）进入模型上下文并发往 provider；system 角色注入比 user/attachment 危险得多 |
| FR-20 | 工作区 `<repo>/skills/**` 自动进入模型指令，`skill load` 原样返回正文，无 untrusted 标记；顶层 `skills/*.md` 也会被当技能；配合 FR-18 可"克隆恶意仓库 → 技能指令 → 清空/外泄记忆" | `internal/modules/skills/skills.go:145-207`；rig `tools/skill/skill.go:177-191`；端到端探针：`IGNORE ALL PREVIOUS INSTRUCTIONS ...` 进入 `skill load` 输出；`skills/*.md discovered as skills: [changelog contributing]` | 供应链/提示注入；技能内容本质是指令，但模型与用户无法区分来源可信度 |
| FR-21 | `@session` 引用块无转义：被引用 transcript 可伪造 `[引用结束]` / `[引用的会话 …]` 标记，提前关闭/冒充引用数据区；且只保留 user/assistant 文本、丢弃工具证据且无提示 | `internal/modules/agent/references.go:105-131`；探针：footer 出现 3 次（1 真 + 2 伪造） | 引用协议可被内容劫持；引用"结论来自工具"的会话时模型看不到证据，易放大错误 |

### D. MCP 运行期（2）

| ID | 缺陷 | 证据 | 影响 |
|---|---|---|---|
| FR-22 | MCP 是"一次性连接"：HTTP session 过期（404/410）或 stdio server 崩溃后**永不重连**，该 server 的工具永久失败；Plugins 页仍显示「已连接」，只有手动刷新才变「连接失败」 | `internal/modules/toolkit/toolkit.go:251-307`；`plugins.go:54-97`；rig `tools/mcp/transport.go:21-23`、`http_transport.go:288-300`；`ErrSessionExpired` 全仓库无处理；探针：两次调用均 `session expired: 404`，initialize 仅 1 次 | 远端 MCP 重启/会话 TTL 到期是常态，之后必须重启应用；健康状态展示不可信 |
| FR-23 | MCP 启动在激活期**串行阻塞**（`context.Background()`，rig 默认 30s/请求），settings 行无法配 `timeoutMs`；一台挂起 server 让桌面启动白屏 30s，多台叠加可达数分钟 | `toolkit.go:251-320`；`mcp_stdio.go:43-56`；`internal/contracts/plugins.go:51-58`；`internal/host/host.go:568`；探针实测 `elapsed=30.03s` | 企业网关/代理黑洞/被防火墙丢包时，用户只能强杀；无进度提示 |

### E. 记忆 / 技能语义（2）

| ID | 缺陷 | 证据 | 影响 |
|---|---|---|---|
| FR-24 | 记忆与技能**从不注入模型上下文**（只有工具定义）：`buildRunSystemPrompt` 不读 memoryStore/KeySkills，system prompt 无 memory/skill 字样；真实 provider 请求体验证 `contains memory text=false` / `skill body=false`。README 只承诺工具/页面（文档诚实），但会话页/`/memory` 文案"后续会话都可以使用"是过度承诺 | `internal/modules/agent/agent.go:1613-1641`；`system_prompt.go:117-173,198-210`；`internal/ui/session/view.go:2156`；`command_dialogs.go:207`；探针 `TestReviewMemoryContentNotInjectedIntoModelContext` | 用户保存记忆后，模型不主动 `memory list` 就永远看不到 → 记忆功能实际不可达；技能也没有目录，模型不知道有哪些 |
| FR-25 | 记忆 `scope` 只是字符串标签：存储无 workspace 身份、List/Search 不按当前工作区过滤 → 工作区 A 的"工作区记忆"在 B 同样可见/可被一键清空；默认 scope 四入口不一致（模块 global / 工具 workspace / 会话 Tab workspace / 设置页·`/memory`·提取 global），`Config.Scope` 对工具路径是死配置 | `internal/modules/memory/memory.go:162-167,252-260,278-294`；rig `tools/memory/memory.go:18-20,194-209`；`ui/session/view.go:2231-2261`；`ui/settings/memory.go:234`；`agent/memory_extract.go:124`；探针：工具 add 存 `Scope:"workspace"`，持久化 JSON 无任何 workspace 标识 | 跨项目上下文/机密泄漏；"工作区记忆"标签无法解释；设置页"全部记忆"会清理其它工作区条目 |

### F. 产品/UX（5）

| ID | 缺陷 | 证据 | 影响 |
|---|---|---|---|
| FR-26 | **幽灵默认模型**：全新数据目录首启 chip 显示 `gpt-4o-mini`，设置→模型却说"还没有模型"；README 声称"模型列表是唯一来源/无内置默认模型" | `agent.go:62-64,683-705`；`assemblies/desktop.json:64`；`ui/session/view.go:3073-3092,3010-3021`；`README.md:158,261` | 用户无法判断是否配置成功；它真的会请求 `api.openai.com`（空 BaseURL） |
| FR-27 | 会话所属工作区只是元数据：打开别组会话仍以"当前工作区"为工具根；自动化任务的"工作区"选择静默无效；ACP/headless 完全没有工作区引导（`session/new` 的 `cwd` 被忽略，全新安装时文件/命令工具全部 `no roots configured`；已有桌面工作区时会静默操作错误项目） | `agent.go:1084-1110`；`toolkit/rebind.go:57-77`；`automation.go:697-707`；`internal/modules/acp/acp.go:331-345`（`session/new` 只 `NewSession("")`，忽略 params；ACP 服务只依赖 ChatService，无法设置 workspace）；`docs/COVERAGE.md:52` 称已应用 | 可能在错误项目里读/写/执行命令；定时任务跑错仓库；ACP 编辑器集成无法真正改文件 |
| FR-28 | "目标"是宿主**全局内存单例**：跨会话显示，自动续跑会把 A 的目标发进当前会话 B，重启即丢（契约/文案却称"当前会话的长期目标"） | `internal/uiroot/goal.go:28-45`；`ui/session/view.go:585-620`；`ui/session/session.go:1287-1330`；`contracts/goal.go:9`；`command_dialogs.go:89`；无持久化键 | 目标串会话、推进错会话、重启丢失 |
| FR-29 | 审批卡没有会话归属：后台会话/自动化的审批会以全局队列最旧一条渲染在当前会话；卡片无来源信息、无跳转 | `contracts/store.go:91-102`（请求无 SessionID）；`ui/approval/approval.go:192-220`；`agent/plan_mode.go:216-221` | 用户为看不到的对话放行/拒绝工具调用 |
| FR-30 | 落地页 hero「新对话」重复创建空会话，且该会话标题非空 → auto-title 永不触发（是**唯一**传非空标题的新建入口）；多点几次堆出多条「新对话」 | `ui/session/view.go:1007-1010,4159-4160`；`agent.go:1325-1331`；其它入口均传空标题；`docs/COVERAGE.md:65` 称 auto-title 按首条提示生成 | 侧栏堆冗余空会话且永不命名，用户找不到自动标题 |
| FR-31 | 无模型时发送按钮仍可用，失败后只显示英文后端错误、无"去设置"动作；中文界面普遍混入 `agent: no model configured…`、`sandbox: denied: no roots configured`、`web search endpoint 未配置（websearch.endpoint）` 等内部串 | `ui/session/view.go:2429,3473-3478`；`agent.go:1265`；`toolkit.go:173`；`docs/AGENT-SURFACES.md:356`（承诺"发送禁用+明确原因/跳转"）与 `:535-539`（ErrorPanel 未建） | 用户必须读懂 Go 错误才能自救；与规范承诺不符 |

> 注：FR-31 同时含 PM-02（发送不禁用）与 PM-08（错误本地化/恢复动作）两条，合并为一条便于排期。

---

## 4. P3 · 打磨（24，摘要）

**Loop/流式**：`finish()` 先清 `running` 再持久化中断回合，窗口内新 run 会被旧 finish 重复写入半截答案并注入假"应用崩溃"标记；`finish_reason=length` 截断与正常完成不可区分（`FinishReason` 全仓库无消费者）；迭代上限触顶直接展示 `rig: max iterations reached…` 英文内部串。

**MCP/工具**：MCP 工具结果不受 `maxOutputBytes` 约束（实测 200KB vs 1024）；Plugins 工具计数在禁用过滤前统计、`tools/list` 失败仍显示"已连接 0 工具"、Refresh 不更新 registry；workspace `mcp.json` 是全局 opt-in（开启后任何仓库的 mcp.json 都可 spawn 子进程，无按仓库确认）；LSP 不随工作区重绑（`RootURI`/配置只在激活时读一次）；`subagent` 不继承会话预设工具白名单（与 FR-17 同根，单列为 P3）。

**注入**：headless `never`/`ask` 审批策略在 prompt 中被压成 read-only 文案（实际立即 deny/挂起）；`@session` 引用丢弃工具证据且标记可伪造（已列 FR-21）；skill 来源/信任级别不标注（与 FR-20 同根）；AGENTS.md 截断无 prompt 内标记、可切 UTF-8 rune（已列 FR-19）。

**记忆/技能**：`add` 接受任意 scope 而 `update` 只接受两种 → 设置页编辑必失败；设置页"新记忆会出现在列表顶部"实际在底部；提取部分失败已写入却只报"提取失败"且列表不刷新；提取 scope 硬编码 global、secrets/长度仅靠 prompt 约束；memory 工具描述仍称"默认实现是进程内"；模型可无审批清空记忆（与 FR-18 同根）；无跨进程锁/无目录 fsync/裁剪仅手动（`maxEntries/maxAgeDays` 不自动执行）；会话记忆 Tab 可见时外部新增不刷新；技能根 workspace 优先级最低且静默遮蔽项目同名技能；`$ZEOKEEL_SKILLS`/rig 默认技能根被忽略且无用户配置入口；编辑 dataDir 技能会丢弃其它 frontmatter 键（`allowed-tools` 等）；技能 List 每次全量读取全文（≤1MB/个，无数量上限）。

**产品/UX**：记忆管理两套术语（会话「整理记忆」vs 设置「去重并合并」）；后台会话/自动化完成或失败无通知（托盘态完全无感）；MCP 配置在设置、状态/工具数/重试在插件页且无互链；审批等待时同屏"Esc 拒绝"与"Esc 停止"两个承诺；归档页打开会话后仍是归档态、侧栏不可见；「会话设置」菜单混合全局（权限/模式）与会话级（人设/本会话允许）作用域；搜索设置"留空则禁用 web_search"不实（工具始终注册并返回内部键错误）；默认 `contextBudget=0` 使 `/compact` 返回"压缩不可用"，而上下文超限提示却叫用户"压缩或新建会话"。

---

## 5. 文档承诺 vs 实际（D1–D10）

| # | 文档承诺 | 实际 |
|---|---|---|
| D1 | `docs/AGENT-SURFACES.md:356`（S7）：无模型/API key 时「发送禁用 + 明确原因/跳转」 | 发送不禁用，失败显示英文后端错误、无跳转 |
| D2 | README 多处把 Home 写成现役页面（真实 composer/hero/setup/侧栏导航） | Home 自 v0.2.6 起已不是导航目的地，setup/next-steps 为不可达代码 |
| D3 | `README.md:158,261`：模型列表是唯一来源、无内置默认模型 | `DefaultModel="gpt-4o-mini"` + 桌面装配显式默认模型 → 幽灵模型 |
| D4 | `docs/COVERAGE.md:65`：auto-title 按首条提示生成 | hero 新建的会话标题是"新对话"（非空），永不自动命名 |
| D5 | `docs/COVERAGE.md:52`：自动化 workspace 字段经 `SessionWorkspaceSwitcher` 应用 | permission 生效；workspace 只写元数据，工具根仍是运行时全局当前工作区 |
| D6 | `docs/UI-AUDIT-2026-11-11.md`：记忆术语已统一 | 设置页"去重并合并" vs 会话页"整理记忆"仍两套 |
| D7 | `README.md:265-266` + 搜索设置：留空禁用 web_search | searcher 始终注册，未配置只在调用时报内部键错误 |
| D8 | `contracts/goal.go:9` + `/goal` 文案：当前会话的长期目标 | 宿主全局内存单例，跨会话、自动续跑串会话、重启丢失 |
| D9 | `docs/AGENT-SURFACES.md:535-539`：错误必须给下一步、`uikit.ErrorPanel`；toast 不替代可操作错误 | `ErrorPanel` 未建；错误行只有文本，无恢复动作 |
| D10 | `docs/USABILITY-REVIEW.md` 把 Home setup/最近会话/可切模型等判为已修 | 这些 Home 行为在产品里不可达（"修在了到不了的地方"） |

---

## 6. 建议修复顺序（按风险/投入产出）

**Wave 0（本周，阻断）**：FR-01（MCP 重名 panic）→ FR-02（凭据自动放行）→ FR-03（只读 override 落穿）→ FR-06（损坏数据文件隔离 + 启动降级）。

**Wave 1（安全/核心语义）**：FR-04/FR-05（LSP 接线 + PathPolicy）→ FR-15（工具开关生效）→ FR-13/FR-14（工具目录收敛：删 stub team、精简 legacy 同义工具、补指引）→ FR-16（mode 进入 prompt 与工具面）→ FR-17（子代理继承预设白名单）。

**Wave 2（loop/上下文正确性）**：FR-08/FR-12（压缩串行化 + 读模型同步）→ FR-09（删除墓碑/join）→ FR-10（取消文案归入 cancelled）→ FR-11（Regenerate 重放引用）→ FR-19/FR-20/FR-21（信任边界：AGENTS/skills/引用转义）→ FR-22/FR-23（MCP 生命周期 + 并发启动/超时）。

**Wave 3（产品门面）**：FR-07（首启检查清单）→ FR-26（幽灵模型）→ FR-27（工作区真实生效 + ACP `cwd`）→ FR-28（目标会话级 + 持久化）→ FR-29（审批归属）→ FR-30/FR-31（落地页 CTA、错误本地化 + 恢复动作）→ FR-24/FR-25（记忆注入决策与 scope 语义）→ 剩余 P3 与文档同步（D1–D10）。

---

## 7. 已核查、**不是**缺陷（避免误伤/重复修）

- **Cancel 能停 provider 流与在飞行工具**；approvals 的 `Request` 用 `ctx.Done()` 清理 pending，不会悬挂。
- **usage/context token 记账**、**spill**（阈值/回退/prune）、**loop guard**（[3,5,8]、todo 排除、参数规范化）、**restore/hydration** 语义均与测试一致。
- **审批安全默认**（无默认动作、Esc=拒绝且窗口级兜底）、**危险确认**（取消 Default、危险非 Default）、**设置"重启生效"标注**（MCP/热键/托盘）均真实成立。
- **预设组合顺序**（identity/env → approval → base → AGENTS.md → persona → preset → tool guidance）无重复；`@session` 的预算/尾部 20 条/持久化原始 prompt 正确；附件文本截断 UTF-8 安全且带 `[truncated]`（可作 AGENTS.md 修复的对照）。
- **子代理无重复 system prompt、无陈旧编排指引**（rig 排除集与产品排除集一致）。
- **MCP 合并/去重/坏配置跳过**、普通 MCP 工具的 `mcp__<server>__<tool>` 命名空间、未知工具错误路径、plan/chat 优先级、AUDIT-P3-13 的诊断脱敏均正确。
- **记忆/技能核心边界**：原子写、进程内并发、提取单飞/去重、技能编辑的路径穿越/symlink 拒绝、O_EXCL 创建、realpath 二次校验均成立。
- `ui-drive` 91/91、`ui-snapshot` 102 帧、`designlint` 0 new/0 blocking、`design-check` 6/6、全量 `go test ./...` 均通过。

---

## 8. 复现索引与局限

- 每条 P0/P1/P2 的**完整复现命令与原始输出**见 §8.1 对应角色报告；所有探针通过 `go test -overlay=<json>` 从 `.cache/` 注入测试包，**未改动仓库源码/测试/文档**，`git status --porcelain` 为空。
- 一次性复跑全部探针（示例）：
  ```bash
  export PATH=/usr/local/go/bin:$PATH GOPATH=/data/mygo/.cache/gopath GOCACHE=/data/mygo/.cache/go-build \
         GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off TMPDIR=/tmp/zeokeel-tmp GOTMPDIR=/tmp/zeokeel-tmp
  go test -overlay=/data/mygo/.cache/review/repro/overlay.json ./internal/modules/toolkit -run TestReproTwoStdioMCPServers -v
  go test -overlay=/data/mygo/.cache/review/probes/agent_overlay.json ./internal/modules/agent -run TestReview -v
  go test -overlay=/data/mygo/.cache/review/work/overlay.json ./internal/regression -run TestReview -v
  go test -overlay=/data/mygo/.cache/review/lead/overlay.json ./internal/assembly -run TestLead -v
  go test -overlay=/data/mygo/.cache/review/lead/overlay-agent.json ./internal/modules/agent -run TestLead -v
  ```
- **局限**：桌面/ACP 的首启与真实网络（模型/MCP/搜索/更新）未在干净数据目录的图形会话中端到端操作；FR-07/FR-26/FR-27 的"用户感知"由导航/装配/代码路径推定（PM 报告 §6 已注明）；MCP 真实 Streamable HTTP 成功握手仍属集成测试空白；Windows/macOS 真机、IME/屏读器、多显示器 DPI 未覆盖。
