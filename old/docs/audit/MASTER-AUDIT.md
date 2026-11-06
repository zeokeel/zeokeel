# ZeoKeel 全量审计报告（2026-11-05 · 智能体团队审计）

> **审计对象**：`/data/mygo`（ZeoKeel：纯 Go 桌面 AI Agent，mygo 原生 UI + eino 运行时；目标 = 功能对齐 DSH 的全功能 AI Agent 桌面端）
> **审计团队**：Lead（总控/交叉复核）+ 5 名审计员（功能对齐 / 运行时安全 / UI·UX / 架构工程 / 产品完整性）+ 2 名独立验证员（后端核心 / UI·产品，对抗性复核）
> **审计基线**：开工 `HEAD=7eebe47`；审计期间并发开发进程推进到 `27d5d5f`→`20f1399`→`3111614`（详见 §1.4）；各分报告标注了自己的核验 commit
> **方法**：代码全量阅读 + 可运行探针（9 个安全/正确性探针）+ Xvfb 真跑 GUI（xdotool/import 驱动，90+ 张运行截图）+ DSH 0.2.1-rc.2 已安装包文档对照（305 包）+ 独立验证员复核 P0/P1
> **原始发现**：**97 条**（P0×1 / P1×29 / P2×55 / P3×12），去重合并后约 **84 条独立问题**；正文按维度聚合，逐条证据见分报告
> **独立验证**：51 条 P0/P1（含反向结论）复核 → **CONFIRMED 51 / PARTIAL 5 / 整条 REFUTED 0**（另有 2 个子项被推翻并已在正文/附录修正）；P0 唯一一条（RT-01）独立复现成立
> **交付物**：本文件 + 5 份分报告 + 2 份独立验证报告（索引见附录 A）
> **限制**：无 Windows/macOS 真机；Xvfb 无窗口管理器/托盘/通知服务/IME/桌面 a11y；真实模型仅少量回合（多数 UI 态用本地 fake provider）；DSH 侧仅对照 README 文档未读源码；审计期间仓库被并发开发修改（见 §1.4）

---

## 0. 一页结论

### 0.1 总评

**ZeoKeel 是一个"核心已经真实跑通、产品化只完成约六成"的早期桌面 Agent。** 好消息是：agent loop、工具循环、审批、多会话、压缩、checkpoint、goal driver、plan mode、auto-review、MCP、skills、定时任务、子代理/团队、多模态、fork/export 都**真实存在且有测试**；正常路径的工程质量（路径沙箱、SSRF 防护、进程组治理、SQLite 打开路径、不可变快照）高于同类早期项目。

**但它现在还不能作为"全功能 AI Agent 桌面端"发布**，原因不是"画界面不够像 DSH"，而是四条硬伤：

1. **1 个 P0 安全漏洞**：把克隆来的仓库设为 workspace 即触发 `<workspace>/mcp.json` 里的任意命令执行——**完全绕过审批**（RT-01，已独立复现）。
2. **权限语义不闭环**：后台/自动化回合不按会话自身权限重建 Agent（Full 会话后 automation 继承"无审批"）；"只读"仍能写 automation/memory/goal（RT-02/RT-03）。安全承诺与实际行为不一致。
3. **失败反馈系统性静默（键盘路径）**：无模型/网络失败时按 Enter 发送，用户只看到"跳到一个空会话"，无 toast、无错误、消息不落库（UX-01/UX-03/PD-01，已两路独立复现；点 Send 按钮有 toast，静默仅发生在键盘 Enter 路径——这是最常用路径）。这是新用户第一天就会踩的"应用坏了"体验。
4. **产品化外壳缺"桌面产品六条生命线"**：首启引导、错误恢复、设置与数据管理、自动化可信度、三平台分发/更新、生态集成（ACP/SDK/headless/迁移）均存在 P1 缺口；"DSH 桌面替代/迁移器"的定位**没有任何迁移实现支撑**。

### 0.2 五维评分（主观但基于本报告证据）

| 维度 | 评分 | 一句话依据 |
| --- | --- | --- |
| **功能对齐 DSH** | **~60%**（工具数量 85–90%、工具语义 55–65%、编排/会话/交付 45–55%） | 48 项能力矩阵：✅15 / ⚠️22 / ❌11；workflow/PTC/subagent/preset 都是"语义降级版" |
| **运行时正确性与安全** | **3.5 / 5** | 正常路径扎实；1 个 P0 + 7 个 P1（其中 RT-06 已由并发提交修复；信任边界、权限、并发串行、无重试、spill 逃逸为核心） |
| **UI（视觉/布局）** | **4 / 5** | 品牌、深浅主题、对比度、22 张预览整体过关；空态/反馈/状态残留/窄窗有 3 个 P1 级问题 |
| **UX（交互/流程）** | **3 / 5** | 核心会话流程可用；首启、错误反馈、恢复、信息架构（返回 Home/命令面板/侧栏折叠）缺口明显 |
| **工程质量/项目治理** | **3 / 5** | 测试规模与 ADR 纪律好；但无自动化 CI、无崩溃日志、无版本注入、发布链路缺失、文档多处与代码相反 |
| **产品完整性（桌面端）** | **~60%** | 会话/工具/审批/设置/插件/自动化"能用"；配置、错误、恢复、分发、OS 集成、迁移六条生命线未闭环 |

### 0.3 发布判断（建议）

- ❌ **不建议**以"全功能正式版 / DSH 桌面替代"对外发布。
- ✅ **可以**以"技术预览 / 内测版"发布，但发布说明必须写明：仅 Windows unsigned zip、无自动更新、无迁移、自动化仅前台运行、无内核沙箱。
- 正式版发布门槛建议：**P0 全部修复 + 下列 P1 关键项完成**：RT-01、RT-02、RT-03、RT-07、RT-08、UX-01/02/03、PD-01、PD-02、PD-03、PD-04（或改定位）、PD-06、AR-01、AR-02。详见 §9 阶段 0/1。

---

## 1. 审计方法与证据可信度

### 1.1 团队与分工

| 角色 | 覆盖 | 产物 |
| --- | --- | --- |
| Lead | 总控、环境/方法学校正、DAG 与基线管理、交叉复核、总报告 | `MASTER-AUDIT.md`、`00-brief.md`、`06/07` 验证复核 |
| parity-auditor | 305 个 DSH 包 vs ZeoKeel 能力矩阵（48 行） | `01-functional-parity.md`（21 条） |
| runtime-auditor | agent loop/工具/provider/session/store/审批/安全；9 个可运行探针 | `02-runtime-correctness.md`（16 条） |
| uiux-auditor | 静态 22 张预览 + Xvfb 真跑 90 张截图 + 交互/状态残留/a11y | `03-ui-ux.md`（16 条） |
| arch-auditor | 包结构/数据模型/测试/CI/构建发布/文档一致性；go/ast + race + 运行探针 | `04-architecture-quality.md`（22 条） |
| product-auditor | 用户旅程/onboarding/设置/管理面/桌面生命周期/迁移；真跑 GUI | `05-product-gaps.md`（22 条） |
| verifier-core / verifier-ui | 对抗性复核 P0/P1（不信任原报告，自己读代码/跑复现） | `06-verification-core.md`、`07-verification-ui.md` |

### 1.2 发现统计与去重

- 原始 97 条：P0×1、P1×29、P2×55、P3×12；NEW 约 88 条，KNOWN 复核约 9 条。**独立验证后建议采纳 P1 约 25 条**（RT-06 已由并发提交修复；RT-03/FP-02/PD-08 建议降 P2；FP-06/AR-02 定级可议）。
- 同一问题被多维度命中是**有意设计**（交叉验证），本报告已合并；典型重复：单实例锁（RT-12/AR-11/PD-03）、发送静默（UX-01/UX-03/PD-01）、key 明文（RT-11/FP-12/PD-14）、逐 token 持久化（RT-10/AR-06）、文档漂移（FP-20/AR-12..15）、默认窗口过大（AR-18/PD-15）。
- 统计口径：本报告 Top 表只列去重后的"独立问题"；分报告保留各自完整证据链。

