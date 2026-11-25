# 外壳（Sidebar / TopBand / 全局浮层 / 响应式）审计

- 覆盖：`/`、`/inbox`、`/c/:id`（完成态 / 待审批态）、`/settings/model`、`/settings/appearance`、`/team`、`/capabilities`；折叠态（Mod+B，有数据/无数据两种）、命令面板打开态、skip-link 聚焦态、Tab 焦点走查、会话悬停、11 条长列表、深色（OS 深色 + `设置→外观→主题` 两种路径）、375/390/768/1024/1280/1440 五档视口、en 语言。
- 截图：49 张（`shots/shell-*.png`）；量测脚本 `shell-measure.mjs` / `shell-probe.mjs` / `shell-probe3.mjs` / `shell-probe5.mjs`，原始数字落盘 `shell-probeC.log` / `shell-probeE.log` / `shell-lang.log`。
- 基线：9860（只读，仅导航+量测，未点审批/未新建）；交互实验全部在 lead 分配的 9871 实例（可写，最后已恢复中文 + 清掉侧栏宽度 localStorage）。

## 结论

外壳的**几何骨架是对的**（264/64 两态、拖宽持久化、Mod+B、Splitter 状态保持、Band 48 + Tabs 38 都实测成立），但**四类系统性缺陷**让它在真实使用中站不住：① 主题设置只驱动 antd、不驱动 `--zk-*` CSS 变量 → 在浅色系统上选"深色"得到**白底白字**（P0）；② 390px 下侧栏仍固定占 200px，内容列只剩 126px，审批按钮重叠、内容横向裁掉不可达（P0）；③ 会话列表无 `tabindex`/`role`，键盘用户**完全无法切换会话**（P0）；④ 会话行与导航行是两套选中语言、两套内距、两条竖线，且 `SessionSummary` 里的 `running/waiting/unread/updatedAt` 全被丢弃 → 侧栏退化成"标题清单"。

## 发现

### SH-01 主题=深色在浅色系统上得到"白底白字 + 三色割裂" [P0]
- 现象：`设置 → 外观 → 主题 = 深色` 保存成功（提示"已保存"，刷新后仍为深色）后，antd 组件进入暗色，但侧栏底色、标签色仍取浅色 token：品牌名、新建会话、全部会话标题变成 **#f7f8fa 上的白字**（几乎不可见）；导航菜单块是 #141414 的深色矩形，顶栏一条黑带，正文白底 —— 一屏三套表面。
- 证据：`shots/shell-dark-setting-inbox-1440.png`（品牌/会话标题全白不可见、菜单块深黑矩形、顶栏黑带）；`shots/shell-dark-setting-thread-1440.png`、`shots/shell-dark-setting-settings-1440.png`；反向同样成立：OS 深色 + 主题=浅色 → 全站仍是深色（`shots/shell-light-setting-osdark-inbox-1440.png`）。量测：`.zk-sidebar` bg=rgb(247,248,250)、`--zk-bg-sidebar`=#f7f8fa、`--zk-label-primary`=#0f1115，而同页 `.zk-sidebar__nav .ant-menu-item` color=rgba(255,255,255,0.85)、`.ant-menu` bg=rgb(20,20,20)（对比度 ≈1.05:1）。
- 原因（代码）：`web/src/design/tokens.css:75` 的暗色块只挂在 `@media (prefers-color-scheme: dark)`（生成器 `web/scripts/gen-tokens.mjs:222` 同样只产生 media 查询）；`web/src/app.tsx:30,41-46` 只把 `appearance` 交给 `ConfigProvider`，**从不写 `html[data-theme]`/class**；`web/src/shell/app-shell.tsx:43,111-115` 只影响 `Layout.Sider` 的 `theme`。即 `useSystemAppearance()`（`design/use-system-appearance.ts:16-35`）做出的"设置优先"决策没有传到 CSS 层。
- 规范：WEB-CLIENT §9.1（颜色单一来源、两套变量 light/dark）、§9.3（组件视觉必须能被 tokens.css 单独决定）；DESIGN.md §5.1（S0–S4 表面 ramp，两主题同向）、AP-23（暗色侧栏比页面浅 = 层级隐喻失效）；`use-system-appearance.ts:6-15` 自己的注释就写着"CSS token 块与 antd 算法必须读同一个值"。
- 建议：`gen-tokens.mjs` 同时产出 `[data-theme='light']` / `[data-theme='dark']` 两块（保留 media 块作 `system` 兜底），并在 `App` 里 `useEffect(() => { document.documentElement.dataset.theme = appearance })`；`global.css:8` 的 `color-scheme: light dark` 改成随 `data-theme` 切换。修完补一条 vitest：切到 dark 后断言 `.zk-sidebar` 的 `getComputedStyle().backgroundColor === --zk-bg-sidebar` 实际值。

