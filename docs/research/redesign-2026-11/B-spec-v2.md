# ZeoKeel 视觉语言 v2「精确中性 · Precision Neutral」（Lead 定稿，2026-11-18）

> 输入：`A2-industry-design-languages.md`（业界研究，C1 Linear 4.60 / C6 Primer 4.50）、
> `A3/A4/A5`（审计）、`A1-matrix.tsv`（官方化矩阵）、现有 `docs/DESIGN.md` 契约。
> 裁决人：Lead。**本文件是 C 阶段实现者的唯一视觉契约**；与 `DESIGN.md` 冲突处以本文件为准，
> 完成后由 Lead 回写 `DESIGN.md` / `registry.json` / `AGENTS.md`。

---

## 0. 一句话

**结构、密度、对齐、分隔用中性灰阶与 hairline 说话；层级只靠字号/字重/间距；品牌蓝只给「主操作 + 当前选择 + 焦点 + 链接」。**

主语言 **C1 Linear 精确中性**（视觉/结构）＋ 辅助语言 **C6 GitHub Primer 企业清晰**（状态/流程语义）。
不用 Geist 极限黑白（会吃掉品牌蓝角色）、不用 Claude 暖纸（与"企业级工具"相反）、不引第三方字体/图标库。

---

## 1. Token 变更（brand + design；全部 4 的倍数；颜色仍只在 `internal/brand`）

### 1.1 颜色（`internal/brand/palette.go`）

| 语义 | 亮色 旧 → 新 | 暗色 旧 → 新 | 说明 |
|---|---|---|---|
| `BorderSubtle` **新增** | — → `ink 6%` | — → `白 7%` | L1：卡内头/体分界、表头下边、弱分隔 |
| `Border` (L2) | `ink 10%` → **`ink 9%`** | `白 12%` → **`白 10%`** | 结构线：侧栏右缘、顶带下缘、tab 下缘 |
| `BorderStrong` (L3) | `ink 16%` → **`ink 15%`** | `白 18%` → **`白 16%`** | 可交互边界：输入框、卡片、composer、菜单静息边 |
| `Hover` | `royal 6%` → **`ink 4.5%`** | `cyan 10%` → **`白 6%`** | 悬浮是"瞬态"，用中性；不再整屏泛蓝 |
| `RowSelected` | `royal 12%` → **`royal 9%`** | `cyan 18%` → **`cyan 13%`** | 持久选中保持品牌色相，但更收敛 |
| `TextTertiary` | `#58606E` → **`#67707E`** | `#B0B5C7` → **`#9DA5B6`** | 元数据更"退后"；仍满足 AA（含选中 tint） |
| `SurfaceOverlay` **新增** | `#FFFFFF` | `#23232F` | S4 浮层底；暗色比 S2 `#1B1B26` 高一阶，否则浮层"贴"在卡上 |
| `Scrollbar` | `ink 30%` → **`ink 22%`** | `白 35%` → **`白 24%`** | 滚动条更轻；hover 由 ScrollbarStyle 提亮 |
| 其余（Background/Sidebar/Surface/Strong/Text/Secondary/Accent/状态色/Focus/Selection/Scrim） | 保留 | 保留 | 不动品牌身份色 |

**硬度规则**：边框只画一次（Geist "draw each border once"）；相邻两个容器只有外层画边。

### 1.2 圆角（`internal/brand/brand.go` + `internal/design/components.go`）

| token | 旧 | 新 | 语义 |
|---|---|---|---|
| `brand.Radius`（主题控件圆角） | 10 | **8** | 按钮/输入/select/菜单项 |
| `RadiusXS` | 4 | 4 | kbd、行内 code、状态小方块 |
| `RadiusSM` | 8 | **6** | chip、代码块、行 hover、tab |
| `RadiusMD` | 10 | **8** | 与主题一致 |
| `RadiusLG` | 16 | **12** | 卡片/工具卡/审批卡/对话框/toast/浮层 |
| `RadiusXL` | 20 | **16** | **仅** composer |
| `RadiusMax` | 999 | **999** | 只给头像；**状态徽章改 4**（见 §1.4） |

