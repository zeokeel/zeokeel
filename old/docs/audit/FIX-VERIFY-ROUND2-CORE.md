# 独立复核报告（verifier-core）：Round 2 核心侧验证

> 复核者：verifier-core · 日期：2026-11-06 · 基线：**`8e2ba5b`（feat: round 2 TDD GREEN …）**；复核时 `HEAD=8e2ba5b`，但工作树有其他 worker 的未提交改动（`M internal/acp/server.go`、`M internal/acp/server_green_test.go`）——本报告只验证**已提交内容**（`git archive HEAD` 解包到 `/tmp/verify-round2`），未改仓库、未提交。
> 方法：干净副本内 `go build/vet/test ./...` + **6 个自写独立探针**（RT-04 压力/审批/gate 路由/泄漏、ACP 协议对抗、updater 安全/崩溃恢复、presets fail-closed、skills cwd 锚定、keyless scheme 负例）+ ACP `-race` + P3 定向测试 + 代码阅读。不复用修复作者的断言。
> 证据：`.cache/audit/verify-round2/`（`build-test.log`、`all-independent-probes.log`、`race-p3.log`、探针源码 `zz_verify_round2_*`）。环境：Go 1.27.3、`CGO_ENABLED=0`（race 用 1）。

## 0. 结论摘要

| 领域 | 判定 | 关键证据 |
| --- | --- | --- |
| 1) RT-04 per-session Agent（并发/审批/门禁/泄漏） | **CONFIRMED**（附带 1 个 P3 seam 提示） | 3 会话真并行（265ms vs 250ms 模型延迟，串行应 ~750ms）；审批隔离与路由正确（A approve 完成时 B 仍停；B 仅 deny 后完成）；ApproveAlways 在该会话 gate 生效；Close 丢弃 5 个 agent、幂等、goroutine 2→3。**P3**：`pendingToolName` 要求共享 `c.gate != nil`，纯 per-session（无共享 Gate）配置下 `ApproveAlways` 返回 false（生产 app 总有共享 bundle Gate，无用户影响） |
| 2) ACP 协议/并发/`-race` | **CONFIRMED**（附带 1 个 P3 顺序性提示） | 坏 JSON → -32700 且连接存活；未知方法 -32601；首帧 prompt → -32600；update 全在 end_turn 响应之前且有序；cancel → `stopReason:"cancelled"` 且 ctx 真取消；EOF 取消运行中 prompt 后 Serve 返回（无泄漏）；>8MiB 帧被拒；`-race` PASS。**P3**：prompt 帧与 initialize 同连接流水线时，prompt goroutine 可能在 initialize 处理后才检查 init → 观察到"先 prompt 后 initialize"被执行（客户端本应先 init，影响低；建议 read loop 同步检查 init） |
| 3) updater（安全/崩溃恢复） | **CONFIRMED** | 坏 manifest fail-closed；`http://` feed 在未显式允许时 `ErrInsecureURL`；sha256 不匹配 `ErrChecksumMismatch` 且不落盘；staged 路径越界拒绝；损坏 marker 自愈（删除、不阻断启动）；`.installing` 兄弟文件崩溃续装（Replacer 收到 .installing 路径）；replace 失败保留下载与 marker |
| 4) presets（fail-closed/merge/fallback） | **CONFIRMED**（1 个 P3 文档级提示） | 坏 JSON→ErrInvalidJSON；重复 user ID→ErrDuplicateID（整包失败）；unknown tool→ErrUnknownTool；builtin 字段级合并（name 覆盖、tools 保留）；`tools:[]` 清空 allowlist；Resolve(unknown)→fallback+warning，空串→默认无 warning。**P3**：覆盖 builtin 时校验发生在合并前，`name` 仍必须显式出现（与"缺席字段保留 builtin 值"注释不符，文档/实现小不一致） |
| 5) P3 其余（skills/manifest/TOCTOU/keyless） | **CONFIRMED** | skills：相对 root 锚定 workspace（进程 cwd 变化不影响，绝对路径不变）；keyless：仅 http(s) 本地/私网免 key，ftp/ws/file/公网拒绝；spill TOCTOU：`TestSpillRefusesDirSwapAfterValidation` PASS（Linux `openat(O_NOFOLLOW)` 逐段 + 写后目录身份复核，目录 swap 后拒绝且 outside 无文件）；manifest 迁移：`TestLegacyManifestMigratesWithoutDuplicateImport`、`TestLegacyManifestUnmigratableFailsClosed`、`TestManifestKeyRealLayoutUniqueness`、`TestImportManifestIdempotentRealDSHLayout` 均 PASS（旧 key 迁移不重复导入、不可迁移 fail-closed） |
| 构建/门禁 | **CONFIRMED** | `go build ./...` OK、`go vet ./...` OK、`go test -count=1 ./...` **26 包全绿**（exit 0；含 ACP/presets/updater/globalhotkey/workspacechanges） |

