# UI 官方一致性修复状态（mygo v0.8.3）

> 依据：[docs/UI-CONFORMANCE.md](UI-CONFORMANCE.md) 与 2026-11-09 的盘点报告。
> 目标：按 mygo v0.8.3 官方 `docs/ui` 修掉「官方有标准件却手搓/自己猜」的问题，
> 不改变产品结构；每项带帧测试或快照证据。审计时工作区由另一 agent 并行升级
> mygo v0.7.4 → v0.8.3，修复全部基于升级后的 v0.8.3 源码与文档。

## 1. 修复总览

| 区域 | 修复内容 | 主要落点 | 验证 |
|---|---|---|---|
| 共享层 | `DialogFooter` 默认按钮（AutoFocus）；`PageHeader/Section` heading role；`ErrorLine` RoleAlert + 图标 + 可选中；`KeyValueRows` 值可选中；`uikit.EnterTo`、`uikit.ToolIcon` 收敛重复实现 | `internal/uikit/uikit.go` | `go test ./internal/uikit`；平台帧测试 |
| 设计/品牌 | `Theme.InactiveAccent` 保持品牌 accent（与手绘 accent 面一致，单元测试锁定）；`design.Of` 接 `Preferences.TextScale` 且根主题同倍数缩放 `FontSize`（原生控件与自绘文字一起缩放，Tester 1.5× 测试锁定两层）；`ShadowOverlay`/`DialogWidth*` 等 token；清理死 atoms/White/旧 Sidebar 方法；新增品牌/设计层测试 | `internal/brand/brand.go`、`internal/design/*` | `go test ./internal/brand ./internal/design ./internal/icons` |
| 壳层 | 侧栏/右栏 `ui.Split`+`SplitReset`+宽度持久化；drawer 官方 recipe（RoleDialog/Esc/焦点陷阱/Scrim）；`SetMenuPresentation(ui.PopoverMenus)`；内容顶带 `ui.Toolbar`（命令面板+检查器入口+快捷键 a11y）；palette List Label+ActiveDescendant；屏幕/检查器 `c.Announce`；快捷键 anti-repeat；rail `FocusGroup`+AccessSelected；Alt+←/返回按钮走导航栈 | `internal/uiroot/frame.go`、`shortcuts.go`、`uiroot.go`、`navigator.go` | `internal/uiroot/conformance_test.go`（宽度持久化、drawer 模态/播报、导航播报、palette a11y）；全量 uiroot 测试 |
| 右栏 | 变更面板改官方 `editor.DiffView`（ParsePatch/SetPatch，长 diff 有界+回退）；Files/Log/Tools/Tasks/Agents 迁 `ui.List`（Key/Selected/Enter/Label）；Files 目录与 Log 事件缓存（仅刷新/切换读，测试用计数 workspace 证明）；终端 shell 改 `ui.Select` + 输出跟随 + 可复制；空态/图标修正 | `internal/ui/rightpanel/*` | `rightpanel_conformance_test.go`、`rightpanel_internal_conformance_test.go`；`go test ./internal/ui/rightpanel -count=1`（82 PASS）；`uimodtest` changes e2e 改 a11y Value 断言 |
| 侧栏 | 工作区树 → `ui.Outline`（←→/↑↓ 开合）；会话行 → `ui.List`（Key/Label/Selected/Enter）；右键 `Element.ContextMenu` 与 ⋯ `PopoverMenuButton{At,Self}` 共用 menu；删除项 `Danger`；添加工作区对话框 DialogFooter+Enter | `internal/ui/sidebar/*` | `sidebar/conformance_v083_test.go`；`go test ./internal/ui/sidebar` |
| 会话 | 长会话 transcript 有界窗口（默认最近 200 条 + “加载更早消息”分步揭示）；切换会话时清理上一会话的折叠/usage/展开动作/transcript 窗口状态（内部测试覆盖）；发送成功后释放附件缩略图缓存；goal chip 改 `PopoverMenuButton`（Danger/ID）；权限/模式/思考/模型/人设 → `MenuItem.Radio` + 模型 `ID/Subtitle/Trailing`；slash/@ 建议菜单 RoleListBox/RoleOption/PositionInSet/ActiveDescendant；12 处对话框 Enter；dialog 宽度 token；英文会话菜单改 “Session settings” | `internal/ui/session/*` | `go test ./internal/ui/session`；`uimodtest` 全量 E2E |
| 设置 | 记忆页读模型缓存（不再每帧 SQLite）+ 所有变更动作失效缓存；模型目录自定义思考模式编辑器改为 `ui.List`（Key=模式 ID/行号）、记忆策略、模型运行上限（含“使用模块默认值”开关）、模型上下文窗口、模型生成参数（温度/Top P/max tokens，含“使用服务商默认”开关）全部改用官方 `ui.NumberInput`（步进/方向键/范围钳制，共享 `typeNumberInput` 测试助手）；模型目录/预设/记忆/策略对话框 Enter；数据与隐私页主次操作修正；诊断 unknown 本地化；英文副标题修正；对话框宽度 token；MCP STDIO 字段用 `ui.Fieldset` 分组；动作位置统一为 `uikit.PageHeaderAction`（页面级即时动作）/`uikit.SectionAction`（小节工具动作）/宿主 footer（显式保存页唯一主操作） | `internal/ui/settings/*` | `go test ./internal/ui/settings`（全量通过，~43s） |
| 其他屏 | Home 最近会话 `ui.List`+日期 locale；Automation 摘要本地化/去 ID/Radio/Switch；Plugins 工具摘要中文化+原文 Tooltip、MCP 错误收敛、Enter；Approval DialogFooter（Deny 默认焦点/Enter=拒绝）；Toasts `ShadowOverlay`+网格；welcome 复制版本命令修复；工具图标统一 | `internal/ui/{home,automation,plugins,approval,toasts,interaction,welcome,toolcards}/*` | 各包测试 + `modules-fix` 的 6 个 conformance 测试文件 |
| 图标 | 统一 `uikit.ToolIcon`（read_image=Image、read_file=File、web=Globe）；删除 3 个死别名；新增图标不变量测试（viewBox 16/24 + stroke 1.2/1.8，实心点例外） | `internal/icons/icons.go`、`_test.go`、`internal/uikit/uikit.go` | `go test ./internal/icons` |

