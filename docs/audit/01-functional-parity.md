# 功能对齐审计报告（ZeoKeel vs DSH 0.2.1-rc.2）

> 审计者：parity-auditor · 日期：2026-11-05 · 基线：实际 HEAD `27d5d5f`（brief 写的 `7eebe47`；差异仅为 Windows 图标构建资源提交，见 §1）
> 方法：只读代码审计 + DSH 随包 README 对照（305 包）；未运行 GUI/真实模型，未改任何被跟踪文件。
> 限制：无法验证「模型实际行为」类差异（如 PTC/Workflow 的 token 效率）；DSH 侧为已安装包文档与包结构，不读其源码；UI 呈现细节留给 uiux-auditor。

## 0. 结论摘要

- **核心 agent 循环、工具循环、审批、计划模式、自动审核、goal round driver 已经真实存在且接线**——`docs/FUNCTIONAL-PARITY.md` 与 `docs/design/known-limitations.md` 中若干 KNOWN 结论已过时（§4），brief §7 的「goal round driver 缺失」假设**已被证伪**（`internal/app/services.go:123` + `internal/session/controller.go:1543-1553`）。
- **最大缺口集中在「编排层」**：DSH 的 `workflow`（JS 编排引擎）、PTC（代码运行时）、subagent（异步/续谈/fork）在 ZeoKeel 都被实现为**语义降级版**，且旧矩阵标 ✅ 或未记录。
- **第二个缺口是「状态/交付物」**：DSH 有 workspace changes（每回合 diff）、持久附件服务、FTS5 会话检索、会话级 schedule 投递；ZeoKeel 对应能力缺失或以更弱语义实现。
- **模型供应层偏薄**：仅 OpenAI 兼容单 provider，无重试执行器/模型发现/账号登录/凭据 seam（DSH 有 4 个 llm 包 + credentials 包）。
- 估算对齐度：工具**数量**约 85-90%，工具**语义**约 55-65%，编排/会话/交付能力约 45-55%，整体「全功能桌面 Agent」目标约 **60%**（主观估分，依据矩阵）。

| ID | 严重度 | 标题 | NEW/KNOWN | 修复量 |
|---|---|---|---|---|
| FP-01 | P1 | `workflow` 工具是固定顺序 JSON 步骤，非 DSH 的 JS 编排引擎 | NEW | L |
| FP-02 | P1 | PTC 模式只是工具批处理，无 DSH 代码执行运行时（Node/TS 沙箱） | NEW | L |
| FP-03 | P1 | subagent 仅同步 spawn：无异步完成通知、fork、续谈、steer | NEW | L |
| FP-04 | P1 | Goal 能力半成品：工具无 id/revision/phase/pause/blocked，且无用户 `/goal` 入口 | NEW（部分 KNOWN） | M |
| FP-05 | P1 | 缺 workspace changes / 每回合 diff 捕获（DSH `dsh-workspace-changes`） | NEW | M |
| FP-06 | P1 | 无声明式 agent preset/persona：模式固定 4 个，用户不能定义工具/提示词/模型组合 | NEW | L |
| FP-07 | P2 | schedule 语义降级：只支持 cron 子集、触发时新建会话而非投递回原会话、无时区/一次性/提醒 UI | NEW | M |
| FP-08 | P2 | 文件编辑安全语义：`write_file` 非原子写、无 read-before-write 强制 | NEW | M |
| FP-09 | P2 | 会话检索用 SQL LIKE（无 FTS5/排名）；无 `@session` 原子引用 | NEW（部分 KNOWN） | M |
| FP-10 | P2 | 压缩/上下文：chars/4 估算、24K 硬编码预算、无图片卸载、无 replay-aware token meter | NEW | M |
| FP-11 | P2 | checkpoint 仅回合成功边界；DSH 在模型请求/工具副作用前/pre-step 三屏障持久化 | KNOWN（语义细化） | M-L |
| FP-12 | P2 | 模型供应层单薄 + API key 明文存 SQLite + 无 secret mask/重试/多 provider | NEW | M |
| FP-13 | P2 | MCP 仅 stdio，不支持 DSH 的 streamable-http 远端服务器 | NEW | S-M |
| FP-14 | P2 | 附件是「文本 ≤64KiB 内联 prompt」，非 DSH 持久附件 + 只读路径按需读取 | NEW | M |
| FP-15 | P2 | jobs 无完成通知（需轮询）；Agent Team 无共享任务板/revision，成员状态不持久 | NEW | M |
| FP-16 | P2 | `ask_user` 是 fire-and-forget pending + 结束回合，非 DSH 调用内问答/定时等待 | NEW | M |
| FP-17 | P2 | skills 仅单工作区目录、无文件监视、无 `/` skill source、无 Office 技能 | NEW | S-M |
| FP-18 | P2 | 无 headless/CLI/SDK/ACP 形态（仅 GUI 二进制），无法脚本化/编辑器集成 | NEW | L |
| FP-19 | P2 | HTTP 代理行为不一致：`web_fetch` 显式禁代理，provider/web_search 走默认代理 | NEW | S |
| FP-20 | P2 | 文档漂移：旧矩阵/known-limitations/brief 三处把「已实现」写成缺失，误导排期 | NEW | S |
| FP-21 | P3 | 零散实验性能力缺口：voice input 仅占位、无 CoT 翻译、无 session stats/turn outline、无 open-in-app/浏览器侧栏/Document preview | NEW | S-L |

## 1. 范围与方法

### 1.1 实际执行的命令（可复核）

```sh
cd /data/mygo && git rev-parse HEAD && git log --oneline -12 && git status --short
DSH=/usr/local/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai
ls $DSH | wc -l                       # 305
python3  # 提取每个包 README.zh.md 的 description → .cache/audit/parity/dsh-titles.txt
ls internal/{tools,runtime,prompt,session,state,store,mcp,skills,agentteam,automation,websearch,provider,app,shell}
# 逐项对照（主要证据见各发现）
```

- 基线说明（按 brief §6.5）：brief 起始 HEAD `7eebe47`，本报告所有发现的行号核验于 **`git rev-parse HEAD` = `27d5d5f`**（2026-11-05 21:23–21:29 +08:00）；`git show --stat 27d5d5f` 显示与 `7eebe47` 的差异仅 `Makefile/README/scripts` + Windows 资源/图标（无 Go 逻辑），不影响结论。行号以 27d5d5f 为准；若后续 HEAD 再移动，请以符号名（函数/常量名）复核。
- **并发写入记录**：审计期间工作树被**其他进程/成员**并发修改：2026-11-05T21:29 `git status --short` 观测到 `M internal/shell/session_live.go`、`M internal/state/snapshot.go`、`M internal/state/snapshot_test.go` + `?? docs/audit/`（与 brief §6.5 所述「工作树保持干净」不符，供 Lead 参考）。这些 tracked 改动**非本审计者所为**（内容为 session live 状态与 ErrorInfo/历史错误渲染，与本报告证据无关，auditor 未触碰、未回滚、未提交）；功能结论以 `27d5d5f` 的已提交内容为准。
- 证据缓存：`.cache/audit/parity/dsh-packages.txt`（305 包名）、`.cache/audit/parity/dsh-titles.txt`（每包中文描述）。
- DSH 路径简写：下文 `$DSH/dsh-<pkg>/README.zh.md` 即上述目录。

