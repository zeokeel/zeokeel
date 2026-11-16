# 00 · 经验沉淀：记忆 vs 可执行流程 vs 本机 wiki —— 业界对比与裁决

> 决策问题：一次对话完成工作后总结出来的经验，做成 **记忆(memory)** / **可执行流程(workflow·skill)** / **本机 wiki** 中的哪一种，后续才能被稳定复用？
> 调研日 **2026-11-15**。四路子报告：产品侧 [`01`](01-product-agents.md)（8 个 agent 产品）、记忆工程 [`02`](02-memory-infra.md)（5 个记忆系统 + benchmark）、学术 [`03`](03-academic-sota.md)（15 篇论文）、OpenClaw/龙虾 [`04`](04-openclaw.md)（官方文档）。本文件只做综合裁决，事实与出处见子报告。
> 证据分级：`A` 多数据集可复现 / `B` 单 lab 多 benchmark / `C` 单环境案例 / `D` 概念综述；`ⓥ` 厂商自报、`Ⓟ` 未评审预印本。

---

## 0. 一句话结论

**这不是三选一。业界在 2025→2026 已经把它拆成了三个不同层，并且给出了明确分工：**

| 层 | 承载什么 | 形态 | 检索时序 |
|---|---|---|---|
| **底板** | 可信、可审计、可回滚的存储 | 工作区文件系统 + git（"wiki 形态"） | 人/agent 都能读写，可 grep、可 diff |
| **索引** | 每个知识单元的"一行触发词" | 常驻的 `name+description` 清单 | 每轮都在上下文里，极便宜 |
| **流程** | 可复用的步骤、前置条件、坑、脚本 | `SKILL.md` 目录（Agent Skills 开放标准） | 命中 description 才加载正文，脚本再按需 |
| **事实** | 偏好、决定、项目事实、线索 | 小而精的 memory 条目 | 常驻 or 阈值触发召回 |

**三个可直接执行的判决：**

1. **"后续都可以按照这个执行" = 程序性知识 = 只能是 skill/workflow。** 记忆的形态（一行事实）表达不了步骤、前置条件、失败分支；学术上把流程塞进记忆是"把步骤切成互不相关的事实"（[`02`](02-memory-infra.md) §3）。
2. **wiki 不是入口，是底板。** 它的正确位置是"skill 和 memory 共用的 git 仓库 + 人读视图"，不是"下次任务去查的知识库"。业界已经把独立 wiki 型原语**撤掉了**：Devin 废弃 Knowledge、Windsurf 废弃 Workflow（见 §2）。
3. **真正决定效果的既不是载体也不是格式，是"反馈质量 × 复用频率 × 触发命中率"。** 学术上格式之争被证伪（[`03`](03-academic-sota.md) §2.1：code vs 文本 workflow 差异不显著）；产品上决定成败的是 description 写得对不对、验证门有没有（§4）。

---

## 1. 判据：把"内容类型"换成"检索时序"

8 个产品（Windsurf/Claude Code/Cursor/Cline/Codex/Devin/Copilot/Amp）**全部**能映射到同一张四层表——这是报告里最硬的不变量（[`01`](01-product-agents.md) §11）：

| 检索时序 | 原语 | 谁写 | 成本 | 典型 |
|---|---|---|---|---|
| **每次都要** | 常驻规则文件（`AGENTS.md`/`CLAUDE.md`/rules） | 人 | 每轮都付 token | "本仓库用 pnpm，别用 npm" |
| **可能要用** | `SKILL.md`（description 常驻 + 正文按需 + 脚本再按需） | 人 / agent 提议 + 人审 | O(索引)，命中才付正文 | "发布流程、迁移步骤、踩过的坑" |
| **确定要人发起 / 有副作用** | slash 流程、prompt file、`disable-model-invocation` | 人 | 零常驻 | "清库、发版、改线上配置" |
| **agent 自己学到、只对我有效** | auto memory（行级、带来源时间、可老化） | agent | 常驻预算内 | "我偏好 TypeScript" |

推论（对设计最有用的一条）：**没有 description/trigger 的经验 = 不可检索 = 只能人肉 `@`。** Cursor 没有 frontmatter 的规则就只能手动引用；Devin Knowledge 把 `Trigger Description` 设为必填；Claude 的 skill 是否被加载完全由 description 承担。**wiki 若不做"一行索引 + 正文"两段式，检索必然退化。**

---

## 2. 三家已经替我们做过这个选择的判决

### 2.1 Devin：把 wiki 型知识库**废弃**，把流程**收编**成 skill

