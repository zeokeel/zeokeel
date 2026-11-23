# WEB-CLIENT

> 浏览器客户端（`web/`）的设计契约：信息架构、屏面等价映射、键盘/焦点、拖放、流式、验收。
> 状态：**草案**（2026-11-23）。栈已定 React + TypeScript + Vite；**组件库未定**，本文所有涉及组件库的段落只写行为要求，库名待 `docs/plans/web-ui-framework.md`。
> 配套：[docs/TRANSPORT.md](TRANSPORT.md)（线协议）、[docs/ARCHITECTURE.md](ARCHITECTURE.md)（Go 侧边界）、[docs/APPROVAL-SURFACES.md](APPROVAL-SURFACES.md)（审批面行为）、[docs/DESIGN.md](DESIGN.md)。
> 依据：`.cache/pivot/native-ui-inventory.md`（原生 UI 清点）、`.cache/pivot/contracts-projection.md`（契约投影）。
> 标注：**[新增]** = 原生无此行为；**[设计建议]** = 本文提出、尚未验证；**[占用]** = 语义已定、实现待逐页填充。

---

## 0. 等价性口径

「完全等价」指：**今天桌面端能做的操作与能看到的信息，在浏览器端都能做、能看**（`docs/WEB-CLIENT.md` §2 逐条映射）。凡是浏览器物理上做不到的（系统级全局热键、托盘、原生目录选择器），逐个列在 §2.7「明确不做」并给出替代路径，**不静默降级**——这条沿用原生壳层的既有原则：宿主不认识的贡献必须显式暴露，不得静默丢弃（`docs/ARCHITECTURE.md:76`）。

原生侧待退役清单：`internal/ui` 40,602 行 / `internal/uiroot` 5,560 行 / `internal/uikit` 8,263 行（`.cache/pivot/native-ui-inventory.md` §1.0）。

---

## 1. 信息架构与路由

### 1.1 三面 + 设置

| 面 | 路由 | 回答的问题 | 对应原生的什么 |
|---|---|---|---|
| **Now** | `/` | 「现在是什么状态、我该说什么」 | `home` 屏（`internal/ui/home/home.go:131`）+ 前台会话（`internal/uiroot/foreground.go:20`） |
| **Inbox** | `/inbox` | 「有什么在等我」 | 原生**没有**这个屏；它是 5 个既有打断源的**汇聚**（§1.4）[新增汇聚，不新增能力] |
| **Thread** | `/c/:sessionId` | 「这一轮到底发生了什么」 | `session` 屏（`internal/ui/session/session.go:695`） |
| 设置 | `/settings/:page` | 「配置」 | 设置 Modal（`internal/uiroot/frame.go:2253`）+ 9 个设置页（`internal/ui/settings/settings.go:486`） |

### 1.2 完整路由表

| # | 路由 | 屏面/组件 | 职责 | 深链 | 刷新后如何恢复 |
|---|---|---|---|---|---|
| R1 | `/` | Now | 前台会话状态的**一行摘要** + composer + 最近会话。首条消息创建会话后跳 R3（同 `home/composer.go:1-6` 的既有行为） | ✅ | 读网关状态中的 `foregroundSessionId`；无则渲染空态 hero |
| R2 | `/inbox` | Inbox | 5 类打断的统一队列（§1.4） | ✅ | 全量快照，不缓存；刷新即重取 |
| R3 | `/c/:sessionId` | Thread · 对话 | transcript + composer + 运行态 | ✅ | `sessionId` → 一次性全量快照 → 之后 WS 差值；滚动位置按 `sessionId` 存 `sessionStorage`（原贴底则恢复贴底，原上滚则恢复锚点） |
| R4 | `/c/:sessionId/trajectory` | Thread · 轨迹 | 工具/推理时间线 | ✅ | Tab 在 URL，不靠内存 |
| R5 | `/c/:sessionId/memory` | Thread · 记忆 | 会话记忆 | ✅ | 同上 |
| R6 | `/c/:sessionId/context` | Thread · 上下文 | 上下文检查器；**后端无 `KeyContext` 时该 Tab 不出现**（同 `session/view.go:951` 的条件注册） | ✅（无后端时落到 R3，不 404） | 同上 |
| R7 | `/c/:sessionId/child/:childId` | Thread · 子会话 | 子代理/队友子会话 + 面包屑返回 | ✅ | 子会话身份 = 稳定的 `child-<8hex>`（`AGENT-SURFACES.md` S9），刷新可直接恢复 |
| R8 | `/c/:sessionId/files` | 右检查器 · 文件 | 工作区树 + 交付区历史 | ✅ | 树按目标 path 的祖先链自动展开 |
| R9 | `/c/:sessionId/files/*path` | 右检查器 · 文件预览 | 只读预览（text/Markdown/图片 + 诚实回退） | ✅ | `path` 全在 URL |
| R10 | `/c/:sessionId/files/*path?view=diff` | 逐文件 diff | unified diff（原生是"文件 Tab 内的折叠 diff"，`internal/ui/rightpanel/rightpanel.go:10-11`） | ✅ | `view=diff` 在 URL |
| R11 | `/c/:sessionId/guide` | 右检查器 · 开始 | guide 落地页（原生默认页，`rightpanel/view.go:70`） | ✅ | — |
| R12 | `/settings` | — | 301 → `/settings/model`（首选页 order=10，`settings.go:742`） | ✅ | — |
| R13 | `/settings/model` | 设置 · 模型 | 模型目录/默认模型/上下文预算 (`settings.go:741`) | ✅ | 页面草稿**不持久化**；刷新即丢弃（等同原生关闭守卫的 Discard 路径，`frame.go:1834-1845`）→ 需 `beforeunload` 提示 |
| R14 | `/settings/behavior` | 设置 · 行为与限额 (`settings.go:770`) | 沙箱/权限/工作文件夹 | ✅ | 同上 |
| R15 | `/settings/appearance` | 设置 · 外观 (`settings.go:809`) | 主题/语言/字号/快捷键 | ✅ | 同上 |
| R16 | `/settings/notifications` | 设置 · 通知 (`settings.go:842`) | 分级开关 | ✅ | 同上 |
| R17 | `/settings/network` | 设置 · 网络与搜索 (`settings.go:854`) | 代理 + 搜索 | ✅ | 同上 |
| R18 | `/settings/lsp` | 设置 · LSP (`settings.go:888`) | 服务器列表增删 | ✅ | 同上 |
| R19 | `/settings/archive` | 设置 · 归档 (`settings.go:921`) | 归档会话恢复/删除 | ✅ | 同上 |
| R20 | `/settings/data-privacy` | 设置 · 数据与隐私 (`settings.go:930`) | 导入/清除/文件隐藏 | ✅ | 同上 |
| R21 | `/settings/about` | 设置 · 关于 (`settings.go:961`) | 版本/更新/诊断 | ✅ | 同上 |
| R22 | `/automation` | 自动化屏 (`automation.go:178`) | 定时任务 | ✅ | — |
| R23 | `/capabilities` | 能力屏 (`plugins.go:159`) | 技能 / MCP 连接器 | ✅ | Tab 在 URL query `?tab=skills\|connectors` |
| R24 | `/insights` | 洞察屏 (`insights.go:125`) | 跨会话用量（只读） | ✅ | 范围 `?range=7\|30` 在 URL |
> **路由要么实现、要么不注册**（2026-11-23，task-33）。表里每一条都必须有真实屏；
> 不做"已注册的占位屏"——它会让人以为功能存在。后端缺半边时的正确机制是
> **capability 门控**（`initialize.capabilities` 里没有就不渲染入口，如「上下文」Tab），
> 而不是渲染一个空壳再加一句"暂未实现"。同理，`RouteMeta` 没有 `deferral` 字段：
> 它只描述过一种已经不存在的状态。
| R25 | `*` | 404 | 未知路由 | — | 提供「回 Now」 |
| R26 | `/team` | 团队屏 | 队友 / 子任务看板 | ✅ | 验收 C6 要求"入口零值常显"，原 25 条路由里没有落点，故新增；零值显示空态，**入口不显示计数**。`team.*` 域投影后只填数据，路由与形态不变 |

**设置页分组**（5 组 9 页，`frame.go:1879-1893` 的 rank 与 `settings.go:742-965` 的 Section 决定）：

