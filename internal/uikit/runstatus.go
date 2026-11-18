package uikit

// The product's one run-status vocabulary (S8 in docs/AGENT-SURFACES.md): the
// generating line, the queue badge and its popover, the labeled retry of a
// failure, the official stop control, the composer-adjacent status bar with its
// usage gauge, and the collapsible usage accounting. It is deliberately a set of
// status rows rather than a progress widget: the product has no truthful
// percentage for a run, so it never draws one.
//
// Running is not a warning (DESIGN.md §3.7): the spinner carries the accent and
// the status word stays LabelSecondary, while Warning is reserved for the states
// that need the user.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// statusGaugeHeight is the context gauge's bar: a 3 DIP rule under the numbers,
// light enough to read as a scale rather than as a progress widget (B-spec
// §3.2.6). The product's spacing grid has no 3 DIP step because it is a stroke,
// not a box; naming it here keeps the metric out of call sites.
const statusGaugeHeight float32 = 3

// statusGaugeWidth is the gauge bar's length, so the numbers above it do not
// move while the figure grows from 1K to 128K.
const statusGaugeWidth float32 = design.Space8 + design.Space6

// UsageRow is one expanded usage line: its localized label, the compact value and
// the exact count for the value's tooltip.
type UsageRow struct {
	Label string
	Value string
	Exact string
}

// Usage is the collapsible usage accounting of one turn or session: the summary
// line, the expanded rows and the tooltip's exact figures. A Usage without Rows
// is a single quiet summary line (a host with its own status strip); with Rows it
// is the official disclosure of the message footer.
type Usage struct {
	Summary string
	Exact   string
	Rows    []UsageRow
	// Open is the caller-persisted fold state of the disclosure; nil keeps a
	// local closed state (a host without fold memory).
	Open *bool
}

// StatusStripOptions is the composer-adjacent status bar (S8, B-spec §3.2.6):
// one 32 DIP row in three zones — the left says what the run is doing (spinner,
// status word, the queue with its count), the middle stays empty, and the right
// holds the context gauge over the model's window with its details popover.
// The bar is information, never a decision: errors keep their own row.
type StatusStripOptions struct {
	// Running shows the accent spinner and the status word.
	Running bool
	// StatusLabel is the running copy (运行中); empty uses the product's own.
	StatusLabel string
	// Queued lists the prompts waiting for this turn: the left zone shows the
	// queue control with its count and its popover (per-row cancel, paused
	// explanation and resume, clear with its confirmation).
	Queued []string
	// QueueOpen is the queue popover's caller-owned open flag.
	QueueOpen *bool
	// QueueList is the caller's List state for the popover's rows.
	QueueList *ui.ListState
	// QueuePaused says a stop paused the queue.
	QueuePaused bool
	// QueueError is the last queued-delivery failure (its own error row).
	QueueError string
	// OnQueueCancel/OnQueueClear/OnQueueResume/OnRetryQueue are the queue actions.
	OnQueueCancel func(index int)
	OnQueueClear  func()
	OnQueueResume func()
	OnRetryQueue  func()
	// Used and Window are the newest request's context size and the model's
	// window; Used 0 or Window 0 hides the gauge (a ratio the product cannot
	// compute is not shown at zero).
	Used   int
	Window int
	// Detail are the popover's rows: rounds, tool calls, cache hit and the
	// remaining context of this session.
	Detail []UsageRow
	// DetailOpen is the details popover's caller-owned open flag; nil leaves it
	// closed (a host without the extra figures).
	DetailOpen *bool
	// KeyPrefix keys the strip and its rows (the session ID).
	KeyPrefix string
	// Locale localizes the entry's own copy; nil falls back to Chinese.
	Locale contracts.LocaleService
}

