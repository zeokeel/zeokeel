# ZeoKeel Web 客户端 UI/UX 全面复盘

- 日期：2026-11-24（北京时间）
- 仓库：`/data/zeokeel`，`master` @ `cd29938`（release 0.4.0）
- 栈：React 19.3 / antd 6.7.1 / @ant-design/x 2.10.0 / Vite 8
- 方式：**真实网关 + 确定性假模型**（`acceptance-tool`：工具调用 → 停审批 → 文本回答），出货产物（`go:embed` 的 `web/dist`）在 Chromium 里跑；6 名并行审计员 + 统一截图/量测夹具
- 规模：**148 条发现**（P0 19 / P1 83 / P2 46，去重后 **8 个 P0 根问题**）、**297 张截图**、7 档视口（1920→390）、14 类状态
- **后续**：第二轮「功能可用性与深度交互」见 [docs/audit/2026-11-24/INTERACTION.md](docs/audit/2026-11-24/INTERACTION.md)（80 条，12 个 P0，含连接器/模型/输入框/设置接线）；字段与说明文字的设计规范见 [FIELD-DESIGN.md](docs/audit/2026-11-24/FIELD-DESIGN.md)。
- **本报告未改动任何产品代码**（`git status --short` 只有本报告与附录）

---

## 0. 结论

**问题不在"细节没打磨"，而在三件事同时失效：布局契约没接到前端、主题有两个真源、页面骨架一屏一个样。**

用户最先感到的不是"丑"，而是**找不到该看哪里、该点哪里**：

1. 一个左缘三种 x —— 顶栏标题 **280**、会话正文 **288**、收件箱/设置正文 **264**（贴死侧栏接缝）。
2. 一个侧栏两套选中语言 —— 导航是蓝底蓝字，会话是中立灰，且都没有规范要求的 2px accent 左条。
3. 每屏标题说两遍 —— 顶栏 `h1`(16px) 与页面 `h1`(20px) 同文上下叠（自动化/能力/洞察/团队/轨迹/记忆全中），空态标题 24px 反而最大。
4. 空态插画悬在页面中上部，与主操作相距 300–500px，且不告诉你下一步。
5. 别的会话有一条待审批，**你所有会话（含"现在"落地页）的输入框一起变灰**。
6. 浅色系统下选「深色」主题 → **白底白字**（body `#fff` + 标题 `rgba(255,255,255,.85)`），整页不可读。
7. 手机宽度（390px）侧栏仍占 200px，内容列只剩 126px，正文逐字换行、表格互相重叠、设置表单完全不可见。

| 维度 | 现状 | 判定 |
|---|---|---|
| 信息架构 | 路由/屏面齐全；同层级元素重复（标题 ×2、label ×2、"关于" ×2、内部 key 直接上屏） | ✗ |
| 布局栅格 | 左缘 3 套、页内距 3 套、侧栏内距左右不对称、正文无宽度上限 | ✗ |
| 视觉层级 | 每屏 5 档字号（契约 ≤4）；顶栏 16 < 页面 20 < 空态 24（层级倒挂）；正文行长 80 汉字（契约 640px≈45） | ✗ |
| 主题 | 设置与 CSS 两个真源 → 白底白字；反向（OS 深+设置浅）同样全站深色 | ✗ P0 |
| 响应式 | 只缩宽度不换形态；390px 内容 126px、检查器无关闭路径 | ✗ P0 |
| 交互 | 审批跨会话锁死 composer；Esc 四种含义、下拉内/中文输入法合成中会误拒审批 | ✗ P0 |
| 官方件用法 | antd `Form/Empty/Table` 与 antd X `Conversations/Sender/Bubble` 的骨架能力大量未用 | ✗ |
| 状态语义 | running ≠ warning、工具完成/失败有文字标签 | ✓ |
| 可访问性 | 会话列表键盘不可达、发送键无可读名、8 组对比度 <4.5（浅 6 / 深 2）、`lang` 恒 zh-CN、无 reduced-motion | ✗ |

### 0.1 去重后的 8 个 P0

| # | 问题 | 覆盖区域 | 影响的用户 | 详细 |
|---|---|---|---|---|
| **P0-1** | 主题双源：设置「深色」时 CSS token 不变，白底白字 / 三色割裂 | shell / settings / system | **所有**用深色的人 | SH-01、S17、SYS-01 |
| **P0-2** | 移动端结构性不可用：390px 下侧栏 200–264、内容 126px、审批按钮叠字、设置表单不可见 | shell / surfaces / thread / settings | 手机 / 窄窗用户 | SH-02、S-01、S-26、T-01、S-30 |
| **P0-3** | 审批跨会话全局锁死输入框（含"现在"落地页） | composer / surfaces / settings | **所有**被他人审批阻塞的人 | C-01、S-03 |
| **P0-4** | Esc 语义冲突：同屏两种承诺；下拉内 Esc、中文输入法 Esc 都会**误拒审批** | composer | 中文用户 + 键盘用户 | C-02、C-03、C-04 |
| **P0-5** | 会话列表键盘完全不可达（Tab 序里没有一条会话） | shell / system | 键盘 / 读屏用户 | SH-03、SYS-02 |
| **P0-6** | `/files`、`/guide` 窄屏变成全屏浮层且无关闭路径，左列宽度 0 | surfaces | 窄屏用户（被困） | S-02 |
| **P0-7** | 自动化「新建任务」按界面自带帮助文案填写必定 400，错误渲染在弹窗背后 | surfaces | 所有用自动化的人 | S-04 |
| **P0-8** | 内部实现泄漏进 UI：每页设置底部摊 `session.sess-…` key；收件箱行标题是原始 JSON | settings | 所有用户（隐私与可读性） | S16、S28 |

> 19 条"P0"里有 11 条是这 8 个根问题的不同表现（深色主题被 3 个区域各自记一次、移动端被 4 个区域记一次、审批阻塞被 3 个区域记一次）。**按根因修，不要按条目修。**

---

## 1. 方法与证据

### 1.1 环境（可复现）

```bash
# 1) 构建出货产物（网关 go:embed 的就是这份，不是 dev server）
cd web && npm run build && cd .. && bash scripts/build-web-client.sh

# 2) 真实网关 + 假模型；每个审计员一个独立数据目录，互不干扰
bash .cache/ui-audit/start.sh                 # :9860 参考基线（只读）
bash .cache/ui-audit/startN.sh shell 9871 9881 # 任意数量的独立实例（可写）

# 3) 种数据（1 个完成态工具轮 + 1 个停在审批的会话）
ZK_LABEL=shell ZK_TOKEN=<token> ZK_BASE=http://127.0.0.1:9871 node .cache/ui-audit/seed-one.mjs
```

- Chromium（Playwright）驱动：`deviceScaleFactor` 1（量测）/ 2（窄屏），`locale` zh-CN/en-US，`colorScheme` light/dark 可控。
- 量测：`getBoundingClientRect()` + `getComputedStyle()`；对比度按 WCAG 相对亮度公式**含 alpha 合成**计算；字号分布按可见叶子文本统计。
- 截图：`.cache/ui-audit/shots/`（`desk-*` 全路由、`mob-*`/`tab-*` 视口、`shell-*`/`thread-*`/`composer-*`/`settings-*`/`surfaces-*`/`system-*` 分区）。

### 1.2 覆盖矩阵

| 维度 | 覆盖 |
|---|---|
| 路由 | `/`、`/inbox`、`/c/:id`、`/trajectory`、`/memory`、`/context`、`/files`、`/guide`、`/child/:childId`、`/settings` + 9 页、`/automation`、`/capabilities`、`/insights`、`/team`、404 |
| 视口 | 1920×1080、1600×1000、1440×900、1280×800、1024×768、768、390×844 |
| 状态 | 空 / 加载 / 错误 / 流式中 / 已停止 / 待审批 / 双审批排队 / 工具完成 / 工具失败 / 15 轮长会话 / 折叠侧栏 / 深色（两种路径）/ 英文 / 脏表单 / 校验失败 / 调度任务 400 |
| 键盘 | Tab 全序、focus-visible、Esc 四种上下文、IME 合成中 Esc、Mod+K、`/` 与 `@` 菜单 |

### 1.3 分区报告（原始证据）

