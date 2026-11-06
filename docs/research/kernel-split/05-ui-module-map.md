# 05 · shell → UI 模块映射与迁移顺序（研究线 E）

> 对象：`/data/mygo/internal/shell`（159 个 `.go` = 59 生产 + 100 测试；生产 25,229 行；测试 29,465 行；`func (a *App)` 779 个；19 个包级 `sync.Map`；`App` struct 123 字段）。基线 `HEAD=40368d1`，只读分析，产品代码零改动。
> 前提（用户新约束）：UI 也拆成**编译期模块**——编进同一二进制、经 `zeokeel-kernel/module` 注册表装配、通过**贡献点**（screen/slot/settings page/tool renderer/command/overlay）组合；**不是运行时插件**。
> 与 [03](03-mygo-module-graph.md) 的关系：03 的「UI 不能模块化，留宿主」结论成立于「kernel fiber 管理 UI 生命周期 + 运行时装配」的假设；[04](04-ui-constraints.md) §0.3 已把它细化为「帧循环/窗口不可模块化，UI 功能面可做编译期贡献层」；本报告在该修正之上给出模块映射，并接受 [04 §0.1 八条硬约束](04-ui-constraints.md) 与 §3 的 15 条模块约束。UI 服务命名沿用 04 §5：契约包 `internal/contracts/ui` + 宿主实现 `internal/host/ui`，服务键 `app/ui`（Runtime）、`app/ui/navigator`、`app/ui/theme`、`app/ui/locale`；**注意 06 spike 里 RuntimeKey 写作 `ui/runtime`**（装配契约以 04 定稿为准，本报告统一用 04 命名）。[06](06-ui-spike.md) 已实测：五张贡献注册表 + Effect 逆序注销 + 门控 + Post 帧首 drain + 装配/回滚（36 测试、`-race` 绿）。
> 证据分级：**[A]** 本报告命令实测（探针见 `.probe/research/kernel-split/ui-map/`，全部可复现）；**[B]** 读源码给 `file:line`；**[C]** 估算/判断。
> 产出物：本文件 + `.probe/research/kernel-split/ui-map/{inventory.go,funcs.go,analyze.go,state.go,appfields.go,rollup.py,files.tsv,funcs.tsv,coupling.tsv,state.tsv,modules.tsv,domains.tsv,tests.tsv}`。

## 0. 结论摘要

### 0.1 直接回答

| 问题 | 回答 | 关键证据 |
| --- | --- | --- |
| UI 能拆成编译期模块吗？ | **能**：16 个模块 + 宿主 chrome + 2 个共享库。按文件唯一归属：模块 22,198 + 宿主整文件 2,745 + uikit 286 = **25,229 精确守恒**；按运行时形态（含 668 行宿主内切分、90 行 approval carve-out）：模块 21,620 + 宿主 3,413 + uikit 286（approval 90 不计入唯一合计）。模块只贡献**只读渲染函数 + 命令/设置页/工具渲染器注册**，宿主每帧按注册表调用，不依赖 kernel 的线程语义。 | §1.4、§4、`rollup.py` 断言 prodLoc=25229 |
| 最大障碍是什么？ | **God object 状态**：`App` 123 字段 + 19 个 `*App` 包级 `sync.Map`/159 个状态字段；其中 **20 个字段被 ≥2 个 UI 模块触达、38 个被「模块+宿主」共同触达**（合计 47 个字段存在多归属），`Ctrl/Screen/rightOpen/composerRef/draft` 是跨模块热点。不先做状态搬迁，拆完只是「换个文件放同一张表」。 | §2.2、`state.tsv`、`modules.tsv` |
| 哪些必须留宿主？ | frame/View 入口、layout/几何、sidebar shell、窗口与菜单、overlay 栈与 Esc 链、toast viewport、theme/locale/motion 运行态、settings 独立窗口的**窗口宿主**；共 **3,413 行 shell + 733 行 design（生产）**（另 `internal/app` 窗口/菜单/托盘约 760 行）。 | §5.1 |
| 迁移顺序？ | strangler **4 波 + Wave 0**：Wave 0 宿主 UI 服务/共享库 → W1 低耦合叶子（toolcards/diff/plugins/automation/设置通用页）→ W2 功能 slot（memory/team/goal/plan/approval/todo/设置特性页）→ W3 导航/侧栏（workspaces/files/home）→ W4 session/transcript/composer。每波内置「注册→渲染→卸载」三步验收。 | §7 |
| 工作量？ | 模块化本体 **84–112 人日**（含模块测试迁移 19.9k 行）；加 Wave 0（12–16）与跨模块验收/收尾（8–12）后 **约 105–140 人日 [C]**；与 03 的 20 个后端模块（50–78 人日）可并行，总程序约 **155–218 人日**。 | §7.3 |

### 0.2 五条关键结论

1. **贡献点不是新生事物，代码里已经以「巨型 switch + 散落文件」的形式存在。** 顶层 `mainScreen` switch（`app.go:502`）、14 个 settings 页 switch（`settings_ui.go:421`）、右栏 4 个 tab switch（`right_sidebar.go:185`）、16 条 slash 命令 switch（`composer_menus.go:253`）、Esc 链 switch（`session_search.go:162`）、`toolcards` 的 variant 分支——本报告把它们逐个映射为注册表条目（§3）。
2. **「一屏一模块」不够：真正可复用的边界是「贡献点 + 数据源」。** 例如 diff 卡（`workspace_changes_ui.go`）不是屏，却是 transcript 的 turn-footer 贡献；审批卡（`composer_menus.go:184`）是 composer 上方的 slot；设置页由 5 个文件共同声明枚举（`models_ui.go:23`、`settings_extra.go:29`、`manage_ui.go:25`、`memory_ui.go:32`、`plugin_details.go:26`）——这些正是 DSH client-ui 用 slot 注册替代中心路由表的动因（02 §4.5）。
3. **宿主 chrome 的比例是 13.5%**：3,413 / 25,229 行（frame/布局/侧栏壳/overlay/toast/快捷键/主题/语言/启动恢复）。把 chrome 也拆成模块会直接破坏 mygo 的「单主线程 + 每帧重建 + 模态抢占」契约，收益为负（§5.3）。
4. **迁移成本的大头在测试而非生产代码**：100 个测试文件全部是白盒 `package shell`，29,465 行、572 个 Test/Benchmark 函数、513 处 `ui.NewTester`、430 处 `newLiveApp`；其中 16 个跨模块验收文件 6,074 行只能改造、不能随模块搬走（§8）。
5. **模块化收益 = 装配纪律与边界可断言，不是体积/热插拔**：16 个模块全部编译进同一二进制，二进制不会变小；收益是 screen/命令/设置页/工具卡可被 assembly 裁剪、CI 断言依赖方向、同屏能力可被 headless 复用（与 01/03 的收益边界一致）。

## 1. 口径、全量文件表与度量

### 1.1 规模校验 [A]

| 指标 | 数值 | 复现命令 |
| --- | --- | --- |
| shell `.go` 文件 | **159** = 59 生产 + 100 测试 | `ls internal/shell/*.go \| wc -l`；`ls internal/shell/*_test.go \| wc -l` |
| 非测试 LOC | **25,229** | `find internal/shell -maxdepth 1 -name '*.go' ! -name '*_test.go' -print0 \| xargs -0 cat \| wc -l` |
| 测试 LOC | **29,465** | 同上，`-name '*_test.go'` |
| `func (a *App)` 方法 | **779**（另有 192 个包级函数、仅 6 个导出） | `go run .probe/research/kernel-split/ui-map/inventory.go` |
| 包级 `sync.Map`（`map[*App]*X`） | **19** 个，对应 18 个状态 struct 共 **159** 个字段 + 1 个 `map[string]bool` | `go run .../state.go` |
| `App` struct | 123 字段（45 bool、37 string、10 个 func seam、4 map、3 Handle、6 同步/句柄/上下文） | `go run .../appfields.go` |
| 后端耦合面 | 72 个不同 `Ctrl.X` 方法、**295 处**调用点；`.Store()` 链 **67 处**（AST；`grep -oE "\.Store\(\)"` 为 64，差 3 处为无括号引用） | `go run .../analyze.go` |
| 测试函数 | 572（564 Test + 8 Benchmark）；`ui.NewTester` 513 处、`newLiveApp` 430 处 | `grep -c '^func Test' ...` |

> 口径备注：03 报的「74 个 Ctrl 方法 / 59 处 Store」是更早 HEAD 的 grep 口径；本报告统一用 AST 口径（72 / 295 / 64）。行数均为物理行（含空行/注释），与 `wc -l` 一致。

### 1.2 生产文件全表（59 个，含归属与 App 方法数）[A]

