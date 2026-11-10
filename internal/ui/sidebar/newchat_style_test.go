package sidebar_test

// Frame tests for the accent budget of the primary action (V2): "new chat" is
// the filled primary only on the landing/empty state; with an active session it
// drops to the neutral outlined secondary so a chosen row and a filled accent
// button never sit next to each other.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

func TestNewChatSecondaryWhenSessionSelected(t *testing.T) {
	h := newHarness(t, &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "项目讨论"}},
		active:   "s1",
	}, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	button, ok := tester.AccessByRole(ui.RoleButton, "新对话")
	if !ok {
		t.Fatalf("new-chat button is not in the frame; texts = %v", tester.Texts())
	}
	if got, want := sampleButtonFill(tester, button.Bounds), design.Light().Accent; got == [4]uint8{want.R, want.G, want.B, want.A} {
		t.Fatalf("new-chat button with an active session is still the accent fill (#%02x%02x%02x)",
			got[0], got[1], got[2])
	}
}

func TestNewChatPrimaryOnLanding(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	button, ok := tester.AccessByRole(ui.RoleButton, "新对话")
	if !ok {
		t.Fatalf("new-chat button is not in the frame; texts = %v", tester.Texts())
	}
	fill := sampleButtonFill(tester, button.Bounds)
	accent := design.Light().Accent
	if fill != [4]uint8{accent.R, accent.G, accent.B, accent.A} {
		t.Fatalf("landing new-chat fill = #%02x%02x%02x, want the accent fill",
			fill[0], fill[1], fill[2])
	}
}

// sampleButtonFill returns the fill near a button's left edge, away from its
// centered glyph and label.
func sampleButtonFill(tester *ui.Tester, rect ui.Rect) [4]uint8 {
	img := tester.Image()
	x := int(rect.X) + 6
	y := int(rect.Y + rect.H/2)
	r, g, b, a := img.At(x, y).RGBA()
	return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}
