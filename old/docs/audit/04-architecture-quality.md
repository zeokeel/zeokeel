# 架构与工程质量审计报告（ZeoKeel）

> 审计者：arch-auditor · 日期：2026-11-05 · 基线：HEAD `27d5d5f`（审计开工时共享简报记 `7eebe47`；审计期间出现并发提交 `27d5d5f build: embed the ZeoKeel icon`，本报告所有行号以 `27d5d5f` 为准；审计期间同一并发进程还做了 `20f1399 fix: keep a failed turn's error inside that turn`（改 `shell/session_live.go`、`state/snapshot.go` + 新测试，收尾时工作树又在改 `app/services.go`、`provider/openai/*`）——**这些并发改动不在本报告结论范围内**，引用 `snapshot.go` 的行号在 `20f1399` 上约有 +10 行偏移，符号名不受影响）
> 方法：全仓代码阅读 + `go/ast` 静态探针（大函数/重复体/TODO/死代码/字面量）+ `go list` 依赖图 + 定向 `go test -cover` / **`CGO_ENABLED=1 go test -race`（仅审计用；项目门禁仍为 CGO_ENABLED=0）** + Xvfb 运行 `dist/` 二进制 + 存储写放大微探针 + 文档交叉核对（5 份主文档、14 篇 ADR）
> 限制：无 Windows/macOS 真机（路径/编码/pwsh/安装包只能静态核对）；未跑真实模型 E2E；长会话 UI 冻结只做了静态+单点写成本，未做端到端帧时间；`-race` 只覆盖 state/session/shell/app 中前三个（未跑 app/prompt/mcp）

---

## 0. 结论摘要

ZeoKeel 的**核心基础件（store/state/build 脚本）质量高于同类早期项目**：SQLite 打开路径把 WAL/foreign_keys/busy_timeout 全部显式设置并**回读校验**，JSONL/目录删除有完整清理，`-race` 对 state/session 干净；ADR 有状态与取代链，测试规模 1122 个函数。真正的不合理集中在三处：**(a) 项目自称的门禁/真源没有落地机制**（无自动化 CI、无 BUGS.md、文档数字与事实多处相反）；**(b) UI 层与数据/控制层没有分层**（`internal/shell` 41.4k 行=36%、直接 import 10 个内部包、225 处直调 Controller，并在 UI 线程同步读 DB）；**(c) 两套快照折叠 + 逐 token 双 fsync + 无保留策略** 让 1808 行的 state 包一半闲置、events 表按 token 膨胀。

| ID | 严重度 | 标题 | NEW/KNOWN | 修复量 |
| --- | --- | --- | --- | --- |
| AR-01 | P1 | 发布版零崩溃诊断：无 recover、无日志文件、Windows GUI 丢弃 stderr | NEW | S |
| AR-02 | P1 | CI 只是本地脚本，无 .github/，race/coverage 覆盖不完整且可被 flaky 重跑掩盖 | NEW | S/M |
| AR-03 | P1 | 发布链路缺失：仅 Windows zip，无签名/公证/自动更新/版本注入 | KNOWN | L |
| AR-04 | P2 | `internal/shell` God package：41.4k 行、19k 生产行、UI 直连 store/session | NEW | L |
| AR-05 | P2 | 两套快照折叠并存：Projector 与实际 UI 用的 Controller views 重复且可能发散 | NEW | M |
| AR-06 | P2 | 逐 token 持久化：每事件 1 次 DB commit + 1 次 JSONL fsync（实测 0.675ms），无保留策略 | NEW | M |
| AR-07 | P2 | 排序靠"事件回放重建 Seq"：messages 表无序号列，消费方缺失即退化为列表序 | NEW | M |
| AR-08 | P2 | UI 线程同步执行 OpenSession/ListSessions 等 DB 操作，违反自身线程纪律 | NEW | M |
| AR-09 | P2 | Schema 演进冻结在 v1：只靠 `CREATE IF NOT EXISTS` 追加、非事务、无备份/降级路径 | NEW | M |
| AR-10 | P2 | 依赖风险：eino alpha / mygo 0.7.4 / Go1.27 下 sonic 回退 JSON（每次启动告警） | KNOWN | M |
| AR-11 | P2 | 单实例锁=硬编码回环端口 49731，不同数据目录/端口占用即误报"已在运行" | NEW | S |
| AR-12 | P2 | 文档体系 sprawl：39 篇/8265 行、状态真源三处重叠、历史流水与现状混编 | NEW | M |
| AR-13 | P2 | known-limitations 与代码/其它文档相反：说 plan-mode 增强"未做"，实际已实现 | NEW | S |
| AR-14 | P2 | 真源反转：PLAN 称 JSONL 是"唯一真相"，代码以 SQLite 为准且 JSONL 生产零读取、错误被吞 | NEW | S/M |
| AR-15 | P2 | FUNCTIONAL-PARITY 说 Goal 无自动多轮驱动，实际 `GoalRoundDriver` 已实现并接线 | NEW | S |
| AR-16 | P2 | `docs/BUGS.md` 缺失，但 QUALITY.md 规定已知缺陷只能存在于此；真实 backlog 在 gitignored `.cache/` | NEW | S |
| AR-17 | P2 | 测试盲区：store 62.8%/design 32.6%/cmd 0%，无 fuzz/属性测试，race 不含 shell/app/prompt/mcp | NEW | M |
| AR-18 | P2 | 主窗口默认 1600×1000 不适配小屏；部分可见的旧尺寸原样恢复（超出屏幕） | NEW | S |
| AR-19 | P3 | 文档数字不可复核：包数 17/18/19/20 并存（实际 20）、i18n 键 350 vs 421、无 commit/命令锚点 | NEW | S |
| AR-20 | P3 | 超大函数/文件：65 个函数 >100 行；controller.go 3858 行、services.go 1445 行 | NEW | M |
| AR-21 | P3 | 重复/半死代码：`scrubEnv` 双份、automations DDL 双份、ReadTranscript 生产零调用 | NEW | S |
| AR-22 | P3 | 每 token O(消息数) 快照拷贝（整条 message slice + 全量 string 拼接） | NEW | M |

> 汇总：P1×3、P2×15、P3×4（共 22 条独立发现）；NEW 20 条、KNOWN 2 条（AR-03/AR-10；另 §4 复核 9 项旧结论）。

---

## 1. 范围与方法（实际执行）

