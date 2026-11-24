# S4 原生 UI 退役：爆炸半径清单（只读预研）

> **已归档（2026-11-24，文档核对轮）**：本文是 S4 动手前的只读预研，S4/S5 已执行完毕，本文件只作证据、
> 不再更新。执行版清单见 [s4-execution-checklist.md](s4-execution-checklist.md)，现状看
> [../ARCHITECTURE.md](../ARCHITECTURE.md) §8。

> 状态：**只读预研**（2026-11-23）。未改任何代码/配置，只写本文件。所有数字用 `wc -l` 实测，所有结论带 `file:line`。
> 目的：把"删 12 万行"变成可执行清单，并把**必须在动手前裁决的点**提前暴露。
> 前置决策（Lead，2026-11-23，已写进 task-6）：
> 1. 托盘**不做独立产品**：`--product serve` 起网关（goroutine）+ 主线程跑 mygo `App` 只承载托盘/热键/通知，无窗口/无 uiroot/无 assembly UI；加 `--no-tray`。
> 2. `desktop` 产品**直接删**，不做别名；`--product desktop` 给明确错误指向 `serve`。
> 3. `go.mod` **保留** mygo，边界写死。**已裁决（Lead, 2026-11-23）**：允许清单为
>    `internal/host`、`internal/lib/openpath`、`internal/lib/openurl`、`internal/brand`、
>    `internal/design`（本清单 §3.2 的发现被采纳）；并且边界测试写成**两条独立断言**：
>    (a) mygo 只出现在上述 5 个包；(b) `internal/gateway`、`internal/contracts`、
>    `internal/modules/**` 对 mygo 的传递依赖为 0。两条都要"证明会咬人"。
> 4. `internal/design`、`internal/brand`、`internal/icons` **保留**（web 端 `gen:tokens` / `gen:icons` 从它们生成）。

---

> **执行版清单已拆出**：[s4-execution-checklist.md](s4-execution-checklist.md) —— 本文件是"预研与分析"，那份是"照单作业"（每条含 文件:行 → 改成什么 → 怎么验证 → 回退点，按依赖排序）。

## 0. 结论速览

| 项 | 实测值 |
|---|---|
| 内嵌 UI 三包删除量 | **54,474** 行非测试 + **75,689** 行测试 = 130,163 行 |
| 随之删除的配套 | devtools `ui-snapshot` 2,767、`ui-drive` 7,183、`designlint` 428；`internal/design/conform` 1,412(+591 测试)；`internal/regression` 17,266（测试）；`audit/demo` + `audit/probes/{ui,ext}` 探针 |
| 需改造的引用方（非删除即报错） | **11 处**（§1.2）：assembly×3、host×3、modules×2、design×1、tray 家族 6 文件、CI/脚本 4 处 |
| `go.mod` 能否去掉 mygo | **不能**（§3）。托盘壳只依赖 `mygo.App`，但 `internal/brand`/`internal/design` 依赖 `mygo/ui` —— 允许清单必须扩 |
| 最可能"删到一半必须回退" | ① brand/design 的 `mygo/ui` ② 同进程托盘在无显示环境 ③ `internal/regression` 整包消失（见 §5） |

---

## 1. 引用清单

### 1.1 删除即消失（包本身属于 UI 侧，无外部语义）

