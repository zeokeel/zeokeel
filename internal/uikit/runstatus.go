package uikit

// The product's one run-status vocabulary (S8 in docs/AGENT-SURFACES.md): the
// generating line, the queue badge and its popover, the labeled retry of a
// failure, the official stop control and the collapsible usage footer. It is
// deliberately a set of status rows rather than a progress widget: the product
// has no truthful percentage for a run, so it never draws one.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// UsageRow is one expanded usage line: its localized label, the compact value and
// the exact count for the value's tooltip.
type UsageRow struct {
	Label string
	Value string
	Exact string
}

// Usage is the collapsible usage accounting of one turn or session: the summary
// line, the expanded rows and the tooltip's exact figures. A Usage without Rows
// is a single quiet summary line (the session statistics strip); with Rows it is
// the official disclosure of the message footer.
type Usage struct {
	Summary string
	Exact   string
	Rows    []UsageRow
	// Open is the caller-persisted fold state of the disclosure; nil keeps a
	// local closed state (a host without fold memory).
	Open *bool
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
	// OnQueueClear clears the whole queue; nil hides the action.
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
			ui.Spinner(c).Label(label)
			design.Apply(ui.Text(c, label), t.TextMuted(design.TypeXS))
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
					Tooltip(T(o.Locale, "重新运行上一次请求", "Run the last request again"))
				button.Children(func() {
					ui.Icon(c, icons.Reload()).Size(design.Space4, design.Space4).
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
// clear actions.
func runStatusQueue(c *ui.Context, t *design.Tokens, o RunStatusOptions) {
	queued := o.Queued
	if len(queued) == 0 && o.QueueError == "" {
		return
	}
	var anchor ui.Element
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		if len(queued) > 0 && o.QueueOpen != nil {
			count := len(queued)
			label := fmt.Sprintf(T(o.Locale, "已排队 %d 条", "%d queued"), count)
			tip := T(o.Locale, "查看或取消排队的消息", "View or cancel queued messages")
			if o.QueuePaused {
				tip = T(o.Locale, "已停止：排队消息不会自动发送，展开后可手动发送或取消",
					"Stopped: queued messages are not sent automatically; open to send or cancel them")
			}
			button := ui.Button(c, label).Label(label).
				Height(t.Theme().Space(5)).PaddingX(design.Space2).Radius(design.RadiusMax).
				Background(t.BgHover).TextColor(t.Accent).
				Tooltip(tip)
			if *o.QueueOpen {
				button.Background(t.BgSelected)
			}
			if button.Clicked() {
				*o.QueueOpen = !*o.QueueOpen
				c.Invalidate()
			}
			anchor = button
		}
		if o.QueueError != "" && !o.Running && o.OnRetryQueue != nil {
			label := T(o.Locale, "重试", "Retry")
			retry := design.Apply(ui.Button(c, label), t.SecondaryButton()).
				Label(label).Shrink(0).
				Tooltip(T(o.Locale, "重新投递排队消息", "Retry the queued delivery"))
			if retry.Clicked() {
				o.OnRetryQueue()
			}
		}
	})
	if o.QueueError != "" {
		ErrorLine(c, o.QueueError)
	}
	if !anchor.Valid() || o.QueueOpen == nil || !*o.QueueOpen || len(queued) == 0 {
		return
	}
	panelWidth := anchor.Bounds().W
	if panelWidth < design.DialogWidthXS {
		// The anchor is the small count badge: give the prompt rows room to be
		// readable instead of truncating to the badge width.
		panelWidth = design.DialogWidthXS
	}
	open := true
	ui.PopoverBase(c, anchor, &open, func(panel ui.Element) {
		design.Apply(panel.Width(panelWidth).MaxWidthPercent(92).Padding(design.Space2).Gap(design.Space1).
			AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Margin(0, 0, design.Space2, 0).
			Background(t.BgRaised).Border(1, t.BorderL2).Radius(design.RadiusLG),
			t.ShadowOverlay()).
			Label(T(o.Locale, "排队消息", "Queued messages"))
		if o.QueuePaused && !o.Running {
			design.Apply(ui.Text(c, T(o.Locale,
				"已停止：排队消息不会自动发送，可手动发送或取消",
				"Stopped: queued messages are not sent automatically; send or cancel them here")),
				t.TextMuted(design.TypeXXXS)).MaxLines(2)
		}
		list := o.QueueList
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
			Label(T(o.Locale, "排队消息", "Queued messages")).
			MaxHeight(design.Space6 * 8).
			FillWidth().
			Rows(func(row ui.ListRow) {
				index := row.Index
				rowC := row.Context
				ui.Row(rowC).FillWidth().
					AlignItems(ui.Center).Gap(design.Space2).PaddingY(design.Space1).
					Children(func() {
						full := strings.TrimSpace(queued[index])
						design.Apply(ui.Text(rowC, runStatusQueuedSummary(queued[index])), t.Font(design.TypeXS)).
							TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…").
							Tooltip(full)
						if o.OnQueueCancel != nil {
							remove := ui.Button(rowC, "").
								Label(T(o.Locale, "取消排队", "Cancel queued")).
								Tooltip(fmt.Sprintf(T(o.Locale, "取消第 %d 条排队消息：", "Cancel queued message %d: "),
									index+1)+runStatusQueuedSummary(queued[index])).
								Size(t.Theme().Space(5), t.Theme().Space(5)).
								Radius(design.RadiusSM)
							remove.Children(func() {
								ui.Icon(rowC, icons.Close()).Size(design.Space3, design.Space3).
									TextColor(t.LabelTertiary)
							})
							if remove.Clicked() {
								o.OnQueueCancel(index)
							}
						}
					})
			})
		if o.OnQueueResume != nil || o.OnQueueClear != nil {
			ui.Divider(c)
			ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).
				Gap(design.Space2).Children(func() {
				if o.QueuePaused && !o.Running && o.OnQueueResume != nil {
					if send := design.Apply(ui.Button(c, T(o.Locale, "发送排队消息", "Send queued")),
						t.SecondaryButton()); send.Clicked() {
						o.OnQueueResume()
					}
				}
				if o.OnQueueClear != nil {
					if clear := design.Apply(ui.Button(c, T(o.Locale, "清空全部", "Clear all")),
						t.SecondaryButton()); clear.Clicked() {
						o.OnQueueClear()
					}
				}
			})
		}
	})
	if !open && o.QueueOpen != nil {
		*o.QueueOpen = false
	}
	if len(queued) == 0 && o.QueueOpen != nil {
		*o.QueueOpen = false
	}
}

