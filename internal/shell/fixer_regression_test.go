package shell

import (
	"context"
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

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

// TestViewOptionsBaselineGroups covers the nine View-options labels. (The
// former DSH 268px popover geometry pin was retired with pixel parity; the
// labels are the product behaviour this test keeps.)
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
	assertTabReachable(t, tt, []string{"Set default Minimal", "Set default Creator"})
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

// TestCredentialFieldsAreMasked covers the API-key fields that painted the
// stored secret as plain text: two different keys of equal length must render
// identically when the input is masked. It renders the live Settings dialogs
// (Models add/edit and the plugin detail editor) through App.View; the retired
// static fixture now renders an empty frame, which made the old assertion
// compare two blank images.
func TestCredentialFieldsAreMasked(t *testing.T) {
	renderModel := func(key string) *ui.Tester {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Settings
		st := h.app.shellUI()
		st.settingsTab = settingsTabModels
		st.modelDraft = modelDraft{id: "model-1", name: "Test", apiKey: key}
		st.modelDialog = true
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		return tt
	}
	assertMasked := func(label string, render func(string) *ui.Tester, wantTexts ...string) {
		t.Helper()
		a, b := render("sk-AAAA"), render("sk-BBBB")
		// The dialog must really be painted: the live-only App.View returns an
		// empty frame for a Ctrl-less App, and comparing two blank frames would
		// pass for the wrong reason.
		for _, want := range wantTexts {
			if !a.HasText(want) {
				t.Fatalf("%s: dialog not painted (missing %q): %q", label, want, a.Texts())
			}
		}
		if changed := differingPixels(a.Image(), b.Image()); changed != 0 {
			t.Fatalf("%s: secret is painted in clear text: %d pixels differ between two keys",
				label, changed)
		}
	}
	assertMasked("model API key", renderModel, "Edit model", "API key")

	renderPlugin := func(key string) *ui.Tester {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Settings
		st := h.app.pluginUI()
		st.detailOpen = true
		st.detailKey = "web_search"
		st.detailFields = map[string]*string{"websearch.api_key": &key}
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		return tt
	}
	assertMasked("web search API key", renderPlugin, "Web search settings", "API key (Bearer token)")
}

// differingPixels counts the pixels that differ between two renders of the same
// size; -1 means the inputs are not comparable. It replaces the retired
// pixeltest.Compare(..., tolerance 0) call in the credential-masking test.
func differingPixels(a, b *image.RGBA) int {
	if a == nil || b == nil || a.Bounds() != b.Bounds() {
		return -1
	}
	n := 0
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				n++
			}
		}
	}
	return n
}
