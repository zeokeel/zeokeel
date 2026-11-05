# UI/UX 审计报告（ZeoKeel）

> 审计者：uiux-auditor · 日期：2026-11-05 · 代码核验 HEAD：`20f1399`（首次阅读时为 `27d5d5f`；21:15 构建的 `dist/zeokeel-linux-amd64` 即该区间二进制，UI 代码未变）
> 方法：Xvfb(:99,1600×1000) 真跑 `dist/zeokeel-linux-amd64`（独立数据目录 + 独立 netns，`unshare -n`）+ xdotool/import 驱动截图；`internal/shell` / `internal/brand` / `internal/design` 代码阅读；22 张 `docs/design/preview/` 逐张 `read_image`；`internal/shell` 单测与 i18n 表抽查。
> 限制：无真实桌面偏好注入 → HighContrast / 系统 TextScale / 真机 IME / 高 DPI 未动态验证；真实模型由本地 falseserver（OpenAI 兼容 SSE，127.0.0.1）替代，只验证 UI 状态机，不验证模型质量；确认弹层、窄窗滚动态只做了代码+静态帧核验。

## 0. 结论摘要

界面骨架、品牌令牌、深浅主题、原生控件与桌面窗口行为整体过关：真跑下 Home/Session/Settings/Plugins/Automation/右栏/快捷键/命令面板/审批卡都能渲染且交互，22 张预览与运行时观感一致。**主要风险集中在“失败/空态反馈”和“状态残留”**：无模型发送只在存储层失败、UI 完全静默并留下一个空会话；新会话对话页没有空态；打开设置窗口不关闭主窗口的悬浮菜单；对话框输入无可见标签。这些问题与 round3/round4 已修项不重复，且多数可用单测锁死。

| ID | 严重度 | 标题 | NEW/KNOWN | 修复量 |
| --- | --- | --- | --- | --- |
| UX-01 | P1 | 无模型/无 agent 发送完全静默：无 toast、消息不落库、留下空会话、草稿回填后再 Enter 仍无反馈 | NEW | M |
| UX-02 | P1 | 新会话「对话」页无空态（同一屏「轨迹」有），空会话屏只剩白底 + composer | NEW | S |
| UX-03 | P1 | 发送失败类 toast 路径系统性不一致：composer 的 Send 失败走 `c.Toast`（被 mygo 丢弃），slash 命令走 `toastText`→`queueToast` 才可见 | NEW | S |
| UX-04 | P2 | 打开 Settings 独立窗口时主窗口悬浮菜单不关闭；菜单被设置窗口半遮，关闭设置后仍在（状态残留/双窗口 z-order） | NEW | S |
| UX-05 | P2 | 「添加工作区」对话框两个输入无可见 label/placeholder，无法区分名称与路径 | NEW | S |
| UX-06 | P2 | Settings 独立窗口（900×720）首屏底部行被窗口边缘裁切，初始无滚动条提示 | NEW | S |
| UX-07 | P2 | 双入口/双主按钮仍成立：Settings>模型（添加模型×2）、Automation（新建×2）、Plugins（新建会话+添加插件） | KNOWN（round4 P1-8 未修） | M |
| UX-08 | P2 | 侧栏折叠成 56px 图标栏后，会话列表不可见也不可达（只能按工作区图标切换） | NEW | M |
| UX-09 | P2 | Home 无工作区时侧栏「工作区」区为空无任何提示；添加工作区入口只藏在 composer chip 里 | NEW | S |
| UX-10 | P2 | `@` 提及菜单会话行硬编码英文前缀 `session · `，中文界面混排；两行同名会话无区分信息 | NEW | S |
| UX-11 | P2 | 窄窗 900×600：权限 chip 退化为图标+箭头，当前权限值不可见（仅 a11y Label）；composer 与 transcript 列宽不一致 | NEW | S |
| UX-12 | P2 | Home 的 Ctrl+K 语义是「会话外打开搜索」，会话内只聚焦输入框；没有全局命令面板，`/` 面板仅存在于 composer | NEW（信息架构缺口） | L |
| UX-13 | P2 | 右栏「开始」页装饰 brand 图标（圆形 sparkle）像按钮/加载态，内容垂直居中留白过大 | NEW | S |
| UX-14 | P3 | toast 固定底部居中：Home 压住 composer 工具栏、Settings 里压住表单行（截图为证） | NEW | S |
| UX-15 | P3 | 失败后「重试」会把用户消息再次抽入 transcript，但第二次的失败过程卡不可见（仅 footer 步数变化） | NEW | M |
| UX-16 | P3 | 设置页「字号」调到 16px 仅影响对话内容，界面上无可预览反馈（说明文案已注明，属设计取舍） | NEW | S |

