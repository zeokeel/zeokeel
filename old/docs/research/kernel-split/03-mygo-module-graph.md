# mygo 现状依赖图与模块拆分映射（研究线 C）

> 对象：`/data/mygo`（module `github.com/zeokeel/zeokeel`，Go 1.27.3，HEAD `40368d1`，工作树只读）
> 目标：量化 30 个 `internal` 包 + `cmd/zeokeel` 的耦合结构，给出「若用 `zeokeel-kernel/module` 的编译期注册表按 DSH 方式装配」的候选模块划分、阻塞清单与工作量。
> 证据分级：**[A]** 本报告命令实测（可复现，命令见附录 A）；**[B]** 读源码/文档并给 `file:line`；**[C]** 推断与估算（人日、风险）。
> 产出物：本文件 + `.probe/research/kernel-split/mygo/`（`graph.go`、`collapse.py`、`module_map.json`、`out/{edges,metrics,ranking,layers,global_state_hits,modules}.{tsv,md,json}`）。

## 0. 结论摘要

**[A]** 规模：`go list ./...` = **31 个包**（1 个 `cmd/zeokeel` + **30 个 internal 包**）。非测试 Go 文件 285 个、**91,143 行**；测试文件 376 个、**94,980 行**；合计 186,123 行。任务书里的「32 个 internal 包」不成立：`internal/` 下 30 个目录，其中 `internal/provider/` 本身无 Go 文件（只有 `provider/openai` 子包），`internal/provider/openai`、`internal/tools/winenc` 才是包。全仓测试/基准/Fuzz 函数 1,954 个。

**[A]** 图：内部生产 import 边 **71 条**（GOOS=windows 与 linux 视图完全一致），无 Go import 环，DAG 深度 7 层。枢纽：`internal/runtime`、`internal/store` 各被 8 个包 import；`internal/state`、`internal/winproc` 各 4；传递影响面 `state`=10、`runtime`=9、`store`=8、`winproc`=7。`internal/app` 出度 20、`internal/shell` 出度 16——组装根与 UI 是最大的耦合汇聚点。

**[C]** 结论（有保留）：**能按 DSH 风格拆的只有「非 UI 的能力/数据/工具模块」，共 20 个；UI 外壳与进程生命周期必须留在宿主。** 20 个候选模块折叠后无环、LOC 守恒（50,170 + 宿主 36,921 + 共享库 4,052 = 91,143），但前提是先完成 6 个 T0 阻塞修复与契约抽取（T1）。总工作量约 **50–78 人日**（不含 `internal/shell` 自身的 AR-04 重构 15–30 人日，二者叠加约 65–108 人日）。

**[B]** 收益边界：编译期注册表 = link-time composition，**不会减小 51.8MB 二进制、不提供动态加载/沙箱/热更新**（内核文档自述：`zeokeel-kernel@v0.1.1/docs/COMPILE-TIME-REGISTRY.md:200`「二进制体积/启动链接成本随模块集增长 → 用 build tags/按产品拆 main 包」；`module/module.go:1-30` 包注释）。拆分换来的是：三份组装根（desktop / headless / ACP）统一、启动前预检、类型化配置、按平台/产品用 build tags 裁剪模块集、依赖方向可被 CI 断言——不是体积或运行时插件能力。

## 1. 依赖图（核心）

### 1.1 口径与总体

**[A]** 命令（附录 A.2）：`GOOS=windows go list -e -json ./...` 解析 `Imports`，仅保留 module 前缀内边；测试 import 单独计。结果：**31 节点 / 71 条生产边 / 1 条仅测试边 / 0 环**；linux 视图与 windows 视图边集完全相同（`edges.tsv`）。唯一仅测试边：`internal/runtime` 的**外部测试包** `internal/runtime/ask_user_blocking_test.go`（`package runtime_test`）import `internal/tools`——不构成生产环。

**[A]** 分层（层 = 距叶子的最长路径，0 为最底层叶子）：

| 层 | 包数 | 包 |
| --- | --- | --- |
| 0 | 15 | acp, brand, globalhotkey, i18n, icons, modelcatalog, prompt, sessionreference, state, store, tools/winenc, updater, websearch, winproc, workspacechanges |
| 1 | 7 | automation, design, importer, lsp, memory, presets, runtime |
| 2 | 4 | agentteam, mcp, provider/openai, skills |
| 3 | 1 | tools |
| 4 | 1 | session |
| 5 | 1 | shell |
| 6 | 1 | app |
| 7 | 1 | cmd/zeokeel |

### 1.2 骨架（只画跨层主干；全量 71 边见附录 B）

```
cmd/zeokeel ──> app, store, acp, importer, updater
internal/app (8,153 行, 出度 20)
  ├─> shell ──> design,brand,i18n,icons,automation,globalhotkey,modelcatalog,
  │            presets,runtime,session,sessionreference,state,store,tools,updater,winproc
  ├─> session (8,840 行, 出度 8) ──> modelcatalog,presets,runtime,sessionreference,
  │                                  state,store,tools,workspacechanges
  │      └─> tools (12,007 行, 出度 10) ──> agentteam,automation,lsp,mcp,memory,
  │                                          runtime,skills,store,tools/winenc,winproc
  │             └─> runtime ──> state
  ├─> store (5,887 行, 出度 0)
  ├─> acp, agentteam, automation, globalhotkey, importer, lsp, mcp, memory,
  │   presets, prompt, provider/openai, skills, state, tools, updater, websearch
  └─> (数据/契约叶子) modelcatalog, sessionreference, workspacechanges, winproc
```

### 1.3 枢纽包与影响面（[A]）

| 包 | in | out | 传递依赖者数 | 非测试 LOC | 判断 |
| --- | --- | --- | --- | --- | --- |
| runtime | 8 | 1 | 9 | 6,434 | **第一枢纽**：被 agentteam/mcp/provider/session/shell/skills/tools/app 依赖；把 `Provider/Tool` 契约留在包内是跨模块耦合根源 |
| store | 8 | 0 | 8 | 5,887 | **数据枢纽**：8 个包直接持 `*store.Store`（含 shell 59 处 `.Store()` 链） |
| state | 4 | 0 | 10 | 1,345 | 纯事件/快照库，无包级变量（干净）；被 runtime/session/shell/app 依赖 |
| winproc | 4 | 0 | 7 | 154 | OS 微工具，被 lsp/mcp/shell/tools 依赖；应做共享库而非模块 |
| tools | 3 | 10 | 4 | 12,007 | 既被 session/shell/app 依赖，又依赖 10 个包；24 个导出构造函数（单个/成组）集中在一个包 |
| presets / sessionreference / automation / updater | 3 | 0–1 | 3–5 | 398–713 | 小型可服务化包 |
| acp / agentteam / mcp / lsp / provider | 1–2 | 0–2 | 1–5 | 1,171–1,863 | 天然扩展点（见 §4） |
| app / shell / session | 0–1 | 20/16/8 | — | 8,153 / 25,229 / 8,840 | 组装根与 UI 巨型状态，见 §3.3 |

### 1.4 反向依赖与循环风险（[A]+[B]）

Go 编译器保证无 import 环；风险在**概念分层被契约方向倒置**，拆模块时若不先抽契约，会逼出「必须整体保留」的组合：

