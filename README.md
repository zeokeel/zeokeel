# ZeoKeel

> **继续推进（TDD 并行 + 反证审计）**：新增 subagent 同步委派、permission 权限模式（ask/workspace/full）、
> team manager 编排与右侧「团队」面板（spawn/steer/interrupt）、@session 引用与推理强度（low/medium/high）、
> composer /goal /plan /memory 对话框、更新下载+SHA256 暂存、MCP stdio 服务器、右侧「任务」面板、
> 工件历史（Files「产物」）、单实例 + XDG autostart、install-on-restart、toast 通知、workspace mcp.json opt-in、
> 网络代理设置、MCP 工具级开关、提示词预设（设置 CRUD + composer 插入）、可编辑模型目录、
> 消息用量 footer/逐条复制/反馈 good-bad 与 /反馈 命令（导出含反馈）、计划模式 + keep-planning、自动化运行历史、
> Memory 整理/清空、技能新建/删除、工件下载、系统托盘/关闭到托盘、全局热键、LSP 工具、聊天模式（普通/计划/精简/创作）、
> Home 真实 composer、/停止 /附加文件 /帮助、数据与隐私页、通知开关、侧栏添加工作区、插件 MCP server 详情；
> 反证审计共 283 PASS / 0 SKIP（含最新审计补充），4×P2+4×P3 全部修复。详情：[docs/ADVERSARIAL-AUDIT.md](docs/ADVERSARIAL-AUDIT.md)。
>
> **最终验证（2026-11-08，Round 8 · HEAD a7fe576）**：`./scripts/verify.sh` 全量 **10/10 PASS（128s）**；
> 桌面（xvfb，托盘+全局热键）/headless/ACP 冒烟通过，xfvb 日志确认 **27 module instances、NO_PANIC**；
> regression **283 PASS / 0 SKIP**（含工作树第十六轮审计）；UI 快照 66 步 / 64 张 PNG。
> 新功能单测：memory extract **7+8 项**、presets **4+2 项**、spill **8+4 项**、compact **5 项**、
> goal lifecycle **2 项**、i18n 错误 **3+3 项**、插件参数 **8+1 项**；ACP `session/list`/`session/load`
> 真实帧已验证。旧功能覆盖矩阵见 [docs/COVERAGE.md](docs/COVERAGE.md)
> （62 项：✅61 / 🟡0 / ❌1（winproc 平台非目标）——旧功能覆盖清单全部关闭）。
> 验证证据：[docs/VERIFICATION.md](docs/VERIFICATION.md)。

编译期模块化的 Go 桌面 AI agent：**薄总入口 + 内核装配 + 模块贡献点**。
UI 是 mygo 原生控件（无浏览器/webview）；所有模块一起编译进同一二进制，`assembly.json`
只决定启用/裁剪哪些模块。

```
cmd/zeokeel           薄入口：解析参数 → host.Run
internal/host         进程宿主：窗口/托盘/单实例/日志/退出
internal/uiroot       UI 总入口（帧根）：布局、导航、overlay/toast、主题/语言、五张贡献点注册表
internal/assembly     装配根：编译期 registry + assembly.json 预检 + 产品装配
internal/contracts    契约：服务键 + 接口 + 贡献值类型（模块唯一共享面）
internal/modules/*    后端模块（store/session/tools/...）
internal/ui/*         UI 模块（home/session/settings/...）
internal/uikit        共享 UI 库（优先 mygo 原生控件，不手搓）
internal/design       设计 tokens
old/                  重构前的 ZeoKeel（归档参考，独立 Go module）
```

规则（详见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)）：

1. **mygo 有的控件优先用控件**，不手搓控件/布局图元树。
2. 模块只依赖 `internal/contracts`、`internal/uikit`、`internal/design`、`internal/lib/*`；
   禁止 import 其他模块实现、`internal/uiroot`、`internal/host`。
3. UI 状态归模块实例；禁止包级可变状态；UI 渲染只在帧内，跨线程更新走 `UIRuntime.Post`。
4. 注册/注销必须走 `ctx.Effect`（kernel 事务回滚依赖它）。
5. 配置变更 = 重启进程（编译期注册表，无热更新/运行时插件安装）。

构建（离线可用）：

```sh
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
go build ./...
go test ./...
```

一键重复全部验证（gofmt、build、vet、test、`-race`、回归套件、UI 快照与三产品冒烟）：
`./scripts/verify.sh`；快速循环 `./scripts/verify.sh --quick`，或 `make verify`（可 `ARGS=--quick`）。

