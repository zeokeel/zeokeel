package shell

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/icons"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// planArtifactTitle is the title older agents used for the reviewable plan
// through the present tool. exit_plan_mode is the primary source now; the shell
// keeps matching this artifact title case-insensitively so sessions created
// before the tool still show their review card.
const planArtifactTitle = "Plan"

// planApprovedPrompt is the single continuation submitted when the user
// approves a plan. Approve clears plan mode first, then sends this prompt to
// the same session, so the next turn executes under the normal (mutating)
// tool policy.
const planApprovedPrompt = "Plan approved. Execute the plan."

// planKeepPlanningPrompt is the continuation submitted when the user chooses
// Keep planning without typing feedback: the model revises the plan and
// presents it again through exit_plan_mode.
const planKeepPlanningPrompt = "Keep planning. Revise the plan and present it again."

// planUIState is the per-App plan review interaction state. Decisions and
// dismissals are keyed by the plan delivery's call id (the exit_plan_mode call
// or the present artifact), so a revised plan opens a fresh review while the
// user's answer to an older one is never re-submitted.
type planUIState struct {
	decided   map[string]bool
	dismissed map[string]bool
	// feedback holds each plan delivery's Keep planning draft, keyed by call
	// id so switching sessions or deliveries cannot leak one plan's feedback
	// into another.
	feedback map[string]*string
}

var planUIStates sync.Map // map[*App]*planUIState

// planUI returns the plan review state of a, making it on first use.
func (a *App) planUI() *planUIState {
	if s, ok := planUIStates.Load(a); ok {
		return s.(*planUIState)
	}
	s := &planUIState{}
	actual, _ := planUIStates.LoadOrStore(a, s)
	return actual.(*planUIState)
}

// feedbackDraft returns the stable Keep planning draft pointer for one plan
// delivery, creating it on first use.
func (st *planUIState) feedbackDraft(callID string) *string {
	if st.feedback == nil {
		st.feedback = make(map[string]*string)
	}
	if st.feedback[callID] == nil {
		draft := ""
		st.feedback[callID] = &draft
	}
	return st.feedback[callID]
}

// planModeOn reports whether the current session is in plan mode. A static app
// (Ctrl == nil) is never in plan mode, matching the prompt policy wiring.
func (a *App) planModeOn() bool {
	if a.Ctrl == nil {
		return false
	}
	return a.Ctrl.PlanMode(a.Ctrl.CurrentSessionID())
}

// planReviewPending reports whether a delivered plan is waiting for the user's
// decision. The composer badge uses it for its pending treatment: a plan was
// submitted (through exit_plan_mode or the legacy present artifact) and the
// user has not approved, exited, kept planning, or dismissed the review.
func (a *App) planReviewPending() bool {
	review := a.latestPlanReview()
	if review == nil {
		return false
	}
	return !a.planUI().decided[review.CallID]
}

// planReviewArtifact returns the plan delivery the review card should show, or
// nil when plan mode is off, nothing was delivered, or the user already decided
// on or dismissed that delivery.
func (a *App) planReviewArtifact() *tools.Artifact {
	review := a.latestPlanReview()
	if review == nil {
		return nil
	}
	st := a.planUI()
	if st.decided[review.CallID] || st.dismissed[review.CallID] {
		return nil
	}
	return review
}

// latestPlanReview returns the newest plan delivered for review, or nil when
// plan mode is off. exit_plan_mode is the primary source; once a session has
// used it, older present-based plan artifacts are ignored so a decided plan
// cannot come back. present artifacts titled "Plan" remain supported for
// sessions created before the tool existed.
func (a *App) latestPlanReview() *tools.Artifact {
	if !a.planModeOn() {
		return nil
	}
	if review := a.exitPlanModeReview(); review != nil {
		return review
	}
	return a.presentedPlanReview()
}

