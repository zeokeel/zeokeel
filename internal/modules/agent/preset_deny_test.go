package agent

// The preset denylist (M3) at the two places it must bite: the tool surface the
// model sees, and the gate that decides a call.
//
// The role this exists for: the executor keeps the full tool surface (it does the
// work) but must not be able to sign off its own task. "Everything except
// task_review" cannot be written as an allowlist without freezing the catalog,
// so it is a deny — and a deny that only filtered the surface would be advice,
// not a rule: a replayed call or a teammate's serialized call would still get in.

import (
	"strings"
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func denyService(t *testing.T) (*service, string) {
	t.Helper()
	raw, err := contracts.EncodeAgentPresets([]contracts.AgentPreset{
		{ID: "worker", Name: "Worker", Deny: []string{"task_review"}},
	})
	if err != nil {
		t.Fatalf("encode presets: %v", err)
	}
	registry := rigtools.NewRegistry(
		presetTool{name: "bash"},
		presetTool{name: "write_file"},
		presetTool{name: "task_review"},
		presetTool{name: "task_submit"},
	)
	settings := newFakeSettings(map[string]string{contracts.SettingAgentPresets: raw})
	svc := newService(Config{SystemPrompt: "base"}, registry, settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(sessionID, "worker"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	return svc, sessionID
}

func TestPresetDenyRemovesTheToolFromTheSurface(t *testing.T) {
	svc, sessionID := denyService(t)
	got := map[string]bool{}
	for _, tool := range svc.filterSessionTools(sessionID, svc.enabledRegistryTools()) {
		got[tool.Name()] = true
	}
	if got["task_review"] {
		t.Fatalf("surface = %v, want task_review removed by the role's deny list", got)
	}
	for _, want := range []string{"bash", "write_file", "task_submit"} {
		if !got[want] {
			t.Fatalf("surface = %v, want %s kept (deny is not an allowlist)", got, want)
		}
	}
}

func TestPresetDenyRefusesAtTheGate(t *testing.T) {
	svc, sessionID := denyService(t)
	preset, ok := svc.agentPresetForSession(sessionID)
	if !ok {
		t.Fatal("session preset did not resolve")
	}
	if denial := presetToolsDenialForPreset(preset, "task_review"); denial == "" {
		t.Fatal("gate allowed a denied tool")
	} else if !strings.Contains(denial, "task_review") || !strings.Contains(denial, "worker") {
		t.Fatalf("denial = %q, want the tool and the role named", denial)
	}
	if denial := presetToolsDenialForPreset(preset, "bash"); denial != "" {
		t.Fatalf("gate refused bash (%q); deny must only remove what it names", denial)
	}
}

// The built-in executor is the role this exists for: it keeps everything but the
// review tool, and the contract row says so.
func TestBuiltinExecutorDeniesTheReviewTool(t *testing.T) {
	var executor contracts.AgentPreset
	for _, preset := range contracts.BuiltinAgentPresets() {
		if preset.ID == contracts.PresetExecutorID {
			executor = preset
		}
	}
	if executor.ID == "" {
		t.Fatal("builtin executor preset is missing")
	}
	denied := map[string]bool{}
	for _, name := range executor.Deny {
		denied[name] = true
	}
	if !denied["task_review"] {
		t.Fatalf("executor deny = %v, want task_review", executor.Deny)
	}
	if denial := presetToolsDenialForPreset(executor, "task_review"); denial == "" {
		t.Fatal("executor role could still call task_review")
	}
	if denial := presetToolsDenialForPreset(executor, "task_submit"); denial != "" {
		t.Fatalf("executor was denied task_submit (%q); it must be able to submit", denial)
	}
}
