# 01 · 代码度量与成本归因：DSH 像素对齐 UI vs mygo 原生方案

> 任务：task-1（code-metrics）· 审计时间：2026-11-05 12:30–13:10（Asia/Shanghai）· 仓库：`/data/mygo`（ZeoKeel，go1.27.3，**无 git 仓库**，`CGO_ENABLED=0`）
> 口径：只读审计，不写产品代码。所有数字给出复现命令；引用格式 `file:line`。快照类数字（`artifacts/`、`.probe/`）带时间戳——审计期间有并发队友在 `.probe/research/` 写原生 demo，数字会继续增长。

---

## 0. 结论先行（一页）

1. **规模复核实测**：仓库 Go 代码（`cmd/`+`internal/`）= **生产 49,363 行 / 测试 44,725 行**（145 / 166 个文件；全仓 838 个 `Test` 函数已复核）。UI 层（shell+design+icons+pixeltest）生产 **17,261 行**、测试 **14,816 行**；仅 `internal/shell` 一个包就占全仓 Go 的 **32.7%**（30,802/94,088），其中生产 16,288 行（占全仓生产 **33.0%**）、测试 14,514 行（占全仓测试 **32.5%**）。注：`.probe/verify-copy/` 里还有一份完整源码副本（236 个 `.go`、64,567 行、564 个测试函数），**不计入**仓库代码。
2. **纯视觉对齐层（可删）≈ 3,800–4,500 行生产代码**，占 UI 层生产代码 **22%–26%**（其中静态五屏 2,042 行只被静态回退路径与 23 个测试 fixture 使用，删除需同步改 23 个测试文件，工作量小）；再用 mygo 原生控件替换自研组件可**净省 1,450–2,400 行**（8%–14%）。两档合计 **5,250–6,900 行 = UI 层 30%–40%**，但只等于**全仓生产代码的 10.6%–14.0%**。
3. **测试可减 ≈ 4,300–5,100 行**（视觉/像素/几何/对比度/a11y 2,341 行 + 修-验循环测试 3,065 行的 60%–85% + 少量 design/icon/pixeltest 单测），占 UI 测试 **29%–34%**、全仓测试 **9.6%–11.4%**。
4. **对齐基建成本**：`baseline/` 605 文件 **106MB**、`artifacts/` 185 文件 8.9MB（112 张 png）、`.probe/` 18MB（726 文件，含一份 64,567 行源码副本）、全仓 662 张 png（时点）；脚本 ~852 行、文档 ~2,990 行；CI 每次多跑 **6 次** shell 像素测试（shell 包全量测试 **39.4–41.1s**）。
5. **迭代代价才是真正的大头**：仓库留下 **19 轮 verifier overlay**（837 行探针、81 分钟）、12 份修-验日志（7 次 fixer 重跑，21 分钟）、6 次 gates 运行、8 轮 verifier findings 失败清单；而**实际落地的像素门禁阈值是 2.0%–8.5%/屏（浅色）、6.2%–23%（深色/窄屏），比 PLAN §1.2 的 0.2%/屏 松 10–100 倍，且只测静态 `App{Ctrl:nil}`、真 UI 无像素门禁**（`docs/EVOLUTION.md:94` G7）。→ "1:1 像素"实际已被"回归预算"替代，继续投入边际收益很低。
6. **诚实结论（省不了想象中那么多）**：删掉的是对齐脚手架与视觉回归资产，**不删产品功能**。功能/交互/状态接线约 **10,350–12,000 行**（UI 层 60%–70%）必须保留并迁移；迁移本身需 **8–14 人日**（一次性总投入含回归/验收 12–20 人日），且原生外观会失去 DSH 视觉一致性。若目标是把整个仓库"腰斩"，本方案做不到（全仓 Go 仅减 ~10%–13%）。真正收益在**增量**：以后每新增/修改一个屏，UI 代码省 20%–40%，且不再背 106MB 基线、像素预算与 6 次/CI 的像素调用。

---

## 1. LOC 统计：生产 / 测试 / 分类

### 1.1 全仓与 UI 相关包（实测，含命令）

```bash
export PATH=/usr/local/go/bin:$PATH
# 全仓（cmd+internal）
find cmd internal -name '*.go' ! -name '*_test.go' | xargs cat | wc -l   # 49363
find cmd internal -name '*_test.go' | xargs cat | wc -l                  # 44725
# UI 层
find internal/shell -name '*.go' ! -name '*_test.go' | xargs cat | wc -l # 16288
find internal/shell -name '*_test.go' | xargs cat | wc -l                # 14514
find internal/design -name '*.go' ! -name '*_test.go' | xargs cat | wc -l # 752
find internal/icons  -name '*.go' ! -name '*_test.go' | xargs cat | wc -l # 81
find internal/pixeltest -name '*.go' ! -name '*_test.go' | xargs cat | wc -l # 140
# 测试函数
grep -rh '^func Test' internal --include='*_test.go' | wc -l               # 838
```

