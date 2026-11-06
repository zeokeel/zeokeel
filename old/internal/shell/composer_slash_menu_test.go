package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// accessRoleHeading is platform.AccessRole's RoleHeading value (the access tree
// maps ui.RoleHeading with the same RoleGroup base as the other menu roles).
const accessRoleHeading uint8 = 43

// openSlashPalette types "/" into the live composer and renders the palette.
func openSlashPalette(t *testing.T, h *liveApp, tt *ui.Tester) {
	t.Helper()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Type("/")
	tt.Frame()
	if !h.app.slashMenuVisible() {
		t.Fatalf("slash palette did not open: draft=%q", h.app.draft)
	}
}

// TestSlashMenuGroupsRowsAndHints pins the new palette layout: the add section
// over the commands section, every command row carrying its localized title,
// the typed command and a right-aligned hint, with menu role and focus.
func TestSlashMenuGroupsRowsAndHints(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSlashPalette(t, h, tt)

	for _, want := range []string{"Add", "Commands"} {
		if !tt.HasText(want) {
			t.Fatalf("palette missing section %q: %v", want, tt.Texts())
		}
	}
	for _, want := range []string{
		"/file", "/plan", "/feedback", "/permission", "/model", "/export",
		"/clear", "/stop", "/help", "/new", "/settings", "/automate", "/plugins",
	} {
		if !tt.HasText(want) {
			t.Fatalf("palette missing command %q: %v", want, tt.Texts())
		}
	}
	for _, hint := range []string{
		"Pick files to attach to the next message",
		"Enter or leave plan mode",
		"Record good/bad feedback for the last answer",
		"Change the access mode",
		"Choose the model this session uses",
		"Export this session's log to a file",
		"Clear the composer draft",
		"Stop the running turn",
	} {
		if !tt.HasText(hint) {
			t.Fatalf("palette missing row hint %q: %v", hint, tt.Texts())
		}
	}
	if !tt.HasText("Type to filter") {
		t.Fatalf("palette missing footer hint: %v", tt.Texts())
	}

	// Rows are menu items with the typed command as their accessible name; the
	// group headings are headings. The composer keeps the keyboard focus.
	nodes := testerAccessNodes(t, tt)
	if got := popupCount(nodes); got != 0 {
		// The palette is a composer-column panel, not a popover: no popup role.
		t.Fatalf("palette unexpectedly uses a popup role (%d)", got)
	}
	plan := accessNodeIndex(nodes, accessRoleMenuItem, "/plan")
	if plan < 0 {
		t.Fatalf("palette has no menu-item row labeled /plan: %+v", nodes)
	}
	if nodes[plan].States&accessFocusableState == 0 {
		t.Fatal("/plan palette row is not focusable")
	}
	if accessNodeIndex(nodes, accessRoleHeading, "Add") < 0 { // 43 = platform RoleHeading
		t.Fatalf("palette has no heading labeled Add: %+v", nodes)
	}
	if accessNodeIndex(nodes, accessRoleHeading, "Commands") < 0 {
		t.Fatalf("palette has no heading labeled Commands: %+v", nodes)
	}
	if !tt.Focused("Composer input") {
		t.Fatal("opening the palette moved the keyboard focus out of the composer")
	}
	// The row area is a labelled list whose active descendant is the
	// highlighted row, matching the "@" list and DSH's listbox.
	if !hasAccessLabel(nodes, "Command suggestions") {
		t.Fatalf("palette has no labelled command list: %+v", nodes)
	}
}

// TestSlashMenuFiltersAsTyped proves typing filters the palette and a new query
// restarts the highlight at the first match.
func TestSlashMenuFiltersAsTyped(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSlashPalette(t, h, tt)

	tt.Type("pl")
	tt.Frame()
	// Both /plan and /plugins start with "pl"; unrelated rows disappear.
	for _, want := range []string{"/plan", "/plugins", "Plan mode"} {
		if !tt.HasText(want) {
			t.Fatalf("filtered palette missing %q: %v", want, tt.Texts())
		}
	}
	for _, gone := range []string{"/clear", "Clear draft", "/stop"} {
		if tt.HasText(gone) {
			t.Fatalf("filtered palette still shows %q: %v", gone, tt.Texts())
		}
	}

	tt.Type("a")
	tt.Frame()
	// "/pla" keeps only /plan (the name prefix outranks nothing else).
	if !tt.HasText("Plan mode") {
		t.Fatalf("palette missing /plan after /pla: %v", tt.Texts())
	}
	if tt.HasText("/plugins") || tt.HasText("Clear draft") {
		t.Fatalf("palette did not filter to /plan: %v", tt.Texts())
	}

	// A query with no matches shows the empty state and keeps the panel.
	tt.Type("zzz")
	tt.Frame()
	if !tt.HasText("No matching commands") {
		t.Fatalf("empty palette state missing: %v", tt.Texts())
	}
}

