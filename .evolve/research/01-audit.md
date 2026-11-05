# ZeoKeel 自进化闭环审计（只读）

> 审计人：repo-auditor（Team 成员）｜审计时间：2026-11-04 18:50–19:10 CST
> 工作区：`/data/mygo`（ZeoKeel：纯 Go 桌面 Agent，mygo 原生 UI + eino runtime，目标与 DSH 0.2.1-rc.2 1:1）
> 环境：go1.27.3 linux/amd64（`/usr/local/go/bin`，需手动加 PATH）；无 `DISPLAY`/`WAYLAND_DISPLAY`；96 vCPU / 32 GB；`git 2.43.0` 已安装但仓库**无 `.git`**
> 审计快照：`/tmp/audit/snapshot`（220 个 `.go` 文件，sha256 清单 `/tmp/audit/source.sha256`，汇总 sha256=`1d28758475025cc38d2d654b049ff636419782061353008d9993ca6531927829`，18:57 复制）
> 说明：live 树在审计期间被其他 teammate 并发修改（19:02 时与快照相比仅 `internal/shell/automation_ui.go`、`internal/shell/plugin_details.go` 两文件 drift）。**本文行号以快照为准**；gate 数据来自快照，避免并发编辑导致的门禁抖动。
> 证据存档：`/tmp/audit/make-check-snapshot.log`（全链路 gate 日志）、`/tmp/audit/snap-extra.log`（分项计时）、`/tmp/audit/test-run{1,2}.log`、`/tmp/audit/*.sha256`

---

## 0. 结论摘要（先看这 8 条）

1. **闸门存在、无头可跑、当前全绿，但只是「人读的文本 + exit code」**：`make check` 80–84s 全绿；像素 diff（浅色 5 屏 + 暗色/窄屏 5×2）纯软件渲染、无 `DISPLAY` 可跑、秒级；但没有任何 JSON/scoreboard，且 `make check` 不传 `-count=1`（重复运行 17 个包命中 `(cached)`，0.62s 就"全绿"）。
2. **闸门会污染仓库**：`scripts/check-nocgo.sh` 的四目标交叉编译不指定 `-o`，把 `zeokeel` / `zeokeel.exe` 写回仓库根；实测跑完后 `file zeokeel` = **Mach-O 64-bit arm64**（最终非 Windows 目标是 darwin/arm64），宿主机 linux 二进制被覆盖，当前根目录 `zeokeel` 不能在本机执行。
3. **自驱动零件齐全，但没有「无人值守」通道**：`automation`（cron 子集 + scheduler）、`schedule_*`、`ralph`、`goal`、`subagent/workflow/team`、`job_*`、`terminal_*`、`session_query`、`inspect`、`report` 都在生产 registry 里；然而 `cmd/zeokeel` 只有 GUI 入口（无 flag/无 headless），自动化任务默认会被 approval gate 拦住 `bash/write_file/edit_file`，且自动化 turn 没有超时 → 无人值守的"修/测"会卡在 waiting_approval。
4. **知识层「机制有、内容无」**：`internal/prompt` 是 37 节编译期 registry（95.9% 覆盖），AGENTS.md/skills 在构建 agent 时从 workspace 读取；但仓库根**没有 AGENTS.md、没有 skills/ 目录**，也没有跨会话 learnings/memory。
5. **会话可回放，但两条声明中的持久化路径是死代码**：SQLite `messages`+`events` 可完整回放（`session_query`/`inspect` 可读）；`store.AppendTranscript/ReadTranscript`（JSONL，PLAN §9 称其为"唯一真相"）与 `tool_calls` 表在生产代码中**零调用**。
6. **安全网最弱**：无 git、无 CI、无代码快照/回滚；`worktree` 工具因无 git 实际不可用；仓库 26 GB（`.cache` 25.7 GB、根目录二进制 91 MB、`dist/` 86 MB、`baseline/` 106 MB）；`.gitignore` 只有 8 行，不含二进制/dist/artifacts。
7. **观测性只有「事件持久化 + stderr log」**：无 metrics、无 scoreboard、无 crash reporting、应用层无 `recover()`；自动化 run 只有 `last_run/next_run`，无运行记录/失败归因；`docs/STATUS.md` 是手工状态真源。
8. **自进化闭环最短路径**：P0 = git 安全网 + 无人值守权限策略 + headless 入口 + 机器可读 gate 汇总 + 自进化 run 记录；P1 = 产品级 headless e2e、死代码接线、仓库 AGENTS.md/skills、CI、像素预算余量；P2 = 观测性/缓存治理等（见 §7）。

---

## 1. Q1 质量闸门清单（命令 / 耗时 / 无头 / 机器可读）

### 1.1 汇总表（快照 `/tmp/audit/snapshot` 实跑）

