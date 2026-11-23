# Ant Design X 组件映射（按功能拼接）

> 依据：`@ant-design/x@2.10.0` 包元数据与文件树（jsDelivr 实测）、`antd` v6 `llms.txt` 组件清单（2026-11-23）。
> 目标已调整为：**功能等价，视觉跟 antd 走**（不再追求像素级等价于现有桌面端）。

---

## 0. 先回答"X 是否包含 antd"

**不包含。必须两个都装。**

`@ant-design/x@2.10.0` 的 `peerDependencies`：

```json
{ "antd": "^6.1.1", "react": ">=18.0.0", "react-dom": ">=18.0.0" }
```

X 是**建立在 antd 之上**的对话层，不是 antd 的集合包。而且两者分工很明确：

| | 覆盖范围 |
|---|---|
| `@ant-design/x` | **只有对话面**：17 个组件（见 §2） |
| `antd` | **壳层与数据面**：Layout / Menu / Modal / Drawer / Table / Tree / Form / Upload / Tabs / Splitter / Tabs / Popover / Tag / Typography… |

安装清单：

```bash
npm i antd@6 @ant-design/x@2.10.0 @ant-design/icons@6
```

- `@ant-design/icons@6` 必须与 antd 6 配对（v5 的图标包不兼容）。
- **X 已经自带** `mermaid@11` 与 `react-syntax-highlighter@16`（间接依赖）——`code-highlighter` 与 `mermaid` 组件开箱可用，**不必再装 shiki 做基础高亮**。
- 版本锁精确（不用 `^`），尤其 X：v1→v2 有破坏性变更。

---

## 1. 依赖体积实况（提前知道）

X 的直接依赖里较重的：`mermaid`、`react-syntax-highlighter`、`@ant-design/cssinjs`、`@rc-component/*`。mermaid 本身是重库，若不渲染图表可考虑用 X 的 `mermaid` 组件的懒加载入口，避免进首屏。

---

## 2. Ant Design X 2.10.0 真实组件清单（17 个）

| 组件 | 用途 | 我们要用它做什么 |
|---|---|---|
| `Bubble` / `BubbleList` | 消息气泡与列表 | **transcript 主体** |
| `Sender` | 输入框（含发送/停止/语音/附件/Skill） | **composer** |
| `Conversations` | 会话列表与分组、新建 | **侧栏会话树** |
| `ThoughtChain` | 思维链（Item/Node/Status） | **推理展示**（含工具步骤） |
| `Think` | 思考中状态 | 推理进行态 |
| `Attachments` / `FileCard` | 附件上传与卡片 | **附件栈** |
| `CodeHighlighter` | 代码块高亮 | 消息内代码、工具卡代码 |
| `Mermaid` | 图表渲染 | 计划/架构图（按需） |
| `Sources` | 引用来源 | 检索结果引用 |
| `Prompts` / `Suggestion` | 推荐提示词 | 空态引导、slash 建议 |
| `Welcome` | 欢迎/空态 | Home 屏 |
| `Actions` | 消息操作组 | 复制/重试/编辑 |
| `Notification` | 通知 | 运行完成/失败提示 |
| `Folder` | 文件目录展示 | 右检查器文件页 |
| `XProvider` | 主题/国际化/上下文 | 顶层 Provider |

**X 没有的**：虚拟滚动列表、审批卡、工具卡、计划/提问浮层、分支切换、diff 视图、终端、命令面板、文件树（`Folder` 是展示件，不是可拖动树）。

---

## 3. 按功能映射（我们的屏面 → 组件）

### 3.1 壳层

| 我们的面 | 组件 | 备注 |
|---|---|---|
| 根布局 S-01 | `Layout` + **`Splitter`**（v6 新增） | 可拖拽分栏用它，替掉 react-resizable-panels |
| 侧栏 S-02 会话树 | `Conversations` + `Menu` | X 管会话列表，antd `Menu` 管导航 |
| 折叠栏 S-03（64px） | `Layout.Sider collapsedWidth` | 原生支持 |
| 顶带 S-04（48 标题 + 36 Tabs） | `Flex` + `Typography.Title` + `Tabs` | |
| 内容列 S-05 | `Layout.Content` | |
| 右检查器 S-06 | `Drawer`（v6 可调宽） | 用 Drawer 而非 Splitter，窄窗天然降级 |
| 命令面板 S-07 | `Modal` + `Input` + `List` | **antd 无命令面板**，需自拼；键盘上下选择自己写 |
| 设置 S-08 | `Modal` + 左 `Menu` + 右 `Form` | 9 页 |
| 浮层 S-09 | `Modal` / `Popover` / `Drawer` | |
| Toast S-10 | `App.useApp()` 的 `message` / `notification` | 或 X `Notification` |
| 文件拖入 S-11 | `Upload.Dragger` | **仍然拿不到绝对路径**，需宿主代理上传 |
| 导航 S-12 | react-router | |