归档的旧工程：`cd old && go build ./...`（独立 module，仅参考）。

## 状态（2026-11-07 · P1 持久化/审批/工具卡已可运行）

- ✅ **品牌设计体系已从 old 移植**：`internal/brand`（品牌色板/主题/logo 资源）、`internal/design`
  （Design Tokens、字阶、间距、组件样式）、`internal/icons`（Lucide SVG 图标集）；宿主 chrome 使用 280px
  品牌侧栏 + 64px brand row（logo+wordmark）+ 52px 顶栏；全部 UI 模块（对话/Home/侧栏/设置/审批/工具卡）
  已按老 UI 设计重做（mygo 原生控件 + design 样式，无自绘）
- ✅ 编译期装配：`internal/assembly`（显式 registry + 内嵌 `assemblies/desktop.json`、`headless.json`
  + kernel 预检 + required 失败整树回滚 + UI 子组级联卸载）
- ✅ UI 总入口 `internal/uiroot`：帧根、布局区域、五张贡献点注册表、Post 队列、导航、设置面、
  槽位渲染（`RenderSlot`）与工具卡注入（`RenderTool`）
- ✅ 进程宿主 `internal/host` + 薄 `cmd/zeokeel`（17 行）+ `Makefile`
- ✅ 示范模块：`appinfo`、`toolkit`（把 ZeoKeel Rig 工具集 + 沙盒策略组装成产品服务：
  fs/shell/pwsh、editor、image、PTY terminal、jobs、web、远程 MCP over HTTP、
  memory/schedule/worktree/skill/team_task/settings 等）、
  `hosttools`（产品侧 host 工具：ask_user/present/plan/goal/team，注册进共享工具注册表）、
  `ui/home`、`ui/about`、`ui/welcome`、`ui/toolcards`、`uikit`
- ✅ SDK 依赖：`github.com/zeokeel/zeokeel-rig v0.13.0`（纯工具集：agent loop / subagent / provider /
  sandbox / 81 个静态工具 + MCP/LSP / gate / session+compact / checkpoint；零第三方依赖；
  旧 agent 61 个工具名 + 现代工具面 55 个名称 100% 覆盖，`TestToolCatalogCoverage` 与
  `TestModernToolSurface` 双重门禁）
- ✅ Phase-1 聊天产品（用 rig 内核重建 old 的核心体验，UI 全部 mygo 原生控件）：
  - 后端模块 `zeokeel/agent`：ChatService（流式事件 → 会话读模型、订阅、取消、会话管理；
    provider/tools/history 全部来自 rig）
  - UI 模块 `zeokeel-ui/session`（对话页：Tabs/Scroll/TextArea/Collapsible/Badge/RenderTool）、
    `zeokeel-ui/sidebar`（新对话/SidebarItem 会话列表/设置入口）、
    `zeokeel-ui/settings`（模型配置页：TextInput/TextArea，API key 打码）
  - 端到端验证：真实 desktop assembly + 假 OpenAI SSE 服务 → 流式聊天 → 帧断言；Xvfb 真窗口冒烟
    （12 实例、screens [home session]）；截图见 `docs/screenshots/phase-1-*.png`
- ✅ P1 会话持久化 + 审批 + 工具卡/轨迹（新增 kernel 模块）：
  - `zeokeel/sessionstore`（SQLite，modernc.org/sqlite 纯 Go）：rig History / SessionLog /
    CheckpointStore / HistoryReplacer + 会话元数据 CRUD；重启后会话与消息自动恢复
  - `zeokeel/approvals`（backend）：ask/auto/never 模式、阻塞式审批队列、Subscribe；
    agent 后端用 `gate.Func` 接入，模型工具调用需人工允许/总是允许/拒绝
  - `zeokeel-ui/approval`（UI）：native `ui.Modal` 审批弹窗（允许/总是允许/拒绝，Esc=拒绝）
  - `zeokeel-ui/toolcards`：24 个真实工具卡（native Badge + Collapsible 摘要）；
    `zeokeel-ui/session` 轨迹 Tab 接入真实工具时间线
  - 装配：desktop 15 实例、headless 6 实例（store/approvals 后端 + approval UI）；
    E2E 覆盖「工具调用→审批弹窗→允许→工具执行→工具卡/轨迹展示→重启恢复」
  - 截图：`docs/screenshots/p1-approval.png`、`p1-chat.png`、`p1-trajectory.png`
