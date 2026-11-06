# 状态（唯一状态真源）

> 更新：2026-11-06（Round 3 收尾 + 模型配置/推理强度 parity + **1.0 功能缺口统一收口**）· 基线 `HEAD=20e98d4`；此后并发提交以 `git log` 为准。
> 本文件只写**当前状态与验证结果**，不写过程。历史流水已归档：[docs/history/STATUS-journal.md](history/STATUS-journal.md)（不再维护）。
> 缺陷状态：[docs/BUGS.md](BUGS.md) · 审计证据：[docs/audit/](audit/) · 已知限制：[docs/design/known-limitations.md](design/known-limitations.md) · 规划：[docs/PLAN.md](PLAN.md)。

## 1. 产品定位

- **DSH 同类替代**：独立品牌桌面 Agent，功能对齐 DSH；**不是已可用的迁移器**——目前是"最小导入（技术预览 CLI）"：`zeokeel import` 只导入**文本对话**（工具/图片/附件/trajectory 不导入，凭据只读键名，subagent 默认跳过），建议先 `--dry-run`；见 [BUGS PD-04](BUGS.md) 与 [FIX-VERIFY-PRODUCT §2](audit/FIX-VERIFY-PRODUCT.md)。
- 纯 Go、`CGO_ENABLED=0`、单二进制、单进程；UI 只用 mygo 原生控件（无 webview）；插件只内置。
- 不可信 workspace 内容默认不执行：`<root>/mcp.json` 需显式 `mcp.workspace_config=allow`（默认 `off`，`on/true/1` 一律 fail-closed；RT-01）。
- **明确不做（产品决定，2026-11-06）**：PTC 代码运行时、语音输入——不提供入口，**不是能力缺口/后续项**（known-limitations §2.1）。

## 2. 已实现并可用（代码在、测试在）

| 域 | 能力 |
| --- | --- |
| 桌面壳 | 主窗口 + 设置窗口（单例非模态）+ 托盘 + 关窗隐藏 + 单实例 + 窗口状态持久化；无原生菜单栏（ADR 0006） |
| 会话 | 流式对话（SSE）、多轮历史、SQLite 持久化、重开恢复、自动/LLM 标题、fork、JSONL 导出、反馈 |
| Agent | eino ADK 工具回路；**声明式 Presets**（`internal/presets`：内置 standard/minimal/creator 等 + 用户 JSON 字段级覆盖 + Presets 页 + 用户指南）；**ask_user 交互阻塞等待回答**（FP-16；headless/ACP 降级 pending）；压缩（LLM 摘要 + 工具裁剪）；checkpoint（回合边界）；Goal round driver（自动续轮，预算 6/cap 12）；subagent/Agent Team；workflow（JSON 步骤）；ralph（**PTC 明确不做，已移除**） |
| 工具 | fs（read/write/edit/editor/glob/grep）、bash/pwsh（平台门控）、terminal（Unix PTY）、jobs、web_search、web_fetch、read_image、todo、ask_user、present、report、session_query、schedule_*、skill、inspect、worktree、working_directory、MCP（stdio + resources/prompts） |
| 审批/权限 | Full/Workspace/Read-only/Ask + Always allow；Auto-review EXP（LLM 预审，15s 超时回退人工）；审批范围 ADR 0010 |
| 数据 | `index.sqlite`（WAL/foreign_keys/busy_timeout/单写者）为持久化真源；每会话 JSONL 镜像/导出；附件与工具结果落盘；搜索用 FTS5 trigram（LIKE 降级）；**DSH 迁移导入器**：`internal/importer` + `zeokeel import`（读取 DSH `sessions/**/session.v4.jsonl[.zstd]`，只读 DSH，凭据只读键名）；UI 入口为设置→数据与隐私→「从 DSH 导入」命令弹窗（复制命令，非应用内执行） |
| 诊断/发布 | 版本注入（`--version` 输出 version/commit/buildTime）+ `-s -w`（约 -32% 体积）；日志 `logs/app-YYYYMMDD.log`（轮转）+ panic `logs/crash-*.txt`；Windows zip / Linux tar.gz / macOS unsigned `.app` 打包；**发布流水线**：`make release` / `release.yml`（tag→三平台包+SHA256SUMS+GitHub Release，未签名）；自动更新：引擎 `internal/updater` + `zeokeel update` CLI + 设置页「检查更新 / 下载并安装」（install-on-restart）已交付；托管 feed/签名 DEFERRED |
| CLI | 桌面（默认）、`--version`/`--help`、`run [--json] [--yes] "<prompt>"` headless（退出码 0/1/2，无 GUI/托盘/单实例锁）、`import [--from --dry-run --json --max --manifest/--no-manifest]`（DSH 会话迁移；幂等 manifest；凭据只列键名不读值；失败隔离）、`acp`（ACP v1 stdio server，编辑器/客户端）、`update [--check|--download|--install] [--json]`（自动更新 CLI） |
| CI | `.github/workflows/ci.yml`：gate job 跑 `scripts/ci.sh`（gofmt/vet/build/test/零 cgo/coverage/race），shell race 单独 job `-timeout 20m` |

