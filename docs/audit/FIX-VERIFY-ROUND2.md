# Round2 独立复核（runtime-auditor，task-37 §3）

- 基线：Round2 提交 `8e2ba5b`（HEAD 可能被并发 worker 移动；本报告在 `d69d7d9` 工作树上完成，不改代码、不 commit。
- 范围限定：本报告只覆盖 **ACP（本人负责包）/ RT-04 并发压力 / updater / presets** 的独立抽检；完整的 Round2 独立验证由 verifier-core 的 `FIX-VERIFY-ROUND2-CORE.md`（task-38）负责，不重复其矩阵。
- 结论：**未发现未修复缺陷**；唯一发现 P3（ACP initialize 顺序竞态）已修复并获 verifier-core 在干净快照上独立复验。

## 1. ACP 协议（internal/acp）· CONFIRMED

| 检查项 | 结果 | 证据 |
| --- | --- | --- |
| initialize/session-new/prompt/cancel 正常流 | CONFIRMED | `go test -count=1 ./internal/acp` **13/13 PASS**（含 pipelined ordering 2 条新测试） |
| 坏 JSON 存活 / -32601 / -32603 / -32602 / -32600 | CONFIRMED | 同套件；`TestServerRejectsUnknownMethodAndBadJSON` 等 |
| cancel → stopReason `cancelled`；单会话单 in-flight（first-prompt-wins） | CONFIRMED | `TestServerCancelSettlesPromptAsCancelled`、`TestServerFirstPromptWinsSingleInflight` |
| EOF 清理 / ctx 取消（SIGINT） | CONFIRMED（本轮修复） | `TestServeStopsOnContextCancellation`；修复前阻塞 stdin 读导致 SIGINT 空闲不退出，改为后台 reader + select ctx 后 exit 0 |
| initialize 顺序竞态（P3） | **FIXED**（verifier-core 干净快照复验 CONFIRMED） | pipelined prompt-before-init 现在同步按帧到达顺序返回 -32600；`TestServerPipelinedPromptBeforeInitializeRejected` / `...InitializeThenPromptRuns` |
| 8MiB 帧、并发 id、update 顺序、-race | CONFIRMED | 作者测试 + verifier-core task-38 探针（`zz_verify_round2_acp_test.go`）在干净快照全 PASS |

## 2. RT-04 / 并发压力 · CONFIRMED（抽检）

- `CGO_ENABLED=1 go test -race -count=1 ./internal/runtime` → **ok 3.9s**（含 QuestionGate/审批/流式/检查点用例）。
- `go test -count=1 -timeout 240s ./internal/session` → **ok 37s**（含 parallel turns / approval-does-not-block-other-sessions / session events 订阅）。
- 未复跑 verifier-core 的 3+ 会话真并发压测，矩阵以 task-38 为准；本项仅确认无回归。

## 3. updater / presets · CONFIRMED（抽检）

- `go test -count=1 ./internal/updater` → ok；`go test -count=1 ./internal/presets` → ok。
- 未重复 verifier-core 的崩溃恢复/坏 JSON 对抗用例（见 task-38）。

## 4. 已知外部失败（与本轮改动无关）

- 最终全仓 `go build ./...` 绿；`go test ./internal/app` 因并发 worker 未收口的 automation/session-permission 测试 `TestRunAutomationTaskRecordsFailure`（want "ask", got ""）失败，属对应 owner 在改范围，不影响 state/runtime/acp/session 包结论。
