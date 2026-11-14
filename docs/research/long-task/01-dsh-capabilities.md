# DSH 长时任务能力档案（0.2.1-rc.2，源码级）

> 对象：本机安装的 DSH（DeepSeek Harness）CLI 包 `@deepseek-ai/dsh@0.2.1-rc.2`。
> 根：`$DSH=/usr/local/lib/node_modules/@deepseek-ai/dsh`；能力实现全部在 `$DSH/node_modules/@deepseek-ai/dsh-*`（305 个包）。
> 方式：以「长时任务 / long-horizon task」为主轴，按 D1–D10 逐维建档；先给结论，再给机制、数字、可见面与证据行号。
> 证据约定：`node_modules/@deepseek-ai/<pkg>/lib/index.js:LINE` 均相对 `$DSH`；`README.md` 亦相对包目录。判定级别：[实测] 可复现命令 / [源码] `file:line` / [判断]（作者推断，已显式标注）。
> 版本核对命令见 §附录A。行号基于 2026-11-14 本机安装产物；升级后需重跑。

---

## 0. 一页结论（TL;DR）

DSH 的「长跑」不是单一 `maxSteps` 循环，而是**六层可独立替换的机制叠加**：

1. **目标域（D1）**：`dsh-goal` 把「一个完成目标」做成一等持久对象（session log 事件源），默认 256 轮自动续跑上限；`dsh-goal-round-driver` 只在 agent 空闲且 goal 处于 `armed` 时排队续跑；模型只能通过 `get_goal/create_goal/update_goal` 改状态，且创建/编辑/暂停/恢复必须来自**直接人类输入**。
2. **循环无步数上限（D2）**：`dsh-agent-loop` 的 step 循环是 `while (true)`，turn 循环是 `while (await this.turn())`——只要 inbox 还有输入就一直跑；预算不放在循环里，而分布在 goal 轮数、context 压缩阈值、单请求 `maxTokens`、工具超时与 retry 策略上。
3. **事件源持久化 + 语义收尾（D3）**：session 是 append-only 事件日志；`dsh-session-checkpoint-policy` 在「模型请求前 / 顶层工具执行前 / 下一步前」三个语义点 flush；崩溃后由 `interruptedTurnClosers` 合成 `TOOL_OUTCOME_UNKNOWN` 收尾，而不是盲目重试。
4. **上下文是显式的工程问题（D4）**：`dsh-token-meter` 统一度量；`dsh-compaction-basic` 默认在 `min(W×0.8, W−O−65536)` 处触发，保留最近 16% 的 `W−O`；工具结果先按 8192/4096/1024 字符裁剪，图片按 provider 拒绝信号永久 offload，超大工具输出经 spill 落盘（base 默认 `maxInlineTokens: 12500`）。
5. **编排是多档位的（D5）**：subagent（spawn/fork，`maxDepth=1`、`maxActiveSubagents=8`）→ Team（`maxMembers=16`、`maxTasks=256`、9 个工具 + 共享任务板）→ workflow（脚本 fan-out，`maxTotalAgents=1000`、并发 `min(16, parallelism−2)`）→ ralph（fresh-agent 轮次，`maxRounds=256`）。各自独立的预算，不互相借用。
6. **调度与无人值守分离（D6）**：schedule 的 6 种触发器持久化在 Host 侧、绑定原 session，需要 Web Session controller；jobs 是**进程内**的（重启即死，但生命周期事件持久化）；headless 是「一次一任务后退出」，ACP/SDK 是常驻协议形态。真正无人值守长跑 = 常驻 Host + goal driver + schedule/jobs + 持久化（[判断]）。

对 ZeoKeel 最值得照抄的三点见 §附录C（[判断]）。

---

## D1 目标与续跑（Goal & continuation）

**结论**：DSH 把「目标」与「执行轮次」彻底拆开，且用 5 个包分别实现四种角色：
- 状态：`dsh-goal`（`GoalService`，`ctx.goals`）
- 模型面：`dsh-tool-goal`（`get_goal` / `create_goal` / `update_goal`）
- 调度：`dsh-goal-round-driver`（同一 session 内自动续跑）
- 人类面：`dsh-command-goal`（`/goal`）+ `dsh-client-ui-goal`（Web composer 状态条）

每个 session 至多一个 current goal；所有变更写入 session log（事件 `goal/change` v1），因此跨 turn、跨 resume、跨进程重启都在；**唯一的例外**是「自动续跑是否 armed」——它是进程内状态，`agent/created` 边沿一律置为 `disarmed`，resume/fork 后必须由人类显式 resume 才会重新续跑。

**机制（关键符号）**
- 四相状态机：`active | paused | blocked | complete`（`PHASES`，`node_modules/@deepseek-ai/dsh-goal/lib/index.js:41`）；可快照操作 `create/edit/pause/resume/complete/block`（`:33`）。`clear` 写带 revision 的 tombstone。
- 严格 replay fold：拒绝不连续 revision、非法相位迁移、非单调时间戳、非顺序 admitted round（`goal/lib/index.js:170-205`、`:277-278`）。
- Compare-and-set：`get()` 返回 detached `GoalView`，变更必须携带精确 `{id, revision}`；乐观并发用 revision 拒绝 stale 写入（`goal/lib/index.js:636-697`）。
- `resume` 的硬约束：轮数已耗尽时拒绝（`goal/lib/index.js:697`）；模型**不能** resume 一个 durable `paused` goal（那是用户路径）——`GOAL_TOOL_RESUME_PAUSED`（`node_modules/@deepseek-ai/dsh-tool-goal/lib/index.js:352`）。
- 续跑驱动：`readyToDrive` 要求「fiber 已就绪 + 未 stopping + 同一 live agent + `status === "idle"` + 无竞争入队」（`node_modules/@deepseek-ai/dsh-goal-round-driver/lib/index.js:85`）；每轮向 inbox 投递一条 user 角色 `<goal_round>`，内容含 JSON 转义 objective、`Round: n/cap` 与工作指令（`:14-16`）。
- 轮次计数只认「真正进入历史的 goal 消息」：reservation → `agent/pre-step` 校验 → 只有 admitted `user/message` 才 `roundsStarted += 1`（`goal/lib/index.js:277-278`；驱动侧 race fence `goal-round-driver/lib/index.js:289-301`）。
- 轮数耗尽不是静默停止，而是自动落 blocker：`code: "round-limit"`，message 为 `Goal reached its configured limit of N rounds.`（`goal-round-driver/lib/index.js:135-142`）。
- 模型侧终止策略：goal 轮里可以 `complete`（立即生效），但 `blocked` 在非人类回合必须已积累 `blockedAfterConsecutiveRounds`（默认 3）轮才允许，且 `blocked_reason` 必填，写稳定 code `model-reported`（`tool-goal/lib/index.js:360`、`:361-366`）。
- 权限来源：`get_goal` 只要求 live agent + open turn；`create/edit/pause/resume` 还要求该 turn 内存在 runtime-root agent 的 `user/message`（`tool-goal/lib/index.js:50-64`、`:285-296`）。这保证 subagent / scheduler / 插件不能自己造目标。
- 人类命令：`/goal`、`/goal <objective>`、`/goal edit …`、`/goal pause|resume|clear`；附件会作为一条普通 user followup 进入历史（`dsh-command-goal` README 的 command reference）。

**默认值与上限**