- Knowledge（必填 trigger description 的 note 知识库）与 Playbooks（流程）**都已 deprecated，自动迁移为 Skills**；新账号自 2026-10-24 起没有 Playbooks。官方口径：**"Use skills for all new work"**。
- 但保留了 **Memory**（个人化、git 仓库形态、每行带 `[source: <session>; added: <date>]`、stale write 拒绝重试、**Dreaming 每天合并去重删 stale**）。
- 官方表格把边界写死了：**Memory = 与你共事时学到的（自动写）；Skill = 可重复的过程（人/团队写）**。
- → 结论：**"知识库型 note"作为独立一等原语被放弃；流程与知识都收敛到 `SKILL.md`。**

### 2.2 Windsurf → Devin Desktop：Workflow 原语被标注 manual-only 后让位给 Skill

- 唯一把 **Memories / Rules / Workflows 三件套并列**的产品，边界极清晰：Memories 绑 workspace、自动生成、只能人工编辑；Rules 人写、四种激活；Workflows 是 `.windsurf/workflows/*.md`、**只能 `/name` 手动触发**。
- 迁到 Devin Local 后只剩 **rules + skills**，原话："**Workflows are manual-only — Cascade will never invoke a workflow automatically**"，要自动触发请改用 Skill。
- → 结论：**"流程"必须能被 description 触发，否则它只是个宏。** 手动流程 = 你每次都得记得有它，复用率会掉到接近零。

### 2.3 OpenClaw（龙虾）：工作区即记忆，但严格切成 memory / skill / 仓库文档

用户自己的日常形态（一个工作区 = 一个龙虾），也是"文件系统当记忆"最激进的生产实现（[`04`](04-openclaw.md)）：

| 它怎么切 | 官方原话 |
|---|---|
| 事实/偏好/决定 → **memory** | "Facts about the user or the world belong in memory." |
| 可复用方法 → **learned skills** | "**Learned skills are procedures, not memory**: the method for a task the user repeats…" |
| 某个 codebase 的知识 → **仓库文档/AGENTS.md** | "Knowledge about one codebase… belongs in that repository's docs or `AGENTS.md`。" |
| 意图/提醒 → **cron 或 SQLite standing intents** | "storing intentions as prose in a memory file is the least reliable design available" |

它的两条生产线值得单独记：

- **dreaming（记忆整理）**：默认开，cron `0 3 * * *`，light→REM→deep 三阶段，**只有 deep 写 `MEMORY.md`**；打分（relevance .30 / frequency .24 / query-diversity .15 / recency .15 / consolidation .10 / 概念丰富度 .06）+ 三重阈值门；模型只输出**操作决策（新增/合并/取代）**，由 writer 用带 `Source: path#Lx-Ly` 的片段拼装；旧条目丢失 >25% 直接回退 append-only；改前存 preimage，摘要写进人读的 `DREAMS.md`。
- **self-learning → Skill Workshop（经验→流程）**：累计 10 个 model iteration 后入队、空闲 30s 跑；只给一个 `skill_workshop` 工具；≤64 events / 32 KiB、16k 上下文、≤2 分钟；**一次最多一处改动**（改最相关那条 skill；没有才新建；同类合并 + `absorbed_into` 归档）；默认 `auto`、**改动即时生效且可一键 undo**，可选 `propose` 模式存 draft 等 `/learn drafts` 审；每天 **skill diet**（正文 >12KB 才动，整节挪进 `references/`，要求缩 ≥20% + 二次规则对比审查）；30 天没人用自动归档。

**它自己承认的三个坑**（比它的功能更有参考价值）：

1. **"Writing is the hard part."** 检索已经能和重度设计打平，让记忆系统退化的是**写入期策展**（引 LongMemEval）。
2. **生产审计发现：绝大多数自动捕获的记忆是运行时指令复述、heartbeat 噪声、召回反馈循环** —— 所以才有 session-kind gating（cron/heartbeat/subagent 会话不产生耐久记忆）+ recall-loop prevention（被注入过的内容永不再被抽取）。
3. provenance 误判的爆炸半径：`MEMORY.md` 被判为 untrusted → **整份被排除出自动上下文**，用户只看到"agent 突然失忆"+一条日志。

---

## 3. 实证账本：记忆层买的是成本，不是能力

这一节是"效果会更好吗"的唯一硬答案。

