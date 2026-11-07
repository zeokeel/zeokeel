# 能否直接引用 Rig 最新版拼出 `old/` 的全部功能？

> 审计时间：2026-11-07 · Rig 版本：`github.com/zeokeel/zeokeel-rig v0.12.1`（发布 tag）
> 对照对象：`/data/mygo/old`（旧 ZeoKeel Agent，eino 运行时 + mygo UI）
> 结论：**能直接拼出 `old/` 的 agent 内核与工具层；不能直接拼出 `old/` 的全部功能。**
> 全部功能里约 2 万行（~22%）可被 Rig 直接替代，约 6.8 万行（~78%）是产品层（UI/会话持久化/
> ACP/热键/导入/更新器…），Rig 不提供，只能由产品仓保留并改接 Rig 契约。

## 1. 最新版核对

| 项 | 值 |
|---|---|
| 远端最新 tag | `v0.12.1`（本地与 `origin/main` 均为 `bf90d73`，tag 之后的 main 提交只有文档改动） |
| `version.go` | `const Version = "0.12.1"` |
| go.mod | `require mvdan.cc/sh/v3 v3.14.1`（唯一依赖，仅 sandbox 策略用） |
| 规模 | 81 静态工具 + server-bound MCP/LSP + 动态 `mcp__<server>__<tool>` + opt-in `run_code`(PTC) |
| 新增能力（v0.9–v0.12） | `kit` 一次导入门面、异步 subagent/team、typed schedule、goal revision-CAS、后台 jobs/terminal/bash、PTC、中文文档/`cmd/rigdoc` 机器可读工具目录 |

## 2. 直接引用可行性验证（实测）

在全新空模块中只引用发布版：

```sh
mkdir /tmp/rig-v0121-parity && cd /tmp/rig-v0121-parity
go mod init parity
GOPROXY=direct go get github.com/zeokeel/zeokeel-rig@v0.12.1
go build && ./parity
```

实测输出（spike 源码见本目录 `spike/`）：

```
1) kit.New: ok (default tools assembled, no API key needed)
2) full.All: 81 static tools
3) agent.Run: finish=stop iterations=1 messages=2 (tools available to the loop)
4) kit.RunText: "hello from kit" finish=stop iterations=1
```

- 构建成功，离线运行成功（fake provider 注入，不需要 API key）。
- 实际 import 依赖只有 `github.com/zeokeel/zeokeel-rig` + `mvdan.cc/sh/v3/{syntax,fileutil}`；
  没有 eino、没有 mygo、没有 SQLite。
- `go list -m all` 还会列出 mvdan 的 test-only 模块（`go-quicktest` 等），它们不进入二进制。

**结论：引用最新版本身没有问题；问题是 Rig 的职责边界。**

## 3. `old/` 功能面 → Rig 覆盖矩阵

`old/internal` 生产代码 **89,978 行**，测试 **93,965 行**。逐包对照：