### 1.2 能力矩阵（48 行）

> 状态：✅ 对齐 / ⚠️ 有但语义降级或部分接线 / ❌ 缺失。ZeoKeel 证据默认省略 `internal/` 前缀（已在路径中给出）。

| # | 能力 | DSH 参考包 | ZeoKeel 现状（证据） | 状态 |
|---|---|---|---|---|
| 1 | agent loop / 多轮工具循环 | `dsh-agent-loop` | `runtime/agent.go:1-40`，eino ADK | ✅ |
| 2 | 文件读写工具（read/write/edit） | `dsh-tool-fs` | `tools/fs.go:25,76,121` | ⚠️ 缺 read-before-write、原子写（FP-08） |
| 3 | glob/grep 文件搜索 | `dsh-tool-fs-search` | `tools/search.go:35,91` | ✅ |
| 4 | `str_replace_editor`（Claude Code 风格） | `dsh-tool-str-replace-editor` | `tools/editor.go:73` | ✅ |
| 5 | bash 一次性命令 | `dsh-tool-bash` | `tools/bash.go`（POSIX 平台门控） | ✅ |
| 6 | pwsh 一次性命令 | `dsh-tool-pwsh` | `tools/pwsh.go:77`（Windows 门控） | ✅ |
| 7 | 持久 bash/pwsh（跨调用 shell 状态） | `dsh-tool-bash-persistent`/`-pwsh-persistent` | 无该工具；用 `terminal_*` 间接实现 | ❌ |
| 8 | 持久终端 6 工具 | `dsh-tool-terminal` | `tools/terminal.go:851-1038`（open/read/send/signal/close/list） | ✅ |
| 9 | 后台 jobs 4 工具 | `dsh-tool-jobs` | `tools/jobs.go:465-598` | ⚠️ 无完成通知（FP-15） |
| 10 | web_search | `dsh-tool-web` + `dsh-web-search-deepseek` | `tools/webfetch.go`/`websearch`（必须自配 endpoint，默认关） | ⚠️ 无内置搜索提供方 |
| 11 | web_fetch（SSRF 防护） | `dsh-web-fetch-http` | `tools/webfetch.go:46-100` | ✅ 但禁代理（FP-19） |
| 12 | todo_write | `dsh-tool-todo` | `tools/todo.go` | ✅ |
| 13 | ask_user | `dsh-tool-ask-user` + `dsh-user-questions` | `tools/ask_user.go` | ⚠️ 语义不同（FP-16） |
| 14 | present / deliverables | `dsh-tool-present` + `dsh-client-ui-deliverables` | `tools/present.go` + `shell/plan_ui.go`（artifact 卡） | ✅ |
| 15 | plan mode + exit_plan_mode | `dsh-plan-mode` + `dsh-command-*` | `tools/exit_plan_mode.go:48`、`shell/plan_ui.go:19-120,259-270,388-440` | ✅（KNOWN 文档已过时） |
| 16 | goal 服务 + 工具 | `dsh-goal` + `dsh-tool-goal` | `tools/goal.go:45-66,112-233` | ⚠️ 能力子集（FP-04） |
| 17 | goal round driver | `dsh-goal-round-driver` | `session/goal_driver.go:1-60`、`app/services.go:123`、`session/controller.go:1543-1553` | ✅ |
| 18 | schedule 4 工具 | `dsh-tool-schedule` + `dsh-schedule` | `tools/schedule.go:130-301`、`automation/scheduler.go` | ⚠️ 语义降级（FP-07） |
| 19 | skills（SKILL.md） | `dsh-tool-skill` + `dsh-skill-filesystem` + `dsh-skill-office` | `skills/skills.go:1-120`、`app/services.go:725-733` | ⚠️ 单根/无监视（FP-17） |
| 20 | subagent 委派 | `dsh-tool-subagent` + `dsh-subagent` | `tools/subagent.go:58-75`、`runtime/subagent.go:101-163` | ⚠️ 同步 only（FP-03） |
| 21 | subagent 控制（send/interrupt/list） | `dsh-tool-subagent-control` | `tools/team.go:125-224`（仅 `agent_teams` 插件开启时注册，默认关 `app/services.go:634`） | ⚠️ |
| 22 | Agent Team（实验） | `dsh-experimental-tool-agent-team` + `dsh-experimental-agent-team` | `agentteam/manager.go`、`tools/team_spawn.go:161-362`、`shell/team_ui.go` | ⚠️ 8 工具 vs 9，无任务板（FP-15） |
| 23 | workflow | `dsh-tool-workflow` + `dsh-workflow` | `tools/workflow.go:25-90`（固定步骤 JSON） | ❌ 语义降级（FP-01） |
| 24 | ralph | `dsh-tool-ralph` + `dsh-experimental-ralph-bundle` | `tools/ralph.go` | ✅ |
| 25 | PTC 模式 | `dsh-ptc-runtime(-node)` | `runtime/ptc.go:10-52`（pipeline 批处理） | ❌ 语义降级（FP-02） |
| 26 | worktree | `dsh-experimental-tool-worktree` | `tools/worktree.go:226-398` | ✅（DSH 为实验性） |
| 27 | memory / AGENTS.md | `dsh-agent-instructions` | `tools/memory.go` + `prompt/registry.go:99-100,634-646`（仅根 AGENTS.md，无 CLAUDE.md） | ⚠️ ZeoKeel 额外有 memory 工具 |
| 28 | session query/search/trace | `dsh-tool-session-query` + `dsh-session-query(-sqlite)` | `tools/session_query.go:178-624`、`store/session.go:52,115-138`（LIKE） | ⚠️ 无 FTS5（FP-09） |
| 29 | session 引用（@session） | `dsh-session-reference` + `dsh-client-ui-reference` | 仅 `#<title>` 纯文本 fallback（`known-limitations.md:32`） | ❌ KNOWN |
| 30 | session 标题 | `dsh-session-title-*` | `session/title.go:1-50` | ✅ |
| 31 | 附件（图片/文件） | `dsh-attachment(-local)` + `dsh-client-file-upload` | `session/attachment.go:24-32`、`shell/attachments_ui.go` | ⚠️ 语义不同（FP-14） |
| 32 | compaction | `dsh-compaction-*`（basic/image-offload/pruner） | `runtime/compaction.go`、`runtime/compaction_llm.go` | ⚠️ 无图片卸载（FP-10） |
| 33 | checkpoint 持久性 | `dsh-session-checkpoint-policy` | `runtime/checkpoint.go:20-28`、`runtime/agent.go:787,840` | ⚠️ 仅回合边界（FP-11） |
| 34 | 输出裁剪/spill | `dsh-output-retention` + `dsh-spill*` | `tools/config.go:11`（64KiB cap）、`session/tool_spill.go` | ⚠️ 无统一省略页脚/图片预算 |
| 35 | agent 模式/preset | `dsh-agent-preset(-registry)` + `dsh-persona` | `runtime/modes.go:18-120`（固定 4 模式） | ❌ 不可配置（FP-06） |
| 36 | 权限预设/审批 | `dsh-permission-presets` + `dsh-user-approval` | `session/controller.go:2276-2283`、`app/services.go:549-553,886-900` | ⚠️ 固定 4+auto，无 custom 预设表（FP-12 相关） |
| 37 | auto-review | `dsh-experimental-auto-review` | `runtime/review.go:1-80`、`app/services.go:856-884` | ✅（但缺历史分区，FP-12） |
| 38 | MCP tools/resources/prompts | `dsh-mcp-client` + `dsh-mcp-resources` | `mcp/mcp.go:1-70`、`mcp/resources.go`、`tools/mcp_resources.go` | ⚠️ 仅 stdio（FP-13） |
| 39 | 模型 provider/凭据 | `dsh-llm*`(4) + `dsh-credentials(-local)` | `provider/openai/*`（3 文件）、`store/model.go:47-49`、`store/schema.go:63-66` | ⚠️ 单薄（FP-12） |
| 40 | slash commands | `dsh-commands` + `dsh-command-*` | `shell/composer_slash.go:66-146`（13 命令） | ⚠️ 缺 `/goal`、`/compact`（KNOWN） |
| 41 | @ 文件/会话提及 | `dsh-file-reference(-local)` | `shell` 文件面板 / `@` 下钻（`known-limitations.md:32`） | ⚠️ 文件 text 插入，会话无原子引用 |
| 42 | 插件管理/profile/市场 | `dsh-plugin-manager` + `dsh-plugin-market*` + profile | `shell/plugins_ui.go:40-100`（内置开关；voice 明示未内置） | ⚠️ 产品边界（无市场/profile） |
| 43 | voice input / STT | `dsh-experimental-voice-input-bundle` + `dsh-experimental-speech-to-text(-sensevoice)` | 仅 UI 占位行，无实现（`shell/plugins_ui.go:67`、`shell/plugin_details.go:363-368` 明示未内置） | ❌ 实验性 |
| 44 | CoT 翻译 | `dsh-experimental-translator` | 无 | ❌ 实验性 |
| 45 | HTTP 代理 | `dsh-http-proxy` | 无统一代理策略；`tools/webfetch.go:70-74` 显式 `Proxy: nil` | ❌（FP-19） |
| 46 | headless / CLI / SDK / ACP | `dsh-headless`、`dsh-sdk-*`、`dsh-acp*`、`dsh-cmdline` | `cmd/zeokeel` 仅 GUI 入口 | ❌（FP-18） |
| 47 | workspace changes / turn diff | `dsh-workspace-changes` | 无对应实现（全仓 grep 无 `git diff`/turn changes） | ❌（FP-05） |
| 48 | 右侧栏浏览器/Document preview/open-in-app | `dsh-client-ui-sidebar-browser`、`dsh-client-ui-sidebar-documentpreview`、`dsh-client-ui-open-in-app` | 右栏有 Files/Terminal/Session Log（`shell/start_tab.go:376+`），无 browser/office；无 open-in-app（`shell/shortcuts.go:169` 注明未实现） | ❌ |

