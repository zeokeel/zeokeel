# A3 · 视觉细节审计（ZeoKeel 重设计，逐屏像素级）

> 审计官：visual-audit（A 阶段，只读产品代码）
> 基线：`.cache/ui-conformance/`（1512×944 主场景 + `narrow/` 1024×720，亮/暗齐全，manifest head `e9a0d6b`，119 张）
> 证据方式：`read_image` 逐张看 **41 张整屏 + 17 张 3.5×–9× 裁剪**，并用 ImageMagick 做像素扫描（颜色/几何）与代码回查（`file:line`）。
> 裁剪图落在 `.cache/ui-conformance-audit/crops/`（未覆盖基线）。**未改任何产品代码。**

---

## 0. 结论摘要（按影响排序）

1. **P0 窄窗布局破损**：1024 宽 + 检查器打开时，顶带右侧控件簇（⋯/🔍/折叠）溢出到内容列之外，被检查器裁掉一半，并与「检查器」标题视觉相撞（`narrow/narrow-session-inspector.png`）。这是本次唯一一个"用户能直接看见坏掉"的缺陷。
2. **P0 通知浮层骑在阅读内容上**：toast/警告卡锚定在内容列右上角（y=82 起），盖住用户消息正文、盖住审批提示、在设置页盖住页脚按钮区；窄窗还能叠 2 张 + "＋2 条通知排队"。企业工具里通知只能出现在不遮挡当前任务的角落。
3. **P1 分割线有三套做法**：侧栏接缝是 **2px 双线**（16% + 10%），检查器接缝 1px 10%，设置页导航与正文之间 **0px**（只靠 1.04:1 的底色差）。一个 App 的 chrome 接缝必须只有一种线。
4. **P1 Accent 严重超预算**：展开侧栏三个导航目的地图标全是品牌蓝（`#0D44F6`），折叠 rail 里同一批图标却是灰的；加上 tab 下划线、选中左条、composer 焦点环、发送按钮、toast 链接，一屏 accent 目标 ≥6，"主按钮唯一"失效。
5. **P1 浅色主题的"卡片"不存在表面**：`BgRaised` 与 `BgBase` 都是 `#FFFFFF`，卡片=白底白页 + 1px 10% 描边；用户消息气泡同构（白底+描边+r16），于是**消息气泡读起来像输入框**，"看这张图"产物按钮又和气泡一模一样。
6. **P1 审批卡的琥珀四层叠加**：40% 琥珀 1px 边框 + 22% 琥珀大色块 + 琥珀字 + 琥珀「Esc 拒绝」，整卡呈"便签/罚单"质感；扩散到 diff 区是"整块绿洗底 + 行底 + 3px 绿条 + 绿边"四层绿。
7. **P1 11px 成句仍在**：底部状态条 6 个指标（`3 轮对话 · 1 步工具调用 · 62 tok/s · 42K/128K tok · 缓存命中 86% · 上下文剩余 67%`）、每条消息的时间/用时/tokens、code fence 的语言与"复制"按钮，全部 11px —— AP-22 明令禁止的类型用法，而且恰恰是信息密度最高的一行。
8. **P1 三种表格语言**：设置页/上下文页是"每行一条 1px 横线"，表格表头有**竖线**而正文没有，Markdown 表格是**全网格**。三套并存，用户无法建立"这里的数据长什么样"的预期。
9. **P1 右栏 320px 里叠了 3 层 header 条**（全局 tab 条 / 文件 tab 条 / 文件头），120px 高三条 1px 横线，且 `README.md` 在 100px 内出现 3 次、两条 accent 下划线上下相邻。
10. **值得保留的优点**（改的时候别弄坏）：`LabelTertiary #58606E` 实测 6.34:1（AA 达标）、暗色导航确实比页面深（`#0B0B10` vs `#111118`）、状态色语义正确（运行中=warning、失败=danger、已停止=中性）、行高与栅格基本还在 4 的倍数上（36/32/28/24/8/12/16）。

---

## 1. 现状（证据）

### 1.1 覆盖矩阵（实看 41 屏 + 17 裁剪）

| 场景 | PNG | 结论要点 |
|---|---|---|
| 会话主屏 | `p1-chat.png` | 接缝双线、气泡描边、11px 元信息、composer 焦点环 |
| 审批 | `p1-approval.png`、`p2-approval-diff.png`、`p2-approval-diff-dark.png` | 琥珀四层、diff 绿三层、按钮行下方 16px |
| 轨迹 | `p1-trajectory.png` | 右列 7 个"在对话中查看"、卡片与散行两套系统 |
| 设置：外观/行为/关于/模型/记忆/归档 | `p2-appearance.png`、`p2-behavior.png`、`p2-about-settings.png`、`p2-model-settings.png`、`p2-memory-settings.png`、`p2-archive-settings.png` | 导航 0px 接缝、segmented 断线、小节规则线有无不一、正文 13px 无段距 |
| 右栏 | `p2-files.png`、`p2-file-preview.png`、`p2-file-diff.png` | 3 层 header、README.md 三连、行内两按钮 |
| 上下文 | `p2-context.png`、`p2-context-sections.png`、`p2-context-dark.png` | 表格两套边框、标题混入数据行、11px 状态条 |
| 能力 | `p2-capabilities-skills.png`、`p2-capabilities-connectors.png`、`p2-capabilities-skills-dark.png` | 卡内徽章宽度导致路径列错位、右缘不齐 |
| 自动化 | `p2-automation.png` | 行内 5 控件 + 2 徽章、徽章浮在控件行之上 |
| 杂项 | `p2-ask.png`、`p2-attachments.png`、`p2-artifact.png`、`p2-drop.png`、`p2-interrupted.png`、`p2-goal.png`、`p2-goal-blocked.png`、`p2-palette.png`、`p2-team-overlay.png`、`p2-status-strip.png`、`p2-tool-row-states.png`、`p2-landing.png`、`p2-insights.png`、`p2-markdown-specimen.png`、`p2-modelmenu.png` | 各自见 §2 |
| 暗色 | 上述 `*-dark` + `narrow/dark-session.png`、`narrow/dark-settings.png` | 双线在暗色更刺眼、卡片台阶 1.09:1、composer 阴影不可见 |
| 窄窗 | `narrow/narrow-session(-inspector).png`、`narrow/narrow-settings.png`、`narrow/narrow-landing.png`、`narrow/dark-session.png` | P0 溢出、建议按钮内边距 7px |

