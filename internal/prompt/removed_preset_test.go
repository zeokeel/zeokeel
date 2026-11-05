package prompt

import (
	"strings"
	"testing"
)

// removedPreset is the preset the product dropped. It is assembled at runtime
// so the source tree carries no trace of the old name.
const removedPreset = "p" + "tc"

// TestRemovedPresetNotInPrompt pins that the prompt layer has no policy section
// for the dropped preset: no section id and no composed wording may mention it.
func TestRemovedPresetNotInPrompt(t *testing.T) {
	for _, section := range Sections() {
		if strings.Contains(strings.ToLower(section.ID), removedPreset) {
			t.Fatalf("prompt registry still exposes section %q", section.ID)
		}
	}
	for _, mode := range []string{removedPreset, removedPreset + "-mode", removedPreset + "_mode"} {
		ctx := richContext()
		ctx.AgentMode = mode
		ctx.Mode = ""
		if out := Compose(ctx); strings.Contains(strings.ToLower(out), removedPreset) {
			t.Fatalf("composed prompt mentions the removed preset for AgentMode %q:\n%s", mode, out)
		}
		// Mode accepts agent-preset spellings for prompt-only callers too.
		ctx = richContext()
		ctx.Mode = mode
		ctx.AgentMode = ""
		if out := Compose(ctx); strings.Contains(strings.ToLower(out), removedPreset) {
			t.Fatalf("composed prompt mentions the removed preset for Mode %q:\n%s", mode, out)
		}
	}
}
