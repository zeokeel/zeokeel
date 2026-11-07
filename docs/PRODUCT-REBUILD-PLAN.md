> **UI 设计体系落地（2026-11-07）**：old 的 `internal/{design,brand,icons}` 已移植进新工程（原样，
> 仅改 import 路径），宿主 chrome 与全部 UI 模块按老 UI 的设计/布局重做，截图更新在
> `docs/screenshots/`。这是对"UI 完全不对"的修复：不是自创样式，而是复用 old 的设计系统。
>
> **P2 第一批落地（2026-11-07）**：Home 真实最近会话/新对话入口、外观设置页（深色模式+界面语言，
> 持久化到 `ui.theme.dark`/`ui.language`）、模型快速预设；composer slash/@/附件与图片留待下一批。
>
> **P1 落地状态（2026-11-07）**：会话持久化 `zeokeel/sessionstore`（SQLite）、审批后端
> `zeokeel/approvals` + 审批弹窗 `zeokeel-ui/approval`、工具卡/轨迹（`zeokeel-ui/toolcards`、
> `zeokeel-ui/session`）已完成并接入 kernel assembly；desktop 15 实例 / headless 6 实例，
> E2E 覆盖审批、工具执行与重启恢复。下一轮 P2。

# ZeoKeel 产品重建计划 — old → rig v0.13.0 + mygo 原生 UI

> 版本：v1（Phase-1 D 产出）· 写作时间：2026-11-07
> 对象：`/data/mygo/old`（旧 ZeoKeel Agent，只读参考；独立 Go module，不参与新根工程构建）
> 目标栈：`github.com/zeokeel/zeokeel-rig v0.13.0` + 现有产品骨架 `internal/{contracts,assembly,host,uiroot,modules,ui}` + **mygo v0.7.4 原生控件**
> 读者：后续每轮负责"按图施工"的实现者、review 者与 Lead。**本文只写方案，不改代码**。
> 相关依据：[`ARCHITECTURE.md`](ARCHITECTURE.md)、[`INJECTION-POINTS.md`](INJECTION-POINTS.md)、
> [`docs/research/rig-parity/01-report.md`](research/rig-parity/01-report.md)、
> [`03-v0.13.0-verification.md`](research/rig-parity/03-v0.13.0-verification.md)、
> [`docs/research/kernel-split/05-ui-module-map.md`](research/kernel-split/05-ui-module-map.md)、
> [`old/docs/research/02-mygo-native.md`](../old/docs/research/02-mygo-native.md)。

---

## 0. 结论摘要（TL;DR）

1. **目标**：用 rig v0.13.0 替换 old 的 agent 内核与全部工具链，用 mygo 原生控件重建 old 的全部产品 UI；
   新根工程继续走 kernel 编译期模块化（assembly + uiroot 注入点），不引入 webview、不自绘控件。
2. **能直接替代的部分**：old 的 tools（12,267 行）、provider/openai（1,193）、lsp 客户端（1,347）、
   mcp 客户端（1,171）、agentteam（733）、runtime 的循环/审批/子代理/多模态部分——约占 old 生产代码
   **22%**（~2 万行），rig v0.13.0 已全部覆盖（81 静态工具 + 38 测试包全绿 + 12 项 SDK 缺口补齐）。
3. **需要重建的部分**：old 的 shell（25,229 行生产）、session（8,840）、store（5,887）、app（8,153）、
   runtime 产品逻辑、prompt/state/workspacechanges/sessionreference、ACP/热键/导入/更新等——约占 **78%**，
   全部是"产品层"：它们在新架构里分别落到 `internal/modules/*`（后端/服务）与 `internal/ui/*`（UI 模块）。
4. **UI 规模**：old shell 59 个生产文件 → **16 个 UI 模块 + 宿主 chrome（约 3.4k 行）**；
   old 原报告估计重写为原生控件后 UI 生产代码 17.1k → 9.2–11.5k，**净省 5.2–7.9k 行（30%–46%）**。
5. **阶段**：P0 chat MVP（**本轮**）→ P1 会话持久化 + 工具卡/轨迹 + 审批 UI → P2 composer 增强 + 设置扩展 →
   P3 memory/skills/automation/插件/搜索/fork/export/diff → P4 ACP/全局热键/导入/更新/诊断。
   每阶段都有可执行验收（`go test ./...`、headless 帧断言、fake-provider E2E、边界 grep）。
6. **写作时的落地状态**（以代码为准）：`internal/contracts/chat.go` 已定义 `KeyChat`/`ChatService` 读模型；
   Phase-1 的 `internal/ui/session`、`internal/ui/sidebar`、`internal/ui/settings` 目录已在仓库出现；
   `internal/modules/agent`（ChatService 后端）尚未落地；**rig v0.13.0 升级正在进行**（写作时 `go.mod` 已切到
   v0.13.0，toolkit/hosttools 的 `contracts/v1` 等 API 迁移由集成方并行完成，见 §4.2 P0-0）。
7. **最大风险**（详见 §6）：① mygo 0.x 的原生控件边界（无图标库、无消息气泡/工具卡成品控件，需要 uikit 组合）；
   ② agent 后台 goroutine 与 UI 线程的桥（只允许 `UIRuntime.Post` + `Invalidate`）；
   ③ 数据迁移（old SQLite 12 张表 → 新 schema，附件/事件序号/API key 明文风险）。

**三条不可协商的硬规则**：

1. **UI 一律用 mygo 原生控件**：不允许 Canvas/Painter 自绘、不允许用 `Row+Text` 手搓按钮/列表/Tabs/弹窗/
   Markdown/virtual list；组合件放 `internal/uikit`，且只组合原生控件。
2. **跨线程只走 `UIRuntime.Post` + `Invalidate`**：`ui.Context`/`ui.Element`/`ui.Handle` 只在帧内有效，
   agent goroutine、后台任务、定时器一律先 Post 再改 UI 状态。
3. **模块边界**：UI 模块只能 import `internal/contracts`、`internal/uikit`、`internal/design`、mygo、kernel；
   不 import 其他模块实现、`internal/uiroot`、`internal/host`；CI 用 `go list -deps` 断言。

---

## 1. 架构分层

### 1.1 分层图

