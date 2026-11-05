# 状态流水（历史归档，不再维护）

> 2026-11-05 归档：本文件是 2026-11-04/05 的 M0–M13 开发过程记录（含已退役的像素门禁指标）。现行状态见 [docs/STATUS.md](../STATUS.md)；缺陷状态见 [docs/BUGS.md](../BUGS.md)；审计证据见 docs/audit/。

---

# 状态流水（归档前原文）

更新时间：2026-11-05

## 2026-11-05 方向变更（当前状态）

- **用户决策**：ZeoKeel = DSH 桌面替代/迁移器 + 独立品牌产品；**功能对齐 DSH，视觉与交互自有，停止 DSH 像素级对齐**。见 [ADR 0001：退役 DSH 像素对齐基建](../decisions/0001-retire-dsh-pixel-parity.md)。
- 已删除并归档：`baseline/`（截图/控件/CSS/行为数据）、基线文档目录、`artifacts/`、`scripts/cdp.mjs`、`scripts/capture/`、`internal/pixeltest/`（归档包 `/data/zeokeel-backups/parity-assets-20261105.tar.gz`，回滚点 tag `pre-native-ui`）；CI/门禁像素步骤与像素指标同步移除（`scripts/ci.sh`、`scripts/evolve/gates.sh`）。
- **以下 M0–M13 历史里程碑保留原文记录以追溯**；其中与像素/基线/比对回路相关的内容均为**历史记录，已退役、不再有效**（因此模块清单中不再列 `pixeltest` 包）。
- 现行质量口径见 `docs/QUALITY.md` 与 `docs/PLAN.md` §10；功能对齐见 `docs/FUNCTIONAL-PARITY.md`；视觉方向见 `docs/decisions/0002-brand-theme-foundation.md`。

## 2026-11-05 交互调整（追加，ALL GREEN）

- **原生窗口菜单栏退役**（用户原话「file edit 这些菜单栏不需要」）：删除 `internal/app` 的菜单模板/安装/分发（File/Edit/View/Window），`run()` 显式 `mygo.App.SetMenu(nil)`（macOS 默认菜单一并移除）；托盘菜单、shell 快捷键与应用内入口保留；新增 [ADR 0006](../decisions/0006-retire-native-menu-bar.md)（取代 ADR 0003 的菜单栏部分，窗口状态持久化继续有效）。`internal/app` 净 -325 行，菜单测试改写为 Quit 路径测试。
- **移除会话头「计划模式」复选框**（用户：计划模式就是 composer 下方权限里的「只读」）：删除 `planHeaderToggle`/`togglePlanMode` 与 header 调用；controller/runtime 的 plan mode 语义与计划审阅卡当时保留、暂无 UI 入口（入口处置见下方「计划模式对齐 DSH」）。[ADR 0007](../decisions/0007-remove-plan-mode-toggle.md)。
- **Chat 会话页改为按记录 Seq 渲染（对齐 DSH）**：原实现把工具摘要 + 卡作为**会话级尾部区块**追加在消息列表之后，导致「最终回复在上、执行过程在下」；且多轮/重开会话会把历史工具卡堆到最后一条回复之后、摘要跨回合累计（实测渲染 Y 序：firstAnswer < secondAnswer < summary < tools）。现数据层 `state.Message.Seq` 与 `ToolState.StartedSeq` 共用快照 Seq 轴（重开时恢复为 store 事件 seq），视图按回合归并：用户消息 → 该回合过程块（摘要行 + 卡，插在首个调用的记录位置）→ assistant 回答/最终回答 + 完成行；过程块折叠逐块独立，卡序按 StartedSeq。新增 live + 重开两条路径的顺序回归、手工 fixture 降级路径测试；`session-native`/`toolcard` 预览按新顺序重生成。
- **验证**：`gofmt`/`go vet ./...` 干净；`go test ./... -count=1` **19 包全绿**；`ZEOKEEL_VISUAL=1` 视觉回归 PASS；22 张预览按当前代码刷新（含 Session 头的 10 张去掉了计划模式 chip，toolcard/session 预览呈现新顺序）。

## 2026-11-05 计划模式对齐 DSH（方案 A，ALL GREEN）

- **背景**：header 复选框移除后，用户追问「DSH 是怎么处理计划模式的？不就是只读么？」。对已安装 DSH 0.2.1-rc.2 包的审计结论：DSH 的 plan mode 是**流程**（`@deepseek-ai/dsh-plan-mode`：`plan:policy` 软引导 + `exit_plan_mode` 审阅；文档明确「它不限制 agent：每个工具仍可调用」「计划模式是引导而非执行沙箱」），**只读**是权限预设里的 sandbox 模式（`read-only`/`workspace-write`/`danger-full-access` + 审批策略捆绑），两者正交可叠加；DSH 的入口是 `/plan` 命令 + plan 激活时的 composer「计划」徽章，没有 header 复选框。ZeoKeel 原 plan mode 是 runtime 硬门（拦截 mutating 工具），因此与「只读」重复。
- **决策（[ADR 0008](../decisions/0008-plan-mode-soft-alignment.md)）**：对齐 DSH。删除 `internal/runtime/plan_mode.go` 与 runtime 侧 PlanModeFunc/模型适配/PTC/子代理的全部工具拦截接线；`internal/app/services.go` 不再向 runtime 传 `PlanModeFunc`（prompt 的 `PlanMode` 注入保留）。plan 模式 = `plan:policy` 软引导 + `present` "Plan" + shell 计划审阅卡（Approve/Exit 不变）；需要强制只读规划时用户同时选 composer「只读」权限。
- **入口**：`/plan` 开启、`/plan off` 关闭、`/plan <message>` 开启并把消息作为下一请求提交（无 session 时提示先开会话；slash 行不进模型）；composer 计划徽章仅在激活时显示（accent「✦ 计划/Plan」chip，点击=退出，a11y RoleButton/Focusable，窄 composer 只留图标）；`/help` 与命令面板同步。
- **验证**：gofmt/vet/build 干净；`go test ./... -count=1` 19 包全绿；新增 `/plan` 命令与徽章测试 4 个 + `/help`/命令识别测试，计划审阅卡 Approve/Exit 测试不回退；`ZEOKEEL_VISUAL=1` PASS（徽章仅激活态，预览 fixture 未变）。
- **遗留（ADR 0008 §4）**：独立 `exit_plan_mode` 工具、审阅卡 Keep planning（带反馈）分支、pending 态徽章、Home 草稿会话的 `/plan`（Home `/` 面板丢焦点已在本轮后续修复，见下）。

## 2026-11-05 平台感知 + 弹层统一（ALL GREEN）

- **问题**：用户反馈「agent 没办法知道自己是什么操作系统，在 Win 上执行 Linux 命令」+「权限下拉弹层没有样式，统一设计语言」+ Home composer 打开 `/` 面板丢焦点。
- **平台感知（[ADR 0009](../decisions/0009-platform-shell-stacks.md)）**：审计 DSH 后按其方案落地——DSH 不写 OS 提示段，而是按平台门控 shell 栈（Windows 只 pwsh、POSIX 只 bash，`dsh-base` 组合层）+ 工具描述即方言 + per-tool 提示段（`tool:bash` 1000 / `tool:pwsh` 1010）。ZeoKeel 等价实现：`tools.Config.AllowBash` + `app.shellStackFor(goos)` 按平台注册（Windows 不再注册 bash、POSIX 不再注册 pwsh）；`internal/prompt` 新增 `runtime:environment` 段（Order 105，OS/arch + shell 调用方式/语法/路径/环境变量风格，中英）与 `tool:pwsh` 段，`Context.Platform` 由 app 注入；bash/pwsh/terminal 描述与缺 PowerShell 错误按平台方言化；Windows 终端默认 shell 优先探测 pwsh。
- **弹层统一**：mygo `ui.Popover` 只负责定位/关闭/焦点圈定、不带面板样式（原 permission 菜单是裸 `ui.Button` 列表）。新增 `internal/shell/popover_menus.go`（品牌 Panel + 菜单行 + 选中 ✓ + 复选/动作/指定角色行，宽度 260–320 并 clamp 到所在列）；统一 composer 权限/模型/工作区/附件/`/` 命令面板、会话头 agent mode/More actions、侧栏账户 More/View options、右栏 New tab/Choose shell/Files 面包屑；Settings select 弹层与工作区 hover 卡有意跳过（自有视觉/非菜单）。详见 [native-ui.md §19.4](../design/native-ui.md)。
- **Home composer 焦点缺陷**：`homeLiveComposer` 把 `/`/@ 面板插在 composer 列之前 → 元素 id 变化、旧焦点悬空；`homeComposerCard` 补 `input.AutoFocus()`（与 Session composer 对齐、不抢用户手动聚焦），Enter 恢复正常执行命令；Home 预览重生成显示聚焦边框。
- **验证**：gofmt/vet/build 干净；`go test ./... -count=1` 19 包全绿；`GOOS=windows GOARCH=amd64 go build/vet ./...` 通过；`check-nocgo` 四目标 PASS；`ZEOKEEL_VISUAL=1` 视觉回归 PASS；`docs/prompts.md` 段目录同步（41→43，新增 `runtime:environment`/`tool:pwsh` 与 `Context.Platform`）。遗留：Windows 运行期真机验证、POSIX 不再暴露 pwsh、Git Bash/WSL 未集成（见 [known-limitations](../design/known-limitations.md) §1/§5）；plan 模式 ADR 0008 §4 事项仍待排期。