| 项 | 默认/上限 | 证据 |
|---|---|---|
| `defaultMaxGoalRounds` | `256` | `node_modules/@deepseek-ai/dsh-goal/lib/index.js:588`、`:593` |
| 单次 create 覆盖 | 任意正整数（`>0` safe integer） | `goal/lib/index.js:456`、`:465-469` |
| 模型 blocked 阈值 `blockedAfterConsecutiveRounds` | `3` | `node_modules/@deepseek-ai/dsh-tool-goal/lib/index.js:115` |
| 每 session current goal 数 | 1（未完成必须先 edit/pause/resume/block/clear） | `goal/lib/index.js:639` |
| goal/driver/tool/command 是否 base 默认启用 | 全部启用 | `node_modules/@deepseek-ai/dsh-base/cordis.patch.yml:323-330`、`:444-445` |

**模型可见 vs 产品面**：三个 tool（模型可用）+ 一个 slash command（人类）+ Web 状态条（人类）。goal 状态本身不注入 prompt，只有续跑 prompt 会渲染 objective/round。

**UI 入口**：Web composer-context 栈的第 2 张卡（Todo 之后、Queue 之前），有 goal 时才出现；armed active 提供 pause，active-disarmed/paused 提供 resume，edit 走 inline editor，clear 直接删；`/goal` 运行以 `Command input` 气泡出现在 transcript（`dsh-client-ui-goal` README）。

**判定**：[源码]。核心数字与状态机均已在 lib 中逐行核对。

---

## D2 循环与预算控制（Loop & budget）

**结论**：DSH 的 agent loop **没有** `maxSteps` / `maxIterations` / `maxTurns`。全仓 `lib/*.js` 检索这些标识符为空（附录A 命令 5）。停止条件只有四类：
1. inbox 不再有待处理输入（`turn()` 返回 false）；
2. 取消（`agent.cancel(cause)`，cooperative）；
3. `agent/pre-step` 决策为 `reject`（如 plan-mode 拒绝、driver fence 拒绝）；
4. 模型流错误经 `agent/request-error` 无人处理 → terminal。
「预算」由外挂策略承担，而不是循环内计数。

**机制（关键符号）**
- 双层循环：`kick()` 里 `while (await this.turn())`（`node_modules/@deepseek-ai/dsh-agent-loop/lib/index.js:889`）；`turn()` 里 `while (true)` 逐步执行，条件为 `turnEnds && inbox.nextStep.length === 0` 才 break（`:953-1021`）。
- 一个 step = 一次 model request + 该次响应触发的全部工具调用；token 用量按 step 累积（`sessionStats.steps` 计 closed steps，`dsh-session-stats` README）。
- 并行工具预算：`maxParallelToolCalls` 默认 10，exclusive 调用作为 ordering barrier（`agent-loop/lib/index.js:1297`、`:1554`；`DEFAULT_MAX_PARALLEL_TOOL_CALLS`）。
- 输出 token 预算：`AgentOptions.maxTokens` 是**单请求**输出上限（`dsh-agent` README；`dsh-agent-loop` README 的 `agents[].maxTokens`）。没有「整轮 token 预算」或「成本预算」的硬停止。
- 重复调用防护：`dsh-repeat-tool-reminder` 按工具调用链阈值 `[3, 5, 8]` 注入提醒（base 配置 `node_modules/@deepseek-ai/dsh-base/cordis.patch.yml:447-452`；默认阈值数组在 `node_modules/@deepseek-ai/dsh-repeat-tool-reminder/lib/index.js:2118`）。这是提醒不是拦截。
- 工具超时：`dsh-tool-call-timeout-policy` 使用「工具自己的配置」做 cooperative deadline，超时映射为模型可读错误；不硬杀（`dsh-tool-call-timeout-policy` README）。
- 上下文预算：见 D4（compaction 阈值）。
- 度量而非决策：`dsh-token-meter` 明确「nothing in the harness makes decisions from occupancy」，compaction 读 `measure()`（`dsh-token-meter` README）。

**默认值与上限**

| 项 | 默认/上限 | 证据 |
|---|---|---|
| 每 turn 最大 step 数 | **无上限** | `agent-loop/lib/index.js:953`（`while (true)`） |
| 每 session 最大 turn 数 | **无上限** | `agent-loop/lib/index.js:889` |
| 每 step 并行安全工具调用 | `10`（`1` = 串行） | `agent-loop/lib/index.js:1297`、`:1554` |
| 重复工具提醒阈值 | `[3, 5, 8]` | `dsh-base/cordis.patch.yml:447-452` |
| 单请求输出 token | 由 `AgentOptions.maxTokens` 给出，无默认上限 | `dsh-agent` README / `agent-loop` README |
| 整轮/整 session token·成本硬预算 | **未找到**（只有度量与压缩） | 附录A 命令 6 检索为空 |
| 取消语义 | cooperative，保留已流出的文本 | `agent-loop` README「Cancellation is cooperative」；`:1097-1109` interrupted anchor |

**模型可见 vs 产品面**：循环参数对模型不可见；`maxParallelToolCalls` 是产品设置项（见 D8 UI）。模型只能感知「继续给出工具调用」或「结束回合」。

**UI 入口**：**Plugins → Agent loop** 卡片里的 Parallel tool calls（`dsh-client-ui-settings-agent-loop` README）；无「最大步数」设置，因为不存在该参数。

**判定**：[源码] + [实测]（全仓检索无 `maxSteps` 类标识符）。

---

## D3 持久化与恢复（Persistence & recovery）

**结论**：DSH 用「append-only session 事件日志 + flush 屏障 + load-time 语义收尾」实现可恢复长跑：
- 写路径：`dsh-session-persistence` 抽象 seam，ship 的 `dsh-session-persistence-jsonl` 每 session 一个 `.jsonl.zstd`；
- 默认**有 200ms 批处理窗口**（延迟写），但 `session/flush` 是明确 durability barrier；
- `dsh-session-checkpoint-policy` 在三个语义点强制 flush：模型请求前、顶层工具 body 前、下一步前；
- 崩溃/重启：读 physically valid prefix，`interruptedTurnClosers` 合成 `tool/result` 错误、开放的 `step/end`、`turn/end {interrupted}` 作为普通批次 append；
- **后台 job 不跨重启**（jobs-local 纯内存记录，只有生命周期事件随 session log 持久化）。

