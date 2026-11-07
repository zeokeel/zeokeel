# ZeoKeel

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
