package contracts

// Tests for the model thinking/reasoning contracts: family inference, built-in
// mode lists, custom-mode merge/override, per-session resolution priority and
// the shared default catalog.

import (
	"strings"
	"testing"
)

func TestInferThinkingFamily(t *testing.T) {
	cases := map[string]string{
		"qwen3.8-flash-next": ModelFamilyQwen,
		"qwen-max":           ModelFamilyQwen,
		"deepseek-chat":      ModelFamilyDeepSeek,
		"deepseek-reasoner":  ModelFamilyDeepSeek,
		"gpt-5":              ModelFamilyOther,
		"my-local-model":     ModelFamilyOther,
		"":                   ModelFamilyOther,
	}
	for model, want := range cases {
		if got := InferThinkingFamily(model); got != want {
			t.Errorf("InferThinkingFamily(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestBuiltinThinkingModes(t *testing.T) {
	if modes := BuiltinThinkingModes(ModelFamilyCustom); len(modes) != 0 {
		t.Fatalf("custom family built-ins = %v, want none", modes)
	}
	qwen := BuiltinThinkingModes(ModelFamilyQwen)
	if len(qwen) != 4 {
		t.Fatalf("qwen modes = %d, want 4", len(qwen))
	}
	off, ok := ThinkingModeByID(qwen, ThinkingModeQwenOff)
	if !ok || off.Params["enable_thinking"] != false {
		t.Fatalf("qwen off mode = %+v, want enable_thinking=false", off)
	}
	deep, ok := ThinkingModeByID(qwen, ThinkingModeQwenDeep)
	if !ok || deep.Params["thinking_budget"] != 8192 {
		t.Fatalf("qwen deep mode = %+v, want thinking_budget=8192", deep)
	}
	other := BuiltinThinkingModes(ModelFamilyOther)
	high, ok := ThinkingModeByID(other, ThinkingModeReasoningHigh)
	if !ok || high.Params["reasoning_effort"] != SessionEffortHigh {
		t.Fatalf("other high mode = %+v, want reasoning_effort=high", high)
	}
	for _, mode := range append(qwen, other...) {
		if strings.TrimSpace(mode.ID) == "" || mode.DisplayName(false) == "" {
			t.Fatalf("built-in mode lacks id/name: %+v", mode)
		}
	}
}

func TestThinkingModesForModelCustomOverride(t *testing.T) {
	info := ModelInfo{ID: "qwen-test", Family: ModelFamilyQwen, ThinkingModes: []ThinkingMode{
		{ID: ThinkingModeQwenDeep, NameZH: "更深的思考", NameEN: "Deeper",
			Params: map[string]any{"enable_thinking": true, "thinking_budget": 16384}},
		{ID: "fast", NameZH: "快速", Params: map[string]any{"enable_thinking": false}},
	}}
	modes := ThinkingModesForModel(info)
	if len(modes) != 5 {
		t.Fatalf("modes = %d, want 4 built-in + 1 new custom", len(modes))
	}
	deep, _ := ThinkingModeByID(modes, ThinkingModeQwenDeep)
	if deep.DisplayName(false) != "更深的思考" || deep.Params["thinking_budget"] != 16384 {
		t.Fatalf("custom override not applied: %+v", deep)
	}
	if _, ok := ThinkingModeByID(modes, "fast"); !ok {
		t.Fatalf("custom new mode missing: %+v", modes)
	}
}

func TestResolveThinkingModePriority(t *testing.T) {
	info := ModelInfo{ID: "qwen-test", Family: ModelFamilyQwen,
		DefaultThinking: ThinkingModeQwenOn,
		ThinkingModes:   []ThinkingMode{{ID: "custom", Params: map[string]any{"custom_flag": true}}},
	}
	cases := []struct {
		name        string
		sessionMode string
		legacy      string
		wantID      string
		wantParam   string
		wantValue   any
	}{
		{"session wins", "custom", "high", "custom", "custom_flag", true},
		{"model default", "", "high", ThinkingModeQwenOn, "enable_thinking", true},
		{"legacy fallback", "", "high", ThinkingModeQwenOn, "enable_thinking", true},
	}
	for _, tc := range cases {
		mode, params := ResolveThinkingMode(info, tc.sessionMode, tc.legacy)
		if mode.ID != tc.wantID {
			t.Errorf("%s: mode = %q, want %q", tc.name, mode.ID, tc.wantID)
		}
		if params[tc.wantParam] != tc.wantValue {
			t.Errorf("%s: params[%q] = %v, want %v", tc.name, tc.wantParam, params[tc.wantParam], tc.wantValue)
		}
	}

	// No selection/default/legacy: the family's default mode is the empty
	// parameter set.
	plain := ModelInfo{ID: "deepseek-chat", Family: ModelFamilyDeepSeek}
	mode, params := ResolveThinkingMode(plain, "", "")
	if mode.ID != ThinkingModeDefault || len(params) != 0 {
		t.Fatalf("plain resolution = %+v / %v, want default/no params", mode, params)
	}

	// Legacy effort maps to the reasoning_effort family mode when nothing else
	// is selected.
	mode, params = ResolveThinkingMode(plain, "", SessionEffortMedium)
	if mode.ID != ThinkingModeReasoningMedium || params["reasoning_effort"] != SessionEffortMedium {
		t.Fatalf("legacy effort resolution = %+v / %v, want reasoning-medium", mode, params)
	}

	// An unknown/stale session mode falls through instead of sending garbage.
	mode, params = ResolveThinkingMode(plain, "removed-mode", "")
	if mode.ID != ThinkingModeDefault || len(params) != 0 {
		t.Fatalf("stale mode resolution = %+v / %v, want default", mode, params)
	}
}

func TestSanitizeThinkingModes(t *testing.T) {
	params := map[string]any{"enable_thinking": true}
	modes := SanitizeThinkingModes([]ThinkingMode{
		{ID: "  Custom-One  ", NameEN: "Custom one", Params: params},
		{ID: ""},
		{ID: "custom-one", NameZH: "duplicate"},
	})
	if len(modes) != 1 {
		t.Fatalf("modes = %+v, want one row", modes)
	}
	if modes[0].ID != "custom-one" || modes[0].NameZH != "Custom one" || modes[0].NameEN != "Custom one" {
		t.Fatalf("sanitized mode = %+v", modes[0])
	}
	modes[0].Params["mutated"] = true
	if params["mutated"] != nil {
		t.Fatalf("sanitize shared the caller's params map")
	}
}

func TestThinkingFamiliesAndValidators(t *testing.T) {
	if len(ThinkingFamilies()) != 4 {
		t.Fatalf("families = %v", ThinkingFamilies())
	}
	for _, family := range []string{"", ModelFamilyQwen, ModelFamilyDeepSeek, ModelFamilyOther, ModelFamilyCustom} {
		if !ValidThinkingFamily(family) {
			t.Fatalf("ValidThinkingFamily(%q) = false", family)
		}
	}
	if ValidThinkingFamily("bogus") {
		t.Fatal("ValidThinkingFamily accepted an unknown family")
	}
	if got := EffectiveThinkingFamily(ModelInfo{ID: "qwen-max"}); got != ModelFamilyQwen {
		t.Fatalf("inferred family = %q", got)
	}
	if got := EffectiveThinkingFamily(ModelInfo{ID: "whatever", Family: ModelFamilyCustom}); got != ModelFamilyCustom {
		t.Fatalf("explicit custom family = %q", got)
	}
}

func TestResolveModelInfoWithoutCatalog(t *testing.T) {
	// The product ships no built-in model list: every model is user-configured.
	// An unknown id still gets an inferred family so custom model names keep the
	// generic thinking modes.
	unknown := ResolveModelInfo(nil, "qwen3.8-flash-next")
	if unknown.Family != ModelFamilyQwen || unknown.ID != "qwen3.8-flash-next" {
		t.Fatalf("resolved unknown model = %+v", unknown)
	}
	if DefaultModelID(nil, nil) != "" {
		t.Fatalf("empty catalog default = %q", DefaultModelID(nil, nil))
	}
}

func TestSessionModelThinkingKeysAndOverride(t *testing.T) {
	if got := SessionModelThinkingSettingKey("s1", "model-a"); got != "session.s1.thinking.model-a" {
		t.Fatalf("per-model key = %q", got)
	}
	if SessionModelThinkingSettingKey("", "model-a") != "" || SessionModelThinkingSettingKey("s1", " ") != "" {
		t.Fatal("invalid session/model produced a non-empty key")
	}
	settings := mapSettings{}
	if got := SessionThinkingOverride(nil, "s1", "model-a"); got != "" {
		t.Fatalf("nil settings override = %q", got)
	}
	if got := SessionThinkingOverride(settings, "s1", "model-a"); got != "" {
		t.Fatalf("unset override = %q", got)
	}
	// The legacy session-wide row is a fallback for old conversations.
	settings[SessionThinkingSettingKey("s1")] = ThinkingModeReasoningHigh
	if got := SessionThinkingOverride(settings, "s1", "model-a"); got != ThinkingModeReasoningHigh {
		t.Fatalf("legacy override = %q", got)
	}
	// A per-model row wins over the legacy row and only affects that model.
	settings[SessionModelThinkingSettingKey("s1", "model-a")] = ThinkingModeReasoningLow
	if got := SessionThinkingOverride(settings, "s1", "model-a"); got != ThinkingModeReasoningLow {
		t.Fatalf("per-model override = %q", got)
	}
	if got := SessionThinkingOverride(settings, "s1", "model-b"); got != ThinkingModeReasoningHigh {
		t.Fatalf("other model fell back to legacy = %q, want the legacy value", got)
	}
	// Unknown models keep their own (empty) per-model row separate too.
	if got := SessionThinkingOverride(settings, "s1", ""); got != ThinkingModeReasoningHigh {
		t.Fatalf("empty model override = %q", got)
	}
}

func TestSessionThinkingOverrideKeys(t *testing.T) {
	keys := SessionThinkingOverrideKeys("s1", "model-a")
	want := []string{
		SessionModelThinkingSettingKey("s1", "model-a"),
		SessionThinkingSettingKey("s1"),
		SessionEffortSettingKey("s1"),
	}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for index := range want {
		if keys[index] != want[index] {
			t.Fatalf("keys[%d] = %q, want %q", index, keys[index], want[index])
		}
	}
	// A missing model id still clears the two legacy rows.
	if got := SessionThinkingOverrideKeys("s1", ""); len(got) != 2 {
		t.Fatalf("empty-model keys = %v", got)
	}
}
