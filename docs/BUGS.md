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
| 功能 | FP-02 | P1 | PTC 只是工具批处理（无代码运行时） | ⏸ DEFERRED | 产品边界+纯 Go 约束；见 known-limitations §5 |
| 功能 | FP-03 | P1 | subagent 仅同步（无 childId/异步/续谈） | ✅ DONE（异步） | wave2：`subagent` 支持 `async:true` 后台子代理，返回 `{kind:"subagent_async",child_id,...}`（`internal/tools/subagent.go:59`）；续谈/fork 仍缺 |
| 功能 | FP-04 | P1 | Goal 半成品（无 revision/phase/pause，无 `/goal`） | ✅ DONE（模型+API；UI 命令待前端） | wave2：`GoalPhase`（active/paused/blocked）+ revision/暂停语义（`internal/tools/goal.go`）；`/goal` 命令行入口待前端 |
| 功能 | FP-05 | P1 | 无 workspace changes / 每回合 diff | ✅ DONE（后端事件） | wave2：`internal/workspacechanges` 快照/差异 + controller 在回合结束发 `state.WorkspaceChanges`（`controller.go:1557`）；UI 渲染待前端 |
| 功能 | FP-06 | P1 | 无声明式 agent preset/persona | ⏸ DEFERRED | L 项，见 §3 |
| 功能 | FP-07 | P1 | schedule 触发不回原会话/不记录 origin | ✅ DONE（worktree） | wave2：`schedule` 记录 `OriginSessionID`（`internal/tools/schedule.go:180`），调度器优先投回原会话否则新建（`internal/app/automation_runs.go:199`）；automation_runs 表记录运行 |
| 功能 | FP-15 | P1 | job 完成无通知 | ✅ DONE（worktree） | wave2：`jobManager.notifyFinished` + `completionNotice`（`internal/tools/jobs.go:121,305`）在回合内投放 advisory |
| 功能 | FP-17 | P1 | skills 单 root | 🟡 IN PROGRESS | 当前仍单 root（`internal/app/services.go:847`）；wave2 多 root 未落地 |
| 功能 | FP-19 | P1 | 无统一代理设置 | ✅ DONE（worktree） | wave2：`network.proxy` 统一 provider/web_search/web_fetch（`app/services.go:635,933`；`tools.SetNetworkProxy`）；设置行在 Settings › 网络 |
| 功能 | FP-16 | P1 | ask_user 非阻塞（回答走下一轮） | ⏸ DEFERRED | 阻塞式需 interrupt/resume 语义（当前工具内阻塞会死锁回合，见 `internal/tools/ask_user.go:91` 注释）；见 known-limitations §3.9 |
| UX | UX-01 | P1 | 无模型发送完全静默 | ✅ DONE | deferred toast + 失败回滚空会话/保留 draft；**独立 GUI 复核 CONFIRMED**（FIX-VERIFY-UI：toast+回滚+保留 draft，DB sessions=0） |
| UX | UX-03 | P1 | 发送失败 toast 路径不一致（根因；键盘 Enter 静默） | ✅ DONE | `toastDeferred`（build 外 queueToast）；**独立 GUI 复核 CONFIRMED**（Home/Session 键盘 Enter 均 toast） |
| UX | UX-02 | P1 | 新会话对话 tab 无空态 | ✅ DONE | 会话空态 + 无模型 CTA；**独立 GUI 复核 CONFIRMED**（FIX-VERIFY-UI） |
| 产品 | PD-01 | P1 | 无模型首启无引导 + 错误静默 | ✅ DONE（首启卡 PARTIAL） | 与 UX-01 同根因修复，独立 GUI CONFIRMED；首启 setup 引导卡已在 wave2 工作树（`TestHomeSetupCardGuidesFirstRun`），待提交后复核 |
| 产品 | PD-02 | P1 | 添加模型无语义校验/预设/模型清单 | ✅ DONE | Provider 预设 + 异步测试连接（GET /models）；**独立 GUI 复核 CONFIRMED**（FIX-VERIFY-UI `shots/39/42/43`） |
| 产品 | PD-02b | P1 | **新发现**：无 key 的本地 provider（Ollama 预设）可保存但发送时被判"未配置"（`services.go` 要求 `provider.api_key != ""`），本地模型"可配不可用" | 🟡 OPEN | 来源 `docs/audit/FIX-VERIFY-PRODUCT.md` §3（截图 shots/45/46）；建议：空 key 时允许 localhost/本地 base URL，或预设携带哨兵 key |
| 产品 | PD-03 | P1 | 单实例固定端口、二次启动不激活窗口 | ✅ DONE | data-dir 锁 + 二次启动 IPC 激活窗口；独立验证 CONFIRMED |
| 产品 | PD-04 | P1 | "迁移器"定位但零迁移实现 | 🟡 IN PROGRESS（最小导入已交付） | **最小导入器 + CLI 已交付**：`internal/importer` + `zeokeel import [--from --dry-run --json --max]`（退出码 0/1/2；真实 `/root/.dsh` 只读 dry-run 325 sessions/17223 messages）。独立验证（`docs/audit/FIX-VERIFY-PRODUCT.md` §2）：**可作为「最小导入（技术预览 CLI）」发布，不可宣称迁移器**；口径已降级（README/STATUS/PLAN/known-limitations）。wave2 已补①–④的导入器侧：`SkipSystemInjected`/`SkipStepAssistant` 过滤 + `Skips` 原因分解 + `ImagesDropped` 统计 + `ManifestPath` 幂等 manifest（`--manifest`/`--no-manifest`）+ header version 守卫（`ErrUnsupportedVersion`）；剩余：设置页导入入口、运行中实例检测、独立复验（product-auditor 需按新行为复核）。 |
| 产品 | PD-05 | P1 | 设置面缺通知/隐私/诊断/代理/更新/重置 | 🟡 PARTIAL（工作树已实现，未提交） | 独立验证 FIX-VERIFY-PRODUCT：设置导航 通知/数据隐私/诊断/网络/桌面/更新 + 持久化测试 PASS；提交后复核 |
| 产品 | PD-06 | P1 | Automation 失败/历史不可见、不绑定会话配置 | 🟡 PARTIAL（工作树已实现，未提交） | store.AutomationRun* + 运行历史/前景提示 + origin 投递测试 PASS；提交后 GUI 复核 |
| 产品 | PD-07 | P1 | MCP/Skills/AGENTS.md 无管理 UI | 🟡 PARTIAL（工作树已实现，未提交） | manage_ui.go 页面 + MCP/Skills/AGENTS 测试 PASS；MCP 连通测试未做（设计允许） |
| 产品 | PD-09 | P1 | 失败回合直接展示内部错误栈、无建议/重试 | ✅ DONE | 错误分类卡 + 动作 + 技术细节折叠；`shell/session_errors.go`；4 测试 |
| 产品 | PD-10 | P1 | 重启不恢复会话、草稿仅内存 | 🟡 PARTIAL（工作树已实现，未提交） | ui.last_session + draft.<id> 持久化，3 测试 PASS；GUI 复跑未完成 |
| 产品 | PD-12 | P2 | 桌面生命周期（关闭行为/自启/托盘动作/全局快捷键） | 🟡 PARTIAL（工作树已实现，未提交） | settings 桌面页 + app/autostart*；3 测试 PASS；全局快捷键若 mygo 无 API 则 DEFERRED |
| 工程 | AR-01 | P1 | 发布版零崩溃诊断（无 recover/日志，Windows 丢 stderr） | ✅ DONE | `cmd/zeokeel/{main,logging}.go`：`<DataDir>/logs/app-YYYYMMDD.log`（0600、8MiB→`.1` 轮转、跨天换文件）+ MultiWriter(stderr,file) + `debug.SetCrashOutput` + recover 写 `crash-*.txt`；`go test ./cmd/...`（14 个用例）；**独立验证 CONFIRMED**（FIX-VERIFY-CORE2：Xvfb 日志落盘/崩溃机制单测） |
| 工程 | AR-02 | P1 | CI 只是本地脚本；race/coverage 覆盖不全、flaky 重跑 | ✅ DONE | `.github/workflows/ci.yml`（gate 跑 `scripts/ci.sh`；shell race 独立 job `-timeout 20m`）；`ci.sh` 写 `.cache/coverage.out` 并把 app 纳入 race；`scripts/evolve/gates.sh` 的 flaky 重跑改为 exit 1 不掩盖；**独立验证 CONFIRMED**（FIX-VERIFY-CORE2 §0：干净副本机制正确，另发现需修 BUILD-02） |
| 工程 | AR-03 | P1 | 发布链路缺失（仅 Windows zip/无签名/无更新/版本未注入） | 🟡 PARTIAL | ✅ 版本注入 + `-s -w`（-31.8%）+ linux/mac 打包（**独立验证 CONFIRMED**，FIX-VERIFY-CORE2：`--version`/package-linux）；签名/公证/自动更新 ⏸（需证书/发布账号） |

