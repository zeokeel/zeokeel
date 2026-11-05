package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// requireExactToolSet asserts that got contains exactly want, independent of
// order. It is the strict form of requireTools used by the acceptance tests:
// an extra tool is a failure, not just a missing one.
func requireExactToolSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s tools = %v, want exactly %v", label, got, want)
	}
	wanted := make(map[string]bool, len(want))
	for _, name := range want {
		if wanted[name] {
			t.Fatalf("%s want list repeats %q", label, name)
		}
		wanted[name] = true
	}
	seen := make(map[string]bool, len(got))
	for _, name := range got {
		if !wanted[name] {
			t.Fatalf("%s tools = %v, unexpected %q (want exactly %v)", label, got, name, want)
		}
		if seen[name] {
			t.Fatalf("%s tools = %v, duplicate %q", label, got, name)
		}
		seen[name] = true
	}
} // TestMinimalToolRestriction pins Minimal mode to DSH's exact preset:
// terminal_open, terminal_list, terminal_read, terminal_send, terminal_signal,
// terminal_close, read_image and working_directory. Every other registry tool
// is hidden, including a terminal_-prefixed tool that is not one of the six
// DSH terminal tools. The pure ModeTools helper must agree with the live agent.
func TestMinimalToolRestriction(t *testing.T) {
	minimal := []string{
		"terminal_open",
		"terminal_list",
		"terminal_read",
		"terminal_send",
		"terminal_signal",
		"terminal_close",
		"read_image",
		"working_directory",
	}
	forbidden := []string{
		"bash", "read_file", "write_file", "edit_file", "glob", "grep",
		"search_file", "settings_get", "settings_set",
		"terminal_debug", "terminal_run", "terminal_manager",
	}
	reg := NewRegistry()
	for _, name := range append(append([]string(nil), minimal...), forbidden...) {
		name := name
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	agent := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
		Tools:     reg,
		Mode:      ModeMinimal,
	})
	requireExactToolSet(t, "minimal", agent.ToolNames(), minimal)
	for _, name := range forbidden {
		if tool, ok := agent.ToolByName(name); ok {
			t.Fatalf("minimal ToolByName(%q) = %q, want hidden", name, tool.Name())
		}
	}

	available := make([]string, 0, len(minimal)+len(forbidden))
	available = append(available, minimal...)
	available = append(available, forbidden...)
	requireExactToolSet(t, "ModeTools(minimal)", ModeTools(ModeMinimal, available), minimal)

	// Standard keeps the ordinary tools and the registry's extra terminal_*
	// stub, while still hiding the creator-only settings pair.
	standard := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
		Tools:     reg,
		Mode:      ModeStandard,
	})
	standardNames := standard.ToolNames()
	for _, name := range []string{"bash", "read_file", "terminal_open", "terminal_debug"} {
		if !hasTool(standardNames, name) {
			t.Fatalf("standard tools = %v, want %q", standardNames, name)
		}
	}
	for _, name := range []string{"settings_get", "settings_set"} {
		if hasTool(standardNames, name) {
			t.Fatalf("standard tools = %v, must hide %q", standardNames, name)
		}
	}
}

// TestCreatorSettingsRoundTrip proves Creator mode owns exactly Standard plus
// settings_get/settings_set, and that the pair round-trips whitelisted keys
// through the SettingsStore while rejecting secret and non-whitelisted names.
func TestCreatorSettingsRoundTrip(t *testing.T) {
	settings := &fakeSettings{}
	reg := NewRegistry()
	for _, name := range []string{"bash", "read_file", "read_image", "working_directory"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	newCreator := func() *Agent {
		t.Helper()
		return newTestAgent(t, Config{
			einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
			Tools:     reg,
			Mode:      ModeCreator,
			Settings:  settings,
		})
	}
	agent := newCreator()
	if !hasTool(agent.ToolNames(), "bash") {
		t.Fatalf("creator tools = %v, want the standard tools", agent.ToolNames())
	}
	set, ok := agent.ToolByName("settings_set")
	if !ok {
		t.Fatalf("creator tools = %v, want settings_set", agent.ToolNames())
	}
	get, ok := agent.ToolByName("settings_get")
	if !ok {
		t.Fatalf("creator tools = %v, want settings_get", agent.ToolNames())
	}

	ctx := context.Background()
	cases := []struct {
		key   string
		value string
		store string
	}{
		{key: "ui.theme", value: `"dark"`, store: "dark"},
		{key: "ui.font_size", value: "14", store: "14"},
		{key: "plugin.web_search.enabled", value: "true", store: "true"},
	}
	for _, tc := range cases {
		if _, err := set.Invoke(ctx, json.RawMessage(`{"key":"`+tc.key+`","value":`+tc.value+`}`)); err != nil {
			t.Fatalf("settings_set(%s): %v", tc.key, err)
		}
		got, err := get.Invoke(ctx, json.RawMessage(`{"key":"`+tc.key+`"}`))
		if err != nil {
			t.Fatalf("settings_get(%s): %v", tc.key, err)
		}
		if got != tc.store {
			t.Fatalf("settings_get(%s) = %q, want %q", tc.key, got, tc.store)
		}
		settings.mu.Lock()
		stored := settings.values[tc.key]
		settings.mu.Unlock()
		if stored != tc.store {
			t.Fatalf("store[%s] = %q, want %q", tc.key, stored, tc.store)
		}
	}

	// A rebuilt Creator agent reads the same persisted values.
	rebuilt := newCreator()
	rebuiltGet, ok := rebuilt.ToolByName("settings_get")
	if !ok {
		t.Fatalf("rebuilt creator tools = %v, want settings_get", rebuilt.ToolNames())
	}
	if got, err := rebuiltGet.Invoke(ctx, json.RawMessage(`{"key":"ui.theme"}`)); err != nil || got != "dark" {
		t.Fatalf("rebuilt settings_get(ui.theme) = %q, %v; want dark", got, err)
	}

	for _, key := range []string{"provider.api_key", "ui.auth_token", "not.a.real.setting"} {
		if _, err := set.Invoke(ctx, json.RawMessage(`{"key":"`+key+`","value":"leak"}`)); err == nil {
			t.Fatalf("settings_set accepted non-whitelisted key %q", key)
		}
		if _, err := get.Invoke(ctx, json.RawMessage(`{"key":"`+key+`"}`)); err == nil {
			t.Fatalf("settings_get accepted non-whitelisted key %q", key)
		}
	}

	// The settings pair is Creator-only: Standard hides it.
	for _, mode := range []string{ModeStandard} {
		other := newTestAgent(t, Config{
			einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
			Tools:     reg,
			Mode:      mode,
			Settings:  settings,
		})
		for _, name := range []string{"settings_get", "settings_set"} {
			if _, ok := other.ToolByName(name); ok {
				t.Fatalf("%s tools = %v, must hide %q", mode, other.ToolNames(), name)
			}
		}
	}
}