### 3.2 会话屏（session）

| 我们的面 | 组件 |
|---|---|
| transcript | `BubbleList`（**虚拟化要自己加**，见 §4） |
| 流式输出 | `Bubble` 的 loading/typing 机制 |
| composer | `Sender`（含停止、附件、语音位） |
| slash / @ 建议 | `Suggestion`（输入触发仍要自己接） |
| 推理 / 工具步骤 | `ThoughtChain`（`Item` + `Status`） |
| 工具卡 | **自建**（X 无），内嵌 `CodeHighlighter` / `Collapse` |
| 审批卡 | **自建**（`Card` + `Space` + `Button`，danger 语义） |
| 附件 | `Attachments` + `FileCard` |
| 代码块 | `CodeHighlighter` |
| 消息操作 | `Actions` |
| 子 Tab（对话/轨迹/记忆/上下文） | `Tabs` |
| 空态 | `Welcome` |

### 3.3 其余屏

| 屏 | 组件 |
|---|---|
| Home | `Welcome` + `Sender` + `Prompts` |
| Automation | `Table`（virtual）或 `List` |
| 能力页（3 列卡片） | `Card` + `Row/Col`，或 v6 新增的 `Masonry` |
| Insights | **antd 无图表** → 需 `@ant-design/charts` 或 recharts |
| 设置 9 页 | `Form` + `Switch` + `Select` + `InputNumber` + `Tabs` |
| 右检查器 · 文件 | `Tree`（virtual）+ `Folder` 展示件 |
| 右检查器 · diff | **自建**（无内置 diff） |
| 终端（若恢复） | **自建**（xterm.js） |

---

## 4. 五个必须自建的产品特有表面

这些是我们的产品价值所在，任何框架都不给：

1. **审批卡 + 审批收件箱（Inbox）**——四档语义（直接做/预先批准/先问/交还给你）、无默认动作、Esc=拒绝、过期机制。
2. **工具卡**——26 个工具渲染器，需要按工具名分发的注册表。
3. **计划 / 提问 / 产物浮层**——已有的 3 类 interaction 表面。
4. **transcript 虚拟化 + 流式跟随**——`BubbleList` 不带虚拟滚动，长会话必须自己加窗口化与"用户上滚后不再自动跟随"。
5. **分层 Esc 仲裁器**——`浮层 > 脏守卫 > 查找 > 子会话回父 > 拒绝审批 > 停止运行 > 清空草稿`，单一 capture 监听 + 声明式栈，不许各组件各自绑 keydown。

---

## 5. 主题：从"覆盖战"降为三行

既然不再追求像素级等价，主题只需要保持"不像通用后台"：

```ts
<ConfigProvider theme={{
  algorithm: dark ? theme.darkAlgorithm : theme.defaultAlgorithm,
  token: { colorPrimary: <Accent>, fontFamily: <brand 字体栈> },
}}>
```

**可选**（想要更接近现在的密度再开）：`controlHeight: 36`、`borderRadius: 6`、`fontSize: 14`。
**不要**用 `compactAlgorithm`（会把档位按比例改回去）。

---

## 6. 与框架无关、必须保留的行为契约

换框架不能把这些一起丢掉：

| 契约 | 为什么不能丢 |
|---|---|
| 浏览器保留键不可抢（Cmd+N/T/W、Tab） | Tab 兼"接受建议"会破坏可访问性 |
| 分层 Esc 仲裁 | 原生版曾因"Esc 拒绝审批"静默失效出过 P0 |
| 审批无默认动作 | 安全默认：不允许回车即批准 |
| running ≠ warning | 进行中用 Accent，只有 needs-approval 用 warning |
| 图标只用 `internal/icons` 的 34 个 SVG | 业务图标不混用 antd 图标 |
| 文件拖入走宿主代理 | 浏览器拿不到绝对路径 |

以上六条应写进 `docs/WEB-CLIENT.md` 并落到验收清单，与组件选型解耦。