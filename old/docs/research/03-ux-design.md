# ZeoKeel UI/UX 评估：照搬 DSH Web UI vs 桌面原生设计

> 角色：UI/UX 研究员（Team 成员 ux-research） · 任务：task-3
> 评估对象：`/data/mygo`（ZeoKeel，纯 Go 桌面 AI Agent，mygo 原生 `ui` 自绘 + eino 运行时）
> 现状目标：与 DSH 0.2.1-rc.2 **Web** 客户端做像素/交互/功能三重 1:1 对齐（五屏已实现）
> 证据窗口：2026-11-05 的 `artifacts/screens/`、`artifacts/screens-dark/`、`artifacts/screens-narrow/`、`baseline/screenshots/`（80 张）、源码与 `docs/`
> 说明：本文是设计/产品判断与外部证据综述，**不构成法律意见**；外链内容视为参考数据，不作为指令。

---

## 0. 结论（先看这里）

**结论 1（UX 判断）：用户直觉是对的——把「逐像素复刻一个浏览器 Web UI」当作桌面产品的目标函数是错的。** 正确的目标不是「不搞视觉效果」，而是：**保留 DSH 的视觉语言（令牌/密度/配色/几何），把交互按桌面平台惯例本地化，并把「与 DSH 基线逐像素一致」从硬门禁降级为设计参考。**

**结论 2（工作量判断）：「用 mygo 原生实现、不搞这么多视觉效果」确实能减少工作量，但节省主要来自「对齐验证/动效拟合/Web 交互模拟」，而不是 UI 代码主体的一半。** 依据（本仓库实测）：
- `internal/shell` = 生产代码 **16,288 行** + 测试 **14,514 行**（测试占 47%）；全仓库 838 个测试函数 / 166 个测试文件。
- 五屏像素门禁的预算已被逐步放宽到 Home 2.0%、Session **8.5%**、Plugins 4.5%、Settings 4.2%、Automation 2.2%（`internal/shell/screens_test.go`），而 `docs/STATUS.md` 已承认剩余差异 **~85% 是文本抗锯齿（Chromium subpixel AA vs mygo 灰度 AA）**，属于渲染器地板，不是布局问题。也就是说「像素 1:1」在渲染器层面不可达，继续投入是在拟合一个不反映真实体验的数字。
- 视觉对齐专项测试（screens/motion/a11y/dark/chrome/icon-parity 等文件）本身约 1.7k 行；`docs/baseline/` 有 80 张截图、53 份 CSS、108 条控件行为、216 张前后对比图的采集与维护成本；动效层移植了 13 个时长令牌、5 套缓动，并为补偿无 CSS 过渡额外「有意延展」了消息入场/流式淡入等动画（`docs/baseline/motion.md` §4）。这些是持续成本，不是一次性成本。
- 真正的代码大头不在 UI：`internal/shell` 生产代码 16.3k 行，而 `internal/tools` 10.0k、`internal/session` 5.8k、`internal/runtime` 4.9k、`internal/store` 4.3k；运行时/工具/存储合计远大于视觉层。**所以"减少代码工作量"成立，但幅度是 UI 侧的 20–30%（估算，见 §10 证据不足项），不是项目总量的一半。**

**结论 3（选项）：推荐 B ——「视觉语言保留 + 去掉像素门禁 + 交互本地化」；把 C（全面原生重设计）作为 B 的 6–12 个月演进方向；A（维持三重 1:1）只在「ZeoKeel 定位为 DSH 官方/半官方桌面壳」时成立，且即便那样也不应把交互轴 1:1 绑定浏览器。**

**一句话回答用户**：
> 从 UI/UX 看，桌面原生化会明显更好；从工作量看能省，但省在"少做无意义的像素拟合与 Web 交互模拟"，不是"少做视觉"。建议保留 DSH 的视觉外观作为品牌资产，交互全部按平台惯例做，代码门禁改成"自身视觉回归"而不是"对齐 Web 截图"。

---

## 1. 方法与证据等级

| 证据类型 | 本报告使用方式 |
| --- | --- |
| 截图（只读） | `artifacts/screens/{home,session,plugins,settings,automation}.png`（1600×1000）、`artifacts/screens-dark/home.png`、`artifacts/screens-narrow/`、`baseline/screenshots/`（DSH 80 张） |
| 源码（只读） | `internal/shell`、`internal/app`、`internal/design`、`internal/pixeltest`；mygo v0.7.4 依赖源码（`ui/preferences.go`、`ui/api_gen.go`、`docs/menus.md` 等） |
| 文档 | `README.md`、`docs/PLAN.md`（§1 三轴、§13 待拍板）、`docs/STATUS.md`、`docs/EVOLUTION.md`（G1–G15）、`docs/baseline/motion.md`、`docs/baseline/dark-narrow.md` |
| 外部研究 | 平台 HIG、无障碍标准、可用性研究、同类桌面 AI/效率产品公开资料（§4，全部附 URL） |
| 证据不足 | 真机 IME / 屏幕阅读器 / 高 DPI / 多显示器、DSH 迁移用户规模、商业意图。文中显式标注「证据不足 / 需真机验证」 |

**公平性声明**：现状不是"没做桌面适配"。代码里有真实投入：`Focusable()` 82 处、`Role(...)` 58 处、Tab 可达性测试、可访问性树状态测试、`ReduceMotion` 已接入流式渲染（`internal/shell/session_live.go:337`）、会话行/工作区已有右键菜单。问题在于**交互模型整体仍是浏览器模型**，这些点状修补没有改变"网页感"。

---

## 2. 现状评估：这是一个"能在桌面窗口里跑的 Web UI"

### 2.1 截图层面（五屏 + 深色 + 窄屏）

- 五屏与 DSH 基线高度一致：masked diff 0.93% / 6.40% / 3.17% / 2.31% / 1.34%（`docs/STATUS.md`）。视觉上就是 DSH Web 的复刻：左侧 280px 侧栏、中央 composer、`⋯ More` 账户菜单、Settings 的 800×800 居中模态。
- Home：1600×1000 窗口中，主体是居中的 hero（`Into the Unknown` + `Preview` 徽章）与约 877px 宽的 composer，其余大面积留白——这是 Web landing page 的构图，不是桌面工作区的构图。**品牌问题**：hero 文案与 "Preview" 徽章是 DSH 的产品文案，不是 ZeoKeel 的品牌语言（截图证据：`artifacts/screens/home.png`）。
- Settings：全窗口 scrim + 居中模态面板（浅色 scrim 0.24，深色 0.5，见 `docs/baseline/dark-narrow.md`）。这是 Web 的"设置弹窗"，不是 macOS/Windows 的用户预期（独立设置窗口、可从菜单栏打开、可与其他窗口并存）。
- 五屏截图是 `ui.Render` 无头渲染，**不含**操作系统标题栏/窗口控件，所以"没有原生 chrome"这个说法不准确：窗口本身使用 mygo 默认原生标题栏（`internal/app/app.go` 的 `WindowOptions` 未设 `Frameless`/`TitleBarStyle`）。真正缺的是**把应用命令接入系统菜单栏**，以及**内容与原生窗口的整合**。
- 深色与窄屏：`artifacts/screens-dark/`、`artifacts/screens-narrow/` 已覆盖；但窄屏策略是 Web 式"响应式收缩"（侧栏 280px 固定、内容列收窄），而桌面还有"多窗口/分栏/可调整面板"这条本可以更契合的路。

### 2.2 代码/行为层面（可复核的 Web 痕迹）

