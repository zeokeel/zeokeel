# 旧功能覆盖审计（old/ → 当前产品，复核版）

> 复核：2026-11-08（P7）。范围：`old/internal` + `old/internal/shell` 对照当前
> `internal/modules/*`、`internal/ui/*`、`internal/assembly/assemblies/*.json`、host/uiroot。
> 只改本文档；判定以当前代码/装配为准（presets 已随后落地，本文档已同步）。目标：回答"旧功能覆盖是否完成、剩余最小必须项"。

## 0. 复核摘要

- 自上一版后落地的关键能力：
  - **编排执行**：agent 真实 subagent runner（depth/并发上限、编排工具排除、fork/model/max-iterations
    覆盖）+ rig team manager 全量工具（team_*、legacy、DSH spawn_teammate/list_agents/wait_agent/
    send_message/interrupt_agent）+ **Agents Inspector**（成员/状态/最新回答、steer/中断/关闭）
    + **共享任务板**（team_task_* 工具 + rightpanel「任务」tab）+ goal 工具/会话 goal chip。
  - **Composer 高级控制**：/goal /plan /memory 原生对话框、**权限模式**（命令 + composer badge，
    ask/workspace/full，settings `agent.permission`）、**推理强度**（per-session low/medium/high +
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
    会话搜索/消息全文搜索、JSONL 导出、任务板、MCP 工具开关均已可用。
- **presets 已落地**：prompt 片段契约 + composer「预设/Presets」插入菜单 + 设置「预设」页 CRUD（名称/提示词/排序/
  恢复默认）+ 设置「模型目录」编辑页（modelcatalog.settings 动态读取）；旧 agent presets 的 tool allowlist 语义仍未迁移，
  故 presets 记 🟡。
- 复核统计（Round 59 终值）：62 项中 **✅38 / 🟡23 / ❌1**（Plan UI 完成 keep-planning 后升 ✅；❌ 仅 winproc 平台非目标）；
  技能新建/删除、数据与隐私页、Home composer、通知开关、工作区创建等轮次落地后更新）。核心对话/持久化/工作区/审批/设置/
  右栏/搜索/导入/更新检查继续可用；编排、composer 控制、MCP 管理、宿主桌面能力大幅补齐。
- **旧功能全量覆盖仍未完成**，但已从"核心 + 多数高级 UI"推进到"核心 + 编排 + 管理深度可用"，
  剩余缺口以 composer 收尾、plan/modes 深度、消息级操作、插件/记忆管理深度与少量平台项为主。

## 1. 统计

| 状态 | 数量 | 占比 | 含义 |
|---|---:|---:|---|
| ✅ 已迁移 | 37 | 59.7% | 当前代码有对应实现，且进入 desktop/headless 装配或明确等价 |
| 🟡 部分迁移 | 24 | 38.7% | 主路径可用，但仍缺子功能/交互/执行语义/管理能力 |
| ❌ 未迁移 | 1 | 1.6% | 当前无等价实现（含系统级/明确非目标） |
| 合计 | 62 | 100% | 28 后端能力 + 34 Shell/UI 能力 |

## 2. 后端能力矩阵（28 项）

