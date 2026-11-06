# 07 · 最终验收复跑报告（HEAD 68bc842）

> 角色：独立验收（Team 成员 `parity-tests`）· 任务：`task-31` · 时间：2026-11-05 15:35–15:46（Asia/Shanghai）
> 对象：**HEAD `68bc842`**（`68bc842b2400ef3d6ff51041faa139b18143b86f`，"design review P0 fixes: WCAG AA tertiary text, dark wordmark, destructive confirms"，提交时间 15:34:51）
> 环境：`/data/mygo`，go1.27.3 linux/amd64，`GOMODCACHE=/data/mygo/.cache/gomod`，`GOCACHE=/data/mygo/.cache/go-build`，`GOSUMDB=off`，`CGO_ENABLED=0`
> 只读验收：唯一产物为本文；未修改代码/图片/其他文档，未 `git add/commit`。
> **结论速览：objective ① PASS、② PARTIAL（1 个必须补做的小项，见 §8-F1）、③ PASS；FAIL = 0。建议：暂缓在 `68bc842` 直接标记 goal complete——先完成 3 个"收口动作"（F1 对比度正文修复、F2 设置关闭入口收敛、review-round4.md 入版本库）并重跑 §2/§3；完成后即可标记 complete，无需新增功能。**

---

## 0. 验收方法与工作树时间线（重要）

任务背景称"工作树干净"；实测**验收开始时确为干净**，且 §2/§3 的全部构建/测试均在干净 HEAD 上完成。但验收后段，另一个并行任务（UX 第 4 轮复核 task-32）开始写工作树，随后其发现被另一个并行修复立即处理——**这些改动没有提交，不属于 HEAD `68bc842` 验收对象**：

| 时间 | 事件 |
| --- | --- |
| 15:35:29 | `git status --short` 0 行；HEAD = 68bc842；`git log -1` = 68bc842（15:34:51） |
| 15:35:46 | 卫生检查后 status 仍 0 行 |
| 15:36:14–15:37:53 | `go test ./...`、shell 全量：status 0 行（预览测试未写工作树，见 §3） |
| 15:37:58–15:39:05 | `ci.sh --fast`：status 0 行 |
| 15:40:46–15:41:11 | 定向测试 + benchmark 冒烟：status 0 行 |
| 15:41:32 | 出现未跟踪 `docs/design/review-round4.md`（并行 UX 复核产物） |
| 15:42:35–15:43:33 | 工作树出现**未提交代码修复**（brand palette/design tokens/toolcards/settings_ui/brand_test，`git diff --stat`：5 文件 +21/−6），对应 review-round4 的两个 P1；同期 `go build ./...` 仍通过。**未在脏工作树上重跑全量**——本报告裁决以 HEAD 68bc842 为准 |

因此本文分两层：**§1–§7 是 HEAD `68bc842` 的验收结论**；**§8 记录验收期间出现的、影响"是否可收口"的新证据（F1/F2 + 进行中的修复状态）**。

---

## 1. 仓库卫生（PASS）

| 时间 | 检查 | 命令 | 结果 |
| --- | --- | --- | --- |
| 15:35:29 | 工作树干净 | `git status --short \| wc -l` | **0** |
| 15:35:29 | HEAD/提交 | `git log -1 --format='%H %ad %s'` | 68bc842（15:34:51）；近 8 提交见日志（68bc842 → 3eb3c2e → b3ff65f → …） |
| 15:35:46 | 退役路径 | 逐个 `[ -e $p ]`（baseline、docs/baseline、artifacts、scripts/cdp.mjs、scripts/capture、internal/pixeltest） | 6/6 **ABSENT** |
| 15:35:46 | parity 门禁活引用 | `grep -rn 'TestScreensBaseline\|TestScreensDarkNarrow' --include='*.go' --include='*.sh' --include='Makefile' internal cmd scripts Makefile` | **0 命中**（历史文档 28 处提及，均已标注退役） |
| 15:35:46 | CI 像素步骤 | `grep -n 'pixel\|ScreensBaseline\|ScreensDarkNarrow\|SCREEN=\|ARTIFACTS=' scripts/ci.sh scripts/evolve/gates.sh Makefile` | 仅 2 条退役注释（`ci.sh:38`、`gates.sh:13`），无像素调用/参数 |
| 15:35:46 | 回滚点 | `git tag --list` | `pre-native-ui` 存在（归档包 §ADR 0001 记录，前次验收已核验 48.3MB tar） |

