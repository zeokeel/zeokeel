package shell

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// goal returns the live session's goal, or nil in static mode. The controller
// folds the goal tool's structured result into per-session state, so the
// banner always shows the objective the agent last reported.
func (a *App) goal() *tools.Goal {
	if a.Ctrl == nil {
		return nil
	}
	return a.Ctrl.Goal()
}

// SetTeamRoster registers the app's live asynchronous team roster. The Team
// panel appends those members to the synchronous subagent calls the controller
// folded into the session, so spawn_team members appear while they run.
func (a *App) SetTeamRoster(fn func() []tools.Teammate) { a.teamRoster = fn }

// SetTeamStop registers the app's hook that cancels a session's asynchronous
// team members after the user stops its turn. Nil keeps stopping local only.
func (a *App) SetTeamStop(fn func(sessionID string)) { a.teamStop = fn }

// teammates returns the live session's teammates: the completed synchronous
// subagent calls first (oldest first), then the asynchronous team members from
// the app's live roster. A member repeated by both sources is shown once.
func (a *App) teammates() []tools.Teammate {
	if a.Ctrl == nil {
		return nil
	}
	members := a.Ctrl.Team()
	if a.teamRoster == nil {
		return members
	}
	seen := make(map[string]bool, len(members))
	for _, member := range members {
		if member.CallID != "" {
			seen[member.CallID] = true
		}
	}
	live := a.teamRoster()
	out := make([]tools.Teammate, 0, len(members)+len(live))
	out = append(out, members...)
	for _, member := range live {
		if member.CallID != "" && seen[member.CallID] {
			continue
		}
		out = append(out, member)
	}
	return out
}

// stopTurn stops the live session's turn and then stops the asynchronous team
// members it spawned. The team stop runs off the view goroutine: it waits for
// the members to settle, while Controller.Stop returns immediately.
func (a *App) stopTurn() {
	if a.Ctrl == nil {
		return
	}
	sessionID := a.Ctrl.CurrentSessionID()
	a.Ctrl.Stop()
	if a.teamStop == nil || sessionID == "" {
		return
	}
	stop := a.teamStop
	go stop(sessionID)
}

// goalStatusLabel is the banner's status word.
func (a *App) goalStatusLabel(status tools.GoalStatus) string {
	switch status {
	case tools.GoalCompleted:
		return a.T("team.completed")
	case tools.GoalAbandoned:
		return a.T("team.abandoned")
	default:
		return a.T("team.in_progress")
	}
}

// teamStatusLabel localizes a teammate's raw tool status.
func (a *App) teamStatusLabel(status string) string {
	key := "team.status." + status
	if !hasTranslation(key) {
		return status
	}
	return a.T(key)
}

// goalProgress clamps a goal's percent into 0-100 so a hand-written tool result
// cannot render a broken bar.
func goalProgress(goal *tools.Goal) int {
	if goal == nil {
		return 0
	}
	progress := goal.Progress
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

// teamGoalBanner shows the session goal above the conversation: the objective,
// its progress percent, a thin progress bar and, when the tool reported steps,
// how many there are. It renders nothing without a goal or for an abandoned
// one, so a normal session keeps the full-height transcript.
func (a *App) teamGoalBanner(c *ui.Context, t *design.Tokens) {
	goal := a.goal()
	if goal == nil || strings.TrimSpace(goal.Objective) == "" {
		return
	}
	if goal.Status == tools.GoalAbandoned {
		return
	}
	progress := goalProgress(goal)
	color := t.Accent
	if goal.Status == tools.GoalCompleted {
		color = t.Success
	}
	th := t.Theme()
	ui.Column(c).Key("goal-banner").Gap(th.Space(2)).
		Padding(th.Space(3)).Radius(th.Radius).
		Background(t.BgHover).Children(func() {
		ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.T("team.goal")).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelTertiary).Grow(1)
			ui.Text(c, a.goalStatusLabel(goal.Status)).FontSize(10).
				FixedLineHeight(16).TextColor(t.LabelTertiary).NoWrap()
			ui.Text(c, a.T("team.progress", progress)).FontSize(11).
				FixedLineHeight(16).FontWeight(600).TextColor(color).NoWrap()
		})
		ui.Text(c, goal.Objective).FontSize(13).FixedLineHeight(20).
			TextColor(t.LabelPrimary).MaxLines(2).Ellipsis("…")
		// The progress bar spans the banner instead of a fixed sample width,
		// so it follows the transcript measure at every window size.
		track := ui.Row(c).FillWidth().Height(th.Space(1)).
			Radius(design.RadiusMax).Background(t.BorderL2)
		track.Children(func() {
			fill := ui.Box(c).Height(th.Space(1)).Radius(design.RadiusMax).
				WidthPercent(float32(progress)).Background(color)
			_ = fill
		})
		if len(goal.Steps) > 0 {
			ui.Text(c, a.T("team.steps", len(goal.Steps))).FontSize(10).
				FixedLineHeight(16).TextColor(t.LabelTertiary)
		}
	})
}

