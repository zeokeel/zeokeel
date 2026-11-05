# 状态（唯一状态真源）

> 更新：2026-11-05（审计修复轮）· 基线 `HEAD=3100af0`；此后并发提交以 `git log` 为准。
> 本文件只写**当前状态与验证结果**，不写过程。历史流水已归档：[docs/history/STATUS-journal.md](history/STATUS-journal.md)（不再维护）。
> 缺陷状态：[docs/BUGS.md](BUGS.md) · 审计证据：[docs/audit/](audit/) · 已知限制：[docs/design/known-limitations.md](design/known-limitations.md) · 规划：[docs/PLAN.md](PLAN.md)。

## 1. 产品定位

- **DSH 同类替代**：独立品牌桌面 Agent，功能对齐 DSH；**不是已可用的迁移器**——DSH 会话/配置导入仍在开发中（`internal/importer` 初版；CLI/UI 未接线，[BUGS PD-04](BUGS.md)），落地前不宣称"迁移器"。
- 纯 Go、`CGO_ENABLED=0`、单二进制、单进程；UI 只用 mygo 原生控件（无 webview）；插件只内置。
- 不可信 workspace 内容默认不执行：`<root>/mcp.json` 需显式 `mcp.workspace_config=allow`（默认 `off`，`on/true/1` 一律 fail-closed；RT-01）。

## 2. 已实现并可用（代码在、测试在）

| 域 | 能力 |
| --- | --- |
| 桌面壳 | 主窗口 + 设置窗口（单例非模态）+ 托盘 + 关窗隐藏 + 单实例 + 窗口状态持久化；无原生菜单栏（ADR 0006） |
| 会话 | 流式对话（SSE）、多轮历史、SQLite 持久化、重开恢复、自动/LLM 标题、fork、JSONL 导出、反馈 |
| Agent | eino ADK 工具回路；Standard/PTC/Minimal/Creator；压缩（LLM 摘要 + 工具裁剪）；checkpoint（回合边界）；Goal round driver（自动续轮，预算 6/cap 12）；subagent/Agent Team；workflow（JSON 步骤）；ralph |
| 工具 | fs（read/write/edit/editor/glob/grep）、bash/pwsh（平台门控）、terminal（Unix PTY）、jobs、web_search、web_fetch、read_image、todo、ask_user、present、report、session_query、schedule_*、skill、inspect、worktree、working_directory、MCP（stdio + resources/prompts） |
| 审批/权限 | Full/Workspace/Read-only/Ask + Always allow；Auto-review EXP（LLM 预审，15s 超时回退人工）；审批范围 ADR 0010 |
| 数据 | `index.sqlite`（WAL/foreign_keys/busy_timeout/单写者）为持久化真源；每会话 JSONL 镜像/导出；附件与工具结果落盘；搜索用 LIKE（FTS 未做） |
| 诊断/发布 | 版本注入（`--version` 输出 version/commit/buildTime）+ `-s -w`（约 -32% 体积）；日志 `logs/app-YYYYMMDD.log`（轮转）+ panic `logs/crash-*.txt`；Windows zip / Linux tar.gz / macOS unsigned `.app` 打包 |
| CLI | 桌面（默认）、`--version`/`--help`、`run [--json] [--yes] "<prompt>"` headless（退出码 0/1/2，无 GUI/托盘/单实例锁） |
| CI | `.github/workflows/ci.yml`：gate job 跑 `scripts/ci.sh`（gofmt/vet/build/test/零 cgo/coverage/race），shell race 单独 job `-timeout 20m` |

## 3. 门禁（"绿"的定义）

```sh
export PATH=/usr/local/go/bin:$PATH GOCACHE=/data/mygo/.cache/go-build GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0
sh scripts/ci.sh                  # gofmt/vet/build/test/零 cgo/coverage/race（核心包）
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
3. 发布链路：签名/公证/自动更新仍缺（需证书/发布账号，DEFERRED）。
4. 迁移定位已降级；最小导入器 `internal/importer` 开发中（CLI/UI 接线待做，PD-04）。

## 5. 已知限制与真机项

- 无真机 Windows/macOS：IME/屏幕阅读器/DPI/通知/安装包行为只能静态核对，发布前必须真机矩阵（[known-limitations §1](design/known-limitations.md)）。
- 流中途失败不重试（已显示文本无法撤回，需重发）；流空闲超时/重试设置见 known-limitations §5。
- 结构性债务（shell God package AR-04、双快照折叠 AR-05、逐 token 持久化 AR-06、Seq 回放排序 AR-07）见 BUGS。
- 视觉回归为 opt-in 工具（`scripts/visual-check.sh`），CI 不强制。

## 6. 下一步（按投入产出）

1. 完成本轮 P0/P1 修复与独立验证（FIX-REPORT）；确保 CI 在干净树上绿。
2. 迁移最小导入器（PD-04）→ README/STATUS 再升级为"支持最小导入"。
3. 发布前真机矩阵 + 签名/公证/自动更新基建（DEFERRED，需外部条件）。
4. 结构性债务分批：AR-05/06/07 数据层收敛 → AR-04 shell 分层（L）。