**判定：PASS。**

---

## 2. 构建 / 静态检查（PASS）

| 时间 | 步骤 | 命令 | 结果 |
| --- | --- | --- | --- |
| 15:35:51 | 格式 | `gofmt -l ./cmd ./internal` | **空输出，exit 0** |
| 15:35:51 | 静态检查 | `go vet ./...` | **PASS**，exit 0，wall 592ms（缓存热） |
| 15:35:52 | 构建 | `go build ./...` | **PASS**，exit 0，wall 2.45s |
| 15:35:58 | 零 cgo 交叉编译 | `sh scripts/check-nocgo.sh` | **PASS**：cgo 依赖 0；linux/amd64、linux/arm64、darwin/arm64、windows/amd64 全 ok；wall **12.5s** |
| 15:37:58 | 快速 CI 门禁 | `sh scripts/ci.sh --fast` | **CI PASS**，exit 0，wall **67.1s**（gofmt→vet→build→`go test ./...`→check-nocgo→`TestProductionToolsInvocable`；`--fast` 按设计跳过 coverage/race） |

日志：`/tmp/acceptance2/10-hygiene.log`、`20-build-static.log`、`21-nocgo.log`、`32-ci-fast.log`（末尾 `CI PASS`）。
**判定：PASS。** 每次步骤后 `git status` 均为 0 行。

---

## 3. 测试（PASS）

| 时间 | 步骤 | 结果 |
| --- | --- | --- |
| 15:36:14 | `go test -count=1 ./...` | **exit 0**，wall **47.9s**；**18 个包**（17 `ok` + `cmd/zeokeel` no test files），无 FAIL。热点：`internal/shell 46.039s`、`internal/session 30.124s`、`internal/tools 7.247s`、`internal/app 1.251s` |
| 15:37:07 | `go test -count=1 -v ./internal/shell`（shell 全量） | **exit 0**，wall **45.1s**（包耗时 43.473s）；**306 个顶层 PASS / 0 FAIL / 0 SKIP**（307 个 `func Test*` 含 1 个 `TestMain`） |
| 15:37:58 | `ci.sh --fast` 内再次 `go test -count=1 ./...` | 再次全绿（§2） |
| 15:37:02 / 15:37:53 / 15:39:05 / 15:41:11 | 测试结束后 `git status --short` | **每处均为 0 行 → 预览测试/普通测试不写工作树**（opt-in 卫生，机制见 `livetest_helpers_test.go:36-45`：默认写 `t.TempDir()`，仅 `ZEOKEEL_UPDATE_PREVIEWS=1` 写仓库） |

测试规模（HEAD 68bc842）：`internal/shell` **53 个测试文件 / 307 个 Test 函数**；全仓 **167 个测试文件 / 913 个 Test 函数**（含 4 个 `TestMain`、1 个 `//go:build e2e` 可选真模型用例）；`Benchmark*` **7 个**（24 个 sub-benchmark）。对比上一轮验收（b3ff65f：868 / 282 / 0 benchmark）：**+45 个测试、+7 个 benchmark**。

benchmark 冒烟（15:41:08，`-benchtime=1x`，非正式数字）：24 个 sub-benchmark 全部可运行、PASS，wall 2.95s；正式数字以 `docs/design/perf.md` 为准（3 次中位数：稳态帧 0.48–1.26ms/帧，`ui.Render` 21–31ms）。
**判定：PASS。**

---

## 4. 覆盖：预览 / 品牌 / P0 修复证据（PASS）

### 4.1 预览与覆盖

- `docs/design/preview/`：**22 张 PNG**（11 组 × 浅/深）+ README（11 行表格，逐屏链接）：
  Home、Session、Sidebar、Settings、Plugins、Automation、Trajectory、Right sidebar（Start）、Right sidebar（Files）、**Tool cards（新）**、**Settings window（新）**。
  其中 10 组为 1600×1000（`ui.Render(...,1600,1000,1)`），独立设置窗口 2 张为 **900×720**（设置窗口设计尺寸，ADR 0005）；全部为 PNG RGB、非空。
