# S4 执行清单（原生 UI 退役 · 照单作业）

> 由 `docs/plans/s4-retirement-inventory.md`（只读预研）拆解而来。**分析已做完，S4 只剩执行**：每条给出「改哪个文件 / 哪几行 / 改成什么 / 怎么验证」，按依赖顺序编号，逐步可提交、可回退。
> 行号以 **2026-11-23 HEAD `59bd7f4`** 为准；动手前用每条的 `grep` 复核一次行号（本文件不含"大概位置"）。
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
- [ ] **S1.5** `internal/host/{hotkey.go,notify.go,notification_click.go}`：同样把 `*mygo.Window` 换成 `clientOpener`（热键、通知点击 → `OpenClient()`）。
  - 位置：`hotkey.go` 7 处、`notify.go` 7 处、`notification_click.go` 10 处 `window` 引用。
  - 验证：`go test ./internal/host -run 'Hotkey|Notify|NotificationClick'`。
- [ ] **S1.6** 删除 `internal/host/close_guard.go`（12 处 `window` 引用）+ 其测试：无窗口即无"关闭到托盘"语义。
  - 连带的**第二处**读取点在 `internal/host/tray.go:81-88 closeToTrayEnabled` + 它喂给 `setupTray(…, closeToTray bool)` 的菜单文案：新世界里没有窗口可关，去掉该参数与 `contracts.SettingCloseToTray` 的读取。
  - `contracts.SettingCloseToTray` 常量本身保留在 contracts（契约不删类型），设置页那一行由 **web 轨**标注「仅桌面端」或移除（跨轨事项，登记后通知）。
  - 验证：`go build ./...`；`grep -rn "CloseToTray" --include=*.go internal | grep -v internal/ui` 只剩 `internal/contracts/interaction.go`。
- [x] **S1.7** 提交 `S1: serve runs the gateway before the tray; tray opens the browser`。
  - 回退点检查：`git revert <S1>` 后 `go build ./...` 应恢复原状（此时 UI 包从未被动过，必然通过）。

### S1 执行记录（2026-11-23）

- 顺序与双保险落地：`runServe` 先起 `gw.Serve` goroutine 再进托盘；`trayEnabled(opts.NoTray)` = `--no-tray` 优先，Linux 无 `DISPLAY`/`WAYLAND_DISPLAY` 静默跳过。
- **过程中发现并修掉一个真 bug**：最初版本在托盘失败后 `cancelServe()`，导致"托盘起不来 → 网关跟着退出"（实测 `curl /` 得到 000）。改成"托盘失败只告警、等待网关自然结束"，实测 `DISPLAY=:99`（无 X server）时日志 `tray shell unavailable (mygo: cannot open display…)`，且 `/` 持续 200。
- 新增 `gateway.ReadInfo(dataDir)` 供托盘读 `gateway.json`；`internal/host/tray_shell.go` + 4 条单测（flag 优先、无显示跳过、适配器、URL 构造）。
- `--no-tray` 加进 `Options`/flag/usage，并对非 serve 产品报错。
- 验收：`make acceptance-approval` 与 `make acceptance-check` 通过；`go test ./internal/host ./internal/gateway ./internal/assembly` 全绿。

## 2. 步骤 S2：切除 host 的桌面路径

- [ ] **S2.1** `internal/host/host.go:291-292`：`case assembly.ProductDesktop:` 从 `runDesktop(opts)` 改成明确错误并指向 serve。
  - 文案：`desktop 产品已移除：请用 --product serve（浏览器客户端）`，返回 `fmt.Errorf`，退出码非零。
  - 验证：`go run ./cmd/zeokeel --product desktop; echo $?` → 非零 + 该文案。
