package shell

import (
	"context"
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/pixeltest"
	"github.com/zeokeel/zeokeel/internal/session"
)

// TestSplitLeavesFullscreen covers the dead Split control: while the right
// panel is fullscreen, clicking Split used to flip the button state without
// changing the layout. Split and fullscreen are now mutually exclusive, and the
// panel is actually half the content column.
func TestSplitLeavesFullscreen(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.Language = LangEN
	h.app.rightOpen = true
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("Fullscreen"); err != nil {
		t.Fatalf("click Fullscreen: %v", err)
	}
	tt.Frame()
	if !h.app.rightSidebarMaximized() || h.app.rightSidebarSplit() {
		t.Fatalf("fullscreen state maximized=%v split=%v", h.app.rightSidebarMaximized(), h.app.rightSidebarSplit())
	}
	full, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatalf("right panel missing: %q", tt.Texts())
	}
	if full.W < 1200 {
		t.Fatalf("fullscreen panel width = %v, want the content column", full.W)
	}

	if err := tt.Click("Split"); err != nil {
		t.Fatalf("click Split: %v", err)
	}
	tt.Frame()
	if h.app.rightSidebarMaximized() {
		t.Fatal("Split left the panel maximized")
	}
	if !h.app.rightSidebarSplit() {
		t.Fatal("Split did not enable split mode")
	}
	panel, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatalf("split panel missing: %q", tt.Texts())
	}
	want := (float32(1600) - sidebarW) / 2
	if panel.W < want-2 || panel.W > want+2 {
		t.Fatalf("split panel width = %v, want half the content column (%v)", panel.W, want)
	}
	if panel.X < sidebarW+want-2 || panel.X > sidebarW+want+2 {
		t.Fatalf("split panel x = %v, want the divider at %v", panel.X, sidebarW+want)
	}

	// Enabling fullscreen again leaves split.
	if err := tt.Click("Exit fullscreen"); err == nil {
		t.Fatal("unexpected exit-fullscreen label")
	}
	if err := tt.Click("Fullscreen"); err != nil {
		t.Fatalf("click Fullscreen again: %v", err)
	}
	tt.Frame()
	if h.app.rightSidebarSplit() || !h.app.rightSidebarMaximized() {
		t.Fatalf("fullscreen did not clear split: split=%v maximized=%v",
			h.app.rightSidebarSplit(), h.app.rightSidebarMaximized())
	}
}

// TestNarrowRightPanelWidth covers DSH's 520px docked panel at 1200x800
// (400px of main content), instead of the old 560px clamp.
func TestNarrowRightPanelWidth(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.Language = LangEN
	h.app.rightOpen = true
	tt := ui.NewTester(h.app.View, 1200, 800)
	tt.Frame()
	panel, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatalf("right panel missing: %q", tt.Texts())
	}
	if panel.W < 518 || panel.W > 522 {
		t.Fatalf("docked panel width = %v, want 520 (main 400)", panel.W)
	}
	if panel.X < 678 || panel.X > 682 {
		t.Fatalf("docked panel x = %v, want 680", panel.X)
	}
}

// TestInlineSearchIsNotADialog covers DSH screen 78: the sidebar search
// expands an inline field with a Clear control and filters the sidebar; it does
// not open a modal.
func TestInlineSearchIsNotADialog(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	if _, err := h.ctrl.CreateSession(ctx, "Needle session"); err != nil {
		t.Fatal(err)
	}
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Search sessions"); err != nil {
		t.Fatalf("click Search sessions: %v", err)
	}
	tt.Frame()
	if h.app.paletteOpen {
		t.Fatal("sidebar search opened the modal palette")
	}
	if _, ok := tt.Find("Sort: Last updated"); ok {
		t.Fatal("sidebar search opened the search dialog")
	}
	if !tt.HasText("Search session names") {
		t.Fatalf("inline field has no accessible label: %q", tt.Texts())
	}
	if !tt.HasText("Clear search") {
		t.Fatalf("inline field has no Clear control: %q", tt.Texts())
	}
	tt.Type("Needle")
	tt.Frame()
	if !tt.HasText("Needle session") {
		t.Fatalf("inline search did not show the match: %q", tt.Texts())
	}
	if tt.HasText("live") {
		t.Fatalf("inline search did not filter the list: %q", tt.Texts())
	}
	if err := tt.Click("Clear search"); err != nil {
		t.Fatalf("click Clear search: %v", err)
	}
	tt.Frame()
	if h.app.inlineQuery != "" {
		t.Fatal("Clear search left the query set")
	}
	// Esc collapses the field.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.inlineSearch {
		t.Fatal("Esc did not collapse the inline search")
	}
}