| 包 / 目录 | 文件数 | 非测试行 | 测试行 | 说明 |
|---|---|---|---|---|
| `internal/ui/approval` | 1 / 4 | 312 | 1,041 | |
| `internal/ui/automation` | 3 / 11 | 1,676 | 1,861 | |
| `internal/ui/home` | 2 / 11 | 1,398 | 1,780 | |
| `internal/ui/insights` | 2 / 3 | 1,059 | 995 | |
| `internal/ui/interaction` | 1 / 2 | 527 | 1,275 | |
| `internal/ui/plugins` | 13 / 19 | 5,032 | 3,752 | |
| `internal/ui/rightpanel` | 5 / 18 | 2,682 | 3,119 | |
| `internal/ui/session` | 16 / 112 | 15,371 | 23,277 | 最大模块 |
| `internal/ui/settings` | 27 / 51 | 9,050 | 9,411 | |
| `internal/ui/sidebar` | 3 / 20 | 2,065 | 4,197 | |
| `internal/ui/toasts` | 1 / 4 | 887 | 1,390 | |
| `internal/ui/toolcards` | 1 / 3 | 474 | 734 | |
| `internal/ui/welcome` | 1 / 1 | 115 | 112 | |
| `internal/ui/uimodtest` | 0 / 29 | 0 | 5,851 | 测试专用 harness |
| `internal/uiroot` | 15 / 39 | 5,563 | 8,646 | 壳层 |
| `internal/uikit` | 30 / 30 | 8,263 | 8,248 | **import `internal/ui`（29 处）**，三包必须一起删 |
| `internal/devtools/ui-snapshot` | 1 | 2,767 | 0 | PNG 快照 |
| `internal/devtools/ui-drive` | 16 | 7,183 | 0 | 交互驱动 |
| `internal/devtools/designlint` | 1 | 428 | 0 | DS-01/03/06/07/10 AST 规则 |
| `internal/design/conform` | 4 / 1 | 1,412 | 591 | 设计一致性扫描器 |
| `internal/regression` | 0 / 71 | 0 | 17,266 | 单一 Go 包，见 §5③ |

> 三包互相依赖：`internal/uikit` → `internal/ui`（29 个文件）、`internal/ui/*` → `internal/uiroot`（`session` 25 个文件等）。**不能逐个删**，必须作为一组。

### 1.2 需要改造的引用方（删之前必须先改）

| # | 位置 | 现状（file:line） | 处置 |
|---|---|---|---|
| 1 | `internal/assembly/registry.go` | `:24-36` 13 个 `internal/ui/*` import；`:78-85` 对应 `mustRegister`（home/interaction/toasts/welcome/toolcards/sidebar/session/settings 等） | 删 import + 那批 `mustRegister`（UI 模块不再注册）；`:86-88` 的 approval/automationui/insightsui 同样删 |
| 2 | `internal/assembly/assembly.go` | `:33` `ProductDesktop`；`:207-208` `usesUIServices`/`declareUIServices` 分支；`:215-231` 声明 5 个 UI 服务 | 删常量与两函数；`registryFor` 只返回 `Registry()` |
| 3 | `internal/assembly/boundary_test.go` | S0 写的 `uiModuleAllowed`/`uiroot` 分支（`:96-100`、`:139`） | 改写成 §3 的 mygo 允许清单断言 |
| 4 | `internal/host/host.go` | `:407-832` `runDesktop`（窗口+事件循环）；`:461` `uiroot.New`；`:820` `mygo.App.Run()`；`:834-882` `provideServices(...uiRoot)`；`:893` `persistPreferences(uiRoot)` | 见 §2.1 |
| 5 | `internal/host/update_command.go` | `:23` 命令 ID `"uiroot.install-update"`；`:27` `mygo.App.Quit`；`:38` `registerUpdateInstallCommand(uiRoot *uiroot.Root,...)` | 见 §2.2 |
| 6 | `internal/modules/approvals/approvals.go` | `:361,373,393` `uikit.Language(...)` | 唯一"后端→UI 层"实依赖：把 `Language` 移到 `contracts`（或就地内联 5 行），再删 uikit |
| 7 | `internal/modules/hosttools/{bridges_test.go,goal_dsh_test.go}` | 测试 import `internal/uiroot`（`:…`） | 改为 fake/直接构造；或随 uikit 删除一起改 |
| 8 | `internal/host/{host_test,interaction_test,notifier_test,update_command_test}.go` | 测试 import `internal/uiroot` | 改写：headless 路径已由 gateway 覆盖，删掉 UI 断言 |
| 9 | `internal/design/conformance_test.go` | import `internal/design/conform` | 随 conform 删除 |
| 10 | `internal/host/{tray,hotkey,notify,notification_click,close_guard,update_helper}.go` | 用 `*mygo.Window` 做"显示/聚焦窗口"（tray.go 5 处、hotkey.go 4、notify.go 5） | 换成"打开浏览器 URL"（见 §2.3） |
| 11 | `Makefile` / `scripts/verify.sh` / `.github/workflows/ci.yml` / `scripts/design-check.sh` | 见 §4 | 删/改 |

