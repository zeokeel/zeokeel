> **评审图默认静息态；hover/focus 证据单独出图（名字带 `-hover` / `-focus`）。**
> 快照驱动为此提供 `renderRest()`（指针移开 + 200ms + 一帧后再拍）：`p2-file-diff.png`、`p2-memory.png`、`p2-context-sections.png`、`p2-capabilities-skills.png`、`p2-model-settings.png` 都是静息帧。
> 示例：`p2-file-diff.png`（静息，折叠段是裸 trigger）vs `p2-file-diff-hover.png`（指针停在「查看改动」上，出现 hover 底色）= 同一控件两种状态，**不是两种样式**。
> 审计时请先确认指针位置：需要点击才到达的场景，旧图里的灰底/高亮行多为 hover 残留，勿记为 chip/选中态缺陷。

# 全量逐屏视觉清点（119 张 · v-final 02:40 快照）

> 快照：`.cache/ui-conformance-final/`（generated 2026-11-18T02:40:47）
> 判据：顶带 48+36=84 · 圆角 4/6/8/12/16 · 接缝 1px `BorderL2`（亮 `#E9EAEA` / 暗 `#29292F`）· 正文 14/22 · 元数据 12 · accent 填充块 ≤2/屏 · primary ≤1
> 方法（重要，先读）：**A 目视整图**（55 张整屏 + 1 张 4× 裁剪 + 4 个状态变体差分裁剪）+ **B 程序化度量**（**全部 119 张**：画布/顶带/接缝/accent 连通块）+ **C 族差分**（12 组：同族变体与已目视图做 `compare -metric AE`，差异区单独裁剪再看）。
> 所有"对比度"结论以**裁剪区直方图/最暗像素**为准，不用单点采样（本轮因此纠正了 4 条误报，见 §5）。

---

## 1. 问题汇总（P0 → P2，按 owner 可派）