## 2026-11-05 反馈修复批次（ALL GREEN）

- **构建产物统一 `dist/` + 稳定命名**（用户：「清理一下编译，搞的到处都是，固定编译到一个位置，并且编译名称要稳定」）：新增 `scripts/build.sh`（make 同名目标），本机/交叉/Windows 包全部固定落 `dist/zeokeel-<goos>-<goarch>[.exe]`（Windows 另加 `-console.exe`），发布包固定 `dist/zeokeel-windows-amd64.zip`；`make clean` 只清构建产物（不再删 `.cache/`），根目录遗留 `zeokeel`/`zeokeel.exe` 已删；Windows 包 README 移入仓库 `packaging/windows/`。[ADR 0012](../decisions/0012-build-output-layout.md)
- **审批范围收敛**（用户：`str_replace_editor view` 为什么也要审批、点是不是有点多）：Workspace Write 默认只审批能逃逸工作区沙箱/有工作区外副作用的工具（bash/pwsh/terminal/job/worktree/web/mcp__*）；workspace 内文件写由沙箱边界保证、不再逐次审批；`str_replace_editor command=view` 永不审批；Auto review EXP 保留原宽复核范围；`approval.tools` 覆盖语义保留。[ADR 0010](../decisions/0010-approval-scope.md)
- **Windows 不再弹 cmd 窗口**（用户：「执行命令时不断弹出 cmd 的对话框」）：新增 `internal/winproc.Hide`（Windows 设 `CREATE_NO_WINDOW`+`HideWindow`；其它平台 no-op），应用到 bash/pwsh/terminal/jobs/git worktree/MCP server/UI 终端全部 spawn 点；DSH 对应实现是 Node child_process 的 `windowsHide:true`。[ADR 0011](../decisions/0011-windows-hidden-console.md)
- **Chat 顺序回归修复**（用户实测：「这个又跑下面去了，你怎么修复的？」）：根因是 runtime 每 turn 单 msgID、assistant 消息在工具前的 Reasoning 时创建，`Message.Seq` 早于工具 `StartedSeq`。`state.Message` 新增 `LastSeq`（最后可见 Token seq，Reasoning 不更新），重开快照按末 token 事件恢复；`transcript_order` 改为按可见文本**结束位置**与过程块归并——过程块在回答上方。含修复前 FAIL/修复后 PASS 的 reasoning-first 回归、多轮与重开测试。
- **执行失败 exceeds max iterations 修复 + 默认不限步数**（用户：「执行命令时候失败了」→「dsh 有设置么？不应该是让 AI 自己决定什么时候结束么？」）：不是配置 bug——eino `MaxIterations` 是模型调用次数，旧默认 20 且错误硬失败。现改为**默认不限**（0=不限，模型返回最终回答即结束，对齐 DSH `while(true)`；eino 的 0→20 回退用 sentinel 绕过）；Settings 可选 1–1000 安全上限，命中时**优雅收尾**（保留工具结果、本地化提示、history/checkpoint、idle+usage），不再 failed；超时/取消/真实错误仍失败。[ADR 0013](../decisions/0013-agent-step-limit.md)
- **Session 右侧空白缺口修复**（用户截图标注右侧空白）：Chat 布局改为 `Row{ Column(Grow){transcript; composer}, teamPanel, todoPanel }`——composer 回到主列、team/todo 面板贯穿到窗口底部；Trajectory 复用同一 body 帧保持 composer 元素身份（避免切 tab 抢焦点）；新增渲染断言 + 窄窗回归。
- **Settings 剩余弹层统一**：Permission/Font size/More font settings 三个自定义 popover 切到 `popover_menus.go` 品牌 helper；其余 5 个原生 `ui.Select` 保留原生键盘/a11y 语义。
- **plan 模式 ADR 0008 §4 四项补齐**：独立 `exit_plan_mode` 工具（per-session plan 门控、非阻塞提交、shell 从 snapshot 渲染审阅卡）、审阅卡 Keep planning（反馈作为下一轮用户消息）、pending 态计划徽章（warning+Clock+待审阅、a11y、点击重开卡片）、Home 无 session `/plan` 创建草稿会话并开启 plan；旧 `present` 路径兼容。[ADR 0008 §4](../decisions/0008-plan-mode-soft-alignment.md)
- **验证**：gofmt/vet 干净；`go test ./... -count=1` **19 包全绿**；`GOOS=windows go build/vet ./...` 通过；`scripts/check-nocgo.sh` 四目标 PASS；`ZEOKEEL_VISUAL=1` **14/14 PASS**（预览资产无需重生成）；Windows 包用新脚本重建（见 README 构建章节）。

## 2026-11-05 反馈修复批次 2：使用量/中断生命周期 + 编排护栏（ALL GREEN）

- **Usage 累计 + live**（[ADR 0014](../decisions/0014-usage-lifecycle-contract.md)）：`state.Usage` 改为会话累计（Turns/Steps/tokens 累计），runtime 每次模型调用 emit usage delta；成功/取消/失败/优雅超限都有已结算 usage；旧 per-turn 绝对事件求和兼容。
- **P1 收口（第六轮）**：逐轮终态标记持久化（`turn_end` 事件 + `Snapshot.TurnMarks`，Chat 每个历史回合末尾显示「已停止/已中断/本轮失败」，重开回放，旧会话回退最后回合）；大工具结果（>32KB，含 CJK）持久化时溢写 workspace `.zeokeel/tool-results/<call>.txt`，持久化/下一轮 history 只发摘要+路径+前 2KB，保留最近 100 份、写失败/无 workspace root 回退原文；`plugin.agent_loop.budget_warn_steps` / `budget_warn_tokens` 设置接线到 runtime 软预算 advisory（空=默认、0/off=关闭）；取消瞬间 trailing usage 在流结束/取消路径 flush（ADR 0014 更新）。
- **中断/停止带原因**：`Status.Reason`（stopped/failed/interrupted）持久化+回放；取消路径补 `ToolEnd{Cancelled}`，工具卡/工具行不再永久 running；`OpenSession`/`phaseFromStore` 对冷 running 做恢复（关闭悬挂 tool_calls + idle/interrupted），不再幽灵运行中；`persistDrafts` 跳过 reasoning-only 空行，历史读取丢弃 reasoning-only assistant。
- **footer live**：运行中显示累计 turns/steps/out tok，tok/s 用本回合基线，全 0 隐藏（保留行高防布局跳动），`完成于` 只给完成回合，Chat 显示「已停止/已中断」。
- **编排护栏**（[report-orchestration-impl](../../.cache/team/report-orchestration-impl.md)）：新增 `tool:ralph` 提示段（Order 2700，实验性/默认不用/取消语义）；新增 always-on `harness:self-knowledge`（能力问题直接答、不建 team/ralph/workflow 自检、旧自检报告是数据）；收敛 subagent/team 引导。`agentteam.Manager.StopSession` 定向取消+清队列+等待成员退出；`spawn_team` 写入会话归属，bundle Close 关闭 team；shell 4 个 Stop 入口统一 `stopTurn`；团队面板合并同步 subagent + spawn_team live roster。ralph/workflow 取消返回结构化 `status:"cancelled"`（保留已完成轮次/步骤）。`Controller.SetTeamStopper` 覆盖非 UI Stop。
- **Files 面板 P0 + 命令面板**（前一批）：见上方「反馈修复批次」；本批补齐文件面板/命令面板的测试与文档。
- **验证**：`go test ./... -count=1` 连续两次全绿（20 包）；gofmt/vet 干净；`GOOS=windows go build/vet ./...` 通过；`check-nocgo` 四目标 PASS；`ZEOKEEL_VISUAL=1` PASS；Windows 包重建。
- **遗留**：Windows 真机验收（编码/无 cmd 窗/新命令/工作区 chip/Files open/reveal）；`/goal`、`/compact` 有意不加（无安全落盘/手动压缩 API）；P1 增强已收口（逐轮标记/软预算/重复提醒/工具结果 spill/Files 预览/@ quoted path 均落地，见上）。