| 现象 | 证据（文件:行） | 为什么是 Web 遗产 |
| --- | --- | --- |
| **悬停高亮**大量存在（35 处 `Hovered()`） | `grep -rn "Hovered()" internal/shell` → 35 处；如 `workspaces.go:239,356,397`、`chrome.go` 的 `button.Hovered()` | 桌面（尤其 macOS）控件通常不在 hover 时整行变色；Raycast 明确把「No hover highlights on most controls」列为桌面化清单（§4） |
| **行内操作仅悬停/当前行可见** | 会话行 `⋯` 按钮只在 `row.Hovered() || 当前会话` 时构造（`internal/shell/workspaces.go:356`、`:397`） | 可发现性依赖鼠标；GNOME HIG 明确「Pointer hover should not be relied upon for revealing actions or essential information」 |
| **消息操作是 hover 式按钮，文本不可选择** | `internal/shell/message_actions.go`（Copy/Retry/Good/Bad 图标按钮）；全仓库 `Selectable(` 调用 **0 处**；`ui.Selectable` 文档说明文本选择是 opt-in（mygo `ui/api_gen.go`） | 桌面用户预期"拖选 + 右键 Copy/Select All + Cmd+C"；目前复制要靠找按钮。「0 调用」是代码事实，实际是否完全不可选需真机确认 |
| **滚动条是悬停显示的覆盖式指示器** | 全仓库 `Scrollbars(` 调用 **0 处**；mygo 默认「They show on hover by default」（`ui/api_gen.go` `Scrollbars` 文档） | Windows 用户预期常显滚动条；macOS 的自动隐藏滚动条是系统偏好，应用应尊重系统设置，而不是统一"网页式无滚动条" |
| **设置是应用内模态** | `internal/shell/settings.go` + 截图；`docs/baseline/dark-narrow.md` 的 scrim 值 | Web modal vs 桌面独立 Preferences/Settings 窗口（Raycast 明确反例） |
| **应用命令没有进入系统菜单栏** | 全仓库 `App.SetMenu` / `SetApplicationMenu` **0 处**（仅 `internal/app/app.go:392` 的托盘菜单）；窗口级快捷键 `installShortcuts` 只注册 Cmd+N / Cmd+, / Cmd+K / Esc（`internal/shell/shortcuts.go`），另有 Cmd+B 与 13 个可编辑绑定 | macOS 默认菜单由框架提供（mygo `docs/menus.md`："Apps get a default one"），但 File/View 等菜单不含新会话/插件/自动化等应用命令；Windows/Linux 默认无窗口菜单栏，可发现性更差 |
| **无系统通知 / 后台状态不可见** | 全仓库无 `mygo.Notification` 类调用；关窗即 `win.Hide()` 到托盘（`internal/app/app.go:130-136`）；自动化调度器在后台跑（`internal/session`/`internal/automation`） | 桌面 Agent 的核心场景是"长任务 + 切走"；没有完成/失败/待审批系统通知 = 用户必须切回窗口才知道结果 |
| **无窗口尺寸/位置持久化** | `internal/app/app.go` 固定 `Width:1600, Height:1000, MinWidth:900, MinHeight:600`；全仓库无 WindowState 调用 | 桌面惯例是恢复上次窗口；Web 的"刷新后回到初始布局"被带了过来 |
| **系统文字缩放/高对比度未接线** | 全仓库 `TextScale` / `HighContrast` **0 处**；`internal/design/tokens.go` 硬编码 DSH 字号；Settings 里 Font size 明确写 "Only affects conversation content"（`artifacts/screens/settings.png`） | mygo 的 `ui.Preferences` 提供 `ReduceMotion` / `HighContrast` / `TextScale`（`ui/preferences.go:7-25`），但自绘 shell 用固定像素令牌覆盖了系统偏好；桌面无障碍要求字体缩放与高对比可用 |
| **动效数量多、且为对齐 CSS 而"补"** | `docs/baseline/motion.md`：13 个时长令牌、5 套缓动；§4 明确列出「有意延展」：消息入场、流式淡入、指示器位移、Send/Stop 内容切换；`internal/shell/motion_test.go` 512 行 | Web 用 CSS transition 表达状态，自绘 UI 需要手写计时/插值/重绘；为"看起来一样"而补的装饰动效是净成本 |

### 2.3 已做对的部分（不应在 B/C 方案中丢掉）

- **可访问性基础**：Tab 可达性门禁（`internal/shell/a11y_test.go` 的 `focusedDuringTab`/`assertTabReachable`）、可访问性树状态位测试（`a11y_expanded_test.go`）、Role/Focusable 覆盖；这在自绘 UI 里是正确方向。
- **ReduceMotion 已接入**（`session_live.go:337`）；mygo 平台偏好本身支持 Reduced Motion/High Contrast/Text Scale，只是 shell 没有全部接线。
- **右键菜单已有实践**：会话行、工作区行支持 `RightClick`（`menus_test.go`、`workspaces_test.go`），说明"原生上下文菜单"在框架里可行，可以扩展到文本/消息。
- **确定性的无头像素/交互测试能力**（`ui.Render` + `ui.NewTester`）是真实可行性红利——**问题不在工具，而在 KPI 选错了对照物**（对照 DSH 而不是对照自身回归 + 平台规范）。

---

## 3. 逐屏 heuristic evaluation

评级：✅ 良好 · ⚠️ 有问题 · ❌ 明显违反桌面惯例。每条给证据 + 桌面应有的做法。

### 3.1 Home

| 启发式 | 评级 | 观察与证据 | 桌面应有 |
| --- | --- | --- | --- |
| 信息密度/空间利用 | ⚠️ | 1600×1000 下主体仅 hero + composer，大量留白（截图 `home.png`）；这是 Web 首页密度 | 桌面可用"欢迎/最近工作区/继续上次任务"三栏或紧凑的启动面板；窗口拉大时应利用空间（多栏、最近列表） |
| 可发现性/命令入口 | ⚠️ | 账户/设置/关于/退出都在左下 `⋯ More` 弹出菜单（`internal/shell/menus.go`），没有系统菜单栏承载 | 系统菜单栏（App 菜单/File/View/Window）+ 明确的"新建会话 Cmd+N / 设置 Cmd+, / 退出"；Windows/Linux 也应有菜单或命令栏（Microsoft Commanding basics 的 command surfaces） |
| 品牌一致性 | ❌ | hero 文案 `Into the Unknown` + `Preview` 是 DSH 的产品文案（截图） | ZeoKeel 自己的品牌语言；即使保留 DSH 视觉令牌，文案/品牌应替换 |
| 键盘效率 | ⚠️ | Cmd+K 聚焦 composer、Cmd+N 新建会话（`shortcuts.go`）；但新用户无法从菜单发现 | 菜单 + 加速器 + 快捷键面板三处一致 |
| 空态 | ✅ | Home 本身即欢迎态，构图干净 | 保留，但注意与"桌面工作区"心智模型对齐 |

### 3.2 Session

