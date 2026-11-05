# 独立验证 E：产品面复核 + PD-04 DSH 导入器验收

> 验证者：product-auditor · 时间：2026-11-05 23:30–00:05 · 基线：HEAD `6da09e3`（验证期间从 `da12d44` 移动到 `6da09e3`；wave 1 提交 `1da4f12`，随后 `da12d44` 文档、`0bbf051` 补提交 importer 凭据文件、`6da09e3` 文档）
> 方法：只读代码审查 + `go test` + 真实 `/root/.dsh` 只读 dry-run/隔离导入 + Xvfb 真跑 GUI（隔离 data dir、`unshare -n`、精确 PID）+ 单测
> 构建：`/tmp/zkv/zeokeel`（构建自 `da12d44`，含 wave 1）与 `/tmp/zkv/zeokeel-w2`（构建自工作树 `6da09e3` + wave 2 未提交改动，23:40 构建通过）
> 证据：`.cache/audit/product-verify/logs/`（import dry-run JSON、DSH 会话元数据扫描、测试输出）、`.cache/audit/product-verify/shots/`（截图 43 张）
> 限制：未使用真实模型；无 macOS/Windows 真机；wave 2 在工作树中仍在进行（63 个文件变更/未提交），其验收仅到「代码 + 单测 + 冒烟截图」层级；未逐个 GUI 复跑全部 wave 2 页面。**未修改 /root/.dsh、未导入到用户真实 data dir、未打印任何凭据值。**

## 0. 结论摘要

- **PD-04（DSH 导入器）**：实现真实、CLI 可用、安全边界正确（只读 DSH、凭据只读键名、zstd 缺失显式报错、坏会话隔离），但**数据质量不足以按「迁移器」对外宣称**：真实数据 325 个会话中 89% 的 DSH `user/message` 是**系统注入上下文**（`source.kind != "user"`），导入器未过滤 → 会变成可见的「用户气泡」；assistant 消息 59,431 条中 95.7% 是 step 级 reasoning/tool-call，导入后 transcript 被拆成大量步骤行；图片 26 处静默丢失；重复导入会重复；无 UI 入口（committed HEAD）。
- **结论（PD-04）**：**可作为「最小导入（技术预览 CLI）」发布**（需文档写明范围 + 建议先 `--dry-run`）；**不可作为「迁移器」发布**，需补 5 项（§2.6）。README 的新定位（「最小迁移已可用；设置页 UI 入口仍待做」）与 `docs/STATUS.md:9`（仍写「CLI/UI 未接线」）自相矛盾，需同步。
- **wave 1 产品修复**：PD-01（键盘发送错误反馈 + 空会话回滚）**CONFIRMED**；PD-02（provider 预设 + 测试连接）**CONFIRMED**，但发现 **PARTIAL 级残余**：无 key 的本地 provider（Ollama 预设）被 agent 构建器判为「未配置」；PD-03（单实例/二次启动/多 data dir）**CONFIRMED**；PD-09/08/11/15/17/18 代码+测试确认。
- **wave 2 产品项**（PD-05/06/07/10/12）：**工作树已落地、单测通过、冒烟截图可用**，但验证时**尚未提交**（HEAD 不含）→ 判 **PARTIAL（待提交后复核）**。
- **过程发现**：wave 1 提交 `1da4f12` 自身**不可构建**（importer 引用的 `InspectCredentials` 所在文件被 `.gitignore` 的 `credentials*` 规则漏提交，`0bbf051` 才补上）→ 发布流程需要在最终 commit 上跑全量门禁。

## 1. 方法与基线

