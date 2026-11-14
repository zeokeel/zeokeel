> 附（2026-11-15 后续）：Windows sandbox 并发写修复已随 rig v0.17.1 发布；产品直接 require v0.17.1（无 fork/replace），干净 checkout 可构建；DSH 语义不变。Win32 运行时行为待 Windows 实机验证。
# 05 · 长时任务严格对齐 DSH：最终独立审计（第二轮复审计 + D1–D3 收尾复核）

> 日期：2026-11-15 · 角色：**独立审计员（adversarial verifier）**，非实施者
> 对象：`/data/mygo` @ `e369e8b`（`release: ZeoKeel v0.2.35`）+ **未提交工作树 184 个改动文件**
> DSH 参考：本机 `@deepseek-ai/dsh@0.2.1-rc.2`（能力档案见 `01-dsh-capabilities.md`）
> 对照：`docs/plans/long-task-dsh-alignment.md`、`03-gap-matrix.md`、`04-implementation-status.md`（只作线索）
> 证据分级：**[实测]** ＞ **[源码]** `file:line` ＞ **[判断]**
> 范围：本轮重点复核 W1（可复现构建）/ W2（文档纠偏）/ W3（预压缩 exact、Outcome、gate fail-closed），
> 并重新裁定六条目标与剩余 PARTIAL。上一版报告（W1/W2/W3 前）已被本文件替换。
> **收尾复核（本版最终）**：D1–D3（CHANGELOG / AGENT-SURFACES S10 / 04 历史行与 PNG 标注）已按验收修复并逐项验证（§3.3）。
> 约束遵守：未改产品代码、未 commit、未刷 design baseline；唯一改动是本报告。

---

## 0. 结论（一页）

**六条判定：① PASS　② PASS　③ PASS　④ PASS　⑤ PASS　⑥ PARTIAL（可接受边界，非 blocker）。**

**objective 现在可以标记 complete（无残留文档不一致）**——语义、可构建性、关键审批安全语义、文档一致性均已闭环；上一版遗留的 D1–D3 三处收尾项已按验收修复并复核通过（§3.3），不涉及任何产品代码/行为。

> 与上一版审计相比的三个关键变化：
> 1. **W1（上一版唯一硬 blocker）已清除**：`go.mod` 回到已发布 `zeokeel-rig v0.16.2`、无 `replace`、`go.sum` 有条目；在**无 sibling rig / 无 .git / 无 .cache** 的干净目录用 `-mod=readonly GOPROXY=off` 实测 `go build ./...` + `go vet ./...` + 8 个目标包测试全通过，`go list -m` 指向 module cache。DSH goal 三件套改由产品 `internal/hosttools/goaltools.go` 自实现，模型面 schema/描述/结果 JSON 与 rig v0.17.0 候选实现逐字一致。
> 2. **W3 已落地并复核**：run 前主自动压缩改为 exact（`inferred:false` + `kind=auto` archive + 精确 before/after/replaced/saved + `Outcome` 失败/跳过原因），Outcome 已进 durable payload → ContextInspector 折叠 → read model → Context tab 本地化展示；agent gate 在无 approvals 服务时改为 **fail-closed（deny + 可读错误）**，提示词策略同步为 workspace。
> 3. **W2 已完成**：CHANGELOG `Known limitations`、`04 §3` 已按实现重写（不再把已实现写成未做、去重、PNG 计数 120 与最新证据一致）；上一版残留的 D1–D3 三处小不一致已在收尾复核中修复并验证（§3.3）。

### 0.1 判定总表

| # | 目标条目 | 判定 | 关键变化 / 证据 |
|---|---|---|---|
| ① | Goal v2（三件套/CAS/blocked/256/durable/引擎续跑/UI+headless） | **PASS** | 三件套现为产品自实现（`internal/hosttools/goaltools.go` + `goalrevision.go`），语义与 DSH 一致；engine 驱动/UI/headless 不变。`goaltools_test.go`/`goalrevision_test.go`/`goal_dsh_test.go` 16 测试 + 目标包全绿 |
| ② | 默认上下文安全（预算/默认压缩/溢出重试/压缩可审计） | **PASS**（上一版 PARTIAL 关闭） | run 前主压缩 exact+archive+Outcome（`agent/precompact.go:51-126`、调用 `agent.go:2406`）；手动/溢出路径不变（exact/archive/retry 1 次）；仅 run 中 rig 兜底压缩仍 inferred（已文档化，非破坏性） |
| ③ | 自动化/schedule | **PASS** | 与上一版一致（同会话/delivered+completed/weekly+cron/durable/重启补跑），本轮回归全绿 |
| ④ | 审批 preset + 无人值守 + fail-closed | **PASS**（上一版 PARTIAL 关闭） | `agent/plan_mode.go:331-340` 无 approvals 时 deny + 可读错误；`TestGateNilApprovalsFailsClosed`；`system_prompt.go:468-480` 提示词回退 workspace；headless pin 不变 |
| ⑤ | 通知/常驻 | **PASS** | 与上一版一致（OS 通知/点击直达/关窗保护/close-to-tray/XDG autostart）；无 daemon 为设计非目标 |
| ⑥ | 执行域/cold resume/workflow-ralph/观测性/fork | **PARTIAL** | fork/workflow/ralph/观测性 PASS；执行域 job/终端、cold resume 控制面、ralph 默认暴露、Insights 窗口为**已文档化产品边界**，逐项判非 blocker（§5.6） |

