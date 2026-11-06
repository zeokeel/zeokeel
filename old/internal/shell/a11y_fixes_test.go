package shell

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestEmptySendIsDisabled covers the composer Send control in both live
// screens: with an empty draft it must not take keyboard focus and pressing it
// must not submit or create a session.
func TestEmptySendIsDisabled(t *testing.T) {
	t.Run("home", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Home
		h.app.Language = LangEN
		h.app.draft = ""
		before, err := h.ctrl.ListSessions(context.Background())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if seen := focusedDuringTab(tt, []string{"Send message"}, 200); seen["Send message"] {
			t.Fatal("empty Home Send is still focusable")
		}
		if err := tt.Click("Send message"); err != nil {
			t.Fatalf("Send message not rendered: %v", err)
		}
		tt.Frame()
		after, err := h.ctrl.ListSessions(context.Background())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("empty Home Send created a session: %d -> %d", len(before), len(after))
		}
	})

	t.Run("session", func(t *testing.T) {
		provider := &shellProvider{}
		h := newLiveApp(t, provider)
		h.app.Screen = Session
		h.app.Language = LangEN
		h.app.draft = ""
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if seen := focusedDuringTab(tt, []string{"Send"}, 200); seen["Send"] {
			t.Fatal("empty Session Send is still focusable")
		}
		if err := tt.Click("Send"); err != nil {
			t.Fatalf("Send not rendered: %v", err)
		}
		tt.Frame()
		if got := provider.callCount(); got != 0 {
			t.Fatalf("empty Session Send submitted %d model call(s)", got)
		}
	})
}

// TestMoreFooterIsFullWidthAccountMenu covers the sidebar account menu: the
// trigger is named and focusable, and the whole footer row is its hit target
// rather than just the left 40px.
func TestMoreFooterIsFullWidthAccountMenu(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Account menu") {
		t.Fatalf("More trigger has no Account menu label: %v", tt.Texts())
	}
	if seen := focusedDuringTab(tt, []string{"More"}, 300); !seen["More"] {
		t.Fatal("More/account menu trigger is not keyboard focusable")
	}
	for _, x := range []float32{150, 250} {
		if err := tt.Click("More"); err == nil {
			// close if the previous click left it open
			tt.Frame()
			if h.app.moreOpen {
				tt.Key(0, ui.KeyEscape)
				tt.Frame()
			}
		}
		tt.ClickAt(x, 974)
		tt.Frame()
		if !h.app.moreOpen {
			t.Fatalf("click at x=%v did not open the account menu", x)
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
	}
}

// TestSidebarMenusKeyboardReachable covers the account popover and the home
// selector popovers (mode, workspace, view options).
func TestSidebarMenusKeyboardReachable(t *testing.T) {
	t.Run("account", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Home
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("More"); err != nil {
			t.Fatalf("click More: %v", err)
		}
		tt.Frame()
		seen := focusedDuringTab(tt, []string{"Settings", "About", "Quit"}, 300)
		if !seen["Settings"] || !seen["About"] || !seen["Quit"] {
			t.Fatalf("account menu items not keyboard focusable: %v", seen)
		}
	})

	t.Run("mode", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Home
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("Standard mode"); err != nil {
			t.Fatalf("click mode chip: %v", err)
		}
		tt.Frame()
		if seen := focusedDuringTab(tt, []string{"Minimal mode"}, 300); !seen["Minimal mode"] {
			t.Fatal("agent mode options are not keyboard focusable")
		}
	})

	t.Run("workspace", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Home
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("Workspace, current: test"); err != nil {
			t.Fatalf("click workspace chip: %v", err)
		}
		tt.Frame()
		if seen := focusedDuringTab(tt, []string{"Switch to test"}, 300); !seen["Switch to test"] {
			t.Fatal("workspace picker rows are not keyboard focusable")
		}
	})

	t.Run("view options", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Home
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("View options"); err != nil {
			t.Fatalf("click View options: %v", err)
		}
		tt.Frame()
		if seen := focusedDuringTab(tt, []string{"Manual"}, 300); !seen["Manual"] {
			t.Fatal("view options rows are not keyboard focusable")
		}
		// Choosing dismisses the popover, as DSH does.
		if err := tt.Click("Manual"); err != nil {
			t.Fatalf("click Manual: %v", err)
		}
		tt.Frame()
		if h.app.viewOptionsOpen {
			t.Fatal("view options popover stayed open after a choice")
		}
	})
}

// TestModelActionsKeyboardReachable covers Settings > Models Add/Edit/Delete.
// Set default is only on non-default rows (the default row's no-op action was
// removed), so it is not part of the single-model surface.
func TestModelActionsKeyboardReachable(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if _, err := h.ctrl.Models().Create(store.Model{Name: "Alpha", Model: "alpha-1", BaseURL: "https://api.example.com"}); err != nil {
		t.Fatalf("create model: %v", err)
	}
	h.app.Screen = Settings
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Models"); err != nil {
		t.Fatalf("open Models: %v", err)
	}
	tt.Frame()
	seen := focusedDuringTab(tt, []string{"Add model", "Edit", "Delete"}, 400)
	for _, label := range []string{"Add model", "Edit", "Delete"} {
		if !seen[label] {
			t.Errorf("model action %q is not keyboard focusable", label)
		}
	}
	if tt.HasText("Set default") {
		t.Fatalf("the default model row still offers a no-op Set default")
	}
}

