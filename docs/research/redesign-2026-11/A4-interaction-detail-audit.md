# A4 交互细节审计（2026-11-18，interaction-audit）

> **审计对象**：ZeoKeel `/data/mygo`，代码基线 `e9a0d6b`（`ci: drop the goproxy.cn mirror override`）**加工作区未提交改动**（本轮并行改动：`internal/design/motion.go`、`internal/uikit/kbd.go`、`internal/brand/{brand,palette}.go`、`internal/design/{tokens,components,registry}.go`、`internal/uikit/iconbutton*.go` 等；见 §5.0）。
> **任务**：按 BRIEF §1（用户原话：每个按钮、每个点击、每个悬浮都要仔细检查）与 AGENTS.md §3 十七条，把交互面拆成"用户能做的每一个动作"逐项审计，交付 `A4-interaction-detail-audit.md` + `A4-findings.tsv`。
> **只读边界**：未修改任何产品代码/产品测试/配置；唯一仓库写入是本报告、`A4-findings.tsv`、`.cache/ui-drive-audit/`（截图与驱动报告）。临时探针测试全部写在新包 `internal/uitest_scratch/`，**审计结束后已删除**（源码逐字收录在 §5.3，可复现）。
> **方法**：源码逐行（`internal/uiroot`、`internal/uikit`、`internal/ui/*`）+ 现有帧测试清单 + **14 组自写运行时探针（A4-P1…P14）**（`ui.NewTester`，真 desktop assembly headless）+ `go run ./internal/devtools/ui-drive --out .cache/ui-drive-audit`（84 步，84 PASS）+ WCAG 对比度计算（§5.4）。

---

## 0. 结论摘要（≤10 条，按影响排序）

1. **`Tooltip` 会吞掉第一次 Esc——但只对"不挂在焦点祖先链上的 Esc 拥有者"**（2026-11-18 运行时复核后**收窄**）：mygo 的 `routeKey`（`ui/input.go:1333-1357`）先给**聚焦元素祖先链**上的普通 shortcut，再给 overlay；tooltip 是 overlay（`ui/tooltip.go:91`）。所以：
   - **composer 内安全**（屏幕级 Esc 在焦点祖先链上）：悬浮/聚焦停止控件后 Esc 一次生效（A4-QA 三例 PASS）；
   - **窗口级兜底失效**：焦点在侧栏/顶带带提示控件上时，「Esc 拒绝审批」「Esc 停止」被吃掉一次（基线 5/5 稳定复现）；
   - **Modal 层失效**：设置里焦点在任一带提示控件（如「关闭设置」）时，第一次 Esc 不关设置（基线稳定复现）。
   这违反 AP-24「Esc 必须在窗口级生效（不依赖焦点位置）」。修法是**把 Esc 注册到聚焦内容的祖先上**（根 Row / 设置内容列 / 对话框内容列），不是删提示——删 composer 的停止/排队/发送提示解决不了这两类。证据：A4-P1/P13 + A4-QA `esc-chrome-tip-deny|stop`、`esc-modal-tip-close`（基线 EXPECTED-FAIL）与 `esc-composer-*`（PASS）。
2. **`uikit.ConfirmDanger` 关闭后不归还键盘焦点**（2026-11-18 复核后**收窄**）—— 18 个调用点（删除/清空/恢复类确认）关闭瞬间 `Focused()==""`：Esc 与 Enter 两条路径都丢。**设置面与命令面板不受影响**（实测：Esc/执行命令后焦点回到打开它的控件，见 A4-QA `focus-return-settings`、`palette-*-restores-focus`）。根因是"用面板内按钮的 OnClick 去清 open 标志"这条关闭路径不走 mygo overlay 的归还逻辑（裸 `ui.Modal` 用 Enter 激活按钮关闭时同样丢，焦点停在已消失的按钮上）。
3. **拖放无落点视觉** —— 产品只挂了窗口级 `OnFileDrop`（`session/session.go:629-643`），未使用 mygo 的 `Element.FileDragOver()/DroppedFiles()`（`mygo/docs/ui/input.md:483-499`）：拖文件进窗口全程没有任何"可放下"高亮，松手后才在 composer 里出现附件。
4. **「新对话」双击 = 两个会话** —— `chat.NewSession` 无防抖/幂等（`sidebar/sidebar.go:412-417`），一次双击创建两个空会话。探针 A4-P8。
5. **禁用态的解释多半不可见** —— 产品用 `Tooltip` 解释禁用控件（goal 继续、连接器添加、数据目录打开/复制、更新页复制链接），但**禁用控件不显示 tooltip**（探针 A4-P5），解释永不出现；唯一正确写法（把提示挂在包裹 Row 上 + `Description`）只出现在 `context_pane.go:230-238`。
6. **命令面板能记住的键盘只有 ↑↓/Enter/Esc** —— Home/End/PageUp/PageDown 无效（探针 A4-P7），多词查询（"设置 主题"）直接"没有匹配的命令"（探针 A4-P6，`frame.go:1335` 单串 `Contains`），与"覆盖全部动作"的定位不相称。
7. **快捷键可发现性两处漂移** —— `/help` 的键盘表（`commands_help.go:53-66`）与 设置→全部快捷键（`settings.go:84-92`）内容不同，且**都没有** `Esc = 停止生成`、`Cmd+.`、`Cmd+Alt+P`、`@`、`Tab`；`uikit.Kbd` 键帽组件刚落地但**零采用**（`internal/uikit/kbd.go` 无调用点），快捷键仍是纯文本。
8. **选择态契约漂移（部分已修）** —— 实现是 `RowSelected 亮 9% / 暗 13%`（`brand/palette.go:165/224`）；审计期间 AGENTS §3.11 与 DESIGN §5.1（:492）已同步为 9%/13%，但 **DESIGN.md:187、:289（AP-19）、:609（§6.1 预设表）仍是 12%/18%**（AGENTS §6 同步义务）。
9. **滚动条零 hover 反馈** —— `brand.go:118-128` 注释承诺「hover 变亮、拖拽变粗」，实际 `ScrollbarStyle` 未设 `Hover/Active` 且 `TrackWidth==ThumbWidth`（10/10），既不亮也不粗；thumb 对比度仅 1.62:1（亮）/2.13:1（暗）。
10. **检查器文件页"打开即离开列表"** —— 点树行 → `openFile` 把 page tab 切到文件页签（`rightpanel.go:282-302`），文件树消失，唯一的返回方式是再点一次「文件」页面 tab；页面 tab 仍高亮「文件」，状态含糊；同时树行点击走 `ObserveInput`，**不落到 Outline 的选中位**（探针 A4-P11/P14），键盘无法从刚点过的行继续。

**干净区（本轮实测通过，不要再"修"）**：审批内联卡无 Default / Esc=拒绝 / 窗口级兜底 + 统一 `session.escape` 裁决（`view.go:392-441`，`shortcuts.go:235-274`，`escape_fallback_test.go` 6 例）；危险确认取消为 Default（探针 A4-P12：焦点落在「取消」、Enter 取消）；计划/提问安全默认；流式跟随 + 回到最新（`uikit/session.go:180-190`，`scroll_follow_test.go`）；停止中间态 `Stopping`（`composer.go:365-375`）；队列逐条取消 + 清空确认（`runstatus.go:213-232`）；焦点环对比（4.38–4.59 亮 / 6.18–6.70 暗，§5.4）；hover 与选中可区分（中性 4.5% vs brand 9–13%）；行内动作 hover 出现但键盘 `FocusWithin` 可达（`sidebar.go:1453`）；toast 悬停暂停 + Tab 可达 + Esc 关闭 + 上限 2 条（`toasts.go:55`，mygo toast.md）；`ui-drive` 84/84 PASS。

---

## 1. 现状（证据）

### 1.1 交互面的实际形态（本轮核实，与历史文档的差异已标注）

| 面 | 现状 | 证据 |
|---|---|---|
| 壳层导航 | 280 DIP 展开侧栏（可拖宽）↔ 64 DIP rail；侧栏顶部 brand 行（折叠按钮）、`新对话`、目的地列表、工作区树、footer `设置` | `uiroot/frame.go:161-222`、`:236-353` |
| 顶带 B3 | 只有屏幕有贡献时才出现：标题 + Run Band（团队/子代理/后台任务/待审批）+ 屏幕动作 + `⋯ 会话操作` + `快捷操作`（放大镜）+ `打开右侧栏`；下面一行是屏幕 tabs（对话/轨迹/记忆/上下文） | `frame.go:806-890`，探针 A4-P9 的 Tab 顺序 |
| 会话 | 空会话显示 hero（示例提示 ×3 + 首次运行清单）；有会话才有 composer；composer dock 在输入卡上方（目标条/审批卡/todo） | `view.go:160-215`、`:1163` |
| 右栏 | 两个固定页（开始/文件）+ 每个打开的文件一个可关闭页签；窄窗自动 rail、更窄转 modal drawer | `rightpanel/rightpanel.go:57-63`、`:282-302`；`uiroot/frame.go:945-997` |
| 浮层 | 命令面板（`ui.Modal` + 搜索 + `ui.List`）、设置面（Modal + 原生 Sidebar 导航 + 固定页脚）、检查器 drawer、模块 overlay slots | `frame.go:1023-1244`、`:1435-1465`、`:1870-2038` |
| 通知 | 原生 `c.AddToast`，右上（顶带下 8 DIP）→ 有浮层时让到右下；最多 2 条；同文合并 ×N | `uiroot/toastanchor.go:79-108`、`toasts/toasts.go:280-340` |

### 1.2 键盘模型现状（实测 Tab 顺序，会话屏 + 一个会话，1280×800）

探针 A4-P9 用无障碍树读出真实焦点序列（截取一轮）：

```
 0 折叠侧边栏        1 主导航(树)        2 新对话          3 添加工作区
 4 搜索会话与消息     5 设置(footer)      6 Divider(分隔条)  7 智能体团队
 8 子智能体 0         9 快捷操作         10 打开右侧栏      11 对话(tab)
12 开始输入           13-15 示例提示      16 配置 API Key   17 添加工作区
22 输入消息…(TextArea) 23 附件(toolbar)   —(左/右: probe-model, 工作区修改（全局）)
24 → 回到 0 循环
```

- **顺序符合 BRIEF 期望**（侧栏 → 顶带 → 内容 → composer → 右栏），但 6 号是**分隔条 `Divider`**：可聚焦、可 `←/→` 调宽（合规），但**无障碍名是平台默认英文 "Divider"**，产品在 `ui.Split(...).Label("侧栏宽度可调")` 上设的名字没有落到分隔条上（i18n/a11y 细节，见 A4-I-15 附条）。
- composer 内是**两级**：TextArea → 工具栏（附件/模型/权限，一个 Tab stop，`←/→` 在内部移动，探针 A4-P10）→ 发送/排队/停止（`composerShellActions`，`composer.go:325-410`）。空草稿时发送禁用 → **不在 Tab 顺序里**（正确），但也没有任何"为什么不能发"的解释（见 A4-I-05/06 同类）。
- Esc 分层（代码 + 规格一致，已验证）：建议菜单 → 子会话 → 待审批(拒绝) → 会话对话框(让位给 overlay) → 建议菜单(再次) → 停止本轮（`view.go:392-441`）；窗口级兜底在焦点离开会话屏时接管并**委托** `session.escape` 命令（`shortcuts.go:235-274`）。框架事实（探针 A4-P13）：**屏幕作用域的 `OnShortcut(0, Esc)` 先于聚焦输入框的 `OnTextKey`**（Esc 不是输入框"编辑用"的键），Enter 则先给输入框——所以 composer 的 Esc 分支在屏幕注册了 Esc 的状态下不会执行，实际裁决者是 `sessionEscape`（旧审计 UX-01 的"输入框抢先"前提不成立；本次已实测更正）。
- **打破 Esc 承诺的唯一因素是 Tooltip**（§0-1）：焦点/悬浮抬起的提示是 overlay，最先生吃 Esc。

### 1.3 悬浮 / 按下 / 选中 / 禁用 / 加载 现状

