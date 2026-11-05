# ADR 0004：桌面通知（turn 完成/失败/等待审批，含后台会话）

- 日期：2026-11-05
- 状态：**已采纳**（桌面交互 P0-2；后台会话与后台审批为 P1 补全）
- 决策者：用户（ZeoKeel = 独立品牌桌面产品，交互按桌面惯例本地化）；记录：Lead / test-migrate-a
- 关联：[ADR 0003 桌面壳集成](0003-desktop-shell-integration.md)、[UI/UX 评估 §5.2/§7](../research/03-ux-design.md)、[mygo 通知 API](../../.cache/gomod/github.com/egoist/mygo@v0.7.4/modules.go)（`NotificationOptions`/`NewNotification`/`OnClick`/`NotificationsSupported`）
- 实施：`internal/app/notify.go`、`internal/app/notify_test.go`、`internal/app/app.go`（仅接线）、`internal/session/controller.go`（可选 `TurnFinishedHook` / `ApprovalRequestedHook`）、`internal/app/services.go`（未改）
- 更新 1：2026-11-05 P1——后台会话完成/失败通知（controller `SetOnTurnFinished` + 会话级去重/节流）。
- 更新 2：2026-11-05 P1 补全——后台"等待审批"通知（controller `SetOnApprovalRequested` + session+call 去重；快照审批路径在 hook 接线后让位，避免双发）。

## 1. 背景与目标

`docs/research/03-ux-design.md` §5.2 把「长任务完成有系统通知」列为 P0 桌面缺口：ZeoKeel 是长时间运行的桌面 Agent，用户提交任务后会切到别的应用；通知是用户知道任务结束或需要操作的唯一通道。

P0-2 首批（task-18）已实现当前会话的系统通知。P1 补充解决遗留：`Controller.OnUpdate` 只对 `currentID` 派发快照，用户在会话 A 启动长 turn 后切到会话 B（或新会话）时，A 的完成/失败/审批等待都不会到达通知分发器。

目标：

1. 当前会话与**后台会话**的 turn 完成/失败、以及**审批/提问等待**都通知；
2. 只在窗口**不活跃或隐藏**时通知；点击通知 → 显示/聚焦窗口并打开对应会话；
3. 中英双语、隐私安全（通知出现在锁屏与通知中心，不能带 prompt/工具输出/错误详情）；
4. 去重、节流、启动/重连不重放；
5. Notifier/事件源可注入，fake 覆盖全部矩阵，无显示器可测。

## 2. 决策

### 2.1 事件来源：两条路径

| 路径 | 覆盖 | 机制 |
| --- | --- | --- |
| 当前会话快照 | 未接 hook 时：审批等待 + turn 完成/失败（兼容旧接线与既有单测） | `Controller.OnUpdate` → `notificationDispatcher.Observe(*state.Snapshot)` 的相位状态机 |
| 全量 turn 终态 | 所有会话（当前 + 后台）的完成/失败 | `Controller.SetOnTurnFinished(TurnFinishedHook)` → `notificationDispatcher.observeTurnFinished(TurnOutcome)` |
| 全量审批请求 | 所有会话（当前 + 后台）的新增审批/提问 | `Controller.SetOnApprovalRequested(ApprovalRequestedHook)` → `notificationDispatcher.observeApprovalRequested(sessionID, request)` |

```text
turn goroutine (drain) ── forward ──┬─ state.ApprovalRequest → pending[c.sessionID][CallID]
                                   │      └─ ApprovalRequestedHook(sessionID, request)
                                   │             → dispatcher.observeApprovalRequested：审批通知
                                   │                （当前 + 后台；接 hook 后快照不再报审批）
                                   ├─ state.Status(PhaseIdle/Failed) → Broker/OnUpdate
                                   │      → dispatcher.Observe：仅审批通知（未接 hook 时）
                                   └─ finishTurn ── TurnFinishedHook(TurnOutcome)
                                          → dispatcher.observeTurnFinished
                                          → 完成/失败通知（当前 + 后台）
```

