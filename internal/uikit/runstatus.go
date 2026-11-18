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
	"strconv"
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
	// RunStateHidden drops the run row from the bar because another surface on
	// the same screen already states it: the conversation's reply shows its own
	// live line (生成中) inside the transcript, and two run states a few DIP apart
	// read as two runs (user report: "这里出现两个，生成中和运行中"). The stopping
	// state is not a duplicate and always shows, so a stop in flight stays
	// visible where the stop was pressed.
	RunStateHidden bool
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
	// window; Used 0 or Window 0 hides the ring (a ratio the product cannot
	// compute is not shown at zero).
	Used   int
	Window int
	// Metrics is the bar's read-out, in the order it is drawn: the run's shape
	// (rounds · steps · speed), its volume (tokens · cache hit) and then the
	// context ring. A group with no truthful figure is left out whole — the bar
	// never pads with a zero or a dash.
	Metrics StatusStripMetrics
	// Narrow drops the two text groups and keeps the ring and the 详情 control:
	// the numbers stay one click away instead of wrapping the bar onto a second
	// line in a narrow window.
	Narrow bool
	// KeyPrefix keys the strip and its rows (the session ID).
	KeyPrefix string
	// Locale localizes the entry's own copy; nil falls back to Chinese.
	Locale contracts.LocaleService
}

// StatusStripMetrics is the strip's read-out, already formatted by the caller:
// the strip owns the layout, never the wording of a measurement.
type StatusStripMetrics struct {
	// Rounds, Steps and Speed form the first group (activity); each is optional,
	// and a group with nothing to show is not drawn at all.
	Rounds int
	Steps  int
	Speed  float64 // tokens per second, 0 when the turn was too short to say
	// Tokens is the session's generated volume and CachePercent the share of its
	// prompt tokens that came from the cache; 0 leaves the part out.
	Tokens       int
	CachePercent int
}

// MetricsVisible reports whether the strip should draw its read-out cluster. A
// running turn shows nothing here: the usage a session accumulates lands when the
// turn settles, so mid-turn the cluster would print two counters (轮次, 步数) and
// leave the speed, the tokens, the cache hit and the context empty — half a line
// that reads as broken. The transcript's own live line already says the turn is
// generating; the figures appear when they are whole.
func MetricsVisible(o StatusStripOptions) bool {
	if o.Running {
		return false
	}
	return o.Metrics.any() || (o.Used > 0 && o.Window > 0)
}

// any reports whether the read-out has anything to say.
func (m StatusStripMetrics) any() bool {
	return m.Rounds > 0 || m.Steps > 0 || m.Speed > 0 || m.Tokens > 0 || m.CachePercent > 0
}

// StatusStrip draws the composer-adjacent status bar. It draws nothing at all
// when it has nothing truthful to say (no run, no queue, no token accounting),
// so an idle conversation keeps one quiet composer instead of a zeroed row.
func StatusStrip(c *ui.Context, o StatusStripOptions) {
	t := design.Of(c)
	hasQueue := len(o.Queued) > 0 || o.QueueError != ""
	showMetrics := MetricsVisible(o)
	if !hasQueue && !showMetrics {
		return
	}
	key := "runstatus.strip"
	if prefix := strings.TrimSpace(o.KeyPrefix); prefix != "" {
		key += "/" + prefix
	}
	// The bar is 32 DIP, the compact row step: it reads as chrome under the
	// composer, not as a second panel. Nothing in it is 11 DIP copy (VD-10): the
	// status word, the queue count and the gauge's numbers are all 12.
	// The bar is a centred read-out band, not a right-aligned toolbar: the
	// metrics cluster takes the middle of the reading column, and the queue badge
	// (when prompts wait) is positioned at the left edge so it can never push the
	// cluster off centre.
	row := ui.Row(c.Key(key)).FillWidth().Height(design.RowCompactHeight).
		AlignItems(ui.Center).Justify(ui.Center)
	row.Children(func() {
		if showMetrics {
			statusStripMetrics(c, t, o, key)
		}
		if hasQueue {
			ui.Box(c.Key(key + ".queue.slot")).Absolute().
				Top(0).Bottom(0).Start(0).AlignItems(ui.Center).
				Children(func() {
					statusStripQueue(c, t, o, key)
				})
		}
	})
	if o.QueueError != "" {
		ErrorLine(c, o.QueueError)
	}
}

