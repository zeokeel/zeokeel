# 03 · ZeoKeel 现状基线：自研「dsh-context 等价能力」的能力盘点与改造点

> 日期：2026-11-14 · 任务：task-3（调研 only，未改代码）
> 对象：`/data/mygo`（ZeoKeel，rig v0.16.2 + mygo v0.9.0 + zeokeel-kernel 编译期模块）
> 参照：`docs/research/kernel-split/02-dsh-anatomy.md`（DSH 装配解剖）、`docs/research/rig-parity/*`（rig 能力边界）
> 证据等级：文件:行号均来自本仓库或 `zeokeel-rig@v0.16.2` 模块缓存（`/data/mygo/.cache/gopath/pkg/mod/github.com/zeokeel/zeokeel-rig@v0.16.2`）。行号以当前工作区为准。

---

## 0. 结论速览（给 Lead 的 6 条）

1. **ZeoKeel 已有「用量条」但没有「上下文账本」**：会话底部状态行已经显示轮/步/tok/s/上下文/窗口/缓存命中/剩余百分比（`internal/ui/session/view.go:3713-3800`），数据来自 `MessageUsage`（`internal/contracts/chat.go:270-285`）；但它是**每回合一条累计数**，没有每 step/每次模型调用的明细，也没有请求组成快照。
2. **数据源最关键的缺口是「逐模型调用」与「请求快照」**：rig 每次模型调用都会发 `EventUsage`（含 `Iteration`，`rig/agent/agent.go:382-402`），产品只取最后一次的 prompt tokens 存进内存（`internal/modules/agent/agent.go:2348-2350`），回合结束才写**一条**聚合 `usage` 事件（`internal/modules/agent/usage.go:57-65`）。`run_start` 只记录 model（`agent.go:2255`）——`request header / system prompt 分段 / tool schema 列表`全部缺失。
3. **系统提示词是字符串拼接，没有结构化来源**：`systemPromptForRun` 把环境、审批策略、模式、配置提示、AGENTS.md、preset、memory、skill catalog、逐工具 guidance 拼成一段（`internal/modules/agent/system_prompt.go:286-305,374-395`；AGENTS.md 注入在 `agent.go:677-717`、preset 在 `preset.go:109`），且明确「不持久化」（`system_prompt.go:16-18`）。要做注入来源/分段 token 归属，必须先把组装改成 section 列表并落快照。
4. **压缩/清理事件基本不可见**：手动 `/compact` 只返回 `CompactResult`，不写会话事件（`internal/modules/agent/compact_now.go:31-131`、`internal/contracts/chat.go:195-208`）；自动压缩由 rig 在 `prepareContext` 内完成且**不发事件**（`rig/agent/agent.go:489-506`）；spill 由 rig 在记录 tool 消息前完成（`rig/agent/spill.go:21-64`），但结果事件里带稳定前缀与 bytes，产品侧**可识别**（`rig/context.go:95-101`）。
5. **UI 落位不需要改壳层**：会话已有 Chat/Trajectory/Memory 三个 tab（`view.go:33-35,855-887`），右栏已有「固定页 + 文件 tab」模型（`internal/ui/rightpanel/rightpanel.go:50-63`），侧栏会自动列出任何新注册 screen（`internal/ui/sidebar/sidebar.go:416,1589-1604`）。洞察页=新 screen 模块；Context 标签页=session 新增第 4 个 tab；右栏面板=rightpanel 新增固定页；`/context`=session 命令表新增一条；设置卡片=独立设置页/新模块。**核心成本在契约 + 采集 + 聚合，不在布局。**
6. **最大风险不是 UI 而是数据真实性与性能**：SQLite 单连接（`internal/modules/sessionstore/store.go:177-179`）+ events 表只有 `(session_id,seq)` 索引（`store.go:148`），跨会话聚合若每帧扫表会卡帧；自动压缩在 rig 内不可观测，只能做启发式（contextTokens 掉落）或改 rig。建议 P0 只做「会话内 + 运行快照 + 逐调用」，跨会话聚合用**投影表/离线聚合**（P1），不承诺 DSH 的自动压缩事件原样等价。

---

## 1. 功能 × 现状 × 缺口矩阵

> 「dsh-context 等价能力」按用户描述拆 11 项；「现状」= 本仓库已有什么；「缺口」= 要做等价 UI 还缺什么（★ = P0 阻塞）。

