package shell

import (
	"hash/crc32"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// TestSettingsShortcutsAndPersistence drives the settings modal with the
// keyboard and the mouse, then checks that every value is persisted and
// applied to the running app.
func TestSettingsShortcutsAndPersistence(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)

	// Cmd/Ctrl+, opens the modal.
	tt.Key(ui.Cmd, ui.KeyComma)
	if h.app.Screen != Settings {
		t.Fatalf("Cmd/Ctrl+, left screen %v, want Settings", h.app.Screen)
	}
	if !tt.HasText("Settings") {
		t.Fatalf("settings dialog missing: %q", tt.Texts())
	}

	// Appearance: picking Dark persists ui.theme and re-themes immediately.
	if err := tt.Click("Dark"); err != nil {
		t.Fatalf("click Dark: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Theme != session.ThemeDark {
		t.Fatalf("theme = %q, want dark", cfg.Theme)
	}
	// The brand nav surface is dark in the dark theme; sample it rather than
	// a fixed DSH pixel (the nav column's bottom-left stays free of controls).
	img := tt.Image()
	if r, g, b, _ := img.RGBAAt(400, 850).RGBA(); r>>8 > 0x60 || g>>8 > 0x60 || b>>8 > 0x60 {
		t.Fatalf("settings panel stayed light after Dark: %02x %02x %02x", r>>8, g>>8, b>>8)
	}

	// Font size: 14 -> 16 persists ui.font_size and applies through the
	// conversation design helper.
	if err := tt.Click("14"); err != nil {
		t.Fatalf("click font size: %v", err)
	}
	if !tt.HasText("16 px") {
		t.Fatalf("font size popover missing: %q", tt.Texts())
	}
	if err := tt.Click("16 px"); err != nil {
		t.Fatalf("click 16 px: %v", err)
	}
	cfg, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FontSize != session.FontSizeLarge {
		t.Fatalf("font size = %d, want 16", cfg.FontSize)
	}
	if got := h.app.ConversationFontSize(); got != 16 {
		t.Fatalf("ConversationFontSize = %v, want 16", got)
	}

	// Permission: choose Read Only (the canonical DSH label for the Ask
	// value), then a new session inherits it.
	if err := tt.Click("Permission, current: Workspace Write"); err != nil {
		t.Fatalf("click permission: %v", err)
	}
	if err := tt.Click("Read Only"); err != nil {
		t.Fatalf("click Read Only: %v", err)
	}
	cfg, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Permission != session.PermissionAsk {
		t.Fatalf("permission = %q, want ask", cfg.Permission)
	}
	tt.Key(ui.Cmd, ui.KeyN)
	if h.app.Screen != Session {
		t.Fatalf("Cmd/Ctrl+N left screen %v, want Session", h.app.Screen)
	}
	if got := h.ctrl.SessionPermission(h.ctrl.CurrentSessionID()); got != session.PermissionAsk {
		t.Fatalf("new session permission = %q, want ask", got)
	}

	// Work details: Detailed -> Standard persists and changes the tool card
	// expansion default. Reopen Settings over Home: the Session header behind
	// the modal also shows the text "Standard", which would make the option
	// ambiguous to the Tester's first-match lookup. Overlay-over-Session is
	// covered by TestSettingsOpenedFromSessionKeepsIt.
	h.app.Screen = Home
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("Detailed"); err != nil {
		t.Fatalf("click work details: %v", err)
	}
	if err := tt.Click("Standard"); err != nil {
		t.Fatalf("click Standard: %v", err)
	}
	cfg, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkDetails != session.WorkDetailsStandard {
		t.Fatalf("work details = %q, want standard", cfg.WorkDetails)
	}
	if h.app.ToolDetailsExpanded() {
		t.Fatal("tool details default expanded under Standard")
	}

	// Language last: every label in the settings dialog switches to Chinese.
	if err := tt.Click("English"); err != nil {
		t.Fatalf("click language: %v", err)
	}
	if err := tt.Click("中文"); err != nil {
		t.Fatalf("click 中文: %v", err)
	}
	cfg, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Language != session.LanguageChinese {
		t.Fatalf("language = %q, want zh", cfg.Language)
	}
	if !tt.HasText("设置") || !tt.HasText("通用") || !tt.HasText("外观") {
		t.Fatalf("Chinese labels missing: %q", tt.Texts())
	}
	if !tt.HasText("设置已保存") {
		t.Fatalf("settings toast missing: %q", tt.Texts())
	}

	// Esc closes the settings modal.
	tt.Key(0, ui.KeyEscape)
	if h.app.Screen != Home {
		t.Fatalf("Esc left screen %v, want Home", h.app.Screen)
	}
}

// TestKeyboardShortcuts checks New Session, Settings, the session search
// palette and Esc.
func TestKeyboardShortcuts(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)

	before, err := h.ctrl.ListSessions(t.Context())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyN)
	after, err := h.ctrl.ListSessions(t.Context())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("Cmd/Ctrl+N created %d sessions, want %d", len(after)-len(before), 1)
	}
	if h.app.Screen != Session {
		t.Fatalf("screen = %v, want Session", h.app.Screen)
	}

	// Cmd/Ctrl+K in a session focuses the composer: typing reaches the draft.
	if err := tt.Click("Send"); err != nil {
		t.Fatalf("click Send: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyK)
	if !tt.Focused("Composer input") {
		t.Fatalf("Cmd/Ctrl+K did not focus the composer: %q", tt.Texts())
	}
	tt.Type("hello")
	if !strings.HasPrefix(h.app.draft, "hello") {
		t.Fatalf("typing after Cmd/Ctrl+K reached draft %q, want hello", h.app.draft)
	}

	// Outside a session it opens the search palette with its input focused.
	h.app.Screen = Home
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	if !h.app.paletteOpen {
		t.Fatal("Cmd/Ctrl+K did not open the palette from Home")
	}
	if !tt.HasText("Search sessions") {
		t.Fatalf("palette missing: %q", tt.Texts())
	}
	tt.Type("live")
	tt.Frame()
	if !tt.HasText("live") {
		t.Fatalf("palette lost its matching session: %q", tt.Texts())
	}
	// Enter opens the first match.
	tt.Key(0, ui.KeyEnter)
	if h.app.Screen != Session || h.app.paletteOpen {
		t.Fatalf("palette Enter left screen %v open=%v", h.app.Screen, h.app.paletteOpen)
	}

	// Esc closes the palette.
	h.app.Screen = Home
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Key(0, ui.KeyEscape)
	if h.app.paletteOpen {
		t.Fatal("Esc did not close the palette")
	}
}

// TestRenameAndDeleteFromSidebar exercises the session row's native menu,
// both dialogs and the resulting store state.
func TestRenameAndDeleteFromSidebar(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	ctx := t.Context()

	sessions, err := h.ctrl.ListSessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("ListSessions = %+v, %v", sessions, err)
	}
	id := sessions[0].ID
	actions := "Session actions for " + sessions[0].Title

	// Rename through the row's native actions menu.
	if err := tt.Click(actions); err != nil {
		t.Fatalf("open the session actions menu: %v", err)
	}
	if !tt.HasText("Rename") || !tt.HasText("Delete") {
		t.Fatalf("session actions menu missing items: %q", tt.Texts())
	}
	if err := tt.Click("Rename"); err != nil {
		t.Fatalf("choose Rename: %v", err)
	}
	if !tt.HasText("Rename session") {
		t.Fatalf("rename dialog missing: %q", tt.Texts())
	}
	tt.Type(" renamed")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save: %v", err)
	}
	stored, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !strings.HasSuffix(stored.Title, "renamed") {
		t.Fatalf("title after rename = %q", stored.Title)
	}
	if !tt.HasText("Session renamed") {
		t.Fatalf("rename toast missing: %q", tt.Texts())
	}
	if !tt.HasText(stored.Title) {
		t.Fatalf("sidebar does not show renamed title %q: %q", stored.Title, tt.Texts())
	}

	// Delete through the row's native actions menu, then the dialog.
	if err := tt.Click("Session actions for " + stored.Title); err != nil {
		t.Fatalf("reopen the session actions menu: %v", err)
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("choose Delete: %v", err)
	}
	if !tt.HasText("Delete session?") {
		t.Fatalf("delete dialog missing: %q", tt.Texts())
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete button: %v", err)
	}
	if _, err := h.store.GetSession(id); err == nil {
		t.Fatal("session still exists after delete")
	}
	if !tt.HasText("Session deleted") {
		t.Fatalf("delete toast missing: %q", tt.Texts())
	}
}