- ✅ P2（第一批）：Home 接入真实最近会话 + "开始新对话"；设置新增「外观」页（native Toggle 深色模式 +
  native Select 界面语言，写 `ui.theme.dark`/`ui.language` 并立即生效）；模型页新增 DeepSeek/OpenAI/
  Ollama 快速预设（native Button 填 draft，保存生效）。截图：`docs/screenshots/p2-home.png`、
  `p2-appearance.png`
- ✅ P2（第二批，按老 UI 布局/功能对齐）：
  - 宿主布局改为老结构：品牌侧栏 → 新对话 → 首页导航 → 会话区（搜索/会话行/重命名/删除）→ footer；
    去掉自创的全局顶栏，屏幕自己拥有 header；右侧检查栏为**窗口级 docked 面板**（内容列右上角开关，
    老 UI 的 right sidebar 位置），跨屏保持
  - 侧栏会话操作：native SearchField 搜索、native Modal 重命名（SessionRenamer）、native AlertDialog
    删除确认
  - composer slash 命令菜单：读 `UIRuntime.Commands()`，过滤/匹配/点击执行；命令注册表与未来命令面板共用
  - 新增右侧检查面板模块 `zeokeel-ui/rightpanel`（概览/工具/模型，含 API key 打码），由宿主 docked 渲染
  - Home 简化为老结构：居中 hero（logo+wordmark）+ 初始设置卡（缺模型时引导"配置模型"，
    对照 old homeSetupCard）+ composer 入口，不再堆 demo 工具卡/设置按钮/最近会话
  - Composer 工具栏（对照 old composer_native）：模型 chip（打开模型设置）、`/` 命令入口、
    附件按钮（禁用占位，附件落 P2 剩余）
  - 会话行的内联操作（对照 old workspaces.go）：每行 native PopoverMenuButton「⋯」提供
    重命名/删除/设为当前；footer 只保留设置入口
  - 老 chrome 快捷键：Cmd/Ctrl+B 折叠/展开左侧栏为 64 DIP 图标 rail（品牌 mark 展开、屏幕图标、
    设置入口），与宿主 API 共享状态；截图 `p2-sidebar-collapsed.png`
  - 设置新增「服务商」页（对照 old models_ui）：DeepSeek/OpenAI/Ollama 内置兼容服务商卡片，
    "使用"写入模型/BaseURL 设置；截图 `p2-providers.png`
  - 设置新增「快捷键」页（对照 old shortcuts 页）：折叠侧栏/发送/换行/命令菜单/Esc 参考；
    截图 `p2-shortcuts.png`
  - 右侧检查栏新增 **日志** 页（对照 old right_sidebar.go 的 Log tab）：读取 SQLite 持久化的
    session events（seq/type/time/payload 折叠），真实事件流经 E2E 验证；截图 `p2-log.png`
- ✅ P3（首批）：**记忆**功能 —— `zeokeel/memory`（JSON 原子持久化 rig.MemoryStore，注册 memory 工具进
  toolkit）；对话页「记忆」Tab 真实面板（列表/搜索/添加/删除/scope/时间，native 控件）；助手正文改用
  mygo 官方 `ui/markdown`（标题/列表/表格/代码块+复制/流式增量）；截图 `p2-memory.png`、`p1-chat.png`
- ✅ P2 附件/图片：contracts.Attachment + 可选 AttachmentSender；agent 把图片转为 rig image content part
  （data URL），非图片附件文本化（文本内容 ≤32KiB）；composer 原生文件选择（mygo.Dialog.Open）+ 缩略图/
  chips/移除/10MB 限制，用户气泡显示附件 chips；重启后从 rig parts 恢复 chips；截图 `p2-attachments.png`
- ✅ P3 工作区 + Files 检查器：`zeokeel/workspace`（JSON 注册表 + 沙箱文件树/Files/ReadFile/Search，
  symlink 逃逸防护、原子持久化）；右侧栏「文件」Tab：打开文件夹（native Directory picker）、目录浏览/
  面包屑、文本/Markdown/图片预览；截图 `p2-files.png`、`p2-files-preview.png`；desktop 17 / headless 8 实例
- ✅ P2 @ 提及：composer 输入 `@` 弹出工作区文件建议（`WorkspaceService.Files/Search`，native 面板，
  方向/文件图标 + 大小），选择后写入 `@path `；无工作区不显示、读取错误 DangerText；截图 `p2-mentions.png`
