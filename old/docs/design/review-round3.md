# 设计/UX 独立评审（round 3）

> 角色：UI/UX 研究员（Team 成员 ux-research） · 任务：task-25 · 日期：2026-11-05
> 评审对象：`HEAD b3ff65f`（提交时间 15:01）的 18 张品牌预览 + 设计与代码；参考 `docs/design/native-ui.md`（§1–17）、`docs/decisions/0001–0004`、`docs/research/00-report.md`、`docs/research/03-ux-design.md`
> 评审方式：只读。18 张预览逐张 `read_image` + 关键区域 3–8 倍放大 + ImageMagick 取色按 WCAG 2.x 公式计算；代码用 `git show/git grep HEAD` 抽查（避免误读并行未提交改动）；`git archive HEAD` 导出到 `/tmp` 后跑 `gofmt/vet/go test ./...`。
> **未改动代码/图片/其他文档**；未 `git add/commit`；唯一产物是本文件。

---

## 0. 一句话结论

**品牌化方向成立、九个视图已经整体从"DSH Web 复刻"走成"ZeoKeel 桌面应用"，原生控件与桌面 P0 是真实落地而不是文档口径；但当前观感是"统一、克制、能用"，还不是"出彩、完成"——收口前必须修的是破坏性操作无确认（数据风险）与浅色三级文字系统性低于 WCAG AA；另有一批一致性、深色字标、中英混排、系统字号覆盖与视觉验收覆盖的问题。**

具体验收判断（本评审口径）：

| 维度 | 判断 | 说明 |
| --- | --- | --- |
| 品牌一致性 | **7.5/10** | 色板/字号/图标/控件语法整体统一；扣分在深色字标残边、两套卡片圆角与两套 tab 选中语法未成文 |
| 层级与密度 | **7/10** | 列表页与设置页层级清晰；Home 仍是 landing page 式大留白；`Settings` 内容区重复标题 |
| 浅/深色对比与可读性 | **6/10** | 深色全部通过；浅色三级文字 3.26–3.62:1 系统性不达 AA（160 处使用） |
| 桌面感 vs Web 感 | **7.5/10** | 菜单/通知/窗口状态/原生控件已落地；Settings 仍是应用内模态、滚动条仍是 hover overlay、Home 构图仍是 Web |
| 同类交互一致性 | **6.5/10** | Switch/主按钮/焦点环统一；关闭方式、破坏性操作、空态 CTA、tab 选中语法不一致 |
| 可签收程度 | **不通过（有条件）** | 结构可签收，观感/无障碍/覆盖度需下一轮 |

**诚实评价**：这是一次质量不错、执行克制的品牌迁移，不是"设计失败"；但也不到"可以停止迭代的完成态"。最大价值不是这 18 张图有多惊艳，而是**终于把 mygo 原生控件、桌面交互和品牌主题接成了一条线**；下一轮应该修 bug 与补验收，而不是再大面积换视觉。

---

## 1. 评审范围、方法与限制

### 1.1 材料

- 预览：`docs/design/preview/` 18 张（9 个视图 × 浅/深）+ `README.md`；均为 `ui.Render(..., 1600, 1000, 1)` 的 live fixture 截图（中文默认）。
- 文档：`docs/design/native-ui.md`（402 行）、ADR 0001–0004、README、STATUS、`docs/research/00-report.md`、`docs/research/03-ux-design.md`。
- 代码：`internal/brand`、`internal/design`、`internal/shell`、`internal/app`、`internal/automation` 的 HEAD 快照。

### 1.2 复验结果（HEAD b3ff65f，干净导出到 `/tmp/repo-head`）

```text
gofmt -l ./cmd ./internal  → 空
go vet ./...               → HEAD_VET_EXIT:0
go test -count=1 ./...     → HEAD_TEST_EXIT:0（17 包；shell 37.747s、session 34.662s）
```

> 任务背景"HEAD 全绿"属实。**评审期间工作树有并行未提交改动**（12 个代码文件，`internal/app/{app,notify}.go`、`internal/session/controller.go`、`internal/shell/{app,composer_style,layout,markdown,menus,toolcards,workspaces}.go` 等），当时工作树一度不可编译；随后 `docs/decisions/0004…`、`docs/design/preview/session-native-*.png` 也被并行任务改动；评审末尾工作树还出现未跟踪的 `docs/design/preview/settings-window-native-*.png`、`docs/design/preview/toolcard-native-*.png` 与 `docs/design/perf.md`（说明独立设置窗口/工具卡原生化可能已在并行推进）。**这些都不是本次评审对象**；本报告以我读取时的 HEAD 版本为准（session 预览读取时尚未被修改）。下一轮请以最终提交的预览+代码再复核本报告结论（尤其 P1-4 与工具卡相关项）。

### 1.3 限制

- 只看 headless 静态帧：**看不到焦点环、hover、动效、滚动条、IME 候选窗、系统通知真实外观**；这些只能从代码/令牌推断，标注为"风险"而非"事实"。
- 未做真机（macOS/Windows/Linux）验证；无屏幕阅读器/高对比/TextScale 实际用户验证。
- 对比度取色基于抗锯齿像素的极值，误差约 ±0.05:1；数值与代码中声明的色板一致，可信。

---

## 2. 逐屏观感评审（浅色 / 深色）

> 统一评述：九个视图的颜色语言高度一致（品牌蓝 `#0D44F6` / 深色天空蓝 `#3CBBFA`、中性表面、状态色），icon 统一 Lucide 线条、字重集中在 400/500/600，控件（Switch/Select/Segmented/SearchField/Button）外形统一。深色表面层级（页面 `#0D0D14` < 侧栏 `#13131C` < 卡片 `#1B1B26`）克制但成立。