### 1.3 字号阶梯（`design.TypeMetrics`）——字重烘焙进 role

| Role | 旧 | 新 | 用途 |
|---|---|---|---|
| `TypeXL` | 24/32/400 | **24/32/600** | 仅落地页 hero（一屏 1 处） |
| `TypeL` | 20/28/500 | **20/28/600** | 设置页/对话框 H1（一屏 1 处） |
| `TypeM` | 16/24/400 | **16/24/600** | 分节标题、窗口标题、markdown H2 |
| `TypeBody` | 14/22/400 | 14/22/400 | 正文、markdown 段落 |
| `TypeS` | 14/24/400 | **14/22/500** | 列表行标题、菜单项、按钮标签、工具名 |
| `TypeXS` | 13/20/400 | 13/20/400 | 次要正文、表单标签、检查器正文 |
| `TypeXXS` | 12/18/400 | 12/18/400 | 元数据、状态词、hint |
| `TypeXXXS` | 11/14/400 | **11/14/500** | 仅 kbd / gutter / 分组微标签 |

- call site **禁止再传 `FontWeight`**（role 已带权重）；只有 `TypeBody` 例外可加 `WeightSemibold` 做强调。
- 一屏 ≤4 种字号（既有规则不变）。

### 1.4 新增 token / 预设（`design/components.go`）

| 新增 | 值 | 用途 |
|---|---|---|
| `ProseMeasure` | **640** | markdown 段落/列表/引用最大宽；代码块/表格/卡片仍用 `TranscriptWidth 960` |
| `SpaceInline/Row/Group/Section/Screen/Page` | 4/8/12/16/24/32 | 语义别名（不新增数值），评审可检查 |
| `FocusRingOffset` | **2**（主题） | 焦点环外扩，不压内容 |
| `ScrollbarStyle` | 宽 10、thumb 10、圆角、`Track` 透明、hover 提亮 | 全局滚动条 |
| `Tokens.BgOverlay` | 见 §1.1 | S4 |
| `Tokens.BorderL1` | 见 §1.1 | L1 |
| `Badge()` | `RadiusXS(4)`、`PaddingX(Space1+2?)`、`TypeXXXS/500` | 状态标签改成"小方块"，不再是药丸 |
| `Kbd()` | 11/14/500、`BgStrong`、`BorderL2`、`RadiusXS`、内距 4×2 | 键位提示 |
| `MotionFast/MotionBase/MotionSlow` | 80/120/160ms | 见 §1.5 |

### 1.5 动效（新增 `internal/design/motion.go`）

| 场景 | 时长 | 说明 |
|---|---|---|
| hover/focus 颜色过渡 | **80ms** | `ElementTransition{Colors:true}` |
| 展开/折叠（工具行、推理、参数） | **120ms** | `Height:true`；配 `HoldPlace` |
| 浮层/菜单/对话框 | **160ms** 进 / **120ms** 出 | `Enter: Opacity 0→1, Y 8→0`；不做缩放 |
| 列表跟随/重排 | **200ms** | `ListState.FollowAnimation` / `MoveAnimation`（v0.9.1 新增） |
| 主题切换 | **0ms** | 直接跳变 |
| ReducedMotion | 全部 **0ms** | 读 `c.Preferences().ReduceMotion` |

**禁止**：>240ms 的 chrome 动画、流式文本逐字淡入/位移、连续宽高布局动画。

---

## 2. 表面与边框规则（S0–S4）

| 层 | token | 亮 | 暗 | 边框 | 阴影 |
|---|---|---|---|---|---|
| S0 页面 | `BgBase` | #FFFFFF | #111118 | 无 | 无 |
| S1 下沉 | `BgSidebar` | #F7F8FA | #0B0B10 | 右缘 1px L2 | 无 |
| S2 抬升 | `BgRaised` | #FFFFFF | #1B1B26 | 1px L3 | **无**（composer 也改无阴影） |
| S2.5 内嵌 | `BgStrong` | #F2F4F7 | #272736 | 无 | 无 |
| S3 只描边 | `BgBase` + L3 | 白 | #111118 | 1px L3 | 无 |
| S4 浮层 | `BgOverlay` | #FFFFFF | #23232F | 1px L3 | `ShadowOverlay`（0 10 30 α.10 / 暗 .34） |

