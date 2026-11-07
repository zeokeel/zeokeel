# 旧功能覆盖审计（old/ → 当前产品）

> 日期：2026-11-08 · 只读审计：`old/internal`（旧 ZeoKeel）+ `old/internal/shell`（55k 行 UI）+ 当前
> `internal/modules/*`、`internal/ui/*`、`internal/assembly/assemblies/*.json`。未改代码；
> 判定以当前工作树代码/装配为准，不采信旧文档或废弃计划中的"已完成"陈述。
> 本文件只回答：**旧功能覆盖清单是否完成；剩余真正影响用户的缺口是什么。**

## 0. 结论摘要

- **核心产品闭环已覆盖**：会话/流式/持久化/恢复、审批、工具卡与轨迹、Home、Sidebar（置顶/归档/工作区树/
  搜索/重命名/删除/复制）、设置（模型/服务商/外观/快捷键/技能/诊断/更新/导入/关于）、右侧检查栏
  （概览/工具/日志/文件/变更/终端）、i18n、工作区沙箱、终端、memory/skills/automation/plugins 后端、
  ACP server、DSH 导入、更新检查、headless 形态均可在当前装配中运行。
- **但"旧功能全量覆盖"未完成**：62 项审计条目中 ✅24 / 🟡23 / ❌15。缺口集中在 **composer 深度**
  （模型/推理强度/权限/计划/slash 命令）、**编排类 UI**（plan/modes/team/todo/artifacts/ask_user）、
  **模型目录与预设**、**默认可用 web search / MCP 配置**、以及宿主平台能力。
- **是否可宣布覆盖完成：否**。可以宣布"核心对话产品可用"，不能宣布"old 功能面等价"。

## 1. 统计口径

| 状态 | 数量 | 占比 | 含义 |
|---|---:|---:|---|
| ✅ 已迁移 | 24 | 38.7% | 当前代码有对应实现且已进入 desktop/headless 装配或明确等价 |
| 🟡 部分迁移 | 23 | 37.1% | 主路径可用，但缺字段/子功能/交互/接线，或输出格式/语义降级 |
| ❌ 未迁移 | 15 | 24.2% | 当前无等价实现；含"计划明确不做/系统级"与"计划内尚未做" |
| 合计 | 62 | 100% | 28 项后端能力 + 34 项 Shell/UI 能力 |

## 2. 后端能力矩阵（old/internal 顶层包，28 项）

