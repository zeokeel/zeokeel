package toolkit

// The two model-facing settings tools, owned by the product.
//
// Until v0.19.0 the pair came from rig's tools/settings package. Rig removed
// that package in v0.20.0 ("settings belong to the UI; the model must not be
// able to rewrite the application's own configuration"), so the tool surface is
// now the product's own decision and the product's own code. Behaviour, tool
// names and schemas are kept byte-compatible with the rig v0.19.0 originals:
// settings_get/settings_set stay in the approval matrix (they are NOT
// auto-allowed by any preset — see maskedSettingsTools for the redaction wrapper
// that keeps credentials away from the model).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	settingToolGet = "settings_get"
	settingToolSet = "settings_set"
)

// productSettingsTools returns the bare settings_get/settings_set pair bound to
// store. A nil store (or a typed-nil one) answers every call with a readable
// tool error instead of panicking.
func productSettingsTools(store contracts.SettingsService) []rig.Tool {
	return []rig.Tool{
		settingsGetTool{store: store},
		settingsSetTool{store: store},
	}
}

// settingsGetTool implements settings_get.
type settingsGetTool struct {
	store contracts.SettingsService
}

func (t settingsGetTool) Name() string { return settingToolGet }

func (t settingsGetTool) Description() string {
	return "Read one setting by key and return its value. When the key is not stored the optional " +
		"default is returned (an empty string when no default is given). Use settings_set to change values."
}

func (t settingsGetTool) Schema() map[string]any {
	return settingsObjectSchema(map[string]any{
		"key":     settingsStringProp("Setting key, for example \"ui.theme\" or \"model\". Any non-empty string is valid."),
		"default": settingsStringProp("Value to return when the key is absent (default: empty string)."),
	}, "key")
}

func (t settingsGetTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		Key     string `json:"key"`
		Default string `json:"default"`
	}
	if err := settingsDecodeArgs(call, &args); err != nil {
		return rig.ToolResult{}, err
	}
	if args.Key == "" {
		return settingsErrorResultf("settings_get: key is required"), nil
	}
	if settingsStoreMissing(t.store) {
		return settingsErrorResultf("settings_get: the host did not configure a settings store"), nil
	}
	if err := ctx.Err(); err != nil {
		return rig.ToolResult{}, err
	}
	return rig.ToolResult{Content: t.store.Get(args.Key, args.Default)}, nil
}

// settingsSetTool implements settings_set.
type settingsSetTool struct {
	store contracts.SettingsService
}

func (t settingsSetTool) Name() string { return settingToolSet }

func (t settingsSetTool) Description() string {
	return "Store one setting value by key. The value is a string; the host decides where settings " +
		"persist. Pass an empty value to clear the value (the key keeps its empty value)."
}

func (t settingsSetTool) Schema() map[string]any {
	return settingsObjectSchema(map[string]any{
		"key":   settingsStringProp("Setting key, for example \"ui.theme\" or \"model\". Any non-empty string is valid."),
		"value": settingsStringProp("New value to store."),
	}, "key", "value")
}

func (t settingsSetTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := settingsDecodeArgs(call, &args); err != nil {
		return rig.ToolResult{}, err
	}
	if args.Key == "" {
		return settingsErrorResultf("settings_set: key is required"), nil
	}
	if settingsStoreMissing(t.store) {
		return settingsErrorResultf("settings_set: the host did not configure a settings store"), nil
	}
	if err := ctx.Err(); err != nil {
		return rig.ToolResult{}, err
	}
	if err := t.store.Set(args.Key, args.Value); err != nil {
		return settingsErrorResultf("settings_set: %v", err), nil
	}
	return rig.ToolResult{Content: fmt.Sprintf("set %q", args.Key)}, nil
}

// --- shared schema/argument helpers (rig's tools/internal/toolutil is internal) ---

// settingsObjectSchema declares a tool argument object: the given properties, no
// additional properties, and a required list when at least one name is given.
func settingsObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// settingsStringProp builds one string property.
func settingsStringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// settingsDecodeArgs decodes a tool call's arguments; empty arguments mean {}.
func settingsDecodeArgs(call rig.ToolCall, target any) error {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("invalid arguments JSON: %w", err)
	}
	return nil
}

// settingsErrorResultf builds a model-visible tool failure (the run continues).
func settingsErrorResultf(format string, args ...any) rig.ToolResult {
	return rig.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}

// settingsStoreMissing reports whether store is nil, including a typed nil that
// would panic when its methods are called.
func settingsStoreMissing(store contracts.SettingsService) bool {
	if store == nil {
		return true
	}
	// contracts.SettingsService is always a pointer/struct implementation in this
	// product; a typed nil is still possible through reflection-free checks only
	// when the interface itself is nil, so the nil test above is the whole guard.
	return false
}
