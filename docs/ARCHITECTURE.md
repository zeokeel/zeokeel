# ZeoKeel 架构（v2 · 编译期模块化）

> 产品名：**ZeoKeel**。UI 是浏览器客户端（`web/`），网关承载后端能力，原生只剩托盘壳
> （2026-11-23 起；原生 UI 的架构描述已随代码删除，见 `pre-s4` tag）。旧工程的重建已完成，归档代码已从仓库移除（历史设计记录保留在 `docs/research/`）
> （独立 Go module，仅参考，不参与构建），新根工程按本文件重组。
> 相关调研与决策依据：[docs/research/kernel-split/00-report.md](research/kernel-split/00-report.md)。

## 1. 名词

| 名词 | 含义 |
| --- | --- |
| **模块 module** | 一个 Go 包 + 一个 `module.Descriptor`；编译期注册、随二进制一起编入；启动时由 assembly 选择是否激活 |
| **网关 gateway** | `--product serve` 的传输层：WS 控制面（域快照+差值+通知）、SSE 运行流、静态资源托管、token/Origin 校验、端口协商 |
| **Web 客户端 web** | React + TypeScript 浏览器客户端；只经 `web/src/api/` 与网关对话 |
| **进程宿主 host** | 托盘/热键/通知/单实例/设置存储/日志/退出等进程能力；**不拥有**任何业务逻辑 |
| **装配根 assembly** | 显式编译期 registry + 内嵌 assembly.json + 启动预检；薄 `cmd` 只调用它 |
| **域 domain** | 网关的同步单元（sessions/messages/approvals/plans/tasks/team/settings/prompts…）；每个域有版本号与可重放的差值环 |
| **快照 / 差值** | 客户端订阅后先取 `.snapshot`，之后只收增量；**域的第一帧不发差值** |
| **契约 contracts** | 服务键 + 接口 + 贡献值类型；模块唯一共享面 |

## 2. 目录

```
cmd/zeokeel/            薄 main（≤30 行）→ host.Main
internal/host/          进程宿主：托盘/热键/通知/单实例/设置存储/日志/退出
internal/gateway/       serve 产品的传输层：WS 控制面、SSE 运行流、静态资源、端口协商
web/                    浏览器客户端（React + TS + antd 6 + antd X）
internal/assembly/      装配根：编译期 registry、assembly.json、预检、按产品装配
internal/contracts/     契约（本文件 §5/§6 的接口与枚举）
internal/modules/*      后端模块（17 个：acp agent appinfo approvals automation goal
                        hosttools importer insights memorystore modelcatalog sandbox
                        sessionstore skills toolkit update workspace）
internal/design/        设计 tokens（→ web/src/design/tokens.css）
internal/brand/         品牌层：唯一 hex 来源
internal/icons/         34 个 SVG 图标（→ web 图标组件）
internal/devtools/      开发夹具（fakemodel / gwprobe），不进产品二进制
docs/plans/             迁移决策与执行记录　docs/archive 已退役文档
```

## 3. 三层边界（谁拥有什么）

```
cmd (≈30 行) → host (进程：托盘/设置/日志/退出) → assembly (装配) → kernel root
                                                    │
                                                    └─ 后端模块 provides 服务（store/session/appinfo/...）

web (浏览器) ⇄ gateway (serve 产品) → 同一套 kernel root + 后端模块
```

**形态（2026-11-23 起）**：网关进程 + 浏览器客户端 + 极小托盘壳。原生 UI 宿主
（`internal/uiroot`）与其模块已删除，代码在 `pre-s4` tag；退役记录见
[archive/s4-execution-checklist.md](archive/s4-execution-checklist.md)。

- **网关拥有传输**：WS 控制面（快照/差值/通知）、SSE 运行流、静态资源、token/Origin 校验、
  端口协商（`gateway.json`）。协议事实见 [TRANSPORT.md](TRANSPORT.md)。
- **host 拥有进程**：单实例、数据目录、设置存储、日志、退出信号，以及**尽力而为**的
  托盘/热键/通知。「网关先起、托盘后起」：托盘失败只警告，绝不取消网关
  （`internal/host/serve.go`）。
- **模块拥有能力**：17 个后端模块，全部与 UI 无关；它们只认 `contracts` 的服务键。
- **状态原则**：模块状态归模块实例，禁止包级可变状态；后台结果不得直接回调 UI。
- **生命周期**：模块在装配期激活并把注销函数交给 `ctx.Effect`，kernel dispose 逆序撤销；
  配置变更 = 重启进程。
