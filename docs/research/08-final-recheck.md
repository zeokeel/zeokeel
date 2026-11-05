# 08 · 最终收口复核（HEAD e4e71e4）：M1–M3 闭环确认 + goal complete 建议

> 角色：独立验收（Team 成员 `parity-tests`）· 任务：`task-33` · 时间：2026-11-05 15:49–15:54（Asia/Shanghai）
> 对象：**HEAD `e4e71e4`**（`e4e71e4f1531c5c8d120b7fc8dc55a7721ff8dcf`，"close review round-4 findings: danger-on-tint AA, single settings close"，提交 15:49:34）
> 环境：`/data/mygo`，go1.27.3 linux/amd64，`GOMODCACHE=/data/mygo/.cache/gomod`，`GOCACHE=/data/mygo/.cache/go-build`，`GOSUMDB=off`，`CGO_ENABLED=0`
> 只读复核：唯一产物为本文；未修改代码/图片/其他文档，未 `git add/commit`。

---

## 0. 结论速览（最终裁决）

| 项 | 要求 | 复核结果 | 裁决 |
| --- | --- | --- | --- |
| 仓库状态 | HEAD e4e71e4、工作树干净 | 15:50:03 HEAD=e4e71e4、`git status` 0 行；全部测试/CI 后 15:52:51 仍 **0 行** | ✅ |
| **M1** 工具卡错误正文 danger-on-tint AA | DangerText 加到 brand/design；toolcards 标题/**正文**/Copy 全改；brand_test 用真实 BgRaised 合成 8%/12% tint；浅色 ≥4.5:1 | 代码、测试、独立复算全部通过（浅色 8% tint **5.73:1**、12% tint **5.37:1**；深色 8.04/7.57） | ✅ 闭环 |
| **M2** 设置窗口只保留一个关闭入口 | 窗口模式隐藏文字「关闭」，只留面板 ×；overlay 回退保留 | 条件分支 `if !a.hasSettingsWindowHost()` 已落地，窗口模式只剩 ×；overlay 路径不变；8 个设置窗口测试全过。**P3 证据完善建议**（预览 fixture 未设置窗口宿主、无"唯一关闭"断言，见 §3） | ✅ 闭环（P3 非阻塞） |
| **M3** 报告/预览入版本库 | review-round4、07 报告已提交；22 预览 tracked；README/STATUS/known-limitations 更新；树干净 | `git ls-files` 确认两份报告 + 22 张预览 tracked；commit 含 README/STATUS/known-limitations 更新；树干净 | ✅ 闭环 |
| 门禁复跑 | gofmt/vet/build + 定向测试 + `ci.sh --fast`；跑后树干净 | gofmt 空；vet/build PASS（3.1s）；定向 33 shell + brand PASS；`ci.sh --fast` **CI PASS 72.6s**（18 包 ok / 0 FAIL）；树干净 | ✅ |
| known-limitations | 无未披露的 PARTIAL | 已披露项与代码/测试一致；3 处 P3 措辞/数字偏差 + 2 处旧采样色（见 §5），均**非功能缺口** | ✅ |

> **最终建议：可以标记 goal complete。** objective ① PASS / ② PASS / ③ PASS，FAIL = 0，**阻塞项 = 0**。07 报告的 M1/M2/M3 全部闭环；剩余仅为 P3 文档/证据完善（可留 v1.1，不阻塞收口）。

---

## 1. 仓库状态（PASS）

```text
[15:50:03] HEAD = e4e71e4f1531c5c8d120b7fc8dc55a7721ff8dcf
           git status --short → 0 行
           git log -3: e4e71e4 15:49:34 close review round-4 findings: danger-on-tint AA, single settings close
                       68bc842 15:34:51 design review P0 fixes: WCAG AA tertiary text, dark wordmark, destructive confirms
                       3eb3c2e 15:23:41 native visual round 3: tool cards, settings window, background notifications, a11y, cleanup
```

`e4e71e4` 提交内容（`git show --stat`）：16 文件 +493/−8，含 `internal/brand/palette.go`（DangerText）、`internal/brand/brand_test.go`（tint 断言）、`internal/design/tokens.go`（映射）、`internal/shell/toolcards.go`（正文改色）、`internal/shell/settings_ui.go`（单一关闭）、6 张预览重生成、`review-round4.md`、`docs/research/07-final-acceptance.md`、README/STATUS/known-limitations 更新。