| 项 | 内容 |
| --- | --- |
| 基线 | `git rev-parse HEAD`=`27d5d5f`（21:22 前为 `7eebe47`）；`git log --oneline -2` 显示审计开始后并发提交了 Windows 图标资源（未改 Go 逻辑）；21 个 commit、1 个 tag（`pre-native-ui`）、489 个跟踪文件。21:03-21:31 期间另一进程未提交地修改了 3 个已跟踪 Go 文件（`shell/session_live.go`、`state/snapshot.go`、`state/snapshot_test.go`）并新增 1 个测试，21:37 其提交为 `20f1399`，随后工作树又改 `app/services.go` + `provider/openai/*`；本报告以 `27d5d5f` 提交内容取证（`20f1399` 只影响错误归位渲染与 `ErrorInfo.Seq`，不改变任何结论），未提交改动只用于判断"并发开发/审计无冻结基线"这一流程问题 |
| 规模 | 115,135 行 Go（不含 `.probe/`、`.cache/`）；19 个 `internal` 包 + `cmd/zeokeel`（`go list ./...` = 20）；`internal/shell` 41,420 行/120 文件（生产 19,052 + 测试 22,368）；`tools` 17,880、`session` 13,072、`runtime` 12,710、`app` 9,522、`store` 6,659；测试文件 211/测试函数 1,122 |
| 静态探针 | `go run .cache/audit/arch/analyze.go .` → 大函数/大文件/重复函数体/TODO 分布/候选死代码（输出 `static-analysis.txt`） |
| 依赖图 | `go list -f '{{.ImportPath}} -> {{join .Imports " "}}' ./internal/...`（`imports.txt`）；内部依赖边 43 条，`shell` 出度 10、`app` 13、`tools` 8、`session` 4；`runtime` 只依赖 `state`，`state` 无内部依赖（这一点是好的） |
| 测试 | `go test -count=1 -cover ./internal/...` 全绿（`coverage.txt`）；`CGO_ENABLED=1 go test -race -count=1 ./internal/state ./internal/session ./internal/shell` **3/3 PASS**：state 1.0s、session 200.2s、shell 518.8s（`race-*.log`；项目 CI 只用 0-cgo 且 race 列表不含 shell） |
| 运行 | `xvfb-run` 跑 `dist/zeokeel-linux-amd64`（隔离 `ZEOKEEL_DATA_DIR=/tmp/zk-audit-arch`）：窗口映射 ~112ms、RSS 167MB、103 线程、17 fd、数据目录 192KB（`startup-rss.txt`）；进程日志仅 stderr |
| 窗口几何 | Xvfb 1280×800 三步探针：首启默认 / 完全离屏恢复 / 部分可见超尺寸恢复（`window-bounds.txt`） |
| 写成本 | 探针 `.cache/audit/arch/probe_write_cost.go`：`AppendEvent`+`AppendTranscript` 300 次 = 202.4ms，**0.675ms/事件**；`ListEvents` 300 行 4.8ms（`write-cost.txt`） |
| 体积 | `go build -trimpath -ldflags "-s -w"` 同源构建：49,534,307 → **33,751,303 字节（-31.8%）** |
| 文档 | 通读 README/STATUS/PLAN/FUNCTIONAL-PARITY/known-limitations/QUALITY/EVOLUTION/14 篇 ADR；grep 抽查交叉矛盾（`docs-evidence.txt` 记录了每条复核命令与输出） |

---

## 2. 详细发现

### [AR-01] 发布版零崩溃诊断：无 recover、无日志文件、Windows GUI 丢弃 stderr · P1 · NEW

- 现象：全仓 `recover()` 出现 0 次（`grep -rn "recover()" internal cmd` 无输出）；进程日志只有标准库 `log.Printf` 到 stderr（如 `app.go:258`、`services.go:1442`），无 `log.SetOutput`/文件日志/轮转；`main.go:9-17` 用 `log.Fatal`。Windows GUI 产物用 `-H windowsgui` 构建（`scripts/build.sh:92`），该子系统下 stderr 没有控制台可写。
- 证据：`cmd/zeokeel/main.go:9-17`；`scripts/build.sh:92`；`grep "#4" .cache/audit/arch/docs-evidence.txt`；运行探针只得到 stderr 日志（`startup-rss.txt`）。
- 影响：任一 goroutine（projector/回合 drain/UI 回调）panic 都会静默终止整个应用；Windows 用户看到窗口消失、无任何可提交的日志/崩溃记录，发布后无法定位问题。这正是"全功能桌面 Agent"最难维护的一类缺陷。
- 复现/验证：`grep -rn "recover()" internal cmd`（空）；`grep -rn "SetOutput" internal cmd`（空）；查看 `build.sh` windows 目标。
- 建议修复（S，1-2d）：`main` 里 `defer recover` + 写 `DataDir()/logs/crash-*.txt`；`log.SetOutput(io.MultiWriter(stderr, 文件))` + 简单按天轮转；Windows GUI 构建必须启用文件日志；加 `-ldflags "-X main.version=… -X main.commit=…"` 并在 About/日志打印。

### [AR-02] CI 只是本地脚本：无 .github/，race/coverage 覆盖不完整且可被 flaky 重跑掩盖 · P1 · NEW

- 现象：仓库无任何托管 CI 配置（`.github/`、`.gitlab-ci.yml`、`Jenkinsfile` 均不存在，`.git/hooks` 只有 sample），`scripts/ci.sh` 必须人工执行；race 仅覆盖 6/20 包（state/store/session/agentteam/tools/runtime），**最大包 shell（36% 代码、426 个测试函数）、app、prompt、mcp 被排除**；coverage 步骤只 `sort|tail -20` 打印（`ci.sh:42-43`），不设阈值也不失败；视觉回归明确"CI 未强制"（`known-limitations.md:47`）；`scripts/evolve/gates.sh` 对测试失败自动重跑最多 3 次并记 `flaky`（`gates.sh:12,84`，注释自己写"flaky 不是通过"）。
- 证据：`scripts/ci.sh:42-57`；`scripts/evolve/gates.sh:12,75,84,143-145`；`docs/QUALITY.md:14`（"任何改动不得降低覆盖率与门禁标准"）与 ci.sh 不符；README:16（C4"功能与交互验收进 CI"）。
- 影响：21 个 commit 全部无 CI 记录；回归只能靠事后 `ci.sh`。race 漏掉 shell/app 意味着 UI 状态竞争类问题在门禁里永远看不到；flaky 重跑把不确定性变成"绿"。本审计期间就出现"另一个进程在验证/审计同时修改已跟踪代码"（见 §1），说明仓库没有"验证以某个 commit 快照为准"的纪律——报告、门禁、真源文档都可能引用不同版本。
- 复现/验证：`ls -d .github`；`grep -n "race" scripts/ci.sh`；对同一提交手工跑 `ci.sh` 可复现。
- 建议修复（S/M，1-2d）：加 `.github/workflows/ci.yml`（push/PR 跑 gofmt/vet/build/test/check-nocgo/race）；race 至少加入 app（shell 实测 518.8s，建议 PR 上 `-timeout 20m` 或 nightly，见 §5）；coverage 记录基线并在 CI 比较（低于基线 fail）；删除 flaky 重跑或只允许 1 次且必须 fail 报告。

### [AR-03] 发布链路缺失：仅 Windows zip，无签名/公证/自动更新/三平台安装包，且版本号未注入 · P1 · KNOWN