// StatusStrip draws the composer-adjacent status bar. It draws nothing at all
// when it has nothing truthful to say (no run, no queue, no token accounting),
// so an idle conversation keeps one quiet composer instead of a zeroed row.
func StatusStrip(c *ui.Context, o StatusStripOptions) {
	t := design.Of(c)
	hasQueue := len(o.Queued) > 0 || o.QueueError != ""
	hasGauge := o.Used > 0 && o.Window > 0
	if !o.Running && !hasQueue && !hasGauge && len(o.Detail) == 0 {
		return
	}
	key := "runstatus.strip"
	if prefix := strings.TrimSpace(o.KeyPrefix); prefix != "" {
		key += "/" + prefix
	}
	// The bar is 32 DIP, the compact row step: it reads as chrome under the
	// composer, not as a second panel. Nothing in it is 11 DIP copy (VD-10): the
	// status word, the queue count and the gauge's numbers are all 12.
	row := ui.Row(c.Key(key)).FillWidth().Height(design.RowCompactHeight).
		AlignItems(ui.Center).Gap(design.Space2)
	row.Children(func() {
		statusStripRun(c, t, o, key)
		ui.Spacer(c).Grow(1)
		statusStripGauge(c, t, o, key)
	})
	if o.QueueError != "" {
		ErrorLine(c, o.QueueError)
	}
}

// statusStripRun draws the left zone: what the run is doing, and the queue (with
// its count) while prompts wait. Both are quiet; the accent lives in the
// spinner alone, so the bar never competes with the send control.
func statusStripRun(c *ui.Context, t *design.Tokens, o StatusStripOptions, key string) {
	if o.Running {
		label := o.StatusLabel
		if strings.TrimSpace(label) == "" {
			label = T(o.Locale, "运行中", "Running")
		}
		// One status line with a stable identity: v0.9 reads a RoleStatus whole
		// again whenever its text changes (docs/ui/accessibility.md).
		ui.Row(c.Key(key + ".run")).AlignItems(ui.Center).Gap(design.Space2).
			Role(ui.RoleStatus).Label(label).Children(func() {
			ui.Spinner(c).Label(label).Size(design.IconSizeS, design.IconSizeS).
				TextColor(t.Accent)
			StatusText(c, t, design.TypeXXS, StatusRunning, label)
		})
	}
	runStatusQueue(c, t, RunStatusOptions{
		Running:       o.Running,
		Queued:        o.Queued,
		QueueOpen:     o.QueueOpen,
		QueueList:     o.QueueList,
		QueuePaused:   o.QueuePaused,
		QueueError:    o.QueueError,
		OnQueueCancel: o.OnQueueCancel,
		OnQueueClear:  o.OnQueueClear,
		OnQueueResume: o.OnQueueResume,
		OnRetryQueue:  o.OnRetryQueue,
		KeyPrefix:     o.KeyPrefix,
		Locale:        o.Locale,
	})
}

// statusStripGauge draws the right zone: the newest request's context over the
// model's window in mono 12, a 3 DIP bar under it in the same green/amber/red
// vocabulary the usage audit asked for (Warning past 80%, Danger past 95%), and
// the 详情 control that opens the exact figures (rounds, tool calls, cache hit,
// remaining context). The numbers are the product's only always-on metric, so
// they are the one place mono is used for a figure rather than for code.
func statusStripGauge(c *ui.Context, t *design.Tokens, o StatusStripOptions, key string) {
	if o.Used <= 0 || o.Window <= 0 {
		// A session with accounting but no context window still has figures
		// worth opening (rounds, tool calls, cache hit).
		statusStripDetailButton(c, t, o, key)
		return
	}
	ratio := float64(o.Used) / float64(o.Window)
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0 {
		ratio = 0
	}
	figures := FormatCompactCount(o.Used) + "/" + FormatCompactCount(o.Window)
	ui.Row(c.Key(key + ".gauge")).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, figures), t.MonoText(design.TypeXXS)).
			TextColor(t.LabelSecondary).NoWrap().
			Description(fmt.Sprintf(T(o.Locale, "上下文 %d / %d tokens", "Context %d / %d tokens"),
				o.Used, o.Window))
		fill := t.Accent
		switch {
		case ratio >= 0.95:
			fill = t.Danger
		case ratio >= 0.80:
			fill = t.Warning
		}
		// The official progress bar (a real, truthful ratio — the one meter the
		// product has) at the gauge's own 3 DIP rule, over the surface's line.
		ui.Progress(c, ratio).Width(statusGaugeWidth).Height(statusGaugeHeight).
			TextColor(fill).Background(t.BorderL2).
			Label(fmt.Sprintf(T(o.Locale, "上下文用量 %d%%", "Context used %d%%"),
				int(ratio*100)))
		statusStripDetailButton(c, t, o, key)
	})
}

