# 独立复核报告 C（verifier-core2）：wave 1 修复验证

> 复核者：verifier-core · 日期：2026-11-05/06 · 对抗性独立验证（不采信修复报告）
> 复核基线：wave 1 提交 `1da4f12` + 文档提交 `da12d44`（任务开始 HEAD）；冻结副本 `/tmp/verify-core2 = git archive da12d44`（+ 从工作树补入被 .gitignore 漏掉的 `internal/importer/credentials.go`，否则 wave1 提交无法编译——见 §3.1）；期间 HEAD 前移到 `6da09e3`（`0bbf051` wave2 部分提交 + docs）。
> 方法：冻结副本内重跑 wave1 定向测试（全绿）＋ **11 个自写独立探针**（`.cache/audit/verify-core2/zz_vc2_*.go`，不复用修复作者的断言）＋ 运行级验证（Xvfb 窗口几何、真 CLI、net/http fake provider、MCP HTTP 真连、干净 checkout 构建/ci）。
> 证据：`.cache/audit/verify-core2/`（`all-probes.log`、`baseline-tests.log`、`ar03-*.log`、`ci-fast-clean-archive.log`、`vc2-*.log` 及探针源码）。未改被跟踪文件、未提交、未回滚他人改动。
> 限制：未接真实模型/真实网络 provider；未在 GitHub Actions 真跑；Windows/macOS 打包未跑；真 panic/OOM 未触发（AR-01 的崩溃机制经单测验证）。

## 0. 结论摘要

- 复核 14 项：**CONFIRMED 13 / PARTIAL 1 / REFUTED 0**。
- 唯一 PARTIAL：**FP-11**——双屏障持久化与 fail-closed **成立**，但 FIX-REPORT 的"屏障态可恢复"**不成立**（无消费者读取 `requested`/`tool_pending` checkpoint；恢复仍走 History/tool_calls）。
- **新发现（P1，已上报 lead）**：当前 HEAD `6da09e3` 在干净 checkout 上 `go test ./...` / `sh scripts/ci.sh --fast` **红**：`internal/app TestCreatorCanSetSettingAndWriteSkill` 断言已被 `0bbf051`（skills 改 live re-scan）改变的行为；worktree 已有未提交的 `modes_test.go` 修正。**合并前必须在干净 `git archive HEAD` 副本上重跑全量 ci**。
- **BUILD-01（wave1 干净 checkout 不可构建）已确认并验证修复**：`0bbf051` 改名后，`git archive HEAD`（6da09e3）`go build ./...` OK；新增 `scripts/check-clean-checkout.sh` + CI 步骤机制正确。
- 其余 12 项（FP-08/09/10/13/18、AR-18、RT-05/10、AR-01/02/03、RT-06/08 无回归）均以独立证据 CONFIRMED，无影响夸大或伪造迹象；仅有少量文档/注释级备注（见 §4）。

## 1. 方法与基线

```sh
# 冻结副本（wave1 代码）
mkdir -p /tmp/verify-core2 && cd /tmp/verify-core2 && git -C /data/mygo archive da12d44 | tar -x
cp /data/mygo/internal/importer/credentials.go internal/importer/   # 见 §3.1：该文件当时被 .gitignore 漏提交
export PATH=/usr/local/go/bin:$PATH GOCACHE=/data/mygo/.cache/go-build GOMODCACHE=/data/mygo/.cache/gomod GOSUMDB=off CGO_ENABLED=0
go build ./...   # OK（补文件后）
go test -count=1 ./internal/store ./internal/runtime ./internal/mcp ./internal/tools ./internal/agentteam \
                ./internal/automation ./internal/provider/openai ./internal/app ./internal/session ./cmd/zeokeel   # 全绿
# 自写独立探针（11 个，见证据目录；均打印 VC2-* CONFIRMED）
go test -count=1 -run 'TestVC2' -v ./internal/tools ./internal/store ./internal/runtime ./internal/session \
                ./internal/app ./internal/mcp ./internal/provider/openai
# 运行级
sh scripts/build.sh native                               # AR-03
sh scripts/build.sh package-linux                        # AR-03 打包
xvfb-run -a -s '-screen 0 1280x800x24' ...               # AR-18（1280x800 屏）
ZEOKEEL_DATA_DIR=/tmp/vc2-ar01 dist/zeokeel-linux-amd64 run --json "hi"   # AR-01 日志落盘
# 当前 HEAD 干净 checkout
rm -rf /tmp/cleanhead-ci && mkdir -p /tmp/cleanhead-ci && cd /tmp/cleanhead-ci && git -C /data/mygo archive HEAD | tar -x
go build ./... && echo CLEAN_BUILD_OK                    # BUILD-01 修复验证
sh scripts/ci.sh --fast                                  # → FAIL：internal/app 测试（§3.2）
```

