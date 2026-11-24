# S4 执行清单（原生 UI 退役 · 照单作业）

> **已归档（2026-11-24，文档核对轮）**：S4/S5 的退役工作已全部执行完毕，本清单是**执行记录与证据**，
> 不再更新。现状请看 [../ARCHITECTURE.md](../ARCHITECTURE.md) §8 与根 [AGENTS.md](../../AGENTS.md)；
> 活文档索引见 [README.md](README.md)。

> 由 [s4-retirement-inventory.md](s4-retirement-inventory.md)（只读预研）拆解而来。**分析已做完，S4 只剩执行**：每条给出「改哪个文件 / 哪几行 / 改成什么 / 怎么验证」，按依赖顺序编号，逐步可提交、可回退。
> 行号以 **2026-11-23 HEAD `59bd7f4`** 为准；动手前用每条的 `grep` 复核一次行号（本文件不含"大概位置"）。
>
> **勾选规则（2026-11-23 复盘后加）**：每条勾选**必须指向一个可验证的对象**——提交哈希、测试名、
> 或命令输出；**不能指向意图**。复盘发现 S3.5 曾勾引用了一个不存在的提交，根因正是"我记得做了"
> 而不是"我贴了输出"——与 S5 那次"验了工作树没验提交"同源。更正记录保留在对应条目下，不改原文。
> 前置裁决（已定，不再讨论）：① 托盘与网关同进程、desktop 直接删；② `go.mod` 保留 mygo，允许清单 = `internal/host` + `internal/lib/{openpath,openurl}` + `internal/brand` + `internal/design`；③ `internal/{design,brand,icons}` 保留（web `gen:tokens`/`gen:icons` 依赖）；④ 无头托盘双保险（`--no-tray` + `DISPLAY` 探针）；⑤ regression 的后端覆盖已迁出（15 条 `TestE2E*`，见 `regression-migration.md`）。

## 0. 开工门（Gate）

- [ ] **G1** Lead 在真网关跑通浏览器端到端：新建会话 → 发一轮 → 审批停 → 允许 → 流式完成 → 刷新恢复。
- [ ] **G2** 两个提交点已落库（拆法已定）：`(a) internal/** + docs/** + scripts/** + Makefile`、`(b) web/**`。
- [ ] **G3** `git tag pre-s4` 打在 (a)(b) 之后；S4 每一步一个提交，随时 `git reset --hard pre-s4`。
- [ ] **G4** 单独一个提交删除 75 个 `internal/modules/agent/memory/memory/*/memory.db`（3.1MB 误提交的运行时产物，Lead 已判定；**不与 S4 混提**）。
- [ ] **G5** 基线全绿：`go build ./... && go vet ./... && go test ./... -count=1`（唯一已知红：`internal/lib/hostbundle` 在本沙箱因 `$HOME` 只读失败，属环境，记录在案）。

## 1. 步骤 S1：托盘自足（**additive，删任何东西之前**）

> 目的：让 `serve` 在"无窗口、无 uiroot、无 UI 模块"的世界里已经能跑起来，再动删除。这一步结束时 `internal/ui` 仍然在场（只是没人注册它），所以回退 = 撤这一步的几个文件。

- [x] **S1.1** `internal/host/serve.go`：起服务顺序改成 **先网关、后托盘**。
  - 现：`gateway.New/Provide` → `LoadEntriesWithBuildInfo` → `gw.Serve(ctx)`（阻塞到 ctx 结束）。
  - 改：`serveCtx, cancel := context.WithCancel(ctx)`；`go func(){ err := gw.Serve(serveCtx); cancel(); … }()`；随后进托盘分支；末尾 `<-ctx.Done()`。
  - 验证：`go run ./cmd/zeokeel --product serve --no-tray` 打印 URL 后可 `POST /quit` 正常退出。
- [x] **S1.2** `internal/host/serve.go`：新增 `trayEnabled` 判定，双保险。
  - `--no-tray` 直接 false；否则 Linux 下 `os.Getenv("DISPLAY")=="" && os.Getenv("WAYLAND_DISPLAY")==""` → false（**静默跳过，不告警**）；其余平台 true。
  - 验证：`env -u DISPLAY -u WAYLAND_DISPLAY go run ./cmd/zeokeel --product serve` 正常起、无托盘日志、无报错；带 `DISPLAY=:0` 时有托盘尝试日志。
- [x] **S1.3** 新增 `internal/host/tray_shell.go`（新文件，约 120 行）：`setupTrayShell(opts, settings, logger, openClient func() error, quit func())`。
  - `mygo.App.WhenReady(func(){ tray, err = setupTrayWithActions(...) }); mygo.App.Run()`；`Run` 返回错误 → **只记 warning**，不杀网关（`mygo: cannot open display` 属于预期，见 `mygo@v0.9.2/internal/linux/app.go:141`）。
  - `openClient`：读 `<data-dir>/gateway.json` 的 `port`+`token` → `openurl` 打开 `http://127.0.0.1:<port>/#token=<T>`。
  - `quit`：`cancel()` + `mygo.App.Quit()`（复用 `internal/host/update_command.go:27` 的 `quitApplication` 语义）。
  - 验证：`go test ./internal/host -run 'Tray'`；`make acceptance` 起服务后点托盘图标能打开浏览器。
- [x] **S1.4–S1.6（改法调整，见提交说明）** 没有改 `trayWindow` 的签名，而是新增 `BrowserWindow` 适配器**实现**该接口（`Show/Restore/Focus` → 打开浏览器 URL；`Hide` 空操作；`OnClose` 返回空注销）。于是 `tray.go` / `hotkey.go` / `notify.go` / `notification_click.go` 保持单一代码路径，零签名改动、零测试重写。
  - 理由：把"窗口"抽象成接口的初衷就是这个——换个实现即可。原计划的签名级替换会牵动 4 个文件 + 6 个测试 fake，收益为零而风险更高。
  - `close_guard.go` **移到 S2 删除**：它只被 `runDesktop` 与 tray 的 close 监听使用，S2 删 `runDesktop` 后自然成为死代码。
  - 验证：`go test ./internal/host -run 'Tray|Hotkey|Notify'` 全绿。
- [x] **S1.5 + S1.6（合并；已被上面 S1.4–S1.6 的"改法调整"覆盖）** 原计划：把
  `internal/host/{hotkey.go,notify.go,notification_click.go}` 的 `*mygo.Window` 换成
  `clientOpener`，并删 `close_guard.go`。**实际**：签名没改，改用 `BrowserWindow` 适配器实现
  `hotkeyWindow`（`Show/Restore/Focus` → 打开浏览器 URL），行为等价；`close_guard.go` 在 S2
  随 `runDesktop` 一起删除。
  - **（更正 2026-11-23）** 这两条曾被记成"未勾"却又被 line 31 记为"已由调整覆盖"——同一件事
    两处记账。这里合并成一行，避免读者以为还欠工。
  - 验证：`go test ./internal/host -run 'Tray|Hotkey|Notify'` 全绿。
  - 连带的**第二处**读取点在 `internal/host/tray.go:81-88 closeToTrayEnabled` + 它喂给 `setupTray(…, closeToTray bool)` 的菜单文案：新世界里没有窗口可关，去掉该参数与 `contracts.SettingCloseToTray` 的读取。
  - `contracts.SettingCloseToTray` 常量本身保留在 contracts（契约不删类型），设置页那一行由 **web 轨**标注「仅桌面端」或移除（跨轨事项，登记后通知）。
  - 验证：`go build ./...`；`grep -rn "CloseToTray" --include=*.go internal | grep -v internal/ui` 只剩 `internal/contracts/interaction.go`。
- [x] **S1.7** 提交 `S1: serve runs the gateway before the tray; tray opens the browser`。
  - 回退点检查：`git revert <S1>` 后 `go build ./...` 应恢复原状（此时 UI 包从未被动过，必然通过）。


### S9 收口记录（2026-11-23）

