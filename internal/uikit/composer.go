package uikit

// The one composer shell (S7 in docs/AGENT-SURFACES.md): the conversation screen
// and Home are the same control in two places, so they share the card
// (t.Composer()), the width policy (the transcript reading column), the
// multiline input with its focus ring, the action row with the send/queue/stop
// control pinned at its right edge, the key hint and the error rows. The
// per-surface business — the attach/model/session menus, staged attachments, the
// queue, sending — comes in through slots, so neither surface can drift on the
// shell's own look.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// noEdge is the zero edge of the composer input: the shared shell zero-pads the
// textarea's vertical edges so the input hugs the action row (mygo's TextArea
// otherwise keeps its own vertical padding). design has no Space0 token yet, so
// the named zero keeps the metric call free of a bare literal until one lands.
const noEdge float32 = 0

// ComposerShellOptions is one composer card. The shell owns the frame, the
// input, the send/queue/stop position and the key semantics; the caller owns the
// surfaces' slots and the draft/submit logic.
type ComposerShellOptions struct {
	// Key prefixes the composer's element keys, so two surfaces keep stable
	// identities.
	Key string
	// Draft is the focused text area's bound draft. Required.
	Draft *string
	// Placeholder is the input's placeholder and its accessible label.
	Placeholder string
	// Running is true while the turn streams: the send position becomes the
	// queue (when QueueSend is set) and stop controls.
	Running bool
	// Attachments builds the staged-attachment rows above the input (the
	// attachments a surface keeps are its business).
	Attachments func(c *ui.Context)
	// Notices builds the quiet copy between the input and the status area (a
	// missing API key, "Enter is unavailable while generating", …).
	Notices func(c *ui.Context)
	// Queue lists the prompts waiting for the running turn: the shell draws the
	// shared queue badge and its popover through RunStatus. QueueOpen is the
	// popover's caller-owned state; QueueList backs its rows.
	Queue []string
	// QueueOpen is the queue popover's caller-owned open flag; nil leaves the
	// popover closed.
	QueueOpen *bool
	// QueueList is the caller's List state for the queue popover rows.
	QueueList *ui.ListState
	// QueuePaused says a stop paused the queue: the popover explains it and
	// offers the explicit resume action.
	QueuePaused bool
	// QueueError is the last queued-delivery failure; the shell shows the error
	// row and, while no turn runs, a retry action.
	QueueError string
	// OnQueueCancel/OnQueueClear/OnQueueResume/OnRetryQueue are the queue
	// actions; nil hides the matching control.
	OnQueueCancel func(index int)
	OnQueueClear  func()
	OnQueueResume func()
	OnRetryQueue  func()
	// Toolbar builds the action row's left side (attach/model/session controls).
	Toolbar func(c *ui.Context)
	// Usage builds the rows under the card (the session usage strip).
	Usage func(c *ui.Context)
	// Send submits the draft and reports whether it was accepted (a successful
	// action focuses the composer again, as a mouse send hands the keyboard
	// back).
	Send func() bool
	// QueueSend queues the draft while a turn runs; nil hides the queue control
	// (queueing off).
	QueueSend func() bool
	// Stop interrupts the running turn; nil leaves no stop control.
	Stop func()
	// OnKey handles the input's keys first: the surface's suggestion menus and
	// slash/mention/queue semantics run before the shell's defaults.
	OnKey func(ui.TextKey) bool
	// InputReady runs right after the input is built, before the slots: a surface
	// that tracks the caret (the @ menu follows it) or focuses the input itself
	// reads/writes it there.
	InputReady func(c *ui.Context, input ui.Element)
	// OnPasteImage turns an image paste into a staged attachment. mygo's
	// OnPaste sees a paste before it is inserted and passes the clipboard's text
	// (empty when it holds none, as with an image); the shell gives text pastes
	// back to the input untouched and calls this hook only for an empty paste.
	// True takes the paste so nothing is inserted. nil leaves image pastes inert
	// (a surface without an attachment path should still pass a hook that tells
	// the user how to attach the image instead of failing silently).
	OnPasteImage func() bool
	// Focus asks the shell to focus the input on this frame (one-shot: the shell
	// clears it).
	Focus *bool
	// InputBind binds the input's Handle for persistent focus queries and
	// requests (the caller keeps it in its state).
	InputBind *ui.Handle
	// OnWidth receives the card's last laid-out width before the slots build, for
	// a toolbar that folds its low-frequency controls on a narrow composer.
	OnWidth func(width float32)
	// Hint builds the quiet key hint at the action row's end; empty hides it (the
	// surface decides when a hint helps, e.g. not on a narrow composer).
	Hint func(c *ui.Context) string
	// SendEnabled disables the send control (empty draft, no backend).
	SendEnabled bool
	// QueueEnabled disables the queue control (queueing sends text only, so a
	// draft is required).
	QueueEnabled bool
	// SendLabel/SendTooltip are the localized send copy ("发送中…" while a
	// surface-send is in flight, the key hint in the tooltip).
	SendLabel   string
	SendTooltip string
	// QueueLabel/QueueTooltip are the localized queue control copy.
	QueueLabel   string
	QueueTooltip string
	// StopLabel/StopTooltip are the localized stop copy.
	StopLabel   string
	StopTooltip string
	// Error is one error row under the action row (the surface's error line).
	Error string
	// Errors are further error rows under the action row, in order.
	Errors []string
	// Notice is a quiet non-error note after the error rows.
	Notice string
	// FullWidth fills the surface's container up to the reading width instead of
	// capping the card at 92% of it: Home's container already is the readable
	// column, so its composer spans it like the rest of Home.
	FullWidth bool
	// CardLabel is the card's accessible name; empty uses a generic one.
	CardLabel string
	// Locale localizes the shell's own copy; nil falls back to Chinese.
	Locale contracts.LocaleService
}

