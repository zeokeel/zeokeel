# 03 · 长时任务差距矩阵：DSH 0.2.1-rc.2 vs ZeoKeel v0.2.35

> 日期：2026-11-14 · 角色：Lead 综合（事实来自 `01-dsh-capabilities.md`、`02-zeokeel-baseline.md`，Play 侧抽样复核）
> 口径：「DSH」= 本机安装的 `@deepseek-ai/dsh@0.2.1-rc.2`（305 包）；「ZeoKeel」= `/data/mygo` v0.2.35（rig v0.16.2）。
> 差距类型：**①** rig/DSH 已有能力但产品未注册；**②** 产品有入口但后端语义不匹配；**③** 单例/不持久/生命周期/竞态缺陷；**④** 能力取舍或共同限制（不是缺陷，但影响长跑体验）。
> 严重度：**P0** = 长时任务在当前默认配置下会失败/不可用/结果不可信；**P1** = 长跑能力明显弱于 DSH、需要用户强干预；**P2** = 体验/一致性/审计问题。
> 证据行号：Play 侧 `internal/...` 均相对 `/data/mygo`；rig 侧相对 `$RIG=/data/mygo/.cache/gopath/pkg/mod/github.com/zeokeel/zeokeel-rig@v0.16.2`。**[实测]** 为本次或队友实跑测试。

---

## 0. 10 维总览

| 维度 | DSH（0.2.1-rc.2） | ZeoKeel（v0.2.35） | 差距类型 | 严重度 |
|---|---|---|---|---|
| D1 目标与续跑 | 每 session 一个 durable goal（事件源 + CAS revision）；四相 active/paused/**blocked**/complete；默认 **256 轮**；仅 agent idle + armed 时续跑；resume/fork/重启后保留但**必须人类显式 resume** | 进程级**单例**目标（无 sessionID、无持久化）；**仅桌面 UI 模块**在 run 结算后向**当前活动会话**发一条固定续跑消息；**5 次/目标**后**静默**停止；无 blocked 相位、无轮次展示 | ①③ | **P0** |
| D2 循环与预算 | 无 maxSteps/turn 上限（`while(true)`）；预算外挂：goal 轮数、compaction 阈值、单请求 maxTokens、工具超时、retry；并行工具默认 10 | 主 run 迭代 0→**1 000 000**（实际无上限）；`contextBudget` 默认 **0**（自动压缩关、`/compact` 不可用）；无 run 超时；子代理/成员默认反而只有 **20 轮** | ③ | **P0** |
| D3 持久化与恢复 | append-only session log（`.jsonl.zstd`）；3 个 fail-closed checkpoint（请求前/工具前/下一步前）；崩溃合成 `TOOL_OUTCOME_UNKNOWN`；**jobs 不跨重启**（共同限制）；schedule durable | SQLite 存 sessions/messages/events/checkpoints；rig checkpoint 已接线，崩溃后下次 run 自动收尾；但**目标/排队/后台 job/终端/todo/审批队列全部进程内**；job/终端在**工作区切换**时被 `CloseAll` 杀掉 | ③④ | **P1** |
| D4 上下文管理 | 默认开：`min(W×0.8, W−O−65536)` 触发压缩、保留 0.16、pruner 8192/4096/1024、spill 12500、溢出后 1 次最大头压缩 + retry；压缩写 durable 事件、原始事件保留 | 链路齐全但**默认全关**（需手动设 contextBudget）；自动压缩只改本次 run 内存副本、不落盘、无事件；手动 `/compact` **替换 durable history**、无事件；spill 默认 32 KiB/100 文件 | ②③ | **P0** |
| D5 编排 | 四档：subagent（depth 1/active 8）、Team（16 成员/256 任务）、workflow（脚本 fan-out，maxTotalAgents 1000）、ralph（fresh-agent，maxRounds 256） | 两档：同步 subagent（depth 1/并发 2）、后台 team（depth 1/并发 4）+ 持久任务板；**ralph / workflow / review / PTC / inspect / report 全部未注册**（rig 已有） | ① | **P1** |
| D6 调度与无人值守 | Host 级 schedule：6 种选择器（含 weekly/cron）、durable、绑定原 session、重启补最近一次；job 完成可唤醒 agent；真正无人值守 = 常驻 Host + 权限 preset | 进程内 500ms ticker + durable 任务表；**每次 run 新建会话**、从不清理；run 成败只记 `chat.Send` 是否被接受；无 daemon/service，桌面默认关窗即退出；表达式无 weekly/cron | ②③④ | **P0** |
| D7 安全与审批 | sandbox mode × approval policy；默认 `workspace-write + ask`；fail-closed；无人值守需显式 `danger-full-access + never`；提权一次一审批 | 三档 UI 权限 + OS 沙箱 + 审批队列（带 session 归属）齐备；但**审批无超时**，自动化遇审批**无限期阻塞**；无「自动拒绝并记录」策略；SSRF 拦截已关闭 | ②③ | **P1** |
| D8 可观测与控制 | goal/job/sessionStats/tokenUsage/contextPressure/contextBreakdown 投影；每 turn usage 行；job 输出流；完成通知唤醒 | 运行带、侧栏等待/运行/未读、job/团队/子代理浮层、用量状态行都在；但**无逐调用 usage、无 context 组成、无压缩事件、无成本**；**无系统通知**，后台/自动化完成失败**无任何提示**（PM-10 仍成立） | ②③ | **P1** |
| D9 会话生命周期 | create/resume/fork/search/export/title；fork 复制 inclusive 前缀事件 + inherited marker + closers；模型可 session_search | 持久化/恢复/fork/搜索/导出/归档/置顶/未读/自动标题齐全；**fork 只复制 user/assistant 文本，丢工具调用、不感知压缩**（FR-12 仍成立）；子会话重启后只读不可续 | ③④ | **P2** |
| D10 失败处理 | 分类 → 有界重试（5 次、500ms→10s、10% jitter，durable `llm/retry` 事件）→ 语义收尾 → 人决定；上下文溢出自动压缩 + 1 次 retry；无模型 fallback | 分类/本地化/手动「重试」好；**provider 层有 rig 默认 2 次有界重试**（429/408/425/5xx/传输，尊重 Retry-After，`$RIG/provider/openai/openai.go:54-67,263-292`），但没有 turn 级 durable retry 事件、没有溢出自动压缩重试；无模型 fallback（共同限制） | ③④ | **P1** |

