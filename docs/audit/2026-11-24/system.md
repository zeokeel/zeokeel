# 视觉系统与规范符合度 审计（跨区域横向）

- **覆盖**：`/`、`/inbox`、`/c/sess-1795522277318168425`（完成态）、`/c/sess-1795522282596021154`（停在审批）、`/settings/appearance`、`/insights`、`/team`、`/capabilities`、`/automation`；浅色 / OS 深色 / 设置强制深色；中文 / 英文；键盘 Tab 走查 5 屏。
- **截图**：31 张，前缀 `system-`（`shots/system-light-*` 10 张（含 2 张侧栏选中态）、`system-dark-*` 8 张、`system-forced-dark-*` 3 张、`system-en-*` 4 张、`system-focus-*` 6 张（Tab 焦点 + 输入框焦点））。
- **实例**：9860（只读基线）+ 9876（lead 分配的专属实例，暗色/英文/强制深色都在这里做；9862 因并发 429 未使用）。
- **只读声明**：读代码 + 只读 Playwright 量测，未改任何源码；9876 上改了「主题/语言」设置以取证，已恢复为「中文 + 跟随系统」。

## 结论（6 行）

1. **ZeoKeel 手写 CSS 的 token 纪律很好、antd 的默认色板很差，两者在同一屏共存**：`web/src/**` 里 hex/rgb/hsl 字面量为 **0**（除 4 个生成物）；`global.css` 全部走 `var(--zk-*)`；4/8/12/16/20/24 网格的**手写 padding 离群值 0 处**。但**所有 WCAG 不合格项都来自 antd 自带色**（`#87AFFF` 焦点环 2.19:1、`rgba(0,0,0,.45)` 3.36:1、`rgba(0,0,0,.25)` 1.84:1、`#ff4d4f` 角标 3.27:1、暗色白字压 `#3cbbfa` 2.17:1）。
2. **最严重的不是颜色而是「深色主题只实现了一半」**：`tokens.css` 的 dark 块只在 `@media (prefers-color-scheme: dark)` 里，而 `useSystemAppearance()` 让 antd 跟着**设置项**走 → OS 浅色 + 选「深色」= 白底白字（实测 `body` `rgb(255,255,255)` / 标题 `rgba(255,255,255,.85)`），整页不可读。见 `SYS-01`。
3. **字号阶梯本身没被破坏（无 10/15/17/18 裸值），但阶梯的语义被用错**：实际只用了 12/13/14/16/20/24，**11px 从未出现**；`24px` 只由 antd `Welcome` 的 `h4` 提供（`--zk-type-xl` 从未被引用）；于是**空态标题 24 > 页面 H1 20 > 顶栏标题 16**，且 `/insights`、`/team` 等 5 个屏出现**两个文案相同的 `h1`**（16px + 20px 上下叠）。见 `SYS-10`。
4. **状态语义存在三套词汇**：`status-badge.tsx` 声称「One status vocabulary for the whole client」，但 5 个 tone 里**只有 `warning` 被渲染过一次**（审批卡）；「运行中」是 13px `--zk-label-secondary` 灰字 + ThoughtChain 转圈；`/team` 的运行态用 antd `<Tag color="blue">`。`.zk-badge--success/-error/-running/-neutral` 是死代码。见 `SYS-14`。
5. **对比度：token 全过、antd 全崩**。`--zk-label-tertiary` 在白底 6.07:1 / 侧栏 5.72:1、暗色 8.15:1 / 8.51:1，全部 AA 通过；不合格的 6 组全部是 antd 自己的灰/红/蓝（清单见表 3）。
6. **可访问性有两处 Level A 级硬伤**：会话列表项 `tabindex=null / role=null`，键盘完全到不了（`SYS-02`）；英文界面 `html[lang]` 仍是 `zh-CN`，且跳转链接仍是硬编码中文（`SYS-18`）。

---

## 发现

### SYS-01 「设置→深色」在 OS 浅色下产生白底白字（token 只跟媒体查询） [P0]

- **现象**：OS 报告浅色时，在 `/settings/appearance` 选「深色」并保存：页面底色仍是白的，而所有 antd 文字变白——标题、表单标签、radio 文字、导航全部**消失**；同时侧栏仍是浅灰白，antd 的菜单面/输入框变成近黑方块。反向（OS 深色 + 选浅色）同理。
- **证据**：
  - `shots/system-forced-dark-appearance.png`（白底、黑色输入框、看不见任何标签）、`shots/system-forced-dark-now.png`、`shots/system-forced-dark-insights.png`
  - 同一页面保存前后实测（`system-forced-en.mjs`）：
    `BEFORE {"bodyBg":"rgb(255,255,255)","topbandTitle":"rgba(0, 0, 0, 0.88)","menuUlBg":"rgb(255,255,255)"}`
    `AFTER  {"bodyBg":"rgb(255, 255, 255)","topbandTitle":"rgba(255, 255, 255, 0.85)","menuUlBg":"rgb(20, 20, 20)","senderBg":"rgba(255,255,255,0.08)","antInputColor":"rgba(255,255,255,0.85)"}`
    → 底色没变，前景全变白。
  - 代码路径：`web/src/design/tokens.css:66` dark 块只写在 `@media (prefers-color-scheme: dark)` 里；`web/src/design/use-system-appearance.ts:34` 返回 `choice ?? osAppearance`（设置优先）；`web/src/app.tsx:56` 把该值喂给 `ConfigProvider` 的 `darkAlgorithm`（`theme.ts:15`）。三者没有一个把 `data-theme`/class 写到 `<html>`/`<body>`。
  - 对比 DSH：`design-platform.css` 的暗色是 `body[data-ds-dark-theme]` 显式属性（与系统偏好解耦），因此不会出现这种半翻。
- **规范**：`docs/WEB-CLIENT.md §9.1`（CSS token 块与 antd algorithm 必须一致，`tokens.css` 头注释自己也写了这条）；WCAG 1.4.3（正文对比度）。
- **建议**：`useSystemAppearance` 的最终值写进 `document.documentElement.dataset.appearance = 'dark'|'light'`，同时把 `tokens.css` 的 dark 块从 `@media` 改成 `:root[data-appearance='dark']`（并保留 `@media` 版本给"跟随系统"），两处消费同一个值。这是生成物改动，需先改 `web/scripts/gen-tokens.mjs` 的模板再 `npm run gen:tokens`。

### SYS-02 会话列表项无法用键盘聚焦（核心导航键盘不可达） [P0]

- **现象**：Tab 走查 5 屏，焦点序列里**从不出现任何会话项**；会话项是 `<li>` + `cursor:pointer`，纯鼠标可点。
- **证据**：
  - `shots/system-light-sidebar-selection.png`；Tab 序列（`system-radii-focus.mjs`）：`now` 屏 8 步 = `A.zk-skip-link → BUTTON.ant-btn-text(新建会话) → UL.ant-menu → DIV.ant-collapse-header → INPUT(允许范围) → 拒绝 → 允许一次 → 添加附件`，会话列表被整体跳过。
  - DOM 实测：`li.ant-conversations-item {"tag":"LI","tabindex":null,"role":null,"cursor":"pointer"}`，`focusableAncestor: null`，`listCount: 2`。`ul.ant-menu` 的 `tabIndex = 0`（导航菜单可聚焦，会话列表不可）。
  - `web/src/shell/sidebar.tsx:17-19` 注释写「Conversations owns the list semantics (selection, **item roles**…)」——实际 antd X 不提供 role/tabindex；`Conversations` 另有 `shortcutKeys` prop（默认未传）。
- **规范**：WCAG 2.1.1 Keyboard（Level A）；antd X `Conversations` 的 `shortcutKeys` / `menu` prop（`web/node_modules/@ant-design/x/es/conversations/index.d.ts:60-70`）。
- **建议**：给 `Conversations` 传 `shortcutKeys={{ items: ['number'] }}` + 每项渲染为 `<a href="/c/:id">`（用 `label` 包一层链接），或自渲染 `role="listbox"`/roving tabindex；同时补 ↑↓ 键。

