# 06 · 独立验收报告：objective 全清单可复现验收

> 角色：独立验收（Team 成员 `parity-tests`，独立于本轮改造实现者）· 任务：`task-27`
> 时间：2026-11-05 15:04–15:16（Asia/Shanghai）· 仓库：`/data/mygo`
> 验收对象：`objective` 三项 + 调研验收清单（[00-report §7](../research/00-report.md)、[native-ui §17](../design/native-ui.md)、[ADR 0001–0004](../decisions/)）
> 结论速览：**PASS 2 项 / PARTIAL 1 项 / FAIL 0 项**；HEAD `b3ff65f` 上无构建/测试失败。**建议有条件收口**（不新增功能；等并行任务提交后的最终 HEAD 上重跑本报告 §2/§3 即可），不可在**当前漂移工作树**上直接标记 goal complete（详见 §7、§8）。
> 只读验收：本文是唯一产品；未修改任何代码/图片/其他文档，未 git add/commit。

---

## 0. 验收方法与偏差说明（重要）

### 0.1 基线状态（15:04:18）

```text
HEAD    b3ff65f96d3222eb916f07248a89882d83cabd7b (b3ff65f)
branch  master
status  clean（git status --short 输出 0 行）
tag     pre-native-ui（回滚点）
归档    /data/zeokeel-backups/parity-assets-20261105.tar.gz（48,360,224 bytes, 13:21）
log     b3ff65f 11-05 15:01 native visual round 2
        dcb8dcc 11-05 14:39 tests: keep preview generation out of the work tree
        a592258 11-05 14:37 native visual: restyle Session/Sidebar/Settings/Plugins/Automation
        a718b2c 11-05 14:05 native visual pilot: rebrand Home on the mygo brand theme
        8bf0053 11-05 13:48 retire DSH pixel parity: remove gate, baseline assets and static screens
        b974d92 11-05 13:21 snapshot: pre-native-UI migration
```

### 0.2 隔离运行（为什么测的是只读导出副本）

本轮验收开始时工作树与 HEAD 完全一致（15:04:18 clean），但 **15:09 起其他队友的并行任务开始写工作树**（15:09:25 dirty 17 个文件 → 15:12:04 25 个 → 15:13:21 31 个；见 §8 附录）。为避免"验收跑一半代码变了"导致结果不可复现，构建/测试在 **`git archive HEAD` 的只读导出副本** 上执行：

```bash
cd /data/mygo && rm -rf /tmp/acceptance/tree && mkdir -p /tmp/acceptance/tree
git archive HEAD | tar -x -C /tmp/acceptance/tree     # 15:05:15，394 文件 6.3MB
```

导出内容 = 15:04:18 的干净工作树 = 任务指定的 HEAD `b3ff65f`。**未提交的并行任务改动不在本次验收范围**。原始日志在 `/tmp/acceptance/*.log`（仓库外，不污染工作树）。
环境：`go1.27.3 linux/amd64`，`GOMODCACHE=/data/mygo/.cache/gomod`，`GOCACHE=/data/mygo/.cache/go-build`，`GOSUMDB=off`，`CGO_ENABLED=0`。

> 过程偏差（如实记录）：容器内无 `/usr/bin/time`，计时改用 `date +%s%N` 前后差；首条计时命令因此退出 127 后重跑，不涉及产品代码。

---

## 1. 仓库卫生（Step 1）

