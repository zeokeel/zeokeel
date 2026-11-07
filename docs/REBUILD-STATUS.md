# ZeoKeel 重建状态（Rig v0.13.0 · old/ 对照）

> 截至 2026-11-07（goal round 10）。目标：按 old/ 功能面重建，UI 全部 mygo 原生控件。

## 已落地（可运行 / 可验证）

**内核与宿主**
- kernel 编译期模块装配（`internal/assembly/registry.go` + `assemblies/{desktop,headless}.json`）
- 产品布局：`cmd/zeokeel` 薄 main → `internal/host` → 模块；rig v0.13.0 作为 agent 内核
- desktop 15 / headless 6 实例；`go test ./...` + `-race` 18 包全绿；xvfb 真窗口冒烟通过

**设计体系（从 old 原样移植）**
- `internal/brand`（palette/theme/logo）、`internal/design`（tokens/字阶/间距/组件样式/motion）、
  `internal/icons`（Lucide SVG）
- 宿主 chrome：280px 侧栏 + 64px brand row + Cmd/Ctrl+B 64px 图标 rail；无全局顶栏，屏幕自带头部；
  右侧检查栏为窗口级 docked 面板

**P3 工作区 + Files 检查器（2026-11-07 晚）**
- `zeokeel/workspace`：workspaces.json 注册表（Create/Open/Delete/current 持久化）、沙箱 Files/ReadFile/
  Search（绝对路径/`..`/symlink 逃逸拒绝，深度与 limit）、原子写；desktop 17 / headless 8 实例
- 右侧栏「文件」Tab：打开文件夹（mygo 原生 Directory picker）、目录进入/面包屑、文本/Markdown/图片预览、
  刷新与错误状态；E2E 覆盖工作区创建 + 列表 + 预览帧

**TDD-proxy/mcptoggle + 反证第三轮（2026-11-08）**
- 网络代理：host `proxyEnv/applyProxySettings`（http/https 校验、HTTP(S)_PROXY 大小写键、NO_PROXY、仅清理本进程设置过的 key；
  desktop/headless 启动调用）+ 设置「网络」页（代理/NO_PROXY 字段、校验、重启生效）；host 50 / settings 80 测试
- MCP 工具级开关：toolkit 按 `mcp.disabledTools` 过滤 MCP 工具/资源工具（坏 JSON warning）；plugins 页每个 MCP 工具 Toggle
  （写入/还原 disabled 列表、重启提示、失败回滚）；toolkit 36 / plugins 13 测试
- 反证第三轮：internal/regression 新增 35 例（98→133，0 SKIP）覆盖 install(module/host)/toast/workspace-mcp/refs；发现
  P3-9/10/11（symlink 包含、宿主 SHA 未复检、toast 过期未 Dismiss）已全部修复并加回归单测

**TDD-install/toast/wsmcp（2026-11-08）**
- install-on-restart：update `UpdateInstaller`（pending.json 原子写、path 包含/sha 复核）、host 启动 `applyPendingUpdate`
  （可注入 ExecutableReplacer，Linux 同目录 temp+rename；失败保留 pending；desktop/headless 都应用）、设置更新页「安装并在下次启动生效」+
  待安装 Badge（update 29/host 44/settings 74 测试）
- 通知 overlay：uiroot Notifier（notice-N/空文本不投递/panic 遏制）、host 桌面提供、新模块 `zeokeel-ui/toasts`（overlay.toasts、
  4 条上限、级别 Badge、4s 自动消失、关闭按钮、i18n；位于 composer 上方 140 DIP）；session 导出成功/失败接 toast；
  desktop 27 实例 / registry 29；E2E Notify→帧显示
- workspace mcp.json opt-in：toolkit 仅在设置开关开启且有当前工作区时读 `<root>/mcp.json`（对象/数组、assembly>settings>workspace
  去重），设置 MCP 页 Toggle；toolkit 33 / settings 74 测试

**TDD-artifact/desktop（2026-11-08）**
- 工件历史：uiroot artifactService 保留最近 50（ArtifactLister，newest-first 深拷贝、CreatedAt、panic/nil 也记录）；rightpanel Files
  「产物」section 最近 10 条 + 内嵌 markdown 预览/收起；E2E present→Files 产物→预览
