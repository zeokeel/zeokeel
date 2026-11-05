# ZeoKeel Agent 总体规划

> **ZeoKeel · 纯 Go · mygo 原生 UI · eino Agent 运行时 · 与 DSH 1:1 对齐（像素/交互/功能）**
>
> 文档版本：v0.13（2026-11-04） · 状态：**M0–M10：多模态图片输入 + 终端 PTY 完成** · 基线：**DSH 0.2.1-rc.2**（官方发版后同步；参考窗口 1600×1000） · 品牌：**ZeoKeel**（logo 只使用 `logo/` 下的资产，不允许猜测或重绘）
>
> 本文档不依赖任何既有项目代码或既有设计文档。所有技术结论来自三处可复核证据：
> ① `github.com/egoist/mygo@v0.7.4` 与其 `ui`、`ui/markdown`、`ui/editor`、`ui/i18n` 包源码；
> ② `github.com/cloudwego/eino@v0.10.0-alpha.35` 及其 `adk`、`compose`、`components` 源码；
> ③ 本机 DSH 前端构建产物（设计令牌、字体）与逐屏基线截图（`baseline/screenshots/`，控制项清单 `baseline/controls/`）；
> ④ DSH 官方源码（只读，用于确认按钮行为与边界，不复制代码）。

---

## 0. 一页结论

**结论：路线成立，但要换一个心智模型——这不是"做一个聊天框"，而是"用纯 Go 重建一个 Agent 宿主"。**

1. **约束可满足**：mygo（purego，无 cgo）、eino（纯 Go）、`modernc.org/sqlite`（纯 Go）可以在 `CGO_ENABLED=0` 下单进程共存，产出单二进制；mygo 的 `ui.Render(view, w, h, scale)` 与 `ui.NewTester` 能把"像素对齐"变成**无头、确定性、可进 CI 的单元测试**，这是本项目最重要的可行性红利。此外，DSH 的“基础 CSS”会全部函数化为语义样式层（§5.4）：所有界面共用同一批 style 函数，视图层不写样式字面量。
2. **真正的成本在 Agent 运行时与对齐验收体系，不在画界面**：UI 约占总工作量的 1/3；运行时（会话、工具、审批、恢复、子智能体）约 1/2；像素/交互/功能三套对齐门禁约 1/6。功能对齐的难点是"运行时 + 插件形态"，不是按钮画得像不像。
3. **"1:1"必须工程化定义，否则永远做不完**：三条轴各自有度量、阈值与门禁（见 §1.2）；先锁 P0（宿主壳 + 会话 + 工具/审批），再按矩阵推进 P1/P2。**不建议一上来追求全功能 1:1**——那是 9 个月以上的单人体量。
4. **纯 Go 带来的两个硬性功能边界**，必须提前拍板：
   - **插件只做内置**：对齐 DSH 的 Built-in / Experimental 插件（开关 + 配置页），但**不做第三方插件安装、上传与插件市场**。插件在编译期以注册表形式存在；这既保留"插件"这一产品概念，也避免 Node/Cordis 依赖。
   - **没有内核级沙箱**。DSH 的权限预设/审批可以对齐交互，但底层隔离只能做到"策略 + 审批 + 审计"（纯 Go 可用 `os/exec` + 路径策略），不承诺与系统沙箱同等级别的隔离。这也是诚实的产品边界。
5. **估时**：2 名 Go 工程师，约 **22 周（5.5 个月）**到 v1.0；1 人约 9–10 个月。里程碑见 §11。
6. **已锁定的决策**（2026-11-04）：基线 = DSH 0.2.1-rc.2，官方发版后同步；品牌 = ZeoKeel（logo 用 `logo/` 资产）；插件 = 只做内置插件；允许只读官方源码确认行为；采集时可以建测试会话并少量对话。
7. **施工前置：基线文档必须 100% 覆盖**。每个界面、每个按钮都要有截图 + 行为说明 + 边界条件，全部落在 `docs/baseline/`；未覆盖的界面不进入编码（§2.7）。这也是当前第一优先级。

---

## 1. 目标、范围与"1:1"的工程定义

### 1.1 产品目标

用**纯 Go** 构建一个桌面 AI Agent：视觉、交互、能力三方面与 DSH 对齐，单二进制、离线优先、可三平台分发。

| 维度 | 目标 |
| --- | --- |
| 形态 | 原生桌面应用（mygo 原生 `ui`，非 webview、非 Electron） |
| 语言/构建 | 100% Go；`CGO_ENABLED=0`；单二进制；`go test` 无显示器可跑 |
| 产品能力 | 会话、工作区、Agent 工具调用、审批、轨迹、用量、模型选择、插件页、自动化任务、设置 |
| 对齐基准 | DSH 当前 Web 客户端：布局/令牌/交互/功能逐项对齐 |
| 交付物 | Linux/macOS/Windows 可执行 + 安装包 + 自动更新（P2） |

### 1.2 三条对齐轴：定义、度量、门禁

| 轴 | 对齐对象 | 度量方法 | 通过门槛（P0 屏） | 不达标处理 |
| --- | --- | --- | --- | --- |
| **像素对齐** | 布局几何、设计令牌、字体、图标、圆角/阴影/边框、明暗主题 | ① 设计令牌逐项比对（机器）；② 无头渲染 `ui.Render` 与基线截图做 ROI 差异；③ 尺寸/坐标断言 | 几何偏差 ≤ 1px；颜色 ΔE ≤ 1（或同值）；非文本区像素差异 ≤ 0.2%/屏；文本区 SSIM ≥ 0.98 | 进白名单必须写明原因 + 到期日；不接受"整体看起来差不多" |
| **交互对齐** | 状态机、键位、焦点顺序、动效时长、异步反馈、错误反馈 | 交互用例库（§6.9）在 `ui.NewTester` 虚拟时钟上逐条执行；关键动效断言 ±50ms | 用例 100% 通过；同一操作在基准与实现上的状态转移一致 | 用例降级必须改需求单，不允许静默跳过 |
| **功能对齐** | 能力矩阵（§7）中的每一条 | 每条能力一个验收用例（成功 + 失败 + 边界） | P0 行 100%；P1 行 ≥ 90% 且余项有排期；P2 行有明确降级说明 | 写进"功能降级清单"并在产品内可见 |

**三条轴的共同前提：基准版本必须锁死。** 建议在仓库里提交 `baseline/VERSION`（如 `dsh 0.2.1-rc.2`）与基线截图集；DSH 升级时走 §2.6 的基线更新流程，否则"1:1"没有判据。

### 1.3 对齐分层（先做对 20% 的屏，再扩）

| 层级 | 范围 | 验收强度 |
| --- | --- | --- |
| **P0** | 宿主壳（侧栏 + 首页 composer）、会话页（Chat/Trajectory 头、消息流、composer）、工具调用卡片、审批、停止/流式、用量条 | 像素 + 交互 + 功能三轴全门禁 |
| **P1** | 工作区/会话列表管理、模型与权限选择、设置页、插件页、自动化任务页、Markdown/代码/差异渲染、深色主题、i18n | 像素门禁（ROI 放宽）+ 交互门禁用例 |
| **P2** | 目标、定时、子智能体、Agent Team、skills、MCP、后台任务、工作流 | 功能门禁 + 交互门禁（视觉只要求组件级一致） |
| **P3** | 语音输入、Office/PDF 预览、CoT 翻译、检查器等等官方实验能力 | 只要求"入口与文案对齐"，实现可延后 |

### 1.4 明确不做（非目标，写入产品口径）

1. **不做第三方插件**：不运行 DSH 的 TS/Cordis 插件，不做插件安装/上传/市场；只实现官方内置插件（Built-in / Experimental）与它们的开关、配置页。不引入 Node 运行时，不用 webview 承载主界面。
2. **不做数据格式互通**（不承诺读写 DSH 的会话库/配置）；只做"行为对齐"。导出/导入 Markdown/JSON 属于可选增强。
3. **不承诺内核级沙箱**（见 §0-4）；不承诺与系统级权限模型（如 macOS TCC 全能力）逐项一致。
4. **不做多租户/云同步**；单机、单用户、本地优先。
5. **不引入任何 cgo 依赖**，包括"性能更好的 SQLite/图形库"这类理由。
6. **不复制 DSH 的代码与资源**：令牌用测量/提取值重写，图标用等价图形重绘，字体用 OFL 字体替代（§2.5）。官方源码只用于确认行为，不搬运实现。
7. **不做第三方的插件生态**（见第 1 条）；也不做插件热加载/动态加载，插件集合编译期固定。

---

## 2. 基线：DSH 界面与设计令牌

### 2.1 基线画面

| 画面 | 说明 | 基线尺寸 |
| --- | --- | --- |
| Home | 侧栏 + 居中标题 + composer | 1600×1000 |
| Session | 侧栏 + 会话头（标题、Agent Team、模式、后台任务、右侧动作）+ Chat/Trajectory + 消息流 + composer + 用量行 | 1600×1000 |
| Plugins | 侧栏入口 → 插件列表页（Built-in/Experimental、开关、Add plugin） | 1600×1000 |
| Settings / Automation | 设置分组与自动化任务列表 | 1600×1000 |

**基线截图必须先落库**：`baseline/<screen>@1600x1000.png`，并附 `geometry.json`（每个关键盒子的 x/y/w/h）。当前仓库的 `baseline/` 已建立，M0 第一件事就是补齐（DSH Web 需要带鉴权 token 的 URL 才能打开，采集脚本见附录 B）。

### 2.2 设计令牌体系（已从 DSH 前端产物提取，M0 生成 Go 常量）

DSH 的令牌分三层：`static`（原始色板）→ `alias`（语义）→ 组件。我们**原样移植这三层**，不做"我觉得更好看"的改动。

**字号/行高（已确认）**

