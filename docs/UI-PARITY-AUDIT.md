# UI / 交互对齐审计（对照 DSH 0.2.1-rc.2）

方法：DSH 真实实例 + 前端 bundle 源码（Rows.client.js / locale）+ 基线截图；ZeoKeel 侧用无头 `ui.Render` + fake Controller 实拍（`TestCaptureLiveSession` → `artifacts/live/session.png`），逐像素 diff 与控件测试。不做猜测。

## 本轮已修复（有实拍/测试证据）

| # | 问题（DSH 行为） | 修复 |
|---|---|---|
| 1 | 侧栏 Plugins / Automation 是图标行，不是带边框按钮（我们此前 mygo Button 默认边框导致整行按钮） | `navRow` 改为 icon+label 行、无边框、hover/激活背景；渲染验证 |
| 2 | 工作区行 hover 显示「+ 新会话」与「⋮」两个动作（源码 `Rows.client.js` rowActions；aria "在…中新建会话"） | 工作区行新增 + 按钮（hover/选中显示），`CreateSessionInWorkspace` + `SelectWorkspace` + Open；单测 `TestWorkspaceNewSessionAction` 通过 |
| 3 | 会话页头部：左侧为会话标题 + 模式，右侧为动作/边栏按钮；不是 "ZeoKeel + 模式 + idle" | 头部改为真实会话标题 + 模式弹出选择器 + 状态；右侧边栏开关；实拍验证 |
| 4 | Composer：placeholder、+、权限 chip（盾牌）、模型 chip、圆形发送/停止 | 重建 composer：`Placeholder`、盾牌权限 chip、模型 badge、圆形 ↑/■ 按钮；模式选择移到头部；实拍验证 |
| 5 | 静态基线不受影响 | 五屏 masked diff：0.92 / 6.39 / 3.16 / 2.30 / 1.33%，均低于原阈值 |

## 第二批已修复（2026-11-04，均有测试）

| # | 问题 | 修复 |
|---|---|---|
| 6 | 会话行缺状态点/悬停动作 | 增加状态点（运行=accent）、无 hover 时隐藏 ⋯；标题行布局对齐 |
| 7 | 工作区不能展开/折叠 | 点击当前工作区名折叠/展开（▸/⌄），折叠后隐藏会话；`TestSidebarWorkspaceCollapseHidesSessions` 通过 |
| 8 | 首页是静态渲染，不能选工作区/发首条消息 | 新增 live 首页：工作区 chip（无工作区显示 Choose workspace，点击弹出新建）、模式弹出、composer 输入 + 权限/模型/发送；发送即创建会话并提交首条消息；`TestHomeLiveSendCreatesSession` / `TestHomeLiveNoWorkspaceAsksForOne` 通过 |
| 9 | 设置弹层打开时首页控件可被误点（tester 命中重复文本） | live 首页只在 Home 屏渲染，模态背景用静态首页；设置回归测试恢复通过 |

## 待修问题清单（按屏幕，全部已实测确认存在）

### 侧栏
- 会话行缺状态点（运行/空闲/完成/等待审批）与相对时间样式（DSH "1min"；我们 "now"）
- 工作区名点击应为展开/折叠（三角旋转），当前只切换选中；hover card（cwd/创建时间/复制）缺失
- 工作区/会话行动作应仅 hover 显示（我们部分常显）；动作间距、图标尺寸需对齐
- New Session 按钮缺前置 + 圆形图标；Workspaces 头部三个图标间距偏紧
- 会话行右键菜单/置顶/归档/重命名与 DSH 菜单项、快捷键提示不一致

### 首页
- 无工作区状态未实现：DSH 显示 "Choose workspace" chip + 提示 toast，点 composer 先选目录；我们首页仍是静态渲染，点中间不会弹工作区选择
- 首页 composer 未接真实 Controller（不能直接发首条消息创建会话）
- 模式/权限/模型 chip 点击交互未接

### 会话页
- 头部 "◇ Standard mode" 图标、右侧 "⋯" 菜单；空闲时 DSH 不显示 "idle" 文本
- 消息渲染：DSH 支持 Markdown、代码块、文件引用卡片、"Ran commands, read images, called tools" 工具摘要行、每轮 usage、消息操作（复制/重试/反馈）；我们仅纯文本 + 工具卡
- Tool 卡片折叠/展开样式、运行中 spinner 文案动画（已有动画层，但样式/内容需对齐）
- Composer 多行自适应（Shift+Enter 换行），当前单行输入；usage footer 需含 tok/s、cache%、context%
- 审批卡、提问卡、Todo/Team 面板的视觉与位置需与 DSH 对齐

### 插件页 / 设置 / 自动化
- 插件页 Built-in 行、Experimental 分组、搜索/刷新按钮视觉与交互需逐项像素对齐
- 设置子页：Models 卡片、Built-in plugins、Agent presets、Shortcuts、About 布局需对照基线逐页修正
- 自动化空态、任务行、创建/编辑弹层需对照 60-automation 基线

### 全局交互
- 快捷键集合未对齐（DSH 更多：命令面板、侧栏折叠、消息操作等）
- Toast 位置/样式、菜单/弹层进入动画、focus ring、滚动条、拖拽排序未对齐
- 深浅色主题下未逐屏实测基线；Windows 原生标题栏与 DSH Web 客户端差异需确认目标平台

## 下一批修复顺序（每项均先实拍/对照源码，再改，再回归）
1. 侧栏会话行（状态点、时间、hover 动作、展开折叠）
2. 首页 live 化（无工作区选择、首条消息、chip 交互）
3. 会话消息渲染（Markdown/工具摘要/usage/消息操作）+ composer 多行与 usage footer
4. 插件页 → 设置子页 → 自动化页逐屏像素对齐
5. 全局快捷键/弹层/toast/深色主题