**验收命令**（在 `d484644` 上真跑）

| 命令 | 结果 |
|---|---|
| `gofmt -l internal cmd` | 空 |
| `go build ./internal/... ./cmd/...` | OK |
| `go vet ./internal/... ./cmd/...` | OK |
| `go test ./internal/... ./cmd/... -count=1` | 1467 个顶层测试；**34 个包全 ok，非 ok 包数 = 0**（hostbundle 已由 `8873f41` 修复，见下） |
| `go list -deps ./internal/{contracts,gateway,modules/...} \| grep -c egoist/mygo` | 0 / 0 / 0 |
| `./zeokeel --list-products` | `headless`、`serve` |
| `./zeokeel --product desktop` | exit=1，提示改用 serve |
| `serve --no-tray --port 9967` | `GET /`=200、`/settings`=200、`/api/ws`（无 token）=401 |
| `make acceptance-check / -approval / -slow` | 三条 PASSED（plain / approval / slow） |
| 交叉编译 linux/arm64·darwin/amd64·darwin/arm64·windows/amd64（CGO_ENABLED=0） | 四个目标全部 OK |
| 泄漏检查（ui/uiroot/uikit 引用、`zeokeel-ui/` id、`ProductDesktop`、`desktop.json`、`memory.db`） | 产品代码 0 处（仅 `assembly_test.go` 里有意断言"已不存在"的字符串；`docs/research` 属历史调研，不动） |
| CI `-race`（21 包） | 0 FAIL |
| headless / ACP smoke | 正常（165/ACP 帧含 protocolVersion:1） |

**净变化**：`pre-s4..HEAD` 649 文件 **+2,231 / −163,300**；`web/` 17 文件 +867/−273，
Go+docs 632 文件 +1,364/−163,027。

**测试规模（迁移后剩下的保护网）**

| 层 | 规模 |
|---|---|
| 顶层测试总数 | **1,467**（`internal/**` + `cmd/**`） |
| `internal/modules/agent` | 482 |
| `internal/modules/toolkit` | 260 |
| `internal/host` | 132 |
| `internal/gateway` | **69**，其中 **15 条 `TestE2E*`**（run 生命周期/取消/断连存活/用量/审批 allow·deny·非法 verdict/plan mode/plan review 全链/队列排空·移除/automation/模型覆盖/ACP 两轮） |
| `internal/contracts` | 57 |
| `internal/assembly` | 17（含 5 个边界断言函数） |
| 协议级端到端 | 3 条 acceptance（真 WS + 真 SSE，fakemodel + gwprobe） |
| 测试代码量 | 269 文件 / 66,415 行（pre-s4 为 701 文件；删除的 10.4 万行测试：ui 59,083 + uiroot 8,685 + uikit 8,278 + regression 17,337 + conform 592） |

**已知验收盲区（S9 之后发现，补记；不改历史勾选）**
`settings.set` 的能力条目缺失让设置页在真网关下**永远只读**（客户端 `hasCapability('settings.set')`
精确匹配，服务端只报 `settings`），而 **24 条 P0 验收里没有一条走"改一项设置并保存"的端到端**：
B1（设置脏守卫）只在 jsdom + fake 里验过，而 `web/src/test/fake-gateway.ts:99` **手写了**
`'settings.set'`——替身有、真网关没有，两边各自自洽。所以这条 bug 能躲过 171 条测试＋全部人工验收。
修复：`2248be9` 在静态能力表补 `settings.set`（并补 `approvals`）；根治见 task-17（能力清单与参数
契约从 Go 侧生成，**禁止任何替身手写 capabilities**）。
**盲区本身要记住**：设置写路径目前仍无真环境端到端，下一次动设置相关的服务端能力时补一条。

**覆盖净变化（必须记录的损失）**
`internal/modules/hosttools/bridges_test.go` 已删除：它断言的是 **uiroot 实现**的
artifact/plan 桥（present 经宿主桥到渲染器、plan_review 状态机、interactive=false 时
保留 artifact 桥隐藏 plan 工具），uiroot 删除后无法原地保留，造假实现等于"测自己的假件"。
等价覆盖：`gateway/services_test.go`（artifact/plan/ask 服务本体）+
`TestE2EPlanReviewOverTheProtocol` + `TestE2EApprovalAllowRunsTheTool`。
**净损失**：Go 侧不再覆盖"渲染侧"（谁把 artifact 画出来）——那现在归 web 端测试。

**hostbundle：曾是环境性失败，已修，现全量全绿**
`internal/lib/hostbundle` 的 `TestHostBundleCompilesIntoKit` 曾经在本沙箱恒红
（`sqlite: pragma: unable to open database file (14)`，因为 kit 在用户 state 目录开库，
而沙箱 `$HOME` 只读）。它**不是环境宿命，是测试卫生问题**：让它自带 HOME 沙盒即可
（`t.Setenv("HOME"/"XDG_*", t.TempDir())`），已在 `8873f41` 修复。
理由写进了那条提交：**一个在某个环境里永久红的测试，会教人忽略红**——与"永远空过的
检查"是同一个失效模式。修复后 `go test ./internal/... ./cmd/...` 非 ok 包数 = **0**。

<!-- npm-scripts:ignore-begin — 本段是在*记录*检查器抓到的旧命令，不是指令 -->
**文档↔脚本一致性断言（web 轨，`ba12a38`）**
`npm run e2e` 一度只存在于 `AGENTS.md` 而没注册进 `web/package.json`（第 7 次"文档比现实
宽松"）。web 轨补了脚本，并加了一条机器断言：从 `AGENTS.md`/`docs/**` 抓 `npm run <x>`，
断言每个 `x` 都存在于 `package.json`。它一上线就抓出**两处真实漂移**（`npm run design-check`、
`npm run tokens` 都是原生期命令），且修法是**改文档而不是加豁免**——豁免机制存在但不能当
创可贴。另一个细节值得记：他第一版正则把 `npm run e2e` 截成了 `npm run e`，于是把 fixture
断言改成精确列表。**检查器本身也会错**，这正是每条断言都必须"证明会咬人"、并且要能自校验
的原因。
<!-- npm-scripts:ignore-end -->

**`go tool mygo vet` 移除说明**
S5 之后产品里已无 `ui.Element` 帧循环（托盘只有 App + Tray + 菜单），该工具在当前树上
`exit=0`、零输出——**空过**，比没有检查更糟。`go.mod` 的 tool 指令保留。**恢复条件**：
托盘壳若重新长出 element/部件代码（多窗口、自定义托盘弹层），把它加回 `Makefile vet`
与 `ci.yml`，并对 `internal/host` 跑一次确认非空过。

### S8 执行记录（2026-11-23）

