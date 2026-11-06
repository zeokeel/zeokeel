# 04 · UI 模块化约束事实：mygo 渲染/线程模型 + shell 解剖（研究线 D）

> 对象：`/data/mygo`（module `github.com/zeokeel/zeokeel`，Go 1.27.3，HEAD `40368d1`，产品代码只读）；mygo 框架只读副本 `/root/go/pkg/mod/github.com/egoist/mygo@v0.7.4`。
> 新前提（用户约束）：① 内核作者=用户本人，供应链可信；② **UI 也必须模块组装**——模块构建期一起编进二进制、经 assembly 装配，不是运行时插件。
> 证据分级：**[A]** 本报告探针/命令实测；**[B]** 读源码并给 `file:line`；**[C]** 推断/估算（未验证）。
> 探针：`/tmp/probe-ui/main.go`（未入库，未改产品代码），在 mygo v0.7.4 的 headless `ui.Tester` 上验证 Post 队列/Invalidate/键盘注册语义；输出见附录 A。
> 目标：给出「UI 能不能像 DSH client-ui 那样拆」的约束事实，供 05（模块映射）与 06（装配方案）引用。**本报告不设计业务拆分**。

## 0. 结论摘要

### 0.1 八条硬约束（05/06 必须先接受，否则设计会返工）

1. **帧循环与窗口永远属宿主，UI 模块只贡献 builder。** 只有宿主持有 `ui.View`/`*ui.Content`；模块注册的是 `func(*ui.Context)` 构建函数，不持有 Content、不自己跑帧。依据：`ui/api_gen.go:2667-2677`（view 在主线程、窗口需要帧时调用）、`ui/doc.go:17-18`。[B]
2. **渲染回调只在帧内被调用，且一次帧可能调用多次。** 一帧最多 3 个 build pass（`ui/runtime.go:582`），事件在构建中改状态会重建；实测一次点击触发 **3 次** view 调用（探针 S1）。回调必须无副作用，状态变更只走 action/Post。[A][B]
3. **`*ui.Context` 只在帧内有效；跨帧只能用 `ui.Services`（弱引用、不保留窗口）或 Post 队列；`ui.Element` 过期，只有 `ui.Handle` 跨帧。** 依据：`ui/frame.go:11-13,37-38,63-66,94-107`、`ui/services.go:8-12`、`ui/doc.go:25-33`；探针 S2（帧外 `Valid()=false`、`Theme()=nil`、`Toast` no-op）。[A][B]
4. **跨线程更新只能「Post + Invalidate」；异步 post 未导出，`RunOnMain` 是唯一导出的主线程原语（阻塞）。** `loop.go:86,89,94,119`；`content.go:152,172`；`ui/frame.go:41-45`、`ui/services.go:24-28`。实测队列在帧首 drain 生效（探针 S3/S6）。[A][B]
5. **主题、语言、窗口清单、根 overlay、toast 层、焦点所有权都属宿主。** 模块不得 `c.SetTheme` 根主题（用 `ui.Themed` 做局部主题）、不得自建根 `ui.Overlay`、toast 必须走宿主队列（`c.Toast` 帧外静默丢弃，shell 已用 `deferredToast` 兜底）。依据：`app.go:339`、`settings_ui.go:284`、`ui/api_gen.go:467-487,1519-1525,2378`、`composer_menus.go:434-449`。[A][B]
6. **状态必须归模块实例，不得包级；帧内不得同步调业务/IO。** shell 现状反例：`App` 123 字段 + `shellUIState` 56 字段、19 个以 `*App` 为 key 的包级 `sync.Map`、72 个不同的 `Ctrl.*` 方法/295 个调用点（03 的「74」并入测试里 `app.Ctrl.` 后的并集）、每帧同步拉 `SessionsForWorkspace`。依据见 §2.3。[A][B]
7. **注册只发生在装配/启动期（UI 线程），运行期启用/禁用 = build 时返回空，不做注册表增删。** kernel `Load` 同步执行（`module/load.go:190-234`），但 reactive 重激活可能发生在任何 Provide 的 goroutine；贡献顺序必须显式 `Order`，不得依赖注册/激活顺序（kernel 文档 `docs/COMPILE-TIME-REGISTRY.md:152-158`；重复快捷键「先注册者胜」探针 S5）。[A][B]
8. **同一模块可能被多个窗口构建（主窗口 + 独立 settings 窗口共享同一个 App）。** mygo `Context` 无窗口 ID API（`ui` 包无导出窗口标识），但每个窗口的 `*Context` 稳定且可比较；per-window 状态必须用 `*ui.Context` 或 `ui.Handle`（每窗口独立 binding）为键。依据：`architecture.md:2164-2170`、`ui/api_gen.go:2674-2675`、`ui/ref.go:7,60,118`。[B]

### 0.2 可行性结论（克制版）

**成立**：编译期 UI 贡献层在 mygo 上成立——screen / slot / settings page / tool renderer / command 五类都能实现为「宿主帧内按注册表调用模块 builder」。这是 DSH `client-ui` slot 注册（02 §4.5）在单进程原生 UI 里的 Go 等价物。
**不成立**：把帧循环、窗口生命周期、多窗口调度、主题根、焦点路由做成可独立启停的模块。mygo 没有这些抽象，也不该由 kernel fiber 提供（kernel 无主线程语义）。
**成本重心**：不在框架能力，而在 §4.2 的五项（God object 状态迁移、AR-08 同步调用、30k 行 UI 测试、多窗口、焦点/快捷键冲突）。

### 0.3 对 03/00 结论的修正

- 03 §8「**UI 不能模块化**」应细化为「**UI 帧循环/窗口不能模块化；UI 功能面可以做成编译期贡献层**」。03 §6.6「拆 shell 的收益只在 AR-04」不再完整：AR-04 内部分层之外，还可换来 05 的模块注册面与 assembly 裁剪。
- 03/00 的 T0-2 判断仍成立：主线程单循环契约不变，UI 模块不能有独立生命周期；变的是**模块边界**而不是线程模型。
- 03 §3.3/§3.4 关于「19 个 sync.Map、74 个 Ctrl 方法、同步 DB」的事实继续有效，且正是 §3 约束的来源；但它们是**迁移成本**，不构成「UI 不可模块化」的证明。

## 1. mygo 渲染与线程模型（直接决定 UI 模块边界）

### 1.1 立即模式：view 是「每帧全量重建」的纯函数