| 视图 | 浅色观感 | 深色观感 | 主要问题 | 桌面感判断 |
| --- | --- | --- | --- | --- |
| **Home** `home-native-{light,dark}.png` | 品牌 hero（mark 44 + ZeoKeel 字标 + “今天想构建什么？”）成立；composer 卡片层级干净；侧栏主按钮醒目 | 深色层次好；**hero/侧栏字标四角出现白色残边**（见 §3.1） | 1600×1000 仍是大留白 landing 构图，无"最近会话/继续上次"密度块；侧栏"新建会话"与主区 composer 抢夺注意力 | 品牌 ✅，构图仍偏 Web |
| **Session** `session-native-{light,dark}.png` | 全组最强：品牌 tint 用户气泡、markdown 代码块 + Copy、工具卡 ghost 行 + tint 详情、composer focus 态清晰、用量行 | 深色对比全部达标；tint 层次仍成立 | 用户气泡 `#E6ECFE`、工具详情 `#F1F4FE` 两种蓝 tint 过于接近，扫描时层级弱；`已完成` 等 footer 文字 3.38:1；header 两个相邻 chip（ghost `标准模式⌄` vs 描边 `☐ 计划模式 关`）语法不统一 | ✅ 强 |
| **Sidebar** `sidebar-native-{light,dark}.png` | 原生 Sidebar 选择/方向键/键盘语义；行内动作**常显**（上一轮 hover-only 问题已修）；分组清楚 | 深色选中 tint 舒服；字标同样有白边 | `工作区` 标签 3.47:1；会话行 `刚刚` 与 `⋯` 紧贴（约 4 DIP）；行尾 `⋯` 是文字字形而工作区行用图标 | ✅ |
| **Settings** `settings-native-{light,dark}.png` | 面板/导航/分组卡成形；原生 Select/Switch/主题卡正确；800×760 模态居中 | 深色面板与 scrim 分离好；主题卡选中态清楚 | **仍是应用内模态 + scrim**（不是独立设置窗口）；头部 **“关闭”文字按钮 + “×” 两个关闭入口**；内容区“通用”与导航“通用”重复；行说明 3.26:1 | ⚠️ 半 Web |
| **Plugins** `plugins-native-{light,dark}.png` | 页头/说明/刷新/主按钮 + 两组卡片；36×36 tint 图标盘 + 原生 Switch；层级清楚 | 深色卡片、图标盘、Switch 全部协调；`实验性` 徽章对比 5.85:1 通过 | 侧栏“新建会话”实心蓝 + 页头“添加插件”实心蓝，**双主按钮**；卡片圆角 16 vs 设置卡片 10（见 §3）；无空搜索/加载/错误预览 | ✅ |
| **Automation** `automation-native-{light,dark}.png` | 标题/筛选/搜索/任务卡 + 原生 Switch + 行操作；一屏信息量合适 | 深色好；禁用态 pill 清楚 | **中文界面里出现 “Daily at 00:00 / Every hour / Every 15 minutes”**（未本地化）；删除按钮与其余操作同为中性 ghost、**无确认**；一行 4 个同权重按钮略平 | ✅ |
| **Trajectory** `trajectory-native-{light,dark}.png` | 时间轨 + 事件色点 + 本地化标签 + `×count` pill；raw payload 展开块可读 | 深色事件色点（蓝/绿/橙/灰）可辨 | 时间戳/用量 3.4:1；tab 选中是 **accent 下划线**，与右侧栏 tab 的 pill 选中语法不同；无页标题（仅小标题行，按子视图可接受） | ✅ |
| **Right sidebar（Start）** `right-sidebar-native-{light,dark}.png` | 三张引导卡 + 圆形图形标；图标盘统一；header 控件齐全 | 深色卡片/图标盘一致 | `新建终端` 卡带 chevron、另两张没有 → 同类卡暗示不同交互；面板 header 高 44 DIP 与主区 header 56 DIP 不齐；tab 用 pill 选中（与 Session 下划线两套） | ✅ |
| **Right sidebar（Files）** `right-sidebar-files-{light,dark}.png` | 面包屑 + Reload + 文件行；图标/文案干净 | 深色可读 | 文件列表极简（无大小/时间列），空目录/加载/错误态未见预览；唯一动作是 Reload，权限/隐藏文件提示不可见 | ✅ |

---

## 3. 品牌一致性（色板 / 圆角 / 间距 / 字号 / 图标 / 字重）

### 3.1 做对的部分（应保持，勿在下一轮推翻）

1. **色板与语义色跨屏一致**：accent、tint、边框、状态色、badge、Switch、焦点都来自 `internal/brand` + `design.Tokens`；深色不是简单反色，而是有品牌紫倾向的夜色（`#0D0D14/#13131C/#1B1B26`）。
2. **令牌化程度高**：非测试代码中硬编码 hex 仅剩 2 处（`internal/shell/plugins.go:34-35` 的 `#6E7278/#C2C6CA`，注释明确写为 stand-in），其余全部走 `design`/`brand`/`Theme.Space/Rem/Radius`（对比上一轮 DSH 采样色是质变）。
3. **字阶与字重统一**：24/20/16/14/13/12/11 的 TypeRole + 400/500/600 字重使用稳定；页面标题 600、正文 400、强调 500/600；系统字体栈（PingFang/微软雅黑回退）与 README/ADR 一致。
4. **图标统一**：Lucide 等价线稿 + `currentColor`，尺寸 16/20 DIP；插件/设置/右栏图标风格一致。
5. **品牌文案去 DSH 化完成**：`Into the Unknown`、`Preview` 徽章、鲸鱼图标已消失，Home 用自有 tagline；中文默认语言成立。

### 3.2 不一致 / 缺陷