- 现象：`dist/` 只有 Linux 单文件、Windows GUI+console+zip；`packaging/` 只有 Windows README；无 deb/rpm/AppImage/dmg/tar、无代码签名/公证、无自动更新代码（`grep "auto.?update|签名" internal cmd` 仅命中无关注释）；版本不是构建信息：About 硬编码 `"about.version": {"version 0.1.0"}`（`i18n.go:45`），而 README/STATUS 把当前交付叫 **v1.1**（`STATUS.md:58`、`README.md:67`），git 无 v1.x tag；build.sh 无 `-ldflags -X`（`build.sh:64,92,94` 只有 `-trimpath`/`-H windowsgui`）。
- 证据：`ls dist/ packaging/`；`grep -n "ldflags" scripts/build.sh`；`internal/shell/i18n.go:45`；`git tag`；`docs/FUNCTIONAL-PARITY.md:28`（"仅 Windows zip，无签名/更新 ❌ 缺失"）。
- 影响：发布无法追版本/无法自动更新/无法证明产物来源；用户报告问题时不知道对应哪个构建。**产物也未做最小化**：同源 `-ldflags "-s -w"` 构建 33,751,303 字节 vs `dist/` 49,534,307 字节（-31.8%，`scripts/build.sh` 只用 `-trimpath`，调试符号/DWARF 全随包发布，Windows exe 50.3MB）。注意：**打包/签名/更新缺失此前已被 parity 文档记录**，本条复核状态并补"版本注入 + 体积"两个未被记录的点。
- 复现/验证：`git tag`（只有 `pre-native-ui`）；`grep -rn "version" internal/shell/i18n.go`；`ls dist/`。
- 建议修复（L，分两步）：M7 先做版本注入+About/Settings 显示 commit+构建时间、Linux tar/AppImage 与 macOS .app/.dmg 打包脚本（S/M）；签名/公证/自动更新（L，需真机与证书）。

### [AR-04] `internal/shell` God package：41.4k 行、UI 直连 10 个内部包、225 处直调 Controller · P2 · NEW

- 现象：`internal/shell` 41,420 行（占全仓 Go 36%）/120 文件，其中生产 19,052 行、测试 22,368 行；单文件 `workspaces.go` 1,394、`right_sidebar.go` 1,232、`composer_slash.go` 989、`settings_ui.go` 975、`plugin_details.go` 857。它同时是：UI 渲染层 + 交互/命令层 + 会话/工作区业务动作层 + 状态缓存（`App.snap`）。`imports.txt` 显示它直接 import `automation brand design icons runtime session state store tools winproc`（10 个内部包）；对 Controller 的直接调用 225 处（`grep -c "a\.Ctrl\."`），既有提交/停止，也有 `OpenSession`/`ListSessions` 这类 DB 动作。PLAN §4.2 声称"依赖只能向下 ui → view → runtime → {tools, provider, store}"（`PLAN.md:277-284`），实际 shell→store/session 是横穿。
- 证据：`.cache/audit/arch/static-analysis.txt`（大文件表）；`internal/shell/workspaces.go:1-1394`；`internal/shell/menus.go:344-347`；`PLAN.md:277-284`；`docs-evidence.txt #14`。
- 影响：任何 UI 改动都有跨层回归风险；无法单独测试"动作层"；新增屏/新入口倾向继续堆进 shell（`app` 只有 9.5k，本该是唯一组装层）。这是当前最大结构性成本项。
- 复现/验证：`wc -l internal/shell/*.go | sort -rn | head`；`grep -rn "a\.Ctrl\." internal/shell | grep -v _test | wc -l`。
- 建议修复（L，3-5d）：拆 `shell/kit`（布局/组件/渲染原语）、`shell/screens`（视图）、`shell/actions`（命令→Ctrl 调用，可单测），把 Controller/Store 依赖收敛到 actions 一层；新代码禁止在 view 内直接 `a.Ctrl.*`。

### [AR-05] 两套快照折叠并存：Projector 与实际 UI 使用的 Controller views 重复且可能发散 · P2 · NEW

- 现象：`state.Projector` 的设计声明是"Broker 的**唯一**消费者，在独立 goroutine 把事件折叠成不可变快照"（`projector.go:36-39`，`PLAN.md:323`"投影层是唯一消费者"）。但真正驱动 UI 的是 `session.Controller` 自己维护的 `views map[string]*Snapshot` 和 `applyLocked()` 里的第二次折叠（`controller.go:1050-1057` 调 `state.Apply`），`notify` 发出去的就是这份；`Controller.Snapshot()` 先返回 views，**只有没有当前会话时才回退到 projector**（`controller.go:1661-1673`）。Projector 仍在每个事件上折叠、分配快照、占 CPU/内存。另外 Projector 的快照是**会话无关**的：Token/Reasoning 带 SessionID 时"最后一个事件赢"（`snapshot.go:201-202`），多会话并发时它表达不了不同会话，这解释了 controller 为何另起一套。
- 证据：`session/controller.go:299`（创建 projector）、`controller.go:1050-1057`、`controller.go:1661-1673`、`controller.go:904-907`（controller 自己给用户消息分配 Seq）；`internal/state/projector.go:36-39,77,140-158`；`internal/state/snapshot.go:201-202`；`internal/app/services.go:112`；`internal/app/app.go:202-206`（UI 用 OnUpdate 传入的 Controller 快照）。
- 影响：同一条事件被折叠两次（逐 token 双倍不可变拷贝）；两套 Seq 轴来源不同，任何"用 projector 读状态"的新代码会拿到与 UI 不一致的会话；Broker 的 drop 计数（`broker.go:99-103`）生产零调用，若真启用 projector 也会静默丢事件。
- 复现/验证：`grep -rn "NewProjector" internal`；`grep -n "c.views\[" internal/session/controller.go`；对同一会话比较 `Controller.Snapshot()` 与 projector 快照（仅测试里可注入 projector 观察）。
- 建议修复（M，2-3d）：二选一——(a) 删 projector/broker 事件流，Controller 直接发布快照；(b) 把 projector 改为 per-session（key 化）并让 Controller 只做动作、快照统一由 projector 产出。二选一后再补 drop 告警（`Dropped()` 接入日志/指标）。

### [AR-06] 逐 token 持久化：每事件 1 次 DB commit + 1 次 JSONL fsync，无保留策略 · P2 · NEW

- 现象：`forward()` 对**每个**事件（含每个 Token 增量）调用 `appendEvent`（`controller.go:1028`），内部 `store.AppendEvent` 开事务、`SELECT MAX(seq)+1`、INSERT、Commit（`store/event.go:31-100`），随后 `AppendTranscript` 追加一行并 `f.Sync()`（`store/transcript.go:98`）。探针实测 **0.675ms/事件**（本机）。用户实际导出的会话样本为 2 个回合 1,593 个事件（`.cache/team/bug-backlog.md:3`，约 1 事件/token）；events 表因此按 token 数增长，`buildSnapshot` 重开会话时全量 `ListEvents` 回放（`session/history.go:70-160`）。
- 证据：`.cache/audit/arch/write-cost.txt`；`internal/session/events.go:123-145`；`internal/session/controller.go:1028`；`internal/store/transcript.go:88-112`；`internal/store/event.go`（AppendEvent）。
- 影响：长回答/长会话下，回合 goroutine 每 token 被 fsync 拖慢（Windows/Defender 环境更差），events 表与 JSONL 无上限增长（无 retention/VACUUM：`grep -rn "VACUUM\|retention\|prune" internal/store` 无实现），重开会话 O(事件数) 且内存全载。这是"可扩展性"的结构性税。
- 复现/验证：`go run .cache/audit/arch/probe_write_cost.go`（重建 300 事件）；`grep -n "AppendEvent\|AppendTranscript" internal/session/events.go`。
- 建议修复（M，2-3d）：Token/Reasoning 不再逐条落库（消息表已有最终文本），事件按 100-200ms 或按消息聚合批量提交；JSONL 改为导出时生成或批量 append；对 events 加保留策略（保留最近 N 回合 / 压缩为聚合事件）+ 定期 `PRAGMA incremental_vacuum`。

