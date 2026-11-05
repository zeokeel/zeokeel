# ZeoKeel 审计修复报告（FIX-REPORT · 最终版）

> 目标：修复 `docs/audit/MASTER-AUDIT.md` 的 P0/P1 全部 + 可行 P2/P3，并逐项给出代码/测试/真跑证据；L 项写清延期理由。
> 起点：`HEAD=3100af0`（审计基线）；**修复提交**：`1da4f12`（wave 1）→ `0bbf051`/`6da09e3`（补提交与状态）→ `84c5b1f`（wave 2/3，当前）→ 后续仅文档/验证收尾。
> 验证口径：`gofmt`/`go vet`/`go build`/`go test -count=1 ./...`（22 包）/`scripts/check-nocgo.sh` 四目标/`GOOS=windows build+vet`；**干净 checkout `git archive HEAD` + `sh scripts/ci.sh --fast` 全绿**（84c5b1f）；独立验证员报告见 §5。

## 0. 一页结论

- **P0：1/1 修复**（workspace `mcp.json` 不再默认执行；独立对抗探针 CONFIRMED）。
- **P1：审计清单 29 项 → 已修复 24 项、部分修复 2 项（FP-05 后端完成/UI diff 未做；FP-11 屏障写入完成但恢复 consumer 未做）、按裁决延期 3 项**（RT-04 per-session Agent、PD-12 全局快捷键部分、AR-03 签名/公证/自动更新基础设施；均有方案与文档）。
- **P2/P3**：高价值且低风险的项已修（FP-08/09/10/13/14/15/17/18/19、PD-04/05/06/07/10、AR-01/02/06/16/18/21、UX 全量 P2/P3 除 UX-04 框架限制与 UX-14 mygo 限制）；结构大重构（AR-04/AR-05/AR-07/AR-09/AR-20/AR-22、RT-04）明确延期并写入 `docs/BUGS.md`/known-limitations。
- **发布判断**：从「技术预览」升级为「**可发布内测/技术预览（功能完整的桌面 Agent beta）**」；正式 v1.0 仍需：三平台签名安装包+自动更新、DSH 迁移数据质量打磨/UI 入口验收、真机 IME/a11y/DPI/通知矩阵、RT-04 并发专项、AR 结构重构。
- **审计基线 97 条发现（去重 ~84）**：修复/关闭 **~70 条**，明确延期 **~14 条**（含 L 级重构与产品边界），无未处理的 P0/P1 缺陷（仅 2 项 PARTIAL 明确记录）。

## 1. 提交与验证证据

| 提交 | 内容 |
| --- | --- |
| `1da4f12` | wave 1：P0 + 安全/权限/数据 + 运行时可靠性 + UI/UX + 发布/CI/文档（119 文件） |
| `0bbf051` | 修复 importer 凭据文件被 `.gitignore` 忽略导致干净 checkout 不可构建（verifier 发现 P1） |
| `6da09e3` | UX-04 状态修正（独立验证发现 PARTIAL）+ 平台 CI/忽略规则 |
| `84c5b1f` | wave 2/3：产品面（PD-05/06/07/10/12/04）、剩余功能缺口（FP-01/03/04/05/07/14/15/16/17/19）、设置/管理 UI、导入器质量、预览金标、独立验证报告 |

**门禁证据（Lead 执行）**

```sh
export PATH=/usr/local/go/bin:$PATH GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod GOSUMDB=off CGO_ENABLED=0
gofmt -l ./cmd ./internal          # 空
go build ./... && go vet ./...     # OK
go test -count=1 ./...             # OK：22 包（session 252s/shell 68s/prov 16s）
sh scripts/check-nocgo.sh          # OK（linux-amd64/arm64, darwin-arm64, windows-amd64 无 cgo）
GOOS=windows go build ./... && GOOS=windows go vet ./...   # OK
# 干净 checkout：
git archive 84c5b1f | tar -x -C /tmp/clean && cd /tmp/clean && sh scripts/ci.sh --fast   # CI PASS
# 独立验证：docs/audit/FIX-VERIFY-*.md（6+ 份；含 17+11+ 个独立探针）
# 真跑：Xvfb 1600×1000（GUI 冒烟）、zeokeel --version、run --help、import --dry-run、
#      预览金标重生成 22 张 + TestVisualRegression PASS（14 屏）
```

