# 04 · Parity 测试清理与剩余静态依赖迁移清单

> 角色：parity 测试清理工程师（Team 成员 `parity-tests`）· 任务：`task-5`
> 时间：2026-11-05 13:10–13:35（Asia/Shanghai）· 仓库：`/data/mygo`
> 背景：用户决策停止 DSH 像素级对齐（功能对齐 + mygo 原生视觉）。Lead 正在拆除 `internal/shell` 五个静态屏（`Ctrl == nil` 静态渲染路径）；队友 `parity-cleanup` 删除 `internal/pixeltest`、`baseline/`、`artifacts/`；队友 `native-theme` 在改 `internal/design` 令牌。
> 本文只记录测试侧清理结果与迁移清单；**不修改生产代码**，不 git add/commit。
> ⚠️ 快照时点：Lead 的静态屏拆除仍在进行中（`App.View` 已改为 `Ctrl == nil` 时返回空帧，但 121 处 `Ctrl == nil` 分支尚未清理完）。编译错误/静态依赖数据是 **2026-11-05 13:30 左右**的快照，重构完成后可能进一步变化。

---

## 0. 结论速览

| 指标 | 数值 |
| --- | --- |
| 删除的 parity 测试文件 | **7 个**（`internal/shell/*_test.go`） |
| 删除的 `Test*` 函数 | **17 个**（整文件删除 11 个 + 裁剪删除 6 个） |
| 被裁剪但保留文件的测试（断言级） | 3 个文件：`dark_contrast_test.go`、`fixer_regression_test.go`、`fixer_regression2_test.go` |
| 清理后 `internal/shell` 测试规模 | 49 个文件 / 270 个 `Test*`（清理前 56 / 287） |
| 仍依赖静态 fixture 的测试函数 | **41 个**（16 个文件，其中建议删除 11 个 / 迁移 30 个） |
| 仅用零值 App 但**不渲染**静态屏的函数 | 4 个（3 个保留、1 个建议删除） |
| 当前编译错误 | **1 个文件 / 3 个符号**（`fixes_shell_test.go` 依赖 Lead 已删除的 `sess*Surface`） |
| 仍 import `internal/pixeltest` 的受控测试文件 | **0 个**（清理前 6 个，全部已处理；`artifacts/overlay/zz_verifier_overlay*.go` 属被删的 `artifacts/`，已随目录消失） |

---

## 1. 已删除的 parity 测试文件（7 文件 / 11 Test 函数）

删除前均逐文件读过断言：全部是 DSH baseline 像素比对、DSH 几何/颜色测量 pin、或为 parity 回路服务的截图输出，没有纯产品行为断言。

| 文件 | 删除的 `Test*` 函数 | 判定依据（断言内容） | 随文件删除的 helper |
| --- | --- | --- | --- |
| `internal/shell/screens_test.go` | `TestScreensBaseline` | `pixeltest.CompareExcluding` 对比 `baseline/screenshots/*.png`，masked/raw 预算门禁（home 2.0%/session 8.5%/…） | — |
| `internal/shell/baseline_dark_test.go` | `TestScreensDarkNarrow` | DSH 深色/窄屏 baseline 像素 diff + 固定像素坐标采样（x=280 分界、BgStrong/BgRaised 颜色） | `darkMasks`、`darkCase`、`assertDarkSurfaces`、`rgbaOf`（迁移到 `dark_contrast_test.go`）、`scrimmed` |
| `internal/shell/capture_live_test.go` | `TestCaptureLiveSession` | 只把 live 截图写到 `artifacts/live/` 供人工/后续 parity 比对，无产品断言 | — |
| `internal/shell/icon_parity_test.go` | `TestStaticHomeIconParity` | 与 DSH baseline 做图标 crop 像素 diff（plugins jigsaw / folder-plus） | — |
| `internal/shell/live_screens_test.go` | `TestLiveScreensRenderAndDifferFromStatic` | 断言 live 渲染与 static 渲染"必须不同"（parity 回路补丁），并写 artifacts | `itoa` |
| `internal/shell/sidebar_geometry_test.go` | `TestStaticSidebarGeometry`、`TestWorkspaceHeaderTargets`、`TestSelectedWorkspaceKeepsSidebarFill`、`TestStaticSessionRowsAreFlat`、`TestNewSessionButtonTokens` | DSH 基线测量值 pin：x=14/y=70/w=252/h=38、行 y=316、命中区 176/208/240、`#F9FAFB`/`#E0E0E0` 颜色 pin、字重像素计数 | — |
| `internal/shell/sidebar_divider_test.go` | `TestSidebarDividerUsesBorderL3` | 1px 侧栏分隔线颜色 pin（`(279,450)` 必须等于 BorderL3 合成值、`(280,450)` 必须 BgBase） | — |