| 启发式 | 评级 | 观察与证据 | 桌面应有 |
| --- | --- | --- | --- |
| 文本选择/复制 | ❌ | 消息区无 `Selectable`；复制/重试/反馈是 hover 式小图标（`message_actions.go`） | 拖选、双击选词、Cmd+C、右键 Copy/Select All；选区可跨消息（mygo `ui.Selectable` 已支持，需接入） |
| 滚动 | ⚠️ | 无 `Scrollbars(` 调用，默认 hover overlay；长对话无位置感/无拖拽滚动条 | 按平台显示滚动条（Windows 常显；macOS 跟随系统"显示滚动条"偏好），支持 PageUp/PageDown/Home/End |
| 错误反馈/恢复 | ⚠️ | 截图里 `Task stopped` 后直接给出内部英文错误（"The DSH process exited…"），恢复入口不显眼；错误文本未本地化 | 面向用户的错误摘要 + 一键重试/继续；详情可展开；i18n 覆盖错误文案 |
| 键盘可达性 | ✅/⚠️ | 有 Tab 可达性测试；但"工具卡展开/收起、消息更多操作"等交互是否全部键盘可达、焦点顺序是否合理，测试只覆盖部分 | 全量 Tab 顺序回归 + 焦点环可见性 + F10/Shift+F10 菜单键（GNOME HIG 标准导航键） |
| 动效 | ⚠️ | 流式淡入/消息入场/三点/呼吸/Spinner 等多套动效（`motion.md`），仅部分响应 ReduceMotion | 语义化"有意义的动效"（流式、进行中），去除装饰性延展；统一 ReduceMotion 降级 |
| 后台/长任务 | ❌ | 无系统通知；关窗隐藏后 turn 完成/失败/待审批不可见（`app.go` 关窗隐藏 + 无通知调用） | 系统通知 + Dock/taskbar 角标 + 托盘状态；审批请求必须能唤醒用户 |
| 信息层级 | ✅ | 会话头/消息/工具卡/用量行的层级接近 DSH，可读性好 | 保留 |

### 3.3 Plugins

| 启发式 | 评级 | 观察与证据 | 桌面应有 |
| --- | --- | --- | --- |
| 控件语义 | ⚠️ | 页面内自绘 toggle 开关；键盘 Space 切换是否可用需测试覆盖（有 a11y 测试，但未逐控件确认） | 原生 toggle 行为（Space 切换、焦点环、可访问名称）；危险开关需确认/撤销 |
| 产品概念冲突 | ⚠️ | 右上 `+ Add plugin` 与"只做内置插件"的定位冲突（`docs/PLAN.md` §13 已列为待拍板） | 去掉安装入口，或改为"查看内置插件说明"；避免暗示不存在的市场能力 |
| 反馈 | ⚠️ | 开关生效无确认/撤销/影响说明；错误路径无截图证据 | Toast + Undo（Microsoft Commanding basics：可撤销的操作给 undo；不可撤销的给确认） |
| 空态/错误态 | ⚠️ 证据不足 | 未见错误/空列表截图（内置列表恒有内容） | 加载失败/插件不可用的降级态 |

### 3.4 Settings

| 启发式 | 评级 | 观察与证据 | 桌面应有 |
| --- | --- | --- | --- |
| 窗口模型 | ❌ | 800×800 居中模态 + 全窗 scrim（截图；`dark-narrow.md` 的 0.24/0.5 scrim） | 独立设置窗口（macOS Settings / Windows Settings 风格），Cmd+, 打开，可与其他窗口并存；Raycast 明确：「Settings open in a separate native window, not a modal or a side panel」 |
| 可访问性设置 | ❌ | "Font size 只影响对话内容"（截图文案）+ `TextScale`/`HighContrast` 0 调用 | 跟随系统文字缩放/高对比/减少动效；应用内字号设置应覆盖全局或明确标注范围 |
| 平台整合 | ⚠️ | "Open configuration file" 是普通按钮 | "Reveal in Finder/Explorer"、拖拽导出等平台惯例操作 |
| 键盘 | ⚠️ | 有设置内快捷键行（Edit shortcuts），但设置窗口本身无系统菜单项 | Cmd+, 从菜单栏可发现；Tab/方向键导航完整 |
| 信息层级 | ✅ | 分组清晰（General/Models/Built-in plugins/Agent presets），行内说明好 | 保留 |

### 3.5 Automation

| 启发式 | 评级 | 观察与证据 | 桌面应有 |
| --- | --- | --- | --- |
| 空态 | ✅ | 中心图标 + 说明 + CTA（`automation.png`）清晰 | 保留；可加"新建"菜单项入口 |
| 后台运行可见性 | ❌ | 定时任务在后台执行；无系统通知/角标（同 Session） | 通知中心记录、托盘状态、失败重试入口 |
| 时间/时区/本地化 | ⚠️ 证据不足 | 未见时区展示与本地化截图 | 明确时区、重复规则的人类可读摘要 |
| 破坏性操作 | ⚠️ 证据不足 | 删除/停用是否有确认/撤销未见截图 | 确认或 Undo |

### 3.6 跨屏（全局）

| 项 | 评级 | 观察与证据 | 桌面应有 |
| --- | --- | --- | --- |
| 系统菜单栏 | ❌ | `App.SetMenu` 0 处；仅托盘菜单 | App/File/Edit/View/Window 菜单 + 命令 accelerator；macOS 默认菜单之外补应用命令 |
| 快捷键体系 | ⚠️ | 窗口级快捷键约 13 个可编辑绑定（`bindShortcut` 13 处）+ 固定键；未在菜单中展示 | 菜单驱动快捷键（可发现）、标准 Cmd+W/Q/1..5、命令面板兜底 |
| 多显示器/DPI | ⚠️ 证据不足 | 像素门禁只测 `ui.Render(..., scale=1)`（`screens_test.go`），无 2x DPR 断言；mygo 本身支持 scale | 2x/1.5x 渲染回归；字体缩放 150%/200% 冒烟 |
| IME | ⚠️ 证据不足 | composer 用 mygo editor，框架声称支持 IME；但自绘控件 + 自定义样式下的候选窗位置/组合文本未验证 | 真机中文/日文/韩文 IME 测试；组合期间不重排、不丢字 |
| 触摸/笔 | ⚠️ | hover-only 操作在 Windows 触屏二合一设备不可用；GNOME HIG 明确禁止依赖 hover | 常显/焦点显示操作，或长按菜单 |
| 高对比/减少动效 | ⚠️ | 只有 Light/Dark 主题；`HighContrast` 未接线；`ReduceMotion` 仅 1 处接线 | 接入系统偏好；高对比下边框/焦点环可见 |
| 品牌/法律 | ⚠️ | 视觉与文案复刻 DSH（含 hero 文案）；见 §4.5 的 trade dress 讨论 | 保留"视觉语言"可以，逐像素+逐文案复制风险更高；具体风险需法务判断 |

---

## 4. 外部研究（平台规范、Web→桌面、同类产品、克隆风险）

> 下表每条 = URL + 一句话结论 + 对 ZeoKeel 的含义。共 12 个来源（> 要求的 6 个）。

### 4.1 平台 HIG：原生控件、键盘、无障碍

