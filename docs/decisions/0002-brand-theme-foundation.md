# ADR 0002：ZeoKeel 品牌主题基础

- 日期：2026-11-05
- 状态：**已采纳**（随「原生 UI 迁移」第一阶段落地）
- 决策者：用户（方向：停止 DSH 像素对齐、视觉/交互自有、回归 mygo 设计能力）；记录：Lead / native-theme
- 关联：[ADR 0001 退役 DSH 像素对齐](0001-retire-dsh-pixel-parity.md)、[调研总报告](../research/00-report.md)、[mygo 原生能力调研](../research/02-mygo-native.md)、[字标规格](../wordmark.md)、`logo/logo.svg`、`logo/logo-dark.svg`
- 实施：`internal/brand`（新增）、`internal/design`（改造）、本 ADR

## 1. 背景与目标

ADR 0001 已把「与 DSH 像素/交互/功能三重 1:1」降级为「功能对齐 DSH，视觉与交互自有」。但当时 `internal/design`（752 行生产代码）仍是 DSH 计算样式/基线令牌的逐值复制品：颜色、字号、间距、动效时长、注释来源全部指向 `baseline/css`、`docs/baseline` 与 DSH 前端构建产物。这批值既不再有验收意义（像素门禁已退役），又挡住后续「用 mygo 原生控件 + 自有视觉」的重构。

本轮（原生 UI 迁移第一阶段）的目标是**打地基，不逐屏改视觉**：

1. 建立 ZeoKeel 自己的品牌层 `internal/brand`，作为颜色、字体、度量的唯一来源；
2. 把 `internal/design` 从「DSH 基线令牌复制」改成「品牌主题 + 语义映射」，并清除 DSH/baseline 口径；
3. 保持 `design.Tokens` 使用面 API 不变，让仍在并行重构的 `internal/shell` 继续编译；
4. 为下一阶段的原生控件替换、自有布局、交互本地化留出稳定接口。

## 2. 决策

1. **新增 `internal/brand` 包（品牌唯一来源）**：
   - `Palette`：品牌色板 + 语义角色（背景/表面/文字/分隔线/强调色/选区/焦点/滚动条/遮罩/状态色），`LightPalette()` / `DarkPalette()` / `PaletteOf(dark)`；
   - `LightTheme() *ui.Theme` / `DarkTheme()` / `ThemeFor(dark)`：以 mygo `ui.LightTheme()/DarkTheme()` 为底 + 品牌覆盖；
   - 品牌度量：`Radius=10`、`Spacing=4`、`FontSize=14`、`SystemFont`（系统字体栈）、`MonoFont`；
   - 色板常量保留来源（`logo/logo.svg` 行号 / `docs/wordmark.md`），测试直接读取品牌资产校验。
2. **`internal/design` 改为品牌主题层**：
   - `Tokens.Light()/Dark()` 从 `brand.Palette` 1:1 派生，不再有 DSH 数值；
   - `Tokens.Theme()` 直接返回 `brand.LightTheme()/DarkTheme()`（保持 `c.SetTheme(t.Theme())` 兼容）；
   - `FontStack()/MonoFontStack()` 委托 `brand.SystemFont/MonoFont`（API 不变）；
   - 包注释与全部代码注释去掉 DSH/baseline 来源，只保留一行「历史已退役」说明；
   - `motion.go` 只删除**包外零引用**的 `AnimTick` / `AnimSlow` / `AnimBase`（grep 验证，含 `.probe` 副本排除），其余动效 helper 保留给 shell；
   - 组件几何/样式函数本轮保留（避免逐屏视觉回归、不阻塞 shell 拆除），仅把 `RadiusMD` 从 12 收敛为 `brand.Radius`（10），让手写控件与 mygo stock 控件的圆角一致。