### 1.3 独立验证（对抗性复核）

- verifier-core：覆盖 RT-01..RT-08、FP-01..FP-06、AR-01..AR-03 + 每报告 P2 抽样；重点**独立重跑** RT-01 的 RCE 探针；结果见 `06-verification-core.md`。
- verifier-ui：独立真跑复现 UX-01/02/03、PD-01/02/03/06、UX-04 等；并判定每条 UI 问题是"真 bug / 截图方法学假象 / 设计取舍"；结果见 `07-verification-ui.md`。
- 本报告 §2–§7 的 P0/P1 均标注"复核"列；**若验证员判 REFUTED 的条目，以验证报告为准并在本表降级/删除**（截至本文件写出时，验证员结论见 §1.3 补充行）。

> **验证结论摘要**
> - **06-core（后端/核心）**：复核 30 条（P0×1、P1×16、反向校验×3、P2 抽样×10）→ **CONFIRMED 28 / PARTIAL 2 / REFUTED 0**。**唯一 P0（RT-01）独立重跑探针复现成立**（HEAD 仍存在）。两处修正：① RT-05 的"child 并发竞态"未证实（`-race` 无告警、代码自述并发安全），只保留"Teams 成员 gated 工具必失败"；② FP-02（PTC 无代码运行时）属代码自述的**有意设计取舍**，按缺陷定性有夸大，建议降 P2/产品边界。另：**RT-06 已被并发提交 `3111614` 修复**（见 §1.4）。重复项：RT-12≡AR-11、FP-20≡AR-13≡AR-15、FP-12 重试段并入 RT-08；RT-03 建议降 P2 或并入 RT-02。
> - **07-ui（UI/产品）**：复核 28 条（UX/PD 的 P0/P1 + P2 抽样）→ **CONFIRMED 23 / PARTIAL 3 / REFUTED 子项 2**；两份报告**无 P0**。真跑复现 7 项必查项：无模型首启静默（0.2/1.0/3.5s 三帧 + `messages=0`）、toast 根因、空态缺失、popover 残留、单实例锁、Automation 可信度、迁移缺失全部成立。**两处修正**：① 「点 Send 也无反应 / 按钮回调 toast 被丢弃」**不成立**——点 Send 会显示 toast（点击回调在 mygo frame 内），静默仅发生在**键盘 Enter（OnTextKey）路径** → UX-03/PD-01 判 PARTIAL（UX-01 全 CONFIRMED）；② 「Add plugin 死 CTA」**不成立**（弹出边界说明弹窗），真实问题是 4 vs 6 列表不一致 + 实验开关无功能 → PD-08 降 P2。

### 1.4 基线漂移与并发开发（重要方法学）

审计期间有**另一个开发进程**在同一仓库持续提交（`ZeoKeel Agent <agent@zeokeel.local>`），HEAD 从 `7eebe47` 移动到：
- `27d5d5f` build: embed the ZeoKeel icon in the Windows executable（Windows 图标/构建资源，无 Go 逻辑）
- `20f1399` fix: keep a failed turn's error inside that turn（错误归位渲染；本审计未发现该问题）
- `3111614` fix: no fixed 60s overall timeout for streamed model responses（**修复了本报告 RT-06**：去掉整体 60s 上限、加 `provider.timeout_seconds`；无 idle 超时，重试缺失属 RT-08 仍开放）

因此：**分报告的行号以各自核验 commit 为准**；本报告写结论时会同时看"审计基线是否成立"与"当前 HEAD 是否已被并发修复"，并在 P0/P1 表标「仍存在 / 已部分修复」。若并发开发继续，请以最新 HEAD 重跑关键验证（附录 C 给出可复现命令）。

### 1.5 环境限制（未验证≠不存在）

| 限制 | 影响的结论 |
| --- | --- |
| 无 Windows/macOS 真机 | Windows pwsh/GBK/无控制台崩溃、macOS Gatekeeper、三平台安装包均只能静态核对（分报告已标未验证） |
| Xvfb（无 WM/托盘/D-Bus/IME/桌面 a11y） | 托盘菜单、系统通知真实外观、IME、屏幕阅读器、高 DPI、多显示器、窗口装饰未验证 |
| 真实模型受限 | runtime/产品/UI 多数用本地 fake provider（OpenAI 兼容 SSE）验证状态机；真实模型下的 reasoning/usage/超时行为需真机 |
| DSH 仅 README | DSH 能力清单来自 305 个包的 README.zh.md 与包结构，未读其源码实现；"DSH 有 X" 以文档为准 |
| 仅单用户 Linux | 多用户/多数据目录并发（单实例锁）问题由探针暴露，真机复现待验 |

### 1.6 方法学更正（防止把假象当 bug；也记录给后续 agent）

1. **Xvfb 屏幕必须 1600×1000**：应用默认窗口 1600×1000（`app.go:182-184`）。1280×800 屏幕会裁掉 Session composer/footer，看起来像"UI 缺 composer"——这是截图裁剪，**不是 bug**（Lead 一度误判，已验证更正）。
2. **单实例锁是固定端口 49731**（与 `ZEOKEEL_DATA_DIR`/display 无关）：并行跑 app 必须 `unshare -n` 隔离 netns；否则第二个实例直接退出，xdotool 会打在空屏上。
3. **窗口状态恢复**：完全离屏会被 mygo clamp+居中（不是 bug）；真缺口是"部分可见时保留超尺寸窗口"与"首启默认不裁剪"（AR-18）。
4. **"文档说缺失"常是过时信息**：本审计反向纠错了 3 处（auto-review、goal round driver、plan mode 增强均**已实现**，见 §7.2），后续排期不要重复投入。
5. **独立验证阶段又推翻了 2 个子项表述**（已修正正文）：① "点 Send 按钮也无反应"不成立——点 Send 有 toast，静默只在键盘 Enter（`OnTextKey` 在 mygo frame 外执行）；② "Add plugin 是死 CTA"不成立——它弹出无第三方市场的边界说明。说明"同一现象的不同触发路径"必须分别验证，不能以一条路径的观测推断整类行为。

---

## 2. 安全与数据完整性（发布阻塞项）

### 2.1 P0 — 工作区 `mcp.json` 在 agent 构建期自动执行任意命令（RT-01 · NEW · 已独立复现）

- **链路**：`registerAllToolsWithOptions` 在所有权限模式下构建 agent → `connectMCP(root, …)`（`services.go:739`）→ 若无 `mcp.servers` 设置则读 `<workspace root>/mcp.json`（`services.go:1413`）→ 对每个 server `exec.Command(server.Command, server.Args...)`（`mcp/mcp.go:180`）。**没有审批、没有"信任此工作区"提示、没有完整命令行展示；握手失败只写 stderr，UI 无感。**
- **触发**：把克隆的不可信仓库（自带 `mcp.json`）添加/切换为 workspace；启动、切 workspace、改权限、开插件、保存设置等**每次 agent rebuild 都会重执行**。
- **影响**：以用户身份任意 RCE，可读取 `~/.zeokeel` 中的明文 API key 与会话历史、外联、持久化。与"无内核级沙箱"的既定边界无关——这里连**应用层审批**都绕过了，属于"审批之外的第二条执行通道"。
- **修复**（S/M/L）：S = 默认忽略 workspace `mcp.json`（`mcp.allow_workspace_config=false`）+ 首次使用必须展示完整命令/参数/env 键名并确认 + 失败可见 toast；M = workspace trust 持久化（路径 hash/来源/时间）+ untrusted 下不启 workspace MCP/skills、AGENTS.md 不进 auto-review；L = 扩展安装式模型（签名/来源、workspace 仅引用已安装 id、启动审计日志）。
- **证据**：探针 `probes/probe_mcp_test.go`（CONFIRMED：marker 被创建，`connectMCP` 返回 nil error）；报告 `02-runtime-correctness.md` §2 RT-01 附 1/附 2。

### 2.2 权限语义不闭环（RT-02 P1 / RT-03 P1→P2 复核建议 · NEW）

