# 审批表面（APPROVAL-SURFACES）

> **活文档**。原生 UI 的 12 个 surface 规格已随 `S5: retire the native UI` 退役
> （归档见 [archive/AGENT-SURFACES.md](archive/AGENT-SURFACES.md)），但"哪些工具会
> 停审批"是**浏览器客户端与托盘壳都还在依赖的行为契约**，因此单独成文。
>
> 协议侧：`approvals.snapshot` / `approvals.delta` / `approval.resolve`
> （[TRANSPORT](TRANSPORT.md) §5.1.3）；客户端渲染见 [WEB-CLIENT](WEB-CLIENT.md)。

#### S4.1 哪些工具会停审批（批准卡的真实触发面）

> 结论先行：**审批卡的出现频率远低于直觉**。`serve`/`desktop` 装配里 sandbox 模块在场时，命令执行家族被 `gate.AllowSandboxedCommands` 判定"已被 OS 沙箱约束"而**自动放行**——用户点了"发送"却看不到卡，这不是审批坏了。判断顺序（`internal/modules/agent/plan_mode.go:253-360`）：

| 优先级 | 层 | 命中即 | 条件 | 来源 |
|---|---|---|---|---|
| 1 | plan / minimal / creator 模式的工具白名单 | allow 或 **deny**（不给审批机会） | 模式开启时 | `plan_mode.go` 门 `agentModePlan()` / `chatMode()` |
| 2 | 会话级 permission 覆盖 | full=全部 allow；read-only/workspace 走各自的自动放行集 | 会话设置了 `session.permission.set` | `contracts.ReadOnlyAutoAllowedTools` / `WorkspaceAutoAllowedTools` |
| 3 | **OS 沙箱豁免** | allow（**不弹卡**） | sandbox 模块 active + runner 报告真实约束 + policy ≠ danger-full-access | `internal/modules/agent/sandbox.go:26-51` → `gate.AllowSandboxedCommands` |
| 4 | 交互工具 | allow（它们自己的 overlay 才是交互） | 见下方 interaction 清单 | `plan_mode.go` `interactionTools` |
| 5 | 记住的「本会话允许」 | allow | 用户曾点过"始终允许" | `session.allowedTools` |
| 6 | 以上都没命中 | **弹审批卡**（`approvals.Request` 阻塞） | 默认 workspace 模式 | `internal/modules/approvals` |

**机器可读清单**（测试 `internal/modules/agent/approval_surface_test.go` 逐条对照代码，改规则不改文档会红）：

<!-- approval-exemptions:begin -->
sandbox-shell: bash, pwsh
workspace-auto: apply_patch, edit_file, glob, grep, inspect, job_list, job_output, job_wait, list_dir, lsp_definition, lsp_diagnostics, lsp_hover, lsp_implementation, lsp_references, lsp_symbols, memory, read_file, read_image, sandbox_status, search, session_event_read, session_event_search, session_event_trace, session_search, session_trace, skill, spill_read, str_replace_editor, team_task_get, team_task_list, terminal_list, terminal_read, todo_write, tool_search, undo, working_directory, write_file
interaction: ask_user, present, plan_review, exit_plan_mode, get_goal, create_goal, update_goal
parks: web_fetch, web_search, settings_set, mcp__
<!-- approval-exemptions:end -->

- `sandbox-shell` 是**会免审批的命令执行面**（`sandboxShellTools`）；沙箱不可用（无 runner / policy 为 danger-full-access）时它退回第 6 层，照常弹卡。
- `workspace-auto` 是 workspace 模式自动放行集（`contracts.WorkspaceAutoAllowedTools`，含只读子集与 `memory` 等应用状态工具）。
- `interaction` 不弹审批，因为工具本身就是一次用户交互（DTO 形状见 S5/S6）。
- `parks` 是**保证会停审批**的家族：网络工具、设置写入、以及所有 MCP 工具（`mcp__*`）——浏览器验收里的"审批停住 → 允许 → 流式完成"就该用它们。

#### S4.2 MCP 工具注解（destructiveHint / readOnlyHint）与审批

> 结论先行：**"危险工具必须停审批"今天已经按构造成立，不需要读注解**。`mcp__` 是 `parks` 里的**前缀家族**：任何 MCP 工具（`tools/list` 工具，以及管理器的资源/提示助手工具）都不在 `contracts.WorkspaceAutoAllowedTools` 里，也不在沙箱命令家族与交互工具里，所以在默认 workspace 模式下必然走到上一节第 6 层——弹审批卡。不是"看到 destructive 注解才停"，而是"全都停"；因此 M2.5 批次 A 的验收条件"destructive 注解的工具一定停审批"目前是**平凡成立**的。

- **注解尚未被读取**：`tools/list` 返回的 `annotations.readOnlyHint` / `destructiveHint` / `idempotentHint` 目前不参与任何判定。`internal/modules/toolkit/mcp_control.go` 只把工具名、描述与参数 schema 交给注册表，审批侧只按名字家族判定（见上一节 `parks` 行）；工具卡片上也不会出现"只读/破坏性"标记。
- **如果将来读取它们，会改变什么**：`destructiveHint: true` 不会改变行为（本来就弹卡）；真正会变的是 `readOnlyHint: true`——一条"自称只读"的工具可以被加入自动放行集，**跳过审批卡**，让高频只读工具不再打断会话。**未实现，且本次刻意不实现**：注解是服务端自述、可撒谎且缺省语义含糊（未声明 ≠ 只读，也 ≠ 破坏性），用它来"减审批"等于把安全面从"默认停卡"换成"第三方自我声明"的正确性。要做也得先有服务端信任等级、注解缺省策略，以及 UI 上明示"该工具自称只读，未经核实"，否则不划算。
- 相关条目：`docs/plans/grokbot-open-source-plan.md` §7.5 G14 #1（注解→审批）、§8 M2.5 批次 A。同批次的"单 server 超时不拖住整轮"已实现且同样**不触碰审批面**：超时只让一次调用以 `*MCPCallTimeoutError` 失败（`internal/modules/toolkit/mcp_call_timeout.go`），不改变哪些工具会停审批。