---

## 2. M1 复核：工具卡错误正文 danger-on-tint AA（闭环）

### 2.1 代码证据

```text
internal/shell/toolcards.go:
  339  Background(t.Danger.Alpha(0.08).Over(t.BgRaised))      // 面板底：8% danger over BgRaised
  340  Border(1, t.Danger.Alpha(0.45).Over(t.BgRaised))       // 边框（非文本，Danger 保留）
  345  TextColor(t.DangerText)  // 「错误」标题
  353  Background(t.Danger.Alpha(0.12).Over(t.BgRaised))      // Copy hover 底
  357  TextColor(t.DangerText)  // 「复制错误」
  365  TextColor(t.DangerText)  // ← 错误正文（07 报告必须补做项，已改）
internal/brand/palette.go:
  124-126  DangerText 字段（注释：WCAG-AA foreground on danger tint；Danger 仅作 fill/border/icon）
  165      Light: DangerText = #B91C1C  // 5.7:1 on the 8% danger tint
  198      Dark:  DangerText = #FCA5A5  // AA on the dark danger tint
internal/design/tokens.go:
  90-92 / 132  DangerText 暴露并映射自 brand.Palette.DangerText
internal/brand/brand_test.go:
  160-161  danger 文本断言改为在**真实 raised surface** 上合成：
           p.Danger.Alpha(0.08).Over(p.Surface) / p.Danger.Alpha(0.12).Over(p.Surface)
```

### 2.2 独立复算（不依赖项目测试）

| 主题 | 合成底色（Danger α over BgRaised/Surface） | DangerText | 对比度 | AA 4.5:1 |
| --- | --- | --- | ---: | --- |
| 浅色 8% tint | `#FCEEEE`（#DC2626 8% over #FFFFFF） | `#B91C1C` | **5.73:1** | ✅ |
| 浅色 12% tint | `#FBE5E5` | `#B91C1C` | **5.37:1** | ✅ |
| 深色 8% tint | `#2D222C`（#F87171 8% over #1B1B26） | `#FCA5A5` | **8.04:1** | ✅ |
| 深色 12% tint | `#36252F` | `#FCA5A5` | **7.57:1** | ✅ |

复算口径：WCAG 2.x 相对亮度公式（与 `internal/brand` 的 `contrast()` 一致）；与 Lead 声明的"浅色 5.7:1"一致。

### 2.3 测试与预览证据

- `go test -count=1 -v ./internal/brand -run TestPaletteContrast` → **PASS**（15:50:28）；该测试在 8%/12% 两种真实 tint 上对 DangerText 断言 ≥4.5:1。
- 定向 shell 工具卡测试全 PASS（15:50:32）：`TestToolCardErrorUsesDangerPanel`（错误面板文案/Copy 行为）、`TestToolCardCommandOutputIsCodeBlockWithCopy`、`TestToolCardMarkdownResultRendersMarkdown`、`TestToolCardImageFallsBackToDescription`、`TestToolCardLongResultLineCap`、`TestRenderToolCardNativePreview`。
- 预览 `docs/design/preview/toolcard-native-light.png` 已随 e4e71e4 重生成（bytes 113434→113480）；`read_image` 目检：错误面板「错误 / fatal: not a git repository / 复制错误」文字较修复前明显加深（#DC2626 → #B91C1C），面板其余视觉不变。
- 说明（P3）：颜色回归由 brand 令牌级 `TestPaletteContrast` 守护，渲染层 `TestToolCardErrorUsesDangerPanel` 只断言结构与 Copy 行为、未取样颜色——令牌级守护已足够（渲染直接使用 `t.DangerText`），非阻塞。

---

## 3. M2 复核：设置独立窗口只保留一个关闭入口（闭环，P3 证据建议）

### 3.1 代码证据

```text
internal/shell/settings_ui.go:
  420  if !a.hasSettingsWindowHost() {                 // 窗口宿主存在时隐藏文字「关闭」
         a.settingsOutlineButtonLive(c, t, a.T("menu.close"), a.closeSettingsRequested)
       }
  423  a.settingsCloseLive(c, t)                       // 面板 ×（始终保留）
  296-300 hasSettingsWindowHost() = settingsWindowOpen != nil || settingsWindowClose != nil
internal/app/app.go:150-151  真实桌面宿主注册 opener/closer → hasSettingsWindowHost()==true
```

