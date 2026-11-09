// Package interaction contributes the agent-to-user overlays: the ask_user
// dialog, the present artifact preview and the plan review dialog.
//
// It is a kernel UI module: activation requires the host UI runtime, installs
// the handlers of the optional host bridges (UserInteraction, ArtifactPresenter
// and PlanReviewer — each handler call hops to the UI thread with
// UIRuntime.Post), and registers one overlay.dialogs slot item. The ask and plan
// dialogs render through the uikit event components (uikit.AskDialog,
// uikit.PlanReviewDialog); only the artifact preview and queue state stay here.
//
// The three overlay kinds share one FIFO queue rendered one at a time: the
// oldest arrived question/artifact/plan-review is shown, and answering,
// closing or approving it pops the entry so the next overlay opens on the
// following frame. Plan review is driven by its bridge's state: reviewing
// enqueues the plan, approved/cancelled removes it. Disposal clears every
// handler, cancels pending questions and exits a still-reviewing plan so an
// agent tool call can never hang on a dialog that no longer exists.
package interaction

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/interaction"

// SlotID identifies this module's contribution to overlay.dialogs.
const SlotID = "interaction.dialog"

// maxOverlayBodyHeight bounds the Markdown preview inside the artifact modal.
const maxOverlayBodyHeight = design.Space6 * 12

// Config is the module's typed assembly config (all copy is localized; the
// module has no configurable fields).
type Config struct{}

// overlayKind distinguishes the unified FIFO entries.
type overlayKind int

const (
	kindQuestion overlayKind = iota
	kindArtifact
	kindPlan
)

// overlayItem is one queued overlay. Questions and artifacts carry their
// payload; plan review reads the live plan state instead.
type overlayItem struct {
	kind     overlayKind
	question contracts.UserQuestion
	artifact contracts.Artifact
}

// state is the module instance's FIFO overlay queue and dialog state. It is
// touched only on the UI thread.
type state struct {
	interaction contracts.UserInteraction
	presenter   contracts.ArtifactPresenter
	reviewer    contracts.PlanReviewer
	chat        contracts.ChatService
	notifier    contracts.Notifier

	overlays   []overlayItem
	currentKey string
	// open is the artifact preview's Modal open flag; the ask and plan dialogs
	// are uikit entry components and own their own dialog state.
	open bool
	// draft is the ask dialog's answer buffer: the free-text answer or the
	// inline "Other…" text (one question is shown at a time).
	draft string

	plan         *contracts.PlanReview
	planErr      string
	planFeedback string
	// planFeedbackSent records feedback the agent accepted before a later
	// reviewer.Exit failed: a retry must close the review without sending the
	// same feedback twice.
	planFeedbackSent bool
	// askOtherOpen is the ask_user options question's inline "Other…" answer
	// state: the choice expands a text input whose text becomes the answer
	// (Esc collapses back to the options).
	askOtherOpen bool

	// locale localizes the dialog copy per frame (zh is the default).
	locale contracts.LocaleService
}

// tr returns the localized copy for the current frame's language.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