## 2.1 wave 2 已实现（工作树，待提交/独立复核）

- 产品面：PD-05 设置补页（通知/数据隐私/诊断/网络/桌面/更新/重置）、PD-06 Automation 运行历史/失败/绑定、PD-07 MCP/Skills/AGENTS 管理、PD-10 会话/草稿恢复、PD-12 关闭行为/自启、首启 setup 卡、About 版本（残留重复版本行）、context budget 设置行。
- 后端/功能：FP-19 统一代理、FP-04 Goal 模型（phase/revision）、FP-07 schedule 投回原会话、FP-15 job 完成通知、FP-01 workflow JSON parallel/pipeline、FP-03 subagent 异步、FP-05 workspace changes、PD-04 `zeokeel import` CLI + 独立验收（技术预览）；FP-11/FP-16/FP-17 见 known-limitations §3.9。
- 视觉：22 张预览金标已按 wave1+wave2 UI 重生成（含设置窗 900×840、About 单一版本来源），`TestVisualRegression` PASS（14 屏/1.9s）。

## 2.2 Round 3 已实现（已提交 `20e98d4`）

- **产品功能**：FP-06 声明式 Presets（`internal/presets` + Presets 页 + 用户指南）；FP-16 `ask_user` 交互会话阻塞等待回答（headless/ACP 降级 pending）；自动更新 GUI 检查+下载+安装（install-on-restart；app `update_install.go`）；全局快捷键：Windows + Linux/X11 已实现（Linux 用 purego dlopen libX11，无 cgo）；macOS Carbon/purego 代码已实现（compile-only，待真机验证）；Wayland DEFERRED；ACP v1 最小服务器 + `zeokeel acp` + adapter/e2e。
- **数据/工程**：AR-07 `messages.ordinal` 持久化排序；AR-09 schema v2（有序迁移 + `backups/` 快照 + 旧版 downgrade 拒绝）；AR-20 `scrubEnv`/DDL 重复收敛；AR-05 快照折叠收敛（Controller 单一折叠，`state.Projector` deprecated，每事件折叠 2→1；app/services.go 4 行死代码已删）；UX-04 设置窗重绘 **CONFIRMED FIXED**（Linux/Xvfb 无 WM；真实 WM spot check）；AR-04 **IN PROGRESS**：P1（read-model/async 拆分）完成 + wave2/3 模块化；i18n 抽离完成（新包 `internal/i18n` 552 生产行；shell `i18n.go` 660→190、生产 LOC −470）；P2–P4 按 `docs/design/shell-refactor-plan.md` 计划（未实施）。
- **发布**：`.github/workflows/release.yml`（tag→三平台矩阵包+SHA256SUMS+GitHub Release）+ `make release`；三平台交叉编译持续验证。

## 2.3 模型配置与推理强度 parity（工作树）

- **内置模型目录**（`internal/modelcatalog`）：DeepSeek（`deepseek-flash` / `deepseek-v4-pro`，1,000,000 上下文、384,000 输出，推理档 off/low/high/max）、OpenAI、Moonshot AI（含 CN）、Z.AI、Groq、Together、OpenRouter、Ollama（本地免 key）。设置 › 模型 › 添加模型默认打开在 DeepSeek 配置（端点、模型 id、上下文窗口、默认推理强度、名称均已预填），只需粘贴 API key；其他供应商与自定义端点一次切换即可，内置模型也可改选目录中的其他模型。
- **composer 推理强度菜单**：输入框下方模型菜单新增「推理强度」段（模型默认 + 该模型公布的档位；无论文模型不显示）。Session 级选择持久化到 `session.<id>.reasoning_effort` 并经 `Provider.SetReasoningEffort` 下一回合生效（无需重建 agent）；Home 的选择暂存到新会话；切换模型自动回到该模型默认档。
- **上下文窗口**：`store.Model.context_window`（Settings 行显示 `1M ctx` 等）；会话 agent 的压缩预算按模型窗口的 80% 设置（预留输出/系统提示），模型无窗口时回退全局 `agent.context_budget_tokens`。
- 新增/更新测试：`internal/modelcatalog`、store 上下文窗口往返/容错、provider 动态强度、runtime `SetReasoningEffort`/`SetContextBudget`、session 会话强度与预算、shell 对话框目录预填/校验、composer 强度菜单与 Home 暂存。
- 附带修复（门禁发现）：Updates 页异步状态与渲染的并发读写（基线 `3565c97` 的 shell race job 红）——`updateMu` + 一致快照，见 BUGS AR-23；修复后 `CGO_ENABLED=1 go test -race -timeout 20m ./internal/shell` PASS（462.8s）。

