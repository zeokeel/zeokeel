# 03 · 学术 SOTA：经验蒸馏为"可复用流程/技能"的实证证据

> 范围：LLM/agent 把一次任务的经验沉淀为后续可复用知识——自然语言笔记、结构化 workflow、可执行代码/技能库、提示优化、权重更新——**各自被证明了什么、增益多少、代价与失效边界**。
> 方法：只读论文原文（arXiv HTML/PDF、会议页），数字逐条取自摘要、正文、表格；标 `rel.` 为相对提升，`abs.` 为绝对百分点。证据分级：**A**=多数据集+多组消融、代码开源、结论较稳；**B**=单 lab 多 benchmark；**C**=单环境/单案例/未独立复现；**D**=概念/综述，无量化。
> 注意：这些"经验"几乎都是**同一任务族内**的累积；跨产品、跨用户、跨环境的迁移证据非常薄，倒数第二节单列"不可外推"清单。

## 0. 十一条结论（先看这些）

1. **五条路线都有可复现增益，但量级不可直接比较**：不同 benchmark、不同模型版本（gpt-4 / gpt-4o / claude-3.5）、相对 vs 绝对口径混用。多数"显著提升"在绝对点上只有 +3~+20 点，且 baseline 很弱。
2. **最稳的增益来源不是"载体形态"，而是"可靠成功信号 + 任务族重复"**。Reflexion/ExpeL/DC 在 ALFWorld/HotPotQA/数学题上 +11~+22 abs. 或翻倍；同一套 Reflexion 在 WebShop 上 4 轮内无提升、作者主动终止（探索型任务失效）。
3. **AWM 是最直接对应"从轨迹归纳可复用 workflow"的受控实证**：WebArena 35.5 vs 23.5（**+12.0 abs.，+51.1% rel.**），Mind2Web step SR 45.1 vs 36.2（**+8.9 abs.，+24.6% rel.**），每例仅归纳 ~7.4 个 workflow，但**单 lab、仅 web 导航、无独立复现**（B/C 级）。
4. **代码/技能库增益最大但最"环境绑定"**：Voyager 在 Minecraft 单游戏里 3.3× 物品、15.3× 科技树里程碑；ASI 在 WebArena 只比 AWM-text 高 **+4.1 abs.**（+11.3% rel.），其中"验证"贡献 +3.8 点，而"文本放 memory"比"程序放 memory"反而高 **2.6 点**；SkillWeaver 报 +31.8% rel. 但基数低。
5. **"验证后才入库"是可靠性分水岭**：ASI 只让 **15.6%** 的 turn 通过三重验证（AWM 有 31.4% 的时机直接加 skill）；Voyager 去掉 self-verification 物品数 **-73%**；SkillWeaver/TroVE 有测试或执行一致门控；**AWM/ExpeL/Reflexion/DC 都没有真正的入库验证**。
6. **失败的论文级证据齐全**：context collapse（18,282→122 tokens，acc 66.7→57.1，低于无记忆 baseline 63.7）；brevity bias；反思幻觉（ExpeL 把 reflection 加进 insight 抽取：29.0 vs 39.0）；错误固化/毒化（MemoryGraft 持久行为漂移）；过度泛化（AWM action F1 57.3<60.6；offline+online 不可加）。
7. **增量 delta 更新优于整体重写**：ACE 消融 56.9→70.3（**+13.4 abs. avg**），适配延迟 -82.3%、rollout -75.1% vs GEPA；机制是 bullet 级增删改 + 非 LLM 的确定性合并。
8. **另一个坏选项是"全量历史直接塞"**：DC 的 full-history 让 GPT-4o AIME-2024 从 20.0 掉到 **6.7**；ACE 的 collapse 与 DC 的污染都说明"不筛选的追加"和"整体重写"一样危险。
9. **提示优化（DSPy/GEPA）证据强，但优化对象是"任务级 prompt"，不是"用户经验"**：GEPA 6 任务平均 +6%（最高 +20%）超 GRPO，rollout 少 35×，比 MIPROv2 高 10%+；需要可计算 metric + 验证集，产物不可解释、跨分布易碎。
10. **权重（SEAL）是极端路线，不应作为产品默认**：SQuAD 无上下文 33.5→47.0、ARC few-shot 0→72.5，但需 RL 外循环 + 微调基础设施、**catastrophic forgetting**（顺序 self-edit 后旧任务持续退化）、不可解释、闭源 API 不可用。
11. **"格式之争"被高估**：AWM 的 code vs 结构化文本 workflow 差异不显著（task SR 4.8 vs 3.6，step SR 45.1 vs 45.4）；ASI 的格式效应 ≤±2.6 点，而验证效应 +3.8 点。决定效果的是**反馈质量 × 复用频率 × 检索/用法**，不是"存成什么"本身。

