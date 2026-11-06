# 功能级 1:1 对齐审计（宏观视角，2026-11-05）

> 结论：**核心 agent 已经可用**（真实 LLM 流式对话、工具循环、审批、多会话、子代理、MCP、技能、定时任务均跑通）；
> 但**功能上与 DSH 未做到一比一**。此前的 UI 像素/交互微循环不应再作为主目标，先补功能缺口。
> 2026-11-05 审计修复轮复核：此前标为缺失的 **Auto-review、Goal round driver、plan mode 三项增强** 经代码复核已实现，本表随之更正（缺陷跟踪见 [BUGS.md](BUGS.md)）。

## A. 核心 agent 能力

| 能力 | DSH | ZeoKeel | 状态 |
|---|---|---|---|
| 流式对话 + 多轮历史 | 有 | 有（SSE、SQLite history、e2e 真实模型通过） | ✅ |
| 工具循环（ReAct） | 有 | 有（eino ADK，真实模型工具调用 e2e 通过） | ✅ |
| 工具集 | 约 37 个（fs/bash/terminal/jobs/pwsh/web/todo/goal/schedule/skill/subagent/subagent-control/workflow/ralph/present/ask_user/session-query/worktree/inspect/report/MCP…） | 约 34 个，覆盖除 LSP/computer-use 外全部（**PTC 已移除：产品决定明确不做**）；Creator 设置工具已接入；shell 栈按平台门控（Windows `pwsh` / POSIX `bash`，对齐 DSH，见 [ADR 0009](decisions/0009-platform-shell-stacks.md)） | ⚠️ 基本齐 |
| 审批/权限 | Full/Workspace/Read-only/Ask/Auto-review EXP | Full/Workspace/Read-only/Ask + Always allow；**Auto-review EXP 已实现**（`internal/runtime/review.go` 的 `LLMToolReviewer`，15s 超时回退人工；app 接线 + 审批 UI 测试） | ✅ 基本对齐 |
| 上下文压缩 | 自动压缩 + 工具结果裁剪 + LLM 摘要 | `LLMCompactor`（真实模型摘要 + 20s 摘要超时后回退 `WindowCompactor` + 旧工具结果裁剪），ContextBudget 24000，app 已接线；压缩在回合 goroutine 内执行，不阻塞 UI/Submit | ✅ 已接线 |
| 模型配置与选择 | 内置供应商/模型目录（DeepSeek 等），模型带上下文窗口与推理等级；composer 模型 + 推理强度菜单，按会话生效 | 内置 `internal/modelcatalog`（DeepSeek、OpenAI、Moonshot AI(CN)、Z.AI、Groq、Together、OpenRouter、Ollama）；添加模型只需填 API key，端点/模型/上下文窗口/默认强度按目录自动填；composer 模型菜单新增推理强度段（Home 暂存、Session 持久化，下一回合生效）；模型上下文窗口自动决定该会话压缩预算（80%） | ✅ 已对齐（OpenAI-compatible 路由） |
| 崩溃恢复/checkpoint | checkpoint + 断点续跑 | `CheckpointStore` 已接 app 的 SQLite checkpoints 表：每成功回合写入（压缩后 prior + 本回合消息），独立冷启动可恢复；生产以 History 为准，checkpoint 作为镜像 | ✅ 已接线（回合边界） |
| Agent 模式 | **Standard / PTC / Minimal / Creator** | **Standard / Minimal / Creator**（工具集/prompt 策略/切换重建已对齐；Deep/Supervisor/Plan-Execute 仅旧会话别名）；**PTC ❌ 明确不做**（2026-11-06 用户决策，模式与入口已移除） | ⚠️ 3/4（PTC 不做） |
| 子代理 / Agent Team | 同步+异步、消息/中断、team 工具 | 同步 subagent + 异步 manager/team 工具 | ✅ 基本 |
| Goal（跨回合推进） | goal round driver 自动多轮 | **round driver 已实现**（`internal/session/goal_driver.go`，app 默认接线，回合预算 6/cap 12）；goal 工具 + 状态/进度；`/goal` 命令行仍缺（缺 Controller 写入 API，见 BUGS FP-04） | ⚠️ 基本 |
| Schedule（模型侧） | schedule_* 工具 | 有，落 automation 表，app 调度器执行 | ✅ |
| 后台 jobs | job_* 工具 | 有（start/list/output/kill，wait） | ✅ |
| Skills / MCP | 有（tools/resources/prompts） | 有（tools/resources/prompts） | ✅ |
| 多模态 | 图片输入 + 附件 | 图片工具→content parts 注入；**用户附件上传已实现**（文本注入 prompt、图片 data URL content part、chip 持久化/删除清理） | ✅ |
| 语音输入 | 有（实验性） | **❌ 明确不做（产品决定，2026-11-06）**：无 UI/入口，不列入能力缺口 | ❌ |
| 会话标题 | 自动/LLM 标题、conversation-following titles | 自动/LLM 标题（首条完成回合；失败/停止后下个成功回合补）+ header/sidebar 实时刷新；手改优先 | ✅ |
| Fork / 分支 / 导出 | fork session、下载 session log | `Store.ForkSession` 复制会话/消息/事件/工具调用/反馈/附件（附件行换新 id、message_id 重映射、字节复制到 fork 自己的 `attachments/<fork>/`，删除任一侧互不影响；标题加 “ (fork)”），Session More 菜单可 fork；`Controller.ExportSession` 导出 session/messages/attachments/events/feedback JSONL，附件记录内嵌 base64 数据与文本正文，下载走保存对话框 | ✅ 本地 |
| 反馈 | 服务端反馈传输 | 反馈行（message_id/rating/note/created_at）持久化，重开回显、随导出；仍无联网上送 | ⚠️ 本地持久化 |
| 自动记忆/AGENTS.md | AGENTS.md + auto-memory | AGENTS.md 注入；无 auto-memory | ⚠️ |
| 插件 | 内置 + 第三方市场 | 仅内置（产品决定） | 设计差异 |
| 打包/更新 | 三平台安装包 + 自动更新 | Windows zip + **Linux tar.gz（amd64/arm64）+ macOS unsigned `.app` zip**（`make package-*`，版本注入已接入）；签名/公证/自动更新仍缺 | ⚠️ 部分 |