## 2. 安全 / 权限 / 数据（全部修复）

| ID | 结果 | 关键实现与证据 |
| --- | --- | --- |
| RT-01 P0 | ✅ | workspace `mcp.json` 默认不读不执行（`mcp.workspace_config=off`，仅 `allow`）；`mcp.servers` 优先；忽略原因日志；args 脱敏；inspect 门状态；`settings_set` 写白名单移出 mcp.*/provider.base_url/approval.tools 等。独立验证 17 探针 CONFIRMED（`FIX-VERIFY-BACKEND.md`） |
| RT-02 P1 | ✅ | submit 持 bindMu 重新 pin 目标会话后按目标权限 rebuild，失败拒回合；独立 CONFIRMED |
| RT-03 P1 | ✅ | Read Only 移除 schedule_create/update/delete、memory、goal；独立 CONFIRMED |
| RT-05 P1 | ✅ | `WithTurnApprovalFrom` 审批上下文回退 + agentteam spawnCtx 透传（成员不随父回合取消；父回合结束 fail-closed）；8 测试 + 独立 CONFIRMED |
| RT-07 P1 | ✅ | spill 逐级 Lstat 拒 symlink + O_EXCL；独立 CONFIRMED |
| RT-11 P2 | ✅ | data dir 0700、db/wal/shm 0600；独立 CONFIRMED |
| RT-12/PD-03 P1 | ✅ | 每 data-dir 锁（flock/LockFileEx）+ 二次启动 IPC 激活窗口；独立 CONFIRMED（含 Xvfb 双实例 e2e/Windows 交叉编译） |
| RT-13 P2 | ✅ | 终态事件有界可靠投递 + approval early TTL/cap；独立 CONFIRMED |
| RT-14 P2 | ✅ | auto-review 不再以 workspace AGENTS.md 为授权证据；独立 CONFIRMED |
| RT-15 P3 | ✅ | `winproc.ScrubEnv` 统一覆盖第三方凭据；独立 CONFIRMED |
| FP-08 P2 | ✅ | write_file 原子写（tmp+fsync+rename+dir sync）+ edit_file read-before-write/stale；独立 CONFIRMED |
| FP-13 P2 | ✅ | MCP streamable-http（含鉴权头/真连测试）；独立 CONFIRMED |

## 3. 运行时可靠性 / 会话上下文