| 类 | 现状 | 证据 |
|---|---|---|
| 图标按钮 | Ghost/Subtle/Danger/Native 四变体；hover=BgHover、pressed/selected=BgSelected、焦点环走主题；≥24 命中区；**新加颜色过渡 MotionFast** | `uikit/iconbutton.go:114-194`、`:130-143`（工作区改动） |
| 列表/树行 | 原生 hover（`SurfaceHover`）+ 原生选中（Accent 填充 + AccentText）；自绘富行用 BgSelected + 2 DIP Accent 左条 | mygo `ui/list.go:832-876`；`sidebar/sidebar.go:1698-1730` |
| 行内动作显隐 | 会话/工作区行的 `⋯`/`+` 默认 `Opacity(0)+IgnoreInput`，`current`、`row.Hovered()`、`row.FocusWithin()` 任一为真时显示（键盘可达） | `sidebar/sidebar.go:1453`、`:1555`、`:1615` |
| 禁用 | 半透明 + 不取焦点/点击；**解释多半挂错位置**（见 A4-I-05） | `iconbutton.go:151-153`；探针 A4-P5 |
| 加载 | 全部用 `Spinner + 文案`（无骨架屏）；按钮型加载改写 label（`选择中…`/`停止中…`/`发送中…`） | `view.go:2969-2978`、`composer.go:365-375`、`attachments.go:363` |
| 选中 vs hover | 亮色 hover=中性墨 4.5% / selected=品牌蓝 9%（暗色 白 6% / 青 13%）→ 色相不同，可区分（§5.4 对比 1.05 亮度比，靠色相） | `brand/palette.go:158-166`、`:220-225` |

### 1.4 滚动 / 流式 / 长任务 现状

| 项 | 现状 | 证据 |
|---|---|---|
| 跟随 | `ListState.FollowEnd` + `HoldPlace`；上滚即停跟随并浮出「回到最新」（有测试） | `uikit/session.go:95-190`；`scroll_follow_test.go`；`uikit/session_test.go:150-230` |
| 主区/右栏 | 各自独立 Scroll（Split 两 pane） | `uiroot/frame.go:562-640` |
| 滚动条 | 全局 10 DIP thumb、无 track、**hover/拖拽无视觉变化** | `brand/brand.go:118-128`；mygo `ui/scrollbar.go:57-80`、`:269-279`；§5.4 |
| 停止 | 空闲→`停止`；请求后 `停止中…` + Disabled（防重复触发） | `composer.go:365-375`；`view.go:3300` |
| 排队 | 运行中 Enter 排队（提示行明示）；队列徽章 + popover：逐条取消/清空（ConfirmDanger）/暂停说明/失败重试 | `composer.go:296-330`；`runstatus.go:200-330` |
| 工具运行/失败/取消 | 六态词表 + `StatusBadge`；工具行折叠展开 `CollapsibleBase`；失败展开显示错误与恢复 | `uikit/toolrow.go:240-340`；`status.go` |
| 恢复会话 | 冷恢复横幅 + 「继续」聚焦 composer + toast + Announce；空草稿时主操作禁用 | `interrupted.go:60-70`；`interrupted_test.go` |

### 1.5 拖放 / 复制 / 菜单 / 对话框 现状

| 项 | 现状 | 证据 |
|---|---|---|
| 拖放（外部文件） | 窗口级 handler 暂存到 composer；跨屏拖入有 toast+「前往会话」；**无落点高亮** | `session/session.go:629-643`；`attachments.go:284-306`；grep 无 `FileDragOver`/`DroppedFiles` |
| 粘贴 | 图片粘贴转附件；文本粘贴走原生 | `composer.go:196-207`；`attachments.go` |
| 代码块复制 | 原生 `b.CopyButton`（图标切换为"已复制"） | `uikit/markdown_code.go:43-46` |
| 消息复制 | `⋯ → 复制消息/复制原文` + **toast「已复制」** | `view.go:2096-2101`、`:2182-2188` |
| 设置复制 | `Badge「已复制」` | `settings/settings.go:639`、`data.go:171`、`update.go:334-338` |
| 菜单 | 全部 `PopoverMenuButton`（窗口内绘制）：Up/Down/Home/End/字母跳转/←→ 子菜单/Enter/Esc，焦点归还给按钮 | mygo `docs/ui/menu-button.md`；`?Danger()` 12 处一致 |
| 对话框 | Modal + `uikit.DialogFooter`（危险=取消 Default）；Enter 默认动作；Esc 关闭；背景滚动锁定（Modal）；焦点**不归还**（A4-I-02） | `uikit/uikit.go:327-390`；探针 A4-P4 |

---

## 1.6 交互矩阵（动作 × 当前行为 × 期望行为 × 证据）

> 判定：✅ 合规（有实现+有证据）｜⚠ 可用但有缺陷（见发现编号）｜❌ 缺失
> 证据里的 `A4-Pn` = 本报告 §5.3 的探针；`file:line` = 产品源码；`test:` = 现有帧测试名。

### 1.6.1 键盘

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| K1 | Tab 走完整个窗口 | 侧栏(brand→nav 树→新对话→工作区→搜索→设置)→分隔条→顶带(团队/子代理/快捷操作/右侧栏)→屏幕 tabs→内容→composer→回绕；右栏收进 pane 时排在其后 | 同一顺序；分隔条不应是可 Tab 的一站（或应有本地化名称） | A4-P9；`frame.go:161-222`、`:806-890`；`uiroot` Split | ⚠（分隔条名 "Divider"） |
| K2 | composer 内 Tab | TextArea → 工具栏（单 stop）→ 发送/排队/停止 | 同上；发送禁用时跳过（现状正确） | A4-P10；`composer.go:325-410` | ✅ |
| K3 | 工具栏内左右移动 | `←/→` 在 附件 → 模型 → 权限 间移动，`↓/Enter` 开菜单 | 同上 | A4-P10；mygo `docs/ui/toolbar.md` | ✅ |
| K4 | Enter 发送 | 空闲 Enter/Cmd+Enter 发送；Shift/Alt+Enter 换行；运行中 Enter 排队（排队关时换行） | 同上 | `composer.go:290-322`；`test:TestComposerEnterSends` | ✅ |
| K5 | Esc 关闭建议菜单 | 菜单优先；draft 保留；再按才停止 | 同上 | `view.go:3373-3385`；`test:TestEscCancelsRunningTurnMenuFirst` | ✅ |
| K6 | Esc 停止生成（焦点在输入框） | 屏幕作用域 Esc → `sessionEscape` → 停止；提示行写「Esc 停止」 | 一次按键即停止 | A4-P13；`view.go:392-441`、`:3517` | ✅ |
| K7 | Esc 停止生成（焦点/悬浮在任意带提示控件） | **第一次 Esc 只关提示**，第二次才停止 | 一次按键即停止 | **A4-P1/P2/P3**；`uikit/composer.go:369`；`mygo/ui/tooltip.go:91` | ❌ A4-I-01 |
| K8 | Esc 拒绝审批（焦点在顶带/侧栏） | 窗口级兜底 → `session.escape` → 拒绝最早待审批 | 同上，且不被提示吞掉 | `shortcuts.go:235-274`；`escape_fallback_test.go` 6 例 | ⚠（同 A4-I-01） |
| K9 | Esc 清焦点/退出子会话 | 无"Esc 清焦点"概念；子会话 Esc 回父会话 | 子会话回父（现状即期望）；不需要清焦点 | `view.go:399-406`；`test:TestEscE2EChildFromSidebarReturnsToParent` | ✅ |
| K10 | Esc 关 Modal | overlay 先吃；审批=拒绝、计划审阅不取消、ask=取消、会话对话框自行关闭 | 同上 | `uikit/{approval,planreview,ask}.go`；`uimodtest` E2E | ✅ |
| K11 | Esc 关查找条（运行中） | `sessionEscape` 第一步关条，不停 run | 同上 | `view.go:404-409`；`test:TestFindBarEscapeDoesNotStopRunningTurn` | ✅ |
| K12 | Cmd/Ctrl+N 新会话 | 新建 + 跳会话屏；**双击按钮会建 2 个** | 新建一次（幂等/防抖） | A4-P8；`shortcuts.go:106-139`；`sidebar.go:412-417` | ❌ A4-I-04 |
| K13 | Cmd/Ctrl+, 设置 | 打开设置面 | 同上 | `shortcuts.go:142-150` | ✅ |
| K14 | Cmd/Ctrl+K 命令面板 | 打开/关闭；设置内可用（浮在上层）；Esc 先关面板 | 同上 | `frame.go:1023-1244`；`test:TestPaletteEscapeClosesOnFirstPress` | ✅ |
| K15 | 命令面板 ↑↓ | 移动选择并滚动跟随 | 同上 + Home/End/PageUp/Down | `frame.go:1061-1062`；**A4-P7（Home 无效）** | ❌ A4-I-07 |
| K16 | 命令面板查询 | 单串 `Contains`（title/ID/hint/keywords） | 多词 AND（"设置 主题" 应命中） | `frame.go:1335-1344`；**A4-P6** | ❌ A4-I-07 |
| K17 | Cmd/Ctrl+Alt+I 右侧栏 | 开/关；窄窗 drawer 关闭后焦点还给触发按钮 | 同上 | `shortcuts.go:309-317`；`frame.go:886` | ✅ |
| K18 | Cmd/Ctrl+Alt+P 文件页 | 打开 inspector 文件页 | 同上 | `shortcuts.go:322` | ✅ |
| K19 | Cmd/Ctrl+F 查找 | 打开 FindBar；非对话 tab 先切回 | 同上 | `view.go:5303-5320` | ✅ |
| K20 | Cmd+. 中断 | 直接中断（不碰菜单） | 同上，且应在帮助里可发现 | `view.go:462-467`；`commands_help.go`（缺此条） | ⚠ A4-I-08 |
| K21 | Cmd/Ctrl+B 折叠侧栏 | 切换 rail；长按不连发（`ShortcutRepeated` 抑制） | 同上 | `shortcuts.go:281-300` | ✅ |
| K22 | `/` `@` 建议菜单 | 打开、↑↓、Tab/Enter 接受、Esc 关闭且保留 draft | 同上 | `view.go:4686-4700`、`:4990-5010`；`test:TestSlashMenuKeyboard` | ✅ |
| K23 | 建议菜单溢出 | 固定上限 8 条，无"还有 N 条"提示 | 显示截断提示或可滚动继续 | `view.go:27-28`、`:4937` | ⚠ A4-I-19 |
| K24 | 菜单键盘 | Up/Down/Home/End/字母跳转/←→ 子菜单/Enter/Space/Esc/Tab；关闭后焦点回按钮 | 同上 | mygo `docs/ui/menu-button.md` | ✅ |
| K25 | 快捷键可发现性 | `/help` 与 设置→快捷键 两处清单不一致，均缺 Esc=停止、Cmd+.、Cmd+Alt+P、@、Tab；键帽组件 `uikit.Kbd` 建而未用 | 唯一一份完整清单 + 键帽样式 + composer 提示带 `Esc` 键帽 | `session/commands_help.go:53-66`；`settings/settings.go:84-92`；`uikit/kbd.go`（0 调用） | ⚠ A4-I-08/I-14 |
| K26 | 命令面板覆盖全部动作 | 注册命令 + 生成"前往 X"/"设置：X"；禁用命令被隐藏（不显示灰项） | 覆盖全部可执行动作；可发现"存在但当前不可用" | `frame.go:1263-1331` | ⚠（隐藏禁用项，可接受） |

### 1.6.2 焦点

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| F1 | 焦点环可见性（亮/暗） | 2 DIP 环 + 2 DIP 外扩；亮 4.38–4.59:1、暗 6.18–6.70:1 | ≥3:1 | `brand/brand.go:115-117`；§5.4 计算 | ✅ |
| F2 | 焦点环被裁切 | 原生件环画在自身内缩 3 DIP；外扩环在 `Clip()` 容器（代码块、圆角卡）内可能被裁 | 任何可聚焦元素四周完整可见 | mygo `ui/list.go:877-884`；`uikit/markdown_code.go:35` | ⚠（未逐点验证，列入 §5.5 限制） |
| F3 | 输入框聚焦态 | composer 卡：focus=1 DIP Accent 边、hover=BorderL3；TextArea 自身无边框 | 清晰可辨 | `uikit/composer.go:274-280` | ✅ |
| F4 | 列表/树行焦点 | 原生行焦点环 + 选中填充 | 同上 | mygo `ui/list.go:877-884` | ✅ |
| F5 | 弹层打开时焦点陷阱 | Modal 把焦点移入并锁住（Tab 循环在面板内） | 同上 | mygo `docs/ui/overlays.md:120-122`；A4-P4 | ✅ |
| F6 | 弹层关闭后焦点归还 | **丢失**（ConfirmDanger/设置/命令面板）；裸 Modal 仅 Esc 路径归还 | 归还原触发控件（否则归还可预测的落点） | **A4-P4**；`uikit/uikit.go:347-390`；`frame.go:1435-1453`、`:1414-1432` | ❌ A4-I-02 |
| F7 | 对话框默认焦点 | ConfirmDanger：焦点在「取消」（安全）；审批 Modal（死路径）：焦点在「拒绝」 | 危险确认=取消；审批=无默认 | A4-P12；`uikit/uikit.go:363-390`；`uikit/approval.go:311-380` | ⚠（审批 Modal 路径 A4-I-12） |
| F8 | composer 重聚焦不跳滚动 | 聚焦只滚到可见（mygo 一次性），发送后 `focusComposerAgain` | 同上 | `composer.go:330-340`；`test:TestComposerRefocusKeepsScroll` | ✅ |
| F9 | 行内动作键盘可达 | `FocusWithin` 揭示后进入 Tab 序 | 同上 | `sidebar.go:1453` | ✅ |
| F10 | 拖出/关闭窗后焦点 | 关闭 inspector 时焦点回 toggle（`inspectorFocusPending`） | 同类场景统一（对话/面板/错误条） | `frame.go:880-888` | ✅（仅此一处） |