| old 能力 | 状态 | 当前实现/证据 | 差异说明 |
|---|---|---|---|
| `acp` | 🟡 | `internal/modules/acp`、host RunACP | initialize/session-new/prompt/cancel、chunk、错误码；缺 session/list/load、auth、fs/terminal 双向、tool_call 更新 |
| `agentteam` | ✅ | agent `team.go` + rig `tools/team` + Agents Inspector + team_task 共享板 | 真实异步 subagent runner（spawn/steer/wait/interrupt/close、fork/model/max-iterations）；全局 manager（旧按会话），Inspector 展示全局成员 |
| `automation` | 🟡 | `internal/modules/automation` + `internal/ui/automation` | cron/every/after、持久化、ticker、schedule 工具、列表/新建/运行/删除/运行历史/任务级模型覆盖/**搜索与启用筛选**；缺 workspace/permission 字段、独立 runs 表 |
| `brand` | ✅ | `internal/brand` | palette/theme/logo/wordmark |
| `design` | ✅ | `internal/design` | tokens/style/motion/font/components 子集 |
| `globalhotkey` | ✅ | host hotkey.go + mygo `GlobalShortcut` + 外观设置 | 可配置系统级加速键（修饰符+单键，固定顺序规范化），注册后 Show/Restore/Focus 窗口；留空关闭、非法/注册失败仅警告、shutdown 注销；桌面不可用环境安全回退 |
| `i18n` | 🟡 | uiroot locale + uikit + 各 UI 切片 | 新增页面已 en/zh；**Round 57 审计修复：11 条 slash 命令 Title/Hint 本地化+语言切换重注册、侧栏/右栏原生目录选择器标题**；仍缺部分 host chrome tooltip、后端错误文案 |
| `icons` | ✅ | `internal/icons` | Lucide SVG |
| `importer` | 🟡 | `internal/modules/importer` + settings 导入页 | DSH session.v4.jsonl Scan/Import、文本+reasoning、workspace、symlink 跳过/重复 ID 去重；attachments/credentials/manifest/zstd 未迁移 |
| `lsp` | 🟡 | toolkit lsp.go（rig `tools/lsp`）+ 设置 LSP 页 | 可选单服务器（command/args/dir/languageId/env JSON），启动后注册 rig 默认 `lsp_*` 工具（hover/definition/references/implementation/diagnostics/symbols），卸载关闭 client；错误仅警告不阻断；缺多服务器/UI 语言服务器选择 |
| `mcp` | ✅ | toolkit `mcp.Start`（stdio+HTTP）+ settings/workspace 合并 + 工具级开关 | HTTP/stdio 握手/工具注册/失败诊断；assembly>settings>workspace mcp.json（opt-in）；`mcp.disabledTools` 注册前过滤；插件详情/市场在 Shell Plugins 行仍 🟡 |
| `memory` | 🟡 | `internal/modules/memory` + rig memory tool + session Memory tab + **设置「记忆」页** | JSON MemoryStore/tool/搜索/添加/整理/清空；设置页可全局搜索/添加/删除并显示 scope/时间；缺模型驱动 consolidate/extract |
| `modelcatalog` | 🟡 | `internal/modules/modelcatalog` + composer 模型菜单 + settings「模型目录」页 | 默认 DeepSeek Chat/Reasoner 预设、per-session 切换/推理强度、**目录编辑（增删/恢复默认，动态生效）**；缺旧 context window/max tokens/efforts 元数据、连接检查按钮 |
| `presets` | 🟡 | contracts/presets.go + session presets 菜单 + settings「预设」页（CRUD/排序/恢复默认） | prompt 片段/人设模板已迁移并可在 composer 插入；旧 agent presets 的 systemPrompt/model/tools allowlist JSON 语义未迁移 |
| `prompt` | 🟡 | agent SystemPrompt + settings 模型页提示词 + **AGENTS.md 注入** | 系统提示可配置；**每次运行读取当前工作区 `AGENTS.md`（文件名可配/32KiB 截断/读取失败告警）追加注入**；auto-title 已按首条提示生成；缺 review prompt |
| `provider` | ✅ | rig `provider/openai` + agent 模块 | OpenAI Chat Completions 兼容 |
| `runtime` | 🟡 | agent + rig agent/gate/compact | 循环/审批/压缩/检查点/多模态 parts/subagent runner/team manager/goal 工具与 goal 桥；缺 modes、review、goal 自动驱动、PTC/workflow/ralph |
| `session` | 🟡 | agent ChatService + Regenerate/Fork/Archive/Pin/ModelSwitcher + usage + 排队发送 | 发送/取消/历史/恢复/压缩/fork/export/附件/重新生成/会话模型/推理强度/turn usage/运行中排队发送/**消息字号（小/标准/大，即时生效）**；缺 goal 多轮自动执行、tool spill |
| `sessionreference` | 🟡 | composer @ 菜单 + agent `references.go` | token 持久化、注入尾部 20 条/≤8000 rune、session_refs 事件、self/unknown/重复忽略、分隔符解析；缺旧候选排序细节/URI 语义/预算错误分类 |
| `skills` | 🟡 | `internal/modules/skills` + settings 技能页 | 多根 List/Read/roots/预览/刷新 + **新建（dataDir/skills，frontmatter/校验）与删除（仅 dataDir，realpath 校验）**；缺 workspace 技能编辑、文件监听 |
| `state` | ✅ | ChatService 读模型 + uiroot runtime | old broker/projector 被读模型替代；UI 所需投影可用 |
| `store` | 🟡 | `internal/modules/sessionstore` | sessions/messages/events/checkpoints/search/workspace_id；旧 attachments/automation_runs/feedback/model 表语义分散或未迁移 |
| `tools` | 🟡 | toolkit + hosttools + agent + rig tools | 文件/执行/编辑/图像/终端/jobs/memory/schedule/worktree/skills/settings/web/MCP/session/subagent/subagent_fork/team_*/spawn_teammate/list_agents/wait_agent/ask_user/present/plan_review/exit_plan_mode/goal/team_task_* 已注册；缺 workflow/ralph/review/inspect/report/lsp/ptc 与 tool allowlist/presets |
| `updater` | ✅ | `internal/modules/update`（check/download/install）+ settings 更新页 + host apply | manifest/https/1MiB/semver/asset；下载流式+SHA/size 校验+原子暂存+install 调度+启动替换；Windows 运行中 exe 无法替换（保留 plan，非目标） |
| `websearch` | ✅ | `internal/websearch` + toolkit 动态 searcher | POST/Bearer/limit/10s/1MiB/错误语义；assembly network=true，设置 endpoint/key 即时生效 |
| `winproc` | ❌ | 无 | Windows 控制台隐藏/环境/编码辅助未迁移（平台非目标） |
| `workspacechanges` | ✅ | workspace WorkspaceDiffer + rightpanel Changes | git status/diff、截断；用户可见等价 |
| `app`（宿主） | 🟡 | host/assembly | 窗口/设置/headless/ACP + interaction/artifact/plan/notifier 服务 + 单实例锁 + XDG autostart + install-on-restart + toast overlay（设置可关闭）+ 网络代理 + 系统托盘（显示/新建会话/设置/退出）/关闭到托盘 + 全局热键 + **主题三态（浅/深/跟随系统）**；窗口几何由 mygo StateKey 隐式持久化；缺 Windows 运行中替换 |

