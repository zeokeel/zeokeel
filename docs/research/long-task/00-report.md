# 00 · 对比 DSH：ZeoKeel「长时任务」的问题清单与修复路线

> 日期：2026-11-14 · 类型：**只读调研 + 差距分析**（未改任何产品代码；仅新增本目录 4 个文档）
> 问题：对比 DSH 的能力（核心是长时任务 / long-horizon task），ZeoKeel 当前还有哪些问题？
> 对象：ZeoKeel v0.2.35（`/data/mygo`，rig v0.16.2 + mygo v0.9.0）vs 本机 DSH `@deepseek-ai/dsh@0.2.1-rc.2`（305 包）。
> 证据分级：**[实测]** 本次可复现测试 ＞ **[源码]** `file:line` ＞ **[判断]**（显式标注）。
> 配套：`01-dsh-capabilities.md`（DSH D1–D10 档案，501 行）· `02-zeokeel-baseline.md`（ZeoKeel D1–D10 现状，369 行）· `03-gap-matrix.md`（10 维矩阵与 Top 10）。

---

## 0. 一页结论

**总体判断**：ZeoKeel 的长跑**基础件不弱**——会话/消息/事件/checkpoint 持久化、崩溃后语义收尾、会话恢复/fork/搜索/导出、team + 共享任务板、loop guard、审批归属（FR-29）都已对齐或接近 DSH。真正的问题不是「缺工具」，而是**长任务的生命周期没有归属者**：目标、上下文、无人值守三条闭环都还挂在 UI 或进程内存上。

| # | 问题（一句话） | 严重度 | 关键证据 |
|---|---|---|---|
| 1 | **目标（goal）是宿主级单例**：不绑会话、不持久；切会话即串味/停摆，重启即丢（FR-28 仍成立） | **P0** | `internal/uiroot/goal.go:28-36`、`internal/uiroot/uiroot.go:342`、`internal/contracts/goal.go:11-17`、`internal/ui/session/session.go:1313-1330` |
| 2 | **自动续跑只在桌面 UI 模块**：发往「当前活动会话」，每目标固定 5 次且**静默**停止；headless/ACP 完全没有续跑 | **P0** | `internal/ui/session/session.go:1230,1244-1345`；`internal/host/host.go:681,711`；`internal/hosttools/hosttools.go:147-152` |
| 3 | **默认没有上下文保护**：`contextBudget` 默认 0 → 自动压缩关闭、`/compact` 直接不可用；长任务撞上下文上限时只能失败 | **P0** | `internal/modules/agent/agent.go:1162,2243-2248`、`internal/modules/agent/compact_now.go:45-57`、`internal/ui/settings/settings.go:1379-1391` |
| 4 | **自动化 run 语义失真**：`success` 只代表 `chat.Send` 被接受；run 内失败/审批挂起不可见；每次新建会话、从不清理、上下文不连续 | **P0** | `internal/modules/automation/automation.go:658-760,712-766`、`internal/contracts/automation.go:58-63` |
| 5 | **无人值守审批没有兜底**：自动化遇到需要审批的工具会无限期等待（无超时、无自动拒绝、无通知） | **P0** | `internal/modules/approvals/approvals.go:235-300`、`internal/modules/agent/plan_mode.go:340-360` |
| 6 | **进程生命周期 = 任务生命周期**：无 daemon/service，桌面默认关窗即退出；退出/崩溃后 run 不续跑、goal 丢失、job 被工作区切换直接杀掉 | **P0** | `internal/host/tray.go:59-72`、`internal/modules/toolkit/rebind.go:92-97,143-180` |
| 7 | 执行域不持久：排队/todo/目标/后台 job/终端/审批队列全在内存；子代理/团队重启后只读不可续 | **P1** | `internal/ui/session/session.go:1105-1225`、`internal/modules/agent/child_sessions.go:163-169` |
| 8 | 委派预算反而更小：主 run 实际无上限，子代理/团队成员默认只有 rig 的 **20 轮**；同步 subagent 还阻塞主 run | **P1** | `internal/modules/agent/subagent.go:148-153`、`team.go:104-110`；rig `agent/agent.go:83-85` |
| 9 | 编排缺两档：rig 已有的 `workflow` / `ralph` 未注册；长任务缺少脚本化 fan-out 与 fresh-agent 迭代 | **P1** | 全仓 grep `tools/workflow|tools/ralph` = 0；rig `tools/workflow`、`tools/ralph` |
| 10 | 可观测性：无逐调用 usage、无上下文组成、无压缩事件、无成本；**后台/自动化完成或失败没有任何通知**（PM-10 仍成立） | **P1** | `internal/modules/agent/usage.go:57-65`；`internal/contracts/notify.go:5-40`；`docs/research/dsh-context/03-zeokeel-baseline.md` |

