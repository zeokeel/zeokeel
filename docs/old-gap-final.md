# old/ → 当前实现最终缺口核对（只读审计）

审计时间：2026-11-08。范围：`old/internal/shell`、`old/internal/app`、`old/internal/*` 的用户可见面，对照当前
`internal/ui/*`、`internal/modules/*`、`internal/assets/assemblies/*.json`、`internal/host`。只读 old/ 与当前代码，
未改任何产品代码；本文档为唯一产出。既有 COVERAGE/REBUILD-STATUS 中部分状态已被后续 TDD 超越，本表以当前代码为准。
图例：✅已迁移 / 🟡等价或部分 / ❌遗漏 / ⛔明确非目标。

## 主框架与导航

| 功能面 | 状态 | 当前证据 / 差异 |
|---|---|---|
| Home 主体（hero、构造、setup card） | ✅ | `ui/home` hero、setup card、品牌启动按钮 |
| Home 输入框与工具栏 | ❌ | 旧 Home 有真 TextArea + 附件 + 权限/模型/工作区 chip + Send；当前 Home 只有“开始新对话”CTA，必须先建会话才能输入 |
| Home composer 的 workspace/模式 chip、slash/@ 与附件 | ❌ | 旧 Home composer 内含工作区/模式 chip、`/`/`@` 菜单、附件与 Send；当前 Home 没有这些入口（仅会话内 composer 支持） |
| Sidebar 会话树/搜索/消息搜索 | ✅ | `ui/sidebar` 工作区树、标题搜索、全文消息搜索 |
| Sidebar 行操作（重命名/删除/置顶/归档/fork） | ✅ | 行菜单与确认对话框已迁移 |
| Sidebar 工作区创建/切换 | 🟡 | 当前只能经右侧 Files 空态的“打开文件夹”创建；旧侧栏头部有 FolderPlus“添加工作区”与切换器 |
| Sidebar 视图选项（分组/排序/归档过滤） | ❌ | 旧 sliders 菜单有 By workspace/Workspace Tree/In one list、排序；当前仅“显示已归档”开关，树形固定 |
| Chrome 折叠/右栏 dock | ✅ | uiroot 侧栏 rail、右栏开关、拖拽宽度 |
| 托盘 | 🟡 | 当前托盘 Show/Quit + close-to-tray；旧托盘还有 New Session、Settings 两项（旧 ADR 后无菜单栏，托盘为唯一原生入口） |
| 命令面板 Cmd/Ctrl+K | 🟡 | 当前是命令面板；旧 Cmd+K 在会话内聚焦 composer、会话外打开会话搜索，语义不等价 |
| 全局热键 | ✅ | host hotkey + 外观页 GlobalShortcut 面板 |
| 窗口快捷键/可编辑 keybinding | ❌ | 旧 Cmd+N/Cmd+,/Cmd+P/Ctrl+`/Cmd+Alt+B/Esc 与完整快捷键编辑器；当前仅侧栏折叠、命令面板两条可改 |
| 原生菜单栏 | ✅ 等价 | 旧版 ADR 已退役窗口菜单栏，改托盘；当前同样无菜单栏 |
| 原生窗口几何持久化 | 🟡 | 旧显式 window-state；当前依赖 mygo StateKey 隐式持久化，无设置面 |
| i18n | 🟡 | 主 UI 中英覆盖；host chrome tooltip、部分原生 Dialog 标题、后端错误文案仍有残留 |

## 会话与 Composer

| 功能面 | 状态 | 当前证据 / 差异 |
|---|---|---|
| 对话/轨迹/记忆三 Tab、Markdown、流式 | ✅ | `ui/session` 全部迁移 |
| 复制/重新生成/反馈/用量 footer | ✅ | 复制、Regenerate、good/bad、tokens/duration/cached |
| 附件/图片/拖放 | ✅ | 按钮、file picker、native drop |
| @ 文件/@session 引用 | ✅ | 文件与会话分组、`@session:<id>` 注入与审计事件 |
| 模型切换/推理强度/权限模式 | ✅ | 模型菜单、effort、权限命令与 badge |
| 聊天模式 standard/minimal/creator | 🟡 | 已有四模式门禁与 UI；但旧 minimal=终端+图像阅读、creator=改设置/写技能，当前分别是只读白名单/隐藏编码工具，语义不同；旧模式按 session 存，当前为全局 settings |
| 权限 auto-review 模式 | ❌ | 旧 read-only/workspace/full/auto-review 四档；当前 ask/workspace/full/never，无模型自动审查档 |
| slash 命令覆盖 | 🟡 | 当前 9 条；旧 16 条。缺 /compact、/file、/clear、/stop、/help、/automate、/plugins、/plan 模式切换（部分有按钮/页面等价） |
| /goal 生命周期 | 🟡 | 当前 set/clear；旧 /goal + pause/resume/complete/abandon/edit 与多轮自动驱动未迁移 |
| /compact 手动压缩 | ❌ | agent 有自动压缩与 compactor；无 UI/命令触发当前会话立即压缩 |
| Plan 审阅 | 🟡 | 当前批准/取消；旧 Keep planning 反馈修订、Exit plan mode、plan 卡与继续执行循环未迁移 |
| 子代理/团队/共享任务板 | ✅ | subagent、team_*、rightpanel 团队/任务 tab、任务 CRUD |
| /feedback 与导出含反馈 | ✅ | 最近回答摘要 + good/bad/clear；Markdown/JSONL 含 feedback |
| 消息级旧操作（Retry/标注、completion summary、tool summary、turn marks） | 🟡 | Retry/Regenerate、usage、工具卡已有；旧 tool summary/completion summary/turn marks 等展示细节未逐项复刻 |
| reasoning 展示 | 🟡 | 折叠 Reasoning 文本；旧实时 preview/shimmer 未迁移 |

## 右栏与检查器

| 功能面 | 状态 | 当前证据 / 差异 |
|---|---|---|
| 右栏 dock/Split/Fullscreen/拖拽 | ✅ | 当前 8 tab，旧 4 tab（Start/Files/Terminal/Log）为子集 |
| Files/预览/面包屑/打开文件夹 | ✅ | 文本/Markdown/图片预览，错误态 |
| Changes/diff | ✅ | git status + diff 折叠/截断 |
| Terminal + Choose shell | ✅ | 单命令 runner、shell picker、停止/清屏 |
| Session Log | ✅ | 持久化事件日志 tab |
| 旧 Start 卡片页 | 🟡 等价 | 旧右栏 Start 的 Files/Terminal/Log 三卡被 8-tab 直接入口取代；无 Start 引导卡 |
| Overview/Tools/Agents/Tasks | ✅ 新增 | 旧没有，属增量非缺口 |
| Artifacts 预览/下载/最近历史 | ✅ | Interaction Modal + Files 产物 + 下载；跨重启历史非目标 |

## Settings 各页

| 功能面 | 状态 | 当前证据 / 差异 |
|---|---|---|
| 外壳/侧边导航/i18n/动态标题 | ✅ | 15 页：模型/模型目录/服务商/诊断/快捷键/外观/技能/更新/导入/搜索/MCP/LSP/预设/网络/关于 |
| 模型页（当前模型/BaseURL/Key/Prompt） | ✅ | 对应旧 Models 主路径 |
| 多模型/多 provider 管理 | 🟡 | 旧 Models 页可 Add/Edit/Delete/Set default 任意模型条目；当前固定 3 个 provider 预设 + 单一当前模型 + modelcatalog 简表 |
| 模型目录编辑 | ✅ | 增删/恢复默认，动态生效 |
| 外观主题/语言 | 🟡 | 当前只有深色开关 + 语言；旧有 Light/Dark/System 三卡与会话字体大小 |
| General 行为设置 | ❌ | 旧页有工作详情详细度、自动折叠时机、Send busy 行为 queue/interrupt、Performance、Show coding view、Context budget、默认权限；当前全部无设置面 |
| 通知 | ❌ | 旧有两开关（turn notification / silence while focused）；当前无设置页、无系统通知策略 |
| 数据与隐私 | ❌ | 旧数据目录查看/打开、API key 说明、从 DSH 导入入口、导出全部数据 zip、Reset settings、Delete all conversations；当前仅导入页与诊断路径，无导出全部/重置/清空会话 |
| 内置插件开关/实验性插件 | ❌ | 旧 Settings > Built-in plugins + 实验插件（subagent/web_search/agent_loop/shell/skills/mcp/lsp/teams/auto-review/devtools）；当前无页面、无 plugin.enabled 门控 |
| 内置插件详情/配置 | ❌ | 旧每插件详情与参数（web_search max_results、shell timeout、agent_loop max_iterations 等）；当前仅 MCP 服务器详情 |
| 记忆设置页 | 🟡 等价 | 旧 Settings > Memory 管理全局记忆；当前会话 Memory tab 有搜索/添加/删除/整理/清空，无独立全局页 |
| Notifications/Data/Desktop 旧独立页 | ❌ | 旧 Desktop 有 close behavior Hide/Quit + autostart + hotkey；当前外观页有 autostart/single/tray/hotkey，close behavior 变成 close-to-tray 布尔 |
| 快捷键页 | 🟡 | 参考表 + 两条 chrome 快捷键可改；旧完整编辑器多键位可改 |
| 诊断 | 🟡 | 当前只读信息 + Copy diagnostics；旧还有 Export logs zip |
| 网络 | ✅ | 代理/NO_PROXY + 重启提示 |
| 更新 | ✅ | 检查/下载/SHA/安装调度/重启，较旧更完整 |
| 导入 | 🟡 | DSH 会话/工作区 Scan/Import；旧 importer 的 attachments/credentials/manifest/zstd 未迁移 |
| 搜索 | ✅ | endpoint/API key 设置，即时生效 |
| MCP | ✅ | HTTP/stdio、workspace mcp.json opt-in、工具级开关、传输表单 |
| LSP | 🟡 | 单 server 设置页 + lsp_* 工具；旧多语言服务器/工作区配置信任语义未全量 |
| 预设 | 🟡 | prompt 片段/persona 插入 + CRUD；旧 agent preset（tools allowlist/model/systemPrompt，JSON 文件、默认 preset、会话选择）未迁移 |
| 技能 | ✅ 增强 | 发现/roots/预览/刷新 + 新建/删除 |
| 关于/服务商/网络/更新 | ✅ | 对齐 |

## 独立功能页与后端

| 功能面 | 状态 | 当前证据 / 差异 |
|---|---|---|
| 审批 Modal | ✅ | allow/allow-always/deny、队列、workspace 只读自动放行 |
| 工具卡/工具目录 | ✅ | rig 工具卡 + MCP 工具目录 + MCP 工具开关 |
| Automation | 🟡 | 列表/新建/运行/删除/启用/历史/持久化已有；旧 filters/search、默认 workspace/permission/model 字段未迁移 |
| Plugins 页 | 🟡 | MCP 卡片/详情/刷新/工具目录/工具开关已有；旧 built-in/experimental 行、Add plugin 说明、Refresh/About 按钮、插件参数未迁移 |
| 插件市场/第三方安装 | ⛔ | 旧 build 明确无第三方 registry/installer，仅内置插件；无需迁移 |
| Artifacts/present | ✅ | 预览/历史/下载 |
| 团队/任务/Goal 工具 | ✅ | team_*、team_task_*、goal get/set/clear |
| 会话 pin/archive/fork/export/JSONL | ✅ | 行菜单、导出、JSONL、反馈字段 |
| 全文搜索/消息搜索 | ✅ | sessionstore 解码文本 + LIKE |
| ACP | 🟡 | initialize/session-new/prompt/cancel/chunks；旧 session/list/load、auth、fs/terminal、tool_call 更新未迁移（非主要 UI 面） |
| Importer | 🟡 | 见 Settings 导入行 |
| memory 自动整理/extract/auto-memory | ❌ | 当前仅手动整理/清空；旧自动注入/提炼/整合未迁移 |
| skills 文件监听 | 🟡 | 当前刷新按钮；旧 watch 未迁移 |
| modelcatalog 元数据（context window/max tokens/efforts/连接检查） | 🟡 | 当前仅模型名/ID/Note/编辑 |
| session reference 排序/URI/预算错误分类 | 🟡 | 注入主路径已有，旧候选排序细节未复刻 |
| updater/tray/hotkey/modes/presets/memory/skills 平台项 | ✅/🟡 | 已在各表说明 |
| ACP advanced、Windows 控制台隐藏/运行中 exe 替换、PTC/workflow/ralph、插件远程市场、artifact 跨重启历史 | ⛔ | 旧版本身未完成/平台专属/明确非目标；不计入覆盖完成门槛 |

## 明确非目标（不计缺口）

- `winproc`（Windows 控制台隐藏/环境/编码）与 Windows 运行中 exe 替换：平台专属，当前 Linux 桌面交付不要求。
- 第三方插件市场/远程安装：旧 build 自述没有 registry/installer。
- PTC/run_code、workflow、ralph 等未在旧 shell 形成稳定用户面的高级编排：按产品后续项处理。
- ACP 的 fs/terminal 双向、auth、session/list/load：后端协议深度项，不影响主 UI 日常。
- artifact 跨重启历史：现有内存 50 条 + 导出路径；旧亦非持久产物库核心。

## 结论与最小修补建议

**是否存在阻断覆盖完成的遗漏：是（不阻断核心日常对话，但阻断“旧功能全量已迁移”的对外表述）。**
最影响用户可见完整度的是：Home 无真实输入框、Sidebar 无新建工作区/视图选项、Settings 缺 General/通知/数据隐私/内置插件页、托盘缺 New Session/Settings、快捷键编辑器退化、/compact 与 plan Keep planning/goal 生命周期缺失、Automation 表单字段与插件参数缺失。

按影响排序的最小修补（≤5，每项都可独立交付）：
1. Home 恢复真实 composer（TextArea + 附件 + 权限/模型/工作区 chip，发送时自动建会话），或至少输入+发送直达会话。
2. Settings 补 General 行为组（字体/主题三态/详细度/折叠/Send busy/Performance/Context budget/默认权限）+ 通知 + 数据隐私（导出全部/重置/清空会话）+ 内置插件开关页。
3. Composer 命令补全：/compact、/file、/clear、/stop、/help、/automate、/plugins、/plan 模式切换；plan 卡补 Keep planning/Exit plan mode；/goal 补 pause/resume/edit/complete。
4. Sidebar 加“添加工作区”与视图分组/排序菜单；托盘加 New Session/Settings；快捷键补 Cmd+N/Cmd+,/Cmd+P/Ctrl+`/Cmd+Alt+B 并恢复可编辑表。
5. Automation 表单补 workspace/permission/model 与搜索/筛选；Plugins 补 built-in/experimental 开关与插件参数详情；Memory 补全局设置页/自动整理入口。

审计结论：核心链路（会话、持久化、工作区、审批、右栏、导出、MCP、编排、反馈、模式、更新、托盘、热键）可用；但“old 功能全量覆盖”仍不成立，主要是 Settings 旧 General/管理页、Home/Sidebar 入口级 UI、plan/goal/compact 深度与插件/自动化管理面。