| # | 闸门 | 实际命令 | 实测耗时 | 无头? | 失败输出机器可读? | 证据 |
|---|---|---|---|---|---|---|
| 1 | gofmt | `gofmt -l ./cmd ./internal` | <0.1s | ✅ | ✅（stdout 为空=通过） | `Makefile:18-20` |
| 2 | vet | `CGO_ENABLED=0 go vet ./...` | 1.3s（含 `-tags e2e` 6.4s） | ✅ | ✅（exit code + 文本） | `Makefile:12-13` |
| 3 | 单测/集成测试 | `CGO_ENABLED=0 go test ./...`（默认 536 个 Test，不含 6 个 e2e-tag） | 全量强制 `-count=1` **21.7s** 墙钟（user 40s）；`make check` 内 shell 20.3s、tools 6.0s | ✅ | 半：`go test` 文本；`-json` 可用但**未接线** | `Makefile:9-10`；`/tmp/audit/make-check-snapshot.log` |
| 4 | no-cgo + 四目标交叉编译 | `sh scripts/check-nocgo.sh` | 暖缓存 9.7s；`make check` 整链 80–84s | ✅ | ✅（文本 + exit） | `scripts/check-nocgo.sh:1-24` |
| 5 | 像素 diff（浅色 5 屏） | `go test -count=1 -run '^TestScreensBaseline$' ./internal/shell` | 测试 1.38s；包级 6.9s（含编译） | ✅（`ui.Render` 软件渲染，不依赖 DISPLAY） | 半：`t.Logf` 百分比（需 `-v`）+ 红 diff PNG；失败 `t.Errorf` | `internal/shell/screens_test.go:14-82` |
| 6 | 像素 diff（dark/narrow 5×2） | `-run '^TestScreensDarkNarrow$'` | 1.71s | ✅ | 同上 | `internal/shell/baseline_dark_test.go:67-235` |
| 7 | 交互 / a11y | `go test ./internal/shell`（165 个 Test，`ui.NewTester`） | 20.3s（含像素） | ✅ | ✅（无头 headless） | `internal/shell/*_test.go`（32 个测试文件） |
| 8 | 真实模型 E2E | `sh scripts/e2e-model.sh` → `go test -tags e2e -run TestRealModel`；`sh scripts/e2e-tools.sh` → `-run TestRealModelToolCall` | **本次未运行**（任务要求；需真模型/凭据/网络） | 进程本身无头，但需网络 + `DEEPSEEK_API_KEY` 或 `/root/.dsh/.credentials.yaml` | ✅（go test） | `scripts/e2e-model.sh:24,30`；`scripts/e2e-tools.sh:17,25` |
| 9 | race | `go test -race ...` | **不可用**：`go: -race requires cgo`（C1 强制 `CGO_ENABLED=0`） | – | – | 实测 |
| 10 | 覆盖率 | `go test -cover -count=1 ./...` | 44.6s | ✅ | ✅（数字文本） | 实测，见 §2 |
| 11 | 性能预算 | **不存在**（无 Benchmark，无 PLAN §10.3 的冷启动/首帧断言） | – | – | – | `grep -rn '^func Benchmark'` 为空 |
| 12 | 文档承诺但缺失 | `scripts/pixel-gate.sh`、`scripts/lint-style-literals.sh`、`scripts/cross-build.sh` 均不存在；`Makefile` 无 `verify`/`e2e`/`pixel` 目标 | – | – | – | `docs/PLAN.md:894-902,917-921`；`baseline/README.md:29` |

### 1.2 关键细节

**（a）`make check` 的真实构成与耗时量级**
`Makefile:18-20`：`check: fmt vet test` → 再查一次 gofmt → `sh scripts/check-nocgo.sh`。快照实跑整链 **80.4s**（`real 1m20.383s`，`user 6m42s`，4 次交叉编译占大头）；live 树首跑 **84.4s**（`user 6m0s`）。冷 `go build ./...` 17.9s，暖 4.1s；`go vet` 1.3s；全量测试强制 `-count=1` 21.7s。

**（b）测试缓存会让「刚跑过」变假象**
`Makefile:10` 是裸 `go test ./...`，无 `-count=1`。实测同一快照：第一次 `go test ./...` 20.4s，第二次 **0.62s 且 17 个包全是 `(cached)`**。对自进化闭环这是危险信号：agent 重复执行 gate 会得到"秒过"的旧结果。建议 verify 脚本固定 `-count=1`（或 `GOFLAGS=-count=1`）。

**（c）像素门禁：无头、快、但只有文本指标**
- 浅色（`screens_test.go:30-34` 预算；实跑 margin 见括号）：

  | 屏 | masked 实测/预算 | raw 实测/预算 | 余量（masked） |
  |---|---|---|---|
  | home | 0.86% / 2.0% | 2.13% / 3.5% | 1.14pp |
  | session | 6.27% / 8.5% | 8.29% / 11.0% | 2.23pp |
  | plugins | 2.80% / 4.5% | 4.25% / 6.5% | 1.70pp |
  | settings | 2.49% / 4.2% | 2.99% / 5.0% | 1.71pp |
  | automation | 0.82% / 2.2% | 2.55% / 4.0% | 1.38pp |

- dark/narrow（`baseline_dark_test.go:67-100` 预算）：**余量已很薄**——session/narrow `31.47% vs 32.0%`（仅 0.53pp）；session/dark `23.15% vs 24.5%`；home/dark `9.26% vs 12%`；settings/dark `5.03% vs 6%`；plugins/dark `3.14% vs 4.5%`。任何几何小改动都可能先打红 dark/narrow 而不是 1:1 主判据。
- 输出：`t.Logf("%-10s diff ... (masked ..., raw ...)")`（`screens_test.go:72`，注意第一个 "diff" 打印的其实是 raw，日志有误导）；失败为 `t.Errorf` 文本 + exit code；渲染图/红图写到 `../../artifacts/{screens,diff}`（`screens_test.go:37-40`，可用 `ARTIFACTS`/`SCREEN` 环境变量重定向/过滤）；dark/narrow 同样支持 `SCREEN`/`ARTIFACTS`（`baseline_dark_test.go:108-109`）。**没有任何机器可读的 scoreboard（JSON）**。