| # | 问题 | 证据 | 建议 |
| --- | --- | --- | --- |
| B1 | **深色主题字标使用浅色位图，四角出现白色残边** | `internal/shell/assets.go:12-23` 只 `embed mark.png`（48×48 位图）；`mark.png` 边缘含白色圆角容器像素（(2,2)=白色 α0.96）；`logo/logo.svg:28` 是白色圆角容器、`logo/logo-dark.svg:28` 是黑色容器；`home-native-dark.png` hero 与 `sidebar-native-dark.png` 品牌行可见白色弧角。`native-ui.md:262` 却声称“品牌 UI 全部是文本 + SVG（无位图资产）” | 深色使用 `logo-dark.svg`（或生成 dark mark）；字标改 `ui.SVG`/矢量，至少提供 2x 位图；同步修正设计文档 |
| B2 | **位图字标在 2x/3x DPI 会重采样**：48px 位图在 44 DIP（2x 需 88px）和 24 DIP（2x 需 48px，刚好）使用 | `assets.go` + `home.go` hero 尺寸；`preferences_test.go` 的 `TestHighDPIRenderSmoke` 只断言尺寸/非空/控件在窗口内，不验证清晰度 | 与 B1 一起换 SVG；2x 测试至少截图人眼复核 |
| B3 | **同一“卡片”角色两套圆角**：列表页卡 16，设置/右栏/Start 卡 10 | `design/components.go:27-28`（`RadiusMD=brand.Radius=10`、`RadiusLG=16`）；`plugins.go:240`、`automation_ui.go:239` 用 `RadiusLG`；`settings_ui.go:360/523`、`start_tab.go:125` 用 `th.Radius` | 若是有意区分 pageCard/panelCard，写进规范；否则统一为 12 或 10 |
| B4 | **两套 tab 选中语法**：主区 tab = 文字 + 2 DIP accent 下划线；右栏 tab = `BgHover` pill | `trajectory.go:99-125`（下划线）；`right_sidebar.go:365-400`（pill） | 保留差异可以（主视图 vs 面板），但需成文并统一 hover/选中权重；否则统一 |
| B5 | **同一功能两种字形**：会话行 `⋯` 是文字字形，工作区/header 用 SVG 图标；Start 的 `新建终端` 卡带 chevron、另两卡无 | `workspaces.go` 会话行注释（§9.2-4 已知）；`start_tab.go:147-172` | 统一用同一套 16 DIP 图标；Start 三卡若行为不同，用副标题/角标表达，不要只在其中一张加 chevron |
| B6 | **相邻 header chip 语法不统一**：`标准模式 ⌄` 是无容器 ghost，`☐ 计划模式 关` 是描边 pill；后者同时用 checkbox + “关” 文字表达同一状态 | `session-native-{light,dark}.png` header；`home_live_menus.go`/`session_live.go` 的 chip/plan 实现 | 二选一：都做成 ghost chip（状态用图标/点），或都做描边 chip；去掉状态双编码 |
| B7 | **双主按钮竞争**：Plugins/Automation 页同时存在侧栏实心蓝 `新建会话` 与页头实心蓝 `添加插件/新建` | `chrome.go:102`（accent 背景的新建会话按钮）；`plugins.go` 页头 `PrimaryButton`；`automation_ui.go` 页头 `PrimaryButton`；截图左侧+页头两个蓝块 | 列表页把侧栏新建降为 secondary，或页头主按钮改为 tint/outline；一屏一个主操作 |
| B8 | **文案/格式残留**：Automation 计划描述英文（`Daily at 00:00` 等）；next-run 用 `2006-01-02 15:04`；字体单位 `14 px`；Trajectory raw key 英文（技术视图可接受） | `docs/design/preview/automation-native-light.png`；`internal/automation/cron.go:174-188` 的 `Describe()` 硬编码英文；`automation_ui.go:269-273` 直接使用 | `Describe(spec, lang)` 或返回 i18n key；next-run 用 `time.Format` 本地化；补一条"中文界面禁止英文 label"测试 |
| B9 | **文档/编号漂移**：`native-ui.md` 缺 §5、§6（4→7），§13 内小节写成 “12.1/12.2/12.3/12.4”，§16 引用不存在的 §5；`doc §12.2` 与 B1 的"无位图资产"矛盾 | `native-ui.md:94/125/282/291/316` 等 | 下一轮顺手修文档编号与事实声明（§17 验收口径也应补"预览状态矩阵"） |

---

## 4. 可访问性 / 可读性（WCAG 抽样 + 设计规则）

### 4.1 取色与对比（WCAG 2.x 公式，浅色/深色都测）

| 配对（代码令牌） | 浅色 | 深色 | 判定（正常文本 AA=4.5:1） |
| --- | --- | --- | --- |
| 主文字 / 页面背景 (`Text/Background`) | **18.90:1** | **16.32:1** | ✅ |
| 次文字 / 页面背景 (`TextSecondary`) | **6.04:1** | **10.46:1** | ✅ |
| 三级文字 / 卡片强表面 (`TextTertiary` on `#F1F3F5`) | **3.26:1**（实测 `#7E8796`） | **5.64:1** | ❌ 浅色 / ✅ 深色 |
| 三级文字 / 侧栏 (`#F9FAFB`) | **3.47:1** | **7.09:1** | ❌ 浅色 / ✅ 深色 |
| 三级文字 / 白底 | **3.62:1** | — | ❌ 浅色 |
| AccentText / Accent（主按钮） | **6.61:1** | **8.71:1** | ✅ |
| `实验性` 徽章（实测） | **5.06:1** | **5.85:1** | ✅ |
| Placeholder（实测） | **6.04:1** | **10.46:1** | ✅ |
| 焦点环有效色 vs 相邻背景（代码合成） | **2.73:1** | **4.84:1** | ⚠️ 浅色 < 3:1（WCAG 2.2 SC 2.4.11 焦点外观） |
| 轻边框 / 背景（`Border` 合成） | 1.24:1 | 1.43:1 | ⚠️ 对"仅靠边框识别"的次级按钮/卡片偏低 |
| 禁用发送箭头 / 其填充 | 2.24:1 | 2.59:1 | 禁用态不强制（WCAG 豁免），但深色下略像可点 |

**结论**：深色主题可读性全部通过，值得肯定；**浅色主题的三级文字系统性不达 AA**。三级文字不是装饰：它承载设置行说明、时间戳、用量、区块标签、空态说明（`grep LabelTertiary` 160 处，`settingsText` 62 处、`nativeText` 47 处、`design.Type` 引用 115 处中的相当部分）。`docs/decisions/0002-brand-theme-foundation.md` §4.1 自己记录 `TextTertiary = 3.6:1` 并注为“标签级文字”，但 11–13px 的正常文本同样适用 4.5:1；`internal/brand/brand_test.go:138-160` 的 `TestPaletteContrast` 只覆盖 primary/secondary/warning/accent text，**没有覆盖 tertiary**，所以测试是绿的、问题仍在。

