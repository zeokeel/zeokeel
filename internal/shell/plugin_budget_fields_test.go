package shell

import "testing"

// TestAgentLoopPluginBudgetFields pins the two soft-budget settings rows on the
// agent-loop plugin detail: they exist, persist through the plugin settings
// path, and default to empty (= runtime defaults) rather than a fixed value.
func TestAgentLoopPluginBudgetFields(t *testing.T) {
	spec, ok := pluginDetailForKey("agent_loop")
	if !ok {
		t.Fatal("agent_loop plugin detail spec missing")
	}
	fields := map[string]pluginDetailField{}
	for _, field := range spec.fields {
		fields[field.key] = field
	}
	for _, key := range []string{
		"plugin.agent_loop.budget_warn_steps",
		"plugin.agent_loop.budget_warn_tokens",
	} {
		field, ok := fields[key]
		if !ok {
			t.Fatalf("field %q missing from agent_loop detail", key)
		}
		if field.kind != pluginFieldText {
			t.Fatalf("field %q kind = %v, want text", key, field.kind)
		}
		if field.def != "" {
			t.Fatalf("field %q default = %q, want empty (runtime defaults)", key, field.def)
		}
		if field.labelEN == "" || field.labelZH == "" {
			t.Fatalf("field %q missing localized labels: %+v", key, field)
		}
	}
}
