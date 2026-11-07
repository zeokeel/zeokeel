package regression

// Adversarial audit: the host three-state appearance (ui.theme.mode =
// light/dark/system) with the legacy ui.theme.dark boolean fallback. The host
// package unit-tests the helpers; this file adds the regression-layer branches:
// normalization table, legacy-vs-mode precedence, the system listener wiring and
// its shutdown unsubscribe.
//
// theme_mode.go's helpers are unexported; this test-only file reaches them via
// //go:linkname declarations with structurally identical types (same technique
// as the tray/hotkey/LSP seams). No production code is changed.

import (
	"strings"
	"testing"
	_ "unsafe"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/host"
)

// themeServiceAlias mirrors contracts.ThemeService.
type themeServiceAlias interface {
	Dark() bool
	SetDark(dark bool)
	Toggle() bool
}

// systemThemeAlias mirrors host.systemTheme.
type systemThemeAlias interface {
	IsDark() bool
	OnUpdated(func()) func()
}

// hostResolveThemeDark is host.resolveThemeDark reached by linkname.
//
//go:linkname hostResolveThemeDark github.com/zeokeel/zeokeel/internal/host.resolveThemeDark
func hostResolveThemeDark(mode string, systemDark bool) bool

// hostInitialThemeDark is host.initialThemeDark reached by linkname.
//
//go:linkname hostInitialThemeDark github.com/zeokeel/zeokeel/internal/host.initialThemeDark
func hostInitialThemeDark(settings *host.Settings, systemDark bool) (dark bool, followSystem bool)

// hostApplyThemeMode is host.applyThemeMode reached by linkname.
//
//go:linkname hostApplyThemeMode github.com/zeokeel/zeokeel/internal/host.applyThemeMode
func hostApplyThemeMode(theme themeServiceAlias, settings *host.Settings, system systemThemeAlias) func()

// themeFake implements contracts.ThemeService for the audit.
type themeFake struct {
	dark    bool
	setDark int
}

func (t *themeFake) Dark() bool { return t.dark }
func (t *themeFake) SetDark(dark bool) {
	t.dark = dark
	t.setDark++
}
func (t *themeFake) Toggle() bool {
	t.dark = !t.dark
	return t.dark
}

// systemThemeFake records listeners and their unsubscription.
type systemThemeFake struct {
	dark      bool
	listeners []func()
	offCalls  int
	lastOffID int
}

func (s *systemThemeFake) IsDark() bool { return s.dark }
func (s *systemThemeFake) OnUpdated(fn func()) func() {
	s.listeners = append(s.listeners, fn)
	return func() {
		s.offCalls++
		s.lastOffID = len(s.listeners)
	}
}

// fireThemeUpdate triggers every registered listener.
func (s *systemThemeFake) fireThemeUpdate() {
	for _, fn := range s.listeners {
		fn()
	}
}

// themeTestSettings builds host settings with optional rows.
func themeTestSettings(t *testing.T, values map[string]string) *host.Settings {
	t.Helper()
	settings, err := host.NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	for key, value := range values {
		if err := settings.Set(key, value); err != nil {
			t.Fatalf("Set(%s): %v", key, err)
		}
	}
	return settings
}

// TestThemeResolveTable covers resolveThemeDark: light/dark force the appearance,
// system/empty/unknown follow the desktop, with case/whitespace normalization.
func TestThemeResolveTable(t *testing.T) {
	for _, systemDark := range []bool{false, true} {
		for _, test := range []struct {
			mode string
			want bool
		}{
			{"dark", true}, {"DARK", true}, {" Dark ", true},
			{"light", false}, {"LIGHT", false}, {" Light ", false},
			{"system", systemDark}, {"SYSTEM", systemDark}, {"", systemDark},
			{"bogus", systemDark}, {"  ", systemDark}, {"auto", systemDark},
		} {
			if got := hostResolveThemeDark(test.mode, systemDark); got != test.want {
				t.Fatalf("resolveThemeDark(%q, %v) = %v, want %v", test.mode, systemDark, got, test.want)
			}
		}
	}
}