| 时间 | 检查 | 命令 | 结果 |
| --- | --- | --- | --- |
| 15:04:18 | 工作树干净 | `git status --short \| wc -l` | **0**（clean） |
| 15:04:39 | 退役路径不存在 | `for p in baseline docs/baseline artifacts scripts/cdp.mjs scripts/capture internal/pixeltest; do [ -e $p ] && echo EXISTS \|\| echo ABSENT; done` | 6/6 **ABSENT** |
| 15:04:39 | 无 parity 门禁活引用 | `grep -rn 'TestScreensBaseline\|TestScreensDarkNarrow' --include='*.go' --include='*.sh' --include='Makefile' internal cmd scripts Makefile` | 无命中（exit 1）；仅 24 处**历史文档**提及（01-code-audit/02/00-report/ADR-0001/STATUS/EVOLUTION 等，均已标注退役） |
| 15:04:39 | 无已删资产活引用 | `grep -rn 'internal/pixeltest\|baseline/screenshots\|baseline/css\|docs/baseline\|artifacts/' --include='*.go' --include='*.sh' --include='Makefile' internal cmd scripts Makefile` | 仅 1 处注释（`responsive_panels_test.go:234` "point at the retired docs/baseline tree"），无代码依赖 |
| 15:04:39 | CI 无像素步骤 | `grep -n 'pixel\|ScreensBaseline\|ScreensDarkNarrow\|SCREEN=\|ARTIFACTS=' scripts/ci.sh scripts/evolve/gates.sh Makefile` | 仅退役注释（`ci.sh:38-39`、`gates.sh:13`）；**无像素测试调用、无 SCREEN/ARTIFACTS 门禁步骤** |
| 15:04:39 | 回滚点存在 | `git tag --list` / `ls -la /data/zeokeel-backups/` | tag `pre-native-ui` 存在；归档包 48.3MB 存在 |

**判定：PASS。** 备注（不阻塞）：`.gitignore` 仍保留 `/baseline/`、`/artifacts/`、`/internal/shell/artifacts/` 三条退役忽略规则（无害，可择机删）；CI 保留退役说明注释，属合理历史记录。

---

## 2. 构建 / 静态检查（Step 2）

全部在 `/tmp/acceptance/tree`（HEAD 导出）执行：

| 时间 | 步骤 | 命令 | 结果 |
| --- | --- | --- | --- |
| 15:05:20 | 格式 | `gofmt -l ./cmd ./internal` | **空输出，exit 0** |
| 15:05:24 | 静态检查 | `go vet ./...` | **PASS**，exit 0，wall **11.7s** |
| 15:05:36 | 构建 | `go build ./...` | **PASS**，exit 0，wall **2.5s** |
| 15:05:43 | 零 cgo 四目标交叉编译 | `sh scripts/check-nocgo.sh` | **PASS**：cgo 依赖 0；linux/amd64、linux/arm64、darwin/arm64、windows/amd64 全部 ok；wall **46.8s** |
| 15:08:21 | 快速 CI 门禁 | `sh scripts/ci.sh --fast` | **CI PASS**，exit 0，wall **59.4s**（gofmt→vet→build→go test→nocgo→`TestProductionToolsInvocable` 全过；`--fast` 按设计跳过 coverage/race） |

原始日志：`/tmp/acceptance/10-build-static.log`、`11-nocgo.log`、`22-ci-fast.log`（ci 末尾输出 `CI PASS`）。

**判定：PASS。**

---

## 3. 测试（Step 3）

| 时间 | 步骤 | 结果 |
| --- | --- | --- |
| 15:06:35 | `go test -count=1 ./...` | **exit 0**，wall **46.4s**；**18 个包**（17 个 `ok` + `cmd/zeokeel` no test files）；无 FAIL。热点：`internal/session 34.644s`、`internal/shell 38.561s`、`internal/tools 7.897s`、`internal/app 1.192s` |
| 15:07:33 | `go test -count=1 -v ./internal/shell`（shell 全量） | **exit 0**，wall **40.1s**（包耗时 38.159s）；**281 个顶层 PASS / 0 FAIL / 0 SKIP**（282 个 `func Test*` 含 1 个 `TestMain`，故 runnable=281） |
| 15:08:21 | `sh scripts/ci.sh --fast` 内重复一次 `go test -count=1 ./...` | 再次全绿（见 §2） |

测试规模（HEAD）：`internal/shell` 50 个测试文件 / 282 个 `Test*`；全仓 163 个测试文件 / 868 个 `Test*` 源函数（含 4 个 `TestMain`；`internal/e2e` 1 个测试为 `//go:build e2e` 可选真模型用例，普通 `go test ./...` 不运行，属设计内）。
对比调研基线（[01-code-audit](../research/01-code-audit.md)：838 个 Test 函数；shell 39.4–41.1s）：**测试数 +30，shell 耗时 38.1–38.6s（略降）**。

