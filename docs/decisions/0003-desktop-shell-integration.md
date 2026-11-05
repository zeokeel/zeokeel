# ADR 0003：桌面壳集成（系统菜单栏 + 窗口状态持久化）

> **2026-11-05 更新**：本 ADR 的**菜单栏部分**（§2.1 系统菜单栏、§2.2 命令分发、§4 菜单栏条目）已由 [ADR 0006](0006-retire-native-menu-bar.md) 取代（用户决定移除原生窗口菜单栏）；**窗口状态持久化**与桌面壳其余部分继续有效。

- 日期：2026-11-05
- 状态：**部分取代**（菜单栏部分由 ADR 0006 取代；窗口状态持久化仍有效）
- 决策者：用户（ZeoKeel = 独立品牌桌面产品，交互按桌面平台惯例本地化）；记录：Lead / test-migrate-a
- 关联：[ADR 0001 退役 DSH 像素对齐](0001-retire-dsh-pixel-parity.md)、[ADR 0002 品牌主题基础](0002-brand-theme-foundation.md)、[UI/UX 评估 §5.2/§6/§7](../research/03-ux-design.md)、[mygo 菜单文档](../../.cache/gomod/github.com/egoist/mygo@v0.7.4/docs/menus.md)、[mygo 窗口文档](../../.cache/gomod/github.com/egoist/mygo@v0.7.4/docs/windows.md)
- 实施：`internal/app/menu.go`（新增）、`internal/app/window_state.go`（新增）、`internal/app/app.go`、`internal/app/menu_test.go`（新增）、`internal/app/window_state_test.go`（新增）、`internal/shell/app.go`（菜单 hook）、`internal/shell/shortcuts.go`（命令表/accelerator）

## 1. 背景与目标

`docs/research/03-ux-design.md` §5.2 把「菜单栏里能找到命令」列为 P0 桌面缺口：此前的 `internal/app/app.go` 只有托盘菜单，应用命令（新建会话、设置、搜索等）没有进入系统菜单栏，快捷键不可发现，macOS 上连 Cmd+Q/Cmd+C/V 的常规菜单项也没有。窗口固定 1600×1000（最小 900×600），关闭窗口只是隐藏到托盘，重新打开后尺寸/位置/最大化状态全部丢失（§5.2「窗口大小/位置被记住」）。

本批次（P0-1）只解决两件事，纯 Go / no-cgo 约束不变，单测可在无显示器环境运行：

1. **系统菜单栏**：App/File/Edit/View/Window 结构；应用命令进原生菜单并带 accelerator；菜单点击经 shell 暴露的 hook 分发，`internal/app` 与 `internal/shell` 保持解耦；
2. **窗口状态持久化**：保存/恢复 width/height/x/y/maximized（mygo 同时保存 fullScreen），JSON 存到应用数据目录，启动恢复、关闭（退出）保存、损坏文件 fallback。

## 2. 决策

### 2.1 系统菜单栏

**结构**（所有平台同一套模板；mygo 负责按平台隐藏 macOS 专属 role）：

| 顶层菜单 | role | 条目（accelerator） | 行为 |
| --- | --- | --- | --- |
| App（仅 macOS） | `RoleAppMenu`（自定义 submenu） | Settings… ⌘, · About · Services · Hide/Hide Others/Show All · Quit ⌘Q | `MenuActionSettings`、`MenuActionQuit` 进 shell 分发；其余用 mygo role |
| File | `RoleFileMenu`（自定义 submenu） | New Session `CmdOrCtrl+N` · Search `CmdOrCtrl+K`；Windows/Linux 追加 Settings… `CmdOrCtrl+,` 与 Quit `CmdOrCtrl+Q` | 同上 |
| Edit | `RoleEditMenu`（整菜单 role） | Undo/Redo/Cut/Copy/Paste/Delete/Select All | mygo 原生编辑 role：`Cmd/Ctrl+C/V/A` 在输入框和 composer 里生效 |
| View | `RoleViewMenu`（自定义 submenu） | Toggle Sidebar `CmdOrCtrl+B` · Plugins · Automation · Toggle Full Screen | 前三条经 shell 分发；全屏用 mygo role |
| Window | `RoleWindowMenu`（整菜单 role） | macOS：Minimize/Zoom/Bring All to Front；Win/Linux：Minimize/Close | mygo 原生窗口 role |

