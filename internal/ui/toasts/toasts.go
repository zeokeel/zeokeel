// Package toasts contributes the desktop notification bridge to the product's
// one toast system: mygo's native toasts (ui.Context.AddToast).
//
// It is a kernel UI module: activation requires the host UI runtime and the
// host Notifier, installs the notification handler (whose calls hop to the UI
// thread with UIRuntime.Post) and registers one overlay.toasts slot item. The
// handler only queues the notification; the overlay's build drains the queue
// into native toasts and paints them with a branded look of our own
// (ui.ToastViewportBase). Where the stack sits is the host's call when it
// provides contracts.ToastAnchorService: below the screen header band at the
// main column's right edge, or yielded to the window's bottom-right while a
// modal or overlay is open. Without the service the module keeps its own
// default: a top-right popup below the header (which holds the window title,
// tabs and the right-side actions), laid out from the top down. At most
// toastMaxVisible toasts show at once (the rest wait in a visible queue count)
// and an ActionNotification's action label renders as the toast's button.
// Fading, hover/focus pause, the timeout, Escape and the screen-reader
// announcements stay the framework's. The module is only loaded by products
// with the desktop notifier bridge.
package toasts

import (
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/toasts"

// SlotID identifies this module's contribution to overlay.toasts.
const SlotID = "toasts.notifications"

// toastTimeout is how long one native toast shows. The framework pauses it
// while the pointer or keyboard focus rests on the toast, so it is the visible
// time, not the wall-clock time.
const toastTimeout = 4 * time.Second

// toastStickyTimeout keeps an error on screen until the user closes it (mygo
// ui.Toast.Timeout: a negative value shows until closed). A notice the user
// missed leaves no trace, and a failure that vanishes is a failure the user
// repeats (B-spec §3.1.5).
const toastStickyTimeout = -1

// maxPending bounds the queue while the window renders no frames; the oldest
// queued notifications are dropped, as the too-old ones would not be useful.
const maxPending = 32

// toastMaxVisible bounds how many toasts show at once (H14): further
// notifications wait in the module's queue and appear as earlier ones close.
// Two is the depth the corner affords at any window size — one card plus its
// merged count already cover a row of the reading column, and a burst must not
// stack a wall over it (audit VD-02: three cards covered the inspector and the
// composer at 1024).
const toastMaxVisible = 2

// toastNarrowWidth is the window width at which the stack keeps one card: the
// product's narrow layout (a 1024 DIP window is the case the audit measured)
// with one step of room above it. It is the window width, not the pane width:
// the module paints a window-level viewport.
const toastNarrowWidth = design.SidebarWidth*4 - design.Space8 // 1088

// stackDepth is how many cards this window shows at once, recomputed every
// frame so a window that shrank (or an inspector that opened over the column)
// gives the reading column its room back.
func stackDepth(c *ui.Context) int {
	if width, _ := c.Size(); width > 0 && width < toastNarrowWidth {
		return 1
	}
	return toastMaxVisible
}

// toastWidthFor is the widest card this window can hold where the host anchored
// the stack: the room from the window's right edge to the anchor, less the
// stack's own gap. A docked inspector takes 320 DIP of that room, so a narrow
// window's stack drops to the minimum card width instead of reaching over the
// pane (B-spec §3.1.5: 320 at most, 240 at least).
func toastWidthFor(c *ui.Context, st *state) float32 {
	width, _ := c.Size()
	room := width - design.Space4
	if anchor, ok := st.toastAnchor(); ok {
		room -= anchor.Right
	}
	if room < design.ToastWidth {
		return design.ToastMinWidth
	}
	return design.ToastWidth
}

// Toast placement: the window's bottom-right corner, growing upward, so a notice
// never sits on the reading column it just interrupted. The top-right popup it
// used to be covered the user's own message, the approval card and a settings
// footer's primary button (audit VD-02): a toast may only appear where the
// current task cannot be clicked. The host anchors the stack through
// contracts.ToastAnchorService (the shell knows the composer dock, the status
// strip and the open overlays); this default keeps the same corner for a host
// that provides no service.
const (
	// toastMinWidth bounds a card below: 240 DIP, a two-line notice and never a
	// banner across the column (B-spec §3.1.5, design.ToastWidth).
	toastMinWidth float32 = design.ToastMinWidth
	// toastInset is the gap the stack keeps from the window's right edge and
	// from whatever occupies the bottom edge.
	toastInset float32 = design.Space4
	// The close control's hit area is a full 24 DIP target (mygo/DESIGN
	// accessibility AP-17: an icon action's target is at least 24): a 20 DIP
	// button next to the toast's action button was easy to miss on a desktop
	// pointer. The glyph stays 16 (design.IconSizeS), quiet.
	toastCloseButtonSize float32 = design.Space6 // 24
)

// Config is the module's typed assembly config (all copy comes from the
// notification; the module has no configurable fields).
type Config struct{}

// state is the module instance's UI-thread state: the notification queue
// between the notifier handler and the overlay build, and the native toasts it
// added (by native toast id, to close the ones an updated setting now hides).
type state struct {
	notifier contracts.Notifier
	// locale localizes the toast chrome (the level label and the close hint).
	locale contracts.LocaleService
	// settings is the optional host settings store; the master
	// ui.notifications switch is read on every delivery/frame so it applies
	// without a restart. Per-level filters are gone (A7): the old
	// ui.notifications.hidden row is no longer read and never hides a toast.
	settings contracts.SettingsService
	// runtime requests the next frame after a toast closes so the queue's head
	// can take the freed slot.
	runtime contracts.UIRuntime
	// kernel resolves the optional host toast anchor lazily: a host may bind
	// the service after this module activates, so the lookup retries per frame
	// until it lands (nil keeps the module's default placement).
	kernel cordis.Context
	anchor contracts.ToastAnchorService
	// theme resolves the window's real appearance: the toast layer builds with
	// the theme turned over, so design.Of(c) would read the inverse and paint a
	// light card in a dark window (the dark snapshot caught it). The service is
	// looked up lazily like the anchor, because a host may bind it late.
	theme contracts.ThemeService

	pending []contracts.Notification
	// queue holds accepted notifications waiting for a free slot; at most
	// toastMaxVisible toasts render at once.
	queue []contracts.Notification
	// showing holds the ids of the native toasts currently on screen, so the
	// master ui.notifications switch can close them when it turns off.
	showing map[string]string
	// order is the showing ids in the order they appeared, oldest first: a
	// window that shrinks closes the oldest cards to keep the stack inside the
	// room it now has (VD-02).
	order []string
	// shown keeps the notification each showing toast stands for, so a placement
	// change can requeue it and it appears again at the new geometry instead of
	// vanishing.
	shown map[string]contracts.Notification
	// dropping marks the toasts a resize closed: their panels still animate out
	// at the coordinates of the old window, so they are not painted at all (a
	// ghost of the old geometry over the new one is worse than no fade).
	dropping map[string]bool
	// placeKey is the window size the showing cards were added at. A toast's panel
	// is laid out where the viewport was when it appeared, so a window that
	// changed size leaves the stack hanging over the new geometry (a narrowed
	// window that opened the inspector had its cards over the pane). A resize
	// closes them; they are transient, and the queue re-shows at the new place.
	placeKey string
	// merged holds one showing toast's dedupe bookkeeping by native toast id:
	// how many identical level+text notifications it represents and the ids
	// the notifier bookkeeping must forget when it closes.
	merged map[string]*mergedToast
}

// mergedToast is the bookkeeping of one showing toast that may stand for
// several identical notifications (same level and text): count is what the
// toast shows as "×N", notificationIDs every notifier id it merged.
type mergedToast struct {
	count           int
	notificationIDs []string
}

// dedupeToastID names the native toast of one level+text pair: adding a toast
// with an id already showing changes that toast (it "shows anew", resetting
// its time), so identical notifications merge into one row instead of stacking
// a wall of duplicates. The level is part of the key: a success and an error
// with the same words stay two toasts.
func dedupeToastID(level, text string) string {
	return "notice:" + level + "\x00" + text
}

// Module registers the toast overlay contribution.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		// The notifier is host-provided only in desktop products; in a minimal
		// test host the module degrades to a no-op instead of failing the
		// assembly.
		notifier, ok := ctx.Get(contracts.KeyNotifier)
		if !ok {
			return nil
		}
		settings, _ := ctx.Get(contracts.KeySettings)
		locale, _ := ctx.Get(contracts.KeyLocale)
		st := &state{notifier: notifier, locale: locale, settings: settings, runtime: runtime, kernel: ctx, showing: make(map[string]string)}
		st.anchor, _ = ctx.Get(contracts.KeyToastAnchor)

		// The handler may run on the notifier's goroutine: only queue the
		// notification, and let the UI thread's frame drain it (mygo's native
		// toasts must be added from a build callback).
		notifier.SetHandler(func(notification contracts.Notification) {
			runtime.Post(func() {
				if len(st.pending) >= maxPending {
					st.pending = st.pending[len(st.pending)-maxPending+1:]
				}
				st.pending = append(st.pending, notification)
				runtime.Invalidate()
			})
		})
		// The OS mirror reads the same render-time localization the in-app
		// cards apply: an approval notice's text carries a stable tool id the
		// overlay translates, and the notification center must not show the
		// raw id. Optional half — a bare notifier mirrors the text as created.
		if localizer, ok := notifier.(toastLocalizer); ok {
			localizer.SetLocalizer(func(text string) string { return localizeToastText(st.locale, text) })
		}
		if err := own(ctx, func() (func(), error) {
			return func() {
				notifier.SetHandler(nil)
				if localizer, ok := notifier.(toastLocalizer); ok {
					localizer.SetLocalizer(nil)
				}
				st.pending = nil
				st.queue = nil
				st.showing = nil
				st.order = nil
				st.dropping = nil
				st.shown = nil
				st.merged = nil
			}, nil
		}); err != nil {
			notifier.SetHandler(nil)
			if localizer, ok := notifier.(toastLocalizer); ok {
				localizer.SetLocalizer(nil)
			}
			return err
		}

		item := contracts.SlotItem{
			Slot:  contracts.SlotOverlayToasts,
			ID:    SlotID,
			Order: 10,
			Build: func(c *ui.Context) {
				buildOverlay(c, st)
			},
		}
		return own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(item)
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		WithName("ZeoKeel toast notifications")
}