**建议**：把浅色 `TextTertiary` 从 `#7E8796` 提到 ≥4.5:1（例如 `#626B7A` 在白底 5.38、在 `#F1F3F5` 上 4.84、在侧栏 5.15；或 `#5F6878` 更稳），同时把 tertiary 三背景加进 `TestPaletteContrast`；若品牌坚持浅灰，则必须把 tertiary 限定为装饰性文本，并在设计规范中禁止承载信息。

### 4.2 HighContrast / TextScale / ReduceMotion（对照 `native-ui.md` §12）

| 规则 | 现状 | 判定 |
| --- | --- | --- |
| HighContrast | 主题边框/次文字/焦点/滚动条增强，`design.Tokens` 同步（`preferences.go` `shellTokensForPreferences`）；实测三级文字增强后浅色约 5.37:1 | ✅ 已实现（未见 HC 预览图，需补） |
| ReduceMotion | `syncShellPreferences` 调 `DisableAnimations()`，语义动效保留；`preferences_test.go` 覆盖 | ✅ 已实现 |
| TextScale | **仅主题基座字号与会话正文**按 scale 缩放（`preferences.go:90-100` 只改 `th.FontSize`；`shellTextScaleOf`）；而自绘文本大量使用**固定** `design.TypeMetrics`（12/13/14/16/20/24），不随系统缩放 | ⚠️ **部分**：`native-ui.md` §12.4 已诚实记录为遗留，但 README/STATUS 的"系统偏好接入"对外口径会让人误以为已完整 |
| 2x DPI | `TestHighDPIRenderSmoke` 通过（尺寸/非空/控件在窗内）；但均为位图 mark 的清晰度问题（B1/B2）不在断言内 | ⚠️ 冒烟而非验收 |
| 真机 IME / 屏幕阅读器 / HighContrast 视觉 | 未做 | ❌ 证据不足（不能宣称"已验证"） |

### 4.3 其他可读性细节

- **最小字号 11px**（TypeXXXS）用于“停用” pill、元数据；叠加上浅色 tertiary 低对比，实际可读性差。建议最小信息字号 ≥12px，或 11px 仅用于非信息性装饰。
- **焦点可见性**：浅色 Focus = accent 55% 透明（`internal/brand/palette.go:152`），合成后约 `#7A98FA`，对白底 2.73:1；HighContrast 下不透明才达标。默认浅色主题的键盘用户可能看不清焦点（静态预览无法验证，标为风险）。
- **轻边框**：`Border` 合成后 1.24/1.43:1，若次级按钮/卡片仅靠边框识别，则不符合 WCAG 1.4.11 的 3:1（部分元素有填充/文字可辨，属风险而非全部违规）。

---

## 5. 交互一致性（代码抽查）

| 交互类 | 现状 | 证据 | 判定 |
| --- | --- | --- | --- |
| 开关（Switch） | Plugins / Automation / Settings 共用 `pluginSwitchLive` + 原生 `ui.Switch` | `plugins_ui.go`、`automation_ui.go`、`settings_ui.go` 注释与调用 | ✅ 一致 |
| 主按钮 | 每屏都用原生 `PrimaryButton`（蓝底）；Settings 用 `PrimaryButton`（Add model/Save） | `grep PrimaryButton` | ✅ 基本一致 |
| 关闭方式 | Settings：右上 `打开配置文件` + **`关闭` 文字按钮** + **`×`** + Esc + 点 scrim，共 5 种；其它弹层（Automation 新建/编辑、Plugins About/Add/More）是**底部 `关闭/取消` 按钮** | `settings_ui.go:339-349`（header 三动作）、`automation_ui.go:350-354`（底部 Cancel/Save）、`plugin_details.go:456-490`（底部 Close） | ❌ 不一致（冗余 + 弹层模式分裂） |
| 破坏性操作 | 会话删除 / 工作区删除：**确认模态 + Danger 色**；Automation 删除：**一键执行、无确认、中性色**；Memory `全部清除`：**一键执行、无确认**（有 Danger 色） | 确认：`menus.go:322`、`workspaces.go:1292`；无确认：`automation_ui.go:236`→`:434-443`、`memory_ui.go:45-48`→`:176-196` | ❌ 不一致 + 数据风险（Top1） |
| 空态 CTA | Automation 空态用 `ui.Button`（次级）而页头用 `PrimaryButton`；右侧栏空态只有图标+文字；Plugins 空搜索/空态未在预览中出现 | `automation_ui.go:289-303`；`right_sidebar.go:1011+` | ⚠️ 强调不一致/覆盖不足 |
| Tab 选中 | Session：accent 下划线；右侧栏：`BgHover` pill | 见 B4 | ⚠️ 两套语法 |
| Hover-only | 会话/工作区行内动作已改为**常显原生按钮**；消息 footer 操作常显；hover 只做背景反馈 | `native-ui.md:226-227`；`message_actions.go`（按钮常显，hover 仅染色）；67 处 `Hovered()` 均为 tint/背景 | ✅ 已解决（上一轮 P0） |
| 键盘/焦点 | 原生控件自带；`Role/Focusable` 保留；Tab 可达性测试在 | `a11y_test.go` 等 | ✅ 基本（真机未验） |
| 菜单/通知/窗口状态/滚动条 | 菜单 ✅（`internal/app/menu.go`）、窗口状态 ✅（`window_state.go`）、通知 ✅（`notify.go`，当前会话限制）、滚动条 = mygo 原生 hover overlay，无平台"常显"偏好接入 | ADR 0003/0004、`preferences.go:112-145` | 菜单/窗口 ✅，通知部分，滚动条部分 |
| 文本选择 | 消息 markdown 可选中/右键复制（`ui/markdown` 迁移）；**跨消息不共享**、工具卡详情仍是纯文本不可选、无 Copy | `native-ui.md` §13.4；`toolcards.go:193` `MaxLines(10)` | ⚠️ 部分 |

---

## 6. 与目标 / 调研结论 / UX P0-P1 清单对照

### 6.1 方案对照（00-report A/B/C）

当前实际路线 = **C 的视觉方向（自有品牌视觉）+ B 的交互本地化 + 功能对齐 DSH**，与 `docs/research/00-report.md` §7「Step 2 视觉二选一 → C；Step 3 品牌演进」一致；ADR 0001/0002 记录完整。**没有发现方向跑偏**。偏离 00-report 的有两处：