- [ ] **S2.2** 删除 `internal/host/host.go:407-832` 的 `runDesktop` 整段（约 425 行）。
  - 含 `:431-436` 单实例 GUI 握手、`:461 uiroot.New`、`:820 mygo.App.Run()`。
  - 注意：单实例锁已在 `gateway.New` 里做（S1a 决策），serve 不需要 GUI 握手。
  - 验证：`go build ./...`；`grep -n "uiroot" internal/host/host.go` 只剩 `provideServices` 与 `persistPreferences` 两处签名（删段后行号会前移，S2.3/S2.4 再收掉）。
- [ ] **S2.3** `internal/host/host.go:834` `provideServices(root, dataDir, settings, logger, uiRoot *uiroot.Root)`：去掉 `uiRoot` 参数与 `:845-882` 的 uiRoot 分支（12 个键）。
  - 保留：DataDir / Settings / Logger。另 5 个键（UserInteraction/ArtifactPresenter/PlanReviewer/Notifier/ForegroundSession）由 `gateway.Provide` 提供 —— 已经是现状。
  - 验证：`go vet ./internal/host`；`go test ./internal/host -run 'Provide'`。
- [ ] **S2.4** `internal/host/host.go:893` 删除 `persistPreferences(settings, uiRoot, logger)`（窗口尺寸/位置的持久化随窗口消失）。
  - 验证：`go build ./...`；`grep -rn "persistPreferences" internal` 为空。
- [ ] **S2.5** `internal/host/update_command.go`：去掉 uiroot。
  - `:18` 删 import；`:38` `registerUpdateInstallCommand(uiRoot *uiroot.Root, dataDir string, …)` 去掉参数；`:23` 命令 ID `"uiroot.install-update"` 删除（无消费者）或改 `"host.install-update"`（若托盘菜单要读，用后者）；`:27` `quitApplication` 保留（托盘 quit 用它）。
  - 保留 `applyPendingUpdateOnStartup` 的启动路径。
  - 验证：`go test ./internal/host -run 'Update'`；`grep -n "uiroot" internal/host/update_command.go` 为空。
- [ ] **S2.6** 改 4 个 host 测试文件（去掉 uiroot 依赖）：`host_test.go`、`interaction_test.go`、`notifier_test.go`、`update_command_test.go`。
  - 手法：删掉"经 uiroot 驱动窗口"的断言；保留并补"经网关服务直接断言"的等价断言（gateway 侧已有覆盖，见 15 条 `TestE2E*`）。
  - 验证：`go test ./internal/host -count=1` 全绿。
- [ ] **S2.7** 提交 `S2: remove the desktop host path`；回退点：`git revert <S2>`。

## 3. 步骤 S3：装配层塌缩

- [ ] **S3.1** `internal/assembly/registry.go`：删 `:24-36` 的 13 个 `internal/ui/*` import + `:78-90` 的 13 个 `mustRegister`（home/interaction/toasts/welcome/toolcards/sidebar/session/settings/approval/rightpanel/automationui/insightsui/plugins）。
  - 保留 `:62 sessionstore`、`:63 approvals` 及其余后端模块。
  - 验证：`go build ./internal/assembly`；`grep -c "internal/ui/" internal/assembly/registry.go` = 0。
- [ ] **S3.2** `internal/assembly/assembly.go:33` 删 `ProductDesktop` 常量及其注释 `:31-32`。
  - 连带 `:53 Products()` / `:72 AllProducts()`：返回集合去掉 desktop（serve/headless/acp 保留）。serve 成为默认。
  - 验证：`go run ./cmd/zeokeel --list-products` 输出不含 desktop；`internal/host/host_test.go` 的期望同步改。
- [ ] **S3.3** `internal/assembly/assembly.go:207-208` 的 `registryFor` 塌缩为 `Registry()`；删 `:213-221 declareUIServices`、`:223-245 usesUIServices`（`uiServiceNames` 表只在 `:244` 的 `usesUIServices` 内部使用，随它一起删）。
  - 验证：`go build ./internal/assembly`；`grep -rn "declareUIServices\|usesUIServices" internal` 为空。
