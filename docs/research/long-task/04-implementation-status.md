# 04 · 长时任务 DSH 对齐：第一轮实施状态

> 日期：2026-11-14 · 类型：实施记录（对应 `00-report.md` 的 P0 项）
> 设计：`docs/plans/long-task-dsh-alignment.md` · 差距：`03-gap-matrix.md`
> 独立审计：[05-final-parity-audit.md](05-final-parity-audit.md)（第二轮复审计：六条 5 PASS + ⑥ 可接受 PARTIAL，objective 可 complete）。
> 状态：**第一轮**落地 Goal v2 / 上下文安全默认 / 自动化 DSH 语义 / 通知与常驻；
> **第二轮（goal round 1）**落地后端 PromptQueue（DSH inbox）、GoalRound/中断 restore 回填、headless 审批 fail-closed；其余 P1/P2 见 §4。

---

## 1. 已落地（本轮）

| 报告项 | 实现 | 关键文件 | 验证 |
|---|---|---|---|
| P0-1/P0-2 Goal v2（DSH 三件套、durable、引擎续跑） | 会话级 `GoalService`（`goal/change` 事件源、revision CAS、active/paused/blocked/complete、默认 256 轮、`round-limit` blocked、clear tombstone、重启 disarmed、删除会话防复活）；模型工具 `get_goal/create_goal/update_goal`（人类 turn 限制、blocked ≥3 轮）；agent 后端 run settle 后 `AdmitRound` + goal-round prompt（失败/取消/上限不续，Stop disarm）；headless/ACP 同构；UI dock/命令/回执迁移，旧宿主单例删除 | `internal/contracts/goal.go`、`internal/modules/goal/`、`internal/modules/agent/goal_continue.go`、`internal/modules/hosttools/`、`internal/hosttools/goalrevision.go`、`internal/ui/session/goal.go`、`internal/uiroot/` | goal/agent/hosttools/ui/session/uimodtest/regression 单测 + `-race`；§3 |
| P0-3 上下文安全默认 | `contextBudget` 未设置时按模型窗口推导 `min(0.8W, W-MaxTokens-65536)`；>0 时 run/subagent/team 默认装 LLMCompactor；`/compact` 用有效预算；显式 0 仍是关闭 | `internal/modules/agent/context_budget.go`、`compact_now.go` | `context_budget_test.go`、`TestCompactNow*` |
| P0-4/P0-5 自动化/无人值守 | 任务绑定 `SessionID`（默认同会话续跑，可切每次新建）；run 记录 delivered 与 completed/failed/interrupted 两态（时长/错误/会话链接），启动补结算；schedule 支持 daily/weekly/cron/at + 时区；结算通知；审批挂起通知（本地化，不泄漏 raw tool id）；关窗/退出前运行中工作确认 | `internal/contracts/automation.go`、`internal/modules/automation/`、`internal/ui/automation/`、`internal/contracts/notify.go`、`internal/host/close_guard.go`、`internal/uiroot/running.go` | automation/contracts/regression/UI 测试；§3 |
| P1-6 通知 | OS 通知（mygo）+ 应用内 toast 双通道；新「系统通知」设置（默认开，不支持时禁用说明）；run/自动化/审批等待触发 | `internal/uiroot/notifier.go`、`internal/host/notify.go`、`internal/ui/settings/notifications.go` | notifier/host/settings 单测 + designlint/快照 |
| 常驻（部分） | close-to-tray 说明与「关闭窗口继续运行」语义；运行中工作存在时关窗/Cmd+Q/托盘退出/更新重启先确认；托盘不可用回落退出+确认 | `internal/host/close_guard.go`、`internal/host/tray.go`、`internal/ui/settings` | close_guard 测试 |
| **第二轮：后端 PromptQueue（DSH inbox）** | 运行中发送进入会话 durable inbox（`queue/change` 快照事件），settle 先 drain 用户消息再续 goal；删除 200ms 宽限；Stop 保留、重启恢复并自动 drain idle 队列；UI 排队改读后端（顺序/时间/移除/清空） | `internal/contracts/promptqueue.go`、`internal/modules/agent/promptqueue.go`、`internal/ui/session/session.go`、`internal/ui/session/view.go` | agent/UI/regression/uimodtest + `-race` |
| **第二轮：恢复语义** | restore 从 durable `user_message.goalRound` 回填 `ChatMessage.GoalRound`；checkpoint 未完成 → `ChatSession.Interrupted`（下一次 run 清零） | `internal/modules/agent/restore_metadata.go`、`internal/contracts/chat.go` | agent restore 测试 |
| **第二轮：无人值守审批** | headless/ACP pin `ignorePermissionSetting`，桌面保存的权限预设不能把无人值守 auto 变成永久等待审批 | `internal/modules/approvals/approvals.go`、`internal/assembly/assemblies/headless.json` | approvals/assembly 测试 |
| **第三轮：编排对齐** | 注册 rig `workflow`/`ralph` + dynamic orchestration runner（depth 1/并发 8/递归排除）；委派预算对齐主 run；guidance/审批分类/toolcards；sync subagent 保留并在 guidance 推荐异步 team | `internal/modules/agent/orchestration.go`、`subagent.go`、`team.go`、`system_prompt.go`、`internal/uikit/approval.go` | agent/assembly/uikit/toolcards/plugins 测试 + `-race` 关键用例 |
| **第三轮：中断提示 UI** | restore 未完成 checkpoint → `ChatSession.Interrupted` → 会话页中性提示 +「继续（聚焦 composer）/忽略（仅本次）」；不自动重发 | `internal/ui/session/interrupted.go`、`internal/regression/interrupted_test.go`、`internal/ui/uimodtest` | 帧测试/regression/真实后端 E2E + snapshot `p2-interrupted{,-dark}.png` |
| **第四轮：观测性 P0 数据基座** | 逐模型调用 `model_call` 事件；手动 compact 事件 + 自动压缩 inferred 近似；spill 事件；run 前 context_snapshot（system prompt/tools/history/injected，sections 预留）；`contracts.ContextInspector`（KeyContext）+ agent 折叠/重启重建；模型价格与 per-request/总成本（未知价不伪造） | `internal/contracts/contextinspect.go`、`internal/modules/agent/observability.go`、`compact_now.go`、`restore_metadata.go`、`internal/contracts/modelcatalog.go`、`internal/modules/modelcatalog/` | agent/contracts/modelcatalog/assembly 单测 + `-race`；UI Context tab 待 task-15 |
| **第四轮：rig v0.17.0** | 本地同步 v0.16.2 内容后发布：`GoalRevisionStore.CurrentGoal(ctx)`、`Options.GoalRevision`、GoalRoundDriver 256；产品 require v0.17.0 + 临时 replace | `/data/zeokeel-rig` commit/tag `v0.17.0`、`go.mod` | rig hosttools/agent 测试 + sync-version OK |
| **第五轮：Fork/goal 复制 + Stop inbox** | fork 复制完整 durable history（含 tool call/result）；源 goal 复制为新 ID/revision 1/disarmed（phase/rounds/blocked 保留）；Stop 后队列不自动跑，切回/Send/Enqueue 按序 drain 旧队首，goal 不续 | `internal/modules/agent/{agent.go,promptqueue.go}`、`internal/modules/goal/service.go` | agent/goal/contracts 单测 + `-race` |
| **第五轮：Context tab** | 会话第 4 个 tab：概览/逐调用表/事件/成本；无后端隐藏；空态诚实；AGENT-SURFACES S10 | `internal/ui/session/{context_pane.go,view.go,session.go}`、`internal/devtools` | 帧测试/regression/uimodtest 真实后端 E2E + snapshot `p2-context{,-dark}.png` |
| **第六轮：cold resume** | 历史 teammate/子代理重启后显式发送即冷恢复（child session 作 history、tool 写回 durable、记录/overlay/读模型更新）；`Resumable` 字段；绝不自动恢复 | `internal/modules/agent/cold_resume.go`、childagents/team | 连续 resume/重启/overlay/安全错误测试 + `-race` |
| **第六轮：通知可达** | `SessionNotifier` + 通知 ID 编码 sessionID；点击置前并打开会话；后台 run 完成/失败通知；automation/审批带 session | `internal/uiroot/notifier.go`、`internal/host/notification_click.go`、`internal/modules/agent/run_notify.go`、automation | notifier/host/agent/automation 测试 |
| **第六轮：上下文恢复/分段** | context 超限自动压缩+重试一次（不重复 user/工具），durable `context_recovery` + timeline；system prompt 12 sections 写入 snapshot（字节级兼容） | `internal/modules/agent/context_recovery.go`、`prompt_sections.go`、`system_prompt.go`、observability | fake provider 溢出/工具不重放/字节级 oracle + `-race` |
| **第六轮：价格 UI** | 设置→模型编辑输入/缓存/输出价格（可选），校验/回显/清除；Context tab 成本可见/隐藏；AGENT-SURFACES 价格规则 | `internal/ui/settings/modelcatalog*`、`internal/ui/session/context_pane.go`、devtools | 帧测试 + uimodtest 真实后端 + snapshot `p2-model-prices{,-dark}.png` |
| **第七轮：Insights 洞察页** | 新后端模块 + UI 一级入口：7/30 天 tokens/成本/会话/工具/模型/工作区聚合（model_call 主源、旧 usage 回退不重复；有界读取+缓存+截断明示；未知价只计 UnpricedCalls）；Table/List/KPI，无自绘图表；点击会话打开 | `internal/contracts/insights.go`、`internal/modules/insights/`、`internal/ui/insights/`、`internal/assembly/`、`internal/ui/sidebar/` | 模块聚合/日期/无价/空/缓存单测 + UI 帧测试 + uimodtest E2E + snapshot `p2-insights{,-dark}.png` |
| **第七轮：冷恢复 UI** | 历史 teammate/子代理 Resumable 行「历史 · 可继续」+「继续」；子会话冷恢复输入框（发送/已发送等待/最新回复/错误本地化）；只读历史不变 | `internal/ui/session/agentsurface.go`、`internal/devtools` | 帧测试 7 项 + uimodtest 真实重启 E2E + snapshot `p2-cold-resume*.png` |
| **第八轮：压缩审计 archive** | 手动 /compact 替换前 durable `compact_archive`（完整 rig messages；2MiB/200 条上限/truncated 元数据）；失败 fail-closed 保留历史；溢出恢复同样归档；ContextInspector 只暴露元数据，重启可审计 | `internal/modules/agent/compact_archive.go`、`compact_now.go`、`context_recovery.go`、`observability.go`、contracts | archive/失败/双上限/重启测试 + `-race` |
| **第十一轮：Windows 沙盒并发写修复** | rig v0.16.3（`0515026`，tag 已推送 GitHub、goproxy.cn 未索引）：跨进程命名 mutex + per-grant marker + per-workspace 状态文件；并发 grant 各自加/撤自己的 capability ACE，最后一个恢复原始 descriptor；崩溃 grant 自愈；产品以内树 fork `third_party/zeokeel-rig` + replace 消费，保持干净构建；Win32 行为待 Windows 实机验证 | `/data/zeokeel-rig`、`third_party/zeokeel-rig`、`go.mod` | rig 纯状态机 9 测试 + Windows 交叉 build/vet + 产品全量 go test + Windows 交叉 build |
| **第十轮：执行域持久化** | 所有 `(root,mode,sandbox)` build 保留到模块 unload（切工作区/权限/沙盒重建不再 CloseAll）；job_*/terminal_* 按 ID 路由 owner build（新任务用当前策略），job_list/terminal_list 合并同 root builds，通知从所有 builds drain；durable execution 生命周期事件（审计，不含输出；跨 app 重启不重连） | `internal/modules/toolkit/execution_routing.go`、`rebind.go`、`contracts/execution.go` | 新增 9 个路由/生命周期测试 + 旧测试新语义 + `-race` + 全量 go test |
| **第九轮：W1 干净构建** | 产品回退已发布 rig v0.16.2（无 replace），DSH goal 三件套产品自实现（`goaltools.go`），模型面 schema 不变；干净目录构建通过 | `internal/hosttools/goaltools.go`、`internal/modules/hosttools/`、`go.mod/go.sum` | clean dir `go build ./...` + goal/agent/hosttools/assembly 测试 |
| **第八轮：Context sections/recovery/archive UI** | 系统提示分段表（本地化/来源/字节/Tokens/合计/占比；空隐藏）+ recovery/archive 事件行 + `/compact` 归档失败文案；AGENT-SURFACES S10 | `internal/ui/session/context_pane.go`、devtools、regression、uimodtest | 帧测试/regression/uimodtest + snapshot `p2-context-sections.png` |
| rig 支撑（历史，已被第九轮回退） | 曾用 `hosttools.Options.GoalRevision` + `GoalRevisionStore.CurrentGoal(ctx)` + `GoalRoundDriver` 256 消费本地 rig v0.17.0；第九轮已回到**已发布 v0.16.2（无 replace）**，DSH goal 三件套由产品 `internal/hosttools/goaltools.go` 自实现 | `/data/zeokeel-rig`（本地候选 tag `v0.17.0`，未发布/不消费） | 干净目录 `go build ./...` + goal/agent/hosttools/assembly 测试 |