---

## 1. 逐维展开

### D1 目标与续跑（最大结构差距）

| 能力 | DSH | ZeoKeel | 差距 |
|---|---|---|---|
| 目标状态归属 | 每 session 一个 current goal，`goal/change` v1 写 session log，replay 重建 | 全进程一个内存 `*Goal`，`contracts.Goal` 无 SessionID（`internal/contracts/goal.go:11-17`；`internal/uiroot/goal.go:28-36`；`internal/uiroot/uiroot.go:342`） | ③ 单例 |
| 持久化 | 事件源，跨 turn/resume/fork/重启 | 无任何持久化键；重启即空 | ③ |
| 模型工具 | `get_goal` / `create_goal` / `update_goal`（revision CAS；active/paused/blocked/complete；`roundsStarted/maxGoalRounds` 默认 256；activation armed/disarmed） | 只注册旧 `goal`（set/get/clear/update；blocked 返回错误）。rig 的 DSH 三件套已实现但产品按名过滤（`internal/modules/hosttools/tools.go:8-12,42`；rig `tools/hosttools/goal_dsh.go:17-22,63-100`） | ① |
| 自动续跑位置 | 引擎侧 `dsh-goal-round-driver`：agent idle + armed + 有余量才续跑，轮次有 reservation/admission、耗尽自动 block `round-limit` | 桌面 UI 模块 `maybeAutoContinue`：run settle 后向 `chat.ActiveSession()` 发「请继续推进目标」（`internal/ui/session/session.go:1244-1345`） | ③ |
| 续跑预算 | 默认 256，create 可覆盖，耗尽→blocked（有原因） | 固定 5 次/goal，耗尽**静默 return**（`session.go:1230,1324-1326`） | ③ |
| 会话隔离 | 按 session，UI 只显示该 session 的 goal | 所有会话显示同一个 goal；续跑可能落到当前活动会话（FR-28，`internal/ui/session/session.go:1313-1330`） | ③ |
| headless/ACP | goal driver 不依赖 UI；resume 后 disarm，人类显式 resume | 无 UI 模块 → **无续跑**；goal 工具落到进程内 `MemoryGoals`（`internal/hosttools/hosttools.go:147-152`、`internal/hosttools/memory.go:208-266`） | ③ |
| 重启语义 | goal 保留、armed 丢失、必须人类 resume | goal 直接丢失，无任何提示 | ③ |

