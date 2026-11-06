# 功能级 1:1 对齐审计（宏观视角，2026-11-05）

> 结论：**核心 agent 已经可用**（真实 LLM 流式对话、工具循环、审批、多会话、子代理、MCP、技能、定时任务均跑通）；
> 但**功能上与 DSH 未做到一比一**。此前的 UI 像素/交互微循环不应再作为主目标，先补功能缺口。
> 2026-11-05 审计修复轮复核：此前标为缺失的 **Auto-review、Goal round driver、plan mode 三项增强** 经代码复核已实现，本表随之更正（缺陷跟踪见 [BUGS.md](BUGS.md)）。
> **2026-11-06 统一收口轮**（[FIX-REPORT-CLOSURE](audit/FIX-REPORT-CLOSURE.md)）：补齐工作区 inline diff、`/goal`、`/compact`（会话级持久压缩）、`@` 提及细节、会话引用原子化、崩溃中断回合修复、ACP 完整面、LSP 工具、Wayland 全局快捷键、导入器收尾、Chat 流式思考展示与折叠（DSH ReasoningRow 对齐）；并**修正 auto-memory / computer-use 的 DSH 归属**——经核实 DSH 0.2.1-rc.2 安装包内无对应包/能力，二者不是对齐缺口（详见 FIX-REPORT-CLOSURE §0）。

## A. 核心 agent 能力