1. **会话编排 ↔ 工具实现类型倒置**：`internal/session/controller.go:23` import `internal/tools`；`goal_driver.go:10` 同。session 引用 `tools.Goal`（27 处）、`tools.Question`（16）、`tools.Todo`（14）、`tools.Teammate`（10）、`tools.Artifact`（5）等**实现包内类型**。→ session 模块无法独立：必须先把 Goal/Todo/Question/Teammate/Artifact 抽到共享契约库（§7 的 `LIB_toolsapi`），否则 session 与 tools 只能同模块。
2. **提供方反向依赖运行时契约**：`internal/provider/openai/openai.go:36`、`internal/mcp/mcp.go:40`、`internal/lsp/*`、`internal/tools/*.go` 均 import `internal/runtime` 以取得 `runtime.Provider`（`runtime/provider.go:236`）、`runtime.Tool`（`runtime/tool.go:11`）、`runtime.SubagentRunner`（`runtime/subagent.go:58`）。契约放在 runtime 包内 → provider/mcp/lsp 只能位于 runtime 之上，runtime 被迫成为底层；拆模块时建议拆出 `runtime/api` 契约包（纯类型/接口，无 eino 依赖），否则出现「provider 模块 → runtime 模块 → eino」的编译期重依赖。
3. **工具实现依赖能力服务**：`internal/tools/lsp.go:13`、`mcp_resources.go:10`、`memory.go:11`、`subagent.go:12`、`team.go:10`、`inspect.go:15-17` 使 tools 依赖 lsp/mcp/memory/agentteam/skills。这与 DSH 风格相反：**应由能力模块自己向工具注册表注册工具**（§4.1 给出证据），tools 不应知道 lsp/mcp 的存在。
4. **模块级环风险**：上述 1–3 若不先做契约抽取，把 tools、session、runtime 当三个模块会形成「session→tools→runtime」「session→tools→agentteam→runtime」「provider→runtime」等跨模块双向依赖语义；届时只能把 tools+session+runtime 合并成一个巨型模块。本报告的 20 模块映射在**契约抽取完成的前提下**（§6）实测折叠后 0 环。

## 2. 度量（[A]）

### 2.1 汇总

| 指标 | 数值 | 说明 |
| --- | --- | --- |
| 包 | 31（30 internal + 1 cmd） | `go list ./...`；无 `internal/provider` 根包 |
| 非测试文件 / LOC | 285 / **91,143** | 含所有 build tag 文件并集（`_test.go` 除外，含空行/注释） |
| 测试文件 / LOC | 376 / **94,980** | 包目录内统计（另有 `internal/e2e` 1 个 build-tag 文件）；测试 LOC ≈ 生产的 1.04 倍 |
| 测试/基准/Fuzz 函数 | 1,954 | `grep -rhoE '^func (Test|Benchmark|Fuzz)'` |
| 白盒 vs 黑盒测试文件 | 325 同包 / 47 外部包 | `go list`（linux 视图）`.TestGoFiles/.XTestGoFiles`；87% 是白盒 → 拆包会拖测试 |
| 导出符号 | 函数 233 / 类型 319 / 变量 102 / 常量 501 / 方法 676 | AST 统计，前缀大写 |
| 平台文件 | 34 个文件带 `//go:build` | app 8、globalhotkey 10、tools 5、winproc 3、winenc 3、session 3、provider 1、e2e 1 |
| 跨平台目标 | 4（linux/amd64+arm64、darwin/arm64+amd64、windows GUI+console） | `README.md` 硬约束 C3、`scripts/build.sh` |

全量 31 包明细（文件数/非测试 LOC/测试 LOC/导出符号/全局变量统计/入出度/层级）见 `.probe/research/kernel-split/mygo/out/metrics.tsv`；Top10 摘要如下。

### 2.2 Top 10

| 排名 | 非测试 LOC | | 测试 LOC | | 导出方法 | |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | shell 25,229 | | shell 29,465 | | tools 201 | |
| 2 | tools 12,007 | | app 12,145 | | session 140 | |
| 3 | session 8,840 | | session 10,419 | | store 85 | |
| 4 | app 8,153 | | tools 8,941 | | runtime 52 | |
| 5 | runtime 6,434 | | runtime 8,882 | | app 43 | |
| 6 | store 5,887 | | store 4,213 | | design 28 | |
| 7 | globalhotkey 3,869 | | globalhotkey 2,452 | | shell 22 | |
| 8 | importer 2,341 | | importer 1,947 | | agentteam 13 | |
| 9 | acp 1,863 | | acp 1,933 | | mcp 12 | |
| 10 | prompt 1,398 | | provider/openai 1,524 | | acp 11 | |

另：导出类型 Top3 为 acp 57、tools 56、runtime 39；包级变量 Top3 为 shell 121、icons 32、runtime 32（含义见 §3）；导出常量 Top3 session 72、acp 46、prompt 46。

### 2.3 解读

- `shell`（25,229 行，59 文件，779 个 `func (a *App)` 方法）+ `design/brand/icons/i18n`（1,696 行）= **26,925 行（29.5%）**是 UI 宿主；`app`+`cmd` 9,318 行是组装根；`session/runtime/tools/store` 33,168 行（36.4%）是待模块化的业务核心。
- 测试与生产同量级且以白盒为主：模块拆分必须**按包整体搬迁测试**，`app` 的 59 个测试文件（12,145 行）大量依赖 `internal/app` 未导出装配函数（如 `BuildServicesWith`），会是最大回归面。`docs/audit/04-architecture-quality.md:189`（AR-17）已记录 store/design/cmd 覆盖率低，拆分后再叠加测试迁移风险。

## 3. 全局状态与隐式依赖（T0 阻塞项，核心）

### 3.1 统计（[A]）

| 类别 | 数量 | 备注 |
| --- | --- | --- |
| 包级 `var` 声明（名字数） | **352** | 含错误哨兵、正则表、只读查找表；真正可变的集中在 shell/i18n/prompt/icons/app/store/session |
| `init()` | 3 | tools/terminal_other.go:32、shell/assets.go:31、cmd/zeokeel/cli.go:19 |
| `sync.Once` 字段/局部 | 13 | 见 3.4 |
| `os.Getenv/LookupEnv` | 19 处直读（生产共 31 个环境变量名） | 见 3.5 |
| 全局 `log.*` 调用 | **99** 处（6 个包） | app 72、cmd 16、globalhotkey 4、acp 3、session 3、provider 1 |
| `time.Now()` | 66 | app 7、session 9、shell 14、runtime 7、importer 4、其余散布 |
| 随机 | 2 | provider 重试抖动 `provider/openai/retry.go:122`；crypto/rand 用于 token |
| 默认路径解析 | 14 | `os.UserHomeDir/Executable/Getwd`，见 3.5 |
| `go func(` | 21 | 后台 worker；模块化后需改用 `module.SafeGo` 或模块自有 goroutine 管理 |
| `//go:embed` | 3 | shell/assets.go:20,23（logo PNG）；icons/icons.go:15（SVG FS） |

### 3.2 全局 logger（T0-1，阻塞全部分模块）

**[B]** 日志初始化与输出是进程级单例：`cmd/zeokeel/logging.go:152` `log.SetOutput(io.MultiWriter(os.Stderr, w))`；崩溃输出 `cmd/zeokeel/main.go:75`；99 个调用点中 72 在 `internal/app`（如 `internal/app/app.go:168-170,325,362,376`），且 session/acp/globalhotkey/provider 也直接用 `log.Printf`（如 `internal/session/controller_compact.go:168`、`internal/acp/server.go:680`、`internal/provider/openai/retry.go:171`）。内核根 context 自带 `logger` 内置服务（`module/module.go` 的 `NewRegistry` 声明 logger provider，`logger.go`），但**现有代码没有 logger 抽象**。**判断：阻塞**——模块激活时序不同、模块可能被禁用/重建，全局 logger 无法表达「模块私有 logger + 宿主统一 sink」。修复：宿主 `app/logger` 服务（包装内核 logger 或 `log/slog`），99 处替换；约 **2–3 人日**。

### 3.3 UI 主线程与 shell 单例（T0-2，阻塞 UI 及一切同步回调）

