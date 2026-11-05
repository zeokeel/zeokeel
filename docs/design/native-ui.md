# ZeoKeel 原生视觉方向（v0.1，2026-11-05）

> 状态：已定方向，逐屏实施中。依据：[ADR 0001 退役像素对齐](../decisions/0001-retire-dsh-pixel-parity.md)、[ADR 0002 品牌主题基础](../decisions/0002-brand-theme-foundation.md)。
> 目标：**ZeoKeel = DSH 桌面替代/迁移器 + 独立品牌产品**。功能对齐 DSH；视觉与交互由 ZeoKeel 自己设计，回归 mygo 原生能力（`ui.Theme` + 原生控件 + 系统字体/度量），不再以任何外部页面的像素为验收标准。

---

## 1. 设计原则

| # | 原则 | 含义 |
| --- | --- | --- |
| P1 | **原生优先** | 能用 mygo 原生控件（Button/PrimaryButton/TextArea/Select/MenuButton/Tabs/Collapsible/List/Toast/Tooltip…）就不手搓；控件行为（键盘、焦点、a11y、IME、DPI）随控件免费获得 |
| P2 | **令牌说话** | 尺寸/间距/圆角/字号一律走 `brand` 与 `design` 令牌、`Theme.Space/Rem/Radius`；不在业务代码里写 DSH 的魔法像素值（877/909/28/52…） |
| P3 | **层次靠结构** | 用表面色、边框、留白和字号建立层级，不靠阴影堆叠/装饰动效；动效只保留有语义的（流式、进行中、布局过渡） |
| P4 | **桌面惯例** | 屏幕是桌面窗口，不是浏览器页面：操作常显或焦点可见、文本可选、菜单可发现、系统通知/窗口状态后续补齐（UX 报告 §7 的 P0/P1 清单） |
| P5 | **品牌克制** | 品牌蓝/青/紫只用在强调、焦点、选中和少量图形标；大面积中性色保证 AI 内容可读 |
| P6 | **中文优先** | 默认中文界面；系统字体栈含 PingFang/微软雅黑；文案口语化、无 DSH 产品文案 |

## 2. 品牌基础（来自 ADR 0002）

- 主色（浅色）：`#0D44F6`（logo 渐变主蓝）；hover `#0946EF`；pressed `#0B38CA`。
- 主色（深色）：`#3CBBFA`；hover `#2ECCFB`；pressed `#37E7FE`。
- 中性：背景 `#FFFFFF`、侧栏 `#F9FAFB`、表面 `#F1F3F5`、文字 `#0F1115` / 次要 / 三级；深色以 `#18181B` 系夜色为底。
- 度量：`Radius=10`、`Spacing=4`、`FontSize=14`、系统字体栈（`brand.SystemFont`）。
- 状态色：语义色（成功/警告/危险），不来自 logo。

## 3. Home 屏（第一个原生重构屏）

### 3.1 信息结构（自上而下）

```
┌ Sidebar（保留现有功能，下一步原生化） ┐ ┌ 主区 ────────────────────────────┐
│ ZeoKeel 字标 + 折叠               │ │ 顶栏：空白（可选轻量右侧面板开关）    │
│ New Session（原生主按钮）          │ │                                   │
│ Plugins / Automation（原生导航行）  │ │        ZEoKeel 图形标 + 字标        │
│ Workspaces 列表                   │ │        “今天想构建什么？”（tagline） │
│ …                                │ │   ┌ composer 卡片（原生控件） ┐      │
│ More                             │ │   │ 工作区/模式 chip 行          │      │
└──────────────────────────────────┘ │   │ 多行输入（TextArea）        │      │
                                     │   │ + 权限 模型      发送按钮   │      │
                                     │   └──────────────────────────┘      │
                                     │   最近会话/工作区（可选，空态提示）     │
                                     └───────────────────────────────────┘
```

### 3.2 与现状的差异（重点）