| 项 | 值 |
| --- | --- |
| 验证时 HEAD（开始） | `da12d44`（wave 1 提交 `1da4f12` 之后） |
| 验证时 HEAD（结束） | `6da09e3`（`0bbf051` 补 importer 凭据文件；`6da09e3` 文档） |
| 工作树状态 | wave 2 进行中：63 个 modified/untracked 文件（settings_extra/manage_ui/session_restore/automation_runs/autostart/network_proxy…），`go build ./...` 通过；已构建 `/tmp/zkv/zeokeel-w2` 做冒烟 |
| 真实 DSH 数据 | `/root/.dsh` 411MB sessions、325 个 `session.v4.jsonl.zstd`、5 个 workspace bucket、`zstd 1.5.5` |
| dry-run 安全 | 全程 `--dry-run`/只读；真实导入只写 `/tmp/zkv/data-real`；凭据仅输出键名（已做无泄漏断言） |
| 关键命令 | 见补充证据 `.cache/audit/product-verify/evidence-notes.md` |

## 2. A 部分：DSH 导入器验收（PD-04）

### 2.1 实现与单元测试

| 检查项 | 结果 | 证据 |
| --- | --- | --- |
| 包结构 | `internal/importer`：`Probe`/`ParseSessionFile`/`ParseSession`/`Import`/`InspectCredentials`/`zstd` 支持；CLI 接线 `cmd/zeokeel/import_cli.go`（`zeokeel import [--from] [--dry-run] [--json] [--max] [--include-subagents]`） | `internal/importer/importer.go:75,249`；`session.go:110`；`import_credentials.go:20`；`cmd/zeokeel/cli.go:68` |
| 单测 | `go test ./internal/importer` ✅ 10/10 PASS（布局/凭据/v4 解析/dry-run 不写 store/真实写库/subagent 跳过与包含/zstd/torn line/缺 DSH） | `.cache/audit/product-verify/logs/importer-tests.txt` |
| 凭据安全 | `InspectCredentials` 只取 `refs:` 下的键名；值从不进入报告/日志；测试含「value 不得出现在报告结构」断言；真实文件 `drwx------ dir + 0600 file`，另验证 0644 时输出 `credentials file ... group/world accessible` 警告 | `import_credentials.go:20-90`；`logs/import-fake-creds-mode.txt`（fake 值未打印） |
| zstd 缺失 | 显式 `ErrZstdUnavailable` 逐会话失败 + 报告 warning + exit 1，不静默跳过 | `logs/import-no-zstd.json` |
| 只读 DSH | Probe/Parse 只 `os.Open`/`ReadDir`；不写 `/root/.dsh`；CLI 帮助明确「Quit the desktop app before importing」 | 代码审查；dry-run 后 `ls -la /root/.dsh` 无变化 |

### 2.2 真实数据只读探测（`/root/.dsh`，325 会话）

| 指标 | 默认（跳过 subagent） | `--include-subagents` |
| --- | --- | --- |
| sessions_found | 325 | 325 |
| sessions_imported | 27 | 312 |
| sessions_skipped | **298**（91.7%） | 13（空会话） |
| sessions_failed | **0** | 0 |
| messages_imported | 17,219 | 65,242 |
| tool_calls_seen / tool_results_seen | 16,815 / 17,026 | 73,383 / 74,631 |
| compressed_input | true（325/325 zstd） | true |
| credentials（仅键名） | `DEEPSEEK_API_KEY` | 同左 |
| workspaces | 5（`/data/Plot`、`/data/ZeoKeel*`、`/data/mygo`，全部 destination_exists=true） | — |
| 输出含凭据值/`sk-` 明文 | **无**（grep 断言） | — |

- 全量 dry-run 耗时 ~25s（325 次 zstd 子进程 + 解析），可接受。
- 真实数据元数据扫描（`.cache/audit/product-verify/logs/dsh-session-scan.json`）：`user/message` 共 **6,025** 条，其中 `source.kind == "user"` 仅 **652** 条（10.8%）；其余 5,373 条为 `time-context`(1,578)、`agent-message`(1,496)、`subagent-settled`(697)、`tool-jobs`(506)、`agent-instructions`(388)、`runtime-context`(366)、`goal`(150)、`skill-catalog`(115)、`compact-checkpoint`(51) 等系统注入；另有 **26** 个 `image` content part、`system/message` 319。
- assistant/message 共 **59,431**：`assistant_with_text` 2,535，`assistant_with_reasoning` 59,431，其中 **56,887** 条只有 tool-call（无 text）。