**最该先做的三件事**（建议 Wave 1，约 **20–30 人天**）：
1. **Goal v2**：把目标从「宿主单例 + UI 续跑」迁到「会话级 durable 目标 + 引擎侧续跑驱动」（可复用 rig 已提供的 DSH 三件套与 `GoalRoundDriver`）——一次解决 FR-28、静默 5 次、headless 无续跑、跨会话错投。
2. **默认上下文安全**：按模型目录的 `ContextWindow` 推导默认预算（如 0.8×W）并默认启用压缩；`/compact` 不再依赖手工预算；溢出自动压缩 + 1 次重试。
3. **自动化真生命周期**：run 记录从「已投递」升级为「运行中/完成/失败/中断 + 时长 + 错误」，完成/失败可通知；把「每次新会话」改为可选的「同一任务会话续跑」；加会话清理策略。

**若只做一件事**：做第 1 件。长时任务的可信度首先取决于「目标是否活过会话/重启/切屏」。

---

## 1. 对比框架：DSH 六层 vs ZeoKeel 三轨

DSH 的长跑不是某个 `maxSteps`，而是**六层可独立替换、各有明确「耗尽后行为」的机制**（详见 `01-dsh-capabilities.md` §0）：

```
目标域 dsh-goal ── 轮数 256，耗尽→blocked(round-limit)
续跑驱动 dsh-goal-round-driver ── 仅 idle+armed 时投 <goal_round>
循环 dsh-agent-loop ── 无步数上限；并行 10；预算外挂
持久化 dsh-session + checkpoint-policy ── 3 个 fail-closed 屏障 + 崩溃语义收尾
上下文 dsh-compaction* + token-meter ── 默认 0.8W 自动压缩 + pruner + 溢出重试
调度/编排 dsh-schedule + jobs + subagent/Team/workflow/ralph
```

ZeoKeel 目前是**三条互不相通的轨道**：

```
(1) 桌面 UI：goal 单例 + 自动续跑（5 次、静默、发往活动会话）
(2) agent 引擎：run（无预算、默认不压缩、checkpoint 收尾、子代理 20 轮）
(3) automation：进程内 ticker → 新建会话 → chat.Send（成功=已接受）
```

**结论**：DSH 把「长跑」当作一个**有状态、可恢复、可观测、可审计的生命周期**；ZeoKeel 把它拆成了三段一次性动作。差距主要不在单个工具，而在**谁拥有长任务的生命周期**。

10 维详细对比见 `03-gap-matrix.md` §0/§1。总览：

| 维度 | DSH 支持度 | ZeoKeel 现状 | 差距性质 | 严重度 |
|---|---|---|---|---|
| D1 目标与续跑 | 完整 | 桌面 UI 半成品（单例/不持久/5 次静默） | 结构差距 | **P0** |
| D2 循环与预算 | 无步数上限，预算外挂 | 主 run 无上限但无配套预算；委派 20 轮 | 预算缺位 | **P0** |
| D3 持久化与恢复 | 事件日志 + 3 checkpoint | 会话域强；执行域（job/排队/goal）弱 | 分层不齐 | **P1** |
| D4 上下文管理 | 默认自动压缩 + pruner + 溢出恢复 | 链路齐全但**默认全关**；手动压缩破坏性 | 默认值与语义 | **P0** |
| D5 编排 | subagent/Team/workflow/ralph 四档 | subagent/Team 两档；workflow/ralph 未注册 | 能力未暴露 | **P1** |
| D6 调度与无人值守 | Host 级 durable schedule + 原会话投递 | 进程内 ticker；新会话；成败只记 Send | 语义不匹配 | **P0** |
| D7 安全与审批 | preset；无人值守需显式选择 | 三档权限齐备；审批无超时 | 兜底缺失 | **P1** |
| D8 可观测与控制 | 投影 + 每 turn usage + 通知 | 过程可见；无逐调用/压缩/成本/通知 | 数据基座 | **P1** |
| D9 会话生命周期 | 完整（fork 保事件） | 完整；fork 丢工具调用/压缩 | 小差距 | **P2** |
| D10 失败处理 | 有界重试 + 语义收尾 + 溢出恢复 | 分类/手动重试好；provider 2 次重试；无溢出恢复 | 部分缺口 | **P1** |

---

## 2. 问题清单

> 每条包含：症状 → 根因 → 证据 → 影响 → 建议修法 → 估算。人天按 1 名熟悉本仓库的工程师计，含单测/帧测试/i18n/快照/designlint。估算为 [判断]。