> 清单里**没有** `p2-errors*.png` 与 `p2-models.png`（模型页实际叫 `p2-model-settings.png`，`p2-model-*`/`p2-modelmenu*` 是弹窗与菜单）。错误态我用 `p2-interrupted.png`、`p2-goal-blocked.png`、`p2-capabilities-connectors.png`（连接失败）覆盖。`p2-appearance-actions`、`p2-english*`、`p2-jobs*`、`p2-team-tasks*`、`p2-sidebar-*` 未逐张展开（不在指定清单内，已抽查同类问题）。

### 1.2 量化基线（像素实测，非估计）

| 项 | 实测值 | 出处 |
|---|---|---|
| 侧栏 | 280 宽；**右接缝 2px**：`x=279 #D3D5D6`（16% ink over `#F9FAFB`）+ `x=280 #E7E7E7`（10%） | `p1-chat.png` 列扫描 |
| 暗色同一接缝 | `x=279 #37373B` + `x=280 #2E2E34` | `narrow/dark-session.png` |
| 检查器接缝 | **1px**：`x=1189 #E7E7E7` | `p2-file-preview.png` |
| 设置导航↔正文 | **0px**：`x=569 #F9FAFB` → `x=570 #FFFFFF`（1.04:1） | `p2-appearance.png` |
| 顶带 | 高 72（40 标题行 + 32 tab 行），底边 1px `#E1E2E4`（BorderL2 over `#F9FAFB`） | 同上 |
| 对话阅读列 | 正文左 `x=433`，右 `x=1360` → **927**（≈66 全角字/行） | `p1-chat.png` |
| composer | `x=417..1376`（960）、`y=766..890`（124），XL r20 | 同上 |
| 消息气泡 | `x=1103..1360`、`y=415..462`，白底 + 1px `#E7E7E7` + r16 | `p2-status-strip.png` |
| 审批卡 | `x=433..1360`、`y=473..778`，边 1px `#F0C99B`（warning α0.40），头块 22% `#F7E1C8` 高 54 | `p1-approval.png` + `approval.go:198,219` |
| 卡片/对话框 | 卡 r16 + 1px；设置面 860×800 外框（820×760 内容 + 20 padding），**无边框只靠阴影** | `p2-appearance.png` |
| 控件高度 | 搜索/树行 32、chip 28、按钮/composer 动作 36、分段段 28（轨 36） | 多屏 |
| 字号实测档位 | 一屏最多 5 档：20/16/14/13/12/11 | 设置页、会话页 |
| 对比度 | `LabelSecondary #545C6A` 6.74:1、`LabelTertiary #58606E` 6.34:1（白底），达标 | `palette.go:151,160` |

### 1.3 交互状态抽查（"每个按钮/悬浮/点击"）

- 原生件（`ui.Button`/`SidebarItem`/`SearchField`/`TextField`/`TabsBase`/`Switch`）：hover/pressed/focus/disabled 由主题提供，**抽查通过**；但默认态与 hover 态的**边界太窄**（见 VD-27）。
- 自绘 `ButtonBase` ≈21 处（sidebar 9、runstatus 3、session 2、rightpanel 1、iconbutton 6），其中 12 处显式读 `Hovered()`；`rowTint()`（`sidebar.go:1872`）是唯一"选中/按下/悬浮"三态统一实现，其余自绘行靠单独 `if Hovered()`。
- **没有 hover 反馈的区域**（视觉上"死"）：code fence 的"复制"（仅靠原生 button 的点击反馈）、用户气泡的 ⋯（灰圆盘常态，看起来已按下）、右栏树行、能力卡本身（只有 ⋯ 可点）。
- 焦点环：composer 用 1px Accent **替换** 10% 常规边（`composer.go:274`），与常态同宽 → "激活"只表现为颜色变化；搜索框同样用 1px Accent 边框（`p2-appearance.png` x=358 实测 `#0D44F6` 单像素）。

---

## 2. 发现（按严重度排序）

### 2.1 P0（破坏可用性）

| 编号 | 位置（PNG · 坐标 · 代码） | 问题 | 为什么是问题 | 具体改法 |
|---|---|---|---|---|
| VD-01 | `narrow/narrow-session-inspector.png` · 顶带 `x=680..702, y=0..42` 与检查器 `x=700..740, y=8..40` · `uiroot/frame.go:815` `contentHeader` → `screenHeaderRow` 尾部 chrome 簇 | 1024 宽下顶带尾部控件（⋯ / 🔍 / 折叠）**溢出内容列**，被检查器裁掉一半，且与「检查器」标题挤在同一 40px 行里（裁剪 `crops/narrow-hdr.png` 可见「⋯ 🔍 台 ▯ 5」残字） | 控件被裁 = 点不到、看不清；标题与图标相撞 = 看起来像坏掉的客户端。这是全仓唯一"结构性破损"级问题 | 顶带尾部簇加 `Shrink(1).MinWidthPercent(0)`，并把 `⋯` 之后的三项收进一个 overflow 菜单；规则：`windowW − SidebarRail(64) − InspectorMinWidth(260) < 640` 时尾部只保留 `⋯`，其余进菜单（新增 `design.HeaderOverflowMin = 640`） |
| VD-02 | `p1-chat.png` · toast `x=1198..1478, y=82..176` 覆盖用户气泡 `x=1103..1218, y=86..135`；`narrow/narrow-settings.png` · toast `x=731..1006, y=490..680` 覆盖设置页脚；`p2-ask.png` 同理 · `toasts.go:369-392`（anchor `Justify(Start)` + `toastTopInset 84`） | 通知浮层锚在**内容列右上角**，直接骑在阅读内容/审批提示/设置页脚上；可同时存在 2 张 + "＋N 条排队" | 用户正在读的那段话被永久遮挡（截图里用户自己的消息只剩「慢慢检查当前工」）；设置页里它盖住页脚主按钮区 = 可能挡住"保存"的点击 | 改锚点：默认 `FromBottom`（status strip 之上、composer 右下，`Bottom = Space8`），modal 打开时同理但 `Right = Space6`；一屏最多 1 张、其余折叠为计数器；`toastMaxWidth` 360 → 320，`toastMinWidth` 280 → 240 |

### 2.2 P1（明显不专业）

