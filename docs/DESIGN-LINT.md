# DESIGN-LINT 确定性检查规格（设计契约的执行层）

> 本文是 [DESIGN.md](DESIGN.md) 的执行层规格：把「设计契约」变成 AI 每次写文件时**必定撞上**的确定性反馈。
> **当前状态：Phase 1a 已落地（2026-11-09）**：`internal/design/conform` 规则库、`internal/devtools/designlint` CLI、`internal/design/registry.json`、`.design-baseline.json`、`internal/design/conformance_test.go`、`scripts/design-check.sh`，已接入 verify/CI/Makefile 与 `go tool mygo vet`。DS-02/04/05/09 与 DS-06 的 Base 扩展、DS-08 完整文档镜像仍待 Phase 2/3。
> 分工：本文 = 规则/检测/接入/基线；[DESIGN.md](DESIGN.md) = 契约本身；[AGENT-SURFACES.md](AGENT-SURFACES.md) = 表面语义；[UI-CONFORMANCE.md](UI-CONFORMANCE.md) = 官方 API 事实。

---

## 0. 定位与分工

### 0.1 为什么需要 lint 而不是更多 prompt

软提示在长上下文里会衰减；AI 在生成某一行时可能"忘记"第 300 行之前读到的规则。确定性检查把违规成本从**人工 review 时**提前到**写入/测试时**，并给出可执行的修复指令。

### 0.2 检查矩阵（各管一段，不重复）

| 层 | 管什么 | 工具 | 反馈时机 |
|---|---|---|---|
| 静态契约 | 颜色/度量/入口/自绘登记/文档同步 | **designlint** + `go test` conformance | 写文件后 / pre-commit / CI |
| 交互语义 | 焦点/键盘/Esc/Default/状态切换 | `ui.NewTester` 帧测试 | `go test` |
| 视觉 | 布局/密度/层级/暗色 | `ui-snapshot` PNG + 人工 | PR |
| 端到端 | 真实操作链 | `ui-drive` | 发布前 |
| 框架生命周期 | 元素跨帧存储/goroutine 捕获 | `go tool mygo vet`（go.mod 已声明 tool 指令） | CI |

### 0.3 设计原则

1. **可判定**：规则必须能给出 `file:line:col` 与修复建议；判不了的交给帧测试，不要写成"建议检查"。
2. **低误报**：用 AST + type info，不用纯正则（正则版仅作 Phase 0 过渡，见附录 B）。
3. **只对新增强制**：存量进 baseline；`--diff` 模式只查改动文件，避免一次 100+ 报错淹没 AI。
4. **同源**：规则从 `internal/design/registry.json`（[DESIGN.md](DESIGN.md) 附录 A；离线环境载体为 JSON）读取组件/场景/白名单，不硬编码在代码里。
5. **可退出**：规则有生命周期（warn → enforce → 退役）与误报统计。

---

## 1. 执行架构

### 1.1 形态（Phase 1a 已落地）

```
internal/design/conform/                 // 规则库：纯标准库 go/parser + go/ast
├── conform.go                           // Rule 元数据、Scan、AllowEntry、Violation
├── scan.go                              // 文件遍历
├── rules.go                             // DS-01/03/06/06b/07/10
├── baseline.go                          // baseline 读写/匹配
└── conform_test.go

internal/devtools/designlint/main.go      // 薄 CLI：--diff/--json/--rules/--baseline/--strict/--write-baseline
internal/design/registry.json             // 机器可读契约（go:embed + encoding/json）
internal/design/registry.go
internal/design/conformance_test.go
scripts/design-check.sh                   // Phase 0 grep + 阈值（verify/CI 也跑）
```

- 技术栈：**标准库 AST（语法级）**。离线环境无法引入 `golang.org/x/tools`（依赖不在本地模块缓存），v1 不做 type-aware；误报由 baseline 与 allowlist 收口，需要类型信息时再升级。
- 设计规则不进 mygo 上游：`go tool mygo vet` 管框架生命周期，designlint 管应用设计契约；两者在 verify/CI 并行（go.mod 已声明 tool 指令）。
- 退出码与输出：`0` 通过、`1` 有违规、`2` 工具错误；text 行格式 `file:line:col: DS-xx [AP-xx] message → fix`。

### 1.2 CLI

```bash
# 全仓（CI）
go run ./internal/devtools/designlint ./...

# 只查改动（AI 写文件后 / pre-commit；性能最好）
go run ./internal/devtools/designlint --diff --base=HEAD ./...

# 机器可读（作为 agent 的 tool result 回灌）
go run ./internal/devtools/designlint --diff --json ./...
```

