# ADR 0005：Settings 独立原生窗口（桌面 P1）

> **2026-11-05 更新**：原生窗口菜单栏已退役（[ADR 0006](0006-retire-native-menu-bar.md)）；本 ADR 中「菜单栏」相关描述（§2.6 平台表、§2.2 的 `dispatchMenuAction("settings")` 路径）以 ADR 0006 为准——Settings 入口现为 Cmd/Ctrl+,、托盘与应用内，不再有菜单项。

- 日期：2026-11-05
- 状态：**已采纳**（桌面交互 P1；菜单栏入口部分被 ADR 0006 取代）
- 决策者：用户（ZeoKeel = 独立品牌桌面产品，交互按桌面平台惯例本地化）；记录：native-prototype
- 关联：[ADR 0003 桌面壳集成](0003-desktop-shell-integration.md)（系统菜单栏 / 菜单 action / 窗口状态）、[ADR 0002 品牌主题基础](0002-brand-theme-foundation.md)、[UI/UX 评估 §5.2/§7](../research/03-ux-design.md)（P1「设置独立窗口」）、[mygo 窗口文档](../../.cache/gomod/github.com/egoist/mygo@v0.7.4/docs/windows.md)
- 实施：`internal/app/settings_window.go`（新增）、`internal/app/settings_window_test.go`（新增）、`internal/app/app.go`（接线）、`internal/shell/settings_ui.go`（`SettingsView` + host-aware Close）、`internal/shell/app.go`（host hooks / `openSettings` 路由）、`internal/shell/settings_ui_test.go`、预览 `docs/design/preview/settings-window-native-{light,dark}.png`

## 1. 背景与目标

`docs/research/03-ux-design.md` §7 把「设置移到独立窗口」列为 P1 桌面缺口：此前的设置是应用内 modal overlay + scrim——这是 Web 惯例（浏览器里只有一个画布），桌面产品通常给设置自己的窗口：用户可以把它和主窗口并排放、切回主窗口继续操作、关掉它不影响应用。

目标（在纯 Go / no-cgo、无显示器可测的前提下）：

1. **非模态独立窗口**：`mygo.NewWindow` + `ui.View(shell.App.SettingsView)`，与主窗口并存、可各自聚焦；主窗口保持可操作。
2. **入口一致**：Cmd/Ctrl+,、原生菜单 Settings…、More 菜单、命令面板全部打开（已打开则聚焦）同一个窗口。
3. **状态同步**：设置读写仍走同一个 `shell.App` 与同一个 settings store（Controller.Settings）；在设置窗口改的主题/语言/会话字号即时反映到主窗口，反之亦然。
4. **关闭/退出语义**：关闭设置窗口隐藏而不退出；再次打开保留上次 Tab 与滚动位置；退出应用时两窗口都真正关闭。
5. **无显示器单测**：窗口创建参数、单例、focus、关闭/退出语义、菜单 action 分发全部可测；真实 mygo 窗口只在桌面运行期创建。

## 2. 决策

### 2.1 窗口模型：惰性创建的单例，close-to-hide

- **内容共用现有设置视图**。把 `internal/shell/settings_ui.go` 的面板拆成「nav + body」与宿主外壳：
  - overlay 宿主：`settingsPanelLive`（居中卡片、scrim、`RoleDialog`、Escape/快捷键在遮罩层注册）；
  - 窗口宿主：`SettingsView`（整窗填充、无 scrim、nav 填满窗口高度、`Cmd/Ctrl+,` 聚焦自身、Escape 关闭窗口）。
  - 两者调用同一 `settingsNavLive` / `settingsBodyLive` / 各设置页，功能、Tab、焦点、a11y 标签与持久化只有一份实现；不存在「窗口版设置」的第二套 UI。
- **单例**：`settingsWindowController` 持有唯一窗口。第一次打开时 `manager.New`（mygo `NewWindow`，`Hidden: true` 后 `Show` 避免白屏），之后每次打开只 `Focus`（窗口已显示）或 `Show + Focus`（窗口被隐藏）。不创建第二个设置窗口。
- **尺寸**：900×720（最小 640×480）。`SettingsView` 是两列布局（200 DIP nav + 页面），这个尺寸给页面留出舒适列宽；它是品牌布局尺寸（`settingsWindowWidth` 常量），不是从外部页面采样的像素。
- **关闭 = 隐藏**：标题栏关闭按钮走 `Window.OnClose` →
  - 应用退出中（`a.quitting`）→ 允许关闭（真正的窗口销毁）；
  - 否则 `e.PreventDefault()` + `win.Hide()`，与主窗口的 close-to-hide 语义一致，应用继续运行。