- `AGENTS.md` 整体重写：§0 改为"迁移已完成"（网关 + web + 托盘壳三块及各自规则），§1 目录与**两档引擎边界**（5 包允许清单 + 后端传递为 0 + test 档单列），§2 必读顺序换成 WEB-CLIENT/TRANSPORT/DESIGN/APPROVAL-SURFACES/ARCHITECTURE，§3 规则改为 web 客户端 + 托盘壳两套，§4 命令改为 `./internal/... ./cmd/...` 并补 `make acceptance-*` 与 web 生成器命令，§5 改为"Web 改动固定流程"，§6 同步义务表重写，§7 硬边界改为引擎隔离/不自造协议/不绕过 api 层，§9 速查表更新。
- **审批面按裁决拆出**：新建 [docs/APPROVAL-SURFACES.md](../APPROVAL-SURFACES.md)（§S4.1 原文 + 机器可读 `approval-exemptions` 块）；`internal/modules/agent/approval_surface_test.go` 的解析路径同步改为该文件（`TestApprovalExemptionsMatchTheGate`、`TestDocumentedParksActuallyPark` 通过）；`AGENT-SURFACES.md` 整体移入本目录（现为 `docs/archive/AGENT-SURFACES.md`）并在顶部指向新文档。
- 归档 16 份 + AGENT-SURFACES = **17 份历史文档**移入 `docs/archive/`，每份加归档头（"本文描述的是…，代码已被 S5 删除，现状请看 …"）；`docs/archive/README.md` 重写为**索引**（工具 2 项 + 文档 17 项 + 活文档清单）。
- `docs/ARCHITECTURE.md`：§1 名词表（gateway/web/域/快照差值 替换 uiroot/槽位）、§2 目录、§3 三层边界（含两档引擎断言）、§5 加"本节属原生 UI，浏览器扩展点是路由+域订阅"、§6 服务键改为网关/host 提供、§7 装配换成 headless.json + serve 覆盖、§8 当前状态整体重写、§9 规则去掉 uiroot。
- `docs/DESIGN.md`：加适用范围头——mygo/帧测试/designlint 部分只对托盘壳有意义，web 视觉事实来自 `gen:tokens`/`gen:icons`，冲突时以 web 现状为准。
- 链接体检：脚本扫描 `docs/**` + `AGENTS.md` + `README.md` 的相对链接，**broken = 0**（发现并修掉一个真问题：`docs/plans/web-client-pivot.md` 从来不存在，引用改为 `web-ui-framework.md` / `web-runtime-adapter.md`）。
- 验证：`go build ./internal/... ./cmd/...` OK；`go test ./internal/{modules/agent,gateway,assembly}` 全绿（含读文档的 `TestApprovalExemptions*` 与 `TestTransport*`）。本步**未改产品代码**，只改文档与一处测试里的路径常量。

### S7 执行记录（2026-11-23）

- `ci.yml`：删掉 "Design contract"（designlint + design-check + `go tool mygo vet`），换成 **Protocol/implementation consistency**（`go test ./internal/gateway -run TestTransport`）；`:85` 的 `-race` 名单改为 `./internal/gateway ./internal/contracts ./internal/modules/... ./internal/host ./internal/assembly`（按 Lead 要求把 contracts 纳入）；Import boundaries 步骤的注释改为两档描述。
- **`go tool mygo vet` 从 CI/Makefile 移除**（Lead 裁决：它在 S5 之后是"空过"——产品里已无 `ui.Element` 帧循环，空过的检查比没有检查更糟）。**`go.mod` 的 tool 指令保留**，将来托盘壳若长出 element 代码可手动跑；S9 报告会写明原因与恢复条件。
- `scripts/verify.sh`：删 `check_designlint`/`check_design_grep`/`check_mygo_vet`/`check_regression`/`check_snapshot` 及其 `run_step`（连同头部注释同步）；**`check_protocol_consistency` 保留**；`check_desktop_smoke`（xvfb + 窗口）改写为 **`check_serve_smoke`**（无显示环境起 `--product serve --no-tray --port 0`，断言打印 `product: serve` 与带 token 的 `gateway: http://127.0.0.1:`）。
- `Makefile`：删 `design-check`/`design-baseline`，`.PHONY` 与 `ci`/`verify` 的注释同步。
- 归档（**不是删除**）：`.design-baseline.json` → `docs/archive/design-baseline.json`，`scripts/design-check.sh` → `docs/archive/design-check.sh`，并新增 `docs/archive/README.md` 说明"为何保留、被什么取代"。
- **真跑 `bash scripts/verify.sh --quick`**（不是 `bash -n`）：gofmt / build / vet / protocol-consistency 四步 PASS，`go test ./...` 仅 `internal/lib/hostbundle` 失败——**环境性**（本沙箱 `$HOME` 只读，`sqlite: unable to open database file`），迁移前在 pristine HEAD 上同样失败。
- 已知小瑕疵：`go test ./...` 会扫到 `web/node_modules/flatted/golang`（第三方 JS 包自带的 Go 文件，只产生一行 `[no test files]`）。不影响任何断言；若要彻底消除需给 Go 命令加 scope，留待需要时处理。

### S6 执行记录（2026-11-23）

- 删除 `internal/contracts/uinative`：**`go list -deps ./internal/contracts | grep -c egoist/mygo` = 0**，S0 那一步到这里才真正收口。
- `boundary_test.go` 重写为两档 + 一条独立直查：
  - `TestEngineIsQuarantinedToTheShell`（直查 import）：允许清单 = `internal/host`、`internal/lib/{openpath,openurl}`、`internal/brand`、`internal/design` —— **正好 5 个，未扩张**；双向断言（清单外的 import → 红；清单里的包不再 import → 也红，防陈旧条目）。
  - `TestBackendIsTransitivelyEngineFree`（传递）：`./internal/gateway`、`./internal/contracts`、`./internal/modules/...` 的 `go list -deps` 里 mygo = 0。
  - `TestContractsDependsOnNoEngine`：保留。
  - `TestTestDependenciesStayEngineFree`（test 档）：`go list -deps -test ./internal/gateway` 必须**含** `internal/modules/*`（e2e 需要真模块，断言它确实在用）且**不含** mygo。
  - `TestImportBoundaries`：保留后端模块/host/contracts 的规则，删掉 ui/uikit/uiroot 分支。
- **新发现（断言当场抓到的）**：`internal/icons` 也 import `mygo/ui`（把 34 个 SVG 解析成 `ui.SVG` 供原生渲染）。它的消费者（ui/uiroot）已随 S5 删除，所以整个 Go API 成了死代码。处理：**把 icons 收敛成"只嵌入 SVG + FS()/Names()/SVG() 访问器"**，去掉 mygo 依赖（web 的 `gen:icons` 仍从 `svg/*.svg` 读盘，34 个文件未动）；测试改为校验网格/描边不变量 + 每个文件可读。结果：5 包允许清单**不需要第 6 项**。
- **证明会咬人**（两次，恢复后复绿）：
  1. 往 `internal/modules/agent` 加一行 `_ "github.com/egoist/mygo/ui"` → `internal/modules/agent imports github.com/egoist/mygo: the native engine is quarantined to [...]` + 传递档报 `./internal/modules/... depends on github.com/egoist/mygo/i18n`。
  2. 从允许清单里删掉 `internal/brand` → `internal/brand imports github.com/egoist/mygo: ... quarantined to [design host openpath openurl]`。
- 验证：gofmt 空 / build / vet OK；assembly、host、gateway(35.7s)、icons、contracts 全绿。

### S5 执行记录（2026-11-23，含并入的 S3）

- 删除（git 跟踪）：`internal/ui`(364) + `internal/uiroot`(54) + `internal/uikit`(60) + `internal/devtools/{designlint,ui-snapshot,ui-drive}`(18) + `internal/design/conform`(5) + `internal/design/conformance_test.go` + `internal/regression`(71) + `internal/modules/hosttools/bridges_test.go` + `internal/assembly/assemblies/desktop.json`。合计 **575 文件 / 160,637 行**。
- 删除（本地、被 `.git/info/exclude` 排除、从不入库）：`audit/demo`、`audit/probes`（`audit/demo/main.go` 与 probes 的 harness 都 import uiroot，S5 后无法编译）。
- 注册与产品面：registry 去掉 13 个 UI import/注册（剩 18 单元 + group）；`declareUIServices`/`usesUIServices`/`uiServiceNames` 删除，`registryFor` 塌缩为 `Registry()`；`ProductDesktop` 常量与 `desktop.json` 删除，`Products()`=`[headless]`、`AllProducts()`=`[headless, serve]`；`--product` 默认 `serve`；`--product desktop` 保留可读错误（`retiredProductDesktop` 字面量）。
- 测试重写：`assembly_test.go` 的 desktop 用例整体改写为 headless（products/entries、load-unwind、team-stub、preflight）；`hosttools` 的 bridge 测试删除（UI 未提供时无法断言 artifact/plan 桥），goal 三件套测试改为自带 loader；`boundary_test.go` 删掉已空洞的 `TestNativeUIModelIsQuarantined`（S6 补两档 mygo 断言）。
- 契约注释更新：`contracts.{go,foreground.go,toastanchor.go}` 里"provided by internal/uiroot"改成"browser transport host (gateway.Provide)"。
- 验证：gofmt 空 / build / vet OK；全量 `go test ./...` 仅 hostbundle 环境性失败；`--list-products` 亲眼验 = `headless`、`serve`；`--product desktop` 退出码 1；`serve --no-tray --port 9965` 后 `GET /` = 200；`make acceptance-check / acceptance-approval / acceptance-slow` 三跳全 PASS。
- **回退语义**：`git revert` 这一个提交即可回到"原生 UI 仍在"的状态；`git reset --hard pre-s4` 回到整个迁移前的干净点。两者都不影响 web 轨（它的提交独立）。

