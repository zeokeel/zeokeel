# 覆盖深度限制工作清单（Round 1 只读分析）

> 基线：HEAD `e1f1b85` + 工作树未提交 WIP。13 项 🟡 = §5.1 深度限制（矩阵 48✅/13🟡/1❌）。
> 范围：只读核对 `internal/**` 与 `old/**`；本文件为唯一产出（未改代码、未 commit）。
> 注意：其他 agent 正在改 automation/LSP/ACP（工作树已出现 `contracts/automation.go`、`contracts/lsp.go`、
> `internal/modules/acp/tool_updates.go` 等 WIP），本轮方案**不与其抢同一文件**，先验证后收口。

## 0. 结论摘要

- 13 项中：**3 项基本已完成，验证 WIP 后可直接改 ✅**（automation / LSP 多服务器 / ACP tool_call）；
  **1 项实测已超旧实现，建议直接 ✅**（skills）；其余 9 项为真实深度缺口。
- **1 项建议明确非目标**：skills 的 workspace 编辑（旧实现只读、无任何编辑 API；新增写权限风险大于收益）。
- 工作包 5 个（P0 验证收口 + P1~P4 精修），每包 ≤2 agent、范围不重叠；另有 2 个高风险项单列排期。
  顺序：P0(低风险/解锁 ✅) → P1(每日可见) → P2 → P3 → P4(高投入) → 高风险追加。

## 1. 逐项对照（现状 / old 行为 / 缺口 / 方案 / 验证）

### 1.1 automation：任务字段与 runs 语义（🟡→WIP 已完成）
- 现状：cron/every/after、运行历史（内嵌 10 条）、任务模型覆盖、搜索/启停筛选已落地；WIP 已加
  `AutomationTask.WorkspaceID/Permission`（`internal/contracts/automation.go:128-129`）并在执行时通过
  `SetSessionWorkspace`/`SetSessionPermission` 应用（`internal/modules/automation/automation.go:682-696`）。
- old：任务表含 `workspace_id/permission/model/origin_session_id`，独立 `automation_runs` 表含
  running/completed/failed/stopped 与 session_id。
- 缺口：WIP 落地后仅剩 runs 形态差异（内嵌 history vs 独立表、stopped 状态未记、session_id 仅摘要文本）。
- 方案：验证 WIP（任务字段往返+执行应用+旧 JSON 兼容）；runs 表差异写 §5.2“内部存储形态”。
- 验证：`internal/modules/automation` 测试 + regression `TestAutomation*` + 手工旧 JSON 载入。

### 1.2 lsp：多服务器与 workspace 配置（🟡→多服务器 WIP 已完成）
- 现状：WIP 已实现 `lsp.servers` 列表 + `lsp.active` 选择 + 旧 `lsp.server` 回退
  （`internal/contracts/lsp.go:144-178`、`internal/modules/toolkit/lsp.go:63-75`），设置页可增删/选择/激活
  （`internal/ui/settings/lsp_settings.go`）；只启动 active 一个服务器。
- old：`lsp.servers` 对象、多语言服务器；`lsp.workspace_config=allow` 显式 opt-in 后读
  `<root>/.zeokeel/lsp.json`（未 opt-in 一律忽略，settings 优先）；安全理由：仓库声明会执行第三方命令。
- 缺口：**workspace 配置 opt-in 未迁移**；old 可能同时启动多个服务器（当前 active 单启动需确认是否等价）。
- 方案：新增 `lsp.workspace_config`（仅 "allow" 开启，默认 off）+ 读 workspace 文件并深合并（settings 覆盖），
  文件 `contracts/lsp.go`（新增 key/parse）+ `toolkit/lsp.go` + `settings/lsp_settings.go`（toggle）。
- 风险：执行仓库声明的命令；必须默认关、显式 opt-in、解析失败仅警告、UI 明示风险。
- 验证：lsp parse/list 测试 + toolkit 激活（坏 JSON/未 opt-in 不读）+ settings 页 toggle。

