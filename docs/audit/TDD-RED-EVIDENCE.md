# TDD RED 证据（tracked）

> 目的：记录「先写失败测试、再实现」的 RED 证据，回应独立验证（`FIX-VERIFY-REMOVAL-CORE.md` §①：部分 RED 证据只在 gitignored `.cache/` 或 agent 日志里）。
> 约定：实现完成后，测试的 `//go:build tdd_red` 标签被移除并进入默认门禁；仍处于 RED（未实现）的功能保持标签并由 `scripts/tdd-red-evidence.sh` 复跑。历史 RED 输出摘录见下（来自当时的 worker 日志/会话记录）；当前可复现的 RED 用命令给出。

## 1. 当前仍可复现的 RED（FP-16，未实现）

```sh
go test -tags tdd_red -count=1 -run 'TestAskUser' ./internal/runtime
```

实际输出（2026-11-06 01:12，HEAD d69d7d9+worktree）：

```text
--- FAIL: TestAskUserBlockingSeamExists (0.00s)
    ask_user_blocking_test.go:36: RED (FP-16): runtime.Config has no QuestionGate seam; ask_user is fire-and-forget and cannot block inside a turn
--- FAIL: TestAskUserBlocksWithinTurn (0.00s)
    ask_user_blocking_test.go:67: RED (FP-16): ask_user returned after 84.966µs with {"kind":"question","question":"Which database should I target?","status":"pending"}; it must block for the user's answer (published card + answer channel) instead of ending the turn with a pending question
FAIL github.com/zeokeel/zeokeel/internal/runtime 0.008s
```

## 2. 已转 GREEN 的 RED→GREEN 记录

| 功能 | RED（写测试后首次运行） | GREEN（实现后） |
| --- | --- | --- |
| RT-04 per-session Agent | `TestSessionsRunInParallelWithPerSessionAgents`: "two one-call sessions took 615.4ms (limit 450ms)"；`TestAgentFactorySeamAvailableForPerSessionAgents`: "session.Config has no AgentFactory seam"；`TestApprovalDoesNotBlockOtherSessions`: "session B did not finish within 2s ... global agent runMu" | parallel 0.32s、approval 0.06s；`go test ./internal/session` ok 31.25s；`//go:build tdd_red` 已移除 |
| ACP 最小协议 | `go test ./internal/acp` 7/7 FAIL（`ErrNotImplemented`: "JSON-RPC server loop is not implemented yet" / stdio loop） | 9/9 PASS（新增 cancel/ordered-updates 用例），`-race` PASS；标签已移除 |
| presets/persona | 5/5 FAIL（`Builtins() returned 0 presets, want 3`；`Load returned a nil registry` 等） | 6/6 PASS（含 testdata 示例），标签已移除 |
| updater | `go test -tags tdd_red ./internal/updater` 11/11 FAIL（`updater: not implemented yet`；日志 `.cache/fix-platform/updater-red.txt`，摘录：`--- FAIL: TestCheckFindsNewerVersion ... updater: not implemented yet`） | 14/14 PASS（含 3 个崩溃恢复），标签已移除 |
| globalhotkey | 9 FAIL（`ParseHotkey: globalhotkey: not implemented yet` 等；日志 `.cache/fix-platform/globalhotkey-red.txt`） | 10/10 PASS，5 目标交叉编译 OK |
| PTC/voice 删除守卫 | 守卫测试先于删除：`TestModesExcludePTC` 得到 `[standard ptc minimal creator]`；`TestPluginListExcludesVoiceInput` 仍见 `voice_input`；`TestToolRegistryHasNoPTC` registry 仍含 ptc | 删除后守卫 PASS；独立验证在 /tmp 副本恢复 PTC 符号时守卫再次 FAIL（`FIX-VERIFY-REMOVAL-CORE.md` §②） |
| FP-05 diff UI | 3 FAIL（`workspace changes block missing`、`no element shows "parser.go"`、replay 缺失） | 3 PASS（渲染/点击打开/replay） |
| UX-14 toast | `toast is not in the top band {Y:949}` | 主窗口默认 toast 不遮 composer + Settings 自绘 top-band pass-through；3 测试 PASS |
| P3 skills cwd | `go vet ./internal/skills`: `too many arguments in call to RootsFor`（新 API 缺失） | 4 新测试 + skills 全量 PASS |
| P3 manifest 迁移 | 旧 key 下 `SessionsImported=1`（重复导入）、裸 uuid 未迁移、损坏未 fail-closed | 迁移后 0 imported、key 重写、二次 idempotent；3 新测试 PASS |
| P3 spill TOCTOU | 编译缺 `spillDirOpenedHook` seam | `TestSpillRefusesDirSwapAfterValidation` + 原 RT-07 4 用例 PASS |
| keyless scheme | 新负例失败（ftp/ws/file 被当 keyless） | 仅 http/https 允许空 key，负例 PASS |

## 3. 复跑方式

```sh
# 仍有 RED 的测试（应保持失败，直到该功能实现）：
go test -tags tdd_red -count=1 ./internal/runtime
# 已转 GREEN 的契约测试都在默认门禁内：
go test -count=1 ./internal/{acp,presets,updater,globalhotkey,shell,session,app,skills,importer}
```

> 说明：`.cache/` 为 gitignored 临时证据；本文件是进入版本库的 RED 证据摘要。原始日志摘录（updater/globalhotkey/acp-cli）仍在 `.cache/fix-platform/`（本机可查）。