| Token | 字号 | 行高 | 典型用途 |
| --- | --- | --- | --- |
| `font-xl-24` | 24px | 32px | 首页大标题 |
| `font-l-20` | 20px | 28px | 页面标题 |
| `font-m-18` | 16px | 28px | 区块标题（注意：该 token 名为 18，实际字号 16） |
| `font-base-16` | 16px | 24px | 强调正文 |
| `font-s-14` | 14px | 22px | 正文（默认） |
| `font-xs-13` | 13px | 20px | 次要正文 |
| `font-xxs-12` | 12px | 18px | 辅助信息 |
| `font-xxxs-11` | 11px | 14px | 极小标签 |

**圆角与阴影（已确认）**

| Token | 值 |
| --- | --- |
| `radius-xs / sm / md / lg / xl / panel` | 4 / 8 / 12 / 16 / 20 / 28 px |
| `shadow-lv1` | `0 2px 4px 0 #0000000D` |
| `shadow-lv2` | `0 4px 12px 0 #00000005, 0 2px 8px 0 #0000000A` |
| `shadow-lv3` | `0 0 1px 0 #0003, 0 0 4px 0 #00000005, 0 12px 32px 0 #00000014` |

**关键语义色（浅色主题，已确认）**

| 语义 | 值 | 用途 |
| --- | --- | --- |
| `bg-base` | `#FFFFFF` | 主区背景 |
| `bg-sidebar`（实测） | `#F9FAFB` | 侧栏背景 |
| `label-primary` | `#0F1115` | 主文字 |
| `label-secondary` | `#61666B` | 次文字 |
| `label-tertiary` | `#81858C` | 辅助文字/占位 |
| `border-l2` | `#0000001A` | 常规分隔线 |
| `border-l3` | `#0000001F` | 强分隔线 |
| `interactive-bg-hover` | `#2631480F` | 行/按钮 hover |
| `blue-500` / `blue-600` | `#3B82F6` / `#2563EB` | 强调、发送按钮、链接 |
| `green-500` / `red-500` / `amber-500` | `#22C55E` / `#EF4444` / `#F59E0B` | 状态 |

> 完整色板（约 90 个 static token）与 alias 映射在 M0 用脚本导出为 `internal/design/tokens.json`，再由 `go generate` 生成 `tokens_gen.go`；生成物有单测：**Go 常量必须与基线 JSON 完全一致**，禁止手改。

### 2.3 布局几何（初步测量，M0 复核到 1px）

| 测量项 | 基线值 | 说明 |
| --- | --- | --- |
| 窗口参考尺寸 | 1600×1000 | 所有 P0 基线以此为准 |
| 左侧栏宽 | **280px**（0–279，279 为 1px 边框） | 背景 `#F9FAFB`，右边框约 `#0000001F` |
| 主区 | 280–1600，白底 | 无二级侧栏时主区全宽 |
| Home composer | x=502 起步，约 **868×116** | 圆角约 16–20px，边框 `#EAEAEA` 级 |
| New Session 按钮 | 约 248×38，圆角 8 | 侧栏顶部 24px 区 |
| 会话头 | 约 **75px** 高 | 标题行 + Chat/Trajectory 行 + 底部分隔线 |
| 会话内容列 | 与 composer 同列宽（约 868） | 居中，左右留白由窗口宽度分配 |
| 侧栏分组标题 | 11–12px，`#81858C` | Workspaces / 项目名 |

> 这些数字是 1600×1000 下的实测初值；M0 用 `geometry.json` 固化，任何改动都要走基线更新。

### 2.4 令牌的机器化移植（关键工程手段）

```
DSH 前端产物 ──(内置提取脚本)──> baseline/tokens.json ──(go generate)──> internal/design/tokens_gen.go
                                                            │
                                          单测：tokens_gen.go == baseline/tokens.json
```

- `internal/design` 只暴露**语义令牌**（`ColorLabelPrimary`、`Space4`、`RadiusMD`、`TextBody` …）；令牌只被 §5.4 的样式函数层消费，屏代码通过 style 函数访问，禁止直接写魔法值。
- 主题切换（浅/深）用同一套 alias 表 + 两套 static 值，`tt.SetDark` 可无头验证。
- 间距用 4px 栅格（4/8/12/16/20/24/32），但**以基线实测为准**，不硬套栅格。

### 2.5 字体策略（像素对齐的最大变量）

| 用途 | DSH 基线 | 我们的做法 |
| --- | --- | --- |
| 正文/UI | `-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Helvetica, Arial, sans-serif` | **同一字体栈**按平台解析；mygo 的 `Font(family)` 按平台映射到同一族 |
| 品牌字 | Montserrat 300/400/500（OFL） | 打包 Montserrat TTF，`ui.RegisterFont(data, "Montserrat")`；用于 wordmark |
| 代码 | `"SF Mono", "JetBrains Mono", "Fira Code", Consolas, "Liberation Mono", Menlo, Courier, "PingFang SC", "Microsoft YaHei"` | 同栈；Linux 无 SF Mono 时回退 JetBrains Mono（打包 OFL 版本），差异进白名单 |

**风险与对策**：Chromium 的文本整形/抗锯齿与 mygo 的系统文本引擎不同，**文本区不可能逐像素相同**。因此像素门禁对文本区采用 SSIM/感知阈值，对几何（盒子位置、行高、断行位置）采用 1px 断言；如果断行不一致，优先修字体族/字号/字距，而不是改布局。

### 2.6 基线更新流程

1. 锁定 DSH 版本（`baseline/VERSION`）→ 2. 重新截图与测量 → 3. 更新 `tokens.json`/`geometry.json` → 4. 重新生成常量 → 5. 跑像素门禁，差异逐条解释 → 6. 提交基线 commit 并记录变更说明。**基线更新是一个独立 PR，不夹在功能 PR 里。**

---

### 2.7 基线文档体系（施工前置，当前第一优先级）

**规则：一个界面没有"截图 + 控件清单 + 行为说明"，就不进入编码。** 文档不是附属品，是施工图。

#### 2.7.1 目录与产物

```
baseline/
├── VERSION                     # dsh 0.2.1-rc.2 + 采集时间
├── screenshots/                # 逐屏、逐状态截图（1600×1000）
├── controls/                   # 每屏的交互控件 dump（json：标签/位置/尺寸/禁用态）
└── tokens.json / geometry.json
docs/baseline/
├── index.md                    # 屏幕地图：全部入口、路由、状态
├── controls.md                 # 控件总表（ID/所属屏/名称/类型/行为/状态/截图/确认日期）
├── states.md                   # 全局状态：空/加载/错误/离线/流式/审批/权限
├── coverage.md                 # 覆盖矩阵（界面 × 控件 × 截图 × 文档 × 验收）
└── screens/
    └── NN-<screen>.md          # 每屏：目的、布局、区域、控件表、状态、边界、截图引用
```

#### 2.7.2 每屏文档模板（必须填满）

```md
# <屏名>
- 入口/路由：<从哪进、URL 或点击路径>
- 目的：<一句话>
- 截图：<empty/有内容/加载/错误 各一张>
- 布局：<区域树 + 关键尺寸（来自 geometry.json）>
- 控件表：| ID | 名称/文案 | 类型 | 位置 | 可用条件 | 点击行为 | 结果状态 | 快捷键 | 截图 |
- 状态：<该屏可能出现的全部状态与切换条件>
- 边界：<空数据、权限不足、超长文本、离线、错误>
- 待确认：<仅允许暂时存在，且有 owner/日期>
```

#### 2.7.3 采集方式（已跑通）

- 探测实例：`DSH_HOME=/data/mygo/.probe/home`（127.0.0.1:8099），已复制 workspace 与会话投影缓存；采集不影响用户实例。
- 冷启动先点 **Preview Notice → Continue**（坐标 1010,684）；**打开会话点侧栏标题文字（x≈85）**，点行尾空白不会进入会话。
- `scripts/cdp.mjs`：无头 Chromium + CDP，用 `~/.dsh/.credentials.yaml` 里的 browser-session 秘密生成会话 cookie，逐屏导航、点击、截图、dump 控件。
- **滚动扫描**：`scrollscan` 自动找滚动容器（含弹窗内），逐屏滚动并按步截图 + dump，确保长列表/长会话的所有元素被发现。采集脚本在 `scripts/capture/*.json`。
- **行为探测**：`probescan` 逐个点击控件，记录点击前后（URL、弹窗数、页面文本、控件集合）变化，写入 `baseline/behavior/*.jsonl`。危险/破坏性控件与用户配置开关先跳过，确认后再测。
- 每次采集产出：`baseline/screenshots/NN-name.png` + `baseline/controls/NN-name.json`（控件标签、坐标、尺寸、禁用态、页面文本）。
- 行为确认：点击前后各截一张图 + dump 一次；对照 DSH 官方源码确认边界条件。
- 版本锁定：采集前写 `baseline/VERSION`；官方发版后重跑同一套序列，diff 出变化。

#### 2.7.4 全元素 CSS 提取（新增，已跑通）

- `scripts/cdp.mjs cssscan`：对每个屏/状态逐元素读取 **computed style**（43 个属性：颜色、字体栈/字号/字重/行高、边框、圆角、阴影、padding/margin/gap、display/flex、过渡等），原始数据写入 `baseline/css/<screen>.json`。
- 汇总为 [docs/baseline/css.md](docs/baseline/css.md) + `baseline/css/summary.json`：唯一值频次表 + 组件样式样本（按钮/Tab/设置行/插件行等）。
- 已覆盖 17 个屏/状态（含会话深色、窄屏、各弹层），共数万条元素样式样本。
- **用法**：`internal/design` 的样式函数按此表实现，禁止凭感觉取值；令牌与样式函数改动必须能以 CSS 表为证据。
- 已确认路线（A）：**CSS 只作为对齐依据**，运行时是 Go 样式函数，不引入 CSS 解析器、不用 webview 承载界面。