3. **不改 `internal/shell` 生产代码**：本轮只提供品牌主题，原生控件/布局替换是下一阶段；shell 对 `design.Tokens` 的引用（本轮开始时 278 处）保持可编译。
4. **纯离线、可回滚**：不引入新依赖、不打包字体；全部改动可回到 tag `pre-native-ui`。

## 3. 品牌色板（来源：图形标 + 字标）

### 3.1 图形标渐变 stop（`logo/logo.svg`）

| 常量 | 色值 | 来源（文件:行） | 角色 |
| --- | --- | --- | --- |
| `HexCyan` | `#37E7FE` | `logo/logo.svg:4`（另见 `:31` 平涂高光） | 品牌冰青；深色主题按下态 |
| `HexBlue` | `#0946EF` | `logo/logo.svg:5`（渐变 id0 终点） | 浅色主题 AccentHover |
| `HexRoyal` | `#0D44F6` | `logo/logo.svg:12`（渐变 id2 起点） | **品牌主色 / 浅色主题 Accent** |
| `HexSky` | `#3CBBFA` | `logo/logo.svg:13`（渐变 id2 终点） | **深色主题 Accent** |
| `HexIndigo` | `#1D32FD` | `logo/logo.svg:16`（渐变 id3 起点） | 品牌蓝紫，装饰/图标备用 |
| `HexIce` | `#2ECCFB` | `logo/logo.svg:17`（渐变 id3 终点） | 深色主题 AccentHover |
| `HexViolet` | `#755BFD` | `logo/logo.svg:20`（渐变 id4 起点） | 品牌紫，夜色表面染色 |
| `HexUltra` | `#3935FB` | `logo/logo.svg:21`（渐变 id4 终点） | 品牌蓝紫，装饰备用 |

### 3.2 图形标浅色层次（高光/描边，用于图标与装饰）

| 常量 | 色值 | 来源 |
| --- | --- | --- |
| `HexMist` | `#CFE5FF` | `logo/logo.svg:8` |
| `HexPeriwinkle` | `#9BB7FA` | `logo/logo.svg:9` |
| `HexFrost` | `#A2BBFE` | `logo/logo.svg:24` |
| `HexGlow` | `#F0F8FB` | `logo/logo.svg:25` |

### 3.3 容器底色与字标墨色

| 常量 | 色值 | 来源 | 角色 |
| --- | --- | --- | --- |
| `HexWhite` | `#FFFFFF` | `logo/logo.svg:28` | 浅色页面底色 |
| `HexBlack` | `#000000` | `logo/logo-dark.svg:28` | 深色主题夜色的派生起点（不上屏，避免纯黑死板） |
| `HexInk` | `#0F1115` | `docs/wordmark.md` 颜色行 | 浅色主题主文字 / 强调填充上的文字 |
| `HexInkDark` | `#F9FAFB` | `docs/wordmark.md` 颜色行 | 深色主题主文字 |

> 品牌主色取 ADR 0001 §8.3 的建议：**单一主蓝 `#0D44F6`（浅色）/ 品牌天空蓝 `#3CBBFA`（深色）**；渐变只用于图形标与局部装饰，不做 UI 大面填充。

## 4. 语义映射

### 4.1 表面、文字、分隔线（`Palette` → `design.Tokens`）

