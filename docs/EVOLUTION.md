# ZeoKeel 自进化闭环（Self-Evolution Loop）设计提案

> 版本 v0.1 · 2026-11-04 · 状态：待拍板
> 输入：仓库实跑证据 + `.evolve/research/01-audit.md`（现状审计，repo-auditor）+ `.evolve/research/02-external.md`（外部模式调研）+ `.evolve/research/03-product-review.md`（产品/设计自评审，37 项 rubric）
> 本文回答一个问题：**怎样让 ZeoKeel 自己找 bug、自己修、自己测、自己评审产品与设计，并且可信、可控、可回滚。**

---

## 0. 一页结论

| 问题 | 结论 |
| --- | --- |
| 「自动进化」到底能做什么 | 能自动做：**发现客观失败 → 定位 → 提案修复 → 用测试/像素/编译门禁验证 → 记录与学习**；不能自动做：产品方向、设计品味、对外承诺、发布决策 |
| 最值得先做的形态 | **研发期外部闭环**：仓库既有门禁 + 外部编码 Agent（DSH 等）+ 夜间调度；2–3 天可跑起来，零产品风险 |
| 产品内置形态 | `zeokeel evolve`（headless 子命令）+ 复用 `internal/automation` 调度 + SQLite 状态；1–2 周，是「个人版 Agent 自己能进化」的正解 |
| 明确不做 | 在线 AlphaEvolve/DGM 式「Agent 改自己二进制并自动采用」；运行时自动改 system prompt；LLM judge 单独当发布门禁；自动 merge |
| 成败关键 | 不是模型多强，而是**门禁确定性**（现在测试有 flake）、**安全网**（现在无 git/CI）、**真源文档**（README/STATUS/PLAN 互相矛盾） |
| 今天就能落地的第一层 | ① `git init` + 基线 tag ② `scripts/evolve/gates.sh` 统一跑门禁并出机器可读报告 ③ 把本轮评审结果写成 backlog ④ 修 STATUS 单一真源 |

三条闭环，共用一套安全网：

```
                     ┌──────────── 安全网（worktree / 门禁 / 预算 / 审计 / 人类闸门）────────────┐
 信号 Sensors ──▶ 分诊 Triage ──▶ 修复 Repair ──▶ 验证 Verify ──▶ 采纳 Adopt / 回滚 Rollback
   ▲                                                                    │
   │                                                              ┌─────▼──────┐
   └──────────── 学习 Learn（反思/技能/评测集/评分板）◀─────────────┤ 产品评审 Review │
                                                                   └────────────┘
```

---

## 1. 定义与边界

### 1.1 什么是「自进化」

本文的「自进化」指**可验证、可回滚的持续改进**，不是无监督的自我重写：

| 层 | 能力 | 由谁裁决 | 产物 |
| --- | --- | --- | --- |
| L1 自体检 | 跑门禁、找失败、归类、去重、排优先级 | 确定性脚本 | issue backlog |
| L2 自修复 | 复现 → 最小补丁 → 跑门禁 → 提案 | 测试/编译/像素门禁 | diff + 验证记录 |
| L3 自评审 | 按 rubric 评产品功能/交互/视觉/文档真实性 | 客观项自动 + 主观项 judge + 人类抽检 | 评审报告 + 改进提案 |
| L4 自学习 | 失败反思、技能沉淀、评测集增长、提示词离线优化 | eval set + 人类 review | learnings / skills / prompt 版本 |

### 1.2 非目标（先写死，防范围失控）

- ❌ 在线「改自己的二进制并按自评分自动采用」（AlphaEvolve / DGM 式）——ZeoKeel 是单二进制桌面产品，权限是策略级、无内核沙箱（PLAN.md R5），不可审计地自改不可接受。
- ❌ 运行时自动改 system prompt / 工具描述——成本、污染、审计都不可控；只做开发期离线优化 + 人工 review。
- ❌ 用 LLM judge 单独作为发布门禁——JudgeBench 显示 judge 在客观正确性上接近随机，且有 self-preference。
- ❌ Agent 自动 merge / 自动发版——自动化的终点是「变更提案」，采纳必须过人类闸门。
- ❌ 把「自评分数」当优化目标——会直接诱发 reward hacking。

### 1.3 与「与 DSH 1:1 对齐」的关系

自进化是**超出 DSH 的叠加能力**，不进入像素对齐面：

- 默认关闭（`self_evolution.enabled=false`）；
- 第一形态只做 headless CLI + 状态目录，不动五屏 UI；
- GUI 入口放到最后（复用 Automation 页的卡片式呈现，不新建页面）；
- 需要一条 ADR 记录「这是 ZeoKeel 独有、非 DSH parity 范围」。

---

## 2. 现状盘点：手里的牌与缺口

### 2.1 可以直接复用的资产（已核实）

