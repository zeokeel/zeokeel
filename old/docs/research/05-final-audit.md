# 05 · 最终代码审计：结构 / 删除量 / 品牌原生化覆盖 / 残余耦合 vs 调研基线

> 角色：code-metrics（task-26）· 审计对象：**HEAD `b3ff65f`**（audit 开始时工作树干净；审计期间有并发队友在改下一阶段代码，见附录）
> 基线：`docs/research/01-code-audit.md`（提交 `b974d92` 快照：全仓生产 49,363 / 测试 44,725；shell 16,288 / 14,514；UI 17,261；test 838；baseline 106MB；pixeltest 140）
> 方法：用 `git archive` 把 `b974d92`（像素对齐时代）与 `HEAD b3ff65f` 提取到 `/tmp` 独立度量，避免受并发工作树影响；HEAD 副本上跑 gofmt/vet/build/test 验证"全绿"且**不写工作树**。
> 本文只读，唯一写入即本文件；未 git add/commit。

---

## 0. 结论先行

### 0.1 目标达成度（对照三条 objective）

| # | 目标 | 判定 | 关键证据 | 残余问题 |
| --- | --- | --- | --- | --- |
| 1 | **拆像素门禁与匹配代码** | ✅ **达成** | `baseline/`、`artifacts/`、`docs/baseline/`、`scripts/cdp.mjs`、`scripts/capture/`、`internal/pixeltest/` 全部删除并归档（tar 实测 **862 项**）；`ci.sh`/`gates.sh` 像素步骤删除；parity 测试删 7 文件/17 Test + 13 个静态契约测试；`App.View` 只剩 live 路径（无 Ctrl 画空帧）；代码内 **0 处活引用**（仅 2 条注释提及） | 54 处 pixel/baseline/static 提及（含过期描述）、2 处 `ui.Hex` 采样色、少量 DSH 派生布局常量、PLAN §10.2 仍列 2 个不存在的门禁脚本 |
| 2 | **自有品牌视觉与原生控件** | ✅ **基本达成** | `internal/brand` 306 行品牌主题（35 个品牌色，源自 logo SVG）；8 个顶层屏/区 + 面板全部品牌化；预览 **8 测试 / 18 张 PNG（9 区×浅深）**；样式 setter **2,833→1,974（−30%）**、`design.*` 引用 974→638（−34%）；markdown 自研 628→106 行（迁 mygo `ui/markdown`） | 预览图是人工评审产物、**无 golden diff 断言**；仍有 1 文件 2 处残留采样色；`ui.List/Tree/Table` 仍 0 使用（列表未虚拟化）；部分自绘 popover 为 a11y 有意保留 |
| 3 | **桌面交互本地化 + 功能对齐 + 全绿** | ⚠️ **部分达成** | 系统菜单栏（menu.go 143）、窗口状态持久化（window_state.go 116）、系统通知（notify.go 351 + 17 测试）、TextScale/HighContrast/ReduceMotion/2x DPI（preferences.go 148 + 7 测试）；HEAD 副本 **gofmt/vet/build/test 全绿**、check-nocgo 四目标 PASS（47.5s） | 真机 IME/a11y/多平台未验证；**后台会话通知在 HEAD 缺失**（ADR 0004 §4.1，工作树中修）；通知动作/替换未做；独立设置窗口未提交；滚动条仅 hover overlay；功能矩阵仍有 打包/自动更新 ❌、auto-review ❌、Goal driver ❌ |

### 0.2 关键数字变化（HEAD vs 01 基线）

| 指标 | 01 基线（b974d92） | HEAD b3ff65f | 变化 |
| --- | ---: | ---: | ---: |
| 全仓 Go 生产 | 49,363 | **49,589** | +226（+0.5%） |
| 全仓 Go 测试 | 44,725 | **45,758** | +1,033（+2.3%） |
| `internal/shell` 生产 | 16,288 | **15,761** | **−527（−3.2%）** |
| `internal/shell` 测试 | 14,514 | **14,513** | −1 |
| UI 层生产（shell+design+brand+icons） | 17,121（不含 pixeltest） | **16,838** | **−283（−1.7%）**，其中 brand +306 为新能力 |
| `internal/pixeltest` | 140 / 40 | **0 / 0** | −180 |
| 全仓 Test 函数 | 838 | **868** | +30（先 −26 拆除、后 +56 新增） |
| Benchmark 函数 | 0 | **0**（工作树新增 7 个未提交） | — |
| baseline 资产 | 605 文件 / 106MB | **0** | 归档 862 项 |
| 样式 setter（shell） | 2,833 | **1,974** | **−30.3%** |