1. 00-report Step 1 要求「CI 加自身视觉回归（少量关键屏 + 2x + 令牌断言）」——**未做**（`scripts/ci.sh` 只有 fmt/vet/build/test/nocgo/tool/coverage/race；预览测试是 opt-in 生成，无对比）。
2. 00-report/03-report 的 P1「设置独立窗口」——HEAD 仍是应用内模态（`settingsOverlay`），仅在工作树并行任务中看到 `SettingsView` 雏形（不在本评审范围）。

### 6.2 UX P0/P1 清单逐项状态（03-ux-design §5.2 / 00-report §5.1）

| 项 | 来源 | 状态 | 证据 |
| --- | --- | --- | --- |
| 消息文本可选择/复制 | P0 | ✅ 部分 | markdown 迁移（`native-ui.md` §13）；跨消息/工具卡未做 |
| Cmd/Ctrl+C/V/A + 右键 | P0 | ✅ 部分 | 原生 Edit 菜单（ADR 0003）+ markdown 选择菜单；工具卡纯文本 |
| 菜单栏命令可发现 | P0 | ✅ 已解决 | `internal/app/menu.go` + `menu_test.go`；菜单/快捷键同源 |
| 长任务系统通知 | P0 | ✅ 部分 | ADR 0004；仅当前会话，无通知动作/ID |
| 窗口大小/位置记忆 | P0 | ✅ 已解决 | ADR 0003 `window_state.go` + 测试 |
| hover-only 操作 | P0 | ✅ 已解决 | 行内动作常显（`native-ui.md` §9.1） |
| 设置独立窗口 | P1 | ❌ 未做（HEAD） | `settingsOverlay` + scrim；预览即证据 |
| 滚动条跟随平台 | P1 | ⚠️ 部分 | overlay 保留 + HC 增强（`preferences.go` §12.3 记录） |
| 系统字号缩放 | P1 | ⚠️ 部分 | 仅主题基座+会话；固定 type scale 占多数（§4.2） |
| 高对比/减少动效 | P1 | ✅ 已解决 | `preferences.go` + 6 个测试 |
| 2x DPI | P1 | ⚠️ 冒烟 | `TestHighDPIRenderSmoke`；真机矩阵未做 |
| 工具卡内容原生化 | P1 | ❌ 未做 | 纯文本 + `MaxLines(10)`（`toolcards.go:193`） |
| Trajectory 虚拟化 | P2 | ❌ 未做 | 文档 §16 遗留 |
| 后台会话通知 | P1 | ❌ 未做 | ADR 0004 §4 已知限制 |
| 通知动作按钮/替换 ID | P2 | ❌ 未做 | ADR 0004 §4 |
| 真机 IME / a11y / DPI 矩阵 | P1 | ❌ 未验证 | 证据不足 |
| CI 自身视觉回归 | 工程 | ❌ 未做 | `scripts/ci.sh` 无预览/快照步骤 |
| 品牌去 DSH 文案 | P0 | ✅ 已解决 | Home tagline 预览 + 测试 `TestHomeBrandHero` |

**口径问题（诚实指出）**：README/STATUS 把「系统偏好 TextScale/HighContrast/ReduceMotion 与 2x DPI 冒烟」写成阶段 4 完成项，实际 TextScale 是部分覆盖、2x 是冒烟、HC/RM 才是完整；建议在 STATUS/README 用「部分/冒烟/待真机」三档措辞，避免后续验收误判。

---

## 7. Top 10 问题（P0 = 收口前必须修；P1 = 影响体验/一致性；P2 = 打磨）

### P0-1 破坏性操作缺少确认且危险态不一致（数据风险）

- **现象**：Automation 行内 `删除` 一点即删（无确认、无撤销、无 Danger 色）；Memory「全部清除」一点即清（无确认）；而会话删除/工作区删除都有确认模态 + Danger 色。
- **证据**：`internal/shell/automation_ui.go:236`（`automation.delete` → `deleteAutomation`）、`:434-443`（直接 `Delete` + Toast）；`internal/shell/memory_ui.go:45-48`（Clear all）→`:176-196`（直接 `ClearMemories`）；对照 `internal/shell/menus.go:322`、`internal/shell/workspaces.go:1292` 的确认模态；截图 `preview/automation-native-light.png`（删除与其余按钮同款）。
- **建议**：抽一个 `confirmDanger` 模式（复用 workspace delete 模态）：标题/后果/取消+删除；Automation 删除、Memory Clear all 必须走；按钮用 Danger；可选 Undo toast。补测试：取消不删除、确认才删除、Esc 关闭。

### P0-2 浅色主题三级文字系统性不达 WCAG AA

- **现象**：`TextTertiary #7E8796` 在浅色下 3.26–3.62:1（白底/侧栏/强表面），被用于 11–13px 的设置说明、时间戳、用量、区块标签、空态（160 处）。
- **证据**：`preview/settings-native-light.png` 权限行说明实测 `#7E8796` on `#F1F3F5` = 3.26:1；`sidebar-native-light.png` “工作区” = 3.47:1；`session-native-light.png` “已完成” = 3.38:1；`trajectory-native-light.png` 用量 = 3.43:1；代码 `internal/shell/settings_ui.go:358`（区块标签 TypeXXS+LabelTertiary），`internal/brand/palette.go` 三级色，ADR 0002 §4.1 自记 3.6:1；`internal/brand/brand_test.go:138-160` 未覆盖 tertiary。
- **建议**：浅色 `TextTertiary` 提到 ≥4.5:1（候选 `#626B7A`：白底 5.38 / 强表面 4.84 / 侧栏 5.15）；把三组背景加入 `TestPaletteContrast`；若保留浅灰，则仅用于非信息性装饰并写规范。

### P1-3 深色字标是浅色位图：白色残边 + 2x 模糊风险 + 文档矛盾