// statusStripDetailButton draws the 详情 control and its popover: the exact
// session figures, one per row.
func statusStripDetailButton(c *ui.Context, t *design.Tokens, o StatusStripOptions, key string) {
	if len(o.Detail) == 0 || o.DetailOpen == nil {
		return
	}
	label := T(o.Locale, "详情", "Details")
	button := design.Apply(ui.Button(c.Key(key+".detail"), label), t.SecondaryButton()).
		Label(label).Height(design.ControlCompactHeight).PaddingX(design.Space2).
		FontWeight(design.WeightMedium)
	button = button.Transition(design.TransitionColors(c))
	if button.Clicked() {
		*o.DetailOpen = !*o.DetailOpen
		c.Invalidate()
	}
	if !*o.DetailOpen {
		return
	}
	open := true
	ui.PopoverBase(c, button, &open, func(panel ui.Element) {
		design.Apply(panel.Width(design.OverlayWidthS).MaxWidthPercent(92).
			Padding(design.Space3).Gap(design.Space2).
			AttachTo(button, ui.AnchorTopEnd, ui.AnchorBottomEnd).
			Margin(0, 0, design.Space2, 0).
			Background(t.BgOverlay).Border(1, t.BorderL3).Radius(design.RadiusLG),
			t.ShadowOverlay()).
			Label(T(o.Locale, "用量详情", "Usage details"))
		for _, row := range o.Detail {
			row := row
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				design.Apply(ui.Text(c, row.Label), t.TextMuted(design.TypeXXS)).
					Grow(1).Shrink(1).MaxLines(1).Ellipsis("…")
				value := design.Apply(ui.Text(c, row.Value), t.MonoText(design.TypeXXS)).
					TextColor(t.LabelPrimary).NoWrap()
				if row.Exact != "" {
					value.Description(T(o.Locale, "精确值：", "Exact: ") + row.Exact)
				}
			})
		}
	})
	if !open {
		*o.DetailOpen = false
	}
}

// RunStatusOptions is one run-status area: a generating line, the queue of
// prompts waiting for the turn, the queue delivery error, a failed turn's retry,
// the official stop control and the usage accounting.
type RunStatusOptions struct {
	// Running shows the official spinner and the localized status copy.
	Running bool
	// StatusLabel is the running copy (生成中, Refreshing…); empty uses the
	// product's 生成中/Generating.
	StatusLabel string
	// Queued lists the prompts waiting for this turn; non-empty shows the queue
	// badge and, while QueueOpen, its popover.
	Queued []string
	// QueueOpen is the queue popover's caller-owned open flag; nil leaves the
	// popover closed.
	QueueOpen *bool
	// QueueList is the caller's List state for the popover rows.
	QueueList *ui.ListState
	// QueuePaused says the stop paused the queue: the popover explains it and
	// offers the explicit resume action.
	QueuePaused bool
	// QueueError is the last queued-delivery failure; it shows an error row and,
	// while no turn runs, a retry action through OnRetryQueue.
	QueueError string
	// OnQueueCancel removes one queued prompt; nil hides the per-row action.
	OnQueueCancel func(index int)
	// OnQueueClear clears the whole queue; nil hides the action. It runs only
	// after the entry's ConfirmDanger is explicitly confirmed (Cancel is the
	// Enter default), never on the popover button's own click.
	OnQueueClear func()
	// OnQueueResume sends the first queued prompt after a pause; nil hides it.
	OnQueueResume func()
	// OnRetryQueue retries a failed queued delivery.
	OnRetryQueue func()
	// Usage is the collapsible usage accounting; nil hides it.
	Usage *Usage
	// Error is a failure line (the turn failed, a retry failed, a delivery
	// failed); empty shows none.
	Error string
	// Retryable adds the labeled retry action beside the error (or alone).
	Retryable bool
	// OnStop draws the official stop control while Running; nil hides it (the
	// composer keeps its own stop in the send position).
	OnStop func()
	// OnRetry runs the retry action.
	OnRetry func()
	// OnDismiss dismisses the error (a labeled quiet button); nil hides it.
	OnDismiss func()
	// OnToggle receives the usage disclosure's new fold state.
	OnToggle func(open bool)
	// HoldPlace is the transcript's one-shot fold flag; a usage toggle sets it.
	HoldPlace *bool
	// Locale localizes the entry's own copy; nil falls back to Chinese.
	Locale contracts.LocaleService
	// KeyPrefix keys the usage disclosure and the queue rows (the message ID or
	// the session ID).
	KeyPrefix string
}