### SH-02 390px 视口：侧栏固定 200px，内容列 126px，审批按钮重叠、内容不可达 [P0]
- 现象：手机宽度下侧栏仍是常驻面板（200px），内容列只剩 126px：顶栏标题被压成 1–2 字 + "…"；`ConnectionBadge` 竖排成"正在连/接网/关…"三行；会话子标签溢出被裁（只看到"对话"和一个"…"）；审批卡的"拒绝 / 允许一次"两个按钮换行后**叠在卡片正文上**；`/` 与 `/c/:id` 正文按单字换行。设置页两级导航吃掉整屏，表单完全不可见。
- 证据：`shots/shell-thread-data-390.png`（标题 94px 宽、tabs 222px 溢出）、`shots/shell-root-390.png`（逐字换行）、`shots/shell-inbox-390.png`（审批按钮重叠/卡片被裁）、`shots/shell-settings-model-390.png`（表单不可见）、`shots/shell-team-390.png`；量测（9860 与 9871 一致）：`.zk-sidebar`=200×844，`.zk-column`/`#zk-main` x=200 w=126，`#zk-main.scrollWidth`=322（9860）/341（9871）而 `clientWidth`=126，`documentElement.scrollWidth`=390 → 溢出部分既不能滚动也不可达。`.zk-topband .ant-tabs-nav-list` w=222–339，父容器 `overflow-x: hidden`。
- 原因：`global.css:1206-1240` 只把面板从 264 硬改成 232（≤1024）/200（≤900），**没有 390–768 档的抽屉/rail 方案**；`app-shell.tsx:111-115` 的 `Layout.Sider` 未传 `breakpoint`/`collapsedWidth`/`onBreakpoint`（antd 6 全都支持，见下），折叠只能靠 Mod+B（移动端无键盘）。DESIGN.md:596 的窄窗规则要求"放不下 `ContentMinWidth 520` 时侧栏临时渲染为 `SidebarRailWidth 64`"，此处连 `ContentMinWidth 520` 都只剩 126。
- 规范：DESIGN.md §5.4 布局 token（`ContentMinWidth 520`、`SidebarRailWidth 64`）+ 窄窗规则；WEB-CLIENT §8.1 P0 清单（移动端可用）；主流做法：ChatGPT / Claude / Cursor 在 <768px 一律把侧栏变成 hamburger + 抽屉（关闭后内容占满 100% 宽），从不把侧栏与正文并排挤在 390px 里。
- 建议：`Layout.Sider` 传 `breakpoint="lg" collapsedWidth={0} onBreakpoint`；`<900px` 时侧栏改 `position:fixed; z-index` 抽屉 + 顶栏出现一个 44×44 的汉堡按钮（`aria-expanded`、`aria-controls`），抽屉打开时垫 scrim；同时给 `#zk-main` 加 `min-width: 0` 与 `overflow-x: hidden`，审批卡动作区在 <480px 用 `flex-wrap` + 全宽按钮（现在只有 `.zk-approval__actions` 有 wrap，按钮本身仍并排）。

### SH-03 会话列表键盘不可达：Tab 序里没有一条会话 [P0]
- 现象：从页面加载起按 Tab，焦点序列是 `skip-link → 新建会话 → 整个导航 UL（一个 stop）→ body → 回到 skip-link`，**永远不会落到会话项**，也不能用 Tab 打开某个会话；会话项既无 `tabindex` 也无 `role`/`aria-current`。
- 证据：`shell-measure.mjs` 的 `TABORDER`（14 次 Tab，循环只出现 4 个节点；`shots/shell-focus-tab-1440.png`）；`convRoles` = 3 条全是 `{role:null, ariaCurrent:null, ariaSelected:null, tabIndex:null}`；`menuRoles` = `role=menuitem, tabIndex=-1`（导航本身合规，整块一个 stop）。代码：`shell/sidebar.tsx:72-77` 只给 `Conversations` 传 `items/activeKey/onActiveChange`。
- 规范：WEB-CLIENT §5.3 规则 2/3（区域顺序 侧栏→顶栏→主列；**每组一个 Tab stop，列表用 roving tabindex**）；§5.6 验收第 1 条（"不碰鼠标可完成…切换会话"）；§5.5（集合行必须有可读名称）；WCAG 2.1.1。
- 建议：给 `Conversations` 的 item 包一层可聚焦容器或改用 `role="option"` + `aria-selected` + roving `tabIndex`（容器 `role="listbox"`，↑↓/Home/End 移动、Enter 打开）；antd X 未内建键盘导航，可在 `Sidebar` 里包一个 `onKeyDown` 处理 + `activeKey` 同步焦点。验收：Tab 一次进列表、方向键可切、Enter 打开。