// notificationsEnabled reports whether the desktop toast overlay may show
// notifications. The ui.notifications row is normalized: "false", "0" and "no"
// (case-insensitive, trimmed) disable it; anything else — including an empty,
// malformed or missing value and no settings store at all — keeps it enabled.
func notificationsEnabled(st *state) bool {
	if st.settings == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(st.settings.Get(contracts.SettingNotifications, ""))) {
	case "false", "0", "no":
		return false
	default:
		return true
	}
}

// notificationLevel normalizes one toast level (trim + lower case).
func notificationLevel(level string) string {
	return strings.ToLower(strings.TrimSpace(level))
}

// notificationType maps a notification level to the native toast Type ("error",
// "info", "success", "warning"); an unknown level keeps the theme's default
// look.
func notificationType(level string) string {
	switch notificationLevel(level) {
	case contracts.NotifyError:
		return "error"
	case contracts.NotifyWarning:
		return "warning"
	case contracts.NotifySuccess:
		return "success"
	case contracts.NotifyInfo:
		return "info"
	default:
		return ""
	}
}

// buildOverlay is the overlay slot's build callback: it drains the queued
// notifications into mygo's native toasts, closes the ones the master switch
// now hides, and paints the native toast stack with the product's branded look.
// Draining a queue here is the module's one guarded one-shot side effect, run on
// the UI thread inside the frame that renders the result.
func buildOverlay(c *ui.Context, st *state) {
	enabled := notificationsEnabled(st)
	// The master switch is hot: turning notifications off also drops the native
	// toasts already on screen instead of leaving them until they time out.
	// Levels are never filtered (A7 fixed behavior): every notification that
	// reaches the module is shown.
	if !enabled {
		for id := range st.showing {
			delete(st.showing, id)
			c.CloseToast(id)
		}
	}
	// A resize — or a right edge that moved because the inspector opened, or a
	// stack that moved from the bottom corner to the top because the screen
	// entered its hero or asked for a decision — drops the showing cards (see
	// placeKey): mygo lays a toast's panel out where the viewport was when it
	// appeared, so the card would keep the old coordinates. The bottom inset is
	// deliberately not part of the key: a modal moving the stack upward by a few
	// DIP must not close the notices it is about to sit behind; the side it hangs
	// from is, because that is a different corner.
	width, height := c.Size()
	right, fromBottom := design.Space4, true
	if anchor, ok := st.toastAnchor(); ok {
		right, fromBottom = anchor.Right, anchor.FromBottom
	}
	if width > 0 && height > 0 {
		key := strconv.Itoa(int(width)) + "x" + strconv.Itoa(int(height)) +
			"r" + strconv.Itoa(int(right)) + "b" + strconv.FormatBool(fromBottom)
		if st.placeKey != "" && st.placeKey != key {
			// The cards on screen were laid out for the old geometry, and mygo
			// keeps a toast's node — so they are closed rather than moved. They
			// are not requeued either: a toast is a transient notice the user has
			// already read, and a re-added one would come back with the same id as
			// a later delivery (two cards for one message). The queue is untouched,
			// so anything still waiting shows at the new place.
			for _, id := range st.order {
				delete(st.showing, id)
				delete(st.merged, id)
				delete(st.shown, id)
				if st.dropping == nil {
					st.dropping = make(map[string]bool)
				}
				st.dropping[id] = true
				c.CloseToast(id)
			}
			st.order = nil
		}
		st.placeKey = key
	}
	pending := st.pending
	st.pending = nil
	if !enabled {
		st.queue = nil
	} else {
		for _, notification := range pending {
			text := strings.TrimSpace(notification.Text)
			if text == "" {
				continue
			}
			st.queue = append(st.queue, notification)
		}
		// At most toastMaxVisible toasts render at once; the rest wait in the
		// FIFO queue and take a slot as earlier toasts close (H14: a burst must
		// not stack a wall of notifications over the transcript).
		// The stack's depth is per frame: a card that no longer fits closes
		// (oldest first) and its notification was already seen, so the queue
		// count is what tells the user more arrived while they read.
		depth := stackDepth(c)
		for len(st.order) > depth {
			oldest := st.order[0]
			st.order = st.order[1:]
			delete(st.showing, oldest)
			delete(st.merged, oldest)
			c.CloseToast(oldest)
		}
		for len(st.showing) < depth && len(st.queue) > 0 {
			notification := st.queue[0]
			st.queue = st.queue[1:]
			level := notificationLevel(notification.Level)
			text := strings.TrimSpace(notification.Text)
			// One id per level+text, whatever the placement: the id is the dedupe
			// key, so two identical notifications merge into one card with its
			// ×N count (audit VD-02's team overlay had the same "审批等待" notice
			// twice because a placement change had split the id).
			toastID := dedupeToastID(level, text)
			// Identical level+text within the toast's life merges: the same
			// native id means AddToast updates the showing toast (time reset)
			// and the count says how many notifications it stands for.
			entry := st.merged[toastID]
			if entry == nil {
				entry = &mergedToast{}
				if st.merged == nil {
					st.merged = make(map[string]*mergedToast)
				}
				st.merged[toastID] = entry
			}
			entry.count++
			if id := strings.TrimSpace(notification.ID); id != "" {
				entry.notificationIDs = append(entry.notificationIDs, id)
			}
			count := entry.count
			notifier := st.notifier
			runtime := st.runtime
			// The action half (contracts.ActionNotifier) rides on the
			// notification: show its label as the toast's action button and run
			// the callback (on the UI thread) when pressed. A panicking callback
			// must not take the window down. A merged update carries the latest
			// action, so its Undo applies to the most recent duplicate.
			action := notification.Action
			actionLabel := strings.TrimSpace(notification.ActionLabel)
			// An error stays until the user closes it: a failure the user was
			// told about for four seconds is a failure they will re-trigger
			// (B-spec §3.1.5, VD-02).
			timeout := toastTimeout
			if level == contracts.NotifyError {
				timeout = toastStickyTimeout
			}
			c.AddToast(ui.Toast{
				ID:      toastID,
				Title:   text,
				Type:    notificationType(level),
				Timeout: timeout,
				Action:  actionLabel,
				Data:    count,
				OnAction: func() {
					if action == nil {
						return
					}
					defer func() {
						if panicValue := recover(); panicValue != nil {
							log.Printf("ui/toasts: notification action panic: %v", panicValue)
						}
					}()
					action()
				},
				OnClose: func() {
					delete(st.showing, toastID)
					delete(st.shown, toastID)
					st.order = slicesDelete(st.order, toastID)
					merged := st.merged[toastID]
					delete(st.merged, toastID)
					if notifier != nil && merged != nil {
						for _, notificationID := range merged.notificationIDs {
							notifier.Dismiss(notificationID)
						}
					}
					// A free slot: the next queued notification takes it on the
					// next frame.
					if runtime != nil {
						runtime.Invalidate()
					}
				},
			})
			st.showing[toastID] = level
			if st.shown == nil {
				st.shown = make(map[string]contracts.Notification)
			}
			st.shown[toastID] = notification
			// A requeued card reuses its id: it is not a ghost of the old
			// geometry any more, it is the live card at the new one.
			delete(st.dropping, toastID)
			if !slicesContains(st.order, toastID) {
				st.order = append(st.order, toastID)
			}
		}
	}
	// The viewport is built every frame: the framework removes expired toasts,
	// runs their OnClose and keeps the exit transition when the last one goes,
	// and our look decides where and how they show. The product's tokens are read
	// HERE, from the window's own theme: the framework builds the toast look with
	// the theme turned over (its own cards are dark on a light desktop and light
	// on a dark one), and design.Of inside that callback would resolve the wrong
	// appearance — a white card in a dark window, which is what the dark snapshot
	// caught.
	t := design.Of(c)
	if appearance, ok := st.toastTheme(); ok {
		// The appearance comes from the host, not from this layer's inverted
		// context; the text scale still comes from the frame's preferences.
		base := design.Light()
		if appearance {
			base = design.Dark()
		}
		if t.TextScale > 0 {
			base.TextScale = t.TextScale
		}
		t = base
	}
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		buildToastViewport(c, st, t, viewport, toasts)
	})
}