**（d）e2e 是 opt-in，且与 DSH 凭据耦合**
`internal/e2e/e2e_test.go:1` 与 `internal/app/tools_e2e_test.go:1` 均为 `//go:build e2e`；`scripts/e2e-model.sh:30` / `scripts/e2e-tools.sh:25` 用 `-tags e2e` 跑。无 key 时 `e2e-model.sh` exit 1（`:24`）、`e2e-tools.sh` exit 2（`:17`），测试内部也 `t.Skip`。`make check` **从不**包含它们 → 日常门禁对"真实模型能否正确调工具"零覆盖，只有人工运行。

**（e）闸门缺陷：交叉编译回写仓库根**
`scripts/check-nocgo.sh:18-22`：
```sh
for t in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do
	GOOS=... GOARCH=... go build ./cmd/zeokeel
done
```
无 `-o` → 每个目标都写仓库根同名文件；最后 darwin/arm64 覆盖 linux 二进制。实测跑完：
```
zeokeel:     Mach-O 64-bit arm64 executable, flags:<|DYLDLINK|PIE>
zeokeel.exe: PE32+ executable (console) x86-64, for MS Windows
```
这不是单纯产物污染：**仓库根的 linux 二进制被破坏**，任何"直接 `./zeokeel` 冒烟"都会以 shell 报 `Syntax error` 失败（本次已复现）。

**（f）无 CI**
`/data/mygo` 下无 `.github/`、无 `.gitlab-ci.yml`、无任何 CI 配置。`docs/PLAN.md:917-921` 声称有 build-linux/darwin/windows/smoke-linux/lint 五个 job，实际均未落地；`docs/STATUS.md` 的"全绿"全靠人手执行、手写记录。

---

## 2. Q2 测试覆盖与缺口

### 2.1 规模与语句覆盖（快照 `go test -cover -count=1 ./...`，44.6s）

| 包 | 非测试 .go | 测试文件 | Test 函数 | 语句覆盖率 |
|---|---|---|---|---|
| internal/agentteam | 1 | 2 | 10 | 86.1% |
| internal/app | 2 | 4 | 15（默认 14，1 个 e2e） | 51.6% |
| internal/automation | 2 | 2 | 15 | 90.3% |
| internal/design | 5 | 2 | 7 | **17.5%**（常量/样式数据为主） |
| internal/e2e | 0 | 1 | 1（e2e-tag） | 默认不参与 |
| internal/icons | 1 | 1 | 4 | 90.9% |
| internal/mcp | 2 | 2 | 14 | 79.1% |
| internal/pixeltest | 2 | 1 | 3 | 35.5% |
| internal/prompt | 2 | 1 | 12 | 95.9% |
| internal/provider/openai | 3 | 3 | 29（默认 25，4 个 e2e） | 85.2% |
| internal/runtime | 15 | 9 | 57 | 72.0% |
| internal/session | 4 | 3 | 22 | **56.3%** |
| internal/shell | 33 | 32 | 165 | 84.3% |
| internal/skills | 1 | 1 | 11 | 90.1% |
| internal/state | 5 | 3 | 24 | 92.6% |
| internal/store | 10 | 4 | 26 | **62.1%** |
| internal/tools | 32 | 26 | 118 | 80.6% |
| internal/websearch | 1 | 1 | 9 | 86.2% |
| cmd/zeokeel | 1 | 0 | 0 | **0.0%** |

合计：220 个 `.go` = 122 生产文件（37,269 行）+ 98 测试文件（26,570 行）；542 个 Test 函数（含 6 个 e2e-tag；默认 536）。

### 2.2 最薄弱区域（按"自进化闭环需要"排序）

1. **`cmd/zeokeel` 0%**：`main.go` 只有 20 行、无测试、无 CLI 行为可测。
2. **`internal/session` 56.3%**：`controller.go` 76 KB（本轮被并发修改中）是全局状态机核心，仅 3 个测试文件；渲染/审批/模式/工作区大多覆盖，但"重建 agent、跨 turn 恢复、取消/超时、自动化 Submit"边界不易覆盖。没有 turn 超时概念，自然也没有超时测试。
3. **`internal/app` 51.6%**：`app.go` 生命周期（单实例 lock、tray、OnClosed、startAutomation）依赖 GUI，无法在 CI 里测；只有 services/tools 注册层被覆盖。**产品级**（创建会话→提交→工具→审批→停止→恢复）无自动化测试。
4. **`internal/store` 62.1%**：事件/消息/会话主路径覆盖好；`tool_calls` 表、JSONL transcript 只有单元测试没有生产调用（见 §4），`workspace`/迁移类路径覆盖偏薄。
5. **e2e 只有 2 条真实模型用例**（纯文本 1 + 工具调用 1），provider 另 4 条 live 用例；全部 opt-in、依赖外部 key/网络，默认门禁无覆盖。
6. **测试基础设施缺口**：无 `-race`（CGO 禁止）、`testing.Short` 零使用（`-short` 是 no-op）、无 Benchmark、无性能预算断言（PLAN §10.3 的"冷启动 ≤400ms"等未实现）、无覆盖率阈值门禁。

---

## 3. Q3 既有「自驱动」能力

### 3.1 `internal/automation`：能跑任意任务，但不是无人值守引擎