合计：**7 文件 / 11 个 `Test*`**。删除后这 7 个文件不再存在。

---

## 2. 逐函数裁剪：`fixer_regression*.go` 与 `dark_contrast_test.go`

### 2.1 `internal/shell/fixer_regression_test.go`（13 → 9 个 Test，删除 4 个）

parity 修复循环产物，混合了真实功能回归与 DSH 几何/基线断言。处理原则：保留产品行为/a11y，删除"DSH 像素/几何 pin"。

| 删除的函数 | 原因 |
| --- | --- |
| `TestNarrowRightPanelWidth` | DSH 520px docked panel / x=680 的几何 pin |
| `TestNarrowContentColumns` | DSH 1200×800：composer 708px、x≈381 的几何 pin |
| `TestNarrowPageColumns` | DSH 1200×800：Plugins/Automation 824px 列、48px gutter 的几何 pin |
| `TestNewSessionIconCrop` | 与 DSH baseline 的图标 crop 像素 diff |

保留的 9 个：`TestSplitLeavesFullscreen`（Split/Fullscreen 互斥行为）、`TestInlineSearchIsNotADialog`（内联搜索行为）、`TestViewOptionsBaselineGroups`（只留 9 个选项标签，**删除 268px popover 几何 pin**）、`TestSettingsHeaderActionsHaveAccessibleNames`（a11y）、`TestSetDefaultPresetKeyboardReachable`（a11y）、`TestSearchDialogChipsAndRowsReachable`（a11y）、`TestAddWorkspaceRowRemoved`（重复控件行为）、`TestComposerPermissionUsesSessionMode`（权限模式行为）、`TestCredentialFieldsAreMasked`（密钥掩码安全行为）。

- 移除 `internal/pixeltest` import；新增本地 `differingPixels()`（语义等价 `pixeltest.Compare(tolerance=0)`）。
- `TestCredentialFieldsAreMasked` 依赖静态 Settings fixture（`&App{Screen: Settings}` + `shellUI/pluginUI` 草稿态）。`App.View` 已改为 `Ctrl == nil` 返回空帧后它会"两帧都空 → 假通过"，故加了 **skip 守卫**：检测到静态渲染不再绘制草稿态时 `t.Skipf`，并指向本清单；安全断言本身保留（产品资产）。

### 2.2 `internal/shell/fixer_regression2_test.go`（12 → 10 个 Test，删除 2 个）

| 删除的函数 | 原因 |
| --- | --- |
| `TestLiveSessionColumnGeometry` | DSH live 列几何 pin：transcript 845、composer 877、docked 529/561/357/329/361 等十余处 `near(...)` |
| `TestSettingsFontSizeStepperGeometry` | DSH 129×36 控件 / 17×12 箭头 / x=1094 等几何 pin |

- 删除仅被上述两测试使用的 `near()` helper。
- `TestLiveMentionListboxGeometryAndKeyboard`：删除 mention 行宽 ≥850、行高 34 的 DSH pin，保留 Down/Enter、Tab/Enter 键盘选择行为（函数名保留，注释标注已退役的几何 pin）。
- `TestDarkLiveOffSwitchContrast`：删除 off track `#3B3B3C` 颜色区间、`switchTrack` 令牌身份 pin；保留渲染像素对比度断言并改为通用标准 **WCAG 2.1 SC 1.4.11 非文本对比度 ≥ 3:1**（原 7:1 是 DSH 令牌 pin 的产物）。
- 保留其余 10 个功能测试（审批卡布局容纳、右栏 collapse 28×28 点击区、mention 键盘、Send 命中区、跨工作区点击、Settings 草稿作用域、快捷键错误提示、usage 不贴右边等）。

### 2.3 `internal/shell/dark_contrast_test.go`（7 个 Test 全部保留，仅断言级裁剪）

该文件确有通用 a11y 价值，故**不整文件删除**：