### S4pre 执行记录（2026-11-23）

- `contracts.Language(locale)` 新增（nil → "zh"），`internal/modules/approvals` 的 4 处调用改为 `contracts.Language`，uikit import 删除。
- 副作用检查：`grep -rn "internal/uikit" internal/modules internal/gateway internal/contracts` 为空——**后端已零 UI 层依赖**。
- `internal/uikit/locale.go` 的 `Language` 保留（UI 侧仍在用，S5 一并删除；两处实现里 contracts 版本是唯一真相，UI 版是薄包装）。
- 验证：build/vet 全绿；`go test ./internal/modules/approvals ./internal/contracts` 绿；全量仅 hostbundle 环境性失败。

### S3 执行记录（2026-11-23）

**与 S5 合并执行**（Lead 已批准）。实测证据：删掉 `Registry()` 里的 13 条 UI
`mustRegister` 后，`internal/ui/uimodtest` 的 7 条集成测试立刻红
（`assembly.Load(desktop): module: validation failed`），因为它们用 desktop
装配断言"所有 UI 模块都激活"。也就是说 **"UI 模块注册" 与 "UI 包/它的测试"
必须同生共死**：注册先删，UI 测试就先红；包先删，注册就编译不过。
S3 唯一能独立绿的部分（`usesUIServices`/`declareUIServices`）也无法脱离注册
单独删（desktop 装配需要这些 builtin 声明）。因此 S3 的内容整体并入 S5。

### S2 执行记录（2026-11-23）

- 删除 `runDesktop`（415 行）+ `persistPreferences` + `close_guard.go`（无窗口后死代码）+ `update_command.go`（命令面板注册，只服务 UI）。
- `provideServices(root, dataDir, settings, logger)`：只剩 DataDir/Settings/Logger 三键；注释写明窗口类服务由 `gateway.Provide` 提供。
- `Run()` 的 desktop 分支改为明确错误；**`--product` 默认值改为 `serve`**（否则裸启动会立刻报错）。
- 测试调整：删掉"desktop host 提供 interaction/notifier/foreground"的 3 条（属性已转移给 gateway，gateway 侧 `services_test.go` 覆盖），保留并改名"host 不提供这些键"；`--port` 用例表更新（裸 `--port` 现在合法），新增 `--no-tray` 冲突用例。
- 顺带把全局热键接进托盘壳（`setupGlobalHotkey` 接受同一个 `BrowserWindow` 适配器，"按热键 = 打开客户端"）。
- **发现并修掉一个测试稳定性问题**：`TestE2EAutomationRunAppliesModelOverride` 在 run 未落定时就结束，SQLite 仍在写导致 `TempDir RemoveAll cleanup: directory not empty`（间歇红）。修法：追加"等 run 落定"，之后 `-count=5` 连跑 4 批全绿。
- 验收：build/vet 干净；`go test ./internal/host ./internal/assembly ./internal/gateway` 全绿；`make acceptance-approval` 通过；`./zeokeel --product desktop` 退出码 1 并给出指向 serve 的提示；裸启动 = serve。

### S1 执行记录（2026-11-23）

- 顺序与双保险落地：`runServe` 先起 `gw.Serve` goroutine 再进托盘；`trayEnabled(opts.NoTray)` = `--no-tray` 优先，Linux 无 `DISPLAY`/`WAYLAND_DISPLAY` 静默跳过。
- **过程中发现并修掉一个真 bug**：最初版本在托盘失败后 `cancelServe()`，导致"托盘起不来 → 网关跟着退出"（实测 `curl /` 得到 000）。改成"托盘失败只告警、等待网关自然结束"，实测 `DISPLAY=:99`（无 X server）时日志 `tray shell unavailable (mygo: cannot open display…)`，且 `/` 持续 200。
- 新增 `gateway.ReadInfo(dataDir)` 供托盘读 `gateway.json`；`internal/host/tray_shell.go` + 4 条单测（flag 优先、无显示跳过、适配器、URL 构造）。
- `--no-tray` 加进 `Options`/flag/usage，并对非 serve 产品报错。
- 验收：`make acceptance-approval` 与 `make acceptance-check` 通过；`go test ./internal/host ./internal/gateway ./internal/assembly` 全绿。

## 2. 步骤 S2：切除 host 的桌面路径

- [x] **S2.1** `internal/host/host.go:291-292`：`case assembly.ProductDesktop:` 从 `runDesktop(opts)` 改成明确错误并指向 serve。
  - 文案：`desktop 产品已移除：请用 --product serve（浏览器客户端）`，返回 `fmt.Errorf`，退出码非零。
  - 验证：`go run ./cmd/zeokeel --product desktop; echo $?` → 非零 + 该文案。
- [x] **S2.2** 删除 `internal/host/host.go:407-832` 的 `runDesktop` 整段（约 425 行）。
  - 含 `:431-436` 单实例 GUI 握手、`:461 uiroot.New`、`:820 mygo.App.Run()`。
  - 注意：单实例锁已在 `gateway.New` 里做（S1a 决策），serve 不需要 GUI 握手。
  - 验证：`go build ./...`；`grep -n "uiroot" internal/host/host.go` 只剩 `provideServices` 与 `persistPreferences` 两处签名（删段后行号会前移，S2.3/S2.4 再收掉）。
- [x] **S2.3** `internal/host/host.go:834` `provideServices(root, dataDir, settings, logger, uiRoot *uiroot.Root)`：去掉 `uiRoot` 参数与 `:845-882` 的 uiRoot 分支（12 个键）。
  - 保留：DataDir / Settings / Logger。另 5 个键（UserInteraction/ArtifactPresenter/PlanReviewer/Notifier/ForegroundSession）由 `gateway.Provide` 提供 —— 已经是现状。
  - 验证：`go vet ./internal/host`；`go test ./internal/host -run 'Provide'`。
- [x] **S2.4** `internal/host/host.go:893` 删除 `persistPreferences(settings, uiRoot, logger)`（窗口尺寸/位置的持久化随窗口消失）。
  - 验证：`go build ./...`；`grep -rn "persistPreferences" internal` 为空。
- [x] **S2.5** `internal/host/update_command.go`：去掉 uiroot。
  - `:18` 删 import；`:38` `registerUpdateInstallCommand(uiRoot *uiroot.Root, dataDir string, …)` 去掉参数；`:23` 命令 ID `"uiroot.install-update"` 删除（无消费者）或改 `"host.install-update"`（若托盘菜单要读，用后者）；`:27` `quitApplication` 保留（托盘 quit 用它）。
  - 保留 `applyPendingUpdateOnStartup` 的启动路径。
  - 验证：`go test ./internal/host -run 'Update'`；`grep -n "uiroot" internal/host/update_command.go` 为空。
- [x] **S2.6** 改 4 个 host 测试文件（去掉 uiroot 依赖）：`host_test.go`、`interaction_test.go`、`notifier_test.go`、`update_command_test.go`。
  - 手法：删掉"经 uiroot 驱动窗口"的断言；保留并补"经网关服务直接断言"的等价断言（gateway 侧已有覆盖，见 15 条 `TestE2E*`）。
  - 验证：`go test ./internal/host -count=1` 全绿。
- [x] **S2.7** 提交 `S2: remove the desktop host path`；回退点：`git revert <S2>`。

