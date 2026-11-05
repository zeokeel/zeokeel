package shell

import (
	"reflect"
	"testing"

	"github.com/zeokeel/zeokeel/internal/state"
)

// orderedSnapshot builds a snapshot with explicit record positions: messages
// are (MsgID, role, text, Seq) and tools are (CallID, name, StartedSeq).
type orderMessage struct {
	id   string
	role state.Role
	text string
	seq  uint64
}

type orderTool struct {
	callID string
	name   string
	seq    uint64
}

func orderedSnapshot(msgs []orderMessage, tools []orderTool) *state.Snapshot {
	snap := state.NewSnapshot()
	for _, m := range msgs {
		snap.Messages = append(snap.Messages, state.Message{
			MsgID: m.id, Role: m.role, Text: m.text, Seq: m.seq,
		})
	}
	for _, tool := range tools {
		snap.Tools[tool.callID] = state.ToolState{
			CallID: tool.callID, Name: tool.name, Status: state.ToolDone, StartedSeq: tool.seq,
		}
	}
	return snap
}

func orderMessageIDs(t *testing.T, snap *state.Snapshot, plan turnPlan) []string {
	t.Helper()
	ids := make([]string, 0, len(plan.entries))
	for _, entry := range plan.entries {
		if entry.tools != nil {
			names := make([]string, 0, len(entry.tools))
			for _, tool := range entry.tools {
				names = append(names, tool.CallID)
			}
			ids = append(ids, "block:"+joinIDs(names))
			continue
		}
		ids = append(ids, snap.Messages[entry.msgIndex].MsgID)
	}
	return ids
}

func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}

// TestBuildTranscriptTurnsMergesByRecordSeq pins the core order: a user
// message, the calls recorded next, then the assistant answer.
func TestBuildTranscriptTurnsMergesByRecordSeq(t *testing.T) {
	snap := orderedSnapshot(
		[]orderMessage{
			{"u1", state.RoleUser, "question", 1},
			{"a1", state.RoleAssistant, "answer", 5},
		},
		[]orderTool{{"c1", "bash", 3}},
	)
	plans := buildTranscriptTurns(snap)
	if len(plans) != 1 {
		t.Fatalf("turns = %d, want 1", len(plans))
	}
	want := []string{"u1", "block:c1", "a1"}
	if got := orderMessageIDs(t, snap, plans[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn order = %v, want %v", got, want)
	}
}

// TestBuildTranscriptTurnsKeepsTurnsSeparate pins the multi-turn fix: each
// turn's calls stay inside that turn instead of accumulating at the tail.
func TestBuildTranscriptTurnsKeepsTurnsSeparate(t *testing.T) {
	snap := orderedSnapshot(
		[]orderMessage{
			{"u1", state.RoleUser, "q1", 1},
			{"a1", state.RoleAssistant, "a1", 5},
			{"u2", state.RoleUser, "q2", 8},
			{"a2", state.RoleAssistant, "a2", 12},
		},
		[]orderTool{
			{"c1", "bash", 3},
			{"c2", "read_file", 10},
		},
	)
	plans := buildTranscriptTurns(snap)
	if len(plans) != 2 {
		t.Fatalf("turns = %d, want 2", len(plans))
	}
	if got, want := orderMessageIDs(t, snap, plans[0]), []string{"u1", "block:c1", "a1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("turn 1 order = %v, want %v", got, want)
	}
	if got, want := orderMessageIDs(t, snap, plans[1]), []string{"u2", "block:c2", "a2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("turn 2 order = %v, want %v", got, want)
	}
}

// TestBuildTranscriptTurnsGroupsAdjacentCalls pins block grouping: calls with
// no assistant message between them share one summary/card block, and a call
// recorded after an intermediate answer starts a new block.
func TestBuildTranscriptTurnsGroupsAdjacentCalls(t *testing.T) {
	snap := orderedSnapshot(
		[]orderMessage{
			{"u1", state.RoleUser, "q1", 1},
			{"mid", state.RoleAssistant, "working on it", 4},
			{"a1", state.RoleAssistant, "final", 8},
		},
		[]orderTool{
			{"c1", "bash", 2},
			{"c2", "read_file", 3},
			{"c3", "bash", 6},
		},
	)
	plans := buildTranscriptTurns(snap)
	if len(plans) != 1 {
		t.Fatalf("turns = %d, want 1", len(plans))
	}
	want := []string{"u1", "block:c1,c2", "mid", "block:c3", "a1"}
	if got := orderMessageIDs(t, snap, plans[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn order = %v, want %v", got, want)
	}
}

// TestBuildTranscriptTurnsKeepsUnknownSeqInListPlace guards the fixture
// degradation rule: Message.Seq 0 means "unknown", never "first". A partially
// known snapshot keeps the unknown message where it was listed.
func TestBuildTranscriptTurnsKeepsUnknownSeqInListPlace(t *testing.T) {
	snap := orderedSnapshot(
		[]orderMessage{
			{"u1", state.RoleUser, "question", 0},
			{"a1", state.RoleAssistant, "answer", 5},
		},
		[]orderTool{{"c1", "bash", 3}},
	)
	plans := buildTranscriptTurns(snap)
	if len(plans) != 1 {
		t.Fatalf("turns = %d, want 1", len(plans))
	}
	want := []string{"u1", "block:c1", "a1"}
	if got := orderMessageIDs(t, snap, plans[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn order = %v, want %v", got, want)
	}
}

// TestTranscriptOrderEnabledFallsBackForHandBuiltSnapshots pins the
// degradation switch: a fixture with every Message.Seq 0 uses the legacy tail
// layout instead of guessing positions.
func TestTranscriptOrderEnabledFallsBackForHandBuiltSnapshots(t *testing.T) {
	handBuilt := orderedSnapshot(
		[]orderMessage{{"u1", state.RoleUser, "question", 0}},
		[]orderTool{{"c1", "bash", 0}},
	)
	if transcriptOrderEnabled(handBuilt) {
		t.Fatal("a snapshot without message Seq must use the legacy layout")
	}
	live := orderedSnapshot(
		[]orderMessage{{"u1", state.RoleUser, "question", 1}},
		[]orderTool{{"c1", "bash", 2}},
	)
	if !transcriptOrderEnabled(live) {
		t.Fatal("a snapshot with a message Seq must use the record order")
	}
}

// TestSnapshotToolStatesOrdersByStartSeq pins the card order: start Seq
// ascending with the call ID as a stable tiebreak, replacing the old
// CallID-string order that could invert the record.
func TestSnapshotToolStatesOrdersByStartSeq(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Tools["zzz-1"] = state.ToolState{CallID: "zzz-1", Name: "bash", StartedSeq: 3}
	snap.Tools["aaa-2"] = state.ToolState{CallID: "aaa-2", Name: "read_file", StartedSeq: 10}
	snap.Tools["b-0"] = state.ToolState{CallID: "b-0", Name: "echo", StartedSeq: 0}
	snap.Tools["a-0"] = state.ToolState{CallID: "a-0", Name: "echo", StartedSeq: 0}

	got := snapshotToolStates(snap)
	want := []string{"a-0", "b-0", "zzz-1", "aaa-2"}
	ids := make([]string, 0, len(got))
	for _, tool := range got {
		ids = append(ids, tool.CallID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("tool order = %v, want %v", ids, want)
	}
}