`ui.View(func(c *ui.Context))` 返回 `*ui.Content`，交给 `mygo.WindowOptions.Content`；view 在主线程、窗口需要帧时调用（输入后、`Context.Invalidate`/`Window.Invalidate` 后、动画期间），**一个 Content 可服务多个窗口，每窗口独立状态**（`ui/api_gen.go:2667-2677`）。view 每帧构建整棵树，布局复用上一帧未变的部分（`architecture.md:2387-2394`）。元素跨帧的身份 = 父元素身份 + 位置或 `Key`（`ui/frame.go:49-55`；`architecture.md:2374-2377`）。`ui/doc.go:17-30` 同时给出三条硬规则：一帧可含多个 pass、Element 只能活到下一 pass、**不要把 Element/自定义 part 放进 app 状态或后台 goroutine**。

对 UI 模块的直接含义：模块的 `Build(c)` 每次被调用都要重建自己整棵子树；模块不得缓存 Element；模块跨帧状态放模块实例字段，或放 `Context.Key`/`ui.Local`/`ui.Handle` 这类由元素身份承载的状态。

### 1.2 一帧多 pass、Element 过期、pass 不可观测

`runFrame` 先置 `inFrame`（`ui/runtime.go:536-541`），然后 `for pass := 0; pass < 3; pass++`（`:582`）：某次回调在构建中改了状态（点击、编辑、`Router.Push`），帧内会重建最多 3 次再布局/绘制。每次 pass 开始 `context.reset` 清空 `rt.actions` 与快捷键注册表（`ui/context.go:98-119`），所以 **OnClick/OnShortcut 是每 pass 重新收集的，不是持久订阅**。`Element` 在下一 pass 前过期（`ui/frame.go:63-66`；开发构建越界会 panic，`:94-107`；生产静默失效 `ui/handles_debug.go:5` / `handles_release.go:5`），跨帧身份必须用 `ui.Handle`（`ui/ref.go:7,60-131`）。

**关键缺口**：公共 API 没有「当前 pass/帧号」查询（`Context.Valid` 只回答是否在 build 中，`ui/frame.go:37-38`）。宿主无法区分「pass 1」与「重建 pass」，所以宿主只能把 Post 队列 drain 挂在 **每次 view 调用前**；模块不能依赖「每帧恰好调用一次」的假设——探针 S1：一次点击 → 3 次 view 调用、`n` 只 +1。

### 1.3 线程模型：主线程唯一，公共方法任意 goroutine

- 主 goroutine 锁定主线程，`App.Run` 阻塞原生事件循环（`architecture.md:180-207`）；backend 调用与 handler 回调都在主线程（`AGENTS.md:32-34`）。
- 每个公共方法可从任意 goroutine 调用（`AGENTS.md:32-34`），但 **UI 层的 view 只在主线程调用**（`ui/api_gen.go:2672-2673`），「永不阻塞主线程等待需要它的工作」（`AGENTS.md:34`）。
- 内部原语：`postMain`（`loop.go:89`，**未导出**）、`onMain`（`:94`）、`await`（`:119`）；唯一导出的是阻塞版 `RunOnMain`（`loop.go:86`）。异步 post 没有导出 API——这是宿主自建队列的直接动因。
- 内容层：`Window.Invalidate` 任意 goroutine、CAS 合并到一帧（`content.go:152-160`）；`Window.Update(fn)` = 主线程执行 fn 后重绘（`:172-178`）；`contentChanged` 走 `Conn.Changed`（`:181-188`）。
- UI 层：`Context.Invalidate` 任意 goroutine（`ui/frame.go:41-45`）→ `Services.Invalidate`（`ui/services.go:24-28`）→ `host.invalidate` → `conn.Invalidate`。`Services` 只含 `weak.Pointer[engine]`，**不保留窗口**（`ui/services.go:8-12`），是官方推荐的跨帧句柄（`ui/doc.go:31-33`：捕获 Services，不要存 Context）。
- 隐性后果：窗口不可见时帧可能被「hold」不构建（`ui/runtime.go:866-880`，`holdFrame`），settings 窗口的 stale popover 问题（`internal/app/settings_window.go:260-281` 注释 UX-04）即由此而来；模块不能假设 Invalidate 后立即有帧。

### 1.4 宿主自建「UI 线程 Post 队列 + 帧首 drain」：可行，探针已验证

**结论**：不需要 mygo 未导出 API。三要素齐全：自有队列（mutex + slice）、drain 挂在每次 view 调用开头、唤醒用 `Services.Invalidate`（或 `Window.Invalidate`）。草案（与 §5 一致）：

```go
type Runtime struct {
    mu   sync.Mutex
    q    []func()
    done bool
    svc  ui.Services // 帧内捕获一次，弱引用、可跨帧/跨 goroutine
}
func (r *Runtime) Post(fn func()) { // 任意 goroutine
    r.mu.Lock(); if r.done { r.mu.Unlock(); return }; r.q = append(r.q, fn); r.mu.Unlock()
    r.svc.Invalidate() // frame.go:41 文档保证任意 goroutine 安全
}
func (r *Runtime) drain() { // 仅主线程，view 开头
    r.mu.Lock(); q := r.q; r.q = nil; r.mu.Unlock()
    for _, fn := range q { fn() }
}
```

**实测（探针 S2/S3/S6）**：帧外保存的 Context `Valid()=false`、`Theme()==nil`、`Toast` 静默丢弃，但 `Invalidate()` 合法且确实请求下一帧；goroutine 里 `Post` 后 `FramePending=true`，下一帧 view 开头 drain 后 fn 在「UI 线程」生效；在点击回调里 Post，也在这轮点击引发的后续 pass/帧前生效。
**替代方案**：`Window.Update(fn)` 本身就是导出的异步 post+重绘（`content.go:172`），语义与 Post 接近；但它是**逐窗口**的、由 backend dispatch 驱动（`loop.drain`，`loop.go:38-56`），与 view 起点无固定先后，且 headless 测试无窗口可用。宿主若要「多窗口统一队列、可测试、帧首确定性顺序」，自建队列更合适；两者的 fn 都必须在主线程执行。

**drain 语义边界（写进 06 的实现要求）**：
- (a) drain 逢 view 调用即执行，一帧可能 1–3 次；fn 必须幂等/可重入安全（不会重复执行，但可能在一个帧内多个 pass 前执行）。
- (b) 构建回调内不应 Post（会在下一 pass/帧才执行）；应从 `OnClick`/命令等 action 里 Post。
- (c) 关闭窗口后：置 `done=true` 丢弃后续 Post，对齐 `loop.shutdown`（`loop.go:67-78`）的语义。
- (d) 多窗口：每窗口一个 Runtime 或一个 Runtime + 多窗口 Services 列表（Invalidate 全部），由宿主决定；当前 shell 已有两个窗口（主窗口 + settings）。

### 1.5 mygo 能力矩阵：帧内必须 vs 帧外可调