| 证据 | 数字 | 读法 |
|---|---|---|
| Mem0 论文 Table 2（LoCoMo，同口径）`ⓥ` | full-context **72.90** J（26,031 tok/查询，p95 17.12s）> Mem0+图 68.44（3,616 tok）> Mem0 66.88（1,764 tok）> Zep（Mem0 复测）65.99 > LangMem 58.10 > A-Mem 48.38；朴素 RAG 48.57–60.53 | **长上下文精度最高**；记忆层用 15× token、12× 延迟换 **6pp 回退**。记忆层之间只差 1.6pp → 自研花样边际收益极小 |
| "Delivered, Not Used"（2026-11 预印本）`Ⓟ` | 8,000 条 SWE-bench Verified × 3 个记忆系统：resolve 率与**无记忆基线差 ≤0.4pp** | **"把经验塞进记忆库就能让 agent 更能干"目前没被测出来**；多数投递条目无关、多数相关条目未被使用 |
| AWM（ICML 2025）`B` | WebArena **35.5 vs 23.5（+12.0 abs. / +51.1% rel.）**，每例仅归纳 ~7.4 个 workflow；Mind2Web step SR 45.1 vs 36.2（+8.9 abs. / +24.6% rel.） | **"从轨迹归纳可复用流程"这条路有受控增益**，但单 lab、仅 web 导航、无独立复现 |
| ASI / Voyager / SkillWeaver `B-/C` | ASI 比 AWM-text 只高 +4.1 abs.；Voyager 去掉 self-verification 物品数 **-73%**；ASI 仅 **15.6%** 的 turn 通过三重验证，而"验证"单独贡献 +3.8 abs. | **验证门是可靠性的分水岭**，不是格式 |
| ACE（ICLR 2026）`B` | delta patch 使 AppWorld offline avg **56.9 → 70.3（+13.4 abs.）**，适配延迟 -82.3%；反例：整体重写让 context 从 18,282 tokens 塌到 **122**，acc 66.7→**57.1**（低于无记忆 baseline 63.7） | **"周期性让模型重写整份经验"是明确的反模式**；条目级增量 + 确定性去重才安全 |
| LongMemEval / MemoryAgentBench `A` | 商用系统跨会话记忆掉 ~30%；**selective forgetting（新旧冲突）全系统皆差**，"cannot be solved by prompt engineering alone" | 规模退化的真因是**干扰与冲突**，不是容量；对策是索引/分层/整合，不是调大 top_k |
| MemoryGraft / KEEPER `C` | 注入"成功经验"→ 语义模仿 → **跨 session 持久行为漂移**；错误声明一旦进 memory 会持续保留 | **经验是一条可被投毒的输入**，必须当不可信数据处理 |

**两条可以当设计公理用：**
- 记忆层解决的是**成本/延迟/跨会话连续性**；**能力提升来自流程化 + 验证**。
- 最危险的不是"没沉淀"，而是"自动沉淀了错的"：错了会被 100% 复用，而且比没有更难发现。

---

## 4. 最先进的系统到底是怎么闭环的（可抄清单）

把 §2、§3 拼起来，2026 年 SOTA 的完整闭环是这 6 步，缺一步就退化：

| 步 | 机制 | 谁做得最好 | 关键参数/判据 |
|---|---|---|---|
| 1 触发 | 不是"会话结束就总结"，而是**阈值触发**：累计 N 个 model iteration / context 达 70% / compaction 前 flush / 空闲 30s | OpenClaw（10 iteration + 30s）、MemGPT（70% pressure）、Generative Agents（重要度累计 >150）、LangMem（background） | 触发太频繁 → 垃圾；太稀疏 → 忘光 |
| 2 分流 | 同一次复盘里**分两路写**：事实/偏好→memory，可复用方法→skill | OpenClaw `/learn`（一次同时落两边）、Claude Code | 唯一硬边界："procedures, not memory" |
| 3 写入形态 | **append-only + 条目级 delta + 确定性去重**；模型只输出"操作决策"，writer 用有出处的片段拼装 | ACE（delta +13.4 abs）、OpenClaw dreaming（deep phase） | 禁止整体重写、禁止全量历史追加（DC 反例 20.0→6.7） |
| 4 门控 | **验证后才入库**：执行/测试/重放轨迹；或阈值门（score + recall 次数 + query 多样性） | ASI（15.6% 通过率）、SkillWeaver、Voyager、TroVe、OpenClaw deep 门 | 没有验证基础设施时，"代码化"只拿到维护成本 |
| 5 可回滚 | 版本化 + preimage + 一键 undo + 人读审计面 | OpenClaw（最近 10 版、`DREAMS.md`、Learned chip + Undo）、Letta（git commit） | **没有一家做人审写入门控**（02 §5）；行业默认是"事后可查可回滚"，最多加可选的 propose 模式 |
| 6 防膨胀 | 未用归档 + skill diet + 软衰减 + 到期隐藏 | OpenClaw（30 天归档、>12KB 才 diet）、Mem0（0.3×–1.5× 软衰减、expiration 只隐藏不删） | 这些是"运维补丁"，但它们决定了 6 个月后还能不能用 |