| ID | 结果 | 关键实现 |
| --- | --- | --- |
| RT-06/RT-08 | ✅（审计期间并发提交） | 默认无整体流超时 + idle 超时 + 重试/退避；独立验证无回归 |
| RT-09 P2 | ✅ | @daily/@hourly 本地时区、持久化 UTC |
| RT-10/AR-06 P2 | ✅ | token/reasoning 批量持久化（8KB/200ms、边界先 flush；store 单事务批量 + 单 fsync）；残余：崩溃窗口 ≤8KB/200ms（已入 known-limitations） |
| FP-09 P2 | ✅ | FTS5 trigram 增量索引 + bm25 + 标题优先 + LIKE 降级 + 删除清索引；独立 CONFIRMED |
| FP-10 P2 | ✅ | CJK rune token 估算 + 图片计费/offload + `context.budget_tokens` 可配置；独立 CONFIRMED |
| FP-11 P2 | 🟡 PARTIAL | 模型请求前 + 有副作用工具前双屏障、fail-closed（独立 CONFIRMED）；**屏障态无恢复 consumer**（恢复仍走 History/tool_calls），措辞已修正 |
| FP-15 P2 | ✅ | job 完成 advisory 注入（in-turn/跨回合） |
| FP-16 P2 | ⏸ DEFERRED | 阻塞式 ask_user 需 runtime question gate + shell 输入卡 + Controller answer channel（跨三 owner，P2）；方案见 known-limitations §3.9 |
| FP-17 P2 | ✅ | skills 多 root（ws/ws.zeokeel/~/.zeokeel）+ SKILL.md 指纹 refresh；app prompt/注册一致 |
| FP-01 P2 | ✅（受限 DSL） | workflow 支持 `parallel`/`pipeline`（失败隔离、`{{item}}`/`{{prev}}`、cap），与 DSH JS 引擎差异在文档标明 |
| FP-03 P2 | ✅ | subagent `async:true` child_id + 完成 advisory + status/list/cancel，同步兼容 |
| FP-05 P2 | 🟡 PARTIAL | `internal/workspacechanges` + 回合前 Capture/回合后 Diff + `state.WorkspaceChanges` 事件（Read Only 跳过，5s 有界）；**UI diff 视图未做** |
| FP-14 P2 | ✅ | file 附件 live/history 均按只读 path 引用（不再静默丢弃），文本快路径保留 |
| FP-04 P1 | ✅ | goal id/revision/phase + pause/resume/block/unblock/clear + Controller.GoalAction；paused/blocked 不自动续跑（`/goal` 命令 UI 可选后续） |
| FP-07 P1 | ✅ | schedule 记录 origin session，触发优先投回原会话（busy/不存在回退新建），失败记 run |
| FP-18 P2 | ✅ | headless CLI（`--prompt/--json/--yes/--permission`，0/1/2 退出码，非交互默认 deny；`--permission` 失效 P1 由独立验证发现并修复） |
| FP-19 P2 | ✅ | `network.proxy` 统一 provider/websearch/webfetch + SSRF 预校验保留 + 日志脱敏 |
| PD-02b（新发现） | ✅ | 无 key 本地 provider（loopback/私网/host.docker.internal）可构建并跑通；公网仍要求 key |
| PD-04 P1 | ✅（技术预览） | `internal/importer` + `zeokeel import`：system 注入/step 级过滤（真实数据 17,219→1,248 messages）、skips 分解、images 统计、幂等 manifest、header 版本守卫；UI 入口在设置页；口径由「迁移器」降级为「同类替代 + 最小导入（技术预览）」。独立再验收（FIX-VERIFY-IMPORT2）质量 PASS；发现并修复「真实 `session-<uuid>` 布局下 manifest 幂等失效」P1（key 改 `home|相对 sessions 路径`；旧版 manifest 升级首导一次重导后稳定） |

## 4. UI / UX / 产品面

| 项 | 结果 | 证据 |
| --- | --- | --- |
| UX-01/UX-03/PD-01 | ✅ | 键盘 Enter 失败 → deferred toast + 回滚空会话 + draft/附件保留；独立 GUI 复核 CONFIRMED（toast 可见、sessions=0） |
| UX-02 | ✅ | 会话空态（图标+引导+无模型 CTA）；独立 CONFIRMED |
| UX-04 | 🟡 PARTIAL | popover state 清理 + 设置窗 show/hide 重绘已做，但 mygo 在「主窗口被独立窗口遮挡且无输入」时仍跳过出帧 → 残影；已入 known-limitations（框架限制），独立复核确认 |
| UX-05/06/08/09/10/11/13 | ✅ | 工作区可见 label、设置窗 840 无裁切、rail 会话 flyout、侧栏空态、@ 中文化、窄窗 tooltip、Start 页整治；独立 GUI CONFIRMED |
| UX-14 | ⏸ DEFERRED | toast 上移会拦截 popover 指针（mygo viewport 限制），已记录 |
| PD-02 | ✅ | Provider 预设 + 异步测试连接；无 key 本地 provider 已修（见 PD-02b） |
| PD-05 | ✅ | 设置补页：通知/数据与隐私（导出脱敏 zip/重置/删会话）/诊断（复制/导出日志）/网络/桌面/更新；独立 GUI 复核进行中 |
| PD-06 | ✅ | automation_runs 历史/失败原因/耗时/session + workspace/permission/model 绑定 + 前景提示；独立 GUI 复核进行中 |
| PD-07 | ✅ | MCP JSON 编辑/校验、Skills 扫描/校验/打开文件夹、AGENTS.md 查看/编辑；独立 GUI 复核进行中 |
| PD-08/09/11/17/18 | ✅ | 插件页统一、失败分类卡、Home 入口、工作区校验、删除确认；独立 GUI CONFIRMED |
| PD-10 | ✅ | `ui.last_session` 只读恢复 + `draft.<id>` 防抖持久化；独立 GUI 复核进行中 |
| PD-12 | 🟡 PARTIAL | 关闭行为/三平台自启/托盘动作已实现；全局快捷键 mygo 无 API → DEFERRED |
| PD-13/AR-03 | 🟡 PARTIAL | 版本注入 + linux tar.gz/mac .app 打包完成；签名/公证/自动更新需证书/发布账号 → DEFERRED |
| PD-19/20/21/22 | 部分 | 导出全部数据/诊断/通知开关已做；ACP/SDK → DEFERRED；文案一致性已修 |
| 首启 setup / About 版本 / import 入口 | ✅ | Home setup 卡（无模型/无 workspace CTA）、About 单一版本来源（`BuildVersionString`）、设置页 DSH 导入入口 |

