package contracts

// Model thinking/reasoning configuration.
//
// Different model families expose "thinking" with different request fields:
// Qwen/DashScope-compatible endpoints use enable_thinking (and sometimes
// thinking_budget), while OpenAI/DeepSeek-compatible endpoints use
// reasoning_effort. A model's family selects the built-in mode list; users can
// add their own modes (arbitrary top-level request fields) and pick a default
// mode per model. A conversation stores only the selected mode ID, so it can be
// changed at any time without restarting the app.

import (
	"strings"
)

// Model family identifiers. The family determines which built-in thinking modes
// a model offers; ModelFamilyCustom starts with no built-ins, only user modes.
const (
	ModelFamilyQwen     = "qwen"
	ModelFamilyDeepSeek = "deepseek"
	ModelFamilyOther    = "other"
	ModelFamilyCustom   = "custom"
)

// Built-in thinking mode IDs.
const (
	// ThinkingModeDefault sends no thinking fields at all.
	ThinkingModeDefault = "default"
	// ThinkingModeQwenOff asks Qwen to skip thinking (enable_thinking=false).
	ThinkingModeQwenOff = "qwen-off"
	// ThinkingModeQwenOn asks Qwen to think (enable_thinking=true).
	ThinkingModeQwenOn = "qwen-on"
	// ThinkingModeQwenDeep asks Qwen to think with a longer budget.
	ThinkingModeQwenDeep = "qwen-deep"
	// ThinkingModeReasoningLow/Medium/High send reasoning_effort.
	ThinkingModeReasoningLow    = "reasoning-low"
	ThinkingModeReasoningMedium = "reasoning-medium"
	ThinkingModeReasoningHigh   = "reasoning-high"
)

// ThinkingMode is one selectable thinking preset. Params are merged into the
// top-level OpenAI-compatible request body (the SDK's Request.Extra path), so
// they may contain any provider-specific field (enable_thinking, thinking_budget,
// reasoning_effort, chat_template_kwargs, ...).
type ThinkingMode struct {
	// ID is the stable identifier stored per conversation. Required and unique
	// within a model's effective mode list.
	ID string `json:"id"`
	// NameZH/NameEN are display labels. Built-in modes ship both; user-defined
	// modes use the same name for both languages.
	NameZH string `json:"nameZh,omitempty"`
	NameEN string `json:"nameEn,omitempty"`
	// Params are extra top-level request fields. Empty/nil sends nothing.
	Params map[string]any `json:"params,omitempty"`
}

// DisplayName returns the localized label (falling back to the other language
// and then to the ID).
func (m ThinkingMode) DisplayName(english bool) string {
	if english {
		if name := strings.TrimSpace(m.NameEN); name != "" {
			return name
		}
		if name := strings.TrimSpace(m.NameZH); name != "" {
			return name
		}
		return m.ID
	}
	if name := strings.TrimSpace(m.NameZH); name != "" {
		return name
	}
	if name := strings.TrimSpace(m.NameEN); name != "" {
		return name
	}
	return m.ID
}

// ThinkingFamilies lists the selectable families in display order.
func ThinkingFamilies() []string {
	return []string{ModelFamilyQwen, ModelFamilyDeepSeek, ModelFamilyOther, ModelFamilyCustom}
}

// ValidThinkingFamily reports whether family is selectable. An empty value is
// allowed (it means "infer from the model ID").
func ValidThinkingFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "", ModelFamilyQwen, ModelFamilyDeepSeek, ModelFamilyOther, ModelFamilyCustom:
		return true
	default:
		return false
	}
}

// InferThinkingFamily guesses the family from a model ID. Unknown IDs are
// treated as the generic OpenAI-compatible reasoning_effort family, which keeps
// the historical behavior for custom model names.
func InferThinkingFamily(modelID string) string {
	id := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.Contains(id, "qwen"), strings.Contains(id, "dashscope"), strings.Contains(id, "tongyi"):
		return ModelFamilyQwen
	case strings.Contains(id, "deepseek"), strings.Contains(id, "reasoner"):
		return ModelFamilyDeepSeek
	default:
		return ModelFamilyOther
	}
}

// EffectiveThinkingFamily resolves a model's family: an explicit valid family
// wins, otherwise the model ID is inspected.
func EffectiveThinkingFamily(info ModelInfo) string {
	if family := strings.TrimSpace(info.Family); family != "" && ValidThinkingFamily(family) && family != ModelFamilyCustom {
		return family
	}
	if strings.TrimSpace(info.Family) == ModelFamilyCustom {
		return ModelFamilyCustom
	}
	return InferThinkingFamily(info.ID)
}