#### 2.7.5 覆盖门禁

- P0/P1 屏：控件覆盖率 100%（每个可交互元素都有 ID、行为、截图）。
- 每个 P0 屏至少 4 张截图：空态、有内容、加载/流式、错误态（能构造的话）。
- 未覆盖项必须登记在 `coverage.md`，带原因与排期；不允许"看起来差不多"。

### 2.8 品牌替换：ZeoKeel（logo 以资产为准）

- **资产唯一来源**：`logo/logo.svg`（浅色）与 `logo/logo-dark.svg`（深色）。**不允许自行猜测、重绘或生成 logo**；任何尺寸从 SVG 派生（mygo 用 `ui.ParseSVG` / 位图资源）。
- **品牌出现的位置**（逐项对齐并替换）：
  | 位置 | DSH | ZeoKeel |
  | --- | --- | --- |
  | 侧栏左上 wordmark | deepseek HARNESS | 图形标 + “ZeoKeel”文字（**已选方案 A**；系统字体栈、字重 600、17px、间隔 8px；规格见 `docs/wordmark.md`） |
  | 窗口标题 / 任务栏 | DSH | ZeoKeel |
  | 安装包 / 应用名 / About | DeepSeek Harness | ZeoKeel |
  | 启动闪屏 / 欢迎页 | DSH wordmark | ZeoKeel logo + 字标 |
  | 设置›关于、更新器 | DSH | ZeoKeel |
  | 对话框/错误页里的产品名 | DSH | ZeoKeel |
- **不替换**：官方协议/数据目录名（若需保留兼容）由 ADR 决定；第三方 API 名称（DeepSeek 模型名等）保持原样。
- **验收**：品牌位置逐项截图对照（同为 1600×1000），确认无残留 "deepseek"/"DeepSeek Harness" 字样（模型名除外）。

---

## 3. 硬约束与技术选型

### 3.1 硬约束（每一条都进 CI）

| # | 约束 | 守卫方式 |
| --- | --- | --- |
| C1 | 纯 Go，`CGO_ENABLED=0` | 构建脚本 + `go list -deps` 检查 `CgoFiles` 为空 + 交叉编译 |
| C2 | UI 只用 mygo 原生控件 | 架构评审：`internal/ui` 之外禁止引入其他 UI 库；无 webview/HTML |
| C3 | 单二进制、单进程 | 构建产物检查；运行期无子进程托管（工具 exec 除外） |
| C4 | 三平台可构建 | CI 矩阵：linux/amd64+arm64、darwin/arm64、windows/amd64 |
| C5 | 测试无显示器可跑 | `go test ./...` 在无 `DISPLAY` 下全绿；像素/交互测试走 `ui.Render`/`NewTester` |
| C6 | 版本钉死 | `go.mod` 固定 mygo/eino 版本；`toolchain go1.27.3` |
| C7 | 无 cgo 依赖白名单外的新依赖 | CI 扫描 `go.mod` + `CgoFiles`；新增依赖需 ADR |

### 3.2 依赖白名单（全部纯 Go）

| 依赖 | 版本（已验证可用） | 用途 |
| --- | --- | --- |
| `github.com/egoist/mygo` | v0.7.4 | 原生 UI、窗口、托盘、自动化、i18n、打包 |
| `github.com/cloudwego/eino` | v0.10.0-alpha.35 | Agent 编排（ADK、图、checkpoint、中断、middlewares） |
| `github.com/cloudwego/eino-ext/components/model/openai` | v0.1.13 | OpenAI 兼容模型（DeepSeek 亦走此通道） |
| `modernc.org/sqlite` | v1.61.1 | 嵌入式 SQLite（纯 Go） |
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | MCP 客户端 |
| `gopkg.in/yaml.v3` | 最新纯 Go 版 | 配置 |
| 标准库 | — | HTTP/SSE、JSON、PNG、exec、crypto 等 |

> mygo 与 eino 的版本在 M0 锁死；升级走独立 PR + 全量门禁。`go1.27.3` 是 mygo 要求的最低版本，本机已具备（`/usr/local/go/bin/go`），注意 PATH。

### 3.3 no-cgo 门禁（可直接落地的脚本）

```sh
# 1) 构建
CGO_ENABLED=0 go build ./...

# 2) 依赖里不能有 cgo 包
out=$(CGO_ENABLED=0 go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./...)
[ -z "$out" ] || { echo "cgo deps: $out"; exit 1; }

# 3) 六目标交叉编译
for t in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do
  GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 go build ./cmd/mygo-agent || exit 1
done
```

### 3.4 构建与分发

- **开发**：`go run ./cmd/mygo-agent`；本机无显示器时 `xvfb-run -a`。
- **打包**：`go tool mygo build`（mygo 自带三平台打包、图标、签名、自动更新能力）；产物为单文件 + 安装包。
- **Linux 产物注意**：purego 的 fakecgo 仍动态链接 glibc，**不能放进 Alpine/scratch**；发布用 glibc 基础镜像或普通发行版。
- **体积预算**：stripped 目标 ≤ 20MB（mygo 空壳约 9.5MB，加上 editor/markdown/sqlite 预计 13–18MB）。

---

## 4. 总体架构

### 4.1 进程与线程纪律（最重要的一条）

```
┌───────────────────────────── 单进程 · 单二进制 ─────────────────────────────┐
│  mygo UI 主线程                                                             │
│    view(state) 每帧重跑 ── 只读不可变快照（atomic.Pointer[Snapshot]）        │
│         ▲  Invalidate()                                                     │
│         │                                                                   │
│  投影层 projection（把运行时事件折叠成 UI 快照）                             │
│         ▲  事件总线（chan Event，单消费者）                                  │
│         │                                                                   │
│  运行时 runtime（goroutine 池）                                             │
│    eino ADK Runner / 工具执行 / 审批挂起 / 子智能体                         │
│         │                                                                   │
│  存储 store（SQLite 单写者 + JSONL 追加）                                    │
└─────────────────────────────────────────────────────────────────────────────┘
```

**规则**：后台 goroutine **绝不直接改 UI 状态**，只能"产出不可变快照 → 原子换指针 → `Window.Invalidate()`"；UI 层绝不阻塞等待 DB/模型/子进程（异步命令队列 + 进行中状态）。这条守住，mygo 的性能与测试模型就是朋友；守不住会得到随机卡死。

### 4.2 分层与依赖方向

```
cmd/mygo-agent
   └─ internal/app        组装：配置、store、runtime、UI、托盘、单实例
        ├─ internal/ui      纯视图：view(snapshot) 函数 + 主题 + 组件
        ├─ internal/view    快照类型、投影、动作（Action）定义
        ├─ internal/runtime eino 图、会话、审批、工具调度、事件
        │     ├─ internal/tools     工具实现（fs/bash/web/...）
        │     ├─ internal/provider  模型 provider（SSE 流式）
        │     ├─ internal/prompt    系统提示词、工具描述、模板
        │     └─ internal/ext       skills / MCP / 计划 / 目标 / 定时
        ├─ internal/store   SQLite + JSONL + 迁移
        └─ internal/design  设计令牌（生成）+ 基础样式函数层（§5.4）
```

依赖只能向下：`ui → view → runtime → {tools, provider, store}`；`design` 与 `platform` 被所有层依赖。禁止 `runtime` 反向依赖 `ui`。

### 4.3 一次对话的端到端时序

```
输入 → Composer action → app.Handle → runtime.Submit(sessionID, msg)
  → 持久化用户消息（JSONL append + SQLite index）
  → eino ADK Runner（ChatModelAgent，流式）
      → 模型 token 流 → 事件 token
      → 工具调用 → 审批（可能 interrupt 挂起）→ 执行 → 事件 tool_start/tool_result
      → usage 事件（tokens/cost/cache）
  → 投影层折叠事件 → 快照 → Invalidate → view 重绘
  → 回合结束 → 持久化 assistant 消息 + 用量 → 快照 idle
```

### 4.4 事件总线（流式协议的唯一定义）

```go
type Event interface{ isEvent() }

type TokenEvent struct{ SessionID, MsgID, Text string }
type ReasoningEvent struct{ SessionID, MsgID, Text string }
type ToolStartEvent struct{ CallID, Name string; Args json.RawMessage }
type ToolEndEvent struct{ CallID string; Result string; IsError bool; Duration time.Duration }
type ApprovalRequestEvent struct{ CallID, Name string; Args json.RawMessage; Kind ApprovalKind }
type UsageEvent struct{ Turns, Steps int; In, Out, CacheHit int; ContextPct float64 }
type StatusEvent struct{ Phase Phase; Subagents, Jobs int }
type ErrorEvent struct{ MsgID string; Err error }
```

- 总线是**有界队列 + 单消费者**；投影层是唯一消费者，保证 UI 快照的确定性与可测试性。
- 事件同时落 JSONL（`events.jsonl`），用于恢复、轨迹页与回放测试。

### 4.5 状态机（会话/回合/工具/审批）

| 层 | 状态 |
| --- | --- |
| 会话 | `idle → running → (waiting_approval | waiting_user | paused) → running → idle / failed` |
| 回合 | `queued → streaming → tool_calls → streaming → done / cancelled / error` |
| 工具调用 | `pending → awaiting_approval → running → done / error / denied / cancelled` |
| 审批 | `requested → approved_once / approved_always / denied / expired` |

所有状态转移都有表单测试；**状态是推导的不变量**（如"是否可停止"= 会话状态 ∈ {running}），不存冗余字段。

### 4.6 目录结构（建议）