**最新集成验证**（Lead 在最终工作区重跑）：
- `gofmt -l` 空；`go build ./...`、`go vet ./...`、`CGO_ENABLED=0 go build`、`go tool mygo vet .` 通过。
- `go test ./... -count=1` 全绿（含 insights/insights-ui、agent、regression、uimodtest、settings）。
- `designlint --strict --baseline` 0 error/0 warn/0 new；`design-check.sh` 6/6。
- `ui-drive` 全量 + `ui-snapshot`（含 `p2-insights{,-dark}.png`、`p2-cold-resume*.png`、`p2-context{,-dark}.png`、`p2-interrupted{,-dark}.png`）人工复核。

**第二轮集成验证**（保留记录）：
- `gofmt -l` 空；`go build ./...`、`go vet ./...` 通过；`go test ./... -count=1` 全绿（含 ui/session 11s、regression 21.6s、settings 40s）。
- `designlint --strict --baseline` 0 error/0 warn/0 new；`design-check.sh` 6/6；`go tool mygo vet .` 干净。
- （历史记录，当时 `ui-snapshot` 111 PNG）`ui-drive` 全量 93/93 PASS（含 queue-popover/goal 步骤）；人工复核 `p2-inbox{,-popover}.png` 与 goal 帧。

**第一轮集成验证**（保留记录）：
- `gofmt -l internal cmd` 空；`go build ./...`、`go vet ./...`、`go tool mygo vet .` 通过。
- `go test ./... -count=1` 全绿（43+ 包，含 ui/session 11s、settings 40s、regression 21.6s）。
- `go test -race ./internal/modules/goal ./internal/modules/agent -run 'TestGoal|TestCancel|TestClearGoal|TestForget|TestDeleteSessionForget|TestAdmitRound'` 通过。
- `designlint --strict --baseline`：0 error/0 warn/0 new；`scripts/design-check.sh` 6/6 PASS。
- `ui-snapshot` 全量 109 PNG；`ui-drive -only session-narrow-header,overlay-goal` 2/2 PASS；目标 dock 亮/暗/窄窗/暂停/受阻帧人工复核。