### 1.3 memory：模型驱动 consolidate/extract（🟡，高风险）
- 现状：JSON store + tool + session Memory tab + 设置页 添加/搜索/删除/整理/清空；无 caps/retention，
  无后台提取。
- old：`old/internal/memory/consolidate.go` 确定性 ConsolidationPlan（caps/retention，删过期/超限）+
  `extract_prompt.go` 的 ExtractionPrompt（后台调用模型从最近对话抽取记忆）。
- 缺口：两层都缺；② 需要 provider 调用（成本/隐私/质量/去重策略）。
- 方案：A. 确定性 consolidation 纯函数 + store 支持（安全，可先做）：`internal/modules/memory/memory.go`
  增 `Consolidate(caps, now)`，settings 记忆页加“整理/上限/保留天数”；B. 模型驱动 extraction 单列高风险：
  后台任务需 provider、频率上限、opt-in、失败静默、与 consolidation 去重；`contracts/memory.go` 扩展。
- 验证：A 用纯函数/存储测试；B 需 fake provider + 开关测试，禁止默认开。

### 1.4 presets：旧 agent presets 语义（🟡）
- 现状：prompt 片段 CRUD + composer 插入；无 per-session preset、无 persona/systemPrompt/model/tools allowlist。
- old：`old/internal/presets/presets.go:136-156` AgentPreset{ID,Name,Tools,Prompt,Persona,Model,Order}，
  session 存 preset ID；工具 allowlist 过滤注册顺序，Prompt/Persona 追加系统提示，Model 作为默认模型。
- 缺口：preset 四字段语义 + per-session 选择 + 工具过滤 + 用户/内置 JSON 导入。
- 方案：`contracts/presets.go` 扩 AgentPreset（保留 PromptPreset 兼容）；settings 预设页双区（片段/代理）；
  agent 每 run 应用 persona/prompt/model + 过滤 `spec.Tools`；composer 选择器写 session preset。
- 风险：与 chat modes 工具策略叠加顺序需定义（建议：preset allowlist ∩ mode 策略）；旧 JSON 迁移。
- 验证：presets 解析/CRUD、agent 请求 system/tools/model 断言、composer 选择与历史兼容。

### 1.5 session：tool spill（🟡，高风险）
- 现状：无 spill；大工具结果原样持久化并进入后续 provider 历史。
- old：`old/internal/session/tool_spill.go`：>32KiB 的 tool 结果写入 `<workspace>/.zeokeel/tool-results/`，
  持久化替换为“头部 2KiB + 文件路径”，保留 100 个；当前 turn 模型仍看到全文，仅改持久化历史。
- 缺口：完全未迁移（成本/上下文风险真实存在）。
- 方案：agent 侧包装 `rig.History`：Append 时对 tool result 文本超限者落盘+改写（需 workspace root 解析、
  原子写、prune、失败回退全文）；`internal/modules/agent/spill.go` + `contracts/chat.go` 无需改。
- 风险：改写必须保持 tool_call/tool_result 配对合法；workspace 不可写时降级；避免与 compaction 冲突。
- 验证：fake workspace/超限/失败/重放测试 + race；provider 请求仍含完整结果、持久化含摘要。

### 1.6 sessionreference：候选排序/URI/预算错误（🟡）
- 现状：@session 解析（分隔符/尾部裁剪/self/unknown/重复忽略）、注入尾部 20 条/≤8000 rune、事件。
- old：`old/internal/sessionreference/candidates.go` Candidates(cwd,query,limit) 排序、`uri.go`
  EncodeURI/DecodeURI（sessionref 语义）、`context.go` 预算错误分类。
- 缺口：候选排序、URI 编解码、预算错误类型/文案。
- 方案：新 `contracts/sessionreference.go`（Candidate/URI/错误 kind，纯函数）+ `agent/references.go` 使用 +
  composer @session 菜单按候选排序/显示已用预算；UI 错误行本地化。