| 编号 | 位置（PNG · 坐标 · 代码） | 问题 | 为什么是问题 | 具体改法 |
|---|---|---|---|---|
| VD-03 | `p1-chat.png` `x=279,280`（全高）+ `narrow/dark-session.png` 同坐标 · `uiroot/frame.go:168-171`（侧栏 `BorderL3`）与 Split 的 `ui.Divider` | 侧栏与正文之间是**两条 1px 叠在一起**（16% + 10%），全 App 最重的竖线；暗色更刺眼（`#37373B`+`#2E2E34`） | 一条接缝两笔，等于把 16% 的线再加粗又加深一档；和检查器接缝（1px 10%）并排时左右不对称，chrome 立刻显脏 | 接缝只留一笔：侧栏 `BorderWidth(0,0,0,0)`，由 Split divider 画 1px `BorderL2`；若 divider 不可调，则侧栏保留 `Border(1, BorderL2)` 并把 divider 设为 `Transparent`+`Width(0)`（在 `frame.go` 顶部集中一个 `chromeSeam(t)` 常量） |
| VD-04 | `p2-file-preview.png` `x=1189`（1px）· `p2-appearance.png` `x=569/570`（0px）· `frame.go:1808-1830` 设置导航 `.Background(t.BgSidebar)` 无 border | 三种接缝权重：**2px / 1px / 0px**。设置页导航与正文只靠 `#F9FAFB`↔`#FFFFFF`（1.04:1）分区 | 用户学不到"这里是两栏"；同一 App 里"面板边界"这一视觉概念失效，是"看着不舒服"的主要来源之一 | 统一为**1px `BorderL2`**：设置导航加 `BorderWidth(0,1,0,0) BorderL2`；检查器保持 1px；侧栏按 VD-03 收敛到 1px。写进 DESIGN §5.1："任何面板接缝 = 1px BorderL2，禁止 2px 与 0px" |
| VD-05 | `p1-chat.png` `x=26..34, y=142..230`（自动化/洞察/能力图标 `#3462F7/#0E45F6`）vs `narrow/narrow-session-inspector.png` rail `x=24..40, y=84..300`（灰 `#6B7280`）· mygo `ui/sidebar.go:184` `iconColor := c.accent(t)`；`sidebar.go:468` | 同一批导航目的地：展开侧栏图标是**饱和品牌蓝**，折叠 rail 里是**灰**。加上 tab 下划线/选中左条/焦点环/发送/toast 链接，一屏 accent 目标 ≥6 | 强调色被当成"装饰色"用掉后，真正的主操作（发送、允许、新建任务）不再跳出来；导航图标全蓝也让界面显得"廉价彩色" | 给导航目的地一个局部主题：`sidebar.go:1848` 已有 `sessionSelectionTheme()` 先例，加 `navDestTheme(t)`：`Accent = LabelSecondary`（图标墨色）、`AccentText = LabelPrimary`，选中仍走 `SurfacePressed` 底 + `LabelPrimary` 字；当前目的地允许**唯一一个** accent 图标。写进 DESIGN §3 规则 10 的附注 |
| VD-06 | `p1-chat.png` 卡片/气泡区域 · `palette.go:145` `Surface: #FFFFFF`、`Background: #FFFFFF`、`Border: ink 10%` | 浅色下 S2（`BgRaised`）= S0（`BgBase`）= 白，卡片只靠 1px 10% 描边"存在"，没有表面台阶 | 描边成了唯一的层级手段 → 全 App 到处是线（用户抱怨的"各种边框线"根源）；卡片和输入框、气泡、代码块看起来是同一类东西 | 拉开 ramp：`BgBase #FBFCFD`（或 `#F7F8FA`）、`BgSidebar #F4F6F8`、`BgRaised #FFFFFF`、`BgStrong #EEF1F4`。这样 1px 线才有意义，且可以把"卡片边框"减到 `BorderL2` 的半档（`ink 8%`）。同步更新 DESIGN §5.1 表面 ramp 表 |
| VD-07 | `p2-status-strip.png` 气泡 `x=1103..1360, y=415..462` · `session/view.go:1359` `design.Apply(ui.Column(c), t.Panel())` | 用户消息 = 白底 + 1px `#E7E7E7` + r16 —— 与输入框/产物按钮同构；`p1-chat.png` 的「看这张图」按钮（`x=1257..1360, y=428..475`，47 高）与气泡几乎完全一样 | 用户分不清"这是我发的话"还是"这是个可点的东西"；描边气泡在白色页面上像未填写的表单 | 气泡改成**填充面**：`Background(BgStrong).Border(0)`（或 accent 6% tint），`Radius(LG)`，`Padding(Space3, Space4)`；产物按钮走独立词汇（`BgRaised` + `BorderL2` + `RadiusMD` + 32 高），不要 r16/47 高 |
| VD-08 | `p1-approval.png` 卡 `x=433..1360, y=473..778` · `uikit/approval.go:198`（`Border(1, Warning.Alpha(0.4))`）、`:219`（`Background(Warning.Alpha(0.22))`） | 审批卡叠了 4 层琥珀：40% 琥珀边 + 22% 琥珀 54px 高色块（含正文说明）+ 琥珀图标/标题 + 琥珀「Esc 拒绝」 | 大块 22% 琥珀（`#F7E1C8`）在纯白页上像"便利贴/罚单"；40% 边框比全 App 其它卡（10%）重 4 倍，两套卡边框并存 | 卡=**中性卡**：`Background(BgRaised)` + `Border(1, BorderL2)` + r16；琥珀只保留两处：① 左侧 2px `Warning` 竖条（`Radius` 内嵌实现），② 标题前 `WarningText` 图标 +「需要审批」`StatusBadge`。说明文字回到 `LabelSecondary` on 正常表面。「Esc 拒绝」改 `LabelTertiary`（kbd 提示不该抢色） |
| VD-09 | `p2-approval-diff.png` `x=32..928, y=95..265` · `uikit/diffpreview.go:158-159`（`BgBase` + `Border(1, BorderL2)` + `RadiusSM`）与 12% success 洗底 | diff 区四层绿：整块 12% 绿洗底 + 每行绿底 + 3px 绿条 + 绿边；行号列却是白底 → 一条"绿带 + 白缝"的条纹 | 整块洗底让"哪几行变了"失去对比基准（全是绿的）；绿+白交替像表格错位 | 块=`BgBase` 白 + `Border(1, BorderL2)` + r8；只有 `+` 行 `Success.Alpha(0.08)`、`-` 行 `Danger.Alpha(0.08)`；行号列与代码区同底（去掉白缝）；左条 2px→2px 但只画在增删行上；hunk 头 `LabelTertiary` 12px 无底色 |
| VD-10 | 所有会话屏底部 `y=898..906`（`p1-chat.png`、`p2-context.png`）· `uikit/runstatus.go:412` `t.Font(design.TypeXXXS)`（11px） | 状态条把 6 项指标拼成一句 11px 灰字：`3 轮对话 · 1 步工具调用 · 62 tok/s · 42K/128K tok · 缓存命中 86% · 上下文剩余 67%` | AP-22/§5.3 明确"11px 禁止成句"；而这行是全屏信息密度最高的一段，11px × 6 项在 1512 宽下已经难以扫读 | 字号 `TypeXXXS`→`TypeXXS`（12/18）；内容裁剪到 3 项（轮次 · 上下文 42K/128K · 缓存 86%），其余进 tooltip（`Exact` 已存在）；用 `·` 前后加 `Space1` 而不是纯空格；`MaxLines(1)` 保留 |
| VD-11 | 每条消息元信息 `y=190..200 / 250..262`（`p1-chat.png`）、`y=478..494`（`p2-status-strip.png`）· `session/view.go:2147`（分隔点 11px）、`:2310`（时间 `TypeXXXS`） | "23:55 · 用时 353ms · 42.1K tokens · ⋯" 全部 11px；分隔点也是 11px 文本元素 | 元信息是用户判断"这条多久前/花了多少"的依据，11px 在 100% 缩放下是"刻意隐蔽"，不是"克制" | 统一 `TypeXXS`（12/18）+ `LabelTertiary`；分隔点改 `ui.Box(2×2, RadiusMax).Background(BorderL3)`（图形分隔不占字阶）；`⋯` 目标区 24×24 |
| VD-12 | `p2-appearance.png` 轨 `x=585..1155`、选中段 `x=585..655, y=222..258`（浅色/深色分段）；`p2-insights.png` `x=352..484, y=68..100` · `uikit/segmented.go:76-87` | 选中段 = `BgSelected` 底 + **2px Accent 仅底边**，被 r8 圆角切掉两端，看起来像"下划线画歪了/被裁了"；轨底又是 `BgHover`（蓝 6%），形成 轨道蓝 → 段蓝 → 断线 蓝 三层 | 用户读到的是"半个高亮"，不是"选中"。加外框又会和焦点环混淆（代码注释里已自认），说明这个方案本身不成立 | 二选一：①**抬升块**（推荐）：轨 `BgStrong(#F1F3F5)`、选中段 `BgRaised`（白）+ `Border(1, BorderL2)` + `LabelPrimary/600`，去掉 accent 下划线；②**下划线语言**：轨透明，选中段 `LabelPrimary/600` + 底边 2px Accent 且**段本身不圆角**（`Radius(0)`），与顶带 tab 同构。两种都不要再叠加 tint |
| VD-13 | `p2-about-settings.png` `x=585..1155, y=210..440`；`p2-context.png` `y=200..500`；`p2-markdown-specimen.png` `x=20..133, y=360..470` | 三套表格边框：(a) 每行一条横线（关于/上下文）；(b) 表头有**竖线**+行横线（上下文分段/洞察）；(c) **全网格**（Markdown 表格，每格都有 1px 边） | 同一产品里"表格"的样子有三种，用户无法形成预期；全网格在 14px 字下像 Excel 截图，竖向线尤其脏 | 只保留一套：**行分隔**型——表头 `LabelSecondary/TypeXXXS` 无底无竖线，表头下 1px `BorderL2`，行间 1px `BorderL2`（或 `Space3` 间距无行线，二选一并在 DESIGN §6 固化）；Markdown 表格改"仅行线 + 表头字重 600"，去掉所有竖线与外框 |
| VD-14 | `p2-appearance.png`：`主题`(y=198) 无规则线、`桌面集成`(y=386) 有规则线(y=397)、`全局热键`(y=511) 有规则线(y=548)；`p2-archive-settings.png` `归档的会话` 有规则线(y=213)；`p2-model-settings.png` `模型列表` 无 | 设置页小节标题"有时下面一条规则线、有时没有"，且标题→内容间距在 12/14/16 之间漂移 | 规则线是否有 = 用户判断"这一节到哪结束"的唯一信号，信号自身不稳定就变成噪声 | 统一为**无规则线**：小节 = `Space6(24)` 上间距 + 标题 `TypeM/600` + `Space3(12)` 下间距；只有"整页唯一分区线"（如 `诊断与设置` 之前）允许 1px `BorderL2`，全页最多 1 条 |
| VD-15 | `p2-model-settings.png` `y=268..510`（3 个模型块）；`p2-memory-settings.png` `y=310..560`（5 条记忆）· `settings/modelcatalog.go`、`settings` 记忆列表 | 列表行**无分隔、上下间距仅 12px**，而每行是 3–4 行文本块 → 下一行的标题紧紧贴住上一行的价格行，行归属不清 | 用户会以为「DeepSeek Chat」是 snapshot-model 的第 4 行属性 | 行间加 1px `BorderL2`（平铺行 + Divider，DESIGN §3 AP-25 已要求）+ 行内 `PaddingY(Space3)`；或改 `Space6(24)` 间距并给行首加 16px 图标位做视觉锚。模型块内 3 行改成"名称行 / 元信息 12px / 价格 12px"三级，价格用 `MonoText` 数字对齐 |
| VD-16 | `p2-file-preview.png` / `p2-file-diff.png` 右栏 `x=1189..1512, y=0..122` · `rightpanel/view.go:98`、`:136`（两条 `BorderWidth(0,0,1,0) BorderL2`）+ 文件头行 | 320px 面板里叠 3 层 header（全局 tab / 文件 tab / 文件头）共 120px 高，三条横线；`README.md` 在 100px 内出现 3 次；两条 accent 下划线（文件 tab + 全局 tab）上下相邻 18px | 三层头把 320 宽的面板吃掉 1/6 高度，且信息完全冗余（同一个文件名说三遍）；双 accent 下划线让"当前在哪一层"失焦 | 合并：全局 tab 与文件 tab 同一行（`开始 / 文件 / README.md ×`），文件头行只保留路径/操作（复制、重载）并改用**面包屑 12px**；全局 tab 不用 accent 下划线，改用 `BgSelected` 药丸（面板内选中态唯一词汇） |
| VD-17 | `p1-trajectory.png` 右列 `x=1271..1360, y=258..696`（7 个"在对话中查看"） | 7 个 32px 描边按钮在右缘排成一列"按钮脊柱"，左侧内容行之间没有任何分隔线；上方是 1 张 r16 卡片，下方是散行 | 右侧按钮列的视觉重量超过内容本身；同屏"卡片"与"无容器行"两套系统，用户不知道哪些是对象 | 「在对话中查看」改为行 hover/focus 时出现的 trailing icon（16px，`IconButton`），或整行可点（`ButtonBase` + `rowTint`）并在行尾放 chevron；轨迹条目统一为"行 + 1px 分隔线"，不用卡片（`p1-trajectory.png` 的团队任务卡保留为"决策卡"这一特例） |
| VD-18 | `p2-automation.png` 第 2 行 `x=1176..1450, y=310..372` | 一行里 3 个状态徽章（`已停用` 浮在 y=317、`未知计划` y=345、`已投递` y=439）+ 5 个控件（修复/启用+开关/立即运行/编辑/⋯）；徽章行与控件行不在同一基线上 | 8 个可点/状态目标（3 徽章 + 5 控件）挤在 180px 宽的右区，眼睛无法排序；徽章"浮"在控件上方像没对齐 | 右侧最多 2 个常显动作（`立即运行` + `⋯`），其余进 `⋯` 菜单；`已停用` 从徽章降级为行首 `StatusText`（12px，与 DESIGN §5.2 "紧凑行内三层"一致）；徽章与开关同一 `AlignItems(Center)` 基线 |
| VD-19 | `p2-behavior.png` `x=585..1155, y=188..290`（5 行 13px 连排） | 设置页正文 5 行 13px 之间**没有段距**，行高 20 且段落无间距，形成一块"文字墙" | 这 5 行是 5 条独立规则（权限三态、审批与沙箱、当前会话），挤在一起后没人愿意读 | 每条一行 → 每条一个 `Column Gap(Space2)` 块；正文 `TypeXS(13/20)`→`TypeS(14/24)` 或加 `Space2` 段距；行宽 `MaxWidth(design.DialogBodyMax=480)` 限制在 480 内提升可读性 |
| VD-20 | `p1-chat.png` 发送 `x=1280..1363, y=840..875`（fill `#F8F9FA`、边 `#ECEDEE`、字 `#B4B8BF`） | 禁用态发送按钮：填充 1.05:1、描边 1.08:1、文字约 2.1:1 → 在白色页上**几乎隐形**，只剩一个轮廓 | 空输入时用户找不到"发送"在哪，也不知道"输入后这里会亮"；企业工具的主操作应当在禁用时仍清晰可辨 | 用现有 `design.DisabledPrimary()`（`BgStrong #F1F3F5` + `LabelTertiary` + r10 + 36 高）；或禁用态用 `BgHover` 底 + `LabelSecondary` 字（≥3:1），并保留 1px `BorderL2` |
| VD-21 | `narrow/narrow-session.png` / `narrow/narrow-landing.png` 顶带 `y=8..36` | 1024 宽时 `智能体团队`/`子智能体` 两个 chip 退化为**纯图标**（人形 / 波形），失去名称；再窄一档就撞上 P0 溢出 | AGENTS §3.17 要求关键入口常显且可发现；图标 chip 无可读名称，用户只能 hover 才知道是什么 | chip 折叠保留**文字**、改为更紧凑的 `icon + 数字`（`👥1`），或降级为顶带右侧一个"协作"图标按钮 + 菜单（保留可发现性、可点性）；折叠规则写进 `design.HeaderOverflowMin` 的同一处 |
| VD-22 | `narrow/dark-session.png` composer `y=545..665`；`p2-approval-diff-dark.png` 头块 `#4B3820`；`narrow/dark-session.png` 卡片 | 暗色三处：① composer 阴影是 5% 黑（`ShadowComposer`），在 `#111118` 上完全不可见；② 卡片 `#1B1B26` vs 页 `#111118` 仅 1.09:1；③ 审批头块 22% warning over `#1B1B26` = `#4B3820` 浑浊泥棕 | 暗色里层级的三个手段（阴影/表面差/语义色）同时偏弱或偏脏，界面像"一块黑"，只有边线在撑结构 | ① composer 暗色给 `Border(1, BorderL3)` + `Shadow(0,2,8,0, rgba(0,0,0,0.45))`（`ShadowComposer` 加 dark 分支）；② 暗色卡片改 `#20202D`（与页 1.4:1）；③ 审批头块改 `Warning.Alpha(0.12)` over `#20202D` ≈ `#3B3327`，或按 VD-08 去掉头块 |

