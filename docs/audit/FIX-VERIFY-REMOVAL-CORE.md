# 独立复核报告（verifier-core）：Round-1 PTC / 语音删除验证

> 复核者：verifier-core · 日期：2026-11-06 · 基线：**`d69d7d9`（feat: remove PTC and voice input…）**，复核时 `HEAD=d69d7d9`、工作树干净；所有实验在 `git archive HEAD` 解包的 `/tmp` 副本内进行（`/tmp/verify-removal`、`/tmp/vr-fresh`），未改仓库被跟踪文件、未提交。
> 范围：核心侧 3 项（TDD RED 证据审计、删除守卫有效性、删除后行为与门禁）；UI 侧（模式选择器/插件页/截图/旧会话 UI 回退）属 verifier-ui，本报告不覆盖。
> 证据：`.cache/audit/verify-removal/`（`build-test.log`、`tdd-red-rerun.log`、`guard-effectiveness.log`、`independent-probes.log`、`residual-grep.log`）。环境：Go 1.27.3、`CGO_ENABLED=0`、共享 `GOCACHE/GOMODCACHE`。

## 0. 结论摘要

| 检查项 | 判定 | 关键证据 |
| --- | --- | --- |
| ① TDD `tdd_red` 红测试是否可复跑 RED / 有 RED 证据记录 | **PARTIAL**（可复跑 RED 5/5；**记录在案的红证据仅 1/5**） | 我重跑 `-tags tdd_red`：runtime 2、session 3、acp 7、presets 5、updater 11 = **28/28 FAIL**（与设计一致）；但只有 `updater` 有落盘 RED 日志（`.cache/fix-platform/updater-red.txt`），其余 4 包只有测试文件内注释说明"RED today"，无失败输出存档；`.cache/` 被 gitignore，证据未进提交/CI |
| ② 删除守卫是否真的防回归（临时恢复 PTC 符号 → 守卫应 FAIL） | **CONFIRMED** | 仅在 `/tmp` 副本恢复 `ModePTC="ptc"`（const+Modes()+NormalizeMode）→ runtime 3 个守卫全 FAIL；恢复注册表 `ptc` 工具 → app 2 个守卫 FAIL；恢复 `voice_input` inspect 行 → voice 守卫 FAIL；还原后守卫 PASS 且三文件 md5 与 HEAD 一致 |
| ③ 删除后行为：`NormalizeMode("ptc")` 报错 / 注册表无 ptc / prompt 无 mode:ptc / inspect 无 voice_input / 门禁绿 | **CONFIRMED** | 独立探针 4 个全 PASS（`VC3-*`）；`go build ./...` OK、`go vet ./...` OK、`go test -count=1 ./...` **25 包全绿**（exit 0）；生产核心代码 `ptc/voice` 引用 = 0；`internal/runtime/ptc.go` 已删除（-265 行） |
| 总体 | **3 项中 2 CONFIRMED + 1 PARTIAL（证据记录缺口，非功能缺陷）** | — |

## 1. ① TDD RED 证据审计（PARTIAL：红可复跑，但证据记录不完整）

**复跑命令与结果**（干净 archive，`-tags tdd_red`；完整日志 `tdd-red-rerun.log`）：

