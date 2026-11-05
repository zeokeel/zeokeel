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

// reasoningFirstSnapshot is the real-world turn shape the old ordering got
// wrong: one assistant message is created by the Reasoning event that precedes
// the tool call, and its visible text only arrives afterwards.
//
//	user(Seq 1) -> Reasoning(2) -> ToolStart(3) -> ToolEnd(4) -> Token(5)
func reasoningFirstSnapshot() *state.Snapshot {
	snap := state.NewSnapshot()
	snap.Seq = 1
	snap.Messages = []state.Message{
		{MsgID: "u1", Seq: 1, LastSeq: 1, Role: state.RoleUser, Text: "question"},
	}
	snap = state.Apply(snap, state.Reasoning{MsgID: "a1", Text: "let me check"})
	snap = state.Apply(snap, state.ToolStart{CallID: "c1", Name: "bash"})
	snap = state.Apply(snap, state.ToolEnd{CallID: "c1", Result: "ok"})
	snap = state.Apply(snap, state.Token{MsgID: "a1", Text: "final answer"})
	return snap
}

// TestBuildTranscriptTurnsReasoningFirstPutsProcessAboveAnswer is the order
// regression: the assistant message's first event is pre-tool Reasoning, so
// its Seq is before the call; the message must still render below the process
// block because its visible text ends after the call (LastSeq). Before
// LastSeq/end-based placement this produced user -> answer -> block.
func TestBuildTranscriptTurnsReasoningFirstPutsProcessAboveAnswer(t *testing.T) {
	snap := reasoningFirstSnapshot()
	msg, tool := snap.Messages[1], snap.Tools["c1"]
	if !(msg.Seq < tool.StartedSeq && tool.StartedSeq < msg.LastSeq) {
		t.Fatalf("fixture axis = msg Seq %d, tool StartedSeq %d, msg LastSeq %d; want Seq < tool < LastSeq",
			msg.Seq, tool.StartedSeq, msg.LastSeq)
	}

	plans := buildTranscriptTurns(snap)
	if len(plans) != 1 {
		t.Fatalf("turns = %d, want 1", len(plans))
	}
	if got, want := orderMessageIDs(t, snap, plans[0]), []string{"u1", "block:c1", "a1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("turn order = %v, want %v (process block must be above the answer)", got, want)
	}
}

// TestBuildTranscriptTurnsReasoningFirstMultiTurn checks the same shape on two
// turns: each turn keeps its own block above its own answer, and the second
// turn stays below the first turn's answer.
func TestBuildTranscriptTurnsReasoningFirstMultiTurn(t *testing.T) {
	snap := reasoningFirstSnapshot()
	// Turn 2: user(6) -> Reasoning(7) -> ToolStart(8) -> ToolEnd(9) -> Token(10).
	snap.Seq = 6
	snap.Messages = append(snap.Messages,
		state.Message{MsgID: "u2", Seq: 6, LastSeq: 6, Role: state.RoleUser, Text: "second question"})
	snap = state.Apply(snap, state.Reasoning{MsgID: "a2", Text: "checking again"})
	snap = state.Apply(snap, state.ToolStart{CallID: "c2", Name: "read_file"})
	snap = state.Apply(snap, state.ToolEnd{CallID: "c2", Result: "contents"})
	snap = state.Apply(snap, state.Token{MsgID: "a2", Text: "second answer"})

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

// TestBuildTranscriptTurnsStreamingAfterToolKeepsBlockAbove pins streaming
// stability: once the answer has any token after the call, the block stays
// above it on every later token frame.
func TestBuildTranscriptTurnsStreamingAfterToolKeepsBlockAbove(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Seq = 1
	snap.Messages = []state.Message{
		{MsgID: "u1", Seq: 1, LastSeq: 1, Role: state.RoleUser, Text: "question"},
	}
	snap = state.Apply(snap, state.Reasoning{MsgID: "a1", Text: "thinking"})
	snap = state.Apply(snap, state.ToolStart{CallID: "c1", Name: "bash"})
	snap = state.Apply(snap, state.ToolEnd{CallID: "c1", Result: "ok"})

	// Before the first visible token the message has no text yet and no row.
	if got := orderMessageIDs(t, snap, buildTranscriptTurns(snap)[0]); !reflect.DeepEqual(got, []string{"u1", "block:c1"}) {
		t.Fatalf("pre-token order = %v, want [u1 block:c1]", got)
	}
	// Every token frame keeps the block above the growing answer.
	for i := 0; i < 3; i++ {
		snap = state.Apply(snap, state.Token{MsgID: "a1", Text: "chunk"})
		if got := orderMessageIDs(t, snap, buildTranscriptTurns(snap)[0]); !reflect.DeepEqual(got, []string{"u1", "block:c1", "a1"}) {
			t.Fatalf("token frame %d order = %v, want block above answer", i+1, got)
		}
	}
}

// TestBuildTranscriptTurnsAnswerBeforeToolStaysAbove pins the other direction:
// text recorded entirely before the call stays above the block (the message
// ended before the call started, e.g. a text-only assistant step followed by a
// later turn's question in hand-built data).
func TestBuildTranscriptTurnsAnswerBeforeToolStaysAbove(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Seq = 1
	snap.Messages = []state.Message{
		{MsgID: "u1", Seq: 1, LastSeq: 1, Role: state.RoleUser, Text: "question"},
	}
	snap = state.Apply(snap, state.Token{MsgID: "a1", Text: "answer"})    // Seq 2
	snap = state.Apply(snap, state.ToolStart{CallID: "c1", Name: "bash"}) // Seq 3
	snap = state.Apply(snap, state.ToolEnd{CallID: "c1", Result: "ok"})   // Seq 4
	// A second visible message created after the tool ends up below it.
	snap = state.Apply(snap, state.Token{MsgID: "a2", Text: "later"}) // Seq 5

	plans := buildTranscriptTurns(snap)
	if len(plans) != 1 {
		t.Fatalf("turns = %d, want 1", len(plans))
	}
	want := []string{"u1", "a1", "block:c1", "a2"}
	if got := orderMessageIDs(t, snap, plans[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn order = %v, want %v", got, want)
	}
}