- **依赖方向**：`contracts ← modules ← gateway ←（WS）← web`。
- **边界（机器断言，`internal/assembly/boundary_test.go`）**：
  - 后端模块可 import `contracts`、`design`、`lib/*`、共享库 `internal/{hosttools,memorytools,websearch}`；
    禁止 import 其他模块实现、`host`、`assembly`。
  - `host` 不得直连 `internal/modules/*`（共享代码移 `internal/lib`，如 `lib/version`）。
  - **原生引擎隔离**：`github.com/egoist/mygo` 只允许出现在 `internal/host`、
    `internal/lib/openurl`、`internal/brand`、`internal/design` 这 4 个包
    （`TestEngineIsQuarantinedToTheShell`）；`gateway`/`contracts`/`modules` 的**传递**依赖
    必须为 0（`TestBackendIsTransitivelyEngineFree`）。
  - **test 档单列**：`gateway` 的 e2e 允许并鼓励拖进 `internal/modules/*`（真模块才有价值），
    但同样不得拖进 mygo（`TestTestDependenciesStayEngineFree`）。

## 4. 注入点模型（灵活扩展的核心）

三层模型：**区域 region → 槽位 slot → 条目 item**。

- 区域：宿主布局容器（主侧栏、顶栏、对话页、设置页、overlay 层……）。区域数量与位置由
  宿主定义，模块不可新增区域（否则就是绕过壳层）。
- 槽位：具名位置。**宿主保留一批名字**（下表），同时**允许模块自定义**（建议前缀
  `"<owner>."`，如 `session.transcript.turn`）。宿主只渲染它认识的保留槽位；自定义槽位由
  **拥有者模块**在自己的 `Build` 里用 `UIRuntime.RenderSlot(c, name)` 渲染——于是模块可以
  给别的模块开扩展点，而宿主不需要知道。
- 条目：`SlotItem{Slot, ID, Order, Build}`；排序 = `Order` 升序，`ID` 兜底；重复 ID 注册报错；
  `Build` 只在帧内调用，必须无副作用；注销随模块 Effect 自动逆序。

**灵活性规则**

1. 槽位是自由字符串；新增槽位不需要改宿主（由拥有者渲染）。
2. 条目只声明顺序/可见性/内容，不声明布局；布局永远归容器 owner。
3. 运行期不增删注册表；按需显示用 `Visible()` 或 build 返回空实现。
4. 模块间行为协作走服务（read model / command），槽位只放 UI。
5. 宿主不认识的保留槽位不会被静默丢弃：`UIRuntime.Slots()` 可诊断，`Validate` 阶段可警告
   （后续加）。
6. 条目身份用 `ID`，控件身份用 mygo `ui.Handle`/`Context.Key`；不要把 `ui.Element` 存进状态。

## 5. 框架注入点清单（保留槽位）

> **本节属原生 UI（已删除），保留作历史记录。** 浏览器客户端的扩展点不是槽位，而是
> **路由 + 域订阅**：页面在 `web/src/pages`，数据来自 `web/src/api` 的域快照/差值，
> 工具卡/审批卡/计划卡由 `@ant-design/x` 组件渲染（见 [WEB-CLIENT.md](WEB-CLIENT.md)）。
> 原生期「现状」= 宿主/示范模块是否已渲染；槽位已随 `internal/uiroot` 删除。

### 5.1 窗口与通用

| 区域 | 槽位 | 现状 | 典型贡献 |
| --- | --- | --- | --- |
| 主侧栏 | `sidebar.primary` / `sidebar.nav` / `sidebar.tree` / `sidebar.footer` | ✅ 宿主 | 新建会话、屏幕导航、工作区/会话树、账户脚注 |
| 屏幕 | `screen.header` / `screen.actions` / `screen.tabs` / `screen.footer` | ✅ 宿主（B3 顶栏 band） | 屏幕标题/工作区、右侧动作（运行中/团队/⋯）、Tabs、底部状态 |
| overlay | `overlay.palette` / `overlay.dialogs` / `overlay.toasts` / `overlay.settings` | ✅ 宿主（prefix 通用渲染） | 命令面板、确认框、通知/toast |
| 命令 | `CommandSpec`（非槽位） | ✅ 宿主（内容顶部 Toolbar/命令面板） | 菜单/快捷键/命令面板统一入口 |