| old 包 | 行数 | 旧职责 | Rig v0.12.1 对应 | 结论 |
|---|---:|---|---|---|
| `tools` | 12,267 | 58 个工具实现（fs/shell/terminal/jobs/web/todo/goal/schedule/skill/subagent/workflow/ralph/present/ask/session-query/worktree/inspect/report/MCP/LSP…） | `tools/*` 全部包；旧工具名 58/58 已覆盖（`scripts/audit-legacy-tools.sh` 通过） | ✅ **直接替代**（可删旧实现） |
| `runtime` | 6,434 | eino ADK 封装：agent 循环、审批队列/LLM reviewer、checkpoint、压缩、modes、settings、多模态、subagent | `agent` + `kit` + `gate` + `sandbox` + `tools/subagent`；**无 checkpoint/压缩/审批队列/LLM reviewer/settings/modes** | ⚠️ 循环/gate/subagent 可替代；其余产品层重写 |
| `provider/openai` | 1,193 | OpenAI 兼容 adapter + model adapter | `provider/openai` | ✅ 直接替代 |
| `session` | 8,840 | 会话控制器：回合、审批、plan mode、goal driver、自动标题、压缩、附件、fork/export/feedback、tool spill | `session.Memory` 仅 `rig.History`；`SessionLog` 只是契约 | ❌ 产品层 |
| `store` | 5,887 | SQLite 持久化：sessions/messages/events/attachments/checkpoints/automation/permissions/feedback/fork/export | 无持久化实现（仅契约） | ❌ 产品层 |
| `shell` | 25,229 | mygo UI：chat/composer/面板/设置/审批 UI… | 无 UI | ❌ 产品层 |
| `app` | 8,153 | 组合根、启动、settings、automation、ACP adapter、审批接线 | 无进程宿主/装配（README 明确） | ❌ 产品层 |
| `globalhotkey` | 3,869 | 全局快捷键 | 无 | ❌ 产品层 |
| `importer` | 2,341 | 导入器/凭据迁移/附件 | 无 | ❌ 产品层 |
| `acp` | 1,863 | ACP server（list/load/fs/terminal/permission） | 无 ACP | ❌ 产品层 |
| `prompt` | 1,398 | system prompt 组合、plan mode、memory 注入、registry | 只有 `AgentSpec.SystemPrompt` 字符串 | ❌ 产品层 |
| `state` | 1,345 | 事件 projector、compaction 事件、broker | 无 | ❌ 产品层 |
| `lsp` | 1,347 | LSP client + definition/references/implementation/hover + config | `tools/lsp` 有 hover/definition/diagnostics/symbols + legacy `lsp`；缺 references/implementation 与配置管理 | ⚠️ 大部分替代 |
| `mcp` | 1,171 | MCP server 管理（stdio/HTTP、resources/prompts） | `tools/mcp` 客户端（stdio+HTTP、tools/resources/prompts/templates） | ✅ 客户端替代；配置产品侧 |
| `workspacechanges` | 1,134 | 工作区 diff 卡片 | 无 | ❌ 产品层 |
| `memory` | 1,059 | 抽取/整合/双层 scope | `tools/memory` + `rig.MemoryStore` 契约 | ⚠️ 工具/存储替代；抽取整合产品 |
| `skills` | 772 | 多根 skill 加载 | `tools/skill` + FileStore（多目录 SKILL.md） | ✅ 大部分 |
| `agentteam` | 733 | 同步/异步 team 管理 | `tools/team` + 异步 subagent/team | ✅ 大部分 |
| `sessionreference` | 713 | 跨会话引用 | 无 | ❌ 产品层 |
| `updater` | 678 | 自动更新 | 无 | ❌ 产品层 |
| `presets` | 645 | 预设 | 无 | ❌ 产品层 |
| `i18n` | 560 | 国际化 | 无 | ❌ 产品层 |
| `automation` | 398 | cron scheduler + 任务执行 | `tools/schedule` + `rig.Scheduler` 契约 | ⚠️ CRUD 替代；定时器/执行产品 |
| `modelcatalog` | 357 | 供应商/模型目录 | 无（只 OpenAI-compatible provider） | ❌ 产品层 |
| `brand`/`design`/`icons` | 316/733/87 | 品牌/设计系统/图标 | 无 | ❌ 产品层 |
| `websearch` | 302 | HTTP 搜索后端 | `tools/web` 的 `web_search` 需要 `web.Searcher` 实现 | ⚠️ 接口替代，实现需搬迁 |
| `winproc` | 154 | Windows 进程辅助 | 无 | ❌ 产品层 |

### 量化（按包归类估算）

| 归类 | 行数 | 占比 |
|---|---:|---:|
| Rig 可直接提供/替代（工具层 12.3k + provider/openai + runtime 循环部分 + lsp/mcp 客户端 + skills/memory/automation/agentteam 的存储与工具部分等） | ≈ 20,000 | ≈ 22% |
| 产品层（shell/session/store/app/acp/热键/导入/更新器/prompt/state/…) | ≈ 69,978 | ≈ 78% |
| 合计 | 89,978 | 100% |

> 说明：runtime 的 6.4k 里约 2k 是 Rig 已覆盖的循环/门禁/子代理/多模态部分，其余
> checkpoint/压缩/审批队列/LLM reviewer/settings/modes 属于产品层，上表按可替代部分计入。

## 4. 关键缺口：`old` 的"功能级 parity"里哪些 Rig 不提供

对照 `old/docs/FUNCTIONAL-PARITY.md`：

| 能力 | Rig 提供 | 产品还需做 |
|---|---|---|
| 流式对话 / 多轮历史 | ✅ `agent` + `History` | SQLite 持久化（`store` 5.9k） |
| 工具循环 / 审批 / 权限模式 | ✅ `agent` + `gate` + `sandbox` | 审批 UI/队列（`runtime`/`shell`） |
| 上下文压缩（自动 + `/compact`） | ❌ | `LLMCompactor`/`WindowCompactor` + 持久替换（`runtime`/`session`/`store`） |
| checkpoint/崩溃恢复 | ❌ | `CheckpointStore` + interrupted-turn 修复（`runtime`/`store`/`app`） |
| 模型目录 / 推理强度 / 预算 | ❌（只有 provider 适配器） | `modelcatalog` + 设置 UI |
| Auto-review（LLM tool reviewer） | 仅 `Reviewer` 接口 + `tools/review` 工具 | 审批链路里的 `LLMToolReviewer` 接法 |
| Goal round driver | 只给 goal 工具 + revision-CAS | 自动续轮 driver（`session/goal_driver`） |
| 工作区 diff / 会话引用 / fork / export / 标题 / 反馈 | ❌ | `workspacechanges`、`sessionreference`、`session`、`store` |
| 附件 / 多模态 prompt | ✅ `ToolResult.Parts` + provider | 用户附件上传/回放（`session`/`prompt`/`shell`） |
| ACP | ❌ | `acp` server（1.9k） |
| LSP | ⚠️ 4 类工具 + client | references/implementation、`lsp.servers` 配置与插件开关 |
| 自动记忆 / AGENTS.md | 仅 memory 工具 + 契约 | 抽取/整合/审核 inbox（`memory` 1.1k + UI） |
| 插件 / 自动更新 / 打包 | ❌ | `shell`/`app`/`updater`/`packaging` |
| PTC `run_code` | ✅ opt-in（系统 Python/Node 运行时） | 旧产品决定不做；如要做需重新评估 |

