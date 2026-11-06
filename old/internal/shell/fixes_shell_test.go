package shell

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

func assertRectInside(t *testing.T, tt *ui.Tester, label string, w, h int) ui.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("%q not found: %q", label, tt.Texts())
	}
	if r.W <= 0 || r.H <= 0 {
		t.Fatalf("%q has no box: %+v", label, r)
	}
	if r.X < 0 || r.Y < 0 || r.X+r.W > float32(w) || r.Y+r.H > float32(h) {
		t.Fatalf("%q is outside the %dx%d window: %+v", label, w, h, r)
	}
	return r
}

// TestSettingsOverlayKeepsScreen pins the fix for the Settings navigation bug:
// Settings is a modal over the current screen, and Close/Esc return to it.
func TestSettingsOverlayKeepsScreen(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Chat") || !tt.HasText("Trajectory") {
		t.Fatalf("session content missing before Settings: %q", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("Cmd+, screen = %v, want Settings", h.app.Screen)
	}
	if h.app.backgroundScreen() != Session {
		t.Fatalf("settings background = %v, want Session", h.app.backgroundScreen())
	}
	if !tt.HasText("Chat") || !tt.HasText("Trajectory") {
		t.Fatalf("session content disappeared behind Settings: %q", tt.Texts())
	}
	if err := tt.Click("Close settings"); err != nil {
		t.Fatalf("click Close settings: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Session {
		t.Fatalf("Close settings returned to %v, want Session", h.app.Screen)
	}
	// Escape follows the same path.
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.Screen != Session {
		t.Fatalf("Esc returned to %v, want Session", h.app.Screen)
	}
}

// TestSettingsFitsMinimumWindow covers the 900x600 minimum: the panel is
// clamped to the viewport and its fixed header/first nav cell stay visible.
func TestSettingsFitsMinimumWindow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	for _, label := range []string{"Settings", "General", "Close settings"} {
		assertRectInside(t, tt, label, 900, 600)
	}
	// The body scrolls: the last settings row is reachable at this height.
	last := "Not available in this build: no session-log upload endpoint is configured."
	r, ok := tt.Find(last)
	if !ok {
		t.Fatalf("last settings row missing: %q", tt.Texts())
	}
	for i := 0; i < 8 && (r.Y >= 600 || r.H <= 0); i++ {
		tt.Wheel(800, 380, 0, 600)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
		r, _ = tt.Find(last)
	}
	if r.Y < 0 || r.Y >= 600 || r.H <= 0 {
		t.Fatalf("last settings row not reachable by scrolling: %+v", r)
	}
}

// TestRightSidebarHeaderFitsMinimumWindow covers the 900x600 right-panel
// header: all four controls remain inside the window and functional.
func TestRightSidebarHeaderFitsMinimumWindow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("open right sidebar: %v", err)
	}
	tt.Frame()
	for _, label := range []string{"New tab", "Split", "Fullscreen", "Collapse right sidebar"} {
		assertRectInside(t, tt, label, 900, 600)
	}
	// The panel's own control (unique now that the Session header's toggle is
	// named differently) must be a real hit target, not the old 8x28 sliver.
	collapse := assertRectInside(t, tt, "Collapse right sidebar", 900, 600)
	if collapse.W < 24 || collapse.H < 24 {
		t.Fatalf("panel collapse target = %+v, want at least 24x24", collapse)
	}
	if err := tt.Click("Split"); err != nil {
		t.Fatalf("click Split: %v", err)
	}
	tt.Frame()
	if !h.app.rightSplit {
		t.Fatal("Split did not enable split mode at 900x600")
	}
	if err := tt.Click("Split"); err != nil {
		t.Fatalf("toggle Split: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Fullscreen"); err != nil {
		t.Fatalf("click Fullscreen: %v", err)
	}
	tt.Frame()
	if !h.app.rightFullscreen {
		t.Fatal("Fullscreen did not maximize at 900x600")
	}
	if err := tt.Click("Exit fullscreen"); err != nil {
		t.Fatalf("exit fullscreen: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Collapse right sidebar"); err != nil {
		t.Fatalf("collapse right sidebar: %v", err)
	}
	tt.Frame()
	if h.app.rightOpen {
		t.Fatal("Collapse right sidebar left the panel open")
	}
}

// TestAutomationScrollsAtMinimumHeight proves the task list scrolls instead of
// clipping the later rows at 900x600.
func TestAutomationScrollsAtMinimumHeight(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	for i := 0; i < 10; i++ {
		if _, err := h.ctrl.Automations().Create(store.Automation{
			Title: fmt.Sprintf("Task %02d", i), Cron: "@hourly", Enabled: true,
		}); err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
	}
	h.app.Screen = Automation
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()
	r, ok := tt.Find("Task 09")
	if !ok {
		t.Fatalf("Task 09 missing: %q", tt.Texts())
	}
	for i := 0; i < 10 && (r.Y+r.H <= 0 || r.Y >= 600 || r.H <= 0); i++ {
		tt.Wheel(800, 400, 0, 600)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
		r, _ = tt.Find("Task 09")
	}
	if r.Y < 0 || r.Y >= 600 || r.H <= 0 {
		t.Fatalf("Task 09 never became visible after scrolling: %+v", r)
	}
}

// TestPluginsScrollsAtMinimumHeight proves the Developer Tools row is
// reachable by scrolling the plugin page at 900x600.
func TestPluginsScrollsAtMinimumHeight(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Plugins
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()
	r, ok := tt.Find("Developer Tools")
	if !ok {
		t.Fatalf("Developer Tools missing: %q", tt.Texts())
	}
	for i := 0; i < 10 && (r.Y+r.H <= 0 || r.Y >= 600 || r.H <= 0); i++ {
		tt.Wheel(800, 350, 0, 600)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
		r, _ = tt.Find("Developer Tools")
	}
	if r.Y < 0 || r.Y >= 600 || r.H <= 0 {
		t.Fatalf("Developer Tools never became visible after scrolling: %+v", r)
	}
}

// TestHomeComposerInputFillsRow guards the live Home composer: the input used
// to shrink to ~216px. It must still fill the composer card it sits in (the
// card itself is now the brand's theme-derived column, not the retired 877px
// width).
func TestHomeComposerInputFillsRow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	r, ok := tt.Find("Composer input")
	if !ok {
		t.Fatalf("Home composer input missing: %q", tt.Texts())
	}
	card, ok := tt.Find("Home composer")
	if !ok {
		t.Fatalf("Home composer card missing: %q", tt.Texts())
	}
	// The card pads by design.Space(3) on each side; the input fills the rest.
	if want := card.W - 2*design.Space3 - 4; r.W < want {
		t.Fatalf("Home composer input width = %v, want >= %v inside the %v card", r.W, want, card.W)
	}
}

