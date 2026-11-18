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
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.SpaceScreen).Children(func() {
		uikit.PageHeader(c, localized(locale, "通知", "Notifications"), "")
		search.region(c, "notifications.toasts", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "通知气泡", "Notification toasts"), func(*ui.Context) {
				enabled := notificationsSettingEnabled(store.Get(contracts.SettingNotifications, ""))
				if uikit.SwitchRow(c, uikit.SwitchRowOptions{
					Label: localized(locale, "显示通知气泡", "Show notification toasts"),
					Value: &enabled,
				}) {
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
		search.region(c, "notifications.system", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "系统通知", "System notifications"), func(*ui.Context) {
				enabled := contracts.SystemNotificationsEnabled(
					store.Get(contracts.SettingSystemNotifications, ""))
				description := localized(locale,
					"任务在后台完成或失败时显示系统通知；点击通知会打开对应会话，没有会话的通知只把窗口带到前台。应用内通知气泡照常显示。",
					"When work finishes or fails in the background, notify the system too. Clicking a notification opens its conversation; one without a conversation only brings the window to the front. In-app toasts still show.")
				if !systemSupported {
					// A platform with no OS notification sink gets a read-only state
					// row, not a switch: mygo's switch keeps its accent fill when
					// disabled, so a stored "on" painted an available-looking ON
					// toggle directly above a sentence saying the platform has no
					// support (2026-11-18 review). The stored value is left as it is.
					ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
							// Same row anatomy as uikit.SwitchRow (caption at the
							// section edge, state at the trailing edge, description
							// below), with a status word where the switch would be.
							design.Apply(ui.Text(c, localized(locale, "显示系统通知", "Show system notifications")),
								t.Font(design.TypeXS)).TextColor(t.LabelPrimary).Grow(1)
							uikit.StatusText(c, t, design.TypeXS, uikit.StatusIdle,
								localized(locale, "不可用", "Unavailable"))
						})
						design.Apply(ui.Text(c, localized(locale,
							"当前平台不支持系统通知；应用内通知气泡仍然可用。",
							"System notifications are not available on this platform; in-app toasts still work.")),
							t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
					})
					return
				}
				// Reached only when the host has an OS notification sink (the
				// unsupported platform returned above with a read-only row).
				if uikit.SwitchRow(c, uikit.SwitchRowOptions{
					Label:       localized(locale, "显示系统通知", "Show system notifications"),
					Description: description,
					Value:       &enabled,
				}) {
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
		// One status line for both sections: the last save result, whether it
		// came from the toast switch or the system switch.
		statusBadgesRow(c, t, st.status, st.statusErr)
	})
}
