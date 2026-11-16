# 04 · OpenClaw（"龙虾"）：工作区即记忆 —— 文件系统记忆 + skills 固化

> 调研日 2026-11-15。**一手来源 = docs.openclaw.ai 官方文档**（每个 URL 后加 `.md` 可直接拿干净 Markdown 源码，绕开网页侧栏截断）。
> 标记 `[二手]` 的是社区文章/第三方 skill，仅作旁证，不作为机制事实。
> 版本注意：官方文档在 2026 年快速迭代（本文多处提到 "pre-July 2026 格式已废弃"），下述细节有保鲜期。

## 0. 结论速览

| 问题 | 结论 |
|---|---|
| 它站哪边？ | **记忆 > 本机 wiki > workflow**。核心是"工作区文件系统即记忆"，wiki 是后来加的一层编译视图（插件），workflow 被明确**移出**记忆（time-based→cron，event-based→SQLite standing intents） |
| 常驻上下文是什么 | `AGENTS.md` + `SOUL.md` + `USER.md` + `MEMORY.md`（+ 今天/昨天 daily note），总预算 60K 字符；其余靠工具检索 |
| 谁写长期记忆 / 后台整理 | **只有一个主写入者：dreaming 的 deep phase 合并 pass**。agent 日常只写 daily note，flush 也只写 daily note；dreaming 是官方内置、默认开、cron `0 3 * * *`，light→REM→deep 三阶段，只有 deep 写 `MEMORY.md`，全部可审阅（`DREAMS.md`） |
| 经验怎么变 skill | 官方**内置 self-learning + Skill Workshop**：每 10 个 model iteration 触发一次后台复盘，用 `skill_workshop` 工具落成 `SKILL.md`；默认 `auto`，改动即生效且可 undo |
| 最值得抄的 | ①分层"常驻/按需"预算表 ②唯一写入者 + 门禁 + 可回滚 preimage ③provenance 决定能否进上下文 ④无模型调用的一档召回（trigger 注入）⑤skill 与 memory 的职责切割原话 |
| 最大的坑 | 写入质量是瓶颈（官方自认）；provenance 误判会把 `MEMORY.md` 整个排除出上下文；后台复盘/做梦都是"额外模型调用 = 额外钱与隐私面"；第三方/向量插件并不实现官方三阶段 |

## 1. 工作区模型

默认路径 `~/.openclaw/workspace`，与放配置/凭证/会话的 `~/.openclaw/` **分家**。官方定位："The workspace is the agent's home… Keep it private and treat it as memory."（<https://docs.openclaw.ai/concepts/agent-workspace>）

**关键警告（官方原文）**：工作区是"default cwd, not a hard sandbox"——工具相对路径落在工作区，但绝对路径仍可到宿主机任意处，除非开 sandbox。

```
~/.openclaw/workspace/
├── AGENTS.md            # 操作手册：规则/优先级/"如何使用记忆"。每次会话注入
├── SOUL.md              # 人格、语气、边界。每次会话注入
├── USER.md              # 用户模型：稳定偏好，写成祈使句指令。每次会话注入，独立 4000 字符预算
├── IDENTITY.md          # 名字/vibe/emoji（bootstrap 仪式生成）
├── BOOT.md / BOOTSTRAP.md  # 可选启动清单（boot-md hook）/ 一次性首次运行仪式（跑完删）
├── MEMORY.md            # 长期记忆：耐久非画像事实/决策/短摘要。会话开始注入（超预算则截断注入）
├── DREAMS.md            # Dream Diary + 各阶段报告，只给人看，永不注入
├── memory/
│   ├── YYYY-MM-DD.md          # 每日笔记（工作层），会被索引但不注入
│   ├── YYYY-MM-DD-<slug>.md   # 同日分片（session-memory hook 写）
│   ├── imports/{codex,claude-code,hermes}/  # 从 Codex/Claude Code/Hermes 导入的记忆
│   └── .dreams/、dreaming/<phase>/YYYY-MM-DD.md  # dreaming 机器状态与阶段报告
└── skills/              # 工作区技能，优先级最高（压过 .agents/skills、bundled、插件）
```