### 工作树污染检查（预览测试不写仓库）

- 隔离副本执行完全部测试/CI 后，与 15:09:25 重新导出的干净 HEAD 副本做 `diff -r` → **exit 0（逐字节一致）**，证明普通测试与预览生成测试**没有写工作树**。
- 机制证据：`internal/shell/livetest_helpers_test.go:36-45` 的 `previewDir(t)` 默认写 `t.TempDir()`，只有 `ZEOKEEL_UPDATE_PREVIEWS=1` 才写 `docs/design/preview/`；本次验收未设置该变量。
- 对比：**live 工作树**在 15:09 后被并行任务改脏（非测试产物，见 §8）；任务要求的"运行后 git status 干净"需在并行任务提交后的最终 HEAD 上复核（本报告 §7 列为收口前置项）。

**判定：PASS（测试全绿 + 无工作树污染；live 树漂移见 §8）。**

---

## 4. 品牌 / 原生覆盖（Step 4）

### 4.1 预览图（18 张齐全，已提交）

- `docs/design/preview/` 共 19 个 tracked 文件 = **18 张 PNG + README.md**；18 张全部为 `PNG 1600 x 1000, 8-bit RGB`（`file` 校验），目录 1.8MB。
- 覆盖 9 个界面区域 × 浅/深：Home、Session、Sidebar、Settings、Plugins、Automation、Trajectory、RightSidebar、RightSidebar-Files。
- 生成测试 8 个（全部在 shell 全量运行中 PASS）：

```text
TestRenderHomeNativePreview / TestRenderSessionNativePreview / TestRenderSidebarNativePreview
TestRenderSettingsNativePreview / TestRenderPluginsNativePreview / TestRenderAutomationNativePreview
TestRenderTrajectoryNativePreview / TestRenderRightSidebarNativePreview
```

- 独立抽检 4 张（Home 浅/深、Session 深、RightSidebar 浅）：均为真实 UI 渲染（非空帧/非占位），品牌一致（ZeoKeel 字标 + 品牌蓝 accent + 圆角卡片 + 4 DIP 网格），浅/深色对比正常，无 DSH 文案/鲸鱼/Preview 徽章；Home 使用新 tagline「今天想构建什么？」，Session 显示 markdown 代码块 + Copy、工具卡摘要、usage footer。

### 4.2 品牌主题与 design 派生

| 检查 | 证据 | 结果 |
| --- | --- | --- |
| `internal/brand` 存在 | `brand.go`、`palette.go`、`brand_test.go`（生产 306 行） | ✅ |
| design 派生自品牌 | `internal/design/tokens.go:101/104` `fromPalette(brand.LightPalette()/DarkPalette())`；`:150-158` `Tokens.Theme()` 返回 `brand.LightTheme()/DarkTheme()`；`components.go`/`font.go` 亦 import brand | ✅ |
| 品牌测试 | `go test ./internal/brand ./internal/design` → 5 + 10 个 Test 全过（含 logo SVG / wordmark 资产校验） | ✅ |
| 无 DSH 产品文案 | grep `Into the Unknown`/鲸/whale/`DeepSeek Harness` → 0；hero 为 `ZeoKeel` wordmark + `home.tagline`；"Preview" 仅剩文件预览功能文案 | ✅ |
| 残余 DSH 采样色 | `grep ui.Hex / ui.RGBA internal/shell`（生产，7 处，见下表） | ⚠️ 1 类残留 |
| 魔法几何 | 生产代码已无 845/877/909 等像素常量（session.go 改为 `180 * design.Space1 = 720 DIP`；layout.go 仅剩历史注释 "old 845/877 fixed pair"） | ✅ |

残余硬编码颜色分类（7 处）：