## 2.4 1.0 功能缺口「统一收口」轮（2026-11-06）

> 详细契约/差异/证据：[docs/audit/FIX-REPORT-CLOSURE.md](audit/FIX-REPORT-CLOSURE.md)；RED→GREEN 摘要：[TDD-RED-EVIDENCE.md](audit/TDD-RED-EVIDENCE.md) §4。

- **交付 13 项**：①工作区变更 inline diff（捕获/统一 diff/事件持久化/可展开 UI/回放）；②`/goal` 命令；③`/compact` 手动压缩 + **会话级持久替换**；④`/goal edit` 保持 goal 身份；⑤`@` 提及细节（`@"带空格路径"` 含引号/反斜杠转义与往返、chevron 下钻、面包屑回跳、跨工作区会话行详情）；⑥会话引用原子化（canonical mention + 受限只读快照作第二条 user 消息注入，Chat 折叠显示）；⑦崩溃中断回合修复（checkpoint consumer 幂等收尾，不重放副作用）；⑧ACP 完整面（list/load/authenticate/fs/terminal/request_permission）；⑨LSP 工具 + 插件页开关 + inspect 配置键；⑩Wayland 全局快捷键（GlobalShortcuts portal，纯 Go D-Bus）；⑪导入器收尾（运行中检测/图片附件迁移/manifest v2/失败回滚）；⑫Chat 流式思考展示与折叠（DSH ReasoningRow 对齐，用户追加）；⑬独立审查 P2 修复 7 项 + REVIEW-3 P1/P2 修复。
- **明确不做/非对齐项**：反馈联网上送、模型配置剩余限制（用户指定）；PTC/语音（产品决定）；auto-memory、computer-use（经核实 DSH 0.2.1-rc.2 无对应包/能力，非对齐项，见 FIX-REPORT-CLOSURE §0）。
- **质量**：每项 TDD（RED→GREEN 留档）；3 组独立审查（REVIEW/REVIEW-2/REVIEW-3，P2 全修、REVIEW-3 P1×2 修复并复验）；最终 `sh scripts/ci.sh` PASS（coverage 80.3%，含 session race 233.6s）、`CGO_ENABLED=1 go test -race ./internal/shell` PASS（528.1s）、Windows/darwin 交叉 build PASS；提交 `f204755`/`63617af` 后 `check-clean-checkout.sh` PASS + `ci.sh --fast` PASS。
- **收口时修正的过期口径**：BUGS RT-04（per-session Agent 已落地）、FP-05（UI 卡片本轮补 diff）、FP-11（本轮补 consumer）均已关闭；FUNCTIONAL-PARITY 修正 auto-memory/computer-use 的 DSH 归属。
- **残余/未验**：真实 GNOME/KDE portal、真实 gopls、真机 IME/a11y/DPI/通知/安装包、签名/公证与托管 feed（DEFERRED）——见 known-limitations。

## 3. 门禁（"绿"的定义）

```sh
export PATH=/usr/local/go/bin:$PATH GOCACHE=/data/mygo/.cache/go-build GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0
sh scripts/ci.sh                  # gofmt/vet/build/test/零 cgo/coverage/race（核心包）
sh scripts/check-clean-checkout.sh # 干净 checkout 构建（git archive HEAD；BUILD-01 守卫）
make check                        # fmt+vet+test+check-nocgo
CGO_ENABLED=1 go test -race -timeout 20m ./internal/shell   # 单独跑（约 8-9 分钟）
sh scripts/evolve/gates.sh --full # 统一门禁；flaky 重跑按失败处理
```

- 基线（Lead @3100af0）：`go build ./...` / `go vet ./...` / `go test -count=1 ./...` 全绿；包数随迭代变化（`go list ./... | wc -l`，当前 21，含新增 `internal/importer`）。
- CI 记录 coverage 到 `.cache/coverage.out`（artifact），暂不设阈值（[BUGS AR-17](BUGS.md)）。
- 修复期间并发编辑会让树短暂不绿；以每波 `docs/audit/FIX-REPORT.md` 的复跑结果为准。

