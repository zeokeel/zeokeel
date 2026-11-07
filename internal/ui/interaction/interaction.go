// Package interaction contributes the ask_user dialog: the native overlay that
// shows an agent's question and resolves it with the user's answer or cancel.
//
// It is a kernel UI module: activation requires the host UI runtime and the
// host UserInteraction service, installs the question handler (whose calls hop
// to the UI thread with UIRuntime.Post), and registers one overlay.dialogs slot
// item that renders a native mygo Modal. The dialog itself uses only native
// controls (Modal, TextInput, Buttons); nothing is hand-drawn.
//
// Questions are handled FIFO one at a time: the handler appends to the queue,
// the build shows the head, and answering/cancelling pops it so the next
// question opens on the following frame. Disposal clears the handler and
// cancels every pending question so an agent tool call can never hang on a
// dialog that no longer exists.
package interaction

import (
	"strings"

	"github.com/egoist/mygo/ui"
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

// Config is the module's typed assembly config (all copy is localized; the
// module has no configurable fields).
type Config struct{}

// state is the module instance's FIFO question queue and dialog state. It is
// touched only on the UI thread.
type state struct {
	queue     []contracts.UserQuestion
	currentID string
	open      bool
	draft     string

	// locale localizes the dialog copy per frame (zh is the default).
	locale contracts.LocaleService
}

// tr returns the localized copy for the current frame's language.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

// Module registers the ask_user dialog contribution.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		// The host provides the bridge only in desktop products; without it
		// (headless or a minimal test host) the dialog module degrades to a
		// no-op instead of failing the whole assembly.
		interaction, ok := ctx.Get(contracts.KeyUserInteraction)
		if !ok {
			return nil
		}
		locale, _ := ctx.Get(contracts.KeyLocale)
		st := &state{locale: locale}

		// The host invokes the handler from whichever goroutine asks the
		// question: hop to the UI thread and request a frame. Nothing here
		// touches frame state.
		interaction.SetHandler(func(question contracts.UserQuestion) {
			runtime.Post(func() {
				st.queue = append(st.queue, question)
				runtime.Invalidate()
			})
		})
		if err := own(ctx, func() (func(), error) {
			return func() {
				interaction.SetHandler(nil)
				for _, question := range st.queue {
					interaction.Cancel(question.ID)
				}
				st.queue = nil
				st.currentID = ""
			}, nil
		}); err != nil {
			interaction.SetHandler(nil)
			return err
		}

		item := contracts.SlotItem{
			Slot:  contracts.SlotOverlayDialogs,
			ID:    SlotID,
			Order: 20,
			Build: func(c *ui.Context) {
				buildDialog(c, runtime, interaction, st)
			},
		}
		return own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(item)
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		WithName("ZeoKeel ask_user dialog")
}

// buildDialog renders the oldest pending question in a native Modal, or nothing
// while the queue is empty. Escape/backdrop dismissal is treated as cancel so
// the waiting ask_user call always resolves.
func buildDialog(c *ui.Context, runtime contracts.UIRuntime, interaction contracts.UserInteraction, st *state) {
	if len(st.queue) == 0 {
		st.open = false
		st.currentID = ""
		st.draft = ""
		return
	}
	question := st.queue[0]
	if st.currentID != question.ID {
		st.currentID = question.ID
		st.open = true
		st.draft = ""
	}
	ui.Modal(c, &st.open, func() {
		questionPanel(c, runtime, interaction, st, question)
	})
	if !st.open && st.currentID == question.ID {
		// Escape or a backdrop click closed the native Modal: cancel the
		// waiting question and advance the queue. A button answer/cancel
		// already popped the question, so this only handles dismissal.
		cancelQuestion(runtime, interaction, st, question.ID)
	}
}

// questionPanel is the Modal body: title, the question text, either one native
// button per option or a free-text input with Submit, and the Cancel action.
func questionPanel(c *ui.Context, runtime contracts.UIRuntime, interaction contracts.UserInteraction, st *state, question contracts.UserQuestion) {
	t := design.Of(c)
	design.Apply(ui.Column(c).Width(520).Gap(design.Space4), func(el ui.Element) ui.Element {
		return el.PaddingX(design.Space6).PaddingY(design.Space2)
	}).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.Sparkles()).Size(design.Space5, design.Space5).TextColor(t.Accent)
			design.Apply(ui.Text(c, st.tr("需要你的确认", "Needs your input")),
				t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
		})
		design.Apply(ui.Text(c, question.Text), t.Font(design.TypeBody)).TextColor(t.LabelPrimary)

		if len(question.Options) > 0 {
			ui.Row(c).FillWidth().Justify(ui.End).Wrap().Gap(design.Space2).Children(func() {
				for index, option := range question.Options {
					option := option
					var button ui.Element
					if index == 0 {
						button = design.Apply(ui.PrimaryButton(c, option), t.PrimaryButton())
					} else {
						button = design.Apply(ui.Button(c, option), secondaryButtonStyle(t))
					}
					if button.Clicked() {
						answerQuestion(runtime, interaction, st, question.ID, option)
					}
				}
				if design.Apply(ui.Button(c, st.tr("取消", "Cancel")), secondaryButtonStyle(t)).Clicked() {
					cancelQuestion(runtime, interaction, st, question.ID)
				}
			})
			return
		}

		// Free-text question.
		ui.TextInput(c, &st.draft).Label(st.tr("回答", "Answer")).
			Placeholder(st.tr("输入你的回答", "Type your answer")).FillWidth()
		answer := strings.TrimSpace(st.draft)
		ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
			submit := design.Apply(ui.PrimaryButton(c, st.tr("提交", "Submit")), t.PrimaryButton()).
				Disabled(answer == "")
			if submit.Clicked() && answer != "" {
				answerQuestion(runtime, interaction, st, question.ID, answer)
			}
			if design.Apply(ui.Button(c, st.tr("取消", "Cancel")), secondaryButtonStyle(t)).Clicked() {
				cancelQuestion(runtime, interaction, st, question.ID)
			}
		})
	})
}

// answerQuestion resolves the shown question with answer, pops it and asks for
// the next frame (which opens the next queued question, if any).
func answerQuestion(runtime contracts.UIRuntime, interaction contracts.UserInteraction, st *state, id, answer string) {
	interaction.Answer(id, answer)
	popQuestion(runtime, st)
}

// cancelQuestion dismisses the shown question with an error, pops it and asks
// for the next frame.
func cancelQuestion(runtime contracts.UIRuntime, interaction contracts.UserInteraction, st *state, id string) {
	interaction.Cancel(id)
	popQuestion(runtime, st)
}

// popQuestion removes the displayed question and resets the dialog state.
func popQuestion(runtime contracts.UIRuntime, st *state) {
	if len(st.queue) > 0 {
		st.queue = st.queue[1:]
	}
	st.open = false
	st.currentID = ""
	st.draft = ""
	runtime.Invalidate()
}

// secondaryButtonStyle is the brand's secondary dialog button.
func secondaryButtonStyle(t *design.Tokens) design.Style {
	return func(el ui.Element) ui.Element {
		return el.Height(36).PaddingX(design.Space3).Radius(design.RadiusMD).
			Background(t.BgRaised).Border(1, t.BorderL2).TextColor(t.LabelPrimary)
	}
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