文件职责与"常驻 vs 按需"（一手：agent-workspace + memory-architecture 的 tier 表）：

| 文件/层 | 谁写 | 何时进上下文 | 预算/限制 |
|---|---|---|---|
| `AGENTS.md`、`SOUL.md` | **仅人类**（官方 tier 表写 "Human only"） | 每次会话必注 | 计入 bootstrap 总预算 |
| `USER.md` | dreaming 合并 / 用户直接要求 | 每次会话（provenance 合格时） | 独立 **4000 字符**上限 |
| `MEMORY.md` | **仅 dreaming deep phase** | 每次会话（合格时） | 超 `bootstrapMaxChars` 只截断注入，磁盘不动 |
| `memory/YYYY-MM-DD.md` | agent 干活时、pre-compaction flush、transcript 入库 | **不注入**；`memory_search`/`memory_get` 按需取 | 可无限增长，靠检索 |
| `DREAMS.md` | dreaming 三阶段 | **永不注入**，人读 | — |
| standing intents | `intent` 工具，存 SQLite | 仅 trigger 命中时注入 | 冷却 24h / 最多 3 次 / 90 天过期 |
| `skills/*/SKILL.md` | 人写或 Workshop 写入 | 会话开始时快照，编译进系统提示 | 每 skill ≈97 字符+字段（≈24 token） |

bootstrap 预算真相（官方）：`bootstrapMaxChars` 默认 **20000**、`bootstrapTotalMaxChars` 默认 **60000**；缺文件会注入 "missing file" 标记而不是报错；`MEMORY.md` 超预算 → 保留原文件、截断注入副本，官方把这种情况定性为"该把细节挪进 `memory/*.md` 的信号"。

> 更正一个常见误解：当前官方文件表里**没有独立 `TOOLS.md`**。本地工具/环境约定放在 `AGENTS.md` 的 `## Tools` 小节，且官方明说该小节"不控制工具可用性，只是指引"。

## 2. 记忆三层：daily note → 长期 → 检索，谁在什么时候写

| 层 | 文件 | 写入者 | 触发时机 |
|---|---|---|---|
| 工作层（episodic） | `memory/YYYY-MM-DD.md` | agent 日常追加；**pre-compaction memory flush** 补写 | 会话中随手记；上下文快满、compaction **之前**跑一个静默 turn 提醒存盘（默认开，`compaction.memoryFlush.enabled:false` 可关）；`AGENTS.md` 要求"会话开始读今天+昨天+`MEMORY.md`" |
| 工作层（episodic） | session transcripts（索引） | 会话结束后 ingestion | 交互会话结束→transcript 变成可被 dreaming 摄取的证据 |
| 长期（curated） | `MEMORY.md` / `USER.md` | **dreaming deep phase（唯一主写入者）** | cron 定时 sweep（默认每天 03:00） |
| 检索 | SQLite 索引 | 索引 worker | 会话结束后 ~5s 合并窗口 + embedding 时间；完成那一轮之后才可搜到 |

**"会话结束钩子"的准确答案**：没有"结束时让模型总结一遍"的钩子。官方是两条：①compaction 前的 flush（写 daily note，且**只允许追加**新文本——重复整篇笔记会被拒）；②会话结束时 transcript 自动入库。总结/升格统一推给 dreaming。

**heartbeat 不写记忆**（官方原文）："The default heartbeat prompt performs no memory maintenance on its own"；dreaming 关闭时也不会去翻 heartbeat 队列。heartbeat 的定位是保活/监控（AGENTS.default："Heartbeats keep background tasks alive"）。

**谁能进长期记忆**（写路径的安全边界，一手：memory-architecture）：