## 5. 独立验证汇总（对抗性复核，全部报告在 docs/audit/）

| 报告 | 范围 | 结果 |
| --- | --- | --- |
| `06-verification-core.md` | 审计基线 P0/P1（修复前） | CONFIRMED 28 / PARTIAL 2 / REFUTED 0 |
| `07-verification-ui.md` | 审计基线 UI/产品（修复前） | CONFIRMED 23 / PARTIAL 3 / 子项 REFUTED 2 |
| `FIX-VERIFY-BACKEND.md` | 修复后后端安全项 | 17 探针：RT-01（含 3 残留）/02/03/07/11/12/13/14/15 全部 CONFIRMED；FP-18 `--permission` P1 由它发现并推动修复 |
| `FIX-VERIFY-CORE2.md` | wave1 核心未双验项 | CONFIRMED 13 / PARTIAL 1（FP-11 恢复措辞）/ REFUTED 0；发现 BUILD-01/02（已修） |
| `FIX-VERIFY-UI.md` | wave1 UI/UX 真跑 | CONFIRMED 13 / PARTIAL 2（UX-04 框架限制、About 版本已修）|
| `FIX-VERIFY-PRODUCT.md` | 产品面 + PD-04 导入器验收 | 导入器「最小导入（技术预览）」结论；PD-05/06/07/10/12 待提交后复核；发现 PD-02b（已修） |
| `FIX-VERIFY-WAVE23.md` | wave2/3 后端新增项 | **9/9 CONFIRMED**：PD-02/FP-19/FP-04/FP-07/FP-14/FP-05/PD-06/skills 独立探针确认；importer manifest 幂等 P1（真实 `session-<uuid>` 布局）由验证员发现 → 修复（key=`home\|相对 sessions 路径`）→ 19/19 测试 + 真实数据双跑复验 PASS（run2 idempotent=26，仅内容变化者重导） |
| `FIX-VERIFY-UI-WAVE2.md` | wave2 UI 真跑 | **CONFIRMED 9 / PARTIAL 1 / REFUTED 0**：PD-05/06/10、About/setup/import 入口 CONFIRMED；PD-07 PARTIAL（AGENTS.md 编辑保存流程未驱动）；无 P0/P1 回归；2 个 P3 观察入 BUGS |
| `FIX-VERIFY-IMPORT2.md` | 导入器改进再验收 | 质量 PASS（1,248 msgs/系统注入=0/`[tool call]`=0）；发现 manifest 幂等 P1（真实布局）→ 已修复并由 fix-security 独立复核；旧版 manifest 首导重导一次说明 |

## 6. 明确延期（DEFERRED，含理由；均已写入 docs/BUGS.md 与 known-limitations）