### SYS-03 5 个 token 被使用但从未定义 → 整块灰阶退化为继承色 [P1]

- **现象**：`--zk-text-primary/secondary/tertiary/muted` 在 `global.css` 里有 13 处 `color: var(...)`，但 `tokens.css` 里不存在这四个名字；CSS 变量解析失败时整条声明被丢弃，颜色回退为继承的 `--zk-label-primary`。于是"未接线设置行""inspector 面包屑""队列序号""上下文说明""更新版本号"这些**本该是次级灰的文字全部变成正文黑**，层级消失。
- **证据**：
  - 设置页截图 `shots/system-light-appearance.png` 底部「其它设置（网关有、界面未接线）」：`session.sess-…unread` 与 `false` **同色同重**（都继承正文色）；深色版 `shots/system-dark-appearance.png` 同样。
  - `global.css:1361, 1385, 1395, 1468, 1484, 1495, 1499, 1512, 1543`（`--zk-text-*`），另 `global.css:1231, 1330` `--zk-z-overlay` 有 fallback `40` 所以无害。
  - 差集命令：`comm -13 defined.txt used.txt` → `--zk-text-muted / --zk-text-primary / --zk-text-secondary / --zk-text-tertiary`（+ 运行时注入的 `--zk-chat-font-size`，有 fallback，合法）。
- **规范**：`tokens.css` 是唯一颜色来源（AGENTS.md §3.1 / WEB-CLIENT §9.2）；`docs/DESIGN.md §5` 的灰阶名为 `label-primary/secondary/tertiary`。
- **建议**：把 `--zk-text-{primary,secondary,tertiary,muted}` 全量替换为 `--zk-label-{primary,secondary,tertiary}`（`muted` → `tertiary`）。这是 13 行纯 CSS 改名，零风险。

### SYS-04 两套灰阶并存：antd `rgba(0,0,0,.45)` 顶替 `--zk-label-tertiary`，空态主文案 3.35:1 [P1]

- **现象**：同一个"次要说明文字"的语义，在 `.zk-*` 里是 `#5a6371`（6.07:1），在 antd 拥有的面（`Empty` 描述、表格内空态、菜单分组标题、`Typography.Text type="secondary"`、审批卡的快捷键提示）里是 `rgba(0,0,0,.45)`（3.36:1）。
- **证据**（全页扫描，`out-9876-light.json`）：
  - `3.35:1 f=14px/400 fg=rgba(0,0,0,0.45)`：`div.ant-empty-description "没有等你处理的事"`、`div.ant-menu-item-group-title "常用"`（7 处）、`span.ant-typography-secondary "Esc 拒绝 · Cmd/Ctrl+Enter 允许"`、`div.ant-empty-description "还没有队友或子任务"`、`"没有加载技能。"`、`"还没有定时任务。"`、`span.ant-typography-secondary "1 轮 1 步 · 25 tok · 缓存…"`
  - `1.83:1`：`td.ant-table-cell "还没有活动。"`（表格内空态，`rgba(0,0,0,.25)`）
  - 截图：`shots/system-light-inbox.png`、`shots/system-light-team.png`、`shots/system-light-insights.png`
- **规范**：WCAG 1.4.3（14px 正文 4.5:1）；antd 6 `Empty` 的 `description` 语义位（`<Empty description={…} />` 已用对，问题在色）。
- **建议**：在 `theme.ts` 里给出 `token.colorTextSecondary/colorTextDescription/colorTextPlaceholder`（= `--zk-label-secondary/tertiary`），或把 antd 组件包在设了 `color` 的容器里。**这一条与 SYS-06 同源：`theme.ts` 只传了 `algorithm/colorPrimary/fontFamily` 三个 token。**

### SYS-05 未读角标（`需要审批` 计数）白字 3.27:1 [P1]

- **现象**：侧栏"收件箱"的红色计数是**全 app 唯一的注意力信号**，但 12px 白字压在 `#ff4d4f` 上只有 3.27:1；暗色 `#dc4446` 也只有 4.24:1。
- **证据**：`shots/system-light-now.png`、`shots/system-focus-now-tab2.png`（角标 1）；实测 `span.ant-scroll-number-only-unit.current {"fg":"rgb(255,255,255)","bg":"rgb(255,77,79)","ratio":3.27,"font":"12px"}`（7 个屏全部命中）；dark `badgeSup {"bg":"rgb(220,68,70)"}` → 白字 4.24:1。`sidebar-nav.tsx` 用 antd `Badge`（`colorError` 派生）。
- **规范**：WCAG 1.4.3；ZeoKeel 自己的 `--zk-danger: #dc2626` 在白底是 4.83:1（仍不达标），`--zk-danger-text: #b91c1c` 是 6.47:1。
- **建议**：`ConfigProvider` 里 `token.colorError` 绑 `--zk-danger`，并把 `Badge` 的文字色改为 `--zk-accent-text`（白）配更深底，或改用 `.zk-badge--error`（`--zk-danger-text` 文字 + 12% 底）以保证 4.5:1。

### SYS-06 暗色主色按钮白字 2.17:1（`--zk-on-primary` / `--zk-accent-text` 定义了但没接线） [P1]

- **现象**：暗色下主色是浅青 `#3cbbfa`，antd 主按钮/发送键仍是**白字白箭头**压在浅青上；而 `tokens.css` 恰恰为此准备了 `--zk-on-primary: #0f1115`（暗色）与 `--zk-accent-text: #0f1115`，两者**从未被引用**。
- **证据**：
  - `shots/system-dark-team.png`（`新建队友` 按钮）、`shots/system-dark-now.png`（发送键圆形箭头）
  - 实测 `2.87:1 fg=rgb(255,255,255) bg=rgb(54,162,216)`（`span "新建队友"`）
  - 计算：`white on #3cbbfa = 2.17:1`；若用 `--zk-accent-text #0f1115` 则 `8.71:1`
  - `grep` 结果：`--zk-on-primary`、`--zk-accent-text` 在 `web/src` 中零引用（定义未使用清单第 22 项之一）
- **规范**：WCAG 1.4.3 / 1.4.11（非文本对比 3:1）；`tokens.css` 自带 `on-primary` 语义；DSH 对应 `--dsw-alias-label-on-color` / `label-primary-foreground`。
- **建议**：`theme.ts` 增加 `token.colorTextLightSolid = appearance === 'dark' ? '#0f1115' : '#ffffff'`（从 `antd-theme.ts` 生成，不要手写 hex）。

### SYS-07 暗色下导航选中项 3.51:1（accent 文字压 accent 12% 底） [P1]

- **现象**：暗色侧栏选中项是 `rgb(54,162,216)` 文字压 `rgb(32,70,89)` 底 = 3.51:1，14px 正文不达 AA。同一侧栏的会话选中项（`rgba(255,255,255,.12)` 中性底 + 原字色）反而 ≥8:1。
- **证据**：`shots/system-dark-sidebar-selection.png`、`shots/system-dark-appearance.png`（"设置"+"外观"两处同款）；实测 `navSelected {"bg":"rgb(32, 70, 89)","color":"rgb(54, 162, 216)"}`；全页扫描 `3.51:1 fg=rgb(54,162,216) bg=rgb(32,70,89)`。
- **规范**：WCAG 1.4.3；DSH 的做法是选中态**不用主色文字**——`--dsw-specific-sidebar-nav-item-active: var(--dsw-static-neutral-bluish-750)`（深色 `rgb(67,69,74)` 中性底）+ 字色不变。
- **建议**：选中态改用 `--zk-bg-selected`（暗色 `rgba(55,231,254,.13)`）+ `--zk-label-primary` 文字，不要给文字染色；或把 accent 提到 `#37e7fe`（8.67:1）。

### SYS-08 会话列表「选中」与「悬停」是同一个颜色，分不清当前会话 [P1]

