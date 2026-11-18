package uikit

// The ask-user dialog entry component (AGENT-SURFACES.md S6): the native mygo
// Modal an agent's question is answered in. The component owns the appearance,
// the option layout and the safety keyboard contract; the module keeps the FIFO
// overlay queue and hands the answer to the interaction bridge.
//
// Safety contract (locked here, never re-composed by a caller):
//   - the options are equal choices: one full-width SecondaryButton each, no
//     primary hierarchy that would silently rank them, plus one trailing
//     "Other…" choice;
//   - while the options show, the footer's Cancel action is the Default (when
//     no "Other…" input holds the keyboard), so a stray Enter cancels instead
//     of answering with an option the user never chose (C-03);
//   - "Other…" opens one inline TextInput that takes the keyboard
//     (AutoFocus): Enter submits its non-empty text, Escape collapses back to
//     the options instead of dismissing the question;
//   - a free-text question submits only a non-empty answer, and its Submit
//     action stays disabled while the input is empty.
//
// The dialog draws only native controls (Modal, Column, Text, Button,
// TextInput, Divider, DialogFooter) styled with internal/design.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// askOtherChoice is the sentinel value of the option list's fixed trailing
// "其他…" row: choosing it reveals the free-text input instead of answering.
const askOtherChoice = "\x00other"

// AskDialogOptions is one agent question, rendered as the product's ask-user
// Modal.
type AskDialogOptions struct {
	// Question is the question body and its selectable options; no options mean
	// a free-text answer.
	Question contracts.UserQuestion
	// OtherOpen is the "Other…" inline input's open flag; nil lets the dialog
	// own it.
	OtherOpen *bool
	// Answer is the answer buffer: the "Other…" text for an options question,
	// the typed answer for a free-text question; nil lets the dialog own it.
	Answer *string
	// OnAnswer receives the chosen option or the non-empty custom answer.
	OnAnswer func(answer string)
	// OnCancel is the cancel/dismiss action (the footer's Cancel, Escape or a
	// click outside).
	OnCancel func()
	// Locale localizes the component's copy; nil falls back to the product's
	// Chinese copy.
	Locale contracts.LocaleService
}

// AskDialog renders the question in a native Modal. The component owns the
// dialog's visual state: the open flag and, when the caller passes nil, the
// "Other…" flag and the answer buffer, keyed by the question identity so the
// next question starts on its options. It is a frame builder: it only builds
// controls, except for the answer and dismissal handlers.
func AskDialog(c *ui.Context, o AskDialogOptions) {
	wrap := ui.Column(c.Key(askDialogKey(o.Question)))
	open := ui.Local(wrap, "open", func() bool { return true })
	otherOpen := o.OtherOpen
	if otherOpen == nil {
		otherOpen = ui.Local(wrap, "otherOpen", func() bool { return false })
	}
	answer := o.Answer
	if answer == nil {
		answer = ui.Local(wrap, "answer", func() string { return "" })
	}
	wrap.Children(func() {
		el := ui.Modal(c, open, func() {
			askDialogBody(c, o, open, otherOpen, answer)
		})
		if !el.Dismissed() {
			return
		}
		// Escape or a click outside the Modal cancels the question (the
		// "Other…" input consumes its own Escape before this handler).
		if o.OnCancel != nil {
			o.OnCancel()
			return
		}
		*open = true
		c.Invalidate()
	})
}

// askDialogKey derives the dialog's stable identity: its own visual state must
// reset for the next question, so the id (or the text of an unidentified
// question) names it.
func askDialogKey(question contracts.UserQuestion) string {
	if question.ID != "" {
		return "uikit.ask/dialog." + question.ID
	}
	return "uikit.ask/dialog!" + question.Text
}