**未发现 P0/P1 缺陷**；3 个 P3 级观察（均为协议/文档加固项，不影响产品正确性）见 §4。

## 1. RT-04 详情（探针 `internal/session/zz_verify_round2_rt04_test.go`）

自写 fake provider（每回合 250ms）+ 每会话独立 `runtime.Agent`/`ApprovalQueue` 工厂，模拟 app 安装方式：

- **并行**：3 会话同时 Submit，全部完成耗时 **265.7ms**（模型单回合 250ms；共享 runMu 串行应 ≈750ms）→ 真并行。
- **审批隔离与路由**：A、B 两会话同时命中需审批工具；**Approve(A)** 后 A 完成而 B 仍 `bFinished=false`；**Deny(B)** 后 B 才完成 → 决策路由到各自 session 的 queue，互不影响。
- **ApproveAlways**：对 C 的待批调用 ApproveAlways 后，C 的下一次同工具回合**不再弹审批**（规则只落在 C 的 per-session gate）。
- **资源泄漏**：5 会话构建 5 个 agent；`Close()` 后 discard 回调恰好 5 次、二次 Close 幂等、goroutine 2→3（无泄漏）。

## 2. ACP 详情（探针 `internal/acp/zz_verify_round2_acp_test.go`）

- 坏 JSON 帧 → `undefined id`+`-32700`，**连接不中断**（后续请求正常应答）；
- 未知方法 → `-32601`；prompt 作为首帧（无 initialize）→ `-32600`（确定性）；
- initialize/session-new 正常，字符串 id 原样回显；
- prompt：两条 `session/update` 严格在 `end_turn` 响应**之前**按序写出；
- cancel 竞态：prompt 运行中发 `session/cancel` → 响应 `stopReason:"cancelled"` 且 agent ctx 确实被取消；EOF：运行中 prompt 被取消后 Serve 正常返回（无 goroutine 泄漏）；
- 8MiB+1 帧 → `Serve` 返回 `bufio.Scanner: token too long`（帧上限生效）；
- `go test -race ./internal/acp`：PASS（含作者 9 测试 + 本探针）。

## 3. updater / presets / P3 详情（探针见证据目录）

- **updater**：见 §0 行；另验证 marker 指向不存在 staged 文件且**无** `.installing` 时返回 `false,nil` 并清 marker（不阻断启动）。
- **presets**：见 §0 行；additional：`Resolve("")` 返回默认 preset 且 warning 为空。
- **skills**：`os.Chdir(other)` 后 `skills.RootsFor("skills", ws)` 首位仍为 `ws/skills`；绝对 root 行为不变。
- **spill TOCTOU**：`safeSpillDir` 后通过 `spillDirOpenedHook` 交换目录为指向外部的 symlink → `spillToolResultFile` 返回 error 且外部目录为空；Linux `openat` 链 + 写后 `Fstat/Lstat` dev+ino 复核实现已读码确认。
- **manifest 迁移**：`TestLoadLegacyManifestMigratesKeys`/`TestLegacyManifestDeduplicatesAfterMigration`/幂等用例通过；`migrateLegacyKeys` 把旧 key 规范化为新 key 且去重。
- **keyless**：`keylessLocalProvider` 仅接受 http(s)+本地/私网；`ftp://127.0.0.1`、`ws://localhost`、`file:///`、公网 https、非法 URL 全部 false（→ 无 key 时报"未配置模型"而不是给不可用 scheme）。