// toastTheme resolves the window's appearance from the host, retrying the
// lookup per frame until a late-bound service lands.
func (st *state) toastTheme() (bool, bool) {
	if st.theme == nil && st.kernel != (cordis.Context{}) {
		st.theme, _ = st.kernel.Get(contracts.KeyTheme)
	}
	if st.theme == nil {
		return false, false
	}
	return st.theme.Dark(), true
}

// toastAnchor resolves the optional host placement service, retrying the
// kernel lookup per frame until a host that bound it late lands. ok=false
// keeps the module's default top-right placement.
func (st *state) toastAnchor() (contracts.ToastAnchor, bool) {
	if st.anchor == nil && st.kernel != (cordis.Context{}) {
		st.anchor, _ = st.kernel.Get(contracts.KeyToastAnchor)
	}
	if st.anchor == nil {
		return contracts.ToastAnchor{}, false
	}
	return st.anchor.Anchor(), true
}

// buildToastViewport places the toast stack where the host anchors it
// (contracts.ToastAnchorService): the window's bottom-right corner, above
// whatever the frame's bottom edge holds (the composer dock on the conversation
// screen), so the stack never covers the reading column, an approval card, a
// footer's primary button or the send control (audit VD-02). Without the service
// the stack keeps the same corner with the plain inset. The queue count rides
// the same anchor. The framework keeps the pointer pass-through, hover pause and
// screen-reader roles.
func buildToastViewport(c *ui.Context, st *state, t *design.Tokens, viewport ui.Element, toasts []ui.Toast) {
	// The stack keeps the window's bottom-right corner and grows upward, with the
	// host's insets: the right one keeps it out of the docked inspector's column
	// and the bottom one above the composer dock and the status strip.
	// The stack hangs from the edge the host names: the bottom right corner in
	// the ordinary case, the top right when the bottom edge is taken by a card
	// the user must press (the hero's setup card, an approval card). Justify
	// follows FromBottom — with a top anchor the row must start at the top, or
	// the padding is simply a larger gap above a bottom-aligned stack (the
	// landing/toast frames had cards on the composer's send button that way).
	justify := ui.End
	if anchor, ok := st.toastAnchor(); ok {
		viewport.Padding(anchor.Top, anchor.Right, anchor.Bottom, design.Space4)
		if !anchor.FromBottom {
			justify = ui.Start
		}
	} else {
		viewport.Padding(design.Space0, design.Space4, toastInset+design.Space8*5, design.Space4)
	}
	viewport.FillWidth().AlignItems(ui.End).Justify(justify).Gap(design.Space2)
	for _, toast := range toasts {
		if st.dropping[toast.ID] {
			continue
		}
		buildToast(c, st, t, toast)
	}
	if queued := len(st.queue); queued > 0 {
		// A sentence, not a kbd or micro label: the metadata step (TypeXXS), never
		// TypeXXXS's 11 DIP line (DESIGN.md §5.3).
		design.Apply(ui.Text(c, st.tr(
			"+ "+strconv.Itoa(queued)+" 条通知排队", "+ "+strconv.Itoa(queued)+" more queued")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
	}
	// A closed toast leaves the framework's list once its exit is over: forget it.
	if len(st.dropping) > 0 {
		live := make(map[string]bool, len(toasts))
		for _, toast := range toasts {
			live[toast.ID] = true
		}
		for id := range st.dropping {
			if !live[id] {
				delete(st.dropping, id)
			}
		}
	}
}

// toastLevel is the branded presentation of one notification level: a short
// label, its text color token and an icon, so success/error/warning are
// distinguishable at a glance without a large badge.
type toastLevel struct {
	label string
	color ui.Color
	icon  *ui.SVG
}

// toastLevelFor resolves the look of a toast Type (the level the module mapped
// from the notification). Info keeps the plain label: the icon set has no info
// glyph, and a made-up one would be noise.
func (st *state) toastLevelFor(t *design.Tokens, toastType string) toastLevel {
	switch strings.ToLower(strings.TrimSpace(toastType)) {
	case "error":
		return toastLevel{label: st.tr("错误", "Error"), color: t.DangerText, icon: icons.AlertTriangle()}
	case "warning":
		return toastLevel{label: st.tr("警告", "Warning"), color: t.WarningText, icon: icons.AlertTriangle()}
	case "success":
		return toastLevel{label: st.tr("成功", "Success"), color: t.SuccessText, icon: icons.Check()}
	default:
		return toastLevel{label: st.tr("提示", "Info"), color: t.LabelTertiary}
	}
}

// buildToast is one branded toast, in the unified skeleton of V24: a fixed
// 16 DIP status-icon slot (info levels keep the slot empty so every card starts
// at the same edge), a title line at TypeXS/600 in the level's semantic color,
// the notification copy as the TypeXXS body, an optional action and the quiet
// 16 DIP close glyph. All cards share the min/max width, the MD radius, the
// regular border and the overlay elevation, so a stack reads as one surface.
// Timing, hover pause and announcements stay with ToastBase.
func buildToast(c *ui.Context, st *state, t *design.Tokens, toast ui.Toast) {
	p := ui.ToastBase(c, toast)
	level := st.toastLevelFor(t, toast.Type)
	// The body and the action label localize at render time, like the level
	// title: a language switch while the toast is alive never leaves a
	// mixed-language card (US-14).
	body := localizeToastText(st.locale, toast.Title)
	actionLabel := localizeToastText(st.locale, toast.Action)
	box := p.Root.Row().AlignItems(ui.Start).Gap(design.Space2).
		MinWidth(toastMinWidth).MaxWidth(toastWidthFor(c, st)).
		Padding(design.Space2, design.Space3).
		Radius(design.RadiusLG).Background(t.BgOverlay).Border(1, t.BorderL2).
		// It arrives from 8 DIP below, fading in over MotionBase, and leaves
		// over MotionFast: the toast's own timing (B-spec §1.5: entering 120,
		// leaving 80), not the slower dialog entrance.
		Transition(toastTransition(c))
	// The elevation is the shared floating-surface token (stronger in the dark
	// theme), not a bare shadow value of this module.
	box = design.Apply(box, t.ShadowOverlay())
	box.Children(func() {
		// The status slot: one 16 DIP glyph (info reserves the slot without a
		// glyph; the icon set has no info mark and an invented one is noise).
		ui.Box(c).Width(design.IconSizeS).Height(design.IconSizeS).Shrink(0).
			Children(func() {
				if level.icon != nil {
					ui.Icon(c, level.icon).Size(design.IconSizeS, design.IconSizeS).TextColor(level.color)
				}
			})
		ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				// 13/600: the spec's toast title. The weight comes from the token
				// scale rather than a literal, so the 13 DIP step keeps its
				// heading weight without a raw 600 at the call site.
				design.Apply(ui.Text(c, level.label), t.Font(design.TypeXS)).
					FontWeight(design.WeightSemibold).TextColor(level.color).
					Grow(1).MaxLines(1).Ellipsis("…")
				if count, ok := toast.Data.(int); ok && count > 1 {
					// One toast standing for N identical notifications: a quiet
					// count, not another badge row.
					design.Apply(ui.Text(c, "×"+strconv.Itoa(count)), t.Font(design.TypeXXS)).
						TextColor(t.LabelTertiary)
				}
			})
			// The notification copy is the body at the metadata size: the title
			// carries the level, the body the content (V24).
			design.Apply(ui.Text(c, body), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(2)
			if toast.Description != "" {
				design.Apply(ui.Text(c, localizeToastText(st.locale, toast.Description)),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
			}
			if actionLabel != "" {
				action := p.ActionButton().Padding(design.Space1, design.Space2).
					Radius(design.RadiusSM).Label(actionLabel).
					Transition(design.TransitionColors(c))
				switch {
				case action.Pressed():
					action = action.Background(t.BgSelected)
				case action.Hovered():
					action = action.Background(t.BgHover)
				}
				action.Children(func() {
					design.Apply(ui.Text(c, actionLabel), t.Font(design.TypeXS)).
						FontWeight(design.WeightSemibold).TextColor(t.Accent)
				})
			}
		})
		// No Tooltip: a tooltip is an overlay with its own Escape handler, and this
		// control lives on a surface Escape dismisses (A4-I-01).
		closeLabel := st.tr("关闭通知", "Close notification")
		close := p.CloseButton().Size(toastCloseButtonSize, toastCloseButtonSize).
			Center().Radius(design.RadiusSM).Label(closeLabel).
			Transition(design.TransitionColors(c))
		switch {
		case close.Pressed():
			close = close.Background(t.BgSelected)
		case close.Hovered():
			close = close.Background(t.BgHover)
		}
		close.Children(func() {
			ui.Icon(c, icons.Close()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelTertiary)
		})
	})
}