## 3. Shell / UI 能力矩阵（34 项）

| old Shell/UI 能力 | 状态 | 当前实现/证据 | 差异说明 |
|---|---|---|---|
| Home / 起始页 | ✅ | `internal/ui/home` | hero/greeting/setup card/**真实 composer（多行输入、Enter 发送、附件选择、工作区/模型/模式只读 chip、发送后建会话并进入）**/最近会话 |
| Composer 基础（输入/附件/发送/停止/模型 chip/@//） | ✅ | `internal/ui/session/view.go`、`attachments.go` | 文本、附件/图片/拖放、发送/停止、per-session 模型菜单、工作区 @、/ 菜单 |
| Composer 深度（/goal /plan /memory/权限/effort/@session/presets） | 🟡 | `sessionCommands` + `command_dialogs.go` + `commands_help.go` + `buildComposerToolbar` | 已落地 12 条：/新会话 /创建副本 /导出对话 /打开设置 /设置目标 /审阅计划 /添加记忆 /反馈 /停止 /附加文件 /帮助 + 权限模式、模式切换（普通/计划/精简/创作）、推理强度、@session、预设插入；缺 /compact、/goal 子命令 |
| Chrome/Sidebar/折叠 rail/右栏 dock | ✅ | uiroot + sidebar | 280px sidebar、64px rail、Cmd/Ctrl+B、**Cmd/Ctrl+N 新建会话、Cmd/Ctrl+, 设置、Cmd/Ctrl+K 命令面板**、右栏开关、添加工作区、视图选项（排序/分组持久化）、工作区搜索/置顶/归档 |
| 命令面板/窗口内快捷键 | ✅ | uiroot palette/shortcuts | Cmd/Ctrl+K、可配置两条 chrome 快捷键；非系统全局热键 |
| Settings 外壳与页面 | 🟡 | `internal/ui/settings`、about | 17 页：…/**行为（默认代理权限、消息字号、运行中排队发送）**/数据与隐私（含反馈统计导出）；缺内置插件参数页/市场 |
| Approval 审批 Modal | ✅ | `internal/ui/approval` + approvals 模块 | allow/allow-always/deny、ask/workspace/full/never（settings 覆盖、workspace 只读自动 allow） |
| 右侧检查栏 | ✅ | rightpanel | 8 tab：概览/工具/日志/文件/变更/终端/**团队**/**任务**，en/zh |
| Files 检查器 | ✅ | rightpanel files + workspace | 打开文件夹/面包屑/文本/Markdown/图片预览/错误态 |
| Terminal 运行器 | ✅ | rightpanel terminal + terminal 模块 | 独立 shell -c、cwd、输出/退出码/停止/超时；无 PTY/持久 shell |
| Workspace Changes/diff | ✅ | rightpanel changes + WorkspaceDiffer | 未提交变更列表、状态 badge、diff 折叠/截断 |
| Trajectory/工具时间线 | ✅ | session Trajectory tab | 轨迹卡片/工具调用时间线 |
| Tool cards | ✅ | `internal/ui/toolcards` | rig 工具卡 + todo/team_task 卡 + demo/兜底；MCP 动态工具走 session 兜底 |
| Markdown 渲染 | ✅ | mygo ui/markdown | 消息/技能/更新 notes/artifact/plan 预览 |
| Reasoning 展示 | 🟡 | session buildAssistantMessage | 折叠 Reasoning 文本；缺旧实时 preview/shimmer |
| 消息级操作（复制/重试/反馈/用量） | ✅ | session 行动区 + usage footer + `SessionFeedbacker` + /feedback + 导出 | 「重新生成」+ 逐条复制 + usage footer + feedback good/bad + **/反馈 命令（摘要/三按钮）**，**Markdown/JSONL 导出含反馈** |
| `@` mentions | ✅ | session buildMentionMenu + agent references | 工作区文件 + 会话分组（@session，排除当前/过滤/上限 8）；agent 侧展开注入 |
| 会话搜索 | ✅ | sidebar + agent Sessions | 标题/分组过滤；pin/archive 行菜单与 show-archived 视图 |
| 消息全文搜索 | ✅ | sidebar + sessionstore SearchMessages | LIKE/转义/片段/限制/跨会话（解码文本匹配） |
| Fork | ✅ | agent SessionForker + sidebar 行菜单 | 深拷贝消息/附件/tools、持久化历史/事件 |
| Export | ✅ | session 导出 Markdown + 复制对话 + JSONL + toast | 缺跨重启导出历史（非目标） |
| Automation 页 | ✅ | `internal/ui/automation` + module | 列表/新建（含可选模型）/运行/删除/after/every/cron + 运行历史折叠/展开 + 任务模型 Badge |
| Plugins 页 | 🟡 | `internal/ui/plugins` + toolkit | MCP 状态卡片 + 工具目录 + MCP 工具级开关；缺内置插件开关、插件详情/配置、市场 |
| Skills 页 | 🟡 | settings 技能页 + skills 模块 | 发现/roots/预览/刷新；无创建/编辑/删除 |
| Memory 页 | 🟡 | session Memory tab + memory 模块 | 搜索/添加/删除 scope；无 consolidate/extract、自动记忆设置 |
| Workspace→Session 树/分组/计数 | ✅ | sidebar workspace tree | 多 workspace 分组、未分组、计数、搜索、折叠、置顶/归档 |
| Plan UI（plan_review/exit_plan_mode） | ✅ | interaction plan Modal + /审阅计划 + plan 门禁 + toolcards 计划卡 + **keep-planning 反馈** | Markdown 审阅 + 批准/取消 + 计划模式门禁 + transcript 计划卡 + **「继续规划」反馈（写回当前会话作为下一条用户消息 → 模型修订后重新 plan_review）** |
| Modes UI（standard/minimal/creator） | ✅ | session 模式 Popover + agent gate 策略 | 四模式：普通（原审批策略）、计划（只读+计划白名单，最高优先）、精简（只读探索白名单）、创作（隐藏编码工具，仅 ask/present/goal/memory/skill）；composer 模式菜单 + header 徽标，坏值归一，中英 |
| Team UI（成员/任务/steer） | ✅ | rightpanel Agents/Tasks Inspector + team_* 工具 | 成员列表/状态/最新回答、选中后 steer/中断/关闭；spawn 由 agent 工具发起（同旧）；任务板增删改/状态排序 |
| Todo UI（会话 todo 面板） | ✅ | toolkit `KeyTaskBoard` + rightpanel「任务」tab | 任务列表/状态排序/添加/开始/完成/删除/刷新/错误与空态，与 team_task_* 共用；缺旧 todo 卡内联进度 |
| Artifacts / Present UI | 🟡 | interaction Modal + uiroot ArtifactLister + rightpanel Files「产物」 | present 预览 + 最近 50/产物列表最近 10、内嵌 markdown 再预览 + **下载为 Markdown（原生保存对话框/slug）**；缺跨重启历史 |
| 交互式 ask_user UI | ✅ | `internal/ui/interaction` question Modal + uiroot bridge | 选项/自由文本/取消；hosttools ask_user 已走真实桥，E2E 覆盖 |
| About 页 | ✅ | `internal/ui/about` | 构建信息/架构/bullet，en/zh 动态导航 |
| Welcome/start tab | ✅ | `internal/ui/home`（welcome 为演示模块） | Home 起始页覆盖旧 start tab 主路径 |

