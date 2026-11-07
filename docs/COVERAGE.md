# 旧功能覆盖审计（old/ → 当前产品，复核版）

> 复核：2026-11-08（P6）。范围：`old/internal` + `old/internal/shell` 对照当前
> `internal/modules/*`、`internal/ui/*`、`internal/assembly/assemblies/*.json`、host/uiroot。
> 只改本文档，未改代码；判定以当前工作树代码/装配为准。目标：回答"旧功能覆盖是否完成、剩余最小必须项"。

## 0. 复核摘要

- 自上一版后落地的关键能力：**web search 全链路**（`internal/websearch` + toolkit 动态 `web_search`/`web_fetch` +
  settings 搜索页，desktop/headless assembly `toolkit.network=true`）、**hosttools 真实 UI 桥**
  （uiroot `UserInteraction`/`ArtifactPresenter`/`PlanReviewer` + hosttools Asker/Presenter/Plan +
  `zeokeel-ui/interaction` 原生 Modal）、**composer 增强**（/新会话/创建副本/导出对话/打开设置、消息级重新生成、
  per-session 模型切换 + modelcatalog 预设）、**MCP 配置**（settings `mcp.servers` + settings MCP 页 +
  toolkit 启动合并；仍仅 Streamable HTTP）。
- 复核统计：62 项中 **✅26 / 🟡27 / ❌9**。核心对话/持久化/工作区/审批/设置/右栏/搜索/导入/更新检查已可用；
  真正未覆盖的集中在**编排执行（team/subagent/goal）**、**composer/plan/modes 高级控制**、
  **presets/persona 与 @session 引用**、**更新安装/桌面宿主能力**、**MCP/插件/artifact/todo 管理深度**。
- **旧功能全量覆盖仍未完成**；但已从"核心可用"推进到"核心 + 多数高级 UI 可用"，剩余缺口不再阻断日常对话。

## 1. 统计

| 状态 | 数量 | 占比 | 含义 |
|---|---:|---:|---|
| ✅ 已迁移 | 26 | 41.9% | 当前代码有对应实现，且进入 desktop/headless 装配或明确等价 |
| 🟡 部分迁移 | 27 | 43.5% | 主路径可用，但仍缺子功能/交互/执行语义/管理能力 |
| ❌ 未迁移 | 9 | 14.5% | 当前无等价实现（含系统级/明确非目标） |
| 合计 | 62 | 100% | 28 后端能力 + 34 Shell/UI 能力 |

## 2. 后端能力矩阵（28 项）