- ✅ P3 工作区变更（diff）：`WorkspaceDiffer.Changes`（git status porcelain + `git diff HEAD`，二进制/未跟踪/rename
  语义、maxChanges/maxDiffBytes、非 git 可读错误）；右侧栏「变更」Tab：状态 Badge + ±统计 + Collapsible
  展开 unified diff（+ 行 Success / − 行 Danger / hunk 表头 muted，mono）；截图 `p2-changes.png`、
  `p2-changes-diff.png`
- ✅ P3 会话↔工作区 + workspace 树：sessionstore 幂等迁移 `workspace_id`（旧库兼容）+ agent 在新建/首次发送时
  记录当前工作区；侧栏会话区按工作区分组（当前工作区高亮、组折叠、会话数、未分组、搜索跨组）；截图
  `p2-workspace-tree.png`
- ✅ P3 终端：`zeokeel/terminal`（workspace root 为 cwd 的简单命令运行器，shell 探测/选择、timeout、
  输出截断、ctx 取消；无 PTY/持久 shell，与 old terminalPanel 一致）；右侧栏「终端」Tab：cwd/shell/清空、
  mono 输出、输入 + 运行/停止、退出码；截图 `p2-terminal.png`；desktop 18 / headless 9 实例
- ✅ P3 技能：`zeokeel/skills`（rig `skill.FileStore` 多根发现：config dirs → dataDir/skills →
  workspace/skills，动态 roots、frontmatter 解析、body 去 frontmatter）；toolkit 的 skill 工具改用该持久
  store；设置「技能」页：roots、名称/描述/路径、Collapsible + markdown 预览、刷新/错误；截图
  `p2-skills.png`、`p2-skills-content.png`；desktop 19 / headless 10 实例
- ✅ P3 会话 fork / 导出：`SessionForker.ForkSession`（深拷贝消息/附件/Tools，标题「副本 - …」，持久化历史+事件，
  重启恢复）；会话行「⋯」菜单「创建副本」；会话头部「导出」→ mygo 原生保存对话框写 Markdown（标题/时间/
  transcript，测试 seam 可替换）；截图 `p2-fork.png`
- ✅ P3 自动化：`zeokeel/automation`（cron 子集 + every/after，JSON 持久化，ticker 到点执行 → 新会话 +
  Prompt，RunNow/启停/删除/NextRun/LastError，Subscribe；toolkit schedule_* 工具改绑持久 scheduler）；
  `zeokeel-ui/automation` 自动化页（列表/新建/启停/立即运行/删除，native 控件）+ 侧栏通用屏幕导航；
  截图 `p2-automation.png`；desktop 21 / headless 11 实例
- ✅ P3 插件页：toolkit 暴露 `KeyPlugins`（MCP 服务器快照/Ping 刷新/工具数）；`zeokeel-ui/plugins` 插件页
  （MCP 插件状态卡 + 48 工具目录按分类分组 + 搜索过滤）+ 侧栏「插件」导航；截图 `p2-plugins.png`、
  `p2-plugins-search.png`；desktop 22 实例
- ✅ P3 全文搜索：sessionstore `MessageSearcher`（跨会话消息 LIKE 搜索、LIKE 字面转义、snippet 去
  Markdown 标记、新旧排序、limit 上限）；侧栏搜索框输入后显示「消息搜索」命中列表（标题/角色/时间/
  片段），点击跳转会话；截图 `p2-search.png`
- ✅ P4 命令面板：宿主持有 Cmd/Ctrl+K（`ui.Context.OnShortcut`）打开 native Modal 命令面板，过滤
  `UIRuntime.Commands()`、点击执行并关闭、Esc 关闭；截图 `p2-palette.png`
- ✅ P4 诊断页：版本/Commit/构建时间/Go/平台/数据目录/会话数据库路径/工具数/会话数/设置键（API key
  打码）+「复制诊断信息」；快捷键页补 Cmd/Ctrl+K；截图 `p2-diagnostics.png`
- ✅ P4 可配置全局快捷键：设置「快捷键」页两项 chrome 快捷键（Cmd/Ctrl+B、Cmd/Ctrl+K）用 native Select
  持久化到 `ui.shortcut.*`，宿主每帧解析并注册 OnShortcut（改动下一帧生效、恢复默认）；解析器支持
  cmd/ctrl/alt/shift 组合；E2E 覆盖改键 Alt+B 生效与恢复默认