### 2.3 P2（精修）

| 编号 | 位置（PNG · 坐标 · 代码） | 问题 | 具体改法 |
|---|---|---|---|
| VD-23 | `p1-chat.png` 正文 `x=433..1360`（927 宽 ≈66 全角字/行）· `design/components.go:72` `TranscriptWidth 960` | 14px 正文 927px 宽，比 ChatGPT(768@16) / Claude(720@16) 明显更宽，长段落回读困难 | `TranscriptWidth 960 → 816`（内容 784），或正文升 `TypeBody 15/24` 并把列宽收到 860；composer 同步（同一 token 已共享） |
| VD-24 | `p2-status-strip.png` 气泡右缘 `x=1360` vs composer 右缘 `x=1376` | 消息列被 list 内边距内缩 16px，导致"对话右缘"与"输入框右缘"差 16px，垂直线不齐 | 消息行的 `PaddingX` 与 composer 对齐：要么 composer 加 `PaddingX(Space4)` 内缩，要么列表去掉横向 padding 让气泡贴到 1376 |
| VD-25 | 同一屏统计：composer 20 / 卡 16 / 图标钮圆 12 / 按钮 10 / chip 8 / badge 999 → 6 档 | §5.4 规定"同屏最多 3 档圆角"，实测会话屏 6 档 | 收敛为 3 档：**8**（chip/小控件）、**10**（按钮/输入）、**16**（卡/气泡/composer）；`RadiusXL 20` 从 composer 撤掉（改 16 或 14），`RadiusMax` 只留给真正的药丸（`StatusBadge`） |
| VD-26 | `p1-chat.png` composer `y=766..890` · `components.go:143-149`（`Panel` + `RadiusXL`） | composer 是 r20 的"胶囊卡"，而内容区全是 r16/r8；XL 圆角与内容不协调，且 1px 焦点环在 20 半径上显得更细 | composer `Radius(RadiusLG=16)`；焦点态改 `Border(2, Accent)`（内嵌 1px 视觉外扩）或 `Border(1,Accent) + Shadow(0,0,0,2,rgba(13,68,246,.18))` 的柔光环 |
| VD-27 | `composer.go:270-277`（`Focused→Accent`、`Hovered→BorderL3`） | 常态 10% → hover 16% 只差 6 个色阶，hover 几乎不可见（"悬浮没反应"） | hover 用 `BorderL3` + `Background(BgRaised)` 提升一档（暗色 `#20202D`），或 hover 用 `Shadow(0,3,10,rgba(0,0,0,.08))` 的浮起感；常态 8%、hover 16%、focus accent 三级各差 ≥8% |
| VD-28 | `p1-chat.png` code fence `x=433..631, y=347..407` · `markdown_code.go:34-48`（`Padding(Space1,Space2,Space0,Space2)`） | ① 头部只有 4px 上 / **0px 下**内边距；② "bash" 左缘 441、代码文本左缘 448 → 差 7px 不齐；③ "复制"按钮 11px 字、高约 16px（<24 命中区） | 头部 `Padding(Space2,Space2,Space1,Space2)`；语言标签与代码同左缘（body 的 0.8em 内边距改为与头部一致的 `Space2`）；复制改 `IconButton`（24×24，图标 16）+ 复制成功 toast，去掉 11px 文字 |
| VD-29 | 附件 chip `x=1207..1317, y=752..780`（28）· 文件 chip `x=1219..1359, y=480..504`（24）· 能力卡徽章（20） | 同一"类型标签/chip"角色有 28/24/20 三种高度 | 统一 `ControlCompactHeight 28`（`ControlChip`），徽章类统一 `20 + PaddingX(Space2)`；文件 chip 的 24 是"半档"，删掉 |
| VD-30 | 消息 ⋯ 灰圆盘 `x=1341..1365, y=474..498` · 顶带 ⋯ 裸图标 · 产物弹窗 ✕ 灰圆 | 图标按钮三种形态：灰圆盘（看起来已按下）/ 裸图标 / 灰圆（r=Max），同一 `IconButton` 组件三种外观 | 统一 `uikit.IconButton` 两变体：**Ghost**（无底，hover 才出 `BgHover`）用于行内与顶带；**Filled**（`BgStrong`，r=SM 8，非圆形）用于弹窗关闭。灰圆盘从默认态去掉 |
| VD-31 | `p2-landing.png` `x=804..972`（3 个 168 宽的阶梯按钮）· `narrow/narrow-landing.png` 同 | 三个建议按钮宽 168、文案 11 字几乎顶到边（左右内边距≈7px），且等宽竖向阶梯排列像"按钮塔" | 改横向 wrap 的 chip 行（`ControlChip` + `PaddingX(Space3)`，内容宽度自适），或改成 480 宽的左对齐行列表（前置 16px 图标 + 文字，`Space4` 内边距） |
| VD-32 | `p2-ask.png` 弹窗 `x=511..1001, y=290..650` · `uikit/ask.go:117,134-150` | 选项为 `FillWidth` 原生 `SecondaryButton`（标签居中）+ 三段叠放；页脚 hint（12px）居中排在「取消」按钮**下方**，与按钮右对齐相冲突 | 选项左对齐（`Row` + 前置序号/圆点）+ 高度 36；hint 移到动作行左侧（`Spacer` 右推动作），与 `DialogFooter` 同一条基线；hint 字号 12 保持 |
| VD-33 | `p2-palette.png` `x=456..1056, y=225..720` · 行描述列 + kbd 列 | 描述文字右对齐到 kbd 列，长描述（`权限模式：…所有权限`）挤压到 kbd 位置；`Esc: 关闭 / 拒绝…` 被截断成"…"；底部 hint 之下还有 45px 死空白 | 三列定宽：`Label` 撑满 + `Description` 定宽 `OverlayWidthS(380)/2` + `kbd` 40 定宽右对齐；描述超长直接 `MaxLines(1)+Ellipsis` 并进 tooltip；弹窗高度改由内容决定（`Gap(Space1)` 收尾，去掉固定高） |
| VD-34 | `p2-capabilities-skills.png` 两卡 `x=300..680 / 700..1080`，徽章宽 44 vs 32 | 卡内尾部行 = 徽章 + mono 路径 + 复制：徽章宽度不同 → 两张卡的路径左缘错开 12px | 徽章固定 `MinWidth(56)` 或移到行尾（路径靠左、徽章贴右），保证跨卡对齐 |
| VD-35 | `p2-team-overlay.png` `x=703..1222, y=38..367` | 面板里"成员/还没有队友"、"共享任务/还没有共享任务"两组空态文案占掉一半高度，中间塞一张已结束成员的卡；顶部 `0 名成员 · 0 个运行中 · 1 条已结束` 自相矛盾 | 空 epi 折叠成一行：`成员 0 · 共享任务 0`，仅在有内容时展开分组标题；顶部 meta 只显示与内容相关的项 |
| VD-36 | `p2-interrupted.png` 提示条 `x=416..1376, y=110..158`（1px 描边 r16 全宽条 + 中性 chip） | 与审批卡、goal band 一起构成"第三种全宽描边条"，每个都带自己的 chip/按钮/✕ | 提示条改成**无边框 + `BgStrong` 底 + r8**（或 2px `BorderL3` 左条），与"卡片"区分开；全 App 只允许审批卡是"描边卡" |
| VD-37 | `p2-about-settings.png` `x=585..1155, y=210..440`（7 条横线） | 定义表 7 行 → 7 条 1px 横线；标签列 163 宽（`名称`只占 30px）导致值列远在 748；小节标题`最近一次请求`以数据行形式出现（`p2-context.png` y=300） | 行高 36 + 只画**行间**线（首行前/末行后不画）；标签列定宽 120 或改"标签在上、值在下"的两行式；小节标题独立成 `TypeXS/600` 行，不参与表格线 |
| VD-38 | `p2-files.png` 树 `x=1216..1300`：文件夹行有 chevron，文件行无 leading glyph | 文件行（notes.txt/README.md）左缘比文件夹标题右移，树的垂直对齐被破坏 | 所有树行统一 `[16px leading slot][gap 8][title]`（DESIGN §5.2 已规定的解剖），文件行补 `icons.File()` 16px 占位 |
| VD-39 | `p2-files.png` 行 `y=126..152`：`检查报告 23:55:42 [预览][下载]` | 两个 32px 描边按钮直接塞进树行，把行高撑到 44+，且按钮抢走行的视觉重心 | 行 hover/focus 时只显示一个 `⋯`（菜单含 预览/下载/复制路径），或行本身可点预览 + 尾部 icon 下载 |
| VD-40 | 顶带 `y=0..42`：4 个 chip + 3 个图标按钮 = 7 个 24–28px 目标 | 40px 高的带子里放 7 个可点目标，右侧 3 个图标间距 8px，密度过高 | 合并：`⋯` 菜单承载不常用项（折叠/检查器/搜索可保留），chip 最多 2 个常显；目标间距 `Space2(8)`→`Space3(12)` |
| VD-41 | `p2-appearance.png` `x=326..1184, y=72..871（外框 858×800）` | 设置面**无边框**（scrim→`#FFFFFF` 直切），而 S4 规范写的是 `BgRaised + BorderL2 + RadiusLG`；产物弹窗却有边框 | 二选一并写进 §5.1：浮层统一 `Border(1, BorderL2)`（推荐，暗色尤其需要），或规范改为"仅阴影"。注意 scrim 实测约 42%（`#999` on white），比 token 的 30% 重，需一并核对 |
| VD-42 | 一屏字号 20/16/14/13/12/11 = **5 档**（设置页、会话页） | §5.3 规定"一屏最多 4 种字号"；12 与 13 只差 1px，层级分不出 | 收敛到 4 档：`20/600`（仅带页标题的屏）· `16/600`（节标题）· `14/22`（正文/列表行/按钮）· `12/18`（元数据）；**13px 并入 12px**（次级靠 `WeightMedium` 而非 +1px 强调），**删掉 11px**（VD-10/11/28 的替代方案），`TypeXXS` 作最低档 |
| VD-43 | `tokens.go:37-56`：`TypeM = 16/24/**400**`，但 §5.3 要求节标题 600 | token 与语义不一致 → 每个调用点必须自己写 `FontWeight(600)`，忘了就变细（`p2-model-settings.png` `模型列表` 与 `p2-about-settings.png` `构建信息` 字重就不同） | 给 `Tokens` 加 `HeadingStyle()`（`TypeM/600/LabelPrimary`）或改 `TypeMetrics(TypeM).weight=600`；designlint 加规则：`Font(TypeM)` 必须成对出现 `WeightSemibold` |
| VD-44 | `p2-model-settings.png` `默认` 徽章（r999）· `p2-capabilities-skills.png` `工作区`（r999）· `HTTP` 药丸 · 附件 chip（r8） | 同类"类型/标签"既用 r999 药丸又用 r8 圆角块 | 定两个词汇：**StatusBadge**（状态，r999，带语义 tint）与 **Tag**（类型/计数，r8，中性 `BgStrong`）；禁止混用 |
| VD-45 | 暗色：全屏只有 cyan 一种强调 + 亮白字；`narrow/dark-session.png` 选中行 `#13333B`、发送 `#3CBBFA`、toast 链接同色 | 暗色里"选中/主操作/链接"三者同色同亮，层级压缩 | 暗色选中行 tint 降 18%→12%，主操作用 `Accent` 满填，链接用 `AccentText` 的 90% 不透明度/下划线区分 |
| VD-46 | 全局：`uikit/IconButton` 与 21 处自绘 `ButtonBase` | 全仓仅 20 处 `Hovered()` 调用；自绘行若无 hover/pressed 分支就"看起来是死的"（AP-11） | designlint 增 `UD-11`：`ButtonBase(` 所在函数必须出现 `Hovered()`/`rowTint(`/`Pressed()` 三者之一，否则 warn；把 `rowTint` 提升为 `uikit` 公共函数（现在在 `sidebar.go:1872`） |

