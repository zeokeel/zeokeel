# 06 · UI 编译期模块组装 spike：可行性与边界

> 研究线 F / task-6 交付物。对象：`github.com/zeokeel/zeokeel-kernel@v0.1.1` 的 `module/`
> 编译期注册表 + 自研宿主 UI 运行时 +「假 frame」。spike：
> `.probe/research/kernel-split/ui/spike/`（独立 go module，36 个测试全绿，`go test -race ./...`
> 全绿；不 import mygo、不依赖 GUI、未改产品代码）。证据分级沿用 01 报告：**[实测]**、
> **[外部引用]**、**[判断]**。内核结论给相对内核仓库根的 `file:line`；mygo 框架结论给相对
> `/root/go/pkg/mod/github.com/egoist/mygo@v0.7.4` 的路径；产品结论给相对 `/data/mygo` 的路径。

## 0. 结论摘要（先读 7 条）

1. **[实测] UI 模块 = 普通 kernel `module.Descriptor` 成立**：模块只声明 `Requires(ui/runtime)`
   和（home）`Requires(app/sessions)`，Activate 注册贡献、注销交给 `ctx.Effect`；kernel 的激活
   事务因此覆盖宿主 UI 注册表（失败回滚、dispose 逆序注销、provider 撤回后回 Pending）。
2. **[实测] 宿主 `UIRuntime` builtin 服务可用**：Screen/Slot/Command/SettingsPage/ToolRenderer
   五张注册表 + 确定性排序（priority↑、id↑）+ 重复身份检测；registry `AddBuiltin(ui/runtime)`
   后，`module.Validate` 预检与 `module.Load` 的 builtin 校验都能解析它。
3. **[实测] 编译期装配成立**：显式 `Register` + assembly.json（含 `Entry.Children` 的 UI 子组）
   + `module.LoadFile`；disabled/optional 行、required 整树回滚、optional 容忍、预检零 fiber 均实测。
4. **[实测] 依赖门控成立**：home 在 backend 缺失时 Pending（帧只有 skeleton），Provide 后同步
   Active、贡献出现；撤回后回 Pending、贡献消失。
5. **[实测] 跨线程模型成立**：后台 goroutine 改 model + `Post` → 帧首 drain（UI 线程、FIFO）→
   同一帧渲染新值；4 worker × 100 轮并发 + 50 帧并发绘制在 `-race` 下全绿。
6. **[实测] 线程纪律可断言**：帧首 drain、激活注册、注销的 24 条 UI 操作 `{GoID, Linux TID}`
   完全一致；非 owner 线程注册/画帧返回 `ErrWrongThread` 并留审计，注销只标记不阻断。
7. **[判断] 可进入 mygo 集成，瓶颈不在 kernel**：贡献点、生命周期、门控、回滚、线程队列现成；
   必须重做的是真实 frame 边界（`*ui.Context` 布局/输入/主题/像素）、mygo 主线程 API 桥接、
   以及 settings/commands/toolcards 三个贡献点的真实数据形状。

> 命名说明（与 04/05 对齐）：本 spike 的服务键写作 `ui/runtime`，04 §5 与 05 §4/§5 的最终建议键名是
> `app/ui`（Runtime）/`app/ui/navigator`/`app/ui/theme`/`app/ui/locale`；00 总报告以 `app/ui` 为准，
> spike 里的 `ui/runtime` 视为临时名，语义一致。

## 1. 方法与证据

- 环境：离线（`GOPROXY=off GOSUMDB=off`），Go 1.27.3，内核只读缓存；spike 独立 module
  `probe.local/uispike`，`require ... v0.1.1`，两行 `go.sum` 来自缓存元数据，无网络、无 `replace`。
