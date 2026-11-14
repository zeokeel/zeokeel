# ZeoKeel 长时任务现状实测基线（v0.2.35，代码+测试级）

> 对象：`/data/mygo` = ZeoKeel **v0.2.35**（`VERSION`），依赖 `zeokeel-rig v0.16.2`、`zeokeel-kernel v0.1.1`（`go.mod:7-8`）。
> 方式：以「长时任务 / long-horizon task」为主轴，按 D1–D10 逐维核查**当前真实状态**；CHANGELOG/README/docs 只作线索，结论以 `v0.2.35` 代码与测试为准。
> 证据约定：`file:line` 相对 `/data/mygo`；rig 源码相对 `$RIG=/data/mygo/.cache/gopath/pkg/mod/github.com/zeokeel/zeokeel-rig@v0.16.2`。判定级别：**[实测]** 可复现测试（§附录A 命令）/ **[源码]** file:line / **[判断]**（推断，显式标注）。运行环境见 §附录A。
> 口径：`模型可见` = 注册进工具 registry 的工具名；`用户可见` = UI 入口；`引擎内部` = 无模型/用户直接入口的机制。

---

## 0. 一页结论（TL;DR）

ZeoKeel 的「长时能力」不是一套统一的 run 生命周期，而是**三条互不相通的轨道**拼起来的：

1. **目标续跑（D1）是一条只存在于桌面 UI 模块的短链**：`goal` 工具设置**进程级单例目标**（无 sessionID、无持久化），桌面会话屏在每次 run 结算后自动向**当前活动会话**再发一条「请继续推进目标」用户消息，**同一 goal 最多 5 次**且**静默停止**。headless/ACP 产品完全没有这条链（无 UI 模块、无 `KeyGoalTracker`）。rig v0.16.2 已提供 DSH 同名的 `get_goal/create_goal/update_goal`（revision CAS、`blocked` 相位、默认 256 轮），但产品**按名字过滤掉、不注册**（分类①）。
2. **循环本身几乎没有预算（D2）**：主 run 的 `maxIterations` 默认 0 → 被映射成 **1 000 000** 轮；自动压缩/手动 `/compact` 依赖 `contextBudget`，而默认 **0 = 关闭**（手动压缩直接 `ErrCompactUnavailable`）；loop guard 只是 advisory（3/5/8 次相同调用提醒，从不阻塞）。**子代理/团队成员默认反而只有 rig 的 20 轮**（父 spec 的 0 被 rig 解释为默认 20）——委派比主 run 先死。
3. **持久化分层不齐（D3）**：会话/消息/事件/checkpoint、自动化任务、团队任务板、子会话**有落盘**；**目标、排队消息、后台 job、终端会话、todo 列表、审批队列、goal 续跑计数**全部**进程内**。后台 job/终端在**工作区切换**时就会被杀死（旧 build retire `CloseAll`），不只是重启。
4. **无人值守（D6）只有「进程活着 + 桌面窗口在」这一种形态**：automation 是进程内 500ms ticker，退进程即停；没有 daemon/service；自动化 run 的「成功/失败」只记录 **`chat.Send` 是否被接受**，不记录 run 的真实结果；每次 run 新建一个会话且**永不清理**。
5. **可观测有产品面但缺主动通知（D8）**：运行带、侧栏等待/运行/未读、job/团队/子代理 popover、用量状态行都在；通知只有**应用内 toast**，后台会话/自动化完成或失败**没有系统通知**，也没有任何自动化完成 toast（PM-10 **仍成立**）。
6. **失败处理是「分类 + 手动重试」（D10）**：无自动 retry/backoff、无模型 fallback；失败 turn 有本地化分类和「重试」（=Regenerate）；崩溃/被杀的半成品靠 rig checkpoint + 下一次 run 的 `RecoverInterruptedTurn` 收尾（标记「上一回合被中断，未重放」），不是自动续跑。

对最终对比最有价值的是三类事实（§三类事实汇总）：**rig 有能力而产品没注册**（DSH goal 三件套、ralph、workflow、review/PTC/inspect 等）、**UI 有入口而后端语义不足**（自动化运行历史、无人值守审批挂起、排队仅内存、goal 静默上限）、**单例/不持久/生命周期缺陷**（goal 宿主级单例、job 随工作区切换死亡、自动化会话泄漏、headless 无续跑）。

---

## 0.5 已知限制核实（CHANGELOG v0.2.35 附近逐条回代码）

CHANGELOG「已知限制（v0.2.24 明确 Deferred）」（`CHANGELOG.md:251-260`）与 A 系列固定行为（`CHANGELOG.md:182`）相关条目核实如下：

| 线索（原文位置） | 原文摘要 | 核查结论 | 证据 |
|---|---|---|---|
| **FR-28**（`CHANGELOG.md:253`） | 「目标（goal）仍是宿主级单例：跨会话显示/自动续跑可能落到当前会话、重启不持久」 | **仍成立（三个症状全部复现于代码）** | ①单例：`uiroot/uiroot.go:342` 每进程只建一个 `newGoalService()`，`contracts.Goal` 无 SessionID 字段（`contracts/goal.go:11-17`）；②跨会话显示：会话屏把 tracker 回调直接写进自己的 `moduleState.goal`，dock 每个会话都渲染同一目标（`ui/session/session.go:512-541`、`ui/session/view.go:586`）；③续跑落到当前会话：`maybeAutoContinue` 用 `chat.ActiveSession()` 而不是目标所属会话（`ui/session/session.go:1313-1330`）；④不持久：`GoalTracker` 无任何 settings/存储键（全仓 grep `goal` 于 `internal/modules/sessionstore`、`internal/host` 仅命中 host 组装代码），重启后 `newGoalService()` 为空。 |
| **PM-10**（`CHANGELOG.md:257`） | 「后台会话/自动化完成或失败无系统通知」 | **仍成立，且比原文更彻底**：全仓没有任何 OS 通知调用（无 dbus/notify-send/osascript）；`contracts.Notifier` 只连到应用内 mygo toast（`uiroot/notifier.go`、`ui/toasts/toasts.go`）；`internal/modules`/`internal/host` 内**没有一处** `Notify*` 调用（grep 为空），即后台会话完成不给 toast、自动化完成/失败也不给 toast；自动化模块自身只有运行历史行（`internal/ui/automation/view.go`）。子代理完成有**会话内 durable notice 行**（`modules/agent/child_notices.go`）+ 侧栏未读点，但那是 transcript，不是通知。 | `internal/contracts/notify.go:5-40`（只有 4 个 level 的 toast 结构）、`uiroot/notifier.go`、`internal/ui/toasts/toasts.go:211-227`；grep `NotifySuccess|NotifyInfo|NotifyWarning|NotifyError` 于 `internal/modules`+`internal/host` = 0 命中 |
| **A5**（`CHANGELOG.md:182`） | 「目标始终自动续跑（每个目标上限 5 次）」 | **部分成立**：桌面成立（不可关闭，5 次上限）；**headless/ACP 完全不成立**（无 UI 模块、`KeyGoalTracker` 只在有 uiRoot 时提供：`host/host.go:681`, `:711`，headless 落到 `MemoryGoals`：`hosttools/hosttools.go:147-160`，没有任何续跑驱动）；且 5 次上限**没有任何用户提示**（`maybeAutoContinue` 达上限直接 return，无 toast/错误行，`ui/session/session.go:1324-1326`）。 | 同上；另 `ui/session/goal_autocontinue_test.go:130 TestGoalAutoContinueStopsAfterFive` [实测 PASS] |
| FR-12 Fork 部分（`CHANGELOG.md:260`） | Fork 仍复制「用户可见 transcript」 | **仍成立**：`ForkSession` 只复制 `user/assistant` 消息（工具调用丢弃、压缩不感知），见 `modules/agent/agent.go:1434-1510`（`forkHistoryMessages` 仅取两种角色）。 | 源码 + `TestForkSessionCopiesMessages` |
| M-10 记忆跨进程锁（`CHANGELOG.md:258`） | 未实现跨进程文件锁 | 与长任务弱相关；未复核（不属本报告主轴） | 待确认 |