// TestViewOptionsBaselineGroups covers the nine baseline option labels and
// the popover's right-aligned 268px geometry over the sidebar.
func TestViewOptionsBaselineGroups(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("View options"); err != nil {
		t.Fatalf("click View options: %v", err)
	}
	tt.Frame()
	for _, label := range []string{
		"Group by", "WorkSpace", "Workspace Tree", "In one list",
		"Order by", "Manual", "Last updated",
		"Filter sessions", "Hide archived", "All conversations (show archived)", "Archived only",
	} {
		if !tt.HasText(label) {
			t.Fatalf("view options missing %q: %q", label, tt.Texts())
		}
	}
	item, ok := tt.Find("WorkSpace")
	if !ok {
		t.Fatal("WorkSpace item missing")
	}
	if item.X < 12 || item.X+item.W > 281 {
		t.Fatalf("view options item = %+v, want it inside the sidebar (x 12..280)", item)
	}
	if item.W < 240 {
		t.Fatalf("view options item width = %v, want the 268px popover", item.W)
	}
}

// TestSettingsHeaderActionsHaveAccessibleNames checks that the live settings
// header's "Open configuration file" and "Close" buttons carry explicit names,
// not just a child Text (assistive technology saw them as unnamed buttons).
func TestSettingsHeaderActionsHaveAccessibleNames(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	if h.app.Screen != Settings {
		t.Fatal("settings did not open")
	}
	tt.Frame()
	for _, label := range []string{"Open configuration file", "Close"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q missing: %q", label, tt.Texts())
		}
		// The label on the button gives the whole 28px control its name; a
		// child text alone would report only the ~12px glyph line.
		if r.H < 24 {
			t.Fatalf("%q named rect height = %v, want the 28px button", label, r.H)
		}
	}
	seen := focusedDuringTab(tt, []string{"Open configuration file", "Close"}, 120)
	if !seen["Open configuration file"] || !seen["Close"] {
		t.Fatalf("settings header actions not keyboard reachable: %v", seen)
	}
}

// TestSetDefaultPresetKeyboardReachable covers the agent-preset control that
// had a label but no Role/Focusable, so there was no keyboard path.
func TestSetDefaultPresetKeyboardReachable(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Settings
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Agent presets"); err != nil {
		t.Fatalf("open Agent presets: %v", err)
	}
	tt.Frame()
	assertTabReachable(t, tt, []string{"Set default PTC mode", "Set default Minimal mode"})
}