### 0.2 完成度判断

| 问题 | 本轮结论 |
|---|---|
| 六条语义是否达成？ | **5 PASS / 1 可接受 PARTIAL，无 P0 语义缺口**；上一版 ②④ 的窄 PARTIAL 已由 W3 关闭 |
| 可复现构建？ | **是**。`go.mod`/`go.sum` 与 HEAD 完全一致（v0.16.2、无 replace）；干净目录实测通过（§2） |
| 能否标记 complete？ | **可以（无残留）**。W1/W2/W3 与 D1–D3 全部闭环；干净构建、目标测试、文档一致性均已复核 |
| 上一版审计的问题是否消解？ | 是：W1 硬 blocker 消除；②④ 升级 PASS；W2 由「多段自相矛盾」收敛到 3 处小残留，并已随 D1–D3 修复 |

---

## 1. 环境、证据与方法

### 1.1 工作树与依赖状态

- `git rev-parse HEAD` = `e369e8b`；`git status --short` = **184 项**；`git diff --stat` = 109 tracked 文件、+7127/−4732（未计新文件）。[实测]
- `go.mod`：`github.com/zeokeel/zeokeel-rig v0.16.2`，**无 `replace`**；`git diff -- go.mod go.sum` 为**空**（即与 HEAD 的发布状态完全一致）；`go.sum` 含 rig v0.16.2 的 `h1:`/`go.mod` 两条。[实测]
- `/data/zeokeel-rig` 的 v0.17.0 候选 tag **未被产品消费**，仅作为产品三件套的对齐参考。[实测/源码]

### 1.2 证据文件（本轮新生成）

| 证据 | 内容 | 核对 |
|---|---|---|
| `.cache/long-task-final7/manifest.txt` | head `e369e8b`、**changed files 184**、**screenshots 120**、生成 `2026-11-15T01:27:09+08:00` | [实测] 与当前 HEAD/工作树一致；120 张 PNG 完整（含 `p2-goal*`、`p2-inbox*`、`p2-context*`、`p2-context-sections`、`p2-insights*`、`p2-cold-resume*`、`p2-interrupted*`、`p2-model-prices*`） |
| `.cache/long-task-drive7/{drive-report.md,results.json}` | head `e369e8b`、窗口 1280×800、**93/93 PASS**（settings 32 / session 26 / uiroot 15 / shell 10 / team 8 / design 2）、132 张 shot、生成 `01:26:34` | [实测] `Counter({'PASS': 93})`；含 `overlay-goal`、`queue-popover-cancel`、`settings-page-notifications`、`nav-automation` |
| 抽查 PNG | `p2-goal.png`（138 KB）、`p2-context-sections.png`（141 KB）、`p2-insights.png`、`p2-cold-resume/child`、`p2-interrupted{,-dark}` | [实测] 均存在且非空；final7 与 final6 的 PNG 集合完全相同（120 张） |

### 1.3 本轮实跑命令（定向，未跑全量）

```bash
export PATH=/usr/local/go/bin:$PATH GOPATH=/data/mygo/.cache/gopath GOCACHE=/data/mygo/.cache/go-build \
       GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp
mkdir -p /tmp/zeokeel-tmp
```

| 命令 | 结果 |
|---|---|
| `gofmt -l internal cmd` | 空 |
| `go build ./...` / `go vet ./...` | 通过（缓存命中） |
| `go test ./internal/modules/goal ./internal/modules/agent ./internal/modules/automation ./internal/modules/approvals ./internal/modules/insights ./internal/ui/session -count=1` | 6 包全 **ok**（agent 1.359 s；automation 3.718 s；ui/session 11.534 s） |
| `go test ./internal/assembly ./internal/host ./internal/uiroot ./internal/regression -count=1` | 全 **ok**（regression 21.838 s） |
| `go run ./internal/devtools/designlint --strict --baseline .design-baseline.json ./...` | `294 files, 91 violations: 0 error, 0 warn … 0 new, 0 blocking` |
| W3 定向（见 §4） | 全部 PASS（precompact 5、context outcome UI 3、gate 1、overflow 4、hosttools 2 包） |
| 干净目录（见 §2） | build/vet + 8 目标包全 **ok** |

