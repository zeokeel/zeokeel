# 01 · 产品级证据：主流 Agent 产品的"经验沉淀"原语

> 决策问题：一次对话结束后总结出的经验，应该做成 **memory / 可执行流程(workflow·skill) / 本机 wiki** 中的哪一种？
> 抓取时间 **2026-11-15**，只用官方 docs / 官方博客；每节末给"对决策意味着什么"。
> **口径警告（影响证据解读）**：① `docs.windsurf.com` 已 301 到 `docs.devin.ai`——Windsurf 并入 Devin Desktop，Cascade 成为 legacy（[cascade.md](https://docs.devin.ai/desktop/cascade.md)）；② Claude Code 文档已迁到 `code.claude.com`。Windsurf 证据取自仍然保留的 Windsurf 插件文档 + 原 Cascade 文档（现存于 devin.ai）；"迁移本身"就是一条重要证据（流程类原语被 skills 吞并）。

## 0. 一句话结论

八个产品没有把"经验"当成一个东西，而是**按"何时需要它"拆成四种检索时序 + 一种写入者身份**：**每次都要 → 常驻规则；可能要用 → description 索引 + 按需加载的 skill；确定要人发起 → slash 工作流/prompt file；不确定但可信来源是 agent 自己 → 自动 memory**。2026 年的方向是：**skill 成为流程的唯一收敛格式，memory 保持个人化，wiki 形态被 skill 吸收**（Devin Knowledge→Skills、Windsurf Workflow→Skill、Cursor `/migrate-to-skills`）。

## 1. Windsurf / Cascade：最直接的"三选一"现场

| 原语 | 内容形状与粒度 | 触发/检索 | 谁写 | 冲突/更新 | 官方承认的坑 |
|---|---|---|---|---|---|
| **Memories** | 一条条短事实/偏好，按 **workspace 绑定** | "Cascade 认为相关时检索"；跨 workspace 不共享 | **Cascade 自动生成**；也可 prompt "create a memory of ..."；人可在 Customizations 里编辑 | 只能人工编辑/删除；无去重、无 TTL、无合并机制 | workspace 边界导致知识不迁移；自动生成的时机与粒度不可控；现行文档**未见**按项目开关自动生成的设置；官方 tip：创建/使用 auto memory **不消耗 credits**（[memories.md](https://docs.devin.ai/windsurf/plugins/cascade/memories.md)） |
| **Rules** | 一条条"always/条件生效"的指令；`global_rules.md` + `.windsurf/rules/*.md` | 四种激活：`always_on` / `glob` / `model_decision`（按 description 由模型决定）/ `manual`（[trigger 表](https://docs.devin.ai/desktop/devin-local/customizations.md)）；Windsurf 插件文档表述为"绑定 glob 或自然语言描述" | **人写**（面板 + Global/Workspace 按钮） | 全部注入合并；enterprise **system rules 是追加合入，官方明说不覆盖用户规则**（只读、用户不可删） | 每个 rule 文件 **12000 字符上限**；规则 token 每条消息都付费；官方明确"不要写 generic rules"（[best practices](https://docs.devin.ai/windsurf/plugins/cascade/memories.md)） |
| **Workflows** | `.windsurf/workflows/*.md`，标题+描述+有序步骤；**trajectory 级**（"Rules 是 prompt 级，Workflows 是 trajectory 级"） | **只手动**：`/name` 触发；可 workflow 套 workflow | 人写，或让 Cascade 生成（"Generate a Workflow with Cascade"） | 按名字覆盖：**System > Workspace > Global > Built-in**；同名 system workflow 静默压过项目 workflow | 12000 字符上限；**successor 文档写明 "Workflows are manual-only — Cascade will never invoke a workflow automatically"**，要自动触发请改用 Skill（[workflows.md](https://docs.devin.ai/windsurf/plugins/cascade/workflows.md)、[cascade.md](https://docs.devin.ai/desktop/cascade.md)） |

**迁移后的落点**：Devin Local 只保留 rules+skills，`trigger` 仍是那四值，`.windsurf/*` 作为 legacy 被默认导入、`.devin/global_rules.md` 优先于 `.windsurf/global_rules.md`；skills 用 `name`+`description` 做 progressive disclosure（[customizations.md](https://docs.devin.ai/desktop/devin-local/customizations.md)）。
**对决策意味着什么**：这是最直接把"记忆/规则/流程"并列为三件套的样本，其结论很尖锐——**流程必须与"我是谁写的"解耦，改由 skill 描述触发或人 slash；auto-memory 只解决 workspace 内偏好，不解决团队经验复用**。

## 2. Claude Code + Agent Skills：把"检索时序"做成一等公民

| 原语 | 形状/粒度 | 触发 | 谁写 | 冲突/更新 |
|---|---|---|---|---|
| **CLAUDE.md**（+`AGENTS.md`） | 项目/用户/组织/本地四层；按路径**拼接**注入（不是后者覆盖前者） | 每次会话自动；子目录文件在读写该目录文件时才加载 | 人写；`/init` 可生成草稿；agent 不应擅自改 | 多文件**叠加不覆盖**，冲突靠模型自行取舍；子目录 deeper 后读；`@path` import 最深 4 跳；>4MiB 整文件跳过；块级 HTML 注释注入前剥离（[memory.md](https://code.claude.com/docs/en/memory.md)） |
| **Auto memory** | Claude 自写的 markdown：`MEMORY.md` 索引 + 每主题一文件；4 类 `user/feedback/project/reference` | 索引每次加载（前 **200 行或 25KB**）；主题文件按需 Read | **agent 写**，人可 `/memory` 编辑；写时记录 `modified` 时间戳 | 目录按 git repo 聚合（worktree 共享）；超出限额时系统提醒"合并/删除旧条目"；仅本机、不跨机 | 
| **`.claude/rules/`** | 一文件一主题；`paths:` frontmatter 按 glob 条件加载 | 命中文件被 Read/Write/Edit 时加载；无 `paths` 则启动即加载 | 人写 | user 级先于 project 级；两套**都不覆盖**，冲突可能任选；symlink 到项目外要审批 |
| **Skills** | `SKILL.md` 目录：frontmatter `name`+`description`，正文 + `scripts/`、`references/` | **三层渐进披露**：① name+description 常驻 ② 命中后读正文 ③ 正文指向的脚本/参考再按需打开 | 人写；`/run-skill-generator`、`/verify` 会把跑通的配方**回写**成项目 skill；`/create-skill` 类 | 同名按 managed>user>project 覆盖；plugin 内命名空间；`disable-model-invocation:true` 变成纯人工触发；`context:fork` 进隔离 subagent |
| **Plugins / subagents / hooks** | 打包分发层 / 隔离执行 / 生命周期强制 | 手动或事件 | 人写为主 | hooks 是保证"每次都要执行"的手段（权限规则同理） |

**官方自己说的坑**：CLAUDE.md 是"**context, not enforced configuration**"，要保证执行就写 hook；文件 >200 行会降低遵循度；两条指令冲突时"Claude may pick one arbitrarily"；skill 的 `description` 模糊/重叠时会加载错或漏（[features-overview](https://code.claude.com/docs/en/features-overview.md)）；skill 列表**每轮都在 context 里**，有成本，官方给了 `/skill-doctor` 看每个 skill 的 context 成本与使用次数；`/doctor prompt-audit` 专门查"过时/自相矛盾的指令"；第三方 skill 可执行代码，官方要求审阅（[blog](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills)）。
**对决策意味着什么**：官方给了本报告里**最完整的一份按"触发时机"排序的决策表**：重复犯同一个错→写 CLAUDE.md；总在粘贴同一段 prompt→存成 user-invocable skill；同一套多步流程贴第三次→写成 skill；"每次都必须发生"→不要写经验，写 hook。这与"经验→memory / 流程→skill / 资料→按需 skill"的三分完全同构。

## 3. Cursor Rules：四种激活方式 = 四种检索代价

| 规则类型（UI） | 底层 frontmatter | 何时进 context | 语义 |
|---|---|---|---|
| Always Apply | `alwaysApply: true` | 每个会话 | 常驻规则，最高确定性/最高成本 |
| Apply to Specific Files | `alwaysApply:false` + `globs` | 匹配文件在 context 中时自动 attach | 路径条件加载 |
| Apply Intelligently（即 Agent Requested） | `alwaysApply:false` + `description` | **Agent 读 description 自己决定** | 模型检索 |
| Apply Manually | 三者皆无 | 仅 `@rule` 提及 | 人工触发，零常驻成本 |

补充事实：只有 `.mdc` 才被识别，普通 `.md` 被忽略；规则里的 `@file` **不内联**，只是指路，必须常驻的内容要写进 body；优先级 **Team > Project > User**，全部合并、冲突时前者优先；`AGENTS.md` 支持根目录+子目录嵌套，更具体者优先；Team Rule 可"Enforce"到用户无法关闭。Cursor 现在也支持 **Agent Skills**（`.agents/skills`、`.cursor/skills`，且兼容读 `.claude/skills` 与 `.codex/skills`），模型按描述自行调用，`/` 手动调用；内置 `/create-rule`、`/create-skill`，以及 **`/migrate-to-skills`：把 eligible 的 dynamic rules 和 slash commands 转成 skills**（[rules.md](https://cursor.com/docs/rules.md)、[skills](https://cursor.com/docs/skills)）。
**About Memories**：当前 docs 的 Customize 章节只有 Rules / Skills / Subagents / Hooks / Plugins，**没有产品级 Memories 原语**；memory 只出现在 Agent SDK（append-only `memory/journal.jsonl`，模型没有 recall 工具、靠指令去读目录；官方 caveat："所有 session/用户共享一个 journal"、"记录可能丢或迟到"）（[llms.txt](https://cursor.com/docs/llms.txt)、[sdk memory](https://cursor.com/docs/agent-sdk/reference/memory.md)）。
**官方坑**：规则 <500 行；"不要抄整份 style guide，用 linter"；"只在 Agent 反复犯同一错误时加规则"；"AI guidance 不能当安全控制"；Team Rules 虽可强制但不保证遵守。
**对决策意味着什么**：**"自动"和"可检索"不是一回事**。Cursor 的四种激活正是把"经验被用到的概率"显式标价：Always 花钱买确定性，globs 买场景，description 买模型判断，manual 买零成本。wiki 若不能给每条经验一个 description/globs，就永远只能 manual。

## 4. Cline：本机 wiki 路线的代表与它的账

- **Rules**：`.clinerules/`、`.cline/rules/`（+ 自动识别 `.cursorrules`、`.windsurfrules`、`AGENTS.md`、`~/.agents/AGENTS.md`）；workspace 规则与 global 合并、workspace 优先；每条可单独 toggle。**条件规则**用 `paths:` frontmatter，按"当前上下文"（消息里提到的路径、打开的 tab、可见/已编辑/将编辑文件）匹配，命中时弹 "Conditional rules applied"；无 frontmatter = always on；`paths: []` = 永不生效；**YAML 解析失败时 fail-open，规则以原文生效**（[cline-rules.md](https://docs.cline.bot/customization/cline-rules.md)）。
- **Memory Bank**（官方 best-practice 文档，本质是"用规则教 agent 维护一套 md wiki"）：6 个文件 `projectbrief/productContext/activeContext/systemPatterns/techContext/progress`；命令 "initialize / update memory bank"、"follow your custom instructions"；文档要求 **"Must read ALL memory bank files at the start of EVERY task"**，`activeContext.md` 每次会话后更新（[memory-bank.md](https://docs.cline.bot/best-practices/memory-bank.md)）。
- **官方承认的坑**：规则消耗 context token，忌粘贴整份 style guide；Memory Bank 的每次任务全量读取 = 固定 context 税，且正确性完全依赖 agent 每次记得写、写对；`activeContext.md` 高频更新会**持续膨胀**；更新时机（里程碑/方向变化/上下文将满）是人工判断；与 auto-compact 的关系需要用户自己编排。
**对决策意味着什么**：wiki 路线的真实成本不是"写"而是"**每次都要读，且必须保持最新**"。它适合大块、稳定、需要人浏览的项目档案；把"一次会话的经验"直接扔进 wiki 会让固定 context 税线性上涨而检索收益不确定。

## 5. Devin：memory 与 skill 的分工被写成了官方表格

| 原语 | 形状 | 触发/检索 | 谁写 | 冲突/老化 |
|---|---|---|---|---|
| **Memory**（+Dreaming） | 一个 **git 仓库形态的 memory drive**：`MEMORY.md`（通用偏好）+ 按主题分的 md；每条是**一行 bullet，带 `[source: <session-id>; added: <date>]`** | 每次 session 拿到 `MEMORY.md`；其他 note **只在需要时搜索/读取** | **Devin 自动写**（纠正/偏好/可复用教训）；人在 app 里只读，改要"跟 Devin 说" | 提交后与其他 session 合并；**stale write 被拒并重试，冲突被 resolve 而非覆盖**；**Dreaming 每天整理一次**：合并重复、删瞬态/过期条目、补教训、重排（保留来源与显式偏好） |
| **Memory vs Skills（官方原表）** | Memory = 与你共事时学到的；Skill = 可重复的过程 | Memory 自动召回；Skill 由 name+description 或 `@skills:name` 调用 | Memory: Devin；Skill: 你/团队 | Memory **个人**；Skill 可 repo/个人/org/enterprise |
| **Skills** | `SKILL.md`；推荐 `.agents/skills/<name>/`（兼容扫描 `.devin/.github/.claude/.cognition/.windsurf/.cursor/.codex/.codeium/.kiro` 共 10 处）；`allowed-tools`、`triggers:["user","model"]`、`argument-hint`、`permissions` | session 开始只看到 name+description；被调用后正文进入 context **并保留到 session 结束**；也可 `@skills:` 带参 | 人/团队；**Devin 主动建议** "create or update a skill"（含草案 + Create PR 按钮）；可从过往 session 生成 | 同会话可叠加多个 skill；skill 内容留在 context，多了会累积 |
| **Knowledge（存量）** | 一条条 note：必填 **Trigger Description**（用于召回）+ 几段内容；`!macro` 快捷引用；可 pin 到 no repo / 特定 repo（总是用）/ 所有 repo（总是用） | **自动按 trigger 召回**；pin 则绕开判断 | 人（org/enterprise 级）；**Knowledge Suggestions**：Devin 在对话中主动建议，人编辑/接受/驳回，也能建议更新既有条目 | 分 folder、可整组 enable/disable、可 promote 到 enterprise；**已 deprecated，自动迁移为 Skills**（trigger→description、content 逐字、level→plugin） |
| **Playbooks（存量）** | 附在 session 上的流程；macro | `!macro` 或选择器 | 人写 | **迁移到 Skills**；新账号自 2026-10-24 起不再有 Playbooks |

**官方自己说的坑**：Knowledge 必须"写得具体、保持最新"，"Devin 会读整条内容"；"检索是 relevant 时，不是开头全量"；memory 只对个人生效、automation 起的 session 不读不写；Dreaming 也承认需要"删 stale"。最关键的官宣是 **"Use skills for all new work"**，以及 Knowledge→Skills 的迁移表（[memory.md](https://docs.devin.ai/product-guides/memory.md)、[skills.md](https://docs.devin.ai/product-guides/skills.md)、[knowledge.md](https://docs.devin.ai/product-guides/knowledge.md)、[playbooks](https://docs.devin.ai/product-guides/using-playbooks.md)）。
**对决策意味着什么**：Devin 是**"memory 独立、workflow=skill、wiki=被废弃"**的完整判决：记忆坚持个人化 + 自动老化；流程与知识都收敛到 SKILL.md；知识库型 note 因为"检索靠 trigger 描述、维护靠人"被合并掉。ZeoKeel 若要做"本机 wiki"，必须回答 Devin 已经回答过的问题——**它比 skill 多出来的价值是什么**。

## 6. Codex + AGENTS.md：事实标准的分层与截断

- **发现链**：global `~/.codex/AGENTS.override.md` 优先，否则 `AGENTS.md`（每层只用第一个非空文件）→ 从 project root 逐目录下到 cwd，每目录按 `AGENTS.override.md` > `AGENTS.md` > `project_doc_fallback_filenames` 取**至多一个** → **root→cwd 顺序拼接**，更近的文件因靠后而"覆盖"。
- **硬上限**：合并后达到 `project_doc_max_bytes`（默认 **32 KiB**）就停止追加；空文件跳过；每次 run 重建指令链，无缓存可清。官方 troubleshooting："instructions truncated → 调大上限或拆到嵌套目录"（[guides/agents-md](https://developers.openai.com/codex/guides/agents-md)；官网 403，正文经[官方文档镜像](https://cdn.jsdelivr.net/gh/crasuna/openai-dev-docs-cn-mirror@main/sources/en/codex/guides/agents-md.md)取得）。
- **标准侧**：[agents.md](https://agents.md/) 已被 Copilot、Cursor、Cline、Windsurf/Devin、Claude Code 直接读取——**"给 agent 的项目指令"已经有跨工具事实标准**。
**对决策意味着什么**：AGENTS.md 证明"人写的、随仓库走的常驻经验"是刚需，但它**只解决"每次都要"的那一类**，且被 32KiB 截断逼着做索引化。它是 wiki 的天然落点，却不是"一次性经验"的落点（没有写入/去重/老化机制）。

## 7. Copilot：instructions / prompt files / memory 三分

- **Instructions（自动）**：`.github/copilot-instructions.md`（全仓）、`.github/instructions/**/NAME.instructions.md` + `applyTo` glob（路径条件，可 `excludeAgent`）、`AGENTS.md`（就近生效）/`CLAUDE.md`/`GEMINI.md`（agent 指令）；个人/仓库/组织三级。GitHub.com 优先级：**Personal > 路径级 > 仓库级 > Agent 指令 > Organization**；其他客户端多为"合并、无统一优先级，请自己避免冲突"（[custom-instructions](https://docs.github.com/en/copilot/concepts/prompting-and-context/custom-instructions)）。
- **Prompt files（手动）**：任务级提示，**由用户决定何时用**——与 instructions 的核心区别就是"检索由谁发起"（[VS Code prompt files](https://code.visualstudio.com/docs/agent-customization/prompt-files)）。
- **Agent Skills**：VS Code/GitHub 的 customization 已并列列出 [Agent Skills](https://code.visualstudio.com/docs/agent-customization/agent-skills)（SKILL.md 体系）与 Instructions、Prompt Files、Custom Agents。
- **Copilot Memory（自动）**：两类——**repository-level facts**（同仓库协作者共享，带指向代码的 citation）与**user-level preferences**（跨仓库跟随个人）；Copilot 自己判断该记哪类、歧义时先问；**upvote/downvote 强化，默认 90 天过期（repo facts 可配 7–180 天），仅"被检索"不续期**；密钥/注入 token 会拒存；不能直接编辑，只能通过"跟 Copilot 说"来影响或删除；官方明确 **memory 不是规则**，要保证执行请用 custom instructions（[copilot-memory](https://docs.github.com/en/copilot/concepts/prompting-and-context/copilot-memory)）。
**官方坑**：instructions 每条消息都发送、越长的指令越可能失效；AI 非确定性，"可能不总是遵循"；Memory 的失效靠模型判断 + TTL，官方承认"不能当作永远遵循的规则"。
**对决策意味着什么**：Copilot 把"**共享的事实**（repo facts）"与"**个人的偏好**（user prefs）"拆成两类记忆，这是本报告里唯一显式做"记忆分权"的产品；它给出的经验是：**记忆一旦共享就会遇到 citation/权限/审计问题，成本远高于个人记忆**。

## 8. Amp（次要）：skill 的 Git 仓储化

Skills 存 `.agents/skills/`（项目）/ `~/.config/agents/skills/`（机器）/ 个人与 workspace 各一个 **git skills 仓库**；模型只看到 name+description，调用后才加载正文与脚本；同名按 11 级来源顺序取第一个（本地 > 仓库 > 官方）；skill 可带 `mcp.json`/`mcpServers`，未加载前隐藏其工具。**官方坑**：每个 skills 仓库**上限 200 个 skill**，超出按目录名字母序静默截断；单文件 10MiB、单 skill 25MiB、仓库 25MiB（[skills](https://ampcode.com/docs/markdown/customize/skills)、[AGENTS.md](https://ampcode.com/docs/customize/agents-md)）。

## 9. 六问总表（每个产品一行）

| 产品 | ①原语 | ②形状/粒度 | ③触发/检索 | ④谁写 | ⑤冲突/更新 | ⑥官方坑 |
|---|---|---|---|---|---|---|
| Windsurf | Memories / Rules / Workflows | 事实条 / 规则条 / 步骤 md | 自动召回 / 4 种激活 / 仅 slash | agent 写 memory；人写 rules·workflow（agent 可生成） | workflow 同名覆盖 System>WS>Global；memory 仅手动编辑 | workspace 隔离、12000 字上限、rule 每条消息计费 |
| Claude Code | CLAUDE.md / rules / Skills / auto memory / plugins | 分层 md / 带 `paths` 条 / SKILL.md 目录 / 索引+主题文件 | 每次注入 / 文件命中 / description 三层 / MEMORY.md 索引 | 人写 + agent 写 memory，可回写 skill | 文件叠加不覆盖；同名 skill 覆盖 | 非强制、>200 行降遵循、description 模糊会错载 |
| Cursor | Rules(4 种) / AGENTS.md / Skills | `.mdc` 规则 / md / SKILL.md 目录 | always / globs / description / `@`；skill 由模型或 `/` | 人写；agent 可 `/create-rule`、`/migrate-to-skills` | Team>Project>User 合并；嵌套 AGENTS 具体优先 | 无 description 只能手动、<500 行、非安全控制 |
| Cline | rules / 条件 rules / Memory Bank | md 规则 / `paths` 条件 / 6 文件 wiki | 全量或 paths 命中 / 任务开始全量读 wiki | 人写规则；agent 维护 wiki | workspace>global；可 toggle；YAML 解析失败 fail-open | 规则吃 token；wiki 全量读 + 人工更新、会膨胀 |
| Codex | AGENTS.md 链 | md，每目录至多一个 | 每次 run 自动拼接 | 人写 | 拼接、后读者覆盖；32KiB 截断 | 截断、每 run 重建、无自动写入机制 |
| Devin | Memory / Skills /（Knowledge·Playbooks→Skills） | git memory drive / SKILL.md / note(trigger) | MEMORY.md 每 session + 按需 note / description 或 `@skills:` | agent 写 memory、建议 skill；人写 skill | stale write 拒+重试、Dreaming 合并；同名 skill | 知识库被废弃、skill 正文常驻 context |
| Copilot | instructions / prompt files / Agent Skills / Memory | 全仓·路径 md / 任务 md / SKILL.md / repo-fact·user-pref | 每条消息 / 用户调用 / 模型 / 相关性+投票 | agent 写 memory（部分 surface 需批准）；人写 instructions | 优先级合并；TTL 90 天；投票淘汰 | 非确定性、长指令失效、memory 不能当规则 |
| Amp | AGENTS.md / Skills | md / SKILL.md 目录（git 仓库） | 启动读取 / name+description | 人/agent 共建 | 同名按 11 级来源覆盖 | 仓库 200 skill 静默截断、体积上限 |

## 10. 横向对照表

**A. 原语 × 写入者 × 触发（谁让经验被用到）**

| 产品 | 常驻规则 | 按需知识/流程 | 手动流程 | 自动记忆 | 写入者 |
|---|---|---|---|---|---|
| Windsurf/Cascade | Rules（4 激活） | Skills（successor） | **Workflows**（仅 slash，绝不自动） | **Memories**（workspace 级） | 人写规则/流程；agent 写 memory、可生成 workflow |
| Claude Code | CLAUDE.md + `.claude/rules`（paths） | **Skills**（三层加载） | `/skill-name`、plugins | **Auto memory**（MEMORY.md 索引） | 人写规则/skill；agent 写 memory、可回写 skill |
| Cursor | Rules（4 激活）+ AGENTS.md | **Skills**（兼容 `.claude`/`.codex`） | `@rule`、`/skill`、`/create-*` | 无产品级；SDK journal | 人写；agent 可 `/create-rule`、`/migrate-to-skills` |
| Cline | `.clinerules`（含 paths 条件）+ AGENTS.md | （靠 rules 指路） | 规则 toggle | **Memory Bank**（agent 维护 md） | 人写规则；agent 写/更新 wiki |
| Codex | **AGENTS.md** 层级链 | —（plugins/skills 生态，未取到一手正文） | — | — | 人写；无内建自动写入 |
| Devin | Devin Desktop 有 rules；Cloud 无 rules | **Skills**；Knowledge/Playbooks→Skills | `@skills:`、macro | **Memory + Dreaming** | agent 写 memory、建议 skill（人审+PR）；人写 skill |
| Copilot | instructions（全仓/路径/AGENTS） | Agent Skills | **Prompt files**、slash | **Copilot Memory**（repo facts / user prefs） | agent 写 memory（部分 surface 需人批准）；人写 instructions |
| Amp | AGENTS.md | **Skills**（git 仓库） | `amp skill add` | — | 人/agent 共建 skill |

**B. 硬约束对照（"索引常驻、正文按需"是共同解法）**

| 产品 | 常驻预算/上限 | 按需部分的检索键 |
|---|---|---|
| Claude Code | CLAUDE.md 目标 <200 行；MEMORY.md 前 200 行/25KB；>4MiB 跳过 | skill `description`；rule `paths` glob |
| Codex | AGENTS.md 合并 32KiB（默认） | 目录层级 |
| Windsurf | rule/workflow 各 12000 字符 | glob / natural language description |
| Cursor | 规则建议 <500 行 | `alwaysApply` / `globs` / `description` / `@` |
| Cline | 无硬限，但"规则消耗 token" | `paths` frontmatter |
| Devin | MEMORY.md 每 session；note 按需读 | skill name+description；Knowledge trigger |
| Copilot | instructions 每条消息发送 | `applyTo` glob；memory 相关性 + 投票 |

## 11. 八个产品共同收敛出的不变量

1. **原语按"检索时序"分层，而不是按"内容类型"分层**：always-on / description 触发 / 人工调用 / 自动召回，四种时序对应四个原语——所有 8 个产品都能映射到这四层。
2. **"描述即索引"**：凡是"可能要用"的经验，必须有一行 `name+description` 常驻，否则模型无法发现，只能退化为人工调用（Claude skills、Devin skills/Knowledge trigger、Cursor description、Amp、Copilot）。
3. **索引常驻、正文按需**是唯一同时满足"可发现"与"省 context"的结构（三层 progressive disclosure 被 Claude/Devin/Cursor/Amp 共同采用）。
4. **文字经验不具强制力**：官方普遍承认"指令是 context 不是配置"；要确定性必须走 hook / 权限 / CI（Claude 原文、Copilot"不是规则"、Cursor"不能当安全控制"、Windsurf 规则不保证遵守）。
5. **冲突处理普遍是"全量注入 + 后者/高优先者覆盖"，没有逻辑裁决**；因此"何时删除旧经验"是每个产品都必须回答但都答得不好的问题（Copilot 用 TTL、Devin 用 Dreaming、Claude 用 `/doctor`、Cursor 用"犯错才更新"）。
6. **自动记忆默认个人化/机器本地**：Claude（按 repo、machine-local）、Devin（personal）、Copilot（user prefs）、Windsurf（workspace）。唯一共享形态是 Copilot 的 repository facts，但它带 citation、权限与投票淘汰，本质仍是"有来源的个人贡献"而不是团队知识库。
7. **共享 = 文件 + 仓库**：团队级经验全部落在可提交文件（AGENTS.md、rules、SKILL.md、instructions）里，靠 git 审阅，而不是靠记忆系统同步。
8. **写入者正在从"人"转向"agent 写 + 人审"**：Claude auto memory / Devin memory 自动写，Devin/Claude 会建议 skill 或把跑通的配方回写，Cursor 提供 `/create-rule`、`/create-skill`、`/migrate-to-skills`，Windsurf 可让 Cascade 生成 workflow——但所有产品都保留了人的审阅/删除动作。
9. **格式已经标准化**：`SKILL.md`（Agent Skills 开放标准，agentskills.io）与 `AGENTS.md`（agents.md）成为跨工具事实标准；Claude/Cursor/Devin/Amp 互相读对方的 skills 目录。→ 自造格式是逆流。
10. **流程类原语在收敛**：Windsurf Workflow→Skill、Devin Knowledge/Playbook→Skill、Cursor dynamic rules→Skill。**"可执行流程" ≈ SKILL.md 已经是跨产品事实**。

## 12. 仍然没解决的分歧

1. **记忆的归属**：个人（Devin/Copilot user prefs）vs 仓库（Copilot repo facts/Claude）vs workspace（Windsurf）vs 机器（Claude/Amp）——同一"经验"的可见范围没有共识，而这决定了它是"记忆"还是"wiki"。
2. **何时写**：没有产品给出可验证的写入 trigger 定义。Claude/Devin 让 agent 自己判断，Copilot 用投票+TTL，Windsurf 默认自动生成——"会不会写垃圾"仍是开放问题。
3. **如何检索**：description 匹配（模型）vs 全量注入 vs glob/路径 vs 向量 RAG（Cursor 的 context engine 是代码 RAG，不是经验检索）——经验检索没有统一答案。
4. **失效与遗忘**：TTL（Copilot）、Dreaming（Devin）、人工 audit（Claude）、"犯错才改"（Cursor）都只是补丁；"哪条经验该删"没有一等机制。
5. **流程的调用权**：manual-only（Windsurf workflow / Copilot prompt file / Claude `disable-model-invocation`）vs 模型可自动调（Devin/Claude/Cursor skills）。有副作用的流程是否允许模型自启，仍是安全分歧。
6. **文件派 vs UI 派**：文件即真相（Claude/Cursor/Codex/Cline/Amp，利于 git）vs UI 管理（Windsurf/Devin/Copilot，利于权限/去重/投票）。两者在"谁拥有经验"上取向不同。
7. **经验是否可执行**：带 scripts 的 skill（Claude/Devin/Amp）vs 纯文本（rules/AGENTS.md）。沉淀的代码被自动执行的安全模型未统一（Claude 警告恶意 skill；Amp 的 hosted skill 只允许文本）。
8. **没有效果评估**：只有 Claude 给了 skill evals / baseline 对比（用与不用跑同一组 prompt）；其他产品没有"这条经验是否真的更好"的闭环。经验沉淀缺"效果验证"这一环。

## 13. 对"记忆 vs workflow vs wiki"的裁决依据

| 经验形态 | 产品证据支持的落点 | 依据 |
|---|---|---|
| 一两行事实/偏好/纠正，总是适用 | **memory** | Claude auto memory、Devin Memory、Copilot user prefs；行级、带来源/时间、可老化 |
| 多步、可重复、需要顺序/脚本/参考文件、有副作用 | **workflow/skill**（SKILL.md） | Windsurf workflow 只手动→迁 skill；Devin"Use skills for all new work"；三层加载解决 context |
| 大块、需要浏览/多人编辑/审阅的项目档案 | **wiki（可提交 md）**，但**必须带索引与描述** | Cline Memory Bank 的固定 context 税；AGENTS.md 32KiB 截断；Cursor"没有 description 就只能 @ 手动" |
| 项目/团队级常驻约定 | 规则文件（AGENTS.md / rules） | 8 个产品全部保留此原语；但官方都说"不是强制"，强制用 hook |

**三个可直接搬用的设计约束**：① 任何沉淀物都要有"一行索引 + 正文"两段式；② 自动写入必须是个人/机器局部，共享必须走文件 + 审阅；③ 流程与知识都优先复用 `SKILL.md`/`AGENTS.md` 格式，不自造。

## 14. 一手来源

- **Windsurf/Cascade**：<https://docs.devin.ai/windsurf/plugins/cascade/memories.md> · <https://docs.devin.ai/windsurf/plugins/cascade/workflows.md> · <https://docs.devin.ai/desktop/cascade.md> · <https://docs.devin.ai/desktop/devin-local/customizations.md>
- **Claude Code / Agent Skills**：<https://code.claude.com/docs/en/memory.md> · <https://code.claude.com/docs/en/skills.md> · <https://code.claude.com/docs/en/features-overview.md> · <https://claude.com/docs/skills/overview.md> · <https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills> · <https://agentskills.io/specification>
- **Cursor**：<https://cursor.com/docs/rules.md> · <https://cursor.com/docs/skills> · <https://cursor.com/docs/llms.txt> · <https://cursor.com/docs/agent-sdk/reference/memory.md>
- **Cline**：<https://docs.cline.bot/customization/cline-rules.md> · <https://docs.cline.bot/best-practices/memory-bank.md>
- **Codex / AGENTS.md**：<https://developers.openai.com/codex/guides/agents-md>（直连 403，正文取自[镜像](https://cdn.jsdelivr.net/gh/crasuna/openai-dev-docs-cn-mirror@main/sources/en/codex/guides/agents-md.md)）· <https://agents.md/>
- **Devin**：<https://docs.devin.ai/product-guides/memory.md> · <https://docs.devin.ai/product-guides/skills.md> · <https://docs.devin.ai/product-guides/knowledge.md> · <https://docs.devin.ai/product-guides/using-playbooks.md>
- **Copilot**：<https://docs.github.com/en/copilot/concepts/prompting-and-context/custom-instructions> · <https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/add-custom-instructions/add-repository-instructions> · <https://docs.github.com/en/copilot/concepts/prompting-and-context/copilot-memory> · <https://code.visualstudio.com/docs/agent-customization/prompt-files>
- **Amp**：<https://ampcode.com/docs/markdown/customize/skills> · <https://ampcode.com/docs/customize/agents-md>