- 假 frame：`Frame.Draw()` 按「帧首 drain Post → screen（priority/id）→ 无 screen 则 skeleton →
  regular slots（slot 名字典序，内部 priority/id）→ overlay.* 最后」输出有序 `Node` 列表，而不是
  像素。这是 mygo `ui.View(func(c *ui.Context))` 立即模式帧的结构等价物（`examples/counter-native`；
  `ui/context.go:14-16` 明确 Context 只在本次调用内有效）。
- 复现：`cd .probe/research/kernel-split/ui/spike && export PATH=/usr/local/go/bin:$PATH
  GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod && go test ./...`（ok host 0.039s / ok uiruntime
  0.006s）与 `go test -race ./...`（ok host 1.097s / ok uiruntime 1.022s）。
- 关键实测日志：desktop 帧 15 nodes / 2 commands / 1 settings page / 3 tool renderers、close
  逆序注销 10 条注册；gating `pending(0 screens) -> provide -> active(1 screen) -> withdraw ->
  pending(0 screens)`；stress 400 posts 全在 UI 线程 drain；thread contract 24 条 UI 操作同在
  `goroutine 149/thread 950028`。

## 2. 证明了什么（对应 task-6 的 9 条）

### 2.1 UIRuntime builtin 服务与贡献点注册表 [实测]

`contracts.UIRuntimeKey = NewServiceKey[UIRuntime]("ui/runtime")`；宿主 `root.Provide` 实现并让
registry `AddBuiltin` 声明，模块 `AddRequires`。Screen/Command/SettingsPage 按 ID、Slot 按
(slot,ID)、ToolRenderer 按 (tool,ID) 检测重复（error，非静默替换）；排序一律 priority↑ 再 ID↑。
证据：`TestScreensOrderedByPriorityThenID`、`TestCommandsAndSettingsPagesOrderedByPriorityThenID`、
`TestSlotContributionsOrderedWithinSlot`、`TestDuplicateIdentitiesRejected`（5 子测试）、
`TestInvalidRegistrationsRejected`（6 子测试）。内核依据：`module/module.go:298-320`、
`module/validate.go:168-224`。

### 2.2 UI 模块 = Descriptor + Effect 逆序注销 [实测]

三个 demo 模块：`ui/home`（screen + sidebar.top + composer.toolbar + command 共 4 条贡献）、
`ui/settings`（settings page + overlay.settings slot）、`ui/toolcards`（同一 tool 两个 renderer +
另一个 tool 的 renderer + command）。模块用 `contracts.Own(ctx, unregister)` 把注销交给 kernel
effect；实测 close/subtree dispose 后 unregister 序列 = register 序列严格逆序，且注销全在 UI 线程。
内核依据：`context.go:770`（Effect）、`effect.go`（LIFO 单次撤销）、`fiber.go:1325-1339`（失败逆序 drain）。

### 2.3 编译期 registry + assembly.json（含 UI 子组）[实测]

`module/` 没有 group 概念，spike 注册 no-op `host/group` 模块作为 UI 子组容器：

```json
{"version":1,"entries":[
  {"id":"backend","module":"app/sessions","config":{"titles":["First session","Second session"]}},
  {"id":"ui","module":"host/group","children":[
    {"id":"home","module":"ui/home","config":{"screenId":"home","title":"Home","greeting":"Ready"}},
    {"id":"settings","module":"ui/settings","config":{"pageId":"settings.general","title":"General"}},
    {"id":"toolcards","module":"ui/toolcards","config":{"commandId":"tools.copy-output"}}]}]}
```

实测：`module.LoadFile` 后 5 个实例全 Active、typed config 送达、Warnings 为空；`rows.json` 的
disabled 行不实例化、optional 失败保留 Failed + warning；`broken-required.json` 的 required 失败
→ `*module.LoadError` + 整树回滚；`badconfig.json` 预检阶段 `*module.ValidationError`、0 贡献 0 fiber。
内核依据：`module/config.go:33-78`、`module/validate.go:113-235`、`module/load.go:95-138`。
配置变更 = 重启进程（与 01 报告一致）。