> 未跑：`go test ./...` 全量、`-race` 全量、ui-drive/ui-snapshot 重跑（本轮证据由 `final7`/`drive7` 覆盖，本人做了清单/计数/PNG 存在性与内容抽查）。本报告不把「全量绿」当作本人验证结论。
> 上一版记录的一次 automation `waitCall` 10 s flake 本轮未复现（6 包 + 整包均通过）；该测试时序问题仍是 P3 待加固项（§5.6）。

---

## 2. W1 复核：可复现构建（上一版硬 blocker）—— **已清除**

### 2.1 干净目录实测（无 sibling rig / 无 .git / 无 .cache）

```bash
rm -rf /tmp/zeokeel-clean && mkdir -p /tmp/zeokeel-clean
cd /data/mygo && rsync -a --exclude '.git' --exclude '.cache' ./ /tmp/zeokeel-clean/
cd /tmp/zeokeel-clean
ls -d /tmp/zeokeel-rig           # → 不存在（无 sibling rig）
grep -n replace go.mod           # → 无 replace
export PATH=/usr/local/go/bin:$PATH GOPATH=/data/mygo/.cache/gopath \
       GOCACHE=/data/mygo/.cache/go-build GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off
go list -m github.com/zeokeel/zeokeel-rig   # → v0.16.2
go list -m -f '{{.Path}} {{.Version}} dir={{.Dir}}' github.com/zeokeel/zeokeel-rig
# → github.com/zeokeel/zeokeel-rig v0.16.2 dir=/data/mygo/.cache/gopath/pkg/mod/github.com/zeokeel/zeokeel-rig@v0.16.2
go build ./...    # 7.5 s，通过
go vet ./...      # 2.9 s，通过
go test ./internal/modules/goal ./internal/modules/agent ./internal/modules/automation \
        ./internal/modules/approvals ./internal/modules/insights ./internal/modules/hosttools \
        ./internal/hosttools ./internal/ui/session -count=1
# → 8 包全 ok（goal/agent/automation/approvals/insights/modules-hosttools/hosttools/ui-session）
```

**结论**：`-mod=readonly` 能成功解析依赖、`go list -m` 指向 module cache、构建与目标测试通过 → **上一版「干净检出/CI 无法构建」的 blocker 已消除**。[实测]

### 2.2 DSH goal 三件套改为产品自实现（rig 不消费 v0.17.0）

| 关注点 | 结论 | 证据 |
|---|---|---|
| 为什么自实现 | rig v0.16.2 无 `Options.GoalRevision`（该能力只存在于本地未发布的 v0.17.0 候选） | `internal/hosttools/goaltools.go:1-18`；rig v0.16.2 `tools/hosttools/hosttools.go:48-60` |
| 产品工具注册 | `get_goal`/`create_goal`/`update_goal` 由产品追加；rig 自带 trio 被名称过滤掉，不会重复注册 | `internal/modules/hosttools/tools.go:55,88`；模块测试全绿 |
| 模型面兼容 | 工具名、description、JSON schema、结果 JSON（`{"goal":null}` / `{"goal":{...},"activation":...}`）与 rig v0.17.0 候选实现逐字一致 | 与 `/data/zeokeel-rig/tools/hosttools/goal_dsh.go:130-230` 逐段比对 [源码]；`goaltools_test.go:63 TestGoalToolsSchema`、`:98 TestGetGoalNullAndValue` |
| DSH 语义 | session ctx 解析、create/edit/pause/resume 仅直接人类 turn、goal-round 的 blocked 需 ≥3 轮且带 reason、CAS stale 拒绝、activation、删除会话不复活 | `internal/hosttools/goalrevision.go:73-238`；`goalrevision_test.go` 8 测试；`goal_dsh_test.go` 5 个行为测试（全部实跑通过） |
| 默认轮数 | `create_goal` 省略 → rig 常量 `DefaultMaxGoalRounds=256` | `goaltools.go:31-34,125-152`；`contracts/goal.go:72-74` |

**判定**：W1 修复方式（回退已发布依赖 + 产品自实现等价工具）**没有降低 DSH 语义对齐度**；模型可见面与行为保持不变，且换来真实可复现构建。[实测+源码]

---

## 3. W2 复核：文档纠偏 —— **完成（D1–D3 已修复并验证）**

### 3.1 已修正（与上一版对比）

