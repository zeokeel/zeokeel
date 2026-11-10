package session

// DSH-aligned agent surfaces (this file): the session header's 智能体团队 entry,
// 子智能体 chip and background-jobs chip with their native overlay panels, the
// child-agent conversation view the main content area switches to, and the
// generated-file turn-tail cards under a finished answer.
//
// The session shell stays one screen: the child conversation is a content-area
// switch with the parent's title as a breadcrumb, never a sidebar entry or a
// second window. The read models come from internal/contracts/agentsurface.go
// (ChildAgentService/JobService/FilePreviewer); every service is an optional
// context lookup, so a host without one hides the entry instead of failing.
//
// Frames never wait on the backends: overlay opens read the in-memory read
// models synchronously (one bounded snapshot), and the two actions that can
// block — sending a message to a continuable teammate and killing a job (the
// job tool waits a grace period for the process) — run off the UI thread and
// publish their result through UIRuntime.Post.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Header entry metrics. The compact width is where the agent entries drop their
// words (the icon and the count stay) so the conversation title keeps its
// measure; the row action height is the 24 DIP compact target used inside the
// overlay cards and the turn-tail file rows.
const (
	// agentHeaderCompactWidth is the header column width under which the agent
	// entries render in their compact (icon + count) form.
	agentHeaderCompactWidth float32 = design.TranscriptWidth * 2 / 3
	// agentRowActionHeight is the compact inline action height.
	agentRowActionHeight float32 = design.Space6
	// agentHeadingWeight is the heading weight of the agent surfaces' names and
	// titles (600): the type roles carry no heading weight yet (DESIGN.md D6),
	// so the surfaces name it once instead of repeating a raw literal.
	agentHeadingWeight = 600
)

// agentSurface is the UI-thread state of the agent surfaces: the overlay open
// flags and their scroll/list state, the cached task-board snapshot (loaded
// when the team overlay opens), the child conversation view's state, and the
// in-flight action flags. It lives in the module's state (one instance per
// loaded module) and is only touched on the UI thread.
type agentSurface struct {
	// teamOpen/childOpen/jobsOpen are the three header overlays' open flags:
	// the native popovers close them on Escape, a press outside or a second
	// click on their entry.
	teamOpen  bool
	childOpen bool
	jobsOpen  bool

	// The overlays' scroll offsets and list state (stable row identity).
	teamScroll   ui.ScrollState
	childScroll  ui.ScrollState
	jobsScroll   ui.ScrollState
	teamMembers  ui.ListState
	teamTasks    ui.ListState
	switcherList ui.ListState
	jobsList     ui.ListState
	activityList ui.ListState

	// tasks is the shared task board snapshot cached when the team overlay
	// opens; tasksLoaded separates "not read yet" from "read, empty".
	tasks       []contracts.BoardTask
	tasksErr    string
	tasksLoaded bool

	// Child conversation view. childID names the open child ("" = the parent
	// conversation); childDraft/childSending/childSendErr are the continuable
	// teammate's message input; childReply is the last answer SendChildAgent
	// returned; crumbChosen backs the breadcrumb's native link state.
	childID       string
	childDraft    string
	childInput    ui.Handle
	childFocus    bool
	childSending  bool
	childSendErr  string
	childReply    string
	childReplyFor string
	crumbChosen   int

	// Background-job stop feedback: jobKilling is the id of the job whose kill
	// is in flight (its stop action is disabled), jobErr the last failure.
	jobKilling string
	jobErr     string
}

// --- optional service lookups (nil-tolerant) --------------------------------

// teamService returns the host's team backend, or nil when the host has none.
func (st *state) teamService() contracts.TeamService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyTeam)
	if !ok || raw == nil {
		return nil
	}
	team, _ := raw.(contracts.TeamService)
	return team
}

// childAgentService returns the host's unified child-agent read model.
func (st *state) childAgentService() contracts.ChildAgentService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyChildAgents)
	if !ok || raw == nil {
		return nil
	}
	children, _ := raw.(contracts.ChildAgentService)
	return children
}

// jobService returns the host's background-job read/control surface.
func (st *state) jobService() contracts.JobService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyJobs)
	if !ok || raw == nil {
		return nil
	}
	jobs, _ := raw.(contracts.JobService)
	return jobs
}

// filePreviewer returns the right sidebar's file-preview bridge.
func (st *state) filePreviewer() contracts.FilePreviewer {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyFilePreview)
	if !ok || raw == nil {
		return nil
	}
	previewer, _ := raw.(contracts.FilePreviewer)
	return previewer
}

// taskBoard returns the shared team task board.
func (st *state) taskBoard() contracts.TaskBoard {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyTaskBoard)
	if !ok || raw == nil {
		return nil
	}
	board, _ := raw.(contracts.TaskBoard)
	return board
}

// --- header entries ---------------------------------------------------------

// buildAgentHeaderEntries adds the header's agent entries after the title, in
// the DSH order: 智能体团队, 子智能体 N, N 个后台任务. The host keeps rendering
// its own utilities (search, inspector toggle) after the actions slot.
func buildAgentHeaderEntries(c *ui.Context, t *design.Tokens, st *state, chat contracts.ChatService) {
	if st == nil {
		return
	}
	activeID := chat.ActiveSession()
	compact := st.headerWidth > 0 && st.headerWidth < agentHeaderCompactWidth
	buildTeamEntry(c, t, st, compact)
	buildSubagentEntry(c, t, st, activeID, compact)
	buildJobsEntry(c, t, st, compact)
}

// agentEntryButton is one header entry: an icon, optional words and count, and
// the entries' common metrics. The accessible name (Label) is the stable entry
// name even when the compact form hides the words; the Tooltip carries the
// numbers.
func agentEntryButton(c *ui.Context, t *design.Tokens, label, tooltip string) ui.Element {
	th := t.Theme()
	return ui.Button(c, "").Label(label).Tooltip(tooltip).
		Height(t.Scale(th.Space(7))).PaddingX(design.Space2).
		Radius(design.RadiusSM).Shrink(0).Gap(design.Space1)
}

// agentCountText renders the quiet count beside an entry's words.
func agentCountText(c *ui.Context, t *design.Tokens, count int) {
	design.Apply(ui.Text(c, fmt.Sprintf("%d", count)), t.Font(design.TypeXS)).
		TextColor(t.LabelTertiary).NoWrap()
}

// agentStatusFill is the status dot's fill color: the semantic fill token,
// never a status text color used as a fill (DESIGN.md §5.2).
func agentStatusFill(t *design.Tokens, kind uikit.StatusKind) ui.Color {
	switch kind {
	case uikit.StatusRunning, uikit.StatusInProgress:
		return t.Warning
	case uikit.StatusSuccess:
		return t.Success
	case uikit.StatusError:
		return t.Danger
	default:
		return t.LabelTertiary
	}
}

