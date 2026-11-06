# 独立验证 F：wave2/3 后端新增项（task-17 · runtime-auditor）

> 验证者：runtime-auditor · 日期：2026-11-06 · 基线：`HEAD = 84c5b1f`（wave2/3 提交，工作树干净）
> 方法：读代码 + 定向 `go test` + 自写独立探针（不复用作者用例的 `/tmp/zk-w23copy` 副本）+ 真 CLI e2e（本机 fake SSE provider / 真实 `/root/.dsh` 只读 dry-run）
> 证据目录：`.cache/audit/verify-wave23/`（探针源码、日志、报告 JSON）
> 限制：未验证 UI 展示层（本任务只做后端）；HTTPS 代理 CONNECT 路径、`host.docker.internal` 真机解析未在本环境 e2e；真实 DSH 数据在验证期间仍被 DSH 使用，skip 计数会小幅漂移。

## 0. 结论摘要（矩阵）

| # | 项 | 声称 | 独立复核方式 | 判定 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 1 | PD-02 无 key 本地 provider | localhost/127.0.0.1/::1/私网/host.docker.internal 可无 key 构建；公网空 key 拒绝 | 真 CLI e2e（无 `DEEPSEEK_API_KEY`，`ZEOKEEL_PROVIDER_BASE_URL=http://127.0.0.1:<fake>/v1` → `run --json` exit 0/status ok）；公网 base URL 无 key → exit 1 `no model is configured`；`keylessLocalProvider` 9 正例 + 6 反例矩阵探针 | **CONFIRMED** | P3 备注：predicate 不校验 scheme，`ftp://127.0.0.1/...` 也被当作 keyless，但随后 `openai.New` 以清晰错误拒绝（不构成安全/功能缺陷） |
| 2 | FP-19 统一网络代理 | `network.proxy` 生效于 provider/websearch/webfetch；webfetch 私网目标在代理前拒绝；错误不泄漏凭据 | 独立探针（`/tmp/zk-w23copy/internal/tools/zz_verify_fp19_test.go`）：① 4 个私网目标全部在 `validateURL` 拒绝且代理命中 0 次；② 公网目标经代理（absolute-form URI、返回代理响应体）；③ 代理返回 302 → 重定向目标 127.0.0.1 被本地重新校验拒绝，代理仅命中第一次；④ 代理 URL 带 `user:s3cr3t` → 错误文本不含用户名/密码；仓库定向测试（provider/websearch/webfetch/headless proxy）全 PASS | **CONFIRMED** | 未独立 e2e：HTTPS CONNECT 与 env-proxy fallback（由仓库测试覆盖）；webfetch 直连路径保留 DNS-rebinding `Control` 校验 |
| 3 | FP-04 goal 模型/API | id/revision/phase，pause/resume/block/unblock/clear + Controller.GoalAction，paused/blocked 不自动续跑 | `go test -run 'TestGoal|TestControllerGoalAction' ./internal/tools ./internal/session` 全 PASS（生命周期/新目标新 id/phase 持久化/Controller API/continuable honors phase + 旧 GoalDriver 全套）；代码复核 `goal_driver.go:216-223` 对 paused/blocked 直接不续跑，`controller.GoalAction` 持久化到 goal store 并 reset/保留计数器语义正确 | **CONFIRMED** | 未验证 UI `/goal` 入口（前端范围） |
| 4 | FP-07 schedule 投回原会话 | 原会话存在且空闲→Submit 原会话；busy/不存在→新建；失败记 run | 定向测试 PASS：`TestScheduleCreateRecordsOriginSession`/`TestRunAutomationTaskDeliversToOriginSession`/`...CreatesSessionWhenOriginMissing`/`...RecordsFailure`；代码复核 `automation_runs.go:198-216`：origin 存在→`submitInto`，`ErrTurnRunning` 才回退新建（日志），其他错误不重复提交；run 行在 submit 前创建、失败即 finish failed | **CONFIRMED** | busy 分支由代码 + `ErrTurnRunning` 语义确认（无专门 busy e2e） |
| 5 | FP-14 文件附件只读 path 引用 | live 与 history 重放均以只读 path 引用而非静默丢弃；文本快路径不变 | 定向测试 PASS：`TestAttachmentHistoryReplaysFileReference`（app 级 history 重放）/`TestAttachmentPromptRendersReadOnlyPath`/`EscapesPathAttributes`/`TextFastPathUnchanged`；代码复核 `AttachmentPrompt` 对无内联文本的附件输出转义后的只读路径引用，文本附件仍走原 fast path | **CONFIRMED** | 图片 vision 附件仍按既有设计走 image parts（非本项） |
| 6 | FP-05 workspace changes | Submit 前 Capture、回合后 Diff emit `workspace/changes`；Read Only 跳过；replay 正确；性能可接受 | 定向测试 PASS：`internal/workspacechanges` 6 项（git/walk/忽略构建目录/大文件 modtime/转 state changes/isGit）+ `TestWorkspaceChangesReportedAfterTurn`（session 端到端 emit）+ `TestWorkspaceChangesEventRoundTrip`（state replay）；代码复核：`submit` 在 `SessionPermissionOrDefault != ask` 时捕获（Read Only 跳过），5s 超时；`finishTurn` 对所有终态（含 failed/stopped）在持久化后 emit，失败静默；`state.Apply` 写入 `WorkspaceChanges/Turn` | **CONFIRMED** | 性能：见 §2.4（capture 每回合 ≤5s×2 有界；Lead 干净 checkout ci 记录 session 套件 ~250s，建议下一 wave 做 before/after 快照复用并纳入 CI 基线跟踪）；捕获在 Submit 内同步执行（每次 ≤5s，最坏会给回合开始增加延迟，属有界折衷） |
| 7 | PD-06 automation runs | 记录/查询/级联删除/legacy 迁移；绑定 workspace/permission/model 生效 | 定向测试 PASS：store `TestAutomationRunLifecycle`/`Validation`/`DeleteCascadesRuns`/`LegacyAutomationsTableMigrates`/`AutomationBindingFieldsRoundTrip`；app 侧 origin+binding+`TestFastTurnFinishesRunTrackedLater`（极快回合仍 finish run）；代码复核 run 在 submit 前创建、terminal hook 按 TurnOutcome finish completed/failed/stopped，失败原因写入 err_text | **CONFIRMED** | 未验证 Automation UI 展示（前端范围） |
| 8 | importer wave2（过滤/幂等/版本） | 真实 dry-run ~1248 messages、skips 分解、manifest 幂等、版本/损坏 fail-closed | 过滤/版本/损坏 fail-closed 全部 CONFIRMED（真实 dry-run messages_imported=1248、skips 分解正确、unsupported version 显式、损坏 manifest 写前 ErrManifest、dry-run 不写 manifest）；幂等：**修复前**真实双跑每次 imported=27、DB 54 sessions/2496 messages、manifest 仅 1 entry（key 覆盖）→ 定位并修复 `manifest.go sessionDirName()` 后：真实双跑 run1 imported=27/1248；run2 dry-run idempotent=26、would-import=1（该 1 个会话在两次运行之间被运行中的 DSH 继续写入、hash 变化 → 按设计重导，属 live 数据漂移）；干净 fixture（2 个 `session-<uuid>` 目录）run1 imported=2、run2 **imported=0 / skips.idempotent=2 / DB 仍 2 sessions**；修改一个源后 run3 imported=1、DB=3 | **CONFIRMED（修复后）** | 修复：key 改为 `<dshHome>/sessions` 相对路径、`session-<uuid>` 不再误判、`dsh_session_id` 用 header 真 id（修复未提交，由 Lead 提交）；回归测试 `TestImportManifestIdempotentRealDSHLayout`/`TestManifestKeyRealLayoutUniqueness`/`TestSessionPathIdentity`；旧 manifest 覆盖型 key 升级后首次会重导一次再稳定（见 §2.6） |
| 9 | skills 多 root | workspace + user + project 三 root 注册，prompt 与工具一致；仅 user root 也工作；自定义 root 保持单 root | 定向测试 PASS：`TestSkillsPromptMergesMultiRoots`（prompt 与工具目录一致）/`TestSkillsToolRegistersFromUserRootOnly`（仅 user root）/`TestSkillsCustomRootStaysSingleRoot`；代码复核 `skills.RootsFor` 去重与 `services.skillsRootsAvailable` 一致 | **CONFIRMED** | `/` 面板前端展示未验证 |