- **现象**：深色 hero/sidebar 字标四角可见白色圆角残边；`mark.png` 是 48×48 位图且含白色容器边缘像素；`logo-dark.svg` 已在仓库但未使用；`native-ui.md` §12.2 声称“无位图资产”。
- **证据**：`internal/shell/assets.go:12-23`（唯一 `embed mark.png`）；`mark.png` 取色 `(2,2)=srgba(255,255,255,0.96)`；`logo/logo.svg:28` 白色容器 vs `logo/logo-dark.svg:28` 黑色容器；`preview/home-native-dark.png`、`preview/sidebar-native-dark.png`；`docs/design/native-ui.md:262`。
- **建议**：用 `ui.SVG`（`logo/logo-dark.svg`）或按主题切换位图；2x 下不重采样；修正文档表述；在预览中补 2x 截图证据。

### P1-4 设置仍是应用内模态，且头部关闭冗余/弹层模式分裂

- **现象**：Settings 仍是 800×760 居中面板 + 全窗 scrim（Web modal 惯例），未做独立设置窗口（P1）；头部同时有 “关闭” 文字按钮 + “×”，另有 Esc/点 scrim 关闭；其它弹层用底部 `关闭/取消`。
- **证据**：`git show HEAD:internal/shell/settings_ui.go` 只有 `settingsOverlay`/`settingsPanelLive`；`settings_ui.go:339-349`（3 个 header 动作）；`settings_ui.go:808`（X）；预览 `settings-native-light.png`；其它弹层：`automation_ui.go:350-354`、`plugin_details.go:456-490`。
- **建议**：若本轮范围内做独立窗口，验收非模态/无 scrim/Cmd+,/菜单一致/关闭语义唯一；否则至少在模态上去掉一个关闭入口，并统一所有弹层"头部标题 + 右上 X"或"底部取消/确认"二选一。**不要三种混用。**

### P1-5 Automation 中文界面里的英文计划文案与未本地化日期

- **现象**：`Daily at 00:00` / `Every hour` / `Every 15 minutes` 直接出现在中文 UI；next-run 用 ISO 格式。
- **证据**：`preview/automation-native-{light,dark}.png`；`internal/automation/cron.go:174-188` `Describe()` 硬编码英文；`internal/shell/automation_ui.go:269-273` 直接拼接。
- **建议**：`Describe` 返回 i18n key 或接受语言；统一 `time.Format` 的本地化；加一条 i18n 回归（中文界面下无纯英文 schedule）。

### P1-6 TextScale 只覆盖“基座字号 + 会话正文”，固定 type scale 不缩放

- **现象**：系统大字号用户打开后，设置说明/区块标签/时间戳/用量等固定 11–16px 文本不变大。
- **证据**：`internal/shell/preferences.go:90-100` 只缩放 `th.FontSize`；`internal/design/tokens.go:35-56` 固定 TypeMetrics；`settingsText`/`nativeText` 直接用固定值（`settings.go:73+`、`plugins.go:95-98`）；HEAD 中 `design.Type` 引用 115 处、`settingsText` 62、`nativeText` 47；测试 `TestTextScaleScalesThemeAndConversationText` 只断言主题字号+会话；文档 §12.4 已承认遗留。
- **建议**：`design.TypeMetricsScaled(role, scale)` 并在 `settingsText/nativeText` 接入；补 150%/200% 渲染冒烟（宽度不裁切 + 主要文本字号增大）；STATUS 措辞降到“部分”。

### P1-7 预览只覆盖“静止的 9 视图 × 2 主题”，无视觉回归门禁

- **现象**：18 张图覆盖不了 Settings 子页（Models/Memory/Built-in plugins/Agent presets/About）、Plugins 详情/About/Add、Automation 新建编辑/空态、Session 审批/计划/提问/工具错误、右栏 Terminal/Session Log、900×600 窄窗、HighContrast/TextScale、focus/hover。CI 没有任何预览或视觉回归步骤（预览测试只在 `ZEOKEEL_UPDATE_PREVIEWS=1` 时写盘）。
- **证据**：`docs/design/preview/README.md` 的 9 行清单；`scripts/ci.sh`（无 preview/snapshot 步骤，只有一行注释说像素门禁已退役）；`native-ui.md:395-402` 的验收口径只有 1600×1000 浅/深截图。
- **建议**：下一轮补 10–14 张状态矩阵图（见 §8）；CI 加"预览可生成 + 自身关键区域/令牌断言"（不对比 DSH）；正式验收前由设计 owner 逐张签。

### P1-8 视觉层级仍有 Web 感：Home 大留白 + 双主按钮 + Session tint 过近

- **现象**：Home 在 1600×1000 仍是居中 hero + 720 composer 的 landing 构图，没有最近会话/继续上次等密度块；Plugins/Automation 同屏两个实心蓝主按钮；Session 用户气泡 `#E6ECFE` 与工具详情 `#F1F4FE` 同族过近，扫读时层级弱。
- **证据**：`preview/home-native-{light,dark}.png`、`preview/plugins-native-light.png`、`preview/automation-native-light.png`；session 取色 `#E6ECFE` vs `#F1F4FE`；`chrome.go:102` + 页头 `PrimaryButton`。
- **建议**：Home 在 ≥1200 宽加"最近会话/继续上次/快捷入口"密度块或收窄 hero；列表页只保留一个实心主按钮（侧栏新建改 secondary）；工具详情用中性 surface（`BgStrong`）与用户 tint 区分。

### P2-9 细节一致性包（可批量修）

- 卡片圆角 16 vs 10（B3）；tab 选中下划线 vs pill（B4）；`⋯` 文字字形 vs 图标、Start 卡 chevron（B5）；header 两个 chip 语法（B6）。
- `plugins.go:34-35` 的硬编码 `#6E7278/#C2C6CA` stand-in（对比度合格但违反 §17 令牌口径）。
- `native-ui.md` 缺 §5/§6、§13 小节误编号、§16 引用不存在 §5——文档可信度问题。
- **建议**：写一页 `docs/design/consistency.md`（半径/间距/tab/按钮/关闭/破坏性操作/空态 6 张表），下一轮按表清理；文档编号一并修。

### P2-10 默认浅色焦点环与轻边框对比偏低