| ID | 问题 | 影响 | 修复 |
| --- | --- | --- | --- |
| RT-02 | 非当前会话/automation 回合不按自身权限重建 Agent：权限在**构建期**闭包进工具集与审批谓词，`submit` 路径从不 `rebuildForPermission`；探针确认"新会话 wants workspace 但 agent 仍是 full、0 次 rebuild" | Full 会话后触发的 automation 回合继承 Full 工具集 + 永不审批 gate → 可在用户不知情时执行 shell/写任意路径/外联；反向则 Read Only 下任务静默失败。UI 标签与实际权限不一致，审计不可信 | S/M：`submit` 前按目标会话权限重建（持 bindMu）；更彻底=每会话独立 Agent（与 RT-04 合并做） |
| RT-03 | Read Only 仍注册 `schedule_create/update/delete`、`memory`、`goal`、`todo` 等持久副作用工具且不审批（探针确认 `needsApproval=false`；**独立复核确认事实成立，但建议降 P2 或并入 RT-02 作为利用链**） | "只读"被弱化为"文件/命令只读"：可在只读会话落 automation（未来在更高权限上下文执行）、写 workspace memory（进入后续 system prompt）、改 goal（触发最多 6 轮无人值守续跑）。现实利用链：只读会话中被网页/工具结果注入 → 落定时任务 → 用户在 Full 会话时触发全权执行 | S：只读注册表按"有无持久/调度副作用"收敛（移除或纳入审批），prompt 政策与工具集一致 |

### 2.3 其他安全/隐私 P1/P2（按风险排序）

| ID | 级别 | 问题 | 一句话影响 |
| --- | --- | --- | --- |
| RT-07 | P1 | 工具结果 spill 跟随 workspace 内 `.zeokeel/tool-results` symlink 写出工作区（探针 CONFIRMED） | 恶意仓库 + 一次大工具输出即可向任意可写路径写可控内容（0600 不阻止新建/截断既有文件） |
| RT-14 | P2 | auto-review 把 workspace `AGENTS.md` 作为"授权证据"交给复核模型 | 不可信仓库可写注入指令削弱"自动审批"（人类替代）边界（未验证模型行为） |
| RT-11 | P2 | `index.sqlite`（含明文 API key）文件 0644（目录 0700 兜底） | 目录权限一旦放宽/备份/同步即泄露 key 与全部会话 |
| FP-12 | P2 | API key 明文存 SQLite settings；无 secret mask（粘贴到用户消息不遮挡）、无重试执行器 | 明文落盘 + 用户消息泄漏面 + 网络抖动整轮失败 |
| PD-14 | P2 | 无凭据管理（keychain/加密/清除/掩码说明） | 企业用户不可接受；号被盗无吊销入口 |
| RT-15 | P3 | 子进程/MCP 环境只 scrub `ZEOKEEL_*`/`DEEPSEEK*`，`OPENAI_API_KEY`/`AWS_*`/`GITHUB_TOKEN` 等原样继承 | prompt-injected 工具可读环境凭据；"承诺 scrub 却只 scrub 自家前缀"给人错误安全感 |
| RT-12 | P2 | 单实例锁 = 固定回环端口 49731，可被本机任意用户抢占 | 启动 DoS（"已在运行"误导）、多数据目录无法并存（审计团队被迫 unshare -n） |

### 2.4 安全修复顺序（建议）

`RT-01`（S，立刻）→ `RT-07`（S）→ `RT-03`（S）→ `RT-02`（S/M，与 RT-04 合并规划）→ `RT-11`（S）→ `RT-08`（S/M）→ `RT-14`（M）→ `RT-15`/`FP-12`（M/L）。**在 RT-01 修复前，不要在公开渠道鼓励用户"把任意仓库拖进 ZeoKeel"。**

---

## 3. 功能缺失（DSH 对照 + 全功能桌面 Agent）

### 3.1 总评与 parity 计分

DSH 0.2.1-rc.2 已安装包 305 个。ZeoKeel 的 **agent 能力面数量接近 DSH（工具数量约 85–90%）**，但**语义完整性明显落后**：

| 层 | DSH | ZeoKeel | 对齐度 |
| --- | --- | --- | --- |
| 工具数量 | ~37+ 工具族 | ~35 工具（71 文件），覆盖除 LSP/computer-use/持久 shell 外大部分 | 85–90% |
| 工具语义 | 原子写/观察策略/持久 shell/代码运行时/结构化编排 | 有降级（write_file 非原子、workflow 固定步骤、PTC 批处理、subagent 同步） | 55–65% |
| 编排 | workflow JS 引擎、PTC Node/TS 运行时、异步 subagent、teams 任务板、ralph | workflow/PTC/subagent 均为降级版；teams 默认关闭 | 40–50% |
| 会话/上下文 | FTS5 检索、session 引用、checkpoint 三屏障、token meter、image offload、workspace changes | LIKE 检索、纯文本引用、回合边界 checkpoint、chars/4 估算、无 diff | 45–55% |
| 产品面 | 三平台分发+更新、插件市场/profile、headless/SDK/ACP、voice、CoT 翻译、浏览器/Office 预览 | 仅内置插件、仅 GUI、无更新/迁移 | 35–45% |

能力矩阵 48 行：**✅ 15 / ⚠️ 22 / ❌ 11**。已有 ✅ 项（agent loop、terminal、jobs、todo、present、plan mode、goal driver、worktree、session titles、auto-review）与 ⚠️/❌ 项明细见 `01-functional-parity.md` 附录矩阵。

### 3.2 编排层"语义降级"（最值得投钱，FP-01/02/03/05/06）

| ID | 缺口 | 现状 vs DSH | 影响 |
| --- | --- | --- | --- |
| FP-01 | `workflow` 不是 JS 编排引擎 | 固定顺序 JSON 步骤（≤8 步、失败即停）；DSH 是模型写的纯 JS（`agent/parallel/pipeline/phase/log` + 结构化 schema） | 无法一次调用并行扇出/条件编排，复杂任务自动化上限被锁死 |
| FP-02 | PTC 模式只是工具批处理 | 无代码执行运行时（DSH 是受管 Node 子进程 + 可擦除 TS + 沙箱/REPL 内存）；旧矩阵标 ✅ 会误导 | "模型写程序做数据加工/循环/聚合"的核心价值缺失。**独立复核：代码自述"有意不嵌入 JS 沙箱"，属产品/安全取舍而非实现缺陷——建议降 P2 或移入"产品边界"，但旧矩阵 ✅ 仍需改正** |
| FP-03 | subagent 仅同步 spawn | 无 childId/异步完成通知/fork/续谈/steer；异步只在默认关闭的 agent_teams 插件 | 多 agent 并行工作流不可用；长任务不能追加指令 |
| FP-05 | 无 workspace changes / 每回合 diff | 全仓无 git diff/turn changes；DSH 有逐回合改动文件、摘要与对比 UI | 编码 agent 的核心反馈闭环缺失：用户无法在 UI 审阅"这轮改了什么" |
| FP-06 | agent preset/persona 不可声明 | 模式硬编码 4 个；DSH 可 YAML 定义工具/提示词/技能/默认模型组合 | 无法定义"代码审查员/文档写手"等专属 agent |

**建议**：先做 FP-05（M，2–3d，反馈闭环）+ FP-03 异步化（M，3–5d）；FP-01/02 要么实现受限执行器（goja/WASM/Node 外挂），要么在文档明确标 ❌ 并给用户预期管理。

### 3.3 会话/上下文/交付能力缺口（FP-04/07/09/10/11/14/15/16/17）

