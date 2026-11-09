package main

// Team and shared-task-board interaction steps: the session header's team chip,
// the grouped Agents pane with truthful run states, the native spawn form, the
// grouped task board (owner/dependency/expansion), the task board's live refresh
// through the team notification chain (T-03 regression) and the stable
// open-pane palette commands.
//
// The teammates are created through the same toolkit tool surface the model
// uses (spawn_teammate / subagent), and the task-board writes go through
// team_task_create, so every step drives the real backends, not fakes.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// teamService requires the live team backend.
func (d *drive) teamService() contracts.TeamService {
	team, err := d.root.Require(contracts.KeyTeam)
	if err != nil {
		panic(fmt.Sprintf("require team service: %v", err))
	}
	return team
}

// invokeTool runs one registered toolkit tool and reports whether it succeeded.
func (d *drive) invokeTool(name, arguments string) bool {
	registry, err := d.root.Require(contracts.KeyToolkit)
	if err != nil {
		return false
	}
	result, err := registry.Invoke(context.Background(), rig.ToolCall{
		ID: "drive-" + name, Name: name, Arguments: arguments,
	})
	return err == nil && !result.IsError
}

// spawnTeamMember starts one teammate through the spawn_teammate tool (the model
// path) and leaves its first run to the scripted server.
func (d *drive) spawnTeamMember(name, prompt string) bool {
	return d.invokeTool("spawn_teammate", fmt.Sprintf(`{"name":%q,"prompt":%q}`, name, prompt))
}