### 2.3 真实导入（隔离目录，`--max 8`）

- `ZEOKEEL_DATA_DIR=/tmp/zkv/data-real zeokeel import --from /root/.dsh --max 8 --json` → 2 imported / 6 skipped / 0 failed，704 messages；自动创建 workspace `Plot` 并链接 `/data/Plot`（DB 验证：1 workspace、2 sessions、704 messages、0 events、0 tool_calls）。
- 会话标题保留 DSH 标题（如「移动优先的AI小说创作工具」），GUI 打开后 Chat 渲染原对话（`shots/10-session-open.png`）；Trajectory 无内容（events=0）——符合「messages 为真源、事件未导入」的设计，但**Usage/Trajectory/回合终态/tool 卡在导入会话中缺失**。
- 消息形状：2 个会话共 631 assistant / 73 user；**610/704 条消息包含合成的 `[tool call] bash(...)` 文本**；assistant 行 612 条带 reasoning —— step 级拆分明显。

### 2.4 支持矩阵

| 能力 | 状态 | 说明/证据 |
| --- | --- | --- |
| `session.v4.jsonl.zstd` | ✅ | 325/325 真实会话；zstd 外部二进制（缺失→显式失败） |
| 明文 `session.v4.jsonl` / v3/旧命名 | ⚠️ 文件名接受，**版本未校验** | `sessionFileIn` 接受 v3·v4·通用名；`sessionHeader.Version` 解析后未使用（未来 v5 会静默按 v4 解析） |
| subagent 子会话 | ✅ 默认跳过 / `--include-subagents` 导入 | 真实数据 298 个 subagent 默认跳过（91.7%） |
| 多 workspace | ✅ 按 session header cwd 归并；不存在则创建 workspace；dry-run 提供 `destination_exists` | 5 个 workspace 全部映射正确 |
| 凭据 | ✅ 只读键名，从不读值；提示用户重填 | 报告含 `DEEPSEEK_API_KEY`（name only） |
| 会话标题/用户与 assistant 文本/reasoning | ✅ | 标题取 `session/title`，否则首条 user 消息截断 |
| tool calls / tool results | ❌ 不持久化（计入计数 + 合成为 assistant 文本行） | 真实数据 73,614 calls / 74,631 results 丢弃 |
| 图片/附件 | ❌ 丢失且报告不可见 | 真实数据 26 个 image part；`Session.Images` 统计但 `Report` 无字段 |
| profiles / storages / 附件对象 | ❌ 不做（文档说明「最小导入」） | `doc.go` |
| 系统注入上下文过滤 | ❌ **未过滤**（`source` 字段未解析） | §2.5 风险 1 |
| 幂等/去重 | ❌ 无（重复 import 会重复创建会话） | `importSession` 每次 `CreateSession`，无 DSH session id 映射 |
| UI 入口 | ⚠️ committed HEAD 无；工作树已有 `TestImportFromDSHDialog`（wave 2，未提交） | `cmd/zeokeel/import_cli.go` 为 CLI |
| App 运行中导入保护 | ⚠️ 仅帮助文案提示，无单实例锁检测 | `import_cli.go:36` 文案；未检查 `instance.lock` |

### 2.5 导入完整性风险（按严重度）

