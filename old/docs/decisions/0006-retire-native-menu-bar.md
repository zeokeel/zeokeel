# ADR 0006：退役原生窗口菜单栏（File/Edit/View/Window）

- 日期：2026-11-05
- 状态：**已采纳**（桌面交互回退；取代 [ADR 0003](0003-desktop-shell-integration.md) §2.1 的系统菜单栏与 §4 的菜单栏条目，ADR 0003 的窗口状态持久化部分继续有效）
- 决策者：用户（2026-11-05 原话「file edit 这些菜单栏不需要」）；记录：menu-removal
- 关联：[ADR 0003 桌面壳集成](0003-desktop-shell-integration.md)、[ADR 0005 Settings 独立原生窗口](0005-settings-window.md)、[mygo 菜单文档](../../.cache/gomod/github.com/egoist/mygo@v0.7.4/docs/menus.md)、[mygo 窗口文档](../../.cache/gomod/github.com/egoist/mygo@v0.7.4/docs/windows.md)
- 实施：`internal/app/app.go`（移除 `installMenu` 调用与菜单 hook 接线、`mygo.App.SetMenu(nil)`）、`internal/app/quit.go`（原 `menu.go` 改名；删除菜单模板/安装/分发，只留进程级 Quit）、`internal/app/quit_test.go`（原 `menu_test.go` 改名；改写为 Quit 路径测试）、`internal/app/settings_window_test.go`（改走 shell 的公开命令入口）

## 1. 背景与目标

ADR 0003 依据桌面惯例，在 2026-11-05 为 ZeoKeel 增加了系统菜单栏：macOS 是应用级 App/File/Edit/View/Window，Windows/Linux 是主窗口顶部的 File/Edit/View/Window（`internal/app/menu.go` 的 `buildMenu`/`installMenu`）。用户在实际使用 Windows 版后要求移除窗口顶部的菜单栏：「file edit 这些菜单栏不需要」。

目标：

1. **不再有原生窗口菜单栏**（所有平台）。Windows/Linux 让出窗口顶部；macOS 不再有应用菜单（用户/Lead 明确「全部移除」，不留 macOS App 菜单）；
2. **托盘菜单保留**：`internal/app/app.go` 的 `installTray`（Show ZeoKeel / Quit），它是托盘而不是窗口菜单栏；
3. **shell 键盘快捷键保留**：`internal/shell/shortcuts.go` 的 `installShortcuts`、chrome 快捷键（Cmd/Ctrl+N / , / K / B 等），它们继续从 `menuCommands` 表解析 accelerator；
4. **应用内入口保留**：侧栏 More 菜单（Settings / About / Quit）、界面按钮、Settings 独立窗口（ADR 0005）；
5. 不改变窗口状态持久化、关闭到托盘、通知等其它桌面行为。

## 2. 决策

### 2.1 移除范围与安装机制

- 删除 `internal/app` 的整个菜单模板与安装/分发代码：`buildMenu`、`appMenuItems`、`fileMenuItems`、`menuCommandItem`、`installMenu`、`dispatchMenuAction`、`handleMenuAction`；
- `app.run()` 不再调用 `a.installMenu()`，也不再通过 `shellApp.SetMenuAction(a.handleMenuAction)` 注册菜单 hook（该 hook 只为原生菜单分发服务）；
- **显式移除应用菜单**：`run()` 在 `SetName` 之后、`WhenReady` 之前调用 `mygo.App.SetMenu(nil)`。原因：mygo 在 macOS 上「没有调用 SetMenu 时会安装默认 App/File/Edit/View/Window 菜单栏」（mygo `menu.go` 的 `defaultMenu` 与 `app.go` 的 `SetMenu` 文档）；不显式置 nil，macOS 只是把定制菜单换回默认菜单，窗口顶部依旧有菜单栏。Windows/Linux 的窗口默认没有菜单栏（`docs/menus.md`「windows have none by default」），不再安装后自然消失；
- 只保留进程级 Quit 路径 `quitApplication`（托盘 Quit 使用；`run()` 的 `mygo.App.OnBeforeQuit` 仍覆盖托盘/应用内/OS 的所有退出路径来置 `quitting`）；
- 文件层面：`internal/app/menu.go` 与 `menu_test.go` 保留文件名、内容缩减为 Quit 路径（避免删除非 `.cache` 文件；后续可改名为 `quit.go`/`quit_test.go`）。

### 2.2 命令入口：快捷键 / 托盘 / 应用内 UI

原生菜单项删除后，同一批命令的入口如下（命令表 `menuCommands`、快捷键绑定均未改动）：