| 位置 | 上一版问题 | 现状 |
|---|---|---|
| `CHANGELOG.md:46-60` Known limitations | 把通知点击直达、sections、cold resume、fork、逐调用 usage/成本等**已实现**写成未做；重复 Stop 队列条目；`111 PNG` | 已按实现重写：自动压缩 exact 说明、nil approvals fail-closed、cold resume 控制面、Insights 4000、Stop inbox、执行域、rig v0.16.2 等；PNG 计数 `120` 与 final7 一致 [实测] |
| `CHANGELOG.md:61-65` Verification | 111 PNG 与最新证据不符 | `ui-snapshot 120 PNG` + `ui-drive 93/93 PASS` 与 final7/drive7 一致 [实测] |
| `04-implementation-status.md:70-83` §3 | 「Interrupted UI 未做、溢出重试未做、压缩不可观测/破坏性、team cold resume 未做、逐调用 usage 未做、fork 只复制文本、通知只置前」等已实现项仍列为限制 | 重写为 10 条真实剩余限制/产品边界；与 §1 第九轮及代码一致 [源码] |
| `04` §4 下一轮建议 | 仍把 W1/W2 列为待办 | 标注 W1/W2 已完成，剩余为执行域/cold resume 控制面/rig v0.17.0 可选发布等 [源码] |
| `README.md:323` | 曾宣称 rig v0.17.0 + replace | 现为「rig 保持已发布 v0.16.2（无 replace，干净 checkout 可构建）」[源码] |

### 3.2 验证

- `git diff` 抽查 `CHANGELOG.md`/`04`/`README.md`：上述声明与当前 `go.mod`、测试名、代码行一一对得上。[实测+源码]
- 证据计数：final7 `screenshots 120` = CHANGELOG `120 PNG`；drive7 `93/93` = CHANGELOG/04 的 `93/93`。[实测]

### 3.3 收尾复核：D1–D3 已修复并验证（本版最终）

| # | 复核点 | 现状 | 验收结果 |
|---|---|---|---|
| D1 | CHANGELOG 不得再把 exact 压缩说成「只能推断」 | `CHANGELOG.md:56` 已改为「**run 中 rig 内兜底压缩只能推断（run 前主压缩为 exact + archive + Outcome）**」；`:48` 保留 run 前 exact 说明 | [实测] `grep -n "自动压缩事件只能推断" CHANGELOG.md` **无匹配**；两条语义一致（主路径 exact / 兜底 inferred），不再矛盾 |
| D2 | AGENT-SURFACES S10 事件规格同步 W3 | S10「上下文事件」现为四分类：`Inferred=true`→「压缩（推断）」；`Inferred=false`+`Detail=manual`→「压缩」；`Inferred=false`+`Outcome==""`→「自动压缩」（运行前 exact，显示 before→after tokens 与 `Replaced → Saved`）；`Outcome!=""`→「自动压缩跳过/失败：failed\|no-reduction\|archive-failed\|replace-failed」+ 本地化原因；失败只显示 tokens、**不显示消息 delta、不显示「已压缩」**；archive 与 compact 独立成行、不合并/不重复计数 | [实测] 规格文本与 `TestContextCompactOutcomeEvents`/`TestContextCompactOutcomeEnglish`/`TestContextCompactSuccessInferredAndManual` 的断言逐项对应（4 个 Outcome 标签 + 「自动压缩」/「运行前自动压缩」） |
| D3 | 04 不得把当前态写成 replace 消费；111 PNG 须标历史 | `04:37` 已改为「rig 支撑（历史，已被第九轮回退）」并写明当前=已发布 v0.16.2（无 replace）+ 三件套产品自实现；`04:48` 已标「（历史记录，当时 `ui-snapshot` 111 PNG）」；`04:35` 第九轮=无 replace | [实测] `grep -n replace 04` 仅剩 `:25`（第四轮历史轮次记录，已被 `:35/:37` 明确覆盖）与正确表述（`:35/:37/:80/:87`）；**无当前态 replace 声明**，历史块已标注 |

**残留说明**：`04:25` 的「第四轮：rig v0.17.0 + 临时 replace」是**按轮次记录的历史行**（表中同时有第九轮回退行与「历史，已被第九轮回退」行），不是当前态陈述；如需绝对清晰可在该行加「（已被第九轮取代）」标注，但不构成不一致。除此之外 **D1–D3 全部通过，无残留文档 blocker**。[实测+源码]

---

## 4. W3 复核：预压缩 exact、Outcome 全链路、gate fail-closed —— **已闭环**

### 4.1 run 前主自动压缩 exact + archive

