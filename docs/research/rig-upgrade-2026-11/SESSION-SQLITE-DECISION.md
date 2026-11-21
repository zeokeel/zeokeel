# 决策：不迁移到 rig `session/sqlite`（2026-11-21）

> 结论先行：**保留自研 `internal/modules/sessionstore`（SQLite，modernc），不把会话持久化换成 rig `session/sqlite`**。
> 原因不是"工作量"，而是替换会**丢能力**（检索质量、会话重命名、产品专有表/接口）并把依赖钉到无 tag 的嵌套子模块。
> 本文记录证据与"什么情况下重新评估"，防止后续再次把它当成"SDK 已有一份就该搬过来"。

## 1. 两边是什么

| | 本仓 `sessionstore` | rig `session/sqlite@v0.26.1` |
|---|---|---|
| 形态 | 主模块内，`contracts.Store` 的一实现 | **嵌套子模块**（不在根 module zip 内；`@v/list` 空，只有伪版本） |
| 规模 | 非测试约 1.4k 行（store 937 / search 190 / search_text 269） | 非测试约 8.2k 行 |
| 表 | `sessions / messages / events / checkpoints` | `rig_sessions / rig_messages / rig_events / rig_costs / rig_memory_pages / rig_spill` + 账本表 |
| 契约 | `contracts.Store` = `rig.SessionLog` + `History()` + `CheckpointStore()` + `ReplaceHistory` + `Create/Delete/Rename/Session list/GetSessionMeta/AppendEvent` | `Store.History()/Checkpoints()/SessionLog()` + 检索/记忆/溢出/成本/导入导出/租约 |
| 检索 | 维护 `search_text` 解码列（引号/反斜杠/tool 参数 JSON 不产生漏命中） | `CAST(payload AS TEXT) LIKE ?`（匹配原始 JSON） |
| 依赖 | `modernc.org/sqlite v1.61.1` | 要求 `modernc.org/sqlite v1.62.0`（连带抬 `modernc.org/libc`） |

## 2. 四个硬缺口（详细取证见 [PARITY-v0.24.0.md](PARITY-v0.24.0.md) §B.1）

1. **没有 `RenameSession`**：子模块只有 Create/Append/AppendBatch/Load/List/Delete/Replace；产品 UI 的会话重命名要改成裸 SQL（`DB()`）或扩展，等于把"产品契约"塞进别人的库。
2. **检索质量回退**：rig 直接对 JSON 载荷 `LIKE`，本仓专门用解码列修正中文与含引号内容的漏命中；换过去要么接受回退，要么用 `Options.Extensions` 给 `rig_messages` 加解码列 + 自写检索 SQL——那时省下的代码又长回来。
3. **依赖与发版形态**：嵌套子模块、无正式 tag（只能钉伪版本）、`modernc.org/sqlite` 版本抬升。对一个个人客户端来说，这是把"可预测的依赖"换成"跟着上游未发布子模块走"。
4. **现有数据库迁移**：rig `ImportDir` 只吃 NDJSON，不吃本仓 `zeokeel.db`；需要一次性旧表→新表迁移 + 回滚方案，且表名全变会让仓库外任何诊断/分析脚本失效。

## 3. 收益评估

- **代码量**：保守方案（保检索质量）净删 ≈640 行、重写约 800–1500 行测试；激进方案（接受 LIKE 检索）净删 ≈1100 行但**功能回退**。
- **能力增量**：成本落库（`rig_costs`）、记忆/溢出落库、导入导出、租约、`CanOpen/OpenReadOnly`。这些对**桌面单用户**客户端不是刚需：成本已由产品侧 catalog/事件折叠，记忆/溢出各有产品实现，租约只在多进程共享库时才有意义。
- **风险**：中高（表结构全变 + 数据迁移 + 检索回退），收益是"少维护 600 行"——对一个已经把 SQLite 跑通、测试齐全的模块不划算。

## 4. 决策

- **不替换**。`internal/modules/sessionstore` 继续作为唯一会话存储；`rig.SessionLog`/`History`/`CheckpointStore` 契约已经满足，SDK 侧不需要我们的库长什么样。
- **不引入** `session/sqlite` 依赖（避免无 tag 子模块与 sqlite 版本抬升）。
- **保留借鉴点**（未来可选、独立评估）：
  - 事件导出对齐 `events.Envelope`（给事件载荷加 `version`，`session_log_export.go` 形状对齐），使外部工具能用 `events.Replay` 读回——这是 PARITY §B.2 已记录的低成本对齐点，与本次决策无关。
  - 若将来需要"多进程共享同一库"，再评估 rig 的 `Lease`，而不是整体换后端。

## 5. 什么情况下重新评估

同时满足以下三条才值得重开：

1. rig `session/sqlite` 发布正式 tag 且进入根 module 的发布流程（依赖可预测）；
2. 上游检索改成结构化/解码列（或提供扩展点让本仓保留 `search_text` 质量）；
3. 产品出现"多进程/多设备共享同一会话库"或"需要 SDK 官方成本/租约账本"的真实需求。

在此之前，把它当作**可选后端参考**，不作为待办事项。