**结论**：FR-28 与 PM-10 在 v0.2.35 **均未修复**；A5 的「目标自动续跑」只在桌面且带一个不可见的 5 次硬上限。CHANGELOG 里「已修复」相关表述（如审批归属 FR-29）与代码一致，可作为其余维度的可信索引。

---

## D1 目标与续跑（Goal & continuation）

**结论**：半成品，且是**桌面 UI 层的一段 60 行逻辑**，不是引擎能力。契约有 tracker/pauser/lifecycler 三档，模型工具只有 rig 旧版 `goal`；DSH 三件套未注册；目标不绑定会话、不持久、续跑上限 5 且静默。

**机制与位置**

| 角色 | 实现 | 证据 |
|---|---|---|
| 契约 | `contracts.Goal`（ID/Text/Status，**无 SessionID**）、`GoalTracker`、可选 `GoalPauser`/`GoalLifecycler`；4 个状态 active/paused/completed/abandoned | `internal/contracts/goal.go:11-68` |
| 宿主单例 | `Root` 每进程建一个 `goalService`，内存 `*Goal` + 原子 ID 计数，无落盘 | `internal/uiroot/uiroot.go:342`、`internal/uiroot/goal.go:32-211`（`GoalTracker()` 在 `:211`） |
| 模型工具 | 仅注册 rig 旧版 `goal`（`action: set|get|clear|update`，update 支持 complete/pause/resume/edit；`blocked` 返回可读错误） | `internal/modules/hosttools/tools.go:42`、`internal/modules/hosttools/module.go:141-215` |
| DSH 三件套 | **未注册**：rig 提供 `get_goal/create_goal/update_goal`（revision CAS、phases active/paused/blocked/complete、`roundsStarted/maxGoalRounds`、activation armed/disarmed，默认 256 轮），产品按名字过滤，且产品 bridge 不实现 `GoalRevisionStore` | rig `tools/hosttools/goal_dsh.go:17-22,63-100`（`DefaultMaxGoalRounds=256`）；产品过滤名单 `internal/modules/hosttools/tools.go:8-12,42` |
| 自动续跑 | 桌面会话模块在 `chat.Subscribe` 回调与帧兜底里观察「活动会话 running→idle」，每 settle 发一条 `chat.Send(activeSessionID, "请继续推进目标：<text>\n\n完成后请更新或清除目标。")` | `internal/ui/session/session.go:1244-1345`（`observeRunSettled`:1244、`handleRunSettled`:1272、`maybeAutoContinue`:1295、消息文案:1330）；调用点 `session.go:475`、`view.go:71-79` |
| 上限 | `maxGoalAutoContinues = 5`，**按 goal ID 计数**，goal 变化重置、clear 归零，**发送失败也计数**（防死循环），暂停/完成/放弃跳过；达到上限**静默 return** | `session.go:1230,1310-1326`；`TestGoalAutoContinueStopsAfterFive`、`TestGoalAutoContinueResetsOnGoalChangeAndStopsWhenCleared` [实测 PASS] |
| 用户可见 | composer 上方 goal 条（状态 badge + 文本 + hover 清除 + ⋯ 菜单：暂停/继续、完成、结束保留记录、删除）；命令面板「设置目标 / 完成目标 / 放弃目标」；`/goal` 对话框（编辑保 ID） | `ui/session/view.go:586-760`（`buildGoalDock`:586、菜单:660-720）、`ui/session/session.go:804-860`、`ui/session/command_dialogs.go` |
| headless/ACP | 无 UI 模块 → **无续跑**；hosttools 的 Goals 落到进程内 `MemoryGoals`（Set/Get/Clear，单目标），模型可设但无人推进 | `internal/host/host.go:681,711`；`internal/hosttools/hosttools.go:140-153`；`internal/hosttools/memory.go:208-266` |

**默认值与上限（数字）**：每进程 1 个 goal；每会话可见同 1 个；续跑 5 次/goal；续跑间隔=一次 run 结算；无轮数/时间/token 上限；无持久化；无 revision/CAS；无 `blocked` 相位。

**测试**（均 [实测] PASS，见 §附录A-2/3）：`TestGoalAutoContinueAlwaysOn`、`TestGoalAutoContinueSendsOnceWithGoalText`、`TestGoalAutoContinueStopsAfterFive`、`TestGoalAutoContinueResetsOnGoalChangeAndStopsWhenCleared`、`TestGoalAutoContinueSendFailureShowsDanger`、`TestGoalAutoContinueSkipsPausedGoal`、`TestGoalAutoContinueSkipsCompletedAndAbandoned`、`TestGoalChipShowsAndClears`、`TestGoalChipLifecycle*`、`TestGoalPauseResumeChipAndActions`、`TestGoalLifecycleCommands*`；regression：`TestGoalAutoContinueBoundedAndReset`、`TestGoalPauseResumeStateMachine`、`TestGoalLifecycleStateMachine` 等。

**缺口（三类）**
- ① rig 能力未暴露：DSH goal 三件套（revision CAS、blocked、roundsStarted/maxGoalRounds 256、activation）全部缺失；模型只能看到旧 `goal`。
- ② UI 承诺后端不对齐：dock/文案（`command_dialogs.go`「为当前会话设置/清除长期目标」）与「全局单例」事实不符；5 次上限无提示。
- ③ 缺陷：宿主单例（FR-28）、无持久化、续跑目标会话错位（`ActiveSession`）、仅桌面有续跑、续跑 prompt 以普通 user 消息进 transcript（用户会看到自己没打过的「请继续推进目标…」）。

---

## D2 循环与预算（Loop & budget）

**结论**：主 run **实质无上限**；压缩预算默认关闭；停止/排队可用但排队只在 UI 内存；有 advisory loop guard；无全局 run 并发上限、无 run 超时。子代理/团队成员的隐藏轮数上限（20）比主 run 低得多。

**机制与数字**

| 项 | 现状 | 证据 |
|---|---|---|
| 主 run 迭代上限 | 设置 `chat.maxIterations` 留空/0 → 模块默认 0 → `effectiveMaxIterations` 映射为 **1_000_000**（rig 的 0 会被解释成 20，故产品显式抬高） | `modules/agent/agent.go:46-58,1161,2216,2227`；设置页可选输入「留空使用模块默认」`ui/settings/settings.go:1358-1395` |
| rig 默认 | `DefaultMaxIterations = 20`（spec/RunRequest 都未配置时） | `$RIG/agent/agent.go:83-85,123-131,180-186` |
| 上下文预算 | `contextBudget` 模块默认 **0 = 不启用自动压缩**；桌面装配未设置（`internal/assembly/assemblies/desktop.json` agent config 只有 model/requireFinishReason）；设置页留空即 0 | `modules/agent/agent.go:119-121,1162,2243-2248` |
| 停止 | `Cancel(sessionID)` 取消该 run context；rig 中止工具轮、合成取消；UI「停止」/Esc/`Cmd+.`；停止时抑制 goal 续跑并暂停排队 | `modules/agent/agent.go:2887-2900`；`ui/session/session.go:897-922`、`ui/session/session.go:913`（`queuePaused`） |
| Loop guard | 仅 advisory：同一 `(tool, 规范化参数)` 连续第 3/5/8 次时在结果尾部追加提醒，**从不阻塞/延迟/失败**；`todo_write/todo` 透明 | `modules/agent/loop_guard.go:27-97,180-200`；`TestRepeatToolGuardThresholds` [实测 PASS] |
| 会话排队 | 「运行中始终排队发送」（A4）：排队只在 **UI moduleState 内存**（`map[sessionID][]string`），**不持久化**；只排文字（附件留在输入框）；停止会 pause 队列；发送失败暂停并显示可重试错误行；未排队时 backend `Send` 直接返回 `ErrSessionRunning` | `ui/session/session.go:1105-1225`（`queueDraft`:1114、`processQueuedPrompts`:1193）；`modules/agent/agent.go:1786-1793`（running 拒绝） |
| 并发 | 无全局 run 并发上限；不同会话可并行（`TestConcurrentSessions`）；同一会话串行 | `modules/agent/agent.go` run 生命周期；`TestConcurrentSessions` |
| 超时 | 桌面 run 无截止时间；ACP `session/prompt` 有可选 `timeoutMs`（0=不限） | `modules/acp/acp.go:75` |
| 子代理/团队成员轮数 | 产品把 `cfg.MaxIterations`（默认 0）传给 rig runner，rig 子 agent 的 0 被视为默认 **20**（父 run 的 1e6 不会继承）→ 委派默认 **20 轮** | `modules/agent/subagent.go:150-170`（parent spec `MaxIterations: cfg.MaxIterations`）、`modules/agent/team.go:100-115`；rig `agent/subagent.go:255-266` |