| old 能力 | 状态 | 当前实现/证据 | 差异说明 |
|---|---|---|---|
| `acp` | 🟡 | `internal/modules/acp`、host RunACP | ACP server 子集（initialize/session-new/prompt/cancel、chunk、错误码）；缺 session/list/load、auth、fs/terminal 双向、tool_call 更新 |
| `agentteam` | ❌ | hosttools `team` 工具 + rig `tools/team` 任务板 | 只有内存 roster/任务板；真实异步 subagent runner（spawn/steer/interrupt/续谈）未接 |
| `automation` | 🟡 | `internal/modules/automation` + `internal/ui/automation` | cron/every/after、持久化、ticker、schedule 工具；缺 automation_runs 历史 |
| `brand` | ✅ | `internal/brand` | palette/theme/logo/wordmark |
| `design` | ✅ | `internal/design` | tokens/style/motion/font/components 子集 |
| `globalhotkey` | ❌ | 无 | 系统级全局热键未实现；只有窗口内快捷键（uiroot） |
| `i18n` | 🟡 | uiroot locale + uikit + 各 UI 切片 | 新页面（搜索/MCP/interaction/about/rightpanel 等）已 en/zh；仍缺 host chrome tooltip、原生 Dialog 标题、后端数据文案、home/session 命令面板静态标题 |
| `icons` | ✅ | `internal/icons` | Lucide SVG |
| `importer` | 🟡 | `internal/modules/importer` + settings 导入页 | DSH session.v4.jsonl Scan/Import、文本+reasoning、workspace；attachments/credentials/manifest/zstd 未迁移 |
| `lsp` | ❌ | 无；rig 有 `tools/lsp` 但 toolkit 未注册 | 旧 LSP client/servers 配置/工具未接 |
| `mcp` | 🟡 | toolkit `mcp.Start`（stdio+HTTP）+ `mcp_settings.go` + settings MCP 页 | settings `mcp.servers` 支持 **HTTP/stdio** 行（command/args/env）、增删/校验/去重、启动时与 assembly 合并（assembly 优先）、stdio 子进程握手/工具注册/失败诊断；仍无 workspace mcp.json opt-in、工具级开关 |
| `memory` | 🟡 | `internal/modules/memory` + rig memory tool + session Memory tab | JSON MemoryStore/tool/搜索/添加；consolidate/extract/auto-memory 未迁移 |
| `modelcatalog` | 🟡 | `internal/modules/modelcatalog` + composer 模型菜单 + 推理强度 | 默认 DeepSeek Chat/Reasoner 预设、per-session 切换、**per-session reasoning effort（low/medium/high，provider reasoning_effort + settings 持久化）**；缺旧 context window/添加编辑/连接检查 |
| `presets` | ❌ | 无 | 无 agent presets/persona/tool allowlist/用户 JSON 覆盖 |
| `prompt` | 🟡 | agent 静态 SystemPrompt + settings | 无 modes/plan/memory/AGENTS.md 注入策略、auto-title/review prompt |
| `provider` | ✅ | rig `provider/openai` + agent 模块 | OpenAI Chat Completions 兼容 |
| `runtime` | 🟡 | agent + rig agent/gate/compact | 循环/审批/压缩/检查点/多模态 parts/subagent runner/**team manager（spawn/steer/wait/interrupt/close + 工具注册去重）**/goal 跟踪桥；缺 modes、review、goal 自动 driver、PTC/workflow |
| `session` | 🟡 | agent ChatService + SessionRegenerator + per-session model | 发送/取消/历史/恢复/压缩/fork/export/附件/重新生成/会话模型覆盖；缺 goal 多轮驱动、tool spill、per-message 反馈、附件服务 |
| `sessionreference` | 🟡 | composer @ 菜单 + agent references.go | **@session 引用已迁移**（@ 菜单列其它会话、token 持久化、agent 注入尾部 20 条/8000 rune 上下文、session_refs 事件、self/unknown 忽略）；缺旧候选排序/预算错误分类 |
| `skills` | 🟡 | `internal/modules/skills` + settings 技能页 | 多根 List/Read/roots/预览；缺技能管理/编辑、文件监听 |
| `state` | ✅ | ChatService 读模型 + uiroot runtime | old broker/projector 被读模型替代；UI 所需投影可用 |
| `store` | 🟡 | `internal/modules/sessionstore` | sessions/messages/events/checkpoints/search/workspace_id；旧 attachments/automation_runs/feedback/model/setting 表语义分散或未迁移 |
| `tools` | 🟡 | toolkit + hosttools + rig tools | ask_user/present/plan_review/exit_plan_mode/goal 已接真实宿主桥；**subagent/subagent_fork + team_* / spawn_teammate / list_agents / wait_agent / send_message / interrupt_agent 已接真实 rig team manager（Inspector 团队面板可 steer/中断）**；缺 workflow/ralph/review/inspect/report/lsp 注册与 tool allowlist/presets |
| `updater` | 🟡 | `internal/modules/update` + settings 更新页 | 检查（manifest/https/1MiB/semver/asset）；download/SHA256/stage/install 未迁移 |
| `websearch` | ✅ | `internal/websearch` + toolkit 动态 searcher | 旧 HTTP 协议（POST/Bearer/limit/10s/1MiB）、错误语义；assembly network=true，设置搜索页 endpoint/key 即时生效；无旧代理/多后端选择（旧也只有通用端点） |
| `winproc` | ❌ | 无 | Windows 控制台隐藏/环境/编码辅助未迁移 |
| `workspacechanges` | ✅ | workspace WorkspaceDiffer + rightpanel Changes | git status/diff、截断；用户可见等价 |
| `app`（宿主） | 🟡 | host/assembly | 窗口/设置/headless/ACP + interaction/artifact/plan 服务 + **单实例锁（OnSecondInstance 聚焦）** + **XDG autostart（外观页开关）**；缺窗口几何显式持久化（mygo StateKey 已隐式）、通知、网络代理、update install |

## 3. Shell / UI 能力矩阵（34 项）