| 区域 | 文件 | 条目 |
|---|---|---|
| 外壳：侧栏 / 顶栏 / 浮层 / 响应式 | [`findings/shell.md`](docs/audit/2026-11-24/shell.md) | 20 |
| 会话流：消息 / 工具卡 / 轨迹 | [`findings/thread.md`](docs/audit/2026-11-24/thread.md) | 18 |
| 输入区与审批交互 | [`findings/composer.md`](docs/audit/2026-11-24/composer.md) | 20 |
| 设置九页与收件箱 | [`findings/settings.md`](docs/audit/2026-11-24/settings.md) | 34 |
| 其余屏面与空态 | [`findings/surfaces.md`](docs/audit/2026-11-24/surfaces.md) | 28 |
| 视觉系统与规范符合度 | [`findings/system.md`](docs/audit/2026-11-24/system.md) | 28 |

---

## 2. 根因（7 条，跨区域）

### R1 · 设计契约只写在 Go 里，前端从来没接到

`internal/design/components.go` 定义了整套布局尺度：

```
SidebarWidth 280   PaletteWidth 640   ToastWidth 320
InspectorWidth 320 SettingsWidth 880
TranscriptWidth 960 ProseMeasure 640
```

但 `web/scripts/gen-tokens.mjs:145-147` 的白名单只导出**高度**，注释还写着"palette/settings geometry belong to screens not built yet"——**屏早就建好了**。于是前端各自硬编码：

| 契约 | 前端实际 | 证据 |
|---|---|---|
| SidebarWidth 280 | **264** | `web/src/app.tsx:19` |
| InspectorWidth 320 | **380**（min 280、max 55%） | `web/src/features/app-routes.tsx:38` |
| TranscriptWidth 960 | **无上限**（1440 下 1128，1920 下 1632） | `global.css:155-160` |
| ProseMeasure 640 | **无上限**（单行 ≈80 汉字） | 同上 |
| SettingsWidth 880 | **无上限**（956） | `settings-screen.tsx` |
| ToastWidth 320 | antd 默认 **384** | `toasts.tsx` |

**后果**：16:9 屏上正文行长是规范的 1.8 倍；1920 屏会话被拉散到两端；改一处宽度要翻 6 个文件。修复只需在 `gen-tokens.mjs` 补 7 个变量 + 替换硬编码。

### R2 · 主题有两个真源（P0-1）

- CSS：`web/src/design/tokens.css:66` 的暗色块只有 `@media (prefers-color-scheme: dark)`。
- antd：`useSystemAppearance()`（`design/use-system-appearance.ts`）把**设置项**喂给 `zkThemeConfig()` 的 `darkAlgorithm`。
- 两者之间**没有任何桥**：`app.tsx` 从不写 `html[data-theme]`。

实测（OS 浅色 + 设置深色）：`body` = `rgb(255,255,255)`、`.zk-sidebar` = `rgb(247,248,250)`、标题 = `rgba(255,255,255,.85)`、`.ant-menu` = `rgb(20,20,20)` → 对比度 **1.05:1**，品牌名与会话标题全部消失。反向（OS 深 + 设置浅）同样全站深色。

修法：生成器同时产出 `:root[data-theme='light']` / `[data-theme='dark']` 两块（`@media` 只留给 `system` 兜底），`App` 里 `documentElement.dataset.theme = appearance`。

### R3 · antd 的默认色板越过了 token 体系

`web/src/design/theme.ts` 只传了 `algorithm / colorPrimary / fontFamily` 三个 token，其余全走 antd 默认。结果**所有 <4.5:1 的对比度问题，100% 来自 antd 色值，ZeoKeel 自己的 24 组 token 全部达标**：

| 组合 | 比值 | 出现在 |
|---|---|---|
| antd placeholder `rgba(0,0,0,.25)` | **1.84:1** | composer 主输入框、4 个设置示例框 |
| antd 表格空态 `.25` | **1.83:1** | 团队"还没有活动。" |
| antd 次要文字 `.45` | **3.36:1** | 空态描述、设置分组标题、**审批卡的「Esc 拒绝 · Cmd/Ctrl+Enter 允许」** |
| antd 角标 白 on `#ff4d4f` | **3.27:1** | 侧栏未读/审批计数 |
| antd 链接 `#1677ff` | **4.10:1** | 各处链接 |
| antd 焦点环 `#87AFFF` | **2.19:1** | 所有 antd 控件 |
| 暗色主按钮 白 on `#3cbbfa` | **2.17:1** | 暗色发送键（`--zk-on-primary` 已定义却零引用） |

一次补 `colorTextSecondary / colorTextDescription / colorTextPlaceholder / colorLink / colorTextLightSolid / colorError` 即可清掉 8 项。

### R4 · 页面骨架没有参数化，每屏各自打补丁

`zk-screen` 这一层同时承担"内距 / 滚动 / 宽度 / 标题块"四种职责，于是：

- 内距：`/` = 16、`/inbox` = **0**、`.zk-screen`（team/capabilities/insights/automation）= 24、`.zk-stub` = 32/24、子会话头 = 41。
- 标题：**四个来源** —— 顶栏 `h1`(16px, `routes.ts` 的 titleKey)、页内 `h1`(20px)、tab 同名(14px)、404 退化成品牌名 `ZeoKeel`。5 个屏出现"同样的字上下写两遍"。
- 状态：loading 借 `.zk-empty` 居中、empty 在顶部、error 是一条无边框红字（还常被弹窗遮住）。

### R5 · 全局单例状态把"别人的事"变成"你的事"（P0-3/P0-4/P0-5）

`approvals`（全局数组）、`esc` claim、`mod+Enter` shortcut 都是单例：

- `thread-screen.tsx:484` `disabled={approvals.length > 0}` —— 不区分 session；
- `esc-arbiter.ts` 在 `document` capture 阶段抢先 `preventDefault`，没有 `isComposing` / `defaultPrevented` 守卫；
- `Conversations` 只传 `items`，没有 `role/tabindex`，键盘进不去。

### R6 · 响应式只缩宽度，不换形态（P0-2/P0-6）

`global.css:1206-1240` 只把侧栏 264 → 232（≤1024）→ 200（≤900），**没有 768 以下的抽屉/rail 方案**；`Layout.Sider` 从未传 `breakpoint`。于是 390px 下侧栏占 51%，`#zk-main` = 126px，`scrollWidth` 322 > `clientWidth` 126 且页面不横向滚动（内容不可达）。

### R7 · 官方件"用了壳，没用到骨"

| 组件 | 用了 | 没用（而这次实测每一条都在扣分） |
|---|---|---|
| antd `Form` | 容器 | `Form.Item label/extra/tooltip`、`validateStatus/help`、`htmlFor` 关联（4 个 `<label for>` 指向不存在的 id） |
| antd `Empty` | 插画+文案 | `children`（下一步操作）、统一槽位 |
| antd `Table` | 渲染 | `align:'right'`、`sorter`、`scroll={{x}}`、`Statistic` |
| `Conversations` | `items` | `groupable`（时间分组）、`menu`（悬停 ⋯）、`shortcutKeys`、图标 |
| `Sender` | `prefix` | `footer`/`actions` 的正确分工（3 个控件塞进 prefix → 253px，输入列被推到 24% 处） |
| `Welcome` | 组件 | 文案居中（`text-align: start` 让标题视觉重心左偏 86px） |

`antd-cli lint ./web/src` 结果：`Scanned 152 files. No issues found.` —— 说明这些问题都**在规则之外**（色板、槽位、a11y、骨架），不是 lint 能抓的。
---

## 3. 分区发现（148 条）

每条都带 ID 与严重度；**证据（截图名 + 选择器 + 数字 + 代码行）在对应分区文件里**，本节给全量索引与关键条目的详情。


### 3.1 A · 外壳：侧栏 / 顶栏 / 全局浮层 / 响应式

- 截图：49 张 `shots/shell-*.png`；量测 `shell-measure.mjs` / `shell-probe*.mjs`
- 条目：20（P0 3 / P1 10 / P2 7）
- 结论：几何骨架是对的（264/64 两态、拖宽持久化、Mod+B、Band 48 + Tabs 38 实测成立），但四类系统性缺陷让它在真实使用里站不住：主题只驱动 antd、窄屏不换形态、会话列表键盘不可达、会话行与导航行是两套语言两套内距。

**P0 详情**

