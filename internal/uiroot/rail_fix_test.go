package uiroot

// Frame tests for the rail conversation popover's audit fixes (2026-11 wave D):
//
//   - UD-03: the running dot wears the §5.2 Warning color, not Success green
//     (a green dot reads as "finished" while the run is still streaming).
//   - AR-04: the row buttons carry a stable per-session identity, so a reorder
//     while the popover is open (a run starts, a new message bumps UpdatedAt)
//     keeps the keyboard focus on the user's session and Enter opens it.
//   - UD-18: the registered rich row paints its Pressed state (the selection
//     tint, the product's one pressed vocabulary), not only Hovered.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// railRowRect finds one conversation row of the open popover by its accessible
// name and returns its bounds.
func railRowRect(t *testing.T, tt *ui.Tester, name string) ui.Rect {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleButton, name)
	if !ok {
		t.Fatalf("conversation row %q missing; texts = %v", name, tt.Texts())
	}
	return info.Bounds
}

// TestRailRunningDotUsesWarning is UD-03: DESIGN.md §5.2 maps running→Warning.
// The dot is the trailing 8×8 box of the row; sample a small grid near the
// row's right edge and require the Warning fill, never the Success green.
func TestRailRunningDotUsesWarning(t *testing.T) {
	chat := &railChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "运行会话", Running: true},
	}}
	r := newRailTestRoot(t, chat)
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	openRailSessions(t, r, tt)
	row := railRowRect(t, tt, "运行会话 · 运行中")

	tokens := design.Light()
	foundSuccess, foundWarning := false, false
	for dx := 4; dx <= 24; dx += 2 {
		for dy := -6; dy <= 6; dy += 2 {
			x := int(row.X+row.W) - dx
			y := int(row.Y+row.H/2) + dy
			px := tt.Image().RGBAAt(x, y)
			got := ui.RGB(px.R, px.G, px.B)
			if got == tokens.Success {
				foundSuccess = true
			}
			if got == tokens.Warning {
				foundWarning = true
			}
		}
	}
	if foundSuccess {
		t.Fatalf("UD-03: the rail running dot is painted Success green (%v); §5.2 maps running→Warning", tokens.Success)
	}
	if !foundWarning {
		t.Fatalf("UD-03: no Warning dot pixel found near the row's trailing edge (row=%+v)", row)
	}
}

// focusRailRow tabs until the named conversation row holds the keyboard focus.
func focusRailRow(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	for i := 0; i < 16; i++ {
		if tt.Focused(name) {
			return
		}
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	t.Fatalf("never focused row %q; texts = %v", name, tt.Texts())
}

// TestRailRowEnterOpensFocusedSessionAfterReorder is AR-04: with the popover
// open, focus the second conversation, then reorder the list (the chat service
// returns a new order, as a starting run or a new message does). The focus must
// follow the session identity, and Enter must open the session the user focused
// — not the one that moved into its old position.
func TestRailRowEnterOpensFocusedSessionAfterReorder(t *testing.T) {
	chat := &railChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "写测试"},
		{ID: "s2", Title: "跑发布"},
		{ID: "s3", Title: "看日志"},
	}}
	r := newRailTestRoot(t, chat)
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	openRailSessions(t, r, tt)
	focusRailRow(t, tt, "跑发布")

	// s3 becomes the newest (a run started / a message arrived): the service
	// order changes while the popover stays open.
	chat.sessions = []contracts.ChatSessionInfo{
		{ID: "s3", Title: "看日志"},
		{ID: "s1", Title: "写测试"},
		{ID: "s2", Title: "跑发布"},
	}
	tt.Frame()
	tt.Frame()

	if !tt.Focused("跑发布") {
		t.Fatalf("AR-04: after the reorder the focus left the user's session (focused 写测试=%v 看日志=%v)",
			tt.Focused("写测试"), tt.Focused("看日志"))
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if len(chat.setCalls) != 1 || chat.setCalls[0] != "s2" {
		t.Fatalf("AR-04: Enter opened %v, want the focused session [s2]", chat.setCalls)
	}
}

// TestRailRowPressedShowsSelectionTint is UD-18: pressing a row paints the
// product's pressed vocabulary (the BgSelected selection tint, as rowTint and
// IconButton use), so the press is visible before the menu/open happens.
func TestRailRowPressedShowsSelectionTint(t *testing.T) {
	chat := &railChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "写测试"},
	}}
	r := newRailTestRoot(t, chat)
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	openRailSessions(t, r, tt)
	row := railRowRect(t, tt, "写测试")

	// A point inside the row's leading padding: no text or dot covers it, so
	// the pixel is the row's own fill over the popover surface.
	x, y := int(row.X)+6, int(row.Y+row.H/2)
	rest := tt.Image().RGBAAt(x, y)
	resting := ui.RGB(rest.R, rest.G, rest.B)

	tt.Press(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	pressed := tt.Image().RGBAAt(x, y)
	got := ui.RGB(pressed.R, pressed.G, pressed.B)
	want := over(design.Light().BgSelected, resting)
	tt.Release(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()

	if got == resting {
		t.Fatalf("UD-18: the pressed row keeps its resting fill %v; the press has no visual state", resting)
	}
	if got != want {
		t.Fatalf("UD-18: pressed fill = %v, want the selection tint %v over %v", got, want, resting)
	}
}
