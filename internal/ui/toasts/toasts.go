// Package toasts contributes the desktop notification bridge to the product's
// one toast system: mygo's native toasts (ui.Context.AddToast).
//
// It is a kernel UI module: activation requires the host UI runtime and the
// host Notifier, installs the notification handler (whose calls hop to the UI
// thread with UIRuntime.Post) and registers one overlay.toasts slot item. The
// handler only queues the notification; the overlay's build drains the queue
// into native toasts and paints them with a branded look of our own
// (ui.ToastViewportBase), so the notification never covers the conversation
// composer or a dialog footer: the stack sits in the bottom-right corner above
// the composer's reserved height. At most toastMaxVisible toasts show at once
// (the rest wait in a visible queue count) and an ActionNotification's action
// label renders as the toast's button. Fading, hover/focus pause, the timeout,
// Escape and the screen-reader announcements stay the framework's. The module
// is only loaded by products with the desktop notifier bridge.
package toasts

import (
	"encoding/json"
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

// maxPending bounds the queue while the window renders no frames; the oldest
// queued notifications are dropped, as the too-old ones would not be useful.
const maxPending = 32

// Toast placement. The conversation composer is a card pinned to the bottom of
// the content column (about 116 DIP tall, inside the screen's 24 DIP bottom
// padding), so the toast stack reserves its height plus a gap instead of
// floating over the send row. Below the breakpoint the composer's toolbar wraps
// to a taller card, hence the larger inset.
// toastMaxVisible bounds how many toasts show at once (H14): further
// notifications wait in the module's queue and appear as earlier ones close.
const toastMaxVisible = 2

// The placement measures are on the design system's 4 DIP grid, written as
// multiples of its spacing tokens instead of bare numbers, so the stack stays
// on the same grid as the composer it floats above.
const (
	// The composer card with a staged attachment is about 178 DIP tall on a
	// wide window (the screenshot probe shows its top edge at y≈598 of 800) and
	// taller when its toolbar wraps on a narrow one; the stack keeps its bottom
	// above the card so no toast touches the input, the send row or a settings
	// page footer.
	toastBottomInset        float32 = design.Space8*7 - design.Space1  // 220
	toastBottomInsetNarrow  float32 = design.Space8*7 + design.Space3  // 236
	toastBottomInsetBreakAt float32 = design.Space8 * 35               // 1120
	toastMaxWidth           float32 = design.Space8*11 + design.Space2 // 360
	// The close control's hit area is a full 24 DIP target (mygo/DESIGN
	// accessibility AP-17: an icon action's target is at least 24): a 20 DIP
	// button next to the toast's action button was easy to miss on a desktop
	// pointer. The glyph stays 16 (design.Space4), quiet.
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
	// settings is the optional host settings store; ui.notifications and
	// ui.notifications.hidden are read on every delivery/frame so switches
	// apply without a restart.
	settings contracts.SettingsService
	// badHiddenRaw remembers the last malformed hidden-levels row already
	// logged, so the per-frame read cannot spam the log.
	badHiddenRaw string
	// runtime requests the next frame after a toast closes so the queue's head
	// can take the freed slot.
	runtime contracts.UIRuntime

	pending []contracts.Notification
	// queue holds accepted notifications waiting for a free slot; at most
	// toastMaxVisible toasts render at once.
	queue []contracts.Notification
	// showing maps a native toast id to its notification level, so the level
	// filter can close toasts already on screen.
	showing map[string]string
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
		st := &state{notifier: notifier, locale: locale, settings: settings, runtime: runtime, showing: make(map[string]string)}

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
		if err := own(ctx, func() (func(), error) {
			return func() {
				notifier.SetHandler(nil)
				st.pending = nil
				st.queue = nil
				st.showing = nil
				st.merged = nil
			}, nil
		}); err != nil {
			notifier.SetHandler(nil)
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

// notificationLevel normalizes one toast level (trim + lower case); an empty or
// unknown level is returned as-is and is never filtered.
func notificationLevel(level string) string {
	return strings.ToLower(strings.TrimSpace(level))
}

// knownNotificationLevel reports whether level is one of the four filterable
// notification levels.
func knownNotificationLevel(level string) bool {
	switch level {
	case contracts.NotifyInfo, contracts.NotifySuccess, contracts.NotifyWarning, contracts.NotifyError:
		return true
	default:
		return false
	}
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

// hiddenLevels reads ui.notifications.hidden: a JSON array of levels to hide.
// Missing/empty rows mean nothing is hidden; a malformed row is logged once and
// treated as "nothing hidden" (bad values never filter toasts).
func hiddenLevels(st *state) map[string]bool {
	if st.settings == nil {
		return nil
	}
	raw := strings.TrimSpace(st.settings.Get(contracts.SettingNotificationHiddenLevels, ""))
	if raw == "" {
		return nil
	}
	var levels []string
	if err := json.Unmarshal([]byte(raw), &levels); err != nil {
		if st.badHiddenRaw != raw {
			st.badHiddenRaw = raw
			log.Printf("ui/toasts: ignoring malformed %s: %v", contracts.SettingNotificationHiddenLevels, err)
		}
		return nil
	}
	hidden := make(map[string]bool, len(levels))
	for _, level := range levels {
		normalized := notificationLevel(level)
		if knownNotificationLevel(normalized) {
			hidden[normalized] = true
		}
	}
	if len(hidden) == 0 {
		return nil
	}
	return hidden
}

// buildOverlay is the overlay slot's build callback: it drains the queued
// notifications into mygo's native toasts, closes the ones a switch now hides,
// and paints the native toast stack with the product's branded look. Draining a
// queue here is the module's one guarded one-shot side effect, run on the UI
// thread inside the frame that renders the result.
func buildOverlay(c *ui.Context, st *state) {
	enabled := notificationsEnabled(st)
	hidden := hiddenLevels(st)
	// Hot switches: hiding a level (or all notifications) also drops the
	// native toasts already on screen instead of leaving them until they time
	// out.
	for id, level := range st.showing {
		if !enabled || hidden[notificationLevel(level)] {
			delete(st.showing, id)
			c.CloseToast(id)
		}
	}
	pending := st.pending
	st.pending = nil
	if !enabled {
		st.queue = nil
	} else {
		for _, notification := range pending {
			text := strings.TrimSpace(notification.Text)
			if text == "" || hidden[notificationLevel(notification.Level)] {
				continue
			}
			st.queue = append(st.queue, notification)
		}
		// At most toastMaxVisible toasts render at once; the rest wait in the
		// FIFO queue and take a slot as earlier toasts close (H14: a burst must
		// not stack a wall of notifications over the transcript).
		for len(st.showing) < toastMaxVisible && len(st.queue) > 0 {
			notification := st.queue[0]
			st.queue = st.queue[1:]
			level := notificationLevel(notification.Level)
			text := strings.TrimSpace(notification.Text)
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
			c.AddToast(ui.Toast{
				ID:      toastID,
				Title:   text,
				Type:    notificationType(level),
				Timeout: toastTimeout,
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
		}
	}
	// The viewport is built every frame: the framework removes expired toasts,
	// runs their OnClose and keeps the exit transition when the last one goes,
	// and our look decides where and how they show.
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		buildToastViewport(c, st, viewport, toasts)
	})
}

// buildToastViewport places the toast stack in the bottom-right corner above the
// conversation composer. The composer is a card pinned to the bottom of the
// content column, so the stack reserves its height (plus a gap) instead of
// floating over the send row or a settings page footer; the framework keeps the
// pointer pass-through, hover pause and screen-reader roles.
func buildToastViewport(c *ui.Context, st *state, viewport ui.Element, toasts []ui.Toast) {
	width, _ := c.Size()
	bottom := toastBottomInset
	if width > 0 && width < toastBottomInsetBreakAt {
		bottom = toastBottomInsetNarrow
	}
	viewport.FillWidth().Padding(design.Space3, design.Space4, bottom, design.Space4).
		AlignItems(ui.End).Justify(ui.End).Gap(design.Space2)
	for _, toast := range toasts {
		buildToast(c, st, toast)
	}
	if queued := len(st.queue); queued > 0 {
		design.Apply(ui.Text(c, st.tr(
			"+ "+strconv.Itoa(queued)+" 条通知排队", "+ "+strconv.Itoa(queued)+" more queued")),
			design.Of(c).Font(design.TypeXXXS)).TextColor(design.Of(c).LabelTertiary).NoWrap()
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

// buildToast is one branded toast: the level (icon + label), the copy and the
// close control, on the raised surface with the product's border and the
// theme's overlay elevation. Timing, pause and announcements stay with
// ToastBase.
func buildToast(c *ui.Context, st *state, toast ui.Toast) {
	t := design.Of(c)
	p := ui.ToastBase(c, toast)
	level := st.toastLevelFor(t, toast.Type)
	box := p.Root.Row().AlignItems(ui.Start).Gap(design.Space2).
		MaxWidth(toastMaxWidth).Padding(design.Space2, design.Space3).
		Radius(design.RadiusMD).Background(t.BgRaised).Border(1, t.BorderL2)
	// The elevation is the shared floating-surface token (stronger in the dark
	// theme), not a bare shadow value of this module.
	box = design.Apply(box, t.ShadowOverlay())
	box.Children(func() {
		ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space1).Children(func() {
				if level.icon != nil {
					ui.Icon(c, level.icon).Size(design.Space3, design.Space3).TextColor(level.color)
				}
				design.Apply(ui.Text(c, level.label), t.Font(design.TypeXXXS)).
					FontWeight(600).TextColor(level.color)
				if count, ok := toast.Data.(int); ok && count > 1 {
					// One toast standing for N identical notifications: a quiet
					// count, not another badge row.
					design.Apply(ui.Text(c, "×"+strconv.Itoa(count)), t.Font(design.TypeXXXS)).
						FontWeight(600).TextColor(level.color)
				}
			})
			design.Apply(ui.Text(c, toast.Title), t.Font(design.TypeXS)).
				TextColor(t.LabelPrimary).MaxLines(2)
			if toast.Description != "" {
				design.Apply(ui.Text(c, toast.Description), t.Font(design.TypeXXS)).
					TextColor(t.LabelSecondary).MaxLines(2)
			}
			if toast.Action != "" {
				action := p.ActionButton().Padding(design.Space1, design.Space2).
					Radius(design.RadiusSM).Label(toast.Action)
				switch {
				case action.Pressed():
					action = action.Background(t.BgSelected)
				case action.Hovered():
					action = action.Background(t.BgHover)
				}
				action.Children(func() {
					design.Apply(ui.Text(c, toast.Action), t.Font(design.TypeXS)).
						FontWeight(600).TextColor(t.Accent)
				})
			}
		})
		closeLabel := st.tr("关闭通知", "Close notification")
		close := p.CloseButton().Size(toastCloseButtonSize, toastCloseButtonSize).
			Center().Radius(design.RadiusSM).Label(closeLabel).Tooltip(closeLabel)
		switch {
		case close.Pressed():
			close = close.Background(t.BgSelected)
		case close.Hovered():
			close = close.Background(t.BgHover)
		}
		close.Children(func() {
			ui.Icon(c, icons.Close()).Size(design.Space4, design.Space4).TextColor(t.LabelTertiary)
		})
	})
}

// tr localizes the toast chrome for the current frame.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

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