基线说明：任务开始时工作树已有 wave2 未提交改动（`M cmd/zeokeel/cli.go`、`M internal/runtime/*`、`?? cmd/zeokeel/import_cli.go` 等），本报告以提交内容为准；wave1 单据 `1da4f12` 与其文档提交 `da12d44` 的代码在复核中未再变化。

## 2. 核验矩阵（14 项 + 2 项工程发现）

| 项 | 声称修复 | 独立复核方式与证据 | 判定 |
| --- | --- | --- | --- |
| FP-08 | write_file 原子写 + edit_file read-before-write | 探针 `zz_vc2_fp08_test.go`：write_file 后 inode 改变（rename 而非原地截断）、mode 0640 保留、目录无 tmp 残留、内容正确；未读先 edit 被拒；**跨会话 read 不解锁 edit**（会话级观察）；read→edit 成功；read 后外部改文件→edit 报 stale 且内容未变。 | **CONFIRMED** |
| FP-09 | FTS5 trigram 搜索（增量/排名/删除清索引/LIKE 降级） | 探针 `zz_vc2_fp09_test.go`（公开 Store API）："budget" 命中 2 会话且**标题命中排第一**；索引建立后追加消息立即可搜（增量）；`DeleteSession` 后其消息不再命中；2 rune 短查询仍走 LIKE 返回标题命中。`session_search.go`：LIKE 过滤不变、FTS 仅用于排序、`bm25`、watermark 增量、FTS 建表失败静默回退 LIKE。 | **CONFIRMED** |
| FP-10 | CJK token / 图片 offload / 可配置 context budget | 探针 `zz_vc2_fp10_test.go`：ASCII 100 字符=25 token；CJK 100 字符=100 token（不再 chars/4 低估）；混合文本正确；图片按 `1024/张` 计费；`offloadOldImages(keep 2)` 只替换最旧图片为 `[image omitted to recover context]`、最新 2 条图片消息保留、非图片消息不动、不修改入参。`context.budget_tokens`/`ZEOKEEL_CONTEXT_BUDGET_TOKENS` 解析与 1000..2e6 clamp 已读码确认。 | **CONFIRMED** |
| FP-11 | checkpoint 双屏障（模型请求前 + 副作用工具前）fail-closed + 可恢复 | 探针 `zz_vc2_fp11b_test.go` / `zz_vc2_rt05_fp11_test.go`：`requested` checkpoint 在 provider 调用前落盘、`completed` 最后；Save 失败时 **provider 调用数=0**（fail-closed）；`write_file` 前落 `tool_pending` 且含 user 消息、不含工具结果；只读工具不设屏障；无 store 时无行为变化。**但恢复侧缺失**：全仓无消费者读取 `CheckpointPhaseRequested/ToolPending`；`loadPriorConversation` 仅在 History=nil 时读 checkpoint（app 配了 History），session 恢复走 `tool_calls` 表（`recoverInterruptedSession`）。FIX-REPORT "屏障态可恢复"表述不成立。 | **PARTIAL** |
| FP-13 | MCP streamable-http（URL/Headers/鉴权） | 探针 `zz_vc2_fp13_test.go`：用 SDK `NewStreamableHTTPHandler` 起真实 HTTP MCP server（校验 `Authorization: Bearer`），Manager 以 `Transport:"http"`+URL+Headers 连接成功、发现 `mcp__remote__echo`、真实调用得到 server 结果；**错误 token → Connect 返回错误**（不静默降级）；http 配置带 command 被校验拒绝；stdio 路径不受影响。修复自带 `transport_http_test.go` 同样真连。 | **CONFIRMED** |
| FP-18 | headless CLI + `--permission` 生效（read-only/full） | 探针 `zz_vc2_fp18_test.go`（真 `RunHeadless` + fake SSE provider e2e）：`--permission read-only` 时模型请求 `write_file` 未执行（目标文件不存在）；`--permission full` 时 gated `bash` 真执行（marker 存在且 `denied=0`）；默认 Workspace Write 时 gated 调用被 deny（exit 1、`status=denied`、不执行）。另 CLI 二进制：`run --help` exit 0、未知 flag exit 2。修复点 `headless.go:87 SetBuiltPermission` 已读码确认（验证者发现的 P1 修复）。 | **CONFIRMED** |
| AR-18 | 主/设置窗口创建与恢复按 WorkArea clamp | 探针 `zz_vc2_ar18_test.go`（纯函数：clampSize/clampWindowState/map、主窗口 options、极小屏 800×480 的 min 一并 clamp）+ **Xvfb 运行复现**（1280×800 屏）：A 首启窗口 `xwininfo` = **1280x800+0+0**（旧行为为 1600×1000 越屏）；B 预置 `{x:100,y:80,w:3000,h:2000}` 的 window.json 恢复后 = **1280x800+0+0**（旧行为原样保留越界尺寸）。maximized 标志保留、零尺寸不处理。 | **CONFIRMED** |
| RT-05 | Team 成员继承发起回合的审批上下文，父回合结束 fail-closed | 探针 `zz_vc2_rt05_fp11_test.go`：`WithTurnApprovalFrom(parentTurnCtx, child)` 后 `HasTurnApproval=true`；delegated 调用把 ApprovalRequest 发布到父回合 sink，父侧 Approve 后调用成功返回（**成员可用 gated 工具**）；无父通道的 delegated 调用 fail-closed 并给出 "no approval channel" 指引；普通无 turn 上下文仍是 outside-an-agent-turn。修复自身 `manager_spawnctx_test.go` 亦覆盖 manager 侧 spawnCtx 透传与"不继承取消"。 | **CONFIRMED** |
| RT-10 | 流式 token/reasoning 批量持久化（语义/崩溃窗口） | 探针 `zz_vc2_rt10_test.go`：3 个 token/reasoning 事件在边界事件前**不落库**；随后 Status 触发 flush，落库顺序 `token,token,reasoning,status` 且 Seq 连续；无边界时 **~200ms 定时器**自动落库（轮询确认）。**残余（设计边界，非缺陷）**：`Controller.Close()` 不调用 flush，异常进程退出时缓冲区（≤8KB 或 ≤200ms 的 token/reasoning）可能丢；消息/工具/状态/终态等边界事件均即时落盘，重开消息与工具记录完整。建议写入 known-limitations。 | **CONFIRMED**（含残余说明） |
| AR-01 | 崩溃诊断：日志落盘/轮转/crash 报告 | 运行：干净 DataDir 执行 `zeokeel run` → 生成 `logs/app-20261105.log`（0600），内容含 version/data dir 行；stderr 同步。单测（cmd/zeokeel）：`TestLogWriterCreatesDailyFile`/`RotatesBySize`/`RollsOverByDay`/`TestSetupLoggingWritesFileAndStderr`/`TestWriteCrashReport`/`TestCrashReportPathIsUniquePerSecond` 全 PASS。`main.go`：日志先于一切初始化、`defer recover` 写 crash 文件、`debug.SetCrashOutput` 兜底其他 goroutine。真实 panic 未人为触发（机制经单测+读码确认）。 | **CONFIRMED**（机制级） |
| AR-02 | CI 落地 / flaky 不再算绿 | `/.github/workflows/ci.yml` 存在：gate job 跑 `check-clean-checkout.sh` + `scripts/ci.sh`，shell race 独立 job（20m），coverage artifact 上传；`ci.sh` 覆盖 profile 输出；`gates.sh` 重跑后通过明确记 `flaky` 并计入失败（`ok = FAILED==0 and FLAKY==0`，注释"重跑才通过按失败处理"）；`sh -n` 两脚本语法 OK。**但 HEAD 上 `ci.sh --fast` 实跑为红（见 §3.2）**——CI 落地本身 CONFIRMED，门禁未绿是另一个问题。 | **CONFIRMED**（CI 机制）/ 见 §3.2 |
| AR-03 | 版本注入/体积/打包产物 | `scripts/build.sh` 注入 `-X main.version/commit/buildTime` + `-trimpath -s -w`；带 `ZEOKEEL_VERSION=2.0.0-verify` 构建后 `--version` 输出 `zeokeel 2.0.0-verify (commit deadbee, built 2026-11-05T15:00:00Z, linux/amd64, go1.27.3)`；二进制 **34,062,599B**（wave1 前 49.5MB，-31.8%）；`package-linux` 产出 `zeokeel-linux-amd64.tar.gz`（12.6MB）、arm64 包，`dist/SHA256SUMS.txt` `sha256sum -c` 全部 OK，tar 内含 `zeokeel` 二进制 + `README-LINUX.txt`。备注：无 `.git` 的 archive 中默认 version 回落 `0.1.0-dev`，发布构建必须显式 `ZEOKEEL_VERSION`（脚本已说明）。 | **CONFIRMED** |
| RT-06/08 | 默认无整体流超时 + idle 超时 + 429/5xx 重试（无回归） | 探针 `zz_vc2_rt0608_test.go`：429→自动重试→成功（server 收到 2 次请求，`Retry-After:0` 生效）；400 **不重试**（1 次）；stall 流在 150ms idle watchdog 下报 `stream idle timeout`（耗时 ~150ms 而非挂死）；慢但持续输出的流在**默认无整体 deadline** 配置下完整收完（`01234`）。与 `3111614`/`6bd0720`/`3100af0` 三个并发提交语义一致；`retry.go`/`idle.go`/`openai.go` 读码确认重试仅在可见输出前、可重试集合正确。 | **CONFIRMED** |
| BUILD-01 | wave1 提交干净 checkout 不可构建 | 见 §3.1：`git archive da12d44` + `go build ./...` → `undefined: CredentialRef`；`git cat-file -e da12d44:internal/importer/credentials.go` = 128；`.gitignore:6 credentials*` 命中。`0bbf051` 改名 `import_credentials.go` 后：`git archive 6da09e3` 干净副本 `go build ./...` **OK**；新增 `check-clean-checkout.sh` 在 CI 前置运行（机制正确）。 | **CONFIRMED + FIXED** |
| HEAD 红 | 当前 HEAD 干净 checkout 测试失败 | 见 §3.2：`git archive 6da09e3` 干净副本 `sh scripts/ci.sh --fast` → `FAIL internal/app`，`TestCreatorCanSetSettingAndWriteSkill` 断言 "want empty before rebuild"；单测确定性复现（2/2 次）。原因：`0bbf051` 将 skills 改为 live re-scan（`internal/skills/skills.go` +244），wave1 测试未同步；worktree 已有未提交的 `modes_test.go` 修正（改成期望 catalog 含 creator-probe）。 | **新发现（P1）** |

