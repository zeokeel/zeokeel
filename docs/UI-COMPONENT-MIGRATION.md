# 官方组件迁移清单（mygo v0.8.3）

> 目标：把 ZeoKeel 中「官方已有标准件却仍自绘/手搓」的 UI 逐项替换为
> `mygo v0.8.3` 官方推荐用法，同时保留有明确理由的自绘项（accessibility /
> 交互语义无法用官方控件表达时）。
>
> 官方源：`/root/go/pkg/mod/github.com/egoist/mygo@v0.8.3/docs/ui/*.md`。
> 规则与组件速查见 [UI-CONFORMANCE.md](UI-CONFORMANCE.md)；已完成的官方化
> 修复证据见 [UI-FIX-STATUS-v0.8.3.md](UI-FIX-STATUS-v0.8.3.md)。

## 1. 现状：已经是官方用法（勿重复迁移）

| 场景 | 当前官方组件 |
|---|---|
| 窗口骨架/分栏 | `ui.Split` + `SplitReset`；`ui.Sidebar`（导航与设置导航） |
| 右侧检查器 | 官方 overlay recipe（`Row + Absolute + Modal + RoleDialog + Esc`） |
| 列表/日志/表格行 | 右栏 Files/Log/Tools/Tasks/Agents、侧栏会话、Home 最近会话、palette、设置搜索命中均为 `ui.List` |
| 大纲/树 | 侧栏工作区树 `ui.Outline` |
| 菜单 | `PopoverMenuButton` / `MenuItem.Danger/Radio/ID/Shortcut/Subtitle`；Files/Tasks 行 `Element.ContextMenu` |
| 输入 | `TextInput`/`TextArea`/`Select`/`SearchField`/`NumberInput`/`Switch`/`Segmented`/`Select` |
| 对话框 | `Modal`/`AlertDialog`/`DialogFooter`（Enter/默认焦点/危险操作） |
| 命令面板 | `SearchField` + `List` + `ActiveDescendant` + `OverlayShortcut` |
| 会话 tab | `ui.TabsBase` |
| Toast | 官方 `c.AddToast`/`ui.ToastBase`/`ToastViewportBase` |
| Markdown/差异 | `markdown.View/Stream`；`editor.DiffView` |
| 终端选择器/滚动 | `ui.Select`；`ui.Scroll` + `ScrollState.TrackScroll`（单文本面） |
| 图标/主题/偏好 | `ui.Icon` + `internal/icons`；`Theme`/`Preferences.TextScale/ReduceMotion` |

## 2. 待迁移（按优先级）

### P1 — 高价值、可测

| 项 | 现状 | 官方目标 | 验收 |
|---|---|---|---|
| 会话 transcript | `ui.Scroll` + 消息循环（有界窗口） | `ui.List`：`ListState{Key: message.ID, Label, FollowEnd, HoldPlace}`，变量行高由 List 测量；加载更早保持滚动锚点 | 流式跟随、返回最新、加载更早位置、折叠不跳动、长会话只构建可见行；session 测试与快照 |
| 轨迹 pane | `ui.Scroll` + tool call 循环 | `ui.List`（Key=tool.ID）；跟随 end 由 ListState | 键盘/滚动/空态测试 |
| 记忆 pane | `ui.Scroll` + memory 循环 | `ui.List`（Key=memory.ID） | 同上 |
| 斜杠/`@` 建议菜单 | 自绘 popover + ButtonBase 行 + 手写 Up/Down/Enter | 官方 suggestion 模式：`ui.List`（`Rows`/`Selected`/`Submitted`/`RoleOption` 等价 a11y）或 `ui.Autocomplete`（纯文本场景不适用富行）；保留 rich row 但键盘交回 List | 键盘上下/Enter/Tab/Esc、a11y role、鼠标选择；既有 suggestion 测试 |
| 帮助对话框命令列表 | `ui.Scroll` + Row 循环 | `ui.List`（只读行，Label） | 文本/键盘滚动测试 |
| 设置技能卡列表 | 页内 Column + 卡片循环 | `ui.List`（Key=skill.Path；卡片为行内容） | 搜索过滤/展开/编辑删除按钮测试 |
| 归档页会话行 | `ui.ButtonBase` 行 | `ui.List`（Selected/Submitted） | 打开/恢复/删除测试 |
| 模型目录思考模式编辑器 | `ui.Scroll` + Row 循环 | `ui.List`（Key=mode.ID） | 增删/编辑/保存测试 |
| 自动化任务卡 / 插件列表 | `ui.Scroll` + 卡片循环 | `ui.List`（Key=task/plugin ID；卡片为行） | 过滤/运行/删除测试 |