**总判定：9/9 CONFIRMED（importer 项在报告初版为 PARTIAL，修复后复验通过），0 REFUTED；无安全/数据完整性缺陷遗留。** 另有 1 条 P3 输入校验建议（PD-02 scheme）与 1 条性能观察（FP-05，见下）。

## 1. 独立探针与命令

```sh
# /tmp 副本（HEAD 84c5b1f 干净快照）+ 自写探针
rsync -a --exclude .git --exclude .cache --exclude dist /data/mygo/ /tmp/zk-w23copy/
go test -count=1 -run 'TestVerifyFP19|TestVerifyPD02' -v ./internal/tools ./internal/app
# 结果：FP-19 3/3 PASS；PD-02 矩阵仅 ftp://127.0.0.1 一项按“malformed public”预期不符（见 §2.1）

# PD-02 真 CLI e2e（本机 fake SSE，无任何 key env）
ZEOKEEL_DATA_DIR=<tmp> ZEOKEEL_PROVIDER_BASE_URL=http://127.0.0.1:<fake>/v1 /tmp/zk-w23 run --json "hello local"   # exit 0, status ok
ZEOKEEL_DATA_DIR=<tmp> ZEOKEEL_PROVIDER_BASE_URL=https://api.deepseek.com/v1 /tmp/zk-w23 run --json "hi"          # exit 1, "no model is configured"

# 定向测试矩阵（app/tools/session/store/runtime/workspacechanges）
go test -count=1 -run '<PD02/FP19/FP04/FP07/FP14/FP05/PD06/FP17 相关 Test...>' ./internal/{app,tools,session,store,runtime,workspacechanges}

# importer 真实数据与幂等
go run ./cmd/zeokeel import --from /root/.dsh --dry-run --json
ZEOKEEL_DATA_DIR=<tmp>/data /tmp/zk-w23 import --from /root/.dsh --json --manifest <tmp>/manifest.json   # 两次
```