// BuiltinThinkingModes returns the built-in mode list for one family.
func BuiltinThinkingModes(family string) []ThinkingMode {
	switch strings.TrimSpace(family) {
	case ModelFamilyQwen:
		return []ThinkingMode{
			{ID: ThinkingModeDefault, NameZH: "默认", NameEN: "Default"},
			{ID: ThinkingModeQwenOff, NameZH: "关闭思考", NameEN: "Thinking off",
				Params: map[string]any{"enable_thinking": false}},
			{ID: ThinkingModeQwenOn, NameZH: "思考开启", NameEN: "Thinking on",
				Params: map[string]any{"enable_thinking": true}},
			{ID: ThinkingModeQwenDeep, NameZH: "深度思考", NameEN: "Deep thinking",
				Params: map[string]any{"enable_thinking": true, "thinking_budget": 8192}},
		}
	case ModelFamilyDeepSeek:
		return []ThinkingMode{
			{ID: ThinkingModeDefault, NameZH: "默认", NameEN: "Default"},
			{ID: ThinkingModeReasoningLow, NameZH: "低强度", NameEN: "Low",
				Params: map[string]any{"reasoning_effort": SessionEffortLow}},
			{ID: ThinkingModeReasoningMedium, NameZH: "中强度", NameEN: "Medium",
				Params: map[string]any{"reasoning_effort": SessionEffortMedium}},
			{ID: ThinkingModeReasoningHigh, NameZH: "高强度", NameEN: "High",
				Params: map[string]any{"reasoning_effort": SessionEffortHigh}},
		}
	case ModelFamilyOther:
		return []ThinkingMode{
			{ID: ThinkingModeDefault, NameZH: "默认", NameEN: "Default"},
			{ID: ThinkingModeReasoningLow, NameZH: "低强度", NameEN: "Low",
				Params: map[string]any{"reasoning_effort": SessionEffortLow}},
			{ID: ThinkingModeReasoningMedium, NameZH: "中强度", NameEN: "Medium",
				Params: map[string]any{"reasoning_effort": SessionEffortMedium}},
			{ID: ThinkingModeReasoningHigh, NameZH: "高强度", NameEN: "High",
				Params: map[string]any{"reasoning_effort": SessionEffortHigh}},
		}
	default:
		return nil
	}
}

// ThinkingModesForModel returns the model's effective mode list: the family
// built-ins followed by the model's custom modes. A custom mode with the same
// ID replaces the built-in one, so users can retune a preset without editing
// conversation data.
func ThinkingModesForModel(info ModelInfo) []ThinkingMode {
	family := EffectiveThinkingFamily(info)
	modes := BuiltinThinkingModes(family)
	if len(info.ThinkingModes) == 0 {
		return modes
	}
	custom := SanitizeThinkingModes(info.ThinkingModes)
	base := make(map[string]int, len(modes))
	for index, mode := range modes {
		base[mode.ID] = index
	}
	for _, mode := range custom {
		if index, ok := base[mode.ID]; ok {
			modes[index] = mode
			continue
		}
		base[mode.ID] = len(modes)
		modes = append(modes, mode)
	}
	return modes
}

// SanitizeThinkingModes normalizes user modes: IDs are trimmed/lower-cased,
// empty IDs are dropped, a display name falls back to the ID, duplicate IDs keep
// the first row and parameter maps are copied.
func SanitizeThinkingModes(modes []ThinkingMode) []ThinkingMode {
	if len(modes) == 0 {
		return nil
	}
	out := make([]ThinkingMode, 0, len(modes))
	seen := make(map[string]bool, len(modes))
	for _, mode := range modes {
		id := strings.ToLower(strings.TrimSpace(mode.ID))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		nameZH := strings.TrimSpace(mode.NameZH)
		nameEN := strings.TrimSpace(mode.NameEN)
		if nameZH == "" {
			nameZH = firstNonEmptyTrimmed(nameEN, id)
		}
		if nameEN == "" {
			nameEN = nameZH
		}
		out = append(out, ThinkingMode{
			ID:     id,
			NameZH: nameZH,
			NameEN: nameEN,
			Params: CloneRequestParams(mode.Params),
		})
	}
	return out
}