## 1. 范围与方法

**运行命令（可复现）**

```sh
Xvfb :99 -screen 0 1600x1000x24 &
# 单实例锁是固定端口 127.0.0.1:49731（internal/app/app.go:112-121），并行审计用独立 netns：
nohup unshare -n -- sh -c 'ip link set lo up; exec /data/mygo/dist/zeokeel-linux-amd64' \
  >/tmp/zk-app.log 2>&1 &   # env: ZEOKEEL_DATA_DIR=/tmp/zk-audit-uiux
DISPLAY=:99 import -window root shot.png
DISPLAY=:99 xdotool mousemove X Y click 1 / type / key ctrl+comma
```

**覆盖屏幕/状态**（全部真跑截图，`.cache/audit/uiux/shots/`）：Home 空态（无工作区/无模型）、Home 工作区/模式/权限/模型/加号菜单、添加工作区对话框、首次发送→建会话、Settings 覆层与独立窗口（浅/深/模型页/字号）、Plugins、Automation、侧栏展开/折叠/搜索、Ctrl+K、Ctrl+/、右栏 Start/Files/Terminal/Log、Session 空态、slash 面板、`@` 提及、真实回合（本地 fake provider：用户气泡→工具卡 running→审批卡→允许/拒绝→回答→完成行→轨迹）、浅/深主题、900×600 窄窗、英文界面。**共 80+ 张运行截图**，关键图均 2× 放大复核。

**代码核验**：`internal/shell/{home,session_live,composer_style,composer_menus,workspaces,settings_ui,toolcards,start_tab,trajectory,i18n}.go`、`internal/brand/palette.go`、`internal/design/{tokens,motion}.go`、`internal/app/settings_window.go`、`internal/session/controller.go`；`docs/design/{native-ui.md,review-round3.md,review-round4.md,known-limitations.md,preview/22张}`。

## 2. 详细发现

### [UX-01] 无模型/无 agent 发送完全静默 · P1 · NEW

- **现象**：Home composer 输入文字按 Enter（已有工作区、未配置模型）→ 创建会话并跳到 Session 屏；但 ①用户消息没有进 transcript；②草稿被回填到会话 composer；③屏幕无 toast、无错误行、无重试入口；④侧栏留下一个「新建会话」（SQLite `messages` 0 行）；⑤在会话 composer 再按 Enter 仍然毫无反应（连错误提示都没有）。
- **证据**：
  - 截图：`../../.cache/audit/uiux/shots/12-send-0.5s.png`、`14-send-3.5s.png`（空 transcript + 回填草稿，0.5s/3.5s 均无 toast）；`16-retry-enter-0.5s.png` 与 `15-retry-enter-0.15s.png` 像素级相同（sha256 一致），证明 Enter 未产生任何可见变化；`25-new-session-click.png` 的侧栏出现两条同名「新建会话」。
  - 运行证据：`sqlite3 /tmp/zk-audit-uiux/index.sqlite "select count(*) from messages"` → `0`（sessions 1 行、title「新建会话」）。
  - 代码：`internal/shell/home.go:326-334`（`Ctrl.Submit` 失败 → `saveSessionDraft` + `c.Toast("尚未配置 Agent…")`）；`internal/shell/session_live.go:820-825` 同样的 `c.Toast`。