- 桌面集成：单实例锁（mygo RequestSingleInstanceLock + OnSecondInstance 聚焦/恢复；cfg 默认 on、可关）、XDG autostart
  （`<config>/autostart/zeokeel.desktop`，Exec 转义/权限/幂等/禁用删除，启动+退出同步）；外观页「桌面集成」两个 Toggle；
  host 32 / settings 66 测试；截图 p2-artifact-history.png

**TDD-mcp/todo（2026-11-08）**
- MCP stdio：toolkit 按 `MCPServerConfig` 行分派 stdio（`mcp.Start` Command/Args/Env、握手/tools/resources 注册）与 HTTP；
  command+args 去重、stdio 优先、失败诊断条目；settings MCP 页新增传输 Select（HTTP/stdio）、命令/参数/环境字段、校验与
  旧 JSON 兼容（toolkit 29 / settings 62 测试）
- 共享任务板：toolkit 单一 `team.NewTaskManager()` 同时驱动 `team_task_*` 工具与 `contracts.KeyTaskBoard`；rightpanel
  第 8 tab「任务」：状态排序/添加/开始/完成/删除/刷新/错误/空态/i18n（rightpanel 56 测试）；E2E 创建→面板→完成
- 截图 p2-tasks.png、p2-mcp-settings.png（含 Transport 表单）

**TDD-cmd/upd + 反证扩展（2026-11-08）**
- composer 命令族：/设置目标、/审阅计划、/添加记忆（native Modal；服务可选；ctx.Effect 注销；i18n；session 85 测试）
- 更新下载：`contracts.UpdateDownloader` + update 模块流式下载/SHA-256/size 校验/`<dataDir>/updates` 暂存/路径穿越防护/并发；
  设置更新页「下载更新」按钮、下载中禁用、已下载面板（版本/大小/SHA/路径）与手动安装说明（update 22 测试 / settings 55 测试）
- 反证扩展：internal/regression 新增 20 例覆盖 team/@session/effort/permission（98 PASS / 0 SKIP），发现 4 个 P3 并已修复
  （TeamService.Wait 最新结果、引用解析分隔符/边界、permission never 映射）

**TDD-refs：@session 引用 + 推理强度（2026-11-08）**
- agent：`ParseSessionReferences` 展开被引用会话尾部 20 条/8000 rune 上下文，仅注入本次 run 输入；持久化/read model 保留原始 prompt；
  `session_refs` 审计事件；self/unknown/去重/预算反证
- reasoning effort：agent Config/每会话 `SessionEffortSettingKey`（low/medium/high，非法→空）→ openai `DefaultReasoningEffort` +
  `Request.Extra["reasoning_effort"]`；Send/Regenerate/子代理/team 全链路
- UI：composer @ 菜单新增「会话」分组（排除 active/过滤/上限 8，插入 `@session:<id>`）；工具栏「推理：默认/低/中/高」
  菜单写 settings；i18n
- E2E：引用 token 持久化原文、effort=high 发送完成；截图 p2-sessionrefs.png

**TDD-team：编排与团队 UI（2026-11-08）**
- agent `TeamService`（rig team.Manager：Spawn/Send/SendAsync/Wait/Interrupt/Close/Members + Last 结果；惰性 dynamic runner、
  MaxDepth=1/MaxConcurrent=4、递归工具排除、dispose CloseAll）+ 模型工具注册（team_*/spawn_teammate/list_agents/wait_agent/
  send_message/interrupt_agent，按 registry 重名跳过同步 subagent）；agent 测试 65
- rightpanel 第 7 tab「团队」：成员列表（name/id/status/Last）、选中 steer 输入 SendAsync、中断/关闭、空/错误态、i18n；
  rightpanel 45 测试
- E2E：spawn_teammate → 真实子代理产出 Last → Inspector 团队面板显示成员/状态；截图 p2-team.png

**TDD 并行推进 + 反证审计修复（2026-11-08）**
- TDD-1 subagent 同步委派：agent 模块提供 `contracts.Subagents`（rig agent.NewRunner 嵌套、递归排除编排名、fork/模型/迭代覆盖），
  注册 `subagent`/`subagent_fork` 工具；agent 测试 +6