## 3. 步骤 S3：装配层塌缩

- [x] **S3.1** `internal/assembly/registry.go`：删 `:24-36` 的 13 个 `internal/ui/*` import + `:78-90` 的 13 个 `mustRegister`（home/interaction/toasts/welcome/toolcards/sidebar/session/settings/approval/rightpanel/automationui/insightsui/plugins）。
  - 保留 `:62 sessionstore`、`:63 approvals` 及其余后端模块。
  - 验证：`go build ./internal/assembly`；`grep -c "internal/ui/" internal/assembly/registry.go` = 0。
- [x] **S3.2** `internal/assembly/assembly.go:33` 删 `ProductDesktop` 常量及其注释 `:31-32`。
  - 连带 `:53 Products()` / `:72 AllProducts()`：返回集合去掉 desktop（serve/headless/acp 保留）。serve 成为默认。
  - 验证：`go run ./cmd/zeokeel --list-products` 输出不含 desktop；`internal/host/host_test.go` 的期望同步改。
- [x] **S3.3** `internal/assembly/assembly.go:207-208` 的 `registryFor` 塌缩为 `Registry()`；删 `:213-221 declareUIServices`、`:223-245 usesUIServices`（`uiServiceNames` 表只在 `:244` 的 `usesUIServices` 内部使用，随它一起删）。
  - 验证：`go build ./internal/assembly`；`grep -rn "declareUIServices\|usesUIServices" internal` 为空。
- [x] **S3.4** `internal/assembly/assembly_test.go`：`:123` 产品列表期望、`:138/:217/:284/:469/:643` 的 `ProductDesktop` 用法全部改到 `ProductServe` 或删除。
  - 验证：`go test ./internal/assembly -count=1`。
- [x] **S3.5** 提交 `S3: collapse the assembly layer to headless products`。
  - **（更正 2026-11-23）** 原记的这个提交**不存在**（`git log --all` 无匹配）：S3 已并入 S5。
    S3.1–S3.4 的工作确实做了，落在 `3a6c14b S5: retire the native UI` 内。

## 4. 步骤 S4：清掉"后端→UI 层"的最后一根依赖（**必须在删 uikit 之前**）

- [x] **S4.1** `internal/modules/approvals/approvals.go:361,373,393` 的 `uikit.Language(locale)`：把该 5 行 helper 移到 `internal/contracts`（`Language(locale string) string`，纯字符串判断 "en"/其余）。
  - 为什么进 contracts：它是纯本地化工具，被后端模块使用；**不要**为它新建包。
  - 验证：`go build ./internal/modules/approvals`；`grep -rn "uikit" internal/modules` 为空。
- [x] **S4.2** `internal/modules/hosttools/{bridges_test.go,goal_dsh_test.go}` 的 `internal/uiroot` import：改成 fake 或直接构造（这两个是测试，`go test ./internal/modules/hosttools` 必须仍绿）。
  - **（更正 2026-11-23）** `6544558 S4pre` 实际只改了 `contracts` + `approvals` + 本清单，
    **没动这两个文件**：`bridges_test.go` 是 S5 删除的（它断言的是 uiroot 的桥接实现），
    `goal_dsh_test.go` 的自带 loader 也是 S5 补的。
  - 验证：`go test ./internal/modules/hosttools -count=1`。
- [x] **S4.3** 提交 `S4pre: approvals localizes without the UI layer`。**此时 `internal/ui*` 仍然编译通过，随时可退回。**

## 5. 步骤 S5：删除（一次性，一个大提交）

- [x] **S5.1** 删包：`internal/ui/**`（13 模块 + `uimodtest`）、`internal/uiroot/**`、`internal/uikit/**`。
  - 一次性删完（三包互相 import，不能逐个删：uikit→ui 29 文件、ui→uiroot 如 session 25 文件）。
  - 验证：`go build ./...`。
- [x] **S5.2** 删只服务 UI 的 devtools：`internal/devtools/{designlint,ui-snapshot,ui-drive}`。
  - 验证：`grep -rn "devtools/designlint\|devtools/ui-snapshot\|devtools/ui-drive" --include=*.go --include=*.sh --include=Makefile --include=*.yml . | grep -v "^./.cache"` → 只在 S6/S7 待改的脚本里出现。
- [x] **S5.3** 删 `internal/design/conform/**` + `internal/design/conformance_test.go`；`internal/design/registry.go:16` 的注释里删掉对 `internal/design/conform` 的提及。
  - **保留** `internal/design/{tokens.go,components.go,font.go,motion.go,style.go,registry.go,registry.json}`（web `gen:tokens` 按文本解析，`registry.json` 是前端组件规格源）。
  - 验证：`go build ./...`；`go test ./internal/design -count=1`；`cd web && node scripts/gen-tokens.mjs` 仍产出（这是跨轨验收，必须跑）。
- [x] **S5.4** 删 `internal/regression/**`（71 文件 / 17,337 行）。后端覆盖已迁出：15 条 `TestE2E*` 在 `internal/gateway/`，对照表见 `docs/archive/regression-migration.md`。
  - 验证：`go build ./... && go test ./internal/gateway -run TestE2E -count=1` 全绿（这正是替代覆盖）。
- [x] **S5.5** 删 `audit/demo/**`（非测试包，import uiroot）与 `audit/probes/{ui,ext}/**`（探针测试）；`audit/upstream/*` 各自有 go.mod，不在 `./...`，不动。
  - 验证：`go list ./... | grep audit` 为空。
- [x] **S5.6** 清扫残留：`grep -rn "internal/uiroot\|internal/uikit\|internal/ui/" --include=*.go . | grep -v "^./.cache"` 必须为空（host 里没有单独的 uiroot 文件，残留只可能是 import 行）。
  - **（更正 2026-11-23）** S5 当时**并不为零**：还剩 3 处代码注释（`toolkit/settings_tools.go`、
    `agent/child_notices.go`、`design/registry.go`），直到 `7932b1c` 才清零。当时的准确表述应是
    "只剩注释级的陈旧引用"。
- [x] **S5.7** 记录删除行数（`git show --stat` 汇总），提交 `S5: retire the native UI (…lines)`。
  - **回退点**：`git reset --hard <S5^>` 一步回到"UI 仍在"的状态。

## 6. 步骤 S6：边界断言两档（产品严格 / test 单列）

- [x] **S6.1** 重写 `internal/assembly/boundary_test.go`：删 `:99-108 uiModuleAllowed` 与 `:231 TestNativeUIModelIsQuarantined`。
  - 验证：`go test ./internal/assembly -run 'TestImportBoundaries|TestContractsDependsOnNoEngine'`。
- [x] **S6.2** 新断言 **(a) 产品依赖**：`go list -deps` 逐包扫 mygo，允许清单**只有**：`internal/host`、`internal/lib/openpath`、`internal/lib/openurl`、`internal/brand`、`internal/design`。
  - 验证：`go test ./internal/assembly -run TestNativeUIModelIsQuarantined` 改名后的新用例绿；并附一次 `go list -deps ./internal/brand | grep -c mygo` = 1（证明清单里的包确实用它）。
- [x] **S6.3** 新断言 **(b) 后端为 0**：`internal/gateway`、`internal/contracts`（含 `uinative` 移除后）、`internal/modules/**` 对 mygo 的**传递**依赖必须为 0。
  - 验证：`go list -deps ./internal/gateway ./internal/contracts ./internal/modules/... | grep -c egoist/mygo` = 0。
- [x] **S6.4** 新断言 **(c) test 依赖单列**：用 `go list -deps -test` 单独统计，断言"test 依赖里出现 `internal/modules/*` 是允许的（e2e 要真模块），但**不得出现 mygo**"。注释里写明这是有意分档（避免有人把 e2e 的 test 依赖当红线误判）。
  - 验证：`go list -deps -test ./internal/gateway | grep -c egoist/mygo` = 0 **且** `... | grep -c 'internal/modules/'` > 0（证明分档生效）。
