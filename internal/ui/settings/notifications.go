package settings

// Notification page: the desktop toast-overlay switch. The toasts module reads
// the same ui.notifications row on every delivery/frame, so toggling applies
// without a restart. Toasts always show every level: the old per-level filter
// section is gone (the behavior is unconditional), so the master switch is the
// page's only control.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

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

// notificationsState is the page's per-visit status line (UI thread only).
type notificationsState struct {
	status    string
	statusErr bool
}

// buildNotificationsPage renders the notification settings. The master switch
// applies at once; toasts always show every level, so there is no per-level
// filter any more.
func buildNotificationsPage(c *ui.Context, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService, st *notificationsState) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, "通知", "Notifications"), "")
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
			statusBadgesRow(c, design.Of(c), st.status, st.statusErr)
		})
	})
}