// TestSlashMenuKeyboard drives the palette entirely from the keyboard: the
// highlight follows Up/Down, Tab and Enter run it, Escape closes the palette
// without touching the draft.
func TestSlashMenuKeyboard(t *testing.T) {
	t.Run("enter runs the highlighted match", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		openSlashPalette(t, h, tt)
		tt.Type("pla")
		tt.Frame()
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if h.app.draft != "" {
			t.Fatalf("Enter left draft %q", h.app.draft)
		}
		if !h.ctrl.PlanMode(h.ctrl.CurrentSessionID()) {
			t.Fatal("Enter did not run the highlighted /plan row")
		}
		if h.app.slashMenuVisible() {
			t.Fatal("palette stayed open after running a row")
		}
	})

	t.Run("down moves the highlight", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		openSlashPalette(t, h, tt)
		// Row 0 is /file, row 1 is /plan: Down must move to /plan before Enter.
		tt.Key(0, ui.KeyDown)
		tt.Frame()
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if !h.ctrl.PlanMode(h.ctrl.CurrentSessionID()) {
			t.Fatal("Down+Enter did not run the second (/plan) row")
		}
		if h.app.draft != "" {
			t.Fatalf("Down+Enter left draft %q", h.app.draft)
		}
	})

	t.Run("tab runs the highlight", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		openSlashPalette(t, h, tt)
		tt.Type("pla")
		tt.Frame()
		tt.Key(0, ui.KeyTab)
		tt.Frame()
		if !h.ctrl.PlanMode(h.ctrl.CurrentSessionID()) {
			t.Fatal("Tab did not run the highlighted /plan row")
		}
	})

	t.Run("typing filters then enter runs the match", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		openSlashPalette(t, h, tt)
		tt.Type("mo")
		tt.Frame()
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if !h.app.liveMenus().modelOpen {
			t.Fatal("Enter did not run the filtered /model row")
		}
	})

	t.Run("escape closes and keeps the draft", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		openSlashPalette(t, h, tt)
		tt.Type("pl")
		tt.Frame()
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if h.app.slashMenuVisible() {
			t.Fatal("Escape did not close the palette")
		}
		if h.app.draft != "/pl" {
			t.Fatalf("Escape changed the draft to %q", h.app.draft)
		}
		if h.ctrl.PlanMode(h.ctrl.CurrentSessionID()) {
			t.Fatal("Escape executed a command")
		}
	})
}

// TestSlashMenuClickRunsRow keeps the pointer path working.
func TestSlashMenuClickRunsRow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSlashPalette(t, h, tt)
	if err := tt.Click("/help"); err != nil {
		t.Fatalf("click /help row: %v", err)
	}
	tt.Frame()
	if len(h.app.draft) == 0 || h.app.draft[0] != 'C' {
		t.Fatalf("clicking /help did not write the command list: draft=%q", h.app.draft)
	}
}

// TestSlashMenuNarrow checks the palette fits a 900x600 window and its first
// row is still clickable.
func TestSlashMenuNarrow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()
	openSlashPalette(t, h, tt)
	rect, ok := tt.Find("Commands")
	if !ok {
		t.Fatalf("narrow palette missing its section heading: %v", tt.Texts())
	}
	if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > 900 || rect.Y+rect.H > 600 {
		t.Fatalf("narrow palette heading is outside the window: %+v", rect)
	}
	if err := tt.Click("/plan"); err != nil {
		t.Fatalf("click /plan in the narrow palette: %v", err)
	}
	tt.Frame()
	if !h.ctrl.PlanMode(h.ctrl.CurrentSessionID()) {
		t.Fatal("clicking /plan in the narrow palette did not run it")
	}
}

// TestSlashMenuUnknownQueryFallsThroughToModel keeps the documented behavior
// for an unmatched slash line: it is sent to the model as plain text.
func TestSlashMenuUnknownQueryFallsThroughToModel(t *testing.T) {
	provider := &shellProvider{}
	h := newLiveApp(t, provider)
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSlashPalette(t, h, tt)
	tt.Type("zzz")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()
	if got := provider.callCount(); got != 1 {
		t.Fatalf("unknown slash line model calls = %d, want 1", got)
	}
	if h.app.draft != "" {
		t.Fatalf("unknown slash line left draft %q", h.app.draft)
	}
}

// hasAccessLabel reports whether any accessible node carries label.
func hasAccessLabel(nodes []testerAccessNodeInfo, label string) bool {
	for _, node := range nodes {
		if node.Label == label {
			return true
		}
	}
	return false
}