### 1.6.3 悬浮

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| H1 | 图标按钮 hover | 背景 BgHover + 颜色过渡 80ms | 同上 | `iconbutton.go:158-172`、`:137-143` | ✅ |
| H2 | 列表/树行 hover | 原生 SurfaceHover（tint） | 同上 | mygo `ui/list.go:872-876` | ✅ |
| H3 | 富行 hover vs selected | hover 中性墨 4.5%，selected 品牌蓝 9%（暗 白 6% / 青 13%） | 可区分（色相不同） | `brand/palette.go:158-166` | ✅ |
| H4 | 行内动作只在 hover 出现 | 会话/工作区行 `⋯`/`+` 默认透明且不吃输入，`current/hover/FocusWithin` 显示 | 键盘/触屏可达 | `sidebar.go:1453`、`:1555` | ✅ |
| H5 | 文件树行 hover | 原生行 hover（Outline = List） | 同上 | mygo `ui/list.go:834`、`:872` | ✅ |
| H6 | 工具栏控件 hover | 无面色直到 hover/pressed/focused | 同上 | `uikit/toolbar.go:66-105`；mygo `toolbar.md` | ✅ |
| H7 | 滚动条 hover | **无任何变化**（不亮、不粗） | hover 变亮/变粗，拖拽更明显 | `brand/brand.go:118-128`；`§5.4` | ❌ A4-I-09 |
| H8 | 禁用控件 hover | 无反馈（不吃输入），挂在它身上的 tooltip 也不显示 | 解释用可见文本行或包裹容器 tooltip | **A4-P5**；`context_pane.go:230-238` | ❌ A4-I-05 |
| H9 | tooltip 延迟/内容 | 悬浮 0.6s、键盘聚焦立即；点击后同元素不再显示直到离开 | 同上；且不应吞 Esc | mygo `tooltip.md`；**A4-P1/P2/P3** | ⚠ A4-I-01 |
| H10 | 消息 ⋯ / 关闭 × | 常显（非 hover 隐藏） | 同上 | `view.go:2077-2097`；`frame.go:981-987` | ✅ |

### 1.6.4 按下 / 选中 / 激活 / 禁用 / 加载

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| P1 | pressed 视觉 | IconButton pressed=BgSelected；原生件自带 | 同上 | `iconbutton.go:158-160` | ✅ |
| P2 | 选中两级 | 持久导航 BgSelected+2 DIP 条；瞬态浮层允许 Accent 填充 | 同上 | `sidebar.go:1698-1730`；`frame.go:1165-1173` | ✅（文档漂移见 A4-I-13） |
| P3 | 重复点击停止 | `Stopping` → 「停止中…」+ Disabled，Esc 不再二次触发 | 同上 | `composer.go:365-375`；`view.go:3300` | ✅ |
| P4 | 重复点击「新对话」 | 建 2 个会话 | 一次 | **A4-P8** | ❌ A4-I-04 |
| P5 | 禁用态解释原因 | 4 处挂 tooltip（不可见）+ 1 处启用无解释 | 可见文本/wrapper tooltip | **A4-P5**；`goal.go:409`、`connectors_section.go:344`、`data.go:139/162`、`update.go:331` | ❌ A4-I-05 |
| P6 | 表单主按钮禁用 | 自动化表单 `valid=false` 时禁用且无原因 | 必填标记 + 就地错误（连接器/模型编辑即此模式） | `automation/view.go:572-575`、`:650` | ❌ A4-I-06 |
| P7 | 加载态是 Spinner 还是文字 | 全部 `Spinner + 文案`；按钮型改写 label | 一致 | `view.go:2969-2978`；`attachments.go:363` | ✅ |
| P8 | 长任务进度 | 不伪造进度；只 spinner/状态词 | 同上 | `uikit/runstatus.go:100-140` | ✅ |
| P9 | 乐观更新 + 失败回滚 | 会话行 fork/pin/archive/delete 失败有 `sessionOpErr`/`deleteErr` 行；goal 有成功/失败双反馈 | 同类操作统一"失败必报" | `sidebar.go:216-224`、`:2300` | ✅ |
| P10 | toast 撤销 | 归档恢复走 toast（`toast-action-undo`）；队列清空走 ConfirmDanger | 不可逆操作有确认或撤销 | `ui-drive:toast-action-undo`；`runstatus.go:213-232` | ✅ |

### 1.6.5 流式与长任务

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| S1 | 跟随滚动 | FollowEnd 贴底 | 同上 | `uikit/session.go:118-170` | ✅ |
| S2 | 用户上滚 | 停止跟随 + 浮出「回到最新」 | 同上 | `uikit/session.go:176-190`；`test:TestSessionThreadFollowAndBackToLatest` | ✅ |
| S3 | 展开/折叠工具与推理 | `HoldPlace` 一次性，行不跳 | 同上 | `uikit/session.go:119-121`；`reasoning.go:42-45` | ✅ |
| S4 | 停止按钮 | 立即 toast「已停止」+ `Stopping` 态 | 停在真实 settle | `view.go:276-287`、`:3300` | ✅ |
| S5 | 排队消息 | 徽章 + popover；逐条取消；清空确认；暂停说明 | 同上 | `runstatus.go:240-330` | ✅（文案字号 A4-I-16） |
| S6 | 工具运行/完成/失败/取消 | 六态 + StatusBadge + 折叠详情 | 同上 | `toolrow.go:240-340`；`status.go` | ✅ |
| S7 | 取消后状态 | 中性 cancelled 文案（不红色） | 同上 | `status.go`；`interrupted.go:40-70` | ✅ |
| S8 | 恢复会话 | 中断横幅 + 继续/忽略 + 冷恢复只读历史 | 同上 | `interrupted.go`；`test:TestInterruptedRestoreE2E` | ✅ |
| S9 | 流式完成播报 | 有 `Announce`（审批到达也有） | 屏幕阅读器可感知 | `view.go:382-392` | ✅ |

### 1.6.6 滚动

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| R1 | 滚动条样式/宽度 | 10 DIP thumb、无 track、无 hover | 6–10 DIP，hover 变亮/变粗 | `brand/brand.go:118-128` | ❌ A4-I-09 |
| R2 | 滚动边界 | 原生 overscroll 不弹跳（桌面） | 同上 | mygo `scroll.md` | ✅ |
| R3 | 虚拟化跳变 | `ui.List` 虚拟化 + `ScrollIntoView`/`ScrollTo` | 同上 | `uikit/session.go:95-130`；`test:TestSessionThreadVirtualizesLongConversation` | ✅ |
| R4 | 主区/右栏独立滚动 | 两个 pane 各自 Scroll | 同上 | `frame.go:562-640` | ✅ |
| R5 | 滚动锚定 | 贴底 + 折叠 `HoldPlace` | 同上 | 同 S1/S3 | ✅ |
| R6 | 命令面板滚动跟随选择 | `ScrollIntoView` 且首帧 `ScrollTo(0)` | 同上 | `frame.go:1070-1124` | ✅ |

### 1.6.7 拖放

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| D1 | 文件拖入窗口 | 无任何视觉提示；松手后进 composer 附件 | 拖入即高亮可放置区（composer 卡/整窗） | `session/session.go:629-643`；grep 无 `FileDragOver` | ❌ A4-I-03 |
| D2 | 拖到非会话屏 | toast「文件已暂存…」+「前往会话」 | 同上 | `attachments.go:284-306` | ✅ |
| D3 | 不支持的拖入内容 | 无提示（OS 层只投递路径，产品按扩展名分类；非法类型在附件芯片上标注） | 明确"不支持"反馈 | `attachments.go:190-280` | ⚠（无整窗级反馈） |
| D4 | 拖拽重排 | 无（没有可重排集合） | 不需要（会话顺序是后端概念） | — | — |
| D5 | 窗口内拖拽（tab 撕出等） | 未使用 | 不需要 | — | — |

### 1.6.8 复制 / 选择

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| C1 | 代码块复制 | 原生 CopyButton，图标切换 + 无障碍名 | 同上 | `markdown_code.go:43-46` | ✅ |
| C2 | 消息复制 | ⋯ 菜单 + toast | 同上（toast 语汇见 A4-I-17） | `view.go:2096-2101` | ✅ |
| C3 | 路径/ID 可复制 | 文件树右键「复制路径」；工作区根只在 tooltip/菜单里，**文本不可选中** | 值可选中或有一键复制 | `rightpanel/view.go:718-727`；`tools.go`? | ⚠ A4-I-21 |
| C4 | 错误文本可选中 | `ErrorLine` `RoleAlert` + 可选中 | 同上 | `uikit/uikit.go` ErrorLine | ✅ |
| C5 | 工具输出复制 | 工具面板段落可复制 | 同上 | `uikit/toolcall.go`/`toolpreview.go` | ✅ |
| C6 | 复制反馈统一 | 三种语汇：图标切换 / toast / Badge | 一种（图标切换为主，toast 仅跨屏） | 见上 3 行 | ⚠ A4-I-17 |

### 1.6.9 菜单

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| M1 | ⋯ 菜单 / 右键菜单 | 窗口内绘制（`PopoverMenus` 全局）+ 同一 build 函数 | 同上 | `frame.go:53`；mygo `context-menu.md` | ✅ |
| M2 | 菜单键盘 | Up/Down/Home/End/字母/←→/Enter/Space/Esc/Tab | 同上 | `menu-button.md` | ✅ |
| M3 | 菜单与锚点 | `AnchorBottomEnd/TopEnd` 对齐按钮端，无空间自动翻转 | 不遮触发点 | `sidebar.go:1620-1621` | ✅ |
| M4 | 危险项样式 | `.Danger()` 12 处（delete/revoke/close member…） | 同上 | grep `.Danger()` | ✅ |
| M5 | 禁用项原因 | 禁用组标题带 Tooltip / Trailing「轮次已用尽」 | 同上 | `view.go:3651-3672`、`goal.go:449` | ✅ |
| M6 | 菜单内 Tooltip 与 Esc | 禁用项 Tooltip 在菜单里（菜单先吃 Esc，安全） | 同上 | mygo `context-menu.md` | ✅ |

### 1.6.10 对话框

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| G1 | 默认焦点 | 危险确认=取消（安全） | 同上 | A4-P12 | ✅ |
| G2 | Enter 默认动作 | 危险确认 Enter=取消；审批 Inline 无 Default | 同上 | A4-P12；`uikit/approval.go:300-380` | ✅ |
| G3 | 危险动作非 Default | 是（danger 按钮不 Default，排序 取消→危险） | 同上 | `uikit/uikit.go:381-388`；`test:TestConfirmDangerDefaultsToCancel` | ✅ |
| G4 | Esc | 关闭=取消；审批=拒绝；计划审阅=不取消 | 同上 | 各组件测试 | ✅ |
| G5 | 背景滚动锁定 | Modal 锁滚动与交互 | 同上 | mygo `overlays.md` | ✅ |
| G6 | 尺寸与溢出 | `DialogWidth*` + 审批中段 `MaxHeight` 内滚 | 同上 | `uikit/approval.go:55-62`、`:258` | ✅ |
| G7 | 标题/说明/页脚层级 | `DialogHeader` + 说明 + `DialogFooter` | 同上 | `uikit/uikit.go` | ✅ |
| G8 | 关闭后焦点 | ConfirmDanger 丢失（Esc/Enter）；设置面与命令面板归还 | 全部归还 | A4-P4 + A4-QA 三例 | ❌ A4-I-02（限 ConfirmDanger） |

### 1.6.11 动效

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| A1 | 时长/缓动统一 | 新 `design/motion.go` 四档（80/120/160/200ms）；**仅 IconButton 采用** | 所有自绘状态/浮层/折叠引用同一张表 | `design/motion.go`；`iconbutton.go:137-143` | ⚠（刚开始） |
| A2 | ReduceMotion | 框架原生过渡与 `design.TransitionColors` 均遵守（负时长=立刻） | 全局遵守 | `design/motion.go:33-46`；mygo `transitions.md:404` | ✅ |
| A3 | 流式文字抖动 | 官方 `markdown.Stream` 负责 reveal，无自绘逐 token 动画 | 同上 | `uikit/session.go`；mygo `markdown.md` | ✅ |
| A4 | 窗口缩放跳变 | 窄窗→rail→drawer 有完整决策链与测试；toast 锚点在浮层开合时**瞬时跳位**（右上↔右下） | 布局跳变可接受；浮层锚点应过渡或固定 | `frame.go:668-742`；`toastanchor.go:79-108` | ⚠ A4-I-18 |