| 范围 | 生产行 | 测试行 | 生产/测试文件 | 占全仓 Go 行 |
| --- | ---: | ---: | --- | ---: |
| `internal/shell` | 16,288 | 14,514 | 38 / 56 | 32.7% |
| `internal/design` | 752 | 160 | 5 / 2 | 1.0% |
| `internal/icons` | 81 | 102 | 1 / 1 | 0.2% |
| `internal/pixeltest` | 140 | 40 | 2 / 1 | 0.2% |
| **UI 层小计** | **17,261** | **14,816** | 46 / 60 | **35.0% / 33.1%** |
| `internal/app`（宿主窗/托盘/服务接线，非像素层） | 1,691 | 3,887 | 2 / 19 | 5.9% |
| `internal/tools`（最大非 UI 包） | 9,965 | 6,482 | — | 17.5% |
| `internal/session` / `runtime` / `store` | 5,751 / 4,894 / 4,319 | 5,089 / 6,369 / 2,340 | — | 30.6% |
| 全仓合计 | **49,363** | **44,725** | 145 / 166 | 100% |

测试函数分布（`grep -rh '^func Test' internal --include='*_test.go' | wc -l` = 838）：shell **287**、tools 130、runtime 113、session 70、app 53、store 36、provider 30、state 24、prompt 20、automation/mcp 15、skills 11、agentteam 10、websearch 9、design 7、icons 4、pixeltest 3、e2e 1。
> 复核说明：`docs/EVOLUTION.md:71` 写的"542 个测试函数"是旧快照，现为 838（+54.6%）；没有 benchmark 函数（`grep '^func Benchmark'` = 0，与 EVOLUTION G10 一致）。

### 1.2 `internal/shell` 分类表（38 个生产文件 / 16,288 行）

分类依据（人工）：① 文件头 package doc 与常量注释是否声明"measured from DSH baseline"；② 是否只在 `Ctrl==nil`（静态）路径渲染；③ 样式 token 密度（见 §2）。完整文件清单见 §1.3。

| 类别 | 文件 | 行数 | 占 shell 生产 | 归类依据 |
| --- | --- | ---: | ---: | --- |
| **A. 五屏静态像素视图** | home.go 525、session.go 591、plugins.go 387、settings.go 368、automation.go 171 | **2,042** | 12.5% | 不是产品路径：只被 `&App{Screen:…}` 静态回退路径与 **23 个测试文件**的 fixture 使用（`grep -l 'App{Screen' internal/shell/*_test.go \| wc -l` = 23）；生产恒有 Ctrl/services（`internal/shell/app.go:42`；`internal/app/app.go:101`）。像素测试只是其中一类用法 |
| **B. live 功能视图/接线** | workspaces 1,232、right_sidebar 913、plugin_details 882、settings_ui 824、session_search 673、session_live 631、composer_menus 616、home_live_menus 612、start_tab 519 等 24 个文件 | **11,239** | 69.0% | 引用 `a.Ctrl` / Store / 事件与焦点逻辑（shell 生产共 378 处 `.Ctrl` 引用） |
| **C. 内容渲染** | markdown.go 628、toolcards.go 479 | **1,107** | 6.8% | 只做消息/工具内容 → mygo 元素映射 |
| **D. 壳 / 宿主接线** | app.go 542、chrome.go 241、assets.go 24 | **807** | 5.0% | App 结构、屏幕路由、主题/字体、侧栏折叠、Toast、窗口壳 |
| **E. i18n** | i18n.go 589 | **589** | 3.6% | 中英文文案表（350 键） |
| **F. 视觉基础层（shell 内）** | layout.go 297（全为几何常量/宽度计算）、motion.go 108（动效状态）、composer_style.go 99（样式 helper + 键处理） | **504** | 3.1% | 无业务逻辑（composer_style 中 `composerTextKey` 约 35 行为功能，见 §5） |

### 1.3 38 个生产文件清单（复核用）

```
1232 workspaces.go   913 right_sidebar.go  882 plugin_details.go  824 settings_ui.go
 673 session_search.go 631 session_live.go   628 markdown.go        616 composer_menus.go
 612 home_live_menus.go 591 session.go       589 i18n.go            542 app.go
 533 menus.go         525 home.go            519 start_tab.go       479 toolcards.go
 453 trajectory.go    438 automation_ui.go   424 models_ui.go       387 plugins.go
 368 settings.go      337 message_actions.go 330 shortcuts.go       297 layout.go
 272 attachments_ui.go 270 plugins_ui.go     241 chrome.go          218 draft_scope.go
 204 todo_ui.go       202 memory_ui.go       191 plan_ui.go          182 modes_ui.go
 171 automation.go    165 team_ui.go          118 sidebar_hover.go   108 motion.go
  99 composer_style.go  24 assets.go
```
命令：`find internal/shell -maxdepth 1 -name '*.go' ! -name '*_test.go' -printf '%f\n' | while read f; do echo "$(wc -l < internal/shell/$f) $f"; done | sort -rn`

---

## 2. 样式密度与 `ui.*` API 频率

### 2.1 样式调用计数（`internal/shell` 生产代码）