// TestThemeInitialLegacyFallback covers initialThemeDark: an empty mode falls back
// to the legacy ui.theme.dark boolean (and then stops following the system), a
// mode wins over the legacy key, and forced modes do not follow.
func TestThemeInitialLegacyFallback(t *testing.T) {
	// No settings at all: follow the system.
	if dark, follow := hostInitialThemeDark(nil, true); !dark || !follow {
		t.Fatalf("nil settings = (%v, %v), want (true, true))", dark, follow)
	}

	// No mode, no legacy key: follow the system (both desktop appearances).
	for _, systemDark := range []bool{false, true} {
		settings := themeTestSettings(t, nil)
		if dark, follow := hostInitialThemeDark(settings, systemDark); dark != systemDark || !follow {
			t.Fatalf("empty settings/systemDark=%v = (%v, %v)", systemDark, dark, follow)
		}
	}

	// Legacy boolean fallback: forced, no system follow, in both directions.
	for _, legacy := range []string{"true", " false ", "banana"} {
		settings := themeTestSettings(t, map[string]string{contracts.SettingsUIThemeDark: legacy})
		dark, follow := hostInitialThemeDark(settings, true)
		wantDark := strings.TrimSpace(legacy) == "true"
		if follow {
			t.Fatalf("legacy %q still follows the system", legacy)
		}
		if dark != wantDark {
			t.Fatalf("legacy %q -> dark %v, want %v", legacy, dark, wantDark)
		}
	}

	// A mode wins over the legacy boolean.
	settings := themeTestSettings(t, map[string]string{
		contracts.SettingsUIThemeDark: "true",
		contracts.SettingThemeMode:    "light",
	})
	if dark, follow := hostInitialThemeDark(settings, true); dark || follow {
		t.Fatalf("mode light + legacy true = (%v, %v), want forced light", dark, follow)
	}
	settings = themeTestSettings(t, map[string]string{
		contracts.SettingsUIThemeDark: "false",
		contracts.SettingThemeMode:    "dark",
	})
	if dark, follow := hostInitialThemeDark(settings, false); !dark || follow {
		t.Fatalf("mode dark + legacy false = (%v, %v), want forced dark", dark, follow)
	}

	// System/unknown modes follow the desktop even with a legacy row present.
	settings = themeTestSettings(t, map[string]string{
		contracts.SettingsUIThemeDark: "true",
		contracts.SettingThemeMode:    "system",
	})
	if dark, follow := hostInitialThemeDark(settings, false); dark || !follow {
		t.Fatalf("mode system + legacy true = (%v, %v), want follow-system light", dark, follow)
	}
	settings = themeTestSettings(t, map[string]string{contracts.SettingThemeMode: "bogus"})
	if _, follow := hostInitialThemeDark(settings, true); !follow {
		t.Fatal("an unknown mode does not follow the system")
	}
}

// TestThemeApplySystemListenerAndUnsubscribe covers applyThemeMode: system mode
// registers the desktop listener and applies its updates; forced modes register
// nothing; the returned hook unsubscribes the listener.
func TestThemeApplySystemListenerAndUnsubscribe(t *testing.T) {
	theme := &themeFake{}
	system := &systemThemeFake{dark: true}
	settings := themeTestSettings(t, map[string]string{contracts.SettingThemeMode: "system"})
	off := hostApplyThemeMode(theme, settings, system)
	if off == nil {
		t.Fatal("system mode returned no unsubscribe hook")
	}
	if !theme.dark || theme.setDark != 1 {
		t.Fatalf("system mode theme = dark %v (sets %d), want dark once", theme.dark, theme.setDark)
	}
	if len(system.listeners) != 1 {
		t.Fatalf("system mode listeners = %d, want 1", len(system.listeners))
	}

	// Desktop appearance changes are applied to the theme.
	system.dark = false
	system.fireThemeUpdate()
	if theme.dark || theme.setDark != 2 {
		t.Fatalf("system update theme = dark %v (sets %d), want light after update", theme.dark, theme.setDark)
	}
	system.dark = true
	system.fireThemeUpdate()
	if !theme.dark {
		t.Fatal("second system update did not restore dark")
	}

	// The shutdown hook unsubscribes the listener.
	off()
	if system.offCalls != 1 {
		t.Fatalf("unsubscribe calls = %d, want 1", system.offCalls)
	}

	// Forced modes: apply once, register no listener, no hook.
	for _, mode := range []string{"light", "dark"} {
		forcedTheme := &themeFake{}
		forcedSystem := &systemThemeFake{dark: true}
		forcedSettings := themeTestSettings(t, map[string]string{contracts.SettingThemeMode: mode})
		if off := hostApplyThemeMode(forcedTheme, forcedSettings, forcedSystem); off != nil {
			t.Fatalf("forced mode %q returned an unsubscribe hook", mode)
		}
		if len(forcedSystem.listeners) != 0 {
			t.Fatalf("forced mode %q registered a system listener", mode)
		}
		wantDark := mode == "dark"
		if forcedTheme.dark != wantDark {
			t.Fatalf("forced mode %q theme dark = %v", mode, forcedTheme.dark)
		}
	}

	// Nil collaborators are safe.
	if off := hostApplyThemeMode(nil, settings, system); off != nil {
		t.Fatal("nil theme returned a hook")
	}
	if off := hostApplyThemeMode(theme, settings, nil); off != nil {
		t.Fatal("nil system theme returned a hook")
	}
}