- **SH-01 主题=深色在浅色系统上得到"白底白字 + 三色割裂"** — 设置保存成功、刷新仍生效，但 `.zk-sidebar` 仍是 `#f7f8fa`，而同页菜单文字 `rgba(255,255,255,0.85)`（对比度 ≈1.05:1），品牌名/会话标题全不可见；一屏三套表面（白正文 / 黑菜单块 / 黑顶栏）。  证据：`shots/shell-dark-setting-inbox-1440.png`、`shots/shell-dark-setting-thread-1440.png`、`shots/shell-light-setting-osdark-inbox-1440.png`
- **SH-02 390px：侧栏 200px、内容 126px、审批按钮重叠、内容不可达** — `.zk-sidebar` 200×844；`#zk-main` x=200 w=126；`scrollWidth` 322 > `clientWidth` 126 且页面不横向滚动；顶栏标题被压成 1–2 字，`ConnectionBadge` 竖排三行，审批卡"拒绝 / 允许一次"换行后叠在正文上。  证据：`shots/shell-thread-data-390.png`、`shots/shell-root-390.png`、`shots/shell-inbox-390.png`、`shots/shell-settings-model-390.png`
- **SH-03 会话列表键盘完全不可达** — Tab 序只有 `skip-link → 新建会话 → 整个 Menu → body` 循环（14 次 Tab 只出现 4 个节点）；会话项 `{role:null, tabIndex:null, aria-current:null}`。  证据：`shots/shell-focus-tab-1440.png`

| ID | 严重度 | 问题 |
|---|---|---|
| `SH-01` | P0 | 主题=深色在浅色系统上得到"白底白字 + 三色割裂" |
| `SH-02` | P0 | 390px 视口：侧栏固定 200px，内容列 126px，审批按钮重叠、内容不可达 |
| `SH-03` | P0 | 会话列表键盘不可达：Tab 序里没有一条会话 |
| `SH-04` | P1 | 顶栏 16px 与正文 0/24px：一屏三套左缘（280 / 264 / 288） |
| `SH-05` | P1 | 侧栏两套选中语言，且两套都不合 DESIGN 的会话行解剖 |
| `SH-06` | P1 | 图标列与标题列不在同一条竖线；两级左右内距不一致 |
| `SH-07` | P1 | 会话行缺分组／时间／未读／运行态／悬停操作 —— 数据在协议里有，被侧栏丢掉 |
| `SH-08` | P1 | 折叠态没有任何展开入口；rail 用 10px 圆点当按钮 |
| `SH-09` | P1 | 加载态：2.9s 内顶栏显示通用标题、侧栏只有一行纯文本；折叠后变成竖排 |
| `SH-10` | P1 | `Layout.Sider` 的 breakpoint 从未生效；264 不是 token 值 |
| `SH-11` | P1 | 侧栏没有可读名「侧栏」；badge 的 `aria-label` 被丢掉 |
| `SH-12` | P1 | 焦点环两套语言：antd 3px #87afff vs `--zk-focus` 2px |
| `SH-13` | P1 | `<html lang>` 恒为 zh-CN：英文界面仍声明中文 |
| `SH-14` | P2 | 导航菜单块自带不透明底色，在侧栏上画出一个矩形接缝 |
| `SH-15` | P2 | badge 红与 tab 蓝都没走 token：同一屏出现两套色值 |
| `SH-16` | P2 | `--zk-scrollbar` 生成了却没人用：列表溢出时用系统滚动条 |
| `SH-17` | P2 | 折叠/展开没有过渡：264→64 瞬间跳变 |
| `SH-18` | P2 | skip-link 文案硬编码中文 |
| `SH-19` | P2 | 断线徽标与 toast 抢同一个右上角；toast 位置为 topRight |
| `SH-20` | P2 | 设置页内层导航与主侧栏同款蓝底，两级导航视觉同权 |


### 3.2 B · 会话流：消息 / 工具卡 / 轨迹

- 截图：`shots/thread-*.png`；量测见该文件内联数字
- 条目：18（P0 1 / P1 8 / P2 9）
- 结论：虚拟化与滚动跟随是**正面验证过**的（15 轮 30 行只渲染 9–19 行，贴底/锚点正确）；问题集中在"没有宽度上限、没有状态语汇、缺耗时与可操作"：正文单行 80 汉字、流式中无信号、停止后刷新像完整回答、工具卡直接打印协议枚举。

**P0 详情**

- **T-01 390px 下会话列只剩 78px，正文逐字换行** — 与会话列无 `min-width:0` 及外层固定侧栏叠加，正文每行 1–2 字。  证据：`shots/thread-mobile-390.png`

| ID | 严重度 | 问题 |
|---|---|---|
| `T-01` | P0 | 390px 下会话列只剩 78px，正文逐字换行 |
| `T-02` | P1 | 助手正文与整个会话列没有宽度上限：1440 下单行 80 汉字，1920 下更长 |
| `T-03` | P1 | 用户气泡无上限且对齐到列右缘，长消息会横跨 1128px |
| `T-04` | P1 | 流式中的回答没有任何"正在生成"的可视信号 |
| `T-05` | P1 | 点停止后「已停止」不出现；刷新后该轮显示 token 用量，像是完整回答 |
| `T-06` | P1 | 工具卡里直接打印协议枚举当错误文案（红字 "other"） |
| `T-07` | P1 | 工具行没有耗时、状态只有灰字，无状态 tint |
| `T-08` | P1 | 悬停操作条藏在气泡内部，且常驻 28px 死区把气泡撑高 |
| `T-09` | P1 | 展开后的工具卡宽度随内容收缩（236→321px 跳变），信息挤成一列 |
| `T-10` | P2 | 用量行没有单位、没有标签，紧贴正文 4px，且与 composer 用两套词汇 |
| `T-11` | P2 | 代码块没有复制按钮、没有横向可滚动的暗示 |
| `T-12` | P2 | 表格/引用/标题的排版没有产品级样式 |
| `T-13` | P2 | `.zk-markdown > :first-child` 归零规则失效，首块上边距泄漏 17px |
| `T-14` | P2 | 工具卡默认折叠与 WEB-CLIENT §7.3 的"本轮默认展开"不符 |
| `T-15` | P2 | Ctrl+F 查找条搜不到工具参数/输出，输入框是一条 916px 的原生 input |
| `T-16` | P2 | 轨迹页信息密度过低，结果块无标签 / 编号可疑 |
| `T-17` | P2 | 状态色双轨：ThoughtChain 用 antd 默认绿/红，产品 token 未生效 |
| `T-18` | P2 | 长会话：虚拟化与跟随正确，但中段 DOM 略超预算 |


### 3.3 C · 输入区与审批交互

- 截图：41 张 `shots/composer-*.png`；原始 JSON 在 `logs/c*.json`
- 条目：20（P0 4 / P1 11 / P2 5）
- 结论：本产品最核心的交互面缺陷最多。审批成了全局单例的受害者（一条待审批锁死所有会话）、Esc 在四个上下文里有四种含义且会误拒审批、输入列被 prefix 挤到内容区 24% 处、审批三动作没有危险/安全的视觉层级。

**P0 详情**

- **C-01 一条待审批锁死所有会话与「现在」屏的 composer** — `approvals` 是全局数组，`disabled={approvals.length > 0}` 不分 session（`thread-screen.tsx:484`、`now-screen.tsx:125`）。两个独立 context 并发各开会话 → 两页同时 `taDisabled`，新开会话也立刻禁用；第 2 条审批只能靠并发竞速才造得出来。  证据：`shots/composer-crosssession-other-thread-1440.png`、`shots/composer-tab-order.png`、`shots/composer-two-pending-card.png`
- **C-02 同一屏印出两条互相矛盾的 Esc 说明** — 卡片写「Esc 拒绝 · Cmd/Ctrl+Enter 允许」，其下方 100px 处 hint 写「Esc 清空草稿」或「Esc 停止本轮」；实际按 Esc 是拒绝（level 30 > 20 > 10）。  证据：`shots/composer-seeded-now-approval.png`、`shots/composer-scope-session.png`
- **C-03 在「仅此一次」下拉里按 Esc 关下拉，会顺带把审批拒掉** — `logs/c12.json`：`before {dropdown:true, cards:1}` → `after {dropdown:false, queue:null}`；根因 `esc-arbiter.ts:115` 用 document capture，没有「浮层已消费」判断。  证据：`shots/composer-esc-in-dropdown.png`
- **C-04 中文输入法按 Esc 取消候选词 = 拒绝审批** — 派发 `keydown{Escape, isComposing:true, keyCode:229}` → `defaultPrevented:true`，`cards 1 → 0`；缺 `isComposing`/`keyCode===229` 守卫（DSH `ApprovalCard.tsx:50-54` 三条守卫齐全）。  证据：`shots/composer-ime-esc.png`