| 语义角色 | Palette 字段 | Light | Dark | 依据 |
| --- | --- | --- | --- | --- |
| 页面背景 | `Background` | `#FFFFFF` | `#0D0D14` | 品牌白；夜色 = 黑底向品牌紫抬升 ~8% |
| 侧栏 | `Sidebar` | `#F9FAFB` | `#13131C` | ADR 0001 §8.2 品牌中性色 |
| 卡片/面板/输入框 | `Surface` | `#FFFFFF` | `#1B1B26` | 靠分隔线区分层级 |
| 强表面（主按钮） | `SurfaceStrong` | `#F1F3F5` | `#272736` | ADR 0001 §8.2 品牌中性色 |
| 悬停/选中行 | `Hover` | 蓝 `#0D44F6` 6% | 冰青 `#37E7FE` 10% | 品牌色低透明度染色 |
| 按下填充 | `SurfacePressed` | 蓝 10% | 冰青 16% | 同上 |
| 主文字 | `Text` | `#0F1115` | `#F9FAFB` | 字标墨色，对比度 18.9:1 / 18.5:1 |
| 次文字 | `TextSecondary` | `#5B6373` | `#C7CAD9` | 墨色向品牌蓝微调，6.0:1 / 11.9:1 |
| 三级文字 | `TextTertiary` | `#7E8796` | `#9AA0B4` | 标签级文字，3.6:1 / 7.4:1 |
| 常规分隔线 | `Border` | 墨色 10% | 白 12% | |
| 强分隔线 | `BorderStrong` | 墨色 16% | 白 18% | |

`Tokens` 字段映射：`BgBase←Background`、`BgSidebar←Sidebar`、`BgRaised←Surface`、`BgStrong←SurfaceStrong`、`BgHover←Hover`、`LabelPrimary/Secondary/Tertiary←Text/TextSecondary/TextTertiary`、`BorderL2/L3←Border/BorderStrong`、`White←#FFFFFF`、`Scrim()←Scrim`。

### 4.2 强调色、选区、焦点与遮罩

| 语义角色 | Palette 字段 | Light | Dark | 依据 |
| --- | --- | --- | --- | --- |
| 强调色 | `Accent` | `#0D44F6` | `#3CBBFA` | 品牌主蓝 / 品牌天空蓝 |
| 强调悬停 | `AccentHover` | `#0946EF` | `#2ECCFB` | logo 相邻 stop |
| 强调按下 | `AccentPressed` | `#0B38CA` | `#37E7FE` | 浅色压暗 18%；深色用更亮冰青靠亮度反馈 |
| 强调填充上的文字 | `AccentText` | `#FFFFFF` | `#0F1115` | 对比度 6.6:1 / 8.7:1 |
| 主按钮文字 | `OnPrimary` | `#FFFFFF` | `#0F1115` | 为后续强调色实心按钮预留 |
| 文本选区 | `Selection` | 主蓝 22% | 天空蓝 30% | 半透明，文字仍可读 |
| 焦点环 | `Focus` | 主蓝 55% | 冰青 70% | 半透明环 |
| 滚动条滑块 | `Scrollbar` | 墨色 30% | 白 35% | |
| 模态遮罩 | `Scrim` | 墨色 30% | 黑 55% | `design.Tokens.Scrim()` 随之改为品牌值 |

### 4.3 状态色（语义色，非 logo 派生）

`Success` / `Danger` / `Warning` 是通用语义色，刻意不取自 logo 色板；`WarningText` 是 `Warning` 的 WCAG-AA 文字伴生色（状态色本身对白底小字太浅）：

| 字段 | Light | Dark | 说明 |
| --- | --- | --- | --- |
| `Success` | `#16A34A` | `#22C55E` | 成功/通过 |
| `Danger` | `#DC2626` | `#F87171` | 错误/危险（浅色 4.8:1） |
| `Warning` | `#D97706` | `#F59E0B` | 徽标/状态点 |
| `WarningText` | `#8A4B0F` | `#E9A24C` | 警告文字，6.8:1 / 9.0:1 |

### 4.4 ui.Theme 字段与度量

`brand.LightTheme()/DarkTheme()` 从 `ui.LightTheme()/DarkTheme()` 出发，覆盖：

- **颜色槽**：`Background`、`Surface`、`SurfaceHover←Hover`、`SurfacePressed`、`Border`、`Text`、`TextMuted←TextSecondary`、`Accent`、`AccentHover`、`AccentPressed`、`AccentText`、`Danger`、`Warning`、`Success`、`Selection`、`Focus`、`Scrollbar`；
- **度量**：`Radius=10`、`Spacing=4`、`FontSize=14`、`Font=SystemFont`；
- **有意不覆盖**：`Dark`（由 mygo 基础主题给出）、`Inverse/InverseText`（留空，mygo 文档规定回退到 `Text/Background`，品牌下即深色 tooltip/toast）、`ScrollbarWidth`（沿用 mygo 默认 6 DIP）。

