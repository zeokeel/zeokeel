package agent

// The "my role does not resolve" disclosure (2026-11-26 audit, 高).
//
// A session whose stored role cannot be resolved gets no tools and every call is
// refused (filterSessionTools / presetToolsDenial fail closed — that part is
// deliberate). What was missing is the OTHER half: the sentence the user could
// read. The selection is a settings row (`session.<id>.preset`), the failure was
// one log line, and the chat screen showed an ordinary conversation that simply
// refused to do anything.
//
// Sessions() is the read model the sidebar and the composer already consume, so
// the diagnosis travels there: the stored id, and a stable problem code the
// client turns into words. The gate does not read either field.

import (
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSessionsReportAnUnresolvableRole(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(),
		&policyApprovals{mode: contracts.PermissionWorkspace})

	plain, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	good, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(good, contracts.PresetLeadID); err != nil {
		t.Fatalf("SetSessionPreset(lead): %v", err)
	}
	broken, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// The case the guard cannot prevent: a row written by an older document, or
	// a role that was deleted after it was selected. Written straight to the
	// store, exactly like `settings.set` used to allow.
	if err := settings.Set(contracts.SessionPresetSettingKey(broken), "ghost"); err != nil {
		t.Fatalf("write broken preset row: %v", err)
	}

	infos := make(map[string]contracts.ChatSessionInfo, 3)
	for _, info := range svc.Sessions() {
		infos[info.ID] = info
	}

	if info := infos[plain]; info.PresetID != "" || info.PresetProblem != "" {
		t.Fatalf("a session with no role reports %q/%q, want both empty", info.PresetID, info.PresetProblem)
	}
	if info := infos[good]; info.PresetID != contracts.PresetLeadID || info.PresetProblem != "" {
		t.Fatalf("a session with a resolvable role reports %q/%q, want %q/\"\"", info.PresetID, info.PresetProblem, contracts.PresetLeadID)
	}
	brokenInfo := infos[broken]
	if brokenInfo.PresetID != "ghost" {
		t.Fatalf("unresolvable session reports preset id %q, want %q", brokenInfo.PresetID, "ghost")
	}
	if brokenInfo.PresetProblem != contracts.PresetProblemUnknown {
		t.Fatalf("unresolvable session reports problem %q, want %q", brokenInfo.PresetProblem, contracts.PresetProblemUnknown)
	}
	// The diagnosis is a code, not an internal message: the client owns the
	// words, and a path or stack here would leak into the UI.
	if len(brokenInfo.PresetProblem) > 40 {
		t.Fatalf("problem code is not stable and short: %q", brokenInfo.PresetProblem)
	}
}