## 2. 详细发现

> 行号核验时点：全部发现均核验于 `git rev-parse HEAD` = `27d5d5f`（2026-11-05 21:23–21:29 +08:00）；未运行 GUI/真实模型。

### [FP-01] `workflow` 工具是固定顺序 JSON 步骤，非 DSH 的 JS 编排引擎 · P1 · NEW

- 现象：ZeoKeel `workflow` 只接受 `{"steps":["...",...],"agent":"..."}`，最多 8 步、严格串行、失败即停；DSH workflow 是「模型编写的纯 JavaScript 编排脚本」，提供 `agent()/parallel()/pipeline()/phase()/log()`、结构化 schema 返回、`runId/agentsStarted/result` 包络。
- 证据：`internal/tools/workflow.go:25-46`（`DefaultWorkflowMaxSteps=8`、`WorkflowStepResult`）与 `:58-90`（描述里的 JSON 步骤 schema）；`$DSH/dsh-tool-workflow/README.zh.md`、`$DSH/dsh-workflow/README.zh.md`（“运行一段纯 JavaScript 编排脚本…`parallel()` 与 `pipeline()` 组合独立工作”）；`docs/FUNCTIONAL-PARITY.md:12` 只写“PTC 以 pipeline 批处理工具实现”，未记录 workflow 差异。
- 影响：模型无法在一次工具调用里做**并行扇出**（多文件审计/迁移/多角度研究）与基于中间结果的脚本逻辑；DSH 的 `workflow` 是 agent 在高复杂度任务上的主要编排手段。这直接削弱“全功能桌面 Agent”的自动化上限。
- 复现/验证：注册表里 `workflow` 的 schema 只有 `steps`/`agent`；与 DSH tool catalog 的 `script/meta/args` 对比即见。也可构造 9 步任务看被拒。
- 建议修复：分两阶段：S 阶段先在 Go 里实现 `parallel/pipeline` 语义的 JSON 编排子集（低成本补齐 80% 场景）；L 阶段引入受限脚本运行时（可评估 goja/WASM，保持纯 Go/no-cgo 边界）。

### [FP-02] PTC 模式只是工具批处理，无 DSH 代码执行运行时（Node/TS 沙箱） · P1 · NEW

- 现象：ZeoKeel 明确“deliberately does not embed a JavaScript sandbox”，PTC 模式只增加一个 `ptc` 工具，串行执行一批普通工具调用并返回全部结果；DSH PTC 是 `ctx.ptcRuntime` + `dsh-ptc-runtime-node`：在受管 Node 子进程中执行模型编写的可擦除 TypeScript，带会话文件沙箱、执行/输出限制、持久 REPL 内存、结构化失败类别。
- 证据：`internal/runtime/ptc.go:10-24`（注释自述刻意不嵌入 JS 沙箱）、`:39-52`（`PTCRequest.Calls`）、`:79-108`（`NewPTCTool`）；`runtime/modes.go:22-28`；`$DSH/dsh-ptc-runtime/README.zh.md`、`$DSH/dsh-ptc-runtime-node/README.zh.md`；`docs/FUNCTIONAL-PARITY.md:16` 标“Agent 模式 ✅ 已对齐”。
- 影响：PTC 的核心价值是“模型写代码做数据加工/循环/聚合”，批处理只能省模型往返，不能表达真实程序逻辑（过滤/去重/条件分支仍需模型推理）。旧矩阵 ✅ 会误导用户以为该模式已对齐。
- 复现/验证：`runtime/ptc.go` 的 schema 只有 `calls[]`；DSH README 的 `program/bindings/result.logs/result.error` 结构在 ZeoKeel 不存在。
- 建议修复：M 阶段：为 `ptc` 增加一组内建 pipeline 操作（filter/dedup/sort/collect）作为受限 DSL，并在文档标注降级；L 阶段：引入受限执行器（如 goja 解释器 + 纯 Go 沙箱策略），或明确产品边界并在 FUNCTIONAL-PARITY 中标 ❌。

### [FP-03] subagent 仅同步 spawn：无异步完成通知、fork、续谈、steer · P1 · NEW