| 改动 | 说明 |
| --- | --- |
| 保留 `TestDarkContrastRatios` / `TestPrimaryButtonLabelContrastInCards` / `assertLabelContrast` / `contrastRatio` / `luminance` | 通用 WCAG 对比度断言（不是 DSH 像素 pin），品牌主题仍需满足 |
| `TestDarkPrimaryActionContrast`：删除 `tokens.OnPrimary ≤ 0x50` 的**令牌值 pin** | 保留"暗色主按钮标签确有暗色像素（可见）"的 a11y 断言 |
| `TestRightSidebarHeaderFitsPanel`：删除 **DSH 720px 面板宽度 pin** | 保留"四个 tab + New tab/Split/Fullscreen 都在面板内、非零尺寸"的 live 行为断言 |
| `TestNarrowLayoutKeepsControlsVisible` / `TestNarrowPluginsHeaderFits` / `TestSettingsLabelsDoNotTruncate` | **静态 fixture 行为测试，保留**并加 `TODO(static-fixture)`；见 §3-B（需迁移 live） |
| helper | `rgbaOf()` 从被删的 `baseline_dark_test.go` **迁移到本文件**（`scrim_test.go`、`verified_ui_test.go` 仍在使用） |

### 2.4 helper 迁移/删除汇总

| helper | 原位置 | 处理 |
| --- | --- | --- |
| `rgbaOf` | `baseline_dark_test.go` | 迁移到 `dark_contrast_test.go`（`scrim_test.go`、`verified_ui_test.go` 使用） |
| `darkMasks` / `darkCase` / `assertDarkSurfaces` / `scrimmed` | `baseline_dark_test.go` | 随文件删除（无其他引用） |
| `itoa` | `live_screens_test.go` | 随文件删除（无其他引用） |
| `near` | `fixer_regression2_test.go` | 删除（仅被两个已删几何测试使用） |
| `differingPixels` | 新增于 `fixer_regression_test.go` | 替代 `pixeltest.Compare` |

---

## 3. 仍依赖静态 fixture 的测试清单

**判定口径**：`&App{Screen: ...}` / `&App{}`（未设置 `Ctrl`，即 `Ctrl == nil`）+ `ui.NewTester(app.View...)` / `ui.Render(app.View...)`，或直接调用只在静态路径下才有意义的 production helper。**只读 grep + 逐函数读断言**得到，不按文件名判断。

统计：**16 个文件 / 41 个静态渲染测试** + 4 个"零值 App 但不渲染"的函数。当前 `App.View` 在 `Ctrl == nil` 时返回空帧（`app.go:224` 起），所以下表 A/B 的渲染型测试现在都会失败/假通过——**这不代表断言没价值**，而是需要"删除"或"迁移 live fixture"。

### A. 建议删除（纯静态视觉/基线契约断言，13 个函数）

> 其中 12 个来自上文"静态 App fixture"扫描（11 个静态渲染 + `models_ui_test.go` 的 1 个不渲染标签 pin）；`fixes_shell_test.go` 的 `TestDarkStaticTranscriptContrast` 依赖已删静态符号（不渲染），单列为编译阻塞项。