- **调度器**：`Scheduler` 轮询 store（`scheduler.go:135-190` `Tick` 确定性、可注入时钟；`Start` 每 `Interval` tick，`app` 传 **30s**，`app.go:293-304`）。
- **任务内容**：`OnFire` 由 app 注入（`app.go:269-304`）：**新建 session → `Submit(ctx, id, task.Prompt)`**，prompt 原样交给完整 agent（含全部已注册工具）→ 所以"任意任务"成立。
- **限制（对自进化闭环致命）**：
  1. 只在 GUI 进程内运行；app 退出/崩溃 = 调度停止；无 daemon/CLI 触发。
  2. cron 是极窄子集（`cron.go:5-13`）：仅 `*/N * * * *`、`M H * * *`、`@hourly`、`@daily`，不支持一次性/日期/星期/区间。
  3. 默认权限 Workspace Write 会对 `bash,write_file,edit_file,...` 发 approval（`services.go:35,405-411`；`runtime/model_adapter.go:351-390`），而自动化 turn 用 `context.Background()`（`app.go:273-274`）**没有超时** → 无人值守时会永久卡在 waiting_approval，直到人点按钮或 app 退出。
  4. 无 run 记录：`store.Automation` 只有 `last_run/next_run`（`store/automation.go:14-24`），失败原因只在内存 `Scheduler.LastError`（`scheduler.go:199-204`）或 stderr。
  5. 无并发上限/去重：每次 fire 新建 session，上一轮未结束时仍可再开一轮。

### 3.2 工具层的自驱动原语（全部注册进生产 registry：`app/services.go:228-360`）

| 工具 | 作用 | 关键限制 | 证据 |
|---|---|---|---|
| `schedule_create/list/update/delete` | **agent 自己**创建/改/删持久化 automation（与 UI 同一 store） | 同 3.1 的 cron/审批/无 run 记录限制 | `internal/tools/schedule.go:18-133` |
| `ralph` | 前台 fresh-agent 轮次循环（共享 objective、上轮摘要 2KiB、DONE/COMPLETE 完成标记、默认 3/上限 10 轮） | 阻塞当前 turn；需要 model runner；depth 守卫（root 才能发起） | `internal/tools/ralph.go:51-84,190-250` |
| `goal` | 会话目标 set/get/complete/abandon，折入 session banner | **会话级**，不跨 session；无持久化表（靠事件回放重建） | `internal/tools/goal.go:28-99`；`session/controller.go:1866-1895` |
| `subagent` / `workflow` | 进程内子 agent / 顺序多步；depth 默认 2 | 需要 model；受限工具子集；无持久化队列 | `internal/tools/subagent.go:16-88`；`internal/tools/workflow.go:13-132` |
| `team`（agent_teams 实验开关，默认关） | 异步 agent manager（spawn/list/status/wait/close） | 默认关闭；无跨进程 | `app/services.go:286-294`；`internal/agentteam/` |
| `job_start/list/output/kill` | 后台 shell 命令（8 并发、单任务 256KB 输出、进程组 kill） | **内存态**，agent 重建/进程退出即丢；默认 workspace-write 下 `job_start` 自身不受 approval（只有 `bash` 在默认 patterns 里！） | `internal/tools/jobs.go:26-60,72-133`；`services.go:35` |
| `terminal_open/list/read/send/signal/close` | 持久 PTY（unix）会话 | 内存态、30s 空闲回收 | `internal/tools/terminal*.go` |
| `report` | 给 subagent/ralph 用的结构化 JSON 摘要（16KiB） | 无状态、不落盘 | `internal/tools/report.go:17-42` |
| `session_query`（5 个） | 只读检索/读取/追踪会话事件（可当"自我复盘"数据源） | 只读 | `internal/tools/session_query.go:98` |
| `inspect` | 只读快照：plugins/settings（脱敏）/sessions/skills/mcp | **被 `plugin.developer_tools.enabled` 门控，默认 false** | `app/services.go:315-317`；`internal/tools/inspect.go:70-112` |

### 3.3 runtime agent loop

- eino ADK 驱动；`DefaultMaxIterations = 20`（`runtime/agent.go:22`），每轮 Token/Reasoning/ToolStart/ToolEnd/Usage/Error 事件流；完成回合才写 history/checkpoint（`agent.go:421-433`）。
- **审批闸门**：工具调用前 `awaitApproval`（`model_adapter.go:351-390`）→ 发 `ApprovalRequest` 事件 → `gate.Wait(ctx, callID)` 阻塞直到 Approve/Deny 或 ctx 结束；ctx 结束返回 `ErrApprovalExpired`（`runtime/approval.go:12-31`）。**没有自动审批/超时策略**；`AllowAlways` 规则只在内存（`approval.go:83-95`；`session/controller.go:1293`）。
- **无 turn 超时**：`session.Submit`/`agent.Run` 全程没有 `WithTimeout/WithDeadline`（grep `WithTimeout|WithDeadline|Deadline` 非测试代码为空）→ 模型卡住/审批卡住都只能人工 Stop。

### 3.4 headless / CLI 入口：**没有**