1. **系统注入上下文变成「用户消息」**（P1，导入质量）：`userMessageData` 未解析 `source`（`session.go:89` 注释即写明 `{content,role,source,id}`，结构体却省略 source），所有 `user/message` 事件都写成 user 行。真实数据 89% 的 user 事件是 `time-context`/`runtime-context`/`agent-instructions`/`subagent-settled`/`goal` 等注入 → 导入后 transcript 出现大量「Current runtime context…」「Time sampled…」等假用户气泡（`shots/10-session-open.png` 可见），与真实对话混杂。
2. **step 级 assistant 消息导致 transcript 膨胀**（P1）：DSH 每个 step 一条 assistant/message（含 reasoning / tool-call）；导入后 469 条消息的会话里多数是「[tool call] bash(...)」合成行（真实数据 610/704）。用户看到的是按 step 拆散的对话而非按回合聚合的会话。
3. **重复导入无幂等**（P1，数据风险）：无 DSH session id 记录/去重，`import` 跑两次会得到两套会话（含附件交互）；建议在 settings 存 `dsh_import.<dsh-session-id>` 或 sessions 表加 `source` 列（或导入前检测标题+时间重复并提示）。
4. **图片静默丢失**（P2）：26 个 image part 被丢弃，报告无 `images_seen` 字段；用户无法得知。
5. **skip 原因不可见**（P2）：`sessions_skipped=298` 只给数字，未区分 subagent / 空会话 / 无 cwd；用户会以为数据丢失。
6. **导入会话无 events**（P2）：Trajectory/Usage/turn 终态/tool 卡缺失；Chat 可读（messages 真源），但与原生会话体验不一致。
7. **附件引用未迁移**（P2）：消息内图片/文件引用（DSH attachments v1）不落地。
8. **报告 `created_workspace` 被后续会话覆盖为 false**（P3）：同一 cwd 第二个会话把 `CreatedInZeo` 写回 false（`importer.go` 内 `updateWorkspaceReport` 调用顺序导致）。
9. **版本守卫缺失**（P2）：未来 `session.v5.jsonl` 会按 v4 解析并可能静默丢事件；建议 header.version > 4 时警告/跳过。
10. **App 运行中导入未拒绝**（P2）：SQLite WAL 并发写风险，仅文档提示。
11. **无 UI 入口**（P2，产品面）：committed HEAD 只有 CLI；README 也承认「设置页 UI 入口仍待做」。

### 2.6 PD-04 验收结论

**可发布为最小导入（技术预览）**——前提：① 文档明确「导入的是文本对话，不含工具/图片/附件/trajectory；credential 只读键名」；② 默认提示先 `--dry-run`；③ 声明 subagent 默认跳过与 `--include-subagents` 行为。
**不可发布为「迁移器」**，需补（建议 wave 3 必做）：
- X1 过滤 `source.kind != "user"` 的注入上下文（或转为 system/隐藏 meta，而不是 user 气泡）；
- X2 按 turn 聚合 assistant step 消息（或至少把 `[tool call]` 合成行渲染为可折叠 meta，不混入正文）；
- X3 报告补 `images_seen`/`attachments_skipped` 与 skip 原因分解；补 workspace `created` 报告 bug；
- X4 幂等/去重 + header version 守卫 + 运行中实例检测（`instance.lock`）；
- X5 UI 入口（wave 2 工作树已有 import dialog，需 GUI 验收）+ 文档口径统一（README 与 `STATUS.md:9` 当前矛盾）。

## 3. B 部分：wave 1/2 产品面复核矩阵