- ✅ 设计复核（design-guide）：会话头部三枚等权按钮收敛为⿾更多⿿原生溢出菜单；右侧栏去掉重复「模型」
  tab（6 tabs），模型配置并入概览同一面板（分隔线+紧凑行）；插件页 48 工具由「每工具卡片」改为紧凑行
  （分隔线分组），降低视觉噪音；快捷键页可编辑项保持一行一个控件
- ✅ P3 会话置顶：pin 标记持久化到 settings `session.<id>.pinned`（不变 DB schema），`ChatSessionInfo.Pinned`；
  侧栏行菜单「置顶会话/取消置顶」，各组内置顶优先排序 + 「置顶」Badge；设计复核移除冗余「当前」Badge
  （选中态由 tint + accent 图标承担），标题不再被挤断；截图 `p2-pinned.png`
- ✅ P3 会话归档：archived 标记持久化到 settings（不变 schema），侧栏默认隐藏归档、footer「显示/隐藏已归档」
  视图开关、行菜单「归档会话/取消归档」、归档行「已归档」Badge；排序 置顶→普通→归档；截图 `p2-archived.png`
- ✅ P4 更新检查：`zeokeel/update`（release manifest 获取、https 策略、semver 比较、平台 asset 选择、
  ErrDisabled/ErrManifest/ErrNoAsset，中文错误）+ 设置「更新」页（当前版本、检查更新异步、可更新 Badge、
  notes markdown、复制下载链接；仅检查不自动替换）；截图 `p2-update.png`；desktop 23 / headless 12 实例
- ✅ P2 拖放附件：宿主 `window.OnFileDrop` → `contracts.KeyFileDrop` → 会话 composer 暂存 chips（与原生
  picker 同一路径，10MB/MIME/错误处理复用）；截图 `p2-drop.png`
- ✅ P4 DSH 导入：`zeokeel/importer`（扫描 `<dsh>/sessions/**/session.v4.jsonl`，解析 header v4 + user/assistant
  visible text + reasoning，跳过 context/tool/附件/zstd；预览计数；导入为 `dsh-<id>` 会话 + 历史 + workspace
  注册，幂等）+ 设置「导入」页（目录输入/浏览/扫描预览/导入结果）；截图 `p2-import.png`；desktop 24 / headless 13 实例
- ✅ P4 ACP 适配器：`zeokeel/acp`（newline-framed JSON-RPC 2.0 over stdio）：initialize / session-new /
  session-prompt（agent_message_chunk 流式 + end_turn/cancelled）/ session-cancel；`zeokeel --acp` 进入 ACP 模式
  （headless assembly + KeyACP），与桌面产品共享 settings/会话；见 [docs/ACP.md](docs/ACP.md)
- ✅ P4 i18n（第一切片）：`uikit.T(locale, zh, en)`；侧栏与会话界面全部自有文案接入 `LocaleService`
  （en/zh 每帧切换即时生效；导航 screen 标题/设置页仍中文，后续切片）；桌面 assembly 移除显式中文 labels
  以便本地化默认值生效；截图 `p2-english.png`
- ✅ P4 i18n（第二切片）：设置全部页面（模型/服务商/外观/技能/快捷键/诊断/更新/导入 + Settings 浮层标题，
  页面标题语言变化动态重注册）+ Home（hero/setup/composer）+ 侧栏导航标题（Home/Automation/Plugins）；
  desktop assembly 去掉显式中文 settings title 让本地化默认值生效；截图 `p2-english`/`p2-english-settings.png`
- ✅ P4 i18n（第三切片）：About 页（内容 + 导航标题动态重注册）、右侧检查栏（Inspector/6 tabs/全部面板）、
  Automation/Plugins 页面、toolcards、approval 全部 en/zh 每帧切换；desktop assembly 去掉 about 显式 title；
  截图 `p2-english-automation.png`、`p2-english-settings.png`
- ✅ P4 host chrome i18n：命令面板（Command palette/Search commands/Esc 提示）、打开/收起右侧栏、展开侧边栏；
  E2E en 断言
- 📋 旧功能覆盖审计：[docs/COVERAGE.md](docs/COVERAGE.md)（✅48 / 🟡13 / ❌1，附深度限制与非目标分类）
- ✅ P5 Composer 深度（第一切片）：slash 命令「新会话/创建副本/导出对话/打开设置」（UIRuntime.Commands，
  `/` 菜单与 Cmd/Ctrl+K 共用）+ 最后一条 assistant 的「重新生成」（agent SessionRegenerator：截断旧回复、
  复用运行管线、持久化回滚）；截图 `p2-slash.png`