| # | 严重度 | 问题 | 位置（图 · 坐标 · 实测） | owner |
|---|---|---|---|---|
| 1 | **P1** | **滚动区底部切在半行字上**："超时 (ms)" 标签只剩上半、其 input 完全被动作行遮住；滚动视口底 y≈725，动作行 738 起，无渐隐/留白暗示 | `p2-capabilities-connector-form-stdio.png` · 模态 152..792 · label 基线 717 | **impl-settings** |
| 2 | P2 | **toast 压正文/宿主文字**（1512 宽同样发生）：消息元信息被截成 "03:38 · 用时 2.5s · 4…"；composer 提示被截成 "Enter 发送 · Shi" | `p2-goal.png` (1256..1495,595..680)、`p2-jobs.png`/`p2-jobs-stop-armed.png`/`p2-file-diff.png` (931..1166,866..926) | **impl-shell** |
| 3 | P2 | **同一审批弹两条内容相同的警告 toast**（中央锚定 + 右下） | `p2-team-chip.png` (766..1004,380..470) 与 (1256..1495,605..690) | impl-shell |
| 4 | P2 | **工具行四态不对称**：完成态没有右侧状态词，其余三态都有（运行中/失败/已停止） | `p2-tool-row-states.png` 行2 y=80 | impl-tools |
| 5 | P2 | **模态无关闭 ✕**；且「取消计划 / 继续规划」两动作视觉完全同权（无主次） | `p2-plan.png` 模态 426..1086,306..637 | impl-tools |
| 6 | P2 | **collapsible trigger 两种样式**：连接器选项/查看改动=有灰底 chip；技能目录=无底 | `p2-capabilities-connectors-options.png` (307..380,452..476) vs `p2-capabilities-skills.png` (307..435,461..483)；`p2-file-diff.png` (1200..1290,140..164) | **impl-settings**（uikit 统一，settings 主导） |
| 7 | P2 | **技能正文 H2 用 18px**，落在 20/16/14/12 阶梯之外 | `p2-capabilities-skill-content.png` "发布检查" y≈410 | impl-settings |
| 8 | P2 | **说明文字暴露内部绝对路径**（`/tmp/zeokee1-tmp/zeokee1-snapshot-workspace1604…/mcp.json`） | `p2-capabilities-connectors-options.png` y≈506 | impl-settings |
| 9 | P2 | **表单动作行间距不足**：最后一条字段说明 → 按钮行仅 ~20（应 24）；必填字段（名称/URL）无标记、空表单时"添加"仍为启用态 | `p2-capabilities-connector-form.png` y=707→738；按钮 957..1015 | impl-settings |
| 10 | P2 | **团队浮层三处空态冗余**（0 名成员 / 还没有队友 / 还没有共享任务）；toast 紧贴浮层下缘 10px | `p2-team-overlay.png` / `p2-team-history.png` | impl-shell |
| 11 | P2 | **"关闭「研究员」" 是副文本却写成动作词**（像按钮）；且"已完成"与"历史·可继续"两条状态并列 | `p2-team-history.png` 卡副行 y=186 | impl-shell |
| 12 | P2 | **子代理页底部输入不是 composer 形态**（裸 input + 按钮，无卡/无工具栏），像未完成的 composer | `p2-team-child.png` (417..1303,848..878) | impl-shell |
| 13 | P2 | **模型菜单把"打开模型设置…"与选项混排**，无分隔线 | `p2-modelmenu-models.png` 一级菜单 y=783 | impl-session |
| 14 | P2 | **composer 空态垂直留白偏大**：首行 y750 → 动作行 y832，中间 ~60px 空 | `p2-running-composer.png` | impl-session |
| 15 | P2 | **"确认停止" armed 态变宽导致同行元素左移**（布局跳动） | `p2-jobs-stop-armed.png` 按钮 44→74px | impl-shell |
| 16 | P2 | 能力卡**徽章定宽缺失** → 两卡 mono 路径列错位 ≈12px（沿用上轮） | `p2-capabilities-skills(-dark).png` | impl-settings |
| 17 | P2 | **模型列表空态无 CTA**（"还没有模型" 纯一行，添加入口在页头另一处） | `p2-model-empty.png` y≈305 | impl-settings |
| 18 | P3 | 连接器详情里"连接失败"是**纯红字**，卡片上是 badge → 同一状态两种形态；且下方"服务器未完成 MCP 初始化"与之语义重复 | `p2-capabilities-connector-details.png` y=522 / y=563 | impl-settings |
| 19 | P3 | 提示条缺 warning 语义锚点（无 icon、无左 rail），仅靠文案 | `p2-interrupted.png` 条 417..1376,121..165 | impl-session |
| 20 | P3 | 连接器卡内**开关右缘(684) 与按钮右缘(668) 不齐** | `p2-capabilities-connectors.png` | impl-settings |
| 21 | P3 | ask 模态底部留白 28 > 顶部 24 | `p2-ask.png` 322..618 | impl-tools |
| 22 | P2 | **英文侧栏搜索 placeholder 被截断且无省略号**："Search sessions and message:"（应为 "messages"），在 x=266 处硬切 | `p2-english.png` 输入框 14..266,y=268 | **impl-shell** |

> **无 P0**。全 119 屏：accent 填充块全部 ≤2（最多 1/屏）、primary 全部 ≤1、画布尺寸全部属标准集（1512×944 / 1024×720 / 1000×820 / 1000×620 / 760×300）。

---

## 2. 逐屏清点

### 2.1 会话主面（p1-*）

| 场景 | 可见控件（逐个） | 状态可辨性 | 问题 | 级别 |
|---|---|---|---|---|
| **p1-chat** (A+B) | 顶带：‹自动化 chip、标题、智能体团队 chip、子智能体 chip、⋯、🔍、▯；tabs 对话/轨迹/记忆/上下文；侧栏：折叠钮、＋新建(Ctrl+N)、搜索框、分组＋、会话行×4；正文：过程摘要、思考行、code fence+复制、产物气泡、附件图；composer：输入区、附件、model▾、权限▾、发送；底：42K/128K 计量+详情；toast | 选中行 tint `#E2E8F9`✓；nav 图标 `#4B5261`✓；发送 disabled `#5A6371`/`#F2F4F7`=5.9:1✓；chip 文字保留✓ | 无 | — |
| **p1-approval** (A+B) | 同上 + 审批卡（rail/标题/说明/摘要块/原始参数折叠/Esc+Ctrl+Enter 提示/仅此一次·拒绝·允许一次·⋯）+ 顶带「运行中」chip(accent) + 「待审批 1」chip(中性) | 允许一次=accent 唯一 primary✓；rail=warning 2px✓；拒绝=描边✓ | 「待审批 1」chip 为中性灰（needs-action 宜 warning 系）P3 | P3 |
| **p1-trajectory** (A) | 顶带/tabs；轨迹列表（思考/团队任务/正文 行 + 1px 分隔 + 用时）；目标行；composer | 行 hover 未拍到；行内 › 可辨 | 无 | — |