```
┌──────────────────────────── 产品进程（cmd/zeokeel，薄 main ≤30 行） ────────────────────────────┐
│                                                                                             │
│  internal/host            进程宿主：窗口/单实例/托盘/设置存储(settings.json)/日志/信号/退出      │
│    provides KeySettings、KeyLogger（+ 未来 KeyShortcuts/KeyClipboard 等进程能力）              │
│        │                                                                                    │
│  internal/assembly        编译期装配：registry + assemblies/{desktop,headless}.json + 预检      │
│        │                                                                                    │
│  kernel root（github.com/zeokeel/zeokeel-kernel v0.1.1，Cordis 式内核）                        │
│        ├── 后端模块 internal/modules/*（提供 service，业务/数据/agent）                        │
│        │     appinfo        ── KeyAppInfo                                                  │
│        │     toolkit        ── KeyToolkit：rig rigtools.Registry（81 静态工具）              │
│        │     hosttools      ── ask_user/present/plan_review/exit_plan_mode/goal/team 工具    │
│        │     agent          ── KeyChat：ChatService（P0 新）                                 │
│        │     store/session  ── rig.History/SessionLog/Settings/CheckpointStore…（P1 新）      │
│        │     memory/skills/automation/…（P3）                                                 │
│        │        │                                                                           │
│        │        └── 依赖 rig v0.13.0（纯 SDK：kit/agent/provider/openai/tools/full/gate/      │
│        │            sandbox/session/compact/checkpoint/…；无 UI、无 OS、无 DB）                │
│        │                                                                                    │
│        └── internal/uiroot  UI 总入口（宿主 chrome）：帧根/布局区域/导航/overlay/toast/主题/语言  │
│              provides KeyUIRuntime、KeyNavigator、KeyTheme、KeyLocale、KeyScreenLister          │
│                 │                                                                           │
│              UI 模块 internal/ui/*（只注册贡献；screen/slot/settings page/tool renderer/command）│
│                 │   home / session / sidebar / settings / toolcards / welcome / about / …     │
│                 │                                                                           │
│              internal/uikit   共享 UI 组合库：只组合 mygo 原生控件，不自绘（PageHeader/Section/  │
│                 │              KeyValueRows/EmptyState/…；后续新增 composer/toolcard/settings 基元）│
│                 └── internal/design  设计 tokens（从 mygo Theme 派生：颜色/间距/字体/圆角）      │
│                          │                                                                  │
│                     mygo v0.7.4  ui.* 原生控件（Sidebar/Tabs/Modal/Scroll/TextArea/List/Table/  │
│                          markdown/editor/richtext/chart/toast/tooltip/…，106 个生成式构造函数）  │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

### 1.2 层职责

| 层 | 目录 | 职责 | 依赖方向 | 现状 |
|---|---|---|---|---|
| 进程宿主 | `internal/host` | 窗口、单实例、托盘、设置存储、日志、信号、退出顺序 | → mygo、contracts | ✅ Stage 0 |
| 装配根 | `internal/assembly` | 编译期 registry、内嵌 assembly.json、启动预检、级联卸载 | → kernel、modules、uiroot、host | ✅ Stage 0 |
| 内核 | zeokeel-kernel | 服务 Provide/Require、模块生命周期、Effect 逆序注销 | — | ✅ |
| 后端模块 | `internal/modules/*` | agent、工具装配、存储实现、产品服务；只依赖 contracts + rig + stdlib | → contracts、rig | 🟡 P0 起逐模块 |
| UI 宿主 | `internal/uiroot` | 唯一帧入口、布局区域、导航、注入点注册表、overlay/Esc、toast viewport、主题/语言 | → contracts、mygo、uikit/design | ✅ Stage 0 |
| UI 模块 | `internal/ui/*` | 注册 screen/slot/settings page/tool renderer/command；帧内只用原生控件构建 | → contracts、uikit、design、mygo | 🟡 P0 起逐模块 |
| 共享 UI 库 | `internal/uikit` | 原生控件的组合件（页面头/区块/键值行/空态；后续 composer、tool card 基元） | → mygo、design | 🟡 |
| 控件层 | mygo `ui` 包 | 原生控件、主题、无障碍、动效、markdown/editor/chart/i18n | — | 固定 v0.7.4 |
| Agent SDK | rig v0.13.0 | provider、agent 循环、工具、gate、sandbox、session/compact/checkpoint | 被后端模块 import | 升级进行中（v0.8.0→v0.13.0） |

### 1.3 关键数据流

**(a) 一次对话（P0 起）**

1. UI 模块在帧内 `Build` 读 `contracts.ChatService`（只读快照，不修改状态）→ 原生控件渲染消息/流式文本。
2. 用户点发送（或 Enter）→ UI 调 `ChatService.Send(sessionID, prompt)` → 返回到 UI 线程；按钮立即变 Stop。
3. agent 模块在独立 goroutine 跑 `rig.Agent.Run(ctx, RunRequest{SessionID, Input, Emit})`：
   `Emit` 把 `rig.EventTextDelta`/`EventReasoningDelta`/`EventToolCall`/`EventToolResult`/`EventError`/
   `EventUsage` 写进会话读模型（加锁），然后回调订阅者。
4. UI 模块的订阅回调（任意 goroutine）→ `runtime.Post(func(){ runtime.Invalidate() })`；
   下一帧重新 Build，读到最新读模型。**禁止**在回调里碰 `ui.Context`。
5. Stop → `ChatService.Cancel` → cancel run ctx；读模型保留已流式内容并标记 cancelled。

**(b) 审批（P1 起）**

1. rig agent 在工具调用前调用 `rig.Gate`（产品用 `gate.Ask(asker)` 组合；P0 先用 `gate.AllowAll`）。
2. `Asker.Ask(ctx, question, options)` 由 host 实现：把 pending 请求写进读模型 + Invalidate；
   Ask 阻塞等待 UI 的 Allow / Always / Deny，直到 ctx 取消或用户决定。
3. UI 的审批卡（`ui/approval`）只调用 `Approve/Deny/Always` 服务方法；状态唯一真相在后端 pending 集。
4. P3 可叠加 `gate.Review(reviewer)`（`tools/review.SubagentReviewer`）实现 LLM auto-review。

**(c) 持久化 / 压缩 / 恢复（P1 起）**

1. `rig.History` 的 SQLite 实现：`Load/Append` 对应 messages 表；`HistoryReplacer.Replace` 对应压缩后持久替换。
2. `rig.SessionLog` 实现：events 表，`Events/Search/Last` 支撑轨迹页、`session_*` 工具与会话搜索。
3. `rig.CheckpointStore`：agent 在模型请求前/副作用工具前/回合结束写检查点；
   `agent.RecoverInterruptedTurn` 在启动恢复（只修消息层，不重放副作用）。
4. `rig.Compactor`（`session/compact` 的 Window/LLM）+ `ContextBudget` + `SpillPolicy` 在 agent 模块接线；
   持久替换由 History 实现完成，UI 只显示「已压缩」状态。

### 1.4 rig v0.13.0 契约 → 产品落点

| rig 能力（v0.13.0） | 关键 API | 产品落点 | 阶段 |
|---|---|---|---|
| Agent 循环/流式事件 | `rig.AgentSpec`、`agent.New`、`rig.RunRequest{SessionID,Input,Emit}`、`rig.Event{Type,Text,Reasoning,Call,Result,Usage,Err}` | `internal/modules/agent`（ChatService） | P0 |
| OpenAI 兼容 provider | `provider/openai.New(openai.Config{DefaultModel,BaseURL,APIKey,APIKeyEnv})` | 同上 | P0 |
| 工具装配 | `tools/full.All(full.AllOptions{Policy,Root,History,SessionLog,Host,Runner,…})`、`kit.New` | `internal/modules/toolkit`（升级 v0.13） | P0-0 |
| 审批 Gate | `gate.AllowAll`、`gate.Ask(rig.Asker)`、`gate.Review(rig.Reviewer)`、`rig.Decision` | agent 模块 + host `Asker` + ui/approval | P0/P1/P3 |
| 会话历史 | `rig.History`、`rig.HistoryReplacer` | `internal/modules/store`（SQLite） | P1 |
| 会话日志 | `rig.SessionLog`（`Events/Search/Last`）、`rig.SessionEvent` | store + 轨迹/搜索 UI + session_* 工具 | P1/P3 |
| 检查点 | `rig.CheckpointStore`、`agent.RecoverInterruptedTurn` | store + agent 模块 | P1 |
| 压缩 | `rig.Compactor`、`rig.ContextBudget`、`session/compact.WindowCompactor/LLMCompactor/PruneToolResults/OffloadOldImages` | agent 模块 + store `Replace` | P1 |
| 工具结果 spill | `rig.SpillPolicy{Dir,ThresholdBytes,MaxFiles}` | agent 模块 | P1 |
| 目标 | `rig.GoalStore/GoalUpdater`、`agent.GoalRoundDriver` | hosttools goal 工具 + ui/goal + agent 模块 | P3 |
| 记忆/技能/调度 | `rig.MemoryStore`、`rig.SkillStore`、`rig.Scheduler` + `tools/memory/skill/schedule` | 对应后端模块 + settings 页 | P3 |
| 团队任务板 | `rig.TeamTaskManager` + `tools/team` | team 模块 + ui/settings | P3 |
| 子代理/通知 | `rig.SubagentRunner`、`rig.NoticeSource`、`rig.Reviewer` | tools 装配 + ui/toolcards/team | P3 |
| 预设 | `tools/presets`（minimal/standard/all）、`kit.Config.ToolPreset` | 产品 preset 页 + agent 模块 | P3 |
| 宿主工具 | `tools/hosttools.Options/ Tools`（Asker/Presenter/FilePresenter/PlanController/GoalStore） | hosttools 模块 + host 服务 | P0 起 |
| 会话工具 | `tools/session.Tools(rig.SessionLog)`、`tools/inspect`、`tools/report` | toolkit（P1 接 SQLite 后自然可用） | P1 |

### 1.5 依赖方向与 CI 门禁

允许的 import：

- `internal/ui/*` → `internal/contracts`、`internal/uikit`、`internal/design`、mygo、kernel。
- `internal/modules/*` → `internal/contracts`、rig、stdlib（+ 必要的 `internal/lib/*`）。
- `internal/uiroot` / `internal/host` → 只被 `cmd`、`assembly` 与内核调用。
- 禁止：UI 模块 import 其他模块实现、`internal/uiroot`、`internal/host`；后端模块 import UI。

建议 CI 断言（Phase-1 起）：

```sh
# 1) UI 模块不得依赖后端模块实现
go list -deps ./internal/ui/... | grep -E 'internal/modules|internal/host|internal/uiroot' && exit 1 || true

# 2) UI 模块不得 import 其他 UI 模块实现（除 uikit/uimodtest）
go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/ui/... | grep -E 'internal/ui/[a-z]+' | grep -v uikit | grep -v uimodtest

# 3) 禁止自绘（详见 §2.1）
grep -rnE 'ui\.(Canvas|Painter|NewBitmap)|\.Paint\(' internal/ui internal/uikit --include='*.go'
```

---

## 2. UI 原则与 mygo 原生控件

### 2.1 "UI 一律用 mygo 原生控件，不自绘"的十条规则

| # | 规则 | 说明 / 反例 |
|---|---|---|
| 1 | **控件必须来自 mygo** | 按钮用 `ui.Button/ButtonBase/PrimaryButton`，输入用 `ui.TextInput/TextArea/SearchField`，开关用 `ui.Switch/Toggle/Checkbox`，下拉用 `ui.Select/Combobox/Autocomplete/MenuButton`，分段用 `ui.Segmented`，列表用 `ui.List`，表格用 `ui.Table`，树用 `ui.Tree/TreeItem`，页签用 `ui.Tabs`，弹层用 `ui.Modal/Popover/AlertDialog`，提示用 `c.Toast`/`Element.Tooltip`，Markdown 用 `ui/markdown.View/Stream`，diff 用 `ui/editor.DiffView`，图表用 `ui/chart.Plot`。 |
| 2 | **布局图元只做布局** | `Row/Column/Box/Spacer/Divider/Scroll/Center/Stretch` 可用，但**禁止**用 `Row+Text` 拼按钮、用 `Column` 拼 Tabs、用 `Text` 拼 Badge、用 `Box` 拼开关。反例：old `chrome.go:285` 的 `chromeIconRow`、`workspaces.go` 的 session row。 |
| 3 | **组合件只放 uikit** | 页面头/区块/键值行/空态/多字段表单/tool-card 外框/composer 卡片等"原生控件组合"统一放 `internal/uikit`，模块不得各自复制布局代码。 |
| 4 | **禁止任何自绘** | 不得 Canvas/Painter/NewBitmap 画控件、不得自定义文本排版/滚动/焦点/hover/动画；品牌资产（logo/图标）用 `ui.MustParseSVG` 解析 SVG 作为内容，不算自绘。 |
| 5 | **状态归模块实例** | 禁止包级可变状态、`map[*App]` 式全局表；tab 选中、草稿、折叠状态等放模块闭包 struct（一实例一份）。 |
| 6 | **帧内构建、无副作用** | Build 只读数据和构建 Element；不发请求、不写设置、不启动 goroutine；副作用放到 `OnClick`/`OnSubmit` 回调。 |
| 7 | **跨线程只 Post** | 后台/agent goroutine 只调用 `UIRuntime.Post(fn)`，fn 里改模块状态并 `Invalidate()`；不直接改 UI 状态，不持有 `*ui.Context`/`Element`/`Handle`。 |
| 8 | **注册走 Effect** | Screen/Slot/SettingsPage/Command/ToolRenderer 注册必须把 unregister 交给 `ctx.Effect`（kernel 逆序注销），否则模块卸载泄漏。 |
| 9 | **主题/文案/尺寸统一** | 颜色/字体/间距/圆角走 `c.Theme()` 与 `internal/design`；文案走 `Locale` 服务（配置化：模块 Config 提供默认文案）；窗口断点/布局策略归宿主（uiroot 区域），模块不自定窗口几何。 |
| 10 | **每个 UI 模块带 headless 测试** | `ui.NewTester`/fake frame：注册 → 渲染断言 → 卸载后贡献消失；`-race` 绿。 |

Review checklist（每个 UI PR 必查）：

- [ ] 新增文件没有 `ui.Canvas`/`ui.Painter`/`ui.NewBitmap`/`.Paint(`；
- [ ] 没有 `Row+Text` 拼控件、没有手写 tabs/list/modal/markdown/toast；
- [ ] 没有包级变量、没有跨帧缓存 `ui.Element`/`ui.Handle`；
- [ ] 跨线程写状态都经过 `runtime.Post`，并调用 `Invalidate`；
- [ ] 注册/注销成对走 `ctx.Effect`；
- [ ] import 边界符合 §1.5；文案/主题/尺寸走统一入口；
- [ ] 有 headless 测试覆盖新贡献。

### 2.2 mygo v0.7.4 可用控件清单

> 来源：`/root/go/pkg/mod/github.com/egoist/mygo@v0.7.4/ui`（`api_gen.go` 共 106 个导出构造函数；
> 另有 markdown/editor/richtext/chart 子包）。表中"用途"为本项目预期用法，不是 mygo 的完整能力说明。

**A. 布局 / 图元（允许直接用于布局，不可拼控件）**

| 控件/API | 用途 |
|---|---|
| `Row`、`Column`、`Box`、`Spacer`、`Divider` | 横向/纵向/容器/撑开/分隔线 |
| `Scroll`、`ScrollBoth`、`Split`、`SplitPercent`、`SplitVertical`、`SplitVerticalPercent` | 滚动区、分栏、可拖拽分隔 |
| `Grid`、`GridView` | 网格布局 / 虚拟网格 |
| `Center`、`Stretch`、`SpaceBetween`、`Align` 等对齐常量 | 对齐策略值（不是控件本体） |

**B. 按钮 / 表单（P0–P2 高频）**

| 控件/API | 用途 |
|---|---|
| `Button`、`ButtonBase`、`PrimaryButton` | 普通/可组合/主操作按钮 |
| `TextInput`（+Base/Buffer）、`TextArea`（+Base/Buffer）、`SearchField`、`EditableText` | 输入框、多行、搜索、可编辑文本 |
| `Select`、`Combobox`、`Autocomplete`、`TokenField`、`MenuButton`、`PopoverMenuButton` | 下拉、可输入下拉、自动补全（@ 提及）、标签输入、菜单按钮 |
| `Segmented`、`Toggle`、`ToggleGroup`、`Switch`、`Checkbox`、`CheckboxGroup`、`RadioGroup` | 分段选择、开关、复选、单选 |
| `Slider`、`RangeSlider`、`StepSlider`、`Stepper`、`NumberInput`、`DecimalInput` | 数值/范围/步进 |
| `DateInput`、`TimeInput`、`Calendar`、`ColorPicker`、`ColorWell`、`Rating`、`FindBar` | 日期/时间/日期选择/颜色/评分/查找条 |
| `Form`、`Field`、`Fieldset`、`Link` | 表单容器、字段标签、字段组、链接 |

**C. 导航 / 容器 / 弹层（P0–P3 高频）**

| 控件/API | 用途 |
|---|---|
| `Sidebar`、`SidebarItem`、`SidebarSection` | 侧栏（键盘/树语义/首字母搜索自带）；主侧栏、设置导航、会话树 |
| `Tabs`、`TabsBase` | 页签（Session 的 Chat/Trajectory/Memory；右栏） |
| `Toolbar` | 工具条容器（composer 工具栏、页头动作条） |
| `Collapsible`、`CollapsibleBase`、`Accordion`、`AccordionItem` | 折叠区（思考行、工具卡详情、设置分组） |
| `Breadcrumbs` | 面包屑（文件路径） |
| `Modal`、`DialogBase`、`AlertDialog`、`Overlay`、`Popover`、`PopoverBase` | 弹窗/确认框/遮罩/浮层（设置、重命名、确认删除、菜单） |
| `Menu`/`ContextMenu`（Element 方法，`ui/menu.go`） | 节点右键/上下文菜单 |
| `Tooltip`（`Element.Tooltip`/`TooltipBase`） | 悬浮提示 |
| `Avatar`、`Badge`、`Icon`、`Image`、`Video`、`RichText` | 头像、徽标、图标、图片、视频、富文本 |

**D. 数据 / 集合（P1–P3）**

| 控件/API | 用途 |
|---|---|
| `List`（虚拟列表：`ListState` + `ItemKey` + `Selection` + `Rows`） | transcript/轨迹/插件/文件/搜索结果的唯一列表方案，替代手写滚动列表 |
| `Table`（`TableColumn` + `ListState`） | 设置表格、自动化运行历史、诊断键值表 |
| `Tree`、`TreeItem` | 工作区→会话树、文件树 |
| `Grid`、`GridView` | 模型卡片、插件卡片等网格 |

**E. 反馈 / 系统**

| 控件/API | 用途 |
|---|---|
| `c.Toast`、`c.ToastAction`、`c.AddToast`、`c.CloseToast`（`ui/toast.go`）、`ToastBase`、`ToastViewportBase` | 轻提示（成功/错误/复制），由宿主 viewport 统一渲染 |
| `Spinner`、`Progress`、`Meter` | 加载、进度、容量 |
| `Theme`/`LightTheme`/`DarkTheme`/`Context.SetTheme` | 明暗主题；产品只做 token 覆盖 |
| `Preferences`（Accent/ReduceMotion/HighContrast）、`RegisterFont`/字体族 | 桌面偏好、字体/DPI |
| `ui.Render`、`NewTester`、`ui/inspector`、`ui/automation` | headless 渲染/交互测试、调试 |

**F. 内容 / 富媒体（old 自研重灾区的替代品）**

| 包/API | 用途 |
|---|---|
| `ui/markdown`.`View`、`Stream`、`Options{Theme,Extensions,OnLink,Anchor…}` | 消息 Markdown 渲染/流式；表格、代码块（自带 Copy）、数学、链接拦截 |
| `ui/markdown`.`DefaultTheme`、`DefaultCodeBlock`、`copy.go`、`table.go` | 代码块/复制（Markdown/HTML）/表格 |
| `ui/editor`.`View`、`DiffView` | 代码编辑器（语法高亮/多光标/查找替换）与 **unified diff 视图**（workspace diff 用） |
| `ui/richtext`.`Editor` | Markdown/HTML/RTF/JSON 富文本编辑（P3 artifact 编辑可选） |
| `ui/chart`.`Plot` | 用量/统计图表（P3 诊断页可选） |
| `ui/access.go`（Role/Value/Focusable 等）、`focusgroup`、`Context.Shortcut` | 无障碍/键盘/焦点；原生控件自动上报 a11y 树 |

**G. 明确不用**

`ui.WebView`（禁止 webview 混合方案）、`Canvas`/`Painter`/`NewBitmap`（禁止自绘）、
`ui.Router`（产品导航用 uiroot `Navigator` 服务，避免两套路由）。

### 2.3 old 手搓 → native 替换对照（重点）

| old 手搓实现 | native 方案 | 备注 |
|---|---|---|
| 手写列表行：`workspaces.go:353`（session row）、`plugins_ui.go` 行、settings 行 | `ui.SidebarItem` / `ui.List`（ItemKey+Selection）/ `ui.Table` | 键鼠/首字母/滚动/a11y 免费 |
| 手写 Tabs：`trajectory.go:147 liveTabs`、`right_sidebar.go:165-197` tab strip | `ui.Tabs`（内容区仍由模块自绘内容，但 tab 条用 native） | 右栏 tab 若需自定义样式，用 `TabsBase` 组合 |
| 手写弹窗/overlay：`session_search.go:524`、`menus.go:110 aboutDialog`、`:130 shortcutsDialog`、`:263 renameDialog` | `ui.Modal`/`ui.AlertDialog`/`ui.Popover` + 宿主 overlay 槽位 | Esc 链/z-order 仍归宿主 |
| 手写 Markdown：`markdown.go:105`（628 行 parser/render） | `ui/markdown.View/Stream` | 流式 + 代码 Copy + 表格 + 数学免费 |
| 手写工具卡：`toolcards.go:111/119/134/238`（7 变体） | `ui.Collapsible` + `ui/markdown`（代码块）+ `ui.Badge` + `ui.Row/Image` | 变体只决定内容，不决定控件 |
| 手写下拉菜单：`home_live_menus.go:214/291/341`、`popover_menus.go:77` | `ui.Select`/`ui.MenuButton`/`ui.Popover`/`Element.Menu` | 键盘/溢出/焦点免费 |
| 手写 toast：`chromeToast`（`chrome.go:377`）、deferred toast | `c.Toast` + 宿主 viewport | 帧内入队；帧外先存状态下一帧 flush |
| 手写 hover/focus 样式：`.Hovered()` 78 处、`.Focusable()` 40 处、`.Role()` 31 处 | 原生控件内建 hover/focus/role | 只保留 theme token 覆盖 |
| 手写设置行/表单：`settings.go:177 settingsRow`、`settings_ui.go` 行、`models_ui.go:325 modelRow` | `ui.Form`/`ui.Field`/`ui.Fieldset` + 原生 Switch/Select/Segmented/TextInput | 设置页统一模板 |
| 手写 diff 着色：`workspace_changes_ui.go:209` | `ui/editor.DiffView` | P3 |
| 手写虚拟滚动/列表 | `ui.List`（`ListState`/`ItemKey`/`Selection`） | transcript/轨迹/搜索统一 |
| 手写 i18n 匹配/格式化：`i18n.go:109 resolveLanguage` | mygo `i18n`（CLDR）+ 产品 Locale 服务 | 文案目录仍保留 |
| 手写动效：`design/motion.go` 313 行（22/35 导出零引用） | 原生 `Transition`/`Animate`/`Collapsible` 动画 | 删除未用 token |
| 手写图标库：`internal/icons`（26 个 SVG）+ 52 处内联 `MustParseSVG` | 统一 `ui.MustParseSVG` + 产品图标资源包 | mygo 无图标库，图标是资产不是控件 |
| 手写 a11y 补丁：4 个 a11y 测试文件（703 行） | 原生控件自动 a11y | 只保留关键交互断言 |

### 2.4 native 能力边界与"允许的组合"

mygo 原生控件覆盖不了、允许在 `uikit` 组合的部分（**只组合原生控件**）：

1. **页面模板**：`PageHeader`/`Section`/`KeyValueRows`/`EmptyState`（已存在）。
2. **composer 卡片**：`TextArea` + `Row/Toolbar` + `PrimaryButton/ButtonBase` + 阴影/圆角只通过 Element 样式属性；
   不手写输入/光标/滚动。
3. **消息行外壳**：`Row/Column` 容器 + `markdown.View/Stream` + `Collapsible` + `Badge` + `Button`；
   "气泡"只是容器样式，不是自绘控件。
4. **工具卡外壳**：`Collapsible`（标题/展开状态）+ 内容用 markdown/Image/Table/List 原生控件。
5. **diff 卡**：`Collapsible` + `editor.DiffView` + `Badge`。
6. **设置页模板**：SettingsPageSpec + `Form/Field/Fieldset` + 原生控件；不手写导航（设置导航由宿主 Sidebar 渲染）。
7. **品牌层**：logo/图标资产 + Theme token 覆盖（颜色/字体/圆角），不重写控件形态。

明确**不允许**：自绘控件、自写文本布局、自写滚动/虚拟列表、自写焦点/hover、自写 Markdown/编辑器/diff、webview/H5 混合。

### 2.5 版本/资产策略

- **锁定 mygo v0.7.4**（0.x；升级前先跑 uikit 与 16 个模块的 headless 测试，必要时加适配层）。
- 每模块 Config 提供默认文案（中英），语言切换走宿主 Locale 服务；文案目录从 old `i18n.go` 平移（350 键量级）。
- 品牌图标统一收进产品资产包（`internal/ui/assets` 或 `internal/icons`），模块只引用图标常量；
  禁止模块各自内联 SVG 字符串。
- 主题：宿主从桌面偏好 + 设置解析 `ui.Theme`，模块只读 `c.Theme()`；产品只覆盖 token（~20 个颜色 + 圆角/间距）。

## 3. 旧功能矩阵

> 矩阵按 old/internal/shell 的页面/面板/文件梳理。表中 old 路径省略公共前缀 `/data/mygo/old/internal/shell/`
> （G-01、X-* 等跨包条目写了完整相对路径）。`新落点` 用现有/待建模块 id；
> `阶段` 见 §4；`原生控件` 只列主要控件（详细约束见 §2）；`旧数据依赖` 指是否必须迁移 old store/session 数据才能实现。
> old shell 总计：59 个生产文件 / 25,229 行（100 个测试文件 / 29,465 行，见 §7）。

### 3.1 全局 / 宿主骨架

| # | old 参考（路径:行） | 旧行为摘要 | 新落点 | 阶段 | 原生控件选型 | 旧数据依赖 |
|---|---|---|---|---|---|---|
| G-01 | `old/internal/app/app.go`、`singleinstance*.go`、`quit.go`、`window_geometry.go` | 单实例、窗口创建、关闭到托盘、退出顺序、窗口几何持久化 | `internal/host`（已有）+ `internal/app` 职责并入 host | P0 已有 / P4 托盘细节 | 宿主使用 mygo 窗口；UI 模块不建窗口 | 否 |
| G-02 | `shell/chrome.go:81`（品牌行）、`:98`（New Session）、`:146`（折叠 rail） | 侧栏品牌、主 CTA、折叠成图标轨 | uiroot 宿主 chrome + `zeokeel-ui/sidebar`（`sidebar.primary` 槽） | P0 | `ui.Sidebar`/`PrimaryButton`/`ButtonBase`/`Icon` | 否 |
| G-03 | `shell/workspaces.go:185`（导航项）、`:195`（Workspaces 头）、`:233`（树）、`:279`（tree 容器） | Home/Session/Plugins/Automation 导航；工作区→会话树；状态点/相对时间/行操作 | `zeokeel-ui/sidebar`（P0 会话列表；P3 工作区树/操作） | P0 / P3 | `SidebarItem`/`SidebarSection`/`Tree`/`TreeItem`/`Badge`/`Tooltip` | 是（P1 起 sessions/workspaces） |
| G-04 | `shell/session_search.go:188`（overlay 栈）、`:135-158`（Esc 链） | 通用 overlay 栈、Esc 逐层关闭、任务快捷键 | `internal/uiroot`（已有 `overlay.*` 槽位与设置 modal） | P0 宿主 / P2 补 Esc 优先级 | `ui.Overlay`/`Modal`/`Popover`（宿主渲染） | 否 |
| G-05 | `shell/app.go:394-427`（toast viewport）、`chrome.go:377`（chromeToast） | 窗口顶部 toast 带、pass-through、延迟 toast | `internal/uiroot` 宿主 chrome（P1 增加 toast viewport）；模块统一 `c.Toast` | P1 | `c.Toast`/`ToastBase`/`ToastViewportBase` | 否 |
| G-06 | `shell/shortcuts.go:25`（installShortcuts）、`:171-189`（13 行默认表）、`:369`（7 条菜单命令）、`:427`（HandleMenuAction） | 窗口级快捷键、命令菜单、`keybinding.*` 用户覆盖 | host Command 服务 + `UIRuntime.RegisterCommand`（P2）；全局热键 P4 | P2 | `Element.Shortcut`/`Element.Menu`/`MenuItem.Shortcut` | 否（设置存储） |
| G-07 | `old/internal/app/settings_window.go` | 非模态独立设置窗口、主窗/设置窗共享 `*App`、双窗重绘 | `internal/host` 窗口能力（P4）；Phase-1 用 uiroot `Modal` 兜底 | P4 | `ui.Modal`（fallback）→ 独立 mygo 窗口 | 否 |
| G-08 | `shell/menus.go:48`（moreFooter）、`:86`（moreMenu）、`:110`（About）、`:130`（快捷键表）、`:263`（重命名） | More 账户菜单、About、快捷键编辑器、重命名/删除确认 | `zeokeel-ui/settings`（About/Shortcuts 页）+ 宿主 dialogs 槽位 | P2 / P3 | `Modal`/`AlertDialog`/`Field`/`TextInput`/`Table` | 部分（设置存储） |
| G-09 | `shell/settings_ui.go:96`（themeTokens）、`preferences.go`、`motion.go` | 明暗主题、字体缩放、高对比、减少动效 | `internal/host` + uiroot theme/Preferences；设置页 P2 | P2 | `Theme`/`Preferences`/`Transition` | 否（设置存储） |
| G-10 | `shell/i18n.go:44`（中文文案表）、`:66`（T） | 350 键中英、语言解析/持久化、`tr(en,zh)` | uiroot Locale 服务 + 产品文案目录；模块 Config 默认文案 | P2 | mygo `i18n` + Locale 服务 | 否 |
| G-11 | `shell/assets.go`、`buildinfo.go`、`old/internal/icons`（26 SVG） | logo/wordmark、版本号、26 个图标资产 | `internal/ui/assets` + appinfo 模块（已有） | P0 最小 / P2 全量 | `ui.MustParseSVG`/`Image`/`Text` | 否 |

### 3.2 Home

| # | old 参考 | 旧行为摘要 | 新落点 | 阶段 | 原生控件选型 | 旧数据依赖 |
|---|---|---|---|---|---|---|
| H-01 | `home.go:67`（home）、`:74`（live）、`:100`（未配置模型 setup card）、`:173`（hero） | 落地页：hero、setup card（无模型时引导配置）、工作区入口 | `zeokeel-ui/home`（现有示范模块升级） | P2 | `Column`/`Text`/`Button`/`Image` | 否 |
| H-02 | `home.go:205`（chips）、`home_live_menus.go:154/175`（权限）、`:254/291`（模型）、`:341`（推理强度）、`:363`（工作区选择） | 工作区/模式/权限/模型/effort chip + popover 菜单 | `zeokeel-ui/home` + composer chips 服务 | P2 | `Select`/`MenuButton`/`Popover`/`Segmented`/`Badge` | 是（设置/模型配置） |
| H-03 | `home.go:271`（composer 宿主块）、`:298`（card）、`:341`（toolbar）、`:361`（sendHomeDraft） | Home composer；发送即创建会话并提交首条消息；附件随行 | `zeokeel-ui/composer`（Home 变体）+ `KeyChat`（P0 已有发送能力） | P2 | `TextArea`/`Toolbar`/`PrimaryButton`/`Row` | 否 |
| H-04 | `attachments_ui.go:41`（选择）、`:112`（staging）、`:147/161/177`（home 附件）、`:208`（chips） | 文件/图片选择、chips、随首条消息附加、删除清理 | `zeokeel-ui/composer` + 产品 attachments 服务 | P2 | `ButtonBase`/`Badge`/`Image`/`Row` | 是（附件存储 P1/P2） |
| H-05 | 新架构预留 `home.recent` 槽（old 无独立区块） | 最近会话入口（可选，old 仅有侧栏列表） | `zeokeel-ui/home` | P2（可选） | `List`/`SidebarItem` | 是（sessions） |

### 3.3 Session（对话主界面，最重）

| # | old 参考 | 旧行为摘要 | 新落点 | 阶段 | 原生控件选型 | 旧数据依赖 |
|---|---|---|---|---|---|---|
| S-01 | `session.go:30`（session）、`session_live.go:61`（sessionLive） | 会话屏骨架：header + transcript + composer 三段 | `zeokeel-ui/session`（P0 已有目录） | P0 | `Column`/`Scroll`/`Row` | 否 |
| S-02 | `session_live.go:154`（liveHeader）、`home_live_menus.go:412`（更多菜单）、`:506`（header actions） | 标题、工作区 chip、模式 chip、preset chip、阶段标签、右栏开关、更多（fork/export/feedback） | `zeokeel-ui/session`（P1 基础）/ `zeokeel-ui/goal`、`zeokeel-ui/settings`（preset P3） | P1 / P3 | `Text`/`Badge`/`Popover`/`MenuButton`/`Select`/`Icon` | 是（title/session） |
| S-03 | `trajectory.go:95`（layout）、`:147`（liveTabs） | Chat / Trajectory / Memory 三个页签（按 Show coding view 切换） | `zeokeel-ui/session`（P0 占位；P1 轨迹；P3 记忆页） | P0 / P1 / P3 | `ui.Tabs` | 轨迹依赖 SessionLog（P1） |
| S-04 | `session_live.go:351`（transcriptEmpty） | 空会话/首次运行引导（图标+说明+主操作） | `zeokeel-ui/session` | P0 | `Text`/`Button`/`Icon` | 否 |
| S-05 | `session_live.go:690`（liveMessage）、`markdown.go:105`（markdown 自研渲染） | 消息行：role、Markdown、流式、状态、时间 | `zeokeel-ui/session` | P1 | `markdown.View`/`Stream`/`Row`/`Text` | 是（messages + reasoning 事件） |
| S-06 | `reasoning_ui.go:340`（liveReasoningRow） | 思考行：running 单行预览/透明度脉冲、完成折叠、展开 mono 全文 | `zeokeel-ui/session` | P1 | `Collapsible`/`Text`/`Badge` | 是（reasoning 事件，P1） |
| S-07 | `session_live.go:741/780`（referenceContextOpen/liveReferenceContextRow）、`old/internal/sessionreference`（713 行） | 跨会话引用：canonical mention、只读快照折叠行 | `zeokeel-ui/session` + 产品 sessionreference 服务 | P3 | `Collapsible`/`Text`/`Badge` | 是（SessionLog + 引用服务） |
| S-08 | `message_actions.go:33`（actions）、`:186`（usage footer） | 复制/重试/反馈、每轮 usage（tokens/耗时/上下文%） | `zeokeel-ui/session` | P1（usage）/ P3（copy/retry/feedback） | `Button`/`ButtonBase`/`Toolbar`/`Text` | 是（messages/usage/feedback） |
| S-09 | `session_errors.go:116`（liveErrorCard） | 错误卡片分类（网络/鉴权/限流/取消）与建议 | `zeokeel-ui/session` | P1 | `Badge`/`Text`/`Button` | 是（events/error） |
| S-10 | `toolcards.go:111`（cards）、`:119`（card）、`:134`（row）、`:238`（detail）、7 变体 | 工具卡：命令/文件/搜索/web/图片/提问/通用；折叠、状态点、结果面板 | `zeokeel-ui/toolcards`（现有示范模块升级） | P1 | `Collapsible`/`Badge`/`Row`/`Image`/`markdown` | 是（ToolCall/Result 读模型） |
| S-11 | `trajectory.go:388`（buildTrajectory）、`:454`（label）、`:370`（entry） | 轨迹页：按事件序列展示/过滤/错误高亮/时间轴条 | `zeokeel-ui/session`（轨迹） | P1 | `List`/`Scroll`/`Text`/`Badge` | 是（SessionLog） |
| S-12 | `memory_ui.go:129`（sessionMemoryPane）、`:170`（memoryManager） | 会话 Memory 页签：工作区记忆管理（Off/Suggest/Auto、待审核、增删改） | `zeokeel-ui/memory` + 产品 memory 模块 | P3 | `List`/`Segmented`/`TextInput`/`Button` | 是（MemoryStore） |
| S-13 | `session_live.go:874`（liveComposerArea，固定顺序） | composer 区：plan 卡 → question 卡 → 审批卡 → slash palette → @ 菜单 → 附件菜单 → composer → usage footer | `zeokeel-ui/composer`（服务）+ 各特性模块贡献 composer 槽 | P0 基础 / P2 完整 | `Column`/`Row`/`TextArea`/`Toolbar` | 部分（审批/plan 后端） |
| S-14 | `session_live.go:916`（sessionComposerCard）、`:992`（liveRunButton） | composer 卡片：多行输入、+ 附件、权限 chip、plan badge、模型 badge、发送/停止 | `zeokeel-ui/composer` | P2 | `TextArea`/`Row`/`PrimaryButton`/`ButtonBase`/`Badge` | 否 |
| S-15 | `plan_ui.go:394`（planReviewCard）、`:332` 附近（composerPlanBadge）、`planUIState` | plan 评审卡：review/keep planning/pending；plan 模式徽标 | `zeokeel-ui/plan` + host `PlanController`（hosttools） | P3 | `Column`/`Button`/`Badge`/`Text` | 是（plan artifact） |
| S-16 | `session_live.go:945`（approvalCardsWithAlways）、`composer_menus.go:76/184`（pendingApprovals/approvalCards） | 审批卡：Allow/Deny/Always allow；auto-review 拒绝原因展示；Esc 先行 deny | `zeokeel-ui/approval` + `rig.Asker`/`gate.Ask` | P1 | `Row`/`Button`/`PrimaryButton`/`Badge`/`Text` | 后端 pending 集（P1） |
| S-17 | `todo_ui.go:142`（questionCards）、`:201/236`（track/answer） | ask_user 提问卡：选项/自定义输入/提交/错误 | `zeokeel-ui/todo`（questions 或独立 `ui/questions`） | P3 | `Field`/`TextInput`/`Button`/`Column` | 是（questions 服务） |
| S-18 | `todo_ui.go:44`（liveTodoPanel）、`:97`（todoRow） | Todo 侧板：条目/状态/顺序 | `zeokeel-ui/todo` | P3 | `List`/`Badge`/`ButtonBase` | 是（todos） |
| S-19 | `team_ui.go:117`（teamGoalBanner）、`:164`（teamPanel）、`:34/63`（roster/stop） | goal banner（进度/状态）+ 异步 team 面板（成员/状态/停止） | `zeokeel-ui/goal` + `zeokeel-ui/team` | P3 | `Text`/`Badge`/`List`/`Button`/`Progress` | 是（goal/team 服务） |
| S-20 | `workspace_changes_ui.go:60`（liveWorkspaceChanges）、`:164`（workspaceDiffPanel）、`:209`（着色） | 每回合工作区变更卡：文件列表、+/- 着色、binary/oversized 标记、点击打开 | `zeokeel-ui/diff` + 产品 workspacechanges 模块 | P3 | `Collapsible`/`editor.DiffView`/`Badge` | 是（workspacechanges 事件） |
| S-21 | `todo_ui.go:273`（liveArtifacts）、`files_root.go:101`（artifactPreviewRel） | presented artifacts 列表/预览入口 | `zeokeel-ui/todo`（artifacts）+ `zeokeel-ui/files` | P3 | `List`/`Button`/`Modal` | 是（artifacts 服务） |

### 3.4 Composer（输入区）

| # | old 参考 | 旧行为摘要 | 新落点 | 阶段 | 原生控件选型 | 旧数据依赖 |
|---|---|---|---|---|---|---|
| C-01 | `composer_native.go:20`（card）、`:35`（input）、`:70`（icon button）、`:91`（send button） | 品牌 composer 卡、多行输入、附件/发送按钮、focus/hover 边框 | `zeokeel-ui/composer`（P2） | P2 | `TextArea`/`ButtonBase`/`PrimaryButton`/`Row` | 否 |
| C-02 | `composer_slash.go:68`（16 条 slashCommands）、`:183`（filter）、`:652`（palette viewport）、`composer_menus.go:218`（commandPalette） | `/` 命令面板：分组、图标、标题+命令名+说明、键入过滤、↑↓/Tab/Enter/Esc | `zeokeel-ui/composer` + host Command 服务 | P2 | `Autocomplete`/`List`/`Popover`/`TextInput` | 否（命令目录静态） |
| C-03 | `composer_menus.go:249`（runSlashCommand 分发） | 16 条命令语义：/file /plan /feedback /permission /model /memory /goal /compact /export /clear /stop /help /new /settings /automate /plugins；未知 `/` 作为普通消息 | 特性模块各自注册 Command（/goal→ui/goal，/memory→ui/memory…）；composer 只做 palette | P2（基础命令）/ P3（goal/compact/memory/plan） | `Command` 注册表 + palette 原生控件 | 部分（会话/goal/memory 服务） |
| C-04 | `composer_menus.go:605`（mentionMenu）、`:562`（choices）、`session_search.go:789`（会话候选） | `@` 提及：文件/目录下钻、带引号路径、面包屑回跳、跨工作区会话候选 | `zeokeel-ui/composer`（文件 P2）+ sessionreference/会话候选（P3） | P2 / P3 | `Autocomplete`/`List`/`Popover`/`Text` | 是（文件树 P2；sessions P3） |
| C-05 | `composer_menus.go:943`（attachmentMenu）；`attachments_ui.go:208`（chips） | 附件菜单/选择器、已选 chips、删除 | `zeokeel-ui/composer` + attachments 服务 | P2 | `ButtonBase`/`Image`/`Badge`/`Row` | 是（附件存储） |
| C-06 | `home_live_menus.go:175/214`（权限）、`:254/291`（模型）、`:341`（effort）、`modes_ui.go`（模式） | 权限/模型/模式/推理强度菜单；按会话生效 | `zeokeel-ui/composer` + 设置/模型服务 | P2 | `Select`/`MenuButton`/`Popover`/`Segmented`/`Badge` | 是（settings/models） |
| C-07 | `plan_ui.go:332`（plan badge）、`composer_goal_compact.go:40`（/goal）、`:161`（/compact） | plan 模式徽标、goal/compact 命令（本地执行不进模型） | `zeokeel-ui/plan`、`zeokeel-ui/goal` + Command | P3 | `Badge`/`Command`/`Toast` | 是（plan/goal/compact 服务） |
| C-08 | `draft_scope.go`、`session_restore.go:26/43/93`、`composer_slash.go:139`（menu state/caret） | 草稿按会话隔离、启动恢复、输入法/光标、菜单 caret 对齐 | `zeokeel-ui/composer` + store（P1 持久化草稿可选） | P1/P2 | `TextArea`/`Handle`（帧内） | 是（draft 设置） |

### 3.5 侧栏 / 工作区 / 搜索

| # | old 参考 | 旧行为摘要 | 新落点 | 阶段 | 原生控件选型 | 旧数据依赖 |
|---|---|---|---|---|---|---|
| W-01 | `workspaces.go:195`（header）、`:233`（workspaceTree）、`:279`（sidebarTree） | Workspaces 头（搜索/视图/加号）、工作区→会话树、折叠状态 | `zeokeel-ui/sidebar`（树 P3） | P3 | `SearchField`/`Button`/`Popover`/`Tree`/`SidebarItem` | 是（workspaces/sessions） |
| W-02 | `workspaces.go` 会话行（状态点/时间/hover 操作，配合 `:1122` pin）、`sidebar_hover.go:26` | 会话行：运行状态点、相对时间、hover 动作、置顶/归档/重命名/删除 | `zeokeel-ui/sidebar` | P0 基础行 / P3 完整 | `SidebarItem`/`ButtonBase`/`Popover`/`Badge`/`Text` | 是（sessions） |
| W-03 | `workspaces.go:603`（拖拽边）、`:795`（工作区排序）、`:947`（会话排序） | 工作区/会话手动拖拽排序，持久化顺序键 | `zeokeel-ui/sidebar`（native drag + List） | P3 | `Draggable`/`DataDragOver`/`DropData`/`List` | 是（sessions/workspaces） |
| W-04 | `chrome.go:146`（sidebarRail） | 侧栏折叠为图标轨，Cmd/Ctrl+B 恢复 | uiroot 宿主 chrome | P3 | `ButtonBase`/`Icon` | 否 |
| W-05 | `menus.go:48/86`（moreFooter/moreMenu） | 底部更多菜单（关于/设置等） | uiroot 宿主 chrome + `zeokeel-ui/settings` | P2/P3 | `Popover`/`Menu`/`ButtonBase` | 否 |
| W-06 | `session_search.go:524`（sessionSearchDialog）、`:135-158`（Esc 链）、`:188`（overlay） | Cmd+K 会话搜索弹窗：标题/内容匹配、结果列表、排序/归档过滤、键盘导航 | `zeokeel-ui/workspaces`（或 `zeokeel-ui/session-search`）+ SessionLog.Search | P3 | `Modal`/`SearchField`/`List`/`Segmented` | 是（SessionLog） |
| W-07 | `session_search.go:215`（openInlineSearch）、`:460`（viewOptions） | 会话内联搜索、视图选项（分组/归档/排序） | `zeokeel-ui/session`（inline）+ ui/sidebar（视图选项） | P3 | `SearchField`/`Segmented`/`Popover` | 是（settings 键） |
| W-08 | `sessions_cache_async.go:77`（异步缓存） | 会话列表异步加载/缓存/TTL | `zeokeel-ui/sidebar`（读服务，缓存归后端） | P1 | 无（数据层） | 是（store） |
| W-09 | `session_search.go:789/835`（mention 会话候选/排序） | `@` 会话候选（同 cwd 优先、canonical mention） | `zeokeel-ui/composer` + sessionreference | P3 | `Autocomplete`/`List` | 是（SessionLog） |

### 3.6 Settings（14 页 + 独立窗口）

| # | old 参考 | 旧行为摘要 | 新落点 | 阶段 | 原生控件选型 | 旧数据依赖 |
|---|---|---|---|---|---|---|
| T-01 | `settings_ui.go:256`（panel）、`:347`（nav）、`:421`（body switch）、`settings.go:33/177` | 设置壳：导航列 + 页头 + 滚动页体 + 页脚；14 页 switch 改为注册表 | `zeokeel-ui/settings`（shell）+ uiroot `RegisterSettingsPage` | P2 | `Sidebar`/`SidebarSection`/`Scroll`/`Form`/`Field` | 否（设置存储） |
| T-02 | General 页（`settings_ui.go` 行、`settings_extra.go` 部分） | 通用设置：会话文本大小、工作详情、Show coding view 等 | `zeokeel-ui/settings` | P2 | `Form`/`Switch`/`Select`/`Segmented` | 是（设置键） |
| T-03 | `models_ui.go:23`（枚举）、`:261`（modelsSettingsPage）、`:325`（modelRow）、`:619`（provider 目录） | 模型目录：provider/模型卡片、添加/编辑、连接检查、默认模型、推理强度 | `zeokeel-ui/settings` + 产品 `models` 服务 | P2 | `List`/`Table`/`Field`/`TextInput`/`Button`/`Select` | 是（modelcatalog/设置） |
| T-04 | Appearance/Language（`settings_ui.go` 行、`preferences.go`、`i18n.go`） | 主题/字体缩放/高对比/减少动效/Language | `zeokeel-ui/settings` + host theme/locale | P2 | `Select`/`Segmented`/`Switch`/`Slider` | 是（设置键） |
| T-05 | `memory_ui.go:32`（枚举）、`:107`（global 页）、`:170`（manager） | Memory 设置页：Global 作用域 Off/Suggest/Auto、待审核、增删改、清空、导出 | `zeokeel-ui/memory` | P3 | `List`/`Segmented`/`TextInput`/`TextArea`/`Button` | 是（MemoryStore） |
| T-06 | Builtin plugins 页（`plugins_ui.go:12`、`plugin_details.go`） | 内置插件开关（LSP/MCP/skills 等）、说明、重启提示 | `zeokeel-ui/plugins`（设置页贡献） | P3 | `List`/`Switch`/`Badge`/`Button` | 是（设置键） |
| T-07 | `presets_ui.go`（Agent presets 页） | Agent 预设（persona/tools/prompt）列表、编辑、选择 | `zeokeel-ui/settings` + `tools/presets`（工具子集） | P3 | `List`/`Field`/`TextArea`/`Button` | 是（presets 数据） |
| T-08 | `settings_extra.go:167`（Notifications）、`:191`（Data & privacy） | 通知开关/抑制聚焦；数据与隐私（清空/导出/重置） | `zeokeel-ui/settings` | P4 | `Form`/`Switch`/`Button`/`AlertDialog` | 是（设置/数据） |
| T-09 | `settings_extra.go:292`（Diagnostics）、`:333`（Network）、`:350`（Channel） | 诊断信息/复制；网络代理；更新通道 | `zeokeel-ui/settings` | P4 | `KeyValueRows`/`Text`/`Collapsible`/`Field` | 是（版本/设置） |
| T-10 | `settings_extra.go:397`（Desktop）、`:156`+（Updates） | 桌面行为（关闭/自启动）；更新检查/安装进度 | `zeokeel-ui/settings` + host/updater | P4 | `Form`/`Switch`/`Select`/`Button`/`Progress` | 是（设置/updater） |
| T-11 | `manage_ui.go:25`（MCP servers）、`:26`（Skills）、`:419/421`（页面） | MCP 服务器列表/配置；Skills 多根目录/开关 | `zeokeel-ui/settings` + `zeokeel/mcp`/`zeokeel/skills` 服务 | P3 | `List`/`Field`/`TextArea`/`Switch`/`Button` | 是（MCP/skills 配置） |
| T-12 | `plugin_details.go:659`（aboutSettingsPage）、`:26`（枚举） | About：版本/构建/支持入口 | `zeokeel-ui/about`（现有） | P2 | `uikit.KeyValueRows`/`Text`/`Divider` | 否（appinfo） |
| T-13 | `old/internal/app/settings_window.go` | 独立设置窗口（见 G-07） | `internal/host` | P4 | `ui.Modal` fallback → 独立窗口 | 否 |

### 3.7 Plugins / Automation 屏幕

| # | old 参考 | 旧行为摘要 | 新落点 | 阶段 | 原生控件选型 | 旧数据依赖 |
|---|---|---|---|---|---|---|
| P-01 | `plugins.go:122`（plugins 屏）、`:131`（header）、`:174`（section）、`plugins_ui.go:12`（builtinPluginDef）、`plugin_details.go:165/216`（详情） | 插件页：内置/实验分组、搜索/刷新、Add、About、插件详情字段编辑 | `zeokeel-ui/plugins` | P3 | `List`/`Switch`/`Badge`/`Button`/`Popover`/`Modal`/`Field` | 是（插件开关/配置） |
| P-02 | `plugin_details.go:354`（不支持原因）、`:516`（行描述） | 插件不可用原因/说明文案 | `zeokeel-ui/plugins` | P3 | `Badge`/`Text`/`Tooltip` | 否 |
| A-01 | `automation.go:33`（automation 屏）、`automation_ui.go:19`（page）、`:39`（heading）、`:57`（new）、`:72`（filters）、`:145`（task list）、`:199`（row） | 自动化页：标题、New、过滤/搜索、任务行（schedule/prompt/状态） | `zeokeel-ui/automation` + 产品 automation/scheduler 模块 | P3 | `Table`/`List`/`SearchField`/`Button`/`Badge` | 是（automations） |
| A-02 | `automation_ui.go:313`（run history）、`:366/374`（actions）、`:462`（dialog）、`:647`（run now） | 运行历史、启用/暂停/删除/立即运行、创建/编辑弹层 | `zeokeel-ui/automation` + `rig.Scheduler` | P3 | `Table`/`Form`/`Field`/`TextArea`/`Select`/`Modal`/`Button` | 是（automation_runs） |

### 3.8 右栏（Right sidebar）

| # | old 参考 | 旧行为摘要 | 新落点 | 阶段 | 原生控件选型 | 旧数据依赖 |
|---|---|---|---|---|---|---|
| R-01 | `right_sidebar.go:23-33`（rightTab 枚举）、`:65-113`（状态/宽度/缓存）、`:165-197`（容器/tab strip） | 右栏壳：dock/split/fullscreen、拖拽 resizer、4 个 tab、跨屏保持 | uiroot 宿主 chrome（右栏区域/`session.right.rail` 槽） | P3 | `SplitPercent`/`Tabs`/`ButtonBase`/`Icon` | 否 |
| R-02 | `start_tab.go:95`（startTabPanel）、`:202-277`（shell 探测） | Start 页：Workspace tools 卡片（文件/终端/会话日志）+ Choose shell | `zeokeel-ui/files` | P3 | `ButtonBase`/`Icon`/`Text`/`Popover` | 部分（终端 shell 设置） |
| R-03 | `right_sidebar.go:456`（filesPanel）、`:1194-1215`（路径工具）、`files_root.go:66`、`files_preview.go` | Files：面包屑/目录列表/文件预览/打开/在系统打开/图片预览 | `zeokeel-ui/files` + 产品 files 服务 | P3 | `List`/`Tree`/`Image`/`Button`/`Tooltip` | 是（workspace root/artifacts） |
| R-04 | `right_sidebar.go:989`（terminalPanel）、`:1147`（appendTerminal）、`start_tab.go:24-61` | Terminal：shell 选择、命令输入、输出累积、clear/stop；old 为 30s 一次性 exec，无 PTY | `zeokeel-ui/terminal` + rig `tools/terminal`（真 PTY） | P4（建议提前到 P3 可选） | `Scroll`/`TextInput`/`Button`/`Popover` | 否（可选用 rig terminal） |
| R-05 | `start_tab.go:382`（sessionLogPanel） | 会话原始事件日志（只读、TTL 缓存） | `zeokeel-ui/files` + SessionLog | P3 | `List`/`Scroll`/`Text`/`Badge` | 是（SessionLog） |
| R-06 | `app.go:394-427`（toastViewport） | 窗口 toast 层（主窗/设置窗） | uiroot 宿主 chrome | P1 | `c.Toast`/`ToastViewportBase` | 否 |

### 3.9 shell 之外的 old 能力（产品层）

| # | old 模块（行数） | 旧行为摘要 | 新落点 | 阶段 | 依赖 |
|---|---|---|---|---|---|
| X-01 | `old/internal/prompt`（1,398） | system prompt 组合、plan/memory/AGENTS.md 注入、registry | 产品 `internal/modules/prompt`（或并入 agent 模块） | P1/P2 | rig `AgentSpec.SystemPrompt` |
| X-02 | `old/internal/state`（1,345） | 事件 projector、compaction 事件、broker（UI 读模型来源） | ChatService 读模型 / `internal/modules/agent`（P0 起） | P0/P1 | rig.Event |
| X-03 | `old/internal/workspacechanges`（1,134） | 回合 diff 计算/内容对比/限额 | 产品模块 + `zeokeel-ui/diff` | P3 | rig tools（read/bash）/sandbox |
| X-04 | `old/internal/sessionreference`（713） | 会话引用 canonical mention + 受限只读快照 | 产品模块 + session UI | P3 | rig.SessionLog |
| X-05 | `old/internal/memory`（1,059） | 双层记忆抽取/整合/inbox | 产品 memory 模块 + rig `MemoryStore`/`tools/memory` | P3 | rig.MemoryStore |
| X-06 | `old/internal/skills`（772） | 多根 skill 加载、开关 | rig `tools/skill`+`SkillStore` + 设置页 | P3 | rig.SkillStore |
| X-07 | `old/internal/automation`（398） | cron scheduler + 任务执行 | rig `tools/schedule`+`Scheduler` + 产品执行器 | P3 | rig.Scheduler |
| X-08 | `old/internal/modelcatalog`（357） | 供应商/模型目录、上下文窗口/推理强度 | 产品 models 服务 + 设置页 | P2 | 设置/`provider/openai` |
| X-09 | `old/internal/presets`（645） | Agent 预设（persona/tools/prompt） | 产品 presets 模块 + `tools/presets` | P3 | rig tools/presets |
| X-10 | `old/internal/websearch`（302） | HTTP 搜索后端 | 产品 `web.Searcher` 实现（rig tools/web） | P2/P3 | rig `web.Searcher` |
| X-11 | `old/internal/lsp`（1,347） | LSP client + 工具 + servers 配置 | rig `tools/lsp`（v0.13 已含 references/implementation/UTF-16） | P2/P3 | toolkit 配置 |
| X-12 | `old/internal/mcp`（1,171） | MCP server 管理（stdio/HTTP/resources/prompts） | rig `tools/mcp`（v0.13 含 notifications/templates） | P2/P3 | toolkit + 设置页 |
| X-13 | `old/internal/acp`（1,863） | ACP server（list/load/fs/terminal/permission） | 产品 `internal/modules/acp`（调用 rig agent） | P4 | rig agent/SessionLog |
| X-14 | `old/internal/globalhotkey`（3,869） | 全局快捷键（平台） | 产品 host 能力 | P4 | mygo/OS |
| X-15 | `old/internal/importer`（2,341） | 导入器/凭据迁移/附件 | 产品模块 | P4 | store/legacy 数据 |
| X-16 | `old/internal/updater`（678） | 自动更新 | 产品模块 | P4 | 打包/签名 |
| X-17 | `old/internal/i18n`（560）、`design`、`icons`、`brand` | 国际化/设计系统/图标/品牌 | 产品 Locale/design tokens/资产 | P0 最小 / P2 全量 | mygo Theme/i18n |
| X-18 | `old/internal/winproc`（154）、`e2e`、`shell` 专项测试 | Windows 进程辅助、E2E/视觉/a11y 专项 | 产品 host 辅助 + 新测试体系 | P4；测试随阶段 | — |

---

## 4. 阶段划分（P0–P4）

### 4.1 阶段总览

| 阶段 | 主题 | 交付模块 | 依赖 rig 契约 | 退出验收（摘要） |
|---|---|---|---|---|
| **P0（本轮）** | chat MVP：能配置模型、发消息、流式回复、停止、会话列表 | `zeokeel/agent`、`zeokeel-ui/session`、`zeokeel-ui/sidebar`、`zeokeel-ui/settings`；assembly/contracts 接线；go.mod 升 rig v0.13.0 | `rig.AgentSpec/RunRequest/Event`、`provider/openai`、`session.NewMemory`、`gate.AllowAll`、`full.All`/toolkit | `go test ./...`、fake-provider E2E、headless 三屏渲染、native-only 检查 |
| **P1** | 会话持久化 + 工具卡/轨迹 + 审批 UI（+压缩/检查点） | `zeokeel/store`；`zeokeel/agent` 升级（History/Gate）；`zeokeel-ui/toolcards`、`zeokeel-ui/session`（轨迹/usage/错误）、`zeokeel-ui/approval`；host `Asker` | `rig.History/HistoryReplacer`、`rig.SessionLog`、`rig.CheckpointStore`、`rig.Compactor`、`rig.SpillPolicy`、`gate.Ask`、`agent.RecoverInterruptedTurn` | 重启恢复会话；工具卡渲染；审批 allow/always/deny；压缩 + 中断恢复 E2E |
| **P2** | composer 增强 + 设置扩展 | `zeokeel-ui/composer`、`zeokeel-ui/home`（升级）；`zeokeel-ui/settings` 扩充；models 服务；host theme/locale | `rig.Asker/Presenter/FilePresenter`（hosttools）、`rig.ToolResult.Parts`、mygo markdown/editor/i18n | slash/@/附件/图片可用；模型目录/外观/语言设置可用 |
| **P3** | 记忆/技能/自动化/插件/搜索/fork/export/diff/规划/团队 | `zeokeel/memory`、`zeokeel/skills`、`zeokeel/automation`、`zeokeel/workspaces`、`zeokeel/files`；`zeokeel-ui/{memory,skills,automation,plugins,workspaces,files,diff,plan,goal,team,todo,approval 扩展}` | `rig.MemoryStore/SkillStore/Scheduler/TeamTaskManager/GoalStore/GoalUpdater/Reviewer/NoticeSource`、`agent.GoalRoundDriver`、`gate.Review`、`tools/presets` | memory/skill/automation 可用；会话搜索/fork/export；diff 渲染；goal driver |
| **P4** | 宿主集成 | `zeokeel/acp`、host 全局热键、`zeokeel/importer`、`zeokeel/updater`；设置独立窗口/诊断/桌面页 | 无新 SDK 契约（复用 SessionLog/agent） | ACP 会话可被编辑器驱动；热键；旧数据导入；更新检查 |

### 4.2 P0：chat MVP（本轮）

**P0-0 前置（集成方负责，不是 UI）**

1. `go.mod` 升级 `github.com/zeokeel/zeokeel-rig v0.13.0`（写作时升级进行中），同步 `go.sum`；
   toolkit/hosttools 按 v0.13 API 调整（`full.All` 的 `History/SessionLog/Host/Runner` 等选项；
   subagent 异步/present/bash 语义变化见 rig CHANGELOG）。
2. 冻结 Phase-1 契约：`internal/contracts/chat.go`（`KeyChat`/`ChatService`/读模型）——后续阶段只做加法。
3. `internal/assembly/assemblies/desktop.json` 增加 `agent` 后端模块与 `ui` 子组中的 `session`/`sidebar`/`settings` 模块；
   headless 不挂 UI（按现有约束）。

**P0 并行任务对照**（A/B/C 与本文 → 落点）

| 任务 | 模块 | 关键交付 | 依赖 | 验收 |
|---|---|---|---|---|
| A：agent 后端 | `internal/modules/agent`（id `zeokeel/agent`） | `Config{Model,BaseURL,ApiKeyEnv,SystemPrompt,MaxIterations,ContextBudget}`；`ChatService`：sessions/active/New/Delete/Send/Cancel/Subscribe；`rig.Config` 优先级（模块 Config → KeySettings `contracts.SettingsChat*` → env）；Emit→读模型；httptest SSE 假 provider 测试 | `KeyToolkit`、`KeySettings`、`KeyLogger`；rig v0.13 | `go test ./internal/modules/agent/... -race` |
| B：对话主界面 | `internal/ui/session`（id `zeokeel-ui/session`） | Screen `session`（Order 20）；Tabs（Chat/Trajectory/Memory 占位）；transcript（markdown/流式/reasoning 折叠/tool renderer/error）；composer TextArea + 发送/停止；订阅→Post→Invalidate；Config 文案 | `KeyUIRuntime/KeyNavigator/KeyChat`；`contracts.ToolCall` | headless 测试：空态/消息/流式/发送/取消/卸载 |
| C：侧栏+设置 | `internal/ui/sidebar`、`internal/ui/settings` | `sidebar.primary` 新对话；`sidebar.nav` 会话列表（SidebarItem、active 高亮、点击切换）；`sidebar.footer` 设置入口；设置页 `model`：Model/BaseURL/APIKey/SystemPrompt（API key 打码）保存到 `KeySettings`；订阅刷新 | `KeyChat/KeySettings/KeyUIRuntime/KeyNavigator` | headless：列表/空态/新对话/保存键值断言 |

**P0 验收标准（全部满足才算完成）**

- [ ] `go build ./... && go vet ./... && gofmt -l .`（无输出）。
- [ ] `go test ./... -count=1` 全绿；`go test ./internal/modules/agent/... ./internal/ui/... -race` 绿。
- [ ] 真实 assembly（desktop.json）+ headless `ui.NewTester` 能渲染三个屏：空会话、流式回复、设置页。
- [ ] fake OpenAI SSE 服务 E2E：Send → 文本逐字流式 → 完成；Stop → cancelled；第二次 Send 冲突报错；会话列表出现标题。
- [ ] 桌面真窗口（Xvfb）冒烟：输入 → 发送 → 回复可见；侧栏切换会话；设置保存后重启仍在（KeySettings 持久化）。
- [ ] native-only 检查：新增 UI 代码无 Canvas/Painter/NewBitmap；无 `Row+Text` 拼按钮；无包级状态；跨线程仅 Post。
- [ ] 依赖边界：`go list -deps ./internal/ui/...` 不含 `internal/modules`、`internal/host`、`internal/uiroot`。

**P0 明确不做**：持久化（重启丢会话）、真实审批（先用 `gate.AllowAll`）、工具卡内容细节（只放通用卡片）、轨迹/记忆页内容（占位）、多窗口设置。

### 4.3 P1：持久化 + 工具卡/轨迹 + 审批

**建议实施顺序（按依赖/风险/用户价值）**

1. **SQLite History + sessions**（最先）：store 模块实现 `rig.History`（Load/Append、HistoryReplacer 预留）+ 会话元数据；
   UI 侧栏从真实 sessions 读；启动恢复 last session。**理由**：一切后续（工具卡、审批、压缩、fork）都依赖持久化读模型。
2. **SessionLog + 轨迹页**：events 表实现 `rig.SessionLog`；轨迹页用 `ui.List` 渲染事件；`session_*` 工具自动可用。
   **理由**：轨迹是诊断/回放基础，也是 old 已有高频面板。
3. **Event→读模型 + 工具卡**：agent 模块把 `rig.Event` 写入 messages/tool_calls 读模型；`zeokeel-ui/toolcards` 注册
   ToolRenderer（命令/文件/搜索/web/图片/提问/通用 7 变体）；transcript 接 `RenderTool`。**理由**：agent 产品感的核心。
4. **审批 UI（gate.Ask + Asker）**：host 实现 `rig.Asker`；agent 模块把 pending 请求写读模型；`zeokeel-ui/approval`
   渲染 composer 上方审批卡（Allow/Deny/Always）；Esc 先 deny。**理由**：安全边界必须早于"放开工具写操作"。
5. **压缩 + 检查点 + spill**：`ContextBudget` + `LLMCompactor`/`WindowCompactor` + `HistoryReplacer.Replace`；
   `CheckpointStore` + `RecoverInterruptedTurn`；`SpillPolicy`。**理由**：长会话/崩溃恢复/大工具结果的产品稳定性。
6. **标题/会话操作**：首回合自动标题（rig 结果 + 首次 user 文本），重命名/删除/fork/export 基础能力
   （fork/export 完整数据复制放 P3）。**理由**：低成本高感知，依赖 1 的 store。

**P1 依赖的 rig 契约**：`rig.History`、`rig.HistoryReplacer`、`rig.SessionLog`、`rig.CheckpointStore`、
`rig.Compactor`、`rig.ContextBudget`、`rig.SpillPolicy`、`gate.Ask(rig.Asker)`、`agent.RecoverInterruptedTurn`、
`rig.ToolResult`（Status/Summary）。

**P1 验收标准**

- [ ] 关闭进程再启动：会话列表/消息/工具调用/草稿恢复；`store` schema 迁移版本化。
- [ ] 轨迹页按会话展示事件（seq/type/time/payload 摘要），过滤与错误高亮可用。
- [ ] 工具卡 7 变体在真实 rig.Event 下渲染；折叠/展开/错误/图片/搜索计数正确。
- [ ] 审批流：需要审批的工具 → 审批卡出现 → Allow 执行 / Always 后续同工具免审 / Deny 拒绝；Esc deny；Stop 取消 pending。
- [ ] 压缩：超预算触发摘要并持久替换；重启后不回退；`/compact` 手动压缩可用（命令 P2 可先用 UI 按钮）。
- [ ] 检查点：模拟崩溃（kill 进程）后启动，中断回合补 unknown-outcome、不重放副作用。
- [ ] `-race` 全绿；store 并发测试；跨重启 E2E 脚本。

### 4.4 P2：composer 增强 + 设置扩展

- **composer**：从 session/home 内联输入升级为 `zeokeel-ui/composer` 服务（单点持有 composer 元素/焦点/命令目录）：
  slash palette（16 条命令按归属模块注册）、`@` 文件/目录提及（P2 文件；会话 P3）、附件/图片 chips、
  权限/模型/模式/推理强度菜单、草稿作用域与持久化。
- **设置**：General、Models（目录+添加/编辑+连接检查）、Appearance（主题/字体/高对比/减少动效）、Language；
  设置页全部走 `RegisterSettingsPage`；设置壳由宿主渲染（native Sidebar）。
- **home**：hero/setup card/chips/composer 重写；无模型时引导到设置页；发送创建首条会话。
- **host 能力**：Command 服务（快捷键/菜单/palette 统一）、toast viewport、Locale 服务接产品文案、Theme 覆盖。
- **rig 契约**：`rig.ToolResult.Parts`（图片/多模态）、hosttools 的 `Asker/Presenter/FilePresenter/PlanController`、
  mygo markdown/editor/i18n。**注意**：`present` 在 rig v0.13 改为声明文件（`present_artifact` 保留旧工具），
  UI 按 `FilePresenter` 实现展示。

**验收**：16 条 slash 命令可见/可过滤/可执行（未实现的显示禁用或提示）；`@` 提及文件下钻与引号路径；
附件与图片进入下一回合（`ToolResult.Parts` 真图片）；模型目录 CRUD 并生效；外观/语言切换即时生效；
设置独立窗口仍为 P4（P2 仅 modal）。`go test ./...` 绿；headless 交互测试覆盖 palette/mention/chips。

### 4.5 P3：memory/skills/automation/插件/搜索/fork/export/diff/规划/团队

| 子项 | 交付 | rig 契约 | 验收 |
|---|---|---|---|
| memory | `zeokeel/memory` + `zeokeel-ui/memory`（设置页 + 会话 Memory 页签） | `rig.MemoryStore` + `tools/memory` | Off/Suggest/Auto、待审核、增删改、作用域隔离 |
| skills | `zeokeel/skills` + 设置 Skills 页 | `rig.SkillStore` + `tools/skill` | 多根加载、启停、skill 搜索/加载 |
| automation | `zeokeel/automation` + `zeokeel-ui/automation` | `rig.Scheduler` + `tools/schedule` | 任务 CRUD、调度执行、运行历史、立即运行 |
| plugins 页 | `zeokeel-ui/plugins`（内置/实验分组、详情、LSP/MCP 开关） | 设置键 + toolkit 配置 | 开关生效/重启提示；插件详情字段 |
| workspace diff | `zeokeel-ui/diff` + 产品 workspacechanges | rig tools + sandbox | 每回合文件变更卡、`editor.DiffView` 渲染、限额 |
| 会话搜索 | `zeokeel-ui/session-search`（Cmd+K）+ inline | `rig.SessionLog.Search` | 标题/内容匹配、过滤、键盘导航 |
| fork/export | 产品 store + UI 动作 | `rig.History`/`SessionLog` + 导出 | fork 复制会话/附件；JSONL 导出兼容 old schema |
| plan / goal | `zeokeel-ui/plan`、`zeokeel-ui/goal` | host `PlanController`、`GoalStore/GoalUpdater`、`agent.GoalRoundDriver` | plan 评审卡/徽标；goal banner + 自动续轮（预算/cap） |
| approval 扩展 | auto-review（`gate.Review` + `tools/review.SubagentReviewer`） | `rig.Reviewer` | 自动审查 allow/deny/超时回退人工；原因展示 |
| todo/questions/artifacts | `zeokeel-ui/todo` | hosttools + `Asker`/`Presenter` | todo 侧板、ask 卡、artifacts 预览 |
| team | `zeokeel-ui/team` + 产品 team 服务 | `rig.TeamTaskManager`、异步 subagent/`NoticeSource` | 成员动态、停止、任务板 |
| session references | `zeokeel-ui/session` 子槽 + 产品 sessionreference | `rig.SessionLog` | canonical mention、受限只读快照注入 |
| presets | 设置 Agent presets 页 | `tools/presets`（工具子集）+ 产品 prompt | 预设生效、工具子集断言 |

**验收**：以上功能均可从 UI 完成端到端操作；`go test ./... -race` 绿；新增模块各自 headless 测试；
边界 grep（无自绘/无跨模块 import）绿；旧功能清单（§3）P3 行全部勾掉。

### 4.6 P4：宿主集成

ACP server（list/load/fs/terminal/permission，复用 rig agent/SessionLog）、全局热键、导入器（old SQLite→新 store）、
自动更新、诊断/网络/桌面/更新/通知/数据隐私设置页、独立设置窗口、托盘/单实例细节、打包与签名。

**验收**：编辑器 ACP 客户端可 list/load 会话并收发消息；旧数据导入覆盖率 100%（见 §5.5）；更新检查可手动触发；
设置窗口独立可用；跨平台冒烟（Linux/Windows/macOS）记录。

### 4.7 阶段依赖关系

```
P0 (agent+session+sidebar+settings)
   │
   ├── P1 (store History/SessionLog + toolcards/trajectory + approval + compaction/checkpoint)
   │       │
   │       ├── P2 (composer + settings 扩展 + home；依赖 P1 的 sessions/settings)
   │       │
   │       └── P3 (memory/skills/automation/plugins/diff/search/fork/plan/team；依赖 P1 的 store/log)
   │
   └── P4 (ACP/热键/导入/更新/诊断；依赖 P1 store，可与 P2/P3 并行)
```

**明确不做 / 非目标**（避免边界漂移）：

- DSH 像素级对齐（ADR 0001 已退役）；webview/H5/浏览器客户端；运行时插件安装/热更新；
  PTC `run_code`（old 产品决定不做，rig 可选但默认关闭）；语音输入；插件市场（老产品决定不做）；
  多窗口平铺布局（只保留设置窗口 P4）。
## 5. 迁移策略

### 5.1 old 包 → 处理方式总表

> old 生产代码合计约 **89,978 行**，测试约 **93,965 行**（见 `docs/research/rig-parity/01-report.md` §3）。
> 处理方式四类：**删除**（rig 直接替代）、**适配**（语义/数据/资产参考重写）、**重写**（产品层新实现）、**精简保留**。

| old 包 | 生产行数 | 处理方式 | 新落点 | 阶段 |
|---|---:|---|---|---|
| `old/internal/tools` | 12,267 | **删除**（rig 81 工具全替代） | `rig/tools/*` + `internal/modules/toolkit` | P0（toolkit 升级） |
| `old/internal/provider/openai` | 1,193 | **删除** | `rig/provider/openai` | P0 |
| `old/internal/lsp` | 1,347 | **删除**（v0.13 已含 references/implementation/UTF-16） | `rig/tools/lsp` + toolkit 配置 | P2/P3 |
| `old/internal/mcp` | 1,171 | **删除**（v0.13 含 notifications/templates） | `rig/tools/mcp` + 设置页 | P2/P3 |
| `old/internal/agentteam` | 733 | **删除** | `rig/tools/team` + agent Runner | P0/P3 |
| `old/internal/runtime` | 6,434 | 循环/审批/子代理/多模态部分**删除**；产品逻辑（压缩持久化/检查点恢复/审批队列/auto-review/goal driver/modes）**重写** | rig agent/gate + `zeokeel/agent` + `zeokeel/store` | P1/P3 |
| `old/internal/shell` | 25,229 | **重写**（16 UI 模块 + 宿主 chrome，全部 native 控件） | `internal/ui/*` + `internal/uikit` + uiroot | P0–P4 |
| `old/internal/session` | 8,840 | **重写**（控制器→ChatService/读模型/产品服务） | `zeokeel/agent` + 产品模块 | P0–P3 |
| `old/internal/store` | 5,887 | **重写**（SQLite 实现 rig 接口 + 新 schema + 迁移器） | `zeokeel/store` | P1 |
| `old/internal/app` | 8,153 | **拆分**：进程能力→host；组合根→assembly；设置窗口/托盘/ACP 接线→产品 host/module | `internal/host`、`internal/assembly`、`zeokeel/acp` | P0/P4 |
| `old/internal/prompt` | 1,398 | **重写**（system prompt 组合/注入策略） | 产品 prompt 模块/agent 模块 | P1/P2 |
| `old/internal/state` | 1,345 | **重写**（事件 projector→读模型） | ChatService 读模型 / store | P0/P1 |
| `old/internal/workspacechanges` | 1,134 | **重写**（diff 计算与限额） | 产品 workspacechanges + `zeokeel-ui/diff` | P3 |
| `old/internal/memory` | 1,059 | **适配 + 重写**（工具/存储用 rig；抽取/整合产品化） | `zeokeel/memory` + `zeokeel-ui/memory` | P3 |
| `old/internal/skills` | 772 | **适配**（rig FileStore；多根/开关产品化） | `zeokeel/skills` | P3 |
| `old/internal/automation` | 398 | **适配 + 重写**（CRUD 用 rig Scheduler；执行器产品化） | `zeokeel/automation` | P3 |
| `old/internal/sessionreference` | 713 | **重写** | 产品 sessionreference + session UI 子槽 | P3 |
| `old/internal/globalhotkey` | 3,869 | **重写**（产品 host） | host 能力 | P4 |
| `old/internal/importer` | 2,341 | **重写** | `zeokeel/importer` | P4 |
| `old/internal/acp` | 1,863 | **重写**（调用 rig agent/SessionLog） | `zeokeel/acp` | P4 |
| `old/internal/updater` | 678 | **重写** | 产品 updater | P4 |
| `old/internal/i18n` | 560 | **适配**（文案目录平移 + mygo/i18n） | Locale 服务 | P2 |
| `old/internal/modelcatalog` | 357 | **重写**（模型目录产品化） | `zeokeel/models` + 设置页 | P2 |
| `old/internal/presets` | 645 | **适配 + 重写**（工具子集用 rig presets；persona/prompt 产品化） | 产品 presets | P3 |
| `old/internal/websearch` | 302 | **重写实现**（rig `web.Searcher` 接口） | 产品 searcher | P2/P3 |
| `old/internal/design` / `icons` / `brand` | 733 / 87+SVG / 316 | **精简适配**（tokens 子集 + 资产包；删未用动效） | `internal/design`、`internal/ui/assets` | P0 最小 / P2 全量 |
| `old/internal/winproc` / `e2e` / shell 专项测试 | 154 / 测试 | **适配/重写**（host 辅助 + 新测试体系） | host / 新测试 | P4 / 全程 |
| old 测试（`old/internal/**/*_test.go`） | 93,965（测试） | **不迁移**；按模块重写 headless + E2E | 各模块 `_test.go` + 产品 E2E | 全程 |

### 5.2 可以直接适配（参考重写，不复制实现）

1. **布局、文案、交互逻辑**：old shell 的布局顺序、界面文案、交互路径、禁用/空态/错误态行为是"产品规格"，
   在 native 控件上重写（尺寸/像素不照抄；断点/间距用 design token）。
2. **数据模型语义**：old store 的表/字段/状态含义（sessions/messages/tool_calls/events/attachments/feedback/
   memories/automations/automation_runs/checkpoints/settings/workspaces）作为新 schema 的输入，见 §5.5。
3. **命令与快捷键语义**：16 条 slash 命令、13 条默认快捷键、7 条菜单命令、`keybinding.<id>` 覆盖键。
4. **设置键名**：能保留的键名（如 `ui.language`、`terminal.shell`、`keybinding.*`、`session.<id>.permission`）
   尽量保留，降低迁移与用户预期成本；新键统一 `chat.*`、`models.*` 前缀。
5. **prompt 模板与文案**：system prompt 结构、plan/memory/AGENTS.md 注入规则、auto-title prompt、review prompt，
   作为产品 prompt 模块的输入；不照搬 eino 实现。
6. **资产**：logo/wordmark/26 个图标、设计 tokens 中实际被使用的颜色/圆角/间距（research 报告显示 22/35 动效导出零引用，
   只保留在用 token）。
7. **协议客户端配置**：LSP servers / MCP servers / skills 根目录的配置格式与默认值（迁移到设置页/assembly 配置）。

### 5.3 必须重写（不能直接搬）

| 组件 | 原因 | 新实现 |
|---|---|---|
| eino runtime（`old/internal/runtime`） | eino ADK 的事件/审批/子代理模型与 rig 不同 | rig `agent`/`gate`/`sandbox` + 产品薄包装 |
| 会话控制器（`old/internal/session/controller.go` 等） | 与 eino runtime/store 强耦合；rig 的 RunRequest/Event 模型不同 | `zeokeel/agent` 的 ChatService + 读模型 |
| store 数据层 | old schema 为 eino/旧 UI 设计；rig 需要 `rig.History/SessionLog/...` 接口 | `zeokeel/store`：新 schema + 接口实现 + 迁移器 |
| 组合根（`old/internal/app`） | old 直接组装 eino + shell；新架构是 kernel 编译期模块 | `internal/assembly` + `internal/host` |
| 事件 projector（`old/internal/state`） | old 事件模型绑定 store/runtime | ChatService 读模型（只保留 UI 需要的投影） |
| 设置独立窗口/多窗口 | 新架构窗口归 host；uiroot 只有单帧入口 | host 窗口能力（P4）+ uiroot Modal 兜底 |
| 测试体系 | old 是白盒 `package shell` + 像素 baseline/a11y 专项 | 模块 headless（fake ChatService）+ 产品 E2E + 真窗口冒烟 |
| prompt registry | old 以代码注册 prompt 片段 | 产品 prompt 模块 + `rig.AgentSpec.SystemPrompt` |

### 5.4 删除清单

| 删除项 | 原因 |
|---|---|
| `old/internal/tools`（58 工具实现） | rig `tools/*` 全替代（catalog 61 旧名 100% 覆盖 + 现代面审计 63 参考） |
| `old/internal/provider/openai` | rig `provider/openai` |
| `old/internal/lsp` / `old/internal/mcp` / `old/internal/agentteam` | rig 客户端/团队替代（配置留产品） |
| old runtime 的 eino 循环/审批/子代理/多模态实现 | rig agent/gate/runner/parts |
| old design/motion 未用导出、像素基线/`artifacts/`（106MB）、DSH baseline | 像素对齐退役（ADR 0001）；原生控件自带动效 |
| old shell 的 a11y 补丁专项测试、手写 i18n 解析、手写 markdown/列表/tabs/modal/toast | 原生控件替代 |
| PTC `run_code`、语音输入、插件市场 | 产品明确不做（见 §4.7 非目标） |

### 5.5 数据迁移设计（old SQLite → 新 schema）

**源**：old 应用数据目录下的 SQLite（old `store` 包打开；具体路径由 old app 决定，迁移器按候选目录探测并让用户确认）。

**表映射**（新表名/列可调整，语义以 `rig` 接口为准）：

| old 表（old 字段） | 新落点 | rig 契约 | 迁移要点 |
|---|---|---|---|
| `workspaces(id, root, ...)` | `workspaces` | 产品服务 | 保留 id/root；路径不存在时标记 unavailable，不丢数据 |
| `sessions(id, workspace_id, title, created_at, updated_at, status)` | `sessions`（+ archived/permission/mode/goal 列） | `rig.History` 的会话索引 | 保留 id/标题/时间；status→archived/running 映射；会话级 permission 迁移 |
| `messages(id, session_id, role, content, reasoning, created_at, turn_id)` | `messages`（+ ordinal） | `rig.History.Load/Append` | 保留 role/content/reasoning/turn_id；按时间→ordinal 重排（old 已有 `messages_ordinal` 迁移先例） |
| `tool_calls(id, session_id, message_id, name, args, result, status, started_at, ended_at, approval)` | `tool_calls` | `rig.ToolResult`/读模型 | status 映射（running/ok/error）；args/result JSON 原样；approval→决策记录 |
| `events(session_id, seq, type, payload, ts)` | `session_events` | `rig.SessionLog` + `rig.SessionEvent` | **保留 seq**（会话内唯一）；type/payload 原样；供轨迹/搜索/session_* 工具 |
| `settings(key, value)` | host `settings.json`（`internal/host`，0600 原子写）+ sessions 级键 | `contracts.SettingsService`/`rig.Settings` | 模型/provider 键→settings.json；会话级键→sessions 列；API key 不打印 |
| `attachments(...)` | `attachments` + 文件目录 | 产品 attachments | 文件复制 + 校验和；message 关联重映射；清理旧引用 |
| `feedback(...)` | `feedback` | 产品服务 | 保留 message_id/rating/note/created |
| `memories(...)` | `memories`（scope/workspace/text） | `rig.MemoryStore` | scope 映射（global/workspace）；id 重新生成但保留去重键 |
| `automations(...)` / `automation_runs(...)` | `scheduled_tasks` / `task_runs` | `rig.Scheduler` | schedule 表达式原样；enabled→暂停标记；run 历史保留 |
| `checkpoints(...)` | `checkpoints` | `rig.CheckpointStore` | phase/turn/payload 原样；仅未完成回合需要恢复 |
| `schema_migrations`/`schema_version` | `migration_state` | — | 记录导入版本、时间、计数、失败项 |

**迁移机制（必须可回滚、可重复）**：

1. **源只读**：复制 old DB 到临时目录再读；绝不修改 old 数据目录；导入成功前保留备份。
2. **版本化**：新库 `migration_state(version, source_hash, imported_at, counts, errors)`；迁移器可重复执行（幂等 upsert）。
3. **dry-run**：先统计（每表行数、附件字节、事件 seq 范围、缺失引用、非法 JSON），输出报告；用户确认后才写。
4. **事务/批处理**：按会话分批（每批 500 行）单事务提交；WAL；进度回调到 UI（导入器页面）。
5. **校验**：导入后行数/附件哈希/seq 连续性/会话消息数一致性断言；失败可重新导入（upsert）。
6. **API key**：old `api_key` 字段是明文 SQLite——迁移时**不打印、不写日志**，直接写入 host settings.json（0600 原子写）；
   建议 P4 接 OS keychain；诊断页只显示"已设置"。
7. **导出兼容**：fork/export JSONL 保留 old 字段名；P3 的 fork/export 与导入器共用 schema 版本。
8. **回滚**：保留 old DB 与新库备份；回滚 = 停新版、恢复旧 DB（导入过程不删任何 old 文件）。
9. **用户确认**：首次启动检测到 old 数据时提示"导入/稍后/跳过"；不自动改写用户数据。

### 5.6 迁移顺序（与阶段对齐）

```
P0：无迁移（新会话 in-memory；old 数据不动）
P1：新 schema 建库 + rig.History/SessionLog/CheckpointStore 实现
     └─ 导入器最小版：workspaces/sessions/messages/tool_calls/events/settings（P4 完整化）
P2：settings/models/i18n 迁移 + 设置页
P3：attachments/feedback/memories/automations/automation_runs/sessionreferences/diff 事件迁移
P4：完整导入器 UI + ACP + updater + 迁移校验报告
```

---

## 6. 风险与边界

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| R1 | **native 控件能力边界**：mygo 无图标库、无消息气泡/工具卡成品控件；产品容易"想自绘" | 违反 native-only 原则、代码量反弹、a11y 退化 | 允许的组合件全部进 uikit（§2.4）；review 用 §2.1 checklist + grep 门禁；设计参考 old 但不照抄像素 |
| R2 | **UI 线程桥**：agent/后台 goroutine 误碰 `ui.Context`/Element/Handle | 帧错乱、panic、难复现 | 只允许 `UIRuntime.Post` + `Invalidate`；模块状态加锁；headless `-race` 测试；禁止跨帧缓存 Element/Handle |
| R3 | **API key 存储**：old 明文 SQLite；新 host settings.json 明文 JSON（0600 原子写） | 泄露/误显示/日志 | 迁移不打印；诊断只显示"已设置"；文件 0600；P4 接 OS keychain；日志脱敏 |
| R4 | **数据迁移 schema drift**：old 12 表 → 新 schema；事件 seq/ordinal/附件引用易错 | 用户数据丢失或错乱 | 源只读 + 备份 + dry-run + 幂等 upsert + 行数/哈希校验（§5.5）；迁移器独立测试 |
| R5 | **rig v0.8→v0.13 升级破坏**：subagent 异步、present 改声明文件、bash 非零退出码、hosttools/full 选项变化 | P0-0 卡住或行为回退 | 先升 rig 并跑 rig 全测试 + toolkit/hosttools 回归；锁定版本；CHANGELOG 逐条核对 |
| R6 | **God object 心态迁移**：old App 123 字段/779 方法/19 个 sync.Map，容易原样搬进"模块" | 模块化名存实亡 | 状态归模块实例 struct；禁止包级 map；每模块只暴露必要服务；App 类对象不再出现 |
| R7 | **测试量巨大**：old 93,965 行测试（shell 29,465；564 Test；513 NewTester） | 迁移期无回归网 | 不迁移 old 白盒测试；按模块重写 headless 测试 + 关键 E2E；P1 建 product E2E 清单 |
| R8 | **性能**：长会话 transcript/轨迹/文件列表；流式 Markdown 每帧重建 | 帧卡顿/内存增长 | `ui.List` 虚拟化；markdown `Stream` 增量；事件分页（SessionLog limit）；帧时间基准入 CI |
| R9 | **多窗口**：设置独立窗口在同一 UI 线程与主窗交替帧 | 焦点/模块贡献归属混乱 | Phase-1 只用 uiroot modal；独立窗口 P4，由 host 持窗口、模块只提供 Content |
| R10 | **i18n/文案**：350 键迁移 + 模块默认文案 + 动态文案 | 中英混排/漏译 | 文案目录集中 + Locale 服务；PR 检查新增硬编码文案；默认中文/英文可配 |
| R11 | **品牌/图标资产**：26 SVG + logo 的许可证与一致性 | 法律/视觉不一致 | 资产包集中管理 + license 检查（见 .audit/license-verify.md 经验）；模块不内联 SVG |
| R12 | **安全边界**：P0 `gate.AllowAll`、sandbox 默认 `workspace-write` + 空命令 allowlist | 早期版本写操作无审批 | P0 仅内部冒烟；P1 必须上 `gate.Ask`；默认拒绝 shell/写文件直到用户配置；文档明确"非 OS 沙箱" |
| R13 | **范围蔓延**：UI 微调/像素对齐/非目标功能挤占阶段 | 交付延期 | §4.7 非目标清单；每阶段只做该阶段验收；UI 视觉以 native 默认 + 品牌 token 为口径 |

**边界说明**：

- rig 是**纯工具集 SDK**，不是 OS 沙箱；审批 `Gate` 与 sandbox 策略是进程内围栏，不可信模型需容器/VM 隔离（沿用 rig README 的安全口径）。
- mygo 是 0.x：升级前先锁版本、跑 uikit/模块 headless 测试；必要时冻结 API 适配层。
- old/ 是只读参考；新根工程不 import old 任何包（old 为独立 module）。

---

## 7. 附：old shell 的 mygo 控件使用统计与替换建议

### 7.1 规模与口径

| 指标 | 数值 | 复现 |
|---|---:|---|
| shell `.go` 文件 | 159（59 生产 + 100 测试） | `ls old/internal/shell/*.go \| wc -l` |
| 生产 LOC | 25,229 | `find old/internal/shell -maxdepth 1 -name '*.go' ! -name '*_test.go' -exec cat {} + \| wc -l` |
| 测试 LOC | 29,465 | 同上 `*_test.go` |
| `func (a *App)` 方法 | 779 | `grep -rh '^func (a \*App)' old/internal/shell --include='*.go' --exclude='*_test.go' \| wc -l` |
| `App` struct 字段 | 123 | `sed -n '/^type App struct/,/^}/p' old/internal/shell/app.go` |
| 包级 `sync.Map`（`map[*App]*X`） | 19 | `grep -rn sync.Map old/internal/shell --include='*.go' --exclude='*_test.go' \| wc -l` |
| 测试函数 | 564 Test（+8 Benchmark） | `grep -rh '^func Test' old/internal/shell --include='*_test.go' \| wc -l` |
| `ui.NewTester` | 513 | 同上 |
| 后端耦合 | `.Ctrl.` 295 处 / `.Store()` 64 处 | grep |

### 7.2 `ui.*` 调用统计（生产代码，共 1,091 次）

| 类别 | 次数 | 占比 | 主要 API |
|---|---:|---:|---|
| 布局图元 | 708 | 64.9% | `Text` 263、`Row` 219、`Column` 176、`Box` 23、`Scroll` 13、`Spacer` 10 |
| 图形/颜色 | 147 | 13.5% | `Icon` 67、`MustParseSVG` 52、`Image` 20、`RGBA` 7 |
| 高级控件 | 227 | 20.8% | `Button` 70、`ButtonBase` 54、`Modal` 22、`Popover` 17、`TextInput` 14、`PrimaryButton` 11、`TextArea` 6、`Field` 5、`Switch` 5、`SidebarItem` 5、`Select` 3 |
| 其他/基础 | 9 | 0.8% | `Key` 2、`Form` 2、`PopoverBase` 2 等 |

**原生控件使用次数（生产，重点看"该用没用"）**：

| 控件 | 次数 | 说明 |
|---|---:|---|
| `Sidebar` / `SidebarSection` / `SidebarItem` | 1 / 0 / 5 | 侧栏/树仍是手写行（`workspaces.go`），应换 `Sidebar`+`SidebarItem`/`Tree` |
| `Tabs` | 0（仅 `TabsBase` 2） | Session/右栏 tab 为手写条，应换 `ui.Tabs` |
| `Collapsible` | 0 | 思考行/工具卡详情手写，应换 `ui.Collapsible` |
| `List`（虚拟列表） | 1 | 消息/插件/文件列表大多手写，应换 `ui.List` |
| `Table` / `Tree` | 0 / 0 | 设置表格/工作区树手写，应换 `ui.Table`/`ui.Tree` |
| `Toolbar` | 0 | composer 工具条手写 Row，应换 `ui.Toolbar` |
| `Badge` | 1 | 状态点/徽标多为手写药丸（Box+Text） |
| `Segmented` / `Toggle` / `Checkbox` / `RadioGroup` | 0 / 0 / 1 / 0 | 设置项手写，应换原生选择控件 |
| `MenuButton` / `Combobox` / `Autocomplete` | 0 / 0 / 0 | 权限/模型/mention 菜单手写 Popover，应换原生 |
| `PrimaryButton` | 11 | 大量主操作用 `ButtonBase` 自拼，应换 `PrimaryButton` |
| `Modal` / `Popover` | 22 / 17 | 已用原生外壳，但内容仍手写行（换成原生表单/列表即可） |

其他方法调用：`c.Toast` 123（已用原生 toast）、`.Tooltip` 32、`.Hovered()` 78、`.OnClick` 105、
`.Transition` 2（动效基本未接）、`.Animate` 0、`.Focusable()` 40、`.Role()` 31、`.AutoFocus()` 13。

### 7.3 替换建议（按收益排序）

| 优先级 | old 手搓 | native 替换 | 预计收益 |
|---|---|---|---|
| 1 | `session_live.go` 消息流/列表 + 手写滚动 | `ui.List`（ListState/ItemKey/Selection） | 虚拟化/键盘/a11y，长会话稳定 |
| 2 | `workspaces.go` 侧栏树/会话行 | `ui.Sidebar`+`SidebarItem`/`SidebarSection` + `ui.Tree` | 少 ~1k 行，键盘/首字母/树语义免费 |
| 3 | `markdown.go` 628 行自研 Markdown | `ui/markdown.View/Stream` | 直接删除 600+ 行；流式/代码块/表格/复制 |
| 4 | `toolcards.go` 704 行手写卡片 | `ui.Collapsible`+`ui/markdown`+`ui.Badge` | 卡片变体只写内容，控件行为免费 |
| 5 | `trajectory.go`/`right_sidebar.go` 手写 tabs | `ui.Tabs`/`TabsBase` | 少手写交互，焦点/键盘一致 |
| 6 | `home_live_menus.go`+`popover_menus.go` 手写下拉（~1.2k 行） | `ui.Select`/`MenuButton`/`Popover`/`Menu` | 键盘/溢出/焦点免费 |
| 7 | 设置行/表单（`settings_ui.go`、`models_ui.go:325` 等） | `ui.Form`/`Field`/`Fieldset` + 原生控件 | 统一设置页模板，a11y 免费 |
| 8 | 手写 toast（deferred/chrome） | `c.Toast`/`ToastAction` | 统一 viewport/动效 |
| 9 | `workspace_changes_ui.go` diff 着色 | `ui/editor.DiffView` | 标准化 diff 渲染，减少自研 |
| 10 | `design/motion.go` 未用动效 + a11y 补丁 | 原生 Transition/Focus/Role | 删除 22/35 未用导出与 703 行 a11y 专项测试 |

### 7.4 old shell 文件 → 新模块归属（59 文件，LOC）

| 新模块 | old 文件（LOC） | 小计 |
|---|---|---:|
| `zeokeel-ui/home` | `home.go`(503) | 503 |
| `zeokeel-ui/session` | `session.go`(43)、`session_live.go`(1131)、`trajectory.go`(612)、`message_actions.go`(387)、`reasoning_ui.go`(437)、`transcript_order.go`(264)、`session_errors.go`(165)、`session_open_async.go`(144)、`session_workspace.go`(94) | 3,277 |
| `zeokeel-ui/composer` | `composer_slash.go`(1060)、`composer_menus.go`(996)、`home_live_menus.go`(622)、`attachments_ui.go`(272)、`draft_scope.go`(234)、`modes_ui.go`(176)、`composer_native.go`(121)、`composer_style.go`(113) | 3,594 |
| `zeokeel-ui/toolcards` | `toolcards.go`(704) | 704 |
| `zeokeel-ui/settings` | `models_ui.go`(1055)、`settings_ui.go`(1033)、`settings_extra.go`(703)、`presets_ui.go`(500)、`settings_update_hotkey.go`(440)、`manage_ui.go`(439)、`settings.go`(204) | 4,374 |
| `zeokeel-ui/plugins` | `plugin_details.go`(858)、`plugins.go`(393)、`plugins_ui.go`(312) | 1,563 |
| `zeokeel-ui/automation` | `automation_ui.go`(828)、`automation.go`(46) | 874 |
| `zeokeel-ui/memory` | `memory_ui.go`(832) | 832 |
| `zeokeel-ui/team` | `team_ui.go`(229) | 229 |
| `zeokeel-ui/goal` | `composer_goal_compact.go`(218) + `team_ui.go` goal 片段（≈70） | ≈288 |
| `zeokeel-ui/plan` | `plan_ui.go`(475) | 475 |
| `zeokeel-ui/approval` | `composer_menus.go` approval 片段 + `session_live.go:945-991`（≈90） | ≈90 |
| `zeokeel-ui/todo` | `todo_ui.go`(379) | 379 |
| `zeokeel-ui/workspaces` | `workspaces.go`(1504)、`session_search.go`(943)、`sessions_cache_async.go`(137)、`sidebar_hover.go`(127) | 2,711 |
| `zeokeel-ui/files` | `right_sidebar.go` 内容段（≈777）、`start_tab.go`(524)、`files_root.go`(276)、`files_native.go`(109)、`files_preview.go`(75) | 2,216 |
| `zeokeel-ui/diff` | `workspace_changes_ui.go`(249) | 249 |
| 宿主 chrome（uiroot/host） | `app.go`(553)、`shortcuts.go`(466)、`chrome.go`(379)、`menus.go`(354)、`layout.go`(306)、`i18n.go`(190)、`preferences.go`(151)、`session_restore.go`(126)、`motion.go`(108)、`buildinfo.go`(57)、`assets.go`(55)、`right_sidebar.go` 外壳段(≈455)、`session_search.go` 宿主段(≈213) | ≈3,413 |
| `internal/uikit` | `popover_menus.go`(167)、`markdown.go`(119) | 286 |
| 合计 | 59 文件 | 25,229 |

---

## 8. Phase-1 执行清单（本轮并行任务对接）

### 8.1 契约

`internal/contracts/chat.go`（已冻结，后续只做加法）：

- `contracts.KeyChat = cordis.NewServiceKey[ChatService]("zeokeel/chat")`
- 读模型：`ChatMessage{ID,Role,Text,Reasoning,Streaming,Tools []ToolCall,Error,CreatedAt}`、
  `ChatSession{ID,Title,Messages,Running,Error,CreatedAt,UpdatedAt}`、`ChatSessionInfo`、`ChatConfig`。
- 服务：`Config/Sessions/Session/ActiveSession/SetActiveSession/NewSession/DeleteSession/Send/Cancel/Subscribe`。
- 设置键：`contracts.SettingsChat{Model,BaseURL,APIKey,SystemPrompt}`。

### 8.2 assembly 接线（P0 待加）

```json
{
  "id": "agent",
  "module": "zeokeel/agent",
  "config": {"model": "deepseek-chat", "maxIterations": 20, "contextBudget": 32000}
},
{
  "id": "ui",
  "module": "zeokeel/host/group",
  "children": [
    {"id": "home", "module": "zeokeel-ui/home", "config": {}},
    {"id": "session", "module": "zeokeel-ui/session", "config": {}},
    {"id": "sidebar", "module": "zeokeel-ui/sidebar", "config": {}},
    {"id": "settings", "module": "zeokeel-ui/settings", "config": {}},
    {"id": "toolcards", "module": "zeokeel-ui/toolcards", "config": {}},
    {"id": "welcome", "module": "zeokeel-ui/welcome", "config": {}},
    {"id": "about", "module": "zeokeel-ui/about", "config": {"title": "关于"}}
  ]
}
```

### 8.3 完成定义（DoD）

1. `go build ./...`、`go vet ./...`、`gofmt -l .`、`go test ./... -count=1` 全绿（含 `-race` 关键包）。
2. 真实 `assembly.Load(desktop)` 后 headless 渲染：session/sidebar/settings 贡献可见；Close 后贡献消失（uiroot 测试模式）。
3. fake OpenAI SSE E2E：会话创建 → 流式回复 → Stop → 再发；设置页保存模型配置生效；重启后设置仍在。
4. 手工冒烟（desktop + Xvfb 或真机）：输入、发送、滚动、切换会话、设置保存、退出。
5. native-only 与依赖边界检查（§2.1/§1.5）通过。
6. 更新文档：`docs/INJECTION-POINTS.md`（新模块/注入点）、`docs/ARCHITECTURE.md`（当前状态）、本计划 §0/§4 状态行。

### 8.4 P0 写作时的已知状态

- ✅ `internal/contracts/chat.go` 已定义；`internal/ui/{session,sidebar,settings}` 目录已在仓库出现（并行任务 B/C）。
- 🟡 `internal/modules/agent`（任务 A）尚未落地；rig v0.13.0 升级进行中（`go.mod` 已切 v0.13.0，
  toolkit/hosttools 的 API 迁移未完成，属 P0-0 前置）。
- ⬜ assembly 尚未加入 agent/session/sidebar/settings 条目；上述 DoD 未验证。
- 本文与并行任务的边界：本文只定义"怎么做/验收"；代码由 A/B/C 任务交付，完成后按 §8.3 联调。

---

## 9. 证据与复现

```sh
# old shell 规模
cd /data/mygo/old/internal/shell
ls *.go | wc -l; ls *_test.go | wc -l
find . -maxdepth 1 -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l
grep -rh '^func (a \*App)' --include='*.go' --exclude='*_test.go' . | wc -l

# ui.* 调用统计（生产）
grep -rhoE 'ui\.[A-Z][A-Za-z0-9]*\(' --include='*.go' --exclude='*_test.go' . | sort | uniq -c | sort -rn | head -30

# 原生控件"该用没用"清单
for w in Sidebar SidebarItem SidebarSection Tabs Collapsible List Table Tree Toolbar Badge Segmented Select MenuButton Combobox Autocomplete PrimaryButton; do
  printf '%s=' "$w"; grep -rho "ui\.${w}(" --include='*.go' --exclude='*_test.go' . | wc -l
done

# 旧的包规模
cd /data/mygo/old/internal
for d in tools runtime session store app shell prompt state lsp mcp agentteam memory skills automation; do
  printf '%-12s %s\n' "$d" "$(find $d -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l)"
done
```

其他依据：

- rig v0.13.0 能力：`/data/zeokeel-rig`（tag `v0.13.0`；`docs/research/rig-parity/03-v0.13.0-verification.md`，38 测试包全绿、81 静态工具）。
- old → rig 覆盖矩阵：`docs/research/rig-parity/01-report.md`（22% 可替代 / 78% 产品层）。
- UI 模块映射与 4 波迁移：`docs/research/kernel-split/05-ui-module-map.md`。
- mygo 原生控件能力/重造轮子审计：`old/docs/research/02-mygo-native.md`（1,169 次 ui 调用旧口径；本文用当前 59 文件 1,091 次复测）。
- DSH 功能对齐历史：`old/docs/FUNCTIONAL-PARITY.md`（auto-memory/PTC 等产品决策依据）。

**维护方式**：每次 DSH/rig/mygo 升级或阶段完成后，重跑 §9 命令并更新 §0/§3/§4 的数字与勾选状态；
阶段验收失败时回到对应 §4 小节的验收项，不要跨阶段打补丁。

---

## 10. 附录：术语

| 术语 | 含义 |
|---|---|
| 模块（module） | 一个 Go 包 + `module.Descriptor`，编译期注册、随二进制编入，assembly 决定是否激活 |
| uiroot | UI 总入口：帧根、布局区域、导航、注入点注册表、overlay/toast、主题/语言 |
| 注入点（slot） | 宿主布局里的具名位置；模块通过 `RegisterSlotItem` 贡献内容（区域→槽位→条目） |
| screen | 顶层页面贡献（`RegisterScreen`），如 home/session/plugins/automation |
| settings page | 设置页贡献（`RegisterSettingsPage`），宿主渲染设置壳（native Sidebar + 页头/页体/页脚） |
| tool renderer | 工具卡渲染贡献（`RegisterToolRenderer`），由 session 的 transcript 调 `UIRuntime.RenderTool` |
| ChatService | 产品会话服务（`contracts.KeyChat`）：读模型 + 发送/取消/订阅，背后是 rig agent |
| 读模型 | UI 只读的数据视图（ChatSession/ChatMessage/ToolCall…），由 agent 模块在事件回调中更新 |
| History / SessionLog | rig 契约：对话消息持久化 / 会话事件日志（轨迹、搜索、session_* 工具） |
| CheckpointStore | rig 契约：回合边界检查点（崩溃恢复，不重放副作用） |
| Gate / Asker | rig 契约：审批门禁 / 向用户提问的宿主接口（ui/approval 实现 UI） |
| chrome | 应用外壳：窗口/布局/侧栏容器/overlay 栈/toast/快捷键/主题/语言（归宿主，不拆模块） |
| native 控件 | mygo `ui.*` 提供的真实控件（Sidebar/Tabs/List/Table/Modal/…），非项目自绘 |