**测试**：`TestRepeatToolGuard*`、`TestCancelKeepsPartialContent`、`TestCompactRejectsSendAndRegenerate`、`TestConcurrentSessions`、`TestSessionRunningConflict`（`TestSendValidationAndConflict`）[实测 PASS]。

**缺口**：② 排队重启即丢（UI 有提示但无持久）；③ 主 run 无预算、无超时，长任务只能靠模型自己停；子代理 20 轮隐藏上限与主 run 不一致（长任务委派会「提前放弃」）；loop guard 只提醒，不做硬熔断。

---

## D3 持久化与恢复（Persistence & recovery）

**结论**：**会话域持久化做得扎实，执行域几乎不持久**。SQLite 存会话/消息/事件/checkpoint；checkpoint 中断恢复由 rig 在下次 run 自动收尾。后台 job、终端、目标、排队、todo、审批队列全部进程内；后台 job/终端还会在**工作区切换**时被主动杀掉。

**机制与位置**

| 对象 | 是否持久 / 位置 | 证据 |
|---|---|---|
| 会话/消息/事件/checkpoint | SQLite（默认 dataDir 下 DB）4 张表 `sessions`/`messages`/`events`/`checkpoints`，事件表带 seq/type/payload/text | `internal/modules/sessionstore/store.go:123-157`；`TestPersistenceAcrossReopen`、`TestCheckpointStore`、`TestModulePersistenceAcrossRestart` [实测 PASS] |
| 重启恢复 | 启动 `restore()`：从 store 列会话、载入 rig history、把 durable `tool_call/tool_result` 事件回放到读模型（保留卡片时间线）、恢复失败 turn 的 error 与截断标记 | `modules/agent/agent.go:872-944`；`TestRestoreHydratesFailedTurn`、`TestRestoreAttachesErrorToPersistedPartialAssistant` |
| 崩溃/被杀恢复 | 产品给 run 装 `CheckpointStore`；rig 在「请求前/副作用工具前/完成」写检查点，下次 `Run` 开头调 `RecoverInterruptedTurn`：补齐「结果未知、未重放」的合成 tool 结果 + 追加 `The previous turn was interrupted: ZeoKeel exited before it finished…` 标记；产品在失败/停止时另持久化半截 assistant 文本 | `modules/agent/agent.go:2241,2307-2313,2617-2651`；rig `agent/agent.go:449`、`agent/checkpoint.go:20-72,114-232`；测试 `TestFinishPersistsPartialAssistantAndClearsCheckpoint`、`TestFinishDoesNotPersistToolCallTurn` |
| durable 事件类型 | `user_message`/`run_start`/`run_end`/`tool_call`/`tool_result`/`usage`/`session_refs`/`fork`；**没有** compaction、goal、queue、approval 事件 | `modules/agent/agent.go:1486,1875,1893,2042,2255,2400,2432,2564`、`usage.go:57` |
| 后台 job / 终端 | **进程内**：rig `jobs.Manager` 是内存 map；每个工作区根一个 build，**工作区切换即 retire 旧 build → `terminals.CloseAll()` + `background.CloseAll()`**（job/终端被杀，不可再访问） | `$RIG/tools/jobs/jobs.go:144-163`；`modules/toolkit/rebind.go:92-97,160-180,270-280`；`TestWorkspaceSwitchReleasesOldRootJobs`、`TestWorkspaceSwitchClosesRetiredBuild` [实测 PASS] |
| 自动化任务 | `automations.json`（原子写 + fsync + rename），含 `NextRun`(UTC)、`LastRun`、`LastError`、`RunHistory`（≤10）；损坏则隔离 `*.corrupt-<ts>` | `modules/automation/automation.go:39-43,246-370,780`；`TestCreateListUpdateDeleteAndPersistence`、`TestRunHistoryPersistsAndReloads` [实测 PASS] |
| 自动化「错过补跑」 | 重启后 tick 发现 `NextRun` 已过期 → **补跑一次**（不补 N 次），然后按 schedule 重排 | `automation.go:396-404,794-810`；`TestTickerRunRecordsHistory` |
| 自动化 run 会话 | 每次 run **新建**会话 `"自动化: <name>"`，记录 origin 供侧栏过滤；**从不删除/清理**（grep 无 DeleteSession/prune）→ 长期运行会线性堆积会话 | `automation.go:712-766`；`internal/contracts/automation.go:35-43`、`ui/sidebar/sidebar.go:1720,1801` |
| 子会话（subagent/teammate） | `child-sessions.json`（≤64 条、14 天 TTL，原子写）；**重启后是历史只读行**，teammate 也不可继续（`Historical` 拒绝 Send） | `modules/agent/child_sessions.go:31-54,136-218,497-520`；`modules/agent/childagents.go:101-121`；`TestChildNoticeStableAcrossRestart` |
| 团队任务板 | 每会话 `dataDir/team-tasks/<session>.json`（tombstone 删除、重放编号），上限 256 | `modules/toolkit/task_store.go:38-47,91-101`；rig `tools/team/tasks.go:46-47`（`DefaultMaxTasks=256`） |
| todo 列表 | 进程内单 `misc.TodoStore`（toolkit 建一份），**不落盘、不按会话隔离** | `modules/toolkit/toolkit.go:146`；`$RIG/tools/misc/todo.go:28-78` |
| goal | **不持久**（见 D1） | 无键可引 |

**测试**：[实测] `TestPersistenceAcrossReopen`、`TestCheckpointStore`、`TestWorkspaceSwitchReleasesOldRootJobs`、`TestAutomationRunNowRecordsPersistedHistory`、`TestChildNoticeStableAcrossRestart`、`TestDeletedRunningSessionIsNotResurrected` 等。

**缺口**：③ 执行域不持久（job/终端/目标/排队/todo/审批）；job/终端被工作区切换杀死是**产品主动设计**（rebind 注释明说防泄漏），但对长任务是硬伤；自动化会话泄漏；重启后无法续跑任何被中断的 **run**（只做「收尾+标记」，不重放、不自动续跑）。

---

## D4 上下文管理（Context & compaction）

**结论**：压缩链路齐全但**默认全关**：自动压缩/手动 `/compact` 都需要手工设置 `contextBudget`；自动压缩只压缩**本次 run 的内存对话**、不落盘；spill 用 rig 默认策略（产品只给目录/32KiB/100 文件）；用量状态行完整。

**机制与数字**