> 本壳层提供唯一一块**顶层 header band**（B3）：当当前屏向 `screen.header` /
> `screen.actions` / `screen.tabs` 有贡献时，宿主渲染「40px 标题行 + 可选 32px Tabs 行」
> 的浅色 band（`BgSidebar` + 仅底边 hairline、行间无线），并在标题行右端带 palette 与
> inspector 按钮；没有贡献的屏幕不渲染任何顶部工具栏（没有空条、没有返回按钮，侧栏即
> 导航）。屏幕级槽位是全局注册表：item ID 前缀等于某个已注册 screen ID 时只在该屏渲染
> （如 `session.title` / `session.actions` / `session.tabs`），其它 ID 视为 host/全局贡献；
> screen body 不再自建 header。`screen.footer` 仍渲染在 body 之后。
>
> 不要注册 `topbar.*` 这类宿主不渲染的槽位（宿主只渲染保留清单与拥有者自行
> `RenderSlot` 的自定义槽位）。模块自有扩展点用模块前缀 + `UIRuntime.RenderSlot`，
> 例如 session 的 `session.banner`（composer 上方目标条/审批内联卡的 dock 槽）。

### 5.2 主界面

| 区域 | 槽位 | 现状 | 典型贡献 |
| --- | --- | --- | --- |
| Home | `home.hero` / `home.composer` / `home.recent` | 🟡 示例模块将渲染 | 欢迎区、输入框变体、最近会话 |
| Automation | `automation.toolbar` / `automation.list` | ⬜ 迁入时 | 新建任务、运行历史 |
| 能力（Plugins screen） | `plugins.title`（`screen.header`）；页面正文由模块自建 | ✅ 已有 | 能力页标题；正文 = 页级 Tabs（技能/连接器，未来工作流为第三 Tab）+ 3 列 `ui.GridView` 卡片；工具目录/工具开关已删除 |

### 5.3 对话页（Session）

| 区域 | 槽位 | 现状 | 典型贡献 |
| --- | --- | --- | --- |
| 页头 | `session.header.brand` / `session.header.tabs` / `session.header.actions` | ⬜ | 会话标题、**页头一横条**（Chat/Trajectory/Memory 页签）、右侧动作 |
| 页头下方 | `session.banner` | ⬜ | goal/team/plan 横幅 |
| 正文 | `session.transcript` | ⬜ | 消息/工具卡/artifact 流 |
| **轨迹** | `session.trajectory` / `session.trajectory.bar` | ⬜ | **轨迹主体**、**轨迹旁的一横条**（统计/过滤/时间轴条） |
| 侧板 | `session.panel.left` / `session.panel.right` / `session.right.rail` | ⬜ | Todo/Team 面板、右栏 Start/Files/Terminal/日志页签 |
| 输入区 | `session.composer.top` / `session.composer.input` / `session.composer.toolbar` | ⬜ | 审批/plan 评审卡、输入框、模式/模型/权限/附件工具条 |
| 状态 | `session.status` | ⬜ | 用量/耗时/后台任务 |

### 5.4 设置

| 区域 | 槽位 | 现状 | 典型贡献 |
| --- | --- | --- | --- |
| **设置侧边栏** | `settings.sidebar` | ✅ 宿主 | 页面导航（`SettingsPageSpec` 自动生成）+ 额外动作/入口 |
| 设置页头 | `settings.header` | ✅ 宿主 | 页标题/说明/操作 |
| 设置页正文 | `settings.page.body` | ✅ 宿主 | 页面段落（模块自己 build） |
| 设置页脚 | `settings.page.footer` | ✅ 宿主 | 危险操作/保存状态 |

设置页本身用 `RegisterSettingsPage(SettingsPageSpec{ID, Section, Title, Order, Visible, Build})`
贡献，宿主渲染侧边栏 + 页头/页体/页脚槽位；独立设置窗口以后复用同一注册表。

### 5.5 非视觉注入点

| 注入点 | 注册 API | 用途 |
| --- | --- | --- |
| 屏幕 | `RegisterScreen` | Home/Session/Plugins/Automation 等顶层页面 |
| 设置页 | `RegisterSettingsPage` | 设置侧栏页面 |
| 工具卡 | `RegisterToolRenderer` + `UIRuntime.RenderTool` | 每种工具的自定义渲染（MCP/LSP/memory 等模块自带 UI） |
| 命令 | `RegisterCommand` | 菜单/快捷键/命令面板 |
| 服务 | kernel `Provide/Require` | read model、存储、能力（模块协作的正道） |

