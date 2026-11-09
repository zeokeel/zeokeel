# 深度交互轮（2026-11-09）：五路并行审计与修复

> 背景：用户反馈「大部分问题都是 UI 的 bug；智能体基础 UI 不够好；要仔细检查交互，
> 参考市面上的智能体；思考深度不够——支持了智能体团队，就必须有地方看团队；每个功能
> 要更深入、更细节地看清楚」。
>
> 本轮不是一次性打磨，而是一次**系统性的交互深度审计 + 分工作流修复**：5 个只读审计
> agent 各自用探针（真实 assembly + `ui.NewTester` + 脚本化模型服务器）复现问题、给出
> 文件:行号与真实帧截图；Lead 汇总分级后，按写范围拆成 5 条修复工作流并行落地，再统一
> 集成验证。全部结论可复验，证据帧存放在 `.cache/` 下（已 gitignore）。

---

## 1. 方法与基线

- 基线：`release v0.1.2`（`900b8e1`）+ 未提交的「真实会话反馈轮」改动。
- 审计技能：`design-guide`（含 MyGo/ZeoKeel adaptation）与 `mygo-maintenance`。
- 证据来源（每个 finding 均带）：
  - 真实产品帧：`go run ./internal/devtools/ui-snapshot .cache/<dir>/shots`（1280×800，
    含 narrow/dark/english）；
  - 交互探针：`.cache/audit-*/probe/`（真实 desktop assembly + `ui.NewTester`，逐帧
    驱动键盘/鼠标/对话框）；
  - 交互驱动：`go run ./internal/devtools/ui-drive`（84 步功能矩阵，PASS/FAIL 判定）。
- 对照基线：DSH harness（本产品对标的智能体运行时：Agent Teams、共享任务板、计划/goal、
  steer/interrupt、后台作业）、Claude Code、OpenAI Codex、Cursor Agents、Windsurf、
  Cline、Zed、Devin、OpenHands。只提炼交互模式，不做视觉照搬。

## 2. 审计结论汇总

五路审计共记录 **131 条可复现 finding**（去重前，含跨区重复），其中 P0 2 条、P1 32 条。
最重要的系统性问题不是控件选型，而是**信息与状态的深度**：

| 区域 | 审计报告 | 条目 | P0/P1 | 一句话结论 |
|---|---|---:|---:|---|
| 会话核心（transcript/工具卡/流式/usage） | `.cache/audit-session/report.md` | 28 | 0/8 | 过程不透明：工具调用只显示结果摘要、看不到输入（命令/参数/diff）；失败/取消恢复路径断裂 |
| 输入区/浮层/键盘 | `.cache/audit-composer/report.md` | 24 | 1/5 | 边界状态不完整：计划审阅死锁、长草稿推出窗口、发送失败静默、运行中 slash 被排队 |
| 团队/子代理/任务板/后台 | `.cache/audit-team/report.md` | 26 | 4/15 | **团队是藏起来的名单**：默认界面无「团队」，3 步才打开、命令面板零命中；状态把可用性当运行中；任务板不自动刷新、无 owner/依赖 |
| 壳层/导航/侧栏/响应式 | `.cache/audit-shell/report.md` | 24 | 0/2 | 危险操作默认不安全（删除会话 Enter=删除）；Esc/返回键被 tooltip/输入框抢占；布局状态只持久化一半 |
| 设置/自动化/插件 | `.cache/audit-settings/report.md` | 30 | 0/5 | 可发现性断裂（搜索只认标题）；语义不可信（更新页必然失败、诊断复制泄露非 chat 密钥、自动化「成功」只代表已投递） |

**关键判断**：

1. **智能体基础 UI 的深度 = 过程可核对 + 状态真实 + 恢复路径完整**。本轮所有 P1 都落在这三点。
2. **功能存在 ≠ 功能可用**。团队、任务板、todo、后台作业等能力已经写进后端工具，但 UI 浅或
   无入口，用户感知不到——这就是「思考深度不够」。本轮把团队与任务板从「名单/列表」升级为
   「可解释的运行视图」，并在页头/命令面板补上一步入口。
3. **细节 bug 多来自默认值与键盘路径**：危险确认默认删除、Esc 被 tooltip 吃掉、输入框内
   Alt+← 触发导航、鼠标发送后丢焦点、失败静默——全部按「桌面/键盘优先」约定修正。