- 预览测试 **10 个**（`TestRender*NativePreview`，全部 PASS）：Home / Session / Sidebar / Settings / **SettingsWindow** / Plugins / Automation / Trajectory / RightSidebar / **ToolCard**。
- 独立抽检（`read_image`）：`settings-window-native-light.png`（900×720，独立设置窗口：左侧 nav + 右侧 General/外观/行为，品牌蓝选中态、无 scrim）、`toolcard-native-dark.png`（1600×1000：bash 代码块 + Copy、markdown 结果、图片工具、错误面板 + 复制错误、折叠摘要行）→ 均为真实 headless 渲染，品牌一致。

### 4.2 品牌主题与 design 派生

- `internal/brand`：`brand.go` + `palette.go`（生产 311 行）+ `brand_test.go`（5 个 Test 全 PASS）。
- `internal/design` 全面派生：`tokens.go:101/104` `fromPalette(brand.LightPalette()/DarkPalette())`、`:150-158` `Theme()` 返回 `brand.LightTheme()/DarkTheme()`；`components.go`/`font.go` 同样 import brand。
- 品牌口径：无 DSH 产品文案（`Into the Unknown`/鲸/whale 均 0；`"Preview"` 仅文件预览功能文案）；Home 为 `ZeoKeel` wordmark + `home.tagline`「今天想构建什么？」。
- 残余硬编码色（分类）：`ui.Hex/ui.RGBA` 在 shell 生产共 **7 处**——`home.go:272/274`（hero scrim/阴影）、`session_live.go:256`/`settings_ui.go:250`/`sidebar_hover.go:50`（半透明黑阴影）= **5 处合理**（阴影/遮罩非令牌）；`plugins.go:34-35` `#6E7278`/`#C2C6CA` "More" 标签色 = **2 处 DSH 采样色残留**（应换 `LabelSecondary/tertiary`；`a11y_fixes_test.go:316` 仍在 pin，需同步改）。不阻塞 v1，但属 objective② 的 P3 残留。
- 魔法几何：生产代码无 845/877/909 等像素常量（`session.go:16-17` = `180 * design.Space1`=720 DIP；`layout.go` 仅注释提及 "old 845/877"）。✔

### 4.3 P0 修复证据（68bc842）

| P0 | 修复实现 | 测试证据（均 PASS） |
| --- | --- | --- |
| **P0-1 破坏性确认** | Automation 删除 / Memory Clear all 接入确认弹层（Cancel 默认焦点、Esc/点外关闭、Enter 不误删、Danger 确认按钮、确认后 Toast） | `TestAutomationDeleteRequiresConfirmation`、`TestMemoryClearAllRequiresConfirmation`（15:40:49 PASS）；`review-round3.md` 附录记录 |
| **P0-2 浅色三级文字 AA** | `internal/brand/palette.go:148` `TextTertiary = #646C7A`（BgBase/BgRaised 5.29、BgSidebar 5.06、BgStrong 4.76:1）；design 映射 | `TestPaletteContrast`（brand，15:40:52 PASS；含 4 组表面 + 层级断言）；`fixes_shell_test.go:TestHomeTagContrast` 等 shell 对比度测试 PASS |
| **P0-3 深色字标白边 / 2x** | `mark-dark@2x.png`（96×96）embed；`assets.go:50 brandMark(t)` 按主题切换；Home hero / Sidebar / rail 使用 | `TestBrandMarkAssets`、`TestDarkMarkHasNoWhiteContainer`、`TestBrandMarkRendersWithoutWhiteCorners`（15:40:49 PASS，含外圈无近白像素断言） |

**判定：PASS（P0 三项均有实现+自动化断言）。**

---

## 5. 桌面交互本地化（PASS；真机项列为发布门禁）