// firstNonEmptyTrimmed returns the first non-empty trimmed candidate.
func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// ThinkingModeByID finds one mode by ID (case-insensitive).
func ThinkingModeByID(modes []ThinkingMode, id string) (ThinkingMode, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return ThinkingMode{}, false
	}
	for _, mode := range modes {
		if strings.ToLower(strings.TrimSpace(mode.ID)) == id {
			return mode, true
		}
	}
	return ThinkingMode{}, false
}

// ResolveThinkingMode picks the effective mode for a conversation:
// explicit session mode > the model's configured default > the legacy
// per-session/config reasoning effort > the family default mode.
func ResolveThinkingMode(info ModelInfo, sessionModeID, legacyEffort string) (ThinkingMode, map[string]any) {
	modes := ThinkingModesForModel(info)
	if mode, ok := ThinkingModeByID(modes, sessionModeID); ok {
		return mode, CloneRequestParams(mode.Params)
	}
	if mode, ok := ThinkingModeByID(modes, info.DefaultThinking); ok {
		return mode, CloneRequestParams(mode.Params)
	}
	if effort := normalizeLegacyEffort(legacyEffort); effort != "" {
		if mode, ok := ThinkingModeByID(modes, "reasoning-"+effort); ok {
			return mode, CloneRequestParams(mode.Params)
		}
	}
	if mode, ok := ThinkingModeByID(modes, ThinkingModeDefault); ok {
		return mode, CloneRequestParams(mode.Params)
	}
	return ThinkingMode{ID: ThinkingModeDefault}, nil
}

// normalizeLegacyEffort accepts only low/medium/high.
func normalizeLegacyEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case SessionEffortLow:
		return SessionEffortLow
	case SessionEffortMedium:
		return SessionEffortMedium
	case SessionEffortHigh:
		return SessionEffortHigh
	default:
		return ""
	}
}

// CloneRequestParams copies a request-parameter map so callers never share a
// map with settings or another request.
func CloneRequestParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]any, len(params))
	for key, value := range params {
		out[key] = value
	}
	return out
}

// SessionThinkingSettingKey is the LEGACY per-conversation thinking-mode key
// used before thinking strength became per model. New selections are stored with
// SessionModelThinkingSettingKey; this row is only read as a fallback for
// conversations created by an older version, and is cleared as soon as the user
// picks a strength in that conversation.
func SessionThinkingSettingKey(sessionID string) string {
	return "session." + strings.TrimSpace(sessionID) + ".thinking"
}

// SessionModelThinkingSettingKey stores the selected thinking-mode ID for one
// conversation and one model. Thinking strength belongs to the model: every
// model keeps its own conversation override, so switching models never carries
// another model's override over, and an empty/absent row follows the model's own
// DefaultThinking.
func SessionModelThinkingSettingKey(sessionID, modelID string) string {
	sessionID = strings.TrimSpace(sessionID)
	modelID = strings.TrimSpace(modelID)
	if sessionID == "" || modelID == "" {
		return ""
	}
	return "session." + sessionID + ".thinking." + modelID
}

// SessionThinkingOverride resolves the thinking-mode ID explicitly selected for
// one conversation and model: the per-model row first, then the legacy
// session-wide row (old conversations). Empty means "follow the model default".
func SessionThinkingOverride(settings SettingsService, sessionID, modelID string) string {
	if settings == nil {
		return ""
	}
	if key := SessionModelThinkingSettingKey(sessionID, modelID); key != "" {
		if mode := strings.TrimSpace(settings.Get(key, "")); mode != "" {
			return mode
		}
	}
	return strings.TrimSpace(settings.Get(SessionThinkingSettingKey(sessionID), ""))
}

// SessionThinkingOverrideKeys lists the settings rows that carry a
// conversation's thinking override for one model, most specific first: the
// per-model row, the legacy session-wide thinking row and the legacy per-session
// reasoning-effort row. Writing a selection or "follow the model default" clears
// every row, so a legacy value can never leak into the per-model state (a
// conversation-wide strength applying to every model).
func SessionThinkingOverrideKeys(sessionID, modelID string) []string {
	keys := make([]string, 0, 3)
	if key := SessionModelThinkingSettingKey(sessionID, modelID); key != "" {
		keys = append(keys, key)
	}
	keys = append(keys, SessionThinkingSettingKey(sessionID), SessionEffortSettingKey(sessionID))
	return keys
}