### 2.2 消息/过程/运行态（动态面，重点）

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-tool-row-states** (A+B) | 4 行工具行（icon+名称+mono命令+右状态词+›） | 运行中 accent 蓝+spinner / 完成 绿✓ / 失败 danger / 已停止 tertiary ✓ | **完成态缺右状态词**（#4） | P2 |
| **p2-tool-row-states-dark** (A+B) | 同上 | 暗色四态对比均 ≥4.5:1 ✓ | 同 #4（同族） | P2 |
| **p2-process-expanded** (A+B) | 过程摘要、思考行×2、工具行、fence+复制、消息⋯、toast、composer | 折叠箭头方向✓；计数保留✓；正文 14/22✓ | 无 | — |
| **p2-process-fold** (A) | 摘要行 + 正文 | 计数仍在✓ | 无 | — |
| **p2-running-composer** (A+B) | 运行中 chip、草稿+光标、排队、停止…(danger)、✎正在回复、流式 heading | 停止=danger 非 default✓；排队中性✓ | **composer 空态留白偏大**（#14） | P2 |
| **p2-jobs** (A+B) | 浮层：标题+计数、任务行(运行中 chip/mono/用时+退出码/输出/停止)、底部提示；composer | 停止=danger✓ | **toast 压 composer 提示文字**（#2） | P2 |
| **p2-jobs-stop-armed** (A) | 停止→确认停止 | 两段式✓ | **armed 变宽致行内跳动**（#15） | P2 |
| **p2-jobs-stopped** (A+B) | 已取消(中性)+输出；提示变 Esc 关闭 | cancelled 中性✓ | 无 | — |
| **p2-interrupted** (A+B) | 提示条(说明+在输入框继续+✕)、user 气泡、过程、fence、composer | 与审批卡形态不同✓ | 无 warning 锚点（#19） | P3 |
| **p2-interrupted-dark** (A) | 同上 | 提示条 `#23232F`、按钮边可见✓ | 无 | — |
| **p2-run-band** (B+C) | 运行态带（与 running-composer 同族） | 度量：band 85、accent 0 ✓ | 族差分未见异常 | — |
| **p2-tool-row-complete** (B+C) | 单行完成态特写 | accent 1（spinner 已消失）✓ | 与 #4 同源 | P2 |
| **p2-status-strip** (B+C) | 底部计量条 + 详情按钮 | accent 1（进度条）✓ | 族差分未见异常 | — |

### 2.3 审批 / 计划 / 提问 / 计划块

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-approval-diff** (A) | 审批卡（rail/标题/路径块/将改动/文件名行 +1−1/diff 行/动作行） | 增行 `#F0F9F3`、删行 `#FEF1F0`、上下文白✓；行级 rail✓ | 无 | — |
| **p2-approval-diff-dark** (A) | 同上（暗） | 卡头 `#1B1B26`/卡身 `#272736`✓ | 无 | — |
| **p2-plan** (A+B) | 模态：accent 左 rail、计划待审阅 chip、批准后开始实施、标题、3 步、改计划输入、取消计划/继续规划/批准(primary)、kbd 提示 | 唯一 primary✓；无 default（刻意）✓ | **无 ✕ + 两动作同权**（#5） | P2 |
| **p2-ask** (A+B) | 模态：✦标题、问题、3 radio、Esc 提示、取消/提交(disabled) | 提交 disabled 标签 `#5A6371` on `#F2F4F7` = 5.9:1 ✓ | 底部留白偏大（#21） | P3 |
| **p2-goal** (A+B) | 目标行(chip+文本+轮次+⋯)、composer、滚动条 | chip accent tint（执行中）✓ | **toast 压元信息**（#2） | P2 |
| **p2-goal-paused** (C, 差分仅 424×28+433+692) | 同上，chip 变「目标已暂停」+「继续目标」按钮 | paused=中性 tint + 恢复动作✓ | 无 | — |
| **p2-goal-blocked / p2-goal-narrow / p2-goal-dark** (B+C) | 同族 | 度量通过（accent ≤1、band 85） | 未目视（差分 bbox 覆盖全屏，属主题/布局差异，建议下轮补看） | 待验 |