**机制（关键符号）**
- `Session.append()` 接受类型化事件，snapshot+freeze+lossless JSON 校验；`deriveMessages()` 增量投影（`dsh-session` README）。
- JSONL 写入：live `session/event` 进有界缓冲，首个事件启动固定批窗口 `setTimeout(..., 200)`（`node_modules/@deepseek-ai/dsh-session-persistence-jsonl/lib/index.js:190-196`）；`drainLive` 失败保留批次供下次 flush 重试（`:198-220`）；压缩默认 `zstd`（`:2290`），文件扩展名 `.jsonl.zstd`（`:750-756`）。
- 三检查点：`llm/stream` 之前 flush（`node_modules/@deepseek-ai/dsh-session-checkpoint-policy/lib/index.js:61`）；`tools/execute` 顶层调用前 flush（`:66`）；`agent/pre-step` flush（`:72`）。失败 fail-closed：adapter/工具 body 不执行。
- 崩溃收尾：`interruptedTurnClosers(events)`（`node_modules/@deepseek-ai/dsh-session/lib/index.js:907`）基于 `openTurnClosers`（`:750`）生成合成错误；两种错误文本 `TOOL_OUTCOME_UNKNOWN` / `TOOL_NOT_STARTED`（`:728-733`）；agent-loop 在 resume 时 append（`node_modules/@deepseek-ai/dsh-agent-loop/lib/index.js:1992`）。
- 恢复语义：resume 以 `open(id, 'write')` 先取写所有权，再读日志、append closers，然后才发布 agent（`agent-loop` README「Persistence integration」）。语义修复属于 agent 层，不是 storage 层。
- goal 状态：完全靠 session log replay（`goal/change` v1，`goal` projection stateVersion 6，`node_modules/@deepseek-ai/dsh-goal/lib/index.js:440-452`）。
- schedule 状态：独立 durable 域（`schedule.json` slot），与 session log 分开（`dsh-schedule` README「Storage, dispatch, and ownership」）。
- jobs：`dsh-jobs-local` 内存记录，进程退出即消失；registration/settlement 事件同步写入原 session（`dsh-jobs-local` README「Session timing records」）——所以**能看到历史，但不能续跑**。

**默认值与上限**

| 项 | 默认/上限 | 证据 |
|---|---|---|
| 落盘格式 | 每 session 一个 `.jsonl.zstd`（zstd 默认开） | `session-persistence-jsonl/lib/index.js:2290`、`:750-756` |
| live 事件批处理窗口 | `200 ms` | `session-persistence-jsonl/lib/index.js:190-196` |
| 持久化检查点 | 3 个（模型请求前、顶层工具前、下一步前） | `session-checkpoint-policy/lib/index.js:61`、`:66`、`:72` |
| 单 session 写者 | 1（`SessionAlreadyOwnedError`） | `dsh-session-persistence` README |
| 后台 job 跨重启 | **否**（内存 registry） | `dsh-jobs-local` README；`dsh-jobs` README「Jobs managed by the shipped in-process registry die with the harness process」 |
| schedule 跨重启 | **是**（Host 任务表 + 历史投递记录） | `dsh-schedule` README |

**模型可见 vs 产品面**：模型只看到「恢复后的历史」和合成的 unknown-outcome 工具结果；持久化本身不可见。

**UI 入口**：Session Header → more actions → `Download session log`（ZIP）；`/export` 命令（`dsh-session-log-export` README）。

**判定**：[源码]。200ms 窗口、zstd、三检查点、closers 均已核对；「jobs 不跨重启」为 README 明示。

---

## D4 上下文管理（Context & compaction）

**结论**：DSH 把「上下文不爆」拆成五件独立可组合的事：
1. **度量**（`dsh-token-meter`）：replay 式确定性估算，`tokenUsage` / `contextPressure` / `contextBreakdown` 三个 projection；
2. **摘要压缩**（`dsh-compaction-basic`）：压力触发 + 溢出恢复 + `/compact` 手动；
3. **工具结果裁剪**（`dsh-compaction-tool-result-pruner`）：仅在触发后裁剪超大 tool result；
4. **图片 offload**（`dsh-compaction-image-offload`）：provider 拒绝时永久替换为占位文本；
5. **超大输出 spill**（`dsh-spill-policy` + `dsh-spill-local`）：超预算文本写文件，模型只看到 head/tail + 路径。
没有独立「memory」体系：长期记忆 = session log + workspace + skills（[判断]，全仓无 `dsh-memory*` 包）。

**机制（关键符号）**
- 压缩契约：`dsh-compaction` 的 `CompactionEngine` 三个操作 `compactIfNeeded` / `compactNow` / `compactRegion`；日志括号 `compaction/start` → `compaction/summary` → 一次 surface 替换 → `compaction/end`；替换内容是 `<compacted-summary>` 包裹的 user message；shadowed 事件保留在 raw log（`dsh-compaction` README）。
- 自动触发：`agent/pre-step` 监听器先测压，再 prune，再挑「最老的 balanced span」摘要（`node_modules/@deepseek-ai/dsh-compaction-basic/lib/index.js:839-841`）。
- 溢出恢复：`agent/request-error` 上识别 `CONTEXT_WINDOW_EXCEEDED`，跳过正常阈值与保留策略，做一次 maximal balanced head reduction，只有 surface replacement generation 前进后才允许 retry（`compaction-basic/lib/index.js:861-872`）。
- 阈值公式：`thresholdTokens = floor(min(contextWindow × thresholdRatio, messageBudgetTokens − headroomTokens))`，`retainTokens = floor(messageBudgetTokens × retainRatio)`（`compaction-basic/lib/index.js:132-133`）。
- 摘要复用 provider 暖前缀：重放 surface node 0 的 system message、最近请求 tools、shadowed 区消息，最后追加压缩指令；`GenerateOptions.purpose = "compaction"`；只保留返回文本（`compaction-basic` README「Summarization mechanics」）。
- 裁剪：仅当压缩触发成立后运行；按 Unicode code point 切；`compaction/prune` 影子价事件 + 替换 `tool/result`（`node_modules/@deepseek-ai/dsh-compaction-tool-result-pruner/lib/index.js:8-13`）。
- spill：`maxInlineTokens` 预算内保留 head/tail，其余写 `…-web_fetch.txt` 类文件并给模型可 `read`/`grep` 的路径（`dsh-spill-policy` README）。
- 可观测性：压缩后仍可看到 `contextBreakdown`（system/tools/message 估算）与 shadow price；压缩本身不删日志。

**默认值与上限**

| 项 | 默认/上限 | 证据 |
|---|---|---|
| `thresholdRatio` | `0.8` | `dsh-compaction-basic/lib/index.js:15`、`:66` |
| `headroomTokens` | `65536` | `compaction-basic/lib/index.js:63` |
| `retainRatio` | `0.16`（`W−O` 的比例） | `compaction-basic/lib/index.js:17`、`:67` |
| summary 输出上限 `maxTokens` | 默认 = `headroomTokens` = `65536` | `compaction-basic/lib/index.js:64` |
| `compactionRetries` | `1` | `compaction-basic/lib/index.js:82` |
| `maxOverflowRetries` | `1` | `compaction-basic/lib/index.js:83` |
| `auto`（自动压缩+溢出恢复） | `true` | `compaction-basic/lib/index.js:85` |
| 工具结果裁剪 | `8192` 字符触发，保留 head `4096` + tail `1024` | `dsh-compaction-tool-result-pruner/lib/index.js:11-13`；base 显式同值 `dsh-base/cordis.patch.yml:425-431` |
| spill 内联预算 | `12500` estimated tokens | `dsh-base/cordis.patch.yml:414-417` |
| 图片 offload | 无配置；`IMAGE_OFFLOAD_REQUIRED` 触发 | `dsh-compaction-image-offload` README |
| `/compact` | 手动立即压缩（含阈值以下） | `dsh-command-compact` 挂载后可用 |

**模型可见 vs 产品面**：压缩后的摘要对模型可见（user message）；工具裁剪与图片 offload 直接改变模型看到的工具结果；`contextPressure`/`contextBreakdown` 是产品面 projection，不进 prompt。

**UI 入口**：`/compact` 命令；Chat composer 统计条（token total / cache hit）与每 turn 可展开 usage 行（`dsh-client-ui-chat` README「Turn token usage」）。

**判定**：[源码]。阈值公式与所有默认数字均为源码行核对。

---

## D5 编排（Orchestration）