**为什么用 controller hook 而不是在 app 层订阅 Broker**：`state.Status`（相位终态）、`state.Error`、`state.ToolStart/ToolEnd` 等事件都不带 `SessionID`（只有 `Token`/`Reasoning` 带），Broker 又是多消费者的事件流；app 层无法可靠地把"某个会话的 Status 终态"归属到 session，也就无法按会话去重。因此在 `Controller` 增加**可选、默认 nil** 的终态 hook 是低侵入且语义准确的方案；未安装 hook 时 Controller 行为与之前完全一致。

分发器按会话记住"上一次相位"，只在**相位迁移**时判定事件（快照路径）：

| 迁移（上一次快照 → 本次快照） | 事件 | 文案（EN / ZH） | 额外条件 |
| --- | --- | --- | --- |
| `running → idle` | turn 完成 | Task complete / 任务完成 | 本轮有可读输出（assistant 文本/推理或工具调用存在）；只有用户 prompt 不算输出 |
| `running → failed`（或 `waiting_* → failed`） | turn 失败 | Task failed / 任务失败 | 终态由 `PhaseFailed` 判定；不读取 `LastError.Text` 进正文 |
| 任意 → `waiting_approval` / `waiting_user` | 等待审批/回答 | Approval needed / 需要确认 | 快照本身已有 `PhaseWaitingApproval`/`PhaseWaitingUser`；生产接线 approval hook 后本路径不再发（后台也由 hook 覆盖），仅作为未接 hook 时的回退 |
| 其他迁移（如 `idle → running`） | 无 | — | 只更新基线 |

**首次快照只记录基线，不发通知**：应用启动、会话恢复、重新打开会话时快照可能已经是 idle/failed/waiting 状态，不能把旧 turn 或旧审批当作新事件重放。hook 只报告**实时**事件，不重放历史。

### 2.2 后台会话通知（P1：controller hooks）

- `internal/session.Controller` 新增：

  ```go
  type TurnOutcome struct {
      SessionID string
      TurnID    string // 去重键的一半
      Failed    bool
      Stopped   bool   // 用户/关停取消，不算失败也不发完成
      HasOutput bool   // assistant 文本/推理或工具调用
  }
  type TurnFinishedHook func(TurnOutcome)
  func (c *Controller) SetOnTurnFinished(hook TurnFinishedHook) // nil 移除
  ```

- 调用时机：`finishTurn` 中、终态状态落库之后、`done` 关闭之前，**每 turn 恰好一次**，跑在 turn 的 drain goroutine 上（不改变现有事件顺序；hook 为 nil 时零开销）。
- `HasOutput`：turn 内出现过 Token/Reasoning（`t.order`）或 ToolStart（`t.toolStarted`）。
- `Stopped`：`t.stopped` 或 turn context 取消（与 goal driver 的判定一致）。
- 分发器收到 hook 后（`observeTurnFinished`）：
  - `Stopped` → 不发（用户主动取消不是完成/失败）；
  - `Failed` → 失败通知（即使没有可读输出）；
  - 完成且 `!HasOutput` → 不发（与快照路径的"有输出"规则一致）；
  - 其余 → 完成通知；点击回调携带 `SessionID` 打开对应会话。
- 双路径不重复：生产接线时 `notifier.useTurnFinishedHook()`。此后 turn 完成/失败一律由 hook 发出，快照路径不再发终态通知；当前会话的终态快照即使先于 hook 到达也会被跳过，不会出现同 turn 两条通知（`TestCurrentSessionTurnNotifiesOnce` 覆盖）。

**后台审批等待（P1 补全：controller ApprovalRequestedHook）**

- `internal/session.Controller` 新增：

  ```go
  type ApprovalRequestedHook func(sessionID string, request state.ApprovalRequest)
  func (c *Controller) SetOnApprovalRequested(hook ApprovalRequestedHook) // nil 移除
  ```

