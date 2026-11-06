# ADR 0009：平台感知的 shell 工具栈 + 运行环境提示段

- 日期：2026-11-05
- 状态：**已采纳**（功能对齐 DSH；取代「所有平台同时暴露 bash 与 pwsh」的旧行为）
- 决策者：用户（2026-11-05：「agent 没办法知道自己是什么操作系统，在 win 上执行 Linux 命令。你看看 dsh 是怎么解决这个问题的」→ 按 DSH 方案修复）；记录：Lead / dsh-reference
- 关联：[ADR 0001 退役 DSH 像素对齐](0001-retire-dsh-pixel-parity.md)（功能对齐继续有效）、DSH 参考包（`dsh-base` / `dsh-system-prompt` / `dsh-tool-bash` / `dsh-tool-pwsh`，已安装于 `/usr/local/lib/node_modules/@deepseek-ai/dsh/`）、[docs/prompts.md](../prompts.md)、[docs/design/known-limitations.md §3](../design/known-limitations.md)
- 实施：`internal/tools/config.go`（`Config.AllowBash`）、`internal/tools/{bash,pwsh,terminal,terminal_other}.go`（平台方言与描述）、`internal/app/services.go`（`shellStackFor(goos)`、按平台注册、`Platform` 注入 prompt）、`internal/prompt/{prompt,registry}.go`（`Context.Platform`、`runtime:environment` 105、`tool:pwsh` 1010、权威事实清单）

## 1. 背景

用户在实际 Windows 构建上看到 agent 执行 Linux 命令（如 `ls -la`、`rm`、`cat`）。审计发现：

- ZeoKeel 在**所有平台**同时注册 `bash` 与 `pwsh` 两个 shell 工具；
- `bash` 工具描述写死 `The command runs as "sh -c <command>"`，但 Windows 实现实际走 `cmd /C`（`internal/tools/bash_other.go`）；
- 系统提示词（`internal/prompt`，41 段）里**没有任何操作系统/平台事实**，只有 tool:bash 段（描述退出码）→ 模型只能按工具名与描述猜方言，于是按 Linux 写命令。

对 DSH 0.2.1-rc.2 的审计结论（报告：`.cache/team/report-dsh-platform.md`）：

1. **DSH 不写「OS=Windows」这种系统提示段**：`dsh-system-prompt` 的 `SECTION_ORDERS` / `CONTEXT_ORDERS` 均无 OS/arch/shell；所有运行时上下文贡献方（cwd/沙箱/审批/subagent/team/harness 源码）都不含 OS。
2. **DSH 把平台事实做进组合层**：`dsh-base` 按 `process.platform` 门控 shell 栈——`bash-sandbox`/`tool-bash` 在 win32 禁用，`pwsh-sandbox`/`tool-pwsh` 仅在 win32 挂载，**每台机器恰好一套 shell 栈**（`dsh-base/README.zh.md`「Shell tools per platform」）。
3. **工具描述即方言说明**：bash 描述写 `bash -c`；pwsh 描述写 `pwsh -Command`、原生 `C:\...` 路径、`$env:NAME`、Windows 强制终止的 `[exit code: 1]` 语义。
4. **per-tool 提示段随工具互斥**：`tool:bash`（1000）与 `tool:pwsh`（1010），工具不挂载就不出现对应段。
5. 辅助线索：工作目录上下文是 JSON 引号内的原生路径（`"C:\\..."` vs `"/..."`）。

## 2. 决策

### 2.1 平台门控 shell 栈（P0，对齐 DSH）

- **Windows 构建**：只注册 `pwsh` 工具（PowerShell）；**不注册 `bash`**。
- **POSIX 构建（linux/darwin/…）**：只注册 `bash` 工具（`sh -c`）；**不注册 `pwsh`**。
- 实现方式：`internal/app/services.go` 新增可注入纯函数 `shellStackFor(goos)`（`windows` → `(allowBash=false, allowPwsh=true)`，其它 → 反之）；`tools.Config` 新增显式 `AllowBash`（不复用 `AllowShell`，因为后者还控制 write/edit）；`Builtins` 仅当 `AllowShell && AllowBash && mutatingFiles` 时注册 bash；pwsh 仅在 `allowPwsh` 时注册。
- 结果：模型可见工具目录本身就是平台事实；配合 tool 段互斥，模型不会拿到「bash + sh -c」这个错误组合。POSIX 不再暴露 pwsh 工具（行为变化，对齐 DSH 默认组合；将来需要时可用自定义组合扩展）。