- 现象：`subagent` 工具“runs the child agent to completion and returns its final text”，子 agent 无状态（不加载 History/compaction/checkpoint），不返回 childId；DSH subagent seam：`startActivation()` 立即返回 childId，后台 activation 结算后父级收到完成通知；本地 child 可 `sendMessage()` 续谈/Steer/冷恢复；有 spawn/fork 两个后端、`maxActiveSubagents` 容量默认 8、child 可指定 agentPreset/模型。
- 证据：`internal/tools/subagent.go:58-75`（描述自述 synchronous）、`internal/runtime/subagent.go:101-109`（“Child agents are stateless”）、`:148-163`（`adk.NewAgentTool` 同步 Invoke）；`internal/app/services.go:632-647`（子代理族 + `agent_teams` 默认 false）；`$DSH/dsh-subagent/README.zh.md`（“工具立即返回子会话 ID，后台 activation 结算后父级收到完成通知”）。
- 影响：多 agent 并行工作流只能靠实验性 Agent Teams（默认关闭，且与 DSH 语义也不同）；用户在长任务中无法“给子代理追加指令/续谈”；fork 语义（带父对话上下文的新 agent）完全缺失。
- 复现/验证：`subagent` 工具结果只有 `agent/task/output/depth/tools`，无 childId；`runtime/subagent.go` 未实现 resume/fork。
- 建议修复：M/L：把现有 `agentteam.Manager` 的异步队列提升为 `subagent` 的默认交付路径（返回 childId + 完成通知），并让 `_fork` 用父 History 种子构建 child；同步模式保留为显式选项。

### [FP-04] Goal 能力半成品：工具无 id/revision/phase/pause/blocked，且无用户 `/goal` 入口 · P1 · NEW（部分 KNOWN）

- 现象：ZeoKeel `goal` 工具只有 `set/get/complete/abandon`，goal 对象无 id/revision/phase/round 信息；DSH `get_goal/create_goal/update_goal` 支持 edit/pause/resume/complete/blocked、精确 revision CAS、`max_goal_rounds`、blocked 连续回合阈值、以及“create/edit/pause/resume 需要人类直接请求”的权限规则；面向人的 `/goal` 命令（DSH `dsh-command-goal`）ZeoKeel 有意未做（KNOWN，`known-limitations.md:30`）。
- 证据：`internal/tools/goal.go:45-66`（四种 action）、`:112-233`；`$DSH/dsh-tool-goal/README.zh.md`（“更新必须使用先前读取到的精确 goal id 和 revision”；5 种 action 表）；`docs/FUNCTIONAL-PARITY.md:18` 说无 round driver（已过时），但工具子集问题未记录。
- 影响：自动 goal round driver 只对“模型自己 set 的 goal”生效；用户无法用 `/goal` 查看/暂停/恢复/放弃目标（只有 UI 外入口），也无法设 round 预算（只有内部 setting `session.<id>.goal_rounds`）。暂停/恢复/阻塞语义缺失使长任务不可控。
- 复现/验证：调用 `goal {"action":"get"}` 返回的 goal 无 revision/phase；`/goal` 不在 `composer_slash.go:66-146` 的命令表中。
- 建议修复：M：goal 领域模型加 id/revision/phase/rounds/blocked_reason（store 已有 goal 持久化，可扩展 JSON）→ 然后接 `/goal` 命令与 Session UI 徽章；这是 DSH 对齐中“用户可控制长任务”的关键一环。

### [FP-05] 缺 workspace changes / 每回合 diff 捕获 · P1 · NEW

- 现象：全仓 grep 无 `git diff`、`workspace/changes`、turn changes 相关实现；DSH `dsh-workspace-changes` 用 git 工作树快照 + 文件工具编辑前后整文件捕获，汇总每个顶层回合改动文件，宣告 `workspace/changes` Session 事件，并提供摘要与逐文件对比。ZeoKeel 右栏 Files 有文件预览/列表（`shell/files_preview.go`），但没有“本回合改了哪些文件”的视图/事件。
- 证据：`internal/` 全仓 grep `git diff|TurnDiff|workspace/changes|WorkspaceChanges` 无命中；`$DSH/dsh-workspace-changes/README.zh.md`；`docs/FUNCTIONAL-PARITY.md` 未列此能力。
- 影响：编码 agent 的核心反馈闭环缺失——用户无法在 UI 中审阅 agent 本轮改了什么，只能自己去翻文件/外部 git。DSH 把它同时用于 UI 展示与评价/遥测。
- 复现/验证：完成一个写文件的回合后，Session 事件流无 changes 事件，右栏无 diff 视图。
- 建议修复：M：复用现有 `store/event.go` 发 `workspace/changes` 事件；实现可在每回合前对 workspace 做 git status 快照（无 git 时退化 mtime/hash），回合后 diff。

### [FP-06] 无声明式 agent preset/persona：模式固定 4 个 · P1 · NEW

- 现象：ZeoKeel 的 agent 模式硬编码 `standard/ptc/minimal/creator`（+ 三个 legacy 别名），工具集与提示词策略写死在 `runtime/modes.go`；DSH 用 `dsh-agent-preset` 在 Cordis YAML 里声明多个 preset（工具、提示词、技能），`dsh-agent-preset-registry` 让会话选择，`dsh-persona` 给每个 agent 挂人设段，`dsh-agent-default-model`/subagent 可按 preset 选模型。
- 证据：`internal/runtime/modes.go:18-120`（`Modes()` 固定 4 项、`minimalToolNames` 白名单写死）；`$DSH/dsh-agent-preset/README.zh.md`（“在普通 Cordis YAML 中定义 Agent 的子插件列表…由会话选择”）、`$DSH/dsh-persona/README.zh.md`。
- 影响：用户/团队无法定义“代码审查员”“文档写手”这类专属 agent（不同工具/提示词/模型），也无法给 subagent 指定 preset。Desktop agent 场景里这是重要的可扩展点；ZeoKeel 只有 Creator mode 的 settings 工具 + 手写 SKILL.md 两种弱扩展手段。
- 复现/验证：Settings/Plugins UI 无 preset 管理；`runtime.Modes()` 为常量返回。
- 建议修复：L：引入 `agent presets` 存储（JSON 定义工具 allowlist/提示词段/模型/父 preset）+ 会话选择 UI + 每 preset 默认模型；先做“内置 preset 的声明式化”，再考虑 YAML/插件。

### [FP-07] schedule 语义降级：cron 子集、触发新建会话、无时区/一次性/提醒 UI · P2 · NEW

- 现象：ZeoKeel `schedule_create` 只接受 cron 子集（`*/N * * * *`、`M H * * *`、`@hourly/@daily/@midnight`），无 `after_seconds/at/every_seconds/daily/weekly` 选择器、无 IANA 时区；automation 表无 session_id，到点由 `app.startAutomation` **新建一个会话**再提交 prompt；DSH schedule 是宿主级持久提醒，到期**投递回原会话**（必要时冷恢复会话），支持一次性/间隔/每日/每周/cron+IANA 时区、投递历史记录。
- 证据：`internal/tools/schedule.go:34-46`（cron 子集）；`internal/store/schema.go:67-78`（automations 无 session 列）；`internal/app/app.go:593-614`（`runTask` 调 `ctrl.CreateSession` 再 Submit）；`$DSH/dsh-schedule/README.zh.md`（“作为后续消息投递到原会话…到期时宿主恢复冷会话”）。
- 影响：用户让 agent “明早提醒我看这个任务的进展”时，结果是一个全新空会话，没有上下文；相对延迟/时区/一次性提醒（最常见的提醒形态）无法表达；模型侧 `schedule_list` 只能看全局任务。旧矩阵 `FUNCTIONAL-PARITY.md:19` 标 ✅ 过于乐观。
- 建议修复：M：automation 增加 session_id/origin 与更多 trigger 选择器；fire 时若原会话存在则投递消息（`Submit`），否则新建并标注来源；UI 增加会话内提醒列表（DSH `dsh-client-ui-schedule`）。