```bash
# 25 个样式方法，正则用 \bName\s*\(（容跨行链式调用）：
#   Width Height MinWidth MaxWidth Padding PaddingX PaddingY Radius Background TextColor
#   FontSize FontWeight Border BorderWidth BorderColor Shadow Gap Justify Margin
#   AlignItems Opacity Clip FixedLineHeight LineHeight NoWrap
grep -rhoE '\b(Width|Height|MinWidth|MaxWidth|Padding|PaddingX|PaddingY|Radius|Background|TextColor|FontSize|FontWeight|Border|BorderWidth|BorderColor|Shadow|Gap|Justify|Margin|AlignItems|Opacity|Clip|FixedLineHeight|LineHeight|NoWrap)\s*\(' \
  internal/shell --include='*.go' --exclude='*_test.go' | wc -l    # 2833
# design.* 令牌引用 974；ui.Hex/ui.RGBA/ui.Color 76；ui.* 调用合计 2070
```
> 口径修正：旧版用 `\.Name\b`（要求前导点在同一行）统计，跨行链式调用被漏计，导致 `Shadow`/`Margin` 被误报为 0（修正后 **Shadow 16、Margin 54**；例：`home.go:225-226,457-458`、`session.go:521-523`、`settings.go:142-144`、`settings_ui.go:231-233`、`session_search.go:501`、`sidebar_hover.go:50`、`session_live.go:266`），合计也从 2,552 修正为 **2,833**（+11.0%）。下表列出全部 25 个方法（按次数降序）。

| 方法 | 次数 | 方法 | 次数 | 方法 | 次数 |
| --- | ---: | --- | ---: | --- | ---: |
| TextColor | 430 | FontSize | 362 | Gap | 268 |
| Height | 225 | AlignItems | 219 | FixedLineHeight | 194 |
| Background | 190 | Radius | 162 | Padding | 142 |
| Width | 141 | PaddingX | 99 | FontWeight | 86 |
| Justify | 65 | NoWrap | 55 | **Margin** | **54** |
| Border | 37 | BorderColor | 26 | BorderWidth | 21 |
| LineHeight | 20 | **Shadow** | **16** | PaddingY | 7 |
| Clip | 5 | MaxWidth | 4 | MinWidth | 3 |
| Opacity | 2 | | | | |

> 25 个方法合计 **2,833**（已含跨行链式调用，逐方法相加 = 命令统计总数）；`Padding`/`AlignItems`/`FixedLineHeight`/`NoWrap` 为旧表未列出的高频 setter。

- **样式 setter 总量 2,833 次（修正口径）+ `design.*` 令牌 974 次 ≈ 3,807 个样式 token**；每行非空非注释代码（13,023 行）平均 **0.29 个样式 token**；含 ≥1 个 setter 的代码行 1,549 行（**11.9%** 代码行，平均每行 1.83 个 setter）。
- **静态五屏 vs live**：静态五屏 1,609 代码行 / 1,099 样式 token（**0.68 token/行**）；live 功能视图 9,066 代码行 / 2,460 token（**0.27/行**）。静态视图样式密度是 live 的 2.5 倍——印证其主要职责是像素/静态回归比对，而非产品功能。
- 样式 token 最多的文件：`session.go` 434 个 / 463 代码行（94%）、`home.go` 227/416（55%）、`menus.go` 222/455（49%）、`settings_ui.go` 178/675（26%）、`right_sidebar.go` 214/741（29%）。

### 2.2 `ui.*` API 频率（shell 生产，合计 2,070 次）

| 类别 | 调用（次数） | 小计 |
| --- | --- | ---: |
| 基础图元 | `Text` 310、`Row` 303、`Center` 218、`Column` 154、`Icon` 77、`Box` 69、`Span` 22、`SVG` 18、`Image` 17、`Element` 16、`SpaceBetween` 10、`Spacer` 5、`Stretch` 6 | **1,225（70%*）** |
| 自绘语义 | `RoleButton` 64、`RolePopUpButton` 6、`RoleMenuItemRadio` 5、`RoleSwitch` 4、`RoleMenuButton` 3 | 82 |
| **原生高级控件** | `Button` 55、`TextInput` 14、`Popover` 15、`Modal` 15、`Scroll` 13、`Menu` 9、`RichText` 7、`TabsBase` 3、`Spinner` 1、`TextArea` 1 | **133（6.4%*）** |
| 明确为 0 的原生控件 | `Toast`、`Tooltip`、`Select`、`MenuButton`、`Segmented`、`SearchField`、`Badge`、`Progress`、`Collapsible`、`Sidebar`、`List` | 0 |
| 上下文参数 | `Context` 322（不是控件；用于 `View(c *ui.Context)`） | — |

\* 分母为去掉 `Context` 后的 1,748 次。

**解读（对"改用 mygo 原生"很关键）**：当前 UI **不是"用 mygo 原生控件搭出来的"**，而是 **70% 用 `Text/Row/Center/Column/Box` 图元手搓布局**，Toast/Tooltip/Select/Sidebar/List/Collapsible 全部自研（0 次调用原生）。这有两层含义：

- 迁到原生控件确实能删掉大量"手搓布局 + 样式"代码（§5 档 b）；
- 但迁移是**重写**而非删开关：焦点顺序、键盘可达性、hover/展开态、虚拟化等行为要重新接（这正是 `.probe` 里失败最多的一类，§4）。`COMPONENT-PARITY.md:40-48` 也把"字体栈 / 字标 / 图标 / 工具卡 / hover 态 / AA"列为"怪异"根因，其中字体与 AA 是永久的移植地板（`docs/STATUS.md:58-59`：~85% 差异来自文本抗锯齿）。

### 2.3 "纯视觉 vs 功能"比例区间（方法与局限）