- **影响**：用户的核心动作（发送）失败且零反馈；空会话被持久化污染侧栏与会话列表，看起来像「发丢了」。这是本次审计最严重的 UX 缺陷。
- **复现**：全新 `ZEOKEEL_DATA_DIR` → 建工作区 → Home 输入任意文案 Enter → 观察 Session 屏与 `messages` 表。
- **建议修复**：把 compose 发送路径的失败 toast 统一改走 `a.toastText(...)`（`composer_menus.go:364-372` 已有“next frame 再 Toast”的 helper）；失败后 **不要创建/保留空会话**（或创建后立即回滚 `DeleteSession`），保留 Home 草稿；给 Session 空态加「尚未配置模型 → 打开设置 > 模型」CTA。工作量 M（toast 统一 S，回滚+CTA M）。

### [UX-02] 新会话「对话」页没有空态 · P1 · NEW

- **现象**：新建会话后 Session 屏「对话」tab 是整片空白（1600×1000 只有 header + 底部 composer），没有任何新手引导/空态文案；而同屏「轨迹」tab 有「暂无事件。发送消息以开始追踪。」、右栏 Files「此目录为空。」、Log「暂无事件记录。」、Automation 有空态插画+CTA。
- **证据**：`../../.cache/audit/uiux/shots/25-new-session-click.png`、`26-ctrlk-palette-session.png`（对话 tab 空白）对比 `49-session-trajectory.png`、`51-right-files.png`、`53-right-log.png`、`24-automation.png`（均有空态）；代码 `internal/shell/trajectory.go:164` 有 `session.trajectory_empty`，`session_live.go` 全文件没有任何空 transcript 分支。
- **影响**：首启/新建会话时用户面对白色空白，不知道下一步该做什么；空态风格在同一产品内不一致。
- **复现**：侧栏「新建会话」→ 切到「对话」tab。
- **建议修复**：在 transcript 为空且无 pending 工具时渲染与 Trajectory 同语法的空态（图标 + 一句引导 + 可选示例 prompt chips）；无模型时复用「配置模型」CTA。工作量 S。

### [UX-03] 发送失败类 toast 路径系统性不一致 · P1 · NEW

- **现象**：同一个 UI 里，slash 命令的结果 toast 能看到（如 `/clear` → 「已清空输入框草稿」），而 Send 失败（`ErrNoAgent`、网络失败、`发送失败：%v`）的 toast 永远不出现。
- **证据**：截图 `21-slash-palette-clear.png` → `22-slash-clear-toast.png`（toast 可见）对比 UX-01（不可见）。代码：`composer_menus.go:364-372` 注释明写「Outside a build pass mygo drops Context toasts; keep the message for the next frame」，因此 slash 走 `queueToast`→`flushDeferredToast`（`app.go:279-281`）；而 `home.go:326-334`、`session_live.go:811-825` 在 Enter/按钮回调里直接 `c.Toast(...)`，被丢弃。同类问题还有 `composer_menus.go:311-320`（`/plan` 失败）等直接 Toast 的调用点。
- **影响**：所有非 slash 命令触发的失败反馈都可能静默，属于类缺陷而非单点；会掩盖网络/权限/模型错误。
- **复现**：任何键路径触发的 `c.Toast`（Enter 发送失败、`/plan` 参数失败）；对照 slash 命令 toast。
- **建议修复**：把 `toastText` 提升为 `App` 通用 helper（或在 `View` 里 flush），替换正文直接 `c.Toast` 的键处理路径；补 `TestSendFailureToastVisible`。工作量 S。

### [UX-04] 打开设置窗口时主窗口悬浮菜单不关闭（状态残留） · P2 · NEW

- **现象**：在 Session composer 打开「标准模式」菜单，再 Ctrl+, 打开 Settings 独立窗口：菜单仍然存在，被设置窗口压住一半（只露出上缘），背景主窗口暗色化后依旧悬浮；关掉设置窗口后菜单还在原位，且可继续交互。截图里还能看到浮动菜单与设置窗口重叠的「半截」文字。
- **证据**：`../../.cache/audit/uiux/shots/42-settings-open.png`（浅色：菜单上半截露出）、`43-settings-dark.png`、`44-settings-models.png`、`47-dark-session-no-settings.png`（关闭设置后菜单仍完整显示）。代码：`liveMenus()` 状态没有在 `openSettings()` 时清理；设置窗口是独立窗口，不触发主窗口菜单的 dismiss。
- **影响**：窗口层级错乱、视觉脏；用户可能误点主窗口残留菜单。属于「双入口/状态残留」类体验缺陷。
- **复现**：Session header 点「标准模式」→ 不关闭 → Ctrl+,。
- **建议修复**：`openSettings()`/其他窗口打开时统一 `closeAllPopovers()`（existing `dismissComposerPopup` 可复用）；或者设置窗口获得焦点时主窗口 popover 自动收起。工作量 S。