| 资产 | 证据 | 对自进化的价值 |
| --- | --- | --- |
| 542 个测试函数、18 个包 | `grep -rh '^func Test' internal --include=*_test.go \| wc -l` = 542；`go test ./...` 18 包 | 现成的 verifier；失败输出可当信号 |
| 无头渲染 + 像素回路 | `internal/pixeltest`、`TestScreensBaseline`、`TestScreensDarkNarrow`、`ui.Render` | 视觉回归的客观裁决器（不需要显示器） |
| 硬门禁脚本 | `scripts/check-nocgo.sh`（零 cgo + 四目标交叉编译）、Makefile | 结构/约束门禁可直接进闭环 |
| 真实模型 E2E | `scripts/e2e-model.sh`、`scripts/e2e-tools.sh`、`internal/e2e`（`-tags e2e`） | 端到端验收信号（需要凭据，默认跳过） |
| Agent 运行时 | `internal/runtime`（eino ADK：工具回路/审批/压缩/checkpoint/多拓扑模式） | 修复 Agent 的执行引擎 |
| 会话控制器 | `internal/session.Controller`（CreateSession/Submit/Stop/事件流） | headless 驱动的入口；无需 GUI |
| 持久化 | `internal/store`（SQLite + JSONL，`events`/`messages`/`tool_calls`） | 轨迹、审计、回放、learnings 的落点 |
| 调度器 | `internal/automation`（cron 子集 + `Scheduler.OnFire`，当前 OnFire = 建会话并提交提示词） | 夜间自体检的触发器 |
| 自驱动工具 | `internal/tools/{ralph,goal,subagent,jobs,schedule,team,workflow}`；`internal/agentteam`（有并发上限、批量原子性） | 会话内自驱 / 并行修复 |
| 技能库 | `internal/skills`（目录 + `SKILL.md` frontmatter，运行时注册为 `skill` 工具） | 学到的技能可直接沉淀 |
| 提示词层 | `internal/prompt`（37 个 section、确定性 `Compose`、EN/ZH） | 离线优化对象；变更可版本化 |
| 产品基线 | `baseline/`（106MB 截图/CSS/控件 dump）、`internal/design` tokens | 产品/视觉评审的对照物 |

### 2.2 缺口（按阻塞程度排序）

| # | 缺口 | 证据 | 对闭环的影响 |
| --- | --- | --- | --- |
| G1 | **无 git/CI，无回滚基线** | `git rev-parse` = not a repository；无 `.github/` | 自修复无法安全试错/回滚，是 P0 阻塞 |
| G2 | **门禁不完全确定（存在 flake）** | 同一天：`TestExperimentalSwitches` 全量跑 2/3 失败、单跑 20/20 通过；`TestScreensDarkNarrow` 首跑失败、重跑通过 | 假失败会浪费修复预算，甚至诱发「为过测试而改测试」 |
| G3 | **无 headless 入口** | `cmd/zeokeel/main.go` 只调 `app.Run()`（GUI + 单实例 + 托盘） | 无人值守进化只能靠外部 Agent 或改代码 |
| G4 | **无 issue backlog / 指标 / 审计** | 没有 `.evolve/`、scoreboard、变更单 | 无法衡量「进化速度」，也无法归因 |
| G5 | **门禁与文档脱节** | PLAN §10.2 承诺 `pixel-gate.sh` / `lint-style-literals.sh` / `cross-build.sh`，`scripts/` 中不存在；`go` 不在非登录 shell 的 PATH | 外部循环第一步就假失败 |
| G6 | **产品真源文档冲突** | README 同时写「M0–M10 完成」与「尚未开始编码」；STATUS 写「M0 进行中」；PLAN §11 只有 M0–M7；审计文档的「待修」已修但未回写 | 自评审会继承错误现状，结论不可信 |
| G7 | **live UI 无像素门禁、PLAN 阈值未实现** | `TestScreensBaseline` 渲染 static `&App{Screen:…}`；live 用例只 `SavePNG`；`pixeltest` 无 SSIM/ROI/1px 几何断言 | 「1:1」不可证伪，自评审的视觉维度没有地基 |
| G8 | **测试写仓库 artifacts** | 跑 shell 测试会重写 `artifacts/screens|diff|live/` | 自动循环会制造噪声 diff；CI 必须 `ARTIFACTS=/tmp` |
| G9 | **仓库体积失控** | `du -sh .cache` = 26G；`dist/` 86M；根目录两个 ~46MB 二进制 | 每次 worktree/快照/打包成本高，git 必须配好 ignore |
| G10 | **无性能基准** | `grep '^func Benchmark'` = 0 | PLAN §10.3 的预算无法回归 |
| G11 | **无人值守会被审批卡死** | automation turn 用 `context.Background()` 且无 deadline；默认 workspace-write 下 `bash/write_file/edit_file` 都进 approval；`grep -rn 'WithTimeout\|WithDeadline' internal --include='*.go'` 非测试为空 | 「无人值守自修」会永远停在 `waiting_approval`，直到人点按钮 |
| G12 | **门禁存在「缓存假绿」** | `Makefile:10` 是裸 `go test ./...`；同目录重复跑第二次 0.62s、17 个包全 `(cached)` | Agent 会以为「刚修好就全绿」，实际没跑 |
| G13 | **存在死代码与文档漂移** | `store.AppendTranscript/ReadTranscript`、`tool_calls` 表生产零调用，而 PLAN §9 称 JSONL 为唯一真相；覆盖率：session 56.3%、app 51.6%、cmd 0% | 自进化若基于「文档说的持久化」做决策会踩空 |
| G14 | **危险清理命令** | `Makefile:28-29` `clean: rm -rf .cache build dist`，而 `.cache` 有 ~25GB Go 构建缓存 | 误执行会让 CI/本地全量重编，自动循环必须禁用 |
| G15 | **门禁会污染仓库（已修）** | `scripts/check-nocgo.sh` 交叉编译不写 `-o`，把仓库根 `zeokeel` 覆盖成最后的目标（darwin/arm64）；像素测试默认写 `artifacts/` | 自修复的产物与门禁产物混在一起，diff 噪声大 |