- `cmd/zeokeel/main.go:1-20` 只调 `app.Run()`；`grep os.Args|flag.`（cmd + internal/app）为空 → **无子命令、无 `--headless`、无 prompt 注入**。
- 无 DISPLAY 直接启动（用 `/tmp` 重编的 linux 二进制实测）：
  ```
  $ env -u DISPLAY ZEOKEEL_DATA_DIR=/tmp/audit-data ./zeokeel
  2026/11/04 18:55:04 mygo: cannot open display (is DISPLAY or WAYLAND_DISPLAY set?)
  exit=1
  ```
  仓库 `.cache/smoke.log` 也显示历史冒烟用 `xvfb-run`（docs/PLAN.md:298/891 提及），但**没有形成脚本/case**。
- 唯一可脚本化的接缝在 Go test 里：`app.BuildServicesWith`（`services.go:61`）+ `registerAllTools`（`services.go:228`）+ 假 provider（shell 测试里的 `shellProvider`）。没有对外的"对话式 CLI"。

---

## 4. Q4 自我改进所需的持久化与知识层

### 4.1 提示词层（`internal/prompt`）

- **编译期 registry**：37 个有序 section（`prompt/registry.go:424` 的 `orderedRegistry`，ID 从 `harness:identity` 到 `deployment:persona-suffix`），EN/ZH 双语、按"已注册工具名/别名/MCP 前缀"门控、含动态 section（workdir/sandbox/approval/AGENTS.md/skills catalog/time/file-reference）。`Compose`（`prompt.go:172`）拼接；`Sections()`（`:194`）只读导出；12 个测试覆盖排序/门控/语言/确定性（95.9%）。
- **运行时改提示词：只有"半条路"**：
  - ✅ workspace 根 `AGENTS.md` 在构建 agent 时读取（`app/services.go:473-474`，注入 DSH 风格 workspace instruction）；
  - ✅ `<workspace>/skills/<name>/SKILL.md` 在构建时扫描并注册 `skill` 工具（`app/services.go:342-346`；`internal/skills/skills.go:1-22,83`）；
  - ✅ 设置/插件/工作区变化会触发 agent 重建（`session/controller.go:1813-1821`；`app/app.go:189`），live 树新代码还在插件设置保存时主动 `RebuildAgent`（快照之后 drift 的 `plugin_details.go`）；
  - ❌ 系统提示词**文本本身**只能改 Go source（`registry.go`）后重编译；无 setting/API 可改；agent 也没有"改自己 instruction"的工具。`docs/prompts.md` 有 37 行 DSH 对照表。
- **知识资产现状**：仓库根**无 `AGENTS.md`**、**无 `skills/` 目录**（`find` 为空）；`baseline/` 有截图/controls/css/behavior 文档；`docs/` 有 PLAN/STATUS/COMPONENT-PARITY/UI-PARITY-AUDIT/prompts.md。对 agent 而言这些都是"可读文件"，没有任何自动加载/索引。

### 4.2 store / 会话回放

- **表**（`store/schema.go:14-77`）：`schema_version, workspaces, sessions, messages, tool_calls, events, settings, automations`（迁移 v2 只补 automations index）。
- **可回放**：`messages` 是 transcript 真相；`events` 是 append-only 机器可读 trace（`session/events.go:90-190`：`token/reasoning/message/tool_start/tool_end/approval_request/usage/status/error`；ToolStart 含 args，ToolEnd 含 result/isError/duration，Usage 含 in/out/cache/turns/steps）。`session/history.go:23` 用 `ListMessages + ListEvents` 重建 Snapshot，并把 tool result 折回 todo/goal/question/artifact/team 面板。
- **自省工具**：`session_query` 5 个只读工具（search/event_search/event_read/event_trace/session_trace，`session_query.go:98`）；`inspect`（默认 off）；`report`（无状态）。
- **死代码/文档漂移（重要）**：
  - `AppendTranscript/ReadTranscript`（`store/transcript.go:52,108`，唯一 JSONL 实现）**生产零调用**；`store/event.go:15` 注释与 `docs/PLAN.md:775`"session.jsonl 是唯一真相"均未落地。实际真相是 SQLite。
  - `tool_calls` 表（`schema.go:42-54`）零读写。
- **记忆层**：**没有跨会话 learnings/memory**。goal/todo/team/questions 都是 controller 内存 map（如 `controller.go:1866`），关闭后靠事件回放恢复"同会话"状态；新会话从零开始。`report` 是唯一"结构化结论"通道，但它明确不写文件/不写 session（`report.go:17-20`）。

---

## 5. Q5 安全网

