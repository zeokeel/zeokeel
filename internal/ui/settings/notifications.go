package settings

// Notification page: the desktop toast-overlay switch. The toasts module reads
// the same ui.notifications row on every delivery/frame, so toggling applies
// without a restart.

import (
	"encoding/json"
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

// notificationsState is the page's per-visit status line (UI thread only).
type notificationsState struct {
	status    string
	statusErr bool
}

// notificationLevelFilter is one row of the per-level filter switches.
type notificationLevelFilter struct {
	level string
	zh    string
	en    string
}

// notificationLevelFilters lists the filterable levels in canonical order.
var notificationLevelFilters = []notificationLevelFilter{
	{level: contracts.NotifyInfo, zh: "信息", en: "Info"},
	{level: contracts.NotifySuccess, zh: "成功", en: "Success"},
	{level: contracts.NotifyWarning, zh: "警告", en: "Warning"},
	{level: contracts.NotifyError, zh: "错误", en: "Error"},
}

// notificationHiddenLevels reads ui.notifications.hidden: a JSON array of the
// levels whose toasts are hidden. Missing/empty/malformed rows mean "nothing
// hidden" (bad values never filter notifications), and unknown levels are
// dropped so the page only writes levels the toasts module understands.
func notificationHiddenLevels(store contracts.SettingsService) map[string]bool {
	hidden := map[string]bool{}
	if store == nil {
		return hidden
	}
	raw := strings.TrimSpace(store.Get(contracts.SettingNotificationHiddenLevels, ""))
	if raw == "" {
		return hidden
	}
	var levels []string
	if err := json.Unmarshal([]byte(raw), &levels); err != nil {
		return hidden
	}
	for _, level := range levels {
		normalized := strings.ToLower(strings.TrimSpace(level))
		for _, filter := range notificationLevelFilters {
			if filter.level == normalized {
				hidden[normalized] = true
			}
		}
	}
	return hidden
}

// encodeNotificationHiddenLevels renders the hidden-level list in canonical
// order (an empty list is stored as "[]").
func encodeNotificationHiddenLevels(hidden map[string]bool) string {
	levels := make([]string, 0, len(hidden))
	for _, filter := range notificationLevelFilters {
		if hidden[filter.level] {
			levels = append(levels, filter.level)
		}
	}
	encoded, err := json.Marshal(levels)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// setNotificationLevelHidden toggles one level: hidden levels are appended to
// the stored list, visible ones removed. Writes are normalized JSON.
func setNotificationLevelHidden(store contracts.SettingsService, level string, hidden bool) error {
	current := notificationHiddenLevels(store)
	if hidden {
		current[level] = true
	} else {
		delete(current, level)
	}
	return store.Set(contracts.SettingNotificationHiddenLevels, encodeNotificationHiddenLevels(current))
}

// buildNotificationsPage renders the notification settings.
func buildNotificationsPage(c *ui.Context, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService, st *notificationsState) {
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
		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "按级别过滤", "Filter by level"))
			design.Apply(ui.Text(c, localized(locale,
				"按级别过滤通知气泡；关闭的级别不再显示（总开关关闭时全部隐藏）。",
				"Filter toast bubbles by level; disabled levels are hidden (the master switch still hides everything).")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
			hidden := notificationHiddenLevels(store)
			for _, filter := range notificationLevelFilters {
				filter := filter
				shown := !hidden[filter.level]
				toggle := ui.Toggle(c, &shown, localized(locale, filter.zh, filter.en))
				if toggle.Changed() {
					if err := setNotificationLevelHidden(store, filter.level, !shown); err != nil {
						st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
						st.statusErr = true
						runtime.Invalidate()
						continue
					}
					st.status = localized(locale, "已保存", "Saved")
					st.statusErr = false
					runtime.Invalidate()
				}
			}
			if st.status != "" {
				statusBadge(c, t, st.status, st.statusErr)
			}
		})
	})
}