| # | 来源 | 一句话 | 对 ZeoKeel 的含义 |
| --- | --- | --- | --- |
| 1 | [GNOME HIG — Pointer & Touch](https://developer.gnome.org/hig/guidelines/pointer-touch.html) | **「Pointer hover should not be relied upon for revealing actions or essential information」**；所有指针操作都应能用键盘完成；副操作（右键）应提供上下文菜单 | 直接否决"hover 才出现的操作"作为唯一入口；会话行 `⋯`、消息操作需常显或焦点显示 |
| 2 | [GNOME HIG — Keyboard](https://developer.gnome.org/hig/guidelines/keyboard.html) | 每个动作都要可键盘完成；用平台标准快捷键；自定义 UI 必须支持 Tab/Return/Space/F10/Menu/Shift+F10/Esc 等标准导航键 | ZeoKeel 需要标准菜单键与上下文菜单键；快捷键要可发现（菜单/快捷键面板） |
| 3 | [GNOME HIG — Accessibility](https://developer.gnome.org/hig/guidelines/accessibility.html) | 每个元素都要有可访问名称；测试要高对比、大字号、纯键盘、屏幕阅读器、屏幕键盘 | 自绘 shell 必须把 accessible name/role/state 做成系统能力，而不是逐屏补丁；TextScale/HighContrast 必须接线 |
| 4 | [Microsoft Learn — Commanding basics](https://learn.microsoft.com/en-us/windows/apps/design/basics/commanding-basics) | 命令要放在正确的 command surface（canvas/command bar/menu bar/context menu）；要提供命令反馈；可撤销的用 undo、不可撤销的用确认 | ZeoKeel 的应用命令应进菜单栏/命令栏；插件开关、删除会话等要有 undo/确认 |
| 5 | [Microsoft Learn — Design basics for Windows apps](https://learn.microsoft.com/en-us/windows/apps/design/basics/) | Windows 设计基础：标准命令元素、导航、内容密度与类型 ramp | 自绘可以，但密度/层级/控件语义应服从 Windows 平台语言；"看起来像 Web"不是差异化 |
| 6 | [Apple Developer — Considerations for Creating Custom Controls](https://developer.apple.com/news/?id=12022010c) | 只有平台没有的元素才建议自绘；自绘控件必须遵循 Apple 的界面设计原则 | ZeoKeel 自绘是技术约束，不是产品理由；自绘更要主动补齐平台行为（菜单、焦点、a11y） |
| 7 | [Apple HIG — Designing for macOS](https://developer.apple.com/design/human-interface-guidelines/designing-for-macos) | macOS HIG 入口：窗口、菜单栏、键盘、全屏、外观等平台约定 | 作为 macOS 本地化 checklist 的权威入口（页面为 JS 渲染，本报告只把它当作规范入口引用；具体条款需在浏览器查阅） |

### 4.2 Web app → 桌面：UX 陷阱与经验

| # | 来源 | 一句话 | 对 ZeoKeel 的含义 |
| --- | --- | --- | --- |
| 8 | [Raycast — A Technical Deep Dive Into the New Raycast](https://www.raycast.com/blog/a-technical-deep-dive-into-the-new-raycast) | 原文：「The easiest way to make a web app feel wrong on the desktop is to follow web conventions where native ones exist」；并列出：**不要 cursor:pointer、不要 hover 高亮、Settings 用独立原生窗口、popover/tooltip 用原生窗口**、不要闪烁；同时承认 WebView 下 a11y/IME/拖拽更费劲 | 这是与 ZeoKeel 最接近的公开经验：即使 UI 用 Web 技术，交互也必须"去 Web 化"。ZeoKeel 现状正好踩中 hover 高亮 + 设置模态 + 窗口内 tooltip 三条 |
| 9 | [Daring Fireball — "Electron and the Decline of Native Apps"（存档重发）](https://daringfireball.net/linked/2026/07/04/from-the-archive-electron-decline-of-native-apps) | 桌面用户（尤其 macOS）会在意 HIG 违背与原生手感；文中「For every Claude (Electron) there's a ChatGPT (AppKit)」说明 AI 助手桌面产品两条路线并存，且原生路线被用户认可 | AI Agent 桌面产品并非只能 Web 化；ZeoKeel 用 mygo 自绘反而具备"原生手感"的潜力，不应主动放弃 |
| 10 | [Zed — Zed is 1.0](https://zed.dev/blog/zed-is-1-0) | Atom/Electron 团队复盘：「Web 技术带来快速，但设置了天花板」，于是自研原生 GPUI（Rust/GPU）来做高性能编辑器 | 自绘/原生的价值主张成立：性能、手感、与系统整合；ZeoKeel 的 mygo 路线与之一致，目标应是"原生桌面 Agent"而非"浏览器壳" |

### 4.3 视觉相似 vs 可用性/一致性（研究依据）

| # | 来源 | 一句话 | 对 ZeoKeel 的含义 |
| --- | --- | --- | --- |
| 11 | [Nielsen Norman Group — Maintain Consistency and Adhere to Standards (Heuristic #4)](https://www.nngroup.com/articles/consistency-and-standards/) | 一致性分内部一致性与**外部一致性（平台/行业惯例）**；遵循标准提升 learnability、降低困惑；Jakob's Law：用户带着其他产品的预期来 | 与 DSH 内部一致 ≠ 与桌面平台外部一致；迁移用户的预期主要来自 macOS/Windows 应用，不只来自 DSH |
| 12 | [Nielsen Norman Group — The Aesthetic-Usability Effect](https://www.nngroup.com/articles/aesthetic-usability-effect/) | 好看的界面会使用户宽容小的可用性问题，并**在可用性测试中掩盖问题**；功能/严重可用性问题仍会反噬 | "像素还原"带来的好感会掩盖交互缺陷；验收不能只看像素 diff 与主观"像不像"，必须做任务测试 |
| 13 | [W3C WAI — Understanding SC 1.4.13 Content on Hover or Focus](https://www.w3.org/WAI/WCAG21/Understanding/content-on-hover-or-focus.html) | 由 hover/focus 触发的内容必须可关闭、可悬停、可持续；能 hover 触发的也应能 focus 触发（见 SC 2.1.1） | hover 菜单/提示必须同时支持键盘 focus 与 Esc 关闭；仅 hover 操作不合规 |

> 注：以上为规范/经验证据，不保证覆盖所有平台细节；具体平台行为仍需真机验证。

### 4.4 同类桌面 AI / 编码 Agent 的 UI 取向（公开信息）

| 产品 | UI 取向（公开证据/可信观察） | 启示 |
| --- | --- | --- |
| **Claude Desktop** | 基于 Electron（DF 文章称其为 "Electron turd"，并批评其 Mac 体验）；跨平台一致但被原生派批评 | Web 技术可行但并非默认正确；桌面用户会评价"手感" |
| **ChatGPT desktop (macOS)** | 原生 AppKit 路线（DF 原文："For every Claude (Electron) there's a ChatGPT (AppKit)"），系统级快捷唤起是卖点 | AI 助手桌面端原生路线存在并被认可 |
| **Cursor / Windsurf** | VS Code 分支（Electron）生态，插件生态与跨平台优先 | 工具型 AI 产品用 Web 技术换生态/速度是合理取舍 |
| **Zed** | 自研原生 GPU UI（Rust/GPUI），把性能与手感作为差异化 | 自绘原生可以做高端体验；ZeoKeel 的 mygo 自绘与之同构 |
| **Warp** | 原生 Rust/自绘终端，强调输入与渲染性能（公开资料） | 命令行 Agent 类产品也走原生自绘 |
| **Raycast** | 混合：原生 shell（Swift/AppKit、C#/WPF）+ 系统 WebView；**明确按平台惯例本地化** | 最接近 ZeoKeel 的参考：壳原生、内容可自定义，但交互必须本地化 |

**结论**：没有"桌面 AI Agent 必须长成 Web 聊天页"的行业共识；相反，头部产品要么原生、要么混合但强本地化。ZeoKeel 现在把"Web 客户端像素"当基准，是少数派路线。

### 4.5 "Pixel-perfect clone" 的维护/品牌/法律风险（谨慎、不做法学结论）

| 风险 | 说明 | 证据 |
| --- | --- | --- |
| **维护** | 目标随 DSH 发版漂移：80 张基线截图、53 份 CSS、108 条控件行为需要在每次 DSH 更新后重采集（`docs/PLAN.md` 附录 B 需带鉴权 token 的 URL，采集不可完全自动化）；`docs/EVOLUTION.md` G1 记录仓库甚至没有 git（`git rev-parse` = not a repository），像素基线没有版本化回滚 | 本仓库文档与命令可复核 |
| **品牌** | 目前界面连 hero 文案 `Into the Unknown`、`Preview` 徽章都照搬 DSH（截图）；用户会把 ZeoKeel 当成 DSH 的皮肤，而不是独立产品；"迁移用户识别价值"与"自有品牌资产"冲突 | `artifacts/screens/home.png` |
| **法律（不构成意见）** | 美国法下 UI 的 "look and feel" 可能通过版权（expression 而非 idea/method of operation）或 trade dress（显著性 + 非功能性 + 混淆可能性）路径主张；trade dress 保护的是整体外观/组合，而非单个通用控件；是否存在风险取决于具体元素组合、市场与混淆证据，**需法务判断** | [Byerly, "Look And Feel Protection Of Web Site User Interfaces: Copyright Or Trade Dress?" (Santa Clara Computer & High Tech. L.J., 1998)](https://cyber.harvard.edu/property/protection/resources/byerly_unedited.html) |
| **误判** | 逐像素复制不一定更安全：越接近"整体外观组合"，越可能落入 trade dress 的"整体印象/混淆"分析；而保留视觉语言 + 本地化交互反而更像常见行业做法 | 同上；此为设计策略建议，不是法律结论 |

---

## 5. 「视觉相似」vs「交互相似」：哪个对迁移用户更重要？

### 5.1 结论

**短期（前 3 秒/首次打开）视觉相似贡献识别与信任；长期（第 2 天起）交互相似决定留存。对"从 DSH Web 迁移到 ZeoKeel 桌面"的用户，交互（尤其平台交互）比视觉更重要，且不可替代。**

理由链：
1. **心智模型主要是交互的**：用户记住的是"Cmd+K 搜会话""设置在哪打开""任务完成会通知我""右键能复制"。视觉只提供"这是同一个产品"的线索（NNG Heuristic #4 的 learnability 论证）。
2. **迁移用户不是只熟悉 DSH**：他们在桌面同时用 VS Code/Cursor、浏览器、系统设置；平台惯例是更强的外部一致性（NNG：外部一致性 + Jakob's Law）。
3. **视觉相似会掩盖交互缺陷**：Aesthetic-Usability Effect 说明"看起来对"会让测试者忽略"用起来不对"；像素 diff 降低与主观满意度上升可能同时发生，这是**度量陷阱**。
4. **Web 用户习惯 ≠ Web 页面习惯**：真实 Web 用户有键盘、右键、文本选择、滚动条、系统通知、菜单栏；当前 ZeoKeel 恰好把"浏览器提供的这些能力"丢掉了，只保留了页面外观——所以它甚至不是"像 Web 一样交互"，而是"像一张 Web 截图一样交互"。

### 5.2 迁移用户的真实预期矩阵

| 用户预期 | 来源 | 现状 | 优先级 |
| --- | --- | --- | --- |
| 文本可选择/复制 | 桌面 + 浏览器通用 | ❌ 无 Selectable | P0 |
| Cmd/Ctrl+C/V/A、右键菜单 | 桌面 + 浏览器通用 | ⚠️ 输入框由框架处理；消息区缺失 | P0 |
| 菜单栏里能找到命令 | 桌面 | ❌ 应用命令未注册 | P0 |
| 设置是独立窗口/可并存 | macOS/Windows | ❌ 应用内模态 | P1 |
| 长任务完成有系统通知 | 桌面 Agent 核心 | ❌ 无 | P0 |
| 滚动条/滚动位置 | 桌面 + 浏览器 | ⚠️ hover overlay | P1 |
| 窗口大小/位置被记住 | 桌面 | ❌ 固定 1600×1000 | P1 |
| 跟随系统字号/高对比/减少动效 | 桌面无障碍 | ⚠️ 仅 ReduceMotion 部分接入 | P1 |
| 视觉像 DSH | 迁移用户 | ✅ 已达成 | P2（保留即可） |
| 快捷键与 DSH Web 一致 | 迁移用户 | ⚠️ 部分（Cmd+K/Cmd+N/Cmd+,） | P2（保留） |

> 说明：P0 = 不修就会让用户认为"这不是桌面应用"；P1 = 影响日常效率；P2 = 迁移友好度。

### 5.3 Web 惯例 vs 桌面惯例：哪些会变差、哪些必须本地化

| 类别 | Web 惯例（当前继承的） | 桌面后果 | 平台惯例（应本地化） |
| --- | --- | --- | --- |
| 操作揭示 | hover 才显示行内按钮/工具提示 | 触屏不可用、键盘用户依赖 Tab 试错；可发现性差 | 操作常显或焦点时显示；Esc 可关；F10/Shift+F10 菜单键 |
| 控件反馈 | 行 hover 高亮、cursor:pointer | 桌面（尤其 macOS）显得"像网站" | 按钮按下态/焦点环；克制 hover 高亮（Raycast 经验） |
| 文本 | 不可选、复制按钮 | 与桌面/浏览器习惯冲突 | `Selectable` + 原生右键 Copy/Select All + Cmd+C |
| 滚动 | 隐藏/悬停滚动条 | Windows 用户找不到滚动位置；长文档无拖拽 | 跟随系统滚动条显示策略；键盘滚动 |
| 容器 | 应用内 modal + scrim 表示设置 | 阻断主窗口操作；不符合平台设置窗口预期 | 独立 Settings 窗口、Sheet（macOS）或系统对话框 |
| 弹出层 | tooltip/popover 限制在窗口内、可能被裁剪 | 靠近窗口边缘时体验差 | 原生 popover/tooltip 窗口（Raycast 经验）；至少允许越界绘制 |
| 命令 | 应用内 More 菜单、快捷键面板 | 系统菜单栏空、快捷键不可发现 | App/File/Edit/View/Window 菜单 + accelerator |
| 后台 | 应用内 Toast | 切走后完全错过 | 系统通知、Dock/taskbar 角标、托盘状态 |
| 窗口 | 单窗口、固定初始尺寸 | 无法恢复工作现场 | 窗口状态持久化、多窗口/分屏（可选） |
| 主题/无障碍 | 固定浅/深令牌 | 系统字号/高对比/减少动效失效 | 接入 `Preferences`（TextScale/HighContrast/ReduceMotion）+ 2x DPI 回归 |
| 输入 | 自绘输入 + 自定义样式 | IME 候选窗/组合文本风险 | 真机 IME 验证；组合期不重排 |

---

## 6. 决策选项分析：A / B / C

### 6.1 对比总表

| 维度 | **A. 维持三重 1:1**（像素+交互+功能对齐 DSH Web） | **B. 视觉语言保留、去像素门禁、交互本地化（推荐）** | **C. 全面原生重设计**（mygo 默认外观 + 自有设计语言） |
| --- | --- | --- | --- |
| 用户价值 | 迁移识别度最高；"和 DSH 一模一样" | 视觉熟悉 + 桌面手感；日常效率显著提升 | 平台归属感最强；长期体验最好 |
| 差异化 | 无（是 DSH 的桌面复制品） | 中：视觉像 DSH，交互像原生桌面 Agent | 高：自有品牌与体验 |
| 维护成本 | **最高且持续**：DSH 每次发版重采集 80 屏/53 CSS/108 行为；像素门禁 + 动效拟合持续投入；且像素 1:1 不可达（AA 地板） | 中：去掉 DSH 基线对比，保留自身视觉回归；交互层一次性重做约 10 类能力 | 高前期、低长期：设计系统/五屏/组件/测试基线重来；但不再被 DSH 绑架 |
| 主要风险 | 桌面体验天花板（hover/menu/modal/通知/文本选择）；跨平台渲染差异让基线不可移植；品牌/法律风险（逐像素+逐文案） | 迁移期"像 DSH 但操作不同"的认知落差；DSH 特有交互需重新设计；仍需谨慎处理视觉相似度（法律边界） | 丧失迁移识别价值；前期投入大；现有 16.3k 行 shell 与 80 基线大部分作废 |
| 代码影响 | 不减反增（每次 DSH 升级都是回归） | **净减少**：可删除/降级 DSH 基线像素对比、逐像素常量、部分装饰动效（估算 UI 侧 2–4k 行 + 门禁维护成本；新增原生交互 1–2k 行，见 §10 不确定性） | 短期 +（重写），长期 −（组件化后更省） |
| 适用前提 | ZeoKeel 是 DSH 官方/半官方桌面壳，或用户明确只接受"和 Web 完全一样" | 面向 DSH 用户的桌面替代品，6 个月内交付 | 独立品牌、长期产品；或 B 完成后的下一阶段 |

### 6.2 逐项展开

#### A. 维持像素/交互 1:1

**成立条件**：产品定位就是"DSH 的桌面客户端"，且 DSH 团队认可（甚至合作）。
**不成立的理由（证据）**：
- 像素目标不可达：`docs/STATUS.md` 已量化"~85% 差异是文本 AA 地板"，session 在容差 24/36/48 下仍有 5.59/5.05/4.52% 差异；门禁预算只好放宽到 8.5%。继续优化的是"渲染器拟真度"，不是用户价值。
- 交互 1:1 与桌面惯例冲突：hover-only、模态设置、无菜单栏、无通知（见 §3）。
- 每个 DSH 版本都会打破基线；仓库无 git（`docs/EVOLUTION.md` G1），基线无法可靠回滚；DSH Web 采集需要鉴权 token URL（`docs/PLAN.md` 附录 B），CI 难以自动完成。
- 品牌/法律风险：连 hero 文案都复刻（截图），且 trade dress 分析关注"整体外观组合"，越接近越难主张独立。

**结论**：只在明确的官方合作/迁移器场景下保留 A 的**视觉轴**，不建议保留"交互轴 1:1"和"像素硬门禁"。

#### B. 混合：保留 DSH 视觉语言（tokens/几何/密度），交互本地化（推荐）

**保留什么**：`internal/design` 的颜色/字体/间距/圆角令牌；五屏信息架构与密度；DSH 的 composer、工具卡、审批卡、轨迹等产品概念；Cmd+K/Cmd+N/Cmd+, 等迁移快捷键。
**改什么（按优先级）**：
1. **P0 桌面交互**：系统菜单栏 + accelerator（mygo `App.SetMenu`，保留 `RoleAppMenu`/`RoleEditMenu` 让 Cmd+Q/Cmd+C/V 生效）；文本 `Selectable` + 原生右键；消息/行操作的常显或焦点显示；系统通知（完成/失败/待审批）；窗口状态持久化。
2. **P1 平台观感**：Settings 独立窗口；滚动条跟随系统策略；`Preferences` 的 TextScale/HighContrast/ReduceMotion 接线；tooltip/popover 越界与焦点行为；2x DPI 回归。
3. **P2 动效瘦身**：保留语义动效（流式、进行中、布局过渡），去掉为"像 DSH"补的装饰动效；统一 ReduceMotion。
4. **门禁改造**：`pixeltest` 从"对比 DSH 基线"改为"自有版本视觉回归 + 令牌一致性 + 2x scale"；DSH 截图降级为 `docs/baseline/` 参考素材，不进 CI。
**代价**：约 10 类交互改造，估计 2–4 周 1 人（不含通知/窗口等多平台细节）；需要真机矩阵（macOS/Windows/Linux）。
**收益**：UI 侧工作量净减（不再追 DSH 发版/像素拟合），桌面体验从"网页感"转为"原生感"，并保留迁移识别。

#### C. 全面原生重设计

**成立条件**：ZeoKeel 要做独立品牌（不是 DSH 迁移器），且愿意承担 1 个设计系统 + 五屏重做的前期成本。
**风险**：现有 16.3k 行 shell 生产代码、14.5k 测试、80 张基线资产大部分废弃；迁移用户识别价值归零；上市时间推迟。
**建议**：**B 是 C 的第一步**。B 完成后，视觉语言可逐步去 DSH 化（换品牌色/字体/圆角/图标语言），自然演进到 C，而不需要"推倒重来"。

---

## 7. 推荐与执行路线

### 7.1 推荐

**采用 B（视觉语言保留 + 去像素门禁 + 交互本地化），按 P0→P1→P2 分三步走；把 C 作为 6–12 个月后的品牌演进方向。**
- 若用户/产品负责人确认 ZeoKeel 只是"DSH 桌面迁移器"：B 仍然成立，A 只保留视觉轴。
- 若确认要做独立品牌：B 立即执行，C 在 B 完成后启动，不做一次性重写。

### 7.2 建议的落地清单（给工程的可执行项）

| 优先级 | 动作 | 框架能力 | 验收 |
| --- | --- | --- | --- |
| P0 | 注册应用菜单（App/File/Edit/View/Window + New Session/Settings/Plugins/Automation/Search/Quit），Windows/Linux 也注册窗口菜单 | `mygo.App.SetMenu`、`Menu`/`Role`、`Accelerator`（mygo `docs/menus.md`） | 菜单可键盘导航；accelerator 与 `shortcuts.go` 一致；Cmd+Q/Cmd+C/V 可用 |
| P0 | 消息/工具卡文本 `Selectable` + 原生右键 Copy/Select All | `ui.Selectable`（mygo 支持文本选择与上下文菜单） | 拖选、双击选词、Cmd+C、右键菜单可用 |
| P0 | hover 操作改为常显或 `Focused()` 时显示（会话行 ⋯、消息操作） | `Hovered()/Focused()` | 键盘 Tab 可达；触屏可用；Esc 关闭菜单 |
| P0 | 系统通知：turn 完成/失败/待审批/自动化完成 | mygo 通知 API；`app.go` 关闭隐藏处接状态 | 窗口隐藏时 3 类事件均能收到系统通知，点击回到会话 |
| P0 | 窗口尺寸/位置持久化 | mygo window state / 自行存 setting | 重启恢复上次窗口 |
| P1 | Settings 独立窗口（无 scrim），Cmd+, 打开 | `mygo.NewWindow` | 可与主窗口并存；关闭不退出应用；非模态 |
| P1 | 滚动条跟随系统策略；长列表键盘滚动 | `ui.Scrollbars(true/false)` + 平台偏好 | Windows 常显；macOS 跟随"显示滚动条"设置 |
| P1 | 接入 `Preferences`（TextScale/HighContrast/ReduceMotion） | mygo `ui/preferences.go` | 系统字号 150%/200%、高对比、减少动效冒烟通过 |
| P1 | 2x DPI 像素回归 | `ui.Render(..., scale=2)` | 布局 1px 不漂移；图标/阴影清晰 |
| P2 | 动效瘦身 + 统一降级 | `internal/design/motion.go` | 无装饰性延展；ReduceMotion 下全部动效瞬时完成 |
| P2 | 品牌去 DSH 化（hero 文案、Preview 徽章、产品命名/图标） | `internal/shell/home.go` 等 | 界面无 DSH 产品文案；品牌资产只用 `logo/` |
| P2 | 门禁改造：DSH 基线退出 CI，改为自身视觉回归 + 令牌断言 | `internal/pixeltest`、`screens_test.go` | CI 不含 DSH 截图对比；视觉回归基线由本项目生成 |

### 7.3 反建议（不要做）

- 不要继续把 DSH 基线的 masked diff 预算继续放大/收紧来"证明 1:1"——它已经是不可达指标的噪声。
- 不要为"补 CSS 过渡"继续新增自绘动效（`motion.md` §4 的"有意延展"）：每一条都是长期维护面。
- 不要在未验证真机 IME/高 DPI/屏幕阅读器前宣称"桌面体验对齐"。
- 不要在 B 方案中直接复制 DSH 的像素级文案/图标/布局组合而不做品牌替换（法律与品牌双重风险）。

---

## 8. 如何验证「UX 是否更好」（轻量可用性测试方案）

### 8.1 目标与假设

| 假设 | 验证方式 |
| --- | --- |
| H1：B 方案在"桌面任务"上比 A 更快、错误更少 | 对照实验：A 构建 vs B 构建，同一批任务 |
| H2：B 方案让"键盘用户/无障碍用户"可用 | 纯键盘完成全部任务 + 屏幕阅读器冒烟 + TextScale 150% |
| H3：视觉相似仍能带来迁移识别 | 首屏 3 秒识别测试（"这是什么产品？和 DSH 什么关系？"） |
| H4：hover-only 改常显不会降低界面简洁感 | 主观评分 + 界面元素数量对比 |

### 8.2 测试脚本（30–40 分钟/人，5–8 人，有 DSH Web 使用经验者优先）

1. **启动与识别（3 min）**：首次打开，请描述你看到什么、会先做什么；问"你觉得这是 Web 应用还是桌面应用？为什么？"（记录提及"菜单/滚动条/通知/窗口"等线索）
2. **新建会话并提交（5 min）**：新建会话 → 选择工作区 → 发送一条消息 → 中途停止再重试；观察是否用快捷键、是否找 hover 按钮。
3. **复制答案（3 min）**：复制部分回答（要求只复制第二段）；观察是否尝试拖选、右键、Cmd+C 还是找 Copy 图标。
4. **改设置（5 min）**：把默认权限从 Workspace Write 改为 Read Only，并解释影响；观察是否去找系统菜单/设置窗口；是否触发模态后迷失。
5. **后台任务（5 min）**：启动一个长任务后切到其他应用；问"任务完成后你怎么知道？"；观察是否有系统通知/角标。
6. **纯键盘任务（8 min）**：只用键盘：打开设置、切换深色、开启一个插件、回到会话、聚焦输入框；记录 Tab 陷阱与焦点丢失。
7. **问卷（5 min）**：SEQ（每任务 1–7 分）+ SUS（10 题）+ 2 个开放问题（最像 Web 的地方 / 最像桌面的地方）。

### 8.3 指标与验收判据

| 指标 | 目标（B 相对 A 的验收线） |
| --- | --- |
| 任务完成率（任务 2/4/6） | ≥ 90%（当前 A 预计 < 70%，尤其任务 3/5） |
| 纯键盘完成率（任务 6） | 100%（不可有键盘死路） |
| 每任务错误/求助次数 | ≤ 1 |
| SEQ 中位数 | ≥ 5.5/7（关键任务 ≥ 6） |
| SUS | ≥ 75 |
| "像桌面应用"主观判断 | ≥ 70% 受访者选择"桌面应用" |
| 3 秒识别（H3） | ≥ 50% 能说出"和 DSH 视觉相似，但更像桌面应用" |
| 系统通知触达 | 切走后 ≤ 2s 收到；点击回到对应会话 |
| a11y 冒烟 | VoiceOver/Narrator 能读出主要控件与状态；HighContrast 下边框/焦点可见；TextScale 150% 不裁切 |
| 回归 | 启动时间、内存不劣化（与 A 构建对比） |

**方法学说明**：n=8 的对照只能给方向性结论，**统计功效不足**（证据不足项）；可先用 5 人做形成性测试修问题，再用 2 个构建做一次方向性 A/B。视觉相似性测试必须与可用性测试分开做，避免 Aesthetic-Usability Effect 污染（NNG）。

---

## 9. 证据不足与待确认

1. **真机行为**：IME 组合窗位置、2x/1.5x DPI、屏幕阅读器全覆盖、多显示器、Windows 触屏——本次只做代码/无头渲染分析，未启动 GUI（任务约束）。建议在 macOS/Windows/Linux 各做一次清单化冒烟。
2. **`Selectable` 的实际效果**：全仓 0 处调用 + mygo 文档显示 opt-in，推断消息区不可拖选；但未真机验证，需确认（若 mygo `Text` 默认可选，则本条降级）。
3. **窗口默认菜单**：mygo 文档称 macOS 应用有默认菜单（App/File/Edit/View/Window）；ZeoKeel 未注册自有菜单。默认菜单的具体项目与 Windows/Linux 行为需真机确认。
4. **工作量估算**：UI 侧"可减 2–4k 行 / 20–30%"是依据现有包规模与门禁资产做的估算，未做逐文件审计；需要 owner 用 1 天做一次"删除清单"验证。
5. **用户规模/迁移意图**：DSH 有多少 Web 用户会迁到桌面、他们是否真的在意"像素一致"，缺乏数据（证据不足）。建议对 3–5 个真实 DSH 用户做访谈（30 min）再定 A/B/C 权重。
6. **法律**：本报告只做风险提示，不构成法律意见；trade dress 是否成立取决于整体外观、市场混淆证据与具体司法辖区，需法务评估。

---

## 10. 直接回答用户的两个问题

### Q1：「如果是用 mygo 的原生去实现，不搞这么多视觉效果，是不是可以减少很多代码工作量？」

**是，但省的是"对齐/拟合/验收"成本，不是"视觉实现"成本本身。** 具体：
- 可省：与 DSH 基线逐像素对比的采集/维护（80 屏 + 53 CSS + 108 行为 + 216 对比图）、像素门禁调试、为补 CSS 过渡而写的装饰动效（`motion.md` 的"有意延展"）、跨平台拟合 Web 渲染差异的无效迭代。按现有规模估算，UI 侧工作量可降 **20–30%**（估算；需 owner 验证）。
- 不可省：会话/工具/审批/轨迹等产品 UI，运行时时序，a11y 基础（反而要加强），自绘控件的焦点/键盘/IME 适配。
- 结论：**减少的是"为了看起来像 DSH 而付出的额外成本"，这部分目前约占 UI 工程的 1/4 到 1/3；占全项目（含运行时）约 1/8 到 1/6。** 不要期待"砍一半代码"。

### Q2：「从 UI/UX 设计的角度，这样（原生 mygo 实现、不搞这么多视觉效果）是不是更好？」

**分两层回答：**
- **交互层：明显更好。** 桌面惯例（菜单栏、系统通知、设置独立窗口、文本选择、滚动条、系统字号/高对比、键盘完整可达）是用户每天真实接触的界面；当前 Web 复刻在这些点上系统性缺失。外部证据：GNOME HIG 明确禁止依赖 hover 揭示操作、Raycast 的原生 Feel 清单、Microsoft command surfaces、WCAG 1.4.13。
- **视觉层：不应放弃，而应重新定位。** 品牌识别、信息密度、DSH 的视觉语言是有价值的资产；问题不是"有视觉效果"，而是"视觉以像素为单位对齐一个 Web 参考页面"。正确做法：保留令牌与整体观感，去掉像素硬门禁，动效只保留有语义的，品牌文案/图标去 DSH 化。
- **一句话**：**不搞"像素复刻"，但保留"视觉语言"；不搞"Web 交互"，全部本地化为桌面交互。** 这就是 B。

---

## 11. 参考来源清单（可直接点开）

1. GNOME HIG — Pointer & Touch: https://developer.gnome.org/hig/guidelines/pointer-touch.html
2. GNOME HIG — Keyboard: https://developer.gnome.org/hig/guidelines/keyboard.html
3. GNOME HIG — Accessibility: https://developer.gnome.org/hig/guidelines/accessibility.html
4. Microsoft Learn — Commanding basics: https://learn.microsoft.com/en-us/windows/apps/design/basics/commanding-basics
5. Microsoft Learn — Design basics for Windows apps: https://learn.microsoft.com/en-us/windows/apps/design/basics/
6. Apple Developer — Considerations for Creating Custom Controls for your Mac App: https://developer.apple.com/news/?id=12022010c
7. Apple HIG — Designing for macOS: https://developer.apple.com/design/human-interface-guidelines/designing-for-macos
8. Raycast Blog — A Technical Deep Dive Into the New Raycast: https://www.raycast.com/blog/a-technical-deep-dive-into-the-new-raycast
9. Daring Fireball — From the DF Archive: 'Electron and the Decline of Native Apps': https://daringfireball.net/linked/2026/07/04/from-the-archive-electron-decline-of-native-apps
10. Zed Blog — Zed is 1.0: https://zed.dev/blog/zed-1-0
11. Nielsen Norman Group — Maintain Consistency and Adhere to Standards (Heuristic #4): https://www.nngroup.com/articles/consistency-and-standards/
12. Nielsen Norman Group — The Aesthetic-Usability Effect: https://www.nngroup.com/articles/aesthetic-usability-effect/
13. W3C WAI — Understanding SC 1.4.13 Content on Hover or Focus: https://www.w3.org/WAI/WCAG21/Understanding/content-on-hover-or-focus.html
14. Byerly, "Look And Feel Protection Of Web Site User Interfaces: Copyright Or Trade Dress?" (Santa Clara Computer & High Technology Law Journal, 1998): https://cyber.harvard.edu/property/protection/resources/byerly_unedited.html

---

## 附录 A：本报告证据索引（可复核）

| 结论 | 证据位置 |
| --- | --- |
| 五屏与 DSH 基线像素近似 | `docs/STATUS.md`（masked diff 0.93/6.40/3.17/2.31/1.34%）；`artifacts/screens/*.png`；`baseline/screenshots/`（80 张） |
| 像素门禁预算已放宽到 8.5% | `internal/shell/screens_test.go`（maskedBudget: home 0.020、session 0.085、plugins 0.045、settings 0.042、automation 0.022；渲染仅 scale=1） |
| 像素差异 ~85% 是 AA 地板 | `docs/STATUS.md`（"剩余差异的性质（已量化）"） |
| shell 生产/测试行数 | `find internal/shell -name '*.go' ! -name '*_test.go'` = 16,288；`*_test.go` = 14,514 |
| 13 个动效时长 + 5 套缓动 + 有意延展 | `docs/baseline/motion.md` §1/§2/§4 |
| hover 高亮 35 处 | `grep -rn "Hovered()" internal/shell` = 35 |
| 行内操作仅 hover/当前行 | `internal/shell/workspaces.go:356`、`:397` |
| 无 `Selectable` 调用 | `grep -rn "Selectable(" internal/shell` = 0（mygo `ui/api_gen.go` 说明 opt-in；需真机确认） |
| 无 `Scrollbars(` 调用、默认 hover 显示 | `grep -rn "Scrollbars(" internal` = 0；mygo `ui/api_gen.go` `Scrollbars` 文档 |
| Settings 模态 + scrim 0.24/0.5 | `artifacts/screens/settings.png`；`docs/baseline/dark-narrow.md` |
| 无应用菜单 | `grep -rn "App.SetMenu\|SetApplicationMenu" internal cmd` = 0；仅 `internal/app/app.go:392` 托盘菜单 |
| 快捷键有限 | `internal/shell/shortcuts.go`（Cmd+N/Cmd+,/Cmd+K/Esc）；`internal/shell/chrome.go`（Cmd+B）；`bindShortcut` 13 处 |
| 无系统通知 | `grep -rn "Notify\|Notification" internal cmd --include=*.go`（生产代码无通知调用）；关窗隐藏 `internal/app/app.go:130-136` |
| 无窗口状态持久化 | `internal/app/app.go` `WindowOptions{Width:1600,Height:1000,MinWidth:900,MinHeight:600}`；无 WindowState 调用 |
| TextScale/HighContrast 未接线 | `grep -rn "TextScale\|HighContrast" internal cmd` = 0；mygo `ui/preferences.go:7-25` 提供能力 |
| ReduceMotion 部分接入 | `internal/shell/session_live.go:337` |
| a11y 基础投入 | `internal/shell/a11y_test.go`（Tab 可达性）、`a11y_expanded_test.go`（access tree states）、`Role(` 58 处 / `Focusable()` 82 处 |
| 右键菜单已有 | `internal/shell/menus_test.go`、`workspaces_test.go`（RightClick 用例） |
| 无 git 仓库 | `git rev-parse --is-inside-work-tree` → fatal: not a git repository（与 `docs/EVOLUTION.md` G1 一致） |
| 品牌文案来自 DSH | `artifacts/screens/home.png`（"Into the Unknown" + "Preview" 徽章） |
| mygo 菜单能力 | mygo v0.7.4 `docs/menus.md`（`mygo.App.SetMenu`、Role、Accelerator、macOS 默认菜单） |

## 附录 B：可直接交给工程的"最小 B 方案"验收清单

- [ ] 系统菜单栏出现 App/File/Edit/View/Window，含 New Session（Cmd+N）/Settings（Cmd+,）/Plugins/Automation/Search/Quit；Windows/Linux 窗口有同一套菜单
- [ ] 消息区可拖选 + 右键 Copy/Select All + Cmd+C；输入框 Cmd+A/C/V 正常
- [ ] 会话行 `⋯`、消息操作在键盘 focus 时可见并可操作；触屏可用
- [ ] Settings 是独立窗口、非模态、可与其他窗口并存
- [ ] 关闭窗口后 turn 完成/失败/待审批触发系统通知，点击回到对应会话
- [ ] 重启恢复窗口尺寸/位置
- [ ] Windows 常显滚动条；macOS 跟随系统滚动条偏好；长列表键盘滚动
- [ ] 系统 TextScale 150%/200%、HighContrast、ReduceMotion 冒烟通过
- [ ] 2x DPI 渲染回归通过
- [ ] CI 不再包含 DSH 基线对比；视觉回归基线由本项目生成
- [ ] 界面无 DSH 产品文案（hero/Preview/命名）；品牌仅用 `logo/` 资产
