# 02 · 记忆工程层：怎么写、怎么检索、怎么整合、怎么遗忘

> 决策问题：对话结束后总结出的"经验"，该做成 **memory / 可执行流程 / 本机 wiki** 中的哪一种？
> 本文件只负责**记忆基础设施层的事实**：五个成熟系统的写入/检索/整合/遗忘机制，以及它们**能不能承载"流程"这类知识**。
> 抓取时间 **2026-11-15**，优先官方 docs / 论文 / 官方博客。
> **证据等级标记**：Ⓐ 学术论文/第三方 benchmark ｜ ⓥ 厂商自报（同公司发布的产品数字）｜ ⓟ 预印本（未经同行评审）。表中未标者即官方文档的机制描述（事实性）。

## 0. 五个决策问题的直接答案

| 决策问题 | 事实答案 | 关键证据 |
|---|---|---|
| "流程/步骤"是否被当记忆存？ | **主流做法是"不存成记忆记录"**。语义/情节/程序三分法里的 procedural，在 LangMem/LangGraph 的实际实现是**用轨迹去重写 system prompt**；Letta 2026 把程序性知识**从常驻记忆块改为 Skills 文件目录**（`$MEMORY_DIR/skills/<name>/SKILL.md`，可带可执行脚本，随记忆仓版本化）；Anthropic 把同一形态定成开放标准 Agent Skills | §3 |
| 一条记忆被推翻怎么办？ | 三条路线：**覆盖**（LangMem 可选、Cognee）、**删除**（Mem0 2025 论文的 DELETE）、**保留+置失效**（Zep 四时间戳；Mem0 2026 产品改为 append-only + Dream.Supersede；Letta 用 git 历史）。**写入越自动，越倾向保留历史而非真删** | §4 |
| 写入是自主还是有门控？ | **没有任何一家成熟系统做人审写入门控**。门控信号是"压力/阈值/去重"：MemGPT 上下文 70% 触发 memory pressure；Generative Agents 重要度累计 >150 才反思；Letta 靠 agent 自判 durable + git 事后审计；可审计性靠 `/memory`、`/doctor`、`git log`、Memory History 事后查 | §5 |
| 规模变大检索怎么退化？ | 退化主因是**干扰项与新旧冲突**，不是容量：LongMemEval 商用/长上下文掉 ~30%；MemoryAgentBench 的 selective forgetting 全系统皆差且"cannot be solved by prompt engineering alone"；NeurIPS 2026 的 scale-conditioned 评测给出"可用规模边界"（HippoRAG 随无关会话增加掉 16–20pp） | §7 |
| 文本记忆 vs 长上下文 vs RAG？ | 同口径下**长上下文精度最高但成本高**（Mem0 自家论文：full-context J=72.90 vs Mem0 66.88，但 26,031 vs 1,764 tokens/查询、p95 17.12s vs 1.44s）；RAG 切块最弱（J 48.6–60.5，且再大的 chunk 也没追平）；记忆层买到的是**成本与延迟**，不是精度。2026 独立预印本更进一步：8k 条 SWE-bench Verified 上记忆层 resolve 率与无记忆基线差 ≤0.4pp | §8 |

## 1. 五个系统的记忆原语（2026-11 现状）

| 系统 | 核心抽象 | "一条记忆"是什么 | 组织形态 | 检索 |
|---|---|---|---|---|
| **Letta**（MemGPT） | MemFS：git 管理的记忆仓库 | Markdown 文件 + YAML frontmatter | 目录树；`MEMORY.md` 作索引 | 文件工具（grep/open/read）；**默认无向量索引**，语义检索靠可选 mod |
| **Mem0** | fact 抽取层 + 向量库（+ 图） | 抽取出的陈述句事实 + metadata | 集合，按 `user_id/agent_id/run_id` 作用域 | 向量语义 + 过滤；可选图/关键词/重排 |
| **Zep/Graphiti** | 时序知识图谱 | 边（fact 三元组）+ 时间戳 | 实体–关系图 + episode 溯源 | 语义 + 全文本 + 图遍历融合（auto search） |
| **LangMem/LangGraph** | Store（命名空间 KV）+ 记忆管理器 | 文档/记录（collection）或 profile | 命名空间 + collection/profile | 向量/自定义策略由开发者接 |
| **Cognee** | 知识图谱 + 本体 | 实体/关系三元组 | 图 + 自定义 data model/ontology | 图检索 + 向量 |