- **再次打开保留 Tab/滚动**：窗口实例不销毁，`shellUIState.settingsTab` 与 mygo 的元素状态（滚动等）都还在；`Open` 只是 Show/Focus。
- **位置/大小持久化**：窗口使用 `StateKey: "settings"`，接入 ADR 0003 的 `window.json` store，与主窗口 `StateKey: "main"` 共用同一个 `map[key]WindowState`（`window_state_test` 已覆盖多 key），启动恢复/退出保存由既有逻辑承担，未新增存储格式。

### 2.2 状态同步：一个 Controller，两条窗口

```text
设置窗口 (SettingsView)                主窗口 (App.View)
        │ 控件 updateSettings()              │
        └──────────────┬──────────────────────┘
                       ▼
          shell.App（同一个实例）
            currentSettings 缓存 / lang / prefs
                       │  SavePreservingLanguage
                       ▼
        session.Controller.Settings()（同一个 store）
                       │
                       ▼
        settingsChangedHook → internal/app.refreshWindows()
                       │
             ┌─────────┴─────────┐
             ▼                   ▼
       主窗口 Invalidate()   设置窗口 Invalidate()
```

- 两个窗口渲染的是**同一个 `shell.App`**（`internal/app/run` 只构造一次），所以主题令牌、语言解析、会话字号、快捷键 override 天然共享，不需要跨窗口状态复制。
- `updateSettings` 成功后调用新增的 `settingsChangedHook`；`internal/app` 注册的回调 `refreshWindows()` 使主窗口与设置窗口都重绘，主题/语言/字号改动即时可见（此前 overlay 只重绘主窗口自身）。
- 设置数据仍由 `session.Controller.Settings()` 持久化；窗口化没有引入新的 store 或 schema。

### 2.3 入口与回退：同一个 `openSettings`，host opener 决定宿主

- 所有入口（`Cmd/Ctrl+,` 的窗口快捷键与可重绑 override、原生菜单 `MenuActionSettings`、More 菜单、命令面板）都收敛到 `shell.App.openSettings()`。
- `internal/app` 通过 `shell.App.SetSettingsWindowOpener(fn func() bool)` 注册宿主：opener 显示/聚焦窗口并报告是否成功。
  - opener 返回 `true`：主窗口**不**切到 Settings，屏幕保持原样；
  - opener 返回 `false`，或没有注册 opener：走原 overlay 路径（`Screen = Settings` + scrim），行为与之前完全一致。
- **为什么用自动回退而不是用户设置/构建开关**：overlay 与窗口共用同一视图，回退只发生在「宿主无法创建窗口」这一种情况（headless 测试、桌面后端不可用、或将来某个平台限制）。加一个用户可见的「使用独立设置窗口」开关会把两套宿主的选择暴露给用户，却几乎没有人会去关掉它，还会让两条路径都被长期维护；自动回退让桌面默认永远走窗口，而非桌面环境（测试/无 GUI）保持可渲染。**取舍**：overlay 作为回退路径仍需保留 `Screen=Settings` 分支与遮罩快捷键，但不再有用户可见的设置项。
- Close 控件：`settingsWindowClose` hook 存在时隐藏窗口；不存在时关闭 overlay（`closeSettingsRequested` 是唯一入口，X 按钮/「Close」/Escape 都走它）。

### 2.4 关闭、退出与多窗口生命周期

- **关闭设置窗口不退出应用**：windows 只是隐藏，`mygo.App.OnWindowAllClosed` 的既有逻辑（主窗口隐藏到托盘）不受影响；隐藏的窗口不是「closed」，所以不会触发 all-closed 路径。
- **退出**：菜单 Quit / 托盘 Quit / 系统退出都经 `mygo.App.Quit` 关闭窗口；`OnBeforeQuit` 已置 `a.quitting`，两窗口的 `OnClose` 都放行真正的关闭（与 ADR 0003 的主窗口语义一致）。
- **焦点**：打开/再次 Cmd+, 时 `Focus()`；设置窗口是普通窗口而不是 `Parent` 子窗口/panel，因此非模态、可拖动、可最小化，行为由平台窗口管理器决定。

### 2.5 无显示器单测：接口 + fake，真实实现只做窗口适配

mygo 的窗口只能在桌面后端（`App.Run` 之后）创建，无显示器测试无法构造。因此把窗口生命周期与 mygo 分开：

```go
type settingsWindowHost interface { Show(); Hide(); Focus(); SetTitle(string); Invalidate() }
type settingsWindowManager interface { New(settingsWindowOptions, requestClose func() bool) (settingsWindowHost, error) }
```

