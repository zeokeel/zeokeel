package uikit_test

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// TestOnAccentBadgeRendersItsLabel locks the selected-row badge helper: the
// label is mandatory copy (DESIGN.md §5.2: status is never carried by color
// alone), so the inverted pill must still render it as text the user reads.
func TestOnAccentBadgeRendersItsLabel(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.OnAccentBadge(c, "运行中", design.Of(c))
	}, 240, 120)
	if !tester.HasText("运行中") {
		t.Fatalf("frame does not show the badge label; texts = %v", tester.Texts())
	}
}
