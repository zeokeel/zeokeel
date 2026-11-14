package uiroot

// Fix-round regression tests for the shell audit findings: the command palette
// keeps a visible chosen row when it reopens (SH-02), opens above the settings
// surface (SH-07), resets its match count when nothing matches (SH-08), the
// collapsed rail keeps the new-chat action and drops the conversation
// destination (SH-05), and a language change mirrors into the framework locale
// hook (SH-06).

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// accentPixels counts the light theme's accent color in the frame image, which
// is how a chosen List row paints: about one row's worth (~19k pixels) when the
// palette shows its choice, and a few hundred for focus rings when it does not.
func accentPixels(tt *ui.Tester) int {
	img := tt.Image()
	n := 0
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 == 13 && g>>8 == 68 && b>>8 == 246 {
				n++
			}
		}
	}
	return n
}

// TestPaletteKeepsChosenRowVisibleAfterReopen covers SH-02: a List writes -1
// back when the chosen item no longer exists (mygo list.md), which left a
// reopened palette with no painted chosen row while Enter still ran the first
// match. The palette must coerce that to a real choice and paint it, so the
// keyboard target is visible before Enter is pressed.
func TestPaletteKeepsChosenRowVisibleAfterReopen(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	var alphaRan, betaRan bool
	mustCommand(t, r, contracts.CommandSpec{ID: "alpha", Title: "Alpha", Order: 10,
		Run: func() error { alphaRan = true; return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "beta", Title: "Beta", Order: 20,
		Run: func() error { betaRan = true; return nil }})
	unregisterZulu := mustCommand(t, r, contracts.CommandSpec{ID: "zulu", Title: "Zulu", Order: 30,
		Run: func() error { return nil }})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	// First episode: choose Zulu (the filter leaves it as the only match), then
	// close. The list remembers the chosen command's key for the next frame.
	r.togglePalette()
	tt.Frame()
	requireHasText(t, tt, "命令面板")
	tt.Type("Zulu")
	tt.Frame()
	requireHasText(t, tt, "Zulu")
	r.togglePalette()
	tt.Frame()
	requireNoText(t, tt, "命令面板")

	// The chosen command disappears before the palette reopens, as when its
	// Enabled state changes: the reused ListState can no longer follow it.
	unregisterZulu()

	r.togglePalette()
	tt.Frame()
	requireHasText(t, tt, "命令面板")
	if got := accentPixels(tt); got < 5000 {
		t.Fatalf("reopened palette paints no chosen row: accent pixels = %d (a row is about 19k); selected = %d",
			got, r.paletteSelected)
	}
	if r.paletteSelected != 0 {
		t.Fatalf("reopened palette selected = %d, want the first match (0)", r.paletteSelected)
	}
	// Enter must run the row the user can see, not an invisible index.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	if !alphaRan || betaRan {
		t.Fatalf("Enter ran alpha=%v beta=%v, want only the visible first row Alpha", alphaRan, betaRan)
	}
}

// TestPaletteReopenStartsFromFirstRow covers the stale-list half of SH-02: a
// reused ListState follows the previous opening's chosen item and writes its
// old index back into the palette selection while the reopened frame builds, so
// the visible first row was not the row Enter acted on. Opening the palette is
// an app choice of row 0 through ListState.Choose, which forgets the previous
// opening's chosen key, and the painted choice is row 0.
func TestPaletteReopenStartsFromFirstRow(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	titles := []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon"}
	for i, title := range titles {
		mustCommand(t, r, contracts.CommandSpec{ID: fmt.Sprintf("cmd-%d", i), Title: title,
			Order: i * 10, Run: func() error { return nil }})
	}
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()
	// Move the choice away from the first row, then close.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if r.paletteSelected != 2 {
		t.Fatalf("paletteSelected after two Down = %d, want 2", r.paletteSelected)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireNoText(t, tt, "命令面板")

	r.togglePalette()
	tt.Frame()
	requireHasText(t, tt, "命令面板")
	if r.paletteSelected != 0 {
		t.Fatalf("reopened palette selected = %d, want the first row (0)", r.paletteSelected)
	}
	if got := accentPixels(tt); got < 5000 {
		t.Fatalf("reopened palette paints no chosen row: accent pixels = %d", got)
	}
}