### SH-04 顶栏 16px 与正文 0/24px：一屏三套左缘（280 / 264 / 288） [P1]
- 现象：同一个左缘出现了三个 x：顶栏标题 x=280、`/`·`/inbox`·`/settings` 的正文 x=**264**（贴着侧栏接缝，零内距）、`/team`·`/capabilities`·`/insights`·`/automation` 的正文 x=**288**。8–16px 的错位肉眼可见（`/team` 上"团队"标题与页面 h2 不对齐）。
- 证据：`shots/shell-team-1440.png`（顶栏"团队"x=280，页内 h2 x=288）、`shots/shell-inbox-1440.png`（"1 项等待你" x=264，直接压着接缝）、`shots/shell-team-gutter-1440.png`；`shell-probeE.log` 的 `GUTTERS`：`/`、`/inbox`、`/settings/model` → `firstChild.x=264, padding:0px`；`/team`、`/capabilities`、`/insights`、`/automation` → `.zk-screen{padding:24px}` → 内层 x=288；`titleX` 恒为 280。代码：`global.css:110-113`（`.zk-topband{padding:0 var(--zk-space-4)}` = 16px）vs 各页自带的 0/24。
- 规范：WEB-CLIENT §9 视觉一致性（同一 shell 的左缘必须唯一）；主流做法：VS Code 把侧栏内容、编辑器与标题栏统一到同一 16px 内距网格，ChatGPT 的会话区与顶栏标题同一条 24px 竖线。
- 建议：在 `.zk-column` 上定义唯一的 `--zk-column-gutter: var(--zk-space-6)`，`.zk-topband` 与 `.zk-content > *` 都读它；`/inbox`、`/settings` 去掉"0 内距 + 各自内距"的写法，改由 shell 提供 gutter，页面内只留组件间距。

### SH-05 侧栏两套选中语言，且两套都不合 DESIGN 的会话行解剖 [P1]
- 现象：导航选中 = antd 蓝底 + 蓝字药丸（bg rgb(230,240,255)、color rgb(13,68,246)、radius 8、**无左侧竖条**）；会话选中 = 中性灰底（bg rgba(0,0,0,0.06)，深色下 rgba(255,255,255,0.12)）而文字色不变、**也无竖条**。同一侧栏里"我在哪个页面"是蓝的，"我在哪个会话"是灰的，两者权重完全不对等。
- 证据：`shots/shell-thread-loaded-1440.png`、`shots/shell-palette-1440.png`（会话选中灰底，导航选中蓝底同屏）、`shots/shell-dark-osdark-thread-1440.png`（深色下同样是灰底会话 / 蓝底导航）；量测 `navSelected={bg:rgb(230,240,255), color:rgb(13,68,246)}`、`convActive={bg:rgba(0,0,0,0.06), color:rgba(0,0,0,0.88)}`；代码：选中色全部来自 antd 默认（`shell/sidebar-nav.tsx:27-33` 的 `Menu` 无 className 覆盖，`shell/sidebar.tsx:72-77` 的 `Conversations` 无 `styles`/`classNames`）。
- 规范：DESIGN.md §1.3 规则 9（持久导航当前项 = `BgSelected` + 2px `Accent` 左条 + `LabelPrimary`；`BgSelected` 亮 12%/暗 18%）；WEB-CLIENT §9.4（"持久导航当前项 = BgSelected + 2px Accent 左条"）；DESIGN.md:557（会话行解剖 `[选中 2px 左条][16px leading slot][gap][标题][状态]`）。DSH 用**一套**中性语言：`--dsw-specific-sidebar-nav-item-active: neutral-bluish-100`（`design-platform.css:322,473`）、会话行 `.panelEntryActive { background: var(--dsw-alias-interactive-bg-hover) }`（`SidebarRoot.module.css:655-660`）。
- 建议：导航与会话统一到 `--zk-bg-selected`；会话行加 `::before` 2px 左条（`--zk-accent`，选中才显示）+ 标题 `font-weight 500`；`Menu` 用 `styles={{ itemSelectedBg: 'var(--zk-bg-selected)', itemSelectedColor: 'var(--zk-label-primary)' }}` 收敛到 token，去掉 antd 的蓝字。

### SH-06 图标列与标题列不在同一条竖线；两级左右内距不一致 [P1]
- 现象：导航图标起点 x=32，导航文字起点 x=58；会话标题起点 x=**28** —— 与图标差 4px、与导航文字差 30px，一列图标、两列文字，没有一条竖直对齐参考线。左右内距也是两套：导航行 x=8 w=246（左 8 / 右 9），会话行 x=20 w=223（左 20 / 右 20）。
- 证据：`shots/shell-inbox-1440.png`（图标列 32 vs 标题列 28 可直接看出错位）、`shots/shell-session-hover-1440.png`；量测：`.ant-menu-item{x:8,w:246,padding:0 16px 0 24px}`、`iconX=32,w=16`、`labelX=58`；`.ant-conversations-item{x:20,w:223,padding:0 8px}`、`label.x=28`；`.ant-conversations{x:8,w:247,padding:12px}` 套在 `.zk-sidebar__list{padding:8px}` 里（8+12=20，两层内距叠加）。
- 规范：DESIGN.md:557（`[左条][16px leading slot][gap][标题]`，即所有行应有同一个 leading slot 起点）；WEB-CLIENT §9.4 token 纪律；主流做法：VS Code 侧栏所有行共用同一 8px 行内距 + 同一 leading 图标槽（16px + 6px gap），Cursor/Claude 的会话行与导航行左缘完全同线。
- 建议：删掉 `.ant-conversations` 的 12px 自内距（`styles={{ root: { padding: 0 } }}`），让会话行继承 `.zk-sidebar__list` 的 8px；给导航菜单项设 `padding-inline: 8px`（去掉 antd 的 24px 左内距），两类行都用 `padding-inline-start: var(--zk-space-2)` + 16px 图标槽 + 8px gap → 图标 x=16、文字 x=40 全场一致。