| ID | wave | 验证方式 | 判定 | 残余缺口 / 证据 |
| --- | --- | --- | --- | --- |
| PD-01 键盘发送错误静默 | 1 | GUI 真跑（干净 data dir，无模型）：回车后 toast「尚未配置 Agent。请在 设置 > 模型 中添加模型。」可见；仍停留 Home；`sessions=0`/`messages=0`（空会话已回滚）；单测 `TestKeyboardSendFailureIsSurfacedAndRollsBack` 等 PASS | **CONFIRMED** | 代码 `home.go:386 toastDeferred`、`home.go:401 rollbackHomeSession`；截图 `shots/23/24/25` |
| PD-01 首启引导卡 | 2（工作树） | wave 2 构建 GUI：Home 显示「完成 ZeoKeel 的初始设置」卡（缺 workspace/缺模型两行 + 两个 CTA）；`TestHomeSetupCardGuidesFirstRun` PASS | **CONFIRMED（未提交）** | `shots/50`、`shots/55`；`home.go:83,100`；提交后需复核 |
| PD-02 provider 预设 + 测试连接 | 1 | GUI：添加模型对话框「服务商」下拉含 自定义/DeepSeek/OpenAI/Ollama；选 Ollama 自动填 `http://localhost:11434/v1` + `llama3.2`；「测试连接」对无效地址显示本地化错误「连接失败：Get … connection refused」；`Add model` 空字段内联校验 | **CONFIRMED** | 截图 `shots/28/30/42`；代码 `models_ui.go` `providerPresets`、`probeModelConnection`；`TestProvider…` |
| PD-02 残余：keyless 本地 provider | — | GUI：保存 Ollama 模型后 composer 徽章显示「Ollama Local」，但发送后 toast「尚未配置 Agent…」、`sessions=0`（回滚）；根因 `services.go:904-907` 要求 `provider.api_key != ""`，否则 agent=nil（Ollama 无需 key） | **PARTIAL（新发现）** | 截图 `shots/45/46`；这是「本地模型预设可配不可用」的发布级缺陷，建议：空 key 时允许 localhost base URL（或预设携带哨兵 key） |
| PD-03 单实例/二次启动 | 1 | 真跑：`instance.lock` 0600、数据目录 0700；同 data dir 二次启动 exit 0 且输出「asked it to show its window」；手工激活请求返回 `{"ok":true}`；不同 data dir 可并行两个实例；**legacy 49731 被占位时仍能启动**（日志继续 on data-directory lock） | **CONFIRMED** | `singleinstance.go:38-44,86`；日志/回归证据见 evidence-notes；修复原 P1 |
| PD-05 设置补页 | 2（工作树） | GUI 设置导航出现 通知/数据与隐私/诊断/网络/桌面/更新；`TestWave2SettingsPagesRender`、`TestNotificationSettingsPersist`、`TestNetworkProxyPersists`、`TestDataPrivacyExportAll/ResetAndClear`、`TestDiagnosticsCopyAndExportLogs` PASS | **PARTIAL（未提交）** | 截图 `shots/51`；提交后复核 |
| PD-06 Automation 历史/失败 | 2（工作树） | 代码 `store.AutomationRun*` + `app/automation_runs.go` + shell 运行历史/前景提示；`TestAutomationPageShowsForegroundHint`、`TestAutomationCardShowsRunHistory`、`TestRunAutomationTaskRecordsFailure` PASS | **PARTIAL（未提交）** | 提交后需 GUI 复核；origin 会话投递测试 PASS |
| PD-07 MCP/Skills/AGENTS 管理 | 2（工作树） | `manage_ui.go` 页面 + 测试 `TestMCPPageValidatesAndSaves`、`TestSkillsPageScansAndOpensFolder`、`TestAgentsMDEditorCreatesAndSaves` PASS | **PARTIAL（未提交）** | MCP 未做连通测试（设计允许） |
| PD-10 恢复会话/草稿 | 2（工作树） | `session_restore.go`：`ui.last_session` + `draft.<id>` 持久化；`TestLastSessionRestoredReadOnly`、`TestDraftPersistedAndRestoredAcrossRestart`、`TestMissingLastSessionFallsBackHome` PASS | **PARTIAL（未提交）** | GUI 复跑未完成（脚本被杀进程中断）；单测覆盖 |
| PD-12 桌面生命周期 | 2（工作树） | `settings_extra` 桌面页（关闭行为/自启）+ `app/autostart*.go`；`TestDesktopCloseBehaviorAndAutostart`、`TestSetAutostartLinux`、`TestSyncAutostartFromSetting` PASS | **PARTIAL（未提交）** | 托盘菜单与全局快捷键是否补需再查（worktree app.go 有改动） |
| PD-09 错误产品化 | 1 | 代码 `session_errors.go` 分类（auth/rate/context/network/model）+ 技术细节折叠 + 动作；`TestSessionError…` PASS；本轮未做「真实 provider 失败」GUI 复跑（wave 1 原问题是原始栈暴露） | **PARTIAL（代码+测试确认，运行未复跑）** | 建议 wave 2 收口时补一次 GUI 失败回合 |
| PD-08/11/15/17/18 | 1 | 插件页统一 6 项（`plugins_ui.go:242`）；Home 导航「首页」（`workspaces.go:186`/`chrome.go:161`）；窗口 WorkArea clamp（`window_geometry.go`）；workspace root 校验/创建目录（`workspaces.go:1329-1360`）；模型删除确认（`models_ui.go:570-605`）；对应测试 PASS | **CONFIRMED** | 截图 `shots/01`（首页导航）、模型删除确认代码 |
| 过程门禁 | 1 | `git show 1da4f12:internal/importer/` **缺 `credentials.go`**（被 `.gitignore` 的 `credentials*` 规则忽略），该提交单独 `go build` 失败；`0bbf051` 修复 | **发现（P2 流程）** | 最终提交前必须从该 commit 重新构建/跑全量门禁；CI 会拦住但不能依赖 |

