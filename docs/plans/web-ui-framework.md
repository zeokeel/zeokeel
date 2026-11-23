# Web UI 框架对比与选型（2026-11-23，v2）

> 数据来源：GitHub API / shields.io（星数，2026-11-23）+ npm registry（周下载量，last-week）+ 官方变更日志。
> 结论：**分面选型，不用单一框架通吃**。壳层 shadcn/ui（Base UI）＋ 会话面 assistant-ui primitives ＋ 数据面 TanStack。

---

## 0. 决定（已定稿 2026-11-23）

**Ant Design 6 + Ant Design X 2.0 作为唯一框架**（用户决策）。其余候选作为证据保留在下面各节。

这个决定比我初稿的评估更有依据，有两条硬事实修正了我先前的反对意见：

1. **antd v6 默认就是 CSS 变量模式**（v5 才需要 `cssVar: true` 显式开），并可开 `zeroRuntime: true` + `@ant-design/static-style-extract` 预提取静态 CSS——"运行时样式税"这条反对基本失效。
2. **seed token 档位与现有 `internal/design` 同源**：`sizeUnit=4`（我们的 4 DIP 栅格）、`controlHeight=32`（`design.ControlHeight`）、`borderRadius=6`（`RadiusSM`）、`fontSize=14`（`TypeBody`）、`lineWidth=1`（hairline）。这不是巧合——两边都源自同一套桌面密度约定，映射成本远低于 MUI/Mantine。

**仍然成立的风险（必须用纪律抵掉，见 §8）**：默认视觉仍有 AntD 味道；`@ant-design/icons` 与"只用 internal/icons"规则冲突；Ant Design X 仅 4.9k 星且 v1→v2 有破坏性变更。

**分面配套（不构成第二个框架）**

| 面 | 选什么 | 理由 |
|---|---|---|
| 框架本体 | **antd 6**（含 `@ant-design/pro-components` 按需） | 决策 |
| 会话面 | **Ant Design X 2.0**（Bubble/Sender/Conversations/ThoughtChain 一类） | 现成 agent 对话件，省 session 屏大头 |
| 编辑器 / 高亮 / diff | CodeMirror 6 + shiki | antd 不提供 |
| 分栏 | `react-resizable-panels` | antd 只有 resizable Drawer |
| 虚拟化 | antd 内置 `virtual`（Table/Select/Tree）；transcript 用 `@tanstack/react-virtual` | antd 无通用虚拟列表 |

**明确不引入**：MUI、Mantine、Chakra、shadcn、assistant-ui、第二套图标库、任何运行时 CDN。

---

## 1. 星数排行榜（2026-11-23）

### 1.1 UI 框架 / 组件库

