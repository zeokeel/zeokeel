package settings

// Deterministic unit tests for the shared credential mask: the diagnostics page
// and the exported report both go through maskedSettingValue, so these cases pin
// every credential shape the audit found leaking (non-chat API keys and MCP
// environment values).

import (
	"strings"
	"testing"
)

func TestMaskedSettingValueHidesCredentialRows(t *testing.T) {
	for _, key := range []string{
		"chat.apiKey", "web.search.apiKey", "WEB_SEARCH_API_KEY",
		"mcp.authorization", "some.token", "db.password", "client.secret",
	} {
		if got := maskedSettingValue(modelCheckZH, key, "plain-secret"); got != "已设置(隐藏)" {
			t.Errorf("maskedSettingValue(%q) = %q, want the hidden marker", key, got)
		}
		if got := maskedSettingValue(modelCheckEN, key, "plain-secret"); got != "set (hidden)" {
			t.Errorf("maskedSettingValue en (%q) = %q, want the hidden marker", key, got)
		}
	}
}

func TestMaskedSettingValueKeepsPlainRows(t *testing.T) {
	cases := map[string]string{
		"chat.model":          "deepseek-chat",
		"agent.permission":    "workspace",
		"ui.theme.mode":       "dark",
		"web.search.endpoint": "https://search.example.com",
		"ui.chatFontSize":     "standard",
		"mcp.disabledServers": `["a"]`,
	}
	for key, value := range cases {
		if got := maskedSettingValue(modelCheckZH, key, value); got != value {
			t.Errorf("maskedSettingValue(%q) = %q, want unchanged %q", key, got, value)
		}
	}
	// Structured rows without credentials stay readable (JSON re-encoding may
	// reorder object keys, so compare content, not bytes).
	row := `[{"name":"srv","url":"http://127.0.0.1:1/mcp"}]`
	got := maskedSettingValue(modelCheckZH, "mcp.servers", row)
	if strings.Contains(got, "***") {
		t.Fatalf("credential-free mcp.servers = %q, want it readable", got)
	}
	for _, want := range []string{`"name":"srv"`, `"url":"http://127.0.0.1:1/mcp"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("credential-free mcp.servers = %q, want it to keep %q", got, want)
		}
	}
}

func TestMaskedSettingValueHidesMCPEnvSecrets(t *testing.T) {
	row := `[{"name":"srv","transport":"stdio","command":"run","env":["GITHUB_TOKEN=abc123","FOO=bar","db_password=pw","PLAIN=value"]}]`
	got := maskedSettingValue(modelCheckZH, "mcp.servers", row)
	for _, leaked := range []string{"abc123", "pw", "GITHUB_TOKEN=abc123", "db_password=pw"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("masked mcp.servers leaked %q: %s", leaked, got)
		}
	}
	for _, kept := range []string{"GITHUB_TOKEN=***", "db_password=***", "FOO=bar", "PLAIN=value"} {
		if !strings.Contains(got, kept) {
			t.Fatalf("masked mcp.servers = %s, want it to keep %q", got, kept)
		}
	}
}

func TestMaskedSettingValueHidesBearerAndEnvEntries(t *testing.T) {
	for _, value := range []string{
		"Authorization: Bearer abc123",
		"GITHUB_TOKEN=abc123",
		"api_key=abc123",
	} {
		got := maskedSettingValue(modelCheckZH, "some.setting", value)
		if strings.Contains(got, "abc123") {
			t.Errorf("maskedSettingValue(%q) = %q, want the token hidden", value, got)
		}
	}
}

func TestMaskedSettingValueMalformedJSONIsHidden(t *testing.T) {
	// A malformed structured row must fail closed (hidden), never echo the raw
	// bytes that may contain the credential.
	if got := maskedSettingValue(modelCheckZH, "mcp.servers", `{"env":["TOKEN=abc123"]`); got != "已设置(隐藏)" {
		t.Fatalf("malformed JSON = %q, want the hidden marker", got)
	}
}
