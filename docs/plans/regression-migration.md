# internal/regression 迁出对照表（S4 前置，task-11）

> S4 会整包删除 `internal/regression`（单个 Go 包：helper 里 import uiroot）。本文记录**哪些覆盖被保住、保住到哪、哪些随 UI 一起失去**。
> 分类口径：`(a)` 只测后端服务/协议；`(c)` 含 UI 断言（帧/文本/测试器）；`(b)` 纯 UI 器材（无断言，供他人使用）。

## 汇总

| 分类 | 文件数 | 行数 | 处置 |
|---|---|---|---|
| (a) 后端 | 34 | 9058 | 迁出：四条必需行为已迁（见下表），其余按需随包删除 |
| (c) 混合 | 35 | 7657 | 拆出后端断言；UI 断言随 S4 退役 |
| (b) 纯 UI 器材 | 2 | 622 | 不迁，随 S4 删除 |
| **合计** | 71 | 17337 | |

## 已迁出（新增版本，旧文件保留至 S4 统一删除）

| 原文件（行数） | 新位置 | 保住的行为 |
|---|---|---|
| `acp_session_test.go` (256) | `internal/gateway/e2e_acp_test.go` | ACP stdio 一轮（initialize/session-new/session-prompt/update/stopReason）+ 错误帧 |
| `agent_harness_test.go` (377) | `internal/gateway/e2e_agent_test.go` | agent 运行生命周期（流式→done、run.cancel、断连≠停止、usage） |
| `usage_test.go` (330) | `internal/gateway/e2e_agent_test.go` | usage 事件到达 SSE |
| `approvals_test.go` (314) | `internal/gateway/e2e_approval_test.go` | 审批 resolve 语义（未知 id/非法决策/幂等） |
| `permission_test.go` (307) | `internal/gateway/e2e_approval_test.go + docs/AGENT-SURFACES.md §S4.1` | 审批与自动放行面（含沙箱豁免清单断言） |
| `approval_geometry_e2e_test.go` (198) | `internal/gateway/e2e_approval_test.go` | 审批链路（原为 UI 驱动，现走协议） |
| `automation_history_test.go` (330) | `internal/gateway/e2e_automation_test.go` | automation 完整流程（create→RunNow→session→history） |
| `automation_model_test.go` (176) | `internal/gateway/e2e_automation_test.go` | 任务级 model override 生效 |
| `plan_mode_test.go` (394) | `internal/gateway/e2e_plan_queue_test.go` | plan 模式：plan_review 放行 → `plan.delta`/`plan.snapshot` 内容可见 → `plan.exit approve` → 状态 approved；fail-closed：写文件类工具被门拒绝、且不弹审批 |
| `queue_send_test.go` (445) | `internal/gateway/e2e_plan_queue_test.go` | 队列：运行中 enqueue → 后端在 run 结束后自行发出 → inbox 清空、两条用户消息；运行中 remove → 永不发出 |

新增测试（`internal/gateway/e2e_*_test.go`，全部通过）：

`e2e_harness_test.go`（器材）、`e2e_agent_test.go`（4 条：lifecycle / cancel / disconnect / usage）、
`e2e_approval_test.go`（3 条：allow→tool.result / deny→tool 不执行 / 决策词与幂等）、
`e2e_automation_test.go`（2 条：完整流程 / model override）、`e2e_acp_test.go`（2 条：一轮会话 / 错误帧）、
`e2e_plan_queue_test.go`（4 条：plan 审阅一轮 / plan 模式拒绝写操作 / 队列排空 / 队列移除不再发出）。合计 **15 条** e2e。

## 仍将失去的覆盖（随 UI 一起删除，S4 后不可恢复）

| 文件 | 行数 | 失去什么 |
|---|---|---|
| `approval_geometry_e2e_test.go` | 198 | 几何/布局断言（链路已迁） |
| `automation_filter_test.go` | 141 | automation 过滤 UI |
| `behavior_harness_test.go` | 62 | 行为器材（UI） |
| `builtin_modules_pages_test.go` | 120 | 内置模块页 |
| `commands_i18n_test.go` | 133 | 命令文案 i18n |
| `compact_now_test.go` | 305 | 压缩交互 |
| `context_budget_test.go` | 103 | 上下文预算 UI |
| `context_test.go` | 247 | 上下文面板 |
| `data_privacy_test.go` | 286 | 隐私面板 |
| `denied_globs_test.go` | 99 | denied globs UI |
| `fake_goal_test.go` | 289 | goal UI |
| `fixverify_snapshot_test.go` | 85 | 快照差分 |
| `font_size_test.go` | 144 | 字号档位 |
| `goal_v2_test.go` | 179 | goal v2 UI |
| `helpers_test.go` | 854 | uiroot 器材 |
| `home_composer_test.go` | 189 | 首页 composer |
| `interaction_bridge_test.go` | 422 | UI 桥接器材 |
| `interaction_ui_test.go` | 244 | 交互浮层 |
| `interrupted_test.go` | 115 | UI 断言 |
| `landing_primary_e2e_test.go` | 78 | 首屏主操作 |
| `model_check_test.go` | 232 | 模型检查弹层 |
| `notifications_test.go` | 128 | 通知 UI |
| `notifier_test.go` | 200 | 通知器 UI 器材 |
| `plan_keep_planning_test.go` | 317 | plan 模式对话框交互 |
| `plugin_params_lsp_test.go` | 251 | 插件参数 UI |
| `provider_presets_test.go` | 242 | provider 预设表单 |
| ~~`queue_send_test.go`~~ | 445 | 已迁（队列后端断言），仅 UI 断言随 S4 退役 |
| `reasoning_preview_test.go` | 123 | 推理折叠预览 |
| `session_commands_test.go` | 264 | 命令面板与 slash 交互 |
| `session_export_notify_test.go` | 165 | UI 断言 |
| `session_ui_test.go` | 435 | 会话屏渲染/虚拟化/滚动跟随 |
| `sidebar_view_test.go` | 159 | 侧栏分组/选中态 |
| `toast_levels_test.go` | 104 | toast 语义色 |
| `toasts_test.go` | 342 | toast 队列与层级 |
| `window_esc_e2e_test.go` | 314 | Esc 分层（窗口级） |
| `window_shortcuts_test.go` | 136 | 快捷键 |
| `workspace_double_test.go` | 129 | 双工作区 UI |