**2px 只允许三处**：① 选中左条（Accent）；② 焦点环；③ 审批/计划卡语义左条（Warning/Danger）。
**不许画边框**：transcript 消息（无气泡边框）、侧栏行（hover 才出底）、工具行（展开后内部才有 hairline）、设置分节（间距 + SectionLabel，不用卡片）。

---

## 3. 逐表面规格

### 3.1 壳层（`internal/uiroot`）

1. **侧栏**：宽 280 保留；背景 S1；右缘 1px L2。
   - 品牌行 64 → **56**：logo 24、wordmark 15/600、右侧折叠按钮 28。
   - 主操作「新对话」：**32 高、Ghost 行（无描边，hover 出 `BgHover`，圆角 6）**，左 `Plus` 16 + 文本 14/500，右侧 `⌘N` kbd。
   - 导航行（自动化/洞察/能力）：**32 高**、图标 16 单列、文本 14/500、gap 8、选中 = `BgSelected` + 2px Accent 左条 + 14/600。
   - 小节标题 `SectionLabel`：**11/500、`LabelTertiary`**，上间距 `Space4`、下 `Space1`。
   - 会话行 32 高、标题 13、元数据 11；hover 出中性底；选中 9% 蓝 + 左条。
2. **顶带**：高 40 → **48**；底 S1 + 下缘 1px L2；标题 **14/600**（不是 16），副标题/面包屑 12 tertiary；右侧图标按钮 28、间距 `Space1`。
   - 状态/团队/子智能体 chip：**去掉描边**，改 `BgHover` 底 + 12/500 文本 + 图标 16；hover 加深；这是"标签型按钮"。
   - Tabs 行：32 → **36**；tab 文本 13/500，未选 `LabelTertiary`，选中 `LabelPrimary` + **2px Accent 下划线**（宽 = 文本宽，左右各 8 内距）；tab 行下缘 1px L2（已有则只画一次）。
3. **检查器**：背景改 **S1**（与内容分离）；左缘 1px L2；头部 40、tab 36；内容内距 `Space4`。
4. **命令面板**：宽 640；行高 36；图标 16；行尾 kbd；分区标题 11/500；选中 = 9% 蓝 + 左 2px Accent；底部 hint 行 32。
5. **Toast**：宽 ≤320 / ≥240；**底右锚**（`Right` 16；`Bottom` = 当前底部占位 + 16，有 composer dock 时避开发送按钮与状态条；浮层打开时同一底右锚）；标题 13/600、正文 12/18 最多 2 行；同类合并 `×N`；错误不自动消失；进入 120ms / 退出 80ms。**判据：toast 只能出现在当前任务不会点到的角落。**

### 3.2 会话（`internal/ui/session`）

1. **阅读列**：transcript 容器 960 保留；**正文段落 max 640**（`ProseMeasure`），左对齐于列内（不居中，避免和代码块错位）；composer 与列同宽 960。
2. **消息节奏**：用户消息 → 助手消息间距 **24**；同一回复内块间距 **12**；消息与状态条间距 16。
3. **用户消息**：右对齐气泡，底 `BgStrong`、圆角 `RadiusMD(8)`、**无描边**、内距 12×8、最大宽 80%；文本 14/22。行内产物 chip 用 `BgRaised`+1px `BorderL3`+`RadiusSM`，与气泡不同构。
4. **助手消息**：无卡片无边框；正文 14/22；**元信息行统一在消息底部一行**（时间 11 tertiary · 用时/tokens 11 tertiary · 动作 `⋯`），**hover/focus 才显形**，行高固定 24 不跳动；流式结束后 2s 内自动显示一次。
5. **思考/推理**：单行 28 高、图标 16 +「思考」13/500 + 用时 11 tertiary + chevron；展开体 13/20 `LabelSecondary`，左侧 2px L2 竖线，内距 12。
6. **状态条**：高 **32**，分三段：左=运行态（Spinner/状态词/队列 ×N）｜中=空｜右=用量组（`42K/128K` + 3px 进度 + `详情` popover，popover 内放轮次/步数/缓存/剩余）。数字用 mono 12。
7. **Composer**：`RadiusXL(16)`、1px L3、**无阴影**、内距 12；输入区 14/22、placeholder `LabelTertiary`；工具条 32 高、按钮 28；发送按钮 **32 圆角 8**（不是药丸），禁用 = `BgStrong` + tertiary 图标；运行态为「停止」danger 描边按钮；排队为「排队」次按钮；审批 pending 时发送降级为禁用、焦点环不画。