- **现象**：`li.ant-conversations-item-active` 的背景 `rgba(0,0,0,.06)` 与 `:hover` 的背景**完全相同**，且选中不改变文字颜色。鼠标停在别的会话上时，屏幕上出现两个一模一样的灰块，"我在哪个会话里"无法判断。
- **证据**：实测 `conv normal=rgba(0,0,0,0) hover=rgba(0,0,0,0.06) active=rgba(0,0,0,0.06)`（`system-hover.mjs`）；`shots/system-light-sidebar-selection.png`、`shots/system-dark-sidebar-selection.png`。
- **规范**：选中态必须与 hover 态可区分（`docs/DESIGN.md §3` 反模式；DSH 选中 `neutral-bluish-100` vs hover `interactive-bg-hover 6%` 两档）。
- **建议**：选中用 `--zk-bg-selected` + `--zk-label-primary`（并把字重提到 500），hover 保留 6% 中性灰；或给选中项左侧 2px accent 竖条。

### SYS-09 同一个侧栏两套选中语言（蓝 tint vs 中性灰） [P1]

- **现象**：导航菜单选中 = `#e6f0ff` 底 + `#0d44f6` 蓝字（5.76:1，达标）；会话选中 = 6% 中性灰底 + 字色不变。两者在**同一列、上下相邻**。
- **证据**：`shots/system-light-inbox.png`（"收件箱"蓝底蓝字，下面两个会话灰底）、`shots/system-light-appearance.png`（"设置"蓝底蓝字）；实测浅色 `navSel {"bg":"rgb(230,240,255)","color":"rgb(13,68,246)"}` vs `convSelected {"bg":"rgba(0,0,0,0.06)","color":"rgba(0,0,0,0.88)"}`。
- **规范**：DSH 侧栏只有一种选中语言（`--dsw-specific-sidebar-nav-item-active` 中性灰，`...-accent` 仅用于强调场景）；`docs/DESIGN.md §3`「同一区域只用一种选中语言」。
- **建议**：两者都收到 `--zk-bg-selected` + `--zk-label-primary`（与 SYS-08 同一个补丁）。

### SYS-10 标题层级倒挂 + 同文案两个 `h1` [P1]

- **现象**：
  1. `/`：`h1.zk-topband__title` "现在" = **16px/600**，下面空态标题 `h4.ant-welcome-title` "现在，说点什么" = **24px/600** → 视觉主标题是 h4，语义主标题是 h1，且**跳过 h2/h3**。
  2. `/insights`、`/team`、`/capabilities`、`/automation`、`/settings/*`：`document.querySelectorAll('h1').length === 2`，两个 h1 文案**完全相同**（顶栏 "洞察" 16px 与页面 "洞察" 20px，垂直相距 24px）。
- **证据**：
  - 字号普查（7 屏）：`14px×174, 12px×31, 16px×14, 13px×14, 20px×8, 24px×1`；唯一 24px 元素 = `h4.ant-typography.ant-welcome-title "现在，说点什么"`
  - `a11y.h1Count = 2`（appearance / insights / team / capabilities / automation 各 1 次），`headings` 示例 `/insights`：`[('H1','洞察'), ('H1','洞察')]`；`/`：`[('H1','现在'), ('H4','现在，说点什么')]`
  - 截图 `shots/system-light-now.png`、`shots/system-light-insights.png`、`shots/system-en-now.png`（"Now" + "What is on your mind?"）
  - `--zk-type-xl (24px)` 在 `web/src` 中**零引用**（定义未使用清单）
- **规范**：antd `Typography` 的 level→size 映射（h4=24 是 antd 的语义，被用在了最高视觉位）；WCAG 1.3.1 / 2.4.6；`docs/WEB-CLIENT.md §9` 字号阶梯 11/12/13/14/16/20/24。
- **建议**：顶栏标题降为 `<div class="zk-topband__title">`（它不是页面主标题），只保留页面 `h1`；空态标题用 `--zk-type-l`(20px) 或 `--zk-type-m`(16px) 与 `--zk-type-xl` 二选一并写进 token；两个同名标题去重（顶栏只在会话页显示会话名，非会话页留空或显示分组名）。

### SYS-11 内容列左缘三套（280 / 288 / 264），/inbox 首行被顶栏裁切 [P1]

- **现象**：同一个 `zk-shell` 里，正文左缘有三套值：
  | 屏 | 顶栏标题 x | 内容 x | 偏差 |
  |---|---|---|---|
  | `/`、`/c/:id` | 280 | 288（`.zk-thread__body` / `.zk-composer` pad 24） | +8 |
  | `/insights`、`/team` | 280 | 288（`.zk-screen` pad 24） | +8 |
  | `/inbox` | 280 | **264**（`.zk-inbox` pad **0**） | **−16（贴死面板边）** |
  | `/settings/*` | 280 | 484（`220px` nav）/ 508（内容 pad 24） | 另一套 |
  `/inbox` 因为没有上内距，第一行"1 项等待你处理"被顶栏 1px 边框**从中间切开**（截图可见上半行缺失）。
- **证据**：
  - `shots/system-light-inbox.png`：`1 项等待你`（"处理"被裁）+ 审批行 `x=264 w=1176` 贴边
  - 实测：`.zk-topband {pad: 0 16px}`、`.zk-topband__title {x:280}`、`.zk-thread__body {x:264 pad:16px 24px 0}`、`.zk-composer {x:264 pad:12px 24px 16px}`、`.zk-inbox {x:264 pad:0px}`、`.zk-settings__body {x:484 pad:8px 24px 24px}`
  - `global.css:716-720`（`.zk-inbox` 无 padding）对 `global.css:781-785`（`.zk-screen` pad 24）
- **规范**：`docs/DESIGN.md §5` 间距阶梯；`BRIEF §3` 已记录的 8px 偏差（此处补上第三套 264 与裁切后果）。
- **建议**：抽一个 `.zk-page { padding: var(--zk-space-6) }` 容器给 `/inbox`、`/insights`、`/team`、`/capabilities`、`/automation` 共用；顶栏左右内距从 16 改 24 与正文对齐（`.zk-topband { padding-inline: var(--zk-space-6) }`）。

### SYS-12 设置页手拼表单：未用 `Form`/`Form.Item`，4 个 `<label for>` 指向不存在的元素 [P1]

- **现象**：`SettingsScreen` 用 `<div class="zk-field"><div class="zk-field__label-row"><label for=…>…</label><span class="zk-field__help">…</span></div><div class="zk-field__control">{控件}</div></div>` 手拼每个字段。后果：
  1. 4 个 radio 字段的 `htmlFor` 指向**不存在的 id**（`Radio.Group` 不接受 `id`）→ 点标签文字无效，屏幕阅读器也无法把标签关联到控件：`deadLabels = ['zk-field-ui.language','zk-field-ui.theme.mode','zk-field-ui.workSteps.mode','zk-field-ui.workSteps.collapse']`；
  2. help 文案放在 **label 同一行右侧**（`align-items: baseline; gap: 8px`），而 antd `Form.Item` 的 `extra` 在**控件下方**，全站两套说明文字位置；
  3. 没有任何 `aria-describedby`（全 `web/src` 命中 **0 次**），help 文案对辅助技术不可见；
  4. 无 `required`/`validateStatus`/`help` 错误态，保存失败只能抛到页头。
- **证据**：`shots/system-light-appearance.png`（"语言 界面语言；缺失时跟随浏览器。" 标签与说明同行）、`shots/system-dark-appearance.png`；`web/src/features/settings/settings-screen.tsx:390-410`（label/help/control 三段手拼）、`:474`（`Radio.Group` 无 id）；`grep -rn "aria-describedby" web/src` → 0。
- **规范**：antd 6 官方 `Form` demo（`bash scripts/antd-cli.sh demo Form layout --version 6`）—— `<Form layout="vertical"><Form.Item label="Field A"><Input/></Form.Item></Form>`；`bash scripts/antd-cli.sh semantic Form --version 6` 给出 root/label/content 三个语义槽；AUDIT 任务书「Form 用 `Form.Item` 的 label/extra/tooltip 而不是自己拼」；WCAG 1.3.1 / 3.3.2。
- **建议**：改用 `Form layout="vertical"` + `Form.Item label help(=extra) htmlFor`；控件作为 `Form.Item` 唯一子元素（radio 用 `Form.Item name` + `Radio.Group`）。若坚持受控 `values` 模式，至少给 `Radio.Group` 外面加 `id` + `role="radiogroup" aria-labelledby`，并给所有 help 加 `aria-describedby`。

