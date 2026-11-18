package uikit

// The one composer shell (S7 in docs/AGENT-SURFACES.md): the conversation screen
// and Home are the same control in two places, so they share the card
// (t.Composer()), the width policy (the transcript reading column), the
// multiline input with its focus ring, the action row with the send/queue/stop
// control pinned at its right edge, the key hint and the error rows. The
// per-surface business — the attach/model/session menus, staged attachments, the
// queue, sending — comes in through slots, so neither surface can drift on the
// shell's own look.
//
// The card is the product's docking surface, not a floating dialog (VD-22/VD-26):
// a raised fill, one 1 DIP edge and the largest radius in the product
// (RadiusXL), flat on the page. Its edge is a ramp — BorderL2 at rest, BorderL3
// under the pointer, Accent under the keyboard focus with a 2 DIP ring drawn
// over the edge's own line so nothing inside shifts — and a file dragged over it
// turns the same edge into the drop target (A4-I-03).

import (
	"fmt"
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

// composerSendSize is the send control's square: the action row's own height, so
// the one primary action is the tallest control in the row (B-spec §3.2.7: a 32
// DIP, 8 radius button — a button, not the pill it used to be).
const composerSendSize float32 = design.RowCompactHeight

// ComposerShellOptions is one composer card. The shell owns the frame, the
// input, the send/queue/stop position and the key semantics; the caller owns the
// surfaces' slots and the draft/submit logic.
type ComposerShellOptions struct {
	// Key prefixes the composer's element keys, so two surfaces keep stable
	// identities.
	Key string
	// Draft is the focused text area's bound draft. Required.
	Draft *string
	// Placeholder is the empty input's visual hint, and the accessible label of a
	// surface that has only ever one composer.
	Placeholder string
	// InputLabel is the input's accessible name when the placeholder is not a
	// usable name: the placeholder changes with the surface's state (继续该队友…
	// / 继续该子代理…) and two composers can be on screen, but the name has to stay
	// unique and stable so assistive technology and automation never anchor on a
	// string that moves. Empty falls back to Placeholder.
	InputLabel string
	// Running is true while the turn streams: the send position becomes the
	// queue (when QueueSend is set) and stop controls.
	Running bool
	// Attachments builds the staged-attachment rows above the input (the
	// attachments a surface keeps are its business).
	Attachments func(c *ui.Context)
	// Notices builds the quiet copy between the input and the status area (a
	// missing API key, "Enter is unavailable while generating", …).
	Notices func(c *ui.Context)
	// QueueOpen is the queue panel's caller-owned open flag: the ×N chip in the
	// action row toggles it (see QueueCount/QueuePanel). nil leaves the panel
	// closed.
	QueueOpen *bool
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
	// Stopping marks a requested stop the backend has not settled yet (S8
	// stopping row, 2026-11 audit UX-06): the stop control reads 停止中… and is
	// disabled, and neither a click nor Esc fires Stop again while it is set.
	Stopping bool
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
	// OnFiles receives the files dropped on the card from the file manager
	// (A4-I-03). While files are dragged over the card its edge turns into the
	// accent drop target and DragHint shows; a surface that does not accept
	// dropped files leaves this nil and the window's own handler keeps them.
	OnFiles func(paths []string)
	// DragHint is the copy a surface shows while files are dragged over the card
	// ("松开以添加附件"); empty hides the line.
	DragHint string
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
	// SendBlocked reports that a pending decision (an approval) owns the turn:
	// the send control stays disabled, never draws the focus ring, and the
	// blocked reason explains it in a quiet note row. The keyboard belongs to
	// the decision then, so the send action is not a place to land on.
	SendBlocked bool
	// SendBlockedReason is that explanation, drawn in the action row's note (see
	// Notice) when the surface has no more specific note: one sentence, 12/18
	// tertiary, beside the control it explains.
	SendBlockedReason string
	// Attention is true while a pending decision owns the screen: the composer
	// stops competing for the eye — no accent focus ring, a neutral stop control
	// instead of the danger one — so the decision card keeps the screen's one
	// accent (B-spec §1.6 accent budget, F-02).
	Attention bool
	// QueueEnabled disables the queue control (queueing sends text only, so a
	// draft is required).
	QueueEnabled bool
	// SendLabel/SendDescription name the send control; the description is its
	// accessible description and the tooltip's copy. It never spells out a key
	// promise: the visible hint line owns the "Enter"/"Esc" wording.
	SendLabel       string
	SendDescription string
	// QueueCount is how many prompts already wait in the backend inbox. With a
	// count the action row shows one quiet ×N chip immediately left of the queue
	// control: the count belongs to the queue, not to the status bar's run state,
	// where "运行中 ×2" read as two turns running (p2-inbox audit).
	QueueCount int
	// QueuePanel draws the queue's popover from that chip: the composer calls it
	// with the chip element as the anchor, so the panel hangs from the control
	// that shows the count (right-aligned, opening upward) instead of from the
	// status bar on the other side of the row (p2-inbox-popover audit). nil leaves
	// the chip a plain count.
	QueuePanel func(c *ui.Context, anchor ui.Element)
	// QueueLabel/QueueDescription are the localized queue control copy.
	QueueLabel       string
	QueueDescription string
	// StopLabel/StopDescription are the localized stop copy.
	StopLabel       string
	StopDescription string
	// Error is one error row under the action row (the surface's error line).
	Error string
	// Errors are further error rows under the action row, in order.
	Errors []string
	// Notice is the quiet note immediately left of the send control: why the
	// send cannot run yet. It is a sentence at 12/18 in the tertiary ink, so it
	// reads as part of the action row rather than as a second error line, and it
	// is the only explanation a disabled send gets (A4-I-05: a disabled control
	// shows no tooltip). Empty draws nothing — an empty draft needs no sentence,
	// the placeholder already says what to do.
	Notice string
	// NoticeAction makes Notice clickable, for the note that has one obvious fix
	// ("先配置模型" opens the model settings). nil leaves it plain text.
	NoticeAction func()
	// FullWidth fills the surface's container up to the reading width instead of
	// sitting on the reading frame's text measure: Home's container already is
	// the readable column, so its composer spans it like the rest of Home.
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
		// One block per frame: the card and the usage strip under it, on the
		// shell's own 8 DIP rhythm. The reading frame is the transcript's when
		// the surface has one (uikit.ReadingColumn), so the card's edges line up
		// with the message column's text edges instead of a second, hand-rolled
		// 960 frame (VD-24); Home's container already is that column, so it
		// fills it (FullWidth).
		block := func(fc *ui.Context) {
			ui.Column(fc).FillWidth().Gap(design.Space2).Children(func() {
				ui.Row(fc.Key(key + ".row")).FillWidth().Justify(ui.Center).Children(func() {
					card = design.Apply(ui.Column(fc.Key(key+".card")), t.Composer()).
						Padding(th.Space(3)).Gap(design.Space2).
						Label(cardLabel)
					if o.FullWidth {
						card = card.FillWidth().MaxWidth(design.TranscriptWidth)
					} else {
						card = card.FillWidth()
					}
					if o.OnWidth != nil {
						o.OnWidth(card.Bounds().W)
					}
					card.Children(func() {
						if o.Attachments != nil {
							o.Attachments(c)
						}
						inputLabel := o.InputLabel
						if inputLabel == "" {
							inputLabel = o.Placeholder
						}
						input = ui.TextArea(c.Key(key+".input"), o.Draft).
							Label(inputLabel).
							Placeholder(o.Placeholder).
							Background(ui.Transparent).Border(0, ui.Transparent).
							Padding(noEdge, th.Space(1)).
							// The input's first row is 44 DIP: one Space8 action
							// row tall, so an empty composer still reads as a
							// place to write instead of a slim strip.
							MinHeight(design.Space8 + design.Space3).
							// A long draft must not push the action row (and the
							// transcript) out of the window: the input grows up to
							// about six Space8 rows and scrolls inside.
							MaxHeight(design.Space8 * 6).
							Grow(1).
							OnTextKey(composerShellKey(o))
						// The input is body copy (14/22): it is what the user
						// writes, not metadata, and it matches the reply's own
						// size so the conversation and the draft read as one text.
						input = design.Apply(input, t.Font(design.TypeBody))
						// An image paste is an attachment, not text: the shell sees
						// the empty paste and asks the surface to stage the
						// clipboard image (the surface owns attachments). A
						// non-empty paste keeps the native insert path.
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
						// A failed queue delivery is the one queue state the card
						// still owns: an error row with its retry. The queue itself
						// is the ×N chip in the action row, whose panel hangs from
						// that chip (p2-inbox-popover audit: the old badge here
						// anchored the panel on the wrong side of the composer).
						if o.QueueError != "" {
							RunStatus(c, RunStatusOptions{
								QueueError:   o.QueueError,
								OnRetryQueue: o.OnRetryQueue,
								Locale:       o.Locale,
							})
						}
						composerShellActionRow(c, t, o)
						if o.Error != "" {
							ErrorLine(c, o.Error)
						}
						for _, text := range o.Errors {
							if strings.TrimSpace(text) != "" {
								ErrorLine(c, text)
							}
						}
						// The send's own explanation is the action row's note (see
						// composerShellNote): one sentence, beside the control it
						// explains, never a tooltip on a disabled control (A4-I-05).
						// The drop target's own line: only while files are dragged
						// over the card, so the composer explains what the lit edge
						// means at the moment it lights up.
						if o.OnFiles != nil && card.FileDragOver() &&
							strings.TrimSpace(o.DragHint) != "" {
							ui.Row(c).FillWidth().AlignItems(ui.Center).
								Gap(design.Space2).Children(func() {
								ui.Icon(c, icons.File()).Size(design.IconSizeS, design.IconSizeS).
									TextColor(t.AccentText)
								design.Apply(ui.Text(c, o.DragHint), t.Font(design.TypeXXS)).
									TextColor(t.AccentText)
							})
						}
					})
					// The edge ramp: the quiet BorderL3 edge at rest, under the
					// pointer and with the caret in it; the accent edge only for a
					// keyboard-visible focus; the drop target's accent while files
					// hover. While a pending decision owns the screen the card keeps
					// the quiet edge, so the decision card is the screen's one
					// accent (F-02). One indicator at a time, and the transition is
					// the shared fast colour fade (VD-27).
					focused := input.Focused()
					// The accent edge is the keyboard's, not the pointer's: the
					// composer takes the focus by itself when a conversation
					// opens, so an accent edge on every focus put a standing
					// accent block on every screen and spent the budget without
					// saying anything (accent budget, B-spec §1.6). One indicator
					// only: the 1 DIP accent edge on a keyboard-visible focus, a
					// neutral edge otherwise.
					focusVisible := focused && input.FocusVisible()
					dragging := o.OnFiles != nil && card.FileDragOver()
					switch {
					case dragging:
						card = card.Border(1, t.Accent).Background(t.BgHover)
					case focusVisible && !o.Attention:
						card = card.Border(1, t.Accent)
					default:
						// Every other state — at rest, under the pointer, and with
						// the caret in it after a click — keeps the quiet edge. The
						// caret says where the keyboard is; the composer is focused
						// by default on entry, so a fill or an accent edge here would
						// be a standing decoration on every screen.
						card = card.Border(1, t.BorderL3)
					}
					card = card.Transition(design.TransitionColors(c))
					// Files dropped on the card are the surface's attachments;
					// the window's own handler only sees what no element takes
					// (mygo input.md, Dropped files).
					if o.OnFiles != nil {
						if paths := card.DroppedFiles(); len(paths) > 0 {
							o.OnFiles(paths)
						}
					}
				})
			})
			if o.Usage != nil {
				o.Usage(fc)
			}
		}
		if o.FullWidth {
			block(c)
			return
		}
		ReadingColumn(c, block)
	})
	return card, input
}

