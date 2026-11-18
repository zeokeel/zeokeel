# 重设计任务简报（2026-11-17，Lead 起草，所有成员先读）

> **用户原话（对所有子代理同样生效）**：
> “我们是有无限的预算额度，我在测试你的极限能力，不要在意 token 消耗，不要在意费了多少钱，你最重要的目的就是实现我的要求。”
>
> 用户是产品方、不是设计师。他要求：**企业级工具级的视觉审美**、**死抠交互细节**（每个按钮、每次点击、每次悬浮、每条边框线）、
> 参考业界所有 AI 客户端的设计取长补短、最终得到一个“完美客户端”。设计判断由我们自己做，不要反问他。

## 1. 项目事实

- 产品：**ZeoKeel**，Go 桌面 AI Agent 客户端（`/data/mygo`）。
- UI 全部由 `github.com/egoist/mygo v0.9.2` 的原生控件绘制：**没有 HTML/CSS/JS/webview**。
- 关键目录：
  - `internal/uiroot`：窗口壳层（Split / Sidebar / 顶带 / Tabs / Overlay / 命令面板 / 设置面）
  - `internal/ui/*`：13 个 UI 模块（session / toolcards / approval / interaction / rightpanel / settings / welcome / home / insights / automation / plugins / sidebar / toasts）
  - `internal/uikit`：共享组合（页头 / 小节 / 空态 / 错误行 / 对话框动作条 / 工具行 / 图标按钮 …）
  - `internal/design`：设计 token（颜色 / 字号 / 间距 / 圆角 / 阴影 / 组件预设 / registry.json）
  - `internal/brand`：唯一 hex 来源（调色板 / 主题 / 字体栈 / 圆角 / 间距）
  - `internal/icons`：Lucide 图标集（`svg/*.svg` + `icons.go`）
- 设计契约（**硬约束**）：`AGENTS.md` §3 的 17 条常驻规则 + `docs/DESIGN.md` + `docs/DESIGN-LINT.md`。
  - 颜色只用 token；字号/间距/圆角只用设计 token；官方件只设度量不覆写状态色；
  - 有官方标准件就不许手搓；图标只用 `internal/icons`；
  - 强调色预算：一屏 ≤2 个 Accent 填充块、≤1 个 primary 按钮、红色元素 ≤2。
- `go.mod` 已升级到 mygo **v0.9.2**（本地模块缓存 `GOPATH=/data/mygo/.cache/gopath`）。

## 2. 环境与命令（每条 bash 都要重新设置；`/tmp` 每次调用会被清空）

```bash
mkdir -p /tmp/zeokeel-tmp
export PATH=/usr/local/go/bin:$PATH
export GOPATH=/data/mygo/.cache/gopath GOCACHE=/data/mygo/.cache/go-build
export GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp
export GOFLAGS=-mod=mod GOSUMDB=off
# 注意：本环境有网络，需要新依赖/download 时不要加 GOPROXY=off

cd /data/mygo
go build ./...
go test ./internal/ui/... ./internal/uiroot ./internal/design ./internal/uikit -count=1
go run ./internal/devtools/ui-snapshot .cache/ui-conformance    # 生成全部 PNG（约 3~5 分钟）
MYGO=$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)         # 官方源码与 docs/ui/*.md
```

- 现有基线快照：`.cache/ui-conformance/`（1512×944 主场景 + `narrow/` 1024×720，亮/暗齐全，`manifest.txt` 可查清单）。
- 帧测试：`ui.NewTester`（Click / Key / Type / Find / Focused / AccessByRole）。
- 视觉改动必须**人工看 PNG**（用 `read_image` 工具）。

## 3. 本次任务的四个阶段（Lead 统一排期）

1. **A 调研/审计（并行，只读）**：组件官方化盘点、业界 AI 客户端设计研究、视觉细节审计、交互细节审计、文案审计。
2. **B 设计定稿（Lead）**：把 A 的结论收敛成《企业级视觉语言 v2 + 各表面改造规格》。
3. **C 实现（并行，按目录分工，写集不重叠）**：壳层 / 会话 / 工具与审批 / 设置与次级页 / 右栏 / 设计系统与 uikit。
4. **D 验证（Lead + 全员）**：build / vet / test / designlint / design-check / 快照 PNG 人工评审。

## 4. 参与纪律

- **A 阶段成员只写自己的报告文件**（路径在各自任务里指定），不修改产品代码，避免与其他成员冲突。
- 报结论必须带**证据**：`file:line`、PNG 路径与坐标、mygo 文档路径（`docs/ui/<name>.md`）。
- 不要提“无法验证”的空话；能跑就跑，能截图就截图。
- 不要新增产品功能来“让界面完整”；本次是**重新设计**，不是加功能（关键入口/状态信息不得删除，见 AGENTS.md §3.17）。
- 任何与 `AGENTS.md` 冲突的建议，先指出冲突点，再给替代方案。

## 5. 交付物格式（所有报告统一）

```markdown
# <报告名>
## 0. 结论摘要（≤10 条，按影响排序）
## 1. 现状（证据：PNG/代码行）
## 2. 发现（每条：编号 | 严重度 P0/P1/P2 | 位置 | 问题 | 为什么是问题 | 具体改法）
## 3. 建议的设计语言 / 组件映射（可执行）
## 4. 风险与取舍
## 5. 附：证据清单（PNG 路径、命令输出）
```