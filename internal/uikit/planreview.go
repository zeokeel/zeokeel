package uikit

// The plan-review dialog entry component (AGENT-SURFACES.md S5): the native
// mygo Modal an agent's plan is reviewed in. The component owns the appearance,
// the footer order and the safety keyboard contract; the module keeps the plan
// state, the reviewer bridge and the keep-planning delivery.
//
// There is deliberately no ✕ in the header, unlike the product's form dialogs:
// a plan review is a decision gate, and a close-only control has nowhere honest to
// go — the review is re-enqueued while the plan is still "reviewing", so closing
// would either bounce the dialog back or leave the agent waiting on a decision
// nobody can reach. Closing *with* a decision would make one stray click cancel an
// irreversible plan. The card therefore states its exits: 批准, 取消计划 (the
// explicit, deliberate cancel) and 继续规划 with feedback, and Escape refuses to
// cancel (a one-shot warning says so). The approval card follows the same rule.
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
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
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

// planReviewBody is the Modal body, built as the approval card's sibling
// (B-spec-v2 §3.3.4): the same anatomy — a semantic rail, a status badge, the
// content in the middle and one decision footer — with the brand accent as its
// semantic color instead of the approval warning.
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
	feedback := ""
	if o.Feedback != nil {
		feedback = strings.TrimSpace(*o.Feedback)
	}
	// VS Code's plan review blocks Approve while feedback is typed but not
	// submitted: approving would silently drop what the user just wrote.
	pendingFeedback := feedback != ""

	// The card's accent rail runs from the panel's top edge to its bottom, so it
	// reads as the card's edge rather than a floating line inside a dialog: the
	// padding lives in the content column, not around the row.
	design.Apply(ui.Row(c).Width(design.DialogWidthL).AlignItems(ui.Stretch),
		func(el ui.Element) ui.Element {
			return el.Clip()
		}).Children(func() {
		design.Apply(ui.Box(c), func(el ui.Element) ui.Element {
			return el.Width(approvalRailWidth).Shrink(0).Background(t.Accent)
		})
		ui.Column(c).FillWidth().Grow(1).Padding(design.Space4, design.Space6).
			Gap(design.Space3).Children(func() {
			planReviewHeader(c, t, o)
			ui.Scroll(c).FillWidth().MaxHeight(planReviewMaxBodyHeight).Children(func() {
				// The plan is read with the transcript's Markdown theme, at the
				// same reading measure: a plan reviewed in a scroll box should
				// look like the reply it belongs to, not like a document.
				markdown.View(c, o.Plan.Content, planReviewMarkdown(t))
			})
			if o.Feedback != nil {
				// Enter submits the form's current content: a non-empty
				// feedback goes to Keep planning (the agent receives it);
				// approving the plan always needs the explicit button. An
				// empty feedback never approves: the key is consumed and
				// decides nothing.
				ui.TextInput(c, o.Feedback).
					Label(T(o.Locale, "继续规划反馈", "Keep-planning feedback")).
					Placeholder(T(o.Locale, "告诉 agent 计划需要怎么改…", "Tell the agent how the plan should change…")).
					FillWidth().
					OnTextKey(func(k ui.TextKey) bool {
						if k.Key != ui.KeyEnter || k.Mods != 0 {
							return false
						}
						if feedback != "" {
							keepPlanning(*o.Feedback)
						}
						return true
					})
			}
			if o.Err != "" {
				ErrorLine(c, o.Err)
			}
			planReviewFooter(c, t, o, pendingFeedback, approve, keepPlanning, cancel)
			planReviewHints(c, t, o, pendingFeedback, feedback, *escHint)
		})
	})
}

// planReviewMarkdown is the plan body's Markdown theme: the chat theme at the
// reading measure, so a plan's headings, lists and fences match the reply that
// carries it.
func planReviewMarkdown(t *design.Tokens) *markdown.Options {
	return ChatMarkdown(t, 0)
}

