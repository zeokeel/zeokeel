# 审计修复报告（FIX-REPORT，渐进更新）

> 基线：`HEAD=3100af0`（审计修复起点）→ **wave 1 提交 `1da4f12`**（2026-11-05 23:4x）
> 范围：`docs/audit/MASTER-AUDIT.md` 的 P0/P1 全部 + 可行 P2/P3（L 项延期理由见文末）
> 验证口径：每个修复有回归测试或可复核探针；wave 1 全量门禁见 §4
> 状态：✅ FIXED / 🟡 PARTIAL（说明剩余）/ ⏸ DEFERRED / 🧪 待独立验证（编号见 docs/audit/FIX-VERIFY-BACKEND.md）

## 1. 安全 / 权限 / 数据完整性（全部 ✅ 且经独立验证 CONFIRMED，除非另注）

| ID | 修复内容 | 关键文件/证据 |
| --- | --- | --- |
| RT-01 P0 | workspace `<root>/mcp.json` **默认不执行**（`mcp.workspace_config=off`；仅 `allow` 放行；`mcp.servers` 优先；失败/忽略可见日志；args 脱敏、source 区分；inspect 标注门状态；Creator `settings_set` 写白名单移除 mcp.*/provider.base_url/approval.tools 等执行类键） | `internal/app/services.go`、`internal/mcp/workspace.go`、`internal/tools/inspect.go`、`internal/runtime/settings.go`；`mcp_workspace_gate_test.go` 等；独立验证 17 探针 CONFIRMED |
| RT-02 P1 | `submit` 持 bindMu 重新 pin 目标会话后按目标权限重建 Agent；重建失败拒绝回合 | `internal/session/controller.go`；`submit_permission_test.go`；独立 CONFIRMED |
| RT-03 P1 | Read Only 移除 `schedule_create/update/delete`、`memory`、`goal`（保留只读 list），prompt 与工具集一致 | `internal/app/services.go`；`readonly_tools_test.go`；CONFIRMED |
| RT-07 P1 | 工具结果 spill 逐级 `Lstat` 拒 symlink + `O_EXCL`，失败回退原文 | `internal/session/tool_spill.go`；3+5 组回归；CONFIRMED |
| RT-11 P2 | 数据目录 0700、`index.sqlite`/wal/shm 显式 0600 | `internal/store/store.go`；`permissions_test.go`；CONFIRMED |
| RT-12/PD-03 P1 | 单实例改为 **每 data-dir 锁文件**（Unix flock / Windows LockFileEx）+ 二次启动 IPC（token 校验）激活已有窗口 + stderr 带 data dir/pid；旧固定端口降级为 best-effort | `internal/app/singleinstance*.go`、`app.go`；Xvfb 双实例 e2e + Windows 交叉编译；CONFIRMED |
| RT-13 P2 | 终态事件投递由"可丢"改为有界等待（2s）；审批 early 决策 5min TTL + 256 上限 | `internal/runtime/sink.go`、`approval.go`；5 测试；CONFIRMED |
| RT-14 P2 | auto-review 不再把 workspace `AGENTS.md` 当授权证据；`user_request` 唯一授权，constraints 只可收窄 | `internal/app/services.go`、`internal/runtime/review.go`；CONFIRMED |
| RT-15 P3 | 子进程/MCP 环境清洗统一 `winproc.ScrubEnv`，覆盖第三方凭据（*_KEY/*_TOKEN/*_SECRET/*_PASSWORD、AWS/GITHUB/OPENAI/ANTHROPIC、SSH_AUTH_SOCK、KUBECONFIG 等） | `internal/winproc/env.go`、`tools/bash.go`、`mcp/mcp.go`；CONFIRMED |
| FP-08 P2 | `write_file` 原子写（tmp+fsync+rename+dir fsync，保留 mode）＋ `edit_file` read-before-write（会话级 hash/stale 检测） | `internal/tools/fs.go`；`fs_atomic_test.go`；独立冒烟 |
| FP-18 P2/P1 | headless CLI：`internal/app/headless.go`（`RunHeadless(args,stdout,stderr) int`，`--json/--yes/--permission/-p`，0/1/2 退出码，非交互默认 deny）+ `cmd/zeokeel` 接线；**并修复验证者发现的 `--permission` 失效 P1**（read-only 曾可写文件） | `internal/app/headless.go`、`cmd/zeokeel/cli.go`；真 SSE e2e + 独立复验 PASS |

## 2. 运行时可靠性 / 会话与上下文

| ID | 修复内容 | 关键文件/证据 |
| --- | --- | --- |
| RT-05 P1 | Agent Team 成员通过 `WithTurnApprovalFrom` 继承发起回合的审批上下文（不继承取消）；父回合结束仍 fail-closed | `internal/runtime/subagent.go`、`internal/agentteam/manager.go`；8 测试 |
| RT-09 P2 | `@daily/@hourly` 按**本地时区**计算，持久化归一 UTC；工具描述同步 | `internal/automation/scheduler.go`、`tools/schedule.go`；3 测试 |
| RT-10/AR-06 P2 | 流式 token/reasoning 批量持久化（8KB/200ms，边界事件先 flush 保序；store `AppendEvents` 单事务 + transcript 单 fsync） | `internal/session/event_batch.go`、`store/{event,transcript}.go`；8+4 测试 |
| FP-09 P2 | SQLite **FTS5 trigram** 会话全文搜索（增量维护、bm25 排名、标题优先、短查询 LIKE 降级、删除清索引） | `internal/store/session_search.go`；8 测试 |
| FP-10 P2 | CJK rune 感知 token 估算、图片按张计费并 offload 旧图占位、`context.budget_tokens` 可配置 | `internal/runtime/compaction*.go`、`internal/app/services.go`；4+测试 |
| FP-11 P2 | checkpoint 屏障：模型请求前 + 有副作用工具执行前落盘（fail-closed），屏障态可恢复 | `internal/runtime/checkpoint.go`、`agent.go`、`model_adapter.go`；6 测试 |
| FP-13 P2 | MCP **streamable-http** 传输（Transport/URL/Headers，校验/日志/鉴权头） | `internal/mcp/mcp.go`；httptest 真连测试 |
| AR-18 P2 | 主窗口/设置窗口创建与恢复按当前屏幕 **WorkArea clamp** | `internal/app/window_geometry.go`、`window_state.go`；11 headless 几何测试 |
| RT-06/RT-08 | 审计期间并发提交已修（默认无整体流超时 + idle 超时 + 重试/退避）；本轮复核无回归 | `provider/openai/{retry,idle}.go`；独立验证 PASS |

## 3. UI / UX / 产品面（wave 1）

| ID | 修复内容 | 证据 |
| --- | --- | --- |
| UX-01/UX-03/PD-01 | 键盘 Enter 发送失败改 deferred toast（build 外 queueToast 下帧显示）；失败**回滚空会话**、保留 draft 与暂存附件；`/plan` 失败同样回滚 | `shell/home.go`、`session_live.go`、`composer_menus.go`；`send_feedback_test.go` 5 测试 |
| UX-02 | 会话「对话」空态（图标+引导+无模型 CTA→Settings>Models） | `shell/session_live.go`；2 测试 |
| PD-02 | 添加模型 Provider 预设（DeepSeek/OpenAI/Ollama/自定义）+ 异步「测试连接」（GET /models，12s，可注入） | `shell/models_ui.go`；2 测试 |
| PD-09 | 失败回合产品化卡片：错误分类（auth/限流/网络/上下文/模型/通用）+ 动作（打开设置/重试）+ 技术细节折叠 | `shell/session_errors.go`；4 测试 |
| PD-11/PD-17/PD-18/UX-05/UX-09 | Home 返回入口（侧栏+rail）、工作区 root 校验（存在/目录/可写/绝对路径 + 创建目录）、模型删除二次确认、工作区对话框可见 label、侧栏空态 | `shell/{chrome,workspaces,models_ui}.go`；6 测试 |
| UX-04/UX-06/UX-08/UX-10/UX-11/UX-13/PD-08 | popover 生命周期、设置窗口高度 840、rail 会话 flyout、@ 会话行本地化、权限 chip tooltip、Start 页整治、插件页统一（6 项+实验项标注） | shell 多文件；10+ 测试 |
| PD-04 | 「迁移器」文案降级为「同类替代+迁移路线图」；`internal/importer` 新包（DSH session JSONL/zstd + credentials 键名 → store），dry-run/失败隔离，10 测试 | `internal/importer/`、README/STATUS/PLAN；CLI/UI 接线待下轮 |

## 4. 工程 / 发布 / 文档

| 项 | 结果 |
| --- | --- |
| AR-01 崩溃诊断 | 日志落盘 `<DataDir>/logs/app-YYYYMMDD.log`（0600、8MiB 轮转、跨天）+ MultiWriter + `debug.SetCrashOutput` + recover crash 文件；cmd 测试 14 |
| AR-03 版本/体积 | `-ldflags -X main.version/commit/buildTime` + `-trimpath -s -w`：49.5→33.8MB（实测 -31.8%）；`--version` 输出 |
| AR-02 CI | `.github/workflows/ci.yml`（gate 跑 ci.sh；shell race 独立 job 20m；coverage artifact）；`gates.sh` flaky 重跑改为 fail |
| FP-18 CLI | `zeokeel --version/--help/run`（0/1/2）已接线并测试 |
| 打包 | `package-linux`（tar.gz amd64/arm64）、`package-macos`（unsigned .app zip）、Windows 不变、SHA256SUMS；实测产物 |
| 文档真源 | 新建 `docs/BUGS.md`；STATUS 收敛 60 行（旧流水归档 `docs/history/`）；README/PLAN/FUNCTIONAL-PARITY/known-limitations/QUALITY/EVOLUTION 纠错（含 3 处"已实现说缺失"、JSONL SQLite 真源、迁移文案、mcp.workspace_config、900×840、RT-06/08 设置） |

## 5. wave 1 全量验证（Lead 执行）

```sh
export PATH=/usr/local/go/bin:$PATH GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod GOSUMDB=off CGO_ENABLED=0
gofmt -l ./cmd ./internal      # 空
go build ./...                 # OK
go vet ./...                   # OK
go test -count=1 ./...         # OK：21 包（含新增 cmd/importer）；shell 61s/session 30s/prov 16s
sh scripts/check-nocgo.sh      # OK：4 目标交叉编译 + 无 cgo
GOOS=windows go build/vet ./... # OK
# 独立验证：docs/audit/FIX-VERIFY-BACKEND.md（17 对抗探针；RT-01/02/03/07/11/12/13/14/15 CONFIRMED；RT-12 CLI e2e）
# 真跑：zeokeel --version / run --help / run 无模型 exit1；Xvfb 日志落盘
```

**wave 1 提交**：`1da4f12 fix: audit remediation wave 1 — P0/P1 security, reliability, UX, product, release`（119 文件，+3.5k/−0.95k）。

## 6. 下一轮（wave 2）待办

1. 产品面：PD-05 设置补页（通知/数据隐私/诊断/网络/更新/重置）、PD-06 Automation 历史/失败/绑定（后端+UI）、PD-07 MCP/Skills/AGENTS 管理 UI、PD-08 剩余、PD-10 会话/草稿恢复、PD-12 桌面生命周期（自启/托盘动作/关闭偏好）、About 版本显示、context.budget_tokens 设置行。
2. 后端功能：FP-19 代理设置、FP-04 goal 模型+`/goal`、FP-07 schedule 投回原会话、FP-16 ask_user 阻塞、FP-15 job 完成通知、FP-17 skills 多 root、FP-01 workflow parallel/pipeline、FP-03 subagent 异步、FP-05 workspace changes/diff、FP-14 附件路径语义、PD-04 导入器 CLI/UI 接线。
3. 架构 P2（低风险优先）：AR-21 重复代码、AR-08 UI 异步、AR-05 projector 收敛、AR-07 messages ordinal、AR-09 迁移框架、AR-17 fuzz；AR-04/AR-22 大重构与 RT-04 per-session Agent 继续 DEFERRED。
4. 预览金标重生成 + 视觉回归 + 最终独立验证（verifier-core/verifier-ui 复跑 P0/P1 修复）。
5. FIX-REPORT 最终版 + `docs/BUGS.md` 状态同步 + 提交 wave 2。

## 7. DEFERRED（明确延期，理由）

| 项 | 理由 |
| --- | --- |
| workflow 完整 JS 引擎 / PTC Node 运行时 | 需引入 JS/Node 运行时，与"纯 Go、CGO_ENABLED=0、单二进制"硬约束冲突；先做受限 JSON parallel/pipeline（wave 2）并在文档明确边界 |
| ACP/SDK 完整协议 | L 项，需协议服务端与客户端矩阵；headless CLI 已覆盖最小自动化形态 |
| 三平台签名/公证/自动更新 | 需证书与发布账号；安装包/版本注入已就绪，更新通道需发布基础设施 |
| RT-04 per-session Agent | 涉及 Eino 并发模型与 MCP/terminal manager 生命周期，风险高，需专项设计与压测（方案已记录，本轮不做） |
| internal/shell 整体分层（AR-04）、schema v2 大迁移（AR-09 完整版）、JSONL 真源重写 | L 项重构，分批进行；本轮已做低风险部分（批量持久化、窗口 clamp、文档纠错） |
| 真机 IME/屏幕阅读器/高 DPI/通知矩阵 | 无真机/无桌面会话环境，只能 headless 验证；列入发布前真机清单 |
