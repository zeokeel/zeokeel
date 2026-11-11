package main

// Team, child-agent and shared-task-board steps for the DSH-aligned shell: the
// session header's 智能体团队 entry opens the native team overlay (member cards +
// shared task cards), the 子智能体 N chip opens the child switcher and switches
// the main content area to a child conversation (parent breadcrumb returns), and
// the task board is read inside the team overlay. The retired inspector team and
// task panes (and their rightpanel.team.open/tasks.open commands) are gone, so
// this file drives the same backends through the new entries only.
//
// The teammates are created through the same toolkit tool surface the model
// uses (spawn_teammate), and the task-board writes go through team_task_create,
// so every step drives the real backends, not fakes.

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

// invokeToolResult runs one registered toolkit tool and returns its result
// content for failure evidence.
func (d *drive) invokeToolResult(name, arguments string) (bool, string) {
	registry, err := d.root.Require(contracts.KeyToolkit)
	if err != nil {
		return false, err.Error()
	}
	result, err := registry.Invoke(context.Background(), rig.ToolCall{
		ID: "drive-" + name, Name: name, Arguments: arguments,
	})
	if err != nil {
		return false, err.Error()
	}
	return !result.IsError, result.Content
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

// teamHeaderLabel returns the header team entry's localized visible count form
// (the words and the count are separate labels; the entry itself is named
// 智能体团队 / Agent team).
func (d *drive) teamHeaderLabel() string {
	if d.hasExactText("智能体团队") {
		return "智能体团队"
	}
	if d.hasExactText("Agent team") {
		return "Agent team"
	}
	return ""
}

// openTeamOverlay clicks the header team entry and reports whether the native
// overlay opened (the 成员/共享任务 sections are its landmarks).
func (d *drive) openTeamOverlay() bool {
	label := d.teamHeaderLabel()
	if label == "" {
		return false
	}
	if err := d.click(label); err != nil {
		return false
	}
	d.drain(2)
	return d.has("成员") || d.has("Members")
}

// closeOverlay dismisses a header popover with Escape.
func (d *drive) closeOverlay() {
	d.key(0, ui.KeyEscape)
	d.drain(2)
}

// childBridge opens the parent breadcrumb link in the child view: the crumb is
// the only RoleLink named after the session title (the sidebar rows are tree
// items), so the driver can click it without depending on frame positions.
func (d *drive) childBridge(sessionTitle string) bool {
	info, ok := d.tester.AccessByRole(ui.RoleLink, sessionTitle)
	if !ok {
		return false
	}
	d.tester.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	d.drain(3)
	return true
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

// taskBoard requires the shared task board.
func (d *drive) taskBoard() contracts.TaskBoard {
	board, err := d.root.Require(contracts.KeyTaskBoard)
	if err != nil {
		panic(fmt.Sprintf("require task board: %v", err))
	}
	return board
}

// assignTaskOwner assigns one task through the optional TaskAssigner.
func (d *drive) assignTaskOwner(taskID, owner string) bool {
	assigner, ok := any(d.taskBoard()).(contracts.TaskAssigner)
	if !ok || taskID == "" || owner == "" {
		return false
	}
	_, err := assigner.Assign(context.Background(), taskID, owner)
	return err == nil
}

// runTeamSteps registers the team/child-agent/task-board steps. They run after
// the sidebar steps and clean up their own teammates/tasks.
func (d *drive) runTeamSteps() {
	d.step("team-chip", "team", "团队：页头「智能体团队」入口 + 原生浮层",
		"有队友时会话页头出现「智能体团队」入口；点击打开原生浮层（成员/共享任务）；Esc 关闭；英文下为 Agent team/Members/Shared tasks（旧的「团队 N」chip 与「打开团队面板/任务板」菜单已移除）", func(c *check) {
			d.resetToSession()
			d.uiRoot.SetRightPanelOpen(false)
			d.frame()
			d.closeAllTeamMembers()
			d.server.SetMode(scriptAnswer)
			defer d.server.SetMode(scriptAnswer)

			if !d.spawnTeamMember("研究员", "研究一下当前工作目录并给出摘要") {
				c.failf("spawn_teammate 调用失败，无法验证团队入口")
				return
			}
			if !d.waitTeamRunState("研究员", contracts.TeamRunDone, 15*time.Second) {
				c.warnf("队友 15s 内未完成首个回复，团队浮层仍可验证计数")
			}
			// Let the completion toast expire so the screenshot only shows the
			// header.
			d.tester.Advance(6 * time.Second)
			d.drain(3)
			c.require(!d.uiRoot.RightPanelOpen(), "右侧栏应保持关闭以验证浮层入口")
			if !c.mustText("智能体团队") {
				return
			}
			c.note("页头团队入口=%q", "智能体团队")
			c.shot("team-chip-closed")

			if !d.openTeamOverlay() {
				c.failf("点击「智能体团队」后没有打开团队浮层；texts=%v", paletteTailTexts(d))
				return
			}
			c.mustText("成员", "研究员", "已完成", "共享任务", "点击成员在面板中打开会话")
			c.require(!d.hasExactText("运行中"), "已结束队友的浮层仍显示「运行中」：availability 被当成 running")
			c.shot("team-overlay")
			d.closeOverlay()
			c.require(!d.hasExactText("共享任务"), "Esc 没有关闭团队浮层")

			// The retired entry has no menu: the old command items are gone.
			c.require(!d.has("打开团队面板") && !d.has("打开任务板"),
				"团队浮层或页头仍暴露旧的检查器命令入口")

			// English: the entry and the overlay follow the UI language.
			d.uiRoot.Locale().SetLanguage("en")
			d.drain(3)
			if !c.has("Agent team") {
				c.failf("英文下会话页头没有 Agent team 入口；texts=%v", paletteTailTexts(d))
			} else {
				if !c.click("Agent team") {
					d.uiRoot.Locale().SetLanguage("zh")
					return
				}
				d.drain(2)
				c.mustText("Members", "Shared tasks")
				c.shot("team-overlay-en")
				d.closeOverlay()
			}
			d.uiRoot.Locale().SetLanguage("zh")
			d.drain(3)
			d.closeAllTeamMembers()
		})

	d.step("team-states", "team", "团队：浮层成员真实运行态（finished vs running）",
		"团队浮层同一帧里 finished 队友显示「已完成」、running 队友显示「运行中」，计数 2 名成员 · 1 个运行中；只有一个已结束队友时不出现「运行中」", func(c *check) {
			d.resetToSession()
			d.closeAllTeamMembers()
			d.server.SetMode(scriptAnswer)
			defer d.server.SetMode(scriptAnswer)

			if !d.spawnTeamMember("审阅者", "审阅一下发布说明") {
				c.failf("spawn_teammate 调用失败")
				return
			}
			if !d.waitTeamRunState("审阅者", contracts.TeamRunDone, 15*time.Second) {
				c.failf("审阅者 15s 内没有结束首个运行，无法验证 truthful badge")
				return
			}
			d.tester.Advance(6 * time.Second)
			d.drain(3)
			if !d.openTeamOverlay() {
				c.failf("无法打开团队浮层；texts=%v", paletteTailTexts(d))
				return
			}
			c.mustText("1 名成员 · 0 个运行中", "已完成")
			if d.hasExactText("运行中") {
				c.failf("只有一个已结束队友时浮层仍显示「运行中」：availability 被当成了 running")
			}
			c.note("仅 finished 队友：badge=已完成，无「运行中」（availability≠running）")
			c.shot("team-states-finished")
			d.closeOverlay()

			// A running teammate: the slow scripted stream keeps its run alive
			// while the finished one stays 已完成 in the same overlay frame.
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
			if !d.openTeamOverlay() {
				c.failf("无法打开团队浮层")
				return
			}
			c.mustText("2 名成员 · 1 个运行中", "运行中", "已完成")
			c.note("同一帧：running 队友=运行中，finished 队友=已完成")
			c.shot("team-states")
			d.closeOverlay()

			d.server.SetMode(scriptAnswer)
			d.closeAllTeamMembers()
		})

	d.step("team-child-view", "team", "子智能体：switcher + 主内容区子会话 + 面包屑返回",
		"团队浮层点「打开成员」→ 主内容区切到该子会话（面包屑「父标题 › 子智能体名」、任务/输出/活动、可继续输入框）；点父标题面包屑返回父会话；再用页头「子智能体 N」chip 的 switcher 进入同一子会话", func(c *check) {
			d.resetToSession()
			d.closeAllTeamMembers()
			d.server.SetMode(scriptAnswer)
			defer d.server.SetMode(scriptAnswer)

			if !d.spawnTeamMember("研究员", "研究一下当前工作目录并给出摘要") {
				c.failf("spawn_teammate 调用失败")
				return
			}
			if !d.waitTeamRunState("研究员", contracts.TeamRunDone, 15*time.Second) {
				c.failf("研究员 15s 内没有结束首个运行")
				return
			}
			d.tester.Advance(6 * time.Second)
			d.drain(3)
			title := d.currentSessionTitle()

			// Path A: the team overlay's member card opens the child view.
			if !d.openTeamOverlay() {
				c.failf("无法打开团队浮层；texts=%v", paletteTailTexts(d))
				return
			}
			if err := d.click("打开成员 研究员"); err != nil {
				c.failf("团队浮层缺少「打开成员 研究员」：%v", err)
				return
			}
			d.drain(3)
			c.mustText(title, "研究员", "任务", "研究一下当前工作目录并给出摘要",
				"输出", "活动", "队友 · 可继续", "发送后等待该队友的回复。")
			c.require(!d.has("输入消息…"), "子会话仍显示父会话 composer")
			c.shot("team-child-view")
			if !d.childBridge(title) {
				c.failf("子会话找不到父标题面包屑返回链接；texts=%v", paletteTailTexts(d))
				return
			}
			c.require(!d.has("队友 · 可继续"), "点父标题面包屑后仍在子会话")
			d.syncSession()
			c.shot("team-child-return")

			// Path B: the header subagent chip's switcher enters the same child.
			chip := ""
			for _, text := range d.texts() {
				if strings.HasPrefix(text, "子智能体 ") {
					chip = text
					break
				}
			}
			if chip == "" {
				c.failf("页头没有「子智能体 N」chip；texts=%v", paletteTailTexts(d))
				return
			}
			if err := d.click(chip); err != nil {
				c.failf("点击 %q 失败：%v", chip, err)
				return
			}
			d.drain(2)
			c.mustText("子智能体会话", "研究员", "进入子智能体会话 研究员")
			c.shot("team-child-switcher")
			if err := d.click("进入子智能体会话 研究员"); err != nil {
				c.failf("switcher 缺少进入子会话的入口：%v", err)
				return
			}
			d.drain(3)
			c.mustText("研究员", "任务", "队友 · 可继续")
			if !d.childBridge(title) {
				c.failf("switcher 进入子会话后无法返回父会话")
				return
			}
			c.shot("team-child-switcher-return")
			d.closeAllTeamMembers()
		})

	d.step("team-tasks", "team", "任务板：浮层中的共享任务（状态/owner/依赖/写入范围）",
		"团队浮层的共享任务区按任务显示 subject、状态 badge（进行中/待处理）、负责人、依赖 task-X、写入范围；任务写入走 team_task_create/assigner", func(c *check) {
			d.resetToSession()
			d.closeAllTeamMembers()
			d.server.SetMode(scriptAnswer)
			defer d.server.SetMode(scriptAnswer)

			board := d.taskBoard()
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
			if !d.spawnTeamMember("文档审查员", "核对 docs 与 README") {
				c.failf("spawn_teammate 调用失败")
				return
			}
			if !d.waitTeamRunState("文档审查员", contracts.TeamRunDone, 15*time.Second) {
				c.warnf("文档审查员 15s 内没有结束首个运行，owner 显示仍可验证")
			}
			blockedSubject := "被阻塞的驱动任务"
			if _, exists := findBoardTask(tasks, blockedSubject); !exists {
				d.createBoardTask(fmt.Sprintf(
					`{"subject":%q,"description":"等待前置任务","blocked_by":[%q],"write_scopes":["docs/screenshots/"]}`,
					blockedSubject, blocker.ID))
			}
			tasks, _ = board.List(context.Background())
			if blocked, exists := findBoardTask(tasks, blockedSubject); exists {
				if !d.assignTaskOwner(blocked.ID, "文档审查员") {
					c.warnf("任务负责人指派失败（TaskAssigner 不可用）")
				}
			}
			d.tester.Advance(6 * time.Second)
			d.drain(3)
			if !d.openTeamOverlay() {
				c.failf("无法打开团队浮层；texts=%v", paletteTailTexts(d))
				return
			}
			c.mustText("共享任务", "发布前检查", "进行中", "编写发布说明", "待处理",
				blockedSubject, "依赖 "+blocker.ID, "写入 docs/screenshots/")
			c.note("共享任务卡：subject/状态/依赖 task-%s/写入范围", blocker.ID)
			c.shot("team-tasks")
			d.closeOverlay()
			d.closeAllTeamMembers()
		})

	d.step("team-tasks-refresh", "team", "任务板：重新打开浮层刷新共享任务",
		"浮层关闭期间用 team_task_create 写入新任务 → 重新打开团队浮层时新任务出现（浮层在打开时读取任务板快照）", func(c *check) {
			d.resetToSession()
			// The header only shows the team entry once a team exists
			// (subtraction pass); spawn a short-lived member through the same
			// tool surface the model uses, so the module wiring runs too.
			spawnedName := ""
			if d.teamHeaderLabel() == "" {
				spawnedName = "刷新验证队友"
				if !d.spawnTeamMember(spawnedName, "等待共享任务") {
					c.failf("spawn team member failed")
					return
				}
				d.drain(3)
			}
			if spawnedName != "" {
				defer func() {
					for _, member := range d.teamService().Members() {
						if member.Name == spawnedName {
							_ = d.teamService().Close(member.ID)
						}
					}
					d.drain(2)
				}()
			}
			subject := "刷新验证任务"
			board := d.taskBoard()
			tasks, _ := board.List(context.Background())
			_, exists := findBoardTask(tasks, subject)
			if !exists {
				if !d.createBoardTask(fmt.Sprintf(`{"subject":%q}`, subject)) {
					c.failf("team_task_create 失败")
					return
				}
			}
			if !d.openTeamOverlay() {
				c.failf("无法打开团队浮层；texts=%v", paletteTailTexts(d))
				return
			}
			if !d.has(subject) {
				c.failf("首次打开浮层没有显示新任务 %q", subject)
				return
			}
			d.closeOverlay()
			// Write while the overlay is closed, then reopen: the snapshot is
			// read when the overlay opens, so the new task must appear.
			subject = "关闭期间新增任务"
			d.createBoardTask(fmt.Sprintf(`{"subject":%q}`, subject))
			if !d.openTeamOverlay() {
				c.failf("无法重新打开团队浮层")
				return
			}
			if !d.has(subject) {
				c.failf("重新打开浮层后没有显示关闭期间新增的任务 %q；texts=%v",
					subject, paletteTailTexts(d))
				return
			}
			c.shot("team-tasks-refreshed")
			d.closeOverlay()
		})

	d.step("repro-team-turn", "team", "复现：真实回合内 spawn_teammate，回合结束后团队/子智能体入口与浮层",
		"模型在真实回合里调用 spawn_teammate → 回合结束、队友完成后，页头仍显示「智能体团队」与「子智能体」入口，浮层里仍列出 研究员（已完成）", func(c *check) {
			d.resetToSession()
			d.closeAllTeamMembers()
			d.server.SetMode(scriptSpawnTeamThenAnswer)
			defer d.server.SetMode(scriptAnswer)

			before, err := d.sendFromComposer("用智能体团队研究当前工作目录")
			if err != nil {
				c.failf("%v", err)
				return
			}
			chat := d.syncSession()
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				d.frame()
				if d.has("需要审批") {
					d.resolvePendingApprovals(contracts.ApprovalAllow)
				}
				session, ok := chat.Session(d.sessionID)
				if ok && !session.Running && len(session.Messages) > before {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			session, ok := chat.Session(d.sessionID)
			if !ok || session.Running {
				c.failf("回合在 20s 内没有结束")
				return
			}
			if !d.waitTeamRunState("研究员", contracts.TeamRunDone, 15*time.Second) {
				c.warnf("队友没有在 15s 内完成")
			}
			d.tester.Advance(6 * time.Second)
			d.drain(3)

			header := []string{}
			subagentLabel := ""
			for _, text := range d.texts() {
				if strings.HasPrefix(text, "智能体团队") || strings.HasPrefix(text, "子智能体") {
					header = append(header, text)
					if strings.HasPrefix(text, "子智能体") {
						subagentLabel = text
					}
				}
			}
			c.note("回合结束后页头入口：%v", header)
			c.require(d.hasExactText("智能体团队"), "回合结束后页头没有「智能体团队」入口；texts=%v", paletteTailTexts(d))
			c.require(subagentLabel != "", "回合结束后页头没有「子智能体」入口；texts=%v", paletteTailTexts(d))
			c.shot("repro-team-turn-header")
			if !d.openTeamOverlay() {
				c.failf("无法打开团队浮层；texts=%v", paletteTailTexts(d))
				return
			}
			c.mustText("研究员", "已完成")
			c.shot("repro-team-turn-overlay")
			d.closeOverlay()
			d.closeAllTeamMembers()
		})

	d.step("palette-right-pages", "team", "命令面板：右侧栏页面命令（旧团队/任务命令已移除）",
		"Cmd+K 输入「团队」不再命中旧的团队/任务命令；输入「打开文件页」后 Enter → 右侧栏打开并切到文件页（README.md 可见）", func(c *check) {
			if !d.openPalette() {
				c.failf("命令面板未打开")
				return
			}
			if !d.focusPaletteSearch() {
				c.failf("命令面板没有可点击的搜索框")
				return
			}
			d.typeText("团队")
			d.drain(2)
			c.require(!d.has("打开团队面板") && !d.has("打开任务板"),
				"命令面板仍注册旧的团队/任务命令；texts=%v", paletteTailTexts(d))
			d.clearDraft()
			d.typeText("打开文件页")
			d.drain(2)
			c.require(d.has("打开文件页"), "命令面板没有「打开文件页」命令")
			c.shot("palette-right-pages")
			d.key(0, ui.KeyEnter)
			d.drain(3)
			c.require(d.uiRoot.RightPanelOpen(), "进入「打开文件页」命令后右侧栏未打开")
			if !d.waitText("README.md", 5*time.Second) {
				c.warnf("右侧栏文件页没有在 5s 内列出 README.md")
			}
			c.shot("palette-files-page")
			d.uiRoot.SetRightPanelOpen(false)
			d.frame()
		})
}