## 3. 本轮落地修复

### 3.1 团队可见性（用户点名的例子）

- **页头团队 chip**：会话标题右侧显示「团队 N / 团队 N 运行中」，点开即「打开团队面板 /
  打开任务板」；无团队时不占位。窄 header 只显示数量。
- **一步可达**：命令面板搜索「团队/任务」命中 `rightpanel.team.open` / `rightpanel.tasks.open`
  （稳定 ID），检查器关闭时先打开再切 pane；pane 选择器按「编排/工作/记录」分组。
- **真运行态**：rig 的 `Member.Status` 只是可用性；后端新增运行态跟踪（running/idle/done/
  failed/interrupted）、错误、模型、开始/结束时间、token/步数，面板显示真实状态而不是
  「active→运行中」。
- **团队面板分三段**：主代理 / 队友（状态、当前活动、耗时·步数·token、活动时间线、产出
  复制、steer/中断/关闭）/ 同步子代理（只读分组与说明）；空态解释团队能力并给「新建队友」
  CTA + 原生 spawn 表单（名称/自包含任务/模型）。
- **完成通知**：队友完成/失败时走既有通知链刷新并弹 toast「X 已完成 · 查看」（按完成时间
  去重），点击打开团队面板。
- **任务板深度**：状态分组标题+计数、负责人显示/认领菜单（未认领/主代理/队友）、依赖
  `等待 task-X 完成`/`可开始`、写入范围、Enter 展开详情、`⋯/右键` 菜单；任务板随 chat
  通知自动失效缓存（此前 agent 写任务后面板一直显示旧数据）、显式刷新有 spinner。
- **工具卡交叉导航**：spawn_teammate / subagent / team_task_* 卡片展开后可「在团队中查看」
  「打开任务板」。

### 3.2 会话核心（基础智能体 UI）

- **工具输入与输出可见**：工具卡展开显示「输入」（bash 命令、文件路径、edit 的替换前/后、
  其余 JSON 美化）与「输出」（此前只渲染 200 字符摘要且无法查看完整输出；现在有界摘要
  4KB、可复制、截断有提示）；JSONL 导出补 arguments。
- **失败与取消语义**：失败回合的「重试」恢复可用（错误行直接重试）；用户取消不再画成红色
  「失败」+ 英文 `context canceled`，改为中性「已取消」；会话级错误切换会话时清空。
- **键盘与焦点**：运行中 Esc / Cmd+. 中断（菜单优先）；鼠标发送/排队后焦点回到输入框；
  长草稿有最大高度（900×600 下不再把发送/停止推出窗口）；Cmd/Ctrl+Enter 发送；
  宽 composer 内联「Enter 发送 · Shift+Enter 换行」提示。
- **流式与动作行**：生成中隐藏复制/评分/重生成动作行；复制按钮 32×32 命中区、1.8s 回落；
  「重新生成」收进消息动作行，usage 明细去掉「预算 预算」。
- **输入区边界**：发送失败不再静默（composer 内 RoleAlert，草稿保留）；运行中 slash 草稿
  不再被当普通消息排队；排队投递失败有「重试」；@ 菜单跟随光标（句中也触发），接受后光标
  正确移动；斜杠菜单重开高亮首项。
- **消息元数据与用户消息动作**：消息行显示时间（HH:MM，Tooltip 完整日期时间）；用户消息
  可复制原文，也可「编辑消息」把原文填回输入框（不自动发送、不删历史）。
- **记忆页**：搜索改为 180ms 防抖 + 后台读 + 回填（此前每次击键在帧内同步读 SQLite），
  添加记忆支持 Enter 提交。

### 3.3 计划审阅与浮层（P0）

- **计划审阅死锁修复**：先投递反馈再退出计划（此前先 Exit 再 Send，任何 Send 失败即进入
  不可关闭的 `planHold`，只能重启）。现在 Send 失败保持 reviewing 可重试；Esc 在错误态可
  关闭；反馈已发送但 Exit 失败时只重试 Exit。
- **ask_user 安全默认**：选项对话框不再默认聚焦第一项（stray Enter 曾直接把第一项当答案），
  首帧焦点在「取消」，显式 Tab/点击选项才提交；并新增「其他…」内联自定义回答（Esc 返回选项）。
