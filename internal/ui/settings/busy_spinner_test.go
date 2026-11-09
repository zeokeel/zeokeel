package settings_test

// Shared assertion of the settings pages' async busy states: the official
// Spinner (an accessible progress indicator of unknown length, named by the
// action's label) is present while the work runs and the action button stays
// disabled as before.

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// requireBusySpinner asserts the busy action of one settings page: the official
// spinner is exposed as a progress indicator named label, and the action button
// showing that label is disabled. Repeating the action must be impossible.
func requireBusySpinner(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	progress, ok := tester.AccessByRole(ui.RoleProgress, label)
	if !ok {
		t.Fatalf("frame has no progress indicator named %q; texts = %v", label, tester.Texts())
	}
	// A spinner is progress of unknown length: its Now sits below Min.
	if progress.Now >= progress.Min {
		t.Fatalf("progress indicator %q reports a known value (%v of %v), want unknown length",
			label, progress.Now, progress.Min)
	}
	button, ok := tester.AccessByRole(ui.RoleButton, label)
	if !ok {
		t.Fatalf("busy action button %q missing; texts = %v", label, tester.Texts())
	}
	if !button.Disabled {
		t.Fatalf("busy action button %q is not disabled", label)
	}
}