| 项 | 现状 | 证据 |
|---|---|---|
| 手动 `/compact` | `CompactNow`：要求会话 idle、`contextBudget>0`（否则 `ErrCompactUnavailable`）、有消息；LLMCompactor + `WindowCompactor` fallback；预算扣 system prompt；写回前重新 Load 校验前缀与并发追加（`ErrCompactConflict`）；**替换 durable history**；**不写任何 durable 事件** | `modules/agent/compact_now.go:31-160`；`TestCompactNowReplacesHistory`、`TestCompactNowUnavailableWithoutBudgetOrCompactor`、`TestCompactNowBudgetDeductsSystemPrompt` [实测 PASS] |
| UI 入口 | 命令面板「压缩上下文」（`zeokeel.session.compact`），in-flight 显示 loading，成功 toast「已压缩：N 条 → M 条（tokens）」 | `ui/session/session.go:796-803`、`ui/session/compact_command.go:31-110` |
| 自动压缩 | rig 每次模型调用前 `prepareContext()`：`EstimateTokens(conversation)+systemTokens > contextBudget` 时压缩为内存副本，**只影响后续请求，不写回 History**；产品仅在 `budget>0` 时安装 LLMCompactor | `$RIG/agent/agent.go:494-511`；`modules/agent/agent.go:2243-2248` |
| 默认 | `contextBudget=0` → 自动压缩关闭、手动压缩不可用；模型窗口只用于**显示**（模型目录 `contextWindow`） | `modules/agent/agent.go:1162`；`ui/session/view.go:3694-3713`（状态行）、`sessionContextWindow`（同文件） |
| tool spill | 产品只解析目录：`<workspace>/.zeokeel/tool-results`（headless 落 dataDir），阈值 **32 KiB**（rig 默认 16 KiB），保留 **100** 个文件；超阈值后模型只看到 rig 摘要（path/bytes/240B preview），同轮后续请求不再带全文 | `modules/agent/spill.go:24-64`；`$RIG/agent/spill.go:19-66` |
| 用量/上下文状态行 | composer 下一条静默状态行：轮数·步骤·输出速度·context/window·缓存命中·剩余上下文（精确值在 tooltip；无 usage 时整行隐藏）；`ContextTokens` = 最后一次模型调用的 prompt tokens（run 的 Usage 是累计计费值） | `ui/session/view.go:3627-3720+`；`modules/agent/usage.go:1-66`；`TestRunRecordsUsageOnAssistantMessage` |
| 记忆注入 | system prompt 注入 ≤2 KiB / ≤20 条 / 每条 ≤200 rune 的记忆摘要（global+本工作区，标注“用户持久记忆、仅上下文”），2s 读超时，失败只记日志 | `modules/agent/system_prompt.go:399-470` |
| 技能目录注入 | ≤2 KiB / ≤30 条（仅 name/description），正文按需 `skill` load | `system_prompt.go:407-420,475-510` |
| 截断/工具结果上限 | 读模型单工具摘要 4000 rune、参数 4000 rune；回答 finish_reason=length 标 `Truncated` 并 UI 提示 | `modules/agent/agent.go:72-82`、`finish_reason.go`；`TestFinishReasonLengthMarksTruncated` |

**测试**：[实测] `TestCompactNow*`（8 个）、`TestCompactRejectsSendAndRegenerate`、`TestCompactPreservesMessagesAppendedDuringCompaction`、`TestSpillPolicyUsesWorkspaceDir`、`TestRunRecordsUsageOnAssistantMessage`。

**缺口**：③ 自动压缩不落盘 → 长会话每次新 run 都从全量历史重新压缩（成本/首 token 延迟重复支付），且压缩结果不产生 durable 事件（审计/恢复不可见）；默认关闭意味着开箱状态没有上下文保护；手动压缩在默认配置下永远返回「不可用」。

---

## D5 编排（Orchestration）

**结论**：同步 subagent（深度 1、并发 2）+ 异步 team（深度 1、并发 4）+ 持久化任务板可用；**rig 的 ralph / workflow / review / PTC(inspect,report,code_review,auto_review,run_code) 全部未注册**——编排只有两档，没有长跑 fan-out。

**机制与上限**

| 能力 | 现状 | 证据 |
|---|---|---|
| subagent / subagent_fork | 模型工具 `subagent`、`subagent_fork`；**同步**（`runSubagent` 里 `sub.Wait(ctx)` 直到结束才返回）；深度 `subagentMaxDepth=1`、并发 `subagentMaxConcurrent=2`；子代理工具面 = 父会话 preset/mode/disabled 过滤后再剔除编排工具 | `modules/agent/subagent.go:26-60,95-200,240-295`；`TestRunSubagentDelegatesSynchronously`、`TestSubagentExcludedTools` |
| team | rig team manager 全量注册：`team_spawn/list/send/wait/last/close/interrupt` + 旧别名（`spawn_team/list_teams/team_status/close_team/wait_team/send_message/interrupt_agent/list_subagent_models`）+ DSH 名（`spawn_teammate/list_agents/wait_agent`）；**后台运行**；深度 1、并发 4；成员 run 生命周期被 tracker 记录成 activity 读模型 | `modules/agent/team.go:26-70,593-616`；`$RIG/tools/team/tools.go:28-56`、`dsh.go:14-19`、`legacy.go:10-32` |
| 任务板 | `team_task_create/list/get/update` 4 工具，按 run 会话隔离 + 落盘（见 D3）；UI 团队浮层「共享任务」段 | `modules/toolkit/task_board.go`、`task_store.go`；`ui/session/agentsurface.go:543+` |
| ralph | rig 有 `tools/ralph`（fresh-agent 轮次循环）；产品**无 import、未注册**（只在子代理排除名单与 UI 名称表里出现） | 全仓 grep `tools/ralph|ralph.New` = 0；`modules/agent/subagent.go:53`（仅排除）；`ui/uikit/toolnames.go:70-72`、`ui/toolcards/toolcards.go:75-76`（仅为显示名预留） |
| workflow | 同上：rig `tools/workflow` 未 import/未注册 | 同上 |
| 其他未注册 rig 工具 | `present_artifact`、`code_review`、`auto_review`、`run_code`(PTC)、`inspect`、`report`；产品只注册 `present`（内容 artifact）与会话查询 5 件套（`session_search/trace/event_*`） | `internal/modules/hosttools/tools.go:8-12`（明确只注册 5 个 host tool）；`internal/modules/agent/agent.go:195-199`（session 工具来自 `rigsession.Tools(store)`，只 5 个）；grep `tools/workflow|tools/ralph|ptc|code_review|auto_review` = 0 命中 |
| 并发/后台 | 不同会话可并行；team 成员后台跑；同步 subagent 会**占住当前 run** 直到子代理结束（长任务里等于串行） | `modules/agent/subagent.go:180-200` |
| 子代理/成员模型 | 可 per-delegation 覆盖模型（按模型目录连接） | `subagent.go:106-120`、`team.go:96-115` |

**测试**：[实测] `TestChildAgentsTeammateMappingAndActivity`、`TestSendChildAgentRoutesToTeamAndRejectsOneShot`、`TestRunSubagentDelegatesSynchronously`、`TestTeamSpawnWaitAndMembers`、`TestTeamToolsRegisteredAndDeduplicated`、`TestTaskBoardCreateListStatusDelete`、`TestSubagentToolsRegisteredAndInvokable`。

**缺口**：① ralph/workflow/review/PTC/inspect/report 是现成 rig 能力，产品没暴露（长任务缺少「脚本化 fan-out」和「独立评审」两档）；③ 子代理同步阻塞主 run、成员重启后不可续、成员默认 20 轮（见 D2）。

---

## D6 调度与无人值守（Scheduling & unattended）

**结论**：有**进程内**调度器（持久任务 + 到期补跑一次 + 运行历史），但没有 daemon/服务形态；退进程即停；「无人值守」只在 headless（审批 auto）或桌面开「所有权限」时成立；自动化 run 的成败判定是假的（只记 Send）。

**机制与数字**