## 2. 演进主线：MemGPT 分层 → blocks → 文件系统（MemFS）

这条线是本报告最重要的信号：**"文件系统当记忆"是 2025→2026 被业界重新采纳的结果，不是倒退**。

| 阶段 | 时间 | 形态 | 官方说法/动机 |
|---|---|---|---|
| MemGPT | 2023-10 | OS 式分层：core memory（常驻）、recall（消息历史）、archival（外部向量库） | "virtual context management"；上下文超 70% 插 memory pressure warning，100% 强制 flush（[paper](https://arxiv.org/abs/2310.08560)） |
| Memory blocks + sleep-time | 2024–2025 | blocks 常驻 prompt（persona/human 等，默认 5000 字符上限），agent 用 memory 工具自编辑；sleep-time 后台重写 | blocks"always visible - no retrieval needed"；官方举例就包括"**Store tool usage guidelines so agents avoid past mistakes**"（[blocks 文档](https://docs.letta.com/v1-sdk/memory/memory-blocks/)） |
| Letta Filesystem | 2025-08 | 文件挂载 + `grep/search_files/open` 工具；LoCoMo **74.0%**（gpt-4o-mini）> Mem0 自报的图记忆 68.5% | "agent capabilities matter more than the tools"；专门工具不如 agent 自主迭代检索（[博客](https://www.letta.com/blog/benchmarking-ai-agent-memory)）ⓥ |
| Context Repositories / MemFS | 2026-02 起 | 每 agent 一个 **git 仓**，投影成本地 checkout，用普通文件工具读写，commit/push 后成为"记忆本身" | 动机①此前记忆被限制在"MemGPT 式 memory 工具或虚拟文件操作"里；②文件是人和 agent 都能用的通用原语，可链式组合与脚本化；③git 让**并发记忆 subagent**（worktree）与冲突合并成为可能（[博客](https://www.letta.com/blog/context-repositories)） |

**MemFS 的具体机制**（对"wiki 式文件记忆"最直接的一手证据）：

| 机制 | 做法 |
|---|---|
| 常驻 vs 惰性 | 记忆根目录的文件每轮进 system prompt（身份/重要偏好/持久项目事实/**关键 workflow 规则**）；带自己 `MEMORY.md` 索引的子目录默认不进上下文，靠名字当路标按需读 |
| 寻址 | 按路径寻址：label=`persona` → `persona.md`；"选择 label 就是选择它住哪、以及是否常驻上下文" |
| 版本化 | 每次编辑都 commit → 历史、冲突解决、"已保存/未提交"边界；记忆 subagent（dreaming / memory doctor）用 git worktree 并发改 |
| 检索 | **默认不做向量索引**；靠文件搜索 + 读。可选 `memfs-search` mod 提供关键词/语义/混合 |
| 程序性知识 | 技能放在 `$MEMORY_DIR/skills/`，随记忆一起版本化、随 agent 迁移（见 §3） |
| 容量 | Cloud 免费 5 MiB / Pro 50 MiB 每个记忆仓；`/doctor` 审计"位置、重复、system-prompt token 占用"（[文档](https://docs.letta.com/configuration/memory)） |

## 3. 程序性知识到底存在哪（本报告核心问题）

### 3.1 LangMem/LangGraph 的三分法，以及 procedural 的真实做法

| 类型 | 官方定义（用途） | 典型存储 | 实际实现 |
|---|---|---|---|
| Semantic | 事实与知识（用户偏好、知识三元组） | profile 或 collection | `create_memory_manager` 抽取/合并；collection 需自处理"删除/失效 vs 更新/合并" |
| Episodic | 过去经历（few-shot 例子、会话摘要） | collection | 同上，存"事件"供 few-shot |
| **Procedural** | **系统行为**（核心人格与响应方式） | **prompt 规则**（taxonomy 也允许 collection） | **`create_prompt_optimizer`：喂 trajectory（含 score/comment/revised）→ metaprompt 或 gradient 两轮反思，直接重写 system prompt** |

来源：[conceptual guide](https://langchain-ai.github.io/langmem/concepts/conceptual_guide/)、[优化 prompt 指南](https://langchain-ai.github.io/langmem/guides/optimize_memory_prompt/)。LangGraph 文档说得更直白：**"对 AI agent 而言，procedural memory 是模型权重、agent 代码、agent 的 prompt 三者之和"**，实践中"改权重/改代码不常见，改 prompt 常见"，方法就是 reflection/meta-prompting（[docs](https://docs.langchain.com/oss/python/concepts/memory)）。

→ **结论**：三分法里的 procedural ≠ "存一条步骤序列的记忆"；它是**对指令文本的在线优化**。差别在于：prompt 优化产出的是"风格/原则级规则"，无步骤、无前置条件、无失败分支、无版本 diff（旧 prompt 被覆盖）；而"写成文档"能表达步骤、可评审、可回滚、可带脚本。

### 3.2 Letta 的两次迁移（最有信息量的样本）

- **blocks 时代**：程序性知识被当成**记忆块里的文本**（官方例子："存工具使用指南，让 agent 避免重犯过去的错"），靠 5000 字符的 block 常驻 prompt。
- **MemFS 时代**：程序性知识**从常驻记忆块搬到 Skills 目录**：`$MEMORY_DIR/skills/<name>/SKILL.md` + 可执行脚本；agent 可以**把 skill 写进自己的记忆系统**，使其随 agent 迁移（[skills 文档](https://docs.letta.com/configuration/skills)）。Skills 采用开放 Agent Skills 标准，可在 Letta Code / Codex / Claude Code 等之间移植。

### 3.3 其余系统：没有 procedural 这个类

| 系统 | 能否表达"可执行流程" | 原因 |
|---|---|---|
| Mem0 | 否 | 只有抽取出的 fact；"用户偏好/决定"能存，步骤序列会被切成互不相关的事实 |
| Zep/Graphiti | 否（声明式经验可以，过程不行） | 基础模型是"实体–关系–时间"，一条流程会被拆成多个事实。2026 新增 **Observations**="durable, evidence-backed pattern, decision, or commitment across entities"——最接近"沉淀出的经验"的原语，但**仍是声明式摘要、不可执行**，且需更高档套餐 |
| Cognee | 弱 | 本体可定义流程类实体，但仍是知识图谱中的声明式数据；不负责"执行" |

### 3.4 行业实际收敛到的形态：Skill 文件

Anthropic 官方把 Agent Skills 的目的写成"**capturing and sharing their procedural knowledge**"：一个目录 = `SKILL.md`（YAML frontmatter 的 `name`+`description`）+ `scripts/` + `references/`；**三层渐进披露**：① name/description 常驻 system prompt ② 命中后读正文 ③ 正文引用的脚本/参考再按需打开——"带文件系统和代码工具的 agent 不必把整个 skill 读进上下文，因此 skill 能打包的上下文实际上是**无界的**"（[工程博客](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills)，2025-12 起为开放标准）。
→ 这正是"wiki 式文件记忆"被重新采纳的机制原因：**程序性知识的单位是文件目录，不是记忆记录**。

## 4. 一条记忆被推翻/过时时，各系统怎么做

| 系统 | 机制 | 旧条目结局 | 时间语义 |
|---|---|---|---|
| Mem0（2025-04 论文） | LLM 直接选 `ADD/UPDATE/DELETE/NOOP`；`DELETE` = 把矛盾条目从集合里移除 | **删除**（不可回查） | 无 |
| Mem0（2026 产品） | 自动抽取路径**改为 append-only**："新记忆不会覆盖或删除旧记忆"；由 Dream 的 **Supersede** 把旧事实标为过时、**Merge** 去重（二者默认常开）；另有 `expiration_date` 到期隐藏（不删）与 Memory History 变更日志 | **保留，标记过时** | 有过期日期，无事件时间 |
| Zep/Graphiti | 新信息使旧边**失效**而非删除；边带四个时间戳 `created_at / valid_at / invalid_at / expired_at`，支持时间点查询；被否证时还会新建事实 | **保留 + 写入失效时间** | **bi-temporal**（事件时间 vs 摄入时间） |
| Letta MemFS | 文件被覆盖，但每次编辑 commit；可 diff/回滚；dreaming 后台合并 | **保留 git 历史** | commit 时间 |
| LangMem | 文档给了两种选择："delete/invalidate 或 update/consolidate"，由开发者指令决定 | 取决于配置 | 无 |
| Cognee | 重新跑 pipeline / 更新图 | 覆盖 | 无 |

关键洞察：**写入越自动，越不敢真删**。Mem0 从论文的 `DELETE` 退回到"追加 + 后台 supersede + 过期隐藏"；Zep 用失效时间保留全部历史；Letta 用 git 保住所有版本。只有"人能看全历史 + 能回滚"时，删除才是安全的。

## 5. 写入：谁写、何时写、有没有门控

| 系统 | 写入者 | 触发时机 | 门控/门槛 |
|---|---|---|---|
| MemGPT | agent 自决 | 每轮都可调 memory 工具；**上下文达 70% 时注入 memory pressure system message**，100% 强制 flush | 上下文压力（不是重要度） |
| Letta MemFS | agent 自决；用户可 `/remember` 显式教 | agent 判定"durable"；`/init` 引导初始化 | **无人工审批**。可选 "Agent reviews before applying" = 让第二个后台会话复审，官方明说"**does not ask you for approval**"；事后 `/memory`、`/doctor`、git log 审计 |
| Mem0 | 应用调用 `add()` | 每次交互后 | `infer=True/False`；自动路径无写入门槛，靠上下文查重 + dedup |
| Zep | 应用 `add_episode` | 每个 episode | 无；由 LLM 判断是否使旧边失效 |
| LangMem | 开发者在 hot path（同轮，增加延迟）或 background（会话后）二选一 | 配置决定 | 无内建审批；`enable_inserts` 控制能否新增 profile 字段 |
| Generative Agents Ⓐ | 系统 | **重要度累计 > 150** 才触发 reflection | 阈值门控（约每天 2–3 次） |
| Anthropic memory tool | agent | 工具调用 | 无；文件存**客户端**基础设施，开发者控制持久化 |

→ **"写入门槛"的行业共识是"信号 + 事后审计"，不是"人审弹窗"**。可借的信号：会话结束/step 数/context 压缩/重要度累计/去重判定。

## 6. 整合、遗忘、后台整理（consolidation / sleep-time）

| 机制 | 系统 | 触发 | 已公布的参数/效果 |
|---|---|---|---|
| **Dreaming**（后台 subagent 复盘近期会话、consolidate lessons、改记忆） | Letta | 每 N 个完成 step，或 context 被压缩时 | 可选"再跑一轮 review"（更烧 token）；`/doctor` 处理"层级漂移/过大"；记忆仓 5/50 MiB |
| **Dream**：Supersede + Merge 常开，Synthesis（把多条提炼成高阶模式记忆）需 Pro 且 opt-in | Mem0 | 写入时 / 定时 | pattern memories **与源记忆并存**，不替换 |
| **Memory Decay** 软衰减：按最近检索历史给排序乘 0.3×–1.5×，**永不置零**，opt-in，候选池 `top_k×3`（≥50） | Mem0 | 检索时 | 明确是"排序偏置，不是过滤器" |
| **Observations**（跨实体/跨 episode 的"持久模式、决定、承诺"）+ thread/document summaries | Zep | 从图派生（ingest/后台） | 作为 Context Block 的可选类型；Observations 仅在 Flex Plus / Enterprise 提供 |
| **background memory formation** | LangMem | 会话后 | 目标是不增加交互延迟 |
| **sleep-time compute**（离线预先思考） | Letta Ⓐ | 空闲期 | 达到同精度所需 test-time compute **×1/5**；扩大 sleep 计算再 +13%（Stateful GSM-Symbolic）/+18%（Stateful AIME）；多查询摊销后每查询成本 ×1/2.5 |

⚠ 口径提醒：sleep-time 的数字来自**合成的 stateful 推理任务**，不是记忆 QA benchmark；不能当成"整理记忆能提升问答准确率"的证据，只能当"离线整理可以换在线算力"的机制证据。

## 7. 规模变大后检索质量怎么退化

| 证据 | 规模 | 结论 |
|---|---|---|
| LongMemEval Ⓐ | 500 题，历史可扩展 | 商用品与长上下文 LLM 在跨会话记忆上**掉 ~30%**；瓶颈拆成 indexing/retrieval/reading 三段，优化点是会话分解、fact 增强的 key、时间感知查询扩展 |
| MemoryAgentBench Ⓐ | 6K/32K/64K/262K token，干扰项比 1:100 | 四项能力里 **selective forgetting（新旧事实冲突）全系统都差**，且"**cannot be solved by prompt engineering alone**"，需要专门的记忆机制设计 |
| Scale-conditioned 评测（NeurIPS 2026）Ⓐ | 固定任务证据 + 持续追加无关会话 | 报告"**可用规模边界**"：HippoRAG 在 LongMemEval 上随无关会话增加损失 **16–20pp** 的 budget-compliant reliability；结论要求任何"可扩展记忆"声明都必须绑定 agent/接口/规模区间/调用预算 |
| Mem0 自家动因 ⓥ | — | Memory Decay 的存在理由就是"结果被旧事实挤满" |
| Letta 自家动因 ⓥ | — | `/doctor` 用来处理"hierarchy drifted or grown too large" |

→ 退化来自**干扰项、重复、冲突**，不是单纯容量。对策是**层级/索引/后台整合**，而不是调大 `top_k` 或换更强向量库（See §8：向量/图记忆的边际收益极小）。

## 8. 三条路线在长期任务上的实测对比

### 8.1 Mem0 论文 Table 2（LoCoMo，LLM-as-Judge，同一套题、同一评测口径）ⓥ

| 方法 | tokens/查询 | p95 总延迟(s) | J 分 |
|---|---|---|---|
| Full-context（整段历史塞进去） | 26,031 | 17.12 | **72.90** |
| Mem0 + 图记忆 | 3,616 | 2.59 | 68.44 |
| Mem0（基础） | 1,764 | 1.44 | 66.88 |
| Zep（**由 Mem0 团队复测**） | 3,911 | 2.93 | 65.99 |
| LangMem | 127 | 60.40 | 58.10 |
| OpenAI memory | 4,437 | 0.89 | 52.90 |
| A-Mem | 2,520 | 4.37 | 48.38 |
| RAG（chunk 2048 / 4096 / 8192） | — | — | 48.57 / 51.79 / 60.53 |

**读法**：① 同一张表里 **full-context 精度最高**，记忆层用 15× token、12× p95 只换来 6pp 回退——记忆层的卖点是成本/延迟，不是精度；② 记忆系统之间差距（66.88 vs 68.44）远小于"记忆 vs 长上下文"的差距，**自研花样的边际收益很小**；③ 朴素 RAG 最差。

### 8.2 其他一手数字

| 来源 | 数字 | 性质 |
|---|---|---|
| Letta：把 LoCoMo 历史塞进文件 + `grep/search_files` | **74.0%**（gpt-4o-mini），> Mem0 自报图记忆 68.5% | ⓥ 厂商自报、单点 |
| LongMemEval | 长上下文/商用助手跨会话记忆掉 ~30%；时间推理与"拒答"是其五能力中的两项 | Ⓐ |
| MemoryAgentBench | 统一协议下无一家全面领先；selective forgetting 普遍失败 | Ⓐ |
| Delivered, Not Used（2026-11） | 8,000 条 SWE-bench Verified × 3 个记忆系统：**resolve 率与无记忆基线差 ≤0.4pp**；多数投递条目无关、多数相关条目未被使用、两个系统里多数投递只是复述 agent 已经能看到的内容 | Ⓟ 独立预印本 |

→ 综合：**在"答历史问题"上记忆层≈长上下文但更便宜；在"把任务做成"上，记忆层目前没有被测出稳定收益**。这直接质疑"把经验塞进记忆库就能提升 agent 能力"的前提。

## 9. 厂商自报 vs 独立复现：口径差异必须写清

| 宣称 | 出处 | 口径问题 |
|---|---|---|
| Mem0 相对 OpenAI +26%（66.88 vs 52.90） | Mem0 论文 | 自报，但同表公开了对自己不利的 full-context 72.90，可信度较高 |
| Zep DMR 94.8 vs MemGPT 93.4 | Zep 论文 | **DMR 是 MemGPT 团队自己定的指标**，非第三方；且 Mem0 复测 Zep 的 LoCoMo 只有 65.99，两个数字不可比 |
| Zep LongMemEval 最高 +18.5%、延迟 −90% | Zep 论文 | 自报；对照组的实现与调参由其自定，未见第三方复现 |
| Letta 文件系统 74.0% | Letta 博客 | 自报；同时公开质疑 Mem0 论文里"MemGPT 的数字无法复现"，[issue #3004](https://github.com/mem0ai/mem0/issues/3004) 由 Letta 打开、**0 回复关闭** |
| "记忆系统比 RAG/长上下文好" | 各家博客 | 换 benchmark 就换赢家；Letta 直言"current memory benchmarks may not be very meaningful"，评估重心正转向 LongMemEval / MemoryAgentBench / 任务内评测 |

## 10. 评估方法清单（选型时看这张就够）

| benchmark | 规模 | 测什么 | 坑 |
|---|---|---|---|
| LoCoMo Ⓐ | ~300 轮 / 9K token / ≤35 会话 | QA 检索（单跳/时间/多跳/开放域） | 以检索为主；长上下文与 RAG 都"有提升但仍远低于人类"；已被质疑区分不出记忆工具（Letta：文件系统 74.0 > 各家宣传） |
| LongMemEval Ⓐ | 500 题 | 5 项能力：信息抽取、多会话推理、时间推理、知识更新、拒答 | 最贴近"经验过期"场景；商用系统掉 ~30% |
| MemoryAgentBench Ⓐ | 4 能力，6K–262K | 检索 / 测试时学习 / 长程理解 / **选择性遗忘** | 少数专门测"旧记忆该被忘掉"的公开 benchmark（FactConsolidation，1:100 干扰） |
| DMR | MemGPT 自建 | 对话记忆检索 | 单一厂商定义，易被针对 |
| MemUse / SWE-bench Verified Ⓟ | 8k episodes | 记忆在真实任务中**是否被用到** | 揭示"投递≠使用"，outcome 指标会掩盖机制好坏 |

## 11. 哪些机制可以直接借到"经验沉淀"，哪些不适用

### 11.1 可直接借用（按性价比排序）

| 机制 | 来源 | 借用方式 | 为什么适合 |
|---|---|---|---|
| **文件系统当记忆 + git 版本化** | Letta MemFS | 经验库 = 一个 git 仓；每次编辑 commit，可 diff/回滚/并发（worktree） | 直接支撑"本机 wiki"路线；审计、冲突、并发三件事一次解决 |
| **渐进披露（根目录常驻 + 子目录索引惰性加载 + frontmatter description）** | Letta MemFS / Agent Skills | 只把"索引 + 少量常驻规则"放进 system prompt，其余按需读 | 把上下文成本从 O(全部经验) 降到 O(索引)，且与 §7"退化来自干扰"一致 |
| **程序性知识用 skill 目录，不用记忆记录** | Letta Skills / Anthropic Agent Skills | `skills/<name>/SKILL.md` + 可选 `scripts/`；name+description 常驻，正文按需 | 业界唯一收敛格式；能表达步骤、前置条件、脚本、失败分支 |
| **append-only + 后台 supersede/merge，保留历史** | Mem0 Dream / Zep 失效 / Letta git | 新经验追加；旧的标"已被取代"并留指针，不物理删 | 解决"更好的做法推翻旧做法"且不怕误判 |
| **软衰减 + 到期隐藏** | Mem0 Decay/Expiration | 只做排序偏置（如 0.3×–1.5×，永不置零、可关），过期只隐藏 | 极低成本，避免旧经验长期霸榜 |
| **阈值触发写入** | MemGPT 70% 压力 / Generative Agents 150 重要度 | 对话结束、上下文压缩、step 数、重要度累计 任一达阈值才沉淀 | 不需要人审弹窗，摩擦最小；配合事后审计 |
| **事后再审而非事前审批** | Letta `/memory` `/doctor`、Mem0 Memory History | 提供"看已沉淀了什么 + 逐条回滚/编辑"的界面 | 业界无一家做人审门控；事后成本更低 |
| **写入侧去重（实体/语义等价判定）** | Mem0 | 先查重再写，宁少写不多写 | 规模退化的第一因是重复与干扰 |

### 11.2 不适用（以及为什么）

| 机制 | 为什么不要直接搬 |
|---|---|
| 向量库/图数据库作为默认记忆后端 | Letta 2026 **默认零向量索引**；Mem0 图记忆只 +2%（68.44 vs 66.88）；同一论文里 full-context 精度更高。对单机桌面客户端，运维与延迟成本不划算 |
| bi-temporal 四时间戳 | 为"会变化的事实"（CRM/客服/账户状态）设计；"流程经验"没有事件时间语义，四列时间戳是过度设计。只需 `created/updated/superseded_by` |
| 自动事实抽取（Mem0/A-Mem 式切句） | 会把"步骤/前置条件/失败模式"切成互不相关的事实，丢失流程结构；且独立评测显示多数投递条目与任务无关 |
| 人审写入门控（审批弹窗） | 没有一家成熟系统这么做；会显著增加摩擦，也不解决"冲突判定"这个真问题。改为事后审计 + 回滚 |
| 云端配额（5/50 MiB） | 多租户成本控制产物；本机 wiki 设硬配额会截断价值，应改为"体积提示 + 体检（doctor）" |
| sleep-time 的数字作为升级理由 | 其收益来自合成 stateful 推理任务，不是记忆 QA；可借用**机制**（离线整理），不能借用**结论** |
| 把 procedural memory 实现成 prompt 自动改写 | 覆盖式、无 diff、无审批，规则会漂移；经验沉淀需要可评审的产物，文件路线（skill）天然满足 |

### 11.3 对"三选一"决策的边界建议

**一条经验按"下次怎么被用到"分流，三者共用同一套文件系统 + git 历史：**

| 经验形状 | 落点 | 检索时序 |
|---|---|---|
| "用户偏好 X / 项目事实 Y"（陈述性、高频、要每次生效） | **memory**：小的常驻规则文件 + 索引条目 | 每次进上下文（贵，所以要小） |
| "做 Z 这类事的步骤/前置条件/坑"（程序性、可复用） | **skill 文件**（`SKILL.md` + 脚本），description 常驻 | 命中才读正文 |
| "资料/参考/长背景" | **wiki 页**，索引里只留一行摘要 | 需要时 agent 自己 grep/open |

记忆层只承载"指针 + 偏好"，**不承载流程**；流程的唯一格式是 skill 文件；wiki 是二者的共同底座。这也是 Letta 2026、Anthropic Agent Skills、以及 01 号报告里产品侧（Devin/Windsurf/Cursor 把工作流收编为 skill）指向的同一个方向。

## 12. 出处

- MemGPT：<https://arxiv.org/abs/2310.08560> ｜ Letta memory blocks：<https://docs.letta.com/v1-sdk/memory/memory-blocks/>
- Letta MemFS：<https://docs.letta.com/concepts/memfs> ｜ Memory & dreaming：<https://docs.letta.com/configuration/memory> ｜ Skills：<https://docs.letta.com/configuration/skills>
- Letta Context Repositories（2026-02）：<https://www.letta.com/blog/context-repositories> ｜ 文件系统 LoCoMo（2025-08）：<https://www.letta.com/blog/benchmarking-ai-agent-memory>
- Sleep-time compute：<https://www.letta.com/blog/sleep-time-compute>、<https://arxiv.org/abs/2504.13171>
- Mem0 论文：<https://arxiv.org/abs/2504.19413>（ADD/UPDATE/DELETE/NOOP 与 Table 2）｜ how-it-works：<https://docs.mem0.ai/core-concepts/how-it-works> ｜ add（append-only）：<https://docs.mem0.ai/core-concepts/memory-operations/add>
- Mem0 Dream：<https://docs.mem0.ai/platform/features/dream> ｜ Decay：<https://docs.mem0.ai/platform/features/memory-decay> ｜ Expiration：<https://docs.mem0.ai/platform/features/memory-expiration>
- Zep 论文：<https://arxiv.org/abs/2501.13956> ｜ Facts/四时间戳：<https://help.getzep.com/v4/facts.md> ｜ 检索：<https://help.getzep.com/v4/retrieving-context.md> ｜ Graphiti overview：<https://help.getzep.com/graphiti/getting-started/overview.md>
- LangMem 概念：<https://langchain-ai.github.io/langmem/concepts/conceptual_guide/> ｜ prompt optimizer：<https://langchain-ai.github.io/langmem/guides/optimize_memory_prompt/> ｜ LangGraph memory：<https://docs.langchain.com/oss/python/concepts/memory> ｜ 上下文工程四分法：<https://blog.langchain.com/context-engineering-for-agents/>
- Cognee 论文：<https://arxiv.org/abs/2505.24478> ｜ README：<https://github.com/topoteretes/cognee>
- Anthropic memory tool（2025-09）：<https://www.anthropic.com/news/context-management> ｜ Agent Skills（2025-12）：<https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills>
- Generative Agents：<https://arxiv.org/abs/2304.03442> ｜ LoCoMo：<https://arxiv.org/abs/2402.17753> ｜ LongMemEval：<https://arxiv.org/abs/2410.10813> ｜ MemoryAgentBench：<https://arxiv.org/abs/2507.05257>
- Scale-conditioned 评测（NeurIPS 2026）：<https://arxiv.org/abs/2605.07313> ｜ Delivered, Not Used（2026-11 预印本）：<https://arxiv.org/abs/2611.01718>
- Mem0 复现争议：<https://github.com/mem0ai/mem0/issues/3004>