### [FP-08] 文件编辑安全语义：`write_file` 非原子写、无 read-before-write 强制 · P2 · NEW

- 现象：`write_file` 直接用 `os.WriteFile`（非 tmp+rename，不加跨进程锁），且不要求先 `read_file`；`edit_file` 描述只说“Use read_file first”但不强制。DSH `dsh-atomic-write` 提供原子替换 + 跨进程写锁（用于“绝不能在磁盘上留下不完整内容”的场景），`dsh-fs-observation-policy` 在写入/编辑前强制观察（读）文件。
- 证据：`internal/tools/fs.go:104`（`os.WriteFile(path, []byte(a.Content), 0o644)`）、`fs.go:114-125`（edit 的 old/new 无观察校验）；`$DSH/dsh-atomic-write/README.zh.md`、`$DSH/dsh-fs-observation-policy/README.zh.md`。
- 影响：崩溃/磁盘满/并发写入时可留下截断文件（数据损坏）；模型可在未读文件的情况下整文件覆盖（盲写破坏用户内容）。对“桌面 AI Agent”是数据安全底线，DSH 专门用两个包解决。
- 建议修复：M：write_file 改 write-temp+fsync+rename，`edit_file/str_replace_editor` 记录本会话读过的文件（含 mtime/hash 校验）后再允许写；审批侧可对“未观察的盲写”升级为需审批。

### [FP-09] 会话检索用 SQL LIKE（无 FTS5/排名）；无 `@session` 原子引用 · P2 · NEW（部分 KNOWN）

- 现象：`store.SearchSessions` 用 `title LIKE` + `messages.content LIKE` 精确子串匹配（无分词/排名/FTS 索引）；DSH `dsh-session-query` 是统一查询服务，附 `dsh-session-query-sqlite`（SQLite FTS5 全文搜索后端）提供带排名的搜索；另外 DSH `dsh-session-reference` 提供跨会话快照引用（原子 `@session` 提及 + 不可信模型上下文隔离），ZeoKeel 只有 `#<title>` 纯文本 fallback（KNOWN，`known-limitations.md:32`）。
- 证据：`internal/store/session.go:52,115-138`；`internal/tools/session_query.go:178-624`；`$DSH/dsh-session-query/README.zh.md`（“带排名的全文搜索需要 dsh-session-query-sqlite”）、`$DSH/dsh-session-reference/README.zh.md`。
- 影响：中文/多词查询召回差、无相关性排序；历史长时 LIKE 全表扫描。会话引用只是文本，模型/用户无法解析成结构化引用，跨会话上下文复用弱。
- 建议修复：M：SQLite FTS5 虚表（或 tri-gram）+ 中文分词策略；session 引用先做“选中即插入结构化 block + 服务端解析”的轻量版（KNOWN 已列 P1，建议按 DSH 语义补齐）。

### [FP-10] 压缩/上下文：chars/4 估算、24K 硬编码预算、无图片卸载 · P2 · NEW

- 现象：ZeoKeel `estimateTokens` 用 chars/4 启发式（CJK 会显著低估/高估），ContextBudget 在 app 里写死 24000，只做 LLM 摘要 + 旧工具结果裁剪；DSH 有 `dsh-token-meter`（replay-aware token/context 压力计量）、`dsh-compaction-image-offload`（图片预算超限时卸载/省略）、`dsh-compaction-tool-result-pruner`（可配置的裁剪策略），压缩行为可配置。
- 证据：`internal/runtime/compaction.go:136-139`、`internal/app/services.go:988`（`ContextBudget: 24000`）、`compaction_llm.go`；`$DSH/dsh-token-meter/README.zh.md`、`$DSH/dsh-compaction-image-offload/README.zh.md`。
- 影响：中文长会话的压缩触发点不准（chars/4 假设按英文）；图片会一直留在历史里占上下文，高图片会话容易爆预算或过早压缩；预算不可按模型窗口配置（固定 24000 对长上下文模型过于保守）。
- 建议修复：M：按 model 元数据配置 context window/budget（provider 侧加 model catalog）；token 估算按 rune 区分 CJK；压缩前把旧图片替换为占位（image offload）；把压缩策略并入设置。

### [FP-11] checkpoint 仅回合成功边界；DSH 三屏障持久化 · P2 · KNOWN（语义细化）

- 现象：ZeoKeel 只在**成功回合结束**写 checkpoint（`CheckpointPhaseCompleted`），History 为权威、checkpoint 仅镜像；DSH `dsh-session-checkpoint-policy` 在三个屏障持久化：模型请求前 flush、顶层工具调用（有外部副作用）前 flush、每个 `agent/pre-step` 边界 flush——崩溃后可从已存请求/工具调用/结果恢复，未完成流保持瞬态、中断工具调用以“结果未知”恢复且不自动重试。
- 证据：`internal/runtime/checkpoint.go:20-28`、`internal/runtime/agent.go:787,840`；`docs/FUNCTIONAL-PARITY.md:15`（KNOWN：checkpoint 已在回合边界接线）、`:36`（进行中回合不续跑）；`$DSH/dsh-session-checkpoint-policy/README.zh.md`。
- 影响：进程崩溃时，当前回合内已发生的工具副作用（如已写文件/已执行命令）没有持久记录，恢复后模型不知道执行到哪一步，可能重复执行有副作用的操作。这是数据/操作安全边界，DSH 专门建包解决。
- 建议修复：M-L：在 runtime 的模型请求前/工具执行前各发一个 checkpoint（同一 `checkpoints` 表存 phase + 事件序列），恢复时把未完成工具调用标为“结果未知”加入 history（`recoveredToolResult` 常量已存在 `session/controller.go:1853`，可复用）。

### [FP-12] 模型供应层单薄 + API key 明文存 SQLite + 无 secret mask/重试/多 provider · P2 · NEW

- 现象：只有 `internal/provider/openai`（OpenAI Chat Completions 兼容，含 reasoning_content/reasoning_effort/图片），无 provider 目录（DSH `dsh-llm-pi-ai` 多提供方）、无模型发现（`dsh-llm-deepseek-api-key` 有模型列表）、无按 provider 路由的重试执行器（`dsh-llm-retry`）、无 DeepSeek 账号登录（`dsh-deepseek-account`）；API key 与其他设置一起明文存 SQLite `settings` 表（目录 0700）；设置页的 key 输入框有遮挡（`internal/shell/models_ui.go:261`、`plugin_details.go:162`），但**用户消息里粘贴的凭据**没有 DSH `dsh-client-ui-secret-mask` 那样的显示遮挡，`inspect` 工具只对设置项做 `[redacted]`（`tools/inspect.go:146-147`）。
- 证据：`internal/provider/openai/openai.go:69-143`、`:133`（`&http.Client{Timeout}`，无 Transport 定制/重试）；`internal/store/model.go:47-49`（`provider.api_key` setting key）；`internal/store/schema.go:63-66`（settings 明文 KV）；`$DSH/dsh-llm-retry/README.zh.md`、`$DSH/dsh-llm-pi-ai/README.zh.md`、`$DSH/dsh-client-ui-secret-mask/README.zh.md`。
- 影响：网络抖动/限流时无自动重试（长 agent 回合中途失败即整轮失败）；用户无法用 Anthropic/OpenAI/本地网关等多 provider 组合；key 以明文落盘，任何能读该 SQLite/备份/同步盘的人可直接取用；用户消息里粘贴的 key 会原样显示。
- 建议修复：M：先做 (a) 请求级重试（幂等流式边界/退避）；(b) settings 读写走 credentials 抽象 + 至少文件权限/加密存储（或 OS keychain 可选）；(c) UI 文本 secret mask；L：provider registry + 模型列表发现。

