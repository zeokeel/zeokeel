# AGENTS.md

> 给 AI coding agent 的项目说明（"README for agents"，格式参考 [agents.md](https://agents.md)）。
> **最近的 AGENTS.md 优先；用户对话中的明确指令优先于本文件。**
> 本文件是**约束**，不是介绍；与它冲突的实现一律拒绝——先改文档，再改代码。
> 通用方法论技能（`.agents/skills/design-guide`、`.agents/skills/mygo-maintenance`）已自动注入；**设计契约的硬约束以本文件与下列文档为准**。

---

## 1. 项目概览

**ZeoKeel**：Go 桌面 AI Agent 客户端，UI 由 `github.com/egoist/mygo v0.8.3` 的**原生控件**绘制（无 HTML/CSS/JS/webview）。当前依赖见 [go.mod](go.mod)。

```
cmd/zeokeel             入口（desktop / headless / acp 产品）
internal/uiroot         窗口壳层：Split/Sidebar/Toolbar/Overlay/palette/设置面
internal/ui/*           14 个 UI 模块（session/toolcards/approval/interaction/rightpanel/settings/...）
internal/uikit          共享组合：页头/小节/空态/错误行/对话框动作条/工具行
internal/design         设计 token（颜色/字号/间距/圆角/阴影/预设）
internal/brand          品牌层：唯一 hex 来源、主题、字体栈
internal/icons          33 个 SVG 图标（16/24 网格不变量测试）
internal/contracts      服务契约（chat/approval/plan/team/task/...）
internal/modules/*      后端能力模块
```

- 模块边界：UI 模块只能 import `internal/contracts`、`internal/uikit`、`internal/design`、`internal/icons`、`internal/lib/*`；禁止 import 其他 UI 模块实现、`internal/uiroot`、`internal/host`（按 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) §3 执行；当前无自动断言，评审人工检查）。
- 依赖方向：`contracts ← modules/ui ← uiroot`；帧循环/窗口/`*ui.Context` 只归 `uiroot`，模块只贡献 builder（`func(c *ui.Context)`）。

---

## 2. 必读顺序（写 UI 前）

1. 本文件（常驻 10 条 + 工作流）。
2. [docs/DESIGN.md](docs/DESIGN.md)：组件目录 / Agent 场景映射 / 反模式 / 最小示例 / token / 入口组件 / 自绘白名单。**这是唯一契约入口。**
3. [docs/AGENT-SURFACES.md](docs/AGENT-SURFACES.md)：要改会话/工具/推理/审批/计划/提问/composer/运行态/团队时，按对应 surface 规格做。
4. [docs/DESIGN-LINT.md](docs/DESIGN-LINT.md)：交付前要过的确定性检查、规则 ID、baseline。
5. [docs/UI-CONFORMANCE.md](docs/UI-CONFORMANCE.md)：官方组件速查与写 UI 固定流程（API 事实）。
6. [docs/UI-COMPONENT-MIGRATION.md](docs/UI-COMPONENT-MIGRATION.md)：自绘保留清单与理由（偏离前先看）。
7. 改壳层/导航/注入点时读 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) 与 [docs/INJECTION-POINTS.md](docs/INJECTION-POINTS.md)。

> [docs/DESIGN-REVIEW.md](docs/DESIGN-REVIEW.md) 是 2026-11-08 / mygo v0.7.4 的**历史审计报告**，只作证据，不作现状依据。

---

## 3. 常驻设计规则（与 DESIGN.md 一致，违反即拒绝）

