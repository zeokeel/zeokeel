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

// TestPaletteFooterHintUsesMetadataType covers UX-14 and B-spec §3.1.4: the
// palette's bottom legend is now a 32 DIP key-reference row — the words take the
// metadata step (12/18, never an 11 DIP sentence) and each chord is a key cap.
func TestPaletteFooterHintUsesMetadataType(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "session-body", 20))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()

	for _, word := range []string{"选择", "运行", "关闭"} {
		rect, ok := tt.Find(word)
		if !ok {
			t.Fatalf("the palette footer legend word %q is missing; texts = %v", word, tt.Texts())
		}
		if rect.H < 12 {
			t.Fatalf("UX-14: legend word %q is %.1f DIP tall, want at least the 12 DIP metadata line (TypeXXS)", word, rect.H)
		}
	}
	// The chords are key caps, not text: the cap's own 16 DIP box and
	// Background(BgStrong) are what the row draws (uikit.Kbd). Each cap carries
	// the legend's own accessible name, so a reader looking for "Esc" cannot take
	// the legend's key for a command *named* after one (A4-I-02 discoverability).
	for _, cap := range []string{"图例：↑ ↓", "图例：↵", "图例：Esc"} {
		rect, ok := tt.Find(cap)
		if !ok {
			t.Fatalf("the palette footer key cap %q is missing; texts = %v", cap, tt.Texts())
		}
		if rect.H > 16+0.5 {
			t.Fatalf("key cap %q is %.1f DIP tall, want the 16 DIP cap", cap, rect.H)
		}
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
	// session screen 会话 (uikit.ScreenNavSpec; A5: 对话 → 会话 so the
	// destination matches the sidebar and the insights copy), so that is the
	// generated title.
	r.closePalette()
	r.togglePalette()
	tt.Frame()
	tt.Type("前往 会话")
	tt.Frame()
	requireHasText(t, tt, "前往 会话")
}