### 0.3 一句话

**三条目标中，"拆像素对齐"已彻底完成（代码、门禁、测试、资产、CI 全链路清零，且用归档+回滚 tag 兜底）；"品牌视觉与原生控件"已完成主体并获得可复现预览，但视觉验收靠人工评审而非自动断言；"桌面交互 + 功能对齐"完成了 P0 且全绿，真机平台验证与 P1 能力（后台通知/独立设置窗口/滚动条/IME/a11y）仍未闭环——这是当前与"完成"之间的全部差距。**

---

## 1. 代码规模复测与基线对照

### 1.1 三时点数据（命令见 §6）

| 提交 | 时点 | 生产行 | 测试行 | 合计 |
| --- | --- | ---: | ---: | ---: |
| `b974d92` | 像素对齐时代（01 基线） | 49,363 | 44,725 | 94,088 |
| `8bf0053` | 阶段 1：退役 parity（静态屏/基线/像素工具拆除） | 48,522 | 43,691 | 92,213 |
| `b3ff65f` | HEAD：品牌原生视觉 + 桌面 P0 完成 | **49,589** | **45,758** | **95,347** |

- 退役提交 `8bf0053` 本身：**110 文件，+2,211 / −6,050 行**；`8bf0053→HEAD`：**80 文件，+6,920 / −3,202 行**；`b974d92→HEAD` 净变化 **−121 行**（172 文件）。
- 结论：parity 拆除净删 ~6k 行，但随后品牌/桌面/原生视觉净增 ~7k 行，**全仓规模基本持平（+0.5% 生产）**——这与 01 报告"省的是对齐脚手架、不是产品功能"的预测一致。

### 1.2 各包变化与归因

| 包 | 生产行 | 测试行 | 变化归属 |
| --- | ---: | ---: | --- |
| `internal/shell` | 16,288 → **15,761** | 14,514 → **14,513** | −527：拆除静态五屏 −945（阶段 1）→ 品牌原生化 +418（后续轮次）；测试先 −1,281 再 +1,280 |
| `internal/design` | 752 → **690** | 160 → **241** | −62：DSH 令牌层瘦身为品牌派生层；测试 +81（品牌映射断言） |
| `internal/brand` | 0 → **306** | 0 → **206** | **新增**：品牌色板 201 + 主题/字体/指标 105 |
| `internal/pixeltest` | 140 → **0** | 40 → **0** | **删除**（parity 工具） |
| `internal/app` | 1,691 → **2,340** | 3,887 → **4,674** | **+1,436**：menu.go 143、window_state.go 116、notify.go 351、app.go/services 增量 + 测试 menu 8/window_state 5/notify 17 个 |
| 其余 13 包 | 不变 | 不变 | 无改动（runtime/session/store/tools/provider/mcp/…） |
| `cmd/zeokeel` | 20 → 20 | 0 → 0 | — |

### 1.3 UI 层口径（三种算法，避免歧义）

| 口径 | 01 基线 | HEAD | 变化 |
| --- | ---: | ---: | ---: |
| 01 报告原口径（shell+design+icons+pixeltest） | 17,261 | — | 口径已不适用（pixeltest 删除） |
| 同口径对比（shell+design+icons） | 17,121 | **16,532** | **−589（−3.4%）** |
| 新 UI 层（shell+design+brand+icons） | 17,121（brand=0） | **16,838** | **−283（−1.7%）** |

> 生产 UI 代码没有明显缩小，因为**品牌层是新增能力**；真正消失的是 parity 资产（106MB/605 文件/862 归档项）、6 次/CI 像素调用与 ~1,300 行测试脚手架。

### 1.4 测试与基准

- `go list ./...` = **18 个包**（17 个有测试 + `cmd` 无测试）；全部包均含在 `go test ./...` 中。
- Test 函数 **868**（01 基线 838）：app 83（+30）、brand 5（新增）、design 10（+3）、shell 282（−5）、其余不变；benchmark **0**、fuzz 0。
- 测试函数变化 = parity 拆除 −26（838→812）→ 原生视觉/桌面能力 +56（812→868）。

### 1.5 删除清单与归档核验