- 风险：低（纯逻辑），注意与现有 token 格式兼容（新 URI 可选）。
- 验证：parse/URI/排序/预算表驱动 + composer 菜单帧 + agent 注入不变。

### 1.7 i18n：后端错误文案（🟡）
- 现状：17 页 en/zh + 命令动态重注册；会话错误直接显示 provider/后端原文（`agent.go finish`）。
- old：`old/internal/i18n/catalog.go:249+` 结构化 `session.error.{auth,rate_limit,network}_title/_cause`
  并附原始 detail；即错误分类+本地化标题/原因，而非原文直出。
- 缺口：错误分类（auth/rate_limit/network/timeout/context）与本地化映射；host chrome tooltip 残留少量。
- 方案：agent 在 finish 前分类（HTTP 状态/错误串）写入 `Message.ErrorKind`/`Session.ErrorKind`
  （contracts/chat.go 新枚举）；session/UI 按 kind 渲染本地化 title/cause，原文折叠/日志；host 残留 tooltip 清扫。
- 风险：分类误判导致误导；保留 raw detail + 日志可诊断。
- 验证：错误分类表（401/429/timeout/cancel/unknown）+ en/zh 帧 + 回归现有错误路径。

### 1.8 Composer：/compact 与 /goal 子命令（🟡）
- 现状：12 命令；权限/模式/推理/@/预设已齐；缺 `/compact`；`/goal` 仅对话框 set/clear，
  生命周期靠 header 暂停/暂停恢复/清除（无 complete/abandon/edit 语义）。
- old：`/compact` 手动压缩（替换历史+报告 replaced/saved，分类错误：运行中/无 compactor/无可压缩/保存失败）；
  `/goal <obj>|edit|pause|resume|complete|abandon|clear|status`。
- 缺口：手动压缩入口与后端；goal 生命周期状态机与命令/菜单。
- 方案：A. `contracts/chat.go` 增可选 `SessionCompactor{CompactNow(ctx,id)(replaced,saved int,err)}` +
  agent 实现（复用 `store.ReplaceHistory` + `compact.LLMCompactor`，拒绝 running/budget=0，稳定错误码）
  + `sessionCommands` `/compact` + 本地化错误；B. `contracts/goal.go` 增可选 `GoalLifecycler{Complete,
  Abandon,Edit}`（状态 completed/abandoned，auto-continue 自动停）+ `/goal` 子命令 + header 菜单项。
- 风险：压缩=一次模型调用（成本/失败），必须 running guard + 可读错误；goal 新状态需 UI 与 auto-continue 同步。
- 验证：agent CompactNow（无 budget/运行中/成功替换/history 断言）、session 命令帧、goal 状态机表 + E2E。

### 1.9 Settings/Plugins：插件参数页（🟡）
- 现状：插件页有 MCP 详情/连接配置/开关/内置模块清单；无 per-plugin 参数编辑。
- old：`old/internal/shell/plugin_details.go` 每插件设置：subagent depth/model、web_search endpoint/max_results、
  agent_loop max_iterations/budget_warn_steps/tokens、shell timeout/output_cap、plugin.<id>.enabled；
  部分（auto review/budget 实验）old 自身标注 experimental。
- 缺口：per-plugin 参数编辑页；且部分 key 当前模块未消费（写了也无效）。
- 方案：先盘点已消费设置（websearch 已有设置页、agent MaxIterations/工具开关部分），只暴露已接线 key；
  `internal/ui/plugins/settings.go`（新页）+ `contracts/plugins.go` key 常量 + 消费方（agent/toolkit）动态读取；
  未接线的 budget/auto-review 写 §5.2（old 即 experimental）。
- 风险：死配置误导；改 agent 行为需回归工具/循环测试。
- 验证：设置页帧+持久化+消费方生效断言；无接线 key 不展示。

