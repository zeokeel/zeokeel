# 独立复核报告 A（verifier-core）：FP/RT/AR 的 P0/P1 与抽样 P2

> 复核者：verifier-core · 日期：2026-11-05 · 对抗性独立验证（不采信报告转述）
> 冻结基线：`git archive 27d5d5f | tar -x -C /tmp/verify-core`（报告行号对应此副本）；当前 HEAD 核验时 = **`3111614`**（任务书写的 `20f1399` 已前移；复核期间从 3111614 开始未再移动）
> 方法：① 在 `/tmp/verify-core` 内重跑全部 auditor 探针并逐条读代码；② 自写/旁路复现（Xvfb 窗口几何、端口抢占、双构建体积、-race）；③ 用 `git show HEAD:<path>` / `git grep HEAD` 核对"问题是否仍存在"并记录行号位移。
> 写入边界：未改任何被跟踪文件、未提交；证据在 `.cache/audit/verify/`（`probes-rerun.log`、`head-evidence.log`、`runtime-repro.log`）。
> 限制：未接真实模型；RT-01/RT-02 未走 GUI 端到端；DSH 侧仅以随包 README 为准；见 §5 未验证项。

## 0. 结论摘要

- 复核 30 条（P0×1、P1×16、反向结论×3、抽样 P2×10）：**CONFIRMED 28 / PARTIAL 2 / REFUTED 0**。
- **唯一 P0（RT-01，工作区 `mcp.json` 无审批执行任意命令）独立复现成立**：干净快照内重跑探针，marker 文件被创建、`connectMCP` 返回 `nil`；静态调用链与"无 trust 闸门"复核一致 → **P0 结论采纳**。
- **关键状态变化**：RT-06（provider 整条流 60s 硬超时）已由并发提交 **`3111614` 在 HEAD 修复**（默认无整体超时、仅响应头 60s、新增 `provider.timeout_seconds`）；报告 02 头部"并发提交不改变任何结论"现已不成立，master 必须更新状态。
- **重要校正**：RT-05 的"并发竞态"未证实（`-race` 复跑无告警、代码注释自述 child 并发安全）；FP-02 是代码自述的**有意设计取舍**（不嵌入 JS 沙箱），按 P1 缺陷计有夸大；RT-03 事实成立但严重度建议降 P2 或并入 RT-02。
- **反向校验三处全部成立**（文档说缺失、代码已实现并接线）：auto-review / goal round driver / plan mode；旧矩阵、`known-limitations.md:28` 与 brief 的"goal round driver 缺失"假设必须更正（brief 实际行号 184，FP 报告写成 177）。
- 重复项需合并：RT-12≡AR-11；FP-20≡AR-13（plan mode）≡AR-15（goal driver）；FP-12 的重试段与 RT-08 重复。

## 1. 方法与基线（实际执行）

```sh
mkdir -p /tmp/verify-core && cd /tmp/verify-core && git -C /data/mygo archive 27d5d5f | tar -x
# 探针按包拷贝后重跑（证据 .cache/audit/verify/probes-rerun.log，全部 PASS）：
go test -count=1 -run 'TestProbe' -v ./internal/app ./internal/session ./internal/runtime \
        ./internal/provider/openai ./internal/automation ./internal/store
# 既有测试反向校验：go test -run 'TestExitPlanMode|TestPlanMode' ./internal/app ./internal/shell
#                     go test -run 'AutoReview' ./internal/session ./internal/runtime
#                     go test -run 'Goal' ./internal/session            # 均 ok
# 自建复现（.cache/audit/verify/runtime-repro.log）：
#   Xvfb 1280x800 启动 dist 二进制 + xwininfo/ window.json  （AR-18 A/C 例）
#   python bind 127.0.0.1:49731 后启动 app                   （RT-12）
#   go build -trimpath -ldflags '-s -w' 对比 dist 体积        （AR-03）
#   go test -race -run TestProbeConcurrentSubagentsShareChildAgent ./internal/runtime（RT-05）
# HEAD 状态：git diff --stat 27d5d5f..HEAD；git show HEAD:<path>；git grep ... HEAD（.cache/audit/verify/head-evidence.log）
```