| 项 | 现状 | 证据 |
|---|---|---|
| git | **无**。`git rev-parse --show-toplevel` → `fatal: not a git repository`。git 2.43.0 已装 | 实测 |
| 影响 | 无 diff/无 attribution/无 revert/bisect；`worktree` 工具（`internal/tools/worktree.go:130-155` 先 `git rev-parse --show-toplevel`）实际不可用；PLAN 里"基线更新单独 PR""基线 commit"（`docs/PLAN.md:171`、`baseline/README.md:26-32`）全部无法执行 | 实测 + 代码 |
| CI | **无**（见 §1.2f）；全部 gate 靠人手 | `ls .github` 失败 |
| 代码快照/回滚 | **无**。runtime `Checkpoint` 只存对话（`runtime/checkpoint.go:11-45`），不存文件；`str_replace_editor` 的 undo 仅内存 20 份（`tools/editor.go:25-26,83`）；approval `AllowAlways` 规则仅内存（`approval.go:83-95`） | 代码 |
| 大文件 | 总计 **26 GB**：`.cache` **25,704 MB**（其中 `go-build` 24,748 MB）、`baseline` 106 MB、`dist` 86 MB、根 `zeokeel` 45 MB + `zeokeel.exe` 46 MB、`.probe` 12 MB、`artifacts` 4 MB | `du -sm` |
| `.gitignore` | 仅 8 行：`.env/.env.*/*.secret/*.key/credentials*/.cache//.probe/`；**缺** `zeokeel`、`zeokeel.exe`、`dist/`、`artifacts/`、`sessions/`、`.zeokeel-worktrees/`、coverage 文件 | `.gitignore:1-8` |
| 构建污染 | `check-nocgo.sh` 回写根二进制（§1.2e）；像素测试默认写 `artifacts/`（`screens_test.go:37-40`）；`make clean` 会 `rm -rf .cache build dist`（`Makefile:28-29`），而 `.cache` 里有 25 GB 共享缓存 | 代码 + 实测 |
| 基线可追溯 | `baseline/VERSION`、`baseline/geometry.json`、`baseline/tokens.json` **均不存在**（README 却要求）；`docs/PLAN.md:171` 的"锁版本"无落点 | `ls baseline` |
| 凭据 | e2e 脚本读 `/root/.dsh/.credentials.yaml`（`scripts/e2e-model.sh:10-24`）；`.gitignore` 覆盖 `*.key/.env` 但 `docs/prompts.md` 权限 600 之外无统一 secret 扫描 | 脚本 |

---

## 6. Q6 观测性

- **日志**：只有 `log.Printf` → stderr，集中在 `internal/app`（`app.go:144,158,214,219,234,256,272,276,282,291,299` 等）；`cmd/zeokeel/main.go:15,18`。**无** `slog`、无文件日志、无轮转、无级别；`/root/.zeokeel/logs/` 是空目录。mygo 的告警（libayatana/dbus、sonic fallback、llvmpipe）也只在 stderr（`.cache/smoke.log`）。
- **指标/scoreboard**：**无**（`grep metrics|prometheus|otel|scoreboard` 命中仅注释。"STATUS.md 是唯一状态真源"= 人工维护，`docs/STATUS.md:1-3`）。无自动化 run 的通过率/耗时/失败分布。
- **崩溃/错误上报**：无 crash reporter；应用层无显式 `recover()`（`grep 'recover()' internal --include='*.go' | grep -v _test` 为空）→ turn/tool goroutine 的 panic 是否会拖垮进程取决于 eino/adk 内部，风险未兜底。
- **已有可观测资产**：
  - `events` 表（JSON payload，含 tool args/result、isError、duration、usage、error、approval）→ 可重建 trajectory；`session_query` 可被 agent 自己检索；`inspect` 可看 sessions/settings（脱敏）；
  - 像素测试产物 `artifacts/screens|diff|screens-dark|diff-dark|screens-narrow|diff-narrow`（36 个文件）；
  - 会话错误进 Snapshot.LastError 并由 UI 渲染（`state/event.go:107`）。
- **缺口**：观察不到的恰好是"自进化"最需要的——gate 通过率趋势、每次自动修复的耗时/失败原因、模型/工具错误分布、automation run 记录、自评审结论。`Scheduler.LastError` 只在内存（`scheduler.go:199`）；automation 失败只 `log.Printf`（`app.go:272,276`）。

---

## 7. Q7 缺口清单（P0/P1/P2）+ 落点建议

> 建议优先级依据：不满足则"agent 自己跑一轮 找 bug→修→测→评审"无法安全闭环。

### P0（闭环阻断项，建议本轮就做）

| ID | 缺口（一句话） | 落点建议 | 验收信号 |
|---|---|---|---|
| P0-1 | **无版本化安全网**：无 git/CI/快照，任何自修都可能不可逆 | `git init` + 首次 commit + tag（`baseline-<date>`）+ 补全 `.gitignore` + `scripts/checkpoint.sh`（改前 tar 源码） | `git status` 干净；能 revert 一次坏改动；`git diff` 可产出 patch |
| P0-2 | **无人值守被 approval 卡死**：自动化 turn 默认 permission=workspace，`bash/write/edit` 均需人工，且无 turn 超时 | `internal/runtime/approval.go` 加 policy/timeout；`internal/app/services.go` 为 self-evolution 专用 session 提供 `full` 权限或 `auto_review` 策略（把 `plugin.auto_review` 从 UI stub 变实现）；自动化 ctx 加 deadline | headless/automation 场景下 agent 能改文件并跑 `go test`，无人工点击；卡住会超时失败而非挂死 |
| P0-3 | **无 headless/CLI 入口**：只有 GUI，无 `--prompt/--json`，无法脚本化对话/自测 | 复用 `BuildServicesWith`（`services.go:61`）+ `registerAllTools`（`:228`）加 `cmd/zeokeel run --headless --workspace --prompt --json-events`；产品级无显示冒烟脚本 | `env -u DISPLAY zeokeel run --prompt ... --json` 退出码 0 且事件 JSON 可解析 |
| P0-4 | **闸门不可作为 scoreboard**：纯文本、无 JSON、`-count=1` 缺失、check-nocgo 污染仓库 | `scripts/verify.sh` + `make verify`：`CGO_ENABLED=0 go test -count=1 -json ./...` 汇总到 `.evolve/results/<ts>.json`（含 pixel 百分比/耗时/失败测试）；check-nocgo 用 `-o /tmp/...`；ARTIFACTS/GOCACHE 固定到 `.cache` | 一条命令产出机器可读 gate 报告；重复执行不会 `(cached)` 假绿；仓库无新增二进制 |
| P0-5 | **自进化 run 无记录**：automation 只有 last_run/next_run，失败不可归因 | `store` 增 `automation_runs` 表（id/session/status/duration/error/event_seq）+ `internal/app` fire 时落记录 + `.evolve/backlog.md` 记录"发现→修复→验证" | 触发一次定时任务后可从 DB 查询成功/失败/耗时；loop 有可读的 backlog/结论 |