## B. 典型 E2E 场景（验收矩阵）

| 场景 | 现状 |
|---|---|
| 新建会话 → 真实模型对话 → 工具调用 → 审批 Allow/Deny/Always | ✅ 已通过（真实端点 e2e） |
| 多轮上下文 + 压缩后继续对话 | ✅ 超预算自动 LLM 摘要 + 工具结果裁剪；摘要失败/超时回退 WindowCompactor |
| 进程重启后继续未完成回合 | ⚠️ 已完成回合可从 checkpoint/History 冷启动恢复；进行中回合仍不续跑 |
| 子代理/Team 并行任务 + 消息/中断 | ✅ 工具层通过（同步/异步） |
| Goal 多轮自动推进 | ✅ round driver 自动续轮（默认预算 6，cap 12；`/goal` 命令行仍是缺口） |
| 定时任务到点执行 | ✅ 已验证 |
| MCP 服务器工具/resources/prompts | ✅ 测试通过（stdio 假服务器） |
| 图片理解 | ✅ 真实模型识图通过 |
| 上传文件/附件给 agent | ✅ 文本附件进入 prompt（≤64 KiB），图片作为 data URL content part 发送；chip 持久化、删除 chip/会话时清理 |
| 会话 fork/导出/反馈 | ✅ fork 复制历史（含附件行与文件字节，续聊仍重放文本/图片）并打开；JSONL 导出（保存对话框）含附件记录与内容；反馈行持久化、重开回显、随导出 |
| Composer 斜杠命令 / `@` 提及 | ✅ 对齐 DSH：分组面板（添加/指令）、图标+本地化标题+命令名+右对齐说明、键入过滤、↑↓/Tab/Enter/Esc；`/file`（附件 picker / 工作区路径校验）、`/permission`（read-only/workspace/full/auto 写入）、`/export`（JSONL）、`/feedback good|bad`、`/plan`、`/model`、`/clear`、`/stop`、`/help`、`/new`、`/settings`、`/automate`、`/plugins`；`@` 文件/目录下钻（Tab 进入、Esc 逐级上返）与会话引用；`/goal`、`/compact` 因写入/API 缺口有意不加行（见 known-limitations），未知 `/` 行按文档作为普通消息发送 |

## C. 优先级建议（停止 UI 微循环，按此顺序）

> 已完成：Agent 模式语义对齐；真 LLM 摘要压缩 + 工具结果裁剪；checkpoint 接线（回合边界恢复）；会话标题自动生成（首条完成回合 / LLM，实时刷新）；用户附件上传（文本/图片进入会话，持久化与清理）；会话 fork / JSONL 导出 / 本地反馈持久化。

P0（核心体验/数据安全）：
1. ~~会话标题自动生成（首条消息/LLM）~~ ✅ 已完成
2. ~~用户附件上传（文件/图片进入会话）~~ ✅ 已完成
3. ~~会话 fork + 导出 session log + 本地反馈持久化~~ ✅ 已完成（反馈无联网上送）

P1（能力完整）：
4. ~~Goal round driver（自动多轮）~~ ✅ 已完成（`goal_driver.go` 接线）；剩余 `/goal` 命令行（需 Controller 写入 API）
   Plan mode 已按 DSH 对齐为软引导 + `/plan` 命令 + composer 徽章 + `exit_plan_mode` 工具 / Keep planning / pending 徽章 / Home `/plan`（[ADR 0008](decisions/0008-plan-mode-soft-alignment.md)）✅
5. ~~Auto-review 权限（模型审前审批）~~ ✅ 已完成（`LLMToolReviewer` + 审批 UI）
6. auto-memory / AGENTS.md 深化

P2（产品化）：
7. 三平台安装包~~（Linux/macOS 已做 unsigned 版）~~/签名/自动更新
8. 性能与资源基线（启动、内存、长会话）

> 每项完成后跑对应 E2E 场景，而不是以“像素 diff/死控件扫描”作为完成标准。