`27d5d5f..HEAD` 仅 9 个文件：`internal/app/services.go`、`internal/provider/openai/*`、`internal/shell/session_live.go`、`internal/state/snapshot.go` + 测试与 `known-limitations.md`（+1 行超时说明）。FP 功能结论与 AR 的 shell/docs 结论不受影响；**RT-06 被 `3111614` 直接修复**，其余 RT 条目在 HEAD 仍存在（行号见矩阵备注）。

## 2. 核验矩阵（来源ID | 报告严重度 | 判定 | 复核证据 | HEAD 状态/备注）

| 来源ID | 严重度 | 判定 | 复核证据（命令/符号而非转述） | HEAD/备注 |
| --- | --- | --- | --- | --- |
| RT-01 | P0 | **CONFIRMED** | 重跑 `probe_mcp_test.go`（`/tmp` 干净快照）：`CONFIRMED: workspace mcp.json command executed, created …/pwned-marker`；`connectMCP` 返回 `err=<nil>`。代码：`services.go:738` `pluginOn("mcp", true)` 默认开；`services.go:739` 调用；`connectMCP`（baseline `services.go:1411-1427`）在 rawSetting 空时读 `<root>/mcp.json`；`mcp/mcp.go:180` `exec.Command(server.Command, server.Args...)`；校验仅 name/env（`mcp.go:468-482`），无 trust/allowlist。`git grep trust HEAD -- internal` 无 workspace trust 状态。 | **仍存在**。HEAD 行号：services.go:739/1422，mcp.go:180；无修复提交 |
| RT-02 | P1 | **CONFIRMED** | 静态链：`rebuildForPermission` 仅 2 个调用点（baseline `controller.go:569` OpenSession、`:2914` SetSessionPermission），`submit`（`:631-760`）只 `bindWorkspace`+`applySessionMode`；自动化路径 `app.go:589-600` `CreateSession`→`Submit`；`CreateSession` 仅 `applySessionMode`，`sessionActivated`（app/session_cwd.go:28）只恢复 cwd 不重建。⚠️ auditor 探针的 `rebuilds==0` 断言因 `ErrNoAgent` 提前返回而空转（弱证据），但静态调用点证据充分。 | **仍存在**。HEAD 相同（569/submit 631） |
| RT-03 | P1 | **CONFIRMED**（严重度建议降 P2/并入 RT-02） | `readOnly := permission == session.PermissionAsk`（baseline `services.go:553`）；`PermissionAsk` 的 UI 标签确为 "Read Only"（`shell/home_live_menus.go:123-131`、`i18n.go:328`），故探针用 Ask 正确；`schedule_create/update/delete、memory、goal、todo` 在只读注册表中（探针重跑 `CONFIRMED … needsApproval=false`）。 | **仍存在**。HEAD `services.go:553` |
| RT-04 | P1 | **CONFIRMED**（架构限制，非 bug） | 探针重跑：两会话模型调用起点相隔 `150.996ms`（回合 150ms）→ 严格串行；`agent.go:713-714` `runMu.Lock/defer Unlock` 覆盖整个 eino 回合；审批阻塞在工具 `InvokableRun`→`awaitGateApproval`（`model_adapter.go:484-510`）内，锁内等待成立。 | **仍存在**。HEAD agent.go:713 |
| RT-05 | P1 | **PARTIAL** | (a) 成立：`agentteam.New(Config{})` 无 BaseContext→`context.Background()`（`manager.go:169-175`），成员 ctx 经 `manager.go:622` 传入 `RunSubagent`，gated 工具命中 `turnContext==nil`→`requires approval outside an agent turn`（fail-closed）。复跑探针：两并发 child 共享同一缓存 agent。**但 "竞态" 未证实**：`go test -race` 重跑无 `DATA RACE`；`subagent.go` 注释自述 child "safe for concurrent use"。 | **仍存在**。建议标题删"竞态"或移入未验证；P1 依据仅为 (a) |
| RT-06 | P1 | **CONFIRMED（基线）/ 已修复（HEAD）** | 基线：`openai.go:44-45` `DefaultTimeout=60s`（注释明确覆盖 streamed body）、`:133` `&http.Client{Timeout:…}`；`services.go:802-807` 未传自定义 client。**HEAD `3111614` 已改**：`DefaultResponseHeaderTimeout=60s` + `Timeout: cfg.Timeout`（默认 0=无限长）、`provider.timeout_seconds` 由 `services.go` 传入（HEAD `:806-815`）。 | **已由并发提交修复**；master 应将本条移至"复核期间已修复"，保留回归测试建议 |
| RT-07 | P1 | **CONFIRMED** | 探针重跑：`.zeokeel/tool-results` 为 symlink 时文件写到工作区外 `call-1.txt`；`tool_spill.go:40-48` 仅 `MkdirAll`+`WriteFile`，无 `Lstat`/`O_EXCL`。 | **仍存在**。HEAD 同（41/46 行） |
| RT-08 | P1 | **CONFIRMED** | 探针重跑：429+`Retry-After:0` 服务端只收到 1 次请求；`openai.go` 非 2xx 直接返回错误，无重试/退避；失败回合不写 history（`agent.go:778-783` "Persist only completed turns"）。 | **仍存在**。HEAD openai.go 无 Retry-After/backoff（grep=0） |
| FP-01 | P1 | **CONFIRMED** | `tools/workflow.go:20,58-90`：`DefaultWorkflowMaxSteps=8`、描述为固定顺序 `{"steps":[…],"agent":…}`；DSH `dsh-tool-workflow/README.zh.md` 为"JavaScript 编排脚本…`parallel()`/`pipeline()`"，`dsh-workflow` 同。 | 仍成立（代码未变） |
| FP-02 | P1 | **PARTIAL**（设计取舍被当作缺陷） | 事实成立：`runtime/modes.go:22-28` 描述 PTC 只做 batch pipeline、"deliberately … no JavaScript or other code-execution sandbox"；DSH `dsh-ptc-runtime(-node)` 是模型程序运行时。但这是**代码自述的有意产品/安全取舍**，不是实现缺陷；建议降 P2 或移入"产品边界/能力降级"章，而非缺陷清单。 | 仍成立 |
| FP-03 | P1 | **CONFIRMED** | `tools/subagent.go:58-75` 自述同步"runs the child agent to completion"；`SubagentResult` 无 childId（`runtime/subagent.go:42-48`）；`runtime/subagent.go:101-109` "Child agents are stateless"；DSH `dsh-subagent/README.zh.md`：spawn/fork、`startActivation()` 返回 childId、本地 child 保留持久会话。 | 仍成立 |
| FP-04 | P1 | **CONFIRMED** | `tools/goal.go`：`Goal` 仅 objective/steps/progress/status/updated_at（`:32-38`），action 仅 set/get/complete/abandon（`:54-57,161-233`）；`/goal` 不在 `shell/composer_slash.go` 命令表（grep 无命中）。DSH `dsh-tool-goal` 有 create/update/get + revision/phase。 | 仍成立（`/goal` 有意未做已被 known-limitations 记录，报告已注明 KNOWN 部分） |
| FP-05 | P1 | **CONFIRMED** | `grep -rn 'git diff\|TurnDiff\|WorkspaceChanges\|workspace/changes' internal/` 无命中；DSH `dsh-workspace-changes/README.zh.md` 存在且描述每回合 diff/`workspace/changes` 事件。 | 仍成立 |
| FP-06 | P1 | **CONFIRMED**（严重度可议：能力扩展差距，建议放功能对齐章） | `runtime/modes.go:112-113` `Modes()` 固定返回 4 个；`minimalToolNames` 硬编码；DSH `dsh-agent-preset/README.zh.md`（YAML 多 preset、会话选择）+ `dsh-agent-preset-registry`/`dsh-persona` 存在。 | 仍成立 |
| AR-01 | P1 | **CONFIRMED**（证据表述小误） | `grep -rn 'recover()' internal cmd` = **1 处，在 `internal/icons/icons_test.go:97`（测试）**，生产代码 0 处（报告写"无输出"不准确）；`log.SetOutput` 0 处；`cmd/zeokeel/main.go:12-17` 仅 `log.Fatal`；`scripts/build.sh:92` `-H windowsgui`。 | 仍成立 |
| AR-02 | P1 | **CONFIRMED** | `.github/`、`.gitlab-ci.yml`、`Jenkinsfile` 均不存在；`scripts/ci.sh:51` race 仅 6 包（state/store/session/agentteam/tools/runtime，无 shell/app/prompt/mcp）；`ci.sh:42-43` coverage 仅 `sort|tail -20` 无阈值；`scripts/evolve/gates.sh:12,84` 测试失败自动重跑最多 3 次并记 flaky。 | 仍成立 |
| AR-03 | P1 | **CONFIRMED** | `dist/` 实测：linux-amd64、windows GUI/console、zip、SHA256SUMS，无安装包/签名/更新；无 `-ldflags -X` 版本注入（`build.sh:64,92,94`）；`i18n.go:45` "version 0.1.0" vs `README.md:67`/`STATUS.md:58` v1.1；`git tag` 仅 `pre-native-ui`。**体积独立复测**：dist=49,534,307B，同源 `-s -w`=33,751,303B（-31.8%，与报告数字完全一致）。 | 仍成立 |
| 反向-1 auto-review | — | **CONFIRMED（旧文档过时）** | `runtime/review.go:1-80`（LLMToolReviewer、15s 超时、DENY 回退人工审批）；`services.go:857-884` 在 `PermissionAutoReview` 时构造 reviewer+reviewContext；`go test -run AutoReview ./internal/session ./internal/runtime` ok；`docs/FUNCTIONAL-PARITY.md:13` 仍写 "Auto-review 缺失"。 | 结论成立；旧文档未随 HEAD 更新 |
| 反向-2 goal round driver | — | **CONFIRMED（旧文档过时）** | `session/goal_driver.go`（`DefaultGoalRounds=6`、`MaxGoalRounds=12`）；`app/services.go:123` `GoalDriver: session.NewGoalRoundDriver()`；`controller.go:1543-1553` 每回合终态触发；`go test -run Goal ./internal/session` ok；旧文档 `FUNCTIONAL-PARITY.md:18,38` 说缺失。**报告引用 brief:177 有误，实际 `00-brief.md:184`**。 | 结论成立；brief 假设必须更正 |
| 反向-3 plan mode | — | **CONFIRMED（旧文档过时）** | `tools/exit_plan_mode.go` 存在且经 `app` 注册（`internal/app/exit_plan_mode_test.go` 断言 ToolByName 可查、minimal 模式不暴露）；`shell/plan_ui.go:31-33,82-86,259-270,327-350,427` Keep planning 反馈 + pending 徽章；`go test -run 'TestExitPlanMode|TestPlanMode' ./internal/app ./internal/shell` ok；`known-limitations.md:28` 仍写"尚未做"。 | 结论成立；与 AR-13 重复 |
| RT-09 | P2 | **CONFIRMED** | `automation/scheduler.go:136` `now = now.UTC()`；探针重跑：`@daily` next=`2026-01-01 08:00 +0800`（UTC 午夜），本地午夜应为 `2026-01-02 00:00 +0800`（-16h）。 | **仍存在**。HEAD:136 |
| RT-11 | P2 | **CONFIRMED**（需目录放宽才可利用，报告已注明） | 重跑探针：目录 755 时 `index.sqlite=-rw-r--r--`、wal/shm 同；`store.Open` 只 `MkdirAll(dir,0700)`，无 Chmod（`store.go:78` 附近；transcript 显式 0600 反而暴露不一致）。 | **仍存在** |
| RT-12 | P2 | **CONFIRMED（≡AR-11，需合并）** | `app.go:115` `net.Listen("tcp","127.0.0.1:49731")` 失败即 `ErrAlreadyRunning`；独立复现：python 先 bind 49731 → 启动 app 输出 `ZeoKeel is already running` 并退出（exit 0）。 | **仍存在**；与 AR-11 同一条，master 去重 |
| FP-08 | P2 | **CONFIRMED** | `tools/fs.go:104` `os.WriteFile(path,…,0o644)`（非 tmp+rename）；`edit_file` 无 read-before-write 强制（仅描述建议）；DSH `dsh-atomic-write`、`dsh-fs-observation-policy` README 存在且语义相符。 | 仍成立 |
| FP-12 | P2 | **CONFIRMED**（重试段与 RT-08 重复） | 仅 `internal/provider/openai` 一个 provider；`provider.api_key` 明文存 settings（`store/model.go:48,411`）；`inspect.go:147-152,328` 只按 key 名 redact；shell 仅 key 输入框遮挡（`models_ui.go:261`、`plugin_details.go:162`）；DSH `dsh-llm-retry`/`dsh-llm-pi-ai`/`dsh-client-ui-secret-mask` 存在。 | 仍成立（provider 6 文件于 HEAD 有改动但不影响结论；retry 仍无） |
| FP-13 | P2 | **CONFIRMED** | `mcp/mcp.go:5-8` 包注释"stdio"、`:62-68` `ServerConfig` 仅 Name/Command/Args/Env；`:189` `sdk.CommandTransport`；DSH `dsh-mcp-client/README.zh.md` transport=`stdio|streamable-http`。 | 仍成立（HEAD 同） |
| AR-04 | P2 | **CONFIRMED** | `wc -l internal/shell/*.go`=41,420（与报告一致）；`grep -c 'a\.Ctrl\.' internal/shell`（非测试）=**225**（一致）；生产直接 import 内部包 10 个（automation/brand/design/icons/runtime/session/state/store/tools/winproc）。 | 仍成立 |
| AR-05 | P2 | **CONFIRMED** | `state.NewProjector` 两处创建：`app/services.go:112`、`session/controller.go:299`；`Controller.Snapshot()`（`controller.go:1655-1673`）先返回 `views[currentID]`，仅 `currentID==""` 才回退 projector；`applyLocked` 内 `c.views[...] = &next` 二次折叠（`:1050-1057`）。 | 仍成立 |
| AR-13 | P2 | **CONFIRMED（≡FP-20 第 3 条，需合并）** | `known-limitations.md:28` 与代码相反（同反向-3 证据）；HEAD `known-limitations.md` 仅新增超时说明（+1 行），**该段未改**。 | 仍存在 |
| AR-18 | P2 | **CONFIRMED（独立运行复现）** | `app.go:182-183` 默认 1600×1000/最小 900×600；Xvfb 1280×800 首启 `window.json` 仍写 `1600×1000`（无 clamp，A 例）；预置 `{x:100,y:80,w:3000,h:2000}` 后 `xwininfo` 显示窗口 **3000x2000+100+80**（部分可见即原样恢复，C 例）；mygo `window_state.go` `onDisplay` 阈值 100×50、真值分支不按 WorkArea 裁剪。 | 仍成立。属框架行为+默认值取舍，P2 可保留（P3 亦可议） |