---

## 3. 视觉规则清单（可执行）

### 3.1 边框策略（一句话：**一条缝，一条线，一种卡**）

全 App 只允许四种边框语义，别的都不行：
1. **结构接缝** = 1px `BorderL2`(10% ink)，且只由一处绘制。侧栏/正文、正文/检查器、设置导航/正文、顶带底边全部同一档；**禁止叠加**（当前侧栏是 16%+10% 两笔，检查器 1px，设置 0px —— VD-03/04）。
2. **对象卡** = `Border(1, BorderL2)` + r16，**只给真正的对象**（审批卡、决策卡、产物、对话框）。语义色只能以「2px 左条 + 语义图标 + StatusBadge」出现，**禁止给整张卡染色边框**（当前审批卡 40% 琥珀，VD-08）。
3. **内嵌块**（代码块、diff、内层参数框）= `BgStrong` 填充 + `RadiusSM`，**无边框**；同一块内不得再叠"整块语义洗底"（VD-09）。
4. **分隔靠间距，不靠线**：同一节内的段落/列表项用 `Space3/Space4` 分隔；线只用于"跨语义组"（表格行、面板边）。设置页每个小节标题下**不再画规则线**（当前有无不一，VD-14）。

### 3.2 间距节奏（一句话：**4 的倍数 + 三档呼吸**）