- 调用时机：`forward` 处理 `state.ApprovalRequest` 时，请求进入 `pending[sessionID][CallID]` 之后（`applyLocked` 返回"本次新增的审批"），在快照发布之后、且不在 `c.mu` 锁内调用；**每个 call id 恰好一次**（重复事件、决定/清除都不重复）。
- 审批决策（`Approve`/`Deny`）、pending 语义、`PendingApprovals` 均不变；无 hook 时行为完全不变。
- 分发器收到 hook 后（`observeApprovalRequested`）：
  - 只在窗口不活跃/隐藏时发通知（同 turn 通知的门控与退出抑制）；
  - 去重键 `sessionID + CallID`（controller 已保证一次，这里防御性去重并设 1024 上限，超出时清空历史而不是漏报）；
  - **审批通知不做节流**：每个等待中的调用都需要用户注意；连续多个审批会各自通知（跨会话总量节流见 §4）；
  - 文案固定为 "Approval needed / 需要确认" + 通用提示，**绝不渲染** `ApprovalRequest.Name/Args`（工具名与参数属于隐私边界）；
  - 点击回调携带 `sessionID` → 聚焦窗口并打开对应会话。
- 双路径不重复：生产接线时 `notifier.useApprovalHook()`，此后审批通知全部由 hook 发出，快照的 `waiting_approval/waiting_user` 迁移不再发通知（`TestSnapshotApprovalSkippedWhenHookWired` / `TestCurrentSessionApprovalNotifiesOnce` 覆盖）。

### 2.3 去重、节流与恢复

1. **快照路径**：按会话 + 相位迁移去重；同相位重复快照不产生事件；`waiting_approval → waiting_approval` 不重复提醒；终态处理后遗忘基线。
2. **hook 路径（turn）**：按 `SessionID + TurnID` 做防御性去重（同一 turn 的重复 hook 调用最多一条通知）；Controller 本身保证每 turn 只调用一次。
2b. **hook 路径（审批）**：按 `SessionID + CallID` 去重（Controller 每 call 只报一次 hook；分发器保留 1024 条历史防御重接线）。审批不参与完成节流。
3. **节流（同会话不轰炸）**：hook 路径的**完成**通知按会话 5 秒冷却（`turnNoticeThrottle`），goal/automation 连续 turn 只发一条；**失败通知绕过节流**（少见且需要立即处理）。冷却窗口内被合并的完成事件不再补发（打开应用即可看到结果）。
4. **活跃时不排队**：窗口在前台（`notificationActive()`：`a.quitting || (win.IsVisible() && win.IsFocused())`）时事件直接丢弃而不是延后补发，避免"切走时突然收到刚看完的结果"。
5. **启动/重连不重放**：hook 只报告实时 turn；首个快照只建立基线；应用退出期间被取消的 turn 以 `Stopped` 报告并被丢弃。
6. 本批不为通知设置业务 ID：每次事件一条系统通知；同一会话连续事件在 macOS 通知中心按 `Group: "zeokeel.tasks"` 归组。若后续需要"替换未读通知"，可在 `NotificationOptions.ID` 上以会话 id 为键（P2）。

### 2.4 活跃窗口判定与点击行为

- **仅在不活跃/隐藏时通知**：`windowActive()` = `win.IsVisible() && win.IsFocused()`（mygo 窗口 API，线程安全、任意 goroutine 可查）。窗口关闭到托盘后 `IsVisible()==false`；应用在后台时 `IsFocused()==false`；两者任一为假就允许通知。`a.quitting` 为真时一律抑制（退出时 controller 取消的 turn 也不打扰用户）。没有窗口（启动中/退出中）视为活跃，不发通知。
- **点击行为**：`focusSession(sessionID)` → `win.Show()` + `win.Focus()`；若 shell 与 controller 可用且当前不在该会话，再用**已导出的** API `Ctrl.OpenSession` + `shellApp.Screen = shell.Session` + `shellApp.SetSnapshot` 切回对应会话（不修改 `internal/shell/**`，不新增 shell 内部 hook）。后台 turn 与后台审批的通知同样携带 session id，点击即可回到该会话。
- **通知自清除**：应用回到前台时（`mygo.App.OnDidBecomeActive`）调用 `mygo.ClearNotifications()`，避免用户已看过的结果还留在通知中心（平台不支持时跳过）。