| 命令 | 移除前的入口 | 移除后的入口 |
| --- | --- | --- |
| New Session `Cmd/Ctrl+N` | File 菜单项 accelerator + 窗口快捷键 | 窗口快捷键（`installShortcuts`）+ 界面按钮 |
| Settings `Cmd/Ctrl+,` | 应用/File 菜单项 accelerator + 窗口快捷键 | 窗口快捷键 + More 菜单「Settings」+ 设置按钮 |
| Search `Cmd/Ctrl+K` | File 菜单项 accelerator + 窗口快捷键 | 窗口快捷键（会话内聚焦 composer，否则搜索面板） |
| Toggle Sidebar `Cmd/Ctrl+B` | View 菜单项 accelerator + chrome 快捷键 | chrome 快捷键（`installChromeShortcuts`，可重绑） |
| Plugins / Automation | View 菜单项 | 应用内导航（插件/自动化界面） |
| Quit `Cmd/Ctrl+Q` | 菜单项 accelerator（**唯一**绑定） | 托盘 Quit、应用内 More 菜单 Quit、OS 退出（macOS 另有 Dock 右键 Quit） |
| About | macOS App 菜单 role | More 菜单 About 对话框 |
| Full Screen / Minimize / Zoom | View/Window 菜单 role | 系统窗口按钮（标题栏）；`⌃⌘F` 随菜单移除 |

- shell 的 `menuCommands` / `MenuCommands()` / `MenuCommandByAction()` 保留：`installShortcuts` 仍解析同一串 accelerator 注册窗口快捷键（accelerator 单一来源不变）；
- shell 的原生菜单期 API `MenuLabel`、`SetMenuAction`、`HandleMenuAction` 保留定义（`internal/shell` 不在本任务改动范围，其测试仍用 `HandleMenuAction` 验证 shell 侧分发），但 `internal/app` 不再注册/调用；这是后续可评估退役的遗留（见 §5）。

### 2.3 Edit 菜单与剪贴板：源码级验证与剩余风险

ADR 0003 §2.1/§4 记录「Edit 菜单承载 Cmd/Ctrl+C/V/A，用 `RoleEditMenu` 保证原生文本输入框、IME 与系统剪贴板行为一致」。移除 Edit 菜单前做了源码级验证，结论：**ZeoKeel 自己的文本输入不依赖 Edit 菜单**。

- mygo 的 Go 侧编辑器自己处理编辑快捷键：`ui/editor_navigation.go` 的 `wants` 在 `m == Cmd`（macOS Command、其它平台 Control，见 `ui/keys.go` 的 `Cmd`）时接管 C/X/V/A/Z/Y，`onKey` 直接调用 copy/cut/paste/undo/redo/selectAll 并通过宿主剪贴板读写；`ui/textselection.go` 对可选择文本同样处理 `Cmd+A/C`。这些是菜单无关的键盘处理；
- 菜单 role 只是把同样的命令路由到同一个 Go 处理器（如 `internal/darwin/surface.go` 注册的 `command("copy")…`），不是实现本身；
- macOS 无主菜单时，`Cmd+C/V` 等按键作为普通 keyDown 到达 surface（`internal/darwin/surface.go` 的 `keyDown:` 对 Super/Ctrl 组合照常发送 `KeyPressed`），再由 ui 层处理。

剩余风险（本环境是 Linux 无显示器，无法真机验证 macOS/Windows）：

1. **非 mygo 自绘的输入上下文**（系统原生对话框内的文本框、IME 组合边缘情况）依赖平台惯例；若某平台的原生控件靠 Edit 菜单转发 `copy:`/`paste:`，会失去快捷键，需要真机补测；
2. **macOS 无主菜单是 HIG 偏差**：`Cmd+Q`、菜单型全屏快捷键（`⌃⌘F`）、Accessibility/VoiceOver 的全局菜单导航随菜单消失；
3. 若未来嵌入网页/第三方内容，WebView 的编辑快捷键在 macOS 上通常依赖主菜单，接入时需重新评估。

风险缓解：所有命令仍有应用内 UI/托盘/快捷键三个入口；若真机验证发现剪贴板回归，最小回退是只恢复 macOS 的 `RoleEditMenu`（一次 `mygo.App.SetMenu`），无需恢复完整菜单。

### 2.4 跨平台差异

| 平台 | 菜单栏现状 | 命令入口 |
| --- | --- | --- |
| macOS | 无应用菜单（显式 `SetMenu(nil)`，不再有默认 App/File/Edit/View/Window 菜单栏） | `Cmd` 快捷键、托盘状态栏图标菜单（显示/ZeoKeel 退出）、应用内 More 菜单、Dock 右键 Quit / 系统退出 |
| Windows | 主窗口无菜单栏（不再 `Window.SetMenu`；mygo 窗口默认无菜单栏） | `Ctrl` 快捷键、托盘图标菜单、应用内入口 |
| Linux | 同 Windows（GTK 窗口无菜单栏） | 同上 |