- [x] **S6.5** 删除 `internal/contracts/uinative`（**必须等 S2+S3+S5 都完成**：它的消费者是 `internal/ui/**`、`uiroot`、`assembly.go` 的 `declareUIServices`、`host.go` 的 `provideServices` 分支，前两步先改掉这些引用）。
  - 验证：`grep -rn "contracts/uinative" --include=*.go internal cmd audit` 为空；`go build ./...`；`go list -deps ./internal/gateway | grep -c uinative` = 0。
- [x] **S6.6** **证明会咬人**：临时把 `internal/lib/openurl` 加进一个不在清单的包（或在 `internal/modules/agent` 里 import mygo）→ 断言必须红；恢复后绿。把两次输出记进提交信息。
- [x] **S6.7** 提交 `S6: boundary assertions for the post-UI world (product + test tiers)`。

## 7. 步骤 S7：CI / 脚本 / Makefile

- [x] **S7.1** `.github/workflows/ci.yml:49-53` 删 `designlint` + `design-check` + `go tool mygo vet` 三步（若保留 mygo vet，见下方说明）。
  - 验证：`grep -n 'designlint\|design-check' .github/workflows/ci.yml` 为空；`actionlint`（若有）或人工读一遍。
  - mygo vet：托盘壳仍用 mygo → **保留**，但仅当它在只剩托盘时通过；否则改为对 `internal/host` 生效。
- [x] **S7.2** `ci.yml:85` 的 `-race ./internal/ui/... ./internal/uiroot …` 改成 `-race ./internal/gateway ./internal/modules/... ./internal/host ./internal/assembly`。
  - 验证：本地跑同一条命令（`-timeout 20m`）全绿再提交。
- [x] **S7.3** `scripts/verify.sh`：删 `:161-166 check_designlint / check_design_grep`、`:182-194 check_regression`、`:196-206 check_snapshot` 及 `:303-305/:313-314` 对应 `run_step`；`:10` 头部注释同步。
  - 验证：`bash -n scripts/verify.sh`；`bash scripts/verify.sh --quick` 走完无 "command not found"。
  - **保留** `:306` 的协议一致性检查（`TestTransport*`）。
- [x] **S7.4** `Makefile`：删 `design-check`(`:95-97`)、`design-baseline`(`:103`) 目标；`:28` 的 `.PHONY` 同步；`build/package/release` 不变；`acceptance*` 与 `web-client*` 保留。
  - 验证：`make -n build package` 无报错；`make design-check` 变成 "No rule"（预期）。
- [x] **S7.5** 归档 `scripts/design-check.sh` 与 `.design-baseline.json`（移到 `docs/archive/` 或直接删；二者只服务被删的 designlint）。
  - 验证：`grep -rn 'design-check.sh\|design-baseline' --include=*.sh --include=Makefile --include=*.yml . | grep -v docs/archive` 为空。
- [x] **S7.6** 保留 `mygo.json`（托盘打包用），检查其字段是否只服务窗口（单实例/图标），删除无用字段需验证打包脚本 `scripts/build-app.sh`。
- [x] **S7.7** 验证：`bash scripts/verify.sh --quick`；`make ci`；`go test ./internal/assembly -run TestImportBoundaries`。
- [x] **S7.8** 提交 `S7: CI and local verification without the native UI tooling`。

## 8. 步骤 S8：文档

- [x] **S8.1** `AGENTS.md`：§0（换轨表改为"已完成"）、§2 必读顺序（删 native UI 文档、指向 WEB-CLIENT/TRANSPORT）、§3 规则改为"仅托盘壳适用或归档"、§5 流程改 Web、§6 同步义务表、§7 硬边界、§9 速查表。
  - 验证：通读一遍，确认没有指向已删文件的命令/路径（`grep -n 'designlint\|ui-snapshot\|internal/uiroot' AGENTS.md` 为空）。
- [x] **S8.2** `docs/ARCHITECTURE.md`：增"传输层"章节；删 uiroot/UI 模块描述；§3 边界表更新为 S6 的两档断言。
  - 验证：`grep -n 'uiroot\|internal/ui/' docs/ARCHITECTURE.md` 为空或仅历史注记。
- [x] **S8.3** `docs/DESIGN.md`：标注 mygo 相关条目仅托盘壳适用。
  - 验证：文档头部的适用范围声明已改。
- [x] **S8.4** 归档（加"已归档 2026-11-23，随原生 UI 退役"头）：`INJECTION-POINTS.md`、`UI-CONFORMANCE.md`、`UI-COMPONENT-MIGRATION.md`、`DESIGN-LINT.md`、`UI-FIX-STATUS-v0.8.3.md`、`AGENT-SURFACES.md`（保留 S4.1 审批触发面章节——它对 web 仍有效，其余归档）、`COVERAGE.md`、`VERIFICATION.md`、`UI-AUDIT-2026-11-11.md`、`DESIGN-REVIEW.md`、`UI-STYLE-DIRECTION-2026-11-11.md`、`UI-UX-UPGRADE-2026-11-15.md`、`SETTINGS-IA-REVIEW-2026-11-16.md`、`USABILITY-REVIEW.md`、`INTERACTION-DEPTH-ROUND.md`、`PRODUCT-BUG-REVIEW-2026-11-11.md`、`ADVERSARIAL-AUDIT.md`。
  - 验证：每个归档文件首行有归档注记；`grep -rn 'S4.1' docs/archive/AGENT-SURFACES.md` 仍在（审批触发面保留）。
- [x] **S8.5** `docs/plans/s4-retirement-inventory.md` 与本清单本身移到 `docs/archive/`（它们已完成使命）。
  - **（2026-11-23 曾决定不移；2026-11-24 已真的移）** 当时留活是因为本清单还在被追加；
    现在 S9 之后的 T2 投影与复盘都已收口，退役工作结束，两份文件按原条目执行归档：
    `git mv docs/plans/s4-retirement-inventory.md docs/archive/s4-retirement-inventory.md`、
    `git mv docs/plans/s4-execution-checklist.md docs/archive/s4-execution-checklist.md`。
    引用同步到新路径：`AGENTS.md` §0/§9、`docs/ARCHITECTURE.md` §3/§8、`docs/archive/README.md` 索引。
  - 验证：`ls docs/plans/ | grep -c s4-` = **0**，`ls docs/archive/ | grep -c s4-` = **2**。
- [x] **S8.6** 提交 `S8: documentation for the browser-first product`。

## 9. 步骤 S9：收口验证（全绿才允许宣布完成）

- [ ] **S9.1** `gofmt -l internal cmd` 为空。
- [ ] **S9.2** `go build ./... && go vet ./...`。
- [ ] **S9.3** `go test ./... -count=1`（唯一允许的红：`internal/lib/hostbundle` 环境性失败，需在报告里复述）。
- [ ] **S9.4** `go test ./internal/gateway ./internal/modules/... ./internal/host ./internal/assembly -count=1 -race`。
- [ ] **S9.5** 泄漏检查（全为空/为零）：
  - `grep -rn "internal/uiroot\|internal/uikit\|internal/regression\|devtools/designlint" --include=*.go --include=*.sh --include=Makefile --include=*.yml . | grep -v "^./.cache" | grep -v "^./docs/archive"`
  - `go list -deps ./internal/gateway | grep -c egoist/mygo` → `0`
  - `go list -deps ./internal/contracts | grep -c egoist/mygo` → `0`