**[B]** mygo 的线程契约：`github.com/egoist/mygo@v0.7.4/AGENTS.md:32-34`「Backend methods and handler callbacks run on the main thread. Public methods must be safe from any goroutine (`onMain`/`postMain`/`await` in `loop.go`). Never block the main thread waiting on work that needs it.」`ui/doc.go:18` 亦说明 view 在主线程逐帧调用。
**[A]** shell 规模：59 文件 / 25,229 行 / **779 个 App 方法**；`type App struct`（`internal/shell/app.go:29`）是单一巨型状态对象，字段含 draft/overlay/窗口 seam 等。
**[A]** shell 的全局缓存：**19 个 `sync.Map` 以 `*App` 为 key 的包级变量**，例如 `composer_slash.go:167`、`files_native.go:28`、`markdown.go:59`、`memory_ui.go:74`、`session_live.go:35`、`trajectory.go:76`。这等于把「per-App 状态」挂到包全局，模块实例/测试隔离都会失效。
**[A]** UI 直连业务：shell 内出现 **74 个不同的 `Ctrl.*` 方法**（调用点更多）与 **59 处 `.Store()` 直取链**（`grep -rhoE '\.(Ctrl|ctrl)\.[A-Z]'` / `\.Store\(\)\.[A-Z]`）；`docs/design/shell-refactor-plan.md:40-58` 已把「view 直接读 store、同步调 Controller」「UI 线程同步 DB」列为 AR-08/AR-04 风险，`docs/audit/04-architecture-quality.md:85,117` 为原始发现。
**[B]** app→shell 的注入式 seam：`internal/app/app.go:206` `shellApp := &shell.App{Ctrl: a.services}`；随后 `SetTeamRoster/SetTeamStop`（:209-210）、`SaveSessionLog`（:215）、`SetSettingsWindowOpener`（:231）、`SetInstallUpdateHook`（`internal/app/round3.go:68`、`internal/shell/settings_update_hotkey.go:39,66`）、`SetDefaultLanguageForTest` 等十余个函数变量被宿主覆盖。**判断：阻塞**——这不是 kernel 能解决的线程问题，而是 UI 框架的单主循环契约：任何「模块」若要在 UI 线程上触达 shell，必须走宿主的 `app/ui` 服务（`Post(fn)`/`Call`），且业务调用要异步化（AR-08）。修复工作量：shell 拆 read-model/actions/异步队列（`shell-refactor-plan.md` P1–P3）约 **15–25 人日**；不做则所有消费 UI 的模块无法启动/卸载。

### 3.4 包级可变变量 / init / sync.Once（T0-5，阻塞实例隔离与重载）

**[B]** 代表性证据与判断：

| 位置 | 内容 | 判断 |
| --- | --- | --- |
| `internal/i18n/i18n.go:33` | `var liveDefaultLanguage = LangZH`；`testDefaultLanguage` 仅测试 | **阻塞（轻）**：语言是可写全局；模块化后应由 `app/i18n` 服务持有 |
| `internal/prompt/registry.go:12,541` | `var registry = []registration{...}`、`var orderedRegistry = sortRegistry(registry)` | 不可变注册表（只读）；**非阻塞**，但需移到模块实例或契约包 |
| `internal/icons/icons.go:16-20` | `embed.FS files` + `once sync.Once` + `map[string]*ui.SVG` | 懒加载缓存；**非阻塞**，宿主 UI 内部保留 |
| `internal/shell/*`（19 处） | `sync.Map // map[*App]...`、`quitApp = func(){ mygo.App.Quit() }`（menus.go:18）、`checkForUpdate`/`installUpdateHook`（settings_update_hotkey.go:39,66）、`pickWorkspaceDirectory`（workspaces.go:1315）、`sessionLister`（sessions_cache_async.go:35）、`sessionOpenLoader`（session_open_async.go:32） | **阻塞**：per-view 状态全局化 + 宿主注入钩子，模块化会与多实例/测试冲突；随 AR-04 一起处理 |
| `internal/app/buildinfo.go:15-17` | `BuildVersion/BuildCommit/BuildTime` 包变量，由 `-ldflags` 注入 | **非阻塞**（一次性写入，启动早期），但应改为宿主 builtin 服务 |
| `internal/app/round3.go:68` | `var setShellInstallUpdateHook = shell.SetInstallUpdateHook` | 测试 seam 泄漏到生产装配；**阻塞（轻）**，随装配改造清理 |
| `internal/app/services.go:157` | `once sync.Once`（cleanup 幂等） | 局部，**非阻塞** |
| `internal/session/controller.go:3423`、`lsp/client.go:109-113`、`app/singleinstance.go:78`、`shell/start_tab.go:209`、`globalhotkey/dbus_linux.go:1149`、`globalhotkey/globalhotkey_darwin.go:56` | 各类 Once | 生命周期幂等，**非阻塞**，模块 Close/Dispose 时天然对应 |
| `internal/store/migrations.go:45`、`internal/session/tool_spill.go:36`、`internal/tools/terminal.go:86,93` | 测试 seam 函数变量（生产为 nil/默认实现） | **非阻塞**（nil 检查），但说明 session/store/tools 依赖「包级可覆盖」，拆包时要改成注入 |
| `internal/tools/terminal_other.go:32` | `init()` 覆盖 `terminalShellDefaults`/`terminalDeliverSignal`（平台默认 shell 与信号） | **半阻塞**：`init` 适合按 GOOS 的 build tag 注册，编译期注册表天然兼容（每平台注册不同模块） |
| `internal/shell/assets.go:31` | `init()` 解码 embed logo PNG → `*ui.Bitmap` | 宿主 UI 内保留，**非阻塞** |
| `cmd/zeokeel/cli.go:19` | `init(){ headlessRun = app.RunHeadless }` | **阻塞（轻）**：CLI 装配与 app 包循环式初始化；P0 应改成显式注册 |

### 3.5 数据目录 / 环境变量 / 默认路径（T0-3）

**[B]** 数据目录有 **3 处独立解析**，未来模块若各自读环境变量会漂移：
- `internal/app/app.go:34-45` `DataDir()`：`ZEOKEEL_DATA_DIR` → `os.UserHomeDir()` → `.zeokeel`（权威）；
- `internal/shell/files_root.go:53`、`internal/shell/plugin_details.go:95`：shell 自己再读 `ZEOKEEL_DATA_DIR`；
- `internal/store/store.go:83` `Open(dir)` 由调用方传目录；schema 在 `<dir>/index.sqlite`（`store.go:24`、`setting.go` 存 settings 表）。
另：`internal/session/controller.go:4816` 用 `c.store.Dir()` 加载用户 preset；skills root、spill 目录（`.zeokeel/tool-results`，`session/tool_spill.go:27`）、logs/backups 均由宿主拼接。

**[A]** 生产代码共出现 **31 个字面量环境变量名**，其中 **19 处是直接 `os.Getenv/LookupEnv`**，其余经 `read(key, env, def)`（`app/services.go:1014,2023`）或字符串拼接读取。分四类：

| 类 | 变量 | 位置 |
| --- | --- | --- |
| 宿主/路径 | `ZEOKEEL_DATA_DIR`、`XDG_CONFIG_HOME`、`COMSPEC` | app/app.go:35、app/autostart_linux.go:17、shell/files_root.go:53、shell/plugin_details.go:95、tools/terminal_other.go:37 |
| 更新 | `ZEOKEEL_UPDATE_FEED`、`ZEOKEEL_UPDATE_ALLOW_HTTP` | app/update_install.go:77,107、cmd/update_cli.go:163,175、shell/settings_update_hotkey.go:87 |
| 模型/能力 | `DEEPSEEK_API_KEY`、`ZEOKEEL_PROVIDER_*`（model/base_url/retry/timeout）、`ZEOKEEL_MCP_*`、`ZEOKEEL_LSP_*`、`ZEOKEEL_SKILLS_ROOT`、`ZEOKEEL_WEBSEARCH_*`、`ZEOKEEL_NETWORK_PROXY`、`ZEOKEEL_CONTEXT_BUDGET_TOKENS` | app/services.go:1014,1863,2023（通用 `read(key, env, def)`）；app/memory_auto.go:471；provider/openai |
| 平台/测试 | `DISPLAY`、`WAYLAND_DISPLAY`、`XDG_SESSION_TYPE`、`DBUS_SESSION_BUS_ADDRESS`（Linux 热键）；`ZEOKEEL_VISUAL*`、`ZEOKEEL_LIVE_*`、`ZEOKEEL_X11_TEST` | globalhotkey/{x11,wayland}_linux.go；CI/测试脚本 |