| 能力 | 实现/证据 | 测试（15:40:46–15:40:49 定向复跑，全部 PASS） | 判定 |
| --- | --- | --- | --- |
| 系统菜单栏 | `internal/app/menu.go`（mygo.NewMenu；Role App/File/Edit/View/Window；accelerator 与 shell 命令表同源）；ADR 0003 | `TestMenuBarStructure`、`TestMenuCommandAccelerators`、`TestMenuDispatchRunsShellCommand`、`TestMenuDispatchQuit`、`TestMenuActionHookSeesEveryCommand`、`TestMenuLabelsFollowShellLanguage`、`TestMenuUnknownActionDoesNotPanic`（7） | ✅ |
| 窗口状态 | `internal/app/window_state.go`（JSON 原子写、损坏 fallback）；ADR 0003 | `TestWindowStateStoreRoundTrip/MissingFile/CorruptFile/DropsUnusableEntries/EmptyStates`（5） | ✅ |
| 系统通知（完成/失败/审批 + **后台会话**） | `internal/app/notify.go` + `internal/session/controller.go:1490 SetOnTurnFinished` hook（覆盖所有会话，含后台）；5s/会话完成冷却；不活跃才发；点击聚焦+开会话；中英双语、隐私；ADR 0004 更新 | 13 个 `TestNotification*` + 2 个 `TestBackgroundSession*` + 6 个 `TestTurnFinishedHook*` + `TestNotificationActiveGate`（22） | ✅ P0/P1 完成；后台"等待审批"仍依赖快照路径（known-limitations §3 已如实记录） |
| **设置独立窗口（P1）** | `internal/app/settings_window.go`（mygo.NewWindow；单例/close-to-hide/状态同步/overlay 回退；900×720；ADR 0005）；`settingsHeaderLive` 共用视图 | `TestSettingsWindowSingletonAndFocus`、`CloseHidesAndReopens`、`ClosesOnlyWhenQuitting`、`Unavailable`、`OptionsAreBrandSized`、`TestAppSettingsActionOpensWindow`、`FallsBackToOverlay`、`TestRefreshWindowsInvalidatesBoth`（8） | ✅ 交付；关闭入口收敛待收口（§8-F2） |
| 系统偏好 / DPI | `internal/shell/preferences.go`（TextScale 与用户字号相乘、HighContrast 令牌增强、ReduceMotion 全局 `DisableAnimations`、滚动条策略）；§12 文档；2x DPI（Home/Session → 3200×2000） | `TestTextScale*`、`TestHighContrast*`、`TestPreferences*`、`TestHighDPIRenderSmoke`（7–8） | ✅；固定 type scale 不随 TextScale 缩放 = known-limitation |
| 文本可选 | assistant 消息与工具卡内容走 mygo `ui/markdown`（库内 `view.go:202 root.Selectable()`，含 Copy/Copy as Markdown）；**用户消息气泡仍为普通 `ui.Text` 不可选**（`session_live.go:310-320`） | 间接：`TestAssistantMessageCopyRetryAndFeedback`、markdown/工具卡 Copy 测试 PASS | ◐ 部分（与 known-limitations §3 措辞有轻微出入，见 §6） |
| 滚动条 | 仅 hover overlay（`preferences.go:138 Overlay:true`；`Theme.ScrollbarWidth` 6→8 DIP under HighContrast）；无全局常显 | `TestPreferencesScrollbarPolicy` PASS | ◐ 框架限制（known-limitations §2 如实记录） |
| 真机 IME / 屏幕阅读器 / 多平台 / 通知外观 | 无项目级实现/测试，依赖 mygo 原生控件与系统；本环境无显示器 | — | ❌ 证据不足（**发布门禁，非设计/开发收口门禁**，known-limitations §1 已列） |

---

## 6. 功能不回退 + known-limitations 对照（PASS）

### 6.1 功能回归证据

- 全仓 `go test ./...` 18 包全绿；shell 306/306 PASS、0 SKIP；无功能测试被删除/跳过。
- 关键域测试计数（shell，全部通过）：审批 6、附件 9、键盘 13、a11y 17（含新增 `a11y_native_test.go` 8）、markdown 10、工具卡 10、工作区 20、快捷键 13、composer 12、菜单/popover 18、搜索 10、动效 13、Automation 11、Memory 3、Settings 24、Plugin 13、Session 39、Trajectory 5；`internal/app` 103 个测试（菜单/通知/设置窗口/窗口状态/工具 e2e 接线）、`internal/session` 70、`internal/tools` 130、`internal/runtime` 113。
- 真实模型 e2e：`internal/e2e` 1 个测试 `//go:build e2e`（`scripts/e2e-model.sh` 触发），普通 CI 不运行——与既定 CI 口径一致。

### 6.2 `docs/design/known-limitations.md` 逐项核实

