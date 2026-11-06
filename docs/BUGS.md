# ZeoKeel 缺陷与待办清单（BUGS）

> 真源约定：本文件是"已知缺陷 / 未达标项 / 延期项"的**唯一落地清单**（见 `docs/QUALITY.md` 流程第 3 条）。
> 证据来源：`docs/audit/MASTER-AUDIT.md` 附录 B + 各分报告（只读）；修复记录与逐项验证：`docs/audit/FIX-REPORT.md`（Lead 汇总，两处交叉引用）。
> 状态图例：✅ DONE（代码落地且本文件给出可复核证据）/ 🟡 IN PROGRESS（本轮修复中，验收见 FIX-REPORT）/ ⏸ DEFERRED（本轮不做，理由见 §5）/ OPEN（未开始）。
> 更新：2026-11-05 · 基线 `HEAD=3100af0`；**wave 1 修复提交 `1da4f12`**（修复清单/验证见 `docs/audit/FIX-REPORT.md`）；新增条目必须写"复现命令/测试链接"。

## 1. P0/P1（来自 MASTER-AUDIT 附录 B）

| 维度 | ID | 级别 | 问题 | 状态 | 修复/证据 |
| --- | --- | --- | --- | --- | --- |
| 安全 | RT-01 | P0 | workspace `mcp.json` 构建期无审批执行任意命令 | ✅ DONE | `mcp.workspace_config` 默认 `off`，仅 `allow` 放行；env `ZEOKEEL_MCP_WORKSPACE_CONFIG`；`internal/app/services.go`（`workspaceMCPConfigAllowed`）+ 设置解析测试 |
| 安全/权限 | RT-02 | P1 | automation/新会话不按自身权限重建 Agent | ✅ DONE | submit 持 bindMu 重新 pin + rebuildForPermission；`internal/session/submit_permission_test.go`；独立验证 CONFIRMED |
| 安全/权限 | RT-03 | P1 | Read Only 仍注册 schedule/memory/goal 且不审批 | ✅ DONE | 只读注册表移除 schedule_create/update/delete/memory/goal；`internal/app/readonly_tools_test.go`；独立验证 CONFIRMED |
| 安全/数据 | RT-07 | P1 | spill 跟随 workspace symlink 写出工作区 | ✅ DONE | 逐级 Lstat 拒 symlink + O_EXCL；`internal/session/tool_spill.go`；独立验证 CONFIRMED |
| 可靠性 | RT-08 | P1 | 无重试/退避，429/瞬断整轮失败 | ✅ DONE | `internal/provider/openai/retry.go`；设置 `provider.retry_max_retries/retry_initial_ms/retry_max_ms`；流中途失败不重试（避免重复输出）仍见 known-limitations §5 |
| 并发 | RT-04 | P1 | 单 Agent 全局串行，后台回合/审批可阻塞前台 | ⏸ DEFERRED | 需 Eino 并发模型/MCP 生命周期专项设计，方案见 FIX-REPORT §7 |
| 团队 | RT-05 | P1 | Agent Teams 成员无 turnContext，gated 工具失败 | ✅ DONE | `WithTurnApprovalFrom` 审批上下文回退 + agentteam spawnCtx 透传；8 测试；父回合结束 fail-closed |
| 可靠性 | RT-06 | P1 | 模型流 60s 硬超时 | ✅ DONE | 默认无整体超时；`provider.timeout_seconds`（0=不限）、`provider.stream_idle_timeout_seconds`（默认 300s）；见 known-limitations §5 |
| 功能 | FP-01 | P1 | workflow 非 JS 编排引擎（固定 JSON 步骤） | ✅ DONE（JSON 范围） | wave2：workflow 支持 JSON parallel/pipeline（`internal/tools/workflow.go`；parallel 组展开/依赖）；完整 JS/Node 引擎 ⏸（§3 纯 Go 约束） |
| 功能 | FP-02 | P1 | PTC 只是工具批处理（无代码运行时） | ❌ NOT PLANNED（产品决定明确不做） | 2026-11-06 用户决策：**PTC 代码运行时明确不做**，PTC 模式/入口同批从代码与 UI 删除（不再作为缺口/后续项跟踪）；见 known-limitations §2.1 |
| 功能 | FP-03 | P1 | subagent 仅同步（无 childId/异步/续谈） | ✅ DONE（异步） | wave2：`subagent` 支持 `async:true` 后台子代理，返回 `{kind:"subagent_async",child_id,...}`（`internal/tools/subagent.go:59`）；续谈/fork 仍缺 |
| 功能 | FP-04 | P1 | Goal 半成品（无 revision/phase/pause，无 `/goal`） | ✅ DONE（模型+API；UI 命令待前端） | wave2：`GoalPhase`（active/paused/blocked）+ revision/暂停语义（`internal/tools/goal.go`）；`/goal` 命令行入口待前端 |
| 功能 | FP-05 | P1 | 无 workspace changes / 每回合 diff | 🟡 PARTIAL（后端已交付） | wave2：`internal/workspacechanges` 快照/差异 + controller 在回合结束发 `state.WorkspaceChanges`（`controller.go:1557`）；UI 渲染待前端 |
| 功能 | FP-06 | P1 | 无声明式 agent preset/persona | ✅ DONE | wave3：`internal/presets`（内置 standard/minimal/creator 等 + 用户 `<data dir>/presets/*.json` 字段级覆盖，7/7 测试）+ shell「预设」页（`presets_ui.go`，会话级选择/覆盖）+ 用户指南 `docs/audit/PRESETS-USER-GUIDE.md`；Deep/Supervisor 保留旧别名 |
| 功能 | FP-07 | P1 | schedule 触发不回原会话/不记录 origin | ✅ DONE（worktree） | wave2：`schedule` 记录 `OriginSessionID`（`internal/tools/schedule.go:180`），调度器优先投回原会话否则新建（`internal/app/automation_runs.go:199`）；automation_runs 表记录运行 |
| 功能 | FP-15 | P1 | job 完成无通知 | ✅ DONE（worktree） | wave2：`jobManager.notifyFinished` + `completionNotice`（`internal/tools/jobs.go:121,305`）在回合内投放 advisory |
| 功能 | FP-17 | P1 | skills 单 root | ✅ DONE | `internal/skills` 多 root（ws/skills + ws/.zeokeel/skills + ~/.zeokeel/skills，自定义 root 不扩张）+ SKILL.md 指纹自动 refresh；app 注册/prompt 用 `LoadRootsTolerant(RootsFor(...))`；测试 8+ |
| 功能 | FP-19 | P1 | 无统一代理设置 | ✅ DONE（worktree） | wave2：`network.proxy` 统一 provider/web_search/web_fetch（`app/services.go:635,933`；`tools.SetNetworkProxy`）；设置行在 Settings › 网络 |
| 功能 | FP-16 | P1 | ask_user 非阻塞（回答走下一轮） | ✅ DONE | wave3：`runtime.QuestionGate` + `app/question_gate.go`：交互会话**阻塞工具调用等待回答**（超时/取消降级 pending）；headless/ACP/子代理无问答通道时立即返回 pending 并结束回合（不阻塞）；shell 问题卡经 `Controller.AnswerQuestion` 回答；测试 + 文档同步 |
| UX | UX-01 | P1 | 无模型发送完全静默 | ✅ DONE | deferred toast + 失败回滚空会话/保留 draft；**独立 GUI 复核 CONFIRMED**（FIX-VERIFY-UI：toast+回滚+保留 draft，DB sessions=0） |
| UX | UX-03 | P1 | 发送失败 toast 路径不一致（根因；键盘 Enter 静默） | ✅ DONE | `toastDeferred`（build 外 queueToast）；**独立 GUI 复核 CONFIRMED**（Home/Session 键盘 Enter 均 toast） |
| UX | UX-02 | P1 | 新会话对话 tab 无空态 | ✅ DONE | 会话空态 + 无模型 CTA；**独立 GUI 复核 CONFIRMED**（FIX-VERIFY-UI） |
| 产品 | PD-01 | P1 | 无模型首启无引导 + 错误静默 | ✅ DONE（首启卡 PARTIAL） | 与 UX-01 同根因修复，独立 GUI CONFIRMED；首启 setup 引导卡已在 wave2 工作树（`TestHomeSetupCardGuidesFirstRun`），待提交后复核 |
| 产品 | PD-02 | P1 | 添加模型无语义校验/预设/模型清单 | ✅ DONE | Provider 预设 + 异步测试连接（GET /models）；**独立 GUI 复核 CONFIRMED**（FIX-VERIFY-UI `shots/39/42/43`） |
| 产品 | PD-02b | P1 | **新发现**：无 key 的本地 provider（Ollama 预设）可保存但发送时被判"未配置" | ✅ DONE | wave2：`keylessLocalProvider`（localhost/127.0.0.1/::1/私网/链路本地/host.docker.internal 允许空 key，公网仍要求 key）+ headless e2e；测试 `TestKeylessLocalProvider`/`TestBuildAgentKeylessLocalProvider`/`TestRunHeadlessKeylessLocalProvider` |
| 产品 | PD-03 | P1 | 单实例固定端口、二次启动不激活窗口 | ✅ DONE | data-dir 锁 + 二次启动 IPC 激活窗口；独立验证 CONFIRMED |
| 产品 | PD-04 | P1 | "迁移器"定位但零迁移实现 | ✅ DONE（最小导入，技术预览） | **最小导入器 + CLI 已交付**：`internal/importer` + `zeokeel import [--from --dry-run --json --max]`（退出码 0/1/2；真实 `/root/.dsh` 只读 dry-run 325 sessions/**1,248 messages**（系统注入/step 级过滤后；修复前 17,219））。独立验证（`docs/audit/FIX-VERIFY-PRODUCT.md` §2）：**可作为「最小导入（技术预览 CLI）」发布，不可宣称迁移器**；口径已降级（README/STATUS/PLAN/known-limitations）。wave2 已补①–④的导入器侧：`SkipSystemInjected`/`SkipStepAssistant` 过滤 + `Skips` 原因分解 + `ImagesDropped` 统计 + `ManifestPath` 幂等 manifest（`--manifest`/`--no-manifest`）+ header version 守卫（`ErrUnsupportedVersion`）；剩余：运行中实例检测、旧版 manifest 首次重导说明。独立再验收（FIX-VERIFY-IMPORT2）质量 PASS；manifest 幂等在真实 `session-<uuid>` 布局上的 P1 已修复（key 改 `home|相对 sessions 路径` + 真实布局回归测试，fix-security 独立复核）；UI 入口为设置→数据与隐私→「从 DSH 导入」**命令弹窗**（展示并复制 `zeokeel import` 命令，提示先退出应用；**非应用内执行**）。 |
| 产品 | PD-05 | P1 | 设置面缺通知/隐私/诊断/代理/更新/重置 | ✅ DONE（已提交 84c5b1f） | 设置导航 通知/数据与隐私/诊断/网络/桌面/更新 + 持久化测试；独立 GUI 复核（FIX-VERIFY-UI-WAVE2）进行中 |
| 产品 | PD-06 | P1 | Automation 失败/历史不可见、不绑定会话配置 | ✅ DONE（已提交 84c5b1f） | store.AutomationRun* + automation_runs 记录 workspace/permission/model + 历史/失败原因/前景提示 UI；origin 投递；独立 GUI 复核进行中 |
| 产品 | PD-07 | P1 | MCP/Skills/AGENTS.md 无管理 UI | ✅ DONE（已提交 84c5b1f） | manage_ui.go：MCP JSON 编辑/校验、Skills 扫描/校验错误/打开文件夹、AGENTS.md 查看/编辑；独立 GUI 复核进行中 |
| 产品 | PD-09 | P1 | 失败回合直接展示内部错误栈、无建议/重试 | ✅ DONE | 错误分类卡 + 动作 + 技术细节折叠；`shell/session_errors.go`；4 测试 |
| 产品 | PD-10 | P1 | 重启不恢复会话、草稿仅内存 | ✅ DONE（已提交 84c5b1f） | ui.last_session 只读恢复 + draft.<id> 500ms 防抖持久化；独立 GUI 复核进行中 |
| 产品 | PD-12 | P2 | 桌面生命周期（关闭行为/自启/托盘动作/全局快捷键） | 🟡 PARTIAL | 关闭行为/自启三平台/托盘 Show-New-Settings-Quit 已实现并提交；全局快捷键 mygo 无 API → DEFERRED（known-limitations） |
| 工程 | AR-01 | P1 | 发布版零崩溃诊断（无 recover/日志，Windows 丢 stderr） | ✅ DONE | `cmd/zeokeel/{main,logging}.go`：`<DataDir>/logs/app-YYYYMMDD.log`（0600、8MiB→`.1` 轮转、跨天换文件）+ MultiWriter(stderr,file) + `debug.SetCrashOutput` + recover 写 `crash-*.txt`；`go test ./cmd/...`（14 个用例）；**独立验证 CONFIRMED**（FIX-VERIFY-CORE2：Xvfb 日志落盘/崩溃机制单测） |
| 工程 | AR-02 | P1 | CI 只是本地脚本；race/coverage 覆盖不全、flaky 重跑 | ✅ DONE | `.github/workflows/ci.yml`（gate 跑 `scripts/ci.sh`；shell race 独立 job `-timeout 20m`）；`ci.sh` 写 `.cache/coverage.out` 并把 app 纳入 race；`scripts/evolve/gates.sh` 的 flaky 重跑改为 exit 1 不掩盖；**独立验证 CONFIRMED**（FIX-VERIFY-CORE2 §0：干净副本机制正确，另发现需修 BUILD-02） |
| 工程 | AR-03 | P1 | 发布链路缺失（仅 Windows zip/无签名/无更新/版本未注入） | 🟡 PARTIAL | ✅ 版本注入 + `-s -w`（-31.8%）+ linux/mac 打包（**独立验证 CONFIRMED**，FIX-VERIFY-CORE2：`--version`/package-linux）；签名/公证 ⏸（需证书/发布账号）；**发布流水线已就绪**：`.github/workflows/release.yml`（tag → 三平台矩阵包+SHA256SUMS+GitHub Release）+ `scripts/release.sh`/`make release`；Windows 安装器 Inno/NSIS 可执行草案（未构建）见 `packaging/windows/INSTALLER-PLAN.md`；自动更新引擎 + `zeokeel update` CLI + 设置页检查已交付（托管 feed/签名 DEFERRED） |

## 2. P2/P3 结构性债务（本轮修复中 / 延期）

| ID | 级别 | 问题 | 状态 | 去向 |
| --- | --- | --- | --- | --- |
| AR-04 | P2 | `internal/shell` God package（57 生产文件/22.8k 行；`App` 262 行字段/715 方法；UI 直连 store/session） | 🟡 IN PROGRESS（P1 完成；i18n 抽离完成；P2–P4 按计划） | **已完成**：wave2/3 功能模块拆文件（`manage_ui`/`settings_extra`/`presets_ui`/`session_errors`/`plan_ui`/`message_actions`/`files_*` 等）+ **P1 read-model/async 拆分**（`sessions_cache_async`/`session_open_async`/`workspace_changes_ui`/`draft_scope`）+ **i18n 抽离**（新包 `internal/i18n` 552 生产行；shell `i18n.go` 660→190、shell 生产 LOC 23,231→22,761（−470）；验收 gofmt/vet/build/test 绿）。残留：shell `translation`/`translations` 兼容视图 ~18 行（目录断言迁走后删除）。P2（actions 命令层+异步队列）/P3（大屏拆包+import 门禁）/P4（app.go 生命周期拆分）按 [`docs/design/shell-refactor-plan.md`](design/shell-refactor-plan.md) 推进（未实施）；剩余同步路径归 AR-08（OPEN） |
| AR-05 | P2 | 两套快照折叠（Projector 闲置） | ✅ FIXED（2026-11-06） | 收敛为 **Controller 单一折叠**：`session.Snapshot()` 只读 controller 自己的 per-session views；`state.Projector` **deprecated**（`state/projector.go:36`、`state/doc.go:11`），`session.Config.Projector` 传入时 `New` 立即 `Close` 以停止旁路折叠（`internal/app/services.go` 的 4 行 projector 死代码已删除，fix-security）。RED→GREEN：`TestControllerStopsBorrowedProjector`、`TestControllerSnapshotIgnoresBorrowedProjector`（`internal/session/projector_convergence_test.go`）。bench：每事件折叠 **2→1 次**（`BenchmarkProjectorFoldEvent` → `BenchmarkApplyFoldEvent`，parity-auditor 记录 ~3792→~1416 ns/op） |
| AR-06 / RT-10 | P2 | 逐 token 持久化 + 无保留策略（RT-10：实测 0.66–0.79ms/事件 + snapshot O(n²)） | ✅ PARTIAL | 批量持久化已落地：流事件按 `eventBatchMaxBytes=8KB` / `eventBatchMaxDelay=200ms` 批量写（`internal/session/event_batch.go`），即**崩溃恢复窗口 ≤8KB/200ms**；store 批量事务/单 fsync；非流事件强制先 flush 保持重放顺序；snapshot O(n²) 拷贝（AR-22）与 events 保留策略仍待后续 |
| AR-07 | P2 | 排序依赖事件回放重建 Seq | ✅ DONE | wave3：`messages.ordinal` 列（schema v2 迁移回填 + 索引 `messages_session_ordinal_idx`），新增消息在 store 内分配 ordinal，`ListMessages ORDER BY ordinal, id`，排序不再依赖事件回放 |
| AR-08 | P2 | UI 线程同步 DB（OpenSession/ListSessions） | OPEN | 前端/后端协同，未排本轮 |
| UX-04 | P2 | 设置窗打开后主窗口残留 popover（state 已清但 `win.Invalidate()` 不足以强制出帧） | ✅ CONFIRMED FIXED（2026-11-06，Linux/Xvfb 无 WM） | fix-security 真跑复现序列（popover→Ctrl+,→移窗→Escape）popover 立即消失无残影，证据 `.cache/audit/fix-round4-ux04/{00-first…07-escape}.png`。**根因澄清**：mygo `holdFrame` 只在 `host.occluded()` 时挂帧，Linux GTK surface 未实现 `OccludableSurface` → 不是残影原因；现有重绘链路（关闭 composer/popover 状态 + `refreshWindows` + `Invalidate`）已正确重绘。**备注**：真实 WM/compositor（GNOME/KDE 等）下仍需 spot check |
| About | P2 | About 页硬编码 `version 0.1.0`（i18n.go:45）与 `BuildVersionString()` 两行版本冲突 | ✅ DONE（worktree） | fix-frontend 删除硬编码行与 `pluginProductVersion`，About 统一读 `shell.BuildVersionString()`（单一来源）；预览已重生成 |
| FP-11 | P2 | checkpoint 双屏障：屏障**可写入/fail-closed 成立**，但无恢复 consumer（requested/tool_pending checkpoint 不被读取，恢复仍走 History/tool_calls） | 🟡 PARTIAL | 独立验证 FIX-VERIFY-CORE2 §0；补恢复 consumer 或文档写明"仅供审计" |
| Budget | P2 | `context.budget_tokens` 无设置入口 | ✅ DONE（worktree） | wave2：Settings 显示并持久化 `context.budget_tokens`（`shell/settings_ui.go:622-627`，默认 200000） |
| AR-09 | P2 | schema 演进冻结 v1 | ✅ DONE | wave3：`SchemaVersion=2`，有序迁移步骤（每步单事务 + `schema_migrations` 日志），升级前 `VACUUM INTO <data dir>/backups/`（0600、失败即迁移失败 fail-closed）；旧版打开 v2 库被 `ErrSchemaTooNew` 拒绝（不支持 downgrade，见 known-limitations） |
| AR-17 | P2 | 测试盲区（store 62.8%/design 32.6%、无 fuzz） | 🟡 IN PROGRESS | CI 已记录 coverage；fuzz 待补 |
| AR-18 | P2 | 窗口首启/恢复未按 WorkArea 裁剪 | ✅ DONE | WorkArea clamp（主窗口+设置窗口）；11 headless 几何测试 |
| AR-20 | P3 | `scrubEnv`/DDL 重复 | ✅ DONE | `scrubEnv`/`scrubMCPEnv` 统一委托 `winproc.ScrubEnv`（RT-15 名单），automations DDL 收敛为单一来源 |
| AR-21/22 | P3 | 快照每 token O(n) 拷贝 / 大文件函数 | 🟡 PARTIAL | 重复代码已收敛（AR-20）；`state.updateMessage/updateTool` 仍整片复制 + 字符串重拼，大文件/长函数重构 ⏸ DEFERRED（见 shell-refactor-plan P3） |
| PD-08 | P2 | 插件双界面不一致（4 vs 6）、无功能实验开关 | ✅ DONE | 统一 6 项 + 实验项「未实现」标注；shell 测试 |
| AR-16/文档 | — | 已知缺陷此前无落地清单（本文件缺失） | ✅ DONE | 本文件；STATUS/README/PLAN/known-limitations 同轮纠错 |
| Hotkey | P2 | 全局快捷键无平台支持 | ✅ PARTIAL（Windows + Linux/X11 已实现；macOS 代码完成待真机；Wayland DEFERRED） | 平台矩阵：**Windows** implemented（`RegisterHotKey` + 锁 OS 线程消息泵 + WM_HOTKEY 分发）；**Linux/X11** implemented（`x11_linux.go` 454 行：purego dlopen `libX11.so.6`，无 cgo、无新依赖；`XGrabKey` 四组 lock-mask 变体；Wayland-only / 无 `$DISPLAY` 降级 `ErrUnsupported`，portal 方案 DEFERRED）；**macOS** implemented（`globalhotkey_darwin.go`：purego dlopen `Carbon.framework`/HIToolbox `RegisterEventHotKey`，纯 Go、无 cgo；要求 AppKit 主线程/事件 target，无 target 优雅降级 `ErrUnsupported`；`carbon_mapping.go` 纯映射层 5 个跨主机单测；**compile-only，runtime 待真机验证**）。验证：`go test ./internal/globalhotkey` **23 PASS + 1 SKIP**；`GOOS=darwin amd64/arm64 go build ./internal/globalhotkey ./cmd/zeokeel` ✅ + `GOOS=darwin go vet` ✅；6 目标交叉编译 ✅。已知真机验证项：Carbon API 10.8 起弃用但仍可用、主线程要求、F21–F24 在 macOS 无标准键码、Linux 自动重复（按住可能重复触发）待 debounce（详见 known-limitations） |
| ACP | P2 | 无编辑器/外部客户端协议（ACP v1） | ✅ DONE（最小面） | wave3：`internal/acp`（JSON-RPC stdio 最小 v1：initialize/session/new/prompt/cancel + session/update 投影；11 测试）+ `internal/app/acp_adapter.go`（真 `acp.Server`→真 runtime e2e，4 测试）+ `zeokeel acp`（stdout 仅协议帧、SIGINT/EOF 干净退出）。**Non-goals（DEFERRED）**：session/load|list、authenticate、client fs/terminal、image prompt、session/request_permission（headless 自动 deny） |
| BUILD-01 | P1 | `.gitignore` 的 `credentials*` 规则把 `internal/importer/credentials.go`（已跟踪构建输入）忽略，wave1 提交在干净 checkout 下无法 `go build`（verifier-core 发现） | ✅ DONE | Lead 改名 `internal/importer/import_credentials.go` 并提交 `0bbf051`；`.gitignore` 收紧为 `.credentials*`/`/credentials*`；新增 `scripts/check-clean-checkout.sh`（`git archive HEAD` 到临时目录后 `go build ./...`）+ CI gate job 步骤 + `make clean-check`；本文件记录该类问题。验证：`sh scripts/check-clean-checkout.sh` → PASS |
| BUILD-02 | P1 | 干净 HEAD 曾 `ci.sh --fast` 红：wave1 断言与 skills live re-scan 冲突 | ✅ DONE | `modes_test.go` 修正随 wave2 提交；`git archive 84c5b1f` 干净副本 `sh scripts/ci.sh --fast` = **PASS**（22 包全绿，session 252s/shell 68s/nocgo 四目标）；CI 已加 `check-clean-checkout.sh` 守卫 |
| Preview | P3 | 视觉金标与 wave1/wave2 UI 漂移 | ✅ DONE（22 张） | 2026-11-05 重生成 22 张（当前代码，含设置窗 **900×840**、About 单一版本来源）+ `TestVisualRegression` PASS（14 屏/1.9s）；`identify` 校验尺寸；命令见 `docs/design/preview/README.md` 与 `make previews`/`make visual-check` |
| Voice | ❌ NOT PLANNED | 语音输入（DSH 实验能力） | 产品决定明确不做（2026-11-06）；UI/入口不提供，不是能力缺口 | known-limitations §2.1 |
| Updater | ✅ DONE（引擎+CLI+GUI 全链路） | 自动更新（check/download/sha256/install-on-restart） | `internal/updater/` 引擎（HTTPS-only、平台 asset、SHA-256、原子 install-on-restart、崩溃恢复，14/14 测试绿）+ **`zeokeel update` CLI**（`--check/--download/--install/--json`，10 测试绿，`cmd/zeokeel/update_cli*.go`）+ **GUI 更新页全链路**：「检查更新」+「下载并安装」（`shell/settings_update_hotkey.go` 下载→SHA-256 校验→`ScheduleInstall`→**重启应用完成安装**；GUI 安装经 shell hook `SetInstallUpdateHook` 路由到 app seam `DownloadAndInstallUpdate`，启动时 `applyPendingUpdate` 应用暂存更新；`app/update_install.go` 另有 executable replacer；feed 取 `update.feed_url`/`ZEOKEEL_UPDATE_FEED`）。**托管 feed 与签名/公证为外部依赖 → DEFERRED**。 | `.cache/fix-platform/updater-{red,green}.txt`、`round3-evidence.txt` |
| FP-18 | P2 | headless CLI 缺失 | ✅ DONE | `internal/app/headless.go` + cmd 接线；`--permission` 失效 P1 已由独立验证发现并修复（read-only/full 回归） |
| AR-23 | P2 | Updates 页异步检查/安装 goroutine 无同步读写 `shellUIState.update*`（checking/message/release/available/installing/installed/staged）：`CGO_ENABLED=1 go test -race ./internal/shell` 在基线 `3565c97` 复现 5 个 Update* 测试 DATA RACE 失败（本轮模型 parity 门禁发现） | ✅ DONE（2026-11-06） | 更新状态并入 `updateMu` 临界区；新增 `updatePageSnapshot`/`mutateUpdatePage`，渲染只读一致快照（`internal/shell/models_ui.go`、`settings_update_hotkey.go`、`settings_extra.go`）。复现命令：`CGO_ENABLED=1 go test -race -run 'TestUpdatesCheckRunsAndShowsResult|TestUpdatesCheckErrors|TestUpdateDownloadInstallFlow|TestUpdateInstallErrorIsShown|TestUpdateInstallFallbackWithoutHook' ./internal/shell`（基线红；修复后绿）。全量 `CGO_ENABLED=1 go test -race -timeout 20m ./internal/shell` = PASS（462.8s） |

## 3. 延期项（DEFERRED，本轮不做；理由见 FIX-PLAN §5）

- workflow 完整 JS 引擎（与纯 Go / no-cgo 约束冲突）。**PTC 运行时不在延期项**：已改为明确不做（FP-02）。
- ACP 完整协议（session/load/list、client fs/terminal、request_permission 映射等）；SDK 完整协议；三平台代码签名/公证与托管 update feed（自动更新引擎/CLI/检查+下载+安装 UI 已交付，缺证书、发布账号与 feed 托管）。
- `internal/shell` 整体分层重构、schema v2 大迁移、JSONL 真源重写（高风险，分批）。
- 真机 IME/屏幕阅读器/DPI/通知矩阵（无真机环境）。
- 第三方插件市场（产品边界：仅内置插件）。

## 4. 如何更新本文件

1. 发现缺陷先写复现命令/测试名；修复后把状态改为 ✅ 并附证据（文件:行 或 `go test -run ...`）。
2. 每轮修复由 Lead 在 `docs/audit/FIX-REPORT.md` 汇总，本文件只保留"当前仍有/已修"的结论，不写过程。
3. 不允许再出现 `.cache/team/*.md` 这类未跟踪的缺陷积压（`docs/QUALITY.md` 的要求）。
4. 构建/发布类问题的回归守卫：`sh scripts/check-clean-checkout.sh`（干净 checkout 构建，BUILD-01）、`sh scripts/check-nocgo.sh`（零 cgo + 交叉编译）、`sh scripts/ci.sh`（全门禁）。

## 6. wave2 验证新观察（P3，来自 FIX-VERIFY-UI-WAVE2，未阻塞）

| ID | 级别 | 问题 | 状态/去向 |
| --- | --- | --- | --- |
| UX-W2-1 | P3 | Automation「立即运行」在提交失败时仍创建 0 消息会话（与 UX-01 空会话污染同类） | OPEN，下轮修复（run-now 失败应回滚会话并记 failed run） |
| UX-W2-2 | P3 | Skills 默认扫描路径显示进程 cwd 下 `/data/mygo/skills`（从任意目录启动时可能反直觉，应优先 workspace 根） | OPEN，下轮确认预期语义/修正文案 |
| UX-W2-3 | P3 | Settings 设置页 nav 14 项、840 高可显示（已在独立验证确认无裁切）；后续新增页面需回归 840 高度 | 记录 |