### P0（长任务在当前默认配置下会失败、不可用或结果不可信）

#### P0-1 目标跨会话串味、重启即丢（FR-28）

- **症状**：在会话 A 设置目标后，切到会话 B 仍显示 A 的目标；A 跑完时若 B 是活动会话，续跑消息会被发进 B；重启应用后目标消失、无任何提示。
- **根因**：`contracts.Goal` 没有 SessionID；`uiroot` 每进程只建一个内存 `goalService`；goal 工具直连这个单例；续跑逻辑用 `chat.ActiveSession()` 而不是目标所属会话。
- **证据**：`internal/contracts/goal.go:11-17`；`internal/uiroot/uiroot.go:342`；`internal/uiroot/goal.go:28-36`；`internal/ui/session/session.go:1313-1330`；`internal/ui/session/view.go:586`（dock 不区分会话）；CHANGELOG `FR-28`（`CHANGELOG.md:253`）。
- **影响**：长任务推进错会话；用户切到别的会话查看进度时，原任务的目标推进被静默中断；重启后无法接续，只能凭记忆重建目标。
- **建议**：目标状态改为 **per-session durable**（建议写 `sessionstore` 事件 `goal/change`，或先落 settings 键并绑定 sessionID）；`contracts.Goal` 加 SessionID/Owner；`/goal`、dock、goal 工具全部按当前会话解析；fork 复制目标；重启后恢复（armed 可丢，但 phase/rounds 必须保留）。
- **估算**：**4–6 人天**（Goal v2 合计 8–12，与 P0-2 共享存储/契约改造）。**依赖**：无（需定契约）。

#### P0-2 自动续跑只在桌面 UI + 活动会话 + 5 次静默

- **症状**：headless/ACP 里模型能设 goal 但永远没人推进（工具却返回成功）；桌面里目标最多推进 5 次后无声停止；用户切走会话后不再推进；5 次上限与失败计数均无 UI 提示。
- **根因**：续跑是 `internal/ui/session` 的一段帧观察逻辑，不属于 agent 引擎；预算硬编码 5；`maybeAutoContinue` 达到上限直接 `return`，不写错误行/不通知；headless 没有 UI 模块，goal 落到 `MemoryGoals` 黑盒。
- **证据**：`internal/ui/session/session.go:1230,1244-1345,1324-1326`；`internal/host/host.go:681,711`（只有 `uiRoot != nil` 才提供 `KeyGoalTracker`）；`internal/hosttools/hosttools.go:147-152`、`internal/hosttools/memory.go:208-266`；rig 已提供但未用的 `tools/hosttools/goal_dsh.go:17-22,63-100`（DSH 三件套，默认 256 轮）与 `agent/goal_driver.go`（`DefaultGoalRounds=6`、`MaxGoalRounds=12`）；产品过滤名单 `internal/modules/hosttools/tools.go:8-12,42`。
- **影响**：长任务的「持续自主推进」只在「桌面 + 正好停在该会话」的窄条件下成立；换个形态（ACP/headless/自动化）或切个会话就断；用户无法判断是「在推进」还是「已停」。
- **建议**：把续跑迁到 **agent 后端模块**（不依赖 UI）：run 结算后由后端读取该会话的 durable goal，按轮数预算排队下一条；UI 只做展示与暂停/恢复。预算建议 **默认 8–12、按目标可配**，耗尽写「已停止（round-limit）」并在 dock 展示。重启后默认 **disarmed**，由用户显式恢复（对齐 DSH 的安全语义），无人值守任务可配置自动恢复。
- **估算**：**5–7 人天**（Goal v2 合计 8–12，含 P0-1 的 4–6）。**依赖**：P0-1。

#### P0-3 默认没有上下文保护，长任务撞墙即失败

- **症状**：默认安装下长会话不会自动压缩；`/compact` 提示「不可用」；上下文超限时整轮失败，用户只能新建会话或手动调预算。
- **根因**：`contextBudget` 默认 0；只有 `>0` 才装 `Compactor`；`CompactNow` 同样要求 `>0`；模型目录虽有 `ContextWindow`，但只用于状态行显示，不参与预算推导。
- **证据**：`internal/modules/agent/agent.go:1162`（`settingInt(..., 0)`）、`:2243-2248`（`if cfg.ContextBudget > 0` 才装 Compactor）；`internal/modules/agent/compact_now.go:45-57`（`ErrCompactUnavailable`）；`internal/ui/settings/settings.go:1379-1391`（设置页原文「0 = 不自动压缩」）；rig 无 compactor 时 `prepareContext` 直接返回（`$RIG/agent/agent.go:497-500`）。
- **影响**：长任务最典型的失败模式之一；用户需要先懂「上下文预算」这个内部概念才能自救。
- **建议**：模型有 `ContextWindow` 时默认推导预算（如 0.8×W，扣除输出与余量）；启用 `LLMCompactor` + 溢出时自动压缩并重试 1 次；`/compact` 在无显式预算时也按模型窗口工作；压缩结果写 durable 事件（与 P1-5 合并）。保留「手动预算」作为覆盖项。
- **估算**：**4–6 人天**（含测试与设置页文案）。**依赖**：无。

