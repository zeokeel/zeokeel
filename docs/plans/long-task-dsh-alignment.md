# 长时任务严格对齐 DSH：实施设计（v1）

> 日期：2026-11-14 · 状态：实施中 · 决策来源：用户对 `00-report.md` §4 的五条拍板
> 决策：① 采用 DSH 三件套（revision CAS + blocked + 256 轮）；② 续跑预算/重启语义对齐 DSH；
> ③ 自动化接受「已投递/已完成」两态；④ 审批对齐 DSH preset/无人值守显式选择；⑤ 投入常驻/通知。
> 其他：严格对齐 DSH；需要时改 `/data/zeokeel-rig`（本地以 `replace` 消费，版本待发布后移除 replace）。
>
> 实施状态：第一轮已完成 Goal v2、上下文安全默认、自动化 DSH 语义、通知/常驻基础（见
> `docs/research/long-task/04-implementation-status.md`）；余下 P1/P2 项见该文档 §4。

## 0. 目标与非目标

**目标**：ZeoKeel 的长任务生命周期与 DSH 对齐：目标 durable/按会话、续跑在引擎侧、默认上下文保护、
自动化原会话投递与两态结果、审批显式无人值守、完成/失败通知、常驻形态。

**本阶段非目标（后续任务）**：workflow/ralph、session 级不可变 fork、成本页、跨设备并发写锁。

## 1. 总体架构（目标态）

```
             contracts.GoalService (per-session durable goal + activation)
                      │
   ┌──────────────────┼───────────────────┐
   ▼                  ▼                   ▼
goal backend 模块    agent 续跑驱动       hosttools DSH 三件套
(事件源 goal/change) (run settle → 续跑)  (get_goal/create_goal/update_goal
   │                  │                   + revision CAS/blocked/256)
   ▼                  ▼                   ▼
sessionstore.events  agent.run 生命周期    rig.GoalRevisionStore 适配器
                      │
                      ▼
              UI goal dock / 命令 / headless / ACP
```

**事实源**：`sessionstore` 的 events 表。每次目标变更 append 一条 `goal/change` 事件（完整快照，
revision 单调递增）；启动/首次访问时按 session 重放最后一条有效事件。目标缓存常驻；不新增数据库表。

**activation（自动续跑是否 armed）**：进程内状态，不持久化。启动后所有目标为 `disarmed`；
`create_goal` 成功、用户 `resume` 成功、新目标替换后 `armed`；`pause`/`complete`/`blocked`/
用户 Stop/重启后 `disarmed`。UI 显示 `armed/disarmed` 与「继续目标」按钮。

## 2. 契约（冻结）

`internal/contracts/goal.go` 重写为：

```go
type GoalPhase string   // active | paused | blocked | complete
type GoalActivation string // armed | disarmed
type GoalBlockedReason struct{ Code, Message string }
type Goal struct {
    SessionID, ID string
    Revision int
    Objective string
    Phase GoalPhase
    RoundsStarted, MaxGoalRounds int
    BlockedReason *GoalBlockedReason
    Activation GoalActivation
    UpdatedAt time.Time
}
type GoalRef struct{ ID string; Revision int }
type GoalAction string   // edit | pause | resume | complete | blocked
type GoalUpdate struct {
    Action GoalAction
    Objective string
    MaxGoalRounds int
    BlockedReason string
}
type GoalService interface {
    Goal(sessionID string) (Goal, bool)
    CreateGoal(ctx context.Context, sessionID, objective string, maxGoalRounds int) (Goal, error)
    UpdateGoal(ctx context.Context, sessionID string, ref GoalRef, update GoalUpdate) (Goal, error)
    ClearGoal(ctx context.Context, sessionID string, ref GoalRef) (bool, error) // CAS tombstone；无 goal→(false,nil)
    AdmitRound(ctx context.Context, sessionID string) (Goal, error) // CAS；cap 处写 blocked(round-limit)
    Disarm(sessionID string) (Goal, bool)
    Subscribe(fn func(sessionID string)) (unsub func(), err error)
}
var KeyGoal = cordis.NewServiceKey[GoalService]("zeokeel/goal")
```

错误：`ErrGoalNotFound`、`ErrGoalStaleRef`、`ErrGoalInvalidTransition`、`ErrGoalRoundLimit`、
`ErrGoalConflict`（已有未完成目标）、`ErrGoalDirectHumanRequired`。

## 3. DSH 语义对齐点

| DSH | 本实现 |
|---|---|
| `defaultMaxGoalRounds=256`，create 可覆盖 | `CreateGoal(..., 0)` → 256；正数直用 |
| 四相 active/paused/blocked/complete | 同名 phase；`complete` 落 `complete` |
| CAS `{id,revision}` | `UpdateGoal`/`AdmitRound` 校验 revision，stale 报错不覆盖 |
| blocked 需具体原因；自治轮次需 ≥3 轮 | `blocked` 必填 reason；来自 goal-round 的 blocked 要求 `RoundsStarted >= 3`（由 rig 适配器/驱动用 ctx 来源判定） |
| create/edit/pause/resume 需直接人类 turn | 产品以 run origin 判定：`user` 才允许；`goal`/`automation`/`subagent` 拒绝 |
| 模型不能 resume paused（用户才能） | 同上；UI/命令直接调 `GoalService.UpdateGoal`（human 路径） |
| 轮次耗尽自动 block `round-limit` | `AdmitRound` 在 cap 处写 blocked(code=round-limit) 并返回 `ErrGoalRoundLimit` |
| resume/fork/重启后 active disarmed | 启动 load 后 disarmed；fork 复制目标并 disarmed（后续） |
| clear 写 revision tombstone | `ClearGoal`：CAS 校验后 append cleared tombstone，replay 视为无 goal，之后可 create 新实例 |
| goal-round prompt 含 objective/round/cap | 驱动 prompt：`Continue working toward the active goal ... Round n/cap ...`，消息源标记 goal-round |