// statusStripRun draws the left zone: what the run is doing, and the queue (with
// its count) while prompts wait. Both are quiet; the accent lives in the
// spinner alone, so the bar never competes with the send control.
func statusStripRun(c *ui.Context, t *design.Tokens, o StatusStripOptions, key string) {
	if !o.Running || o.RunStateHidden {
		return
	}
	label := o.StatusLabel
	if strings.TrimSpace(label) == "" {
		label = T(o.Locale, "运行中", "Running")
	}
	// One status line with a stable identity: v0.9 reads a RoleStatus whole again
	// whenever its text changes (docs/ui/accessibility.md).
	ui.Row(c.Key(key + ".run")).AlignItems(ui.Center).Gap(design.Space2).
		Role(ui.RoleStatus).Label(label).Children(func() {
		ui.Spinner(c).Label(label).Size(design.IconSizeS, design.IconSizeS).
			TextColor(t.Accent)
		StatusText(c, t, design.TypeXXS, StatusRunning, label)
	})
}

// statusStripQueue draws the queue control and its popover in the strip's left
// slot: the prompts waiting for the turn, with per-row cancel, the paused
// explanation and the guarded clear.
func statusStripQueue(c *ui.Context, t *design.Tokens, o StatusStripOptions, key string) {
	_ = t
	runStatusQueue(c, design.Of(c), RunStatusOptions{
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

// statusStripMetrics draws the bar's right zone: the read-out groups of the
// usage audit (activity, volume, then the context ring), right-aligned, 12 DIP,
// with the figures in mono and the words in the text face. Groups are separated
// by Space3 and their parts by a middot with Space1 around it; a group with no
// truthful figure is skipped whole, so an empty bar is just the 详情 control.
//
// In a narrow window the two text groups fold into 详情 and the bar keeps the
// ring and the control: the figures stay one click away instead of the bar
// wrapping onto a second line.
func statusStripMetrics(c *ui.Context, t *design.Tokens, o StatusStripOptions, key string) {
	row := ui.Row(c.Key(key + ".metrics")).AlignItems(ui.Center).Gap(design.Space3)
	row.Children(func() {
		if !o.Narrow {
			statusStripActivityGroup(c, t, o)
			statusStripVolumeGroup(c, t, o)
		}
		statusStripMeter(c, t, o, key)
	})
}

// statusStripPart draws one part of a group: an optional figure in mono, an
// optional word in the text face, both 12 DIP tertiary and drawn tight (the part
// is one token of the line, so no gap lands between a number and its unit).
func statusStripPart(c *ui.Context, t *design.Tokens, figure, word, description string) {
	ui.Row(c).Gap(design.Space0).AlignItems(ui.Center).Children(func() {
		if figure != "" {
			part := design.Apply(ui.Text(c, figure), t.MonoText(design.TypeXXS)).
				TextColor(t.LabelTertiary).NoWrap()
			if description != "" {
				part.Description(description)
			}
		}
		if word != "" {
			if figure != "" {
				// One Space1 between the number and its unit, the same 4 DIP the
				// middot uses: never a space character inside the string, which the
				// element boundary would trim.
				ui.Box(c).Width(design.Space1).Shrink(0)
				design.Apply(ui.Text(c, strings.TrimSpace(word)), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).NoWrap()
				return
			}
			design.Apply(ui.Text(c, word), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).NoWrap()
		}
	})
}

// statusStripGroupSeparator draws the middot between two parts of one group, with
// Space1 on each side: the group reads as one sentence of figures.
func statusStripGroupSeparator(c *ui.Context, t *design.Tokens) {
	ui.Box(c).Width(design.Space1).Shrink(0)
	design.Apply(ui.Text(c, "·"), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
	ui.Box(c).Width(design.Space1).Shrink(0)
}

// statusStripActivityGroup is 轮次 · 步数 · 速度, with the activity glyph: the
// shape of the run.
func statusStripActivityGroup(c *ui.Context, t *design.Tokens, o StatusStripOptions) {
	rounds, steps, speed := o.Metrics.Rounds, o.Metrics.Steps, o.Metrics.Speed
	if rounds <= 0 && steps <= 0 && speed <= 0 {
		return
	}
	ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.Activity()).Size(design.IconSizeS, design.IconSizeS).
			TextColor(t.LabelTertiary)
		parts := ui.Row(c).Gap(design.Space0).AlignItems(ui.Center)
		parts.Children(func() {
			// "19 轮 898 步 · 174 tok/s": the counts and the speed are one reading,
			// so only the speed gets the middot (DSH's own line).
			first := true
			if rounds > 0 {
				statusStripPart(c, t, strconv.Itoa(rounds), T(o.Locale, "轮", "rounds"), "")
				first = false
			}
			if steps > 0 {
				if !first {
					// A space, not a middot: 轮 and 步 belong together.
					ui.Box(c).Width(design.Space1).Shrink(0)
				}
				statusStripPart(c, t, strconv.Itoa(steps), T(o.Locale, "步", "steps"), "")
				first = false
			}
			if speed > 0 {
				if !first {
					statusStripGroupSeparator(c, t)
				}
				statusStripPart(c, t, FormatCompactCount(int(speed+0.5)),
					T(o.Locale, "tok/s", "tok/s"), "")
			}
		})
	})
}

// statusStripVolumeGroup is 总量 · 缓存命中, with the data glyph: how much the
// session moved. English puts the percentage first ("97% cached"), Chinese the
// word first ("缓存命中 97%").
func statusStripVolumeGroup(c *ui.Context, t *design.Tokens, o StatusStripOptions) {
	tokens, cached := o.Metrics.Tokens, o.Metrics.CachePercent
	if tokens <= 0 && cached <= 0 {
		return
	}
	english := o.Locale != nil && o.Locale.Language() == "en"
	ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.Data()).Size(design.IconSizeS, design.IconSizeS).
			TextColor(t.LabelTertiary)
		parts := ui.Row(c).Gap(design.Space0).AlignItems(ui.Center)
		parts.Children(func() {
			first := true
			if tokens > 0 {
				statusStripPart(c, t, FormatCompactCount(tokens), T(o.Locale, "tok", "tok"), "")
				first = false
			}
			if cached > 0 {
				if !first {
					statusStripGroupSeparator(c, t)
				}
				// The word and the figure are two parts (one face each: mono for the
				// number, the text face for the word), so the space between them must
				// be its own element — a trailing space inside a text node is eaten at
				// the element boundary, and the line read 缓存命中86%.
				if english {
					statusStripPart(c, t, strconv.Itoa(cached)+"%", "", "")
					ui.Box(c).Width(design.Space1).Shrink(0)
					statusStripPart(c, t, "", "cached", "")
					return
				}
				statusStripPart(c, t, "", T(o.Locale, "缓存命中", "cached"), "")
				ui.Box(c).Width(design.Space1).Shrink(0)
				statusStripPart(c, t, strconv.Itoa(cached)+"%", "", "")
			}
		})
	})
}