## 5. 结论

1. **"直接引用最新版"完全可行**：`go get ...@v0.12.1`，唯一依赖 `mvdan.cc/sh/v3`，
   `kit.New` / `full.All` + `agent.New` 即可装出 agent 内核与 81 个工具（spike 已跑通）。
2. **"拼出 old 全部功能"不可行（仅靠 Rig）**：Rig 是纯 SDK，README 明说不含 UI、进程宿主、
   模块装配、持久化；`old/` 约 78% 的代码是这些产品层。要恢复 old 全部功能，产品仓必须保留
   shell/session/store/app/acp/… 并实现 Rig 的宿主契约（`History`、`SessionLog`、`Settings`、
   `Scheduler`、`Asker`、`Presenter`、`PlanController`、`GoalStore/Updater`、`MemoryStore`、
   `SkillStore`、`Reviewer`、`NoticeSource`、`web.Searcher`、MCP/LSP 客户端配置）。
3. **能"直接拼"出的是 old 的 headless agent 能力**：流式对话、工具循环、审批 gate、全部工具、
   编排（subagent/team/workflow/ralph）、schedule/goal、后台 jobs/terminal、PTC；
   这部分可以直接用 Rig 替换 `old/internal/{tools,runtime,provider/openai,lsp,mcp}` 的实现。

## 6. 推荐装配架构

```
产品仓（ZeoKeel）
├─ cmd/zeokeel            thin main → zeokeel-kernel 装配（保留）
├─ internal/agenthost     kit 或 full.All + agent.New：provider/openai + 全部工具 + gate
│                         + session（实现 rig.History）
├─ internal/sessionstore  SQLite 实现 rig.History / SessionLog / Settings / MemoryStore /
│                         SkillStore / Scheduler / TeamTaskManager（替换 old/store+session 数据层）
├─ internal/agentruntime  checkpoint / compaction / approval queue / auto-review / goal driver
│                         （old/runtime 的产品逻辑，改为消费 rig.Event / rig.Gate / rig.NoticeSource）
├─ internal/prompt+state  system prompt 组合、事件 projector、workspace diff、会话引用
├─ internal/shell         mygo UI（保留，事件流改为 rig.Event；审批面板实现 rig.Gate 阻塞）
├─ internal/host          Asker/Presenter/PlanController/GoalStore 的 UI 实现（注入 full.All）
├─ internal/acp           ACP server（调用 rig agent）
└─ old 中可删除：internal/tools、internal/runtime（eino）、internal/provider/openai、
                internal/lsp（改用 tools/lsp）、internal/mcp（改用 tools/mcp）、internal/agentteam
```

装配代码骨架（spike 已验证同类代码可编译运行）：

```go
policy := sandbox.New(sandbox.WithRoots(root), sandbox.WithCommandAllowlist("git", "go", "ls"))
host := hosttools.NewMemoryHost()                       // 产品换成真实 UI host
devices := full.All(full.AllOptions{
    Policy: policy, Root: root,
    Host: hosttools.Options{Asker: host, Presenter: host, Plan: host, Goal: host, Interactive: true},
    History: sqliteHistory, SessionLog: sqliteLog,     // 产品实现
    Settings: settings, Scheduler: scheduler,
    Runner: agent.NewRunner(rig.AgentSpec{Provider: provider, Model: model, Tools: nil}, agent.RunnerOptions{}),
    Reviewer: review.NewSubagentReviewer(runner),
})
a, err := agent.New(rig.AgentSpec{Provider: openaiProvider, Model: model, Tools: devices, History: sqliteHistory, Gate: approvalGate})
```

> 注意：`kit.New` 不暴露 Host/Runner/MCP/Searcher，要 full 能力必须像上面这样用 `full.All` 显式装配。

## 7. 下一步（可选）

- **A（推荐，1–2 天）**：做一个"headless old-parity"验证程序：SQLite 实现 rig.History/SessionLog +
  prompt/state + 审批 gate + full.All，跑通"多轮对话 + 工具 + 审批 + 压缩前/后"最小闭环，证明
  产品层可以在 Rig 上重建。
- **B（1 天）**：输出逐包迁移清单与工作量估算（shell/session/store/app/acp…），排迁移顺序。
- **C（按需）**：补齐 Rig 侧旧功能残差（LSP references/implementation、`Searcher` 后端适配、
  skills 锚点），但压缩/checkpoint/ACP 等不属于 SDK，不建议塞进 Rig。

## 附录 A：spike 复现

见本目录 `spike/{main.go,go.mod,go.sum}`：

```sh
cd spike && GOPROXY=direct go mod download && go run .
# 期望：kit.New ok / full.All 81 / agent.Run stop / kit.RunText hello from kit
```