| # | 能力 | ZeoKeel 现状（证据） | 缺口 |
|---|---|---|---|
| 1 | 上下文组成（总量/窗口/剩余） | ✅ 每回合最后一调用 `ContextTokens` + 模型 `ContextWindow` 计算「x/y tok、剩余 %」（`view.go:3638-3692,3736-3745`；`contracts/modelcatalog.go:10-19`）；窗口未知时整项省略（合理） | ★ 组成只到「总量」；无 system prompt / tool schema / history / memory / skills 的分项与占比；重启后无 usage 读模型（`MessageUsage` 只在内存 + 一条事件，`usage.go:3-12`），状态行会消失（`view.go:3656-3657` 依赖 `ChatMessage.Usage`） |
| 2 | 每 step 请求快照（request header/sections/tools） | ❌ `run_start` 只有 `{model}`（`agent.go:2255`）；rig `Request` 只有 Messages/Tools/采样参数，不回传调用方（`rig/provider.go:7-17`） | ★ 需要在产品侧运行前快照：system prompt 各 section 的 bytes/tokens/hash、tool 名单与 schema token、history 消息分类计数、memory/skill 条目数、workspace/审批/模式等注入来源 |
| 3 | 系统提示词分段与 token 归属 | 🟡 分段逻辑已显式存在（身份/环境 `system_prompt.go:88-92`；审批 `133-142`；模式 `modePolicyLine`；配置提示 `298-300`；AGENTS.md `agent.go:677-717`；preset `preset.go:109`；memory `440-470`；skills `475-507`；工具 guidance `278-284`）但全部拼成字符串 | ★ 把 `systemPromptForRun` 重构为 `[]PromptSection{ID,Source,Text}`（或同时返回 section 列表），对每段 `rig.EstimateTextTokens`（`rig/context.go:49-73`）估算；快照落事件，UI 才能画「谁占了多少」 |
| 4 | tool schema 的 token 归属 | 🟡 rig 用 `ToolSpec{Name,Description,Parameters}` 生成请求（`rig/message.go:95-100`；`rig/tools/registry.go:75-88`）；运行面的工具名集合由 `toolNameSet` 精确得到（`system_prompt.go:310-329`），`runTools` 在 `agent.go:2222` 构造 | ★ 未记录「本次运行实际暴露的工具列表 + 每个 schema 的 token」；可在 run 前用 registry `Specs()` + JSON 序列化估算，但 preset/mode/disabled 后的真实子集需在 `run()` 内取（`runTools` → 名字集合已有） |
| 5 | 注入来源标记（环境/审批/AGENTS.md/preset/memory/skills） | 🟡 来源在代码注释与函数边界里清楚（见 #3），UI 无任何展示；诊断页只列静态配置（`internal/ui/settings/settings.go:541-584,977`） | ★ 快照里为每个 section 打 `Source`；若要「注入事件」时间线（P2），还需记录每次运行的 section hash 并与上次 diff |
| 6 | 压缩/清理事件 | 🟡 手动 `/compact` 有完整结果（`compact_command.go`；`contracts/chat.go:195-208`）；自动压缩在 rig 内（`agent.go:2247-2253` 配置 `LLMCompactor`；`rig/agent/agent.go:489-506`） | ★ 手动压缩未写会话事件（`compact_now.go` 全程无 `appendEvent`）；自动压缩无事件、rig 不通知；spill 可在产品侧识别（`rig.IsSpilledToolResult`，`rig/context.go:95-101`，摘要含 `path/bytes/preview`，`rig/agent/spill.go:63`）但产品未记录 |
| 7 | 会话内趋势（按轮/step 的 token 曲线） | 🟡 回合级累计在 `MessageUsage`；每回合一条 `usage` 事件（`usage.go:57-65`）；事件有时间戳（`sessionstore/store.go:140-146`） | ★ 无「逐模型调用」事件：`EventUsage.Iteration`/prompt/completion/cached 被丢弃（仅取 lastPromptTokens，`agent.go:2348-2350`）；无调用耗时（rig 不发；产品只能在 `tool_call`→`tool_result` 之间量工具批次墙钟，`agent.go:2372-2437`） |
| 8 | 上下文浏览器（消息/工具结果/文件/记忆/技能） | 🟡 transcript 读模型完整（`contracts/chat.go:52-93`）；tool_call/tool_result 事件带 `text_offset/path/status/content`（`agent.go:2393-2400,2432-2435`；`tool_timeline.go:28-47`）；memory/skills 有服务（`system_prompt.go:380-385`） | ★ 没有「统一上下文条目索引 + 每条 token 估算 + 跳转」；items 的 token 估算可现算但需后端统一提供（UI 不可 import rig，`ARCHITECTURE.md:49-51`） |
| 9 | 跨会话聚合 / 洞察页 | ❌ 仅有单会话事件查询（`store.Events/Last`，`store.go:729-805`）与全局文本搜索（`store.Search`，`store.go:758-783`）；会话列表只有 `MessageCount`（`contracts/store.go:16-25`） | ★ 无 token/成本/模型/工作区维度的聚合；events 表无 `type/created_at` 索引（`store.go:116-148`），跨会话扫描成本高；需要新契约 + 后端聚合模块 + 投影/缓存 |
| 10 | 成本（单价×token） | ❌ `ModelInfo` 只有 `ContextWindow/BaseURL/APIKey/Family/Thinking/Temperature/TopP/MaxTokens`，无价格字段（`contracts/modelcatalog.go:10-45`）；全仓库无 price/cost（grep 无业务实现） | ★ 需要价格元数据（模型级输入/缓存/输出单价）+ 计算；价格编辑属设置面改动（`internal/ui/settings/modelcatalog*.go`） |
| 11 | File Activity / 命令执行 / Fleet | 🟡 文件：`tool_call` 事件对 8 个 path-bearing 工具记录 `path`（`agent.go:2686-2699`），但只覆盖 write/edit/str_replace/read/read_image/list_dir/glob/grep；workspace differ 有 git 变更（`internal/ui/rightpanel/changes.go`）。命令：bash/pwsh 的 `tool_call.arguments` 有界截断（`agent.go:2379,2672`），`tool_result` 无退出码/耗时。Fleet：团队/子代理/后台任务读模型已存在（`contracts/agentsurface.go:53-77,123-131`），团队成员有 `UsageTokens`（`team.go:419`、`childagents.go:374`），但同步子代理结果把 `Usage` 丢弃（`internal/modules/agent/subagent.go:189-199`） | File Activity：可基于事件做「写入/读取/搜索」聚合，但读事件不记录「读取了多少字节/行」；命令执行：需扩事件（命令/退出码/耗时），退出码当前只能从结果文本启发式解析（工具 guidance 提到 `[exit code: N]`，`system_prompt.go:151`）；Fleet：同步 subagent 的 token 需要把 `result.Usage` 补进 `contracts.SubagentResult/SubagentActivity` |