### SH-07 会话行缺分组／时间／未读／运行态／悬停操作 —— 数据在协议里有，被侧栏丢掉 [P1]
- 现象：会话列表是一列纯标题（超长截断、同一行高 40）；没有分节标题、没有时间戳、没有未读点、没有运行/等待状态、hover 时没有 ⋯ 菜单（重命名/删除/归档都无入口）；11 条会话时全是 "Untitled session" 完全无法区分。
- 证据：`shots/shell-long-session-list-1440.png`（11 条同名、`groups: []`）、`shots/shell-session-hover-1440.png`（hover 只有底色变化，无任何操作按钮，`convHasMenuButton: false`）、`shots/shell-inbox-1440.png`；代码：`shell/sidebar.tsx:29-32` 把 `SessionSummary` 映射成 `{key, label}` —— 而 `api/gateway.ts:45-57` 里 `running/waiting/unread/archived/pinned/updatedAt/messageCount` 全都有；`Conversations` 的 `groupable`、`menu`、`icon`、`creation` 一个都没用（`node_modules/@ant-design/x/es/conversations/index.d.ts` 明确支持 `menu?: ConversationsItemProps['menu']`（hover ⋯）、`groupable?: boolean | GroupableProps`、`item.icon`、`shortcutKeys`）。
- 规范：DESIGN.md:546-557（行首运行 glyph、行尾状态词/8px Accent 未读点、hover 显现的 ⋯，数据来自 `Running/Waiting/Unread`，不伪造）；DESIGN.md:617（侧栏小节标题 = `SectionLabel()`，TypeXXXS + LabelTertiary）；antd X `Conversations` 的 `groupable`+`GroupTitle` 与 `menu` 就是这两件事的官方件；主流做法：ChatGPT 会话列按 Today/Yesterday/Previous 7 days 分组 + hover ⋯（重命名/删除）+ 未读粗体；Claude/Cursor 同构。
- 建议：`items` 里补 `group`（今天/昨天/更早/已归档，由 `updatedAt` 算）、`icon`（running→`clock`、waiting→`clock`+状态词、unread→8px accent 点）、`menu={{items:[重命名,归档,删除]}}`；`<Conversations groupable>` 直接得到分组标题；无标题会话用 `t('shell.sessions.untitled')` + 首条消息摘要而不是统一的 "Untitled session"。

### SH-08 折叠态没有任何展开入口；rail 用 10px 圆点当按钮 [P1]
- 现象：Mod+B 折叠后只剩 64px 图标轨：品牌消失、`+` 变成 24px 图标、下方是几个 10×10 的圆点代表会话。**页面上没有展开按钮**（`Layout.Sider` 没传 `collapsible`/`trigger`，Splitter 面板此时 `min=max=64`），鼠标用户折叠后无法恢复；圆点命中区 10×10（远小于 24×24），且只有 hover 1 秒后的原生 title 才知道是哪个会话；选中会话另有一个几乎看不见的 2px 描边。
- 证据：`shots/shell-collapsed-dots-1440.png`（两个 10px 点，未选中那个是 rgba(15,17,21,0.06) 几乎不可见）、`shots/shell-collapsed-tooltip-1440.png`（导航 tooltip 正常）、`shots/shell-collapsed-1440.png`（9860 无数据时"正在加载…"**逐字竖排**成 5 行）；量测：`.zk-sidebar__rail-dot` → `{x:26.5, w:10, h:10, padding:0, border:0, tabIndex:null}`（`tabIndexes: [null,null]` 说明连键盘也进不去）；`activeDot` outline = rgba(13,68,246,0.09) 2px（几乎不可见）。代码：`global.css:1286-1301`、`sidebar.tsx:55-70`、`app-shell.tsx:104-115`。
- 规范：DESIGN.md AP-17（图标/点状控件 ≥24×24 命中区）；WEB-CLIENT §5.5（图标按钮必须有 aria-label —— dot 只有 aria-label 无焦点）；DSH 对照：折叠 rail 的图标按钮是 **36×36** + `--dsw-specific-sidebar-glass-collapsed-hover` hover 底（`SidebarRoot.module.css:695-700`），并且**logo 位置本身就是展开开关**（hover 时 whale mark 换成 panel 图标，代码注释 "the expand affordance"）——ZeoKeel 既没有这个 affordance，也没有 hover 底。
- 建议：header 左侧加一个 28×28 `PanelLeft` 图标按钮（`aria-label="展开侧栏"`/`aria-expanded`，Mod+B 同源 handler），折叠态放在 64px 轨顶部正中；rail dot 改成 32×32 按钮（内部 8px 点 + 命中区撑到 32），未选中点用 `--zk-border-l3` 而不是 `--zk-border-l1`；`zk-sidebar__empty` 在折叠态 `writing-mode` 改回横排或直接隐藏（至少 `white-space: nowrap; text-overflow: ellipsis`）。

