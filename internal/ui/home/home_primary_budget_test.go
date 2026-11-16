package home_test

// Whole-shell frame test for UD-01 (2026-11 wave D): on first launch (zero
// conversations) the screen carries exactly ONE accent-filled primary "新对话"
// — the sidebar's. Home's empty-state next steps used to render a second
// filled primary of the same name, breaking DESIGN.md's ≤1 primary budget
// (AP-20); the Home action drops to the neutral outlined secondary while the
// sidebar keeps the one primary. The two modules' own tests each rendered in
// isolation and never asserted the budget in the same frame; this boots the
// real shell (sidebar + Home) and counts accent-filled primaries across it.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/home"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// buttonFill samples a button's fill near its left edge, away from its glyph
// and label (the sidebar newchat_style_test technique).
func buttonFill(tester *ui.Tester, rect ui.Rect) [4]uint8 {
	img := tester.Image()
	x := int(rect.X) + 6
	y := int(rect.Y + rect.H/2)
	r, g, b, a := img.At(x, y).RGBA()
	return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// TestFirstLaunchSingleAccentPrimary boots the shell with the sidebar and Home
// and zero conversations, then counts the "新对话" buttons painted with the
// accent primary fill: exactly one (the sidebar's), not two.
func TestFirstLaunchSingleAccentPrimary(t *testing.T) {
	h := newHarness(t, &fakeChat{}) // no conversations at all
	activate(t, h, sidebar.Module(), sidebar.Config{})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1280, 800)
	tester.Frame()
	if err := h.ui.Navigator().Push("home"); err != nil {
		t.Fatalf("push home: %v", err)
	}
	tester.Frame()
	tester.Frame()

	accent := design.Light().Accent
	want := [4]uint8{accent.R, accent.G, accent.B, accent.A}
	primaries := 0
	newChatButtons := 0
	for _, info := range tester.AccessAll() {
		if info.Role != ui.RoleButton || info.Name != "新对话" {
			continue
		}
		newChatButtons++
		if buttonFill(tester, info.Bounds) == want {
			primaries++
		}
	}
	if newChatButtons < 2 {
		t.Fatalf("expected the sidebar and Home both to offer 新对话 on first launch; found %d", newChatButtons)
	}
	if primaries != 1 {
		t.Fatalf("UD-01: first launch paints %d accent-filled 新对话 primaries, want exactly 1 (the sidebar's); Home's must be the outlined secondary",
			primaries)
	}
}
