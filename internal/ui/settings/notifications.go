package settings

// Notification page: the desktop toast-overlay switch and the separate
// "system notifications" switch (mirroring notifications to the OS notification
// center). The toasts module reads the same ui.notifications row on every
// delivery/frame, so toggling applies without a restart; the notifier reads
// ui.notifications.system per notification. Toasts always show every level: the
// old per-level filter section is gone (the behavior is unconditional).
//
// The system-notification switch is disabled with an explanation on a platform
// where the host did not install an OS notification sink (mygo reports no
// support, or the product has no desktop notifier); the in-app toast switch
// stays usable there.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// NotificationsPageID is the notification-toast page identity.
	NotificationsPageID = "notifications"
	// NotificationsPageSection places it in the general group.
	NotificationsPageSection = "高级"
	// NotificationsPageOrder places it next to Update/Import (99).
	NotificationsPageOrder = 10
)

// notificationsSettingEnabled normalizes ui.notifications: "false", "0" and
// "no" disable the toast overlay; anything else (including an empty or
// malformed value) keeps it enabled.
func notificationsSettingEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "false", "0", "no":
		return false
	default:
		return true
	}
}

// systemNotificationsSupported reports whether the desktop host can mirror
// notifications to the OS. It reads the notifier's optional contracts.SystemNotifier
// half from the kernel context on every build, so a host that installs the sink
// later in startup updates the page. A notifier without that half (or no
// notifier at all) means the platform has no usable OS notification support.
func systemNotificationsSupported(ctx cordis.Context) bool {
	notifier, ok := ctx.Get(contracts.KeyNotifier)
	if !ok || notifier == nil {
		return false
	}
	system, ok := notifier.(contracts.SystemNotifier)
	return ok && system.SystemNotificationsSupported()
}

// notificationsState is the page's per-visit status line (UI thread only).
type notificationsState struct {
	status    string
	statusErr bool
}

// buildNotificationsPage renders the notification settings. Both switches apply
// at once and are independent: turning off the in-app toasts leaves OS
// notifications on, and vice versa.
func buildNotificationsPage(c *ui.Context, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService, st *notificationsState,
	systemSupported bool, search *pageSearch) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, "通知", "Notifications"), "")
		search.region(c, "notifications.toasts", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "通知气泡", "Notification toasts"), func(*ui.Context) {
				ui.Form(c, func() {
					enabled := notificationsSettingEnabled(store.Get(contracts.SettingNotifications, ""))
					if switchField(c,
						localized(locale, "显示通知气泡", "Show notification toasts"),
						"",
						&enabled) {
						if err := store.Set(contracts.SettingNotifications, fmt.Sprintf("%t", enabled)); err != nil {
							st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
							st.statusErr = true
						} else {
							st.status = localized(locale, "已保存", "Saved")
							st.statusErr = false
						}
						// Keep the stored value untouched on failure; the next
						// frame re-reads it and the description below stays accurate.
						runtime.Invalidate()
					}
				})
			})
		})
		search.region(c, "notifications.system", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "系统通知", "System notifications"), func(*ui.Context) {
				ui.Form(c, func() {
					enabled := contracts.SystemNotificationsEnabled(
						store.Get(contracts.SettingSystemNotifications, ""))
					description := localized(locale,
						"任务在后台完成或失败时显示系统通知；点击通知会打开对应会话，没有会话的通知只把窗口带到前台。应用内通知气泡照常显示。",
						"When work finishes or fails in the background, notify the system too. Clicking a notification opens its conversation; one without a conversation only brings the window to the front. In-app toasts still show.")
					if !systemSupported {
						description = localized(locale,
							"当前平台不支持系统通知；应用内通知气泡仍然可用。",
							"System notifications are not available on this platform; in-app toasts still work.")
					}
					if switchFieldDisabled(c,
						localized(locale, "显示系统通知", "Show system notifications"),
						description, &enabled, !systemSupported) {
						if err := store.Set(contracts.SettingSystemNotifications, fmt.Sprintf("%t", enabled)); err != nil {
							st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
							st.statusErr = true
						} else {
							st.status = localized(locale, "已保存", "Saved")
							st.statusErr = false
						}
						runtime.Invalidate()
					}
				})
			})
		})
		// One status line for both sections: the last save result, whether it
		// came from the toast switch or the system switch.
		statusBadgesRow(c, design.Of(c), st.status, st.statusErr)
	})
}

// switchFieldDisabled is switchField with an explicit disabled state: a
// capability switch (system notifications) whose platform support is missing
// shows the stored value but cannot change it, and its description says why.
func switchFieldDisabled(c *ui.Context, label, description string, value *bool, disabled bool) bool {
	var changed bool
	ui.Field(c, label, func() {
		sw := ui.Switch(c, value)
		if disabled {
			sw.Disabled(true)
		}
		changed = sw.Changed()
	}).Description(description)
	return changed
}