---

## 2. 现有数据面清单（可直接用 / 需新增）

### 2.1 已存在、可直接消费

| 数据 | 位置 | 形态 | 备注 |
|---|---|---|---|
| 会话/消息读模型 | `contracts.ChatService`（`chat.go:441-465`）；`ChatMessage/ToolCall`（`chat.go:52-93`；`contracts/contracts.go:63-82`） | 内存 read model，`Subscribe` 通知 | UI 唯一正道（`docs/ARCHITECTURE.md:43-51`、`INJECTION-POINTS.md:109-118`） |
| 每回合用量 | `MessageUsage`（`chat.go:270-285`）；写入 `usage.go:35-65` | 内存 + 一条 durable `usage` 事件 | 重启丢失内存值；事件保留 |
| 窗口大小 | `modelcatalog.ModelInfo.ContextWindow`（`modelcatalog.go:10-19`）；读取 `view.go:3668-3692` | 设置 JSON | 0 表示未知，UI 省略 |
| 会话事件日志 | `store.Events/Last/Search`（`store.go:729-805`）；事件类型见 `agent.go:1486,1875,1893,2042,2255,2400,2432,2564` + `usage.go:57` | SQLite `events(seq,session_id,type,payload,text,created_at)`（`store.go:140-148`） | `text` 列已做 payload 搜索文本（`AppendEvent`，`store.go:716-719`） |
| 工具时间线 | `tool_timeline.go:28-47,79`；`hydrateToolTimeline` | 事件回放 | 证明「事件→读模型 hydrate」模式可复用 |
| checkpoint | `contracts.Store.CheckpointStore`（`contracts/store.go:54-56`）；rig `Checkpoint`（`rig/checkpoint.go:69-89`） | SQLite | 与 Context 页无直接关系，但可作为「中断回合」标注来源 |
| memory/skills 目录 | `system_prompt.go:380-385,440-507`；`contracts/memory.go`、`skills.go` | 服务读取，有界 | 可做浏览器数据源 |
| 团队/子代理/任务/Jobs | `contracts/team.go`、`agentsurface.go:53-131`、`tasks.go` | 只读服务 | Fleet 页可直接复用；团队有 `UsageTokens/Iterations` |
| 会话 tab 与命令面 | `view.go:33-35,855-887`；`session.go:727-900`；`contracts.CommandSpec`（`contracts.go:95-106`） | UI 模块内状态 + runtime 命令注册 | 新增 tab/命令成本低 |

### 2.2 缺失（按 dsh-context 需求）

| 缺失项 | 影响 | 建议补齐位置 |
|---|---|---|
| ★ 逐模型调用 usage（iteration/model/prompt/completion/cached/contextTokens/时间） | 趋势、每 step 组成、缓存曲线 | `agent.go:2342-2350`（handleEvent 收到 `EventUsage` 时 `appendEvent("model_call", …)`）；rig 事件带 `Iteration`（`rig/agent.go:399`） |
| ★ 请求快照（system prompt sections + tool schema + history 分类 + 注入来源） | 组成树、注入来源、每 step 对比 | 新增 `internal/modules/agent/context_capture.go`；run 前在 `agent.go:2200-2255` 生成并 `appendEvent("context_snapshot", …)`；`system_prompt.go:286-305` 改造成 section 列表 |
| ★ 压缩事件 | 压缩时间线、前后 token | `compact_now.go` 结果处 `appendEvent("compact", …)`（`CompactResult` 已有 Before/After，`chat.go:197-202`）；自动压缩只能启发式（无 rig 事件） |
| ★ spill 事件 | 清理/大结果时间线 | `handleEvent` 的 `EventToolResult` 分支用 `rig.IsSpilledToolResult(event.Result.Content)` 判定并写事件（`agent.go:2402-2437`） |
| ★ 跨会话聚合契约 | 洞察页 | 新 `internal/contracts/insights.go` + `internal/modules/insights`（或 sessionstore 扩展查询 + 后端模块） |
| ★ 成本单价 | 成本页 | `contracts/modelcatalog.go:10-45` 加 `InputPrice/CachedInputPrice/OutputPrice`（每 1M token）；设置面编辑 |
| 工具耗时/退出码 | 命令执行页 | rig 不提供；可在产品包装层计时（tool_call→tool_result 墙钟，并行批次近似）并从结果文本解析 `[exit code: N]`（启发式，需标注近似） |
| 同步 subagent 用量 | Fleet 成本归属 | `subagent.go:189-199` 把 `result.Usage` 补进 `contracts.SubagentResult`/`SubagentActivity`；rig 已提供（`rig/subagent.go:29-40`） |

