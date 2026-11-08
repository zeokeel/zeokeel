# ZeoKeel 设计修复状态报告（Round 1 → Round 2）

> 对应审计：[docs/DESIGN-REVIEW.md](DESIGN-REVIEW.md)（`design-guide` + `mygo-maintenance`）。
> 日期：2026-11-08 · HEAD `e977b12` + **未提交**修复工作区（179 个受跟踪文件，+8107 / −4718）
> 最终冻结：产品代码 13:42（复审 N1/N2 小修：设置重开滚动选中、快照深链接校验），截图证据 13:43（78 PNG + manifest，0 panic）。
> 结论：**Round-1 的 1 个 P0 与系统性 P1 全部关闭；Round-2 独立复审无新 P0/P1、无回归。** 全量测试、构建、vet、gofmt、交互驱动与截图证据均通过。遗留 P2/P3 见 §6。

---

## 1. 结论摘要

| 维度 | 修复前（Round 1） | 修复后（终态） |
|---|---|---|
| P0 | 设置弹窗固定 760×640 且内容无滚动 → 长页主操作画到弹窗外 | **Fixed**：自适应窗口 + 内容滚动 + 固定关闭 X；另修掉 modal padding 少算 32px 的边界溢出 |
| 系统性 P1 | 设计系统共享层失效 / 主按钮不主 / 15 处 Toggle 当开关 / 设置 IA 平铺 / 命令面板与 slash/@ 无键盘 / 响应式失控 / toast 重实现 / 浮层顺序反惯例 | **全部 Fixed**（逐项见 §3） |
| `ui.Toggle` 当开关 | 15 | **0**（`ui.Switch` 6 + `ui.Field` 标签行；设置/automation/plugins 全部原生开关） |
| 浮层动作顺序 | 6/10 手写 footer 反惯例 | **0**：`uikit.DialogFooter` 34 处统一「危险/次要在左、主操作 accent 最右」 |
| 危险确认 | 5 处 AlertDialog，多处无确认 | **17** 处原生 `ui.AlertDialog`，覆盖删除/清空/恢复默认/放弃目标等 |
| 稳定控件身份 | `c.Key` 全仓 2 处 | **38** 处（`c.Key` + `ui.List.ItemKey`） |
| 裸字号 | 47 处 | **7 处**（余为 mono 内联字号/动态正文，P3 backlog） |
| 全量测试 | — | `go test ./... -count=1` **全绿**；`go build ./...`、`go vet ./...`、`gofmt -l` 干净 |
| 真实交互驱动 | 同工具在未修改 HEAD 上 **44 PASS / 22 FAIL** | **66 PASS / 0 FAIL / 0 WARN**（`internal/devtools/ui-drive`，66 步） |
| 截图证据 | 12:05 旧 UI、右栏 pane 截图重复、无窄窗/暗色 | 13:33 冻结后 **78 张**（71 全屏 + 7 窄窗/暗色 + manifest，含 pane 正确切换） |

冻结后 4 位独立复审员的最终结论：shell「无新 P0/P1、无回归，可冻结交付」、conformance「PASS（有条件通过），R2-01 证据链全部恢复」、session「Round-1 Fixed 34/Partial 13/Open 3，无 P0/P1，Round-2 批次全部 Fixed」、settings「Round-1 P0/P1 主线全部 Fixed；新增 3 条 P2（重开设置导航滚动、快照证据可信度、固定 footer）中前两条已随后修复，第 3 条进 backlog」（详见 §4/§5）。
Round-2 独立复审（4 个只读复审 + feature driver）对 Round-1 的 218 条原始 finding 复核结果：
settings 面 全部 P0/P1 Fixed；shell 面 **Fixed 26 / Partial 21 / Open 9 / Regressed 0**；
session 面 **Fixed 33 / Partial 14 / Open 3 / Regressed 0**；conformance 面 **Fixed 31 / Partial 16 / Open 6 / Regressed 0**。
所有 Round-2 新发现（toast 遮挡、skills 确认、日期本地化、快捷键漂移、pressed/BgSelected、SuccessText 等）
已在 Round-2 修复并再次复核；**无新 P0/P1、无回归**。

