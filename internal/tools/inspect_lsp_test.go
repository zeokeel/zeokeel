package tools

import (
	"strings"
	"testing"
)

// TestInspectLSPConfiguration covers the LSP settings surface: the inspect
// settings snapshot exposes lsp.servers and lsp.workspace_config (neither is a
// secret, so the configured command stays readable while the 2 KiB value cap
// still applies), and the plugin catalog reports the LSP toggle default.
func TestInspectLSPConfiguration(t *testing.T) {
	st := openInspectStore(t)
	root := t.TempDir()
	if err := st.SetSetting("lsp.servers", `{"go":{"command":"gopls","args":["serve"]}}`); err != nil {
		t.Fatalf("SetSetting(lsp.servers): %v", err)
	}
	if err := st.SetSetting("lsp.workspace_config", "off"); err != nil {
		t.Fatalf("SetSetting(lsp.workspace_config): %v", err)
	}
	if err := st.SetSetting("plugin.lsp.enabled", "false"); err != nil {
		t.Fatalf("SetSetting(plugin.lsp.enabled): %v", err)
	}

	tool := NewInspectTool(InspectConfig{Store: st, Root: root})
	out := mustInvoke(t, tool, map[string]any{})
	var snap inspectTestSnapshot
	decodeInspectJSON(t, out, &snap)

	servers, ok := snap.Settings["lsp.servers"]
	if !ok {
		t.Fatalf("settings snapshot has no lsp.servers key: %v", snap.Settings)
	}
	if !strings.Contains(servers, "gopls") {
		t.Fatalf("lsp.servers = %q, want the configured command", servers)
	}
	if got := snap.Settings["lsp.workspace_config"]; got != "off" {
		t.Fatalf("lsp.workspace_config = %q, want off", got)
	}

	if found, enabled, source := pluginByName(t, snap, "lsp"); !found || enabled || source != "setting" {
		t.Fatalf("lsp plugin = found:%v enabled:%v source:%q, want true/false/setting", found, enabled, source)
	}
	// An unset LSP toggle reports its default: on, matching the app registry.
	fresh := openInspectStore(t)
	freshTool := NewInspectTool(InspectConfig{Store: fresh, Root: root})
	out = mustInvoke(t, freshTool, map[string]any{})
	decodeInspectJSON(t, out, &snap)
	if found, enabled, source := pluginByName(t, snap, "lsp"); !found || !enabled || source != "default" {
		t.Fatalf("default lsp plugin = found:%v enabled:%v source:%q, want true/true/default", found, enabled, source)
	}
}
