package session_test

import (
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/session"
)

// TestMemorySettingsDefaults pins the approved defaults: workspace auto, global
// suggest (review-first), background extractor off, interval 6.
func TestMemorySettingsDefaults(t *testing.T) {
	def := session.DefaultSettings()
	if def.MemoryWorkspaceMode != session.MemoryModeAuto {
		t.Fatalf("workspace memory mode default = %q, want %q", def.MemoryWorkspaceMode, session.MemoryModeAuto)
	}
	if def.MemoryGlobalMode != session.MemoryModeSuggest {
		t.Fatalf("global memory mode default = %q, want %q", def.MemoryGlobalMode, session.MemoryModeSuggest)
	}
	if def.MemoryExtractor {
		t.Fatal("background extractor must default off")
	}
	if def.MemoryExtractionInterval != session.MemoryExtractionDefaultInterval {
		t.Fatalf("extraction interval default = %d, want %d",
			def.MemoryExtractionInterval, session.MemoryExtractionDefaultInterval)
	}
}

// TestMemorySettingsNormalize pins corrupt/older values falling back to the
// defaults and the interval clamp.
func TestMemorySettingsNormalize(t *testing.T) {
	def := session.DefaultSettings()

	got := session.NormalizeSettings(session.Settings{
		MemoryWorkspaceMode:      "always",
		MemoryGlobalMode:         "",
		MemoryExtractionInterval: 0,
	})
	if got.MemoryWorkspaceMode != def.MemoryWorkspaceMode {
		t.Fatalf("workspace mode = %q, want %q", got.MemoryWorkspaceMode, def.MemoryWorkspaceMode)
	}
	if got.MemoryGlobalMode != def.MemoryGlobalMode {
		t.Fatalf("global mode = %q, want %q", got.MemoryGlobalMode, def.MemoryGlobalMode)
	}
	if got.MemoryExtractionInterval != def.MemoryExtractionInterval {
		t.Fatalf("interval = %d, want default %d", got.MemoryExtractionInterval, def.MemoryExtractionInterval)
	}

	got = session.NormalizeSettings(session.Settings{
		MemoryWorkspaceMode:      session.MemoryModeOff,
		MemoryGlobalMode:         session.MemoryModeAuto,
		MemoryExtractionInterval: session.MemoryExtractionMaxInterval + 1,
	})
	if got.MemoryWorkspaceMode != session.MemoryModeOff || got.MemoryGlobalMode != session.MemoryModeAuto {
		t.Fatalf("valid modes changed: %+v", got)
	}
	if got.MemoryExtractionInterval != def.MemoryExtractionInterval {
		t.Fatalf("out-of-range interval = %d, want default %d", got.MemoryExtractionInterval, def.MemoryExtractionInterval)
	}

	got = session.NormalizeSettings(session.Settings{MemoryExtractionInterval: 26})
	if got.MemoryExtractionInterval != 26 {
		t.Fatalf("in-range interval = %d, want 26", got.MemoryExtractionInterval)
	}
}

// TestMemorySettingsLoadSaveRoundTrip pins persistence of the four new keys.
func TestMemorySettingsLoadSaveRoundTrip(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})

	got, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if got.MemoryWorkspaceMode != session.MemoryModeAuto || got.MemoryGlobalMode != session.MemoryModeSuggest {
		t.Fatalf("loaded defaults = %+v, want workspace auto/global suggest", got)
	}

	cfg := got
	cfg.MemoryWorkspaceMode = session.MemoryModeOff
	cfg.MemoryGlobalMode = session.MemoryModeAuto
	cfg.MemoryExtractor = true
	cfg.MemoryExtractionInterval = 21
	if err := h.ctrl.Settings().Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	again, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if again != cfg {
		t.Fatalf("Load after Save = %+v, want %+v", again, cfg)
	}
	for key, want := range map[string]string{
		session.SettingMemoryWorkspaceMode:      "off",
		session.SettingMemoryGlobalMode:         "auto",
		session.SettingMemoryExtractor:          "true",
		session.SettingMemoryExtractionInterval: "21",
	} {
		value, err := h.store.GetSetting(key)
		if err != nil {
			t.Fatalf("GetSetting(%q): %v", key, err)
		}
		if value != want {
			t.Fatalf("setting %q = %q, want %q", key, value, want)
		}
	}
}