// TestSearchDialogChipsAndRowsReachable covers the search dialog's filter
// chips and its result open row: both were pointer-only and unnamed.
func TestSearchDialogChipsAndRowsReachable(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	if !h.app.paletteOpen {
		t.Fatal("palette did not open")
	}
	tt.Frame()
	assertTabReachable(t, tt, []string{
		"Sort: Last updated", "Hide archived", "Open live",
	})
	// Enter on the focused chip activates it, not only a pointer click.
	for i := 0; i < 120 && !tt.Focused("Sort: Last updated"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("Sort: Last updated") {
		t.Fatal("sort chip never took focus")
	}
	tt.Key(0, ui.KeyEnter)
	if !h.app.manualSort {
		t.Fatal("Enter on the sort chip did not toggle it")
	}
	h.app.manualSort = false
	tt.Frame()
	if err := tt.Click("Sort: Last updated"); err != nil {
		t.Fatalf("chip is not clickable: %v", err)
	}
	if !h.app.manualSort {
		t.Fatal("sort chip did not toggle")
	}
}

// TestAddWorkspaceRowRemoved covers the extra mouse-only "+ Add workspace"
// row the live sidebar used to draw under the workspace list; DSH has only the
// header control.
func TestAddWorkspaceRowRemoved(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	count := 0
	for _, text := range tt.Texts() {
		if text == "Add workspace" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("sidebar shows %d Add workspace controls, want the single header button", count)
	}
}

// TestNarrowContentColumns covers DSH's 1200x800 geometry: the Home stack and
// composer are min(baseline, contentWidth-212) = 708px at x≈381, and the
// Session composer follows the same clamp.
func TestNarrowContentColumns(t *testing.T) {
	home := &App{Screen: Home}
	tt := ui.NewTester(home.View, 1200, 800)
	tt.Frame()
	card, ok := tt.Find("Home composer")
	if !ok {
		t.Fatalf("home composer missing: %q", tt.Texts())
	}
	if card.W < 706 || card.W > 712 {
		t.Fatalf("home composer width = %v, want 708 (+2 border)", card.W)
	}
	if card.X < 380 || card.X > 386 {
		t.Fatalf("home composer x = %v, want ~381", card.X)
	}

	sess := &App{Screen: Session}
	st := ui.NewTester(sess.View, 1200, 800)
	st.Frame()
	composer, ok := st.Find("Session composer")
	if !ok {
		t.Fatalf("session composer missing: %q", st.Texts())
	}
	if composer.W < 700 || composer.W > 716 {
		t.Fatalf("session composer width = %v, want 708", composer.W)
	}
}

// TestNarrowPageColumns covers DSH's 824px Plugins/Automation column in the
// 920px main area (48px gutters) at 1200x800.
func TestNarrowPageColumns(t *testing.T) {
	plugins := &App{Screen: Plugins}
	pt := ui.NewTester(plugins.View, 1200, 800)
	pt.Frame()
	header, ok := pt.Find("Plugins page header")
	if !ok {
		t.Fatalf("plugins header missing: %q", pt.Texts())
	}
	if header.X < 326 || header.X > 330 || header.W < 822 || header.W > 826 {
		t.Fatalf("plugins column = %+v, want x=328 w=824", header)
	}
	if add, ok := pt.Find("Add plugin"); !ok {
		t.Fatalf("Add plugin missing")
	} else if add.X+add.W > 1152.5 {
		t.Fatalf("Add plugin right edge = %v, want <= 1152", add.X+add.W)
	}

	automation := &App{Screen: Automation}
	at := ui.NewTester(automation.View, 1200, 800)
	at.Frame()
	heading, ok := at.Find("Automation page heading")
	if !ok {
		t.Fatalf("automation heading missing: %q", at.Texts())
	}
	if heading.X < 326 || heading.X > 330 || heading.W < 822 || heading.W > 826 {
		t.Fatalf("automation column = %+v, want x=328 w=824", heading)
	}
	search, ok := at.Find("Search automation tasks")
	if !ok {
		t.Fatalf("automation search missing")
	}
	if search.X < 326 || search.X > 330 || search.X+search.W > 1152.5 {
		t.Fatalf("automation search = %+v, want 328..1152", search)
	}
}

// TestComposerPermissionUsesSessionMode covers the composer chip reading the
// global default while the session's effective mode differed.
func TestComposerPermissionUsesSessionMode(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.Language = LangEN
	// Global default stays Read Only; the session is explicitly Full access.
	cfg := h.app.currentSettings()
	cfg.Permission = session.PermissionAsk
	if err := h.ctrl.Settings().Save(cfg); err != nil {
		t.Fatal(err)
	}
	h.app.settingsLoaded = false
	sess, err := h.ctrl.CurrentSession()
	if err != nil {
		t.Fatal(err)
	}
	h.ctrl.SetSessionPermission(sess.ID, session.PermissionFull)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Access mode, current: Full access") {
		t.Fatalf("chip does not use the session mode: %q", tt.Texts())
	}
	if tt.HasText("Access mode, current: Read Only") {
		t.Fatalf("chip still shows the global default: %q", tt.Texts())
	}
}

// TestNewSessionIconCrop compares the sidebar's New Session glyph with the
// baseline's circled plus. The old bare plus left the ring unpainted, which
// this crop detects even though the label and button geometry are unchanged.
func TestNewSessionIconCrop(t *testing.T) {
	app := &App{Screen: Home}
	got := ui.Render(app.View, 1600, 1000, 1)
	want, err := pixeltest.LoadPNG("../../baseline/screenshots/00-home.png")
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	// The 16px glyph sits inside the 252x38 primary button at y=70.
	res := pixeltest.Compare(got, want, image.Rect(80, 76, 108, 104), 40)
	if res.Ratio > 0.25 {
		t.Fatalf("New Session icon crop diff = %.1f%%, want <= 25%%", res.Ratio*100)
	}
}

// TestCredentialFieldsAreMasked covers the API-key fields that painted the
// stored secret as plain text: two different keys of equal length must render
// identically when the input is masked.
func TestCredentialFieldsAreMasked(t *testing.T) {
	renderModel := func(key string) *image.RGBA {
		app := &App{Screen: Settings}
		st := app.shellUI()
		st.modelDraft = modelDraft{id: "model-1", name: "Test", apiKey: key}
		st.modelDialog = true
		return ui.Render(app.View, 1600, 1000, 1)
	}
	assertMasked := func(label string, render func(string) *image.RGBA) {
		t.Helper()
		a, b := render("sk-AAAA"), render("sk-BBBB")
		if a == nil || b == nil {
			t.Fatalf("%s: nil render", label)
		}
		if res := pixeltest.Compare(a, b, a.Bounds(), 0); res.Changed != 0 {
			t.Fatalf("%s: secret is painted in clear text: %d pixels differ between two keys",
				label, res.Changed)
		}
	}
	assertMasked("model API key", renderModel)

	renderPlugin := func(key string) *image.RGBA {
		app := &App{Screen: Settings}
		st := app.pluginUI()
		st.detailOpen = true
		st.detailKey = "web_search"
		st.detailFields = map[string]*string{"websearch.api_key": &key}
		return ui.Render(app.View, 1600, 1000, 1)
	}
	assertMasked("web search API key", renderPlugin)
}