---

## 2. host 与 assembly 改造点

### 2.1 `host.go`：`runDesktop` 整段消失，`,serve` 变成"网关 + 托盘"

| 删除 | 位置 | 替换成 |
|---|---|---|
| `runDesktop`（~425 行：窗口创建、`mygo.App.SetName`、`RequestSingleInstanceLock` 的 GUI 握手、`window.OnClose`、`persistPreferences`） | `:407-832` | 删除。`Run()` 的 `case assembly.ProductDesktop` 改为返回错误："desktop 产品已移除，请用 `--product serve`" |
| `uiRoot := uiroot.New(...)` | `:461` | 删除（网关不建 uiroot） |
| `provideServices(root, dataDir, settings, logger, uiRoot)` 的 uiRoot 分支（12 个键） | `:834-882` | 签名去掉 uiRoot 参数，只 provide DataDir/Settings/Logger；另 5 个键由 `gateway.Provide` 提供（已实现） |
| `mygo.App.Run()` 在 `runDesktop` 内的调用 | `:820` | 移到 `runServe`：**先**在 goroutine 里 `gw.Serve(ctx)`，**再**在主 goroutine 调 `mygo.App.Run()`（mygo 要求主 goroutine：`.cache/.../mygo@v0.9.2/app.go:113-118`） |

新的 `runServe` 形状（已在 `internal/host/serve.go` 基础上加托盘）：

```
serveCtx, cancel := context.WithCancel(ctx)
go func(){ err := gw.Serve(serveCtx); cancel() }()   // 先起网关
if trayEnabled {
    mygo.App.WhenReady(func(){ tray, err = setupTray(openClientURL, settings, keepAlive) })  // 失败只告警
    if err := mygo.App.Run(); err != nil { log("tray unavailable: %v", err) }  // 无显示环境：不杀网关
}
<-ctx.Done()
```
- `quit`（托盘菜单）→ `cancel()` + `mygo.App.Quit()`；`open`（托盘点击/热键/通知点击）→ `openBrowser(gateway.json URL)`（`internal/lib/openurl` 已有该能力）。
- `--no-tray`：完全跳过 mygo（不 `Run`、不 import 任何 mygo 调用路径），纯网关。
- **双保险（已裁决）**：除 `--no-tray` 外，Linux 下若 `DISPLAY` 与 `WAYLAND_DISPLAY`
  都不存在则**静默跳过托盘**（不尝试、不报警）；CI/冒烟仍显式传 `--no-tray`，不依赖
  探测。这样"无头机器上 `serve` 起不来"从根上不可能发生。
- 生命周期：网关先起、托盘后起；`App.Run` 失败 → 记一条 warning，进程继续（`mygo: cannot open display`，`.cache/.../mygo@v0.9.2/internal/linux/app.go:141`）。

### 2.2 `update_command.go`

| 删除 | 位置 | 替换 |
|---|---|---|
| `import uiroot` + `registerUpdateInstallCommand(uiRoot *uiroot.Root, …)` | `:38` 起 | 命令改为不经 UI runtime 注册：更新安装命令**不再进命令面板**（面板已不存在）；只保留 `applyPendingUpdateOnStartup`（host 启动路径）与托盘菜单里的"立即安装更新"动作 |
| 命令 ID `"uiroot.install-update"` | `:23` | 删除（无消费者）或改 `"host.install-update"`（如托盘要读） |
| `quitApplication = func(){ mygo.App.Quit() }` | `:27` | 保留（托盘退出用它），但调用方变成托盘 `quit` 动作 |

### 2.3 托盘家族：窗口参数 → 打开客户端

`tray.go` 已有 `trayWindow` 接口（`show/focus`，`:113-174`），`hotkey.go`/`notify.go`/`notification_click.go` 拿 `*mygo.Window` 做同样的事。替换为单个回调：

