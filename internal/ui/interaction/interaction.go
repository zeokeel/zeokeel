// Package interaction contributes the agent-to-user overlays: the ask_user
// dialog, the present artifact preview and the plan review dialog.
//
// It is a kernel UI module: activation requires the host UI runtime, installs
// the handlers of the optional host bridges (UserInteraction, ArtifactPresenter
// and PlanReviewer — each handler call hops to the UI thread with
// UIRuntime.Post), and registers one overlay.dialogs slot item that renders a
// native mygo Modal. The dialogs use only native controls (Modal, TextInput,
// Buttons, Markdown) styled with internal/design; nothing is hand-drawn.
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

// maxOverlayBodyHeight bounds the Markdown preview inside the modal.
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
	open       bool
	draft      string

	plan         *contracts.PlanReview
	planErr      string
	planFeedback string
	// planHold keeps the plan overlay visible after reviewer.Exit succeeded but
	// the keep-planning feedback could not be delivered; the cancelled plan
	// state would otherwise remove the modal on the next frame.
	planHold bool

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
						st.planHold = false
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
			st.planHold = false
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
		if st.planHold {
			return
		}
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

// buildDialog renders the oldest queued overlay in a native Modal, or nothing
// while the queue is empty. Escape/backdrop dismissal resolves the overlay as
// if its cancel action had been used.
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
		st.planHold = false
	}
	ui.Modal(c, &st.open, func() {
		switch item.kind {
		case kindArtifact:
			artifactPanel(c, runtime, st, item.artifact)
		case kindPlan:
			planPanel(c, runtime, st)
		default:
			questionPanel(c, runtime, st, item.question)
		}
	})
	if !st.open && st.currentKey == key {
		if item.kind == kindPlan {
			// Esc (or a click outside) must not cancel a plan: the review stays
			// open and the footer's explicit Approve/Cancel actions decide.
			st.open = true
			runtime.Invalidate()
			return
		}
		// Escape or a backdrop click closed the native Modal: reuse the
		// overlay's cancel semantics and advance the queue. A button action
		// already popped the overlay, so this only handles dismissal.
		dismissOverlay(runtime, st, item)
	}
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
			design.Apply(ui.Text(c, title), t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
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

// planPanel is the plan review dialog: the plan Markdown, the keep-planning
// feedback and the decision footer. Approve is the accent primary on the right
// (Enter); cancel is an ordinary secondary action on the left, never the
// dangerous default. Escape does not cancel the plan (see buildDialog).
func planPanel(c *ui.Context, runtime contracts.UIRuntime, st *state) {
	t := design.Of(c)
	if st.reviewer == nil || st.plan == nil {
		return
	}
	approve := func() { exitPlan(runtime, st, "approve", st.tr("批准失败：", "Approve failed: ")) }
	design.Apply(ui.Column(c).Width(design.DialogWidthL).Gap(design.Space4), func(el ui.Element) ui.Element {
		return el.PaddingX(design.Space6).PaddingY(design.Space2)
	}).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.Activity()).Size(design.Space5, design.Space5).TextColor(t.Accent)
			design.Apply(ui.Text(c, st.tr("计划待审阅", "Plan review")),
				t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
		})
		ui.Scroll(c).FillWidth().MaxHeight(maxOverlayBodyHeight).Children(func() {
			markdown.View(c, st.plan.Content, nil)
		})
		if st.chat != nil {
			// Enter submits the form's current content: a non-empty feedback
			// goes to Keep planning (the agent receives it); approving the plan
			// always needs the explicit button or the focused Approve button.
			ui.TextInput(c, &st.planFeedback).
				Label(st.tr("继续规划反馈", "Keep-planning feedback")).
				Placeholder(st.tr("告诉 agent 计划需要怎么改…", "Tell the agent how the plan should change…")).
				FillWidth().
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEnter || k.Mods != 0 {
						return false
					}
					if strings.TrimSpace(st.planFeedback) != "" {
						keepPlanning(runtime, st)
					}
					// An empty feedback never approves: the decision is explicit.
					return true
				})
		}
		if st.planErr != "" {
			uikit.ErrorLine(c, st.planErr)
		}
		ui.Divider(c)
		actions := []uikit.DialogAction{
			{Label: st.tr("取消计划", "Cancel plan"), OnClick: func() {
				exitPlan(runtime, st, "cancel", st.tr("取消失败：", "Cancel failed: "))
			}},
		}
		if st.chat != nil {
			actions = append(actions, uikit.DialogAction{
				Label:    st.tr("继续规划", "Keep planning"),
				Disabled: strings.TrimSpace(st.planFeedback) == "",
				OnClick:  func() { keepPlanning(runtime, st) },
			})
		}
		actions = append(actions, uikit.DialogAction{
			Label: st.tr("批准", "Approve"), Primary: true, OnClick: approve,
		})
		uikit.DialogFooter(c, actions)
		hint := st.tr("点击「批准」批准计划 · Esc 不会取消计划",
			"Click Approve to accept · Esc does not cancel the plan")
		if st.chat != nil {
			hint = st.tr("输入反馈后按 Enter = 继续规划 · 点击「批准」批准计划 · Esc 不会取消计划",
				"Enter with feedback = keep planning · Click Approve to accept · Esc does not cancel the plan")
			if strings.TrimSpace(st.planFeedback) != "" {
				hint = st.tr("Enter = 继续规划（发送反馈给 agent）· 点击「批准」批准计划",
					"Enter = keep planning with your feedback · Click Approve to accept")
			}
		}
		design.Apply(ui.Text(c, hint), t.TextMuted(design.TypeXXS))
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
	popOverlay(runtime, st)
}