**结论**：DSH 提供四档编排，预算互相独立：
- **subagent**：一次委派一个子 session，spawn（干净上下文）或 fork（继承父已完成 turn）；`maxDepth=1`、`maxActiveSubagents=8`；
- **Team**（experimental）：一个 Lead + 具名 teammate + 共享 durable task board，9 个模型工具；
- **workflow**：模型写 JS 脚本 fan-out，引擎在受限 Node 进程执行；`maxTotalAgents=1000`；
- **ralph**：fresh-agent 每轮只带上一轮 bounded report，`maxRounds=256`，默认关闭、需产品面开启。

**机制（关键符号）**
- subagent seam：`ctx.subagents.startActivation()` 返回 `{childId, result, dispose}`；`delivery: 'parent'` 由 runtime 发完成通知；local child 可继续发消息、可 cold resume；external（ACP/SDK/Codex/Claude Code）只执行一次（`dsh-subagent` README）。
- 容量：`maxActiveSubagents` 在**每个 activation tree** 内计数（root 不占位），到限抛 `ACTIVATION_LIMIT_REACHED`，**不排队**（`node_modules/@deepseek-ai/dsh-subagent/lib/index.js:2979-2980`）。
- fork 语义：种子止于父最后一个 **completed turn**，当前 in-flight turn 不含；种子是一次性快照（`dsh-subagent-fork-in-process` README）。
- Team 域：roster 不可变（名字永不复用）、task board CAS、`ownerName`、`inactive` 只表示「没有 turn 在执行」；`wait_agent` 无变化立即返回 `noProgress`（`dsh-experimental-agent-team` / `dsh-experimental-tool-agent-team` README）。
- workflow：`agent()`/`parallel()`/`pipeline()`/`phase()`/`log()`；fatal `WorkflowError` 杀死脚本，普通 child 失败映射为 per-item `null`；`run_in_background: true` 注册为 owned `workflow` job（`dsh-workflow` / `dsh-tool-workflow` README）。
- workflow 并发：`maxConcurrentAgents: 0` 时取 `min(16, max(1, availableParallelism()−2))`（`node_modules/@deepseek-ai/dsh-workflow-ptc/lib/index.js:631`）。
- ralph：固定脚本，模型只能传 `{objective, maxRounds?}`；每轮 child 只收到 objective、round、cap、workspace-as-authority 指令与上一轮结构化 handoff；report 状态 `continue|complete|blocked`；非法/超长 report 直接失败（`dsh-tool-ralph` README）。

**默认值与上限**

| 编排 | 关键数字 | 证据 |
|---|---|---|
| subagent depth | `maxDepth = 1`（`0` 禁止委派） | `node_modules/@deepseek-ai/dsh-subagent/lib/index.js:2979` |
| subagent 并发 | 每 activation tree `8` | `dsh-subagent/lib/index.js:2980` |
| Team 成员 | `maxMembers = 16` | `node_modules/@deepseek-ai/dsh-experimental-agent-team/lib/index.js:1420`、`:1439` |
| Team 任务 | `maxTasks = 256` | `experimental-agent-team/lib/index.js:1421`、`:1440` |
| Team 单消息 | `65536` bytes | `experimental-agent-team/lib/index.js:1422`、`:1441` |
| Team disposal | `5000 ms` | `experimental-agent-team/lib/index.js:1423`、`:1442` |
| workflow 单 run 总 child | `1000` | `dsh-workflow-ptc/lib/index.js:600` |
| workflow 并发 child | `0` → `min(16, parallelism−2)` | `dsh-workflow-ptc/lib/index.js:599`、`:631` |
| workflow 单次 hook item | `4096` | `dsh-workflow-ptc/lib/index.js:601` |
| workflow 同步段超时 | `5000 ms` | `dsh-workflow-ptc/lib/index.js:602` |
| workflow 结果渲染上限 | `50000` chars | `node_modules/@deepseek-ai/dsh-tool-workflow/lib/index.js:68` |
| workflow 后台运行 | `enableRunInBackground = true` | `tool-workflow/lib/index.js` Config（README 表） |
| ralph 轮数 | `maxRounds = 256`（部署值同时是调用覆盖上限） | `node_modules/@deepseek-ai/dsh-tool-ralph/lib/index.js:20`、`:128`、`:145-146` |
| ralph handoff / result | `16384` chars 各 | `tool-ralph/lib/index.js:21-22` |

**模型可见 vs 产品面**：subagent/Team/ralph/workflow 均有模型工具；ralph 与 session search 是 experimental bundle，默认关闭，需在 Plugins 里开启；Team 在 Web/Desktop 也不默认启用（experimental）。

**UI 入口**：session header subagent 区/子 session；Team 面板（experimental）；workflow run 卡与 background job 行；Ralph 在 Plugins → Official 的「Ralph loops」开关。

**判定**：[源码]（数字）+ [源码]（语义，README 明示）。

---

## D6 调度与无人值守（Scheduling & unattended）

**结论**：DSH 把「未来某个时刻让 agent 继续干活」交给 Host 级 `dsh-schedule`：
- 6 种选择器：`after_seconds` / `at` / `every_seconds`（≥60s）/ `daily` / `weekly` / `cron`（5 字段 Vixie）；
- 任务绑定「创建它的 session」，Host 重启后仍存在，每个 recurring 任务只补**最近一次**错过的 occurrence；
- 投递 = 一条 plugin-sourced `followup()` + 成功 `session/flush` 才算 durable；**ack 的是 inbox 投递，不是模型执行**；
- schedule **不能**挂在纯 headless/SDK 组合上：需要 Host Web Session controller + persistence；
- 后台 job（`dsh-jobs-local`）提供「同一 session 内的并行长任务 + 完成通知唤醒」，但不跨进程。

**机制（关键符号）**
- `ScheduleService` 持有 version-1 `schedule` 域与一个 Host timer；活动/非活动任务都可查，删除会移除 task row 与 delivery history（`dsh-schedule` README）。
- 投递：`sessionController.resolveAgent` → `followup()` 同步进 inbox → `session/flush` 成功 → 写 receipt（`scheduledAt`/`deliveredAt`/`messageId`）。crash 在 flush 与 task put 之间会重复投递（README 明示）。
- 历史：`deliveryHistoryDays` 30、`deliveryHistoryRecords` 200；`history()` 分页 1–100，`before` 游标（`node_modules/@deepseek-ai/dsh-schedule/lib/index.js:1880-1882`、`:1977-1978`）。
- archive admission：有 active reminder 的 session 归档会被拒绝，选择停止则删除所有 active reminder（`dsh-schedule` README）。
- 完成通知：job settle 时 busy agent 注入下一步、idle agent 开一个 turn（`completionDelivery: wakeup`）；`maxConsecutiveWakes` 可限制自激励唤醒链（`node_modules/@deepseek-ai/dsh-tool-jobs/lib/index.js:89-92`）。
- 各形态承载：
  - **Web/Desktop**：常驻 Host，schedule + jobs + goal driver 全在 → 唯一真正「无人值守长跑」形态（`node_modules/@deepseek-ai/dsh-web-app/cordis.patch.yml:139-140` 挂 Host schedule；`:398-399` 挂 UI）。
  - **headless**：一次一任务，打印最终答案后退出；exit 0/1；支持 `--json`、`--session-id` 续跑；无交互 follow-up（`dsh-headless` README；Config `task/sessionId/json` 在 `node_modules/@deepseek-ai/dsh-headless/lib/index.js:107-109`）。
  - **ACP**：常驻 stdio JSON-RPC；`session/list`、`session/resume` 可跨进程；resume 重连 MCP 声明但**不重放历史**；禁用模型生成标题（`dsh-acp-app` README）。
  - **SDK**：`sdk` profile 继承 base；`sdk-minimal` 显式排除 jobs/subagents/skills，shell 300s 超时，`danger-full-access`（`dsh-sdk-minimal` README）。