- **生产实现** `mygoSettingsWindows`：只做 `mygo.NewWindow` / `OnClose` / `Show` / `Hide` / `Focus` 的翻译；`App.IsReady()` 为 false 时返回 `errSettingsWindowUnavailable`，让 shell 回退 overlay。
- **测试** 注入 `fakeSettingsWindows`（记录创建次数、options、show/focus/hide/invalidate 计数，并可模拟标题栏关闭）。覆盖矩阵见 `settings_window_test.go`：
  - 单例 + 重复打开只 focus；
  - 原生关闭 → 隐藏且拒绝真正关闭、重新打开复用同一窗口并重读标题（语言切换）；
  - 退出中关闭放行；
  - 窗口创建失败 → 返回错误/回退 overlay；
  - 创建参数（900×720、最小 640×480、`StateKey="settings"`、Title/Content 非空）；
  - app 层 `dispatchMenuAction("settings")` → opener → 窗口创建；host 不可用 → `Screen=Settings` overlay 回退；
  - `refreshWindows()` 重绘设置窗口（主窗口为 nil 时安全）。
- shell 侧新增 `TestSettingsViewRendersStandalone`（`ui.Tester` 直接渲染 `SettingsView`：nav/页面/Close host/Escape/切页）与 `TestOpenSettingsRoutesToHostWindow` / `TestSettingsChangedHookRuns`。

### 2.6 跨平台差异

| 平台 | 菜单栏 | Cmd/Ctrl+, | 备注 |
| --- | --- | --- | --- |
| macOS | 应用级菜单栏（已有 Settings… ⌘,） | 全局菜单 accelerator → `MenuActionSettings` → 窗口 | 设置窗口聚焦时菜单仍可用；窗口标题随语言 |
| Windows | 主窗口菜单栏（File ▸ Settings… Ctrl+,） | 主窗口快捷键注册；设置窗口聚焦时由 `SettingsView` 自己注册 Ctrl+, 聚焦自身 | 设置窗口无菜单栏（窗口内容自绘） |
| Linux | 同 Windows（GTK 窗口菜单栏） | 同上 | 关窗隐藏由窗口管理器关闭按钮触发，`OnClose` 拦截 |

统一行为：设置窗口关闭只隐藏；菜单 Settings… 在已打开时聚焦；Esc/关闭按钮都经 `closeSettingsRequested`。

## 3. 后果与取舍

**收益**：
- Settings 不再是遮挡主窗口的 modal + scrim（Web 惯例），符合桌面产品预期；可以和主窗口并排使用；
- 设置 UI 只有一份（nav/body/页面/持久化），窗口与 overlay 共用，不会漂移；
- 主窗口与设置窗口共享同一个 `shell.App`/Controller，改动即时双向可见；
- 窗口位置/大小自动接入 ADR 0003 的 window.json（`StateKey="settings"`）；
- 生命周期逻辑 100% 可在无显示器环境单测（接口 + fake），真实 mygo 适配器很薄。

**代价 / 取舍**：
- 多一个窗口实例与其内容树常驻内存（惰性创建，只有打开过才存在；隐藏时保留 Tab/滚动状态是刻意的）；
- overlay 回退路径保留（headless/测试/创建失败），仍需维护 `Screen=Settings` 分支；
- 设置窗口的快捷键只注册了 Cmd+, 与 Esc；其余应用级快捷键（新建会话/搜索等）属于主窗口，不在设置窗口生效（与多数桌面应用一致）；
- 设置窗口与主窗口都显示 toast：保存反馈出现在发起保存的窗口。

## 4. 遗留

1. **窗口位置持久化**已由 `StateKey="settings"` + ADR 0003 的 store 实现，但未做「多窗口几何联动」（例如设置窗口跟随主窗口屏幕）；后续可在 `window_state.go` 增加窗口间偏移策略。
2. 设置窗口**无菜单栏**（Windows/Linux）：若用户期望设置窗口里也能用 File/Edit 菜单，可在 `internal/app` 用同一模板 `settingsWin.SetMenu(...)`（mygo 支持 per-window menu）；当前保持最小窗口。
3. 大型设置页的**滚动位置**保留依赖窗口实例常驻；若将来改为销毁重建，需要把滚动位置提升到 `shellUIState` 持久化。
4. 没有做「设置窗口打开时主窗口仍响应 Cmd+,」的双向测试（主窗口快捷键在窗口失焦时不派发，属平台行为）；macOS 由应用级菜单 accelerator 覆盖。
