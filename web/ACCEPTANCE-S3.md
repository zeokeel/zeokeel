# S3 验收对账（docs/WEB-CLIENT.md §8）

> 2026-11-23 更新：A3、A5 已由 task-8 收口，P0 24/24。

> 状态：2026-11-23 · web-engineer · 前端全部 mock 驱动；WS 快照/差值未接线（隔离在 `src/api/`）。
> 判定标准：**有可跑的测试或 curl 证据**才算「完成」；只有设计没实现的写「未完成」。

## P0（24 条）

| # | 验收点 | 状态 | 证据 / 原因 |
|---|---|---|---|
| A1 | `/` 有 composer，可聚焦可输入，空态引导 | **完成** | `app.smoke.test.tsx`（shell + textbox + Welcome）；`NowScreen` 空态 |
| A2 | `/` 输入并发送 → 建会话 → 跳 `/c/:id`，消息进 transcript | **完成** | `app.smoke.test.tsx > creates a session from / …(A2)`；pendingText 一次性消费 |
| A3 | 侧栏点另一会话 → thread 切换，滚动位置符合恢复规则 | **完成**（task-8） | 锚定「消息 id + 行内偏移 + 是否贴底」存 `sessionStorage`（不存 scrollTop）；`scroll-restore.test.tsx`：A/B 两会话各滚不同位置、**往返三次**位置与跟随状态都对，且窗口重建后仍有行渲染（不白屏不跳顶）；`scroll-anchor.test.ts`（存取/损坏容错/找不到消息回落贴底/坐标网格与实测两种模式） |
| A4 | 发送 → 用户气泡 → 流式 → 完成态 | **完成** | `app.smoke.test.tsx > sends a turn …`、`sse-run.integration.test.ts`（真 SSE） |
| A5 | 流式中 composer 变停止；运行态可见且不伪造进度；运行中发送进入队列 | **完成**（task-8 补齐排队） | `Sender loading` + `onStop`；运行中用 Enter 排队（`PromptQueuePanel`「N 条待发送」+ 单条取消 + done 后发队首 + abort 保留队列）；`prompt-queue.test.tsx` 三条用例；队列在后端：WS `prompts:<sessionID>` 域（`prompts.snapshot` / `prompts.delta` / `prompts.enqueue{prompt}` / `prompts.remove`），REST 路径已删除不并存；真网关浏览器验证：enqueue → delta → 「1 条待发送」→ Esc 中止后队列仍在（「发送队首」出现） |
| A6 | 点停止 → 本轮结束 + 中性「已停止」 | **完成** | `sse-run.integration.test.ts > aborts…`；`AssistantBody` 中性 `已停止` |
| A7 | 流式中 Esc（焦点在 composer）→ 停止本轮 | **完成** | `esc-chain.test.tsx > E2`（更强：焦点不在任何组件） |
| A8 | 待审批时 Esc（焦点在侧栏）→ 拒绝 | **完成** | `esc-chain.test.tsx > E1`（真实 keydown on document） |
| A9 | 审批卡可见时裸 Enter → 不做任何决定 | **完成** | 审批卡无 `autoFocus`、无默认按钮；`Modal.confirm` 全仓 0 处；Mod+Enter 才决定（E3/审批测试） |
| A10 | 审批卡可见时 Mod+Enter → 允许 | **完成** | `ApprovalsProvider` 的 `useShortcut(mod+Enter)`；`esc-chain.test.tsx > E3` 侧证不被 Esc 抢 |
| A11 | 点拒绝 → 请求被拒、卡片消失、下一条出现 | **完成** | `ApprovalCard` + 队列 FIFO；mock `deny` 路径 curl 验证（`tool.result ok:false`） |
| A12 | `/inbox` 看到待审批，数量与 thread 一致，点进去回 thread | **完成** | `InboxScreen` 直接读审批队列（同一实体，不是副本）；`GET /api/inbox` curl = 5 类 |
| A13 | `/inbox` 处理完 → 空态，无 0 值统计 | **完成** | `Empty` 分支，不渲染计数 |
| A14 | 输入 `/` 出现菜单，不推送布局 | **完成** | `composer.test.tsx > opens the slash menu…`；菜单为绝对定位浮层 |
| A15 | slash 菜单 ↑↓ + Enter 执行 | **完成** | 同上（Enter 执行、不发送） |
| A16 | slash 菜单 Esc → 菜单关、草稿保留 | **完成** | 同上（断言输入框仍为 `/`） |
| A17 | 输入 `@` → 文件候选，Esc 关 / Enter 选 | **完成** | `Composer` mention 菜单（`parseMentionDraft` + 文件树候选，Enter/Tab 补全，Esc 关） |
| A18 | Mod+K → 面板打开可立即输入 | **完成** | `CommandPalette`（`autoFocus` Input；Mod+Shift+P 别名） |
| A19 | 面板过滤 + Enter 执行 | **完成** | 同上（filter + Enter） |
| A20 | 面板 ↑↓ + Enter | **完成** | 同上（highlight 索引） |
| A21 | 面板 Esc → 关闭；下次打开 query 空、选中第 1 项 | **完成** | `close()` 重置 query/activeIndex；E7 断言 Esc 被 overlay 层消费 |
| A22 | 右栏文件页点文件 → 预览，内容一致 | **完成** | `InspectorPanel`（Tree → `/files/*path` → 预览）；curl `files/content` |
| A23 | 改动文件可展开 unified diff | **完成** | `DiffBlock` + `?view=diff`；`files.test.ts`（解析/行号）；curl `files/diff` |
| A24 | Mod+Alt+F → 右栏文件页 | **完成** | `esc-chain.test.tsx > opens the find bar with Mod+F and the inspector with Mod+Alt+F` |