| 规则 | 内容 |
|---|---|
| origin class（闭集） | `owner`（可信渠道里用户亲写）/`agent`（从 owner 内容派生）/`untrusted`（网页、工具输出、群里的非 owner）/`system`（heartbeat、cron 等运行时指令） |
| session-kind gating | cron / heartbeat / subagent 会话**不产生**耐久候选，只能写任务产物 |
| recall-loop prevention | 从记忆注入到上下文的内容会被结构化标记，永不再被抽取成新记忆（"回忆一百次仍是一条事实"） |
| 保守分类 | 判不出就 `untrusted`（外部派生）或 `system`（含运行时指令），**永不默认 owner** |
| taint 传播 | 一个 turn 里出现声明为网络来源的工具结果后，后续 assistant 输出都被标 taint，整段按 untrusted 处理，直到下一条用户消息 |
| 写入前拦截 | `MEMORY.md` 被标 untrusted → 启动时被排除出自动上下文，日志 `excluding MEMORY.md from automatic memory context: memory provenance is untrusted` |

## 3. dreaming：官方的"后台整理记忆"

一手：<https://docs.openclaw.ai/concepts/dreaming>（默认启用；关闭 `plugins.entries.memory-core.config.dreaming.enabled:false`）

| 维度 | 官方行为 |
|---|---|
| 节奏 | 自动托管**一个** cron job，`dreaming.frequency` 默认 `0 3 * * *`；`cron.enabled:false` 时只推迟建 job，不删既有 job |
| 模型 | Dream Diary/consolidation 是"tool-free completion"，默认用**该 workspace agent 的默认模型**；`dreaming.model` 可覆盖（需打开 `subagent.allowModelOverride`）；不可用则重试一次 agent 默认模型 |
| 阶段 | `light` → `REM` → `deep`，**只有 deep 写 `MEMORY.md`** |
| light | 读近期 short-term recall、daily notes、transcripts；去重、暂存候选行；写 `## Light Sleep` 块；**不碰 MEMORY.md** |
| REM | 从近期痕迹生成主题/反思摘要；写 `## REM Sleep`；给 deep 记强化信号（light/REM 都只有 recency 衰减加成，不写长期） |
| deep | 加权打分（relevance .30 / frequency .24 / query diversity .15 / recency .15 / consolidation .10 / conceptual richness .06，light/REM 命中另有 recency 加成）+ 门槛（`minScore`、`minRecallCount`、`minUniqueQueries` **全部通过**）；从 live daily 文件重新取片段（已删/已改则跳过）；把过关候选 + 现 `MEMORY.md` 交给模型，模型只输出**操作决策（新增/合并/取代）**，由 writer 用"有出处的片段"拼装成文；每条晋升上限 `phases.deep.maxPromotedSnippetTokens` 默认 **160** token，可带 `<!-- trigger: ... -->`（≤3 短语）与 `<!-- importance: 1..10 -->` |
| 安全阀 | 重写必须：保留旧条目比例 ≥ `1-maxPriorEntryLossFraction`（默认 0.25）、每条晋升带 `Source: path#Lx-Ly`、塞进 bootstrap 预算、能被结构化解析；否则**回退 append-only**；改前把 preimage 存 SQLite，改动摘要写 `DREAMS.md` |
| taint 门 | `untrusted`/`system` 候选在**构造 prompt 之前**就被结构性剔除（不是扣分，是物理隔离） |
| 摄取范围 | 只有交互会话；cron/heartbeat/subagent/unknown 永不进耐久候选；被标记 recalled 的上下文先剥离，防止"学会自己回忆的东西" |

**已知问题/坑（官方自述）**：
- 空 sweep 不建任何文件（因此不会完成新工作区的首次运行仪式）；
- 模型失败 → diary 写本地 fallback 占位（"degraded outcome"），不是静默成功；
- 显式多 agent 编制缺 `agents.defaults.systemAgent.agentId` 时，reconciliation **不建 job 并只 warn 一次**；
- 2026-07 之前的 JSON journal 不再导入，迁移状态混淆时官方建议"从 2026-07 之后版本的备份恢复"——**升级期的状态迁移是真实痛点**；
- LanceDB 等第三方向量记忆插件**不实现** light/REM/deep 这套，"slot-owned settings" ≠ 官方 dreaming 管线；
- 重写是 optimistic concurrency（改前校验 content hash），冲突则本次回退 append；残留竞态窗口"毫秒级、可恢复"，官方**明确接受**这个权衡（不想给 Markdown 加锁）。