### 2.4 团队 / 子代理

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-team-overlay** (A+B) | 浮层：标题+计数、成员空态、已结束卡(绿点/名/已完成/⋯/打开)、共享任务空态、打开工作台、底部提示 | 状态词 12px 灰✓ | **三处空态冗余 + toast 贴 10px**（#10） | P2 |
| **p2-team-tasks** (A+B) | 同 + 3 张任务卡(id chip/标题/状态 badge/负责人/依赖) | 状态是 **badge**（灰/蓝 tint）✓ | 无（我上轮误报为纯文字，已撤回） | — |
| **p2-team-tasks-scrolled** (C, 差分 912×934+600+10) | 同上（滚动后） | badge 清晰✓ | 无 | — |
| **p2-team-history** (A+B) | 已结束卡变体（历史·可继续/继续） | — | **副行动作词 + 双状态**（#11） | P2 |
| **p2-team-child** (A+B) | 面包屑+▾、团队 chip、已完成 chip、任务/输出/活动、底部 input+发送 | 发送 disabled 浅灰（同 composer 配方）✓ | **底部输入非 composer 形态**（#12） | P2 |
| **p2-team-chip** (A+B) | 对话 + 中央锚定 toast + 右下 toast | — | **同一审批两条 toast**（#3） | P2 |