## 4. Top 剩余缺口（按用户影响排序）

1. **Composer 收尾（高，每日可见）**：/compact、/feedback、/goal pause/resume/complete、plan-mode 切换、
   预设/persona 的管理页已落地（预设片段），仍缺 /compact /feedback 与 plan-mode 切换。
2. **Plan 深度（中高）**：keep-planning + 反馈修订循环、plan mode 工具策略切换、transcript plan 卡；
   当前只有批准/取消两键。
3. **消息级操作/用量（低）**：已实现逐条复制与 usage footer；仅缺 feedback good/bad。
4. **管理深度（中）**：插件详情/内置插件开关/配置；memory consolidate/extract/自动记忆设置；
   skills 创建/编辑/删除；artifact 下载动作。
5. **编排执行深度（中）**：goal 自动驱动（多轮自动继续）、review/PTC/workflow/ralph 工具接线；
   当前 subagent/team/goal 桥可用，但旧 runtime 的高级编排未全接。
6. **宿主桌面集成（中）**：托盘/关闭到托盘（旧 close-to-hide）、窗口几何显式持久化、
   Windows 运行中替换（非目标）；当前 proxy/autostart/single-instance/install/toast 已落地。
7. **后端长尾（低）**：ACP session/list/load+auth+fs/terminal、automation runs 历史、
   importer attachments/credentials/manifest/zstd、store 旧表语义、LSP client、modes/persona。