| 声明 | 独立核实 | 结论 |
| --- | --- | --- |
| 真机 IME / 屏幕阅读器 / 高 DPI 切换 / 通知服务 / 窗口管理未验证 | 无项目级 IME/VoiceOver 代码与测试；仅 2x headless 冒烟（`TestHighDPIRenderSmoke`） | **如实**（发布门禁） |
| 滚动条仅 hover overlay；TextScale 只覆盖基座+会话正文；HighContrast 为品牌增强 | 代码核对一致（`preferences.go`）；固定 type scale "约 115 处" 实测（`.FontSize(<数字>` 字面量）**138 处** | **基本如实**（数字口径偏差，建议按实测更新） |
| 后台"等待审批"通知仍会漏；通知无 ID 替换/动作按钮/退出后点击；同会话 5s 冷却 | `notify.go`：hook 只管 complete/failed，approval 走当前会话快照；`NotificationOptions` 仅 Title/Body/Group + OnClick；`turnNoticeThrottle = 5 * time.Second` | **如实** |
| 设置窗口 Win/Linux 无菜单栏、几何联动/滚动位置持久化未做、overlay 回退保留 | `installMenu` 仅主窗口；`TestAppSettingsActionFallsBackToOverlay` 存在 | **如实** |
| 文本选择："消息正文与工具卡内容已可选中/右键复制（mygo markdown）；侧栏/设置等普通 UI 文本未全面开启" | assistant 消息与工具卡（markdown）✅；**用户消息气泡为普通 Text 不可选**；侧栏/设置不可选 | **大体如实但措辞偏宽**（建议改成"assistant 消息正文与工具卡内容"） |
| 22 张预览、opt-in 不写工作树、CI 无 golden/视觉回归、无状态矩阵 | 22 张 ✓；opt-in 机制与 git status 0 行 ✓；`ci.sh` 无 preview/golden 步骤 ✓；预览仅浅/深两态 ✓ | **如实**；另发现 `docs/design/preview/README.md` 写"全部 1600×1000"，而 settings-window 为 900×720（P3 文档小误差） |
| Trajectory 未虚拟化；工具结果 40 行封顶+展开；无内核沙箱、PTY 仅 Unix 等产品边界 | `trajectory` 无 virtual 逻辑；`toolResultMaxLines = 40` ✓ | **如实** |
| 打包/签名/自动更新未重新验证 | 本轮无相关测试/CI 证据 | **如实**（发布门禁） |

**判定：PASS（known-limitations 与代码/测试一致，含 2 处 P3 措辞/数字小偏差）。**

---

## 7. 上一轮（06 报告）PARTIAL 项闭环对照

| 06 报告遗留 | 本轮状态（HEAD 68bc842） | 证据 |
| --- | --- | --- |
| 设置独立窗口未交付 | ✅ **已闭环** | `internal/app/settings_window.go` + 8 测试 + ADR 0005 + 900×720 预览 |
| 后台会话通知缺失 | ✅ **已闭环（完成/失败）**；后台"等待审批"仍为 known limitation | `controller.SetOnTurnFinished` hook + 2 个后台事件测试 + 6 个 hook 测试 |
| 工具卡纯文本不可选 | ✅ **已闭环** | `toolcards.go` 接入 mygo markdown（代码块+Copy、markdown 结果、图片、错误面板）；`TestToolCardCommandOutputIsCodeBlockWithCopy` 等 10 个工具卡测试 PASS |
| 原生控件替换不完整 | ◐ 部分推进（工具卡/设置窗口接入；`ui.List/Tree/Table/Tabs/Toolbar/Collapsible` 仍手绘/未用） | 05-final-audit §4.2 记为"基本达成"，属下一轮工程项（可接受，非本收口必须项） |
| 文本可选仅 markdown | ◐ 同前（用户气泡仍不可选，known limitation） | §5/§6 |
| 真机证据不足 | ❌ 仍无真机（环境限制，known-limitations 已列，**发布门禁而非开发收口门禁**） | §6.2 |
| 残余 DSH 清理 | ◐ 部分推进（DSH 提及 163→**152**、baseline 50→**30**；`plugins.go` 2 处采样色仍在） | §4.2 |
| UX 观感终评 | ◐ 并行 `review-round4.md`（task-32）已完成：P0 三项验证通过，"v1 视觉/UX 有条件签收"，**新发现 1 个 P1 对比度问题 + Settings 关闭入口问题**（见 §8） | `docs/design/review-round4.md`（验收时未提交） |

---

## 8. 验收期间出现的新证据：F1/F2 与收口建议（关键）

### F1（**必须补做**，小）：工具卡错误面板浅色正文对比不足 WCAG AA