## 3. 最终可信 P0/P1 清单（建议 master 采纳）

- **P0（1 条）**：**RT-01** 工作区 `mcp.json` 在 agent 构建期无审批执行任意命令（已独立复现；HEAD 仍存在）。修复优先级最高，S 级止血=工作区 mcp.json 默认不读/显式开关+启动失败可见。
- **P1（12 条，采纳）**：RT-02（权限重建缺失；利用链依赖 automation 与 Full 会话，报告已自标部分未验证）、RT-04（全局 turn 串行；审批等待占锁，架构限制）、RT-07（spill 跟随 symlink 写出工作区）、RT-08（429/5xx 零重试，失败回合丢历史）、FP-01（workflow 固定步骤 vs DSH JS 编排）、FP-03（subagent 仅同步、无 fork/续谈）、FP-04（goal 工具缺 id/revision/phase/pause/blocked；`/goal` 有意未做已注明）、FP-05（缺 workspace changes/回合 diff）、AR-01（发布版无崩溃诊断）、AR-02（无托管 CI/门禁可 flaky）、AR-03（发布链路缺失、版本未注入）、RT-05(a)（Team 成员对 gated 工具必然失败且用户不可见；仅 (a)）。
- **降级/重构建议（不进入 P1 缺陷清单）**：RT-03 → P2 或并入 RT-02 作为利用链；FP-02 → P2/产品边界（有意不嵌入 JS 沙箱）；FP-06 → 功能对齐章（能力扩展差距，P1 可议）；AR-02 严重度可议（流程问题，非运行时缺陷）；AR-18 可 P2/P3。
- **已修复**：RT-06 在 HEAD `3111614` 修复（默认无整体超时 + `provider.timeout_seconds`），master 应移至"复核期间已修复"并保留回归测试（慢流 >60s）。
- **文档类反向结论（3 条，非缺陷但必须更正）**：auto-review、goal round driver、plan mode 均已实现并接线；需改 `FUNCTIONAL-PARITY.md:13,18,38`、`known-limitations.md:28`、brief §7"goal round driver 缺失"（实际行 184，非 177）。