```
/data/mygo
├── cmd/mygo-agent/main.go
├── internal/{app,ui,view,runtime,tools,provider,prompt,ext,store,design,platform}
├── internal/ui/...            # 视图按屏拆分：home/session/plugins/settings/trajectory
├── assets/                    # logo、图标、字体（Montserrat、JetBrains Mono）
├── baseline/                  # DSH 基线：VERSION、截图、geometry.json、tokens.json
├── docs/                      # 本规划 + 规格 + ADR
├── scripts/                   # 提取、生成、门禁、打包
└── go.mod
```

### 4.7 关键 ADR（先定，后写代码）

| # | 决策 | 理由 | 备选与否决 |
| --- | --- | --- | --- |
| ADR-1 | mygo 原生 UI，不用 webview | 硬约束；单二进制、低内存、可无头测试 | webview 复用前端：否决（违反"UI 必须 mygo"；且仍要 Node/构建链） |
| ADR-2 | 快照 + `Invalidate` 的 UI 模型 | mygo 的 view 每帧重跑；必须与后台解耦 | 直接回调改状态：会死锁/竞态 |
| ADR-3 | eino ADK 作为运行时骨架 | 有 ChatModelAgent、interrupt、checkpoint、middlewares、backgroundtask、prebuilt | 自研 agent loop：否决（重复造轮子且没有 checkpoint/中断） |
| ADR-4 | SQLite 索引 + JSONL 事件流 | 查询快 + 追加写安全 + 可回放 | 纯 SQLite：JSONL 便于恢复/轨迹；纯 JSONL：查询差 |
| ADR-5 | 插件 = 编译期内置注册表（内置插件可开关/配置），不做第三方安装 | 纯 Go 不能运行 TS 插件；产品只需要官方内置能力；编译期固定便于对齐与测试 | 第三方插件 / WASM / goja：否决（范围外且不可对齐） |
| ADR-6 | 无内核沙箱，策略 + 审批 + 审计 | 纯 Go 能力边界；不假装有隔离 | 引入 cgo 沙箱：违反 C1 |
| ADR-7 | 令牌机器生成 + 单测锁死 | "1:1"需要可验证，不靠人眼 | 手抄令牌：漂移不可控 |
| ADR-8 | 交互/像素测试基于 `NewTester`/`Render` | 确定性、无显示器、快 | 外部截图 E2E：作为补充，不作主门禁 |
| ADR-9 | 基础样式全部函数化（CSS → Go style 函数） | 单一事实来源；像素对齐集中维护；复用与评审 | 各屏手写链式样式：漂移、无法门禁 |

---

## 5. UI 规格（像素对齐）

### 5.1 窗口与栅格

- 默认窗口 1200×800，最小 900×600；基线验收尺寸 1600×1000（与截图一致）。
- 主区内容列宽 = `min(868, 可用宽度 - 96)`，居中；侧栏 280 固定；窗口 < 1100 时侧栏可折叠为 56px 图标栏（P1）。
- 缩放：100% / 125% / 150%；`tt.SetScale` 验证令牌与布局随 DPR 等比。
- 无系统标题栏（自绘窗口控件或原生标题栏隐藏由平台决定，M1 定稿）。

### 5.2 三屏结构树（P0）

```
Window
├─ Sidebar (280)
│  ├─ BrandRow(64)              # wordmark + 折叠按钮
│  ├─ PrimaryButton "New Session"
│  ├─ NavRow "Plugins" / "Automation tasks"
│  ├─ Section "Workspaces" + 搜索/筛选/新建图标
│  ├─ WorkspaceRow(选中)         # 名称 + 图标
│  ├─ SessionRow ×N             # 标题 + 相对时间 + 选中态
│  ├─ ...
│  └─ BottomRow "… More"
└─ Main
   ├─ HomeView:    TitleRow("Into the Unknown"+Preview badge) + Composer(868)
   ├─ SessionView: Header(75) [Title · AgentTeam · Mode · Background · …]
   │               Tabs [Chat | Trajectory]
   │               MessageList(Scroll, 内容列 868)
   │               Composer(868)
   │               UsageFooter
   └─ PluginsView / SettingsView / AutomationView
```

### 5.3 组件映射表（不造控件，只组合）

| DSH 组件/区域 | mygo 控件 | 备注 |
| --- | --- | --- |
| 侧栏整体 | `ui.Sidebar` + `ui.Column`/`ui.Scroll` | mygo 自带 source-list 语义 |
| 主按钮/次按钮/图标按钮 | `ui.Button` / `ui.ButtonBase` | 样式走 `design` 令牌 |
| 搜索框/输入框 | `ui.TextInput` / `ui.SearchField` | |
| Composer 多行输入 | `ui.TextArea` + 内部 `ui.Row` 组合 | 自动增高、Enter/Shift+Enter |
| 下拉（项目/模型/权限/模式） | `ui.Select` / `ui.MenuButton` + `ui.Popover` | 模型项支持搜索 |
| Tabs / Segmented | `ui.Tabs` / `ui.Segmented` | 键盘方向键/Home/End 自带 |
| 消息流 | `ui.List`（虚拟化）或 `ui.Scroll` | 1000+ 消息不卡 |
| Markdown 消息 | `ui/markdown`（`View`/`Stream`） | 流式增量、表格、任务列表、数学 |
| 代码块/编辑器 | `ui/editor`（只读高亮） | 复制、换行、行号 |
| 差异卡片 | `ui/editor.DiffView` | 单栏/并排、± 行 |
| 工具调用卡片 | `ui.Collapsible` + `ui.Row`/`ui.Column` + 边框令牌 | 摘要行 + 展开详情 |
| 审批/提问 | `ui.AlertDialog` / 自定义卡片 + `ui.Button` | 支持"允许一次/始终/拒绝" |
| 轨迹 | `ui.List` + `ui.ColoredText`/`ui.Badge` | 事件时间线 |
| Badge/状态点 | `ui.Badge` / 小圆点（`ui.Box`+`Radius`） | 颜色走状态令牌 |
| Toast/通知 | `c.Toast` | 3s 自动消失 |
| Tooltip | `ui.Tooltip` | 键盘焦点也触发，带无障碍描述 |
| 弹窗/命令面板 | `ui.AlertDialog` + `ui.Popover` / 自组 `ui.Modal` | M5 定 |
| Spinner/进度 | `ui.Spinner` / `ui.Progress` | "Deep diving …" 计时文案 |
| 图标 | `ui.Icon` + SVG 资产（`ui.ParseSVG`） | 统一 16/20 两档 |
| 字体注册 | `ui.RegisterFont` | Montserrat / 等宽字体 |
| 窗口/托盘/菜单/快捷键 | mygo 根包 | 单实例、关窗=隐藏（托盘） |

### 5.4 基础样式函数层（把"基础 CSS"做成函数，所有界面共用）

**目标**：把 DSH 的"基础 CSS"（reset + 设计令牌 + 组件类）在 Go 里实现成**可组合的纯函数**。所有界面只调用函数，视图层不出现颜色、圆角、字号、间距字面量；改一处样式，全应用生效。

**三层结构**

| 层 | 内容 | 位置 | 示例 |
| --- | --- | --- | --- |
| ① 令牌 | 颜色 / 字号 / 行高 / 圆角 / 阴影 / 间距 / 动效曲线 | `internal/design/tokens_gen.go`（生成） | `ColorLabelPrimary`、`TypeBody`、`RadiusMD`、`ShadowLV2` |
| ② 原子函数 | 单属性应用（等价 CSS 工具类） | `internal/design/atoms.go` | `t.Pad(design.Space4)`、`t.Text(design.TextSecondary)`、`t.Fill(design.SurfaceSidebar)` |
| ③ 组件函数 | 语义组件样式（等价 CSS 组件类） | `internal/design/components.go` | `t.Panel()`、`t.Composer()`、`t.Button(design.Primary)`、`t.ToolCard()` |

**API 形状**（Go 函数 + 链式组合，不用字符串类名）

```go
// internal/design/style.go
type Style func(ui.Element) ui.Element

func Apply(el ui.Element, ss ...Style) ui.Element {
    for _, s := range ss { el = s(el) }
    return el
}

// 主题感知：浅色/深色只是两套令牌，函数签名不变
func Of(c *ui.Context) *Tokens // c.Theme().Dark 选表

// atoms.go
func (t *Tokens) Pad(v SpaceRole) Style { return func(el ui.Element) ui.Element { return el.Padding(t.Space[v]) } }
func (t *Tokens) Text(r TypeRole) Style { /* 字号/行高/字重/颜色 */ }
func (t *Tokens) Fill(s SurfaceRole) Style

// components.go
func (t *Tokens) Panel(opts ...Style) Style        // 背景 + 边框 + 圆角 + 阴影
func (t *Tokens) Composer() Style                  // 868 列宽约束 + 圆角 + 边框 + 内边距
func (t *Tokens) Button(v ButtonVariant) Style
func (t *Tokens) ToolCard() Style
func (t *Tokens) ListRow(state RowState) Style     // Normal / Hover / Selected / Focus
```

**界面写法（所有屏一致的调用方式）**

```go
func (v sessionView) body(c *ui.Context) {
    t := design.Of(c)
    design.Apply(ui.Column(c), t.Page()).Children(func() {
        design.Apply(ui.Row(c), t.ContentColumn()).Children(func() {
            // 消息流、工具卡片、composer 全部由样式函数产出，屏里只有布局与数据
            _ = design.Apply(ui.TextArea(c, &s.Draft), t.ComposerInput())
            _ = design.Apply(ui.Button(c, "发送"), t.Button(design.Primary))
        })
    })
}
```

**六条规则（会写进 CI）**