## 2026-11-05 v1.1 收尾（追加，ALL GREEN）

- **后台审批通知**：`session.Controller.SetOnApprovalRequested` 可选 hook（默认 nil），所有会话新增审批/提问等待 → 系统通知（同 session+call 去重、隐私文案、点击回会话、与快照路径不双发）；notify 测试 29→36；ADR 0004 更新。
- **TextScale 覆盖**：`design.Tokens.TextScale` + `Metrics/Scale`，`Font/Text/TextMuted` 全走缩放；Home/Session/Settings 21 处关键显式字号转 TypeRole + `t.Scale`；1.0x 渲染与 HEAD 逐字节一致，1.5x 不裁切；剩余约 169 处留 v1.2。
- **Trajectory 虚拟化**：`ui.List` 只建可视行，Key=事件序号、展开状态/A11y/交互不变；1000 事件稳态帧 **22.7ms→3.12ms（7.3×）**，冷渲染 128.8→42.6ms（perf.md §8）。
- **视觉回归（opt-in）**：`scripts/visual-check.sh`（12s，阈值 2%，--update/--all/退出自动还原）+ `TestVisualRegression`（仅 ZEOKEEL_VISUAL=1，默认 skip，时钟文本掩码）；known-limitations §4 更新。
- **P3 清理**：plugins 采样色→`t.LabelTertiary`、settings-window 预览注册宿主并断言唯一关闭入口、known-limitations 数字/措辞、preview README 尺寸。
- **验证**：`gofmt`/`vet`/`build` 干净；`go test -count=1 ./...` 17 包全绿（shell ~58s）；`ZEOKEEL_VISUAL=1` 视觉回归 PASS；22 张预览已按当前代码/字体环境重生成；工作树提交前将保持干净。

## 2026-11-05 收口：独立验收 PASS（objective 全项）

- 独立验收复跑（HEAD `68bc842` → 修复后 `e4e71e4`）：objective ①拆像素门禁与匹配代码 PASS、②品牌原生视觉/桌面交互本地化 PASS、③功能对齐与测试绿色 PASS；FAIL=0、阻塞=0。
- 门禁证据：`gofmt` 空、`go vet ./...`、`go build ./...`、`go test -count=1 ./...` 18 包全绿（shell ~45–52s）、`check-nocgo` 四目标 PASS、`scripts/ci.sh --fast` PASS；测试/CI 后工作树保持干净（预览测试 opt-in 不写仓库）。
- 报告：[07-final-acceptance.md](../research/07-final-acceptance.md)、[08-final-recheck.md](../research/08-final-recheck.md)；遗留仅为真机验证与已知框架限制（[known-limitations.md](../design/known-limitations.md)），不阻塞目标达成。

## 2026-11-05 阶段 6：设计评审批次修复（P0，完成，ALL GREEN）

- **P0-2 对比度**：Light `TextTertiary` `#7E8796` → `#646C7A`，BgBase/BgRaised 5.29、BgSidebar 5.06、BgStrong 4.76:1（全部 ≥4.5:1）；brand_test 增加四组表面 + 层级断言；ADR 0002 §9。
- **P0-3 深色字标**：`logo-dark.svg` → `mark-dark@2x.png`（96×96）；`brandMark(t)` 按主题选择；Home hero/Sidebar/rail 切换；assets_test 验证深色无白边、浅色不变；ADR 0002 §10。
- **P0-1 破坏性操作**：Automation 删除与 Memory Clear all 改为确认弹层（Cancel 默认焦点、Esc/点外关闭、Enter 不误删、Danger 确认按钮、确认后 Toast）；计划文案中文本地化（每小时/每 N 分钟/每天 HH:MM），cron 行为未改；新增 3 测试。
- **P1 收口（第五轮）**：toolcard 错误面板对比度（品牌新增 `DangerText` #B91C1C/#FCA5A5，danger tint 5.7:1）+ 测试；设置独立窗口隐藏多余文字关闭入口（只留面板 ×，overlay 回退不变）。
- **预览**：22 张全部重生成（含深色字标/对比度变化）。
- **验证**：`gofmt`/`go vet`/`go build ./...`/`go test -count=1 ./...` 全绿（shell 45.9s）；工作树提交前干净。
- 未验证/框架限制已整理：[known-limitations.md](../design/known-limitations.md)。

## 2026-11-05 阶段 5：工具卡/设置独立窗口/后台通知/a11y/清理与性能（完成，ALL GREEN）

- **工具卡内容原生化**：命令/文件结果 → mygo markdown 代码块（高亮+语言+Copy）、普通结果 markdown、read_image 真实加载（失败 alt）、错误 danger 面板+复制错误、40 行封顶可展开；新增 toolcard 预览。
- **设置独立原生窗口（P1）**：非模态单例、Cmd+, 打开/聚焦、关闭隐藏不退出、状态与主窗口同步、overlay 回退；可注入 window manager 单测 8 个；ADR 0005。
- **后台会话通知（P1）**：session.Controller 新增可选 `TurnOutcome` hook（默认 nil），后台 turn 完成/失败也通知；sessionID+turnID 去重、同会话 5s 冷却、失败绕过、Stop/无输出/退出/活跃抑制、点击回会话；notify 测试 17→30；ADR 0004 更新。
- **a11y/键盘收尾**：Sidebar a11y 树层级（RoleTree/TreeItem Level）+ 行内动作暴露、5 屏无名节点审计、8 个键盘回归用例。
- **清理与性能**：死代码净 -203 行（layout/menus/composer_style 零引用符号）、app.go 旧 helper 3 个；新增 6 屏 BenchmarkRender（冷渲染 21.7–31.4ms，稳态帧 0.48–1.26ms，[perf.md](../design/perf.md)）。
- **独立评审/审计**：`docs/design/review-round3.md`（Top10 问题）、`docs/research/05-final-audit.md`、`docs/research/06-acceptance.md`。
- **验证**：`gofmt`/`go vet`/`go build ./...`/`go test -count=1 ./...` 17+ 包全绿（shell 45.3s）；22 张预览。

## 2026-11-05 阶段 4：剩余面板 + markdown 原生化 + 桌面交互 P0-2（完成，ALL GREEN）

- **剩余面板/区**：Trajectory 内部行、内联搜索/搜索弹层、todo/team/plan 面板、右侧栏（Files/Terminal/Start，720px → 响应式 560/45% 可拖拽）全部品牌原生视觉化；新增 Trajectory 与右栏浅/深预览。
- **Markdown 迁移 mygo `ui/markdown`**：删除自研解析渲染 522 行（markdown.go 628→106），获得代码高亮/语言标签/Copy、GFM 表格、任务列表、流式增量解析、文本选中+右键复制（文本/HTML/Markdown）；`markdownBlocks` 签名不变。
- **系统通知**：Snapshot 相位状态机识别完成/失败/待审批，窗口不活跃时 `mygo.NewNotification`，点击回焦并打开对应会话；Notifier 可注入，17 个单测；ADR 0004（含后台会话通知等遗留）。
- **系统偏好**：TextScale/HighContrast/ReduceMotion 接入（主题字号、对比度增强、`DisableAnimations`），2x DPI 渲染冒烟；滚动条策略调研记录（native overlay + HC 增强）。
- **工程**：Home/Session composer 收敛到 `composer_native.go`；responsive 测试改为按实际面板 rect 判定；预览测试 opt-in 写盘。
- **验证**：`gofmt`/`go vet`/`go build ./...`/`go test -count=1 ./...` 17 包全绿（shell 40.0s）；工作树干净。

