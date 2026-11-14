# 功能覆盖审计（旧产品 → 当前产品，复核版）

> 复核：2026-11-08（P7）。范围：旧产品 `internal` + `internal/shell` 对照当前（旧工程归档已移除，路径保留作历史审计记录）
> `internal/modules/*`、`internal/ui/*`、`internal/assembly/assemblies/*.json`、host/uiroot。
> 只改本文档；判定以当前代码/装配为准（presets 已随后落地，本文档已同步）。目标：回答"旧功能覆盖是否完成、剩余最小必须项"。

## 0. 复核摘要

- 自上一版后落地的关键能力：
  - **编排执行**：agent 真实 subagent runner（depth/并发上限、编排工具排除、fork/model/max-iterations
    覆盖）+ rig team manager 全量工具（team_*、legacy、DSH spawn_teammate/list_agents/wait_agent/
    send_message/interrupt_agent）+ **会话头部智能体团队浮层**（成员/状态/最新回答、打开成员会话）
    + **共享任务**（team_task_* 工具，团队浮层只读展示）+ goal 工具/会话 goal chip。
  - **Composer 高级控制**：/goal /plan /memory 原生对话框、**权限模式**（命令 + composer badge，默认工作区：工作区文件读写自动允许，shell/网络仍询问；
    只读/工作区可写/所有权限（DSH 同款预设），settings `agent.permission`）、**思考强度**（per-session、按模型系列/自定义模式 +
    toolbar 菜单 + provider `reasoning_effort`）、**@session 引用**（@ 菜单会话分组、token 持久化、
    agent 注入尾部 20 条/≤8000 rune、`session_refs` 事件）。
  - **MCP 管理深度**：Streamable HTTP + **stdio** 传输、assembly/settings/**workspace mcp.json
    opt-in** 三源合并（优先级 assembly>settings>workspace，URL/stdio 去重）、settings MCP 页传输/
    args/env 配置、**工具级开关**（`mcp.disabledTools`，Plugins 页 toggle，重启生效）。
  - **宿主**：单实例锁、XDG autostart、**install-on-restart**（pending.json + 下载 SHA 校验 + Replacer）、
    **toast 通知 overlay**（Notifier 桥 + 4 条上限/TTL/关闭）、**网络代理**（HTTP(S)_PROXY/NO_PROXY
    启动注入 + 设置「网络」页，重启生效）。
  - **工件历史**：uiroot `ArtifactLister` 最近 50 条 + rightpanel Files「产物」最近 10（含预览）。
  - **更新**：下载（流式 + SHA-256/size 校验 + 原子暂存）+ install 调度（`ScheduleInstall`/
    `PendingInstall` + 宿主启动 apply，失败保留 plan 待重试）。
  - **既有项复核**：pin/archive（agent + sidebar 行菜单 + show-archived 视图）、i18n 覆盖到新增页面、
    会话搜索/消息全文搜索、JSONL 导出、共享任务（团队浮层只读）、MCP 工具开关均已可用。
- **presets 已落地**：prompt 片段契约 + composer「预设/Presets」插入菜单 + 设置「预设」页 CRUD（名称/提示词/排序/
  恢复默认）+ 设置「模型」页的模型列表（modelcatalog.settings 动态读取）；旧 agent presets 的 tool allowlist 语义仍未迁移，
  故 presets 记 🟡。
- 复核统计（Round 65 终值）：62 项中 **✅61 / 🟡0 / ❌1**（❌ 仅 winproc，Windows 平台非目标）。✅ 为有等价实现或剩余差异属明确非目标；🟡 为已实现但存在
  深度/长尾差异（§5.1）；❌ 仅 winproc（Windows 平台非目标）。核心对话/持久化/工作区/审批/设置/右栏/搜索/导入/更新/
  编排/composer 控制/MCP 管理/宿主桌面能力均已可用。
- **功能面覆盖已完成**：旧应用的用户可见功能均有等价实现；剩余工作为 §5.1 的交互深度精修与 §5.2 的明确非目标。

## 1. 统计

| 状态 | 数量 | 占比 | 含义 |
|---|---:|---:|---|
| ✅ 已迁移/等价 | 61 | 98.4% | 当前代码有对应实现，进入 desktop/headless 装配或剩余差异属明确非目标 |
| 🟡 已实现（深度限制） | 0 | 0% | （全部关闭） |
| ❌ 未迁移 | 1 | 1.6% | 仅 winproc（Windows 平台非目标） |
| 合计 | 62 | 100% | 28 后端能力 + 34 Shell/UI 能力 |

## 2. 后端能力矩阵（28 项）

| old 能力 | 状态 | 当前实现/证据 | 差异说明 |
|---|---|---|---|
| `acp` | ✅ | `internal/modules/acp`、host RunACP | initialize（loadSession）/session-new/prompt/cancel/session/list/session/load、流式文本 + **tool_call/tool_call_update 流式更新（in_progress/completed/failed、4KiB 截断）**、错误码；auth/fs/terminal 属 ACP 客户端职责（非服务器范围） |
| `agentteam` | ✅ | agent `team.go` + rig `tools/team` + session 团队浮层 + team_task 共享板 | 真实异步 subagent runner（spawn/steer/wait/interrupt/close、fork/model/max-iterations）；全局 manager，团队浮层展示成员与共享任务（只读；成员会话在主栏打开） |
| `automation` | ✅ | `internal/modules/automation` + `internal/ui/automation` | cron/every/after、持久化、ticker、schedule 工具、列表/新建/运行/删除/运行历史/模型覆盖/**workspace + permission 字段（运行时经 SessionWorkspaceSwitcher/SessionPermissionSwitcher 应用）**/搜索筛选；独立 runs 表为内部存储形态非目标 |
| `brand` | ✅ | `internal/brand` | palette/theme/logo/wordmark |
| `design` | ✅ | `internal/design` | tokens/style/motion/font/components 子集 |
| `globalhotkey` | ✅ | host hotkey.go + mygo `GlobalShortcut` + 外观设置 | 可配置系统级加速键（修饰符+单键，固定顺序规范化），注册后 Show/Restore/Focus 窗口；留空关闭、非法/注册失败仅警告、shutdown 注销；桌面不可用环境安全回退 |
| `i18n` | ✅ | uiroot locale + uikit + 各 UI 切片 | 全部页面 en/zh + 命令动态重注册 + **后端错误分类本地化（auth/rate_limit/network/timeout/context/cancelled 标题+原因，原文保留）**；少量 host chrome tooltip 为长尾 |
| `icons` | ✅ | `internal/icons` | Lucide SVG |
| `importer` | ✅ | `internal/modules/importer` + settings 导入页 | DSH session.v4.jsonl Scan/Import、文本+reasoning、workspace、symlink 跳过/重复 ID 去重；旧 DSH 私有长尾（attachments/credentials/manifest/zstd 压缩）为明确非目标（预览中计数说明） |
| `lsp` | ✅ | toolkit lsp.go + 设置 LSP 页 | **多服务器列表 + 活动服务器选择 + workspace `.zeokeel/lsp.json` opt-in**（默认关、显式 allow、settings 优先合并）；启动活动服务器并注册 `lsp_*` 工具，卸载关闭；错误仅警告 |
| `mcp` | ✅ | toolkit `mcp.Start`（stdio+HTTP）+ settings/workspace 合并 + 工具级开关 | HTTP/stdio 握手/工具注册/失败诊断；assembly>settings>workspace mcp.json（opt-in）；`mcp.disabledTools` 注册前过滤；插件详情/市场在 Shell Plugins 行仍 🟡 |
| `memory` | ✅ | `internal/modules/memory` + rig memory tool + session Memory tab + 设置「记忆」页 | JSON MemoryStore/tool/搜索/添加/编辑/整理/清空 + **工作区身份 scope（`workspace:<id>`，List/Search 按当前工作区过滤，`ListAll` 供管理页）+ 统一默认 scope（Config.Scope 生效）** + **损坏 memory.json 隔离备份后空 store 继续（assembly 不失败）** + 确定性 consolidation（上限/保留期，**启动与每次写入自动执行**）+ **模型驱动 extract（opt-in 手动触发，最近 20 条→≤5 条，去重/忙碌守卫/坏 JSON 错误、scope 跟随配置默认、候选长度≤200 rune、密钥硬校验、部分失败返回已写入条目）** |
| `modelcatalog` | ✅ | `internal/modules/modelcatalog` + composer 模型菜单 + settings「模型」页（模型列表） | **用户配置的模型列表（列表为空时装配默认模型仍生效，UI 标注「默认模型，尚未配置连接」；旧 chat.model/baseURL/apiKey 连接在首次启动时迁移为一行）**、**每模型独立 Base URL/API Key（可同时配置多家服务商）**、per-session 模型切换、**模型 chip 两级菜单（一级「模型／推理等级」，二级模型按服务商分组、推理等级为“跟随模型默认+模式”）**、**每模型默认思考强度（Home composer / 设置编辑弹窗）+ 会话×模型思考覆盖（会话内「推理等级」，默认跟随该模型默认值；切换模型不会串用其他模型的强度）**、自定义模式 JSON 参数、**每模型生成参数（temperature/top_p/max_tokens）**、列表增删改/上下文窗口/默认模型/测试连接、消息元信息行预算显示 |
| `presets` | ✅ | contracts/presets.go + session presets 菜单/人设选择器 + settings「预设」页 | prompt 片段 CRUD/插入 + **AgentPreset（persona/prompt/tools allowlist/model/order）**：settings 代理预设 CRUD、composer「人设」选择器
（per-session 设置）、agent 每 run 应用（base→AGENTS→persona→prompt，model 默认、tool allowlist 与模式门禁叠加） |
| `prompt` | ✅ | agent SystemPrompt + settings 模型页提示词 + **AGENTS.md 注入** | 系统提示可配置；每次运行读取当前工作区 `AGENTS.md`（文件名可配/32KiB 截断/读取失败告警）追加注入；auto-title 按首条提示生成；review prompt 属 review 工具非目标 |
| `provider` | ✅ | rig `provider/openai` + agent 模块 + 模型列表每模型连接 | OpenAI Chat Completions 兼容 + **常用服务商预设（DeepSeek/OpenAI/Ollama/通义千问/Kimi/智谱/硅基流动/OpenRouter/Gemini/自定义，添加模型弹窗预填）** + **每模型 Base URL/API Key 解析（列表行优先，chat.*/env 回落）** + **每模型思考/采样参数注入（Request.Extra / AgentSpec Temperature/TopP/MaxTokens）** + **模型页运行参数（最大迭代、上下文预算自动压缩、流完整性开关）** |
| `runtime` | ✅ | agent + rig agent/gate/compact | 循环/审批/自动压缩/检查点/多模态 parts/subagent runner/team manager/goal 工具与 goal 桥 + **聊天模式（普通/计划/精简/创作）** + **目标暂停/自动续跑**；PTC/workflow/ralph/review 工具为明确非目标 |
| `session` | ✅ | agent ChatService + Regenerate/Fork/Archive/Pin/ModelSwitcher + usage + 排队发送 + goal 续跑/压缩 + tool spill | 发送/取消/历史/恢复/手动压缩/fork/export/附件/重新生成/会话模型/思考强度（模型级默认 + 按（会话×模型）覆盖，按模型系列与自定义模式）/turn usage/排队发送/消息字号/目标续跑 + **大工具结果落盘（>32KiB→`.zeokeel/tool-results`，持久化头部 2KiB+路径，保留 100，失败回退）** |
| `sessionreference` | ✅ | composer @ 菜单 + agent `references.go` + contracts/sessionreference.go | token 持久化/注入尾部 20 条/8000 rune/session_refs 事件 + **候选排序（ID 精确→前缀→标题，最近优先，上限 8）** + **sessionref: URI 编解码** + **预算错误分类（messages/runes typed error 与事件 budget 字段）** |
| `skills` | ✅ | `internal/modules/skills` + settings 技能页 | 多根 List/Read/roots/预览/刷新 + **根优先级：工作区（`skills`/`.agents/skills`/`.zeokeel/skills`）→ `skills.dirs` 用户目录 → assembly 目录 → dataDir → `$ZEOKEEL_SKILLS` → `~/.zeokeel/skills`；同名遮蔽有可见警告** + **List 只读文件前缀（≤8KiB/个、≤500 个、每根 ≤2000 条），load 才读全文（≤1MiB）** + 新建/编辑（dataDir 内原子重写、名称不可改、**保留未知 frontmatter 键**）/删除（仅 dataDir，realpath 校验）；**已超出旧实现（old 只有只读 API）**；workspace 编辑/文件监听为非目标 |
| `state` | ✅ | ChatService 读模型 + uiroot runtime | old broker/projector 被读模型替代；UI 所需投影可用 |
| `store` | ✅ | `internal/modules/sessionstore` | sessions/messages/events/checkpoints/search/workspace_id + text/search_text 迁移；旧表功能以等价语义落地：attachments 在消息 parts、automation_runs 在 automations.json runHistory、feedback 在 settings 行、per-session model 在主记录 |
| `tools` | ✅ | toolkit + hosttools + agent + rig tools | 文件/执行/编辑/图像/终端/jobs/memory/schedule/worktree/skills/settings/web/MCP/session/subagent/subagent_fork/team_*/spawn_teammate/list_agents/wait_agent/ask_user/present/plan_review/exit_plan_mode/goal/team_task_*/**lsp_*** 已注册 + **全工具级启停（tools.disabled 并集过滤）**；hosttools 只保留 ask_user/present/plan_review/exit_plan_mode/goal（**旧的内存 stub `team` 已移除**，真实团队工具由 agent 的 rig team manager 提供）；MCP 普通工具名 `mcp__<server>__<tool>`，资源/prompt 工具名 `mcp__<server>__mcp_list_resources` 等（按 server 命名空间，避免多 server 重名）；workflow/ralph/review/inspect/report/ptc 为明确非目标 |
| `updater` | ✅ | `internal/modules/update`（check/download/install）+ settings 更新页 + host apply | manifest/https/1MiB/semver/asset；下载流式+SHA/size 校验+原子暂存+install 调度+启动替换；Windows 运行中 exe 无法替换（保留 plan，非目标） |
| `websearch` | ✅ | `internal/websearch` + toolkit 动态 searcher | POST/Bearer/limit/10s/1MiB/错误语义；assembly network=true，设置 endpoint/key 即时生效 |
| `winproc` | ❌ | 无 | Windows 控制台隐藏/环境/编码辅助未迁移（平台非目标） |
| `workspacechanges` | ✅ | workspace WorkspaceDiffer + rightpanel Changes | git status/diff、截断；用户可见等价 |
| `app`（宿主） | ✅ | host/assembly | 窗口/设置/headless/ACP + interaction/artifact/plan/notifier 服务 + 单实例锁 + XDG autostart + install-on-restart + toast overlay（可关闭）+ 网络代理 + 系统托盘（显示/新建会话/设置/退出）/关闭到托盘 + 全局热键 + 主题三态 + 通知；窗口几何由 mygo StateKey 隐式持久化；Windows 运行中替换为平台非目标 |

## 3. Shell / UI 能力矩阵（34 项）

| old Shell/UI 能力 | 状态 | 当前实现/证据 | 差异说明 |
|---|---|---|---|
| Home / 起始页 | ✅ | `internal/ui/home` | hero/greeting/setup card/**真实 composer（多行输入、Enter 发送、单行动作行：附件菜单（选择文件/手动路径）+ 模型 + 模式 + 发送；发送后建会话并进入）/最近会话 |
| Composer 基础（输入/附件/发送/停止/模型 chip/@//） | ✅ | `internal/ui/session/view.go`、`attachments.go` | 文本、附件/图片/拖放、发送/停止、per-session 模型菜单、工作区 @、/ 菜单；动作行收敛为附件菜单 + 模型 + 会话设置菜单 + 发送 |
| Composer 深度（/goal /plan /memory/权限/thinking/@session/presets） | ✅ | `sessionCommands` + `command_dialogs.go` + `commands_help.go` + `compact_command.go` | **15 条命令**（/新会话/副本/导出/设置/目标/审阅/记忆/反馈/停止/附加/帮助/**压缩上下文/完成目标/放弃目标**）+ 权限模式、聊天模式、**模型与推理等级（模型 chip 两级菜单：一级「模型／推理等级」，二级选模型/选该模型推理等级；覆盖按（会话×模型）保存、默认跟随模型）**、@session、预设；目标生命周期（完成/放弃/编辑/暂停/恢复/清除）已全量 |
| Chrome/Sidebar/折叠 rail/右栏 dock | ✅ | uiroot + sidebar + session B3 顶栏 | 280px sidebar、64px rail、Cmd/Ctrl+B、**Cmd/Ctrl+N 新建会话、Cmd/Ctrl+, 设置、Cmd/Ctrl+K 命令面板**、右栏开关、添加工作区、视图选项（排序/分组持久化）、工作区搜索/置顶/归档；**会话 B3 顶栏**：40px 标题/工作区行（TypeM 16px 标题）+ 32px Tabs 行、浅色 band + 单条底边线、无返回/无 Alt+←、⋯ 会话操作（复制/导出/归档）、右上 palette+inspector；无贡献屏无空工具栏；输入框上方 dock（目标条 + 审批内联卡）；通知右上角（header 下方，最多 2 条） |
| 命令面板/窗口内快捷键 | ✅ | uiroot palette/shortcuts | Cmd/Ctrl+K、可配置两条 chrome 快捷键；非系统全局热键 |
| Settings 外壳与页面 | ✅ | `internal/ui/settings`、about | 17 页（高级分组默认折叠，日常导航 9 项）：常用（模型/行为/预设）、外观（外观/技能/快捷键）、集成（MCP/搜索/网络）、高级（通知/归档/导入/更新/诊断/记忆/LSP/数据与隐私）、关于；**模型页 = 模型列表（每模型服务商/Base URL/API Key/系列/思考模式/生成参数编辑；添加模型弹窗含常用服务商预填、密钥继承与测试连接，可设默认模型）+ 提示词 + 运行参数（最大迭代默认不限制/上下文预算/流完整性）**；**插件参数已落在 Plugins 页（已接线键）**；插件市场为明确非目标 |
| Approval 审批内联卡（V3 强调） | ✅ | `uikit.ApprovalDialog`（Inline）+ `internal/ui/approval` + approvals 模块 | 输入框上方内联卡（警示横幅+警示描边，不遮挡 transcript）；allow/allow-always/deny、无默认焦点、Esc=deny 最早一条且不中断本轮（session 判断审批优先）、多请求计数、非会话屏自动切回会话；旧 Modal 路径保留给 embedder；只读/工作区可写/所有权限三档预设（settings 覆盖；旧 ask/never 存值归一为只读；headless assembly 可配 ask/never 策略） |
| 右侧栏 | ✅ | rightpanel | 默认收起；多标签：开始引导 / 文件 / 终端 / 每个打开文件一个可关闭标签；文件页含工作区树、交付区、预览/改动；无概览/工具/日志/变更/团队/任务常驻 pane；en/zh |
| 文件页 / 文件标签 | ✅ | rightpanel files + workspace | 工作区树/面包屑/文本/Markdown/图片预览/错误态/交付区；打开文件新标签，内容可滚动 |
| Terminal 运行器 | ✅ | rightpanel terminal + terminal 模块 | 独立 shell -c、cwd、输出/退出码/停止/超时；无 PTY/持久 shell |
| Workspace Changes/diff | ✅ | rightpanel changes + WorkspaceDiffer | 未提交变更列表、状态 badge、diff 折叠/截断 |
| Trajectory/工具时间线 | ✅ | session Trajectory tab | 轨迹卡片/工具调用时间线 |
| Tool cards | ✅ | `internal/ui/toolcards` | rig 工具卡 + todo/team_task 卡 + demo/兜底；MCP 动态工具走 session 兜底 |
| Markdown 渲染 | ✅ | mygo ui/markdown | 消息/技能/更新 notes/artifact/plan 预览；mygo v0.9.0 的 GitHub alerts/mention/reference 与流式解析 |
| Reasoning 展示 | ✅ | session buildAssistantMessage | 折叠 Reasoning 文本 + **流式「思考中…」预览（尾部 120 rune、最多两行，结束自动消失）** |
| 消息级操作（复制/重试/用量） | ✅ | session 元信息 ⋯ 菜单 + 导出 | **一条紧凑元信息行**（时间 + `用时/tokens` 紧凑单位 + 单一 ⋯）：复制消息/重新生成/用量明细子菜单（时长/输入/输出/缓存/上下文预算，全紧凑值）；折叠轮的摘要行与元信息共用一行；**同一回合的连续助手步骤合并为一条过程摘要（工具数合计，默认折叠，只展开最终结果）**；**最新回复正文底部有常显「复制」按钮**；**工作步骤展示四档（简洁/标准/详细/完全展开，默认标准）+ 收起时机（回答结束后/下次有新消息，默认回答结束后）**；失败轮保留错误行+重试；**Markdown/JSONL 导出不再含评分字段**（消息反馈功能已移除） |
| `@` mentions | ✅ | session buildMentionMenu + agent references | 工作区文件 + 会话分组（@session，排除当前/过滤/上限 8）；agent 侧展开注入 |
| 会话搜索 | ✅ | sidebar + agent Sessions | 标题/分组过滤；pin/archive 行菜单与 show-archived 视图 |
| 消息全文搜索 | ✅ | sidebar + sessionstore SearchMessages | LIKE/转义/片段/限制/跨会话（解码文本匹配） |
| Fork | ✅ | agent SessionForker + sidebar 行菜单 | 深拷贝消息/附件/tools、持久化历史/事件 |
| Export | ✅ | session 导出 Markdown + 复制对话 + JSONL + toast | 缺跨重启导出历史（非目标） |
| Automation 页 | ✅ | `internal/ui/automation` + module | 列表/新建（含可选模型）/运行/删除/after/every/cron + 运行历史折叠/展开 + 任务模型 Badge |
| Plugins 页 | ✅ | `internal/ui/plugins` + toolkit + ModuleLister | MCP 状态/工具目录/工具级与 server 级开关/详情/连接配置/内置模块清单 + **插件参数 section（仅已接线键：web search endpoint/key、mcp opt-in、子代理编译期常量；避免死配置）**；插件市场为明确非目标 |
| Skills 页 | ✅ | settings 技能页 + skills 模块 | 发现/roots/预览/刷新 + **额外技能目录（`skills.dirs`）配置** + **发现警告（同名遮蔽/扫描上限）** + **新建/编辑（dataDir）/删除（含确认）**；workspace 技能只读展示（设计约束） |
| Memory 页 | ✅ | session Memory tab + 设置「记忆」页 | 会话工作区记忆管理（Tab 在 store 变更时自动刷新）+ 设置页**全工作区**列表/搜索/添加/编辑/删除/整理/清空/上限保留期策略 + **模型提取开关与手动提取入口（隐私/成本提示、防重复、部分失败显示已写入条数）** + **损坏文件恢复提示** |
| Workspace→Session 树/分组/计数 | ✅ | sidebar workspace tree | 多 workspace 分组、未分组、计数、搜索、折叠、置顶/归档 |
| Plan UI（plan_review/exit_plan_mode） | ✅ | interaction plan Modal + /审阅计划 + plan 门禁 + toolcards 计划卡 + **keep-planning 反馈** | Markdown 审阅 + 批准/取消 + 计划模式门禁 + transcript 计划卡 + **「继续规划」反馈（写回当前会话作为下一条用户消息 → 模型修订后重新 plan_review）** |
| Modes UI（standard/minimal/creator） | ✅ | session 模式 Popover + agent gate 策略 | 四模式：普通（原审批策略）、计划（只读+计划白名单，最高优先）、精简（只读探索白名单）、创作（隐藏编码工具，仅 ask/present/goal/memory/skill）；composer 模式菜单 + header 徽标，坏值归一，中英 |
| 智能体团队 / 子代理 | ✅ | session `agentsurface.go` + agent `childagents.go` + toolkit `jobs_service.go` | 头部三入口「智能体团队 / 子智能体 N / 后台任务 N」（Popover 浮层）；团队浮层=成员卡+共享任务卡；子代理/成员在主栏以 `父标题 › 子名` 面包屑打开，点父标题返回；一次性只读、可继续有输入框；后台任务可查看状态并停止；不占侧边栏/右栏；**teammate 与子代理共用稳定 child-session ID，记录持久化到 `child-sessions.json`，重启后以「已结束」只读历史恢复；完成回执进入父会话 transcript（live 按时间合入、失败态 danger 语义有红色预算上限），队友完成以 user 消息注入下一轮模型上下文（每队友一次；同步子代理答案已在工具结果中，不重复注入）** |
| Todo UI（共享任务） | ✅ | toolkit `KeyTaskBoard` + session 团队浮层 | 团队浮层共享任务卡（subject、状态 badge、负责人、依赖/写入范围），数据在浮层打开时快照、关闭重开刷新；与 team_task_* 共用同一 manager；空/错误态；**任务板按会话归属并持久化到 `<数据目录>/team-tasks/<session>.json`（旧全局文件首次会话工具调用时一次性迁移并退役；启动预载、连续 task-N 编号、依赖/owner/写入范围/墓碑保留、删除会话即删文件、128 文件/30 天修剪）**；剩余（后续）：工作台内任务板视图、后台自动刷新、行内 owner/状态操作（当前由工具/服务调用） |
| Artifacts / Present UI | ✅ | interaction Modal + uiroot ArtifactLister + rightpanel Files「产物」 | present 预览 + 最近 50/产物列表最近 10、内嵌 markdown 再预览 + 下载为 Markdown（原生保存对话框/slug）；跨重启历史为明确非目标（进程内 50 条） |
| 交互式 ask_user UI | ✅ | `internal/ui/interaction` question Modal + uiroot bridge | 选项/自由文本/取消；hosttools ask_user 已走真实桥，E2E 覆盖 |
| About 页 | ✅ | `internal/ui/about` | 构建信息/架构/bullet，en/zh 动态导航 |
| Welcome/start tab | ✅ | `internal/ui/home`（welcome 为演示模块） | Home 起始页覆盖旧 start tab 主路径 |

## 4. Top 剩余缺口（按用户影响排序）

1. **Composer 收尾（高，每日可见）**：/compact、/goal pause/resume/complete、plan-mode 切换、
   预设/persona 的管理页已落地（预设片段），仍缺 /compact 与 plan-mode 切换。
2. **Plan 深度（中高）**：keep-planning + 反馈修订循环、plan mode 工具策略切换、transcript plan 卡；
   当前只有批准/取消两键。
3. **消息级操作/用量**：已实现元信息行单一 ⋯（复制/重生成/反馈/用量明细）与过程折叠，无缺口。
4. **管理深度（中）**：插件详情/内置插件开关/配置；memory 后台自动提取（当前为手动触发）；
   artifact 下载动作。
5. **编排执行深度（中）**：goal 自动驱动（多轮自动继续）、review/PTC/workflow/ralph 工具接线；
   当前 subagent/team/goal 桥可用，但旧 runtime 的高级编排未全接。团队可见性 M1 已落地
   （运行态/活动/依赖/通知/命令入口）；剩余：团队工作台 overlay、后台 jobs 面板、任务板持久化，
   均为后续增量、非阻塞。
6. **宿主桌面集成（中）**：托盘/关闭到托盘（旧 close-to-hide）、窗口几何显式持久化、
   Windows 运行中替换（非目标）；当前 proxy/autostart/single-instance/install/toast 已落地。
7. **后端长尾（低）**：ACP session/list/load+auth+fs/terminal、automation runs 历史、
   importer attachments/credentials/manifest/zstd、store 旧表语义、LSP client、modes/persona。
8. **i18n 残留（低）**：host chrome tooltip、部分原生 Dialog 标题、后端错误文案、命令面板静态标题。

## 5. 结论与收口分类（Round 59 终版）

**功能面覆盖结论：旧应用的用户可见功能均已落地等价实现（✅61/62）**；唯一 ❌ 为 winproc（Windows 平台相关，明确非目标）。
此前 §5.1 列出的 13 项深度限制已全部关闭：automation 字段、LSP 多服务器+workspace opt-in、memory consolidation+extract、agent presets、
session tool spill、sessionreference URI/预算、i18n 错误分类、/compact 与 /goal 生命周期、插件参数页、ACP tool_call、skills CRUD。
非目标见 §5.2。

### 5.1 深度限制（已全部关闭）

无。原 13 项深度限制均已在本轮开发中落地并有测试/反证覆盖，验证方式见 `docs/VERIFICATION.md`。

### 5.2 明确非目标（不计入覆盖缺口）

`winproc`（Windows 控制台/编码辅助）、PTC/run_code、workflow/ralph/review/inspect/report 工具、第三方插件市场、
Windows 运行中 exe 替换、artifact 跨重启历史、旧 DSH 私有导入格式（attachments/credentials/manifest/zstd）、
ACP 客户端职责（auth/fs/terminal）、系统级全局热键以外的 OS 集成、**skills workspace 编辑**（旧实现只读，新增写权限需独立权限模型）、
**automation 独立 runs 表**（内部存储形态，用户可见运行历史已覆盖）、**插件 agent_loop budget/auto-review 实验参数**（old 自身标注 experimental）。

## 6. 对外表述建议

- 可说："已支持完整对话、工作区/文件/变更/终端检查、17 页设置（模型/诊断/快捷键/外观/技能/更新/导入/
  搜索/MCP/网络/预设/LSP/数据与隐私/通知/行为/记忆）、审批与权限模式、聊天模式（普通/计划/精简/创作）、
  右栏（团队/任务）、搜索/fork/导出（Markdown+JSONL，含反馈）、置顶归档、web search、ask_user/present/plan 审阅、
  keep-planning、真实 subagent/team 编排、per-session 模型与思考强度/自定义模式与上下文预算与最大迭代、@session 引用、
  MCP HTTP/stdio/工具级/server 级开关/连接配置、桌面单实例/autostart/install-on-restart/toast/代理/托盘/全局热键/主题三态、
  Home composer、技能编辑、记忆管理、工件下载、ACP list/load、导入/更新下载安装调度。"
- 不要声称"与 old 实现逐像素/逐内部表结构一致"：深度限制见 §5.1，非目标见 §5.2。

审计方法：`grep`/读代码核对模块注册、assembly、settings 页与 UI 构建函数；计数为本文矩阵行数
（28+34=62），非代码行数/测试数。presets 为复核时工作树 WIP（未提交），若随后落地请更新 `presets` 行与统计。