1. **单一事实来源**：颜色/尺寸/圆角/阴影只允许出现在 `internal/design`；视图层出现 `ui.Hex(...)`、`ui.RGBA(...)`、`.Padding(14)` 这类字面量即 CI 失败（AST 扫描，图标资源与测试快照白名单除外）。
2. **状态由参数表达**：hover/active/selected/disabled/dark 是组件函数的参数，内部查令牌；**禁止**为某个状态复制一份函数或一份屏幕代码。
3. **与基线一一对应**：每个组件函数登记对应的 DSH CSS 角色（下表），新增屏幕先"登记样式"再写视图——等同于往设计系统里加 class，评审能看见。
4. **原生控件也要统一**：`design.Theme(dark) *ui.Theme` 喂给 `c.SetTheme`，让 mygo 自带的 Button/TextInput/List 等控件与令牌一致；组件函数负责 Theme 表达不了的部分（边框层级、hover 叠加、动效）。
5. **样式有 golden 测试**：每个组件函数渲染一张样本卡（`ui.Render`），断言颜色/字号/圆角/阴影；暗色再跑一遍，任何漂移都失败。
6. **样式改动可追溯**：`docs/ui-styles.md` 维护"函数 → 基线截图 → 用途"三列表；改样式必须同步改文档，否则评审拒收。

**映射表（DSH CSS 角色 → Go 样式函数）**

| DSH CSS 角色 | Go 样式函数 |
| --- | --- |
| 页面背景 / 内容列 | `t.Page()` / `t.ContentColumn()` |
| 侧栏容器 / 分组标题 / 选中行 / hover 行 | `t.Sidebar()` / `t.SectionLabel()` / `t.ListRow(Selected)` / `t.ListRow(Hover)` |
| 面板 / 卡片 / 浮层 | `t.Panel()` / `t.Panel(t.Shadow(ShadowLV2))` / `t.Popover()` |
| 主按钮 / 次按钮 / 幽灵按钮 / 图标按钮 / 发送圆钮 | `t.Button(Primary)` / `Secondary` / `Ghost` / `t.IconButton()` / `t.SendButton(state)` |
| 输入框 / 搜索框 / Composer | `t.Input()` / `t.SearchInput()` / `t.Composer()` |
| Tab / 胶囊 / 徽标 / 状态点 | `t.Tab(Active)` / `t.Pill()` / `t.Badge(tone)` / `t.StatusDot(tone)` |
| 工具卡片 / 审批卡片 / 提问卡片 | `t.ToolCard()` / `t.ApprovalCard()` / `t.QuestionCard()` |
| 代码块 / Markdown 正文 / 行内代码 | `t.CodeBlock()` / `t.Prose()` / `t.InlineCode()` |
| Toast / Tooltip / 右键菜单 | `t.Toast()` / `t.Tooltip()` / `t.ContextMenu()` |
| 消息气泡（用户）/ 助手正文 | `t.UserBubble()` / `t.Prose()` |

**为什么必须这样做**

- **像素对齐成本最低**：基线升级只改令牌 + 少量组件函数，不用逐屏改链式调用；
- **一致性**：hover/focus/禁用态不会在 20 个屏里各漂各的；
- **可测**：样式函数有 golden 单测，屏幕测试只关心布局与交互；
- **可评审**："新增一个样式"是显式变更，像加 CSS class 一样进入 code review。

### 5.5 逐屏规格（P0 关键值）

| 屏 | 元素 | 规格 |
| --- | --- | --- |
| Home | 标题 | 24/32 粗体居中；距 composer 顶部约 40px |
| Home | composer | 868×116，radius 16–20，边框 `border-l2`，内含：底部左（+、权限、模式）、底部右（模型、推理强度、发送圆钮 32） |
| Session | 头 | 75px：标题 14/22 粗体；右侧 AgentTeam/模式/后台任务胶囊；`…` 与分栏按钮 |
| Session | Tab 行 | Chat/Trajectory 13px，选中下划线 2px，hover 背景 token |
| Session | 消息 | 用户消息右对齐气泡（浅蓝/浅灰底、radius 12）；assistant 左对齐无气泡；工具卡片全宽 |
| Session | composer | 同 Home，含停止按钮（streaming 时替换发送） |
| Session | usage | 11–12px 灰字：turns/steps/tok·s/tokens/cache/context% |
| Plugins | 列表 | 分组标题 20px；行高 56；开关 `ui.Switch`；"Add plugin" 右侧 |
| 全局 | hover/active | hover 背景 `#2631480F`；active 加深一档；focus ring 2px 蓝色 |

### 5.6 动效（交互对齐的一部分）

| 场景 | 时长/曲线 | 实现 |
| --- | --- | --- |
| hover/active | 120ms ease-out | `ui.Motion` |
| 面板/弹层出现 | 160–200ms ease-out，opacity+scale 0.98→1 | `ui.Transition` |
| 消息流追加 | 文本渐显（token 追加） | `markdown.Stream` 自带 |
| Spinner | 0.8–1s 线性旋转 | `ui.Spinner` |
| Toast | 进入 160ms / 停留 3s / 退出 200ms | `c.Toast` 默认，必要时自管 |

### 5.7 Markdown / 代码 / 工具卡片（流式渲染）

- 流式：token 追加 → `markdown.Stream`；完成后切 `markdown.View`，同位置不跳动。
- 代码块：`ui/editor` 只读模式 + 语言高亮 + 复制按钮 + 折叠；超长代码折叠 20 行。
- 差异：`editor.DiffView`，默认单栏；工具结果里的 diff 与文件变更卡片共用组件。
- 工具卡片摘要行：图标 + 工具名 + 关键参数（截断）+ 状态点 + 耗时；展开显示输入/输出（JSON/Markdown/代码按类型渲染）。
- 未知工具/超长输出：截断 + "展开全部" + 复制；任何渲染失败都回退纯文本，不白屏。

### 5.8 中英文与图标

- 语言：zh-Hans 默认、en 次之；全部文案走 `i18n` 目录（mygo 自带 locales），禁止硬编码。
- 中文行高与西文不同（PingFang 的行高更大），基线以 DSH 中文界面为准截图。
- 图标：统一 16/20px、1.5px 线宽、圆角端点；从基线截图逐枚对照重绘为 SVG；差异进白名单。

### 5.9 像素门禁流程（无头、确定、可进 CI）

```
基线截图（DSH，1600×1000） ──┐
                              ├─> internal/pixeltest：ROI 裁剪 → 像素/SSIM 计算 → 报告
我们的画面：ui.Render(view, 1600, 1000, 1) ─┘
                              └─> 失败时输出 diff.png（红=差异）+ 差异清单
```

- 门禁阈值：非文本区 ≤ 0.2%/屏；文本区 SSIM ≥ 0.98；几何断言 ≤1px（用 `tt.Find("…")` 的盒子坐标）。
- 分类白名单：`text-rasterization`（抗锯齿/字体 hinting）、`icon-redraw`、`platform-font-fallback`；每项有 owner 和到期日。
- 同一套流程加 `tt.SetScale(2)` 覆盖 2x；加 `tt.SetDark` 覆盖深色（P1）。

---

## 6. 交互规格（交互对齐）

### 6.1 输入与发送

- Composer 聚焦即输入；Enter 换行、`Ctrl/Cmd+Enter` 发送（与基线一致后再定稿）。
- 支持 `/` 命令面板（/clear、/stop、/help、/new、/settings、/automate、/plugins、/model），`@` 引用文件/会话；上下文压缩仅自动执行（超预算时在每轮开始前压缩），不提供手动命令。
- 输入为空时发送禁用；流式中发送变"停止"；停止只停当前回合，保留已产出文本。
- 发送后不清空历史：同一会话连续发送；`New Session` 清零进入首页。

### 6.2 流式与回放

- **token 流**：首 token 到达即渲染；reasoning 与正文分开展示（若当时模型/模式有）。
- **停止**：立即取消 eino run（context cancel），落库部分输出，状态回 idle。
- **重试**：同一消息重新生成，旧结果保留在轨迹中；"编辑后重发"作为 P1。
- **断线/超时**：错误卡片 + 重试按钮；不吞错、不假装完成。

### 6.3 工具、审批、提问

- 工具开始 → 卡片 streaming 状态；结束 → 成功/失败状态 + 可展开结果。
- 需审批的工具 → 会话进入 `waiting_approval`，composer 上方出现审批卡片（允许一次/始终允许/拒绝）；同一工具"始终允许"按会话或工作区记忆（与基线一致）。
- Agent 提问 → 问题卡片 + 选项/自由文本；回答后自动恢复 run（eino interrupt resume）。
- 权限预设（如 Full access）在 composer 底部常驻，切换需确认。

### 6.4 会话与工作区

- 侧栏：新建会话、会话列表（按时间倒序）、相对时间、当前选中态；工作区切换只切换 UI 作用域，不重启 runtime。
- 会话页头：标题、Agent Team/子智能体数、模式、后台任务数、右侧动作（…、分栏、搜索）。
- 重命名/删除/搜索/导出（P1）；删除二次确认。

### 6.5 Trajectory

- 事件时间线：用户消息、模型轮次、工具调用（参数/结果）、审批、错误、用量；按回合分组。
- 每条可展开看原始 JSON；支持复制；与 Chat 使用同一数据源，绝不双写。

### 6.6 快捷键（M0 从基线"Shortcuts 设置页"提取成表）

建议先覆盖高频项：新建会话、发送、停止、聚焦 composer、切换会话、搜索、打开设置、切换侧栏、命令面板、复制最后回复。所有快捷键在设置页可见、可改（P1）。

### 6.7 错误 / 空态 / 加载 / 离线

| 场景 | 表现 |
| --- | --- |
| 未配置模型 | 引导卡片 + "去设置"；composer 可输入但发送提示 |
| 模型鉴权失败 | 错误卡片 + 重试 + 打开设置 |
| 工具执行失败 | 卡片红色状态 + 错误文本 + 重试 |
| 网络断开 | 顶部细条提示；发送入队或禁用（与基线一致） |
| 空会话/空列表 | 一句话说明 + 主操作；不造假数据 |
| 崩溃重启 | 恢复未完成回合：标记中断 + 重试；JSONL 回放 |

