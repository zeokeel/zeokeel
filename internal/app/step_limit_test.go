package app

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
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
// the runtime default (a high safety bound, not the old 20) applies when the
// user has not configured one, configured values above the old editor limit are
// honored up to the new limit, and values beyond it clamp instead of disabling
// the ceiling.
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
	closeBundle := func(bundle *AgentBundle) {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	}

	def := build()
	if got := def.Agent.MaxIterations(); got != runtime.DefaultMaxIterations {
		t.Fatalf("default MaxIterations = %d, want %d", got, runtime.DefaultMaxIterations)
	}
	closeBundle(def)

	if err := st.SetSetting("plugin.agent_loop.max_iterations", "700"); err != nil {
		t.Fatal(err)
	}
	configured := build()
	if got := configured.Agent.MaxIterations(); got != 700 {
		t.Fatalf("configured MaxIterations = %d, want 700", got)
	}
	closeBundle(configured)

	if err := st.SetSetting("plugin.agent_loop.max_iterations", "100000"); err != nil {
		t.Fatal(err)
	}
	clamped := build()
	if got := clamped.Agent.MaxIterations(); got != runtime.MaxIterationsLimit {
		t.Fatalf("clamped MaxIterations = %d, want %d", got, runtime.MaxIterationsLimit)
	}
	closeBundle(clamped)
}