## 2026-11-05 阶段 3：六屏品牌原生视觉 + 桌面壳 P0-1（完成，ALL GREEN）

- **屏/区**：Home、Session、Sidebar、Settings、Plugins、Automation 全部完成品牌原生视觉重构（mygo 原生控件 + `internal/brand` 主题 + Theme/design 令牌；删除 DSH 文案、采样色与 877/909/800×800/960 等像素魔法值）；交互与 a11y 标签/键盘可达不回退。
- **桌面壳**：系统菜单栏（macOS Role/Windows-Linux 窗口菜单、accelerator 与 `shortcuts.go` 同源）、窗口尺寸/位置/maximized 持久化（`DataDir()/window.json`，损坏 fallback、原子写）、关窗到托盘与退出逻辑修复（ADR 0003）；新增 13 个 app 单测。
- **验证**：`go build ./...`、`go vet ./...`、`gofmt`、`go test -count=1 ./...` 全绿（shell 38.8s）；新增 12 张浅/深色预览图（`docs/design/preview/`）+ 6 个 `TestRender*NativePreview`。
- **遗留（下一阶段）**：Trajectory/右侧栏/内联搜索/面板视觉、markdown→`ui/markdown`、文本选择/系统通知/滚动条/DPI/TextScale、设置独立窗口、Sidebar a11y 嵌套与真机 IME/a11y 矩阵。见 [原生视觉方向 §10](../design/native-ui.md)。

## 2026-11-05 阶段 2：Home 原生视觉 pilot（完成）

- 用户决策后的第一个"回归 mygo 设计能力"落地屏：Home live 屏重写为 ZeoKeel 自有视觉。
- Hero：品牌 mark（44）+ `ZeoKeel` 字标（600、2.25rem）+ 双语 tagline「今天想构建什么？/ What will you build today?」；删除 DSH 的 `Into the Unknown`、`Preview` 徽章、鲸鱼图标。
- 布局令牌化：内容列 `min(720, 主区-80)`（`Theme.Space` 派生），删除 909/877 等 DSH 像素常量；composer 用品牌 Panel（Radius 10 / 1px border / 单层轻阴影）+ 原生 `ui.TextArea`；发送/加号/右侧栏开关改原生 `ButtonBase`；工作区/权限/模型 trigger 保留自定义 Popover 但按品牌 chip 重绘（标签/角色/键盘可达不变）。
- 截图（可复现，`TestRenderHomeNativePreview`）：`docs/design/preview/home-native-light.png`、`home-native-dark.png`；设计记录见 [原生视觉方向](../design/native-ui.md)。
- 验收：`go build ./...`、`go vet ./...`、`gofmt`、`go test -count=1 ./...` 全绿（shell 36.6s）；功能（/ 命令、@ 提及、附件、Cmd+K/N、i18n、禁用发送、a11y）由原有测试覆盖。
- 下一阶段：Session composer/消息流与 Home 统一 → Sidebar 原生化 → Settings/Plugins/Automation → 桌面交互 P0（菜单栏/文本选择/通知/窗口状态）。

## 2026-11-05 阶段 1 完成：像素门禁与静态屏拆除（ALL GREEN）

- **像素门禁已拆**：`TestScreensBaseline` / `TestScreensDarkNarrow` / CI 6 次像素调用 / `internal/pixeltest` / `baseline/` / `docs/baseline/` / `artifacts/` / `scripts/cdp.mjs` / `scripts/capture/` 全部移除（归档包与回滚 tag 见上）。
- **静态五屏已拆**：home/session/plugins/settings/automation 的 `Ctrl == nil` 静态渲染路径与静态专用 helper 已删除；`App.View` 只渲染 live（无控制器时绘制空帧）。生产 `internal/shell` 生产代码 16,288 → 15,343 行。
- **测试已迁移 live**：parity 专项测试删除 7 文件/17 个 Test；13 个静态契约测试删除；30 个产品行为测试迁移到 live fixture（新增 `newLiveAppAt` / `newLiveAppWithSnapshot`）。shell 测试 14,514 → 13,233 行，测试函数全仓 838 → 812。
- **品牌主题地基**：新增 `internal/brand`（logo 色板 + mygo `ui.Theme` 品牌覆盖 + Radius=10/Spacing=4/FontSize=14/系统字体）；`internal/design` 全量派生品牌主题，去除 DSH 令牌口径。详见 [ADR 0002](../decisions/0002-brand-theme-foundation.md)。
- **门禁**：`go build ./...`、`go vet ./...`、`gofmt`、`go test -count=1 ./...` 全绿（shell 36s、session 30s）。
- **下一阶段**：按 [原生视觉方向](../design/native-ui.md) 逐屏重构，Home 屏为 pilot。

## 基线文档：100% 收口（历史，像素对齐已退役 2026-11-05）
- 80 张屏/状态截图、53 份全元素 CSS、108 条逐控件行为、216 张前后对比（历史数据；资产已归档删除，见 `/data/zeokeel-backups/parity-assets-20261105.tar.gz`）
- 历史文档入口已随基线目录归档删除，不再维护。

## M0 进行中（历史记录）
| 任务 | 状态 |
| --- | --- |
| M0-1 仓库骨架 + go.mod | ✅ |
| M0-2 no-cgo 守卫 + 交叉编译脚本 | ✅ |
| M0-3 基线采集（截图/controls） | ✅（历史；资产已归档删除） |
| M0-4 令牌提取 + 生成（首版：design 包常量 + 单测） | ✅ |
| M0-5 空壳窗口（侧栏 280 + 首页 composer + 主题 + 字体） | ✅ 无头渲染通过（历史产物已删除） |
| M0-6 `ui.Render` 像素链路 | ✅ `shell` 无头渲染 + 像素断言（历史；像素门禁已退役，与 DSH 基线 diff 记录见下） |
| M0-7 `ui.NewTester` 交互骨架 | ✅ 侧栏导航/会话行可点击，`TestSidebarNavigation` 无头点击通过 |
| M0-8 事件总线 + 快照骨架 | ✅ `internal/state`：Broker + Projector + 不可变快照，24 测试 |
| M0-9 存储骨架 | ✅ `internal/store`：SQLite v1（modernc，纯 Go）+ JSONL 记录，19 测试 |
| M0-10 单实例/托盘/关窗隐藏 | ✅ `internal/app`：回环监听单实例锁、托盘 Show/Quit、关窗隐藏 |
| M0-11 基础样式函数层 | ✅ 首版 atoms/components（Page/Sidebar/BrandRow/NavRow/Panel/Composer/PrimaryButton/Tab/Row） |
| M0-12 逐屏截图采集 | ✅（历史；已归档） |
| M0-13 基线文档 | ✅（历史；已归档） |

## M1 第一轮：五屏渲染 + 像素比对回路（2026-11-04，历史；像素门禁已退役）

已实现 `internal/shell` 五个屏：Home / Session / Plugins / Settings / Automation；像素比对测试（历史）逐屏用 `ui.Render` 渲染 1600×1000，并与 DSH 基线截图做像素 diff，输出：

- 渲染图：历史产物目录（`artifacts/`，已删除）
- 差异图（红=不同）：历史产物目录（`artifacts/`，已删除）
- 首轮差异率：

| 屏 | diff | 说明 |
| --- | --- | --- |
| home | 3.72% | 壳层几何已接近；内容/图标差异 |
| session | 11.26% | 头部/tabs 位置未对齐、消息内容不同 |
| plugins | 6.15% | 行高/图标/开关样式待调 |
| settings | 64.95% | Modal 布局有问题（右列被拉伸、背景脏），下一轮修 |
| automation | 3.14% | 结构接近 |

**回路已建立（历史，已退役）**：改 `internal/shell` → 跑像素比对测试 → 看 diff 率与红图 → 再改。下一轮从 settings 的 Modal 布局与 session 头部几何开始压差异率。