## 4. 对 master 报告的具体建议（采纳/降级/删除）

1. **采纳原样**：RT-01（P0）、RT-02、RT-04、RT-07、RT-08、FP-01、FP-03、FP-04、FP-05、AR-01、AR-03；RT-05 仅保留 (a) 并改写标题。
2. **降级/改类**：RT-03→P2（或并入 RT-02）；FP-02→P2 或"产品边界"；FP-06→"功能对齐差距"；AR-02 标注"流程/质量"；AR-18 可降 P3。
3. **状态更新**：RT-06 改"已在 HEAD 3111614 修复"；报告 02 头部"并发提交不改变本报告任何结论"的说法已过时（3111614 恰好只改 RT-06 所在文件 provider/openai）。
4. **合并去重**：RT-12 与 AR-11 合并；FP-20（3 条文档漂移）与 AR-13、AR-15 合并为一条"旧文档把已实现写成缺失"；FP-12 的"无重试"与 RT-08 合并；AR-17 的 race 覆盖段与 AR-02 合并。
5. **修正引用**：FP-20 的 `00-brief.md:177` → `:184`；AR-01 的"全仓 recover() 0 次"→"生产 0 次、测试 1 次（icons_test.go:97）"；RT-06 行号以修复后 HEAD 为准。
6. **删除**：无整条需删除项（0 REFUTED）；唯一空转证据是 RT-02 探针的 rebuild 计数断言，建议换成 `submit` 静态调用点引用或更强探针。