- ✅ P5 模型切换：`zeokeel/modelcatalog` 预设（DeepSeek Chat/Reasoner）+ agent per-session 模型覆盖
  （`SessionModelSwitcher`，settings 持久化，Send/Regenerate 使用会话模型）+ composer 模型菜单（打勾/当前/打开模型
  设置回退）；desktop 25 实例 / registry 27；截图 `p2-modelmenu.png`、`p2-model-switched.png`
- ✅ P5 Web search：`internal/websearch`（迁移旧 HTTP 搜索后端：POST/Bearer/limit/超时/1MiB）+ toolkit 动态注册
  `web_search`/`web_fetch`（assembly `toolkit.network=true`，settings 变更下一次调用生效）+ 设置「搜索」页；
  截图 `p2-search-settings.png`
- ✅ P5 ask_user 真实交互链路：宿主 `UserInteraction` 桥（uiroot 阻塞 Ask/Answer/Cancel）+ hosttools 真实 Asker +
  `zeokeel-ui/interaction` 原生 Modal（选项/自由文本/FIFO 队列/Esc 取消/i18n）；E2E 工具调用阻塞→点击选项→返回
  `user answered: sqlite`；截图 `p2-ask.png`；desktop 26 实例 / registry 28
- ✅ P5 present/plan review：`KeyArtifactPresenter`/`KeyPlanReviewer` 宿主桥（uiroot 服务 + host provideServices +
  hosttools Presenter/PlanController 桥接）+ interaction 模块统一 FIFO overlay（工件 markdown 预览、计划待审阅
  批准/取消、ask/artifact/plan 三队列、i18n）；E2E present→artifact 覆盖层、plan_review→批准→planState=approved；
  截图 `p2-artifact.png`、`p2-plan.png`
- ✅ P5 MCP 用户配置：设置「MCP」页（Streamable HTTP 服务器列表/添加/移除，JSON 持久化 `mcp.servers`，
  校验/重复拒绝/坏 JSON 提示，重启生效）+ toolkit 启动时合并 assembly 与用户配置（assembly 优先、按 URL 去重、
  非法行 warning）；截图 `p2-mcp-settings.png`
- ✅ 会话 JSONL 导出：header 更多菜单「导出 JSONL」（session 头 + 每条 message 一行，含 reasoning/error/tools
  扁平化），独立 native Save 对话框 seam；单测覆盖内容/行数/状态
- 📊 覆盖审计（最新）：[docs/COVERAGE.md](docs/COVERAGE.md)（✅26 / 🟡27 / ❌9；核心+多数高级 UI 可用，
  剩余集中在编排执行/composer 深度/presets/桌面集成/管理深度）
- ✅ P5 goal 跟踪：`contracts.GoalTracker` 宿主桥（uiroot goal 服务 + host 提供 + hosttools GoalStore 桥接）+ 会话 header
  「目标」Badge/文本/「清除目标」；E2E goal 工具→header 显示→清除后 tracker 空；截图 `p2-goal.png`
- ⬜ 后续阶段见 [docs/PRODUCT-REBUILD-PLAN.md](docs/PRODUCT-REBUILD-PLAN.md)：
  P2 剩余（composer 附件/图片/@ 提及、右侧栏 Files/Terminal/Log 页、会话操作上下文菜单）→
  P3 memory/skills/automation/插件/workspace diff/搜索/fork/export → P4 ACP/热键/导入/更新/诊断
- ✅ `make ci` 全绿；uiroot 29 个 headless 测试（覆盖率 99.3%）；desktop/headless 装配集成测试；
  Xvfb 真窗口冒烟通过
- ⬜ 逐模块迁移（后端 20 + UI 16，见 `docs/research/kernel-split/05-ui-module-map.md`）：
  `ui/session` 后续接管 `session.*` 注入点（轨迹、轨迹旁横条、页头横条、右栏、输入区）

## 常用命令

```sh
make ci            # build + vet + test
make run           # 桌面产品（需要桌面会话）
make run-headless  # headless 产品（无窗口，等 SIGINT/SIGTERM）
make build ARGS=   # 仅构建
```

注入点清单与灵活性规则：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) §4/§5；
已接线的模块清单：[docs/INJECTION-POINTS.md](docs/INJECTION-POINTS.md)。