### [UX-05] 「添加工作区」对话框输入无可见标签 · P2 · NEW

- **现象**：Home → 选择工作区（无工作区时）直接弹「添加工作区」：两个输入框上下排列，**都没有可见 label，也没有 placeholder**；用户无法判断上面是名称、下面是根路径，只能靠下方提示「在此工作区创建的会话以其根目录为工作目录。」猜。填错后「创建」只报「工作区名称/根路径不能为空」。
- **证据**：`../../.cache/audit/uiux/shots/03-home-workspace-menu.png`、`09-home-send-no-ws.png`、`10-add-ws-filled.png`（填写后仍无 label）；代码 `internal/shell/workspaces.go:1196-1204` 只调用 `ui.TextInput(...).Label(...)`，没有可见文案（mygo 的 Label 只进 a11y 树）；同页 `workspace.sessions_created_...` 提示存在，说明作者知道要解释。
- **影响**：新用户第一次配置工作区就卡住/填错，a11y 名称有但视觉没有。同类表单（如 Settings 行）都有可见标题，只有该对话框缺失。
- **建议修复**：`ui.Row` 中加可见 `ui.Text(label)`，或改用带浮层 label 的 `ui.TextInput` 变体 + placeholder（如「我的项目」/「/home/me/project」）。工作量 S。

### [UX-06] Settings 独立窗口首屏底部行被裁切、无滚动提示 · P2 · NEW

- **现象**：900×720 设置窗口打开时，最后一个分组「忙碌时的发送行为」整行被窗口底边切断（只露出行标题与半个 select），静态帧中看不到滚动条（mygo scrollbar 是 hover overlay，6 DIP），用户无从得知下面是可滚动的。
- **证据**：`docs/design/preview/settings-window-native-light.png` 与 `settings-window-native-dark.png`（两张金标图同样裁切，非运行时偶发）；运行时 `../../.cache/audit/uiux/shots/42-settings-open.png`、`43-settings-dark.png`、`46-settings-font-large.png`。代码 `internal/app/settings_window.go:220-221`（`settingsWindowWidth=900, settingsWindowHeight=720`）。
- **影响**：设置最后一项（会影响 Enter 行为）被藏；用户可能以为设置内容就这些。滚动条策略属已知框架限制（known-limitations §2），但「初始视口刚好切一半行」是布局问题，可避免。
- **复现**：Ctrl+,（独立窗口宿主）→ 看首屏底部。
- **建议修复**：窗口高度按内容取整/给列表底部增加 `Space(6)` 内边距并确保首屏最后一个分组不跨视口裁切；或加「滚动指示」渐隐条。工作量 S。

### [UX-07] 双主按钮 / 双入口仍成立 · P2 · KNOWN（round4 P1-8 未修）

- **现象**：①Settings > 模型 空态：右上「添加模型」实心主按钮 + 居中「添加模型」实心按钮；②Automation：右上「新建」实心 + 空态「新建自动化任务」按钮；③Plugins：侧栏「新建会话」实心 + 页头「添加插件」实心。一个视口出现两个同权重主操作。
- **证据**：`../../.cache/audit/uiux/shots/44-settings-models.png`、`24-automation.png`、`23-plugins.png`；预览 `docs/design/preview/{plugins,automation}-native-*.png`。round4 已记录（P1-8），本轮复核**现状仍成立**。
- **影响**：主次不分、视觉噪音；empty-state CTA 与页头 CTA 重复。属设计主张/打磨，不阻塞使用。
- **建议修复**：空态里只保留一个主 CTA（页头降级为 ghost/次级），或空态 CTA 成为唯一入口。工作量 M。

### [UX-08] 折叠侧栏无法访问会话列表 · P2 · NEW

