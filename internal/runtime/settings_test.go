package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestSettingsKeyWhitelist(t *testing.T) {
	allowed := []string{
		"ui.language", "ui.theme", "agent.preset", "agent.collaboration_mode",
		"plugin.shell.enabled", "skills.root", "approval.tools", "model.default",
		"provider.model", "websearch.endpoint", "mcp.servers",
	}
	for _, key := range allowed {
		if !SettingsKeyAllowed(key) {
			t.Errorf("SettingsKeyAllowed(%q) = false, want true", key)
		}
	}
	rejected := []string{
		"", "UI.Language", "../etc/passwd", "ui language",
		"provider.api_key", "provider.apikey", "websearch.api_key",
		"mcp.token", "session.secret.value", "password",
	}
	for _, key := range rejected {
		if SettingsKeyAllowed(key) {
			t.Errorf("SettingsKeyAllowed(%q) = true, want false", key)
		}
	}
}

func TestSettingsToolsRequireAStore(t *testing.T) {
	get := NewSettingsGetTool(nil)
	set := NewSettingsSetTool(nil)
	if _, err := get.Invoke(nil, []byte(`{"key":"ui.language"}`)); err == nil {
		t.Fatal("settings_get without a store succeeded")
	}
	if _, err := set.Invoke(nil, []byte(`{"key":"ui.language","value":"en"}`)); err == nil {
		t.Fatal("settings_set without a store succeeded")
	}
}

// TestSettingsGetRedactsMCPSecrets is the regression for settings_get returning
// raw MCP configuration: env entries and Authorization headers carried live
// credentials straight to the model. mcp.servers is projected to names (the
// same projection inspect uses), and other mcp.* JSON values redact
// secret-named fields.
func TestSettingsGetRedactsMCPSecrets(t *testing.T) {
	const servers = `[{"name":"demo","command":"npx","args":["-y","srv"],"env":["OPENAI_API_KEY=sk-live-secret","PLAIN=visible"],"headers":{"Authorization":"Bearer tok","X-Trace":"ok"}}]`
	settings := &fakeSettings{values: map[string]string{"mcp.servers": servers}}
	get := NewSettingsGetTool(settings)
	ctx := context.Background()

	got, err := get.Invoke(ctx, []byte(`{"key":"mcp.servers"}`))
	if err != nil {
		t.Fatalf("settings_get(mcp.servers): %v", err)
	}
	if got != `["demo"]` {
		t.Fatalf("settings_get(mcp.servers) = %q, want the server-name projection [\"demo\"]", got)
	}
	for _, secret := range []string{"sk-live-secret", "Bearer tok", "OPENAI_API_KEY", "Authorization"} {
		if strings.Contains(got, secret) {
			t.Fatalf("settings_get(mcp.servers) leaked %q: %s", secret, got)
		}
	}

	// A non-servers mcp.* value keeps its non-secret shape but redacts both
	// object fields named like credentials and KEY=VALUE env entries.
	settings.values["mcp.extra"] = `{"headers":{"Authorization":"Bearer extra"},"env":["MCP_API_KEY=sk-extra","KEEP=visible"],"keep":"value"}`
	got, err = get.Invoke(ctx, []byte(`{"key":"mcp.extra"}`))
	if err != nil {
		t.Fatalf("settings_get(mcp.extra): %v", err)
	}
	for _, secret := range []string{"Bearer extra", "sk-extra"} {
		if strings.Contains(got, secret) {
			t.Fatalf("settings_get(mcp.extra) leaked %q: %s", secret, got)
		}
	}
	for _, keep := range []string{`"keep":"value"`, "KEEP=visible"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("settings_get(mcp.extra) = %q, want non-secret %q preserved", got, keep)
		}
	}

	// settings_set still writes the complete configuration; only the read side
	// is projected.
	set := NewSettingsSetTool(settings)
	newServers := `[{"name":"other","command":"npx"}]`
	if _, err := set.Invoke(ctx, []byte(`{"key":"mcp.servers","value":`+newServers+`}`)); err != nil {
		t.Fatalf("settings_set(mcp.servers): %v", err)
	}
	settings.mu.Lock()
	stored := settings.values["mcp.servers"]
	settings.mu.Unlock()
	if stored != newServers {
		t.Fatalf("stored mcp.servers = %q, want the full configuration %q", stored, newServers)
	}
}