| 组 | rank | 页（order） |
|---|---|---|
| 常用 | 12 | 模型(10)、行为与限额(20) |
| 界面与通知 | 30 | 外观(10)、通知(10) |
| 连接 | 50 | 网络与搜索(30)、LSP(50) |
| 数据 | 70 | 归档(30)、数据与隐私(40) |
| 关于 | 90 | 关于(10) |

### 1.3 右检查器为什么是路由而不是浮层

原生右检查器有两种呈现：**停靠面板**（宽窗）与**模态抽屉**（窄窗，`frame.go:750-778`、`frame.go:1167`）。浏览器端把「停靠/抽屉」变成**纯 CSS 断点行为**（同一路由，窄窗渲染为覆盖层 + 焦点陷阱），但**路径保持唯一** —— 于是：

- 深链能直接打开某个文件/diff（原生只能打开面板再点）；
- 刷新恢复零成本（`path`、`view` 都在 URL）；
- 窄窗打开抽屉时 `/settings` 式的脏守卫不适用（只读预览，无草稿）。

⚠️ 浏览器历史副作用：右检查器进出会**产生历史条目**。规则：仅在「用户主动打开/切换文件」时 `pushState`；由响应式断点自动折叠/展开时**不**写历史。

### 1.4 Inbox 的 5 类来源（不新增能力，只汇聚）

| 来源 | 原生表现 | 引用 |
|---|---|---|
| 待审批工具调用 | 会话 banner 槽上的内联审批卡（**不是模态**） | `internal/ui/approval/approval.go:1-14`、`approval.go:145` |
| `ask_user` 提问 | `overlay.dialogs` 上的 FIFO 队列 | `internal/ui/interaction/interaction.go:1-15` |
| 计划审阅 | 同上（队列内第三种） | 同上 |
| 产物预览（present） | 同上 | 同上 |
| 子代理/队友完成回执 | transcript 内 `ChatRoleNotice` 行 + 后台任务 chip | `AGENT-SURFACES.md` S9、`internal/ui/session/agentsurface.go:359` |

Inbox **不包含** toast：toast 是"我刚做的事的结果"（`toasts.go:44-50`，4s 自动消失，错误粘滞），不是"在等我做的事"。两者不可合并。

---

## 2. 屏面清单与等价映射

### 2.0 纠正任务书里的旧描述

> 任务书说右检查器是「Files / Terminal / Changes / Log 四窗格」——**这个形态已经不存在**。8 窗格检查器（overview/tools/log/changes/agents/tasks）在 2026 年重构中退役（`internal/ui/rightpanel/rightpanel.go:14-19`）。**现状是 2 个固定页 + N 个文件文档 Tab**：`pageFixedCount = 2`（`rightpanel.go:58-64`），标签由 `tabLabel` 只返回「开始 / 文件」（`rightpanel.go:249-255`）；变更以「文件 Tab 内的折叠 diff」存在（`rightpanel.go:10-11`）；Log 窗格已并入「导出原始日志 ZIP」（`internal/ui/session/view.go:671`）。
> 另外 `rightpanel/view.go:70` 的注释仍写「开始/文件/命令」三个固定页 —— 那是**文档漂移**，以 `pageFixedCount=2` 为准。

### 2.1 屏幕（5 个 ScreenSpec）

| 原生屏 | 注册点 | 行数 | Web 路由 | 版本 | 理由 |
|---|---|---|---|---|---|
| `session` 会话 | `session.go:695` | 15,358 | R3–R7 | **M1**（Thread 主体） | 里程碑四面之一 |
| `home` 首页 | `home.go:131` | 1,396 | R1 | **M1** | composer 的落点，`/` 的唯一内容；原生里它**不在侧栏也不在命令面板**（`frame.go:1633`），Web 侧保持一致：`/` 不进侧栏导航 |
| `automation` 自动化 | `automation.go:178` | 1,673 | R22 | **M2** | 纯 CRUD 屏，与 Thread 无耦合 |
| `plugins` 能力 | `plugins.go:159` | 5,031 | R23 | **M2** | 技能/连接器的编辑表单量大（含 1,286 行连接器编辑器） |
| `insights` 洞察 | `insights.go:125` | 1,058 | R24 | **M2** | 只读表格页，S11 规格完整可照抄 |

### 2.2 设置页（9 页）

| 页 | route | 版本 | 理由 |
|---|---|---|---|
| 模型 R13 / 行为与限额 R14 / 外观 R15 | — | **M1（前置）** | composer 的模型选择、权限模式、主题/语言不可缺；缺它们 Thread 无法日常用 |
| 通知 R16 / 网络与搜索 R17 / LSP R18 / 归档 R19 / 数据与隐私 R20 / 关于 R21 | — | **M2** | 低频；M1 期间用原生桌面端兜底（原生产品保留为回退路径） |

### 2.3 壳层 chrome（12 个面 → Web 组件）

| ID | 原生面 | 引用 | Web 归属 | 版本 |
|---|---|---|---|---|
| S-01 | 根布局 / 帧序 | `frame.go:45` | `<AppShell>`（顶栏 + 侧栏 + 主列 + 检查器 4 区 CSS Grid） | M1 |
| S-02 | 展开侧栏（可拖宽 + 持久化） | `frame.go:222`、`uiroot.go:304-380` | `<Sidebar>`；拖宽 → CSS `resize` 句柄 + `localStorage` | M1 |
| S-03 | 折叠图标栏 64 DIP | `frame.go:312` | 同一组件的 `collapsed` 态（不另立路由） | M2 |
| S-04 | 顶栏 band（48 + 36 DIP 两行） | `frame.go:888-926` | `<TopBand>`；标题/Tabs/动作三区 | M1 |
| S-05 | 内容列 + `screen.footer` | `frame.go:644,632` | `<Content>` | M1 |
| S-06 | 检查器抽屉（窄窗模态） | `frame.go:1167` | 断点行为，见 §1.3 | M1（R8–R11） |
| S-07 | 命令面板 640 DIP | `frame.go:1277` | `<Palette>` 浮层（§2.5-①） | M1 |
| S-08 | 设置面 | `frame.go:2253` | 路由 R12–R21（**不再用模态**） | M1 部分 |
| S-09 | overlay 层（`overlay.` 前缀通配） | `frame.go:1788` | `<OverlayStack>`（有序栈，非通配字符串） | M1 |
| S-10 | toast 锚点几何 | `toastanchor.go:11-19` | `<ToastViewport>` 按「有无浮层」两态定位（原逻辑见 `toastanchor.go:11-17`） | M1 |
| S-11 | 文件拖放桥 | `filedrop.go:5-7` | `dropzone` + 上传（§6） | M1 |
| S-12 | 导航状态（屏栈 + settings 开态） | `navigator.go:34,48` | 浏览器历史栈本身（`Back()` ↔ `history.back()`） | M1 |

### 2.4 检查器 2 页

| 页 | 引用 | Web | 版本 |
|---|---|---|---|
| 开始 Guide | `rightpanel/view.go:70,249-255` | R11 | M1（便宜） |
| 文件（树 + 交付区 + 预览 + diff） | `files.go:245-320`、`changes.go:15-22` | R8/R9/R10 | **M1**（M1 四面之外的必须项：没有它看不到 agent 改了什么） |

### 2.5 浮层 8 类

| # | 浮层 | 原生引用 | Web | 版本 |
|---|---|---|---|---|
| ① | 命令面板 | `frame.go:1277` | 浮层（不占路由）；分组：操作 / 前往 / 设置（`frame.go:1696-1698`） | M1 |
| ② | 审批卡（**内联，非模态**） | `approval.go:145`、`uikit/approval.go:8-11` | 内联卡，挂在 composer 上方 banner 位；**M1 同时在 Inbox 里出现同一实体** | M1 |
| ③ | `ask_user` 提问 | `interaction.go:190` | 模态对话框（队列一次一个） | M1 |
| ④ | 计划审阅 | 同上 | 模态对话框 | **M2** |
| ⑤ | 产物预览 present | 同上 | 模态 + 预览体 | **M2** |
| ⑥ | 通知栈 | `toasts.go:261` | 视口内 toast（≤2 同屏、4s / 错误粘滞，`toasts.go:47,53,65`） | M1 |
| ⑦ | 确认对话框（危险操作） | 15 处 `ConfirmDanger(` | 模态；**取消为默认焦点、危险动作非默认** | M1 |
| ⑧ | 重命名对话框 / 行内菜单 | `sidebar.go:358-366`、23 处 `PopoverMenuButton(` | 模态 / 锚定菜单 | M1 |