- [ ] **S3.4** `internal/assembly/assembly_test.go`：`:123` 产品列表期望、`:138/:217/:284/:469/:643` 的 `ProductDesktop` 用法全部改到 `ProductServe` 或删除。
  - 验证：`go test ./internal/assembly -count=1`。
- [ ] **S3.5** 提交 `S3: collapse the assembly layer to headless products`。

## 4. 步骤 S4：清掉"后端→UI 层"的最后一根依赖（**必须在删 uikit 之前**）

- [ ] **S4.1** `internal/modules/approvals/approvals.go:361,373,393` 的 `uikit.Language(locale)`：把该 5 行 helper 移到 `internal/contracts`（`Language(locale string) string`，纯字符串判断 "en"/其余）。
  - 为什么进 contracts：它是纯本地化工具，被后端模块使用；**不要**为它新建包。
  - 验证：`go build ./internal/modules/approvals`；`grep -rn "uikit" internal/modules` 为空。
- [ ] **S4.2** `internal/modules/hosttools/{bridges_test.go,goal_dsh_test.go}` 的 `internal/uiroot` import：改成 fake 或直接构造（这两个是测试，`go test ./internal/modules/hosttools` 必须仍绿）。
  - 验证：`go test ./internal/modules/hosttools -count=1`。
- [ ] **S4.3** 提交 `S4pre: approvals localizes without the UI layer`。**此时 `internal/ui*` 仍然编译通过，随时可退回。**

## 5. 步骤 S5：删除（一次性，一个大提交）

- [ ] **S5.1** 删包：`internal/ui/**`（13 模块 + `uimodtest`）、`internal/uiroot/**`、`internal/uikit/**`。
  - 一次性删完（三包互相 import，不能逐个删：uikit→ui 29 文件、ui→uiroot 如 session 25 文件）。
  - 验证：`go build ./...`。
- [ ] **S5.2** 删只服务 UI 的 devtools：`internal/devtools/{designlint,ui-snapshot,ui-drive}`。
  - 验证：`grep -rn "devtools/designlint\|devtools/ui-snapshot\|devtools/ui-drive" --include=*.go --include=*.sh --include=Makefile --include=*.yml . | grep -v "^./.cache"` → 只在 S6/S7 待改的脚本里出现。
- [ ] **S5.3** 删 `internal/design/conform/**` + `internal/design/conformance_test.go`；`internal/design/registry.go:16` 的注释里删掉对 `internal/design/conform` 的提及。
  - **保留** `internal/design/{tokens.go,components.go,font.go,motion.go,style.go,registry.go,registry.json}`（web `gen:tokens` 按文本解析，`registry.json` 是前端组件规格源）。
  - 验证：`go build ./...`；`go test ./internal/design -count=1`；`cd web && node scripts/gen-tokens.mjs` 仍产出（这是跨轨验收，必须跑）。
- [ ] **S5.4** 删 `internal/regression/**`（71 文件 / 17,337 行）。后端覆盖已迁出：15 条 `TestE2E*` 在 `internal/gateway/`，对照表见 `docs/plans/regression-migration.md`。
  - 验证：`go build ./... && go test ./internal/gateway -run TestE2E -count=1` 全绿（这正是替代覆盖）。
- [ ] **S5.5** 删 `audit/demo/**`（非测试包，import uiroot）与 `audit/probes/{ui,ext}/**`（探针测试）；`audit/upstream/*` 各自有 go.mod，不在 `./...`，不动。
  - 验证：`go list ./... | grep audit` 为空。
- [ ] **S5.6** 清扫残留：`grep -rn "internal/uiroot\|internal/uikit\|internal/ui/" --include=*.go . | grep -v "^./.cache"` 必须为空（host 里没有单独的 uiroot 文件，残留只可能是 import 行）。
- [ ] **S5.7** 记录删除行数（`git show --stat` 汇总），提交 `S5: retire the native UI (…lines)`。
  - **回退点**：`git reset --hard <S5^>` 一步回到"UI 仍在"的状态。

