package app

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// TestStepLimitNoticeFollowsLanguage pins the assistant copy the runtime emits
// when a turn hits its step ceiling: Chinese by default (the product language)
// and English when ui.language says so, always naming the limit so the user
// knows what happened.
func TestStepLimitNoticeFollowsLanguage(t *testing.T) {
	en := stepLimitNoticeFor("en")
	if got := en(7); !strings.Contains(got, "step limit (7 steps)") ||
		!strings.Contains(got, "continue") {
		t.Fatalf("English notice = %q", got)
	}
	zh := stepLimitNoticeFor("zh")
	if got := zh(7); !strings.Contains(got, "步骤上限（7 步）") ||
		!strings.Contains(got, "继续发消息") {
		t.Fatalf("Chinese notice = %q", got)
	}
	// A missing or unknown ui.language follows the product's Chinese default.
	for _, lang := range []string{"", "fr"} {
		if got := stepLimitNoticeFor(lang)(3); !strings.Contains(got, "步骤上限") {
			t.Fatalf("notice for language %q = %q, want the Chinese default", lang, got)
		}
	}
}

// TestBuildAgentStepCeiling covers the app wiring of the per-turn step ceiling:
// the default is 0 (no fixed ceiling, DSH-aligned: the model decides when to
// stop), a positive setting is honored up to the editor limit, values beyond it
// clamp, negative or unreadable values fall back to 0 (unlimited), and the
// disabled agent-loop plugin keeps its single-round-trip meaning.
func TestBuildAgentStepCeiling(t *testing.T) {
	st, dir := newToolStore(t)
	for key, value := range map[string]string{
		"provider.api_key": "test-key",
		"provider.model":   "test-model",
	} {
		if err := st.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	build := func() *AgentBundle {
		t.Helper()
		bundle, err := buildAgent(st, dir, "instruction")
		if err != nil {
			t.Fatalf("buildAgent: %v", err)
		}
		if bundle == nil || bundle.Agent == nil {
			t.Fatal("buildAgent returned no agent")
		}
		return bundle
	}
	maxIterations := func(value string) int {
		t.Helper()
		if value != "" {
			if err := st.SetSetting("plugin.agent_loop.max_iterations", value); err != nil {
				t.Fatal(err)
			}
		}
		bundle := build()
		defer func() {
			if bundle.Close != nil {
				_ = bundle.Close()
			}
		}()
		return bundle.Agent.MaxIterations()
	}

	// Missing setting: no fixed ceiling, like DSH.
	if got := maxIterations(""); got != 0 {
		t.Fatalf("default MaxIterations = %d, want 0 (unlimited)", got)
	}
	// Explicit positive values are kept, clamped to the editor limit.
	if got := maxIterations("700"); got != 700 {
		t.Fatalf("configured MaxIterations = %d, want 700", got)
	}
	if got := maxIterations("100000"); got != runtime.MaxIterationsLimit {
		t.Fatalf("clamped MaxIterations = %d, want %d", got, runtime.MaxIterationsLimit)
	}
	// A negative or unreadable value means unlimited, never "one step".
	if got := maxIterations("-5"); got != 0 {
		t.Fatalf("negative MaxIterations = %d, want 0 (unlimited)", got)
	}
	if got := maxIterations("not-a-number"); got != 0 {
		t.Fatalf("unreadable MaxIterations = %d, want 0 (unlimited)", got)
	}
	if got := maxIterations("0"); got != 0 {
		t.Fatalf("explicit zero MaxIterations = %d, want 0 (unlimited)", got)
	}
	// Disabling the agent-loop plugin keeps its old single-round-trip meaning.
	if err := st.SetSetting("plugin.agent_loop.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if got := maxIterations("700"); got != 1 {
		t.Fatalf("disabled agent-loop MaxIterations = %d, want 1", got)
	}
}