**[B]** 默认路径 14 处：`app.go:38`、`autostart_{darwin,linux,windows}.go`、`update_install.go:223`、`cmd/update_replacer.go:33`、`importer/importer.go:72`（读 DSH home）等。**判断：阻塞**——模块的 config 不能在激活时自行猜路径；宿主须提供 `app/paths` builtin。修复 **2–3 人日**。

### 3.6 时钟 / 随机 / goroutine（T0-5 的次要项）

**[B]** 66 处 `time.Now()` 均直连系统时钟；唯一可注入时钟是 `internal/automation/scheduler.go:27-35` 的 `Config.Now func() time.Time`（模式可推广）。`internal/provider/openai/retry.go:122` 用 `math/rand/v2` 做抖动；`internal/app/singleinstance.go` 的端口锁与窗口激活监听有 21 处 `go func`/后台 worker（如 `app/app.go:372`、`automation/scheduler.go:89`、`runtime/model_adapter.go:98`）。**判断：非 T0 阻塞**，但模块化后测试注入与 `module.SafeGo` 需要时钟/随机服务（可并入 `app/clock`，1–2 人日，可延后）。

### 3.7 汇总判断

| 类别 | 阻塞级别 | 依据 | 修复归属 |
| --- | --- | --- | --- |
| 全局 logger（99 处） | **T0** | 3.2 | P0/P1：logger 服务 |
| UI 主线程 + shell 单例 | **T0** | 3.3 | P1/P2：AR-04 + UI 服务 |
| 数据目录 3 处 + env 19 处 | **T0** | 3.5 | P0：`app/paths` builtin |
| 手工装配/函数钩子/3 组装根 | **T0** | §4.2 | P0：registry + assembly |
| 包级可变状态（19 sync.Map 等） | **T0** | 3.4 | P1：随 shell/宿主改造 |
| 进程级职责（单实例/托盘/更新替换/热键线程） | **T0** | app.go、update_install.go、globalhotkey | P0/P1：宿主服务 |
| 时钟/随机/goroutine | T1 | 3.6 | P2：可选服务 |
| init() 3 处 | T2 | 3.4 | 随平台模块化 |

## 4. 现有扩展点（天然模块候选）

### 4.1 扩展点与手工接线（[B]）

| 扩展点 | 现状 | 接线证据 | 模块化判断 |
| --- | --- | --- | --- |
| 工具注册表 | `runtime.Registry`（`internal/runtime/tool.go:24-56`），不可变、sealed 后拒绝注册 | `internal/app/services.go:684-991` `registerAllToolsWithOptions`（≈308 行）：14 处直接 `_ = reg.Register(...)` + 15 组 `register(...)` 批量注册；24 个 `tools.New*Tool(s)` 导出构造函数；开关 `pluginOn(...)` 8 处 | **首选模块化点**：每个工具模块激活时向 `app/tool-registry` 注册；工具→能力依赖改为能力模块自注册（lsp/mcp/memory 工具文件应搬走） |
| 内置插件开关 | 7 个 builtin（subagent/web_search/agent_loop/shell/skills/mcp/lsp）+ 3 个 experimental（agent_teams/auto_review/developer_tools） | `internal/shell/plugins_ui.go:23`、:75；读取在 `app/services.go` `pluginOn("...")`；设置键 `plugin.<name>.enabled` | 直接映射为 assembly 的 `Disabled`/entries（kernel `Entry.Disabled`，`module/validate.go:18-43`） |
| 命令 | 16 个 slash（`shell/composer_slash.go:68`）、7 个菜单（`shell/shortcuts.go:378`）、4 个 CLI 子命令（`cmd/zeokeel/commands.go:28`：run/import/update/acp） | shell 全部内联在 `App` 方法里；CLI 已是表驱动 | slash/菜单属 UI 宿主；CLI 子命令可模块化（importer/acp/updater 已基本独立） |
| 设置页 | 15 个 Tab（settingsTabAbout…Updates，`shell/settings_ui.go`）；持久化走 SQLite `settings` 表自由键 | `session.SettingsStore`（`controller.go:2833`）；`store.Setting` API（`store/setting.go:11,27`）；shell 每帧缓存一次（`settingsLoaded`） | Tab = UI；**设置项定义可成为模块 manifest 的一部分**（模块声明 config schema，宿主渲染），是 DSH 式扩展的关键收益 |
| Provider | `runtime.Provider` 接口（`provider.go:236`）；`provider/openai.New` | `app/services.go:993` `buildAgent`、:1008 `buildAgentWithOptions` | 直接做成模块（provides `app/provider`，requires `app/settings`） |
| i18n | 465 键目录（`internal/i18n/catalog.go`）；`i18n.T(lang,key)` | shell 兼容视图 `shell/i18n.go:41`；测试钩子 `i18n.SetDefaultLanguageForTest` | 可模块化（provides `app/i18n`），但 shell 渲染同步读 → 宿主内保留缓存 |
| automation | `automation.Scheduler`（`scheduler.go:11-35` 定义 `Store` 接口 + `OnFire` 回调） | `app/app.go:720` `startAutomation`；`shell/automation_ui.go` | **已具备 DI 形态**，易模块化 |
| memory | `internal/memory`（pure core）+ `app/memory_auto.go` 抽取 worker | `app/services.go:885` 注册 memory 工具；`app/memory_auto.go:471,578` | 模块化（requires store） |
| skills | `skills.NewTolerantIn(root, dir)` 直接返回 `runtime.Tool` | `app/services.go:936-951` | 模块化，但需 workspace 服务与 tool-registry |
| mcp | `mcp.Manager`/`mcp.ServerConfig`；workspace 配置 fail-closed | `app/services.go:1954` `connectMCP`、:953-980 注册 4 个资源工具 | 模块化（requires paths/winproc；自注册工具） |
| lsp | `lsp.LoadServers`、`lsp.NewStdioProvider` | `app/services.go:850-870` | 模块化（requires paths/winproc；自注册工具） |
| agentteam | `agentteam.Manager`，依赖 `runtime.SubagentRunner` seam | `app/session_agents.go`；`tools/team*.go`；`shell/team_ui.go` | 模块化（requires `app/agent-runner`） |
| acp | `internal/acp` 纯传输 + `app/acp_adapter.go` 适配 | `cmd/acp_cli.go`；`app/headless_runtime.go` | 适配器需从 app 拆出，才能成为独立模块 |
| updater | 引擎 + `app/update_install.go` 安装/重启 | `shell.SetInstallUpdateHook`（round3.go:68） | 引擎可模块化，**install-on-restart 必须留宿主**（替换可执行文件 + 重启） |
| importer | 只被 CLI 使用（`cmd/import_cli.go`）；依赖 store | `internal/importer/importer.go` | **最干净的模块候选**（无 UI、无 runtime 依赖） |

### 4.2 三份组装根：模块化的直接动机（[B]）

同一套服务在 `internal/app` 内被手工装配了 3 次：

- 桌面：`app.go:313 startServices` → `app.go:316 BuildServicesWith(dir, factory)`；
- headless CLI：`headless.go:51 BuildServicesWith`；
- ACP 长驻运行时：`headless_runtime.go:86 BuildServicesWith`。