| 口径 | 结果 | 计算/依据 |
| --- | ---: | --- |
| ① 严格"样式行" | **11.9%**（shell 代码行） | 含样式 setter 的行 1,549 / 13,023（修正口径） |
| ② 文件级"无功能职责"视觉层 | **19.8%–20.1%**（UI 生产 17,261） | design 752 + pixeltest 140 + 静态五屏 2,042 + shell 视觉基础层 ~420–470 + icons 81 = 3,435–3,485 |
| ③ ② + live 视图可被原生默认样式吸收的样式表达式 | **24.2%–27.3%** | live 样式 token 2,460 × 0.3–0.5 行 ≈ 738–1,230 → 4,173–4,715 |
| ④ 理论上限（把交互态样式、图标美术全算进去） | **≤30%–33%** | 假设过多，不建议引用 |
| **推荐引用区间** | **纯视觉对齐层 ≈ UI 生产代码 20%–27%（上限 30%）** | ①–③ 的交叉（修正口径后 ②–③ 为 20%–27%） |

**方法局限（必须一起读）**：
1. 样式计数是正则近似：本版已用 `\bName\s*\(` 修正跨行链式调用的漏计（旧 `.Name\b` 口径会误报 `Shadow`/`Margin` = 0）；但按行归属仍可能低估/高估，误差估 ±15%；
2. 没有做 AST 函数级分类，无法机械区分"功能逻辑里的样式"与"纯装饰"；上表 ② 的文件级归因是人工判定，依据写在 §1.2；
3. 静态五屏并非 100% 无功能：`Ctrl` 引用 home.go 13、plugins.go 10、session.go 3、automation.go 2、settings.go 0——它们含少量"静态回退"分支；
4. 改用原生控件后样式并非归零，只是"从业务代码转移到 mygo 库 + 主题"；本报告统计的是**本仓库代码量**减少，不是总渲染代码量；
5. `ui.Hex/RGBA` 等颜色构造只统计了 76 次，因为颜色多数经 `design.Tokens` 字段间接使用（design 包 183 行 tokens.go 内是 `ui.Hex`）。

---

## 3. 对齐基建成本盘点

### 3.1 `baseline/`（605 文件 / **106MB**）

```bash
find baseline -type f | wc -l        # 605
du -sh baseline                      # 106M
for d in baseline/*/; do echo "$d $(find $d -type f|wc -l) $(du -sh $d|cut -f1)"; done
```

| 子目录 | 文件数 | 体积 | 内容 |
| --- | ---: | ---: | --- |
| `screenshots/` | 80（png） | 8.4M | DSH 逐屏/逐状态截图（1600×1000，含深色/窄屏） |
| `css/` | 53（json） | **63M** | 17 屏逐元素 computed style（43 属性，`docs/baseline/css.md:3`） |
| `controls/` | 74（json） | 1.9M | 可交互控件 dump |
| `behavior/` | 397（13 jsonl + 384 png） | 34M | 108 条行为记录（238 行 jsonl）+ 384 张前后对比图 |
| `README.md` | 1 | 4K | 采集流程 |
| `VERSION` | **缺失** | — | PLAN §1.2 要求锁定基线版本，`baseline/README.md:9,20` 明确要写；仓库没有（EVOLUTION:423 也记录） |

### 3.2 `artifacts/` 与 `.probe/`（迭代产物落点）

| 目录 | 文件 | 体积 | 说明 |
| --- | ---: | ---: | --- |
| `artifacts/` | 185（时点快照） | 8.9M | 14 个子目录：screens 6、diff 6、screens-dark 5、screens-narrow 5、diff-dark 4、diff-narrow 2、live 22、compare 32、verify 23、overlay 42、probe 27、verifier 3；**png 112** |
| `artifacts/overlay/` | 42 | — | **19 个 `zz_verifier_overlay*_test.go`（837 行）** + 22 个 JSON + go.mod——独立验证代理轮的探针残留 |
| `.probe/` | 726（时点快照） | 18M | 76 png（时点）、12 份日志、`verify-copy/`（整仓源码副本：236 个 `.go`、**64,567 行**、564 测试函数、3.1MB） |
| 全仓 png | **662**（时点） | — | `find . -path ./.cache -prune -o -name '*.png' -print \| wc -l` |
| 仓库卫生（非视觉但影响迭代） | — | — | `.cache` 79GB、`dist/` 88MB、根目录 `zeokeel`+`zeokeel.exe` ~92MB、**无 git**（EVOLUTION G1/G9） |

### 3.3 脚本与文档

| 资产 | 行数/体积 | 作用 |
| --- | ---: | --- |
| `scripts/cdp.mjs` | 509 行 | Chromium CDP 驱动：截图/控件 dump/CSS scan/行为探测（`baseline/README.md:20-23` 的采集工具） |
| `scripts/capture/*.json` | 25 文件 / 108K | 探测脚本的会话/弹层/终端 dump |
| `scripts/ci.sh` | 66 行 | CI 门禁：**像素步骤 6 次调用**（`scripts/ci.sh:38-44`：5 屏浅色循环 + dark/narrow） |
| `scripts/evolve/gates.sh` | 192 行 | 统一门禁 + flake 重跑 + 像素 masked/raw 指标（`:98-99` 两个像素 step） |
| `scripts/check-nocgo.sh` | 30 行 | 零 cgo + 四目标交叉编译 |
| `docs/baseline/` | **1,222 行**（index 173、css 220、motion 125、dark-narrow 126、coverage 18、screens/ 14 篇 560） | 基线施工图 |
| `docs/PLAN.md` | 1,097 行；其中 §5 UI 规格 417–613 约 197 行、§1.2/§5.9/§10.2 为门禁定义 | 1:1 规格 |
| UI 对齐专项文档 | `UI-PARITY-AUDIT.md` 60、`COMPONENT-PARITY.md` 52、`QUALITY.md` 52、`EVOLUTION.md` 434 | 审计/修复清单/门禁哲学 |