| ID | 严重度 | 问题 |
|---|---|---|
| `C-01` | P0 | 一条待审批锁死所有会话与「现在」屏的 composer（跨会话污染） |
| `C-02` | P0 | 同一屏印出两条互相矛盾的 Esc 快捷键说明 |
| `C-03` | P0 | 在「仅此一次」下拉里按 Esc 关下拉，会顺带把审批拒掉 |
| `C-04` | P0 | 中文输入法按 Esc 取消候选词 = 拒绝审批（缺 `isComposing` 守卫） |
| `C-05` | P1 | 输入列被 prefix 挤到内容区 24% 处，placeholder 与 `+` 差 249px |
| `C-06` | P1 | 审批三动作没有危险/安全的视觉层级，拒绝的 hover 是"主色蓝" |
| `C-06b` | P1 | 范围下拉与允许按钮印出同一句话 |
| `C-07` | P1 | `Cmd/Ctrl+Enter` 忽略范围选择，永远只允许一次 |
| `C-08` | P1 | 审批卡右侧「来自会话：… · 用户」语义含糊、右对齐吞标题、「现在」屏没有跳转 |
| `C-09` | P1 | 「影响对象」既不在卡片正文里，也不在收件箱行里 |
| `C-10` | P1 | composer 完全没有可见焦点环，而卡片是 antd 3px 蓝框、`.zk-field` 是 2px 主色 |
| `C-11` | P1 | 发送/停止按钮没有可读的无障碍名：`arrow-up` / 英文 `Stop loading` |
| `C-12` | P1 | 斜杠 / `@` 菜单不锚定输入位置且空结果沉默 |
| `C-13` | P1 | 控件高度有四套，且全都不接自己的 token（24 / 32 vs 28 / 36 / 40） |
| `C-14` | P2 | 状态行文案在运行中与两个 Text 粘连、颜色对比度不足 |
| `C-15` | P2 | 禁用态说明文字对比度 1.82:1，几乎读不出"为什么不能输入" |
| `C-16` | P2 | hint 三态文案不一致（命令键漏项） |
| `C-17` | P2 | 多条待审批只给一个计数，看不到第 2 条是什么 |
| `C-18` | P2 | 队列 chip / 面板细节 |
| `C-19` | P1 | 计划 / 提问对话框（本环境无法触发）——代码层问题 |


### 3.4 D · 设置九页与收件箱

- 截图：55 张 `shots/settings-*.png`
- 条目：34（P0 5 / P1 22 / P2 7）
- 结论：设置页有 34 条，是最"糙"的区域：手拼表单（label 重复、help 同行、4 个 `<label for>` 指向不存在的 id）、枚举靠自由文本且非法值被静默归一化、零校验、只读页仍有保存栏、每页底部摊内部 key；收件箱把审批降级成一行原始 JSON。

**P0 详情**

- **S17 选「深色」后整页白底白字、不可读** — `htmlTheme:""`、title `rgba(255,255,255,.85)` 叠在 `rgb(255,255,255)` 上；根因同 SH-01（tokens.css 只跟媒体查询）。  证据：`shots/settings-dark-reload.png`
- **S16 每个设置页底部把内部 key 与会话 id 摊给用户** — `session.sess-1795522277388119007.unread → false`，9 页各一遍（`settings-screen.tsx:313-334`）。  证据：`shots/desk-settings-appearance.png`、`shots/desk-settings-about.png`
- **S28 收件箱把审批降级成一行原始 JSON** — 标题 `{"url":"http://…"}`、副行只有 `web_fetch`，无会话归属、无影响面、无「始终允许」；与线程内审批卡是同一张审批的两个产品。  证据：`shots/desk-inbox.png`、`shots/settings-inbox-1440.png`
- **S29 收件箱页内距 0，行通栏 1176，首行被顶栏裁切** — `.zk-inbox` padding=0；计数行 x=264。  证据：`shots/desk-inbox.png`
- **S30 390 下审批按钮压住标题，URL 被裁** — 审批信息在手机宽度下不可读。  证据：`shots/settings-inbox-390.png`

| ID | 严重度 | 问题 |
|---|---|---|
| `S01` | P1 | 切换语言后设置导航仍是旧语言，中英混排 |
| `S02` | P1 | 分组名「关于」与页名「关于」重复；组标题与页项字号相同 |
| `S03` | P1 | 顶栏「设置」与正文 h1「模型」是两级标题，但都不承担层级职责 |
| `S04` | P1 | 三列宽度与内距不成体系（264 / 220 / 956，label 行 908 vs 控件 720 vs 90） |
| `S05` | P2 | 搜索框与多个按钮是 24px 高，不在 28/32/36/40/44 阶梯上 |
| `S06` | P1 | 字段 label 与 section 标题重复（语言/语言、主题/主题、Agent 预设/Agent 预设），「模型」出现 4 次 |
| `S07` | P1 | label 与 help 同行，13px vs 12px、同字重，只靠颜色区分 |
| `S08` | P1 | 输入宽度三套：720 / 90 / 内容驱动；数字框恒定 90px |
| `S09` | P1 | 数字字段缺单位、缺默认值、缺范围（`上下文预算 (tokens)` / `对话字号`） |
| `S10` | P1 | 开关行解剖违反自家契约：开关在标签下方而非行尾，4 个布尔占 240px |
| `S11` | P1 | 单选组的 `<label for>` 指向不存在的 id；radiogroup 无名字；help 无 aria-describedby |
| `S12` | P2 | JSON 文本域用比例字体、禁止手动 resize、无格式提示 |
| `S13` | P1 | 枚举字段用自由文本，非法值被后端静默归一化 |
| `S14` | P1 | 主表单零校验：非法 JSON、非法 URL 都能「已保存」，无 validateStatus/help |
| `S15` | P2 | 字段顺序与同义字段分散：外观页 9 项单列、热键被拆成 3 段、通知页 4 开关单列 |
| `S16` | P0 | 每页底部把网关原始 key（含会话 id）直接摊给用户 |
| `S17` | P0 | 切「深色」主题后设置内容区白底白字，整页不可读 |
| `S18` | P1 | 关于页版本语义自相矛盾：当前版本 dev / 最新 0.4.0 /「有可用更新」 |
| `S19` | P1 | 只读页（关于）仍显示保存栏，且该页有 2 个 primary 按钮 |
| `S20` | P1 | 发布说明是英文标签 + 裸 URL，装在输入框样式的盒子里 |
| `S21` | P2 | 日期格式 `2026/11/24 19:59:29` |
| `S22` | P1 | 模型目录列表零样式：disc 圆点 + 40px 缩进 + 按钮紧跟文字；空目录无空态 |
| `S23` | P1 | 模型删除：确认键非危险色、删默认模型不清 `chat.model`、同 id 可重复且一次删掉全部同 id |
| `S24` | P2 | 模型行每行一个红色按钮，3 行就超「一屏 ≤2 处红色」 |
| `S25` | P1 | 归档页只有 1 个开关；破坏性操作（删除归档/清除数据）在 9 页里完全缺失且无二次确认 |
| `S26` | P1 | 「数据与隐私」页全是 JSON 文本域，没有任何隐私项 |
| `S27` | P2 | 通知页 section 标题「分级」不成立 |
| `S28` | P0 | 收件箱行标题是原始 JSON 参数，与线程内审批卡信息严重不一致 |
| `S29` | P0 | 收件箱页内距 0：容器/列表/行全贴屏幕左缘，行通栏 1176 |
| `S30` | P0 | 390 视口下审批按钮压住标题，URL 被裁 —— 审批信息不可读 |
| `S31` | P1 | 收件箱行没有会话归属；ask/plan/artifact 的「查看」是无 sessionId 的死按钮 |
| `S32` | P1 | 空态文案重复两次；计数行与标题左缘不一致 |
| `S33` | P2 | 收件箱动作按钮 24px、缺「始终允许」、单项占满 1176×64 |
| `S34` | P1 | 英文界面下这些页面：无溢出，但中英混排与「标题=label」重复依旧 |


### 3.5 E · 其余屏面与空态