// Module registers the ask_user / artifact / plan-review overlays.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		// Every bridge is optional: a minimal host may provide only some of
		// them (or none, in which case the module is a no-op).
		interaction, _ := ctx.Get(contracts.KeyUserInteraction)
		presenter, _ := ctx.Get(contracts.KeyArtifactPresenter)
		reviewer, _ := ctx.Get(contracts.KeyPlanReviewer)
		chat, _ := ctx.Get(contracts.KeyChat)
		notifier, _ := ctx.Get(contracts.KeyNotifier)
		if interaction == nil && presenter == nil && reviewer == nil {
			return nil
		}
		locale, _ := ctx.Get(contracts.KeyLocale)
		st := &state{
			interaction: interaction, presenter: presenter, reviewer: reviewer,
			chat: chat, notifier: notifier, locale: locale,
		}

		if interaction != nil {
			// The host invokes the handler from whichever goroutine asks the
			// question: hop to the UI thread and request a frame.
			interaction.SetHandler(func(question contracts.UserQuestion) {
				runtime.Post(func() {
					st.overlays = append(st.overlays, overlayItem{kind: kindQuestion, question: question})
					runtime.Invalidate()
				})
			})
		}
		if presenter != nil {
			presenter.SetArtifactHandler(func(artifact contracts.Artifact) {
				runtime.Post(func() {
					st.overlays = append(st.overlays, overlayItem{kind: kindArtifact, artifact: artifact})
					runtime.Invalidate()
				})
			})
		}
		if reviewer != nil {
			reviewer.SetPlanHandler(func(review contracts.PlanReview) {
				runtime.Post(func() {
					st.plan = &review
					if review.State == "reviewing" {
						st.planFeedback = ""
						st.planErr = ""
						st.planFeedbackSent = false
					}
					runtime.Invalidate()
				})
			})
		}

		cleanup := func() {
			if interaction != nil {
				interaction.SetHandler(nil)
				for _, item := range st.overlays {
					if item.kind == kindQuestion {
						interaction.Cancel(item.question.ID)
					}
				}
			}
			if presenter != nil {
				presenter.SetArtifactHandler(nil)
			}
			if reviewer != nil {
				reviewer.SetPlanHandler(nil)
				if st.plan != nil && st.plan.State == "reviewing" {
					_, _ = reviewer.Exit("cancel")
				}
			}
			st.overlays = nil
			st.plan = nil
			st.planFeedback = ""
			st.planErr = ""
			st.planFeedbackSent = false
			st.askOtherOpen = false
			st.currentKey = ""
			st.open = false
		}
		if err := own(ctx, func() (func(), error) { return cleanup, nil }); err != nil {
			cleanup()
			return err
		}

		item := contracts.SlotItem{
			Slot:  contracts.SlotOverlayDialogs,
			ID:    SlotID,
			Order: 20,
			Build: func(c *ui.Context) {
				buildDialog(c, runtime, st)
			},
		}
		return own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(item)
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		WithName("ZeoKeel interaction overlays")
}

// overlayKey is the identity of one queued overlay (the plan kind has a single
// instance: its state is the plan review itself).
func overlayKey(item overlayItem) string {
	switch item.kind {
	case kindQuestion:
		return "q:" + item.question.ID
	case kindArtifact:
		return "a:" + item.artifact.ID
	default:
		return "plan"
	}
}

// syncPlanOverlays mirrors the live plan-review state into the unified queue:
// reviewing enqueues the plan, approved/cancelled removes it.
func (st *state) syncPlanOverlays() {
	if st.reviewer == nil || st.plan == nil {
		return
	}
	switch st.plan.State {
	case "reviewing":
		if strings.TrimSpace(st.plan.Content) == "" || st.hasPlanOverlay() {
			return
		}
		st.overlays = append(st.overlays, overlayItem{kind: kindPlan})
	case "approved", "cancelled":
		st.removePlanOverlay()
	}
}

func (st *state) hasPlanOverlay() bool {
	for _, item := range st.overlays {
		if item.kind == kindPlan {
			return true
		}
	}
	return false
}

func (st *state) removePlanOverlay() {
	kept := st.overlays[:0]
	for _, item := range st.overlays {
		if item.kind != kindPlan {
			kept = append(kept, item)
		}
	}
	st.overlays = kept
}

// buildDialog renders the oldest queued overlay, or nothing while the queue is
// empty. The ask and plan dialogs are uikit entry components that own their
// native Modal (and with it the Escape contract); the artifact preview is a
// plain Modal whose dismissal closes it.
func buildDialog(c *ui.Context, runtime contracts.UIRuntime, st *state) {
	st.syncPlanOverlays()
	if len(st.overlays) == 0 {
		st.open = false
		st.currentKey = ""
		st.draft = ""
		st.planErr = ""
		return
	}
	item := st.overlays[0]
	key := overlayKey(item)
	if st.currentKey != key {
		st.currentKey = key
		st.open = true
		st.draft = ""
		st.planErr = ""
		st.planFeedback = ""
		st.planFeedbackSent = false
		st.askOtherOpen = false
	}
	switch item.kind {
	case kindArtifact:
		ui.Modal(c, &st.open, func() {
			artifactPanel(c, runtime, st, item.artifact)
		})
		if !st.open && st.currentKey == key {
			// Escape or a backdrop click closed the artifact preview.
			popOverlay(runtime, st)
		}
	case kindPlan:
		buildPlanDialog(c, runtime, st)
	default:
		buildAskDialog(c, runtime, st, item.question)
	}
}

