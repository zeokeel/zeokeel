package toolkit

// Regression tests for FR-02: settings_get must not hand the model plaintext
// credentials (the read-only/workspace presets auto-allow the tool), and
// settings_get/settings_set must no longer be auto-allowed by the permission
// presets. Also covers FR-18: the memory tool is no longer a read-only tool.

import (
	"context"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// invokeSettingsGet calls settings_get on an activated toolkit registry.
func invokeSettingsGet(t *testing.T, registry interface {
	Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error)
}, arguments string) rig.ToolResult {
	t.Helper()
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "settings_get", Arguments: arguments})
	if err != nil {
		t.Fatalf("settings_get %s: %v", arguments, err)
	}
	return result
}

func TestSettingsGetMasksCredentialValues(t *testing.T) {
	settings := newMutableSettings(map[string]string{
		contracts.SettingsChatAPIKey:     "sk-provider-SECRET-123",
		contracts.SettingWebSearchAPIKey: "sk-search-SECRET-456",
		"ui.theme":                       "dark",
		contracts.SettingMCPServers: `[{"name":"srv","url":"https://example.test/mcp",` +
			`"env":["GITHUB_TOKEN=ghp_SECRET_789","PLAIN_VALUE=visible"]}]`,
	})
	tools, _ := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)

	for _, key := range []string{contracts.SettingsChatAPIKey, contracts.SettingWebSearchAPIKey} {
		result := invokeSettingsGet(t, tools, `{"key":"`+key+`"}`)
		if strings.TrimSpace(result.Content) != hiddenSettingValue {
			t.Fatalf("settings_get(%s) = %q, want %q", key, result.Content, hiddenSettingValue)
		}
		for _, secret := range []string{"sk-provider-SECRET-123", "sk-search-SECRET-456"} {
			if strings.Contains(result.Content, secret) {
				t.Fatalf("settings_get(%s) leaked %q", key, secret)
			}
		}
	}

	// Non-sensitive settings stay readable.
	if result := invokeSettingsGet(t, tools, `{"key":"ui.theme"}`); result.Content != "dark" {
		t.Fatalf("settings_get(ui.theme) = %q, want dark", result.Content)
	}
	// A missing key still honors the default.
	if result := invokeSettingsGet(t, tools, `{"key":"missing.key","default":"fallback"}`); result.Content != "fallback" {
		t.Fatalf("settings_get(missing) = %q, want fallback", result.Content)
	}
	// An unset credential key is not reported as "set".
	if result := invokeSettingsGet(t, tools, `{"key":"web.search.otherApiKey"}`); strings.TrimSpace(result.Content) == hiddenSettingValue {
		t.Fatalf("unset credential key reported as set")
	}

	// JSON-valued settings are redacted recursively (mcp.servers env rows).
	result := invokeSettingsGet(t, tools, `{"key":"`+contracts.SettingMCPServers+`"}`)
	for _, secret := range []string{"ghp_SECRET_789"} {
		if strings.Contains(result.Content, secret) {
			t.Fatalf("settings_get(%s) leaked %q: %s", contracts.SettingMCPServers, secret, result.Content)
		}
	}
	if !strings.Contains(result.Content, "GITHUB_TOKEN=***") {
		t.Fatalf("mcp.servers env credential not masked: %s", result.Content)
	}
	if !strings.Contains(result.Content, "PLAIN_VALUE=visible") {
		t.Fatalf("non-credential mcp.servers env value was masked: %s", result.Content)
	}
}

// TestSettingsToolsNotAutoAllowed pins FR-02/FR-18 at the shared preset layer:
// settings_get asks in every permission preset and memory is workspace-only.
func TestSettingsToolsNotAutoAllowed(t *testing.T) {
	if contracts.ReadOnlyAutoAllowed("settings_get") || contracts.WorkspaceAutoAllowed("settings_get") {
		t.Fatal("settings_get is still auto-allowed by a permission preset")
	}
	if contracts.ReadOnlyAutoAllowed("memory") {
		t.Fatal("memory (add/remove/clear) is still auto-allowed by the read-only preset")
	}
	if !contracts.WorkspaceAutoAllowed("memory") {
		t.Fatal("memory should stay auto-allowed in the default workspace preset")
	}
	// Existing read-only tools keep working.
	if !contracts.ReadOnlyAutoAllowed("read_file") || !contracts.ReadOnlyAutoAllowed("skill") {
		t.Fatal("read-only preset lost an informational tool")
	}
}