- 截图：35 张 `shots/surfaces-*.png`，量测 `surfaces-1440.json` / `surfaces-viewports.json`
- 条目：28（P0 4 / P1 14 / P2 10）
- 结论：这 10 个屏共用 `zk-screen`，缺陷高度同构——标题说两遍、内距三套、没有内容最大宽、空态"默认插画 + 一句文案"且与主操作分居两处。三处不是审美问题：390 下内容 126px、`/files` 无关闭路径、自动化新建任务按自带帮助文案必 400。

**P0 详情**

- **S-01 390 视口内容列只剩 126px，洞察两表互相重叠** — `main w=126`、`tables[0] w=31/innerW=82`、`tables[1] w=31/innerW=183`、`seg right=389 > 326`。  证据：`shots/surfaces-insights-390.png`、`shots/surfaces-now-390.png`
- **S-02 /files 与 /guide 在 390 下全屏浮层且无关闭路径** — `insp {x:31.2,w:358.8,pos:fixed}`、`.zk-thread w=0`、composer `x=200 w=16`；浮层内无关闭按钮，Esc 也不关。  证据：`shots/surfaces-files-390.png`、`shots/surfaces-guide-390.png`
- **S-03 Now 渲染「其他会话」的审批卡并禁用整个 composer** — `approval {y:573,h:169}`、`sender disabled=true`、placeholder "先处理上面的审批…"。  证据：`shots/surfaces-now-1440.png`、`shots/desk-now.png`
- **S-04 自动化「新建任务」按自带帮助文案填写必 400，错误渲染在弹窗背后** — 帮助写 `every 24h / daily 09:00`，接口实测两者 400（`every needs a whole number of seconds` / `must be HH:MM:SS`）；错误 `automation create failed: 400` 画在 x=288 被 modal(x=460) 遮住。  证据：`shots/surfaces-automation-1440.png`

| ID | 严重度 | 问题 |
|---|---|---|
| `S-01` | P0 | 390 视口下内容列只剩 126px，洞察页表格互相重叠、范围切换溢出列外 |
| `S-02` | P0 | /files 与 /guide 在 390 下变成全屏浮层且没有任何关闭路径，左侧会话列宽度为 0 |
| `S-03` | P0 | Now（落地页）渲染「其他会话」的待审批卡，并因此禁用整个 composer |
| `S-04` | P0 | 自动化「新建任务」按界面自带帮助文案保存必定 400，且失败信息渲染在弹窗背后、表单无字段错误 |
| `S-05` | P1 | 顶栏 h1 与页面 h1 同文重复：每屏把标题渲染两遍 |
| `S-06` | P1 | 页面内距三套并存，主操作按钮掉到说明文字下方（标题行结构未启用） |
| `S-07` | P1 | 没有内容最大宽度：1600 以上表格/统计条/输入框被拉成一片 |
| `S-08` | P1 | 空态用 antd 默认插画 + 一句文案，位置在页面中上部，与标题和主操作脱节，且不告诉下一步 |
| `S-09` | P1 | 加载态、空态、错误态三种形态三种位置：loading 居中、empty 顶部、error 一条红字 |
| `S-10` | P1 | 团队屏一屏两个空态，活动表空行横跨整宽 |
| `S-11` | P1 | 洞察统计条：label 在上、数字在下，5 个数字不等宽左对齐，没落在网格上 |
| `S-12` | P1 | 洞察两张表并排却不等高、列宽不对齐，数字列左对齐 |
| `S-13` | P1 | 检查器右栏与左栏顶边错位 43px，且两栏是两套 tab 语言 |
| `S-14` | P1 | `/files` 未选文件时渲染"开始"内容并高亮"开始"tab，`文件` tab 永远无法处于选中态 |
| `S-15` | P1 | 404：顶栏标题退化成品牌名 `ZeoKeel`，内容贴在左上，页内标题用了 h4 |
| `S-16` | P1 | 轨迹屏信息量低于对话内的工具行：时间线只有圆点+对勾，时间列是消息序号，没有耗时与参数 |
| `S-17` | P1 | 子会话屏：面包屑没有父会话标题与子会话身份，顶栏仍是父会话标题 + 父会话 tabs |
| `S-18` | P2 | 能力页"来源"列直接打印绝对路径，不省略 |
| `S-19` | P2 | 自动化表格：时间表列 code 块换行、日期格式与洞察页不一致、操作列 4 个按钮 |
| `S-20` | P2 | 删除确认用裸 `Popconfirm`：确定是蓝色 primary，焦点留在触发按钮，项目自带 `ConfirmDanger` 未使用 |
| `S-21` | P2 | 运行历史弹窗 720px 只放一个默认空插画 |
| `S-22` | P2 | Now hero 与 composer 在同一轴上（cx 均为 852），但 hero 悬在空态区中心、与主操作相距 500px |
| `S-23` | P2 | hero 字号 24/32 与页面标题 20/28、Empty 文案 14 三档并存 |
| `S-24` | P2 | 顶栏标题（16px）比页面标题（20px）小，信息层级倒挂 |
| `S-25` | P2 | 轨迹/记忆页 tab 与页面标题同名，出现"轨迹→轨迹"的连续重复 |
| `S-26` | P1 | 390 下自动化/团队的表单与表格仍按桌面列宽绘制（未做响应式） |
| `S-27` | P2 | `.zk-empty` 被同时用作"空态容器"和"加载容器"，语义与位置都错 |
| `S-28` | P2 | 洞察范围切换 `Segmented` 只有 24px 高、贴标题顶部，1600 下离标题 1100px |


### 3.6 F · 视觉系统与规范符合度

- 截图：31 张 `shots/shell-focus-tab-1440.png`；含字号/间距/对比度三张统计表
- 条目：28（P0 2 / P1 18 / P2 8）
- 结论：**token 世界与 antd 默认世界没有接线**，且缝正好落在最关键的语义上（次要文字、占位、链接、角标、主按钮前景、焦点环）。可量化证据：脚本算的 24 组 token 对比度全部 ≥4.5，16 项不合格零例外全是 antd 色；间距 100% 合规（唯一自产离群是顶栏 h1 未重置 UA margin 10.72px）；字号没被裸值破坏，但档位语义错了（空态 24 > 页面 H1 20 > 顶栏 16）。

**P0 详情**

- **SYS-01 「设置→深色」在 OS 浅色下产生白底白字** — 与 SH-01/S17 同根：`bodyBg rgb(255,255,255)` + `topbandTitle rgba(255,255,255,.85)`。  证据：`shots/system-forced-dark-appearance.png`
- **SYS-02 会话列表项无法用键盘聚焦** — `li.ant-conversations-item` `tabindex=null / role=null / 无 focusable 祖先`。  证据：`shots/shell-focus-tab-1440.png`

| ID | 严重度 | 问题 |
|---|---|---|
| `SYS-01` | P0 | 「设置→深色」在 OS 浅色下产生白底白字（token 只跟媒体查询） |
| `SYS-02` | P0 | 会话列表项无法用键盘聚焦（核心导航键盘不可达） |
| `SYS-03` | P1 | 5 个 token 被使用但从未定义 → 整块灰阶退化为继承色 |
| `SYS-04` | P1 | 两套灰阶并存：antd `rgba(0,0,0,.45)` 顶替 `--zk-label-tertiary`，空态主文案 3.35:1 |
| `SYS-05` | P1 | 未读角标（`需要审批` 计数）白字 3.27:1 |
| `SYS-06` | P1 | 暗色主色按钮白字 2.17:1（`--zk-on-primary` / `--zk-accent-text` 定义了但没接线） |
| `SYS-07` | P1 | 暗色下导航选中项 3.51:1（accent 文字压 accent 12% 底） |
| `SYS-08` | P1 | 会话列表「选中」与「悬停」是同一个颜色，分不清当前会话 |
| `SYS-09` | P1 | 同一个侧栏两套选中语言（蓝 tint vs 中性灰） |
| `SYS-10` | P1 | 标题层级倒挂 + 同文案两个 `h1` |
| `SYS-11` | P1 | 内容列左缘三套（280 / 288 / 264），/inbox 首行被顶栏裁切 |
| `SYS-12` | P1 | 设置页手拼表单：未用 `Form`/`Form.Item`，4 个 `<label for>` 指向不存在的元素 |
| `SYS-13` | P1 | 焦点指示三套语言，其中 antd 的环只有 2.19:1 |
| `SYS-14` | P1 | 状态语义三套词汇：「运行中」被画成次要文字 |
| `SYS-15` | P1 | `Conversations` 只用了一半能力：无分组、无悬停菜单、无图标、无快捷键 |
| `SYS-16` | P1 | Markdown 标题完全脱离字号阶梯（UA 默认 28/21/16.4/11.6） |
| `SYS-17` | P1 | 输入框 placeholder 1.83:1（浅）/ 2.22:1（深）——主输入框首屏就是这个 |
| `SYS-18` | P1 | 英文界面 `html[lang]` 仍是 `zh-CN`，跳转链接未翻译 |
| `SYS-19` | P1 | 发送按钮无可读名（图标按钮无 `aria-label`） |
| `SYS-20` | P1 | 对比度清单不达标的其余项（antd 链接色 4.10:1） |
| `SYS-21` | P2 | 22 个 token 定义了却从未使用（含整个 11px / 24px 阶梯） |
| `SYS-22` | P2 | 表格列宽是裸 px（12 处，无 token） |
| `SYS-23` | P2 | `h1.zk-topband__title` 保留 UA 默认 margin 10.72px |
| `SYS-24` | P2 | 一个 composer 里四种圆角、两套尺寸语法 |
| `SYS-25` | P2 | 侧栏被 antd 的白色菜单面切开（两种底色、硬边） |
| `SYS-26` | P2 | 暗色表格表面是 antd 的 #1d1d1d，不在 zk 中性阶上 |
| `SYS-27` | P2 | 空/未设值的数字输入框是一个纯空框（无单位、无 placeholder、无步进暗示） |
| `SYS-28` | P2 | 全仓没有 `prefers-reduced-motion` 分支 |
---