// composerShellActionRow draws the row under the input: the surface's toolbar,
// the quiet key hint and, at the fixed right edge, the send/queue/stop control.
// The row is one Space8 tall so the controls share one baseline.
func composerShellActionRow(c *ui.Context, t *design.Tokens, o ComposerShellOptions) {
	ui.Row(c).FillWidth().Height(design.RowCompactHeight).
		AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		if o.Toolbar != nil {
			o.Toolbar(c)
		}
		if o.Hint != nil {
			if hint := o.Hint(c); hint != "" {
				// The hint is a sentence (what Enter/Esc do), so it uses the
				// metadata step 12, not the 11 micro step.
				design.Apply(ui.Text(c, hint), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Description(hint)
			}
		}
		ui.Spacer(c).Grow(1)
		composerShellNote(c, t, o)
		composerShellActions(c, t, o)
	})
}

// composerShellNote draws the send's explanation immediately left of the send
// control: one 12/18 sentence in the tertiary ink, clickable when the surface
// has the one obvious fix (a model to configure, an approval to answer). It is
// the disabled control's only voice — mygo shows no tooltip on a disabled
// control — and it stays out of the way when the send can run.
func composerShellNote(c *ui.Context, t *design.Tokens, o ComposerShellOptions) {
	text := strings.TrimSpace(o.Notice)
	if text == "" && o.SendBlocked {
		text = strings.TrimSpace(o.SendBlockedReason)
	}
	if text == "" {
		return
	}
	if o.NoticeAction == nil {
		design.Apply(ui.Text(c, text), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Description(text)
		return
	}
	// An actionable note reads as a link, not as a second button: the accent's
	// text ink and an underline under the pointer, with no fill (DESIGN.md §5.2:
	// the accent's text role is for links). The whole note is the target.
	action := ui.Button(c, "").Label(text).Height(design.RowCompactHeight).
		PaddingX(design.Space1).Radius(design.RadiusSM).Shrink(0).
		Background(ui.Transparent)
	if action.Hovered() || action.Focused() {
		action = action.Background(t.BgHover)
	}
	action = action.Transition(design.TransitionColors(c))
	action.Children(func() {
		design.Apply(ui.Text(c, text), t.Font(design.TypeXXS)).
			TextColor(t.AccentText).MaxLines(1).Ellipsis("…")
	})
	if action.Clicked() {
		o.NoticeAction()
	}
}

// composerShellKey is the input's key hook: the surface's OnKey runs first (the
// suggestion menus, the slash/mention handling, the running-turn interrupt and
// the queueing decision), then the shell's defaults — Esc stops a running turn,
// Enter or Cmd/Ctrl+Enter sends (queues while running when QueueSend is set),
// and Shift/Alt/Enter reach the input as a newline.
func composerShellKey(o ComposerShellOptions) func(ui.TextKey) bool {
	return func(k ui.TextKey) bool {
		if o.OnKey != nil && o.OnKey(k) {
			return true
		}
		if k.Key == ui.KeyEscape && k.Mods == 0 && o.Running && o.Stop != nil && !o.Stopping {
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

// composerQueueChip draws the queue's size beside the run controls: ×N on a quiet
// text chip, which also opens the queue panel. The count lives next to the
// control that adds to the queue (排队) and, while idle, next to the send, so the
// status bar can keep reporting only the run state — "运行中 ×2" read as two turns
// running (p2-inbox audit). Nothing is drawn at zero: an empty badge is a box that
// says nothing (p2-inbox audit).
func composerQueueChip(c *ui.Context, t *design.Tokens, o ComposerShellOptions) {
	if o.QueueCount <= 0 {
		return
	}
	chip := ui.ButtonBase(c.Key("composer.queue.count")).
		Height(design.ControlCompactHeight).PaddingX(design.Space1).
		Radius(design.RadiusSM).Shrink(0).
		Label(fmt.Sprintf(T(o.Locale, "队列中的消息（%d 条）", "Queued messages (%d)"), o.QueueCount)).
		Description(T(o.Locale, "查看或取消队列中的消息", "View or cancel queued messages"))
	switch {
	case chip.Pressed():
		chip = chip.Background(t.BgSelected)
	case chip.Hovered() || chip.Focused():
		chip = chip.Background(t.BgHover)
	}
	chip = chip.Transition(design.TransitionColors(c))
	chip.Children(func() {
		design.Apply(ui.Text(c, fmt.Sprintf("×%d", o.QueueCount)), t.Font(design.TypeXXS)).
			FontWeight(design.WeightMedium).TextColor(t.LabelSecondary).NoWrap()
	})
	if chip.Clicked() && o.QueueOpen != nil {
		*o.QueueOpen = !*o.QueueOpen
		c.Invalidate()
	}
	if o.QueuePanel != nil {
		o.QueuePanel(c, chip)
	}
}

// composerShellActions draws the action row's right edge: the send control when
// idle, and the queue plus stop controls while a turn runs. They are plain row
// siblings rather than toolbar items, so no overflow menu can ever take the
// primary action.
func composerShellActions(c *ui.Context, t *design.Tokens, o ComposerShellOptions) {
	composerQueueChip(c, t, o)
	if o.Running {
		if o.QueueSend != nil {
			label := o.QueueLabel
			if label == "" {
				label = T(o.Locale, "排队", "Queue")
			}
			button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
				Label(label).Height(design.ControlCompactHeight).Shrink(0)
			if o.QueueDescription != "" {
				button = button.Description(o.QueueDescription).Tooltip(o.QueueDescription)
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
			if o.Stopping {
				// The settle is the backend's: say the stop is in flight instead
				// of claiming it finished, and take the control out of reach so
				// a second click cannot re-trigger it (S8 stopping, UX-06).
				label = T(o.Locale, "停止中…", "Stopping…")
			}
			// The stop control is the one outlined danger action of the composer
			// (B-spec §3.2.7): the danger ink and its edge say what the click
			// costs, while the fill stays the neutral control's, so the native
			// hover/pressed states keep working. While a pending decision owns
			// the screen the stop steps back to the quiet control: the decision
			// card's own accent (or danger) must be the screen's only strong
			// colour (accent budget, F-02).
			var stop ui.Element
			if o.Attention {
				stop = design.Apply(ui.Button(c, label), t.SecondaryButton())
			} else {
				stop = design.Apply(ui.Button(c, label), t.DangerButton()).
					Border(1, t.Danger)
			}
			stop = stop.Label(label).Height(design.ControlCompactHeight).Shrink(0)
			if o.StopDescription != "" {
				stop = stop.Description(o.StopDescription).Tooltip(o.StopDescription)
			}
			if o.Stopping {
				stop.Disabled(true)
			}
			stop.Children(func() {
				ink := t.DangerText
				if o.Attention {
					ink = t.LabelSecondary
				}
				ui.Icon(c, icons.Stop()).Size(design.IconSizeS, design.IconSizeS).
					TextColor(ink)
			})
			if stop.Clicked() && !o.Stopping {
				o.Stop()
			}
		}
		return
	}
	label := o.SendLabel
	if label == "" {
		label = T(o.Locale, "发送", "Send")
	}
	// The send control is the row's one primary action and its tallest control:
	// a 32 DIP square with an 8 DIP radius and the 16 DIP glyph (B-spec §3.2.7),
	// not the pill it used to be. A send that cannot run yet keeps a visible
	// neutral control — BgStrong fill and tertiary ink, ≥3:1 — because the
	// native disabled look faded into the page (VD-20); a blocked send (a
	// pending decision owns the turn) neither lands in the Tab order nor draws
	// the focus ring.
	canSend := o.SendEnabled && !o.SendBlocked
	var send ui.Element
	if canSend {
		send = design.Apply(ui.PrimaryButton(c, ""), t.PrimaryButton())
	} else {
		send = design.Apply(ui.Button(c, ""), t.DisabledPrimary())
	}
	send = send.Label(label).Shrink(0).
		Width(composerSendSize).Height(composerSendSize).
		PaddingX(design.Space0).PaddingY(design.Space0).Radius(design.RadiusMD)
	if o.SendBlocked {
		send = send.FocusRing(false)
	}
	if o.SendDescription != "" {
		send = send.Description(o.SendDescription).Tooltip(o.SendDescription)
	}
	glyph := t.OnPrimary
	if !canSend {
		// A disabled control keeps the look it was built with: mygo dims a
		// disabled subtree to half opacity by default, which turned the neutral
		// fill and its tertiary ink into a 1.15:1 ghost of a button (QA on
		// p1-chat). The control is styled for its disabled state already (BgStrong
		// fill, tertiary ink, one hairline edge), so it shows at full strength.
		send = send.Disabled(true).DimDisabled(false).Border(1, t.BorderL2)
		glyph = t.LabelTertiary
	}
	send.Children(func() {
		ui.Icon(c, icons.Send()).Size(design.IconSizeS, design.IconSizeS).TextColor(glyph)
	})
	if send.Clicked() && canSend && o.Send != nil {
		if o.Send() {
			focusComposerAgain(o)
		}
	}
}