#### P0-4 自动化 run 语义失真：success 只代表「prompt 已投递」

- **症状**：自动化历史显示「成功」，但模型/工具随后失败、审批卡住、应用退出都不影响这条「成功」；每次运行新建一个会话，上下文不连续；会话只增不减。
- **根因**：`execute()` 在 `runTask()` 返回后即写 `AutomationRunSuccess`；`runTask` 只做 `NewSession` + `chat.Send`（异步，`Send` 返回即视为投递成功）；automation 不订阅 run 状态、不等待完成；无会话清理。
- **证据**：`internal/modules/automation/automation.go:658-707`（execute）、`:712-766`（runTask）、`:753`（`chat.Send`）；`internal/contracts/automation.go:58-63`（注释原文「marks a run whose prompt was sent successfully」）；`internal/contracts/chat.go:457-459`（Send 异步）；全文件无 `DeleteSession`/`Subscribe(chat)`。
- **影响**：用户把「已投递」误读为「任务成功」；失败无人知道；长期运行后会话列表被自动化会话淹没；recurring 任务每次从零开始，长任务失去连续性。
- **建议**：① run 记录扩为「delivered/running/completed/failed/interrupted + finishedAt + error + 会话 ID」；② automation 订阅 `ChatService` 的会话状态，run 结算时回写历史；③ 完成/失败走通知（P1-6）；④ 可选「同一任务复用会话」模式 + 保留策略（N 天/保留最近 N 个）；⑤ UI 的「成功」改为「已投递/运行中/成功/失败」四态。
- **估算**：**5–7 人天**。**依赖**：P0-5/P1-6 可复用状态通道。

#### P0-5 无人值守审批可无限期挂起

- **症状**：自动化任务在「工作区可写」下遇到需要审批的工具（shell/网络/MCP/设置写）时，run 永久等待；人不在电脑前就永远不结束；没有任何超时或通知。
- **根因**：审批 `Request` 只等 `Resolve` 或 ctx 取消（`approvals.go:276`），没有 timer；automation 默认继承全局权限，没有「任务级无人值守」策略；UI 只展示审批队列，不区分「自动化在等」。
- **证据**：`internal/modules/approvals/approvals.go:235-300`；`internal/modules/agent/plan_mode.go:340-360`；`internal/ui/automation/view.go:917-937`（三档权限）。
- **影响**：长跑被一个审批卡死且无人知晓；与「自动化」的产品承诺冲突。
- **建议**：任务级「无人值守」策略三选一：超时自动拒绝并记录 / 任务级 `never`（只允许只读或显式白名单）/ 通知后等待；至少要有「等待审批」状态与通知；审批 pending 超时写 durable 事件。
- **估算**：**3–5 人天**。**依赖**：无（与 P0-4 的 run 状态可合并）。

#### P0-6 进程生命周期 = 任务生命周期

- **症状**：关窗即退出（close-to-tray 默认 off）；应用退出/系统重启后，正在跑的 run、goal、job、排队全部消失；没有「继续上次」的入口；更新重启同样直接杀掉。
- **根因**：桌面是唯一常驻形态；automation ticker 与 agent run 都在 UI 进程内；goal/续跑/排队为内存状态；无 daemon/service，无退出保护，无「恢复未完成 run」。
- **证据**：`internal/host/tray.go:59-72`（默认 false = 关窗退出）；`internal/modules/automation/automation.go:380-412`（ticker 属模块）；`internal/modules/toolkit/rebind.go:92-97,143-180,270-280`（工作区切换/退出路径关闭 job/terminal）；`internal/ui/session/session.go:1105-1225`（排队仅内存）；rig checkpoint 只在下次 run 收尾（不自动续跑）。
- **影响**：长任务随时可能被一次误关窗、系统更新、crash 打断；用户没有「继续」按钮；后台自动化不可靠。
- **建议**：分三步：① **退出/更新前检测运行中的 run/goal/job** 并确认（或提供「最小化到托盘继续」）；② 重启后 **列出被中断的 run**，一键「继续」（goal 恢复为 disarmed，人工 resume）；③ 评估常驻形态（tray 保持进程 + 可选开机启动），或明确「长任务请用 ACP/headless 常驻进程」的产品边界。
- **估算**：**4–6 人天**（不含 daemon 化；daemon 另计 8–12）。**依赖**：P0-1/P0-2（恢复需要 durable goal）。

