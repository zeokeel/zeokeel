package gateway

// The role problem on the wire (2026-11-26 audit, 高).
//
// The agent's read model reports the stored role and whether it resolves
// (contracts.ChatSessionInfo.PresetID / PresetProblem). The session list is the
// surface both the sidebar and the composer read, so the JSON must carry both —
// and must carry nothing at all for the sessions that are fine: an optional field
// that is serialized as "" would make every healthy session look diagnosed.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSessionInfoDTOCarriesTheRoleProblem(t *testing.T) {
	broken, err := json.Marshal(newSessionInfoDTO(contracts.ChatSessionInfo{
		ID: "s1", PresetID: "ghost", PresetProblem: contracts.PresetProblemUnknown,
	}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(broken), `"presetId":"ghost"`) {
		t.Fatalf("session row lost the stored role: %s", broken)
	}
	if !strings.Contains(string(broken), `"presetProblem":"unknown-preset"`) {
		t.Fatalf("session row lost the problem code: %s", broken)
	}

	healthy, err := json.Marshal(newSessionInfoDTO(contracts.ChatSessionInfo{ID: "s2", PresetID: contracts.PresetLeadID}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(healthy), `"presetId":"lead"`) {
		t.Fatalf("a resolvable role is not reported: %s", healthy)
	}
	if strings.Contains(string(healthy), "presetProblem") {
		t.Fatalf("a healthy session reported a problem: %s", healthy)
	}

	plain, err := json.Marshal(newSessionInfoDTO(contracts.ChatSessionInfo{ID: "s3"}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(plain), "preset") {
		t.Fatalf("a session with no role carries preset fields: %s", plain)
	}
}

// TestSessionListDeltaCarriesARoleProblem pins the delivery half: the session
// list is sent as a snapshot and then as row-level deltas, and a delta is only
// produced when a row differs. If the role fields are not part of that comparison
// a session that just became broken (or was just fixed) keeps the stale row on
// every open client — the diagnosis would exist on the wire and never arrive.
func TestSessionListDeltaCarriesARoleProblem(t *testing.T) {
	healthy := []sessionInfoDTO{{ID: "s1", Title: "Work", PresetID: contracts.PresetLeadID}}
	broken := []sessionInfoDTO{{ID: "s1", Title: "Work", PresetID: "ghost", PresetProblem: contracts.PresetProblemUnknown}}
	delta := diffSessionList(healthy, broken)
	if len(delta.Changed) != 1 {
		t.Fatalf("a role that stopped resolving produced no row change: %+v", delta)
	}
	if delta.Changed[0].PresetProblem != contracts.PresetProblemUnknown {
		t.Fatalf("the changed row does not carry the problem: %+v", delta.Changed[0])
	}
	// And the same row twice is still no delta (nothing to redraw).
	if idle := diffSessionList(broken, broken); len(idle.Changed) != 0 || len(idle.Added) != 0 {
		t.Fatalf("an unchanged board produced a delta: %+v", idle)
	}
}