**默认值与上限**

| 项 | 默认/上限 | 证据 |
|---|---|---|
| schedule 最小固定间隔 | `60 s` | `dsh-schedule` README（`every_seconds` 行） |
| schedule 标题长度 | ≤ `120` chars | `dsh-schedule` README |
| delivery history | `30` 天 / `200` 条 | `dsh-schedule/lib/index.js:1880-1882`、`:1977-1978` |
| job 等待默认/上限 | `30_000 ms` / `600_000 ms` | `node_modules/@deepseek-ai/dsh-tool-jobs/lib/index.js:89-90` |
| 完成投递模式 | `wakeup` | `tool-jobs/lib/index.js:91` |
| 连续唤醒上限 | 未设置（无上限） | `tool-jobs/lib/index.js:92` |
| 每 owner 并发 job | `10` | `node_modules/@deepseek-ai/dsh-jobs-local/lib/index.js:340` |
| job 输出环 | `256 KiB` live / `16 KiB` settled | `jobs-local/lib/index.js:342`、`:344` |
| job pull pump | `150 ms` | `jobs-local/lib/index.js:346` |
| headless 任务数 | 每次调用 1 个 | `dsh-headless` README |
| sdk-minimal shell 超时 | `300 s` | `dsh-sdk-minimal` README |

**模型可见 vs 产品面**：`schedule_create/list/update/delete` 是模型工具（standard/cordis/ptc preset 挂）；`minimal` 不挂。job 三工具 `job_output/job_list/job_kill` 同理。headless/ACP/SDK 不消费 `ctx.commands`（无 slash 命令）。

**UI 入口**：Automation tasks 页面 + 右侧 Sidebar task tab + Session header 时钟按钮 + transcript 的 `schedule_create` 卡片；job 列表在 session header（可展开输出面板、两段式 stop）。

**判定**：[源码]（数字）+ [README]（调度语义；实现拆分在 `src/`，未随安装产物发布）。

---

## D7 安全与审批（Safety & approval）

**结论**：DSH 把「能不能无人跑」表达为一个**二维产品选择**：sandbox mode × approval policy，再由 permission preset 打包。默认是保守的 `workspace-write + ask`；`never` 是 CI/无人值守立场；任何审批缺失或失败都 **fail closed**（`unavailable`），不会静默放行。

**机制（关键符号）**
- approval seam：`ctx.approval.request(req)` 需要 open turn；answerer 是 `approval/request` waterfall，缺少 terminal answerer → `unavailable`；`ask`（默认）/`never`；审计事件 `approval/asked` + `approval/decided`（log-only）（`dsh-user-approval` README）。
- 会话级策略：`approval/policy` 事件 + runtime-context 快照 `approval:policy`；switch 会注入一条 sourced user message（`dsh-user-approval` README）。
- base 的无人值守默认式：`policy: (DSH_PERMISSION_MODE ?? 'workspace-write') === 'danger-full-access' ? 'never' : 'ask'`（`node_modules/@deepseek-ai/dsh-base/cordis.patch.yml:259-262`）。
- permission presets：`workspace-write → {sandbox: workspace-write, approval: ask}`、`danger-full-access → {sandbox: danger-full-access, approval: never}`，另有保留态 `custom` 与可选 `auto`（`node_modules/@deepseek-ai/dsh-permission-presets/lib/index.js:50-59`、`:145-158`）；`/permission` 命令切换。
- sandbox：`read-only | workspace-write | danger-full-access`；无法执行请求模式时 `SANDBOX_UNAVAILABLE` 失败而非无约束运行；被拒后模型可**一次性**用 `sandbox_permissions` + `justification` 申请严格更宽模式（`dsh-sandbox` README）。
- 默认 sandbox mode：`read-only`（`dsh-sandbox-policy` 的 fail-safe 默认，`node_modules/@deepseek-ai/dsh-sandbox-policy/lib/index.js:99-102`）；base 显式 pin 到 `workspace-write`（`dsh-base/cordis.patch.yml:243-247`）。会话切换写 `sandbox/mode` 日志事件，重启可重放。
- 取消/中断：`agent.cancel(cause)` cooperative；`keepInbox` 决定是否清空待处理；goal driver 对「host 发起的 pause」会 abort 正在跑的 turn，模型自己 pause 则让本轮正常结束（`dsh-goal-round-driver` README）。
- 目标权限：subagent / 非人类 producer 不能 create/edit goal（D1）。

**默认值与上限**

| 项 | 默认 | 证据 |
|---|---|---|
| approval policy | `ask` | `node_modules/@deepseek-ai/dsh-user-approval/lib/index.js:74` |
| 无人值守式 | `DSH_PERMISSION_MODE=danger-full-access` → `never` | `dsh-base/cordis.patch.yml:262` |
| sandbox 默认 | `read-only`（库） / base pin `workspace-write` | `dsh-sandbox-policy/lib/index.js:102`；`dsh-base/cordis.patch.yml:243-247` |
| 预设 | `workspace-write`：sandbox=workspace-write + approval=ask；`danger-full-access`：danger-full-access + never | `dsh-permission-presets/lib/index.js:145-158` |
| 审批缺失 | fail closed `unavailable` | `dsh-user-approval` README |
| 提权 | 一次调用一次审批 | `dsh-sandbox` README |

**模型可见 vs 产品面**：模型可见当前 policy 的完整语义（runtime-context 快照）与被拒/提权结果；人类 UI 与审计事件对模型不可见。

**UI 入口**：`/permission` 命令 + Permissions selector（Web 设置/会话头）；审批浮层（`dsh-client-ui-approval`）；sandbox 拒绝后的 escalation 提示。

**判定**：[源码]（数字与默认值）。

---

## D8 可观测与控制（Observability & control）

**结论**：DSH 把「长跑中人类怎么看见、怎么插手」做成了 projection + 专用 UI 的组合：
- 状态：`goal` / `job.list` / `permissions` / `tokenUsage` / `contextPressure` / `contextBreakdown` / `sessionStats` 等 projection；
- 控制：`/goal`、job kill、`/permission`、steer/queue、interrupt、`/compact`、`/export`；
- 通知：job 完成唤醒、schedule 投递、goal round 的终局 wrap-up。