| 能力 | DSH | ZeoKeel | 状态 |
|---|---|---|---|
| 流式对话 + 多轮历史 | 有 | 有（SSE、SQLite history、e2e 真实模型通过） | ✅ |
| 工具循环（ReAct） | 有 | 有（eino ADK，真实模型工具调用 e2e 通过） | ✅ |
| 工具集 | 约 37 个（fs/bash/terminal/jobs/pwsh/web/todo/goal/schedule/skill/subagent/subagent-control/workflow/ralph/present/ask_user/session-query/worktree/inspect/report/MCP…） | 约 35 个：新增 `lsp`（definition/references/implementation/hover，stdio server，安全门 + 插件页开关）；**computer-use 经核实 DSH 无该工具包（非对齐项）**；**PTC 已移除（产品决定明确不做）**；Creator 设置工具已接入；shell 栈按平台门控（[ADR 0009](decisions/0009-platform-shell-stacks.md)） | ✅ |
| 审批/权限 | Full/Workspace/Read-only/Ask/Auto-review EXP | Full/Workspace/Read-only/Ask + Always allow；**Auto-review EXP 已实现**（`internal/runtime/review.go` 的 `LLMToolReviewer`，15s 超时回退人工；app 接线 + 审批 UI 测试） | ✅ 基本对齐 |
| 上下文压缩 | 自动压缩 + 工具结果裁剪 + LLM 摘要 + 手动 `/compact` | `LLMCompactor`（真实模型摘要 + 20s 摘要超时后回退 `WindowCompactor` + 旧工具结果裁剪），压缩在回合 goroutine 内执行；**新增 `/compact` 手动压缩**：会话级**持久替换**（store 事务替换早期历史为 summary、清理被替换回合 tool_calls、ordinal 重排、`state.Compaction` 事件幂等回放），重开/后续回合不回退；忙碌/空历史/无压缩器稳定文案 | ✅ 已对齐 |
| 模型配置与选择 | 内置供应商/模型目录（DeepSeek 等），模型带上下文窗口与推理等级；composer 模型 + 推理强度菜单，按会话生效 | 内置 `internal/modelcatalog`（DeepSeek、OpenAI、Moonshot AI(CN)、Z.AI、Groq、Together、OpenRouter、Ollama）；添加模型只需填 API key，端点/模型/上下文窗口/默认强度按目录自动填；composer 模型菜单新增推理强度段（Home 暂存、Session 持久化，下一回合生效）；模型上下文窗口自动决定该会话压缩预算（80%） | ✅ 已对齐（OpenAI-compatible 路由） |
| 崩溃恢复/checkpoint | checkpoint + 断点续跑 + interrupted-turn 收尾 | `CheckpointStore` 已接 SQLite：每成功回合写入；**新增崩溃中断回合修复 consumer**：requested/tool_pending checkpoint → 为已派发无结果的工具补 unknown-outcome（不重放副作用）+ assistant 中断锚点 + 按 turn 去重 marker + checkpoint 落 completed，二次恢复 no-op，用户 Stop 不误判（对齐 DSH interruptedTurnClosers 语义） | ✅ 已接线 |
| 工作区变更 / 每回合 diff | workspace changes 卡片 + 逐文件内容对比（3 行上下文，binary/oversized/coarse 标记；Host 内存态，重开丢失） | **inline diff**：回合开始基线 + 结束统一 diff，payload 随 `workspace/changes` 事件**持久化**（重开可回放）；限额单文件 1MiB/2000 行/64KiB、每回合 100 文件/256KiB（truncated 标记）；UI 默认折叠、可展开 +/- 着色、点击打开文件 | ✅ 对齐（持久化强于 DSH） |
| 会话引用 | `@` 引用其他会话 → canonical mention + 受限只读快照作为不可信上下文注入（≤3 来源/单源预算/omitted 计数） | `@[label](dsh-session:<base64url(id)>)` 精确往返；prompt 后**第二条 user 消息** `## Referenced sessions`（固定不可信警告 + tag-safe JSON + 每源 ≤8KiB + ≤3 来源 + omitted）；持久化/幂等/重开回放；Chat 折叠为紧凑引用 disclosure（不显示原始 JSON、不产生幻影 turn） | ✅ 对齐（composer 明文 mention，无内联芯片——mygo TextArea 限制，已记录） |
| 流式思考展示/折叠 | ReasoningRow：思考 disclosure + 单行 shimmer preview + 展开全文 + 过程组折叠 | 每轮「思考」行：running 单行实时 preview（最后完成段落首行）+ 透明度脉冲；完成「已思考」默认折叠；点击/Enter 展开 mono 全文；展开态按消息保持；无 reasoning 不占位；最终答案永远可见；工具 summary 默认折叠沿用「何时收起工作详情」设置 | ✅ 对齐（差异：无 settled preview、shimmer 为透明度脉冲、正文 mono） |
| ACP | v1 完整面（list/load/authenticate/fs/terminal/permission） | `session/list`、`session/load`（历史回放）、`authenticate`（默认 no-op/可配门禁）、server→client `fs/read|write_text_file`、`terminal/create|output|wait_for_exit|kill|release`（超时/能力门控 fail-closed）、`session/request_permission` 四选项映射 ApprovalQueue；同步控制 handler 内发起 client 请求返回明确错误 | ✅ 对齐（client 侧仍由编辑器提供；无 client 能力时稳定错误） |
| LSP | 可选包 `dsh-lsp`/`dsh-lsp-stdio`/`dsh-tool-lsp` | 新 `internal/lsp`（JSON-RPC stdio、initialize/didOpen、definition/references/implementation/hover、UTF-16 坐标、输出 ≤16KiB/位置 ≤50、超时 kill）；工具 `lsp` + `plugin.lsp.enabled` 插件页开关 + `lsp.servers`/`.zeokeel/lsp.json`（默认 off，需 allow）；inspect 可读配置键 | ✅ 对齐（冷启动 server、无连接池，已记录） |
| Agent 模式 | **Standard / PTC / Minimal / Creator** | **Standard / Minimal / Creator**（工具集/prompt 策略/切换重建已对齐；Deep/Supervisor/Plan-Execute 仅旧会话别名）；**PTC ❌ 明确不做**（2026-11-06 用户决策，模式与入口已移除） | ⚠️ 3/4（PTC 不做） |
| 子代理 / Agent Team | 同步+异步、消息/中断、team 工具 | 同步 subagent + 异步 manager/team 工具 | ✅ 基本 |
| Goal（跨回合推进） | goal round driver 自动多轮 + `/goal` 命令 | **round driver 已实现**（回合预算 6/cap 12）；goal 工具 + `/goal` 命令（status/create/edit/pause/resume/complete/abandon/clear，本地执行不进模型；edit 保持同一 goal id/phase/rounds 不重置预算） | ✅ |
| Schedule（模型侧） | schedule_* 工具 | 有，落 automation 表，app 调度器执行 | ✅ |
| 后台 jobs | job_* 工具 | 有（start/list/output/kill，wait） | ✅ |
| Skills / MCP | 有（tools/resources/prompts） | 有（tools/resources/prompts） | ✅ |
| 多模态 | 图片输入 + 附件 | 图片工具→content parts 注入；**用户附件上传已实现**（文本注入 prompt、图片 data URL content part、chip 持久化/删除清理） | ✅ |
| 语音输入 | 有（实验性） | **❌ 明确不做（产品决定，2026-11-06）**：无 UI/入口，不列入能力缺口 | ❌ |
| 会话标题 | 自动/LLM 标题、conversation-following titles | 自动/LLM 标题（首条完成回合；失败/停止后下个成功回合补）+ header/sidebar 实时刷新；手改优先 | ✅ |
| Fork / 分支 / 导出 | fork session、下载 session log | `Store.ForkSession` 复制会话/消息/事件/工具调用/反馈/附件（附件行换新 id、message_id 重映射、字节复制到 fork 自己的 `attachments/<fork>/`，删除任一侧互不影响；标题加 “ (fork)”），Session More 菜单可 fork；`Controller.ExportSession` 导出 session/messages/attachments/events/feedback JSONL，附件记录内嵌 base64 数据与文本正文，下载走保存对话框 | ✅ 本地 |
| 反馈 | 服务端反馈传输 | 反馈行（message_id/rating/note/created_at）持久化，重开回显、随导出；仍无联网上送 | ⚠️ 本地持久化 |
| 自动记忆/AGENTS.md | AGENTS.md（经核实 DSH 0.2.1-rc.2 安装包无 memory/auto-memory 包或能力） | AGENTS.md 注入 + **memory 工具 + Settings › Memory 页**（按 workspace 持久、跨会话注入、可增删改查/清空） | ✅ 不落后（auto-memory 非 DSH 能力，不列为缺口） |
| 插件 | 内置 + 第三方市场 | 仅内置（产品决定） | 设计差异 |
| 打包/更新 | 三平台安装包 + 自动更新 | Windows zip + **Linux tar.gz（amd64/arm64）+ macOS unsigned `.app` zip**（`make package-*`，版本注入已接入）；签名/公证/自动更新仍缺 | ⚠️ 部分 |