### [AR-07] 排序靠"事件回放重建 Seq"：messages 表无序号列，缺失即退化为列表序 · P2 · NEW

- 现象：`state.Message.Seq/LastSeq` 注释明说它们来自"快照 Seq 轴"，重开时靠回放 `events` 里 `message`/`token`/`reasoning` 事件把 messageID 映射回序号（`snapshot.go:59-73`；`session/history.go:20-26,96-105`；`shell/transcript_order.go:23-37` 明确"Seq 为 0 的旧快照退化为列表顺序"）。messages 表 schema 没有 seq/ordinal 列（`store/schema.go:36-44`），用户消息的 Seq 是 controller 自己预测的 `next.Seq`（`controller.go:904-907`）。
- 证据：`internal/state/snapshot.go:59-73`；`internal/session/history.go:96-105`；`internal/store/schema.go:36-44`；`internal/shell/transcript_order.go:23-37,62-90`。
- 影响：显示顺序的正确性依赖"events 表完整 + 回放实现正确"两个隐含前提；任何不经过回放构造快照的路径（checkpoint 恢复、测试 fixture、未来导出/搜索）都会拿到 Seq=0 并**静默改变顺序**；同一 turn 的"过程块在回答上方"这种语义用两个序号字段硬编码，后续维护成本高。**审计期间刚提交的 `20f1399` 又给 `state.ErrorInfo` 加了 `Seq` 字段**（把失败错误归到发生它的回合），即瞬时快照 Seq 轴在 2 天内新增了第 4 个消费方——该抽象还在扩散，越晚改越贵。
- 复现/验证：`grep -n "Seq" internal/session/history.go`；构造 messages 表有数据但 events 缺失的会话（可删 events 行）→ 快照 Seq 全 0，UI 走列表顺序。
- 建议修复（M）：messages 表增加 `turn_id + ordinal`（或 `seq`）列并在写入时分配；排序只读该列；events 只作为轨迹/审计，不再承担排序职责。

### [AR-08] UI 线程同步执行 OpenSession/ListSessions 等 DB 操作，违反自身线程纪律 · P2 · NEW

- 现象：`PLAN.md:271-273` 规定"UI 层绝不阻塞等待 DB/模型/子进程（异步命令队列 + 进行中状态）"。实际 shell 的点击/快捷键处理器直接同步调用：`shortcuts.go:62-66,107`（新建+`OpenSession`）、`workspaces.go:671-706`、`menus.go:344-347`（`ListSessions`+`OpenSession`）、`home.go:360-365`、`composer_menus.go:451`、`session_live.go:754`。`OpenSession` 首次打开会同步 `ListMessages + ListAttachments + FeedbackByMessage + ListEvents` 并回放（`session/history.go:30-160`），且 store 只有 1 条连接（`store.go:88`）。
- 证据：`internal/shell/shortcuts.go:62-66,107`；`internal/shell/workspaces.go:671-706`；`internal/session/controller.go`（OpenSession）；`internal/store/store.go:85-89`；`PLAN.md:271-273`。
- 影响：大会话/慢磁盘下打开会话会冻结 UI（帧时间无上限）；同一时刻后台写库也串行在单连接上，UI 读要排队。PLAN 风险 R9 的缓解措施"CI 加帧时间断言"未实现（`PLAN.md` R9 vs `scripts/ci.sh` 无帧断言）。
- 复现/验证：静态路径；配合 AR-06 的 0.675ms/事件与 1,593 事件样本可估算百毫秒级冻结；无端到端测量（见 §5）。
- 建议修复（M）：命令队列/`mygo.App.Queue` 或后台 goroutine + "打开中"快照；所有 Ctrl 调用从 view/handler 移入异步动作；补一个慢 store 注入的 shell 测试断言不阻塞帧。

### [AR-09] Schema 演进冻结在 v1：只靠 `CREATE IF NOT EXISTS` 追加、非事务、无备份/降级路径 · P2 · NEW

- 现象：`SchemaVersion = 1`（`store/store.go:33`），后续表/索引靠 `schemaV1Additions` 一串 `CREATE TABLE/INDEX IF NOT EXISTS` 补齐（`schema.go:80-146`），且 `ensureSchemaAdditions` 在每次 `Open` 时**逐条自动提交**执行（`schema.go:203-206`），不在事务里；`automations` 表在 `schemaV1` 与 additions 里**重复定义一次**（`schema.go:67`/`85`）。版本策略是硬拒绝：旧库 `ErrSchemaTooOld` 提示"remove the index or restore a backup"（`schema.go:157-165`）——即**升级到新 schema 的发布会让旧版本用户要么删数据要么无路可走**。
- 证据：`internal/store/schema.go:33,67,80-90,147-170,203-206`；`internal/store/store.go:33`；`docs-evidence.txt #15`。
- 影响：无法安全 rename/alter/drop 列、无法数据回填、无法写 downgrade 守卫；未来任何破坏性 schema 变更都会变成"用户删库"；非事务 additions 中断时状态依赖幂等性而非原子性。对"长期产品"是明确的债务。
- 复现/验证：读 schema.go；`grep -n "SchemaVersion" internal/store`。
- 建议修复（M）：迁移改为有序步骤表 `[]migration{from,to,fn}` + 每步事务 + 迁移前 `VACUUM INTO` 备份文件；additions 合并进 v2 迁移；`schema_version` 写迁移日志。

### [AR-10] 依赖风险：eino alpha / mygo 0.7.4 / Go1.27 下 sonic 回退 JSON · P2 · KNOWN

- 现象：`go.mod` 直接依赖 `github.com/cloudwego/eino v0.10.0-alpha.35`（预发布）、`github.com/egoist/mygo v0.7.4`（UI 框架，<1.0）、`modernc.org/sqlite v1.61.1`；`go 1.27.3` 工具链钉死。实测启动日志**每次**打印 `WARNING: sonic/ast only supports (go1.17~1.26 ...) but your environment is not suitable and will fallback to encoding/json`（`/tmp/zk-arch-app.log`，本轮运行复现）——eino 依赖链的 sonic 在 Go 1.27 下退化到 encoding/json。
- 证据：`go.mod:5-13`；`startup-rss.txt` 对应启动日志第一行；`PLAN.md` R7 已登记该风险并写明缓解（版本钉死/独立升级 PR/封装边界）。
- 影响：运行时 JSON 路径变慢（provider 流式解析每 chunk）；上游 alpha 版本 API 变动风险已被隔离在 runtime/provider 一层（`runtime` 只 import `state`，provider 只 import `runtime`，这是做对的部分），但升级成本仍高；Go 1.27 与 sonic 的兼容告警会污染用户日志。
- 复现/验证：启动 `dist/zeokeel-linux-amd64` 看 stderr 第一行；`go.mod`。
- 建议修复（M）：升级 eino 到 stable 或替换 sonic 依赖（若上游支持）；在 runtime 内统一 JSON 编解码适配层；把工具链版本纳入发布检查单。

### [AR-11] 单实例锁=硬编码回环端口 49731，不同数据目录/端口占用即误报"已在运行" · P2 · NEW