**机制（关键符号）**
- goal：`goal` projection stateVersion 6，client view 是 current goal 或 null（`node_modules/@deepseek-ai/dsh-goal/lib/index.js:440-452`）；Web 用 `useProjection('goal')` + `session/projection` 帧；mutation 用 CAS ref（`dsh-client-ui-goal` README）。
- job：`job.list`（whole-set 帧）+ `job.follow`（按绝对 byte offset 的只读输出流）；两者都不触碰模型的消费游标与通知状态；`job.kill` 以 `cancelled by the user` 记录，模型的 completion notice 仍会到达（`dsh-api-job-controller` README）。
- job UI：session header 的 job 列表；running 行两段式 stop（`KILL_ARM_MS = 3000`，失败提示 4000）（`node_modules/@deepseek-ai/dsh-client-ui-jobs/lib/client.js:75-77`、`:379`）；展开面板用内嵌 terminal 流。
- usage：`tokenUsage`（uncachedInput/output/cacheRead/cacheWrite）、`contextPressure`（pressureTokens/projectedTokens/contextWindow）、`contextBreakdown`（system/tools/message 估算）（`dsh-token-meter` README）；Chat 的每 turn usage 行与 composer 统计条（`dsh-client-ui-chat` README）。
- sessionStats：turns / steps / llmMs / toolMs / ttftMs / ttftSteps / decodeMs / decodeTokens，来自完整日志，分页/压缩不影响（`dsh-session-stats` README）。
- 会话干预：`send_message`（Team/子 agent）、`steer`（nearest step boundary）、`queue`（next turn）、`interrupt_agent`（Team Lead only）、queue 的 Edit/Remove（`dsh-api-session-controller` README）。
- 交付物：changed-files 卡 + `present` 工具 + 文件预览（`dsh-client-ui-deliverables` README）。

**默认值与上限**

| 项 | 默认/上限 | 证据 |
|---|---|---|
| job 观察合窗 | `100 ms` | `node_modules/@deepseek-ai/dsh-api-job-controller/lib/index.js:251`、`:309` |
| job 观察单帧字节 | `65536`（软预算，超大一整块照发） | `dsh-api-job-controller/lib/index.js:253`、`:310` |
| stop 二次确认窗口 | `3000 ms` | `dsh-client-ui-jobs/lib/client.js:75` |
| 每 turn usage 显示 | 需要完整 window + 精确 usage，否则隐藏 | `dsh-client-ui-chat` README |
| goal UI 位置 | composer-context 第 2 张卡（Todo 后、Queue 前） | `dsh-client-ui-goal` README |

**模型可见 vs 产品面**：projection/UI/审计是产品面；模型只看到工具结果、完成通知、wrap-up 指令。

**UI 入口**：见各节；核心是「session header（jobs/subagent/schedule 时钟）+ composer（goal/queue/usage）+ Sidebar（Automations/Sessions）+ 设置/Plugins」。

**判定**：[源码]+[README]。

---

## D9 会话生命周期（Session lifecycle）

**结论**：session 是 DSH 的持久单位：create / resume / fork / search / export / title 都围绕 append-only log 构建。fork 是「稳定前缀复制 + inherited marker + 收尾事件」，不是新起一段对话；resume 是「写所有权 + 重放 + 语义收尾」，不是重新开始。

**机制（关键符号）**
- `ctx.sessions.create()` 建 live session；`fork(source, boundary?, childSessionId?)` 复制 inclusive 前缀（默认最后一条），`buildForkSeed` 放置 inherited marker、为 open step 补 `tool/result` 错误、以 `forked` reason 关闭 step/turn；`inheritedEventCount` 只计复制前缀（`dsh-session` README）。
- resume：见 D3（persistence open write → 读 valid prefix → append closers）。
- 多会话并发：`ctx.sessions.list()`、`ctx.agents.list()`；session controller 的 list 只读 header + projection-cache row，不激活 cold session（`dsh-api-session-controller` README）。
- 搜索：`dsh-session-query` 服务（list/filter/read/trace/search），ranked full-text 需要 `dsh-session-query-sqlite`；模型面是 5 个只读工具 `session_search` / `session_event_search` / `session_trace` / `session_event_trace` / `session_event_read`，工作区授权用**精确 cwd 相等**，无 cwd 只能看自己，搜索永远排除调用者自身（`dsh-tool-session-query` README）。
- 搜索 bundle：`dsh-experimental-session-search` 默认关闭，在 Plugins 里开启后作为 global tool layer 对所有 preset 生效（含 minimal）。
- export：Session Header → `Download session log` 或 `/export`，产出 `dsh-session-<id>.zip`，含 session/sub-sessions/attachments（`dsh-session-log-export` README）。
- title：三个来源（首条人类消息派生 / provider 生成 / 显式 rename），log-backed、永进不了模型输入（`dsh-session-title` README）。

**默认值与上限**

| 项 | 默认/上限 | 证据 |
|---|---|---|
| fork 边界 | inclusive 前缀，默认最后一条 | `dsh-session` README |
| `readEvent` 前后窗口 | `50` | `node_modules/@deepseek-ai/dsh-session-query/lib/index.js:1049` |
| 持久化读取并发 | `4` | `dsh-session-query/lib/index.js:1051` |
| cold prepared session 缓存 | `5` | `dsh-session-query/lib/index.js:1053` |
| 模型搜索返回上限 | `100` | `node_modules/@deepseek-ai/dsh-tool-session-query/lib/index.js:936` |
| 搜索超时 | `30000 ms` | `tool-session-query/lib/index.js:933`、`:937` |
| export 压缩级别 | `6`（0–9） | `node_modules/@deepseek-ai/dsh-session-log-export/lib/index.js:496` |

**模型可见 vs 产品面**：搜索/读取是模型工具（opt-in）；fork/resume/export/title 是产品面（fork 也有 subagent fork 这条模型面路径）。

**UI 入口**：Sidebar 的 Workspaces/Sessions、New Session、Session Header more-actions（export/feedback）、标题就地重命名。

**判定**：[源码]（数字）+ [README]（语义）。

---

## D10 失败处理与鲁棒性（Failure handling）

**结论**：DSH 的失败策略是「**分类 → 有界重试 → 语义收尾 → 人决定**」，而不是无限自愈：
- provider 失败：按错误码有界重试（normal 5 次，500ms→10s，10% jitter），或 `always` 无上限；
- 上下文溢出：一次 maximal head reduction + 一次 retry；
- 工具超时：cooperative deadline，映射为模型可读错误；
- 崩溃：未知结果（`TOOL_OUTCOME_UNKNOWN`）而非自动重试；
- 取消：durable `turn/end {aborted|error|max-tokens|interrupted}`，保留已交付文本；
- **没有模型 fallback / 路由 failover**：找不到时 `NO_ADAPTER`（[判断]：无自动换模型机制）。

