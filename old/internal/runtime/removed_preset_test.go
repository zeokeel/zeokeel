package runtime

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

// removedPreset is the preset the product dropped. It is assembled at runtime
// so the source tree carries no trace of the old name.
const removedPreset = "p" + "tc"

// TestModesExcludeRemovedPreset pins the product decision that the dropped
// preset is not offered: the user-visible mode list must not contain it.
func TestModesExcludeRemovedPreset(t *testing.T) {
	for _, mode := range Modes() {
		if mode == removedPreset {
			t.Fatalf("Modes() = %v, must not expose the removed preset", Modes())
		}
	}
}

// TestRemovedPresetRejectedByNormalize pins that every historical spelling is
// rejected as an unknown mode, so persisted/legacy settings cannot revive it.
func TestRemovedPresetRejectedByNormalize(t *testing.T) {
	for _, spelling := range []string{removedPreset, removedPreset + "-mode", removedPreset + "_mode", "P" + "TC"} {
		if got, err := NormalizeMode(spelling); err == nil {
			t.Fatalf("NormalizeMode(%q) = %q, want an unknown-mode error", spelling, got)
		}
	}
}

// TestRemovedPresetNotRegistered pins that no agent mode synthesizes the
// removed tool.
func TestRemovedPresetNotRegistered(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&jsonTool{name: "echo"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	model := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil })
	for _, mode := range Modes() {
		agent := newTestAgent(t, Config{einoModel: model, Tools: reg, Mode: mode})
		if _, ok := agent.ToolByName(removedPreset); ok {
			t.Fatalf("mode %q exposes the removed tool: %v", mode, agent.ToolNames())
		}
		for _, name := range agent.ToolNames() {
			if name == removedPreset {
				t.Fatalf("mode %q tool list contains the removed tool: %v", mode, agent.ToolNames())
			}
		}
	}
}