### SYS-13 焦点指示三套语言，其中 antd 的环只有 2.19:1 [P1]

- **现象**：同一屏里三种焦点样式：
  1. zk 自己的 `:focus-visible { outline: 2px solid rgba(13,68,246,.8); offset 2px }`（4.59:1）—— 用于 skip-link / radio input / segmented input；
  2. antd 的 `outline: 3px solid rgb(135,175,255)`（**2.19:1**，低于 1.4.11 要求的 3:1）—— 用于所有按钮/菜单/Tab；
  3. 输入框只把边框从 `#d9d9d9` 换成 `#0d44f6` + 一层 10% 透明蓝晕（`box-shadow: rgba(5,105,255,.1) 0 0 0 2px`，几乎看不见）。
- **证据**：Tab 走查（`out-radii.json` 的 `WALK` 段）：`1. A.zk-skip-link ring=solid 2px rgba(13,68,246,0.8)` vs `2. BUTTON.ant-btn-text ring=solid 3px rgb(135,175,255)`；`5. INPUT.ant-select-input ring=none 3px`；按钮环实测 `rgb(135,175,255)` → 白底 2.19:1；`shots/system-focus-now-tab2.png`、`shots/system-focus--settings-appearance-tab2.png`、`shots/system-focus-input-settings.png`。
- **规范**：WCAG 1.4.11 非文本对比 3:1 / 2.4.11 Focus Appearance；DSH `focus.css` 的思路（`--dsw-focus-ring-width: 2px` + 统一 `outline-color` + 指针模态下抑制环）。
- **建议**：`theme.ts` 里设 `token.colorPrimaryBorder`/自定义 `:focus-visible` 覆盖 antd 的 3px 环为 `2px solid var(--zk-focus)`；`web/src/design/global.css:27` 已是正确写法，只需让它赢过 antd（`.zk-shell :focus-visible { outline: 2px solid var(--zk-focus) !important }` 或用 `classNames` 语义槽）。

### SYS-14 状态语义三套词汇：「运行中」被画成次要文字 [P1]

- **现象**：
  - `status-badge.tsx` 定义 5 个 tone（running / success / error / neutral / warning）并注释「One status vocabulary for the whole client」，但**全仓只有 1 处调用**：`approval-card.tsx:63 tone="warning"`。`.zk-badge--running/-success/-error/-neutral` 是**从未渲染的死 CSS**。
  - 工具调用行的"运行中"是 `span.zk-tool-text`（`--zk-label-secondary` 13px 灰）+ antd X `ThoughtChain status="loading"` 的转圈图标；完成/失败也只换图标，不换字色。
  - `/team` 的运行态用 antd `<Tag color="blue">`（第三套形状）。
  - 结论：没有把 running 画成 warning（这点合规），但 running/success/error 的**色与形在三处完全不同**，且"运行中"没有任何进行态颜色。
- **证据**：`shots/system-light-thread-pending.png`（"工具 web_fetch / http://… · **运行中**" 全灰）、`shots/system-light-team.png`；实测 `_statusColor = rgb(74,81,96) / 13px / "http://127.0.0.1:9886/health · 运行中"`；`grep -rn "zk-badge--" web/src --include=*.tsx` → 仅 `status-badge.tsx` 模板字符串 + `approval-card`。
- **规范**：AGENTS.md §3.4「状态语义不可降级：running ≠ warning」；`status-badge.tsx` 自身注释；DSH 用 `--dsw-alias-state-*`（running/idle/done/error/warn 各有 primary/secondary/tertiary 三档）而不是单靠图标。
- **建议**：工具行的状态位改用 `<StatusBadge tone="running|success|error">`（12px、accent 12% 底）替换现在的灰字；`/team` 的 `Tag color="blue"` 换成同一组件；删掉没用的 tone 或补上调用，别留死 CSS。

### SYS-15 `Conversations` 只用了一半能力：无分组、无悬停菜单、无图标、无快捷键 [P1]

- **现象**：会话列只有标题文字。antd X 提供的 `groupable`（今天/昨天/更早）、`menu`（悬停 ⋯ 重命名/删除）、`icon`、`shortcutKeys` **全部未使用**。实测 `groups: 0, menuBtn: 0, creation: false`。
- **证据**：`sidebar.tsx:31-34` 只构造 `{key, label}` 并传 `items/activeKey/onActiveChange`；实测 `conv features {"creation":false,"groups":0,"menuBtn":0}`；`shots/system-light-now.png`（三条会话无时间、无悬停操作、无未读点）。
- **规范**：antd X `Conversations` 的 `menu` / `groupable` / `ConversationsItemProps.group` / `shortcutKeys`（`es/conversations/index.d.ts:37,42,60-70`、`es/conversations/interface.d.ts:19-34`）；主流做法（ChatGPT / Claude / Kimi 的会话列都有"今天/更早"分组 + 悬停 ⋯ 菜单 + 未读点）。
- **建议**：`items` 增加 `group`（按 `updatedAt` 分「今天/过去 7 天/更早」）与 `menu`（重命名/删除）；给当前会话加 accent 小圆点。

### SYS-16 Markdown 标题完全脱离字号阶梯（UA 默认 28/21/16.4/11.6） [P1]

- **现象**：回答正文里的 `#`~`######` 没有任何样式规则，落在 `<div class="zk-markdown">` 里继承 UA 默认：h1 `2em`=28px、h2 `1.5em`=21px、h3 `1.17em`≈16.4px、h5 `0.83em`≈11.6px、h6 `0.67em`≈9.4px，且带 `0.67em/0.83em` 的 UA 外边距。
- **证据**：`web/src/design/global.css` 中 `.zk-markdown` 只有 3 条规则（`213-224`：font-size/首末子元素 margin），**没有任何 `h1..h6` 选择器**；渲染器是 `@ant-design/x-markdown` 的 `XMarkdown`（`chat/markdown.tsx:34`），不做标题映射。（当前两条会话的回答是纯文本，未在截图复现，故以代码为准。）
- **规范**：`docs/WEB-CLIENT.md §9` 字号阶梯 11/12/13/14/16/20/24；DSH 有完整 `--dsw-font-markdown-h1..h4`（21/19/18/14，且全部 `calc(… + var(--dsh-content-font-delta))` 可随字号设置缩放）。
- **建议**：在 `global.css` 里补 `.zk-markdown h1{font-size:var(--zk-type-l)} h2{var(--zk-type-m)} h3{var(--zk-type-body)…}` 并同时把 `--zk-chat-font-size` 换成缩放因子（现在只有 `.zk-transcript` 的 `font-size` 变，标题用的是 em 会跟着变，但正文 px 固定，结果是缩放不一致）。

### SYS-17 输入框 placeholder 1.83:1（浅）/ 2.22:1（深）——主输入框首屏就是这个 [P1]

- **现象**：composer 的 `给 ZeoKeel 发消息，/ 调用指令，@ 文件或对话` 是全 app 最强的引导句，却是 `rgba(0,0,0,.25)`（1.83:1）/ `rgba(255,255,255,.25)`（2.22:1）。设置页三个"示例快捷键"输入框里**只有 placeholder 有内容**，等于整行不可读。
- **证据**：实测 placeholder 清单：`1.82:1 rgba(0,0,0,0.25) textarea.ant-input-borderless`（composer，所有会话屏）、`1.83:1` ×4（设置页 `ant-input-outlined` / `ant-input-sm`）、dark `2.22:1 fg=rgba(255,255,255,0.25)`；截图 `shots/system-light-thread-pending.png`、`shots/system-dark-now.png`、`shots/system-light-appearance.png`。
- **规范**：WCAG 1.4.3（placeholder 属于文本）；DSH 对应 `--dsw-alias-markdown-placeholder` 走 `neutral-bluish-60 #f5f6f7`（浅色下即"极浅底"而非浅字）。
- **建议**：`theme.ts` 设 `token.colorTextPlaceholder = 'var(--zk-label-tertiary)'`（6.07:1）——同样受 SYS-06 的"theme.ts 只传 3 个 token"影响。