---

## 3. UI 落位建议（功能 → 模块 → 入口 → 新增契约/命令/设置）

> 壳层事实：新 screen 只需 `RegisterScreen`（`internal/uiroot/runtime.go:63-83`），侧栏自动出导航行（`sidebar.go:416`），但**中文标签/图标要加 `ScreenNavSpec` case**（`sidebar.go:1589-1604`，未知 id 回落为 id + `icons.Data()`）；会话页头 tab 由 host B3 band 渲染（`frame.go:577-582`），session 注册 `SlotScreenTabs`（`session.go:558-580`）。

| 交付物 | 落位模块 | 入口/机制 | 需要新增 | 不做什么 |
|---|---|---|---|---|
| **A. 上下文洞察页**（跨会话趋势/成本/模型分布） | 新 UI 模块 `internal/ui/insights`（新包，只 import contracts/uikit/design/icons/lib） | `RegisterScreen(contracts.ScreenSpec{ID:"insights", Title:"洞察", Order:40})`；侧栏自动导航；`/insights` 或 palette 命令 `zeokeel.insights.open`（`Navigator.Push`） | 新 screen 注册 + `assembly.Registry()` 与 `assemblies/desktop.json` 各加一行（`internal/assembly/registry.go:44-98`, `assemblies/desktop.json`）；`sidebar.ScreenNavSpec` 加 `case "insights"`（中文名+图标）；新契约 `contracts.KeyInsights` | 不改 uiroot 壳层；不做卡片墙（用 `ui.List/Table`）；不把图表做成自绘 canvas |
| **B. 会话 Context 标签页** | `internal/ui/session`（现有模块） | 在 `tabChat/tabTrajectory/tabMemory` 后加 `tabContext`（`view.go:33-35`）；`buildTabs` labels 加一项（`view.go:855-887`）；`buildScreen` switch 加 pane（`view.go:167-170` 附近）；`Config` 加 `TabContext` 标签（`session.go:55-62`） | 新文件 `internal/ui/session/context_pane.go`（组成条 + 趋势 + 浏览器）；从新契约读取；帧测试 + 快照 | 不新增页头第二行；不重排 transcript；浏览器行不得自行渲染 `ChatMessage/ToolCall`（DS-04，见 §5） |
| **C. 右栏 Context 面板** | `internal/ui/rightpanel`（现有模块） | 固定页加 `pageContext`（`pageFixedCount` 前，`rightpanel.go:50-63`）；`buildTabStrip` 标签（`rightpanel.go:80-123`）；`view.go:51-56` switch 加 case；命令 `rightpanel.context.open`（仿 `filesOpenCommandID/terminalOpenCommandID`，`rightpanel.go:463-474`），可选快捷键 | `Config.ContextTab`、`state` 数据缓存、后台读取走 `UIRuntime.Post`（`rightpanel` 已有此模式） | 右栏宽度固定 320/min 240（`design/components.go:54-62`），别做多列宽表；不加第二个右栏 |
| **D. `/context` 命令** | `internal/ui/session` + `internal/ui/rightpanel` | `sessionCommands`（`session.go:722-900`）加 `zeokeel.session.context`：切到 `tabContext` 并 `Invalidate`；命令自动出现在 `/` 菜单与 `/help`（`commands_help.go:94-130`） | 可选第二命令 `zeokeel.context.panel` 打开右栏 Context 页（经 `uiroot.open-inspector`） | 不把 `/context` 做成弹窗；命令 Enable 条件=有活动会话 |
| **E. 设置卡片** | 新设置页（可由 `insights` 模块自己 `RegisterSettingsPage`，避免继续膨胀 1621 行的 `settings.go`） | `RegisterSettingsPage(SettingsPageSpec{ID:"context", Section:"高级", …})`（`contracts/contracts.go:37-58`；现有注册样例 `settings.go:691-916`） | 设置项键（JSON）放 contracts：采集开关、保留天数、显示成本、单价表入口、清空统计；页脚保存用 `SettingsPageSpec.Footer/Dirty`（`contracts.go:45-57`） | 不在内容底部放主按钮；价格编辑若进「模型」页需改 `modelcatalog` 编辑器（工作量大） |

**导航/命令汇总**：新增 screen 1 个（`insights`）、命令 2-3 个（`zeokeel.insights.open`、`zeokeel.session.context`、可选 `rightpanel.context.open`）、设置页 1 个（`context`）、会话 tab 1 个（Context）。**不新增右栏第 2 个模块**（`INJECTION-POINTS.md:118` 明确右栏只一个多标签模块）。

---

## 4. 数据管道（rig event → module → contract → UI）

### 4.1 目标路径