## 4. 规范对照

### 4.1 antd 6（官方件该有的形状）

| 面 | antd 6 的做法 | ZeoKeel 现状 | 判定 |
|---|---|---|---|
| 表单 | `Form` + `Form.Item label / extra / tooltip`：label 在上，帮助文字在**下**且小一号；错误用 `validateStatus` + `help`；label 与控件用 `htmlFor` 关联 | 手拼 label，把帮助文字**接在同一行**（"语言 界面语言；缺失时跟随浏览器。"），label 还重复两次；4 个 `<label for>` 指向不存在的 id；非法 JSON / 非法 URL 都能"已保存" | ✗ S06/S07/S11/S14/SYS-12 |
| 空态 | `Empty` 三件：`image` + `description` + `children`（children 放"下一步"按钮） | 只有插画 + 一行灰字，无 `children`；插画漂在页面中上部 | ✗ S-08 |
| 表格 | `columns[].align`、`sorter`、`pagination`、`scroll={{x}}`；数字列右对齐 | 洞察两表并排、表头与数值对齐方式不一致（值 x=744/1196/1356 各异）、无 sorter/分页；团队活动表空行横跨整宽 | ✗ S-12/S-19 |
| 侧栏 | `Layout.Sider breakpoint="lg"` + `collapsedWidth={0}`，窄屏自动折叠 | `Splitter` 固定 264，从未传 `breakpoint`；390px 仍占 200 | ✗ SH-02/SH-10 |
| 页面标题 | `Typography.Title` 层级唯一，一屏一 H1 | 顶栏 h1(16) + 页内 h1(20) 同文；空态 h4(24) 比 H1 还大 | ✗ SYS-10/S-05/S-24 |
| 只读信息 | `Descriptions` 或 `Typography.Text type="secondary"` | "关于"页用表单行摆版本/最新版本/检查时间 | ✗ S18/S20 |
| 危险操作 | `Button danger` + `Modal.confirm` 的 `okButtonProps.danger` | 模型删除用 `Popconfirm`，确定键是蓝色 primary；项目自带 `ConfirmDanger` 未使用 | ✗ S23/S-20 |
| 分段控件 | `Segmented size="small"`，选中态由主题决定 | 用了，但选中态与周围灰字几乎无差别（24px 高、贴标题顶部） | △ S-28 |

### 4.2 antd X 2.10（对话层的关键能力）

| 组件 | X 的能力（该用） | ZeoKeel 现状 | 判定 |
|---|---|---|---|
| `Conversations` | `groupable`（时间分组）、`menu`（悬停 `⋯` 重命名/删除）、`shortcutKeys`、图标、`label` 省略 | 只传 `items` + `activeKey`：无分组、无时间、无悬停操作、无未读点；且 **item 不可聚焦**（键盘整个进不去） | ✗ SH-03/SH-07/SYS-15 |
| `Sender` | `prefix` 是**前置单个控件**；次级控件放 `footer` / `actions`；`header` 放附件行 | `+`、权限选择器、模型选择器全塞进 `prefix` → prefix 实测 **252.8px**，textarea 起点 **x=561.8**（容器左 288），输入列被推到内容区 24% 处；空态高 96px | ✗ C-05 |
| `Bubble` | `variant="filled"/"outlined"`、`placement`、`avatar`、`maxWidth` | 用户 `filled` ✓；助手无 avatar/variant 区分；正文无宽度上限（1440 下单行 80 汉字） | △ T-02/T-03 |
| `Welcome` | 标题+描述居中，`extra` 放推荐提示 | 用了 `Welcome`，但块内 `text-align:start` → 标题视觉重心左偏 86px；无 `extra` 推荐 | △ S-22/SYS |
| `ThoughtChain` | 工具/推理时间线（`status` 点 + `title` + `description` + `content`） | 工具卡自绘、轨迹另画一套，状态点形状/颜色/状态色都不统一（antd 绿 `rgb(82,196,26)` vs `--zk-success #16a34a`） | ✗ T-17/S-16 |
| `Prompts` | 空态给 3 条可点提示，降低首次输入成本 | 未用 | ✗ |

### 4.3 DSH（同机产品，作为"同代基准"）

token 表：`.../dsh-client-ui-theme/src/styles/design-platform.css`；侧栏几何：`.../dsh-client-ui-sidebar/src/client/SidebarRoot.module.css`；审批：`dsh-client-ui-approval`。

| 维度 | DSH | ZeoKeel | 差距 |
|---|---|---|---|
| 主色 | `--dsw-alias-brand-primary` ≈ 近黑 `#0f0f0f`；蓝/靛只作链接与状态强调 | `--zk-accent #0d44f6` 同时承担发送键、导航选中、tab、角标、链接 | 强调色用量大一个量级，屏幕更"吵" |
| 选中态 | 侧栏当前项 = **中性灰**填充（`#ebeef2`），无彩色 | 导航 = 蓝底 `#e6f0ff` + 蓝字；会话 = 灰底 `rgba(0,0,0,.06)`；**同一栏两套**，且都没有 DESIGN §5.5 要求的 2px accent 左条 | 两套语言 + 缺竖条 |
| 边框 | `border-l1 #0000000a` / `l2 #0000001a` / `l3` / `l4`，含 0.5px 发丝 | `--zk-border-l2 rgba(15,17,21,.09)` 接近 ✓ | 基本一致 |
| 圆角 | 4 / 8 / 12 / 16 / 20 / 28 | 4 / 6 / 8 / 12 / 16 / max（composer 里同屏 4 种） | 一致（ZeoKeel 多一档 6、缺 20/28） |
| 间距 | 侧栏 inline 8 / row 12；行高 36–40；12px 节奏 | 导航 item x=8 w=246（左 8 / 右 10）、会话 item x=20 w=223（左 20 / 右 13） | **同栏两种左内距 + 左右不对称** |
| 字号 | 正文 14、次级 −1/−2、代码 11；markdown 标题随用户字号缩放 | 每屏 5 档（12/13/14/16/20）；markdown 标题**零规则**（UA 默认 28/21/16.4/11.6） | 阶梯与语义都失控 |
| 阴影 | 三级极轻（`shadow-lv1/2/3`）+ 面板 0.5px 描边 | 浮层用 antd 默认 | 观感更"贴纸" |
| 空态 | 首页留白 + 居中 composer，**不放插画** | antd `Empty` 小插画 + 一行字，位置漂移 300–500px | DSH 更克制、更聚焦输入 |
| 主题切换 | `body[data-ds-dark-theme]` 驱动**全部** token（含插件） | `@media` 一种、antd `algorithm` 一种，两者不通信 | P0 |

### 4.4 主流 Agent 产品（行为基准）