## 4. Release gate 残余缺口（用户视角，按优先级）

**发布前必须（P1）**
1. PD-02 残余：本地 provider（Ollama/自定义无 key）保存后不可用（`services.go:904-907`）——要么允许空 key + localhost，要么预设写哨兵 key，并补测试。
2. PD-04 导入质量：注入上下文过滤 + assistant step 聚合（否则「迁移」后会话不可读/误导）。
3. PD-04 幂等/重复导入与版本守卫（数据风险）。
4. wave 2 全部产品项需**提交并独立复核**（当前仍是工作树）；PD-09 真实失败回合 GUI 复跑。
5. PD-04 文档口径：README（最小迁移可用、UI 待做）vs `docs/STATUS.md:9`（CLI/UI 未接线）矛盾；`docs/BUGS.md` PD-04 状态需与「CLI 已可用」一致。

**后续（P2/P3）**
6. 导入报告字段（images/attachment/skip 原因/created_workspace bug）；导入 UI 入口 GUI 验收；导入时检测运行中实例。
7. 导入会话的 Trajectory/Usage/工具卡视觉：至少在 UI 标注「导入会话：仅文本」。
8. 第三方/首次运行体验：setup 卡之外，首启是否自动引导创建默认 workspace（当前仍需手动）可再评估。

## 5. 未验证 / 限制

1. 未使用真实模型（成本限制）：PD-02 的「测试连接成功」路径、PD-09 真实失败回合卡片未做端到端 GUI 复跑（仅失败连接路径与单测）。
2. wave 2 在验证时未提交且仍在写入：其判定只能到 `go build`/单测/冒烟；提交后需按本报告 §3 重跑。
3. macOS/Windows 真机：单实例文件锁、autostart（LaunchAgents/注册表）、托盘菜单、通知均未真机验证（Linux Xvfb 无系统托盘/D-Bus）。
4. 未解析 DSH `profiles/storages` 语义（只按目录存在性记录，不导入）。
5. 未验证 `/root/.dsh` 之外的用户数据形态（例如项目级 `.dsh` 或自定义 data dir）。
6. `--include-subagents` 导入后的会话可见性/导航体验未 GUI 验证（312 会话的场景）。
7. PD-04 UI 导入对话框（wave 2 `TestImportFromDSHDialog`）未 GUI 走查。

## 6. 证据索引

- 日志：`.cache/audit/product-verify/logs/` — `import-dryrun-full.json`、`import-dryrun-includesubagents.json`、`import-real-max8.json`、`import-no-zstd.json`、`import-fake-creds-mode.txt`、`dsh-session-scan.json`、`wave2-shell-tests.txt`
- 截图：`.cache/audit/product-verify/shots/` — wave1 产品复验 20–46、wave2 冒烟 50–55、导入会话打开 09–11
- 补充方法与命令：`.cache/audit/product-verify/evidence-notes.md`