// TestTurnNavKeyboardReachable covers the session turn rail.
func TestTurnNavKeyboardReachable(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	id := h.ctrl.CurrentSessionID()
	for _, text := range []string{"first", "second"} {
		if err := h.ctrl.Submit(context.Background(), id, text); err != nil {
			t.Fatalf("Submit %q: %v", text, err)
		}
		waitIdle(t, h.ctrl)
	}
	h.ctrl.Wait()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if _, ok := tt.Find("Jump to turn 1"); !ok {
		t.Fatalf("no turn nav rendered: %v", tt.Texts())
	}
	if seen := focusedDuringTab(tt, []string{"Jump to turn 1", "Jump to turn 2"}, 400); !seen["Jump to turn 1"] || !seen["Jump to turn 2"] {
		t.Fatalf("turn nav is not keyboard focusable: %v", seen)
	}
}

// TestChooseShellKeyboardReachable covers both Choose shell triggers: the
// terminal header and the Start card's chevron.
func TestChooseShellKeyboardReachable(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = terminalTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if _, ok := tt.Find("Choose shell"); !ok {
		t.Fatalf("terminal Choose shell missing: %v", tt.Texts())
	}
	if seen := focusedDuringTab(tt, []string{"Choose shell"}, 300); !seen["Choose shell"] {
		t.Fatal("terminal Choose shell is not keyboard focusable")
	}

	h.app.rightTab = startTab
	tt.Frame()
	if _, ok := tt.Find("Choose shell"); !ok {
		t.Fatalf("Start card Choose shell missing: %v", tt.Texts())
	}
	if err := tt.Click("Choose shell"); err == nil {
		tt.Frame()
		if !h.app.startUI().shellMenuOpen {
			t.Fatal("Start card Choose shell did not open its menu")
		}
	}
}

// TestPerformanceUsageChangesSessionView covers the Performance & usage
// setting: Standard drops the detailed step/output/performance segments.
func TestPerformanceUsageChangesSessionView(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()

	render := func(perf string) []string {
		cfg := session.DefaultSettings()
		cfg.PerformanceUsage = perf
		h.app.settingsLoaded = true
		h.app.settingsValue = cfg
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		return tt.Texts()
	}
	detailed := render(session.PerformanceDetailed)
	standard := render(session.PerformanceStandard)
	if reflect.DeepEqual(detailed, standard) {
		t.Fatal("Performance & usage Standard and Detailed render identical session text")
	}
	if !containsText(standard, "1 turn") {
		t.Fatalf("Standard footer lost the turn count: %v", standard)
	}
	if containsText(standard, "out tok") || containsText(standard, "step") {
		t.Fatalf("Standard footer still shows detailed segments: %v", standard)
	}
}

func containsText(texts []string, want string) bool {
	for _, text := range texts {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

// TestDarkAccentBadges guards the dark-theme accent pills and status colors
// under the brand theme: the tag accent and the primary accent keep WCAG AA
// contrast on the dark surfaces, the plugin "More" action stays readable, and
// the danger color is a theme-aware semantic red with AA text contrast instead
// of the retired DSH hex samples (#EC1313 / #F25A5A).
func TestDarkAccentBadges(t *testing.T) {
	dark := design.Dark()
	if homeTagAccent(dark) == homeTagAccent(design.Light()) {
		t.Errorf("reasoning-effort tag accent is not theme-aware: %v", homeTagAccent(dark))
	}
	for _, bg := range []ui.Color{dark.BgBase, dark.BgRaised} {
		if got := contrastRatio(homeTagAccent(dark), bg); got < 4.5 {
			t.Errorf("dark tag accent %v on %v contrast = %.2f, want >= 4.5",
				homeTagAccent(dark), bg, got)
		}
		if got := contrastRatio(dark.Accent, bg); got < 4.5 {
			t.Errorf("dark accent on %v contrast = %.2f, want >= 4.5", bg, got)
		}
	}
	// The plugin More action uses the theme's tertiary label, not a sampled
	// DSH hex, and that role keeps AA contrast on the plugin cards.
	for _, tokens := range []*design.Tokens{design.Light(), dark} {
		if got := pluginMoreColor(tokens); got != tokens.LabelTertiary {
			t.Errorf("plugin More = %v, want the brand LabelTertiary %v", got, tokens.LabelTertiary)
		}
		if got := contrastRatio(pluginMoreColor(tokens), tokens.BgRaised); got < 4.5 {
			t.Errorf("plugin More %v on %v contrast = %.2f, want >= 4.5",
				pluginMoreColor(tokens), tokens.BgRaised, got)
		}
	}
	// Danger is a semantic status color: AA text contrast on the page in both
	// themes, and it adapts to the appearance instead of one DSH hex sample.
	if design.Light().Danger == dark.Danger {
		t.Errorf("danger color is not theme-aware: %v", design.Light().Danger)
	}
	for _, tokens := range []*design.Tokens{design.Light(), dark} {
		if got := contrastRatio(tokens.Danger, tokens.BgBase); got < 4.5 {
			t.Errorf("danger on %v contrast = %.2f, want >= 4.5", tokens.BgBase, got)
		}
	}
	for _, tokens := range []*design.Tokens{design.Light(), dark} {
		if got := contrastRatio(tokens.WarningText, tokens.BgBase); got < 4.5 {
			t.Errorf("warning text on %v contrast = %.2f, want >= 4.5", tokens.BgBase, got)
		}
	}
}