| 面 | ChatGPT | Claude / Claude Code | Cursor / Copilot | Kimi / 豆包 | ZeoKeel 现状 | 差距 |
|---|---|---|---|---|---|---|
| 会话列 | Today/昨天/7 天分组，悬停 `⋯`，两行截断 | 项目→会话两级 + 状态点 | 工作区树，密度高 | 分组 + 悬停操作 | 单列纯标题，无分组/时间/未读/悬停 | 缺时间锚点与操作入口 |
| 消息区 | 助手无气泡、正文 ≈48rem 居中；用户右对齐浅灰气泡 | 正文 + 工具单行 chip | 内联 diff / 终端块 | 气泡 + 引用卡 | 助手无气泡但**不限宽**（1128–1632） | 长文可读性差 |
| 工具调用 | 单行 "Used tool ⌄"，点开看入参与结果 | `Bash …` 单行 + 状态点 + **耗时** | 终端 / diff 内联 | 折叠卡 | 单行 + 文字"完成/运行中"，无耗时、无退出码、状态用灰字 | 缺可操作信息 |
| composer | 底部一行：+ / 模型 / 工具；发送键 32px 圆形；占位文字**左对齐** | 权限模式左下、快捷键提示右下 | `@` 上下文 + 模型选择 | 模式开关在输入框下方 | 三个控件塞 prefix 挤走占位文字；四种状态提示互相矛盾 | 输入区第一眼就不像输入区 |
| 审批 | 危险动作红、显式确认、无默认焦点 | 允许一次 / 始终允许 / 拒绝 + 影响面 + 展开参数 | 逐条批准 + diff 预览 | 弹层确认 | `仅此一次 / 拒绝 / 允许一次`，危险动词居中且无红色；Esc 有歧义 | 危险动词不突出、顺序不合"安全在右、危险在左" |
| 空态 | 一句引导 + 推荐提示 | 留白 + 输入框 | 快捷键提示 | 能力卡片 | antd Empty 插画悬空，无推荐 | 缺"下一步" |

### 4.5 与四类基准的 10 个关键差距（归纳）

| # | 基准怎么说 | ZeoKeel 怎么做 | 差在哪 |
|---|---|---|---|
| 1 | antd X `Sender` 的 `prefix` 只放**一个**前置控件，次级控件在 `footer`；DSH 的工具条在文本**下方一行**；ChatGPT 同 | 3 个控件塞进 `prefix`（253px），输入列被推到内容区 24% 处 | 输入区第一眼不像输入区 |
| 2 | DSH 选中态用**中性灰**，品牌主色近乎黑；antd 默认也不鼓励主色铺满 | 蓝色同时承担主色/选中/焦点/角标/链接 | 强调色预算失控，屏幕"吵" |
| 3 | antd `Form.Item` 的 label 在上、`extra` 在下；DSH 设置行是"标题 + 说明 + 控件"三段 | label 重复两次、help 接在 label 同行、4 个 `for` 指向不存在的 id | 表单看起来像草稿 |
| 4 | 主流会话列都按时间分组 + 悬停 `⋯` + 未读点；antd X `Conversations` 原生支持 | 只传 `items`，协议里的 `updatedAt/unread/running` 全被丢弃 | 侧栏退化成标题清单 |
| 5 | 主流助手正文 ≈48rem；DSH `ProseMeasure` 语义 | 正文 1128–1632px，单行 80 汉字 | 长回答读不动 |
| 6 | 主流一屏一个 H1；antd `Typography.Title` 层级唯一 | 顶栏 h1(16) + 页内 h1(20) + 空态 h4(24) | 层级倒挂 |
| 7 | 审批：危险动作红、安全动作主色、无默认焦点、Esc 只在该区域有焦点时生效 | 三个动作同为默认样式，拒绝 hover 变蓝；Esc 全局抢键且在 IME/下拉中误触发 | 安全审查被削弱 |
| 8 | 空态：一句说明 + 一个"下一步"（antd `Empty.children` / `Prompts`） | 插画 + 一句灰字，位置漂移 300–500px | 空态不产生行动 |
| 9 | 主题：一个真源驱动所有 token（DSH `body[data-ds-dark-theme]`） | `@media` 与 antd `algorithm` 两个真源 | 白底白字 |
| 10 | 窄屏：<768 一律"抽屉 + 汉堡"（ChatGPT/Claude/Cursor） | 侧栏固定 200px 与正文并排 | 手机不可用 |


---

## 5. 修复路线

### 5.1 P0（8 条，建议 1–2 天内全部落地）

| # | 修什么 | 具体动作 | 验收 |
|---|---|---|---|
| 1 | **主题双源** | `gen-tokens.mjs` 同时产出 `:root[data-theme='light']` / `[data-theme='dark']`；`App` 里 `documentElement.dataset.theme = appearance`；`color-scheme` 跟随 | OS 浅 + 设置深 → body/侧栏/顶栏/表单全部深色；OS 深 + 设置浅 → 全部浅色；两组各截 3 屏 |
| 2 | **审批不再全局阻断** | `disabled` 只看 `approvals.some(a => a.sessionId === sessionId)`；`/` 屏只保留 `creating` 禁用；他会话审批只给角标 | 会话 A 有待审批时，会话 B 与 `/` 的 composer 可用 |
| 3 | **Esc 三层守卫** | `esc-arbiter` 开头 `if (key.defaultPrevented \|\| key.isComposing \|\| key.keyCode === 229) return false`；浮层打开时停用 `deny-oldest` claim；composer hint 增加「审批在屏」分支 | 下拉内 Esc 只关下拉；拼音候选 Esc 不拒审批；同屏不再出现两条 Esc 承诺 |
| 4 | **会话列表键盘可达** | `Conversations` 外层 `role="listbox"`，item `role="option"` + `aria-selected` + roving `tabIndex`，↑↓/Home/End/Enter | Tab 一次进列表，方向键可切，Enter 打开 |
| 5 | **移动端形态切换** | `Layout.Sider breakpoint="lg" collapsedWidth={0}`；`<900px` 改抽屉 + 顶栏汉堡（`aria-expanded`/`aria-controls`）；`#zk-main{min-width:0}` | 390px：内容占满宽、`scrollWidth === clientWidth`、设置表单可见 |
| 6 | **检查器窄屏可关闭** | 窄屏用 antd `Drawer` 承载 `InspectorPanel`（`closable`，`onClose → navigate`）；Splitter 左 panel `min={0}` | 390px 下 `/files` 能关闭并回到会话 |
| 7 | **自动化帮助文案与解析器对齐** | 要么支持 `every 24h`/`daily 09:00`，要么把帮助改成 `0 8 * * *`；错误渲染进弹窗内（`Form.Item help` + `Alert`） | 按界面文案填写能保存；失败时字段级报错可见 |
| 8 | **内部实现不上屏** | 删除「其它设置（网关有、界面未接线）」；收件箱行标题改「工具名 · 目标」，参数折叠 | 9 页设置与收件箱不再出现 `session.*` key 或原始 JSON |

### 5.2 P1（10 条主线，3–5 天）

| # | 主线 | 涉及条目 | 动作 |
|---|---|---|---|
| 1 | **布局 token 接线** | R1 / SH-10 / T-02 / S-07 | `gen-tokens` 补 `--zk-sidebar-width / inspector-width / transcript-width / prose-measure / settings-width / toast-width / palette-width`；替换 `app.tsx:19`(264)、`app-routes.tsx:38`(380)；正文列 `max-width:960px`、prose `max-width:640px` |
| 2 | **统一页面骨架** | SH-04 / S-05 / S-06 / S-24 | 一个 `<Screen>`：唯一 H1（来自路由表）+ 页面内距 24（shell 提供 gutter）+ 内容 max-width + loading/empty/error 同槽位；删掉重复 H2 |
| 3 | **侧栏对齐与解剖** | SH-05/06/07/08 | 导航与会话统一左内距 8 + 行内 12；当前项 = 中性 tint + 2px accent 左条；`Conversations` 开 `groupable` + `menu` + `unread` 点 |
| 4 | **composer 重排** | C-05 / C-10 / C-13 | `prefix` 只留 `+`；权限/模型移入 `footer` 左区（与 DSH/ChatGPT 一致）；`minRows:1`；补 `:focus-within` 2px 焦点环；控件统一 28/36 |
| 5 | **设置表单正规化** | S06-S14 / SYS-12 | 改用 `Form.Item`，label 上、`extra` 下、`validateStatus` 出错误；枚举改 `Select`；数字框补单位/范围/默认值；开关行改"标签左、控件右" |
| 6 | **三态统一** | S-08/S-09/S-27 / SYS | `Empty` 三件套 + 一个主操作；loading 用 `Skeleton`（不借 empty 容器）；error 用 `Alert` 并保证不被遮罩吞掉 |
| 7 | **antd 色板接线** | R3 / SYS-04~20 | `theme.ts` 补 `colorTextSecondary / Description / Placeholder / Link / TextLightSolid / Error`；焦点环统一 `--zk-focus`；角标改 `--zk-danger` 深红 |
| 8 | **状态语汇收口** | T-04/05/06/07/17 / SYS-14 | 流式中给"生成中"指示（光标/进度条）；停止后显示"已停止"；工具行＝`状态点 + 工具名 + 摘要 + 耗时 + 展开`；状态色统一走 `--zk-*`；错误枚举统一经 `provider-error` 映射 |
| 9 | **审批信息一致** | C-06/06b/07/08/09 / S-31 | 卡片与收件箱共用同一套信息与动作；影响对象（URL/命令/路径）进卡片正文；按钮顺序 `[拒绝] [仅此一次] [始终允许]`，拒绝 danger；快捷键文案写「允许一次」 |
| 10 | **a11y 收尾** | SH-11/13 / SYS-18/19/28 | 发送键/停止键 `aria-label`；菜单 `aria-current`；`html lang` 随语言；`prefers-reduced-motion`；触控目标 ≥28px |