| 项 | 现状（DSH 复刻） | 新方向 |
| --- | --- | --- |
| Hero 文案 | `Into the Unknown` + `Preview` 徽章（DSH 产品文案） | ZeoKeel 字标/图形标 + 自有 tagline（如「今天想构建什么？」/「把想法交给 ZeoKeel」）；删除 Preview 徽章 |
| 鲸鱼图标 | DSH 图形标 | ZeoKeel 图形标（`logo/` 资产，`ui.Image`/`ui.SVG`） |
| 布局宽度 | homeStackWidth 909 / composer 877（DSH 像素） | `min(720, 内容宽 - 80)` 之类的内容列；composer 宽 = 内容列宽，内边距走 `Theme.Space(4)` |
| Composer 卡片 | 手调 28px 圆角、双层阴影、52px 输入行 | `Column` + `Theme.Surface` + `Radius(10)` + `Border(1, Border)` + 单层轻阴影；输入用 `ui.TextArea`（原生占位/多行/IME/撤销） |
| chips | 手搓无框文字 + chevron，模型显示 `1017 High` | 用 `ui.Select`（或 `ui.MenuButton` + 自定义触发样式）承载工作区/权限/模型；模型只显示名称（如 `DeepSeek-V4.1-Flash`），上下文/档位移到 tooltip |
| 发送按钮 | 手绘圆形 ↑，禁用色 `#B2C7F4`（DSH 色） | mygo 原生 `ui.PrimaryButton`（「发送」/图标），或 `ui.Button` + accent 填充；禁用态由控件自身处理 |
| 字体/字号 | DSH type scale 14/22、26px hero | `ui.Theme`：正文 14、标题 `t.Rem(2)`，字重 600；全系统字体 |
| 动效 | DSH 13 个时长/5 条曲线、装饰性延展 | mygo 原生 transition（菜单/焦点/布局），保留流式与进行中语义动效 |

### 3.3 实现约束

- 保留功能：工作区选择/新建、权限菜单、模型菜单、`/` 命令、`@` 提及、附件 chips、发送创建会话、Cmd+K/Cmd+N、右侧栏开关、i18n（en/zh）。
- 交互与 a11y 不回退：所有控件有 `Label`/`Role`/`Focusable`；键盘可达；空 composer 发送禁用；错误可见。
- 所有颜色/间距/圆角来自 `internal/brand` + `design.Tokens`，不再新增 DSH 采样色。
- 产出 headless 截图（1600×1000，浅色+深色）供评审；`go test ./internal/shell` 保持绿。

## 4. 后续逐屏路线

1. **Home（本轮 pilot）**：验证品牌主题 + 原生控件 + 新布局的观感与测试成本。
2. **Session**：消息流/工具卡用原生 `Collapsible` + `markdown` 包替换自研 markdown；composer 与 Home 共用新组件。
3. **Sidebar**：从手搓行换成 `ui.Sidebar`（键盘/首字母/a11y 免费），工作区树用 `SidebarSection`。
4. **Settings/Plugins/Automation**：原生 `Form`/`Field`/`List`/表格行；设置改为独立窗口（P1）。
5. **桌面交互 P0/P1**：系统菜单栏、文本可选+右键、操作常显/焦点、系统通知、窗口状态、滚动条/DPI/TextScale（见 [03 报告 §7](../research/03-ux-design.md)）。

## 5. 验收口径

| 维度 | 验收 |
| --- | --- |
| 品牌 | 界面无 DSH 产品文案（`Into the Unknown`/`Preview`/鲸鱼）；品牌资产只用 `logo/` |
| 令牌 | 新代码不出现新的 DSH 采样色/魔法几何；颜色/间距/圆角走 brand/design |
| 测试 | `go test ./internal/shell` 绿；交互/a11y 断言保留；截图 headless 可复现 |
| 观感 | 1600×1000 截图评审通过（浅/深）；信息层级清晰、密度比现状略松、像原生桌面应用 |