- **FP-04 Goal 半成品**：goal 工具只有 set/get/complete/abandon，无 id/revision/phase/pause/blocked，用户无 `/goal` 入口；round driver 已在但是"模型自设 goal 才能用"。
- **FP-07 schedule 语义降级**：cron 子集、无时区/一次性/间隔选择器；触发时**新建会话**而非投递回原会话；无 UI 提醒列表。
- **FP-09 会话检索**：SQL `LIKE`（标题+正文），无 FTS5/排名/分词；无 `@session` 原子引用（仅 `#<title>` 文本）。
- **FP-10 上下文**：chars/4 估算 token（CJK 偏差）、ContextBudget 硬编码 24000、无图片卸载（image offload）、无 replay-aware token meter。
- **FP-11 checkpoint 仅回合成功边界**：崩溃时回合内已发生的工具副作用无持久记录；DSH 在模型请求/工具副作用前/pre-step 三屏障持久化。
- **FP-14 附件语义**：文本 ≤64KiB 内联 prompt 截断；`AttachmentKindFile`（PDF/zip/xlsx）**被接收、显示 chip、持久化，但重组 prompt 时静默跳过**（模型完全收不到）；DSH 是持久附件 + 只读路径按需读取。
- **FP-15 jobs/team**：job 无完成通知（模型只能轮询）；agent team 无共享任务板/revision/持久成员状态。
- **FP-16 ask_user**：fire-and-forget pending + 结束回合；DSH 是调用内阻塞问答（计时/选项/迟到回复），答案作为工具结果回同一回合。
- **FP-17 skills**：单 root、无文件监视、`/` 面板不列 skills、无 Office 技能；DSH 支持 project/custom/user 多根 + 监视 + UI 触发。

### 3.4 模型与生态集成缺口（FP-12/13/18/19）

- **FP-12 模型供应层单薄**：仅 OpenAI 兼容单 provider，无 provider 预设/模型发现/多 provider 路由/重试执行器/账号登录；API key 明文（见 §2.3）。
- **FP-13 MCP 仅 stdio**：不支持 DSH 的 streamable-http 远端服务器（MCP 生态覆盖约一半）。
- **FP-18 无 headless/CLI/SDK/ACP**：只有 GUI 二进制，无 `zeokeel run "prompt"`、无 JSON-RPC SDK、无 ACP，无法被编辑器/CI 平台驱动。
- **FP-19 代理行为不一致**：`web_fetch` 显式 `Proxy: nil`（SSRF 防护原因），provider/web_search 走环境代理 → "搜索能通、抓取不通"，且无设置项。

### 3.5 产品面功能缺失（详见 §6 与 `05-product-gaps.md`）

- **PD-04 迁移缺失**（与"迁移器"定位矛盾）：无 DSH 会话/credentials/设置导入，无 Import 菜单。
- **PD-05 设置面缺通知/数据与隐私/诊断/代理/更新/重置**；PD-07 MCP/Skills/AGENTS.md 无管理 UI；PD-06 Automation 无历史/失败/绑定；PD-08 插件双界面不一致（4 vs 6）+ 无功能实验开关（P2，"死 CTA"表述已被验证推翻）；PD-10 重启不恢复会话、草稿仅内存；PD-12 桌面生命周期缺失（自启/全局键/多窗口/拖拽）；PD-13 仅 Windows unsigned zip、无签名/更新；PD-19 无批量导出/备份/诊断包；PD-20 无 ACP/SDK/headless；PD-21 通知不可配置、无动作按钮。

### 3.6 「全功能桌面 Agent」必备能力差距清单（对照 DSH 能力地图）

| 必备能力 | DSH 包 | ZeoKeel | 缺口级别 |
| --- | --- | --- | --- |
| 三平台安装包/签名/自动更新 | desktop/安装链 | ❌ 仅 Windows zip | **P1（发布阻塞）** |
| 首启引导 + 模型配置验证 | setup/agent-preset | ❌ 无（且失败静默） | **P1** |
| 编辑器/CI 集成（ACP/SDK/headless） | acp/sdk/headless | ❌ | P2（生态） |
| 迁移（DSH 会话/凭据） | session-log/credentials | ❌（定位矛盾） | **P1** |
| 工作区改动 diff/审阅 | workspace-changes | ❌ | **P1（编码场景）** |
| 会话全文检索/引用 | session-query(FTS5)/reference | ⚠️ LIKE / 文本 | P2 |
| 编排（workflow/PTC/async subagent） | workflow/ptc/subagent | ⚠️ 降级 | P1/P2 |
| 定时任务投递回原会话 | schedule | ⚠️ 新建会话 | P2 |
| 多 provider/凭据管理 | llm-*/credentials | ⚠️ 单 provider + 明文 | P2 |
| MCP http / 插件配置 / skills 管理 | mcp-client/plugin-manager/skill | ⚠️ stdio / 手改文件 | P2 |
| 通知/托盘/全局快捷键/自启 | desktop shell | ⚠️ 基础托盘/无自启/无全局键 | P2 |
| 诊断/崩溃日志/遥测声明 | logging | ❌ 无 | **P1（可支持性）** |

---

## 4. 设计不合理（架构与工程）

### 4.1 运行时/并发模型

| ID | 级别 | 问题 | 影响/建议 |
| --- | --- | --- | --- |
| RT-04 | P1 | **单 Agent + `runMu` 全局串行**：所有会话共用一个 Agent，回合锁覆盖"等待审批"；探针确认两会话回合严格串行 | 后台会话/automation 无法与前台并行；后台回合等审批时**无限期阻塞前台**（高度可疑，未 GUI e2e）。建议每会话独立 Agent（M/L），至少"审批等待不占全局锁" |
| RT-05 | P1 | **Agent Teams 成员无 turnContext**：成员上下文不带 turnContext，任何命中审批的工具（workspace 模式下 bash/terminal/job/worktree/web/mcp__*）必然失败且用户收不到审批请求——团队模式里成员实际只能用只读工具（代码链确认，fail-closed） | 独立复核：child 并发复用"竞态"未证实（`-race` 无告警），不计入影响。建议给成员传入携带 turnContext 的会话级上下文，或明确成员审批策略并在 UI 标注（M） |
| RT-06 | ~~P1~~ **已修复** | provider 整条流 60s 硬超时（无 idle/keepalive） | 长 reasoning 回合必失败且失败回合历史不落盘 → 整回合工具工作丢失。**并发提交 `3111614` 已修复**：默认无整体超时、仅响应头 60s、新增 `provider.timeout_seconds`（保留慢流回归测试；无 idle 超时/重试见 RT-08） |
| RT-08 | P1 | 无重试/退避：429/5xx/瞬断一次即失败整个回合（探针确认只发 1 次请求） | 长回合最后一次调用遇 429 即全丢；建议 provider 层有限退避 + `Retry-After` + 失败回合部分落盘 |
| RT-10 | P2 | 每个流式 token 一次 SQLite 事务 + transcript `fsync`（实测 0.675–0.79ms/事件）+ snapshot O(n²) 拼接 | 长回答 CPU/磁盘写放大；与 RT-13（终态事件可丢）叠加；建议批量持久化 |
| RT-13 | P2 | 终态事件走 `trySend`，满缓冲静默丢弃；审批 `early` 无界 | "已停止"可能被记为"完成"；高负载下状态错误（未复现） |

### 4.2 分层与代码结构

| ID | 级别 | 问题 |
| --- | --- | --- |
| AR-04 | P2 | **`internal/shell` God package**：41.4k 行（占 36%）、直接 import 10 个内部包、225 处直调 Controller，同时承担 UI/交互/业务动作/状态缓存；PLAN 的分层约定被横穿 |
| AR-08 | P2 | UI 线程同步执行 `OpenSession/ListSessions` 等 DB 操作，违反自身"UI 绝不阻塞等 DB"的纪律（大会话/慢盘会冻结） |
| AR-05 | P2 | **两套快照折叠并存**：`state.Projector` 与 Controller 自己的 `views` 各折叠同一事件流（UI 只用后者）；projector 白折叠、drop 计数无人消费、文档说"唯一消费者" |
| AR-20/AR-22 | P3 | 65 个 >100 行函数；每 token 整条 message slice 拷贝 + 全量字符串拼接（O(n²)） |
| AR-21 | P3 | 重复/半死代码：`scrubEnv` 双份、automations DDL 双份、ReadTranscript 生产零调用 |

### 4.3 数据模型与持久化