| 关注点 | 结论 | 证据 |
|---|---|---|
| 触发位置 | run 开始、Rig 追加本轮之前：`system prompt + durable history` 估算超过有效预算才压缩 | `internal/modules/agent/precompact.go:51-77`；调用点 `agent.go:2400-2406`（注释明确「新用户消息由 Rig 之后追加，恰好一次」） |
| 归档 | 压缩前写 `compact_archive`（kind=auto，2 MiB/200 条上限）；archive 失败**跳过压缩、不阻塞 run**，事件带 `archive-failed` | `precompact.go:100-124`、`compact_archive.go:105-134`；`TestPreRunCompactArchiveFailureDoesNotBlockRun` |
| 精确事件 | 成功替换后写 durable `compact`（kind=auto、`inferred:false`、精确 before/after/replaced/saved、Outcome 为空）；失败/无可减少/replace 失败也写事件带 Outcome，绝不伪造成「已压缩」 | `precompact.go:124-131`；`observability.go:192-196`（`recordCompactEvent(..., inferred, outcome)`）；`TestPreRunCompactArchivesAndRecordsExactEvent`、`TestPreRunCompactFailureDoesNotBlockRun`、`TestPreRunCompactNoReductionRecordsOutcome` |
| 不重复/不重放 | 压缩只改 durable history；本轮 user 消息由 Rig 之后追加 → 不重复；无工具副作用重放 | 测试断言 user 消息恰好 1 条、first model request 含 SUMMARY 且新消息 1 次（`precompact_test.go:184-230`） |
| mid-run 兜底 | rig 内随增长压缩仍 `inferred:true`（rig 不暴露事件）；明确文档化 | `CHANGELOG.md:48`、`04 §3.1`；`TestInferredAutoCompactionHeuristic` |

### 4.2 Outcome fold / read model / UI 全链路

- **durable payload**：`compactPayload.Outcome`（`observability.go:112-119`）。
- **折叠**：live + restore replay 都把 Outcome 带入 `contracts.ContextEvent.Outcome`（`observability.go:355-373`，`:368`）；契约 `contracts/contextinspect.go:216-224`。
- **read model**：`ContextInspector.SessionContext` 返回的事件现已带 Outcome；`assertFoldedCompactOutcome` 同时断言 **folded、durable、restore replay 三者一致**（`precompact_test.go:127-150`）。
- **UI**：Context tab 事件区区分「自动压缩（运行前精确）」「压缩（推断）」「手动压缩」与「自动压缩跳过/失败：failed / no-reduction / archive-failed / replace-failed / 未知原因」；失败/跳过只显示 before→after tokens，不显示会误导的消息 delta，绝不显示「已压缩」（`ui/session/context_pane.go:528-630`；测试 `TestContextCompactOutcomeEvents`、`TestContextCompactSuccessInferredAndManual`、`TestContextCompactOutcomeEnglish` 实跑通过）。
- **任务-29 的 fold 修复**：Outcome 不再只存在于 durable payload；`observability.go:360-373` 注释与测试锁定。

### 4.3 gate 无 approvals 服务 → fail-closed

- `agent/plan_mode.go:325-340`：`s.approvals == nil` 时对**需要审批**的工具返回 deny + 可读错误（不再是静默 allow）；read-only/workspace/plan/chat/sandbox-shell/interaction/remembered 分支保持既有优先。 [源码]
- 提示词不再宣称「no approval prompts」：`system_prompt.go:468-480` 在无 approvals 时回退 `workspace` 策略。 [源码]
- 测试：`usage_gate_test.go:325 TestGateNilApprovalsFailsClosed`（同时覆盖 read-only 硬拒绝、sandbox auto-allow、full allow、ask_user allow 等分支）实跑通过。 [实测]

### 4.4 本轮 W3 定向实跑（全部 PASS）

```
go test ./internal/modules/agent -run 'TestPreRunCompact|TestGateNilApprovalsFailsClosed|TestCompactEventOnManualCompaction|TestInferredAutoCompactionHeuristic|TestContextOverflow|TestContextInspectorSurvivesRestart' -count=1
# 12 个用例 PASS（含 5 个 TestPreRunCompact*、TestGateNilApprovalsFailsClosed、overflow 3、manual/inferred/restart）
go test ./internal/ui/session -run 'TestContextCompactOutcome|TestContextCompactSuccessInferredAndManual|TestContextRecoveryEvents|TestContextArchiveEvents' -count=1   # ok
go test ./internal/hosttools ./internal/modules/hosttools -count=1                                                                                                   # ok（goal trio 16 测试）
```

**判定**：上一版 ② 的「自动压缩不可审计」与 ④ 的「gate fail-open」均已关闭；②④ 升级为 **PASS**。剩余 mid-run inferred 属 rig 边界、已文档化，不再构成 PARTIAL（在 §5.6 记为可接受边界）。[实测+源码]

---

## 5. 六条重新裁定（含未变项）

### 5.1 ① Goal v2 —— **PASS**