// exitPlanModeReview scans the live snapshot for the newest successful
// exit_plan_mode call and renders its recorded plan as a review artifact. The
// tool result (or, defensively, its plan argument) carries the Markdown, so
// the controller/session layer does not need a plan-specific branch.
func (a *App) exitPlanModeReview() *tools.Artifact {
	snap := a.currentSnapshot()
	if snap == nil || len(snap.Tools) == 0 {
		return nil
	}
	var newest *state.ToolState
	for _, toolState := range snap.Tools {
		if toolState.Name != tools.ExitPlanModeToolName || toolState.Status != state.ToolDone || toolState.IsError {
			continue
		}
		candidate := toolState
		if newest == nil || candidate.UpdatedSeq > newest.UpdatedSeq {
			newest = &candidate
		}
	}
	if newest == nil {
		return nil
	}
	return planReviewFromToolState(*newest)
}

// planReviewFromToolState builds the review artifact for one exit_plan_mode
// call. The JSON result is authoritative; the plan argument is the fallback for
// a result the shell cannot parse (for example a truncated fixture).
func planReviewFromToolState(toolState state.ToolState) *tools.Artifact {
	artifact := tools.Artifact{CallID: toolState.CallID, Kind: "markdown"}
	if trimmed := strings.TrimSpace(toolState.Result); trimmed != "" {
		var result tools.ExitPlanModeResult
		if err := json.Unmarshal([]byte(trimmed), &result); err == nil && result.Kind == tools.ExitPlanModeResultKind {
			artifact.Title = strings.TrimSpace(result.Title)
			artifact.Markdown = result.Markdown
		}
	}
	if strings.TrimSpace(artifact.Markdown) == "" && len(toolState.Args) > 0 {
		var args struct {
			Plan string `json:"plan"`
		}
		if err := json.Unmarshal(toolState.Args, &args); err == nil {
			artifact.Markdown = args.Plan
		}
	}
	if strings.TrimSpace(artifact.Markdown) == "" {
		return nil
	}
	if artifact.Title == "" {
		artifact.Title = planArtifactTitle
	}
	return &artifact
}

// presentedPlanReview returns the newest present artifact titled "Plan", the
// pre-exit_plan_mode delivery path. Only the current session's artifacts are
// considered, like the transcript's artifact list.
func (a *App) presentedPlanReview() *tools.Artifact {
	artifacts := a.artifacts()
	for i := len(artifacts) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(artifacts[i].Title), planArtifactTitle) {
			artifact := artifacts[i]
			return &artifact
		}
	}
	return nil
}

// decidePlanReview remembers the user's decision for one plan delivery and
// clears its transient UI state.
func (a *App) decidePlanReview(callID string) {
	st := a.planUI()
	if callID == "" {
		return
	}
	if st.decided == nil {
		st.decided = make(map[string]bool)
	}
	st.decided[callID] = true
	delete(st.dismissed, callID)
	delete(st.feedback, callID)
}

// dismissPlanReview closes the review card while keeping plan mode on. The
// pending badge stays until a real decision, and clicking it reopens the card.
func (a *App) dismissPlanReview(callID string) {
	st := a.planUI()
	if callID != "" {
		st.dismissed = map[string]bool{callID: true}
		delete(st.feedback, callID)
	}
}

// reopenPlanReview shows the review card again after a dismissal.
func (a *App) reopenPlanReview(callID string) {
	if callID == "" {
		return
	}
	delete(a.planUI().dismissed, callID)
}

// approvePlan is the Plan review card's primary action: turn plan mode off and
// submit exactly one continuation to the same session. The plan-mode check
// before submitting makes a second click (or a stale render of the card) a
// no-op instead of a duplicate turn.
func (a *App) approvePlan(artifact tools.Artifact) error {
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
	a.decidePlanReview(artifact.CallID)
	return nil
}