| old Shell/UI 能力 | 状态 | 当前实现/证据 | 差异说明 |
|---|---|---|---|
| Home / 起始页 | ✅ | `internal/ui/home` | hero/greeting/setup card/composer/最近会话 |
| Composer 基础（输入/附件/发送/停止/模型 chip/@/） | 🟡 | `internal/ui/session/view.go`、`attachments.go` | 文本、附件/图片/拖放、发送/停止、模型 chip、工作区 `@`、`/` 菜单；per-session 模型菜单已接 modelcatalog |
| Composer 深度菜单/权限/plan/goal/compact | 🟡 | session `sessionCommands` + `buildComposerToolbar` + command dialogs | 已有 /新会话、/创建副本、/导出对话、/打开设置、**/设置目标 /审阅计划 /添加记忆（native 对话框）**、权限模式、推理强度、@session 引用、重新生成、per-session 模型；缺 /compact /feedback、plan-mode 模式切换入口 |
| Chrome/Sidebar/折叠 rail/右栏 dock | ✅ | uiroot + sidebar | 280px sidebar、64px rail、Cmd/Ctrl+B、右栏开关 |
| 命令面板/窗口内快捷键 | ✅ | uiroot palette/shortcuts | Cmd/Ctrl+K、可配置两条 chrome 快捷键；非系统全局热键 |
| Settings 外壳与页面 | 🟡 | `internal/ui/settings`、about | 10 页：模型/服务商/外观/快捷键/技能/诊断/更新/导入/搜索/MCP + 关于，en/zh；缺 presets、内置插件开关/详情、记忆管理页 |
| Approval 审批 Modal | ✅ | `internal/ui/approval` + approvals 模块 | allow/allow-always/deny、ask/auto/never |
| 右侧检查栏 | ✅ | rightpanel | 概览/工具/日志/文件/变更/终端，en/zh |
| Files 检查器 | ✅ | rightpanel files + workspace | 打开文件夹/面包屑/文本/Markdown/图片预览/错误态 |
| Terminal 运行器 | ✅ | rightpanel terminal + terminal 模块 | 独立 shell -c、cwd、输出/退出码/停止/超时；无 PTY/持久 shell |
| Workspace Changes/diff | ✅ | rightpanel changes + WorkspaceDiffer | 未提交变更列表、状态 badge、diff 折叠/截断 |
| Trajectory/工具时间线 | ✅ | session Trajectory tab | 轨迹卡片/工具调用时间线 |
| Tool cards | ✅ | `internal/ui/toolcards` | rig 工具卡 + demo/兜底；MCP 动态工具走 session 兜底 |
| Markdown 渲染 | ✅ | mygo ui/markdown | 消息/技能/更新 notes/artifact/plan 预览 |
| Reasoning 展示 | 🟡 | session buildAssistantMessage | 折叠 Reasoning 文本；缺旧实时 preview/shimmer |
| 消息级操作（复制/重试/反馈/用量） | 🟡 | session `buildRegenerateAction` | 最后 assistant 下有「重新生成」（SessionRegenerator）；缺逐条复制、feedback good/bad、usage/tool footer |
| `@` mentions | ✅ | session buildMentionMenu | 工作区文件 + **会话分组（@session 引用，排除当前会话/过滤/上限 8）**；agent 侧展开引用上下文 |
| 会话搜索 | ✅ | sidebar + agent Sessions | 标题/分组过滤 |
| 消息全文搜索 | ✅ | sidebar + sessionstore SearchMessages | LIKE/转义/片段/限制/跨会话 |
| Fork | ✅ | agent SessionForker + sidebar 行菜单 | 深拷贝消息/附件/tools、持久化历史/事件 |
| Export | 🟡 | session 导出 Markdown + 复制对话 + `/导出对话` | 缺旧 JSONL 会话日志导出 |
| Automation 页 | ✅ | `internal/ui/automation` + module | 列表/新建/运行/删除/after/every/cron |
| Plugins 页 | 🟡 | `internal/ui/plugins` + toolkit | MCP 状态卡片 + 工具目录；MCP 配置已移到 settings MCP 页；缺内置插件开关/插件详情/配置 |
| Skills 页 | 🟡 | settings 技能页 + skills 模块 | 发现/roots/预览/刷新；无创建/编辑/删除 |
| Memory 页 | 🟡 | session Memory tab + memory 模块 | 搜索/添加/删除 scope；无 consolidate/extract、全局记忆设置 |
| Workspace→Session 树/分组/计数 | ✅ | sidebar workspace tree | 多 workspace 分组、未分组、计数、搜索、折叠 |
| Plan UI（plan_review/exit_plan_mode/plan 卡） | 🟡 | `internal/ui/interaction` plan Modal | 审阅 Modal 显示 Markdown + 批准/取消（Escape/背景关闭按取消处理）；缺 keep-planning/feedback、plan mode 开关、transcript plan 卡 |
| Modes UI（standard/minimal/creator） | ❌ | 无 | 无模式选择/工具子集/persona 切换 |
| Team UI（成员/任务/steer） | ❌ | 无 | 只有 team 工具内存 roster/任务板，无 Team 屏/真实 subagent |
| Todo UI（会话 todo 面板） | ✅ | toolkit `KeyTaskBoard`（rig team.TaskManager 共享实例）+ rightpanel「任务」tab | 任务列表/状态排序/添加/开始/完成/删除/刷新/错误与空态，与 `team_task_*` 工具共用同一任务板；缺旧 todo 卡内联进度视图 |
| Artifacts / Present UI | 🟡 | `internal/ui/interaction` Modal + uiroot ArtifactLister + rightpanel Files「产物」 | present 预览 + 历史（最近 50，Files 内「产物」列表最近 10、内嵌 markdown 再预览）；缺下载动作/跨重启历史 |
| 交互式 ask_user UI | ✅ | `internal/ui/interaction` question Modal + uiroot bridge | 选项/自由文本/取消；hosttools ask_user 已走真实桥，E2E 覆盖 |
| About 页 | ✅ | `internal/ui/about` | 构建信息/架构/bullet，en/zh 动态导航 |
| Welcome/start tab | ✅ | `internal/ui/home`（welcome 为演示模块） | Home 起始页覆盖旧 start tab 主路径 |