| old 能力 | 状态 | 当前实现/证据 | 差异说明 |
|---|---|---|---|
| `acp` | 🟡 | `internal/modules/acp`、`internal/host` RunACP | ACP server 子集（initialize/session-new/prompt/cancel、chunk 流、错误码）；缺 session/list/load、auth、fs/terminal 双向请求、tool_call 更新 |
| `agentteam` | ❌ | `internal/hosttools` 的 `team` 工具 + rig `tools/team` 任务板 | 只有内存 roster/任务板；旧包的真实异步 subagent runner（spawn/steer/interrupt/续谈、成员上下文）未接 |
| `automation` | 🟡 | `internal/modules/automation` + `internal/ui/automation` | cron 子集 + every/after、持久化、ticker、schedule 工具替换；缺旧 store 的 automation_runs 历史/多年限审计 |
| `brand` | ✅ | `internal/brand` | palette/theme/logo/wordmark |
| `design` | ✅ | `internal/design` | tokens/style/motion/font/components 子集 |
| `globalhotkey` | ❌ | 无 | 系统级全局热键（Carbon/DBus）未实现；当前只有窗口内快捷键（uiroot） |
| `i18n` | 🟡 | `internal/uiroot/locale.go`、`internal/uikit/locale.go`、各 UI 切片 | 主要界面已 en/zh 动态；仍缺 host chrome tooltip、原生 Dialog 标题、部分后端数据文案、home 命令面板静态标题等 |
| `icons` | ✅ | `internal/icons`（SVG） | Lucide SVG 资产 |
| `importer` | 🟡 | `internal/modules/importer` + settings 导入页 | session.v4.jsonl Scan/Import、user/assistant 文本+reasoning、workspace；旧 attachments/credentials/manifest/zstd 未迁移（计数说明） |
| `lsp` | ❌ | 无；rig 有 `tools/lsp` 但 toolkit 未注册 | 旧 LSP client/servers 配置/工具未接；rig 替代未启用 |
| `mcp` | 🟡 | `internal/modules/toolkit`（`mcp.StartHTTP`）+ `internal/ui/plugins` | 仅远端 Streamable HTTP；缺 stdio 子进程、`mcp.json` workspace 配置/opt-in、配置编辑 UI |
| `memory` | 🟡 | `internal/modules/memory` + rig memory tool + session Memory tab | JSON MemoryStore/tool/搜索/添加可用；旧 consolidate/extract/auto-memory/prompt memory 未迁移 |
| `modelcatalog` | ❌ | 无；settings 仅有 3 个静态 provider preset | 无模型目录（context window/efforts/推理强度）、添加/编辑模型、连接检查 |
| `presets` | ❌ | 无 | 无 agent presets/persona/tool allowlist/prompt 组合、JSON 用户覆盖 |
| `prompt` | 🟡 | `agent.Config.SystemPrompt` + settings system prompt | 只有静态 system prompt；无 modes/plan/memory/AGENTS.md 注入策略、auto-title/review prompt |
| `provider` | ✅ | rig `provider/openai` + `internal/modules/agent` | OpenAI Chat Completions 兼容（旧也只有 openai） |
| `runtime` | 🟡 | `internal/modules/agent` + rig agent/gate/compact | 循环/审批/压缩/检查点/多模态 parts 可用；缺 modes、review、subagent runtime、goal driver、PTC/workflow |
| `session` | 🟡 | `internal/modules/agent`（ChatService） | 发送/取消/历史/恢复/压缩/fork/export/附件（标题取首条 prompt）；缺 goal 多轮驱动、tool spill、per-message 反馈、附件服务（图片内联在消息 JSON，读模型不保留原始字节） |
| `sessionreference` | ❌ | 无 | `@session` 引用/候选人/上下文摘要/预算错误分类未迁移 |
| `skills` | 🟡 | `internal/modules/skills` + settings 技能页 | config/dataDir/workspace 多根 List/Read/roots；缺旧 `.zeokeel/skills`/用户目录、文件监听、技能管理/编辑/Office 技能 |
| `state` | ✅ | ChatService 读模型 + uiroot runtime | old broker/projector 已被读模型替代；无事件重放/快照文件，但 UI 所需投影可用 |
| `store` | 🟡 | `internal/modules/sessionstore`（SQLite） | sessions/messages/events/checkpoints/search/workspace_id 可用；旧 attachments/automation_runs/feedback/model/setting/transcript 表语义分散到各模块 JSON/settings 或未迁移 |
| `tools` | 🟡 | `internal/modules/toolkit` + `internal/modules/hosttools` + rig tools | 文件/shell/editor/image/terminal/jobs/memory/schedule/worktree/skill/team 任务板/settings/MCP + hosttools ask_user/present/plan/goal/team；缺 rig 的 subagent/workflow/ralph/review/inspect/report/lsp 注册与旧 tool allowlist/presets |
| `updater` | 🟡 | `internal/modules/update` + settings 更新页 | 检查（manifest/https/1MiB/semver/asset）可用；旧 download/SHA256 校验/stage/install-on-restart 未迁移（P4 只做 check） |
| `websearch` | 🟡 | rig `tools/web`（`web_fetch`/`web_search`）+ toolkit | 默认 desktop assembly `toolkit.network=false`，web 工具不注册；无旧 websearch endpoint/key 设置与代理语义 |
| `winproc` | ❌ | 无 | Windows 控制台隐藏/环境/编码辅助未迁移；rig pwsh 仅部分覆盖 |
| `workspacechanges` | ✅ | `internal/modules/workspace`（WorkspaceDiffer）+ rightpanel Changes | git status/diff、二进制/超大截断、路径翻译；用户可见变更视图等价 |
| `app`（宿主组合根） | 🟡 | `internal/host`、`internal/assembly` | 窗口/设置/headless/ACP 已有；缺旧 autostart、single-instance、窗口几何持久化、通知、网络代理、update install（部分标为非目标） |

## 3. Shell / UI 能力矩阵（34 项）