## 4. 经验 → skill：self-learning / Skill Workshop / Custodian

一手：<https://docs.openclaw.ai/tools/self-learning>、<https://docs.openclaw.ai/tools/skill-workshop>

**边界（官方原话，抄这条就够了）**："Learned skills are procedures, not memory: the method for a task the user repeats… Facts about the user or the world belong in memory. Knowledge about one codebase, such as its conventions, build commands, or architecture, belongs in that repository's docs or `AGENTS.md`."

| 机制 | 触发 | 开销/边界 | 产物 |
|---|---|---|---|
| 前台自救 | 用到的 learned skill 错了/不全 → 当场 `view`+`patch`；硬多步任务完成后直接存流程 | 同一个 turn 内，不额外跑模型 | 改/建 Workshop skill |
| **后台复盘**（self-learning，默认 `auto`） | 会话累计 **10 个 model iteration** 后入队；空闲 **30s** 后开跑；一次一个；前台从不等待 | 复用前台 provider/model；只给 `skill_workshop` 一个工具；读 **≤64 transcript events / 32 KiB**；目标 16,384 token 上下文；≤2,048 输出 token、低推理、≤2 分钟；失败不重试 | 最多**一处**改动（改最相关那条 skill / 没有就新建 / 同类合并后 archive 旧的） |
| `propose` 模式 | 同上，但只存 draft，等 `/learn drafts` 审 | — | `<agentDir>/workshop-skills/.drafts/` |
| `/learn [要求]` | 人主动 | 正常前台 turn，可用全部工具 | 流程→skill，事实→memory（记忆插件支持时**一次同时落两边**） |
| 30 天未用清理 / skill diet | 每天各最多一次（清理在 turn 结束后） | 清理只对 embedded harness 的 agent 生效（Codex harness 无法归因读操作，**故意关掉**）；diet 仅当 `SKILL.md` 正文 >12KB 且一天没改，把整节挪进 `references/`，要求正文缩 ≥20%、链接/锚点全解析、二次规则对比审查 | 归档（可 restore）/ 版本化改动（actor `curator`） |

**明确不学什么（官方列表）**：只对单次请求成立的细节、环境相关/瞬时失败、对工具的负面断言、未解决的失败与猜测、单个 codebase 的知识、一次性任务、个人事实、密钥、没有具体命令/路径/id 的泛泛建议。

**skill 落盘与校验**：`<agentDir>/workshop-skills/<name>/SKILL.md` + `references|templates|scripts|assets`；每次改动前存版本（保留最近 10 个）、变更流 500 条；name 1-63 小写，description 1-1024 字节（**建议 ~160**），`maxSkillBytes` 默认 40000，支撑文件 ≤256KiB；正文 >250 行或 >12KB 给 "Advisory (not blocking)" 而非拒绝；命中密钥/安全扫描 critical 则**拒绝写入**。

**Custodian skills**（<https://docs.openclaw.ai/tools/custodian-skills>）是另一回事：随发行版分发的运维 playbook，**只对 `systemAgent.agentId` 指定的那一个 agent 可见**，统一五段式 `Gather → Mutate → Repair → Prove → Report`。它解决的是"运维动作别乱来"，不是"经验固化"。

**skill 与 memory 的其它交界**：`skills_search`/`skills_read` 存在意味着 skill 目录本身也可能超出提示预算 → 与 memory 同样的"目录有界 + 工具检索"模式；正式 skill 走 `<workspace>/skills`（最高优先级）而 Workshop skill 走 state dir（不入库、不随工作区 git 备份）。