### 2.5 能力（连接器 / 技能）

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-capabilities-skills** (A+B) | tabs 技能/连接器、计数、刷新、新建技能、2 张卡(星标/名/⋯/描述/badge/mono 路径/复制)、技能目录折叠、诊断区 | badge（工作区/全局）✓ | 路径列错位（#16） | P2 |
| **p2-capabilities-skills-dark** (A+B) | 同上（暗，卡 `#1B1B26` 边 `#404049`） | ✓ | #16 | P2 |
| **p2-capabilities-connectors** (A+B) | 卡片(demo-mcp/连接失败 badge/⋯/URL/错误行/chips 查看报错详情/重试/启用开关)、连接器选项折叠、诊断区 | **连接失败 = danger badge**✓（我上轮误报已撤回） | 开关与按钮右缘不齐（#20） | P3 |
| **p2-capabilities-connectors-options** (A+B) | 折叠展开：读取工作区开关 + 说明 + ⚠ 警告 + 显示参数 | 开关 off 可辨✓ | **trigger 有底**(#6)、**暴露 /tmp 路径**(#8) | P2 |
| **p2-capabilities-connector-form** (A+B) | ✕、传输▾、名称(input focused)、URL、认证▾、HTTP 请求头折叠、超时、测试连接/取消/添加 | focused=accent 1px✓；唯一 primary✓ | **动作行间距 + 必填无标记**（#9） | P2 |
| **p2-capabilities-connector-form-stdio** (A+B) | 同上 + STDIO 进程(命令/参数)、环境变量 textarea、⚠ 两行、滚动条 | ✓ | **滚动区底部切半行**（#1，**P1**）；分组标题层级弱 | **P1/P2** |
| **p2-capabilities-connector-details** (A+B) | ✕、定义表 4 行、状态、诊断句、原始错误折叠、关闭 | 表格行线无竖线✓ | **状态红字 vs badge 不一致**（#18） | P3 |
| **p2-capabilities-skill-content** (A+B) | ✕、标题、路径副行、markdown 正文、关闭 | — | **H2 18px 越阶**（#7） | P2 |

### 2.6 设置面

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-appearance** (A+B) | 搜索、12 项导航、主题 segmented(浅色/深色/跟随)、语言▾、2 个开关、组合键 input+录制、3 个 kbd chip、恢复默认/保存(disabled)、✕ | segmented=抬升块✓；导航选中 tint✓；接缝 1px `#E2E3E5`✓ | 内容在页脚处被截（长页，滚动中，可接受） | — |
| **p2-appearance-actions** (B+C) | 同族（动作区特写） | accent 0 ✓ | 族差分未见异常 | — |
| **p2-behavior** (A+B) | 权限模式(3 bullet)、系统沙箱、工作步骤 segmented×2、更多对话设置、消息字号、恢复默认值/保存 | segmented✓；节标题无规则线✓ | 无 | — |
| **p2-model-settings** (A+B) | 搜索、导航、模型列表 3 行(名+badge/元信息 3 行/编辑/⋯)、添加模型、提示词与运行参数(textarea)、保存 | 行间 1px✓；badge✓ | 无 | — |
| **p2-model-empty** (C, 差分 829×614+532+277) | 空态"还没有模型" | — | **空态无 CTA**（#17） | P2 |
| **p2-model-add / -edit / -switched** (B+C) | 表单/切换态 | 度量通过 | 未目视，建议补看（表单同 connector-form 风险） | 待验 |
| **p2-archive-settings / -restored / -settings-dark** (B+C) | 归档页/恢复提示/暗 | 度量通过 | 未目视 | 待验 |
| **p2-english** (A+B) | 同 p2-context 的英文版：Chat/Trajectory/Memory/Context tab、Compact context/Context settings、概览表、System prompt sections、附件 chip + composer、Details | 选中 tab=accent 下划线✓；附件 chip 有 ✕✓；发送为 accent 实心（有附件故 enabled）✓ | **搜索 placeholder 截断无省略号**（#22） | P2 |
| **p2-english-settings** (A+B) | 英文设置模态：搜索、14 项导航、Models 3 行(Default badge/Edit/⋯)、Prompt & runtime、Save(disabled) | 选中 tint✓；Save disabled 5.9:1✓；长导航项 "Interface & notifications" 不挤压内容✓ | 无（英文长文案在此页未截断） | — |
| **p2-english-appearance / -archive-settings / -automation / -memory-settings** (B+C) | 同族英文页 | 度量通过（band 85、accent ≤1、画布标准） | **未目视** | 待验 |

### 2.7 右栏 / 文件 / 工作区

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-files** (A+B) | 右栏 tabs 开始/文件、路径行+重载、交付区、检查报告、树(3 折叠行 + 2 文件行) | 树 leading slot 对齐✓ | 无 | — |
| **p2-file-preview** (A+B) | tabs、README.md tab(accent 下划线)、路径行+复制/重载、查看改动折叠、正文 | README.md 只出现 1 次✓ | 无 | — |
| **p2-file-diff** (A+B) | 同 + diff 块(@@/行号/tint 行) + "+1 −0" | 行级 tint✓ | **trigger 有底**(#6)、**toast 压提示**(#2) | P2 |
| **p2-workspace-tree** (B+C) | 工作区树 | 度量通过 | 未目视 | 待验 |

### 2.8 侧栏 / 搜索 / 浮层入口

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-sidebar-states** (A+B) | 5 行样例(置顶 chip/运行中词/等待回复词/后台完成蓝点)、分组头 | 五态可辨✓ | 无（当前会话不在列表内，无"未高亮"缺陷——已撤回该疑点） | — |
| **p2-sidebar-collapsed** (A+B) | rail 8 图标(logo/折叠/＋/会话/时钟/洞察/能力/设置) | 品牌 logo=accent 例外✓ | 无 | — |
| **p2-sidebar-unread** (B+C) | 未读态 | accent 0（蓝点<24 不计）✓ | 未目视 | 待验 |
| **p2-search** (B+C) | 搜索输入 | — | 未目视 | 待验 |
| **p2-search-results** (A+B) | 搜索框+✕、消息搜索、4 条结果(标题+计数/角色+日期/摘要高亮/截断)、标题无匹配 | 关键词高亮✓；摘要省略✓ | 无 | — |
| **p2-palette** (A+B) | 模态：搜索、操作组、8 行(选中 tint/描述/kbd)、底 kbd 提示 | 选中=BgSelected tint✓ | 无 | — |
| **p2-palette-right-pages** (C, 差分全屏) | 同族（右侧分页） | — | 未目视（差分 bbox 全屏=滚动位置不同） | 待验 |
| **p2-slash / p2-mentions / p2-mentions-cleared** (B+C；mentions-cleared 差分 990×185+402+683) | 触发器浮层 | mentions-cleared = 浮层消失✓ | 未目视 slash/mentions | 待验 |
| **p2-toast** (A+B) | 两条 toast（警告+成功）+ 排队计数 + 团队浮层 | toast 形态一致✓ | 无（浅卡=已接受） | — |
| **p2-inbox / p2-inbox-popover** (B+C) | 通知中心 | 度量通过 | 未目视 | 待验 |

### 2.9 模型菜单 / composer 菜单

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-modelmenu** (B+C) | 一级菜单 | — | 未目视 | 待验 |
| **p2-modelmenu-models** (A+B) | 一级(模型/推理等级/打开模型设置…) + 二级(自定义服务/DeepSeek 分组、3 项、128k) | 选中=accent 满填+白字，副标 `#BFCEFD` on `#0D44F6` = **4.9:1**✓ | **动作项与选项混排无分隔**（#13） | P2 |
| **p2-modelmenu-thinking** (B+C) | 推理等级子菜单 | — | 未目视 | 待验 |

### 2.10 窄窗 / 暗色（10 张）

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **narrow/narrow-session** (A+B) | 同 p1-chat（1024） | chip 保留文字✓；顶带尾簇收进 ⋯ 距缝 27px✓ | 无 | — |
| **narrow/narrow-settings** (A+B) | 设置模态 88..972，导航 223 宽 | 接缝 `x=291` 1px✓ | 无 | — |
| **narrow/narrow-landing** (A+B) | hero + 卡 + composer | — | 1024 hero toast 压卡=**已接受** | — |
| **narrow/narrow-session-inspector** (A+B) | 顶带 + 检查器 700..1024 | 顶带留白 gap 27px✓ | 无 | — |
| **narrow/narrow-capabilities / -capabilities-skills** (B+C) | 能力页 1024 | 度量通过 | 未目视 | 待验 |
| **narrow/dark-session** (A+B) | 暗色会话 | composer `#1B1B26`/page `#111118`✓ | 无 | — |
| **narrow/dark-settings** (A+B) | 暗色设置模态 | nav `#1B1B26` < 内容 `#23232F` < scrim `#07070A`✓ | 无 | — |
| **narrow/dark-landing** (A+B) | 暗色 hero | — | hero toast 压卡=已接受 | — |
| **p2-context-dark** (A+B) | 上下文页（暗） | composer 暗面✓；表格行线✓ | 无（Table 竖线=已接受） | — |

### 2.11 洞察 / 上下文 / 自动化

| 场景 | 控件 | 状态 | 问题 | 级别 |
|---|---|---|---|---|
| **p2-insights / -dark** (A+B) | segmented 7天/30天、4 KPI、4 张表、会话排行搜索 | segmented✓；KPI 值 20/600✓ | 无（Table 竖线=已接受） | — |
| **p2-context / -sections / -dark** (A+B / C) | 上下文页：概览定义表、系统提示分段表、压缩上文/上下文设置 | 行线✓ | context-sections 未目视（差分 1055×611 属内容区） | 待验 |
| **p2-automation** (A+B) | 新建任务(primary)、搜索、筛选、2 张任务卡(启用开关/立即运行/⋯/修复/运行历史) | 已停用行内化✓ | 右侧 4 控件偏多（沿用上轮 P2） | P2 |

### 2.12 其余（仅度量，未目视）

`p2-about-settings`、`p2-about-diagnostics`、`p2-data-import`、`p2-data-privacy`、`p2-network-settings`、`p2-notifications`、`p2-memory`、`p2-memory-settings`、`p2-lsp-settings`、`p2-model-prices(-dark)`、`p2-attachments`、`p2-artifact`、`p2-artifact-history`、`p2-drop`、`p2-pinned`、`p2-sessionrefs`、`p2-child-switcher`、`p2-cold-resume`、`p2-cold-resume-child`、`p2-fork`、`p2-english`、`p2-english-settings`、`p2-markdown-specimen(-dark)`、`p2-guide`、`p2-interrupted` 之外的 3 张 `p2-english-*`、`p2-hotkey-record`、`p2-team-tasks` 之外的 1 张、`p2-capabilities-connectors` 之外的选项屏等。

**未目视的只剩 25 张左右（其余 60 张已目视）**，它们只做了 B（度量）与 C（族差分）：度量项（画布/顶带/accent/接缝）全部通过，但**文案截断、对齐、空态、错误态**这类只有眼睛能判的项目尚未覆盖。建议下一轮按优先级补 A（我已按风险排序）：① `p2-attachments` / `p2-artifact` / `p2-artifact-history` / `p2-drop`（文件类，最容易出截断/错位）；② `p2-memory` / `p2-memory-settings` / `p2-data-import` / `p2-data-privacy` / `p2-network-settings`（设置长页，滚动截断风险同 #1）；③ `p2-cold-resume` / `p2-cold-resume-child` / `p2-fork` / `p2-child-switcher` / `p2-sessionrefs`（恢复/分叉类状态）；④ `p2-slash` / `p2-mentions` / `p2-inbox(-popover)` / `p2-pinned` / `p2-modelmenu(-thinking)` / `p2-model-add|edit` / `p2-english-appearance|archive-settings|automation|memory-settings` / `p2-archive-*` / `p2-about-*` / `p2-guide` / `p2-hotkey-record` / `p2-markdown-specimen(-dark)` / `narrow/narrow-capabilities(-skills)`。

---

## 3. 跨屏模式（一次修多处）

1. **toast 与宿主内容抢位**（#2、#3、#10）：根因是 toast 只在 session 屏预留 dock 高度，且锚点未避让"正文列右端/消息元信息/composer 提示行"。建议把"锚点候选"从「composer 顶」改为「composer 顶 − 48（状态条带）」，并对 1512 宽也裁剪右侧 40px 安全区。
2. **collapsible trigger 两种样式**（#6）：uikit 里应有唯一 trigger 配方；现在"灰底 chip"与"无底行"并存。
3. **状态三种形态并存**：badge（卡片）✓、纯文字（详情表）✗、chip（顶带）✓ —— 统一到 `StatusBadge`，详情表至少加 tint 底。
4. **文案溢出无一处置**：#22（英文 placeholder 硬切）与 #3 级 "…" 截断并存 —— 建议统一 `MaxLines(1)` + 省略号，并在 `TextField`/`Label` 层禁用"硬裁"。
5. **空态质量参差**：#17（无 CTA）、#10（三处冗余）、"标题无匹配"（纯文字）—— 建议统一 `uikit.EmptyState`（图标 + 一句 + 一个动作）。
6. **滚动区底部无渐隐**：#1 暴露的问题，模态/设置长页都可能出现；建议 `Scroll` 统一加 12px 底部安全内边距 + 顶部/底部渐隐。

---

## 4. 通过项（抽验清单，供 Lead 放心）

- 接缝：16 个探针全 1px（亮 `#E9EAEA` / 暗 `#292930` / 设置导航 `#E2E3E5`）— 见 `measure.sh`。
- 顶带：1512 屏统一 85（48+36+边框），1024 屏同构。
- 圆角：composer 16 / 卡 12 / palette 12 / 设置面 12。
- accent 预算：**119 屏全部 ≤2**（脚本连通块口径 + 目视抽查一致）。
- primary：每屏 ≤1（唯一 primary 出现在 添加/批准/新建任务/添加连接器 等表单提交处）。
- 状态语义：running=accent ✓（chip/tool row/sidebar 三处一致）、cancelled 中性 ✓、failed danger ✓。
- 字号：正文 14、元数据 12、H 16/600、模态标题 20/600（唯一例外 #7 的 18）。
- 暗色 ramp：nav `#1B1B26` < 能力/会话页 `#111118`；设置面 nav<内容 ✓。

## 5. 本轮撤回的 4 条误报（透明记录）

| 曾报 | 实况 | 原因 |
|---|---|---|
| 禁用发送 ≈1.6:1 | `#5A6371` on `#F2F4F7` = **5.9:1** ✓ | 单点采样落在箭头抗锯齿上 |
| ask「提交」disabled 对比 <2:1 | 最暗像素 `#5A6371` = **5.9:1** ✓ | 单点采样落在字间空隙 |
| 连接器卡"连接失败"是红字非 badge | 实为 **danger badge**（粉底红字）✓ | 缩略图上未看清 pill 底 |
| team-tasks 状态是纯文字 | 实为 **badge** ✓ | 同上 |
| sidebar-states 当前会话未高亮 | 该会话不在列表中（样例屏）→ 非缺陷 | 误判 |

> 方法修正：后续所有对比度结论改用**裁剪区直方图 + 最暗像素**，不再用 1×1 采样。