| 文件 | LOC | App 方法 | 归属 |
| --- | ---: | ---: | --- |
| `home.go` | 503 | 14 | ui/home |
| `session_live.go` | 1131 | 33 | ui/session |
| `trajectory.go` | 612 | 12 | ui/session |
| `reasoning_ui.go` | 437 | 3 | ui/session |
| `message_actions.go` | 387 | 9 | ui/session |
| `transcript_order.go` | 264 | 0 | ui/session |
| `session_errors.go` | 165 | 2 | ui/session |
| `session_open_async.go` | 144 | 5 | ui/session |
| `session_workspace.go` | 94 | 3 | ui/session |
| `session.go` | 43 | 1 | ui/session |
| `composer_slash.go` | 1060 | 23 | ui/composer |
| `composer_menus.go` | 996 | 31 | ui/composer |
| `home_live_menus.go` | 622 | 19 | ui/composer |
| `attachments_ui.go` | 272 | 11 | ui/composer |
| `draft_scope.go` | 234 | 11 | ui/composer |
| `modes_ui.go` | 176 | 7 | ui/composer |
| `composer_native.go` | 121 | 0 | ui/composer |
| `composer_style.go` | 113 | 1 | ui/composer |
| `toolcards.go` | 704 | 14 | ui/toolcards |
| `models_ui.go` | 1055 | 34 | ui/settings |
| `settings_ui.go` | 1033 | 40 | ui/settings |
| `settings_extra.go` | 703 | 25 | ui/settings |
| `presets_ui.go` | 500 | 15 | ui/settings |
| `settings_update_hotkey.go` | 440 | 13 | ui/settings |
| `manage_ui.go` | 439 | 13 | ui/settings |
| `settings.go` | 204 | 2 | ui/settings |
| `plugin_details.go` | 858 | 29 | ui/plugins |
| `plugins.go` | 393 | 13 | ui/plugins |
| `plugins_ui.go` | 312 | 9 | ui/plugins |
| `automation_ui.go` | 828 | 33 | ui/automation |
| `automation.go` | 46 | 2 | ui/automation |
| `memory_ui.go` | 832 | 40 | ui/memory |
| `team_ui.go` | 229 | 10 | ui/team |
| `composer_goal_compact.go` | 218 | 8 | ui/goal |
| `plan_ui.go` | 475 | 16 | ui/plan |
| `todo_ui.go` | 379 | 12 | ui/todo |
| `workspaces.go` | 1504 | 51 | ui/workspaces |
| `session_search.go` | 943 | 36 | ui/workspaces |
| `sessions_cache_async.go` | 137 | 5 | ui/workspaces |
| `sidebar_hover.go` | 127 | 2 | ui/workspaces |
| `right_sidebar.go` | 1232 | 35 | ui/files |
| `start_tab.go` | 524 | 13 | ui/files |
| `files_root.go` | 276 | 9 | ui/files |
| `files_native.go` | 109 | 6 | ui/files |
| `files_preview.go` | 75 | 2 | ui/files |
| `workspace_changes_ui.go` | 249 | 8 | ui/diff |
| `app.go` | 553 | 13 | host/chrome |
| `shortcuts.go` | 466 | 21 | host/chrome |
| `chrome.go` | 379 | 13 | host/chrome |
| `menus.go` | 354 | 12 | host/chrome |
| `layout.go` | 306 | 12 | host/chrome |
| `i18n.go` | 190 | 10 | host/chrome |
| `preferences.go` | 151 | 2 | host/chrome |
| `session_restore.go` | 126 | 5 | host/chrome |
| `motion.go` | 108 | 1 | host/chrome |
| `buildinfo.go` | 57 | 0 | host/chrome |
| `assets.go` | 55 | 0 | host/chrome |
| `popover_menus.go` | 167 | 7 | lib/uikit |
| `markdown.go` | 119 | 3 | lib/uikit |

### 1.3 测试文件归属（100 个，随生产模块迁移；跨模块验收 16 个留 shell）[A]

| 归属 | 测试文件数 | 测试 LOC | 测试函数 | 文件（LOC） |
| --- | ---: | ---: | ---: | --- |
| ui/home | 3 | 497 | 15 | `home_live_test.go`(204)、`home_focus_test.go`(167)、`first_run_ui_test.go`(126) |
| ui/session | 15 | 3609 | 77 | `session_live_test.go`(996)、`trajectory_test.go`(454)、`reasoning_ui_test.go`(334)、`transcript_order_test.go`(319)、`usage_footer_live_test.go`(203)、`message_actions_test.go`(199)、`send_feedback_test.go`(197)、`reasoning_perf_test.go`(186)、`session_errors_test.go`(132)、`fork_export_acceptance_test.go`(130)、`turn_marks_test.go`(121)、`auto_title_ui_test.go`(99)、`session_open_async_test.go`(93)、`transcript_error_test.go`(87)、`session_workspace_test.go`(59) |
| ui/composer | 16 | 4400 | 87 | `composer_menus_test.go`(627)、`verified_composer_test.go`(489)、`home_live_menus_test.go`(391)、`composer_mention_drill_test.go`(359)、`composer_mention_p1_test.go`(319)、`composer_slash_commands_test.go`(316)、`composer_slash_menu_test.go`(315)、`composer_mention_detail_test.go`(296)、`composer_session_refs_test.go`(285)、`home_attachment_test.go`(191)、`attachment_acceptance_test.go`(189)、`modes_ui_test.go`(182)、`composer_effort_test.go`(146)、`auto_review_ui_test.go`(140)、`composer_mention_quote_test.go`(82)、`composer_popup_escape_test.go`(73) |
| ui/toolcards | 1 | 409 | 9 | `toolcards_test.go`(409) |
| ui/settings | 7 | 2553 | 52 | `settings_ui_test.go`(912)、`models_ui_test.go`(449)、`settings_extra_test.go`(351)、`settings_update_hotkey_test.go`(330)、`presets_ui_test.go`(247)、`settings_popovers_test.go`(135)、`manage_ui_test.go`(129) |
| ui/plugins | 4 | 1074 | 15 | `plugin_details_test.go`(750)、`plugins_ui_test.go`(170)、`plugins_lsp_test.go`(119)、`plugin_budget_fields_test.go`(35) |
| ui/automation | 2 | 587 | 10 | `automation_ui_test.go`(456)、`automation_wave2_test.go`(131) |
| ui/memory | 1 | 960 | 7 | `memory_ui_test.go`(960) |
| ui/team | 1 | 152 | 2 | `team_ui_test.go`(152) |
| ui/goal | 1 | 552 | 8 | `composer_goal_compact_test.go`(552) |
| ui/plan | 1 | 832 | 12 | `plan_ui_test.go`(832) |
| ui/approval | 1 | 369 | 4 | `approval_ui_test.go`(369) |
| ui/todo | 2 | 256 | 8 | `artifact_actions_test.go`(166)、`question_card_ui_test.go`(90) |
| ui/workspaces | 4 | 1384 | 22 | `workspaces_test.go`(689)、`sidebar_hover_test.go`(375)、`workspace_ui_fixes_test.go`(277)、`workspace_actions_test.go`(43) |
| ui/files | 8 | 1893 | 50 | `start_tab_test.go`(368)、`right_sidebar_test.go`(362)、`right_sidebar_screens_test.go`(245)、`files_panel_test.go`(243)、`files_list_test.go`(229)、`files_preview_test.go`(188)、`files_root_test.go`(166)、`files_native_test.go`(92) |
| ui/diff | 2 | 330 | 8 | `workspace_changes_diff_ui_test.go`(215)、`workspace_changes_ui_test.go`(115) |
| host/chrome | 12 | 2808 | 58 | `motion_test.go`(509)、`responsive_panels_test.go`(413)、`preferences_test.go`(385)、`dark_contrast_test.go`(343)、`i18n_test.go`(312)、`chrome_test.go`(247)、`assets_test.go`(164)、`menus_test.go`(120)、`session_restore_test.go`(112)、`toast_placement_ui_test.go`(102)、`scrim_test.go`(68)、`buildinfo_test.go`(33) |
| lib/uikit | 3 | 726 | 17 | `markdown_test.go`(321)、`popover_menus_test.go`(272)、`popover_menus_rest_test.go`(133) |
| cross/integration | 16 | 6074 | 111 | `closure_acceptance_test.go`(916)、`verifier_probe_test.go`(829)、`a11y_native_test.go`(819)、`verified_ui_test.go`(661)、`visual_regression_test.go`(430)、`fixer_regression2_test.go`(424)、`fixes_shell_test.go`(354)、`fixer_regression_test.go`(349)、`a11y_fixes_test.go`(342)、`render_bench_test.go`(333)、`a11y_expanded_test.go`(173)、`a11y_test.go`(125)、`retired_features_absence_test.go`(120)、`a11y_extra_test.go`(111)、`livetest_helpers_test.go`(50)、`interaction_test.go`(38) |

### 1.4 模块度量（按文件唯一归属；`modules.tsv` 完整版）[A]

| 归属 | 生产文件 | 生产 LOC | App 方法 | 测试文件 | 测试 LOC | Ctrl 调用 | Store 链 | 触达 App 字段 | 内部包直接依赖 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| ui/home | 1 | 503 | 14 | 3 | 497 | 11 | 0 | 9 | design/icons/session |
| ui/session | 9 | 3,277 | 68 | 15 | 3,609 | 25 | 3 | 7 | design/icons/session/sessionreference/state/store |
| ui/composer | 8 | 3,594 | 103 | 16 | 4,400 | 51 | 4 | 20 | design/icons/modelcatalog/runtime/session/sessionreference/state/store |
| ui/toolcards | 1 | 704 | 14 | 1 | 409 | 0 | 0 | 0 | design/icons/state |
| ui/settings | 7 | 4,374 | 142 | 7 | 2,553 | 43 | 12 | 30 | brand/design/globalhotkey/icons/modelcatalog/presets/session/state/store/updater |
| ui/plugins | 3 | 1,563 | 51 | 4 | 1,074 | 16 | 10 | 6 | design/runtime/session |
| ui/automation | 2 | 874 | 35 | 2 | 587 | 15 | 4 | 4 | automation/design/store |
| ui/memory | 1 | 832 | 40 | 1 | 960 | 3 | 2 | 2 | design/session/store |
| ui/team | 1 | 229 | 10 | 1 | 152 | 4 | 0 | 3 | design/tools |
| ui/goal | 1 | 218 | 8 | 1 | 552 | 7 | 0 | 1 | runtime/session |
| ui/plan | 1 | 475 | 16 | 1 | 832 | 12 | 0 | 1 | design/icons/state/tools |
| ui/approval | 0（carve-out ≈90） | 0 | 0 | 1 | 369 | 0 | 0 | 0 | design/state |
| ui/todo | 1 | 379 | 12 | 2 | 256 | 5 | 0 | 1 | design/tools |
| ui/workspaces | 4 | 2,711 | 94 | 4 | 1,384 | 66 | 16 | 46 | design/icons/sessionreference/state/store |
| ui/files | 5 | 2,216 | 65 | 8 | 1,893 | 11 | 7 | 17 | design/icons/store/winproc |
| ui/diff | 1 | 249 | 8 | 2 | 330 | 0 | 0 | 2 | design/state |
| **宿主 chrome** | 11 | **2,745** | 89 | 12 | 2,808 | 26 | 9 | 56 | design/i18n/icons/session/state/store/tools |
| **lib/uikit** | 2 | **286** | 10 | 3 | 726 | 0 | 0 | 0 | design |
| **合计（唯一归属）** | **59** | **25,229** | **779** | **100** | **29,465** | **295** | **67** | **122** | — |