## 4. 剩余缺口（按工作量小→大）

1. **S**：i18n 残留（host chrome tooltip、原生 Dialog 标题、session/home 命令面板静态标题、后端错误文案）。
2. **S**：消息级复制/feedback/usage footer；JSONL 会话日志导出。
3. **S-M**：内置插件开关与插件详情/配置页；artifact Files-tab 打开/历史；todo 面板。
4. **S-M**：plan 审阅补 keep-planning/feedback、plan mode 开关与 transcript plan 卡；modes UI。
5. **M**：composer 高级控制：/permission 访问模式、推理强度、/plan /goal /compact /feedback /memory 命令。
6. **M**：modelcatalog 补旧 context window/effort/添加编辑/连接检查；presets/persona + `@session` 引用。
7. **M-L**：MCP 补 stdio/workspace mcp.json opt-in/工具级开关；插件管理。
8. **L**：真实 subagent/agentteam runner + Team UI/steer + goal driver（goal/team 工具仍是 Memory）。
9. **L**：updater 下载/SHA256/暂存/重启替换；宿主 autostart/single-instance/窗口几何/通知/网络代理。
10. **非目标/平台**：globalhotkey、LSP client、winproc、PTC/run_code、语音、插件市场；LSP 等 rig 替代未接线，不计已覆盖。

## 5. 结论与最小必须项

**是否完成旧功能覆盖：否。** 但覆盖面已明显提升：核心对话、持久化、工作区/文件/变更/终端、设置（含搜索/MCP）、
审批、右栏、搜索/fork/导出、i18n 主要界面、自动化、web search、真实 ask_user/present/plan 审阅、
per-session 模型切换均已可用。剩余缺口以**高级编排、composer 控制、自定义 presets/引用、桌面集成与管理深度**为主。

若要以旧功能覆盖完成收口，最小必须项（≤5）：

1. **编排执行**：真实 subagent/agentteam runner、Team UI/steer、goal 自动驱动；`hosttools` 的 Goals/Team
   从 Memory 换成真实实现（涉及 `internal/modules/hosttools`、agent runtime、新 Team/Goal UI）。
2. **Composer/plan/modes 高级控制**：permission 访问模式、推理强度、plan mode + keep-planning/feedback、
   plan 卡与 modes UI，以及 /plan /goal /compact /feedback /memory 命令入口（`internal/ui/session`、`internal/ui/interaction`、agent）。
3. **presets/persona + `@session` 引用**：新增产品 presets 模块/设置页；sessionreference 移植到 composer（`internal/ui/session`）。
4. **更新安装 + 桌面宿主**：updater download/SHA256/stage/install；autostart/single-instance/窗口几何/通知/网络代理
   （`internal/modules/update`、`internal/host`）。
5. **MCP/插件/artifact/todo 管理深度**：MCP stdio/workspace mcp.json/工具级开关；内置插件开关/详情；
   artifact Files-tab 打开/历史；todo 面板（`internal/modules/toolkit`、`internal/ui/{settings,plugins,interaction}`）。

## 6. 对外表述建议

- 可说："已支持完整对话、工作区/文件/变更/终端检查、设置（含搜索/MCP）、审批、右栏、搜索/fork/导出、
  web search、ask_user/present/plan 审阅、per-session 模型切换、i18n 主要界面、import/ACP/更新检查。"
- 不要声称"old 功能全量迁移"：team/subagent/goal 执行、presets/@session、updater 安装、桌面宿主能力、
  MCP 全协议/工具开关仍未完成；globalhotkey/LSP/winproc/PTC 按非目标处理。

---

审计方法：`grep`/读代码核对模块注册与 assembly；计数为本文矩阵行数（28+34=62），非代码行数/测试数。
若并行改动继续落地，以彼时工作树重跑本矩阵。