### 3.3 工具 / 审批 / 提问 / 计划（`internal/ui/toolcards`、`internal/uikit`）

1. **工具行**：32 高；`[状态图标 16][工具名 13/500][摘要 12 tertiary 截断][用时 11][chevron]`；hover 中性底；展开体顶部 1px L1，内容 13/20；失败行默认展开、danger 图标；运行中 spinner + 「运行中」；完成 `Success` 勾；取消中性。
2. **审批卡**（重做，`uikit/approval.go`）：
   - 卡体 S2 + **1px L3** + `RadiusLG(12)`；**去掉满宽 Warning 色带与 0.4 描边**；
   - **左缘 2px Warning 竖条**（`x=0`，不圆角）；头部：`StatusBadge("需要审批")` + 工具友好名 14/500 + 右侧 12 tertiary「来源：会话 …」；
   - 影响声明：12/18 `WarningText` 一行；
   - 目标块：mono 13/20、`BgStrong`、`RadiusSM`、1px L1，带语义标签（命令/路径/域名/SQL…）；
   - 参数：`Collapsible`「原始参数」13/500；
   - footer：`拒绝`（次）· `允许一次`（主，唯一的 accent 填充）· `⋯`（菜单：本会话内始终允许该工具 / 总是允许（写入设置）/查看规则）；**范围以 12 tertiary 显示在主按钮左侧**；键位提示：`Esc` 拒绝、`⌘↵` 允许。
3. **提问卡**：S2 + L3 + LG12；标题 14/600；选项行 36 高、radio、hover 中性；提交 primary 一个。
4. **计划审阅**：同审批卡结构，语义色换 `Accent`；**有未提交反馈时禁用「批准」并说明原因**（业界细节）。
5. **Diff**：用 `editor.DiffView`；行内增删色保持语义色 tint；文件头 32 高 mono 12。

### 3.4 设置与次级页（`internal/ui/settings`、`welcome`、`home`、`insights`、`automation`、`plugins`）

1. **设置面**：宽 820 → **880**、高 760；两栏：导航 **220**（S1、右缘 L2、搜索 32、行 32、选中 9% 蓝 + 左条），内容列内距 **24**、正文最大宽 **640**。
2. **标题**：H1 20/600；分组标题 16/600（上间距 24、下 8）；表单行 label 13/500 + 描述 12/18 tertiary；行高 ≥44。
3. **段落说明**：连续说明 >2 行的一律改成分点 + 首行摘要；放在控件**下方**（12/18 tertiary），不再堆在页首。
4. **控件列宽**：输入/选择 320，数字 160，开关右对齐；按钮 32。
5. **页脚**：高 56、上缘 1px L2、右侧唯一 primary（`保存`），左侧 `恢复默认`（次）。
6. **能力页卡片**：`GridView` 保留；卡片 S2+L3+LG12、内距 16；标题 14/600、描述 12/18 两行截断；hover 只提亮边框（L3→L3+`BgHover`），不加阴影。

### 3.5 右栏（`internal/ui/rightpanel`）

- 宽 320（min 260）保留；底 S1；左缘 1px L2；tab 行 36；
- 文件树行 28/32、图标 16、缩进 12/级；选中 9% 蓝 + 左条；面包屑 12；
- 属性表：label 列 96、值 13、行分隔 L1；
- 产物行：32 高、hover 中性底、右侧动作 hover 才显形（focus 也显形）。

### 3.6 Markdown / 代码块（`internal/uikit/markdown*.go`）