| 位置 | 值 | 分类 |
| --- | --- | --- |
| `home.go:272,274`（hero scrim/阴影） | `rgba(0,0,0,.45)` / `rgb(15,17,21,.08)` | **合理**：阴影/遮罩不属于主题令牌 |
| `session_live.go:256`、`settings_ui.go:246`、`sidebar_hover.go:50`（阴影） | 半透明黑阴影 | **合理**：品牌阴影基色 |
| `plugins.go:34-35` `pluginMoreLabel/pluginMoreLabelDark` | `#6E7278` / `#C2C6CA` "More" 标签色 | **不太合理**：DSH 采样色残留，应改 `t.LabelSecondary/labelTertiary` 令牌；`a11y_fixes_test.go:316` 仍在 pin 这两个常量（改动需同步测试） |

残余 DSH/baseline **注释**：生产 `.go` 中 `DSH` 163 处、`baseline` 50 处（含 `i18n.go`/`plugin_details.go`/`motion.go` 等仍描述已拆除静态路径的注释）。绝大多数是**功能兼容性说明**（快捷键/菜单位/行为对齐，按 ADR/任务口径可保留），少数是**已失效的静态屏/像素注释**（task-24「DSH 注释/死代码清理」正在进行中，工作树已有对应改动）。不改变 objective① 的代码拆除结论，但建议收口前确认 task-24 落地。

### 4.3 原生控件替换覆盖（与调研基线对比）

调研基线（[02-mygo-native](../research/02-mygo-native.md) §2.1）：`Sidebar/Select/Tabs/Collapsible/Table/Tree/List/MenuButton/Toolbar/Badge` 等高层控件调用数**全为 0**。HEAD 实测（`grep -rho "ui.<Control>(" internal/shell/*.go`，排除测试）：

```text
ButtonBase 48   Button 40   Popover 15   Modal 15   TextInput 12   Scroll 12
PrimaryButton 8 Switch 4    SidebarItem 4 TextArea 2  SearchField 2
Sidebar 1 Select 1 SegmentedBase 1 Form 1 Field 1 Checkbox 1 Badge 1
Spinner 1 SplitPercent 1
```

仍在手搓/未替换（0 次）：`Tabs / Toolbar / Collapsible / Table / Tree / List / MenuButton / Tooltip / Toast / Slider / Stepper / Progress / Meter / Avatar`。其中 Tabs/Toolbar/Collapsible/List 属 native-ui §16 未列的下一阶段项；工具体文本仍为手绘 `ui.Text`（task-20 进行中）。

**判定：品牌主题 PASS；原生控件替换 PARTIAL（核心屏已用原生 Button/Sidebar/TextArea/Select/Switch/Modal/Popover，Tabs/List/Tree/Collapsible 等仍未替换）；预览覆盖 PASS（UX 观感终评见 task-25，本报告只做存在性/抽检）。**

---

## 5. 桌面交互本地化（Step 5）