### 1.5 复现命令 [A]

```bash
cd /data/mygo
P=.probe/research/kernel-split/ui-map
go run $P/inventory.go > $P/files.tsv      # 文件/LOC/App方法/sync.Map
go run $P/funcs.go     > $P/funcs.tsv      # 979 个函数/方法的 file:line
go run $P/analyze.go   > $P/coupling.tsv   # Ctrl/Store/imports/App字段
go run $P/state.go     > $P/state.tsv      # 19 个包级 sync.Map 与状态结构
go run $P/appfields.go > $P/appfields.txt  # App 123 字段
python3 $P/rollup.py   > $P/modules.tsv    # domains/tests.tsv 汇总 + 断言（25229/29465/779/100）
```

## 2. 现状：一个入口、一帧、一个 God object

### 2.1 渲染主链路（宿主 chrome 的形状）[B]

`shell.App.View`（`app.go:303`）是唯一入口：`ui.View` 每帧调用一次，帧内按固定顺序做「帧前准备 → 主题 → toast 层 → 左栏 + 主屏 + 右栏 → settings overlay → 顶层 overlay」。`mainScreen` switch 只认 4 个目的地（`app.go:502-514`），Settings 是 overlay 而非目的地（`backgroundScreen`，`app.go:432-442`）。每帧都会重新执行：`restoreSessionState`、`installTaskShortcuts`、`installChromeShortcuts`（`app.go:313-324`）——**快捷键 handler 是每帧重新注册的**（`shortcuts.go:18-24` 注释），这是 UI 模块「卸载即不注册」的天然基础。

### 2.2 God object 的量化 [A]

| 面 | 数字 | 证据 |
| --- | --- | --- |
| `App` 字段 | 123（45 bool、37 string、10 func seam、4 map、3 Handle…） | `appfields.go`；`app.go:29-291` |
| 包级 `sync.Map`（`map[*App]*X`） | **19**，18 个状态 struct = 159 字段（含 1 个 map 型） | `state.tsv` |
| App 字段被 ≥2 个 UI 模块触达 | **20 个**（`Ctrl` 13 个模块、`Screen` 6、`rightOpen` 5、`composerRef`/`attachOpen` 4、`wsPickerOpen`/`rightTab`/`draft` 3…） | `rollup.py` 的 APP_FIELDS 行 |
| 被「模块+宿主」共同触达 | **38 个** | 同上 |
| 模块独占字段 / 宿主独占字段 | 66 / 18 | 同上 |
| 直接 `Ctrl.X` 方法/调用点 | 72 / 295 | `coupling.tsv` |
| 每帧重建 + 包级状态的组合后果 | 多实例/测试隔离不成立；模块卸载无法回收状态（key 是 `*App`，没有 delete） | `state.go` 19 个 map 无 `Delete` |

### 2.3 mygo 的线程/帧契约对「UI 模块」的硬约束 [B]

- `ui.Context` 只能在 UI 线程构建帧时使用；后台结果通过 `Window.Update(fn)`（`mygo/content.go:172`）或 `Context.Invalidate/services.Invalidate`（`mygo/ui/frame.go:41`、`mygo/ui/services.go:24`）发布。**UI 模块不能持有一个「后台渲染线程」**。
- 所有窗口共享一个主线程事件循环（`mygo/loop.go:7-60`、`isMainThread`）——settings 独立窗口与主窗口的 Content **在同一 UI 线程上交替构建**，共享同一个 `*App`。
- 因此 UI 模块的 kernel 生命周期只能是：**启动期（UI 线程）Activate 注册贡献 → 每帧宿主读取贡献表调用渲染函数 → 关闭期（UI 线程）Dispose 逆序注销**。渲染函数本身是**纯即时模式函数**（可返回 `ui.Element`），不得跨帧持有 `ui.Element`/`ui.Handle`（`Handle` 只在帧内有效，需模块在帧内重新绑定）。该契约即是 04 §0.1 的八条硬约束（帧/窗口属宿主、回调帧内且一帧多次、Context/Element 过期、Post+Invalidate、主题/toast/overlay 归宿主、状态归模块、注册仅启动期、多窗口共享模块）；06 spike 已用假 frame + 线程纪律断言（24 条 UI 操作同线程）证明其在 kernel 上可执行。

### 2.4 DSH client-ui 的对照 [B]

DSH 的 57 个 `client-ui` 包用 `dsh.client.inject` 声明浏览器侧服务依赖、通过 slot 注册挂进布局、没有中心路由表（02 §2.4/§4.5）。mygo 侧对应物不是浏览器 bundle，而是**同一二进制内的注册表 + 每帧组合**：screen/slot/settings page/tool renderer/command/overlay 六类贡献（04 §5 与 06 spike 的最小 API）。区别（须在 00/03 中写明）：无动态加载、无 HMR、无跨进程；注册表在 link-time 就固定。

## 3. 贡献点清单（现状位置 → 数据来源 → 卸载清理 → 目标归属）

> 表中「清理」指模块 Dispose/关闭时必须撤掉或搬走的东西；渲染回调本身不跨帧，无需反注册句柄，但**贡献注册、包级 map、窗口/ctx/goroutine、持久化草稿**必须处理。另：03 §4.2 曾记「15 个 settings Tab」，实测为 **14 页**（General/Models/Memory/BuiltinPlugins/AgentPresets/Notifications/Data&Privacy/Diagnostics/Network/Desktop/Updates/MCPServers/Skills/About，枚举见 `models_ui.go:26`、`settings_extra.go:29-34`、`memory_ui.go:32`、`plugin_details.go:26`、`manage_ui.go:25-26`），本报告按 14 计。另：任务书把 approval/plan/goal/diff 列入「overlays/modals」，**现状并非 overlay**——plan/approval/question 都画在 composer 上方 slot（`liveComposerArea` 的绘制顺序：plan → question → approval → palette → composer，`session_live.go:874-912`），goal 是 session header 下方 banner（`team_ui.go:117`），diff 是 transcript 尾部卡片（`session_live.go:286`）；迁移后统一为 slot 贡献，不进入模态栈。

