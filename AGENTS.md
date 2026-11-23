# AGENTS.md

> 给 AI coding agent 的项目说明（"README for agents"，格式参考 [agents.md](https://agents.md)）。
> **最近的 AGENTS.md 优先；用户对话中的明确指令优先于本文件。**
> 本文件是**约束**，不是介绍；与它冲突的实现一律拒绝——先改文档，再改代码。
> 通用方法论技能（`.agents/skills/design-guide`、`.agents/skills/mygo-maintenance`）已自动注入；**设计契约的硬约束以本文件与下列文档为准**。

---

## 0. 架构换轨进行中（2026-11-23 起）

目标形态：**网关进程（gateway）+ 浏览器 Web 客户端（主体）+ 极小原生托盘壳**。本文件已按新形态改写，但**迁移尚未完成**——请按两条轨分开读：

| 轨 | 位置 | 适用规则 |
|---|---|---|
| **Web 轨**（新代码默认） | `web/`（React + TypeScript + Vite） | §3.1 + DESIGN.md |
| **托盘壳轨** | `internal/host/` 的托盘/热键/通知（约 1.5k 行原生 mygo） | §3 的 mygo 规则全量适用 |

- 决策记录：[docs/plans/web-client-pivot.md](docs/plans/web-client-pivot.md)
- 协议：[docs/TRANSPORT.md](docs/TRANSPORT.md)　客户端：[docs/WEB-CLIENT.md](docs/WEB-CLIENT.md)
- **冻结区**：`internal/ui`、`internal/uiroot`、`internal/uikit`（54,425 行 + 83,050 行测试）——只允许 P0 修复，**禁止新功能**。原生桌面产品保留为回退路径，直到 Stage 3 退役。
- 换轨依据：`.cache/pivot/contracts-projection.md`（契约投影盘点）、`.cache/pivot/native-ui-inventory.md`（原生 UI 清点）。

---

## 1. 项目概览

**ZeoKeel**：Go 编码的常驻 AI Agent。**网关进程**承载后端能力，**浏览器**是主客户端，**原生托盘壳**（mygo）负责常驻图标、全局快捷键与系统通知。前端 **React + TypeScript + Vite + Ant Design 6（antd）+ Ant Design X 2.0**；视觉由 antd seed token 从 `internal/design` + `internal/brand` 派生（`web/src/design/antd-theme.ts` 为生成物，手改即违规）。依赖见 [go.mod](go.mod) 与 [web/package.json](web/package.json)。框架决策与 token 映射见 [docs/plans/web-ui-framework.md](docs/plans/web-ui-framework.md)。

```
cmd/zeokeel             入口（desktop / serve / headless / acp 产品）
internal/gateway        【新】serve 产品：WS 控制面（快照+差值）、端口协商、token/Origin 校验
web/                    【新】浏览器客户端：路由 / /inbox /c/:id /settings
internal/host           进程宿主：窗口/托盘/单实例/设置存储/日志/退出
internal/contracts      服务契约（chat/approval/plan/team/task/...）
internal/modules/*      后端能力模块（27 个，迁移中 100% 保留）
internal/design         设计 token（→ 生成 web/src/design/tokens.css）
internal/brand          品牌层：唯一 hex 来源、主题、字体栈
internal/icons          34 个 SVG 图标（web 侧直接复用）
── 冻结区（Stage 3 退役）──
internal/uiroot         旧窗口壳层
internal/ui/*           旧 UI 模块（session/toolcards/approval/rightpanel/settings/...）
internal/uikit          旧共享组合
```

- **Go 侧模块边界（不变）**：旧 UI 模块只能 import `internal/contracts`、`internal/uikit`、`internal/design`、`internal/icons`、`internal/brand`（资产层 logo）、`internal/lib/*` 与第三方；禁止 import 其他模块实现、`internal/uiroot`、`internal/host`。后端模块可 import `contracts/uikit/design/lib/*` 与共享库 `internal/{hosttools,memorytools,websearch}`；`host` 不得直连 `internal/modules/*`（[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) §3；**CI 机器断言** `TestImportBoundaries`）。
- **Web 侧模块边界（新）**：`web/src` 只能经 `web/src/api/`（协议生成）访问网关，**禁止**手写 `fetch`/`WebSocket`；`web/src/ui/`（设计系统）不得 import `web/src/features/`；`web/src/design/tokens.css` 是唯一颜色/字号/间距来源。
- **依赖方向（新）**：`contracts ← modules ← gateway ← WS ← web`；托盘壳只读网关状态（`gateway.json` + 只读状态服务），不参与会话逻辑。

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
7. **状态语义不可降级**：error 用 `semantic.error`/`*Text` 变体；审批动词不中性化；`cancelled` 中性；`active` ≠ running；**running ≠ warning**（进行中 = `Accent` spinner/点 + `LabelSecondary` 词；warning 只给 needs-action：等待审批/授权失效）。
8. **图标只从 `internal/icons` + `ui.Icon` 取**：禁 emoji/Unicode/手写 SVG；图标按钮必须 `Label`（Tooltip 按需，与 Esc 冲突时可不加并注释）；纯图标按钮统一用 `uikit.IconButton`，禁止新增 `ButtonBase` 图标按钮。
9. **危险确认安全默认**：删除/清空/恢复类确认必须用 `uikit.ConfirmDanger`（`ui.Modal` + `DialogFooter`，取消为 Default、危险动作非 Default 且为 danger 按钮）；`ui.AlertDialog` 只用于**非危险、非不可逆**的简单确认。审批无 Default、Esc=拒绝，且 Esc 必须在窗口级生效（不依赖焦点位置）。
10. **强调色预算**：一屏 ≤2 个 Accent 填充块、≤1 个 primary 按钮；状态只能用 `uikit.StatusBadge`（语义 tint + AA 文字 + 必填标签），禁止中性底 + 状态字拼徽章；一屏红色元素 ≤2。
11. **选择态两级**：持久导航/树/列表当前项 = `BgSelected`（亮 9% royal / 暗 13% cyan） + 2px `Accent` 左条 + `LabelPrimary`（2px 左条要求仅针对**自绘富行**；官方 `Sidebar`/`Tabs` 的原生 tint 选中视为合规，不强加左条）；浮层内的瞬态选择（命令面板/菜单/下拉/slash/@ 建议）可用 Accent 填充 + `OnPrimary`，也可用 `BgSelected` tint（两者均为瞬态许可词汇）。禁止满蓝导航选中行。
12. **字号纪律**：`TypeXXXS`(11) 只用于 kbd/终端 gutter/分组微标签，**禁止成句文本与元数据句**；元数据最低 `TypeXXS`(12)，正文 `TypeBody`(14)；顶级屏标题 `TypeM/600`（顶带），设置页/对话框 `TypeL/600`。字重已烘焙进 role（XL/L/M=600、S=500、XXXS=500），call site 不再传字面量字重；需要强调时只能传 `design.Weight*` token（如侧栏行标题 `TypeXS` + `WeightMedium`）。
13. **度量档位**：控件高度只用 28/32/36/40/44（`design.ControlCompactHeight/RowCompactHeight/ControlHeight/PageActionHeight/RowDoubleHeight`），禁止 38；圆角只用 `RadiusXS 4/SM 6/MD 8/LG 12/XL 16/Max`（XL 仅 composer，Max 只给头像）；表面遵循 S0–S4 ramp（暗色导航必须比页面深）。
14. **页面主操作两落点**：页面级即时动作放页头右，需要提交的表单放固定页脚右下的唯一 primary；分节动作放 `SectionAction`；禁止页中悬浮 primary。
15. **新增自绘/组件必须登记**：DESIGN.md §7 + MIGRATION + lint 白名单 + 测试，否则检查失败。
16. **改完 UI 必须验证**：`go test`（帧测试）+ 快照 + `designlint`/`scripts/design-check.sh`；没有证据不算完成。
17. **关键功能入口与状态信息不因“减法”隐藏**：团队、子代理等 orchestration 入口零值也常显（保证可发现）；状态条/消息用量等信息默认保留，删除信息项必须有用户明确同意。

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
  以上两条只对**托盘壳与冻结区**适用；改 Web 代码时跑它们不算验证。

**Web 轨命令（`web/`，新增）**

```bash
cd web
npm ci                        # 锁定依赖（package-lock.json 必须提交）
npm run typecheck             # tsc --noEmit
npm run lint                  # eslint
npm run test                  # vitest
npm run e2e                   # playwright（对应 WEB-CLIENT.md 的验收清单）
npm run build                 # 产物由 go:embed 打进二进制
npm run dev                   # dev server，代理到网关
```

- **antd 组件知识必须查、不许凭记忆写**（v6 与训练数据有差异）。离线 CLI（内置 v4/v5/v6 全量元数据）：

  ```bash
  bash scripts/antd-cli.sh --install                              # 首次；装到 .cache/antd-cli
  bash scripts/antd-cli.sh info Button --version 6 --format json   # props
  bash scripts/antd-cli.sh demo Select basic --format json         # 可运行示例
  bash scripts/antd-cli.sh token Table --version 6 --format json   # 组件 token
  bash scripts/antd-cli.sh semantic Modal --version 6 --format json
  bash scripts/antd-cli.sh lint ./web/src --format json            # 废弃 API / a11y / 性能
  ```

  包装脚本把 `HOME`/`XDG_*` 重定向到 `.cache/antd-cli/`；**不要**直接调 `antd` 或 `npm i -g`（本沙箱 `~/.config`、`~/.npm` 只读）。CLI 不覆盖 `@ant-design/x`——X 的确切 props 读 `web/node_modules/@ant-design/x/es/**/*.d.ts`。
- 两个 skill：`antd`（antd v6）与 `antd-x`（`@ant-design/x` 2.10）——见 [.agents/skills/README.txt](.agents/skills/README.txt)。

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
- **模块边界**：不 import 其他模块实现/uiroot/host；跨模块用 contracts 服务或稳定命令 ID（如 `rightpanel.files.open`）。
- **不自造标准件**：官方有 `List/Modal/AlertDialog/Overlay/Popover/Menu/SearchField/Form/Field/Switch/Checkbox/Radio/Select/Tabs/Collapsible/Toast/Scroll/Split/Sidebar/Table/Outline/GridView/editor/markdown` 等，必须用。
- **不虚构 API**：v0.9.2 没有 `ui.DialogFooter`、`Button.Default`、`ui.Markdown`、`ui.DatePicker`、`ui.CommandPalette`（见 DESIGN.md §7.4）。
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
| 官方组件是否存在/怎么用 | mygo 源码内 `docs/ui/<name>.md`（v0.9.2，用 `go list -m -f '{{.Dir}}' github.com/egoist/mygo` 定位）+ [docs/UI-CONFORMANCE.md](docs/UI-CONFORMANCE.md) §1 |
| 某场景该用哪个组件 | [docs/DESIGN.md](docs/DESIGN.md) §2 |
| 写错了会怎样 | [docs/DESIGN.md](docs/DESIGN.md) §3 反模式 + [docs/DESIGN-LINT.md](docs/DESIGN-LINT.md) §2 |
| token 取值 | `internal/design/tokens.go` / `components.go` |
| 共享组合 | `internal/uikit/*.go` |
| 图标 | `internal/icons/svg/*.svg` + `ui.Icon(c, icons.X())` |
| 现有测试保护网 | `internal/ui/*_test.go`、`internal/uiroot/conformance_test.go`、`internal/regression/` |
| 设计系统决策 | [docs/DESIGN.md](docs/DESIGN.md) 附录 D |