```text
rig run goroutine
  ├─ EventUsage（每次模型调用，含 Iteration/Usage）        rig/agent.go:382-402
  ├─ EventToolCall/EventToolResult（含 spilled 摘要）       rig/agent.go:540,673-684
  └─ EventTurnStart/End                                    rig/agent.go:230-283
        │  (Emit 同步回调)
        ▼
internal/modules/agent.handleEvent                        agent.go:2342-2452
  ├─ 现有：流式文本/推理/工具状态/错误 → ChatMessage 读模型
  └─ 新增 context_capture.go：
       run 前  → context_snapshot 事件（sections/tools/history/注入来源）
       每次调用 → model_call 事件（iteration/tokens/contextTokens）
       tool_result 后 → spill 事件（IsSpilledToolResult + bytes）
       compact 后 → compact 事件（Before/After/Replaced/Saved）
        │
        ▼
sessionstore.AppendEvent → SQLite events 表              store.go:694-725
        │
        ├─────────────────────────────┐
        ▼                             ▼
contracts.KeyContext（会话内读）   contracts.KeyInsights（跨会话聚合）
agent 实现（可选接口 type-assert）  internal/modules/insights 实现（新后端）
        │                             │
        ▼                             ▼
internal/ui/{session,rightpanel}      internal/ui/insights
（UIRuntime.Post + Invalidate，contracts.go:211-216 / uiroot/runtime.go:49-59）
```

契约建议（**不 import rig**，避免 UI 层被 rig 类型污染；现有 `settings.go:30`、`plugins/view.go:10` 已直接 import rigtools，属历史漂移，新代码不要跟进）：

```go
// internal/contracts/context_inspect.go
type ContextSection struct{ ID, Label, Source, Unit string; Bytes, Tokens int; Hash string }
type ContextRequest struct{ Iteration int; Model string; At time.Time
    PromptTokens, CompletionTokens, CachedTokens, ContextTokens, TotalTokens int }
type ContextSnapshot struct{ At time.Time; Model string; Sections []ContextSection
    Tools []ContextSection; History ContextHistory; Window int }
type ContextEvent struct{ Kind, Detail string; At time.Time; Before, After int }
type SessionContext struct{ Snapshot ContextSnapshot; Requests []ContextRequest
    Events []ContextEvent; Used, Window int }
type ContextInspector interface {
    SessionContext(sessionID string) (SessionContext, bool)
    ContextBrowser(sessionID string, kind string) []ContextItem
}
var KeyContext = cordis.NewServiceKey[ContextInspector]("zeokeel/context")

// internal/contracts/insights.go（P1）
type UsageAggregate struct{ Days []DayUsage; Sessions []SessionUsage; Models []ModelUsage; Workspaces []WorkspaceUsage; Cost CostSummary }
type InsightsService interface{ Aggregate(InsightsQuery) UsageAggregate }
var KeyInsights = cordis.NewServiceKey[InsightsService]("zeokeel/insights")
```

### 4.2 持久化 vs 现算（建议）

| 数据 | 策略 | 理由 |
|---|---|---|
| 逐调用 usage、run 快照、spill、compact、注入来源 | **持久化**（events 表，新 type，JSON payload；无 schema 变更） | 重启后仍可看历史；跨会话聚合的基础 |
| 每回合聚合 usage | 已持久化（`usage` 事件） | 与现有数据兼容；可作为 rollup 的输入 |
| 消息/工具/文件/记忆/技能的 token 估算 | **现算 + 缓存** | 读模型已有；用 `rig.EstimateTokens/EstimateTextTokens`（`rig/context.go:25-73`）与分段文本长度；每帧只算当前会话、缓存到模块实例 |
| 跨会话日/模型/工作区聚合 | **投影表**（推荐）或「事件表聚合 + 缓存」 | 单连接 SQLite（`store.go:177-179`）下避免每帧全表扫描；见 §4.3 |
| 成本 | 现算（投影里存 token，价格配置变动时重算） | 价格是设置项，不应回溯改历史 token |

### 4.3 SQLite/sessionstore 承载跨会话聚合的可行性

- **可行**：events 表是通用 `(seq,session_id,type,payload,text,created_at)`，新增事件类型零迁移（`store.go:140-148,694-725`）；sessionstore 已有按会话查询与全局搜索（`store.go:729-805`）。
- **必须补**：`CREATE INDEX IF NOT EXISTS idx_events_type_created ON events(type, created_at)`（当前只有 `(session_id,seq)`，`store.go:148`）——schema 在 open 时幂等执行（`schemaStatements`，`store.go:116-152`），加一条即可；若要按模型/工作区过滤，建议**投影表**：
  `usage_rollup(day TEXT, model TEXT, workspace_id TEXT, prompt INTEGER, completion INTEGER, cached INTEGER, calls INTEGER, turns INTEGER, PRIMARY KEY(day,model,workspace_id))`，在 `run_end` 后由 agent（或 insights 模块订阅）同事务 upsert。
- **单连接约束**：`db.SetMaxOpenConns(1)`（`store.go:177-179`）意味着聚合查询会与写入串行。结论：**任何 >1ms 的聚合都不得在 build 回调里做**；由 `internal/modules/insights` 在 goroutine 里查、用 `UIRuntime.Post` 回 UI（`ARCHITECTURE.md:45`），并按「刷新按钮/进入页面时一次」而非每帧刷新。
- **数据量/保留**：events 会随每次模型调用线性增长；设置页给保留天数（默认 30/90）与「清空统计」，删除走 `DELETE FROM events WHERE type IN (...) AND created_at < ?`（需要 provider 支持，建议 P1 一起做）。