### P1（明显弱于 DSH，需要用户强干预）

#### P1-1 执行域不持久 + 工作区切换杀 job

- **症状**：后台 job / 终端会话在切换工作区时被直接关闭（不是重启才丢）；排队消息、todo、审批队列重启即丢；team 成员重启后只读，不能继续。
- **根因**：toolkit 按工作区根构建工具，旧 build retire 时 `CloseAll` 所有 job/terminal（设计上防泄漏，但对长任务是硬伤）；排队/todo/审批队列是进程内状态；子会话恢复为 `Historical`。
- **证据**：`internal/modules/toolkit/rebind.go:92-97,143-180,270-280`；`TestWorkspaceSwitchReleasesOldRootJobs`/`TestWorkspaceSwitchClosesRetiredBuild` [实测 PASS]；`internal/ui/session/session.go:1105-1225`；`internal/modules/toolkit/toolkit.go:146`（todo）；`internal/modules/agent/child_sessions.go:163-169`、`childagents.go:101-121`。
- **影响**：用户切个项目/工作区就把长任务的后台进程杀掉；多智能体长任务重启即报废。
- **建议**：job/terminal 改为「按会话/工作区拥有」而不是「随 build 销毁」，或至少在工作区切换时提示并保留（可配置）；排队消息落 settings/事件；team 成员支持 cold resume（rig team 具备基础，产品需接线）。
- **估算**：**5–8 人天**（job 生命周期重构）+ **3–5 人天**（team cold resume）。

#### P1-2 goal 的可观测与终态语义（5 次静默、无 blocked、无轮次）

- **症状**：goal dock 只有状态 badge + 文本；没有「已推进 N/M 轮」、没有停止原因、没有 blocked 相位；达到上限无提示。
- **根因**：产品 goal 生命周期只有 active/paused/completed/abandoned，没有 blocked；续跑计数只在 UI 内部；`blocked` 被显式拒绝（`hosttools/module.go:204`）。
- **证据**：`internal/ui/session/view.go:586-760`；`internal/modules/hosttools/module.go:201-204`；`internal/ui/session/session.go:1324-1326`。
- **影响**：用户无法判断长任务是「在推进」「卡住」还是「已停」；模型也无法记录阻塞原因。
- **建议**：并入 P0-2 的 Goal v2：增加 `blocked(reason/code)`、`roundsStarted/maxGoalRounds`、`armed/disarmed`；dock 展示轮次与停止原因；达到预算写终态而非静默。
- **估算**：已含在 P0-2 的 5–7 人天（Goal v2 合计 8–12）。

#### P1-3 委派预算反而更小（20 轮 + 同步阻塞）

- **症状**：主 run 可以无限跑，但 `subagent` / team 成员到 20 轮就停；同步 `subagent` 会占住主 run 直到子任务结束。
- **根因**：产品把 `cfg.MaxIterations`（默认 0）传给子 spec；rig 把 0 解释为 `DefaultMaxIterations=20`；主 run 则被产品显式改成 1e6；同步 subagent 用 `Wait` 阻塞。
- **证据**：`internal/modules/agent/subagent.go:148-153`、`team.go:104-110`；rig `agent/agent.go:83-85`、`agent/subagent.go:263-264`。
- **影响**：长任务一旦委派，子任务比父任务先「放弃」，且主 run 被同步委派拖住。
- **建议**：子代理/成员预算可配置，默认与主 run 对齐（或用独立预算显式说明）；`subagent` 提供异步模式或默认异步（team 已有异步）；在系统提示中告诉模型委派预算。
- **估算**：**2–3 人天**。

#### P1-4 workflow / ralph 未暴露（rig 已有）

- **症状**：模型只能做「subagent 同步委派 + team 后台成员」，无法脚本化 fan-out，也无法 fresh-agent 迭代。
- **根因**：产品装配逐包挑选工具，未 import `rig/tools/workflow` 与 `rig/tools/ralph`；系统提示/工具卡/审批 UI 也未接。
- **证据**：全仓 `grep -rn 'tools/workflow|tools/ralph' internal cmd` = 0；rig `tools/workflow/workflow.go`、`tools/ralph/ralph.go`；`internal/modules/agent/subagent.go:53` 仅把 ralph 放进排除名单；`internal/uikit/toolnames.go:70-72` 只是保留了显示名。
- **影响**：大规模并行调研/迁移、多轮打磨类长任务缺少合适编排；只能用 team 手工模拟。
- **建议**：评估产品是否需要：若需要，注册 workflow/ralph（含系统提示 guidance、工具卡、后台 job 结果渲染、权限）；若不需要，明确产品边界并在 README 说明（避免「DSH 有我们没有」的误解）。
- **估算**：**4–6 人天**（两工具 + UI + 测试）；ralph 为实验性，可后置。

