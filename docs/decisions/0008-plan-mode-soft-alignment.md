# ADR 0008：计划模式对齐 DSH（软引导 + `/plan` 命令 + composer 徽章；移除 runtime 硬门）

- 日期：2026-11-05
- 状态：**已采纳**（取代 [ADR 0007](0007-remove-plan-mode-toggle.md) 的「入口收敛仅隐藏」处置；ADR 0007 的「移除 header 复选框」部分继续有效）
- 决策者：用户（2026-11-05 在 DSH 调查后选择方案 A）；记录：Lead / plan-soft-runtime / plan-entry
- 关联：[ADR 0007 移除计划模式开关](0007-remove-plan-mode-toggle.md)、DSH 参考（已安装包）：`/usr/local/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai/dsh-plan-mode/README.zh.md`、`.../dsh-client-ui-plan/README.zh.md`、`.../dsh-permission-presets/README.zh.md`、[docs/design/native-ui.md §14](../design/native-ui.md)、[docs/design/known-limitations.md §3](../design/known-limitations.md)、[docs/PLAN.md](../PLAN.md)
- 实施：`internal/runtime/{plan_mode.go,plan_mode_test.go}`（删除）、`internal/runtime/{agent,model_adapter,ptc,subagent,modes}.go`（去掉 planMode 接线）、`internal/app/services.go`（不再向 runtime 传 PlanModeFunc）、`internal/shell/composer_menus.go`（`/plan` 命令、`/help`）、`internal/shell/session_live.go`（composer 徽章）、`internal/shell/plan_ui.go`（徽章渲染/退出）

## 1. 背景

上一轮按用户要求移除了会话头的「计划模式」复选框（ADR 0007），因为它在观感上与 composer 权限下拉里的「只读」重复。之后用户问「DSH 是怎么处理计划模式的？不就是只读么？」。对已安装的 DSH 0.2.1-rc.2 包（`dsh-plan-mode`、`dsh-client-ui-plan`、`dsh-permission-presets`）的审计结论是：

- **DSH 的计划模式是流程，不是权限**：它是一个独立产品包，注册 `plan:policy` 提示词段落与 `exit_plan_mode` 工具；`/plan` 进入、`/plan off` 退出，agent 提交计划后用户在审阅界面 Approve / Keep planning。显式文档写着「它不限制 agent：每个工具仍可调用」「计划模式是引导而非执行沙箱——需要强制只读规划的部署必须组合独立的沙箱与审批策略」。
- **只读是权限/沙箱**：DSH 权限预设把 sandbox 模式（`read-only` / `workspace-write` / `danger-full-access`）与审批策略（`ask` / `never`）捆绑，由 composer 权限控件或 `/permission` 切换；`read-only` 拒绝一切文件变更。两者正交、可叠加。
- **UI 入口**：DSH 没有 header 复选框；plan 激活时 composer 访问模式控件右侧出现蓝色「计划」徽章（点击 = `/plan off`），入口是 `/plan` 命令或 composer `+` → 计划。

ZeoKeel 原实现与此不同：`internal/runtime/plan_mode.go` 在每次工具调用前按名单硬拒绝 `bash`、`write_file`、`edit_file`、`str_replace_editor`、`terminal_*`、`mcp__*`、`web_*`、`job_*`、`worktree`、`schedule_*` 等，只放行只读工具和 `present`。因此 ZeoKeel 的 plan mode 实际等于「强制只读 + 计划审阅」，与 composer 的「只读」权限高度重叠——这正是用户认为重复的原因。

用户拍板选择方案 A：**对齐 DSH**。

## 2. 决策