### 2.4 依赖门控 [实测 + 方法说明]

`ui/home` 的 Descriptor `Requires(app/sessions)`；assembly 内 provider-first 调度保证 backend 先于
home 创建（`module/validate.go:245-320`）。**运行期**门控无法用 `module.Load` 演示：预检直接拒绝
「required 服务无 enabled provider」的 assembly（`module/validate.go:189-224`）。spike 用
`host.DescriptorPlugin` 把 Descriptor 适配成 kernel Plugin（Inject = 模块 required 服务名，复刻
`module/load.go:157-175` 的 entryPlugin），实测 Pending → Provide → Active → 撤回 → Pending 全循环，
贡献随状态出现/消失。这是真实集成的重要约束（见 R7）。

### 2.5 帧组合顺序与 dispose 后消失 [实测]

desktop 帧 15 个节点严格等于预期：screen/home + 4 个子节点 → composer.toolbar → sidebar.top →
overlay.settings；命令 `[tools.copy-output home.refresh]`、设置页 `[settings.general]`；
`RenderTool("shell")` 按 priority/id 产出 2 张卡；dispose `ui` group fiber 后注册表归零、帧只剩
skeleton、backend 仍 Active。证据：`TestDesktopAssemblyLoadsAndComposesFrame`、
`TestDisposeUISubtreeRemovesContributions`、`TestFrameCompositionOrder`、`TestToolRendererCompositionOrder`。

### 2.6 跨线程 Post / Invalidate / `-race` [实测]

`Post(fn)` 任意 goroutine 入队 + dirty，`Invalidate` 任意 goroutine 请求帧；帧首在 UI 线程 FIFO
drain，posted 闭包跑完才构图 → 新值同一帧可见（`TestCrossThreadPostDrainedBeforeCompose`：drain
前 model 仍旧值，frame 后渲染新值）；压力测试 4 worker × 100 轮 rename+Post 与 50 次 Draw 并发，
`-race` 全绿、400 个闭包全在 owner mark 上（`TestCrossThreadPostStressRace`）。与 mygo 的
`mainLoop.post/drain`（`loop.go`）、`surface.Conn.Post/Invalidate`（`internal/surface/surface.go:62-72`，
「Everything runs on the main thread, except Invalidate and Post」）以及 headless Tester 的
`runLater`→`runFrame`（`ui/headless.go` settle）同语义。

### 2.7 失败回滚 [实测 + 一个已知边界]

required 模块注册 2 条贡献后 Activate 返回 error → `module.LoadError`、整树 dispose、注册表零
残留、unregister 严格逆序、host 可立即重新加载干净装配（`TestRequiredFailureRollsBackWholeTree`）；
两个模块注册同一 Screen ID → 第二个激活期 `duplicate screen ID` → 整树回滚、host 恢复
（`TestActivationConflictRollsBackAndHostRecovers`）；预检 typed config 失败不创建任何 fiber
（`TestPreflightBadUIConfigCreatesNoContributions`）；optional 失败保留 + warning、disabled 跳过、
无残留（`TestOptionalFailureIsTolerated`）。**已知边界 [实测]**：模块裸改宿主注册表（不经
`ctx.Effect`）时 kernel 无法回滚 → 残留（`TestKnownGapBareRegistrationLeaks`，PASS 即复现）。
生产纪律必须统一走 `contracts.Own` 并加 CI 约束。

### 2.8 线程约束 [实测]

`uiruntime` 在 `NewRuntime()` 记录 owner `{goroutine id, Linux OS thread id}`；注册/画帧在非 owner
线程返回 `ErrWrongThread`（审计留 `OffThread` 违规），注销只标记不阻断。测试用
`runtime.LockOSThread` 把主测试 goroutine 固定为 UI 线程，断言整轮 load（激活→注册）→ frame
（drain）→ close（注销）的 24 条 UI 操作 mark 完全相同
（`TestThreadContractOfActivationUnregistrationAndFrames`），并验证 2 次 off-thread 尝试被拒
（`TestOffThreadUIOperationsRejected`）。