| 文件 | `Test*` 函数 | 依赖的静态符号/断言 | 理由 |
| --- | --- | --- | --- |
| `chrome_test.go` | `TestChromeStaticPathUnchanged` | `&App{Screen: Session, sidebarCollapsed, rightOpen, rightSplit, rightFullscreen, wsHoverID}` | 断言"静态路径忽略 live chrome 标志、baseline 保持不变"= 静态 baseline 契约 |
| `fixes_shell_test.go` | `TestStaticSettingsLanguageIsEnglish` | `&App{Screen: Settings}` + View | 静态 demo 自洽性（像素基线用的英文界面） |
| `fixes_shell_test.go` | `TestStaticSessionUsesSVGIcons` | `&App{Screen: Session}` + View | 静态 session 文本字形 pin |
| `fixes_shell_test.go` | `TestStaticSendFillIsOpaque` | `&App{Screen: Home}` + View | baseline `#B2C7F4` 颜色 pin |
| `fixes_shell_test.go` | `TestDarkStaticTranscriptContrast` | `sessUserSurface` / `sessCardSurface` / `sessCodeSurface`（**Lead 已从 `session.go` 删除**） | 静态 transcript 暗色 fallback 令牌；**当前编译错误来源** |
| `memory_ui_test.go` | `TestMemoryNavCellIsStaticSafe` | `&App{Screen: Settings}` + View | 明确为"静态设置不出现 Memory 行，像素基线不变"的契约 |
| `modes_ui_test.go` | `TestAgentModeStaticSessionRendersStandardChip` | `&App{Screen: Session}` + View | 静态 session chip pin |
| `models_ui_test.go` | `TestModelBadgeStaticKeepsBaselineLabel` | `&App{}` + `DefaultModelLabel()`（不渲染） | 静态 baseline 标签 `"DeepSeek-V4.1-Flash"` pin |
| `render_test.go` | `TestRenderShell` | `(&App{}).View` + 两个 baseline 锚点颜色 + 写 `artifacts/m0-shell.png` | 像素管线"第一环"，纯像素断言 |
| `right_sidebar_test.go` | `TestRightSidebarLiveOnly` | `&App{Screen: Session, rightOpen, rightTab}` + View | 静态路径永不渲染右栏的契约 |
| `team_ui_test.go` | `TestTeamPanelStaticPathHidden` | `renderTeamPreview(&App{})`（test helper）+ View | 静态路径不渲染 Team 面板的契约 |
| `verified_ui_test.go` | `TestBaselineMarginsCompareCollapseControlAndRowFill` | `&App{Screen: Home}` + `design.Light().BgSidebar` 像素扫描 | 为像素 baseline mask 辩护的边距/填充断言 |
| `verified_ui_test.go` | `TestStaticHeaderIcons` | `&App{Screen: Session}` + `sessModeIcon`/`liveHeaderPanelIcon`/`icons.PanelLeft` | 静态 header 图标 pin |

### B. 建议迁移 live fixture（30 个函数 / 10 文件）

| 文件 | `Test*` 函数 | 难度 | 迁移说明 |
| --- | --- | --- | --- |
| `dark_contrast_test.go` | `TestNarrowLayoutKeepsControlsVisible`、`TestNarrowPluginsHeaderFits`、`TestSettingsLabelsDoNotTruncate` | 低 | 断言是真实窄窗行为（控件不出窗、不重叠、标签不截断）；改用 `newLiveApp` + 设置 `h.app.Screen` |
| `fixer_regression_test.go` | `TestCredentialFieldsAreMasked` | 中 | 安全行为资产；需 live Settings：`Cmd+,` → Models/Plugins 详情 → 填两个等长 key 比较渲染（或生产侧提供 draft 注入钩子） |
| `fixes_shell_test.go` | `TestSettingsParityRowsPresent` | 低 | 后半段本来就有 live 断言；把前半段静态 Settings 换成 `newLiveApp`+`Cmd+,` 即可 |
| `i18n_test.go` | `TestStaticAppEnglish`、`TestLanguageSwitchRerenders` | 低 | i18n 产品行为；live 侧已有 `TestLiveLanguageDefault`，这两条可改用 live fixture 渲染 en/zh 文案 |
| `interaction_test.go` | `TestSidebarNavigation` | 低 | 纯交互骨架（点击 Plugins/Automation/会话 → Screen 变化），`newLiveApp` 直接替换 |
| `markdown_test.go` | helper `m6MarkdownApp` + `TestMarkdownRendersHeadingsEmphasisAndLists`、`TestMarkdownPreservesUnsupportedConstructs`、`TestMarkdownMalformedInlineKeepsCharacters`、`TestMarkdownUnclosedFenceKeepsBody`、`TestMarkdownPlainParagraphLineHeight`、`TestUsagePillsSegments`、`TestBackToBottomAppearsAndScrolls` | 中 | **markdown 是产品资产**。helper 用 `&App{Screen: Session}` + `SetSnapshot` 走旧的"无 Ctrl 也渲染 snapshot"路径，现已失效；需 live controller（或生产侧保留 snapshot-only 渲染钩子） |
| `message_actions_test.go` | `TestLiveUsageFooterCompletedTokPerSecondAndCache`、`TestLiveUsageFooterOmitsUnmeasurableRate`、`TestTurnNavigationScrollsToTurn` | 中 | 行为资产（usage 时钟/速率、turn 跳转）；需要"可控 snapshot + 可控时钟"的 live fixture，合成 Usage 数值无法简单从真控制器得到 |
| `motion_test.go` | `TestMotionWorkingIndicator`、`TestMotionLiveSettlesToRestFrame`、`TestMotionStreamingFadeFrames`、`TestMotionStreamReduceMotion`、`TestMotionStreamIdleHasNoFrames`、`TestMotionToolCardExpandCollapse`、`TestMotionToolDotPulse`、`TestMotionRunButtonState`（8 个；其余 4 个为纯函数/令牌测试不受影响） | **高** | 按 Lead 指示**本轮保留**交下一波：依赖静态 session 渲染 + 合成 snapshot + 动画时间推进，迁移成本最高 |
| `toolcards_test.go` | `TestToolCardsVariantRows`、`TestToolCardExpandShowsVariantDetail` | 中 | 工具卡是产品资产；`m6ToolSnapshot()` + 静态 App 渲染需换成 live controller 注入（或 snapshot 钩子） |
| `verified_ui_test.go` | `TestDarkSendControlUsesThemeFill`、`TestNarrowSessionTranscriptStaysInsideWindow` | 中 / 低 | 前者断言暗色 Send 填充走主题 helper（品牌主题落地后建议只留"暗色≠浅色且可见"）；后者是窗口边缘裁切行为，live fixture 直接迁移 |