度量取值理由：

| 度量 | 值 | 理由 |
| --- | --- | --- |
| `Radius` | 10 | 品牌圆角（介于 mygo 默认 6 与网页式 12–16 之间，桌面控件更柔和）；`RadiusMD` 已同步为 10，与 stock 控件一致 |
| `Spacing` | 4 | 4 DIP 网格，与现有 `Space1..8` 及 mygo 默认一致 |
| `FontSize` | 14 | 固定跨平台桌面密度；mygo 默认在 macOS 为 13、其他平台 14，品牌统一为 14 |
| `Font` | 系统栈 | 字标规格（`docs/wordmark.md`）明确不打包字体；中英文 CJK 回退在同一列表内 |

## 5. 代码结构与 API 兼容

### 5.1 新增/修改文件

| 文件 | 变化 |
| --- | --- |
| `internal/brand/palette.go`（新，~200 行） | 品牌色板常量（含 logo 行号来源）、`Palette`、`LightPalette/DarkPalette/PaletteOf` |
| `internal/brand/brand.go`（新，~105 行） | `Radius/Spacing/FontSize`、`SystemFont/MonoFont`、`LightTheme/DarkTheme/ThemeFor`、theme 应用函数 |
| `internal/brand/brand_test.go`（新，~200 行） | 色板与 logo/wordmark 资产互证、theme 映射、WCAG 对比度、返回值独立性 |
| `internal/design/tokens.go` | 品牌化重写：包注释、`TypeRole/TypeMetrics`、`Tokens` 字段注释、`Light/Dark←brand.Palette`、`Theme()→brand theme`、`Scrim()` |
| `internal/design/font.go` | `FontStack/MonoFontStack` 委托 `brand.SystemFont/MonoFont`；注释改品牌口径；API 不变 |
| `internal/design/style.go` | 注释改品牌口径（值不变） |
| `internal/design/components.go` | 注释改品牌口径；`Space1..8←brand.Spacing` 网格；`RadiusMD←brand.Radius`；其余几何保留 |
| `internal/design/motion.go` | 注释全部重写为通用动效令牌（去 DSH 选择器/实测口径）；删除 3 个零引用导出；其余值/API 不变 |
| `internal/design/tokens_test.go`、`font_test.go` | 基线数值断言改为品牌断言（色板映射、theme=品牌主题、品牌网格、字体栈=品牌栈） |

### 5.2 API 兼容性（关键约束）

- 改造前后导出符号 **113 → 110**：仅移除 `AnimTick`、`AnimSlow`、`AnimBase`（包外零引用，grep 验证；`.probe/` 为 Go 工具链忽略的副本，不计）；**无新增导出、无签名变更、无字段增删**。
- `internal/shell` 对 `design.Tokens` 的引用（本轮开始时 278 处）以及 `design.Space*`/`Radius*`/`Type*`/`Apply`/动效 helper 全部保持可编译；本轮未改 shell 任何文件。
- `design.Tokens.Theme()` 语义收紧：返回品牌主题（`brand.LightTheme()/DarkTheme()`），不再从 token 字段二次拼装；`c.SetTheme(t.Theme())` 调用点无需改动。

## 6. 与 DSH 的取舍

**去掉的（本次）**

1. `internal/design` 中所有「数值来自 DSH 基线」的注释与 `baseline/css`、`docs/baseline` 路径引用（全包 grep 只剩 `tokens.go` 包注释里一行「历史已退役」说明）。
2. `dshTheme` 式「18 色覆盖 mygo 默认主题」的思路：品牌语义色板集中在 `internal/brand`，`design` 只做语义映射。
3. 研究型、包外零引用的动效导出（3 个），降低动效层的维护面。
4. 令牌数值不再有任何「与 DSH 对齐」的验收含义（像素门禁已由 ADR 0001 删除）。