```go
type clientOpener interface{ OpenClient() error }   // 读 gateway.json → openurl(URL)
```
- `setupTray(window, …)` → `setupTray(opener, …)`（`tray.go:104,113,124,148,168,175` 五个签名）
- `hotkey.go:…`：热键 → `opener.OpenClient()`
- `notify.go:…`：通知点击 → `opener.OpenClient()`
- `close_guard.go`（close-to-tray 语义）**整个删除**：无窗口可关，`contracts.SettingCloseToTray` 的桌面语义消失（设置页对应字段由 web 端标注"仅桌面端"或删除，跨轨由 web owner 决定）
- `activity.go` 不含该文件；`activity_test.go` 用窗口，随 `runDesktop` 测试删除

---

## 3. `go.mod` 与 mygo

**结论：`go.mod` 必须保留 mygo。** 但**允许清单不能只有 `internal/host` + `lib/{openpath,openurl}`**。

### 3.1 托盘壳确实只需要 `mygo.App`（不需要 uiroot）

只用 mygo、不碰 uiroot 的 host 文件：`tray.go`、`hotkey.go`、`notify.go`、`notification_click.go`、`close_guard.go`、`update_helper.go`（+ 对应测试）。API：`mygo.App`（`Run`/`Quit`/`WhenReady`/`SetName`/`SetVersion`，`app.go`）、`mygo.NewTray`/`TrayOptions`（`modules.go:872-905`）、`mygo.Menu`、通知与热键。→ 托盘不依赖 uiroot 成立。

### 3.2 真实发现：`internal/brand` 与 `internal/design` import `mygo/ui`

| 文件 | 行 | 用途 |
|---|---|---|
| `internal/brand/brand.go` | `:3` | `ui` 颜色/字体类型 |
| `internal/brand/mark.go` | `:8` | logo 绘制 |
| `internal/brand/palette.go` | `:16` | 29 个 `ui.Color` 字段 |
| `internal/design/tokens.go` | `:14` | 24 个语义 token（`ui.Color`） |
| `internal/design/components.go` | `:4` | 度量（`ui` 类型） |
| `internal/design/font.go` | `:4` | 字号 |

而这两个包**必须保留**（web 端 `npm run gen:tokens` 直接解析它们，见 `web/scripts/gen-tokens.mjs:11-22`）。若按原允许清单，`go` 侧的边界断言会立刻红。

**两条路（本清单建议 A）：**
- **A（建议，S4 内做）**：把 mygo 允许清单扩为
  `internal/host`、`internal/lib/openpath`、`internal/lib/openurl`、**`internal/brand`**、**`internal/design`**；
  理由：它们是**资产层**，web 只读其数据；扩清单是 0 风险的边界文字修改。
- **B（另开任务，不在 S4）**：把 `ui.Color` 换成本地类型，但 `web/scripts/gen-tokens.mjs:64-72` 用正则硬匹配 `ui.Hex(HexX)`、`ui.Hex("#rrggbb")`、`ui.RGBA(r,g,b,a)` —— 必须与 web 轨**同一次提交**改 Go 源 + Node 解析器，否则 `gen:tokens` 直接非零退出（该脚本自称 strict）。**不要在 S4 里做这件事。**

### 3.3 CI 如何断言

改写 S0 的两条测试（`internal/assembly/boundary_test.go`）：
1. `TestContractsDependsOnNoEngine` 保留（contracts 依赖图无 mygo）。
2. `TestNativeUIModelIsQuarantined` → `TestEngineIsQuarantinedToTheShell`：`go list -deps`/imports 全仓断言 **mygo 的直接 importer ⊆ §3.2-A 的五个包**；并保留"临时探针证明会咬人"的验证（S0 已用过该手法）。
3. 新增正向断言：`internal/gateway`、`internal/contracts`、`internal/modules/**`、`internal/lib/**`（除 openpath/openurl）依赖图 mygo 计数 = 0。

---

## 4. CI / 脚本 / 文档

### 4.1 直接删除

