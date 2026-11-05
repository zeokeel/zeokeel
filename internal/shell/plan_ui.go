package shell

import (
	"context"
	"errors"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// planArtifactTitle is the title the plan:policy prompt asks the agent to use
// for the reviewable plan. The shell matches it case-insensitively after
// trimming, so "Plan" and "plan" both render the review card while a longer
// title such as "Plan for auth" does not.
const planArtifactTitle = "Plan"

// planApprovedPrompt is the single continuation submitted when the user
// approves a plan. Approve clears plan mode first, then sends this prompt to
// the same session, so the next turn executes under the normal (mutating)
// tool policy.
const planApprovedPrompt = "Plan approved. Execute the plan."

// planModeOn reports whether the current session is in plan mode. A static app
// (Ctrl == nil) is never in plan mode, matching the runtime gate.
func (a *App) planModeOn() bool {
	if a.Ctrl == nil {
		return false
	}
	return a.Ctrl.PlanMode(a.Ctrl.CurrentSessionID())
}

// planReviewArtifact returns the newest presented artifact titled "Plan", or
// nil when plan mode is off or no such artifact exists. Only the current
// session's artifacts are considered, like the transcript's artifact list.
func (a *App) planReviewArtifact() *tools.Artifact {
	if !a.planModeOn() {
		return nil
	}
	artifacts := a.artifacts()
	for i := len(artifacts) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(artifacts[i].Title), planArtifactTitle) {
			artifact := artifacts[i]
			return &artifact
		}
	}
	return nil
}

// togglePlanMode flips the current session's persisted plan mode. It reports an
// error to the caller (shown as a toast) instead of dropping it.
func (a *App) togglePlanMode() error {
	if a.Ctrl == nil {
		return nil
	}
	id := a.Ctrl.CurrentSessionID()
	if strings.TrimSpace(id) == "" {
		return errors.New(a.menuText("Open a session before enabling plan mode.",
			"请先打开会话再启用计划模式。"))
	}
	return a.Ctrl.SetPlanMode(id, !a.Ctrl.PlanMode(id))
}

// approvePlan is the Plan review card's primary action: turn plan mode off and
// submit exactly one continuation to the same session. The plan-mode check
// before submitting makes a second click (or a stale render of the card) a
// no-op instead of a duplicate turn.
func (a *App) approvePlan() error {
	if a.Ctrl == nil {
		return nil
	}
	id := a.Ctrl.CurrentSessionID()
	if strings.TrimSpace(id) == "" {
		return errors.New(a.menuText("No session to continue.", "没有可继续的会话。"))
	}
	if !a.Ctrl.PlanMode(id) {
		return nil
	}
	if snap := a.currentSnapshot(); snap != nil && snap.Phase == state.PhaseRunning {
		return errors.New(a.menuText(
			"Wait for the current turn to finish before approving the plan.",
			"请等待当前回合结束后再批准计划。"))
	}
	if err := a.Ctrl.SetPlanMode(id, false); err != nil {
		return err
	}
	if err := a.Ctrl.Submit(context.Background(), id, planApprovedPrompt); err != nil {
		// The continuation never started. Restore review mode so the card
		// stays available and a failed Submit cannot strand the session with
		// neither a plan review nor an execution turn.
		_ = a.Ctrl.SetPlanMode(id, true)
		return err
	}
	return nil
}

// exitPlanMode leaves review without continuing: plan mode is turned off and
// no turn is submitted. The presented plan stays in the transcript.
func (a *App) exitPlanMode() error {
	if a.Ctrl == nil {
		return nil
	}
	id := a.Ctrl.CurrentSessionID()
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return a.Ctrl.SetPlanMode(id, false)
}

// planHeaderToggle is the session header's plan-mode switch. It always renders
// (on/off) so the state is visible without opening a menu; clicking it flips
// the persisted flag through the controller.
func (a *App) planHeaderToggle(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		return
	}
	on := a.planModeOn()
	stateText := a.menuText("Off", "关")
	if on {
		stateText = a.menuText("On", "开")
	}
	label := a.menuText("Plan mode", "计划模式") + ": " + stateText
	row := ui.Row(c).Height(22).PaddingX(design.Space2).Gap(design.Space1).
		Radius(design.RadiusSM).Border(1, t.BorderL2).AlignItems(ui.Center).
		Label(label).Value(label).Role(ui.RoleSwitch).Checked(on).Focusable()
	if on {
		row = row.Background(t.BgHover).BorderColor(t.Accent)
	}
	row.Children(func() {
		glyph, color := "□", t.LabelTertiary
		if on {
			glyph, color = "☑", t.Accent
		}
		ui.Text(c, glyph).FontSize(11).FixedLineHeight(20).TextColor(color).NoWrap()
		ui.Text(c, a.menuText("Plan mode", "计划模式")).FontSize(12).
			FixedLineHeight(22).TextColor(color).NoWrap()
		ui.Text(c, stateText).FontSize(10).FixedLineHeight(22).
			TextColor(t.LabelTertiary).NoWrap()
	})
	if row.Clicked() {
		if err := a.togglePlanMode(); err != nil {
			c.Toast(err.Error())
		}
	}
}

// planReviewCard is the review surface above the composer: it appears only
// while plan mode is on and the agent presented an artifact titled "Plan". It
// shows the title, the Markdown plan, and the two review decisions. Approve
// clears plan mode and continues with one submitted turn; Exit only clears it.
func (a *App) planReviewCard(c *ui.Context, t *design.Tokens, artifact tools.Artifact) {
	markdown := strings.TrimSpace(artifact.Markdown)
	a.menuFrame(c, t, 868, func() {
		ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.menuText("Plan review", "计划审阅")).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelTertiary).Grow(1)
			ui.Text(c, artifact.Title).FontSize(13).FixedLineHeight(20).
				FontWeight(600).TextColor(t.LabelPrimary)
		})
		if markdown != "" {
			ui.Column(c).Key("plan:" + artifact.CallID).Gap(design.Space1).Children(func() {
				a.markdownBlocks(c, t, markdown)
			})
		} else if strings.TrimSpace(artifact.Path) != "" {
			ui.Text(c, artifact.Path).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelSecondary)
		} else {
			ui.Text(c, a.menuText("The presented plan has no inline content.",
				"展示的计划没有内联内容。")).FontSize(12).TextColor(t.LabelTertiary)
		}
		ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			approve := design.Apply(ui.Button(c, a.menuText("Approve", "批准")), t.PrimaryButton())
			if approve.Clicked() {
				if err := a.approvePlan(); err != nil {
					c.Toast(err.Error())
				}
			}
			exit := ui.Button(c, a.menuText("Exit plan mode", "退出计划模式")).
				Height(38).PaddingX(design.Space4).Radius(design.RadiusSM).
				Border(1, t.BorderL2).Background(t.BgBase).TextColor(t.LabelSecondary)
			if exit.Clicked() {
				if err := a.exitPlanMode(); err != nil {
					c.Toast(err.Error())
				}
			}
		})
	})
}