1. **先查场景表，再写 UI**（DESIGN.md §2）：Agent 表面只能从入口组件进入；有入口就不许绕开。
2. **颜色只用 token**：`design.Of(c)` / `c.Theme()`；**禁止 hex / `ui.RGBA` / 颜色字面量**（品牌层除外）。
3. **字号/间距/圆角/尺寸只用 design token**：`t.Font(design.Type*)`、`design.Space*`、`design.Radius*`、`design.DialogWidth*`；禁止裸数值度量。
4. **官方件只设度量、不覆写状态色**：覆写背景会关掉 mygo 原生 hover/pressed/disabled（`t.PrimaryButton()` 只加度量）。
5. **默认即正确**：最短调用；`editor.State`/`markdown.Options` 零值即默认；加 options/覆写必须有理由。
6. **有官方标准件就不许手搓**：集合用 `ui.List/Table/Outline/GridView`（禁止 `Scroll`+循环）；浮层用 `Modal/Overlay/Popover/AlertDialog`；菜单/搜索/表单/输入同理。
7. **状态语义不可降级**：error 用 `semantic.error`/`*Text` 变体；审批动词不中性化；`cancelled` 中性；`active` ≠ running。
8. **图标只从 `internal/icons` + `ui.Icon` 取**：禁 emoji/Unicode/手写 SVG；图标按钮必须 `Label`（Tooltip 按需，与 Esc 冲突时可不加并注释）；纯图标按钮统一用 `uikit.IconButton`，禁止新增 `ButtonBase` 图标按钮。
9. **新增自绘/组件必须登记**：DESIGN.md §7 + MIGRATION + lint 白名单 + 测试，否则检查失败。
10. **改完 UI 必须验证**：`go test`（帧测试）+ 快照 + `designlint`/`scripts/design-check.sh`；没有证据不算完成。

---

## 4. 命令（离线环境已配好）

```bash
export PATH=/usr/local/go/bin:$PATH
# 本工作区沙箱:模块缓存可写在 .cache/gopath（普通环境可省略）
export GOPATH=/data/mygo/.cache/gopath
export GOCACHE=/data/mygo/.cache/go-build
# 测试临时目录必须在仓库外：workspace 测试会把仓库内的临时目录当成 git 仓库
export GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp
mkdir -p /tmp/zeokeel-tmp
export GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off

gofmt -l internal cmd                     # 期望空
go build ./...                            # CI 在 CGO_ENABLED=0 下构建（mygo：无 cgo）
go vet ./...
go test ./... -count=1                    # 全量（慢）
go test ./internal/ui/... ./internal/uiroot ./internal/design ./internal/uikit -count=1
go run ./internal/devtools/designlint --strict --baseline .design-baseline.json ./...
bash scripts/design-check.sh              # Phase 0 grep + 阈值
go tool mygo vet .                        # 框架生命周期（go.mod tool 指令）
make design-check                         # 串联 designlint + grep + mygo vet
go run ./internal/devtools/ui-snapshot .cache/ui-conformance   # 生成 PNG、人工看
scripts/verify.sh --quick                 # 快速内环（fmt/build/vet/design/test）
scripts/verify.sh                         # 全量（含 -race/regression/snapshot/smokes）
make ci                                   # build+vet+test；全量用 make verify
```

- **baseline 纪律**：默认禁止 `make design-baseline`（会把新违规 grandfather 进存量）；只有 Lead/设计 owner 明确批准、用于清理 stale 条目时才可刷新，并在汇报里说明。
- `TMPDIR`/`GOTMPDIR` 必须放在**仓库外**（本环境用 `/tmp/zeokeel-tmp`，沙箱可写）；放在工作区内会让 `internal/modules/workspace` 的临时目录被当成 git 仓库（TestChangesErrors 失败）。`/data/zeokeel-tmp` 在本沙箱只读，不要用。
- 帧测试：`ui.NewTester`（`Click/Key/Type/Find/Focused/AccessByRole`）；每个交互改动必须补帧测试。
- 快照/驱动：`internal/devtools/ui-snapshot`（PNG）、`internal/devtools/ui-drive`（交互步骤）。视觉改动必须人工看 PNG。

---

## 5. UI 改动固定流程

1. **查**：DESIGN.md §2 场景表 → 官方组件速查（UI-CONFORMANCE §1）→ 本项目是否有入口/预设。
2. **读**：用 `go list -m -f '{{.Dir}}' github.com/egoist/mygo` 定位依赖源码，读 `docs/ui/<component>.md` 与实现（文档与源码不一致时以源码为准）。
3. **实现**：在 view 回调内 build；状态放模块实例；`c.Key` 稳定身份；不在 build 里做 I/O；跨线程走 `UIRuntime.Post`。
4. **测**：帧测试覆盖交互（键盘/焦点/Esc/Default/状态切换）；集合虚拟化/流式跟随用现有测试作回归。
5. **看**：`ui-snapshot` 生成 PNG，人工检查亮/暗、窄窗、空态、错误态。
6. **检**：`designlint --strict --baseline .design-baseline.json` + `scripts/design-check.sh` + `go vet` + `go tool mygo vet`；新违规必须修，不得加 baseline。
7. **同步**：更新 DESIGN.md / registry / 入口规格 / UI-CONFORMANCE 的对应表格（见 §6）。

