// Package toasts contributes the transient notification overlay: the desktop
// notifier bridge hands notifications to this module, which renders them as
// native toast cards in the bottom-right corner of the window.
//
// It is a kernel UI module: activation requires the host UI runtime and the
// host Notifier, installs the notification handler (whose calls hop to the UI
// thread with UIRuntime.Post) and registers one overlay.toasts slot item. Each
// toast disappears on its own after toastTTL via a goroutine that posts the
// frame removing it, or immediately through its native close button. The module
// is only loaded by products with the desktop notifier bridge.
package toasts

import (
	"encoding/json"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/toasts"

// SlotID identifies this module's contribution to overlay.toasts.
const SlotID = "toasts.notifications"

// maxToasts bounds the visible stack; a newer toast replaces the oldest.
const maxToasts = 4

// toastTTL is how long one toast stays visible. Tests shorten it through the
// package variable (the module always reads the current value when queueing).
var toastTTL = 4 * time.Second

// Config is the module's typed assembly config (all copy is localized; the
// module has no configurable fields).
type Config struct{}

// toast is one queued notification with its internal key and expiry.
type toast struct {
	key  uint64
	data contracts.Notification
}

// state is the module instance's toast stack (UI thread only).
type state struct {
	toasts   []toast
	nextID   atomic.Uint64
	locale   contracts.LocaleService
	notifier contracts.Notifier
	// settings is the optional host settings store; ui.notifications and
	// ui.notifications.hidden are read on every delivery/frame so switches
	// apply without a restart.
	settings contracts.SettingsService
	// badHiddenRaw remembers the last malformed hidden-levels row already
	// logged, so the per-frame read cannot spam the log.
	badHiddenRaw string
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
		locale, _ := ctx.Get(contracts.KeyLocale)
		settings, _ := ctx.Get(contracts.KeySettings)
		st := &state{locale: locale, notifier: notifier, settings: settings}

		notifier.SetHandler(func(notification contracts.Notification) {
			runtime.Post(func() {
				enqueue(st, runtime, notification)
				runtime.Invalidate()
			})
		})
		if err := own(ctx, func() (func(), error) {
			return func() {
				notifier.SetHandler(nil)
				st.toasts = nil
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
				buildOverlay(c, design.Of(c), runtime, st)
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

// levelHidden reports whether one toast level is filtered out. Unknown levels
// and a missing settings store always show.
func levelHidden(st *state, level string) bool {
	hidden := hiddenLevels(st)
	if len(hidden) == 0 {
		return false
	}
	return hidden[notificationLevel(level)]
}

// enqueue adds one toast to the stack, keeping at most maxToasts (the oldest is
// replaced), and schedules its automatic expiry. The handler stays installed
// while notifications are disabled or a level is hidden but drops deliveries
// here (a later frame re-reads the settings, so the switches are hot).
func enqueue(st *state, runtime contracts.UIRuntime, notification contracts.Notification) {
	if strings.TrimSpace(notification.Text) == "" || !notificationsEnabled(st) ||
		levelHidden(st, notification.Level) {
		return
	}
	key := st.nextID.Add(1)
	st.toasts = append(st.toasts, toast{key: key, data: notification})
	if len(st.toasts) > maxToasts {
		st.toasts = st.toasts[len(st.toasts)-maxToasts:]
	}
	ttl := toastTTL
	go func() {
		time.Sleep(ttl)
		runtime.Post(func() {
			// Release the notifier's active entry as well; otherwise a long
			// session slowly accumulates expired notification ids.
			st.notifier.Dismiss(notification.ID)
			removeToast(st, key)
			runtime.Invalidate()
		})
	}()
}

// removeToast drops the toast identified by its internal key (no-op when it was
// already closed or replaced).
func removeToast(st *state, key uint64) {
	kept := st.toasts[:0]
	for _, item := range st.toasts {
		if item.key != key {
			kept = append(kept, item)
		}
	}
	st.toasts = kept
}

// buildOverlay renders the toast stack in the bottom-right corner.
func buildOverlay(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	// Hot switches: disabling notifications (or hiding a level) also drops the
	// toasts already on screen instead of leaving them until the next event.
	if !notificationsEnabled(st) {
		st.toasts = nil
		return
	}
	if hidden := hiddenLevels(st); len(hidden) > 0 {
		kept := st.toasts[:0]
		for _, item := range st.toasts {
			if hidden[notificationLevel(item.data.Level)] {
				continue
			}
			kept = append(kept, item)
		}
		st.toasts = kept
	}
	if len(st.toasts) == 0 {
		return
	}
	// Keep the stack clear of the composer card pinned to the bottom of the
	// conversation screen.
	ui.Column(c).FillWidth().Grow(1).Justify(ui.End).AlignItems(ui.End).
		Padding(design.Space4, design.Space4, 140, design.Space4).Gap(design.Space2).Children(func() {
		for _, item := range st.toasts {
			item := item
			buildToast(c, t, runtime, st, item)
		}
	})
}

// buildToast is one native toast panel: level badge, copy and a close action.
func buildToast(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, item toast) {
	notification := item.data
	level := strings.ToLower(strings.TrimSpace(notification.Level))
	label := uikit.T(st.locale, "提示", "Info")
	color := t.Accent
	switch level {
	case contracts.NotifySuccess:
		label = uikit.T(st.locale, "成功", "Success")
		color = t.Success
	case contracts.NotifyWarning:
		label = uikit.T(st.locale, "警告", "Warning")
		color = t.WarningText
	case contracts.NotifyError:
		label = uikit.T(st.locale, "错误", "Error")
		color = t.DangerText
	}
	panel := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space2, design.Space3).
		MinWidth(220).MaxWidth(360)
	panel.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
			design.Apply(ui.Badge(c, label), t.Badge()).TextColor(color)
			design.Apply(ui.Text(c, notification.Text), t.Font(design.TypeXS)).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(3)
			close := ui.Button(c, "×").Tooltip(uikit.T(st.locale, "关闭通知", "Close notification"))
			if close.Clicked() {
				removeToast(st, item.key)
				if st.notifier != nil {
					st.notifier.Dismiss(notification.ID)
				}
				runtime.Invalidate()
			}
		})
	})
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