// RunStatus draws the run-status area for the current state: every row is built
// only when it has something truthful to say, so an idle composer shows nothing.
func RunStatus(c *ui.Context, o RunStatusOptions) {
	t := design.Of(c)
	if o.Running {
		label := o.StatusLabel
		if strings.TrimSpace(label) == "" {
			label = T(o.Locale, "生成中", "Generating")
		}
		// The status row has a stable identity: v0.9 reads a RoleStatus whole
		// again whenever its text changes (aria-atomic, docs/ui/accessibility.md),
		// and screen readers tell the new status from the old by the element.
		// Without a Key an element built earlier in the same area (a spinner or
		// a queue row appearing before it, say) would shift the status to
		// another node, which assistive technology reads as a new status. The
		// prefix keeps several run-status areas in one scope distinct (a message
		// footer's area per message).
		statusKey := "runstatus"
		if prefix := strings.TrimSpace(o.KeyPrefix); prefix != "" {
			statusKey = "runstatus/" + prefix
		}
		ui.Row(c.Key(statusKey)).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
			Role(ui.RoleStatus).Label(label).Children(func() {
			ui.Spinner(c).Label(label).Size(design.IconSizeS, design.IconSizeS).
				TextColor(t.Accent)
			StatusText(c, t, design.TypeXXS, StatusRunning, label)
		})
	}
	runStatusQueue(c, t, o)
	if o.Error != "" {
		ErrorLine(c, o.Error)
	}
	if o.Error != "" || o.Retryable || (o.Running && o.OnStop != nil) {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if o.Retryable && o.OnRetry != nil {
				label := T(o.Locale, "重试", "Retry")
				button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
					Label(label).
					Description(T(o.Locale, "重新运行上一次请求", "Run the last request again"))
				button.Children(func() {
					ui.Icon(c, icons.Reload()).Size(design.IconSizeS, design.IconSizeS).
						TextColor(t.LabelSecondary)
				})
				if button.Clicked() {
					o.OnRetry()
				}
			}
			if o.Error != "" && o.OnDismiss != nil {
				label := T(o.Locale, "关闭", "Dismiss")
				if button := design.Apply(ui.Button(c, label), t.SecondaryButton()); button.Clicked() {
					o.OnDismiss()
				}
			}
			ui.Spacer(c).Grow(1)
			if o.Running && o.OnStop != nil {
				runStatusStop(c, t, o)
			}
		})
	}
	if o.Usage != nil {
		runStatusUsage(c, t, o)
	}
}

