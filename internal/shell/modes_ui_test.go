package shell

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
)

// TestAgentModePickerPersistsAndSwitches drives the production live session
// screen (the inline mode chip in liveHeader), not a preview-only row: the picker
// the user actually sees must persist, switch and close.
func TestAgentModePickerPersistsAndSwitches(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	var switched []string
	h.ctrl.SetAgentModeSwitcher(func(mode string) error {
		switched = append(switched, mode)
		return nil
	})

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Standard mode") {
		t.Fatalf("live header mode chip missing: %q", tt.Texts())
	}

	if err := tt.Click("Standard mode"); err != nil {
		t.Fatalf("open live mode picker: %v", err)
	}
	// The runtime's names and descriptions are the picker's single source of
	// truth.
	for _, label := range []string{"Minimal mode", "Creator mode"} {
		if !tt.HasText(label) {
			t.Fatalf("menu entry %q missing: %q", label, tt.Texts())
		}
	}
	for _, hint := range []string{
		"Work with code, files, and information",
		"terminal and image reading, with context compaction",
		"change whitelisted settings and write reusable skills",
	} {
		if !tt.HasText(hint) {
			t.Fatalf("menu description %q missing: %q", hint, tt.Texts())
		}
	}

	if err := tt.Click("Minimal mode"); err != nil {
		t.Fatalf("click Minimal mode: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeMinimal {
		t.Fatalf("Mode() = %q, want minimal", got)
	}
	tt.Frame()
	if !tt.HasText("Minimal mode") {
		t.Fatalf("live chip did not show Minimal mode: %q", tt.Texts())
	}
	key := session.SessionModeKey(h.ctrl.CurrentSessionID())
	if value, err := h.ctrl.Store().GetSetting(key); err != nil || value != runtime.ModeMinimal {
		t.Fatalf("stored mode = %q, %v", value, err)
	}
	if len(switched) != 1 || switched[0] != runtime.ModeMinimal {
		t.Fatalf("switcher calls = %v", switched)
	}

	// A second selection persists again and calls the switcher again.
	if err := tt.Click("Minimal mode"); err != nil {
		t.Fatalf("reopen live picker: %v", err)
	}
	if err := tt.Click("Creator mode"); err != nil {
		t.Fatalf("click Creator mode: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeCreator {
		t.Fatalf("Mode() = %q, want creator", got)
	}
	if len(switched) != 2 || switched[1] != runtime.ModeCreator {
		t.Fatalf("switcher calls = %v", switched)
	}
}

// TestAgentModePickerShowsSwitcherError verifies a failed SetMode on the live
// session screen is visible, not silent: the inline chip applies modes in a
// real session, and the live composer area renders modeUI().err.
func TestAgentModePickerShowsSwitcherError(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.ctrl.SetAgentModeSwitcher(func(string) error { return errors.New("mode rebuild failed") })

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Standard mode"); err != nil {
		t.Fatalf("open live mode picker: %v", err)
	}
	if err := tt.Click("Creator mode"); err != nil {
		t.Fatalf("click Creator mode: %v", err)
	}
	tt.Frame()
	if !tt.HasText("mode rebuild failed") {
		t.Fatalf("live session did not render the switcher error: %q", tt.Texts())
	}
	// The switch was persisted before the hook ran, so the failure is visible
	// but the choice is not silently lost.
	if got := h.ctrl.Mode(); got != runtime.ModeCreator {
		t.Fatalf("Mode() = %q, want creator", got)
	}
}

// modePresetAliases are every non-canonical spelling runtime.NormalizeMode
// accepts. Settings and the Creator settings tool can persist any of them.
var modePresetAliases = []struct {
	alias string
	want  string
}{
	{"cordis", runtime.ModeCreator},
	{"creator-mode", runtime.ModeCreator},
	{"creator_mode", runtime.ModeCreator},
	{"minimal-mode", runtime.ModeMinimal},
	{"minimal_mode", runtime.ModeMinimal},
}

// TestHomeModeNormalizesPersistedPresetAliases proves the Home chip and the
// session it creates agree when agent.preset holds an alias. Previously the
// chip fell back to Standard while the new session started in the normalized
// mode (for example "cordis" starts Creator mode).
func TestHomeModeNormalizesPersistedPresetAliases(t *testing.T) {
	for _, tc := range modePresetAliases {
		tc := tc
		t.Run(tc.alias, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			if err := h.ctrl.Store().SetSetting("agent.preset", tc.alias); err != nil {
				t.Fatalf("seed agent.preset: %v", err)
			}
			h.app.homeMode = ""
			h.app.Screen = Home

			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			wantLabel := h.app.agentModeLabel(tc.want)
			if got := h.app.homeAgentMode(); got != tc.want {
				t.Fatalf("homeAgentMode() = %q, want %q", got, tc.want)
			}
			if got := h.app.agentModeLabel(h.app.homeAgentMode()); got != wantLabel {
				t.Fatalf("Home chip label = %q, want %q", got, wantLabel)
			}
			if !tt.HasText(wantLabel) {
				t.Fatalf("Home chip does not show %q: %q", wantLabel, tt.Texts())
			}

			// The controller normalizes the same preset when the Home composer
			// creates its session; the chip must not contradict it.
			h.app.newSession()
			if got := h.ctrl.Mode(); got != tc.want {
				t.Fatalf("new session mode = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAgentPresetsPageHighlightsPersistedAlias covers the second half of the
// same bug: Settings > Agent presets compares the raw stored value against the
// canonical option list, so an alias lost the "Default" badge and still offered
// "Set default" for the mode that was already stored.
func TestAgentPresetsPageHighlightsPersistedAlias(t *testing.T) {
	for _, tc := range modePresetAliases {
		tc := tc
		t.Run(tc.alias, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			if err := h.ctrl.Store().SetSetting("agent.preset", tc.alias); err != nil {
				t.Fatalf("seed agent.preset: %v", err)
			}
			tt := ui.NewTester(h.app.View, 1600, 1000)
			openSettingsLive(t, tt)
			if err := tt.Click("Agent presets"); err != nil {
				t.Fatalf("open Agent presets: %v", err)
			}
			tt.Frame()

			wantLabel := h.app.agentModeLabel(tc.want)
			if !tt.HasText("Default") {
				t.Fatalf("Default badge missing for agent.preset=%q: %q", tc.alias, tt.Texts())
			}
			if tt.HasText("Set default " + wantLabel) {
				t.Fatalf("agent.preset=%q still offers %q: %q", tc.alias, "Set default "+wantLabel, tt.Texts())
			}
			// A non-selected canonical preset keeps its action, proving the
			// page rendered every row rather than hiding them.
			other := ""
			for _, standard := range []string{runtime.ModeStandard, runtime.ModeMinimal, runtime.ModeCreator} {
				label := h.app.agentModeLabel(standard)
				if label != wantLabel {
					other = label
					break
				}
			}
			if !tt.HasText("Set default " + other) {
				t.Fatalf("non-selected preset lost its action: %q", tt.Texts())
			}
		})
	}
}