- TDD-2 permission 权限模式：approvals 动态读 `agent.permission`（ask/workspace/full→auto），workspace 只读工具免问；
  composer 「权限模式」slash 命令循环 + Badge（询问/工作区/全部允许）；approvals +11/session +10 测试
- 反证审计 `internal/regression`（78 顶层 Test，7 区，-race 10s）：发现 4 个 P2 + 4 个 P3，全部 TDD 修复，探针 0 SKIP/92 PASS
  （搜索文本语义、importer symlink 逃逸、update nil ctx、uiroot handler panic 遏制、JSONL 文案/i18n、MCP 去重、预览计数）
- E2E：subagent 工具返回真实子代理回答；permission full 自动放行不排队、恢复 ask

**Round 39 最终验证（2026-11-08）**
- build/vet OK；32 测试包（含 -race）全绿；CGO_ENABLED=0 UI 定向绿
- desktop（xvfb，26 实例 / 4 screens）、headless（14 实例）、ACP（initialize/session-new，exit 0）冒烟通过
- ui-snapshot 47 步全部渲染无错误；docs/screenshots 60 个 PNG
- 证据与剩余缺口见 [docs/VERIFICATION.md](VERIFICATION.md) / [docs/COVERAGE.md](COVERAGE.md)

**P5 goal 跟踪（2026-11-08）**
- contracts.GoalTracker + uiroot goalService（Set/Get/Clear/handler/atomic ID/并发）+ host 桌面提供 +
  hosttools GoalStore 桥接（无服务 Memory 保持）
- session header：目标 Badge + 文本（单行省略/Tooltip）+ 清除目标按钮（handler 刷新/i18n/无服务不渲染）
- E2E：goal 工具 set → header 显示 → 清除后 tracker Get ok=false；截图 p2-goal.png

**P5 JSONL 导出 + 覆盖复核（2026-11-08）**
- session header「导出 JSONL」：session 头行 + message 行（reasoning/error/tools 扁平化），ExportJSONLSaver seam；
  单测覆盖内容/行数/已导出状态
- COVERAGE 复核：62 项 → ✅26 / 🟡27 / ❌9；websearch 升 ✅，hosttools ask_user ✅ / present+plan 🟡，
  composer/MCP/presets 等升 🟡；Top5 缺口更新（编排执行、composer 高级控制、presets/@session、桌面集成、管理深度）

**P5 MCP 用户配置（2026-11-08）**
- contracts.SettingMCPServers（mcp.servers JSON [{name,url}]）+ toolkit 启动合并（assembly 优先/URL 去重/非法行 warning/
  坏 JSON 不失败）
- 设置「MCP」页：列表/移除/添加（name+url 校验、重复拒绝、顺序保持、坏 JSON 提示、保存回滚、i18n）；
  E2E 渲染断言；截图 p2-mcp-settings.png

**P5 present 工件 + plan review（2026-11-08）**
- contracts：ArtifactPresenter（Present→artifact id + handler）、PlanReviewer（Review/Exit/State 状态机）+ keys
- uiroot：artifactService（artifact-N/handler/trim 校验/ctx）与 planService（idle→reviewing→approved/cancelled；
  approve/cancel 仅 reviewing 有效；每次状态变化通知）；host 桌面提供两服务；hosttools 用 PresenterFunc/uiPlanController
  桥接（interactive=false 仍保留 present/plan）
- interaction UI：统一 FIFO（question|artifact|plan）overlay.dialogs；工件 Modal（markdown Scroll + 关闭）、
  计划 Modal（计划待审阅 + 批准/取消 + 错误保留）、Esc/遮罩语义、i18n、dispose 清理
- E2E：present 工具→覆盖层→关闭；plan_review→覆盖层→批准→State=approved；截图 p2-artifact/p2-plan

**P5 ask_user 真实交互（2026-11-08）**
- contracts.UserInteraction + uiroot 实现（handler/pending map/atomic ID；Ask 阻塞至 Answer/Cancel/ctx；
  无 handler 错误；并发安全）
- 桌面 host provideServices 提供 KeyUserInteraction；hosttools Module 激活时可选桥接为真实 Asker（无则 MemoryAsker）
- 新 UI 模块 zeokeel-ui/interaction：SlotOverlayDialogs 原生 Modal（问题文本/选项按钮/自由文本/FIFO/Esc→Cancel/i18n，
  dispose 清 handler+cancel 全部）