| 能力 | 实现证据（HEAD b3ff65f） | 测试证据（全部 PASS） | 状态 |
| --- | --- | --- | --- |
| **系统菜单栏** | `internal/app/menu.go`（142 行）：`mygo.NewMenu` + `RoleAppMenu/RoleFileMenu/RoleEditMenu/RoleViewMenu/RoleWindowMenu`，accelerator 来自 shell 命令表 `menuCommandItem`；ADR 0003 | `TestMenuBarStructure`、`TestMenuCommandAccelerators`、`TestMenuDispatchRunsShellCommand`、`TestMenuDispatchQuit`、`TestMenuActionHookSeesEveryCommand`、`TestMenuLabelsFollowShellLanguage`、`TestMenuUnknownActionDoesNotPanic`、`TestInstallMenuWithoutWindowIsSafe`（8/8 PASS，15:11:48） | ✅ P0 完成；真实平台菜单外观未验证（无显示器） |
| **窗口状态持久化** | `internal/app/window_state.go`：`mygo.WindowStateStore` JSON 原子写、损坏 fallback；ADR 0003 | `TestWindowStateStoreRoundTrip/MissingFile/CorruptFile/DropsUnusableEntries/EmptyStates`（5/5 PASS） | ✅ P0 完成 |
| **系统通知（完成/失败/审批）** | `internal/app/notify.go`：Snapshot 相位状态机、按 session+相位去重、不活跃才通知、点击聚焦/开会话、中英双语、隐私（不带正文）；ADR 0004 | 13 个 `TestNotification*` + `TestSystemNotifier*` + `TestWindowActiveWithoutWindowIsSafe` 等 PASS（15:11:48） | ✅ P0 完成；**后台会话通知 P1 未完成**（ADR 0004 §遗留 1：`Controller.OnUpdate` 只派发当前会话；task-22 进行中）；系统通知外观/权限无真机验证 |
| **系统偏好 / DPI** | `internal/shell/preferences.go`：TextScale 与用户字号相乘、HighContrast 令牌增强、ReduceMotion 全局 `DisableAnimations`；§12 文档 | `TestTextScaleScalesThemeAndConversationText`、`TestHighContrastStrengthensThemeAndTokens`、`TestPreferencesReduceMotionDisablesAnimations`、`TestPreferencesScrollbarPolicy`、`TestPreferencesKeepBrandAccent`、`TestTextScaleRoundsToWholeDips`、`TestHighDPIRenderSmoke`（Home/Session 2x = 3200×2000，控件在窗内）全部 PASS（15:11:50） | ✅ P0 完成；type scale 固定档位不随 TextScale 缩放（§12.4 遗留 1）、滚动条仅 hover overlay（§12.3） |
| **文本可选 + 复制** | 消息/Plan 走 mygo `ui/markdown`，库内 `view.go:202 root.Selectable()` 提供选择/复制（含 Copy as Markdown/HTML、代码块 Copy）；`internal/shell/markdown.go` 薄封装 | 无项目级"选择/复制"断言；间接证据：`TestAssistantMessageCopyRetryAndFeedback`（Copy 按钮）、markdown 渲染测试 PASS | ⚠️ **PARTIAL**：assistant markdown 可选；**用户消息气泡、工具卡详情/结果为普通 `ui.Text` 不可选**（toolcards 由 task-20 进行中）；右键 Copy/Select All 未验证 |
| **设置独立窗口（P1）** | HEAD **不存在**：无 `internal/app/settings_window.go`、无 `docs/decisions/0005*`；task-21 进行中，工作树已有未提交 `internal/app/settings_window.go` + `settings_window_test.go` + ADR0005 | 无（未提交，不属 HEAD 验收范围） | ❌ **未完成（本轮进行中）** |
| **滚动条常显** | `scrollbarPolicyForPreferences` 记录策略；shell 无 `Element.Scrollbars()` 调用 | `TestPreferencesScrollbarPolicy` PASS | ⚠️ 未实现常显（mygo v0.7.4 仅 hover overlay，§12.3 已记录为框架限制） |
| **IME / 屏幕阅读器真机** | 无项目级 IME/VoiceOver 代码或测试；依赖 mygo `TextArea/TextInput` 原生 IME；ADR0003/0004 均把真机验证列为遗留 | 无 | ❌ **证据不足**（无显示器环境，需真机矩阵） |

菜单/窗口状态/通知/偏好四类能力的单测覆盖充分（15:11:48–15:11:50 定向复跑全 PASS）；桌面交互的"真实平台行为"（macOS Dock 菜单、Windows Alt 菜单、通知外观、1.5x/2x 真机 DPI、IME）均缺真机证据，报告中不夸大。

---

## 6. 功能不回退（Step 6）

- 全部 18 个 Go 包构建/测试绿（§2/§3），**0 FAIL、0 SKIP**；无被删除/跳过的功能测试（shell `Test*` 281/281 PASS）。
- 功能资产测试覆盖（HEAD 计数，全部通过）：
  - 审批/权限：6 个 `TestApproval*` + `TestDefaultApprovalGatesMutatingTools` 等；
  - 附件：9 个 `TestAttachment*`；
  - 键盘可达/a11y：17 个 `a11y_*` 测试 + 8 个 `TestKeyboard*`；
  - i18n：`i18n_test.go` 9 个（含 `TestCatalogComplete`/`TestCatalogFixedKeys`/`TestLanguageSwitchRerenders`）；
  - markdown：8 个（含表格/任务列表/流式/代码块，mygo `ui/markdown` 迁移后行为保留）；
  - 工具卡：4 个 `TestToolCard*`/`TestToolCardsVariantRows` + runtime/tools 包的 130 个测试；
  - 工作区/会话/搜索/菜单/快捷键/动效：19 / 70 / 10 / 18 / 13 / 13 个相关测试；
  - App 层 83 个测试（菜单、通知、窗口状态、工具 e2e 接线、MCP、检查点、审批等）。