### SH-09 加载态：2.9s 内顶栏显示通用标题、侧栏只有一行纯文本；折叠后变成竖排 [P1]
- 现象：硬刷新 `/c/:id` 后约 **2.9s** 内：顶栏标题显示通用"会话"（不是骨架、也不是会话名），侧栏会话区只有一行"正在加载…"纯文本（无 skeleton），会话项数量 0→2；这段时间里子标签已经渲染、正文只有 composer。折叠态下这行文字被 64px 宽度挤成逐字换行（见 SH-08 截图）。空态与错误态共用同一个 `.zk-sidebar__empty` 容器，文案会挨着状态提示堆叠。
- 证据：`shell-probeC.log` 的 TIMELINE（`{ms:2922, h1:"帮我确认一下网关…", convItems:2}` 而前一刻 `{h1:"会话", convItems:0, loadingText:"正在加载…"}`）；`shots/shell-thread-1440.png`（9860 抓到的正是该状态：标题"会话"、侧栏"正在加载…"、正文 spinner）；`shots/shell-collapsed-1440.png`（竖排）。
- 规范：DESIGN.md §2 状态语义（加载是状态，不是文案）、`running ≠ warning`；主流做法：ChatGPT/Claude 首屏用 3–5 行 skeleton 行（同高 40px、同内距）而不是一行灰字，且标题位置先用灰条占位，避免"会话"→真标题的跳变。
- 建议：`Sidebar` 的 loading 分支改为 3 条 `<Skeleton.Input size="small" block />`（行高对齐 40px 会话行）；`TopBand` 在 `sessionId && !session` 时渲染 120px 宽骨架条而不是 `t('thread.untitled')`；`.zk-sidebar__empty` 加 `overflow-wrap: normal`。

### SH-10 `Layout.Sider` 的 breakpoint 从未生效；264 不是 token 值 [P1]
- 现象：任务书问"`Layout.Sider` 的 breakpoint 是否真的生效" —— **不生效，因为根本没传**。侧栏宽度由 `Splitter.Panel` + 两条 `!important` 媒体查询决定（1440/1280→264，1024→232，900/768/390→200），`Sider` 只负责画表面（`width="100%"`）。
- 证据：`shell/sweep-inbox-{1440,1280,1024,768,390}.png` + 量测 `{1440:264, 1280:264, 1024:232, 768:200, 390:200}`，`panelFlex` 在 ≤1024 变成 `0 0 232px`（`!important` 生效）；DOM 上 `.ant-layout-sider` 类只有 `ant-layout-sider ant-layout-sider-light zk-sidebar-sider`，无 `ant-layout-sider-collapsed`/`ant-layout-sider-zero-width`。代码：`app-shell.tsx:111-115` 只传 `width`/`theme`/`className`；`global.css:1206-1240`。antd 6 的 `Layout` 明确支持 `breakpoint`（`xs…xxl`）、`collapsedWidth`（默认 80）、`onBreakpoint`、`collapsible`、`trigger`（`bash scripts/antd-cli.sh info Layout --version 6` 输出）。
- 规范：`docs/plans/antd-x-component-map.md:75`「折叠栏 S-03（64px）→ `Layout.Sider collapsedWidth`」——设计文档就是这么要求的，实现绕过了；DESIGN.md:596 布局 token 里 `SidebarWidth 280`，而 `app-shell.tsx:16` 硬编码 `SIDEBAR_DEFAULT = 264`（且 `tokens.css` 里没有对应的 `--zk-sidebar-width`）。
- 建议：把宽度决策交给 `Layout.Sider`（`breakpoint="lg" collapsedWidth={64}`），删掉 `global.css:1206-1240` 的 `!important` 覆盖；默认宽度从 token 取值（建议同时把 `SidebarWidth` 对齐到设计文档的 280，或把文档改成 264 —— 二者取一，别留两个数）。