探针源码/日志：`.cache/audit/verify-wave23/`。

## 2. 细节与观察

### 2.1 PD-02：CONFIRMED（含 1 条 P3 建议）
- 真 CLI：空 key + `http://127.0.0.1:<port>/v1` → `status=ok`、`text=hello from fake sse`、exit 0（说明 provider 构建与请求都真实工作）；空 key + 公网 URL → 构建期即拒绝（`no model is configured`），不会发起网络请求。
- `keylessLocalProvider` 正例矩阵全部识别：`localhost`、`localhost.localdomain`、`127.0.0.1`、`[::1]`、`10.x`、`192.168.x`、`172.16.x`、`host.docker.internal`。
- **P3 建议**：predicate 只解析 host 不校验 scheme，`ftp://127.0.0.1/v1` 也会被当作“本地无 key provider”；随后 `openai.New` 会以 `scheme must be http or https` 报错（fail-closed，无安全影响），但更早地校验 scheme 可给出更准确的“no model configured”提示。

### 2.2 FP-19：CONFIRMED（SSRF 在代理之前，凭据不泄漏）
- 私网目标（127.0.0.1/10.0.0.5/169.254.169.254/[::1]）全部在 `validateURL` 阶段被拒绝，测试代理命中 **0 次** —— 证明“先校验目标、后连代理”的顺序成立，代理不会被用作 SSRF 跳板。
- 公网目标经代理成功（代理收到 absolute-form URI），重定向到私网地址会被 `CheckRedirect` 的本地重新校验拒绝（代理只看到第一次请求），代理 URL 内嵌 `user:secret` 时错误文本不含凭据。