// buildTeamEntry is the header's 智能体团队 entry: a users glyph, the words (the
// count alone on a narrow band) and the live member count, opening the native
// team overlay. The entry is present whenever the host provides the team
// backend, even with no members, so the feature stays discoverable.
func buildTeamEntry(c *ui.Context, t *design.Tokens, st *state, compact bool) {
	team := st.teamService()
	if team == nil {
		return
	}
	members := team.Members()
	running := 0
	for _, member := range members {
		if uikit.ParseRunState(member.RunState) == uikit.RunStateRunning {
			running++
		}
	}
	label := st.tr("智能体团队", "Agent team")
	tooltip := fmt.Sprintf(st.tr("%d 名成员 · %d 个运行中", "%d members · %d running"),
		len(members), running)
	chip := agentEntryButton(c, t, label, tooltip)
	chip.Children(func() {
		ui.Icon(c, icons.Users()).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
		if !compact {
			design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
				TextColor(t.LabelSecondary).NoWrap()
		}
		agentCountText(c, t, len(members))
		if running > 0 {
			// The quiet warning dot keeps the running state visible when the
			// compact band has no room for the words.
			ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
				Background(t.Warning).Margin(0, 0, 0, design.Space1)
		}
	})
	if chip.Clicked() {
		st.agents.teamOpen = !st.agents.teamOpen
		if st.agents.teamOpen {
			st.loadTeamTasks()
		}
		st.runtime.Invalidate()
	}
	if !st.agents.teamOpen {
		return
	}
	ui.PopoverBase(c, chip, &st.agents.teamOpen, func(panel ui.Element) {
		agentPanelStyle(panel, t, design.DialogWidthCompact)
		buildTeamPanel(c, t, st, members, running)
	})
}

// loadTeamTasks snapshots the shared task board when the team overlay opens.
// The board is the in-memory rig task manager (one bounded read), and a read
// failure stays inside the overlay instead of failing the screen.
func (st *state) loadTeamTasks() {
	st.agents.tasks = nil
	st.agents.tasksErr = ""
	st.agents.tasksLoaded = false
	board := st.taskBoard()
	if board == nil {
		return
	}
	tasks, err := board.List(context.Background())
	if err != nil {
		st.agents.tasksErr = st.tr("任务板读取失败：", "Task board read failed: ") + err.Error()
		return
	}
	st.agents.tasks = tasks
	st.agents.tasksLoaded = true
}

// agentPanelStyle gives an overlay panel the product's floating-surface look
// (the raised panel style plus the overlay elevation token).
func agentPanelStyle(panel ui.Element, t *design.Tokens, width float32) {
	panel = design.Apply(panel, t.Panel(), t.ShadowOverlay())
	panel.Width(width)
	panel.Padding(design.Space3)
	panel.Gap(design.Space2)
}

// agentSectionLabel is the overlays' small section heading.
func agentSectionLabel(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, text), t.Font(design.TypeXXXS)).FontWeight(agentHeadingWeight).
		TextColor(t.LabelTertiary)
}

// agentHint is the overlays' quiet footer hint (how to leave, what a click
// does).
func agentHint(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, text), t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
}

// agentRowAction is one compact text action inside an overlay card or a file
// row. The visible words stay short; the accessible name is unique per row
// (action + object) so keyboard/AT users and frame tests can tell the rows
// apart.
func agentRowAction(c *ui.Context, t *design.Tokens, text, accessibleName string, onClick func()) {
	button := ui.Button(c, text).Label(accessibleName)
	design.Apply(button, t.Font(design.TypeXXXS))
	button.Height(agentRowActionHeight).PaddingX(design.Space2).
		Radius(design.RadiusSM).Shrink(0)
	if button.Clicked() && onClick != nil {
		onClick()
	}
}

// agentPanelHeader is the overlays' title row: glyph, title and the counts.
func agentPanelHeader(c *ui.Context, t *design.Tokens, icon *ui.SVG, title, counts string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
		design.Apply(ui.Text(c, title), t.Font(design.TypeS)).FontWeight(agentHeadingWeight).
			TextColor(t.LabelPrimary)
		ui.Spacer(c)
		design.Apply(ui.Text(c, counts), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).NoWrap()
	})
}

// buildTeamPanel is the team overlay's body: member cards (state dot, name,
// model, last activity, 打开) and shared task cards (subject, status, owner,
// blockers, write scopes), scrolling inside the native panel.
func buildTeamPanel(c *ui.Context, t *design.Tokens, st *state, members []contracts.TeamMember, running int) {
	agentPanelHeader(c, t, icons.Users(), st.tr("智能体团队", "Agent team"),
		fmt.Sprintf(st.tr("%d 名成员 · %d 个运行中", "%d members · %d running"), len(members), running))
	ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).
		TrackScroll(&st.agents.teamScroll).Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
			agentSectionLabel(c, t, st.tr("成员", "Members"))
			if len(members) == 0 {
				design.Apply(ui.Text(c, st.tr("还没有队友", "No teammates yet")),
					t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
			} else {
				buildTeamMembersList(c, t, st, members)
			}
			agentSectionLabel(c, t, st.tr("共享任务", "Shared tasks"))
			buildSharedTasksList(c, t, st)
		})
	})
	agentHint(c, t, st.tr("点击成员在面板中打开会话 · Esc 关闭",
		"Open a member here · Esc closes"))
}

// buildTeamMembersList renders the teammate cards through the official List so
// the overlay can grow without a per-frame card wall.
func buildTeamMembersList(c *ui.Context, t *design.Tokens, st *state, members []contracts.TeamMember) {
	st.agents.teamMembers.Key = func(index int) any {
		if index < 0 || index >= len(members) {
			return nil
		}
		return members[index].ID
	}
	st.agents.teamMembers.Label = func(index int) string {
		if index < 0 || index >= len(members) {
			return ""
		}
		return agentDisplayName(members[index].Name, members[index].ID)
	}
	ui.List(c, &st.agents.teamMembers, len(members), func(index int) {
		buildTeamMemberCard(c, t, st, members[index])
	}).FillWidth().Gap(design.Space2)
}