- [ ] **S9.6** 交叉编译：`for t in linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do GOOS=… CGO_ENABLED=0 go build -o /tmp/x ./cmd/zeokeel; done`。
- [ ] **S9.7** 产品冒烟：`./zeokeel --version`、`--list-products`（无 desktop）、`--product desktop`（明确错误）。
- [ ] **S9.8** 端到端复跑（浏览器）：`make acceptance-tool` + Lead 的 UI 六步；`make acceptance-check`（plain）、`make acceptance-slow`（断连≠停止）。
- [ ] **S9.9** web 生成物仍可用：`cd web && npm run gen:tokens && npm run gen:icons && npm run typecheck && npm run build`，再 `make web-client`。
- [ ] **S9.10** 提交 `S9: post-retirement verification` + 打 tag `post-s4`，报告里给删除总行数与每步提交号。
  - **（更正 2026-11-23）** 实际没有单独的 S9 提交，而是 `38da7f2`（S9 记录）+
    `9b28015`（hostbundle 表述修正 + 交叉编译/泄漏/文档漂移记录）；tag 名由 Lead 定为
    **`post-migration`**（注释 tag，打在 `9b28015`），不是 `post-s4`。

## 10. 三个"最可能删到一半要回退"的点（预研结论，执行时按此防）

| 风险 | 触发点 | 现在的预防（已就位） |
|---|---|---|
| ① `internal/brand`/`internal/design` 需要 `mygo/ui`，与"mygo 只在 5 个包"冲突 | S6.2 写断言时 | 允许清单已把两包纳入（Lead 已裁决）；**去 mygo 化**需要同时改 `web/scripts/gen-tokens.mjs:64-72` 的正则，明确不在 S4 |
| ② 同进程托盘在无显示环境启动失败 | S1.1/S1.2 | 先网关后托盘 + `App.Run` 失败只告警 + `DISPLAY` 探针 + CI 显式 `--no-tray` |
| ③ `internal/regression` 整包消失带走非 UI 覆盖 | S5.4 | 已迁 15 条 `TestE2E*`（审批/运行生命周期/automation/ACP/plan/队列），对照表在案 |

> 次级风险：`internal/modules/approvals` 的 `uikit.Language`（S4.1 先做，成本 5 行）；`internal/design/conformance_test.go` 依赖 conform（S5.3 同批删）。

## 11. 提交序列总览（每步可独立回退）

| 序 | 提交 | 回退含义 |
|---|---|---|
| 0 | `chore: drop committed memory.db artifacts` | 与 S4 无关，可独立 revert |
| 1 | `S1: serve runs the gateway before the tray; tray opens the browser` | 撤掉托盘能力，UI 仍全在 |
| 2 | `S2: remove the desktop host path` | 恢复窗口宿主（需 S1 回退配合） |
| 3 | `S3: collapse the assembly layer to headless products` | 恢复 desktop 装配 |
| 4 | `S4pre: approvals localizes without the UI layer` | 恢复 uikit 依赖（无意义回退，纯预防） |
| 5 | `S5: retire the native UI` | **一步回到 UI 仍在** |
| 6 | `S6: boundary assertions (product + test tiers)` | 恢复旧断言 |
| 7 | `S7: CI and local verification without the native UI tooling` | 恢复旧 CI |
| 8 | `S8: documentation for the browser-first product` | 恢复旧文档 |
| 9 | `S9: post-retirement verification` + tag `post-s4` | — |

---

## S9 之后新增的 T2 投影（2026-11-23）

S9 收口时的 P1/P2 账上还剩三条卡在 Go 侧。这三笔把它们投影成网关能力，**形态与既有域一致**
（hub 的 epoch/rev/快照缓存/重放环/合并，没有另起一套）。每笔记"改了什么 / 不变量 / 谁验证"。

### 1. `47fc194` — tasks 域（解锁 B12 后台任务 chip）

**改了什么**：`tasks`（进程级）与 `tasks:<sessionId>`（后端实现 `contracts.SessionTaskBoard`
时按会话隔离，否则回落）两个域；方法 `tasks.snapshot {sessionId?}`、`tasks.subscribe`、
`task.status {id,status,sessionId?}`、`task.delete {id,sessionId?}`。**没做** `task.create`/
`task.assign`（TE 表里仍标延后）。

**不变量**
- **delta 是 upsert 而非字段级 diff**：看板的 `status`/`owner`/`updatedAt` 就地变，而后端没有
  字段级变更日志 → 变了的任务**整行**进 `added`，客户端按 `id` upsert；消失的 id 进 `removed`。
  这样 "snapshot → 逐个应用 delta == 下一个快照" 仍然成立。
- **降级且不假装推送**：`TaskBoard` 无通知。网关写路径（status/delete）成功即 `refreshNow` +
  bump rev（调用方即刻回声）；别处的改动（agent 的 `team_task_*`、第二个客户端）靠 2s 轮询，
  **延迟上限 2s** 写进 §5.1.8/§6.3。
- **状态语汇取契约常量**，未知值 → `invalid_params`（不是静默 no-op）。

**谁验证**：`TestTasksDomainReplayMatchesNextSnapshot`（等轮询真发的 delta，upsert 回快照后与
下一次 snapshot 逐行 + rev 对齐）、`TestTaskCommandsDriveTheBoard`、`TestTransportMethodsMatchDispatch`
（先红后绿——新增 4 个方法未记录，补文档而不是改断言）。Lead 复核通过。

### 2. `2edadf1` — regenerate 作为 SSE 的一种模式（解锁 B11 重生成）

**改了什么**：`POST /api/chat/run` 增 `regenerate:true`；`startTurn` 在**挂好 SSE 观察器之后**
调 `chat.Regenerate`。WS 上的 `message.regenerate` **从协议移除**（§5.1 用删除线 + "已移除 →
见 §4.6 regenerate 模式"）。

**不变量**
- **发送路径只有一条**。两步式（WS 先重置、客户端再 POST run）会留下"历史已回退但没有 run"的
  半状态——`message.send` 当初正是因此被删。所以 regenerate 是 run 的**模式**，不是第二个方法。
- **顺序不能反**：`Regenerate` 自己 `startRunGoroutine` 起跑，观察器必须先在，否则丢开头事件。
- `text` 在该模式下必须为空（带 text → `invalid_params {field:"text"}`）：后端重跑的是它已经持有的
  最后一条用户消息，客户端再发一遍等于追加第二条 prompt。
- **`clientRunId` 幂等对 regenerate 同样生效**：重复 id 返回同一条流，**不会二次回退历史**。
- 后端缺该半边 → `1023 capability_unsupported` + `data.name="regenerate_not_supported"`，
  客户端据此禁用菜单项，而不是报"请求失败"。
- **顺带修掉的潜伏竞态**：`hub.refreshWatchedPrompts` 曾在 poller goroutine 直接读 `h.domains`，
  而该 map 归 hub goroutine 独占（`applyBuild` 插入新域不持锁）。在 `d484644` 上不触发是因为
  "建域 + 扫描"重叠不够频繁；tasks 的轮询把它顶了出来。修法：扫描改到 hub goroutine（`hubCall`），
  只有 `h.pending` 留给互斥量。**这类"被新功能暴露的旧竞态"只会出现在集成时。**

**谁验证**：`TestChatRunRegenerateModeIsTheSameSendPath`（全事件流 + 只调 Regenerate 不调 Send +
同 id 不二次回退）、`TestChatRunRegenerateRejectsText`、`TestChatRunRegenerateNeedsTheBackendHalf`、
`TestTransportMethodsMatchDispatch`/`TestTransportDeferredRowsStayHonest` 双绿、
`go test ./internal/gateway -race -count=1` 44.9s / 0 DATA RACE。Lead 复核通过。

### 3. `1c7fade` — context 域（B6 上下文 Tab）

**改了什么**：`context:<sessionId>` 域；`context.snapshot {sessionId}`、
`context.subscribe {sessionId, sinceRev}`。能力探测走 `initialize` 的 capabilities（新增
`gatewayCapabilities()`：装配里有 `KeyContext` 才追加 `"context"`）。`context.archive` 标延后。

**不变量**
- **`sessionId` 必填**。缺省返回一条空时间线会把"参数 bug"伪装成"这个会话没有上下文"——假空态
  与假绿同类。