// planReviewHeader is the card's first line: the accent badge that names the
// gate and the plan's own step count, so the reader knows how much they are
// about to approve before scrolling.
func planReviewHeader(c *ui.Context, t *design.Tokens, o PlanReviewOptions) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		// The plan card's semantic color is the brand accent, not a status: it
		// is a decision point, not a problem (B-spec-v2 §3.3.4). The badge keeps
		// the shared badge anatomy (4 DIP chip, 11/500 label, semantic tint).
		design.Apply(ui.Badge(c, T(o.Locale, "计划待审阅", "Plan review")), t.Badge(),
			func(el ui.Element) ui.Element {
				return el.Background(t.Accent.Alpha(0.12)).TextColor(t.AccentText)
			})
		if steps := planReviewStepCount(o.Plan.Content); steps > 0 {
			design.Apply(ui.Text(c, fmt.Sprintf(T(o.Locale, "%d 步", "%d steps"), steps)),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
		}
		ui.Spacer(c).Grow(1)
		design.Apply(ui.Text(c, T(o.Locale, "批准后开始实施", "Approving starts the work")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
	})
}

// planReviewFooter is the decision bar: cancel, keep planning (needs feedback)
// and approve, with the reason for a blocked approve stated above the buttons
// rather than in a tooltip (a disabled control shows none, A4-I-04).
func planReviewFooter(c *ui.Context, t *design.Tokens, o PlanReviewOptions,
	pendingFeedback bool, approve func(), keepPlanning func(string), cancel func()) {
	if pendingFeedback {
		// The disabled action explains itself in place: the reason is a sentence
		// the user must act on, not a hover hint.
		design.Apply(ui.Text(c, T(o.Locale,
			"有未提交的反馈：「批准」已停用，按 Enter 或「继续规划」先把反馈发给 agent，或清空反馈。",
			"There is unsubmitted feedback: Approve is disabled — press Enter or Keep planning to send it, or clear the box.")),
			t.Font(design.TypeXXS)).TextColor(t.WarningText).
			Role(ui.RoleAlert).MaxLines(2)
	}
	actions := []DialogAction{
		{Label: T(o.Locale, "取消计划", "Cancel plan"), OnClick: cancel},
	}
	if o.Feedback != nil {
		actions = append(actions, DialogAction{
			Label:    T(o.Locale, "继续规划", "Keep planning"),
			Disabled: !pendingFeedback,
			OnClick:  func() { keepPlanning(*o.Feedback) },
		})
	}
	actions = append(actions, DialogAction{
		Label: T(o.Locale, "批准", "Approve"), Primary: true,
		Disabled: pendingFeedback, OnClick: approve,
	})
	DialogFooter(c, actions)
}

// planReviewHints is the card's one hint line: the key caps for the chords that
// work here, and the Escape warning the plan review owns (Escape never cancels a
// plan; a cancel is an irreversible explicit decision).
func planReviewHints(c *ui.Context, t *design.Tokens, o PlanReviewOptions,
	pendingFeedback bool, feedback string, escHint bool) {
	if o.Err != "" {
		// The last action failed. Escape now closes the review, so say where the
		// exit is after the error.
		approvalKbdHint(c, t, T(o.Locale, "关闭审阅（计划未批准）", "Close the review (not approved)"),
			FormatShortcut("Esc"))
		return
	}
	if escHint {
		// C-16: Escape must not look dead. A visible one-shot warning says why
		// nothing happened and what decides the review.
		design.Apply(ui.Text(c, T(o.Locale,
			"Esc 不能关闭计划审阅：请点击「批准」或「取消计划」，或输入反馈继续规划。",
			"Esc does not close the plan review: click Approve or Cancel plan, or write feedback to keep planning.")),
			t.Font(design.TypeXXS)).TextColor(t.WarningText).Role(ui.RoleAlert)
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
		approvalKbdHint(c, t, T(o.Locale, "不取消计划", "does not cancel"), FormatShortcut("Esc"))
		if o.Feedback != nil {
			if pendingFeedback {
				approvalKbdHint(c, t, T(o.Locale, "发送反馈继续规划", "send feedback"),
					FormatShortcut("Enter"))
				return
			}
			approvalKbdHint(c, t, T(o.Locale, "无反馈时不生效", "needs feedback"), FormatShortcut("Enter"))
		}
	})
}

// planReviewStepCount counts the plan's numbered or bulleted steps, so the
// header can say how much the plan covers. It reads the Markdown's own list
// lines and never parses prose.
func planReviewStepCount(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") ||
			strings.HasPrefix(trimmed, "+ ") {
			count++
			continue
		}
		// "1. ", "2) " and the like.
		index := 0
		for index < len(trimmed) && trimmed[index] >= '0' && trimmed[index] <= '9' {
			index++
		}
		if index > 0 && index < len(trimmed) && (trimmed[index] == '.' || trimmed[index] == ')') {
			count++
		}
	}
	return count
}