| 资产 | 01 基线 | HEAD 状态 | 核验 |
| --- | --- | --- | --- |
| `baseline/` | 605 文件 / 106MB | 目录不存在 | 归档 tar 实测总条目 **862**（与 ADR 0001 §4 声称一致） |
| `artifacts/` | 185 文件 / 8.9MB | 不存在 | 同上 |
| `docs/baseline/` | 19 文件 | 不存在 | 同上 |
| `scripts/cdp.mjs` + `scripts/capture/` | 509 行 + 25 json | 不存在 | `git log` 有删除提交 |
| `internal/pixeltest/` | 140 + 40 行 | 不存在 | `go list ./...` 无此包 |
| 归档包 / 回滚 tag | — | `/data/zeokeel-backups/parity-assets-20261105.tar.gz`（48.36MB）、tag `pre-native-ui` | `tar -tzf … | wc -l` = 862；`git tag -l` 有 tag |

---

## 2. 品牌原生化覆盖与残余耦合

### 2.1 每屏/区：实现文件 + 预览图（HEAD 已提交 18 张）

| 屏/区 | 实现文件（HEAD 行数） | 预览（浅/深） |
| --- | --- | --- |
| Home | `home.go` 397 + `home_live_menus.go` 624 + `composer_native.go` 116 | `home-native-{light,dark}.png` |
| Session | `session.go` 43 + `session_live.go` 620 + `toolcards.go` 489 | `session-native-{light,dark}.png` |
| Sidebar | `app.go` sidebar + `workspaces.go` 1,361 + `chrome.go` 296 | `sidebar-native-{light,dark}.png` |
| Settings | `settings.go` 204 + `settings_ui.go` 875 | `settings-native-{light,dark}.png` |
| Plugins | `plugins.go` 380 + `plugins_ui.go` 270 | `plugins-native-{light,dark}.png` |
| Automation | `automation.go` 46 + `automation_ui.go` 460 | `automation-native-{light,dark}.png` |
| Trajectory | `trajectory.go` 516 | `trajectory-native-{light,dark}.png` |
| 右侧栏 Start / Files | `right_sidebar.go` 1,075 + `start_tab.go` 531 | `right-sidebar-native-*.png`、`right-sidebar-files-*.png`（一个测试出 4 张） |
| 面板（todo/team/plan/memory） | `todo_ui.go` 251、`team_ui.go` 174、`plan_ui.go` 215、`memory_ui.go` 212 | **无独立预览**（行为测试覆盖；含在 Session 预览内） |

- 预览总数核验：`git ls-tree HEAD docs/design/preview/ --name-only | grep -c '\.png$'` = **18**；`grep -c 'func TestRender.*NativePreview'` = **8** 个测试函数（右侧栏一个函数产 4 张）。
- 预览生成方式：`ui.Render(..., 1600, 1000, 1)`、live fixture、中文默认；**不是 golden 断言**——测试只断言 PNG 非空（`writePreview`），视觉质量靠人工评审。这是退役像素门禁后的**有意取舍**，但也意味着品牌视觉回归没有自动化防线。

### 2.2 预览 opt-in 卫生（已实测验证）

- `previewDir(t)`（`internal/shell/livetest_helpers_test.go:40-49`；:38 为开关说明注释）：仅 `ZEOKEEL_UPDATE_PREVIEWS=1` 时写 `docs/design/preview`，否则 `t.TempDir()`；**CI 不设该变量**。
- 实测：在 `git archive HEAD` 的干净副本中跑 `go test -count=1 ./...` → 全绿，且**没有任何文件被修改**（`find -newermt` 为空）；工作树跑完整测试后 `docs/design/preview/*.png` mtime 保持 14:37 未变。
- 结论：普通测试/CI**不会污染工作树**；只有显式开关才刷新预览（符合"预览测试 opt-in"的卫生要求）。

### 2.3 原生化采用度（shell 生产代码）

| 指标 | 01 基线 | HEAD | 变化 |
| --- | ---: | ---: | ---: |
| `ui.*` 调用总数 | 2,070 | **1,565** | −24.4% |
| 样式 setter（25 方法集） | 2,833 | **1,974** | **−30.3%** |
| `design.*` 令牌引用 | 974 | **638** | −34.5% |
| 布局图元占比（Text/Row/Column/Center/Box/Icon/…，除 Context） | ~70% | **64.3%** | 手搓布局仍占主体 |
| 原生控件占比（ButtonBase/Button/PrimaryButton/Sidebar/Switch/TextArea/Modal/…） | ~6% | **16.1%** | +10pp |