- **HEAD 68bc842 事实**：`toolcards.go` 错误面板 `Background(t.Danger.Alpha(0.08).Over(t.BgRaised))`；错误标题/「复制错误」/错误正文均 `TextColor(t.Danger)`；浅色 `Danger=#DC2626`，合成底色 `#FCEEEE` → **对比度 4.28:1 < 4.5:1（WCAG AA 正常文本不通过）**。深色为 `#F87171` on `#2D222C` = 5.51:1 通过。该问题由并行 UX 复核 `review-round4.md` 发现；**本报告独立复算确认 4.28:1**。
- **验收时工作树上的进行中修复（未提交，不计入 HEAD）**：新增 `brand.Palette.DangerText=#B91C1C`（5.73:1 on tint）与 `design.Tokens.DangerText`；`toolcards.go` 已把「错误」标签与「复制错误」改为 `DangerText`，但**错误正文（mono 12px，`toolcards.go:364-365`）仍是 `t.Danger`（4.28:1）**；新增的 `TestPaletteContrast` 条目只覆盖 `DangerText`，且合成底色用 `Over(tc.p.Background)`（深色下 `Background` ≠ 实际 `BgRaised`）。
- **结论**：F1 **在 HEAD 上必须补做**；当前进行中修复**不完整**（正文未改、测试未覆盖正文/实际底色）。修复量级 <1 小时：三处危险文本统一用 `DangerText`，并把对比度断言改为"危险文本 on 实际危险 tint 面板（BgRaised 合成）"覆盖 light/dark。**15:44:49 复查：`toolcards.go:365` 正文仍为 `t.Danger`，`brand_test.go:160` 仍用 `Over(tc.p.Background)`，M1 尚未完成**（同期 6 张预览已重新生成、README/STATUS/known-limitations 有并行文档改动）。

### F2（需处理：修复或记录取舍）：Settings 关闭入口不唯一

- HEAD：`settingsHeaderLive` 同时渲染「关闭」文字按钮（`settings_ui.go:421-422`）与「×」（`:424`）；独立窗口宿主还会叠加系统标题栏关闭 → 最多 3 个关闭入口（review-round3 P1-4 部分修复，review-round4 再指出）。
- 工作树已有未提交修复：`!a.hasSettingsWindowHost()` 时隐藏文字「关闭」，独立窗口只留「×」；overlay 回退保留文字+X。**需提交并由测试/截图验证**（建议加一条"独立窗口宿主仅 1 个可见关闭控件"断言）。若 Lead 决定不改，应把"双关闭入口"明确写入 known-limitations 作为 v1 取舍。

### 其他发现（P3，不阻塞收口）

1. `review-round4.md` 当前 **untracked**——v1 视觉签收证据未入版本库，收口前需提交（或 Lead 明确其不作为签收依据）。
2. `known-limitations.md` 两处小偏差：文本可选措辞宜改"assistant 消息正文"；固定 type scale 实测 **138 处**（文档写"约 115 处"）。
3. `docs/design/preview/README.md` 称"全部 1600×1000"，settings-window 实为 900×720。
4. `plugins.go:34-35` 两处 DSH 采样标签色（P3 视觉残留，建议换令牌并更新 `a11y_fixes_test.go` pin）。
5. 05-final-audit D1/D7/D8/D9 文档残留仍在：`docs/STATUS.md:59-60` 阶段 1 快照数字未标注"阶段 1 快照"；`docs/design/native-ui.md` §13 子节仍编号 12.x；README 仍写"六屏"（实为九区+面板）；`docs/research/04` 计数为清理时点快照未标时点。

---

## 9. 裁决与"是否可标记 goal complete"

### 逐条 objective

