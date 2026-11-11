package toolkit

// Model-facing settings tools. The product wraps rig's settings_get so a
// credential-named key never returns its plaintext value to the model (the
// read-only/workspace approval presets used to auto-allow the raw tool, which
// exposed provider/search keys without any approval), and so JSON-valued
// settings (mcp.servers env rows, model catalog entries) are redacted with the
// same rules the diagnostics export uses. settings_set keeps rig's semantics.

import (
	"context"
	"encoding/json"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/settings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// hiddenSettingValue is returned for a credential-named setting that has a
// value: the model learns the key is configured, never the secret.
const hiddenSettingValue = "已设置(隐藏)"

// maskedSettingsTools returns the product's settings_get/settings_set pair.
func maskedSettingsTools(store contracts.SettingsService) []rig.Tool {
	base := settings.Tools(store)
	if len(base) != 2 {
		return base
	}
	return []rig.Tool{maskedSettingsGet{inner: base[0]}, base[1]}
}

// maskedSettingsGet wraps rig's settings_get tool with credential redaction.
type maskedSettingsGet struct {
	inner rig.Tool
}

var _ rig.Tool = maskedSettingsGet{}

// Name implements rig.Tool.
func (t maskedSettingsGet) Name() string { return t.inner.Name() }

// Description implements rig.Tool.
func (t maskedSettingsGet) Description() string { return t.inner.Description() }

// Schema implements rig.Tool.
func (t maskedSettingsGet) Schema() map[string]any { return t.inner.Schema() }

// Invoke implements rig.Tool: credential-named keys report only whether a value
// is set, and JSON-valued settings are redacted before they reach the model.
func (t maskedSettingsGet) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	key := ""
	var args struct {
		Key string `json:"key"`
	}
	if json.Unmarshal([]byte(call.Arguments), &args) == nil {
		key = strings.TrimSpace(args.Key)
	}
	result, err := t.inner.Invoke(ctx, call)
	if err != nil || result.IsError {
		return result, err
	}
	if hasCredentialMarker(key) {
		if strings.TrimSpace(result.Content) == "" {
			return result, nil
		}
		result.Content = hiddenSettingValue
		return result, nil
	}
	result.Content = redactSettingText(result.Content)
	return result, nil
}

// credentialMarkers are the lower-case substrings that mark a setting key (or a
// JSON object key) as holding a credential. Kept aligned with
// internal/ui/settings/redact.go so the model-facing and export-facing
// redaction agree; the backend cannot import the UI module.
var credentialMarkers = []string{
	"apikey", "api_key", "token", "secret", "password", "authorization",
}

// hasCredentialMarker reports whether text looks credential-named.
func hasCredentialMarker(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range credentialMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// redactSettingText hides credentials inside one settings value: JSON values are
// walked recursively (Bearer tokens, credential object keys and credential-named
// KEY=VALUE entries inside string arrays such as mcp.servers env rows), plain
// values get the same KEY=VALUE / Bearer treatment.
func redactSettingText(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			encoded, encodeErr := json.Marshal(redactJSONValue(decoded))
			if encodeErr == nil {
				return string(encoded)
			}
		}
		return hiddenSettingValue
	}
	if strings.Contains(trimmed, "Bearer ") {
		return "Bearer ***"
	}
	if redacted, ok := redactKeyValueEntry(trimmed); ok {
		return redacted
	}
	return value
}

// redactKeyValueEntry masks one KEY=VALUE entry whose key is credential-named.
func redactKeyValueEntry(entry string) (string, bool) {
	key, _, found := strings.Cut(entry, "=")
	if !found || !hasCredentialMarker(key) {
		return "", false
	}
	return strings.TrimSpace(key) + "=***", true
}

// redactJSONValue walks decoded JSON and hides credentials.
func redactJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if hasCredentialMarker(key) {
				redacted[key] = "***"
				continue
			}
			redacted[key] = redactJSONValue(item)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, item := range typed {
			redacted[index] = redactJSONValue(item)
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