// buildTeamMemberCard is one member row: running state dot, name (with the
// 当前 badge when its child view is open), latest activity, model and the 打开
// action (only when the host can render a child conversation).
func buildTeamMemberCard(c *ui.Context, t *design.Tokens, st *state, member contracts.TeamMember) {
	name := agentDisplayName(member.Name, member.ID)
	kind := uikit.ParseRunState(member.RunState).Kind()
	card := ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3)
	card.Children(func() {
		ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
			Background(agentStatusFill(t, kind)).Margin(design.Space1, 0, 0, 0)
		ui.Column(c).Grow(1).Shrink(1).MinWidthPercent(0).Gap(design.Space1).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				if st.agents.childID == member.ID {
					design.Apply(ui.Badge(c, st.tr("当前", "Current")), t.Badge())
				}
				ui.Spacer(c)
				uikit.StatusText(c, t, design.TypeXXXS, kind,
					uikit.RunStateLabel(st.locale, member.RunState))
			})
			design.Apply(ui.Text(c, agentMemberActivity(st, member)), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			if model := strings.TrimSpace(member.Model); model != "" {
				design.Apply(ui.Text(c, model), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			}
		})
		if st.childAgentService() != nil {
			agentRowAction(c, t, st.tr("打开", "Open"),
				st.tr("打开成员 ", "Open member ")+name, func() {
					st.openChildView(member.ID)
				})
		}
	})
}

// agentMemberActivity is the member card's one line of latest activity: the
// newest tracked team activity, else the retained answer, else the empty state.
func agentMemberActivity(st *state, member contracts.TeamMember) string {
	if len(member.Activity) > 0 {
		if text := strings.TrimSpace(member.Activity[len(member.Activity)-1].Text); text != "" {
			return text
		}
	}
	if text := strings.TrimSpace(member.Last); text != "" {
		return st.tr("上一轮：", "Last: ") + truncateRunes(text, 96)
	}
	return st.tr("还没有活动", "No activity yet")
}

// buildSharedTasksList renders the task board snapshot: an unread board, a
// read error and the empty state stay explicit instead of rendering a blank
// section.
func buildSharedTasksList(c *ui.Context, t *design.Tokens, st *state) {
	board := st.taskBoard()
	if board == nil {
		design.Apply(ui.Text(c, st.tr("任务板未启用", "Task board is not enabled")),
			t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
		return
	}
	if st.agents.tasksErr != "" {
		uikit.ErrorLine(c, st.agents.tasksErr)
		return
	}
	if !st.agents.tasksLoaded {
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Spinner(c).Label(st.tr("读取任务板", "Loading tasks")).
				Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, st.tr("读取任务板…", "Loading tasks…")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
		})
		return
	}
	tasks := st.agents.tasks
	if len(tasks) == 0 {
		design.Apply(ui.Text(c, st.tr("还没有共享任务", "No shared tasks yet")),
			t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
		return
	}
	st.agents.teamTasks.Key = func(index int) any {
		if index < 0 || index >= len(tasks) {
			return nil
		}
		return tasks[index].ID
	}
	st.agents.teamTasks.Label = func(index int) string {
		if index < 0 || index >= len(tasks) {
			return ""
		}
		return tasks[index].Subject
	}
	ui.List(c, &st.agents.teamTasks, len(tasks), func(index int) {
		buildSharedTaskCard(c, t, st, tasks[index])
	}).FillWidth().Gap(design.Space2)
}

// buildSharedTaskCard is one task card: subject and truthfully parsed status,
// then the owner, the blockers and the expected write scopes.
func buildSharedTaskCard(c *ui.Context, t *design.Tokens, st *state, task contracts.BoardTask) {
	status := uikit.ParseTaskStatus(task.Status)
	card := ui.Column(c).FillWidth().Gap(design.Space1).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, task.Subject), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
				TextColor(t.LabelPrimary).Grow(1).Shrink(1).MinWidthPercent(0).
				MaxLines(1).Ellipsis("…")
			uikit.StatusBadge(c, t, status.Kind(), uikit.TaskStatusLabel(st.locale, task.Status))
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Wrap().Gap(design.Space2).Children(func() {
			owner := st.tr("未指派", "Unassigned")
			if trimmed := strings.TrimSpace(task.Owner); trimmed != "" {
				owner = st.tr("负责人 ", "Owner ") + trimmed
			}
			agentTaskMeta(c, t, owner)
			if len(task.BlockedBy) > 0 {
				agentTaskMeta(c, t, st.tr("依赖 ", "Blocked by ")+strings.Join(task.BlockedBy, ", "))
			}
			if len(task.WriteScopes) > 0 {
				agentTaskMeta(c, t, st.tr("写入 ", "Writes ")+strings.Join(task.WriteScopes, ", "))
			}
		})
	})
}

// agentTaskMeta is one muted metadata item of a task card.
func agentTaskMeta(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, text), t.Font(design.TypeXXXS)).
		TextColor(t.LabelTertiary).NoWrap()
}

// buildSubagentEntry is the header's 子智能体 N chip: the count of the active
// session's child agents (subagents plus team members), opening the switcher
// overlay. It renders nothing while the session has no children.
func buildSubagentEntry(c *ui.Context, t *design.Tokens, st *state, activeID string, compact bool) {
	children := st.childAgentService()
	if children == nil {
		return
	}
	agents := children.ChildAgents(activeID)
	if len(agents) == 0 {
		st.agents.childOpen = false
		return
	}
	running := 0
	for _, agent := range agents {
		if strings.EqualFold(strings.TrimSpace(agent.Status), contracts.ChildAgentRunning) {
			running++
		}
	}
	label := fmt.Sprintf(st.tr("子智能体 %d", "Subagents %d"), len(agents))
	tooltip := fmt.Sprintf(st.tr("%d 个会话 · %d 个运行中", "%d conversations · %d running"),
		len(agents), running)
	chip := agentEntryButton(c, t, label, tooltip)
	chip.Children(func() {
		ui.Icon(c, icons.Activity()).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
		if compact {
			design.Apply(ui.Text(c, fmt.Sprintf("%d", len(agents))),
				t.Font(design.TypeXS)).TextColor(t.LabelSecondary).NoWrap()
		} else {
			design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary).NoWrap()
		}
	})
	if chip.Clicked() {
		st.agents.childOpen = !st.agents.childOpen
		st.runtime.Invalidate()
	}
	if !st.agents.childOpen {
		return
	}
	ui.PopoverBase(c, chip, &st.agents.childOpen, func(panel ui.Element) {
		agentPanelStyle(panel, t, design.DialogWidthS)
		buildChildSwitcher(c, t, st, agents)
	})
}