### SYS-18 英文界面 `html[lang]` 仍是 `zh-CN`，跳转链接未翻译 [P1]

- **现象**：切到英文后全站文案是英文，但 `<html lang="zh-CN">` 没有更新（屏幕阅读器会用中文语音读英文），且首屏第一个可聚焦元素（skip link）仍是硬编码中文「跳到主内容」。
- **证据**：`painde_checks`：`EN ROUTE now {"lang":"zh-CN","htmlLang":"zh-CN","topband":"Now"}`，`bodySample` 开头 `"跳到主内容 ZeoKeel New session Inbox 1 Capabilities…"`；`web/index.html:2` 写死 `<html lang="zh-CN">`；`grep -rn "documentElement.lang" web/src` → 0 处；`app-shell.tsx:103` 里 skip link 文案是字面量 `跳到主内容`（不在 i18n 目录）。截图 `shots/system-en-now.png`、`shots/system-en-appearance.png`、`shots/system-en-insights.png`。
- **规范**：WCAG 3.1.1 Language of Page（Level A）、3.1.2（部分语言）；项目自身 i18n 契约（`i18n/zh.ts` 与 `en.ts` key-for-key 镜像）。
- **建议**：`I18nProvider` 里 `document.documentElement.lang = locale === 'en' ? 'en' : 'zh-CN'`；skip link 加 `'shell.skipToMain'` 到两份目录。

### SYS-19 发送按钮无可读名（图标按钮无 `aria-label`） [P1]

- **现象**：composer 右下角的圆形发送键只有图标，DOM 里没有任何 accessible name，Tab 到它时读屏只会念 "button"。
- **证据**：`a11y.unnamedButtons`：`['button.ant-btn.ant-btn-circle']`，`unnamedCount: 1`，出现在**每一个**会话屏（now / thread / thread-pending）；实测按钮 `x=1371 w=32 h=32 r=50%`；`composer.tsx:346-392` 的 `<Sender>` 未传 `actions`，用的是 antd X 默认动作区。
- **规范**：WCAG 4.1.2 Name, Role, Value；antd X `Sender` 的 `actions` prop（`es/sender/index.d.ts`）。
- **建议**：`<Sender actions={<Button aria-label={t('composer.send')} …/>}>`，或给默认按钮补 `aria-label`（同一处顺手把禁用态对比度一起修，见 SYS-06）。

### SYS-20 对比度清单不达标的其余项（antd 链接色 4.10:1） [P1]

- **现象**：antd `Typography` 里的链接用 `#1677ff`（4.10:1），而品牌 `--zk-accent` 是 6.61:1；`打开该会话` 这类可点文字刚差一点。
- **证据**：`4.1:1 fg=rgb(22,119,255) bg=rgb(255,255,255) n=1 ['span "打开该会话"']`（thread 屏）；`theme.ts` 未设 `token.colorLink`。
- **规范**：WCAG 1.4.3；DSH `--dsw-alias-link: rgb(65,118,230)` 单一定义。
- **建议**：`token.colorLink / colorLinkHover / colorLinkActive` 全部绑到 `--zk-accent / --zk-accent-hover / --zk-accent-pressed`（后两个 token 目前也未被引用）。

### SYS-21 22 个 token 定义了却从未使用（含整个 11px / 24px 阶梯） [P2]

- **现象**：`tokens.css` 定义 77 个变量，`web/src` 只消费 55 个。未使用：`--zk-type-xxxs(-line/-weight)`（11px 整档）、`--zk-type-xl(-line/-weight)`（24px 整档）、`--zk-space-0/5`、`--zk-radius-xl`、`--zk-control-height`、`--zk-row-compact-height`、`--zk-row-double-height`、`--zk-page-action-height`、`--zk-scrollbar`、`--zk-selection`、`--zk-border-subtle`（与 `border-l1` 重复）、`--zk-accent-hover/-pressed/-text`、`--zk-on-primary`。
- **证据**：`comm -23 <(sort defined.txt) <(sort used.txt)` 输出（见 SYS-03 的命令）。其中 `--zk-scrollbar` / `--zk-selection` 未接线的直接后果：暗色下滚动条与文本选区仍是**浏览器原生白色**（`shots/system-dark-thread-pending.png` 右缘可见浅色滚动条）。
- **规范**：`docs/DESIGN.md §5`；DSH 的 `scrollbar.css` 专门说「Without it every scrolling region renders the UA scrollbar, which ignores the theme」。
- **建议**：接上 `scrollbar-color: var(--zk-scrollbar) transparent` 与 `::selection { background: var(--zk-selection) }`；11px 与 24px 两档要么用起来要么从生成器删掉（现在的状态是"设计给了阶梯，实现只用了一半"）。

### SYS-22 表格列宽是裸 px（12 处，无 token） [P2]

- **现象**：`Table` 用的是 `columns`（未手写 `<table>`，符合规范），但列宽全是字面量：`160/120/100/140/170/84/250/320/110/72/90`，另有 `maxWidth: 420`、`maxHeight: 380`。
- **证据**：`capabilities-screen.tsx:62,67,89,93`；`insights-screen.tsx:92,105,106`；`automation-screen.tsx:182,188,194,200,213`；`team-screen.tsx:156,169,184,227,234`；`member-detail.tsx:130,137`；`context-screen.tsx:134,135,155`。
- **规范**：AGENTS.md §3.1（禁止裸数值度量）；`tokens.css` 有 `--zk-row-*` 却没有列宽档。
- **建议**：加 `--zk-col-<n>` 或统一用 `width: 'N%'`/`minWidth`；至少把 100/110/120/140/160/170 收敛到 2~3 档。

### SYS-23 `h1.zk-topband__title` 保留 UA 默认 margin 10.72px [P2]

- **现象**：顶栏标题是 `<h1>` 但 `global.css:123-131` 没写 `margin: 0`，实测 `margin-top/bottom = 10.72px`（= UA 的 0.67em × 16px）。当前靠 `align-items: center` 侥幸居中，一旦改成多行/换行就会破。
- **证据**：间距普查 `margins != 0` 里唯一由 ZeoKeel 自己产生（非 antd）的离群值：`10.72px/10.72px ×7`（每个屏 1 次）；对比 `.zk-screen__title` 有 `margin: 0`（`global.css:799`）。
- **规范**：4/8/12/16 网格；同类元素（两个页面标题类）应同构。
- **建议**：`.zk-topband__title { margin: 0; }`。

### SYS-24 一个 composer 里四种圆角、两套尺寸语法 [P2]

- **现象**：同一行控件圆角 `4px`（antd `Select`，`工作区内修改`/`acceptance-tool`）/ `6px`（antd `Button`，`拒绝`/`允许一次`）/ `12px`（`Sender` 容器）/ `50%`（发送键）；高度 `24 / 32 / 118`。设计 token 里 `--zk-radius-sm` 是 6、`--zk-radius-md` 是 8，antd 用的是 4 和 6。
- **证据**：实测 `.ant-select {r:4px h:24}`、`.ant-btn-default {r:6px h:32}`、`.ant-sender {r:12px h:118}`、`.ant-btn-primary {r:50% w:32 h:32}`（`out-radii.json`）；`shots/system-light-thread-pending.png`。
- **规范**：`tokens.css` 圆角档 4/6/8/12/16；DSH 4/8/12/16/20/28（composer 用 `radius-panel` 28 + 发丝描边）。
- **建议**：`theme.ts` 设 `token.borderRadius = 6`（`borderRadiusSM/LG` 对应档位），发送键保持圆；把 `Select` 的 `size` 与按钮对齐到同一高度档（都 32 或都 28）。

### SYS-25 侧栏被 antd 的白色菜单面切开（两种底色、硬边） [P2]