| 能力 | 帧内（构建中） | 帧外 | 依据 |
| --- | --- | --- | --- |
| 构造任何控件/读 `Size/Theme/Preferences/Direction/Locale/TitleBar` | **必须** | 不可（`build()==nil` 静默 no-op/零值） | `ui/frame.go:29-38`；`ui/api_gen.go:244-617` |
| `c.SetTheme`/`ui.Themed`/`c.SetZoom` | **必须** | 不可 | `ui/api_gen.go:467-487,2378` |
| `OnShortcut`/`Element.OnShortcut`/`OverlayShortcut`/`Shortcut` | **必须**（每帧/每 pass 重注册） | 不可 | `ui/actions.go:51-61`；`ui/scope.go:196-200`；`ui/input.go:974-988` |
| `Toast/AddToast/CloseToast`/`After`/`AnimationFrame`/`Announce` | **必须** | 不可（需宿主队列兜底） | `ui/api_gen.go:244-300,559-580`；shell `composer_menus.go:434-449` |
| `ui.Overlay`/`Popover`/`Dialog`/菜单 | **必须** | 不可 | `ui/api_gen.go:1519-1525`；`ui/menu.go` |
| `Handle.Bind/Focus(c)`/`Focused(c)` | 帧内（Binding/查询需 Context） | `Handle.Focus()` 无 Context 时可用但文档要求 UI 线程 | `ui/ref.go:60-131,144-169` |
| `Context/Services.Invalidate` | 可 | **可（任意 goroutine）** | `ui/frame.go:41`；`ui/services.go:24` |
| `Window.Invalidate/Update/Show/Hide/SetTitle/...` | 可（已在主线程直接执行） | **可（内部 onMain，阻塞等待）** | `content.go:152-178`；`window.go:739-1233` |
| `App.SetMenu`/`NewTray` 菜单回调 | 回调在主线程 | 回调里可改状态、下一帧生效 | `internal/app/app.go:697-716`；`architecture.md:230-233` |
| `ui.Router.Push/Replace/Back` | 可（帧内会请求重建 pass） | 可但**仅 UI 线程**（文档明确） | `ui/router.go:78-80,309-317` |
| 持久化/DB/网络/阻塞 IO | 当前 shell 在帧内做（反例） | 应在 goroutine + Post | `shell-refactor-plan.md:40-58`；`docs/audit/04-architecture-quality.md:85,117` |

## 2. shell 现状解剖（粒度到「模块映射」可直接用）

### 2.1 帧入口：`App.View`（app.go:303-392）

单个 `ui.View` 入口，每帧固定流水线：`flushDeferredToast`（:310）→ `restoreSessionState`（:313）→ `applySessionOpenResults`（:314）→ `flushDraftPersistence`（:315）→ `syncShellPreferences`（:318，ReduceMotion 时 `c.DisableAnimations()`）→ `syncDraftScope`/`syncComposerMenuDismissal`/`persistResolvedLanguage`（:319-321）→ `installTaskShortcuts`（:322）→ `installChromeShortcuts`（:323）→ `restoreWorkspace`/`restoreViewOptions`（:324-325）→ `themeTokens` + `c.SetTheme`（:338-339）→ `toastViewport`（:345）→ `ui.Row{ sidebar, mainScreen, rightSidebar }`（:347-374）→ `settingsOverlay`（:375-381）→ `shellOverlays`（:382）→ 延迟焦点（:388-391）。
结论：宿主 UI Runtime 的 `View` 就是把这条流水线里的「屏幕/面板/overlay/hotkey」换成注册表取用；流水线骨架（主题、toast、窗口、焦点）必须留宿主。

### 2.2 屏幕、sidebar、settings 与独立 settings 窗口

- **屏幕枚举**：`Home/Session/Plugins/Settings/Automation`（`app.go:20-26`）；分派在 `mainScreen` switch（`:502-514`），`Settings` 是特殊态：`backgroundScreen()` 让原屏幕继续垫在下面（`:432-442`），由 `settingsOverlay`（`settings_ui.go:225-254`）盖 modal，面板本体 `settingsPanelLive`（`:256-278`）承载 nav+body。
- **sidebar**：`sidebar`（`app.go:522-550`）内置品牌行、New Session、原生 `ui.Sidebar`、workspace→session 树；sidebar 的选中状态 `sidebarSel` 每帧从屏幕重算（`:532-533`），点击后 `sidebarChoice` 导航（`:545-547`）。
- **settings**：`settingsTab` 枚举本体在 `models_ui.go:23-29`（General/Models/BuiltinPlugins/AgentPresets），其余枚举值**散落在 5 个文件用 `+1` 算术续号**：`plugin_details.go:26`（About）、`memory_ui.go:32`（Memory）、`settings_extra.go:29-34`（Notifications…Desktop）、`manage_ui.go:25-26`（MCPServers/Skills）→ 共 14 个页；页面渲染在 `settingsNavLive`（`settings_ui.go:347-417`，含条件可见性）与 `settingsBodyLive` 的 switch（`:421-477`，13 个 `*SettingsPage` 实现 + General 的 `settingsRowsLive`）。这套「枚举 + 手写 nav + switch」正是 05 要换成的 settings page 注册表。
- **overlay 清单**：`shellOverlays`（`session_search.go:188-213`）条件渲染 about/shortcuts/rename/delete/workspace 对话框/会话搜索 palette；settings 是模态 overlay（`settings_ui.go:225-278`），其余是各自 `ui.Overlay`。关闭顺序集中在 `closeTaskTopmost`（`session_search.go:135-160`）与 `closeOneTaskOverlay`（`:162-186`），这是宿主必须保留的「顶层 overlay 栈」语义。
- **独立 settings 窗口**：`internal/app/settings_window.go:36-48` 的 `settingsWindowOptions.Content func(*ui.Context)` 收的正是 `shell.App.SettingsView`（`settings_ui.go:274-321`），由 `internal/app/app.go:230` 注入；窗口在 `mygoSettingsWindows.New`（`settings_window.go:174-217`）里懒创建、`Hidden:true`、`Content: ui.View(options.Content)`；生命周期/单例/close-to-hide 由 `settingsWindowController`（`:74-167`）管理，跨窗口刷新靠手动 `refreshWindows()`（`:260-315`，主窗口与 settings 窗口各 Invalidate 一次，并同步 autostart）。**同一个 `*shell.App` 服务两个 `ui.View`**（`internal/app/app.go:206,238` 与 `:230`），这是多窗口约束的现成样本。

### 2.3 状态版图与 feature 归属

规模：`App` 123 字段（`app.go:29-291`，262 行）+ `shellUIState` 56 字段（`models_ui.go:112-226`，经 `shellUIStates` 包级 Map 取，`:247-256`）+ 19 个包级 `sync.Map`（下表）+ `snap atomic.Pointer`（`app.go:83`，`SetSnapshot` `:293-298`，由 controller 后台 goroutine 写入、`OnUpdate` 里 `win.Invalidate()`：`internal/app/app.go:261-264`）。19 个 Map 全部以 `*App` 为 key、**无一处 Delete**（模块实例化/关闭后会泄漏，反证「状态必须归模块实例」）。