### 2.3 今天第一次「自体检」的产出（第一版 backlog）

> 注：本轮的「自体检」不是人工走查，而是跑脚本 + 对照代码。**第 4、5 项已在本次交付中修掉并验证**，其余进 backlog。

1. **P0** 修复门禁 flake：`TestExperimentalSwitches`（全量时 2/3 失败、隔离 0/20 失败，疑似测试间状态污染/时序依赖）与 `TestScreensDarkNarrow`（首跑失败、重跑通过；窄屏采样点 `(300,50)`/`(300,750)` 靠近文字，字体回退时可能命中字形）。→ 修法与「闸门重跑 + 采样区域化」绑定，见 §5.1。
2. **P0** `git init` + 基线 tag + `.gitignore` 补 `dist/`、`zeokeel`、`zeokeel.exe`、`.evolve/state/`（后两项已在本次落盘 `.gitignore`）。
3. **P0** 把 `go` 加进非登录 shell PATH（或 Makefile/脚本里显式 `PATH=/usr/local/go/bin:$PATH`），否则 `make check` 直接 127。
4. **✅ 已完成（P1）** `scripts/evolve/gates.sh` v0：统一门禁 + `-count=1` + flake 重跑 + `ARTIFACTS=/tmp` + JSON/Markdown 报告 + 像素 masked/raw 百分比写入 metrics。
5. **✅ 已完成（P0）** 修 `scripts/check-nocgo.sh` 的交叉编译回写缺陷：交叉编译产物写 `mktemp -d` 临时目录，验证跑完后仓库根 `zeokeel` 的 sha256 不变（见 §12）。
6. **P0** 无人值守策略：给 self-evolution 专用会话加权限白名单/自动审批 + turn deadline + run 记录（`automation_runs` 表），否则 P0-2 卡死。
7. **P1** 单一状态真源：README/PLAN 只链接 STATUS；STATUS 重写为「已实现 / 已验证 / 未达标 / 计划」四段；审计文档加「最后核对时间 + 命令」。
8. **P1** 补 live 像素门禁与 PLAN 阈值（SSIM/ROI/1px）——这是产品自评审的客观地基。
9. **P1** headless CLI（`zeokeel run/evolve`）+ 产品级 headless e2e（fake provider，不依赖 GUI）。
10. **P1** `make check` 固定 `-count=1`；补 `AGENTS.md` 与 `.zeokeel/skills/{audit,fix,verify,review}/SKILL.md`（仓库知识层现在是空的）。
11. **P2** 补 3 个缺失的 PLAN 门禁脚本；把 `ARTIFACTS` 默认指到临时目录。
12. **P2** 补性能 benchmark（启动/首帧/滚动/渲染）；清理/迁移 `.cache`（25GB）与 `dist/`（86MB）。

---

## 3. 四条可选路径（回答「有什么办法」）

| 方案 | 形态 | 工作量 | 风险 | 适合 |
| --- | --- | --- | --- | --- |
| **A. 外部闭环（推荐第一步）** | 夜间 cron / 手动触发 → 跑门禁 → 生成 issue → 外部编码 Agent（DSH/Claude Code）在 git worktree 里修 → 门禁验证 → 输出 patch/分支 → 人类 review 合入 | 2–3 天（脚本 + 流程 + 提示词） | 低（不动产品代码；Agent 由外部托管） | 研发期维护 ZeoKeel 仓库 |
| **B. 产品内置 `zeokeel evolve`** | 新增 headless 子命令：读 store/settings → 构建 runtime.Agent → 跑信号采集/分诊/修复/验证 → 写 SQLite；`internal/automation` 定时触发；默认 dry-run | 1–2 周（含 services 解耦、schema、审计） | 中（无人值守 + 审批/权限设计） | 个人版「Agent 自己维护自己」；可离线/私有 |
| **C. 会话内自驱** | 用户开一个会话，用 `goal`/`ralph`/`subagent` 让 Agent 自我迭代；`skills` 沉淀经验 | 0.5 天（提示词 + 模板） | 低 | 演示、临时任务、无 cron 环境 |
| **D. 在线进化搜索** | Agent 改自身代码/提示词，按自评分自动采用（DGM/AlphaEvolve 式） | 数周且不可控 | 高（无沙箱、不可审计、跨平台桌面） | ❌ 不做；最多做离线实验 |