- 所有内外边距只取 `Space1…Space8`；实测已基本合规，问题在**用法漂移**，不在数值：小节上下 24/12、列表行 12/12、卡片内 16、composer 12。
- 定三档呼吸，全 App 复用：**紧** `Space1-2`（行内元素/图标与文字）· **中** `Space3-4`（块内、行内、卡内）· **松** `Space5-6`（节与节、页头与首行）。
- 三条硬规则：① 同一列表内行距只能有一种（当前模型/记忆列表 12px 无分隔 → 归属不清，VD-15）；② 段落与段落之间必须 ≥ `Space2`，且正文行高 ≥1.55×（当前 13/20 = 1.54 但无段距，VD-19）；③ 卡内边距四边相等（当前审批卡、代码块头部出现 16/4/0 的离散步进，VD-28）。
- 页头→首行 `Space5(20)`，节标题→内容 `Space3(12)`，表格行高 36（双行 44）。

### 3.3 圆角（一句话：**三档，父子差 ≤2**）

- 只保留 **8 / 10 / 16** 三档 + 药丸（`RadiusMax` 仅 `StatusBadge`）：
  - `RadiusSM 8`：chip、tag、菜单行、分段段、小内嵌块；
  - `RadiusMD 10`：按钮、输入框、搜索、图标按钮（方形）；
  - `RadiusLG 16`：卡、气泡、对话框、toast、composer。