**菜单动作解耦**（`internal/app` 不碰 shell 内部状态，`internal/shell` 不 import mygo/app）：

```text
MenuItem.Click
  → app.dispatchMenuAction(action string)
  → shell.App.HandleMenuAction(action)      // shell 的 UI 命令在这里执行
       ├─ new_session → newSession()
       ├─ settings    → openSettings()
       ├─ search      → 会话内聚焦 composer，否则 openPalette()
       ├─ plugins / automation → 切屏
       ├─ toggle_sidebar → toggleSidebar()
       ├─ quit        → 自己不执行，交给 hook
       └─ 未知 action → 返回 false（不 panic）
  → shell.App.menuAction(action)            // SetMenuAction 注册的 hook
  → app.handleMenuAction(action)            // Quit → quitApplication()
```

- shell 新增最小 API：`SetMenuAction(fn func(action string))` 注册 desktop shell 的处理器；`HandleMenuAction(action string) bool` 是菜单点击的唯一入口；模型/UI 行为留在 shell。
- 命令字面量、本地化标签与 accelerator 集中在 `internal/shell/shortcuts.go` 的 `menuCommands` 表（`MenuCommands()`/`MenuCommandByAction()`/`MenuLabel()` 导出）；`internal/app` 只按 action 建原生菜单项，不重复定义快捷键。
- **accelerator 单一来源**：`installShortcuts` 直接解析 `menuCommands` 里同一串 accelerator 注册窗口快捷键，菜单与键盘不会漂移；`Cmd/Ctrl+B`（Toggle Sidebar）由 `installChromeShortcuts` 绑定、表内镜像（其可重绑 override 在 `chrome.go`）。
- **Quit 走自定义命令项而不是 `RoleQuit`**：保证 Quit 也经 hook 分发，并与托盘 Quit 共用 `quitApplication()`；同时在 `run()` 注册 `App.OnBeforeQuit` 置 `quitting`，覆盖 macOS 系统退出等不经菜单的路径，避免「关窗隐藏」逻辑在退出过程中把窗口又显示回来。
- **语言**：菜单在启动时按 shell 当前语言（`MenuLabel`）构建；应用内切换语言不会重建菜单（见 §6 已知限制）。

### 2.2 窗口状态持久化

- **存储位置**：`DataDir()/window.json`（默认 `~/.zeokeel/window.json`；`ZEOKEEL_DATA_DIR` 可覆盖，便携安装随数据目录携带）。使用 mygo 的 `App.SetWindowStateStore(WindowStateStore)` 自定义 store，而不是 mygo 默认的用户数据目录 `window-state.json`。
- **格式**（`map[key]WindowState`，主窗口 `StateKey="main"`）：

  ```json
  {
    "main": {"x": 120, "y": 80, "width": 1600, "height": 1000, "maximized": false}
  }
  ```

  字段来自 `mygo.WindowState`：`x`/`y`/`width`/`height`/`maximized`/`fullScreen`（后两项 `omitempty`）。负坐标合法（多显示器）。
- **生命周期由 mygo 驱动**：首个 `StateKey` 窗口创建时 `LoadWindowStates()`（只调一次）；窗口关闭和应用退出时 `SaveWindowStates()`。本应用「关闭窗口」是隐藏到托盘，因此真正落盘发生在应用退出（托盘 Quit / 菜单 Quit / 系统退出）时；`App.OnBeforeQuit` 也保证退出路径统一。
- **关闭语义修正（本批顺手修复）**：原来的 `OnClosed` + `Hide()` 写法在窗口销毁之后才执行，`Hide` 和托盘的 `Show` 都会变成空操作（关一次窗就再也打不开）。现在用 `Window.OnClose` + `e.PreventDefault()` + `Hide()` 实现关闭到托盘：非退出路径窗口保持存活；`a.quitting`（菜单/托盘 Quit 与 `OnBeforeQuit` 设置）为真时不再拦截，退出时窗口正常关闭。
- **恢复规则**（mygo `restoreWindowState`）：恢复 bounds、`Maximized`、`FullScreen`；若窗口不在任何已连接显示器上，则保留尺寸（夹到主显示器工作区）并居中。无保存状态时使用 `WindowOptions` 默认值（1600×1000，最小 900×600）。
- **健壮性**：
  - 文件不存在 → 返回无状态、不报错；
  - JSON 损坏 → 记录日志并返回无状态（本次启动用默认值），下一次保存用合法 JSON 覆盖；
  - `width<=0 || height<=0` 的条目在 Load 时丢弃（mygo 也无法恢复这种窗口）；
  - 保存为原子写：先写 `window.json.tmp`（0600），再 `rename` 覆盖；目录按需 `MkdirAll(0700)`；rename 失败清理临时文件。