### [FP-13] MCP 仅 stdio，不支持 streamable-http 远端服务器 · P2 · NEW

- 现象：`mcp.ServerConfig` 只有 `Command/Args/Env`（stdio），连接用 `CommandTransport`；DSH `dsh-mcp-client` 支持 `transport: stdio | streamable-http`，远端服务走 Streamable HTTP，协议协商/重连/环境清洗均有实现。
- 证据：`internal/mcp/mcp.go:1-70`（包注释“Servers are launched over stdio”、`ServerConfig` 无 URL/transport 字段）；`$DSH/dsh-mcp-client/README.zh.md`（transport 表：`stdio` 或 `streamable-http`）。
- 影响：无法接入以 HTTP 提供的 MCP 服务（云端 SaaS/团队共享服务），MCP 生态覆盖约一半；对“全功能 agent”是明显缺口。
- 建议修复：S-M：`go-sdk` 支持 Streamable HTTP，给 ServerConfig 加 `transport/url/headers`，配置解析与连接超时复用现有结构即可。

### [FP-14] 附件是「文本 ≤64KiB 内联 prompt」，非 DSH 持久附件 + 只读路径按需读取 · P2 · NEW

- 现象：ZeoKeel 附件：图片转 data URL 多模态 part；文本附件超过 64KiB 截断后**内联进 prompt**；`AttachmentKindFile`（PDF/zip/xlsx 等二进制）会被接收、持久化并在 UI 显示 chip，但重组 prompt 时 `switch` 只处理 text/image，`file` 分支**静默跳过**——模型完全收不到该附件，也不会报错。DSH attachment：图片校验规范化后持久存储、历史中保留并在后续轮次投影；普通文件按字节原样保存（不设格式/大小限制），模型通过保存的**只读路径按需读取**（不接收字节）；持久事件不包含浏览器路径/本地路径/base64；附件可传给命令并在重启后复用。
- 证据：`internal/session/attachment.go:24-32`（`MaxTextAttachmentBytes=64KiB`）、`:437-462`（`turnAttachmentInputs` 只处理 text/image）、`internal/app/services.go:190-210`（重建 history 同样只处理 text/image）；`runtime/provider.go:60-70`（`UserAttachment` 文本/图片 part）；`$DSH/dsh-attachment/README.zh.md`（“其他文件按字节原样保存，不设格式与大小限制；模型通过保存的只读路径按需读取”）。
- 影响：大文件/二进制（PDF、xlsx、zip）无法真正进入工作上下文；文本内联消耗 token 且截断丢信息；附件与命令的复用、跨重启恢复语义弱于 DSH。
- 建议修复：M：附件统一落 `<session>/attachments/`，prompt 中改为 `<attachment name path .../>` 引用 + 模型用 read_file/read_image 按需读；保留现有文本内联作为小文件快路径。

### [FP-15] jobs 无完成通知；Agent Team 无共享任务板/revision，成员状态不持久 · P2 · NEW

- 现象：ZeoKeel 的 job 工具描述明确“There is no push notification when a job finishes, so poll”；DSH `dsh-tool-jobs` 提供完成通知（job_output/job_list/job_kill + completion notification）。ZeoKeel Agent Teams（默认关闭）有 `spawn_team/list_teams/team_status/close_team/wait_team` + `send_message/interrupt_agent/list_subagent_models` 共 8 个工具，团队成员状态“never persisted”；DSH `dsh-experimental-tool-agent-team` 是 9 个工具，含持久共享**任务板**（owner + revision CAS、过期编辑拒绝），团队策略段下发到每个成员，Lead-only spawn/interrupt，且成员 Session 可冷恢复。
- 证据：`internal/tools/jobs.go:537-540`；`internal/tools/team.go:20-31`（“subagent state itself is never persisted”）、`team_spawn.go:22-44`；`internal/prompt/registry.go:337-338`（prompt 也明确告诉模型“没有推送通知，必须轮询”）；`$DSH/dsh-tool-jobs/README.zh.md`、`$DSH/dsh-experimental-tool-agent-team/README.zh.md`（“九个工具…通过共享任务板协调”）。
- 影响：长任务只能靠模型自己轮询（浪费回合、可能错过完成时机）；多 agent 协作缺少共享任务板 → 重复劳动/冲突写；进程重启后团队状态丢失。
- 建议修复：M：job 完成时注入“step reminder/通知”给父 agent（或 in-turn 队列），team 增加 task board（可先复用 `todos` 表结构 + owner/revision），成员状态持久化到 session events。

### [FP-16] `ask_user` 是 fire-and-forget pending + 结束回合，非 DSH 调用内问答/定时等待 · P2 · NEW

- 现象：ZeoKeel `ask_user` 立即返回 `{"kind":"question","status":"pending"}` 并结束回合，用户回答成为下一条用户消息；DSH `dsh-user-questions` 是 waterfall 问答服务：工具挂起等待答案（带计时等待、草稿、迟到回复、`ask_user_question` 附着式提问卡、plan-review 审批卡），答案作为**工具结果**回到同一回合。
- 证据：`internal/tools/ask_user.go:58-70`（描述“stop the turn…user's answer becomes the next user turn”）、`:112-120`（pending 语义）；`$DSH/dsh-tool-ask-user/README.zh.md`（“默认情况下，它会等待回答；设置 `mode: timed` 后…`timeout: -1` 无限期等待”）、`$DSH/dsh-client-ui-user-questions/README.zh.md`（“计时等待、草稿、迟到回复与 plan-review 审批卡片”）。
- 影响：提问后 agent 回合终止，模型无法在同一回合内基于答案继续（多一次完整重建回合）；无“选项点选/计时/迟到回复”体验；与 plan review 的交互不一致。
- 建议修复：M：把 Controller 的 Question 升为阻塞式（工具 Invoke 等待 answer channel），UI 回答后写回 tool result；保留超时→pending 的降级路径。

### [FP-17] skills 仅单工作区目录、无文件监视、无 `/` skill source、无 Office 技能 · P2 · NEW

- 现象：ZeoKeel skills 只扫一个 root（默认 `<workspace>/skills`），启动/重建时加载，无文件监视；skill 只能通过模型侧 `skill` 工具发现，composer `/` 面板不列出 skills（`composer_slash.go:66-146` 无 skill 行）；无 Office 创作技能。DSH：`dsh-skill-filesystem` 支持 project/custom/user 多根目录的发现与**监视**，`dsh-skill` 是合并/解析注册表，`dsh-client-ui-skill` 提供 `/` 触发 skill source 与 skill 调用卡，`dsh-skill-office` 提供渐进加载的 Office 创作/CLI 技能。
- 证据：`internal/skills/skills.go:1-34`（单 root）、`internal/app/services.go:725-733`（`os.Stat` 一次）；`$DSH/dsh-skill-filesystem/README.zh.md`、`$DSH/dsh-client-ui-skill/README.zh.md`。
- 影响：用户级/全局技能无法共享；新增/修改 SKILL.md 需重建 agent；技能不可从 UI 发现/触发；Office 文档工作流（DSH 内置）缺失。
- 建议修复：S-M：多 root（workspace + `~/.zeokeel/skills` + 项目 `.zeokeel/skills`）+ 启动/定时 reload；`/` 命令面板加 skill 分组（点击即注入正文）；Office 技能可作为独立 SKILL.md 后续补。