---

## 2. 修复方式（多代理、多轮）

1. **Lead 基础层**：`internal/design` 按钮体系（Primary=accent、Secondary、Danger、BgSelected、SuccessText、布局常量）、
   `internal/uikit` token 化 + `DialogFooter/ErrorLine/EmptyStateBlock/PageHeader/Section` + 日期本地化 helper、
   `internal/icons` 补齐（copy/close/check/send/stop/trash/file/image/panel-right 等）、
   `internal/uiroot` 壳层（设置自适应+滚动+关闭+分组导航+深链接+选中滚动、命令面板原生 SearchField+List 键盘、
   侧栏 footer dock、rail 图标/本地化/折叠、右栏窄窗 drawer、宿主命令本地化、设置/搜索命中 panic 修复）。
2. **三个并行 fixer（按写作用域隔离，未提交冲突）**
   - settings-fix：19 个设置页（Switch/Form/Field、uikit.Section 取代卡片墙、DialogFooter、危险 AlertDialog、
     跨页状态同步、保存语义、IA/Order、about 去页头元数据）。
   - session-fix：会话头目标 chip/menu、composer `ui.Toolbar`、斜杠/@ 键盘、滚动跟随、消息动作可发现入口、
     紧凑重新生成、Memory 起始位置、浮层 Enter/Esc/DialogFooter、toast 取代常驻 badge、c.Key/I/O 缓存。
   - shell-fix：侧栏工作区行/footer NavRow/会话行 More、首页空态与英文、右栏 pane 选择器+关闭 X+行分隔、
     automation Switch/Field/历史 Collapsible、plugins `ui.List` 虚拟化、原生 toast 品牌化 viewport。
3. **验证工作流**：`internal/devtools/ui-drive`（66 步真实交互驱动，含 UI 操作、浮层、键盘、窄窗、暗色、toast）
   + `internal/ui/uimodtest/interaction_e2e_test.go`（6 组 E2E）+ 4 个独立只读复审（对照 Round-1、不采信修复者自评）。
4. **Round-2 独立复审冻结后**：所有 fixer 停止写入，重跑全量测试 + 快照（manifest 记录 HEAD/变更数/时间），
   复审员以冻结树做最终复核；Lead 修复复审发现的真实回归（设置面 32px 溢出、palette 去重后 E2E 断言、
   sidebar 搜索命中重复 key 导致的 contained panic）。

---

## 3. P0/P1 关闭清单