## M1 五屏 UI：完成（2026-11-04；像素指标为历史记录）

五个顶屏全部用 mygo 原生控件实现，逐屏与 DSH 基线做像素比对并迭代到结构对齐（历史指标：像素比对 masked diff，容差 12，排除品牌行 / 侧栏动态列表 / 基线悬浮 tooltip）：

| 屏 | masked diff | raw | 入口 | 说明 |
| --- | --- | --- | --- | --- |
| Home | **0.93%** | 2.39% | `internal/shell/home.go` | 40px 顶栏、hero、877×114 composer、工具条、禁用发送 |
| Session | **6.40%** | 8.65% | `internal/shell/session.go` | 76px 头、Chat/Trajectory、845px 消息列、工具卡、composer、用量 |
| Plugins | **3.17%** | 3.91% | `internal/shell/plugins.go` | 960px 列、Built-in 4 行、Experimental 行、50×50 图标砖、开关 |
| Settings | **2.31%** | 2.66% | `internal/shell/settings.go` | 800×800 模态、188px 导航、8 行设置、Appearance 三卡 |
| Automation | **1.34%** | 2.34% | `internal/shell/automation.go` | 960px 列、标题/New、筛选、搜索、空态 |

**比对回路（历史，已退役）**：`SCREEN=<name>` 跑当时的像素比对测试，产物为历史截图/差异图（`artifacts/`，已删除）与总览图（同上）。

**剩余差异的性质（已量化）**
- ~85% 是文本抗锯齿：基线是 Chromium LCD/subpixel AA，mygo 无头 `ui.Render` 是 pango/cairo 灰度 AA；同一渲染在容差 24/36/48 下 session 分别为 5.59/5.05/4.52%。这是渲染器地板，不是布局问题。
- 结构框（header/列宽/行高/卡片/分隔线/开关）与基线 CSS 抽取值差 0–1px。
- 图标：新增 `internal/icons`（Lucide，ISC）作为图标库；各屏对 DSH 无源图的图标用语义等价 SVG 近似——按“图标可以不一样、语义一致”的要求。

**下一步**：M0 剩余基础设施（NewTester 交互骨架、事件总线、SQLite 存储、单实例/托盘）→ M2 运行时（eino）。

## M2 运行时骨架：完成（2026-11-04）

- `internal/runtime`：eino ADK（ChatModelAgent + Runner 流式）→ `state.Event` 流；Provider/Tool 接口与 Registry；假模型/假 Provider 10 个测试
- 事件契约：`Status Running → Token/Reasoning → ToolStart/ToolEnd → Status Idle → Usage`；失败以 `Error` 收尾；取消静默停止（2026-11-05 更新：Usage 改累计 delta/中断带 Reason，见 [ADR 0014](../decisions/0014-usage-lifecycle-contract.md)；再更新：非正常终态另写 `turn_end` 供逐轮标记，终态 Status 仍保留最近回合 Reason）
- 集成约定：启动时构建一个共享 Agent；提交时 `for ev := range Run(...) { broker.Publish(ev) }`；取消 ctx = 停止按钮
- M3 遗留（已在 runtime 报告标注）：多轮历史/checkpoint（ADK SessionStore/CheckPointStore）、审批/中断、真实 Provider 的 tool-call（需扩展 Chunk）

## 当前门禁（全绿）

- `CGO_ENABLED=0 go test ./...`：design / icons / runtime / shell / state / store 全过（历史记录，当时含 5 屏像素比对；像素门禁已退役）
- `go vet ./...` 干净；`gofmt` 干净
- `scripts/check-nocgo.sh` → **PASS (zero cgo)**（linux/amd64+arm64、darwin/arm64、windows/amd64）
- 依赖仅纯 Go：mygo v0.7.4、eino v0.10.0-alpha.35、modernc.org/sqlite v1.61.1

## M3 完成（2026-11-04）

- **runtime**：`Chunk.ToolCalls` 驱动工具回路；`History` 多轮历史；`Gate`/`ApprovalQueue` 审批（allow/deny/cancel）；`eventSink` 并发发送安全；21 测试
- **provider/openai**：OpenAI 兼容 SSE（DeepSeek/OpenAI），tool_calls 分片聚合、reasoning_content、usage/cache、取消；22 测试
- **session**：`Controller`（异步 Submit、Stop、OnUpdate、Snapshot、CreateSession/OpenSession、ListSessions、Wait/Close），store 持久化事件/消息/历史；测试含两轮对话、Stop、重开恢复
- **app**：`BuildServices`（store+broker+projector+controller）、`BuildAgentFromSettings`（`provider.api_key/base_url/model` 设置或 `DEEPSEEK_API_KEY` 环境）、`DataDir` 支持 `ZEOKEEL_DATA_DIR`；`OnUpdate → window.Invalidate`
- **UI 实时接线**：session 屏从 `Snapshot` 渲染消息/用量/阶段；发送/停止/新建会话/打开会话已接 Controller；无 services 时回退静态（像素测试不受影响）
- **headless 冒烟**：`ZEOKEEL_DATA_DIR=.cache/zk-data xvfb-run .cache/zeokeel` → 生成 `index.sqlite`，无 services 错误
- 已知剩余（M4）：审批卡片 UI、内置工具注册表（当前为空，模型请求工具会 fail-fast）、ADK checkpoint（现用 History 消息表）、真实模型 60s 超时配置、命令面板/@/附件等交互

## 门禁（全绿）

- `CGO_ENABLED=0 go test ./...`：app / design / provider / runtime / session / shell / state / store 全过（历史记录）
- `go vet ./...`、`gofmt` 干净；`scripts/check-nocgo.sh` PASS（四目标交叉编译）

## M4 完成：工具、审批、菜单与全链路接线（2026-11-04）

- **内置工具**（`internal/tools`）：read_file / write_file / edit_file / glob / grep / bash / web_fetch，全部限定 workspace root、路径穿越与 symlink 逃逸拒绝、输出上限、bash 默认关闭+超时杀进程组、web_fetch 拒绝私网/重定向到私网；测试通过
- **审批 UI**：`session.Config.Gate` + `PendingApprovals/Approve/Deny`；会话屏在 composer 上方渲染审批卡（Allow/Deny），静态路径不变
- **Composer 菜单**（2026-11-05 对齐 DSH）：`/` 命令面板 13 条（添加：/file /plan /feedback；指令：/permission /model /export + /clear /stop /help /new /settings /automate /plugins），分组标题 + 图标/本地化标题/命令名/右对齐说明、键入即过滤、↑↓ 高亮、Tab/Enter 执行、Esc 关闭；`/file`、`/permission`、`/export`、`/feedback` 均为真实本地行为（picker/权限写入/JSONL 导出/反馈落库），`/goal`、`/compact` 有意不提供（见 known-limitations）；未知 `/` 行仍作为普通消息发送给模型。`@` 工作区文件/目录提及（文件→会话分组、Tab 进入目录、路径面包屑、Esc 逐级返回、中间文本替换）；`+` 附件菜单（Files / Commands）。
- **App 全链路接线**：`BuildServicesWith` 先开 store，再构建 provider + 工具注册表 + 审批队列 + store-backed History + runtime Agent，最后接 Controller；设置键 `provider.api_key/base_url/model`（或环境变量），bash 等变更类工具走审批
- **headless 冒烟**：`ZEOKEEL_DATA_DIR=.cache/zk-data xvfb-run .cache/zeokeel` → 建库成功、无 services 错误
- （历史）像素基线未变：home 1.00% / session 6.48% / plugins 3.20% / settings 2.38% / automation 1.39%（masked，AA 地板；像素门禁 2026-11-05 已退役）

### 已知边界（非阻塞）

- ADK checkpoint 未接（多轮历史走 messages 表；失败/取消的回合从上次提交状态重试）
- 工具 root 暂用 DataDir（尚未按 workspace.root）；审批无“始终允许”
- 无 web_search/MCP/skills；`/` 命令为最小集；附件为占位菜单
- 真实模型工具调用需要可用 API key（provider 已实现 tool_calls 流式聚合）

## 门禁（全绿）