- **现象**：浅色 Focus = accent 55% 透明，合成后对白底 2.73:1（<3:1）；轻边框 1.24:1。HighContrast 下 Focus 不透明、边框增强，达标。
- **证据**：`internal/brand/palette.go:152`（Focus light α0.55）、`:184`（dark α0.70）；`preferences.go` HC 分支 `th.Focus.A=255`；合成计算见 §4.1。
- **建议**：浅色 Focus 改 0.75–0.85 或加深色相，保证 ≥3:1；次级按钮/卡片在默认主题下不要只靠 1px 边框表达可点性（加填充或 2px 边框）；补"键盘焦点可见"截图。

---

## 8. 下一轮清单（≤10 项，可执行）

| # | 动作 | 验收 |
| --- | --- | --- |
| 1 | 破坏性操作统一：抽 `confirmDanger`（删除/清空），Automation 删除 + Memory Clear all 接入；Danger 色 + 确认 + Toast/Undo | 取消不删、确认才删、Esc 关闭；新增 2 个行为测试 |
| 2 | 浅色 TextTertiary 提到 ≥4.5:1（建议 `#626B7A`），三背景加入 `TestPaletteContrast` | 3 组对比 ≥4.5；重生成设置/侧栏/会话预览 |
| 3 | 深色字标矢量/主题化（logo-dark.svg 或 dark bitmap + 2x），去白色残边；修 `native-ui.md` §12.2 表述 | 深色 hero/sidebar 截图无白边；2x 截图可辨 |
| 4 | 弹层模式收敛：Settings 若不在本轮做独立窗口，至少去掉“关闭”文字按钮或 X 之一；统一所有 dialog 头部/底部关闭模式 | Settings 只有 1 个显式关闭入口；3 类 dialog 视觉一致 |
| 5 | 本地化 `automation.Describe` / next-run 日期；补 i18n 回归（中文界面无英文 schedule） | 中文预览无 “Daily at/Every hour”；测试通过 |
| 6 | TextScale 接入固定 type scale：`design.TypeMetricsScaled` + `settingsText/nativeText`；补 150%/200% 冒烟 | 150% 下设置说明字号变大、不裁切；状态文字改“部分→已支持（除 X）” |
| 7 | 预览状态矩阵：Settings 子页 ×2、Plugins detail/Add ×1、Automation dialog/空态 ×2、Session 审批/计划/错误 ×2、右栏 Terminal/Log ×2、900×600 ×2、HC ×1、TextScale 150% ×1、focus 态 ×2（可放 /tmp 或保留 opt-in 写盘） | 评审 owner 逐张确认；文件列表更新 preview README |
| 8 | CI 加"自身视觉回归"最小门禁：8 个关键视图生成成功 + 关键区域颜色/尺寸断言（不对比 DSH）；预览失败不静默 | `sh scripts/ci.sh` 增加一步且全绿；无 DSH 依赖 |
| 9 | 层级调整：Home 宽度 ≥1200 时加“最近会话/继续上次”密度块或收窄 hero；列表页侧栏新建降 secondary；工具详情改中性 surface | 截图评审：Home 不再 landing 空；列表页仅 1 个实心主按钮；Session tint 层级可辨 |
| 10 | 一致性规范 `docs/design/consistency.md` + 清理：卡片圆角语义、tab 选中语法、`⋯` 图标化、Start 卡交互、硬编码色替换、文档编号修正 | 规范落盘；`grep` 无新增硬编码色；review-round4 可对照检查 |

---

## 9. 不宜再做的事

1. **不要回到"和 DSH 截图对齐"**：视觉验收只对自身设计系统与平台规范负责（ADR 0001 已拍板）。
2. **不要在预览矩阵补齐前宣称"全部屏/区完成、观感验收通过"**：18 张静态图不等于状态覆盖；说"视觉主线完成、待状态验收"更准确。
3. **不要新增第三套同类控件语法**：卡片圆角/tab 选中/关闭/按钮/空态先收敛（已有两套的地方，二选一或写规范），再谈打磨。
4. **不要用位图缩放做 2x/3x 品牌标**；不要继续用 `mark.png` 兼顾明暗主题。
5. **不要把 TextScale 只接到主题基座字号就对外说"已支持系统大字号"**；STATUS/README 按"完整/部分/冒烟/未验证"四档写。
6. **不要让普通测试写工作树**：预览继续 `ZEOKEEL_UPDATE_PREVIEWS=1` opt-in（现行为正确，保持）。
7. **不要把英文 cron 文案直接拼进中文 UI**，也不要用 `fmt.Sprintf` 硬编码可翻译句（`cron.go:178-188` 这种模式应改为 key）。
8. **不要把删除/清空做成无确认的一键操作**（尤其 Automation 任务、Memory 全部记忆），也不要只靠 Toast 当撤销。

---

## 10. 附录 A：本报告证据索引