| 项 | 现状 | 证据 |
|---|---|---|
| 调度器 | automation 模块：500ms ticker（`DefaultTick`），任务 JSON 持久化；`Close` 停止 ticker 并等在跑的执行写完 | `modules/automation/automation.go:39-43,380-412,758-790`；`TestCloseStopsTicker` |
| 支持表达式 | `*/N * * * *`（N=1..59）、`M H * * *`（每日）、`@hourly`、`@daily`、`every <N>s`、`after <N>s`（一次后自动禁用）；其它 cron 形式拒绝 | `modules/automation/schedule.go:8-14,60-72`；`TestParseSchedule`、`TestParseScheduleRejectsInvalid` |
| 每次 run | 新建会话（`"自动化: <name>"`）→ 记录 origin → 应用 task 的 workspace/permission/model → `chat.Send`；**Send 返回即记 success**（`AutomationRunSuccess` 注释原文「marks a run whose prompt was sent successfully」）；模型/工具后续失败不在 run history 里 | `automation.go:658-760`（`:753` Send）；`contracts/automation.go:58-75`；`TestRunNowRecordsErrorHistoryAndTruncatesSummary`（只验证 send 错误）[实测 PASS] |
| 运行历史 | 每任务 ≤10 条（startedAt/finishedAt/status/summary≤200 rune），newest-first，持久化 | `automation.go:780-792`；`TestRunHistoryCappedAtTenNewestFirst` |
| 重叠 | 同一任务 in-flight 折叠（不会并行两份）；不同任务可并行；**无 run 超时**（Send 内部 run 卡住不阻塞下次调度，但会留下 running 会话） | `automation.go:396-412,658-672` |
| 进程退出 | ticker 随模块 unload 停止；桌面默认**关闭即退出**（close-to-tray 默认 off），可选 tray 隐藏（进程仍在） | `automation.go:370-378`；`host/tray.go:59-72`（默认 false） |
| 无人值守形态 | headless：同装配含 automation + ACP，跑到 SIGINT/EOF；审批 assembly mode = `auto`（全放行）；**无 goal 续跑**；ACP：`session/new(cwd)`、`session/list`、`session/load`（resume）、`session/prompt`（可选 timeoutMs）、`session/cancel` | `internal/assembly/assemblies/headless.json:18-21,67-78`；`host/host.go:286-360`（headless/ACP）；`modules/acp/acp.go:58-63,75` |
| 缺什么 | 无 systemd/launchd/OS service；无「关窗继续跑」默认；无进程重启后继续未完成 run；调度只触发**新一轮**，不会「接着上次跑」 | 全仓无 service/daemon 代码；`TestCloseStopsTicker` |

**测试**：[实测] `TestTickFiresDueTasks`、`TestRunNowAndAfterOneShot`、`TestExecutionErrorRecordedAndCleared`、`TestAutomationCloseWaitsForInFlightRunNow`、`TestACPSessionListRealFrames`、`TestACPSessionLoadRealFrames`。

**缺口**：② 自动化运行历史把「已接受」显示为「成功/失败」（UI 语义与事实不符）；③ 无守护形态、无 run 恢复、每次新会话且不清理、无 run 超时。

---

## D7 安全与审批（Safety & approval）

**结论**：权限模式 + OS 沙箱 + 审批队列都齐；OS 沙箱「始终开启、不可关、无 runner 时 fail closed」；但**无人值守审批没有兜底**：自动化任务默认继承全局权限，遇到需要审批的工具会**无限期阻塞**（无超时、无自动拒绝、无通知）。

**机制与数字**

| 项 | 现状 | 证据 |
|---|---|---|
| 权限模式 | UI 三档：只读 / 工作区可写（默认）/ 所有权限；`ask`/`never` 仅 headless assembly 策略，旧存储值归一到只读 | `modules/approvals/approvals.go:52-75,210-232`；`contracts/subagents.go:45-67`；`ui/settings/behavior.go:225-250` |
| 自动放行集合 | 只读集：read_file/list_dir/glob/grep/read_image/working_directory + 只读信息类（skill/todo/task 查询/terminal_list/job_list/output/wait/session_* /lsp 只读）；工作区集再加 write_file/edit_file/str_replace_editor + memory | `contracts/subagents.go:93-152` |
| gate 优先级 | plan mode > chat mode(minimal/creator) > 会话级 permission override > OS-sandbox shell 自动放行 > interaction 工具豁免 > 会话「总是允许」记忆 > approvals 队列 | `modules/agent/plan_mode.go:231-360` |
| OS 沙箱 | 始终开启；有 runner 时 shell 族（bash/pwsh/job/terminal）**自动放行**（OS profile 兜底）；无 runner 时 fail closed 拒跑 | `modules/sandbox/sandbox.go:7-8,97`；`plan_mode.go` gate 的 `sandboxShellAutoAllowed` 分支 |
| 审批队列 | 无超时阻塞，直到 `Resolve` 或 run context 取消；请求带 `SessionID`/`Source`（用户/自动化）；UI 显示归属并支持跨会话打开；「总是允许」按会话持久化 | `approvals.go:235-300,321-366`；`modules/agent/plan_mode.go:340-360`；`modules/agent/tool_permissions.go`；`TestAskBlocksThenResolve`、`TestApprovalsModeNeverAndAuto` |
| 自动化权限 | task 可选覆盖「只读/工作区可写/所有权限」，默认空 = 用全局；选「工作区可写」+ 需要审批的工具（shell/网络/MCP/settings 写）→ 无人应答时 run 永久等待；无「自动拒绝并记录」策略 | `contracts/automation.go:41-43`；`ui/automation/view.go:917-937`；`automation.go:730-750` |
| headless 审批 | assembly `auto` = 全放行；但 `agent.permission` settings 行**优先于 assembly**（同一 dataDir 的桌面写入会被 headless 读到）→ 非空非 full 时 headless 会进入无人应答的审批阻塞 [源码风险，待实测] | `approvals.go:210-232`；`headless.json:18-21` |
| 取消/终止 | 会话 Cancel、`job_kill`、`terminal_signals/close`、停止命令；审批等待中取消会把 pending 移除并标工具 cancelled | `approvals.go:280-300`；`modules/agent/agent.go:2887`；`TestCancelBeforeToolApprovalMarksToolCancelled` |
| 安全面提示 | 产品**关闭了 web_fetch 的 SSRF 地址拦截**（模型流量可访问 localhost/内网/169.254.169.254），只在信任模型流量的环境使用 | `CHANGELOG.md:147-155`（"`web_fetch` 不再拦截任何地址"） |

**测试**：[实测] `TestGatePlanModeWhitelist`、`TestGateModeSwitches`、`TestGateInteractionToolsRespectPermissionOverrides`、`TestGateInteractionToolsGlobalNeverDenies`、`TestCancelBeforeToolApprovalMarksToolCancelled`、`TestPermission*`、`TestApprovals*`、`TestAskBlocksThenResolve`。

**缺口**：② 自动化 UI 的三档权限与「无人值守会遇到需要审批的工具」之间没有护栏/提示；② headless 的 auto 会被 settings 覆盖（风险）；③ 审批无超时；web_fetch SSRF 防护关闭（无人值守更危险）。

---

## D8 可观测与控制（Observability & control）

**结论**：**过程可见性很好，结果通知很差**。运行带/侧栏状态/任务与子会话浮层/用量行/会话内 toast 都齐；唯一缺的是「人不在窗口前」时的通知通道（PM-10）。

**机制与位置**