| 状态载体 | 位置 | 本质所属 feature |
| --- | --- | --- |
| `Screen/settingsReturn/sidebarSel/sidebarChosen…` | `app.go:30-35,275-279` | 导航/Chrome（宿主 Navigator） |
| `sidebarCollapsed/rightOpen/rightTab/rightSplit*/rightFullscreen` | `app.go:244-267` | 左/右 Chrome 模块 |
| `Language/lang*/settings*/prefs` | `app.go:37-97` | 主题/语言/设置（宿主 UI 服务） |
| `Ctrl/snap/teamRoster/teamStop` | `app.go:42-51,83` | Session 数据读模型（session 服务） |
| `draft/drafts/homeDraft/homeAttachments/homeMode…` | `app.go:52-82` | Composer（Home 与 Session 共用） |
| `moreOpen/permissionOpen/…/paletteOpen/rename*/delete*/modelProbe*` | `app.go:118-171` | 按 feature 拆（菜单/Composer/命令面板/Session/Models 页） |
| `automationDelete*/memoryClear*` | `app.go:151-159` | Automation / Memory 页 |
| `ws*/workspaceOrder/pinnedSessions/sessionOrder/showArchived/groupBy/archiveFilter/inline*` | `app.go:214-290` | Workspace + Session 列表 |
| `files*/termInput/termOutput/termMu/termRunning/termCancel` | `app.go:244-257` | Files / Terminal 右面板 |
| `composerRef/mentionRef/focusComposerOnFrame` | `app.go:172-212` | Composer 焦点；跨帧焦点需宿主帧钩子 |
| `settingsWindowOpen/Close/settingsChangedHook/menuAction/PickDirectory/PickFiles/SaveFileDialog/SaveSessionLog` | `app.go:99-108,179-208` | 宿主平台 seam（窗口/对话框/菜单），**必须留宿主** |
| `shellUIState.settingsTab` 等 56 字段 | `models_ui.go:112-226` | 分摊到 Settings 页/Composer/Session/RightPanel 各模块 |
| `slashMenuStates` | `composer_slash.go:167` | Composer `/` 面板 |
| `toolResultStates` | `toolcards.go:212` | Session 工具卡 |
| `memoryPanelStates` | `memory_ui.go:74` | Memory 设置页 |
| `rightPanelStates` | `right_sidebar.go:101` | 右面板 |
| `liveMenuStates` | `home_live_menus.go:97` | Composer 菜单 |
| `mentionSessionCaches` | `session_search.go:780` | Composer `@` 提及 |
| `workspaceDiffStates` | `workspace_changes_ui.go:35` | Session 变更视图 |
| `planUIStates` | `plan_ui.go:48` | Session 计划卡 |
| `startTabStates` | `start_tab.go:61`（含 `lastComposerContext *ui.Context` `:42-45`） | Home/Start 卡片；**存 Context 是官方反模式（`doc.go:32-33`）** |
| `modeUIStates` | `modes_ui.go:48` | Composer 模式菜单 |
| `pluginUIStates` | `plugin_details.go:63` | Plugins 页 |
| `sessionRenderStates`/`referenceContextOpen` | `session_live.go:35,741` | Session 渲染/会话引用 |
| `filesNativeStates` | `files_native.go:28` | Files 面板 |
| `liveUIStates`/`liveStates`/`reasoningFrameCaches`/`markdownStyles` | `trajectory.go:76`、`motion.go:43`、`reasoning_ui.go:168`、`markdown.go:59` | Session 渲染（轨迹/动效/推理/消息样式） |

**每帧轮询业务**：shell 内 72 个不同的 `Ctrl.*` 方法、295 个调用点（生产文件；含测试文件时分别为 74/342），42 处 `.Store().X` 直取链（含测试 59）；渲染路径里的同步读示例：`home.go:105-107`（Home 每帧 `HasConfiguredModel`/`CurrentWorkspace`）、`workspaces.go:309,327,338`（sidebar 每个 workspace 每帧 `SessionsForWorkspace`）、`:1021,1065`、`models_ui.go:280`（Models 页每帧 `Models().List()`）。已有两个 AR-08 缓解雏形可复用：`session_open_async.go` 的「后台 goroutine + 结果 channel + View 开头 `applySessionOpenResults`」与 `sessions_cache_async.go` 的「缓存 + 后台刷新，帧只读缓存」；`settings_update_hotkey.go` 有 5 处 `c.Invalidate()` 从异步回调唤醒帧。这三处证明：宿主自建 Post/read-model 模式在 shell 里**已经存在**，只是没抽象成统一服务。

### 2.4 现有「伪贡献点」——05 可以直接形式化的四处

1. **菜单命令**：`MenuAction*` 常量（`shortcuts.go:351-359`）+ `menuCommands` 表（`:378-388`，label/accelerator）+ `HandleMenuAction` 硬编码 switch（`:427-443`）；native 菜单/托盘回调经 `internal/app/app_settings.go:69,86` 回到 shell。已是「表 + 分派」，缺的是模块注册与冲突校验。
2. **slash 命令**：`slashCommands` 表（`composer_slash.go:68-140`）+ `runSlashCommand` 硬编码 switch（`composer_menus.go:253-320`）；`slashCommandName` 只查表（`composer_menus.go:224-250`）。两张表 + 两处 switch 分散，正是 command 注册表要收拢的。
3. **工具卡渲染**：`toolKindFor(name)` 硬编码别名 switch（`toolcards.go:56-77`）→ `toolVariant` 再 switch（`:457-512`）→ 图标/详情分支（`:145,252,269`）。工具状态来自 `state.Snapshot.Tools`（只读），是天然的 tool renderer 注册点。
4. **settings 页**：14 个枚举值分散 6 文件 + `settingsNavLive`/`settingsBodyLive` 两处 switch（§2.2）。注册表化后每个页自带 ID/Order/Group/Icon/Build/Visible，导航与内容自动生成。

### 2.5 shell 现有行为归档：帧内必须 vs 帧外可调

