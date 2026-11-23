# Stage 0：契约解耦（`BuildFunc` 出栈）

> 目标：让 `internal/contracts` **不依赖 `github.com/egoist/mygo`**，成为可序列化、引擎无关的共享面。
> 前置：本文档替代原先设想的"把 `BuildFunc` 抽象成引擎无关 builder"——实测那个方案更贵且没有必要。
> 状态：待执行。基线 `master` 干净。

---

## 0. 实测结论：这比原方案便宜得多

原设想是"抽象 5 个签名（`ScreenSpec`/`SlotItem`/`SettingsPageSpec`/`ToolRenderer`/`CommandSpec`），改造 32 个槽位"。

实测后发现**不需要改造任何 builder**：

| 事实 | 证据 |
|---|---|
| `internal/contracts` 里**只有 1 个文件**碰 mygo | `grep -rln "egoist/mygo" internal/contracts/*.go` → 仅 `contracts.go` |
| `contracts.go` 只有 367 行，`ui.` 只出现 5 次 | `contracts.go:17,77,156,311,314` |
| 契约区**之外**的消费者只有 1 处 | `contracts.CommandSpec` → `internal/host/update_command.go`（该文件已 import `uiroot`） |
| `UIRuntime` 在契约区外**零**消费者 | `grep` 无非 ui/uiroot 命中 |
| `ToolCall` / `ToolCallFile` 被后端模块使用 | `internal/modules/agent/*`、`internal/uikit/*` → **必须留在 contracts**，但它们不碰 `ui.Context` |

**所以正解是把"mygo 相关的贡献模型类型"整体挪进子包，而不是抽象接口。** 行为零变化，契约区立刻变干净。

---

## 1. 切分边界

新建 `internal/contracts/uinative/`（名字即"原生 UI 贡献模型，Stage 3 随 `internal/ui` 一起删除"）。

| 挪走（进 `uinative`） | 留下（`internal/contracts`） |
|---|---|
| `BuildFunc`（:17） | `ToolCallFile`（:102） |
| `ScreenSpec`（:20） | `ToolCall`（:111） |
| `SlotItem`（:30） | `AppInfo`（:185） |
| `SettingsPageSpec`（:38） | `Navigator` / `ThemeService` / `LocaleService` / `SettingsService` / `SettingsDeleter` / `Logger` / `ScreenLister` |
| `SettingsSearchItem`（:83） | 其余 60+ 个契约文件（全部不动） |
| `ToolRenderer`（:152） | |
| `CommandSpec`（:161） | |
| `UIRuntime`（:289） | |

`uinative` 允许 import `internal/contracts`（取 `ToolCall`）与 `github.com/egoist/mygo/ui`；**反向禁止**。

> 注意 `Navigator` / `ThemeService` 等接口是否间接引用 `ui.Context`——执行时逐个核对；若有，同样下沉到 `uinative`。

---

## 2. 执行步骤

1. 建包 `internal/contracts/uinative/`，移入上表类型（保持注释与文档字符串原样）。
2. 全仓改引用：`internal/ui/**`、`internal/uiroot/**`、`internal/host/update_command.go`（约 20 处）。
3. `internal/contracts/contracts.go` 删掉 `ui` import，包注释改为"引擎无关"。
4. **加机器断言**（防止回退）：在 `internal/assembly/boundary_test.go` 增加用例——用 `go list -deps` 断言 `internal/contracts` 的依赖图中不含 `github.com/egoist/mygo`，且 `internal/contracts/uinative` 只被 `internal/ui/**`、`internal/uiroot/**`、`internal/host` 引用。
5. 验证并出证据。

---

## 3. 验证（缺一不可）

```bash
export PATH=/usr/local/go/bin:$PATH
export GOPATH=/data/mygo/.cache/gopath GOCACHE=/data/mygo/.cache/go-build
export GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off
gofmt -l internal cmd        # 期望空
go build ./...
go vet ./...
go test ./internal/contracts/... ./internal/assembly ./internal/uiroot -count=1
go test ./internal/ui/... -count=1          # 行为未变，必须全绿
go test ./internal/host -count=1
bash scripts/design-check.sh
```

**验收标准**：上述全绿，且 `go list -deps ./internal/contracts | grep egoist/mygo` 为空。

---

## 4. 为什么不顺手把槽位数据化

槽位 schema 化是 **Stage 1 的裁决点**，不是 Stage 0 的。理由：

- 单客户端下屏幕/设置页用**静态路由**即可，不需要 schema（见 WEB-CLIENT.md 的 25 条路由）。
- 只有**工具卡渲染器**需要按工具名分发，而那只需要一个**字符串 ID**，不需要 `BuildFunc`。
- TRANSPORT.md §8.5 已经把这个悬空标出来了（保留 +2 方法，删除则 ≈106 不变）。**Stage 0 不做，Stage 1 再定。**

---

## 5. 风险

| 风险 | 影响 | 应对 |
|---|---|---|
| `Navigator`/`ThemeService` 等藏着 `ui.Context` | 切分边界要扩大 | 执行时逐接口核对，宁可多挪 |
| `host` 反向依赖 `uiroot` 加深 | 冻结区耦合更紧 | 只允许 `update_command.go` 这一处，并在文档记录 |
| 测试里有大量 `contracts.ScreenSpec` 字面量 | 改 import 面变大 | `gofmt`/编译器驱动，批量机械替换 |

---

## 6. 与换轨的关系

这一步**独立于 Web 换轨也有收益**：契约层不再被原生 UI 引擎绑死，ACP 之外的第三个传输（网关）才可能只依赖 `internal/contracts`。它是整条迁移里唯一"两头都不亏"的动作。