// keepPlanning is the review card's Keep planning action: plan mode stays on
// and the feedback (or the default revision prompt) is submitted as the next
// user message, so the model revises the plan and presents it again.
func (a *App) keepPlanning(artifact tools.Artifact) error {
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
			"Wait for the current turn to finish before answering the plan.",
			"请等待当前回合结束后再回复计划。"))
	}
	draft := a.planUI().feedbackDraft(artifact.CallID)
	feedback := strings.TrimSpace(*draft)
	text := feedback
	if text == "" {
		text = planKeepPlanningPrompt
	}
	// Mark the decision before submitting so a stale card cannot send twice;
	// a failed Submit restores the review and the typed feedback.
	a.decidePlanReview(artifact.CallID)
	if err := a.Ctrl.Submit(context.Background(), id, text); err != nil {
		st := a.planUI()
		delete(st.decided, artifact.CallID)
		*draft = feedback
		st.feedback[artifact.CallID] = draft
		return err
	}
	return nil
}

// currentPlanDeliveryID returns the call id of the plan delivery currently
// under review, or "" when none. Command paths that leave plan mode directly
// use it so a decision is recorded like the review card's buttons.
func (a *App) currentPlanDeliveryID() string {
	if review := a.latestPlanReview(); review != nil {
		return review.CallID
	}
	return ""
}

// exitPlanMode leaves review without continuing: plan mode is turned off and
// no turn is submitted. The presented plan stays in the transcript.
func (a *App) exitPlanMode(artifact tools.Artifact) error {
	if a.Ctrl == nil {
		return nil
	}
	id := a.Ctrl.CurrentSessionID()
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if err := a.Ctrl.SetPlanMode(id, false); err != nil {
		return err
	}
	a.decidePlanReview(artifact.CallID)
	return nil
}

// composerPlanBadge is the composer's plan-mode status chip (ADR 0008): DSH's
// accent "Plan" badge to the right of the access-mode control. It renders only
// while plan mode is on. While a submitted plan waits for the user's decision
// it switches to the pending treatment (warning tint, clock glyph, "pending"
// text and accessible label): clicking it reopens the review card instead of
// leaving the mode. Once decided it is the normal chip whose click leaves plan
// mode exactly like "/plan off". Plan mode is soft guidance, not a permission,
// so the chip never changes the access mode.
func (a *App) composerPlanBadge(c *ui.Context, t *design.Tokens) {
	if !a.planModeOn() {
		return
	}
	th := t.Theme()
	pending := a.planReviewPending()
	label, textLabel, glyph := "Plan mode on; click to exit", "Plan", icons.Sparkles()
	tint, textColor := t.Accent, t.Accent
	fill := t.Accent.Alpha(0.10)
	hoverFill := t.Accent.Alpha(0.18)
	if pending {
		label = a.menuText("Plan review pending; click to open", "计划待审阅；点击查看")
		textLabel = a.menuText("Pending", "待审阅")
		glyph = icons.Clock()
		tint, textColor = t.Warning, t.WarningText
		fill = t.Warning.Alpha(0.14)
		hoverFill = t.Warning.Alpha(0.22)
	} else {
		label = a.menuText("Plan mode on; click to exit", "计划模式已开启；点击退出")
		textLabel = a.menuText("Plan", "计划")
	}
	chip := ui.Row(c).Height(t.Scale(th.Space(7))).PaddingX(t.Scale(th.Space(2))).
		Gap(t.Scale(th.Space(1))).Radius(design.RadiusSM).AlignItems(ui.Center).
		Background(fill.Over(t.BgRaised)).
		Label(label).Value(label).Role(ui.RoleButton).Focusable()
	if chip.Hovered() {
		chip = chip.Background(hoverFill.Over(t.BgRaised))
	}
	chip.Children(func() {
		ui.Icon(c, glyph).Size(th.Space(4), th.Space(4)).TextColor(tint)
		// The narrow composer keeps the icon only, the same rule the access
		// chip follows: a text label would push the model chip or Send out of
		// the toolbar instead of being clipped.
		if a.composerPopupWidthFor(c) >= narrowComposerLabelW {
			ui.Text(c, textLabel).FontSize(12).
				FixedLineHeight(20).FontWeight(500).TextColor(textColor).NoWrap()
		}
	})
	if chip.Clicked() {
		if pending {
			if review := a.latestPlanReview(); review != nil {
				a.reopenPlanReview(review.CallID)
			}
			return
		}
		if err := a.exitPlanMode(tools.Artifact{}); err != nil {
			c.Toast(err.Error())
			return
		}
		c.Toast(a.menuText("Plan mode off", "已退出计划模式"))
	}
}