1. **plan mode 语义 = 软引导流程**：保留 prompt 的 `plan:policy` 段（引导只读调查、用 `present` 提交标题为 "Plan" 的计划、等待批准）；**移除 runtime 的工具硬门**。plan 模式激活时不再有 runtime 层工具拒绝——工具是否可用由权限/沙箱与审批决定。
2. **入口 = `/plan` 命令 + composer 徽章**（与 DSH 同构）：
   - `/plan` 开启当前会话的计划模式；`/plan off` 关闭；`/plan <message>` 开启并把该消息作为下一请求提交；Home 无会话时提示先打开会话。
   - composer 计划徽章只在 plan 模式激活时显示，点击等于 `/plan off`；header 复选框保持移除（ADR 0007）。
3. **审阅保留 ZeoKeel 现有机制**：present 标题为 "Plan" 的 artifact + composer 上方的计划审阅卡（Approve / Exit）。DSH 的 `exit_plan_mode` 独立工具与「Keep planning（带反馈）」本轮不实现，记为后续（见 §4）。
4. **状态与生效**：会话级 `plan_mode` 持久化（`SessionPlanModeKey`）保留，重启/fork 后保留；切换立即写入，下一回合的 prompt 政策生效，不重建 agent。
5. **与只读的关系 = 正交可叠加**：需要「强制只读的规划」时，用户同时选择 composer 的「只读」权限；plan 模式本身不再提供强制限制。

## 3. 后果

- **正面**：plan 模式与权限解耦，概念与 DSH 一致；用户可用「plan 模式 + 只读权限」组合得到强制只读规划，也可只用 plan 模式获得流程约束；composer 徽章提供状态可见性与一键退出；header 不再有重复控件。
- **风险**：plan 模式下 agent 理论上可以直接修改文件（只有 `plan:policy` 文本约束）；依赖强制限制的用户必须选只读权限——在 known-limitations 中写明。
- **兼容**：已持久化 `plan_mode=true` 的旧会话升级后仍处于 plan 模式，可用 composer 徽章退出；prompt 政策与计划审阅卡行为不变。

## 4. 未做 / 后续

> **2026-11-05 更新：以下 1–4 已全部实现**（用户要求把遗留修复；实施报告 `.cache/team/report-plan-parity.md`）：
>
> 1. `exit_plan_mode` 独立工具：`internal/tools/exit_plan_mode.go`，始终注册、按 per-session plan 模式门控、非阻塞提交计划；shell 从 live snapshot 的最新成功调用渲染审阅卡。旧 `present` "Plan" 路径保留兼容。
> 2. 审阅卡 Keep planning（带反馈）：反馈作为下一轮用户消息提交、plan 保持；另有 Exit / Dismiss（Esc 关闭卡片、保持 plan），Approve 仍退出并继续。
> 3. pending 态徽章：交付未决期间 warning 色 + Clock +「待审阅」，a11y Label 更新，点击重开卡片；决定后回 active 或隐藏。
> 4. Home 草稿会话 `/plan`：无 session 时创建草稿会话并开启 plan，`/plan <message>` 作为首条请求；命令面板与 Enter 同路径，失败 toast、单次创建。
>
> 测试：`internal/tools/exit_plan_mode_test.go`、`internal/app/exit_plan_mode_test.go`、`internal/shell/plan_ui_test.go` 新增 8 个场景；全仓测试绿。`internal/prompt/registry.go` 的 `plan:policy` 文本同步引导新工具。

（历史记录，已完成）1. DSH 的独立 `exit_plan_mode` 工具；2. 审阅卡「Keep planning（带反馈）」；3. pending 态徽章；4. Home 草稿会话 `/plan`。

## 5. 验证

- runtime 侧：plan 模式下 mutating 工具不再被拒绝；`go test ./internal/runtime ./internal/app ./internal/prompt ./internal/session` 全绿。
- shell 侧：`/plan`、`/plan off`、`/plan <message>` 与 composer 徽章的开关行为测试；`go test ./internal/shell` 全绿。
- 计划审阅卡（present "Plan" + Approve/Exit）行为不回退；预览/视觉回归保持 PASS；`ZEOKEEL_VISUAL=1` 通过（徽章只在激活时出现，预览 fixture 为未激活态，视觉不变）。