## 5. active-memory：检索是自动注入还是工具调用？

一手：<https://docs.openclaw.ai/concepts/active-memory/how-it-works>、memory-architecture §Recall

**两档，一档零模型调用、一档是真 sub-agent**：

| 档 | 机制 | 何时跑 | 成本 |
|---|---|---|---|
| Lane 1（常开） | ①bootstrap 注入 `MEMORY.md`/`USER.md` ②`memory_search` 混合排名 ③**trigger 注入**：每条目带 trigger 短语，入站消息跑词法+向量预筛，score ≥0.65 的条目前置注入 | 每个 eligible turn | **0 次模型调用**（importance 在写入时打分） |
| Lane 2（升级） | 阻塞式 recall sub-agent，能搜/读跨会话 transcript | 默认 `mode:"escalate"`：**只有**①消息显示 recall 意图（提过去、时间状语、问旧决策）**且**②Lane 1 无强命中；`always` 全跑，`off` 关 | 一次额外 agent turn，延迟计入回复前 |

- **query 怎么生成**：Lane 1 不做 query 生成——直接用入站消息原文做 trigger 预筛 + 混合检索；Lane 2 由 sub-agent 自己把消息变成检索动作（工具调用）。所以"关键词搜索 MEMORY.md"只是 Lane 1 的一部分，不是全部。
- **自动注入只对 curated 层开放**：只有 `MEMORY.md`/`USER.md` 的条目能自动注入；daily notes 与 transcript **无论命中多强**都只能走显式工具或 Lane 2。官方定性："这是安全属性，不是调参选择。"
- 自动 project recall 有 **1 秒总预算**（含 provider 启动/清理），超时则本轮不带候选并记 `project memory recall skipped`；冷/忙存储可能**连续几轮**无自动召回（工具仍可显式用）。覆盖面（官方表）：持久 UI/渠道会话 ✅；headless、heartbeat/后台、内部 `agent-command`、subagent ❌；默认 `allowedChatTypes:["direct"]`。

## 6. 索引与检索实现 / 规模 / 上下文成本

一手：<https://docs.openclaw.ai/concepts/memory-search>、memory-architecture

| 项 | 事实 |
|---|---|
| 默认引擎 | `memory-core`（builtin）：**plain files + 一个 SQLite 索引**；可换 Honcho、LanceDB 插件、Knowledge、PostgreSQL（qualified runtime） |
| 检索方式 | **hybrid** = 向量（embedding）+ 关键词（SQLite BM25；PG 用 native FTS/trigram）并行后加权合并；默认 OpenAI embeddings，可换 Gemini/Voyage/Mistral/Bedrock/DeepInfra/local GGUF/Ollama/LM Studio/Copilot/OpenAI-compatible |
| 排序/衰减 | `hybrid relevance × recency decay × importance multiplier`，再过 **MMR**（lambda .7，每路 200 候选、去重后 ≤400）；daily note 30 天半衰期，`MEMORY.md`/`USER.md`/无日期文件 **evergreen** |
| 查询/结果上限 | PostgreSQL 侧：query ≤4096 UTF-16 单元 / ≤128 词，≤200 hits，单片段 ≤700 字符；`maxResults`（别名 `max_results`/`limit`） |
| chunk/降级 | "只有标题的 chunk"并到后续内容 chunk（保 section 上下文与行号引用）；embedding 失败 → 保留关键词并标 degraded，显式 provider 挂掉 → **报 unavailable 不静默降级**，`provider:"none"` = 故意纯 FTS |
| 上下文成本 | 见 §1 bootstrap 预算表；skill 目录另算（≈24 token/skill，超 `skills.limits.maxSkillsPromptChars` 先保 name+location 再砍 description）；MEMORY.md 超限只截断注入 |

**"为什么需要插件"的答案（§7 一起看）**：builtin 的职责被官方刻意收窄成"召回 + 晋升 + dreaming"。任何"页面结构、claim/evidence、纠错、看板"都做成**旁挂插件**，因为它们要改变的是**知识的组织形态**，而不是"读写记忆"这一层契约。