> 浮层总数口径：原生 `ui.Modal(` **22 处**、`PopoverMenuButton(` **23 处**、`ConfirmDanger(` **15 处**（实测 grep）。Web 不追求 1:1 数量，只追求 §8 的行为等价。

### 2.6 M1 / M2 / 不做 汇总

| 版本 | 内容 |
|---|---|
| **M1（可用）** | R1 `/`、R2 `/inbox`、R3 `/c/:id` + composer、②审批卡、R8–R11 右检查器、S-01/02/04/05/07/09/10/11/12、①⑥⑦⑧ 浮层、R13/R14/R15 三个设置页 |
| **M2（等价）** | R4/R5/R6/R7 Tab 与子会话、R16–R21 六页设置、R22/R23/R24 三屏、S-03 折叠栏、④⑤ 计划与产物、团队/子代理/后台任务浮层（S9 全套） |
| **明确不做** | 见 §2.7 |

### 2.7 明确不做（浏览器能力边界，逐条给替代）

| 做不到 | 原生引用 | Web 替代 |
|---|---|---|
| 系统级全局热键 | `internal/host/hotkey.go:5-8` | 只保留在托盘壳；web 设置页的该字段**只读 + 说明**（不隐藏，避免"以为坏了"） |
| 托盘图标 / 关闭到托盘 | `internal/host/tray.go:3-7` | 托盘壳 |
| 单实例锁 | `internal/host/host.go:415` | 网关层（端口协商） |
| 原生文件/目录选择器 | 12 处 `mygo.Dialog.Open/Save`（`home/composer.go:31`、`settings/import.go:24`、`settings/workfolder.go:33` …） | `<input type="file">` / 上传接口；目录选择用文件树选择或路径输入（**[设计建议]** 需产品确认） |
| "在 Finder/Explorer 打开路径" | `settings/data.go:43` `mygo.Shell.OpenPath` | 改为「复制路径」（原生已有 `c.WriteClipboard` 同类动作，`settings/data.go:166`） |
| 窗口拖宽持久化（`ui.Split`） | `uiroot.go:304-380` | CSS 句柄 + `localStorage`（**不是** 不做，是换实现） |
| 原生系统通知 | `host/notify.go`、`mygo.NotificationsSupported()`（`host.go:650`） | 托盘壳通知（浏览器 Notification API 权限不可靠，不与托盘壳抢） |

---

## 3. 被丢弃的扩展机制及其替代

### 3.1 `BuildFunc` 闭包模型的问题

原生贡献模型是**画图的代码**：`contracts.BuildFunc = func(c *ui.Context)`（`internal/contracts/contracts.go:17`），屏幕/槽位/设置页/工具卡/命令五类注册全部以它为核心（`contracts.go:25,34,44,156,311,314`）——这也是契约层唯一与 UI 框架耦合的地方。

Web 侧的 `ui.Context` 不存在，闭包无法跨进程传递。因此贡献模型必须**数据化**：

```
原生：模块 → RegisterScreen(spec{Build: func(c *ui.Context){...}})  → 宿主调 Build 画
Web ：模块 → 服务端 catalog（数据）  +  客户端 routes 表（静态映射） → 客户端渲染
```

### 3.2 线协议形状归 TRANSPORT，本节只定客户端契约

**线上 schema（`ScreenSpec`/`SlotItem`/`SettingsPageSpec`/`ToolRenderer`/`CommandSpec` 的数据化）以 [docs/TRANSPORT.md](TRANSPORT.md) §8 为准**，本文不重复定义，避免两处漂移。本节只规定**客户端**必须承担的 5 条契约：

| # | 客户端契约 | 说明 |
|---|---|---|
| C1 | **catalog 只读** | 客户端不推断 `visible` / `dirty` / `enabled`；这三个值由网关求值后随快照下发（原生是同步闭包：`contracts.go:24,51,166`，每页各自实现 dirty，见 `settings.go:763,800,836,881,915,955`） |
| C2 | **路由表是静态的** | `screenId → 路由组件` 是编译期映射（`web/src/features/routes.ts`）；catalog 里出现未映射的 `id` 必须**显式降级渲染 + 上报**，不得静默跳过（沿用 `docs/ARCHITECTURE.md:76` 的"保留槽位不静默丢弃"原则；[docs/TRANSPORT.md](TRANSPORT.md) §8.2 的 `kind` 降级与之一致） |
| C3 | **未知 `kind` 降级** | 未识别 `kind` 渲染为纯文本行并记录一条诊断，不抛错、不白屏 |
| C4 | **`Reveal` 不存在** | 原生 `contracts.SettingsSearchItem.Reveal`（`contracts.go:77`）在 Web 侧删除；页内定位由客户端本地滚动 + `anchorKey` 完成（[docs/TRANSPORT.md](TRANSPORT.md) §8.3） |
| C5 | **`Run` 走命令通道** | 原生 `CommandSpec.Run func() error`（`contracts.go:167`）在 Web 侧是命令 id → 网关方法的映射，客户端不得内联业务逻辑 |

> 服务端为 51 个服务键的投影工作量见 `.cache/pivot/contracts-projection.md`（其中 8 个是 UI 侧键，随原生 UI 一并退役）。

### 3.3 32 个保留槽位的去留

原生槽位常量共 **32 个**（`contracts.go:205-251`），其中只有 **11 个有贡献者**（`.cache/pivot/native-ui-inventory.md` §1.5）。

| 槽位 | 原生贡献者 | Web 是否成立 | 说明 |
|---|---|---|---|
| `sidebar.primary` / `.nav` / `.footer` | 1 / 1 / 2 | ✅ 成立 | 三区数据化：`newSession` 动作、`nav[]` 数组、`footer[]` 数组 |
| `sidebar.tree` | 0 | ❌ **消失** | 产品已改单工作区，树这一层 UI 被明确移除（`AGENT-SURFACES.md` S12「侧栏没有切换/管理/颜色 UI」） |
| `screen.header` / `.actions` / `.tabs` | 4 / 3 / 1 | ✅ 成立 | 顶栏三区；`tabs` 只在 session 有贡献者 |
| `screen.footer` | 0 | ⚠️ 保留位 | 无贡献者，M1 不实现，**但 catalog 里保留字段**以免将来加槽位要改协议 |
| `home.hero` / `.composer` / `.recent` | 2 / 0 / 0 | ⚠️ 合并 | `hero` 成立（`welcome` 会覆盖它，`welcome.go:60`）；`composer` 与 Thread 的 composer 是**同一组件**（S7），不重复实现；`recent` 与侧栏会话列表重复 → **消失** |
| `session.header.brand` / `.tabs` / `.actions` | 0 / 0 / 0 | ❌ **消失** | 与 `screen.header/tabs/actions` 语义重复（`ARCHITECTURE.md:118` 标 ⬜，从未有贡献者）→ 只保留一套 |
| `session.banner` | 1（审批） | ✅ 成立 | 审批卡 / 目标条 / 子会话条共用此位 |
| `session.transcript` | 0 | ✅ 成立 | S1（`AGENT-SURFACES.md:53`） |
| `session.trajectory` / `.trajectory.bar` | 0 / 0 | ✅ 成立 | R4 |
| `session.panel.left` | 0 | ❌ **消失** | 从未有贡献者 |
| `session.panel.right` / `.right.rail` | 1 / 0 | ✅ 合并 | 合并为右检查器路由组（R8–R11） |
| `session.composer.top` / `.input` / `.toolbar` | 0 / 0 / 0 | ✅ 合并 | S7 的结构分区，不是三个贡献点 |
| `session.status` | 0 | ✅ 成立 | S8 RunStatus |
| `settings.sidebar` / `.header` / `.page.body` / `.page.footer` | 0 ×4 | ✅ 成立 | 设置内容走 `SettingsPageSpec`，不走槽位（同原生，`frame.go:2371-2395`） |
| `overlay.*`（3 个 + 前缀通配） | 2 / 1 / 前缀 | ⚠️ 改造 | 原生按**字符串前缀通配**渲染（`frame.go:1788`）；Web 改为**显式有序栈**（每个浮层是栈内一项，带 `id/priority`），因为浏览器需要确定 z-index 与焦点陷阱归属 |

