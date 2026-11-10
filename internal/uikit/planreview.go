package uikit

// The plan-review dialog entry component (AGENT-SURFACES.md S5): the native
// mygo Modal an agent's plan is reviewed in. The component owns the appearance,
// the footer order and the safety keyboard contract; the module keeps the plan
// state, the reviewer bridge and the keep-planning delivery.
//
// Safety contract (locked here, never re-composed by a caller):
//   - Approve is the accent Primary action rightmost, Cancel is an ordinary
//     secondary action leftmost; neither is the Default and nothing is
//     auto-focused, so a stray Enter never approves;
//   - Enter in the feedback input means Keep planning and does nothing while
//     the feedback is empty; an empty feedback never approves, and the Keep
//     planning button is disabled without feedback;
//   - Escape does not cancel a plan: the first Escape reopens the dialog with a
//     visible one-shot WarningText RoleAlert; only a failed decision (Err set)
//     lets Escape close the review, so a failure can never trap the window.
//
// The dialog draws only native controls (Modal, Column, Text, Scroll,
// markdown.View, TextInput, Divider, DialogFooter) styled with internal/design.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// planReviewMaxBodyHeight bounds the scrollable plan Markdown so the footer and
// the title stay on screen.
const planReviewMaxBodyHeight float32 = design.Space6 * 12

// PlanReviewOptions is one plan under review, rendered as the product's plan
// review Modal.
type PlanReviewOptions struct {
	// Plan is the review state; Content is the plan Markdown under review.
	Plan contracts.PlanReview
	// Feedback is the keep-planning feedback buffer; nil hides the feedback
	// input and the Keep planning action (a host without a chat bridge cannot
	// deliver feedback), leaving Approve and Cancel.
	Feedback *string
	// Err is the last failed decision's message, shown as the inline error line
	// (and it makes Escape close the review instead of warning).
	Err string
	// OnApprove is the explicit approve action.
	OnApprove func()
	// OnKeep receives the current, non-empty feedback when the user keeps
	// planning (the Keep planning action or Enter in the feedback input).
	OnKeep func(feedback string)
	// OnCancel is the explicit cancel-plan action.
	OnCancel func()
	// OnDismiss closes the review after a failed decision: Escape, the only exit
	// from a review whose decision failed (never called while Err is empty).
	OnDismiss func()
	// Locale localizes the component's copy; nil falls back to the product's
	// Chinese copy.
	Locale contracts.LocaleService
}

// PlanReviewDialog renders the plan under review in a native Modal. The
// component owns the dialog's visual state: the open flag (Escape reopens it
// with the warning) and the one-shot Escape warning, keyed by the plan content
// so a new plan starts clean. It is a frame builder: it only builds controls,
// except for the actions and the dismissal handler.
func PlanReviewDialog(c *ui.Context, o PlanReviewOptions) {
	if strings.TrimSpace(o.Plan.Content) == "" {
		return
	}
	wrap := ui.Column(c.Key("uikit.planreview/" + o.Plan.Content))
	open := ui.Local(wrap, "open", func() bool { return true })
	escHint := ui.Local(wrap, "escHint", func() bool { return false })
	wrap.Children(func() {
		el := ui.Modal(c, open, func() {
			planReviewBody(c, o, escHint)
		})
		if !el.Dismissed() {
			return
		}
		if o.Err != "" {
			// The last decision failed: Escape is the documented exit so the
			// dialog can never trap the window. The caller marks the review
			// cancelled locally; no decision is retried silently.
			if o.OnDismiss != nil {
				o.OnDismiss()
			} else {
				*open = true
				c.Invalidate()
			}
			return
		}
		// Escape must not cancel a plan: a cancel is an irreversible explicit
		// decision. Reopen the dialog with a visible one-shot warning instead
		// of letting the key look dead (C-16).
		*open = true
		*escHint = true
		c.Invalidate()
	})
}