| shell 行为 | 归档 | 证据/说明 |
| --- | --- | --- |
| 屏幕/面板/overlay 的控件树构建 | 帧内 | `app.go:347-392` 全部在 `View` 内 |
| `c.SetTheme(shellTheme(...))`、`ui.Themed` 局部主题 | 帧内 | `app.go:338-339`、`settings_ui.go:96-117,284` |
| `installTaskShortcuts`/`installChromeShortcuts`/`OverlayShortcut` | 帧内（每帧注册） | `session_search.go:32-46`、`chrome.go:41-77` |
| toast（`c.Toast`）与延迟 toast | 帧内 + 队列 | `app.go:310,400-427`、`composer_slash.go:1052-1062`、`composer_menus.go:434-449` |
| `c.After`（草稿持久化节流）/动画/`AnimationFrame` | 帧内 | `session_restore.go:78-88` |
| `Handle.Focus(c)`（composer）、`focusComposerOnFrame` 延迟焦点 | 帧内/帧钩子 | `app.go:388-391`、`:209-212` |
| 导航：`a.Screen = X`、`openSettings`、`ui.Router.Push` | 帧外可调（UI 线程），下一帧生效 | `app.go:448-459`；`ui/router.go:206-222,309-317`；native 菜单回调直接调 `HandleMenuAction`（`app.go` 主线程事件） |
| 命令执行：`newSession`/`HandleMenuAction`/`runSlashCommand` | 帧外可调（UI 线程）；当前内部同步调 Ctrl/DB | `shortcuts.go:427-443`；`composer_menus.go:253-320` |
| 窗口：Show/Hide/Focus/SetTitle/Invalidate | 帧外可调（onMain，任意 goroutine 安全） | `window.go:797-917`；`internal/app/settings_window.go:124-145` |
| 系统托盘/原生菜单 | 宿主；回调在主线程 | `internal/app/app.go:697-716` |
| 持久化/设置读写 | 目前帧内（AR-08 已知问题）；应移出 | `settings_ui.go:32-60`；`workspaces.go:23-35` |
| Post 异步结果应用 | 帧首 drain（现有 channel 版） | `app.go:314`；`session_open_async.go:90-110` |

### 2.6 多窗口、焦点、快捷键的现状事实

- 主窗口与 settings 窗口共享 `*App`；两个 `ui.View` 由不同 `*ui.Context` 驱动，`Context` 稳定但**无窗口 ID**（`architecture.md:2164-2170`）。`shell` 把 `*ui.Context` 存进 `startTabState.lastComposerContext`（`start_tab.go:42-45`）供帧外 toast/回调使用——跨窗口时会指向「最后构建的那个窗口」，05 迁移时应改成宿主的「窗口句柄 + Services」。
- `deferredToast` 是单个槽位（`slashMenuStates`），两个窗口谁先构建谁消费，属现有隐性 bug 面。
- 快捷键语义：每帧注册、**下一帧生效**（`ui/input.go:874-881`；`settleKeys` `:948-970`）；窗口级同 chord **先注册者胜**（`ui/actions.go` `runActions` 遍历 + `context.Shortcut` 删除首个 delivered；探针 S5）；modal 内窗口快捷键被抑制、需 `OverlayShortcut`（`ui/context.go:536-541`、`ui/scope.go:159-200`）；native 菜单 accelerator 优先于窗口快捷键（`shortcuts.go:361-372` 注释）。shell 的注册顺序由 `View` 源码顺序决定（task→chrome→屏幕内的），这本身就是隐式优先级，注册表化后必须显式化。
- 快捷键覆盖持久化：`keybinding.<id>` 设置键（`shortcuts.go:200-288`），编辑后的 chord 通过 `bindShortcut`/`overlayShortcut` 生效——模块命令也应共用这套 ID 与覆盖机制，否则设置页与运行时漂移。

## 3. UI 模块必须遵守的 15 条约束

1. **帧所有权**：模块不得创建 `*ui.Content`/窗口/帧循环；只实现宿主调用的 `Build(c *ui.Context)`。依据 `ui/api_gen.go:2667-2677`；违约后果=双帧循环、状态双写。
2. **渲染回调帧内有效**：`Build` 内只用 `*ui.Context` 系 API；不得把 Context 存进模块状态用于渲染（官方要求存 `Services`，`ui/doc.go:31-33`）。依据 `ui/frame.go:29-38`；帧外调用静默 no-op（探针 S2）。
3. **每 pass 全量重建、回调无副作用**：`Build` 只读状态 + 构建控件；状态改写放在 `OnClick/OnChange/OnSubmit` 或 Post 回调里。依据 `ui/runtime.go:582`、`ui/doc.go:25-27`；后果=重复副作用（一帧 1–3 次调用）。
4. **Element 不跨 pass，Handle/Local/Key 才跨帧**：模块控件身份用 `ui.Handle` 或 `Context.Key`/`ui.Local`；不得把 `ui.Element` 放进模块字段或 goroutine（`ui/frame.go:63-66,94-107`、`ui/ref.go:7`）。
5. **状态归模块实例，禁止包级可变状态**：模块的交互状态放模块 struct；shell 的 19 个 `sync.Map` 是反例（无 Delete、以 `*App` 为 key，`models_ui.go:247` 等）。依据：kernel 模块可 Disabled/Close，包级状态无法随实例回收。
6. **跨线程变更必须 Post**：后台 goroutine 只调 `Runtime.Post(fn)`（宿主在帧首 drain）或 `Services.Invalidate`；不得直接改模块 UI 字段。依据 `loop.go:86-108`、`ui/services.go:24-28`、探针 S3/S6。
7. **帧内禁止同步业务/IO/阻塞**：模块不得在 `Build` 里调 DB、网络、session 控制器或等锁；读宿主注入的 read-model 快照（`App.snap` 范式 `app.go:83,293-298`），动作走异步命令服务（AR-08，`shell-refactor-plan.md:40-58`）。
8. **注册只发生在装配/启动期**：`Register*` 只允许在 kernel 装配阶段或宿主启动阶段、UI 线程调用；运行期禁用通过 `Visible()` 谓词或 build 返回空实现，不增删注册表。依据：kernel `Load` 同步（`module/load.go:190-234`），但 reactive 激活可能在任意 Provide goroutine；`docs/COMPILE-TIME-REGISTRY.md:66,152-158`。
9. **贡献顺序显式化**：每个贡献带 `Order`（+稳定 ID 排序），不得依赖注册/激活顺序；同 chord 快捷键先注册者胜（探针 S5），因此命令注册表必须在启动期检测重复 chord 并报预检错误。依据 `ui/input.go:948-970`、`ui/actions.go:110-142`。
10. **根主题/根 overlay/toast 层归宿主**：模块不调 `c.SetTheme` 根、不建根 `ui.Overlay`；局部主题用 `ui.Themed`；toast 走宿主队列（`c.Toast` 只在帧内且会被宿主 `ToastViewportBase` 接管，`app.go:400-427`）。
11. **焦点归模块自己**：模块持有自己的 `ui.Handle` 并在每个 `Build` 里 `Bind`；跨帧焦点请求由宿主帧钩子派发（`app.go:209-212,388-391` 范式）；Handle 按窗口独立 binding（`ui/ref.go:60-131`）。
12. **多窗口状态按窗口键**：模块若出现在多个 Content（主窗口 + settings），per-window 状态以 `*ui.Context` 或宿主注入窗口 ID 为键；共享状态须显式声明（如 settingsTab 现有共享语义）。依据 `ui/api_gen.go:2674-2675`、`internal/app/app.go:206,230,238`。
13. **overlay 所有权**：模块的 modal/popover 由宿主统一 Overlay 层构建；模块只声明「打开状态 + BuildPanel」，否则 Escape/焦点顺序/无障碍会互相打架（`ui/scope.go:130-200`、`settings_ui.go:225-278`）。
14. **可测试性**：模块构建不得要求 mygo 窗口/桌面后端；宿主 Runtime 必须能在 headless 下构造（`ui.NewTester` 已被 shell 测试用 511 次，92 个文件）。依据 `ui/headless.go:257-268,448`；违约后果=§4.2 测试迁移不可行。
15. **生命周期对称**：模块 `Close/Disable` 在 UI 线程执行，先注销贡献/摘除状态再释放；`Post` 队列关闭后丢弃（对齐 `loop.shutdown` `loop.go:67-78`）。依据 kernel `module/load.go:442-445` 逆序清理、`ui/runtime.go:1057-1105`（窗口关闭后 API 变 no-op）。