// waitTeamRunState pumps frames until the named teammate reports runState (or
// times out).
func (d *drive) waitTeamRunState(name, runState string, timeout time.Duration) bool {
	team := d.teamService()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.tester.Frame()
		for _, member := range team.Members() {
			if member.Name != name {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(member.RunState), runState) {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// closeAllTeamMembers stops and removes every live teammate, so steps start from
// a deterministic roster and a teammate's slow scripted run cannot leak into
// the next step.
func (d *drive) closeAllTeamMembers() {
	team := d.teamService()
	for _, member := range team.Members() {
		_ = team.Close(member.ID)
	}
	d.drain(2)
}

// openInspectorPane opens the inspector (when closed) and switches to a pane.
func (d *drive) openInspectorPane(label string) bool {
	if !d.uiRoot.RightPanelOpen() {
		d.uiRoot.SetRightPanelOpen(true)
		d.frame()
		d.frame()
	}
	return d.selectPane(label)
}

// createBoardTask writes one shared task through the model-facing tool.
func (d *drive) createBoardTask(arguments string) bool {
	return d.invokeTool("team_task_create", arguments)
}

// findBoardTask returns the first task matching subject ("" when absent).
func findBoardTask(tasks []contracts.BoardTask, subject string) (contracts.BoardTask, bool) {
	for _, task := range tasks {
		if task.Subject == subject {
			return task, true
		}
	}
	return contracts.BoardTask{}, false
}

// runTeamSteps registers the team/task-board interaction steps. They run after
// the inspector steps and clean up their own teammates/tasks.
func (d *drive) runTeamSteps() {
	d.step("team-chip", "team", "团队：页头 chip + 菜单入口（检查器关闭）",
		"有队友时会话页头出现「团队 N」chip；检查器关闭时点 chip 菜单「打开团队面板」→ 检查器打开并切到团队 pane；英文下 chip/菜单为 Team/Open team panel",
		func(c *check) {
			d.resetToSession()
			d.uiRoot.SetRightPanelOpen(false)
			d.frame()
			d.closeAllTeamMembers()
			d.server.SetMode(scriptAnswer)
			defer d.server.SetMode(scriptAnswer)

			if !d.spawnTeamMember("研究员", "研究一下当前工作目录并给出摘要") {
				c.failf("spawn_teammate 调用失败，无法验证团队 chip")
				return
			}
			if !d.waitTeamRunState("研究员", contracts.TeamRunDone, 15*time.Second) {
				c.warnf("队友 15s 内未完成首个回复，chip 仍可验证计数")
			}
			// Let the completion toast expire so the screenshot only shows the
			// chip/header, then settle the layout.
			d.tester.Advance(6 * time.Second)
			d.drain(3)

			c.require(!d.uiRoot.RightPanelOpen(), "检查器应保持关闭以验证 chip 入口")
			if !c.mustText("团队 1") {
				c.failf("检查器关闭时会话页头没有团队 chip")
				return
			}
			c.note("检查器关闭，页头 chip=%q", "团队 1")
			c.shot("team-chip-closed")

			if !c.click("团队 1") {
				return
			}
			d.drain(2)
			if !c.mustText("打开团队面板", "打开任务板") {
				return
			}
			c.shot("team-chip-menu")

			if !c.click("打开团队面板") {
				return
			}
			d.drain(3)
			c.require(d.uiRoot.RightPanelOpen(), "chip 菜单没有打开检查器")
			c.mustText("主代理", "队友 ·")
			c.note("chip 菜单一步打开检查器并切到团队 pane（检查器关闭态）")
			c.shot("team-chip-opened")

			// English: the chip and its menu follow the UI language.
			d.uiRoot.Locale().SetLanguage("en")
			d.drain(3)
			if !c.has("Team 1") {
				c.failf("英文下会话页头没有 Team chip；texts=%v", paletteTailTexts(d))
			} else {
				if !c.click("Team 1") {
					return
				}
				d.drain(2)
				c.mustText("Open team panel", "Open task board")
				c.shot("team-chip-en")
				d.key(0, ui.KeyEscape)
				d.drain(2)
			}
			d.uiRoot.Locale().SetLanguage("zh")
			d.drain(2)
			d.closeAllTeamMembers()
		})

	d.step("team-sections", "team", "团队：分节 + 真实运行态 badge（running vs finished）",
		"团队 pane 分「主代理/队友/同步子代理（只读）」；同一帧里 finished 队友显示「已完成」、running 队友显示「运行中」；只 spawn 一个已结束队友时不得出现「运行中」（availability≠running）",
		func(c *check) {
			d.resetToSession()
			d.closeAllTeamMembers()
			d.server.SetMode(scriptAnswer)
			defer d.server.SetMode(scriptAnswer)

			// A finished teammate first: its badge must be 已完成 and the pane
			// must not invent a running state for the availability "active".
			if !d.spawnTeamMember("审阅者", "审阅一下发布说明") {
				c.failf("spawn_teammate 调用失败")
				return
			}
			if !d.waitTeamRunState("审阅者", contracts.TeamRunDone, 15*time.Second) {
				c.failf("审阅者 15s 内没有结束首个运行，无法验证 truthful badge")
				return
			}
			// One synchronous delegation for the read-only group: the subagent
			// tool is the parent's blocking call, so it finishes before returning.
			d.invokeTool("subagent", `{"prompt":"整理一段发布摘要","name":"资料助手"}`)
			d.tester.Advance(6 * time.Second)
			d.drain(3)
			if !d.openInspectorPane("团队") {
				c.failf("无法打开团队 pane")
				return
			}
			d.drain(2)
			c.mustText("主代理", "ZeoKeel 主代理", "队友 · 1", "同步子代理", "只读")
			if !c.has("已完成") {
				c.failf("已结束的队友没有「已完成」badge")
			}
			if d.hasExactText("运行中") {
				c.failf("只有一个已结束队友时 pane 仍显示「运行中」：availability 被当成了 running")
			}
			c.note("仅 finished 队友：badge=已完成，无「运行中」（availability≠running）")
			c.shot("team-sections-finished")

			// A running teammate: the slow scripted stream keeps its run alive
			// while the finished one stays 已完成 in the same frame.
			d.server.SetMode(scriptSlowAnswer)
			if !d.spawnTeamMember("忙碌研究员", "慢慢检查当前工作目录") {
				c.failf("spawn_teammate（slow）调用失败")
				return
			}
			if !d.waitTeamRunState("忙碌研究员", contracts.TeamRunRunning, 10*time.Second) {
				c.failf("慢速队友没有进入 running 运行态")
				return
			}
			d.drain(3)
			c.mustText("队友 · 2", "运行中", "已完成")
			c.note("同一帧：running 队友=运行中，finished 队友=已完成")
			c.shot("team-sections")

			// The selected running teammate's detail: activity timeline + steer.
			if err := d.click("忙碌研究员"); err != nil {
				c.failf("点选忙碌研究员失败：%v", err)
				return
			}
			d.drain(2)
			// The detail offers the steer/interrupt controls, the latest-output
			// section and the bounded activity timeline the backend now fills
			// into contracts.TeamMember.Activity.
			c.mustText("活动时间线", "最新产出", "中断", "发送消息")
			c.shot("team-detail-running")

			// The finished teammate's detail keeps its output and Copy.
			if err := d.click("审阅者"); err != nil {
				c.failf("点选审阅者失败：%v", err)
				return
			}
			d.drain(2)
			c.mustText("最新产出", "复制")
			c.shot("team-detail-finished")

			d.server.SetMode(scriptAnswer)
			d.closeAllTeamMembers()
		})

	d.step("team-spawn-form", "team", "团队：新建队友表单（Esc 关闭，不 spawn）",
		"团队空态显示「还没有队友」+ 能力说明；点「新建队友」打开原生表单（名称/任务/模型 + 取消/创建）；Esc 关闭且没有发起 spawn",
		func(c *check) {
			// A fresh session has no synchronous delegations either, so the
			// team pane shows its real empty state.
			chat := d.chat()
			emptySession, err := chat.NewSession("团队空态验证")
			if err != nil {
				c.failf("NewSession: %v", err)
				return
			}
			_ = chat.SetActiveSession(emptySession)
			d.sessionID = emptySession
			_ = d.uiRoot.Navigator().Push("session")
			d.drain(3)
			d.closeAllTeamMembers()

			if !d.openInspectorPane("团队") {
				c.failf("无法打开团队 pane")
				return
			}
			d.drain(2)
			c.mustText("还没有队友", "spawn_teammate", "新建队友")
			c.shot("team-spawn-empty")

			if err := d.click("新建队友"); err != nil {
				c.failf("点击「新建队友」失败：%v", err)
				return
			}
			d.drain(2)
			c.mustText("新建队友", "名称", "任务", "模型（可选）", "取消", "创建")
			c.shot("team-spawn-form")

			membersBefore := len(d.teamService().Members())
			d.key(0, ui.KeyEscape)
			d.drain(3)
			if d.has("创建") && d.has("模型（可选）") {
				c.failf("Esc 没有关闭新建队友表单；texts=%v", paletteTailTexts(d))
			}
			if got := len(d.teamService().Members()); got != membersBefore {
				c.failf("关闭表单却创建了队友：before=%d after=%d", membersBefore, got)
			}
			c.note("Esc 关闭表单，未发起 spawn（members=%d）", membersBefore)
			c.shot("team-spawn-closed")

			d.restoreOriginalSession()
			_ = d.uiRoot.Navigator().Push("session")
			d.drain(2)
		})

	d.step("tasks-groups", "team", "任务板：状态分组/负责人/依赖/Enter 展开",
		"任务 pane 按状态分组（进行中/待处理/已完成 + 计数）；owner 显示 主代理/未认领/队友；阻塞任务显示「等待 task-X「subject」完成」、无依赖待办显示「可开始」；选中行 Enter 展开描述/依赖/写入范围与行内操作",
		func(c *check) {
			d.resetToSession()
			board, err := d.root.Require(contracts.KeyTaskBoard)
			if err != nil {
				c.failf("任务板服务缺失：%v", err)
				return
			}
			tasks, err := board.List(context.Background())
			if err != nil {
				c.failf("读取任务板失败：%v", err)
				return
			}
			blocker, ok := findBoardTask(tasks, "发布前检查")
			if !ok {
				c.failf("种子任务「发布前检查」缺失")
				return
			}
			// The blocked task and the teammate-owned task are written through
			// the model-facing tool before the pane opens (no cache to miss).
			blockedSubject := "被阻塞的驱动任务"
			if _, exists := findBoardTask(tasks, blockedSubject); !exists {
				d.createBoardTask(fmt.Sprintf(
					`{"subject":%q,"description":"依赖发布前检查","blocked_by":[%q],"write_scopes":["docs/COVERAGE.md"]}`,
					blockedSubject, blocker.ID))
			}
			teammateSubject := "队友负责的驱动任务"
			if _, exists := findBoardTask(tasks, teammateSubject); !exists {
				d.createBoardTask(fmt.Sprintf(`{"subject":%q,"owner":"研究员"}`, teammateSubject))
			}
			if !d.openInspectorPane("任务") {
				c.failf("无法打开任务 pane")
				return
			}
			d.drain(3)
			c.mustText("进行中 · 1", "待处理 ·", "个任务 · 进行中 1")
			c.mustText("负责人：主代理", "负责人：未认领", "负责人：研究员")
			waiting := "等待 " + blocker.ID
			if !c.has(waiting) {
				c.failf("阻塞任务没有依赖行 %q；texts=%v", waiting, paletteTailTexts(d))
			}
			if !c.has("可开始") {
				c.failf("无依赖待办没有「可开始」提示")
			}
			c.note("分组计数/owner/依赖行均在任务 pane 可见（blocker=%s）", blocker.ID)
			c.shot("tasks-groups")

			// Open the blocked task's detail through its row menu: the subject
			// Text is not an input target (the native List row owns the click),
			// while the ⋯ button's accessible name is per task. The keyboard
			// Enter path on the list is covered by the rightpanel frame tests
			// (TestTasksEnterExpandsDetail).
			var clickErr error
			for attempt := 0; attempt < 4; attempt++ {
				d.drain(2)
				if clickErr = d.click("任务操作：" + blockedSubject); clickErr == nil {
					break
				}
			}
			if clickErr != nil {
				c.failf("打开被阻塞任务菜单失败：%v", clickErr)
				return
			}
			d.drain(2)
			if err := d.click("展开详情"); err != nil {
				c.failf("任务菜单缺少「展开详情」：%v", err)
				return
			}
			d.drain(3)
			c.mustText("写入范围：", "依赖：", "开始任务", "docs/COVERAGE.md")
			c.require(d.hasExactText("收起"), "展开的任务详情缺少「收起」按钮")
			c.note("Enter 展开任务详情（描述/依赖/写入范围/开始任务）")
			c.shot("tasks-groups-expanded")

			if err := d.click("收起"); err != nil {
				c.failf("收起任务详情失败：%v", err)
				return
			}
			d.drain(2)
			// hasExactText: "收起" is a substring of the inspector's own
			// "收起右侧栏" toolbar label.
			if d.hasExactText("收起") {
				c.failf("收起后任务详情仍展开")
			}
		})

	d.step("tasks-auto-refresh", "team", "任务板：团队通知链自动刷新（T-03 回归）",
		"任务 pane 打开且缓存后，用 team_task_create 写入新任务；随后团队活动通知到达（不切 pane、不点刷新）→ 新任务在任务 pane 出现",
		func(c *check) {
			d.resetToSession()
			if !d.openInspectorPane("任务") {
				c.failf("无法打开任务 pane")
				return
			}
			d.drain(3)
			subject := "自动刷新进来的任务"
			if d.has(subject) {
				// A previous run left it behind; a fresh subject keeps the
				// assertion honest across repeated drive runs in one process.
				subject = fmt.Sprintf("自动刷新进来的任务-%d", time.Now().UnixNano()%100000)
			}
			baseline := 0
			if board, err := d.root.Require(contracts.KeyTaskBoard); err == nil {
				if tasks, listErr := board.List(context.Background()); listErr == nil {
					baseline = len(tasks)
				}
			}
			c.require(!d.has(subject), "任务写入前 pane 已显示目标任务，无法证明刷新")
			if !d.createBoardTask(fmt.Sprintf(`{"subject":%q,"description":"T-03 回归"}`,
				subject)) {
				c.failf("team_task_create 调用失败")
				return
			}
			// The pane cache still holds the old list: the direct tool write
			// alone does not repaint. The team notification chain does.
			d.drain(2)
			c.note("写入后 2 帧（无通知）：pane 命中缓存 subject-visible=%v", d.has(subject))
			if !d.spawnTeamMember("刷新触发器", "做一个很短的任务") {
				c.failf("spawn_teammate 调用失败（团队通知触发器）")
				return
			}
			if !d.waitText(subject, 10*time.Second) {
				c.failf("团队通知到达后任务 pane 仍未刷新出 %q；texts=%v", subject, paletteTailTexts(d))
				return
			}
			if board, err := d.root.Require(contracts.KeyTaskBoard); err == nil {
				if tasks, listErr := board.List(context.Background()); listErr == nil {
					c.require(len(tasks) > baseline, "任务板没有新增任务（%d→%d）", baseline, len(tasks))
				}
			}
			c.note("团队活动通知触发右栏任务缓存失效，新任务在 pane 出现（未点刷新）")
			c.shot("tasks-auto-refresh")
			d.closeAllTeamMembers()
		})

	d.step("palette-team", "team", "命令面板：搜「团队」命中团队/任务命令并打开",
		"Cmd+K 输入「团队」命中「打开团队面板」与「打开任务板」；Down+Enter 打开任务 pane（检查器关闭时先打开）；再次 Enter 打开团队 pane",
		func(c *check) {
			d.resetToSession()
			d.uiRoot.SetRightPanelOpen(false)
			d.frame()
			d.drain(2)
			c.require(!d.uiRoot.RightPanelOpen(), "前置条件：检查器应关闭")

			d.key(ui.Cmd, ui.KeyK)
			d.drain(2)
			if !c.has("命令面板") {
				c.failf("Cmd+K 没有打开命令面板")
				return
			}
			d.typeText("团队")
			d.drain(3)
			c.mustText("打开团队面板", "打开任务板")
			c.shot("palette-team")

			// The team command is first, the task-board command second: Down
			// then Enter opens the task board through the palette.
			d.key(0, ui.KeyDown)
			d.drain(2)
			d.key(0, ui.KeyEnter)
			d.drain(3)
			c.require(d.uiRoot.RightPanelOpen(), "palette 命令没有打开检查器")
			c.mustText("新任务", "个任务 ·")
			c.note("Down+Enter 打开「打开任务板」（检查器由关闭→打开）")
			c.shot("palette-team-tasks")

			d.key(ui.Cmd, ui.KeyK)
			d.drain(2)
			d.typeText("团队")
			d.drain(3)
			d.key(0, ui.KeyEnter)
			d.drain(3)
			c.mustText("主代理")
			c.note("Enter 打开「打开团队面板」→ 团队 pane")
			c.shot("palette-team-team")
		})
}