// buildChildSwitcher is the child switcher overlay: every child agent with its
// kind, truthful status, model and last activity, and the 进入 action that
// switches the main content area to that child conversation.
func buildChildSwitcher(c *ui.Context, t *design.Tokens, st *state, agents []contracts.ChildAgent) {
	running := 0
	for _, agent := range agents {
		if strings.EqualFold(strings.TrimSpace(agent.Status), contracts.ChildAgentRunning) {
			running++
		}
	}
	agentPanelHeader(c, t, icons.Activity(), st.tr("子智能体会话", "Child conversations"),
		fmt.Sprintf(st.tr("%d 个运行中 · 共 %d 个", "%d running · %d total"), running, len(agents)))
	ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).
		TrackScroll(&st.agents.childScroll).Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
			st.agents.switcherList.Key = func(index int) any {
				if index < 0 || index >= len(agents) {
					return nil
				}
				return agents[index].ID
			}
			st.agents.switcherList.Label = func(index int) string {
				if index < 0 || index >= len(agents) {
					return ""
				}
				return agentDisplayName(agents[index].Name, agents[index].ID)
			}
			ui.List(c, &st.agents.switcherList, len(agents), func(index int) {
				buildChildSwitcherRow(c, t, st, agents[index])
			}).FillWidth().Gap(design.Space2)
		})
	})
	agentHint(c, t, st.tr("选择后在当前面板打开 · Esc 关闭",
		"Choosing one opens it here · Esc closes"))
}

// buildChildSwitcherRow is one switcher row: state dot, name, kind/mode/model
// and the latest activity, with the 进入 action.
func buildChildSwitcherRow(c *ui.Context, t *design.Tokens, st *state, agent contracts.ChildAgent) {
	name := agentDisplayName(agent.Name, agent.ID)
	kind := uikit.ParseRunState(agent.Status).Kind()
	card := ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3)
	card.Children(func() {
		ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
			Background(agentStatusFill(t, kind)).Margin(design.Space1, 0, 0, 0)
		ui.Column(c).Grow(1).Shrink(1).MinWidthPercent(0).Gap(design.Space1).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				design.Apply(ui.Text(c, childAgentMeta(st, agent)), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
				ui.Spacer(c)
				uikit.StatusText(c, t, design.TypeXXXS, kind,
					uikit.RunStateLabel(st.locale, agent.Status))
			})
			design.Apply(ui.Text(c, childAgentActivityLine(st, agent)), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		})
		agentRowAction(c, t, st.tr("进入", "Open"),
			st.tr("进入子智能体会话 ", "Open child conversation ")+name, func() {
				st.openChildView(agent.ID)
			})
	})
}

// childAgentMeta is the switcher's quiet type line: the kind and the follow-up
// capability, plus the model when the backend names one.
func childAgentMeta(st *state, agent contracts.ChildAgent) string {
	parts := []string{childAgentKindLabel(st, agent.Kind), childAgentModeLabel(st, agent.Mode)}
	if model := strings.TrimSpace(agent.Model); model != "" {
		parts = append(parts, model)
	}
	return strings.Join(parts, " · ")
}

// childAgentKindLabel localizes the two child-agent families.
func childAgentKindLabel(st *state, kind string) string {
	if kind == contracts.ChildAgentTeammate {
		return st.tr("队友", "Teammate")
	}
	return st.tr("子智能体", "Subagent")
}

// childAgentModeLabel localizes the follow-up capability.
func childAgentModeLabel(st *state, mode string) string {
	if mode == contracts.ChildAgentContinuable {
		return st.tr("可继续", "Continuable")
	}
	return st.tr("一次性", "One-shot")
}

// childAgentActivityLine is the switcher row's latest-activity line: the newest
// timeline entry, else the latest output, else the prompt, else the empty state.
func childAgentActivityLine(st *state, agent contracts.ChildAgent) string {
	if len(agent.Activity) > 0 {
		if text := strings.TrimSpace(agent.Activity[len(agent.Activity)-1].Text); text != "" {
			return text
		}
	}
	if text := strings.TrimSpace(agent.Output); text != "" {
		return truncateRunes(text, 96)
	}
	if text := strings.TrimSpace(agent.Prompt); text != "" {
		return truncateRunes(text, 96)
	}
	return st.tr("还没有活动", "No activity yet")
}

// openChildView switches the main content area to one child conversation and
// closes the opening overlay.
func (st *state) openChildView(childID string) {
	childID = strings.TrimSpace(childID)
	if childID == "" {
		return
	}
	st.agents.childID = childID
	st.agents.crumbChosen = -1
	st.agents.teamOpen = false
	st.agents.childOpen = false
	st.agents.childReply = ""
	st.agents.childReplyFor = ""
	st.agents.childSendErr = ""
	st.agents.childDraft = ""
	st.agents.childFocus = true
	st.agents.childScroll = ui.ScrollState{}
	st.runtime.Invalidate()
}

// exitChildView returns the main content area to the parent conversation.
func (st *state) exitChildView() {
	st.agents.childID = ""
	st.agents.crumbChosen = -1
	st.runtime.Invalidate()
}

// --- background jobs --------------------------------------------------------

// buildJobsEntry is the header's background-jobs chip: the retained job count,
// opening the native jobs overlay. It renders nothing while the manager has no
// jobs (the chip is a notification of real work, not a permanent badge).
func buildJobsEntry(c *ui.Context, t *design.Tokens, st *state, compact bool) {
	jobs := st.jobService()
	if jobs == nil {
		return
	}
	list := jobs.Jobs()
	if len(list) == 0 {
		st.agents.jobsOpen = false
		return
	}
	running := 0
	for _, job := range list {
		if job.Running {
			running++
		}
	}
	label := fmt.Sprintf(st.tr("%d 个后台任务", "%d background tasks"), len(list))
	tooltip := fmt.Sprintf(st.tr("%d 个任务 · %d 个运行中", "%d jobs · %d running"),
		len(list), running)
	chip := agentEntryButton(c, t, label, tooltip)
	chip.Children(func() {
		ui.Icon(c, icons.Clock()).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
		if compact {
			design.Apply(ui.Text(c, fmt.Sprintf("%d", len(list))),
				t.Font(design.TypeXS)).TextColor(t.LabelSecondary).NoWrap()
		} else {
			design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary).NoWrap()
		}
	})
	if chip.Clicked() {
		st.agents.jobsOpen = !st.agents.jobsOpen
		st.runtime.Invalidate()
	}
	if !st.agents.jobsOpen {
		return
	}
	ui.PopoverBase(c, chip, &st.agents.jobsOpen, func(panel ui.Element) {
		agentPanelStyle(panel, t, design.DialogWidthCompact)
		buildJobsPanel(c, t, st, jobs, list, running)
	})
}

// buildJobsPanel is the jobs overlay: status, command, duration and exit code
// per job, with the stop action of a running job.
func buildJobsPanel(c *ui.Context, t *design.Tokens, st *state, jobs contracts.JobService,
	list []contracts.BackgroundJob, running int) {
	agentPanelHeader(c, t, icons.Clock(), st.tr("后台任务", "Background jobs"),
		fmt.Sprintf(st.tr("%d 个任务 · %d 个运行中", "%d jobs · %d running"), len(list), running))
	if st.agents.jobErr != "" {
		uikit.ErrorLine(c, st.agents.jobErr)
	}
	ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).
		TrackScroll(&st.agents.jobsScroll).Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
			st.agents.jobsList.Key = func(index int) any {
				if index < 0 || index >= len(list) {
					return nil
				}
				return list[index].ID
			}
			st.agents.jobsList.Label = func(index int) string {
				if index < 0 || index >= len(list) {
					return ""
				}
				return backgroundJobCommand(list[index], st)
			}
			ui.List(c, &st.agents.jobsList, len(list), func(index int) {
				buildBackgroundJobRow(c, t, st, jobs, list[index])
			}).FillWidth().Gap(design.Space2)
		})
	})
	agentHint(c, t, st.tr("停止会终止任务的进程 · Esc 关闭",
		"Stopping ends the job's process · Esc closes"))
}