### 2.5 文案与本地化

- 跟随持久化的 UI 语言：读取 `Controller.Settings().Load()` 的 `ui.language`；缺失/读取失败用产品默认中文，与 shell 一致。
- 中英两套固定短文案（见 §2.1 表）；标题是事件本身，正文一句话说明去哪里继续。不做模板/参数化，避免把会话数据拼进通知。
- **后台会话也不带 session 标题**：标题可能是自动生成的（来自 prompt），存在锁屏泄露风险；统一用事件文案。

### 2.6 隐私边界

通知会被系统显示在锁屏、通知中心，可能被其他应用/旁人看到，因此：

- **不包含**：用户 prompt、assistant 输出、工具名/参数/结果、错误详情、session 标题、文件路径、模型名。
- **只包含**：事件类型 + 固定的操作提示（"Open ZeoKeel to review the result."）。
- 通知点击回调只携带 `sessionID`（本地标识，不渲染）。
- 审批 hook 携带的 `ApprovalRequest.Name/Args` 只用于内部去重/调试，不进入通知文案（`TestBackgroundSessionApprovalNotifies` 断言工具名与参数不出现在标题/正文）。
- 日志里只记录事件类型/会话失败，不打印通知正文以外的会话内容；后台失败同样只发 "Task failed"，不把 provider 错误拼进通知（`TestBackgroundSessionTurnFailureNotifies` 覆盖）。

### 2.7 可测试性与降级

- `type notifier interface { Show(title, body string, onClick func()) bool }`：生产实现 `systemNotifier` 包 `mygo.NewNotification(...).Show()`；测试注入 fake。
- `systemNotifier` 的 `supported`/`show` 是可替换函数：`supported` 默认 `mygo.NotificationsSupported`；`show` 默认 `showMygoNotification`（创建通知、注册 `OnClick`、在 goroutine 里 `Show`，因为 `Show` 在 macOS 可能等待权限弹窗且不能在 UI 线程阻塞）。
- **不支持时降级**：`NotificationsSupported()==false`（无显示器/无通知服务/未打包的 macOS 二进制）时 `Show` 打一条日志并返回 false，应用其余功能不受影响。
- 分发器与 mygo、shell 解耦：`active`/`language`/`focusSession` 都是注入函数；hook 路径额外注入时钟（`now`）测试节流。测试同时使用**真实 controller（store/broker/projector + scripted provider）**和 fake notifier，不依赖 GUI/显示器。

## 3. 跨平台注意

| 平台 | 行为 |
| --- | --- |
| macOS | `mygo.NotificationsSupported()` 只在**已打包**的 app（`mygo build`/打包产物）为 true，`mygo dev` 下为 false（软件无通知权限）；首次 `Show` 会弹系统授权框，用户拒绝时 `Show` 返回 `ErrNotificationsDenied`，本实现记日志后继续；通知按 `Group` 归组到应用的通知中心。 |
| Windows | mygo 用系统通知（托盘/Toast 通道）显示；应用未打包时同样可用，但系统可能按"未知应用"显示。权限/专注助手由系统控制。 |
| Linux | 依赖桌面环境的 notification service（DBus）；没有时 `NotificationsSupported()` 为 false，通知降级为日志，不影响应用。 |
| 通用 | 通知点击回调只由 mygo 在主线程派发；应用退出后点击的行为取决于平台（本批不持久化待点击通知，P2 可接 `App.OnNotificationClick` 按 ID 处理）。后台 turn 的通知与前台使用同一套 API，平台差异一致。 |

## 4. 已知限制与后续