| Round-1 问题 | 状态 | 修复落点与证据 |
|---|---|---|
| P0 设置壳无滚动/固定 760×640，主操作溢出 | **Fixed** | `uiroot/frame.go` settings：`ui.Scroll` 内容区、尺寸随窗口（并扣除 modal Space(10) 外边距 + Space(5) padding）、右上关闭 X、固定标题行；帧测试断言「底部操作在面板内」；截图 `final5/p2-appearance.png`、`final5/p2-about-settings.png` |
| P1 共享层失效（uikit 裸字号、design 死代码、4 套页头/空态/错误行） | **Fixed（主要）** | uikit 基于 design token；`PageHeader/Section/ErrorLine/EmptyStateBlock/DialogFooter` 统一；删除 design 无用样式与 motion.go、uikit 死 helper；页头统一 `uikit.PageHeader` |
| P1 主按钮不主 / 5 套按钮外观 | **Fixed** | `design.PrimaryButton` 恢复 native accent（保留 hover/pressed）；新增 `SecondaryButton`/`DangerButton`；侧栏「新对话」/会话「发送」/设置「保存」统一 accent；删除 3 份 `secondaryButtonStyle` |
| P1 15 处 `ui.Toggle` 当开关 | **Fixed** | settings/automation/plugins/sidebar 全部改 `ui.Switch`+`ui.Field` 可见标签（`ui.Toggle` 0 处），通知过滤改 `ui.Checkbox` |
| P1 设置 IA：19 项平铺、Section 不渲染、默认「关于」 | **Fixed** | 按 Section 渲染分组导航（通用→模型与对话→外观与技能→键盘→高级→关于），修重复 Order，默认页=行为，About 单页不重复分组头；深链接切页并滚动选中项（`TestSettingsNavGroups`） |
| P1 命令面板假「Enter 选择」 | **Fixed** | 原生 `ui.SearchField` + `ui.List`（↑↓/Enter/Tab/Esc、单点运行、去重同 Title 命令），并新增键盘帧测试 |
| P1 斜杠/@ 菜单无键盘、Enter 误发送 | **Fixed** | ↑↓ 高亮、Enter/Tab 执行、Esc 关闭且保留草稿；**Enter 只在高亮项存在时执行**，无匹配时恢复正常发送（Round-2 N1 回归修复）；帧测试覆盖 |
| P1 流式回复不跟随滚动 | **Fixed** | chat/trajectory/memory 各自 `ScrollState.TrackScroll`；仅 chat 贴底跟随，上滑出现「回到最新」，切页签/会话恢复位置；Memory 不再初始滚到底 |
| P1 窄窗发送按钮/标题被裁 | **Fixed** | composer 发送/停止固定在右组，任何宽度可见（Round 4 后动作行最多 4 个控件、窄窗折叠进会话设置菜单，不再依赖 `»` 溢出）；1024/900 帧测试断言发送 rect 在窗口内且可点；右栏窄窗改 drawer |
| P1 会话头目标 4 按钮挤占标题 | **Fixed** | 目标文本回到可见 chip（截断+Tooltip）、动作收进 popover 菜单、危险操作 AlertDialog；标题优先级与窄窗测试 |
| P1 浮层按钮顺序反惯例 / 主按钮失权 | **Fixed** | `uikit.DialogFooter` 统一 34 处；approval/plan/ask/goal/memory/feedback/help/present 全部「危险 左…主操作 accent 右」，Enter=主操作，plan Esc 不取消计划 |
| P1 toast 重实现（无 a11y/暂停/Esc） | **Fixed** | 模块只做队列+过滤；`c.AddToast` + `ui.ToastViewportBase` 品牌化 viewport（右下、让出 composer、级别图标+关闭 X），计时/悬停暂停/Esc/读屏交还框架 |
| P1 侧栏「工作区」竖排/英文截断 | **Fixed** | 工作区行拆两行（标签+视图菜单+添加图标；工作区名 muted 行），全部 NoWrap；英文实测不截断 |
| P1 右栏 8 页签在 320px 溢出 | **Fixed** | 改为 `PopoverMenuButton` pane 选择器（8 项 Checked，当前项名显示）+ 停靠态关闭 X；pane 内改行+Divider |
| P1 设置跨页状态不同步 | **Fixed** | model 页草稿跟随 store 快照同步（输入焦点保护），页尾「当前模型」读生效值；新增 3 个回归测试 |
| P1 渲染路径同步 I/O（session） | **Partial（改善）** | 记忆/mention 缓存、附件缩略图 staging 时解码；查询变更帧仍同步调用服务（余 P3，见 §6） |

## 4. Round-2 新发现与修复