| # | 贡献点 | 现在实现位置（file:line） | 初始状态来源 | 卸载需清理项 | 归属 |
| ---: | --- | --- | --- | --- | --- |
| 1 | 顶层 screen（Home/Session/Plugins/Automation；Settings 为 overlay） | `app.go:17-26`（枚举）、`app.go:502-514`（switch）、`app.go:432-442`（背景屏） | `App.Screen`（`app.go:30`）、`settingsReturn`（`:35`） | Navigator 根重置；无持久状态 | 宿主 nav + 各屏模块 |
| 2 | 左栏导航项 / 工作区树 / 搜索 / 视图选项 | `app.go:522-549`（容器）、`workspaces.go:185-193`（3 项 nav）、`:195-244`（Workspaces 头条+搜索/视图/加号）、`:279-...`（树）；`sidebar_hover.go:26` | `sidebarSel`/`wsCollapsed`/`groupBy`/`archiveFilter`/`workspaceOrder`/`sessionOrder`/`pinnedSessions`（`app.go:217-290`）、Ctrl.ListWorkspaces/SessionsForWorkspace、设置键 `ui.*` | 注销 sidebar 槽位；清 `wsCollapsed` 等映射；取消会话列表异步缓存（`sessions_cache_async.go:77` goroutine 结果只写缓存，须标记失效） | 壳 = 宿主；项/树 = ui/workspaces |
| 3 | settings 页（14 页） | `settings_ui.go:347-419`（`settingsNavLive`）、`:256-273`（`settingsPanelLive`）、`:421-475`（`settingsBodyLive` 页 switch）；枚举散落 5 文件：`models_ui.go:23-30`、`settings_extra.go:29-34`、`manage_ui.go:25-26`、`memory_ui.go:32`、`plugin_details.go:26` | `shellUIState.settingsTab`（`models_ui.go:247` + `shellUIState` 定义 `:112`）、`currentSettings` 缓存（`settings_ui.go:35`）、选择器开关 `app.go:120-127` | 注销 SettingsPage 贡献（含 nav 图标/顺序/可见条件）；清选择器开关；设置缓存归宿主 settings 服务 | ui/settings（14 页中的 11 页）；Memory→ui/memory；Builtin plugins/About→ui/plugins |
| 4 | 独立 settings 窗口 | `settings_ui.go:274-314`（`SettingsView`）、`:316`（标题）；宿主 `internal/app/settings_window.go:231-258`（`initSettingsWindow`）、`:260-282`（Open/fallback）、`:296-305`（双窗重绘） | 同一个 `*App`；窗口单例 + `StateKey`；`shellUIState.settingsTab` | 宿主关闭/隐藏窗口并清 `settingsWindowOpen/Close`、`settingsChangedHook`（`app.go:104-108`）；模块不得自己持窗口 | chrome（窗口宿主）+ ui/settings（内容） |
| 5 | tool cards / tool renderer | `toolcards.go:111`（`liveToolCards`）、`:119`（`liveToolCard`）、`:212`（`toolResultStates`）；调用点 `message_actions.go:385`；默认展开 `settings_ui.go:185-224`（`applyToolCardDefaults`，由宿主 `app.go:505` 调） | `state.Snapshot.Tools`；`liveState.expanded`（`motion.go:43`）；设置 `WorkDetails`/`CollapseWorkDetail` | 清 `toolResultStates`/`liveState.expanded`/`liveMenuState.doneSeen`；注销 ToolRenderer；`applyToolCardDefaults` 改由宿主服务在帧前准备（不再由 screen switch 调） | ui/toolcards |
| 6 | transcript / message 渲染 | `session_live.go:247`（`liveTranscript`）、`:439`（ordered）、`:690`（`liveMessage`）、`transcript_order.go:118`（`buildTranscriptTurns`） | `Snapshot.Messages/Seq/TurnMarks`；`sessionRenderStates`（`:35`，滚动）；`liveUIState`（`trajectory.go:76`） | 清滚动状态；注销 transcript 槽（turn 行/消息行/空态/回底按钮）；reasoning/reference 子槽随模块 | ui/session |
| 7 | composer（Home+Session 共享） | Session：`session_live.go:874-944`（`liveComposerArea`/`sessionComposerCard`）；Home：`home.go:271-357`；共享件 `composer_native.go:20/35/91`；palette `composer_slash.go:676`；mention `composer_menus.go:605` | `App.draft/drafts/homeDraft/homeAttachments/attachOpen/composerRef/mentionRef`（`app.go:57-82,176-177`）；`slashMenuStates`（`composer_slash.go:167`）；`liveMenuStates`（`home_live_menus.go:97`）；`Ctrl.Submit/AttachFiles/…` | 注销 Composer 服务与命令/菜单贡献；**持久化后清草稿状态**；解绑 focus Handle；取消 caret/deferred toast 状态 | ui/composer（提供 `app/ui/composer` 服务） |
| 8 | 右栏 tabs（Start/Files/Terminal/Session log） | 枚举 `right_sidebar.go:23-33`；容器 `:165-197`；tab switch `:185-194`；内容 `:456`（files）、`:989`（terminal）、`start_tab.go:95`（start）、`:382`（session log） | `rightOpen/rightTab`（`app.go:245-246`）；`rightPanelStates`（`:101`）；`startTabStates`（`start_tab.go:61`）；`termCancel`（`app.go:257`） | 宿主保留面板/tab strip/resize/split/fullscreen；模块注销 TabRenderer；**取消 `termCancel`**、清 `dirCache`/preview/`filesNativeStates` | 壳 = 宿主；内容 = ui/files |
| 9 | overlays/modals（通用） | 栈 `session_search.go:185-213`（`shellOverlays`：about/shortcuts/rename/delete/wsDialog/wsDelete/palette）；Esc 链 `:135-158`（`closeTaskTopmost`）、`:162-183`（`closeOneTaskOverlay`）；settings overlay `settings_ui.go:225-253`；各页 `ui.Modal` 20+ 处 | 各自的 App bool（`app.go:120-170,218-241`）；`pluginUIState`（`plugin_details.go:63`）；`memoryPanels`（`memory_ui.go:74`）等 | 注销 Overlay 贡献与 Esc 优先级；关闭时清各 dialog 状态；宿主保留 z-order/模态抢占/点击穿透 | 栈 = 宿主；overlay 条目 = 模块 |
| 10 | toasts | viewport `app.go:394-427`（top band、pass-through）；延迟队列 `composer_slash.go:1044`（queueToast）、`:1052`（flushDeferredToast）；直接 `c.Toast` 124 处 | mygo toast 引擎（帧内入队）；`slashMenuState.deferredToast`；`chromeToast`（`chrome.go:377`） | 宿主保留 viewport（主窗口 + settings 窗口各一层）；模块清 deferred toast 字段；不得自建窗口层 | 宿主 viewport + 模块发起 |
| 11 | 快捷键 | 窗口绑定 `shortcuts.go:25`（installShortcuts）、`session_search.go:32-47`（installTaskShortcuts，12 项）、`chrome.go:41`（installChromeShortcuts）；注册 `shortcuts.go:281`（bindShortcut）、`chrome.go:68`（overlayShortcut）；13 行默认表 `shortcuts.go:171-189`；覆盖键 `keybinding.<id>` | 设置存储 `keybinding.*`（`shortcuts.go:202-252`）；每帧重新注册 | 停止帧内注册即卸载；清 override 缓存（字段 `app.go:133-139`；加载 `shortcuts.go:205-224`）；**跨模块冲突须由宿主冲突检测**（例：`Cmd+K` 会话内聚焦 composer vs 非会话态开会话搜索，`shortcuts.go:81-89`） | 宿主 Shortcut 服务 + 模块注册命令 |
| 12 | 命令/菜单 | slash 目录 `composer_slash.go:68-155`（16 条）+ 分发 switch `composer_menus.go:253-300`；菜单表 `shortcuts.go:350-390`（7 条 `MenuAction*`）；`HandleMenuAction` `shortcuts.go:422`；托盘菜单 `internal/app/app.go:694-716`（窗口菜单栏已按 ADR 0006 退役） | 命令目录是静态数组；`HandleMenuAction` 由宿主导航/打开 settings 等；托盘是进程级入口 | 注销 Command 贡献（id/分组/handler）；宿主保留导航类命令（new/settings/search/sidebar/quit）；命令处理器回到各特性模块（`/goal`→ui/goal、`/memory`→ui/memory、`/plan`→ui/plan…） | 宿主 Command 服务 + 特性模块贡献 |
| 13 | Home 区块 | `home.go:468`（header/右栏按钮）、`:173`（hero）、`:100`（setup card）、`:205`（chips）、`:271`（composer 宿主块）、`:298`（composer card）、`:341`（toolbar） | `homeDraft/homeAttachments/homeMode/homePermission/homeModel/homeEffort`（`app.go:57-79`）、`homeAttachmentSeq`、`Ctrl.HasConfiguredModel/CurrentWorkspace/CreateSession` | 注销 Home screen 与 home 槽；清 home 草稿/附件（先持久化 policy：Home draft 属未创建会话，直接丢弃并提示）；依赖 ui/composer 服务 | ui/home |
| 14 | approval 卡 | `composer_menus.go:76-81`（`pendingApprovals`）、`:184-217`（`approvalCards`）；`session_live.go:945-991`（`approvalCardsWithAlways`）；Esc 先行 deny `session_search.go:150-153` | `Ctrl.PendingApprovals()`（后端）；`state.ApprovalRequest`；无本地 map | 注销 composer.approval 槽与 Esc 优先级条目；**不持有审批状态**（后端 pending 集是唯一真相） | ui/approval（carve-out） |
| 15 | plan 评审卡 + 模式徽标 | `plan_ui.go:48`（`planUIStates`）、`:332`（`composerPlanBadge`）、`:394`（`planReviewCard`） | `tools.Artifact`（plan delivery）+ `Ctrl.PlanMode/SetPlanMode`；`planUIState` | 注销 composer.plan-review 槽；清 `planUIStates`；plan 模式本身归后端 session 状态 | ui/plan |
| 16 | goal banner + team panel | `team_ui.go:15-20`（`goal`）、`:117-163`（`teamGoalBanner`）、`:34-75`（`teammates`）、`:164-...`（`teamPanel`）；钩子 `:22-31`（SetTeamRoster/SetTeamStop）；`composer_goal_compact.go:40-...`（`runGoalCommand`，/goal 命令） | `Ctrl.Goal()/Team()/Stop()`；App `teamRoster/teamStop` 函数 seam；`composer_goal_compact.go` 的 `Ctrl.GoalAction/GoalRounds/CompactCurrent` | 注销 session.goal-banner / session.team 槽；**置空宿主注入的两个 hook**；命令 `/goal` 注销 | ui/team + ui/goal（goal banner/命令单独成模块） |
| 17 | todo / question / artifact | `todo_ui.go:44`（todo 面板）、`:142`（question cards）、`:273`（artifacts） | `Ctrl.Todos()/Questions()/Artifacts()/AnswerQuestion`；`shellUIState.questionAnswered`、`liveUIState.questionDraft` | 注销 session.todo-panel/composer.question-cards/transcript.artifacts 槽；清 questionAnswered/questionDraft | ui/todo |
| 18 | memory 面板/设置页 | `memory_ui.go:74`（`memoryPanelStates`）、`:107`（settings 页）、`:129`（session memory pane）、`:170`（manager） | `Ctrl.Store()`（memory 表）、`CurrentWorkspace`；`memoryPanels` 状态 | 注销 SettingsPage "Memory" + session.memory-pane 槽；清 dialog map | ui/memory |
| 19 | Files/Start/Terminal/Session log 内容 | `right_sidebar.go:456`/`:989`；`files_root.go`、`files_native.go`、`files_preview.go`；`start_tab.go:95`/`:382` | 工作区根（`Ctrl.CurrentWorkspace`）、`store` artifacts/events、`winproc`；`filesNativeStates`、`rightPanelState.dirCache` | 注销 TabRenderer；取消终端 ctx；清目录/预览缓存 | ui/files |
| 20 | 本轮变更 diff 卡 | `workspace_changes_ui.go:35`（`workspaceDiffStates`）、`:60`（`liveWorkspaceChanges`）、`:164`（`workspaceDiffPanel`） | `Snapshot.WorkspaceChanges/WorkspaceChangesTurn` | 注销 transcript.turn-changes 槽；清 expand map | ui/diff |
| 21 | referenced-session 折叠行 | `session_live.go:741`（`referenceContextOpen` map）、`:780`（`liveReferenceContextRow`）；标签 `:747-776` | `sessionreference.SummarizeContext` + `Snapshot.Messages` | 清 `referenceContextOpen` map；作为 ui/session 内部子槽（不单独成模块，见 §4.3） | ui/session |
| 22 | 会话搜索（Cmd+K 模态）/内联搜索/视图选项 | `session_search.go:524`（`sessionSearchDialog`）、`:215`（`openInlineSearch`）、`:460`（`viewOptionsPopover`）、`:789`（mention 会话候选） | `paletteOpen/paletteQuery/inlineSearch/inlineQuery/searchIndex`（`app.go:140-141,239-242`）；`Ctrl.SearchSessions`；`mentionSessionCaches`（`:780`）；设置 `ui.group_by/archive_filter` | 注销 search overlay + mention candidates 贡献；清 cache map；Esc 优先级 | ui/workspaces（导航类）+ ui/composer（mention 候选） |

## 4. 16 个 UI 模块提案

### 4.1 总表

> LOC 为文件唯一归属（不含 carve-out）；「波次」见 §7；人日为 [C]（含该模块测试迁移，不含 Wave 0/收尾）。模块 id 与 kernel `module.ID` 的建议值（前缀 `mygo-ui/`，报告内简写）。「依赖的 UI 服务」列中 Screen/Slots/SettingsPages/ToolRenderers/Commands/Overlay 均指 **Runtime 上的注册表方法**（04 §5.2：`RegisterScreen/RegisterSlotItem/RegisterCommand/RegisterToolRenderer/RegisterSettingsPage`，overlay 用 `overlay.*` slot 表达），只有 `app/ui/navigator`、`app/ui/theme`、`app/ui/locale` 与 `app/ui/composer`（由 ui/composer **模块提供**）是独立服务；toast 无独立服务键（帧内 `c.Toast`，帧外延迟由模块自持，04 §3-10）。