// buildPlanDialog renders the plan review through uikit.PlanReviewDialog: the
// component owns the Modal, the Esc-does-not-cancel contract, the one-shot
// warning and the footer safety order; the module owns the plan state, the
// reviewer bridge and the keep-planning feedback delivery.
func buildPlanDialog(c *ui.Context, runtime contracts.UIRuntime, st *state) {
	if st.reviewer == nil || st.plan == nil {
		return
	}
	// The feedback loop needs the chat bridge that delivers it: without one the
	// dialog shows Approve and Cancel only.
	var feedback *string
	if st.chat != nil {
		feedback = &st.planFeedback
	}
	uikit.PlanReviewDialog(c, uikit.PlanReviewOptions{
		Plan:     *st.plan,
		Feedback: feedback,
		Err:      st.planErr,
		Locale:   st.locale,
		OnApprove: func() {
			exitPlan(runtime, st, "approve", st.tr("批准失败：", "Approve failed: "))
		},
		OnKeep: func(feedback string) {
			keepPlanning(runtime, st, feedback)
		},
		OnCancel: func() {
			exitPlan(runtime, st, "cancel", st.tr("取消失败：", "Cancel failed: "))
		},
		OnDismiss: func() {
			// A failed decision must never trap the dialog: Escape closes the
			// review and the local snapshot is marked cancelled so the overlay
			// cannot be re-enqueued.
			closePlanAfterFailure(runtime, st)
		},
	})
}

// buildAskDialog renders an ask_user question through uikit.AskDialog: the
// component owns the equal-weight options, the "Other…" input and the
// no-stray-Enter safety semantics; the module answers through the bridge.
func buildAskDialog(c *ui.Context, runtime contracts.UIRuntime, st *state, question contracts.UserQuestion) {
	uikit.AskDialog(c, uikit.AskDialogOptions{
		Question:  question,
		OtherOpen: &st.askOtherOpen,
		Answer:    &st.draft,
		Locale:    st.locale,
		OnAnswer: func(answer string) {
			answerQuestion(runtime, st, question.ID, answer)
		},
		OnCancel: func() {
			cancelQuestion(runtime, st, question.ID)
		},
	})
}

// artifactPanel is the present-tool preview: the artifact title over its
// Markdown body, with the close action in the same footer position every other
// overlay uses.
func artifactPanel(c *ui.Context, runtime contracts.UIRuntime, st *state, artifact contracts.Artifact) {
	t := design.Of(c)
	title := firstNonEmpty(artifact.Title, st.tr("工件预览", "Artifact preview"))
	close := func() { popOverlay(runtime, st) }
	dialogEnter(c, close)
	design.Apply(ui.Column(c).Width(design.DialogWidthL).Gap(design.Space4), func(el ui.Element) ui.Element {
		return el.PaddingX(design.Space6).PaddingY(design.Space2)
	}).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.Folder()).Size(design.Space5, design.Space5).TextColor(t.Accent)
			// The artifact preview is not one of the S4/S5/S6 entry components;
			// keep its existing heading weight (the old baseline entry covers it).
			design.Apply(ui.Text(c, title), t.Font(design.TypeL)).FontWeight(600).
				TextColor(t.LabelPrimary)
		})
		body := strings.TrimSpace(artifact.Body)
		if body == "" {
			design.Apply(ui.Text(c, st.tr("（无内容）", "(No content)")), t.TextMuted(design.TypeXS))
		} else {
			ui.Scroll(c).FillWidth().MaxHeight(maxOverlayBodyHeight).Children(func() {
				markdown.View(c, artifact.Body, nil)
			})
		}
		ui.Divider(c)
		uikit.DialogFooter(c, []uikit.DialogAction{
			{Label: st.tr("关闭", "Close"), Primary: true, OnClick: close},
		})
	})
}

// exitPlan applies the plan decision through the reviewer bridge; errors stay
// visible in the dialog instead of closing it silently.
func exitPlan(runtime contracts.UIRuntime, st *state, action, errPrefix string) {
	if st.reviewer == nil {
		return
	}
	if _, err := st.reviewer.Exit(action); err != nil {
		st.planErr = errPrefix + err.Error()
		runtime.Invalidate()
		return
	}
	markPlanExited(st, action)
	st.planFeedbackSent = false
	popOverlay(runtime, st)
}