| 目标 | 位置 |
|---|---|
| `designlint` + `design-check` + `go tool mygo vet` 三步 | `.github/workflows/ci.yml:49-53`；`scripts/verify.sh:161-172, 296-298`；`Makefile: design-check, design-baseline` |
| `-race ./internal/ui/... ./internal/uiroot` | `ci.yml:85` → 改为 `./internal/gateway ./internal/modules/... ./internal/host` |
| `check_regression`、`check_snapshot` 两步 | `scripts/verify.sh:175-206, 305-306` |
| `scripts/design-check.sh`、`.design-baseline.json`（3 个顶层键） | 归档到 `docs/archive/` 或删除 |
| `internal/devtools/{designlint,ui-snapshot,ui-drive}`、`internal/design/conform`、`internal/design/conformance_test.go` | 包删除 |
| `audit/demo`（非测试包，import uiroot）、`audit/probes/{ui,ext}` 探针测试 | 删除（`audit/upstream/*` 有自己的 go.mod，不在 `./...`） |

### 4.2 保留 / 改

| 项 | 处置 |
|---|---|
| `make build / package / release / icons / web-client*` | 保留；`icons` 与 web 的 `gen:icons` 不冲突（前者生成 Go 侧图标资产）；`package`/`release` 不变（仍打单二进制） |
| `go tool mygo vet .` | 保留（托盘仍用 mygo 框架），但要确认在只剩托盘时仍通过；若报 UI 生命周期规则无关噪声，改为只对 `internal/host` 生效 |
| `mygo.json` | 保留（打包清单：窗口图标/单实例等字段若只服务窗口，按 mygo 文档裁剪；托盘 logo 仍来自 `internal/brand`） |
| `scripts/build-web-client.sh` + `web-client-fresh` | 保留（S1b 新增） |
| `internal/design`、`internal/brand`、`internal/icons`、`internal/design/registry.json` | **保留**（web `gen:tokens` / `gen:icons` 依赖；registry.json 是前端组件清单规格源） |
| `AGENTS.md` §0/§1/§2/§3/§5/§6/§7/§9 | 改写为 Web 轨口径（§3 的 17 条设计规则全部只对托盘壳适用或归档） |
| `docs/ARCHITECTURE.md` | 增"传输层"章节；删除 uiroot/UI 模块的描述 |
| `docs/DESIGN.md` | 标注 mygo 部分仅托盘壳适用 |
| 归档（保留文件、加"已归档"头） | `INJECTION-POINTS.md`、`UI-CONFORMANCE.md`、`UI-COMPONENT-MIGRATION.md`、`DESIGN-LINT.md`、`UI-FIX-STATUS-v0.8.3.md`、`AGENT-SURFACES.md`、`COVERAGE.md`、`VERIFICATION.md`、`UI-AUDIT-2026-11-11.md`、`DESIGN-REVIEW.md`、`UI-STYLE-DIRECTION-2026-11-11.md`、`UI-UX-UPGRADE-2026-11-15.md`、`SETTINGS-IA-REVIEW-2026-11-16.md`、`USABILITY-REVIEW.md`、`INTERACTION-DEPTH-ROUND.md`、`PRODUCT-BUG-REVIEW-2026-11-11.md`、`ADVERSARIAL-AUDIT.md`（共 21 个 md 引用 `internal/ui*`；其中 `TRANSPORT.md`/`WEB-CLIENT.md` 属现行 Web 轨文档，只需改描述不需归档） |

---

## 5. 删除顺序 + 三个最可能回退的点

### 5.1 建议顺序（每步结束时 `go build ./... && go vet ./... && go test ./...` 必须是绿的）

1. **先加托盘、后删任何东西**（可逆、可测）：`runServe` 里加 `--no-tray`/托盘路径；托盘开会话只依赖 `gateway.json` + `openurl`。此时 desktop 仍在，回退=删这几个文件。
2. **拆 host 的桌面耦合**：删 `runDesktop`、`provideServices` 的 uiRoot 分支、`close_guard`、`persistPreferences`；`update_command.go` 去掉 uiroot。仍保留 `internal/ui` 包（只是没人注册它）。
3. **拆 assembly**：`registry.go` 去 UI 模块、删 `ProductDesktop`、删 `usesUIServices`/`declareUIServices`；更新 `assembly_test.go` 期望；`--product desktop` 变成明确错误。
4. **删包**：`internal/ui` + `internal/uikit` + `internal/uiroot`（一组）、`internal/devtools/{designlint,ui-snapshot,ui-drive}`、`internal/design/conform` + `conformance_test.go`、`internal/regression`、`audit/demo` + `audit/probes/{ui,ext}`。
5. **收口**：改写边界测试（§3.3）、Makefile/verify.sh/ci.yml、文档与归档；跑交叉编译 + 产品 smoke + web 客户端同源验证。

