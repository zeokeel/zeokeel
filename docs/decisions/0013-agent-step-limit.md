# ADR 0013：单轮步骤上限（eino max iterations）：默认不限、AI 自行结束 + 可选安全上限

- 日期：2026-11-05
- 状态：**已采纳**（2026-11-05 修订：默认不设上限，对齐 DSH「AI 自己决定何时结束」；显式上限保留为可选安全功能）
- 决策者：用户（2026-11-05 Windows 实测反馈整轮失败；随后追问「dsh 有设置么？不应该是让 AI 自己决定什么时候结束么？」→ 默认改为不限）；实施：order-tracer / Lead
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

## 2. 决策（2026-11-05 修订后）

### 2.1 默认不设步数上限（对齐 DSH）

- `runtime.DefaultMaxIterations = 0`；`Config.MaxIterations <= 0`（含负数）表示**不设固定上限**：模型返回不带 `tool_calls` 的最终回答即本轮结束——由 AI 自己决定何时收尾（与 DSH 的 `while (true)` agent loop 一致）。
- eino 的坑：`adk/react.go` 的 `genReactState`/`genAgenticReactState` 在 `config.maxIterations <= 0` 时会**回退到 eino 自己的默认 20**，所以「不限」不能传 0。`internal/runtime/agent.go` 的 `einoMaxIterations()` 在配置 `<=0` 时传 `unlimitedEinoIterations = 1_000_000_000` sentinel（足够大、int 递减安全），显式 `>0` 原样传；`modes.go`/`mode_deep.go`/`subagent.go` 六处 eino 拓扑构建点统一改用它。
- eino 返回的 `ErrExceedMaxIterations` 只有在用户显式配置了有限上限时才可能出现。

### 2.2 显式上限是可选的用户安全功能，不是默认行为

- Settings（Plugins → Agent loop → Max iterations）默认 **0 = 不限**（中英文案已说明），可选 1–1000；`plugin.agent_loop.max_iterations` 缺失/空/不可读 → 0；负值 clamp 到 0；plugin 关闭仍是旧语义「单轮 1 步」。
- 命中显式上限时仍走 `completeMaxIterations` 优雅收尾（`internal/runtime/agent.go`）：收尾 pending 工具、追加本地化提示（`internal/app/step_limit_notice.go`，zh/en 由 app 注入）、正常保存 history/checkpoint、发真实 usage、回 idle——不再 failed/Error。
- 超时、取消、真实模型/工具错误仍走原 `fail()` 路径（保持 failed）。

### 2.3 为什么保留可选上限

DSH 自身没有该设置（只暴露 Parallel tool calls）；ZeoKeel 保留一个默认关闭的上限，供在意 token 成本/无人值守的部署显式开启。默认语义仍是「AI 自己决定何时结束，用户可随时 Stop」。

## 3. 后果与验证

- 正面：默认下长工具循环不会被任何固定步数截断，AI 用最终回答决定结束（DSH 同款语义）；显式开了上限的用户撞线时仍有可继续的优雅收尾，不再整轮红失败。
- 取舍：默认「不限」意味着失控循环只能由用户 Stop / 取消 / 真实错误终止（DSH 同样如此）；sentinel 是 10 亿而非数学无限，但远超任何真实回合；显式上限是 ZeoKeel 的可选增强，默认关闭。
- 测试：`internal/runtime/max_iterations_test.go`
  - `TestUnlimitedIterationsModelEndsTurn`：默认 0 下假模型连续 25 次工具调用（> eino 的 20 回退）后自行返回最终文本 → 26 次模型调用/25 次工具调用、`[running idle]`、无 Error、无步数提示；
  - `TestNegativeMaxIterationsMeansUnlimited`、`TestExplicitMaxIterationsGetter`；
  - 显式 cap=3 时仍优雅收尾（原来的修复路径保留），真实错误仍 failed。
- app：`internal/app/step_limit_test.go` 覆盖默认 0/显式值/越界 clamp/负值/不可读/plugin 关闭；shell：`plugin_details` 默认 0、0 可保存、负数与超限拒绝。全仓 `go test ./... -count=1` 绿。