| 面 | 现状 | 证据 |
|---|---|---|
| 会话头运行带 | 运行中 badge + 会话运行数/团队数/子智能体数/后台 job 入口（零值常显可发现性） | `ui/session/agentsurface.go:237-300`（`buildAgentRunBand`:245） |
| 侧栏状态（v0.2.35 新） | 「等待回复」（已发未出字）vs「运行中」（spinner）vs「运行结束未查看」（未读点/加粗）；折叠工作区分组汇总；未读持久化在 `session.<id>.unread` | `CHANGELOG.md:7-20`；`contracts/chat.go:125-136`；`modules/agent/agent.go:1322-1365` |
| 后台 job popover | job 列表（状态/命令/时长/退出码）+ kill，经 rig `job_list`/`job_kill` 文本解析 | `ui/session/agentsurface.go:1069-1210`；`modules/toolkit/jobs_service.go:1-90` |
| 团队/子智能体浮层 | 成员列表、活动流、运行/完成/失败 badge、子会话切换、向 teammate 发消息 | `ui/session/agentsurface.go:543+`、`childagents.go` |
| 用量状态行 | 见 D4 | `ui/session/view.go:3694-3790` |
| 通知 | 仅**应用内 mygo toast**（可总开关；级别不过滤）；无 OS 通知；后台/自动化完成或失败**无 toast、无系统通知**；子代理完成有 transcript notice + 未读点 | `contracts/notify.go:1-40`；`ui/toasts/toasts.go:211-260`；`ui/settings/notifications.go`；grep 结论见 §0.5 PM-10 |
| 日志 | 进程日志文件 `dataDir/logs/zeokeel.log`；UI 无日志查看器；设置→数据可打开数据目录、导出诊断报告（只含应用信息+设置项，**不含日志内容**） | `host/logger.go:16`；`ui/settings/data.go:106-175,340-380` |
| goal 续跑可见性 | 只有失败错误行（「自动续跑发送失败：…」）；**达到 5 次上限、跨会话目标、headless 无续跑都无提示** | `ui/session/view.go:150-152`；`session.go:1324-1338` |

**测试**：[实测] `TestSessionRowShowsWaitingAndRunning`、`TestSessionRowUnreadReply`、`TestCollapsedWorkspaceGroupShowsChildState`（侧栏等待/运行/未读）、`TestRunBandShowsPendingApproval`、`TestJobsOverlayAndKill`、`TestTeamOverlayShowsMembersAndSharedTasks`、`TestTranscriptShowsChildCompletionNotice`、`TestGoalAutoContinueSendFailureShowsDanger`。

**缺口**：② 「通知」页只控制应用内气泡，与「完成/失败通知」的用户预期不符；② 自动化完成/失败在 UI 上没有主动提示（只能去自动化页看历史）；③ 日志无应用内查看/上传；goal 上限静默。

---

## D9 会话生命周期（Session lifecycle）

**结论**：会话域是产品最完整的一维：持久化、恢复、fork、搜索、导出、归档/置顶/未读、自动标题都在；缺「压缩感知的 fork」与「跨设备/跨进程并发编辑」。多会话并发运行可用。

**机制与位置**

| 能力 | 现状 | 证据 |
|---|---|---|
| 新建/改名/删除/置顶/归档/未读 | 全部有；删除用 tombstone 防止运行中会话复活；置顶/归档/未读存在 settings 行（不改 schema） | `modules/agent/agent.go:1238-1365,1595-1645`；`TestDeletedRunningSessionIsNotResurrected`、`TestSessionPinnedPersistsAndNotifies`、`TestSessionArchivedSurvivesRestore` |
| 自动标题 | 首条 prompt 截断 32 rune 作标题；空会话「未命名会话」 | `agent.go:71`；`TestAutoTitleTruncation` |
| 恢复 | 重启 restore（见 D3）；ACP `session/load` 可恢复 | `agent.go:872-944`；`acp.go:60,562-570` |
| Fork | 复制读模型 transcript（user/assistant + reasoning + attachments，**工具调用丢弃**）→ 新 ID、持久化、`fork` 事件；运行中的源会话只快照不取消 | `agent.go:1434-1510`；`TestForkSessionCopiesMessages`、`TestForkRunningSourceSnapshots` |
| 搜索 | 侧栏全文搜索消息体 + 标题（`SearchMessages`，snippet/limit）；模型侧另有 `session_search/trace/event_*` 5 工具 | `internal/modules/sessionstore/search.go:38-60`；`ui/sidebar/sidebar.go`（searchHits）；`agent.go:195-199` |
| 导出 | Markdown / JSONL（原生保存对话框 + toast） | `ui/session/view.go:469-473,772-810`、`ui/session/export.go:62-160` |
| 多会话并发 | 支持；同会话串行（Send 拒绝 running）；排队见 D2 | `TestConcurrentSessions` |
| 子会话 | 子代理/teammate 有独立子会话视图（只读历史），可向 teammate 追发消息（进程内） | `childagents.go`、`child_sessions.go`；`ui/session/agentsurface.go` |

**测试**：[实测] `TestSessionsOrderingAndDelete`、`TestFork*`（8 个）、`TestSearchMessages*`（10 个）、`TestExport*`、`TestConcurrentSessions`。

**缺口**：③ fork 不复制工具调用/压缩窗口（FR-12 deferred），副本上下文与源不一致；③ 子会话重启后只读不可续；无「会话级并发写保护」（同会话靠 running 标志，跨进程无文件锁——M-10）。

---

## D10 失败处理（Failure handling）

**结论**：**分类与手动重试做得好，自动化/无人值守链路几乎没有失败处理**。无自动 retry/backoff、无模型 fallback；失败要靠用户点「重试」；崩溃靠 checkpoint 收尾而非续跑。

**机制与位置**

| 项 | 现状 | 证据 |
|---|---|---|
| 错误分类 | `ClassifyError`：cancelled / auth / rate_limit / timeout / network / context / iterations / other；UI 本地化标题 + 恢复动作（模型设置/工作区/搜索设置） | `contracts/chat.go:206-250`；`ui/session/view.go:2046+`；`TestClassifyErrorTable` [实测 PASS] |
| 迭代上限 | rig `ErrMaxIterations` → `MessageErrorKindIterations`，文案「已达最大迭代次数」+ 打开模型设置 + 提高上限建议 | `agent.go:2500-2530`；`TestIterationCapUsesDedicatedErrorKind` |
| 截断 | finish_reason=length → `Truncated` + 可折叠「回答被截断」 | `finish_reason.go`；`TestFinishReasonLengthMarksTruncated` |
| 自动重试 | **无**：无 retry/backoff/jitter，无多次尝试；provider 流中断（`RequireCompleteStream` 始终开）直接判失败 | 全仓 grep `Retry|backoff` 于 `internal/modules` = 0 业务命中；`agent.go` 无重试路径 |
| 模型 fallback | **无**：一个 run 一个模型/连接；无「失败降级到备用模型」 | `agent.go:2186-2200`（单 provider） |
| 手动重试 | 失败 turn 保留「重试」→ `Regenerate`：重建 durable history 到该轮答案前（压缩感知：以 durable 为基底），重发最后一条 user 消息，不追加第二条 user 消息，重新展开 `@session` 引用 | `ui/session/view.go:1972+`；`agent.go:1927-2060`；`TestRegenerateUsesCompactedDurableHistory`、`TestRegenerateReExpandsSessionReferences` |
| 取消一致性 | 用户 Stop → error kind cancelled（中性），运行中工具标 cancelled 并清掉英文后端串；真实工具失败仍 error；finish 临界区内清 running，半截回答先持久化 | `agent.go:2478-2615`；`TestCancelBeforeToolApprovalMarksToolCancelled`、`TestGenuineToolErrorStaysError`、`TestFinishKeepsRunningUntilInterruptedTurnPersisted` |
| 半成品恢复 | 崩溃/被杀：checkpoint + `RecoverInterruptedTurn` 合成「结果未知」并追加中断标记（不重放工具副作用）；优雅停止不写 interrupted 标记 | `agent.go:2617-2660`；`$RIG/agent/checkpoint.go:20-72`；`TestFinishPersistsPartialAssistantAndClearsCheckpoint` |
| 限流 | 仅分类 + 文案「请求过于频繁，请稍后重试」；**无等待重试、无排队/退避** | `contracts/chat.go:253`；`agent.go` 无 429 处理路径 |
| 自动化失败 | 只有 Send 失败会进 run history/LastError；run 内失败（模型错、工具错、审批挂起）**不进自动化历史**；无重试、无告警 | D6 表；`contracts/automation.go:58-63` |