// statusStripMeter draws the context level as the official Meter: a 32 by 4 DIP
// bar filled by the used share of the window, Accent until 85%, Warning to 100%
// and Danger at the edge. The official meter is the one long, thin level
// indicator the product has, and its range/label/description give assistive
// technology the real numbers the bar only sketches.
//
// coreMeter sets MinWidth(Space20)/Height(Space2) of its own; the strip wants the
// icon-height, hairline-weight bar, so the box is pinned here (otherwise it draws
// an 80 by 8 capsule).
func statusStripMeter(c *ui.Context, t *design.Tokens, o StatusStripOptions, key string) {
	if o.Used <= 0 || o.Window <= 0 {
		return
	}
	ratio := float64(o.Used) / float64(o.Window)
	if ratio > 1 {
		ratio = 1
	}
	pct := int(ratio*100 + 0.5)
	ui.Row(c.Key(key + ".meter")).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		// Capacity semantics: 85% is amber and 100% is red, so Warning is below
		// Critical (MeterLevels reads Critical < Warning as "low values are the bad
		// ones", the battery reading, which is not this gauge).
		levels := &ui.MeterLevels{
			Warning:     0.85 * float64(o.Window),
			Critical:    float64(o.Window),
			NormalColor: t.Accent,
		}
		ui.Meter(c, float64(o.Used), 0, float64(o.Window), levels).
			MinWidth(design.Space8).Width(design.Space8).Height(design.Space1).
			Label(fmt.Sprintf(T(o.Locale, "上下文 %d%%", "Context %d%%"), pct)).
			Description(FormatCompactCount(o.Used) + " / " + FormatCompactCount(o.Window))
		statusStripPart(c, t, strconv.Itoa(pct)+"%", "", "")
	})
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