// runStatusQueue draws the queue badge and its popover: the count is a button
// (the queue must be inspectable), each row can be cancelled, and after a stop
// paused the queue the popover explains it and offers the explicit resume and
// clear actions. Clearing the whole queue is irreversible, so it asks through
// uikit.ConfirmDanger first (DESIGN.md rule 9, AP-24).
func runStatusQueue(c *ui.Context, t *design.Tokens, o RunStatusOptions) {
	queued := o.Queued
	if len(queued) == 0 && o.QueueError == "" {
		return
	}
	// The queue area keeps a stable identity: the clear confirmation's open
	// flag is element-local state on this row, so it survives the frames the
	// popover rebuilds and never leaks between run-status areas.
	rowKey := "runstatus.queue"
	if prefix := strings.TrimSpace(o.KeyPrefix); prefix != "" {
		rowKey += "/" + prefix
	}
	var anchor ui.Element
	row := ui.Row(c.Key(rowKey)).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		if len(queued) > 0 && o.QueueOpen != nil {
			count := len(queued)
			label := fmt.Sprintf("×%d", count)
			name := fmt.Sprintf(T(o.Locale, "已排队 %d 条", "%d queued"), count)
			desc := T(o.Locale, "查看或取消队列中的消息", "View or cancel queued messages")
			if o.QueuePaused {
				desc = T(o.Locale, "已停止：队列中的消息不会自动发送，展开后可手动发送或取消",
					"Stopped: queued messages are not sent automatically; open to send or cancel them")
			}
			button := runStatusQueueBadge(c, t, rowKey, label, name, desc, *o.QueueOpen)
			if button.Clicked() {
				*o.QueueOpen = !*o.QueueOpen
				c.Invalidate()
			}
			anchor = button
		}
		if o.QueueError != "" && !o.Running && o.OnRetryQueue != nil {
			label := T(o.Locale, "重试", "Retry")
			retry := design.Apply(ui.Button(c, label), t.SecondaryButton()).
				Label(label).Height(design.ControlCompactHeight).Shrink(0).
				Description(T(o.Locale, "重新投递队列中的消息", "Retry the queued delivery"))
			if retry.Clicked() {
				o.OnRetryQueue()
			}
		}
	})
	if o.QueueError != "" {
		// A failed delivery is an error, not a queue row: it says what happened
		// under the count, with the retry beside it while no turn runs.
		ErrorLine(c, o.QueueError)
	}
	// The clear confirmation's open flag is element-local state on the queue
	// row, so it survives the frames the popover rebuilds and never leaks
	// between run-status areas.
	var confirmOpen *bool
	if o.OnQueueClear != nil {
		confirmOpen = ui.Local(row, "clearConfirm", func() bool { return false })
	}
	if anchor.Valid() && o.QueueOpen != nil && *o.QueueOpen && len(queued) > 0 {
		QueuePanel(c, t, QueuePanelOptions{
			Queued:      queued,
			Running:     o.Running,
			QueuePaused: o.QueuePaused,
			Open:        o.QueueOpen,
			List:        o.QueueList,
			Locale:      o.Locale,
			KeyPrefix:   rowKey,
			OnCancel:    o.OnQueueCancel,
			OnClear:     o.OnQueueClear,
			OnResume:    o.OnQueueResume,
			ConfirmOpen: confirmOpen,
		}, anchor)
	}
	// The confirmation is built after the popover so it sits on top: overlays
	// stack in build order, and Escape and the pointer go to the one built
	// last (docs/ui/overlays.md). While it is open the modal owns the focus
	// (safe cancel, never a clear).
	if confirmOpen != nil {
		ConfirmDanger(c, confirmOpen, ConfirmDangerOptions{
			Key:     rowKey + ".clear",
			Title:   T(o.Locale, "清空全部排队消息？", "Clear all queued messages?"),
			Message: T(o.Locale, "此操作无法撤销。", "This cannot be undone."),
			Confirm: T(o.Locale, "确认清空", "Clear all"),
			Cancel:  T(o.Locale, "取消", "Cancel"),
			OnConfirm: func() {
				o.OnQueueClear()
			},
		})
	}
}

