# Agent 自进化工程模式：外部调研与 ZeoKeel 落地取舍

> 任务：task-2（自进化 Agent 的工程模式外部调研）· 日期：2026-11-04 · 作者：evolution-researcher
> 目标场景：ZeoKeel —— 纯 Go 桌面 Agent（mygo UI + eino runtime）、本地 SQLite、内置 scheduler、单二进制、无服务端。
> 结论先行：**可落地的是「验证器门控的自修复 + 受控的轨迹/技能记忆 + 离线提示词优化」；不可直接落地的是「在线自改代码的进化搜索」与「LLM-as-judge 单独做门禁」。**

## 0. 一句话结论

| 问题 | 结论 |
| --- | --- |
| 最值得抄的 3 个模式 | ① Verifier-gated self-repair（测试/编译/像素断言当硬门禁，模型只负责 propose）② 轨迹→技能库（Reflexion/Voyager 式，带写入门控）③ 离线 GEPA 式 reflective prompt optimization（用本地 eval set 当 fitness） |
| 最大的 2 个风险 | ① Reward hacking / 测试过拟合（改测试、改判分、泄漏答案）② 无内核沙箱下的自改代码 + 不可审计（ZeoKeel 权限是策略级，见 PLAN.md R5） |
| 明确不做 | 在线 AlphaEvolve/DGM 式「Agent 改自己二进制并按自评分数进化」；无标注集/无 objective metric 的运行时提示词自优化；单模型自评当发布门禁 |

## 1. 证据分级与检索方法

| 等级 | 含义 | 本报告中的例子 |
| --- | --- | --- |
| A（有实证） | 同行评审/公开基准可复现，有量化数字 | Self-Debugging、Reflexion、Voyager、SWE-bench 系、GEPA、MIPROv2、OPRO、APE、AlphaEvolve、DGM、MT-Bench、JudgeBench、CriticGPT |
| B（工程实现） | 开源仓库/工程博客，可读代码但无严格对照实验 | OpenEvolve（AlphaEvolve 开源复刻）、DSPy 文档、SWE-agent/Agentless 仓库、AlphaEvolve 官方博客 |
| C（仅概念/推断） | 尚无公开实证，或需按 ZeoKeel 场景外推 | 「技能库长期漂移」的量化、设计稿 rubric 自评审的可靠性、无服务端场景的评测污染治理 |

检索：`web_search` 定位来源 + `web_fetch` 读 arXiv 摘要/官方页；引用均给 URL。注意：论文数字来自各自基准，**不可直接外推到 ZeoKeel 的 Go/桌面任务**。

## 2. 模式一：验证器门控自修复（self-repair / self-debugging / CI-driven repair）

**机制**：`propose → execute → verify(Δ) → accept | rollback`。关键不是「让模型反思」，而是**外部 verifier 的决定权**：测试、编译器、linter、类型检查、像素/几何断言。