#### P1-5 长跑可观测性：只有「过程」，没有「账本」

- **症状**：状态行只有每回合累计；看不到每次模型调用的 token/上下文；看不到压缩/裁剪；没有成本；长任务跑到一半，用户无法回答「还在推进吗？预算烧了多少？」
- **根因**：`EventUsage.Iteration` 被丢弃，只记最后 prompt tokens 与一条聚合 `usage` 事件；手动压缩不写事件；自动压缩在 rig 内不可观测；模型目录无价格。
- **证据**：`internal/modules/agent/usage.go:57-65`、`agent.go:2348-2350`；`compact_now.go` 全程无 `appendEvent`；`docs/research/dsh-context/03-zeokeel-baseline.md` §1（11 项缺口矩阵，P0 9–12 人天）；`internal/ui/session/view.go:3694-3763`（状态行）。
- **影响**：长任务不可诊断：分不清「模型在思考」「工具卡住」「上下文压力过高」「成本失控」。
- **建议**：按 `docs/research/dsh-context/` 的 P0 路径做数据基座：逐调用 usage 事件、上下文快照（system prompt 分段/tool schema/注入来源）、压缩/spill 事件、成本字段；UI 先做会话内 Context tab，再做跨会话洞察页。
- **估算**：**9–12 人天**（会话内 P0）。

#### P1-6 完成/失败无通知（PM-10 仍成立）

- **症状**：后台会话、自动化跑完/失败、审批等待都没有任何应用外或应用内通知；用户只能回来看侧栏未读点或自动化历史。
- **根因**：`contracts.Notifier` 只连到应用内 toast；`internal/modules` + `internal/host` 没有任何 `Notify*` 调用；无 OS 通知通道。
- **证据**：`internal/contracts/notify.go:5-40`；`internal/uiroot/notifier.go`；`internal/ui/toasts/toasts.go:211-227`；grep `NotifySuccess|NotifyInfo|NotifyWarning|NotifyError` 于 `internal/modules`+`internal/host` = 0；CHANGELOG `PM-10`（`CHANGELOG.md:257`）。
- **影响**：无人值守的核心体验缺口——任务结束/失败/等人，用户不知道。
- **建议**：① 后端在 run 结束/失败/审批 pending 时发 toast（先做应用内，成本低）；② 评估 OS 通知能力（mygo/桌面平台）后补系统通知；③ 设置页「通知」文案改为真实语义。
- **估算**：**3–5 人天**（应用内 + 设置；OS 通知另需 spike）。

#### P1-7 调度只触发「新一轮」，不「接着上次跑」；无常驻形态

- **症状**：recurring 自动化每次新建会话，长任务的上下文/进度不连续；无 weekly/cron；无 daemon，关窗即停。
- **根因**：`runTask` 固定 `NewSession`；调度表达式只支持 6 种；产品没有 service 形态。
- **证据**：`internal/modules/automation/automation.go:712-766`；`internal/modules/automation/schedule.go:1-30`；`internal/host/tray.go:59-72`。
- **影响**：周期性长任务（日报/巡检/迁移）每次都从零开始，无法利用上次结果；跨天运行依赖用户不关电脑。
- **建议**：可选「任务会话复用」模式；补 weekly/cron；明确「常驻运行用 ACP/headless」或提供 tray 保活/服务化。
- **估算**：**4–6 人天**。**依赖**：P0-4。

#### P1-8 崩溃/中断后不续跑，UI 无「上次被打断」标记

- **症状**：应用崩溃/被杀后，下次打开看上次会话像正常结束；被中断的 run 不自动继续；目标已丢。
- **根因**：rig checkpoint 只做语义收尾（补 unknown 结果 + assistant 标记），不重放；产品不展示中断状态；goal 不持久。
- **证据**：`$RIG/agent/agent.go:445-471`、`$RIG/agent/checkpoint.go:20-72`；`internal/modules/agent/agent.go:2617-2651`；`internal/modules/agent/agent.go:925-944`（restore 只回放失败/截断）。
- **影响**：用户不知道上次跑到哪、为什么停；长任务需要人工重建上下文。
- **建议**：restore 时展示「上次运行被中断（时间/原因）」卡片 + 「继续」按钮；goal 恢复为 paused/disarmed；可选「自动恢复无人值守任务」。
- **估算**：**2–3 人天**。**依赖**：P0-1/P0-2。