- `CGO_ENABLED=0 go test ./...`：app/design/icons/provider/runtime/session/shell/state/store/tools 全过（历史记录）
- `go vet ./...`、`gofmt` 干净；`scripts/check-nocgo.sh` PASS（四目标交叉编译）

## M5 完成：动画/交互/功能对齐（2026-11-04）

### 展示过程动画（与 DSH 对齐）
- 从 DSH 构建产物提取 motion tokens：100/120/150/160/200/300/800/1500ms、`cubic-bezier(.4,0,.2,1)` 等，落在 `internal/design/motion.go`；证据表（历史，已随基线归档删除）
- 已实现：流式文本 120ms 淡入+2DIP 上移（静止帧精确 1/0，支持 ReduceMotion）；运行中 spinner + “Deep diving for Xm Ys” 1.5s 三点逐现（c.After 驱动，不 60fps 重建）；工具卡 200ms 展开/收起 + 状态点 1.5s 脉冲；新消息 200ms 淡入/位移；Send↔Stop 内容/颜色 150/100ms 过渡；12 个虚拟时钟测试
- 静止帧与从未动画的渲染逐像素一致；五屏基线 diff 不变

### 交互（真实可用）
- More 菜单（Settings/About/Quit）、Settings 模态全部可交互并持久化：权限、语言 zh/en、主题 Light/Dark/System（即时生效）、字号 14/16、Work details、折叠/发送行为
- 快捷键：Cmd/Ctrl+N 新会话、Cmd/Ctrl+, 设置、Cmd/Ctrl+K 聚焦输入、Esc 关闭
- 会话重命名/删除（侧栏 ... / 右键 + 确认）、Toast 反馈、快捷键帮助
- 静态路径不变（像素测试不受影响）

### 功能补齐
- `internal/websearch`：web_search 工具（可配置 endpoint/apiKey/maxResults）
- `internal/mcp`：stdio MCP 客户端管理器 → `mcp__<server>__<tool>` 工具
- `internal/skills`：`<root>/skills/<name>/SKILL.md` + `skill` 工具（内容视为数据）
- App 接线：`tools` + `websearch` + `skills` + `mcp`（`mcp.servers` 设置或 `mcp.json`）→ 注册表；MCP 管理器随 AgentBundle 关闭
- 设置键：`websearch.endpoint/api_key/max_results`、`skills.root`、`mcp.servers`

### 门禁（全绿）
- `CGO_ENABLED=0 go test ./...`：app/design/mcp/provider/runtime/session/shell/skills/state/store/tools/websearch 全过（历史记录）
- `go vet`、`gofmt` 干净；`check-nocgo.sh` PASS（四目标）
- headless 冒烟：建库成功、无 services 错误
- 像素 diff：home 1.00 / session 6.48 / plugins 3.20 / settings 2.38 / automation 1.39%（masked，AA 地板）

### 已知边界
- 真实模型需 API key；web_search 为通用 HTTP 适配器（非厂商原生 payload）；MCP 工具名可能超 provider 64 字符限制；审批未覆盖 mcp__*/web_search（可在 predicate 增加）

## M6 完成：功能补齐（2026-11-04）

### Agent 能力
- **Trajectory 实时时间线**：Chat/Trajectory 真实切换，读 store 事件（token/reasoning 聚合 ×N、tool_start/end、approval、usage、status、error），逐行展开原始 JSON
- **Todo/计划**：`todo` 工具 + 会话右侧 Todo 面板（done/total、状态、完成划线）
- **ask_user**：非阻塞提问工具 + 回答卡片（回答作为下一轮用户消息提交）
- **present/交付物**：artifact 卡片（标题/路径/inline Markdown 展开）
- **压缩**：`LLMCompactor`（真实模型摘要 + 旧工具结果裁剪，保留 system + 最近 12 条；`DefaultLLMSummaryTimeout` 20s 超时后回退 `WindowCompactor`），ContextBudget 24000，app 已接线；压缩在回合 goroutine 内执行并先发 `PhaseRunning`，不阻塞 `Controller.Submit`/UI 主线程
- **Checkpoint**：`CheckpointStore` 已接 app 的 SQLite checkpoints 表：每成功回合写入（压缩后 prior + 本回合消息），独立冷启动可恢复；生产以 SQLite History 为准，checkpoint 作为镜像；删除会话级联删除
- **Always allow**：`ApprovalQueue.AllowAlways/ClearRules`（运行时可用，UI 按钮待接）

### 宿主/UI
- **Settings › Models**：真实 CRUD + 默认模型 + 推理强度，provider.* 同步；live composer 已加模型徽标
- **Built-in plugins**：开关持久化（`plugin.<name>.enabled`），app 在下一次构建 agent 时应用（shell/web_search/skills/mcp），实验项仅持久化
- **Automation tasks**：SQLite 表 + cron 解析（*/N、M H * * *、@hourly/@daily）+ 纯 Go Scheduler；UI 列表/新建/编辑/删除/启停/搜索/Run now；app 定时触发 = 新建会话并提交 prompt
- **Workspaces**：真实工作区列表、创建（名称+目录 Browse）/切换/重命名/删除（级联），`ui.workspace` 持久化恢复
- **Session 搜索/归档**：标题+消息内容 LIKE 搜索、Cmd/Ctrl+K、排序（Last updated/Manual）、归档/取消归档
- **右侧边栏**：Files（面包屑/目录/文本预览 256KB/截断与二进制提示）、Terminal（cwd=workspace root，30s 超时，无 PTY/非持久 shell）

### 门禁（全绿）
- `CGO_ENABLED=0 go test ./...`：app/automation/design/mcp/provider/runtime/session/shell/skills/state/store/tools/websearch 全过（历史记录）
- `go vet`、`gofmt` 干净；`check-nocgo.sh` PASS（四目标）
- headless 冒烟：建库成功、无 services 错误
- 五屏像素 diff 不变：1.00 / 6.48 / 3.20 / 2.38 / 1.39%（masked）

### 仍未做（非阻塞）
- 真实模型 E2E（需 API key）；Agent Teams/subagent/goal/workflow；MCP/web_search 审批门控；always-allow 的 UI 入口；i18n 全量；三平台安装包/签名/自动更新；深色/窄屏逐屏基线

## M7 完成：E2E / Agent 模式 / 审批门控 / i18n / 深色窄屏（2026-11-04）

### 真实模型 E2E
- `internal/e2e`（`//go:build e2e`）+ `scripts/e2e-model.sh`（从 `$DEEPSEEK_API_KEY` 或 `~/.dsh/.credentials.yaml` 读取，永不打印 key）
- 实测 PASS：真实端点一轮对话，assistant text 32–68 字符、usage in/out >0、phase idle

### 审批门控 + Always allow
- 默认审批工具集 `bash,write_file,edit_file,web_search,mcp__*`（`approval.tools` 设置可配，前缀/通配）
- 审批卡新增 **Always allow**；Controller：`ApproveAlways(callID)` / `AllowAlways(tool)` / `ClearApprovalRules()`，测试覆盖放行后下一轮不再询问、清规则恢复

### Agent 模式 / Team / Goal / Workflow（用 eino 框架能力）
- 模式：Standard（ChatModelAgent）、PTC（Standard + pipeline 式 `ptc` 批处理工具）、Minimal（仅 `terminal_*` / `read_image` / `working_directory`）、Creator（Standard + `settings_get` / `settings_set` 与 SKILL.md 写入）；旧会话的 Deep / Supervisor / Plan-Execute 拓扑仅作为兼容别名保留。`Agent.SetMode` 在回合间原子重建并按代次清理子代理缓存，会话模式持久化 `session.<id>.mode`，shell 模式选择器 + app switcher 已接线；`agent.preset` 在 controller 的统一建会话路径应用（含 automation）；shell 的 Home 模式 chip 与 Settings > Agent presets 读取时同样经 `runtime.NormalizeMode` 归一化，别名（如 `cordis`）显示/高亮/新建会话三者一致
- 工具：`subagent`（深度限制 2，受限工具子集）、`goal`（set/get/complete/abandon + 会话 Goal 折叠 + 进度 banner）、`workflow`（顺序子任务，步骤/深度上限）
- UI：模式选择器、Goal banner、Team 面板（teammates + 模式徽标）已接入 live session

