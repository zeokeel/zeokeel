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
| updater 下载/安装 UI | 安装动作未接时 RED：`TestUpdateDownloadInstallFlow`: `text "Restart ZeoKeel" did not appear`；`TestUpdateInstallErrorIsShown`: `text "checksum mismatch" did not appear`（2026-11-06 记录，随后接线） | GREEN：状态机 idle→checking→available→downloading→done/error（含下载进度 % 文案）；无 feed 时按钮禁用并提示配置 `update.feed_url`；成功后「重启 ZeoKeel 完成安装」+「立即重启」；**app seam**：导出 `SetInstallUpdateHook`（`UpdateInstallHook`/`UpdateInstallOutcome` 对齐 `app.DownloadAndInstallUpdate` 的 UpToDate/Staged/Applied/Error 契约），未注入时回退 `updater.Download+ScheduleInstall`；测试 `TestUpdateDownloadInstallFlow`（含 progress）/`TestUpdateInstallErrorIsShown`/`TestUpdateNoFeedDisablesInstall`/`TestUpdateInstallFallbackWithoutHook` PASS；窄窗 `TestSettingsPagesFitNarrowWindow`（900×600）PASS |
| 首启 setup 卡（P2 回归） | production-wiring fixture（factory+无模型）下 RED：`setup card missing with a factory but no model`、`empty session lost the Add model action`（此前 `!HasAgent()` 因 factory 安装恒 false） | 改用 `Controller.HasConfiguredModel()`（home.go/session_live.go）；fixture=`SetAgentFactory`+无模型+无 predicate→false，`SetModelConfiguredFunc(true)`→隐藏；测试 `TestHomeSetupCardShows/HidesWithConfiguredModel`、`TestEmptySessionAddModelShows/HidesWithConfiguredModel` PASS；真跑截图 `.cache/audit/uiux/wave2-shots/25-first-run-home-setup-card.png`（干净 data dir：创建工作区+配置模型两行）、`26-first-run-configure-model.png`（配置模型→Settings>模型） |
| P3 文案本地化 | hotkey 解析错误在中文 UI 泄漏英文（`Invalid shortcut: … unknown key`）；automation 失败原因显示 `session: no agent configured` | 新增 `hotkeyErrorText`（empty/需要修饰键/未知修饰键/重复修饰键/未知按键→中文）与 `automationFailureText`（no agent/model、取消、限流、鉴权、不可达→中文，未知原因保留原文）；测试 `TestHotkeyErrorsAreLocalized`、`TestAutomationFailureReasonIsLocalized` PASS |
| FP-16 问题卡撤回 | 卡片被控制器撤回（超时/回合结束）时此前无任何提示 | 按卡 id 跟踪 on-screen 状态：未回答即消失时 toast「问题卡已撤回（超时或回合结束）；代理将继续处理。」；`TestQuestionCardWithdrawalIsExplained` PASS |
| presets UI（FP-06） | 设置页原为旧 mode 列表，无 registry 列表/picker | Settings>代理预设=registry 列表（builtin 在前、覆盖标识、tools 数、Model、warning/banner、Reload/打开目录/创建示例）+ 会话 header picker（SetSessionPreset/persona 摘要/回退 warning）；product-auditor 复核 CONFIRMED（`FIX-VERIFY-PRESETS-UI.md`），shell 测试 `TestPresetsPage*`/`TestSessionPresetPicker*` PASS |
| updater | `go test -tags tdd_red ./internal/updater` 11/11 FAIL（`updater: not implemented yet`；日志 `.cache/fix-platform/updater-red.txt`，摘录：`--- FAIL: TestCheckFindsNewerVersion ... updater: not implemented yet`） | 14/14 PASS（含 3 个崩溃恢复），标签已移除 |
| globalhotkey | 9 FAIL（`ParseHotkey: globalhotkey: not implemented yet` 等；日志 `.cache/fix-platform/globalhotkey-red.txt`） | 10/10 PASS，5 目标交叉编译 OK |
| PTC/voice 删除守卫 | 守卫测试先于删除：`TestModesExcludePTC` 得到 `[standard ptc minimal creator]`；`TestPluginListExcludesVoiceInput` 仍见 `voice_input`；`TestToolRegistryHasNoPTC` registry 仍含 ptc | 删除后守卫 PASS；独立验证在 /tmp 副本恢复 PTC 符号时守卫再次 FAIL（`FIX-VERIFY-REMOVAL-CORE.md` §②） |
| FP-05 diff UI | 3 FAIL（`workspace changes block missing`、`no element shows "parser.go"`、replay 缺失） | 3 PASS（渲染/点击打开/replay） |
| UX-14 toast | `toast is not in the top band {Y:949}` | **主窗 + Settings 窗都改走 shell 自绘 top-band + `.PassThrough()` viewport**（此前主窗仍是框架底部 toast，压 composer 工具栏；2026-11-06 verifier-ui 复核后补修）：Home/Session × 1600×1000 / 900×600 下 toast 均不遮 composer 及其 footer，toast 可见时点 composer chip 仍能开菜单（指针穿透）；测试 `TestToastDoesNotOverlapComposer`/`TestToastDoesNotInterceptPopupPointer`/`TestSettingsToastSitsInTopBand` PASS；真跑截图 `.cache/audit/uiux/wave2-shots/14,15,17,18` |
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