### 2.9 多产品共用 registry [实测]

同一个 `*module.Registry` 被 desktop 与 headless 两个 host 使用；desktop 有 home screen，headless
无 screen 模块 → 帧首 skeleton；两者 settings/tool renderers 相同
（`TestTwoProductsShareRegistryAndRenderDifferently`）。验证「模块集随构建固定、产品差异在
assembly」，与 01 报告的多产品结论一致。

## 3. 没证明什么（明确边界）

1. **真实 UI**：无布局/裁剪/文本 shaping、无输入/焦点/键位、无主题 token、无动画、无 scene diff、
   无像素、无 a11y；假 frame 只证明贡献点的组合顺序与所有权。
2. **真实 mygo 主线程**：证明的是「goroutine + Linux tid 一致」，不是 mygo 的 `IsMainThread()`；
   goroutine 可能换 OS 线程（spike 用 LockOSThread 固定，生产不能用这个技巧）。
3. **热插拔/运行时 enable-disable/HMR**：编译期路线本就不做；module 层 disabled 只在启动预检
   生效（`module/validate.go:142-145`）。
4. **多窗口**：mygo 的 settings 是独立非模态窗口（`internal/app/settings_window.go`），每窗口一个
   content/frame；spike 只有单 runtime/单 frame，未证明 per-window runtime 或共享贡献语义。
5. **per-session 子树**（DSH 两级平面）：未涉及 kernel `Isolate`/子 fiber 的 session 级隔离。
6. **性能**：无 benchmark（帧构建、贡献数量放大、Post 争用）；`-race` 只证明安全。
7. **真实后端契约**：app/sessions 是内存 fake；`ToolCall` 是占位（真实工具卡需要 diff/附件/
   流式状态/展开状态）。
8. **配置/i18n/持久化/主题的贡献契约**：settings page 只证明注册+排序+组合，真实 settings 还需要
   nav、schema、store 持久化、翻译、design token 注入。
9. **生产硬化**：未做长时间 soak、模块自建 goroutine 的 panic 遏制（kernel 只保护同 goroutine）、
   跨平台 TID。

## 4. 与真实 mygo 的差距（最重要的一节）

### 4.1 帧模型：结构等价，语义差一层

- 真实：`windowOptions.Content = ui.View(shellApp.View)`（`internal/app/app.go:238`），
  `App.View(c *ui.Context)`（`internal/shell/app.go:303`）在 main thread 每帧重建 widget 树；
  Context 只在本次调用内有效（`ui/context.go:14-16`），元素有 key、布局、主题、动画、输入与 scene
  缓存。假 frame 只有「渲染回调 + 有序节点」；贡献回调可以每帧重跑（方向兼容），但**没有布局
  上下文**：真实 slot 贡献要建 widget（`ui.Row(...).Children(func(){…})`），spike 只能证明注册与次序。
- 好消息：mygo 自带 headless 渲染与断言（`ui.NewTester`，`ui/api_gen.go:1437`；`ui.Render`，`:1701`；
  `Tester.Texts/Find/HasText/Image`，`ui/headless.go:457-500`），产品仓库已在用
  （`internal/shell/visual_regression_test.go`、`render_bench_test.go`）。下一步可把假 frame 换成
  `ui.NewTester(view, 1600, 1000)` 做真实 headless 断言，假 frame 退化为快速单测。

### 4.2 线程模型：goroutine ≠ mygo main thread

mygo 的权威判定是 backend `IsMainThread`（`loop.go`）与 `RunOnMain`/`postMain`；surface 明确
「main thread only, except Invalidate and Post」。真实集成里装配加载时机很关键：产品 main 在
`mygo.App.Run()` 之前建 window，`App.WhenReady` 回调时才在 main thread 上。**UI runtime 的 owner
不能再用 goroutine id**，应改为 mygo 的 `IsMainThread` + `RunOnMain` 桥；Post/Invalidate 直接桥到
`surface.Conn.Post/Invalidate` 或 mainLoop。spike 的 `ThreadMark{TID}` 只是 Linux 测试证据，
跨平台（Windows/macOS）不可用，真实实现应委托 mygo backend。

