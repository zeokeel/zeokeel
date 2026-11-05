# ADR 0004：桌面通知（turn 完成/失败/等待审批）

- 日期：2026-11-05
- 状态：**已采纳**（桌面交互 P0-2）
- 决策者：用户（ZeoKeel = 独立品牌桌面产品，交互按桌面惯例本地化）；记录：Lead / test-migrate-a
- 关联：[ADR 0003 桌面壳集成](0003-desktop-shell-integration.md)、[UI/UX 评估 §5.2/§7](../research/03-ux-design.md)、[mygo 通知 API](../../.cache/gomod/github.com/egoist/mygo@v0.7.4/modules.go)（`NotificationOptions`/`NewNotification`/`OnClick`/`NotificationsSupported`）
- 实施：`internal/app/notify.go`（新增）、`internal/app/notify_test.go`（新增）、`internal/app/app.go`（仅接线）

## 1. 背景与目标

`docs/research/03-ux-design.md` §5.2 把「长任务完成有系统通知」列为 P0 桌面缺口：ZeoKeel 是长时间运行的桌面 Agent，用户提交任务后会切到别的应用；目前完成/失败/等待审批只体现在应用内 UI（`Toast`、会话界面），切走后完全错过。`internal/app` 已在 `internal/app/app.go` 通过 `services.OnUpdate(func(*state.Snapshot))` 接收状态流（同一条流也驱动 shell 重绘），因此通知可以在应用层从快照相位变化推导，不需要改 shell。

目标（P0-2）：

1. 从 `state.Snapshot` 的 `Phase` 变化识别 **turn 完成 / 失败 / 等待审批** 三类事件；
2. 只在应用窗口**不活跃或隐藏**时发系统通知（避免打断正在看结果的人）；
3. 点击通知 → 显示并聚焦窗口，并打开事件所属会话；
4. 中英双语、隐私安全（通知会出现在锁屏和通知中心，不能带 prompt/工具输出）；
5. Notifier 接口可注入，fake 覆盖三类事件/去重/不支持降级，无显示器可测。

## 2. 决策

### 2.1 事件来源与触发矩阵

数据来源：`internal/session.Controller.OnUpdate` 推送的 `*state.Snapshot`（与 shell 重绘同一份不可变快照）。通知分发器只观察 `Snapshot.SessionID`、`Snapshot.Phase`、`Snapshot.Messages`、`Snapshot.Tools`、`Snapshot.LastError`，不自己订阅 broker，也不读取会话内容正文。

分发器（`notificationDispatcher.Observe`）按会话记住"上一次相位"，只在**相位迁移**时判定事件：

| 迁移（上一次快照 → 本次快照） | 事件 | 文案（EN / ZH） | 额外条件 |
| --- | --- | --- | --- |
| `running → idle` | turn 完成 | Task complete / 任务完成 | 本轮有可读输出（assistant 文本/推理或工具调用存在）；只有用户 prompt 不算输出 |
| `running → failed`（或 `waiting_* → failed`） | turn 失败 | Task failed / 任务失败 | 终态由 `PhaseFailed` 判定；不读取 `LastError.Text` 进正文 |
| 任意 → `waiting_approval` / `waiting_user` | 等待审批/回答 | Approval needed / 需要确认 | 快照本身已有 `PhaseWaitingApproval`/`PhaseWaitingUser`（`internal/state/snapshot.go`），无需扩展事件流 |
| 其他迁移（如 `idle → running`） | 无 | — | 只更新基线 |

**首次快照只记录基线，不发通知**：应用启动、会话恢复、重新打开会话时快照可能已经是 idle/failed/waiting 状态，不能把旧 turn 或旧审批当作新事件重放。

### 2.2 去重策略

1. **按会话 + 相位迁移去重**：同相位重复快照（token 流、重绘、标题刷新）不产生事件；`waiting_approval → waiting_approval` 不会重复提醒；审批通过后再次进入等待才算新事件。
2. **终态即遗忘**：`PhaseIdle`/`PhaseFailed` 处理后从 per-session 状态表删除，下一轮从新基线开始；同时保证同一 turn 的完成通知最多一次。
3. **活跃时不排队**：窗口在前台时事件直接丢弃（而不是延后补发），避免"切走时突然收到刚看完的结果"。
4. **本批不为通知设置业务 ID**：每次事件一条系统通知；同一会话连续事件在 macOS 通知中心按 `Group: "zeokeel.tasks"` 归组。若后续需要"替换未读通知"，可在 `NotificationOptions.ID` 上以会话 id 为键（P1）。

### 2.3 活跃窗口判定与点击行为

- **仅在不活跃/隐藏时通知**：`windowActive()` = `win.IsVisible() && win.IsFocused()`（mygo `internal/app` 窗口 API，线程安全、任意 goroutine 可查）。窗口关闭到托盘后 `IsVisible()==false`；应用在后台时 `IsFocused()==false`；两者任一为假就允许通知。没有窗口（启动中/退出中）视为活跃，不发通知。
- **点击行为**：`focusSession(sessionID)` → `win.Show()` + `win.Focus()`；若 shell 与 controller 可用且当前不在该会话，再用**已导出的** API `Ctrl.OpenSession` + `shellApp.Screen = shell.Session` + `shellApp.SetSnapshot` 切回对应会话（不修改 `internal/shell/**`，不新增 shell 内部 hook）。会话 id 只来自快照，不参与文案。
- **通知自清除**：应用回到前台时（`mygo.App.OnDidBecomeActive`）调用 `mygo.ClearNotifications()`，避免用户已看过的结果还留在通知中心（mygo 文档推荐用法；平台不支持时跳过）。