### C. 零值 App、但不做静态渲染（保留，非 parity 依赖）

| 文件 | 函数 | 处理 |
| --- | --- | --- |
| `i18n_test.go` | `TestFormatAndLookupFallback`、`TestPersistencePath` | 纯 `T()`/settings 逻辑单测，保留 |
| `start_tab_test.go` | `TestStartUIStateIsPerApp` | 纯 per-App 状态 map 单测（`startUI()`），保留 |

> 说明：`dark_contrast_test.go` 的 `renderDarkComponent` helper 用 `&App{}` 直接调用 production 组件函数（如 `addPluginButton`/`automationHeadingLive`），不经过 `View` 静态屏路径，因此**仍可工作**，不计入"静态屏渲染"依赖。

---

## 4. 依赖 Lead 拆除的静态符号 / 编译错误汇总（13:30 快照）

### 4.1 编译错误（`go vet ./internal/shell` / `go test -run '^$' ./internal/shell`）

```text
# github.com/zeokeel/zeokeel/internal/shell [github.com/zeokeel/zeokeel/internal/shell.test]
internal/shell/fixes_shell_test.go:347:24: undefined: sessUserSurface
internal/shell/fixes_shell_test.go:348:24: undefined: sessCardSurface
internal/shell/fixes_shell_test.go:349:24: undefined: sessCodeSurface
FAIL	github.com/zeokeel/zeokeel/internal/shell [build failed]
```

- 引用点：`fixes_shell_test.go` 的 `TestDarkStaticTranscriptContrast`（静态 transcript 暗色 fallback 表面）。
- Lead 已在 `internal/shell/session.go` 删除 `sessUserSurface` / `sessCardSurface` / `sessCodeSurface`（连同 `sessFaint`/`sessCardBg`/`sessCodeBg`/`sessUserBg`/`sessPlusBg` 等静态颜色）。
- **建议动作：直接删除 `TestDarkStaticTranscriptContrast`**（纯静态视觉断言，无行为价值）；删掉后本包恢复可编译（其余错误为 0）。
- 其余：`go test -run '^$' ./internal/shell` 只报以上 3 条，没有别的编译错误。

### 4.2 已发生的运行时失效（编译通过但断言失效）

| 静态符号/入口 | 现状 | 影响 |
| --- | --- | --- |
| `App.View` 的 `Ctrl == nil` 路径 | Lead 已改为**返回空帧**（`app.go:224`：`if a.Ctrl == nil { ui.Row(c).Fill(); return }`） | §3 表 A/B 的 41 个静态渲染测试不再渲染任何内容：删除类会红/假绿，迁移类需改 fixture |
| `m6MarkdownApp` / `m6ToolSnapshot` / message_actions 自造 snapshot | 依赖"无 Ctrl 但 `SetSnapshot` 仍渲染旧静态 transcript"的旧行为 | 这些"live 命名 + 静态 fixture"的测试失效（见 §3-B） |
| 静态契约字段（`sidebarCollapsed`/`rightOpen`/`rightSplit`/`rightFullscreen`/`wsHoverID`/`Screen` 对静态 App 的意义） | `View` 不再读取（空帧提前返回） | §3-A 的契约断言全部失去意义 |

### 4.3 仍需关注的生产符号（当前编译通过，但 native-theme/Lead 后续可能删除）

