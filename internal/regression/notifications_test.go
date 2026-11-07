package regression

// Adversarial audit: the desktop toast switch (ui.notifications). The toasts
// module normalizes the row (only false/0/no disable; missing/malformed values
// keep toasts on), re-reads it per delivery and per frame (hot switch clears the
// visible stack), and the settings 通知 page writes it.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// setNotifications writes the ui.notifications row on the harness settings.
func setNotifications(t *testing.T, harness *toastsHarness, value string) {
	t.Helper()
	if err := harness.h.settings.Set(contracts.SettingNotifications, value); err != nil {
		t.Fatalf("set %s=%q: %v", contracts.SettingNotifications, value, err)
	}
}

// TestNotificationsNormalizationBehavior covers the normalization matrix through
// user-visible behavior: false/0/no (any case/whitespace) suppress a toast;
// empty/missing/unknown values keep toasts on.
func TestNotificationsNormalizationBehavior(t *testing.T) {
	harness := startToastsUI(t)
	// Missing row: enabled by default.
	harness.raise(t, "notice-default", contracts.NotifyInfo, "toast-default")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-default")

	offValues := []string{"false", "FALSE", " False ", "0", " 0 ", "no", "NO", " no\n"}
	for index, value := range offValues {
		setNotifications(t, harness, value)
		text := "toast-off-" + itoa(int64(index))
		harness.raise(t, "notice-off-"+itoa(int64(index)), contracts.NotifyInfo, text)
		harness.tester.Frame()
		requireNoText(t, harness.tester, text)
	}

	onValues := []string{"", "true", "1", "yes", "banana", "2", "off", "disabled"}
	for index, value := range onValues {
		setNotifications(t, harness, value)
		text := "toast-on-" + itoa(int64(index))
		harness.raise(t, "notice-on-"+itoa(int64(index)), contracts.NotifyInfo, text)
		harness.tester.Frame()
		requireHasText(t, harness.tester, text)
	}
}

// TestNotificationsHotToggleClearsStack covers the hot switch: disabling drops
// toasts already on screen, deliveries while disabled are ignored, and re-enabling
// shows the next delivery without a restart.
func TestNotificationsHotToggleClearsStack(t *testing.T) {
	harness := startToastsUI(t)
	harness.raise(t, "notice-a", contracts.NotifyInfo, "visible-a")
	harness.raise(t, "notice-b", contracts.NotifyInfo, "visible-b")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "visible-a")
	requireHasText(t, harness.tester, "visible-b")
	if closeButtons(harness.tester) != 2 {
		t.Fatalf("close buttons before disabling = %d, want 2", closeButtons(harness.tester))
	}

	setNotifications(t, harness, "false")
	harness.tester.Frame()
	requireNoText(t, harness.tester, "visible-a")
	requireNoText(t, harness.tester, "visible-b")
	if got := closeButtons(harness.tester); got != 0 {
		t.Fatalf("close buttons after disabling = %d, want 0 (stack cleared)", got)
	}

	harness.raise(t, "notice-c", contracts.NotifyInfo, "while-off")
	harness.tester.Frame()
	requireNoText(t, harness.tester, "while-off")

	setNotifications(t, harness, "true")
	harness.raise(t, "notice-d", contracts.NotifyInfo, "back-on")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "back-on")
	requireNoText(t, harness.tester, "while-off")
}

// TestNotificationsSettingsPageToggle covers the 通知 settings page: the toggle
// writes the row ("false" off / "true" on) and a malformed stored value starts
// enabled, then the first toggle write normalizes it.
func TestNotificationsSettingsPageToggle(t *testing.T) {
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 800)
	tester.Frame()
	if err := tester.Click("通知"); err != nil {
		t.Fatalf("open notifications page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "显示通知气泡")

	// Default off row: the page renders the switch on; the first toggle writes false.
	if err := tester.Click("显示通知气泡"); err != nil {
		t.Fatalf("toggle notifications: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := h.settings.Get(contracts.SettingNotifications, ""); got != "false" {
		t.Fatalf("stored notifications after toggling off = %q, want false", got)
	}
	if err := tester.Click("显示通知气泡"); err != nil {
		t.Fatalf("toggle notifications back: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := h.settings.Get(contracts.SettingNotifications, ""); got != "true" {
		t.Fatalf("stored notifications after toggling on = %q, want true", got)
	}

	// A malformed stored value starts enabled and the first toggle fixes it.
	if err := h.settings.Set(contracts.SettingNotifications, "banana"); err != nil {
		t.Fatalf("set malformed notifications: %v", err)
	}
	tester.Frame()
	if err := tester.Click("显示通知气泡"); err != nil {
		t.Fatalf("toggle malformed notifications: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := h.settings.Get(contracts.SettingNotifications, ""); got != "false" {
		t.Fatalf("malformed notifications after toggle = %q, want false", got)
	}
}