| 包 | 红测试（编号见下） | 复跑结果 | 记录在案的 RED 证据 |
| --- | --- | --- | --- |
| `internal/runtime` | `TestAskUserBlockingSeamExists`、`TestAskUserBlocksWithinTurn`（FP-16） | **2/2 FAIL**，首个失败断言："ask_user returned after 99µs … must block for the user's answer" | 无落盘日志；仅测试文件头注释说明"red today/seam 不存在" |
| `internal/session` | `TestSessionsRunInParallelWithPerSessionAgents`、`TestAgentFactorySeamAvailableForPerSessionAgents`、`TestApprovalDoesNotBlockOtherSessions`（RT-04） | **3/3 FAIL**（parallel: 611ms > 450ms limit；approval 超时；factory seam 缺失） | 无落盘日志；仅注释"must stay red until the seam exist" |
| `internal/acp` | `TestServerInitialize…`、`…NewSessionPromptAndCancel`、`…RejectsUnknownMethodAndBadJSON`、`…RequiresInitializeBeforePrompt`、`…RejectsRelativeCwd`、`…ReturnsErrorWhenAgentFails`、`TestStdioLoopStreamsResponses` | **7/7 FAIL**（每条失败消息带"RED (TDD acceptance not yet implemented)"） | 无落盘日志；测试文件头有契约说明 |
| `internal/presets` | `TestBuiltinPresetsPresent`、`…LoadUserPresetsFromJSON`、`…ToolAllowlistApplied`、`…ResolutionAndValidation`、`…PersonaPromptInjected` | **5/5 FAIL**（"preset catalog is not implemented yet (RED)"） | 无落盘日志；注释"STEP 0 is RED" |
| `internal/updater` | 11 个 `TestCheck*/TestParser*/TestCompare*/TestDownload*/TestInstall*` | **11/11 FAIL**（`updater: not implemented yet`，与记录一致） | ✅ `.cache/fix-platform/updater-red.txt`（命令+逐用例输出+"11/11 FAIL"结论）；`docs/BUGS.md:47,73` 引用 |

- 共 **28 个 red 测试全部可复跑失败**；默认门禁（无 tag）中 `acp/presets/updater` 显示 `[no test files]`，`go test ./...` 25 包全绿 → **红测试不会污染默认门禁**（build tag 生效，CONFIRMED）。
- **缺口**：commit message 称各包"each has recorded RED evidence"，实际只有 updater 有可查的失败输出存档；其余 4 包只有测试内注释。且 `.cache/` 在 `.gitignore` 中 → 唯一证据文件也不在提交内，换机器/清缓存即丢（`docs/` 引用 `.cache/fix-platform/updater-red.txt`，链接不可复现）。
- **建议（P2/P3）**：把 RED 证据落成**被跟踪**文件（如 `docs/audit/TDD-RED-EVIDENCE.md`，含命令+首个失败断言摘要+日期+commit）；或让 `docs/BUGS.md`/FIX-PLAN 引用 tracked 证据路径；至少把另外 4 包的 RED 输出按 updater 格式补存。commit message 可改为"updater 有落盘 RED 证据，其余以测试注释记录"。

## 2. ② 删除守卫有效性实验（CONFIRMED；仅在 /tmp 副本）

命令与完整输出：`.cache/audit/verify-removal/guard-effectiveness.log`。要点：

1. **恢复 PTC 符号**（`internal/runtime/modes.go`：`ModePTC = "ptc"` + `Modes()` 加回 + `NormalizeMode` 接受）→
   `go test ./internal/runtime -run 'TestModesExcludeRemovedPreset|TestRemovedPresetRejectedByNormalize|TestRemovedPresetNotRegistered'` →
   **3/3 FAIL**，如 `Modes() = [standard minimal creator ptc], must not expose the removed preset`、`NormalizeMode("ptc") = "ptc", want an unknown-mode error`。
2. **恢复注册表 ptc 工具**（`internal/app/services.go` 注册一个名为 `ptc` 的探针 Tool）→
   `go test ./internal/app -run 'TestToolRegistryHasNoPTC|TestAppModesExcludePTC'` → **2/2 FAIL**（`production registry still registers the PTC batch tool`、`mode "standard" tool set still contains ptc`）。
3. **恢复 voice_input 目录项**（`internal/tools/inspect.go` plugin catalog 加回 `voice_input`）→
   `go test ./internal/app -run TestInspectPluginCatalogHasNoVoice` → **FAIL**（`inspect still advertises the removed voice_input plugin`）。
4. 还原三个文件 → 守卫全部 PASS；`md5sum` 与 fresh `git archive HEAD` 逐字节一致（modes.go `adcd54…`、inspect.go `6374991…`、services.go `e9b77f2…`）→ 实验未污染，且证明守卫通过 ≠ 空转。

结论：**删除守卫是有效防回归网**（重新引入 PTC/voice 任一符号都会让 `go test ./...` 变红）。