| 子项 | 证据 |
|---|---|
| 三件套 + 旧 goal 下线 | 产品自实现 `internal/hosttools/goaltools.go:84-260`；注册 `internal/modules/hosttools/tools.go:88`；rig 旧 goal 被过滤 |
| CAS | `internal/contracts/goal.go:110-119`；`modules/goal/service.go:261-265`；`TestUpdateGoalStaleRefIsRejected` |
| blocked（reason/≥3 轮/round-limit） | `contracts/goal.go:75-81`；`goalrevision.go:120-132`；`service.go:410-425`；`TestBlockedRequiresReasonAndDisarms`、`TestAdmitRoundCountsAndBlocksAtCap` |
| 默认 256 / create 覆盖 | `contracts/goal.go:72-74`；`service.go:187-196`；`TestCreateGoalHonoursPositiveBudgetAndRejectsConflicts`；final7 `p2-goal.png` 显示 `0/256 轮` |
| durable/重启 disarmed/删除防复活 | `service.go:640-821`；`TestGoalReplaySurvivesRestartAndIsDisarmed`、`TestDeletedSessionCannotBeRevivedByGoalEvents` |
| 引擎续跑/Stop disarm/inbox 优先 | `agent/goal_continue.go:88-140`；`promptqueue.go:346-369`；`agent.go:3119-3140`；定向测试通过 |
| UI per-session/轮次/blocked/armed | `ui/session/goal.go:6-20`、`session.go:241-244`；`goal_test.go`；final7 goal 帧 |
| headless/ACP | `headless.json` 含 `zeokeel/goal`；驱动无 UI 依赖；headless 常驻 `host/host.go:286-330` |
| fork goal 复制 | `service.go:535-620`；`agent.go:1617-1640`；`TestForkCopiesGoalDisarmed` |

残留差异（P3，可接受）：`AdmitRound` 在启动前计数（DSH 在消息真正进入历史后计数）；replay 取最后有效快照（DSH 严格 fold）。见上一版 §2.2，未变。

### 5.2 ② 默认上下文安全 —— **PASS**（本轮升级）

- 预算推导 `min(0.8W, W−MaxTokens−65536)`、默认 compactor、`/compact` 无手工预算：`agent/context_budget.go:41-100`、`compact_now.go:64-70`（与上一版一致）。
- 溢出恢复：一次压缩 + 同 turn 重试一次、不重复 user/不重放工具、durable `context_recovery`：`context_recovery.go:51-126`；`TestContextOverflow*` 4 用例通过。
- 手动压缩审计 + fail-closed archive：`compact_archive.go:35-134`、`compact_now.go:125-143`；4 个 archive 测试通过。
- **run 前主自动压缩 exact + archive + Outcome（本轮新增）**：见 §4.1。
- 残留（可接受）：run 中 rig 兜底压缩仍 inferred（rig 不暴露事件）、无 tool-result pruner/image offload（计划未列入本条验收，P2 可后补）。

### 5.3 ③ 自动化/schedule —— **PASS**

未变项，本轮回归全绿：`automation.go:140-145,261-269,761-854,1071-1101`；`contracts/automation.go:51-59,99-133,220-320`；`TestRunsReuseBoundSession`、`TestDeliveredThenCompleted`、`TestRestartSettlesPersistedDeliveredRun`、`TestRestartCatchesUpMostRecentRun`、schedule 解析测试。边界：schedule 依附桌面进程存活（⑤ 形态边界，非语义缺陷）。

### 5.4 ④ 审批 preset / 无人值守 / fail-closed —— **PASS**（本轮升级）

- 三档 preset 映射与「所有权限 = 无人值守」文案：`ui/settings/behavior.go:41-58`、`ui/automation/view.go:628-634`。
- headless/ACP pin：`approvals.go:90-97,239-260`、`headless.json`（auto + ignorePermissionSetting）。
- **无 approvals 服务 fail-closed（本轮新增）**：§4.3。
- 审批等待通知带 session、点击直达会话：`approvals.go:298-330`；`host/notification_click.go`。
- 残留（文档/边界）：approvals **服务存在但配置为 `ask` 且没有任何 answerer** 的自定义 headless 装配仍会等待（DSH 的 `unavailable` 语义未建模）；shipped 桌面/headless(auto+pin)/ACP 均不出现该路径，已写入 CHANGELOG Known limitations；判非 blocker。

### 5.5 ⑤ 通知 / 常驻 —— **PASS**

未变项：`uiroot/notifier.go:100-130,242-260`、`host/notify.go:30`、`host/host.go:566,624-626`、`host/notification_click.go:44-125`、`host/close_guard.go:34-120`、`host/tray.go:59-126`、`host/autostart.go:18-75`；本轮 `host`/`uiroot` 测试与 `ui-drive settings-page-notifications` PASS。明确非目标：systemd/launchd、macOS 打包前提。