- 真实模型 e2e 为 `//go:build e2e` 可选（1 个，`scripts/e2e-model.sh` 触发），本次按 CI 定义未运行（无 API key），与 CI 口径一致。
- 功能对齐文档 `docs/FUNCTIONAL-PARITY.md` 维护中（能力矩阵 + 状态列）；本次验收**未**对照真实 DSH 重新逐项实测（无 DSH 实例），"功能对齐"结论以现有测试与文档为准。

**判定：PASS（测试绿色 + 产品功能测试资产完整）；局限：未独立重测 DSH 功能矩阵。**

---

## 7. 裁决：逐条 objective

### Objective ①：拆除 DSH 像素对齐门禁及仅为其服务的代码，保持绿色

**PASS**

证据行：
- `baseline/`、`docs/baseline/`、`artifacts/`、`scripts/cdp.mjs`、`scripts/capture/`、`internal/pixeltest/` 全部不存在（15:04:39）。
- `TestScreensBaseline`/`TestScreensDarkNarrow` 在 Go/脚本/构建文件中 **0 活引用**；仅历史文档说明（24 处）。
- `scripts/ci.sh`、`scripts/evolve/gates.sh` 无像素步骤（仅退役注释）；`scripts/ci.sh --fast` PASS（15:08:21）。
- 静态渲染路径已拆：`App.View` 在 `Ctrl == nil` 时返回空帧（`app.go:246-250`）；上游 `8bf0053` 起删除门禁/基线/静态屏，之后 5 个提交保持全绿。
- 文档口径已改：README/PLAN/STATUS/QUALITY 均声明退役；[UI-PARITY-AUDIT](../UI-PARITY-AUDIT.md)、[COMPONENT-PARITY](../COMPONENT-PARITY.md) 标注"历史文档，已退役"；无活动链接指向已删目录。
- 回滚路径存在：tag `pre-native-ui` + 48.3MB 归档包。

小瑕疵（不影响 PASS）：`plugins.go` 两处 DSH 采样标签色；生产注释中 163/50 处 DSH/baseline 提及（task-24 清理中）；`.gitignore` 三条退役规则。

### Objective ②：用 mygo Theme / 原生控件重做视觉与交互（品牌主题、原生控件替换、桌面交互本地化）

**PARTIAL**

证据行（已达成）：
- 品牌主题：`internal/brand` 存在且 `internal/design` 全面派生（`tokens.go:101/104/150-158`），品牌测试全过。
- 视觉重做：18 张 1600×1000 浅/深预览齐全且由 8 个 `TestRender*NativePreview` 生成（普通测试不写工作树）；首页/会话/侧栏/设置/插件/自动化/Trajectory/右栏均已品牌原生化；无 DSH 产品文案。
- 桌面 P0：菜单栏、窗口状态、通知（当前会话）、系统偏好、2x DPI 全部实现且有 30+ 单测通过（§5）。
- 原生控件：核心屏已使用 Sidebar/PrimaryButton/ButtonBase/TextInput/TextArea/Select/Switch/Modal/Popover/SearchField 等。

缺口（未达成/证据不足）：
- 原生控件替换不完整：Tabs/Toolbar/Collapsible/Table/Tree/List/MenuButton/Tooltip/Toast 仍 0 使用（部分属下一阶段设计）；工具卡内容仍纯文本（task-20 进行中）。
- 桌面 P1 未完成：设置独立窗口（task-21 进行中，HEAD 无实现）；后台会话通知/通知动作（task-22 进行中）；滚动条常显受框架限制。
- 文本可选不完整：仅 markdown 消息可选；用户消息/工具卡文本不可选（task-20 覆盖工具卡）。
- 真机证据缺口：IME、VoiceOver/Narrator、平台菜单外观、通知外观、1.5x/2x 真机 DPI 矩阵均未验证。
- UX 观感终评由 task-25（UX 独立评审，进行中）给出；本报告只做存在性与抽检。

