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

// teammates returns the subagents the live session spawned, oldest first.
func (a *App) teammates() []tools.Teammate {
	if a.Ctrl == nil {
		return nil
	}
	return a.Ctrl.Team()
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

// teamPanel lists the subagents the session spawned. It renders only when at
// least one subagent tool result was folded into the session, matching the todo
// panel's behavior. The header also carries the current agent mode.
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

// teammateRow draws one subagent: a glyph, its name and status, and the task it
// was given. The status word is the tool's own status ("completed").
func (a *App) teammateRow(c *ui.Context, t *design.Tokens, member tools.Teammate) {
	th := t.Theme()
	color := t.LabelTertiary
	statusColor := t.LabelTertiary
	if member.Status == tools.SubagentStatusCompleted {
		color, statusColor = t.Success, t.Success
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