### 5.6 ⑥ 其余 —— **PARTIAL（全部为已文档化产品边界，不阻塞 complete）**

| 子项 | 现状 | 是否 blocker | 理由/若要求修复的验收 |
|---|---|---|---|
| 执行域 job/终端 | 进程内；无 workspace 根/未绑定会话根时切工作区会 retire 旧 build（有会话根时 override build 保护） | **否**（P2） | DSH jobs 同样不跨重启；产品损失只在「无会话根」路径且已文档化。若修：`toolkit/rebind.go` 按会话持有，验收=切工作区后 job 仍 running |
| cold resume 控制面 | 历史 teammate「问一次答一次」，无 live Wait/Interrupt/Close；运行中 overlay 无停止 | **否**（P2） | 「重启后还能继续工作」的最小闭环已达成且绝不自动恢复；DSH live team 语义在冷恢复场景无直接对等。若修：运行中可 interrupt 且状态 durable，验收=UI + child session 状态机测试 |
| ralph 默认暴露 | 已注册，guidance 限定「仅显式请求」，可经 preset/`tools.disabled` 关闭；无默认关闭开关 | **否**（P2，产品选择） | 实验性工具已被 model guidance + 权限预设约束；DSH 默认关闭属产品策略差异。若要求严格一致：加默认关闭位并由 assembly 显式开启，验收=默认工具面无 ralph、preset 开启后出现 |
| Insights 4000 事件窗口 | 每会话最近 4000 事件，超限 `Truncated` 明示；无预聚合 | **否**（P2） | 诚实截断、不静默少算；DSH 有投影表属实现深度差异。若修：预聚合投影表，验收=截断标志消失且性能不退化 |
| rig v0.17.0 未消费 | 产品 v0.16.2 + 产品自实现 goal trio；模型面/行为与 v0.17.0 候选一致 | **否**（明确产品决策） | 可复现构建优先；CHANGELOG/README 已声明。若将来发布：bump v0.17.0 + 删产品三件套，验收=模型面 schema 不变 + 干净构建 |
| mid-run inferred 压缩 | rig 内兜底仍 inferred | **否** | 主路径 exact；兜底不破坏 durable history，且已文档化 |
| automation 测试时序 | `RunNow` 异步 execute 尾部 inFlight 窗口可能让一次手动触发被折叠（上一版记录一次 10 s flake，本轮未复现） | **否**（P3） | 与既有 in-flight 折叠设计一致；建议给 RunNow 加显式 ack/排队，验收=`-count=20` 稳定 |
| fork/workflow/ralph/observability/中断提示/通知点击 | 全部实现且有测试/PNG | — | 见上一版 §7.1；本轮回归通过 |

---

## 6. 证据与文档同步抽查（本轮）

- `.cache/long-task-final7/`：120 PNG，head `e369e8b`，changed 184，生成 01:27:09；与 final6 帧集合一致。[实测]
- `.cache/long-task-drive7/`：93/93 PASS，132 shots，head `e369e8b`，生成 01:26:34；含 goal/queue/automation/notifications 步骤。[实测]
- `docs/AGENT-SURFACES.md`：S10/S11/新状态仍在，价格规则在；**S10 事件规格已同步 W3 exact auto compact / Outcome 四态（D2 复核通过）**。[源码]
- `docs/DESIGN.md`：Insights 场景行在（`:256`）。[源码]
- `designlint --strict --baseline`：0 error / 0 warn / 0 new（294 files）。[实测]
- `README.md`：rig v0.16.2 无 replace 的表述与 `go.mod` 一致（`:323`）。[实测+源码]

---

## 7. 结论与剩余工作

### 7.1 能否标记 objective complete？

**能。** 依据：

1. **六条语义**：5 PASS + 1 可接受 PARTIAL（⑥），无 P0 语义缺口；上一版 ②④ 已由 W3 关闭。
2. **可构建/可发布**：`go.mod`/`go.sum` 与 HEAD 一致（v0.16.2，无 replace），无 sibling 干净目录 `-mod=readonly GOPROXY=off` 构建 + 目标测试通过；上一版唯一硬 blocker 消除。
3. **安全语义**：gate 在无 approvals 服务时 fail-closed；headless 无人值守 pin 不变。
4. **剩余 PARTIAL**：全部是已写入 CHANGELOG/04 的产品边界或 DSH 共同限制，逐项判非 blocker（§5.6）。

**无附带条件**：D1–D3 已按验收修复并复核通过（§3.3），W1/W2/W3 全部闭环；功能、构建、测试、证据、文档一致性均已满足 → **objective 可立即标记 complete**。

### 7.2 剩余工作