## B. 典型 E2E 场景（验收矩阵）

| 场景 | 现状 |
|---|---|
| 新建会话 → 真实模型对话 → 工具调用 → 审批 Allow/Deny/Always | ✅ 已通过（真实端点 e2e） |
| 多轮上下文 + 压缩后继续对话 | ✅ 超预算自动 LLM 摘要 + 工具结果裁剪；摘要失败/超时回退 WindowCompactor |
| 进程重启后继续未完成回合 | ✅ 已完成回合冷启动恢复；**进行中回合**：requested/tool_pending checkpoint 被消费为幂等「中断收尾」（工具补 unknown-outcome、不重放副作用、UI 显示已中断），不自动续跑（与 DSH interruptedTurnClosers 同语义） |
| 子代理/Team 并行任务 + 消息/中断 | ✅ 工具层通过（同步/异步） |
| Goal 多轮自动推进 | ✅ round driver 自动续轮（默认预算 6，cap 12）；`/goal` 命令可取状态/创建/编辑/暂停/恢复/完成/放弃/清除 |
| 手动压缩 `/compact` | ✅ 会话级持久替换：早期历史变 summary，重开/后续回合不回退；忙碌/空历史稳定提示 |
| 工作区变更 diff 回放 | ✅ 逐文件 inline diff（+/- 着色、binary/oversized/coarse 标记）随事件持久化，重开仍可展开 |
| 跨会话引用 | ✅ canonical mention + `## Referenced sessions` 只读快照注入（不可信警告/有界/幂等），Chat 折叠显示 |
| 定时任务到点执行 | ✅ 已验证 |
| MCP 服务器工具/resources/prompts | ✅ 测试通过（stdio 假服务器） |
| 图片理解 | ✅ 真实模型识图通过 |
| 上传文件/附件给 agent | ✅ 文本附件进入 prompt（≤64 KiB），图片作为 data URL content part 发送；chip 持久化、删除 chip/会话时清理 |
| 会话 fork/导出/反馈 | ✅ fork 复制历史（含附件行与文件字节，续聊仍重放文本/图片）并打开；JSONL 导出（保存对话框）含附件记录与内容；反馈行持久化、重开回显、随导出 |
| Composer 斜杠命令 / `@` 提及 | ✅ 对齐 DSH：分组面板（添加/指令）、图标+本地化标题+命令名+右对齐说明、键入过滤、↑↓/Tab/Enter/Esc；`/file`（附件 picker / 工作区路径校验）、`/permission`、`/export`（JSONL）、`/feedback good|bad`、`/plan`、`/model`、`/goal`、`/compact`、`/clear`、`/stop`、`/help`、`/new`、`/settings`、`/automate`、`/plugins`；`@` 文件/目录下钻（Tab/chevron）、`@"带空格路径"`（含引号/反斜杠转义）、面包屑多级回跳、跨工作区会话候选（同 cwd 优先）与 canonical 会话引用（原子 mention + 有界只读上下文）；未知 `/` 行按文档作为普通消息发送 |