这些被测试直接引用，若后续清理设计令牌层需同步改/删测试：
`homeSendIdle`（`home.go` 常量，`verified_ui_test.go`）、`homePreviewColors`/`homeTagAccent`（`fixes_shell_test.go` 的 `TestDarkHomeTokens`）、`sendIdleFill`/`switchTrack`（`composer_style.go`）、`sessModeIcon`/`liveHeaderPanelIcon`（`verified_ui_test.go` 的 `TestStaticHeaderIcons`，该测试建议删除）、`ConversationLineHeight`（`markdown_test.go`）、`rgbaOf` 的调用方 `scrim_test.go`/`verified_ui_test.go`。

---

## 5. 其他 parity 残留（不在 §3 表格但建议下一波一并处理）

| 位置 | 内容 | 建议 |
| --- | --- | --- |
| `internal/shell/session_header_metrics_test.go`（整文件） | `TestLiveSessionHeaderMatchesStaticMetrics`：把 live header 钉到 static 76px 块（y=75 边框、mode chip 22px、"▾" 字形禁止等） | **整文件删除**（live fixture 但断言目标是 DSH/static 度量，属 parity） |
| `internal/shell/scrim_test.go` | `TestSessionSearchScrimIsThemeAware`：主题感知行为有价值，但内含 DSH 精确样本 pin `(10,10,11)`（dark scrim 0.5 over #151517） | 保留行为断言（浅/深 scrim 不同、等于 `Scrim().Over(BgBase)` 合成），删除硬编码样本 |
| `internal/shell/workspaces_test.go` | `TestLiveSelectedWorkspaceKeepsSidebarFill`：live 选中态填充色断言 | 可保留（交互状态可辨识），品牌主题落地后复核阈值/颜色来源 |
| `internal/shell/fixes_shell_test.go` | `TestDarkHomeTokens`：Preview badge/tag 暗色对比度 ≥4.5 | 通用 a11y 对比度，保留 |
| `internal/shell/a11y_fixes_test.go` | `TestDarkAccentBadges` 等 | 通用 a11y，保留 |

---

## 6. 给 Lead 的优先级建议（前 10 个文件）

排序原则：先解除编译阻塞 → 再删"纯静态视觉/契约"（低风险、快）→ 最后迁移有产品价值的行为测试。

| # | 文件 | 动作 | 原因 | 工作量 |
| --- | --- | --- | --- | --- |
| 1 | `internal/shell/fixes_shell_test.go` | 删 `TestDarkStaticTranscriptContrast` + 3 个 `TestStatic*` 静态视觉 pin；`TestSettingsParityRowsPresent` 迁 live | **当前唯一编译阻塞**（`sess*Surface` 已删）；删完包可编译，是后续所有验证的前置 | 低 |
| 2 | `internal/shell/render_test.go` | 整文件删除 | 像素管线"第一环"：baseline 锚点颜色 + 写 `artifacts/`，无产品行为 | 低 |
| 3 | `internal/shell/session_header_metrics_test.go` | 整文件删除 | live header 对齐 static/DSH 度量的 parity pin，与已删 `live_screens_test.go` 同类 | 低 |
| 4 | `internal/shell/chrome_test.go` | 删 `TestChromeStaticPathUnchanged`（其余 2 个 live 测试保留） | 静态路径契约，`View` 空帧后无意义 | 低 |
| 5 | `right_sidebar_test.go` + `memory_ui_test.go` + `modes_ui_test.go` + `team_ui_test.go` + `models_ui_test.go` | 各删 1 个 `*Static*` 契约测试（共 5 个函数） | 同属"静态路径不得出现 X"的基线契约，可一次清完 | 低 |
| 6 | `internal/shell/verified_ui_test.go` | 删 `TestBaselineMarginsCompareCollapseControlAndRowFill`、`TestStaticHeaderIcons`；迁 `TestDarkSendControlUsesThemeFill`、`TestNarrowSessionTranscriptStaysInsideWindow` | 2 删 2 迁；`TestStaticHeaderIcons` 还引用 `sessModeIcon` 等静态符号，早删早稳 | 低-中 |
| 7 | `internal/shell/markdown_test.go` | 迁移 `m6MarkdownApp` + 7 个测试到 live fixture | **markdown 是产品资产**，但当前全部依赖已失效的 snapshot 静态渲染，优先级高于一般静态视觉 | 中 |
| 8 | `internal/shell/message_actions_test.go` | 迁移 3 个 snapshot/静态测试 | usage 时钟/速率、turn 导航是产品行为；需要"snapshot 注入 + 假时钟"的 live fixture | 中 |
| 9 | `internal/shell/toolcards_test.go` | 迁移 2 个测试 | 工具卡行为是产品资产；`m6ToolSnapshot` 可复用到 live fixture | 中 |
| 10 | `internal/shell/i18n_test.go` + `interaction_test.go` + `dark_contrast_test.go` 的静态部分 | 迁移低难度组：i18n 2 个、sidebar 导航 1 个、窄窗布局 3 个 | 断言本身都是产品行为，只差把 fixture 换成 `newLiveApp`，可批量做 | 低 |

