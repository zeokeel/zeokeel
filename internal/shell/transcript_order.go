package shell

import (
	"github.com/zeokeel/zeokeel/internal/state"
)

// transcript_order.go turns one session snapshot into the Chat transcript's
// render order. Message.Seq and ToolState.StartedSeq share the snapshot's Seq
// axis (state.Apply numbers every folded event once), so both kinds of row can
// be merged on one timeline: a turn's process block sits at its first call's
// record position, which is before the assistant answer the turn finally
// streams, exactly like the DSH transcript.
//
// Snapshots built by hand may leave Message.Seq 0 ("unknown"); a snapshot with
// no known message position at all keeps the legacy layout (all messages, then
// the session's tool block), and inside a partially known snapshot an unknown
// message is placed just after its predecessor so the list order it was built
// in survives.

// transcriptOrderEnabled reports whether the snapshot carries the Seq axis the
// record-ordered transcript needs. Hand-built fixtures leave every Message.Seq
// at 0; those render with the legacy layout instead of guessing positions.
func transcriptOrderEnabled(snap *state.Snapshot) bool {
	if snap == nil {
		return false
	}
	for i := range snap.Messages {
		if snap.Messages[i].Seq > 0 {
			return true
		}
	}
	return false
}

// transcriptEntry is one row of the transcript body: either one message or a
// process block (the summary line plus its tool cards) inserted at the record
// position of the block's first call.
type transcriptEntry struct {
	msgIndex int               // index into Snapshot.Messages; -1 for a block
	tools    []state.ToolState // the block's calls in record order; nil for a message
}

// turnPlan is one user turn's entries in render order. The messages before the
// first user message form their own plan (the preamble), so a snapshot that
// opens with an assistant message still renders it above everything else.
type turnPlan struct {
	entries []transcriptEntry
}

// positionedMessage is one visible assistant message with its resolved
// position on the snapshot's Seq axis.
type positionedMessage struct {
	index int
	seq   uint64
}

// normalizedMessageSeqs resolves each message's position on the snapshot's Seq
// axis. A message carrying 0 is unknown: it is placed just after its
// predecessor, which keeps the order the snapshot listed it in (sorting 0 to
// the front would move hand-built messages above the real transcript). The
// result is strictly increasing, so a partially known snapshot still has a
// usable timeline for tool placement.
func normalizedMessageSeqs(msgs []state.Message) []uint64 {
	seqs := make([]uint64, len(msgs))
	var last uint64
	for i := range msgs {
		seq := msgs[i].Seq
		if seq <= last {
			seq = last + 1
		}
		seqs[i] = seq
		last = seq
	}
	return seqs
}

// buildTranscriptTurns splits the snapshot into user turns and merges each
// turn's tool calls with its assistant messages by record position:
//
//	user message -> process block -> assistant messages (final answer last)
//
// A turn is the user message plus every message up to the next user message;
// a call joins the turn of the last user message whose Seq is at or before the
// call's StartedSeq (calls recorded before the first user message join the
// preamble). Calls are grouped into blocks: a call starts a new block when an
// assistant message's Seq falls strictly between it and the previous call, so
// intermediate assistant text splits the process the way DSH's process groups
// are cut. The block itself renders at its first call's record position.
//
// The returned plans are in render order; empty plans are dropped.
func buildTranscriptTurns(snap *state.Snapshot) []turnPlan {
	if !transcriptOrderEnabled(snap) {
		return nil
	}
	msgs := snap.Messages
	seqs := normalizedMessageSeqs(msgs)

	// plans[turn+1] is one turn and planMsgs keeps its assistant messages;
	// plans[0]/planMsgs[0] are the preamble (turn -1).
	plans := []*turnPlan{{}}
	planMsgs := [][]positionedMessage{{}}
	userSeqs := make([]uint64, 0, 2)
	turn := -1
	for i := range msgs {
		if msgs[i].Text == "" {
			continue
		}
		if msgs[i].Role == state.RoleUser {
			turn++
			userSeqs = append(userSeqs, seqs[i])
			plans = append(plans, &turnPlan{entries: []transcriptEntry{{msgIndex: i}}})
			planMsgs = append(planMsgs, nil)
			continue
		}
		pi := turn + 1
		planMsgs[pi] = append(planMsgs[pi], positionedMessage{index: i, seq: seqs[i]})
	}

	// Assign each call to its turn, preserving record order.
	blocks := make([][]state.ToolState, len(plans))
	for _, tool := range snapshotToolStates(snap) {
		pi := 0
		for t := len(userSeqs) - 1; t >= 0; t-- {
			if userSeqs[t] <= tool.StartedSeq {
				pi = t + 1
				break
			}
		}
		blocks[pi] = append(blocks[pi], tool)
	}

	for pi := range plans {
		entries := plans[pi].entries
		ms := planMsgs[pi]
		tools := blocks[pi]
		mi, ti := 0, 0
		for ti < len(tools) {
			// Assistant messages recorded before the block's first call.
			for mi < len(ms) && ms[mi].seq < tools[ti].StartedSeq {
				entries = append(entries, transcriptEntry{msgIndex: ms[mi].index})
				mi++
			}
			end := ti + 1
			for end < len(tools) && !messageBetween(ms, mi, tools[end-1].StartedSeq, tools[end].StartedSeq) {
				end++
			}
			entries = append(entries, transcriptEntry{tools: tools[ti:end]})
			ti = end
		}
		// Assistant messages recorded after the last call (the final answer).
		for mi < len(ms) {
			entries = append(entries, transcriptEntry{msgIndex: ms[mi].index})
			mi++
		}
		plans[pi].entries = entries
	}

	out := make([]turnPlan, 0, len(plans))
	for _, plan := range plans {
		if len(plan.entries) > 0 {
			out = append(out, *plan)
		}
	}
	return out
}

// messageBetween reports whether an assistant message's Seq falls strictly
// between two calls' start positions, i.e. whether the message splits the two
// calls into separate process blocks. ms is in record order.
func messageBetween(ms []positionedMessage, from int, lo, hi uint64) bool {
	for i := from; i < len(ms); i++ {
		if ms[i].seq >= hi {
			return false
		}
		if ms[i].seq > lo {
			return true
		}
	}
	return false
}
