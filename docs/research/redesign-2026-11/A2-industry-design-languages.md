# A2 · 业界 AI 客户端 / 企业工具设计语言研究（给 ZeoKeel 重设计 2026-11）

> 作者：业界设计研究员（子代理 `industry-research`）
> 格式说明：正文骨架按 [BRIEF.md](BRIEF.md) §5（§0 摘要 / §1 现状 / §2 发现 / §3 建议 / §4 风险 / §5 证据），
> 并按本任务要求追加了 §3（逐产品调研）、§4（候选语言评审矩阵）、§5（token 级落地）、§6（12 个关键表面的最佳实践）。
> **证据等级**：`[A]` 官方设计系统文档（token/组件规范）｜`[B]` 官方产品文档（交互行为）｜`[C]` 官方工程/设计博客｜`[D]` 本环境未取得公开证据（只写观察，不作决策依据）。
> 所有事实性断言都带 URL；找不到的写「未找到公开证据」，没有编造的设计规范。

---

## 0. 结论摘要（按影响排序）

1. **主编织语言定为「Linear 式精确中性」，辅助语言定为「GitHub Primer 式企业清晰」**（评分矩阵 §4.3：Linear 4.60 / Primer 4.50 / Zed 3.60 / Raycast 3.40 / Carbon 3.40 / Geist 3.30 / Claude 暖纸 3.00）。含义：**结构、密度、对齐、分隔用中性灰阶与 hairline 说话**（Linear），**状态、审批、空态、表单、键位提示的"语义词汇"照 Primer 的企业范式**（Primer 的 `StateLabel / InlineMessage / Blankslate / KeybindingHint / ConfirmationDialog` 这一层）。不用 Geist 的高对比黑白（会吃掉品牌蓝的角色）、不用 Carbon 的 IBM 密度（组件体量与字体栈冲突）、不用 Claude 式暖纸（与"企业级工具"目标相反，且无公开 token 证据）。
2. **一屏的"响度"必须重新分配**：当前审批卡用 22% Warning 满宽警示带（`internal/uikit/approval.go:219`）+ 0.4 Warning 描边（`:198`）+ `RadiusLG`，是全屏最响的元素，同时"允许"主按钮与 composer 焦点环、发送按钮都在用品牌蓝（`p1-approval.png`）。业界（VS Code 审批、Claude 权限提示）都用**中性卡体 + 一条语义边 + 范围选择器**：告警色只出现在 2px 边、12px 胶囊标签和一行影响声明里。见 §2 F-01/F-02。
3. **圆角档位偏软**：现为 4/8/10/16/20（`internal/design/components.go:108-113`）。业界容器圆角收敛在 6–12，大浮层 16（Vercel Geist Materials：base/small 6、medium/large 12、modal 12、fullscreen 16，见 [Geist Materials](https://vercel.com/geist/materials)）。建议 **LG 16→12（卡片/工具卡/菜单/对话框）、XL 20→16（仅 composer）**，企业感立刻变硬。
4. **代码围栏缺"块语法"**：markdown 围栏是"填充面 + 无色边框"（`internal/uikit/markdown.go:49-51`），而工具卡里的代码有语言标签 + 复制（`p1-chat.png`）——同一产品里有两种代码外观。业界（Geist Code Block、ChatGPT、Claude）统一为"28 高头部（语言 + 复制）+ 1px 底分隔 + mono 正文"。
5. **阅读度量需要第二个宽度 token**：`TranscriptWidth=960`（`components.go:72`）在 14px 下对中文约 66 字/行（正好），对拉丁文约 130 字符/行（远超建议值）。结论：**960 保留给卡片/代码/表格，正文段落另加 `ProseMeasure=640`**（中文 ≈45 字/行、拉丁 ≈90 字符/行）；这正是"中文 40–50 字 + 拉丁 68–78ch 两个建议的交集"（推导见 §5.1）。
6. **状态语义有一处业界背离**：我们把 `running` 映射到 warning（`docs/DESIGN.md` §5.2），而 Carbon/VS Code/Linear 的"进行中"用**中性或强调色 + 进度语汇**，warning 保留给"需要你注意/等待你操作"。建议 `running` 改用 `Accent`（spinner/点）+ 状态词，`warning` 只给 needs-action（与本文件 §2 F-06 的冲突说明一致）。
7. **侧栏有"三套行解剖"，且官方件聚焦选中是满蓝**：导航行走 mygo 官方 `SidebarItem`（`ui/sidebar.go:164-200`），聚焦选中直接 `Background(t.Accent)` 实色填充；会话树走自绘富行（2px 左条 + `BgSelected` tint）；两者左缘、hover 底色都不同。业界（VS Code 侧栏、Linear 导航）用单一灰阶 tint 表示选中，绝不用实色（[Linear 重设计 II](https://linear.app/now/how-we-redesigned-the-linear-ui)）。建议用官方局部主题把侧栏的 `Accent` 换成 tint 合成色，并统一左缘到 `12+16+12` 网格（§2 F-07）。
8. **计划审阅缺"反馈未提交则禁用批准"的安全细节**：VS Code 的 Review Plan 卡片明确写了「Pending feedback disables approval until you submit or clear it」（[VS Code: Plan work](https://code.visualstudio.com/docs/agents/run/planning.md)）。这是"企业级交互细节"的典型样本，应直接移植。
9. **审批的"范围"必须是一等公民**：VS Code 的审批是「Allow Once（默认）+ 下拉扩展成 command prefix / host / file-or-folder，二选一 session 或 always」（[VS Code: Approvals](https://code.visualstudio.com/docs/agents/run/approvals.md)），Claude 是「Yes / Yes, and don't ask again for: `npm test *` / Yes, and switch to auto mode / No + `Esc to cancel` + `Tab to amend`」（[Claude Code: Permissions](https://code.claude.com/docs/en/permissions.md)）。我们只有「拒绝 / 本会话内始终允许 / 允许」三键，缺少"这次允许的是哪个前缀/域名/路径"的披露——**这是 P0 级交互细节**。
10. **键盘可发现性要系统化**：Raycast 把主/次动作固定绑定 `↵` 与 `⌘↵` 并在 ActionPanel 里显示快捷键（[Raycast Action Panel](https://developers.raycast.com/api-reference/user-interface/action-panel.md)），Zed 的 UI/UX checklist 要求"快捷键可发现（tooltip、菜单、文档）"且"每帧 ≤8ms"（[Zed UI/UX checklist](https://zed.dev/docs/development/ui-checklist)）。我们目前只有 composer 一行文案提示。建议统一 `uikit.Kbd`（11/14/Medium、`BgStrong` 底、`RadiusXS`、`BorderL2` 边）+ 每个可键盘触发的行内动作都带 kbd 提示 + 命令面板为唯一"全量入口"。

---

## 1. 现状（证据）

### 1.1 现在的设计 token 基线（只列与本报告相关的事实）

> **版本锚定（重要）**：下表是 **2026-11-18 00:00 工作树**的读数。同一时间窗内，设计系统工作流已在
> `internal/design/tokens.go`（字重烘焙：XL/L/M=600、S=14/22/500、XXXS=500）与 `internal/brand/palette.go`
> （新增 `BorderSubtle` 三档边线、`SurfaceOverlay`、`Hover` 转中性 ink 4.5%、`RowSelected` 12%→9%、侧栏 `#F9FAFB→#F7F8FA`）
> 落地了本报告 §5.1 / §5.4 / §5.5 的一部分建议 —— 标 **✅** 的行表示"我的读数已在并行工作树中被改动"，
> 复核时**以工作树为准**，本报告只负责给出"为什么这么改"的业界依据与剩余项。

| 类别 | 现值 | 位置 |
|---|---|---|
| 字号阶梯 ✅ | 读数为 24/32/400、20/28/500、16/24/400、14/22/400、14/24/400、13/20/400、12/18/400、11/14/400；**工作树已改为末 4 档 600/600/500/500（见 §5.1 表）** | `internal/design/tokens.go:37-56` |
| 字重 | 400 / 500 / 600（`WeightRegular/Medium/Semibold`） | `internal/design/components.go:24-28` |
| 间距 | 0/4/8/12/16/20/24/32（无 28） | `internal/design/components.go:11-19` |
| 控件高度 | 28 / 32 / 36 / 40 / 44（禁 38） | `components.go:39-43`（原注释在 `:30-38`） |
| 圆角 | 4 / 8 / 10 / 16 / 20 / 999 | `components.go:108-113` |
| 布局度量 | Sidebar 280、Rail 64、Inspector 320/260、ContentMin 520、Transcript 960、Settings 820×760 | `components.go:46-80` |
| 阴影 | `ShadowComposer` = 0,2,4,α.05；`ShadowOverlay` | `components.go:133-146` |
| 边线 ✅ | 读数亮色 ink 10%/16%、暗色白 12%/18%；**工作树已补 `BorderSubtle`（第三档）** | `internal/brand/palette.go:161-162`、`:210-211` |
| 焦点 | 亮 80% royal（4.24–4.59:1）、暗 70% cyan | `palette.go:173`、`:218` |
| 选中 ✅ | 读数亮 12% royal / 暗 18% cyan + 2px Accent 左条；**工作树已把亮色降到 9% 且 `Hover` 转中性 ink** | `palette.go:149`、`:201`；`AGENTS.md` §3.11 |
| Markdown 正文 | 14px、行高 1.55、块间距 0.72em、代码块无边框、代码 0.9em | `internal/uikit/markdown.go:30-56` |
| 审批卡 | `Border(1, Warning.Alpha(0.4))`、`RadiusLG`、顶部 `Warning.Alpha(0.22)` 满宽带 | `internal/uikit/approval.go:198`、`:219` |

### 1.2 PNG 证据（本仓库已生成）

| 文件 | 观察到的问题（人工看图） |
|---|---|
| `.cache/ui-conformance/p1-chat.png` | ① 右上 toast（警告）压在消息区右上，遮住首条消息的元信息行；② 侧栏导航行（自动化/洞察/能力）与工作区行、会话行三种节奏；③ composer 内"发送"按钮与底部 6 段状态条同为弱化态，主操作不显眼；④ 助手消息的元信息行（`23:55 · 用时 353ms · 42.1K tokens · ⋯`）用 12px 灰字横跨整列，右对齐后与正文左缘形成一条"虚线带"。 |
| `.cache/ui-conformance/p1-approval.png` | ① 审批卡黄色满宽带 + 0.4 黄描边 + 16 圆角 = 全屏最响；② "允许"蓝色主按钮 + composer 2px 蓝焦点环 + "发送"按钮同屏 3 处品牌蓝；③ 审批卡内"摘要"用 mono 框但无"命令/路径"标签层级；④ `Esc 拒绝` 孤零零挂在警示带最右，与底部动作条语义重复。 |
| `.cache/ui-conformance/p2-approval-diff-dark.png` | 暗色下 22% `Warning` 叠在 `#1B1B26` 上混成**橄榄/泥褐**宽带（读作"脏棕"而不是"警示"）；`允许` 用暗色 accent（天蓝）实色填充，是全卡最亮的元素；卡片本体只有 0.4 黄描边、没有 elevation，浮层感全靠那条黄边。→ 支持 F-01（去满宽带）与 §5.5（暗色 S4 需要比 S2 更高一阶，否则浮层"贴"在卡上）。 |

### 1.3 与本次调研直接相关的"已有资产"

- 已有 hairline/表面 ramp/强调预算/两级选中/危险确认安全默认等**规则**（`docs/DESIGN.md` §3、§5），本报告只做"与业界对齐后的数值与语义收紧"，不推翻。
- 已有 `uikit.StatusBadge / StatusText / ToolRow / ToolCallCard / ApprovalDialog / ComposerShell` 等入口（`docs/AGENT-SURFACES.md` S1–S10），本报告的落地建议全部映射到这些入口，不新建组件族。

---

## 2. 发现（编号 | 严重度 | 位置 | 问题 | 为什么是问题 | 具体改法）

> 严重度：P0 = 影响"企业级审美/一次误操作"的判断；P1 = 明显拉低精致度或成本可控地不一致；P2 = 打磨项。

| 编号 | 级别 | 位置 | 问题 | 为什么是问题（业界对照） | 具体改法 |
|---|---|---|---|---|---|
| F-01 | P0 | `internal/uikit/approval.go:198,219`；`p1-approval.png`、`p2-approval-diff-dark.png` | 审批卡用 22% Warning 满宽警示带 + 0.4 Warning 整卡描边 + 16 圆角，形成"黄色大色块" | VS Code 审批是**中性卡 + 工具名/参数/范围动作**（[Approvals](https://code.visualstudio.com/docs/agents/run/approvals.md)）；Claude 是**编号选项列表**（[Permissions](https://code.claude.com/docs/en/permissions.md)）。满宽色带在密集界面里等于"第二个主色"，会盖过正文；暗色下 22% Warning 还会混成泥褐色（见 §1.2 暗色快照） | 改为：卡体 `BgRaised` + `Border(1, BorderL3)` + `RadiusLG(12)`；**左侧 2px `Warning` 竖条**（不圆角）+ 头部一行 `uikit.StatusBadge("需要审批")` + 工具友好名（14/500）+ 右侧 `Esc 拒绝` 收进 footer；正文只留"影响声明（12/18 WarningText）→ 命令/路径 mono 块（带语义标签）→ 参数折叠" |
| F-02 | P0 | `p1-approval.png` 全屏 | 同屏 3 处品牌蓝：`允许` 主按钮 + composer 焦点环 + `发送` 按钮 | 业界强调预算普遍是"每视图一个主操作"（Linear 的 density-first 导航 + 单一 accent；Geist 的 `Color 7/8` 高对比面留给唯一主操作，[Geist Colors](https://vercel.com/geist/colors)） | 审批 pending 时 composer 进入"降级态"：焦点环降为 `BorderL3`（`Focus` 只在键盘聚焦时画）、发送按钮 `DisabledPrimary`；`允许` 保持全屏唯一 accent 填充 |
| F-03 | P0 | `docs/AGENT-SURFACES.md` S4 footer | 只有"拒绝 / 本会话内始终允许 / 允许"，**没有说明这次允许的是哪个命令前缀/域名/路径** | VS Code 的默认动作是 `Allow Once`，下拉才扩展到 prefix/host/path，并区分 in-this-session / always（[Approvals](https://code.visualstudio.com/docs/agents/run/approvals.md)）；Claude 把 `npm test *` 直接写在选项文案里（[Permissions](https://code.claude.com/docs/en/permissions.md)）。用户无法预判"始终允许"的范围 = 阴影权限 | footer 改为：`拒绝`（次）· `允许一次`（主）· `⋯ 更多`（本会话始终允许该前缀 / 总是允许（写入设置） / 查看规则）；主按钮文案随范围变化，范围在按钮左侧以小字披露（`范围：npm run *`） |
| F-04 | P1 | `internal/uikit/markdown.go:49-51` | markdown 代码围栏"填充面 + `ui.Transparent` 边框 + 8 圆角 + 0.8em 内距"，无头部、无语言标签、无复制 | Geist 有独立 Code Block 组件（[Materials](https://vercel.com/geist/materials) 组件索引）；主流聊天客户端的围栏普遍带"语言 + 复制"头部（**观察，无公开设计文档，不作数值依据**）。**我们工具卡里的代码有头、正文里的代码没头**，同一产品两套语言 | 新增 `uikit.MarkdownCodeBlock` 的头部：28 高、`BgStrong` 底、左 mono 12 语言名（无语言时显示 `text`）、右 `uikit.IconButton(Copy)`（16 图标，复制后 1.2s 变 Check）；块体 mono 13/20、`Space3` 内距、`Border(1, BorderL2)`、`RadiusSM(8)`；行内 code 保持 `BgStrong` + mono 12 |
| F-05 | P1 | `p1-chat.png` 底部状态条；`internal/ui/session/session_status_test.go:68` | 一行塞 6 段信息（轮次 · 工具步数 · tok/s · 42K/128K · 缓存命中 · 上下文剩余），等权重排开 | Zed 把用量放在模型选择器旁边、压缩事件作为可展开条目（[Zed Agent Panel](https://zed.dev/docs/ai/agent-panel)）；Claude Code 把 PR 状态做成 footer 里一个带色下划线的链接（[Interactive mode](https://code.claude.com/docs/en/interactive-mode.md)）。信息不是越多越好，是**分组 + 权重** | 状态条分三段：左=运行态（Spinner/状态词/队列 `×N`）｜中=空｜右=**用量组**（`42K/128K` + 百分比条 3px）+ `详情` 弹出 popover（轮次/步数/缓存/剩余）；数字用 tabular 或 mono 12 |
| F-06 | P1 | `docs/DESIGN.md` §5.2 状态矩阵 | `running` 复用 `warning` 语义（Warning/WarningText） | Carbon 的 status indicator 把"进行中/反馈"归为中/低注意力，warning 归"需要立即注意"（[Status indicators](https://www.carbondesignsystem.com/building-blocks/core/patterns/status-indicators)）；VS Code 的"working"是独立的会话类别（[Inbox mode](https://code.visualstudio.com/docs/agents/run/sessions/organize-sessions.md)） | **与 `AGENTS.md` §3.7 / DESIGN.md §5.2 冲突点（先声明）**：建议把 `running` 从 warning 移出，用 `Accent`（spinner/点）+ `LabelSecondary` 词；`warning` 只保留 needs-action / 审批等待 / 注意；`cancelled` 保持中性。若 Lead 不接受改语义，则退让为：running 用 `LabelSecondary`，仅 spinner 用 Accent |
| F-07 | **P0** | `internal/ui/sidebar/sidebar.go:468` + mygo `github.com/egoist/mygo@v0.9.2/ui/sidebar.go:164-200` | 同一列存在**三套行解剖**：导航行走官方 `SidebarItem`（内距 `Space2`/`Space1.5`；**聚焦选中 = 整块 `Accent` 填充 + `AccentText`**，非聚焦选中 = `SurfacePressed`，hover = `SurfaceHover`）；会话树走自绘富行（`Space3` 缩进 + 2px `Accent` 左条 + `BgSelected` tint + `BgHover` hover） | ① 官方件聚焦态是**满蓝填充**（mygo `sidebar.go:188`），会吃掉一屏的强调预算并与主按钮争色——`AGENTS.md` §3.11 的「官方 Sidebar 原生 tint 视为合规」豁免只覆盖非聚焦态，聚焦态是实色，不等于 tint；② 两套左缘让导航文字与会话标题不在同一条竖线上，而 Linear 重设计的第一目标正是 alignment（[Linear 重设计 II](https://linear.app/now/how-we-redesigned-the-linear-ui)）；③ 两种 hover 底色（`SurfaceHover` vs `BgHover`）在暗色下亮度不同，同列出现两种"悬浮感" | 三选一并在 `DESIGN.md` 登记：(A) 会话树也改走官方 `SidebarItem`（最一致，但失去 2px 左条与运行 glyph）；(B) 导航行改自绘富行（统一 tint+左条，需自绘键盘/hover/a11y，成本最高）；**(C) 推荐**：保留官方件，用官方「局部主题」`c.Themed()` 给侧栏子树装配一份 `Accent = BgSelected 合成色 / AccentText = LabelPrimary` 的侧栏专用主题（`DESIGN.md` §1.2-G 允许的局部主题，不是元素覆写），使聚焦/非聚焦选中都读作 tint；同时把导航与会话行的左缘统一到 `12 + 16 + 12` 网格，hover 统一走 `BgHover` |
| F-08 | P1 | `internal/design/components.go:111-112` | 卡片/对话框 16、composer 20 圆角，比业界更"软" | Geist 容器 6/12、浮层 12、全屏 16（[Materials](https://vercel.com/geist/materials)）；Carbon/GNOME 的桌面容器更接近 4–8 | XS4 / SM8 / MD10 / **LG12**（卡片、工具卡、菜单、对话框、toast）/ **XL16**（仅 composer）/ Max；同屏 ≤3 档的规则不变 |
| F-09 | P1 | `palette.go:173`（亮）/`:218`（暗） | 焦点环是"贴着边的 2px 色环"，无 offset | Geist 明确要求"不要只靠阴影/边框表达层级，focusable 子元素要有 focus-visible ring"（[Materials](https://vercel.com/geist/materials)）；Zed checklist 要求"hover 与 focus 指示清晰一致" | `FocusRingWidth=2` + `FocusRingOffset=2`（外扩），环圆角=元素圆角+2；键盘/程序聚焦才画（鼠标点击不画）；深色填充上用 `OnPrimary` 1px 内衬保证对比 |
| F-10 | P1 | `.cache/ui-conformance/p1-chat.png` toast | toast 出现在右上、压住消息区首行的元信息 | VS Code 的通知在右下、且"一次一条、每条带 Don't show again"（[Notifications](https://code.visualstudio.com/api/ux-guidelines/notifications.md)） | toast 视觉上限宽 320，锚在顶栏下方 + 右侧 `Space4`；正文最多 2 行；同类合并 `×N`（已实现）；错误类 toast 不自动消失 |
| F-11 | P2 | `p1-chat.png` 元信息行 | 助手元信息行（时间/用/…）横跨整列且常显 | ChatGPT/Claude 的"复制/重新生成"是 hover 才出现；Zed 把反馈按钮放在回答末尾（[Agent Panel](https://zed.dev/docs/ai/agent-panel)） | 元信息行只在 hover/focus 或流式结束时短暂显示；常显只留"用时 + tokens"两项；动作收进单一 `⋯`（已在 S1 规定），hover 显形不影响布局（行高固定 24） |
| F-12 | P2 | `.cache/ui-conformance/p1-chat.png` 侧栏；`internal/ui/sidebar/sidebar.go:375`（注释写明 36 DIP） | "新对话"是一个 36 高、全宽、带描边的按钮，视觉权重高于导航项 | Linear/Raycast 的"新建"通常是**无边框的 accent 文字行**或顶栏 `+` 图标（命令优先） | 侧栏首行改为 32 高、无边框、hover 才出 `BgHover`、左侧 `Plus` 图标 + "新对话" `WeightMedium`；键盘 `Cmd/Ctrl+N` 提示以 kbd 微标签右对齐 |
| F-13 | P2 | 全局 | 键位提示只有 composer 一行文案（`Enter 发送 · Shift+Enter 换行`） | Raycast 在 ActionPanel 内逐项显示快捷键（[Action Panel](https://developers.raycast.com/api-reference/user-interface/action-panel.md)）；Zed checklist 把"快捷键可发现"列为必检 | 新增 `uikit.Kbd`（11/14/500、`BgStrong`、`BorderL2`、`RadiusXS`、内距 4×2）；用于：composer 提示、命令面板行尾、菜单项右缘、工具提示文案 |
| F-14 | P2 | `docs/DESIGN.md` §3.2 | 32 处裸 `FontWeight` 字面量 warn + 间接字重 warn | 业界做法是把字重**编进 type role**（Windows: "regular for most text, Semibold for titles"；Geist: Heading/Label/Copy/Button 各自带固定字重） | 把字重烘焙进 role（见 §5.1 表），call site 不再传 `FontWeight`，可同时消掉 DS-03 存量 |

---

## 3. 业界调研：设计语言与交互细节（逐产品 → 可迁移决策）

### 3.0 方法与证据分级（先说清楚哪些是证据、哪些是判断）

| 等级 | 含义 | 本次取得的样本 |
|---|---|---|
| **A** | 官方设计系统文档，含 token/组件规范 | Vercel Geist（Typ./Colors/Materials）、IBM Carbon（Spacing/Typography/Status/Carbon-for-AI）、Microsoft Windows 类型系统、GNOME HIG（Typography/Utility Pane）、GitHub Primer（组件词汇，数值页被本环境截断） |
| **B** | 官方产品文档（讲"怎么用/什么行为"，不公开 token） | VS Code（UX guidelines + Chat/Approvals/Planning/Tools/Sessions）、Claude Code（Interactive mode / Permissions / Desktop）、Cursor（Agent / Plan Mode）、Zed（Agent Panel / UI checklist）、Raycast（List / Action Panel API）、Linear（重设计 I/II 博客） |
| **C** | 官方博客/社区模式库 | Shape of AI（AI UX pattern 分类） |
| **D** | 本环境**未找到公开证据** | ChatGPT 桌面版与 claude.ai 聊天面（OpenAI/Anthropic 未公开设计系统；`help.openai.com` 与 `openai.com/index/*` 在本环境返回 **403**）、Perplexity、NotebookLM、Gemini 桌面、Notion AI、JetBrains AI Assistant（`jetbrains.design` 跨域重定向）、Warp 的 block 视觉（文档页只返回导航）、Cline / Roo Code 源码（本环境未能取到 raw 文件）、Figma / Slack / Obsidian / Things（无公开 token 文档） |

> **没有任何一条设计决策建立在 D 级材料上。** D 级产品只作为"我们也该看"的清单存在，报告中不引用其数值。

### 3.1 企业级工具标杆

#### 3.1.1 GitHub Primer `[A/B]`

- 组件词汇表本身就是"企业清晰"的语义清单：`StateLabel`（状态）、`InlineMessage`（行内消息，区别于 `Banner`）、`Blankslate`（空态）、`KeybindingHint`（键位）、`ActionBar/ActionList/PageHeader/PageLayout/SplitPageLayout`、`ConfirmationDialog`、`SegmentedControl`、`CounterLabel`、`RelativeTime`（[Primer Product UI](https://primer.style/product/primitives/size/)）。
- 规范分层为 `Color / Size / Typography / Token Names`，并把 **UI Patterns**（Empty states、Loading、Navigation、Notification messaging、Progressive disclosure、Saving、Degraded experiences）与 **Scenario Patterns**（Copy/Create/Customize/Delegate/Delete/Edit/Filter/Search/View）分开（同上）。这是"状态与流程优先"的成熟表达。
- 数值页（Size/Typography）在本环境抓取时被导航截断，**未取得可引用数值**，因此本报告不引用 Primer 的 px 值，只借用其**语义分层的组织方式**。
- **可迁移决策**：① 我们的 `uikit` 应显式区分 `InlineMessage`（行内、贴在触发点）与 `Banner`（区域级、常驻）——当前 `docs/AGENT-SURFACES.md` 只有 `ErrorPanel` 与 `StatusBadge` 两档；② 建立 `uikit.KeybindingHint`；③ 空态统一 `uikit.Blankslate`（图标 24 + 一句解释 + 一个主操作 + 可选 2–3 个示例）。

#### 3.1.2 Vercel Geist `[A]` — 本次最"可直接抄数值"的对照物

- **字号角色**（[Geist Typography](https://vercel.com/geist/typography)）：`Heading 72…14`、`Button 16/14/12`（12 只用于输入框内的小按钮）、`Label 20/18/16/14/13/12`（`Label 14` 是"最常用"，`Label 13` 做第二行，`Label 12` 带 Strong + CAPS 用于"繁忙视图的三级文本，如 Comments/Show More/日历大写"）、`Copy 24…13`（`Copy 16` 用于 Modal 这种"文字可以呼吸"的更简单视图）。**Mono 有独立档**：`Label 13 Mono` "与 Label 14 配对，视觉上更平衡"。
- **颜色角色**（[Geist Colors](https://vercel.com/geist/colors)）：10 个色阶；**gray vs gray-alpha** 的取舍写得很硬——"Gray 用于 text/icons/fills/borders/dividers by default；gray-alpha 只在背景需要透出时（shadows/overlays）"。四条硬规则：**"Draw each border once"**（不要叠加两层 alpha 边，否则交角处会发亮/发深）、**"Keep material shadows translucent"**（不透明的阴影会留下灰晕）、`Background 1/2`（2 只在需要轻微区分时用）、`Color 1/2/3` = default/hover/active，`Color 4/5/6` = default/hover/active 边框，`Color 7/8` = 高对比主操作面。
- **材料/圆角/层级**（[Geist Materials](https://vercel.com/geist/materials)）：`base 6`、`small 6`、`medium 12`、`large 12`、`tooltip 6`、`menu 12`、`modal 12`、`fullscreen 16`；规则："用 Material 而不是手搓 radius/fill/stroke/shadow"、"**同一元素不要叠两个 Material**"、"**选择仍然读得出层级的最低 elevation**，过度抬升是视觉噪音的常见来源"、"**不要只靠阴影表达层级**，要给 focusable 子元素配 focus-visible ring"、"在两种主题下都要测（暗色阴影对比更弱）"。
- **可迁移决策**：① 圆角 6/12/16 的三级语义（容器/浮层/全屏）→ 我们的 LG12/XL16；② `Color 4/5/6` 的"边框三态"→ 我们只有 `BorderL2/L3`，建议补 `BorderL1（弱）` 并明确 L1=内部细分隔、L2=结构、L3=可交互边界；③ "每边框只画一次" → 直接作为评审规则（我们已有"控件套控件双重描边"的历史问题，见 S1 记录）；④ 阴影必须半透明（我们 `ShadowComposer` 已是 α.05，合规）；⑤ 最低 elevation 原则 → 工具卡/消息卡**不加阴影**（已是约定，写进评审）。

#### 3.1.3 IBM Carbon `[A]` — 企业密度的教科书

- **间距**（[Carbon Spacing](https://www.carbondesignsystem.com/building-blocks/foundations/spacing/overview)）：`2,4,8,12,16,24,32,40,48,64,80,96,160`，并以"2、4、8 的倍数"解释为什么这样切。文档还给了两条可直接引用的判据：**"元素靠近 = 被视为有关系；间距加大 = 关系减弱"**、**"空间可以让内容分区，而不需要线或其他图形分隔"**——这正是"少画边框、多给间距"的理论依据。
- **字号**（[Carbon Type sets](https://www.carbondesignsystem.com/building-blocks/foundations/typography/type-sets)）：productive 基准 14 / expressive 基准 16；`code-01` 12/16（字距 .32）、`label-01` 12/16、`helper-text-01` 12/16、`body-compact-01` **14/18**、`body-01` **14/20**、`body-compact-02` 16/22、`body-02` 16/24、`heading-compact-01` **14/18/600**（字距 .16）。即：**产品界面里 14px 正文只配 18–20 行高（1.29–1.43）**，行高靠"紧凑/长文"两档区分。
- **状态指示**（[Status indicators](https://www.carbondesignsystem.com/building-blocks/core/patterns/status-indicators)）：四种元素——符号/形状/颜色/文字，**"为满足 WCAG，至少要有三种"**；三种注意力等级（high = 需要立即动作，medium = 反馈/进行中，low = 有更新）；**"超过 5–6 个指示器会让用户过载"**；合并状态时"用最高注意力色代表整组"；状态色语义表（红=错误、橙=严重警告、黄=警告、绿=正常/成功、蓝=被动通知/进度、灰=草稿未开始、紫=异常/未定义）。
- **Carbon for AI**（[Carbon for AI](https://www.carbondesignsystem.com/building-blocks/foundations/carbon-for-ai)）：AI 内容必须被标记，**"AI label 是通往可解释性的入口"**，可解释性分层"先给摘要，再按需展开"；AI 视觉语言用"brightness/glow/gradient 的光感"，但**"不要把 AI 样式当装饰（strictly intended to identify AI usage）"**。
- **可迁移决策**：① 我们的状态徽章已是"tint + AA 文字 + 必填标签"，但**缺"形状"通道**——建议加"状态点/图标形状"作为第二通道，并在评审里要求"状态至少 2 通道（我们不做四通道）"；② "每屏 ≤6 个状态指示器"可作 lint 软规则；③ **禁止给"AI 生成"加光效/渐变**（我们本来没有，写进反模式能防以后加）；④ 行高纪律：14px 正文 22 已高于 Carbon 的 18–20，属于"阅读型"而非"产品型"——说明我们的会话正文应按"长文"处理，表单/行内说明应更紧（13/20 已合规）。

#### 3.1.4 Windows / Fluent 类型系统 `[A]`

（`fluent2.microsoft.design` 在本环境只返回导航壳，改抓取 Windows 官方文档）
- 类型阶梯：`caption 12/16`、`body 14/20`、`body strong 14/20 semibold`、`body large 18/24`、`subtitle 20/28 semibold`、`title 28/36 semibold`、`title large 40/52`、`display 68/92`（[Windows Typography](https://learn.microsoft.com/en-us/windows/apps/design/signature-experiences/typography)）。
- 三条硬规则：**"Bold 与 Italic 不在 ramp 里；用 Semibold 代替 Bold；Italic 被排除（降低可读性，读写障碍用户尤其受影响）"**；**最小字号 14px semibold / 12px regular**（更小在某些语言不可读）；**句首大写（sentence case）用于所有 UI 文本，包括标题**。
- 行宽：**"保持每行 50–60 个字母"**，并明确"少于 20 或多于 60 难读"（同上）。
- 截断：默认"裁剪 + 整词换行"，**"不要用省略号来避免视觉杂乱"**；只有在容器边界不清或存在"查看全部"链接时才用省略号。
- **可迁移决策**：① 我们的 600 只用于 ≥16px 的标题（14px 及以下用 500），把"bold soup"消灭；② 12px 是元数据下限（与我们 `TypeXXXS=11` 只做微标签一致）；③ 中文 UI 的句子不要 Title Case；④ 省略号策略写进 `uikit` 的行组件：有"查看全部"才用 `…`。

#### 3.1.5 GNOME HIG（原生桌面）`[A]`

- 字体：**"尽可能使用发行版/系统默认字体"**、**"尽量减少字号与字重种类"**、**"避免 italic/oblique"**、**"不要全大写"**、**"不要硬编码字号，用标准样式或相对值"**（[GNOME Typography](https://developer.gnome.org/hig/guidelines/typography.html)）。→ 与我们 `brand.SystemFont`（`-apple-system, Segoe UI, PingFang SC, …`）+ `TextScale` 的设计**完全同向**，这是"原生控件可实现性"的加分项。
- 工具面板（[Utility Panes](https://developer.gnome.org/hig/patterns/containers/utility-panes.html)）：**"影响主视图的放左边，从属于主视图的放右边"**；**"utility pane 不与 header bar 相交"**；**"窗口放不下时让它覆盖主视图（flap）而不是挤压"**；**"可切换时用 F9"**。→ 我们的右栏检查器（`InspectorWidth=320`、窄窗收缩到 rail 的规则）与这条一致；补充：**F9 作为检查器切换键**（我们目前没有），并且检查器**不画自己的顶带**（不与标题行相交）。
- **可迁移决策**：① 检查器切换键 `F9`（GNOME 生态惯例、不与 `Cmd/Ctrl+B` 冲突）；② 检查器窄窗"覆盖"而非"挤压"已有规则，写进文档即可；③ 保持系统字体栈（不引第三方字体）。

#### 3.1.6 Linear `[B/C]`

- 重设计 II 的自我描述就是"企业级克制"的定义：**"我们调整了 sidebar、tabs、headers、panels，以减少视觉噪音、保持视觉对齐、并提高导航元素层级与密度"**（[How we redesigned the Linear UI](https://linear.app/now/how-we-redesigned-the-linear-ui)）。
- 方法论：先锁定"**inverted L**"（侧栏 + 顶栏构成的全局 chrome）作为设计对象；用 Apple 的平台规范校准原生感；主题生成从 98 个变量收敛到 **3 个（base color / accent color / contrast）**，用 **LCH** 保证不同基色下亮度感知一致，`contrast` 变量顺带产出高对比主题（同上）。→ 对我们的启示：**主题应该由"表面基色 + 强调色 + 对比度"三个旋钮生成**，而不是 27 个手调 hex；这与 `internal/brand` 的 palette 结构可以对齐（长期项）。
- **可迁移决策**：① 侧栏/顶栏/面板的对齐是"看不出来但用几分钟就能感觉到"的部分（Linear 原话），列为本轮 P0 验收项；② 选中态用低 alpha tint + 左条（我们已有）；③ 主题三旋钮 + LCH 生成（长期，P2）。

#### 3.1.7 VS Code 外壳与通知 `[B]`

- 侧栏：**"一个 View Container 通常就够；3–5 个 View 是舒适上限"**、**"不要把本该是 Command 的东西塞进侧栏"**、**"不要重复已有功能"**（[Sidebars](https://code.visualstudio.com/api/ux-guidelines/sidebars.md)）。
- 通知：给了一张**决策树**（多步输入→Quick Pick；需立即输入→Modal；低优先进度→状态栏；用户触发的交互→找对时机；多条合并成一条；不需要就别显示）；硬规则 **"每条通知都给 Do not show again"**、**"一次只显示一条"**、**"不要用于推广/不要一装上就问反馈/没有动作就不要显示动作"**；Modal 的使用条件写得很克制："只在需要立即交互时；提供 Always/Never；可用 checkbox 记住选择；不要用 Modal 确认多步操作"（[Notifications](https://code.visualstudio.com/api/ux-guidelines/notifications.md)）。
- **可迁移决策**：① 我们的 toast 体系补"Do not show again"（对重复性提示）；② 审批**不进 Modal**（我们已如此，正确）；③ 需要"立即输入"的（如新建工作区路径）才用 Modal。

#### 3.1.8 Raycast（命令优先）`[B]`

- `List`：内置搜索栏 + `searchBarAccessory` 下拉（`⌘P`）；行有 `title/subtitle` 与 **accessories**（`tag`/`text`/`date`/`icon`，都支持 `tooltip` 与 secondary text 色），`isShowingDetail` 时**建议把 accessory 的信息移进右侧详情**（避免重复）；`EmptyView` 是组件（[Raycast List](https://developers.raycast.com/api-reference/user-interface/list.md)）。
- `ActionPanel`：**第一个动作 = 主动作，自动绑定 `↵`；第二个 = 次动作，`⌘↵`**；动作可分 section（含 `Danger zone`）；快捷键在面板里可见；支持子菜单（[Raycast Action Panel](https://developers.raycast.com/api-reference/user-interface/action-panel.md)）。
- **可迁移决策**：① "主动作 `↵` / 次动作 `⌘↵`"作为我们**行内动作**的统一绑定（工具行、审批卡、计划卡、会话行）；② 行 accessory 的 `date`/`tag` 语义 → 我们侧栏会话行的"行尾状态词/未读点"已经是这个思路；③ **Danger zone 分节** → 会话 `⋯` 菜单里的"删除会话"单独成节。

### 3.2 AI 客户端

#### 3.2.1 VS Code + GitHub Copilot Chat `[B]` — 本次最重要的"企业级 agent 界面"样本

- **布局**：Chat view 在 Secondary Side Bar；三种落点 = 侧栏 / 编辑器标签 / 独立窗口；sessions list 有 **compact（列表与对话共面板，返回键回列表）与 side-by-side** 两种（[Chat view](https://code.visualstudio.com/docs/agents/run/chat-view.md)）。
- **状态指示**：标题栏命令中心有 **unread badge / in-progress badge**，点 badge 直接过滤会话列表（同上）。
- **工作集**：composer 上方可展开 **working set**（受跟踪的编辑），带 **Keep / Undo**；"Edits are already saved; Keep clears their pending-review state"（同上）。→ 这正是我们"回合尾文件卡"的成熟版：**动作叫 Keep/Undo，而不是"接受/拒绝"**，且明确"文件已保存"。
- **工具调用呈现**：**默认折叠**，"expand a tool summary to inspect the call and its output"；终端命令有 **Show Output (`>`) / Show Terminal**；长命令出现 **Continue in Background**；可配置 `collapsedTools`（off / withThinking / always）与 `thinkingStyle: collapsed`（[Tools](https://code.visualstudio.com/docs/agents/run/tools.md)）。→ 直接对应我们的 S1「工作步骤 compact/standard/detailed/verbose」与 S3 工具行，**可借鉴的是"Show Output / Show Terminal / Continue in Background"三件套**。
- **审批**（[Approvals](https://code.visualstudio.com/docs/agents/run/approvals.md)）：两套权限层（**approvals** 决定是否要确认，**sandboxing** 限制命令能碰什么，二者独立）；权限等级三档（**Manual 默认 / Assisted（LLM 判官）/ Allow all**）；**Allow Once 是终端/网页/文件请求的默认动作**，下拉可扩到"命令前缀 / 主机 / 文件或文件夹"，并区分 **in this Session / Always Allow**；URL 审批**两步**（请求批准 vs 把响应放进上下文，防 prompt injection）；计划审阅里的"批准计划"与"批准工具调用"是**两个独立决定**。
- **计划审阅**（[Plan work](https://code.visualstudio.com/docs/agents/run/planning.md)）：弹出 **Review Plan 卡** → `Open Full Plan`（在 Markdown 编辑器打开、可编辑、可加行内评论）→ `Submit Feedback` → 动作四选一：`Implement Plan` / `Approve Plan Only` / `Implement with Autopilot` / `Reject`；**"Pending feedback disables approval until you submit or clear it"**；复核清单四条（Scope / Project fit / Decisions / Verification）。
- **会话组织**（[Inbox mode](https://code.visualstudio.com/docs/agents/run/sessions/organize-sessions.md)）：分组五类 **Inbox / Working / Ready to merge / On hold / Done**；`Dismiss from Inbox`（不归档）与 `Mark Session as Done`（归档）是两个不同动作；移动会话**默认不弹确认**但给 10 秒 Undo——"**低风险操作给撤销，而不是给确认**"。
- **可迁移决策（高价值）**：① 计划卡的四动作 + 反馈门禁；② 审批的范围下拉与 session/always 二分；③ URL/外部数据"请求 vs 响应"两步批准（我们接 web 工具时应有）；④ 会话列表的"Working/Attention"分区与 badge 过滤；⑤ **10 秒 Undo 替代确认**（用于我们的"归档会话""移动消息"这类可逆动作）。

#### 3.2.2 Claude Code CLI + Claude Desktop `[B]` — 权限提示与"低成本旁问"的教科书

- **权限提示解剖**（[Permissions](https://code.claude.com/docs/en/permissions.md)）：标题（`Bash command`）→ 描述（`Run the test suite`）→ 命令本体 → 一行 `This command requires approval` → 选项：`Yes` / `Yes, and don't ask again for: npm test *` / `Yes, and switch to auto mode` / `No` → footer：**`Esc to cancel`**、**`Tab to amend`**；在 Yes/No 上按 `Tab` 可**附加评论**（同意后把评论发给模型；拒绝时把评论当作理由，模型继续工作）。规则语义：`deny → ask → allow` 顺序求值，deny 覆盖 allow。
- **模式**：`default(Manual) / acceptEdits / plan / auto / dontAsk / bypassPermissions`，`Shift+Tab` 循环（[Interactive mode](https://code.claude.com/docs/en/interactive-mode.md)）。
- **transcript 呈现**：`Ctrl+O` 打开 transcript viewer 显示**每条 assistant 消息的时间戳与所用模型**；**默认折叠的行**（MCP 调用压成一行 `Called slack 3 times`、跨会话消息压成 `Message from @<sender>`）；任务清单 `Ctrl+T`（最多显示 5 条）；`/diff` 在 ≥110 列时开成"面板"，≥144 列自动打开，按 `T1/T2` 看每一轮编辑；PR 状态在 footer 用**彩色下划线**表示（绿=approved、黄=pending、红=changes requested、灰=draft）；`/btw` 旁问面板不打断主回合。
- **桌面版**（[Desktop](https://code.claude.com/docs/en/desktop.md)）：会话侧栏并行多会话；**prompt box 下方一行选择器：environment / project folder / permission mode / model**；建议提示以**灰字**出现在空输入框，`Tab` 或 `→` 接受（`Enter` 不会误发）；`Changes` 卡显示 `+12 -1`，点开进入 diff view；**悬停行号出现 `+` 可加评论，`Cmd+Enter` 批量提交**；`Code review` 卡按文件分组，提供 `Walk through in Changes`（逐条走查）与 `Apply fixes`；站点操作审批卡是 `Allow once / Always allow / Deny`。
- **可迁移决策（高价值）**：① 审批 footer 的两条 kbd（`Esc 取消` / `Tab 修改`）与"附加评论"能力；② **默认选项文案要把范围写进句子**（`Yes, and don't ask again for: npm test *`）；③ 折叠行的"结果优先"表达（`Called slack 3 times` 而不是三条记录）；④ 建议提示用灰字 + Tab 接受、Enter 绝不误发；⑤ 行级评论 + 批量提交（我们的 diff 审阅可做简化版）。

#### 3.2.3 Cursor `[B]`

- Agent 在侧栏（`Cmd+I`）；工具集列全（搜索/编辑/终端/浏览器/图像/提问）（[Agent overview](https://cursor.com/docs/agent/overview)）。
- **Checkpoints**：编辑前自动快照，**在 chat timeline 里点任一 checkpoint 预览并按需 restore**；上一轮请求上也有 `Restore Checkpoint`；消息 hover 出现 `+`（[同上](https://cursor.com/docs/agent/overview)）。
- **运行期输入**：**Queue（等本轮结束）** 与 **Send follow-up now（在下一个 tool call 处 steer）** 两种；**Ask questions 期间 agent 继续读文件/改代码/跑命令**，答案到达即并入（同上）。
- **Plan Mode**：`Shift+Tab` 轮转；流程是"问澄清问题 → 研究代码库 → 生成计划 → 你在 chat 或 markdown 文件里改 → 点构建"；计划默认存家目录，`Save to workspace` 才进仓库（[Plan Mode](https://cursor.com/docs/agent/plan-mode)）。
- **可迁移决策**：① 计划**默认不写进仓库**、显式"保存到工作区"（我们 `plan_review` 可对齐）；② checkpoint 作为"消息级可恢复点"进消息元信息（我们的 `⋯` 里加"从此处回滚"）；③ "ask 期间继续工作"是我们 `ask_user` 可以学的并发语义（当前是阻断式，需后端支持，登记为后续项）。

#### 3.2.4 Zed `[B]`

- Agent Panel：消息**可点开编辑并重新提交**；**队列消息可"Steer"**（在下一个 step 边界插入）、可 `Send Now`（双击 Enter）、可整体取回；右键菜单有 `Copy / Copy as Markdown / Copy This Agent Response / Scroll to Top/Bottom / Open Thread as Markdown`；长会话有"跳到我的上一个 prompt / 跳到开头"的滚动箭头；线程标题自动生成 + 可编辑 + 可重新生成；**"Following the agent"**（十字准星，编辑器跟随 agent 触摸的文件）；**Review Changes** 折叠条在编辑器上方，显示文件数/行数，点开进 multi-buffer，**逐 hunk accept/reject**；token 用量显示在 profile 选择器旁；压缩后线程里出现可展开的 **Context Compacted** 条目（[Agent Panel](https://zed.dev/docs/ai/agent-panel)）。
- **UI/UX checklist**（[Zed UI/UX checklist](https://zed.dev/docs/development/ui-checklist)）：键盘/鼠标/亮暗/窄分栏/13 寸屏/高 DPI/对话框居中且在视口内/系统设置尊重/即时反馈（慢操作必须有进行中指示）/大文件不退化/**"每帧 ≤8ms（120fps）"**/术语一致/错误文案可行动/新手不用文档也能懂/有 undo-redo/专家功能"可发现但不打扰"。
- **可迁移决策**：① "Copy as Markdown / Open Thread as Markdown"作为会话 `⋯` 的两个条目（我们已有"导出原始日志"，缺 Markdown 导出）；② **steer vs queue** 的两种语义（我们目前只有 queue，登记为后续项）；③ 检查器/右栏的"逐 hunk 接受"；④ 把 Zed checklist 直接改写成我们的**视觉评审 checklist**（narrow/13"/暗色/8ms/对话框居中）。

#### 3.2.5 Shape of AI（AI UX 模式库）`[C]`

- 模式分类：**Wayfinders**（initial CTA、suggestions、follow up、templates、gallery、nudges、prompt details）、**Inputs**（open input、regenerate、inline action、summary…）、**Tuners**（attachments、connectors、filters、model management、modes、parameters…）、**Governors**（action plan、branches、citations、controls、cost estimates、memory、references、sample response、shared vision、**stream of thought**、variations、**verification**）、**Trust builders**（caveat、consent、data ownership、disclosure、footprints、incognito、watermark）、**Identifiers**（avatar、color、iconography、name、personality）（[shapeof.ai](https://www.shapeof.ai/)）。
- **可迁移决策**：① 我们的"工作步骤"对应 `stream of thought`，"审批"对应 `verification`，"计划审阅"对应 `action plan`，"用量/tokens"对应 `cost estimates`——**用这套名字统一我们的文档术语**，避免每个模块自创词；② 空态（Wayfinders）与 `cost estimates`（我们的 status bar 用量）是当前最弱的两块。

#### 3.2.6 未取得公开证据的产品 `[D]`（只列，不引用）

ChatGPT 桌面版、claude.ai 聊天面、Perplexity、NotebookLM、Gemini 桌面、Notion AI、JetBrains AI Assistant、Cline / Roo Code、Warp（仅拿到 block 的目录结构，没有视觉参数）。
**处理**：本报告不对其 token/尺寸/动效下任何结论；若 Lead 需要，可用真实截图做"像素采样"另行补证（不属于 A 阶段的文档调研）。

### 3.3 横向对比表（只看能改变我们决策的列）

| 产品 | 外壳/导航 | 字号与行高锚点 | 行密度 | 边线策略 | 圆角 | 强调色配额 | 选中/悬浮 | 焦点环 | composer 形态 | 工具调用呈现 | 审批呈现 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| **Linear** `[B/C]` | inverted-L（侧栏+顶栏）；减少噪音、提高导航密度 | 未公开 token | 未公开；目标=密度 | 未公开；设计以对齐为主 | 未公开 | 单一 accent（主题三旋钮） | 低 alpha tint | 未公开 | 未公开 | 未公开 | 未公开 |
| **Geist** `[A]` | 未规定（web 设计系统） | Label 12–20 / Copy 13–24 / Button 12–16 | — | **每边框只画一次**；gray vs gray-alpha；边框三态 4/5/6 | **6 / 12 / 16** | 高对比面 7/8 只给主操作 | `Color 1/2/3`=default/hover/active | focus-visible ring（必须与阴影配合） | — | — | — |
| **Primer** `[A/B]` | PageHeader/PageLayout/Split | Size/Typography 分四级（数值未取到） | 组件级 | `InlineMessage` vs `Banner` 分档 | 组件级 | 语义色 + `StateLabel` | NavList/ActionList 原生 | 组件内置 | 无（非聊天产品） | — | `ConfirmationDialog` |
| **Carbon** `[A]` | UI Shell（header/left panel/right panel） | productive 14 / expressive 16；code 12/16；body 14/18–20 | 密 | 细线 + 层色；状态 4 元素≥3 | 组件级 | status palette 明确语义 | 组件内置 | 组件内置 | AI Chat 组件库 | — | Modal/Fluid 模式 |
| **Windows** `[A]` | — | 12/16、14/20、18/24、20/28、28/36、40/52、68/92 | — | — | — | — | — | — | — | — | Modal（立即交互才用） |
| **VS Code** `[B]` | 侧栏 3–5 view；Secondary Sidebar；编辑器标签/独立窗口 | 未公开 | 侧栏树紧凑 | hairline 分区 | — | — | 原生 | 原生 | 输入 + 4 个选择器（target/agent/model/permissions） | **默认折叠**；Show Output / Show Terminal / Continue in Background | **Allow Once 默认 + 范围下拉 + session/always** |
| **Claude Code/Desktop** `[B]` | 会话侧栏 + 多窗格（chat/diff/browser/terminal） | 未公开 | 终端密度 | 面板描边 | — | — | — | — | prompt box + 下方一行模式选择器；灰字建议 Tab 接受 | 折叠行（`Called slack 3 times`）+ 时间戳/模型 | 编号选项 + `Esc cancel` / `Tab amend` + 范围写进选项 |
| **Cursor** `[B]` | 侧栏 + plan/agent 切换 | 未公开 | — | — | — | — | — | — | 输入 + 模式（Shift+Tab） | checkpoint 在 timeline + 消息 hover `+` | 站点/工具授权（Allow once/Always） |
| **Zed** `[B]` | Agent Panel + Threads Sidebar + 多 worktree | 编辑器语境 | — | 面板描边 | — | — | — | 强调"hover/focus 清晰一致" | message editor（可扩展）+ model/profile 选择器 | 流式工具指示 + Review Changes 折叠条 | 工具权限（allow/deny/confirm） |
| **Raycast** `[B]` | 命令优先：搜索栏 + List + ActionPanel | 未公开 | 行 + accessories | 行分隔 | — | — | 原生选中 | 原生 | 命令输入（无多行 composer） | — | `Danger zone` 分节 |
| **GNOME** `[A]` | Header bar + Sidebar / Utility pane（右, F9, 不相交, 窄窗覆盖） | 标准 Body/Heading/Caption（不硬编码） | — | 分组用间距优先 | — | — | 原生 | 原生 | — | — | Modal（AdwMessageDialog） |

### 3.4 已被业界"收敛"的 12 条默认（我们应直接采纳）

1. **单一强调色 + 中性灰阶**；强调只给主操作、当前选择、链接、焦点环。
2. **分隔先靠间距（`Space3/4`），其次靠 1px hairline；不为每个分区加边框**（Carbon 的"空间可以分区而不需要线"）。
3. **每边框只画一次**（Geist）：不叠两层 alpha 边，不"控件套控件"。
4. **边框三态**：弱（内部细分隔）/ 中（结构）/ 强（可交互边界、输入框、卡片）。
5. **行密度 28/32/36，控件 32/36/40，双行 44**；全部 4 的倍数；不出现 38。
6. **字号阶梯 11–13 元数据 / 14 正文 / 16 小节 / 20 页标题 / 24+ 仅 hero**；标题 600、控件 500、正文 400；14px 及以下不用 600。
7. **容器圆角 6–12、大浮层 16**；不用 ≥20 的"消费级胶囊"（状态药丸除外）。
8. **状态至少两通道**（颜色 + 文字，最好加图标/形状）；每屏状态指示器 ≤6；`cancelled` 中性。
9. **选择态 = 低 alpha tint（+ 左条）**；不用满饱和填充做持久选中。
10. **审批必须披露"范围 + 影响 + 可持久化级别"**；默认安全（Esc 取消/拒绝），Enter 不误触。
11. **工具调用默认折叠、失败默认展开、长输出进侧栏/终端**；折叠行用"结果摘要"而不是"记录列表"。
12. **键盘可发现**：主/次动作固定绑定（`↵` / `⌘↵`）、kbd 微标签、命令面板为全量入口、tooltip 带键位。

### 3.5 外壳与导航（宽度 / 分组 / 选中）的横向结论

| 议题 | 业界 | 我们 | 结论 |
|---|---|---|---|
| 侧栏宽度 | Linear 的全局 chrome 是"inverted L"，强调 alignment 与密度（[II](https://linear.app/now/how-we-redesigned-the-linear-ui)）；VS Code 侧栏可拖拽、可收起 | 280 / rail 64（`components.go:51-55`） | **保留 280**（中文会话标题需要宽度）；P2 可评估 264；rail 64 保留（图标 16 + `Space3` 内距） |
| 导航项数量 | "一个 View Container 通常就够；3–5 个 View 是上限"（[VS Code Sidebars](https://code.visualstudio.com/api/ux-guidelines/sidebars.md)） | 3 个模块屏 + 新对话 + 工作区树 + footer 设置 | 合规；**新增目的地优先做成命令面板命令/抽屉，禁止往侧栏加第 4 个模块项** |
| 分组方式 | VS Code 用 `SidebarSection` + 折叠；Inbox 用"注意力类别"（Inbox/Working/Ready/On hold/Done）而非项目分组（[Inbox](https://code.visualstudio.com/docs/agents/run/sessions/organize-sessions.md)） | 导航组 + 工作区（文件夹 icon → hover 换 chevron）+ 未分组 | 方向正确；补两条：① **分组只在 ≥3 个工作区时出现**（否则平铺，减少噪音）；② 需要"需要你处理"视图时用我们的 `待审批/等待回复/未读` 三个真实信号，不新增持久分类 |
| 选中语言 | 单一灰阶 tint（Linear/VS Code），不用实色 | 导航行走官方 `SidebarItem` → **聚焦态整块 Accent 实色**；会话行 tint + 2px 左条 | **统一为 tint**（§2 F-07 方案 C：官方局部主题）；2px 左条只给自绘富行 |
| 顶带 / Tabs | 顶带 40 + tab 行 32 是桌面惯例（VS Code 标题区 + 编辑器 tab） | 40 + 32（已实现） | 保留；顶带只放 1 个标题 + 右侧动作，不放面包屑 + 标题 + 状态三行 |

---

## 4. 设计语言候选评审

### 4.1 七个候选（定义"语言"，不是"产品"）

| # | 候选 | 一句话定义 | 证据 |
|---|---|---|---|
| C1 | **Linear 精确中性**（Precision Neutral） | 中性灰阶 + 单一强调蓝；层级靠字号/字重/间距；hairline 只做结构；密度优先；对齐比装饰重要 | `[B/C]` Linear 重设计 I/II |
| C2 | **Raycast 命令优先**（Command-First） | 一屏一个搜索框 + 一张列表 + 一个动作面板；主/次动作固定键位；行 accessories 承载元信息 | `[B]` Raycast List/ActionPanel |
| C3 | **Geist 极简高对比**（Minimal High-Contrast） | 黑白极限对比 + 4 级 material 抬升 + 6/12/16 圆角；边框三态；阴影半透明 | `[A]` Geist Typ./Colors/Materials |
| C4 | **Claude 温润纸质**（Warm Paper） | 衬线/暖灰/大留白/低对比 UI 褪到背景，内容像纸 | `[D]` 无公开 token（只作为方向评测） |
| C5 | **Zed 原生编辑**（Native Editor） | 编辑器原生密度 + 多缓冲审阅 + 线程/面板模型；8ms 帧预算 | `[B]` Zed Agent Panel/UI checklist |
| C6 | **GitHub Primer 企业清晰**（Enterprise Clarity） | 状态/流程语义组件化（StateLabel/InlineMessage/Blankslate/KeybindingHint/ConfirmationDialog）+ 明确的 UI/Scenario 模式分层 | `[A/B]` Primer Product UI |
| C7 | **Carbon 企业密集**（Enterprise Dense） | 14px 产品基准 + 2px 微间距也能用 + 状态四元素 + AI label/可解释性 | `[A]` Carbon |

### 4.2 评分矩阵（1–5，越高越好；权重按"离产品目标多近"）

权重说明：**与 Go 原生控件可实现性 25%**（我们的宿主是 mygo 原生控件，不是 CSS，实现不了的语言就是坏语言）；**信息层级 20%**；**企业感 15%**；**可读性 15%**；**品牌蓝契合 10%**；**维护成本 10%**（分数高=成本低）；**视觉密度 5%**（密度本身不是目标，能适配即可）。

| 维度（权重） | C1 Linear | C2 Raycast | C3 Geist | C4 Claude | C5 Zed | C6 Primer | C7 Carbon |
|---|---|---|---|---|---|---|---|
| 视觉密度（5%） | 4 | 5 | 4 | 3 | 5 | 4 | 5 |
| 信息层级（20%） | **5** | 4 | 4 | 3 | 4 | **5** | 4 |
| 企业感（15%） | **5** | 3 | 4 | 2 | 4 | **5** | **5** |
| 可读性（15%） | 4 | 3 | 4 | 4 | 4 | 4 | 3 |
| Go 原生可实现性（25%） | **5** | 3 | 2 | 3 | 3 | **5** | 3 |
| 与品牌蓝契合（10%） | 4 | 3 | 3 | 2 | 3 | 4 | 2 |
| 维护成本（10%） | 4 | 4 | 3 | 4 | 3 | 3 | 2 |
| **加权总分** | **4.60** | 3.40 | 3.30 | 3.00 | 3.60 | **4.50** | 3.40 |

复算：C1 = .20+1.00+.75+.60+1.25+.40+.40 = **4.60**；C6 = .20+1.00+.75+.60+1.25+.40+.30 = **4.50**；C5 = .25+.80+.60+.60+.75+.30+.30 = 3.60；C2 = .25+.80+.45+.45+.75+.30+.40 = 3.40；C7 = .25+.80+.75+.45+.75+.20+.20 = 3.40；C3 = .20+.80+.60+.60+.50+.30+.30 = 3.30；C4 = .15+.60+.30+.60+.75+.20+.40 = 3.00。

### 4.3 最终推荐：**主语言 C1（Linear 精确中性）+ 辅助语言 C6（Primer 企业清晰）**

**分工（不是二选一，而是分层）**

| 层 | 用哪套 | 具体表现 |
|---|---|---|
| 视觉与结构（壳层、侧栏、transcript、卡片、间距、圆角、边线、表面 ramp） | **C1 精确中性** | 中性灰阶 + 单一皇家蓝；层级只靠字号/字重/间距；hairline 只做结构；卡片无阴影；密度按 32/36；对齐以 4px 网格为唯一真相 |
| 语义与流程（状态、审批、计划、空态、错误、键位、危险确认） | **C6 企业清晰** | `StateLabel`→`StatusBadge`；`InlineMessage` vs `Banner` 分档；`Blankslate`→`EmptyState`；`KeybindingHint`→`uikit.Kbd`；`ConfirmationDialog`→`ConfirmDanger`（我们已有）；场景模式（Copy/Delete/Filter/Search）作为评审清单 |

**为什么是这两套（正面理由）**

1. 它们都**不依赖 CSS 能力**：中性灰阶 + hairline + 单一 accent + 4 的倍数，全部能用 mygo 的 `Background/Border/Radius/Padding/Height` 表达；不会需要 blur、渐变、subpixel 描边、容器查询这类原生控件做不到的东西。
2. 它们都**默认"企业"**：状态与流程是被组件化的，不是靠设计师生造（Primer 的 patterns 分类、Linear 的"减少噪音提高密度"）。
3. 它们**能吃掉品牌蓝而不是被品牌蓝吃掉**：中性语言里唯一彩色就是 accent，皇家蓝 #0D44F6（6.6:1 with white）正好当"主操作"；高频品牌蓝不会变成装饰。
4. 它们**与现有 token 体系同构**：我们已有 S0–S4、BgHover/BgSelected、BorderL2/L3、StatusBadge —— 这套语言基本是"把已有 token 收紧并补齐三处缺口（BorderL1、ProseMeasure、FocusRing offset）"，不需要推倒。

**为什么其他候选不合适（逐条）**

- **C3 Geist 极简高对比**：它的力量来自"黑白极限对比 + 4 级 material 抬升"，在 web 上用大面积黑/白高对比很好看；但在**中文 14px 密集正文 + Go 原生抗锯齿**下，纯灰阶会把品牌蓝逼成"唯一彩色"，反而让蓝色变成装饰色；同时它的圆角/边框三态需要"alpha 与实色混用"的精细控制，mygo 只能给 `Border(1, color)`，做不出"gray-alpha 只在 shadow/overlay 使用"的区分。可作为**局部借用**（Code Block 的头部、边框三态思想），不作为主语言。
- **C2 Raycast 命令优先**：它是"单任务、单手键盘、无持久多面板"的模型；我们的产品是"多会话 + 多面板 + 长 transcript + 右栏检查器"，列表 + ActionPanel 的结构装不下 transcript 的时间线语义。借用其**主/次动作绑定与 kbd 可见性**即可。
- **C4 Claude 温润纸质**：与用户明确要求的"企业级工具的视觉审美"方向相反（低对比、暖色、大留白适合消费级对话产品）；更关键的是**没有公开 token 证据**（D 级），照抄就是猜。排除。
- **C5 Zed 原生编辑**：Zed 的密度与"逐 hunk 审阅"很对我们胃口，但它的视觉语言与 GPUI 编辑器语境绑定（等宽优先、面板即编辑器），而且它的强项是"编辑器内"的交互；我们已有编辑器面（`ui/editor`），把 Zed 的**审阅/线程/键位**思想借过来即可，视觉不采用。
- **C7 Carbon 企业密集**：企业感满分，但 ① 它用 IBM Plex，与我们"系统字体栈 + 不引第三方字体"的品牌决定冲突；② 组件体量巨大（几十个组件 + 4 主题），我们的 13 个 UI 模块无力维护第二套；③ 2px 微间距在 4px 网格上不可达（我们已有 `Space1=4` 的下限）。只借用**状态四元素/≤6 指示器/AI label 三层可解释性**这三条原则。

---

## 5. Token 级落地建议（可直接改 `internal/design` / `internal/brand`）

> 标记：**[保留]** 不改；**[收紧]** 改用法不改数值；**[改值]** 改数值（需 brand/design 同步 + 快照复审）；**[新增]** 新 token。
> 所有数值都在 4 DIP 网格上；所有颜色仍只能来自 `internal/brand`。

### 5.1 字体阶梯（字号 / 行高 / 字重）

**原则（业界共识）**：标题 600、控件/行标题 500、正文 400；14px 及以下不用 600（Microsoft 的 ramp 只有 semibold 给标题，且明确"用 Semibold 代替 Bold"）；元数据 12，微标签 11 只做 kbd/gutter（我们已有）。

| Role | 现值 | 建议值 | 变化 | 用途（收紧后） | 依据 |
|---|---|---|---|---|---|
| `TypeXL` | 24/32/400 | **24/32/600** | 改值（字重） | 仅欢迎页/landing hero，一屏最多 1 处 | Microsoft title 28/36 semibold；Geist Heading 24 |
| `TypeL` | 20/28/500 | **20/28/600** | 改值 | 设置页/对话框 H1（每屏 1 个） | 同上（subtitle 20/28 semibold） |
| `TypeM` | 16/24/400 | **16/24/600** | 改值 | 分节标题、markdown H2、卡片头 | Geist Heading 16/20；Windows subtitle |
| `TypeBody` | 14/22/400 | 14/22/400 | 保留 | 正文、markdown、问题正文 | Carbon body-01 14/20（我们更长=长文档） |
| `TypeS` | 14/24/400 | **14/22/500** | 改值（字重/行高） | 列表行标题、菜单项、按钮、工具名 | Windows body strong 14/20 semibold→我们用 500 更轻 |
| `TypeXS` | 13/20/400 | 13/20/400 | 保留 | 次要正文、表单标签、检查器正文 | Geist Label 13 |
| `TypeXXS` | 12/18/400 | 12/18/400 | 保留 | 元数据、状态词、badge、kbd 说明 | Geist Label 12；Microsoft 最小 12 |
| `TypeXXXS` | 11/14/400 | **11/14/500** | 改值（字重） | **仅** kbd、gutter、分组微标签 | Windows 最小 14/12；11 必须加强字重才可读 |

**同时做的三件事**

1. **[收紧] 字重烘焙进 role**：`TypeL/M/XL=600`、`TypeS=500`、`XXXS=500`，call site 不再传 `FontWeight(600)` —— 可一次消掉 `docs/DESIGN.md` §3.2 记录的 **32 处字面量 warn + 间接字重 warn**（F-14）。
2. **[新增] `TypeBodyReading`（仅 markdown）= 14/23/400**？**不加**。理由：一屏 ≤4 种字号的纪律更重要；改用 `Theme.LineHeight=1.6`（22.4→22 视觉上等价）+ 段间距 `BlockSpacing 0.72→0.80`（11.2 DIP ≈ `Space3`），中文阅读感提升而不新增档位。
3. **[新增] mono 三档明确化**（现值 `CodeSize=0.9em` 隐式换算，不可审计）：
   - 行内 code：mono **12/18**（`t.MonoText(TypeXXS)`）
   - 围栏代码块：mono **13/20**（Geist "Label 13 Mono pairs with Label 14"）
   - 终端/日志 pre：mono **12/18**（`TypeXXS`），行高固定不随字号缩放
   - 数字（tokens/耗时/百分比）：**优先 tabular**；若 mygo 无 tabular 开关，则用 mono 12（Geist 的 `Label 13 tabular` 用于数字对齐）

**行宽（阅读度量）—— 值得单独决策**

- 中文：14px 下 40–50 字/行是舒适区（中文书籍 28–40 字/行，界面可略宽）。
- 拉丁：Microsoft 给的是 **50–60 个字母/行**；排版学常用 66ch，任务书提到的 68–78ch 属于偏宽的长文窗口。
- 换算是：中文 N 字 = N × 14 DIP；拉丁 N 字符 ≈ N × 6.9 DIP（14px Inter 系）。
  → 中文 40–50 字 = **560–700 DIP**；拉丁 68–78ch = **470–540 DIP**；两者交集 ≈ **560–640**。
- **建议**：**[新增] `ProseMeasure = 640`**（正文段落/列表/引用最大宽，居中于 960 列内），**[保留] `TranscriptWidth = 960`** 给代码块/表格/工具卡/审批卡。
  收益：中文 ≈45 字/行、拉丁 ≈92 字符/行（略宽但可接受），且**代码块仍然能用满 960**——这正是 VS Code（Chat 面板代码用满、正文有自己节奏）与 Claude Desktop（diff/浏览器占宽、文字收窄）的混合做法。
- 附带规则：`ProseMeasure` 只作用于 markdown 的"文本块"（段落、列表、引用），**不作用于表格与代码块**，否则表格会挤成两列。

### 5.2 间距刻度

- **[保留] 数值**：`0/4/8/12/16/20/24/32`（`Space0..6,8`），不引入 2px（Carbon 的 `spacing-01=2` 在 4px 网格体系里不可达，且原生控件会向上取整）。
- **[新增] 语义别名**（只是别名，不新增数值；目的是让评审可检查）：

| 别名 | = | 用途 |
|---|---|---|
| `SpaceInline` | 4 (`Space1`) | 图标与文字、kbd 与文案、行内元素 |
| `SpaceRow` | 8 (`Space2`) | 行内左内距、行与行之间、chip 之间 |
| `SpaceGroup` | 12 (`Space3`) | 同一卡片内的段与段 |
| `SpaceSection` | 16 (`Space4`) | 卡片与卡片、面板小节 |
| `SpaceScreen` | 24 (`Space6`) | 页面区块、设置页分节 |
| `SpacePage` | 32 (`Space8`) | 页面顶部/hero 留白 |

- **[收紧] 三条纪律**：① 相邻元素的间距必须是"下一档"（8 之后用 12，不许 10）；② 同一层级内只用一档；③ 组内用 `SpaceGroup`，组间用 `SpaceSection` —— **不要用边框分组**（Carbon：空间可以分区而不需要线）。

### 5.3 圆角

| token | 现值 | 建议值 | 语义（收紧后） |
|---|---|---|---|
| `RadiusXS` | 4 | 4 | 内嵌小方块：kbd、checkbox、行内 code、状态点容器 |
| `RadiusSM` | 8 | 8 | chip、行 hover 高亮、菜单项、代码块、tab |
| `RadiusMD` | 10 | 10 | 原生控件（按钮/输入/select）——由 `brand.Radius` 决定，不改 |
| `RadiusLG` | 16 | **12** | **卡片/工具卡/审批卡/计划卡/对话框/toast/浮层** |
| `RadiusXL` | 20 | **16** | **仅 composer**（+ 全屏类抽屉） |
| `RadiusMax` | 999 | 999 | 状态药丸、头像 |

- 依据：Geist 的 `medium/large/menu/modal = 12`、`fullscreen = 16`、`base/small = 6`（[Materials](https://vercel.com/geist/materials)）；Carbon/GNOME 的桌面容器更小。12 是"企业感"的分水岭，16/20 会读作消费级。
- **[收紧] 规则**：同屏 ≤3 档（已有）；子圆角 ≤ 父圆角（已有）；**禁止 chip 用 LG/XL**（已有）；新增：**代码块用 SM、卡片用 LG，二者不同屏嵌套**（避免"圆角套圆角"）。

### 5.4 边框策略（哪些 1px、哪些 2px、alpha 多少）

| token | 现值 | 建议值 | 用在哪 |
|---|---|---|---|
| `BorderL1` **[新增]** | — | 亮 `ink 6%` / 暗 `白 8%` | **内部细分隔**：卡片内头/体分界、表头下边、列表行分隔（仅当行距不足时） |
| `BorderL2` | 亮 10% / 暗 12% | 保留 | **结构线**：侧栏右边、顶带下边、tab 下边、引用块、表框 |
| `BorderL3` | 亮 16% / 暗 18% | 保留 | **可交互边界**：输入框/palette/菜单/卡片/composer 的静息边 |
| （新增语义） | — | `BorderL3` 的 hover 态用 `BorderL3` + `BgHover`，**不新增 BorderL4** | 已够用；避免 4 档边线 |

- **1px vs 2px**：**只有三种东西可以 2px**——① 选中态左条（`Accent`，已有）；② 焦点环（见 §5.7）；③ 审批卡的语义左条（`Warning`/`Danger`，替代满宽带）。其余一律 1px。
- **alpha vs 实色**：沿用我们的 alpha 方案（亮 ink / 暗白），因为它天然适配 S0–S4 任意底；**但必须遵守 Geist 的"每边框只画一次"**：两个相邻容器只能有一个画边（例：composer 卡已有 1px 边，其内部工具条**不再画上边**；tab 行已有下边，内容区**不再画上边**）。
- **不许边框的地方**：transcript 消息（无气泡无边框）、侧栏行（hover 才出底）、工具行（展开后才有内部 hairline）、设置页分节（用间距 + `SectionLabel`，不用卡片）。

### 5.5 表面 ramp（S0–S4，亮/暗建议值）

| 语义 | token | 亮色（现值→建议） | 暗色（现值→建议） | 说明 |
|---|---|---|---|---|
| S0 page | `BgBase` | `#FFFFFF`（保留） | `#111118`（保留） | 主内容、transcript、设置 body |
| S1 sunken | `BgSidebar` | `#F9FAFB`（保留） | `#0B0B10`（保留） | 侧栏、顶带、设置导航；暗色必须比页面深（已有规则） |
| S2 raised | `BgRaised` | `#FFFFFF`（保留） | `#1B1B26`（保留） | 卡片、composer、工具卡、审批卡、对话框 |
| S2.5 内部 | `BgStrong` | `#F1F3F5`（保留） | `#272736`（保留） | 代码块底、行内 code、填充式中性控件 |
| S3 只描边 | `BgBase`+`BorderL2` | 保留 | 保留 | 团队/任务/文件条目（**不加阴影**） |
| S4 overlay | `BgRaised`+`BorderL2`+`RadiusLG` | `#FFFFFF`（保留） | **`#22222E`（建议 +1 阶）** | modal/palette/菜单/toast；暗色下与 S2 只差一阶会"贴住"卡片 |

- **[建议] 暗色 S4 提一阶**（`#1B1B26 → #22222E`）：Geist 提醒"暗色阴影对比更弱，要确认分离仍然可读"（[Materials](https://vercel.com/geist/materials)）。若 brand 不接受新增 hex，则退让为：**浮层一律 `BorderL3` + `ShadowOverlay`**，靠边框分离。
- **[收紧] 亮色不引入 1% 灰底**：Geist 用 `Background 2` 做轻微区分，我们已有 `BgSidebar` 承担；页面保持纯白，卡片靠 `BorderL3` 读作"抬升"（与现有实现一致，避免大面积改色）。
- **[收紧] 卡片无阴影**（已有）；浮层才有 elevation（已有）；**同屏最多一层浮层**（已有 S2 内最多嵌一层 S3）。

### 5.6 强调色配额

| 规则 | 现值 | 建议 |
|---|---|---|
| 每屏 accent 填充块 | ≤2 | **≤2（保留）**，但追加定义：**"填充块"指面积 ≥ 24×24 的实色 accent**；2px 左条、1px 边框、图标、文字链接不计入 |
| 每屏 primary 按钮 | ≤1 | **≤1（保留）**；审批 pending 时 composer 发送降级（F-02） |
| 每屏红色元素 | ≤2 | 保留 |
| 新增 | — | **同一行/同一卡片内 accent ≤1 处**（避免"图标蓝 + 文字蓝 + 边框蓝"三连） |
| 新增 | — | **accent 不做图标底色/不做装饰性大色块/不做渐变**（Carbon：AI 光效不是装饰） |
| 新增 | — | **链接色 = `Accent`（暗色 `#3CBBFA` 8.7:1 合规）**，但**正文内链接**下划线只在 hover 出现，静息只用颜色差（Geist/Linear 惯例） |

### 5.7 焦点环

- **[新增] token**：`FocusRingWidth = 2`、`FocusRingOffset = 2`、`FocusRingRadius = 元素圆角 + 2`。
- 颜色：亮色 `Focus`（现 80% royal，4.24–4.59:1）+ **外圈 1px `BgBase` 空隙**（offset 2 已提供视觉空隙）；暗色 `Focus`（70% cyan）。
- 形态：**2px 环，画在元素外侧（offset 2），不覆盖内容**；按钮类元素环圆角 = 控件圆角 + 2；行类元素（列表行/树行）用**内嵌 2px 环**（否则会被 Scroll 裁切）。
- 触发：**只在键盘/程序聚焦时画**；鼠标点击不画（`focus-visible` 语义）。若 mygo 无法区分来源，则鼠标态用 1px `BorderL3` 替代，键盘态用 2px `Focus`。
- 对比要求：环与"环所在表面"≥3:1（现有 4.24 已满足）；在 accent 填充元素上，环外再加 1px `OnPrimary` 内衬，避免"蓝环贴蓝底"。

### 5.8 状态色使用

| 状态 | 文字 | 图形 | 词 | 变化 |
|---|---|---|---|---|
| running | `LabelSecondary`（**建议**）/ 现 `WarningText` | `ui.Spinner` + `Accent` | 运行中 / 正在读取文件… | **F-06 语义调整** |
| needs action | `WarningText` | `Warning` 点/图标 | 等待你的操作 / 需要审批 | 保留 |
| success | `SuccessText` | `Success` | 已完成 | 保留 |
| error | `DangerText` | `Danger` | 失败 / 已拒绝 | 保留 |
| cancelled | `LabelTertiary` | `LabelTertiary` | 已停止 / 已取消 | 保留（中性） |
| unknown | `LabelTertiary` | `LabelTertiary` | — | 保留 |

- **[收紧] 两通道原则**：任何状态都必须同时有"颜色 + 文字"；状态点只做**辅助**（与 Carbon 的"≥3 元素"对应，我们降到 2 是刻意的：桌面原生控件没有形状语言）。
- **[收紧] 每屏状态指示器 ≤6**（Carbon 的经验值）；超出时合并为"最高注意力"一条（Carbon："consolidated statuses 用最高注意力色"）。
- **[收紧] 状态色禁止用于**：正文、图标装饰、图表默认色、hover 底。

### 5.9 动效（时长 / 缓动 / 帧预算）

> 本节为**工程判断值**（Carbon Motion 页在本环境抓取失败；Zed 提供了帧预算这一硬证据）。落地时以 `design` 里的常量集中定义，便于统一调整。

| 场景 | 时长 | 缓动 | 备注 |
|---|---|---|---|
| hover / focus / 颜色过渡 | **80ms** | `ease-out` | 短到"几乎即时"，避免按钮"发黏" |
| 展开/折叠（工具行、推理块） | **120ms** | `cubic-bezier(0.2, 0, 0, 1)` | 高度动画必须配 `HoldPlace`（已有）避免列表跳动 |
| 浮层/菜单/对话框进入 | **160ms** | `cubic-bezier(0.2, 0, 0, 1)` | 位移 ≤8 DIP + 透明度 0→1；**不做缩放**（原生控件里缩放会糊） |
| 浮层退出 | **120ms** | `cubic-bezier(0.4, 0, 1, 1)` | 退出永远比进入快 |
| toast 进入/退出 | 120 / 80 | 同上 | 不自动消失的错误类不做退出动画 |
| 主题切换 | **0ms** | — | 桌面应用切换主题直接跳变（Linear/VS Code 同款），避免"渐变蒙层" |
| **禁止** | >240ms 的 chrome 动画、流式文本逐字淡入、布局属性动画（宽/高连续变化） | — | Zed checklist：**每帧 ≤8ms（120fps）**；流式期间任何动画都会抢占帧预算 |

- **reduced motion**：读 `Preferences`，把上述时长全部置 0（不是"减半"），并把展开改为瞬时。
- **流式**：只允许 Spinner（旋转）与进度条（真实进度）；**正文不做任何位移/淡入**（我们的 `markdown.Reveal` 已是 `AtOnce, Fade:-1`，保持）。

### 5.10 度量与密度（小结表）

| 项 | 值 | 说明 |
|---|---|---|
| 侧栏展开/收起 | 280 / 64 | 保留；**P2 可评估 264**（Linear 更窄，但中文标题需要宽度，280 可接受） |
| 检查器 | 320（min 260） | 保留；切换键建议 `F9`（GNOME 惯例） |
| 阅读列 | 960 | 保留（卡片/代码/表格） |
| 正文列 | **640（新增 `ProseMeasure`）** | 段落/列表/引用 |
| 行高 | 28（chip）/32（树、菜单、导航、会话行）/36（列表行、控件）/40（页面主操作）/44（双行） | 保留；**导航与会话行同解剖、同左缘**（F-07） |
| 顶带 / Tabs | 40 / 32 | 保留 |
| 状态条 | 32 | 建议明确为 32 高（当前为一行 12px 文本，高度未定） |
| 卡片内距 | `Space4`（16） | 工具卡/审批卡；行内字段 `Space3`（12） |
| 图标 | 16 / 20 / 24 | 保留；**正文内联图标 = 16 且与大写字母高度对齐** |

### 5.11 变更清单（给 Lead 的实施顺序）

| 优先级 | 变更 | 位置 | 风险 |
|---|---|---|---|
| 1 | `RadiusLG 16→12`、`RadiusXL 20→16` | `internal/design/components.go:111-112` | 快照全量复审；圆角变小后卡片边距可能显空 → 同步 `Panel()` 内距 |
| 2 | 字重烘焙进 role（XL/L/M=600、S=500、XXXS=500；S 行高 24→22） | `internal/design/tokens.go:37-56` | 全局字重变化 → 逐屏看快照；可先只改 M/S 两档 |
| 3 | 审批卡去满宽带，改 2px 语义左条 + StatusBadge | `internal/uikit/approval.go:198-230` | 需要同步 `docs/AGENT-SURFACES.md` S4 布局段 |
| 4 | `BorderL1` 新增 + "每边框只画一次"评审 | `internal/brand/palette.go`、`design.Tokens` | 新增 token 要走 `DESIGN.md` §5.1 表格 |
| 5 | `ProseMeasure=640` + `LineHeight 1.55→1.6` + `BlockSpacing 0.72→0.80` | `internal/design/components.go`、`internal/uikit/markdown.go:32,40` | 帧测试里 `LineHeight==1.55` 的断言要改（`uikit/markdown_code_test.go:79`、`session/fontsize_internal_test.go:59`） |
| 6 | 代码块头部（语言 + 复制）+ mono 13/20 | `internal/uikit/markdown.go`、`MarkdownCodeBlock` | 需要新快照 + 复制成功态 |
| 7 | `uikit.Kbd` + 主/次动作 `↵`/`⌘↵` 绑定 | `internal/uikit` | 与 Esc 优先级、焦点陷阱要一起测 |
| 8 | 焦点环 offset 2（`FocusRingOffset`） | 官方控件无法逐个改 → **只能对自绘元素与富行生效**；官方件用主题 `Focus` 色 | 冲突登记：官方件环形态不可控，见 §7 R-3 |
| 9 | 状态语义：running 去 warning | `docs/DESIGN.md` §5.2、`uikit.StatusText` | **契约变更**，需 Lead 裁决（F-06） |
| 10 | 暗色 S4 提一阶 | `internal/brand/palette.go` | 需过 `palette_contrast_test.go` |

---

## 6. 业界最佳实践 × ZeoKeel 12 个关键表面

> 每条格式：**业界做法（证据）** → **我们应采用的做法** → 优先级。机读版见同目录 `A2-best-practices.tsv`。

### 6.1 会话线程（SessionThread / S1）

- **业界**：单一纵向流，消息不套卡片；VS Code 把工具调用默认折叠、可展开看调用与输出（[Tools](https://code.visualstudio.com/docs/agents/run/tools.md)）；Zed 的消息是**可点开编辑再提交**的（[Agent Panel](https://zed.dev/docs/ai/agent-panel)）；Claude Code 的 transcript 把重复 MCP 调用压成 `Called slack 3 times`（[Interactive mode](https://code.claude.com/docs/en/interactive-mode.md)）。
- **我们**：`ui.List` + `FollowEnd` + `HoldPlace`（已实现，`uikit.SessionThread`）。补三件事：① 回合之间用 `Space6`（24）而不是 `Space5`；② 折叠摘要行采用"结果优先"措辞（`已运行 6 条命令 · 2 个文件被修改` 而非记录列表）；③ 末条回复底部常显"复制"，其余动作 hover 显形（F-11）。
- **优先级**：P1（②③）／P2（①）。

### 6.2 消息头 / 元信息

- **业界**：Claude Code 在 transcript 里给每条 assistant 消息显示**时间戳 + 所用模型**（[Interactive mode](https://code.claude.com/docs/en/interactive-mode.md)）；Zed 把点赞/反馈放在回答末尾（[Agent Panel](https://zed.dev/docs/ai/agent-panel)）；ChatGPT 类的复制/重生成是 hover 出现（`[D]`，仅作观察）。
- **我们**：元信息行固定在助手消息**顶部**（时间 · 用时 · 紧凑 tokens · `⋯`）。建议：① 增加**模型名**（12px，`LabelTertiary`，长度截断到 12 字符）；② `⋯` 菜单增加 `从此处回滚`（C3 checkpoint 思想）、`复制为 Markdown`（Zed）；③ 行高固定 24，hover 显形不改变布局；④ 用户消息不放元信息（除非被编辑过，则显示"已编辑"）。
- **优先级**：P1。

### 6.3 Markdown 正文排版

- **业界**：Microsoft 每行 50–60 字母、最小 14/12、句子式大小写（[Windows Typography](https://learn.microsoft.com/en-us/windows/apps/design/signature-experiences/typography)）；Carbon 长文 `body-01 14/20`、短段 `body-compact-01 14/18`、**"body-long 始终左对齐"**（[Type sets](https://www.carbondesignsystem.com/building-blocks/foundations/typography/type-sets)）；Geist `Copy 13` 用于"空间吃紧的次要文本"（[Typography](https://vercel.com/geist/typography)）。
- **我们**：`ChatMarkdown` 现为 14 / 1.55 / 块间距 0.72em / H1 17–18、H2≤16、H3≤15 全 600（`internal/uikit/markdown.go:30-90`）。建议：① 行高 1.6、块间距 0.80（`Space3`）；② **段落 max-width 640**（§5.1），表格/代码不受限；③ 标题权重保持 600（≥15px 合规），但 H3 降为 500；④ 引用块 3px `BorderL2`（已有）+ 文字 `LabelSecondary`（已有）；⑤ 列表项间距 4（`ItemSpacing 0.28em` 保留），**嵌套列表缩进 1.5em 保留**；⑥ 段落**左对齐**（不两端对齐，中文两端对齐会产生不规则字距）。
- **优先级**：P1。

### 6.4 代码块

- **业界**：Geist 有独立 Code Block 组件与 `Label 13 Mono`（[Materials](https://vercel.com/geist/materials) 的组件索引）；主流聊天客户端的围栏普遍带"语言 + 复制"头部（**观察，D 级**）；Zed 在回复里不显行号（**观察，D 级**）。
- **我们**：现为"填充面 + 透明边框 + 8 圆角 + 0.8em 内距 + 语言角标"（`markdown.go:49-51`）。建议：① **28 高头部**：左 mono 12 语言名（缺省 `text`），右复制按钮（16 图标，复制后 1.2s Check）；② 头部 `BgStrong`、块体 `BgBase`（或反向），中间 1px `BorderL1`；③ 块体 mono **13/20**、内距 `Space3`；④ 外框 1px `BorderL2` + `RadiusSM(8)`；⑤ **超过 24 行折叠**为"显示全部（N 行）"；⑥ 长行**默认横向滚动**、不自动换行（换行会破坏对齐与复制）；⑦ 语言标签用小写规范名（`bash`/`go`/`json`）。
- **优先级**：P1（①②③⑥），P2（⑤）。

### 6.5 工具调用行

- **业界**：VS Code 默认折叠、`Show Output`/`Show Terminal`/`Continue in Background` 三件套（[Tools](https://code.visualstudio.com/docs/agents/run/tools.md)）；Claude Code 折叠成一句话并保留时间/模型（[Interactive mode](https://code.claude.com/docs/en/interactive-mode.md)）；Cursor 在思考/工具过程中允许提问并继续工作（[Agent overview](https://cursor.com/docs/agent/overview)）。
- **我们**：`uikit.ToolRow`（32 行：图标 + 名 + 状态 + 96 rune 预览 + chevron）+ `ToolCallCard`（决策类）。建议：① 状态词右对齐到行尾（与图标列形成左右两端），中间预览 `LabelTertiary` mono 12；② 同类连续调用**合并 `×N`**（Claude Code 的 `Called slack 3 times`）；③ 展开区增加 `在检查器中打开`（完整输出）+ `复制输出`；④ 长结果（>8 行）只显示前 8 行 + "展开全部"；⑤ 失败默认展开（已有）+ 首行给**可行动的下一步**（"检查网络代理"/"重试"）。
- **优先级**：P1（①②④⑤），P2（③ 的检查器跳转已有雏形）。

### 6.6 推理 / 思考块

- **业界**：Claude Code 折叠默认、`Ctrl+O` 展开（[Interactive mode](https://code.claude.com/docs/en/interactive-mode.md)）；VS Code 有 `thinkingStyle: collapsed` 与"工具与思考分组"（[Tools](https://code.visualstudio.com/docs/agents/run/tools.md)）；Carbon 明确**"不要把 AI 样式当装饰"**（[Carbon for AI](https://www.carbondesignsystem.com/building-blocks/foundations/carbon-for-ai)）。
- **我们**：`ReasoningTrace` + 分段交错时间线 + 单行摘要（`过程 · 思考 · N 工具 · 用时`）。建议：① 单行摘要**必须带用时**（决定"值不值得展开"）；② **不用紫色/光效**，只用 `Sparkles` 16 + `LabelTertiary`；③ 展开后保留分段结构（已有 US-01），每段之间 `Space2`；④ 推理正文用 `TypeXS 13/20`（比正文小一档，明确"不是答案"）；⑤ 流式时摘要行原地更新，**不追加新行**（几何稳定）。
- **优先级**：P1（①②④）。

### 6.7 审批对话框（S4）

- **业界**：VS Code `Allow Once` 默认 + 范围下拉 + session/always（[Approvals](https://code.visualstudio.com/docs/agents/run/approvals.md)）；Claude 编号选项 + 范围写进文案 + `Esc to cancel` / `Tab to amend` + 可加评论（[Permissions](https://code.claude.com/docs/en/permissions.md)）；Claude Desktop 的站点审批 `Allow once / Always allow / Deny`（[Desktop](https://code.claude.com/docs/en/desktop.md)）。
- **我们**：内联卡 + 22% 警示带 + 三键（`拒绝 / 本会话内始终允许 / 允许`）+ 无默认焦点 + Esc=拒绝。建议（F-01/F-03）：① 去满宽带 → 2px 左条 + `StatusBadge("需要审批")`；② 卡体四段固定：**做什么**（工具友好名 + 一句话影响）→ **具体内容**（命令/路径 mono 块，带 `命令`/`路径`/`域名` 标签）→ **范围**（`仅此一次 / 本会话始终 / 总是（写入设置）` 三选，旁边小字提示将写入的规则）→ **动作**（`拒绝` · `允许`(primary, 文案随范围变化) · `⋯`）；③ footer 右侧给 kbd：`Esc 拒绝`、`↵ 允许`（**仅当焦点进入动作区时**）；④ 队列计数保留（"还有 N 条"）并支持在卡内切换查看；⑤ 跨会话来源行保留（已有）。
- **优先级**：**P0**。

### 6.8 计划审阅（S5）

- **业界**：VS Code Review Plan 卡：`Open Full Plan`（Markdown 编辑器打开、可编辑、行内评论）→ `Submit Feedback` → `Implement Plan / Approve Plan Only / Implement with Autopilot / Reject`；**反馈未提交时禁用批准**；复核清单 Scope/Project fit/Decisions/Verification（[Plan work](https://code.visualstudio.com/docs/agents/run/planning.md)）。Cursor：计划默认不放仓库，`Save to workspace` 才进 Git（[Plan Mode](https://cursor.com/docs/agent/plan-mode)）。
- **我们**：`PlanReviewDialog` + 决策类工具卡。建议：① 卡内渲染**结构化步骤列表**（每步一行 32，前置序号 `1.` mono，可勾选/评论）；② 动作四键收敛为三键：`拒绝` · `仅批准（不改代码）` · `开始实施`(primary)；③ **反馈框有未提交内容时禁用"开始实施"**（VS Code 的门禁，直接移植）；④ 提供 `在编辑器打开计划`（把计划落到 `.zeokeel/plans/<id>.md`，可审计、可复制）；⑤ 计划卡**不折叠**（它是决策点），但实施后收成一行"计划已批准 · N 步"。
- **优先级**：P1（③④），P2（①②⑤）。

### 6.9 提问卡（S6）

- **业界**：Claude Code 的 `AskUserQuestion` 有选项 + `Other` 行，`Esc` 两次取消（[Interactive mode](https://code.claude.com/docs/en/interactive-mode.md)）；Cursor 的问询**不阻断** agent 继续读文件/改代码（[Agent overview](https://cursor.com/docs/agent/overview)）。
- **我们**：`AskDialog`（阻断式）。建议：① 问题用 `TypeM 16/600` 一行，说明用 `TypeXS 13`；② 选项行 36 高、单选/多选分离、**`其他（自填）` 永远最后一项**；③ 动作条 `跳过（不回答）` 左、`提交` 右；④ 回答后**在 transcript 里落一张紧凑回执卡**（"已回答：…"），不伪装成用户气泡（我们已有"回执行"概念，对齐即可）；⑤ 登记"ask 不阻断"为后续能力项（需后端支持）。
- **优先级**：P1（①②④）。

### 6.10 Composer（S7）

- **业界**：Claude Desktop 的 prompt box 下方一行选择器（environment / folder / permission / model）（[Desktop](https://code.claude.com/docs/en/desktop.md)）；VS Code 输入区带 target/agent/model/permissions 四个选择器（[Chat view](https://code.visualstudio.com/docs/agents/run/chat-view.md)）；Zed 的编辑器可 `shift-alt-escape` 扩展（[Agent Panel](https://zed.dev/docs/ai/agent-panel)）；Raycast 把主/次动作绑定键位（[Action Panel](https://developers.raycast.com/api-reference/user-interface/action-panel.md)）。
- **我们**：`ComposerShell` = 卡（`RadiusXL`）+ TextArea + 工具条（附件/模型/权限）+ 发送/排队/停止 + 提示行。建议：① **两行解剖**：第 1 行输入（min 44、max 6 行），第 2 行控制（左：附件/模型/权限；右：发送），两行之间**不画分隔线**（`Space2` 间距即可，Geist "每边框只画一次"）；② 发送按钮 **32×32、`RadiusMax`、图标 16**，idle 时 `Accent` 填充、无内容时 `DisabledPrimary`（已有）；③ 提示行用 `uikit.Kbd`（`↵ 发送`、`⇧↵ 换行`、运行中 `↵ 排队 · Esc 停止`）；④ 拖拽文件时整卡显示**2px `Accent` 虚线内框**（`BorderL3`→`Accent`）+ "松开以添加附件"；⑤ 模型 chip 显示"模型名 · 推理等级"，权限 chip 显示当前三态（已有）。
- **优先级**：P1（①③④）。

### 6.11 运行状态条（S8）

- **业界**：VS Code 状态栏左右分区（左=工作区，右=当前文件），进度放状态栏而不是通知（[Notifications](https://code.visualstudio.com/api/ux-guidelines/notifications.md)）；Zed 把 token 用量放模型选择器旁、压缩事件作为可展开条目（[Agent Panel](https://zed.dev/docs/ai/agent-panel)）；Claude Code 的 PR 状态是 footer 里带彩色下划线的链接（绿/黄/红/灰）（[Interactive mode](https://code.claude.com/docs/en/interactive-mode.md)）。
- **我们**：底部一行 6 段信息（`session_status_test.go:68`）。建议（F-05）：① 固定 **32 高**，左=运行态/队列，右=用量组；② 用量用 mono/tabular 12：`42K / 128K` + 3px 细条（`Warning` >80% / `Danger` >95%）；③ `详情` 打开 popover（轮次/步数/缓存命中/剩余上下文/压缩记录）；④ **PR/分支状态**若接入，用"链接 + 3px 彩色下划线"（Claude Code 做法）；⑤ 状态条**不承载错误**（错误走 `ErrorPanel`/toast）。
- **优先级**：P1。

### 6.12 右栏检查器（Inspector / S10）

- **业界**：GNOME utility pane——"从属于主视图放右边"、"不与 header bar 相交"、"窄窗覆盖而非挤压"、`F9` 切换（[Utility Panes](https://developer.gnome.org/hig/patterns/containers/utility-panes.html)）；Raycast 的 List Detail "有详情时不要重复 accessory"（[List](https://developers.raycast.com/api-reference/user-interface/list.md)）；VS Code Secondary Sidebar 可拖放视图（[UX overview](https://code.visualstudio.com/api/ux-guidelines/overview)）。
- **我们**：`InspectorWidth=320`（min 260）、多标签、窄窗收缩到 rail（已有规则）。建议：① 头部 36 高：标题 `TypeS 14/500` + 右侧 `⋯` + 关闭（16 图标）；② 标签行 32 高，选中用 2px `Accent` 下边线（不是整块填充）；③ 内容内距 `Space3/4`，小节用 `SectionLabel`（11/500）+ 行 32；④ **不在检查器里再套卡片**（S3 只描边行）；⑤ 空态用 Blankslate（图标 24 + 一句 + 一个动作）；⑥ 切换键 `F9`（新增），并在 `⋯` 菜单与 tooltip 里显示。
- **优先级**：P1（①②⑤），P2（⑥）。

---

## 7. 风险与取舍

| 编号 | 风险 | 影响 | 应对 |
|---|---|---|---|
| R-1 | 圆角 16→12 是**全局**改动，1512×944 + narrow 快照全部变化 | 视觉复审成本高（约 60+ PNG） | 分两步：先只改 `Panel()`/卡片路径，工具卡与对话稳定后再改对话框；每步都跑 `ui-snapshot` 人工看 |
| R-2 | 字重烘焙进 role 会改变所有标题的视觉重量，中文在 600 下容易糊 | 可读性回退风险 | 只对 ≥16px 用 600；14px 用 500；先在 `M`（16）与 `S`（14）两档试点 |
| R-3 | **官方 mygo 控件的焦点环/边框形态不可控**（`AGENTS.md` §3.4：只设度量不覆写状态色） | 我提的 `FocusRingOffset=2` 只在自绘元素生效，官方件仍是贴边环 → 两套焦点视觉 | 要么接受"官方件贴边环"并在文档里登记为已知差异，要么统一走 `*Base` 自绘（成本高）。建议：**接受差异 + 自绘富行统一 offset 环**，写进 `DESIGN.md` §7 |
| R-4 | **F-06（running 去 warning）是契约变更**，与 `AGENTS.md` §3.7 / `DESIGN.md` §5.2 冲突 | 需要 Lead 裁决，且会影响 `uikit.StatusText` 与多个快照 | 按 BRIEF §4 先指出冲突；若 Lead 不同意，退让方案：running 用 `LabelSecondary` 文字 + `Accent` spinner，warning 只留 needs-action |
| R-5 | 正文 640 列会让"中文长表格/长 URL"换行更频繁 | 可读性 vs 布局稳定性 | 只对段落/列表/引用生效；表格/代码/工具输出用 960；`ProseMeasure` 必须是**固定 DIP**而非百分比（避免窗口拉伸时行宽漂移） |
| R-6 | **D 级证据缺口**：ChatGPT 桌面、claude.ai 聊天面、Perplexity、NotebookLM、Gemini 桌面没有公开设计系统（本环境 OpenAI 域名返回 403） | "参考业界所有 AI 客户端"这一要求只覆盖到可公开取证的 8 个产品 | 已用 VS Code/Claude Code/Cursor/Zed/Raycast/Linear/Geist/Carbon 八个**可取证**样本覆盖全部 12 个表面；若需要剩余产品，建议改用"真实截图像素采样"作补充研究（需另开任务，不属文档调研） |
| R-7 | 新增 `BorderL1`、`ProseMeasure`、`FocusRing*` 等 token 会增加维护面 | 与"token 越少越好"的原则张力 | 只新增 4 个具名 token（`BorderL1`、`ProseMeasure`、`FocusRingWidth/Offset`），其余一律用现有 token + 语义别名（`SpaceInline` 等只是常量别名） |
| R-8 | 审批卡重构会动到 `Esc=拒绝` 的窗口级语义（历史上出过事故） | 回归风险高 | 重构只改**视觉层与动作文案**，不改键位语义；帧测试 `TestApprovalEscapeDenies` / `TestApprovalFooterHasNoDefaultAction` 必须保持绿 |
| R-9 | 暗色 S4 提一阶需改 brand hex，可能影响对比度测试 | `palette_contrast_test.go` 可能失败 | 改之前先跑对比度测试；若失败则放弃该建议，改用 `BorderL3` + `ShadowOverlay` 分离 |

---

## 8. 附：证据清单

### 8.1 抓取并核对的来源（2026-11-18，本环境）

| # | 来源 | 等级 | 本报告用到的关键事实 |
|---|---|---|---|
| 1 | [Geist Typography](https://vercel.com/geist/typography) | A | Heading 72–14 / Button 16-14-12 / Label 20-12（含 Mono、CAPS）/ Copy 24–13；`Copy 16` 用于 modal |
| 2 | [Geist Colors](https://vercel.com/geist/colors) | A | 10 色阶；gray vs gray-alpha；"Draw each border once"；半透明阴影；Background 1/2；Color 1-3 组件底、4-6 边框、7-8 高对比 |
| 3 | [Geist Materials](https://vercel.com/geist/materials) | A | radius 6/6/12/12 + tooltip 6 + fullscreen 16；最低 elevation；不叠 Material；不靠阴影独表达层级；双主题测试 |
| 4 | [Windows Typography](https://learn.microsoft.com/en-us/windows/apps/design/signature-experiences/typography) | A | 12/16、14/20、18/24、20/28、28/36、40/52、68/92；Semibold 代替 Bold、无 Italic；最小 14/600 与 12/400；句首大写；50–60 字母/行；裁剪+换行优先于省略号 |
| 5 | [Carbon Spacing](https://www.carbondesignsystem.com/building-blocks/foundations/spacing/overview) | A | 2/4/8/12/16/24/32/40/48/64/80/96/160；"靠近=相关"、"空间分区不需要线" |
| 6 | [Carbon Type sets](https://www.carbondesignsystem.com/building-blocks/foundations/typography/type-sets) | A | productive 14 / expressive 16；code-01 12/16；label-01 12/16；body-compact-01 14/18；body-01 14/20；heading-compact-01 14/18/600 |
| 7 | [Carbon Status indicators](https://www.carbondesignsystem.com/building-blocks/core/patterns/status-indicators) | A | 四元素（符号/形状/颜色/文字）至少三；三级注意力；>5–6 过载；合并用最高注意力色 |
| 8 | [Carbon for AI](https://www.carbondesignsystem.com/building-blocks/foundations/carbon-for-ai) | A | AI label 是通往可解释性的入口；先摘要后展开；光感样式"严格用于标识 AI，不作装饰" |
| 9 | [GNOME Typography](https://developer.gnome.org/hig/guidelines/typography.html) | A | 用系统字体；尽量减少字号/字重；避免 italic；不全大写；不硬编码字号 |
| 10 | [GNOME Utility Panes](https://developer.gnome.org/hig/patterns/containers/utility-panes.html) | A | 从属性面板放右；不与 header bar 相交；窄窗覆盖（flap）；`F9` 切换 |
| 11 | [VS Code Sidebars](https://code.visualstudio.com/api/ux-guidelines/sidebars.md) | B | 单 View Container 通常够；3–5 个 View 上限；不要把 Command 塞侧栏 |
| 12 | [VS Code Notifications](https://code.visualstudio.com/api/ux-guidelines/notifications.md) | B | 通知决策树；每条 Don't show again；一次一条；modal 只用于需立即交互 + Always/Never + 记住选择 |
| 13 | [VS Code Approvals](https://code.visualstudio.com/docs/agents/run/approvals.md) | B | 权限等级 Manual/Assisted/Allow all；Allow Once 默认 + 范围下拉（prefix/host/path）+ session/always；URL 两步批准防注入；计划批准≠工具批准 |
| 14 | [VS Code Plan work](https://code.visualstudio.com/docs/agents/run/planning.md) | B | Review Plan 卡；Open Full Plan/Submit Feedback；Implement / Approve only / Autopilot / Reject；**反馈未提交禁用批准**；四条复核清单 |
| 15 | [VS Code Chat view](https://code.visualstudio.com/docs/agents/run/chat-view.md) | B | 三种布局；sessions list compact/side-by-side；未读/进行中 badge；working set Keep/Undo；编辑已保存 |
| 16 | [VS Code Tools](https://code.visualstudio.com/docs/agents/run/tools.md) | B | 工具默认折叠可展开；Show Output/Show Terminal；Continue in Background；collapsedTools/thinkingStyle |
| 17 | [VS Code Inbox mode](https://code.visualstudio.com/docs/agents/run/sessions/organize-sessions.md) | B | Inbox/Working/Ready to merge/On hold/Done；Dismiss≠Done；移动默认无确认 + 10s Undo |
| 18 | [Claude Code Permissions](https://code.claude.com/docs/en/permissions.md) | B | 权限提示解剖与选项文案；`Esc to cancel` / `Tab to amend`；Tab 加评论；deny→ask→allow 顺序 |
| 19 | [Claude Code Interactive mode](https://code.claude.com/docs/en/interactive-mode.md) | B | 键位全集；`Ctrl+O` transcript（时间戳+模型）；MCP 折叠成 `Called slack 3 times`；任务清单 5 条；`/diff` 面板阈值；PR 状态彩色下划线；`/btw` 旁问 |
| 20 | [Claude Desktop](https://code.claude.com/docs/en/desktop.md) | B | 会话侧栏；prompt box 下选择器行；灰字建议 Tab/→ 接受；Changes 卡 +12 -1；行号 + 评论 + Cmd+Enter；Code review 卡；站点审批 Allow once/Always/Deny |
| 21 | [Cursor Agent](https://cursor.com/docs/agent/overview) | B | 侧栏 agent；checkpoint 在 timeline 可预览/恢复；queue vs steer；ask 时继续工作 |
| 22 | [Cursor Plan Mode](https://cursor.com/docs/agent/plan-mode) | B | Shift+Tab 轮转；澄清→研究→计划→编辑→构建；计划默认存家目录、Save to workspace |
| 23 | [Zed Agent Panel](https://zed.dev/docs/ai/agent-panel) | B | 消息可编辑重发；queue/steer/Send Now；Copy as Markdown；线程标题；Following the agent；Review Changes 逐 hunk；用量与 Compaction 条目 |
| 24 | [Zed UI/UX checklist](https://zed.dev/docs/development/ui-checklist) | B | 键位可发现；hover/focus 清晰；窄分栏/13 寸/高 DPI；即时反馈；**每帧 ≤8ms**；错误可行动；progressive disclosure |
| 25 | [Raycast List](https://developers.raycast.com/api-reference/user-interface/list.md) | B | 搜索 + searchBarAccessory；accessories（tag/text/date/icon + tooltip）；isShowingDetail 时不重复 accessory；EmptyView |
| 26 | [Raycast Action Panel](https://developers.raycast.com/api-reference/user-interface/action-panel.md) | B | 主动作 `↵` / 次动作 `⌘↵`；section 含 Danger zone；快捷键显示 |
| 27 | [Linear: A design reset (I)](https://linear.app/now/a-design-reset) | C | 重设计方法论：concept car、领导层支持、"设计债每 2–3 年偿还" |
| 28 | [Linear: How we redesigned (II)](https://linear.app/now/how-we-redesigned-the-linear-ui) | C | inverted-L；"reduce visual noise / maintain alignment / increase hierarchy and density"；LCH + 3 旋钮主题；contrast 变量产出高对比主题 |
| 29 | [Primer Product UI](https://primer.style/product/primitives/size/) | A/B | 组件词汇（StateLabel/InlineMessage/Blankslate/KeybindingHint/ConfirmationDialog…）与 UI/Scenario patterns 分层；**数值页被截断，未引用数值** |
| 30 | [Shape of AI](https://www.shapeof.ai/) | C | 六类模式与具体条目（stream of thought / verification / action plan / cost estimates / Wayfinders…） |

### 8.2 明确"未找到公开证据"的项（不得引用其数值）

| 目标 | 尝试的 URL | 结果 |
|---|---|---|
| ChatGPT 桌面版设计 | `https://help.openai.com/en/articles/9982051-chatgpt-desktop-app-for-macos` | **HTTP 403**；OpenAI 未公开设计系统文档 |
| ChatGPT canvas / Apps UI 指南 | `https://openai.com/index/introducing-canvas/`、`https://developers.openai.com/plugins/concepts/ui-guidelines.md` | **HTTP 403** |
| Apple HIG 排版数值 | `https://developer.apple.com/design/human-interface-guidelines/typography` | 返回空壳（JS 渲染），未取得正文 |
| Microsoft Fluent 2 token | `https://fluent2.microsoft.design/typography` | 只返回导航壳（改用 Windows 官方文档替代） |
| JetBrains 设计规范 | `https://jetbrains.design/intellij/` | 跨域重定向被拦，未取得 |
| Warp block 视觉参数 | `https://docs.warp.dev/terminal/blocks` | 只返回导航 |
| Cline / Roo 源码（工具卡解剖） | `https://raw.githubusercontent.com/cline/cline/main/webview-ui/src/components/chat/ChatRow.tsx` | 404（路径已变更） |
| Notion AI / Perplexity / NotebookLM / Gemini 桌面 / Figma / Slack / Obsidian / Things | — | 未找到公开设计系统文档 |

### 8.3 本报告用到的仓库内证据

| 证据 | 位置 |
|---|---|
| 字号/字重/行高 | `internal/design/tokens.go:37-56` |
| 间距/高度/圆角/布局/阴影 | `internal/design/components.go:11-19,39-43,46-80,108-113,133-146` |
| 调色板/边线/焦点/选中 | `internal/brand/palette.go:149,161-162,173,201,210-211,218` |
| Markdown 主题（行高 1.55、代码块透明边） | `internal/uikit/markdown.go:26-56` |
| 审批卡（0.22 警示带、0.4 描边、LG 圆角） | `internal/uikit/approval.go:198,219` |
| 侧栏行解剖（官方 SidebarItem 的聚焦满蓝 vs 自绘树行的 tint） | `internal/ui/sidebar/sidebar.go:468`、`mygo@v0.9.2/ui/sidebar.go:164-200` |
| 状态条文本 | `internal/ui/session/session_status_test.go:68` |
| 表面 ramp / 状态矩阵 / 强调预算 | `docs/DESIGN.md` §5.1、§5.2、§5.5 |
| 各 surface 规格 | `docs/AGENT-SURFACES.md` S1–S11 |
| 视觉快照 | `.cache/ui-conformance/p1-chat.png`、`p1-approval.png`、`p2-approval-diff-dark.png`、`narrow/`（目视核对的 4 张） |

> 本报告**未修改任何产品代码**；只写 `docs/research/redesign-2026-11/A2-industry-design-languages.md` 与 `A2-best-practices.tsv`。