---

## 6. 文档同步义务（与代码同 PR）

| 改动 | 必须同步 |
|---|---|
| 新增组件/预设/入口 | DESIGN.md §1/§6 + 附录 A registry + AGENT-SURFACES（若是 surface） |
| 新增 Agent 场景 | DESIGN.md §2 + registry scenarios + 对应入口迁移行 |
| 新增自绘/偏离官方 | DESIGN.md §7.1 + UI-COMPONENT-MIGRATION §3 + lint 白名单（owner/到期/理由） |
| 新增反模式/规则 | DESIGN.md §3 + DESIGN-LINT.md 规则 ID + 检查实现（或注明只能人工评审） |
| 官方 API 变化（升级 mygo） | UI-CONFORMANCE §1 + DESIGN.md 附录 A `source.version` + §7.4 缺口 |

**已知漂移（不要照抄）**：`docs/UI-FIX-STATUS-v0.8.3.md` §1 的「Approval Deny 默认焦点/Enter=拒绝」已过时（现行审批无默认动作）；transcript 也早已是 `ui.List` 虚拟化，不是 200 条有界窗口。以代码 + 测试 + DESIGN.md 为准。

---

## 7. 硬边界（违反 = 拒绝合并）

- **UI 生命周期**：不把 `ui.Element`/parts 存进 struct/全局/goroutine；不跨 build pass 用旧元素；`c.Key` 给循环内 stateful 控件稳定身份；容器 key 在 children 之前。
- **线程**：UI 构建/绘制/输入/回调都在主线程；`*ui.Context` 不出 build；后台结果回 UI 用 `UIRuntime.Post`；`Invalidate` 只请求重绘。
- **模块边界**：不 import 其他模块实现/uiroot/host；跨模块用 contracts 服务或稳定命令 ID（如 `rightpanel.team.open`）。
- **不自造标准件**：官方有 `List/Modal/AlertDialog/Overlay/Popover/Menu/SearchField/Form/Field/Switch/Checkbox/Radio/Select/Tabs/Collapsible/Toast/Scroll/Split/Sidebar/Table/Outline/GridView/editor/markdown` 等，必须用。
- **不虚构 API**：v0.8.3 没有 `ui.DialogFooter`、`Button.Default`、`ui.Markdown`、`ui.DatePicker`、`ui.CommandPalette`（见 DESIGN.md §7.4）。
- **不加依赖**：新增第三方 UI/图标/样式库前必须说明理由并获得确认；不引入第二套设计系统。
- **不做未请求的功能**：不加导航/统计/装饰性面板来"让界面看起来完整"。

---

## 8. 汇报与验证要求

- 汇报必须给**证据**：命令与结果、测试名、PNG 路径（`.cache/...`）、失败与限制说明。
- 没跑验证不说"完成"；只改了文档则明确写"未改代码"。
- 交付前自检：gofmt、build、vet、test（相关包至少）、帧测试、快照；跑 `designlint --strict --baseline`、`scripts/design-check.sh`、`go tool mygo vet`。
- 遇到规则与本文件冲突：先按本文件做，并在回复里指出冲突点；不要静默偏离。

---

## 9. 常用速查

| 需求 | 去哪 |
|---|---|
| 官方组件是否存在/怎么用 | mygo 源码内 `docs/ui/<name>.md`（v0.8.3，用 `go list -m -f '{{.Dir}}' github.com/egoist/mygo` 定位）+ [docs/UI-CONFORMANCE.md](docs/UI-CONFORMANCE.md) §1 |
| 某场景该用哪个组件 | [docs/DESIGN.md](docs/DESIGN.md) §2 |
| 写错了会怎样 | [docs/DESIGN.md](docs/DESIGN.md) §3 反模式 + [docs/DESIGN-LINT.md](docs/DESIGN-LINT.md) §2 |
| token 取值 | `internal/design/tokens.go` / `components.go` |
| 共享组合 | `internal/uikit/*.go` |
| 图标 | `internal/icons/svg/*.svg` + `ui.Icon(c, icons.X())` |
| 现有测试保护网 | `internal/ui/*_test.go`、`internal/uiroot/conformance_test.go`、`internal/regression/` |
| 设计系统决策 | [docs/DESIGN.md](docs/DESIGN.md) 附录 D |
