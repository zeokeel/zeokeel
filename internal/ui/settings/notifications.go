package settings

// Notification page: the desktop toast-overlay switch. The toasts module reads
// the same ui.notifications row on every delivery/frame, so toggling applies
// without a restart.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

const (
	// NotificationsPageID is the notification-toast page identity.
	NotificationsPageID = "notifications"
	// NotificationsPageSection places it in the general group.
	NotificationsPageSection = "general"
	// NotificationsPageOrder places it next to Update/Import (99).
	NotificationsPageOrder = 99
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

// buildNotificationsPage renders the notification settings.
func buildNotificationsPage(c *ui.Context, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, localized(locale, "通知", "Notifications"), localized(locale,
			"桌面通知与提示气泡。",
			"Desktop notifications and toast bubbles."))
		panel(c, t, func() {
			enabled := notificationsSettingEnabled(store.Get(contracts.SettingNotifications, ""))
			toggle := ui.Toggle(c, &enabled,
				localized(locale, "显示通知气泡", "Show notification toasts"))
			if toggle.Changed() {
				if err := store.Set(contracts.SettingNotifications, fmt.Sprintf("%t", enabled)); err != nil {
					// Keep the stored value untouched on failure; the next frame
					// re-reads it and the description below stays accurate.
					runtime.Invalidate()
					return
				}
				runtime.Invalidate()
			}
			design.Apply(ui.Text(c, localized(locale,
				"关闭后导出、更新等提示不再以气泡显示，仍可在界面内查看状态。",
				"When off, export and update notices stay in the app instead of showing toasts")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})
	})
}