// TestPaletteFilterChangeChoosesFirstVisibleRow covers the Choose half of the
// palette's choice model (mygo ListState.Choose): a filter change is an app
// choice of the first visible match. Writing the index alone loses to the
// List's Key memory, which keeps the command chosen before the filter chosen
// and moves the index with it as rows come back above it (the SH-02 class of
// invisible target: Enter would run a row other than the painted first one).
func TestPaletteFilterChangeChoosesFirstVisibleRow(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	ran := make(map[string]bool)
	for i, title := range []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon"} {
		title, i := title, i
		mustCommand(t, r, contracts.CommandSpec{ID: fmt.Sprintf("cmd-%d", i), Title: title,
			Order: i * 10, Run: func() error { ran[title] = true; return nil }})
	}
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()

	// Filter to the commands with an "e": Beta is the first visible match.
	tt.Type("e")
	tt.Frame()
	if r.paletteSelected != 0 {
		t.Fatalf("paletteSelected after filtering = %d, want the first match Beta (0)", r.paletteSelected)
	}

	// Clearing the query brings Alpha back above Beta. The app chooses row 0
	// again; without Choose the List would follow the chosen command's key and
	// leave the choice on Beta, now row 1.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if r.paletteSelected != 0 {
		t.Fatalf("paletteSelected after clearing the filter = %d, want the first row Alpha (0)", r.paletteSelected)
	}
	if got := accentPixels(tt); got < 5000 {
		t.Fatalf("filtered palette paints no chosen row: accent pixels = %d (a row is about 19k)", got)
	}
	// Enter runs the row the user sees.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	if !ran["Alpha"] || ran["Beta"] || ran["Gamma"] || ran["Delta"] || ran["Epsilon"] {
		t.Fatalf("Enter ran %v, want only the visible first row Alpha", ran)
	}
}

// TestPaletteReopenForgetsPreviousChosenCommand covers the List's key memory
// across openings: the command chosen last (a filtered row 0) keeps its key, so
// a reopened palette that only wrote *Selected would have the List follow that
// key to its new row. Opening is an app choice of row 0 through Choose, and
// Enter runs that visible row.
func TestPaletteReopenForgetsPreviousChosenCommand(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	ran := make(map[string]bool)
	for i, title := range []string{"Alpha", "Beta", "Gamma"} {
		title, i := title, i
		mustCommand(t, r, contracts.CommandSpec{ID: fmt.Sprintf("cmd-%d", i), Title: title,
			Order: i * 10, Run: func() error { ran[title] = true; return nil }})
	}
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()
	// Filter to Gamma, the only match, so the chosen command's key is row 0.
	tt.Type("Gamma")
	tt.Frame()
	if r.paletteSelected != 0 {
		t.Fatalf("paletteSelected with one match = %d, want 0", r.paletteSelected)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireNoText(t, tt, "命令面板")

	// Reopened without the filter, Gamma is now the third row: the fresh choice
	// stays on the first row, Alpha.
	r.togglePalette()
	tt.Frame()
	if r.paletteSelected != 0 {
		t.Fatalf("reopened palette followed the previous chosen command to row %d, want 0", r.paletteSelected)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	if !ran["Alpha"] || ran["Beta"] || ran["Gamma"] {
		t.Fatalf("Enter after reopen ran %v, want only the first row Alpha", ran)
	}
}

// TestPaletteOpensAboveSettings covers SH-07: the command palette is the window
// top layer, so Cmd+K keeps working inside the settings surface; Escape closes
// the palette first and leaves the settings surface open.
func TestPaletteOpensAboveSettings(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "appearance", Section: "外观", Title: "外观", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "page-body") },
	})
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.Navigator().ToggleSettings()
	tt.Frame()
	requireHasText(t, tt, "page-body")

	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	requireHasText(t, tt, "命令面板")
	requireHasText(t, tt, "page-body")

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	requireHasText(t, tt, "page-body")

	// A second Escape closes the settings surface itself.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireNoText(t, tt, "page-body")
}