- **现象**：Ctrl+B / 标题栏按钮折叠侧栏后只剩 56px 图标栏：品牌标、展开、新建会话、插件、自动化、工作区图标，**没有任何会话入口**；同一工作区内的多个会话在折叠态完全不可见/不可点（只能点击工作区图标触发折叠/切换）。
- **证据**：`../../.cache/audit/uiux/shots/31-ctrl-b-collapsed.png`、`34-rail-expand-hover.png`（rail + tooltip「展开侧边栏」）、`48-session-right-panel.png`；代码 `internal/shell/chrome.go:146-180`（rail 只渲染 destinations + workspaces，没有 sessions）。
- **影响**：把侧栏当常用屏幕的用户折叠后必须展开才能切会话；与 DSH/常见桌面应用的 icon-rail 期望（hover 弹出会话列表）不一致。
- **建议修复**：rail 的工作区图标 hover 弹 flyout 列出该工作区的会话（或 rail 底部加「最近会话」图标）。工作量 M。

### [UX-09] Home 无工作区时侧栏「工作区」区无空态 · P2 · NEW

- **现象**：全新数据目录 Home：侧栏「工作区」标题 + 搜索/排序/添加三个图标，下面整块空白，没有「暂无工作区」「添加工作区」文案或按钮；唯一入口是 composer 里的「选择工作区」chip（点击后才弹添加对话框，见 UX-05）。
- **证据**：`../../.cache/audit/uiux/shots/02-home-empty-1600.png`（侧栏空白）、`03-home-workspace-menu.png`（点 chip 才出对话框）、`11-home-after-ws-create.png`（创建后才有列表）。对比 Automation/Files/Log/Trajectory 都有空态。
- **影响**：新用户的第一屏不知道该先建工作区；「工作区」三枚图标含义不明（搜索/排序/添加无文字）。
- **建议修复**：空态行加「暂无工作区 · 添加」按钮；给三枚图标补 tooltip（搜索已有，排序/添加未见）。工作量 S。

### [UX-10] `@` 提及菜单会话行中文混排 + 同名会话不可区分 · P2 · NEW

- **现象**：中文界面输入 `@`，会话行显示 `session · 新建会话`（英文前缀硬编码）；行尾还有「会话」类型标签，信息重复；两条同名会话完全一样，无 workspace/时间等区分信息。
- **证据**：`../../.cache/audit/uiux/shots/50-mention-menu.png`；代码 `internal/shell/composer_menus.go:25-29`（`sessionMentionPrefix = "session · "` 硬编码，中文分支不翻译）。
- **影响**：i18n 中英混排（round3 P1-5 同类但未被发现的新实例）；同名会话选错概率高。
- **建议修复**：前缀改 `a.tr("session · ", "会话 · ")`，行尾只保留一种类型标识；同名时追加相对时间或 workspace 名。工作量 S。

### [UX-11] 窄窗 900×600 的权限 chip 与列宽问题 · P2 · NEW

- **现象**：窗口缩到 900×600 时，composer 工具栏的「工作区写入」chip 退化成只有盾牌图标 + 箭头（当前权限值在任何可见位置都不出现，只能点开菜单才知道）；同时 composer 与 transcript 列宽不一致（transcript 占据主区几乎全宽，composer 缩到约 406px 居中），两侧留白不对称。代码有 `narrowComposerLabelW` 判断（`home_live_menus.go:188-194`），属有意取舍，但没有 tooltip/可见替代。
- **证据**：`../../.cache/audit/uiux/shots/57-narrow-home-900x600.png`、放大图 `zm-narrow-toolbar2.png`；`80-narrow-session-900x600.png`（窄窗会话列宽差异）。
- **影响**：窄窗/小屏下「当前权限」是安全相关状态，却不可见；列宽不一致削弱版式一致性。
- **建议修复**：icon-only chip 加 `Tooltip("访问模式：工作区写入")`（已是 a11y Label，可同时做可见 tooltip）；窄窗 transcript 与 composer 使用同一 clamp 宽度。工作量 S。

### [UX-12] 没有全局命令面板；Ctrl+K 语义与任务预期不同 · P2 · NEW