// QueuePanelOptions is the queue panel every surface draws: the same list, the
// same per-row remove, the same guarded clear. It hangs from the control the user
// pressed — the queue count beside the composer's run controls — and opens
// upward, right-aligned to that control's end edge so it never drifts over the
// draft on the other side of the row (p2-inbox-popover audit).
type QueuePanelOptions struct {
	Queued      []string
	Running     bool
	QueuePaused bool
	Open        *bool
	List        *ui.ListState
	Locale      contracts.LocaleService
	KeyPrefix   string
	OnCancel    func(index int)
	OnClear     func()
	OnResume    func()
	// ConfirmOpen is the caller's element-local clear-confirmation state (nil
	// draws no confirmation). The anchor element is a parameter of QueuePanel, not
	// a field: a ui.Element may not be kept in a struct (AGENTS.md §7).
	ConfirmOpen *bool
}

// QueuePanel draws the open queue's panel beside its anchor control: a titled
// list of the waiting prompts with their remove actions, and the resume/clear
// footer.
func QueuePanel(c *ui.Context, t *design.Tokens, o QueuePanelOptions, anchor ui.Element) {
	queued := o.Queued
	if len(queued) == 0 || !anchor.Valid() {
		return
	}
	confirmOpen := o.ConfirmOpen
	rowKey := strings.TrimSpace(o.KeyPrefix)
	if rowKey == "" {
		rowKey = "queue"
	}
	panelWidth := anchor.Bounds().W
	if panelWidth < design.DialogWidthXS {
		// The anchor is a small count chip: give the prompt rows room to be
		// readable instead of truncating to the chip's width.
		panelWidth = design.DialogWidthXS
	}
	panelOpen := o.Open == nil || *o.Open
	if !panelOpen {
		// The panel may be closed while its clear confirmation is still up: the
		// confirmation outlives the panel (below), so this call still runs.
		confirmOpen := o.ConfirmOpen
		if confirmOpen != nil && *confirmOpen {
			queueClearConfirm(c, o, rowKey, confirmOpen)
		}
		return
	}
	open := true
	ui.PopoverBase(c, anchor, &open, func(panel ui.Element) {
		design.Apply(panel.Width(panelWidth).MaxWidthPercent(92).Padding(design.Space2).Gap(design.Space1).
			AttachTo(anchor, ui.AnchorTopEnd, ui.AnchorBottomEnd).
			Margin(0, design.Space2, 0, 0).
			Background(t.BgOverlay).Border(1, t.BorderL3).Radius(design.RadiusLG),
			t.ShadowOverlay()).
			Label(T(o.Locale, "队列中的消息", "Queued messages"))
		// The panel's title is content, not chrome: a list of pending prompts with
		// no heading read as two stray lines (p2-inbox-popover audit).
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, T(o.Locale, "队列中的消息", "Queued messages")),
				t.Font(design.TypeXS)).FontWeight(design.WeightMedium).
				TextColor(t.LabelSecondary).Grow(1)
			design.Apply(ui.Text(c, fmt.Sprintf(T(o.Locale, "%d 条", "%d"), len(queued))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
		})
		if o.QueuePaused && !o.Running {
			design.Apply(ui.Text(c, T(o.Locale,
				"已停止：队列中的消息不会自动发送，可手动发送或取消",
				"Stopped: queued messages are not sent automatically; send or cancel them here")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		}
		list := o.List
		if list == nil {
			list = &ui.ListState{}
		}
		list.Key = func(row int) any { return row }
		list.Label = func(row int) string {
			if row < 0 || row >= len(queued) {
				return ""
			}
			return queued[row]
		}
		ui.List(c, list, len(queued)).
			Label(T(o.Locale, "队列中的消息", "Queued messages")).
			MaxHeight(design.Space6 * 8).
			Gap(design.Space1).
			FillWidth().
			Rows(func(row ui.ListRow) {
				index := row.Index
				rowC := row.Context
				ui.Row(rowC).FillWidth().
					AlignItems(ui.Center).Gap(design.Space2).PaddingY(design.Space1).
					Children(func() {
						full := strings.TrimSpace(queued[index])
						// The row truncates at paint time (MaxLines+Ellipsis), so
						// the full prompt stays in the accessible name and the
						// description instead of being cut in the data layer.
						text := design.Apply(ui.Text(rowC, full), t.Font(design.TypeXS)).
							TextColor(t.LabelPrimary).Grow(1).Shrink(1).MaxLines(1).
							Ellipsis("…")
						text.Description(full)
						if o.OnCancel != nil {
							// The row's remove action is the product's one icon
							// button (DESIGN.md rule 8): the entry guarantees the
							// ≥24 DIP hit target, the Label and the hover/pressed/
							// focus states the hand-written 20×20 button bypassed
							// (2026-11 audit UX-08). Its name says which queued
							// message it removes; the prompt itself is the row's own
							// text right beside it, so no tooltip is needed.
							IconButton(rowC, IconButtonOptions{
								Key: fmt.Sprintf("%s.remove.%d", rowKey, index),
								Label: fmt.Sprintf(T(o.Locale,
									"取消第 %d 条排队消息", "Cancel queued message %d"), index+1),
								Icon:    icons.Close(),
								Size:    design.IconSizeS,
								OnClick: func() { o.OnCancel(index) },
							})
						}
					})
			})
		if o.OnResume != nil || o.OnClear != nil {
			ui.Divider(c)
			ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).
				Gap(design.Space2).Children(func() {
				if o.QueuePaused && !o.Running && o.OnResume != nil {
					if send := design.Apply(ui.Button(c, T(o.Locale, "发送队列中的消息", "Send queued")),
						t.SecondaryButton()); send.Clicked() {
						o.OnResume()
					}
				}
				if o.OnClear != nil && confirmOpen != nil {
					if clear := design.Apply(ui.Button(c, T(o.Locale, "清空全部", "Clear all")),
						t.SecondaryButton()); clear.Clicked() {
						// Irreversible: ask first. ConfirmDanger keeps Cancel the
						// Enter default; the queue survives until the explicit
						// danger click (DESIGN.md rule 9 / AP-24, 2026-11 audit UX-04).
						// The popover yields to the modal: an anchored popover
						// closes on any press outside itself, which would swallow
						// the confirmation's own click mid-gesture. The count chip
						// keeps the queue visible behind the scrim.
						*confirmOpen = true
						if o.Open != nil {
							*o.Open = false
						}
						c.Invalidate()
					}
				}
			})
		}
	})
	if !open && o.Open != nil {
		*o.Open = false
	}
	// The clear confirmation is built after the panel so it sits on top: overlays
	// stack in build order, and Escape and the pointer go to the one built last
	// (docs/ui/overlays.md). While it is open the modal owns the focus (safe
	// cancel, never a clear).
	if confirmOpen != nil && *confirmOpen {
		queueClearConfirm(c, o, rowKey, confirmOpen)
	}
}