---

## 5. 与 DESIGN/架构硬约束的冲突点

| 约束 | 冲突/风险 | 处理建议 | 证据 |
|---|---|---|---|
| **token-only 颜色** | 图表多序列容易写 hex / `ui.RGBA` | 序列色只用 `t.Accent/t.Success/t.Warning/t.LabelSecondary/t.LabelTertiary`；`ui/chart` 官方件（`DESIGN.md:173`；`mygo ui/chart`（headless 图元包））只做布局，颜色由 token 注入 | `DESIGN.md:269`（AP-01/DS-01）、`DESIGN-LINT.md:129` |
| **强调色预算**（每屏 ≤2 Accent 填充、≤1 primary、≤2 红色） | 洞察页若用 accent 填充条/多 primary 会直接违规 | 图表用中性色 + 单 accent 高亮；状态用 `uikit.StatusBadge`；成本/超限提示只在一处 red | `DESIGN.md:189,288-289`（AP-19/AP-20）、`AGENTS.md:10` |
| **状态徽章统一** | 上下文 >80% 之类状态若用中性底+色字拼装 | 一律 `uikit.StatusBadge`（语义 tint + 必填 label） | `DESIGN.md:289`（AP-21）、`DESIGN-LINT.md:142` |
| **字号纪律** | 表格里塞 11px 成句、数字标签用 10px | 表格主文 `TypeXS/TypeXXS`；11px 仅微标签/kbd；页面标题按 B3 顶带 `TypeM/600` | `AGENTS.md:12`、`DESIGN.md:290`（AP-22/DS-15） |
| **度量档位** | 自造 38 高行、非 token 间距 | 控件高度 32/36/40/44；间距 `design.Space*`；圆角 token；集合用 `ui.Table/ui.List` | `DESIGN.md:190`（AP-23）、`design/components.go:33-45` |
| **选择态两级** | 洞察页/浏览器行用满蓝选中 | 持久列表当前项 `BgSelected`+2px Accent 左条；瞬态浮层才 Accent 填充 | `DESIGN.md:187,287`（AP-19） |
| **Agent surface 入口**（DS-04） | Context 浏览器若自行渲染 `contracts.ChatMessage/ToolCall`，designlint DS-04 直接 error | 消息/工具行复用 `uikit.SessionThread`/`uikit.ToolRow` 的入口；浏览器只做「索引行 + 跳转」，不复制消息卡解剖 | `DESIGN-LINT.md:132`；`DESIGN.md:228-229` |
| **自绘白名单** | 组合条若用 `Box` 手绘比例条 | 优先官方 `ui.Meter`/`ui.Progress`（`DESIGN.md:160`）；确需自定义则登记 §7.1 + lint 白名单 + 测试 | `AGENTS.md:15`、`DESIGN.md:280`（AP-15） |
| **模块 import 边界** | UI 模块只能 contracts/uikit/design/icons/lib；不能 import rig/uiroot/host/其他 UI 模块 | 统计/估算/聚合全放 backend module + contracts；UI 只读契约 | `ARCHITECTURE.md:49-51`、`AGENTS.md:6` |
| **UIRuntime.Post 线程模型** | 聚合查询/事件回放在后台，不能直接改 UI 状态 | 后台 goroutine → `Post(func(){…; Invalidate()})`；不得跨帧存 `ui.Element` | `ARCHITECTURE.md:45`、`contracts/contracts.go:211-216`、`uiroot/runtime.go:49-59` |
| **注册生命周期** | 新 screen/page/command 注册必须 `ctx.Effect` 托管 | 复用各模块现有 `own(ctx, …)` 模式 | `ARCHITECTURE.md:258`（§9.4）、`INJECTION-POINTS.md:12-26` |
| **测试/designlint/快照** | 新 UI 必须帧测试 + 快照 + `designlint --strict --baseline` 不新增违规 | 每个 tab/page 至少 1 帧测试；新增 `ui-snapshot` 场景（现有 `p1-trajectory`、`p2-team-*`，`internal/devtools/ui-snapshot/main.go:490-855`）；baseline 不刷新 | `AGENTS.md:16`、`UI-CONFORMANCE.md:90-98`、`AGENTS.md` 命令节 |
| **不删用户可见信息** | 现有状态行/用量明细不得为了新页面删除 | 保留，新能力叠加；删除信息项需用户明确同意 | `AGENTS.md:17`；`view.go:3713-3800` |

---

## 6. 工作量粗估（人天量级，含测试/快照/i18n，不含评审缓冲）

> 假设 1 名熟悉本仓库的工程师；「新增/改动」列出主文件（不是全部测试文件）。

### P0（会话内 Context 组成 + 趋势 + 浏览器 + /context）