- E2E：toolkit.Invoke("ask_user") 阻塞 → 帧出现对话框 → 点选项 → 返回 user answered: sqlite；截图 p2-ask.png

**P5 web search（2026-11-08）**
- internal/websearch 移植（Config/NewSearcher/Search：POST JSON、Bearer、limit clamp、10s/ctx、1MiB、错误语义）
- toolkit：network=true 注册 web_fetch；有 KeySettings 时动态 web_search（每次调用读 web.search.endpoint/apiKey，
  无 endpoint 报可读错误，配置变更即时生效）；desktop/headless assembly network=true
- 设置「搜索」页：endpoint/API key（write-only 保留）、非法 URL 校验、已保存/未配置 Badge、即时生效说明；
  E2E 保存后 settings 键写入；plugins 工具目录出现 web 工具；截图 p2-search-settings.png

**P5 模型切换 + 模型目录（2026-11-08）**
- modelcatalog 模块：默认 deepseek-chat/deepseek-reasoner 预设，Config 覆盖，Validate/副本语义
- agent per-session 模型：SessionModelSwitcher（settings session.<id>.model；effective model 进 ChatSession.Model；
  Send/Regenerate 使用会话模型；restore 后自动生效）+ 5 个新测试
- composer 模型 chip：可交互 PopoverMenuButton（catalog 菜单/当前打勾/Note/打开模型设置回退/i18n）；7 个新测试；
  E2E 切换后 Session().Model=deepseek-chat；截图 p2-modelmenu.png/p2-model-switched.png

**P5 composer 深度第一切片（2026-11-08）**
- session slash 命令（register/unregister via ctx.Effect）：新会话、创建副本（SessionForker）、导出对话（ExportSaver）、
  打开设置；`/` 菜单与命令面板共用 UIRuntime.Commands
- agent `SessionRegenerator.Regenerate`：保留最后 user（含附件），丢弃其后 assistant/tool 消息，ReplaceHistory 持久化
  后复用 run 管线重新生成（running/notify/事件；失败回滚）；session 末尾「重新生成」按钮（非运行/最后 assistant/
  空回复禁用，错误 DangerText）
- E2E：`/` 菜单出现 4 命令并执行「打开设置」；agent 6 个 + session 7 个新测试；截图 p2-slash.png

**P4 host chrome i18n + 覆盖审计（2026-11-08）**
- host uiroot：命令面板（标题/搜索框/空态/Esc 提示）、右侧栏按钮、折叠侧栏按钮按 locale 每帧本地化
- docs/COVERAGE.md：旧功能 62 项矩阵（✅24/🟡23/❌15）+ top5 缺口 + 结论（核心闭环完成，深度功能仍缺）

**P4 i18n 第三切片（2026-11-08）**
- about：内容 + 页面导航标题语言变化 re-register（Config.title 优先）；rightpanel：Inspector/6 tabs/overview/
  tools/log/files/changes/terminal 全部文案（后台输出也按当前 locale）；automation/plugins/toolcards/approval
  页与组件全部 en/zh；E2E 增加 About/Automation 英文断言与 zh 恢复
- 仍缺：host chrome tooltips（打开右侧栏/折叠侧边栏）、原生 Dialog 标题（导出/选目录）、后端数据文案、
  demo tool 卡、home 命令面板静态标题

**P4 i18n 第二切片（2026-11-08）**
- settings：全部 8 个页面 + 浮层标题，每帧 uikit.T；页面标题语言变化时 unregister/re-register SettingsPageSpec
  （Section 稳定 ID 保排序/默认页，导航同帧更新）；诊断新增 Copied 提示；更新/导入异步结果英文
- home：hero/greeting/setup card/composer placeholder/主按钮 en/zh；sidebar screen 导航标题 Home/Automation/
  Plugins 显示层映射（未知 ID 回退注册 ID，未改 contracts）
- assembly 去掉显式中文 settings title；E2E en 设置导航 + 中文恢复，截图 p2-english-settings.png
- 仍缺：About 页（internal/ui/about）、右侧栏、automation/plugins 页面、toolcards/approval、home 命令面板静态标题

