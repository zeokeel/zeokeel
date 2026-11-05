# ADR 0010：审批只用于会逃逸沙箱的操作 + 只读调用豁免

- 日期：2026-11-05
- 状态：**已采纳**（对齐 DSH 的 approval 语义；取代「按工具名全量审批」的旧默认）
- 决策者：用户（2026-11-05 反馈：`str_replace_editor {"command":"view","path":"."}` 只读查看也要审批，「需要审批的点是不是有点多」）；实施：plan-soft-runtime / Lead
- 关联：[ADR 0008 计划模式软引导](0008-plan-mode-soft-alignment.md)、[ADR 0009 平台 shell 栈](0009-platform-shell-stacks.md)、DSH 参考包（`dsh-user-approval`、`dsh-tool-fs`、`dsh-bash-sandbox`，安装于 `/usr/local/lib/node_modules/@deepseek-ai/dsh/`）、实施报告 `.cache/team/report-approval-scope.md`

## 1. 背景

ZeoKeel 的 Workspace Write 权限用「工具名 pattern 列表」决定哪些调用需要人工审批（`internal/app/services.go` 的 `defaultApprovalTools` + `approval.tools` setting），列表里包含 `write_file/edit_file/str_replace_editor/bash/pwsh/terminal_*/...`。问题：

1. 一个**双用途**工具被整体审批：`str_replace_editor` 的 `view`（只读查看，甚至 `path:"."` 列目录）也会弹审批；用户截图即为该场景。
2. 文件写工具本来就被 workspace 沙箱**限定在 root 内**（`internal/tools/path.go` 的 `sandbox.resolve`：绝对路径/`..`/符号链接逃逸都被拒绝），在 Workspace Write 下再逐次审批属于重复门槛。
3. 每次文件编辑/命令轮询都打断工作流，「始终允许」还会把整个工具（含后续危险调用）放行，粒度不对。

DSH 的语义（0.2.1-rc.2 审计）：

- `dsh-user-approval` 的 `ask` 策略把敏感操作的**一次性决定**交给人类/机器应答者；工具通过 `ctx.approval` 主动请求决定。
- 文件工具只在**请求比 standing mode 更宽的沙箱权限**时走审批：`dsh-tool-fs#resolvePolicy` 注释「重复 standing mode 无需审批」，`approveEscalation` 只处理 `sandbox_permissions` 升权（`dsh-fs-sandbox` README：拒绝返回 `FS_SANDBOX_DENIED` + 同轮升级提示）。
- bash 工具同样只在沙箱升权（`sandbox_permissions`）前请求审批（`dsh-tool-bash` README）。
- 即：**审批 = 沙箱升级/越界**，不是「每次变更」；工作区内的读写在沙箱边界内直接执行。

## 2. 决策

### 2.1 默认审批集合收敛为「会逃逸工作区」的工具

`defaultApprovalTools`（Workspace Write 默认）改为：

```
bash,pwsh,terminal_open,terminal_send,terminal_signal,terminal_close,
job_start,job_kill,enter_worktree,exit_worktree,web_search,web_fetch,mcp__*
```

移除 `write_file/edit_file/str_replace_editor`（workspace 沙箱已限定 root）与 `ptc`（PTC 的内层工具调用已逐个经过同一 gate，外层再审是重复）。`terminal_read`/`job_output`/`job_list` 等只读轮询本就不在列表。

### 2.2 只读调用豁免（双用途工具）

新增参数感知 helper `toolNeedsApprovalWithArgs(name, args, patterns)`：

- `str_replace_editor` 且 `args.command == "view"` → **永不审批**（即使 `approval.tools` 配置了 `str_replace_editor` 或 `*`）；非法/缺失 JSON 回落到按 name 判定（fail-closed）。
- 其它工具仍按 name/prefix pattern。

### 2.3 Auto review EXP 保留原有更宽的复核范围

Auto review 的 reviewer 只在 gate 要求审批时介入（`internal/runtime/model_adapter.go` 的 `awaitApproval`），若沿用收敛后的列表会漏掉文件写复核。因此：

- `approvalPatternsForPermission(st, permission)`：显式 `approval.tools` 设置优先（两种模式都生效）；否则 AutoReview → `autoReviewApprovalTools`（原宽集，含 write_file/edit_file/str_replace_editor/ptc），Workspace/Ask → 收敛后的默认集。
- Full access 仍完全 bypass；Read Only 仍然只注册只读工具（不经过 gate）。

## 3. 后果与风险

- 正面：审批点只剩「shell/进程/网络/MCP/worktree/job」这类确实能影响工作区之外的调用；只读查看永不打断；「始终允许」的粒度问题也随噪声下降而缓解。
- 风险：默认下模型可在**无逐次审批**的情况下修改/删除 workspace root 内的文件——边界仍由 path/editor sandbox 保证（root 外被拒绝）。需要逐次审批的用户可把 `approval.tools` 设回旧列表或 `*`（`*` 下 `str_replace_editor view` 仍豁免，这是有意的只读保证）。
- 既有 `approval.tools` 配置不迁移：空/缺失/不可读时按新默认；已显式配置的保持生效。
- 测试覆盖：`internal/app/approval_scope_test.go`（17 个 helper 子用例 + Workspace vs AutoReview 差异/覆盖语义），`fixes_test.go`/`auto_review_test.go` 同步。

## 4. 验证

- `gofmt` / `go vet ./...` / `go test ./internal/app ./internal/runtime` 全绿；全仓 `go test ./... -count=1` 绿。
- 手工对照：Workspace Write 下 `str_replace_editor view` 不弹审批、`create/str_replace` 不弹（沙箱内）；`bash`/`pwsh`/terminal 仍弹；Auto review 下文件写仍由模型复核。
- 真机 Windows 行为待用户验证（本环境 headless）。
