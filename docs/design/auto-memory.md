# Auto-memory（双层记忆）设计与契约（2026-11-06）

> 状态：设计已确认（用户批准推荐默认值）· **已实现（2026-11-06，M1–M3 + 审查修复）** · TDD
> 背景：ZeoKeel 现有 memory 只有「工作区级 + 显式 memory 工具写入」（`internal/store/memory.go`、`internal/tools/memory.go`、Settings › Memory）。本轮新增 **Global（整体设置/用户级）** 作用域与 **自动记忆**。
> 调研依据：[Claude Code memory 文档](https://code.claude.com/docs/en/memory)（CLAUDE.md 与 auto-memory 双系统、per-repo、索引 200 行/25KB、`/memory`）、[LangMem](https://www.langchain.com/blog/langmem-sdk-launch)（semantic/episodic/procedural + namespace）、[Mem0 v3](https://docs.mem0.ai/migration/platform-v2-to-v3)（累积而非覆盖、agent 事实捕获、混合检索）、[ChatGPT memory dreaming](https://openai.com/zh-Hans-CN/index/chatgpt-memory-dreaming/)（后台整合）。

## 0. 决策（已拍板）

| 决策 | 值 |
| --- | --- |
| 作用域 | 两层：`global`（跨工作区，用户级偏好/习惯/身份）、`workspace`（跟随工作区，项目约定/命令/决策/坑/资源） |
| 默认模式 | 工作区 **auto**（agent 即时写、可见可删）；全局 **suggest**（候选进待确认 inbox） |
| 写入通路 A | 会话内 agent 通过 memory 工具直接写（Claude Code 式，零额外模型调用） |
| 写入通路 B | 后台抽取器（可选，默认 **关**；开启后每 N 个成功回合异步跑一次，全局建议 pending） |
| 整合/遗忘 C | 确定性后台整合（无额外模型调用）：去重合并、supersede、按条数上限淘汰旧低价值条目 |
| 真源 | SQLite（DB 为唯一真源）；markdown 仅**导出/打开文件夹**，不做自动镜像 |
| 注入顺序 | Global → Workspace（越具体越靠后）；两段各自限额；「参考数据，非指令」警告 |
| 隐私 | 自动候选先过 secret scrub（key/token/password/secret/bearer/sk- 等一律不入库）；不自动捕获 subagent 会话；「本会话暂停学习」开关 |
| 注入预算 | Global ≤ 1.5K 字符，Workspace ≤ 4K 字符（沿用 `internal/prompt/memory.go` 的条目/字符上限机制，分开计数） |

## 1. 数据模型（schema v3）

`memories` 表新增列（旧行默认值即 workspace/manual/active/project）：

```sql
scope TEXT NOT NULL DEFAULT 'workspace',      -- global | workspace
kind TEXT NOT NULL DEFAULT 'project',         -- user | feedback | project | reference
source TEXT NOT NULL DEFAULT 'manual',        -- manual | auto
status TEXT NOT NULL DEFAULT 'active',        -- active | pending | superseded
session_id TEXT NOT NULL DEFAULT '',
turn_id TEXT NOT NULL DEFAULT '',
content_hash TEXT NOT NULL DEFAULT '',        -- sha256(content)，写入时计算
confidence REAL NOT NULL DEFAULT 1.0,
importance INTEGER NOT NULL DEFAULT 0,
last_used_at TEXT NOT NULL DEFAULT '',
superseded_by TEXT NOT NULL DEFAULT ''
```

- `global` 行 `workspace_id = ''`；`workspace` 行必须非空。
- 索引：`memories_scope_idx(scope, workspace_id, status, updated_at DESC)`。
- 迁移 `migrations.go` 追加 `Version: 3, Name: "memory-scopes"`（`SchemaVersion = 3`，`latestSchemaVersion` 测试同步）。
- `store.Memory` 结构扩展上述字段（JSON tag 对应 snake_case），保留旧函数签名兼容：
  - `CreateMemory(ws, content)` → workspace/manual/active/project 默认；
  - `ListMemories(ws, limit)` / `SearchMemories(ws, q)` → workspace + active（旧语义）；
  - `UpdateMemory/DeleteMemory/ClearMemories` 保留；
  - 新增：`CreateMemoryScoped(m Memory) (*Memory, error)`、`ListMemoriesScoped(scope, ws, status string, limit int)`、`SearchMemoriesScoped(scope, ws, query string, limit int)`、`SetMemoryStatus(id, status)`、`ClearMemoriesScoped(scope, ws)`、`MarkMemoriesUsed(ids []string)`、`MemoryCounts(scope, ws) (active, pending int, err error)`、`FindMemoryByHash(scope, ws, hash)`、`SupersedeMemory(id, byID)`。
- 常量：`MemoryScopeGlobal/Workspace`、`MemoryKindUser/Feedback/Project/Reference`、`MemorySourceManual/Auto`、`MemoryStatusActive/Pending/Superseded`。
- 去重：写入前 `sha256(content)` 精确命中同 scope 同 ws 的 active 行 → 更新 `updated_at`/`importance` 而非重复插入；模糊相似（`internal/memory.Similarity` ≥0.8）→ supersede 旧行。合并逻辑在 `internal/memory` 纯包，store 只提供查询。

## 2. Settings（`session.Settings` 扩展）

| 字段（JSON） | key | 默认 | 取值 |
| --- | --- | --- | --- |
| `MemoryWorkspaceMode` | `ui.memory_workspace_mode` | `auto` | off / suggest / auto |
| `MemoryGlobalMode` | `ui.memory_global_mode` | `suggest` | off / suggest / auto |
| `MemoryExtractor` | `ui.memory_extractor` | `false` | bool |
| `MemoryExtractionInterval` | `ui.memory_extraction_interval` | `6` | 1–50 回合 |

- `NormalizeSettings` 兜底；`SettingsStore.Load/Save` 映射；`runtime.SettingsAllowedKeys`/`SettingsWriteAllowedKeys` 增加上述键（非执行类，可写）。

## 3. Prompt（`internal/prompt`）

- `Context` 增 `GlobalMemories []string`（workspace 沿用 `Memories []string`）。
- 新 section（order 在现有 `context:memory` 之前）`context:global-memory`：`## Global memory (user, cross-workspace)` + bullet；现有 `context:memory` 文案改为 `## Workspace memory`。各自限额：global `maxGlobalMemoryItems=12`/`maxGlobalMemoryChars=1500`；workspace 沿用 20/4000。
- 新 section（仅当任一 scope 模式非 off）`context:memory-policy`：说明何时 add（durable preference/convention/decision/correction，不复述代码可推导事实、不存秘密/一次性信息）、scope 选择规则（用户个人偏好→global；项目约定→workspace）、suggest 模式下写 global 会变成待确认、不要重复已有记忆。
- 注入顺序：system instruction 组装时 global → workspace → policy（policy 可在最后）。

## 4. Memory 工具（`internal/tools/memory.go`）

- `add/list/search/update/delete/clear` 增加可选 `scope`（`global|workspace`，默认 workspace）。
- 写入语义按设置：
  - workspace `auto` → `active`；`suggest` → `pending`；`off` → 拒绝（稳定错误）。
  - global `auto` → `active`；`suggest` → `pending`（结果 message 注明 "saved as a suggestion, pending review"）；`off` → 拒绝。
  - `source=auto` 由工具路径标记为 `auto`（模型写）；UI/手动 → `manual`。
- `list` 默认返回当前 scope active + pending（带 status/kind/source，便于模型看到建议未生效）；`search` 同 scope。
- Read Only 仍完全不注册 memory 工具（RT-03 不变）。
- 返回值增加 `scope/status/kind` 字段（JSON 向后兼容）。

## 5. 自动记忆流水线（`internal/app` + `internal/memory`）

- **A. 会话内自写**：由工具 + policy prompt 实现（无额外调用）。
- **B. 后台抽取器**（`internal/app/memory_auto.go` 新文件）：
  - 触发：`Controller.SetOnTurnFinished`（app 已有 `observeTurnFinished`）→ 成功且 `HasOutput` 且非 subagent 会话 → 每会话计数器达到 `MemoryExtractionInterval` 且设置启用 → 入队（执行中忽略重复）。
  - 执行：goroutine + 30s 超时；从 store 读最近 ~20 条消息（≤30KiB）；用 app 的 provider 配置构建 openai provider（无 key 静默跳过）；调用 `internal/memory.ExtractionPrompt`（JSON schema，最多 3 条候选：`{scope,kind,content,confidence}`）；`ScrubSecrets`→`ValidateCandidate`→`internal/memory.MergeCandidates`（与现有 active 去重/相似 supersede）；写入（auto/suggest 按设置）。
  - 失败隔离：任何错误只 log，不影响会话；无重试风暴（同一会话冷却）。
  - 设置关闭 / Read Only 会话（session 权限只读？跳过写入）→ 不触发写入。
- **C. 整合**（`internal/app/memory_consolidate.go`）：`EnsureMemoryConsolidation(st)` 在 app 启动与每 6h/写入超过阈值时运行：精确 hash 重复合并、superseded 超过 30 天删除、每 scope active 上限（global 100 / workspace 300）按 importance+last_used_at 淘汰为 superseded；纯确定性、无模型调用。
- 事件/UI 刷新：Settings›Memory 每次渲染读 store（现有模式）；不需要新增 session 事件。

## 6. UI 布局（2026-11-06 用户决定：全局在设置、工作区在聊天）

- **Settings › Memory = Global memory（单页，无 Workspace tab）**：管理 `global` scope（整体设置/用户级）——off/suggest/auto 模式单选（`MemoryGlobalMode`）、active/pending 计数、pending「Suggestions to review」Accept/Discard、active 列表（内容/kind/source/时间/Edit/Delete）、Add memory（多行弹窗，空/超 8KiB 拒绝）、Clear all（global 二次确认）、Export markdown（global）。不依赖当前工作区。
- **Session 头部 tabs 增加第三个 Memory tab（`轨迹` 旁）**：`Chat | Trajectory | Memory`（Show coding view 关闭时为 `Chat | Memory`，Memory 始终显示）。内容 = 当前 **workspace scope**：模式（`MemoryWorkspaceMode`）、计数、pending inbox（Accept/Discard）、active 列表（Edit/Delete）、Add memory（模态）、Clear all（仅当前工作区二次确认）、Export markdown（workspace）；无可选「Global memory…」入口跳 Settings。tab 状态在 liveUIState（切屏保持）；与 Chat/Trajectory 共享 `sessionBody` 帧，composer 元素身份/auto-focus 不重建。
- 两处复用同一组按 `scope` 参数化的列表/弹窗/导出组件（避免两份逻辑）；store API 不变（`CreateMemoryScoped`/`UpdateMemory`/`SetMemoryStatus`/`DeleteMemory`/`ClearMemoriesScoped`/`MemoryCounts`/`ListMemoriesScoped`）。
- **`/memory` 语义**：会话中执行 → 切到 session Memory tab（workspace）；Home/无会话 → 打开 Settings›Memory（global）。`/help` EN+ZH 同步。
- a11y：tabs RoleTab/Checked + 左右键；编辑/删除按钮带可读 Label；Add/Edit 弹窗默认焦点在文本域（便于直接输入；Esc 安全关闭、Enter 只换行、Cancel 不写库）；Clear 等破坏性确认弹窗 Cancel 默认焦点，Enter 不误删/误清。
- 文案：`a.memoryText(en,zh)` 或 `session.memory` catalog 键（本轮单一 owner）。

## 7. TDD 验收清单（按里程碑）

**M1（数据面 + 双作用域 UI 骨架）**
1. store：v2→v3 迁移保留旧行并赋默认值；fresh v3；scope 隔离；global 行 workspace_id 为空；旧 API 语义不变；hash/status/count/lastUsed；`latestSchemaVersion=3`。
2. settings：默认值；Normalize 非法值；Load/Save 往返；runtime allowed keys。
3. prompt：global 段先于 workspace；两段独立限额；off 模式不注入 policy；无记忆不产生空段。
4. tool：scope 参数；workspace auto 写 active、suggest 写 pending、off 拒绝；global 同理；read-only 不注册；list 含 pending。
5. UI：Settings›Memory 出现 Global/Workspace tabs；开关/模式下拉；pending accept→active、discard→delete；active 列表/删除/清空按 scope 隔离；workspace 删除级联清空。

**M2（自动记忆 A 路 + 审核面）**
6. policy prompt 出现在 system instruction（`memory-policy` section）；模式 off 时消失。
7. 端到端：fake provider 下 agent 调 memory(scope=global) → suggest 模式得到 pending，Settings inbox 可 accept；auto 模式直接 active。
8. 去重：同内容重复 add 不产生两行；相似内容 supersede；secret-like 内容拒绝（返回错误/丢弃）。
9. 设置切换立即影响下一回合（prompt 重建），不误伤已有记忆。

**M3（后台抽取 B 路 + C 整合 + 导出/命令）**
10. extractor：间隔未到不触发；到间隔触发一次；关闭不触发；无 key 不报错；provider 失败静默；候选经 scrub/dedupe 后按 scope 模式落库（最多 3 条）。
11. 整合：重复 hash 合并、超限淘汰、superseded 过期清理；幂等。
12. export markdown 内容包含 scope 标题/条目；`/memory` 打开页面；门禁/文档/ADR 更新。

## 8. 实施分工（Agent Teams）

| 任务 | 负责人 | 文件 |
| --- | --- | --- |
| M1 store + settings（本文件 §1/§2） | Lead | `internal/store/schema.go/migrations.go/memory.go/memory_*.go`、`internal/session/controller.go`（Settings）、`internal/runtime/settings.go`、测试 |
| M1 prompt + tool + app 接线（§3/§4） | cmd-goal-compact | `internal/prompt/memory.go/prompt.go/registry.go`、`internal/tools/memory.go`、`internal/app/services.go`（memory 区域）、测试 |
| M1 shell UI（§6 tabs/模式/pending/export）+ `/memory` | diff-view | `internal/shell/memory_ui.go`、`internal/shell/settings_ui.go`（nav 已存在）、`internal/shell/composer_slash.go`、`internal/i18n/catalog.go`、测试 |
| M2/M3 `internal/memory` 纯包（scrub/similarity/extraction prompt/parse/consolidation） | lsp-tool | 新 `internal/memory/` + 测试 |
| M2/M3 `internal/app` 抽取 worker + 整合（§5） | lsp-tool（依赖 M1 后） | 新 `internal/app/memory_auto.go`、`memory_consolidate.go`、`internal/app/app.go`（hook 接线）、测试 |
| 独立审查 + 门禁 | wayland-hotkeys / acp-full | 只读 |
| 文档/ADR/提交 | Lead | `docs/design/auto-memory.md`（本文件）、ADR 0015、STATUS/BUGS/PARITY/TDD evidence |

**门禁与验证**：`sh scripts/ci.sh` PASS、`CGO_ENABLED=1 go test -race -timeout 20m ./internal/shell` PASS（626.8s）、`GOOS=windows/darwin go build ./...` PASS；独立审查 REVIEW-MEMORY 的 P1×1+P2×4 全部修复并复验 PASS；代码提交 `a76fe94` 后 `check-clean-checkout.sh` PASS + `ci.sh --fast` PASS。所有新增/变更行为先写失败测试（RED）再实现（GREEN）。