| 参数 | 说明 | 默认 |
|---|---|---|
| `--diff` | 只检查 git 改动文件（staged + unstaged + untracked） | false |
| `--base` | diff 基准 ref | `HEAD` |
| `--json` | 输出 JSON（schema 见 §2.2） | false |
| `--rules` | 逗号分隔规则 ID；用于分阶段启用 | 全部 |
| `--baseline` | baseline 文件路径 | `.design-baseline.json` |
| `--strict` | 把 `warn` 升级为阻塞；baseline 存量仍豁免（清零冲刺需手工删 baseline 条目） | false |

退出码：`0` 通过；`1` 有违规；`2` 工具错误（registry 不存在/解析失败）。

### 1.3 输出格式（text）

```
internal/ui/home/composer.go:634:7: DS-03 [AP-03] 裸数值度量：MinHeight(88)
  fix: 控件高度走 design 控件高度 token（或在 design 中新增 ControlHeight*），不要写裸值
  ref: docs/DESIGN.md §5.4
```

规则行格式：`<规则ID> [<反模式ID>] <一句话问题>：<代码片段>`；`fix:` 必须是**可直接照做的替换建议**。

### 1.4 输出格式（JSON）

```json
{
  "version": 1,
  "files": 3,
  "violations": [
    {
      "rule": "DS-03",
      "ap": "AP-03",
      "severity": "error",
      "file": "internal/ui/session/view.go",
      "line": 3057,
      "column": 4,
      "message": "裸数值阴影：Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.12))",
      "fix": "用 design.Apply(el, t.ShadowOverlay())",
      "doc": "docs/DESIGN.md#54",
      "baseline": false
    }
  ]
}
```

### 1.5 判定级别

| 级别 | CI 行为 | 用途 |
|---|---|---|
| `error` | 阻断 | 已 enforce 的规则 |
| `warn` | 不阻断，输出+统计 | 新规则试运行、误报观察、语义类 |
| `info` | 仅 `--json` | 统计/迁移提示（如"应该用某入口"） |

---

## 2. 规则总表

| ID | 名称 | 级别（目标） | 检测 | 对应反模式 | 阶段 |
|---|---|---|---|---|---|
| DS-01 | 颜色字面量禁止 | error | AST：`ui.RGBA/ui.RGB/ui.Hex` 调用 + hex 字符串字面量；白名单 `internal/brand`、`internal/design`（阴影实现） | AP-01 | 1 |
| DS-02 | 主题/状态色覆写与语义 | error（颜色）/warn（语义） | 链式 `.Background/.TextColor/.Border/.Shadow` 的实参不是主题/token 取得的颜色；状态色当正文（`TextColor(t.Success)`、`t.Danger` 或 `t.Warning` 而非 `*Text`） | AP-02/AP-18 | 2 |
| DS-03 | 裸数值度量 | error | 数值字面量实参出现在固定方法集合（见 §3.3） | AP-03/AP-13 | 1 |
| DS-04 | Agent surface 绕过入口 | error（按 registry） | type info：`contracts.ToolCall/ApprovalRequest/PlanReview/UserQuestion/ChatMessage` 的渲染不在入口包/入口函数内 | AP-05/AP-06 | 3 |
| DS-05 | 默认组件多余样式覆写 | warn→error | 官方/入口组件的调用链上出现 `.Background/.Border/.Radius(非 token)/.Shadow/.FontSize` 覆写 | AP-07 | 2 |
| DS-06 | 未登记自绘 | error | `ui.ButtonBase(` 等自绘调用点不在 `design-allowlist`/registry；`*Base` 使用未登记 | AP-04/AP-11 | 2 |
| DS-07 | 图标来源/尺寸 | error | UI 包内 emoji/Unicode 符号字符串；`ui.MustParseSVG` 在 `internal/icons` 之外；图标尺寸字面量（DS-03 覆盖） | AP-10/AP-17 | 1 |
| DS-08 | 文档/registry 同步 + 虚构 API | error | `go test`：registry ↔ DESIGN.md 表格 ↔ 白名单一致；源码出现 §7.4 禁用 API 名 | AP-14/AP-15 | 3 |
| DS-09 | 状态映射重复/语义降级 | warn | AST：新增 `statusLabel/normalizeStatus/toolStatusColors/...` 同义函数；`cancelled` 用 Danger 色 | AP-08/AP-09 | 3 |
| DS-10 | 对话框语义 | error（结构）/帧测试（行为） | `uikit.DialogAction{Danger:true, Default:true}` 组合；`AlertDialog` 用于结构化内容；动作顺序由入口保证 | AP-12 | 2 |