// closePlanAfterFailure dismisses a plan review whose last action failed. The
// dialog must never trap the window: the review is cancelled best effort, and
// the local snapshot is marked cancelled even when the bridge refuses so the
// overlay cannot be re-enqueued on the next frame.
func closePlanAfterFailure(runtime contracts.UIRuntime, st *state) {
	if st.reviewer != nil {
		_, _ = st.reviewer.Exit("cancel")
	}
	markPlanExited(st, "cancel")
	popOverlay(runtime, st)
}

// keepPlanning implements the keep-planning feedback loop. The feedback is
// delivered FIRST and the review closes only after the agent accepted it: the
// old order cancelled the review before sending, so a failed Send left a
// cancelled review whose overlay could never be closed and whose retry could
// never succeed (the reported deadlock). A failed delivery now leaves the
// review untouched, so the user can retry, approve, cancel, or press Escape
// to close.
func keepPlanning(runtime contracts.UIRuntime, st *state, feedback string) {
	if st.reviewer == nil || st.chat == nil {
		return
	}
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return
	}
	if !st.planFeedbackSent {
		sessionID := strings.TrimSpace(st.chat.ActiveSession())
		if sessionID == "" {
			st.planErr = st.tr("没有活动会话", "No active session")
			runtime.Invalidate()
			return
		}
		message := st.tr("请根据以下反馈继续完善计划：\n\n"+feedback,
			"Keep refining the plan with this feedback:\n\n"+feedback)
		if err := st.chat.Send(sessionID, message); err != nil {
			st.planErr = st.tr("发送继续规划反馈失败：", "Keep-planning send failed: ") + err.Error()
			runtime.Invalidate()
			return
		}
		st.planFeedbackSent = true
	}
	// The feedback is with the agent now. A failed exit keeps the dialog open
	// with the error, but a retry only closes the review: the same feedback is
	// never sent twice.
	if _, err := st.reviewer.Exit("cancel"); err != nil {
		st.planErr = st.tr("反馈已发送，但结束审阅失败：", "Feedback sent, but closing the review failed: ") + err.Error()
		runtime.Invalidate()
		return
	}
	markPlanExited(st, "cancel")
	st.planFeedbackSent = false
	if st.notifier != nil {
		st.notifier.Notify(contracts.NotifySuccess, st.tr("已发送继续规划反馈", "Sent keep-planning feedback"))
	}
	popOverlay(runtime, st)
}

// dialogEnter runs a dialog's primary action on Enter from the overlay scope,
// so it reaches the key wherever the focus is (a focused button clicks
// natively first; a text input forwards Enter through OnTextKey).
func dialogEnter(c *ui.Context, action func()) {
	if action == nil {
		return
	}
	c.OnShortcut(0, ui.KeyEnter, action)
}

// markPlanExited mirrors the decision into the local snapshot so the live
// reviewing state cannot re-enqueue the dialog before the host handler does.
func markPlanExited(st *state, action string) {
	if st.plan == nil {
		return
	}
	switch action {
	case "approve":
		st.plan.State = "approved"
	default:
		st.plan.State = "cancelled"
	}
}

// answerQuestion resolves the shown question with answer and pops it.
func answerQuestion(runtime contracts.UIRuntime, st *state, id, answer string) {
	if st.interaction != nil {
		st.interaction.Answer(id, answer)
	}
	popOverlay(runtime, st)
}

// cancelQuestion dismisses the shown question with an error and pops it.
func cancelQuestion(runtime contracts.UIRuntime, st *state, id string) {
	if st.interaction != nil {
		st.interaction.Cancel(id)
	}
	popOverlay(runtime, st)
}

// popOverlay removes the displayed overlay and resets the dialog state.
func popOverlay(runtime contracts.UIRuntime, st *state) {
	if len(st.overlays) > 0 {
		st.overlays = st.overlays[1:]
	}
	st.open = false
	st.currentKey = ""
	st.draft = ""
	st.planErr = ""
	st.planFeedback = ""
	st.planFeedbackSent = false
	st.askOtherOpen = false
	runtime.Invalidate()
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime, so activation failure and module disposal leave no residue.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

// firstNonEmpty returns the first non-empty trimmed string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