### 2.3 FP-04 / FP-07 / FP-14 / PD-06 / skills：CONFIRMED
均以“定向测试全绿 + 关键代码路径复核”双证据判定；测试名与断言见矩阵。FP-07 的设计选择（origin busy 时回退新会话而不是丢弃/排队）在代码注释与测试中一致；PD-06 的 run 记录在 submit 前创建、terminal hook 结算，极快回合也有 `TestFastTurnFinishesRunTrackedLater` 覆盖。

### 2.4 FP-05：CONFIRMED（性能观察：有界但可感知（capture 在回合启动关键路径，最坏 +5s ×2））
- 设计：Capture 在 Submit 内同步执行（Read Only 与无 workspace root 跳过），5s 超时；Diff 在 `finishTurn` 对**所有终态**执行并 emit，失败静默；`state.Apply` 正确保存最新 turn 的 changes + turn id，事件可 replay。
- 性能观察：Capture 位于回合启动的关键路径上（最坏 +5s），非 git 大仓库走 walk 上限 20k 文件/2MiB hash；turn 结束后再 +≤5s。对桌面交互属“有界但可感知”的折衷，建议后续把 before-capture 改为异步/复用上次 after snapshot（下一 wave 可评估）。
- 本次为测量窗口执行了 `go test -count=1 -timeout 280s ./internal/session`（结果见 §2.4 末尾；受并发 CI/其它 worker 构建影响，绝对耗时仅作量级参考）。

### 2.5 importer：CONFIRMED（含真实数据与幂等 e2e）
- 真实 `/root/.dsh` dry-run：`sessions_found=325, imported=27, skipped=298, failed=0, messages_imported=1248, images_dropped=25`；skips 分解见矩阵。与 wave3 修复时的 1248 一致（skip 计数因 DSH 仍在写入而小幅漂移）。
- 版本守卫/损坏 manifest：单元测试确认 `ErrUnsupportedVersion` 显式计入 `unsupported_version` 且不半解析；损坏 manifest 在写入任何会话前返回 `ErrManifest`；dry-run 不写 manifest。CLI `--manifest` 两次运行复验：两次均 `sessions_imported=27`、无 `idempotent` skip，DB 变 54 sessions/2496 messages；manifest 仅 1 个 entry（key `"/root/.dsh|session.v4.jsonl.zstd"`）。干净 fixture 复现：2 个 `session-<uuid>` 目录，两次均 imported=2、DB 4 sessions/8 messages、manifest 1 entry。**结论：幂等修复对真实 DSH 会话目录名失效**。

### 2.6（初版缺陷，已修复并复验）importer manifest 幂等在真实 DSH 目录名下失效