### 4.3 贡献点的真实数据形状差距

- **screen**：当前是 `type Screen int` 枚举 + `mainScreen` 的 switch（`internal/shell/app.go:18-26`、
  `:502-514`）；改成贡献点后内置页仍可手写，module screen 需要「何时显示」谓词与导航集成。
- **slot**：真实位置（sidebar 顶部、composer toolbar、右侧栏、toast 层）是 shell 布局的一部分；
  贡献需要 `*ui.Context`、design tokens、当前 snapshot；spike 的 slot 只有名字。
- **settings page**：现在 `SettingsView`/`settingsBodyLive`（`internal/shell/settings_ui.go:279`、
  `:225`）手工组织 nav+page；贡献点还需要 settings key schema、持久化 store、i18n、独立设置窗口
  （`internal/app/settings_window.go`）这个第二宿主。
- **tool renderer**：现在 `toolKindFor` 按工具名硬编码 switch（`internal/shell/toolcards.go:56-80`）+
  `liveToolCards`（`:111`），工具卡状态（展开/动画/summary）在 shell 的 live state 里；spike 的
  ToolRenderer 是简化占位，真实贡献要读 `state.ToolState` 快照、遵守流式更新。
- **command**：真实产品是 mygo 原生菜单 + 快捷键 + slash command/palette 三套入口；spike 的
  Command 只是注册表数据，未接菜单/快捷键/焦点。

### 4.4 宿主结构差距

- 当前是单 App 单 View 的巨型 shell（`internal/shell` 约 54.7k 行、`internal/app` 约 20.3k 行），
  所有页面/菜单/设置/tool 卡片手工接线；模块化贡献点要改造这些 dispatch 点，而不是新增文件。
- 已有可借鉴的宿主抽象：`internal/app/settings_window.go` 把「生产 mygo.Window / 测试 fake」藏在
  `settingsWindowHost`/`settingsWindowManager` 接口背后，与 spike 的 contracts 接口 + 假 frame 同构。
- 编译期模块与 mygo「Apps pay only for what they use」的构建纪律有张力：UI 模块编进同一二进制，
  链接体积与初始化成本需要按产品 assembly 裁剪（build tags/多 main）。

## 5. 集成建议（最小路径）

- **P0（先做，验证收益）**：新增 `internal/contracts/ui`（ServiceKey + 接口 + 贡献值类型，不依赖
  ui 包）+ 宿主 `UIRuntime` builtin（注册表 + 单线程纪律）；在 `shell.App.View` 首尾插入两个钩子
  （帧首 drain、screen/slot 组合），先接一个真实贡献点（建议 tool renderer 或 settings page，
  改动面最小）。加载点放 `mygo.App.WhenReady` 回调内（main thread），用 `IsMainThread` 校验 owner。
- **P1（证据升级）**：假 frame 换成 `ui.NewTester` + `Texts/HasText` 真实 headless 断言；CI 跑每个
  产品 assembly `Validate` + headless snapshot；保留假 frame 做快速单测。
- **P2（扩大范围）**：settings（nav/schema/persist/i18n/独立窗口）、commands（菜单/快捷键/palette）、
  per-window runtime、session 级子树（kernel Isolate/子 fiber）；再评估 optional requires / 两阶段 load。

## 6. 风险清单