| old Shell/UI 能力 | 状态 | 当前实现/证据 | 差异说明 |
|---|---|---|---|
| Home / 起始页 | ✅ | `internal/ui/home` | hero/greeting/setup card/composer/最近会话槽 |
| Composer 基础（输入/附件/发送/停止/模型 chip/mention/slash 触发） | 🟡 | `internal/ui/session/view.go`、`attachments.go` | 文本、附件、图片、拖放、发送/停止、模型入口、工作区 `@`、`/` 触发可用 |
| Composer 深度菜单 / 权限 / model / plan / goal / compact slash 命令 | ❌ | 无 | 旧 16 条 slash 命令（/file /plan /feedback /permission /model /memory /goal /compact /export /clear /stop…）仅剩 runtime.commands 的极少数命令；无 permission 模式切换、推理强度、plan/goal/compact 入口 |
| Chrome / Sidebar / 折叠 rail / 右栏 dock | ✅ | `internal/uiroot`、`internal/ui/sidebar` | 280px sidebar、64px rail、Cmd/Ctrl+B、右栏开关与设置入口 |
| 命令面板 / 窗口内快捷键 | ✅ | `uiroot/frame.go` palette + `uiroot/shortcuts.go` | Cmd/Ctrl+K、可配置两条 chrome 快捷键；非全局系统热键 |
| Settings 外壳与页面 | 🟡 | `internal/ui/settings`、`internal/ui/about` | 模型/服务商/外观/快捷键/技能/诊断/更新/导入 + 关于，en/zh；缺模型目录/预设/内置插件开关/插件详情/记忆管理页 |
| Approval 审批 Modal | ✅ | `internal/ui/approval` + `internal/modules/approvals` | allow/allow-always/deny、ask/auto/never 模式 |
| 右侧检查栏 | ✅ | `internal/ui/rightpanel` | 概览/工具/日志/文件/变更/终端，en/zh |
| Files 检查器 | ✅ | rightpanel `files.go` + workspace Files/ReadFile | 打开文件夹/面包屑/文本/Markdown/图片预览/错误态 |
| Terminal 命令运行器 | ✅ | rightpanel `terminal.go` + `internal/modules/terminal` | 每次独立 shell -c、workspace cwd、输出/退出码/停止/超时；无 PTY/持久 shell（产品取向） |
| Workspace Changes/diff | ✅ | rightpanel `changes.go` + WorkspaceDiffer | 未提交变更列表、状态 badge、diff 折叠/截断 |
| Trajectory / 工具时间线 | ✅ | session Trajectory tab | 轨迹卡片/工具调用时间线 |
| Tool cards | ✅ | `internal/ui/toolcards` | 24 个 rig 工具卡 + demo/兜底；MCP 动态工具走 session 兜底 |
| Markdown 渲染 | ✅ | mygo `ui/markdown`（session/skills/update notes） | 流式/表格/代码块由 mygo 提供 |
| Reasoning 展示 | 🟡 | session `buildAssistantMessage` | 折叠 Reasoning 文本；缺旧 ReasoningRow 的实时 preview/shimmer/运行态动画 |
| 消息级操作（复制单条/重试/反馈/用量 footer） | ❌ | 无 | 旧 message_actions（retry last、feedback good/bad、逐条复制、usage/tool summary）未迁移；只剩整会话复制/导出 |
| `@` mentions | 🟡 | session `buildMentionMenu` | 仅工作区文件路径；无 `@session` 引用、无候选上下文摘要 |
| 会话搜索 | ✅ | sidebar 搜索 + `internal/modules/agent` Sessions | 标题/分组内过滤 |
| 消息全文搜索 | ✅ | sidebar Message search + sessionstore `SearchMessages` | LIKE/转义/片段/限制/跨会话 |
| Fork | ✅ | agent `SessionForker` + sidebar 行菜单 | 深拷贝 messages/附件/tools、持久化历史/事件 |
| Export | 🟡 | session 导出 Markdown + 复制对话 | 缺旧 JSONL 会话日志导出（store/export） |
| Automation 页 | ✅ | `internal/ui/automation` + module | 任务列表/新建/运行/删除/after/every/cron |
| Plugins 页 | 🟡 | `internal/ui/plugins` + toolkit KeyPlugins | MCP 状态卡片 + 工具目录；缺旧内置插件开关（plugin.<name>.enabled）、插件详情/配置页 |
| Skills 页 | 🟡 | settings 技能页 + skills module | 发现/roots/预览/刷新；无创建/编辑/删除技能、无旧 manage 操作 |
| Memory 页 | 🟡 | session Memory tab + memory module | 搜索/添加/删除 scope；无旧 consolidate/extract、全局记忆设置页 |
| Workspace→Session 树/分组/计数 | ✅ | sidebar workspace tree | 多 workspace 分组、未分组、当前高亮、计数、搜索、折叠 |
| Plan UI（plan_review/exit_plan_mode/plan 卡） | ❌ | 无 | hosttools 有 plan_review/exit_plan_mode 工具，但 production 用 MemoryPlan，无 UI/状态机 |
| Modes UI（standard/minimal/creator 等） | ❌ | 无 | 无模式选择、工具子集/persona 切换 |
| Team UI（成员/任务/steer） | ❌ | 无 | 只有 team 工具的内存 roster/任务板，无 Team 屏、无真实 subagent 执行 |
| Todo UI（会话 todo 面板） | ❌ | 无专用 UI | rig misc `todo` 工具存在，toolcards 有卡片；无旧 todo 面板/进度视图 |
| Artifacts / Present UI | ❌ | 无 | hosttools `present` 存在，但 production MemoryPresenter；无 artifact 卡片/预览/打开动作 |
| 交互式 ask_user UI | ❌ | 无 | hosttools `ask_user` 存在，但 production MemoryAsker 返回 ErrNoInteractive；无旧 question gate Modal/等待 |
| About 页 | ✅ | `internal/ui/about` | 构建信息/架构/bullet，en/zh 动态导航 |
| Welcome/start tab | ✅ | `internal/ui/home`（`welcome` 模块为演示，不在 desktop assembly） | Home 起始页覆盖旧 start tab 主路径 |