// runStatusQueuedSummary truncates one queued prompt for the queue row.
func runStatusQueuedSummary(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	const limit = 80
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "…"
}

// runStatusStop draws the official stop control: a danger-styled native button,
// never next to the send action with the same weight (the composer keeps them in
// the same send position instead).
func runStatusStop(c *ui.Context, t *design.Tokens, o RunStatusOptions) {
	label := T(o.Locale, "停止", "Stop")
	button := design.Apply(ui.Button(c, label), t.DangerButton()).
		Label(label).Tooltip(label).Shrink(0)
	button.Children(func() {
		ui.Icon(c, icons.Stop()).Size(design.Space3, design.Space3).TextColor(t.DangerText)
	})
	if button.Clicked() {
		o.OnStop()
	}
}

// runStatusUsage draws the usage accounting: a single quiet summary line when
// there are no rows (the session statistics strip), the official disclosure with
// the label/value rows otherwise (a message's footer).
func runStatusUsage(c *ui.Context, t *design.Tokens, o RunStatusOptions) {
	usage := o.Usage
	if len(usage.Rows) == 0 {
		design.Apply(ui.Text(c, usage.Summary), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Tooltip(usage.Exact)
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
						value.Tooltip(T(o.Locale, "精确值：", "Exact: ") + row.Exact)
					}
				})
			}
		})
	})
	design.Apply(collapsible, t.Font(design.TypeXXXS))
	if *open != before {
		if o.OnToggle != nil {
			o.OnToggle(*open)
		}
		if o.HoldPlace != nil {
			*o.HoldPlace = true
		}
	}
}