// toastTransition is the toast card's entrance and exit: up from 8 DIP while it
// fades in over MotionBase, out over MotionFast. A reduced-motion desktop shows
// both at once.
func toastTransition(c *ui.Context) ui.ElementTransition {
	if design.ReduceMotion(c) {
		return ui.ElementTransition{Duration: -1}
	}
	return ui.ElementTransition{
		Duration:     design.MotionBase,
		ExitDuration: design.MotionFast,
		Ease:         ui.EaseOut,
		Enter:        &ui.Motion{Opacity: 0, Y: design.Space2},
		Exit:         &ui.Motion{Opacity: 0},
	}
}

// slicesContains reports whether id is in ids.
func slicesContains(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// slicesDelete returns ids without id, keeping the order of the rest.
func slicesDelete(ids []string, id string) []string {
	for index, candidate := range ids {
		if candidate == id {
			return append(ids[:index:index], ids[index+1:]...)
		}
	}
	return ids
}

// tr localizes the toast chrome for the current frame.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

// toastLocalizer is the optional notifier half the desktop bridge implements
// (uiroot.notifierService.SetLocalizer): it applies this module's render-time
// body localization to the OS notification mirror too.
type toastLocalizer interface{ SetLocalizer(func(string) string) }

// --- render-time copy localization (US-14) ----------------------------------
//
// The notification contract carries one Text string, localized by its creator
// at creation time; the level title is localized here at render time. Switching
// the language while a toast is alive used to leave a mixed-language card (zh
// body under an en title). The body and the action label now pass through the
// same render-time localization: the product's known toast copy translates both
// ways through the phrasebook below, and unknown copy passes through verbatim
// (an honest best effort — no invented translation).

// toastPhrase is one known toast copy in both languages.
type toastPhrase struct{ zh, en string }

// toastPhrases is the product's exact-match toast copy (session/goal/memory/
// reminder/export/queue outcomes and the host's session action label).
var toastPhrases = []toastPhrase{
	{"已停止", "Stopped"},
	{"已加入排队", "Added to the queue"},
	{"已复制", "Copied"},
	{"对话已导出", "Conversation exported"},
	{"目标已保存", "Goal saved"},
	{"目标已清除", "Goal cleared"},
	{"目标已暂停", "Goal paused"},
	{"目标已继续", "Goal resumed"},
	{"目标已完成", "Goal completed"},
	{"计划已提交审阅", "Plan submitted for review"},
	{"已添加记忆", "Memory added"},
	{"已删除记忆", "Memory deleted"},
	{"已更新记忆", "Memory updated"},
	{"已清空", "Cleared"},
	{"没有可整理的重复记忆", "No duplicate memories to tidy"},
	{"提醒已创建", "Reminder created"},
	{"提醒已删除", "Reminder deleted"},
	{"会话已归档", "Conversation archived"},
	{"已打开工作区", "Workspace opened"},
	{"撤销", "Undo"},
	{"打开会话", "Open conversation"},
	{"后台会话已完成", "Background session finished"},
	{"后台会话运行失败", "Background session run failed"},
	{"剪贴板里没有图片；请用「附件」或拖拽添加图片", "No image on the clipboard; use Attach or drag an image in"},
	{"已把光标放到输入框，发送内容由你决定", "The cursor is in the composer; what to send is your call"},
}

// toastAffixes is dynamic copy with a translatable prefix and suffix around a
// payload. The payload stays verbatim by default (a session title, a count, an
// error string — copy the kit cannot translate); an affix whose payload is
// itself translatable (the approval notices' stable tool id) sets payload to
// localize it at render time as well (2026-11-17 UI review P3).
type toastAffixes struct {
	zhPrefix, zhSuffix, enPrefix, enSuffix string
	payload                                func(contracts.LocaleService, string) string
}

// approvalToolPayload localizes an approval notification's tool-id payload at
// render time: a known id takes the kit's friendly name in the current
// language; an unknown id falls back to the original string (lead ruling
// 2026-11-17), which also passes a legacy display-name payload through
// verbatim instead of inventing a translation.
func approvalToolPayload(locale contracts.LocaleService, id string) string {
	return uikit.ToolDisplayName(locale, id)
}

var toastAffixTable = []toastAffixes{
	{"导出失败：", "", "Export failed: ", "", nil},
	{"停止失败：", "", "Stop failed: ", "", nil},
	{"归档失败：", "", "Archive failed: ", "", nil},
	{"打开文件失败：", "", "Open file failed: ", "", nil},
	// The approval notices carry a tool-id payload the overlay localizes. The
	// origin-specific suffixes must precede the suffix-less entry: its empty
	// suffix matches every tail and would swallow the origin otherwise.
	{"审批等待：", "（自动化）", "Approval waiting: ", " (Automation)", approvalToolPayload},
	{"审批等待：", "（子代理）", "Approval waiting: ", " (Subagent)", approvalToolPayload},
	{"审批等待：", "", "Approval waiting: ", "", approvalToolPayload},
	{"审批超时已自动拒绝：", "（自动化）", "Approval timed out and was auto-denied: ", " (automation)", approvalToolPayload},
	{"会话「", "」已完成", "Conversation \"", "\" finished", nil},
	{"会话「", "」运行失败", "Conversation \"", "\" run failed", nil},
	{"已整理 ", " 条", "Tidied ", " memories", nil},
	{"已清空 ", " 条记忆", "Cleared ", " memories", nil},
}

// localizeToastText renders one toast string in the current locale at render
// time (US-14): exact phrasebook matches first, then the affix patterns; an
// unknown string is returned unchanged. An affix with a payload localizer
// resolves its payload in the active language even when the copy already
// matches that language — the approval notices carry a stable tool id, not a
// display name, so the id must become a name in every locale (2026-11-17 UI
// review P3).
func localizeToastText(locale contracts.LocaleService, text string) string {
	english := uikit.Language(locale) == "en"
	for _, phrase := range toastPhrases {
		if english && text == phrase.zh {
			return phrase.en
		}
		if !english && text == phrase.en {
			return phrase.zh
		}
	}
	for _, affix := range toastAffixTable {
		zhMatch := strings.HasPrefix(text, affix.zhPrefix) && strings.HasSuffix(text, affix.zhSuffix)
		enMatch := strings.HasPrefix(text, affix.enPrefix) && strings.HasSuffix(text, affix.enSuffix)
		if !zhMatch && !enMatch {
			continue
		}
		prefix, suffix := affix.zhPrefix, affix.zhSuffix
		if enMatch && !zhMatch {
			prefix, suffix = affix.enPrefix, affix.enSuffix
		}
		payload := strings.TrimSuffix(strings.TrimPrefix(text, prefix), suffix)
		if affix.payload != nil {
			payload = affix.payload(locale, payload)
		}
		if english {
			return affix.enPrefix + payload + affix.enSuffix
		}
		return affix.zhPrefix + payload + affix.zhSuffix
	}
	return text
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
