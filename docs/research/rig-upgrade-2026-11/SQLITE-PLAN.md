# rig 持久化升级计划：把产品的 SQLite 实践回流到 SDK

> 依据：rig `v0.23.4`（`.cache/rigcheck/repo`，当天 clone）+ 本仓 `internal/modules/sessionstore`
> 与 `internal/contracts/store.go`。目标不是"给 rig 塞一个依赖"，而是让 rig 把**状态**当成一等能力，
> 让每个宿主不必像我们一样从零写一遍。

## 1. rig 现在的落盘面（逐项核对源码）

| 状态 | rig v0.23.4 的实现 | 位置 |
|---|---|---|
| 会话消息 | 一会话一 NDJSON，追加写 + fsync，无索引文件 | `session/store`（store 1134 行） |
| 会话列表 | 扫目录 + 读每会话**前 256 KiB / 前 4 条**找标题；`Messages` 恒 0 直到首次 Load | `store.scan`/`scanTitle` |
| 中断检查点 | **只有接口**，无内置实现 | 根包 `checkpoint.go` |
| 事件日志 | `Emitter(io.Writer)` 只**发**不存；`tools/session.MemoryLog` 是进程内实现 | `events/`、`tools/session` |
| 成本/用量 | **纯内存** `Meter`（`Summary`/`Snapshot` 可读，重启即归零） | `costmeter/` |
| 记忆 | 文件（`internal/memwiki`） | `memory.go`/`file.go`/`wiki.go` |
| 大工具结果 | 超阈值落盘成文件（`agent/spill`），无索引、无 GC | `agent/spill.go` |
| 撤销快照 | git 目录（`internal/gitsnap`），元数据不入库 | `internal/gitsnap` |
| 多实例 | **明确不支持**（文档原文："不做索引文件、不做跨实例协调、不做文件锁"） | `store.go` 包注释 |

## 2. 产品实践暴露的缺口（每条都是我们真实踩过/做过的）

1. **跨会话检索**：产品主入口是「搜索会话与消息」，要按正文/事件搜、给片段、分页。NDJSON 只能扫目录。
2. **多窗口 / 多实例并发**：我们的快照 harness 与 fork 会同时打开 store；NDJSON 的两个实例互相看不见新会话。
3. **检查点续传**：中断后续跑需要原子写入 + 可列表；rig 只有接口，宿主各写一遍。
4. **成本持久化**：洞察页要按天/按模型聚合成本，`costmeter` 重启即归零。
5. **便宜的会话列表**：启动 O(会话数) 次头部读；DB 里标题/条数/时间是列。
6. **事件可检索/可回放**：轨迹页要按类型/时间筛；`events` 只写流。
7. **记忆可检索/去重**：我们记忆页有 Merge duplicates / Clear all；纯文件做不到。
8. **spill 治理**：文件会无限增长，需要按会话索引 + GC。
9. **跨文件一致性**：半行裁剪解决单文件撕裂，但"消息写入 + 会话元数据 + 事件"三处跨文件更新没有事务。
10. **数据体积**：会话多了以后目录扫描与头部读是线性成本。

## 3. 我们的做法（可搬的部分）

`internal/modules/sessionstore`：SQLite（`modernc.org/sqlite`，纯 Go 无 cgo）四表
`sessions / messages / events / checkpoints`，WAL + 外键 + busy timeout，事务原子；
会话元数据（标题/计数/时间/workspace）、历史、检查点、事件日志、跨会话搜索都在一个库里。
不变量写在包注释里并有测试守：Append 原子建行、Load 未知会话返回空、Search 大小写不敏感且
LIKE 特殊字符按字面、Last 非正 limit 返回空、事件按 seq 升序。

## 4. 改动计划（每阶段独立可发版）

### P0 拆依赖与契约（≈1 天）
- `session/store` 的 `Store` 接口已是公共契约（`store.Store`），保持不动；
- 新增 **`session/sqlite` 子模块**（自带 `go.mod`，依赖 `modernc.org/sqlite`，纯 Go）；
  主模块的 `make deps`（"编译进来的 import 恰好只有 mvdan.cc/sh/v3"）与 `cgo-free` 门禁**不受影响**；
- 把 `session/store` 现有内部测试抽成 **`storetest`** 公共包：`storetest.Run(t, factory)`，NDJSON 与 SQLite
  跑同一套契约（这也是给所有第三方实现的标准）。

### P1 SQLite 落盘（≈3–5 天）
- 四表 + 索引（`sessions.updated_at`、`messages(session_id, ordinal)`、`events(session_id, seq)`、`checkpoints(session_id)`）；
- 实现 `store.Store` + `rig.History` + `rig.CheckpointStore` + `rig.SessionLog`（**补齐 rig 没有的内置检查点实现**）；
- 崩溃/并发测试：kill -9 中途写、双实例并发、半行残留、索引缺失自愈（对齐我们已有的用例）。

### P2 检索（≈2 天）
- `Search(ctx, Query{Text, Types, Since, Limit, Before})` → 命中 + 片段 + 分页；
- driver 支持就用 FTS5，否则 LIKE + 索引；对宿主统一一套语义。

### P3 成本入库（≈2 天）
- `costmeter.Meter.Snapshot()` → `costs(session_id, model, prompt, completion, cached, usd_nanos, at)`；
- 查询：按天 / 按模型 / 按会话；修掉"重启归零"。

### P4 事件与检查点产品化（≈2 天）
- `events` 增加 `Sink` 接口（`Emit` → 存储），NDJSON 仍是默认 sink；
- 检查点给内置实现（NDJSON 与 SQLite 各一份）。

### P5 记忆入库（≈2 天）
- `internal/memwiki` 增加 SQLite 后端：条目 + 检索 + 去重/合并（对应我们记忆页的 Merge duplicates）。

### P6 spill 治理（≈1 天）
- spill 记录 `(session_id, path, bytes, created_at)`，提供按会话清理与总预算。

### P7 undo 元数据（≈1 天）
- `gitsnap` 快照元数据入库（"这回合改了哪些文件"可查），文件仍走 git。

### P8 迁移与切换（≈1 天）
- `store.Migrator`：NDJSON ↔ SQLite 双向、dry-run、进度、校验（消息数/最后 seq/标题）；
- 宿主一行切换：`kit.Config.SessionDBPath`（与 `SessionStoreDir` 并存，二选一）。

## 5. 默认值取舍（需要拍板）

| 方案 | 说明 | 风险 |
|---|---|---|
| **A（推荐）SQLite opt-in** | `session/sqlite` 子模块，默认仍是 NDJSON | 保守；宿主想要才拿 |
| B SQLite 设为默认 | 新宿主开箱即得检索/事务 | 所有宿主被迫接受 driver；需要 minor + BREAKING 注记 |
| C 只提 storetest + 文档 | 零依赖，但 SDK 自身不进步 | 收益最小 |

无论选哪个，**依赖政策文档要改**：CONTRIBUTING 的"只有一个受审依赖"要允许"核心零依赖 + 可选子模块"。

## 6. 验收与门禁（按 rig 的规则来）

`gofmt` / `go vet` / `go test -count=1` / `-race` / 覆盖率（总 ≥80%、每包 ≥70%）/
catalog gate / deps gate / cgo-free / 跨 OS vet / staticcheck / govulncheck / README 漂移。
提交用 Conventional Commits，PR 模板填"跑过的命令"；无 CLA（inbound=outbound MIT）。

## 7. 建议顺序

P0 → P1（核心价值，也最能证明"SDK 从产品实践长出来"）→ P2 → P3 → P5 → P4/P6/P7 → P8。