> **实现状态（2026-11-09，Phase 1a）**：DS-01、DS-03（度量 error；FontWeight 暂为 warn）、DS-06（ButtonBase/DialogBase）、DS-06b、DS-07、DS-10（Danger+Default 结构）已实现；DS-02/04/05/09 未实现（DS-08 由 conformance 测试部分覆盖）。

> 语义类规则（Default 焦点、Esc 行为、流式几何）无法可靠静态判定，**以帧测试为准**（§6）；lint 只报结构性错误与可判定的模式。

---

## 3. 规则详述

### 3.1 DS-01 颜色字面量

**目标**：UI 层不得出现任何颜色字面量；颜色只能来自 `internal/brand` → `internal/design` → `c.Theme()`。

**检测**：
1. `CallExpr`，`Fun` 解析为 `github.com/egoist/mygo/ui.RGBA/RGB/Hex/HexColor` 等颜色构造器；
2. 字符串字面量匹配 `^#[0-9a-fA-F]{3,8}$`；
3. 白名单包：`internal/brand`（品牌来源）、`internal/design`（阴影/预设实现）、`internal/icons`（SVG 是图标不是颜色）、`_test.go`（断言用）。

**反例 / 正例**：

```go
// DS-01 违规
el.Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.12))       // → t.ShadowOverlay()
ui.Text(c, "x").TextColor(ui.Hex("#333"))            // → t.LabelPrimary
el.Background(ui.Hex("#F9FAFB"))                     // → t.BgSidebar

// 允许
design.Apply(el, t.Panel())
el.Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, alpha))      // 仅 internal/design 内部
```

**修复文案**：`用 design.Tokens（design.Of(c)）或 c.Theme() 的语义色；阴影用 t.ShadowOverlay()/t.ShadowComposer()`。

**误报边界**：测试里的颜色断言、`internal/brand/palette.go` 的 hex 常量、icons 的 SVG 内嵌颜色（图标资产）不报。

### 3.2 DS-02 主题/状态色覆写与语义

**目标**：官方件的状态色由 mygo 主题管理，覆写背景会关掉原生 hover/pressed/disabled；状态语义不可降级。

**检测**：
1. 对 `ui.Button/PrimaryButton/Toggle/...` 的链式调用里出现 `.Background(`/`.Border(`/`.Shadow(`，且实参是颜色字面量或非 token 表达式 → 报（与 DS-01 联动）；
2. `.TextColor(t.Success|t.Danger|t.Warning)` 用于 `ui.Text`（非 fill/badge/icon）→ warn：应使用 `SuccessText/DangerText/WarningText`；
3. `.Background(t.Success|t.Danger|t.Warning)` 大面积容器 → warn：状态色只做 fill/border/icon。

**反例 / 正例**：

```go
// DS-02 违规
design.Apply(ui.PrimaryButton(c, "保存"), t.PrimaryButton()).Background(t.Accent) // 覆写背景，丢 hover/pressed
ui.Text(c, "失败").TextColor(t.Danger)                                            // 状态色当正文
// 正确
design.Apply(ui.PrimaryButton(c, "保存"), t.PrimaryButton()) // 只设度量
design.Apply(ui.Text(c, "失败"), t.Font(design.TypeXS)).TextColor(t.DangerText)
```

**误报边界**：`Badge`/`Spinner`/状态点用 `Success/Danger/Warning` 作 fill/前景是合法的；自绘 base 内部按状态换色属 §7 白名单，单独豁免。

### 3.3 DS-03 裸数值度量

**目标**：字号/间距/圆角/尺寸/阴影只从 design token 取，避免绕过 type scale、TextScale 与 4 DIP 网格。

**监控方法集**（出现数值字面量实参即报）：

```
FontSize, FontWeight, Padding, PaddingX, PaddingY, PaddingStart, PaddingEnd,
PaddingTop, PaddingBottom, Gap, GapX, GapY, Radius, Height, Width,
MinHeight, MaxHeight, MinWidth, MaxWidth, Size, Shadow
```

**白名单**：`internal/design`（token 实现）、`internal/brand`、测试；`FontWeight(400/500/600/700)` 在有 `t.Font(...)` 同链时降为 warn（heading weight 尚未 role 化，见 DESIGN.md D6）。

**反例 / 正例**：

```go
// DS-03 违规
ui.Text(c, title).FontSize(20).FontWeight(600)     // → design.Apply(..., t.Font(design.TypeL)).FontWeight(600)
ui.Column(c).Gap(8)                                // → .Gap(design.Space2)
ui.Column(c).Padding(24)                           // → .Padding(design.Space6)
el.Width(560)                                      // → .Width(design.DialogWidthM)
ui.Icon(c, ic).Size(20, 20)                        // → 等 uikit.IconButton/IconSize token
// 正确
design.Apply(ui.Text(c, title), t.Font(design.TypeL))
ui.Column(c).Gap(design.Space2).Padding(design.Space6)
```