### P2（体验/一致性/审计）

| # | 问题 | 证据 | 建议 | 人天 |
|---|---|---|---|---|
| P2-1 | Fork 只复制 user/assistant 文本，丢工具调用、不感知压缩；副本上下文与源不等价 | `internal/modules/agent/agent.go:1434-1510`（`forkHistoryMessages` 只保留两种角色） | fork 复制事件前缀（工具调用/tool result），标注压缩边界；与 DSH fork 语义对齐 | 3–4 |
| P2-2 | `/compact` 破坏性写回，无 durable 事件；自动压缩不落盘、每次 run 重算 | `internal/modules/agent/compact_now.go:170`；`$RIG/agent/agent.go:494-511` | 压缩改为 append-only/bracket 事件（保留原始历史）；自动压缩结果落盘或至少记录事件 | 4–6 |
| P2-3 | 续跑 prompt 以普通 user 消息进 transcript（用户看到自己没打过的「请继续推进目标…」），无归属标记 | `internal/ui/session/session.go:1330` | 用 goal-sourced 消息源标记（对齐 DSH `GoalMessageSource`），transcript 显示「目标续跑」而非用户气泡 | 1–2 |
| P2-4 | todo 列表单例、不持久、不按会话隔离 | `internal/modules/toolkit/toolkit.go:146`；rig `tools/misc/todo.go` | 按会话持久化；模型跨轮/重启看到自己的计划 | 1–2 |
| P2-5 | 自动化 run 会话只增不减 | `internal/modules/automation/automation.go:712-766`（无 DeleteSession/prune） | 保留策略 + 清理入口 | 1–2 |
| P2-6 | 无 run 超时；长任务只能人工 Stop | `internal/modules/agent/agent.go` | 可选 run deadline（按任务/会话），超时转「中断」而非失败 | 1–2 |
| P2-7 | 无成本/价格；无 token 硬预算 | `internal/contracts/modelcatalog.go`（无价格字段） | 与 P1-5 一并做（价格表 + 成本页） | 含 P1-5 |

> 不列为问题（与 DSH 同为限制或有意取舍）：模型 fallback（DSH 也没有）、后台 job 不跨重启（DSH jobs-local 也不跨）、审批默认 `ask`（DSH 默认也是 ask）、无 `maxSteps`（DSH 也没有）。

---

## 3. 修复路线（建议）

> 原则：**先把长任务的「生命周期」立起来，再补编排与观测**。P0 之间强耦合（goal 契约影响 P0-2/P0-6/P1-2/P1-8），建议同一批做。

### Wave 1 — 让长任务「可信」（P0，约 20–30 人天）

| 工作项 | 内容 | 依赖 | 人天 |
|---|---|---|---|
| **Goal v2** | 会话级 durable goal（事件源 + revision CAS）；blocked/armed/轮次；引擎侧续跑驱动（复用 rig DSH 工具与 GoalRoundDriver，或产品自研同语义）；UI dock 显示轮次/停止原因；headless/ACP 同样续跑 | 契约冻结 | 8–12 |
| **上下文安全默认** | 按模型窗口推导预算并默认开压缩；`/compact` 可用；溢出自动压缩 + 1 次重试；压缩写事件 | 无 | 4–6 |
| **自动化真生命周期** | run 四态（delivered/running/completed/failed/interrupted）+ 会话关联 + 完成回写 + 清理策略 + UI 文案 | 无 | 5–7 |
| **无人值守兜底** | 任务级审批策略（超时拒绝/never/通知等待）+ 审批 pending 状态 + 应用内通知 | 自动化 run 状态 | 3–5 |
| 验收 | `go test` 相关包 + 帧测试（goal/自动化/审批）+ 快照 + designlint；E2E：设 goal→切会话→回来看结果；重启恢复；自动化失败可见 | | |

### Wave 2 — 让长任务「更强」（P1，约 25–38 人天）

| 工作项 | 内容 | 人天 |
|---|---|---|
| 可观测性 P0 | 逐调用 usage、上下文快照、压缩/spill 事件、成本；会话 Context tab | 9–12 |
| 执行域持久化 | 排队落盘；job/terminal 生命周期改按会话持有（工作区切换不杀）；team cold resume | 8–13 |
| 编排补档 | workflow（+ralph 评估）；subagent/team 预算对齐；异步委派 | 4–6 |
| 调度增强 | weekly/cron；同任务会话复用；错过补跑策略 | 4–6 |
| 崩溃恢复体验 | 中断卡片 + 继续按钮 + goal disarm 恢复 | 2–3 |
| 通知 | 应用内完成/失败/审批通知 → OS 通知 spike | 3–5 |

### Wave 3 — 打磨（P2，约 8–12 人天）