### 6.8 无障碍与 i18n

- 所有图标按钮有 `Label`；焦点可见；Tab 顺序合理；`tt.Announcements` 断言屏幕阅读器播报。
- 对比度：正文 ≥ 4.5:1，辅助文字 ≥ 3:1（令牌已满足，测试断言）。
- 文案 zh/en 双语键值；切换语言即时生效（P1）。

### 6.9 交互用例库（示例，编号即测试名）

| ID | 用例 | 断言 |
| --- | --- | --- |
| IX-01 | 首页输入并发送 | 会话创建、消息上屏、状态变 streaming |
| IX-02 | streaming 中停止 | run 取消、状态 idle、部分文本保留 |
| IX-03 | 工具卡片展开/收起 | 展开显示参数与结果，收起只留摘要 |
| IX-04 | 审批允许一次 | 工具执行、状态恢复 running |
| IX-05 | 审批拒绝 | 工具不执行、模型收到拒绝结果 |
| IX-06 | Agent 提问回答 | 输入回答后 run resume |
| IX-07 | 切换会话 | 草稿不串会话、滚动位置记忆 |
| IX-08 | 新建会话 | 回首页、旧会话仍在列表 |
| IX-09 | / 命令面板 | 过滤、键盘上下、Enter 执行 |
| IX-10 | 模型切换 | 下一回合生效、当前回合不中断 |
| IX-11 | 权限预设切换 | 确认弹窗、持久化 |
| IX-12 | Esc 关闭弹层 | 焦点回原位 |
| IX-13 | 长消息滚动 | 1000 条不卡、帧时间 < 16ms |
| IX-14 | 错误重试 | 重新请求、旧错误标记保留 |
| IX-15 | 深色主题切换 | 颜色令牌全切换、无残留浅色 |
| IX-16 | 语言切换 | 所有文案切换、布局不溢 |
| IX-17 | 窗口缩放 900×600 | 侧栏折叠策略、composer 不溢出 |
| IX-18 | 键盘全程操作 | 不碰鼠标完成发送/审批/切换 |

---

## 7. 功能对齐矩阵（P0/P1/P2）

> 语义：**对齐** = 同样的输入产生同样的用户可感知行为；不要求内部实现/数据格式相同。

### 7.1 宿主能力

| 能力 | DSH 行为 | 我们的实现 | 阶段 | 验收 |
| --- | --- | --- | --- | --- |
| 新建会话 | New Session 按钮 | 同左 | P0 | 会话落库、列表出现 |
| 会话列表 | 侧栏按工作区分组 | 同左 | P0 | 排序/相对时间 |
| 工作区 | 目录 + 名称 + 会话 | 目录 + 名称 + 会话 | P1 | 切换/记忆 |
| 模型选择 | 模型 + 推理强度菜单 | 同左 | P0 | 生效于下一回合 |
| 权限预设 | Full access 等 | 同左（策略实现见 §8.10） | P0 | 审批行为变化 |
| 模式选择 | Standard/… 模式 | 同左（提示词/工具集差异） | P1 | 行为差异 |
| 设置 | 分组设置页 | 同左 | P1 | 保存/校验/回显 |
| 插件页 | Plugins 页：Built-in 设置 + Experimental 开关 + 搜索/刷新 | 内置插件注册表视图（开关、配置、状态） | P1 | 每个内置插件开关与配置生效；无 Add plugin |
| 自动化任务 | 定时/触发任务列表 | 定时任务 + 触发 | P2 | 到点执行 |
| 命令面板 | `/` 命令 | 同左 | P1 | 15 条命令 |
| 用量显示 | turns/steps/tok·s/tokens/cache/context | 同左 | P0 | 与后端计数一致 |
| 托盘/单实例 | 关窗=隐藏 | 同左 | P1 | 二次启动唤起 |
| 自动更新 | 检查/下载 | 检查 + 提示（下载 P2） | P2 | 版本比较 |

### 7.2 Agent 能力（运行时，eino 承载）

| 能力 | 语义 | 实现 | 阶段 |
| --- | --- | --- | --- |
| 对话/工具循环 | ReAct + 流式 | eino ADK `ChatModelAgent` + tools node | P0 |
| 文件工具 | 读/写/编辑/搜索 | Go 实现 + 路径策略 | P0 |
| Shell 工具 | 执行命令（含持久会话） | `os/exec` + 会话/超时/输出截断；**无沙箱** | P0 |
| Web 工具 | fetch/search | 纯 Go HTTP + 可插拔搜索后端 | P1 |
| Todo/计划 | 任务清单 | eino `plantask` middleware 或自实现 | P0 |
| 审批/提问 | 中断等人 | eino `adk` interrupt + resume | P0 |
| 轨迹 | 事件时间线 | 事件流投影 | P1 |
| 上下文压缩 | 超限自动摘要/裁剪 | eino `reduction` + 自研 compaction | P1 |
| 崩溃恢复 | 断点续跑 | checkpoint + JSONL 回放 | P1 |
| 子智能体 | 派生 agent 执行子任务 | `adk/backgroundtask/subagent` + 工具 | P2 |
| Agent Team | 多角色并行 | eino prebuilt `supervisor` / `deep` | P2 |
| 目标（goal） | 跨回合推进目标 | 自研 goal 状态 + 轮次驱动 | P2 |
| 定时（schedule） | 定时触发任务 | 自研调度器（纯 Go cron） | P2 |
| 后台任务（jobs） | 长任务 + 通知 | `adk/backgroundtask` | P2 |
| 技能（skills） | 目录化能力包 | 运行时加载 SKILL.md + 工具白名单 | P2 |
| MCP | 外部工具服务器 | `modelcontextprotocol/go-sdk` 客户端 | P2 |
| 工作流 | 脚本化编排 | eino compose + 工具化 | P3 |
| 插件能力 | 官方 Built-in（Subagent/Web search/Agent loop/Shell）+ Experimental（Agent Teams、Auto Authorization Review、Git Worktrees、Persistent terminals、Session search、Conversation-following titles 等） | 全部内置实现，按官方默认开关状态对齐 | P1/P2 |

### 7.3 功能降级清单（必须写进产品文档）

1. **第三方插件**：不做安装/上传/市场/热加载；插件集合编译期固定。插件页只管理官方内置插件（Built-in + Experimental）。
2. **内核沙箱**：只有策略/审批/审计；不对恶意命令提供系统级隔离承诺。
3. **DSH 数据兼容**：不读写其会话库/配置。
4. **实验性能力**（语音输入、CoT 翻译、Office/PDF 变换、检查器）：P3，入口可保留但默认隐藏。
5. **平台差异**：Windows 的 pwsh 持久会话、Linux 的 pty 行为按平台单独验证（P1 末尾）。

---

## 8. Agent 运行时（eino）

### 8.1 职责边界

| 归 eino | 归我们 |
| --- | --- |
| Agent 循环、消息 schema、工具 schema、流式回调 | 事件投影、UI 快照、持久化 |
| interrupt/resume、checkpoint 接口 | checkpoint 存储实现（SQLite/文件） |
| 中间件（reduction、plantask、filesystem、backgroundtask） | 工具实现与权限策略 |
| 多智能体 prebuilt（deep/supervisor/planexecute） | 角色提示词、工具白名单、调度 |
| 模型组件调用 | provider 适配、用量与成本、重试/降级 |

### 8.2 图设计

```
ChatModelAgent(deepseek-v4.1-flash, tools=[...], middlewares=[...])
  ├─ 工具节点（eino tools node）
  ├─ interrupt：审批/提问 → 挂起 → resume
  └─ checkpoint：每个回合后写 SQLite
```

- 单会话一个 Runner；会话间隔离。
- 工具集按"模式/角色"白名单启用（对齐基线行为）。
- 子智能体 = 独立 Runner + 受限工具集 + 预算（步数/token），父会话只收结果摘要。

### 8.3 持久化与恢复

- `session.jsonl`：追加式事件（用户/assistant/工具/审批/用量），**唯一真相**。
- `index.sqlite`：会话、消息、工具调用、用量、搜索索引（可重建）。
- checkpoint：每回合结束与工具批次结束落盘；崩溃后从 checkpoint + JSONL 尾部对齐恢复。
- 恢复策略：未完成回合标 `interrupted`，给"继续/重试"；不自动重放有副作用的工具。

### 8.4 工具实现清单

| 工具 | 语义 | 安全/限制 | 阶段 |
| --- | --- | --- | --- |
| `read_file` | 读文本/图片元数据 | 路径策略、大小上限、二进制检测 | P0 |
| `write_file` | 覆盖写 | 路径策略、审批（可配置） | P0 |
| `edit_file` | 字符串替换编辑 | 精确匹配、失败给上下文 | P0 |
| `glob` / `grep` | 文件搜索 | 工作区根限制、忽略规则 | P0 |
| `bash` | 执行命令 | 超时、输出截断、危险命令策略、审批 | P0 |
| `todo` | 任务清单 | 会话内状态 | P0 |
| `ask_user` | 提问中断 | interrupt | P0 |
| `present` | 交付物展示 | 文件/卡片元数据 | P1 |
| `web_fetch` | 抓网页 | SSRF 防护（拒私网）、大小上限、超时 | P1 |
| `web_search` | 搜索 | 可插拔后端（DeepSeek/自建/Brave） | P1 |
| `jobs` | 后台任务管理 | 与 backgroundtask 对接 | P2 |
| `subagent` | 派生子智能体 | 预算、工具白名单、不可递归越权 | P2 |
| `goal` | 目标推进 | 轮次驱动、进度落库 | P2 |
| `schedule` | 定时 | cron 解析、单机时区 | P2 |
| `skill` | 技能加载 | 目录白名单、内容当数据不当指令 | P2 |
| `mcp__*` | MCP 工具 | 服务器白名单、工具前缀、超时 | P2 |