**推荐组合：A（现在）→ B（产品化）→ C（用户侧入口）→ D（只做离线研究）。**

理由：
- A 复用现有 542 个测试与像素门禁，2–3 天就能产生第一批真实修复；风险在仓库外。
- B 把同一套流程搬进二进制，符合「单进程、单二进制、无服务端」硬约束；先 CLI 后 GUI，避免破坏 1:1 对齐面。
- C 用现有 `ralph`/`goal`/`skills` 即可，适合个人版「我的 Agent 自己变强」，不依赖 git。
- D 的安全边界（内核沙箱、隔离评测、人类 merge）在桌面单机上不成立。

---

## 4. 总体架构（B 形态的正式设计）

### 4.1 组件

| 组件 | 职责 | 落点（建议） | 复用 |
| --- | --- | --- | --- |
| `Signals` | 跑所有确定性检查，产出结构化 finding | `internal/evolve/signals.go` + `scripts/evolve/gates.sh` | Makefile、go test、pixeltest、check-nocgo、e2e |
| `Triage` | 归一化/去重/严重度/复现命令/分派 | `internal/evolve/triage.go` | store（fingerprint 去重） |
| `Repair` | 每个 finding 一次尝试：worktree → Agent 回合 → 取 diff | `internal/evolve/repair.go` | `runtime.Agent`、`session.Controller`、`git worktree` |
| `Verify` | 跑「定向测试 + 全量门禁」；签出裁决 | `internal/evolve/verify.go` | `scripts/evolve/gates.sh` |
| `Review` | rubric 采集 + judge 协议 + 报告 | `internal/evolve/review.go` + `scripts/evolve/review.sh` | provider、baseline、pixeltest |
| `Learn` | 反思、技能沉淀、评测集增长、评分板 | `internal/evolve/learn.go` | `internal/skills`、store、prompt |
| `Store` | append-only 审计与状态 | `internal/store`（v2 schema）或 `.evolve/state/*.jsonl`（v0） | SQLite |

### 4.2 状态机

```
detected ──▶ triaged ──▶ attempting ──▶ verified ──▶ proposed ──▶ (human) merged
                │              │             │
                │              └─ failed ───▶ abandoned（带失败原因/轨迹）
                └─ duplicate ─▶ merged into canonical finding
```

每个状态转换都写审计事件（JSONL）：`{ts, run_id, finding_id, state, actor, evidence}`。

### 4.3 为什么 Agent 只「提案」不「裁决」

外部调研的关键结论：无外部反馈的自纠正会退化（LLM 不能可靠自评），有测试门禁时才是净收益（Self-Debugging 最多 +12%；Agentless 用简单三阶段在 SWE-bench Lite 达 32.00%）。因此：

- **模型**：提出候选补丁、解释失败原因、写回归测试；
- **门禁**：决定候选是否被接受（编译/测试/像素/nocgo/跨平台）；
- **人类**：决定变更是否进入主线、是否改产品口径。

---

## 5. 三条闭环的详细设计

### 5.1 缺陷闭环（Self-Repair）

#### 信号清单（第一批）

| 信号 | 命令 | 耗时量级 | 稳定性 | 机器可读 |
| --- | --- | --- | --- | --- |
| 格式 | `gofmt -l ./cmd ./internal` | <1s | 高 | 是（路径列表） |
| 静态检查 | `go vet ./...` | ~5s | 高 | 是 |
| 构建 | `CGO_ENABLED=0 go build ./...` | ~10s | 高 | 是 |
| 测试 | `CGO_ENABLED=0 go test -count=1 ./...` | ~40s | 中（有 flake，见 G2） | 是（go test 输出） |
| 约束 | `sh scripts/check-nocgo.sh` | ~1–2min（四目标交叉编译） | 高 | 是（PASS/FAIL） |
| 浅色像素 | `ARTIFACTS=/tmp/... go test ./internal/shell -run TestScreensBaseline` | ~18s | 中 | 是（masked/raw 比例） |
| 深色/窄屏 | `... -run TestScreensDarkNarrow` | ~2s | 中 | 是 |
| 真实模型 E2E | `sh scripts/e2e-model.sh` | 30s+ | 依赖凭据/网络 | 部分（需脱敏摘要） |
| 覆盖率 | `go test -cover ./...` | ~40s | 高 | 是 |
| 文档真实性 | 自定义脚本（声明→证据） | <1s | 高 | 是 |
| TODO/死代码/吞错误 | `grep`/AST 检查 | <5s | 高 | 是 |
| 性能 | 需先补 benchmark | — | — | —（G10） |