| 项 | 新增/改动 | 人天 |
|---|---|---|
| 契约 | 新增 `internal/contracts/context_inspect.go`（类型 + `KeyContext`/接口，~120 行） | 0.5 |
| 采集 | 新增 `internal/modules/agent/context_capture.go`（run 快照 + 逐调用/compact/spill 事件，~250 行）；改 `system_prompt.go`（section 化，~80 行）、`usage.go`/`agent.go`（handleEvent 落 model_call，~40 行）、`compact_now.go`（落 compact 事件）、`spill.go` 无需改 | 3.0 |
| 读取/服务 | agent 实现 `ContextInspector`（事件回放 + 现算 + 缓存，~250 行）；重启后从事件恢复会话级用量 | 1.5 |
| 会话 Context tab | 新增 `internal/ui/session/context_pane.go`（组成条/趋势/浏览器，~400 行）；改 `view.go`（tab 枚举/switch/slot 标签）、`session.go`（Config/命令） | 2.5 |
| 右栏 Context 固定页 | 改 `rightpanel.go`（page 枚举/config/命令）+ 新增 `view_context.go`（~250 行） | 1.5 |
| `/context` 命令 | 改 `session.go:722-900`（命令 + 开 tab/面板） | 0.5 |
| 验证 | 帧测试（tab 切换/空态/错误态/背景加载）、`ui-snapshot` 2 个场景、designlint | 1.5 |
| **小计** | | **≈11（9–13）** |

### P1（洞察页 + 跨会话聚合 + 成本 + 设置）

| 项 | 新增/改动 | 人天 |
|---|---|---|
| 聚合契约 | 新增 `internal/contracts/insights.go`（~150 行） | 0.5 |
| 聚合后端 | 新增 `internal/modules/insights`（查询/缓存/订阅/保留期清理，~500 行） | 3.5 |
| 投影与索引 | 改 `sessionstore/store.go`（`usage_rollup` 表 + `(type,created_at)` 索引 + upsert 接口，~120 行） | 1.5 |
| 洞察页 | 新增 `internal/ui/insights`（screen + 图表 + 表，~500 行）；改 `internal/assembly/registry.go`、`assemblies/desktop.json`、`sidebar.ScreenNavSpec` | 2.5 |
| 成本 | 改 `contracts/modelcatalog.go`（价格字段）+ `internal/ui/settings/modelcatalog*.go`（价格编辑）+ 后端计算 | 2.0 |
| 设置页 | 新增 Context/用量设置页（采集/保留/清空/单价入口，~200 行；可放 insights 模块） | 1.0 |
| 验证 | 帧测试/快照（亮暗/窄窗/空态）/i18n/designlint | 1.5 |
| **小计** | | **≈12.5（10–15）** |

### P2（Fleet / 文件活动 / 命令执行 / 注入事件）

| 项 | 新增/改动 | 人天 |
|---|---|---|
| Fleet | 改 `contracts/agentsurface.go`+`subagent.go:189-199` 补同步子代理 usage；insights 增 Fleet 面板（复用 `TeamService/ChildAgentService/JobService`） | 2.5 |
| 文件活动 | 事件已有 path；insights/Context 浏览器加「文件读写/搜索」聚合与筛选（可复用 `workspace` differ） | 2.0 |
| 命令执行 | 采集层加工具计时（wrap tool / tool_call→tool_result 墙钟近似）与退出码解析（`[exit code: N]` 启发式）；列表/详情 UI | 3.0 |
| 注入事件 | 快照 section hash 对比 + 注入时间线（AGENTS.md/memory/skills/preset 变更） | 2.0 |
| 自动压缩可观测 | 启发式检测（同回合 contextTokens 下降 + 估算 history 超预算）并标注「推断」；或立项改 rig 加 `EventCompact` | 1.5 |
| 验证 | 帧测试/快照/i18n | 1.5 |
| **小计** | | **≈12.5（10–16）** |

> P0 可独立交付「会话内 Context」；P1 依赖 P0 的事件格式；P2 的 Fleet/文件可先复用现有服务做只读，不必等 P1 投影表。

---

## 7. 风险表

| 风险 | 等级 | 说明 | 缓解 |
|---|---|---|---|
| rig 不暴露请求快照/自动压缩事件 | 高 | `Request` 不回传、`prepareContext` 无事件（`rig/agent/agent.go:348-353,489-506`）；产品只能估算/启发式 | P0 只承诺「产品侧快照 + 估算」；自动压缩标「推断」或立项改 rig（发 `EventCompact`） |
| 重启后用量读模型为空 | 高 | `MessageUsage` 仅内存（`usage.go:3-12`），`restore` 不回放 usage（`agent.go:872-940`） | 会话 Context 页从事件回放，不依赖 `ChatMessage.Usage`；同时修状态行（可选） |
| SQLite 单连接 + events 无 type 索引 | 中高 | 聚合查询阻塞写入/卡帧（`store.go:177-179,148`） | 只读查询放后台 goroutine；P1 投影表 + 索引 + 保留期；禁止 build 内 I/O |
| 组成归属的「估算 vs 真实」口径 | 中 | prompt token 来自 provider，分项只能用 `EstimateTextTokens`；两者相加可能不等于真实 | UI 明确标注「估算」；以 provider 的 `ContextTokens` 为总量锚点，分项按比例/独立估算并给误差说明 |
| DESIGN 强调色/字号/DS-04 | 中 | 图表与浏览器最容易违规 | 先做线框评审；用官方 `Table/List/Meter/StatusBadge`；designlint 不刷新 baseline |
| 改动面广、多人并行冲突 | 中 | 涉及 contracts/agent/sessionstore/session/rightpanel/settings/assembly 7 处 | 按 task 拆分 write scope：contracts+agent 一个 owner；session 一个；rightpanel 一个；insights 一个 |
| Fleet 口径不完整 | 中 | 同步子代理用量被丢弃；后台任务无 token | 先交付「已有数据」（团队 `UsageTokens`），同步子代理字段补齐后排 P2；UI 标注数据来源与缺失 |
| 功能范围膨胀 | 中 | dsh-context 含 10+ 面板，全量等价成本 >35 人天 | 按 P0/P1/P2 收口；每档单独验收，不做「看起来完整」的装饰面板（`AGENTS.md:7`） |