原生控件已实际接管：`ButtonBase` 48、`Button` 40、`PrimaryButton` 8、`Sidebar` 1 + `SidebarItem` 4、`Switch` 4、`SegmentedBase` 1、`SearchField` 2、`TextInput` 12、`TextArea` 2、`Popover` 15、`Modal` 15、`TabsBase` 2、`SplitPercent` 1、`RoleSplitter` 1、`Badge` 1、`Scroll` 12、`Menu` 11。
**仍未使用**：`ui.List`、`ui.Tree`、`ui.Table`、`ui.Collapsible`、`ui.Tooltip`、`ui.Toast`（0 次）——列表虚拟化、树语义、tooltip/toast 仍是自研或缺失（对应 §4.4 风险）。

### 2.4 残余硬编码：DSH 采样色 / 魔法像素

| 类型 | 数量 | 位置 | 判定 |
| --- | ---: | --- | --- |
| 采样色 `ui.Hex`（shell） | **2** | `plugins.go:34-35` `#6E7278`/`#C2C6CA`（More 标签，深色值被对比度测试钉住） | ⚠️ **真实残留**，应迁到品牌 `LabelSecondary` 或 design token |
| `ui.Hex`（design） | 1 | `tokens.go` `White = ui.Hex(brand.HexWhite)` | ✅ 合理（品牌来源） |
| `ui.Hex`（brand） | 32 调用 / 57 字面量 / 35 唯一色 | `internal/brand/palette.go` | ✅ 合理（品牌自有，注释溯源 `logo/logo.svg` 行号） |
| 魔法几何（活代码） | **1** | `composer_menus.go:140` `menuFrame(c, t, 868, …)`（审批卡宽度=DSH 旧 composer 宽） | ⚠️ 残留，建议改品牌列宽 |
| 魔法几何（布局常量） | **10** | `layout.go:14-46`：280/400/220/0.45/320/48/212/430/272/280 | 其中 3 个品牌派生（280 侧栏、0.45 面板占比、320 home stack）；其余 7 个为 DSH 测量派生（注释已标明来源），属**低风险历史口径残留** |
| 877 / 909 / 800×800 / 960 像素魔法值 | 0 处活代码 | 仅剩注释（`layout.go:151,267,289-291`、`automation.go:10`）与 1 个死函数 `pageColumnWidth` | ✅ 已从活代码清除 |

### 2.5 残余 parity 引用（活引用 vs 历史说明）

| 位置 | 数量 | 性质 |
| --- | ---: | --- |
| `internal/` + `cmd/` 代码 | **2 条注释** | `responsive_panels_test.go:234`（"retired docs/baseline tree"）、`fixer_regression_test.go:335`（"pixeltest.Compare(…)" 历史说明）；**0 处 API/路径引用** |
| `scripts/` | 1 条注释 | `ci.sh:38` 明确写"pixel-baseline gate retired 2026-11-05"；无像素步骤 |
| `scripts/evolve/gates.sh` | 1 条注释 | 第 13 行退役说明；步骤只剩 fmt/vet/build/test(+nocgo) |
| `docs/PLAN.md` | 10 处 baseline 提及 | **全部显式标注"历史/已退役/已归档"**，含 §1.2 像素轴删除线标注 |
| `docs/EVOLUTION.md` | baseline 5 / pixeltest 5 / TestScreens 7 | ⚠️ 文件头有退役声明，但正文 §2.1/§5 仍把 `pixeltest`/`baseline/` 当可复用资产（"+542 个测试"旧数）——**历史文档未行内标注** |
| `docs/UI-PARITY-AUDIT.md`、`COMPONENT-PARITY.md` | 有 baseline 提及 | ✅ 文件头已标注"历史文档，已退役 2026-11-05，不再维护" |
| `baseline/`、`artifacts/`、`internal/pixeltest/` 目录 / 包 | **0** | 已删除；`.gitignore` 保留 ignore 规则防回归 |

### 2.6 死代码与注释债（HEAD）