| # | objective | 裁决 | 证据行 |
| --- | --- | --- | --- |
| ① | 拆除 DSH 像素对齐门禁及仅为其服务的代码，保持绿色 | **PASS** | 6 个退役路径 ABSENT；0 处 parity 活引用（28 处历史文档说明）；CI 无像素 step；`App.View` 无 Ctrl 只画空帧；gofmt/vet/build/nocgo/ci --fast/test 全绿；tag `pre-native-ui` + 归档可回滚 |
| ② | 用 mygo Theme/原生控件重做视觉与交互（品牌主题、原生控件、桌面交互本地化） | **PARTIAL（1 个必须补做项；其余为已知限制可接受）** | 品牌主题 ✅（brand 311 行/design 派生/22 预览/无 DSH 文案）；P0 三项 ✅（自动断言）；桌面交互 ✅（菜单 7、窗口状态 5、通知 22、设置窗口 8、偏好/DPI 7–8 测试 PASS）；工具卡/设置窗口/后台通知已闭环。**未闭环**：F1 错误正文 4.28:1（必须补做）；F2 关闭入口（修复待提交或记录取舍）；原生控件未全量替换（下一轮工程项，可接受）；真机验证（发布门禁，可接受） |
| ③ | 持续功能对齐与测试绿色 | **PASS** | 18 包全绿、shell 306/306、0 SKIP；功能域测试计数完整（审批/附件/i18n/快捷键/工作区/会话/工具/runtime）无回退；`FUNCTIONAL-PARITY.md` 维护中。caveat：未对真实 DSH 重新逐项实测；矩阵中打包/更新、auto-review 等为既定产品边界（known-limitations §5） |

**FAIL = 0。硬阻塞 = 0。** 收口前置为 3 个"小而明确"的动作（无需新增功能、预计 <1 小时）。

### 是否建议标记 goal complete

> **暂缓在 `68bc842` 直接标记 complete**（差 F1 必须补做 + F2 处理 + 提交/重验），**完成 §8 的 M1–M3 后即可标记 complete**；无需新增功能、无需继续大改视觉。

**必须完成（must-complete）**：
1. **M1 修复 F1 并补测试**：错误标题/正文/Copy 三处统一 `DangerText`（light 5.73:1 / dark 8.04:1）；对比度断言改为"危险文本 on 危险 8% tint（light BgRaised+BgBase / dark BgRaised）"，覆盖全部危险文本角色；跑 `go test ./internal/brand ./internal/shell -run 'Contrast|ToolCard'`。
2. **M2 处理 F2**：提交设置关闭入口收敛修复（工作树已有草稿）并加"独立窗口仅 1 个可见关闭"断言；或明确写入 known-limitations 作为 v1 取舍。
3. **M3 收尾提交并重验**：把 `review-round4.md` 与 M1/M2 修复提交为最终 HEAD；在最终 HEAD 重跑 §2/§3（`gofmt -l ./cmd ./internal`、`go vet ./...`、`go build ./...`、`sh scripts/check-nocgo.sh`、`sh scripts/ci.sh --fast`、`go test -count=1 ./...`），确认 `git status` 干净、22 张预览仍可复现。

**可接受（known limitation / 发布门禁，不阻塞 goal complete）**：
- 真机 IME / VoiceOver·Narrator / 多平台菜单与通知外观 / 1.25–2x DPI 真机矩阵 / 后台"等待审批"通知 / 通知动作与 ID 替换；
- TextScale 固定 type scale（138 处）不缩放、滚动条仅 overlay、Trajectory/超长工具输出未虚拟化；
- 原生控件未全量替换（`ui.List/Tree/Table/Tabs/Toolbar/Collapsible` 等）、视觉 golden/CI 自动视觉回归缺位、预览状态矩阵不完整；
- 文档 P3 修正（§8 其他发现 1–5）；
- 功能矩阵既有 ❌ 产品边界（打包/自动更新、auto-review、Goal driver 等）。

---

## 10. 复现命令（本次验收）