新增门禁（建议）：`staticcheck`、`govulncheck`、`go test -race`（夜间）、`go test -fuzz`（限时）、链接检查、`/compact` 类死命令扫描。

#### 分诊规则

1. **去重**：`fingerprint = hash(source + 测试名/文件 + 错误摘要)`；同 fingerprint 合并，计数 +1。
2. **严重度**：S1 = 门禁红 / 崩溃 / 数据丢失；S2 = 用户可感知功能/体验缺陷；S3 = 文档/卫生/一致性。
3. **可复现性**：失败先重跑 3 次——3/3 失败 → 确定性缺陷；1–2/3 → 标记 `flaky`，列入「门禁健康」专项（**不得**为过测试而改测试）。
4. **优先级** = 严重度 × 影响面 ÷ 修复成本（初版用规则表，不必上模型）。

#### 修复协议（硬规则）

| 规则 | 值 | 理由 |
| --- | --- | --- |
| 隔离 | 每次尝试一个 `git worktree`（`/tmp/zk-evolve/<finding>/aN`） | 不污染主树，失败即丢 |
| 先复现 | 必须先用 finding 的 repro 命令复现失败 | 防「猜着修」 |
| 改动白名单 | 由 finding 声明（如 `internal/shell/**`）；测试/门禁/CI/阈值文件永久只读 | 防 reward hacking |
| 补丁预算 | ≤200 行、≤5 文件；超出转人工 | 降低回归面 |
| 尝试次数 | 每个 finding ≤2 次；失败即 abandoned | 防「试到过测试」 |
| 验收 | 定向测试 PASS + 全量门禁（含像素）PASS + 新增回归测试 | 真正「修好」 |
| 审计 | 记录 diff hash、门禁输出、耗时、成本 | 可归因 |
| 采纳 | 输出分支/补丁 + 变更说明，**等人 review** | 人类保留最终控制 |

特殊规则：**门禁文件（测试、`scripts/`、`.github/`、阈值常量）被修改时，本次尝试直接判失败并升级 S1**——这是整套体系的生命线。

#### 门禁健康（防 G2）

- 失败重跑 3 次，记录 flake 率；
- flake 率 >2%/周 → 自动开「门禁健康」S1 issue，暂停自动修复；
- 每周做一次**灵敏度自检**（TST-07）：人为把某个像素/令牌/容器挪 1–2px，确认门禁必红，再恢复；门禁不红 = 比 bug 更严重的问题。
- 采样类断言（单像素）改为**区域主色/多数色**，避免字体回退/AA 抖动导致假失败。

### 5.2 产品/设计闭环（Self-Review）

#### 评分框架（来自 03 报告，37 项）

| 维度 | 权重 | 项数 | 客观/主观 | 主要证据 |
| --- | --- | --- | --- | --- |
| 产品功能 PF | 25% | 8 | 多为 auto | 功能矩阵 × 代码 × 测试 |
| 交互 IX | 20% | 9 | auto + human | `ui.NewTester` 用例、快捷键表、错误态 |
| 视觉 VIS | 30% | 8 | auto | 像素/几何/SSIM/对比度/设计系统 |
| 文档真实性 DOC | 15% | 7 | auto + human | 状态真源、声明→证据、数字复现 |
| 可测试性 TST | 10% | 8 | auto | 门禁、金字塔、e2e、基准、CI |

执行方式：**能客观测的一律进测试/脚本**（像素、几何、对比度、键数、工具数）；**只剩偏好/文案/层级**才交给 judge。

#### judge 协议（防自嗨）

1. rubric 先于打分，每项给判定标准 + 反例；
2. blind 成对比较 + 交换 A/B 位置各评一次；
3. 每次混入 1–2 个已知好/坏 canary，judge 过不了 canary 则本次评审作废；
4. 多 judge 投票，分歧上报；
5. 温度 0、固定 rubric 版本 + 模型快照；
6. 人类抽检 10–20%，记录 judge-human 一致率；一致率下降即冻结；
7. judge 结果**只用于提案**，不直接进发布门禁。

#### 输出物

每轮评审产出：`runs/<date>/`（命令日志、diff 图、rubric CSV、评审报告）+ 更新 `docs/STATUS.md`（唯一状态真源）+ 新 findings 进入 §5.1 backlog。

**人类闸门**：像素阈值/白名单 owner、字体策略、static/live 双轨、深色是否 v1 交付、Add plugin 形态、字标、里程碑口径、降级清单披露、e2e 凭据策略、目标平台（详见 03 报告 §4 的 10 项）。Agent 只给证据与建议。

### 5.3 元闭环（Meta / Learn）