**测试**：[实测] `TestClassifyErrorTable`、`TestCancelKeepsPartialContent`、`TestIterationCapUsesDedicatedErrorKind`、`TestFinishReasonLengthMarksTruncated`、`TestRegenerate*`、`TestRestoreHydratesFailedTurn`。

**缺口**：③ 无自动重试/退避/fallback（长任务遇到 429/网络抖动只能等用户）；③ 无人值守链路无失败告警/重试（见 D6/D8）；② 自动化的失败语义只覆盖「Send 被拒」。

---

## 三类事实汇总（对最终结论最有价值）

### ① rig 有这个能力，但产品没注册 / 没暴露
1. **DSH goal 三件套** `get_goal` / `create_goal` / `update_goal`：rig `tools/hosttools/goal_dsh.go:17-22,63-100` 已实现 revision CAS、`active/paused/blocked/complete`、`roundsStarted/maxGoalRounds`（默认 **256**）、activation armed/disarmed；产品 `internal/modules/hosttools/tools.go:8-12,42` 明确按名字过滤，只注册旧 `goal`；产品 bridge（`modules/hosttools/module.go:141-215`）只实现 `rig.GoalUpdater`，不实现 `GoalRevisionStore`。**这是「目标长跑」最大的能力落差**。
2. **ralph**（fresh-agent 轮次循环，rig `tools/ralph`）与 **workflow**（脚本 fan-out，rig `tools/workflow`）：产品无 import、未注册（只有排除名单与显示名预留：`modules/agent/subagent.go:53`、`ui/uikit/toolnames.go:70-72`）。
3. **`present_artifact` / `code_review` / `auto_review` / `run_code`(PTC) / `inspect` / `report`**：rig 完整工具集（`tools/full/full.go`）里有，产品装配走手工选择，这些均未注册（`hosttools/tools.go:8-12`；`agent.go:195-199` 只有 5 个 session 工具）。
4. **rig presets（minimal/standard/all，81 静态工具）**：产品不使用 preset 机制，而是逐包挑选 + 自研 preset（`presets.agents` 人设）；后果是「产品工具面 ⊂ rig 工具面」且缺少账本级对照测试。

### ② 产品有 UI 但后端语义没实现 / 不匹配
1. **自动化运行历史**：UI 显示「成功/失败」，实际只记录 **`chat.Send` 是否被接受**（`contracts/automation.go:58-63`；`automation.go:658-760`）——run 内模型失败、工具失败、审批挂起都仍显示成功。
2. **自动化无人值守**：任务默认继承全局权限；「工作区可写」档下需要审批的工具会**无限期等待人**（无超时/自动拒绝/通知），UI 无任何护栏或提示（`plan_mode.go` gate → `approvals.Request` 阻塞）。
3. **goal dock/文案 vs 全局单例**：UI 说「为当前会话设置/清除长期目标」，实际全进程一个目标、所有会话显示同一个（FR-28）。
4. **goal 自动续跑 5 次上限静默**：达到上限无任何提示，用户以为还在跑（`session.go:1324-1326`）。
5. **会话排队**：UI 展示「排队」并承诺本轮结束自动发送，但队列只在 UI 内存，重启即丢（`session.go:1105-1225`）。
6. **「通知」设置页**：只控制应用内 toast，后台会话/自动化完成或失败仍无通知（PM-10）。
7. **`todo_write`**：模型侧有工具、UI 只有运行中活动行；todo 列表无面板、不持久、跨会话共享一个 store（`toolkit.go:146`）。
8. **Fork**：UI 呈现完整副本，实际丢弃工具调用、不感知压缩（FR-12 deferred）。
9. **诊断报告**：设置→数据的「导出诊断报告」只含应用信息+设置项，**不含日志**；日志只能打开数据目录自行查看。

### ③ 竞态 / 单例 / 不持久 / 生命周期缺陷
1. **FR-28 目标宿主级单例**：跨会话显示、续跑发到 `ActiveSession`、重启丢失。
2. **goal 续跑只存在于桌面 UI 模块**：headless/ACP 无自动续跑；且依赖 UI 线程/帧（chat.Subscribe→runtime.Post）。
3. **goal 续跑计数**在 UI `moduleState`（进程内），重启归零（若目标还在的话），无「轮次」语义、无 durable 记录。
4. **后台 job/终端**：进程内；**工作区切换**即 retire 旧 build → `CloseAll` 杀进程（`toolkit/rebind.go:92-97,160-180,270-280`）；重启即丢。
5. **会话排队**：进程内，重启丢；被 kill 的 run 不恢复排队项。
6. **自动化 run 会话泄漏**：每次 run 新建会话、从不清理（`automation.go:712-766`，无 DeleteSession/prune），小时级任务一天 24 个会话。
7. **自动化仅进程存活时运行**：无 daemon/service/关窗保活默认；退进程即停。
8. **子会话/teammate 重启后只读**：`child-sessions.json` 恢复的是历史记录，`Historical` 拒绝继续（`childagents.go:101-121`）。
9. **自动压缩不落盘**：只压缩本次 run 的内存对话（`$RIG/agent/agent.go:494-511`），每次 run 重新压缩；无 durable compact 事件。
10. **todo 单 store**：跨会话共享、不持久（`$RIG/tools/misc/todo.go:28-78`）。
11. **审批无超时**：`approvals.Request` 阻塞到 Resolve 或 ctx 取消；无人值守 = 永久挂起。
12. **子代理/成员默认 20 轮**：父 run 1e6，委派 20（rig `agent/subagent.go:255-266` 的 0→默认），长任务委派会先停。
13. **headless 权限被 settings 覆盖风险**：`agent.permission` 行优先于 assembly `auto`（`approvals.go:210-232`），同一 dataDir 下桌面写入会影响 headless 的无人值守行为 [源码，待实测]。

---

## 附录A 核查命令

环境（每条命令同一 shell 内执行；本沙箱 `/tmp` 每次调用重建，故 `mkdir` 与测试必须同一条命令）：

```bash
export PATH=/usr/local/go/bin:$PATH GOPATH=/data/mygo/.cache/gopath \
  GOCACHE=/data/mygo/.cache/go-build GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off \
  GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp
mkdir -p /tmp/zeokeel-tmp
cd /data/mygo
```

1. 版本与依赖：`cat VERSION`、`grep -n 'zeokeel-rig\|zeokeel-kernel' go.mod`、`go version`（实测 go1.27.3）。
2. **D1 目标/续跑** [实测 PASS]：`go test ./internal/ui/session/ -run 'TestGoal' -count=1 -v`
   - 覆盖 `TestGoalAutoContinueAlwaysOn` / `SendsOnceWithGoalText` / `StopsAfterFive` / `ResetsOnGoalChangeAndStopsWhenCleared` / `SendFailureShowsDanger` / `SkipsPausedGoal` / `SkipsCompletedAndAbandoned` 与 chip/命令/对话框。