- **grep 级未引用生产函数**：**39 个 / ≈602 行**（探针：生产函数定义 vs 全仓非定义行标识符；含接口实现/测试反射调用等假阳性）。
- **人工确认 shell 内遗留死代码 ≈151 行**：`layout.go pageColumnWidth`（15）、`menus.go overlays/workspaceSection`（49）、`composer_menus.go composerPopupVisible/approvalCards`（35）、`settings_ui.go settingsSelectorLive`（7）、`chrome.go chromeHeaderButton`（8）、`composer_style.go switchTrack`（6）、`home_live_menus.go liveUsageText`（31）；design 包 `Page/BrandRow/SectionLabel/Pad/PadX/PadY/Stroke`（≈37 行）同属可清候选。
- **注释债**：shell 生产代码中 `pixel/baseline/static` 提及 **54 处（19 个文件）**，其中明确过期的"静态路径/pixel baseline"描述集中在 `app.go`（9 处，如 :38/:43/:99-100/:162/:205/:244/:264/:349）、`i18n.go`（3 处，仍写"static demo shell 渲染英文 pixel baseline"）；`DSH` 提及 **107 处（17 个文件）**，多数是行为对齐说明（合理），但部分是过期约束描述。
- 注：**审计期间工作树正在清理其中一部分**（`menus.go −133`、`composer_style.go −13`、`layout.go −62`，未提交），说明该债务已被识别并在处理。

---

## 3. 测试与门禁现状

### 3.1 `go test ./...` 实测（HEAD 干净副本）

| 项 | 结果 |
| --- | --- |
| 包数 | `go list ./...` = **18**（17 个有测试；`cmd/zeokeel` 无测试文件） |
| 结果 | **PASS（exit 0）**，无 FAIL/skip 异常 |
| 总耗时 | 工作树（暖缓存）**40.3s**；HEAD 干净副本 **56.6s**（与 check-nocgo 并发争抢 CPU） |
| 最慢包 | `internal/shell` **38.3–39.2s**（与 01 基线 39–41s 持平）；`internal/session` **31.0–36.1s**；`internal/tools` 7.3s；其余 ≤1.3s |
| gofmt / vet / build | **全部干净/通过**（HEAD 副本：`gofmt -l` 空、`go vet ./...` 通过、`go build ./...` 通过） |
| Test / Benchmark / Fuzz | 868 / **0** / 0（工作树新增 7 个 render benchmark，未提交） |

### 3.2 `scripts/check-nocgo.sh`（零 cgo + 四目标交叉编译）

- 实测 **PASS (zero cgo)**，耗时 **47.5s**；四目标全部 ok：`linux/amd64`、`linux/arm64`、`darwin/arm64`、`windows/amd64`；交叉编译产物写入 `mktemp -d`，不污染仓库（脚本 30 行，含清理 trap）。

### 3.3 CI / 门禁步骤（对照 PLAN §10.2）

`scripts/ci.sh` 实际步骤：gofmt → `go vet ./...` → `go build ./...` → `go test -count=1 ./...` → check-nocgo → `TestProductionToolsInvocable` → 覆盖率摘要 → race（阻断级）→ **无像素步骤**（第 38 行有退役注释）。
`scripts/evolve/gates.sh`：fmt / vet / build / test（3 次重跑判 flake）/ nocgo（--full）——**pixel-light/pixel-dark 已移除**。
⚠️ `docs/PLAN.md` §10.2 仍列出 `scripts/lint-style-literals.sh` 与 `scripts/cross-build.sh`——**这两个脚本不存在**（`scripts/` 实际只有 ci.sh、check-nocgo.sh、e2e-model.sh、e2e-tools.sh、evolve/gates.sh、render-wordmark.sh），需修正文档。

### 3.4 预览测试 opt-in 卫生

见 §2.2：普通 `go test ./...` 与 CI 只写 `t.TempDir()`；干净副本验证 **0 文件被测试修改**；预览刷新需显式 `ZEOKEEL_UPDATE_PREVIEWS=1`。

### 3.5 门禁覆盖缺位

- **视觉回归无自动断言**：品牌视觉只靠人工看 18 张预览（非 golden diff）——这是退役像素门禁后的合理取舍，但需在质量口径中承认。
- **基准缺失（HEAD）**：0 个 benchmark，PLAN §10.3 性能预算（滚动/首帧/token 上屏）不可回归（工作树已加 `render_bench_test.go` 7 个 benchmark，未提交）。
- **无 fuzz/property 测试**（0 个 Fuzz 函数）；QUALITY.md §测试金字塔把属性/模糊列为"待补"。

---

## 4. 目标达成度判定（逐项）

### 4.1 目标 1：拆像素门禁与匹配代码 —— ✅ **达成**

