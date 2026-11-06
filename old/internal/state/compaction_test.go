package state

import (
	"reflect"
	"testing"
)

// compactedEvent is the persisted /compact checkpoint: the replaced message ids
// plus the summary message that stands in for them.
func compactedEvent() Compaction {
	return Compaction{
		ReplacedMsgIDs: []string{"old-1", "old-2"},
		Summary: Message{
			MsgID: "summary-1",
			Role:  RoleAssistant,
			Text:  "Earlier conversation compacted (2 messages)\n\nsummary text",
		},
	}
}

// TestApplyCompactionReplacesHistory pins the fold half of the persistent
// /compact: the replaced messages leave the snapshot, the summary message takes
// their position, the recent tail keeps its order, and applying the same event
// again (replay after the messages table was already compacted) is a no-op.
func TestApplyCompactionReplacesHistory(t *testing.T) {
	snap := NewSnapshot()
	snap = Apply(snap, Token{SessionID: "s", MsgID: "old-1", Text: "old question"})
	snap = Apply(snap, Token{MsgID: "old-2", Text: "old answer"})
	snap = Apply(snap, Token{MsgID: "recent", Text: "recent question"})
	before := snap
	event := compactedEvent()

	next := Apply(snap, event)
	if got, want := messageIDs(next), []string{"summary-1", "recent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("messages after compaction = %v, want %v", got, want)
	}
	summary := next.Messages[0]
	if summary.Role != RoleAssistant || summary.Text != event.Summary.Text {
		t.Fatalf("summary message = %+v", summary)
	}
	if summary.Seq != next.Seq || summary.LastSeq != next.Seq {
		t.Fatalf("summary seq = %d/%d, want the event seq %d", summary.Seq, summary.LastSeq, next.Seq)
	}
	// The previous snapshot is immutable: folding never mutates it.
	if got, want := messageIDs(before), []string{"old-1", "old-2", "recent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Apply mutated the previous snapshot: %v", got)
	}

	// Replaying the same event is idempotent: the replaced ids are already
	// gone and the summary message is already present.
	again := Apply(next, event)
	if !reflect.DeepEqual(messageIDs(again), []string{"summary-1", "recent"}) {
		t.Fatalf("replayed compaction changed messages: %v", messageIDs(again))
	}
	if len(again.Messages) != 2 {
		t.Fatalf("replayed compaction duplicated rows: %v", messageIDs(again))
	}
}

// TestApplyCompactionReloadedSnapshot covers the reopen path: buildSnapshot
// seeds the snapshot from the already-compacted messages table, then the
// persisted compaction event replays over it. The summary is not duplicated and
// the removed ids are already absent.
func TestApplyCompactionReloadedSnapshot(t *testing.T) {
	snap := NewSnapshot()
	snap.Messages = []Message{
		{MsgID: "summary-1", Role: RoleAssistant, Text: "summary"},
		{MsgID: "recent", Role: RoleUser, Text: "recent"},
	}
	next := Apply(snap, compactedEvent())
	if got, want := messageIDs(next), []string{"summary-1", "recent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed compaction over a reloaded snapshot = %v, want %v", got, want)
	}
}

// TestApplyBatchCompactionMatchesApply keeps the AR-22 invariant: a batch that
// carries a compaction event folds to the same snapshot as one Apply per event.
func TestApplyBatchCompactionMatchesApply(t *testing.T) {
	events := []Event{
		Token{SessionID: "s", MsgID: "old-1", Text: "old"},
		Token{MsgID: "old-2", Text: "old answer"},
		ToolStart{CallID: "call-1", Name: "bash", Args: []byte(`{}`)},
		Token{MsgID: "recent", Text: "recent"},
		compactedEvent(),
		Token{MsgID: "new", Text: "after"},
	}
	want := referenceFold(nil, events)
	got := ApplyBatch(nil, events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyBatch with Compaction = %+v, want sequential %+v", got, want)
	}
	if ids := messageIDs(got); !reflect.DeepEqual(ids, []string{"summary-1", "recent", "new"}) {
		t.Fatalf("batch compaction messages = %v", ids)
	}
}

// messageIDs projects the snapshot message ids for assertions.
func messageIDs(snap *Snapshot) []string {
	out := make([]string, 0, len(snap.Messages))
	for _, message := range snap.Messages {
		out = append(out, message.MsgID)
	}
	return out
}