// queueClearConfirm is the queue's irreversible clear, asked for the way
// DESIGN.md rule 9 requires: a native ConfirmDanger whose Cancel is the Enter
// default and whose danger action is the explicit click.
func queueClearConfirm(c *ui.Context, o QueuePanelOptions, rowKey string, confirmOpen *bool) {
	ConfirmDanger(c, confirmOpen, ConfirmDangerOptions{
		Key:     rowKey + ".clear",
		Title:   T(o.Locale, "清空全部排队消息？", "Clear all queued messages?"),
		Message: T(o.Locale, "此操作无法撤销。", "This cannot be undone."),
		Confirm: T(o.Locale, "确认清空", "Clear all"),
		Cancel:  T(o.Locale, "取消", "Cancel"),
		OnConfirm: func() {
			if o.OnClear != nil {
				o.OnClear()
			}
		},
	})
}

// runStatusQueueBadge draws the clickable queue count on the compact chip step
// (28 DIP, DESIGN.md §5.4): the count on its own ("×3") and the sentence as its
// accessible name, so the bar stays a bar.
//
// design:allowed AP-04 owner=uikit until=2027-06-30
// Why not the official widget: ui.Badge is informational and carries no click
// or keyboard semantics; a ui.Button with a background override loses the
// native hover/pressed/disabled state machine (mygo styleButton only tints a
// button whose background is the theme's base — design/components.go,
// DESIGN.md §1.3.1 / AP-02). ButtonBase keeps the official interaction while
// the badge look is drawn here with every state (2026-11 audit UD-06).
// What official semantics are preserved: ButtonBase owns the pointer, Enter
// and Space, the Tab focus, the theme's focus ring and Disabled; the Name and
// Description below name and describe the count for assistive technology. The
// fill and ink cover rest, hover, pressed, focused and selected (the open
// popover), so the clickable count never looks dead (AP-11).
func runStatusQueueBadge(c *ui.Context, t *design.Tokens, rowKey, label, name, description string, open bool) ui.Element {
	button := ui.ButtonBase(c.Key(rowKey + ".badge")).Label(name).
		Height(design.ControlCompactHeight).PaddingX(design.Space2).Radius(design.RadiusSM).
		AlignItems(ui.Center)
	if description != "" {
		button = button.Description(description)
	}
	// The badge keeps a quiet fill at rest (the count must read without being
	// hovered); hover, keyboard focus and the open popover deepen it to the
	// selection tint, and a press darkens the count ink to the accent's pressed
	// step — every state is visible, and each changes color over the shared
	// fast transition rather than snapping.
	ink := t.LabelSecondary
	switch {
	case button.Pressed():
		button = button.Background(t.BgSelected)
		ink = t.AccentPressed
	case open || button.Hovered() || button.Focused():
		button = button.Background(t.BgSelected)
		ink = t.AccentText
	default:
		button = button.Background(t.BgHover)
	}
	button = button.Transition(design.TransitionColors(c))
	button.Children(func() {
		design.Apply(ui.Text(c, label), t.MonoText(design.TypeXXS)).TextColor(ink).NoWrap()
	})
	return button
}