### 2.5 备选方案与取舍

- `WindowOptions.AutoHideMenuBar`（Alt/F10 唤出菜单）：**否**。用户要的是「不需要」而不是「隐藏」；隐藏仍保留菜单语义、平台分支与测试成本。
- 只移除 Windows/Linux 菜单、保留 macOS App 菜单：**否**。按用户/Lead 指示「全部移除、托盘+快捷键保留」执行；一致且代码更简单。若 macOS 真机反馈需要 App 菜单（`Cmd+Q`/About），按 §5 单独恢复最小 `RoleAppMenu`。
- 保留菜单模板代码但停用：**否**。死代码会漂移；直接删除，靠本 ADR 与测试记录回退点。

## 3. 后果与取舍

**收益**：

- Windows/Linux 窗口顶部让出菜单栏高度，界面更干净，符合用户预期；
- 去掉一条跨平台原生 UI 分支（菜单模板/accelerator/标签/语言重建），命令只剩「窗口快捷键 + 应用内 UI + 托盘」三条路径；
- 不再有原生菜单 accelerator 与窗口快捷键的优先级竞争（ADR 0003 §4 记录的复杂性消失），命令只有一条代码路径，不会出现菜单与快捷键行为漂移。

**代价**：

- **可发现性下降**：命令不再出现在系统菜单。缓解：Settings 的快捷键编辑器（`defaultShortcutRows`）列出全部快捷键，应用内按钮与 More 菜单保留；
- **Quit 的 ⌘Q/Ctrl+Q 消失**：原生菜单 accelerator 曾是唯一绑定；现在走托盘 Quit / 应用内 More Quit；macOS 还有 Dock 右键与系统退出；
- **macOS 无 App 菜单**：About/Hide/Services/全屏/窗口菜单项消失，属 HIG 偏差；Edit 菜单相关风险见 §2.3；
- ADR 0003 的「菜单标签随语言」「菜单栏键盘可达性（Alt/F10）」等行为整体作废（不再适用）。

## 4. 验证

2026-11-05 在本仓库（HEAD `e6f97c2` + 本改动）执行：

```bash
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 GOMODCACHE=/data/mygo/.cache/gomod GOSUMDB=off
gofmt -l internal/app
go vet ./...
go test ./internal/app ./internal/shell -count=1
```

结果：`gofmt` 无输出；`go vet ./...` 通过；`internal/app ok`、`internal/shell ok`。（本会话有并行队友改 `internal/shell`、`internal/state`、`internal/session`；以上是本改动完成时的结果，最终集成以 Lead 的整树验证为准。）

测试变更：

- `internal/app/menu_test.go`：删除只覆盖被删菜单模板的测试（顶层 role 顺序、命令 accelerator、菜单点击分发、语言标签、`installMenu` 无窗口安全等）；改写为 `TestQuitApplicationRunsSeam`（托盘 Quit 路径：跑 quit seam 且置 `quitting`）；
- `internal/app/settings_window_test.go`：`TestAppSettingsActionOpensWindow` / `TestAppSettingsActionFallsBackToOverlay` 改用 shell 的公开命令入口 `shellApp.HandleMenuAction(shell.MenuActionSettings)`，不再注册 app 侧 hook；覆盖的「shell 命令 → host opener → 单例窗口 / overlay 回退」行为不变；
- shell 侧不需改动：`installShortcuts`/chrome 的快捷键绑定与 `menuCommands` 表原样保留，`internal/shell` 测试未改。

## 5. 遗留

1. **shell 原生菜单期 API**：`MenuLabel`、`SetMenuAction`、`HandleMenuAction` 现在只有测试/公开 API 使用（internal/app 不再注册 hook）；后续可评估退役（本任务约束不改 `internal/shell`）。
2. **macOS 真机 QA**：`Cmd+C/V/A/X/Z` 在 composer/搜索/设置输入框与 IME、`Cmd+,`/`Cmd+N`/`Cmd+K`、托盘菜单、Quit 路径；若发现剪贴板回归，按 §2.3 最小回退恢复 `RoleEditMenu`。
3. **Windows 真机 QA**：确认窗口顶部无菜单栏、快捷键行为与移除前一致、关闭到托盘/托盘 Quit 不变。
4. 若用户后续想要 macOS App 菜单（`Cmd+Q`/About），按 §2.3 的最小回退方式新增 ADR 恢复 macOS-only 菜单。
5. ADR 0003 原文不修改（历史记录）；其 §2.1 与 §4 的菜单栏部分由本 ADR 取代，§2.2 窗口状态持久化继续有效。