| 工作 | 做法 | 量化结果 | 证据 |
| --- | --- | --- | --- |
| Self-Debugging | few-shot 教模型「rubber duck」式解释代码 + 执行反馈自修 | Spider（无单测）+2–3%、最难档 +9%；有单测时最多 +12%；复用失败样本可匹配 10× 候选采样 | [arXiv:2304.05128](https://arxiv.org/abs/2304.05128) |
| Reflexion | 失败后写自然语言反思存 episodic memory，下轮复用 | HumanEval pass@1 91%（同期 GPT-4 80%） | [arXiv:2303.11366](https://arxiv.org/abs/2303.11366) |
| Self-Refine | 同一模型生成→自评→改，迭代 | 7 类任务平均 ~+20% absolute（人类/自动指标偏好） | [arXiv:2303.17651](https://arxiv.org/abs/2303.17651) |
| SWE-bench | 真实 GitHub issue + `FAIL_TO_PASS` / `PASS_TO_PASS` 测试做门禁 | 初始最强 Claude 2 仅 1.96% | [arXiv:2310.06770](https://arxiv.org/abs/2310.06770) · [swebench.com](https://www.swebench.com) |
| SWE-agent | 为 Agent 设计 ACI（编辑/导航/执行接口） | SWE-bench pass@1 12.5%；HumanEvalFix 87.7% | [arXiv:2405.15793](https://arxiv.org/abs/2405.15793) |
| Agentless | 反直觉：定位→修复→补丁验证三阶段，无自主工具循环 | SWE-bench Lite 32.00%，$0.70/issue；简单可解释流程胜过复杂 agent | [arXiv:2407.01489](https://arxiv.org/abs/2407.01489) |
| SWE-bench+ | 人工审计成功补丁 | **32.67% 成功补丁存在答案泄漏；31.08% 测试过弱**；过滤后 SWE-Agent+GPT-4 从 12.47% 跌到 3.97%；94% issue 早于模型 cutoff | [arXiv:2410.06992](https://arxiv.org/abs/2410.06992) |

**失效条件（必须写进设计）**：
- **无外部反馈的自纠错会退化**：Huang et al. 证明 intrinsic self-correction 在推理任务上不稳定、有时越改越差 → 反思只能用于「提出候选」，不能替代 verifier。[arXiv:2310.01798](https://arxiv.org/abs/2310.01798)
- **测试是瓶颈**：测试弱 → 假阳性（SWE-bench+ 的 31.08%）；测试泄漏/写在 issue 里 → 假阳性（32.67%）。
- **过拟合测试**：多轮重试会朝「通过测试」而不是「正确」优化；需要 held-out 测试 + 最小 diff + 禁止改测试。
- **成本**：Agentless 的 $0.70/issue 说明即便是轻量流程也不免费；必须设 retry 预算（建议 1–3 次）。

**ZeoKeel 落点**：`internal/shell`（跑 `go test ./...`、`go vet`、`gofmt -l`）、`scripts/check-nocgo.sh`、`internal/pixeltest`（像素门禁）都可直接当 verifier；失败轨迹写 `tool_calls` + `events`，天然可审计。

## 3. 模式二：提示词/程序优化（GEPA / DSPy / OPRO / APE）

| 方法 | 输入 | 输出 | 是否需要标注集/指标 | 成本 | 适合优化什么 | 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| **GEPA**（Genetic-Pareto，ICLR 2026 Oral） | 任务 metric + 轨迹（推理/工具调用/工具输出） | 改写后的 prompt 组合（Pareto 前沿） | 需要 metric；不需要梯度 | 比 GRPO 少至多 **35×** rollout；比 MIPROv2 高 10%+；6 任务平均 +6%、最高 +20%；AIME-2025 +12% | 系统提示词、工具描述（文本组件） | [arXiv:2507.19457](https://arxiv.org/abs/2507.19457) · [repo](https://github.com/gepa-ai/gepa) |
| **DSPy / MIPROv2** | 程序（多阶段 LM 调用）+ 训练/开发集 + metric | 指令 + few-shot 联合优化 | 需要标注集与 metric | 中等；EMNLP 2024 | 多阶段 pipeline 的每模块 prompt | [arXiv:2406.11695](https://arxiv.org/abs/2406.11695) · [dspy.ai](https://dspy.ai/) |
| **OPRO** | 历史 prompt + 分数序列 | 新 prompt | 需要 dev set + 分数 | 每步都要评估，成本随迭代线性 | 单轮指令、数学/推理提示 | [arXiv:2309.03409](https://arxiv.org/abs/2309.03409) |
| **APE** | 候选指令池 + 打分函数 | 最优指令 | 需要任务样本与打分 | 中等；可并行 | 零样本指令 | [arXiv:2211.01910](https://arxiv.org/abs/2211.01910) |

**结论**：GEPA 是当前「少量 rollout + 可解释文本反馈」的最优解，但**它优化的是给定 metric**——metric 错，整套优化就在放大错误。对 ZeoKeel：可在**开发期离线**用 GEPA 式 reflective mutation 优化 `internal/prompt` 的 system prompt / 工具描述，fitness 用本地 eval（golden transcript、工具选择正确率、e2e 模型脚本）；**运行时自改 system prompt 不建议**（成本、污染、审计都不可控）。DSPy 是 Python 生态，无法进单二进制，只能作为「离线参考实现/思路来源」。

## 4. 模式三：轨迹记忆与技能库（Reflexion / Voyager / ExpeL / AWM）

| 工作 | 记忆形态 | 写入时机 | 检索 | 量化结果 | 漂移/风险 |
| --- | --- | --- | --- | --- | --- |
| Reflexion | 自然语言反思文本（episodic buffer） | 每次失败后 | 全量拼接进下轮 | HumanEval 91% | 反思可能记错原因；buffer 膨胀 |
| Voyager | **可执行代码技能库**（Minecraft 技能） | 技能自验证通过后写入 | 按任务描述检索组合 | 3.3× 独特物品、2.3× 距离、15.3× 更快解锁科技树；作者称可缓解 catastrophic forgetting | 技能过时/环境变化后静默失效 |
| ExpeL | 经验 + 抽取的 insight（自然语言） | 跨任务训练后 | 推理时召回 | 经验累积带来稳定提升（AAAI-24） | 经验可能互相矛盾 |
| AWM（Agent Workflow Memory） | 可复用 workflow（动作序列） | 从成功轨迹归纳 | 选择性注入 | Mind2Web/WebArena 相对提升 24.6% / 51.1% | workflow 粒度过粗会绑死策略 |

**落地方式**：轨迹原始数据进 SQLite（已有 `messages` / `tool_calls` / `events`），**反思/技能是派生物**，必须带 provenance（来源 session/turn、模型、验证结果、时间）。写入必须过门：只从「verifier 通过」的轨迹蒸馏；技能启用前跑一次回归 e2e；失败率超阈值自动降级/禁用。长期漂移目前**没有可靠量化研究（C 级）**，工程上按「可回滚的缓存」而非「真相」对待。

## 5. 模式四：进化搜索（AlphaEvolve / Darwin Gödel Machine）

| 系统 | 搜索空间 | 反馈/评估 | 关键结果 | 安全边界 | 证据 |
| --- | --- | --- | --- | --- | --- |
| **AlphaEvolve** | 代码片段（算法/调度/电路） | 一个或多个 evaluator 自动打分 | 4×4 复矩阵乘法 48 次标量乘（56 年来首次改进 Strassen 设定）；数据中心调度、电路简化、加速自身训练 | 依赖可自动验证的 evaluator（fitness 可计算）；Google 内部基础设施，论文强调评测函数质量而非「无人监督」 | [arXiv:2506.13131](https://arxiv.org/abs/2506.13131) · [DeepMind 博客](https://deepmind.google/discover/blog/alphaevolve-a-gemini-powered-coding-agent-for-designing-advanced-algorithms/) |
| **Darwin Gödel Machine** | Agent 自身代码库（工具、上下文管理、peer-review 机制） | SWE-bench / Polyglot 基准测试 | SWE-bench **20.0% → 50.0%**；Polyglot 14.2% → 30.7%；archive + 开放式探索优于无自改/无探索基线 | 论文明确：sandboxing + human oversight；保留 archive 可回滚 | [arXiv:2505.22954](https://arxiv.org/abs/2505.22954) · [repo](https://github.com/jennyzzt/dgm) |
| OpenEvolve | 同 AlphaEvolve 的开源复刻 | evaluator 脚本 | 工程实现（B 级），便于本地试跑 | 本地进程/容器隔离 | [github.com/codelion/openevolve](https://github.com/codelion/openevolve) |

**安全边界（综合 DGM/AlphaEvolve 做法，可直接抄成策略）**：① 每次变异在隔离 worktree/沙箱内跑，评测全程禁网；② 硬预算（wall-clock、token、试跑次数）；③ 只允许「候选」进入 archive，**不自动合并**；④ 人类闸门做最终 merge；⑤ evaluator 与候选生成器分离，评测集 held-out；⑥ 保留父代 + 一键回滚。

## 6. 模式五：产品/设计自评审（LLM-as-judge、rubric review）

| 证据 | 结论 | 对 ZeoKeel 的含义 |
| --- | --- | --- |
| MT-Bench / Chatbot Arena（NeurIPS 2023） | GPT-4 类强 judge 与人类偏好 >80% 一致（≈人类间一致率）；存在 position / verbosity / self-enhancement bias | judge 只能做「偏好近似」，不能做正确性门禁 |
| JudgeBench（ICLR 2025） | 在知识/推理/数学/代码的「客观正确性」任务上，强 judge 接近随机（GPT-4o 仅略高于随机） | 代码/设计**正确性**必须用测试/像素断言，而非 judge |
| Self-preference | 模型能识别自己的输出，且 self-recognition 与 self-preference 强度线性相关 | 生成者与评审者必须不同模型/不同 prompt；blind A/B |
| CriticGPT（OpenAI） | 模型 critique 在 63% 情况下优于人类 critique；**human+critic 团队**在幻觉更少的同时抓到相近数量 bug | 评审要「模型提议 + 人类抽检」组合，而不是纯模型 |

**可操作校准协议（建议写进评审 prompt 与流程）**：
1. **rubric 先于打分**：把「好设计」拆成可验证条目（对齐/间距/对比度/信息层级/文案长度…），每条给判定标准与反例；能客观测的（像素、几何、对比度）不进 rubric，直接进断言。
2. **成对 blind 比较**优于绝对分；交换 A/B 位置各评一次，抵消 position bias。
3. **ground truth 锚点**：每次评审混入 1–2 个已知好/坏样本（canary），judge 若不通过 canary 则本次评审整体作废。
4. **多 judge 投票 + 分歧上报**；judge 温度 0，固定 rubric 版本与模型快照。
5. **人类抽检**：按 10–20% 抽样复核，记录 judge-human 一致率；一致率下降即冻结自评审。
6. **反自嗨**：禁止把「自评分数」当唯一优化目标；设计改进必须同时过客观门禁（pixel diff / 交互测试）与人工抽检。
7. **成本**：judge 调用计入预算；对高频场景用规则/小模型预筛，只有低置信样本才上调大模型。

## 7. 风险清单与缓解

| 风险 | 证据/机制 | 缓解（结合 ZeoKeel） |
| --- | --- | --- |
| **Reward hacking / specification gaming** | DGM/AlphaEvolve 都依赖 evaluator；DeepMind 长期记录 specification gaming 案例（[blog](https://deepmind.google/discover/blog/specification-gaming-the-flip-side-of-ai-ingenuity/)） | 多指标（测试 + 静态检查 + 像素 + 人类）；held-out 测试；禁止 Agent 修改测试/评测脚本；canary 样本 |
| **回归** | SWE-bench 用 PASS_TO_PASS 防回归；DGM 保留 archive 父代 | 每次变更跑全量 `make check` + 像素门禁；git worktree 隔离 + 回滚；技能/提示词版本化 |
| **成本爆炸** | GEPA 即使 35× 更省也仍需多次 rollout；Agentless $0.70/issue | 预算上限（token/时间/重试次数）写进 settings；scheduler 夜间批处理；小模型预筛 |
| **数据污染/泄漏** | SWE-bench+：32.67% 泄漏、94% issue 早于 cutoff | 本地私有 eval set、时间切分、去重；评测禁网；记录 eval set 版本 hash |
| **不可审计** | 自改代码/自改 prompt 若不留痕，无法归因 | 复用 `events`/`tool_calls`/`messages` + 追加「变更单」表；所有自改产生 diff 与审批记录；prompt/技能带版本号与来源 |
| **自评偏差** | MT-Bench biases、JudgeBench、self-preference | 异源 judge、blind、位置交换、canary、人类抽检 |

## 8. 映射到 ZeoKeel（核心交付）：可直接抄 / 需改造 / 不适用

| # | 外部模式 | ZeoKeel 现状与约束 | 判定 | 落地位置/改造点 | 理由 |
| --- | --- | --- | --- | --- | --- |
| 1 | 测试/编译门控自修复 | `internal/shell` 可跑 `go test/vet`、`scripts/check-nocgo.sh`；`tool_calls` 留痕；审批策略可拦危险命令 | **可直接抄** | 新增 repair loop（max 2–3 次）：执行失败→收集 stdout→propose patch→重跑 gate；gate 不过即回滚 | 单机即可完成，verifier 客观、成本低 |
| 2 | SWE-bench 式 `FAIL_TO_PASS`/`PASS_TO_PASS` 补丁验证 | 无 CI server；但有 Makefile、e2e 脚本、像素基线 | **需改造** | 本地 gate = build+vet+test+nocgo+像素断言；用 git worktree 做候选隔离；固定「不可改测试」清单 | 无服务端，但本地可复现；关键是把「验证」与「修复」解耦 |
| 3 | 最小 diff / 补丁审查 | `internal/tools` 有编辑工具与审批 | **可直接抄** | diff 白名单（只改允许路径）、行数预算、禁止改测试/评测脚本 | 防 reward hacking 的第一道闸 |
| 4 | GEPA 提示词优化 | `internal/prompt` 是确定性 section 组合（EN/ZH、tool gate）；provider 可调用；无标注集 | **需改造（仅离线）** | 开发期脚本：用本地 golden 集当 fitness，reflective mutation 生成候选 prompt，人工 review 后写入 registry；prompt 版本存 SQLite/文件 | Python GEPA 无法进单二进制；运行时自改 prompt 成本/污染/审计不可控 |
| 5 | OPRO / APE | 同上，接口简单 | **可直接抄（离线小规模）** | `scripts/` 下一次性优化脚本；固定 dev set，禁止用测试集做 fitness | 实现成本低；但需防过拟合评测集 |
| 6 | Reflexion 反思记忆 | `messages` / `transcript` / `events` 已持久化 | **可直接抄** | 新增 `reflections` 表（task signature、反思文本、来源 turn、验证结果）；失败后写入、下轮按任务类型召回 | 纯本地 SQLite 即可；注意只从失败轨迹蒸馏 |
| 7 | Voyager 技能库 | `internal/skills` 已是「目录 + SKILL.md frontmatter」，运行时装为 `skill` 工具 | **需改造** | 增加 learned skills 目录：provenance、eval 状态、启用开关、回归失败自动禁用；先做启发式检索（关键词/标签），FTS 需验证（PLAN.md R8） | 文件式技能已就绪；补「写入质量门 + 生命周期」即可，无需向量库 |
| 8 | AWM 工作流归纳 | 会话轨迹完整 | **需改造/可选** | 从成功轨迹归纳 workflow，落成 SKILL.md 模板或 automation 提示词 | 收益不确定，优先级低于 1/6/7 |
| 9 | AlphaEvolve / DGM 自改代码进化 | 单二进制、纯 Go；无内核沙箱（PLAN.md R5：权限是策略级）；`danger-full-access` 风险高 | **不适用（在线）/ 仅研发离线** | 仅研发期在独立 worktree + 容器内跑，预算+人类 merge；产品内不做自改二进制 | 安全边界不足、审计成本高；桌面产品无法承担失控风险 |
| 10 | LLM-as-judge 评审 | provider 可调用；无服务端；token 由用户付费 | **需改造** | 仅用于「定性/偏好」维度：blind 成对比较、rubric、位置交换、canary、人类抽检；不作为发布门禁 | judge 在客观正确性上接近随机（JudgeBench）且自偏好 |
| 11 | 设计/UI 自评审 | `baseline/` 基线截图 + `internal/pixeltest` + diff 图 | **可直接抄（客观部分）** | 像素/几何/对比度断言当硬门禁；judge 只做「主观偏好/文案/层级」辅助，人工终审 | 已有客观基线，比 LLM 主观评审可靠得多 |
| 12 | 定时自审计 | `internal/automation` cron（分钟/小时/日）持久化到 SQLite | **可直接抄** | 夜间 automation：跑 `make check` + 像素门禁 → 失败聚类 → 生成「修复提案」→ 等审批（不自动改） | 单机可跑，符合无服务端约束 |
| 13 | 进化 archive / 排行榜 | 无服务端、无集中评测 | **不适用** | 最多本地保留「尝试历史」表；不共享、不排名 | 无服务端无法做跨用户评测，也避免污染 |

## 8.5 关键参数与数据模型（可直接开工的默认值）

**Repair loop 状态机**：`detect(失败信号) → triage(错误分类) → propose(≤1 个 patch) → verify(全量 gate) → accept | rollback | give_up`。建议默认值：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `max_attempts` | 2–3 | 超过即停，避免「试到过测试」的 reward hacking |
| `allowed_paths` | 按任务声明 | 白名单外改动直接拒绝；测试/评测脚本永久只读 |
| `diff_budget` | ≤200 行 / ≤5 文件 | 大 diff 走人工；降低回归面 |
| `verify_timeout` | 10 min/次 | 防止卡死；超时视为失败 |
| `rollback` | git worktree 丢弃 | 每次尝试独立 worktree，不污染主树 |
| `approval` | 默认 `ask` | 自动修复仅对「编译/lint/纯测试失败」放开；涉及依赖/schema/危险命令必须人工批 |
| `budget` | token/日 + 次数/夜 | 写 `settings`，超限暂停 scheduler 的修复任务 |

**SQLite 增量表（v2 schema 建议，全部 append-only）**：

| 表 | 关键列 | 用途 |
| --- | --- | --- |
| `self_repairs` | id, session_id, trigger_event, attempt, diff_hash, gate_result, rollback, ts | 修复尝试与结果，供归因 |
| `reflections` | id, task_sig, text, source_turn, verifier, created_at | Reflexion 式失败反思 |
| `skills_registry` | name, path, provenance, status(enabled/disabled), last_eval, pass_rate | 技能库生命周期 |
| `eval_runs` | id, suite_hash, case_id, pass, cost_tokens, model_snapshot, ts | 离线 prompt 优化/回归的 fitness 来源 |
| `prompt_versions` | id, section, text_hash, diff, eval_run_id, approved_by, ts | 提示词版本与审批 |
| `judge_reviews` | id, artifact, rubric_version, judge_model, blind_pair, verdict, human_spot_check | 评审记录与校准数据 |

**Prompt 优化 eval set 组成**（≥30 例，按 60/20/20 切 dev/val/held-out）：工具选择正确率、`make check` 通过率、e2e 脚本通过率、平均 token 成本、危险命令误触发率。**held-out 下降即拒绝候选**；同一 eval set 不重复用于多轮优化（防过拟合），版本 hash 写入 `eval_runs.suite_hash`。

**Kill switch**：单个配置项 `self_evolution.enabled=false` 必须能一键停掉 repair/scheduler/prompt 自动写入；所有自动变更默认 `dry-run`，先出「变更提案」再审批。

> 证据边界：上述数字多来自 Python/benchmark 场景（SWE-bench、Minecraft、HumanEval），ZeoKeel 的 Go/桌面任务分布不同，**必须用本地 held-out 数据重新 A/B**，不能直接引用论文增益作为承诺。

## 9. 建议的最小可行闭环（MVP，按收益/风险排序）

1. **Phase 1（低风险，先做）**：验证器门控自修复 + 最小 diff + git worktree 回滚；scheduler 夜间自审计只「提案」不「落地」。产出：修复成功率、平均重试次数、回归率。
2. **Phase 2**：Reflexion 式失败反思表 + 技能库写入门控（从通过 verifier 的轨迹蒸馏，回归失败自动禁用）。产出：技能复用率、回归失败率。
3. **Phase 3**：离线 GEPA 式提示词优化（本地 golden 集，人工 review 后合并）；judge 只做设计/文案的 blind 成对评审 + 人类抽检。
4. **不做**：在线自改代码、运行时自改 system prompt、judge 单独门禁、自建排行榜。

## 10. task-2 覆盖对照

| task-2 要求 | 本报告位置 | 关键结论 |
| --- | --- | --- |
| 1) 验证器门控自修复 | §2 | 门禁客观、模型只 propose；无外部反馈的自纠错会退化 |
| 2) 提示词/程序优化 | §3 | GEPA 最优但依赖 metric；仅离线、需 held-out |
| 3) 轨迹记忆与技能库 | §4 | Reflexion/Voyager 有实证；写入必须过 verifier 门，按缓存管理 |
| 4) 进化搜索 | §5 | 离线可借鉴，在线不可落地（无沙箱/不可审计） |
| 5) 产品/设计自评审 | §6 | judge 只做偏好近似；blind 成对 + canary + 人工抽检 |
| 6) 风险清单 | §7 | reward hacking / 回归 / 成本 / 污染 / 审计，逐项有缓解 |
| 7) ZeoKeel 对照表 | §8、§8.5 | 6 项可抄、5 项需改造、2 项不适用；附默认参数与 schema |

## 11. 参考来源（按引用顺序）

1. Chen et al., *Teaching LLMs to Self-Debug*, [arXiv:2304.05128](https://arxiv.org/abs/2304.05128)
2. Shinn et al., *Reflexion*, [arXiv:2303.11366](https://arxiv.org/abs/2303.11366)
3. Madaan et al., *Self-Refine*, [arXiv:2303.17651](https://arxiv.org/abs/2303.17651)
4. Huang et al., *LLMs Cannot Self-Correct Reasoning Yet*, [arXiv:2310.01798](https://arxiv.org/abs/2310.01798)
5. Jimenez et al., *SWE-bench*, [arXiv:2310.06770](https://arxiv.org/abs/2310.06770) · [swebench.com](https://www.swebench.com)
6. Yang et al., *SWE-agent*, [arXiv:2405.15793](https://arxiv.org/abs/2405.15793)
7. Xia et al., *Agentless*, [arXiv:2407.01489](https://arxiv.org/abs/2407.01489)
8. Aleithan et al., *SWE-Bench+*, [arXiv:2410.06992](https://arxiv.org/abs/2410.06992)
9. Agrawal et al., *GEPA*, [arXiv:2507.19457](https://arxiv.org/abs/2507.19457) · [gepa-ai/gepa](https://github.com/gepa-ai/gepa)
10. Opsahl-Ong et al., *MIPROv2 / Optimizing Instructions and Demonstrations*, [arXiv:2406.11695](https://arxiv.org/abs/2406.11695) · [dspy.ai](https://dspy.ai/)
11. Yang et al., *OPRO*, [arXiv:2309.03409](https://arxiv.org/abs/2309.03409) · [google-deepmind/opro](https://github.com/google-deepmind/opro)
12. Zhou et al., *APE*, [arXiv:2211.01910](https://arxiv.org/abs/2211.01910)
13. Wang et al., *Voyager*, [arXiv:2305.16291](https://arxiv.org/abs/2305.16291) · [voyager.minedojo.org](https://voyager.minedojo.org/)
14. Zhao et al., *ExpeL*, [arXiv:2308.10144](https://arxiv.org/abs/2308.10144)
15. Wang et al., *Agent Workflow Memory*, [arXiv:2409.07429](https://arxiv.org/abs/2409.07429)
16. Novikov et al., *AlphaEvolve*, [arXiv:2506.13131](https://arxiv.org/abs/2506.13131)
17. Zhang et al., *Darwin Gödel Machine*, [arXiv:2505.22954](https://arxiv.org/abs/2505.22954) · [jennyzzt/dgm](https://github.com/jennyzzt/dgm)
18. codelion, *OpenEvolve*, [github.com/codelion/openevolve](https://github.com/codelion/openevolve)
19. Zheng et al., *MT-Bench / LLM-as-a-Judge*, [arXiv:2306.05685](https://arxiv.org/abs/2306.05685)
20. Tan et al., *JudgeBench*, [arXiv:2410.12784](https://arxiv.org/abs/2410.12784) · [ScalerLab/JudgeBench](https://github.com/ScalerLab/JudgeBench)
21. Panickssery et al., *LLM Evaluators Recognize and Favor Their Own Generations*, [arXiv:2404.13076](https://arxiv.org/abs/2404.13076)
22. McAleese et al., *LLM Critics Help Catch LLM Bugs (CriticGPT)*, [arXiv:2407.00215](https://arxiv.org/abs/2407.00215)
