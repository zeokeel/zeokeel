# ADR 0007：移除会话头「计划模式」开关（入口收敛）

> **2026-11-05 更新**：ADR 0007 的「移除 header 复选框」继续有效；「plan 模式入口收敛到隐藏/不可达」的处置已被 [ADR 0008](0008-plan-mode-soft-alignment.md) 取代——plan 模式改为 DSH 式软引导流程，入口以 `/plan` 命令 + composer 计划徽章恢复（不再回到 header）。

- 日期：2026-11-05
- 状态：**部分取代**（header 复选框移除有效；入口处置由 ADR 0008 取代）
- 决策者：用户（2026-11-05 原话「计划模式其实就是下面框里的只读。所以这个复选框可以关闭」）；记录：Lead / plan-toggle-removal
- 关联：[ADR 0003 桌面壳集成](0003-desktop-shell-integration.md)（header 布局历史）、[docs/design/native-ui.md §4.1](../design/native-ui.md)、[docs/design/known-limitations.md §3](../design/known-limitations.md)
- 实施：`internal/shell/session_live.go`（删除 `planHeaderToggle` 调用）、`internal/shell/plan_ui.go`（删除 `planHeaderToggle` 与无调用方的 `togglePlanMode`）、`internal/shell/plan_ui_test.go`（改用 controller 开启 plan mode）、`docs/design/preview/session-native-{light,dark}.png`（重生成）

## 1. 背景

会话头（标题行）曾渲染一个「□ 计划模式 关/开」复选框（`RoleSwitch`），用于翻转当前会话持久化的 plan mode。用户在实际使用中认为它与 composer 下方权限下拉里的「只读」重复：

- plan mode 的运行时效果 = 只允许读取/规划类工具，并让 agent 用 `present` 提交可审阅的 Plan；
- composer 的「只读」权限（`PermissionAsk/Read-only`）同样不允许修改数据，用户认为两者是同一件事的两种表达。

因此要求移除 header 里这个复选框。

## 2. 决策

1. **删除 header 的计划模式开关 UI**：`session_live.go` 不再调用 `planHeaderToggle`；`plan_ui.go` 删除该渲染函数，以及唯一调用方 `togglePlanMode`。
2. **保留 plan mode 的功能本体**：`session.Controller` 的持久化 flag、runtime 的只读计划策略、计划审阅卡（`planReviewCard` / `approvePlan` / `exitPlanMode`）全部保留。已持久化 `plan_mode=true` 的会话仍会走只读计划策略与审阅卡；新会话默认 `false`。
3. **不把「只读」权限与 plan mode 联动**（本轮不做）：两者仍是独立机制，permission 走审批/工具白名单，plan mode 走计划审阅 gate。联动或彻底删除属于后续产品决策。
4. **测试/预览同步**：`TestPlanModePresentPlanCardShown` 改为直接用 controller 设置 plan mode；新增「Plan mode 文本不再出现」断言；会话预览重生成（header 只剩标题、模式 chip、相位与动作）。

## 3. 后果与风险

- **正面**：header 减少一个与权限下拉重复的控件；交互模型收敛到一处（composer 权限）。
- **风险/遗留**：
  - plan mode 当前**没有 UI 入口**——只能由已持久化状态、或直接调用 controller（测试/脚本）触发；计划审阅卡因此对普通用户不可达。
  - 若产品确认不再需要 plan mode，应另立任务删除 controller/runtime/prompt 的 plan mode 分支、`SessionPlanModeKey` 持久化、审阅卡与相关测试；若确认需要，则应把「只读」权限与 plan mode 联动（或在 composer 权限菜单里新增「计划」档），再恢复一个入口。
  - 已存 `plan_mode=true` 的旧会话：仍显示审阅卡（可 Approve/Exit），Exit 后不能再开启。
- **文档**：`docs/design/known-limitations.md` §3 记录「无 UI 入口」；`docs/design/native-ui.md` §4.1/§14 更新旧描述。

## 4. 验证

- `gofmt -l internal/shell` 空；`go vet ./...` 通过；`go test ./internal/shell -count=1` 全绿（含 plan 测试改造）。
- `docs/design/preview/session-native-{light,dark}.png` 重生成并人工核对：header 不再出现「□ 计划模式 关」。