---

## 2. 关键语义（已对齐 DSH）

- **目标归属**：每会话一个 goal；CAS `{id, revision}`；跨会话不再串味；重启保留 phase/rounds/blocked，activation 一律 disarmed，人类 resume 才续跑。
- **续跑**：只在 run 正常成功且 origin∈{user,goal} 时；顺序是「用户排队消息优先（200ms 宽限 + running 检查）→ AdmitRound → 新 run（origin=goal）」；失败/取消/暂停/阻塞/上限停；上限写 `blocked(code=round-limit)` 不重试。
- **工具权限**：create/edit/pause/resume 仅直接人类 turn；模型不能 resume paused；goal-round 的 blocked 需 ≥3 轮且带原因；complete 可在 goal-round 内完成。
- **持久化**：`goal/change` 完整快照事件，读失败 fail-closed，删除会话后 goal 事件不会复活 session。
- **自动化**：同会话续跑默认开；delivered 与 completed 分离；旧 JSON 兼容；旧 `M H * * *`/`@daily` 改按 Asia/Shanghai 墙钟（行为变化，CHANGELOG 已注明）。
- **通知**：只有高信号事件走 OS（run 完成/失败、自动化结算、审批等待）；普通 toast 不刷系统通知中心。

---

## 3. 已知限制与产品边界（2026-11-15 最新）