## 6. 契约速查（internal/contracts）

```go
// 模块注册贡献的返回值必须交给 ctx.Effect 托管：
unregister, err := runtime.RegisterScreen(contracts.ScreenSpec{
    ID: "home", Title: "首页", Order: 10,
    Build: func(c *ui.Context) { /* 用 mygo 原生控件 */ },
})
if err != nil { return err }
if _, err := ctx.Effect(func() cordis.Effect {
    return cordis.Effect{cordis.CleanupOf(unregister)}
}); err != nil { unregister(); return err }
```

服务键（**网关/host 提供**）：`KeySettings`、`KeyAppInfo`、`KeyLogger`、`KeyDataDir`；
传输层转接的宿主能力：`KeyUserInteraction`、`KeyArtifactPresenter`、`KeyPlanReviewer`、
`KeyNotifier`、`KeyForegroundSession`（`gateway.Provide` 提供，后台完成通知据此判断当前前台会话）。
可选服务：`KeyExecutionHistory`（toolkit 提供，只读 durable 执行域历史）、
`KeySessionFocus`（session 模块提供，侧栏搜索命中交接过话到消息定位）、
`KeyToastAnchor`（原生期由 uiroot 提供、toasts 模块消费；浏览器端通知栈由客户端布局决定，
该键在 web 形态下不再是注入点）。

## 7. 装配

```json
{"version":1,"entries":[
  {"id":"appinfo","module":"zeokeel/appinfo","config":{"name":"ZeoKeel"}},
  {"id":"sessionstore","module":"zeokeel/sessionstore"},
  {"id":"agent","module":"zeokeel/agent"}
]}
```

- 内嵌装配只剩 `internal/assembly/assemblies/headless.json`（15 个后端 entry）。
  `serve` 复用它的 entries，并在加载后叠加传输层覆盖（例如 approvals 跟随实时权限、
  hosttools 打开交互档）——见 `assembly.ServeEntries()`。
- 产品清单：`Products()` = `[headless]`（有内嵌 JSON 的），`AllProducts()` = `[headless, serve]`；
  `--product` 默认 `serve`，`--product desktop` 已被删除并给出可读错误。
- `module.Load` 由每个产品自己的入口调用（headless/ACP 在 `main` goroutine，serve 在网关启动前，
  托盘壳在 `mygo.App.WhenReady` 之后）。
- kernel 预检拒绝「required 服务无 enabled provider」：传输层服务（interaction/artifacts/plans/…）
  由 `gateway.Provide` 在加载前提供，不再声明为 loader builtin。

## 8. 当前状态（2026-11-23：浏览器客户端 + 网关 + 托盘壳）

- ✅ **网关**（`internal/gateway`，`--product serve`）：WS 控制面按域组织，域键见
  `internal/gateway/domains.go` 的常量与 `hub.go` 的 `domainState.global` 注释：
  - **会话列表** `session.list`（全局，每个 ready 客户端都收）
  - **对话** `session:<id>`（按会话、只发给订阅者；消息/推理/工具步骤都在它的 ops 里）
  - **全局小域** `approvals`、`ask`、`plan`、`artifacts`、`settings`、`workspace.list`、`tasks`
  - **按会话的域** `tasks:<id>`（该会话的任务板视图）、`context:<id>`（上下文时间线）、
    `prompts:<id>`（该会话的排队提示词收件箱）

  每个域都走同一套机制：epoch + 域内 rev + 快照缓存 + 回放环 + 慢客户端合并；域的第一帧不发差值，
  客户端必须先 `.snapshot`。**没有 `messages` 域**（消息属于 `session:<id>`）、
  **没有 `team` 域**（团队/子代理走 `session:<id>` + `tasks`）、plan 域名是单数 `plan`。
  SSE 运行流 7 事件（`done|error` 必为最后一条、`tool.start` 先于 `tool.result`、断连 ≠ 停止），
  `clientRunId` 幂等，token/Origin 校验，端口协商写 `gateway.json`，
  静态资源托管（`go:embed`，`scripts/build-web-client.sh` 从 `web/dist` 复制）。