证据链完整且可复核：
1. **资产**：baseline 605/106MB、artifacts 185、docs/baseline 19 全部删除；归档 tar 实测 862 项、回滚 tag `pre-native-ui` 存在（ADR 0001）。
2. **代码**：`internal/pixeltest`（140 生产 +40 测试）删除；`scripts/cdp.mjs` 509 行 + capture dump 删除；`TestScreensBaseline`/`TestScreensDarkNarrow` 等 7 文件/17 Test + 13 静态契约测试删除；`App.View` 无 Ctrl 时直接画空帧（`internal/shell/app.go:244-249`），静态五屏路径删除。
3. **门禁**：`ci.sh` 6 次像素调用移除；`gates.sh` 的 pixel-light/pixel-dark 步骤与 masked/raw 指标移除。
4. **残余**：代码内 0 处活引用（仅 2 条注释与 1 条 CI 历史注释）；54 处过期注释、2 处采样色、1 个 868 魔法宽、10 个布局常量属低风险残留（§2.4/§2.6）。

### 4.2 目标 2：自有品牌视觉与原生控件 —— ✅ **基本达成**（主体完成，验收自动化缺位）

证据：
- `internal/brand`：306 行、35 个品牌色（全部来自 `logo/` SVG 渐变，含 18 行 provenance 注释）、Radius 10 / Spacing 4 / FontSize 14 / 系统字体栈 / 品牌 dark-light 双主题；`design.Tokens` 改为 `fromPalette` 派生（design 生产 752→690）。
- 覆盖：Home/Session/Sidebar/Settings/Plugins/Automation/Trajectory/右侧栏 + todo/team/plan 面板全部品牌化；8 个预览测试产 **18 张浅/深截图**（`docs/design/preview/`，已提交、可复现）。
- 原生化：样式 setter −30%、design 引用 −34%、`ui.*` −24%；原生 Button/PrimaryButton/Sidebar/Switch/TextArea/SearchField/Modal/SplitPercent 等已接管；markdown 628→106 行迁 `ui/markdown`（净减 452 行，获得高亮/表格/任务列表/流式）。
- 缺口：预览无自动视觉断言（人工评审）；2 处采样色 + 1 处 868 魔法宽；`ui.List/Tree` 未用（无虚拟化/树语义）；部分自绘 popover/stepper 为 a11y 验收有意保留（native-ui.md §8.4 有记录）。

### 4.3 目标 3：桌面交互本地化 + 功能对齐 + 全绿 —— ⚠️ **部分达成**

已交付（P0）：
- 系统菜单栏：App/File/Edit/View/Window 结构 + accelerator，与 `shortcuts.go` 同源（`internal/app/menu.go` 143 行，8 个测试；`SetMenu`/`HandleMenuAction` 跨包解耦）。
- 窗口状态：width/height/x/y/maximized/fullScreen 持久化到 `DataDir()/window.json`，损坏 fallback、原子写（`window_state.go` 116 行，5 个测试）。
- 系统通知：完成/失败/待审批三态，窗口非活跃才发，点击回焦会话（`notify.go` 351 行，17 个测试；ADR 0004）。
- 系统偏好：TextScale/HighContrast/ReduceMotion + 2x DPI 渲染冒烟（`preferences.go` 148 行，7 个测试）。
- **全绿**：HEAD 副本 gofmt/vet/build/`go test ./...` 全过；check-nocgo 四目标 PASS。

未闭环（详见 §4.4）：真机 IME/a11y/多平台；后台会话通知（HEAD 缺失）；通知动作与替换；独立设置窗口（工作树未提交 `settings_window.go`）；滚动条常显；功能矩阵的打包/更新、auto-review、Goal driver 等。

### 4.4 剩余风险与证据不足