| # | id | 职责 | 迁移文件（LOC） | 注册的贡献 | 依赖的 UI 服务（04 §5） | 依赖的后端服务（03/04 草案） | 波次 | 人日 |
| ---: | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | ui/home | Home 屏幕：hero/setup/chips/composer 宿主 | `home.go`(503) | screen `home`；slots `home.*`；depends composer | UIRuntime、Navigator、Composer、Theme、Locale | `app/sessions`（HasConfiguredModel/CurrentWorkspace/CreateSession/OpenSession/Submit）、`app/workspaces` | W3 | 3–4 |
| 2 | ui/session | Session 屏幕：header/tabs/transcript/trajectory/errors/references/diff 槽 | `session.go`(43)、`session_live.go`(1131)、`trajectory.go`(612)、`message_actions.go`(387)、`reasoning_ui.go`(437)、`transcript_order.go`(264)、`session_errors.go`(165)、`session_open_async.go`(144)、`session_workspace.go`(94) | screen `session`；slots `session.header`、`session.transcript.*`、`session.composer-area`；消费 ToolRenderer/PlanReview/Approval/Todo 槽 | UIRuntime、Navigator、Composer、ToolRenderers、Theme、Locale、Toast、Motion | `app/sessions`（Open/Current/Stop/Submit/Wait… 读模型）、`app/state`（Snapshot） | W4 | 13–17 |
| 3 | ui/composer | composer 组件 + slash/mention/palette + 草稿作用域 + 附件 chips + 模式/权限/模型菜单 | `composer_slash.go`(1060)、`composer_menus.go`(996)、`home_live_menus.go`(622)、`attachments_ui.go`(272)、`draft_scope.go`(234)、`modes_ui.go`(176)、`composer_native.go`(121)、`composer_style.go`(113) | provides `app/ui/composer`；slots `composer.toolbar/menus/chips`；commands（/clear /file /feedback /permission /export /help /new /settings /automate /plugins /model /memory?…按归属拆分） | UIRuntime、Theme、Locale、Commands、Toast、Motion | `app/sessions`（Submit/AttachFiles/SetSessionPermission…）、`app/settings`、`app/models` | W4 | 15–19 |
| 4 | ui/toolcards | 工具卡渲染器（全部内置工具 variant）+ 结果面板 | `toolcards.go`(704) | ToolRenderer（tool name→renderer）；slot `transcript.tool-card` | ToolRenderers、Theme、Locale | `app/state`（Snapshot.Tools）、`app/settings`（WorkDetails，只读） | W1 | 2–3 |
| 5 | ui/settings | 设置壳（nav/body/overlay/SettingsView）+ 通用页与模型/预设/MCP/Skills/更新/桌面页 | `models_ui.go`(1055)、`settings_ui.go`(1033)、`settings_extra.go`(703)、`presets_ui.go`(500)、`settings_update_hotkey.go`(440)、`manage_ui.go`(439)、`settings.go`(204) | SettingsPage×11（其余 3 页由 ui/plugins 的 Builtin/About 与 ui/memory 的 Memory 贡献）；settings overlay（fallback）+ 独立窗口 Content；menu action `settings` | UIRuntime、SettingsPages、Theme、Locale、Overlay、Toast | `app/settings`、`app/models`、`app/presets`、`app/updater`、`app/globalhotkey`、`app/mcp`、`app/skills` | W1+W2 | 10–13 |
| 6 | ui/plugins | Plugins 屏幕 + 内置插件设置页 + 插件详情/About 对话框 | `plugin_details.go`(858)、`plugins.go`(393)、`plugins_ui.go`(312) | screen `plugins`；settings page `builtin-plugins`、`about`；dialogs；command `/plugins` | Screen、SettingsPages、Theme、Locale、Overlay | `app/settings`（`plugin.<name>.enabled`、SetPluginEnabled/RebuildAgent）、`app/runtime`（plugin 信息） | W1 | 4–6 |
| 7 | ui/automation | Automation 屏幕 + 任务对话框 + 运行历史 | `automation_ui.go`(828)、`automation.go`(46) | screen `automation`；command `/automate`；dialogs | Screen、Theme、Locale、Toast、Overlay | `app/automation`（Automations store、RunNow）、`app/settings` | W1 | 3–4 |
| 8 | ui/memory | 记忆管理器（设置页 + 会话 Memory 面板） | `memory_ui.go`(832) | settings page `memory`；session slot `session.memory-pane`；command `/memory` | SettingsPages、Slots、Commands、Theme、Locale | `app/memory`（store memory API）、`app/workspaces` | W2 | 4–5 |
| 9 | ui/team | 团队面板 + teammate 行 | `team_ui.go`(229) | session slot `session.team`；App 注入 `teamRoster/teamStop` 改为 host service | Slots、Theme、Locale、ReadModel | `app/teams`（live roster）、`app/sessions`（Ctrl.Team/Stop） | W2 | 2–3 |
| 10 | ui/goal | goal banner + `/goal` 命令 + `/compact` 命令 | `composer_goal_compact.go`(218) + carve-out `team_ui.go:15-20,77-86,117-163`(≈70) | session slot `session.goal-banner`；commands `/goal`、`/compact` | Slots、Commands、Toast | `app/sessions`（Goal/GoalAction/GoalRounds、CompactCurrent） | W2 | 2–3 |
| 11 | ui/plan | plan 评审卡 + plan 模式徽标 + `/plan` 命令 | `plan_ui.go`(475) | slot `composer.plan-review`；slots `composer.plan-badge`；command `/plan` | Slots、Commands、Theme、Locale | `app/sessions`（PlanMode/SetPlanMode/Submit）、`app/state`（artifacts） | W2 | 3–4 |
| 12 | ui/approval | 审批卡（Allow/Always/Deny）+ Esc 优先 deny | carve-out：`composer_menus.go:76-81,184-217` + `session_live.go:945-991`（≈90 行） | slot `composer.approval`；Esc priority contribution | Slots、Theme、Locale | `app/sessions`（PendingApprovals/Approve/ApproveAlways/Deny） | W2 | 1.5–2 |
| 13 | ui/todo | Todo 侧板 + ask_user 卡片 + presented artifacts | `todo_ui.go`(379) | slots `session.todo-panel`、`composer.question-cards`、`transcript.artifacts` | Slots、Theme、Locale | `app/sessions`（Todos/Questions/Artifacts/AnswerQuestion）、`app/files`（artifact 预览） | W2 | 2–3 |
| 14 | ui/workspaces | 侧栏导航项/工作区树/悬停卡/排序/视图选项/会话搜索/内联搜索 | `workspaces.go`(1504)、`session_search.go`(943)、`sessions_cache_async.go`(137)、`sidebar_hover.go`(127) | sidebar slots（nav/workspaces-header/tree/search）；overlay `session-search`；slots `sidebar.view-options`；command search | UIRuntime（sidebar slot）、Navigator、Overlay、Theme、Locale、Search | `app/workspaces`、`app/sessions`（List/Search/Archive/Pin/Order）、`app/settings`（ui.* 排序键）、`app/store` | W3 | 10–13 |
| 15 | ui/files | 右栏内容 tabs：Start/Files/Terminal/Session log | `right_sidebar.go`(1232，内容段≈777)、`start_tab.go`(524)、`files_root.go`(276)、`files_native.go`(109)、`files_preview.go`(75) | TabRenderer×4（`rightpanel.tab` slot）；files 原生动作 | Slots、Theme、Locale、Toast | `app/files`（workspace root/native open/reveal/artifacts）、`app/terminal`（exec）、`app/sessions`（events/JSONL） | W3 | 8–11 |
| 16 | ui/diff | 本轮变更文件卡 + unified diff 渲染 | `workspace_changes_ui.go`(249) | slot `transcript.turn-changes`；diff renderer（供 Files/artifact 复用） | Slots、Theme、Locale | `app/state`（WorkspaceChanges）、`app/files`（打开文件） | W1 | 1.5–2 |

### 4.2 模块边界说明（每模块一行结论）

- **ui/home**：只画 Home 屏与 home 槽；输入框用 ui/composer 提供的 `Composer` 服务（Home 变体），不得复制 slash/mention 逻辑；Home 草稿状态随模块走（`homeDraft/homeAttachments/home*` 选择）。
- **ui/session**：最大模块（68 方法/3,277 行）。 owns 屏幕骨架、transcript 排序（`transcript_order.go` 是纯函数、0 个 App 方法，可先搬 `uikit/list`）、消息行、错误卡、reasoning、trajectory。**不 owns** 工具卡（ui/toolcards 槽）、diff（ui/diff 槽）、plan/approval/todo/goal/team（各自槽）。
- **ui/composer**：跨 Home/Session 的唯一输入组件；provides `app/ui/composer` 服务（返回 composer 元素 + 绑定 `composerRef`），Home/Session 只调用不复制。命令目录 `slashCommands` 作为**宿主 Command 服务的数据**，命令处理器按 §4.3 拆给特性模块。
- **ui/toolcards**：唯一职责是「tool name → 渲染器」；输入只有 `state.ToolState` + 只读设置；`applyToolCardDefaults`（当前由 `mainScreen` 驱动）必须上移为「帧前 ToolRenderers 预处理」或由 ui/session 在渲染前调用模块服务，不能继续由宿主 screen switch 直调。
- **ui/settings**：设置壳 + 14 页中的 11 页（General/Notifications/Data&Privacy/Diagnostics/Network/Desktop/Updates/Models/AgentPresets/MCP/Skills）以及独立窗口内容；**其余 3 页按贡献点拆给 ui/plugins（Builtin plugins/About）、ui/memory（Memory）**，ui/settings 只保留页面注册与渲染 API，不硬编码 switch。
- **ui/plugins / ui/automation**：两个最像「一屏一模块」的叶子；数据源是各自后端服务（plugin 开关/调度器），没有跨屏状态，适合作为首批 strangler 试点。
- **ui/memory**：设置页 + 会话 Memory 面板共享同一 `memoryManager`（`memory_ui.go:170`），两个贡献（SettingsPage + session slot）同模块最自然；数据直连 `Ctrl.Store()`（64 处 Store 链之一），迁移时先用 `app/memory` 服务包装。
- **ui/team / ui/goal**：`team_ui.go` 同时含 team 与 goal banner（历史原因），按「文件可拆、贡献可分」原则拆成两个模块；goal 的 `/goal /compact` 命令与 banner 同模块，team 的 `teamRoster/teamStop` 是宿主注入 seam，必须变成 `app/teams` 服务（该模块不得让 App 持函数指针）。
- **ui/plan / ui/approval**：都是 composer 上方 slot，数据全在后端（plan artifact / pending approvals）；模块**无状态**（plan 只有 expand/decide 的 UI 记忆 `planUIState`），是「贡献点模型」的最佳演示。
- **ui/todo**：三个贡献（todo 侧板 / question 卡 / artifacts）共享 `Ctrl.Todos/Questions/Artifacts`；artifacts 的「预览到 Files」跨模块调用应走 `app/files` 服务而非直接改 `rightTab`。
- **ui/workspaces**：最重的导航模块（94 方法/2,711 行，含会话搜索 943 行）。owns 侧栏内容槽与搜索 overlay；**不 owns** 侧栏容器、品牌栏、新建会话按钮、More 菜单（宿主 chrome）。`session_search.go` 需三方切分：宿主段（1-213 行：任务快捷键 + Esc 链 + overlay 栈）、workspaces 段（215-780：内联搜索/视图选项/会话搜索）、composer 段（780-943：mention 候选）。
- **ui/files**：右栏内容四个 TabRenderer；`right_sidebar.go` 需内切：1-455 行（面板/tab strip/resize/split/fullscreen/header）留宿主，456-1232 行（filesPanel/terminalPanel）归模块；`start_tab.go` 的 terminal shell 探测（`detectTerminalShells`）是进程 I/O，须经 `app/terminal` 服务。
- **ui/diff**：最小模块（249 行/0 Ctrl 调用），只依赖 Snapshot，是 Wave 1 首个可交付模块，也验证「turn-footer 槽」模型。