| ID | 问题 | 影响 |
| --- | --- | --- |
| AR-06 | 逐 token 持久化 + 无保留策略/VACUUM（每事件 DB commit + JSONL fsync，0.675ms/事件；用户样本 2 回合 1593 事件） | 长会话写放大、events 表/JSONL 无上限增长、重开会话全量回放 |
| AR-07 | 排序靠"事件回放重建 Seq"：messages 表无序号列，Seq=0 时静默退化为列表序 | 任何不经过回放的路径（checkpoint/测试/fixture）会改变显示顺序 |
| AR-09 | Schema 演进冻结在 v1：只 `CREATE IF NOT EXISTS` 追加、非事务、无迁移/备份/downgrade；旧库直接拒绝启动 | 未来破坏性 schema 变更 = 用户删库；升级不可控 |
| AR-14 | 真源反转：PLAN 称 JSONL 是"唯一真相"，代码以 SQLite 为准且 JSONL 生产零读取、错误被 `_ =` 吞掉 | 文档/代码互相矛盾；恢复逻辑若按 PLAN 写会得到不一致数据 |
| AR-16 | `docs/BUGS.md` 缺失（QUALITY.md 规定已知缺陷只允许在此），真实 backlog 在 gitignored `.cache/` | 发布前 Critical/Major=0 的判定没有可跟踪载体；清缓存即丢 |

### 4.4 发布与可诊断性

| ID | 级别 | 问题 |
| --- | --- | --- |
| AR-01 | P1 | **发布版零崩溃诊断**：全仓无 `recover()`、无日志文件/轮转；Windows GUI（`-H windowsgui`）丢弃 stderr → 任何 panic 静默退出、无痕迹可报 |
| AR-03 | P1 | 发布链路缺失：仅 Windows unsigned zip；无签名/公证/自动更新/三平台安装包；版本号硬编码 0.1.0（About）/文档称 v1.1/git 无 v1.x tag；build 无 `-ldflags -X` |
| AR-02 | P1 | **CI 只是本地脚本**：无 `.github/.gitlab/Jenkins`、无 git hooks；race 只覆盖 6/20 包（缺 shell/app/prompt/mcp）；coverage 只打印不设阈值；`gates.sh` 自动重跑 3 次把 flake 记成"绿" |

### 4.5 测试与 CI

- AR-17：覆盖率 `store 62.8%（最低）/design 32.6%/cmd 0%`；QUALITY.md 点名的 fuzz（路径穿越/cron/JSON args）全部缺失；`-race` 未覆盖 shell/app/prompt/mcp。
- AR-02 的补充：`CGO_ENABLED=1 go test -race ./internal/shell` 实测 518.8s（默认 10 分钟超时 86%），入 CI 需 `-timeout 20m`——这也是它一直被排除在 CI 外的现实原因，应单独规划。
- 好的一面：211 测试文件/1122 测试函数（shell 86.2%/prompt 95%/runtime 81%/tools 80.9%），无头可跑，`-race state/session/shell` 本轮全 PASS。

---

## 5. UI bug（视觉/布局/渲染）

> 全部在 Xvfb 1600×1000 真跑或 22 张金标预览中复现；截图在 `.cache/audit/uiux/shots/`（关键项 2× 放大）。UI 层总体质量 4/5：品牌/主题/对比度/本地化/审批卡/工具卡都对；问题集中在空态、反馈、状态残留、窄窗。

| ID | 级别 | 现象 | 证据 | 修复 |
| --- | --- | --- | --- | --- |
| UX-02 | P1 | 新会话「对话」tab 全白无空态（同屏「轨迹」/右栏 Files/Log/Automation 都有空态），样式不一致 | shots/25,26 vs 49,51,53,24；`session_live.go` 无空 transcript 分支 | S：复用 Trajectory 空态语法 + 无模型 CTA |
| UX-04 | P2 | 打开 Settings 独立窗口时主窗口 popover 不关闭，被设置窗半遮；关设置后仍在（z-order/状态残留） | shots/42,43,47；`liveMenus()` 未在 openSettings 清理 | S：统一 `closeAllPopovers()` |
| UX-05 | P2 | 「添加工作区」对话框两个输入**无可见 label/placeholder**，无法区分名称/路径 | shots/03,09,10；`workspaces.go:1196-1204` 只有 a11y Label | S：可见 label/placeholder |
| UX-06 | P2 | Settings 独立窗口 900×720 首屏底部行（「忙碌时的发送行为」）被窗口边缘切断、无滚动提示（**金标预览同样裁切**） | preview/settings-window-* + shots/42,43,46；`settings_window.go:220` | S：加底部留白/按内容取整/滚动指示 |
| UX-08 | P2 | 折叠侧栏（56px）后没有任何会话入口，会话列表不可见不可达 | shots/31,34；`chrome.go:146-180` | M：workspace icon hover flyout 或"最近会话"图标 |
| UX-09 | P2 | Home 无工作区时侧栏「工作区」区空白无空态；添加入口只藏在 composer chip | shots/02,03,11 | S：空态行 + 添加按钮 |
| UX-10 | P2 | `@` 提及菜单会话行硬编码英文 `session · `（中文界面混排）；同名会话无区分信息 | shots/50；`composer_menus.go:25-29` | S |
| UX-11 | P2 | 窄窗 900×600：权限 chip 退化为图标+箭头，**当前权限值不可见**（安全相关）；composer/transcript 列宽不一致 | shots/57,80；`home_live_menus.go:188-194` | S |
| UX-13 | P2 | 右栏「开始」页装饰 brand 图标像按钮/加载态、内容垂直居中留白过大 | shots/48；preview/right-sidebar-* | S |
| UX-14 | P3 | toast 固定底部居中遮挡 composer/设置表单行 | shots/11,22,43,84 | S：上移或右上角 |
| UX-07 | P2 KNOWN | 双主按钮/双入口仍成立（Settings>模型、Automation、Plugins 各有两个同权重主操作；round4 P1-8 未修） | shots/23,24,44 | M |
| AR-18 | P2 | 主窗口默认 1600×1000 不适配小屏；"部分可见"旧尺寸（如 3000×2000）原样恢复超出工作区（Xvfb 实测） | `app.go:182-186` + `.cache/audit/arch/window-bounds.txt` | S：创建/恢复时按 WorkArea clamp |

**已复核为"已修/良好"**：P0-1 破坏性确认、P0-2 三级文字 AA、P1-3 深色字标、P1-5 自动化中文化、round4 工具卡对比度、深色主按钮 9:1、22 张预览与运行时一致。**未修且仍未进 known-limitations**：浅色焦点环约 2.7:1（P2-10）、双主按钮（P1-8）、预览缺空态/错误态/弹层/窄窗（P1-7）。

---

## 6. UX bug（流程/反馈/信息架构）

> 这是本次审计中**最影响新用户**的类别。核心根因有四个：(a) 反馈通道不可靠（toast 在键回调被 mygo 丢弃）；(b) 生命周期未闭环（首启/恢复/窗口/单实例）；(c) 配置与信任成本转嫁用户（模型/工作区/MCP/skills 全靠手填手改）；(d) 信息架构缺口（Home 不可返回、无全局命令面板、折叠侧栏丢会话）。

### 6.1 首启与失败反馈（P1 集群）

| ID | 现象 | 根因（已定位） | 修复 |
| --- | --- | --- | --- |
| UX-01/UX-03/PD-01 | 无模型按 **Enter** 发送 → 创建空会话跳 Session 屏、用户消息不落库（`messages=0`）、草稿回填、**0.15s/0.5s/3.5s 均无 toast/错误**；再按 Enter 像素级无变化；侧栏残留空会话。**独立复核修正：点 Send 按钮会显示 toast（点击回调在 mygo frame 内），静默仅发生在键盘 Enter 路径（`OnTextKey` 在 frame 外）；UX-03/PD-01 判 PARTIAL，UX-01 全 CONFIRMED** | `home.go:328/331`、`session_live.go:820/823` 在键盘/按钮回调里直调 `c.Toast`；mygo `Context.Toast` 在 `inFrame=false` 时直接丢弃（`ui/api_gen.go:559-565`）；slash 命令走 `toastText→queueToast→flushDeferredToast` 才可见（`composer_menus.go:361-372`），`/clear` toast 可见作对照 | S：键盘路径统一走 `toastText`；失败不保留空会话（回滚或复用）；S/M：补空态 + 无模型 CTA |
| PD-02 | 添加模型仅 5 个文本框：无 provider 预设、无连通性测试、无模型清单 | `models_ui.go:225-372` 只有文本输入；保存只校验名称 | S/M：预设 + Test 按钮（`/models` 或 1-token 探测）+ 失败分类 |
| PD-09 | 失败回合直接展示 `[NodeRunError] ... node path: [...]` 原始栈，无建议/重试 | provider 错误未分类；UI 直接透传 | S：错误分类器 + 操作按钮（打开设置/重试/复制诊断）+ 技术细节折叠 |