| 机制 | 做什么 | 门控 |
| --- | --- | --- |
| 反思记忆（Reflexion 式） | 失败→修复成功的因果写成短反思，按任务签名召回 | 只从「门禁通过」的轨迹蒸馏；带 provenance；有效期/版本 |
| 技能库（Voyager 式） | 把可复用流程写成 `SKILL.md`，放 `internal/skills` 的 learned 目录 | 写入前跑一次回归；失败率超阈值自动禁用；带来源/时间/验证结果 |
| 评测集增长 | 每个修复必须新增回归测试；定期补 held-out 用例 | held-out 不参与优化，只用于验收 |
| 提示词离线优化（GEPA 思路） | 用本地 eval set 做 fitness，reflective mutation 生成候选 system prompt / 工具描述 | 仅开发期；人工 review 后合入；held-out 下降即拒绝；版本化 |
| 评分板 | 趋势指标（见 §8） | 每日/每周快照；异常自动开 issue |

**经验值**：先做「反思 + 回归测试增长」这一层就够；技能库与提示词优化在 L2 稳定后再上。

---

## 6. 安全网（不可协商）

| # | 规则 | 落地 |
| --- | --- | --- |
| 1 | 主树永远可构建/可测试；所有实验在 worktree | `git worktree add /tmp/zk-evolve/<id>`；主树只接受过门禁的变更 |
| 2 | 测试、门禁脚本、CI、阈值、`.gitignore` 是「宪法文件」，Agent 只读 | diff 命中即拒绝 + S1 升级 + 人类确认 |
| 3 | 每个 finding ≤2 次尝试，每次补丁 ≤200 行/5 文件 | 超限转人工 |
| 4 | 预算硬上限（token/时间/夜间次数） | settings 键 + 超限暂停调度 |
| 5 | 一键 kill switch | `self_evolution.enabled=false` 同时停 repair/scheduler/prompt 写入 |
| 6 | 自动变更默认 dry-run：先「变更提案」再采纳 | 提案 = diff + 验证记录 + 影响面 |
| 7 | 人类闸门清单（产品/设计/依赖/schema/权限/发布）不可自动化 | 见 §5.2 与 §11 |
| 8 | 审计 append-only | JSONL/SQLite：信号→尝试→diff hash→门禁→采纳/回滚 |
| 9 | 人类抽检 10–20% | 修复与 judge 结论都抽检；一致率下降即冻结 |
| 10 | 每周门禁灵敏度自检 + flake 率监控 | 见 §5.1；不通过则暂停自动修复 |

---

## 7. 工程落点与实施顺序

### 7.1 P0：安全网 + 最小信号采集（0.5–1 天）

- `git init -b main`；`.gitignore` 追加 `dist/`、`zeokeel`、`zeokeel.exe`、`.evolve/state/`、`.evolve/runs/`；首次提交后打 tag `baseline-2026-11-04`。
- `scripts/evolve/gates.sh`：统一门禁入口（PATH 自愈、flake 重跑、JSON+Markdown 报告、`ARTIFACTS` 隔离）——本提案已附实现。
- `.evolve/` 目录：`state/`（机器可读）、`runs/`（每轮日志）、`research/`（调研）。
- 用 `gates.sh` 产出第一份 `findings.json`（本轮 backlog，见 §2.3）。

### 7.2 P1：外部闭环（2–3 天）

- `scripts/evolve/run.sh`：采集 → 分诊 → 取 top-N → 对每个 finding 起 worktree → 调 Agent（外部 CLI）→ 跑门禁 → 生成 `proposals/<id>.patch` + 报告。
- Agent 提示词模板：`docs/evolve/prompts/{triage,repair,review}.md`（含硬规则、白名单、diff 预算、禁止改测试）。
- 夜间触发：`cron`/`systemd timer`（研发机），或手工 `run.sh`。
- 出口标准：连续 5 个真实 finding 能自动产出合规提案；误报率 <20%；无一例门禁文件被改。

### 7.3 P2：产品内置 `zeokeel evolve`（1–2 周）

- `cmd/zeokeel` 增加子命令（保持 GUI 默认行为不变）：`zeokeel evolve --once|--watch|--report [--dry-run]`。
- `internal/evolve`：Signals/Triage/Repair/Verify/Review/Learn/Score 七个小包；复用 `session.Controller` + `runtime.Agent` + `store`。
- 解耦点：把 `app.BuildServicesWith` 从 GUI 生命周期里抽出来（现在 `app.run` 里建窗 + 服务，headless 只需服务）；审批 gate 在无人值守下用白名单 + `permission=full`，并把危险工具排除。
- store schema v2（append-only）：`evolve_runs`、`findings`、`attempts`、`reviews`、`learnings`、`metrics_daily`。
- `internal/automation`：新增内置任务模板（夜间自体检 / 每周产品评审），`OnFire` 走 `evolve` 而不是建普通会话。
- 出口标准：单机无人值守跑 1 周；提案产出 ≥10 个；回归率 0；成本可读。