### 2.4 文案与本地化

- 跟随持久化的 UI 语言：读取 `Controller.Settings().Load()` 的 `ui.language`；缺失/读取失败用产品默认中文（`internal/session.DefaultSettings`），与 shell 一致。
- 目前支持中文/英文两套固定短文案（见 §2.1 表）；标题是事件本身（Task complete/Task failed/Approval needed），正文一句话说明去哪里继续。不做模板/参数化，避免把会话数据拼进通知。

### 2.5 隐私边界

通知会被系统显示在锁屏、通知中心，可能被其他应用/旁人看到，因此：

- **不包含**：用户 prompt、assistant 输出、工具名/参数/结果、错误详情、session 标题、文件路径、模型名。
- **只包含**：事件类型 + 固定的操作提示（"Open ZeoKeel to review the result."）。
- 通知点击回调只携带 `sessionID`（本地标识，不渲染）。
- 日志里只记录事件类型/会话失败，不打印通知正文以外的会话内容。

### 2.6 可测试性与降级

- `type notifier interface { Show(title, body string, onClick func()) bool }`：生产实现 `systemNotifier` 包 `mygo.NewNotification(...).Show()`；测试注入 fake。
- `systemNotifier` 的两个依赖（`supported`、`show`）是可替换函数：`supported` 默认 `mygo.NotificationsSupported`；`show` 默认 `showMygoNotification`（创建通知、注册 `OnClick`、在 goroutine 里 `Show`，因为 `Show` 在 macOS 可能等待权限弹窗且不能在 UI 线程阻塞）。
- **不支持时降级**：`NotificationsSupported()==false`（无显示器/无通知服务/未打包的 macOS 二进制）时 `Show` 打一条日志并返回 false，应用其余功能不受影响；fake 也覆盖"先不可用、后恢复"的路径。
- 分发器本身与 mygo、shell 解耦（`active`/`language`/`focusSession` 都是注入函数），可在 `go test ./internal/app` 中完整覆盖：三类事件各一次、同 turn 去重、首次快照不通知、活跃窗口不通知、无输出不通知、多会话独立、中英文文案、不支持降级、点击回调聚焦。

## 3. 跨平台注意

| 平台 | 行为 |
| --- | --- |
| macOS | `mygo.NotificationsSupported()` 只在**已打包**的 app（`mygo build`/打包产物）为 true，`mygo dev` 下为 false（软件无通知权限）；首次 `Show` 会弹系统授权框，用户拒绝时 `Show` 返回 `ErrNotificationsDenied`，本实现记日志后继续；通知按 `Group` 归组到应用的通知中心。 |
| Windows | mygo 用系统通知（托盘/Toast 通道）显示；应用未打包时同样可用，但系统可能按"未知应用"显示。权限/专注助手由系统控制。 |
| Linux | 依赖桌面环境的 notification service（DBus）；没有时 `NotificationsSupported()` 为 false，通知降级为日志，不影响应用。 |
| 通用 | 通知点击回调只由 mygo 在主线程派发；应用退出后点击的行为取决于平台（本批不持久化待点击通知，P1 可接 `App.OnNotificationClick` 按 ID 处理）。 |

## 4. 已知限制与后续

1. **事件流只覆盖当前会话**：`Controller.OnUpdate` 的 `notify` 只对 `currentID` 派发快照（`notifyAny` 仅用于标题刷新）。因此"用户在会话 A 启动长 turn 后切到会话 B 并离开"时，A 的完成快照可能不会到达本分发器。后续可在 controller 增加后台事件钩子（不改 shell 即可）。
2. **审批通知粒度**：本批提示"有工具调用在等待批准"，不含工具名/参数（隐私）。若要显示"Bash 等待批准"，需从 `Snapshot.Tools` 提取并单独评估隐私（P1）。
3. **失败详情**：正文不包含错误文本；用户点开后到会话里查看（P1 可加"查看错误"动作，但需评估锁屏泄露）。
4. **通知持久化/替换/动作按钮**：本批不做通知 ID 复用、通知内按钮（Allow/Deny）、应用退出后点击处理；macOS 打包签名后可用 `NotificationSender`/分类（P1）。
5. **非 UI 线程调度**：`mygo.Notification.Show` 在独立 goroutine 调用；若未来 `Show` 需要在主线程调用，需要改为 `App.WhenReady`/`postMain` 派发（当前 mygo 允许任意 goroutine）。
6. **Windows/Linux 真机验证**：本环境无显示器/通知服务，单测只覆盖分发逻辑与注入 seam；系统通知外观/权限需真机补测。

## 5. 测试与验收

```bash
export PATH=/usr/local/go/bin:$PATH GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0
go build ./...
go test -count=1 ./internal/app
go test -count=1 -run 'TestNotification|TestSystemNotifier|TestWindowActiveWithoutWindowIsSafe|TestUILanguageFallsBackToChinese' ./internal/app
gofmt -l internal/app
```

`internal/app/notify_test.go` 覆盖清单：turn 完成（英文标题/正文/点击聚焦会话）、失败（标题 + 不泄露错误内容）、等待审批（每次等待一次，重绘不重复）、`waiting_user` 同审批文案、首次快照不通知、窗口活跃不通知且不补发、无输出不通知、同 turn 去重、多会话独立、无 session id 仍可分发、中英文文案、不支持通知器降级与恢复、系统通知器 supported=false 不调用 show、supported=true 透传标题/正文/点击、无窗口下 `windowActive`/`focusSession` 安全、`uiLanguage` 缺省中文。
