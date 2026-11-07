package host

// Tests for the three-state appearance setting (light/dark/system): the pure
// resolver, the legacy ui.theme.dark fallback and the system-follow listener
// behind an injectable systemTheme/theme seam.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeSystemTheme is a scripted systemTheme.
type fakeSystemTheme struct {
	dark      bool
	handlers  []func()
	unhandled int
}

func (f *fakeSystemTheme) IsDark() bool { return f.dark }

func (f *fakeSystemTheme) OnUpdated(fn func()) func() {
	index := len(f.handlers)
	f.handlers = append(f.handlers, fn)
	removed := false
	return func() {
		if removed {
			return
		}
		removed = true
		f.unhandled++
		if index < len(f.handlers) {
			f.handlers = append(f.handlers[:index], f.handlers[index+1:]...)
		}
	}
}

// fireSystemTheme calls every registered handler once.
func (f *fakeSystemTheme) fire() {
	handlers := append([]func(){}, f.handlers...)
	for _, handler := range handlers {
		handler()
	}
}

// recordingTheme records SetDark calls.
type recordingTheme struct {
	dark  bool
	calls []bool
}

func (t *recordingTheme) Dark() bool { return t.dark }
func (t *recordingTheme) SetDark(dark bool) {
	t.dark = dark
	t.calls = append(t.calls, dark)
}
func (t *recordingTheme) Toggle() bool {
	t.SetDark(!t.dark)
	return t.dark
}

func themeSettings(t *testing.T, values map[string]string) *Settings {
	t.Helper()
	settings, err := NewSettings(t.TempDir())
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

func TestResolveThemeDark(t *testing.T) {
	tests := []struct {
		mode       string
		systemDark bool
		want       bool
	}{
		{"light", true, false},
		{"dark", false, true},
		{"system", true, true},
		{"system", false, false},
		{"", true, true},
		{"  LIGHT  ", true, false},
		{"Dark", false, true},
		{"banana", true, true},
		{"banana", false, false},
	}
	for _, test := range tests {
		if got := resolveThemeDark(test.mode, test.systemDark); got != test.want {
			t.Fatalf("resolveThemeDark(%q, %v) = %v, want %v", test.mode, test.systemDark, got, test.want)
		}
	}
}

func TestInitialThemeDarkLegacyFallback(t *testing.T) {
	tests := []struct {
		name       string
		values     map[string]string
		systemDark bool
		wantDark   bool
		wantFollow bool
	}{
		{name: "no mode follows system", systemDark: true, wantDark: true, wantFollow: true},
		{name: "legacy true", values: map[string]string{settingDark: "true"}, wantDark: true},
		{name: "legacy false", values: map[string]string{settingDark: "false"}, wantDark: false},
		{name: "mode dark wins", values: map[string]string{settingDark: "false", contracts.SettingThemeMode: "dark"}, wantDark: true},
		{name: "mode light wins", values: map[string]string{settingDark: "true", contracts.SettingThemeMode: "light"}, wantDark: false},
		{name: "mode system", values: map[string]string{settingDark: "true", contracts.SettingThemeMode: "system"}, systemDark: false, wantDark: false, wantFollow: true},
		{name: "bad mode follows system", values: map[string]string{settingDark: "true", contracts.SettingThemeMode: "banana"}, systemDark: true, wantDark: true, wantFollow: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := test.values
			if values == nil {
				values = map[string]string{}
			}
			settings := themeSettings(t, values)
			dark, follow := initialThemeDark(settings, test.systemDark)
			if dark != test.wantDark || follow != test.wantFollow {
				t.Fatalf("initialThemeDark = %v/%v, want %v/%v", dark, follow, test.wantDark, test.wantFollow)
			}
		})
	}
}

func TestApplyThemeModeSystemFollowsUpdates(t *testing.T) {
	theme := &recordingTheme{}
	system := &fakeSystemTheme{dark: true}
	settings := themeSettings(t, map[string]string{contracts.SettingThemeMode: "system"})

	off := applyThemeMode(theme, settings, system)
	if len(theme.calls) != 1 || !theme.calls[0] {
		t.Fatalf("initial SetDark calls = %v, want [true]", theme.calls)
	}
	if len(system.handlers) != 1 {
		t.Fatalf("system listeners = %d, want 1", len(system.handlers))
	}
	system.dark = false
	system.fire()
	if len(theme.calls) != 2 || theme.calls[1] != false {
		t.Fatalf("SetDark calls after a system update = %v, want [true false]", theme.calls)
	}
	if off == nil {
		t.Fatal("applyThemeMode(system) returned no unregister function")
	}
	off()
	if system.unhandled != 1 {
		t.Fatalf("unregister calls = %d, want 1", system.unhandled)
	}
	system.fire()
	if len(theme.calls) != 2 {
		t.Fatalf("SetDark calls after unregister = %v, want no new call", theme.calls)
	}
}

func TestApplyThemeModeForcedModesDoNotFollowSystem(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"light", false},
		{"dark", true},
	}
	for _, test := range tests {
		theme := &recordingTheme{}
		system := &fakeSystemTheme{dark: !test.want}
		settings := themeSettings(t, map[string]string{contracts.SettingThemeMode: test.mode})
		off := applyThemeMode(theme, settings, system)
		if len(theme.calls) != 1 || theme.calls[0] != test.want {
			t.Fatalf("mode %s SetDark calls = %v, want [%v]", test.mode, theme.calls, test.want)
		}
		if len(system.handlers) != 0 {
			t.Fatalf("mode %s registered a system listener", test.mode)
		}
		if off != nil {
			t.Fatalf("mode %s returned an unregister function", test.mode)
		}
	}
}