// teamPanel lists the teammates the session spawned: completed synchronous
// subagent calls plus the app's live asynchronous team members. It renders
// when either source has at least one member. The header also carries the
// current agent mode.
func (a *App) teamPanel(c *ui.Context, t *design.Tokens) {
	members := a.teammates()
	if len(members) == 0 {
		return
	}
	th := t.Theme()
	ui.Column(c).Key("team-panel").Width(th.Space(68)).Shrink(0).
		Padding(th.Space(4)).Gap(th.Space(2)).Background(t.BgSidebar).
		BorderWidth(0, 0, 0, 1).BorderColor(t.BorderL2).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(th.Space(2)).Children(func() {
			ui.Text(c, a.T("team.team")).FontSize(12).FixedLineHeight(18).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
			a.agentModeSummary(c, t)
			count := ui.Row(c).Height(th.Space(5)).PaddingX(th.Space(1.5)).
				Radius(design.RadiusMax).Background(t.BgHover).AlignItems(ui.Center)
			count.Children(func() {
				ui.Text(c, a.T("team.count", len(members))).FontSize(11).
					FixedLineHeight(16).TextColor(t.LabelTertiary)
			})
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(th.Space(1)).Children(func() {
				for _, member := range members {
					a.teammateRow(c, t, member)
				}
			})
		})
	})
}

// teammateRow draws one teammate: a glyph, its name and status, and the task
// it was given. The status word is the tool's or the team manager's own status
// (completed, running, queued, failed, interrupted).
func (a *App) teammateRow(c *ui.Context, t *design.Tokens, member tools.Teammate) {
	th := t.Theme()
	color := t.LabelTertiary
	statusColor := t.LabelTertiary
	switch member.Status {
	case tools.SubagentStatusCompleted:
		color, statusColor = t.Success, t.Success
	case "running":
		color, statusColor = t.Accent, t.Accent
	case "failed":
		color, statusColor = t.Danger, t.Danger
	case "interrupted":
		color, statusColor = t.WarningText, t.WarningText
	}
	card := ui.Column(c).FillWidth().Gap(th.Space(1)).
		Padding(th.Space(2)).Radius(design.RadiusSM).
		Background(t.BgRaised).Border(1, t.BorderL2)
	card.Children(func() {
		ui.Row(c).Gap(th.Space(1.5)).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(th.Space(1.5), th.Space(1.5)).Radius(th.Space(0.75)).
				Background(color).Shrink(0)
			ui.Text(c, member.Name).FontSize(12).FixedLineHeight(18).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).
				NoWrap().Ellipsis("…")
			ui.Text(c, a.teamStatusLabel(member.Status)).FontSize(10).
				FixedLineHeight(16).TextColor(statusColor).NoWrap()
		})
		if member.Task != "" {
			ui.Text(c, member.Task).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelSecondary).MaxLines(2).Ellipsis("…")
		}
	})
}