**净结果**：32 → **成立 16 / 合并 6 / 消失 6 / 保留位 1 / 改造 3**（合计 32）。

| 处置 | 数 | 明细 |
|---|---|---|
| 成立 | 16 | `sidebar.primary/.nav/.footer`(3)、`screen.header/.actions/.tabs`(3)、`home.hero`(1)、`session.banner`(1)、`session.transcript`(1)、`session.trajectory/.bar`(2)、`session.status`(1)、`settings.*`(4) |
| 合并 | 6 | `home.composer`(1) → 与 Thread composer 同一组件；`session.panel.right/.right.rail`(2) → 右检查器路由组；`session.composer.top/.input/.toolbar`(3) → S7 的结构分区 |
| 消失 | 6 | `sidebar.tree`(1，单工作区已移除树)、`home.recent`(1，与侧栏列表重复)、`session.header.brand/.tabs/.actions`(3，与 `screen.*` 重复且零贡献者)、`session.panel.left`(1，零贡献者) |
| 保留位 | 1 | `screen.footer`（零贡献者，M1 不实现，但协议保留字段） |
| 改造 | 3 | `overlay.dialogs/.toasts` + `overlay.` 前缀通配 → 显式有序栈 |

消失的 6 个槽位**不是丢功能**——其中 5 个本来零贡献者，第 6 个（`home.recent`）的功能由侧栏会话列表承载。

### 3.4 不成立的原生机制（明确放弃）

| 机制 | 引用 | 为什么不搬 |
|---|---|---|
| 帧内 `build` 回调 + `Invalidate` 重绘 | `frame.go:45-152`、`contracts.go:290-295` | React 用状态驱动；网关推**差值**而非每帧重建（`.cache/pivot/contracts-projection.md` §3.4） |
| `UIRuntime.Post` / 主线程亲和 | `contracts.go:291` | 浏览器只有一个线程模型，回调即事件；但**服务端**仍需保证同一会话的写序列化 |
| 每帧重建 `OnShortcut` 注册 | `shortcuts.go:301-303` | 改为单例键盘仲裁器 + 声明式 claim（§4.3） |
| 模块 `Effect` 逆序注销 | `ARCHITECTURE.md:47-48` | 客户端进程随页面结束；服务端 kernel 生命周期不变 |
| `RenderTool` 渲染器注册表 | `contracts.go:314`、26 个渲染器（`toolcards.go:51-88`） | 保留**概念**：工具名 → 组件的静态映射表（25 个真实工具 + `demo`），不认识的工具走通用行回退（同原生 `toolcards.go:19-22` 的"精确名匹配、无通配"） |

---

## 4. 键盘映射

### 4.1 总表

修饰键 `Mod` = macOS `Cmd` / 其他 `Ctrl`。原生侧全部快捷键来源：`uiroot/shortcuts.go:304-351`、`session/view.go:498-507,5465`、`uikit/composer.go:449-480`、`settings/hotkey_settings.go:91-130`。

| # | 原生键 | 原生语义 | 引用 | 浏览器冲突 | Web 新键 | 理由 |
|---|---|---|---|---|---|---|
| K1 | `Mod+N` | 新建会话 | `shortcuts.go:348` | **Chrome/Edge/FF 新窗口，不可 preventDefault** | **`Mod+Alt+N`**（主）；`Mod+N` 仅尝试拦截，失效不报错 | 与既有 `Mod+Alt+*` chrome 族一致（`shortcuts.go:332,345`） |
| K2 | `Mod+.` | 停止本轮 | `view.go:503`、`view.go:3465` | **Chrome/Safari「停止加载」** | **删除**；停止 = `Esc`（无浮层时）+ 显式停止按钮 | Esc 本就是原生等价路径（`composer.go:457-460`） |
| K3 | `Tab` | 执行 slash/@ 高亮项 | `view.go:4792`、`view.go:5082` | **Tab 是焦点移动铁律** | `Enter` 执行；`Tab` 仅补全文本，**不执行、不移焦** | 见 §5.2 |
| K4 | 窗口级 `Esc` | 拒绝审批 / 停止运行 | `shortcuts.go:258-297,368` | 浏览器 Esc 还有全屏/取消拖拽 | 保留 `Esc` + **分层仲裁器** | §4.3 |
| K5 | 拖入文件（绝对路径） | 附件 | `filedrop.go:22`、`view.go:3350` | 只有 `File` 对象 | 宿主代理上传 | §6 |
| K6 | `Mod+B` | 折叠侧栏 | `shortcuts.go:323` | Firefox 书签侧栏 | 保留 `Mod+B`（`preventDefault`）+ 别名 `Mod+Shift+B` | 别名兜底不可拦截的浏览器 |
| K7 | `Mod+K` | 命令面板 | `shortcuts.go:326` | Chrome/FF 地址栏搜索 | 保留 `Mod+K`（`preventDefault`）+ 别名 `Mod+Shift+P` | VS Code 惯例替代键普遍可拦截 |
| K8 | `Mod+Alt+I` | 检查器开关 | `shortcuts.go:332` | **macOS Chrome/Edge DevTools = Cmd+Opt+I** | **`Mod+Alt+R`** | 避开 DevTools；R = right |
| K9 | `Mod+Alt+P` | 右栏「文件」页 | `shortcuts.go:345` | 部分浏览器 `Mod+Alt+P` 被占 | **`Mod+Alt+F`** | F = files |
| K10 | `Mod+,` | 设置 | `shortcuts.go:349` | **macOS 浏览器「偏好设置」** | **`Mod+Alt+,`** | 同 K1 族 |
| K11 | `Mod+F` | 会话内查找 | `view.go:5465` | 浏览器查找 | 保留 `Mod+F`，**作用域限定 transcript** + `preventDefault`；`F3` 别名 | 虚拟列表让浏览器原生查找本就失效，覆盖反而更诚实 |
| K12 | `Mod+Enter` | 发送 / 审批允许 | `view.go:3462`、`uikit/approval.go:531` | 无 | **保留** | |
| K13 | `Enter` | 发送（空闲）/ 排队（运行中） | `composer.go:457-470` | 无 | **保留** | |
| K14 | `Shift+Enter` / `Alt+Enter` | 换行 | `composer.go:457` | 无 | **保留** | |
| K15 | `Up` / `Down` | 菜单与面板移选项 | `view.go:4784,4788`、`frame.go:1327-1328` | 无 | **保留**（焦点留在输入框 + `aria-activedescendant`） | |
| K16 | `Mod+Enter`/`Tab` | 提问对话框提交 | `uikit/ask.go:186-191`、`interaction.go:464` | Tab | `Enter` 提交；`Tab` 移焦 | 同 K3 |
| K17 | 任意 chord 录制 | 外观页快捷键录制（含 Tab/方向/Home/End/PageUp/PageDown/F1–F12） | `hotkey_settings.go:91-147` | 这些键在浏览器里都可能被页面/系统先吃 | **录制器只保留 `Mod+字母/数字`、`Alt+...`、`F1–F12`**；其余拒绝并提示原因 | 录不到的 chord 存下来就是谎言 |
| K18 | 系统级全局热键 | 显示/唤起窗口 | `host/hotkey.go:5-8` | 浏览器不可注册 | 仅托盘壳；web 设置页只读 | §2.7 |

**不新增屏幕直切快捷键**：原生没有"切到洞察/自动化"的 chord，只有命令面板的「前往 …」（`frame.go:1633-1650` 自动生成）。Web 侧照旧，不自造 `G then I` 之类。

### 4.2 冲突的判定方法（避免拍脑袋）

`preventDefault` 是否生效**因浏览器而异，且不保证**（Chrome 对部分 accelerator 直接忽略页面处理）。因此每个保留键都必须满足：