## 4. 剩余缺口（按工作量小→大）

1. **S**：i18n 残留硬编码（host chrome tooltip、原生 Dialog 标题、后端错误文案、home 命令面板标题）。
2. **S**：消息级操作（复制单条/重试最后用户消息/feedback/usage footer）— 旧 `message_actions.go` 语义。
3. **S**：JSONL 会话日志导出（现只有 Markdown/整段复制）。
4. **S-M**：默认启用 web search 并加 endpoint/key 设置（当前 desktop assembly `toolkit.network=false`，rig `tools/web` 未注册）。
5. **S-M**：内置插件开关与插件详情/配置页（旧 `plugin.<name>.enabled`、`plugin_details.go`）。
6. **M**：模型目录 + 模型/推理强度选择（新 settings 数据模型 + composer selector；现只有 3 个静态 provider preset）。
7. **M**：composer 全量 slash 命令与权限模式切换（/permission /model /plan /memory /goal /compact /export /clear /stop /new 等 + 会话级 permission scope）。
8. **M**：MCP 完整化（stdio 子进程、workspace mcp.json opt-in、配置编辑；现仅远端 HTTP + 只读 Plugins 卡片）。
9. **M-L**：presets/persona + `@session` 引用（旧 presets/sessionreference 全量数据/预算/错误语义）。
10. **L**：plan/modes/team/todo/artifacts/ask_user 的 UI 与真实执行（hosttools 需从 MemoryHost 换成 UI/agent 真实能力；agent 需 GoalRoundDriver/subagent runner/review）。
11. **L**：updater 完整下载/SHA256/暂存/重启替换；宿主 autostart/single-instance/window geometry/notifications/network proxy。
12. **非目标/平台**：globalhotkey、LSP client、winproc、PTC/run_code、语音输入、插件市场；这些旧实现按产品计划删除或由 rig 可选替代，但 LSP/团队等 rig 替代尚未注册/配置。

## 5. "最小必须项"（若要以旧功能覆盖完成收口，≤5）

1. **会话主界面 composer 深度**：`internal/ui/session/view.go` + `internal/ui/settings/settings.go` 增加模型/推理强度选择、permission 模式、plan/memory/goal/compact 等旧 slash 命令入口；数据需 modelcatalog/presets 支持。
2. **hosttools 真实交互接线**：`internal/modules/hosttools/module.go` 从 `Module()`（MemoryHost）换成 `ModuleWithHost` 并由桌面宿主注入真实 Asker/Presenter/PlanController/GoalStore；新增 plan review/exit plan mode/artifact/ask_user 的 UI 对话框。
3. **web 工具默认可用**：`internal/modules/toolkit/toolkit.go` 注册 `web.NewSearcher`（及 fetch 策略），`internal/assembly/assemblies/desktop.json` 打开相应配置，settings 增加搜索 endpoint/key。
4. **MCP 完整化 + 插件管理**：`internal/modules/toolkit/toolkit.go` 支持 stdio/workspace mcp.json（opt-in gate），`internal/ui/plugins/view.go` 支持配置/刷新写操作与内置插件开关。
5. **模型目录/预设**：新增 `zeokeel/models`/product presets 数据与设置页（provider 模型卡、context window、effort、persona/tool allowlist、JSON 覆盖）；这是旧 modelcatalog/presets 的核心用户价值。

## 6. 建议的对外表述

- 对内部：**核心产品可交付，旧功能覆盖未完成**。剩余缺口主要是"高级交互/编排/集成"，不是基础对话链路。
- 对发布说明：可说"已支持完整对话、工作区/文件/变更/终端检查、设置/审批/搜索/fork/导出/i18n、ACP/导入/更新检查"；
  不要声称"旧功能全量迁移"。
- 若必须二选一：优先补 §5 的前 3 项（composer/交互工具/web），其余（plan/team/todo/artifacts、modelcatalog/presets、updater install、host 平台能力）按版本路线排期；globalhotkey/LSP/winproc/PTC 按非目标处理。

---

审计方法：`grep`/读文件核对当前 `internal/modules/*`、`internal/ui/*`、`internal/assembly/assemblies/*.json` 的注册与代码；
计数为本文矩阵行数（28+34=62），非代码行数/测试数。若有后续并行改动，以彼时工作树重新跑本矩阵。