| # | 仓库 | 星 | 类别 | 样式模型 |
|---|---|---|---|---|
| 1 | [shadcn-ui/ui](https://github.com/shadcn-ui/ui) | **129k** | 组件源码分发 | Tailwind v4 + `data-slot` |
| 2 | [ant-design/ant-design](https://github.com/ant-design/ant-design) | **100k** | 成品框架 | CSS-in-JS + SeedToken |
| 3 | [mui/material-ui](https://github.com/mui/material-ui) | **99k** | 成品框架 | Emotion |
| 4 | [chakra-ui/chakra-ui](https://github.com/chakra-ui/chakra-ui) | 41k | 成品框架 | Panda CSS |
| 5 | [CopilotKit/CopilotKit](https://github.com/CopilotKit/CopilotKit) | 39k | AI agent UI | 自带样式 |
| 6 | [mantinedev/mantine](https://github.com/mantinedev/mantine) | 32k | 成品框架 | CSS Modules |
| 7 | [heroui-inc/heroui](https://github.com/heroui-inc/heroui) | 31k | 成品框架 | React Aria + Tailwind v4 |
| 8 | [magicuidesign/magicui](https://github.com/magicuidesign/magicui) | 23.5k | 动效组件（copy-in） | Tailwind + Motion |
| 9 | [react-bootstrap](https://github.com/react-bootstrap/react-bootstrap) | 23k | 成品框架 | Bootstrap |
| 10 | [radix-ui/primitives](https://github.com/radix-ui/primitives) | 20k | 无样式原语 | 无 |
| 11 | [adobe/react-spectrum](https://github.com/adobe/react-spectrum) | 16k | 无样式原语 | 无 |
| 12 | [tamagui/tamagui](https://github.com/tamagui/tamagui) | 14k | 成品框架 | 编译器 |
| 13 | [assistant-ui/assistant-ui](https://github.com/assistant-ui/assistant-ui) | 13k | **AI 对话原语** | 无（primitives） |
| 14 | [mui/base-ui](https://github.com/mui/base-ui) | 11k | 无样式原语 | 无 |
| 15 | [douyinfe/semi-design](https://github.com/douyinfe/semi-design) | 10k | 成品框架 | CSS 变量 |
| 16 | [rsuite/rsuite](https://github.com/rsuite/rsuite) | 8.7k | 成品框架 | Less |
| 17 | [mui/mui-x](https://github.com/mui/mui-x) | 5.9k | 数据组件 | — |
| 18 | [arco-design/arco-design](https://github.com/arco-design/arco-design) | 5.8k | 成品框架 | Less |
| 19 | [ant-design/x](https://github.com/ant-design/x) | 4.9k | AI 对话组件 | CSS-in-JS |
| 20 | [vercel/ai-elements](https://github.com/vercel/ai-elements) | 2.5k | AI 对话组件 | Tailwind |

### 1.2 支撑库（不属于"框架"，但我们要用）

| 仓库 | 星 | 用途 |
|---|---|---|
| [tanstack/table](https://github.com/TanStack/table) | 29k | 表头/排序/列虚拟化 |
| [vercel/ai](https://github.com/vercel/ai) | 28k | AI SDK（流式协议参考） |
| [xtermjs/xterm.js](https://github.com/xtermjs/xterm.js) | 21k | 终端（二期） |
| [pacocoursey/cmdk](https://github.com/pacocoursey/cmdk) | 13k | 命令面板 |
| [codemirror/dev](https://github.com/codemirror/dev) | 7.8k | 编辑器 |
| [tanstack/virtual](https://github.com/TanStack/virtual) | 7.2k | 虚拟列表 |

### 1.3 参照物（不是库，不能直接用）

| 仓库 | 星 | 说明 |
|---|---|---|
| [open-webui/open-webui](https://github.com/open-webui/open-webui) | **159k** | 最接近我们目标形态的**产品**（Svelte + Python），只作交互参照 |
| [gradio-app/gradio](https://github.com/gradio-app/gradio) | 44k | Python 生态，不适用 |
| [chainlit/chainlit](https://github.com/chainlit/chainlit) | 12k | 同上 |
| [huggingface/chat-ui](https://github.com/huggingface/chat-ui) | 11k | 可直接读代码学交互 |

---

## 2. 星数会骗人：用周下载量交叉验证

周下载（npm last-week，2026-11-23）：

| 包 | 周下载 | 星 | 判读 |
|---|---|---|---|
| `@radix-ui/react-dialog` | **101.9M** | 20k | shadcn 生态的实际基座，被海量项目间接依赖 |
| `cmdk` | 51.0M | 13k | 命令面板事实标准 |
| `ai`（Vercel AI SDK） | 48.5M | 28k | 流式协议参考实现 |
| `@tanstack/react-virtual` | 47.3M | 7.2k | 虚拟化事实标准 |
| `@base-ui/react` | **32.2M** | **11k** | **星少但用量巨大**：2026-02 才 1.0，星还没追上 |
| `@mui/material` | 15.0M | 99k | 用量远低于星数暗示 |
| `react-aria-components` | 9.7M | 16k | a11y 最深的流派，用量扎实 |
| `antd` | 5.5M | 100k | 星第一梯队，用量只有 MUI 的 1/3 |
| `@mantine/core` | 3.8M | 32k | — |
| `@assistant-ui/react` | 3.55M | 13k | 新库但已过 300 万/周 |
| `@chakra-ui/react` | 2.36M | 41k | 星多于用量，热度高于使用 |
| `@heroui/react` | 1.08M | 31k | 同上 |
| `@copilotkit/react-core` | 0.79M | 39k | **星/用量差距最大**，慎信星数 |

**三条判读规则**
1. **星数 = 关注度，下载 = 使用量。** `@base-ui/react`（11k 星 / 32M 下载）和 `@copilotkit/react-core`（39k 星 / 0.79M 下载）是两个反方向的反例。
2. **星数高不一定是"框架"**：`open-webui` 159k 星是产品，`tanstack/table` 29k 是被依赖的库，都不能当框架选。
3. **新库的星天然偏低**，别用它判断生死。

---

## 3. 关键事实：没有一个框架能解决我们 70% 的活

| 需求 | 来源 | 框架提供吗 | 需要什么 |
|---|---|---|---|
| 数万条 transcript 虚拟化 + 流式跟随 | session（15,358 行） | ❌ | TanStack Virtual + 会话运行时 |
| 工具卡 / diff / 代码高亮 | toolcards、rightpanel | ❌ | shiki + 自建 diff |
| 编辑器（composer 富文本、文件编辑） | session、rightpanel | ❌ | CodeMirror 6 |
| 可拖拽分栏（侧栏 / 检查器） | uiroot S-02/S-06 | 部分库有 | react-resizable-panels |
| 命令面板 | uiroot S-07 | 部分库有 | cmdk |
| 图表（insights 1,058 行） | insights | 部分库有 | recharts / visx |
| 键盘分层 Esc 仲裁 | 全局（原生版出过 P0） | ❌ | 自建（见 WEB-CLIENT.md §） |

所以选型实质是：**选一个不会跟 token 打架的组件层**，再按面配专用件。

---

## 4. 候选细评

### 4.1 shadcn/ui on Base UI —— 壳层推荐

**事实**：129k 星（第一）；2026-07 起默认基座换成 Base UI（Radix 与 React Aria 仍是一等基座：`init -b radix` / `-b aria`）；无 semver 版本号（"停更"错觉的来源）；registry 已成分发平台；官方 MCP server + llms.txt + skills。

**支持**
1. **视觉主权**：组件是源码，直接改成 token，不用和库的状态色打架。
2. **抗停更**：不是依赖，是仓库里的代码。
3. **AI 工具链最全**：直接对应我们 agent 驱动的开发方式。
4. 需要深度 a11y 的个别组件（树、多选）可单独用 `-b aria` 那套补齐。

**反对 / 纪律**
- Tailwind v4 的 `@theme` 只允许映射 `internal/design` 生成的 token；禁止默认调色板与任意值（`bg-[#...]`、`text-[13px]`），用 `npm run design-check` 扫。
- vendor 进 `web/src/ui/` 后视为自研，升级手动 diff。
- 不提供数据组件。

### 4.2 assistant-ui —— 会话面推荐（本轮最重要发现）

**事实**：13k 星 / 355 万周下载；AGPL/开源；官方定位是 **unstyled, accessible building blocks for AI chat interfaces**；提供 `llms.txt` 面向 agent。

**它提供的原语正好是我们的 session 屏**

| assistant-ui 原语 | 对应我们现有实现 |
|---|---|
| `Thread` | transcript 容器：自动滚动、空态、消息渲染、流式 |
| `Composer` | composer：Enter 提交、附件、空态禁用、流式中状态 |
| `Message` + `MessagePart` | 消息分段渲染（文本/图片/流式 part） |
| `ActionBar` | 复制 / 重试 / 编辑 |
| `BranchPicker` | 重新生成后的多分支切换 |
| `ThreadList` | 会话列表（我们侧栏已有，可只借状态机） |
| `Attachment` | 附件渲染（对应现有附件栈） |
| `ChainOfThought` | **推理/思维链折叠**（对应现有 reasoning 表面） |
| `Suggestion` | slash/@ 建议、starter prompts |
| `SelectionToolbar` | 选中引用的浮动工具条 |
| `Error` | 错误行（`role="alert"`） |

**为什么比自建强**：它把"流式 + 自动滚动 + 键盘 + 空态 + 分支"这些**极易出 bug 的会话状态机**做完了，而这正是我们 15,358 行里最难迁移的部分。

**代价（必须接受）**
- 引入了 **AssistantRuntime** 概念：要用它的 runtime 接口，写一层 adapter 把我们的"快照+差值"喂进去（TRANSPORT.md §6 的 epoch/rev 模型可以直接映射）。
- 它的 API 是 Radix 风格（`Root` / `Part` / `asChild` / `AuiIf`），要按我们的状态语义改写部分默认行为（尤其 `running ≠ warning`）。
- 是第二套组件来源——但它是**无样式**的，不构成第二套视觉语言，前提是样式全部走 token。

### 4.3 Ant Design 6 + Ant Design X 2.0

本体 100k 星（第二），但周下载 5.5M（低于 MUI 的 15M）。Ant Design X 只有 **4.9k 星**——比我上一轮给的权重要低得多，**降级为备选**。数据组件（Table/Tree/Form）仍是强项；若将来"完全等价"放宽为"可以重做视觉"，它是省代码最优解之一。

### 4.4 MUI v9 + MUI X

99k 星 / 15M 周下载。MUI X 的 DataGrid / TreeView / Charts 是重型数据组件里最成熟的。但 Material 视觉与我们的不透明表面 + hairline 分隔风格冲突明显；Emotion 运行时样式在长列表下不划算。**只在需要 DataGrid 级表格时按面引入。**

### 4.5 Mantine v9

32k 星 / 3.8M 周下载。AppShell + Spotlight + Notifications + DataTable 全现成，是"少写代码"最优。但完整视觉语言 → 覆盖战。**若允许重新设计视觉，它是第一选择。**

### 4.6 React Aria（adobe/react-spectrum，16k 星 / 9.7M 下载）

a11y 天花板：虚拟焦点、i18n、RTL、平台差异键盘约定。**作为按组件补位**（树、多选、命令面板），不作为整体方案。

### 4.7 CopilotKit（39k 星 / 0.79M 下载）

星/用量差距最大的一个：38.8 星/千下载。它的定位是"给已有产品加 AI copilot"，偏 agent 集成而非对话 UI 精修。**不推荐**。

### 4.8 裸用 Base UI / Radix

shadcn 已经把它们的组件源码备好了，裸用没有理由。

---

## 5. 依赖清单（定稿）

```
框架          antd 6（cssVar 默认开，建议再开 zeroRuntime + static-style-extract）
              @ant-design/icons 6（仅结构性图标，见 §7）
              @ant-design/x 2.0（会话面，必须包在 web/src/ui/x/ 适配层内）
主题          ConfigProvider + theme.algorithm + seed token（§6 映射表）
数据          @tanstack/react-query（快照+差值缓存） + 少量 zustand
虚拟化        antd 内置 virtual（Table/Select/Tree） + @tanstack/react-virtual（transcript）
编辑器        CodeMirror 6
高亮 / diff   shiki + 自建 diff 视图
分栏          react-resizable-panels
测试          vitest + @testing-library + playwright（对 WEB-CLIENT.md 的 46 条验收）
```

**明确不引入**：MUI、Mantine、Chakra、shadcn、assistant-ui、Tailwind、第二套图标库、任何运行时 CDN。

---

## 6. AntD 主题契约（"完全等价"能否成立全看这张表）

现有 `internal/design` 的档位天生贴合 antd 的 seed token——同一套 4 DIP 栅格、14px 正文、hairline 边框。映射如下：

| 我们的 token | 值 | antd seed / 别名 token | 说明 |
|---|---|---|---|
| `sizeUnit` | 4 | `sizeUnit: 4` | 同源 4 DIP 栅格，默认即是 |
| `TypeBody` | 14 | `fontSize: 14` | 默认即是 |
| `ControlCompactHeight` | 28 | `controlHeightSM: 28` | chips / 图标按钮 |
| `RowCompactHeight` | 32 | （显式覆盖） | 树 / 菜单行 |
| `ControlHeight` | 36 | `controlHeight: 36` | **antd 默认 32，必须改** |
| `PageActionHeight` | 40 | （显式覆盖） | 页面唯一主操作 |
| `RowDoubleHeight` | 44 | `controlHeightLG: 44` | 带元数据的行 |
| `RadiusXS / SM / MD / LG` | 4 / 6 / 8 / 12 | `borderRadiusSM: 4`、`borderRadius: 6`、`borderRadiusLG: 8` | XL 16 走组件级覆盖 |
| `lineWidth` | 1 | `lineWidth: 1` | hairline，默认即是 |
| `Accent` | — | `colorPrimary` | 由 `internal/brand` 派生 |
| `LabelPrimary` | — | `colorTextBase` | |
| 表面 S0–S4 | — | `colorBgBase` + 组件级 `colorBgContainer` / `colorBgElevated` | 暗色用 `darkAlgorithm` |
| `semantic.error / warning / success` | — | `colorError / colorWarning / colorSuccess` | 状态语义不可降级（规则 7） |
| 字体栈 | — | `fontFamily` | 来自 `internal/brand`，不用系统默认栈 |

**两条硬规则**

1. **不用 `compactAlgorithm`。** 我们的 5 个控件高度是**设计过的离散档位**，不是等比缩放；用 compact 会被比例算法改回去。显式赋 `controlHeight*` 三个档位 + 组件级覆盖其余两档。
2. **`token` 里只许出现本表的值。** 生成方式：`npm run tokens` 从 `internal/design` + `internal/brand` 生成 `web/src/design/antd-theme.ts`，**手改即违规**；`npm run design-check` 断言 `theme.token` 的每个键都在白名单内、且源码中无颜色字面量。

---

## 7. AntD 的三条纪律（不遵守就等于放弃"完全等价"）

1. **图标边界**：`@ant-design/icons` 只允许 antd 组件内部的结构性字形（关闭、箭头、排序、勾选、加载）。**业务与导航图标一律用 `internal/icons` 的 34 个 SVG**；禁止 `import * as Icons`；禁止用 antd 图标做按钮语义。实现方式：能用 `icon` prop 的位置一律传我们的 SVG 组件。
2. **Ant Design X 必须包一层**：`@ant-design/x` 仅 4.9k 星、v1→v2 有破坏性变更，是整条链路里生命周期最不确定的一环。所有 X 组件只能通过 `web/src/ui/x/` 的适配层使用，**禁止业务代码直接 import `@ant-design/x`**；这样它被替换时改动收敛在一个目录。同时锁死精确版本（不用 `^`）。
3. **默认视觉必须被清掉**：逐组件用 `components: {}` 校准——去掉 antd 的投影（`boxShadow*` 置 none/极轻）、统一圆角档位、`Tag`/`Badge` 只承担状态语义（禁止中性底 + 状态字拼徽章）、`Modal`/`Drawer` 的关闭按钮与 footer 按钮顺序按我们的规则（取消为默认、危险动作非默认且为 danger）。
   **验收口径**：拿现有 `docs/screenshots` 的 143 张 PNG 逐屏比对，任一处出现"antd 味道"（材料阴影、蓝紫渐变、大圆角卡片、层级过重的分割线）即为未通过。

---

## 8. 收口

- 本文档结论已定稿：**antd 6 + Ant Design X 2.0**。
- `AGENTS.md` §0/§1 的"Radix 仅作无样式行为基元"表述需同步改为 antd（已改）。
- `docs/WEB-CLIENT.md` 的组件库占位按 §5/§6/§7 填入，不再保留"待定"。
- 下一步：Stage 0（契约解耦），见 [stage0-decouple-contracts.md](stage0-decouple-contracts.md)。