接线职责还被单独测试：`internal/app` 下有 **11 个 `*_wiring_test.go`**（auto_title/checkpoint/compaction/fork_attachment/history/memory_scope/memory_used/memory/round3/team/update_shell），另有 `plugins_test.go`、`lsp_plugin_test.go`、`model_configured_test.go` 等——这既证明「装配逻辑属于 app」是被现有测试强制的，也意味着 P0/P1 改装配方式会连带这些测试。

`BuildServicesWith`（`services.go:99-193`）内部又完成 store 打开、controller 构造、agent factory、preset 载入、cleanup 编排；`app.run()`（`app.go:175-313`）再叠加窗口/tray/通知/自动化/热键/更新。**三份组装根 + 308 行工具注册 + 8 个插件开关**是最适合被 `Registry + assembly.json` 取代的「手工接线」面。注意 headless/ACP 已是**无 UI 组装根**——证明业务栈不依赖 shell 即可运行，是拆分可行性的直接证据。

## 5. 配置面

### 5.1 现状三层

1. **数据目录**（宿主）：`app.DataDir()`（`app.go:34`）→ `store.Open`（`index.sqlite`）、JSONL transcript、`logs/`、`backups/`（store 迁移前快照）、`presets/`、`skills/`、`.zeokeel/tool-results`。
2. **用户设置**（SQLite `settings` 表，运行时实时生效）：15 个设置页；`session.Settings` 14 个字段（`session/controller.go:2727-2760`：permission/language/theme/font_size/work_details/show_coding_view/collapse_work_details/send_behavior/performance_usage/upload_session_log/memory_*）；`runtime.SettingsAllowedKeys` 33 键 + 10 个允许前缀 + 写白名单 29 键/拒绝 9 键（`runtime/settings.go:32-180`，供模型 settings 工具读写）；`grep` 到 32 个去重设置键/前缀（如 `provider.base_url`、`mcp.servers`、`plugin.web_search.endpoint`）。
3. **环境变量**（31 个名称，其中 19 处直接读取，见 3.5）与 **ldflags 注入**（`cmd/zeokeel/version.go:15-17`、`app/buildinfo.go:15-17`）。

### 5.2 拆分后归属建议

| 配置项 | 归属 | 理由 |
| --- | --- | --- |
| 数据目录/日志目录/单实例锁/窗口状态/autostart | **必须宿主**（`app/paths` builtin） | 进程级资源，模块不得自行解析（现状 3 处解析需收敛） |
| logger sink/level、panic/crash 输出 | **必须宿主**（内核 logger builtin 或包装） | 全局进程副作用 |
| UI 主题/TextScale/语言目录/窗口 | **必须宿主 UI** | 主线程与渲染状态 |
| assembly 拓扑（启用哪些模块、父子嵌套、optional/disabled、workspace 根、平台模块集） | **assembly.json（模块 config）** | kernel `Entry{ID,Module,Config,Disabled,Optional,Children}`（`module/validate.go:18-43`），启动预检（`Validate` :113、`Load` `load.go:95`） |
| 模块参数（provider endpoint/model、工具白名单/输出上限、技能根、MCP server 列表、LSP server 列表、automation 开关） | **模块 config JSON** + 宿主 settings 服务兜底 | 属结构性参数；secrets（API key）不进 assembly.json，走宿主 settings/凭据服务 |
| 用户偏好（权限模式、字体、发送行为、memory 模式等） | **宿主 `app/settings` 服务（SQLite 实时生效）** | 现状保存即生效（`settingsChangedHook`，app.go:231）；kernel 配置只在启动读一次（`module/config.go:12-35` 文档注释明确「无进程内热配置更新，改配置=重启」，`Module.LoadFile` 在 :72），不能硬搬 |
| 平台差异（Win32/Darwin Carbon/X11/Wayland、COMSPEC/pwsh） | **build tags + 每平台注册表** | 现状已用 34 个 `//go:build` 文件；kernel 编译期注册天然适配 |
| 更新 feed/签名/安装路径 | 宿主（updater 引擎可模块） | 替换可执行文件+重启是进程职责 |

**结论**：kernel 的 assembly.json 适合承载「**拓扑 + 结构性参数**」，不适合承载「用户实时设置」与密钥；后者必须由宿主 `app/settings` builtin 暴露给模块。若强行把设置搬进 assembly.json，会丢失当前「保存即生效」的产品行为（需重启），属功能回退。

## 6. 候选模块划分（核心）

### 6.1 原则

- 模块 = Go 包（或包组）+ `module.Descriptor[T]`；**不是目录名映射**：跨包契约（state 事件、tool 接口、Goal/Teammate 类型、sessionreference、modelcatalog）先抽成**共享库/契约包**（LIB_*），模块与宿主都可 import，不作为独立 assembly 单元。
- 宿主保留 3 个单元（进程生命周期 + UI + 维护），20 个候选模块（能力/数据/工具），6 个共享库。
- 工具按域拆成 5 个子模块（`internal/tools` 现为 36 文件 / 12,007 行单包，必须先拆子包；共享 `sandbox`/config 辅助抽到 `LIB_toolsapi`）。
- 每个模块用 kernel API 声明：`module.Define(id, factory).AddProvides(module.Provides(Key)).AddRequires(module.Requires(Key))`，可选依赖用 `AddOptionalRequires(module.RequiresOptional(Key))`（`module/module.go:65,70,75`）；宿主用 `registry.AddBuiltin(...)` 声明路径/设置/UI 等 builtin。

### 6.2 20 个候选模块（[A] LOC + [C] Provides/Requires）