```bash
cd /data/mygo
export PATH=/usr/local/go/bin:$PATH
export GOMODCACHE=/data/mygo/.cache/gomod GOCACHE=/data/mygo/.cache/go-build
export GOSUMDB=off CGO_ENABLED=0

# 0) 基线
git rev-parse HEAD && git status --short && git log -1 --format='%H %ad %s'

# 1) 仓库卫生
for p in baseline docs/baseline artifacts scripts/cdp.mjs scripts/capture internal/pixeltest; do
  [ -e "$p" ] && echo "EXISTS: $p" || echo "ABSENT: $p"; done
grep -rn 'TestScreensBaseline\|TestScreensDarkNarrow' --include='*.go' --include='*.sh' internal cmd scripts Makefile
grep -n 'pixel\|ScreensBaseline\|ScreensDarkNarrow' scripts/ci.sh scripts/evolve/gates.sh Makefile

# 2) 构建/静态
gofmt -l ./cmd ./internal
go vet ./...
go build ./...
sh scripts/check-nocgo.sh
sh scripts/ci.sh --fast

# 3) 测试 + 工作树卫生
go test -count=1 ./...
go test -count=1 -v ./internal/shell
git status --short          # 期望空（本轮实测在测试/CI 后均为空）

# 4) 覆盖 / P0 / known limitations
ls docs/design/preview/*.png | wc -l                          # 22
grep -rn 'func TestRender.*NativePreview' internal/shell/*_test.go   # 10
grep -rn 'zeokeel/internal/brand' internal/design/*.go
grep -rn 'ui\.Hex(\|ui\.RGBA(' internal/shell/*.go | grep -v _test
git show HEAD:internal/brand/palette.go | grep -n 'TextTertiary'
git show HEAD:internal/shell/toolcards.go | grep -n 'Danger'
go test -count=1 -v ./internal/app -run 'TestMenu|TestNotification|TestSettingsWindow|TestWindowState|TestBackgroundSession|TestTurnFinishedHook'
go test -count=1 -v ./internal/shell -run 'TestA11y|TestRender.*NativePreview|TestBrandMark|TestAutomationDeleteRequiresConfirmation|TestMemoryClearAllRequiresConfirmation'
go test -run '^$' -bench 'BenchmarkRender|BenchmarkWarmFrame' -benchtime=1x -benchmem ./internal/shell

# 5) F1 独立复算（WCAG）
#    HEAD: Danger #DC2626 on 8% tint #FCEEEE = 4.28:1 (< 4.5)
#    进行中修复: DangerText #B91C1C on tint = 5.73:1；正文仍用 Danger → 4.28
```

原始日志（仓库外）：`/tmp/acceptance2/{00-preflight,10-hygiene,20-build-static,21-nocgo,30-gotest-all,31-shell-verbose,31-shell-summary,32-ci-fast,40-app-targeted,41-shell-targeted,42-bench-smoke}.log`。

---

## 附录 A：验收时间线（本次）

| 时间 | 动作 | 结果 |
| --- | --- | --- |
| 15:35:29 | preflight：HEAD/status/log | 68bc842，status 0 行 |
| 15:35:46 | 仓库卫生 | 6 路径 ABSENT；parity 活引用 0；CI 仅退役注释 |
| 15:35:51–15:35:54 | gofmt / vet / build | 全 PASS（0.59s / 2.45s） |
| 15:35:58 | check-nocgo | PASS，四目标 ok，12.5s |
| 15:36:14 | go test ./... | PASS，18 包，47.9s（shell 46.0s） |
| 15:37:07 | shell 全量 -v | 306 PASS / 0 FAIL / 0 SKIP，45.1s |
| 15:37:58 | ci.sh --fast | CI PASS，67.1s |
| 15:40:46–15:40:52 | 定向测试（app/shell/brand） | 42 + 26 + 2 个 PASS |
| 15:41:08 | benchmark 冒烟 | 24 sub-benchmarks PASS，2.95s |
| 15:41:32 | 并行 UX 产物出现 | `?? docs/design/review-round4.md` |
| 15:42:35–15:43:33 | 并行修复写入工作树（未提交） | 5 文件 +21/−6；DangerText 修复不完整（正文仍 4.28） |
| 15:44:44–15:44:49 | 工作树继续变化（并行任务） | 6 张预览重新生成；README/STATUS/known-limitations 修改；错误正文仍 `t.Danger`（4.28:1） |

## 附录 B：数据速览（HEAD 68bc842）

| 指标 | 值 |
| --- | --- |
| Test 函数 | 全仓 913（167 测试文件）；shell 307（53 文件，含 1 TestMain） |
| 测试结果 | 18 包全绿；shell 306/306 PASS、0 FAIL、0 SKIP |
| Benchmark | 7 个（24 sub-benchmark）；`docs/design/perf.md` 稳态帧 0.48–1.26ms |
| 预览 | 22 张 PNG（11 组浅/深；settings-window 900×720，其余 1600×1000）+ 10 个预览测试 |
| 品牌 | `internal/brand` 311 行生产 + 5 测试；design 4 文件派生；TextTertiary #646C7A |
| 生产 LOC | shell 16,051 / design 690 / brand 311 / icons 81 / app 2,756 |
| CI/门禁 | gofmt/vet/build/check-nocgo(4 目标)/ci.sh --fast 全 PASS |