### 3.4 测试基建（视觉/像素相关）

| 资产 | LOC | 测试函数 | 说明 |
| --- | ---: | ---: | --- |
| `TestScreensBaseline`（`screens_test.go:18`） | 87 | 1 | 5 屏静态渲染 vs DSH 基线；masked 预算 2.0%–8.5%、raw 3.5%–11%（`screens_test.go:30-34`） |
| `TestScreensDarkNarrow`（`baseline_dark_test.go:87`） | 297 | 1 | 5 屏 dark/narrow；预算从实测值 +3pp 推导（`baseline_dark_test.go:43-48`），实际阈值 6.2%–23%（`:91-133`） |
| `internal/pixeltest`（diff/diffimg） | 140 生产 / 40 测试 | 3 | 仅像素 diff 工具 |
| 视觉/几何/对比度/a11y 测试 | **2,341** | **43** | 13 个文件：contrast 320、a11y 703、responsive 323、sidebar geometry 192、dark baseline 297 等 |
| 修-验循环回归测试 | **3,065** | **73** | `fixer_regression` 430 + `fixer_regression2` 614 + `verified_ui` 703 + `verified_composer` 489 + `verifier_probe` 829 |
| **合计** | **5,406（37.2% shell 测试 LOC）** | **116 / 287（40.4%）** | 其余 171 个函数是功能/交互测试 |

### 3.5 CI 门禁现状

- `scripts/ci.sh`：gofmt → vet → build → `go test ./...` → 零 cgo → 工具可调用 → **5 次浅色像素测试（逐屏）** → dark/narrow → 覆盖率 → race。
- 实测耗时：shell 包全量 **39.4–41.1s**（`.probe/ci-final.log`、`.probe/verifier-ci-fork-export-feedback.log`），占 `go test ./...` 的最大单项；像素测试本体 `TestScreensBaseline` 1.36s、`TestScreensDarkNarrow` 1.75s（含进程启动 wall 3.0s / 3.5s，作者实测，`ARTIFACTS=/tmp/zk-metrics`）；`gates.sh` quick 30s、full 43s（`docs/EVOLUTION.md:408`）。
- **测试默认写仓库**：`screens_test.go:37-39` 默认 `../../artifacts`，`capture_live_test.go:40` 硬编码 `../../artifacts/live` → 跑测试会重写 `artifacts/`（EVOLUTION G8:95）。
- **PLAN 承诺的门禁脚本不存在**：`scripts/pixel-gate.sh`、`lint-style-literals.sh`、`cross-build.sh`（`baseline/README.md:29`、PLAN §10.2:894-905 vs `scripts/` 实际内容；EVOLUTION G5:92）。

---

## 4. 迭代代价证据（`.probe` / EVOLUTION / STATUS）

### 4.1 已发生的对齐迭代（可数）

| 证据 | 数量/时间 | 说明 |
| --- | --- | --- |
| verifier overlay 轮次 | **19 个探针文件 / 837 行 / 19:43–21:04 = 81 分钟** | `artifacts/overlay/zz_verifier_overlay{1..19}_test.go`（mtime 2026-11-04） |
| verifier 失败清单 | **8 轮 FAIL**（101 行日志） | `artifacts/verifier-findings.log`：键盘焦点 ×8（More/回合跳转/终端 shell/账户菜单/模式菜单/工作区选择/视图选项/模型操作）、View options 不关闭、右栏跨屏消失、空会话 Send 仍可聚焦、Performance & usage 设置无效 |
| fixer 修-验重跑 | **7 份日志 / 23:32–23:53 = 21 分钟** | `.probe/fixer-baseline-test.log`、`fixer-test2.log`、`fixer-test-final{,2,3}.log`、`fixer-nocgo*.log` |
| 统一门禁跑 | **6 次 / 19:02–19:09 = 7 分钟** | `.evolve/state/logs/20261104-19*.log`（每轮含 fmt/vet/build/test/pixel-light/pixel-dark） |
| 独立验证副本 | **1 份整仓源码 / 64,567 行** | `.probe/verify-copy/`（236 个 `.go`、564 测试函数；mtime 18:52–19:13） |
| 基线截图采集 | 80 张基线截图 + 25 份 capture json | `scripts/cdp.mjs` + `.probe/`、`baseline/` |
| 任务侧记录 | M1 首轮→完成、M7 dark/narrow | `docs/STATUS.md:26-42`（首轮 settings **64.95%** → 完成时 masked 2.31%）、`docs/STATUS.md:191-193`（dark 12.54/22.49/4.05/3.66%） |
| 视觉回归产物 | 全仓 662 张 png（时点） | baseline 464 + artifacts 112 + .probe 76（时点） |

### 4.2 关键发现：落地的门禁 ≠ PLAN 的 1:1 门禁