**修复文案**：给出最近的 token 映射（`8→Space2`、`12→Space3`、`16→Space4`、`20→Space5`、`24→Space6`、`560→DialogWidthM`…）。

**误报边界**：布局算法内部（`design` 包、自绘 Base 的测量代码）豁免；非 UI 包不检查。

### 3.4 DS-04 Agent surface 绕过入口

**目标**：Agent 场景只能从 [DESIGN.md](DESIGN.md) §2.1 的入口函数进入；禁止在业务模块里另起一套渲染。

**检测（registry 驱动）**：

1. 从 `internal/design/registry.json` 读 `scenarios[].entry` 与 `components[].allowed/forbidden`；
2. 用 type info 找关键类型的渲染上下文：
   - `contracts.ToolCall` 的字段被用来直接构造 `ui.Text/ui.Button` 文本（而非传给 `uikit.ToolRow/ToolCallCard`）；
   - `contracts.ApprovalRequest`/`contracts.PlanReview`/`contracts.UserQuestion` 在 `internal/ui/approval|interaction` 之外被渲染；
   - `contracts.ChatMessage` 的正文渲染不在 `uikit.SessionThread`/session transcript 入口内（按包+函数前缀 allowlist）。
3. 阶段实现顺序：先做「包级 allowlist」（哪些文件允许出现这些类型的渲染），再做「函数级」。

**反例 / 正例**：

```go
// DS-04 违规：session 模块里自己拼工具卡
design.Apply(ui.Column(c), t.Panel()).Children(func() { /* 卡头 + 状态 */ })
// 正确
uikit.ToolRow(c, uikit.ToolRowOptions{ /* tool fields */ })
```

**误报边界**：入口实现内部；`*_test.go`；`internal/ui/uimodtest`（测试夹具）。

### 3.5 DS-05 默认组件多余样式覆写

**目标**：默认即设计默认；需要变化时走预设/入口，不逐处覆写。

**检测**：官方/入口组件调用后 3 层链式内出现 `.Background/.Border/.Shadow/.Radius(非 token)/.FontSize`；或显式传入零值/默认 options（如 `editor.State{}` 全默认却逐项设置）。

**处理**：warn 起步；规则稳定后对「覆写背景/边框」升 error（与 DS-02 合并）。

### 3.6 DS-06 未登记自绘

**目标**：自绘只能出现在 [DESIGN-LINT 白名单]（`internal/design/registry.json` 的 `allowlist`）与 [DESIGN.md](DESIGN.md) §7.1；并保留官方交互语义。

**检测**：
1. `ui.ButtonBase(` / `ui.DialogBase(` / `ui.CollapsibleBase(` 等 base 调用点，按 文件+符号 匹配 allowlist；
2. allowlist 条目必须含 `owner`、`reason`、`until`；缺一即报（DS-06b）；
3. 自绘按钮必须同时有 `Label`/`Tooltip`（AST 链式检查）且处理 `Hovered/Pressed/Focused`（字符串级检查 `Hovered()`/`Pressed()`；无法判定时 warn + 建议帧测试）。

**反例 / 正例**：

```go
// 未登记 → DS-06
b := ui.ButtonBase(c.Key("x"))
b.Children(func() { ui.Icon(c, icons.Close()) }) // 无 Label/hover 处理

// 已登记（allowlist 有条目 + 代码注释理由 + hover/focus）
// design:allowed AP-04 owner=uiroot until=2027-06-30: 无官方 IconButton
b := ui.ButtonBase(c.Key("inspector.close")).Label("关闭检查器")
if b.Hovered() { b.Background(t.BgHover) }
```

### 3.7 DS-07 图标来源 / 尺寸

**目标**：图标只从 `internal/icons` + `ui.Icon` 取；禁 emoji/Unicode/手写 SVG；图标按钮尺寸统一。

**检测**：
1. UI 包字符串字面量包含 emoji 区段（U+1F300–U+1FAFF、U+2600–U+27BF；排除 `↑↓→×` 等排版字符白名单）→ error；
2. `ui.MustParseSVG(`/`ui.ParseSVG(` 出现在 `internal/icons` 之外 → error；
3. 图标按钮缺 `Label` → warn（a11y）。

### 3.8 DS-08 文档/registry 同步

**目标**：文档、registry、白名单、规则 ID 同源，防止"文档一套、代码一套"。