### 7.4 P3：学习层与 GUI（2–4 周）

- live 视图像素门禁 + PLAN 阈值（SSIM/ROI/1px）先补齐（产品评审的地基）。
- `internal/evolve/learn`：反思表、learned skills、eval suite 版本化。
- Automation 页新增「Self-evolution」卡片：最近运行、评分板、提案列表、一键暂停；默认隐藏/实验性。
- 离线提示词优化（GEPA 思路）只在有稳定 eval set 后启动。

### 7.5 建议的数据模型（v2，append-only）

| 表 | 关键列 | 用途 |
| --- | --- | --- |
| `evolve_runs` | id, kind, trigger, started_at, ended_at, status, budget_json | 每轮运行 |
| `findings` | id, run_id, source, kind, severity, fingerprint, repro, evidence_path, status | 缺陷/评审项 |
| `attempts` | id, finding_id, attempt, worktree, diff_hash, gate_json, cost, ts | 修复尝试 |
| `reviews` | id, run_id, rubric_version, artifact, judge_model, verdict, canary_ok, human_spot | 评审记录 |
| `learnings` | id, kind, text, provenance, status, created_at | 反思/技能/经验 |
| `metrics_daily` | day, name, value | 评分板 |

v0 可先用 `.evolve/state/*.jsonl`，避免改 schema 阻塞验证。

---

## 8. 指标（怎么判断「真的在进化」）

| 指标 | 定义 | 初版目标 |
| --- | --- | --- |
| 门禁通过率 | 每日全量门禁 PASS 比例 | ≥95% |
| Flake 率 | 重跑才通过的用例比例 | ≤2%/周 |
| 自动修复成功率 | verified / attempted | ≥40%（保守起步） |
| 回归率 | 采纳后 7 天内新门禁失败 | 0 |
| 误报率 | 非确定性 finding 比例 | <20% |
| MTTR | finding 从 detected 到 merged | 逐季下降 |
| 单次修复成本 | token/时间 | 设上限，不追求越低越好 |
| 覆盖率趋势 | `go test -cover` | 不下降 |
| 视觉回归 | 五屏 masked/raw diff | 不上升；live 门禁上线后按预算 |
| 评审分数 | 37 项 rubric 加权 | 2.4 → 3.5（3 个月） |
| judge-human 一致率 | 抽检一致比例 | ≥80%，低于即冻结 judge |
| 进化速度 | 采纳的改进/周 | 前 4 周 ≥3/周 |

**反指标**：测试数量暴涨但覆盖率不涨、门禁预算被悄悄放宽、跳过用例增多、diff 集中在测试/门禁文件——任一出现即触发审计。

---

## 9. 里程碑与工时（2 人估算）

| 阶段 | 内容 | 工时 | 出口标准 |
| --- | --- | --- | --- |
| E0 | 安全网 + `gates.sh` + backlog | 0.5–1 天 | git/tag 就绪；`gates.sh` 可复现；findings.json 生成 |
| E1 | 外部闭环（脚本 + 提示词 + 夜间跑） | 2–3 天 | 连续 5 个真实 finding 的合规提案；0 次门禁文件被改 |
| E2 | `zeokeel evolve` headless + schema + 调度 | 1–2 周 | 无人值守 1 周；≥10 提案；回归 0 |
| E3 | 产品评审 harness + live 像素门禁 | 1–2 周 | rubric 自动项可跑；评审报告进 STATUS |
| E4 | 学习层 + GUI 卡片 + 离线 prompt 优化 | 2–4 周 | 技能复用/反思命中率有数据；GUI 可暂停/回滚 |

依赖：E1 依赖 E0 的 git 与 gates；E3 依赖产品侧先定 live 门禁；E4 依赖 E2 的 eval/指标。

---

## 10. 风险与反模式

| 风险 | 触发信号 | 缓解 |
| --- | --- | --- |
| Reward hacking（改测试/放宽阈值/跳用例） | diff 命中测试或 `scripts/` | 宪法文件只读 + 灵敏度自检 + 新回归测试强制 |
| 假失败导致反复修 | flake 率上升 | 重跑 3 次 + flake 专项 + 采样点区域化 |
| 回归 | 采纳后门禁红 | worktree + 全量门禁 + 7 天观察 + 回滚 |
| 成本爆炸 | token/时间超预算 | 预算上限 + 夜间批量 + 小模型预筛 |
| 文档继续漂移 | 声明→证据不可追溯 | DOC-01/02 自动项 + STATUS 单一真源 |
| 自评自嗨 | judge 分数与客观门禁背离 | judge 只做偏好；canary；人类抽检 |
| 无人值守失控 | 危险命令/越权 | 白名单 + 权限模式 + kill switch + 默认 dry-run |
| 技能/反思污染 | 失败轨迹被沉淀 | 只从通过 verifier 的轨迹蒸馏；版本化；失败自动禁用 |

