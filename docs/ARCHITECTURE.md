# ZeoKeel 架构（v2 · 编译期模块化）

> 产品名：**ZeoKeel**。UI 是 mygo 原生控件（无浏览器/webview）。旧工程整体归档在 `old/`
> （独立 Go module，仅参考，不参与构建），新根工程按本文件重组。
> 相关调研与决策依据：[docs/research/kernel-split/00-report.md](research/kernel-split/00-report.md)。

## 1. 名词

| 名词 | 含义 |
| --- | --- |
| **模块 module** | 一个 Go 包 + 一个 `module.Descriptor`；编译期注册、随二进制一起编入；启动时由 assembly 选择是否激活 |
| **UI 总入口 uiroot** | 宿主里唯一的 mygo 帧入口（`ui.View`）：持有布局容器、导航、overlay/toast、主题/语言，并持有五张贡献点注册表 |
| **进程宿主 host** | 窗口/托盘/单实例/设置存储/日志/退出等进程能力；**不是** UI 布局层 |
| **装配根 assembly** | 显式编译期 registry + 内嵌 assembly.json + 启动预检；薄 `cmd` 只调用它 |
| **注入点 / 槽位 slot** | UI 的扩展点：宿主在布局里预留的具名位置，模块按 `Order` 向其中贡献内容 |
| **区域 region** | 一组槽位的布局容器（如主侧栏、对话页、设置页），布局归宿主 |
| **契约 contracts** | 服务键 + 接口 + 贡献值类型；模块唯一共享面 |

## 2. 目录

```
cmd/zeokeel/            薄 main（≤30 行）→ host.Main
internal/host/          进程宿主：窗口/托盘/单实例/设置存储/日志/退出
internal/uiroot/        UI 总入口：帧根、布局区域、导航、overlay/toast、主题/语言、注册表
internal/assembly/      装配根：编译期 registry、assembly.json、预检、按产品装配
internal/contracts/     契约（本文件 §5/§6 的接口与枚举）
internal/modules/*      后端模块（appinfo/store/session/tools/...）
internal/ui/*           UI 模块（home/session/settings/...）
internal/uikit/         共享 UI 库：优先封装 mygo 原生控件，不手搓控件
internal/design/        设计 tokens（颜色/间距，从 mygo Theme 派生）
old/                    归档旧工程（独立 module）
```

## 3. 三层边界（谁拥有什么）

```
cmd (30 行) → host (进程) → assembly (装配) → kernel root
                                  │
                                  ├─ 后端模块 provides 服务（store/session/appinfo/...）
                                  └─ uiroot provides UI 服务 + 渲染注册表
                                        └─ UI 模块 requires uiroot 服务/后端服务，注册贡献
```

- **owner 原则**：帧循环/窗口/`*ui.Context` 只归 uiroot；模块只贡献 builder
  （`func(c *ui.Context)`）。模块不持帧、不建根 overlay/toast、不改根主题。
- **状态原则**：UI 状态归模块实例 struct；禁止包级可变状态；跨线程只 `UIRuntime.Post`。
- **生命周期**：模块在装配期（主线程）激活并注册贡献；注销函数交给 `ctx.Effect`，kernel
  dispose 逆序撤销。配置变更 = 重启进程。
- **依赖方向**：模块只能 import `internal/contracts`、`internal/uikit`、`internal/design`、
  `internal/lib/*`、第三方；禁止 import 其他模块实现、`internal/uiroot`、`internal/host`。
  CI 用 `go list -deps` 断言。

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

> 「现状」= 宿主/示范模块是否已渲染；其余槽位随对应 UI 模块迁入时接管。所有槽位现在就可以
> 注册（`RegisterSlotItem`），不会因为宿主暂未渲染而报错。

### 5.1 窗口与通用

| 区域 | 槽位 | 现状 | 典型贡献 |
| --- | --- | --- | --- |
| 顶栏 | `topbar.brand` / `topbar.center` / `topbar.actions` | ✅ 宿主 | logo、全局搜索、账户/通知、状态胶囊 |
| 主侧栏 | `sidebar.primary` / `sidebar.nav` / `sidebar.tree` / `sidebar.footer` | ✅ 宿主 | 新建会话、屏幕导航、工作区/会话树、账户脚注 |
| 屏幕 | `screen.header` / `screen.actions` / `screen.footer` | ✅ 宿主 | 屏幕标题/面包屑、右侧动作、底部状态 |
| overlay | `overlay.palette` / `overlay.dialogs` / `overlay.toasts` / `overlay.settings` | ✅ 宿主（prefix 通用渲染） | 命令面板、确认框、通知/toast |
| 命令 | `CommandSpec`（非槽位） | ✅ 宿主 topbar | 菜单/快捷键/命令面板统一入口 |