### P1（闭环可用性/质量，1–2 周内）

| ID | 缺口 | 落点建议 |
|---|---|---|
| P1-1 | 无**产品级 headless e2e**（create→submit→tool→approval→idle→reopen replay） | `internal/app/e2e_headless_test.go`（fake provider + t.TempDir store + `BuildServicesWith`）；纳入 `make verify` |
| P1-2 | JSONL transcript / `tool_calls` 表是死代码，且 PLAN 文档说它们是真相 | 二选一：接线 `store.AppendTranscript`（`session/events.go` 写事件时镜像）或删除并更新 `docs/PLAN.md:775`、`store/event.go:15` 注释 |
| P1-3 | 仓库**无 AGENTS.md/skills**，agent 不知道"本仓怎么构建/怎么测/红线" | 新增根 `AGENTS.md`（先读 docs、`make check`、禁止改 baseline、P0 红线）+ `.zeokeel/skills/{audit,fix,verify,review}/SKILL.md`（自审清单/命令） |
| P1-4 | 提示词文本不可运行时改 | 复用 AGENTS.md 已足够起步；若需改系统段：增加 `prompt.extra` setting 或 `zeokeel prompt dump` 子命令，避免改 registry 源码 |
| P1-5 | 像素预算余量过薄（session/narrow 0.53pp），且日志首个百分比是 raw 误标 "diff" | 调阈值或改 SSIM/文本区掩码（`baseline_dark_test.go:67-100`；`screens_test.go:72` 修日志字段） |
| P1-6 | 无 CI | `.github/workflows/verify.yml`：linux 全量 `make verify`（Render 无头，不需 Xvfb）+ darwin/windows 交叉编译矩阵 + nightly `e2e`（secret）+ artifacts 上传 |
| P1-7 | 覆盖/契约缺口：session 56.3%、app 51.6%、cmd 0% | 补 controller rebuild/取消/超时/automation 端到端；`cmd/zeokeel` 加 `run` 后即可被 e2e 覆盖 |
| P1-8 | 无性能预算门禁（PLAN §10.3） | `internal/app` 启动 benchmark + `internal/shell` 首帧/滚动 bench；verify 中按阈值断言 |
| P1-9 | 文档漂移：baseline `VERSION/geometry.json/tokens.json` 缺失；`pixel-gate.sh/lint-style-literals.sh/cross-build.sh` 不存在；README 仍写"尚未开始编码" | 补 `baseline/VERSION` + 明确 token 来源；README/PLAN 改为"已实现 TestScreensBaseline + check-nocgo"，删除幽灵脚本引用 |

### P2（体验/长期）

| ID | 缺口 | 落点建议 |
|---|---|---|
| P2-1 | 观测性只有 stderr 日志 | 引入 `slog` JSON + `DataDir()/logs/` 轮转 + 计数器（turn/tool/approval 失败数）+ 失败事件上报 |
| P2-2 | automation cron 子集/30s 粒度/无并发上限 | 文档化限制；或扩展 `automation/cron.go`；scheduler 加 in-flight 计数 |
| P2-3 | `-race` 不可用（C1 禁 cgo） | CI 夜间 job 用 `CGO_ENABLED=1 go test -race ./internal/runtime ./internal/session ./internal/automation`（与发布构建分离） |
| P2-4 | 26 GB 仓库（`.cache` 24.7 GB） | GOCACHE/GOMODCACHE 移出仓库或定期 `go clean -cache`；`dist/` 用脚本构建；`.gitignore` 补齐 |
| P2-5 | 测试卫生 | 探索型 `TestProbeLiveScreens` 进默认套件（会 dump 全 UI 文本）；`-short` 无效；像素测试污染 `artifacts/`；可归入 `-short`/tag |
| P2-6 | `inspect` 被 developer_tools 默认关闭 | 自评审需要时默认开，或独立 self-review 工具；`plugin_details` 已显示其"实验" |
| P2-7 | 无 panic 隔离 | `runtime/agent.go` run goroutine + `session/runTurn` 加 recover → 转 `state.Error` |
| P2-8 | approval 规则仅内存 | `AllowAlways` 持久化到 settings（如 `approval.always_allow`），重启不丢 |

---

## 附录 A：可直接复用的"自进化"资产

1. **服务装配接缝**：`app.BuildServicesWith`（`services.go:61`）——store/projector/broker/controller 一次装配，返回 cleanup；测试里已被用（`app/services_test.go`）。做 headless CLI/e2e 直接复用。
2. **生产工具注册**：`registerAllTools`（`services.go:228`）——插件开关/权限/输出上限/技能/MCP 全部走这里；self-evolution 场景可通过 settings 精确裁剪。
3. **确定性调度**：`automation.Scheduler.Tick(now)`（`scheduler.go:135`）+ 注入时钟（tests 已用 fakeStore/fake clock）——适合做"仿真一天"的自测。
4. **事件契约**：`internal/state` 的 8 类事件 + `session/events.go` 的 JSON 编码——天然的机器可读 trace/scoreboard 基础。
5. **自省工具**：`session_query`（搜索/读取/追踪历史）+ `inspect`（配置/插件/会话快照，自动脱敏 secret）。
6. **结构化结论**：`report` 工具（16KB、JSON、无副作用）——适合作为"每轮自评审结论"的载体，再由外层落盘。
7. **迭代原语**：`ralph`（轮次/完成标记/depth 守卫）+ `job_*`（跑 gate 类长命令）+ `terminal_*`（PTY 交互）。**注意：`job_start` 不在默认 approval patterns，但 `bash` 在**——自进化脚本应优先用 `job_*` 跑 gate，避免审批阻塞（前提是 shell 插件开启且权限允许注册）。