### 5.2 三个最可能"删到一半必须回退"的点

**① `internal/brand` / `internal/design` 依赖 `mygo/ui`（§3.2）**
如果先按原允许清单写边界断言再删包，树会立刻红，而且修法要么扩清单（要 Lead 认可）、要么与 web 轨同步改 `gen-tokens.mjs`（跨轨）。**裁决点必须在删除之前**：建议扩清单（A）。

**② 同进程托盘在无显示环境（CI/服务器）——已定双保险缓解（见 §2.1）**
`mygo.App.Run()` → GTK `gtk_init_check` 失败 → `mygo: cannot open display`。若实现顺序写成"先 `App.Run()` 再起网关"，无头环境直接启动失败（CI 的 `--list-products`/smoke、以及任何 `serve` 冒烟都会挂）。**缓解**：网关先起、托盘后起、`App.Run` 失败只告警；CI/冒烟一律 `--no-tray`。顺序写反 = 必须回退。

**③ `internal/regression` 是**一个** Go 包（71 文件 / 17,266 测试行）**
它整体 import `internal/ui/uiroot`（31 个文件直接用，其余靠同包 helper），所以删 UI 等于删掉这 17k 行**行为回归**——其中包含 ACP、agent、approvals、automation 等**并非 UI 专属**的端到端断言。`scripts/verify.sh check_regression`、CI 也围着它建。若无替代，删除后"行为等价"的证据链会断，S4 之后任何后端回归都没有自动化兜底。**缓解（建议 S4 前一个任务）**：把 regression 里非 UI 的子集（acp/agent/approvals/automation/context…）迁到 `internal/gateway` 或按 gateway 协议重写成端到端测试；否则这一步是无法回滚的覆盖率损失。

> 次一级工作项（可做但不构成回退风险）：`internal/modules/approvals/approvals.go:361,373,393` 的 `uikit.Language` 需要先迁走；`internal/modules/hosttools` 两个测试文件的 uiroot import。

---

## 附录 A：证据索引

| 结论 | 证据 |
|---|---|
| mygo `App.Run` 必须主 goroutine 且阻塞 | `.cache/gopath/pkg/mod/github.com/egoist/mygo@v0.9.2/app.go:113-118` |
| `NewTray` 必须在 ready 后（`App.WhenReady`） | 同目录 `modules.go:905-910`（`NewTray` 注释与 `panic`/`waitReady`） |
| 无显示环境 Init 失败信息 | 同目录 `internal/linux/app.go:141` |
| 网关已发布端口/token | `internal/gateway/port.go`（`Info.URL()`、`writeInfo`）、`internal/gateway/gateway.go`（`Serve` 打印 URL） |
| 网关已有 `POST /quit` | `internal/gateway/server.go`（`handleQuit`） |
| web token 生成器只读 Go 源码文本 | `web/scripts/gen-tokens.mjs:11-22`，正则在 `:64-72` |
| web 图标生成器只读 SVG | `web/scripts/gen-icons.mjs:18` |
| approvals 的 uikit 依赖 | `internal/modules/approvals/approvals.go:361,373,393` |
| 三包互相依赖 | `internal/uikit` → `internal/ui`（29 文件）；`internal/ui/*` → `internal/uiroot`（`session` 25 文件等） |
| CI 三步设计检查 | `.github/workflows/ci.yml:49-53`；`scripts/verify.sh:161-172, 296-306` |
| desktop 产品与测试期望 | `internal/assembly/assembly.go:33`、`assembly_test.go:123,138,217,284,469,643` |
| registry UI 注册 | `internal/assembly/registry.go:24-36, 86-98` |
| host 窗口/装配锚点 | `internal/host/host.go:407,461,820,834,893`；`update_command.go:23,27,38` |