## 7. wiki 化记忆：memory-wiki 与 Knowledge

| 方案 | 定位 | 存储/检索 | 关键能力 | 为什么不是内置默认 |
|---|---|---|---|---|
| `memory-wiki` 插件（<https://docs.openclaw.ai/plugins/memory-wiki>） | **旁挂**：不接管 memory slot，不接管 recall/晋升/dreaming | vault 目录（`sources/ entities/ concepts/ syntheses/ reports/ _views/`）；`wiki_search`/`wiki_get`/`wiki_apply`/`wiki_lint`；可用 shared 或 local 后端 | 固定页结构、**结构化 claims + evidence**、矛盾/新鲜度看板、编译快照（SQLite）、Obsidian 友好、OKF 导入、per-agent vault | 它是一套**信念层**（claim 可被支持/contested/取代），与"注入什么进提示"解耦；模式 `isolated`/`bridge`/`unsafe-local` 决定了它能否读 memory 产物，风险面比 memory-core 大 |
| `knowledge` 插件（<https://docs.openclaw.ai/plugins/knowledge>） | **接管 memory slot** 的另一种 memory provider | `<state dir>/knowledge/`：registry.sqlite + 每 store 的 state.sqlite + `vault/`（Markdown + 本地 .git）；**不做 embedding**，检索 = SQLite FTS5 关键词 + 精确身份 + 结构化日期 | 页面/claim/关系、**claims 强制保留 hash 校验的证据**、纠错/撤回/取代/身份合并都保留历史、journal 写入与精确重试、本地 git 历史、38 个 `knowledge_*` 工具、9 个自带 skill、**Scribes** 常驻整理会话、只读 Control UI | 它是"重写入、轻检索"的赌注：官方能力表自认 **semantic search / OCR / 转写 / 连接器 / 远端同步 / 完整备份都未实现**；且槽位唯一，选它就把 `MEMORY.md`+daily note 那套换掉 |

官方给的实用分工（memory-wiki 页）：`memory_search` 做一次广覆盖召回；需要 wiki 专属排名 / provenance / 页面级信念结构时用 `wiki_search`/`wiki_get`；两者都要则 `memory_search corpus=all`。

**wiki 化的代价都写在文档里**（可直接当"坑"引用）：`wiki_search` 有 30 秒硬 deadline（超时=工具错误而非空结果）；页面扫描跑在插件自有的 **2 线程**池里，每 64 页检查是否要让位给 `memory_search`；每个看板最多 2,500 张卡；源文件非 UTF-8 时**不替换既有页、不更新同步元数据**；`vault.scope:"agent"` 是"同进程知识边界，不是 OS 安全边界"（同机插件/非沙箱工具仍可读别的 agent 目录）。

## 8. 官方承认的限制 + 社区已知坑

### 8.1 官方文档白纸黑字（可直接抄进设计评审）

| # | 限制原文/要点 | 出处 |
|---|---|---|
| 1 | "**Writing is the hard part.**" 检索已能跟重度设计打平，真正让记忆系统退化的是不可靠的**写入期策展**（引 LongMemEval, arXiv:2410.10813） | memory-architecture |
| 2 | 生产审计发现**绝大多数自动捕获的记忆是运行时指令复述、heartbeat 噪声、召回反馈循环** → 因此才有 session-kind gating + recall-loop prevention | memory-architecture |
| 3 | `MEMORY.md` 超预算时**磁盘不动、注入被截断**（只有 `/context list|detail`/`doctor` 看得见 raw vs injected）；provenance 不合格则整份**被排除**自动上下文，只留工具可读 + 一条 warning | memory |
| 4 | flush 期间 `write` **只能追加**；整篇重放/复制段落会被拒（不去重改写、不修复既有笔记） | memory |
| 5 | consolidation 出错/不可用 → 回退 **append-only**（结构更差但不丢）；重写有毫秒级竞态窗口 | dreaming / memory-architecture |
| 6 | 显式多 agent 编制缺 systemAgent → dreaming cron **不创建**，只 warn 一次 | dreaming |
| 7 | 后台复盘会把**会话内容（含工具输入输出）发给模型 provider**；且触发条件苛刻（≥10 iteration、无错误、非 cron/heartbeat/hook/subagent/Incognito、tool policy 允许、空闲 30s） | self-learning |
| 8 | Skill Workshop **不支持 sandboxed 会话**；Codex harness 下未用清理**故意关闭** | skill-workshop |
| 9 | Knowledge 明确未实现：语义检索、媒体抽取/OCR/转写、连接器/同步守护、git 远端、完整备份恢复 | knowledge |
| 10 | memory-wiki 的 per-agent vault 只是同进程边界；`unsafe-local` 模式"intentionally experimental and non-portable" | memory-wiki |