// keepPlanning implements the keep-planning feedback loop: cancel the current
// review, send the user's feedback to the active agent turn and pop the dialog.
// The modal is held open when the plan was cancelled but the feedback could not
// be delivered (or no session is active), so the failure stays visible.
func keepPlanning(runtime contracts.UIRuntime, st *state) {
	if st.reviewer == nil || st.chat == nil {
		return
	}
	feedback := strings.TrimSpace(st.planFeedback)
	if feedback == "" {
		return
	}
	if _, err := st.reviewer.Exit("cancel"); err != nil {
		st.planErr = st.tr("继续规划失败：", "Keep planning failed: ") + err.Error()
		runtime.Invalidate()
		return
	}
	markPlanExited(st, "cancel")
	sessionID := strings.TrimSpace(st.chat.ActiveSession())
	if sessionID == "" {
		st.planErr = st.tr("没有活动会话", "No active session")
		st.planHold = true
		runtime.Invalidate()
		return
	}
	message := st.tr("请根据以下反馈继续完善计划：\n\n"+feedback,
		"Keep refining the plan with this feedback:\n\n"+feedback)
	if err := st.chat.Send(sessionID, message); err != nil {
		st.planErr = st.tr("发送继续规划反馈失败：", "Keep-planning send failed: ") + err.Error()
		st.planHold = true
		runtime.Invalidate()
		return
	}
	st.planHold = false
	if st.notifier != nil {
		st.notifier.Notify(contracts.NotifySuccess, st.tr("已发送继续规划反馈", "Sent keep-planning feedback"))
	}
	popOverlay(runtime, st)
}

// questionPanel is the Modal body: title, the question text, either one native
// full-width option button per choice (equal weight, vertically stacked) or a
// free-text input with Submit, and the Cancel action on the left of the footer.
func questionPanel(c *ui.Context, runtime contracts.UIRuntime, st *state, question contracts.UserQuestion) {
	t := design.Of(c)
	design.Apply(ui.Column(c).Width(design.DialogWidthS).Gap(design.Space4), func(el ui.Element) ui.Element {
		return el.PaddingX(design.Space6).PaddingY(design.Space2)
	}).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.Sparkles()).Size(design.Space5, design.Space5).TextColor(t.Accent)
			design.Apply(ui.Text(c, st.tr("需要你的确认", "Needs your input")),
				t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
		})
		design.Apply(ui.Text(c, question.Text), t.Font(design.TypeBody)).TextColor(t.LabelPrimary)

		if len(question.Options) > 0 {
			// Options are equal choices: one full-width row each, no primary
			// hierarchy that would silently rank them, no Enter default.
			ui.Column(c).FillWidth().AlignItems(ui.Stretch).Gap(design.Space2).Children(func() {
				for _, option := range question.Options {
					option := option
					button := design.Apply(ui.Button(c, option), t.SecondaryButton()).FillWidth()
					if button.Clicked() {
						answerQuestion(runtime, st, question.ID, option)
					}
				}
			})
			ui.Divider(c)
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() { cancelQuestion(runtime, st, question.ID) }},
			})
			design.Apply(ui.Text(c, st.tr("Esc = 取消", "Esc = cancel")),
				t.TextMuted(design.TypeXXS))
			return
		}

		// Free-text question: Enter submits the answer.
		submit := func(answer string) {
			if strings.TrimSpace(answer) == "" {
				return
			}
			answerQuestion(runtime, st, question.ID, answer)
		}
		dialogEnter(c, func() { submit(st.draft) })
		ui.TextInput(c, &st.draft).Label(st.tr("回答", "Answer")).
			Placeholder(st.tr("输入你的回答", "Type your answer")).FillWidth().
			OnTextKey(uikit.EnterTo(func() { submit(st.draft) }))
		answer := strings.TrimSpace(st.draft)
		ui.Divider(c)
		uikit.DialogFooter(c, []uikit.DialogAction{
			{Label: st.tr("取消", "Cancel"), OnClick: func() { cancelQuestion(runtime, st, question.ID) }},
			{Label: st.tr("提交", "Submit"), Primary: true, Disabled: answer == "",
				OnClick: func() { submit(st.draft) }},
		})
	})
}

// dialogEnter runs a dialog's primary action on Enter from the overlay scope,
// so it reaches the key wherever the focus is (a focused button clicks
// natively first; a text input forwards Enter through OnTextKey/enterTo).
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

// dismissOverlay applies the cancel semantics of one overlay (question cancel,
// plan exit-cancel, artifact plain close) and pops it.
func dismissOverlay(runtime contracts.UIRuntime, st *state, item overlayItem) {
	switch item.kind {
	case kindQuestion:
		if st.interaction != nil {
			st.interaction.Cancel(item.question.ID)
		}
	case kindPlan:
		if st.reviewer != nil {
			if _, err := st.reviewer.Exit("cancel"); err == nil {
				markPlanExited(st, "cancel")
			}
		}
	}
	popOverlay(runtime, st)
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
	st.planHold = false
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