## 附录 B：复现命令（本次审计）

```sh
export PATH=/usr/local/go/bin:$PATH
cd /tmp/audit/snapshot            # 220 文件快照，manifest /tmp/audit/source.sha256
export GOCACHE=/data/mygo/.cache/go-build GOMODCACHE=/data/mygo/.cache/gomod

time make check                                     # 80.4s，全绿
time go build ./...                                 # 4.1s 暖（冷 17.9s）
time CGO_ENABLED=0 go test -count=1 ./...           # 21.7s
time CGO_ENABLED=0 go test -cover -count=1 ./...    # 44.6s（覆盖率见 §2）
time ARTIFACTS=/tmp/audit/art go test -count=1 -run '^TestScreensBaseline$' -v ./internal/shell     # 1.38s
time ARTIFACTS=/tmp/audit/art go test -count=1 -run '^TestScreensDarkNarrow$' -v ./internal/shell   # 1.71s
sh scripts/check-nocgo.sh && file zeokeel           # 看到 Mach-O arm64 即为回写缺陷
CGO_ENABLED=0 go test -race ./internal/automation   # 报错：-race requires cgo
```

## 附录 C：审计方法与限制

- 只读审计：除本文件外未修改 `/data/mygo` 任何源码/脚本/配置；测试产物通过 `ARTIFACTS=/tmp/...` 重定向。
- 未运行 `e2e-model.sh/e2e-tools.sh`（按任务要求；需要真实模型/凭据），其行为结论来自脚本/测试源码。
- 未修改 live 树；gate 数字来自 18:57 快照。live 树在 19:02 仍有 2 个 shell 文件被队友修改，若引用行号请以快照或当前 source 的同一符号位置为准。
- 未审计第三方依赖内部（eino/mygo/modernc sqlite），未做安全漏洞扫描；`scripts/cdp.mjs`（DSH 基线采集工具）会读取 `~/.dsh/.credentials.yaml`，属于 DSH 侧工具，不在 ZeoKeel 产品运行时路径。

---

## 附录 D：交稿后新增资产（Lead/队友 19:01–19:05 落盘；仅作衔接，不改变前文的审计基线）

本节写于 01-audit.md 首版落盘之后，用于把"审计结论"与 Lead 同日新建的自进化资产对齐：

1. **`docs/EVOLUTION.md`（v0.1，29 KB，19:02）**：自进化闭环设计提案，明确 L1 自体检 / L2 自修复 / L3 自评审 / L4 自学习、非目标（不做在线自改二进制、不做 LLM judge 单独发布门禁、不自动 merge）、以及"研发期外部闭环优先于产品内置 `zeokeel evolve`"的路线。文中已引用本审计（`.evolve/research/01-audit.md`）与 02/03 号调研。
2. **`scripts/evolve/gates.sh`（19:02）+ `.evolve/state/`**：统一门禁入口（Signal collector），已实际跑过：
   - 修复了本报告 P0-4 的大部分诉求：统一入口、`go test -count=1`（消除测试缓存假绿）、像素 `ARTIFACTS=/tmp`（不再污染 `artifacts/`）、失败自动重跑并标记 `flaky`、输出 `.evolve/state/latest.json`（schema `zeokeel.evolve.gates/v1`）+ `latest.md` + 分步日志。
   - 实测 full run（`20261104-190321`）：19:03:21→19:04:35（**74s**），`fmt/vet/build/test/pixel-light/pixel-dark/nocgo` 全 pass，其中 **test 21s、pixel-light 3s、pixel-dark 3s、nocgo 43.8s**——与本报告 §1 的分项数据一致，可互相印证。
   - 仍**未**覆盖本报告指出的两点：(a) 默认 `--quick` 不含 `scripts/check-nocgo.sh`，即 C1 硬门禁（no-cgo + 四目标）不在日常入口；(b) `--full` 仍调用未加 `-o` 的 `check-nocgo.sh`，实测跑完后仓库根 `zeokeel` 仍是 **Mach-O arm64**（回写缺陷未修）。另：gates.sh 用 `python3` 汇总 JSON（本机 3.12.3 可用），跨平台 CI 需确认该依赖。
3. **状态更新**：本报告 §7 的 **P0-4 可视为"部分完成（gates.sh v0）"**；仍建议补 `-o /tmp` 隔离、quick 模式包含 no-cgo build、JSON 中记录像素百分比（当前步骤只有 pass/fail + tail）。**P0-1（git/CI/快照）仍未落实**：`git rev-parse --show-toplevel` 仍报 `fatal: not a git repository`。
4. **`.evolve/research/02-external.md`、`03-product-review.md`** 已由队友落盘（19:01/18:56），与本报告构成"现状 / 外部模式 / 产品评审"三份输入。