## 1. 论文证据索引

| 论文（链接） | 载体 | 基准/设置 | 关键数字（原文口径） | 级 |
|---|---|---|---|---|
| [Voyager](https://arxiv.org/abs/2305.16291) (2023；TMLR 2024) | 可执行代码技能库 + 自动课程 + 迭代提示 | Minecraft，GPT-4，160 次提示迭代 | 3.3× unique items、2.3× 距离、科技树 wooden **15.3×**/stone 8.5×/iron 6.4× 更快；唯一解锁 diamond；160 迭代 63 个新物品；skill library 检索 top-5（描述 embedding） | C |
| [Reflexion](https://arxiv.org/abs/2303.11366) (NeurIPS 2023) | 自然语言反思（episodic buffer，最多 3 条） | ALFWorld、HotPotQA、HumanEval/MBPP | HumanEval **91.0**（GPT-4 80.1）；ALFWorld **130/134**（+22 abs.）；HotPotQA pass@1 0.39→**0.51**（gpt-4）、0.30→0.55（davinci-003）；self-reflection 比纯 episodic memory **+8 abs.**；**MBPP 77.1 < GPT-4 单次 80.1** | B |
| [ExpeL](https://arxiv.org/abs/2308.10144) (AAAI 2024) | 自然语言 insight 列表 + top-k 成功轨迹 | HotpotQA、ALFWorld、WebShop、FEVER 迁移 | HotpotQA 28.0→**39.0**；ALFWorld 40.0→**59.0**；WebShop insights-only 37 / retrieval-only 38；HotpotQA→FEVER 63→**70**；insight 用 UPVOTE/DOWNVOTE 计票管理冲突 | B |
| [AWM](https://arxiv.org/abs/2409.07429)（[ICML 2025](https://icml.cc/virtual/2025/poster/45496)） | 结构化 workflow（NL 描述 + 步骤序列），离线/在线归纳 | WebArena（812 任务）、Mind2Web | WebArena **35.5 vs 23.5（+12.0 abs.，+51.1% rel.）**，少 2.0 步/例；Mind2Web step SR **45.1 vs 36.2**；online 跨域 step SR +14.0~16.9 abs.；~7.4 workflow/例，utility 0.94 | B |
| [ASI](https://arxiv.org/abs/2504.06821) (2025) | 可执行程序技能 + 执行验证后才入 action space | WebArena，claude-3.5-sonnet | vs vanilla **+23.5% rel.**（32.7→40.4）、vs AWM-text **+11.3% rel.**（36.3→40.4）、步数 -15.3%/-10.6%；仅 **15.6%** 的 turn 通过验证 | B |
| [SkillWeaver](https://arxiv.org/abs/2504.07079) (2025) | 自动发现/练习/蒸馏 API + 自动测试用例 | WebArena、真实网站 | WebArena **+31.8% rel.**、真实网站 **+39.8% rel.**；强 agent 的 API 给弱 agent 最高 **+54.3%**；160 轮探索（GPT-4o） | B |
| [TroVE](https://arxiv.org/abs/2401.12869) (ICML 2024) | 可验证函数工具箱（用/长/修剪） | 11 个 math/table/vision 数据集 | 更高准确率且工具箱小 **79–98%**；人工验证快 **31%**、准 **13%**；靠 inter-execution agreement 选解，无需标签 | B |
| [ACE](https://arxiv.org/abs/2510.04618) (2025-10，v3 2026-03；ICLR 2026) | 结构化 playbook（bullet + helpful/harmful 计数）+ delta patch | AppWorld、FiNER/Formula、DDXPlus/Text-to-SQL | AppWorld offline **59.4**（ReAct 42.4，**+17.0**）、online 59.5（+17.1）、无 GT 57.2（+14.8）；finance offline 81.9（+12.8）；延迟 -82.3%、rollout -75.1% vs GEPA；论文宣称 +10.6% agents / +8.6% finance | B |
| [Dynamic Cheatsheet](https://arxiv.org/abs/2504.07952) (2025) | 自策展文本 memory（策略/代码片段），top-3 检索 | AIME、Game of 24、方程配平、GPQA-D、MMLU-Pro | Claude AIME-2024 23.3→**50.0**、AIME-2025 6.7→36.7；GPT-4o Game of 24 10→**99**；方程配平 44.8/50→98–100；GPQA-D +9.1；MMLU-Pro Physics 74→82 | B |
| [MemoryBank](https://arxiv.org/abs/2305.10250) (AAAI 2024) | 长期对话记忆 + 遗忘曲线（R=e^(−t/S)） | SiliconFriend（模拟长对话，194 探测题） | S 初始 1、每次召回 +1 并重置 t；效果以定性/模拟为主，**无任务成功率级数字** | C/D |
| [DSPy](https://arxiv.org/abs/2310.03714) (ICLR 2024) | 程序化提示/管线优化（编译期） | 数学、多跳检索、问答、agent loop | GPT-3.5/llama2-13b 比 few-shot **+25%/+65%**；比专家示例 +5–46%/+16–40%；770M T5 可接近 GPT-3.5 人工提示链 | A |
| [GEPA](https://arxiv.org/abs/2507.19457) (ICLR 2026 Oral) | 反思式 prompt 进化（Pareto 搜索） | 6 任务：HotpotQA、AIME、IFBench、PUPA、HoVer | 平均超 GRPO **+6%**（最高 +20%）、rollout 少 **35×**；超 MIPROv2 **10%+**（AIME-2025 +12%）；总 +13% vs MIPROv2 +5.6% | A |
| [SEAL](https://arxiv.org/abs/2506.10943) (2025) | 自生成微调数据 + RL 学"如何改权重" | SQuAD 无上下文知识注入；ARC-AGI 子集 few-shot | SQuAD 33.5→**47.0**（不经 RL 的自生成 39.7；GPT-4.1 合成 46.3）；ARC few-shot 成功率 0→**72.5**（oracle 100）；顺序更新会遗忘旧任务 | C |
| [Generative Agents](https://arxiv.org/abs/2304.03442) (UIST 2023) | 自然语言 memory stream + reflection + retrieval | 25 agent 沙盒，访谈式评测 | 检索分 = α_recency·recency + α_importance·importance + α_relevance·relevance，α 全 1，recency 衰减 0.995；reflection 阈值 importance 和 150（≈2–3 次/天）；TrueSkill μ 全架构 **29.89** vs 无 reflection 26.88 vs 无 memory 21.21（d=8.16） | B |
| [CoALA](https://arxiv.org/abs/2309.02427) (TMLR 2024) | 综述/框架 | — | 把 memory 分为 **procedural / semantic / episodic**，动作空间含内部 memory 读写；后续记忆综述同构（[2505.00675](https://arxiv.org/abs/2505.00675) 分 parametric/contextual + 六操作） | D |
| [MemoryGraft](https://arxiv.org/abs/2512.16962) (2025-12) | 经验检索的对抗面 | MetaGPT DataInterpreter + GPT-4o | 注入"成功经验"→语义模仿→持久行为漂移；少量毒化记录即可在良性任务中占检索结果很大比例（未给统一比例） | C |
| [KEEPER](https://arxiv.org/abs/2610.41482) (2026-10) | 文本记忆可靠性分析 | 长程 agent | 定义 **self-consistency bias**：错误声明一旦进入 memory 会持续保留；claim 级分析记录"有纠正观察仍不改" | C/D |

## 2. 五条路线：增益量级、适用条件、失效边界

| 路线 | 代表 | 可复现增益（具体） | 论文验证过的适用条件 | 已知失效 |
|---|---|---|---|---|
| (a) 自然语言笔记/反思 | Reflexion、ExpeL、DC、GA、MemoryBank | ALFWorld **+22 abs.**、HotPotQA 0.39→**0.51**（gpt-4，+12 abs.）、HumanEval 80→**91**（但 MBPP 77.1<80.1）；DC 在数学上翻倍/接近满分；GA 的 reflection 对 believability 贡献 μ +3.0 | 任务族重复 + **可靠 binary/可验证反馈**（单测、exact match、执行结果）；同模型重试；memory 小（3 条 / top-3） | WebShop 这类需要探索多样性的任务失效；弱模型自举污染（DC：GPT-4o-mini 收益有限）；反思幻觉（ExpeL：加 reflection 反而 29<39） |
| (b) 结构化 workflow | AWM、ASI(text-skill 对照)、[SkillDisCo](https://aclanthology.org/2026.findings-emnlp.2260/) | WebArena **+12.0 abs.**、Mind2Web step **+8.9 abs.**；cross-template 33.2 vs 20.5；**7.4 workflow/例** | 同一网站/领域内子流程高度重复；有 canonical 示例或 LM evaluator；强模型 | 跨分布时 offline workflow 掉；workflow 会误导动作选择（action F1 57.3<60.6）；offline+online 混用**不可加**（44.5 落在两者之间） |
| (c) 可执行代码/技能库 | Voyager、ASI、SkillWeaver、TroVE | Voyager 3.3×/15.3×（单游戏）；ASI +4.1 abs. over AWM-text；SkillWeaver +31.8% rel.；TroVE 工具箱小 79–98% | 环境可执行、**能自动验证**（execution agreement / 单测 / reward model）、API 稳定、代码能力强的模型 | 环境改版→skill 不兼容（ASI 举例 sort_by_listings 在 Target 失效）；模型弱则学不出（Voyager 用 GPT-3.5 时物品数只有 GPT-4 的 1/5.7）；把流程包成 action 后看不到中间状态（AWM §5 订票/弹窗例子） |
| (d) 提示/程序优化 | DSPy、GEPA、MIPROv2 | DSPy +25%/+65%；GEPA +6% avg（up to 20%）超 GRPO、35× fewer rollouts、超 MIPROv2 10%+ | 有**任务级 metric** 和 held-out 验证集；批量离线优化；单一/少量任务族 | 不是"用户经验"，每次任务分布变化要重优化；产物不可解释；ACE 批评其 brevity bias 会丢领域细节（有争议，见 §4） |
| (e) 权重 | SEAL | SQuAD 33.5→47.0、ARC 0→72.5 | 白盒权重、窄领域、可反复 RL 训练、长期不换模型 | 顺序更新遗忘历史（论文自认）；RL+微调成本高；不可解释/不可审计；闭源 API 无解；实验规模小（SQuAD、ARC 子集） |

### 2.1 直接"格式对比"的三个受控结果（最有用）

- **AWM §4.2（同一批 workflow，code vs 文本化）**：element acc 50.6/51.2、step SR 45.1/45.4、task SR 4.8/3.6。结论是"both forms can bring effective augmentations"，**没有显著差异**；代码形式的优势在可执行、可组合，不在"天然更准"。
- **ASI Table 3（shopping 分站）**：`unverified+text, 只放 memory` 32.6 → `verified+program, 只放 memory` **36.4（验证 +3.8）** → `verified+text, 只放 memory` **39.0（文本 +2.6）** → `verified+program, 放 action space` **40.1（可执行 +1.1）**。
- 解释：**"验证"带来的提升 > "格式"带来的提升**；格式与用法强耦合——文本适合做 memory 参考，程序适合进 action space。产品上若没有沙箱执行与验证，强行"代码化"多半只拿到维护成本。

## 3. 复用前是否验证：谁做了"验证后才入库"

| 工作 | 验证什么 | 门控方式 | 通过率/证据 | 是否"验证后才入库" |
|---|---|---|---|---|
| Voyager | 程序是否完成任务 | 执行 + GPT-4 self-verification（critic） | 去掉 self-verification：发现物品数 **-73%**；4 轮失败换任务 | ✅（但验证器是 LLM，不是环境真值） |
| ASI | 技能程序是否真能解任务 | 重放 skill-prefix 轨迹 + 三维检查：correctness / skill usage / skill validity | 仅 **15.6%** turn 通过；验证单独贡献 **+3.8 abs.** | ✅ 最严格（执行级） |
| SkillWeaver | skill 提案能否成功 | reward model 判成功 → 合成 API → 自动生成测试用例 + 调试 | 160 轮探索；API 带 precondition/usage log | ✅（测试级） |
| TroVE | 函数是否可复用 | inter-execution agreement 选解 + 周期修剪低效用函数 | 工具箱小 79–98%；人工验证快 31%/准 13% | ✅（选择+修剪，非一次性门控） |
| AWM | 仅判断"这条轨迹是否成功" | 在线用 LM evaluator 打二值标签后才归纳 | 无 workflow 级验证；非成功轨迹会产生错误 workflow（论文自述） | ❌（source 验证 ≠ 产物验证） |
| Reflexion / ExpeL / DC | 无独立验证 | 靠模型自评/自反思/自策展 | DC 无标签；ExpeL 用 UPVOTE/DOWNVOTE 计票；Reflexion 依赖外部反馈信号 | ❌ |
| ACE | 无正确性门控 | helpful/harmful 计数 + 去重 + grow-and-refine | 无标签可用；金融无可靠反馈时 FiNER **-3.4** | ❌（但有"事后信用分配"） |
| SEAL | 下游任务是否变好 | RL reward = 更新后模型在 τ 上的表现 | 两轮 ReST-EM 后超 GPT-4.1 合成数据 | ✅（事后、任务级，非入库前） |

**结论**：真正做"验证后入库"的是 Voyager/ASI/SkillWeaver/TroVE——共同点是**环境可执行、验证成本低**。凡是只靠"模型说自己成功"的（AWM online、Reflexion、ExpeL、DC），都在论文里留下了错误固化的口子。

## 4. 失败模式：证据与缓解

| 失败模式 | 论文证据（数字） | 缓解（论文验证过/建议） |
|---|---|---|
| **context collapse**（反复整体重写，细节被压没） | ACE 案例：step 60 context 18,282 tokens、acc 66.7；下一步塌成 **122 tokens、57.1**，低于无适配 baseline **63.7**；DC 是被点名的实现 | bullet 化 + **delta patch** + 确定性合并去重；grow-and-refine；KV cache 使长上下文成本可摊薄（ACE：91.8% 输入 token 命中缓存、计费 -82.6%） |
| **brevity bias**（越写越短越泛） | ACE 引 Gao et al. 2025：提示优化反复产出近义泛化指令（如"写单测确保方法行为符合预期"），丢领域细节；ACE 称 GEPA 把"简短"当优点 | 明确把 context 当"playbook"而非 summary；保留领域 heuristic/工具用法/失败模式；用 helpful/harmful 计数保留有效长条目 |
| **自我总结失真/虚构** | GA：最常见错误是"检索不到相关记忆、给记忆编造细节（fabricated embellishments）、继承模型的正式腔"；ExpeL：把 reflection 加入 insight 抽取后 29.0 vs 39.0 | 反思只作候选、由外部信号验证；保留原始 observation 可回溯；限制 reflection 频率/长度 |
| **错误固化/经验毒化** | MemoryGraft：注入"成功经验"→ agent 语义模仿→跨 session 持久行为漂移；少量毒化记录即可占良性任务检索结果很大比例；KEEPER 的 self-consistency bias：错误声明进入 memory 后持续保留 | 写入端做来源/成功校验；把 experience 当不可信输入（与用户数据同等对待）；定期剪枝 + 冲突检测；敏感动作需人工确认 |
| **过度泛化/漂移** | AWM：workflow 会带偏动作（action F1 57.3<60.6）；offline+online 混用 44.5，低于单用（不可加）；Mind2Web workflow 覆盖率仅 0.40；ASI：跨站 skill 不兼容（下拉变侧栏） | 按 site/domain 分组维护 workflow；跨分布时优先 online 归纳；skill 带 precondition + 失效检测；用"是否 divergence"训练/提示 |
| **经验互相矛盾** | ExpeL：success/failure 轨迹都可能次优，故用 ADD/EDIT/UPVOTE/DOWNVOTE + 重要度计数（初始 2，归零删除）；ACE：bullet 级 helpful/harmful 计数 + 语义去重 | 冲突时投票/计分而非直接覆盖；保留反例；去重阈值在 50%/90% 时为 77.0/78.6（70% 时 73.9，整体不敏感） |
| **自评偏差 / reward-hack 式自利** | DC/Reflexion 用同一模型判定"这次是否成功"再写入；ACE 明确"无可靠反馈时 context 会被虚假信号污染"（无标签 FiNER 67.3，-3.4）；MemoryGraft 说明"看起来成功的经验"最容易被模仿 | 用外部验证（单测/执行/程序化 evaluator）替代模型自评；高风险动作人审；写入前要求可重放证据 |
| **弱模型自举失败** | DC：GPT-4o-mini 生成正确解太少，memory 被有缺陷策略填满，收益有限；ACE：无可靠反馈时 FiNER 67.3（-3.4）；Voyager：GPT-3.5 比 GPT-4 少 5.7× unique items | 只让强模型写 memory / 或让人确认；低置信度不写入；用执行结果而非模型自评 |
| **成本/上下文膨胀** | ACE 评估期输入 token +117.4%（vs GEPA）、输出 +7.6%，但靠缓存摊销；DC 单 query token 370→1831（DC-Cu）；GA 模拟 25 agent 两天花费数千美元 | delta 更新 + 去重 + 剪枝；检索 top-k 小（DC k=3、Voyager k=5）；缓存/分层上下文 |

## 5. 增量 delta vs 整体重写：证据表

| 做法 | 证据 | 结果 |
|---|---|---|
| **整体重写**（每次让 LLM 重写全部 context） | ACE 案例（DC 式） | 18,282→122 tokens；acc 66.7→57.1，低于 baseline；ACE 归因于"monolithic rewriting" |
| **delta patch**（只追加/局部改 bullet） | ACE Table 18（AppWorld offline，test-normal） | 无增量：67.3/46.4/56.9（TGC/SGC/avg）；有增量：**76.2/64.3/70.3 → avg +13.4** |
| **delta 的成本收益** | ACE Table 4 | 适配延迟 53,898s→9,517s（**-82.3%**）、rollout 1,434→357（-75.1%）vs GEPA；FiNER online 延迟 -91.5%、token 成本 -83.6% vs DC |
| **全量历史追加** | DC Table 2（AIME 2024） | GPT-4o full-history 20.0→**6.7**（比 baseline 更差）；Claude 26.7 vs DC 50.0 |
| **多来源经验合并** | AWM Table 11 | offline+online 44.5 落在 offline 45.1 与 online 43.6 之间；论文明确"不兼容、非加性" |
| **局部重写（非全量）** | GEPA / DSPy | 每轮只改一个 module 的 prompt，且**只在 validation minibatch 变好才接受**；这是 delta + 验证门控的组合 |

**结论**：证据支持"**条目级增量 + 确定性去重/剪枝 + 变更前验证**"，不支持"周期性让模型重写整份记忆"，也不支持"把全部历史/全部经验无差别追加"。

## 6. 检索与记忆结构：有数字的部分

- **打分**：GA 的 `score = α_recency·recency + α_importance·importance + α_relevance·relevance`（α 全 1，min-max 归一，recency 衰减 0.995）。消融显示去掉 reflection/planning/memory 逐级下降（μ 29.89→26.88→25.64→21.21）。
- **检索规模**：DC 检索 top-3（试过 5/7 提升不显著）；Voyager top-5 技能；ExpeL top-k 成功轨迹 + **全量 insight**。AWM 不做显式检索，而是按网站分组整体注入 workflow。
- **写入/遗忘**：MemoryBank 用 `R = e^(−t/S)`，S 初始 1、每次召回 +1 并重置 t；属"对话陪伴"场景，**不能当作任务成功率证据**。
- **无检索的代价**：AWM 按网站分组（避免全局干扰）；AWM 论文的 workflow 函数重叠仅 0.08（WebArena）/0.20（Mind2Web），说明"分组 + 去重"比"越大越好"更重要。
- **可靠性缺口**：检索层没有"这条经验是否可信/是否过期"的通用机制；ACE 用 helpful/harmful 计数是少数显式做法，MemoryGraft 证明该层可被投毒。

## 7. 弱证据与不可外推清单（必须写在决策旁边）

1. **Voyager**：单游戏、单模型（GPT-4-0314）、baseline 弱、self-verification 是 LLM 而非环境真值；"3.3×/15.3×"不能外推到开放桌面任务。
2. **AWM**：单 lab、2 个 web benchmark、gpt-4-0613 温度 0；相对提升来自弱 baseline（BrowserGym 23.5%）；跨分布时 offline 明显变弱；无独立复现。
3. **ASI**：+11.3% rel. 只有 4.1 abs.（36.3→40.4，同用 claude-3.5）；AWM 换 GPT-4o 是 35.5——**模型差异与方法差异同量级，存在混杂**；结论"程序更好"被作者自己的 ablation 修正为"验证更好、文本适合 memory"。
4. **SkillWeaver / TroVE**：SkillWeaver 相对百分比基数低、真实网站样本小；TroVE 数据集以程序化任务为主，任务窄；"API 可迁移"仅在同类网站/任务验证。
5. **ACE**：gain 最大的 Formula 分项（85.5 vs 67.5）不代表整体；FiNER 只 +7.6；无标签在线金融任务**会掉点**；context collapse 是单一案例研究（ICLR 2026 版本仍保留该诊断）；"brevity bias"主要是对 GEPA 的批评，GEPA 自身数字不支持"越短越差"，两者是**任务类型差异**（知识密集/agent vs 纯推理）。
6. **DC**：AIME 题量小、基线低（6.7%），翻倍不等于可迁移；同模型自评策展，模型换了结论可能反转（GPT-4o 在 MMLU-Pro 反而略降）。
7. **ExpeL/Reflexion**：4-fold/100 题级；反馈信号是前提条件而非普遍成立；WebShop 反例说明探索型任务不适用。
8. **SEAL**：SQuAD/ARC 子集，规模小；RL 外循环成本高；遗忘未解决；闭源模型不可行。
9. **MemoryBank / Generative Agents**：评测是"像不像人/访谈可信度"，不是任务完成率；不要把 μ 差值当作生产力增益。
10. **MemoryGraft / KEEPER / SkillDisCo**：单篇/很新（2025-12~2026-10），只有机制或定性结论；引用时只作"风险存在"的证据，不作量级依据。
11. **未找到**：任何一篇论文在同一 benchmark、同一模型上**公平对比五种载体**；因此"哪种载体整体最优"没有直接证据，只有分场景证据。

## 8. 判据表（路线 → 证据强度 → 典型增益 → 成本 → 适用条件）

| 路线 | 证据强度 | 典型增益（具体口径） | 成本 | 适用条件（论文验证过的） | 不适用/失效条件 |
|---|---|---|---|---|---|
| (a) 自然语言笔记（insight/反思/cheatsheet） | **B**（多 lab、多 benchmark；但依赖反馈信号，独立复现少） | ALFWorld +19~22 abs.；HotPotQA +11~20；HumanEval 80→91；数学类翻倍/接近满分 | 低：token + 每次任务一次 curation 调用；memory 需检索与剪枝 | 任务族重复、有 binary/可验证反馈、同一模型重试；memory 小而精（3 条~top-3） | 无可靠反馈（WebShop 失败）、探索型任务、弱模型自举、无剪枝导致上下文膨胀 |
| (b) 结构化 workflow（NL 描述+步骤，可读不可执行） | **B-**（AWM 单 lab；2 个 web benchmark，无复现） | WebArena +12.0 abs.（+51.1% rel.）；Mind2Web step SR +8.9 abs.（+24.6% rel.）；~7.4 workflow/例 | 中：归纳调用 + evaluator + 按站点维护；memory 注入占上下文 | 子流程重复、同站点/领域、有成功示例或可靠 evaluator、强模型 | 分布漂移（跨站/跨域 offline 掉）、workflow 误导动作（action F1 降）、offline+online 混用不可加 |
| (c) 可执行代码/技能库 | **B-**（Voyager 单环境；ASI/SkillWeaver web；TroVE 多数据集但任务窄） | Voyager 3.3×/15.3×；ASI +4.1 abs.（shopping 消融：验证 +3.8、文本 +2.6）；SkillWeaver +31.8% rel.；TroVE 工具箱 -79~98% | 高：沙箱/执行验证、单测生成、action space 集成、环境变更维护 | 环境可执行且验证便宜、API 稳定、代码能力强的模型、高频重复任务 | 环境改版/跨站（skill 不兼容）、弱模型（学不出）、动态页面中间状态不可见、无验证基础设施则退化为高风险文本 |
| (d) 提示/程序优化（DSPy/GEPA/MIPRO） | **A**（多任务、代码开源、可复现），但**不是"用户经验"** | GEPA +6% avg（up to +20%）超 GRPO、35× fewer rollouts、超 MIPROv2 10%+；DSPy +25%/+65% vs few-shot | 中高：需 metric、验证集、成百上千 rollout；每次任务分布变化需重跑 | 有明确可计算指标 + 验证集；批量/离线；任务族固定 | 无 metric/无验证集；用户个性化经验；分布频繁变化；需要可解释产物 |
| (e) 权重（SEAL 式） | **C**（单 lab、小规模、两领域） | SQuAD 33.5→47.0；ARC few-shot 0→72.5 | 极高：RL 外循环 + 微调基础设施 + 每次适配算力；遗忘需额外机制 | 白盒模型、窄领域、长期不换模型、可接受不可解释 | 闭源 API、多任务/持续变化、需要审计/回滚、成本敏感 |

**给决策的一句话**：学术证据支持"**默认 (a) 文本 playbook + 验证门控 + delta 更新 + 小 top-k 检索**"；"(b) 结构化 workflow"只在**高频、同域、可评估**的子任务上值得做，并且必须按域隔离、允许失效降级；"(c) 代码技能库"是 (b) 的加强版，**验证基础设施是前置条件而不是可选项**；"(d)"是离线任务级优化工具，不是运行时经验载体；"(e)"不建议进入产品路线。