## 2. 验证证据

环境（本机 `/tmp` 只读，临时目录必须放在 git 工作区之外）：

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/data/mygo/.cache/go-build GOTMPDIR=/data/zeokeel-tmp TMPDIR=/data/zeokeel-tmp
export GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off
gofmt -l internal cmd     # 空
go build ./...            # exit 0
go vet ./...              # exit 0
go test ./... -count=1    # 全绿（见 .cache/final-test.log）
go run ./internal/devtools/ui-snapshot .cache/final-fix7   # 全量截图
```

- 单元/帧测试：`internal/uiroot/conformance_test.go`、`internal/icons/icons_test.go`、
  `internal/design/tokens_test.go`、`internal/brand/brand_test.go`、
  `internal/ui/session/transcript_window_test.go`、`internal/ui/session/prune_internal_test.go`、`internal/uikit/uikit_test.go`（动作位置）、`internal/ui/rightpanel/rightpanel_conformance_test.go`（右键菜单）、
  `internal/ui/rightpanel/*conformance*_test.go`、
  `internal/ui/{sidebar,home,automation,plugins,approval}/conformance_v083_test.go` 等。
- GUI 快照：`.cache/final-fix/`、`.cache/final-fix2/`、`.cache/final-fix3/`、`.cache/final-fix7/`
  （最终，72 PNG）；已人工查看
  chat/trajectory、files/changes/diff、settings（含记忆策略 NumberInput、MCP
  Fieldset 所在页）、sidebar、approval、narrow/drawer、暗色等关键屏。
- 变更测试断言（官方 DiffView 行内容在 canvas 上，`Tester.Texts` 只见 hunk header）：
  `internal/ui/rightpanel/changes_test.go`、`internal/ui/uimodtest/changes_e2e_test.go`
  改为 `tester.Access("New text").Value` 断言；`narrow_h` 两个用例按官方 modal
  drawer 语义（不再有 content-inset）改为窄窗口/先重绘再点击。

## 3. 决策记录（已修复或已接受，附理由）

| 项 | 现状/原因 | 建议 |
|---|---|---|
| transcript 性能 | **已修复（官方 `ui.List`）**：transcript 现在是 `ui.List`（`Key=message.ID`、`Label`、`FollowEnd` 流式跟随、`HoldPlace` 折叠不跳、`ScrollToEnd` 回到最新）；长会话只构建可见行，不再每帧构建全部消息，旧的 200 条有界窗口被虚拟化取代 | 继续随 UI-CONFORMANCE 观察流式性能 |
| 数字设置迁移 | **已完成**：记忆策略、运行上限、上下文窗口、温度/Top P/max tokens 全部为 `ui.NumberInput` + 默认开关（旧自由文本校验路径与测试已移除） | 无需再处理 |
| `ui.Fieldset` | **已采用**：MCP STDIO 字段组用 Fieldset + legend（group a11y）；其余表单是单字段分组，用 `uikit.Section` 更简洁 | 新表单出现多字段子组时用 Fieldset |
| 图标双网格（16/24） | **已用不变量测试锁定**：每个图标必须属于 16 或 24 网格且描边符合各自光学权重（1.2/1.8），新增图标错网格会测试失败；两族在同尺寸渲染下描边视觉等重，重绘 24→16 无视觉收益、风险大于收益 | 新图标必须按测试规则走；需要时再做同族替换 |
| 设置页动作位置 | **已收敛**：新增 `uikit.PageHeaderAction`/`SectionAction` 并迁移技能（新建/刷新）、更新（检查更新）、预设（恢复内置/恢复默认人设）、数据与隐私（导出诊断报告）；规则写入 `docs/UI-CONFORMANCE.md`，uikit 布局测试钉住「动作与标题同行」 | 产品确认无需再做 |
| Log pane 缓存帧测试 | 已由 `uimodtest` 的日志面板 e2e 断言（`TestP1ApprovalToolsAndPersistence` 选中日志页校验 user_message/run_end）与 rightpanel 内部缓存测试共同覆盖 | 可再补一条 count 断言，非缺口 |
| 包级可变状态 | **已清理标题全局**：workspace/文件目录选择、会话 Markdown/JSONL 导出、数据导出/诊断报告标题不再写包级变量，改为 seam 参数（`DirectoryPicker(title)`、`ExportSaver(title, name, content)` 等），双语言多窗口不再共享可变态；剩余的是纯函数注入点（测试替换文件对话框/模型列表 HTTP），无应用状态，按 mygo-maintenance 的测试做法保留 | 如需彻底消除可收进 per-window service，收益低 |
| `topbar.*` 死槽位 | **已清理**：删除 `contracts.SlotTopBar*`/`SlotOverlaySettings/Palette` 常量、welcome 的 `welcome.topbar` 注册与相关测试断言；`docs/ARCHITECTURE.md`/`docs/INJECTION-POINTS.md` 明确「本壳层无顶栏，全局动作在 Toolbar/命令面板」 | 无需再处理 |
| 自定义 ButtonBase 的 rowTint 3 处 | **保留**：mygo custom-widgets.md 明确允许在 base 上自绘状态；这 3 个是没有任何原生外观的独立图标按钮（检查器关闭、artifact 标题、任务 ⋯），代码已注释理由 | 如需彻底收敛可换自带状态的按钮，非缺陷 |

## 4. 约束提醒

- 非活动窗口现在**保留品牌 accent**（`Theme.InactiveAccent = Accent`，官方文档允许两种
  行为）。若产品更想要 AppKit 的原生灰化，删掉 `internal/brand/brand.go` 里那一行并把
  手绘 accent 面改读 `c.Theme().InactiveAccent` 即可。
- `design.Tokens.TextScale` 现在接 `c.Preferences().TextScale`；如果平台显式字号已被
  mygo 主题放大，会出现二次放大——需真机用 `tt.SetPreferences` 验证一次。
- 升级/迁移门禁：`go tool mygo migrate-ui .` + `go tool mygo vet .`（环境无该 tool 时用
  `go vet` 替代），详见 [docs/UI-CONFORMANCE.md](UI-CONFORMANCE.md)。
