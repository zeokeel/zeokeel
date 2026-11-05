# ZeoKeel 缺陷与待办清单（BUGS）

> 真源约定：本文件是"已知缺陷 / 未达标项 / 延期项"的**唯一落地清单**（见 `docs/QUALITY.md` 流程第 3 条）。
> 证据来源：`docs/audit/MASTER-AUDIT.md` 附录 B + 各分报告（只读）；修复记录与逐项验证：`docs/audit/FIX-REPORT.md`（Lead 汇总，两处交叉引用）。
> 状态图例：✅ DONE（代码落地且本文件给出可复核证据）/ 🟡 IN PROGRESS（本轮修复中，验收见 FIX-REPORT）/ ⏸ DEFERRED（本轮不做，理由见 §5）/ OPEN（未开始）。
> 更新：2026-11-05 · 基线 `HEAD=3100af0` + 审计修复轮；新增条目必须写"复现命令/测试链接"。

## 1. P0/P1（来自 MASTER-AUDIT 附录 B）

| 维度 | ID | 级别 | 问题 | 状态 | 修复/证据 |
| --- | --- | --- | --- | --- | --- |
| 安全 | RT-01 | P0 | workspace `mcp.json` 构建期无审批执行任意命令 | ✅ DONE | `mcp.workspace_config` 默认 `off`，仅 `allow` 放行；env `ZEOKEEL_MCP_WORKSPACE_CONFIG`；`internal/app/services.go`（`workspaceMCPConfigAllowed`）+ 设置解析测试 |
| 安全/权限 | RT-02 | P1 | automation/新会话不按自身权限重建 Agent | 🟡 IN PROGRESS | fix-backend（FIX-PLAN §2.2）；验收见 FIX-REPORT |
| 安全/权限 | RT-03 | P1 | Read Only 仍注册 schedule/memory/goal 且不审批 | 🟡 IN PROGRESS | FIX-PLAN §2.3 |
| 安全/数据 | RT-07 | P1 | spill 跟随 workspace symlink 写出工作区 | 🟡 IN PROGRESS | FIX-PLAN §2.4 |
| 可靠性 | RT-08 | P1 | 无重试/退避，429/瞬断整轮失败 | ✅ DONE | `internal/provider/openai/retry.go`；设置 `provider.retry_max_retries/retry_initial_ms/retry_max_ms`；流中途失败不重试（避免重复输出）仍见 known-limitations §5 |
| 并发 | RT-04 | P1 | 单 Agent 全局串行，后台回合/审批可阻塞前台 | 🟡 IN PROGRESS | FIX-PLAN §2.9（per-session agent 或 seam） |
| 团队 | RT-05 | P1 | Agent Teams 成员无 turnContext，gated 工具失败 | 🟡 IN PROGRESS | FIX-PLAN §2 后端 |
| 可靠性 | RT-06 | P1 | 模型流 60s 硬超时 | ✅ DONE | 默认无整体超时；`provider.timeout_seconds`（0=不限）、`provider.stream_idle_timeout_seconds`（默认 300s）；见 known-limitations §5 |
| 功能 | FP-01 | P1 | workflow 非 JS 编排引擎（固定 JSON 步骤） | 🟡 IN PROGRESS | 本轮做 JSON parallel/pipeline；完整 JS 引擎 ⏸（§3） |
| 功能 | FP-02 | P1 | PTC 只是工具批处理（无代码运行时） | ⏸ DEFERRED | 产品边界+纯 Go 约束；见 known-limitations §5 |
| 功能 | FP-03 | P1 | subagent 仅同步（无 childId/异步/续谈） | 🟡 IN PROGRESS | 本轮做异步 childId+通知 |
| 功能 | FP-04 | P1 | Goal 半成品（无 revision/phase/pause，无 `/goal`） | 🟡 IN PROGRESS | round driver 已在（`internal/session/goal_driver.go`）；补 id/revision/phase/pause + Controller API |
| 功能 | FP-05 | P1 | 无 workspace changes / 每回合 diff | 🟡 IN PROGRESS | 新建 `internal/workspacechanges`（FIX-PLAN §2.25） |
| 功能 | FP-06 | P1 | 无声明式 agent preset/persona | ⏸ DEFERRED | L 项，见 §3 |
| UX | UX-01 | P1 | 无模型发送完全静默 | 🟡 IN PROGRESS | fix-frontend（前端 P1 第一优先） |
| UX | UX-03 | P1 | 发送失败 toast 路径不一致（根因；键盘 Enter 静默） | 🟡 IN PROGRESS | fix-frontend |
| UX | UX-02 | P1 | 新会话对话 tab 无空态 | 🟡 IN PROGRESS | fix-frontend |
| 产品 | PD-01 | P1 | 无模型首启无引导 + 错误静默 | 🟡 IN PROGRESS | 与 UX-01 同根因 |
| 产品 | PD-02 | P1 | 添加模型无语义校验/预设/模型清单 | 🟡 IN PROGRESS | fix-backend+frontend |
| 产品 | PD-03 | P1 | 单实例固定端口、二次启动不激活窗口 | 🟡 IN PROGRESS | 后端改数据目录锁（FIX-PLAN §2.6，与 RT-12 合并） |
| 产品 | PD-04 | P1 | "迁移器"定位但零迁移实现 | 🟡 IN PROGRESS | **文案已降级**（README/STATUS/PLAN 改「同类替代，迁移路线图见本文件」）；最小导入器开发中：`internal/importer` 初版已落地（读 DSH `sessions/**/session.v4.jsonl[.zstd]` 与 credentials **键名**，写走 `internal/store`），CLI/UI 接线未接，落地前不宣称支持迁移 |
| 产品 | PD-05 | P1 | 设置面缺通知/隐私/诊断/代理/更新/重置 | 🟡 IN PROGRESS | fix-frontend |
| 产品 | PD-06 | P1 | Automation 失败/历史不可见、不绑定会话配置 | 🟡 IN PROGRESS | fix-backend+frontend |
| 产品 | PD-07 | P1 | MCP/Skills/AGENTS.md 无管理 UI | 🟡 IN PROGRESS | fix-frontend |
| 产品 | PD-09 | P1 | 失败回合直接展示内部错误栈、无建议/重试 | 🟡 IN PROGRESS | fix-frontend |
| 产品 | PD-10 | P1 | 重启不恢复会话、草稿仅内存 | 🟡 IN PROGRESS | FIX-PLAN §2 |
| 工程 | AR-01 | P1 | 发布版零崩溃诊断（无 recover/日志，Windows 丢 stderr） | ✅ DONE | `cmd/zeokeel/{main,logging}.go`：`<DataDir>/logs/app-YYYYMMDD.log`（0600、8MiB→`.1` 轮转、跨天换文件）+ MultiWriter(stderr,file) + `debug.SetCrashOutput` + recover 写 `crash-*.txt`；`go test ./cmd/...`（12 个用例） |
| 工程 | AR-02 | P1 | CI 只是本地脚本；race/coverage 覆盖不全、flaky 重跑 | ✅ DONE | `.github/workflows/ci.yml`（gate 跑 `scripts/ci.sh`；shell race 独立 job `-timeout 20m`）；`ci.sh` 写 `.cache/coverage.out` 并把 app 纳入 race；`scripts/evolve/gates.sh` 的 flaky 重跑改为 exit 1 不掩盖 |
| 工程 | AR-03 | P1 | 发布链路缺失（仅 Windows zip/无签名/无更新/版本未注入） | 🟡 PARTIAL | ✅ 版本注入（`-ldflags -X main.version/commit/buildTime` + `-s -w`，49.5→33.8MB）与 `package-linux`/`package-macos` 已落地；签名/公证/自动更新 ⏸（需证书/发布账号，§3） |