### Objective ③：持续功能对齐与测试绿色

**PASS**

证据行：
- `go test -count=1 ./...`：18 包全绿，wall 46.4s（15:06:35）；shell 281/281 PASS、0 SKIP（15:07:33）；`ci.sh --fast` 复跑全绿（15:08:21）。
- 功能测试资产完整（审批 6 / 附件 9 / a11y 17 / i18n 9 / markdown 8 / 工具卡 4 / 工作区 19 / 会话 70 / 工具 130 / App 83），无功能测试被删或跳过。
- 局限：未对真实 DSH 重新做功能矩阵实测；以 `docs/FUNCTIONAL-PARITY.md` + 现有 e2e/单测证明"无回退"。

---

## 8. 总体建议：能否标记 goal complete

**结论：在 HEAD `b3ff65f` 上无 FAIL；建议"有条件可收口"，但不要在当前漂移工作树上直接标记 complete。**

理由：
1. 本次验收对象是 commit `b3ff65f`；验收期间（15:09 起）工作树被 7 个并行任务改为 dirty（15:13:21 已 31 个文件/未跟踪项），**最终交付状态尚未落盘**，验收结论不能覆盖这些进行中改动。
2. 剩余 P1/收尾项均在既有任务清单内（task-20 工具卡、task-21 设置窗口、task-22 后台通知、task-23 a11y、task-24 注释/基准、task-25 UX 评审、task-26 最终审计），**不需要新增功能需求**；它们完成后重跑本报告 §2/§3 即可收口。
3. objective ①③ 在 `b3ff65f` 已可判定 PASS；objective ② 为 PARTIAL，且其缺口已被 README/`native-ui.md §16` 明确列为"下一阶段"，口径一致、无隐藏欠账。

### 收口前必须完成的项（must-complete）

1. **并行任务落盘并提交**，得到最终 HEAD；在最终 HEAD 上重跑 `gofmt -l ./cmd ./internal`、`go vet ./...`、`go build ./...`、`sh scripts/check-nocgo.sh`、`sh scripts/ci.sh --fast`、`go test -count=1 ./...`（命令见 §9），并确认最终 `git status` 干净。
2. **最终 HEAD 上重跑预览/测试卫生**（`ZEOKEEL_UPDATE_PREVIEWS` 未设置时工作树不被写入；18 张预览与 `TestRender*NativePreview` 仍齐全）。
3. **合并 task-25（UX 评审）与 task-26（最终代码审计）结论**；若 task-20/21/22/23/24 完成后改变了 §4–§6 的事实，需更新本报告对应行。
4. **残余 DSH 清理确认**：`plugins.go:34-35` 采样色（建议换品牌令牌并更新 `a11y_fixes_test.go` pin）与生产注释中 DSH/baseline 引用（task-24 范围）；`Ctrl == nil` 防御守卫 171 处其中疑似死代码部分由 task-24 清理。
5. **真机验证缺口显式接受**：IME / VoiceOver·Narrator / 平台菜单与通知外观 / 1.5x–2x DPI 真机矩阵 / 后台会话通知，建议在收口记录中列为 known limitations（不要声称已验证）。

---

## 9. 复现命令附录