## 5. 未验证 / 残余风险（明确未做）

1. RT-01 GUI 端到端（Add workspace → rebuild 执行命令）未跑；已做 handler 级探针 + `app.go:275`/`controller.go:2854,3274,3304` 构建调用链静态核对。
2. RT-02 完整利用链（Full 会话→创建 automation→触发→无审批执行）未做 e2e；auditor 探针断言空转，结论靠静态调用点。
3. RT-05(b) child agent 并发数据竞争未证实：`-race` 复跑无告警，eino 内部是否真会串扰未验证；不应计入 P1 影响。
4. RT-06 修复仅在 HEAD 静态/提交信息核对，未跑 >60s 慢流回归；RT-06 原 60s 行为在 27d5d5f 可信。
5. RT-04(b) 后台审批 UI 是否可见未做 GUI e2e（锁语义本身代码可证）。
6. AR-02 未实际执行 `scripts/ci.sh`（只读脚本核对）；AR-03 安装包/签名/更新缺失为静态结论。
7. AR-18 B 例（完全离屏 clamp）未独立复跑；A/C 例已独立复现。
8. 抽样范围外 P2 未复核：RT-10/13/14、FP-07/09/10/11/14/15、AR-06..AR-12/14..17/19..22 等；DSH 侧仅依据已安装 README，未读其源码。

## 6. 复核产物

- `.cache/audit/verify/probes-rerun.log`：冻结快照内 10 个 auditor 探针重跑结果（全部 PASS，9 条打印 CONFIRMED/复现事实）。
- `.cache/audit/verify/head-evidence.log`：HEAD 3111614 的 diff、关键符号行号、trust grep、RT-06 修复证据。
- `.cache/audit/verify/runtime-repro.log`：AR-18 A/C 例、RT-12 端口抢占、AR-03 体积、反向校验测试、RT-05 -race 复跑。
- 冻结副本：`/tmp/verify-core`（27d5d5f）。