- 正文 14/22、段落间距 12；`ProseMeasure 640`；
- 标题：H1 18/600、H2 16/600、H3 15/600（低于屏标题）；
- 行内 code：mono 12/18、`BgStrong`、`RadiusXS`；
- 代码围栏：**头部 28 高**（语言 mono 11/500 tertiary 左 + 复制 IconButton 16 右）+ **1px L1 分隔**；块体 mono **13/20**、内距 12、`BgStrong`、**1px L2 外框**、`RadiusSM(6)`；短围栏收缩到内容宽；
- 表格：表头 `BgStrong`、1px L2、单元格 12 内距；引用：2px L2 左条 + 12 tertiary。

---

## 4. 交互细节清单（每个实现者逐条自查）

1. **hover 必须有过渡**（80ms），且 hover 与 selected 可区分（中性 vs 蓝）。
2. **焦点**：键盘聚焦画 2px Accent 环（offset 2）；列表行/树行用内嵌环避免被裁；鼠标点击不画环；关闭浮层后焦点归还触发器。
3. **行内动作**：hover 或 focus-within 时显形（**始终构建**、只过渡透明度，保证键盘可达与行高不跳）；`Label` 完整；快捷键用 `uikit.Kbd` 显示在行尾或提示行。
4. **禁用**：解释原因**只能用旁注/旁置说明**（禁用控件不接收指针事件，Tooltip 永远不会显示），不能只是灰掉；禁用态的填充/文字对比度 ≥3:1。
5. **复制**：点击后图标 1.2s 变 Check + toast「已复制」；代码块/路径/ID 均可复制。
6. **Esc 层级**：浮层 → 菜单 → 清空搜索 → 取消流式（直接停）；窗口级生效。**Esc 拥有者表面内的控件不得挂 Tooltip**（mygo tooltip 是 overlay，会吞第一次 Esc）。
7. **Enter/⌘↵**：输入框 Enter 发送、Shift+Enter 换行；对话框 Enter 主操作；审批无默认动作，Esc=拒绝。快捷键提示只画在**可见提示行/`uikit.Kbd`**，不放进 Tooltip。
8. **滚动**：滚动条 10/10 圆角、track 透明；悬停提亮；流式跟随在用户上滚时暂停并显示「回到最新」。
9. **截断**：长标题用省略号 + tooltip 全文；路径/模型 id 用 `TruncateStart`；数字用 mono 对齐。
10. **空态**：图标 24 + 一句标题 14/600 + 一句说明 12/18 + 至多一个动作；不留大片空白。
11. **加载**：≤1s 用 spinner，长任务用文字状态 + 停止；不伪造进度。
12. **动效**：只用 §1.5 时长；ReduceMotion 全关；流式文本不位移。
13. **窄窗（1024）**：侧栏可折叠、右栏改抽屉、composer 不溢出、顶部 chip 允许折叠进 `⋯`。
14. **暗色**：导航比页面深；边框可见；浮层比卡片亮一阶；状态色不刺眼。
15. **a11y**：所有图标按钮有 `Label`；列表行 `Label` 不含 markdown 源；状态两通道（色+词）。

---

## 5. 官方化（A1 矩阵）执行要求

- C 阶段每个实现者先跑 `A1-matrix.tsv` 里与自己目录相关的行，**"迁移"项必须做**，"可迁移"顺手做；
- **P0 全仓项**：`ElementTransition`（hover 80ms）、`ListState.FollowAnimation`/`MoveAnimation`、`TruncateStart`、`Dividers`、`ui.Tabs`（替换 session 手绘 tab）、`IconButton` 收敛（sidebar 3 处）、`PropertySheet` 替换弃用的 `KeyValueRows`；
- 任何新增自绘必须登记（DESIGN §7 + registry + lint 白名单），否则检查失败。

---

## 6. 验收

每条改动必须有：`go build ./...`、相关包 `go test -count=1`、`designlint --strict --baseline`、`scripts/design-check.sh`、`go tool mygo vet .` 全绿 + 对应 PNG 人工评审（亮/暗/窄）。
新增交互必须补帧测试；新增视觉状态必须进快照。