## 4. 未达标 / 修复中

以 [docs/BUGS.md](BUGS.md) 为唯一清单（P0/P1 状态、IN PROGRESS、DEFERRED 与证据）。当前重点：

1. P0 RT-01 workspace MCP 默认不执行 —— **代码已落地**（`mcp.workspace_config` 默认 off），待全量验证。
2. P1 安全/权限（RT-02/03/07/12、PD-03/05/06/07/09/10）与 UX（UX-01/02/03、PD-01）——本轮修复中。
3. 发布链路：签名/公证与托管 update feed 为外部依赖（DEFERRED）；自动更新引擎、`zeokeel update` CLI、设置页检查+下载+安装已交付；Windows 安装器为 Inno/NSIS 可执行草案（未编译）。
4. 迁移定位已降级；**最小导入器 + `zeokeel import` CLI 已交付**（只读 DSH、凭据只列键名）；UI 入口为设置→数据与隐私→「从 DSH 导入」**命令弹窗**（展示/复制 `zeokeel import` 命令，提示先退出应用；非应用内执行）（PD-04，技术预览）。
5. 结构性债务：AR-04 **IN PROGRESS**（P1 read-model/async 已完成、i18n 抽离已完成（shell −470 行）、P2–P4 按 `docs/design/shell-refactor-plan.md` 计划）；AR-05 快照折叠已收敛（Controller 单一折叠，Projector deprecated）；AR-08 UI 线程同步 DB 仍 OPEN；UX-04 已 CONFIRMED FIXED（Linux/Xvfb；真实 WM spot check）。

## 5. 已知限制与真机项

- 无真机 Windows/macOS：IME/屏幕阅读器/DPI/通知/安装包行为只能静态核对，发布前必须真机矩阵（[known-limitations §1](design/known-limitations.md)）。
- 流中途失败不重试（已显示文本无法撤回，需重发）；流空闲超时/重试设置见 known-limitations §5。
- 结构性债务：shell God package AR-04（IN PROGRESS：P1 完成、i18n 抽离完成（shell −470 行）、P2–P4 按计划）、逐 token 持久化 AR-06（PARTIAL）；AR-05（快照折叠收敛为 Controller 单一折叠/Projector deprecated）、AR-07/AR-09/AR-20 已 DONE——见 BUGS。
- wave1/2 独立验证残余（见 BUGS 与 [known-limitations §3.8](design/known-limitations.md)）：About 双版本号、PD-02b 无 key 本地 provider、PD-04 导入质量 5 项、wave2 产品面待提交后复核；UX-04 设置窗重绘已 CONFIRMED FIXED（Linux/Xvfb，真实 WM spot check）。
- 视觉回归为 opt-in 工具（`scripts/visual-check.sh`），CI 不强制。
- 统一收口轮的已记录差异：会话引用在 composer 为明文 canonical mention（mygo TextArea 无内联芯片）、注入行 Chat 折叠显示；思考行完成态仅「已思考」（无 settled preview）、shimmer 为透明度脉冲、展开正文 mono；`/compact` summary 以 assistant 行持久化（识别语义见 FIX-REPORT-CLOSURE）；LSP 冷启动 server 无连接池/未真机 gopls；Wayland 真实 GNOME/KDE portal 未验（fake portal 测试通过）。

## 6. 下一步（按投入产出）

1. 完成本轮 P0/P1 修复与独立验证（FIX-REPORT）；确保 CI 在干净树上绿。
2. 迁移导入质量（PD-04 wave3）：过滤 DSH 注入上下文、按 turn 聚合 step 消息、幂等/去重、图片统计（均已补）、运行中实例检测/旧 manifest 兼容；导入 UI 为命令弹窗，非应用内执行；补齐后才可称"支持迁移"。
3. 发布前真机矩阵 + 签名/公证与托管 update feed（DEFERRED，需外部条件；引擎/CLI/GUI 更新链路已交付）；真实 GNOME/KDE portal、真实 gopls 冒烟，见 known-limitations §1。
4. 结构性债务分批：AR-05 已收敛（Controller 单一折叠）；剩余 AR-06 events 保留策略/AR-22 快照 O(n) 拷贝 → AR-04 shell 分层（P1 已完成、i18n 抽离已完成（shell −470 行）；P2–P4 按 `shell-refactor-plan` 推进，L 项整体重构未实施）。