**工具命名/描述**：面向模型的描述要与 DSH 的语义一致（同样的任务能同样完成），但**不逐字照抄提示词**。

### 8.5 上下文与用量

- token meter：按模型 tokenizer 估算（DeepSeek/OpenAI 兼容可用近似），记录 in/out/cache-hit；UI 显示 "tok·s / tokens / cache hit % / context %"。
- 压缩：> 80% 窗口触发；策略 = 工具结果裁剪 + 早期消息摘要（保留最近 N 轮 + 系统提示 + 任务状态）；压缩事件进轨迹。
- 缓存：DeepSeek 前缀缓存命中统计直接展示；不伪造数据。

### 8.6 模型层

- 统一接口：`Provider.Stream(ctx, req) (<-chan Chunk, error)`；chunk 含 text/reasoning/tool_call/usage。
- 首批：DeepSeek（OpenAI 兼容，走 eino-ext openai 组件），OpenAI 兼容通用 base_url；Anthropic 自研 SSE 适配（P1）。
- 模型能力表（上下文窗口、是否支持 tool/reasoning）驱动 UI 与压缩策略；未知模型给保守默认。
- 失败：429/5xx 指数退避 + 可见错误；不静默重试破坏幂等。

### 8.7 命令执行与安全模型（诚实版）

**能做的**：命令白名单/黑名单、工作区路径约束、超时、资源上限（子进程数）、`setsid` 清理进程组、输出截断、敏感文件读取拦截、审计日志（谁在何时执行了什么、是否经审批）。

**做不到（纯 Go 无 cgo）**：seccomp/landlock/AppArmor 级别的系统调用隔离；文件系统命名空间；网络命名空间。→ 产品口径：**"权限模型是交互与策略级的，不是内核沙箱"**。对高风险命令默认要求审批。

### 8.8 失败恢复与幂等

- 工具调用有唯一 `CallID`；结果落盘后重放不重复执行有副作用的工具。
- 审批中断跨重启保持（审批请求落库，重启后可继续/过期）。
- 模型流中断：保留部分输出 + 可重试；重试不覆盖旧 assistant 消息（轨迹保留）。
- 单实例锁 + 崩溃安全的 JSONL（追加 + fsync 策略）。

---

## 9. 数据模型与存储

### 9.1 实体

```
Workspace(id, name, root, created_at, settings)
Session(id, workspace_id, title, created_at, updated_at, model, mode, permission, status)
Message(id, session_id, role, content, reasoning, created_at, turn_id)
ToolCall(id, session_id, message_id, name, args, result, status, started_at, ended_at, approval)
Approval(id, session_id, call_id, kind, decision, decided_at)
Usage(id, session_id, turn_id, model, in, out, cache, cost, ts)
Event(id, session_id, seq, type, payload, ts)      # 与 JSONL 同构
Skill(name, path, enabled)
Plugin(name, kind, enabled, config)
Schedule(id, cron, prompt, enabled, last_run)
Goal(id, session_id, objective, status, rounds)
```

### 9.2 SQLite schema（简要）

- `workspaces`、`sessions`、`messages`、`tool_calls`、`approvals`、`usage`、`events`、`settings`、`skills`、`schedules`、`goals`。
- 单写者事务；`PRAGMA journal_mode=WAL`、`foreign_keys=ON`；迁移用 `schema_version` 表，低版本拒绝启动并提示。
- 全文搜索：`messages_fts` 虚拟表（FTS5 需 modernc 支持，M1 验证；否则 LIKE + 索引兜底）。

### 9.3 文件布局

```
~/.mygo-agent/
├── config.yaml
├── index.sqlite
├── workspaces.json
├── sessions/<session-id>/
│   ├── session.jsonl
│   └── artifacts/
├── skills/<name>/SKILL.md
├── plugins/
├── cache/
└── logs/ (按天轮转)
```

### 9.4 迁移与导出

- schema 版本守卫；迁移单向、幂等、可回滚到备份。
- 导出：会话 Markdown + JSON；导出不含密钥。
- 备份：设置页"备份/恢复"（P2）。

---

## 10. 质量与验收体系

### 10.1 测试金字塔

| 层 | 手段 | 数量目标 |
| --- | --- | --- |
| 单元 | 纯函数、状态机表、工具语义（假 fs/假进程） | 300+ |
| 视图 | `ui.NewTester`：点击/输入/键盘/虚拟时钟/无障碍播报 | 100+ |
| 像素 | `ui.Render` → golden 对比（§5.9） | P0 每屏 3 状态（空/加载/有内容） |
| 样式 | `ui.Render` 组件样本 golden（浅/深） | 每个组件函数 1 张，断言令牌 |
| 交互 E2E | Tester 驱动的完整用例库（§6.9） | 30+ |
| 运行时 | 脚本化假模型（确定性流）验证工具循环/中断/恢复 | 50+ |
| 冒烟 | `xvfb-run` 真窗口启动 + 截图 + 退出 | 1/平台 |
| 性能 | 启动、首帧、滚动帧时间、内存、包体 | 见 §10.3 |

### 10.2 门禁（CI 必须全绿）

```sh
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build ./...
sh scripts/check-nocgo.sh
sh scripts/pixel-gate.sh        # 无头像素对比
sh scripts/lint-style-literals.sh # 视图层禁止样式字面量
sh scripts/cross-build.sh       # 四目标
```

### 10.3 性能预算

| 指标 | 预算 |
| --- | --- |
| 冷启动到首帧 | ≤ 400ms（中端机） |
| 空闲内存 | ≤ 180MB（整个应用进程） |
| 1000 条消息滚动 | ≥ 55fps，无长任务 > 50ms |
| 流式 token 到上屏 | 首 token < 300ms 增量、追加帧间隔 < 16ms |
| 二进制 | stripped ≤ 20MB |
| 工具卡片展开 | < 16ms |

### 10.4 CI 矩阵

| Job | 内容 |
| --- | --- |
| build-linux | amd64/arm64，no-cgo 检查，单测，像素门禁（Xvfb 不需要，Render 无头） |
| build-darwin | arm64 交叉编译 + 单测（不跑 UI 截图） |
| build-windows | amd64 交叉编译 + 单测 |
| smoke-linux | Xvfb 真窗口启动、截图、退出码 0 |
| lint | gofmt、go vet、依赖白名单检查 |

---

## 11. 里程碑与排期（2 人）

### 11.1 里程碑

| 里程碑 | 周期 | 目标 | 出口标准 |
| --- | --- | --- | --- |
| **M0 基线工程化** | 3 周 | 仓库骨架、no-cgo 守卫、令牌/基线提取、**逐屏截图 + 控件清单 + 基线文档**、基础样式函数层、`ui.Render` 像素链路、空壳窗口 | `go test ./...` 无显示器全绿；tokens 生成 + 单测；样式 golden（浅/深）通过；`docs/baseline/` 覆盖全部 P0/P1 屏与控件（100%），无"待确认"项 |
| **M1 宿主壳（P0 像素）** | 3 周 | 侧栏 + Home + 空会话 + 主题 + 字体；像素门禁跑通 | Home 屏三状态像素通过；交互 IX-01/08/17 通过 |
| **M2 会话与流式（P0）** | 3 周 | eino 假模型驱动会话页：消息流、Markdown 流式、停止、用量条 | Session 屏像素通过；IX-01/02/07/13 通过；假模型回归 |
| **M3 工具与审批（P0）** | 3 周 | 文件/Shell/Todo/ask_user、审批卡片、interrupt/resume、工具卡片 | IX-03/04/05/06 通过；真模型冒烟 3 任务 |
| **M4 轨迹/持久化/恢复（P1）** | 2 周 | JSONL+SQLite、轨迹页、崩溃恢复、会话管理 | 重启恢复用例通过；IX-14 通过；性能预算达标 |
| **M5 设置/插件/自动化（P1/P2）** | 3 周 | 设置页、插件页、自动化任务页、模型/权限/模式选择、i18n | 三页像素 + 功能验收；IX-09/10/11/15/16 通过 |
| **M6 高级 Agent 能力（P2）** | 4 周 | 子智能体、Agent Team、skills、MCP、jobs、goal、schedule、压缩 | 每条能力一个端到端验收；功能矩阵 P2 行完成 |
| **M7 硬化发布** | 2 周 | 三平台打包、安装、签名/公证、自动更新检查、性能与稳定性 | 发布检查单全绿；二进制/内存/启动达标 |

**依赖关系**：M0 → M1 → M2 → M3 →（M4、M5 可并行）→ M6 → M7。总工期约 **22 周**。

### 11.2 人员与写范围（避免并行改同一批文件）

| 角色 | 负责 | 写范围 |
| --- | --- | --- |
| Lead / 架构 | ADR、令牌与基线、像素/交互体系、集成 | `docs/`、`baseline/`、`internal/design`、`scripts/` |
| Go A（UI） | 所有 `internal/ui` 与 view 快照；像素与交互门禁 | `internal/ui`、`internal/view`、`internal/design` 消费 |
| Go B（运行时） | eino 图、工具、provider、store、恢复 | `internal/runtime`、`internal/tools`、`internal/provider`、`internal/store` |
| 共享 | `cmd/`、`internal/app` 由 Lead 集成，改动需评审 | — |

### 11.3 每个里程碑的固定动作

1. 更新基线（如 DSH 有变化）；2. 代码 + 测试；3. 三轴门禁；4. 写/更新 ADR；5. 录一段 60s 演示；6. 更新 `docs/STATUS.md`（唯一状态真源）。

### 11.4 M0 详细任务（两周，可直接开工）

