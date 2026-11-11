package regression

// Adversarial audit (A7 fixed behavior): toast levels are never filtered. The
// old ui.notifications.hidden row is no longer read, so a stored filter from an
// older version — well-formed, case/whitespace variants, unknown names or
// malformed JSON — cannot hide a toast. Only the master ui.notifications switch
// still turns the overlay off (covered by notifications_test.go).

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// setHiddenLevels writes the legacy hidden-levels JSON row. The module no longer
// reads it; the tests keep writing it to prove it is ignored.
func setHiddenLevels(t *testing.T, harness *toastsHarness, raw string) {
	t.Helper()
	if err := harness.h.settings.Set("ui.notifications.hidden", raw); err != nil {
		t.Fatalf("set hidden levels %q: %v", raw, err)
	}
}

// TestToastLevelsNeverFiltered covers the legacy filter matrix through the
// frame: every level is delivered whatever the stored row says.
func TestToastLevelsNeverFiltered(t *testing.T) {
	harness := startToastsUI(t)
	for _, row := range []string{`["info"]`, `[" INFO ", "Warning"]`, `["info"`, `[]`, `["error"]`} {
		setHiddenLevels(t, harness, row)
		for _, level := range []string{contracts.NotifyInfo, contracts.NotifySuccess, contracts.NotifyWarning, contracts.NotifyError} {
			text := "toast-" + level
			harness.raise(t, "notice-"+level, level, text)
			harness.tester.Frame()
			requireHasText(t, harness.tester, text)
			flushToasts(harness)
			requireNoText(t, harness.tester, text)
		}
	}

	// A row that names every level still filters nothing: the four deliveries
	// show in order, two at a time (the stack bound is separate from filtering).
	setHiddenLevels(t, harness, `["info","success","warning","error"]`)
	for _, level := range []string{contracts.NotifyInfo, contracts.NotifySuccess, contracts.NotifyWarning, contracts.NotifyError} {
		harness.raise(t, "notice-ordered-"+level, level, "toast-ordered-"+level)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-ordered-info")
	requireHasText(t, harness.tester, "toast-ordered-success")
	requireNoText(t, harness.tester, "toast-ordered-warning")

	harness.tester.Advance(5 * time.Second)
	harness.tester.Frame()
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-ordered-warning")
	requireHasText(t, harness.tester, "toast-ordered-error")
	flushToasts(harness)
}

// TestToastLevelRowChangeNeverClears covers the former hot switch: writing the
// legacy row while toasts show neither closes them nor suppresses the next
// delivery, and the row is never rewritten by the module.
func TestToastLevelRowChangeNeverClears(t *testing.T) {
	harness := startToastsUI(t)
	harness.raise(t, "notice-i", contracts.NotifyInfo, "visible-info")
	harness.raise(t, "notice-e", contracts.NotifyError, "visible-error")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "visible-info")
	requireHasText(t, harness.tester, "visible-error")

	setHiddenLevels(t, harness, `["info"]`)
	harness.tester.Frame()
	requireHasText(t, harness.tester, "visible-info")
	requireHasText(t, harness.tester, "visible-error")

	// The next delivery is shown too; the two old toasts expire normally.
	harness.raise(t, "notice-i2", contracts.NotifyInfo, "info-after-row")
	harness.tester.Advance(5 * time.Second)
	harness.tester.Frame()
	harness.tester.Frame()
	requireHasText(t, harness.tester, "info-after-row")
	requireNoText(t, harness.tester, "visible-info")
	requireNoText(t, harness.tester, "visible-error")

	// Case/whitespace variants and unknown level names in the row are ignored
	// as well, and the module leaves the stored row untouched.
	setHiddenLevels(t, harness, `[" SUCCESS ", "custom-level"]`)
	flushToasts(harness)
	harness.raise(t, "notice-case", "Success", "toast-case")
	harness.raise(t, "notice-custom", "custom-level", "toast-custom")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-case")
	requireHasText(t, harness.tester, "toast-custom")
	if got := strings.TrimSpace(harness.h.settings.Get("ui.notifications.hidden", "")); got != `[" SUCCESS ", "custom-level"]` {
		t.Fatalf("hidden levels row was rewritten: %q", got)
	}
}