### 6.2 生命周期与恢复

| ID | 现象 | 影响 | 修复 |
| --- | --- | --- | --- |
| PD-03 | 单实例=固定端口 49731；二次启动只 stderr 一行（Windows GUI 不可见）、**不激活已有窗口** | 双击图标"没反应"；端口被占即无法启动；多数据目录不可并存 | S：数据目录锁文件 + IPC 激活已有窗口 + 明确提示 |
| PD-10 | 重启总回 Home，不恢复上次会话；草稿只在内存，退出即丢 | 桌面 Agent 核心期待"回来接着干"缺失 | M：持久化 `ui.last_session` + 草稿落 store |
| PD-11 | 进入会话后**无法回到 Home**（侧栏只有插件/自动化、品牌行不可点） | 新用户建完第一个会话后再也见不到 Home | S：品牌行/侧栏加 Home 入口 |
| PD-17 | 工作区路径不校验存在/可写/是否文件 | 首启必经步骤填错，之后 agent 工具全失败且难归因 | S：创建时 `os.Stat` + 可写探测 |
| PD-18 | 删除模型**无二次确认**（会话/工作区/自动化都有确认弹窗） | 误删需重输 key；破坏性操作策略不一致 | S |
| PD-15/AR-18 | 默认窗口 1600×1000 超出 1366×768/1280×800 笔记本 | 首屏右侧/底部被裁 | S |

### 6.3 信息架构与桌面惯例

- **UX-12** 无全局命令面板；Ctrl+K 在会话内只聚焦 composer（与"命令面板"预期不符）。建议统一命令目录（`/` 注册表复用）或至少会话内打开会话搜索。
- **UX-08** 折叠侧栏无会话入口（见 §5）。
- **PD-12** 桌面生命周期缺失：无全局快捷键、开机自启、多窗口/分屏、文件拖入、托盘快捷动作、关闭行为偏好；托盘菜单英文硬编码且只有 Show/Quit。
- **PD-21** 通知不可配置（无开关/免打扰/分类），无审批动作按钮。
- **PD-16** i18n 收尾：系统默认标题冻结创建语言（英文界面里「新建会话」）、托盘英文、日期未按 locale。
- **PD-22** 文案/命名一致性：「打开配置文件」实际打开「配置与数据库」对话框；Automation 空态文案与"新建"按钮矛盾。

### 6.4 产品面可信度

- **PD-06 Automation 不可信**：失败只写 stderr、store 只有 last_run/next_run、无执行历史；不绑定 workspace/权限/模型 → 多 workspace 下跑错目录、用默认权限裸跑；仅前台运行（关应用即漏跑）。
- **PD-07 MCP/Skills/AGENTS.md 无管理 UI**：普通用户无法完成 MCP 配置；技能格式错只有模型侧报错；AGENTS.md 无入口。
- **PD-08 插件面**（复核后 P2）：独立 Plugins 页 4 个内置 vs Settings 6 个；4 个"实验开关"无功能（voice 明示未内置仍可开）。"添加插件"按钮**不是死 CTA**（弹出无第三方市场的边界说明弹窗，验证已纠正），但列表不一致与假开关仍应修。
- **PD-05 设置面**：缺通知、数据与隐私（key 存储说明/清空数据/导出全部）、诊断日志导出、代理、更新检查、重置。
- **PD-04 迁移缺失**："DSH 桌面替代/迁移器"是目标而非事实。

---

## 7. 项目不合理（文档/流程/发布）

### 7.1 结构性三宗罪

1. **门禁没有自动化载体**（AR-02）：CI 是本地脚本，race/coverage 覆盖不全且可被 flaky 重跑掩盖；"ALL GREEN"是人工声明而非系统保证。发布前 Critical/Major=0 的判定（QUALITY.md）也没有 `docs/BUGS.md` 承载（AR-16）。
2. **文档与代码双向漂移**（AR-12/13/14/15/19）：`docs/` 39 篇/8265 行，STATUS 自称"唯一真源"却是时间流水；PLAN 称 JSONL 是"唯一真相"而代码以 SQLite 为准；known-limitations 把已完成项写成未做；包数 17/18/19/20 并存（实际 20）；i18n 键 350 vs 421。**本审计反向纠错 3 处、暴露文档级问题 6 条**。
3. **发布链路未建立**（AR-03/AR-01）：无 CI、无版本注入、无签名/安装器/更新、无崩溃日志；49.5MB 二进制可 `-s -w` 省 31.8%（33.8MB）。"桌面产品"的升级与可支持性都没有落点。

### 7.2 被本审计推翻的旧结论（不要重复排期）

| 旧文档说法 | 实际 | 证据 |
| --- | --- | --- |
| "Auto-review 缺失"（FUNCTIONAL-PARITY.md:13） | ✅ 已实现（LLMToolReviewer，15s 超时，拒绝回退人工） | `runtime/review.go`、`app/services.go:856-884` |
| "Goal 无自动 round 驱动"（FUNCTIONAL-PARITY.md:18/38） | ✅ 已实现并接线（默认 6 轮、上限 12） | `session/goal_driver.go`、`app/services.go:123`、`controller.go:1543` |
| "plan mode 三项未做"（known-limitations.md:28） | ✅ 全部已实现（exit_plan_mode/Keep planning/pending 徽章） | `tools/exit_plan_mode.go`、`shell/plan_ui.go` |
| "PTC/Agent 模式 ✅ 已对齐" | ⚠️ 高估：PTC 只是批处理（FP-02） | `runtime/ptc.go` vs DSH `dsh-ptc-runtime-node` |
| "schedule ✅" | ⚠️ 语义降级（新建会话/cron 子集/无时区，FP-07） | `tools/schedule.go`、`app/app.go:593-614` |
| "JSONL 是唯一真相"（PLAN.md:725） | ❌ 代码以 SQLite 为准、JSONL 生产零读取 | `store/transcript.go:58-63`、`session/events.go:137-145` |

### 7.3 其他工程问题（详见 `04-architecture-quality.md`）

AR-05 两套快照、AR-06 写放大、AR-07 排序依赖回放、AR-09 schema 冻结、AR-10 eino alpha/mygo 0.7.4/sonic 回退告警、AR-17 测试盲区、AR-19 数字不可复核、AR-20 超大函数、AR-21 重复代码、AR-22 O(n²) 快照、`.probe/` 下 17MB 陈旧仓库副本（agent 改错树风险）。

---

## 8. 「全功能 AI Agent 桌面端」差距结论

### 8.1 六条产品生命线

| 生命线 | 现状 | 判定 |
| --- | --- | --- |
| ① 首次成功（onboarding/配置/模型） | 无引导；无模型发送静默失败；模型配置 5 个文本框无测试 | ❌ P1 |
| ② 可靠性（超时/重试/恢复/崩溃） | 60s 超时已由并发提交修复一半；无重试；失败回合丢历史；无崩溃日志 | ❌ P1 |
| ③ 信任与安全（workspace/权限/审批/密钥） | 审批与沙箱策略做得认真，但 1 个 P0 绕过 + 权限不闭环 + key 明文 | ❌ P0 |
| ④ 可管理性（设置/MCP/skills/automation/诊断） | 设置 6 页，缺关键页；MCP/skills 无 UI；automation 无历史 | ❌ P1 |
| ⑤ 分发与更新（安装包/签名/更新/版本） | 仅 Windows unsigned zip；版本号混乱；无更新 | ❌ P1 |
| ⑥ 生态与集成（ACP/SDK/headless/迁移/MCP http/插件） | 全缺或降级 | ⚠️ P2 |