## 4. P3 级观察（建议但非阻塞；已报 lead）

1. **ACP initialize 顺序竞态（协议加固）**：prompt 帧与 initialize 流水线到达时，prompt 在 goroutine 中并发执行，`c.init` 检查可能晚于 read loop 内联处理的 initialize → 实到"prompt 先、initialize 后"也被执行。影响低（本地客户端/本仓 `zeokeel acp` 先发 initialize；无鉴权面），但建议在 readLoop 读到 `session/prompt` 帧时同步检查 init 状态（或在 decode 时快照 init），而不是在 goroutine 内检查。
2. **presets builtin 覆盖需显式 name**：`Load` 先 `validatePreset` 后 `mergePreset`，所以覆盖 builtin 的文件即使只想改 `tools` 也必须带 `name`；注释声称"缺席字段保留 builtin 值"。建议把必填校验移到合并后，或在文档/注释写明 ID/Name 必须出现。
3. **RT-04 `ApproveAlways` 依赖共享 gate**：`pendingToolName` 开头 `if c.closed || c.gate == nil { return "", false }`，纯 per-session（Config.Gate=nil、只给 AgentGateFactory）配置下 ApproveAlways=false（Approve/Deny 正常）。生产 app 始终有共享 bundle Gate 故无用户影响；建议去掉该 nil 检查改用 `gateForSessionLocked`，让 seam 自洽。**（2026-11-06 01:29 更新：fix-security 已在未提交 worktree 修复此点；verifier-core 用「HEAD + 仅该 hunk」干净快照独立复验 CONFIRMED——纯 per-session 下 ApproveAlways 返回 true 且抑制下一次 prompt，session 全量 36s 绿。提交时须包含 `internal/session/controller.go` 与未跟踪测试 `question_gate_test.go`/`preset_api_test.go`/`preset_filter_test.go`。）**

## 5. 未验证 / 残余

1. 工作树有未提交改动（`internal/acp/server.go`、`server_green_test.go`），未验证；本报告仅针对 `8e2ba5b` 提交内容。
2. ACP `-race` 只跑了 `./internal/acp`；未跑 `-race` 全仓（shell 慢）与 `internal/globalhotkey` 的 Windows 后端（无 Windows 环境）。
3. updater 未真实验证 HTTPS 证书链/真实 GitHub release 下载；`Replacer` 用 fake seam（真实平台替换属外部环境）。
4. RT-04 探针为 synthetic provider，未接真实模型；长时压力（10+ 会话/数百回合）未做 soak。
5. ACP 与 `internal/app` 的 headless adapter 端到端（`zeokeel acp` 真进程）未在本报告内跑（作者有 cmd smoke；Lead 可决定是否加一轮 CLI e2e）。
6. FP-16 仍为 `tdd_red`（`go test -tags tdd_red ./internal/runtime` 2 红），属下一轮契约，不计入本轮缺陷。

## 6. 复核产物

- `.cache/audit/verify-round2/build-test.log`：干净 HEAD build/vet/26 包测试全绿。
- `.cache/audit/verify-round2/all-independent-probes.log`：6 个独立探针全 PASS（含 `VC2-*` CONFIRMED 行）。
- `.cache/audit/verify-round2/race-p3.log`：ACP `-race`（`ok internal/acp 1.261s`，RACE_EXIT=0）与 P3 定向测试结果（spill TOCTOU、manifest 迁移/幂等、skills RootsFor×5、keyless 全 PASS）。
- 探针源码：`.cache/audit/verify-round2/zz_verify_round2_{rt04,acp,updater,presets,skills,keyless}*.go`（从 `/tmp/verify-round2` 复制，可复跑）。
- 冻结副本：`/tmp/verify-round2`（`git archive 8e2ba5b`）。