### P2 — 语义机会点

| 项 | 现状 | 官方目标 | 验收 |
|---|---|---|---|
| 代理预设工具 allowlist | 逗号分隔 TextInput | `ui.TokenField`（tools 作为 token，可无建议） | 增删 token/保存测试 |
| 上下文占用条 | 自绘进度条/百分比 | `ui.Meter` + `MeterLevels` | 阈值颜色、a11y 测试 |
| 内联重命名（工作区/会话） | 对话框改名 | `ui.EditableText`（列表/页内就地编辑） | 双击/F2/Enter/Esc、保存失败回退 |
| 文件预览里的源码 | 纯 Text | 官方代码展示（`editor.Code`/只读 code-editor，若 v0.8.3 提供只读用法） | 语言/行号/大文件截断测试 |
| 设置页顶部提示/状态 | 自绘 Badge/Text 组合 | 官方 `Badge`/`Alert`/`Progress`/`Spinner` 语义化 | 状态测试 |

### P3 — 自绘但合理保留（记录理由）

| 项 | 保留理由 |
|---|---|
| composer 动作行（Plain Row + 3 个 PopoverMenuButton） | `ui.Toolbar` 的溢出菜单会吞掉发送按钮；官方 Toolbar 不适合「左侧可折行按钮 + 右侧发送」组合 |
| 3 个 `rowTint` 图标按钮（检查器关闭、artifact 标题、任务 ⋯） | `custom-widgets.md` 明确允许 `ButtonBase` 自绘 hover/pressed；这些按钮没有原生外观可套，且已注释 |
| slash/@ 富行建议面板外观 | 行内含图标/标题/说明/多段文本，官方 Autocomplete 只支持纯文本建议；无障碍已按 `RoleListBox/RoleOption/PositionInSet` 实现 |
| transcript 消息内 hover 动作行 | 悬停显示是消息级交互，官方列表行不提供；保留但补键盘可达性与 a11y |
| 会话滚动「回到最新」浮层 | 官方 List `ScrollToEnd` 已用于行为；浮层按钮是 List 之外的必要 affordance |
| 文件/目录选择器 seam 函数 | 纯测试注入点（无应用状态），官方 API 无对应替换 |

## 3. 迁移顺序与验证

1. **P1 列表类**：transcript → 轨迹/记忆 → 建议菜单 → 帮助/技能/归档/模式/自动化/插件。
2. **P2 语义类**：TokenField → Meter → EditableText → 代码展示。
3. 每项：读官方 md + 源码确认 API；改 view + 状态；补/改帧测试；跑
   `go test ./internal/ui/...`、全量 `go test ./...`；必要时更新快照并人工看 PNG。
4. 完成后更新本清单与 `UI-FIX-STATUS-v0.8.3.md` 的「决策记录」。

### 已完成

- 帮助对话框的斜杠命令列表：`ui.Scroll` + Row 循环 → `ui.List`（只读行、`ListState.Label`
  行名、键盘可滚动；新增 RoleList/RoleListItem a11y 断言）。
- 会话「轨迹」pane：`ui.Scroll` + tool card 循环 → `ui.List`（`Key=tool key`、
  行名=tool name、`Gap/PaddingY/Grow`，切换会话重置 ListState）。