// backgroundJobStatus maps the rig job state onto the product's status
// vocabulary: an exited job is a success when its exit code is 0, and every
// other non-running terminal state is a failure (killed jobs are cancelled).
func backgroundJobStatus(st *state, job contracts.BackgroundJob) (uikit.StatusKind, string) {
	switch strings.ToLower(strings.TrimSpace(job.State)) {
	case "running":
		return uikit.StatusRunning, st.tr("运行中", "Running")
	case "killed":
		return uikit.StatusCancelled, st.tr("已取消", "Cancelled")
	case "timed_out":
		return uikit.StatusError, st.tr("超时", "Timed out")
	case "failed":
		return uikit.StatusError, st.tr("失败", "Failed")
	case "exited":
		if job.ExitCode != 0 {
			return uikit.StatusError, st.tr("失败", "Failed")
		}
		return uikit.StatusSuccess, st.tr("完成", "Completed")
	default:
		label := strings.TrimSpace(job.State)
		if label == "" {
			label = st.tr("未知", "Unknown")
		}
		return uikit.StatusUnknown, label
	}
}

// backgroundJobCommand is the row's readable command (the manager's free-text
// command line), falling back to the job id.
func backgroundJobCommand(job contracts.BackgroundJob, st *state) string {
	command := strings.TrimSpace(job.Command)
	if command == "" {
		return job.ID
	}
	return command
}

// backgroundJobExitLabel is the exit-code column: the code of a finished job,
// an honest dash while it still runs.
func backgroundJobExitLabel(job contracts.BackgroundJob) string {
	// A running job has no exit code yet; a killed/timed-out process reports a
	// negative sentinel from the manager, which is also not an exit code.
	if job.Running || job.ExitCode < 0 {
		return "—"
	}
	return fmt.Sprintf("%d", job.ExitCode)
}

// buildBackgroundJobRow is one job row: status badge, command, duration, exit
// code and the stop action of a running job.
func buildBackgroundJobRow(c *ui.Context, t *design.Tokens, st *state,
	jobs contracts.JobService, job contracts.BackgroundJob) {
	kind, label := backgroundJobStatus(st, job)
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3)
	row.Children(func() {
		uikit.StatusBadge(c, t, kind, label)
		command := backgroundJobCommand(job, st)
		design.Apply(ui.Text(c, command), t.MonoText(design.TypeXXXS)).
			TextColor(t.LabelSecondary).Grow(1).Shrink(1).MinWidthPercent(0).
			MaxLines(1).Ellipsis("…").Tooltip(command)
		if job.Duration > 0 {
			design.Apply(ui.Text(c, st.tr("用时 ", "Took ")+formatUsageDuration(job.Duration.Milliseconds())),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
		}
		design.Apply(ui.Text(c, st.tr("退出码 ", "Exit ")+backgroundJobExitLabel(job)),
			t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
		if job.Running {
			stopText := st.tr("停止", "Stop")
			if st.agents.jobKilling == job.ID {
				stopText = st.tr("停止中…", "Stopping…")
			}
			stop := ui.Button(c, stopText).Label(st.tr("停止后台任务 ", "Stop background task ") + job.ID)
			design.Apply(stop, t.Font(design.TypeXXXS))
			stop.Height(agentRowActionHeight).PaddingX(design.Space2).
				Radius(design.RadiusSM).Shrink(0)
			stop.Disabled(st.agents.jobKilling != "")
			if stop.Clicked() {
				st.killBackgroundJob(jobs, job.ID)
			}
		}
	})
}

// killBackgroundJob stops one job off the UI thread (the job tool waits a
// grace period for the process to be reaped) and publishes the result at the
// next frame.
func (st *state) killBackgroundJob(jobs contracts.JobService, jobID string) {
	if jobs == nil || st.agents.jobKilling != "" {
		return
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return
	}
	st.agents.jobKilling = jobID
	st.agents.jobErr = ""
	runtime := st.runtime
	go func() {
		err := jobs.KillJob(jobID)
		runtime.Post(func() {
			st.agents.jobKilling = ""
			if err != nil {
				st.agents.jobErr = st.tr("停止失败：", "Stop failed: ") + err.Error()
			}
			runtime.Invalidate()
		})
	}()
}

// --- child conversation view ------------------------------------------------

// buildChildView renders the main content area's child conversation: the prompt,
// the run state, the output and the activity of one subagent or teammate. A
// continuable teammate gets the message input that calls SendChildAgent; a
// one-shot subagent stays read-only. The header shows the parent > child
// breadcrumb instead of the title.
func buildChildView(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, cfg Config) {
	children := st.childAgentService()
	var child contracts.ChildAgent
	found := false
	if children != nil && strings.TrimSpace(st.agents.childID) != "" {
		child, found = children.ChildAgent(st.agents.childID)
	}
	if !found {
		buildMissingChildView(c, t, st)
		return
	}
	switch st.tab {
	case tabTrajectory:
		buildChildActivityPane(c, t, st, child)
	case tabMemory:
		buildChildMemoryPane(c, t, st, child)
	default:
		buildChildConversationPane(c, t, runtime, st, cfg, child)
	}
}

// buildMissingChildView is the honest empty state of a child that disappeared
// under the view (a closed teammate, a backend restart): the breadcrumb and a
// labeled return action stay.
func buildMissingChildView(c *ui.Context, t *design.Tokens, st *state) {
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Row(c).FillWidth().Grow(1).Justify(ui.Center).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
				Gap(design.Space3).PaddingY(design.Space8).AlignItems(ui.Start).Children(func() {
				uikit.EmptyState(c, st.tr("子智能体已不存在", "This child agent no longer exists"))
				if button := design.Apply(ui.Button(c, st.tr("返回主会话", "Back to the conversation")),
					t.SecondaryButton()); button.Clicked() {
					st.exitChildView()
				}
			})
		})
	})
}