- PLAN §1.2/§5.9 的目标：非文本区 ≤ **0.2%/屏**、文本区 SSIM ≥ 0.98、几何偏差 ≤ 1px（`docs/PLAN.md:45-54,598-612`）。
- 实际 `TestScreensBaseline` 的 masked 预算：home 2.0%、session **8.5%**、plugins 4.5%、settings 4.2%、automation 2.2%（`screens_test.go:30-34`）——**比 PLAN 松 10–42 倍**；dark/narrow 预算 6.2%–23%（`baseline_dark_test.go:91-133`），并且注释明写 `budget = measured dark diff + 3pp`（`:43-48`）。
- 实测（2026-11-05 复核，`ARTIFACTS=/tmp/zk-metrics`）：浅色 masked **0.93 / 6.03 / 2.90 / 2.28 / 1.18%**；dark **6.42 / 19.39 / 2.98 / 2.80%**；narrow **6.86 / 18.44%**。会话屏 6% 的差异大头是文本 AA 地板（STATUS:58-59 记录 ~85% 来自抗锯齿；容差 24/36/48 时 session 仍 5.59/5.05/4.52%）。
- **像素门禁只测静态 `App{Ctrl:nil}`**，真 UI 从未进入像素门禁（`live_screens_test.go:14-16` 注释直说"五屏基线只渲染 static"；EVOLUTION G7:94）。→ **"1:1 像素对齐"的目标没有可执行的验收器；当前门禁只是"布局回归不恶化"**。继续为"更像 DSH"投入，缺少可收敛的完成判据——`docs/QUALITY.md:3-7` 正是为此写的："验证代理找不到任何问题"不可收敛，"于是每轮都有新问题，永远达不到 clean"。
- 文档漂移：`docs/STATUS.md:193` 的 dark 数字（12.54/22.49/4.05/3.66）与本次实测（6.42/19.39/2.98/2.80）差最多 6.1pp；浅色 1.00/6.48/3.20/2.38/1.39 vs 0.93/6.03/2.90/2.28/1.18。说明"状态真源"本身也在漂移（EVOLUTION G6 已记录）。

### 4.3 失败类型归因（哪些是像素/视觉对齐导致的）

| 失败类型 | 实例 | 归属 |
| --- | --- | --- |
| 布局几何/模态 | settings 首轮 64.95%（`docs/STATUS.md:39`）、session 头部/tabs 11.26% | **纯视觉对齐** |
| 深色/窄屏 | dark session 22.49%、narrow session 31.47% vs 预算 32.0（余量 0.53pp，EVOLUTION:423） | **纯视觉对齐**（+基线采集不可比：DSH 截图带右侧栏/不同历史，只能加 mask 压低预算） |
| 字体/AA/图标 | ~85% 文本 AA、字标形状、Lucide vs DSH 图标（`COMPONENT-PARITY.md:40-48`） | **纯视觉对齐，且不可完全收敛** |
| 键盘焦点/弹层关闭/右栏停靠 | verifier 8 轮失败（§4.1） | 交互对齐；原生方案**大部分仍要**（若保留同功能），少量随自定义弹层被原生控件替代而消失 |
| flake | `TestScreensDarkNarrow` 首跑失败重跑通过；`TestExperimentalSwitches` 全量 2/3 失败、单跑 20/20（EVOLUTION G2:89） | 像素测试稳定性成本 |
| 门禁污染/假绿 | 测试写 `artifacts/`（G8）、裸 `go test` 缓存假绿（G12） | 门禁工程成本 |

### 4.4 每轮视觉对齐的实际成本（估）

- 机器成本：一次"改→渲染→看 diff"循环 = `go test ./internal/shell -run 'TestScreensBaseline|TestScreensDarkNarrow'` **≈3–4s 计算 + 1.4s/3.5s wall**（作者实测）；全量 shell 回归 **39–41s**；若按 19 轮 verifier + 7 次 fixer + 6 次 gates 的最低记录，单晚至少 **32 次**门禁级重跑。
- 人工成本：每轮要人（或代理）看 diff png、对照 CSS dump、改样式；`baseline/css/` 63MB 就是"逐元素 43 属性"的对照材料。
- 资产成本：106MB 基线 + 662 张 png（时点）+ 25 个 capture json + 1,222 行基线文档，且**基线没有版本锁**（无 `baseline/VERSION`）。
- **证据不足**：无法给出"像素对齐占总开发工时的百分比"——仓库无 git（无 commit/工时记录）、无时间跟踪；只能用日志时间戳做下界（见上）。若需要，必须补 git 历史或人工工时表。

---

## 5. 三档估算："改用 mygo 原生默认外观"能删/能换多少

> 三个档互斥、可相加：(a) 纯视觉对齐层删除；(b) 自研组件替换为 mygo 原生控件（**净省**）（c）不会减少的功能代码。区间给出依据，非精确值。

### 5.1 (a) 纯视觉对齐层 —— **删除**（不替换）