// runStatusStop draws the official stop control: a danger-styled native button,
// never next to the send action with the same weight (the composer keeps them in
// the same send position instead).
func runStatusStop(c *ui.Context, t *design.Tokens, o RunStatusOptions) {
	label := T(o.Locale, "停止", "Stop")
	button := design.Apply(ui.Button(c, label), t.DangerButton()).
		Label(label).Shrink(0)
	button.Children(func() {
		ui.Icon(c, icons.Stop()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.DangerText)
	})
	if button.Clicked() {
		o.OnStop()
	}
}

// runStatusUsage draws the usage accounting: a single quiet summary line when
// there are no rows (a host with its own status strip), the official disclosure
// with the label/value rows otherwise (a message's footer).
func runStatusUsage(c *ui.Context, t *design.Tokens, o RunStatusOptions) {
	usage := o.Usage
	if len(usage.Rows) == 0 {
		// Metadata is 12 DIP at the smallest (VD-10/VD-11): 11 is kept for kbd and
		// gutters only, so a summary never renders as a whisper.
		design.Apply(ui.Text(c, usage.Summary), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Description(usage.Exact)
		return
	}
	open := usage.Open
	if open == nil {
		open = new(bool)
	}
	before := *open
	collapsible := ui.Collapsible(c.Key(o.KeyPrefix+".usage"), usage.Summary, open, func() {
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			for _, row := range usage.Rows {
				row := row
				ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					design.Apply(ui.Text(c, row.Label), t.TextMuted(design.TypeXXS)).
						Width(design.Space8*3 + design.Space6).Shrink(0)
					value := design.Apply(ui.Text(c, row.Value), t.Font(design.TypeXXS)).
						TextColor(t.LabelSecondary)
					if row.Exact != "" {
						// The compact display keeps the row readable; the exact
						// count is one hover away for anyone reconciling numbers.
						value.Description(T(o.Locale, "精确值：", "Exact: ") + row.Exact)
					}
				})
			}
		})
	})
	design.Apply(collapsible, t.Font(design.TypeXXS))
	if *open != before {
		if o.OnToggle != nil {
			o.OnToggle(*open)
		}
		if o.HoldPlace != nil {
			*o.HoldPlace = true
		}
	}
}