### D2 循环与预算

| 项 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| step/turn 上限 | 无（`while(true)`/`while(turn())`） | 默认 0 → 1e6（等同无上限） | ④ 共同取舍；但产品没有配套预算 |
| 上下文预算 | 按模型窗口自动推导 | 手工 `contextBudget`，默认 0 | **P0**（见 D4） |
| run 超时 | 工具超时策略 + retry；无整体 run deadline | 无 run 超时（`Send` 后只能人工 Stop） | ③ P1 |
| 委派预算 | subagent 独立 maxTokens 等 | 主 run 1e6，子代理/成员 0→rig 默认 **20**（`internal/modules/agent/subagent.go:148-153`、`team.go:104-110`；rig `agent/agent.go:83-85`、`agent/subagent.go:263-264`） | ③ P1「委派先死」 |
| loop guard | `[3,5,8]` advisory | 同款 port：`[3,5,8]` advisory（`internal/modules/agent/loop_guard.go`） | 对齐 |

### D3 持久化与恢复

| 对象 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| 会话/事件 | append-only JSONL + 3 checkpoint | SQLite sessions/messages/events/checkpoints；checkpoint 三相位由 rig 自动写（`internal/modules/agent/agent.go:2241,2307-2313`） | 基本对齐 |
| 崩溃收尾 | `interruptedTurnClosers` 合成 unknown/not-started | rig `RecoverInterruptedTurn` 已接线（`$RIG/agent/agent.go:204,445-471`；`$RIG/agent/checkpoint.go:20-72`） | 基本对齐 |
| 后台 job | 进程内（共同限制） | 进程内；**工作区切换即杀**（`internal/modules/toolkit/rebind.go:92-97,143-180,270-280`；`TestWorkspaceSwitchReleasesOldRootJobs` [实测 PASS]） | ③ 产品额外损失 |
| 终端会话 | 由 terminal 插件管理，进程内 | 进程内；同上被工作区切换杀 | ③ |
| 排队消息 | inbox 属于 session 事件域 | UI `moduleState` 内存，重启丢（`internal/ui/session/session.go:1105-1225`） | ③ |
| goal | durable | 内存 | ③ |
| todo | 模型侧工具、事件域（以 DSH 实现为准） | 单例内存 store，不落盘、不按会话隔离（`internal/modules/toolkit/toolkit.go:146`） | ③ P2 |
| 自动化 run | — | JSON 持久化任务与 ≤10 条历史；但**run 本体**不追踪 | ② |
| 子会话/team | 可 cold resume stored teammates | 重启后只读；`Historical` 拒绝继续（`internal/modules/agent/child_sessions.go:163-169`、`childagents.go:101-121`） | ③ P1 |

### D4 上下文管理