## C. 优先级建议（停止 UI 微循环，按此顺序）

> 已完成：Agent 模式语义对齐；真 LLM 摘要压缩 + 工具结果裁剪；checkpoint 接线（回合边界恢复）；会话标题自动生成（首条完成回合 / LLM，实时刷新）；用户附件上传（文本/图片进入会话，持久化与清理）；会话 fork / JSONL 导出 / 本地反馈持久化。

P0（核心体验/数据安全）：
1. ~~会话标题自动生成（首条消息/LLM）~~ ✅ 已完成
2. ~~用户附件上传（文件/图片进入会话）~~ ✅ 已完成
3. ~~会话 fork + 导出 session log + 本地反馈持久化~~ ✅ 已完成（反馈无联网上送）

P1（能力完整）：
4. ~~Goal round driver（自动多轮）+ `/goal` 命令~~ ✅ 已完成（round driver + `/goal`/`/goal edit` 本地命令）
   Plan mode 已按 DSH 对齐为软引导 + `/plan` 命令 + composer 徽章 + `exit_plan_mode` 工具 / Keep planning / pending 徽章 / Home `/plan`（[ADR 0008](decisions/0008-plan-mode-soft-alignment.md)）✅
5. ~~Auto-review 权限（模型审前审批）~~ ✅ 已完成（`LLMToolReviewer` + 审批 UI）
6. ~~auto-memory~~ → 经核实 DSH 0.2.1-rc.2 无 memory/auto-memory 包（非对齐项）；ZeoKeel 已有 memory 工具 + 设置页，AGENTS.md 注入保留
7. ~~`/compact` 手动压缩~~ ✅ 已完成（runtime 手动压缩 API + 会话级持久替换）
8. ~~工作区 inline diff / 会话引用 / ACP 完整面 / LSP 工具 / 崩溃中断收尾 / 导入器附件迁移 / Wayland 快捷键~~ ✅ 已完成（统一收口轮，见 [FIX-REPORT-CLOSURE](audit/FIX-REPORT-CLOSURE.md)）；LSP/Wayland 真机项见 known-limitations

P2（产品化）：
9. 三平台安装包~~（Linux/macOS 已做 unsigned 版）~~/签名/自动更新（外部依赖，DEFERRED）
10. 性能与资源基线（启动、内存、长会话；思考行 streaming 预览已缓存，见 FIX-REPORT-CLOSURE）

> 每项完成后跑对应 E2E 场景，而不是以“像素 diff/死控件扫描”作为完成标准。