- 现象：`app.acquire()` 用 `net.Listen("tcp", "127.0.0.1:49731")`，失败即 `ErrAlreadyRunning`（`app.go:108-120`）；锁与 `ZEOKEEL_DATA_DIR` 无关、与用户无关。
- 证据：`internal/app/app.go:115-117`；本轮实测：审计用隔离数据目录启动 `dist` 二进制时收到 `ZeoKeel is already running`（当时团队另一成员在跑应用），见 `.cache/audit/arch/startup-rss.txt` 首次尝试与 `/tmp/zk-arch-app.log`；第二次（端口空出）才成功。
- 影响：多用户/多数据目录/多安装（portable 版）无法并存；本机其它程序占用 49731 时应用永久拒绝启动且提示误导。审计团队自身已踩中。
- 复现/验证：`python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",49731))'` 占住端口后启动应用 → "ZeoKeel is already running"。
- 建议修复（S）：锁文件 `DataDir()/instance.lock` + `flock`（Windows 用命名 mutex 或 `CreateFile` 独占）；失败提示带上数据目录；保留端口监听作为可选。

### [AR-12] 文档体系 sprawl：39 篇/8265 行、状态真源三处重叠、历史流水与现状混编 · P2 · NEW

- 现象：`docs/` 39 个 md、8,265 行；`docs/STATUS.md` 423 行自称"唯一状态真源"（`STATUS.md:1`），但内容是 2026-11-04/05 的**时间倒序流水**，M0–M13 历史条目（含已退役像素门禁的截图 diff 表）占大多数（`STATUS.md:118-393`）；README 的"当前状态"（`README.md:50-72`）与 STATUS 重复叙述；`docs/EVOLUTION.md` 436 行是"待拍板"的自进化提案，自带一套 rubric/门禁/backlog 设计；`docs/PLAN.md` 1,050 行同时保留"历史参考（已退役）"章节与现行规格；`docs/research/`（约 2,500 行）被 README 标为"已退役，不再维护"（`README.md:47`）。PLAN §11.3 又要求"更新 docs/STATUS.md（唯一状态真源）"（`PLAN.md:910`）。
- 证据：`wc -l docs/**/*.md`；`STATUS.md:1,5-10,118-393`；`README.md:47,50-72`；`PLAN.md:910`；`EVOLUTION.md:1-20`。
- 影响：读者（含 agent）无法判断哪份是现行口径；已退役内容与现状混排导致"文档说 X、代码是 Y"（AR-13/14/15/18 都是该结构的产物）。维护成本随每次迭代线性上升。
- 复现/验证：`grep -n "唯一" docs/*.md docs/*/*.md`；人工通读 STATUS 前 30 行与末尾。
- 建议修复（M）：STATUS 重写为 ≤100 行"已实现/已验证/未达标/计划"四段，带日期+commit hash；历史流水移 `docs/history/STATUS-journal.md`；README 只链接不重复；EVOLUTION 明确"提案/已废弃"状态并移出主文档区；退役文档加 `> 已退役` 横幅或移档。

### [AR-13] known-limitations 与代码/其它文档相反：说 plan-mode 增强"未做"，实际已实现 · P2 · NEW

- 现象：`docs/design/known-limitations.md:28` 写"**尚未做**：独立 `exit_plan_mode` 工具、审阅卡 Keep planning（带反馈）分支、pending 态徽章"；但代码里 `internal/tools/exit_plan_mode.go:13-38` 就是该工具、`internal/shell/plan_ui.go:31-33,259-262` 实现了 Keep planning 与反馈草稿；`STATUS.md:44`、`README.md:71` 明确写"ADR 0008 §4 四项补齐"已完成。
- 证据：`docs/design/known-limitations.md:28`；`internal/tools/exit_plan_mode.go`；`internal/shell/plan_ui.go:33,262`；`docs/STATUS.md:44`；`README.md:71`。
- 影响：known-limitations 是团队默认的"缺口清单"（brief 也指定先读它），此处把已完成项列为未做，会让后续审计/验收重复排期；同一文件其它数字也在漂移（见 AR-19）。
- 复现/验证：`grep -rn "exit_plan_mode" internal | head`；对照 known-limitations:28 与 STATUS:44。
- 建议修复（S）：删除该段"尚未做"的三项，替换为"已实现（commit/测试）"；给该文件加"最后核对命令"字段。

### [AR-14] 真源反转：PLAN 称 JSONL 是"唯一真相"，代码以 SQLite 为准、JSONL 生产零读取且写入错误被吞 · P2 · NEW

- 现象：`PLAN.md:725` 写 "`session.jsonl`：追加式事件……**唯一真相**；`index.sqlite`……（可重建）"。实际代码：`store/transcript.go:58-63` 注释"JSONL 是事件的持久、人类可读**镜像**，除此之外对 store 不透明"；`session/events.go:137-145` 注释"**SQLite stays the source of truth** for a turn"，并 `_ = store.AppendTranscript(...)` **丢弃错误**；生产代码中 `ReadTranscript` 零调用（只有 `transcript_test.go`/`persistence_test.go` 读）。
- 证据：`docs/PLAN.md:725`；`internal/store/transcript.go:58-63,88-112`；`internal/session/events.go:137-145`；`grep -rn "ReadTranscript" internal | grep -v _test` 只命中定义处。
- 影响：两份文档级"真相"互相矛盾；JSONL 每天被写、每行 fsync，却从不被产品读取（只服务人类/调试），且其失败静默——如果未来有人按 PLAN 写恢复逻辑会得到不一致数据；同时也解释了 AR-06 的写放大为何"看不到收益"。
- 复现/验证：`grep -rn "ReadTranscript" internal`；`sed -n '58,63p' internal/store/transcript.go`；`sed -n '725p' docs/PLAN.md`。
- 建议修复（S/M）：文档二选一并对齐代码；若 SQLite 为准（现状），把 JSONL 定位改为"可选的导出/调试镜像"，允许异步批量写；`AppendTranscript` 错误至少计数/日志，不要 `_ =`。

### [AR-15] FUNCTIONAL-PARITY 说 Goal 无自动多轮驱动，实际 `GoalRoundDriver` 已实现并接线 · P2 · NEW

- 现象：`docs/FUNCTIONAL-PARITY.md:18` "Goal（跨回合推进）……**无自动 round 驱动** ⚠️"，`:38` "Goal 多轮自动推进 ❌ 需手动再次触发"。代码：`internal/session/goal_driver.go` 完整实现（`GoalRoundDriver`、`SetGoalDriver`、`goalChanged`、会话级 round 预算），并在 `internal/app/services.go:123` 接线 `GoalDriver: session.NewGoalRoundDriver()`；`controller.go:1543` 等处在回合结束时驱动下一轮。
- 证据：`docs/FUNCTIONAL-PARITY.md:18,38`；`internal/session/goal_driver.go:1-50`；`internal/app/services.go:121-124`；`internal/session/controller.go:1243,1543`。
- 影响：parity 矩阵是功能验收的活文档（QUALITY.md:13 指定），把已实现能力标为缺失会误导排期；同时它会让"下一轮补 Goal"这种重复工作被排进来。
- 复现/验证：`grep -rn "GoalDriver" internal --include='*.go'`；`go test ./internal/session -run Goal`。
- 建议修复（S）：更新矩阵为 ✅ 并注明测试；`/goal` 命令行是否补是另一个产品决策（known-limitations:30 已单独记录），不要混为一谈。