- ✅ **浏览器客户端**（`web/`）：路由 `/`、`/inbox/incoming`、`/t/:taskId`、`/team`、`/team/board`、`/skills/drafts`、`/settings/{channels,health,memory,bots}`；antd 6 + antd X 2.0；
  token/图标由 Go 侧生成（`gen:tokens` / `gen:icons`），`gen:verify` 守住生成链。
- ✅ **后端**：35 个模块（acp agent appinfo approvals automation bot browser channeldispatch creds dingtalk email feishu goal heartbeat hosttools importer inbox insights lead memorystore modelcatalog notifyroute replyrelay sandbox sessionstore skilldraft skills tasklease teamnotes toolkit triage update webhook wechat workspace）；
  Rig toolkit + hosttools 的工具面完整（serve 交互档 8 个 host 工具，headless 5 个）。
- ✅ **装配**：编译期 registry + 内嵌 assembly（`headless.json`；`serve` 复用其 entries 并加传输覆盖）；
  `Products()` = `[headless]`、`AllProducts()` = `[headless, serve]`；`--product` 默认 `serve`。
- ✅ **托盘壳**（`internal/host`）：托盘/热键/通知尽力而为；网关先起、托盘后起，托盘失败只警告。
- ✅ **验证**：`internal/gateway` 15 条 `TestE2E*` + 协议一致性；`make acceptance-{check,approval,slow}`
  三条协议级端到端；两档引擎边界断言；`scripts/verify.sh`。
- ❌ **已退役**：`internal/{ui,uiroot,uikit,regression}`、原生 desktop 产品、designlint/design-check、
  `go tool mygo vet`（空过）。历史见 [archive/s4-execution-checklist.md](archive/s4-execution-checklist.md)。

## 9. 硬规则（review checklist）

**Web 客户端**（细则见 [WEB-CLIENT.md](WEB-CLIENT.md) 与 [DESIGN.md](DESIGN.md)）：

1. **只用官方件**：antd `6.7.1` 有组件就用（`Table`/`Modal`/`Form`/`Select`/`Tabs`/`Alert`…），
   antd X `2.10.0` 覆盖会话面（Bubble/Sender/Conversations/ThoughtChain…）；自造前先查
   [plans/antd-x-component-map.md](plans/antd-x-component-map.md)。**API 必须查、不许凭记忆写**：
   `bash scripts/antd-cli.sh info <Component> --version 6 …` 与 `web/node_modules/@ant-design/x/es/**/*.d.ts`。
2. **颜色/字号/间距只来自 `web/src/design/tokens.css`**：禁止 hex 字面量、禁止裸数值度量。
   该文件是 `npm run gen:tokens`（消费 `internal/design` + `internal/brand`）的产物，手改即违规。
3. **禁止手写 `fetch`/`WebSocket`**：`web/src` 只能经 `web/src/api/` 访问网关（HTTP 与 WS 客户端都在那里）。
4. **审批动词不中性化**：允许/始终允许/拒绝三值；无默认动作、裸 Enter 不决定、**Esc = 拒绝**
   （[APPROVAL-SURFACES.md](APPROVAL-SURFACES.md)）。
5. **强调色预算**：一屏 ≤2 个主色填充块、≤1 个 primary 按钮、红色元素 ≤2；`running ≠ warning`。

**后端 / 进程**（与 UI 形态无关，长期有效）：

6. 状态归模块实例；禁止包级可变状态。
7. 注册/注销必须走 `ctx.Effect`（裸注册无法回滚）。
8. 模块不 import 其他模块实现 / host / assembly；跨模块行为依赖只走 contracts 服务（白名单见 §3，`boundary_test.go` 机器断言）。
9. 配置变更 = 重启；无运行时模块安装/热更新。

> **原生命令已退役（归档注记，别再照抄）**：曾经的第 1/2/7 条——「优先 mygo 原生控件
> `ui.Sidebar/List/Tabs/Modal/GridView`」「UI 只在帧内构建、跨线程走 `UIRuntime.Post`」
> 「每个 UI 模块带 headless 测试 `ui.NewTester`」——随 `internal/{ui,uiroot,uikit}` 在
> `S5: retire the native UI` 删除（代码在 `pre-s4` tag）。证据文档：
> [archive/AGENT-SURFACES.md](archive/AGENT-SURFACES.md)、[archive/DESIGN-LINT.md](archive/DESIGN-LINT.md)。