- **撤掉 `RadiusXL 20`**（composer 与内容不协调，VD-26）；`RadiusXS 4` 只用于内嵌小方块（代码块、行号）。
- 父子差 ≤2（16→14 内衬 / 10→8 / 8→6）；禁止"胶囊内再放胶囊"（当前分段控件 = 轨 r10 + 段 r8 + 底边 accent 断线，VD-12）。
- 状态与类型标签不再用两套圆角（药丸 vs 8 圆角混用，VD-44）。

### 3.4 字号层级（一句话：**四档 + 权重承担次级**）

- 一屏只用 4 档：**20/600**（页面标题，仅设置页/对话框）· **16/600**（节标题）· **14/22**（正文/消息/菜单/按钮）· **12/18**（元数据/标签/徽章）。
- **删除 11px 成句**（状态条、消息时间/用量、code fence 头部、复制按钮 —— VD-10/11/28）；11px 仅保留 kbd 与终端 gutter，且必须是"≤3 字符"的记号。
- 13px 与 12px 只差 1px 却同时出现在相邻层级（列表次文本 vs 时间戳）→ 合并成 12px；需要强调用 `WeightMedium` 而不是加 1px。带页标题的设置页/对话框可用满 4 档（20/16/14/12），会话页与检查器用 16/14/12 三档即可。
- 次级文字**不要**继续调灰：实测 `LabelSecondary 6.74:1`、`LabelTertiary 6.34:1` 已经达标，问题在字号与字重；如需"更弱"，用 12px/400 + 更小基线，不要再降对比度（否则破 AA）。
- 元数据永不用 accent；标题不用 accent 字（用 `LabelPrimary/600`）。