| # | 模块 ID | 来源包（LOC） | 职责 | Provides（服务键） | Requires（必需；可选） | 层 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | mygo/store | store (5,887) | SQLite 索引 + JSONL transcript + 迁移 | `app/store: *store.Store` | `app/paths` | 0 |
| 2 | mygo/prompt | prompt (1,398) | 系统提示词章节合成 | `app/prompt: Composer` | —（纯函数；registry 只读） | 0 |
| 3 | mygo/runtime | runtime (6,434) | eino Agent 包装、工具回路、审批、压缩、checkpoint | `app/agent-factory`、`app/tool-registry` | `app/provider`（可选）、`app/store`（可选：history/checkpoint） | 0 |
| 4 | mygo/provider-openai | provider/openai (1,193) | OpenAI 兼容 SSE provider | `app/provider: runtime.Provider` | `app/settings` | 1 |
| 5 | mygo/presets | presets (645) | 内置/用户 agent 预设 + 工具白名单 | `app/presets: *presets.Registry` | `app/prompt`、`app/paths` | 1 |
| 6 | mygo/memory | memory (1,059+543 工具) | 记忆 scrub/dedupe/consolidate + memory 工具 | `app/memory` | `app/store`；`app/tool-registry` | 1 |
| 7 | mygo/skills | skills (772) | SKILL.md 多根加载 + skill 工具 | `app/skills` | `app/paths`、`app/tool-registry`、workspace 根 | 1 |
| 8 | mygo/mcp | mcp (1,171+530 工具) | MCP 服务器连接、工具/资源/提示注册 | `app/mcp: *mcp.Manager` | `app/paths`、`LIB winproc`、`app/tool-registry` | 1 |
| 9 | mygo/lsp | lsp (1,347+326 工具) | LSP stdio provider + lsp 工具 | `app/lsp` | `app/paths`、`LIB winproc`、`app/tool-registry` | 1 |
| 10 | mygo/automation | automation (398) | 定时任务调度（已有 Store/OnFire 接口；OnFire 回调由宿主装配时注入，不新增模块边） | `app/automation: *automation.Scheduler` | `app/store` | 1 |
| 11 | mygo/agentteam | agentteam (733) | 异步子代理 Manager | `app/teams: *agentteam.Manager` | `app/agent-runner`（runtime.SubagentRunner） | 1 |
| 12 | mygo/tools-files | tools: fs/search/editor/image/workdir (1,595) | 文件读写编辑、glob/grep、读图、cwd | 注册到 `app/tool-registry` | `app/tool-registry`、`app/settings`（输出上限） | 1 |
| 13 | mygo/tools-exec | tools: terminal/jobs/bash/pwsh + winenc (2,681) | PTY/作业/平台 shell | 注册工具 | `app/tool-registry`、`app/settings`、`LIB winproc` | 1 |
| 14 | mygo/tools-agent | tools: todo/goal/ask_user/present/exit_plan/schedule/report/worktree/inspect (3,064) | 交互与计划类工具 | 注册工具 | `app/tool-registry`、`app/store`、`app/automation`、`app/mcp`(可选)、`app/skills`(可选) | 2 |
| 15 | mygo/tools-orchestrate | tools: subagent/workflow/ralph/team/team_spawn (2,152) | 编排类工具 | 注册工具 | `app/tool-registry`、`app/teams` | 2 |
| 16 | mygo/tools-web | websearch (302) + tools/webfetch (297) | web_search/web_fetch | 注册工具 | `app/tool-registry`、`app/settings`（endpoint/key） | 0 |
| 17 | mygo/session | session (8,840+730 工具) | 会话控制器：回合、审批、goal、历史、压缩 | `app/sessions: *session.Controller` | `app/store`、`app/agent-factory`、`app/tool-registry`、`app/prompt`、`app/presets`、`app/settings`、`app/paths`、LIB(state/modelcatalog/sessionref/wschange) | 2 |
| 18 | mygo/acp | acp (1,863) | ACP v1 stdio server（需把 `app/acp_adapter.go` 拆出） | `app/acp-server` | `app/sessions`、`app/headless-runtime` | 0 |
| 19 | mygo/importer | importer (2,341) | DSH 数据导入（仅 CLI 使用） | `app/importer` | `app/store`、`app/paths` | 1 |
| 20 | mygo/hotkey | globalhotkey (3,869 union；win 893) | 平台全局快捷键（Win32/X11/Carbon/D-Bus） | `app/hotkey` | `app/ui`（窗口显示回调）、`app/settings` | 0 |

> 表中「层」为**实测**折叠层（仅基于现有 import 边，`out/module_graph.json`）；表中新增的装配期 Requires（如 ACP 需 sessions、hotkey 需 ui）会让设计依赖变深，但方向都是「模块→宿主服务」，不产生模块间环（§6.5 实测 0 环）。

### 6.3 宿主 3 单元 + 共享库 6 项（不注册为模块）

| 单元 | 包（LOC） | 为什么必须留宿主 |
| --- | --- | --- |
| HOST_app | cmd/zeokeel + app (9,318) | 进程生命周期：单实例锁（`app/singleinstance.go`）、窗口/tray/关闭隐藏、日志 sink、数据目录、更新安装/重启、ldflags buildinfo、CLI 分发 |
| HOST_ui | shell + design + brand + icons + i18n (26,925) | **UI 主线程单循环**（mygo 契约，§3.3）+ 25k 行 God object；kernel fiber 无主线程保证，UI 不能做成可自由启停的模块 |
| HOST_maint | updater (678) | install-on-restart 要替换可执行文件并重启进程，属宿主维护职责（引擎本身可被宿主直接调用） |

共享库（编译期依赖，不作为 assembly 单元）：`state`(1,345，事件/快照，0 全局变量——最干净)、`modelcatalog`(357)、`sessionreference`(713)、`workspacechanges`(1,134)、`winproc`(154)、`LIB_toolsapi`(349，从 tools 抽出的 config/path/sandbox 辅助 + 待抽的 Goal/Todo/Question/Teammate/Artifact 类型)。另有 `internal/e2e`（1 文件，build tag `e2e`）仅为可选 E2E 测试，不参与装配。

### 6.4 与 kernel `module/` 的对应关系（[B]）

```go
// contract 包（共享库）示例：变量名即全局唯一服务名，类型受编译期检查
var StoreKey  = cordis.NewServiceKey[*store.Store]("app/store")
var PathsKey  = cordis.NewServiceKey[HostPaths]("app/paths")
var RegKey    = cordis.NewServiceKey[*runtime.Registry]("app/tool-registry")

// 模块注册（module/module.go:85 Descriptor、:111 Define、:278 NewRegistry、:297 Register、:315 AddBuiltin）
reg := module.NewRegistry()
reg.AddBuiltin(PathsKey)   // 宿主在 Load 前已提供的 builtin（module.go: AddBuiltin）
_ = reg.Register(module.Define("mygo/store", func(ctx cordis.Context, cfg StoreConfig) error {
    _, err := ctx.Provide(StoreKey, store.Open(cfg.Dir)) // 运行期校验 Provide（module/load.go verifyProvides）
    return err
}).AddProvides(module.Provides(StoreKey)).
  AddRequires(module.Requires(PathsKey)).
  AddOptionalRequires(module.RequiresOptional(SettingsKey)))
// assembly.json: {"version":1,"entries":[{"id":"store","module":"mygo/store","config":{"dir":"..."}},...]}
loader, err := module.LoadFile(root, reg, "assembly.json") // 预检+装配+回滚（validate.go:113, load.go:95, config.go:72）
```

kernel 提供的恰好是 mygo 缺的装配语义：typed config 解码 + Validate、provider 唯一性/缺失/类型不匹配/依赖环预检、optional 依赖、required 失败整树回滚、稳定 entry ID、`Locate/Close/Warnings`（`module/load.go`、`validate.go:113`）。**不提供**（文档明确）：动态加载、跨版本 ABI、热配置更新（`module/config.go:12-35`）、沙箱/权限隔离——这与「内置插件（无第三方安装/市场）」的产品决定（README 硬约束）一致。

### 6.5 折叠验证（[A]，`collapse.py`）

- 20 模块 + 3 宿主 + 6 库；折叠后单元级依赖 **67 条**：`HOST→MOD` 22、`MOD→MOD` 23、`MOD→LIB` 10、`HOST→LIB` 8、`LIB→MOD` 1（`LIB_toolsapi → runtime`，把 `runtime.Tool` 移入契约后消失）、`HOST→HOST` 3；单元内边 6 条；**0 环**。
- 模块层（仅模块 DAG）：层 0 = acp/hotkey/prompt/runtime/store/tools-web；层 1 = agentteam/automation/importer/lsp/mcp/memory/presets/provider/skills/tools-exec/tools-files；层 2 = session/tools-agent/tools-orch。
- LOC 守恒：模块 50,170 + 宿主 36,921 + 库 4,052 = **91,143**（与 §2.1 全量非测试 LOC 完全相等）。
- 该折叠成立的前提：§1.4 的 3 个契约倒置先被抽取（否则 tools/session/runtime 只能合并，模块数降到约 12 个，且单模块 >20k 行）。

### 6.6 明确不拆 / 拆不动

- `internal/shell`（+design/brand/icons/i18n）：UI 主线程、779 方法、19 个 `sync.Map`、宿主注入 seam；拆它的收益在 AR-04 内部分层（kit/text/model/actions/screens），不是 kernel 模块化；`docs/design/shell-refactor-plan.md:60-92` 已给出 P0–P4 计划。
- `internal/app`：组装根本身应**变薄**（把 §4.2 手工接线移入 assembly），而不是变成模块。
- `internal/winproc`(154 行)：微工具，做共享库比做模块更省启动/配置成本。
- `internal/e2e`：测试专用（build tag `e2e`）。
- 不建议拆：`design/brand/icons`（宿主 UI 渲染资源，无独立生命周期）。