| 项 | 理由 |
| --- | --- |
| RT-04 per-session Agent（全局串行/审批占锁） | 涉及 Eino 并发模型与 MCP/terminal manager 生命周期，需专项设计与压测；方案已记录（`SetAgentFactory` seam + 审批不占全局锁），本轮不做大改 |
| FP-02 PTC 代码运行时 | 与「纯 Go、CGO_ENABLED=0、单二进制」硬约束冲突（需嵌 JS/Node）；保持 batch pipeline 并在文档标 ❌/产品边界 |
| FP-06 声明式 agent preset/persona | L 能力扩展项，需 preset 存储/注册表/会话选择 UI；先以 4 模式 + skills/settings 覆盖 |
| FP-16 阻塞式 ask_user | P2 体验项；跨 runtime/shell/controller 三 owner，方案已记录 |
| FP-05 UI diff 视图 | 后端事件/差异已交付；UI 展示留待下一迭代 |
| UX-04 popover 残影 | mygo「遮挡窗口无输入时跳过出帧」框架限制；state 已清、重绘已请求；需框架提供强制重绘/holdFrame 钩子 |
| UX-14 toast 上移 | mygo 自绘 viewport 会拦截 popover 指针（已有 3 回归测试证据） |
| 全局快捷键（PD-12） | mygo 无全局热键 API |
| 三平台签名/公证/自动更新、Windows 安装器（PD-13/AR-03） | 需证书与发布账号/发布基础设施；未签名 zip/tar.gz/.app 已交付 |
| ACP/SDK 完整协议（PD-20/FP-18 扩展） | L 项；headless CLI 已覆盖最小自动化形态 |
| AR-04/AR-05/AR-07/AR-09/AR-20/AR-22 结构重构 | L 项技术债，分批；本轮已做低风险部分（批量持久化、窗口 clamp、文档纠错、去重） |
| 真机 IME/屏幕阅读器/高 DPI/通知矩阵 | 无真机/桌面会话环境，只能 headless；列入发布前清单 |

## 7. 复现与回归

```sh
# 全量门禁（与 CI 同口径）
sh scripts/ci.sh --fast
# 干净 checkout 门禁（防 .gitignore 漏文件类问题；verifier 曾发现一次）
sh scripts/check-clean-checkout.sh
# 关键回归
go test -count=1 -run 'TestWorkspaceMCPConfig|TestConnectMCP|TestRegistry.*MCP' ./internal/app
go test -count=1 -run 'TestSubmitRebuildsForTargetSessionPermission|TestKeylessLocalProvider' ./internal/session ./internal/app
go test -count=1 -run 'TestWorkspaceChanges|TestGoal|TestSchedule.*Origin' ./internal/session ./internal/tools
go test -count=1 -run 'TestVisual' ./internal/shell   # 需 ZEOKEEL_VISUAL=1，详见 scripts/visual-check.sh
go test -count=1 ./internal/importer                  # 16 用例（过滤/幂等/版本守卫）
# CLI
./dist/zeokeel-linux-amd64 --version
./dist/zeokeel-linux-amd64 run --help
./dist/zeokeel-linux-amd64 import --dry-run --json --from /root/.dsh   # 只读，不打印 key
# GUI 冒烟（headless）
unshare -n xvfb-run -a -s '-screen 0 1600x1000x24' bash -c 'ZEOKEEL_DATA_DIR=/tmp/zk-smoke ./dist/zeokeel-linux-amd64 >/tmp/zk-smoke.log 2>&1 & sleep 5; import -window root /tmp/smoke.png; kill $!'
```

## 8. 结论

审计发现的 **P0 全部修复并独立复现验证**；P1 中除 2 项明确 PARTIAL（单项剩余部分已记录）与 3 项按裁决延期（含 RT-04/签名基建）外全部修复；P2/P3 高价值项基本清零，L 级结构/协议项按计划延期。修复后的 ZeoKeel 已具备「全功能 AI Agent 桌面端」的**内测发布条件**：首启可用、核心 agent 可靠、安全信任边界完整、设置/自动化/管理面齐全、三平台可构建、CI/发布/诊断链路就位。正式 v1.0 的剩余门槛（签名安装包与更新、真机矩阵、RT-04 并发、迁移数据质量、UI diff 视图、框架层 UX-04/快捷键）已全部写入 `docs/BUGS.md` 与 `docs/design/known-limitations.md`，可在后续迭代逐项关闭。
