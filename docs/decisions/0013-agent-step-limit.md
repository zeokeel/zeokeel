# ADR 0013：单轮步骤上限（eino max iterations）与超限优雅收尾

- 日期：2026-11-05
- 状态：**已采纳**
- 决策者：用户（2026-11-05 Windows 实测反馈：会话执行到中途 `状态：失败`、`[NodeRunError] run node[ChatModel] pre processor fail: exceeds max iterations`）；实施：order-tracer / Lead
- 关联：`internal/runtime/agent.go`、`internal/app/services.go`、`internal/app/step_limit_notice.go`、`internal/shell/plugin_details.go`、DSH 参考（`dsh-agent-loop`）、实施报告 `.cache/team/report-max-iterations.md`

## 1. 背景

用户 Windows 会话在一次较长工具循环中途整轮失败：

```
状态：失败
[NodeRunError] run node[ChatModel] pre processor fail: exceeds max iterations
```

审计结论：

- 错误来自 eino ADK `adk/react.go:34 ErrExceedMaxIterations`；`MaxIterations` 是「ChatModel generation cycles」= **模型调用次数**上限（`adk/react.go:469-475`：N 次允许，第 N+1 次报错）。ZeoKeel 在全部拓扑构建点（`internal/runtime/{agent,modes,mode_deep,subagent}.go`）确实把 `a.maxIterations` 传给了 eino，**不是配置没生效**；
- 旧默认 `DefaultMaxIterations = 20` 偏低；eino 把错误包成 NodeRunError 冒到 runtime → `fail()` → turn failed + `state.Error`，已完成工具结果无法正常收尾（截图 footer 的「7 步」是上一成功回合的 Usage，失败回合没有 usage；实际是第 21 次模型调用被拒）。
- DSH 对照：`dsh-agent-loop` 的 turn 是 `while (true)` 模型/工具循环，**没有固定步数上限**，只有 abort/取消/模型结束/错误边界；其设置页（`dsh-client-ui-settings-agent-loop`）只有 Parallel tool calls，没有 max iterations。

## 2. 决策

1. **提高安全上限，而不是完全去掉**：`DefaultMaxIterations` 20 → **200**；新增 `MaxIterationsLimit = 1000`，Settings（Plugins → Agent loop → Max iterations）默认/范围同步为 1–1000（已持久化的 >100 值保留但被 clamp 到 1000）。完全无上限在桌面产品里对失控成本/无限循环不友好，200 覆盖绝大多数真实任务。
2. **超限优雅收尾**：runtime 识别 `errors.Is(ev.Err, adk.ErrExceedMaxIterations)` 后走 `completeMaxIterations`（`internal/runtime/agent.go`）：
   - 收尾 pending 工具（不留下永久 running 的工具卡）；
   - 追加一条本地化 assistant 提示（`internal/app/step_limit_notice.go`，zh/en 由 app 通过 `Config.StepLimitNotice` 注入）：说明已达单轮步骤上限、已完成结果保留、可继续发消息让我接着做；
   - 正常保存 history 与 checkpoint、发真实 usage、回 idle——**不再 failed/Error**。
3. 超时、取消、真实模型/工具错误仍走原 `fail()` 路径（保持 failed）。

## 3. 后果与验证

- 正面：长工具循环不再因 20 步硬失败；撞上限时用户看到可理解的提示并能继续，工具结果与历史不丢；失败回合的 footer 计数不再复用上一轮（优雅收尾会发 usage）。
- 取舍：仍存在安全上限（非 DSH 式无限）；默认 200 对极长无人值守任务可能不够，可在 Settings 调到 1000；完全移除上限留待有明确需求再评估。
- 测试：`internal/runtime/max_iterations_test.go`（修复前 `phases=[running failed]` + NodeRunError；修复后 `phases=[running idle]`、无 error、提示文本、usage Steps、history 保留工具结果；含 `Config.Provider` 生产路径）、`internal/app/step_limit_test.go`、`plugin_details` 设置测试；全仓 `go test ./... -count=1` 绿。