1. **自动压缩事件**：run 前主压缩已 exact（`inferred:false` + auto archive，精确 before/after/replaced/saved，失败记 Outcome）；run 中随增长触发的 rig 内压缩仍只能 `inferred:true` 近似，不伪造精确值。
2. **压缩原件读取**：`compact_archive` 正文只在 durable 事件；Context tab 只展示归档元数据，查看原文需后续只读分页 API。
3. **cold resume 控制面**：历史 teammate 只有「问一次答一次」（无 live Wait/Interrupt/Close）；父会话回执经下一次 run 的 completion receipt；pre-feature 记录首次只 seed prompt+最后答案；TTL prune 不清理 durable child 行。
4. **执行域边界**：job/terminal 在同一应用进程内跨工作区/权限/沙盒重建保持存活（按 ID 跨 build 路由）；进程退出/崩溃后无法重连，durable execution 事件只供审计且不含完整输出（缺 finish = interrupted）；未做 systemd/launchd 服务，托盘不可用时关窗仍确认退出。
5. **Insights 窗口**：每会话只读最近 4000 事件（截断明示），首次加载后台，无跨进程预聚合投影表。
6. **Stop 后 inbox**：只在人类手势（切回/Send/Enqueue）时 drain，无后台定时器；`ChatSession.Interrupted` 的继续只聚焦 composer，不自动重发。
7. **审批 fail-closed**：agent gate 在无 approvals 服务时已 fail-closed（deny + 可读错误）；无 approvals 时提示词策略为 workspace。shipped 桌面/headless(auto+pin)/ACP 不受影响；rig v0.16.2 的 gate error 会中止整个 run（非单工具拒绝），仅自定义 embedder 可达。
8. **编排**：workflow/ralph 已注册、委派预算已对齐；sync subagent 仍阻塞当前 run（长任务建议异步 team）；ralph 为实验性、默认暴露但 guidance 限定显式请求。
9. **rig**：产品使用 **v0.16.3 内树 fork**（`third_party/zeokeel-rig` + replace；Windows sandbox 并发修复，tag 已推送 GitHub 但 goproxy.cn 未索引），干净 checkout 可构建；DSH goal 三件套由产品 `internal/hosttools/goaltools.go` 自实现；本地 rig v0.17.0 候选改动保留 tag、不发布、不消费。Win32 并发 ACL 行为待 Windows 实机验证。
10. **平台**：OS 通知 macOS 需打包应用、无 dbus 环境降级；通知点击在会话已删时只置前。

---

## 4. 下一轮建议（按 DSH 对齐余量排序）

1. **W1 干净构建（已完成）**：产品回到已发布 rig v0.16.2、删除 `replace`，DSH goal 三件套产品自实现；干净目录（无 .git/.cache/sibling）`go build ./...` + 目标测试通过。
2. **W2 文档纠偏（已完成）**：CHANGELOG/04 状态与实现一致。
3. **自动压缩精确事件**：可在产品侧做「run 前预算检查 + durable 预压缩」，减少/消除 inferred；in-run rig 压缩保留近似。
4. **执行域持久化（已完成第十轮）**：job/terminal 跨工作区/权限/沙盒重建稳定持有 + ID 路由 + durable 生命周期事件；跨 app 重启重连为明确产品边界。
5. **cold resume 控制面**：历史 teammate 的 Wait/Interrupt/Close（若需要）。
6. **审批 fail-closed 边界**：自定义 embedder 无 answerer 时 fail-closed（非 shipped 路径）。
7. **rig v0.17.0 发布**（可选，若希望产品重新消费框架改动）：推送 tag → bump → 移除产品自实现。
8. **最终达成度复核**：独立审计 + 修复 W1/W2 后标记目标完成。