### 8.2 社区二手（仅旁证，非机制事实）

| 内容 | 来源 |
|---|---|
| **Autodream**：在 dreaming 内置化之前，社区用 `openclaw-autodream`（npm / GitHub）做"夜间记忆合并：读会话、挖 daily log、修剪过时事实"。官方 dreaming 上线后它变成历史的/并行的方案 | <https://github.com/LeoStehlik/openclaw-autodream>、<https://www.npmjs.com/package/openclaw-autodream> `[二手]` |
| **"一虾多魂"跨 agent 记忆整理工作流**：在 OpenClaw 原生记忆之上自己加一层"双轨记忆架构"，用 Bash+Node 定时脚本跨 agent/跨工作区归档；作者踩坑记录里有"配置文件 ≠ 系统配置"、"OpenID 大小写敏感" | <https://cloud.tencent.cn/developer/article/2650141> `[二手，正文未能完整抓取，仅据目录与摘要]` |

> 更多二手：中文"记忆系统完全指南"类教程（<https://cloud.tencent.com.cn/developer/article/2648312>、<https://atomgit.com/SmartAI/AI-Coding-Guide-Zh/blob/main/docs/openclaw/07-记忆系统指南.md>）多写于 2026 上半年"只有 agent 自觉写 + 手工整理"的形态，而**现在是默认开 dreaming + 默认开 self-learning**——看到"让 agent 定期整理 MEMORY.md"的教程，先怀疑它已过期。

## 9. 三选一：它到底站哪边？

| 选项 | OpenClaw 的立场 | 证据 |
|---|---|---|
| **记忆**（文件即大脑） | ✅ 主立场。"No hidden state. The model only remembers what is written to files in the agent workspace. Every memory surface is inspectable and editable with a text editor." | memory-architecture 设计原则 1 |
| **本机 wiki** | 🟡 部分立场，且**降级为插件层**。baseline 是 Markdown 笔记 + 索引，不是 wiki；需要页面/claim/evidence/看板时才挂 `memory-wiki`，或整体换成 `knowledge` 槽位 | memory 页 "Knowledge wiki layer"、memory-wiki 页 "does not replace the active memory plugin" |
| **可执行流程** | ❌ 刻意挪出记忆。意图被"编译"成 cron 或 SQLite standing intents（"storing intentions as prose in a memory file is the least reliable design available"）；流程/方法进 skill（"procedures, not memory"） | standing-intents、skill-workshop |

### 什么条件下 work

1. **写入路径有且只有一个把关者**（dreaming deep），agent 日常只写 append-only daily note —— 把"策展"从回复热路径挪走。
2. **常驻上下文有硬预算**（20K/60K/4K 字符），超了截断而不是硬塞；"细节进 daily note、摘要进 MEMORY.md"是文档化的操作习惯。
3. **可信/不可信在入口就分流**（SQLite 列 + session kind + taint），而不是事后靠内容扫描；不可信内容可存可搜但永不自动注入。
4. **定时 sweep 默认开**，晋升有阈值门 + 可回滚 preimage + 人可读日记 —— "忘了整理"不会立刻烂掉；且**一虾一工作区、单用户、单机**：git 私有仓备份、可 grep、出错能手改。