## 2. P2/P3 结构性债务（本轮修复中 / 延期）

| ID | 级别 | 问题 | 状态 | 去向 |
| --- | --- | --- | --- | --- |
| AR-04 | P2 | `internal/shell` God package 41.4k 行/UI 直连 store | ⏸ DEFERRED | L 项重构（FIX-PLAN §5） |
| AR-05 | P2 | 两套快照折叠（Projector 闲置） | 🟡 IN PROGRESS | fix-backend §2.15（收敛/删除） |
| AR-06 | P2 | 逐 token 持久化 + 无保留策略 | 🟡 IN PROGRESS | fix-backend §2.15（批量） |
| AR-07 | P2 | 排序依赖事件回放重建 Seq | 🟡 IN PROGRESS | fix-backend §2.15（`messages.ordinal` + 迁移） |
| AR-08 | P2 | UI 线程同步 DB（OpenSession/ListSessions） | OPEN | 前端/后端协同，未排本轮 |
| AR-09 | P2 | schema 演进冻结 v1 | 🟡 IN PROGRESS | fix-backend §2.15（迁移框架） |
| AR-17 | P2 | 测试盲区（store 62.8%/design 32.6%、无 fuzz） | 🟡 IN PROGRESS | CI 已记录 coverage；fuzz 待补 |
| AR-18 | P2 | 窗口首启/恢复未按 WorkArea 裁剪 | 🟡 IN PROGRESS | fix-backend §2.13（窗口 clamp） |
| AR-20 | P3 | `scrubEnv`/DDL 重复 | 🟡 IN PROGRESS | fix-backend §2.8（secret 名单 + 收敛） |
| AR-21/22 | P3 | 快照每 token 拷贝 / 大文件函数 | ⏸ DEFERRED | 与 AR-05 一起做 |
| PD-08 | P2 | 插件双界面不一致（4 vs 6）、无功能实验开关 | 🟡 IN PROGRESS | fix-frontend |
| AR-16/文档 | — | 已知缺陷此前无落地清单（本文件缺失） | ✅ DONE | 本文件；STATUS/README/PLAN/known-limitations 同轮纠错 |
| FP-18 | P2 | headless CLI 缺失 | ✅ DONE | `internal/app/headless.go` 的 `RunHeadless`（0/1/2 退出码、`--json`/`--yes`/`--model`）+ `cmd/zeokeel` 接线（`headlessRun = app.RunHeadless`）；`zeokeel run --help`/无 prompt 行为有 cmd 级测试（`go test ./cmd/...`） |

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