**检测（`go test`，不是 analyzer）**：
1. `internal/design/registry.json` 中每个 `components[].id` 在 [DESIGN.md](DESIGN.md) §1.2/§6.3 有对应行，反之亦然；
2. `scenarios[].id` 与 §2.1 行一致；`anti_patterns[].id` 与 §3.1 一致；
3. allowlist 每条有 owner/until/reason，且 `until` 未过期；
4. 源码中出现 [DESIGN.md](DESIGN.md) §7.4 的禁用 API（`ui.DialogFooter` 等）→ error（防虚构 API）。

### 3.9 DS-09 状态映射重复 / 语义降级

**目标**：14 个并行的状态→(文案,颜色) 函数收敛到 `uikit.StatusText/StatusBadge`；状态语义不降级。

**检测**：
1. 新增同名/同义函数（`statusLabel`、`normalizeStatus`、`statusColor`、`toolStatusColors`、`taskStatusLabel`、`subagentStatusLabel`…）不在 allowlist → warn；
2. `cancelled` 状态映射到 `Danger/DangerText` → error（用户取消是中性）；
3. `error` 状态映射到 `Warning/LabelTertiary` → error。

### 3.10 DS-10 对话框语义

**目标**：动作顺序/Default/Esc 语义统一；危险动作不得成为默认。

**检测**：
1. `uikit.DialogAction{Danger: true, Default: true}` 组合 → error；
2. `uikit.DialogFooter` 中 `Primary` 动作不在最后 → 入口内部已排序，外部调用按声明；warn；
3. 对话框宽度数值字面量（`Width(560)` 等）→ error（DS-03 覆盖，这里给出 DialogWidth 映射）；
4. 语义行为（Esc 是否取消、Default 是否误触、外部点击）→ **帧测试**（§6），lint 不判定。

---

## 4. 豁免与 baseline

### 4.1 行内豁免（极少用，需 owner + 到期）

```go
//design:allow DS-06 owner=uiroot until=2027-06-30: 无官方 IconButton，自绘关闭按钮保留 hover/focus
b := ui.ButtonBase(c.Key("inspector.close"))
```

解析规则：`//design:allow <规则ID> owner=<owner> until=<yyyy-mm-dd>: <理由>`；缺字段、过期、owner 为空 → 豁免无效并报 DS-06b。
**禁止**：用豁免处理颜色/字号/尺寸（DS-01/DS-03）；这些必须改代码或进 baseline。

### 4.2 baseline（存量，只降不升）

```json
{
  "version": 1,
  "generated": "2026-11-09",
  "ignored": [
    {
      "rule": "DS-03",
      "file": "internal/ui/session/view.go",
      "line": 3057,
      "text": "Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.12))",
      "owner": "session",
      "expires": "2027-03-31"
    }
  ]
}
```

- 匹配键：`rule + file + 归一化代码文本`（行号可变，文本优先）。
- `--strict` 时 baseline 条目也失败（清零冲刺用）。
- 新违规**不允许**进 baseline；CI 校验 `count(baseline) <= 上次`（防止偷偷加）。
- 季度清理：`expires` 到期条目必须修复或重批。

### 4.3 CODEOWNERS

`.design-baseline.json`、`internal/design/registry.json`、`internal/uikit/` 由设计系统 owner 审批；禁止普通 PR 仅为了过 lint 修改白名单。

---

## 5. 接入点（从软到硬）

### 5.1 接入优先级

1. **`go test` conformance**（最可靠）：天然在 AI 回路和 CI 里，无法被"忘记运行"绕过。
2. **写文件后 hook**：Agent 每次写 Go 文件后运行 `designlint --diff --json`，把结果作为 tool result 回灌。
3. **pre-commit**：staged 文件快速检查（`--diff --base=HEAD`）。
4. **`scripts/verify.sh` + CI**：全量 required check。
5. **`go tool mygo vet` 并行**：框架生命周期检查，与 designlint 互不替代。

### 5.2 `go.mod` tool 指令与 `go tool mygo vet`

[../go.mod](../go.mod) 已声明（Phase 1a）：

```go
tool github.com/egoist/mygo/cmd/mygo
```

```bash
go tool mygo vet .        # 框架规则：struct 存 Element/parts、goroutine 捕获 Context…
go run ./internal/devtools/designlint --strict --baseline .design-baseline.json ./...   # 设计契约
```

两者均已接入 `scripts/verify.sh` 与 CI；离线环境实测可用（`CGO_ENABLED=0` 复测通过）。

### 5.3 CI 步骤（已落地，`.github/workflows/ci.yml`）