### 4.3 不进 16 模块的候选（及理由）

| 候选 | 处置 | 理由 |
| --- | --- | --- |
| ui/references | 并入 ui/session 的内部子槽 | 只有约 150 行（`session_live.go:741-838`）、一个数据源（sessionreference.SummarizeContext）、一个消费者；独立模块的注册开销大于收益，且它必须与 transcript 行顺序同步 |
| ui/commands | 不建模块；拆成「宿主 Command 服务 + 各特性模块注册命令」 | 16 条 slash 命令的处理函数已分散在 6 个文件/模块（`/goal`、`/memory`、`/plan`、`/model`、`/file`…），中心 switch（`composer_menus.go:253`）正是要消灭的东西；命令 UI（palette）归 ui/composer |
| ui/markdown-kit | 不进模块，进 `internal/uikit`（见 §6） | 无状态、被 session/toolcards/settings/artifacts 复用；不是可装配单元，不需要生命周期 |
| settings 独立窗口 | 窗口宿主留 `internal/app` | 创建/隐藏/聚焦/多窗口重绘是进程生命周期能力（`internal/app/settings_window.go`），UI 模块只提供 Content（`SettingsView`） |
| 宿主 chrome | 见 §5 | frame/布局/侧栏壳/overlay 栈/toast/快捷键/主题/语言 = 所有模块的公共前置，拆开会破坏主线程与 z-order 契约 |

## 5. 留在宿主 chrome 的部分（3,413 行 shell；理由与 LOC）

> **术语**：本报告的 **chrome = 窗口/应用外壳（app/window chrome）**，不是浏览器 Chrome，也不涉及
> webview/HTML——mygo 是纯 Go 原生即时模式 UI。chrome 是「承载内容、仲裁模态/焦点/z-order、持有帧
> 与窗口生命周期」的壳层；模块只提供被它调用的 builder。

### 5.1 清单

| 组件 | 现位置 | LOC | 留下的理由 |
| --- | --- | ---: | --- |
| frame / View 入口 / screen switch / settings 模态兜底 / toast 层 / 左栏容器 | `app.go:293-553` | 553 | 每帧唯一的组合根；决定「先 sidebar 后 mainScreen、Settings 作为 overlay、右栏 dock/split/fullscreen」顺序；模块只能被它调用，不能反向组合 |
| 标题栏（titlebar） | **无自绘代码**：原生窗口标题由 `mygo.NewWindow(windowOptions)` 提供（`internal/app/app.go:239`）、应用名 `mygo.App.SetName("ZeoKeel")`（`internal/app/app.go:175`）；屏幕内 session header（`session_live.go:163`）是内容不是窗口标题 | 0（shell） | 窗口标题/系统按钮是进程级窗口能力；与窗口菜单一样属 `internal/app`，拆进 UI 模块会让模块反向依赖窗口创建 |
| 布局/几何 helper | `layout.go:1-306` | 306 | 所有模块共享的宽度/断点策略（900x600 最小窗、右栏 45%、侧板 272）；放模块会形成环形依赖 |
| 侧栏壳：品牌栏/新建会话/折叠 rail/chrome 控件 | `chrome.go:1-379` | 379 | 侧栏是**所有屏**的公共容器；ui/workspaces 只贡献「项/树」槽内容 |
| 窗口菜单/快捷键/命令表 | `shortcuts.go:1-466` | 466 | 窗口级快捷键必须每帧在 `View` 内注册（`shortcuts.go:18-24`）；命令表是宿主与模块共享的词汇表 |
| More 菜单 / About / 快捷键对话框 / 重命名 / 删除确认 | `menus.go:1-354` | 354 | 都是叠加在任意屏上的 chrome 对话框（`shellOverlays` 调用），生命周期与 z-order 归宿主 |
| overlay 栈 + Esc 链 + 窗口任务快捷键 | `session_search.go:1-213` | 213 | 模态抢占、Esc「先关最上层」、overlay 注册顺序是宿主职责；模块只贡献可关闭条目与优先级 |
| 右栏/tab strip/resize/split/fullscreen（不含内容） | `right_sidebar.go:1-455` | 455 | 右栏是跨 Home/Session/Plugins/Automation 的 workspace 级面板（`right_sidebar.go:199-208`），容器必须由宿主跨屏持有 |
| 主题/外观 tokens | `preferences.go:1-151` | 151 | `ui.Context.Theme` + `design.Tokens` 每帧由宿主从桌面偏好/设置解析（`settings_ui.go:96-118`），模块只读 |
| 语言（locale） | `i18n.go:1-190` | 190 | `T/tr/lang` 解析每帧读设置缓存；目录已在 `internal/i18n`（552 行）；模块通过 Locale 服务取文案 |
| 动效运行态（running/stream/tool expand） | `motion.go:1-108` | 108 | 动画时钟与 `c.AnimationFrame()` 驱动属于帧生命周期；模块只消费状态 |
| 启动恢复（last session + draft） | `session_restore.go:1-126` | 126 | 进程启动一次性逻辑，先于任何模块激活 |
| 品牌资源 / 版本信息 | `assets.go`(55)、`buildinfo.go`(57) | 112 | `//go:embed` logo 与 `ldflags` 版本号是进程级只读输入 |
| **小计（shell 内，不含下面两行）** | | **3,413** | 占 25,229 行 **13.5%** |
| 设计系统（已独立包） | `internal/design` 733 生产行（+343 测试） | 733 | 只提供 tokens/style/motion 值；不属于任何 UI 模块 |
| 外部宿主（`internal/app`） | `settings_window.go`(306)+`window_geometry.go`(194)+`window_state.go`(125)+`quit.go`(29)+`app.go` 片段(≈110) | ≈764 | 窗口创建/菜单栏（已按 ADR 0006 退役，仅托盘 `internal/app/app.go:694-716`）/窗口状态/设置窗口单例——进程能力 |

### 5.2 UI 服务面（与 04 §5.1/§5.2 对齐）

> 04 的分层：`internal/contracts/ui`（契约包，只依赖 mygo/ui + design）→ `internal/host/ui`（Runtime/Navigator/Theme/Locale 实现 + 宿主 View）→ UI 模块（requires `app/ui` 等，只注册贡献）。kernel v0.1.1 的 `ExtensionPoint/Contribution` 只有文档未实现，**有序注册表/所有权/冲突检测这 100–200 行必须宿主自写**（04 §5.1）；06 spike 已给出可运行的 registry 形状。

| 服务/机制 | 提供方 | 职责（04 §5.2 方法；本报告别名） | 现状要包装的代码 |
| --- | --- | --- | --- |
| `app/ui`（Runtime） | **宿主 builtin**（`internal/host/ui`） | `Post`/`Invalidate`/`Services`/`Window` + `RegisterScreen`/`RegisterSlotItem`/`RegisterCommand`/`RegisterToolRenderer`/`RegisterSettingsPage`；确定性排序 + 重复身份检测 + `ctx.Effect` 逆序注销 | `app.go:303`（View）、`app.go:502`（screen switch）、`session_search.go:188`（overlay 栈）、`shortcuts.go:422`（命令分派）、`settings_ui.go:421`（settings 页 switch） |
| `app/ui/navigator` | **宿主 builtin** | `Current`/`Push`/`Back`/`CanGoBack`/`SetRoot`；settings 窗口固定根 | `app.go:432-459`（backgroundScreen/openSettings/closeSettings）、`app.go:502-514` |
| `app/ui/theme` | **宿主 builtin** | `Tokens(c *ui.Context) *design.Tokens` 只读；根主题仍由宿主 `c.SetTheme` 设置 | `settings_ui.go:96-118`（themeTokens）、`preferences.go` |
| `app/ui/locale` | **宿主 builtin** | `Lang()`/`T(key,args...)`；文案目录留在 `internal/i18n` | `i18n.go`（`T/tr/lang`） |
| `app/ui/composer` | **ui/composer 模块 provides**（非 builtin） | `Composer(opts) ui.Element` + 焦点 Handle + 命令目录；Home/Session 只调用不复制 | `composer_native.go:20/35/91`、`session_live.go:916`、`home.go:271` |
| `overlay.*` slot | Runtime 的 slot 空间 | 模块只声明「打开状态 + BuildPanel」，宿主统一 Overlay 层与 Esc 顺序；06 spike 约定 `overlay.*` 最后绘制 | `session_search.go:135-213`、`settings_ui.go:225-253`、20+ 处 `ui.Modal` |
| Command 注册 | Runtime.RegisterCommand | `ID/Label/Accelerator/Group/Run`；启动期检测重复 ID 与同 chord 双绑定（mygo 语义是「先注册者胜」，必须提前报错） | `composer_slash.go:68`（slash 目录）、`shortcuts.go:171-189`（快捷键行）、`shortcuts.go:378`（菜单表） |
| Toast | **无独立服务键** | 帧内 `c.Toast` 入宿主 viewport；帧外由模块持 deferred 状态、下一帧 flush（04 §3-10）；宿主保留主窗/settings 窗两层 viewport | `app.go:400-427`、`composer_slash.go:1044-1060` |
| 快捷键 | **不是服务**：每帧 `c.OnShortcut`/`OverlayShortcut` | 模块只注册自己命令的 chord；用户覆盖键 `keybinding.<id>` 由 Command 注册表校验；模态层快捷键由宿主 Overlay 负责 | `shortcuts.go:281`、`chrome.go:68`、`session_search.go:32-47` |
| 动效/时钟 | **不是服务**（帧内原语 + design tokens） | 模块用 `c.Now`/`c.AnimationFrame` + `design/motion.go` 预设 + 自持 `liveState`；宿主不提供全局动画服务 | `motion.go`、`design/motion.go` |