### 5.2 主界面

| 区域 | 槽位 | 现状 | 典型贡献 |
| --- | --- | --- | --- |
| Home | `home.hero` / `home.composer` / `home.recent` | 🟡 示例模块将渲染 | 欢迎区、输入框变体、最近会话 |
| Automation | `automation.toolbar` / `automation.list` | ⬜ 迁入时 | 新建任务、运行历史 |
| Plugins | `plugins.list` / `plugins.detail` | ⬜ 迁入时 | 插件列表/详情动作 |

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

服务键：`KeyUIRuntime`、`KeyNavigator`、`KeyTheme`、`KeyLocale`、`KeySettings`、
`KeyAppInfo`、`KeyLogger`、`KeyScreenLister`。

## 7. 装配

```json
{"version":1,"entries":[
  {"id":"appinfo","module":"zeokeel/appinfo","config":{"name":"ZeoKeel"}},
  {"id":"ui","module":"zeokeel/host/group","children":[
    {"id":"home","module":"zeokeel-ui/home"},
    {"id":"about","module":"zeokeel-ui/about"}
  ]}
]}
```

- 产品 = `internal/assembly/assemblies/<product>.json`；同一 registry 可多产品复用
  （desktop 有 UI 组，headless 没有）。
- `module.Load` 在 `mygo.App.WhenReady`（主线程）里调用；UI 模块注册因此都在 UI 线程。
- kernel 预检拒绝「required 服务无 enabled provider」：UI 与后端必须在同一份 assembly。

## 8. 当前状态（Stage 0 骨架：已可运行）

- ✅ 新根工程、`old/` 归档（`cd old && go build ./...` 独立可构建）
- ✅ contracts / uiroot（帧根 + 五注册表 + Post 队列 + 导航 + 设置面 + 槽位渲染；
  `slot` 模型含保留槽位与模块自定义槽位 `RenderSlot`；`RenderTool` 工具卡注入）
- ✅ assembly（编译期 registry + 内嵌 desktop/headless 装配 + 预检 + 级联卸载）
- ✅ host（窗口/设置存储/日志/信号/退出顺序）+ 薄 `cmd/zeokeel`（17 行）+ Makefile
- ✅ 工具集：`internal/modules/toolkit` 把 **ZeoKeel Rig v0.7.0**（sandbox 策略 + 内置
  fs/shell/pwsh/editor/image、真实 PTY terminal、后台 jobs、memory/schedule/worktree、
  settings（宿主注入）、skill、team 任务板、working_directory/todo、可选 web_fetch 与
  远程 MCP over Streamable HTTP）组装成 `zeokeel/toolkit` 服务；Rig 侧
  `TestToolCatalogCoverage` + `TestModernToolSurface` 保证旧 agent 61 个工具名与
  现代工具面 61 个名称不缺失；`internal/modules/hosttools` 再把产品侧 host 工具（ask_user/present/
  plan_review/exit_plan_mode/goal/team）注册进同一注册表，证明产品「只做组装」
- ✅ 示范模块：`appinfo`（服务）、`ui/home`（screen+command+槽位组合）、`ui/about`
  （settings page + settings.header）、`ui/welcome`（topbar.center + home.hero + command）、
  `ui/toolcards`（ToolRenderer）、`uikit`（共享控件封装）
- ✅ 测试：uiroot 29 个 headless 测试（覆盖率 99.3%，含注入点区域/Post/生命周期/多窗串行）、
  assembly/host 装配与宿主测试、ui 模块集成测试（真实 assembly.Load + Root.View）；`-race` 绿
- ✅ 验证：`make ci` 全绿；headless 产品可运行并优雅退出；Xvfb 下 desktop 真窗口冒烟通过
- ⬜ 逐模块迁移：后端 20 个 + UI 16 个（见 05 报告），先低耦合叶子（importer/tools-web/diff/plugins）
- ⬜ session 读模型 + ui/session 模块（接管 `session.*` 槽位，含轨迹与轨迹旁横条）

## 9. 硬规则（review checklist）

1. **优先 mygo 原生控件**（`ui.Sidebar/List/Tabs/Modal/Markdown/VirtualList/Button/...`），
   不手搓控件或用 Row/Column 拼控件。
2. UI 只在帧内构建；帧内无副作用；跨线程走 `UIRuntime.Post`。
3. 状态归模块实例；禁止包级可变状态。
4. 注册/注销必须走 `ctx.Effect`（裸注册无法回滚）。
5. 模块不 import 其他模块实现 / uiroot / host；依赖只走 contracts 服务。
6. 配置变更 = 重启；无运行时模块安装/热更新。
7. 每个 UI 模块带 headless 测试（`ui.NewTester` 或假 frame），覆盖「注册→渲染→卸载」。