**有意保留的（本轮）**

1. **既有布局几何与视觉密度**（280px 侧栏、868px composer、字号字阶、动效时长等）：避免逐屏视觉回归，也避免与 Lead 并行进行的 shell 静态五屏拆除冲突。这些值现在被重新定义为**品牌设计系统的自有值**，不再声称来自 DSH；原生布局阶段再统一评估/调整。
2. **`design` 的样式原子与组件 helper**：仍是 `ui.Element` 的薄糖，逐屏替换原生控件时按屏删除，而不是一次重写。
3. **状态色语义**：Success/Danger/Warning 保持通用语义与 AA 文字伴生色，不强行品牌化。

**明确不在本轮**

- 不改 `internal/shell` 的视觉结构与交互；
- 不做逐屏原生控件替换（mygo Button/Input/List/Sidebar 等）；
- 不重新设计布局/密度/响应式；
- 不引入品牌字体或图标重绘（图标仍走 `internal/icons` + 现有 SVG 资产）。

## 7. 验证（2026-11-05，本 ADR 实施时）

```sh
export PATH=/usr/local/go/bin:$PATH GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0

gofmt -l internal/brand internal/design      # 输出为空（干净）
go build ./internal/brand ./internal/design  # 通过
go test ./internal/brand ./internal/design   # 全部 PASS
go build ./...                               # 全仓通过（shell 并行重构进行中，以此状态为准）
grep -rn 'baseline\|DSH' internal/design     # 仅 tokens.go 历史说明一行
```

测试覆盖：

- `internal/brand`：色板常量与 `logo/logo.svg`、`logo/logo-dark.svg`、`docs/wordmark.md` 互证；theme 槽位/度量/字体映射；返回值互不污染；WCAG 4.5:1 关键对比度（正文、次文字、警告文字、强调填充上的文字）；`ThemeFor/PaletteOf` 一致性。
- `internal/design`：`Tokens` 来源色板断言（含明暗 Accent 区分）；`Scrim` 品牌值；`Tokens.Theme()` 与 `brand.*Theme()` 深度相等及 radius 一致性；`Of(c)` 跟随 context 主题；字阶（含 `TypeBody == brand.FontSize`）；间距/圆角品牌网格；字体栈 = 品牌系统栈且可实际渲染。

## 8. 后续（原生 UI 迁移下一阶段）

1. **原生控件替换**：从 `markdown`、侧栏、列表开始，用 mygo 原生控件替换手搓控件；每替换一屏，删除对应的 `design` 样式/组件 helper。
2. **自有布局**：几何、密度、响应式统一到 `Theme.Space/Radius/Rem`；评估 280px 侧栏、868px composer 等保留几何是否仍成立。
3. **交互本地化**：系统菜单栏、系统通知、消息文本选择、DPI/TextScale/高对比、窗口状态持久化（优先于视觉打磨，见 00-report §7 Step 1）。
4. **品牌资产上屏**：图形标按主题切换（`logo/logo.svg` / `logo/logo-dark.svg`），字标严格按 `docs/wordmark.md`；图标沿用 `internal/icons`。
5. **主题打磨**：深色/浅色切换、跟随系统、焦点/悬停/禁用态在原生控件上的品牌化；继续用 `brand_test` 的 WCAG 断言做回归。
6. **待拍板**：品牌主色最终值（当前单蓝 `#0D44F6`/`#3CBBFA`）、渐变是否用于更多装饰、是否引入品牌强调字体（当前明确不打包字体）。

**回滚**：`git checkout pre-native-ui` 可回到迁移前状态；本轮的 `internal/brand` 为纯新增，`internal/design` 的改动可按文件单独回退，不涉及数据/运行时迁移。