### 1.6.12 空 / 错 / 边界

| # | 动作 | 当前行为 | 期望行为 | 证据 | 判定 |
|---|---|---|---|---|---|
| E1 | 空态 | `EmptyState`/`EmptyStateBlock`/hero；纯文本无装饰图标 | 同上 | `uikit/uikit.go:392-414`；`frame.go:1088-1091` | ✅ |
| E2 | 加载态 | Spinner + 文案（无骨架） | 冷恢复/长列表用骨架或占位 | `view.go:2969-2978` | ⚠ A4-I-19（签名清单） |
| E3 | 错误态 | `ErrorLine`（RoleAlert + 可选中）+ 恢复动作 | 同上 | `uikit/uikit.go`；`view.go:173-178` | ✅ |
| E4 | 超长文本 | `MaxLines+Ellipsis+Tooltip` 遍布（标题/路径/提示） | 同上 | `frame.go:185`、`:1180-1183`；`folder` 行 | ✅ |
| E5 | 超大数字 | `FormatCompactCount`（42.1K/128K） | 同上 | `uikit/format.go` | ✅ |
| E6 | 窄窗 900×600 | 自动 rail、审批中段内滚、composer 贴底 | 同上 | `narrow_layout_test.go`；`ui-drive:window-900` | ✅ |
| E7 | I18N 长文案 | 英文文案多处更长；`MaxLines/Ellipsis` 兜底；`ui-drive:lang-en` PASS | 不溢出/不裁断操作 | `ui-drive:lang-en`；`composerVisibleHint` 单行约定 | ✅ |
| E8 | 11px 成句 | 队列暂停说明句用 TypeXXXS | 成句 ≥TypeXXS | `runstatus.go:258-260` | ⚠ A4-I-16 |

---

## 2. 发现（编号｜严重度｜位置｜问题｜为什么是问题｜具体改法）

### P1 · 直接破坏用户可见承诺（4）

---

#### A4-I-01 · P1 · 键盘/Esc · 提示浮起时，窗口级与 Modal 层的 Esc 被吃掉一次（焦点祖先链上的 Esc 不受影响）

- **位置**：Esc 的两个"非祖先链"拥有者——`internal/uiroot/shortcuts.go:332`（`c.OnShortcut(0, ui.KeyEscape, r.escapeFallback)`，注册在窗口根上下文，不在任何聚焦元素的祖先链里）；`internal/uiroot/frame.go:1440`（设置面的 `ui.Modal` 自持 Esc）。提示来源：`internal/uiroot/frame.go:236/310/340/369/387` 等侧栏与顶带图标按钮的 `Tooltip`、设置页各图标按钮的 `Tooltip`（`ui.Modal` 的 Esc 与 tooltip 都是 overlay，tooltip 后建、序列更高 → 先拿键）。
- **证据（运行时，基线与改动中都稳定复现）**：
  - `esc-chrome-tip-deny`：侧栏按钮聚焦（提示已起）→ 待审批仍在（Esc 被吃）。
  - `esc-chrome-tip-stop`：同上 → 运行未停。
  - `esc-modal-tip-close`：设置里焦点在「关闭设置」（提示已起）→ 设置未关。
  - 对照组（PASS）：`esc-composer-hover-stop` / `esc-composer-focus-stop`（屏幕级 Esc 在焦点祖先链上）、`esc-overlay-settings`（无提示时 Esc 正常关设置）。
  - 机制：`mygo/ui/input.go:1333-1357` `routeKey` 先查祖先链上的普通 shortcut，再查 overlay（`mygo/ui/tooltip.go:91`）。
  - 我此前的 A4-P1/P2/P3 探针是"根作用域 + 裸 ComposerShell"，属于人工构造的窗口级注册；**产品 composer 不受影响**（已复核并在 A4-QA 中固化为回归用例）。
- **为什么是问题**：AP-24 要求「Esc 必须在窗口级生效（不依赖焦点位置）」——现在恰恰依赖"焦点落在哪种控件上"。用户最常见的两条 Esc 路径都会静默失败一次：从侧栏/顶带按 Esc 拒绝审批或停止，以及在设置里按 Esc 关闭。再按一次可用，但"一次不响应"就是"按键坏了"的直觉。
- **改法（只改注册点，不删提示）**：
  1. `internal/uiroot/view`：把窗口级 Esc 的注册从 `view(c)` 根上下文移进**根 `ui.Row` 的 `Children`** 内（`c.OnShortcut` 在 Children 里注册到该容器），使它在任何聚焦元素的祖先链上；`bindChromeShortcuts` 里其余快捷方式不动。
  2. 设置面：在 `r.settings(c,&open)` 的内容列上再注册一次 `c.OnShortcut(0, ui.KeyEscape, close)`（与 Modal 自身 Esc 并存，祖先链先吃 → 一次生效）。
  3. 通用对话框：给 `uikit.ConfirmDanger`/`AskDialog`/`PlanReviewDialog`/`HelpDialog` 的内容列同样加"祖先链 Esc"，作为统一 helper（`uikit.DialogEscape(c, onEsc)`）。
  4. 结构性校验：新增帧测试"提示在场 + Esc 一次生效"，覆盖以上三处（即 A4-QA 的三个 guraded 用例转正）。
  5. 上游可选：mygo 的 tooltip 由焦点升起时不应消费 Esc（Base UI 只在指针悬停打开时消费）。
- **反证自检**：① 规格豁免？无。② 其它兜底？`Cmd+.` 可中断，但设置关闭没有第二把键。③ 测试覆盖？现有 Esc 测试都在"无提示浮层"条件下。④ 是否只在悬浮后立刻按 Esc？不是——键盘 Tab 把提示顶起来即复现（稳定 5/5）。

---

#### A4-I-02 · P1 · 焦点/对话框 · `uikit.ConfirmDanger` 关闭后不归还键盘焦点（18 个调用点）

- **位置**：`internal/uikit/uikit.go` `ConfirmDanger`（审计时 :347-390，现 :350+；→ `ui.Modal`，关闭由取消按钮的 `OnClick{*open=false}` 完成）。
- **复核结论（2026-11-18）**：设置面与命令面板**不**受影响——`focus-return-settings`、`palette-escape-restores-focus`、`palette-command-restores-focus` 三例在基线与改动中都 PASS；裸 `ui.Modal` 用 Esc 关闭也会归还。丢焦点的只有"面板内按钮点击清 open"这条路径（ConfirmDanger 的取消/删除按钮、以及裸 Modal 的 Enter 激活按钮）。
- **证据**：A4-P4 矩阵（同一 view、同一触发按钮）：

  | 场景 | 关闭方式 | 关闭后焦点 |
  |---|---|---|
  | 裸 `ui.Modal` | Esc | **回到触发按钮** ✅ |
  | 裸 `ui.Modal` | Enter（激活获焦按钮） | 停在**已消失的对话框按钮**上 ❌ |
  | `uikit.ConfirmDanger` | Esc（取消按钮的 Default 路径） | **空（无焦点主）** ❌ |
  | `uikit.ConfirmDanger` | Enter（取消） | **空** ❌ |
  | 设置面 / 命令面板 | Esc / 执行命令 | 回到打开它的控件 ✅ |

  同一代码库里已有三处"关闭后手工归位"的补丁：`frame.go:880-888`（inspector toggle）、`view.go:5345-5355`（FindBar 关闭后聚焦 composer，注释明写"a closed bar must not leave the window without a focus owner"）、`frame.go:992-994`（drawer）。说明模式已知，只是没有普及；设置面/命令面板靠 mygo 的 overlay 归还已达标。
- **为什么是问题**：键盘优先产品里，删除确认是最需要连续操作的对话（确认→回到列表→继续）。焦点归零后：输入无处可去、Enter 不再作用于任何行、Tab 从侧栏第一个可聚焦元素重开——用户会认为"键盘坏了"。而且 `ConfirmDanger` 是**唯一**的危险确认入口（18 个调用点：会话删除、工作区删除/重命名、记忆清空、数据清空/导入、恢复默认快捷键、移除模型/LSP/归档/定时提醒、成员关闭…），影响面覆盖整个产品。
- **改法**：
  1. 给 `uikit.ConfirmDanger`/`DialogFooter` 增加 `RestoreFocus *ui.Handle`（或 `ReturnTo func()`）：关闭时 `ReturnTo(c)`；调用方把触发控件 `Bind` 到 handle 传入。批量改造 18 处，但每处只加一行。
  2. 或者更小：让 ConfirmDanger 的取消/危险动作**先关闭 Modal 的同一条归还路径**（例如把 `*open=false` 交给 Modal 的 dismissed 标志，而不是在按钮回调里直接清），再执行 OnConfirm。
  3. 统一 helper：`uikit.FocusOnClose(c, &open, handle)`，杜绝下一次再出现"某处忘记归位"（设置面/命令面板已达标，可作参照实现）。
- **反证自检**：① 规格豁免？`docs/ui/overlays.md:124-125` 明确"每个 overlay 顶部元素在消失时把焦点还给原来的持有者"——现状违反框架自身承诺（ConfirmDanger 的 Modal 包装丢掉了它）。② 其它兜底？`Esc` 窗口级兜底仍生效（不需要焦点），但打字/Enter 不行。③ 测试已覆盖？`TestConfirmDangerDefaultsToCancel` 只测 Enter 语义，无焦点断言。

---

#### A4-I-03 · P1 · 拖放 · 外部文件拖入没有落点高亮/可放置提示

- **位置**：`internal/uiroot/filedrop.go:1-36`（只有 `SetHandler/Dispatch`）；`internal/ui/session/session.go:629-643`（窗口级 `OnFileDrop` → 后台暂存）；`internal/host/host.go`（`OnFileDrop` 全窗口）。
- **证据**：全仓 grep `FileDragOver` / `DroppedFiles` **零命中**（产品未使用 mygo 的元素级落点 API）；框架能力见 `mygo/docs/ui/input.md:483-499`：
  ```go
  if zone.FileDragOver() { zone.Border(2, t.Accent) }
  if files := zone.DroppedFiles(); files != nil { app.files = files }
  ```
- **为什么是问题**：桌面 AI 客户端里"把文件拖进来"是最高频的附件路径（截图/PDF/代码文件）。没有落点反馈时，用户无法判断"这里能不能放"——拖到 composer 上方松手是成功还是被忽略？现状是"整窗都能接"，但**看起来**哪儿都不像能接；这是"企业级工具质感"里最直观的一处缺口，成本却极低。
- **改法**：
  1. composer 卡（`ComposerShell` 的 `card`）加 `FileDragOver()` → 描边 `Accent` 2 DIP + 卡内一行「松开以添加附件」提示；`DroppedFiles()` 结果直接走现有 `stageAttachmentFilesAsync`（替换/并存窗口级 handler：元素级优先，窗口级兜底）。
  2. 会话屏整体（transcript 区域）也给一个更弱的落点态（例如 `BorderL2` + 右上角浮动提示），因为用户可能拖到任何位置。
  3. 非会话屏：窗口级 handler 继续负责（已有 toast+跳转），但至少给整窗一个 1 帧的遮罩提示「文件将暂存到会话输入框」。
  4. 不支持的拖入内容（目录/无权限）→ 在附件芯片上显示原因（现状部分有，`attachments.go:190-280`）。
- **反证自检**：① 规格豁免？S7 只写"拖入保留"，未禁高亮；DESIGN 反模式也没有反对。② 其它路径兜底？现有跨屏 toast 只解决"事后告知"，不解决"拖的瞬间"。③ 测试已覆盖？`filedrop_test.go` 只测 handler 分发。

---

#### A4-I-04 · P1 · 点击 · 「新对话」等一次性动作无防抖：双击创建两个会话