### SH-11 侧栏没有可读名「侧栏」；badge 的 `aria-label` 被丢掉 [P1]
- 现象：`<aside>`（`.ant-layout-sider`）无 `aria-label`，`role` 靠 HTML 隐式（complementary）但**没有可读名**；导航徽标只留下 `title="待审批 1"`，`aria-label` 为 `null`，于是菜单项的可读名变成 "收件箱1"（没有"待审批"语义、也没有空格）。
- 证据：`shell-probe.mjs` 的 `PROBE_THREAD`：`{asideTag:"ASIDE", asideRole:null, asideLabel:null, sidebarRole:null}`；`BADGE_LIVE`：`{text:"1", title:"1 waiting", ariaLabel:null, rowText:"Inbox1", rowInnerText:"Inbox\n1"}`；`shots/shell-badge-live-1440.png`。代码：`sidebar.tsx:35`（`.zk-sidebar` 无 role/label）、`sidebar-nav.tsx:42-47`（`Badge` 同时传了 `title` 和 `aria-label`，antd 只把 `title` 落到 `sup` 上）。
- 规范：WEB-CLIENT §5.5 ARIA 表（"侧栏 `role="complementary"` + 可读名「侧栏」"；"图标按钮必须有 `aria-label`"；"集合行每行有可读名称"）；`docs/DESIGN.md:542`（行内状态不拼 badge，用状态词）。
- 建议：`<Layout.Sider aria-label="侧栏">` 或给 `.zk-sidebar` 加 `role="complementary" aria-label={t('shell.rail')}`；badge 的可读名放到菜单项自己的 `aria-label`（`${label}，${count} 项待审批}`）或用 `title` + 视觉隐藏文本 `<span className="zk-sr-only">待审批 1</span>`。

### SH-12 焦点环两套语言：antd 3px #87afff vs `--zk-focus` 2px [P1]
- 现象：skip-link、自定义按钮用全局 `:focus-visible{outline:2px solid var(--zk-focus)}`；一进入 antd 的按钮/菜单/会话，焦点环变成 **3px rgb(135,175,255)、offset 1px**。会话行则完全没有焦点环（不可聚焦，见 SH-03）。
- 证据：`shell-measure.mjs` 的 TABORDER：`zk-skip-link → outline rgba(13,68,246,0.8) solid 2px / offset 2px`；`新建会话 → outline rgb(135,175,255) solid 3px / offset 1px`；`Menu UL → 同上 3px`；`shots/shell-skip-link-focused-1440.png`、`shots/shell-focus-tab-1440.png`（新建会话上的淡蓝圆角环）。
- 规范：WEB-CLIENT §5.6（"每个可交互元素有可见焦点环（token `Focus` 色）"）；§9.3（不允许库内硬编码颜色作为最终呈现）；DSH 对照：统一用 `--dsw-focus-ring-width` + `--dsw-focus-ring-color`，且在自己容器里 `outline-offset: -2px` 防裁切（`SidebarRoot.module.css:347-350,696-699`）。
- 建议：`ConfigProvider` 里加 `token: { controlOutline: 'var(--zk-focus)', lineWidthFocus: 2 }`（antd 6 的焦点 token），并把 `global.css:26-30` 的 `:focus-visible` 与之一致；给会话行容器补 `:focus-visible` 环。

### SH-13 `<html lang>` 恒为 zh-CN：英文界面仍声明中文 [P1]
- 现象：切到 `语言=英文`（导航 Inbox/Capabilities/Automation/Insights/Team/Settings、新建按钮 New session）后，`document.documentElement.lang` 仍是 **"zh-CN"**；同时 skip-link 文案仍是中文（只在聚焦时出现，影响小但同一缺陷）。
- 证据：`shots/shell-english-inbox-1440.png`（英文导航）+ `EN_STATE {lang:"zh-CN"}`；代码：`web/index.html:2` 硬编码 `<html lang="zh-CN">`，全仓库无 `documentElement.lang` 赋值；`app-shell.tsx:90-92` 的「跳到主内容」是硬编码字面量，未走 `t()`。
- 规范：WEB-CLIENT §5.5/§5.6（可访问性最小集）；i18n 基本要求（`lang` 决定读屏语音与断词）；WCAG 3.1.1。
- 建议：`I18nProvider` 里 `useEffect(() => { document.documentElement.lang = locale === 'en' ? 'en' : 'zh-CN' }, [locale])`；skip-link 改 `t('shell.skipToContent')`。

### SH-14 导航菜单块自带不透明底色，在侧栏上画出一个矩形接缝 [P2]
- 现象：导航是 `#ffffff`（深色 `#141414`）的一个矩形块，压在侧栏底色 `#f7f8fa`（深色 `#0b0b10`）上，从 y=56 到 y=320 有一条明显的"贴片"边界，浅色下像一张白卡片浮在灰侧栏上，深色下更像。
- 证据：`shots/shell-inbox-1440.png`（导航白块与灰侧栏的分界）、`shots/shell-dark-osdark-thread-1440.png`（#141414 块在 #0b0b10 上，矩形边缘清晰）；量测 `menuRootBg=rgb(255,255,255)` / `sidebarBg=rgb(247,248,250)`；`menuRoot{w:255,radius:0}`。代码：`sidebar-nav.tsx:27` 未覆盖 Menu 背景。
- 规范：DESIGN.md §5.1 表面 ramp（S1 侧栏是一整块下沉面，导航不应自成一层）；DSH 侧栏导航没有独立底（`--dsw-specific-sidebar-fill` 铺满）。
- 建议：`.zk-sidebar__nav .ant-menu { background: transparent; border-inline-end: 0; }`（深色同理）。