---

## 4. 风险与取舍

| 决策 | 收益 | 风险 / 代价 | 建议 |
|---|---|---|---|
| 页面改灰底（`BgBase #FBFCFD`，卡白） | 1px 线才有意义，卡片层级成立，能减少大量装饰线 | 全 App 的对比度重算（暗色不用动）；`#FBFCFD` 与 `#F9FAFB` 太近，侧栏需再深一格到 `#F4F6F8` | 采纳；先在 settings 面灰度验证（1 张 PNG 就能判断） |
| 消息气泡去边框改填充 | 明显更像"聊天"，与输入框区分 | 需要同时处理"看这张图/文件 chip"的词汇，否则又有两个白块 | 采纳；气泡 `BgStrong`、产物按钮 `BgRaised+BorderL2+RadiusMD(32 高)` |
| 导航图标去蓝 | 强调色回归"动作"，主按钮重新唯一 | 品牌蓝在导航上消失，产品"蓝味"变淡 | 采纳；保留"当前目的地"唯一一个 accent 图标做品牌落点 |
| 分段控件去 accent 下划线 | 去掉"断线"视觉 bug | 选中与轨道对比只有 1.15:1（代码注释担心的正是这个） | 采纳"抬升块"方案（白块 + 1px 边 + 600 字重），对比由**边框 + 字重**承担，不再依赖色差 |
| 状态条从 6 项裁到 3 项 | 11px 问题消失、信息可读 | 用户可能觉得"信息变少"（AGENTS §3.17） | 不删数据：3 项常显 + 其余进 tooltip（`Exact` 已实现），符合"不因减法隐藏状态信息" |
| toast 改到底部 | 不再遮挡正文 | 与 composer 右下按钮区竞争（发送附近） | 采纳；`Bottom = Space8` 悬在 status strip 之上、`Right = Space6`，并限制 1 张 |

---

## 5. 附：证据清单

### 5.1 看的图（41 整屏）

`p1-chat`、`p1-approval`、`p1-trajectory`、`p2-appearance`、`p2-behavior`、`p2-about-settings`、`p2-files`、`p2-file-preview`、`p2-file-diff`、`p2-context`、`p2-context-sections`、`p2-memory-settings`、`p2-model-settings`、`p2-capabilities-skills`、`p2-capabilities-connectors`、`p2-automation`、`p2-archive-settings`、`p2-ask`、`p2-attachments`、`p2-artifact`、`p2-drop`、`p2-interrupted`、`p2-goal`、`p2-goal-blocked`、`p2-palette`、`p2-team-overlay`、`p2-status-strip`、`p2-tool-row-states`、`p2-landing`、`p2-insights`、`p2-markdown-specimen`、`p2-modelmenu`、`p2-approval-diff`、`p2-context-dark`、`p2-approval-diff-dark`、`p2-capabilities-skills-dark`、`narrow/narrow-session`、`narrow/narrow-settings`、`narrow/narrow-session-inspector`、`narrow/narrow-landing`、`narrow/dark-session`。

### 5.2 裁剪（`.cache/ui-conformance-audit/crops/`，3.5×–9×）

共 17 张：`chat-top`（侧栏接缝/tab 下划线）、`chat-composer`、`codeblock`（5× 看 11px 与左缘对齐）、`app-search`、`app-seg`、`app-seg-sel`（7× 看断线）、`app-switches`（6× 看开关）、`beh-switch`、`beh-sect`、`rail`（5× 对比 rail 与展开侧栏图标墨色）、`narrow-hdr`（4× P0 溢出）、`appr-seam`/`appr-topright`（9× 看琥珀边与头块圆角）、`appr-bottom`、`meta-row`、`statusline`、`nav`。

### 5.3 采样命令（可复现）

```bash
cd /data/mygo/.cache/ui-conformance
# 任意一点的色值
convert p1-chat.png -crop 1x1+279+500 txt:- | tail -1
# 一行/一列的颜色游程（找边框与接缝）
convert p1-chat.png -crop 1512x1+0+500 +repage txt:- | tail -n +2 \
  | grep -o '#[0-9A-F]\{6\}' | awk '{if($0!=p){print NR-1, $0; p=$0}}'
# 3.5x–9x 裁剪（point 过滤，保留像素）
convert p1-chat.png -crop 210x70+428+344 +repage -filter point -resize 500% out.png
```

### 5.4 代码回查（本报告引用的行号）

`internal/design/tokens.go:37-56`（字号档）、`internal/design/components.go:72,127-131,143-149,186-191,207-211,224-229`（布局/面板/composer/chip/按钮/徽章）、`internal/brand/palette.go:145,160-162,195-201`（浅色 Surface/Border、暗色表面）、`internal/uiroot/frame.go:168-171,802-812,1836`（侧栏 BorderL3、顶带 BorderL2、设置面尺寸）、`internal/uikit/composer.go:151-284`（composer 内距与焦点环）、`internal/uikit/segmented.go:59-94`、`internal/uikit/approval.go:198,219-221`、`internal/uikit/diffpreview.go:158-159`、`internal/uikit/markdown_code.go:32-49`、`internal/uikit/runstatus.go:412`、`internal/ui/session/view.go:1357-1369,2147,2310,4242-4290`、`internal/ui/toasts/toasts.go:60-78,369-392,440-460`、`internal/ui/sidebar/sidebar.go:468,1848-1872`、`internal/ui/rightpanel/view.go:98,136`、mygo `ui/sidebar.go:184`。

### 5.5 未覆盖 / 限制

- 悬停与按下**只能静态推断**：快照是静态帧，hover/pressed 需 `ui-drive` 步骤或帧测试才能出图；本报告对 hover 的判断基于代码（`Hovered()`/`rowStyle`）与对比度计算，已在 VD-27/VD-46 标注。
- 未重新渲染任何画面（未跑 `ui-snapshot`），因此不排除个别状态（如 hover、focus-within、disabled 的其它控件）与基线不同。
- `p2-english*` 三张、`p2-jobs*`、`p2-sidebar-*`、`p2-team-tasks*`、`p2-fork`、`p2-cold-resume*` 未逐张展开（不在指定清单内），同类问题（接缝/字号/列表分隔）应同样存在。