- **位置**：`internal/ui/sidebar/sidebar.go:412-425`（`if button.Clicked() { chat.NewSession("") … }`）、`:1526-1545`（工作区行 + 新建会话）、`internal/uiroot/shortcuts.go:106-139`（Cmd+N 每次触发都新建）、hero 的「新对话」/示例卡（`view.go:1257-1320`）。
- **证据**：A4-P8（真实 assembly）：对新对话按钮连点两次（双击窗口内）→ `Sessions()` 0 → **2**。mygo 的 click 计数会把第二次点击也报 `Clicked()`（双击窗口内两次 click），产品侧没有任何 `lastNewChatAt` 去抖。
- **为什么是问题**：双击是"点不动就再点一次"的自然反应；一次双击留下两个空会话，侧栏里出现两个「未命名会话」，还会各自抢占 active session，用户得手动删。同理，`Cmd+N` 长按由 `ShortcutRepeated` 保护（有），但**鼠标/触屏没有等价保护**。
- **改法**：统一"创建型动作"防抖：`uikit` 增加 `Debounce(c, key, 400ms) bool`（或模块内 `lastCreate time.Time`），新会话/新建技能/新建连接器/新建自动化任务在 300–500ms 内只执行一次；或更彻底——`NewSession` 后若当前会话是"空的且未发送"，再次触发时**复用它**而不是新建（幂等，符合"新对话"的语义）。
- **反证自检**：① 规格豁免？无。② 其它兜底？会话可由用户删除，但"双击产生垃圾数据"违反错误预防。③ 测试已覆盖？侧栏测试覆盖单击新建，无双击。

### P2 · 重要缺陷/显著体验问题（11）

---

#### A4-I-05 · P2 · 禁用态 · 挂在禁用控件上的 Tooltip 永不显示，"为什么不能点"的解释失效

- **位置**（产品把解释挂在 `Disabled` 上的 5 处）：`internal/ui/session/goal.go:409-413`（继续目标：轮次用尽）、`internal/ui/plugins/connectors_section.go:344-350`（设置存储未启用/配置无法解析）、`internal/ui/settings/data.go:139-142`、`:162-165`（数据目录未设置时打开/复制）、`internal/ui/settings/update.go:328-334`（无可下载链接）、`internal/uikit/composer.go:399`（发送：空草稿时）。
- **正确写法的唯一样板**：`internal/ui/session/context_pane.go:230-238`：
  ```go
  // A disabled element shows no tip of its own (mygo tooltip.md), so the tip lives
  // on the wrapper the pointer hovers; Description carries the same reason to AT.
  ui.Row(c).Tooltip(reason).Children(func() {
      design.Apply(ui.Button(c, label), t.SecondaryButton()).Disabled(true).Description(reason)
  })
  ```
- **证据**：A4-P5（对照组：启用按钮悬浮 0.9s → 提示出现；禁用按钮 → 永不出现，且 Tab 跳过）。
- **为什么是问题**：这正是用户点名的"禁用态是否解释原因"。现状是**写了但看不见**——比不写更糟，因为代码注释声称在解释。六处里五处无效（数据目录页另有一行「未设置」文本，属半有效）。
- **改法**：把 `context_pane.go` 的包裹模式抽成 `uikit.DisabledAction{Label, Reason, OnClick, Disabled}`，一次替换 5 处；或统一改为"在控件旁/下显示一行 `TextMuted(TypeXXS)` 原因"（对表单更自然）。同步在 DESIGN.md §2 场景表加一条"禁用控件必须用可见文本解释，不用 Tooltip"。
- **反证自检**：① 规格豁免？无，AGENTS §3.7 语义、§3.8 Tooltip 按需都不豁免"解释不可见"。② 框架行为是否另有路径（如 `HelpText`）？mygo 对禁用控件没有可用的 hover/描述通道，`Description` 只到读屏。③ 测试已覆盖？无禁用解释测试。

---

#### A4-I-06 · P2 · 表单 · 自动化任务「保存」禁用但不说缺什么

- **位置**：`internal/ui/automation/view.go:572-575`（`valid := name != "" && prompt != "" && schedule != ""`）、`:650`（`.Disabled(!valid)`）；字段无「必填」标记、无就地错误。
- **证据**：代码 + 同产品对照：连接器添加按钮用「tooltip 解释」（但见 A4-I-05 不可见）、更新页用独立说明行（`update.go:341-343`）、模型编辑用"可点 + 提交时就地报错"（`modelcatalog_editor.go:1055-1056`）。三种范式并存。
- **为什么是问题**：新建任务时名字没填 → 保存灰着，用户不知道是名字、提示词还是计划没填（三个都可能是原因）。这是"表单不能提交"的经典挫败点，而且此处 Save 是页面唯一 primary（视觉上最重的按钮，却是死的）。
- **改法**：① 字段 label 加必填标记（`ui.Field(...).Description("必填")`）；② 提交态改为"可点 + 点击时就地错误"（与模型编辑一致，也让 Enter 有反馈）；③ 全产品统一：`uikit.FormActions` 里封装"禁用 + 原因行"或"可点 + 错误行"二选一，禁止裸 `Disabled(!valid)`。
- **反证自检**：① 规格豁免？DESIGN §3 反模式未覆盖"表单禁用无原因"，但 §5.2 要求错误可操作。② 其它兜底？`scheduleErr` 只覆盖计划格式错误，不覆盖必填。③ 测试已覆盖？`automation_e2e` 覆盖创建成功路径。

---

#### A4-I-07 · P2 · 命令面板 · 多词查询失败、Home/End/PageUp/PageDown 无效

- **位置**：`internal/uiroot/frame.go:1335-1344`（`strings.Contains(haystack, needle)`，needle 为整串）、`:1061-1062`（仅绑定 `KeyDown`/`KeyUp`）。
- **证据**：A4-P6：输入「设置 主题」→ 面板显示「没有匹配的命令」（而"设置"与"设置：主题"都存在）；A4-P7：`Down,Down,Home,Enter` 打开的是**第三行**对应页面（外观），证明 Home 未把选择移回首行；`PageDown` 亦无效果（`End` 因断言不严未单列，但同源无绑定）。
- **为什么是问题**：命令面板是"覆盖全部动作"的入口（BRIEF 明确要求）。用户的输入习惯是自然语言短词组合（"设置 主题"、"打开 文件 页"），单词精确子串命中率低；而列表导航缺 Home/End 与 PageUp/Down 会让长列表（本产品默认语料数十条命令）键盘操作低效——这正是"企业级工具"与"差不多能用"的差别。
- **改法**：① 查询按空白切词，全部词都要在 haystack 内命中（AND），并支持"最后一个词做前缀优先排序"；② `Home/End` → 首/末命令（跳过 group header），`PageUp/PageDown` → 翻一屏（可用 `ui.List` 的 `ScrollIntoView` + 行数步进）；③ 空结果时给出可执行提示（"没有匹配。试试：设置 / 文件 / 团队"）而不是只一句"Esc 关闭"；④ 记录最近使用的 3 条命令置于顶部（纯本地 UI 状态，不新增后端能力）。
- **反证自检**：① 规格豁免？DESIGN §2.3 只规定组件；字准则也允许更宽的匹配（只放宽匹配，不改语义）。② 其它兜底？用户可退化为单词查询。③ 测试已覆盖？`palette_test.go` 覆盖单串过滤/选中行可见，无多词/Home/End。

---

#### A4-I-08 · P2 · 可发现性 · 两份快捷键参考都不完整且互相不一致

- **位置**：`internal/ui/session/commands_help.go:53-66`（/help：N、,、panel、sidebar、Alt+I、Enter、Shift+Enter、Esc、/、@、↑↓、Tab）；`internal/ui/settings/settings.go:84-92` + `:226-236`（设置→全部快捷键：N、Alt+I、Enter、Shift+Enter、/、,、Esc + 注册命令的 chord）。
- **差异与缺口**：/help 有 `@`、`Tab`、`Cmd/Ctrl+K`（动态）、`Cmd+B`（动态），缺 `Cmd+Alt+P`；设置页有 `Cmd+Alt+P`（来自命令注册），缺 `@`、`Tab`；**两处都没有**：`Esc 停止生成`（产品最常用）、`Cmd+.`（中断）、`Esc 拒绝审批`、`Cmd/Ctrl+Enter`（发送备用）、`↑ 上一条消息`、`Esc 回父会话`、`Cmd+F`。
- **证据**：代码逐条比对（上）；`ui-drive` 步骤 `settings-page-appearance` 断言里有「桌面快捷键」但无完整性断言；A4-P9 的 Tab 顺序证明 `Tab` 是真实交互键却没有在 /help 之外被记录。
- **为什么是问题**：用户明确要求"快捷键是否有冲突、是否可发现"。现在**同一款软件两份清单打架**，且都漏掉运行中最该记的 Esc=停止。用户按下 Esc 时看到 composer 提示说"Esc 停止"，但去帮助里查不到，会怀疑提示。
- **改法**：① 单一数据源：新增 `uikit.ShortcutReference()`（返回 `[]struct{Action, Chord, Scope}`），/help、设置页、命令面板 tooltip 全部从它渲染；② 把动态 chord（sidebar/palette/命令注册）注入同一列表；③ 用新落地的 `uikit.Kbd` 渲染键帽（见 A4-I-14），并在 composer 提示行把 `Esc` 变成键帽；④ 加一条测试：断言"Esc 停止"出现在两处参考里（防止再漂移）。
- **反证自检**：① 规格豁免？AGENTS §3.17 要求关键信息可发现；② 其它兜底？composer 提示行有 Esc 停止，但只在运行中显示一瞬；③ 测试已覆盖？`commands_i18n_test.go` 测文案存在，无完整性。

---

#### A4-I-09 · P2 · 悬浮 · 滚动条完全没有 hover 反馈，且静止对比度不足

- **位置**：`internal/brand/brand.go:118-128`；`internal/brand/palette.go:191`（亮 `Scrollbar: ink 22%`）、`:242`（暗 `white 24%`）。
- **证据**：`ScrollbarStyle{TrackWidth:10, ThumbWidth:10, MinLength:24}`，**未设 `Thumb/Hover/Active`** → mygo `scrollbar.go:118-124` 让三者都退回主题 `Scrollbar`；`scrollbar.go:277-279` 只在 `ThumbWidth+2 <= TrackWidth` 时拖拽变宽（10+2>10 → 永不变宽）。注释写「brighten on hover and while dragging」，行为不符。对比度（§5.4）：thumb vs 背景 亮 **1.62:1**、暗 **2.13:1**。
- **为什么是问题**：滚动条是"长内容可滚动"的唯一常驻提示；静止太淡 + hover 无响应，会让"这块能不能滚/滚到哪"变模糊——这正是用户点名的"滚动条样式/宽度/悬停变粗"。同时注释与实现矛盾，会误导后续改动。
- **改法**：`Thumb: p.Scrollbar`、`Hover: 更强一档`（亮 ink 34% / 暗 white 36%）、`Active: 再强一档`；`TrackWidth: 12, ThumbWidth: 10` 让拖拽/悬停可加宽 2 DIP；静止维持 10 DIP（桌面观感）。改完补一个像素断言（悬停前后 thumb 采样色不同）。同步修掉 brand.go 注释与实现的关系（要么实现它，要么删掉承诺）。
- **反证自检**：① 规格豁免？无，H 类要求每个可交互区域有 hover。② 其它兜底？内容溢出处有渐变/阴影吗——没有。③ 测试已覆盖？无滚动条测试。

---

#### A4-I-10 · P2 · 导航 · 检查器打开文件后文件树消失，无"返回文件列表"

- **位置**：`internal/ui/rightpanel/rightpanel.go:282-302`（`openFile` 把 `st.tab` 切到 `pageFixedCount+index`）；`internal/ui/rightpanel/view.go:494-560`（文件页 = 树）；页签条只有「开始/文件/文件页签」。
- **证据**：A4-P11 截图 `.cache/ui-drive-audit/probe-filetree-selected.png`：点树里的 `README.md` 后，面板变成"文件预览"，树不可见；要回树必须再点一次「文件」页面 tab（`.cache/ui-drive-audit/probe-filetree-back.png` 为返回后的状态）。`ui-drive:panel-file-tabs` / `panel-files-preview` 只验证"能打开/能切换"，未覆盖"如何回列表"。
- **为什么是问题**：文件浏览器的基本循环是"看树 → 开文件 → 回树 → 开下一个"。现在"回树"要重新学一次（点页面 tab），而且**页面 tab 一直显示「文件」被选中**，与"当前在看文件"的视觉不符——状态含糊会让人以为树被删了。检查器在窄窗还会转成 drawer（更容易迷路）。
- **改法**：① 文件页签的头部加一个"返回文件列表"面包屑/按钮（`‹ 文件`），或在页签条左端固定一个列表图标按钮；② 文件树与预览并存时（宽面板）双栏，或至少记住树滚动位置与选中行（见 A4-I-11）；③ 页面 tab 的选中态改为"页面"语义（文件页签在同一行时，页面 tab 仍高亮但加分隔线，明确两级）。
- **反证自检**：① 规格豁免？右栏是产品自定 surface，无硬规格；但 DSH/VS Code/Finder 一致保留树。② 其它兜底？可用 `Cmd+Alt+P` 重开文件页（可发现性差）。③ 测试已覆盖？`files_test.go` 只测打开。

---