- **delta 是整段替换**：`SessionContext` 每次读取都从 durable 事件日志重新折叠，只暴露 `Revision`
  （"没变就别重读"），`requests`/`events` 是"追加+截断"的有界窗口（各 200 条）——没有字段级变更
  日志可 diff。变化判定用后端 `Revision`；两者都 `found:false`、或 revision 未变 → 不发 delta。
- **`found:false` 是诚实空态**（冷会话/从未运行），不编造数字。
- **能力探测只用 capabilities**，不新增探测方法；没有 `KeyContext` 的装配不 advertise，客户端隐藏 Tab。
- 只对**已订阅**会话轮询（`hub.refreshWatchedPrefix`，与 prompts 共用），延迟上限 2s。

**谁验证**：`TestContextDomainReplayMatchesNextSnapshot`（冷会话 → 订阅 → 折叠 → 应用 delta →
与下一次 snapshot 逐字段 + rev 一致）、`TestContextSnapshotRequiresSession`、
`TestInitializeAdvertisesTheContextCapability`（有/无 inspector 双向）、
`go test ./internal/gateway -race -count=1` 50.4s / 0 DATA RACE、红线
`go list -deps ./internal/gateway | grep -c egoist/mygo` = 0。Lead 复核通过。

### 总账

Go 侧这三笔让 **B6 解锁**（context 域 + capability）、**B12 解锁**（tasks 域）、
**B11 变成"部分"**（复制 + 重生成可用；**"编辑"仍无协议方法**，不要写成完成）。
P1/P2 的最终数字由 web 轨接完三笔后确定（web 轨排队：i18n、B13/C5、B11/B12 接线、B6 接线）。

---

## 复盘核对（2026-11-23，只读核对；**不改动上面的历史记录**）

核对方法：对 S1–S9 的每个 `[x]`，回到提交、代码现状或实测输出找支撑。**结论：42 条有支撑，
6 条口径对不上**（下表）。这里的记录保持原样，问题列在这里，处置由 Lead 决定。

### 有明确支撑（抽查通过，示例）

| 项 | 支撑 |
|---|---|
| S1.1/S1.2/S1.3/S1.7 | `internal/host/serve.go` 里 `trayEnabled`(4 处) + `runServeWithTray`；`tray_shell.go` 存在；`519740c S1` |
| S2.1–S2.7 | `runDesktop` 0 处引用；`provideServices` 无 uiRoot 参数；`persistPreferences` 已删；`update_command.go` 已删；`dcb560a S2`（12 文件、0 个 web 文件） |
| S4.1/S4.3 | `contracts.Language` 存在；`6544558 S4pre` 改了 contracts + approvals + checklist |
| S5.1–S5.4/S5.7 | 三包已删；`git show --stat 3a6c14b` 586 文件 / −160,955 |
| S6.1–S6.5/S6.7 | `boundary_test.go` 五个断言函数；`uinative` 已删；`780b997 S6` |
| S6.6 | 两次注入（模块里 import mygo → 直查+传递双红；allowlist 删 brand → 红），恢复后复绿，记录在 S6 执行记录 |
| S7.1–S7.6/S7.8 | `ci.yml` 无 designlint/mygo vet，`-race` 名单含 contracts；`verify.sh` 五函数已删；`Makefile` 目标已删；两个设计工具在 `docs/archive/`；`mygo.json` 保留 |
| S8.1–S8.4/S8.6 | AGENTS 重写；ARCHITECTURE §3/§8 改写；DESIGN 加适用范围头；17 份文档在 `docs/archive/`；`d484644 S8` |
| S9.1–S9.7 | 本会话实测：gofmt 空、build/vet OK、34 包全 ok（hostbundle 修复后）、`--list-products`=headless/serve、`--product desktop` exit=1、四目标交叉编译、泄漏检查 0 |

### 口径对不上的六条（需处置，未擅自修改）

| 项 | 问题 | 事实 |
|---|---|---|
| **S3.5** | 勾选行写着"提交 `S3: collapse the assembly layer to headless products`"，但**这个提交不存在**（`git log --all` 无匹配）。S3 已并入 S5。 | S3.1–S3.4 的工作确实做了，在 `3a6c14b` 里；错的是那句提交记录。 |
| **S1.5 / S1.6** | 同一件事两处记账：line 31 已勾"S1.4–S1.6（改法调整）"，但 35/38 行仍留着未勾的同名条目。且 S1.5 的字面要求（`hotkey/notify/notification_click` 换成 `clientOpener`）**没有按字面发生**：仍是 `hotkeyWindow` 接口 + `BrowserWindow` 适配器（行为上等价：适配器的 Show/Restore/Focus 会打开客户端）。 | 行为覆盖成立，字面未执行；两处重复条目需要合并。 |
| **S4.2** | 勾选行说把 `hosttools/{bridges_test.go,goal_dsh_test.go}` 的 uiroot import 改成假实现。 | `6544558` **只改了 contracts + approvals**；`bridges_test.go` 是 S5 **删除**的，`goal_dsh_test.go` 的自带 loader 也是 S5 补的。S4pre 当时没动这两个文件。 |
| **S5.6** | 勾选行说"清扫残留（grep 为空）"。 | S5 当时 grep **不为零**：还剩 3 处代码注释（`toolkit/settings_tools.go`、`agent/child_notices.go`、`design/registry.go`），直到 `7932b1c`（S9 之后）才清零。当时的准确表述应是"只剩注释级的陈旧引用"。 |
| **S8.5** | 勾选行说把 `s4-retirement-inventory.md` 与本清单移到 `docs/archive/`。 | **已于 2026-11-24 补齐**：两者已 `git mv` 进 `docs/archive/`，引用同步；核对时它们还在 `docs/plans/` 是本条被记下的原因。 |
| **S9.10** | 勾选行写"提交 `S9: post-retirement verification` + 打 tag `post-s4`"。 | 实际是 `38da7f2` + `9b28015`（没有单独的 S9 提交），tag 名由 Lead 定为 **`post-migration`**（打在 `9b28015`）。 |

### 复盘得到的判断（写给后来的人）

**一、机器断言的价值不在"证明代码对"，而在"发现没人报的错"。**
例子：web 轨的路由自匹配断言上线当天就抓出 `/c/:id/files` 被解析成 R9/R10（预览）而不是
R8（文件树）——因为 splat 能匹配空尾巴而排序只看长度；界面看不出来（两条标题相同），语义已错。
同类投资：S6 的四条引擎边界断言（当天抓出 `internal/icons` 也 import 了 mygo）、
`TestTransportMethodsMatchDispatch`（两次抓出"文档声明了、实现没做"）。

**二、凡是被替身（mock / fake / 桩）顶替的地方，都必须有一个真环境的最小验证。**
否则替身的自洽会掩盖真实的缺失——这是本轮第 9 次"替身比生产宽松"。最贵的例子：
`web/src/test/fake-gateway.ts:99` 手写了 `'settings.set'`，而真网关的 `serverCapabilities`
没有它，于是 171 条测试全绿、设置页在真网关下永远只读。**"让 mock 更严格"不足以根治**：
替身手里的清单本身就是抄来的，抄漏一项整条链就失效——所以清单必须**从 Go 侧生成**
（task-17），并断言"替身的 capabilities 里不允许出现契约之外的名字"。

### 附注（不是清单条目）

- **S9.8 的 `make acceptance-tool` 是交互式目标**（起网关 + 假模型后等 Ctrl-C），没有通过/失败
  判定；S9 实际跑的是 `acceptance-check`/`acceptance-approval`/`acceptance-slow` 三条探针。
- **S9.9 的 web 生成链**由 web 轨验证（`gen:verify` 绿），Go 轨未代跑（不动 `web/`）。
- **发现一处死代码（已删，`1031e88`）**：`internal/host/notification_click.go` 与
  `notify.go:notifyWindowFront` 只有测试在用——托盘壳走 tray 菜单，不再接 `App.OnNotificationClick`。
  带测试所以不红，但它**暗示了一条不存在的链路**（读到 `notificationClicker` 的人会以为通知点击
  还接在那条链上），因此删除而不是留着。