> 与 04/06 的差异说明：04 用 `app/ui`、06 spike 用 `ui/runtime` 作为 RuntimeKey；两者语义相同，等 06 集成回产品时统一（建议以 04 的 `app/ui` 为准）。本报告 §4 的「依赖的 UI 服务」列按上表口径解读。

### 5.3 为什么 chrome 不能也模块化

1. **组合根唯一**：`ui.View` 每帧只调用一次 `App.View`；若各模块自己建窗口/帧，会破坏 mygo 单主线程契约（§2.3）。
2. **z-order/模态是全局资源**：`ui.Modal` 在调用点渲染，后绘者在上；overlay 栈与 Esc 链必须单点仲裁（`session_search.go:135-183`），模块各自维护必然冲突。
3. **跨屏共享**：右栏（4 屏共用）、toast（双窗口）、快捷键（窗口级）天然是「所有模块的前置」；做成模块会产生「模块依赖模块」的倒置。
4. **收益反向**：chrome 只占 13.5%，拆它的收益是 0（不会变小、不会减少依赖），风险是主线程/焦点/像素回归；保留它是本方案**可行性**的前提。

## 6. 共享 UI 库：`internal/uikit` 还是 `internal/design`

### 6.1 判据（按优先级）

| # | 判据 | 放 `internal/design` | 放 `internal/uikit` | 留在特性模块 |
| ---: | --- | --- | --- | --- |
| 1 | 是否有帧内状态 | 无状态（值/样式函数） | 有 UI 状态但**由调用方持有**（不包级、不收 `*App`） | 有业务状态（依赖 store/Ctrl/设置） |
| 2 | 依赖方向 | 只依赖 mygo/ui + brand | 依赖 mygo/ui + design（+ i18n 文案参数） | 可依赖后端服务/其他 UI 服务 |
| 3 | 复用面 | 全 UI | ≥2 个模块或宿主+模块 | 单模块（或单屏） |
| 4 | 是否含业务动作 | 否 | 否（回调由参数注入） | 是（读 store/调 Ctrl/写设置） |
| 5 | 测试方式 | 纯值断言 | 可用 `ui.Tester` + 纯输入测试 | 需要 live controller fixture |

### 6.2 归属表（现状 → 目标）

| 复用件 | 现状 | LOC | 归属 | 说明 |
| --- | --- | ---: | --- | --- |
| tokens/颜色/字体/间距/style atoms | `internal/design`（tokens.go 170、components.go 153、style.go 93、font.go 43） | 459 | **design（保持）** | 已是独立包；模块只 import design，不新增控件 |
| 动效 tokens（时长/曲线/过渡） | `internal/design/motion.go` | 274 | **design（保持）** | 值 + 预设，无状态 |
| 品牌 markdown 封装（Options 缓存、Stream/View、代码块） | `markdown.go` | 119 | **uikit/markdown** | 现以 `*App` 为 key 缓存 Options（`markdown.go:59`）；改为调用方传 `markdown.Config{Theme,Size,Line,Root}`，删包级 map |
| popover 面板/单选/复选/动作行 | `popover_menus.go` | 167 | **uikit/popover** | 已被 `home_live_menus.go`/`chrome.go`/`modes_ui.go` 三处复用 |
| composer 卡/输入框/发送钮/品牌图标钮/focus 边框 | `composer_native.go`(121) + `home.go:190-205`(homeChip) | ≈160 | **uikit/composer** | 现注释即写「shared by the Session and Home screens」（`composer_native.go:9-16`）；`homeCardShadow` 定义在 `home.go:332` 却被 composer 件/plugins/popover/session_search 引用，是跨模块泄漏的直接证据 |
| settings 文本/行/分组/选择器基元 | `settings.go`(204) + `settings_ui.go` 的 `settingsText/Row/Section/Selector` | ≈500 | **uikit/settings** | 被 ui/settings 的 7 个文件 + ui/plugins/memory/models 的设置页复用（`settingsText` 被 7 个文件引用） |
| 虚拟列表包装（`ListState` + 行构建 + FollowEnd 语义） | `trajectory.go:237-266`（`ui.List` 用法）+ 其他 Scroll 用法 | ≈80 | **uikit/list** | mygo 已提供 `ui.List`；把「按 Seq 建 key / overscan / scroll 恢复」的薄包装抽出，供 transcript/trajectory/files 列表复用 |
| markdown 消息主体（role bubble + attachment chips + streaming 透明度） | `session_live.go:690-739`、`attachments_ui.go` 的 chips | ≈200 | **uikit/msg**（条件） | 只有一个消费者时按判据 3 留在 ui/session；若 W4 后 search 预览/记忆引用需要渲染消息，再提升（无状态提升成本低） |
| 工具卡行/状态点/结果面板 | `toolcards.go` 全部 | 704 | **留 ui/toolcards** | 单一模块消费者；只有 diff 行渲染（`workspaceDiffLineColor`）与 result header 可复用，可放 uikit/diff |
| diff 行渲染/颜色/折叠 | `workspace_changes_ui.go:164-249` | ≈85 | **uikit/diff** | ui/diff 与未来 Files 预览共用 |
| 搜索行/会话行（sidebar/search 共用） | `session_search.go` 的 `searchResultRow` | ≈40 | **uikit/rows** | 现被 inline search + 会话搜索两处用 |

> 结论：**`internal/uikit` 新建（预计 900–1,200 行，全部无 `*App`、无包级状态），`internal/design` 保持不变只管 tokens/样式/动效值。** 判据是「有没有 UI 状态、复用面、是否含业务动作」三条硬边界；markdown/虚拟列表/composer 基元一律进 uikit，消息行暂留 ui/session（§6.2 条件项），工具卡/设置页行等单消费者件不进 uikit。

## 7. Strangler 迁移：4 波 + Wave 0

### 7.1 波次表

| 波次 | 内容 | 前置 | 交付/验收标准 | 人日 [C] |
| --- | --- | --- | --- | --- |
| **W0 宿主骨架** | `internal/contracts/ui` + `internal/host/ui`（UIRuntime/Slot/Command/SettingsPage/ToolRenderer/Overlay 注册表 + Post/drain + Invalidate）；`internal/uikit`（§6.2）；测试 kit（`shelltest`：把 `newLiveApp` 430 处 fixture 抽成共享包，供模块包与 shell 集成测试共用）；AR-04 P0（`shell/kit`/`shell/text` 纯函数）作为前置 | 04 §5 已定稿、06 spike 已证明注册/回滚/门控/帧内组合（36 测试、`-race` 绿），W0 直接复用其 registry 形状 | 一个 fake-frame 单测：注册→渲染顺序→Dispose 后贡献消失；`-race` 绿；**不改任何屏的行为**（golden 帧零变化） | 12–16 |
| **W1 低耦合叶子** | ui/settings（通用页首批：Notifications/Data&Privacy/Diagnostics/Network/Desktop/Updates/Models/AgentPresets/MCP/Skills）、ui/plugins（含 Builtin plugins/About 页）、ui/automation、ui/toolcards、ui/diff | W0 | 每个模块：settings page/screen 通过注册表出现；模块包 `go test` 独立；golden 帧一致；`Ctrl.` 调用全部经 actions/model 薄包装（grep 门禁） | 18–24 |
| **W2 功能 slot** | ui/memory、ui/team、ui/goal、ui/plan、ui/approval、ui/todo；以及 ui/settings 与 ui/memory/ui/plugins 的 feature 页对接（General 剩余行、权限选择器） | W1（注册表已稳定） | 槽位出现/消失可断言；approval/plan 无模块状态；Esc 链由宿主按贡献优先级执行；卸载后 pending/plan 后端状态不受影响 | 18–24 |
| **W3 导航/侧栏** | ui/workspaces（含搜索/视图选项切分）、ui/files（右栏 4 tab）、ui/home | W1+W2；`app/workspaces`、`app/files` 读模型服务 | 侧栏/右栏/Home 由注册表组合；跨屏右栏行为一致；终端 ctx 在卸载时可取消；跨工作区跳转/草稿作用域不变；交互测试全绿 | 21–28 |
| **W4 会话/输入** | ui/session（transcript/trajectory/message/reasoning/errors）、ui/composer（组件服务/命令目录/mention/草稿）、references 子槽 | W2+W3；session 读模型接口（03 T1-4 的 UI 子集）冻结 | 所有 22 个贡献点由注册表组合；`App` 只剩宿主字段（目标 <40）；无 view 直连 `Ctrl./Store()` 的新代码；全量测试 + -race + golden 绿 | 28–36 |
| **收尾** | 16 个 cross/integration 测试改造（6,074 行）、a11y/visual golden 重生成、性能基准（帧时间）、文档/BUGS 回写 | W1–W4 逐波并行 | CI 全绿、帧时间不劣化（render_bench）、`go list -deps` 边界断言入 CI | 8–12 |
| **合计** | | | | **105–140** |