## 2. P2/P3 结构性债务（本轮修复中 / 延期）

| ID | 级别 | 问题 | 状态 | 去向 |
| --- | --- | --- | --- | --- |
| AR-04 | P2 | `internal/shell` God package 41.4k 行/UI 直连 store | ⏸ DEFERRED | L 项重构（FIX-PLAN §5） |
| AR-05 | P2 | 两套快照折叠（Projector 闲置） | 🟡 IN PROGRESS | fix-backend §2.15（收敛/删除） |
| AR-06 / RT-10 | P2 | 逐 token 持久化 + 无保留策略（RT-10：实测 0.66–0.79ms/事件 + snapshot O(n²)） | ✅ PARTIAL | 批量持久化已落地：流事件按 `eventBatchMaxBytes=8KB` / `eventBatchMaxDelay=200ms` 批量写（`internal/session/event_batch.go`），即**崩溃恢复窗口 ≤8KB/200ms**；store 批量事务/单 fsync；非流事件强制先 flush 保持重放顺序；snapshot O(n²) 拷贝（AR-22）与 events 保留策略仍待后续 |
| AR-07 | P2 | 排序依赖事件回放重建 Seq | 🟡 IN PROGRESS | fix-backend §2.15（`messages.ordinal` + 迁移） |
| AR-08 | P2 | UI 线程同步 DB（OpenSession/ListSessions） | OPEN | 前端/后端协同，未排本轮 |
| UX-04 | P2 | 设置窗打开后主窗口残留 popover（state 已清但 `win.Invalidate()` 不足以强制出帧） | 🟡 PARTIAL | 独立 GUI 复核仍复现（FIX-VERIFY-UI §2.1）；需 mygo 强制重绘/`holdFrame` 方案 |
| About | P2 | About 页硬编码 `version 0.1.0`（i18n.go:45）与 `BuildVersionString()` 两行版本冲突 | ✅ DONE（worktree） | fix-frontend 删除硬编码行与 `pluginProductVersion`，About 统一读 `shell.BuildVersionString()`（单一来源）；预览已重生成 |
| FP-11 | P2 | checkpoint 双屏障：屏障**可写入/fail-closed 成立**，但无恢复 consumer（requested/tool_pending checkpoint 不被读取，恢复仍走 History/tool_calls） | 🟡 PARTIAL | 独立验证 FIX-VERIFY-CORE2 §0；补恢复 consumer 或文档写明"仅供审计" |
| Budget | P2 | `context.budget_tokens` 无设置入口 | ✅ DONE（worktree） | wave2：Settings 显示并持久化 `context.budget_tokens`（`shell/settings_ui.go:622-627`，默认 200000） |
| AR-09 | P2 | schema 演进冻结 v1 | 🟡 IN PROGRESS | fix-backend §2.15（迁移框架） |
| AR-17 | P2 | 测试盲区（store 62.8%/design 32.6%、无 fuzz） | 🟡 IN PROGRESS | CI 已记录 coverage；fuzz 待补 |
| AR-18 | P2 | 窗口首启/恢复未按 WorkArea 裁剪 | ✅ DONE | WorkArea clamp（主窗口+设置窗口）；11 headless 几何测试 |
| AR-20 | P3 | `scrubEnv`/DDL 重复 | 🟡 IN PROGRESS | fix-backend §2.8（secret 名单 + 收敛） |
| AR-21/22 | P3 | 快照每 token 拷贝 / 大文件函数 | 🟡 PARTIAL | scrubEnv/DDL 重复已收敛；O(n²) 快照与大函数重构 ⏸ |
| PD-08 | P2 | 插件双界面不一致（4 vs 6）、无功能实验开关 | ✅ DONE | 统一 6 项 + 实验项「未实现」标注；shell 测试 |
| AR-16/文档 | — | 已知缺陷此前无落地清单（本文件缺失） | ✅ DONE | 本文件；STATUS/README/PLAN/known-limitations 同轮纠错 |
| BUILD-01 | P1 | `.gitignore` 的 `credentials*` 规则把 `internal/importer/credentials.go`（已跟踪构建输入）忽略，wave1 提交在干净 checkout 下无法 `go build`（verifier-core 发现） | ✅ DONE | Lead 改名 `internal/importer/import_credentials.go` 并提交 `0bbf051`；`.gitignore` 收紧为 `.credentials*`/`/credentials*`；新增 `scripts/check-clean-checkout.sh`（`git archive HEAD` 到临时目录后 `go build ./...`）+ CI gate job 步骤 + `make clean-check`；本文件记录该类问题。验证：`sh scripts/check-clean-checkout.sh` → PASS |
| BUILD-02 | P1 | 干净 HEAD（`6da09e3`）`ci.sh --fast` 红：wave1 断言 `TestCreatorCanSetSettingAndWriteSkill` 与 `0bbf051` 的 skills live re-scan 行为冲突；worktree 已有 `modes_test.go` 修正 | 🟡 OPEN（待提交后复跑） | 独立验证 FIX-VERIFY-CORE2 §3.1 发现；Lead 提交 wave2 后必须在 `git archive HEAD` 副本重跑全量 ci（`sh scripts/ci.sh --fast` + `check-clean-checkout.sh`） |
| Preview | P3 | 视觉金标与 wave1/wave2 UI 漂移 | ✅ DONE（22 张） | 2026-11-05 重生成 22 张（当前代码，含设置窗 **900×840**、About 单一版本来源）+ `TestVisualRegression` PASS（14 屏/1.9s）；`identify` 校验尺寸；命令见 `docs/design/preview/README.md` 与 `make previews`/`make visual-check` |
| FP-18 | P2 | headless CLI 缺失 | ✅ DONE | `internal/app/headless.go` + cmd 接线；`--permission` 失效 P1 已由独立验证发现并修复（read-only/full 回归） |