**机制（关键符号）**
- retry executor：监听 `agent/request-error`，在 open step 内重跑失败 step；每次 retry 先写 durable `llm/retry` 事件（id/provider/mode/policy key/failure/scheduled delay）再等；随后 `llm/retry-started`；`Retry-After` 合法且不越界时优先采用（`dsh-llm-retry` README）。
- 策略在 adapter：`retryPolicy.mode: normal | always`；normal 有 `maxRetries`、`retryableCodes`、`backoff`；always 无 attempt 上限，只在 success/cancel/disposal 停止（`dsh-llm-retry` README）。
- 错误分类：`HarnessError.code` 稳定机器码；关键码 `EMPTY_RESPONSE`、`RATE_LIMIT`、`SERVER`、`TIMEOUT`、`TRANSPORT`、`CONTEXT_WINDOW_EXCEEDED`、`QUOTA`、`ACCOUNT_QUOTA`、`INVALID_CREDENTIAL`、`NO_ADAPTER`、`IMAGE_OFFLOAD_REQUIRED`（`node_modules/@deepseek-ai/dsh-llm/lib/index.js:136-140`、`:253-256`、`:2264`）。
- 失败 step 收尾：未回答的 assistant tool call 记录 `TOOL_OUTCOME_UNKNOWN`；没有 call record 的记录 `TOOL_NOT_STARTED`；已提交结果保持（`dsh-agent-loop` README「Failure and cancellation」；`dsh-session/lib/index.js:728-733`）。
- 取消一致性：`turn/end` 声明 `TurnEndCancelCause`；取消流保留已交付前缀并 append `interrupted: true` anchor；未 dispatch 的 tool call 合成 `ABORTED_BEFORE_DISPATCH` 结果（`agent-loop` README；`agent-loop/lib/index.js:1097-1109`）。
- 部分失败：tool 批量执行中 abort 会「stop starts, drain and commit started calls」，结果按模型顺序提交（`agent-loop/lib/index.js:545-548`）；批调度内部失败 drain 后 reject。
- checkpoint fail-closed：flush 失败则 adapter/tool body 不执行（D3）。
- 限流：`RATE_LIMIT` 是 retryable code；`QUOTA`/`ACCOUNT_QUOTA` 有专门 UI 文案（`dsh-client-ui-chat` README）。
- 模型 fallback：全仓检索 `fallback|failover|secondary` 在 `dsh-llm`/`dsh-agent-default-model` 无结果（附录A 命令 8）→ [判断] 不支持自动换模型。

**默认值与上限**

| 项 | 默认/上限 | 证据 |
|---|---|---|
| normal 最大重试 | `5` | `node_modules/@deepseek-ai/dsh-llm/lib/index.js:247`、`:265` |
| backoff | `500 ms` → `10000 ms` | `dsh-llm/lib/index.js:248-249` |
| jitter | `0.1` | `dsh-llm/lib/index.js:250` |
| retryable codes | `EMPTY_RESPONSE / RATE_LIMIT / SERVER / TIMEOUT / TRANSPORT` | `dsh-llm/lib/index.js:252-256` |
| always 模式 | 无 attempt 上限 | `dsh-llm-retry` README |
| 上限定时器 | `MAX_TIMER_DELAY_MS`（Node 上限，配置 max 用） | `dsh-llm/lib/index.js:259-261` |
| 崩溃后重试 | 不自动；unknown outcome | `dsh-session` README / 错误文本 `dsh-session/lib/index.js:728-733` |

**模型可见 vs 产品面**：retry 的 delay/provider error 对模型不可见；模型能看到超时/拒绝/unknown-outcome 的工具结果与 terminal 失败。

**UI 入口**：Chat 的 terminal 失败行（red-dot / amber max-tokens / quota notice）、Settings → General → Performance & usage（detailed/compact）、session log upload。

**判定**：[源码]（数字与错误码）+ [判断]（无 fallback）。

---

## 附录A 复现命令（相对 $DSH）

```bash
export DSH=/usr/local/lib/node_modules/@deepseek-ai/dsh
# 1. 版本与包数
cat $DSH/package.json | head -6            # 0.2.1-rc.2
ls $DSH/node_modules/@deepseek-ai | wc -l  # 305
# 2. goal 默认轮数与状态机
rg -n 'defaultMaxGoalRounds|PHASES|SNAPSHOT_OPERATIONS' $DSH/node_modules/@deepseek-ai/dsh-goal/lib/index.js
rg -n 'blockedAfterConsecutiveRounds' $DSH/node_modules/@deepseek-ai/dsh-tool-goal/lib/index.js
rg -n '<goal_round>|round-limit|roundsStarted' $DSH/node_modules/@deepseek-ai/dsh-goal-round-driver/lib/index.js
# 3. agent loop 无步数上限 + 并行预算
rg -n 'while \(await this.turn\(\)\)|while \(true\)|DEFAULT_MAX_PARALLEL_TOOL_CALLS' \
  $DSH/node_modules/@deepseek-ai/dsh-agent-loop/lib/index.js
rg -n 'maxSteps|maxIterations|maxTurns|stepLimit' $DSH/node_modules/@deepseek-ai/*/lib/*.js   # 期望空
# 4. 持久化窗口 / 检查点 / 崩溃收尾
rg -n 'setTimeout|\.zstd|drainLive' $DSH/node_modules/@deepseek-ai/dsh-session-persistence-jsonl/lib/index.js
rg -n 'llm/stream|tools/execute|agent/pre-step|flush' $DSH/node_modules/@deepseek-ai/dsh-session-checkpoint-policy/lib/index.js
rg -n 'TOOL_OUTCOME_UNKNOWN|interruptedTurnClosers' $DSH/node_modules/@deepseek-ai/dsh-session/lib/index.js
# 5. 压缩阈值与裁剪
rg -n 'thresholdRatio|headroomTokens|retainRatio|Math\.min|Math\.floor' \
  $DSH/node_modules/@deepseek-ai/dsh-compaction-basic/lib/index.js
rg -n 'thresholdChars|headChars|tailChars|PRUNE_MARKER' \
  $DSH/node_modules/@deepseek-ai/dsh-compaction-tool-result-pruner/lib/index.js
# 6. 编排上限
rg -n 'maxDepth|maxActiveSubagents' $DSH/node_modules/@deepseek-ai/dsh-subagent/lib/index.js
rg -n 'DEFAULT_MAX_MEMBERS|DEFAULT_MAX_TASKS' $DSH/node_modules/@deepseek-ai/dsh-experimental-agent-team/lib/index.js
rg -n 'maxTotalAgents|maxConcurrentAgents|maxItemsPerCall|syncTimeoutMs' \
  $DSH/node_modules/@deepseek-ai/dsh-workflow-ptc/lib/index.js
rg -n 'maxRounds|maxHandoffChars|maxResultChars' $DSH/node_modules/@deepseek-ai/dsh-tool-ralph/lib/index.js
# 7. 调度 / job / headless
rg -n 'deliveryHistoryDays|deliveryHistoryRecords' $DSH/node_modules/@deepseek-ai/dsh-schedule/lib/index.js
rg -n 'waitTimeoutMs|maxWaitTimeoutMs|completionDelivery' $DSH/node_modules/@deepseek-ai/dsh-tool-jobs/lib/index.js
rg -n 'maxConcurrentJobsPerOwner|retainBytes|settledRetainBytes|pumpPollMs' \
  $DSH/node_modules/@deepseek-ai/dsh-jobs-local/lib/index.js
# 8. 审批 / sandbox / retry
rg -n 'policy:|default\("ask"\)|APPROVAL_POLICIES' $DSH/node_modules/@deepseek-ai/dsh-user-approval/lib/index.js
rg -n 'default\("read-only"\)|danger-full-access' $DSH/node_modules/@deepseek-ai/dsh-sandbox-policy/lib/index.js
rg -n 'DEFAULT_MAX_RETRIES|DEFAULT_INITIAL_DELAY_MS|DEFAULT_MAX_DELAY_MS|DEFAULT_JITTER_RATIO' \
  $DSH/node_modules/@deepseek-ai/dsh-llm/lib/index.js
rg -n 'fallback|failover|secondary' $DSH/node_modules/@deepseek-ai/dsh-llm/lib/index.js   # 期望空
# 9. 默认组合（哪些能力 ship 默认启用）
rg -n 'goal|tool-jobs|compaction|session-checkpoint|spill-policy|tool-goal|workflow' \
  $DSH/node_modules/@deepseek-ai/dsh-base/cordis.patch.yml
rg -n 'tool-schedule|tool-jobs|tool-goal|tool-workflow|tool-subagent' \
  $DSH/node_modules/@deepseek-ai/dsh-web-app/presets/standard.patch.yml
# 10. 证据行号抽查
sed -n '585,600p' $DSH/node_modules/@deepseek-ai/dsh-goal/lib/index.js   # defaultMaxGoalRounds 256
sed -n '113,117p' $DSH/node_modules/@deepseek-ai/dsh-tool-goal/lib/index.js  # blocked 3
sed -n '60,90p'   $DSH/node_modules/@deepseek-ai/dsh-compaction-basic/lib/index.js
```