| 子项 | 行数 | 依据 |
| --- | ---: | --- |
| `internal/design` 全包（tokens/components/style/font/motion）——可保留 0–100 行最小主题 | 652–752 | 包级 doc 明写是 DSH 基线令牌（`internal/design/tokens.go:1-5,14-27`；`components.go` 全是 DSH 几何：Sidebar 280、BrandRow 64…） |
| `internal/pixeltest` | 140 | 仅 Diff/Compare/SavePNG 工具，产品零调用 |
| 五屏静态像素视图（保留 ~100 行原生空态回退） | 1,942–2,042 | 不是产品路径（`internal/app/app.go:101` 生产恒有 services），只被静态回退路径与 **23 个测试文件的 fixture** 使用；像素测试只是其中一类（`live_screens_test.go:14-16`）。删除需同步改 23 个测试文件（机械替换 fixture 构造） |
| shell 视觉基础层（layout 297 + motion 108 + composer_style 样式部分 ~50） | 400–450 | 保留 `composerTextKey` 约 35 行键处理（`composer_style.go:15-56`） |
| live 视图中可被原生默认样式吸收的样式表达式 | 700–1,100 | live 样式 token 2,460 × 0.3–0.45 行；需迁移验证，属假设性上限 |
| **生产小计** | **3,800–4,500** | **= UI 生产 22%–26%**（图标库 81 行因属"替换"，计入 §5.2 避免重复） |
| 测试删除 | 2,341（视觉/几何/对比度/a11y）+ 40（pixeltest）+ ~100（design/icons 部分）+ 修-验回归 1,800–2,600（3,065 的 60%–85%） | **4,300–5,100 行 = UI 测试 29%–34%** |
| 基建删除 | `baseline/` 106MB/605 文件；`artifacts/`（112 png）；`.probe/` 对齐残留（含 64,567 行 verify-copy）；`cdp.mjs` 509 + capture 25 json；`docs/baseline/` 1,222；`ci.sh` 6 次像素调用 + `gates.sh` 像素步骤；PLAN §5.9 流程 | 资产/流程成本，不折算成代码行 |
| 迁移工作量 | **0.5–1 人日** | 退化 static 路径为原生空态、同步改 23 个测试 fixture、删测试与 CI 步骤、摘基线文档 |

### 5.2 (b) 自研组件 → mygo 原生控件 —— **替换**（删 + 重写，净省）

| 组件 | 现状 LOC | 原生替代（已核实存在） | 净省 | 工作量 |
| --- | ---: | --- | ---: | ---: |
| `markdown.go` | 628 | `ui/markdown`（mygo v0.7.4 已含 view.go 672 + parser/table/math/code/reveal，共 3,423 行，支持流式 reveal） | 350–450 | 0.5–1.5d |
| `toolcards.go` | 479 | `ui/markdown` + `ui.Collapsible` | 250–350 | 1–2d |
| `composer_menus.go` + `menus.go` | 1,149 | `ui.PopoverMenu`/`ui.List`/`ui.Combobox` + 根包 context_menu | 300–500 | 2–3d |
| `i18n.go` | 589 | mygo 内置 `i18n` 包 | 200–350 | 1–2d |
| `internal/icons`（81 + svg） | 81 | `ui.SVG` + 通用图标 | 60–81 | 0.5d |
| 手搓控件 → 原生（当前 `Toast/Tooltip/Select/MenuButton/Sidebar/List/Badge/Collapsible/Progress` 调用全为 0；`RoleButton` 64 处、自绘 switch/chip/toast） | — | 原生 Button/Switch/Tabs/Select/TextArea/Toast/Tooltip | 400–700 | 2–4d |
| **净省小计** | — | — | **1,450–2,400（UI 生产 8%–14%）** | **8–14 人日** |

> 注意：`design/motion.go`（313 行）与 `shell/motion.go`（108 行）已算入 (a) 删除，不在此重复计。mygo 原生控件自带动效/主题，替换后这部分接线基本消失。
> 反例校准：并发队友的原生 demo（`.probe/research/native-demo/main.go`，294 行）用 mygo stock theme + stock widgets + `ui/markdown` 渲染了 Home + session 消息流样例——同等"像素演示"我们的静态五屏是 2,042 行。说明"原生外观"的**表达成本**确实低一个数量级，但 demo 不含 1:1 的交互/状态/无障碍接线。

### 5.3 (c) 与外观无关、**不会减少**的部分

| 子项 | 估算 LOC | 依据 |
| --- | ---: | --- |
| live 功能视图/接线（24 文件 11,239 减去被替换组件与可吸收样式） | **8,400–9,500** | workspaces 1,232、right_sidebar 913（真 Files/Terminal）、plugin_details 882、settings_ui 824、session_search 673、session_live 631、start_tab 519…；shell 生产 378 处 `.Ctrl` 引用、Store/事件/焦点/状态机是产品功能 |
| shell 壳 / 路由 / 窗口壳 | 700–800 | `app.go` 屏幕路由与主题、`chrome.go` 快捷键/侧栏折叠/Toast、assets |
| i18n 迁移后保留 | 240–390 | 文案内容不随外观走 |
| 可选最小 design 主题 | 0–150 | 若保留品牌色极少量 |
| 功能测试（shell 非视觉 ~9,100 + app 3,887 + session/tools/runtime/store 等） | **~39,600** | `go test` 的功能回归必须留 |
| **生产保留小计（自底向上）** | **≈9,750–10,850** | |
| **按总数减 (a)(b) 推算** | **10,350–12,000（UI 生产 60%–70%）** | 差额为替换组件后仍需保留的集成/接线代码 |

### 5.4 汇总与换算

| 指标 | 数值 | 占 UI 层 | 占全仓 |
| --- | ---: | ---: | ---: |
| 生产代码净减（a+b） | **5,250–6,900 行** | **30%–40%** | 10.6%–14.0%（/49,363） |
| 测试代码净减 | **4,300–5,100 行** | 29%–34% | 9.6%–11.4%（/44,725） |
| Go 代码合计净减 | **9,550–12,000 行** | — | **10.2%–12.8%（/94,088）** |
| 资产/流程净减 | baseline 106MB + 662 png 维护 + 6 次/CI 像素调用 + ~2,500 行脚本/文档 | — | — |
| 迁移工作量（一次性） | (a) 0.5–1d + (b) 8–14d + 集成/回归/UX 验收 3–5d | — | **≈12–20 人日（1 人 2–3 个迭代周期）** |