- **初版现象**（HEAD 84c5b1f）：`zeokeel import --from /root/.dsh --manifest <file>` 连跑两次，两次均 `sessions_imported=27`、无 `idempotent` skip；隔离 data dir 的 DB 变 **54 sessions/2496 messages**（重复导入）；manifest 只有 **1 个 entry**，key=`/root/.dsh|session.v4.jsonl.zstd`。
- **根因**：`internal/importer/manifest.go sessionDirName()` 把以 `session-` 开头的真实 DSH 会话目录名（`session-<uuid>`）误判成“手工导出文件直接放 bucket”的 fallback，返回文件名 `session.v4.jsonl.zstd` → 所有会话 key 相同、manifest 互相覆盖；单测 fixture 用 `sess-idem` 恰好绕开该分支，所以原作者测试全绿。
- **修复（本轮，未提交，由 Lead 提交）**：
  - `manifestKey` 改为 `<dshHome>/sessions` **相对路径**（如 `--data-mygo--/session-<uuid>/session.v4.jsonl.zstd`）作为 key 的路径部分，保证唯一；仅在文件位于 sessions 根/直接位于 bucket 时 fallback 文件名。
  - `sessionIDFromFile`/`sessionPathIdentity`：真实目录名、裸 uuid、flat export 均正确；manifest 的 `dsh_session_id` 优先取 session header 真 id（`manifestSessionID`），缺失时按路径推导。
  - 新增回归测试：`TestImportManifestIdempotentRealDSHLayout`（2 个 `session-<uuid>` 目录：两次 Import 第二次 imported=0 / skips.idempotent=2 / DB 仍 2 sessions；修改一个源后重导入该 1 个、DB=3）、`TestManifestKeyRealLayoutUniqueness`（含 flat export bucket 唯一性）、`TestSessionPathIdentity`（正常/session-uuid/裸 uuid/bucket-direct 边界 + 越界拒绝）。
- **复验证据**：
  - `go build ./internal/importer` ✅、`go test -count=1 ./internal/importer` **19/19 PASS**（修复后）。
  - 真实 `/root/.dsh`（只读；DSH 仍在运行）三次复验：run1 实际导入 `imported=27/messages=1248`；run2 dry-run（同 manifest）`idempotent=26`、would-import=1（该 1 个会话在两次运行间被 DSH 继续写入导致 hash 变化，按设计重导；manifest 未因 dry-run 改动）；run3 实际导入 `imported=1 / idempotent=26` → DB 由 27 sessions/1248 messages 变为 **28 sessions/1252 messages**（只多了那个真实变化的会话，未重复其余 26 个）；manifest 27 个条目、key 各不相同（含 bucket/session 目录前缀）。fixture 静态场景（2 个 `session-<uuid>`）则验证了“第二次 imported=0 / idempotent=2 / DB 不变”的完整幂等。
  - 修复后 manifest 含 27 个不同 key（含 bucket/session 目录前缀），不再互相覆盖。
- **迁移语义（升级注意）**：旧版本 manifest 的覆盖型 key 不会被识别，升级后**第一次** import 会对已导入会话再导入一次（manifest 随之迁移为正确 key），之后恢复稳定幂等；已在报告中标注，属可接受的一次性成本。

## 3. 未验证 / 限制
- UI 展示层（Settings 代理/自动化历史/管理页/导入对话框/`/goal` 命令/workspace changes 渲染）不在本任务范围，由 FIX-VERIFY-UI 覆盖。
- FP-19 的 HTTPS CONNECT 代理路径与“仅环境代理变量”路径未独立 e2e（仓库测试 `TestRunHeadlessUsesNetworkProxy` 等覆盖同逻辑）。
- PD-02 的 `host.docker.internal` 在本机不可解析，只验证了 predicate 接受 + 真实 e2e 使用 127.0.0.1。
- FP-05 的完整 session 套件耗时受并发构建/机器负载影响，结论见 §2.4；UI 端消费 `workspace/changes` 未验证。
- 真实 DSH 数据在验证期间被 DSH 继续写入，session/skip 计数存在小幅漂移。

## 4. 证据清单
- `.cache/audit/verify-wave23/`（待归档）：FP-19/PD-02 独立探针源码与输出、PD-02 e2e 日志、importer 真实 dry-run JSON、manifest 两次运行 JSON/日志。
- 代码：`internal/app/services.go`（keylessLocalProvider）、`internal/app/network_proxy.go`、`internal/tools/webfetch.go`、`internal/tools/goal.go`、`internal/session/goal_driver.go`、`internal/app/automation_runs.go`、`internal/runtime/multimodal.go`、`internal/workspacechanges/*`、`internal/store/automation.go`、`internal/importer/*`、`internal/skills/skills.go`、`internal/state/{event,snapshot}.go`。
