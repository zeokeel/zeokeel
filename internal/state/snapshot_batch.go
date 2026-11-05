package state

import (
	"strings"
)

// maxSnapshotErrors is the bounded error history Apply keeps; ApplyBatch uses
// the same bound so both folds produce identical snapshots.
const maxSnapshotErrors = 100

// ApplyBatch folds events (in arrival order) into prev and returns the
// resulting snapshot. The result is always equal to folding the same
// non-nil events one at a time with Apply:
//
//	snap := prev
//	for _, ev := range events {
//		snap = Apply(snap, ev)
//	}
//
// The point of the batch fold is allocation shape, not different semantics:
// one messages slice copy, one tools map copy and one turn-marks map copy per
// batch, while token text accumulates in a chunk buffer and is concatenated
// once per touched message. Projector uses it so a burst of token events costs
// one snapshot copy instead of one per token (AR-22).
//
// nil events are ignored exactly like Apply ignores them (they do not advance
// Seq). Empty events return prev unchanged (or a fresh snapshot when prev is
// nil).
func ApplyBatch(prev *Snapshot, events []Event) *Snapshot {
	nonNil := countNonNil(events)
	if nonNil == 0 {
		if prev != nil {
			return prev
		}
		return NewSnapshot()
	}
	if nonNil == 1 {
		for _, ev := range events {
			if ev != nil {
				return Apply(prev, ev)
			}
		}
	}
	if prev == nil {
		prev = NewSnapshot()
	}

	next := *prev
	baseSeq := prev.Seq
	next.Seq = baseSeq + uint64(nonNil)

	// Lazily allocated working state: a collection is copied only when the
	// batch actually touches it.
	var (
		outMsgs   []Message
		msgIndex  map[string]int
		accs      map[string]*messageAccumulator
		tools     map[string]ToolState
		turnMarks map[string]StatusReason
	)
	msgsChanged := false
	toolsChanged := false
	marksChanged := false
	var newErrors []ErrorInfo
	var lastError *ErrorInfo
	usage := prev.Usage
	sessionID := prev.SessionID
	workspaceTurn := prev.WorkspaceChangesTurn
	workspaceChanges := prev.WorkspaceChanges
	workspaceChanged := false

	ensureMsgs := func() {
		if outMsgs != nil {
			return
		}
		outMsgs = make([]Message, len(prev.Messages))
		copy(outMsgs, prev.Messages)
		msgIndex = make(map[string]int, len(prev.Messages))
		for i := range outMsgs {
			msgIndex[outMsgs[i].MsgID] = i
		}
		accs = make(map[string]*messageAccumulator)
	}
	ensureTools := func() {
		if tools != nil {
			return
		}
		tools = make(map[string]ToolState, len(prev.Tools)+1)
		for id, st := range prev.Tools {
			tools[id] = st
		}
	}
	ensureTurnMarks := func() {
		if turnMarks != nil {
			return
		}
		turnMarks = make(map[string]StatusReason, len(prev.TurnMarks)+1)
		for id, reason := range prev.TurnMarks {
			turnMarks[id] = reason
		}
	}
	// messageAccumulator returns the per-message chunk buffer, creating the
	// message (at seq) when the batch is the first to name it, mirroring
	// Apply's append-at-end creation order.
	messageAccumulator := func(msgID string, seq uint64) *messageAccumulator {
		ensureMsgs()
		idx, ok := msgIndex[msgID]
		if !ok {
			outMsgs = append(outMsgs, Message{MsgID: msgID, Seq: seq, Role: RoleAssistant})
			idx = len(outMsgs) - 1
			msgIndex[msgID] = idx
		}
		acc := accs[msgID]
		if acc == nil {
			acc = &messageAccumulator{idx: idx}
			accs[msgID] = acc
		}
		return acc
	}

	seq := baseSeq
	for _, ev := range events {
		if ev == nil {
			continue
		}
		seq++
		switch e := ev.(type) {
		case Token:
			if e.SessionID != "" {
				sessionID = e.SessionID
			}
			msgsChanged = true
			acc := messageAccumulator(e.MsgID, seq)
			acc.text.WriteString(e.Text)
			acc.lastSeq = seq
			acc.hasText = true
		case Reasoning:
			if e.SessionID != "" {
				sessionID = e.SessionID
			}
			msgsChanged = true
			acc := messageAccumulator(e.MsgID, seq)
			acc.reasoning.WriteString(e.Text)
		case ToolStart:
			ensureTools()
			st := tools[e.CallID]
			st.CallID = e.CallID
			st.Name = e.Name
			st.Args = cloneJSON(e.Args)
			st.Status = ToolRunning
			st.StartedSeq = seq
			st.UpdatedSeq = seq
			tools[e.CallID] = st
			toolsChanged = true
		case ToolEnd:
			ensureTools()
			st := tools[e.CallID]
			st.CallID = e.CallID
			st.Result = e.Result
			st.IsError = e.IsError
			st.Duration = e.Duration
			switch {
			case e.Cancelled:
				st.Status = ToolCancelled
			case e.IsError:
				st.Status = ToolError
			default:
				st.Status = ToolDone
			}
			st.UpdatedSeq = seq
			tools[e.CallID] = st
			toolsChanged = true
		case ApprovalRequest:
			ensureTools()
			st := tools[e.CallID]
			st.CallID = e.CallID
			if e.Name != "" {
				st.Name = e.Name
			}
			if len(e.Args) > 0 {
				st.Args = cloneJSON(e.Args)
			}
			st.ReviewReason = e.ReviewReason
			st.Status = ToolAwaitingApproval
			st.UpdatedSeq = seq
			tools[e.CallID] = st
			toolsChanged = true
		case Usage:
			usage.Turns += e.Turns
			usage.Steps += e.Steps
			usage.In += e.In
			usage.Out += e.Out
			usage.CacheHit += e.CacheHit
			if e.ContextPct > 0 {
				usage.ContextPct = e.ContextPct
			}
			if e.Advisory != "" {
				usage.Advisory = e.Advisory
			}
		case Status:
			next.Phase = e.Phase
			next.TurnReason = e.Reason
		case TurnEnd:
			if e.UserMsgID != "" && e.Reason != ReasonNone {
				ensureTurnMarks()
				turnMarks[e.UserMsgID] = e.Reason
				marksChanged = true
			}
		case Error:
			info := ErrorInfo{MsgID: e.MsgID, Text: errorText(e.Err), Seq: seq}
			newErrors = append(newErrors, info)
			lastError = &info
		case WorkspaceChanges:
			if e.TurnID != "" {
				workspaceTurn = e.TurnID
			}
			if len(e.Changes) > 0 {
				workspaceChanges = append([]WorkspaceChange(nil), e.Changes...)
				workspaceChanged = true
			}
		default:
			// Closed event set: an unknown implementation only advances Seq,
			// matching Apply.
		}
	}

	if msgsChanged {
		for _, acc := range accs {
			msg := &outMsgs[acc.idx]
			if acc.text.Len() > 0 {
				msg.Text += acc.text.String()
			}
			if acc.reasoning.Len() > 0 {
				msg.Reasoning += acc.reasoning.String()
			}
			if acc.hasText {
				msg.LastSeq = acc.lastSeq
			}
		}
		next.Messages = outMsgs
	}
	if toolsChanged {
		next.Tools = tools
	}
	if marksChanged {
		next.TurnMarks = turnMarks
	}
	if len(newErrors) > 0 {
		errors := make([]ErrorInfo, len(prev.Errors), len(prev.Errors)+len(newErrors))
		copy(errors, prev.Errors)
		errors = append(errors, newErrors...)
		if len(errors) > maxSnapshotErrors {
			errors = errors[len(errors)-maxSnapshotErrors:]
		}
		next.Errors = errors
		next.LastError = lastError
	}
	if workspaceChanged {
		next.WorkspaceChanges = workspaceChanges
	}
	next.WorkspaceChangesTurn = workspaceTurn
	next.Usage = usage
	next.SessionID = sessionID
	return &next
}

// messageAccumulator buffers one message's text/reasoning chunks for the
// duration of a batch so token deltas append amortized O(1) instead of
// re-copying the whole accumulated string per token.
type messageAccumulator struct {
	idx       int
	text      strings.Builder
	reasoning strings.Builder
	lastSeq   uint64
	hasText   bool
}

// countNonNil counts the non-nil events.
func countNonNil(events []Event) int {
	n := 0
	for _, ev := range events {
		if ev != nil {
			n++
		}
	}
	return n
}