- **toast 去重**：同 level+文字在 TTL 内合并为一条并显示 ×N、重置计时。

### 3.4 壳层与导航

- **危险确认默认安全**：删除会话/工作区改为无默认按钮的确认（Enter = 取消，只有显式点击
  「删除」才删除）。
- **Esc/键盘**：抽屉关闭按钮不再挂 keyboard tooltip（此前按一次 Esc 只关 tooltip）；抽屉
  关闭后焦点归还工具栏开关；关闭态按 pane 循环快捷键不再静默改隐藏 pane（给一次提示）；
  输入框聚焦时 Alt+← 不再触发全局返回。
- **布局状态**：侧栏折叠 / 检查器开关持久化（此前重启即丢）；侧栏拖拽实时 clamp 200–420
  （此前可拖到 40px 破版、重启跳变）；检查器只在会话屏可打开；900×600+rail 停靠态有
  面板级关闭 X；工作区上下文出现在 Home composer、会话头与概览 pane。
- **右栏 pane 深度**：日志 pane 支持类型/内容过滤、刷新、完整事件展开与复制、真实计数
  （窗口/过滤后的条数）；Files 目录、文件预览与 Changes git 读取全部移出 UI 线程（大仓库/
  网络盘不再卡帧）并显示 Spinner；终端支持复制全部输出与「已暂停跟随/回到底部」；rail
  目的地暴露可访问选中态；Home 最近会话排序（置顶优先）与侧栏一致。
- **窄空间信息优先级**：任务板行把负责人/依赖移到次行（320 DIP 下标题整行可读）；会话
  页头在内容列 <720 DIP 时目标/团队 chip 只留状态，工作区名包成 shrink-proof chip 保持
  可见（此前长标题会把工作区名挤成 0 宽）。

### 3.5 设置/自动化/插件

- **安全**：诊断页「复制诊断信息」与导出报告共用同一遮罩（此前只遮 `chat.apiKey`，明文
  泄露 `web.search.apiKey`/MCP env）；密钥输入统一 `.Password()`。
- **首启**：缺少 API Key/模型时 Home 显示 setup 卡并可深链模型设置、composer 内联警告，
  不再让用户用第一次失败去发现未配置。
- **更新页**：未配置更新源时显示「当前构建未启用更新」，不再承诺下载/安装、不再暴露
  `update.feedURL` 内部键。
- **插件/MCP**：启用「设置中停用」的服务器不再导致卡片消失（保留「已启用（重启后连接）」）；
  刷新/重试连接移出 UI 线程（此前同步 Ping 会冻结窗口）+ busy 态；失败原因本地化摘要、
  原文可复制；MCP 环境变量按换行解析（值可含空格）并逐行校验；「待重启」按净变化显示。
- **自动化**：模型改为可选目录 + 未知模型警告；运行计划有字段级校验与「下次运行」预览；
  「立即运行」有进行中反馈，完成语义改为「已投递」并可跳到会话；编辑表单 Esc 取消、
  表单打开时不再同时显示空态 CTA；工作区显示名称、历史显示完整日期时间。
- **设置交互**：对话框首字段 AutoFocus + Enter 提交；通知页文案改为「应用内提示气泡」；
  数据页收敛单一 Primary；归档空态教用户从哪里归档。

## 4. 验证

命令均可复跑（`TMPDIR` 必须指向仓库外：`/data/mygo` 自身是 git 仓库，测试若在仓库内建
临时目录，`internal/modules/workspace` 的非 git 工作区判定会被父仓库影响）：

```bash
export PATH=/usr/local/go/bin:$PATH GOPATH=/data/mygo/.cache/gopath \
  GOCACHE=/data/mygo/.cache/go-build GOTMPDIR=/tmp \
  TMPDIR=/tmp GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off
gofmt -l internal cmd                                  # 空
go build ./...                                         # PASS
go vet ./...                                           # PASS
go test ./... -count=1                                 # PASS（41 个包全绿）
go run ./internal/devtools/ui-drive --out .cache/depth-drive --quick
go run ./internal/devtools/ui-snapshot .cache/depth-shots
```