```yaml
- name: Design contract
  run: |
    go run ./internal/devtools/designlint --strict --baseline .design-baseline.json ./...
    bash scripts/design-check.sh
    go tool mygo vet .
```

[../scripts/verify.sh](../scripts/verify.sh) 对应步骤：`designlint (--strict)`、`design-check (phase 0 grep)`、`go tool mygo vet`（均在 test 之前）。

### 5.4 写后 hook（agent 侧）

Agent 每完成一次 `.go` 写入（或每批）：

```bash
go run ./internal/devtools/designlint --diff --json ./... > .cache/designlint.json
```

- 有 `error`：把 JSON 的 `file/line/message/fix` 作为 tool feedback 回灌，要求 AI 修正后重跑（最多 2 轮，再失败转人工）。
- 只有 `warn`：继续，但在最终汇报里列出。

### 5.5 pre-commit（可选）

```bash
#!/usr/bin/env bash
# .githooks/pre-commit
set -euo pipefail
go run ./internal/devtools/designlint --diff --base=HEAD ./...
```

---

## 6. conformance 测试规格（`go test`，与 lint 同 PR）

> 位置建议：`internal/design/conformance_test.go`（或 `internal/uikit/conformance_test.go`）。这些测试不依赖 analyzer 进程，作为 lint 的补充与兜底。

| 测试名 | 断言 | 来源 |
|---|---|---|
| `TestDesignRegistryMatchesDocs` | registry 的 components/scenarios/anti_patterns 与 DESIGN.md 表格逐行一致 | 解析 Markdown 表格 + YAML |
| `TestDesignAllowlistIsValid` | allowlist 条目有 owner/reason/until，且未过期 | registry |
| `TestNoLiteralColors` | 对 `internal/ui`/`internal/uiroot` 扫描：无 `ui.RGBA/ui.Hex`（白名单外） | go/analysis 或 `go vet` 风格检查 |
| `TestNoRawMetricLiterals` | 固定方法集无数值字面量（白名单外） | 同上 |
| `TestAgentSurfacesUseEntries` | Agent surface 的渲染函数调用入口（allowlist 文件级别） | AST |
| `TestDialogFooterHasNoDangerDefault` | 全仓 `DialogAction` 字面量无 `Danger: true, Default: true` | AST |
| `TestOfficialDialogFooterIsNotInvented` | 源码不出现 `ui.DialogFooter`/`.Default()` 等虚构 API | 字符串/AST |
| `TestStatusColorsUseTextVariants` | 状态色作正文时用 `*Text` 变体 | AST（warn 级，可先跳过） |

示例（`TestNoLiteralColors` 伪代码）：

```go
func TestNoLiteralColors(t *testing.T) {
    fsys := os.DirFS("../..")
    pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedSyntax | packages.NeedTypesInfo}, "./internal/ui/...", "./internal/uiroot/...")
    if err != nil { t.Fatal(err) }
    for _, pkg := range pkgs {
        for _, file := range pkg.Syntax {
            ast.Inspect(file, func(n ast.Node) bool {
                // 找 ui.RGBA/ui.Hex 调用与 hex 字符串；白名单包跳过
                return true
            })
        }
    }
}
```

---

## 7. 落地阶段与验收

| 阶段 | 内容 | 状态 |
|---|---|---|
| 0 过渡 | grep 检查 | ✅ 已自动化进 `scripts/design-check.sh` |
| 1 骨架 | designlint CLI + conform 规则库 + registry.json + baseline + conformance + verify/CI + `go tool mygo vet` | ✅ 2026-11-09 完成（DS-01/03/06/06b/07/10） |
| 2 收紧 | DS-02/05/09 规则；DS-06 扩展到 CollapsibleBase/PopoverBase/TabsBase/ToastBase；`--diff` 阻断 | ✅ 已完成主要结构收敛：2a IconButton、2b Status、2c ToolCallCard；DS-02/05/09 规则与 Base 扩展仍待后续 |
| 3 全量 | DS-04 type-aware（需 x/tools）；DS-08 完整文档镜像；baseline 时间盒清零 | ⏳ 计划 |

### 验收标准（Definition of done）

1. 任一新 PR：`designlint` 0 error；`warn` 有明确理由或被修复。
2. 每条契约规则（本文 §2 表）都能在一条命令/测试里报出来。
3. baseline 总数单调下降；新增 baseline 条目被 CI 拒绝。
4. 误报率 < 5%（按 warn 阶段统计）；规则有退役记录。
5. 文档、registry、白名单三者同步测试通过（DS-08）。

---

## 8. 与 Agent 客户端的集成