| # | 风险/缺口 | 证据 | 影响 |
| --- | --- | --- | --- |
| R1 | **真机 IME/候选窗/组合文本未验证** | `native-ui.md:392`、`03-ux-design.md:137` 明列证据不足 | 中文/日文/韩文输入体验不确定；自绘控件+自定义样式下候选窗位置未测 |
| R2 | **屏幕阅读器/a11y 未做真机矩阵** | `native-ui.md:392`；Sidebar 是扁平 item 列表而非嵌套树（§9.2-2） | VoiceOver/Narrator 可读性未知；树层级语义缺失 |
| R3 | **后台会话通知在 HEAD 缺失** | ADR 0004 §4.1："OnUpdate 只派发当前会话" | 用户切走/离开窗口时，其他会话完成可能无通知（工作树中已加 `TurnFinishedHook`，未提交） |
| R4 | **通知能力不完整** | ADR 0004 §4.4：无通知 ID 替换/动作按钮（Allow/Deny）/退出后点击 | 与桌面产品预期有差距 |
| R5 | **独立设置窗口未交付** | native-ui §8.4-1（P1）；工作树新增 `settings_window.go` 未提交 | 仍是应用内 overlay（Web 式交互），与"桌面本地化"目标有距离 |
| R6 | **滚动条仅 hover overlay** | native-ui §12.3/§12.4-3；mygo v0.7.4 只有 overlay，无全局常显偏好 | Windows 用户预期"常显滚动条"可能不满足 |
| R7 | **功能对齐仍有硬缺口** | FUNCTIONAL-PARITY：打包/更新 ❌、Auto-review 权限 ❌、Goal round driver ❌、auto-memory ⚠️、反馈无联网 | "DSH 桌面替代"在迁移用户视角仍不完整 |
| R8 | **视觉回归无自动门禁** | 18 张预览为人工评审 PNG；像素门禁已退役 | 品牌视觉退化只能靠 review 发现 |
| R9 | **性能无基准（HEAD）** | benchmark 0；Trajectory 用 `ui.Scroll` 未虚拟化（native-ui §14.3-2） | 长会话/大 trace 性能风险不可回归 |
| R10 | **多平台真机未验证** | ADR 0003 §5（macOS Dock/Windows Alt 菜单栏/Linux Wayland）；ADR 0004 §4.6 | 菜单/窗口/通知跨平台行为未知 |
| R11 | **并发工作树（非 HEAD）** | 审计期间 21 文件 modified + 10 untracked（+1,566/−371，15:04→15:13） | 本报告数字均以 HEAD 为准；合并后需复测 |

---

## 5. 文档数字需修正项

| # | 文档 | 问题 | 建议 |
| --- | --- | --- | --- |
| D1 | `docs/STATUS.md:40-41` | "shell 生产 16,288→15,343、测试 14,514→13,233、测试函数 838→812"是**阶段 1 快照**（与 commit `8bf0053` 实测完全一致），但 HEAD 已是 **15,761 / 14,513 / 868**（shell 测试函数 282） | 标注"阶段 1 快照"；补当前值或改为链接本报告 |
| D2 | `docs/research/00-report.md:76` | UI 层 17,261 行算成 **34.6%**，正确为 **35.0%**（17,261/49,363=34.97%） | 改 35.0% |
| D3 | `docs/research/00-report.md:16` vs `:76` | Q1 用 **17,121**（不含 pixeltest 140），同页表格用 **17,261**（含 pixeltest），两种口径并存未注明 | 加脚注统一口径（如"17,121 不含 pixeltest"） |
| D4 | `docs/research/02-mygo-native.md:15,256` | 结论用 17,121 基线（与 00 表格 17,261 并存）；"Home 525+612=1,137"为改造前基线（HEAD 已 home.go 397） | 标注"改造前基线快照" |
| D5 | `docs/EVOLUTION.md:74,84,117,137` | 文件头有退役声明，但正文仍把 `internal/pixeltest`/`baseline/` 当可复用资产、写"542 个测试"（现 868） | 行内加"历史"标注或裁剪 |
| D6 | `docs/PLAN.md` §10.2 | 仍列 `scripts/lint-style-literals.sh`、`scripts/cross-build.sh`——**两个脚本均不存在** | 改为实际门禁命令（ci.sh 内含 check-nocgo） |
| D7 | `docs/design/native-ui.md` | §13 的子节编号仍是 12.1–12.4（应为 13.x）；§4.3 遗留 2/3（Trajectory 行、Home composer 本地实现）已被 §14/§16 完成，未回收 | 修编号；更新遗留状态 |
| D8 | `README.md` | "原生视觉迁移（进行中，六屏已完成）"与现状（8 屏+面板+右侧栏=九区完成、桌面 P0 完成）不一致 | 更新为九区/当前阶段；或直接指向 STATUS |
| D9 | `docs/research/04-parity-test-inventory.md` | "清理后 49 文件/270 Test"是清理时点快照；HEAD 已 **50 文件/282 Test** | 标注时点属性 |

---

## 6. 复现命令（全部只读）

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/data/mygo/.cache/go-build GOMODCACHE=/data/mygo/.cache/gomod GOSUMDB=off CGO_ENABLED=0

# 0) 提取两个提交做无干扰度量
rm -rf /tmp/zk-head /tmp/zk-base /tmp/zk-s1 && mkdir -p /tmp/zk-head /tmp/zk-base /tmp/zk-s1
cd /data/mygo && git archive HEAD    | tar -x -C /tmp/zk-head
git archive b974d92 | tar -x -C /tmp/zk-base   # 01 基线（像素对齐时代）
git archive 8bf0053 | tar -x -C /tmp/zk-s1     # 阶段 1 退役 checkpoint