// askDialogBody is the Modal body: the question, one radio row per choice (plus
// the trailing "其他…" row and its inline input), and a footer whose only accent
// fill is Submit. The options are the official radio group — one keyboard stop,
// arrows to choose — so a stray Enter can never answer with a choice the user
// did not make (S6); selecting is separate from submitting.
func askDialogBody(c *ui.Context, o AskDialogOptions, open, otherOpen *bool, answer *string) {
	t := design.Of(c)
	submitAnswer := func(value string) {
		*open = false
		if o.OnAnswer != nil {
			o.OnAnswer(value)
		}
	}
	cancel := func() {
		*open = false
		if o.OnCancel != nil {
			o.OnCancel()
		}
	}
	// The same card rhythm as the plan review and the approval card: Space4 of
	// room inside, Space6 at the sides, Space3 between the blocks and the action
	// row, so the two "your decision" cards do not read as two different
	// components (2026-11 per-screen review, p2-ask vs p2-plan).
	root := design.Apply(ui.Column(c).Width(design.DialogWidthS).Gap(design.Space3),
		func(el ui.Element) ui.Element {
			return el.Padding(design.Space4, design.Space6)
		})
	// The chosen option is the dialog's own state, keyed by the question: the
	// next question starts with nothing chosen and Submit disabled.
	choice := ui.Local(root, "choice", func() string { return "" })
	root.Children(func() {
		// The card's first line: the ask glyph and its 14/600 heading. It is not
		// a dialog title (the Modal has none): the card states what it is, then
		// the question.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.Sparkles()).Size(design.Space3, design.Space3).
				TextColor(t.Accent)
			design.Apply(ui.Text(c, T(o.Locale, "需要你的确认", "Needs your input")),
				t.Font(design.TypeS)).TextColor(t.LabelPrimary).
				Role(ui.RoleHeading).Level(2)
		})
		design.Apply(ui.Text(c, o.Question.Text), t.Font(design.TypeBody)).
			TextColor(t.LabelSecondary)

		if len(o.Question.Options) > 0 {
			// Options are equal choices: the official radio group gives one
			// keyboard stop, the arrows choose, and Space selects. No option is
			// a primary action, so the card never ranks the agent's suggestions.
			// The fixed trailing "其他…" is one more row of the same group, so the
			// choice list has one vocabulary instead of a radio stack with a
			// button under it.
			ui.RadioGroup(c, func() {
				for _, option := range o.Question.Options {
					option := option
					ui.Radio(c, choice, option, option).
						Height(design.ControlHeight).PaddingX(design.Space2).
						FillWidth()
				}
				ui.Radio(c, choice, askOtherChoice, T(o.Locale, "其他…", "Other…")).
					Height(design.ControlHeight).PaddingX(design.Space2).FillWidth()
			}).Label(T(o.Locale, "选项", "Options")).Gap(design.Space1).FillWidth()
			*otherOpen = *choice == askOtherChoice
			// Choosing "其他…" expands one inline text input under the rows whose
			// text becomes the answer (Enter submits, Esc collapses back to the
			// options). The rows stay visible so the user can change their mind
			// without cancelling the question.
			if *otherOpen {
				ui.TextInput(c, answer).
					Label(T(o.Locale, "其他回答", "Other answer")).
					Placeholder(T(o.Locale, "输入自定义回答", "Type your own answer")).
					FillWidth().AutoFocus().
					OnTextKey(func(k ui.TextKey) bool {
						if k.Mods != 0 {
							return false
						}
						switch k.Key {
						case ui.KeyEscape:
							// Collapse, never dismiss the question: the user
							// is back to the option rows.
							*choice = ""
							*otherOpen = false
							*answer = ""
							c.Invalidate()
							return true
						case ui.KeyEnter:
							if custom := strings.TrimSpace(*answer); custom != "" {
								submitAnswer(custom)
							}
							return true
						}
						return false
					})
			}
			*choice = strings.TrimSpace(*choice)
			answerValue := ""
			switch {
			case *otherOpen:
				answerValue = strings.TrimSpace(*answer)
			default:
				answerValue = *choice
			}
			// The footer states the keys beside the actions, not below them: the
			// hint shares the action row's baseline (VD-32).
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				approvalKbdHint(c, t, T(o.Locale, "关闭问题", "closes the question"), FormatShortcut("Esc"))
				ui.Spacer(c).Grow(1)
				cancelButton := design.Apply(ui.Button(c, T(o.Locale, "取消", "Cancel")),
					t.SecondaryButton())
				// The cancel action keeps the Default while no "Other…" input holds
				// the keyboard, so a stray Enter dismisses the question instead of
				// answering one of its options (C-03).
				if !*otherOpen {
					cancelButton = cancelButton.AutoFocus()
				}
				if cancelButton.Clicked() {
					cancel()
				}
				submitLabel := T(o.Locale, "提交", "Submit")
				var submit ui.Element
				if answerValue == "" {
					// A blocked Submit explains itself by its look: a neutral fill
					// with tertiary text, not a 50% accent wash that still reads as
					// a live brand button (VD-20).
					submit = design.Apply(ui.Button(c, submitLabel), t.DisabledPrimary()).
						Disabled(true)
				} else {
					submit = design.Apply(ui.PrimaryButton(c, submitLabel), t.PrimaryButton())
				}
				if submit.Clicked() {
					submitAnswer(answerValue)
				}
			})
			return
		}

		// Free-text question: Enter submits the answer.
		submit := func(value string) {
			if strings.TrimSpace(value) == "" {
				return
			}
			submitAnswer(value)
		}
		c.OnShortcut(0, ui.KeyEnter, func() { submit(*answer) })
		ui.TextInput(c, answer).Label(T(o.Locale, "回答", "Answer")).
			Placeholder(T(o.Locale, "输入你的回答", "Type your answer")).FillWidth().
			OnTextKey(EnterTo(func() { submit(*answer) }))
		trimmed := strings.TrimSpace(*answer)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			approvalKbdHint(c, t, T(o.Locale, "关闭问题", "closes the question"), FormatShortcut("Esc"))
			ui.Spacer(c).Grow(1)
			if cancelButton := design.Apply(ui.Button(c, T(o.Locale, "取消", "Cancel")),
				t.SecondaryButton()); cancelButton.Clicked() {
				cancel()
			}
			submitLabel := T(o.Locale, "提交", "Submit")
			var button ui.Element
			if trimmed == "" {
				button = design.Apply(ui.Button(c, submitLabel), t.DisabledPrimary()).
					Disabled(true)
			} else {
				button = design.Apply(ui.PrimaryButton(c, submitLabel), t.PrimaryButton())
			}
			if button.Clicked() {
				submit(*answer)
			}
		})
	})
}