| 结论 | 证据 |
| --- | --- |
| HEAD 全绿（复验） | `git archive HEAD` → `/tmp/repo-head`；`gofmt` 空、`go vet` exit 0、`go test ./...` exit 0（17 包，shell 37.747s / session 34.662s） |
| 评审时工作树有并行改动 | `git status`: 12 个代码文件 + `docs/decisions/0004…`、`preview/session-native-*.png`（评审期间被改）；未纳入本评审 |
| 深色字标白边/位图 | `internal/shell/assets.go:12-23`；`mark.png` 像素 `(2,2)=srgba(255,255,255,0.96)`；`logo/logo.svg:28`、`logo/logo-dark.svg:28`；`preview/home-native-dark.png`、`preview/sidebar-native-dark.png`；`native-ui.md:262` |
| 浅色三级文字 3.26–3.62:1 | 取色：`preview/settings-native-light.png`（`#7E8796` on `#F1F3F5` 3.26）、`sidebar-native-light.png`（3.47）、`session-native-light.png` “已完成” 3.38、`trajectory-native-light.png` 用量 3.43；`settings_ui.go:358`（TypeXXS+LabelTertiary）；`brand/palette.go`；`brand_test.go:138-160`（未覆盖 tertiary） |
| 对比度表 | 计算脚本：sRGB→WCAG 相对亮度（见 §4.1 数值）；令牌来源 `internal/brand/palette.go`、ADR 0002 §4.1 |
| 焦点环 2.73:1 | `brand/palette.go:152`（light `RGBA(13,68,246,0.55)`）、`:184`（dark `0.70`）；合成计算 |
| 破坏性操作不一致 | `automation_ui.go:236`、`:434-443`；`memory_ui.go:45-48`、`:176-196`；对照 `menus.go:322`、`workspaces.go:1292` |
| 设置仍是模态 + 三关闭入口 | `git show HEAD:internal/shell/settings_ui.go`（仅 `settingsOverlay`）；`settings_ui.go:339-349`；`settings_ui.go:808-809`；预览 `settings-native-light.png` |
| Automation 中英混排 | `internal/automation/cron.go:174-188`；`automation_ui.go:269-273`；`preview/automation-native-light.png` |
| TextScale 部分覆盖 | `preferences.go:90-100`（只改 `th.FontSize`）；`design/tokens.go:35-56`；`settings.go:73+`；HEAD 计数 `design.Type` 115 / `settingsText` 62 / `nativeText` 47；`native-ui.md:271-275` §12.4 |
| 预览/CI 覆盖不足 | `docs/design/preview/README.md`（9 行视图清单）；`git grep TestRender.*NativePreview HEAD`（8 个预览测试，不等于状态覆盖）；`scripts/ci.sh`（无 preview/snapshot 步骤） |
| 双主按钮 | `chrome.go:102`（accent 新建会话）；`plugins.go`/`automation_ui.go` 页头 `PrimaryButton`；`preview/plugins-native-light.png`、`preview/automation-native-light.png` |
| 卡片圆角两套 | `design/components.go:27-28`（`RadiusMD=10`、`RadiusLG=16`）；`plugins.go:240`、`automation_ui.go:239` vs `settings_ui.go:360/523`、`start_tab.go:125` |
| tab 两套 | `trajectory.go:99-125`（下划线）vs `right_sidebar.go:365-400`（pill） |
| 硬编码色 2 处 | `internal/shell/plugins.go:34-35`（`#6E7278/#C2C6CA`，注释称 stand-in） |
| 文档编号漂移 | `native-ui.md`：§4→§7 缺 §5/§6（第 94/125 行）；§13 内 “12.1–12.4”（282/291/309 行）；§16 引用 §5（392 行） |
| 预览清单与哈希 | 18 张均为 1600×1000 PNG；读取顺序 Home→Session→Sidebar→Settings→Plugins→Automation→Trajectory→RightSidebar(Start)→RightSidebar(Files)，各浅/深 |

## 11. 附录 B：评审限制 / 证据不足

1. **真机行为**：IME 候选窗、屏幕阅读器（VoiceOver/Narrator）、HighContrast/TextScale 实际效果、多显示器 DPI、Windows/Linux 菜单/通知外观——本环境无显示器，均属证据不足，不能签收。
2. **静态帧**：focus/hover/动效/滚动条不可见；焦点环对比是代码合成值，实际绘制可能叠加控件边框/反锯齿，需真机确认。
3. **并行改动**：评审期间工作树有 12+ 文件未提交修改（含 `SettingsView` 雏形、toolcards/markdown 更新、session 预览重生成）；若这些在下一轮合入，本报告的部分结论（如"设置独立窗口未做""工具卡纯文本"）需重新核对。
4. **用户验证**：未做可用性测试；"像不像桌面应用/出不出彩"是本报告的专家判断，不是用户数据。建议按 03-report §8 的脚本做 5–8 人形成性测试。
5. **设计意图**：部分不一致（卡片 16 vs 10、主区下划线 tab vs 面板 pill）可能是有意区分，报告按"未成文/易误读"处理，最终以 owner 规范为准。

## 12. 附录 C：文档层面的顺手修正（下一轮）

- `native-ui.md` 缺 §5、§6，§13 小节编号误写为 “12.x”，§16 引用不存在的 §5；建议补"视觉验收状态矩阵"小节并把 §17 的验收口径从"1600×1000 浅/深截图"扩为状态矩阵。
- README/STATUS 对 TextScale/2x DPI 的措辞应从"已完成"细化为"部分/冒烟"；"系统偏好"四档（完整/部分/冒烟/未验证）写进 STATUS。
- `docs/design/preview/README.md` 应补每张图对应的测试名/生成命令与"覆盖矩阵"（含未覆盖状态），避免"18 张=全部"的误读。

---

## 附录：问题修复状态（2026-11-05 第四轮，供复核）

| 问题 | 状态 | 证据 |
| --- | --- | --- |
| P0-1 Automation 删除 / Memory Clear all 无确认、无 Danger | **已修复** | 确认弹层（Cancel 默认焦点、Esc/点外关闭、Enter 不误删、Danger 确认按钮、确认后 Toast）；新增 `TestAutomationDeleteRequiresConfirmation` / `TestMemoryClearAllRequiresConfirmation` |
| P0-2 浅色三级文字不达 AA（#7E8796 3.3–3.6:1） | **已修复** | `#646C7A`：BgBase/BgRaised 5.29、BgSidebar 5.06、BgStrong 4.76:1；`TestPaletteContrast` 增四组表面+层级断言；ADR 0002 §9 |
| P0-3 深色字标浅色位图白边 | **已修复** | `logo-dark.svg → mark-dark@2x.png`（96×96），`brandMark(t)` 按主题切换；Home/Sidebar/rail；assets_test 断言深色外圈无近白像素 |
| P1-4 设置仍应用内模态**双入口** | **部分修复** | 设置已默认独立非模态原生窗口（ADR 0005），overlay 仅作无显示器/失败回退；回退路径仍有 close/X 双入口 |
| P1-5 Automation 中文残留英文计划文案 | **已修复** | shell 侧 `automationScheduleLabel` 按 UI 语言渲染；`TestAutomationScheduleLabelLocalized` |
| P1-6 TextScale 未覆盖固定 type scale（115 处） | **未修** | 记录于 known-limitations §2 |
| P1-7 预览无状态覆盖 / CI 无视觉回归 | **未修** | 记录于 known-limitations §4（建议后续 headless golden 冒烟） |
| P2 Home 构图/双主按钮等观感项 | **未修（设计选择/待产品反馈）** | 保留 landing 构图；列表页双主按钮按上下文保留 |

> 说明：本表只记录修复状态，不改动原评审结论。复核时请以最新 HEAD 的预览图与测试为准。
