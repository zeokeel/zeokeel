package sidebar_test

// Frame tests for the sidebar's "new session" row (B-spec §3.1): a 32 DIP
// ghost row with the Plus glyph, the label and the platform's Cmd/Ctrl+N key
// cap — never a filled primary, so the row can sit above the source list
// without spending the screen's accent budget.

import (
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func TestNewChatRowIsGhostNotFilled(t *testing.T) {
	// Both states — landing and with an active session — are the same quiet
	// row: no accent fill ever, because the window's one filled primary is the
	// composer's send.
	for _, tc := range []struct {
		name string
		chat *fakeChat
	}{
		{"landing", &fakeChat{}},
		{"active session", &fakeChat{
			sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "项目讨论"}},
			active:   "s1",
		}},
	} {
		h := newHarness(t, tc.chat, sidebar.Config{})
		tester := h.tester()
		tester.Frame()

		button, ok := tester.AccessByRole(ui.RoleButton, "新建会话")
		if !ok {
			t.Fatalf("%s: new-session row is not in the frame; texts = %v", tc.name, tester.Texts())
		}
		accent := design.Light().Accent
		if got := sampleButtonFill(tester, button.Bounds); got == [4]uint8{accent.R, accent.G, accent.B, accent.A} {
			t.Fatalf("%s: the row paints the accent fill (#%02x%02x%02x); it is a ghost row",
				tc.name, got[0], got[1], got[2])
		}
		if button.Bounds.H != design.RowCompactHeight {
			t.Fatalf("%s: row height = %.0f, want the 32 DIP compact row", tc.name, button.Bounds.H)
		}
	}
}

func TestNewChatRowHoverTints(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	button, ok := tester.AccessByRole(ui.RoleButton, "新建会话")
	if !ok {
		t.Fatalf("new-session row is not in the frame; texts = %v", tester.Texts())
	}
	rest := sampleButtonFill(tester, button.Bounds)
	tester.Move(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
	// The tint fades over design.MotionFast: hover registers on the frame after
	// the pointer moves, then the colour animates, so the sample waits out the
	// transition before reading the settled fill.
	tester.Frame()
	tester.Advance(design.MotionFast + 40*time.Millisecond)
	tester.Frame()
	hovered := sampleButtonFill(tester, button.Bounds)
	if rest == hovered {
		t.Fatalf("hover did not tint the row: fill stayed #%02x%02x%02x", rest[0], rest[1], rest[2])
	}
	// The tint is the product's neutral hover over the sidebar surface, not a
	// brand wash: the painted pixel is that overlay composited on BgSidebar.
	tk := design.Light()
	want := blendOver(tk.BgHover, tk.BgSidebar)
	img := tester.Image()
	r, g, b, _ := img.At(int(button.Bounds.X)+6, int(button.Bounds.Y+button.Bounds.H/2)).RGBA()
	got := [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
	if got != want {
		t.Fatalf("hovered row fill = #%02x%02x%02x, want BgHover over BgSidebar #%02x%02x%02x",
			got[0], got[1], got[2], want[0], want[1], want[2])
	}
}

func TestNewChatRowClickStartsSession(t *testing.T) {
	chat := &fakeChat{}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	if err := tester.Click("新建会话"); err != nil {
		t.Fatalf("click new-session row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	// The single click creates exactly one session (A4-I-04: a double click used
	// to make two).
	if got := chat.createdSessions(); len(got) != 1 {
		t.Fatalf("clicking the row created %d sessions, want exactly 1", len(got))
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen after the click = %q, want session", current)
	}
}

func TestNewChatRowActivatesFromKeyboard(t *testing.T) {
	chat := &fakeChat{}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	button, ok := tester.AccessByRole(ui.RoleButton, "新建会话")
	if !ok {
		t.Fatalf("new-session row is not in the frame; texts = %v", tester.Texts())
	}
	tester.ClickAt(button.Bounds.X+2, button.Bounds.Y+button.Bounds.H/2)
	tester.Frame()
	// The row is a button, so Enter activates it as a click would.
	if !tester.Focused("新建会话") {
		tester.Key(0, ui.KeyTab)
		tester.Frame()
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := chat.createdSessions(); len(got) == 0 {
		t.Fatalf("Enter on the focused row created no session; texts = %v", tester.Texts())
	}
}

func TestNewChatRowShowsThePlatformChord(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	want := uikit.FormatShortcut("CmdOrCtrl+N")
	if runtime.GOOS == "darwin" {
		if want != "Cmd+N" {
			t.Fatalf("FormatShortcut on darwin = %q, want Cmd+N", want)
		}
	} else if want != "Ctrl+N" {
		t.Fatalf("FormatShortcut = %q, want Ctrl+N", want)
	}
	if !tester.HasText(want) {
		t.Fatalf("the row's key cap %q is not in the frame; texts = %v", want, tester.Texts())
	}
}

// blendOver composites a translucent token over an opaque one, the way the
// renderer paints a hover tint on the sidebar surface.
func blendOver(top, bottom ui.Color) [3]uint8 {
	a := float32(top.A) / 255
	mix := func(t, b uint8) uint8 {
		return uint8(float32(t)*a + float32(b)*(1-a) + 0.5)
	}
	return [3]uint8{mix(top.R, bottom.R), mix(top.G, bottom.G), mix(top.B, bottom.B)}
}

// sampleButtonFill returns the fill near a row's left edge, away from its glyph
// and label.
func sampleButtonFill(tester *ui.Tester, rect ui.Rect) [4]uint8 {
	img := tester.Image()
	x := int(rect.X) + 6
	y := int(rect.Y + rect.H/2)
	r, g, b, a := img.At(x, y).RGBA()
	return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}