**延后（按 Lead 指示保留）**：`internal/shell/motion_test.go`（8 个测试，难度高，见 §3-B）；`fixer_regression_test.go` 的 `TestCredentialFieldsAreMasked`（安全资产，等 live Settings fixture）。

**附：推荐的新共享 fixture（下一波开工前先定）**
1. `newLiveApp(t, provider)` 已有 → 作为唯一屏渲染入口；
2. `liveAppForScreen(t, screen)`：内置一个空/1 会话的 controller，供布局/键盘类测试；
3. `snapshotLiveApp(t, snap)`：给需要合成 snapshot（usage、工具卡、长时间 transcript）的测试用——这是解锁 markdown / message_actions / toolcards / motion 的关键；
4. 主题渲染 wrapper 保留 `ui.NewTester(func(c){ c.SetTheme(...); h.app.View(c) })` 模式（`scrim_test.go` 已有）。

---

## 7. 未删除/保留说明（产品行为资产）

以下类别**未做整文件删除**，只在必要时裁剪了 DSH 像素/几何断言；它们是后续功能对齐的基础，迁移时不要误删：

- 键盘可达/a11y：`a11y_*.go`、`a11y_fixes_test.go`、`focusedDuringTab`/`assertTabReachable` 等；
- 弹层/菜单/快捷键：`composer_menus_test.go`、`menus_test.go`、`composer_popup_escape_test.go`、`session_search` 相关；
- 审批/提问卡：`approval_ui_test.go`、`plan_ui_test.go` 的批准流程；
- 附件/工作区/会话：`attachment_acceptance_test.go`、`home_attachment_test.go`、`workspaces_test.go`、`workspace_actions_test.go`、`session_live_test.go`；
- i18n/markdown/工具卡/右栏实时功能：保留内容，只待 fixture 迁移；
- 动效令牌/纯函数：`motion_test.go` 的 `TestMotionTokens`/`TestMotionHelpers` 等 4 个纯逻辑测试不受静态屏影响。

> 本轮**没有修改任何生产 `.go` 文件**；删除/编辑均限定在 `internal/shell/*_test.go` 与本清单。生产代码中的 `internal/shell` 改动全部来自 Lead，`internal/design`/`internal/brand` 来自 `native-theme`，`internal/pixeltest`/`baseline`/`artifacts` 删除来自 `parity-cleanup`。

---

## 8. 复现命令（清理后）

```bash
cd /data/mygo
export PATH=/usr/local/go/bin:$PATH GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0

# 1) parity 测试文件已删 / 仍 import pixeltest 的 Go 文件（期望：无输出）
ls internal/shell/{screens,baseline_dark,capture_live,icon_parity,live_screens,sidebar_geometry,sidebar_divider}_test.go 2>&1
grep -rln 'internal/pixeltest' --include='*.go' internal/ cmd/

# 2) 静态 fixture 扫描（&App{...} 且不含 Ctrl；零值 App{} 也在内）
grep -rn '&App{' internal/shell/*_test.go

# 3) 测试规模与编译状态（重构进行中，预期 fix_shell_test.go 仍报 sess* 符号）
ls internal/shell/*_test.go | wc -l
grep -hE '^func Test' internal/shell/*_test.go | wc -l
go vet ./internal/shell
go test -run '^$' -count=1 ./internal/shell

# 4) 格式检查（本轮改动的 3 个文件）
gofmt -l internal/shell/dark_contrast_test.go internal/shell/fixer_regression_test.go internal/shell/fixer_regression2_test.go
```

> 快照注意：若 Lead 已完成静态屏拆除，`go vet` 的报错点可能变化；以本清单 §3/§4 的函数级判定为准，按 §6 顺序推进。