// buildChildConversationPane is the child's 对话 tab: the prompt, the run state,
// the output (or the failure), the activity timeline, and — for a continuable
// teammate — the send box.
func buildChildConversationPane(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, cfg Config, child contracts.ChildAgent) {
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).TrackScroll(&st.agents.childScroll).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
					Gap(design.Space4).PaddingY(design.Space5).Children(func() {
					buildChildHeader(c, t, st, child)
					if prompt := strings.TrimSpace(child.Prompt); prompt != "" {
						buildChildTextSection(c, t, st.tr("任务", "Prompt"), prompt)
					}
					buildChildOutput(c, t, st, child)
					buildChildActivitySection(c, t, st, child)
					if child.Mode != contracts.ChildAgentContinuable {
						agentHint(c, t, st.tr("一次性子智能体：只读会话，不能继续对话。",
							"One-shot subagent: read-only, it cannot be continued."))
					}
				})
			})
		})
		if child.Mode == contracts.ChildAgentContinuable {
			buildChildComposer(c, t, runtime, st, child)
		}
	})
}

// buildChildHeader is the child view's status line: the truthful run state, the
// agent family, the model, the usage and the elapsed time.
func buildChildHeader(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	kind := uikit.ParseRunState(child.Status).Kind()
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			uikit.StatusBadge(c, t, kind, uikit.RunStateLabel(st.locale, child.Status))
			design.Apply(ui.Text(c, childAgentMeta(st, child)), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary)
			ui.Spacer(c)
			if meta := childAgentUsageSummary(st, child, c.Now()); meta != "" {
				design.Apply(ui.Text(c, meta), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
		})
		if strings.TrimSpace(child.ID) != "" {
			design.Apply(ui.Text(c, child.ID), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).Selectable()
		}
	})
}

// childAgentUsageSummary is the child header's compact accounting: the elapsed
// run (or the finished duration), the iterations and the accumulated tokens.
func childAgentUsageSummary(st *state, child contracts.ChildAgent, now time.Time) string {
	var parts []string
	if !child.Started.IsZero() {
		end := child.Finished
		if end.IsZero() {
			end = now
		}
		if duration := end.Sub(child.Started); duration > 0 {
			parts = append(parts, st.tr("用时 ", "Took ")+formatUsageDuration(duration.Milliseconds()))
		}
	}
	if child.Iterations > 0 {
		parts = append(parts, fmt.Sprintf(st.tr("%d 步", "%d iterations"), child.Iterations))
	}
	if child.UsageTokens > 0 {
		parts = append(parts, uikit.FormatCompactCount(child.UsageTokens)+" tokens")
	}
	return strings.Join(parts, " · ")
}

// buildChildTextSection renders one titled plain-text block (the prompt and the
// latest reply).
func buildChildTextSection(c *ui.Context, t *design.Tokens, title, body string) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		agentSectionLabel(c, t, title)
		design.Apply(ui.Text(c, body), t.Font(design.TypeBody)).
			TextColor(t.LabelPrimary).Selectable()
	})
}

// buildChildOutput renders the child's latest answer as Markdown (the models
// answer in Markdown), the failure as the shared error line, or the honest
// running/empty state.
func buildChildOutput(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	if strings.TrimSpace(child.Error) != "" {
		agentSectionLabel(c, t, st.tr("错误", "Error"))
		uikit.ErrorLine(c, child.Error)
		return
	}
	output := strings.TrimSpace(child.Output)
	if output == "" {
		message := st.tr("还没有输出", "No output yet")
		if strings.EqualFold(strings.TrimSpace(child.Status), contracts.ChildAgentRunning) {
			message = st.tr("正在运行…", "Running…")
		}
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			agentSectionLabel(c, t, st.tr("输出", "Output"))
			design.Apply(ui.Text(c, message), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary)
		})
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		agentSectionLabel(c, t, st.tr("输出", "Output"))
		markdown.View(c, output, st.markdownOptions(t))
	})
	if st.agents.childReplyFor == child.ID && strings.TrimSpace(st.agents.childReply) != "" {
		buildChildTextSection(c, t, st.tr("最新回复", "Latest reply"), st.agents.childReply)
	}
}

// buildChildActivitySection is the child's bounded activity timeline. A
// one-shot subagent's registry records no timeline, and that stays explicit
// instead of inventing entries.
func buildChildActivitySection(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		agentSectionLabel(c, t, st.tr("活动", "Activity"))
		if len(child.Activity) == 0 {
			design.Apply(ui.Text(c, st.tr("还没有活动记录", "No activity recorded yet")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
			return
		}
		st.agents.activityList.Key = func(index int) any {
			if index < 0 || index >= len(child.Activity) {
				return nil
			}
			return child.Activity[index].ID
		}
		st.agents.activityList.Label = func(index int) string {
			if index < 0 || index >= len(child.Activity) {
				return ""
			}
			return child.Activity[index].Text
		}
		activity := child.Activity
		ui.List(c, &st.agents.activityList, len(activity), func(index int) {
			buildChildActivityRow(c, t, st, activity[index])
		}).FillWidth()
	})
}

// buildChildActivityRow is one timeline row: the kind, the text and the clock.
func buildChildActivityRow(c *ui.Context, t *design.Tokens, st *state, entry contracts.ChildAgentActivity) {
	color := t.LabelSecondary
	if entry.Error {
		color = t.DangerText
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingY(design.Space1).Children(func() {
		design.Apply(ui.Text(c, messageClock(st.locale, entry.Time)),
			t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
		design.Apply(ui.Text(c, childActivityKindLabel(st, entry.Kind)),
			t.Font(design.TypeXXXS)).FontWeight(agentHeadingWeight).TextColor(color).NoWrap()
		design.Apply(ui.Text(c, entry.Text), t.Font(design.TypeXXXS)).
			TextColor(color).Grow(1).Shrink(1).MinWidthPercent(0).MaxLines(2).
			Tooltip(entry.Text)
	})
}

// childActivityKindLabel localizes the team activity kinds shown in the child
// timeline.
func childActivityKindLabel(st *state, kind string) string {
	switch kind {
	case contracts.TeamActivitySpawn:
		return st.tr("派发", "Spawn")
	case contracts.TeamActivitySend:
		return st.tr("发送", "Send")
	case contracts.TeamActivityWait:
		return st.tr("等待", "Wait")
	case contracts.TeamActivityFinish:
		return st.tr("完成", "Finish")
	case contracts.TeamActivityError:
		return st.tr("失败", "Error")
	case contracts.TeamActivityInterrupt:
		return st.tr("中断", "Interrupt")
	case contracts.TeamActivityClose:
		return st.tr("关闭", "Close")
	default:
		return st.tr("工具", "Tool")
	}
}

// buildChildActivityPane is the child's 轨迹 tab: the activity timeline alone.
func buildChildActivityPane(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).TrackScroll(&st.agents.childScroll).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
					Gap(design.Space3).PaddingY(design.Space5).Children(func() {
					buildChildHeader(c, t, st, child)
					buildChildActivitySection(c, t, st, child)
				})
			})
		})
	})
}

