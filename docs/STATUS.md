# 状态（唯一状态真源）

> 更新：2026-11-05（审计修复轮）· 基线 `HEAD=3100af0`；此后并发提交以 `git log` 为准。
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
| Agent | eino ADK 工具回路；Standard/Minimal/Creator（**PTC 明确不做，已移除**）；压缩（LLM 摘要 + 工具裁剪）；checkpoint（回合边界）；Goal round driver（自动续轮，预算 6/cap 12）；subagent/Agent Team；workflow（JSON 步骤）；ralph |
| 工具 | fs（read/write/edit/editor/glob/grep）、bash/pwsh（平台门控）、terminal（Unix PTY）、jobs、web_search、web_fetch、read_image、todo、ask_user、present、report、session_query、schedule_*、skill、inspect、worktree、working_directory、MCP（stdio + resources/prompts） |
| 审批/权限 | Full/Workspace/Read-only/Ask + Always allow；Auto-review EXP（LLM 预审，15s 超时回退人工）；审批范围 ADR 0010 |
| 数据 | `index.sqlite`（WAL/foreign_keys/busy_timeout/单写者）为持久化真源；每会话 JSONL 镜像/导出；附件与工具结果落盘；搜索用 FTS5 trigram（LIKE 降级）；**DSH 迁移导入器**：`internal/importer` + `zeokeel import`（读取 DSH `sessions/**/session.v4.jsonl[.zstd]`，只读 DSH，凭据只读键名） |
| 诊断/发布 | 版本注入（`--version` 输出 version/commit/buildTime）+ `-s -w`（约 -32% 体积）；日志 `logs/app-YYYYMMDD.log`（轮转）+ panic `logs/crash-*.txt`；Windows zip / Linux tar.gz / macOS unsigned `.app` 打包 |
| CLI | 桌面（默认）、`--version`/`--help`、`run [--json] [--yes] "<prompt>"` headless（退出码 0/1/2，无 GUI/托盘/单实例锁）、`import [--from --dry-run --json --max --manifest/--no-manifest]`（DSH 会话迁移；幂等 manifest；凭据只列键名不读值；失败隔离） |
| CI | `.github/workflows/ci.yml`：gate job 跑 `scripts/ci.sh`（gofmt/vet/build/test/零 cgo/coverage/race），shell race 单独 job `-timeout 20m` |

## 2.1 wave 2 已实现（工作树，待提交/独立复核）

- 产品面：PD-05 设置补页（通知/数据隐私/诊断/网络/桌面/更新/重置）、PD-06 Automation 运行历史/失败/绑定、PD-07 MCP/Skills/AGENTS 管理、PD-10 会话/草稿恢复、PD-12 关闭行为/自启、首启 setup 卡、About 版本（残留重复版本行）、context budget 设置行。
- 后端/功能：FP-19 统一代理、FP-04 Goal 模型（phase/revision）、FP-07 schedule 投回原会话、FP-15 job 完成通知、FP-01 workflow JSON parallel/pipeline、FP-03 subagent 异步、FP-05 workspace changes、PD-04 `zeokeel import` CLI + 独立验收（技术预览）；FP-11/FP-16/FP-17 见 known-limitations §3.9。
- 视觉：22 张预览金标已按 wave1+wave2 UI 重生成（含设置窗 900×840、About 单一版本来源），`TestVisualRegression` PASS（14 屏/1.9s）。

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
3. 发布链路：签名/公证/自动更新仍缺（需证书/发布账号，DEFERRED）；自动更新引擎 `internal/updater` 仅有 TDD RED 契约（`-tags tdd_red`，本轮不实现），Windows 安装器为 Inno/NSIS 计划草案（未构建）。
4. 迁移定位已降级；**最小导入器 + `zeokeel import` CLI 已交付**（只读 DSH、凭据只列键名），设置页「从 DSH 导入」入口已提供（PD-04，技术预览）。

## 5. 已知限制与真机项

- 无真机 Windows/macOS：IME/屏幕阅读器/DPI/通知/安装包行为只能静态核对，发布前必须真机矩阵（[known-limitations §1](design/known-limitations.md)）。
- 流中途失败不重试（已显示文本无法撤回，需重发）；流空闲超时/重试设置见 known-limitations §5。
- 结构性债务（shell God package AR-04、双快照折叠 AR-05、逐 token 持久化 AR-06、Seq 回放排序 AR-07）见 BUGS。
- wave1/2 独立验证残余（见 BUGS 与 [known-limitations §3.8](design/known-limitations.md)）：UX-04 设置窗重绘、About 双版本号、PD-02b 无 key 本地 provider、PD-04 导入质量 5 项、wave2 产品面待提交后复核。
- 视觉回归为 opt-in 工具（`scripts/visual-check.sh`），CI 不强制。

## 6. 下一步（按投入产出）

1. 完成本轮 P0/P1 修复与独立验证（FIX-REPORT）；确保 CI 在干净树上绿。
2. 迁移导入质量（PD-04 wave3）：过滤 DSH 注入上下文、按 turn 聚合 step 消息、幂等/去重、图片统计、设置页导入入口；补齐后才可称"支持迁移"。
3. 发布前真机矩阵 + 签名/公证/自动更新基建（DEFERRED，需外部条件）。
4. 结构性债务分批：AR-05/06/07 数据层收敛 → AR-04 shell 分层（L）。