### SH-15 badge 红与 tab 蓝都没走 token：同一屏出现两套色值 [P2]
- 现象：待审批 badge 用 antd 的 `#ff4d4f`（`rgb(255,77,79)`，深色 `rgb(220,68,70)`），而 tokens 定义的 `--zk-danger` 是 `#dc2626`；深色下激活 tab 的下划线/文字是 `rgb(54,162,216)`（#36a2d8），而 `--zk-accent` 深色值是 `#3cbbfa`。
- 证据：`BADGE_LIVE {bg:"rgb(255,77,79)"}`；`PROBE_THREAD {activeTabColor: rgb(54,162,216), tabInk.bg: rgb(54,162,216)}`；`tokens.css:32,90`。代码：`config` 只设了 `colorPrimary`（`design/theme.ts:14-24`），`colorError` 未映射。
- 规范：WEB-CLIENT §9.3（"组件样式来源唯一""不允许库内硬编码颜色作为最终呈现"）、§9.4（红色元素 ≤2、状态色语义化）；DESIGN.md §5 语义色表。
- 建议：`zkThemeConfig` 里补 `token: { colorError: danger[appearance], colorPrimary: accent[appearance], colorInfo: accent[appearance] }`，`antd-theme.ts` 从生成物导出危险色；断色差用 `buttons` 之外的 `Tabs.inkBar` semantic token。

### SH-16 `--zk-scrollbar` 生成了却没人用：列表溢出时用系统滚动条 [P2]
- 现象：会话列表 `overflow-y:auto`，溢出时出现 15px 宽的系统滚动条（headless 下是 overlay，宽度测量为 0），既没做细滚动条也没做 `scrollbar-gutter`，一旦真出现滚动条，右侧内距从 20px 变成 20px+15px，左右立刻不对称。
- 证据：`shell-measure.mjs` SCROLL（430px 高视口：`listScrollH 168 > clientH 106`，`scrollbarWidth:0`，`scrollbarColor:"auto"`）、`shots/shell-sidebar-overflow-430.png`；`tokens.css:39,104` 定义了 `--zk-scrollbar`，`web/src` 内**零引用**（`grep -rn scrollbar` 只有 tokens.css 两行）。另外 `.zk-sidebar__list{overflow-y:auto}` 与 `.ant-conversations{overflow-y:auto}` 是嵌套滚动容器。
- 规范：DSH 用 5px 自定义滚动条（`design-platform.css` 的 scrollbar 规则）；WEB-CLIENT §9.4（token 纪律，生成了不用等于散落裸值）。
- 建议：`*{scrollbar-width:thin; scrollbar-color: var(--zk-scrollbar) transparent}` + `::-webkit-scrollbar{width:8px}`；`scrollbar-gutter: stable` 让左右内距恒定；去掉 `.ant-conversations` 的 `overflow`（保留外层一个滚动容器）。

### SH-17 折叠/展开没有过渡：264→64 瞬间跳变 [P2]
- 现象：按 Mod+B 后第一帧就已经是 64px，没有任何中间宽度；展开同样瞬间回到 320（拖宽值保留，这点是对的）。内容列瞬间平移 200px，眼睛跟丢。
- 证据：`COLLAPSE_FRAMES = [64,64,64,64,64,64]`（0/40/80/140/220/400ms 采样全为 64）；`DRAGGED {panelW:320, stored:"320"}` → `EXPAND_RESTORED {panelW:320}`；`shots/shell-expanded-restored-1440.png`。代码：`app-shell.tsx:104-108`（`Splitter.Panel size` 直接受控）。
- 规范：DESIGN.md:595 动效（`MotionBase 120ms` 展开/浮层退出；禁止 >240ms）；DSH 侧栏折叠有过渡。
- 建议：给 `.zk-shell__sidebar-panel` 在折叠切换时加 `transition: flex-basis 120ms var(--zk-ease-*)`（或包一层宽度动画容器），并 `@media (prefers-reduced-motion: reduce)` 关掉。

### SH-18 skip-link 文案硬编码中文 [P2]
- 现象：`跳到主内容` 是 JSX 字面量，英文界面下仍显示中文（焦点可见时出现在左上角 8,8 处，94×30）。
- 证据：`SKIPLINK {text:"跳到主内容", x:8, y:8, w:94, h:30}`、`shots/shell-skip-link-focused-1440.png`；`app-shell.tsx:90-92`。
- 规范：WEB-CLIENT §9（文案随语言）、§5.6（skip link 是验收项）。
- 建议：`{t('shell.skipToContent')}`，i18n 两条（zh：跳到主内容 / en：Skip to content）。

