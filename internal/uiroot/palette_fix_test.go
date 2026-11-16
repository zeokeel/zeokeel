package uiroot

// Frame tests for the command palette's audit fixes (2026-11 wave D):
//
//   - UX-14: the footer key legend is instruction copy, so it takes the
//     TypeXXS metadata step (18 DIP line), not TypeXXXS's 14 DIP line
//     (DESIGN.md §5.3 forbids 11 DIP sentences; the no-match hint already
//     follows the rule, the footer legend was missed).
//   - UX-20: the palette's navigation group excludes Home, aligned with the
//     sidebar's intentional no-Home destination (TestSidebarHasNoHomeDestination):
//     the product's one landing surface is the conversation, reached through
//     the new-chat action, not a second "前往 首页" entry (lead ruling).

import (
	"testing"
)

// TestPaletteFooterHintUsesMetadataType covers UX-14: with matches listed, the
// palette's bottom legend "↑↓ 选择 · Enter 运行 · Esc 关闭" is a sentence of
// instruction copy and must render on the 18 DIP metadata line.
func TestPaletteFooterHintUsesMetadataType(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "session-body", 20))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()

	const legend = "↑↓ 选择 · Enter 运行 · Esc 关闭"
	rect, ok := tt.Find(legend)
	if !ok {
		t.Fatalf("the palette footer legend is missing; texts = %v", tt.Texts())
	}
	if rect.H < 18 {
		t.Fatalf("UX-14: the footer legend is %.1f DIP tall, want the 18 DIP metadata line (TypeXXS); an 11 DIP sentence is DESIGN.md §5.3's TypeXXXS misuse",
			rect.H)
	}
}

// TestPaletteExcludesHomeDestination covers UX-20: the generated navigation
// commands list every visible screen except Home, as the sidebar does. The
// session destination stays reachable from the palette.
func TestPaletteExcludesHomeDestination(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "session-body", 20))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	for _, cmd := range r.paletteCommands("") {
		if cmd.ID == "uiroot.goto.home" {
			t.Fatalf("UX-20: the palette still generates a Home destination (%+v); the sidebar intentionally has none", cmd)
		}
	}

	r.togglePalette()
	tt.Frame()
	tt.Type("首页")
	tt.Frame()
	requireNoText(t, tt, "前往 首页")

	// The session destination is still offered: excluding Home must not hide
	// the conversation landing the palette exists for. The shell labels the
	// session screen 对话 (screenLabel), so that is the generated title.
	r.closePalette()
	r.togglePalette()
	tt.Frame()
	tt.Type("前往 对话")
	tt.Frame()
	requireHasText(t, tt, "前往 对话")
}