# 1) LOC / 测试 / 基准（在 /tmp/zk-head 或 /tmp/zk-base 内执行）
find cmd internal -name '*.go' ! -name '*_test.go' | xargs cat | wc -l
find cmd internal -name '*_test.go' | xargs cat | wc -l
find internal/shell -maxdepth 1 -name '*.go' ! -name '*_test.go' | xargs cat | wc -l
grep -rh '^func Test' internal --include='*_test.go' | wc -l
grep -rh '^func Benchmark' internal --include='*_test.go' | wc -l
go list ./... | wc -l

# 2) 全绿验证（HEAD 干净副本，不污染工作树）
cd /tmp/zk-head
gofmt -l ./cmd ./internal; go vet ./...; go build ./...
time go test -count=1 ./...                                   # 预期 exit 0（本次 56.6s）
find . -newermt '-5 minutes' -type f | grep -v '\.log$'       # 预期为空：测试不写文件

# 3) 零 cgo 四目标（在仓库根）
cd /data/mygo && time sh scripts/check-nocgo.sh               # PASS (zero cgo)，四目标 ok

# 4) 门禁步骤（只读查看）
sed -n '1,70p' scripts/ci.sh
grep -n 'run_step\|pixel' scripts/evolve/gates.sh

# 5) 品牌覆盖与残余耦合
git ls-files docs/design/preview | wc -l                      # 19（18 png + README）
grep -rn '^func TestRender.*NativePreview' internal/shell/*_test.go | wc -l   # 8
grep -rn 'ui.Hex(' internal/shell --include='*.go' | grep -v _test            # 2（plugins.go:34-35）
grep -rn '868' internal/shell --include='*.go' | grep -v _test                # composer_menus.go:140
grep -rho 'ui\.[A-Z][A-Za-z0-9_]*' internal/shell --include='*.go' --exclude='*_test.go' | wc -l
grep -rn 'baseline\|pixeltest\|TestScreens' internal cmd --include='*.go' | grep -v _test
grep -rn 'func TestRender.*NativePreview' internal/shell/*_test.go

# 6) 删除量 / 归档核验
git log --oneline
git show --stat --oneline 8bf0053 | tail -2                   # 110 files, +2211/−6050
git diff --shortstat b974d92 HEAD
git tag -l                                                    # pre-native-ui
tar -tzf /data/zeokeel-backups/parity-assets-20261105.tar.gz | wc -l   # 862

# 7) 预览 opt-in
grep -n 'ZEOKEEL_UPDATE_PREVIEWS' internal/shell/livetest_helpers_test.go   # :38 注释 + :42 判断
```

---

## 附录：审计期间工作树并发改动（不属于 HEAD，供 Lead 收尾参考）

- 审计开始（15:04）工作树干净；审计期间另一执行线在改下一阶段代码，到 15:13 快照为：**21 个已修改 + 10 个未跟踪文件，+1,566/−371 行**。
- 方向（从 diff 与新增文件可辨）：`internal/app/settings_window.go` + ADR 0005（独立设置窗口）、`toolcards.go` 工具卡内容原生化、`controller.go` 加 `TurnFinishedHook` + `notify.go` 后台会话通知、`internal/shell/render_bench_test.go` 7 个渲染 benchmark、`a11y_native_test.go`、`menus.go`/`composer_style.go`/`layout.go` 死代码清理、2 个新预览测试（settings-window/toolcard，4 张新 PNG）。
- 影响：本报告所有数字以 **HEAD `b3ff65f`** 为准；这些未提交改动会让工作树 LOC/测试数小幅上升（15:13 时生产 50,218 / 测试 47,920 / Test 907），合并后需复跑门禁并更新 §1 数字。

---

## 附：证据分级

| 级别 | 内容 |
| --- | --- |
| 实测（命令可复现） | LOC/测试数/基准数、gofmt/vet/build/test 全绿、check-nocgo、预览 opt-in 无写入、`ui.*`/样式 setter 计数、残色/魔法值 grep、归档 862 项 |
| 文档引证 | ADR 0001–0004、native-ui.md、FUNCTIONAL-PARITY、STATUS 的阶段记录（已与提交交叉复核） |
| 判断/推断 | 死代码"死"的判定（grep 级，含接口假阳性）、R1–R11 的影响评估、文档修正建议 |