8. **i18n 残留（低）**：host chrome tooltip、部分原生 Dialog 标题、后端错误文案、命令面板静态标题。

## 5. 结论与最小必须项（Round 60 更新）

**是否完成旧功能覆盖：否（功能面基本齐、深度未齐）。** 当前 62 项：**✅37 / 🟡24 / ❌1**（❌ 仅 winproc，Windows 平台非目标）。
旧应用的主要用户可见面——首页/侧栏/会话/右栏/审批/工具卡/设置/搜索/导入导出/MCP/计划/目标/团队/工件/自动化/更新/
托盘/热键/模式/presets/memory/skills/反馈/用量——都已有可用实现；剩余 🟡 集中在交互深度与少数长尾，
不再阻断日常对话、编排与管理主路径。

从 Round 48 的「最小必须项」出发，已完成：
1. Composer 收口：presets 入口（/预设 菜单）、/反馈、模式切换（普通/计划/精简/创作）、/停止 /附加文件 /帮助（共 12 条命令）；
2. Plan 深度：计划模式门禁 + plan 审阅 Modal + 计划徽标（keep-planning 修订循环仍未做）；
3. 消息级操作：逐条复制、usage footer、feedback good/bad + 导出含反馈；
4. 管理深度：插件 MCP server 详情/工具级开关、Memory 整理/清空、Skills 新建/删除、工件下载；
5. 宿主桌面：托盘/关闭到托盘、全局热键、安装重启、网络代理、通知开关、窗口几何（mygo StateKey 隐式）。

**剩余 Top（按用户可见度）**：
1. Plan keep-planning/反馈修订循环与 transcript plan 卡；
2. Settings 旧 General 行为项（字号/主题三态/默认权限展开等）与通知页深度（通道/静默）；
3. 自动化 workspace/permission/model 字段与筛选、Plugins built-in/实验开关与参数页；
4. 会话级「全部会话」UI（当前在数据与隐私页批量导出/清除）、工作区视图分组/排序；
5. 托盘「新建会话/设置」入口、更多可编辑窗口快捷键（Cmd+N/Cmd+,/Cmd+P 等）；
6. ACP 长尾（session/list、fs/terminal/auth 深度）、artifact 跨重启历史、i18n 残留。

**明确非目标**：`winproc`（Windows 控制台/编码辅助）、PTC/run_code、workflow/ralph 旧全量、插件市场、
Windows 运行中 exe 替换、artifact 跨重启历史。`globalhotkey` 与可选 LSP 已在 Round 55 落地。
审计方法：`grep`/读代码核对模块注册、assembly、settings 页与 UI 构建函数；计数为本文矩阵行数
（28+34=62），非代码行数/测试数。presets 为复核时工作树 WIP（未提交），若随后落地请更新 `presets` 行与统计。