## 4. 可行性判断

### 4.1 五类贡献点逐个判定

| 贡献点 | 判定 | 机制（mygo 上如何成立） | 主要缺口 |
| --- | --- | --- | --- |
| **Screen** | ✅ 成立 | 宿主 `View` 里按 `Navigator.Current()` 查注册表调 `Screen.Build(c)`，替代 `mainScreen` switch（`app.go:502-514`）；屏幕切换仍走宿主导航状态 | `Screen` 枚举（`app.go:20-26`）与 63 处 `a.Screen/backgroundScreen()` 判定要迁移；settings 特殊态（backgroundScreen）需保留为宿主语义 |
| **Slot** | ✅ 成立 | slot = 宿主在布局中预留的 `Children` 回调，按 `Order` 依次调用模块 builder；mygo 无 slot 概念但 Children 可调任意函数 | 每个贡献根必须 `Key`/稳定身份，否则相邻项增减会移动元素身份（`architecture.md:2374-2377`） |
| **Settings page** | ✅ 成立 | 注册 `{ID,Order,Group,Title,Icon,Build,Visible}`，宿主生成 nav + body；替代 6 文件 `+1` 枚举与两处 switch（§2.2） | 选中页状态应由宿主持有（modal 与 settings 窗口共享，`shellUIState.settingsTab`）；页内状态随模块迁移 |
| **Tool renderer** | ✅ 成立 | 注册 `Match(name)/Summary/Detail/Icon` 替代 `toolKindFor`+`toolVariant`（`toolcards.go:56-77,457-512`）；输入是 `state.ToolState` 只读快照 | 未知工具 fallback 必须保留；工具名别名表要并入注册匹配；详情展开状态（`liveStates.expanded`）归属 Session 模块 |
| **Command** | ✅ 成立 | 统一 `menuCommands`（`shortcuts.go:378-388`）与 `slashCommands`（`composer_slash.go:68-140`）为命令注册表，宿主同时生成 native 菜单、palette、快捷键与设置页行 | 命令执行依赖「动作服务」而非 `Ctrl.*`（T1-4 session 接口）；菜单 accelerator 优先级需在注册表里显式 |
| **Navigator** | ✅ 成立（宿主服务） | 宿主状态机 + 可选 `ui.Router`（`ui/router.go:45-52,206-317`）；模块只调 `Push/Back` | 跨窗口导航语义（settings 窗口不承载屏幕）；URL/链接集成是加分项，非首期 |

### 4.2 最大风险与规避（按严重度）

1. **God object 状态迁移（最大工程量）**：`App` 123 字段 + `shellUIState` 56 字段 + 19 个包级 Map，任何拆分都要先决定归属。规避：按 §2.3 表逐 feature 迁移；宿主只保留导航/主题/窗口/设置/命令/焦点；迁移期保留 `App` 同名薄方法委托给模块（测试先绿）；19 个 Map 改为模块字段。估计 [C] 8–15 人日（不含 05 的模块拆分本身）。
2. **帧内同步 Ctrl/DB（AR-08，模块化的前提而非产物）**：72 方法/295 调用点；模块若继续走同步调用，既冻结帧又让模块无法安全 Close。规避：先做 read-model（`state.Snapshot` + `atomic.Pointer`，沿用 `app.go:83,261-264,293-298`）与异步命令队列（沿用 `session_open_async.go`/`sessions_cache_async.go` 模式），再迁屏幕。估计 [C] 与 03 T1-4 合并 8–12 人日。
3. **30k 行 UI 测试迁移**：shell 测试 100 文件/29,465 行，511 处 `ui.NewTester(...)`（92 文件），278 处直接 `.Screen =`（含 `Screen:` 字面量共 299 处），大量置位 App 内部字段（`h.app.draft` 221 处等）。规避：宿主 `App.View` 入口保持不变（`h.app.View` 被 451 处引用），屏幕切换通过测试 helper 的 `nav.Push` 适配层；先迁 1–2 个低耦合屏（Plugins/About）验证测试改写量，再批量。估计 [C] 测试改写 6–10 人日。
4. **多窗口 settings 与跨窗口刷新**：同 App 两窗口、无窗口 ID、toast 单槽、手动 `refreshWindows`、frame-hold（UX-04）。规避：Runtime 显式绑定窗口句柄（`Window.ID()` + `Services`），toast/焦点/命令按窗口队列；settings 页选中态留宿主共享；`refreshWindows` 收敛为 `Runtime.InvalidateAll()`。估计 [C] 3–5 人日。
5. **焦点/快捷键冲突与注册时序**：每帧注册、下一帧生效、先注册者胜、modal 抑制、菜单 accelerator 优先；模块化后注册顺序不再等于源码顺序。规避：启动期 `CommandRegistry` 做 chord 唯一性预检（assembly 预检期报错）、显式 `Order`、快捷键只由宿主安装一次（在 view 顶部固定顺序），模块不直接 `OnShortcut` 全局键、只用 `Handle.OnShortcut`/覆盖表。估计 [C] 2–4 人日。
6. **注册期并发** [B/C]：kernel reactive 激活可能在 Provide goroutine → UI 注册表需 mutex + `Seal()`（首帧后拒绝新注册，返回错误）或强制「UI 模块只在 Load 阶段激活」。规避成本低（1 人日），但必须在 06 明确，否则偶发竞态。
7. **帧 pass 语义误用**：模块把「初始化」写在 Build 里（懒加载/一次性副作用）会因一帧多 pass 重复执行。规避：约束 3 + 测试（同一帧点击后断言副作用计数）；宿主可提供 `Runtime.FrameHook` 但公共 API 无法给帧号（§1.2），只能靠模块自律。

