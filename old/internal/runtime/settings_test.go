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

	// settings_set must NOT write the complete configuration: mcp.servers is a
	// process-execution surface (command/args/env run on the next rebuild), so
	// it is read-only for the Creator tools even though settings_get may show
	// the projected server names.
	set := NewSettingsSetTool(settings)
	newServers := `[{"name":"other","command":"npx"}]`
	if _, err := set.Invoke(ctx, []byte(`{"key":"mcp.servers","value":`+newServers+`}`)); err == nil {
		t.Fatal("settings_set(mcp.servers) = nil error; MCP configuration must be read-only for the model")
	}
	settings.mu.Lock()
	stored := settings.values["mcp.servers"]
	settings.mu.Unlock()
	if stored != servers {
		t.Fatalf("settings_set changed mcp.servers to %q, want the original %q", stored, servers)
	}
}

// TestSettingsWriteWhitelistMinimized pins the security boundary requested by
// the audit: the Creator writer is narrower than the reader and cannot change a
// setting that starts a process, redirects credentials or weakens approvals.
func TestSettingsWriteWhitelistMinimized(t *testing.T) {
	allowed := []string{
		"ui.language", "ui.theme", "model.default", "provider.model",
		"provider.reasoning_effort", "agent.preset", "agent.collaboration_mode",
		"skills.root", "plugin.agent_loop.max_iterations",
		"plugin.subagent.depth", "plugin.subagent.model",
		"websearch.max_results", "web_search.max_results",
	}
	for _, key := range allowed {
		if !SettingsKeyWritable(key) {
			t.Errorf("SettingsKeyWritable(%q) = false, want true", key)
		}
	}
	rejected := []string{
		// Read-whitelisted but execution / credential / authorization surfaces.
		"mcp.servers",
		"mcp.workspace_config",
		"provider.base_url",
		"websearch.endpoint",
		"web_search.endpoint",
		"approval.tools",
		"ui.permission",
		"plugin.shell.enabled",
		"plugin.mcp.enabled",
		// Arbitrary keys in those namespaces, including future ones.
		"mcp.anything",
		"mcp.extra",
		"plugin.shell.timeout_seconds",
		"plugin.mcp.workspace_config",
		// Not on the read whitelist at all.
		"session.secret.value", "provider.api_key",
	}
	for _, key := range rejected {
		if SettingsKeyWritable(key) {
			t.Errorf("SettingsKeyWritable(%q) = true, want false", key)
		}
	}
	if SettingsKeyAllowed("mcp.servers") != true {
		t.Error("mcp.servers must stay readable (projected to server names)")
	}
}

// TestSettingsSetRejectsExecutionKeys pins the tool-level rejection: the model
// gets a clear error and nothing is written.
func TestSettingsSetRejectsExecutionKeys(t *testing.T) {
	settings := &fakeSettings{values: map[string]string{}}
	set := NewSettingsSetTool(settings)
	ctx := context.Background()
	for _, key := range []string{"mcp.servers", "mcp.workspace_config", "provider.base_url", "approval.tools"} {
		args := []byte(`{"key":"` + key + `","value":"x"}`)
		if _, err := set.Invoke(ctx, args); err == nil {
			t.Fatalf("settings_set(%q) = nil error, want rejection", key)
		}
	}
	settings.mu.Lock()
	defer settings.mu.Unlock()
	if len(settings.values) != 0 {
		t.Fatalf("rejected writes persisted values: %v", settings.values)
	}
}