## 6. 步骤 S6：边界断言两档（产品严格 / test 单列）

- [ ] **S6.1** 重写 `internal/assembly/boundary_test.go`：删 `:99-108 uiModuleAllowed` 与 `:231 TestNativeUIModelIsQuarantined`。
  - 验证：`go test ./internal/assembly -run 'TestImportBoundaries|TestContractsDependsOnNoEngine'`。
- [ ] **S6.2** 新断言 **(a) 产品依赖**：`go list -deps` 逐包扫 mygo，允许清单**只有**：`internal/host`、`internal/lib/openpath`、`internal/lib/openurl`、`internal/brand`、`internal/design`。
  - 验证：`go test ./internal/assembly -run TestNativeUIModelIsQuarantined` 改名后的新用例绿；并附一次 `go list -deps ./internal/brand | grep -c mygo` = 1（证明清单里的包确实用它）。
- [ ] **S6.3** 新断言 **(b) 后端为 0**：`internal/gateway`、`internal/contracts`（含 `uinative` 移除后）、`internal/modules/**` 对 mygo 的**传递**依赖必须为 0。
  - 验证：`go list -deps ./internal/gateway ./internal/contracts ./internal/modules/... | grep -c egoist/mygo` = 0。
- [ ] **S6.4** 新断言 **(c) test 依赖单列**：用 `go list -deps -test` 单独统计，断言"test 依赖里出现 `internal/modules/*` 是允许的（e2e 要真模块），但**不得出现 mygo**"。注释里写明这是有意分档（避免有人把 e2e 的 test 依赖当红线误判）。
  - 验证：`go list -deps -test ./internal/gateway | grep -c egoist/mygo` = 0 **且** `... | grep -c 'internal/modules/'` > 0（证明分档生效）。
- [ ] **S6.5** 删除 `internal/contracts/uinative`（**必须等 S2+S3+S5 都完成**：它的消费者是 `internal/ui/**`、`uiroot`、`assembly.go` 的 `declareUIServices`、`host.go` 的 `provideServices` 分支，前两步先改掉这些引用）。
  - 验证：`grep -rn "contracts/uinative" --include=*.go internal cmd audit` 为空；`go build ./...`；`go list -deps ./internal/gateway | grep -c uinative` = 0。
- [ ] **S6.6** **证明会咬人**：临时把 `internal/lib/openurl` 加进一个不在清单的包（或在 `internal/modules/agent` 里 import mygo）→ 断言必须红；恢复后绿。把两次输出记进提交信息。
- [ ] **S6.7** 提交 `S6: boundary assertions for the post-UI world (product + test tiers)`。

## 7. 步骤 S7：CI / 脚本 / Makefile

- [ ] **S7.1** `.github/workflows/ci.yml:49-53` 删 `designlint` + `design-check` + `go tool mygo vet` 三步（若保留 mygo vet，见下方说明）。
  - 验证：`grep -n 'designlint\|design-check' .github/workflows/ci.yml` 为空；`actionlint`（若有）或人工读一遍。
  - mygo vet：托盘壳仍用 mygo → **保留**，但仅当它在只剩托盘时通过；否则改为对 `internal/host` 生效。
- [ ] **S7.2** `ci.yml:85` 的 `-race ./internal/ui/... ./internal/uiroot …` 改成 `-race ./internal/gateway ./internal/modules/... ./internal/host ./internal/assembly`。
  - 验证：本地跑同一条命令（`-timeout 20m`）全绿再提交。
- [ ] **S7.3** `scripts/verify.sh`：删 `:161-166 check_designlint / check_design_grep`、`:182-194 check_regression`、`:196-206 check_snapshot` 及 `:303-305/:313-314` 对应 `run_step`；`:10` 头部注释同步。
  - 验证：`bash -n scripts/verify.sh`；`bash scripts/verify.sh --quick` 走完无 "command not found"。
  - **保留** `:306` 的协议一致性检查（`TestTransport*`）。