```bash
cd /data/mygo
export PATH=/usr/local/go/bin:$PATH
export GOMODCACHE=/data/mygo/.cache/gomod GOCACHE=/data/mygo/.cache/go-build
export GOSUMDB=off CGO_ENABLED=0

# --- 0) 基线核对 ---
git rev-parse HEAD                 # 期望 b3ff65f96d3222eb916f07248a89882d83cabd7b
git status --short                 # 期望空（并行任务未开始时）
git tag --list                     # 期望含 pre-native-ui
ls -la /data/zeokeel-backups/parity-assets-20261105.tar.gz

# --- 隔离导出（避免并行任务改脏工作树导致结果不可复现） ---
rm -rf /tmp/acceptance/tree && mkdir -p /tmp/acceptance/tree
git archive HEAD | tar -x -C /tmp/acceptance/tree && cd /tmp/acceptance/tree

# --- 1) 仓库卫生 ---
for p in baseline docs/baseline artifacts scripts/cdp.mjs scripts/capture internal/pixeltest; do
  [ -e "$p" ] && echo "EXISTS: $p" || echo "ABSENT: $p"; done
grep -rn 'TestScreensBaseline\|TestScreensDarkNarrow' --include='*.go' --include='*.sh' internal cmd scripts Makefile
grep -n 'pixel\|ScreensBaseline\|ScreensDarkNarrow' scripts/ci.sh scripts/evolve/gates.sh Makefile

# --- 2) 构建/静态 ---
gofmt -l ./cmd ./internal
go vet ./...
go build ./...
sh scripts/check-nocgo.sh
sh scripts/ci.sh --fast

# --- 3) 测试 ---
go test -count=1 ./...
go test -count=1 -v ./internal/shell

# --- 4) 品牌/预览覆盖 ---
ls docs/design/preview/*.png | wc -l                     # 期望 18
file docs/design/preview/*.png | head -3                 # 期望 PNG 1600x1000
grep -rn 'func TestRender.*NativePreview' internal/shell/*_test.go
grep -rn 'zeokeel/internal/brand' internal/design/*.go
grep -rn 'ui\.Hex(\|ui\.RGBA(' internal/shell/*.go | grep -v _test

# --- 5) 桌面交互 ---
ls internal/app/menu.go internal/app/menu_test.go internal/app/window_state.go \
   internal/app/notify.go internal/app/notify_test.go internal/shell/preferences.go
go test -count=1 -v ./internal/app -run 'TestMenu|TestNotification|TestWindowState'
go test -count=1 -v ./internal/shell -run 'TestPreferences|TestTextScale|TestHighContrast|TestHighDPI'

# --- 6) 测试不写工作树（与干净 HEAD 副本逐字节对比） ---
rm -rf /tmp/acceptance/tree-ref && mkdir -p /tmp/acceptance/tree-ref
git archive HEAD | tar -x -C /tmp/acceptance/tree-ref
diff -r /tmp/acceptance/tree /tmp/acceptance/tree-ref && echo "worktree identical: tests wrote nothing"
```

原始日志（仓库外，仅供参考）：`/tmp/acceptance/00-preflight.log`、`10-build-static.log`、`11-nocgo.log`、`20-gotest-all.log`、`21-shell-verbose.log`、`21-shell-summary.log`、`22-ci-fast.log`、`23-tree-diff.log`、`30-targeted-tests.log`。

---

## 附录 A：验收期间工作树漂移（与本次结论无关，但证明"最终状态未定"）

| 时间 | `git status --short` 行数 | 说明 |
| --- | --- | --- |
| 15:04:18 | **0** | 验收基线：工作树 = HEAD b3ff65f |
| 15:09:25 | 17 | 并行任务开始落盘（toolcards/notify/settings window 等） |
| 15:12:04 | 25 | 继续增加（含 `internal/app/settings_window.go`、`docs/decisions/0005*`、`toolcard-native-*.png`、`a11y_native_test.go`、`render_bench_test.go`） |
| 15:13:21 | **31** | 含跟踪文件修改 21 个 + 未跟踪 10 个；对应任务：task-20（toolcards/markdown/preview）、task-21（settings window/ADR0005/settings_ui/app.go）、task-22（notify/ADR0004/controller）、task-23（a11y_native_test/workspaces）、task-24（layout/menus/session/render_bench/perf.md）、task-17 后续（session 预览重生成）、docs 更新（STATUS/native-ui/preview README） |

结论不变：上述改动**不在 `b3ff65f` 验收范围内**；最终收口需在它们提交后的 HEAD 上重跑 §9 的 1)–6)。