#### A4-I-11 · P2 · 焦点 · 树行被点开但不会成为 Outline 的当前项，键盘无法从它继续

- **位置**：`internal/ui/rightpanel/view.go:700-732`（`row.ObserveInput(... InputPointerUp ... st.openFile(path))`）；`ui.Outline` 的选中由 `st.filesRow` 决定（`view.go:655-664`），点击路径从不写 `st.filesRow`。
- **证据**：A4-P11 无障碍树：点击 `b.md` 打开后，该 `TreeItem` 的 `Selected=false`（`selectable=true`）；A4-P14 进一步证明键盘不从被点行继续（下一帧行已被预览替换，`filesRow` 仍为 -1）。而键盘路径（Tab 进树 → ↓ → Enter）是正常的（`test:TestFilesTreeKeyboardSemantics`）。
- **为什么是问题**：鼠标用户点击后立刻改用键盘（↓ 继续浏览）是常见混合操作；现在箭头会从第 0 行重新开始，甚至看不到任何"当前行"标记——树看起来像"没有当前项"的静态列表。
- **改法**：`ObserveInput` 的 `InputPointerUp` 分支里同步设置 `st.filesRow = <row index>`（Outline 提供 row 索引/`Item(i)` 反查），或在行内改用 `ButtonBase`+`RowClicked` 让 List 自己选中并处理 Enter（需保留目录行的展开语义）；补帧测试：点击第 2 行 → ↓ → Enter 打开第 3 行。
- **反证自检**：① 规格豁免？无；② 兜底？键盘用户 Tab 进树仍可用（仅鼠标→键盘切换受影响）；③ 测试覆盖？`files_outline_test.go` 只测键盘路径。

---

#### A4-I-12 · P2 · 对话框 · `ApprovalDialog` 的 Modal 兼容路径焦点落在「拒绝」且 Enter 直接拒绝（与 AP-12 冲突的死路径）

- **位置**：`internal/uikit/approval.go:155-380`（`Inline=false` 分支 → `ui.Modal`）；产品唯一调用点 `internal/ui/approval/approval.go:219` 传 `Inline: true`。
- **证据**：探针（`TestProbeApprovalFooterNoDefault`）：Modal 路径打开后焦点在「拒绝」，按 Enter → `OnResolve(deny)`。规格（AGENTS §3.9、AGENTS §6 已知漂移、DESIGN AP-12）要求"审批无 Default、默认焦点不得落在允许/拒绝"。
- **为什么是问题**：这是一条**看似可用、实际违反核心安全约定**的代码路径。任何人（未来重构、第三方 embedder、快照工具 `ui-snapshot/main.go:2333` 已经在用）打开 Modal 形式，就会得到"回车即拒绝"的行为——正是历史上出过事故的那类误触。要么补齐安全语义，要么删掉路径。
- **改法**：① 若保留：打开时把焦点放到卡片容器（`Role(ui.RoleDialog)` 的不可激活节点）而不是第一个按钮，并让 Enter 不做决定（`ui.Modal` 的默认按钮机制要显式关掉）；Esc=拒绝保持。② 若不保留：删除 Modal 分支，`ui-snapshot` 改为 Inline 截图，`ApprovalDialogOptions.Inline` 字段移除，避免"第二套审批解剖"。
- **反证自检**：① 规格豁免？无；② 兜底？产品路径不用它，但快照/未来代码会用；③ 测试覆盖？`TestApprovalFooterOrderAndNoDefaultAction` 测的是 footer 顺序，未测 Modal 焦点。

---

#### A4-I-13 · P2 · 契约 · 选择态 token 文档 12%/18% 与实现 9%/13% 漂移（审计期内部分已修）

- **位置**：实现 `internal/brand/palette.go:165`（`RowSelected: royal 9%`）、`:224`（`cyan 13%`）；**仍漂移**：`docs/DESIGN.md:187`（§3.1 规则 9）、`:289`（AP-19）、`:609`（§6.1 `SelectedRow()` 表）写 `亮 12% / 暗 18%`；**已同步**：`AGENTS.md:57`、`docs/DESIGN.md:492`（§5.1 表）。
- **证据**：grep 上述行（审计结束时状态）；`git diff internal/brand/palette.go` 显示本轮把 12%→9%、18%→13%，`docs/DESIGN.md`/`AGENTS.md` 只有部分表格被并行改动同步。
- **为什么是问题**：AGENTS §6 规定 token/契约改动必须同 PR 同步文档；现在"契约说的和眼睛看到的不是一个值"，下一轮审计/实现会按 12%/18% 去改，造成来回漂移。也会误导对比度结论（9% 与 12% 上的文字对比不同）。
- **改法**：① 把 `docs/DESIGN.md:187`、`:289`、`:609` 三处改为 9%/13%，并说明"暗色更静（calm）"的理由（AGENTS 与 §5.1 表已改好，勿回退）；② 更彻底：在 `brand` 暴露 `RowSelectedAlphaLight/Dark` 常量并让文档引用常量名，避免数字重复。
- **反证自检**：① 豁免？无；② 兜底？无；③ 测试？`onaccent`/`badge_contrast` 测试覆盖文字对比，不覆盖该数字。

---

#### A4-I-14 · P2 · 一致性 · 键帽组件已落地但零采用，快捷键仍作纯文本

- **位置**：`internal/uikit/kbd.go:1-40`（`Kbd`/`KbdHint`）、`internal/design/components.go:265-273`（`Tokens.Kbd`）；调用点：**0**（仅 `components.go` 的样式定义）。
- **证据**：grep `Kbd(`/`KbdHint(` 除自身外无命中；命令面板行尾（`frame.go:1197-1204`）与设置快捷键参考（`settings.go:232-235`）仍是用 `MonoText(TypeXXXS)` 渲染的普通文本。
- **为什么是问题**：用户明确要求"快捷键是否可发现（提示/kbd 样式/帮助面板）"。组件建好不用 = 快捷键视觉上仍是"灰色小字"，在菜单/面板里与正文混在一起，扫读性差；同时三个地方（面板/设置/help）各写一遍文本渲染，迟早再漂移。
- **改法**：① 命令面板行尾、设置「全部快捷键」、`/help` 快捷键表、菜单 `.Shortcut()` 的替代渲染（菜单是框架绘制，无法改）、composer 提示行统一改用 `uikit.Kbd`/`KbdHint`；② 与 A4-I-08 的单一数据源一起做（一次改动解决可发现性 + 一致性）。
- **反证自检**：① 豁免？无；② 兜底？文本可读但样式不一致；③ 测试？`kbd` 尚无测试。

---

#### A4-I-15 · P2 · 图标 · 图标集缺 `chevron-left`，用「‹ 」文本与旋转图标兜底

- **位置**：`internal/icons/svg/`（34 个：`chevron-down`、`chevron-right`，无 `chevron-left`/`chevron-up`/`arrow-left`/`paperclip`/`minus`/`external-link`/`pin`/`archive`…）；兜底：`internal/ui/session/view.go:536`（`label := "‹ " + backTargetLabel(...)`）、`internal/ui/session/attachments.go:757-770`（旋转 `ChevronRight` 180° 当上一张）。
- **证据**：`ls internal/icons/svg`；两处兜底代码与注释（"icons has no left chevron; the shared icon button's Content slot takes the right one turned around"）。
- **为什么是问题**：AGENTS §3.8 禁 Unicode 图标，允许的只是"控制自身的一部分"（+ − ×）。`‹` 是方向图标，靠字体渲染在不同平台宽高/基线不一（与 16 DIP 图标网格不对齐）；旋转 180° 的 chevron 在 1px 描边下会有亚像素模糊。这两处都在"返回/上一张"这种高频动作上。
- **改法**：补 `chevron-left`（把 `chevron-right` 的 path 水平镜像，保持 16/24 网格与 `icons` 不变量测试），再加 `chevron-up`、`paperclip`、`minus`、`arrow-up-right`（外链），并在 `icons.go` 暴露；把两处兜底换掉；分隔条无障碍名改为 `侧栏宽度可调`（`Split` 的 Label 没落到分隔条，A4-P9 第 6 站在读屏里是 "Divider"）。
- **反证自检**：① 豁免？§3.8 明文；② 兜底？旋转/文本能用但质量不达标；③ 测试？`icons` 有网格不变量测试，新增图标需登记。

### P3 · 打磨项（7）

---

#### A4-I-16 · P3 · 字号 · 队列暂停说明句用 11px（TypeXXXS）

- **位置**：`internal/uikit/runstatus.go:257-260`（"已停止：排队消息不会自动发送，可手动发送或取消" → `t.TextMuted(design.TypeXXXS)`）。
- **为什么是问题**：AGENTS §3.12「TypeXXXS(11) 只用于微标签/kbd，禁成句」；同文件其他句子已按 TypeXXS 处理（`:412` 是数字摘要，可接受）。这是完整说明句，11px 在 1280 宽度下几乎不可读。
- **改法**：改 TypeXXS（12/18），与 queue 徽章说明一致。

#### A4-I-17 · P3 · 反馈 · 复制有三套反馈语汇（图标切换 / toast / Badge）

- **位置**：`uikit/markdown_code.go:43-46`（代码块：原生按钮图标切换）、`session/view.go:2096-2101`（消息：toast「已复制」）、`settings/settings.go:639`、`settings/data.go:171`、`settings/update.go:334`（设置：Badge「已复制」）。
- **为什么是问题**：同一动作在不同表面给出三种确认，用户要学三遍；且 Badge 常驻不消失（只在离开页面时清），toast 自消失，图标切换常驻——三种"消失语义"。
- **改法**：统一为"按钮态切换（2 秒后回退）+ 读屏 Announce"；设置页的 Badge 改为按钮态；跨屏动作（复制后跳到别处）才用 toast。

#### A4-I-18 · P3 · 动效 · toast 锚点在浮层开合时从右上瞬移到右下

- **位置**：`uiroot/toastanchor.go:79-108`（浮层打开 → `FromBottom: true`）。
- **为什么是问题**：正在读的通知会突然跳到另一个角；`design.MotionEnter/Base` 已存在但这里没有过渡。ReduceMotion 下应瞬移（现状），普通模式应有 120ms 位移。
- **改法**：给 toast viewport 加 `Transition`/`AnimatePose`（`design.MotionBase`），并验证 ReduceMotion 下为 0。

#### A4-I-19 · P3 · 空/加载 · 无骨架屏；建议列表无"还有 N 条"

- **位置**：`session/view.go:27-28`（`maxSlashCommands/maxMentionFiles = 8`）、`:4937`（合并后截断）；冷恢复/长会话加载只有 Spinner 文案（`view.go:2969-2978`）。
- **为什么是问题**：长会话恢复时 transcript 空白 300–800ms 只有一行"正在读取…"，会让人觉得"丢消息了"；建议列表截断无提示，用户以为没有更多命令。
- **改法**：会话恢复用 3–5 条消息骨架（灰条，宽度错落，`ReduceMotion` 下静态）；建议列表第 8 条改为「…还有 N 条，继续输入以缩小范围」。

#### A4-I-20 · P3 · 可发现性 · 「快捷操作」按钮的命名/图标与命令面板不一致，且无可视 chord

- **位置**：`uiroot/frame.go:851-864`（label「快捷操作/Quick actions」，图标 `Search`）。
- **为什么是问题**：放大镜 + "快捷操作"两个信号都在说"搜索"和"操作"，但面板实际是"命令 + 跳转 + 设置"；无 `Cmd+K` 可视提示，用户只能靠记忆。
- **改法**：label 改「搜索或运行命令」/「命令面板」，按钮内加 `Kbd("⌘K")`（`uikit.Kbd` 已就绪），tooltip 写全。

#### A4-I-21 · P3 · 复制 · 工作区根路径不可选中/一键复制

- **位置**：`rightpanel/view.go:565-590`（`buildWorkspaceHeader`：名称只挂 `Tooltip(current.Root)`）。
- **为什么是问题**：用户要求"路径/ID 可选中"；根路径是最常被复制给别人的值，现在得去「设置→数据」或文件右键里找。
- **改法**：名称旁加一个 `uikit.IconButton(Copy)`（`复制工作区路径`），或让名称 `Selectable()` + 右键菜单加「复制路径」。

#### A4-I-22 · P3 · 层级 · Esc 顺序里「审批拒绝」先于「建议菜单」

- **位置**：`session/view.go:392-441`（find → child → **deny** → dialog → menu → stop）。
- **为什么是问题**：直觉顺序是"最内层浮层优先"（菜单在最内）。若 slash 菜单开着且有来自别的会话的待审批，Esc 会去拒绝审批而菜单不动——两个都不明显。规格（AGENT-SURFACES §2.1）把建议菜单列在最内层，与代码顺序冲突。
- **改法**：把 `suggestionMenuOpen` 分支提到 `denyPendingApproval` 之前（并补帧测试：菜单开 + 待审批 → Esc 只关菜单）。