- **设置面 32px 溢出（复审 P1）**：设置 Row 高度未扣 modal 自身内边距 → 长页动作/导航蓝条伸出面板。已修（`frame.go`）并加 bounds 帧测试。
- **toast 遮挡 composer（P2）**：原生 toast 默认底部居中遮挡输入区。已改品牌化 `ToastViewportBase`：右下角、底部预留 220/236 DIP，1280/1024 实测不遮挡，保留级别图标/关闭/暂停/Esc/读屏。
- **skills 删除确认（P2）**：自绘 Modal + accent 确认 → 原生 `ui.AlertDialog`（取消/删除，Esc 取消，焦点归还）。
- **日期本地化（P2）**：`uikit.FormatDateTime/FormatClock` 统一 session/settings/rightpanel/automation/sidebar；zh 紧凑 / en 可读；外观页承诺成立。
- **快捷键漂移（P2）**：/help 与设置页快捷键表补 Cmd+N、Cmd+,、Cmd+Alt+I，Cmd+B/Cmd+K 读 settings 实时值；宿主命令随语言重注册；palette 按 Title 去重（「设置/打开设置」合并）。
- **pressed/选中态（P3）**：自绘按钮行补 pressed；hover 与 selected 拆分 `BgHover`/`BgSelected`（侧栏工作区行等）。
- **SuccessText/badge（P3）**：成功状态文本统一 WCAG-AA 的 `SuccessText`；元数据 badge 改 muted 文本。
- **N 系列（session 复审）**：斜杠 Enter 语义恢复、消息动作常驻「⋯」入口、重新生成紧凑、Memory 初始置顶、成功 tint 8% 保持 AA。
- **真实 bug（冻结前快照发现）**：侧栏消息搜索命中重复内容时 `c.Key` 冲突 → contained panic + 日志。已修（搜索结果去重 + 行序号 key），最终快照 0 panic；并让 `safeBuild` 日志携带 panic 值与栈，后续可定位。
- **时间显示 UTC（session 复审 P2）**：`uikit.FormatDateTime/FormatClock` 统一转本地时区（记忆行/导出/检查器日志此前显示 UTC）；zh/en 格式保持。
- **斜杠菜单重复「设置」行（session 复审 P3）**：`matchingCommands` 与命令面板一样按 Title 去重（home 与 session 同义命令只显示一条）。
- **设置关闭后重开：停在原页但导航不回滚（settings 复审 N1/P2）**：`navigator.ToggleSettings` 打开时置 `settingsScrollToSelected`，重开自动把当前页导航项滚入视口。
- **快照证据可信度（settings 复审 N2/P2）**：`ui-snapshot` 的 `clickSettingsItem` 改为走宿主深链接 `OpenSettingsPage(id)` 并校验页面真正渲染（同时验证深链接路径），确保 `p2-data-privacy` 等截图内容与页名一致。
- **图标/死代码**：新增图标统一 stroke（16 视口等效 1.2），删除 9 个死图标 accessor；删除未使用 design 样式/motion/uikit helper，修正三套 KV/detail 行重复。

## 5. 验证证据（可复验）

```bash
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod GOCACHE=/data/mygo/.cache/go-build
go build ./...            # exit 0
go vet ./...              # exit 0
gofmt -l internal cmd     # 空
go test ./... -count=1    # 全部 ok（含 internal/regression、internal/ui/uimodtest）

# 真实交互驱动（66 步：导航/设置 19 页/开关/命令面板/slash/@/composer 菜单/流式停止/
# approval/ask/present/plan/goal/右栏 8 pane/终端/任务/主题/语言/折叠/1024/900/toast）
go run ./internal/devtools/ui-drive --out .cache/fix-drive
# → .cache/fix-drive/drive-report.md：66 PASS / 0 FAIL / 0 WARN
# → 对未修改 HEAD 的同一驱动：.cache/fix-drive-baseline-head/ → 44 PASS / 22 FAIL

# 最终截图（pane 真实切换、narrow/dark、manifest）
mkdir -p .cache/final8 && go run ./internal/devtools/ui-snapshot .cache/final8
# → 78 PNG + .cache/final8/manifest.txt（HEAD/变更数/时间戳/文件清单）
```