---

## 附录B 10 维能力一览表

| 维度 | DSH 支持 | 关键机制 | 默认/上限 | 证据 |
|---|---|---|---|---|
| D1 目标与续跑 | 完整 | `dsh-goal` 事件源状态 + `dsh-goal-round-driver` 空闲续跑 + `dsh-tool-goal` 权限受限工具 + `/goal` | `maxGoalRounds=256`；模型 blocked 阈值 `3`；resume 需人类 | `dsh-goal/lib/index.js:588`、`:697`；`dsh-tool-goal/lib/index.js:115`；`dsh-base/cordis.patch.yml:323-330` |
| D2 循环与预算 | 无步数上限，预算外挂 | `while(true)` step + `while(turn())` turn + 并行池 + 外挂策略 | `maxParallelToolCalls=10`；无 `maxSteps`；无 token 硬预算 | `dsh-agent-loop/lib/index.js:889`、`:953`、`:1297`、`:1554` |
| D3 持久化与恢复 | 完整 | append-only log + jsonl.zstd + 3 检查点 + `interruptedTurnClosers` | 批窗 `200ms`；1 写者/session；job 不跨重启 | `dsh-session-persistence-jsonl/lib/index.js:190-196`、`:2290`；`dsh-session-checkpoint-policy/lib/index.js:61-73`；`dsh-session/lib/index.js:907` |
| D4 上下文管理 | 完整 | token-meter 度量 + compaction 摘要 + pruner + image offload + spill | 阈值 `min(W×0.8, W−O−65536)`；保留 `0.16(W−O)`；裁剪 `8192/4096/1024`；spill `12500` | `dsh-compaction-basic/lib/index.js:15-17`、`:63-85`、`:132-133`；`dsh-compaction-tool-result-pruner/lib/index.js:11-13`；`dsh-base/cordis.patch.yml:414-417` |
| D5 编排 | 四档 | subagent（spawn/fork）+ Team task board + workflow 脚本 + ralph | depth `1`；active subagents `8`；Team `16/256/64KiB/5s`；workflow `1000`/`min(16,…)`；ralph `256`/`16KiB` | `dsh-subagent/lib/index.js:2979-2980`；`dsh-experimental-agent-team/lib/index.js:1420-1442`；`dsh-workflow-ptc/lib/index.js:599-602`、`:631`；`dsh-tool-ralph/lib/index.js:20-22` |
| D6 调度与无人值守 | Host 级 schedule + 进程内 jobs | 6 选择器 durable 任务 + 完成唤醒 + headless/ACP/SDK 形态 | `every≥60s`；历史 `30d/200`；job wait `30s/600s`；job 并发 `10`；headless 1 任务/调用 | `dsh-schedule/lib/index.js:1880-1882`、`:1977-1978`；`dsh-tool-jobs/lib/index.js:89-92`；`dsh-jobs-local/lib/index.js:340-346`；`dsh-web-app/cordis.patch.yml:139-140` |
| D7 安全与审批 | 完整 | sandbox mode × approval policy + permission preset + fail-closed | `ask` 默认；`danger-full-access → never`；sandbox 库默认 `read-only`、base pin `workspace-write`；提权一次一审批 | `dsh-user-approval/lib/index.js:74`；`dsh-base/cordis.patch.yml:243-247`、`:259-262`；`dsh-permission-presets/lib/index.js:145-158`；`dsh-sandbox-policy/lib/index.js:102` |
| D8 可观测与控制 | 完整 | goal/job/schedule/usage/sessionStats projection + 专用 UI + 完成通知 | job 观察合窗 `100ms`、帧 `64KiB`；stop 二确认 `3s`；usage 需精确数据 | `dsh-api-job-controller/lib/index.js:251-253`；`dsh-client-ui-jobs/lib/client.js:75`；`dsh-client-ui-goal` README |
| D9 会话生命周期 | 完整 | create/resume/fork/search/export/title；fork=前缀+marker+closers | fork inclusive 前缀；搜索 `100`/`30s`；export level `6`；session-query `50/4/5` | `dsh-session` README；`dsh-tool-session-query/lib/index.js:933-937`；`dsh-session-log-export/lib/index.js:496`；`dsh-session-query/lib/index.js:1049-1053` |
| D10 失败处理 | 有界重试 + 语义收尾 | `agent/request-error` 上 durable retry + 错误分类 + unknown outcome + 取消一致性 | retries `5`；`500→10000ms`；jitter `0.1`；always 无上限；无模型 fallback | `dsh-llm/lib/index.js:247-256`；`dsh-llm-retry` README；`dsh-session/lib/index.js:728-733`；`dsh-agent-loop/lib/index.js:1992` |

---

## 附录C 对 ZeoKeel 最有参考价值的 3 点（[判断]）

> 以下是对照建议，不是 DSH 事实；Lead 做差距综合时可直接引用或推翻。

1. **把「目标状态」做成可 replay 的一等公民，而不是会话里的一个字段。**
   DSH 的 goal 是事件源 + CAS revision + 严格 fold（`dsh-goal/lib/index.js:33-41`、`:170-205`、`:588-597`）。这带来两个可直接复用的性质：resume/fork/重启后状态自动一致；并发写入天然被 revision 拒绝。ZeoKeel 若把 goal 存在内存或单个可变记录里，长跑恢复和并发 steer 都会是薄弱点。

2. **预算要分线，且每条线有明确「耗尽后行为」。**
   DSH 没有 `maxSteps`，取而代之是：goal 轮数 256（耗尽→自动 block `round-limit`）、context 阈值（触发→压缩，压缩失败→fail closed）、工具超时（→模型可读错误）、retry 有界（→terminal 失败）。每条线都定义了「用尽之后发生什么」，而不是一个笼统的全局上限。这是长跑产品最容易被忽略、也最影响可预期性的部分。

3. **无人值守 = 显式产品选择，而不是默认行为。**
   DSH 用 `sandbox mode × approval policy` 的 preset 表达「能不能无人跑」：默认 `workspace-write + ask`（会停下来等人），只有 `danger-full-access + never` 才是真正无人值守（`dsh-base/cordis.patch.yml:259-262`、`dsh-permission-presets/lib/index.js:145-158`）。同时「自动续跑是否 armed」是进程内状态、resume 后必须人类显式恢复（`dsh-goal/lib/index.js:594-595`）。这让「长跑」既可自动、又可审计、又不会在重启后自作主张——比单一「自动继续」开关更安全。

补充一条备选（若只能挑三点之外再加一点）：**完成/阻塞的判定权与执行权分离**——DSH 允许模型在 goal 轮里 `complete`，但 `blocked` 必须跨 3 轮且填具体原因；子 agent/调度器不能创建 goal（`dsh-tool-goal/lib/index.js:50-64`、`:360`）。这避免了「模型自说自话结束任务」。