### 2.2 显式运行环境提示段（P1，ZeoKeel 增强）

DSH 靠工具目录隐式表达平台；ZeoKeel 的工具描述相对更简，因此额外增加一个**显式运行环境段**兜底：

- `prompt.Context` 新增 `Platform string`（如 `windows/amd64`；空值回退编译进程的 `runtime.GOOS+"/"+runtime.GOARCH`），由 `internal/app/services.go` 注入。
- 新段 `runtime:environment`，Order 105（WorkingDirectory 100 与 SandboxPolicy 110 之间），始终包含，内容：`<GOOS>/<GOARCH>` + 当前可见 shell 工具/调用方式（`sh -c` 或 `pwsh -Command`）+ 语法/路径风格/环境变量风格 +「不要假定另一种 shell」；无 shell 工具时明确说明不要执行 shell 命令。中英双语。
- `harness:note` 的「权威事实」清单加入 runtime environment，使该段优先于模型猜测。

### 2.3 工具描述/错误按平台方言化（P2）

- `bash` 描述改为按 GOOS 动态：POSIX 才承诺 `sh -c`；Windows 如实写 `cmd.exe /C` 并提示标准 Windows 构建用 `pwsh`（不注册 bash）。**不再无条件承诺 `sh -c`**。
- `pwsh` 描述补 Windows 事实：`pwsh -Command`、原生 Windows 路径、`$env:NAME`、Windows 强制终止以 `[exit code: 1]` 结算且无信号标记；缺 PowerShell 的错误提示按平台（Windows 不再引导 bash）。
- `terminal_open` 描述按平台（Windows：无 PTY/pipe、PATH 有 pwsh 优先、否则 `%COMSPEC%`/cmd.exe）；Windows 终端默认 shell 同步优先探测 pwsh，使「一次性 shell / 持久终端 / 提示词」三者口径一致。

## 3. 平台矩阵（修复后）

| 维度 | Windows | POSIX（linux/darwin） |
| --- | --- | --- |
| 模型可见 shell 工具 | `pwsh` 仅此一个 | `bash` 仅此一个 |
| 执行方式 | `pwsh -Command`（`pwsh`→`powershell` 解析） | `sh -c` |
| 工具提示段 | `tool:pwsh`（1010）；无 `tool:bash` | `tool:bash`（1000）；无 `tool:pwsh` |
| 运行环境段 | `Runtime environment: windows/<arch> … pwsh … C:\… $env:NAME` | `Runtime environment: <os>/<arch> … bash … /… $NAME` |
| 持久终端 | pty=false、stdio pipes、pwsh 优先/COMSPEC 兜底 | PTY（可回退 pipe）、login `sh -l` |
| 缺失 shell | 提示安装 PowerShell，不提 bash | bash 始终存在（POSIX 标准） |

## 4. 后果与取舍

- **正面**：模型从工具目录 + 运行环境段双重获知平台与方言，Windows 上不会再按 bash 描述写 Linux 命令；提示词与实际工具集不再矛盾。
- **行为变化**：POSIX 构建不再暴露 `pwsh` 工具（此前所有平台同时有 bash+pwsh）；Windows 构建不再暴露 `bash` 工具。这是对齐 DSH「每台机器一套 shell 栈」的有意变化，记入 known-limitations。
- **未做 / 后续**：Windows 真机运行期验证（本环境仅 `GOOS=windows go build/vet` + 纯函数/描述单测）；Windows 的 Git Bash/WSL 集成（如需要可在未来用自定义 shell 栈/profile 扩展）；`terminal_signal` 描述仍为 POSIX 措辞（低风险）。
- **文档**：`docs/prompts.md` 段目录同步（`runtime:environment`、`tool:pwsh`、`Context.Platform`）；known-limitations 记录平台矩阵与真机验证项。

## 5. 验证

- `gofmt` / `go vet ./...` / `go test ./... -count=1` 全绿（`runtime:environment` 与 `tool:pwsh` 段、平台矩阵、工具描述、`AllowBash`、`shellStackFor` 两分支均有测试）。
- `GOOS=windows GOARCH=amd64 go build ./...` 与 `go vet ./...` 通过；`scripts/check-nocgo.sh` 四目标交叉编译通过。
- Linux 真实 registry 断言：`bash` 存在、`pwsh` 不存在；Windows 分支（goos 注入测试）反之。
