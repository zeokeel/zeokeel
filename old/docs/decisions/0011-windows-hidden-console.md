# ADR 0011：Windows 子进程隐藏控制台（不再弹 cmd 窗口）

- 日期：2026-11-05
- 状态：**已采纳**
- 决策者：用户（2026-11-05 反馈：「win 电脑执行命令时候不断在弹出 cmd 的对话框。你看看 dsh 是怎么解决的」）；实施：Lead
- 关联：[ADR 0009 平台 shell 栈](0009-platform-shell-stacks.md)、`internal/winproc`、`internal/tools/{bash,pwsh,terminal,jobs,worktree}.go`、`internal/mcp/mcp.go`、`internal/shell/start_tab.go`

## 1. 背景

ZeoKeel 的 Windows GUI 包用 `-H windowsgui` 链接（无控制台子系统）。Windows 上，GUI 进程用 `CreateProcess` 启动**控制台程序**（`cmd.exe`、`pwsh.exe`、`git`、MCP server 等）时，若没有传 `CREATE_NO_WINDOW`，系统会为子进程分配一个新控制台并**显示窗口**。于是：

- 每次 `bash`/`pwsh` 工具调用、`job_start`、持久终端、`git worktree`、MCP server 连接、UI 终端里跑命令，都会闪出/常驻一个 cmd 窗口；
- 输出本来通过 pipe 回到工具结果，窗口纯属噪声，反复弹窗还会抢焦点。

DSH 的解法（0.2.1-rc.2 审计）：

- DSH 是 Node/Electron 进程，所有子进程通过 Node `child_process` 的 **`windowsHide: true`** 启动：
  - `dsh-subprocess-local/lib/runner-launch-B2zsQ1Dz.js:166,733`、`dsh-subprocess-local/lib/index.js:781`；
  - `dsh-native-command/lib/types/runner.js:16` / `lib/index.js:24` 显式把 `windowsHide: window === "hidden"` 作为「Windows 启动可见性」参数（后台命令 hidden、GUI 启动器 visible）；
  - `dsh-sandbox-windows-acl/lib/types-Cl_DXjhk.js` 注释提到 `CREATE_NO_WINDOW` / `CREATE_NEW_CONSOLE` 子进程随 Job 终止。
- 即：**由启动方显式声明子进程控制台不可见**，而不是依赖终端或用户设置。

## 2. 决策

新增 `internal/winproc`：

- `winproc.Hide(cmd *exec.Cmd)`：Windows 上设置 `SysProcAttr.HideWindow = true` 且 `CreationFlags |= CREATE_NO_WINDOW (0x08000000)`（保留已有 flags）；非 Windows 为 no-op，调用方无需 build tag。
- 两个 flag 都设置的理由：`CREATE_NO_WINDOW` 让子进程根本不分配控制台（GUI 父进程无控制台可继承）；`HideWindow` 覆盖忽略 `CREATE_NO_WINDOW` 的启动器。
- `hide.go`（`!windows` no-op）与 `hide_windows.go`（`windows`）分文件；`hide_windows_test.go` 固定 flag，`hide_test.go` 固定「任意平台调用安全、命令仍可执行」。

在**所有**子进程启动点调用：

| 位置 | 覆盖 |
| --- | --- |
| `internal/tools/bash_other.go` 的 `configureProcess` | bash 工具、jobs（均经 `configureProcess`；Windows 分支 cmd /C） |
| `internal/tools/pwsh.go`（经 `configureProcess`） | pwsh 工具 |
| `internal/tools/terminal.go` 的 `newCmd` | 持久终端（Windows pipe fallback） |
| `internal/tools/worktree.go` | `git` worktree 命令 |
| `internal/mcp/mcp.go` | MCP stdio server 进程 |
| `internal/shell/start_tab.go` | UI 终端面板里 Run 的 shell 命令 |

POSIX 路径不设置任何 Windows 字段（`winproc.Hide` no-op），PTY 的 `Setsid/Setctty` 逻辑不变。

## 3. 后果与验证

- 正面：GUI 包中的所有工具/终端/MCP 子进程不再弹控制台窗口；工具输出仍通过既有 pipe 读取，行为不变。
- 未做/限制：高 DPI 下已隐藏窗口不会再出现；如果用户显式打开「外部终端」类功能，那属于另一条路径。Windows 真机验证需要用户确认（本环境仅 `GOOS=windows` 交叉编译 + vet + Windows-only 单测编译）。
- 验证：`gofmt`、`go vet ./...`、`go test ./internal/winproc ./internal/tools ./internal/mcp ./internal/shell`；`GOOS=windows GOARCH=amd64 go build ./...` 与 `go vet ./...` 通过；`scripts/check-nocgo.sh` 四目标 PASS。