## 3. 新发现（已上报 lead）

### 3.1 [BUILD-01] wave1 提交的干净 checkout 不构建（已修复，已验证）

- 现象：`internal/importer/credentials.go`（定义 `CredentialRef`/`InspectCredentials`，被已提交的 `importer.go:239,274`、`importer_test.go:97` 引用）被 `.gitignore:6` 的 `credentials*` 规则忽略 → 从未进入提交；`1da4f12`/`da12d44` 均无此文件。
- 复现：`git archive da12d44 | tar -x` → `go build ./...` → `internal/importer/importer.go:239:21: undefined: CredentialRef`（exit 1）。wave1 "全绿"是在含未跟踪文件的脏工作树上跑的。
- 影响：干净 clone / CI（新增 ci.yml 的 `go build`/`go test`）/ 发布打包全部失败；属发布阻断。
- 修复验证：`0bbf051` 改名 `internal/importer/import_credentials.go`；独立验证 `git archive 6da09e3` 干净副本 `go build ./...` **OK**；CI 新增 `scripts/check-clean-checkout.sh`（先 archive 再 build，正是该类 bug 的守卫），逻辑已读码确认。**CONFIRMED / FIXED**。

### 3.2 [P1·待修] 当前 HEAD `6da09e3` 干净 checkout 测试红（worktree 已有未提交修复）

