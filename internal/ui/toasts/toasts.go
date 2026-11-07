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
		st := &state{locale: locale, notifier: notifier}

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

// enqueue adds one toast to the stack, keeping at most maxToasts (the oldest is
// replaced), and schedules its automatic expiry.
func enqueue(st *state, runtime contracts.UIRuntime, notification contracts.Notification) {
	if strings.TrimSpace(notification.Text) == "" {
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