## 4. 续跑驱动（agent 后端）

在 `internal/modules/agent` 的 run settle 后执行（不依赖 UI）：

1. 仅当本次 run 正常成功（无 error、非 cancel、非 ctx 超时）才考虑续跑；失败/取消不续跑。
2. 读 `GoalService.Goal(sessionID)`：`phase==active`、`activation==armed`、`rounds<max`。
3. 防抢占：若 session 已在 running（UI 排队消息已抢先启动）或用户已 Stop，则跳过。
4. `AdmitRound`（durable 自增）；`ErrGoalRoundLimit` → 已写 blocked(round-limit)，UI 提示。
5. 以 origin=goal 启动新 run，prompt 带 goal 文本 + Round n/cap；用户输入优先——排队已在后端（见 §4 末的完成注记）。
6. 重启后 disarmed，不自动续跑；UI 提供「继续目标」。

> **已完成后端化（DSH inbox 语义）**：排队不再存在 UI 的 moduleState 里。契约是
> `contracts/promptqueue.go`（`contracts.PromptQueue` / `contracts.QueuedPrompt`），实现是
> `internal/modules/agent/promptqueue.go`：用户 prompt 进会话的 durable 收件箱，run settle 后先排空
> 一条用户消息、再考虑 goal 自动续跑（人优先于 goal），重启后队列仍在。网关把它读成
> `prompts:<sessionID>` 域（快照 + 差值 + `prompts.enqueue`/`prompts.remove`），客户端没有本地副本。
> 因此原先"settle 后加短暂宽限并检查 running"的临时取舍已删除。

## 5. 上下文安全默认（agent 模块）

- 有效预算：设置 `chat.contextBudget` > 0 时用设置；否则模型 `ContextWindow > 0` 时推导
  `budget = min(0.8*W, W - MaxTokens - 65536)`（下限保护，非法则 0.8W）。
- `Compactor` 在有效预算 > 0 时安装；`CompactNow` 同样用有效预算，不再要求手工设置。
- 溢出恢复（下一小步）：run 以 context 错误失败后，自动压缩一次并重试一次（标记 run 内部，不暴露给模型为两次回答）。
- 设置页文案更新：留空 = 按模型窗口自动推导；0 = 关闭。

## 6. 自动化 / schedule（DSH 语义）

- 任务增加 `SessionID`（首次 run 创建后持久化）；默认后续 run 投递到**同一会话**（可配置新会话）。
- 运行记录两态：`deliveredAt`（Send 接受）与 `completedAt/status`（run 结算）分离；
  automation 订阅 ChatService，run 结束回写 completed/failed/interrupted + 时长/错误。
- 选择器补 `weekly`、5 字段 `cron`；保留现有表达式。
- 完成/失败通知（见 §7）；清理策略：保留最近 N 个 run 会话或按天。

## 7. 审批 / 通知 / 常驻

- 审批：对齐 DSH preset 语义（sandbox mode × approval policy）；无人值守 = 显式选择
  「所有权限」/任务级 never；默认保持会停下来等人（不自动超时拒绝）。补 UI 说明与 fail-closed 测试。
- 通知：`internal/host` 用 mygo `mygo.NewNotification`（`NotificationsSupported`）在
  run 完成/失败、自动化完成/失败、审批等待时发系统通知；应用内 toast 保留。设置里可关。
- 常驻：托盘 close-to-tray 提供显式「关闭后继续运行」设置（默认保持 DSH 式：关窗退出要提示）；
  退出/更新前检测运行中 run/goal/job 并确认。

## 8. 工作流与写作用域

| 任务 | 所有者 | 写范围 | 依赖 |
|---|---|---|---|
| T4 Goal 后端 + 上下文默认 | goal-backend | contracts/goal.go、modules/goal/**、modules/agent/**、modules/hosttools/**、hosttools/**、assembly/** | rig 接口已改 |
| T5 Goal UI | goal-ui | ui/session/**、uiroot/**、ui/uikit/** tool names、相关测试/快照 | T4 契约冻结 |
| T6 自动化 DSH | automation-dsh | modules/automation/**、contracts/automation.go、ui/automation/**、schedule 测试 | 无 |
| T7 通知/常驻 | notify-runtime | host/**、uiroot/notifier*、ui/toasts/**、ui/settings/notifications*、tray | 无（用 contracts.Notifier） |
| 集成/验证 | lead | go.mod、docs、跨任务收口 | 全部 |

共享文件冲突规则：agents 不并行改同一文件；`internal/modules/agent/agent.go` 只归 T4；
`internal/ui/session/session.go` 只归 T5；自动化 UI 只归 T6；host/tray 只归 T7。

## 9. 验收（每阶段）

- `go build ./...`、`go vet ./...`、相关包 `go test -count=1`。
- Goal：新建/编辑/暂停/恢复/完成/blocked、CAS stale、256 cap、round-limit block、重启后 disarmed、
  切会话不串味、headless/ACP 续跑、UI dock 轮次/停止原因。
- 上下文：默认预算推导、自动压缩触发、`/compact` 在未设预算时可用。
- 自动化：delivered vs completed、原会话续跑、weekly/cron、重启补跑。
- 通知：run 完成/失败/审批等待触发 OS 通知（平台支持时）；设置可关。
- UI 改动：帧测试 + `ui-snapshot` PNG 人工检查 + designlint/design-check（跑 AGENTS.md 规定的检查）。