## 3. 延期项（DEFERRED，本轮不做；理由见 FIX-PLAN §5）

- workflow 完整 JS 引擎、PTC Node/TS 运行时（与纯 Go / no-cgo 约束冲突）。
- ACP/SDK 完整协议、三平台代码签名/公证/自动更新基础设施（需证书与发布账号）。
- `internal/shell` 整体分层重构、schema v2 大迁移、JSONL 真源重写（高风险，分批）。
- 真机 IME/屏幕阅读器/DPI/通知矩阵（无真机环境）。
- 第三方插件市场（产品边界：仅内置插件）。

## 4. 如何更新本文件

1. 发现缺陷先写复现命令/测试名；修复后把状态改为 ✅ 并附证据（文件:行 或 `go test -run ...`）。
2. 每轮修复由 Lead 在 `docs/audit/FIX-REPORT.md` 汇总，本文件只保留"当前仍有/已修"的结论，不写过程。
3. 不允许再出现 `.cache/team/*.md` 这类未跟踪的缺陷积压（`docs/QUALITY.md` 的要求）。
4. 构建/发布类问题的回归守卫：`sh scripts/check-clean-checkout.sh`（干净 checkout 构建，BUILD-01）、`sh scripts/check-nocgo.sh`（零 cgo + 交叉编译）、`sh scripts/ci.sh`（全门禁）。
