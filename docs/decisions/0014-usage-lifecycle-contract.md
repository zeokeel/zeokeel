# ADR 0014：运行中用量与中断生命周期事件契约

- 日期：2026-11-05
- 状态：**已采纳**
- 决策者：用户（2026-11-05 反馈：运行中 footer 一直「0 轮 0 步 0 输出 tok」、会话卡在运行中、取消后工具卡一直转）→ 实施：chat-order / Lead
- 关联：`internal/runtime/{stream,agent,history}.go`、`internal/state/{event,snapshot}.go`、`internal/session/{controller,events,history}.go`、`internal/shell/{message_actions,session_live}.go`、调研报告 `.cache/team/report-usage-counters.md`、实施报告 `.cache/team/report-usage-impl.md`

## 1. 背景

ZeoKeel 的事件契约（M2 时期）是：一个 turn 内只有 Token/Reasoning/ToolStart/ToolEnd，**turn 结束后**才发一条 `Status Idle → Usage`。这带来四个用户可见问题：

1. **运行中 footer 永远是 0**：shell 只读快照 `Usage`，回合中没有任何 usage 事件；完成一回合后再跑，显示的是**上一回合**的旧值。
2. **已取消/失败的回合没有结算**：Stop/审批中取消/真实失败分支 return 前不发 usage，JSONL 里可以 0 usage 事件。
3. **用途语义错误**：`turnUsage.snapshot()` 硬编码 `Turns: 1`，`state.Apply` 整条替换，重开只回放最后一条 usage → 不是会话累计。
4. **中断状态不可见**：取消路径不补 `ToolEnd`，已持久化的 `ToolStart` 让工具卡永久 running；崩溃/导出后重启，`phaseFromStore` 直接把持久化的 running 映射成运行中（幽灵运行中）；空 assistant 草稿（reasoning-only）落库污染历史。

DSH 的对照：usage 是持久日志投影（`step/end` + 带 usage 的 assistant message），客户端订阅实时累计；中断 turn 用 `interrupted:true` 渲染「已停止」；崩溃用 `interruptedTurnClosers` 补合成 tool/result 错误 + `step/end` + `turn/end interrupted`。

## 2. 决策

### 2.1 Usage 改为「会话累计 + live delta」

- `state.Usage` 语义 = **会话累计**：`Turns` 每回合 +1，`Steps` 每次模型调用 +1，`In/Out/CacheHit/ContextPct` 累计/最新值。
- runtime 在**每次模型调用完成**后 emit 一条 usage delta（首条 `Turns:1`，Steps 递增，tokens 增量）；成功、取消、失败、优雅超限都发终态 usage；回合末不再发一次性绝对快照。
- state 的 Apply 对 usage 做累计 fold，而不是整条替换；重开关闭旧 `per-turn` 绝对事件仍可求和兼容（旧事件按绝对值处理）。
- shell footer 直接读累计快照：运行中实时显示 turns/steps/output tok，tok/s 用本回合基线，全 0 且无消息时隐藏，`完成于` 只给完成回合。

### 2.2 Status 增加 Reason + 中断收尾

- `state.Status` 增加 `Reason`（`stopped` / `failed` / `interrupted`，零值兼容）。
- Stop/取消路径也调用 `finishPendingTools`，为悬挂工具补 `ToolEnd{Cancelled}`/错误结果；工具卡与 `tool_calls` 行不再永久 running。
- 取消/失败都发终态 Status（带 Reason）+ 已结算 usage；UI Chat 显示「已停止/已中断」标记。
- `OpenSession`/`Submit` 对「持久化 running 但没有 in-memory turn」的冷会话做恢复：关闭悬挂 tool_calls（模型可见 outcome-unknown 文案）+ 发 `idle/interrupted`；`phaseFromStore` 非终态归一 idle。

### 2.3 空 assistant 草稿

- `persistDrafts` 只落有可见文本的 assistant 行（reasoning-only/空内容跳过）；
- 历史读取（`messagesToEino`/`storeHistory`）丢弃 reasoning-only assistant 与悬挂 tool_calls 空结果，避免污染下一轮模型上下文。

### 2.4 团队 Stop 钩子（跨范围小项）

- `Controller.SetTeamStopper(func(sessionID))`，`Stop()`（含无 in-flight turn 的情况）调用；app 接到 `stopTeamSession`，让非 UI Stop 也能取消该会话的异步团队成员（配合 BUG-5/9 的 manager 生命周期修复）。

## 3. 后果与取舍

- 正面：运行中成本可见；Stop/失败/崩溃/重开都有确定终态；工具卡不再永久转；历史不被空回复污染；旧事件数据兼容。
- 取舍：`Status.Reason` 只保留**最近回合**（历史逐轮 stopped 标记属后续 P1，需要 messages/turn 级持久化）；取消瞬间 trailing usage chunk 可能丢 1 次调用计数（语义可接受，已记录）；footer 空行保留 24 DIP 防布局跳动。
- 测试：runtime 多步 usage delta / Stop 取消收尾 / 失败保留部分 usage；session 累计+重开 / Stop 清理 / stale running 恢复 / teamStopper；shell footer live/停止/中断/真实 controller 行为；全仓 `go test ./... -count=1` 连续两次全绿。