### i18n 全量
- `internal/shell/i18n.go` 350 键 EN/ZH，覆盖侧栏/首页/会话/插件/设置/自动化/菜单/对话框/Toast/快捷键/审批/Todo/Trajectory/右栏/模型/模式/Team
- 静态 `App{}` 渲染英文（像素基线不变）；live 默认中文（`ui.language` 可切 en/zh，立即生效）

### 深色 / 窄屏逐屏基线
- 像素比对测试（历史，已退役）：5 屏 × dark 1600×1000 + narrow 1200×800，渲染/差异图写入历史产物目录（`artifacts/`，已删除），文档随基线归档删除
- dark 预算内：home 12.54%、session 22.49%、plugins 4.05%、settings 3.66%（高值来自基线截图开着右栏/历史不同，已记录）

### 门禁
- `CGO_ENABLED=0 go test ./...` 全绿；`go vet`（含 `-tags e2e`）、`gofmt` 干净
- `check-nocgo.sh` PASS（四目标）；headless 冒烟建库成功
- 浅色五屏像素 diff 不变：1.00 / 6.48 / 3.20 / 2.38 / 1.39%（masked）
- 真实模型 E2E 通过（3 次运行一致）

### 边界
- eino `adk/prebuilt/deep` 需 `doublestar` 进 go.mod，当前用 ADK 原语组合等价 deep 拓扑
- 会话模式共享单 Agent（切换全局生效，最后切换者胜）
- dark 基线 home/session 含右栏/不同历史，预算为准

## M8 完成：Agent Loop 工具与提示词对齐（2026-11-04）

### 提示词层（DSH 同构）
- 新增 `internal/prompt`：40 个有序 section（identity/persona、workdir/sandbox/approval、plan/team/agent-mode policy：ptc/minimal/creator、逐工具提示、file reference、session-query/goal/workflow/subagent/report、MCP、deliverable/structured-output、harness 说明等），EN/ZH 双语、按 `runtime.ModeTools` 裁剪、动态注入 AGENTS.md/技能目录/时间
- App 已接线：构建 agent 时用当前模式的有效工具集（`runtime.ModeTools`）+ 设置语言 + `AGENTS.md` 组合 instruction；文档 `docs/prompts.md`（40 行 DSH 对照表）
- 有意跳过：LSP、computer-use、tools SDK、ralph、per-MCP-server 指令、DSH 仓库/Web URL 上下文（原因见文档）；PTC 策略已以 `mode:ptc` 实现（pipeline 批处理，非 JS 沙箱）

### 工具补齐（先查框架，能复用则复用，否则纯 Go 自研）
- **terminal_open/list/read/send/signal/close**：持久 `sh -l` 会话（stdin 管道、256KB 回滚、30s 空闲回收、进程组 kill；无 PTY 已文档化）
- **job_start/list/output/kill**：后台任务管理器（脱离工具调用生命周期、256KB 输出、8 并发上限）
- **pwsh**：PATH 中 pwsh/powershell 存在则执行，否则明确报错
- **str_replace_editor**：view/create/str_replace/insert/undo_edit（路径限定 workspace、20 份 undo）
- **read_image**：PNG/JPEG/GIF/WebP 元数据 + ≤2MiB data URL（provider 当前文本，视觉接入待多模态）
- **session_query**：session_search / session_event_search/read/trace / session_trace（SQLite 只读）
- **schedule_create/list/update/delete**：复用 automation cron 校验与 store
- **subagent 控制**：list_subagent_models / send_message / interrupt_agent（新 `internal/agentteam` 异步管理器；eino backgroundtask 评估后不适用）
- **working_directory**：get/set（限 workspace 内，拒绝绝对路径/`..`/symlink 逃逸）
- **report**：结构化 JSON（16KB 上限）
- 框架结论：eino `adk/filesystem` 仅一次性 Execute、`adk/backgroundtask` 是重型 durable task 机制，均不匹配；终端/Jobs/编辑器/图片/团队控制纯 Go 自研；WebP 用已在依赖中的 `x/image/webp`

### 调用验证（“真的能被调用”）
- `TestProductionToolsInvocable`：按生产路径构建注册表（≥30 工具），逐工具校验名称/描述/不 panic；功能子集必须成功（read/write/edit/glob/grep/editor/image/bash/todo/goal/present/skill/session_search/schedule/working_directory/report/job/terminal/team-list），并跑通 terminal open→send→close 往返
- 真实模型工具调用 E2E：`TestRealModelToolCall`（build tag e2e）+ `scripts/e2e-tools.sh`，实测 PASS：模型真实发起 `todo` 工具调用并完成
- 全量门禁：`go test ./...`（含 agentteam/prompt）全过；`go vet`、`gofmt` 干净；`check-nocgo.sh` PASS；headless 冒烟成功；浅色五屏像素 diff 不变

### 仍未对齐（有意/边界）
- ralph、cordis inspect、LSP、computer-use、tools SDK、worktree/agent-team 实验包、MCP resources/prompts（PTC 已以 pipeline 批次工具实现）
- read_image 仅文本化返回，未接入多模态请求；终端无 PTY（无 TTY echo/行编辑）

## M9 完成：可行工具批次（2026-11-04）

- **worktree**：`enter_worktree` / `exit_worktree` / `list_worktrees`（git CLI，root 内限制，非 git 仓库明确报错）
- **inspect**（Cordis inspect 的 ZeoKeel 替代）：`inspect` 返回 plugins/settings(脱敏 api_key/secret)/sessions/skills/mcp 快照
- **MCP resources/prompts**：Manager 新增 `ListResources/ReadResource/ListPrompts/GetPrompt`（分页、256KiB/item、1MiB/read、10s 超时）；工具 `mcp_list_resources` / `mcp_read_resource` / `mcp_list_prompts` / `mcp_get_prompt`
- **Agent Team 工具层**：`spawn_team` / `list_teams` / `team_status` / `close_team` / `wait_team`（复用 `internal/agentteam`，新增批量子代理、按 id/name 关闭、团队快照）
- **ralph**：fresh-agent 轮次循环（共享 objective、上轮摘要 2KiB、完成标记 DONE/COMPLETE、默认 3 轮上限 10、depth 守卫、runner binder）
- 全部注册进生产 registry；`TestProductionToolsInvocable` 增加 `inspect` 必过项；`go test ./...` 全绿，`go vet`/`gofmt` 干净，`check-nocgo.sh` PASS，headless 冒烟正常，浅色五屏像素 diff 不变，真实模型工具调用 E2E 通过

### 本次明确不做（已评估）
- Cordis 真运行时（与内置插件边界冲突）、LSP、computer-use、tools SDK/PTC JS 运行时与沙箱（ZeoKeel 的 PTC 是 pipeline 批次工具）、read_image 多模态请求、终端 PTY

## M10 完成：多模态 + 终端 PTY（2026-11-04）

### 多模态（图片真正进入模型）
- `runtime.Message` 新增 `Images []ImagePart{URL,Detail}`；OpenAI 兼容请求在有图时发送 content 数组 `[{type:text},{type:image_url,image_url:{url,detail}}]`，纯文本消息仍保持字符串形式（字节级不变）
- Agent loop 注入：工具结果 JSON 含 `image_data_url`（`data:image/...`，≤2MiB，去重，单轮最多 4 张）时，自动追加合成 user 消息 “Image returned by tool <name> (attached).”，eino middleware 覆盖 standard/deep/supervisor/subagent，planexecute 由 provider 适配层同样注入；PTC 批次结果 `results[].output` 中嵌套的图片（如批内 `read_image`）按相同上限/去重规则注入，而非仅以转义文本返回
- `read_image` 改为返回 JSON `{kind,path,format,mime,width,height,bytes,image_data_url,note}`；超限时省略 data URL 并注明
- 限制：图片为 runtime-only（合成消息不进 History/checkpoint）；不做能力探测，纯文本端点可能拒绝请求；data URL 上限 2MiB