// ComposerShell draws one composer card: the raised panel, the multiline input,
// the slots, the action row with the fixed send/queue/stop control and the quiet
// hint. It returns the card and the input for the caller's anchoring/accessibility
// wiring: the card (the surface's suggestion popovers anchor to it) and the text
// area (the surface tracks its caret and points ActiveDescendant at an open
// suggestion menu). The elements are returned, not stored, so the frame's build
// values never outlive the pass.
func ComposerShell(c *ui.Context, o ComposerShellOptions) (card, input ui.Element) {
	t := design.Of(c)
	th := t.Theme()
	key := o.Key
	if key == "" {
		key = "uikit.composer"
	}
	cardLabel := o.CardLabel
	if cardLabel == "" {
		cardLabel = T(o.Locale, "输入框", "Composer")
	}
	ui.Column(c.Key(key)).FillWidth().PaddingY(th.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Row(c.Key(key + ".row")).FillWidth().Justify(ui.Center).Children(func() {
			card = design.Apply(ui.Column(c.Key(key+".card")), t.Composer()).
				Padding(th.Space(3)).Gap(design.Space2).
				Label(cardLabel)
			// The reading width is the card's ceiling: the conversation composer
			// caps it at 92% of the screen's content area (a narrow window keeps a
			// margin), while a surface whose container already is the reading
			// column (Home's 720 column) fills that container instead of shrinking
			// it a second time.
			if o.FullWidth {
				card = card.FillWidth().MaxWidth(design.TranscriptWidth)
			} else {
				card = card.Width(design.TranscriptWidth).MaxWidthPercent(92)
			}
			if o.OnWidth != nil {
				o.OnWidth(card.Bounds().W)
			}
			card.Children(func() {
				if o.Attachments != nil {
					o.Attachments(c)
				}
				input = ui.TextArea(c.Key(key+".input"), o.Draft).
					Label(o.Placeholder).
					Placeholder(o.Placeholder).
					Background(ui.Transparent).Border(0, ui.Transparent).
					Padding(noEdge, th.Space(1)).
					MinHeight(th.Space(9)).
					// A long draft must not push the action row (and the
					// transcript) out of the window: the input grows up to about
					// six Space8 rows and scrolls inside.
					MaxHeight(design.Space8 * 6).
					Grow(1).
					OnTextKey(composerShellKey(o))
				// An image paste is an attachment, not text: the shell sees the
				// empty paste and asks the surface to stage the clipboard image
				// (the surface owns attachments). A non-empty paste keeps the
				// native insert path.
				if o.OnPasteImage != nil {
					input.OnPaste(func(text string) bool {
						if text != "" {
							return false
						}
						return o.OnPasteImage()
					})
				}
				if o.InputBind != nil {
					input.Bind(o.InputBind)
				}
				if o.InputReady != nil {
					o.InputReady(c, input)
				}
				if o.Focus != nil && *o.Focus {
					*o.Focus = false
					if o.InputBind != nil {
						o.InputBind.Focus(c)
					} else {
						input.Focus()
					}
				}
				if o.Notices != nil {
					o.Notices(c)
				}
				// The queue is the shared run-status vocabulary (S8): badge with
				// its popover, per-row cancel, paused explanation/resume, clear,
				// and the failed-delivery retry.
				if len(o.Queue) > 0 || o.QueueError != "" {
					RunStatus(c, RunStatusOptions{
						Queued:        o.Queue,
						QueueOpen:     o.QueueOpen,
						QueueList:     o.QueueList,
						QueuePaused:   o.QueuePaused,
						QueueError:    o.QueueError,
						OnQueueCancel: o.OnQueueCancel,
						OnQueueClear:  o.OnQueueClear,
						OnQueueResume: o.OnQueueResume,
						OnRetryQueue:  o.OnRetryQueue,
						Locale:        o.Locale,
					})
				}
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					if o.Toolbar != nil {
						o.Toolbar(c)
					}
					if o.Hint != nil {
						if hint := o.Hint(c); hint != "" {
							// The hint is a sentence (what Enter/Esc do), so it
							// uses the metadata step 12, not the 11 micro step.
							design.Apply(ui.Text(c, hint), t.Font(design.TypeXXS)).
								TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Tooltip(hint)
						}
					}
					ui.Spacer(c).Grow(1)
					composerShellActions(c, t, o)
				})
				if o.Error != "" {
					ErrorLine(c, o.Error)
				}
				for _, text := range o.Errors {
					if strings.TrimSpace(text) != "" {
						ErrorLine(c, text)
					}
				}
				if o.Notice != "" {
					design.Apply(ui.Text(c, o.Notice), t.Font(design.TypeXXS)).
						TextColor(t.LabelTertiary).MaxLines(2)
				}
			})
			// The composer's focus/hover ring also applies while a reply runs:
			// the input is editable then too, so the ring must not disappear.
			switch {
			case input.Focused():
				card.Border(1, t.Accent)
			case input.Hovered():
				card.Border(1, t.BorderL3)
			}
		})
		if o.Usage != nil {
			o.Usage(c)
		}
	})
	return card, input
}