- 复现（干净 `git archive HEAD` 副本）：
  `sh scripts/ci.sh --fast` → `--- FAIL: TestCreatorCanSetSettingAndWriteSkill`（`internal/app/modes_test.go:192: pre-existing skill catalog = "Available skills:\n- creator-probe: ...", want empty before rebuild`）→ `CI FAIL: go test`，exit 1；其余 20 包全绿。
- 单测确定性复现 2/2；`git archive da12d44`（wave1 单据）上同一测试 **PASS** → 不是 wave1 引入，是 `0bbf051` 的 skills live re-scan 改动（`internal/skills/skills.go` +244）与 wave1 测试断言不一致。
- 修复已在工作树但**未提交**：`M internal/app/modes_test.go` 已把断言改为"同一进程内 refresh 后的 catalog 含 creator-probe"。
- 影响：若从 HEAD 提交跑 GitHub CI，"gate" job 必红；"wave1 门禁绿/HEAD 绿"不成立。
- 建议：wave2 提交必须包含 `modes_test.go` 修正，并在干净 `git archive HEAD` 副本跑 `sh scripts/ci.sh`（非仅 --fast）绿后再宣称 gate 绿；CI 的 clean-checkout job 建议加 `go vet`/`go test`（当前只 build）。

## 4. 备注 / 建议（不影响 CONFIRMED 判定）