3. **D1/D6 regression** [实测 PASS]：`go test ./internal/regression/ -run 'TestGoal|TestAutomation' -count=1 -v`（20 个全 PASS）。
4. **D2/D4/D10 agent 单测** [实测 PASS]：`go test ./internal/modules/agent/ -run 'TestRepeat|TestSpill|TestClassify|TestUsage|TestCompact|TestLoop|TestCancel' -count=1 -v`
5. **D3 job/终端随工作区切换被杀** [实测 PASS]：`go test ./internal/modules/toolkit/ -run 'TestWorkspaceSwitchReleasesOldRootJobs|TestWorkspaceSwitchClosesRetiredBuild|TestJobCompletionNoticesReachAgentLoop' -count=1 -v`
6. **D3 会话持久化/checkpoint** [实测 PASS]：`go test ./internal/modules/sessionstore/ -run 'TestPersistenceAcrossReopen|TestCheckpointStore|TestModulePersistenceAcrossRestart' -count=1 -v`
7. **D6 自动化 + hosttools goal** [实测 PASS]：`go test ./internal/modules/hosttools/ ./internal/modules/automation/ -run 'Goal|Schedule|Run' -count=1`
8. 目标单例/无持久化定位：`grep -n 'newGoalService\|goal:' internal/uiroot/uiroot.go`（:342）、`grep -rn '"goal\.\|Goal.*SettingKey' internal/contracts`（无持久化键）。
9. 自动续跑：`grep -n 'maxGoalAutoContinues\|func maybeAutoContinue\|chat.ActiveSession' internal/ui/session/session.go`。
10. DSH goal 三件套在 rig：`sed -n '14,22p;88,120p' $RIG/tools/hosttools/goal_dsh.go`；产品过滤：`sed -n '1,40p' internal/modules/hosttools/tools.go`。
11. ralph/workflow 未注册：`grep -rn 'tools/ralph\|tools/workflow' --include=*.go internal cmd`（0 命中）。
12. 自动压缩默认关：`grep -n 'ContextBudget' internal/modules/agent/agent.go | head`（:1162/:2243）；默认装配：`grep -n 'contextBudget\|maxIterations' internal/assembly/assemblies/*.json`（无）。
13. 自动化 run 语义：`sed -n '55,76p' internal/contracts/automation.go`、`grep -n 's.chat.Send' internal/modules/automation/automation.go`（:753）。
14. 通知面：`grep -rn 'NotifySuccess\|NotifyInfo\|NotifyWarning\|NotifyError' --include=*.go internal/modules internal/host`（0 命中）。
15. 子代理轮数继承：`sed -n '250,270p' $RIG/agent/subagent.go`；产品父 spec：`grep -n 'MaxIterations: cfg.MaxIterations' internal/modules/agent/subagent.go internal/modules/agent/team.go`。

---

## 附录B 10 维现状表

| 维度 | 状态 | 机制/位置 | 默认/上限 | 模型可见 | 用户可见 | 测试 |
|---|---|---|---|---|---|---|
| **D1 目标与续跑** | 半成品（桌面） | 宿主单例 `uiroot/goal.go:32-211`；旧 `goal` 工具 `modules/hosttools/tools.go:42`；续跑 `ui/session/session.go:1244-1345` | 1 个 goal/进程；续跑 **5 次/goal**，静默；无持久化；无 session 绑定；DSH 三件套未注册（rig 默认 256 轮） | `goal`（set/get/clear/update） | goal 条 + ⋯ 菜单 + 命令面板 | `TestGoal*`、`TestGoalAutoContinue*`、regression `TestGoal*` [实测] |
| **D2 循环与预算** | 可用但无预算 | `agent.go:46-58,2216-2248`；loop guard `loop_guard.go`；排队 `ui/session/session.go:1105-1225` | 主 run 迭代 0→**1e6**；contextBudget **0=关**；子代理/成员 **20 轮**；排队仅内存；无 run 超时 | `todo_write`、job_*、terminal_*（无 budget 工具） | composer 状态行；排队 popover | `TestRepeat*`、`TestCancel*`、`TestConcurrentSessions` [实测] |
| **D3 持久化与恢复** | 会话域强、执行域弱 | SQLite `sessionstore/store.go:123-157`；restore `agent.go:872`；checkpoint `agent.go:2241`；rebind retire `toolkit/rebind.go:92-97,160-180` | 表 4 张；run history 10；任务板 256；子会话 64/14 天；job/终端/目标/排队/todo **不持久** | `job_*`、`terminal_*`、`team_task_*` | 会话恢复、任务历史、子会话 | `TestPersistenceAcrossReopen`、`TestWorkspaceSwitchReleasesOldRootJobs` [实测] |
| **D4 上下文管理** | 齐备但默认关 | `compact_now.go:31`；rig 自动压缩 `$RIG/agent/agent.go:494-511`；spill `agent/spill.go:24-64`；用量 `usage.go` | 手动/自动压缩需 budget>0（默认 0）；spill 32 KiB/100 文件/240B preview；状态行 6 项 | 无（引擎内部） | 命令面板「压缩上下文」；composer 状态行 | `TestCompactNow*`、`TestSpillPolicy*`、`TestRunRecordsUsage*` [实测] |
| **D5 编排** | 两档（subagent/team） | `agent/subagent.go`、`agent/team.go`、`toolkit/task_store.go` | 同步 subagent 深度1/并发2；team 后台 深度1/并发4；任务板 256；**ralph/workflow/review/PTC 未注册** | `subagent`、`subagent_fork`、`team_*`、旧别名、`spawn_teammate/list_agents/wait_agent`、`team_task_*` | 运行带计数 + 团队/子智能体浮层 + 任务段 | `TestRunSubagentDelegatesSynchronously`、`TestTeamSpawnWaitAndMembers`、`TestTaskBoardCreateListStatusDelete`、`TestChildAgents*` [实测] |
| **D6 调度与无人值守** | 进程内调度 | `automation/automation.go:380-412,658-760`；headless/ACP `host/host.go:286-360`、`acp/acp.go:58-75` | tick 500ms；6 种表达式；run history 10；**每次新建会话不清理**；退进程即停；无 daemon | `schedule_create/list/update/delete` | 自动化屏 + 运行历史 + 会话链接 | `TestTickFiresDueTasks`、`TestRunNowAndAfterOneShot`、`TestACPSessionListRealFrames` [实测] |
| **D7 安全与审批** | 齐备，无人值守无兜底 | `approvals/approvals.go`；gate `agent/plan_mode.go:231+`；sandbox `modules/sandbox/sandbox.go:7-97` | 3 档 UI 权限；审批 **无超时**；OS 沙箱始终开、无 runner fail-closed；SSRF 拦截关闭 | 无（审批由 gate 触发） | 审批卡（带会话归属）、设置→行为、权限 chip | `TestGate*`、`TestPermission*`、`TestApprovals*`、`TestCancelBeforeToolApprovalMarksToolCancelled` [实测] |
| **D8 可观测与控制** | 过程可见，通知缺失 | `ui/session/agentsurface.go`；侧栏状态；`ui/toasts/toasts.go`；日志 `host/logger.go:16` | toast 应用内仅 1 通道；无 OS 通知；日志文件无查看器；自动化/后台完成无通知 | 无 | 运行带、侧栏等待/运行/未读、job/团队/子代理 popover、用量行、toast | `TestSessionRowShowsWaitingAndRunning`、`TestRunBandShowsPendingApproval`、`TestJobsOverlayAndKill`、`TestGoalAutoContinueSendFailureShowsDanger` [实测] |
| **D9 会话生命周期** | 最完整 | `agent.go:1238-1645,1434-1510`；`sessionstore/search.go`；`ui/session/export.go` | 标题 32 rune；fork 只复制 user/assistant；搜索 limit 由 store 定；无跨进程写锁 | `session_search/trace/event_*` | 侧栏搜索、改名/置顶/归档、fork、Markdown/JSONL 导出 | `TestForkSessionCopiesMessages`、`TestSearchMessages*`、`TestSessionExportJSONL*`、`TestSessionsOrderingAndDelete` [实测] |
| **D10 失败处理** | 分类好、无自动恢复 | `contracts/chat.go:206-250`；`agent.go:2478-2660`；`ui/session/view.go:1972+` | 无 retry/backoff/fallback；手动重试=Regenerate；迭代上限/截断有本地化；checkpoint 中断收尾 | 无 | 错误行 + 「重试」/「打开模型设置」 | `TestClassifyErrorTable`、`TestRegenerate*`、`TestRestore*` [实测] |

---

*本报告只陈述 ZeoKeel v0.2.35 一侧事实；DSH 侧能力见同目录 `01-dsh-capabilities.md`，差距综合由 Lead 完成。*