// planReviewBody is the Modal body: the plan Markdown, the keep-planning
// feedback and the decision footer.
func planReviewBody(c *ui.Context, o PlanReviewOptions, escHint *bool) {
	t := design.Of(c)
	approve := func() {
		*escHint = false
		if o.OnApprove != nil {
			o.OnApprove()
		}
	}
	cancel := func() {
		*escHint = false
		if o.OnCancel != nil {
			o.OnCancel()
		}
	}
	keepPlanning := func(feedback string) {
		*escHint = false
		if o.OnKeep != nil {
			o.OnKeep(feedback)
		}
	}

	design.Apply(ui.Column(c).Width(design.DialogWidthL).Gap(design.Space4), func(el ui.Element) ui.Element {
		return el.PaddingX(design.Space6).PaddingY(design.Space2)
	}).Children(func() {
		DialogHeader(c, DialogHeaderOptions{
			Key:   "uikit.planreview/header",
			Icon:  icons.Activity(),
			Title: T(o.Locale, "计划待审阅", "Plan review"),
		})
		ui.Scroll(c).FillWidth().MaxHeight(planReviewMaxBodyHeight).Children(func() {
			markdown.View(c, o.Plan.Content, nil)
		})
		if o.Feedback != nil {
			// Enter submits the form's current content: a non-empty feedback
			// goes to Keep planning (the agent receives it); approving the plan
			// always needs the explicit button. An empty feedback never
			// approves: the key is consumed and decides nothing.
			ui.TextInput(c, o.Feedback).
				Label(T(o.Locale, "继续规划反馈", "Keep-planning feedback")).
				Placeholder(T(o.Locale, "告诉 agent 计划需要怎么改…", "Tell the agent how the plan should change…")).
				FillWidth().
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEnter || k.Mods != 0 {
						return false
					}
					if feedback := strings.TrimSpace(*o.Feedback); feedback != "" {
						keepPlanning(*o.Feedback)
					}
					return true
				})
		}
		if o.Err != "" {
			ErrorLine(c, o.Err)
		}
		ui.Divider(c)
		actions := []DialogAction{
			{Label: T(o.Locale, "取消计划", "Cancel plan"), OnClick: cancel},
		}
		if o.Feedback != nil {
			actions = append(actions, DialogAction{
				Label:    T(o.Locale, "继续规划", "Keep planning"),
				Disabled: strings.TrimSpace(*o.Feedback) == "",
				OnClick:  func() { keepPlanning(*o.Feedback) },
			})
		}
		actions = append(actions, DialogAction{
			Label: T(o.Locale, "批准", "Approve"), Primary: true, OnClick: approve,
		})
		DialogFooter(c, actions)
		if o.Err != "" {
			// The last action failed. Escape now closes the review, so say where
			// the exit is after the error.
			design.Apply(ui.Text(c, T(o.Locale,
				"Esc = 关闭审阅（计划未批准）",
				"Esc = close the review (the plan was not approved)")),
				t.TextMuted(design.TypeXXS))
			return
		}
		if *escHint {
			// C-16: Escape must not look dead. A visible one-shot warning says
			// why nothing happened and what decides the review.
			design.Apply(ui.Text(c, T(o.Locale,
				"Esc 不能关闭计划审阅：请点击「批准」或「取消计划」，或输入反馈继续规划。",
				"Esc does not close the plan review: click Approve or Cancel plan, or write feedback to keep planning.")),
				t.Font(design.TypeXXS)).TextColor(t.WarningText).Role(ui.RoleAlert)
			return
		}
		hint := T(o.Locale, "点击「批准」批准计划 · Esc 不会取消计划",
			"Click Approve to accept · Esc does not cancel the plan")
		if o.Feedback != nil {
			hint = T(o.Locale, "输入反馈后按 Enter = 继续规划 · 点击「批准」批准计划 · Esc 不会取消计划",
				"Enter with feedback = keep planning · Click Approve to accept · Esc does not cancel the plan")
			if strings.TrimSpace(*o.Feedback) != "" {
				hint = T(o.Locale, "Enter = 继续规划（发送反馈给 agent）· 点击「批准」批准计划",
					"Enter = keep planning with your feedback · Click Approve to accept")
			}
		}
		design.Apply(ui.Text(c, hint), t.TextMuted(design.TypeXXS))
	})
}