1. **FP-11 措辞**：FIX-REPORT 写"屏障态可恢复"→ 应改为"屏障持久化 + fail-closed（诊断/恢复预留）"，或补一个恢复消费者（读取 `tool_pending` 把未完成调用标为 `recoveredToolResult`）。否则 master 报告会过度承诺。
2. **RT-10 崩溃窗口**：建议把"崩溃/强退时最多丢失 ≤8KB 或 ≤200ms 的 token/reasoning 事件，消息/工具/终态不丢"写入 `known-limitations.md`；`Controller.Close` 可顺手 flush。
3. **FP-10 注释陈旧**：`services.go` context budget 注释仍写 "estimated as chars/4"，实际已 CJK-aware（纯注释问题）。
4. **AR-03 版本**：无 `.git` 的构建默认 `0.1.0-dev`，发布脚本应强制要求显式 `ZEOKEEL_VERSION`（或从 tag 读），避免发布产物版本号不明。
5. **AR-01 真机 panic 冒烟**：机制与单测已验证，但发布前建议在真机做一次 panic→crash 文件落盘冒烟（Windows GUI 无 stderr 场景）。
6. **FP-09 范围提醒**：FTS 只用于"排序 LIKE 已过滤结果"，未改变过滤语义（与修复声明一致）；若产品想要"非字面子串召回"需另立需求。

## 5. 未验证 / 残余风险

1. 未接真实模型/真实网络：FP-18/RT-06 的 provider 交互用本地 fake SSE/httptest；真实 429/慢流/长 reasoning 未跑。
2. RT-06 "无整体超时"仅验证默认配置下慢流成功；未真跑 >60s 长推理流。
3. AR-01 未真实触发 panic/OOM；crash 机制为单测 + 读码确认。
4. AR-02 未在 GitHub Actions 真跑（本地等价脚本 + 语法检查）；HEAD 红问题见 §3.2。
5. 打包仅验证 `package-linux`；`package-windows`/`package-macos` 未跑（需 Windows/macOS 产物核验）。
6. 未复核 wave2 未提交/未落地项（PD-06、FP-19、FP-17 等）；本报告只判 wave1 承诺。
7. RT-10 未做真实进程崩溃实验（kill -9 时机控制）；崩溃窗口为代码语义推导。

## 6. 复核产物

- `.cache/audit/verify-core2/all-probes.log`：11 个独立探针一次全跑（均打印 `VC2-* CONFIRMED`，exit 0）。
- `.cache/audit/verify-core2/baseline-tests.log`：冻结副本 wave1 定向测试全绿。
- `.cache/audit/verify-core2/ar03-build.log` / `ar03-package-linux.log`：版本注入、体积、Linux 打包与校验和。
- `.cache/audit/verify-core2/ci-fast-clean-archive.log`：干净 HEAD 副本 `ci.sh --fast` 的失败证据（§3.2）。
- 探针源码：`.cache/audit/verify-core2/zz_vc2_*.go`（可直接拷贝回对应包复跑）；冻结副本 `/tmp/verify-core2`（da12d44）。