// TestCopyTitleToast checks the row menu's copy action writes the clipboard
// and reports with a toast.
func TestCopyTitleToast(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)

	if err := tt.Click("Session actions for live"); err != nil {
		t.Fatalf("open the session actions menu: %v", err)
	}
	if err := tt.Click("Copy title"); err != nil {
		t.Fatalf("choose Copy title: %v", err)
	}
	if !tt.HasText("Copied session title") {
		t.Fatalf("copy toast missing: %q", tt.Texts())
	}
	if got := tt.Clipboard(); got != "live" {
		t.Fatalf("clipboard = %q, want %q", got, "live")
	}
}

// TestFontSizeChangesConversationPixels checks the setting reaches live
// conversation text end to end: the rendered transcript changes when the
// size switches from 14 to 16.
func TestFontSizeChangesConversationPixels(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := t.Context()
	if err := h.ctrl.Submit(ctx, h.ctrl.CurrentSessionID(), "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	h.ctrl.Wait()
	h.app.SetSnapshot(h.ctrl.Snapshot())

	tt := ui.NewTester(h.app.View, 1600, 1000)
	before := crc32.ChecksumIEEE(tt.Image().Pix)

	h.app.settingsLoaded = true
	h.app.settingsValue = session.DefaultSettings()
	h.app.settingsValue.FontSize = session.FontSizeLarge
	tt.Frame()
	after := crc32.ChecksumIEEE(tt.Image().Pix)
	if before == after {
		t.Fatal("16px conversation text rendered identically to 14px")
	}
}

// TestThemeSystemFollowsDesktop checks System follows the desktop and Light
// forces a light theme even when the desktop is dark.
func TestThemeSystemFollowsDesktop(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if err := h.ctrl.Settings().Save(session.Settings{Theme: session.ThemeSystem}); err != nil {
		t.Fatalf("Save theme: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.SetDark(true)
	tt.Frame()
	if r, _, _, _ := tt.Image().RGBAAt(10, 500).RGBA(); r>>8 > 0x40 {
		t.Fatalf("System did not follow the dark desktop: %02x", r>>8)
	}

	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("Light"); err != nil {
		t.Fatalf("click Light: %v", err)
	}
	// The settings scrim darkens the sidebar to 76% of its color.
	if r, g, b, _ := tt.Image().RGBAAt(10, 500).RGBA(); r>>8 < 0xA0 || g>>8 < 0xA0 || b>>8 < 0xA0 {
		t.Fatalf("Light did not force a light sidebar: %02x %02x %02x", r>>8, g>>8, b>>8)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil || cfg.Theme != session.ThemeLight {
		t.Fatalf("theme after Light = %q, %v", cfg.Theme, err)
	}

	if err := tt.Click("System"); err != nil {
		t.Fatalf("click System: %v", err)
	}
	if r, _, _, _ := tt.Image().RGBAAt(10, 500).RGBA(); r>>8 > 0x40 {
		t.Fatalf("System did not return to the dark desktop: %02x", r>>8)
	}
}

// TestFontSizeDesignHelper checks the conversion helper directly so the
// session renderers' hook is covered without depending on their file.
func TestFontSizeDesignHelper(t *testing.T) {
	cfg := session.DefaultSettings()
	if got := conversationTextSize(cfg); got != 14 {
		t.Fatalf("default conversation size = %v, want 14", got)
	}
	cfg.FontSize = session.FontSizeLarge
	if got := conversationTextSize(cfg); got != 16 {
		t.Fatalf("large conversation size = %v, want 16", got)
	}
	if toolDetailsExpanded(session.WorkDetailsDetailed) != true {
		t.Fatal("Detailed should expand tool cards")
	}
	if toolDetailsExpanded(session.WorkDetailsStandard) != false {
		t.Fatal("Standard should collapse tool cards")
	}
}

// TestWorkDetailsSeedsToolCards checks that the Work details setting is the
// default expansion of a tool card the live renderer has not seen before.
func TestWorkDetailsSeedsToolCards(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.settingsLoaded = true
	h.app.settingsValue = session.DefaultSettings()

	detailed := state.NewSnapshot()
	detailed.Tools["card-detailed"] = state.ToolState{CallID: "card-detailed"}
	h.app.applyToolCardDefaults(detailed)
	if !h.app.live().expanded["card-detailed"] {
		t.Fatal("Detailed did not seed an expanded tool card")
	}

	h.app.settingsValue.WorkDetails = session.WorkDetailsStandard
	standard := state.NewSnapshot()
	standard.Tools["card-standard"] = state.ToolState{CallID: "card-standard"}
	h.app.applyToolCardDefaults(standard)
	if h.app.live().expanded["card-standard"] {
		t.Fatal("Standard seeded an expanded tool card")
	}
	// An existing card the user toggled keeps its choice.
	h.app.live().expanded["card-detailed"] = false
	h.app.settingsValue.WorkDetails = session.WorkDetailsDetailed
	h.app.applyToolCardDefaults(detailed)
	if h.app.live().expanded["card-detailed"] {
		t.Fatal("seeding overwrote a user's collapsed card")
	}
}

// TestShortcutsEditorEditsAndRestores covers DSH's Edit shortcuts dialog:
// the settings row opens a searchable editor, editing a row persists an
// override that applies to the next frame, and Restore all defaults clears it.
func TestShortcutsEditorEditsAndRestores(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	if !tt.HasText("Edit shortcuts") {
		t.Fatalf("settings row missing the edit action: %q", tt.Texts())
	}
	if tt.HasText("read-only reference") {
		t.Fatalf("settings row still describes a read-only reference: %q", tt.Texts())
	}
	if err := tt.Click("Edit shortcuts"); err != nil {
		t.Fatalf("open shortcuts editor: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Search shortcuts") {
		t.Fatalf("editor has no search field: %q", tt.Texts())
	}
	if !tt.HasText("Edit shortcut for New session") {
		t.Fatalf("editor has no per-row edit control: %q", tt.Texts())
	}
	if err := tt.Click("Edit shortcut for New session"); err != nil {
		t.Fatalf("start editing New Session: %v", err)
	}
	tt.Frame()
	h.app.shortcutDraft = ""
	tt.Frame()
	if err := tt.Click("Shortcut for New session"); err != nil {
		t.Fatalf("shortcut input missing: %v", err)
	}
	tt.Type("Cmd+Shift+J")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save shortcut: %v", err)
	}
	tt.Frame()
	raw, err := h.ctrl.Store().GetSetting(keybindingKey("new_session"))
	if err != nil || raw != "Cmd+Shift+J" {
		t.Fatalf("persisted binding = %q, %v; want Cmd+Shift+J", raw, err)
	}
	// The edited binding is what the row reports now and what a fresh App
	// restores from the same store.
	if got := h.app.shortcutRows()[0].keys; got != "Cmd+Shift+J" {
		t.Fatalf("row keys = %q, want the override", got)
	}
	fresh := &App{Ctrl: h.ctrl, Language: LangEN}
	if got := fresh.shortcutRows()[0].keys; got != "Cmd+Shift+J" {
		t.Fatalf("fresh App restored %q, want the persisted override", got)
	}

	// Restore all defaults clears the override and re-enables the default key.
	tt.Frame()
	if err := tt.Click("Restore all defaults"); err != nil {
		t.Fatalf("restore defaults: %v", err)
	}
	tt.Frame()
	if raw, _ := h.ctrl.Store().GetSetting(keybindingKey("new_session")); raw != "" {
		t.Fatalf("restore left binding %q", raw)
	}
	if got := h.app.shortcutRows()[0].keys; got != "Cmd/Ctrl+N" {
		t.Fatalf("row keys after restore = %q, want the default", got)
	}
}

// TestCollapseWorkDetailsApplies covers the selector that used to be stored
// but never read: Immediately collapses finished cards, On completion closes
// every card when the turn goes idle.
func TestCollapseWorkDetailsApplies(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	app := &App{Ctrl: h.ctrl}
	app.settingsLoaded = true
	app.settingsValue = session.DefaultSettings()
	app.settingsValue.WorkDetails = session.WorkDetailsDetailed

	running := state.NewSnapshot()
	running.Phase = state.PhaseRunning
	running.Tools["t1"] = state.ToolState{Status: state.ToolRunning}
	app.applyToolCardDefaults(running)
	if !app.live().expanded["t1"] {
		t.Fatal("running card did not start expanded under Detailed")
	}

	app.settingsValue.CollapseWorkDetail = session.CollapseOnCompletion
	done := state.NewSnapshot()
	done.Phase = state.PhaseIdle
	done.Tools["t1"] = state.ToolState{Status: state.ToolDone}
	app.applyToolCardDefaults(done)
	if app.live().expanded["t1"] {
		t.Fatal("On completion did not collapse the card when the turn went idle")
	}

	app2 := &App{Ctrl: h.ctrl}
	app2.settingsLoaded = true
	app2.settingsValue = session.DefaultSettings()
	app2.settingsValue.WorkDetails = session.WorkDetailsDetailed
	app2.settingsValue.CollapseWorkDetail = session.CollapseImmediately
	finished := state.NewSnapshot()
	finished.Phase = state.PhaseRunning
	finished.Tools["t2"] = state.ToolState{Status: state.ToolDone}
	app2.applyToolCardDefaults(finished)
	if app2.live().expanded["t2"] {
		t.Fatal("Immediately left a finished card expanded")
	}
}

// TestPermissionLabelsMatchBetweenSettingsAndComposer pins one canonical
// label for session.PermissionAsk: DSH calls it "Read Only", and both the
// Settings selector and the composer access menu must agree for the same
// persisted value.
func TestPermissionLabelsMatchBetweenSettingsAndComposer(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	want := []string{"Read Only", "Workspace Write", "Full access"}

	// Settings owns the future-session default. Set it to Read Only first;
	// the Home composer must then show the same label for the same value.
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if err := tt.Click("Permission, current: Workspace Write"); err != nil {
		t.Fatalf("open Settings permission selector: %v", err)
	}
	if err := tt.Click("Read Only"); err != nil {
		t.Fatalf("choose Read Only in Settings: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Permission, current: Read Only"); err != nil {
		t.Fatalf("reopen Settings permission selector: %v", err)
	}
	for _, label := range want {
		if !tt.HasText(label) {
			t.Fatalf("Settings permission options missing %q: %q", label, tt.Texts())
		}
	}
	for _, text := range tt.Texts() {
		if text == "Ask" {
			t.Fatalf("Settings still labels PermissionAsk as Ask: %q", tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.Screen != Home {
		t.Fatalf("Esc returned to %v, want Home", h.app.Screen)
	}

	// The composer menu exposes the same three labels for the same value.
	if err := tt.Click("Access mode, current: Read Only"); err != nil {
		t.Fatalf("reopen composer permission menu: %v", err)
	}
	for _, label := range want {
		if !tt.HasText(label) {
			t.Fatalf("composer permission menu missing %q: %q", label, tt.Texts())
		}
	}
	// Choosing in the Home composer stages for the next session; the General
	// default stays Read Only.
	if err := tt.Click("Full access"); err != nil {
		t.Fatalf("choose Full access in composer: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if cfg.Permission != session.PermissionAsk {
		t.Fatalf("Home composer changed the General default to %q", cfg.Permission)
	}
}

// TestRenderSettingsNativePreview renders the Settings overlay for brand
// review: light and dark at the 1600x1000 design size. The PNGs are written to
// docs/design/preview so the design is reproducible straight from the test.
func TestRenderSettingsNativePreview(t *testing.T) {
	dir := previewDir(t)
	for _, tc := range []struct {
		name  string
		theme string
	}{
		{"light", session.ThemeLight},
		{"dark", session.ThemeDark},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			// Chinese is the product default language; the preview shows the
			// shipped first-run look.
			h.app.Language = LangZH
			if err := h.store.SetSetting(session.SettingTheme, tc.theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			h.app.Screen = Settings
			img := ui.Render(h.app.View, 1600, 1000, 1)
			path := filepath.Join(dir, "settings-native-"+tc.name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatalf("encode %s: %v", path, err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close %s: %v", path, err)
			}
			if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
				t.Fatalf("preview %s is empty: %v", path, err)
			}
			t.Logf("wrote %s", path)
		})
	}
}

// TestSettingsNativeControls covers the native widgets the brand settings
// page is built from: the page is a responsive brand panel, drop-downs are
// native Selects that persist their choice, and the Show coding view row is a
// native Switch whose toggle applies to the nav and the session screen.
func TestSettingsNativeControls(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("Cmd+, screen = %v, want Settings", h.app.Screen)
	}

	// The panel is the brand cap on a wide window: 720-880 DIPs wide. The
	// nav's left inset (12) and the header's right inset (24) are the brand
	// 4 DIP grid, so the members give the panel bounds back.
	closeRect, ok := tt.Find("Close settings")
	if !ok {
		t.Fatalf("settings close control missing: %q", tt.Texts())
	}
	general, ok := tt.Find("General")
	if !ok {
		t.Fatalf("settings nav missing: %q", tt.Texts())
	}
	panelLeft := general.X - 12
	panelRight := closeRect.X + closeRect.W + 24
	if panelW := panelRight - panelLeft; panelW < 720 || panelW > 880 {
		t.Fatalf("settings panel width = %v, want 720-880", panelW)
	}

	// A native select: the trigger shows the current value, the popup lists
	// the choices, and choosing one persists through the settings store.
	if err := tt.Click("On completion"); err != nil {
		t.Fatalf("open the native work-details select: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Immediately") {
		t.Fatalf("native select popup missing its option: %q", tt.Texts())
	}
	if err := tt.Click("Immediately"); err != nil {
		t.Fatalf("choose Immediately: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CollapseWorkDetail != session.CollapseImmediately {
		t.Fatalf("collapse setting = %q, want immediately", cfg.CollapseWorkDetail)
	}

	// A native switch: toggling Show coding view persists and hides the Agent
	// presets page, exactly as the row's description says.
	if err := tt.Click("Toggle show coding view"); err != nil {
		t.Fatalf("toggle Show coding view: %v", err)
	}
	tt.Frame()
	if h.app.currentSettings().ShowCodingView {
		t.Fatal("native switch did not persist Show coding view off")
	}
	if _, ok := tt.Find("Agent presets"); ok {
		t.Fatalf("Agent presets nav still offered with Show coding view off: %q", tt.Texts())
	}
	if err := tt.Click("Toggle show coding view"); err != nil {
		t.Fatalf("toggle Show coding view back on: %v", err)
	}
	tt.Frame()
	if !h.app.currentSettings().ShowCodingView {
		t.Fatal("native switch did not persist Show coding view on")
	}

	// The panel is responsive: at the 900x600 minimum window it stays inside
	// the window, with the nav and the close control reachable.
	small := newLiveApp(t, &shellProvider{})
	small.app.Language = LangEN
	small.app.Screen = Session
	stt := ui.NewTester(small.app.View, 900, 600)
	stt.Frame()
	stt.Key(ui.Cmd, ui.KeyComma)
	stt.Frame()
	for _, label := range []string{"Close settings", "General"} {
		r, ok := stt.Find(label)
		if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 900 || r.Y+r.H > 600 {
			t.Fatalf("900x600: %q = %+v, want it inside the window", label, r)
		}
	}
}

// TestSettingsViewRendersStandalone covers the independent settings window's
// view: the same nav and page content the modal hosts, rendered as a whole
// window without the scrim, and its Close controls route to the host instead
// of dismissing an overlay.
func TestSettingsViewRendersStandalone(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	closed := 0
	h.app.SetSettingsWindowCloser(func() { closed++ })
	tt := ui.NewTester(h.app.SettingsView, 900, 720)
	tt.Frame()

	for _, want := range []string{"Settings", "General", "Models", "Built-in plugins", "Language", "Close settings"} {
		if !tt.HasText(want) {
			t.Fatalf("standalone settings view missing %q: %q", want, tt.Texts())
		}
	}
	// The page content is the same General form the modal shows.
	if !tt.HasText("Appearance") || !tt.HasText("Permission") {
		t.Fatalf("standalone settings view lost the General page: %q", tt.Texts())
	}
	// Nav to another page, as in the modal.
	if err := tt.Click("Models"); err != nil {
		t.Fatalf("click Models: %v", err)
	}
	if h.app.shellUI().settingsTab != settingsTabModels {
		t.Fatalf("nav choice = %v, want Models", h.app.shellUI().settingsTab)
	}
	// The Close control asks the host to hide the window; it must not leave
	// Screen=Settings on the main window.
	if err := tt.Click("Close settings"); err != nil {
		t.Fatalf("click Close settings: %v", err)
	}
	if closed != 1 {
		t.Fatalf("Close control called the host %d times, want 1", closed)
	}
	if h.app.Screen == Settings {
		t.Fatal("the settings window's Close switched the main window to the overlay")
	}
	// Escape closes the window too when no settings popover is open.
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if closed != 2 {
		t.Fatalf("Escape called the host %d times total, want 2", closed)
	}

	// Cmd/Ctrl+, focuses the window from inside it (the Windows/Linux menu bar
	// belongs to the main window, whose shortcuts wait while this one has the
	// focus).
	opened := 0
	h.app.SetSettingsWindowOpener(func() bool { opened++; return true })
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if opened != 1 {
		t.Fatalf("Cmd+, inside the settings window called the host %d times, want 1", opened)
	}
}

// TestOpenSettingsRoutesToHostWindow covers Cmd+, and the native menu's
// Settings command: with a host window available they show it, keep the main
// window on its screen, and fall back to the in-app overlay when the host
// reports that it cannot show a window.
func TestOpenSettingsRoutesToHostWindow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	opens := 0
	h.app.SetSettingsWindowOpener(func() bool { opens++; return true })

	if !h.app.HandleMenuAction(MenuActionSettings) {
		t.Fatal("HandleMenuAction did not recognize the settings command")
	}
	if opens != 1 {
		t.Fatalf("host opener calls = %d, want 1", opens)
	}
	if h.app.Screen != Home {
		t.Fatalf("screen = %v, want Home: the settings window is not an overlay", h.app.Screen)
	}

	// A host that cannot show the window falls back to the modal.
	h.app.SetSettingsWindowOpener(func() bool { return false })
	h.app.HandleMenuAction(MenuActionSettings)
	if h.app.Screen != Settings {
		t.Fatalf("fallback screen = %v, want Settings", h.app.Screen)
	}
}

// TestSettingsChangedHookRuns covers the state-sync hook: saving a setting
// notifies the host, which repaints the main window beside the settings one.
func TestSettingsChangedHookRuns(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	changed := 0
	h.app.SetSettingsChangedHook(func() { changed++ })
	h.app.updateSettings(nil, func(cfg *session.Settings) { cfg.FontSize = session.FontSizeLarge })
	if changed != 1 {
		t.Fatalf("settings-changed hook calls = %d, want 1", changed)
	}
	if got := h.app.currentSettings().FontSize; got != session.FontSizeLarge {
		t.Fatalf("saved font size = %d, want large", got)
	}
}

// TestRenderSettingsWindowNativePreview renders the independent settings
// window's view for brand review at its window size (900x720), light and dark.
// previewDir keeps ordinary runs in a temporary directory;
// ZEOKEEL_UPDATE_PREVIEWS=1 refreshes docs/design/preview.
func TestRenderSettingsWindowNativePreview(t *testing.T) {
	dir := previewDir(t)
	for _, tc := range []struct {
		name  string
		theme string
	}{
		{"light", session.ThemeLight},
		{"dark", session.ThemeDark},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangZH
			h.app.Screen = Home
			if err := h.store.SetSetting(session.SettingTheme, tc.theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			img := ui.Render(h.app.SettingsView, 900, 720, 1)
			path := filepath.Join(dir, "settings-window-native-"+tc.name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatalf("encode %s: %v", path, err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close %s: %v", path, err)
			}
			if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
				t.Fatalf("preview %s is empty: %v", path, err)
			}
			t.Logf("wrote %s", path)
		})
	}
}