- **交互驱动 `ui-drive --quick`：90 / 90 PASS（0 FAIL / 0 WARN / 0 BLOCKED）**，原 84 步
  加本轮 6 个团队/任务步骤（`team-chip` / `team-sections` / `team-spawn-form` /
  `tasks-groups` / `tasks-auto-refresh` / `palette-team`），报告
  `.cache/audit-team/drive/drive-report.md`。
- 全量帧 `.cache/depth-shots/`：79 张 1280×800（含 7 张 narrow/dark），新增
  `p2-team-chip`、`p2-team`、`p2-team-detail`、`p2-team-spawn`、`p2-tasks`、
  `p2-tasks-expanded`、`p2-palette-team`。
- 新增/修改交互均有 `ui.NewTester` 帧测试（团队运行态、spawn 表单、任务 owner/依赖/自动
  刷新、工具输入输出、Esc 中断、计划审阅三路退出、发送失败提示、长草稿高度、危险确认
  默认取消、布局持久化、密钥遮罩、消息时间/用户消息动作、记忆防抖等）。
- 证据帧：`.cache/audit-*/impl*/`（各工作流 before/after）、`.cache/depth-shots/`（合并后
  全量帧）。

## 5. 仍开放的深度项（下一轮候选，按用户影响排序）

1. **团队工作台 overlay**（980×740：名册 + 概览/成员/任务板；成员消息时间线、任务摘要、
   spawn 历史回看）。本轮已把「一步入口 + 真实状态 + 任务归属」做进检查器；工作台是
   完整形态（设计原型帧在 `.cache/audit-team/design/`）。
2. **后台作业面板**：job_start/job_output 目前只有工具卡，没有作业列表/状态/取消面。
3. **审批 diff 预览**：write/edit 审批仍只有摘要 + 原始 JSON；应显示将写入的 diff。
4. **图片粘贴与拖放遮罩**：Ctrl+V 粘贴截图、整窗「松开以添加附件」提示。
5. **轨迹页 IA**：按回合分组、耗时、点击定位、流式跟随。
6. **消息元数据补全**：时间已加，模型/耗时仍需后端 read model 字段；命令面板层级
   （palette 与模态 z-order、Esc 两段式、Cmd+K 关闭）。
7. **会话内查找（FindBar）**：长会话按内容定位。
8. **设置搜索索引**（页面内设置项可搜，当前只匹配页面标题）。
9. **任务板持久化**（当前为进程内内存，重启清空）与跨会话团队归属（团队 manager 是
   app 级单例，面板暂不区分会话）。
10. **桌面级通知**（窗口最小化时的完成/审批提醒）与窗口标题状态信号（host 层）。
11. **输入区剩余细节**：会话菜单在 900×600+运行中排队时尾行裁切、拖放 overlay、
    ask_user 多问题、快捷键冲突校验、Home composer 与会话 composer 能力对齐。
12. **历史会话工具参数回填**：重启恢复的历史会话，工具卡只有输出没有输入
    （restore 路径未回填 `arguments`；新会话完整）。

## 6. 审计/修复分工与写范围（复盘）

| 工作流 | 审计 | 修复（文件范围） |
|---|---|---|
| session | `.cache/audit-session/report.md` | `internal/ui/session/*`、`internal/ui/toolcards/*`、`internal/uikit/toolrow.go`、`internal/modules/agent/agent.go`（取消分类/摘要上限） |
| composer | `.cache/audit-composer/report.md` | `internal/ui/interaction/*`、`internal/ui/toasts/*`、`internal/ui/session/view.go` composer 区 |
| team | `.cache/audit-team/report.md` | `internal/contracts/{team,tasks}.go`、`internal/modules/agent/team*.go`、`internal/modules/toolkit/task_board.go`、`internal/ui/rightpanel/*` |
| shell | `.cache/audit-shell/report.md` | `internal/uiroot/*`、`internal/ui/sidebar/*`、`internal/ui/home/*`、右栏 pane 文件 |
| settings | `.cache/audit-settings/report.md` | `internal/ui/settings/*`、`plugins/*`、`automation/*`、`home/*` |

> 写范围互斥 + 同一文件内函数级分工（view.go：页头/工具行 vs composer），修复期间
> `go build ./...` 保持可编译，最终由 Lead 统一验证并更新本文档与 `CHANGELOG.md`。