| # | 风险 | 严重度 | 证据/来源 | 缓解 |
| --- | --- | --- | --- | --- |
| R1 | 假 frame 过度简化，真实集成才发现布局/输入/键冲突 | 高 | §4.1；`ui/context.go:14-16`、headless Tester | P1 换 `ui.NewTester` 真实断言；贡献 ID 映射 `c.Key` |
| R2 | goroutine 线程标记 ≠ mygo main thread；加载时机错会 off-thread | 高 | §4.2；`loop.go`、`surface.go:14/62-72`、WhenReady | owner 用 `IsMainThread`/`RunOnMain`；加载放 WhenReady；CI 断言 |
| R3 | 注册纪律靠自觉：裸注册不可回滚（已实测泄漏） | 中 | `TestKnownGapBareRegistrationLeaks`；内核只回滚 effect 所有权 | 统一 `contracts.Own`；AST/CI 扫描禁止直连注册表 |
| R4 | 每帧重建 + 元素 key 冲突（mygo 严格模式重复 key panic） | 中 | `ui.NewTester` strict；shell 用 `c.Key` | contribution ID 全链路唯一；slot 内命名空间；测试断言 |
| R5 | 贡献点数据形状不足（settings/toolcards/commands/slot 布局上下文） | 高 | §4.3；`settings_ui.go:279`、`toolcards.go:56-80`、shell switch | 先做窄接口；按真实页面逐步加参数（tokens/snapshot/window） |
| R6 | 多窗口/多 frame（settings 独立窗口）与单 runtime 假设冲突 | 中 | `internal/app/settings_window.go` | 每窗口一个 UIRuntime/Frame，共享主线程；贡献按窗口 scope 过滤 |
| R7 | module 预检拒绝「required 无 provider」→ 后端/UI 不同 assembly 时无法启动 | 中 | `module/validate.go:189-224`；§2.4 | 后端与 UI 同一 assembly；或 optional requires + 运行期门控；两阶段 load |
| R8 | 编译期模块与 mygo 构建裁剪纪律冲突（体积/初始化成本） | 低-中 | mygo AGENTS.md「apps pay only for what they use」 | 每产品 build tags/多 main；模块惰性初始化；CI 体积基准 |
| R9 | Post 语义差异：无 signal 合并、shutdown 后拒绝、嵌套主循环（菜单/对话框） | 低-中 | `loop.go` mainLoop.post/shutdown、headless `later` | 桥到 mygo mainLoop/surface，不另建队列；关闭后丢弃语义对齐 |
| R10 | 测试证据可移植性：goroutine id 解析非公开 API、TID 仅 Linux | 低 | `uiruntime/thread*.go` | 生产用 mygo backend；测试平台限定/跳过非 Linux |
| R11 | 内核/module 层 v0.1.x：无运行期 disable、无热更新、无 UI 概念 | 中 | 01 报告 §3.3.6/§6.1 | 与「配置变更=重启」决策对齐；module 当可替换薄层 |

## 7. 复现与文件清单

```text
.probe/research/kernel-split/ui/spike/     # 独立 go module，36 测试全绿，-race 全绿
  contracts/                                # ServiceKey、UIRuntime 接口、贡献值类型、Own()
  uiruntime/                                # 注册表/排序/冲突、Post 队列、假 frame、线程标记
  uimodules/                                # ui/home、ui/settings、ui/toolcards、ui/broken、
                                            # app/sessions、host/group（6 个 Descriptor）
  host/                                     # 产品宿主 + 6 份 assembly.json + e2e_test.go
  README.md                                 # 复现命令、设计、测试→task 映射、限制
docs/research/kernel-split/06-ui-spike.md   # 本文件
```

未修改 `/data/mygo` 的 `internal/`、`cmd/`、`go.mod`、`go.sum`；未 git commit；spike 位于 gitignore
的 `.probe/` 下；产品仓库唯一新增文件就是本报告。

**一句话结论**：编译期 UI 模块组装在 kernel 上成立且已验证到「贡献点 + 所有权 + 装配 + 门控 +
线程 + 回滚」；接到真实 mygo 的剩余工作是 frame/线程/数据形状三件事，不是内核语义。