| 项 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| 自动压缩 | 默认开，阈值 `min(W×0.8, W−O−65536)`，失败/溢出有一次恢复 | 需 `contextBudget>0` 才装 Compactor（`internal/modules/agent/agent.go:2243-2248`）；默认 0 → 不压缩（`agent.go:1162`；设置页明示「0 = 不自动压缩」`internal/ui/settings/settings.go:1379-1391`） | **P0** |
| 手动 `/compact` | 阈值以下也可用 | `contextBudget<=0` 直接 `ErrCompactUnavailable`（`internal/modules/agent/compact_now.go:45-57`） | **P0** |
| 压缩持久化/审计 | durable `compaction/*` 事件；原始事件保留 | 手动压缩**替换** durable history（`compact_now.go:170`）、无事件；自动压缩只改内存、每次 run 重算（`$RIG/agent/agent.go:494-511`） | ③ P1 |
| 压缩感知 fork | 支持（前缀事件） | fork 只复制 user/assistant 文本，压缩不感知（FR-12，`internal/modules/agent/agent.go:1495-1511`） | ③ P2 |
| 工具结果裁剪 | pruner 8192/4096/1024 默认开 | 无独立 pruner；spill 32 KiB/100 文件（`internal/modules/agent/spill.go`） | ④ P2 |
| 上下文状态 | token-meter + contextBreakdown/contextPressure 投影 | 状态行有 context/window/剩余 %（`internal/ui/session/view.go:3694-3763`），无组成/压力预警 | ② P1 |

### D5 编排

| 档位 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| subagent | spawn/fork；depth 1；active 8；异步可选 | `subagent`/`subagent_fork`；**同步阻塞**当前 run；depth 1/并发 2（`internal/modules/agent/subagent.go:26-60`） | ③ P1 |
| team | Lead + 具名成员 + durable task board；可 cold resume | `team_*` + 旧别名 + DSH 名（`spawn_teammate/list_agents/wait_agent`）+ `team_task_*`；后台；depth 1/并发 4；重启后不可续（`internal/modules/agent/team.go:26-70,593-616`） | ③ P1 |
| workflow | JS 脚本 fan-out，后台 job，maxTotalAgents 1000 | rig `tools/workflow` **未 import/未注册** | ① P1 |
| ralph | fresh-agent 轮次，maxRounds 256（experimental/opt-in） | rig `tools/ralph` **未 import/未注册** | ① P2（实验性） |
| 其他 rig 工具 | review/PTC/inspect/report 等 | 未注册（`internal/modules/hosttools/tools.go:8-12`） | ① P2 |

### D6 调度与无人值守

| 项 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| 选择器 | after/at/every(≥60s)/daily/weekly/cron | `*/N * * * *`、`M H * * *`、`@hourly`、`@daily`、`every Ns`、`after Ns`（`internal/modules/automation/schedule.go:1-30`） | ③ P2（无 weekly/cron） |
| 任务持久化 | durable Host 域，重启保留 | JSON 原子写 + 损坏隔离（`internal/modules/automation/automation.go`） | 对齐 |
| 投递目标 | **原 session** 追加 follow-up，冷 session 可恢复 | **每次新建会话**（`automation.go:712-766`），不清理、上下文不连续 | ② P1 |
| 运行结果 | ack 的是投递，不冒充执行完成 | UI/历史把 `chat.Send` 被接受记为 **success**（`internal/contracts/automation.go:58-63`；`automation.go:658-707`）；run 内失败/审批挂起不可见 | **P0**（语义失真） |
| 错过补跑 | 每个 recurring 任务补最近一次 | 启动后 tick 发现过期即补跑一次（`automation.go:396-404`） | 对齐 |
| 进程形态 | 常驻 Host（Web/Desktop）；headless 一次一任务 | 进程内 ticker；无 service/daemon；桌面关窗默认退出（`internal/host/tray.go:59-72`） | ③ P1 |
| 重叠/超时 | 有 job/wait 语义 | 同任务 in-flight 折叠；无 run 超时 | ③ P2 |

### D7 安全与审批