// buildChildMemoryPane is the child's 记忆 tab: a child conversation has no
// memory surface, and saying so is honest.
func buildChildMemoryPane(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
			Gap(design.Space2).PaddingY(design.Space8).Children(func() {
			agentSectionLabel(c, t, st.tr("记忆", "Memory"))
			design.Apply(ui.Text(c, st.tr("子智能体会话没有记忆面板。",
				"Child conversations have no memory panel.")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
		})
	})
}

// buildChildComposer is the continuable teammate's message box: Enter or 发送
// calls ChildAgentService.SendChildAgent off the UI thread and shows the
// answer it returns.
func buildChildComposer(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, child contracts.ChildAgent) {
	children := st.childAgentService()
	if children == nil {
		return
	}
	name := agentDisplayName(child.Name, child.ID)
	ui.Row(c).FillWidth().Justify(ui.Center).PaddingY(design.Space3).Children(func() {
		ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
			Gap(design.Space2).Children(func() {
			if st.agents.childSendErr != "" {
				uikit.ErrorLine(c, st.agents.childSendErr)
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				input := ui.TextInput(c, &st.agents.childDraft).
					Label(st.tr("发送给 ", "Send to ") + name).
					Placeholder(st.tr("发消息给该队友…", "Message this teammate…")).
					Bind(&st.agents.childInput).FillWidth().Grow(1)
				if st.agents.childFocus {
					st.agents.childFocus = false
					st.agents.childInput.Focus(c)
				}
				submitted := input.Submitted()
				send := design.Apply(ui.PrimaryButton(c, st.tr("发送", "Send")), t.PrimaryButton()).
					Label(st.tr("发送消息给 ", "Send message to ") + name).Shrink(0)
				send.Disabled(strings.TrimSpace(st.agents.childDraft) == "" || st.agents.childSending)
				clicked := send.Clicked()
				if st.agents.childSending {
					ui.Spinner(c).Label(st.tr("发送中", "Sending")).
						Size(design.Space4, design.Space4).TextColor(t.Accent)
				}
				if (submitted || clicked) && !st.agents.childSending {
					st.sendChildMessage(children, child.ID)
				}
			})
			agentHint(c, t, st.tr("发送后等待该队友的回复。", "The teammate answers when its next run finishes."))
		})
	})
}

// sendChildMessage delivers one follow-up message to a continuable child off
// the UI thread (the backend waits for the teammate's answer) and posts the
// result back for the frame.
func (st *state) sendChildMessage(children contracts.ChildAgentService, childID string) {
	message := strings.TrimSpace(st.agents.childDraft)
	if children == nil || message == "" || st.agents.childSending {
		return
	}
	st.agents.childSending = true
	st.agents.childSendErr = ""
	st.agents.childDraft = ""
	runtime := st.runtime
	go func() {
		answer, err := children.SendChildAgent(context.Background(), childID, message)
		runtime.Post(func() {
			st.agents.childSending = false
			if err != nil {
				st.agents.childSendErr = st.tr("发送失败：", "Send failed: ") + err.Error()
			} else {
				st.agents.childReply = strings.TrimSpace(answer)
				st.agents.childReplyFor = childID
			}
			st.agents.childFocus = true
			runtime.Invalidate()
		})
	}()
}

// --- child breadcrumb -------------------------------------------------------

// buildChildBreadcrumb is the header's title slot in the child view: the parent
// conversation title as a native breadcrumb link, the child's name as the
// current page, and a switcher entry that reopens the child list. Clicking the
// parent crumb returns to the conversation (the DSH lineage contract).
func buildChildBreadcrumb(c *ui.Context, t *design.Tokens, st *state, chat contracts.ChatService, cfg Config) {
	parent := sessionTitle(st, chat, cfg)
	childName := childAgentDisplayName(st, st.agents.childID)
	crumbs := ui.Breadcrumbs(c, []string{parent, childName}, &st.agents.crumbChosen).
		Label(st.tr("会话路径", "Conversation path")).Shrink(1)
	design.Apply(crumbs, t.Font(design.TypeS))
	if crumbs.Changed() && st.agents.crumbChosen == 0 {
		st.exitChildView()
	}
	th := t.Theme()
	switchChip := ui.Button(c, "").Label(st.tr("切换子智能体会话", "Switch child conversation")).
		Tooltip(st.tr("切换子智能体会话", "Switch child conversation")).
		Height(t.Scale(th.Space(7))).PaddingX(design.Space2).
		Radius(design.RadiusSM).Shrink(0).Gap(design.Space1)
	switchChip.Children(func() {
		ui.Icon(c, icons.ChevronDown()).Size(design.Space3, design.Space3).
			TextColor(t.LabelTertiary)
	})
	if switchChip.Clicked() {
		st.agents.childOpen = !st.agents.childOpen
		st.runtime.Invalidate()
	}
	children := st.childAgentService()
	if !st.agents.childOpen || children == nil {
		return
	}
	ui.PopoverBase(c, switchChip, &st.agents.childOpen, func(panel ui.Element) {
		agentPanelStyle(panel, t, design.DialogWidthS)
		buildChildSwitcher(c, t, st, children.ChildAgents(chat.ActiveSession()))
	})
}

// childAgentDisplayName resolves the child's display name for the breadcrumb
// and the switcher: the service name when available, else the stable ID.
func childAgentDisplayName(st *state, childID string) string {
	childID = strings.TrimSpace(childID)
	if childID == "" {
		return ""
	}
	if children := st.childAgentService(); children != nil {
		if child, ok := children.ChildAgent(childID); ok {
			return agentDisplayName(child.Name, child.ID)
		}
	}
	return childID
}

// agentDisplayName falls back to the stable ID when the backend has no display
// name (an unnamed delegation).
func agentDisplayName(name, id string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(id)
}

// --- generated-file turn-tail cards ----------------------------------------

// generatedFile is one file a turn's tool calls produced: the path the cards
// open, the display name and the directory part (both derived from the path).
type generatedFile struct {
	Path string
	Name string
	Dir  string
}

// generatedFiles is one finished turn's file output, split into the files the
// agent delivered (written) and the files it edited.
type generatedFiles struct {
	delivered []generatedFile
	edited    []generatedFile
}

// newGeneratedFile splits one path into the display name and the directory.
func newGeneratedFile(path string) generatedFile {
	path = strings.TrimSpace(path)
	file := generatedFile{Path: path, Name: path}
	if index := strings.LastIndex(path, "/"); index >= 0 {
		file.Dir = path[:index+1]
		file.Name = path[index+1:]
	}
	return file
}