### SH-19 断线徽标与 toast 抢同一个右上角；toast 位置为 topRight [P2]
- 现象：`ConnectionBadge` 在非 online 时占据顶栏右端（`margin-left:auto`），每次硬刷新都会闪一次"正在连接网关…"（`role="status"`，这一点是对的）；而 toast 的 placement 固定 `topRight`（`features/toasts/toasts.tsx:57`，注释解释"避开 composer"），会盖在同一个位置——断线+toast 同时发生时互相遮挡。
- 证据：`shots/shell-thread-1440.png`（右端"正在连接网关…"带 spinner）、`shots/shell-settings-model-390.png`（390 下徽标竖排三行）；代码 `shell/connection-badge.tsx:39-52`（online 时 `return null`，所以只在异常期出现）、`features/toasts/toasts.tsx:55-57`。toast 本体**未触发到**（本实例没有能产生 `toast.posted` 的用户动作），因此这一条只按代码给结论、不打截图证据。
- 规范：DESIGN.md 浮层与 chrome 的遮挡规则（toast 不得压住可用控件）；BRIEF §5（P2 = 遮挡/打磨）。
- 建议：toast 改 `bottomRight` 并把 composer 上移（或在有 composer 的路由用 `topRight` 但把连接徽标移到标题右侧而非最右端）；至少保证二者不同时占同一 anchor。

### SH-20 设置页内层导航与主侧栏同款蓝底，两级导航视觉同权 [P2]
- 现象：`/settings/*` 左列（搜索框 + 常用/界面与通知/连接/数据/关于）用与主侧栏完全一样的 antd 菜单选中样式（蓝底 + 蓝字药丸），并与主侧栏紧贴（x=264 起、0 内距），1440 下看起来像侧栏中途换了一套内容；390 下两列导航占满整屏。
- 证据：`shots/shell-settings-model-1440.png`（两个蓝色药丸：侧栏"设置" + 内层"模型"）、`shots/shell-settings-model-390.png`；量测 `.zk-settings__nav` x=264、主 `.zk-sidebar` x=0 w=264。
- 规范：DESIGN.md §5.1（设置导航用 `SurfaceSunken()` 而非裸 `BgSidebar`，就是为了区分层级）；DESIGN.md:617（小节标题 TypeXXXS+LabelTertiary）。
- 建议：设置内层导航换 `--zk-bg-strong` 分组标题 + 选中用 `--zk-bg-selected`（不再用 antd 蓝字），并与主侧栏之间加 1px `--zk-border-l2` 或 8px 间隔；窄屏时内层导航改成顶部下拉（Select）以留出表单宽度。

## 未覆盖 / 限制

- toast 的实际外观与 4s/错误粘滞行为：未能在实例上造出 `toast.posted`（唯一入口是删任务/更新检查），只按代码给 SH-19。
- 会话列表的**错误态**（`shell.sessions.failed` + 重试）：需要杀网关或 20s 超时，未触发；代码路径 `sidebar.tsx:79-88` 与空态共用容器，风险见 SH-09。
- 9862 实例被并发打爆（429 冷却），深色实验改用 lead 分配的 9871；9860 只做只读导航与量测（未点审批、未新建）。

## 跨区域共性问题（给 lead）

1. **两套色值体系并存**：自定义 CSS 走 `--zk-*` token，antd 走自己的 `colorPrimary/colorError`，中间没有映射层 —— 表现为 SH-01（主题）、SH-05（选中）、SH-12（焦点环）、SH-15（badge/tab 色）。修一次要动 `design/theme.ts` + `gen-tokens.mjs` 两端。
2. **「生成的 token 没人用」**：`--zk-scrollbar`（SH-16）零引用；`--zk-bg-selected` 只被 rail dot 用；`SessionSummary` 的 `running/waiting/unread`（SH-07）零消费。设计资产存在但没接线。
3. **页面内距各自为政**：`/inbox` 0、`/team` 24、顶栏 16（SH-04）—— 不止外壳，各页都在自定 gutter，建议由 `AppShell` 统一提供。
4. **响应式只做"缩宽度"，没做"换形态"**：≤1024/≤900 只是把侧栏从 264 压到 232/200，没有任何抽屉/rail/断点折叠（SH-02/SH-10）。所有区域在 390 下都会被同一个 200px 侧栏挤坏（收件箱审批按钮重叠、设置表单不可见、会话正文逐字换行）—— 这是全局结构问题，不是某一页的问题。
5. **加载态没有统一形态**：侧栏一行灰字、顶栏回退到通用标题、正文一个大 spinner（SH-09）；别处很可能同样。建议做一套 shell 级 skeleton 原语。
6. **i18n 收尾遗漏**：`<html lang>` 与 skip-link 文案（SH-13/SH-18）——文案例外的"硬编码中文"还可能在别处（palette 行尾 `action`/`go` 也是中英混排，见 `shots/shell-palette-1440.png`）。