## 7. 阻塞排序、阶段与人日（核心）

### 7.1 T0（不解决则任何模块化都会返工）

| # | 阻塞项 | 证据 | 影响 | 人日 [C] |
| --- | --- | --- | --- | --- |
| T0-1 | 全局 `log` 无抽象（99 处/6 包，含 cmd 初始化） | §3.2；`cmd/zeokeel/logging.go:152` | 模块无法拥有 logger/生命周期；日志顺序随装配变化 | 2–3 |
| T0-2 | UI 主线程 + shell God object + 19 个 `*App` 全局 Map | §3.3；`mygo/AGENTS.md:32-34`；`docs/audit/04-architecture-quality.md:85,117` | 任何 UI 相关模块无法安全启停；同步 DB 在 UI 线程 | 15–25（含 AR-04 P1–P3） |
| T0-3 | 数据目录 3 处独立解析 + 14 处默认路径 + 19 处 env | §3.5 | 模块配置漂移、便携模式/测试不可隔离 | 2–3 |
| T0-4 | 手工装配：`BuildServicesWith` + 308 行工具注册 + 3 份组装根 + 函数钩子 | §4.1–4.2；`app.go:206-240,313-385`；`services.go:99,684-991` | 无 registry/assembly 则「模块」只是换名 | 5–8 |
| T0-5 | 包级可变状态（19 shell sync.Map、i18n 语言、session Once、生产里的测试 seam） | §3.4 | 模块多实例/重载/测试隔离不成立 | 3–5 |
| T0-6 | 进程级职责未服务化（单实例锁 49731、托盘、更新替换、热键跨线程回调） | `app/singleinstance.go`、`app/update_install.go`、`globalhotkey/*` | 这些能力无法作为「可选模块」装配 | 2–3 |

T0 合计约 **29–47 人日**（其中 T0-2 是大头且与既有 AR-04 计划重叠）。

### 7.2 T1（拆对应模块前必须完成）

| # | 阻塞项 | 证据 | 人日 |
| --- | --- | --- | --- |
| T1-1 | session↔tools 类型倒置：Goal/Todo/Question/Teammate/Artifact 抽契约包 | `session/controller.go:23`、`goal_driver.go:10`；27/16/14/10/5 处引用 | 2–3 |
| T1-2 | runtime 契约拆分：`Provider/Tool/SubagentRunner/Message` 移到 `runtime/api`（无 eino 依赖） | `runtime/provider.go:236`、`tool.go:11`、`subagent.go:58`；provider/mcp/lsp/tools 反向 import | 2–3 |
| T1-3 | `internal/tools` 拆子包（36 文件/12,007 行）：files/exec/agent/orchestrate/web + 共享 sandbox 辅助 | §6.2；`internal/tools/{config,path}.go`、单包 24 个构造函数 | 3–5 |
| T1-4 | session 服务接口化：103 个导出方法（controller.go）+ shell 74 个调用点，先出 UI 子集接口 | `grep -c '^func (c \*Controller) [A-Z]'`；shell §3.3 | 5–8 |
| T1-5 | presets/skills/mcp/lsp 的路径与设置来源统一走 `app/paths`/`app/settings` | `controller.go:4816`（presets 用 store.Dir()）、`services.go:1014,2023`（通用 env 回退） | 2–3 |
| T1-6 | 工具注册服务化：`runtime.Registry` 的「激活期注册 + 宿主 final seal」语义 | `runtime/tool.go:31-56`（sealed 即拒绝） | 2–3 |

T1 合计约 **16–25 人日**。

### 7.3 T2（可延后，不阻塞首个可运行 assembly）

自动化/agentteam 的 OnFire/Runner 注入（1–2d）；acp adapter 从 app 拆出（2–3d）；i18n 目录模块化（1–2d）；globalhotkey 平台注册表化（2–3d）；时钟/随机服务（1–2d）；`go list -deps` 边界门禁 + assembly 校验进 CI（1d）；删除生产测试 seam（1d）。合计约 **9–14 人日**。

### 7.4 阶段建议（P0→P3）

| 阶段 | 目标 | 内容 | 验收 | 人日 [C] |
| --- | --- | --- | --- | --- |
| **P0 骨架** | 引入内核、三份组装根统一入口 | 加 `zeokeel-kernel` 依赖；新建 `internal/host`（registry + builtins：logger/paths/settings/ui）；写 desktop/headless/ACP **三份 assembly.json**；模块先做「薄适配器」包住现有包（factory 调现有构造函数），代码零搬动；`cmd` 手写注册表 | gofmt/vet/build/test 全绿；desktop 与 headless 行为不变；assembly 预检能在启动前报出缺失 provider | 5–8 |
| **P1 服务化** | 解决 T0-1/3/4/6 + T1-2 | logger 服务化（99 处）；paths/settings builtin 收敛；runtime 契约拆分；工具注册服务化；三份组装根改走 assembly；headless/acp 复用同一注册表 | 无全局 log；DataDir 仅 1 处；`go list -deps` 断言 module 间无反向依赖；headless 与 desktop 共用模块清单 | 15–25 |
| **P2 模块化** | 拆工具与能力模块（T1-1/3/4 + T0-2 前半） | tools 拆 5 子包并迁入 20 模块中的 12–16；lsp/mcp/memory/skills 自注册工具；presets/prompt/automation/agentteam/session 切换为模块；shell 保持宿主但改为消费 session 服务接口 | 20 模块可 Disabled//Optional 装配；required 失败整树回滚；单测随包迁移；插件开关由 assembly 驱动 | 20–30 |
| **P3 配置装配** | 平台/产品化与收尾 | assembly 类型化 config + Validate 全覆盖；plugin 开关、workspace 根、provider/工具参数入配置；settings 服务桥接；按平台 build tags 注册热键/OS 模块；CI 增加 assembly 预检与依赖边界；文档 | 三平台交叉编译通过；启动预检覆盖率 100%；文档化「能/不能」边界 | 10–15 |

**总计 [C]：50–78 人日**（P0 5–8 + P1 15–25 + P2 20–30 + P3 10–15）。注意阶段人日与 §7.1–7.3 的 T0/T1/T2 不是简单相加：T0-2 横跨 P1–P2、T1 已计入 P2、T2 分散在 P3，阶段合计是首尾相接的估算。若 `internal/shell` 的 AR-04 重构尚未完成，T0-2 的 15–25 人日必须叠加：**65–103 人日** ≈ 单人 3–5 个月 / 双人 1.5–2.5 个月。建议**不要**把「UI 模块化」计入本期；把 shell 视为宿主、只做 P1/P2 的 read-model/actions 分层。

### 7.5 二进制与「编译期注册表」边界（必须写进预期）

**[B]** 内核文档自述，注册表是 link-time composition：`module/module.go:1-30`「host binary explicitly registers the Go modules it was compiled with (there is no dynamic code loading)」，`docs/COMPILE-TIME-REGISTRY.md:196-200`「无人可加载未编译模块；二进制体积/启动链接成本随模块集增长 → 用 build tags/按产品拆 main 包」；`module/config.go:12-35` 明确无进程内热配置更新（改配置=重启，`LoadFile` 在 :72）。
**[A]** 本仓事实：`cmd/zeokeel` 链接 **434 个包**（31 内部 + 177 外部 + 标准库）；外部重量级模块为 eino(30 包)、mygo(29)、gonja(11)、MCP SDK(10)、sonic(9)、modernc sqlite/libc(6)；成品 `zeokeel` 51,828,236 B（debug 未剥离），`dist/zeokeel-linux-amd64` 35,451,143 B（`-s -w` 后 −31.6%）。把同样代码拆成 20 个包/模块**不会减少一字节可达代码**（Go 链接器按可达性裁剪，与包数量无关）；注册表层本身反而增加少量代码与启动预检（毫秒级）。
**[C]** 真正能减体积/攻击面的手段只有两种：build tags 或按产品拆 `main`（例如 console-only 构建排除 shell/mygo/eino-UI 相关），以及把可选模块做成独立二进制/子命令——这与 kernel 文档建议一致。**不要把「引入 kernel 模块化」当作二进制优化方案。**