// tailTurnMessages returns the assistant messages of the current (last) turn:
// the messages after the last user message.
func tailTurnMessages(messages []contracts.ChatMessage) []contracts.ChatMessage {
	start := 0
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == contracts.ChatRoleUser {
			start = index + 1
			break
		}
	}
	if start >= len(messages) {
		return nil
	}
	return messages[start:]
}

// turnGeneratedFiles collects the current turn's file-producing calls:
// write_file paths are the delivered files, edit_file/str_replace_editor paths
// the edited files. A path appears once; a file both written and edited counts
// as delivered. Present-tool artifacts carry no filesystem path and are not
// part of these cards.
func turnGeneratedFiles(messages []contracts.ChatMessage) generatedFiles {
	var files generatedFiles
	delivered := make(map[string]bool)
	edited := make(map[string]bool)
	for _, message := range messages {
		if message.Role != contracts.ChatRoleAssistant {
			continue
		}
		for _, call := range message.Tools {
			path := generatedFileFromToolCall(call)
			if path == "" {
				continue
			}
			switch generatedFileTool(call.Name) {
			case generatedToolWrite:
				if delivered[path] {
					continue
				}
				delivered[path] = true
				files.delivered = append(files.delivered, newGeneratedFile(path))
			case generatedToolEdit:
				if delivered[path] || edited[path] {
					continue
				}
				edited[path] = true
				files.edited = append(files.edited, newGeneratedFile(path))
			}
		}
	}
	return files
}

// The file-producing tool families the cards know: the rig file tools.
const (
	generatedToolWrite = "write_file"
	generatedToolEdit  = "edit_file"
	generatedToolNone  = ""
)

// generatedFileTool classifies one tool call name into the card's write/edit
// families (an unknown tool produces no card).
func generatedFileTool(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case generatedToolWrite:
		return generatedToolWrite
	case "edit_file", "str_replace_editor":
		return generatedToolEdit
	default:
		return generatedToolNone
	}
}

// generatedFileFromToolCall reads the file path out of the call's JSON
// arguments (the rig file tools use "path"; the aliases keep MCP-shaped tools
// working). A call without a readable path is skipped instead of inventing one.
func generatedFileFromToolCall(call contracts.ToolCall) string {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return ""
	}
	for _, key := range []string{"path", "file_path", "filePath"} {
		value, ok := values[key].(string)
		if !ok {
			continue
		}
		path := strings.TrimSpace(value)
		if path == "" {
			continue
		}
		return strings.ReplaceAll(path, "\\", "/")
	}
	return ""
}

// buildGeneratedFileCards renders the turn-tail cards under a finished answer:
// 交付文件 (written files) and 已编辑 N 个文件 (edited files), each row offering
// the sidebar preview, the change view (the same file tab until the previewer
// grows a diff entry point) and the native open action.
func buildGeneratedFileCards(c *ui.Context, t *design.Tokens, st *state, files generatedFiles) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		if len(files.delivered) > 0 {
			buildGeneratedFileCard(c, t, st, icons.Check(), st.tr("交付文件", "Delivered files"),
				fmt.Sprintf(st.tr("%d 个", "%d files"), len(files.delivered)),
				files.delivered)
		}
		if len(files.edited) > 0 {
			buildGeneratedFileCard(c, t, st, icons.Pencil(),
				fmt.Sprintf(st.tr("已编辑 %d 个文件", "Edited %d files"), len(files.edited)),
				"", files.edited)
		}
	})
}

// buildGeneratedFileCard is one card: a titled panel with one action row per
// file. The rows stay even when the host has no previewer (the file list is
// conversation data); the sidebar actions are then left out instead of shown
// dead.
func buildGeneratedFileCard(c *ui.Context, t *design.Tokens, st *state, icon *ui.SVG,
	title, count string, files []generatedFile) {
	card := design.Apply(ui.Column(c), t.Panel()).FillWidth().
		Padding(design.Space2, design.Space3).Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
			PaddingX(design.Space1).Children(func() {
			ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
				TextColor(t.LabelPrimary)
			if count != "" {
				design.Apply(ui.Text(c, count), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary)
			}
		})
		for _, file := range files {
			buildGeneratedFileRow(c, t, st, file)
		}
	})
}

// buildGeneratedFileRow is one file row: name, directory and the three
// actions.
func buildGeneratedFileRow(c *ui.Context, t *design.Tokens, st *state, file generatedFile) {
	previewer := st.filePreviewer()
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingX(design.Space1).Children(func() {
		ui.Icon(c, icons.File()).Size(design.Space3, design.Space3).
			TextColor(t.LabelTertiary)
		design.Apply(ui.Text(c, file.Name), t.Font(design.TypeXXXS)).FontWeight(agentHeadingWeight).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Tooltip(file.Path)
		if file.Dir != "" {
			design.Apply(ui.Text(c, file.Dir), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
		ui.Spacer(c)
		if previewer == nil {
			return
		}
		agentRowAction(c, t, st.tr("在侧边栏预览", "Preview"),
			st.tr("在侧边栏预览 ", "Preview in the sidebar ")+file.Path, func() {
				st.previewGeneratedFile(previewer, file.Path, false)
			})
		// The previewer has no diff entry point yet: the same file tab shows
		// the working-tree diff where the right sidebar supports it, so the
		// change action opens the same file.
		agentRowAction(c, t, st.tr("查看改动", "View changes"),
			st.tr("查看改动 ", "View changes ")+file.Path, func() {
				st.previewGeneratedFile(previewer, file.Path, false)
			})
		agentRowAction(c, t, st.tr("用系统应用打开", "Open natively"),
			st.tr("用系统应用打开 ", "Open natively ")+file.Path, func() {
				st.previewGeneratedFile(previewer, file.Path, true)
			})
	})
}

// previewGeneratedFile asks the right sidebar to open (or focus) one file, or
// hands it to the platform; a failure surfaces as an error toast instead of a
// silent no-op.
func (st *state) previewGeneratedFile(previewer contracts.FilePreviewer, path string, native bool) {
	if previewer == nil {
		return
	}
	var err error
	if native {
		err = previewer.OpenNative(path)
	} else {
		err = previewer.PreviewFile(path)
	}
	if err != nil {
		st.toast(contracts.NotifyError, st.tr("打开文件失败：", "Open file failed: ")+err.Error())
	}
}

// --- shared header title ----------------------------------------------------

// sessionTitle resolves the active conversation's title (the configured default
// when the session has none); the header, the breadcrumb and the parent crumb
// all read the same name.
func sessionTitle(st *state, chat contracts.ChatService, cfg Config) string {
	title := st.localized(cfg.Title, "对话", "Chat")
	if chat == nil {
		return title
	}
	if session, ok := chat.Session(chat.ActiveSession()); ok && strings.TrimSpace(session.Title) != "" {
		title = session.Title
	}
	return title
}