1. **有别名**：任一主键失效，别名仍可达成同一动作；
2. **有指针路径**：同一动作在 UI 上有可见按钮（原生的既有约束，`DESIGN.md` §14 页面主操作两落点）；
3. **失效不产生副作用**：例如 `Mod+N` 被浏览器吃掉只意味着"开了个新窗口"，不能同时触发新建会话。

⚠️ **[设计建议]** K6/K7/K11 的 `preventDefault` 有效性需在 Chrome / Edge / Firefox / Safari 四家实测后回填本表（当前是设计意图，不是实测结论）。

### 4.3 分层 Esc 仲裁器（本文最重要的一节）

#### 为什么必须专设

原生踩过 P0：审批卡承诺「Esc 拒绝」，但当焦点不在会话内（比如刚关掉设置、焦点停在侧栏 footer）时，卡片自己的 handler 收不到键，Esc 静默无效。修法是把兜底注册在**帧的根 context**上（`shortcuts.go:353-368` 的注释完整记录了这个 P0 与修法），并让模块通过稳定命令 `session.escape` 参与仲裁（`shortcuts.go:241,265`、`view.go:479`）。

Web 侧风险更高：每个组件自己 `addEventListener('keydown')` 时，**焦点在谁身上决定谁能听见 Esc**——这正是原 P0 的翻版。所以浏览器端必须把 Esc 做成**单一仲裁器**，而不是散落的 handler。

#### 优先级链（从高到低）

| 级 | 谁 | 何时 active | 动作 | 原生依据 |
|---|---|---|---|---|
| **L1** | 浮层栈顶（菜单 / popover / 对话框 / 抽屉 / 命令面板 / 设置面） | `overlayStack.length > 0` | 关闭栈顶一项；若是设置面 → 走脏守卫（见 L1b） | `frame.go:1788`、`frame.go:1317` |
| **L1b** | 设置脏守卫 | 设置面关闭且任一页 `dirty` | 弹「丢弃 / 继续编辑」，**不关面** | `frame.go:1806-1820`、`frame.go:1822-1833` |
| **L2** | Thread 内查找条 | `findBarOpen` | 关闭查找条 + 焦点还给 composer | `view.go:432-439` |
| **L3** | 子会话视图 | `childId != ""` | 返回父会话（**不**拒绝父会话的审批） | `view.go:441-448` |
| **L4** | 待审批 | `pendingApprovals.length > 0 && overlayStack.length === 0` | **拒绝最旧的一条**，本轮继续运行 | `view.go:450-455`、`shortcuts.go:272-279` |
| **L5** | 运行中 | `running && overlayStack.length === 0` | **停止本轮** | `view.go:466-473`、`shortcuts.go:288-296` |
| **L6** | 草稿非空 | `draft.trim() !== ""` | **清空草稿**（不发送、不弹确认） | **[新增]** — 原生 Esc **不**清空草稿（`view.go:4706-4714` 的 slash 逻辑只 `dismissed` 菜单，草稿保留） |
| — | 全部不 active | — | **不 `preventDefault`**，把 Esc 还给浏览器 | — |

> **与 Lead 给定链的关系**：Lead 给的「浮层 > 审批 > 停止运行 > 清空输入」是本链的 **L1 → L4 → L5 → L6** 子序列；L1b/L2/L3 是原生已有、且缺了就丢功能的中间级（`view.go:426-478` 的顺序即 L2 → L3 → L4 → L5）。

#### 实现约束（三条硬规则）

1. **单一监听**：只有一个 `document` 级 `keydown`（capture 阶段）监听器。任何组件**不得**自行绑 Esc。
2. **声明式 claim**：组件打开时把 claim 压栈、关闭时弹栈；仲裁器只读栈，不读 DOM 焦点。这样"焦点在侧栏/顶栏/Inbox"也照样命中 —— 直接消除原 P0 的根因。
3. **一次一键**：同一次 `keydown` 只允许一个 claim 消费并 `stopPropagation()`；`event.repeat === true` 一律忽略（原生对应 `guardRepeat`，`shortcuts.go:308-315`）。

#### 必须钉住的回归（否则 P0 复发）

| # | 场景 | 期望 |
|---|---|---|
| E1 | 焦点在侧栏、有待审批 | Esc **拒绝**该审批（不是无效） |
| E2 | 焦点在顶栏、运行中、无审批 | Esc **停止**本轮 |
| E3 | 焦点在 composer、运行中、有审批 | Esc **拒绝审批**（审批优先于停止） |
| E4 | slash 菜单开着、运行中 | Esc 只关菜单，**不**停轮 |
| E5 | 设置面打开且某页脏 | Esc 弹丢弃守卫，面**不关** |
| E6 | 子会话视图 + 父会话有待审批 | Esc **回父会话**，不拒绝审批 |
| E7 | 命令面板 + 设置面同时开 | Esc 先关面板（后开的在上） |
| E8 | 全 idle、无浮层、草稿空 | Esc **不** `preventDefault`（不打断浏览器，如退出全屏） |
| E9 | 长按 Esc | 只触发一次（`repeat` 忽略） |

---

## 5. 可访问性与焦点

### 5.1 原则

原生把可访问性做在框架的 `Role`/`Label`/`FocusGroup`/announce 上（如顶栏 `RoleBanner`，`frame.go:898`；侧栏 `RoleComplementary` + `FocusGroup(Vertical)`，`frame.go:318-321`；导航切换后 announce，`frame.go:154-158`）。Web 侧将这些映射到 ARIA 与 DOM 顺序，且**不得**因为换引擎而降低既有承诺。

### 5.2 Tab 不再兼任「接受建议」

| 原生 | Web |
|---|---|
| 在 slash/@ 菜单里 `Tab` = 执行高亮项（`view.go:4792,5082`） | `Tab` 默认就是焦点移动，**不得**被拦截；建议菜单的「执行」= `Enter` |
| 菜单开着时 Enter 被菜单截获 | 相同（焦点留在输入框，用 `aria-activedescendant` 表示高亮） |
| 无 | 唯一的 `Tab` 例外：菜单高亮项是**纯文本补全**时，`Tab` 插入文本 + 关菜单 + `preventDefault`（因此焦点不移动）；**执行**永远只归 `Enter` |

理由：`Tab` 移焦是可访问性铁律（WCAG 2.1.1 键盘可达 / 2.4.3 焦点顺序）。原生的 `Tab=执行` 在桌面端尚可解释（控件树由框架管），在浏览器里会同时破坏键盘用户与屏幕阅读器用户的空间感。

### 5.3 Tab 序规则

1. **顺序 = DOM 顺序**，不设正 `tabindex`（禁止 `tabindex > 0`；`0` 与 `-1` 允许）。
2 . **区域顺序**：侧栏 → 顶栏 → 主列内容 → composer → 右检查器（对齐原生实测顺序 `ui-drive` 步骤 `a4qa-tab-order`）。
2. **每组一个 Tab stop**：列表/树/表格用 roving tabindex（容器一个 stop，内部方向键移动）。
3. **跳过重复导航**：提供「跳到主内容」skip link（原生无此需求）。
4. **隐藏即不可达**：`display:none` / `hidden` 而非 `visibility` 或仅透明；`aria-hidden` 的子树不得含可聚焦元素。

### 5.4 焦点陷阱与归还

| 场景 | 要求 | 原生依据 |
|---|---|---|
| 模态对话框 / 抽屉 / 命令面板 / 设置面 | 焦点陷阱在容器内；`Tab` 循环；`Esc` 关闭 | `frame.go:2049-2061`（设置面 Esc 挂祖先链） |
| 关闭任何浮层 | 焦点归还**触发它的元素**；该元素已不存在时归还到最近的可聚焦祖先 | `frame.go:958` `chromeFocusReturn` |
| 抽屉关闭（键盘触发的） | 归还触发按钮；**停靠面板开关不动焦点**（原生明确区分，`shortcuts.go:335-339`） | 同 |
| 发送消息后 | 焦点回到 composer | `uikit/composer.go:472,484-486` `focusComposerAgain` |
| 会话切换 / 导航 | announce 新屏标题 | `frame.go:154-158` |
| 破坏性确认 | 焦点默认在**取消**上 | `uikit/approval.go:26-31` 的反面：审批无默认；确认类取消为 Default |

### 5.5 ARIA 要求（最小集）