- **现象**：Ctrl+K 在 Home/非会话态打开「会话搜索」（提示词为「聚焦输入框，会话外打开搜索」，见快捷键表），在会话内只聚焦 composer；产品内没有类似 DSH 的全局命令面板（跳转/新建/设置/插件等统一入口）。唯一「命令面板」是 composer 的 `/` 面板和 Ctrl+/ 的快捷键编辑对话框。
- **证据**：`../../.cache/audit/uiux/shots/26-ctrlk-palette-session.png`（Ctrl+K 后画面无变化）、`27-ctrlslash-shortcuts.png`（「聚焦输入框，会话外打开搜索」）、`21-slash-palette-clear.png`（`/` 面板）。这属于信息架构层面的能力缺口，是否要做由产品决定；UI 侧建议至少让 Ctrl+K 在会话内也给出可见结果（例如打开会话搜索）。
- **影响**：键盘用户跨屏跳转只能靠 Ctrl+/ 表里逐条记忆；与「全功能桌面 Agent」目标有差距（与 product-auditor 可能重叠）。
- **建议修复**：统一 Ctrl+K 为命令面板（命令目录复用 `/` 面板注册表），或在会话内改为打开会话搜索。工作量 L。

### [UX-13] 右栏「开始」页装饰图形像按钮、留白过大 · P2 · NEW

- **现象**：右栏 Start 页在 3 张卡片上方放了一个圆形底 + brand sparkle 的纯装饰图形；它看起来像图标按钮/加载态，但不可点；页面内容整体垂直居中偏下，上方大块空白。
- **证据**：`../../.cache/audit/uiux/shots/48-session-right-panel.png`（1600 宽）、`51-right-files.png`；预览 `docs/design/preview/right-sidebar-native-{light,dark}.png` 同样构图。
- **影响**：用户会尝试点击/等待；版面失衡。
- **建议修复**：去掉装饰或把它做成页头品牌标；内容改为顶部对齐并加一句空态引导。工作量 S。

### [UX-14] toast 固定底部居中的遮挡 · P3 · NEW

- **现象**：toast 固定在窗口底部居中（mygo 默认）：Home 创建成功后 toast 压住 composer 工具栏附近；Settings 保存后「设置已保存」压住「显示编码视图」行/开关；会话里 toast 压住 composer 工具行。
- **证据**：`../../.cache/audit/uiux/shots/11-home-after-ws-create.png`、`22-slash-clear-toast.png`（toast 压住「工作区写入」chip）、`43-settings-dark.png`、`84-settings-english.png`（`Settings saved` 压在 Show coding view 行上）。
- **影响**：短暂遮挡可交互内容，点击穿透风险（toast 4s）；属打磨项。
- **建议修复**：toast 上移（bottom offset 超过 composer 高度）或右上角显示。工作量 S。

### [UX-15] 重试流程的过程卡缺失 · P3 · NEW

- **现象**：对失败回合点「重试」后，transcript 只追加了用户消息 + 助手回答，第二次的工具过程卡没有出现（footer 从 `1 轮 2 步` 变 `2 轮 3 步`，但没有对应可展开的过程块）；同一屏右侧还残留两条细小滚动条残影。
- **证据**：`../../.cache/audit/uiux/shots/78-retry2.png`（对比 `65-fake-done.png` 的正常回合结构）。该场景由本地 fake provider 触发（第二次请求直接返回 tool_call 但审批被快速拒绝），实际真实模型下重试路径需复核；**定级 P3、标注「部分依赖 fake provider 场景」**。
- **建议修复**：重试回合沿用同一 per-turn 过程块渲染；补重试后快照的渲染测试。

### [UX-16] 字号设置无可视预览 · P3 · NEW

- **现象**：Settings 通用「字号 14px / 16px」切换后，设置界面本身无变化（设计上「仅影响对话内容」），在没有会话的 HOME 状态下用户看不到任何反馈。
- **证据**：`../../.cache/audit/uiux/shots/46-settings-font-large.png`（16px 时设置界面与 14px 完全一致）、`42-settings-open.png`；文案「仅影响对话内容」已说明，属取舍。
- **建议修复**：在设置行内嵌一行示例对话文本随字号缩放，或提供「打开示例会话」链接。工作量 S。