### 4.3 未验证项（诚实边界）

- 未在真实桌面后端（macOS/Linux/Windows）上验证；全部框架结论来自源码 + headless `ui.Tester` 探针。
- 未实现/编译 §5 的 API 草案；接口签名是设计与证据的组合，不是通过编译的代码。
- 未在 kernel 上装配过一个 UI 模块（module.Load + Runtime builtin + 首帧注册）——属 06 的 P0 spike，本报告不声称已验证。
- 未测量帧时间/内存影响；30k 测试迁移人日为 [C] 估算。
- 多窗口只做了源码级确认（`ui/window.go:76-86` 的 `AttachContent` 每次 `newRuntime`，故 `Context` 按窗口唯一；`ui/api_gen.go:2674-2675` 明说 Content 可服务多窗口、各自状态），**未实机**（`ui.NewTester` 只能建一个引擎）；06 若依赖「同模块多窗口」，需真窗口 smoke。

## 5. 给 05/06 的最小 API 草案

### 5.1 分层、服务键与依赖方向

```
internal/contracts/ui     契约包（共享库；import mygo/ui + design；只有 UI 模块依赖）
  ├─ RuntimeKey   = cordis.NewServiceKey[Runtime]("app/ui")              // 宿主 builtin
  ├─ NavigatorKey = cordis.NewServiceKey[Navigator]("app/ui/navigator")  // 宿主 builtin
  ├─ ThemeKey     = cordis.NewServiceKey[Theme]("app/ui/theme")          // 宿主 builtin
  └─ LocaleKey    = cordis.NewServiceKey[Locale]("app/ui/locale")        // 宿主 builtin
internal/host/ui           Runtime/Navigator/Theme/Locale 实现 + 宿主 View（唯一 ui.View 持有者）
app/sessions（session 模块）  只读 read-model（Snapshot 订阅），非 UI 服务
UI 模块（screen/slot/settings/tool/command）  requires: app/ui + 所需业务服务；provides: 无（注册即贡献）
```

依据：kernel `cordis.NewServiceKey`（`service.go:40`）、`module.Provides/Requires/RequiresOptional`（`module/module.go:65,70,75`）、`AddBuiltin`（`:315`）。**注意**：kernel v0.1.1 的 `ExtensionPoint[T]/Contribution[T]` 只有文档设计、未实现（`docs/COMPILE-TIME-REGISTRY.md:184-193`），所以有序贡献/所有权/冲突检测这 100–200 行必须宿主自写（`Runtime` 内部 `registry`）。这与 §3 的约束 9/10 对应。

### 5.2 接口（Go 伪代码，未编译）

```go
type ScreenID string

type Runtime interface {
    // —— UI 线程原语：任意 goroutine 安全 ——
    Post(fn func())      // 宿主在每次 view 调用开头 drain；实现见 §1.4
    Invalidate()         // 合并到下一帧（Services.Invalidate / Window.Invalidate）
    // —— 贡献注册：装配/启动期，UI 线程；返回预检错误而非 panic ——
    RegisterScreen(Screen) error
    RegisterSlotItem(SlotID, SlotItem) error
    RegisterCommand(Command) error
    RegisterToolRenderer(ToolRenderer) error
    RegisterSettingsPage(SettingsPage) error
    // —— 帧辅助（不暴露帧循环）——
    Services(c *ui.Context) ui.Services // 帧内捕获后交后台使用；doc.go:31-33
    Window() WindowID                   // 多窗口键（宿主绑定）；无 Context 窗口 ID 的补偿
}

type Screen struct {
    ID      ScreenID
    Order   int
    Title   func(Locale) string
    Build   func(c *ui.Context)          // 只在宿主 view 调用栈内调用
    Visible func() bool                  // 运行期禁用用谓词，不增删注册表
}

type SlotID string // 如 "sidebar.footer"、"composer.toolbar"、"session.header"、"settings.nav.after"

type SlotItem struct {
    ID    string
    Slot  SlotID
    Order int
    Key   string // 元素身份前缀：宿主用 c.Key(Key) 包裹，防相邻项移动（architecture.md:2374-2377）
    Build func(c *ui.Context)
}

type Command struct {
    ID          string        // 稳定 ID，用于 keybinding.<id> 覆盖（shortcuts.go:200-210）
    Label       func(Locale) string
    Accelerator string        // mygo 菜单语法 "CmdOrCtrl+K"（parseShortcut, shortcuts.go:291-346）
    Group       CommandGroup  // 菜单 / slash / 两者
    Run         func(*Invocation) // 注入 action 服务；不得直接持 *session.Controller
}

type SettingsPage struct {
    ID, Group string
    Order     int
    Title     func(Locale) string
    Icon      func() *ui.SVG
    Build     func(c *ui.Context)
    Visible   func() bool
}

type ToolRenderer struct {
    Match   func(toolName string) bool      // 替代 toolKindFor 的硬编码 switch（toolcards.go:56-77）
    Icon    func() *ui.SVG
    Summary func(tool state.ToolState, l Locale) string
    Detail  func(c *ui.Context, tool state.ToolState) // 只读快照，不读控制器
}

type Navigator interface { // 宿主主窗口导航；settings 窗口固定根
    Current() ScreenID
    Push(c *ui.Context, id ScreenID) // c 用于多窗口定位；内部可 Post
    Back() bool
    CanGoBack() bool
    SetRoot(id ScreenID)
}

type Theme interface { Tokens(c *ui.Context) *design.Tokens } // 只读；根主题仍由宿主 c.SetTheme
type Locale interface { Lang() Lang; T(key string, args ...any) string }
```

### 5.3 每项依据