**无完成前置项**：W1（可复现构建）、W2（文档）、W3（exact 压缩 / Outcome / gate fail-closed）与 D1–D3 全部闭环；无产品代码、依赖或文档一致性 blocker。

可选、不阻塞 complete 的后续改进（建议独立任务）：W4a 执行域按会话持有（3–5d）；W4b cold resume 控制面 + ralph 默认关闭开关（2–4d）；W4c automation `RunNow` ack/测试加固（0.5–1d）；W4d 自动压缩 mid-run 精确事件（需 rig 改动，2–3d）；W4e rig v0.17.0 发布后重新消费（0.5–1d）。

### 7.3 可接受边界（不作为 blocker，建议保留在 04 §3）

- 无 systemd/launchd；macOS 通知需打包；无 dbus 降级 toast；通知点击在会话已删时只置前。
- 执行域 job/终端进程内 + 无会话根时工作区切换回收；cold resume 一问一答；ralph 实验性默认暴露（guidance/preset 约束）；Insights 4000 窗口（截断明示）；sync subagent 阻塞；Stop 后队列需人类手势；模型 fallback 缺失；mid-run inferred 压缩；DSH 的 tool-result pruner/image offload 未做。
- 自定义 embedder 无 approvals 服务时 gate fail-closed 会中止整个 run（rig gate error 语义），仅自定义装配可达。

---

## 附录 A：复跑命令

```bash
cd /data/mygo
export PATH=/usr/local/go/bin:$PATH GOPATH=/data/mygo/.cache/gopath GOCACHE=/data/mygo/.cache/go-build \
       GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp
mkdir -p /tmp/zeokeel-tmp

# 主验证
gofmt -l internal cmd
go build ./... && go vet ./...
go test ./internal/modules/goal ./internal/modules/agent ./internal/modules/automation \
        ./internal/modules/approvals ./internal/modules/insights ./internal/ui/session -count=1
go test ./internal/assembly ./internal/host ./internal/uiroot ./internal/regression -count=1
go run ./internal/devtools/designlint --strict --baseline .design-baseline.json ./...

# W1 干净构建（无 sibling rig / 无 .git / 无 .cache）
rm -rf /tmp/zeokeel-clean && mkdir -p /tmp/zeokeel-clean
rsync -a --exclude '.git' --exclude '.cache' /data/mygo/ /tmp/zeokeel-clean/
cd /tmp/zeokeel-clean
export GOFLAGS=-mod=readonly GOPROXY=off
go list -m -f '{{.Path}} {{.Version}} dir={{.Dir}}' github.com/zeokeel/zeokeel-rig   # 期望 module cache
go build ./... && go vet ./...
go test ./internal/modules/goal ./internal/modules/agent ./internal/modules/automation \
        ./internal/modules/approvals ./internal/modules/insights ./internal/modules/hosttools \
        ./internal/hosttools ./internal/ui/session -count=1

# W3 定向
go test ./internal/modules/agent -run 'TestPreRunCompact|TestGateNilApprovalsFailsClosed|TestCompactEventOnManualCompaction|TestInferredAutoCompactionHeuristic|TestContextOverflow|TestContextInspectorSurvivesRestart' -count=1
go test ./internal/ui/session -run 'TestContextCompactOutcome|TestContextCompactSuccessInferredAndManual|TestContextRecoveryEvents|TestContextArchiveEvents' -count=1
go test ./internal/hosttools ./internal/modules/hosttools -count=1
```

## 附录 B：证据索引

- 代码（W1）：`go.mod`、`go.sum`、`internal/hosttools/{goaltools,goalrevision}.go`、`internal/modules/hosttools/{tools,module}.go`。
- 代码（W3）：`internal/modules/agent/{precompact,compact_now,compact_archive,context_recovery,observability,agent,plan_mode,system_prompt}.go`、`internal/contracts/contextinspect.go`、`internal/ui/session/context_pane.go`。
- 测试：`internal/modules/agent/precompact_test.go`（5）、`internal/ui/session/context_test.go`（Outcome 3 例）、`internal/modules/agent/usage_gate_test.go`（`TestGateNilApprovalsFailsClosed`）、`internal/hosttools/{goaltools,goalrevision}_test.go`、`internal/modules/hosttools/goal_dsh_test.go`。
- 文档：`CHANGELOG.md`（Unreleased §Added/§Changed/§Known limitations/§Verification）、`docs/research/long-task/04-implementation-status.md`（§1/§3/§4）、`docs/AGENT-SURFACES.md`（S10/S11）、`docs/DESIGN.md:256`、`README.md:323`。
- 证据：`.cache/long-task-final7/manifest.txt` + 120 PNG；`.cache/long-task-drive7/{drive-report.md,results.json}`（93/93 PASS）+ 132 shots。
