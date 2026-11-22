# Memory v2 产品接入设计（右栏「记忆」tab）

> 目标：让 v2 记忆在桌面端**可发现、可浏览、可管理**，而不是只剩模型工具。
> 设计原则：先信息结构，后视觉；不新造卡片/组件；复用现有右栏 tab、`ui.List`、
> `ui.SearchField`、`uikit.EmptyStateBlock`、`uikit.ConfirmDanger` 与 design token。

## 1. 范围

**做**

- 右栏「记忆」tab 成为 v2 记忆的唯一产品入口；
- 浏览（按更新时间）、搜索、类型过滤、详情（L0/L1/L2）；
- 新增、编辑、置顶、删除；
- 空态/加载/错误/后端不可用状态；
- 键盘：`/` 搜索、↑↓ 选择、Enter 详情、Esc 返回。

**不做（本期）**

- 会话归档、规则编译、快照/恢复、导入导出等高级动作（放 P2，用 `memory` 工具或后续页面）；
- 恢复已删除的旧设置「记忆」页；记忆管理集中在会话右栏；
- 旧 `memory.json` 自动迁移（按已确认决策：不迁移）。

## 2. 信息架构

右栏宽度有限，采用**列表 → 详情**的两级结构，不并排、不弹大窗：

```
┌ 记忆 ──────────────────────────────────────────────┐
│ [搜索记忆…                         ] [类型 ▾] [+ 新建] │
│ 12 条 · 最近更新 2 小时前                            │
│                                                     │
│ ★ 偏好：提交前先跑测试                  偏好 · 2 小时前 │
│   发布前先跑 go test ./...，失败不提交。              │
│ ─────────────────────────────────────────────────── │
│   事件：修复 macOS 输入法闪烁            事件 · 昨天   │
│   关闭自动切换输入法后消失；根因在 …                 │
│ ─────────────────────────────────────────────────── │
│   案例：CI 缓存失效排查                 案例 · 3 天前 │
│   …                                                  │
│                                                     │
│ [ 记一条…                                      ] 保存 │
└─────────────────────────────────────────────────────┘
```

点击行进入详情（同一 tab 内，不叠 modal）：

```
┌ ← 记忆 / 偏好                                 ★ 置顶 ⋯ │
│ 提交前先跑测试                                        │
│ 偏好 · 更新于 2 小时前 · 128 tokens                   │
│                                                     │
│ 摘要                                                 │
│ 发布前先跑 go test ./...，失败不提交。                │
│                                                     │
│ 概览                                                 │
│ …（L1，最多 16 KiB，可滚动）                          │
│                                                     │
│ 正文                                                 │
│ …（L2，Markdown 纯文本渲染，只读）                    │
│                                                     │
│ [编辑]                                      [删除]   │
└─────────────────────────────────────────────────────┘
```

- 列表只显示 **title + 一行 summary + 类型/时间**；正文只在详情读。
- 类型不是彩色徽章：用 `LabelSecondary` 的短词 + 分隔点，避免右栏到处是 pill。
- 置顶用 `icons.Pin`（12px，`LabelTertiary`；置顶后 `Accent`），不新造图标。
- 列表行之间用 `ui.List` 原生分隔；不改造卡片。
- 详情里的「正文」用现有文本渲染（Markdown 组件不可用），保留换行；不做语法高亮。

## 3. 关键交互

### 搜索与过滤

- 搜索框保留现有 `ui.SearchField`，输入 **300ms debounce**，后台读，结果经
  `UIRuntime.Post` 回 UI（现有 S-17 模式）。
- 类型过滤器是 `ui.Select`/菜单，选项：全部 + 九类（profile/preferences/entities/
  events/cases/trajectories/experiences/tools/skills），显示中文短名。
- 搜索与过滤可叠加；清空搜索恢复按更新时间倒序。

### 新建

- 底部快速输入保留：Enter 保存，默认类型 `preferences`，标题取首行前 60 字符，
  summary 取首行，content 取全文。
- 头部「+ 新建」打开小 dialog：类型、标题、摘要、正文；保存后回到列表并选中新条目。
- 不在帧内做 I/O：点击只写 state，后台 `Write`，成功后刷新。

### 编辑 / 置顶 / 删除

- 编辑：详情右上「编辑」→ 同一个小 dialog（预填），保存 `Get` 后 `Write` 回写
  （保留 Created/Version，更新 Updated）。
- 置顶：`Write` 时翻转 `Pinned`；列表置顶项排在前面（`Pinned` 优先，再按 Updated）。
- 删除：`uikit.ConfirmDanger`；确认后后台 `Delete`，失败 `ErrorLine`，不乐观移除。

### 状态

- 加载：`Spinner + 搜索中…`（复用现有）；首次列表可显示 3 行轻量 skeleton 文本，
  不闪烁空态。
- 空：无记忆 → 「还没有记忆」+ 「写入第一条」；无搜索结果 → 「没有匹配的记忆」。
- 错误：`uikit.ErrorLine` + 重试按钮（重新读取，不丢查询）。
- 后端不可用：不再显示“打开设置”，改为「记忆后端不可用」+ 「重试」；错误详情走
  `contracts.MemoryService` 返回。

## 4. 视觉规格（token）

| 元素 | 规格 |
|---|---|
| tab 内容宽度 | `design.TranscriptWidth`，`MaxWidthPercent(92)`（沿用） |
| 搜索框/类型/按钮行 | 高度 32；间距 `Space2/Space3` |
| 列表行内边距 | 垂直 `Space2`，水平 `Space3`；行间原生分隔 |
| 标题 | `TypeS` + `WeightMedium`，一行省略 |
| 摘要 | `TypeXS`，`LabelSecondary`，最多 2 行 |
| 元信息 | `TypeXXS`，`LabelTertiary`（类型 · 相对时间） |
| 详情标题 | `TypeM` + 600；分节标题 `TypeS` + 600 |
| 正文 | `TypeBody`，行高沿用；代码/长文滚动 |
| 选中态 | `BgSelected` + 2px `Accent` 左条；键盘焦点可见 |
| 危险动作 | `uikit.ConfirmDanger`；取消为 Default |
| 强调预算 | 列表仅置顶星标/选中左条用 Accent；主按钮只有底部「保存」/详情「编辑」之一 |