## 3. 测试（无显示器、无 GUI）

| 测试文件 | 覆盖 |
| --- | --- |
| `internal/app/menu_test.go` | 顶层菜单 role 顺序（App/File/Edit/View/Window）、File/View/App 菜单中的命令项与 accelerator、命令表 accelerator 与 shortcuts 一致、菜单点击经 shell 执行（Settings）、Quit 经 hook 执行 quit 并置 quitting、hook 收到每个命令、未知/空 action 不 panic、无窗口时 `installMenu` 安全 |
| `internal/app/window_state_test.go` | 保存/读取 round-trip（含负坐标、maximized）、文件不存在 fallback + 懒建目录、损坏文件 fallback 且下次保存修复、非法尺寸条目丢弃、空状态保存为 `{}`、原子写无 `.tmp` 残留、文件权限 0600 |

验证命令（纯 Go，无需显示器）：

```bash
export PATH=/usr/local/go/bin:$PATH GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0
go build ./...
go test -count=1 ./internal/app
go test -count=1 -run 'TestMenu|TestWindowState' ./internal/app
gofmt -l internal/app internal/shell/app.go internal/shell/shortcuts.go
```

## 4. 跨平台注意

- **macOS**：菜单栏属于应用（`mygo.App.SetMenu`），默认应用菜单由 `RoleAppMenu` 提供；本 ADR 用自定义 submenu 保留 About/Services/Hide/Quit 的 role 行为，把 Settings… 放到应用菜单（平台惯例），Quit 使用本节 2.1 的自定义命令项（⌘Q）以统一 hook 分发。
- **Windows/Linux**：菜单栏属于窗口（`Window.SetMenu`），同一模板成为主窗口菜单栏；`RoleAppMenu` 由 mygo 整菜单隐藏，Settings…/Quit 落到 File。
- **accel 与窗口快捷键竞争**：原生菜单 accelerator 通常先于窗口快捷键被处理；因此 `HandleMenuAction` 对每个带 accelerator 的命令执行与窗口快捷键相同的操作（Search 在会话内的「聚焦 composer」通过 `focusComposerOnFrame` 在下一帧完成，因为 composer 的 focus handle 只在渲染时绑定）。
- **菜单栏键盘可达性**（Alt/F10、方向键）由 mygo 平台后端提供，不需要应用代码。
- 编辑快捷键用 `RoleEditMenu` 而不是自建 Copy/Paste：保证原生文本输入框、IME 与系统剪贴板行为一致。

## 5. 为什么不用 mygo 的 `WindowOptions.StateKey` 默认文件

mygo 默认把窗口状态写到其 `PathUserData` 下的 `window-state.json`。ZeoKeel 的数据目录由 `DataDir()`（含 `ZEOKEEL_DATA_DIR` 便携安装覆盖）统一管理，session/store/配置都在那里；把窗口状态放进同一个目录，备份/迁移/便携安装只需搬一个目录，这也是 mygo 文档为该场景提供的 `WindowStateStore` 扩展点。

## 6. 已知限制与后续

1. **菜单标签不随应用内语言切换重建**：菜单在启动时按当时的 UI 语言构建（如为中文则显示中文）。应用内切换语言后菜单仍是旧语言，需要 shell 暴露语言变更 hook 后重建菜单（P1）。
2. **Settings 仍是应用内 overlay**：本批只把它接入菜单/快捷键；独立设置窗口是 P1。
3. **菜单项范围**：本批未加 Help 菜单、About 对话框（About 由 macOS role 提供）与「打开数据目录」等命令；后续按桌面惯例补充。
4. **系统通知、文本选择/原生右键菜单**是 P0 剩余项，由后续任务落地。
5. 菜单/窗口行为跨平台真机验证（macOS Dock 菜单、Windows Alt 菜单栏、Linux Wayland 位置恢复）需要在有显示器环境补测；无显示器单测覆盖的是命令分发与状态存储逻辑。