### [AR-16] `docs/BUGS.md` 缺失，但 QUALITY.md 规定已知缺陷只能存在于此；真实 backlog 在 gitignored `.cache/` · P2 · NEW

- 现象：`docs/QUALITY.md:15`："已知缺陷：**只允许**存在于 `docs/BUGS.md`（带严重级、复现命令、回归测试链接）；不允许口头'已知'。" 仓库不存在 `docs/BUGS.md`（也未跟踪）；实际的缺陷 backlog 是 `.cache/team/bug-backlog.md`（`.gitignore` 忽略 `.cache/`，且该目录随时可能被清理/不进备份）。
- 证据：`ls docs/BUGS.md` → ENOENT；`docs/QUALITY.md:15,46-50`；`.cache/team/bug-backlog.md:1-5`。
- 影响：唯一的"发布前 Critical/Major=0 才可发"判定（QUALITY.md:50）没有可跟踪载体；用户反馈的 bug 状态存在本机未跟踪文件里，换机器/清缓存即丢，与"唯一真源"方法论直接冲突。
- 复现/验证：`ls docs/BUGS.md`；`git ls-files | grep -i bugs`（空）。
- 建议修复（S）：建 `docs/BUGS.md`（模板：ID/严重级/复现命令/回归测试/状态），把 `.cache/team/bug-backlog.md` 中仍开放项迁入并跟踪；关闭项留在历史段。

### [AR-17] 测试盲区：store 62.8%/design 32.6%/cmd 0%，无 fuzz/属性测试，race 不含 shell/app/prompt/mcp · P2 · NEW

- 现象：`go test -cover ./internal/...` 实测：shell 86.2%、state 94.3%、prompt 95.0%、tools 80.9%、runtime 81.0%、app 72.4%、**session 69.9%、store 62.8%、design 32.6%、winenc 67.3%**、`cmd` 无测试（0%）；`QUALITY.md:43-44` 自己承认"属性/模糊：**待补**；并发：部分已有"；e2e 需 `-tags e2e` + 真实 key（`internal/e2e` 仅 1 文件，build tag 隔离）。race 覆盖见 AR-02。
- 证据：`.cache/audit/arch/coverage.txt`（19 行完整输出）；`docs/QUALITY.md:35-44`；`internal/e2e/e2e_test.go:1`。
- 影响：最低覆盖恰好落在最该防数据损坏的 `store`（迁移/事务/删除路径）与视觉令牌 `design`；无 fuzz 意味着路径穿越、cron 解析、JSON args（QUALITY 点名的三类）没有自动化对抗输入；cmd 无测试意味着无参启动/错误路径只能手测。
- 复现/验证：`go test -count=1 -cover ./internal/... | grep coverage`。
- 建议修复（M）：store 补迁移/热备/并发删除的针对性测试到 75%+；为 `tools` 路径解析与 `automation` cron 加 go fuzz（各 1-2 个 target 即可）；CI 记录包级覆盖率基线。

### [AR-18] 主窗口默认 1600×1000 不适配小屏；"部分可见"的旧尺寸会原样恢复（超出屏幕） · P2 · NEW

- 现象：`app.go:182-186` 硬编码窗口 1600×1000（最小 900×600），首次启动（无窗口状态）没有按工作区裁剪的逻辑；窗口状态恢复由 mygo 处理——完全在屏幕外会被 clamp 到主屏工作区并居中（mygo `window_state.go:116-138`），但"部分可见"的判定阈值只有 100×50（`window_state.go:140-150`），此时直接把保存的 X/Y/W/H 原样套用、**不检查是否大于工作区**（`window_state.go:126-129`）。
- 证据：Xvfb 1280×800 三步实测（`.cache/audit/arch/window-bounds.txt`，命令见该文件）：A 首次启动 → **1600×1000 @0,0**（大于屏幕）；B 保存在 5000,5000 的 1600×1000 → 恢复 **1280×800 @0,0**（框架正确 clamp+居中）；C 保存在 (100,80) 的 3000×2000 → **恢复 3000×2000 @100,80**（部分可见即整块保留，远超 1280×800）。
- 影响：1366×768/1440×900 笔记本首次启动即超出工作区（真机 WM 常允许超屏，右/下侧 UI 不可见）；外接大屏后拔线换小屏，窗口按旧尺寸恢复。与 known-limitations §3"窗口间几何联动未做"同源，属桌面生命周期缺陷。
- 复现/验证：`xvfb-run -a -s '-screen 0 1280x800x24' bash -c '<BIN> & sleep 3; for id in $(xdotool search --name ZeoKeel); do xdotool getwindowgeometry $id; done'`；按 `.cache/audit/arch/window-bounds.txt` 的 A/B/C 写 `window.json` 复跑。
- 建议修复（S）：创建窗口前把默认尺寸 clamp 到主屏 WorkArea（mygo 有 `backend().Screen().Displays()`，若未导出则由 app 在保存/恢复时自行裁剪）；恢复旧状态时再按当前工作区裁剪一次，不能只依赖框架的 100×50 阈值；补 headless 断言"restore 后几何 ⊆ WorkArea"。
- 备注：Lead 提出的"window.json 恢复不做屏幕边界校验"经实测**部分不成立**——完全离屏由 mygo 兜底（B 例）；真正缺口是"部分可见即不裁剪"（C 例）与首启默认不裁剪（A 例）。

### [AR-19] 文档数字不可复核：包数 17/18/19/20 并存（实际 20）、i18n 键 350 vs 421、无 commit/命令锚点 · P3 · NEW

- 现象：同一交付在不同文档里包数不同：`README.md:68`"（17 包）"、`STATUS.md:70`"18 包全绿"、`STATUS.md:17/24/33/45`"19 包"、`STATUS.md:55`"20 包"；`go list ./...` 实际 **20**。STATUS:310 说 i18n"350 键"，`internal/shell/i18n.go` 的 `translations` 实际 **421** 条。README 自身也有矛盾：:47 把 `docs/research/` 标为"已退役，不再维护"，:68 却引用 `research/07-final-acceptance.md`、`08-final-recheck.md` 作为验收证据；:72"下一阶段（v1.1）……Trajectory 虚拟化"而 :67 与 STATUS:62-63 已写该项完成。
- 证据：`grep -n "包全绿" README.md docs/STATUS.md`；`awk` 统计 i18n 条目（`docs-evidence.txt #11,#13`）；`README.md:47,67,68,72`。
- 影响："全绿/已验收"这类结论不可复核，文档各自漂移；审计/验收代理无法用文档判断现状（本报告 §4 的 4 条复核全部命中此类问题）。
- 复现/验证：`go list ./... | wc -l`；`grep -c "^\t\"" internal/shell/i18n.go`；对照文档行号。
- 建议修复（S）：文档中的数字一律写成"命令 + 输出 + 日期 + commit"，或直接删数字引用测试输出；README 只保留构建/文档索引，不再复述状态。

### [AR-20] 超大函数/文件：65 个函数 >100 行；controller.go 3858 行、services.go 1445 行 · P3 · NEW