- [ ] **S7.4** `Makefile`：删 `design-check`(`:95-97`)、`design-baseline`(`:103`) 目标；`:28` 的 `.PHONY` 同步；`build/package/release` 不变；`acceptance*` 与 `web-client*` 保留。
  - 验证：`make -n build package` 无报错；`make design-check` 变成 "No rule"（预期）。
- [ ] **S7.5** 归档 `scripts/design-check.sh` 与 `.design-baseline.json`（移到 `docs/archive/` 或直接删；二者只服务被删的 designlint）。
  - 验证：`grep -rn 'design-check.sh\|design-baseline' --include=*.sh --include=Makefile --include=*.yml . | grep -v docs/archive` 为空。
- [ ] **S7.6** 保留 `mygo.json`（托盘打包用），检查其字段是否只服务窗口（单实例/图标），删除无用字段需验证打包脚本 `scripts/build-app.sh`。
- [ ] **S7.7** 验证：`bash scripts/verify.sh --quick`；`make ci`；`go test ./internal/assembly -run TestImportBoundaries`。
- [ ] **S7.8** 提交 `S7: CI and local verification without the native UI tooling`。

## 8. 步骤 S8：文档

- [ ] **S8.1** `AGENTS.md`：§0（换轨表改为"已完成"）、§2 必读顺序（删 native UI 文档、指向 WEB-CLIENT/TRANSPORT）、§3 规则改为"仅托盘壳适用或归档"、§5 流程改 Web、§6 同步义务表、§7 硬边界、§9 速查表。
  - 验证：通读一遍，确认没有指向已删文件的命令/路径（`grep -n 'designlint\|ui-snapshot\|internal/uiroot' AGENTS.md` 为空）。
- [ ] **S8.2** `docs/ARCHITECTURE.md`：增"传输层"章节；删 uiroot/UI 模块描述；§3 边界表更新为 S6 的两档断言。
  - 验证：`grep -n 'uiroot\|internal/ui/' docs/ARCHITECTURE.md` 为空或仅历史注记。
- [ ] **S8.3** `docs/DESIGN.md`：标注 mygo 相关条目仅托盘壳适用。
  - 验证：文档头部的适用范围声明已改。
- [ ] **S8.4** 归档（加"已归档 2026-11-23，随原生 UI 退役"头）：`INJECTION-POINTS.md`、`UI-CONFORMANCE.md`、`UI-COMPONENT-MIGRATION.md`、`DESIGN-LINT.md`、`UI-FIX-STATUS-v0.8.3.md`、`AGENT-SURFACES.md`（保留 S4.1 审批触发面章节——它对 web 仍有效，其余归档）、`COVERAGE.md`、`VERIFICATION.md`、`UI-AUDIT-2026-11-11.md`、`DESIGN-REVIEW.md`、`UI-STYLE-DIRECTION-2026-11-11.md`、`UI-UX-UPGRADE-2026-11-15.md`、`SETTINGS-IA-REVIEW-2026-11-16.md`、`USABILITY-REVIEW.md`、`INTERACTION-DEPTH-ROUND.md`、`PRODUCT-BUG-REVIEW-2026-11-11.md`、`ADVERSARIAL-AUDIT.md`。
  - 验证：每个归档文件首行有归档注记；`grep -rn 'S4.1' docs/AGENT-SURFACES.md` 仍在（审批触发面保留）。
- [ ] **S8.5** `docs/plans/s4-retirement-inventory.md` 与本清单本身移到 `docs/archive/`（它们已完成使命）。
  - 验证：`ls docs/plans/ | grep -c s4-` = 0，`ls docs/archive/ | grep s4-` = 2。
- [ ] **S8.6** 提交 `S8: documentation for the browser-first product`。

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