### 5.5 为什么说"省不了想象中那么多"（结论的一半）

1. **全仓 Go 只减 ~10%–13%**：因为 UI 层只占全仓 35%，而 UI 层里又有 54%–63% 是功能/交互代码。
2. **删掉的是一次性对齐脚手架**：design 令牌、静态五屏、像素工具、基线资产——这些成本大部分**已经沉没在仓库里**；删它们不会让产品功能变少，也不会让产品更快，只让仓库更轻、CI 更快、后续 UI 变更更省。
3. **替换不是白捡**：8–14 人日重写 + 回归风险，短期内可能是**负收益**；回本点取决于未来还要做多少 UI 变更。
4. **如果产品定位仍是"与 DSH 对齐"**：今天删掉的基线/像素门禁/视觉资产会以另一种形式回来（重新采基线、重建门禁），成本只是换了个位置。因此这不只是成本决策，而是**产品定位决策**（另一个调研负责 UX/定位侧）。
5. **UX 侧的已知硬地板**：~85% 的残余像素差异来自文本抗锯齿（STATUS:58-59），字体栈/字标/图标差异不可完全消除（COMPONENT-PARITY:40-48）——即"像素 1:1"在 mygo 渲染器上本身不可达；原生外观方案至少是"诚实一致"，而像素对齐方案是"永远差一点且无法验收"。

### 5.6 增量视角（真正该算的账）

按本次比例外推：视觉对齐相关代码约占 UI 生产的 20%–26%、UI 测试的 37%，且每个新屏/新组件的增量都要附带**双份渲染（static+live）、基线截图、像素预算、CI 像素步骤**。若团队后续还有 N 个屏/大改，原生方案的边际成本约为当前的一半以下——这是比"现在删多少行"更值得参考的数字。建议后续补一份**"新增一个屏"的成本对照 spike**（当前：静态视图 + live 视图 + 像素用例 + 基线采集；原生：仅 live 视图 + 交互测试）来验证该外推。

---

## 6. 复现命令清单（附录）

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/data/mygo/.cache/go-build GOMODCACHE=/data/mygo/.cache/gomod GOSUMDB=off CGO_ENABLED=0

# --- LOC ---
find cmd internal -name '*.go' ! -name '*_test.go' | xargs cat | wc -l            # 49363
find cmd internal -name '*_test.go' | xargs cat | wc -l                           # 44725
find internal/shell -maxdepth 1 -name '*.go' ! -name '*_test.go' -printf '%f\n' \
  | while read f; do echo "$(wc -l < internal/shell/$f) $f"; done | sort -rn       # 每文件行数
grep -rh '^func Test' internal --include='*_test.go' | wc -l                       # 838
grep -rh '^func Test' internal/shell --include='*_test.go' | wc -l                 # 287
grep -rh '^func Benchmark' internal --include='*_test.go' | wc -l                  # 0

# --- 样式密度 / API 频率 ---
grep -rhoE '\.(Width|Height|Gap|PaddingX|PaddingY|Radius|Background|TextColor|FontSize|FontWeight|Border|BorderWidth|BorderColor|Justify)\b' internal/shell --include='*.go' --exclude='*_test.go' | wc -l   # 2552（33 方法全集）
grep -rho '\bui\.[A-Z][A-Za-z0-9_]*'     internal/shell --include='*.go' --exclude='*_test.go' | wc -l   # 2070
grep -rho '\bdesign\.[A-Z][A-Za-z0-9_]*' internal/shell --include='*.go' --exclude='*_test.go' | wc -l   # 974

# --- 基建 ---
find baseline -type f | wc -l; du -sh baseline                      # 605 / 106M
for d in baseline/*/; do echo "$d $(find $d -type f|wc -l) $(du -sh $d|cut -f1)"; done
find artifacts -type f | wc -l; find .probe -type f | wc -l         # 快照数，随并发工作变化
find . -path ./.cache -prune -o -name '*.png' -print | wc -l        # 662
wc -l scripts/cdp.mjs scripts/ci.sh scripts/evolve/gates.sh scripts/check-nocgo.sh
wc -l docs/baseline/*.md docs/baseline/screens/*.md | tail -1       # 1222

# --- 像素门禁实测（不写仓库 artifacts） ---
ARTIFACTS=/tmp/zk-metrics go test -count=1 ./internal/shell -run '^TestScreensBaseline$' -v
ARTIFACTS=/tmp/zk-metrics go test -count=1 ./internal/shell -run '^TestScreensDarkNarrow$' -v
```

**证据不足声明**：
1. 无法量化"像素对齐占开发工时比例"——无 git、无工时记录，只有日志时间戳下界；需要 git 历史/人工工时表补。
2. 删除行数是"人工文件分类 + 正则样式计数"的区间估计，无 AST 级验证，误差约 ±15%。
3. mygo 原生控件对现有交互需求的覆盖度（命令面板、虚拟化列表、流式 markdown、多行 composer、a11y 焦点序）未做 spike，迁移风险未量化。
4. 未运行完整 `scripts/ci.sh`（会重写 `artifacts/`）；CI 时间取自既有日志与定向测试实测。
5. `artifacts/`、`.probe/` 文件数为审计时点快照（12:30–13:10），审计期间有并发队友写入 `.probe/research/`，最终数字可能更大。