| ID | 任务 | 产出 | 估时 |
| --- | --- | --- | --- |
| M0-1 | 仓库骨架 + `go.mod` + toolchain 钉死 | `cmd/`、`internal/`、Makefile | 0.5d |
| M0-2 | no-cgo 守卫 + 四目标交叉编译脚本 | `scripts/check-nocgo.sh`、CI | 0.5d |
| M0-3 | 基线采集：截图 + `geometry.json` + `VERSION` | `baseline/` | 1d |
| M0-4 | 令牌提取脚本 + 生成器 + 单测 | `internal/design/tokens_gen.go` | 1.5d |
| M0-5 | 空壳窗口（侧栏 + 主区 + 主题 + 字体注册） | `internal/ui/shell.go` | 1.5d |
| M0-6 | `ui.Render` 像素测试链路 + diff 工具 | `internal/pixeltest`、`scripts/pixel-gate.sh` | 2d |
| M0-7 | `ui.NewTester` 交互测试骨架 + 虚拟时钟用例 | `internal/ui/*_test.go` | 1d |
| M0-8 | 事件总线 + 快照骨架（假数据） | `internal/view`、`internal/runtime/bus.go` | 1.5d |
| M0-9 | 存储骨架（SQLite 打开/迁移/关闭）+ JSONL | `internal/store` | 1d |
| M0-10 | 单实例/托盘/关窗隐藏 | `internal/app` | 1d |
| M0-11 | 基础样式函数层（`style.go`/`atoms.go`/`components.go`）+ 字面量 lint + 样式 golden | `internal/design` | 2d |
| M0-12 | 逐屏截图采集（CDP 驱动：`scripts/cdp.mjs` + `baseline/controls/*.json`） | `baseline/screenshots/`、`baseline/controls/` | 2d |
| M0-13 | 基线文档：屏幕地图、控件总表、逐屏行为说明、覆盖门禁 | `docs/baseline/` | 3d |

---

## 12. 风险登记册

| # | 风险 | 概率 | 影响 | 缓解 | 触发信号 |
| --- | --- | --- | --- | --- | --- |
| R1 | 文本渲染与 Chromium 不一致，像素门禁长期红 | 高 | 中 | 文本区用 SSIM/掩码；先对齐字体族与字号；断行差异单独归类 | 差异集中在文字边缘 |
| R2 | mygo 控件缺口（卡片/命令面板等复合件） | 中 | 中 | 用 Column/Row/边框组合；缺口清单 M1 定稿；不自行发明控件库 | 需要"新控件"超过 3 个 |
| R3 | eino 与 DSH 行为模型差异（中断/恢复/子智能体） | 中 | 高 | 先做假模型一致性测试；运行时能力矩阵逐项验收；必要时在 eino 外包一层薄适配 | 用例语义对不齐 |
| R4 | 插件生态功能无法 1:1（TS/Cordis） | 高 | 高 | 提前改产品口径：内置注册表 + WASM v2；插件页交互对齐 | 用户/评审要求跑第三方插件 |
| R5 | 纯 Go 无内核沙箱被质疑安全性 | 中 | 高 | 审批默认严格、审计、危险命令拦截；文档明确边界 | 安全评审 |
| R6 | 范围蔓延（DSH 功能极多） | 高 | 高 | P0/P1/P2 分层 + 功能矩阵门禁；未列入矩阵的一律不做 | 迭代中加需求 |
| R7 | mygo/eino 版本为预发布，API 变动 | 中 | 中 | 版本钉死、升级独立 PR、封装边界（provider/tools 不直接扩散 API） | 升级时编译大面积失败 |
| R8 | SQLite 纯 Go 性能（全文搜索/大库） | 低 | 中 | FTS 验证；LIKE 兜底；分页；索引 | 搜索 > 200ms |
| R9 | 单一 UI 线程被长任务卡住 | 中 | 高 | 快照 + 异步命令队列；CI 加帧时间断言 | 滚动掉帧/输入延迟 |
| R10 | 基线随 DSH 升级漂移 | 中 | 中 | 基线锁版本 + 独立更新 PR | 上游发版 |

---

## 13. 待拍板问题

**已锁定**（2026-11-04）：
- 基线 **DSH 0.2.1-rc.2**，官方发版后同步；
- 品牌 **ZeoKeel**；字标 = 图形标 + “ZeoKeel”文字（**已选 A**，系统字体栈、17px/600、间隔 8px，规格 `docs/wordmark.md`）；logo 只用 `logo/` 资产；
- 命名：应用 `ZeoKeel` / 二进制 `zeokeel` / module `github.com/zeokeel/zeokeel` / 数据目录 `~/.zeokeel`；
- 插件：全量对齐官方 Built-in + Experimental，开关默认值照官方；**不做第三方安装/市场**，插件页移除 “+ Add plugin”；
- 主题与语言：默认中文，浅色 + 深色同期交付；
- **样式路线（用户已选 A）**：原生 mygo + Go 样式函数层；DSH 的 CSS 只作为对齐依据（computed style 全量抽取），**不在运行时解析 CSS**；webview/CSS 运行时方案否决。
- 可建测试会话做行为确认；可读官方源码确认行为（只读，不复制）。

仍需你拍板（按重要性排序）：

1. **ZeoKeel 字标**：`logo/` 只有图形标，没有文字标。侧栏/关于/安装包里的 "ZeoKeel" 文字用什么字体与字重？还是只放图形标？
2. **应用名与坐标**：应用名 `ZeoKeel`、二进制名 `zeokeel`、Go module 路径、数据目录 `~/.zeokeel/` 是否 OK？
3. **内置插件清单**：DSH 当前 Built-in（Subagent / Web search / Agent loop / Shell）与 Experimental（Agent Teams、Auto Authorization Review、Git Worktrees、Persistent terminals、Session search、Conversation-following titles 等）是否全部按官方默认状态内置？还是只选一部分？
4. **主题与语言**：默认界面语言中文还是英文？v1 是否浅色 + 深色同期交付（DSH 设置里有 Light/Dark/System）？
5. **"Add plugin" 按钮**：DSH 插件页右上角有 "+ Add plugin"；我们不做第三方安装，这个按钮是移除、置灰，还是保留并提示"仅内置"？
6. **首发平台**：Linux 先行验证 + macOS/Windows 同版本发布？
7. **模型范围**：v1 只接 DeepSeek / OpenAI 兼容；Anthropic 是否必须？
8. **发布形态**：签名/公证/自动下载更新是否进 v1？

---

## 附录 A：仓库结构（建议）

```
/data/mygo
├── cmd/mygo-agent/
├── internal/
│   ├── app/          # 组装、生命周期、单实例、托盘
│   ├── ui/           # 纯视图 + 主题 + 组件
│   ├── view/         # 快照、投影、动作
│   ├── runtime/      # eino 图、会话、审批、事件
│   ├── tools/        # 工具实现
│   ├── provider/     # 模型 provider
│   ├── prompt/       # 系统提示词与模板
│   ├── ext/          # skills / MCP / goal / schedule
│   ├── store/        # SQLite + JSONL
│   ├── design/       # 令牌（生成）
│   ├── pixeltest/    # 像素门禁
│   └── platform/     # 平台差异封装
├── assets/           # 图标 / 字体 / logo
├── baseline/         # DSH 基线（VERSION/截图/controls dump/geometry/tokens）
├── docs/             # PLAN + 规格 + ADR + STATUS
│   └── baseline/     # 施工图：index / controls / coverage / screens/*
├── scripts/          # 提取 / 生成 / 门禁 / 打包 / cdp.mjs
└── go.mod
```

## 附录 B：基线采集与令牌提取（可复现命令）

```sh
# 1) 锁定基线版本
echo "dsh 0.2.1-rc.2" > baseline/VERSION

# 2) 截图（需要 DSH web 打印的带鉴权 token 的 URL）
#    有显示器：直接用浏览器 1600×1000 截图
#    无显示器：用 chromium headless（--user-data-dir 必填）
CHROME=.../chrome
"$CHROME" --headless=new --no-sandbox --disable-gpu --hide-scrollbars \
  --window-size=1600,1000 --user-data-dir=/tmp/chrome-profile \
  --virtual-time-budget=12000 --screenshot=baseline/home@1600x1000.png "<带 token 的 URL>"

# 3) 提取令牌（从 DSH 前端产物；按需调整路径）
node scripts/extract-tokens.mjs > baseline/tokens.json

# 4) 生成 Go 令牌并验证
go generate ./internal/design
go test ./internal/design -run TestTokensMatchBaseline
```

## 附录 C：验收单模板（每屏一份）

```md
# <屏名> 对齐验收单

## 像素
- [ ] 几何：<元素> 位置/尺寸与基线偏差 ≤ 1px（附 geometry diff）
- [ ] 颜色：令牌逐项一致（附 tokens diff）
- [ ] 文本区 SSIM ≥ 0.98，非文本区差异 ≤ 0.2%
- [ ] 2x DPR 通过
- [ ] 白名单项：<项> — 原因 / owner / 到期日

## 交互
- [ ] 用例：IX-xx, IX-yy …（附测试名）
- [ ] 键位/焦点/动效时长与基线一致

## 功能
- [ ] 矩阵行：<能力> 成功路径 / 失败路径 / 边界路径
```

## 附录 D：术语

| 术语 | 含义 |
| --- | --- |
| DSH | DeepSeek Harness，本项目的 1:1 对齐基准（Web 客户端） |
| 快照 | 后台产出的不可变 UI 状态，view 每帧只读 |
| 令牌 | 颜色/字体/圆角/阴影/间距等设计常量 |
| 像素门禁 | 无头渲染 + 与基线对比的自动化验收 |
| interrupt | eino 的中断机制，用于审批/提问挂起与恢复 |
| 降级 | 明确记录的不 1:1 项，必须有替代行为与文案 |