行为：独立设置窗口 → 只渲染「打开配置文件」+ 面板 ×（OS 标题栏另有关闭，属系统行为）；应用内 overlay 回退（无宿主/窗口创建失败）→ 保留原「关闭」+ ×。与任务声明一致。

### 3.2 测试证据

- 8 个设置窗口测试全部 PASS（15:50:32 / ci 全绿）：`TestSettingsWindowSingletonAndFocus`、`CloseHidesAndReopens`、`ClosesOnlyWhenQuitting`、`Unavailable`、`OptionsAreBrandSized`、`TestAppSettingsActionOpensWindow`、`FallsBackToOverlay`、`TestRefreshWindowsInvalidatesBoth`。
- `TestSettingsViewRendersStandalone` 设置 `SetSettingsWindowCloser(...)` → 走窗口宿主分支，断言「Close settings」（× 的可访问名）存在，**PASS**。

### 3.3 P3 证据完善建议（不阻塞）

- `TestRenderSettingsWindowNativePreview` 只 `ui.Render(h.app.SettingsView, ...)`，**未注册窗口宿主** `SetSettingsWindowOpener/Closer` → `hasSettingsWindowHost()==false`，该预览实际渲染的是 **overlay 回退头部**（含「关闭」文字 + ×），因此 `settings-window-native-{light,dark}.png` 未体现 M2 的单一关闭（提交中这 2 张也字节未变，仅 6 张其他预览变化）。
- 建议（v1.1）：预览 fixture 注册 host 后重生成 2 张；并加一条断言"窗口模式下可见关闭控件只有 1 个"。属验证证据完善，**不影响产品行为**（真实 app 已注册 host，代码分支确定）。

---

## 4. M3 复核：报告/预览入版本库（闭环）

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| review-round4 / 07 报告 tracked | `git ls-files docs/design/review-round4.md docs/research/07-final-acceptance.md` | 两份均 tracked ✅ |
| 22 张预览 tracked | `git ls-files 'docs/design/preview/*.png' \| wc -l` | **22** ✅（11 组浅/深；settings-window 为 900×720，其余 1600×1000） |
| 预览重生成 | `git show --stat e4e71e4` | session/toolcard/trajectory 6 张 bytes 变化；settings-window 2 张未变（原因见 §3.3） |
| 文档更新 | `git show e4e71e4 -- README.md docs/STATUS.md docs/design/known-limitations.md` | README 下一阶段改为发布前真机验证/视觉黄金/TextScale/Trajectory；STATUS 增"P1 收口（第五轮）"；known-limitations §3.5 增"已修复评审判定项"记录 |
| 工作树 | 全部测试/CI 后 `git status --short` | **0 行** ✅ |

---

## 5. 门禁复跑（PASS）

| 时间 | 步骤 | 结果 |
| --- | --- | --- |
| 15:50:27 | `gofmt -l ./cmd ./internal` | 空输出（exit 0） |
| 15:50:27 | `go vet ./...` | PASS（exit 0） |
| 15:50:27 | `go build ./...` | PASS（exit 0；vet+build 合计 3.1s） |
| 15:50:32 | `go test -run 'TestTool\|TestDanger\|TestSettings\|TestRender.*NativePreview\|TestPaletteContrast' ./internal/shell ./internal/brand` | **PASS**：shell 定向 33+ 个测试（含 10 个 NativePreview、7 个 ToolCard、全部 Settings/窗口测试）9.98s；brand `TestPaletteContrast` PASS |
| 15:50:56–15:52:09 | `sh scripts/ci.sh --fast` | **CI PASS**，exit 0，wall **72.6s**；`go test ./...` **18 包 ok / 0 FAIL**（shell 52.0s、session 47.7s、tools 7.5s、app 1.8s；另 `cmd/zeokeel` no test files）；零 cgo 四目标 PASS；`TestProductionToolsInvocable` PASS |
| 15:52:51 | 跑后 `git status --short` | **0 行（干净）** ✅ |
| 15:52:51 | 规模 | 全仓 913 个 Test 函数 / shell 307 个（53 测试文件）/ 7 个 benchmark（与 e4e71e4 前一致，本次仅增强既有断言，无删减） |