| 项 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| 审批策略 | sandbox × approval preset；默认 ask；fail-closed | 三档 UI 权限 + headless ask/never/auto；OS 沙箱始终开、无 runner fail-closed | 对齐 |
| 无人值守 | 显式 `danger-full-access + never`；否则等人 | 自动化默认继承全局权限；遇审批**无限期阻塞**（`internal/modules/approvals/approvals.go:235-300`；`internal/modules/agent/plan_mode.go:340-360`），无超时/自动拒绝/通知 | ③ P1 |
| 审批归属 | request 带 session | 已带 SessionID（`internal/contracts/store.go:91-106`） | 对齐（FR-29 已修） |
| 网络面 | sandbox 约束 | `web_fetch` SSRF 地址拦截被关闭（CHANGELOG 明示） | ④ P2（无人值守风险） |

### D8 可观测与控制

| 项 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| 运行状态 | sessionStats/tokenUsage/contextPressure 投影 | 运行带 + 侧栏等待/运行/未读 + 状态行 | 对齐 |
| goal 进度 | phase/roundsStarted/cap/blocker/armed | 只有状态 badge + 文本；**无轮次/上限/停止原因** | ② P1 |
| 逐请求用量 | 每 turn usage 行 + projection | 每回合一条累计 `usage` 事件；`EventUsage.Iteration` 被丢弃（`internal/modules/agent/usage.go:57-65`；`agent.go:2348-2350`） | ③ P1 |
| 压缩/spill 事件 | durable | 无（见 D4） | ③ P1 |
| 成本 | token-meter 可定价 | 无价格字段/成本计算 | ③ P2 |
| 通知 | job 完成唤醒；schedule 投递 | 仅应用内 toast；**后台/自动化完成失败无通知**（PM-10 仍成立：`internal/contracts/notify.go:5-40`；`internal/modules`+`internal/host` 无 `Notify*` 调用） | ③ P1 |

### D9 会话生命周期

| 项 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| 持久化/恢复/搜索/导出/标题 | 全 | 全（`session_search/trace/event_*` 5 工具已注册，`internal/modules/agent/agent.go:195-199`） | 对齐 |
| fork | 前缀事件 + marker + closers | 只复制文本（工具调用/压缩丢弃） | ③ P2 |
| 子会话/team resume | cold resume | 只读历史，不可续 | ③ P1 |
| 跨进程并发写保护 | 单写者/文件锁 | 无跨进程文件锁（M-10） | ④ P2 |

### D10 失败处理

| 项 | DSH | ZeoKeel | 判定 |
|---|---|---|---|
| 错误分类 | 稳定 error code | `ClassifyError`：auth/rate_limit/timeout/network/context/iterations/cancelled/other + 本地化恢复动作（`internal/contracts/chat.go:206-250`） | 对齐 |
| 请求重试 | 5 次、500ms→10s、10% jitter、durable 事件 | provider 层 2 次、500ms 起指数退避、尊重 Retry-After（rig 默认，产品未覆盖）；无 turn 级事件 | ③ P2（部分对齐） |
| 上下文溢出恢复 | 自动压缩 + 1 次 retry | 无（默认压缩都关） | **P0**（见 D4） |
| 崩溃恢复 | 语义收尾、不盲目重试 | checkpoint 收尾、不重放（对齐）；但**不续跑**、UI 无中断标记 | ③ P1 |
| 人工重试 | 有 | 失败 turn「重试」= Regenerate（压缩感知） | 对齐 |
| 模型 fallback | 无 | 无 | ④ 共同限制 |

---

## 2. 能力落差 Top 10（按「长跑可用性」排序）