## 3. 已验证良好的方面

1. **真实渲染质量**：Xvfb 下 1600×1000 全屏可控，Home/Session/Settings/Plugins/Automation/右栏/对话框/菜单/斜杠面板/提及/审批全部渲染正确，无崩溃、无空白帧；启动日志只有 llvmpipe 提示与 AT-SPI bus 缺失（headless 环境噪声）。
2. **深浅主题与品牌令牌一致**：`settings-native-dark`/运行时截图里 #0D0D14/#13131C/#1B1B26 层级成立；深色主按钮 #3CBBFA + `AccentText #0D0D14` 实测对比约 9:1（无「白字压浅蓝」问题）；深色 Hero 标（mark-dark@2x）无白边（`zm-dark-hero-mark.png`）。
3. **审批状态机可用**：本地 fake provider 回合里，审批卡（允许/始终允许/拒绝 + 参数 + 工具名）、工具卡 running、拒绝后 error 行、助手回答、action row、footer usage、Trajectory 事件时间线全部正确联动（`62/65/74/79`）；审批卡在等待时确实阻塞回合，拒绝会把 `denied by user` 写进轨迹。
4. **键盘可达性基本成立**：Tab 能遍历到侧栏按钮（tooltip「视图选项」跟随出现）、快捷键对话框可编辑、`/` 面板可用 ↑↓/Tab/Enter/Esc（代码 `composer_style.go:15-90`），Escape 能关菜单。
5. **本地化质量**：切到 English 后 Home/Session/Settings/Automation 全部英文，无溢出/裁切（`84-settings-english.png`、`85-session-english.png`）；中文文案整体口语化、无 DSH 残留「Preview/Into the Unknown」。
6. **错误/空态的正例**：Trajectory 空态、Files「此目录为空。」、Log「暂无事件记录。」、Automation 空态插画、toolcard 错误面板（DangerText #B91C1C，浅色 5.7:1）都符合预期；审批卡拒绝后的错误文本可见。

## 4. 已知问题复核（round3 / round4 / known-limitations）

| # | 旧问题 | 本轮状态 | 证据 |
| --- | --- | --- | --- |
| P0-1 | 破坏性操作无确认 | ✅ **代码核验已修**（未跑弹层视觉） | `automation_ui.go` 的 `automationDeleteDialog`、`memory_ui.go` 的 `memoryClearDialog` 存在；本轮未驱动到该弹层（见 §5） |
| P0-2 | 浅色三级文字 < AA | ✅ 已修 | `brand/palette.go:150` `TextTertiary #646C7A`（5.29/5.06/4.76:1） |
| P1-3 | 深色字标白边 | ✅ 已修 | `55-home-dark.png` + `zm-dark-hero-mark.png`（96×96 深色标，无白边） |
| P1-4 | Settings 双/三重关闭 | ◐ **窗口宿主已收敛为 1 个 X**，overlay 宿主仍「关闭」文字 + X（2 个） | 运行时 `42-settings-open.png` 仅 X + 打开配置文件；预览 `settings-native-*.png`（overlay）仍有「关闭」按钮；代码 `settings_ui.go:434-439`（`hasSettingsWindowHost()` 隐藏文字按钮） |
| P1-5 | Automation 中英混排 | ✅ 已修（schedule 文案中文）；残留 next/last 数字时间格式（预览 `automation-native-*.png` 显示「每天 00:00」） | 预览 + `24-automation.png` |
| P1-6 | TextScale 覆盖不全 | ◐ 代码已扩到 `design.Tokens.TextScale`（`settings_ui.go:96-118`）+ `Tokens.Font` 角色；本环境无法注入桌面 TextScale 做动态验证 | `known-limitations.md §2` 记录剩余 169 处显式 FontSize |
| P1-7 | 预览/CI 视觉回归不足 | ◐ 22 张金标 + opt-in `scripts/visual-check.sh`/`TestVisualRegression`；**空态/错误态/确认弹层/窄窗/HC/焦点仍未进预览集** | `known-limitations.md §4` 自述；预览目录无上述图 |
| P1-8 | 视觉层级/Web 感（双主按钮） | ❌ 仍成立（见 UX-07） | `23/24/44` 截图 |
| P2-9 | 细节一致性包 | ◐ 部分：卡片圆角两套仍在（列表卡 16 / 面板卡 10）；tab 语法两套（Session 下划线 tab vs 右栏文本高亮 tab）；`plugins.go` 采样色已改 token | `49-session-trajectory.png` vs `48-session-right-panel.png` |
| P2-10 | 浅色焦点环/轻边框对比 | ❌ 未修：`palette.go:159` Focus 仍 `RGBA(13,68,246,0.55)`（合成约 2.7:1）；Border 10% 约 1.24:1；仍未进 known-limitations | `brand/palette.go:150-160` |
| round4 P1-新-1 | 工具卡错误面板 4.28:1 | ✅ 已修（新增 `DangerText`，`toolcards.go:370/382/390` 使用） | `toolcard-native-light.png` 错误面板 |
| round4 P2-新-3 | 新确认弹层没有预览 | ❌ 仍未纳入预览集（22 张无弹层） | `docs/design/preview/` |
| round4 P2-新-4 | README/known-limitations 漂移 | ◐ `known-limitations.md` 已大改（§3.5/§3.6 记录了修复与新限制），README 未复核 | 文档抽查 |