**格式层面的结论：** Agent Skills（`SKILL.md`）已由 Anthropic 捐给 Linux Foundation 下的 Agentic AI Foundation，**70+ 客户端**接入（ChatGPT/Cursor/Copilot/VS Code…），官方称已发布 1M skills、~280M 次安装（[来源](https://www.anthropic.com/news/donating-agent-skills-to-the-agentic-ai-foundation)）。Cursor 甚至提供 `/migrate-to-skills` 把规则和 slash 命令转成 skills。**自造第四种格式是逆流。**

---

## 5. 反面清单（比正面清单更重要）

| 别做 | 证据 |
|---|---|
| ❌ 会话结束自动把总结灌进 memory | OpenClaw 生产审计：绝大多数自动捕获是运行时指令复述/心跳噪声/召回循环；「Writing is the hard part」；ACE 案例证明污染后比无记忆更差 |
| ❌ 周期性让模型重写整份经验/记忆 | ACE context collapse：18,282→122 tokens，acc 66.7→57.1（低于 baseline 63.7）；brevity bias 越写越短越泛 |
| ❌ 全量历史无差别追加 | Dynamic Cheatsheet：GPT-4o AIME-2024 full-history 20.0 → **6.7** |
| ❌ 每次沉淀就新建一篇 wiki/skill | skill 一多 description 互相撞车，命中率反而下降；OpenClaw 的默认动作是"改最相关那条，同类合并 + `absorbed_into` 归档"；Amp 每仓库上限 200 个 skill |
| ❌ 人审写入门控（审批弹窗） | 没有一家成熟系统这么做（`02` §5）；摩擦大且不解决冲突判定。正解是事后可查 + 回滚，或**可选** propose 模式 |
| ❌ 向量库/图数据库做默认后端 | Letta 2026 **默认零向量索引**（grep/open 就够）；Mem0 图记忆仅 +2%；单机桌面客户端的运维与延迟不划算 |
| ❌ bi-temporal 四时间戳、三阶段命名（light/REM/deep） | 为"会变化的事实"设计，流程经验没有事件时间语义；照抄复杂度拿不到收益（`04` §10 明确列为"别抄"） |
| ❌ 把"每次都必须发生"写成经验 | Claude/Copilot/Cursor 官方一致："指令是 context 不是配置"，要确定性用 hook/权限/CI |
| ❌ 指望"云端/headless/后台会话"也有这套 | OpenClaw active memory 明确不在 headless/heartbeat/subagent 跑，dreaming 也不摄取这些会话 |

---

## 6. 落到 ZeoKeel：现状盘点与四个缺口

### 6.1 现状（代码事实）

| 模块 | 今天有什么 | 与业界对照 |
|---|---|---|
| `internal/modules/memory` | JSON 持久化 rig.MemoryStore；`global/workspace:<id>` scope；consolidation（过期 + 超额 newest-first 裁剪）；**agent 侧有 `memory` 工具（add/list/search/update/remove/clear）** | 缺 provenance/taint 门、缺单一写入者、缺"写入期策展"→ 直接开放 agent 直写就是 §5 第一条反模式 |
| `internal/modules/skills` | 文件系统根优先级（workspace `skills/`、`.agents/skills`、`.zeokeel/skills`、extra dirs…）；List 只读 frontmatter+description+摘要、Read 才读全文（**天然就是 progressive disclosure**）；`contracts.SkillsEditor` 有 Create/Update/Delete | **写路径只挂在设置页（人写）**，agent 没有写入工具；**无版本历史 / 无 undo / 无归档** |
| `internal/modules/automation` | 定时/事件调度 | 是"何时跑"的载体，正好用来承载 dreaming 式的后台整理 |
| `internal/modules/goal` | 目标状态 | 与经验沉淀正交 |

**结论：今天的写权限分布和证据指向的方向是反的** —— 价值最低的 memory 对 agent 全开放，价值最高的 skills 对 agent 全关闭。所以"做记忆还是做 workflow"这个二选一在我们这里的真实形态是：**"要不要把 pipeline 补完整"**。

### 6.2 四个缺口（按性价比排序）

1. **`skill_workshop` 式的提议工具（不直写）**：给 agent 一个工具，产出 **draft**（目标 skill 的完整 `SKILL.md` 或 patch + reason），落到 `<dataDir>/skills/.drafts/`；设置在设置页已有 create/update/delete 的基础上加"审阅 draft → Apply / Discard"。默认 `propose`，成熟后再可选 `auto` + 一键 undo。
   - 复用现有 `contracts.SkillsEditor`，不新增格式、不新增存储层。
2. **阈值触发 + 一次一处改动**：别做"会话结束就总结"。学 OpenClaw：累计 N 次模型迭代 或 compaction 前 → 后台跑一次受预算约束的复盘（只给草案工具、限定事件数/KiB/超时），**每次最多改一处**，冲突时合并 + `absorbed_into`。
3. **版本化 + undo**：skills 每次写入前存旧版本（保留最近 N 版），提供 diff / restore；memory 侧至少保留"变更前快照"。没有回滚，propose 模式就白做。
4. **触发命中率 eval**：`agentskills.io` 有现成方法论——20 条 should/should-not-trigger 查询 × 3 次跑，算 trigger rate（阈值 0.5）；再加 with-skill vs without-skill 的基线对比。**这是唯一能回答"沉淀的东西有没有用"的闭环**，8 个产品里只有 Claude 做了。

### 6.3 该做什么、别做什么（一句话版）

- **做**：skill 提议/审阅/回滚；description 质量与触发率 eval；未用归档 + 体积体检；memory 写入加来源与作用域、把"偏好/事实"和"线索"分开。
- **不做**：不新建独立的"workflow 存储"或"知识库"模块（skill 目录就是它）；不做向量默认后端；不做审批弹窗；不做会话结束全自动总结直写。
- **wiki 的正确用法**：不从零做知识库产品。把 `docs/` 式的 Markdown 目录当**底座**——skill 和 memory 都存在文件里、可 git、可 grep；需要人读时由索引生成目录页。**它永远不做"下一次任务的检索入口"，入口永远是 description。**

---

## 7. 30 天验证方案（判据先于实现）

| 周 | 做什么 | 度量 |
|---|---|---|
| 1 | 选 3 类**反复做**的任务，只用手工沉淀：把一次成功会话写成 1 个 `SKILL.md`（含 gotchas + 验收标准） | 记录写一个 skill 的实际耗时 |
| 2 | 第二次遇到同类任务：不提示，看它**有没有被自动选中**、结果对不对 | **命中率**（该用的时候用了吗）/ 正确率 |
| 3 | 故意改一次环境（版本/路径），观察 skill 是否会误导 | 失效检出率；修复耗时 |
| 4 | 加 20 条 trigger eval 查询跑 3 次；对比 with/without skill | trigger rate、Δ正确率 |

判据：命中率 <50% → 问题在 description 与检索时机，不在存储格式；命中率高但正确率低 → 泛化过头，收窄 skill 的作用域并补验证门；维护成本 > 收益 → 这条经验根本不该沉淀成 skill（退回 memory 一行事实）。

---

## 8. 未解问题（写进设计时必须自知）

1. **写入质量没有通用解**：所有系统都在用"模型自评 + 阈值 + 事后审计"打补丁；`01` §12 指出"何时写"没有可验证的 trigger 定义。
2. **失效与遗忘没有一等机制**：TTL / dreaming / doctor / "犯错才改"都是补丁。
3. **有副作用的流程是否允许模型自启仍分歧**：Windsurf 干脆 manual-only，Claude 建议 `disable-model-invocation`，Devin/OpenClaw 允许自动调。
4. **没有跨载体的公平对比**：`03` §7 明确"未找到任何论文在同一 benchmark、同一模型上公平对比五种载体"，所以"哪种载体整体最优"只能分场景回答。
5. **2026-11 的"记忆不提升任务成功率"仍是未评审预印本**（`Ⓟ`），结论方向与 Mem0 论文的成本口径一致，但不宜当定论。

---

## 9. 子报告索引

| 文件 | 内容 | 关键贡献 |
|---|---|---|
| [`01-product-agents.md`](01-product-agents.md) | 8 个 agent 产品 × 6 问 + 横向对照 + 10 条不变量 | 判据是检索时序；Windsurf/Devin/Cursor 把流程收编为 skill |
| [`02-memory-infra.md`](02-memory-infra.md) | Letta/Mem0/Zep/LangMem/Cognee + benchmark | 程序性知识被搬出记忆；没有一家做人审门控；记忆买成本不买精度 |
| [`03-academic-sota.md`](03-academic-sota.md) | 15 篇论文 + 证据分级 + 不可外推清单 | 验证门 > 格式；delta > 重写；context collapse 与毒化 |
| [`04-openclaw.md`](04-openclaw.md) | 龙虾的工作区即记忆（一手官方文档） | dreaming 三阶段的参数、self-learning 的预算与 undo、官方自认的坑 |
