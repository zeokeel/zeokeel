package settings

// Credential masking shared by the diagnostics page (its on-screen "设置键"
// rows and the copy action) and the exported diagnostic report. One helper, so
// the same settings map cannot be plaintext in one surface and hidden in the
// other: whole values are hidden for credential-named settings, JSON structured
// settings (mcp.servers, modelcatalog.models, ...) are walked to hide
// Bearer/KEY=VALUE secrets, and non-JSON values carrying a Bearer token are
// hidden too.

import (
	"encoding/json"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// credentialMarkers are the lower-case substrings that mark a setting key or a
// JSON object key as holding a credential.
var credentialMarkers = []string{
	"apikey", "api_key", "api-key", "access_key", "private_key",
	"token", "secret", "password", "authorization",
}

// hasCredentialMarker reports whether text looks credential-named.
func hasCredentialMarker(text string) bool {
	lower := strings.ToLower(text)
	// The OAuth token record is opaque rig JSON; hide the whole row.
	if strings.HasPrefix(lower, contracts.MCPAuthSettingPrefix) {
		return true
	}
	for _, marker := range credentialMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// maskedSettingValue hides credentials for one settings row: whole values for
// credential-named rows, structured secrets inside JSON values and Bearer
// tokens in plain values. Both the diagnostics page and the exported report go
// through this function.
func maskedSettingValue(locale contracts.LocaleService, key, value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	hidden := localized(locale, "已设置(隐藏)", "set (hidden)")
	if hasCredentialMarker(key) {
		return hidden
	}
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			encoded, encodeErr := json.Marshal(redactJSONSecrets(decoded))
			if encodeErr == nil {
				return string(encoded)
			}
		}
		return hidden
	}
	if strings.Contains(trimmed, "Bearer ") {
		return hidden
	}
	if redacted, ok := redactKeyValueEntry(trimmed); ok {
		return redacted
	}
	return value
}

// redactKeyValueEntry masks one KEY=VALUE entry whose key is credential-named
// (MCP environment rows such as "GITHUB_TOKEN=abc").
func redactKeyValueEntry(entry string) (string, bool) {
	key, _, found := strings.Cut(entry, "=")
	if !found || !hasCredentialMarker(key) {
		return "", false
	}
	return strings.TrimSpace(key) + "=***", true
}

// redactJSONSecrets walks decoded JSON and hides Bearer tokens, values of
// credential-looking object keys and credential-named KEY=VALUE entries inside
// string arrays (the mcp.servers environment rows).
func redactJSONSecrets(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if hasCredentialMarker(key) {
				redacted[key] = "***"
				continue
			}
			if strings.EqualFold(key, "headers") || strings.EqualFold(key, "header") {
				redacted[key] = maskHeaderValues(item)
				continue
			}
			redacted[key] = redactJSONSecrets(item)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, item := range typed {
			redacted[index] = redactJSONSecrets(item)
		}
		return redacted
	case string:
		if strings.Contains(typed, "Bearer ") {
			return "Bearer ***"
		}
		if redacted, ok := redactKeyValueEntry(typed); ok {
			return redacted
		}
		return typed
	default:
		return value
	}
}

// maskHeaderValues hides every value of an HTTP header object (header names are
// user-chosen, so credential markers cannot catch e.g. X-Api-Key).
func maskHeaderValues(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		masked := make(map[string]any, len(typed))
		for key, item := range typed {
			if text, ok := item.(string); ok && strings.Contains(text, "Bearer ") {
				masked[key] = "Bearer ***"
				continue
			}
			masked[key] = "***"
		}
		return masked
	case []any:
		masked := make([]any, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				masked[index] = "***"
				continue
			}
			switch {
			case strings.Contains(text, "Bearer "):
				masked[index] = "Bearer ***"
			case strings.Contains(text, ":"):
				key, _, _ := strings.Cut(text, ":")
				masked[index] = strings.TrimSpace(key) + ": ***"
			default:
				masked[index] = "***"
			}
		}
		return masked
	default:
		return "***"
	}
}