// planReviewCard is the review surface above the composer: it appears only
// while plan mode is on and a plan delivery waits for a decision (a successful
// exit_plan_mode call, or a legacy present artifact titled "Plan"). It shows
// the title, the Markdown plan, a Keep planning feedback input, and the three
// review decisions. Approve clears plan mode and continues with one submitted
// turn; Keep planning keeps the mode on and submits the feedback as the next
// user message; Exit only clears the mode. Dismiss (or the global Esc
// shortcut, which treats the card as the topmost overlay) closes the card while
// keeping plan mode on, and the pending composer badge reopens it.
func (a *App) planReviewCard(c *ui.Context, t *design.Tokens, artifact tools.Artifact) {
	markdown := strings.TrimSpace(artifact.Markdown)
	th := t.Theme()
	st := a.planUI()
	a.menuFrame(c, t, a.sessionComposerWidthFor(c), func() {
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
		feedbackLabel := a.menuText("Feedback for the agent (optional)", "给代理的反馈（可选）")
		ui.TextInput(c, st.feedbackDraft(artifact.CallID)).Label(feedbackLabel).
			Placeholder(feedbackLabel).Height(th.Space(9)).PaddingX(th.Space(2)).
			Border(1, t.BorderL2).Radius(design.RadiusSM).Background(t.BgBase)
		ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
			approve := design.Apply(ui.Button(c, a.menuText("Approve", "批准")), t.PrimaryButton())
			if approve.Clicked() {
				if err := a.approvePlan(artifact); err != nil {
					c.Toast(err.Error())
				}
			}
			keepLabel := a.menuText("Keep planning", "继续规划")
			keep := ui.ButtonBase(c).Height(th.Space(9)).PaddingX(th.Space(4)).
				Radius(design.RadiusSM).Border(1, t.BorderL2).
				AlignItems(ui.Center).Justify(ui.Start).Label(keepLabel)
			if keep.Hovered() {
				keep = keep.Background(t.BgHover)
			}
			keep.Children(func() {
				ui.Text(c, keepLabel).FontSize(13).FixedLineHeight(20).
					TextColor(t.LabelPrimary)
			})
			if keep.Clicked() {
				if err := a.keepPlanning(artifact); err != nil {
					c.Toast(err.Error())
				}
			}
			exitLabel := a.menuText("Exit plan mode", "退出计划模式")
			exit := ui.ButtonBase(c).Height(th.Space(9)).PaddingX(th.Space(4)).
				Radius(design.RadiusSM).Border(1, t.BorderL2).
				AlignItems(ui.Center).Justify(ui.Start).Label(exitLabel)
			if exit.Hovered() {
				exit = exit.Background(t.BgHover)
			}
			exit.Children(func() {
				ui.Text(c, exitLabel).FontSize(13).FixedLineHeight(20).
					TextColor(t.LabelSecondary)
			})
			if exit.Clicked() {
				if err := a.exitPlanMode(artifact); err != nil {
					c.Toast(err.Error())
				}
			}
			ui.Spacer(c).Grow(1)
			dismissLabel := a.menuText("Dismiss", "关闭")
			dismiss := ui.ButtonBase(c).Height(th.Space(9)).PaddingX(th.Space(3)).
				Radius(design.RadiusSM).AlignItems(ui.Center).Label(dismissLabel)
			if dismiss.Hovered() {
				dismiss = dismiss.Background(t.BgHover)
			}
			dismiss.Children(func() {
				ui.Text(c, dismissLabel).FontSize(12).FixedLineHeight(20).
					TextColor(t.LabelTertiary)
			})
			if dismiss.Clicked() {
				a.dismissPlanReview(artifact.CallID)
			}
		})
	})
}
