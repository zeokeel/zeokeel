package regression

// Adversarial audit: toast level filtering (ui.notifications.hidden). Negative
// and boundary cases: missing/empty/malformed rows never filter, known levels are
// filtered case-insensitively, unknown levels always show, and hiding a level is a
// hot switch that clears the already-visible toasts of that level.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// setHiddenLevels writes the hidden-levels JSON row.
func setHiddenLevels(t *testing.T, harness *toastsHarness, raw string) {
	t.Helper()
	if err := harness.h.settings.Set(contracts.SettingNotificationHiddenLevels, raw); err != nil {
		t.Fatalf("set hidden levels %q: %v", raw, err)
	}
}

// TestToastLevelFiltering covers the filter matrix through the frame.
func TestToastLevelFiltering(t *testing.T) {
	harness := startToastsUI(t)

	// No hidden row: every level shows.
	for _, level := range []string{contracts.NotifyInfo, contracts.NotifySuccess, contracts.NotifyWarning, contracts.NotifyError} {
		harness.raise(t, "notice-all-"+level, level, "toast-all-"+level)
	}
	harness.tester.Frame()
	for _, level := range []string{"info", "success", "warning", "error"} {
		requireHasText(t, harness.tester, "toast-all-"+level)
	}

	// Hide info: only info disappears; the other levels keep showing.
	setHiddenLevels(t, harness, `["info"]`)
	harness.raise(t, "notice-hidden-info", contracts.NotifyInfo, "toast-hidden-info")
	harness.raise(t, "notice-shown-success", contracts.NotifySuccess, "toast-shown-success")
	harness.tester.Frame()
	requireNoText(t, harness.tester, "toast-hidden-info")
	requireHasText(t, harness.tester, "toast-shown-success")

	// Case/whitespace variants normalize; unknown levels are never filtered and
	// unknown names in the row are ignored.
	setHiddenLevels(t, harness, `[" INFO ", "Warning"]`)
	harness.raise(t, "notice-info-2", contracts.NotifyInfo, "toast-info-2")
	harness.raise(t, "notice-warning-2", contracts.NotifyWarning, "toast-warning-2")
	harness.raise(t, "notice-custom", "custom-level", "toast-custom")
	harness.raise(t, "notice-error-2", contracts.NotifyError, "toast-error-2")
	harness.tester.Frame()
	requireNoText(t, harness.tester, "toast-info-2")
	requireNoText(t, harness.tester, "toast-warning-2")
	requireHasText(t, harness.tester, "toast-custom")
	requireHasText(t, harness.tester, "toast-error-2")

	// Malformed JSON is treated as "nothing hidden" (bad values never filter).
	setHiddenLevels(t, harness, `["info"`)
	harness.raise(t, "notice-malformed", contracts.NotifyInfo, "toast-malformed")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-malformed")

	// An empty array hides nothing.
	setHiddenLevels(t, harness, `[]`)
	harness.raise(t, "notice-empty-array", contracts.NotifyWarning, "toast-empty-array")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-empty-array")
}

// TestToastHiddenLevelHotClear covers the hot switch: hiding a level removes the
// matching toasts already on screen and keeps the other levels; un-hiding shows
// the next delivery again.
func TestToastHiddenLevelHotClear(t *testing.T) {
	harness := startToastsUI(t)
	harness.raise(t, "notice-i", contracts.NotifyInfo, "visible-info")
	harness.raise(t, "notice-e", contracts.NotifyError, "visible-error")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "visible-info")
	requireHasText(t, harness.tester, "visible-error")

	setHiddenLevels(t, harness, `["info"]`)
	harness.tester.Frame()
	requireNoText(t, harness.tester, "visible-info")
	requireHasText(t, harness.tester, "visible-error")

	harness.raise(t, "notice-i2", contracts.NotifyInfo, "info-while-hidden")
	harness.tester.Frame()
	requireNoText(t, harness.tester, "info-while-hidden")

	// Un-hiding shows the next delivery.
	setHiddenLevels(t, harness, `[]`)
	harness.raise(t, "notice-i3", contracts.NotifyInfo, "info-back")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "info-back")

	// Hiding error drops the error toast too; the info toast stays.
	harness.raise(t, "notice-i4", contracts.NotifyInfo, "info-kept")
	harness.tester.Frame()
	setHiddenLevels(t, harness, `["error"]`)
	harness.tester.Frame()
	requireNoText(t, harness.tester, "visible-error")
	requireHasText(t, harness.tester, "info-kept")
	if strings.TrimSpace(harness.h.settings.Get(contracts.SettingNotificationHiddenLevels, "")) != `["error"]` {
		t.Fatalf("hidden levels row was rewritten: %q", harness.h.settings.Get(contracts.SettingNotificationHiddenLevels, ""))
	}
}