## 5. 接入架构

### 5.1 新服务契约（产品侧）

在 `internal/contracts` 增加 v2 视图契约，不把 `memoryvfs` 类型漏进 UI：

```go
type MemoryItem struct {
    URI      string
    Type     string
    Title    string
    Summary  string
    Updated  time.Time
    Pinned   bool
    Tokens   int
}

type MemoryDetail struct {
    MemoryItem
    Overview string
    Content  string
}

type MemoryService interface {
    List(ctx context.Context, workspaceRoot string, query string, types []string, limit int) ([]MemoryItem, error)
    Get(ctx context.Context, workspaceRoot, uri string) (MemoryDetail, error)
    Write(ctx context.Context, workspaceRoot string, draft MemoryDraft) (MemoryItem, error)
    Delete(ctx context.Context, workspaceRoot, uri string) error
}
```

- `internal/modules/memorystore`（新模块）持有 **按工作区缓存** 的
  `memoryvfs.Store`（`<dataDir>/memory/<workspaceID>/memory.db`），实现上述契约，
  模块 dispose 时统一 `Close`。
- agent 不再每次 Run 打开/关闭 store，而是从该服务取当前工作区 store 传给
  `kit.Config.MemoryStore`；memory 工具由 agent 用同一 store 构造。这样：
  - 同工作区 UI 与 agent 共享一个 store，避免多连接与“UI 看不到刚写入”的错觉；
  - store 生命周期归宿主模块，kit 不关闭注入的 store。
- 旧 `contracts.KeyMemory`/`MemoryStore`/`MemoryUpdater`/`MemoryPolicy`/
  `MemoryConsolidationReport` 已从 session UI 移除并删除（2026-11-23 wrap-up）；
  session 只通过 `contracts.KeyMemoryService` 读取 v2 视图。

### 5.2 UI 数据流

```
buildMemoryPane (frame)
  ├─ 读 st.memoryView（缓存；不 I/O）
  ├─ 查询变化 → debounce → goroutine: svc.List(root,…)
  │                         └─ UIRuntime.Post → st.memoryView/err/reading
  └─ 点击/编辑/删除/置顶 → 写 st.pendingAction → goroutine 调 svc
                             └─ Post → 刷新 + 选中/返回
```

- 工作区根来自 session 的 `sessionWorkspaceRoot`（与 agent 同一规则）；
- 切工作区/切会话时清空缓存与选中，重新读；
- 详情按 URI 读 `Get`，不在列表预取正文。

### 5.3 动作映射到 v2

| UI 动作 | memoryvfs |
|---|---|
| 列表 | `Tree(ctx, mem://user/self/memories, depth=3)` 或按类型 `List` |
| 搜索 | `Search(ctx, Query{Text, Scope: memoriesRoot, Kinds: memory, Limit})` |
| 详情 | `Read(ctx, uri, Level2)` |
| 新建/编辑 | `Write(ctx, Entry{URI, KindMemory, Type, Summary, Overview, Content})` |
| 置顶 | `Get` → 改 `Pinned` → `Write` |
| 删除 | `Delete(ctx, uri)` |

URI 命名：`mem://user/self/memories/<type>/<slug>-<hash8>`；slug 由标题生成、冲突时
加 hash；类型必须是九类之一（`pages` 不进入新建菜单）。

## 6. 分期

- **P0（已完成）**：列表 + 搜索 + 详情 + 删除 + 空/错/加载态；只读为主。
- **P1（已完成）**：新建/编辑、置顶、类型过滤、计数；键盘完整。
- **P2（未做）**：会话归档、规则、快照/恢复、统计与导入导出（可复用 `memory` 工具动作）。

## 7. 验收

- 帧测试（`ui.NewTester`）：搜索 debounce、详情进入/返回、删除确认、置顶、空/错误态；
- 快照：亮/暗、窄窗、空态、详情长文，人工看 PNG；
- `designlint --strict --baseline`、`scripts/design-check.sh`、`go tool mygo vet`；
- 产品 `go test ./...` 与 `verify.sh --quick` 全绿。

## 8. 确认与实现状态（最终，2026-11-23）

- 位置：只放会话右栏「记忆」tab，不恢复设置页（已确认）。
- P0/P1 范围：浏览/搜索/类型过滤/详情/删除 + 快速新建/编辑/置顶（已确认并实现）。
- 新建默认类型：`preferences`（已确认并实现）。
- 已实现：共享 `memorystore` 模块 + `contracts.MemoryService`、列表/搜索/类型过滤、
  详情、快速新建、行内编辑、置顶（Get→改 `Pinned`→Write）、删除确认。
- 工作区定位：`memoryWorkspaceRoot` 优先使用会话的 `WorkspaceID`（经 workspace
  服务解析到根），其次才回退到当前工作区；绑定到其他工作区的会话不再看到当前
  工作区的记忆。
- 未实现（P2）：会话归档、规则编译、快照/恢复、统计与导入导出。保留 `memory`
  工具与后续页面承接，不在本期 UI 中做。
- 旧 `memory.json` 不迁移（已确认决策）；v1 扁平存储的 `KeyMemory`/
  `MemoryStore`/`MemoryUpdater`/`MemoryPolicy`/`MemoryConsolidationReport`
  契约已删除。