| 面 | 角色/属性 |
|---|---|
| 顶栏 band | `role="banner"` + 可读名「顶栏」（`frame.go:898`） |
| 侧栏 | `role="complementary"` + 可读名「侧栏」（`frame.go:239,318`） |
| Thread Tab 行 | `tablist` / `tab` / `tabpanel`，panel 以 tab 命名并被 `aria-controls` 指向（原生 `rightpanel/view.go:44-58` 已这么做） |
| 命令面板 / 建议菜单 | `listbox` + `option` + `aria-activedescendant`；输入框 `combobox` |
| 工具行（折叠） | `button` + `aria-expanded` |
| 待审批 | 不是 dialog（原生刻意做成内联卡，`uikit/approval.go:8-11`）；用 `role="region"` + `aria-live="assertive"` 宣告到达 |
| 运行态变化 / 流式完成 | `aria-live="polite"` 的单一状态区；**不要**每帧播报 |
| 图标按钮 | 必须有 `aria-label`（原生规则：图标按钮必须 `Label`，AGENTS.md §3.8） |
| 状态 | 不只靠颜色：语义 tint + **文字**（原生 `uikit.StatusBadge` 必填标签） |
| 集合行 | 每行有可读名称（原生 List 行 Label 约定） |

### 5.6 验收项（全键盘可达）

- [ ] 从页面加载起，**不碰鼠标**可完成：新建会话 → 输入 → 发送 → 审批（允许/拒绝）→ 切换 Tab → 打开右栏文件 → 关闭全部浮层。
- [ ] 每个可交互元素有可见焦点环（token `Focus` 色，`brand/palette.go` 的 `Focus`），且焦点环不被 `overflow:hidden` 裁掉。
- [ ] `Tab` 在模态内循环、不逃逸到背景。
- [ ] 关闭模态后焦点回到触发元素（可断言 `document.activeElement`）。
- [ ] Esc 仲裁器的 E1–E9（§4.3）全部通过。
- [ ] 屏幕阅读器可读出：待审批出现、run 开始/结束、toast 文本、Tab 切换后的面板名。
- [ ] 200% 缩放下无横向滚动、无内容裁切。

---

## 6. 文件拖入与附件

### 6.1 现状（为什么不能直接搬）

原生拖入拿到的**就是绝对路径**（`internal/host/host.go:696` 的 `window.OnFileDrop` → `filedrop.go:22` → `session/view.go:3350-3360`），附件系统也以绝对路径为契约（`internal/ui/session/attachments.go:30-31`「It returns absolute paths.」）。浏览器拖入只给 `File` 对象，**没有路径**。

### 6.2 宿主代理上传

```
浏览器                    网关（gateway）                        工作区/附件系统
  drop ──▶ POST /api/uploads (multipart, sessionId)
                │ 校验 sessionId 属于本连接、大小/数量上限、扩展名白名单
                │ 写入 <dataDir>/uploads/<sessionId>/<uuid>/<basename>
                └─▶ 返回 { absolutePath, displayName, bytes, mime }
  ◀── { absolutePath }
  把 absolutePath 放进 composer 的待发附件列表（与原生附件同形状）
```

**为什么落在 `dataDir` 而不是工作区**：原生已有先例——undo 快照刻意放数据目录，因为"工作区内的镜像会被文件工具读到隐藏路径、让 glob/grep/search 结果翻倍"（`docs/AGENT-SURFACES.md` S9 的 undo 段）。上传物同理：放进工作区会污染 agent 的文件面与 diff。

**与右检查器的衔接（重要，且是"不自动"的）**：
- 上传文件**不自动出现在文件树**（它不在工作区根下，`internal/ui/rightpanel/files.go:92-117` 的树只列工作区）；
- 用户要看内容 → 附件行提供「预览」，走同一个只读预览器（原生 `FilePreviewer` 契约，`rightpanel.go:3`）读绝对路径；
- 用户要 agent 处理它 → 附件机制本身已经把路径交给模型（原生同路径）。

### 6.3 生命周期与清理

| 阶段 | 规则 |
|---|---|
| 上传即声明 | 目录名带 `sessionId`，会话删除时整目录删除 |
| 会话未发送 | 页面关闭 / 会话切换时，尚未发送的暂存附件**保留 24h** 后由启动清扫删除 **[设计建议]** |
| 启动清扫 | 网关启动时删除 `uploads/` 下 mtime > 7 天的孤儿目录 **[设计建议]**，且有日志 |
| 幂等 | 同一 `File` 重复拖入按内容 hash 去重，避免 5 份同名副本 **[设计建议]** |
| 上限 | 单文件与单会话总量需给硬上限并在 UI 报错（原生无此限制，因走本地文件系统；这是浏览器带来的**新**约束） |

### 6.4 交互契约

| 项 | 要求 | 原生依据 |
|---|---|---|
| 拖入高亮 | 拖入期间 composer 卡片亮边 + 文案「松开以添加附件」 | `view.go:3360` `DragHint` |
| 多层元素 | 用 `dragenter/dragleave` 计数避免子元素抖动 | 原生「窗口只收到元素没接的 drop，不会二次暂存」（`view.go:3352-3355`） |
| 粘贴图片 | `paste` 事件读 `clipboardData.files`；无图时提示「请用附件或拖拽添加图片」 | `attachments.go:214`、`home/composer.go:148` |
| 文件夹拖入 | 浏览器给 `webkitGetAsEntry`，需递归展开后逐个上传 **[设计建议]** | 原生直接拿到目录路径 |
| 失败 | 逐个文件报错，其余照常入列（不整体失败） | 原生逐个 stage（`attachments.go:289-292`） |

---

## 7. 流式与虚拟化

### 7.1 transcript 虚拟列表

原生是 `ui.List` 虚拟化 + `FollowEnd`/`HoldPlace` 流式跟随 + 行 key = 消息 ID（`internal/uikit/session.go:20-40`）。Web 侧要求：

| 要求 | 做法 | 依据 |
|---|---|---|
| 变高行 | **不得**假设固定行高（Markdown/代码块/图片高度差异大）→ 测量式虚拟化（ResizeObserver + 位置缓存） | — |
| 稳定 key | `message.id` | `uikit/session.go:26-28` |
| 流式原地增长 | 同 ID 行内容更新时**保持滚动锚点**，不重排 | 同上 |
| 同轮合并 | 连续 assistant 消息按 `turnId` 合成一行（`SameTurn`/`GroupRow`，`uikit/session.go:50-58`） | 原生"多步回合折叠成一条过程行" |
| 首屏 | 只水合最近 **N=80** 条，向上滚再分页 | **[设计建议]** |
| 长会话 | 5000 条消息仍可滚动；离屏行卸载 | **[设计建议]** |

### 7.2 流式跟随（"上滚即脱离"）

| 状态 | 判定 | 行为 |
|---|---|---|
| 贴底 | `scrollHeight - scrollTop - clientHeight < 24px` | 新内容到达 → 自动滚到底 |
| 脱离 | 用户上滚超过阈值 | **停止**自动跟随；显示「回到最新」浮动按钮（原生同一动作，`uikit/session.go:4-6`） |
| 回到底 | 点按钮 / 手动滚到底 | 恢复跟随并隐藏按钮 |
| 用户正在选择文本 | 一律不跟随（避免抢选区） | **[设计建议]** |

### 7.3 工具卡折叠

| 项 | 要求 | 依据 |
|---|---|---|
| 默认态 | 运行中展开、完成后可折叠；本轮（最后一条 user 之后）默认展开，出现更新的 user 消息后折叠 | 原生 `workSteps` 策略 `collapse=next-input`（`view.go:1157-1172`） |
| 用户手动切换 | 优先于默认策略（记在会话状态里） | 同上（"A reader's explicit toggle always wins"） |
| 失败态 | 展开并露出 stderr 摘要，可看完整输出 | `AGENT-SURFACES.md` S3 |
| 折叠时信息 | 仍显示 工具名 + 状态 + 一行摘要（不是空白） | `toolcards.go:8-12` |

### 7.4 性能预算