### 什么条件下崩

1. **信任/并发边界被打破**：污染或误判 provenance → 整份 `MEMORY.md` 被踢出上下文（表现为"agent 突然失忆"，用户只看到一条日志）；多写入者共享工作区 → Markdown 无锁，靠 hash 乐观并发 + 回退 append，必然竞态与互相覆盖。
2. **指望"云端/headless/后台"也有记忆**：active memory 明确不在 headless/heartbeat/subagent 上跑；dreaming 不摄取这些会话。
3. **规模超出检索设计**：召回 1 秒预算、MMR 200/400 候选、PostgreSQL 128 词 200 hits —— 超大工作区会退化成"显式搜才有"。
4. **把"记忆"当"流程"用**：想让 agent 每次按固定步骤做事，写进 `MEMORY.md` 是错的；官方答案是 skill/standing intents。反过来把一次性事实塞进 skill 也会被"skill 只装可复用的类级方法"挡回来。
5. **钱与隐私**：dreaming sweep + 后台复盘 + skill diet 都是**额外模型调用**，且会把会话内容发给 provider；rate-limit 退避（15→30→60 分钟）说明官方预期它会被打到限流。

## 10. 可抄 / 别抄（给 ZeoKeel 的经验蒸馏用）

| 可抄 / 别抄 | 具体形态 |
|---|---|
| ✅ 单一写入者 + 常驻/按需两张表 | 长期记忆只有一个后台合并者，前台只 append；明确哪些文件每轮注入、多少字符、超了截断而非拒写 |
| ✅ 写入期 provenance，自动注入只对 curated 层 | origin class + session kind + 时间戳 + supersession key 存**代码列**、绝不从正文解析；daily/episodic 永不自动注入（安全属性，不是调参） |
| ✅ 晋升门是代码、判断才是模型；每次重写留 preimage | 打分/阈值/资格/生命周期用固定规则；模型只输出**操作决策**（合并/取代），失败回退 append-only |
| ✅ memory 与 procedure 的切割线用一句话说清 | "procedure, not memory" + "knowledge about one codebase → AGENTS.md" |
| ⚠️ 有条件抄 | trigger 短语 + importance **写时**打分（省掉 query-time 模型调用）；skill diet（正文 >12KB 才动、缩 ≥20%、二次规则审查）——都依赖"写入时已有模型在环" |
| ❌ 别抄 | 三阶段 light/REM/deep 的命名与实现复杂度（过度设计）；per-agent wiki vault 的"同进程边界"（真隔离靠进程/沙箱）；taint 传播细节（声明覆盖不全就误伤，官方自认只覆盖声明了网络来源的工具） |

## 附 A：一手来源清单（均为 `docs.openclaw.ai/<path>.md`，加 `.md` 取干净源码）

- 工作区 `/concepts/agent-workspace`｜记忆 `/concepts/memory`｜记忆架构 `/concepts/memory-architecture`｜检索 `/concepts/memory-search`
- dreaming `/concepts/dreaming`｜active memory `/concepts/active-memory/how-it-works`｜standing intents `/concepts/standing-intents`
- 技能 `/tools/skills`、`/tools/self-learning`、`/tools/skill-workshop`、`/tools/custodian-skills`、`/reference/AGENTS.default`
- 插件 `/plugins/memory-wiki`、`/plugins/knowledge`｜仓库 <https://github.com/openclaw/openclaw>（tagline "The lobster way 🦞"）

## 附 B：抓取方法备忘

`https://docs.openclaw.ai/<path>.md` 直接给干净 Markdown（`/llms.txt` 是全文索引）；`raw.githubusercontent.com` 在本环境不可达，改用 `cdn.jsdelivr.net/gh/openclaw/openclaw@main/<path>`；直接抓网页版会先吐整条导航侧栏、正文被截断，只在拿不到 `.md` 时兜底。