fork 事件前缀、压缩 append-only、goal-sourced 消息归属、todo 持久化、自动化会话清理、run 超时、成本页。

**总量级**：P0 20–30 人天；到「与 DSH 长跑等价」的 P0+P1 约 53–80 人天（[判断]，不含 daemon 化与 OS 通知平台工作）。

---

## 4. 需要你拍板的事项

1. **Goal 的数据模型**：直接采用 DSH 三件套（`get_goal/create_goal/update_goal` + revision CAS + blocked + 256 轮）并保留旧 `goal` 作为兼容别名？还是只做「最小修复」（绑定 sessionID + 持久化 + 可配轮数）？→ 影响 3–5 人天与契约稳定性。
2. **续跑预算与重启语义**：默认轮数定多少（现状 5，DSH 256；建议 8–12，按目标可配）？重启后是「自动续跑」还是「必须用户 resume」（DSH 选后者，更安全）？
3. **自动化语义**：UI/历史是否接受「已投递」与「已完成」两个不同状态（建议接受）？recurring 任务是否默认复用同一会话（建议可选、默认关，避免上下文污染）？
4. **无人值守审批**：选「超时自动拒绝（记原因）」「任务级 never」「通知后等待」中的哪一种为默认？（建议：自动化任务默认「通知后等待 + 超时拒绝」，用户可在任务上改 never。）
5. **编排范围**：workflow/ralph 是否进入产品工具面？（workflow 建议做；ralph 实验性，可只做开关不默认启用。）
6. **常驻形态**：是否投入 daemon/service/开机启动，还是明确「长任务请用 ACP/headless 常驻进程」？（影响 P0-6 的做法与预算。）
7. **范围与节奏**：先做 P0-only，还是 P0+P1 一起排？（建议 P0 Wave 1 先交付，可分 2 个 PR：Goal v2、上下文+自动化+审批。）

---

## 5. 验证、证据与限制

### 本次验证

- **[实测 PASS]** `go test ./internal/regression -run 'TestGoal|TestAutomation' -count=1`（Lead 亲自跑，1.768s）。
- **[实测 PASS]** `go test ./internal/modules/toolkit -run 'TestWorkspaceSwitchReleasesOldRootJobs|TestWorkspaceSwitchClosesRetiredBuild' -count=1`（Lead 亲自跑，0.027s）。
- 队友实跑（见 `02-zeokeel-baseline.md` §附录A）：`ui/session TestGoal*`、`agent TestRepeat|Spill|Classify|Compact|Cancel`、`sessionstore` 持久化/checkpoint、`automation`+`hosttools` 等。
- DSH 数字抽查：`defaultMaxGoalRounds=256`、`blockedAfterConsecutiveRounds=3`、`round-limit`、compaction `0.8/65536/0.16`、ralph `maxRounds=256`、llm retry 默认值（见 `01-dsh-capabilities.md` §附录A）。

### 限制与未验证项

1. **DSH 侧为安装产物 0.2.1-rc.2**，不是远端最新；部分语义（schedule/team 的实现细节）来自包 README，已标注 [源码]/[判断]。
2. **ZeoKeel 侧为 v0.2.35 静态代码 + 定向测试**，未做完整 E2E（如真实 provider 长跑、崩溃注入、托盘/最小化时续跑）。
3. **待确认**：headless 的 `auto` 审批是否会被同 dataDir 的 `agent.permission` 设置覆盖（源码风险，未实测）；桌面最小化/托盘时 goal 续跑是否仍推进（依赖 mygo 帧循环）；OS 通知在 mygo 的能力边界（需 spike）。
4. **对 `02-zeokeel-baseline.md` 的修正已采纳**：provider 层存在 rig 默认 2 次有界重试（429/408/425/5xx/传输，500ms 起指数退避，尊重 Retry-After），不是「完全没有重试」；真正的差距是「无产品级 retry 策略、无 durable retry 事件、无上下文溢出重试」。
5. 估算人天为 [判断]，未做迭代排期；实际受契约改动、rig 升级、UI 快照/设计检查影响。

### 交付物（本目录）

| 文件 | 内容 |
|---|---|
| `00-report.md` | 本文：结论、问题清单、路线、拍板项 |
| `01-dsh-capabilities.md` | DSH D1–D10 能力档案（501 行，源码行号） |
| `02-zeokeel-baseline.md` | ZeoKeel D1–D10 现状（369 行，代码/测试行号 + 已知限制核实） |
| `03-gap-matrix.md` | 10 维差距矩阵 + Top 10 落差 + 证据 |

> 本报告未修改任何产品代码；`git status` 仅新增 `docs/research/long-task/`。