| 指标 | 目标 | 说明 |
|---|---|---|
| 首屏可交互（本地网关，冷缓存） | ≤ 1.5 s | **[目标]** |
| 首屏可交互（暖缓存） | ≤ 400 ms | **[目标]** |
| transcript 滚动 | 稳定 55 fps 以上（2000 条消息） | **[目标]** |
| 流式增量渲染 | 每帧 ≤ 16 ms，且**合并高频 token 推送**（≤ 30 Hz 刷新 UI） | **[目标]** |
| 单帧 DOM 节点 | ≤ 2000（虚拟化后） | **[目标]** |
| 会话切换 | ≤ 300 ms 出首屏骨架 | **[目标]** |
| WS 负载 | 只用**差值**，禁止整份会话重推 | `.cache/pivot/contracts-projection.md` §3.4 |

---

## 8. 验收清单（引擎无关）

来源：`internal/devtools/ui-drive` 的 **105 个 `d.step(...)`** 交互步骤（13 个 `steps_*.go`）+ `docs/AGENT-SURFACES.md` 的 **12 个 surface**（S1–S12）。这些是原生版唯一可继承的验收资产——旧测试体系（83,050 行）与 3 个 devtools 全部随退役作废（`.cache/pivot/native-ui-inventory.md` §3.1-3.2）。

**优先级**：`P0` = 缺了就不能日常用；`P1` = 等价性缺口；`P2` = 打磨。同一行可同时来自 drive 步骤与 surface 规格。

### 8.1 P0（24 条）

| # | 输入 | 期望观察 | 来源 |
|---|---|---|---|
| A1 | 打开 `/` | 出现 composer，可聚焦、可输入；空态引导可见 | `landing-new-chat`、`landing-composer-focus` |
| A2 | 在 `/` 输入文字并发送 | 创建新会话并跳到 `/c/:id`，消息出现在 transcript | `landing-new-chat`、`send-answer` |
| A3 | 从侧栏点另一个会话 | thread 切到该会话，滚动位置符合恢复规则 | `landing-conversation-switch` |
| A4 | 发送一条 prompt | 出现用户气泡 → 流式部分文本 → 完成态回答 | `send-answer`、`a4qa-stream-partial` |
| A5 | 流式中观察 composer | 发送按钮变「排队/停止」；运行态可见且**不伪造进度** | `stream-stop`、`a4qa-stream-partial` |
| A6 | 点「停止」 | 本轮结束，出现「已停止」提示（中性态，非报错） | `stream-stop`、`session.go:512-519` |
| A7 | 流式中按 Esc（焦点在 composer，无浮层，无审批） | 本轮被停止 | `a4qa-esc-composer-stop` |
| A8 | 待审批到达，按 Esc（焦点在**侧栏**） | 审批被拒绝（分层仲裁 E1） | `a4qa-esc-chrome-tip-deny` |
| A9 | 审批卡可见时按 Enter | **不做任何决定**（无默认动作） | `approval-safe-default` |
| A10 | 审批卡可见时按 `Mod+Enter` | 该请求被允许 | `uikit/approval.go:531` |
| A11 | 审批卡点「拒绝」 | 该请求被拒绝，卡片消失，下一个（若有）出现 | `overlay-approval` |
| A12 | 打开 `/inbox` | 看到待审批条目，数量与 thread 内一致；点进去回到对应 thread | **[新增汇总]**，来源同 A8/A11 |
| A13 | 在 `/inbox` 处理完所有条目 | 显示空态，不显示 0 值统计 | 同 S11 空态原则 |
| A14 | 输入 `/` | 出现命令菜单；**不推送布局**（浮层不挤内容） | `slash-open`、`overlay-slash-no-layout-shift` |
| A15 | slash 菜单中按 ↑↓ 再 Enter | 执行高亮命令 | `slash-keyboard` |
| A16 | slash 菜单中按 Esc | 菜单关闭，**草稿保留** | `slash-esc` |
| A17 | 输入 `@` | 出现文件候选；Esc 关闭 / Enter 选择 | `mention-open`、`mention-esc-enter` |
| A18 | `Mod+K` | 命令面板打开，可立即输入 | `palette-open`、`palette-immediate-type` |
| A19 | 面板内输入过滤 + Enter | 执行对应命令 | `palette-filter-enter` |
| A20 | 面板内 ↑↓ + Enter | 执行高亮命令 | `palette-down-enter` |
| A21 | 面板按 Esc | 关闭，下次打开 query 为空、选中第 1 项 | `shortcuts.go:202-233` 清空语义 |
| A22 | 打开右栏 → 文件页 → 点一个文件 | 出现预览 Tab；内容与文件一致 | `panel-files-preview`、`panel-open` |
| A23 | 打开一个被改动的文件 | 该 Tab 内可展开 unified diff | `panel-file-diff` |
| A24 | `Mod+Alt+F` | 右栏切到文件页（K9） | `shortcuts.go:345` 的等价 |

### 8.2 P1（16 条）

| # | 输入 | 期望观察 | 来源 |
|---|---|---|---|
| B1 | 打开设置 → 改一项 → 关面 | 弹「未保存」守卫；Discard 后重开恢复原值 | `settings-dirty-guard` |
| B2 | 设置面内按 Esc（焦点在任意字段） | 关闭（脏则先守卫） | `settings-close-esc`、`a4qa-esc-modal-tip-close` |
| B3 | 设置导航搜索「主题」 | 过滤到外观页并可定位到该项 | `settings-nav-search` |
| B4 | 各设置页页脚 | 唯一 primary 在**固定页脚**，滚动时不动 | `settings-footer` |
| B5 | 切暗色 / 切英文 | 全站跟随；无残留硬编码版本 | `theme-dark`、`lang-en` |
| B6 | 会话 Tab 行 | 对话/轨迹/记忆可切换；有 `KeyContext` 时出现「上下文」 | `session-memory-tab`、`view.go:943-951` |
| B7 | 窄窗（1024 / 900） | 顶栏标题与发送按钮仍可见可点 | `window-1024`、`window-900` |
| B8 | 极窄 composer（<480） | 不重叠、不裁切 | `composer-very-narrow` |
| B9 | 工具行点击 / 键盘 | 展开收起；运行时显示 spinner，完成显示状态 | `a4qa-tool-row-toggle` |
| B10 | 工具失败 | 失败行 + 可展开 stderr | `a4qa-tool-fail-state` |
| B11 | 消息 `⋯` 菜单 | 复制 / 编辑 / 重生成可用 | `message-actions-copy` |
| B12 | 后台任务 chip → 浮层 → 停止 | 任务被停止；历史分组可读 | `jobs-chip` |
| B13 | 队列弹层 | 可查看并取消排队消息 | `queue-popover-cancel` |
| B14 | toast（带撤销） | 出现、4s 消失；错误粘滞直到关闭 | `toast-native`、`toast-action-undo` |
| B15 | 危险操作（删除/清除） | 确认框默认焦点在**取消**；Enter 不执行危险动作 | `archive-delete-confirm`、`a4qa-settings-data-clear-guard` |
| B16 | 收起侧栏 `Mod+B` | 侧栏折叠为图标栏，展开后状态不丢 | `sidebar-collapse` |

### 8.3 P2（6 条）

| # | 输入 | 期望观察 | 来源 |
|---|---|---|---|
| C1 | 双击「新对话」 | 只创建一个会话（600ms 防抖） | `a4qa-new-session-double-click`、`shortcuts.go:107-123` |
| C2 | 当前会话行 vs hover 行 | 当前行 tint 更浓（选择态两级） | `a4qa-hover-vs-selected` |
| C3 | 命令面板按标题检索 | 每条命令都可被搜到 | `a4qa-palette-reaches-all-commands` |
| C4 | 右栏文件树 ↑↓ | 改变当前项并显示预览 | `a4qa-files-tree-select` |
| C5 | 面包屑「工作区根目录」 | 回树，**不关页签** | `a4qa-files-tree-return` |
| C6 | 团队入口零值 | 团队/子代理入口**常显**（不显示 0 计数） | `team-turn-persist`、`AGENT-SURFACES.md` S9「零值常显」 |

### 8.4 M2 待补（来自 12 个 surface，本节只登记不展开）