- 全量测试最终日志：`.cache/final-checks.log`（date/gofmt/vet/test/diffstat）。
- 独立复审：`.cache/audit2-{settings,shell,session,conformance}/report.md`、`.cache/audit2-shell-final/brief.md`（冻结后复核）。
- 互证：`.cache/fix-drive/{final-summary.md,e2e-report.md,results.json}`、四个 fixer 各自快照目录（`.cache/fix-*/`）。
- 限制：本环境无 `go tool mygo vet`（`go: no such tool "mygo"`），以 `go vet` + 测试 + 人工扫描替代；
  无真机/键盘焦点环截图（headless 帧测试覆盖焦点与键盘路径）。

## 6. 遗留 backlog（非阻塞，Round-2 复审判定 P2/P3）

| 项 | 说明 | 建议 |
|---|---|---|
| Files/Log 每帧 I/O（P2） | rightpanel 文件/日志 pane 在 build 内读服务 | 改为缓存+按需刷新/POST 结果 |
| transcript 未虚拟化（P2） | 长会话每帧重建全部消息；已记录取舍理由（流式 Markdown 状态/测高/滚动模型风险） | 后续 `ui.List` 迁移或「最近 N 条+加载更早」有界窗口 + 帧耗基准 |
| 侧栏会话树键盘/身份（P2/P3） | 会话行仍为自绘行（非 `ui.List/Tree`），键盘只覆盖原生 Sidebar 项 | 迁移 `ui.List` + `ItemKey` 或 `ui.Tree` |
| TextScale 未接线（P2） | 设计系统文字缩放机制仍空转 | 接真实偏好或删除机制 |
| 死契约/死代码（P3） | `home.recent`、`topbar.center`、`buildSidebarError`、palette 快捷键列缺失、modal 宽度裸值、settings section 中文 key | 下一轮清理 |
| 2+ toast 堆叠偶发遮挡（P3） | 已不遮 composer，堆叠时可能压住上方一行内容 | 再抬高 inset 或限制同时可见数 |
| 设置主操作在滚动内容末尾（P2） | 长页首屏可能在视口边缘露出半截主操作；固定 footer 需要给页面 build 暴露动作区（settings 复审 N3） | 用已有 `SlotSettingsPageFooter` 做固定动作条，逐页迁移 |
| 右栏 Overview 开发元数据、contentHeader 单图标带（P3） | 信息/布局取舍 | 视产品需要收敛 |
| `ui.Toolbar` chip 无显式分组（P3） | 溢出收纳已解决可达性，语义分组未做 | 可加分隔符/分组 |

## 7. 交付说明

- 本报告与 `docs/DESIGN-REVIEW.md` 是本轮交付文档；所有修复仍是**未提交工作区改动**，未创建 commit。
- 新增工具：`internal/devtools/ui-drive/`（交互驱动）；扩展 `internal/devtools/ui-snapshot`（pane 选择、失败非零、narrow/dark、manifest）。
- 未新增第三方依赖；未改 `go.mod`/`go.sum`；`old/` 归档工程未触碰。
- 快速复验：`scripts/verify.sh --quick`（fmt/build/vet/test/snapshot），或按 §5 命令逐项执行。
- 修复后的"人性化"复检见 [docs/USABILITY-REVIEW.md](USABILITY-REVIEW.md)（独立只读审计：4 P1 / 13 P2 / 9 P3，含文件行号与截图证据）。
- **后续 H 系列（人性化修复）已全部落地**（同日 14:40 冻结）：26 条 findings 全量修复（设置固定底栏/未保存守卫、测试连接草稿、计划反馈 Enter、审批安全默认、设置导航搜索、热键录入、composer 分组、slash/@ 锚定浮层、队列管理、消息动作/工具卡、goal 文案、Inspector 降噪、插件重试、Home 最近会话、toast 撤销、文件隐藏文件、pane 快捷键等）；并按需求把**归档迁入设置弹窗**（新增「归档」页，可恢复/删除/打开；侧栏删除「显示已归档」开关、永远清爽）。验证：全量测试全绿、ui-drive 84 步 84 PASS、冻结快照 82 张 0 panic。详见 `docs/USABILITY-REVIEW.md` §0.1 修复状态。