1. ~~事件流只覆盖当前会话~~ **已解决（P1）**：后台会话完成/失败经 `TurnFinishedHook` 覆盖。
2. ~~后台会话的审批/提问等待仍可能漏~~ **已解决（P1 补全）**：`ApprovalRequestedHook` + `useApprovalHook()` 覆盖所有会话的新增审批/提问等待；快照路径让位避免双发。
3. **跨会话通知轰炸**：节流按会话，多个 automation 会话各自完成时仍会各发一条。P2 可做全局节流/汇总通知（如"3 个任务完成"）。
4. **审批通知粒度**：本批提示"有工具调用在等待批准"，不含工具名/参数（隐私）。若要显示"Bash 等待批准"，需从 `ApprovalRequest.Name/Args` 提取并单独评估隐私（P2）。
5. **失败详情**：正文不包含错误文本；用户点开后到会话里查看（P2 可加"查看错误"动作，但需评估锁屏泄露）。
6. **通知持久化/替换/动作按钮**：本批不做通知 ID 复用、通知内按钮（Allow/Deny）、应用退出后点击处理；macOS 打包签名后可用 `Sender`/分类（P2）。
7. **非 UI 线程调度**：`mygo.Notification.Show` 在独立 goroutine 调用；若未来 `Show` 需要在主线程调用，需要改为 `WhenReady`/`postMain` 派发（当前 mygo 允许任意 goroutine）。
8. **Windows/Linux 真机验证**：本环境无显示器/通知服务，单测只覆盖分发逻辑与注入 seam；系统通知外观/权限需真机补测。

## 5. 测试与验收

```bash
export PATH=/usr/local/go/bin:$PATH GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0
go build ./...
go test -count=1 ./internal/app ./internal/session
go test -count=1 -run 'TestNotification|TestSystemNotifier|TestWindowActiveWithoutWindowIsSafe|TestUILanguageFallsBackToChinese|TestBackgroundSession|TestCurrentSessionTurnNotifiesOnce|TestStoppedTurnDoesNotNotify|TestWiringAfterCompletedTurnDoesNotReplayHistory|TestTurnFinishedHook|TestApprovalHook|TestCurrentSessionApprovalNotifiesOnce|TestSnapshotApprovalSkippedWhenHookWired|TestBackgroundSessionApprovalNotifies' ./internal/app
gofmt -l internal/app internal/session
```

`internal/app/notify_test.go`（36 个 Test）覆盖：

- **快照路径（P0-2，17 个）**：turn 完成（英文标题/正文/点击聚焦会话）、失败（标题 + 不泄露错误内容）、等待审批每次等待一次/重绘不重复、`waiting_user` 同审批文案、首次快照不通知、窗口活跃不通知且不补发、无输出不通知、同 turn 去重、多会话独立、无 session id 仍可分发、中英文文案、不支持通知器降级与恢复、系统通知器 supported=false 不调用 show、supported=true 透传标题/正文/点击、无窗口下 `windowActive`/`focusSession` 安全、`uiLanguage` 缺省中文。
- **后台/终态 hook（P1，12 个）**：真实 controller + scripted provider 的后台完成通知（切走后 turn 完成仍通知，点击聚焦并打开该会话）、后台失败通知（且不泄露错误文本）、当前会话每 turn 恰好一条（快照 + hook 不重复）、用户 Stop 不通知、重启后接上新 dispatcher 不重放历史且下一 turn 通知、`sessionID+turnID` 重复 hook 去重、Stopped/无输出过滤、失败即使无输出也通知、活跃窗口门控、完成通知 5s 节流 + 失败绕过、未接 hook 时 hook 调用惰性、后台中文文案、`notificationActive` 退出抑制。
- **后台审批 hook（P1 补全，7 个）**：真实 controller + approval gate 的后台审批通知（切走后审批请求仍通知一次、文案不含工具名/参数、点击聚焦并打开该会话）、当前会话审批恰好一条（快照 + hook 不重复）、`sessionID+callID` 重复 hook 去重且不同会话各自通知、窗口活跃不通知、未接 hook 时惰性、快照审批在 hook 接线后被跳过、后台审批中文文案。