Agent 客户端（ZeoKeel 自身）在工具执行后置 hook 中调用 designlint，把结果作为**结构化工具反馈**回灌：

```
[designlint] 2 violations (1 error, 1 warning)
1. internal/ui/session/view.go:3057 DS-03 [AP-03] 裸数值阴影
   fix: design.Apply(el, t.ShadowOverlay())
2. internal/ui/session/view.go:3712 DS-05 [AP-07] 给默认组件加样式覆写
   fix: 删除 .Background(...)；需要区分时改用入口组件的 variant
Run again after fixing: go run ./internal/devtools/designlint --diff ./...
```

约束：
- 反馈必须含 `file:line`、规则 ID、反模式 ID、**修复建议**、复跑命令；
- 最多 2 轮自动修复，仍失败则转人工并附带报告；
- 不得把 `warn` 当作 error 阻塞（避免 AI 为过 warn 写出怪代码）。

---

## 附录 A：本仓库实测基线（2026-11-09，Phase 1a/1b 后）

> 来源：`.cache/design-research/zeokeel-inventory.md` + designlint/baseline 实测；复算命令见附录 B。

| 规则 | 当前存量 | 说明 |
|---|---|---|
| DS-01 颜色字面量 | **0** | Phase 1b 已把 3 处 UI 层 `ui.RGBA` 换成 `t.ShadowOverlay()`；design/brand 内部实现不计 |
| DS-02 状态色/覆写 | 未纳入 lint | 颜色覆写已基本清零；DS-02 规则 Phase 2 实现 |
| DS-03 裸数值度量 | **51**（grep 行口径；designlint 按字面量计 146 条） | 全部入 `.design-baseline.json`；新代码零容忍 |
| DS-03 裸 FontSize | **0** | Phase 1b 已迁移到 `t.MonoText(role)` |
| DS-03 裸 FontWeight | 90（warn；designlint 口径） | 待 heading type role（DESIGN.md D6）后收敛 |
| DS-06 手绘 ButtonBase | **10** | Phase 2a 的 `uikit.IconButton` 已收编图标按钮；余下 8 处富行/success 态 + 内核 2 处，registry allowlist 已登记 |
| DS-06b 无效豁免 | 0 | 行内 `//design:allow` 必须有 owner/until/reason |
| DS-07 图标来源 | 0 | emoji/手写 SVG 已清零 |
| DS-10 Danger+Default | 0（结构级） | 语义行为由帧测试覆盖 |
| DS-09 重复状态函数 | **0** | Phase 2b 已收敛到 `uikit.Status*`；design-check 阈值收紧为 0 |
| DS-08 文档同步 | 部分 | conformance 覆盖 registry/allowlist/虚构 API；文档表格逐行镜像待 Phase 3 |
| 对话框宽度绕 token | **0** | Phase 1b 已全部换 `DialogWidth*`/`OverlayWidthS` |
| 工具渲染路径 | **4** | 待 Phase 2/3 收编为 ToolRow/ToolCallCard |

**baseline 现状（2026-11-09 刷新后）**：`.design-baseline.json` 共 **156 条**（DS-03 146、DS-06 10），全部带 owner + expires（2027-06-30）；designlint 实测 156 条 violations（全部被 baseline/allowlist 收口），`--strict --baseline` 为 0 new / 0 blocking。design-check 阈值已收紧：metric 51、ButtonBase 10、FontSize 0、status-func 0、Danger+Default 0。
**基线策略**：新代码零容忍；不得为过 lint 向 baseline 加新条目；`make design-baseline` 只在 Lead/owner 批准时用于清理 stale 条目。

## 附录 B：Phase 0 检查（grep 版，已由 `scripts/design-check.sh` 自动化）

脚本 = 以下 6 条 grep + 阈值（超阈值 exit 1）；阈值取 Phase 1a/1b 实测值。手工复算（在 `/data/mygo`）：

```bash
# DS-01 颜色字面量（期望 0）
grep -rnE 'ui\.(Hex|RGBA)\(' internal/ui internal/uiroot --include='*.go' | grep -v _test.go | wc -l
# DS-03 裸 FontSize（期望 0）
grep -rn '\.FontSize(' internal/ui internal/uiroot --include='*.go' | grep -v _test.go | wc -l
# DS-03 裸数值度量（期望 51）
grep -rnE '\.(Height|Width|Size|Gap|Padding|PaddingX|PaddingY|PaddingStart|PaddingEnd|MaxHeight|MinHeight|MinWidth|MaxWidth|Radius|FontSize|Shadow)\(-?[0-9]' internal/ui internal/uiroot --include='*.go' | grep -v _test.go | wc -l
# DS-06 未登记自绘（期望 10，全部 allowlist 登记）
grep -rn 'ui\.ButtonBase(' internal --include='*.go' | grep -v _test.go | wc -l
# DS-10 危险 + 默认组合（期望 0）
grep -rn 'Danger: *true' internal --include='*.go' | grep -v _test.go | wc -l
# DS-09 重复状态函数（期望 0）
grep -rnE '^func (statusLabel|normalizeStatus|statusColor|toolStatusColors|statusTintAlpha|toolStatusBadge|taskStatusLabel|taskStatusColor|subagentStatusLabel)' internal --include='*.go' | grep -v _test | wc -l
```