- **现象**：`.zk-sidebar` 是 `#f7f8fa`，但里面的 `ul.ant-menu` 是**不透明白色** `rgb(255,255,255)`，于是导航区和会话区分成两块颜色，在 y=324 处有一条硬边（暗色下同样：侧栏 `#0b0b10` vs 菜单 `rgb(20,20,20)`）。
- **证据**：实测 `menuUlBg: rgb(255,255,255)` / dark `rgb(20,20,20)`，`sidebar: rgb(247,248,250)` / dark `rgb(11,11,16)`；截图 `shots/system-light-now.png`（导航区偏白、会话区偏灰）、`shots/system-dark-now.png`。
- **规范**：antd `Menu` 的 `styles={{ root: { background: 'transparent' } }}` 语义槽；DSH 侧栏单一 `--dsw-specific-sidebar-fill`。
- **建议**：给 `Menu` 传 `style={{ background: 'transparent' }}`（或在 `theme.ts` 设 `components.Menu.colorItemBg`）。

### SYS-26 暗色表格表面是 antd 的 #1d1d1d，不在 zk 中性阶上 [P2]

- **现象**：表头浅色 `#fafafa`（对上 `#fff` 几乎无差），暗色 `rgb(29,29,29)`（对上底 `#111118` 明显偏亮且偏中性），既不是 `--zk-bg-strong`(#272736) 也不是 `--zk-bg-raised`(#1b1b26)。
- **证据**：实测 light `tableHead {"bg":"rgb(250, 250, 250)"}` / dark `{"bg":"rgb(29, 29, 29)"}`；`shots/system-dark-insights.png`、`shots/system-light-insights.png`。
- **规范**：`tokens.css` 三层表面（base/raised/strong）；DSH `--dsw-alias-markdown-code-block-banner` 等每层都有对应中性。
- **建议**：`components.Table.headerBg` 绑 `--zk-bg-strong`，行 hover 绑 `--zk-bg-hover`。

### SYS-27 空/未设值的数字输入框是一个纯空框（无单位、无 placeholder、无步进暗示） [P2]

- **现象**：「对话字号」是一个 88×32 的空边框盒子；它接受 12–20 的**任意整数**（用户可设 15/17/19，绕过 11/12/13/14/16/20 阶梯），但界面没有任何范围或单位提示。
- **证据**：`shots/system-light-appearance.png`、`shots/system-dark-appearance.png`（"对话字号"下方的空框）；`web/src/features/settings/ia.ts:209`（`kind: 'number'`，无 options）；`web/src/shell/app-shell.tsx:52-55`（`Math.min(20, Math.max(12, raw))` → 12..20 任意整数）。
- **规范**：`tokens.css` 字号阶梯；`docs/DESIGN.md §3`「不要一个看起来能设但没边界的控件」。
- **建议**：改成 `Segmented`/`Select` 提供 12/13/14/16/18/20 六档（与阶梯对齐），或 `InputNumber min=12 max=20 step=1 addonAfter="px"` 并给 placeholder `14`。

### SYS-28 全仓没有 `prefers-reduced-motion` 分支 [P2]

- **现象**：唯一的过渡动画 `.zk-msg-actions { opacity 0 → 1; transition: opacity 120ms ease }`（消息操作条淡入）以及 antd 自带的全部动效（抽屉/下拉/折叠/`ThoughtChain` 转圈）都没有降级路径；系统开启"减少动态效果"时行为不变。
- **证据**：`grep -rn "prefers-reduced-motion" web/src --include=*.css --include=*.tsx` → **0 命中**；`global.css:1399-1411`（`.zk-msg-actions` 的 120ms 过渡）；`shots/system-focus-now-tab2.png` 同屏可见操作条只在 hover 出现。
- **规范**：WCAG 2.3.3 Animation from Interactions；DSH `base.css` 把动效收敛到 `--ds-transition-duration*` 与 `--ds-ease-in-out`（一处可关）。
- **建议**：`@media (prefers-reduced-motion: reduce) { *, *::before, *::after { transition-duration: 0.01ms !important; animation-duration: 0.01ms !important } }`，并把 120ms 提升为 token（`--zk-motion-fast`）。

---

## 统计表

### 表 1 · 字号分布（7 屏 × 可见文本元素，n=242）

| 字号 | 次数 | 出现位置 | 阶梯内？ |
|---|---|---|---|
| 14px | 174 | 正文、菜单、表格、按钮、气泡 | ✅ body/s |
| 12px | 31 | 徽标、hint、meta、help、表头副标 | ✅ xxs |
| 16px | 14 | **顶栏标题 ×7**、侧栏品牌 ×7 | ✅ m |
| 13px | 14 | 表单 label、`.zk-screen__note`、工具行、空态提示 | ✅ xs |
| 20px | 8 | 页面 H1 ×3、洞察大数字 ×5 | ✅ l |
| 24px | **1** | `/` 的 `h4.ant-welcome-title`（antd `Welcome`，非 token） | ✅ xl（但**不是** token 来的） |
| **11px** | **0** | `--zk-type-xxxs` 从未使用 | ✅ 但空转 |
| 10/15/17/18 | **0** | — | 无越阶裸值 ✅ |

**结论**：没有出现阶梯外的裸值（这点很难得）；问题在**档位语义**：11px 与 24px(声明式) 空转、16px 只服务顶栏、24px 反而压过 20px 的页面 H1。

### 表 2 · 间距节奏（4/8/12/16/20/24/32）

| 值 | 次数 | 归属 |
|---|---|---|
| 4 / 8 / 12 / 16 / 24 | 813 | ✅ 全部 ZeoKeel 手写 CSS（`.zk-*`）与 antd 的一部分 |
| 7px | 26 | **antd**（`Button`/`Select`/`Segmented` 的 `padding-inline:7px`） |
| 15px | 7 | **antd**（默认按钮 `padding-inline:15px`） |
| 11px | 4 | **antd**（`Input` / `InputNumber` 的 `padding-inline:11px`） |
| 5px | 3 | **antd**（`Input.TextArea` borderless） |
| 9px | 1 | **antd**（`Switch` 内衬） |
| 2px | 1 | **antd**（`Segmented` sm） |
| **10.72px** | 7 | **ZeoKeel**：`h1.zk-topband__title` 的 UA margin 未重置（唯一自产离群值） |

**结论**：ZeoKeel 自己的 CSS **0 处离群**（很干净）；所有 5/7/9/11/15 都来自 antd 内部尺寸体系，因此"两套节奏"是在**容器与控件的接缝**处被看见的（`.zk-settings__body` 24px 内距 vs 内部 `Input` 11px 内距；`.zk-composer` 24px vs `Sender` 12px）。

### 表 3 · 对比度清单（脚本计算，含 alpha 合成）

**浅色（bg-base #ffffff / bg-sidebar #f7f8fa）**

| 组合 | 比值 | 判定 |
|---|---|---|
| `--zk-label-primary #0f1115` / base | 18.9:1 | ok |
| `--zk-label-secondary #4a5160` / base | 7.96:1 | ok |
| `--zk-label-tertiary #5a6371` / **base** | **6.07:1** | ok |
| `--zk-label-tertiary #5a6371` / **sidebar #f7f8fa** | **5.72:1** | ok |
| `--zk-accent #0d44f6` / base（白字压 accent） | 6.61:1 | ok |
| `--zk-warning-text #8a4b0f` / base（`需要审批`徽标、审批影响面） | 6.79:1 | ok |
| `--zk-success-text #15803d` / base | 5.02:1 | ok |
| `--zk-danger-text #b91c1c` / base | 6.47:1 | ok |
| `--zk-danger #dc2626` / base | 4.83:1 | ok |
| zk 焦点环 `rgba(13,68,246,.8)` | 4.59:1 | ok |
| 导航选中 `#0d44f6` on `#e6f0ff` | 5.76:1 | ok |
| 表头 `rgba(0,0,0,.88)` on `#fafafa` | 15.97:1 | ok |
| **antd 焦点环 `#87AFFF`** | **2.19:1** | ❌ <3 |
| **antd 次要文字 `rgba(0,0,0,.45)`**（空态描述 7 处、设置分组标题、审批快捷键提示） | **3.36:1** | ❌ <4.5 |
| **antd placeholder `rgba(0,0,0,.25)`**（composer 主输入框、设置示例框 ×4） | **1.84:1** | ❌ <3 |
| **antd 表格内空态 `rgba(0,0,0,.25)`**（"还没有活动。"） | **1.83:1** | ❌ <4.5 |
| **antd 未读角标 白 on `#ff4d4f`** | **3.27:1** | ❌ <4.5 |
| **antd 链接 `#1677ff`** | **4.10:1** | ❌ <4.5 |
| 禁用按钮 `rgba(0,0,0,.25)` on `#f5f5f5`（WCAG 豁免，但视觉近消失） | 1.82:1 | 豁免 |

**深色（bg-base #111118 / bg-sidebar #0b0b10）**

| 组合 | 比值 | 判定 |
|---|---|---|
| `--zk-label-primary #f9fafb` / base | 17.99:1 | ok |
| `--zk-label-secondary #c7cad9` / base | 11.53:1 | ok |
| `--zk-label-tertiary #a3abbb` / **base** | **8.15:1** | ok |
| `--zk-label-tertiary #a3abbb` / **sidebar** | **8.51:1** | ok |
| `--zk-accent #3cbbfa` / base | 8.67:1 | ok |
| `--zk-danger-text #fca5a5` / base | 9.90:1 | ok |
| `--zk-warning-text #e9a24c` / base | 8.72:1 | ok |
| `--zk-success-text #4ade80` / base | 10.79:1 | ok |
| `--zk-accent-text #0f1115` on accent（**未被使用**） | 8.71:1 | ok |
| **白 on `#3cbbfa`（antd 主按钮/发送键）** | **2.17:1** | ❌ <3 |
| **antd 导航选中 `#36a2d8` on `#204659`** | **3.51:1** | ❌ <4.5 |
| **antd placeholder `rgba(255,255,255,.25)` / base** | **2.20:1** | ❌ <3 |
| **antd 表格内空态 `rgba(255,255,255,.25)`** | **2.24:1** | ❌ <3 |
| **未读角标 白 on `#dc4446`** | **4.24:1** | ❌ <4.5 |

**一句话**：**8 项深色不合格 + 8 项浅色不合格，全部来自 antd 色板；ZeoKeel 的 24 个 token 组合全部通过。**

---

## 表 4 · ZeoKeel vs DSH（DeepSeek Harness）设计规范对照

DSH 依据：`.../dsh-client-ui-theme/src/styles/design-platform.css`（511 行全量 token）、`base.css`、`focus.css`、`scrollbar.css`、`ConversationRoot.module.css`、`InputBar.module.css`、`UserText.module.css`。

| 维度 | DSH 的做法 | ZeoKeel 的做法 | 差距 |
|---|---|---|---|
| **主色使用比例** | `--dsw-alias-brand-primary` = 近黑 `#0f1115`（浅）/ `#f9fafb`（深）；蓝 `--dsw-static-deepseek-500 #4176e6` **只给 link / info / 轨迹** | `#0d44f6` 是全站主色：主按钮、焦点环、Tab 下划线、**导航选中**、running 徽标、发送键 | ZeoKeel 的蓝承担了 DSH 用中性色承担的角色 → 一屏主色块数量偏高，强调色预算紧张 |
| **选中态语言** | 侧栏选中 = `--dsw-specific-sidebar-nav-item-active` 中性灰 `#ebeef2`（深 `rgb(67,69,74)`），**字色不变**；accent 变体另存但少用 | 导航选中 `#e6f0ff` + `#0d44f6` 蓝字；会话选中 `rgba(0,0,0,.06)` 灰 + 字色不变（**且与 hover 同色**） | 两套并存 + 选中=hover（SYS-08/09） |
| **边框强度** | 4 档 `l1 .04 / l2 .1 / l3 .12 / l4 .16`，常用 **0.5px 发丝**（`border-bottom: 0.5px solid var(--dsw-alias-border-l3)`），浮层描边画在 `box-shadow` 里 | 3 档 `l1 .06 / l2 .09 / l3 .15` + 重复的 `border-subtle .06`，全部 **1px**；`box-shadow` 只有 1 处且为字面量 `0 8px 24px var(--zk-scrim)` | 少一档（无 `l4` 用于输入框/卡片描边）、无 0.5px 发丝、无阴影 token 分级 |
| **圆角** | `4 / 8 / 12 / 16 / 20 / 28(panel)` | `4 / 6 / 8 / 12 / 16 / 999`（`radius-xl 16` 未使用） | ZeoKeel 缺 20/28 大圆角档 → composer 卡片只能停在 12px，观感比 DSH/ChatGPT 更"方" |
| **字号** | 正文 14 + 次级 −1/−2（13/12）；markdown h1 21 / h2 19 / h3 18 / h4 14，全部 `calc(… + var(--dsh-content-font-delta))` 可被"内容字号"设置**整体缩放** | 11/12/13/14/16/20/24（11、24-CSS 空转）；markdown 标题**零规则**（UA 28/21/16.4）；`--zk-chat-font-size` 只改 `.zk-transcript` 的 `font-size`，标题用 em 跟随、正文用 px 固定 | 阶梯方向一致，但 DSH 的阶梯是**活的**（随设置缩放、markdown 有映射），ZeoKeel 是死的（SYS-16/27） |
| **阴影** | 三级 `--dsw-shadow-lv1 0 2px 4px rgba(0,0,0,.05)` / `lv2` / `lv3` + `elevation`（描边+柔光三件套） | 无阴影 token；`box-shadow: 0 8px 24px var(--zk-scrim)` 是字面量（斜杠菜单）、`0 8px 32px color-mix(...)`（窄屏 inspector） | 浮层高度没有统一语言 |
| **密度 / 控件高度** | 侧栏项 30–38px 级、composer 卡片 `radius-panel 28` + 12px 内 gap、`--dsh-composer-card-max-width` 限宽 | token 给了 `28 / 32 / 36 / 40 / 44` 五档高度，但实测侧栏项 **40px**、菜单项 40、composer `Sender` 118px、设置输入 32、`Select` 24 | 五档高度 token **全部未被 antd 消费**（antd 用 24/32），实测高度与 token 无关 |
| **焦点环** | 2px 统一宽（`--dsw-focus-ring-width`）+ 统一色 + `data-input-modality='pointer'` 时抑制环 | 2px brand（zk 自己）/ 3px `#87AFFF` 2.19:1（antd）/ 仅边框变色（输入框） | 三套并存（SYS-13） |
| **滚动条 / 选区** | `scrollbar.css` 专门接线 4 个 `--dsw-alias-scrollbar-*`，5px 宽，并说明"否则原生条忽略主题" | `--zk-scrollbar` / `--zk-selection` **定义但零引用** → 暗色下是原生浅色滚动条 | SYS-21 |
| **暗色开关机制** | `body[data-ds-dark-theme]` 显式属性，与系统偏好解耦 | 只有 `@media (prefers-color-scheme: dark)`，设置项只驱动 antd | **这就是 SYS-01 的根因** |

---

## 表 5 · ZeoKeel vs 主流 Agent 产品（视觉基准）

（"它们"指该产品的通行做法；"ZK"为本仓库实测。）

| 面 | 主流做法 | ZK 现状 | 差距 |
|---|---|---|---|
| **会话列** | ChatGPT/Claude/Kimi：按「今天/昨天/更早」分组 + 悬停 `⋯` 菜单（重命名/删除）+ 未读点 + 相对时间 | 只有标题（`{key,label}`），无分组/无菜单/无时间/无未读点；选中与 hover 同色 | **大**（SYS-15/08） |
| **消息区** | 用户消息右侧窄气泡（max-width ~70–80%）；助手消息无气泡、全宽；hover 出现复制/重试工具条 | 用户气泡 `rgba(0,0,0,.06)` + 12px 圆角、贴右对齐（右缘 = 内容列右缘 1416，正确）；助手无气泡 ✅；hover 动作条 `复制/编辑` 存在但**自拼在气泡外**、`opacity 0→1` 无 `prefers-reduced-motion` 退化 | 中（SYS-22 相关、无 reduced-motion） |
| **工具调用行** | ChatGPT/Claude Code/Cursor：单行折叠 + 状态点（色）+ 耗时 + 可展开原始参数 | 左侧差号图标 + `工具 web_fetch` + 第二行 `URL · 运行中`（13px 灰字）→ 展开后是 `地址` 键值 + `原始参数` 折叠。信息齐，但**状态没有色**，且缩进 26px（`x=314` vs 正文 288）与标题列不齐 | 中（SYS-14） |
| **composer** | ChatGPT/Claude/Cursor：一个**抬起的圆角卡片**（radius 24–28）+ 附件/模型/权限三件套在左下 + 发送键右下 + 快捷键提示在卡片下 | antd `Sender`：`rgba(0,0,0,.04)` 灰底 + 12px 圆角 + 1px 边框；`+`/`工作区内修改`/`acceptance-tool` 在左下 ✅；发送键右下 ✅；提示行在下方 ✅；**但 placeholder 1.83:1**、四档圆角混排、禁用发送键 2.37:1 | 中（SYS-17/24） |
| **审批卡** | Claude Code/Cursor/Copilot：危险动词不做默认动作、给影响面（哪条命令/哪个文件）、给仓库/路径、`Enter` 不默认允许 | ZK 做得好：`需要审批`(warning 6.79:1) + `工具 web_fetch` + `这会向外发出网络请求。` + 原始参数 + `Esc 拒绝 · Cmd/Ctrl+Enter 允许` + **无默认按钮**（三个都是 outline）+ `仅此一次/拒绝/允许一次`。**但**：快捷键提示 3.36:1；`来自会话：…` 挤在右侧同行被截断；没有"目标文件/命令"的结构化位置（只有原始参数折叠） | 小-中（SYS-04） |
| **空态** | 一句引导 + 1 个动作，不放大插画（Claude/ChatGPT hero 是可输入 composer 的一部分） | `/` 是 24px 标题 + 14px 副标题居中，composer 固定在底部（非 hero 合一）；描述文字 `输入一句话开始…` 用的是 antd secondary（3.36:1） | 中（SYS-10/04） |
| **动效** | 200ms 级 ease-out，尊重 `prefers-reduced-motion`；流式文本不做逐字动画 | `.zk-msg-actions { transition: opacity 120ms }` 一处；`prefers-reduced-motion` **全仓 0 命中**；流式已关 typewriter（`markdown.tsx:31` 注释，做得对） | 小（SYS-20 类） |
| **层级/标题** | 顶栏是**面包屑/会话名**，不是页面标题；页面只有一个 H1 | 顶栏 = `h1` 16px + 页面 `h1` 20px（5 屏同文案叠加） | 中（SYS-10） |

---

## 视觉债清单（按优先级）

### P0（2）
1. **SYS-01** 设置「深色」在 OS 浅色下白底白字 —— `tokens.css` 只跟 `prefers-color-scheme`，antd 跟设置项。
2. **SYS-02** 会话列表项无法键盘聚焦（WCAG 2.1.1 A），核心导航键盘不可达。

### P1（18）
3. **SYS-03** 4 个未定义 token（`--zk-text-*`）→ 13 处灰阶退化为正文黑。
4. **SYS-04** antd `rgba(0,0,0,.45)` 顶替 `--zk-label-tertiary`：空态描述/审批快捷键提示 3.36:1、表内空态 1.83:1。
5. **SYS-06** 暗色主按钮白字压浅青 2.17:1（`--zk-on-primary`/`--zk-accent-text` 未接线）。
6. **SYS-17** placeholder 1.84:1（浅）/ 2.20:1（深），composer 首屏引导句。
7. **SYS-05** 未读角标 3.27:1 / 4.24:1。
8. **SYS-07** 暗色导航选中 3.51:1。
9. **SYS-08** 会话选中态 == hover 态。
10. **SYS-09** 同侧栏两套选中语言。
11. **SYS-10** 双 `h1` + 标题层级倒挂（h1 16 < h4 24）+ 同文案叠加。
12. **SYS-11** 内容列左缘 280/288/264 三套，`/inbox` 首行被顶栏裁切。
13. **SYS-12** 设置页手拼表单（未用 `Form`/`Form.Item`）+ 4 个死 `label` + 0 个 `aria-describedby`。
14. **SYS-13** 焦点指示三套，antd 环 2.19:1。
15. **SYS-14** 状态语义三套词汇，"运行中"无进行态色。
16. **SYS-15** `Conversations` 未用 `groupable/menu/icon/shortcutKeys`。
17. **SYS-16** markdown 标题脱离阶梯（UA 28/21/16.4/11.6）。
18. **SYS-18** 英文界面 `lang="zh-CN"` + skip link 未翻译。
19. **SYS-19** 发送按钮无可读名。
20. **SYS-20** 链接色 `#1677ff` 4.10:1（`--zk-accent` 是 6.61:1）。

### P2（8）
21. **SYS-21** 22 个 token 空转（11px/24px 档、`--zk-scrollbar`/`--zk-selection` 未接线 → 暗色滚动条是原生的）。
22. **SYS-22** 表格列宽 12 处裸 px。
23. **SYS-23** `h1.zk-topband__title` 保留 UA margin 10.72px。
24. **SYS-24** 一个 composer 四种圆角 + 五档控件高度 token 全未使用。
25. **SYS-25** 侧栏被 antd 白色菜单面切成两色 + 硬边。
26. **SYS-26** 暗色表头 `#1d1d1d` 不在 zk 中性阶上。
27. **SYS-27** 「对话字号」空输入框、可设 15/17/19 绕过阶梯。
28. **SYS-28** 无 `prefers-reduced-motion`（`.zk-msg-actions` 120ms 过渡 + antd 动效）。

---

## 附录 · 复现命令

```bash
cd /data/zeokeel/.cache/ui-audit
export ZK_TOKEN=<9876 token> ZK_BASE=http://127.0.0.1:9876

node system-shots.mjs light        # 浅色 7 屏 + 就绪等待
node system-shots.mjs dark         # OS 深色（colorScheme: dark）7 屏
node system-9876-census.mjs        # 字号分布 / 间距离群 / 左缘几何
node system-9876-light.mjs         # 对比度全页扫描 + a11y（landmarks/h1/死 label/无名按钮）
node system-radii-focus.mjs        # 圆角/边框/表面 + Tab 走查 8 步 ×5 屏
node system-forced-en.mjs          # 设置强制深色（白底白字证据）+ 英文 lang 检查
node system-selstate.mjs           # 选中态（浅/深）
node system-hover.mjs              # 选中 vs hover vs 普通
node system-kbd.mjs                # 会话项可聚焦性 + 输入框焦点差异
bash ../scripts/antd-cli.sh lint ./web/src     # → "Scanned 152 files. No issues found."
bash ../scripts/antd-cli.sh demo Form layout --version 6
bash ../scripts/antd-cli.sh semantic Form --version 6
bash ../scripts/antd-cli.sh token Button --version 6
```

**antd CLI lint 结果原文**：`Scanned 152 files. No issues found.`（即：静态 API 用法没有违规；本报告里的 antd 问题都是**用法之外的**——色板、焦点环、槽位未用、`Conversations` 能力未用。）

**token 审计命令**：
```bash
grep -oE "\-\-zk-[a-z0-9-]+" web/src/design/tokens.css | sort -u > /tmp/defined.txt
grep -rhoE "var\(--zk-[a-z0-9-]+" web/src --include=*.tsx --include=*.ts --include=*.css | sed 's/var(//' | sort -u > /tmp/used.txt
comm -13 /tmp/defined.txt /tmp/used.txt   # 用而未定义（4 个真问题 + --zk-chat-font-size 有 fallback）
comm -23 /tmp/defined.txt /tmp/used.txt   # 定义而未使用（22 个）
```