---

## 3. 建议的设计语言 / 组件映射（可执行）

### 3.1 业界交互签名清单（缺哪些、值得补哪些）

> 判定口径：**有**=完整可用；**弱**=有但不达标（原因见发现）；**缺**=没有。列"值得补"的只在**不新增产品功能**的前提下补交互质量。

| # | 签名 | 状态 | 我们的现状 | 建议 |
|---|---|---|---|---|
| 1 | 命令面板 | 弱 | Modal+搜索+List+快捷键列+分组；缺 Home/End/PageUp/Down、多词 AND、最近项、可视 ⌘K 入口 | **值得补**（A4-I-07/I-20，成本低） |
| 2 | 快速切换（⌘P 会话/文件） | 缺 | 只能在侧栏搜索会话、右栏树翻文件 | **值得补但有取舍**：可复用 palette 数据源做第二模式（`@` 前缀切到"文件/会话"），不新增后端 |
| 3 | 多选（Shift/Cmd 多选行） | 缺 | 列表/树均单选 | **不补**：当前没有批量动作（批量删除/批量移动）需求，补了是装饰 |
| 4 | 内联重命名（F2/双击标题） | 弱 | 会话/工作区重命名走 Modal（`sidebar.go:2040-2075`、`:1562-1600`） | **值得补**：F2 → 行内 `editor`/TextInput，Enter 提交、Esc 取消、失焦提交；保留 Modal 作为兜底 |
| 5 | 拖拽重排 | 缺 | 无 `ListState.Reorder` 使用 | **不补**：会话/任务顺序由后端语义决定，不是交互质量问题 |
| 6 | 面包屑 | 弱 | 子会话父>子 `ui.Breadcrumbs`（好）；文件预览无返回面包屑 | **值得补**（随 A4-I-10） |
| 7 | Toast 可撤销 | 有 | 归档有 Undo action；队列清空走 ConfirmDanger | 保持；给"删除会话"加 Undo（有 30s 恢复窗口即可）会更像企业工具 |
| 8 | 骨架屏 | 缺 | 只有 Spinner+文案 | **值得补**（A4-I-19） |
| 9 | 行内 hover 动作 + 键盘可达 | 有 | `FocusWithin` 揭示，≥24 命中区 | 保持 |
| 10 | 右键上下文菜单 | 弱 | 文件树有（打开/复制路径）；消息只有 ⋯，无右键 | **值得补**：消息行 `row.ContextMenu`（复制/重试/编辑），与 ⋯ 共用 build 函数 |
| 11 | 键盘帮助面板 | 弱 | `/help` 存在但清单不全、与设置页不一致 | **值得补**（A4-I-08），加 `?` 或 `Cmd+/` 打开 |
| 12 | Esc 分层 | 弱 | 层次与测试都齐（find→child→deny→dialog→menu→stop） | 修 A4-I-01（提示吞键）+ A4-I-22（顺序） |
| 13 | 焦点环 | 有 | 2 DIP + 外扩，亮 4.4 / 暗 6.2 | 保持；补"被裁切"像素断言 |
| 14 | 关闭后焦点归还 | 缺 | ConfirmDanger/设置/面板全丢 | **值得补**（A4-I-02，影响 18 处调用点） |
| 15 | 拖放落点高亮 | 缺 | 无 | **值得补**（A4-I-03，mygo 现成 API） |
| 16 | 流式跟随 / 回到最新 | 有 | FollowEnd + 浮层按钮 + 测试 | 保持 |
| 17 | 停止/取消中间态 | 有 | `Stopping`/`停止中…`/team/job 同构 | 保持 |
| 18 | 排队消息 | 有 | 徽章 + popover + 逐条取消 + 清空确认 + 暂停说明 | 保持（字号 A4-I-16） |
| 19 | 多标签（文件） | 弱 | 可开/可关/自动滚动到新页签；缺中键关闭、⌘W、页签溢出滚动提示 | **值得补**：中键关闭 + ⌘W + 页签条溢出（成本低） |
| 20 | 单键快捷（/、@、↑↓、Tab） | 有 | 有且有测试 | 保持 |
| 21 | 骨架/占位（首屏） | 有 | hero + 首次运行清单（非骨架但明确） | 保持 |
| 22 | 键盘可达的"回到顶部/底部" | 缺 | 只有鼠标可点「回到最新」 | **值得补**：`End`（无焦点或列表聚焦时）跳最新 |

---


### 3.2 可执行的改动映射（按依赖排序）

> 原则：**先补机制，再改站点**。前 4 项是"一次修好一类"，后 12 项是站点替换；不做新功能、不动信息架构。

| 序 | 机制/站点 | 文件 | 具体改动 | 覆盖发现 |
|---|---|---|---|---|
| 1 | **焦点归还 helper** | `internal/uikit/uikit.go`（新增 `FocusOnClose`/`ConfirmDangerOptions.ReturnTo`） | 关闭时 `handle.Focus(c)`；触发控件已消失则由调用方给"区域自然焦点"回退（设置面/命令面板已达标，作参照） | A4-I-02 |
| 2 | **禁用解释组件** | `internal/uikit/`（新增 `DisabledAction`/`Reason` 行） | 把 `context_pane.go:230-238` 的包裹模式抽出来，替换 5 处 `Disabled+Tooltip` | A4-I-05、A4-I-06 |
| 3 | **快捷键单一数据源 + 键帽** | 新增 `internal/uikit/shortcutref.go`（`ShortcutReference()`）；改用 `uikit.Kbd`/`KbdHint` | `/help`、设置→全部快捷键、composer 提示行、面板行尾同源渲染 | A4-I-08、A4-I-14、A4-I-20 |
| 4 | **Esc 拥有者上移到祖先链** | `internal/uiroot/frame.go`（根 Row / 设置内容列）；`internal/uikit`（对话框共用 `DialogEscape`） | 窗口级兜底与 Modal Esc 都要在聚焦元素的祖先链上注册（`c.OnShortcut` 放进 Children）；**不必删图标按钮的提示**（删了也修不了设置/侧栏两类） | A4-I-01 |
| 5 | **落点高亮** | `internal/uikit/composer.go`（card `FileDragOver()`/`DroppedFiles()`） | 拖入描边 + 「松开以添加附件」；`DroppedFiles` 走 `stageAttachmentFilesAsync` | A4-I-03 |
| 6 | **创建型动作幂等** | `internal/ui/sidebar/sidebar.go`、`internal/ui/home/`、`internal/ui/plugins/` | `Debounce(400ms)` 或"空会话复用" | A4-I-04 |
| 7 | **命令面板** | `internal/uiroot/frame.go` | 分词 AND；Home/End/PageUp/PageDown；最近 3 条；空结果可执行提示 | A4-I-07 |
| 8 | **滚动条 hover** | `internal/brand/brand.go` | `Hover/Active` 两档提亮；`TrackWidth 12 / ThumbWidth 10` | A4-I-09 |
| 9 | **检查器文件页** | `internal/ui/rightpanel/{rightpanel,view}.go` | 预览头「‹ 文件」返回；点击行同步 `filesRow`；树滚动位恢复 | A4-I-10、A4-I-11 |
| 10 | **审批 Modal 路径** | `internal/uikit/approval.go` | 焦点放容器、Enter 不决定（或删路径 + 改 ui-snapshot） | A4-I-12 |
| 11 | **选择态契约** | `docs/DESIGN.md:187`、`:289`、`:609`（AGENTS 与 §5.1 表已同步） | 12%/18% → 9%/13%（或改引用 brand 常量） | A4-I-13 |
| 12 | **图标补齐** | `internal/icons/svg/` + `icons.go` | `chevron-left`、`chevron-up`、`paperclip`、`minus`、`arrow-up-right`；替换 `‹ ` 与旋转兜底；分隔条名取 `Split.Label` | A4-I-15 |
| 13 | **字号/语汇打磨** | `internal/uikit/runstatus.go:260`；复制反馈三处 | TypeXXXS→TypeXXS；复制统一按钮态 + Announce | A4-I-16、A4-I-17 |
| 14 | **动效收口** | `internal/uiroot/toastanchor.go`；`internal/ui/toasts/` | toast 锚点用 `design.MotionBase` 过渡；其余自绘状态逐步接 `design.TransitionColors` | A4-I-18 |
| 15 | **加载占位** | `internal/ui/session/view.go`（transcript 恢复） | 3–5 条骨架行（ReduceMotion 静态）；建议列表第 8 条截断提示 | A4-I-19 |
| 16 | **细节补丁** | `internal/ui/rightpanel/view.go`（复制根路径）、`internal/ui/session/view.go`（Esc 顺序） | 复制按钮；建议菜单分支提前 | A4-I-21、A4-I-22 |

**设计语言层结论（供 Lead 收敛进 v2）**：
1. **提示与键位分工**：可见标签已表达动作的控件不再挂重复 Tooltip；Tooltip 只承载"标签无法表达的额外信息"，且必须接受"它会占用一次 Esc"。窗口级安全键（Esc=停止/拒绝）永远在**可见提示行**里，不依赖 Tooltip。
2. **禁用态**：永远用可见文本解释（行内 `TypeXXS` 或 `uikit.DisabledAction`），Tooltip 只做读屏 `Description`。
3. **反馈三选一**：按钮态切换（局部、2s 回退）→ 行内错误（就地）→ toast（跨屏/异步结果）。同一语义不得混用。
4. **焦点即状态**：任何会让元素消失的操作（关弹层、切页签、删行）必须声明焦点去向；无声明视为缺陷。
5. **键帽**：所有 chord 用 `uikit.Kbd`；成句提示用 `TypeXXS`，键帽用 `TypeXXXS`。


### 3.3 与 `B-spec-v2.md` §4「交互细节清单」的对齐与冲突

| B §4 条目 | 状态 | 本报告的补充/冲突 |
|---|---|---|
| 1 hover 80ms + hover≠selected | ✅ 一致 | `design.MotionFast` 已在 IconButton 采用；其余自绘行尚未接（A 类 A1/A2 条目），色值已满足（中性 4.5% vs 蓝 9%） |
| 2 焦点环 2px offset2 + 行内环 + **关闭浮层后焦点归还触发器** | ⚠ 部分 | 环满足（4.4–6.2:1）；设置面与命令面板已归还（A4-QA PASS），**`ConfirmDanger` 18 个调用点仍丢**（Esc 与 Enter 都丢，A4-I-02）——建议写成 `uikit.FocusOnClose` + `ConfirmDangerOptions.ReturnTo` 的机制条目 |
| 3 行内动作 hover/focus 显形 + `Label` + **tooltip 带快捷键** | ⚠ 冲突 | 显形与 Label 已达标；但「tooltip 带快捷键」与 A4-I-01 冲突：tooltip 会吞掉一次 Esc，若提示里写"Esc 停止"，用户按提示操作正好被吞。**建议改为**：快捷键写在可见提示行/键帽里，tooltip 只在"该控件与 Esc 无关"时携带 chord |
| 4 禁用：解释原因（**tooltip 或旁注**） | ❌ 冲突 | 禁用控件**不显示 tooltip**（A4-P5 实测）：现产品 5 处 tooltip 解释全部不可见。**必须改为**"旁注/包裹容器 tooltip"，并在规格里移除 tooltip 选项 |
| 5 复制：图标 1.2s 变 Check + toast | ⚠ 现状三分 | 代码块=图标切换、消息=toast、设置=常驻 Badge（A4-I-17）；建议按本条统一（图标切换 1.2s + 跨屏才 toast） |
| 6 Esc 层级 | ✅ 一致 | 代码已按 find→child→deny→dialog→menu→stop 实现并有测试；仅两处需动：tooltip 吞键（A4-I-01）、审批先于菜单（A4-I-22） |
| 7 Enter/⌘↵ + 审批无默认 | ✅ 一致 | 实测 ConfirmDanger Enter=取消、审批 Inline 无 Default；仅 Modal 兼容路径例外（A4-I-12） |
| 8 滚动条 10/10 + 悬停提亮 | ❌ 未实现 | `ScrollbarStyle` 未设 Hover/Active，`TrackWidth==ThumbWidth` 也不会变粗（A4-I-09）；建议 token 具体值写进规格 |
| 9 截断 + `TruncateStart` | ✅ 大体一致 | 现有 `Ellipsis("…")`+Tooltip 覆盖良好；路径缺 `TruncateStart`（A1 已列） |
| 10 空态 | ✅ 一致 | `EmptyState`/hero 已实现；无装饰图标圈 |
| 11 加载 ≤1s spinner / 长任务文字+停止 | ✅ 一致 | 仅长会话恢复缺骨架（A4-I-19，签名清单项） |
| 12 动效只用 §1.5 + ReduceMotion | ✅ 一致 | `design/motion.go` 的 4 档 + `ReduceMotion` 已就绪；采用面待扩 |
| 13 窄窗 1024 | ✅ 一致 | 自动 rail/drawer 有测试；顶带尾部溢出见 A3 VD-01 |
| 14 暗色 | ✅ 一致 | 焦点环 6.2–6.7:1；滚动条 2.13:1 偏弱（A4-I-09） |
| 15 a11y | ⚠ 部分 | 图标按钮 Label 达标；分隔条读屏名仍是英文 "Divider"（A4-I-15 附条）；建议加"可聚焦非按钮控件必须有本地化 Label"检查 |