---

## 8. 关键证据索引（便于复核）

| 结论 | 证据 |
|---|---|
| 会话状态行已有上下文/缓存/速度 | `internal/ui/session/view.go:3713-3800`（`sessionStatusLine`）、`view.go:3638-3692`（窗口解析）、`view.go:1892-1897`（消息用量摘要） |
| 回合用量只在内存 + 一条事件 | `internal/modules/agent/usage.go:3-12,35-65`；`internal/contracts/chat.go:270-285` |
| 逐调用 usage 被丢弃 | `internal/modules/agent/agent.go:2348-2350`；rig 事件定义 `rig/agent.go:15,399` |
| run_start 仅 model | `internal/modules/agent/agent.go:2255`；run_end `agent.go:2564-2566` |
| 系统提示词组装与不持久化 | `internal/modules/agent/system_prompt.go:1-18,286-305,374-395,399-421,440-507`；AGENTS.md 注入 `agent.go:677-717`；preset `preset.go:109` |
| 工具 schema/名字集合 | `system_prompt.go:310-329`；`agent.go:2222-2233`；`rig/message.go:95-100`；`rig/tools/registry.go:75-88` |
| 手动压缩无事件 | `internal/modules/agent/compact_now.go:31-131`；`contracts/chat.go:195-208` |
| 自动压缩不可观测 | `agent.go:2247-2253`；`rig/agent/agent.go:489-506` |
| spill 可识别 | `rig/context.go:83-101`；`rig/agent/spill.go:21-64`；`rig/agent/agent.go:668-684`；产品 `internal/modules/agent/spill.go:24-44` |
| 事件表结构/写入/查询 | `internal/modules/sessionstore/store.go:1-40,116-152,694-725,729-805`；单连接 `store.go:177-179` |
| 工具时间线事件回放模式 | `internal/modules/agent/tool_timeline.go:28-47,79`；`agent.go:2393-2400,2432-2435` |
| 会话 tab / 命令面 | `internal/ui/session/view.go:33-35,855-887`；`session.go:55-62,545-580,722-900`；`internal/contracts/contracts.go:95-106` |
| 右栏已退役 8-pane、现为固定页 | `internal/ui/rightpanel/rightpanel.go:1-20,50-63`；`view.go:30-62` |
| 设置页注册机制 | `internal/contracts/contracts.go:37-58`；`internal/ui/settings/settings.go:41-75,691-916`；诊断页 `settings.go:541-584,977` |
| 侧栏自动列 screen / 需补标签 | `internal/ui/sidebar/sidebar.go:416,1589-1604` |
| 模块边界/Post/注册 | `docs/ARCHITECTURE.md:43-51,110-120,252-261`；`internal/contracts/contracts.go:211-240`；`internal/uiroot/runtime.go:49-83,117-137,171+` |
| 设计约束 | `docs/DESIGN.md:173,187-190,228-229,269-292`；`docs/DESIGN-LINT.md:129-147`；`docs/UI-CONFORMANCE.md:25-37,90-98` |
| 模型目录无价格 | `internal/contracts/modelcatalog.go:10-45` |
| Fleet 现有读模型 | `internal/contracts/agentsurface.go:53-77,123-131`；`internal/contracts/team.go`；`team.go:419`、`childagents.go:374`；同步 subagent 丢 usage `subagent.go:189-199`，rig 已提供 `rig/subagent.go:29-40` |

---

## 9. 未验证 / 边界（诚实声明）

1. **未改任何代码**，本报告所有「新增/改动」均为建议；未跑测试/快照（只读调研）。
2. 自动压缩的「启发式检测」未做原型验证（需真实 provider 跑多轮触发预算）；只在代码层确认 rig 不发事件。
3. 逐模型调用耗时无法从 rig 获得（`Event` 不含时间字段，`rig/agent.go:19-29`）；本报告的「近似计时」指产品侧收到 `tool_call`→`tool_result` 的墙钟，且并行工具批次只能给批次耗时。
4. 命令退出码只能从结果文本解析（工具 guidance 的 `[exit code: N]` 约定，`system_prompt.go:151`），非结构化保证。
5. 未评估 `events` 表在长期使用下的体积；保留期/清理策略需 P1 实测（10 万事件级别查询计划）。
6. 未验证 `ui/chart` 在本项目主题/TextScale 下的实际渲染（首次引入需小原型 + 快照，按 `DESIGN.md:173` 与 `UI-CONFORMANCE.md` 流程）。