| # | 落差 | 类型 | 严重度 | 一句话影响 |
|---|---|---|---|---|
| 1 | goal 不是 durable/session-bound，续跑在 UI 且只认活动会话 | ③ | P0 | 长任务目标跨会话串味、切会话即停、重启即丢 |
| 2 | 默认没有上下文保护（contextBudget=0 → 压缩全关） | ②③ | P0 | 长任务在上下文超限时直接失败，且 `/compact` 也不可用 |
| 3 | 无人值守语义失真（automation success=Send 被接受；审批可永久挂起；无通知） | ②③ | P0 | 用户以为任务在跑/跑成功，实际卡住或失败 |
| 4 | 执行域不持久（job/终端/排队/todo/审批队列）且 job 被工作区切换杀死 | ③ | P1 | 切工作区/重启打断正在进行的长任务 |
| 5 | goal 续跑上限 5 且静默、无轮次/blocked/停止原因 | ②③ | P1 | 长任务推进到一半无声停住，用户无法判断 |
| 6 | 委派预算反而更小（子代理/成员 20 轮；主 run 无上限） | ③ | P1 | 长任务一旦委派，子任务提前放弃 |
| 7 | workflow/ralph 未暴露（rig 已有） | ① | P1 | 缺少脚本化 fan-out 与 fresh-agent 迭代两档 |
| 8 | 运行结果不可观测（逐调用 usage/context 组成/压缩事件/成本缺失） | ②③ | P1 | 长跑中无法回答「还在推进吗/烧了多少」 |
| 9 | team/子会话重启后不可续；无 cold resume | ③ | P1 | 多智能体长任务被重启打断即报废 |
| 10 | 压缩破坏性、fork 丢工具调用 | ③ | P2 | 无法审计/回滚压缩前上下文，fork 副本不等价 |

---

## 3. 与既有研究的一致性

- 本文 D4/D8 的「逐调用 usage / 上下文快照 / 压缩事件 / 成本不可观测」与 `docs/research/dsh-context/03-zeokeel-baseline.md` 的 P0 结论一致：数据基座缺口，不是 UI 缺口；该报告的 P0 估算（9–12 人天）仍适用。
- 本文 D1 的 FR-28、D8 的 PM-10、D9 的 FR-12 与 `CHANGELOG.md:251-260`、`docs/PRODUCT-BUG-REVIEW-2026-11-11.md` 的 Deferred 列表一致；本报告确认三者在 v0.2.35 **均未修复**。
- 对 `02-zeokeel-baseline.md` 的一处**修正**：D10「无自动 retry」不准确——rig `provider/openai` 默认带 2 次有界重试（429/408/425/5xx/传输错误、500ms 起指数退避、尊重 Retry-After，`$RIG/provider/openai/openai.go:54-67,263-292`；测试 `TestStreamRetryAfterServerError`/`TestStreamRetryExhausted`）。准确表述是「产品层无 retry 策略、无 turn 级 durable 事件、无溢出重试」，而非「完全没有重试」。

---

## 4. 证据与验证

- DSH 侧：`01-dsh-capabilities.md` §附录A 的 10 组 `rg/sed` 命令；本文引用的关键数字已由 Lead 抽查：`defaultMaxGoalRounds=256`（`dsh-goal/lib/index.js:588,593`）、`blockedAfterConsecutiveRounds=3`（`dsh-tool-goal/lib/index.js:115`）、`round-limit`（`dsh-goal-round-driver/lib/index.js:135-142`）、compaction `0.8/65536/0.16`（`dsh-compaction-basic/lib/index.js:15-17,63-85`）、ralph `maxRounds=256`（`dsh-tool-ralph/lib/index.js:128`）。
- ZeoKeel 侧：[实测 PASS] `go test ./internal/regression -run 'TestGoal|TestAutomation' -count=1`（本次）、`go test ./internal/modules/toolkit -run 'TestWorkspaceSwitchReleasesOldRootJobs|TestWorkspaceSwitchClosesRetiredBuild' -count=1`（本次）；其余见 `02-zeokeel-baseline.md` §附录A。
- 未验证/待确认：headless 的 `auto` 审批是否会被同一 dataDir 的 `agent.permission` 设置覆盖（`02` 标为源码风险）；桌面托盘/最小化时 goal 续跑是否仍推进（依赖 mygo 帧循环，未实测）；OS 通知在 mygo 的能力边界（需 spike）。