原始日志：`/tmp/acceptance3/{10-m1-evidence,11-m2-m3-evidence,20-m1-gates,21-targeted-tests,22-ci-fast}.log`。

---

## 6. known-limitations 对照（无未披露 PARTIAL）

`docs/design/known-limitations.md` 当前 6 节 + §3.5 修复记录；以下为 07 报告已提出、仍未更新的 P3 项（**均已在 07/本报告披露，非未披露缺口**）：

1. "固定 type scale（约 115 处）"：07 报告实测（`.FontSize(<数字>` 字面量）为 **138 处**；措辞未更新。
2. "文本选择：消息正文与工具卡内容已可选中"：assistant 消息与工具卡（markdown）✅；**用户消息气泡仍为普通 `ui.Text` 不可选**；措辞宜改"assistant 消息正文"。
3. `docs/design/preview/README.md` 仍写"全部截图 … `ui.Render(...,1600,1000,1)`"，而 settings-window 2 张为 900×720。
4. `internal/shell/plugins.go:34-35` 仍有 2 处 DSH 采样标签色 `#6E7278/#C2C6CA`（P3 视觉残留，`a11y_fixes_test.go:316` 仍 pin；建议 v1.1 换品牌令牌）。
5. §3.3 的设置窗口预览 fixture 未体现窗口模式（P3 证据完善）。

以上均**不影响 objective 达成**；建议留 v1.1，不阻塞收口。

---

## 7. 最终裁决

| # | objective | 裁决 | 证据 |
| --- | --- | --- | --- |
| ① | 拆除 DSH 像素对齐门禁及仅为其服务的代码，保持绿色 | **PASS** | 前次 07 已闭环；本次 `ci.sh --fast` 全绿、退役路径仍不存在、无 parity 活引用（快照复核未变） |
| ② | 用 mygo Theme/原生控件重做视觉与交互 | **PASS** | 07 唯一必须补做项 M1 已闭环（正文 DangerText，浅色 5.73:1、深色 8.04:1，令牌级测试守护）；M2 单一关闭入口已落地；P0-1/2/3 持续有测试；22 预览/10 预览测试；设置窗口/后台通知/工具卡/偏好/DPI 均有测试；残余均为 known-limitations/下一轮工程项 |
| ③ | 持续功能对齐与测试绿色 | **PASS** | `go test ./...` 18 包全绿（0 FAIL）、shell 306/306、0 skip；功能域测试资产完整；known-limitations 如实披露产品边界与发布前真机验证项 |

**FAIL = 0；阻塞项 = 0；必须补做项 = 0。**

### 是否可标记 goal complete

> **可以标记 goal complete（建议立即标记）。**
> 07 报告的 M1/M2/M3 已全部闭环并有可复核证据；最终 HEAD `e4e71e4` 全门禁绿、工作树干净。
> 唯一遗留为 §6 的 P3 文档/证据完善（5 项）与 known-limitations 中的发布前真机验证清单——按既定口径属"已知限制/发布门禁"，**不阻塞开发目标收口**，建议写入 v1.1 计划随发布流程完成。

---

## 附录：本次复核时间线

| 时间 | 动作 | 结果 |
| --- | --- | --- |
| 15:50:03 | preflight | HEAD e4e71e4，status 0 行 |
| 15:50:03–15:50:12 | M1 证据（代码/令牌/测试断言） | 三处文本均 DangerText；brand_test 用真实 Surface tint |
| 15:50:12–15:50:20 | M2/M3 证据 | 条件分支存在；8 设置窗口测试；两份报告 + 22 预览 tracked |
| 15:50:27 | gofmt / vet / build | 全 PASS |
| 15:50:28 | `TestPaletteContrast` | PASS（独立复算 5.73/5.37/8.04/7.57） |
| 15:50:32 | 定向测试（shell+brand） | shell 定向全 PASS（9.98s）；brand PASS |
| 15:50:56–15:52:09 | `ci.sh --fast` | CI PASS，18 包 ok / 0 FAIL，72.6s |
| 15:52:51 | 最终 `git status` | **0 行，干净** |