### 1.10 skills：workspace 编辑（🟡→建议 ✅ + 非目标）
- 现状：多根 List/Read + dataDir 内 Create/Update/Delete（原子写、名称不可改、realpath/symlink 校验）
  （`internal/modules/skills/edit.go:32/95/187`、`contracts/skills.go:40-49`、设置页 `skills_edit.go:67,215`）。
- old：`old/internal/skills/skills.go` 只有 Load/Read/Roots/Tool，**没有任何写 API**（只读）。
- 结论：当前已超出旧实现；workspace 编辑不是旧功能，且写用户仓库风险高 → 矩阵 `skills` 改 ✅，
  “workspace 技能编辑”写 §5.2 非目标（新增能力需单独 RFC 与权限模型）。
- 验证：现有 `skills_update_test.go`、设置页帧、回归套件。

### 1.11 ACP：tool_call 流式更新（🟡→WIP 已完成）
- 现状：initialize 含 loadSession、session-new/prompt/cancel/list/load + 流式文本；WIP 新增
  `internal/modules/acp/tool_updates.go`：tool_call（in_progress）/tool_call_update（completed/failed，
  4KiB 截断、空结果省略、历史 seed 不重放）+ 帧测试 `tool_updates_test.go`。
- old：`old/internal/acp/protocol.go` 的 tool_call/tool_call_update 通知、title/kind/content、permission 分类。
- 缺口：WIP 落地后仅 kind 分类（old 有 edit/execute/fetch，当前 read model 无分类→other）；auth/fs/terminal
  属 client 职责（§5.2 已有）。
- 方案：验证 WIP 帧序/截断/交错；kind 分类可选（需 tool→kind 映射表，低风险）；随后 acp 改 ✅。
- 验证：acp 内部帧测试 + regression 真实 JSON-RPC 帧（现有 session/list 风格）。


## 2. 建议直接改 ✅（附代码引用）

| 项 | 依据 | 条件 |
|---|---|---|
| `skills` | `internal/modules/skills/edit.go:32,95,187`；`contracts/skills.go:40-49`；`settings/skills_edit.go:67,215`；old 无写 API | 无需等待；同时把 workspace 编辑写 §5.2 |
| `automation` | WIP `contracts/automation.go:128-129` + `modules/automation/automation.go:682-696`；runs 表为内部形态 | 待 owner 提交 + 回归通过 |
| `acp` | WIP `modules/acp/tool_updates.go` + `tool_updates_test.go`（tool_call/update） | 待 owner 提交 + 帧测试通过；kind 分类可后续 |
| `lsp`（部分） | WIP `contracts/lsp.go:144-178`、`toolkit/lsp.go:63-75`、`settings/lsp_settings.go` 多服务器/active | 建议补 workspace opt-in 后 ✅；否则保留 🟡 |

## 3. 建议写入 §5.2 的明确非目标

- **skills workspace 编辑**：旧实现只读；写用户 workspace 属新能力，需独立权限/RFC（风险高于收益）。
- **automation 独立 runs 表**：内部存储形态差异；当前内嵌 history 已覆盖用户可见的运行历史。
- **插件 agent_loop budget 警告 / Auto review**：old 自身标注 experimental，无稳定契约；不追平。
- 既有非目标沿用：PTC/workflow/ralph/review 工具、插件市场、Windows 替换、artifact 跨重启、DSH 私有导入、
  ACP client 职责（auth/fs/terminal）。

## 4. 隐藏高风险（必须在方案/评审中显式处理）

1. **memory 模型驱动 extract**：后台 provider 调用（成本、密钥、对话隐私）、频率/去重/失败策略；建议 opt-in+上限。
2. **session tool spill**：改写持久化历史必须保持 tool_call/result 配对，否则 provider 400；workspace 不可写要回退。
3. **automation per-task permission**：per-session 覆盖与全局 `agent.permission`/审批并发竞争；需明确优先级与恢复策略。
4. **LSP workspace 配置**：仓库声明=第三方命令执行；必须默认关、显式 opt-in、settings 覆盖 workspace、失败仅告警。
5. **插件参数页死配置**：只暴露已被消费的 key；新接线 agent_loop/shell 参数会改行为，需回归循环/超时。
6. **/compact**：一次 LLM 压缩调用（成本/失败/运行中）；需 running guard、budget=0=不可用、稳定错误码。
7. **i18n 错误分类**：误分类会误导；保留 raw detail + 日志，分类表用真实 provider 错误样本验证。

