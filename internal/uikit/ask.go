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

// askDialogBody is the Modal body: title, the question text, either one native
// full-width option button per choice (equal weight, vertically stacked) or a
// free-text input with Submit, and the Cancel action on the left of the footer.
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
	design.Apply(ui.Column(c).Width(design.DialogWidthS).Gap(design.Space4), func(el ui.Element) ui.Element {
		return el.PaddingX(design.Space6).PaddingY(design.Space2)
	}).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.Sparkles()).Size(design.Space5, design.Space5).TextColor(t.Accent)
			design.Apply(ui.Text(c, T(o.Locale, "需要你的确认", "Needs your input")),
				t.Font(design.TypeL)).TextColor(t.LabelPrimary)
		})
		design.Apply(ui.Text(c, o.Question.Text), t.Font(design.TypeBody)).TextColor(t.LabelPrimary)

		if len(o.Question.Options) > 0 {
			// Options are equal choices: one full-width row each, no primary
			// hierarchy that would silently rank them. The footer's Cancel
			// action holds the initial focus instead of the first option, so a
			// stray Enter cancels rather than answering with an option the user
			// never chose. Choosing an option is an explicit click or
			// Tab-then-Enter.
			ui.Column(c).FillWidth().AlignItems(ui.Stretch).Gap(design.Space2).Children(func() {
				for _, option := range o.Question.Options {
					option := option
					button := design.Apply(ui.Button(c, option), t.SecondaryButton()).FillWidth()
					if button.Clicked() {
						submitAnswer(option)
					}
				}
				// The fixed trailing choice makes the agent's options a complete
				// answer surface: "Other…" expands one inline text input whose
				// text becomes the answer (Enter submits, Esc collapses back to
				// the options). The option buttons stay visible so the user can
				// change their mind without cancelling the question.
				if !*otherOpen {
					otherLabel := T(o.Locale, "其他…", "Other…")
					other := design.Apply(ui.Button(c, otherLabel), t.SecondaryButton()).
						FillWidth().Label(otherLabel)
					if other.Clicked() {
						*otherOpen = true
						*answer = ""
						c.Invalidate()
					}
				} else {
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
								// is back to the option buttons.
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
			})
			ui.Divider(c)
			DialogFooter(c, []DialogAction{
				// The input owns the first focus while "Other…" is open; the
				// options' cancel action owns it otherwise (C-03: a stray Enter
				// must never answer an option the user did not choose).
				{Label: T(o.Locale, "取消", "Cancel"), Default: !*otherOpen, OnClick: cancel},
			})
			hintText := T(o.Locale,
				"Esc = 取消 · 用 Tab 选择选项后按 Enter 提交 · 「其他…」可自定义回答",
				"Esc = cancel · Tab to an option, then Enter to choose · \"Other…\" answers in your own words")
			if *otherOpen {
				hintText = T(o.Locale, "Enter = 提交自定义回答 · Esc = 返回选项",
					"Enter = submit your answer · Esc = back to the options")
			}
			design.Apply(ui.Text(c, hintText), t.TextMuted(design.TypeXXS))
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
		ui.Divider(c)
		DialogFooter(c, []DialogAction{
			{Label: T(o.Locale, "取消", "Cancel"), OnClick: cancel},
			{Label: T(o.Locale, "提交", "Submit"), Primary: true, Disabled: trimmed == "",
				OnClick: func() { submit(*answer) }},
		})
	})
}