## 3. ③ 删除后行为独立验证（CONFIRMED）

独立探针源码在 `/tmp/verify-removal`（归档于 `independent-probes.log`；不改仓库）：

| 断言 | 结果 |
| --- | --- |
| `NormalizeMode("ptc"/"PTC"/" ptc "/"ptc-mode"/"ptc_mode"/"Ptc")` 全部报 `unknown agent mode` 错误；`Modes() == [standard minimal creator]`；三个合法 mode 仍可 Normalize | PASS（`VC3-PTC-CORE`） |
| prompt：`Sections()` 无 id 含 ptc；`Compose` 在 `AgentMode/Mode="ptc"` 变体下均不出现 "ptc"；基线 prompt 亦无 | PASS（`VC3-PROMPT`，prompt len=2386） |
| 生产工具注册表：44 个工具，无 `ptc`；`runtime.ModeTools(mode, names)` 对 standard/minimal/creator/**ptc**/deep 均不合成 `ptc` | PASS（`VC3-APP-CORE`） |
| `inspect` 的 plugins 输出不含 `voice_input`（也不含 "voice"） | PASS（同上） |
| 会话兼容：`SetMode("ptc")` 被拒；持久化旧值 `session.<id>.mode="ptc"` 重开 `OpenSession` 后 `Mode()==standard`（不复活） | PASS（`VC3-SESSION`） |
| 生产核心代码残留：`grep -rn 'ptc\|PTC\|voice\|Voice\|语音' internal cmd --include=*.go \| grep -v _test.go` = **空**；含测试的文件仅 `internal/app/ptc_voice_removal_test.go`（守卫本身，字面量）；`internal/runtime/ptc.go` 不存在（commit 删除 265 行） | PASS（`residual-grep.log`，在无探针的 pristine archive 上核对） |
| 构建/门禁：`go build ./...` OK、`go vet ./...` OK、`go test -count=1 ./...`（25 包）**全绿 exit 0**；`go list ./...` = 25 | PASS（`build-test.log`） |

备注：commit message 写 "go test ./... (23 packages)"，实际 `go list ./...` = **25 包**（新增 acp/presets/updater 等），属描述性小误差（不影响结论）；另 `internal/shell` 侧守卫使用混淆字面量，不在核心 grep 命中范围（归 verifier-ui 复核）。

## 4. 未验证 / 残余风险

1. UI 侧全部项目（Xvfb 真跑、模式选择器/插件页无残留、旧会话 UI 回退、设置页文案截图）由 verifier-ui 负责，不在本报告。
2. `-tags tdd_red` 未纳入 CI（设计如此：红测试只做本地/下一轮契约）；"实现后转绿"未验证（实现尚未开始）。
3. 未跑 `check-nocgo`、Windows/macOS 交叉构建与真机；本报告只验证 Linux `CGO_ENABLED=0` 构建/测试。
4. RED 证据缺口的具体影响（审计可追溯性）属流程判断，建议 Lead 决定是否补 tracked 证据文件。
5. 会话旧值回退仅测 `session.<id>.mode` 持久化路径；UI 展示层回退由 verifier-ui 覆盖。

## 5. 复核产物

- `.cache/audit/verify-removal/build-test.log`：干净 HEAD 的 build/vet/vet/25 包测试结果。
- `.cache/audit/verify-removal/tdd-red-rerun.log`：5 包 `-tags tdd_red` 复跑，28/28 FAIL 明细。
- `.cache/audit/verify-removal/guard-effectiveness.log`：三组"恢复符号 → 守卫 FAIL → 还原 → PASS"实验与 md5 一致性证明。
- `.cache/audit/verify-removal/independent-probes.log`：4 个独立行为探针输出（`VC3-*`）。
- `.cache/audit/verify-removal/residual-grep.log`：pristine archive 上的残留 grep 证据。
- 实验副本：`/tmp/verify-removal`（HEAD 解包 + 临时探针，实验后已还原）、`/tmp/vr-fresh`（pristine 对照）。