> 正则版只是过渡；最终以 `designlint` AST 规则为准。

## 附录 C：规则准入 checklist（新增规则前自问）

- [ ] 规则能用一句话说清"禁止什么、为什么、改成什么"。
- [ ] 有至少 2 个正例 + 2 个反例（testdata）。
- [ ] 能给出 `file:line` 与可执行修复建议。
- [ ] 误报边界明确（白名单/豁免机制可用）。
- [ ] 有对应反模式编号（AP-xx）与文档锚点。
- [ ] 先 warn 试运行 ≥1 周，误报 < 5% 再 enforce。
- [ ] 有退役条件（官方 API 出现/架构变化时删除）。

## 附录 D：落地记录（2026-11-09，Phase 1a/1b）

| 项 | 交付 | 证据 |
|---|---|---|
| 规则库 | `internal/design/conform/`（conform/scan/rules/baseline + 测试） | `go test ./internal/design/...` |
| CLI | `internal/devtools/designlint/main.go`（--diff/--json/--rules/--baseline/--strict/--write-baseline） | 全仓 run exit 0；注入 `ui.Hex` 可报 DS-01 |
| registry | `internal/design/registry.json` + `registry.go`（embed、schema 校验） | conformance 测试 |
| baseline | `.design-baseline.json`（235 条，owner/expires） | `--strict --baseline` 0 new |
| 检查脚本 | `scripts/design-check.sh`（6 条 grep + 阈值） | `bash scripts/design-check.sh` 6/6 PASS |
| 接入 | `scripts/verify.sh`、`.github/workflows/ci.yml`、`Makefile`（design-check/design-baseline） | diff 见 PR |
| 框架 vet | `go.mod` tool 指令 | `go tool mygo vet .` exit 0 |
| token | `MonoText`、DialogWidthXS/Compact/XL、OverlayWidthS、IconSizeS/M/L | DESIGN.md §5.4 已同步 |
| 存量修复 | 3 阴影 / 8 宽度 / 7 mono FontSize | grep 清零；快照仅有 terminal 行距预期差异 |
| Phase 2a | `uikit.IconButton` + 12 处迁移；rail hover 修复；10 个帧测试 | ButtonBase 20→10；icon-after 快照；design-check 阈值 20→10 |
| Phase 2b | `uikit.Status*`（Tool/Run/Task 词表）+ 13 个状态函数收敛 + 5 处裸 mono FontSize + F-1 rail 品牌底色 | status-func 13→0；design-check DS-09 阈值 0；status-after 快照；tests 全绿 |
| Phase 2c | `uikit.ToolCallCard` + 4 条工具卡路径收编为 2；F-2 plan cancelled 中性；F-3 active 别名测试 | toolrow-after 快照唯一真实差异 p1-trajectory header；tests 全绿 |
| Phase 3b | `uikit.ApprovalDialog/PlanReviewDialog/AskDialog`；approval/interaction 只留队列；28 个安全语义帧测试 | approval-after 三对话框裁剪 AE=0；tests 全绿 |
| Phase 3a | `uikit.SessionThread/ReasoningTrace`；session 容器逻辑收敛；10 个帧测试 | session-after：narrow/dark session 0px；p1-chat 仅时钟文字 |
| Phase 3c | `uikit.ComposerShell/RunStatus`；两处 composer 统一；13 个帧测试 | composer-after：会话面仅时钟/用量文字；Home 有意统一（高度/内边距/发送图标） |
| Phase 3d | `uikit.TeamSurface` + F-1 主代理徽标统一；rightpanel 只留数据 | team-after：任务板 0px；主代理徽标 tinted 统一；14 个帧测试 |

**已知偏差**：① registry 载体 YAML→JSON（离线依赖限制，schema 不变）；② `--strict` 只升级 warn，不使 baseline 失败；③ DS-06 v1 仅 ButtonBase/DialogBase；④ design-check 阈值取实测并随迁移收紧（FontSize 0、metric 68、ButtonBase 10、status-func 0）。