---

## 11. 需要人类拍板（与 03 报告 §4 对齐）

1. 自进化范围：只研发期（A），还是产品内置（B）？默认开启还是 opt-in？
2. 是否接受「自动修复只到提案、不自动 merge」作为长期边界？
3. 门禁阈值/白名单的 owner 与到期日谁签？深色 23–31% 是否算未达标？
4. 字体策略：系统字体栈 vs 打包 OFL 字体（决定像素门禁可复现性）。
5. static/live 双轨：退役 static 假屏，还是给 live 另建 golden？
6. Add plugin 最终形态、字标收口（PLAN 内部矛盾）。
7. 真模型 e2e 的凭据/CI 出网策略。
8. 里程碑口径：PLAN §11（M0–M7）还是 STATUS（M0–M10）为准？
9. 无人值守修复的权限白名单与危险命令策略。
10. 是否允许仓库引入 `git` 之外的 CI（GitHub Actions / 本地 runner）。

---

## 12. 附：v0 已落地的最小实现与验证证据

### 12.1 `scripts/evolve/gates.sh`（Signal collector v0）

- 作用：一条命令跑全部确定性门禁，产出机器可读报告，作为自进化循环的「信号层」。
- 已实现：PATH 自愈（`/usr/local/go/bin`）、`go test -count=1`（消除缓存假绿）、失败自动重跑（默认 3 次，重跑才通过记为 `flaky`）、像素测试 `ARTIFACTS=/tmp`（不污染仓库）、把五屏 masked/raw 百分比写进 JSON `metrics`、`--quick/--full`（full 含 C1 no-cgo + 四目标交叉编译）。
- 实测 full run（run `20261104-190753`）：总 **43s**；fmt/vet/build/test 21s/pixel-light 2s/pixel-dark 3s/nocgo 11s；全部 `pass`、无 flaky。
- 产物：`.evolve/state/latest.json`（schema `zeokeel.evolve.gates/v1`）、`.evolve/state/latest.md`、`.evolve/state/logs/<run>-<step>.log`。
- 依赖：`bash` + `python3`（仅用于 JSON 汇总；产品运行时不依赖它）。
- 已知边界：`--quick` 不含 C1 跨平台；真模型 e2e 未纳入（需要凭据/网络）；flake 只重跑、不自动归因（归因属于 P1 triage）。

### 12.2 已修掉的门禁缺陷：`check-nocgo.sh` 回写仓库

- 症状（01-audit P0-4）：四目标交叉编译不写 `-o`，每次构建都写仓库根同名文件，最后一个目标（darwin/arm64）覆盖宿主机 linux 二进制；实测仓库根 `zeokeel` 变成 Mach-O arm64。
- 修法：交叉编译输出到 `mktemp -d` 临时目录，`trap` 清理；仅保留「四目标 ok + PASS」输出。
- 验证：修复后跑 `sh scripts/check-nocgo.sh`，前后仓库根 `zeokeel` 的 sha256 **完全一致**（`6ea95eac…b012d5`），四目标 PASS；重建宿主二进制后 `file zeokeel` = ELF 64-bit x86-64。
- 意义：自进化循环的构建/门禁不再污染主树；「主树永远干净」这条安全网前提成立。

### 12.3 第一轮信号（2026-11-04）

- 门禁：本轮 4 次 full/quick 全绿（43–74s），但当天早些时候观测到 flake（`TestExperimentalSwitches` 全量 2/3 失败、单跑 20/20 通过；`TestScreensDarkNarrow` 首跑失败、重跑通过）→ backlog #1。
- 视觉：浅色 masked `0.86/6.27/2.80/2.49/0.82%`；深色 `9.26/23.15/3.14/5.03%`；窄屏 `11.14/31.47%`；`session/narrow 31.47 vs 预算 32.0` 余量仅 0.53pp（01-audit P1-5）。
- 文档：README/STATUS/PLAN 状态口径冲突、`baseline/VERSION` 缺失、3 个 PLAN 承诺门禁脚本不存在（03 报告 DOC 维度 1.0/5）。
- 仓库安全：`.gitignore` 已补 `dist/`、`/zeokeel`、`/zeokeel.exe`、`.evolve/state/`、`.evolve/runs/`；**git 尚未初始化**（P0，等拍板）。

---

## 参考

- 外部调研：`.evolve/research/02-external.md`（Self-Debugging、Reflexion、SWE-bench+/Agentless、GEPA、Voyager、AlphaEvolve/DGM、JudgeBench、CriticGPT 等，31 个 URL，A/B/C 证据分级）
- 现状审计：`.evolve/research/01-audit.md`
- 产品评审：`.evolve/research/03-product-review.md`（37 项 rubric、10 条文档抽查、Top10 缺口、10 个人类拍板项）
- 原有规划：`docs/PLAN.md` §1.2/§5.9/§10/§11、`docs/STATUS.md`