## 5. 建议工作包（≤2 agent/包，范围不重叠，TDD）

| 包 | 内容 | Agent 拆分（写范围） | 依赖/顺序 |
|---|---|---|---|
| **P0 验证收口**（低风险） | 验证 WIP 的 automation 字段/LSP 多服务器/ACP tool_call/agent 会话 switcher；帧+回归+旧 JSON 兼容；改矩阵 ✅（lsp 视 opt-in 决定） | 1 agent：只读验证 + 由 Lead 改 `docs/COVERAGE.md`；不写 WIP 文件 | 等 owner 提交；最先 |
| **P1 Composer 收口**（高价值） | A：`SessionCompactor` 契约+agent CompactNow+稳定错误；B：`/compact` 命令、`GoalLifecycler`+`/goal` 子命令+header 菜单、本地化 | A：`contracts/chat.go`+`modules/agent/compact.go`(+agent.go 局部)；B：`contracts/goal.go`+`ui/session/session.go|view.go`+`uiroot/goal.go` | P0 后；A/B 可并行（契约文件不同） |
| **P2 语义细节** | A：i18n 错误分类（contracts/chat.go ErrorKind+agent finish+session 渲染）；B：sessionreference URI/候选/预算错误（新 `contracts/sessionreference.go`+`agent/references.go`+composer 菜单） | A：`contracts/chat.go`+`modules/agent/agent.go`；B：`contracts/sessionreference.go`+`modules/agent/references.go`+`ui/session/view.go` 菜单部分 | P1 后；A/B 文件尽量不重叠（agent.go 冲突则串行） |
| **P3 管理深度** | A：插件参数页（只接已消费 key：subagent depth/model、shell timeout/output cap、agent max iterations；新增 `ui/plugins/settings.go`+consumers）；B：LSP workspace opt-in（`contracts/lsp.go`+`toolkit/lsp.go`+`settings/lsp_settings.go`） | A：`ui/plugins/**`+`contracts/plugins.go`+消费方局部；B：`contracts/lsp.go`+`toolkit/lsp.go`+`ui/settings/lsp_settings.go` | P0 后 P3-B 可并行；P3-A 与 P2 有 agent 消费方冲突→排 P2 后 |
| **P4 高投入语义** | A：memory 确定性 consolidation（纯函数+caps/retention+设置页，无 provider）；B：presets 旧语义（AgentPreset+per-session 选择+工具过滤+JSON 兼容；与 chat modes 叠加规则先定） | A：`modules/memory/**`+`settings/memory.go`；B：`contracts/presets.go`+`ui/settings/presets.go`+`ui/session` presets 菜单+agent run 应用 | 最后；B 风险最高，先写设计 1 页再 TDD |

**不在工作包内（单列高风险）**：memory 模型驱动 extract（需 provider/隐私设计）、session tool spill（持久化历史
重写，需独立 RFC；若排期建议在 P2 后单 agent，与 P3-A 不并行）。

## 6. 验收与命令

- 每包：模块测试 + `go test ./internal/regression -count=1 -race` 绿且 0 SKIP；UI 变更加帧测试/E2E。
- 契约变更：`contracts` 只增可选接口（不改既有实现者），否则同步 headless/desktop 装配与 fake。
- 完成后：§5.1 行删除/缩短，矩阵按第 2 节改 ✅；新增非目标写 §5.2；round 结束时跑 `go build ./...` 与全仓测试。
- 当前基线证据：`go test ./internal/regression -count=1 -race` = 263 PASS / 0 SKIP（第十四轮）。