### [FP-18] 无 headless/CLI/SDK/ACP 形态（仅 GUI 二进制） · P2 · NEW

- 现象：`cmd/zeokeel` 是 GUI 应用；无命令行单任务模式、无 JSON-RPC SDK、无 ACP 服务，也无 `--profile` 概念。DSH 有 `dsh-headless`（一次性任务打印最终答案）、`dsh-sdk-jsonrpc-server`/`dsh-sdk-app`/`dsh-sdk-minimal`、`dsh-acp`/`dsh-acp-app`、`dsh-cmdline`。
- 证据：`ls cmd/`（仅 zeokeel）、`internal/app/app.go`（GUI app 构造）；`$DSH/dsh-headless/README.zh.md`、`$DSH/dsh-acp/README.zh.md`。
- 影响：无法被脚本/CI/编辑器（Zed 等 ACP 客户端）驱动，“全功能桌面 Agent”只能人肉在窗口里用；也无法做无人值守自动化（虽然目标以桌面为主，但这属于 DSH 已有形态差距）。
- 建议修复：L：复用 runtime.Controller 抽出 headless 入口（`zeokeel run "prompt"`）作为最小可用 CLI；SDK/ACP 作为后续阶段。

### [FP-19] HTTP 代理行为不一致：`web_fetch` 显式禁代理 · P2 · NEW

- 现象：`web_fetch` 的 Transport 显式 `Proxy: nil`（注释：代理会隐藏真实目的地、破坏 SSRF 校验），因此企业代理/科学上网环境下 web_fetch 直连失败；而 web_search（默认 `http.Client` → `http.DefaultTransport` 会读 `HTTP(S)_PROXY`）与 provider（`http.Client{Timeout}` 同样默认 transport）走代理。DSH 有独立 `dsh-http-proxy` 包统一解析出站代理策略。
- 证据：`internal/tools/webfetch.go:70-74`（`Proxy: nil` + 注释 `:70`）；`internal/websearch/websearch.go:120-122`（默认 client）；`internal/provider/openai/openai.go:133`；`$DSH/dsh-http-proxy/README.zh.md`。
- 影响：同一环境里“搜索能通、抓取不通”，行为不一致且无设置项；企业用户无法使用 fetch。反过来若简单放开也会破坏 SSRF 防护（需在代理策略里保留目标校验，或对代理目标二次校验）。
- 建议修复：S：加设置 `network.proxy`（不设=默认环境变量），web_fetch 在显式代理时对 CONNECT 目标做同样的 public-address 校验/或提示风险；至少在 known-limitations 记录该行为差异。

### [FP-20] 文档漂移：旧矩阵/known-limitations/brief 三处把「已实现」写成缺失 · P2 · NEW

- 现象（三处矛盾，影响排期判断）：
  1. `docs/FUNCTIONAL-PARITY.md:13` 写“Auto-review 缺失”；实际已实现：`internal/runtime/review.go:1-80`（LLMToolReviewer，15s 超时、拒绝回退人工审批）、`app/services.go:856-884`（permission==auto_review 时构造 reviewer + ReviewContext），commit `5970e3b` “fix: auto-review parity”。
  2. `docs/FUNCTIONAL-PARITY.md:18` 写“goal … 无自动 round 驱动”、`00-brief.md:177` 写“goal round driver 缺失”；实际 `session/goal_driver.go` 早于该文档存在，`app/services.go:123` 安装、`session/controller.go:1543-1553` 每回合终态触发，默认 6 轮/上限 12。
  3. `docs/design/known-limitations.md:28` 写计划模式“尚未做：独立 exit_plan_mode 工具、审阅卡 Keep planning（带反馈）分支、pending 态徽章”；实际全部存在：`tools/exit_plan_mode.go:48`、`shell/plan_ui.go:19-33,259-270,388-440`（Keep planning + feedback + pending 徽章）。
  另：`known-limitations.md:32` 的 `@` 提及/会话引用缺口仍成立（KNOWN 有效）。
- 影响：Lead/产品据此排期会重复投入已完成项，或误判能力边界；“已实现但文档说没有”会让 verifier 和用户失去信任。
- 建议修复：S：以本报告 §4 修正三处；后续建立「能力清单单一真源」（可由 agent 从 `app/services.go` 注册表 + `runtime.Modes()` 生成）。

### [FP-21] 零散实验性/周边能力缺口 · P3 · NEW

- voice input：仅插件页占位行（`shell/plugins_ui.go:67`），详情弹窗明示“此版本未内置 SenseVoice 依赖”（`shell/plugin_details.go:363-368`）；DSH 有完整实验 bundle（录制 + 本地 SenseVoice STT + 远端 API STT）。P3（实验性且 ZeoKeel 已明示）。
- CoT 翻译：无（DSH `dsh-experimental-translator` / `-cot-translation`，中文用户看思考内容有实际价值）。
- 右栏能力：无 browser tab（DSH 沙箱内 HTTP(S) 预览含 loopback）、无 Document preview（Markdown/代码/图片/PDF/Office/HTML）、无 open-in-app（DSH 解析本机编辑器/终端/Git GUI）；ZeoKeel 有 Files/Terminal/Session Log（`shell/start_tab.go`），`shell/shortcuts.go:169` 自述 open-in-app 未实现。
- 会话统计/轮次大纲：DSH `dsh-session-stats`（计数/墙钟投影）与 `dsh-session-turn-outline`（整会话轮次导航），ZeoKeel 只有 Session Log 只读面板。
- 工具差异小项：无 `load_workspace_dependencies`（Python/Node/pnpm 路径下发）、无 `native-command`（无 shell 的原生命令/path 打开、WSL 路径交接）、无 `tool-cordis`（插件运行时只读 API）；ZeoKeel `inspect` 是自研替代（`tools/inspect.go:17-36` 有自述）。

## 3. 已验证良好的方面

1. **Goal round driver 真实且接线**：`session/goal_driver.go`（默认 6 轮、上限 12、每回合终态只触发一次、Stop 抑制、新目标重置预算）+ `app/services.go:123` + `controller.go:1543-1553`；老文档说缺失是错的。
2. **Auto-review 已实现且语义接近 DSH**：`runtime/review.go` 有 DSH 风格的五分区（固定策略/cwd/AGENTS.md/用户请求/待审动作）、拒绝为“建议”会回退人工审批、15s 超时回退；与 DSH 差异仅“不含过滤后历史分区”（`review.go:20` 自述）。
3. **Plan mode 已完整对齐**：`/plan` 命令 + 软引导 policy + composer 徽章 + `exit_plan_mode` 工具 + 审阅卡（Approve/Exit/Keep planning 带 feedback）/pending 徽章（`shell/plan_ui.go`），且与只读权限正交。
4. **安全默认值总体不错**：Full 模式外默认 Workspace Write、审批按“能否逃逸工作区”分级（ADR 0010，`app/services.go:886-900`）、str_replace_editor `view` 免审、web_fetch 有 DNS-rebinding 后校验 + 私网拒绝 + 重定向逐跳校验（`tools/webfetch.go:49-100`）。
5. **上下文保护**：压缩在回合 goroutine 内执行不阻塞 UI、20s 摘要超时回退 WindowCompactor（`runtime/compaction_llm.go`）、工具结果 32KB spill 落盘并保留 100 份、软预算 advisory（步数/token）只提醒不硬停。
6. **工具面覆盖广**：fs/search/editor/shell/terminal/jobs/web/todo/ask/present/goal/schedule/skill/subagent/team/workflow/ralph/worktree/ptc/settings/memory/session-query/report/inspect/MCP 全部有实现，绝大部分有单测（71 个 tools 文件）。