// TestLiveSessionIdleHidesStatus checks DSH's idle header: no "idle" text.
func TestLiveSessionIdleHidesStatus(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if tt.HasText("idle") {
		t.Fatalf("idle session header still shows a status label: %q", tt.Texts())
	}
}

// TestSettingsParityRowsPresent covers the two DSH rows the page was missing.
func TestSettingsParityRowsPresent(t *testing.T) {
	// The parity rows are rendered by the live Settings overlay, so drive the
	// real shortcut instead of the retired static Settings screen.
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("Cmd+, screen = %v, want Settings", h.app.Screen)
	}
	for _, want := range []string{
		"Performance & usage",
		"Upload Session Log when using the official model API",
	} {
		if !tt.HasText(want) {
			t.Fatalf("settings missing %q: %q", want, tt.Texts())
		}
	}

	for i := 0; i < 8; i++ {
		if r, ok := tt.Find("Upload session log"); ok && r.Y >= 0 && r.Y+r.H <= 1000 && r.W > 0 {
			break
		}
		tt.Wheel(800, 500, 0, 600)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
	}
	if !tt.HasText("Not available in this build: no session-log upload endpoint is configured.") {
		t.Fatalf("upload row does not state it is unavailable: %v", tt.Texts())
	}
	// The switch is disabled: clicking it must not persist an inert
	// preference. There is no official-API upload path in this build.
	if err := tt.Click("Upload session log"); err != nil {
		t.Fatalf("upload switch not rendered: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UploadSessionLog {
		t.Fatal("disabled Upload session log switch persisted a no-op setting")
	}
}

// TestPluginDetailTitleIsLocalized covers the mixed "子代理 settings" title.
func TestPluginDetailTitleIsLocalized(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	h.app.Screen = Settings
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("内置插件"); err != nil {
		t.Fatalf("click Built-in plugins: %v", err)
	}
	tt.Frame()
	if err := tt.Click("配置子代理"); err != nil {
		t.Fatalf("click localized Configure 子代理: %v", err)
	}
	tt.Frame()
	if !tt.HasText("子代理设置") {
		t.Fatalf("localized settings title missing: %q", tt.Texts())
	}
	if tt.HasText("子代理 settings") {
		t.Fatalf("mixed-language title still rendered: %q", tt.Texts())
	}
	if err := tt.Click(zh("取消")); err != nil {
		// The cancel button is localized; fall back to the visible text lookup
		// only when it differs.
		t.Logf("cancel click: %v", err)
	}
}

func zh(s string) string { return s }

// TestHomeTagContrast checks the model-effort tag accent (the brand accent)
// against the page background in both appearances. The Preview badge this
// used to cover was removed with the DSH hero in the native Home redesign.
func TestHomeTagContrast(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tokens *design.Tokens
	}{
		{"light", design.Light()},
		{"dark", design.Dark()},
	} {
		if got := contrastRatio(homeTagAccent(tc.tokens), tc.tokens.BgBase); got < 4.5 {
			t.Errorf("%s tag contrast = %.2f, want >= 4.5", tc.name, got)
		}
	}
}

// TestNoAgentSubmitIsSurfaced checks the composer no longer drops
// ErrNoAgent: the user sees a toast that names the fix instead of nothing.
func TestNoAgentSubmitIsSurfaced(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	h.ctrl.SetAgent(nil, nil)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("please send")
	tt.Frame()
	if err := tt.Click("Send message"); err != nil {
		t.Fatalf("click Send: %v", err)
	}
	tt.Frame()
	if !tt.HasText("No agent configured") {
		t.Fatalf("ErrNoAgent was not surfaced: %q", tt.Texts())
	}
}