// composerShellKey is the input's key hook: the surface's OnKey runs first (the
// suggestion menus, the slash/mention handling, the running-turn interrupt and
// the queueing decision), then the shell's defaults — Esc stops a running turn,
// Enter or Cmd/Ctrl+Enter sends (queues while running when QueueSend is set),
// and Shift/Alt+Enter reach the input as a newline.
func composerShellKey(o ComposerShellOptions) func(ui.TextKey) bool {
	return func(k ui.TextKey) bool {
		if o.OnKey != nil && o.OnKey(k) {
			return true
		}
		if k.Key == ui.KeyEscape && k.Mods == 0 && o.Running && o.Stop != nil {
			o.Stop()
			return true
		}
		if k.Key != ui.KeyEnter || (k.Mods != 0 && k.Mods != ui.Cmd) {
			return false
		}
		if o.Running {
			// A running turn never sends: queue the draft when queueing is on,
			// otherwise let Enter insert a newline and keep the draft.
			if o.QueueSend == nil {
				return false
			}
			if o.QueueSend() {
				focusComposerAgain(o)
			}
			return true
		}
		if o.Send != nil && o.Send() {
			focusComposerAgain(o)
			k.SetText("")
		}
		return true
	}
}

// focusComposerAgain hands the keyboard back to the composer after an accepted
// send-shaped action, so the user can keep typing without clicking the input.
func focusComposerAgain(o ComposerShellOptions) {
	if o.Focus != nil {
		*o.Focus = true
	}
}

// composerShellActions draws the action row's right edge: the send control when
// idle, and the queue plus stop controls while a turn runs. They are plain row
// siblings rather than toolbar items, so no overflow menu can ever take the
// primary action.
func composerShellActions(c *ui.Context, t *design.Tokens, o ComposerShellOptions) {
	if o.Running {
		if o.QueueSend != nil {
			label := o.QueueLabel
			if label == "" {
				label = T(o.Locale, "排队", "Queue")
			}
			button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
				Label(label).Shrink(0)
			if o.QueueTooltip != "" {
				button.Tooltip(o.QueueTooltip)
			}
			if !o.QueueEnabled {
				button.Disabled(true)
			}
			if button.Clicked() && o.QueueEnabled {
				if o.QueueSend() {
					focusComposerAgain(o)
				}
			}
		}
		if o.Stop != nil {
			label := o.StopLabel
			if label == "" {
				label = T(o.Locale, "停止", "Stop")
			}
			stop := design.Apply(ui.Button(c, label), t.DangerButton()).
				Label(label).Shrink(0)
			if o.StopTooltip != "" {
				stop.Tooltip(o.StopTooltip)
			}
			stop.Children(func() {
				ui.Icon(c, icons.Stop()).Size(design.Space3, design.Space3).
					TextColor(t.DangerText)
			})
			if stop.Clicked() {
				o.Stop()
			}
		}
		return
	}
	label := o.SendLabel
	if label == "" {
		label = T(o.Locale, "发送", "Send")
	}
	// A disabled primary must not keep the accent fill: mygo's 50% accent wash
	// still read as a clickable brand-blue button. The neutral enabled-shaped
	// control carries the same size and the native disabled state.
	var send ui.Element
	if o.SendEnabled {
		send = design.Apply(ui.PrimaryButton(c, label), t.PrimaryButton())
	} else {
		send = design.Apply(ui.Button(c, label), t.DisabledPrimary())
	}
	send = send.Label(label).Shrink(0)
	if o.SendTooltip != "" {
		send.Tooltip(o.SendTooltip)
	}
	glyph := t.OnPrimary
	if !o.SendEnabled {
		send = send.Disabled(true)
		glyph = t.LabelTertiary
	}
	send.Children(func() {
		ui.Icon(c, icons.Send()).Size(design.Space4, design.Space4).TextColor(glyph)
	})
	if send.Clicked() && o.SendEnabled && o.Send != nil {
		if o.Send() {
			focusComposerAgain(o)
		}
	}
}