## 8. 结论：能拆到什么程度（有保留）

**能（有工程条件）**：
1. **20 个非 UI 模块**可按 DSH 风格装配：store/prompt/runtime/provider/presets/memory/skills/mcp/lsp/automation/agentteam/tools×5/session/acp/importer/hotkey；折叠后无环（§6.5）。
2. **统一 3 份组装根**（desktop/headless/ACP）为「一份注册表 + 多份 assembly.json」，这是最大确定性收益：消除 §4.2 的重复装配与 `pluginOn` 手工开关，获得启动前预检与失败回滚。
3. **平台差异**天然适配编译期注册（现状 34 个 build-tag 文件、globalhotkey 10 个平台文件）。
4. 一批包**已经具备 DI 形态**：`runtime.Provider`、`runtime.Tool`、`runtime.SubagentRunner`、`automation.Store/OnFire`、`lsp.Provider`、`state.Broker`——模块化不是从零开始。

**不能 / 不建议**：
1. **UI 不能模块化**：mygo 主线程契约 + shell God object（779 方法、19 个全局 Map、25k 行）→ 必须留宿主；kernel 不提供线程调度语义。
2. **不能得到动态插件/热更新/沙箱/二进制缩小**：内核设计边界明确（§7.5）；「内置插件、无第三方安装/市场」的产品决定与此一致，不冲突但也无新增能力。
3. **不能绕过 T0/T1**：不先做 logger、paths、装配、契约抽取，直接「按目录拆模块」会把 tools/session/runtime 焊成一个 >20k 行的超级模块，收益接近零。
4. **测试风险高**：376 测试文件 / 94,980 行、87% 白盒；`app` 59 个测试文件直接依赖未导出装配。

**建议路径**（按投入产出排序）：P0 骨架 → P1 服务化（先拿到「三份组装根统一 + 启动预检」的确定性收益）→ 选 2 个低风险模块试点（`importer`、`tools-web`，或 `store`+`prompt`）→ 再 P2 大批量。**先不要动 `internal/shell`**；其 AR-04 重构独立推进，两者通过「session 服务接口」解耦。

> **补记（Lead，2026-11-06，用户新约束）**：本报告 §6.6/§8 的「UI 不能模块化、必须留宿主」需要修正为
> 「**UI 帧循环/窗口不能模块化；UI 功能面可以做成编译期贡献层**」（依据 04 §0.3 的 mygo 框架事实与
> 06 的 36 项 spike 实测）。UI 侧方案见 [05 UI 模块映射](05-ui-module-map.md)（16 个 UI 模块、宿主 chrome
> 3,413 行、UI 侧 105–140 人日），总报告以 [00](00-report.md) 为准。本报告其余结论（依赖图/后端 20 模块/
> T0/T1 阻塞/后端 50–78 人日）不变。

**判断清单（若以下全为是，则值得做）**：① 是否已有/计划第二产品形态（headless/ACP/console-only/其他 OEM）共用本内核？② 是否接受「配置=重启」？③ 是否愿意投入 ≥60 人日与测试迁移？④ 是否需要按产品裁剪模块集（而非动态插件）？只要 ①④ 为是、②可接受，本方向成立；若目标是「运行时装第三方插件 / 缩小 51MB 二进制」，则不成立。

## 附录 A：可复现命令

```bash
# A.1 环境（本报告所有 go 命令的前置）
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
cd /data/mygo && git log --oneline -1        # 40368d1

# A.2 包/依赖图/度量/全局状态证据（.probe 已 gitignore，不触碰产品代码）
go run .probe/research/kernel-split/mygo/graph.go \
  -module github.com/zeokeel/zeokeel -out .probe/research/kernel-split/mygo/out
# 产物：out/{edges.tsv,metrics.tsv,adjacency.md,layers.md,ranking.md,global_state_hits.md,packages.json}

# A.3 模块折叠/环检测（读 out/packages.json + module_map.json）
python3 .probe/research/kernel-split/mygo/collapse.py     # 产物 out/{modules.md,module_graph.json}

# A.4 关键数字的独立复核
go list ./... | wc -l                                      # 31
GOOS=linux go list -deps ./cmd/zeokeel | wc -l             # 434（含标准库）
grep -rn '^func init()' internal cmd --include='*.go' | grep -v _test   # 3
grep -rn 'os\.(Getenv|LookupEnv)(' internal cmd --include='*.go' | grep -v _test | wc -l  # 19
grep -rho 'log\.Printf' internal/app/*.go | wc -l          # 73
awk -F'\t' 'NR>1{a+=$3;b+=$9}END{print a,b}' \
  .probe/research/kernel-split/mygo/out/metrics.tsv        # 91143 94980
```

## 附录 B：完整生产 import 边（71 条，[A] `go list` GOOS=windows）

```
cmd/zeokeel: app, acp, importer, store, updater        (5)
internal/acp: —
internal/agentteam: runtime
internal/app: acp, agentteam, automation, globalhotkey, lsp, mcp, memory, presets,
              prompt, provider/openai, runtime, session, sessionreference, shell,
              skills, state, store, tools, updater, websearch        (20)
internal/automation: store
internal/brand: —            internal/design: brand            internal/i18n: —
internal/icons: —            internal/importer: store          internal/lsp: winproc
internal/mcp: runtime, winproc
internal/memory: store
internal/modelcatalog: —
internal/presets: prompt
internal/prompt: —
internal/provider/openai: runtime
internal/runtime: state
internal/session: modelcatalog, presets, runtime, sessionreference, state, store,
                  tools, workspacechanges                            (8)
internal/sessionreference: — internal/state: — internal/store: —
internal/shell: automation, brand, design, globalhotkey, i18n, icons, modelcatalog,
                presets, runtime, session, sessionreference, state, store, tools,
                updater, winproc                                    (16)
internal/skills: runtime
internal/tools: agentteam, automation, lsp, mcp, memory, runtime, skills, store,
                tools/winenc, winproc                               (10)
internal/tools/winenc: — internal/updater: — internal/websearch: —
internal/winproc: — internal/workspacechanges: —
（仅测试边 1 条：runtime 外部测试 → tools）
```

## 附录 C：产物与参考

- 本线探针：`.probe/research/kernel-split/mygo/graph.go`、`collapse.py`、`module_map.json`、`out/`（edges/metrics/ranking/layers/global_state_hits/modules/packages.json）。
- 内核参考（只读）：`/root/go/pkg/mod/github.com/zeokeel/zeokeel-kernel@v0.1.1/module/{module,validate,load,config}.go`、`docs/COMPILE-TIME-REGISTRY.md`、`docs/GO-DESIGN.md`；mygo 线程契约 `/root/go/pkg/mod/github.com/egoist/mygo@v0.7.4/AGENTS.md:32-34`。
- 本仓既有分析（本报告数据独立实测，与其结论交叉印证）：`docs/audit/04-architecture-quality.md:85`(AR-04)、`:117`(AR-08)、`:133`(AR-10)、`:189`(AR-17)；`docs/design/shell-refactor-plan.md:21-92`（AR-04 分批与人日）；`docs/PLAN.md` §8 运行时设计。
- 已知局限：linux 容器只读环境，未执行全量 `go test`/`go build`（仅 `go list` 全量解析与既有 dist 产物尺寸）；LOC 采用「文件物理行数（含空行/注释）」口径、跨平台文件取并集；人日为 [C] 估算，未含联调与真机 UI 验证。
