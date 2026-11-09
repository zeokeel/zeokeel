package session

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestPruneMessageStateKeepsActiveSession covers the bounded per-message UI
// state: switching conversations drops fold/usage/expanded/transcript-window
// entries that belong to the conversation that is no longer on screen, so a
// long-running app does not accumulate state for every session the user opens.
func TestPruneMessageStateKeepsActiveSession(t *testing.T) {
	st := &state{
		scrollSession: "s2",
		thinkOpen:     map[string]bool{"m1": true, "m2": true},
		toolOpen: map[string]bool{
			"session.tool.m1.tool_a": true,
			"session.tool.m2.tool_b": true,
		},
		usageOpen:       map[string]bool{"m1": true, "m2": true},
		expandedActions: "m1",
		transcriptStart: map[string]int{"s1": 50, "s2": 20},
	}
	pruneMessageState(st, []contracts.ChatMessage{{ID: "m2"}})

	if _, ok := st.thinkOpen["m1"]; ok {
		t.Fatal("thinkOpen kept a message from another conversation")
	}
	if !st.thinkOpen["m2"] {
		t.Fatal("thinkOpen dropped the active conversation's row")
	}
	if _, ok := st.toolOpen["session.tool.m1.tool_a"]; ok {
		t.Fatal("toolOpen kept a tool row from another conversation")
	}
	if !st.toolOpen["session.tool.m2.tool_b"] {
		t.Fatal("toolOpen dropped the active conversation's row")
	}
	if _, ok := st.usageOpen["m1"]; ok {
		t.Fatal("usageOpen kept a message from another conversation")
	}
	if st.expandedActions != "" {
		t.Fatalf("expandedActions = %q, want cleared", st.expandedActions)
	}
	if _, ok := st.transcriptStart["s1"]; ok {
		t.Fatal("transcriptStart kept another session's window")
	}
	if st.transcriptStart["s2"] != 20 {
		t.Fatalf("transcriptStart[s2] = %v, want 20", st.transcriptStart["s2"])
	}
}
