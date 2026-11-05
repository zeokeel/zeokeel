package shell

import (
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
)

// TestBuiltinPluginsTogglesPersistAndHook drives Settings > Built-in plugins:
// every toggle writes plugin.<name>.enabled, reports through the hook, and a
// second click restores it. It then exercises the top-level Plugins page's
// live rows, which share the same settings.
func TestBuiltinPluginsTogglesPersistAndHook(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})

	var (
		mu    sync.Mutex
		calls []struct {
			name    string
			enabled bool
		}
	)
	h.app.SetPluginChangeHook(func(name string, enabled bool) {
		mu.Lock()
		calls = append(calls, struct {
			name    string
			enabled bool
		}{name, enabled})
		mu.Unlock()
	})

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("Built-in plugins"); err != nil {
		t.Fatalf("click Built-in plugins: %v", err)
	}
	for _, label := range []string{"Web search", "Skills", "MCP servers", "Shell tools", "Subagent"} {
		if !tt.HasText(label) {
			t.Fatalf("plugin page missing %q: %q", label, tt.Texts())
		}
	}
	if !tt.HasText("Experimental plugins") {
		t.Fatalf("experimental section missing: %q", tt.Texts())
	}

	// web_search defaults to on; one click persists false and fires the hook.
	tt.Click("Web search")
	if got, err := h.store.GetSetting(session.PluginSettingKey("web_search")); err != nil || got != "false" {
		t.Fatalf("plugin.web_search.enabled = %q, %v; want false", got, err)
	}
	if got, err := h.ctrl.PluginEnabled("web_search"); err != nil || got {
		t.Fatalf("PluginEnabled = %v, %v; want false", got, err)
	}
	if !tt.HasText("Plugin disabled") {
		t.Fatalf("disable toast missing: %q", tt.Texts())
	}
	mu.Lock()
	if len(calls) != 1 || calls[0].name != "web_search" || calls[0].enabled {
		t.Fatalf("hook calls = %+v", calls)
	}
	mu.Unlock()

	// Clicking again turns it back on.
	tt.Click("Web search")
	if got, err := h.ctrl.PluginEnabled("web_search"); err != nil || !got {
		t.Fatalf("PluginEnabled after second click = %v, %v; want true", got, err)
	}
	mu.Lock()
	if len(calls) != 2 || !calls[1].enabled {
		t.Fatalf("hook calls = %+v", calls)
	}
	mu.Unlock()

	// An experimental toggle persists too (default off).
	tt.Click("Agent Teams")
	if got, err := h.store.GetSetting("plugin.agent_teams.enabled"); err != nil || got != "true" {
		t.Fatalf("plugin.agent_teams.enabled = %q, %v; want true", got, err)
	}

	// The top-level Plugins page reads the same keys: toggling Web search
	// there updates the store and the hook.
	tt.Key(0, ui.KeyEscape)
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins nav: %v", err)
	}
	if !tt.HasText("Built-in plugins") {
		t.Fatalf("plugins screen missing: %q", tt.Texts())
	}
	tt.Click("Enable Web search")
	if got, err := h.ctrl.PluginEnabled("web_search"); err != nil || got {
		t.Fatalf("top-level toggle did not persist: %v, %v", got, err)
	}
}

// TestPluginAccessorDefaults covers the controller's persistence surface
// without the UI: unset keys default to on, experimental defaults to off, and
// an empty name is rejected.
func TestPluginAccessorDefaults(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})

	if got, err := h.ctrl.PluginEnabled("skills"); err != nil || !got {
		t.Fatalf("PluginEnabled(unset) = %v, %v; want true", got, err)
	}
	if got, err := h.ctrl.PluginEnabledDefault("auto_review", false); err != nil || got {
		t.Fatalf("PluginEnabledDefault(experimental) = %v, %v; want false", got, err)
	}
	if err := h.ctrl.SetPluginEnabled("skills", false); err != nil {
		t.Fatalf("SetPluginEnabled: %v", err)
	}
	if got, err := h.store.GetSetting("plugin.skills.enabled"); err != nil || got != "false" {
		t.Fatalf("stored toggle = %q, %v; want false", got, err)
	}
	if got, err := h.ctrl.PluginEnabled("skills"); err != nil || got {
		t.Fatalf("PluginEnabled after set = %v, %v; want false", got, err)
	}
	if _, err := h.ctrl.PluginEnabled("  "); err == nil {
		t.Fatal("PluginEnabled accepted an empty name")
	}
	if err := h.ctrl.SetPluginEnabled("", true); err == nil {
		t.Fatal("SetPluginEnabled accepted an empty name")
	}
}

// TestRenderPluginsNativePreview renders the Plugins page for brand review:
// light and dark at the 1600x1000 design size. The PNGs are written to
// docs/design/preview so the page's look is reproducible from the test.
func TestRenderPluginsNativePreview(t *testing.T) {
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
			h.app.Screen = Plugins
			img := ui.Render(h.app.View, 1600, 1000, 1)
			path := filepath.Join(dir, "plugins-native-"+tc.name+".png")
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