**结论**：六条生命线里 5 条未闭环。ZeoKeel 当前是"**能力已跑通的强技术预览**"，不是"可交付的全功能桌面 Agent"。

### 8.2 产品定位建议（二选一，尽快拍板）

- **A. 真做"DSH 桌面替代/迁移器"**（推荐，若这是产品叙事）：补最小迁移向导（探测 `~/.dsh` → 导入 sessions JSONL → 复制/确认 credentials → 设置映射 → 报告导入结果）。工作量 M（会话导入可先做，配置映射后补）。否则文档必须删掉"迁移器"表述。
- **B. 降级为"DSH 同类替代（不迁移）"**：改 README/STATUS/PLAN 定位，把迁移列 roadmap，并在首启明确"需要重新配置模型与工作区"。

### 8.3 与 DSH 的竞争位

- 差异化仍成立：**纯 Go 单二进制 / mygo 原生 UI / 自有品牌设计 / 离线优先 / 无遥测 / 内置插件**。
- 但 DSH 已有而 ZeoKeel 短期难补的：三平台分发+更新、插件市场/profile、ACP/SDK/headless、workflow JS/PTC 运行时、workspace changes、FTS5、丰富的桌面集成。
- 建议打法：先把**可靠性 + 首启 + 分发**补齐（这决定"替代品"资格），再补编排语义（决定"专业用户"留存）。

---

## 9. 优先级路线图（含验收）

### 阶段 0：立即修复（P0 与低风险高收益，约 1 周）

1. **RT-01**：workspace `mcp.json` 默认禁用 + 显式审批（展示完整命令/参数/env 键名）+ 失败可见；补安全回归测试（示例仓库带 mcp.json 不触发执行）。
2. **RT-07**：spill 路径逐级 `Lstat` 拒 symlink + `O_EXCL`。
3. **RT-03**：Read Only 收敛 schedule_*/memory/goal（移除或纳入审批）。
4. **UX-01/03**：composer 发送失败统一 `toastText` + 失败不留空会话。
5. **AR-01**：`recover` + 文件日志 + 轮转；Windows GUI 必须有日志；`-ldflags -X` 版本/commit。
6. **AR-02（最小版）**：`.github/workflows/ci.yml` 跑 gofmt/vet/build/test/check-nocgo；race 至少加 app。
   验收：上述各项有单测/回归；全仓 `ci.sh` 绿；RT-01 探针从"命令执行"变为"被拒绝/需确认"。

### 阶段 1：v1.0 发布前必须（约 2–3 周）

1. **首启闭环**：onboarding setup 卡（检测无模型/无工作区）+ 模型 provider 预设 + Test 连接 + 空会话空态 + 无模型 CTA（PD-01/02 + UX-02）。
2. **权限正确性**：`submit` 前按会话权限重建（RT-02）；每会话独立 Agent 或"审批不占全局锁"评估（RT-04）。
3. **可靠性**：provider 重试/退避 + 流 idle 超时 + 失败回合部分落盘（RT-08；RT-06 已由并发提交修复，补慢流回归测试）；单实例数据目录锁 + 激活已有窗口（PD-03）。
4. **会话恢复**：last session + 草稿持久化（PD-10）；失败错误产品化（PD-09）。
5. **Automation 可信化**：run history/失败原因/重试 + 绑定 workspace/权限/模型 + 前台运行声明（PD-06）。
6. **桌面基础**：默认窗口 clamp 小屏（AR-18/PD-15）；Home 入口（PD-11）；popover 生命周期（UX-04）；工作区对话框 label（UX-05）。
7. **文档纠错 + `docs/BUGS.md`**（§7.2 + AR-16）；README 状态去重（AR-12）。
8. **CI 完整化**：race 加 shell（`-timeout 20m` 规划）、coverage 基线、去掉 flaky 重跑（AR-02/AR-17）。
   验收：新用户从零到"成功发出第一条消息并看到流式回答"全流程无阻断；`ci.sh` 在 CI 上跑；P0/P1 安全清单全绿。

### 阶段 2：产品化（约 3–4 周）

1. **workspace trust 模型**（RT-01 的 M/L 部分）：信任状态持久化、来源标注、untrusted 下 skills/AGENTS.md/MCP 行为收紧；统一 workspace 自动加载入口审计。
2. **管理面**：MCP 表单+连通测试、Skills 列表/校验、AGENTS.md 查看编辑（PD-07）；设置补 Notifications/Data&Privacy/Diagnostics/Network/Updates（PD-05）；插件页统一 + 实验项标注（PD-08）。
3. **分发**：macOS .app/dmg、Linux deb/AppImage、Windows 安装器；签名/公证；版本注入；更新检查（PR-13/AR-03）。
4. **迁移**：按 §8.2 决策实现最小导入或改定位（PD-04）。
5. **桌面生命周期**：开机自启、全局快捷键、托盘快捷动作、关闭行为偏好（PD-12）；通知配置与动作（PD-21）。
   验收：三平台安装包可装可卸；首启/升级/迁移路径 documented + 可复现。

### 阶段 3：架构与 parity（v1.x，持续）

分层重构 shell（AR-04/AR-08）、单一快照权威（AR-05/AR-22）、批量持久化 + retention（AR-06/RT-10）、消息序号列 + 迁移框架（AR-07/AR-09）；编排语义（FP-01/02/03 落实或明确降级）、workspace changes/diff（FP-05）、FTS5/会话引用（FP-09）、checkpoint 三屏障（FP-11）、附件持久化语义（FP-14）、MCP http（FP-13）、headless CLI（FP-18）、preset/persona（FP-06）。

---

## 10. 已验证良好的方面（不要误伤）

1. **路径沙箱**：拒绝绝对路径/卷名/`..`（含反斜杠），对最长存在前缀解析 symlink 后再校验；新建文件也能拒绝 symlink 父目录逃逸。
2. **SSRF 防护**：仅 http/https；DNS 解析后校验真实拨号地址（闭合 rebinding）、每次 redirect 重新校验、私网/保留段黑名单完整。
3. **进程治理**：`Setpgid` + 进程组 kill + `WaitDelay 2s`；jobs/terminal 关闭逐个 kill 组；输出上限 + 并发安全。
4. **store 打开路径**：单写连接 + WAL + foreign_keys + busy_timeout 并**回读校验**；DSN 正确处理空格/`?`/Windows 盘符；删除路径事务化 + id 正则校验防穿越。
5. **state 包质量**：不可变快照 + atomic.Pointer、Broker 非阻塞 + drop 计数、Projector 批量折叠、覆盖率 94.3%、race 干净。
6. **审批实现**：channel 化、提前决策缓冲、拒批回退人工、PTC 每个嵌套调用单独 gate/review。
7. **ADR 纪律**：14 篇 ADR 有状态/取代链/验证小节（如 0003←0006、0007←0008）——比文档体系（AR-12）做得好。
8. **测试规模与可无头运行**：211 测试文件/1122 函数；shell 86.2%、prompt 95%、runtime 81%、tools 80.9%；`check-nocgo` 四目标交叉编译真实把关。
9. **品牌与本地化**：深浅主题、WCAG AA 三级文字、品牌令牌、zh/en 主要屏全覆盖、文案口语化无 DSH 残留；通知内容隐私安全。
10. **产品细节正面项**：快捷键编辑是真的（13 条可改/持久化/恢复默认）、会话搜索/归档/pin/fork/JSONL 导出含附件、审批卡 + 工具卡 + 轨迹状态机可用、设置独立窗口、窗口几何持久化。

---

## 11. 未验证与后续