// TestPaletteNoMatchResetsCount covers SH-08: with no matches the palette must
// clear the previous match count, so Up/Down cannot move a stale choice.
func TestPaletteNoMatchResetsCount(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustCommand(t, r, contracts.CommandSpec{ID: "alpha", Title: "Alpha", Order: 10,
		Run: func() error { return nil }})
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()
	tt.Type("zzz-no-such-command")
	tt.Frame()
	requireHasText(t, tt, "没有匹配的命令")
	if r.paletteCount != 0 {
		t.Fatalf("paletteCount = %d after a query with no matches, want 0", r.paletteCount)
	}
	// The arrows must not schedule a frame for a choice that does not exist.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if r.paletteSelected != 0 {
		t.Fatalf("paletteSelected = %d with no matches, want 0", r.paletteSelected)
	}
}

// TestRailNewChatActionAndScreenSet covers SH-05: the collapsed rail keeps the
// new-session action (the expanded sidebar's primary action) and lists the same
// screen destinations as the expanded navigation, without a conversation
// destination that only the workspace tree owns.
func TestRailNewChatActionAndScreenSet(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen("session", "对话", "body-session", 20))
	mustScreen(t, r, textScreen("automation", "自动化", "body-automation", 30))
	mustScreen(t, r, textScreen("plugins", "插件", "body-plugins", 40))
	var newRan bool
	mustCommand(t, r, contracts.CommandSpec{ID: "zeokeel.session.new", Title: "新会话", Order: 10,
		Run: func() error {
			newRan = true
			return r.Navigator().Push("session")
		}})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.SetSidebarCollapsed(true)
	tt.Frame()

	// The rail's icon buttons carry their accessible names; "新对话" is the
	// action, the remaining module screens are destinations (Home is retired
	// from product navigation).
	if _, ok := tt.AccessByRole(ui.RoleButton, "新对话"); !ok {
		t.Fatalf("rail has no new-chat action; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleToggleButton, "对话"); ok {
		t.Fatalf("rail still lists the conversation screen as a destination; texts = %v", tt.Texts())
	}
	for _, want := range []string{"自动化", "插件"} {
		// Destinations are toggle-like so assistive technology hears which
		// screen the rail is on.
		if _, ok := tt.AccessByRole(ui.RoleToggleButton, want); !ok {
			t.Fatalf("rail is missing the %q destination; texts = %v", want, tt.Texts())
		}
	}

	// The rail action creates a session through the same path as the tray and
	// the Cmd/Ctrl+N shortcut.
	if info, ok := tt.AccessByRole(ui.RoleButton, "新对话"); ok {
		tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
		tt.Frame()
	}
	if !newRan {
		t.Fatalf("rail new-chat action did not run the new-session command")
	}
	requireHasText(t, tt, "body-session")
}

// TestLanguageChangeMirrorsFrameworkLocale covers SH-06: the host needs a UI
// thread hook after a product language change so it can mirror the language
// into mygo's own localization; repeated switches to the same language are not
// reported twice.
func TestLanguageChangeMirrorsFrameworkLocale(t *testing.T) {
	var calls []string
	r := New(Config{AppName: "ZeoKeelTest", Language: "zh",
		OnLanguageChange: func(lang string) { calls = append(calls, lang) }})
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 800, 600)
	tt.Frame()
	if len(calls) != 0 {
		t.Fatalf("language hook called during startup: %v", calls)
	}
	r.Locale().SetLanguage("en")
	tt.Frame()
	r.Locale().SetLanguage("en")
	tt.Frame()
	r.Locale().SetLanguage("zh")
	tt.Frame()
	want := []string{"en", "zh"}
	if len(calls) != len(want) {
		t.Fatalf("language hook calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("language hook calls = %v, want %v", calls, want)
		}
	}
}