**P4 i18n 第一切片（2026-11-08）**
- `internal/uikit/locale.go`：T(locale, zh, en) / Language
- sidebar：新对话/搜索/工作区/未分组/消息搜索/空态/行菜单/重命名/删除确认/Badge/Footer/显示归档全部 en/zh
- session：tabs/More 菜单/运行状态/导出状态/对话框标题/composer（placeholder/发送/停止/附件/提示/附件错误/@与/
  菜单）/transcript 空态/Reasoning/工具状态/轨迹/记忆 全部 en/zh
- 动态切换（SetLanguage 下一帧生效）；assembly 移除 sidebar 显式中文 labels 与 session 的 emptyText/
  composerPlaceholder/sendLabel/stopLabel，让本地化默认值生效；E2E 断言 en→zh 往返；截图 p2-english.png
- 未覆盖：其他模块注册的 screen 导航标题（首页/自动化/插件）、设置页、home、automation/plugins 页面（后续切片）

**P4 ACP 适配器（2026-11-08）**
- `zeokeel/acp` 模块：newline-framed JSON-RPC 2.0，initialize/session-new/session-prompt（Subscribe 增量
  agent_message_chunk，end_turn/cancelled）/session-cancel，错误码与 EOF/ctx 取消；后端可继续用同一 ChatService
- host `--acp`（RunACP）：加载 headless assembly、Require KeyACP、stdio Serve；desktop/headless 计数 24/14，
  registry 26；CLI 冒烟：initialize→session/new→未知方法 -32601→EOF 退出

**P4 DSH 导入（2026-11-08）**
- `zeokeel/importer`：DSH session.v4.jsonl 解析（user source.kind=user、assistant visible text+reasoning、
  context/tool/image 跳过计数）、Scan 预览（sessions/messages/workspaces/skipped/notes）、Import 写 store
  （dsh-<id> 幂等、标题首条用户消息、ReplaceHistory、workspace 注册、import 事件）；zstd/附件仅计数说明
- 设置「导入」页：DSH 目录输入 + 原生浏览 + 扫描预览 + 导入结果/错误；E2E 覆盖扫描→导入→重启可见→侧栏

**P2 拖放附件（2026-11-08）**
- 宿主窗口 `OnFileDrop` → `FileDropService.Dispatch` → 会话模块处理器 staging（复用 picker 的
  stageAttachmentFiles）；uiroot 服务单测 + session frame test + desktop E2E + 截图

**P4 更新检查（2026-11-08）**
- `zeokeel/update`：manifest（version/notes/assets）获取、https 强制、1MiB 上限、semver-ish 比较、GOOS/GOARCH
  asset 选择、错误分类（更新源未配置/清单无效/无平台包，中文）；不下载/不替换
- 设置「更新」页：当前版本、异步检查（检查中禁用）、可更新/已是最新 Badge、notes markdown、资源信息、
  复制下载链接；E2E 覆盖无更新源错误展示

**P3 会话归档（2026-11-08）**
- agent `SessionArchiver`：settings `session.<id>.archived`；`ChatSessionInfo.Archived`
- sidebar：默认隐藏归档、footer 显示/隐藏已归档（settings 持久化）、行菜单归档/取消、已归档 Badge、
  排序 置顶→普通→归档、搜索/分组/计数一致；E2E 验证隐藏→显示

**P3 会话置顶（2026-11-08）**
- agent `SessionPinner`：settings `session.<id>.pinned` 持久化，`ChatSessionInfo.Pinned`；未知会话/无 settings 错误
- sidebar：行菜单切换置顶、组内置顶优先排序、置顶 Badge；E2E 验证排序/徽标/取消置顶；design-guide 复核
  删除冗余「当前」Badge（选中态由 accent 图标+tint 表达）

**P4 可配置快捷键 + 设计复核（2026-11-08）**
- `ui.shortcut.toggleSidebar` / `ui.shortcut.commandPalette`：设置页 native Select 持久化，宿主每帧解析
  chord（cmd/ctrl/alt/shift）并注册 OnShortcut，改动即时生效；E2E 验证 Alt+B 覆盖与恢复默认
- design-guide 复核修正：会话头部复制/导出收敛为「更多」溢出菜单；移除右侧栏重复模型 tab 并合并进概览
  单面板；插件工具目录由卡片墙改为紧凑分隔行；遵循「不重复造控件、复用 design tokens/icons」