## 4. 风险与取舍

1. **A4-I-01 的修法（已收窄，勿按旧口径删提示）**：真正要动的是 Esc 的**注册点**，不是提示。composer 内本就不受影响；删掉 composer 的停止/排队/发送提示**不能**修复"侧栏/顶带窗口级兜底"与"设置 Modal"两类（那些提示来自别处的图标按钮）。若已经删了 composer 三个提示，可保留（信息量损失小），但请把侧栏/顶带/设置里图标按钮的提示**留着**——企业级质感依赖它们。
2. **A4-I-02 的批量改造**：18 处 `ConfirmDanger` 需要各自传 `ReturnTo`（或改签名后统一绑定）。风险是"触发控件在关闭时已消失"（例如删除当前会话后该行不存在）——helper 必须允许回退到"该区域的自然焦点"（列表下一行 / composer / 侧栏搜索）。
3. **A4-I-03 与窗口级 handler 并存**：元素级 `DroppedFiles()` 与 `OnFileDrop` 互斥（框架规则：元素先吃，窗口收剩下的）。若 composer 卡只在会话屏存在，非会话屏仍走窗口 handler —— 需要确认两处不会重复暂存（加一个"本帧已消费"标记）。
4. **A4-I-07 多词 AND**：会让"设置 主题"类查询首次命中，但也可能让"设置主题"（无空格）与"设置 主题"结果不同；建议同时支持整体子串与分词 AND（整体命中优先），避免退化。
5. **A4-I-10 双栏**：检查器只有 ~320 DIP，树+预览双栏会挤；可行方案是"树可折叠为窄条"或"预览头加返回"，不建议硬塞双栏。
6. **A4-I-13 文档同步**：改文档会与并行的设计系统改动冲突（`docs/DESIGN.md` 正被其他成员修改）；建议由 Lead 在合并设计语言 v2 时**一并**改，而不是我单方面写入。
7. **未纳入本次结论**：设置页其余 8 页的字段级交互（模型编辑/网络/记忆/数据/隐私/LSP/导入/更新）只做了抽样（发现 A4-I-06 属抽样命中）；plugins 连接器 OAuth 流程的交互（授权中/失败/重连）只做了代码核对；上下文页（`context_pane.go` 1249 行）只核对了禁用解释样板。这些区域可能仍有 P2 级问题，建议下一轮专审。

---

## 5. 附：证据清单

### 5.0 基线与环境

- 代码：`e9a0d6b` + 工作区未提交改动（`git status --short` 见 §5.2 输出）。并行改动清单（审计时）：`AGENTS.md`、`docs/DESIGN.md`、`docs/UI-COMPONENT-MIGRATION.md`、`docs/UI-CONFORMANCE.md`、`go.mod/sum`、`internal/brand/{brand,palette}.go`、`internal/design/{components,registry,tokens}.go`、`internal/uikit/{iconbutton,iconbutton_test}.go`，新增 `internal/design/motion.go`、`internal/uikit/kbd.go`。
- **行号口径**：本文 `file:line` 取自审计时的工作区快照；并行改动仍在修改 `internal/uikit/uikit.go`、`internal/uikit/status.go`、`internal/brand/palette.go` 等文件，行号可能整体位移（符号名不变，可按 `grep -n` 定位）。
- 环境：`PATH=/usr/local/go/bin`、`GOPATH=/data/mygo/.cache/gopath`、`GOCACHE=/data/mygo/.cache/go-build`、`GOTMPDIR=TMPDIR=/tmp/zeokeel-tmp`、`GOFLAGS=-mod=mod`、`GOPROXY=off`。

### 5.1 测试复跑（本轮新鲜证据）

```
go test ./internal/uikit/ ./internal/ui/session/ ./internal/uiroot/ ./internal/ui/rightpanel/ \
        ./internal/ui/toasts/ ./internal/ui/approval/ ./internal/ui/interaction/ -count=1
→ ok uikit 5.263s | ok session 42.881s | ok uiroot 4.636s | ok rightpanel 3.434s
  ok toasts 1.584s | ok approval 1.420s | ok interaction 1.372s
```

```
go run ./internal/devtools/ui-drive --out .cache/ui-drive-audit
→ 84 steps, PASS 84 / FAIL 0 / WARN 0 / BLOCKED 0
→ report: .cache/ui-drive-audit/drive-report.md
→ results: .cache/ui-drive-audit/results.json
→ shots:   .cache/ui-drive-audit/shots/*.png（84 张）
```
与本审计直接相关的 ui-drive 步骤（全部 PASS，说明这些交互路径当前成立）：`palette-open`、`palette-filter-enter`、`palette-shortcut-column`、`palette-tab-enter`、`palette-down-enter`、`settings-close-esc`、`settings-close-x`、`settings-dirty-guard`、`stream-stop`、`queue-popover-cancel`、`toast-action-undo`、`approval-safe-default`、`overlay-plan`、`overlay-ask`、`mention-esc-enter`、`slash-keyboard`、`composer-slash-keyboard`、`composer-toolbar-groups`、`window-900`、`window-1024`、`panel-files-preview`、`panel-file-tabs`、`team-chip`、`jobs-chip`。

### 5.2 关键截图（本轮生成，人工已看）

| 文件 | 内容 | 结论 |
|---|---|---|
| `.cache/ui-drive-audit/probe-filetree-selected.png` | 点文件树行后：面板变成文件预览、树消失、页面 tab 仍高亮「文件」 | A4-I-10 |
| `.cache/ui-drive-audit/probe-filetree-back.png` | 再点「文件」返回后：树回来、无任何"当前文件"标记 | A4-I-10/I-11 |
| `.cache/ui-conformance/p1-chat.png`（基线） | 有会话时的 composer/工具行/代码块复制/用量条 | 现状参照 |
| `.cache/ui-conformance/p2-files.png`（基线） | 检查器文件页 + 侧栏选中态（BgSelected + 2 DIP 条） | P2 判定参照 |

### 5.3 探针（临时包 `internal/uitest_scratch/`，**已删除**；源码逐字记录以便复现）

探针 1-3（框架层键位/提示事实）：

```go
func TestProbeTooltipTakesFirstEscape(t *testing.T)            // A4-P1 提示吞第一次 Esc（对照：无提示则通过）
func TestProbeEscapeLayerOrderOverTextInput(t *testing.T)      // A4-P13 屏幕 OnShortcut 先于聚焦 TextArea 的 OnTextKey（Enter 相反）
func TestProbeTooltipOnDisabledControl(t *testing.T)           // A4-P5  禁用控件永不显示 tooltip（启用对照组显示）
```

探针 4（产品级 composer/Esc）：

```go
// 真实 uikit.ComposerShell（Running=true, StopTooltip="停止 · 会中断当前回复"）
// + 屏幕作用域 c.OnShortcut(0, ui.KeyEscape)
func TestProbeStopTooltipSwallowsEscape(t *testing.T)          // A4-P2 Tab 到停止 → 第一次 Esc 只关提示
func TestProbeHoveredTooltipSwallowsEscape(t *testing.T)       // A4-P3 悬浮起提示 → 第一次 Esc 只关提示
func TestProbeComposerNoEscapeOwnerOnceFocusLeavesTheInput    // 无屏幕注册时，焦点离开输入框后 Esc 无主（宿主必须自己注册）
```

探针 5（弹层焦点）：

```go
func TestProbeFocusReturnMatrix(t *testing.T)                  // A4-P4 裸 Modal+Esc=归还；裸 Modal+Enter=停在消失按钮；
                                                               //       ConfirmDanger+Esc/Enter=空
func TestProbePlainModalFocusReturn(t *testing.T)              // 对照：框架本身会归还
func TestProbeConfirmDangerFocusAndDefault(t *testing.T)       // A4-P12 焦点在「取消」、Enter=取消、危险动作需显式点击
func TestProbeApprovalFooterNoDefault(t *testing.T)            // A4-I-12 Modal 审批路径焦点在「拒绝」、Enter=deny
```

探针 6（真实 desktop assembly，`assembly.LoadWithBuildInfo` + `uiroot.New` + 内存 settings）：

```go
func TestProbeTabOrder(t *testing.T)                           // A4-P9  完整 Tab 顺序（§1.2 表）
func TestProbeComposerToolbarKeyboard(t *testing.T)            // A4-P10 工具栏单 stop，←/→ 在 附件/模型/权限 间移动
func TestProbePaletteMultiTermQuery(t *testing.T)              // A4-P6  「设置 主题」→ 没有匹配的命令
func TestProbePaletteHomeJumpsToFirstRow(t *testing.T)         // A4-P7  Down,Down,Home,Enter 打开"外观"（Home 无效）
func TestProbeDoubleClickNewChat(t *testing.T)                 // A4-P8  双击「新对话」→ 会话 0→2
func TestProbeFileTreeSelectionPaint(t *testing.T)             // A4-P11 树行点击后 TreeItem.Selected=false；截图两帧
func TestProbeFileTreeSelectionFollowsThePointer(t *testing.T) // A4-P14 键盘不从被点行继续
func TestProbeDisabledSendExplanation(t *testing.T)            // 空草稿时发送禁用，悬浮无解释；提示行仍写「Enter 发送」
```

### 5.4 WCAG 对比度计算（可复现）

脚本（sRGB 相对亮度 + alpha 合成，与 mygo 绘制假设一致）：

```python
def srgb(c): c/=255; return c/12.92 if c<=0.04045 else ((c+0.055)/1.055)**2.4
def lum(rgb): r,g,b=[srgb(v) for v in rgb]; return .2126*r+.7152*g+.0722*b
def comp(fg,a,bg): return tuple(round(f*a+b*(1-a)) for f,b in zip(fg,bg))
def ratio(a,b): la,lb=lum(a),lum(b); hi,lo=max(la,lb),min(la,lb); return (hi+.05)/(lo+.05)
```

| 组合 | 结果 | 判定 |
|---|---|---|
| 亮色焦点环 royal@80% on 白 | #3D69F8，**4.59:1** | ✅ ≥3 |
| 亮色焦点环 on BgBase #FFFFFF / BgSidebar #F7F8FA | **4.59 / 4.38:1** | ✅ |
| 亮色焦点环 on 旧侧栏 #F9FAFB（参照） | **4.45:1** | ✅ |
| 暗色焦点环 cyan@70% on page #111118 | **6.57:1** | ✅ |
| 暗色焦点环 on sidebar #0B0B10 | **6.70:1** | ✅ |
| 暗色焦点环 on surface #1B1B26 | **6.18:1** | ✅ |
| 滚动条 thumb（亮 ink22%）on #F7F8FA | **1.62:1** | ⚠ 低于 3:1（A4-I-09） |
| 滚动条 thumb（暗 white24%）on #111118 | **2.13:1** | ⚠（A4-I-09） |
| 亮色 hover 4.5% vs selected 9% | 亮度比 1.05（靠色相区分） | ✅ 可区分 |

### 5.5 限制与未覆盖

1. **未验证**：焦点环在 `Clip()` 容器内是否被裁切（需逐控件像素采样，本轮只核对了原生件的内缩环实现与 `markdown_code.go:35` 的 `Clip()` 组合，列为待证）。
2. **未量化**：IME 组合输入、真实读屏器体验、触屏/笔的手势路径（产品无自定义手势；图标命中区 ≥24 已满足）。
3. **未覆盖**：设置页 8 个页面的字段级交互、连接器 OAuth 流程交互、上下文页明细、toolcards 渲染细节、welcome 模块（见 §4-7）。
4. **并行改动**：审计期间 `internal/brand`、`internal/design`、`internal/uikit/iconbutton.go` 被其他成员修改（选择态 12%→9%、新增 motion/kbd）。本报告结论已按审计时的工作区状态核对；合并后若这些文件再变，A4-I-09/I-13/I-14 需重核。
5. **探针可复现性**：探针源码已按任务要求删除；§5.3 给了函数名与断言意图，重建约 200 行（`ui.NewTester` + `assembly.LoadWithBuildInfo` 直接照搬 `internal/devtools/ui-drive/main.go:148-190` 的 boot）。