- **未验证**：Windows/macOS 真机（pwsh/GBK/无控制台崩溃/Gatekeeper/SmartScreen/签名）；IME/屏幕阅读器/高 DPI/多显示器/托盘/系统通知真机；真实模型端到端（本轮多数为 fake provider）；DSH 会话文件格式（迁移复杂度未定）；深色/浅色之外的 HighContrast 动态行为；审批鼠标点击路径（xdotool 时序不稳定的部分不下结论）。
- **审计后并发开发跟进**：RT-06 已由 `3111614` 修复（默认无整体流超时 + `provider.timeout_seconds`）；建议按附录 C 命令在最新 HEAD 重跑 P0/P1 验证清单，并在修复每个条目时补回归测试。
- **建议的后续审计周期**：每次大版本发布前跑一次"安全边界 + 首启闭环 + 分发"三项冒烟；把本报告的 P0/P1 迁移到 `docs/BUGS.md` 跟踪（AR-16）。

---

## 附录 A：分报告与证据索引

| 文件 | 内容 | 规模 |
| --- | --- | --- |
| [`00-brief.md`](00-brief.md) | 团队共享简报（环境/证据标准/方法学） | 193 行 |
| [`01-functional-parity.md`](01-functional-parity.md) | DSH 305 包能力地图 vs ZeoKeel（48 行矩阵、21 条） | 319 行 |
| [`02-runtime-correctness.md`](02-runtime-correctness.md) | 运行时/工具/provider/store/审批（P0 RT-01、16 条、9 探针） | 266 行 |
| [`03-ui-ux.md`](03-ui-ux.md) | UI/UX（16 条、90 张运行截图） | 214 行 |
| [`04-architecture-quality.md`](04-architecture-quality.md) | 架构/数据/测试/构建发布/文档（22 条） | 289 行 |
| [`05-product-gaps.md`](05-product-gaps.md) | 产品完整性/桌面生命周期/迁移（22 条、85 张截图） | 289 行 |
| [`06-verification-core.md`](06-verification-core.md) | 独立验证 A（FP/RT/AR 的 P0/P1 + 反向结论；30 条：CONFIRMED 28/PARTIAL 2） | 106 行 |
| [`07-verification-ui.md`](07-verification-ui.md) | 独立验证 B（UX/PD 的 P0/P1 + 真跑；28 条：CONFIRMED 23/PARTIAL 3/子项 REFUTED 2） | 109 行 |

证据目录：`.cache/audit/{parity,runtime,uiux,arch,product,verify,verify-ui,lead}/`（截图/探针/日志/静态分析/覆盖率/race 记录）。

## 附录 B：P0/P1 全量清单（去重后）

| 维度 | ID | 级别 | 一句话 | 状态 |
| --- | --- | --- | --- | --- |
| 安全 | RT-01 | **P0** | workspace mcp.json 构建期无审批执行任意命令 | 已独立复现 |
| 安全/权限 | RT-02 | P1 | automation/新会话不按自身权限重建 Agent（Full 继承） | 探针确认 |
| 安全/权限 | RT-03 | P1（复核建议 P2/并入 RT-02） | Read Only 仍注册 schedule/memory/goal 且不审批 | 探针确认 |
| 安全/数据 | RT-07 | P1 | spill 跟随 workspace symlink 写出工作区 | 探针确认 |
| 可靠性 | RT-08 | P1 | 无重试/退避，429/瞬断整轮失败且历史丢失 | 探针确认 |
| 并发 | RT-04 | P1 | 单 Agent 全局串行，后台回合/审批可阻塞前台 | 串行已确认 |
| 团队 | RT-05 | P1 | Agent Teams 成员无 turnContext → gated 工具必失败且用户不可见（竞态未证实） | 代码确认 |
| 可靠性 | RT-06 | ~~P1~~ 已修复 | 模型流 60s 硬超时 | 已由 `3111614` 修复 |
| 功能 | FP-01 | P1 | workflow 非 JS 编排引擎（固定 JSON 步骤） | 代码确认 |
| 功能 | FP-02 | P1（复核建议 P2/产品边界） | PTC 只是工具批处理（无代码运行时） | 代码确认；属有意取舍 |
| 功能 | FP-03 | P1 | subagent 仅同步（无 childId/异步/续谈/fork） | 代码确认 |
| 功能 | FP-04 | P1 | Goal 半成品（无 revision/phase/pause，无 /goal） | 代码确认 |
| 功能 | FP-05 | P1 | 无 workspace changes / 每回合 diff | 代码确认 |
| 功能 | FP-06 | P1 | 无声明式 agent preset/persona | 代码确认 |
| UX | UX-01 | P1 | 无模型发送完全静默（空会话/消息不落库/无 toast） | 双路复现 |
| UX | UX-03 | P1 | 发送失败类 toast 路径不一致（根因；**PARTIAL：仅键盘 Enter 路径静默，点 Send 有 toast**） | 代码+对照复现 |
| UX | UX-02 | P1 | 新会话对话 tab 无空态 | 截图确认 |
| UX | UX-02 | P1 | 新会话对话 tab 无空态 | 截图确认 |
| 产品 | PD-01 | P1（PARTIAL：键盘 Enter 静默；点 Send 有 toast） | 无模型首启无引导 + 错误静默（与 UX-01 同根因） | 复现 |
| 产品 | PD-02 | P1 | 添加模型无语义校验/预设/模型清单 | 截图+代码 |
| 产品 | PD-03 | P1 | 单实例固定端口、二次启动不激活窗口 | 复现 |
| 产品 | PD-04 | P1 | "迁移器"定位但零迁移实现 | 代码确认 |
| 产品 | PD-05 | P1 | 设置面缺通知/隐私/诊断/代理/更新/重置 | 代码+截图 |
| 产品 | PD-06 | P1 | Automation 失败/历史不可见、不绑定 workspace/权限/模型 | 代码+截图 |
| 产品 | PD-07 | P1 | MCP/Skills/AGENTS.md 无管理 UI | 代码+截图 |
| 产品 | PD-08 | P2（验证降级） | 插件双界面不一致（4 vs 6）、无功能实验开关（"死 CTA"已被推翻） | 截图+代码 |
| 产品 | PD-09 | P1 | 失败回合直接展示内部错误栈、无建议/重试 | 截图 |
| 产品 | PD-10 | P1 | 重启不恢复会话、草稿仅内存 | 代码+复现 |
| 工程 | AR-01 | P1 | 发布版零崩溃诊断（无 recover/日志，Windows 丢 stderr） | 代码确认 |
| 工程 | AR-02 | P1 | CI 只是本地脚本；race/coverage 覆盖不全、flaky 重跑 | 代码确认 |
| 工程 | AR-03 | P1 | 发布链路缺失（仅 Windows zip/无签名/无更新/版本未注入） | 产物确认 |

## 附录 C：关键可复现命令（供修复后回归）

```sh
export PATH=/usr/local/go/bin:$PATH GOCACHE=/data/mygo/.cache/go-build GOMODCACHE=/data/mygo/.cache/gomod GOSUMDB=off CGO_ENABLED=0

# 全量门禁（与 CI 同口径）
sh scripts/ci.sh --fast

# P0 安全探针（在 /tmp 仓库副本 + 探针文件；见 .cache/audit/runtime/probes/）
# 期望修复后：命令不执行 / 需要显式确认
grep -n "mcp.json" internal/app/services.go internal/mcp/mcp.go

# 单实例锁 & 窗口几何 & UI 首启冒烟（注意：单实例固定端口，需 unshare -n 并行）
unshare -n xvfb-run -a -s '-screen 0 1600x1000x24' bash -c '
  ZEOKEEL_DATA_DIR=/tmp/zk-regress /data/mygo/dist/zeokeel-linux-amd64 >/tmp/zk.log 2>&1 &
  sleep 5; import -window root /tmp/home.png; xdotool search --name ZeoKeel'

# 无模型发送回归（期望：可见错误提示 + 不产生空会话）
# 1) 建 workspace；2) Home 输入 Enter；3) 检查 UI 有错误提示、sqlite messages=0 且 sessions 不新增
```

> **报告状态**：本文件由 Lead 基于 5 份分报告 + 2 份独立验证报告合成；验证员结论回填后以 `06/07` 为准。基线漂移说明见 §1.4。所有行号以各分报告标注的核验 commit 为准（多数为 `27d5d5f`，UI/产品为 `20f1399`，运行时部分已对 `3111614` 复核）。