## 5. 未验证 / 可疑项

1. **HighContrast 与系统 TextScale**：`c.Preferences()` 来自桌面宿主，Xvfb 无 desktop portal，无法注入；`gsettings`/mygo 测试钩子均未提供。因此「HC 下边框/焦点/滚动条增强」「1.5x/2x 关键屏不裁切」只有代码与已知限制文档结论，**未做运行验证**。
2. **Settings 字号 16px 的实际会话效果**：本环境无真实模型会话语料，仅验证设置界面本身不缩放（符合「仅影响对话内容」文案）；会话正文 14/16px 视觉未对比。
3. **破坏性确认弹层视觉**（Automation 删除、Memory 清空、工作区删除）：只做了代码核验，未驱动真实弹层截图。
4. **审批鼠标点击路径**：本轮对审批卡按钮的若干次程序化点击（允许/拒绝/Stop）未稳定生效（键盘 Tab+Enter 路径生效并在轨迹里留下 `denied by user`）；无法区分是 xdotool 合成时序问题还是按钮在持续重绘帧中的命中问题，**标记为待验证**，不下结论。
5. **真实模型回合**：未使用真实 DeepSeek 端点（成本/网络约束）；所有会话态由本地 OpenAI 兼容 fake provider 生成，只验证 UI 与状态机，不验证真实流式/推理内容渲染（reasoning 折叠、usage 数字真实性）。
6. **IME、高 DPI、真机窗口装饰/通知**：headless 无法验证；known-limitations §1 已列。
7. **窄窗下右键菜单、拖拽排序、会话拖拽**：未驱动。
8. **插件/自动化新增对话框的表单校验与错误态**：未逐项驱动（时间盒），只看了列表页与 Settings 模型空态。

## 6. 优先级建议（按投入产出）

1. **修 UX-01/UX-03（同一根因）**：发送失败反馈 → 统一走 deferred toast + 失败不留下空会话；S~M，收益最高（最高频失败路径从「无反应」变成「有解释」）。
2. **补 UX-02 空态**：对话空态与 Trajectory 同语法 + 无模型 CTA；S。
3. **UX-04 popover 生命周期**：打开设置/切换屏幕时统一关闭悬浮菜单；S，消灭最明显的「状态残留」。
4. **UX-05 工作区对话框可见标签 + UA-09 侧栏工作区空态**：S，首次使用路径。
5. **UX-06 设置窗口底部裁切 + UX-11 窄窗权限可见性**：S，小屏/窗口用户。
6. **UX-07 双主按钮收口（v1.1）**：M。
7. **UX-08 rail 会话 flyout / UX-12 全局命令面板**：M~L，属「全功能桌面 Agent」信息架构补强，建议进 v1.1 计划。
8. **预览状态矩阵**（known P1-7 收尾）：把空态/错误态/确认弹层/窄窗/HC 加入 22 张金标，S~M，能防止 UX-02/06 类问题复现。