### 终端 PTY
- Unix（linux/darwin）用 `github.com/creack/pty`（纯 Go、无 cgo）启动真实伪终端，`terminal_open/list` 返回 `pty:true`；读/写走 PTY master，signal 发进程组，close 清理；PTY 启动失败自动回退管道（`pty:false`），Windows 保持管道
- 验证：echo/行编辑、`cd` 后 `pwd` 持久、SIGINT 中断 `sleep`、并发 send/read/close、关闭清理；`GOOS=windows` 交叉构建通过
- 依赖：`github.com/creack/pty v1.1.21`（go.mod 直接依赖）

### 门禁
- `go test ./...` 全绿；`go vet`、`gofmt` 干净；`check-nocgo.sh` PASS；headless 冒烟正常；浅色五屏像素 diff 不变；真实模型工具调用 E2E 通过

### 真实 LLM 端点验证（可达网络，凭据仅环境变量）
- `/v1/models` 返回 qwen3.8-flash-next（input: text+image，ctx 1,048,576）
- 文本：回复 "pong"，usage in=59 out=37
- 图像（provider 直发 content parts）：纯红 PNG → 回复 "Red"，usage in=74 out=52
- 工具→图像链路：`read_image` 调用成功 → 运行时注入图片 → 模型回复 "Red"
- 生产注册表真实工具调用：`todo` ToolStart+ToolEnd 完成
- Session Controller 真实回合：assistant 文本 29 字符，usage 75/40，phase idle
- 端点/Key 不写入仓库（仅 env）；`.gitignore` + `.probe` 历史缓存已脱敏

## M11 完成：压缩异步化 + 摘要超时 + checkpoint 文档对齐（2026-11-05）

- **压缩不阻塞 Submit/UI**：`Agent.Run` 只同步加载 prior History；`Config.Compactor` 移入回合 goroutine，先发 `PhaseRunning` 再摘要，失败/取消以 `state.Error` + 失败/空闲终态事件收尾，不再作为 setup error 返回
- **摘要超时**：`LLMCompactor.Timeout`（零值 `DefaultLLMSummaryTimeout` = 20s，短于 provider 60s）限制单次摘要请求，超时回退 `WindowCompactor`；caller 取消仍如实返回错误
- **app 接线**：`buildAgent` 显式配置摘要超时；压缩仍在回合锁之前执行，不串行化无关会话
- **checkpoint 文档对齐**：`FUNCTIONAL-PARITY` / `STATUS` 改为描述 LLM 摘要 + WindowCompactor 回退 + 工具结果裁剪，以及 store-backed checkpoint（独立冷启动恢复 + 生产 History 权威镜像）
- **验收**：`TestLLMCompactorSummarizes`、`TestCompactorFallbackOnError`、`TestToolResultPruning`、`TestCheckpointPersistResume`、`TestCheckpointDeletedWithSession` 通过；`sh scripts/ci.sh` PASS（含 race；当时的像素基线步骤已于 2026-11-05 退役）

## M12 完成：用户附件上传（文件/图片进入会话）（2026-11-05）

- **选择与暂存**：会话和 Home composer 的 “+” → Files 都打开真实多选文件对话框，选中的文件渲染为可移除 chip。会话内选择立即复制到 `<data dir>/attachments/<session id>/<id>-<name>`；Home 尚无会话时按 Home draft 暂存路径（不复制、不写库、不碰上一个会话），无当前会话也可先选文件，Home Send 在 `CreateSessionInWorkspace` 之后、首次 `Submit` 之前把文件复制到新会话，工作区 hero 切换也会随 Home draft 转移。
- **限制**：每条消息最多 5 个附件，单文件最大 25 MiB，文本附件注入模型 prompt 的上限 64 KiB；超限、非普通文件或源文件消失会在选择或提交前拒绝，整批校验失败不会留下半复制的行或文件。
- **持久化**：`attachments` 表保存 `id / session_id / message_id / name / path / mime / kind / size / created_at`；`message_id` 为空表示 pending，提交后链到对应 user message；文件字节存放在 SQLite 之外，读取/删除时重新校验路径必须位于附件根目录。重启后 pending 与已发送附件均可恢复，历史重放会把文本和图片重新送入模型。
- **注入模型**：文本附件以 `<attachment name="...">…</attachment>` 块追加到用户 prompt（≤64 KiB），附件名中的 `&`、`"`、`<`、`>` 会做 XML 转义（`TestAttachmentPromptEscapesName`），不会破坏 header 语法；图片转成 `data:<mime>;base64,...` 的 `runtime.ImagePart`，由 OpenAI 兼容 provider 作为 `image_url` content part 发送；History 适配器按消息重建同样的文本块与图片 data URL，chip 本身不展开正文。
- **清理**：删除 chip 会删除附件行和已复制文件；删除会话级联删除附件行并移除该会话的附件目录。
- **验收**：`TestAttachmentCaps`、`TestAttachmentTextInjected`、`TestAttachmentImageContentPart`、`TestAttachmentPersistsReopen`、`TestAttachmentCleanupOnDelete`、`TestAttachmentPickerCopies`、`TestAttachmentChipRemove`、`TestAttachmentHistoryReplayed`、`TestAttachmentHomeFollowsSend`、`TestAttachmentHomeChipRemove`、`TestAttachmentHomeWithoutCurrentSession` 通过；`go test -run 'TestAttachment' ./internal/session ./internal/shell ./internal/app ./internal/store` 与 `sh scripts/ci.sh` PASS（含 race；当时的像素基线步骤已于 2026-11-05 退役）

## M13 完成：fork / 导出携带附件（2026-11-05）

- **fork 复制附件**：`Store.ForkSession` 在同一事务内新增 `copyForkAttachments`：附件行换新 id，`message_id` 经消息 id 映射指向 fork 的 user message（pending 保持 pending），字节复制到 fork 自己的 `<data dir>/attachments/<fork>/<id>-<name>`；删除 fork 只清理 fork 的行与文件，源会话不受影响；事务未提交时清理已复制文件。源文件缺失或路径越界时跳过该附件而不是让整个 fork 失败。
- **fork 续聊重放**：fork 的附件行与字节使 History 适配器在 fork 中继续对话时仍重放文本 `<attachment>` 块与图片 data URL，不再丢失源会话的附件上下文。
- **导出附件**：`Store.ExportSessionJSONL` 新增 `type:"attachment"` 记录（id/session_id/message_id/name/path/mime/kind/size/created_at），并内嵌 `encoding:"base64"` 的完整字节与文本附件的 `text` 正文，使 session log 自包含；文件缺失时仍导出元数据记录而不中断导出。
- **验收**：`TestForkAttachmentCopyKeepsTextAndImage`、`TestForkAttachmentReplaySnapshot`、`TestForkAttachmentHistoryReplaysInForkTurn`、`TestExportSessionAttachmentRecordsAndPayload`、`TestExportSessionAttachmentLines` 通过；`go test -run 'TestFork|TestExportSession|TestFeedbackPersists' ./internal/session ./internal/store ./internal/shell` 与 `sh scripts/ci.sh` PASS（含 race；当时的像素基线步骤已于 2026-11-05 退役）

## 工程收尾：UI 渲染性能基准（2026-11-05）

- 新增 `internal/shell/render_bench_test.go`：`BenchmarkRender*`（Home/Session/Plugins/Settings/Automation/Sidebar，1600×1000 + 900×600，live fixture，`ui.Render`）与补充的 `BenchmarkWarmFrame`（稳态帧，`tt.Frame()` 热缓存），补齐 EVOLUTION G10 的"0 benchmark"缺口。
- 基线与解读见 [docs/design/perf.md](../design/perf.md)：稳态帧 0.48–1.26ms/屏（最重 Sidebar 工作区树 1.26ms、Session 1.09ms，均远低于 60fps 的 16.7ms 预算）；`ui.Render` 冷渲染 11.5–31.4ms 为一次性启动+settle 成本，不代表每帧成本。
- 复现：`go test -run '^$' -bench 'BenchmarkRender' -benchmem ./internal/shell`（稳态帧用 `-bench 'BenchmarkWarmFrame'`）。
- 同轮清理：删除零引用死代码（layout.go 的 `pageColumnWidth`/`narrowSideInset`/`designGutter`、menus.go 的 `workspaceSection`/`liveSessionRow`/`sessionMenu`/`overlays`/旧 palette 三函数、composer_style.go 的 `switchTrack`），并把 scoped 文件中已退役的 baseline/DSH 注释改为中性描述。