| API | 依据 |
| --- | --- |
| `Post/Invalidate` | `ui/frame.go:41-45`、`ui/services.go:24-28`、`content.go:152-160,172-178`；`loop.go:86-108`（异步 post 未导出）；探针 S2/S3/S6 |
| `RegisterScreen` | 现状 `mainScreen` switch `app.go:502-514` + `Screen` 枚举 `:20-26`；`ui.View` 单入口 `app.go:303` |
| `RegisterSlotItem` + `Key` | mygo 无 slot，但任意 `Children` 回调可调函数；身份=父+位置/Key（`architecture.md:2374-2377`） |
| `RegisterCommand` | 现状 `menuCommands`+`HandleMenuAction`（`shortcuts.go:351-443`）与 `slashCommands`+`runSlashCommand`（`composer_slash.go:68-140`、`composer_menus.go:253-320`）；快捷键覆盖 `keybinding.<id>`（`shortcuts.go:200-288`）；约束 9（重复 chord 先注册者胜，探针 S5） |
| `RegisterToolRenderer` | `toolKindFor`/`toolVariant`/图标分支（`toolcards.go:56-77,145,252,269,457-512`）；数据只读（`state.Snapshot.Tools`） |
| `RegisterSettingsPage` | `settingsTab` 6 文件 `+1` 枚举（`models_ui.go:23-29`、`plugin_details.go:26`、`memory_ui.go:32`、`settings_extra.go:29-34`、`manage_ui.go:25-26`）+ nav/body switch（`settings_ui.go:347-417,421-477`） |
| `Visible` 谓词（而非注销） | 运行期禁用在现状=条件渲染（如 Memory 页 `if a.Ctrl != nil`，`settings_ui.go:371-372`；Agent presets `:376-379`）；kernel `Entry.Disabled` 是装配期（`module/validate.go:18-43`） |
| `WindowID`/多窗口键 | `Context` 无窗口标识（§1.3 事实）；`SettingsView` 与主 `View` 共享 App（`internal/app/app.go:206,230,238`）；`ui.Handle` 每窗口独立 binding（`ui/ref.go:2-6`） |
| `Theme.Tokens` 只读 | `themeTokens`/`shellTheme`（`settings_ui.go:96-117`、`preferences.go:96`）；根主题帧内 `c.SetTheme`（`app.go:339`） |
| `Services(c)` | `ui/doc.go:31-33`（捕获 Services 而非 Context）；`services.go:8-12`（弱引用不保留窗口） |
| 宿主自写有序注册表 | kernel ExtensionPoint 未实现（`docs/COMPILE-TIME-REGISTRY.md:184-193`）；kernel 本身只提供 Service/Event |

### 5.4 装配示例（06 可直接照抄形状）

```go
// 契约包 internal/contracts/ui（示例）
var RuntimeKey = cordis.NewServiceKey[uiapi.Runtime]("app/ui")

// UI 模块（如 home 屏）
reg.Register(module.Define("zeokeel/screen-home", func(ctx cordis.Context, cfg HomeConfig) error {
    rt, err := cordis.Get(ctx, uiapi.RuntimeKey) // 依赖 app/ui builtin
    if err != nil { return err }
    return rt.RegisterScreen(uiapi.Screen{ID: "home", Order: 10, Build: func(c *ui.Context) { home.Build(c, deps) }})
}).AddRequires(module.Requires(uiapi.RuntimeKey)))

// assembly.json：{"entries":[{"id":"home","module":"zeokeel/screen-home","config":{...}}]}
```

依据：`module.Define`（`module/module.go:111`）、`module.Requires`（`:70`）、`Registry.Register`（`:297`）、`Registry.AddBuiltin`（`:315`）。宿主需在 `Load` 前 `AddBuiltin(RuntimeKey)` 等（`docs/COMPILE-TIME-REGISTRY.md:16` 提及 provider-first 装配；`load.go` 负责预检/回滚）。

### 5.5 与 DSH client-ui 的对应关系（回答「像不像 DSH」）

- DSH `dsh-client-ui-*` 通过 slot 注册把组件挂进布局，没有中心化路由表（02 §4.5：`docs/research/kernel-split/02-dsh-anatomy.md` 对 `client.js` 的摘录）。Go 侧对应物 = 本报告 §5 的 `RegisterScreen/RegisterSlotItem/...`，由宿主在唯一 `ui.View` 里消费；模块只拿 `*ui.Context` 构建，等价于 client-ui 的组件函数。
- **不照搬**：浏览器 bundle、HTTP `/plugins` 分发、懒 CJS、HMR（02 §2.3 P7b/P9）；单二进制下这些没有对应物，也不需要。
- **照搬的收益**：模块边界 + 启动预检 + 按 assembly 裁剪（UI 模块可由 `Entry.Disabled` 整体不编译/不激活），与 00 报告的 P0/P1 收益一致。

## 附录 A：探针与命令

```
# /tmp/probe-ui/main.go（摘要，未入库）
#   Runtime.Post：mutex 队列 + Services.Invalidate；view 开头 drain
#   S1 一次点击的 view 调用数（验证一帧多 pass，Context 不区分 pass）
#   S2 帧外保存的 Context：Valid()/Theme()/Toast/Invalidate 行为
#   S3 goroutine Post + Invalidate → 下一帧 drain 生效
#   S4 OnShortcut 注册后下一帧才生效
#   S5 同一窗口 chord 注册两次：先构建者胜
#   S6 点击回调里 Post 的生效时机
cd /data/mygo && PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod \
  go run /tmp/probe-ui/main.go
```

实测输出 [A]：

```
S1 one click: view_calls=3 n=1 text=true (one frame may hold up to 3 passes)
S2 outside frame: Valid=false ThemeNil=true Invalidate=no-panic Toast=no-op
S2 after Invalidate outside frame: frame_pending=true
S3 after goroutine Post: applied="" drains=4 pending=true
S3 after one frame: applied="applied-on-ui-thread" drains=5
S4 not built: hits=0
S4 built in previous frame: hits=1
S5 duplicate chord ran=[first]
S6 post from callback applied=[posted] after click
```

复现事实的命令（只读）：

```bash
cd /data/mygo
grep -rhoE '\ba\.Ctrl\.[A-Z][A-Za-z0-9]*' internal/shell --include='*.go' --exclude='*_test.go' | sed 's/^[^.]*\.//' | sort -u | wc -l  # 72
grep -rhoE '\ba\.Ctrl\.[A-Z][A-Za-z0-9]*' internal/shell --include='*.go' --exclude='*_test.go' | wc -l  # 295
grep -rhoE '\.Store\(\)\.[A-Z][A-Za-z0-9]*' internal/shell --include='*.go' --exclude='*_test.go' | wc -l   # 42
grep -rn 'sync.Map' internal/shell --include='*.go' --exclude='*_test.go' | wc -l                       # 19
grep -rn 'ui.NewTester(' internal/shell --include='*_test.go' | wc -l                                   # 511（92 文件）
grep -rn '\.Screen = ' internal/shell --include='*_test.go' | wc -l                                     # 278（+21 个 Screen: 字面量 = 299）
wc -l internal/shell/*_test.go | tail -1                                                                # 29,465
awk 'NR>=29 && NR<=291 && /^\t[A-Za-z_][A-Za-z0-9_]*[ \t]+[^=]/' internal/shell/app.go | wc -l           # 123
```

## 附录 B：本报告没做的事

- 未改 `/data/mygo` 产品代码、未 `git commit`、未新增依赖；仅新增本文件。
- 未实现 §5 API、未接入 kernel、未跑 `go test ./internal/shell`（无桌面后端/时间成本）；所有「成立」判断都附了可复现证据或标注 [C]。
- 05/06 若需要「UI 模块清单/装配 JSON/回归清单」，请以本报告 §2.3/§2.4/§5 为输入，并以 03 的 session/tools 契约抽取（T1）为前置。