**P4 诊断页（2026-11-08）**
- 设置「诊断」页（高级 section）：AppInfo（版本/commit/构建时间/Go）、平台、数据目录、zeokeel.db 路径、
  工具数（KeyToolkit）、会话数（ChatService）、设置键（API key 打码）；「复制诊断信息」写剪贴板；
  E2E 断言工具数/数据库路径/无明文 key；快捷键页补 Cmd/Ctrl+K

**P4 命令面板（2026-11-08）**
- uiroot 宿主命令面板：Cmd/Ctrl+K 开关（chrome shortcut）、native Modal、搜索命令、点击执行/关闭、Esc；
  单测覆盖过滤/执行/关闭

**P3 全文搜索（2026-11-08）**
- sessionstore `SearchMessages`：跨会话消息 LIKE（大小写不敏感、%/_/\ 字面转义）、SessionTitle、Role、
  CreatedAt、rune 安全 snippet（去 Markdown 代码块/标题标记）、limit 默认 50/上限 200、新→旧
- sidebar：搜索框非空时「消息搜索」区（命中数 + 标题/角色/时间/片段），点击跳转会话；E2E 验证发送→搜索→跳转

**P3 插件页（2026-11-08）**
- toolkit 提供 `KeyPlugins`：MCP servers 快照（Name/URL/ServerName/Version/Protocol/Tools/Status）、Refresh Ping +
  ListTools 刷新；连接失败保留条目
- `zeokeel-ui/plugins`：MCP 插件状态卡 + 工具目录分类/搜索/计数；侧栏通用导航自动出现「插件」；
  E2E 覆盖工具目录（48 工具）与搜索过滤、MCP 空态

**P3 自动化（2026-11-08）**
- `zeokeel/automation`：cron 子集（*/N、M H、@hourly/@daily）+ every/after，JSON 持久化，ticker 扫描执行
  （新会话 + prompt）、RunNow/CRUD/启停/NextRun/LastError/Subscribe；toolkit schedule_* 绑定持久 scheduler
- `zeokeel-ui/automation`：自动化页（任务卡/启停 Toggle/立即运行/删除确认/内联新建表单）+ 侧栏通用屏幕导航；
  E2E 覆盖 RunNow 新建会话执行、schedule_create/list 持久化、页面渲染

**P3 会话 fork / 导出（2026-11-08）**
- agent `SessionForker`：深拷贝读模型（新消息 ID/附件/Tools/Reasoning），标题「副本 - …」，active 切换，
  store 持久化（Create/Rename/ReplaceHistory/fork 事件），重启恢复
- sidebar 行菜单「创建副本」；session 头部「导出」→ mygo.Dialog.Save 写 Markdown（转写+元信息），
  成功 Badge/失败 DangerText；E2E 覆盖 fork→持久化重启、导出文件内容

**P3 技能（2026-11-08）**
- `zeokeel/skills`：rig skill.FileStore 组合多根（config/dataDir/workspace，动态生效），List/Read + Roots；
  body 去 frontmatter；toolkit skill 工具绑定持久 store
- 设置「技能」页：roots 列表、技能卡（名称/描述/路径）、Collapsible markdown 预览、刷新/错误/空态；
  E2E 覆盖工作区 SKILL.md 发现 + skill 工具 list + 设置页展开

**P3 终端（2026-11-07 晚）**
- `zeokeel/terminal`：运行器（workspace cwd、bash/sh/zsh/pwsh 探测与选择、CombinedOutput、退出码、
  timeout/ctx 取消、输出截断）；rightpanel「终端」Tab 全 native（cwd/shell/清空/输出/输入/运行/停止）；
  E2E 真跑 `echo` 命令并断言帧输出

**P3 会话↔工作区（2026-11-07 晚）**
- sessionstore：sessions 表幂等迁移 workspace_id（旧库兼容、重启不重复）+ SessionWorkspaceStore.SetSessionWorkspace
- agent：NewSession/首次 Send 绑定当前工作区并持久化，ChatSessionInfo.WorkspaceID/restore 恢复
- sidebar：workspace→session 树（组头折叠/会话数/当前工作区高亮/未分组/跨组搜索），E2E 覆盖关联与树渲染

