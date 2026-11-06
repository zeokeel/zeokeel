package state

// Compaction is one manual /compact checkpoint: an earlier range of the
// conversation was replaced by a summary, and the fold removes the replaced
// messages from the snapshot and inserts the summary in their place. The same
// event is persisted in the session's event trace, so a reopened session's
// snapshot (seeded from the already-compacted messages table) and the live
// snapshot agree, and folding it twice is a no-op.
//
// Compaction happens while the session is idle, so it carries no turn id. The
// event itself never reaches the model; the summary message it inserts is the
// model-visible stand-in for the replaced history.
type Compaction struct {
	// ReplacedMsgIDs are the message ids the compaction dropped, in their
	// original order. Replay removes exactly these rows.
	ReplacedMsgIDs []string
	// Summary is the message that stands in for the replaced range. The store
	// assigns its id, so replay can recognize an already-inserted summary.
	Summary Message
}

func (Compaction) isEvent() {}

// applyCompactionMessages folds a Compaction into a snapshot message slice:
// replaced ids are removed and the summary takes the first replaced message's
// position. A summary already present in the slice (replay over an
// already-compacted conversation) is left in place instead of duplicated, which
// makes folding the same event twice idempotent.
func applyCompactionMessages(prev []Message, e Compaction, seq uint64) []Message {
	if len(e.ReplacedMsgIDs) == 0 && e.Summary.MsgID == "" {
		return prev
	}
	replaced := make(map[string]bool, len(e.ReplacedMsgIDs))
	for _, id := range e.ReplacedMsgIDs {
		if id != "" {
			replaced[id] = true
		}
	}
	out := make([]Message, 0, len(prev)+1)
	summaryPresent := false
	insertAt := -1
	for _, message := range prev {
		if replaced[message.MsgID] {
			if insertAt < 0 {
				insertAt = len(out)
			}
			continue
		}
		if e.Summary.MsgID != "" && message.MsgID == e.Summary.MsgID {
			summaryPresent = true
		}
		out = append(out, message)
	}
	if e.Summary.MsgID == "" || summaryPresent {
		return out
	}
	summary := e.Summary
	summary.Seq = seq
	summary.LastSeq = seq
	if insertAt < 0 {
		// Nothing to replace (a snapshot that never held the replaced rows):
		// the summary still belongs at the start of the compacted conversation.
		insertAt = 0
	}
	out = append(out, Message{})
	copy(out[insertAt+1:], out[insertAt:])
	out[insertAt] = summary
	return out
}
