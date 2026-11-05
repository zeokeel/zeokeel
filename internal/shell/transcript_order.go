package shell

import (
	"sort"

	"github.com/zeokeel/zeokeel/internal/state"
)

// transcript_order.go turns one session snapshot into the Chat transcript's
// render order. Message.Seq/LastSeq and ToolState.StartedSeq share the
// snapshot's Seq axis (state.Apply numbers every folded event once), so both
// kinds of row can be merged on one timeline: a turn's process block sits at
// its first call's record position, above the assistant answer whose visible
// text only finishes after the calls, exactly like the DSH transcript.
//
// A turn publishes a single assistant message, and its first event is often
// the Reasoning that precedes the turn's tool calls. Message.Seq therefore
// marks where the message was created (possibly before every call) while
// Message.LastSeq marks where its visible text ends. The transcript places the
// message by LastSeq, so pre-tool reasoning cannot drag the final answer above
// the process block; a streamed token after a call keeps the block above it.
//
// Snapshots built by hand may leave Message.Seq/LastSeq 0 ("unknown"); a
// snapshot with no known message position at all keeps the legacy layout (all
// messages, then the session's tool block), and inside a partially known
// snapshot an unknown position is placed just after its predecessor so the
// list order it was built in survives.

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

// positionedMessage is one visible assistant message with the interval it
// occupies on the snapshot's Seq axis: seq is the first event that named it
// (often the pre-tool reasoning) and end is where its last visible token ended
// (LastSeq, falling back to seq while it has no visible text).
type positionedMessage struct {
	index int
	seq   uint64
	end   uint64
}

// normalizedMessagePositions resolves each message's Seq and visible-text end
// on the snapshot's Seq axis. A position carrying 0 is unknown: it is placed
// just after its predecessor, which keeps the order the snapshot listed it in
// (sorting 0 to the front would move hand-built messages above the real
// transcript). Both results are strictly increasing, so a partially known
// snapshot still has a usable timeline for tool placement.
func normalizedMessagePositions(msgs []state.Message) (seqs, ends []uint64) {
	seqs = make([]uint64, len(msgs))
	ends = make([]uint64, len(msgs))
	var lastSeq, lastEnd uint64
	for i := range msgs {
		seq := msgs[i].Seq
		if seq <= lastSeq {
			seq = lastSeq + 1
		}
		end := msgs[i].LastSeq
		if end < seq {
			end = seq
		}
		if end <= lastEnd {
			end = lastEnd + 1
		}
		seqs[i] = seq
		ends[i] = end
		lastSeq, lastEnd = seq, end
	}
	return seqs, ends
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
// assistant message first appears between it and the previous call, so
// intermediate step text splits the process the way DSH's process groups are
// cut. A message is placed at its visible-text end (LastSeq): a block whose
// first call started before that end renders above the message.
//
// The returned plans are in render order; empty plans are dropped.
func buildTranscriptTurns(snap *state.Snapshot) []turnPlan {
	if !transcriptOrderEnabled(snap) {
		return nil
	}
	msgs := snap.Messages
	seqs, ends := normalizedMessagePositions(msgs)

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
		planMsgs[pi] = append(planMsgs[pi], positionedMessage{
			index: i, seq: seqs[i], end: ends[i],
		})
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

		// Split the turn's calls into process blocks: a call opens a new block
		// when an assistant message first appears between it and the previous
		// call. One turn's message usually appears before all its calls (the
		// pre-tool reasoning), so they stay one block.
		toolBlocks := make([][]state.ToolState, 0, 2)
		for ti := 0; ti < len(tools); {
			end := ti + 1
			for end < len(tools) && !messageStartsBetween(ms, tools[end-1].StartedSeq, tools[end].StartedSeq) {
				end++
			}
			toolBlocks = append(toolBlocks, tools[ti:end])
			ti = end
		}

		// Merge blocks and messages by record position. A message is placed at
		// its visible-text end: a block whose first call started before that end
		// renders above the message, so a turn that reasons, runs tools and then
		// answers reads user -> process -> answer. A message that ended before
		// the block's first call stays above it.
		sort.SliceStable(ms, func(a, b int) bool { return ms[a].end < ms[b].end })
		mi, bi := 0, 0
		for bi < len(toolBlocks) || mi < len(ms) {
			switch {
			case bi >= len(toolBlocks):
				entries = append(entries, transcriptEntry{msgIndex: ms[mi].index})
				mi++
			case mi >= len(ms):
				entries = append(entries, transcriptEntry{tools: toolBlocks[bi]})
				bi++
			case ms[mi].end < toolBlocks[bi][0].StartedSeq:
				entries = append(entries, transcriptEntry{msgIndex: ms[mi].index})
				mi++
			default:
				entries = append(entries, transcriptEntry{tools: toolBlocks[bi]})
				bi++
			}
		}
		plans[pi].entries = mergeAdjacentBlocks(entries)
	}

	out := make([]turnPlan, 0, len(plans))
	for _, plan := range plans {
		if len(plan.entries) > 0 {
			out = append(out, *plan)
		}
	}
	return out
}

// messageStartsBetween reports whether an assistant message first appeared
// strictly between two calls' start positions, i.e. whether the message splits
// the two calls into separate process blocks. ms is in record order.
func messageStartsBetween(ms []positionedMessage, lo, hi uint64) bool {
	for i := range ms {
		if ms[i].seq >= hi {
			return false
		}
		if ms[i].seq > lo {
			return true
		}
	}
	return false
}

// mergeAdjacentBlocks folds two process blocks that ended up next to each
// other into one: a message that started between two calls but rendered below
// both (its visible text continued past the later call) should not leave the
// user with two summary lines in a row for one uninterrupted run of calls.
func mergeAdjacentBlocks(entries []transcriptEntry) []transcriptEntry {
	out := entries[:0]
	for _, entry := range entries {
		last := len(out) - 1
		if entry.tools != nil && last >= 0 && out[last].tools != nil {
			combined := make([]state.ToolState, 0, len(out[last].tools)+len(entry.tools))
			combined = append(combined, out[last].tools...)
			combined = append(combined, entry.tools...)
			out[last].tools = combined
			continue
		}
		out = append(out, entry)
	}
	return out
}