**P3 工作区变更 diff（2026-11-07 晚）**
- `WorkspaceDiffer.Changes`：git status --porcelain=v1 -z 解析（M/A/D/R/??、rename 新路径、嵌套 workspace
  路径翻译）、`git diff HEAD` 生成 unified diff（二进制/untracked 空 diff、maxChanges=100 / maxDiffBytes=256KiB、
  ctx 取消）；右侧栏「变更」Tab：状态 Badge、±统计、Collapsible 展开 diff 行着色、刷新；E2E 用真实 git 仓库验证

**P2 @ 提及（2026-11-07 晚）**
- composer `@` 建议面板：读 WorkspaceService（空查询 Files / 非空 Search），native 行插入 `@path `；
  E2E 覆盖真实 desktop assembly 的提及选择 → 发送 → 气泡文本

**P2 附件/图片（2026-11-07 晚）**
- contracts.Attachment / AttachmentSender / ChatMessage.Attachments；agent 图片→rig image part（data URL），
  非图片→文本 part（text/* 内容 ≤32KiB）；重启从 rig parts 恢复 chips
- composer：mygo.Dialog.Open 原生多选、48×48 图片缩略图、chips 移除、10MB 限制、MIME 映射；
  用户气泡只读 chips/缩略图；E2E 验证 provider 请求含 image part 与 UI 帧 chips

**P3 记忆 + Markdown（2026-11-07 晚）**
- `zeokeel/memory`：JSON 原子持久化 rig.MemoryStore（Add/List/Remove/Search、scope、重启恢复），
  toolkit 用持久 store 注册 memory 工具；desktop 16 / headless 7 实例
- 对话页「记忆」Tab：列表/搜索/添加/删除/错误提示/空态（native 控件 + design 样式）
- 助手正文 mygo `ui/markdown`：GFM 表格/任务列表/代码块（语言标签+复制）/流式增量

**垂直切片**
1. agent 宿主：`zeokeel/agent`（ChatService：provider/agent/gate/流式读模型、compaction、checkpoint）
2. 会话/持久化：`zeokeel/sessionstore`（SQLite：History/SessionLog/CheckpointStore/HistoryReplacer + 会话元数据），
   agent 重启恢复、标题/消息/事件持久化
3. 聊天主界面：`zeokeel-ui/session`（品牌消息气泡/思考折叠/工具卡/轨迹 Tab/记忆占位、composer 工具栏
   [模型 chip、`/` 命令、附件占位]、Enter 发送、复制 ID/对话、slash 命令菜单）、`zeokeel-ui/home`
   （品牌 hero + 初始设置卡 + composer 入口）、`zeokeel-ui/sidebar`（新对话/首页/会话搜索/行内
   PopoverMenu 重命名/删除/设为当前/footer 设置）
4. 工具/审批：`zeokeel/approvals` + `zeokeel-ui/approval`（native Modal）、`zeokeel-ui/toolcards`
   （24 真实工具卡）、`zeokeel-ui/rightpanel`（概览/工具/模型/日志；Log 读 SQLite 事件）
5. 设置：模型页（预设）、服务商页（DeepSeek/OpenAI/Ollama）、外观页（深色模式/语言，持久化）、
   快捷键页；about 页

## 未完成（按 old 功能面）

- P2 剩余：附件/图片（rig image ContentPart）、`@` 提及、Markdown 渲染、流式光标/动效、快捷键→命令注册表链路
- P3：memory 页/记忆工具接线、skills、automation、插件、workspace→session 树与 workspace diff、
  会话搜索（全文）、fork/export、右侧栏 Files/Terminal 页
- P4：ACP、全局热键、导入、更新、诊断
- 已知边界：MCP 动态工具名（`mcp__*`）无专用卡（走 session 兜底）；审批队列内存态；SQLite 单连接串行化

## 验证命令

```bash
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
go build ./... && go vet ./...
go test ./... -count=1 && go test ./... -count=1 -race
go run ./cmd/zeokeel --headless --data-dir /tmp/zk   # 6 实例
xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir /tmp/zk2  # 15 实例
go run ./internal/devtools/ui-snapshot /tmp          # 刷新 docs/screenshots/p2-*.png
```