### 7.2 每波验收标准（细则）

- **贡献生命周期**：模块 `Activate` 在 UI 线程（启动期）执行；`Dispose` 在 UI 线程执行且逆序撤销（kernel `ctx.Effect`）；每次注册/注销都有 fake-frame 断言（spike/06 已证明可行）。**渲染函数不得在 UI 线程外被调用**；后台数据一律经 `Post`。
- **卸载可观测**：每波结束时做一个「模块 Disabled 后整屏/整槽消失、无 panic、无残留 goroutine」的装配测试（assembly `Disabled`/`Optional` 行）。
- **状态隔离**：模块不得新增包级变量；新增状态必须落在模块实例 struct；`shell` 内 19 个 `sync.Map` 在本波涉及该模块时删除对应条目（搬迁到模块 struct），禁止 `map[*App]` 新用法。
- **行为等价**：golden 帧（`docs/design/preview`）与 513 个 `ui.NewTester` 交互测试逐步迁移，每波只允许「新增测试/机械化移动」，不允许顺手改交互语义。
- **依赖方向**：`ui/*` 不得 import `internal/shell`（反向）；`ui/*` 只能 import `internal/uikit`、`internal/design` + 通过服务接口访问后端；`go list -deps` 断言入 CI。
- **命令/快捷键冲突检测**：注册表在启动期检测重复 command id / 同一 chord 双绑定（现 `Cmd+K` 的「会话内聚焦 composer / 其他屏搜索」由宿主路由，`shortcuts.go:81-89`）；模块只能注册自己的 chord，宿主报错并回滚。

### 7.3 人日与并行关系

| 项 | 人日 [C] | 说明 |
| --- | ---: | --- |
| W0 宿主骨架 + uikit + 测试 kit | 12–16 | 由 06 spike 的注册表/线程模型直接复用 |
| W1–W4 模块本体（16 模块，含模块测试 19.9k 行） | 84–112 | 见 §4.1 各模块列 |
| 收尾（跨模块验收 6.1k 行 + golden + 性能 + CI 门禁） | 8–12 | 跨模块测试只能改造不能搬 |
| **UI 模块化合计** | **105–140** | 中位 ≈120；约 2–3 人 × 2–3 个月 |
| 03 的 20 个后端模块（P0–P3） | 50–78 | 可与 UI 并行；UI 只依赖 `app/sessions`/`app/settings` 等接口 |
| **总程序** | **约 155–218** | 若先做 03 的 P0/P1（20–33），UI W0 可提前启动 |

> 假设：参与人熟悉 shell/mygo UI（不熟悉则 +20–30%）；不包含视觉重做/新功能；Swift/桌面多平台真机验收另计（+15–20% [C]）。若只做「贡献点接线」不做物理拆包（模块薄适配器留在 shell 包内），可先投入约 25–35 人日拿到装配纪律收益，再按波次物理迁移——这是推荐的首期方案（与 03 的 P0 薄适配器策略一致）。

## 8. 风险

| # | 风险 | 证据/量化 | 影响 | 缓解 |
| ---: | --- | --- | --- | --- |
| R1 | **God object 状态迁移** | `App` 123 字段（45 bool/37 string/10 func seam）；20 个字段被 ≥2 模块触达、38 个模块+宿主共用；`Ctrl` 被 13 个模块直接调用 | 拆包后如果仍传 `*App`，模块化名存实亡；`Screen/rightOpen/composerRef/draft` 会变成隐式全局 | W0 先定义模块上下文接口（`UIContext`：Navigator/Theme/Locale/Runtime），模块不得收 `*App`；逐模块把字段搬进模块实例 struct，`App` 只留宿主字段（目标 <40） |
| R2 | **19 个包级 `sync.Map` 清理** | `state.tsv`：19 个 map 全部 key `*App`、无 `Delete`、合计 159 个状态字段 | 模块卸载/多实例/测试隔离失效；模块化后若保留就是「新包里的老全局」 | 每波删除本模块条目：`slashMenuStates/liveMenuStates/shellUIStates/pluginUIStates/...` → 模块 struct；W0 加静态检查「`internal/shell/ui/**` 禁止 `sync.Map` 包级变量」 |
| R3 | **settings 独立窗口** | 同一 `*App` 被两个窗口在同一 UI 线程交替渲染（`internal/app/settings_window.go:231-305`）；`SettingsView` 复用主屏 provider 的 deferred toast 层；`refreshWindows` 双窗重绘有 Xvfb 已知限制（注释 `internal/app/settings_window.go:270-281`） | 模块贡献在「哪个窗口注册」不明；模态/焦点跨窗冲突；卸载 settings 内容会牵动主窗 | 窗口宿主留 `internal/app`；settings 内容模块只提供 `SettingsView(ctx)`；贡献表按「窗口作用域」注册（主窗/settings 窗各一份 instance id）；双窗重绘走宿主 hook |
| R4 | **快捷键/焦点冲突** | 13 行窗口快捷键 + 5 处 overlay 快捷键 + 每帧重新注册；15 处 `AutoFocus()`；composer focus Handle 跨帧有效但只在帧内绑定（`app.go:176-177`） | 模块各自注册键会重复/抢占；composer 焦点被弹层重建抢走（代码注释里已多次修过同类 bug，`home.go:311-320`） | 宿主 Shortcut 服务统一注册 + 启动期冲突检测；焦点由 Composer 服务单点持有；每波加「打开/关闭弹层后焦点仍在 composer」的回归测试 |
| R5 | **测试量（最大成本项）** | 100 个白盒 `package shell` 测试、29,465 行、572 函数、513 `ui.NewTester`、430 `newLiveApp`；16 个跨模块验收 6,074 行 | 物理拆包即全部编译失败；白盒测试直接读 `App` 字段/方法 | 先抽 `shelltest` 共享 fixture（W0）；模块包测试用 fake frame + 纯输入；shell 保留兼容 facade（同名薄方法转发）直到该波测试迁移完；跨模块测试改为「装配后整树」测试 |
| R6 | **帧内时序** | 渲染函数只在 `View` 构建期有效（`ui.Context` 文档 `mygo/ui/frame.go:4-6`）；`Handle` 帧内有效；`c.AnimationFrame()`/`Transition` 驱动帧 | 模块若缓存 `ui.Element`/Handle 或跨帧调渲染会静默失效/panic | 06 spike 固化契约；模块 API 只暴露 `Render(ctx)` 纯函数；CI 加 fake-frame 生命周期测试 |
| R7 | **文件/所有权冲突（并行开发）** | 多文件注释写「X 文件属别的 change」：`plugin_details.go:23-26`（「models_ui.go (which owns the settingsTab type) is outside this change's ownership」）、`motion.go:15-18`（「The App type lives in app.go, which this change does not own」）、`session_live.go:501-504`（「reasoning_ui.go is owned by another change」）；新增设置页要改 `settingsTab` 枚举所在的 5 个文件 | 并行 wave 与重构冲突；枚举/switch 继续增生 | 先把 settingsTab 枚举改为注册表（W1 第一个动作），删除散落 `+1` 枚举；模块写范围按 §4.1 文件清单冻结 |
| R8 | **跨模块字段 47 个** | 见 R1；例如 `rightOpen/rightTab` 被 6/3 个模块触达 | 右栏开关/搜索模式等状态被多模块写 | 状态归属表（§3「状态来源」列）作为验收清单；跨模块写改为服务方法（`Navigator/Overlay/Composer`） |

### 8.1 三个不要承诺的假设

1. **不会热插拔/热更新**：编译期注册表 + 每帧组合；模块增删 = 重新编译（与 01 的 kernel 结论一致）。
2. **不会减小二进制**：16 个模块全部链接进同一二进制；收益是裁剪与边界断言（可用 build tags 让 headless 不注册 UI 模块，但那属于产品裁剪，不是模块化本身）。
3. **不会把 UI 变成跨进程/跨语言**：mygo 原生即时模式；DSH 的浏览器 client 半边（02 §4.5）不可照搬，对应物是「同进程注册表 + 帧内渲染」。

## 9. 附录：证据与复现

| 证据 | 路径/命令 |
| --- | --- |
| 全量文件/LOC/方法/`sync.Map` | `.probe/research/kernel-split/ui-map/{inventory.go,files.tsv}` |
| 979 个函数/方法 `file:line` | `.probe/.../funcs.tsv`（`go run funcs.go`） |
| 每文件 Ctrl/Store/import/App 字段 | `.probe/.../coupling.tsv`（`go run analyze.go`） |
| 19 个 `sync.Map` + 状态结构字段 | `.probe/.../state.tsv`（`go run state.go`） |
| App 123 字段与模块字段触达统计 | `.probe/.../appfields.txt` + `rollup.py` 的 `# APP_FIELDS` |
| 模块汇总与守恒断言 | `.probe/.../modules.tsv`（`python3 rollup.py`，断言 25,229/29,465/779/100） |
| 模块归属映射 | `.probe/.../domains.tsv`（59 行）、`tests.tsv`（100 行） |
| 约束与接口（本报告的前置） | [04 §0.1 八条硬约束](04-ui-constraints.md)、[04 §5.2 API 草案](04-ui-constraints.md)、[06 spike 实测](06-ui-spike.md)（36 测试、`-race` 绿） |
| 关键 file:line 索引 | §3 表 + §1.2；渲染主链 `app.go:303-392`；贡献栈 `session_search.go:185-213`；Esc 链 `:135-183`；settings 页 `settings_ui.go:347,421`；右栏 `right_sidebar.go:165-197`；命令目录 `composer_slash.go:68`；菜单表 `shortcuts.go:350-390` |

> 局限：本文所有 LOC/方法数/测试函数为物理行与 AST 口径（可复现）；人日为 [C] 估算，未含真实桌面多平台联调；`internal/app` 的应用装配（8,153 行）只按窗口/菜单/托盘面计入宿主，其余属 03 的 HOST_app 范围。04 落盘后如接口命名变动，本报告 §4/§5 的服务名以其为准。