### 5.3 P2（打磨清单）

空态加推荐提示（`Prompts`）；工具卡补耗时/退出码/复制；toast 宽度回 320 并与顶栏错开、审批等待改粘滞；轨迹与工具卡共用状态点；`/files` 空选择时的 tab 高亮；子会话补面包屑；洞察用 `Statistic` + `tabular-nums` + 数值右对齐；自动化日期格式统一；模型删除用 `ConfirmDanger`；`关于` 页去裸 URL / 中英混排；markdown 标题接 token；22 个未使用 token 清理或接线；打包按路由拆包（当前单 chunk 2.4MB / gzip 782KB）；折叠/展开加过渡（≤200ms）。

### 5.4 验收（三个断言脚本尚不存在，需按下面的条件实现后接入 CI）

```bash
cd web && npm run typecheck && npm run lint && npm run test

# 结构断言（WEB-CLIENT §9.6 的"布局框位置"落地）
node .cache/ui-audit/assert-layout.mjs
#   - 顶栏标题 x == 正文 x（同一 gutter）
#   - #zk-main 内容宽 ≤ 960，prose ≤ 640
#   - 390px: documentElement.scrollWidth === clientWidth，且 `.zk-composer` 在视口内
#   - 每屏可见字号档数 ≤ 4

node .cache/ui-audit/assert-theme.mjs
#   - OS 浅 + 设置深：body/侧栏/顶栏背景全部等于暗色 token 值
#   - 所有可见文本对比度 ≥ 4.5（大字 ≥ 3）

node .cache/ui-audit/assert-a11y.mjs
#   - 会话列表 Tab 可达；发送键有 aria-label；html[lang] 跟随语言
```

---

## 6. 附录

### 6.0 10 张关键截图（已随报告入库）

`docs/audit/2026-11-24/shots/`：

| 文件 | 证明什么 |
|---|---|
| `lead-theme-dark-os-light.png` | P0-1：浅色系统 + 设置「深色」→ 白底白字 |
| `shell-root-390.png` | P0-2：390px 侧栏 200 + 内容 126，逐字换行 |
| `surfaces-files-390.png` | P0-6：检查器窄屏全屏浮层、无关闭路径 |
| `desk-now.png` | P0-3：落地页被他会话审批锁死 + 空态插画悬空 |
| `composer-seeded-now-approval.png` | P0-4：卡片「Esc 拒绝」与 hint「Esc 清空草稿」同屏矛盾 |
| `shell-focus-tab-1440.png` | P0-5 + 焦点环三套语言 |
| `desk-inbox.png` | P0-8：收件箱行标题=原始 JSON、页内距 0 |
| `desk-settings-appearance.png` | label 重复两次、help 同行、内部 key 上屏 |
| `desk-thread.png` | 左缘三套 + 侧栏两套选中语言 + 审批跨会话 |
| `wide-thread.png` | 1920 下正文被拉到 1632px |

原始全量（297 张）：`.cache/ui-audit/shots/`（该目录被 gitignore，按 §1.1 可一键重建）。

### 6.1 量测原始值（节选）

```
sidebar            264 | topband padding 0 16 | topband title x=280 | body x=264 pad 24 → 288
menu item x=8 w=246 | icon x=32 | label x=58
conversations item x=20 w=223 | label x=28
nav selected  bg rgb(230,240,255) color rgb(13,68,246)  (无左条)
conv selected bg rgba(0,0,0,0.06) color rgba(0,0,0,0.88)
composer .ant-sender x=288 w=1128 | prefix w=252.8 | textarea x=561.8 w=801.2 | send 32×32
approval card target line x=982.3 w=416.7 (nowrap, 右对齐)
settings nav 264..484 (220) | content x=484 pad 8/24 | footer h=49
inbox .zk-inbox pad 0 (行通栏 1176) | team .zk-screen pad 24
390px: .zk-sidebar 200 | #zk-main w=126 | scrollWidth 322 | inspector fixed w=358.8, .zk-thread w=0
1920px: thread body w=1632 | 单行中文 ≈80 字
字号分布（7 屏 n=242）: 14×174 / 12×31 / 16×14 / 13×14 / 20×8 / 24×1 / 11×0
对比度不合格 16 项，全部为 antd 默认色（1.83–4.10:1）；ZeoKeel 自身 24 组 token 全部 ≥4.5
```

### 6.2 证据索引

| 主题 | 截图 |
|---|---|
| 深色双源（白底白字） | `shots/lead-theme-dark-os-light.png`、`shots/shell-dark-setting-*.png`、`shots/settings-dark-reload.png`、`shots/system-forced-dark-appearance.png` |
| 390 不可用 | `shots/mob-now.png`、`shots/shell-root-390.png`、`shots/surfaces-insights-390.png`、`shots/surfaces-files-390.png` |
| 审批跨会话阻断 | `shots/desk-now.png`、`shots/composer-crosssession-other-thread-1440.png` |
| 左缘三套 / 侧栏 | `shots/desk-thread.png`、`shots/shell-team-gutter-1440.png`、`shots/shell-inbox-1440.png` |
| 1920 正文过长 | `shots/wide-thread.png` |
| 设置 label 重复 / 内部 key | `shots/desk-settings-appearance.png`、`shots/desk-settings-about.png` |
| 收件箱原始 JSON | `shots/desk-inbox.png` |
| 空态漂移 | `shots/desk-team.png`、`shots/desk-automation.png`、`shots/desk-capabilities.png` |
| 洞察表格/统计 | `shots/desk-insights.png`、`shots/surfaces-insights-1440.png` |
| composer 输入列 | `shots/composer-idle-1440.png`、`shots/composer-seeded-now-approval.png` |
| 明暗对照 | `shots/lead-dark-thread2.png`、`shots/system-dark-thread.png、shots/system-dark-now.png` |

### 6.3 本次未覆盖 / 环境限制

1. `plan-dialog` / `ask-dialog` 在当前夹具下**不可达**（假模型只有 `plain*/tool*/slow*`；`ask_user` 与 plan `reviewing` 无触发路径），只做了代码层审查（C-19）。
2. `toast.posted` 在夹具下造不出（SH-19 只按代码给结论）；审批在等待中自行消失时界面**没有任何说明**（`approvals-context.tsx:46` 静默过滤）。
3. 未测真机触摸与 iOS Safari；移动端仅为 390×844 视口模拟。
4. 渲染性能只测了本地首屏（FCP 416ms），未做 4G 节流；单 chunk 2.4MB 是已知风险。
5. 深色模式的对比度在 `@media` 生效时（OS 深色）计算；"设置深色 + OS 浅色"的路径本身不可读，已单列 P0。

### 6.4 建议的下一步

1. 先修 8 个 P0（1–2 天），每一项都有可机器断言的验收条件，避免"改完靠肉眼"。
2. 把 `assert-layout / assert-theme / assert-a11y` 三段写进 CI，作为 WEB-CLIENT §9.6 视觉回归的**结构断言**首版。
3. 骨架（`<Screen>`、`<SettingsRow>`、`<StateSlot>`）与布局 token 一起做，否则逐屏修会返工三次。
4. 主题与色板（R2/R3）一次性收口，能同时清掉 1 个 P0 + 16 项对比度 + 3 套焦点环 + 3 套状态色。