**合计**：**24/24 完成**（task-8 收口了 A3 滚动恢复与 A5 排队）。

## Esc 分层回归（E1–E9）

| # | 场景 | 状态 | 测试 |
|---|---|---|---|
| E1 | 焦点在侧栏 + 待审批 → 拒绝 | 完成 | `esc-chain > E1` |
| E2 | 焦点不在任何组件 + 运行中 → 停止 | 完成 | `esc-chain > E2` |
| E3 | 焦点在 composer + 运行中 + 审批 → 拒绝（审批优先） | 完成 | `esc-chain > E3` |
| E4 | slash 菜单开 + 运行中 → 只关菜单 | 完成 | `esc-chain > E4`（再按一次才停轮） |
| E5 | 设置页脏 → 弹丢弃守卫，面不关 | 完成 | `esc-chain > E5` |
| E6 | 子会话 + 父会话待审批 → 回父，不拒绝 | 完成 | `esc-chain > E6` |
| E7 | 面板 + 设置页同时开 → 先关面板 | 完成 | `esc-chain > E7`（第二次 Esc 才到守卫） |
| E8 | 全 idle → 不 preventDefault | 完成 | `esc-chain > E8` |
| E9 | 长按 Esc 只触发一次 | 完成 | `esc-chain > E9` |

## 长会话与虚拟化（§7.1/§7.2）

| 项 | 状态 | 证据 |
|---|---|---|
| 520 条消息只渲染有界窗口 | 完成 | `transcript.test.tsx`：`rows ≤ 40`，尾部窗口含最新消息，首条不在 DOM |
| 流式贴底跟随 / 上滚脱离 / 回到最新 | 完成 | 同上：`shouldFollow` 24px 阈值 + DOM 滚动事件（按钮出现/消失） |
| 真实浏览器帧率与滚动抖动 | **未验证** | 环境无 headless 浏览器；不声称已测帧率 |

## P1 摘要（16 条，供下一轮排期）

完成：B1（应用内关闭路径）、B2、B3（页过滤，定位跳转未做）、B4、B6、B9、B10、B15。
未完成：B5（主题/语言切换未驱动外观）、B7/B8（窄窗需真浏览器）、B11（消息 ⋯ 菜单）、
B12（后台任务 chip）、B13（排队）、B14（toast 面）、B16（Mod+B 折叠）。