| surface | 关键验收点（原文出处） |
|---|---|
| S1 SessionThread（`AGENT-SURFACES.md:53`） | 虚拟化 / 跟随 / 空态 / 行 key 稳定 |
| S2 ReasoningTrace（`:130`） | 流式中展开、结束后收起 |
| S3 ToolRow/ToolCallCard（`:176`） | 状态收敛、失败展开、无详情回退 |
| S4 ApprovalDialog（`:226`） | 无 Default / Esc=拒绝 / 队列 |
| S5 PlanReviewDialog（`:273`） | Esc 不取消、空反馈不批准 |
| S6 AskDialog（`:322`） | Enter 误选防护、Other 输入框 |
| S7 Composer（`:363`） | Enter/Shift+Enter/Esc/排队 |
| S8 RunStatus（`:424`） | 不伪造进度、重试、用量 |
| S9 TeamSurface（`:484`） | RunState 真实、指派、关闭确认、历史只读/可继续 |
| S10 ContextInspector（`:529`） | 不伪造 0 值/假成本、无后端不注册页签 |
| S11 Insights（`:553`） | 无价不显示 `$0`、空态不渲染 0 |
| S12 单工作区侧栏（`:581`） | 无工作区切换 UI、只读文件夹行 |

**冻结口径**：以上 46 条（24 P0 + 16 P1 + 6 P2）是 M1/M2 的验收面。drive 剩余步骤中纯桌面专属的（`window-1024`/`window-900` 之外的原生窗口行为、托盘、全局热键）按 §2.7 转给托盘壳或被判为不适用。

---

## 9. 视觉一致性规则（与组件库无关）

> 【组件库待定：见 docs/plans/web-ui-framework.md】本节不写任何库名，只写**约束**。任何候选库都必须被本节约束"穿透"：若某库的默认主题与本节冲突，禁用该库主题是**接入前提**，不是可选项。

### 9.1 唯一来源

| 资产 | 唯一来源 | Web 产物 |
|---|---|---|
| 颜色 | `internal/brand/palette.go`（29 个 `ui.Color` 字段 + `Hex*` 常量，`palette.go:22-46`、`palette.go:81-121`） | 生成 `web/src/design/tokens.css` 的两组变量（light/dark） |
| 语义颜色 | `internal/design/tokens.go` 的 `Tokens`（24 个颜色字段，`tokens.go:69-130`；`fromPalette` 是 1:1 直映射，`tokens.go:135`） | `--zk-bg-base` / `--zk-label-primary` / `--zk-accent` … |
| 字号/间距/圆角/控件高度 | `internal/design`（`design.Space*`、`design.Radius*`、`design.Type*`、`design.Control*Height`） | `--zk-space-*` / `--zk-radius-*` / `--zk-font-*` / `--zk-control-height` |
| 图标 | `internal/icons/svg/*.svg`，**34 个**（16×16 网格 / stroke 1.2，`icons.go:1-6`） | 直接拷贝为 `web/src/design/icons/*.svg`，构建期按名索引 |
| 动画 | `internal/design/motion.go`（`TransitionOverlay` 等） | `--zk-duration-*` / `--zk-ease-*` |
| 组件目录 | `internal/design/registry.json`（29 组件 / 2 场景 / 25 反模式 / 12 allowlist） | 前端组件清单的**规格源**，不手抄 |

**规则 T1**：`tokens.css` **只能由脚本从 Go 侧生成**。手改该文件视为违规（与原生"颜色只能来自 token"同一条原则，AGENTS.md §3.2）。

### 9.2 禁止颜色字面量

| 禁止 | 允许 |
|---|---|
| `#0d44f6`、`rgb(13,68,246)`、`hsl(...)`、`color-mix` 里混入字面量 | `var(--zk-accent)` 等 token 变量 |
| 组件内联 `style={{color: '#fff'}}` | 同上 |
| 在 feature 层写 `background: white` | `var(--zk-bg-raised)` |

**执行方式**：替换原生的 `scripts/design-check.sh`（其 grep 针对 Go/mygo，`.cache/pivot/native-ui-inventory.md` §3.2）为前端 lint：
- 自定义 stylelint 规则：`web/src/**` 内禁止 hex/rgb/hsl/named-color 字面量，白名单仅 `web/src/design/tokens.css`；
- 自定义 ESLint 规则：禁止 `style` prop 里出现颜色字面量；禁止 `styled` 里出现裸数值度量（对应原 DS-03 的语义）。

### 9.3 禁止引入默认主题

| 约束 | 具体做法 |
|---|---|
| 不得引入任何设计系统的主题/reset CSS | 只允许"无样式行为基元"（无视觉默认的 primitive）；接入时**逐个组件**核对默认样式来源 |
| 必须能做全局关闭 | 若候选库提供主题入口，必须能整体关闭且关闭后组件退化为浏览器默认（可被 token 覆盖） |
| 变量命名隔离 | 所有 token 前缀 `--zk-`；禁止让第三方库的变量名进入 `web/src/design/` |
| 不允许两套设计系统并存 | AGENTS.md §7「不引入第二套设计系统」原样适用 |
| 组件样式来源唯一 | 组件的视觉必须能被 `tokens.css` 单独决定；不允许库内硬编码颜色作为最终呈现 |

**接入前的验收实验（不做完不许写组件）**：任选 3 个代表组件（按钮、可筛选列表、对话框），只用 `tokens.css` 变量把它们改成品牌外观；若需要覆写库的 `!important` 或内联样式才能达成，则该库**不满足**本节。

### 9.4 token 使用纪律（从原生规则原样继承）

| 规则 | 原生依据 |
|---|---|
| 控件高度只用 28 / 32 / 36 / 40 / 44 | AGENTS.md §3.13 |
| 圆角只用 4 / 6 / 8 / 12 / 16（16 仅 composer，Max 仅头像） | 同上 |
| 表面遵循 S0–S4 ramp；暗色导航必须比页面更深 | AGENTS.md §3.13 |
| 一屏 ≤2 个 Accent 填充块、≤1 个 primary 按钮、≤2 处红色 | AGENTS.md §3.10 |
| `TypeXXXS`(11) 只用于 kbd/微标签，禁止成句 | AGENTS.md §3.12 |
| 持久导航当前项 = `BgSelected` + 2px `Accent` 左条；浮层内瞬态选择可 Accent 填充 | AGENTS.md §3.11 |
| 状态只能"语义 tint + 必填文字标签"，不得中性底 + 状态字 | AGENTS.md §3.10 |
| `running` ≠ `warning`；`cancelled` 中性；`active` ≠ running | AGENTS.md §3.7 |

### 9.5 图标

| 约束 | 依据 |
|---|---|
| 只从 `internal/icons` 的 **34 个** SVG 取；禁 emoji、禁 Unicode 符号、禁手写路径 | AGENTS.md §3.8 |
| 尺寸只用 `design.IconSize*` token | `internal/design/tokens.go` |
| 图标按钮必须有可读名（`aria-label`） | 同上 |
| 16×16 网格 / stroke 1.2 不变量由构建期检查保证（原生有网格不变量测试） | `icons.go:1-6` |

### 9.6 视觉回归

原生的视觉基线是 81 张 `docs/screenshots/*.png` + 62 张 `docs/screenshots/audit-2026-11-11/`（`.cache/pivot/native-ui-inventory.md` §5）。Web 侧：

- 基线**重建**（不是复用）：按 R1–R24 逐路由 × light/dark × 1280/1024/900 出图；
- 出图与断言在 CI 里跑，失败给像素差的图，不靠人工记忆；
- 首轮不设"必须 0 像素差"（换引擎必然有差），只设**结构断言**：布局框位置、可见文本、颜色取自哪个 token。

---

## 附：本文件与其它文档的边界

| 主题 | 归谁 |
|---|---|
| 换轨决策与阶段划分 | `docs/plans/web-client-pivot.md` |
| 线协议消息/字段/端口/token 校验、贡献模型 schema | `docs/TRANSPORT.md`（§8） |
| 组件库选型与逐组件映射 | `docs/plans/web-ui-framework.md` |
| 设计规则与组件契约（引擎无关部分） | `docs/DESIGN.md` |
| surface 行为规格（12 个） | `docs/AGENT-SURFACES.md` |
| Go 侧分层/边界/import 断言 | `docs/ARCHITECTURE.md` |
| 本文 | 路由、屏面等价、键盘/焦点、拖放、流式、验收 |