## 4. 已知问题复核（KNOWN 状态是否仍成立）

| 来源 | 旧结论 | 现状复核 | 结论 |
|---|---|---|---|
| `FUNCTIONAL-PARITY.md:13` | Auto-review 缺失 | 已实现：`runtime/review.go`、`app/services.go:856-884`、commit `5970e3b` | ❌ 旧结论失效 |
| `FUNCTIONAL-PARITY.md:18` / `00-brief.md:177` | goal round driver 缺失 | 已实现且默认启用：`session/goal_driver.go`、`app/services.go:123`、`controller.go:1543-1553` | ❌ 旧结论失效 |
| `known-limitations.md:28` | plan mode 三点未做（exit_plan_mode / Keep planning 反馈 / pending 徽章） | 三者均存在：`tools/exit_plan_mode.go:48`、`shell/plan_ui.go:19-33,259-270,388-440` | ❌ 旧结论失效 |
| `FUNCTIONAL-PARITY.md:19` | Schedule ✅ | 工具有、调度器有，但语义降级（新建会话/cron 子集/无时区） | ⚠️ 降级为部分成立（FP-07） |
| `FUNCTIONAL-PARITY.md:16` | Agent 模式（含 PTC）✅ 已对齐 | 模式路由对齐，但 PTC 语义降级为批处理 | ⚠️ 部分成立（FP-02） |
| `FUNCTIONAL-PARITY.md:12` | 工具集“⚠️ 基本齐” | 工具数量接近，但 workflow/subagent/MCP 传输/持久 shell 等语义缺口 | ⚠️ 数量结论成立、语义结论高估 |
| `FUNCTIONAL-PARITY.md:22` / `known-limitations.md:36` | 多模态/附件 ✅ | 图片输入成立；文件附件是文本内联 64KiB，非 DSH 持久附件语义 | ⚠️ 部分成立（FP-14） |
| `known-limitations.md:30` | `/goal`、`/compact` 有意未做 | 仍成立（`composer_slash.go:66-146` 无二者）；但 `/goal` 缺失的根因是 goal 工具/UI 半成品（FP-04） | ✅ 状态成立，原因需更新 |
| `known-limitations.md:32` | `@` 提及 P1 未做、会话引用纯文本 | 仍成立（无 session reference 解析器） | ✅ 仍成立 |
| `known-limitations.md:65` | 运行中统计/turn_end/spill 已实现 | 代码确认（`session/turn_end_test.go`、`session/tool_spill.go`） | ✅ 成立 |
| `00-brief.md:174-176` | 单 workspace root、插件仅内置、无更新 | 仍成立（`app/workspace_*` 单 root；无 update 代码） | ✅ 仍成立 |

## 5. 未验证/可疑项

- **未验证**：PTC/Workflow 的模型实际使用效果与 token 收益（需真实模型多回合，成本受限未跑）；`fp-01/02` 的“语义降级”判断基于 schema/工具描述与 DSH 文档，非行为基准测试。
- **未验证**：DSH 安装包是否有 ZeoKeel 没有的隐藏能力（本审计只看 305 个包的 README，不读 DSH 源码实现；README 声称对齐度以文档为准）。
- **未验证**：`known-limitations` 提及的 `Auto review EXP 保留更宽复核范围` 与代码一致（`app/services.go:49-56` autoReviewApprovalTools 列表），但 UI 上 auto_review 插件开关“仅存偏好”与权限模式的关系可能让用户困惑（`plugin_details.go:374-376` 自述）——属 UX 范畴，未展开。
- **待验证**：`session.<id>.goal_rounds` 是否有任何 UI 入口设置（grep 未见；可能只能通过 `settings_set` Creator 工具/DB）。若确认无入口，goal round 预算对普通用户不可控，可并入 FP-04 升级。
- **可疑**：`store/session.go` LIKE 搜索对中文无分词（`ESCAPE '\'` 处理正确），若会话量上万可能明显变慢；本环境无大数据集，未做基准。
- **未展开**：permission 语义中内部常量 `PermissionAsk="ask"` 被用作 UI 的「只读」模式（`app/services.go:553`、`composer_slash.go:754`），用户选「只读」实际注册的是无写工具的 Ask 权限；工作区写模式下不逐次审批（ADR 0010 有意设计）。是否与 DSH 预设命名产生长期混淆，建议 Lead 决策，不计为缺陷。
- **未验证**：`report` 工具与 UI 的 deliverables 卡片在真实会话中的呈现（代码路径存在，UI 细节归 uiux-auditor）。

## 6. 优先级建议（本维度 Top 8，按投入产出）

1. **P1 先补「语义降级」的文档与矩阵**（FP-01/02/03/20，S+文档 0.5d）：把 workflow/PTC/subagent/附件/schedule 的降级在 FUNCTIONAL-PARITY 里标 ⚠️ 并写原因，避免继续误导排期；顺带修正 auto-review/goal driver/plan mode 三处过时结论。
2. **P1 Goal 用户闭环**（FP-04，M，2-3d）：goal 模型 + revision/phase + `/goal` 命令 + goal 徽章；这是长任务可用性的最短路径，round driver 已就绪只差入口。
3. **P1 子代理异步化**（FP-03，M，3-5d）：把 agentteam 异步能力合并进 `subagent`（返回 childId + 完成通知 + 续谈），DSH 对齐最大收益项。
4. **P1 workspace changes/turn diff**（FP-05，M，2-3d）：编码 agent 的反馈闭环，影响所有写代码场景的可审阅性。
5. **P2 会话与上下文质量**（FP-09/10/11，M，3-4d）：FTS5 检索 + token meter/图片卸载 + 三屏障 checkpoint；与 runtime-auditor/arch-auditor 可能有交叉，建议 Lead 去重后合并排期。
6. **P2 交付/自动化语义**（FP-07/14/15，M，3-5d）：schedule 投回原会话 + 附件持久化/按路径读取 + job 完成通知/团队任务板。
7. **P2 供应层安全与健壮性**（FP-08/12/13，M，2-4d）：原子写 + read-before-write、请求重试、MCP streamable-http、secret mask。
8. **P3 实验性/周边**（FP-18/19/21，S-L）：headless CLI 最小版、代理设置、voice/CoT 翻译/浏览器侧栏等按产品节奏取舍。

> 说明：本报告发现已与 `docs/FUNCTIONAL-PARITY.md` 旧矩阵去重（旧矩阵不重复列举为 NEW 的：插件市场/三平台打包/自动更新——产品既定边界，不计功能缺口；`/goal`、`/compact` 命令缺失为 KNOWN 保留）。