- 现象：AST 探针：3,753 个函数中 **65 个 >100 行**；生产代码最大：`registerAllToolsWithOptions` 228 行/51 条语句（`app/services.go:531`）、`buildAgentWithOptions` 224 行（`services.go:783`）、`buildSnapshot` 191 行（`session/history.go:30`）、`submit` 151 行（`session/controller.go:631`）、`filesBreadcrumb` 151 行（`shell/right_sidebar.go:487`）、`run` 142 行（`runtime/agent.go:658`）、`Invoke`（goal.go:125）/`Invoke`（memory.go:90）。最大文件 `session/controller.go` 3,858 行、`app/services.go` 1,445 行、`shell/workspaces.go` 1,394 行。
- 证据：`.cache/audit/arch/static-analysis.txt`（TOP-40 表 + 大文件表）。
- 影响：单测难以按行为切入，改动 review 成本高；`services.go` 的注册函数每次加工具都在同一处膨胀；`controller.go` 同时承担动作、快照、投影、恢复、标题生成，是 AR-05/07/08 的共同根因之一。
- 复现/验证：`go run .cache/audit/arch/analyze.go .`。
- 建议修复（M）：tools 注册拆分 per-domain（fs/shell/web/session/team 各一个 builder）；controller 拆 `controller_turn.go / controller_snapshot.go / controller_recovery.go`；给 >150 行函数加 lint 门槛（gofmt 之后跑一个 ast 检查脚本即可）。

### [AR-21] 重复/半死代码：`scrubEnv` 双份、automations DDL 双份、ReadTranscript 生产零调用 · P3 · NEW

- 现象：完全相同函数体 2 组（`mcp/mcp.go:507 scrubMCPEnv` 与 `tools/bash.go:179 scrubEnv`，15 行完全一致）；`automations` 表 DDL 在 `schemaV1`（`schema.go:67`）与 `schemaV1Additions`（`schema.go:85`）**逐字重复**（后者为幂等追加而存在，等于把同一表定义维护两次）；`ReadTranscript` 除测试外无调用（见 AR-14）；工具生命周期状态同时镜像在 3 处（`state.Tools` 快照、`events` 表、`tool_calls` 表，`controller.go:929-936 recordToolCall`）。
- 证据：`.cache/audit/arch/static-analysis.txt`（DUPLICATE FUNCTION BODIES 段）；`internal/store/schema.go:67,85`；`internal/session/controller.go:929-936`。
- 影响：安全相关逻辑（env 清洗）有两份实现，未来只修一份的概率高；DDL 双份让 schema 变更易漏；三处工具状态镜像没有一致性校验（`tool_calls` 表仅"best effort"，注释明说不失败即好）。
- 复现/验证：`go run .cache/audit/arch/analyze.go . | grep -A5 "DUPLICATE"`；`sed -n '60,90p' internal/store/schema.go`。
- 建议修复（S）：env 清洗收敛到 `internal/tools/winenc` 或新 `senv` 包；additions 中删除重复的 automations 定义；`tool_calls` 明确为查询缓存并加校验测试。

### [AR-22] 每 token O(消息数) 快照拷贝（整条 message slice + 全量 string 拼接） · P3 · NEW

- 现象：`state.Apply` 对每个 Token 调 `updateMessage`，后者 `make([]Message, len(prev))`+`copy` **整条消息切片**，再对单条消息 `m.Text += text`（每次 token 复制整个已累积字符串）；ToolStart/ToolEnd 对整张 tools map `make`+copy（`snapshot.go:304-350`）。Projector 批量 256 折叠时同一批内每个事件各拷贝一次。
- 证据：`internal/state/snapshot.go:304-350`；`session/controller.go:904-916`（controller 也手写同样的整 slice copy）；`state/projector.go:140-158`。
- 影响：会话越长、单答越长，每 token 分配 O(messages + text) 的临时对象与拷贝，GC 压力随时间线性增长；这与 AR-06 的逐 token 持久化叠加，是长会话卡顿的潜在结构性来源（未做端到端验证，见 §5）。
- 复现/验证：`go test -bench` 无现成 benchmark；可对 1,000 条消息的快照连续 Apply 10,000 个 token 做微基准（本轮未做）。
- 建议修复（M）：Message 文本改为分段/`strings.Builder`（或按 turn 结束后才生成展示文本，流式用单独 buffer）；messages 用追加日志 + 索引集合而非每次整片复制。

---

## 3. 已验证良好的方面

1. **存储打开路径严谨**：`store.Open` 用单写者连接 + `_txlock=immediate`，显式设置并**回读校验** WAL/foreign_keys/busy_timeout（`store/store.go:85-95,128-176`）；DSN 用 RFC3986 file URI 处理空格/`?`/`#`/`%` 与 Windows 盘符（`store.go:120-146`）——Windows 可移植性是同类项目里少见的认真。
2. **删除路径数据安全**：`DeleteSession` 事务内删行（显式删 checkpoints 以覆盖旧库 FK 缺失），提交后清理 transcript/attachment 目录并返回错误（`store/session.go` DeleteSession），`RemoveSessionDir` 前用正则校验 session id 防路径穿越（`store/transcript.go:20-39`）。
3. **state 包本身设计质量高**：不可变快照 + `atomic.Pointer`、Broker 非阻塞 + drop 计数、Projector 批量折叠与 drain、幂等 Close；覆盖率 94.3%，`go test -race ./internal/state` 1.0s 通过；Broker 并发测试把"投递+丢弃=总数"作为不变量（`broker_test.go:160-176`）。
4. **ADR 纪律**：14 篇 ADR 均有状态字段，且取代关系显式（0003←0006、0007←0008），README/PLAN 有入口；11 篇含验证小节。这在快速迭代项目里属于超平均水平的流程资产（问题在于状态类文档没有同等纪律，见 AR-12/13）。
5. **测试规模与可无头运行**：211 个测试文件/1,122 个测试函数；shell 86.2%、prompt 95%、runtime 81%、tools 80.9%；审计额外补跑 `-race` 的 state/session/**shell 全部 PASS**（shell 518.8s，说明 36% 代码量的 UI 包在审计时并发干净——CI 不跑它只是成本/配置问题，不是"跑不过"）。
6. **构建入口统一、可交叉编译**：`scripts/build.sh` 单入口 + `dist/` 稳定命名 + SHA256SUMS + Windows 图标 `.syso` 提交（无 MinGW 机器也能构建带图标 exe）；`check-nocgo.sh` 对 4 个目标交叉编译并检查依赖图无 CgoFiles，硬约束 C1 真实落地。
7. **密钥卫生**：数据目录 0700、`window.json` 0600（实测 `ls -l`）；`.gitignore` 覆盖 `.env*`/`credentials*`/`.cache/`；`inspect` 工具对 api_key/secret 脱敏（`internal/tools/inspect.go`），e2e 脚本不打印 key。

## 4. 已知问题复核（KNOWN 状态是否仍成立）

| 已知项 | 来源 | 现状核对 | 判定 |
| --- | --- | --- | --- |
| Auto-review 权限缺失 | `FUNCTIONAL-PARITY.md:13` | 已实现：`internal/runtime/review.go` 的 `LLMToolReviewer`（15s 超时回退人工），`app/services.go:866-872` 接线 auto review provider，shell 有 `auto_review_ui_test.go` | 已过时，需回写 ✅ |
| Goal 无自动 round 驱动 | `FUNCTIONAL-PARITY.md:18,38` | `session/goal_driver.go` + `app/services.go:123` 接线 | 已过时，需回写 ✅ |
| `AppendTranscript/ReadTranscript`、`tool_calls` 生产零调用 | `EVOLUTION.md:102` | `AppendTranscript` 已接线（`events.go:139`）、`tool_calls` 已接线（`controller.go:936`）、`ReadTranscript` 仍仅测试 | 部分过时（一半已修，一半仍真） |
| 三平台安装包/签名/自动更新缺失 | `FUNCTIONAL-PARITY.md:28`、brief §7 | `dist/` 仅 Windows zip；代码无签名/更新实现 | 仍成立（并入 AR-03） |
| mygo/eino 预发布 API 风险 | `PLAN.md` R7 | `eino v0.10.0-alpha.35`、`mygo v0.7.4`；启动 sonic 回退告警 | 仍成立（AR-10） |
| 无内核级沙箱（既定产品边界） | `known-limitations.md` §5 | 未变化，审批/sandbox 策略层仍在 | 仍成立，非缺陷 |
| 真机 IME/a11y/DPI/通知未验证 | `known-limitations.md` §1 | 本环境仅 Xvfb，无法验证 | 未验证（见 §5） |
| DSH 像素门禁退役 | ADR 0001 | `internal/pixeltest`、`baseline/` 已删，`ci.sh` 无像素步骤 | 成立 |
| 视觉回归 CI 未强制 | `known-limitations.md:47` | `visual-check.sh`/`TestVisualRegression` 仍为 opt-in，`ci.sh` 不调用 | 成立 |

## 5. 未验证/可疑项（明确未验证）

- **基线冻结问题**：审计期间 HEAD 从 `7eebe47`→`27d5d5f`，报告写作时又有 3 个已跟踪文件被并发修改（见 §1）。本报告的行号与命令输出均取自 `27d5d5f` 提交 + 当时工作树；如果并发进程继续改 `state/snapshot.go`/`shell/session_live.go`，涉及这两个文件的行号需按 diff 重新对照（结论不变：结构与文档问题均在提交内容中成立）。
- **Lead 提出的两个候选（已实测定性）**：① 单实例固定端口 49731 → **成立**，本轮两次启动失败可复现，已记为 AR-11；② "window.json 恢复不做屏幕边界校验" → **部分不成立**：完全离屏状态由 mygo clamp+居中（AR-18 B 例），真正缺口是部分可见时保留超尺寸（C 例）与首启默认不裁剪（A 例），已记为 AR-18（P2）。
- **shell 包 race 的成本/超时**：`CGO_ENABLED=1 go test -race ./internal/shell` 实测 **PASS，518.8s**；对照组普通 `go test` 57.6s。518.8s 已占 `go test` 默认 `-timeout 10m` 的 86%，在更慢的 CI/Windows runner 上很可能先超时；若把 shell 纳入 CI race（AR-02 建议），必须显式 `-timeout 20m` 或放 nightly。审计未覆盖 app/prompt/mcp 的 race。
- **长会话 UI 冻结**：未做端到端帧时间测量。已有间接证据（0.675ms/事件写、1,593 事件/2 回合、整 slice 拷贝）但不足以给"卡 x ms"，列为高可疑。
- **启动/RSS 的代表性**：窗口映射 ~112ms、RSS 167MB、103 线程、VSZ 9.3GB 均在 Xvfb+llvmpipe（软件 GL）下测得；真机 GPU/不同 WM 数字会变，首帧时间未测（只测到窗口被 xdotool 找到）。
- **Windows 真机行为**：路径/编码/pwsh 方言/无控制台的崩溃表现均只能静态核对（brief 环境无 Windows）。
- **API key 落盘**：`provider.api_key` 存在 SQLite `settings` 表（明文），数据目录 0700、index.sqlite 文件 0644；是否有额外加密/系统钥匙串未做，威胁模型未评估——不列为缺陷，列为待确认。
- **`.probe/` 本地副本**：`.probe/verify-copy/` 是 17MB、含 82 个 shell 文件的**陈旧仓库副本**（`.gitignore` 内、非跟踪）。本轮未审计其内容，但它与真仓库并存，存在 agent 改错树的实际风险（建议清理或在 README 标注"禁止引用"）。

## 6. 优先级建议（本维度 Top 9，按投入产出排序）

| 序 | 动作 | 覆盖发现 | 工作量 | 收益 |
| --- | --- | --- | --- | --- |
| 1 | 加 crash handler（recover+文件日志）、日志落盘+轮转、`-ldflags -X` 版本/commit 注入 | AR-01、AR-03 版本部分 | S（1-2d） | 发布后可诊断、可追版本；立刻止血 |
| 2 | 落地自动化 CI（workflow 跑 ci.sh；race 加 app，shell 用 `-timeout 20m` 或 nightly；coverage 基线；禁止 flaky 重跑） | AR-02、AR-17 | S/M（1-2d） | 让"ALL GREEN"从声明变成事实 |
| 3 | 文档收敛：STATUS≤100 行+commit 锚；建 `docs/BUGS.md`；修正 AR-13/14/15 三处事实错误；README 去状态复述 | AR-12、AR-13、AR-14、AR-15、AR-16、AR-19 | S/M（1-2d） | 消除"文档说 X 代码是 Y"，后续审计不再踩空 |
| 4 | 存储写放大治理：token 事件批量/聚合落库，JSONL 改导出格式或异步批量，补 events 保留策略 + VACUUM | AR-06、AR-14 | M（2-3d） | 长会话性能与数据无限增长同时缓解 |
| 5 | 单一快照权威：在"删 projector"与"projector per-session"中二选一，删除第二套折叠与误导性文档 | AR-05、AR-22 | M（2-3d） | 状态一致性与每 token 双倍折叠开销 |
| 6 | 分层止血：拆分 shell（kit/screens/actions），把 `a.Ctrl.*` 与 store 调用从 view/handler 移入异步动作层；补慢 store 不阻塞帧的测试 | AR-04、AR-08 | L（3-5d，可分批） | 降低 UI 回归面与冻结风险；为后续功能扩展腾出手 |
| 7 | 数据模型：消息增加显式序号列（排序不再依赖事件回放）；schema 迁移改造（编号步骤+事务+迁移前备份） | AR-07、AR-09 | M（2-3d） | 排序正确性不再依赖隐式前提；升级不再逼用户删库 |
| 8 | 发布打磨：`-s -w`（-31.8% 体积）、Linux/macOS 打包脚本、单实例锁改数据目录锁 | AR-03、AR-11、AR-22 附带 | S/M（1-2d） | 49.5MB→33.8MB；portable/多用户可用 |
| 9 | 桌面生命周期：首启/恢复窗口尺寸按主屏 WorkArea 裁剪（并补 headless 几何断言） | AR-18 | S（0.5d） | 小屏笔记本与外接拔线场景不再出屏 |

> 说明：以上工作量按"一名熟悉仓库的 Go 工程师"估算；AR-04 是长期项，建议从"新代码必须走 actions 层"开始渐进收敛，而不是大爆炸重写。
