package gateway

import (
	"strings"
	"time"
)

// Session delta operations (docs/TRANSPORT.md §6.6). One struct carries every
// op variant instead of an interface tree: the vocabulary is closed and the
// browser dispatches on "op".
const (
	opMsgAdd        = "msg.add"
	opMsgDelta      = "msg.delta"
	opMsgToolUpsert = "msg.tool.upsert"
	opMsgPatch      = "msg.patch"
	opMsgReset      = "msg.reset"
	opSessionPatch  = "session.patch"
)

// sessionOp is one entry of a session.delta's ops array.
type sessionOp struct {
	Op              string           `json:"op"`
	Index           int              `json:"index,omitempty"`
	MessageID       string           `json:"messageId,omitempty"`
	Message         *messageDTO      `json:"message,omitempty"`
	TextAppend      string           `json:"textAppend,omitempty"`
	ReasoningAppend *reasoningAppend `json:"reasoningAppend,omitempty"`
	Tool            *toolCallDTO     `json:"tool,omitempty"`
	Fields          map[string]any   `json:"fields,omitempty"`
	Messages        []messageDTO     `json:"messages,omitempty"`
}

// reasoningAppend is one reasoning segment's text growth.
type reasoningAppend struct {
	Iteration  int    `json:"iteration"`
	TextAppend string `json:"textAppend"`
}

// sessionDelta is the payload of a `session.delta` notification.
type sessionDelta struct {
	SessionID string      `json:"sessionId"`
	BaseRev   uint64      `json:"baseRev"`
	Rev       uint64      `json:"rev"`
	Ops       []sessionOp `json:"ops,omitempty"`
	// Resync asks the client to discard its copy and take a fresh snapshot: the
	// gateway dropped intermediate frames (backpressure) or cannot describe the
	// change as ops (docs/TRANSPORT.md §4.5, §6.5).
	Resync bool `json:"resync,omitempty"`
}

// listDelta is the payload of a `session.list.delta` notification.
type listDelta struct {
	BaseRev uint64           `json:"baseRev"`
	Rev     uint64           `json:"rev"`
	Added   []sessionInfoDTO `json:"added,omitempty"`
	Changed []sessionInfoDTO `json:"changed,omitempty"`
	Removed []string         `json:"removed,omitempty"`
	Resync  bool             `json:"resync,omitempty"`
}

// notification implements deltaPayload.
func (d *sessionDelta) notification() string { return "session.delta" }

// revs implements deltaPayload.
func (d *sessionDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *sessionDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *sessionDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *sessionDelta) retainBytes() int { return opsBytes(d.Ops) + 64 }

// merge implements deltaPayload by folding the newer ops into a copy of this one.
func (d *sessionDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*sessionDelta)
	if !ok {
		return d
	}
	copied := &sessionDelta{
		SessionID: d.SessionID,
		BaseRev:   d.BaseRev,
		Rev:       d.Rev,
		Resync:    d.Resync,
		Ops:       append([]sessionOp(nil), d.Ops...),
	}
	return mergeSessionDelta(copied, other)
}

// notification implements deltaPayload.
func (d *listDelta) notification() string { return "session.list.delta" }

// revs implements deltaPayload.
func (d *listDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *listDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *listDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *listDelta) retainBytes() int {
	return (len(d.Added)+len(d.Changed))*128 + len(d.Removed)*32 + 64
}

// merge implements deltaPayload.
func (d *listDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*listDelta)
	if !ok {
		return d
	}
	copied := &listDelta{
		BaseRev: d.BaseRev,
		Rev:     d.Rev,
		Resync:  d.Resync,
		Added:   d.Added,
		Changed: d.Changed,
		Removed: d.Removed,
	}
	return mergeListDelta(copied, other)
}

// diffSessionList compares two session-list snapshots.
func diffSessionList(previous, next []sessionInfoDTO) listDelta {
	previousByID := make(map[string]sessionInfoDTO, len(previous))
	for _, item := range previous {
		previousByID[item.ID] = item
	}
	nextByID := make(map[string]sessionInfoDTO, len(next))
	var delta listDelta
	for _, item := range next {
		nextByID[item.ID] = item
		old, existed := previousByID[item.ID]
		switch {
		case !existed:
			delta.Added = append(delta.Added, item)
		case !equalSessionInfo(old, item):
			delta.Changed = append(delta.Changed, item)
		}
	}
	for _, item := range previous {
		if _, still := nextByID[item.ID]; !still {
			delta.Removed = append(delta.Removed, item.ID)
		}
	}
	return delta
}

// diffSession compares two conversation snapshots and describes the change.
// It returns nil when nothing a client can see changed.
func diffSession(previous, next sessionDTO) []sessionOp {
	var ops []sessionOp
	// The session-level patch is appended LAST: a delta whose session.patch says
	// running:false is the run's final word, so it must not overtake the message
	// ops of the same delta (the SSE run stream ends on it, §4.6).
	deferFields := sessionFields(previous, next)
	// A regenerate or a compaction rewrites history: the ids at the same index
	// stop matching, and describing that as ops would be a lossy story. Say
	// "here is the new history" instead.
	overlap := len(previous.Messages)
	rewritten := len(next.Messages) < len(previous.Messages)
	if len(next.Messages) < overlap {
		overlap = len(next.Messages)
	}
	for i := 0; i < overlap && !rewritten; i++ {
		if previous.Messages[i].ID != next.Messages[i].ID {
			rewritten = true
		}
	}
	if rewritten {
		ops = append(ops, sessionOp{Op: opMsgReset, Messages: next.Messages})
		return appendSessionFields(ops, deferFields)
	}
	for i := 0; i < overlap; i++ {
		ops = append(ops, diffMessage(previous.Messages[i], next.Messages[i])...)
	}
	for i := overlap; i < len(next.Messages); i++ {
		message := next.Messages[i]
		ops = append(ops, sessionOp{Op: opMsgAdd, Index: i, Message: &message})
	}
	return appendSessionFields(ops, deferFields)
}

// appendSessionFields appends the session-level patch, if any.
func appendSessionFields(ops []sessionOp, fields map[string]any) []sessionOp {
	if len(fields) == 0 {
		return ops
	}
	return append(ops, sessionOp{Op: opSessionPatch, Fields: fields})
}

// sessionFields collects the conversation-level fields that changed.
func sessionFields(previous, next sessionDTO) map[string]any {
	fields := map[string]any{}
	if previous.Title != next.Title {
		fields["title"] = next.Title
	}
	if previous.Model != next.Model {
		fields["model"] = next.Model
	}
	if previous.Running != next.Running {
		fields["running"] = next.Running
	}
	if previous.Error != next.Error {
		fields["error"] = next.Error
	}
	if previous.Interrupted != next.Interrupted {
		fields["interrupted"] = next.Interrupted
	}
	if !equalTimePointer(previous.UpdatedAt, next.UpdatedAt) {
		fields["updatedAt"] = next.UpdatedAt
	}
	return fields
}

// diffMessage describes one message's change: text growth is an append, tool
// changes are keyed by call id, everything else is a patch.
func diffMessage(previous, next messageDTO) []sessionOp {
	var ops []sessionOp
	// Assistant text streams: send the suffix, never the whole reply (§6.6).
	if next.Text != previous.Text {
		switch {
		case strings.HasPrefix(next.Text, previous.Text):
			suffix := next.Text[len(previous.Text):]
			if suffix != "" {
				ops = append(ops, sessionOp{Op: opMsgDelta, MessageID: next.ID, TextAppend: suffix})
			}
		default:
			ops = append(ops, sessionOp{Op: opMsgPatch, MessageID: next.ID,
				Fields: map[string]any{"text": next.Text}})
		}
	}
	ops = append(ops, diffReasoning(previous, next)...)
	ops = append(ops, diffTools(previous, next)...)
	fields := messageFields(previous, next)
	if len(fields) > 0 {
		ops = append(ops, sessionOp{Op: opMsgPatch, MessageID: next.ID, Fields: fields})
	}
	return ops
}

// diffReasoning appends the new thinking text. Segments are keyed by iteration;
// when the segment set is replaced instead of extended, the whole reasoning is
// patched.
func diffReasoning(previous, next messageDTO) []sessionOp {
	oldSegments := previous.ReasoningSegments
	newSegments := next.ReasoningSegments
	if len(newSegments) == 0 && len(oldSegments) == 0 {
		if previous.Reasoning == next.Reasoning {
			return nil
		}
		return []sessionOp{{Op: opMsgPatch, MessageID: next.ID,
			Fields: map[string]any{"reasoning": next.Reasoning}}}
	}
	if len(newSegments) < len(oldSegments) {
		return []sessionOp{{Op: opMsgPatch, MessageID: next.ID,
			Fields: map[string]any{"reasoning": next.Reasoning, "reasoningSegments": newSegments}}}
	}
	var ops []sessionOp
	for i, segment := range newSegments {
		if i >= len(oldSegments) {
			// A new iteration's thinking starts: send it whole.
			if segment.Text != "" {
				ops = append(ops, sessionOp{Op: opMsgDelta, MessageID: next.ID,
					ReasoningAppend: &reasoningAppend{Iteration: segment.Iteration, TextAppend: segment.Text}})
			}
			continue
		}
		old := oldSegments[i]
		if old.Iteration != segment.Iteration || !strings.HasPrefix(segment.Text, old.Text) {
			return []sessionOp{{Op: opMsgPatch, MessageID: next.ID,
				Fields: map[string]any{"reasoning": next.Reasoning, "reasoningSegments": newSegments}}}
		}
		if suffix := segment.Text[len(old.Text):]; suffix != "" {
			ops = append(ops, sessionOp{Op: opMsgDelta, MessageID: next.ID,
				ReasoningAppend: &reasoningAppend{Iteration: segment.Iteration, TextAppend: suffix}})
		}
	}
	return ops
}

// diffTools upserts tool cards that appeared or changed, keyed by call id.
func diffTools(previous, next messageDTO) []sessionOp {
	if len(next.Tools) == 0 && len(previous.Tools) == 0 {
		return nil
	}
	old := make(map[string]toolCallDTO, len(previous.Tools))
	for _, call := range previous.Tools {
		old[call.ID] = call
	}
	var ops []sessionOp
	seen := make(map[string]bool, len(next.Tools))
	for _, call := range next.Tools {
		seen[call.ID] = true
		if prior, existed := old[call.ID]; existed && equalToolCall(prior, call) {
			continue
		}
		updated := call
		ops = append(ops, sessionOp{Op: opMsgToolUpsert, MessageID: next.ID, Tool: &updated})
	}
	if len(seen) != len(previous.Tools) {
		// A tool disappeared: not a streaming shape (regenerate rewrites history
		// whole), so send the card list itself.
		ops = append(ops, sessionOp{Op: opMsgPatch, MessageID: next.ID,
			Fields: map[string]any{"tools": next.Tools}})
	}
	return ops
}

// messageFields collects the non-streaming message fields that changed.
func messageFields(previous, next messageDTO) map[string]any {
	fields := map[string]any{}
	if previous.Streaming != next.Streaming {
		fields["streaming"] = next.Streaming
	}
	if previous.Error != next.Error {
		fields["error"] = next.Error
	}
	if previous.ErrorKind != next.ErrorKind {
		fields["errorKind"] = next.ErrorKind
	}
	if previous.Truncated != next.Truncated {
		fields["truncated"] = next.Truncated
	}
	if previous.ToolTimelineKnown != next.ToolTimelineKnown {
		fields["toolTimelineKnown"] = next.ToolTimelineKnown
	}
	if !equalUsage(previous.Usage, next.Usage) {
		fields["usage"] = next.Usage
	}
	if !equalAttachments(previous.Attachments, next.Attachments) {
		fields["attachments"] = next.Attachments
	}
	if !equalGoalRound(previous.GoalRound, next.GoalRound) {
		fields["goalRound"] = next.GoalRound
	}
	if previous.Role != next.Role {
		fields["role"] = next.Role
	}
	return fields
}

// equalSessionInfo compares two sidebar rows by value.
func equalSessionInfo(a, b sessionInfoDTO) bool {
	return a.ID == b.ID && a.WorkspaceID == b.WorkspaceID && a.Title == b.Title &&
		a.Pinned == b.Pinned && a.Archived == b.Archived && a.MessageCount == b.MessageCount &&
		a.Running == b.Running && a.Waiting == b.Waiting && a.Unread == b.Unread &&
		equalTimePointer(a.UpdatedAt, b.UpdatedAt) && a.Origin == b.Origin &&
		a.PermissionScope == b.PermissionScope && a.EffectiveApprovalMode == b.EffectiveApprovalMode &&
		a.EffectiveSandboxMode == b.EffectiveSandboxMode
}

// equalToolCall compares two tool cards by value.
func equalToolCall(a, b toolCallDTO) bool {
	if a.ID != b.ID || a.Name != b.Name || a.Status != b.Status || a.Summary != b.Summary ||
		a.Arguments != b.Arguments || a.Path != b.Path || a.TextOffset != b.TextOffset ||
		a.ErrorKind != b.ErrorKind || !equalTimePointer(a.StartedAt, b.StartedAt) ||
		!equalTimePointer(a.EndedAt, b.EndedAt) || len(a.Generated) != len(b.Generated) {
		return false
	}
	for i := range a.Generated {
		if a.Generated[i] != b.Generated[i] {
			return false
		}
	}
	return true
}

// equalTimePointer compares two optional clocks by value.
func equalTimePointer(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// equalUsage compares two optional usage blocks.
func equalUsage(a, b *messageUsageDTO) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// equalAttachments compares two attachment lists.
func equalAttachments(a, b []attachmentDTO) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalGoalRound compares two optional goal markers.
func equalGoalRound(a, b *goalRoundDTO) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// mergeSessionDelta folds next into pending so a slow client sees one frame
// per domain with every op in order (§4.5). Once the merged frame grows past
// the op/byte budget it degrades to a resync marker.
func mergeSessionDelta(pending, next *sessionDelta) *sessionDelta {
	if pending.Rev >= next.Rev {
		return pending
	}
	pending.Ops = append(pending.Ops, next.Ops...)
	pending.Rev = next.Rev
	pending.Resync = pending.Resync || next.Resync
	if !pending.Resync && (len(pending.Ops) > DeltaRingEntries || opsBytes(pending.Ops) > DeltaRingBytes) {
		pending.Resync = true
		pending.Ops = nil
	}
	if pending.Resync {
		pending.Ops = nil
	}
	return pending
}

// mergeListDelta folds next into pending; the session list is small, so the
// merge keeps the item sets rather than the ops. Windows are applied in order
// (pending, then next): an id the newest window removed disappears, an id the
// newest window (re-)added is an add, and "added earlier, changed later" stays
// an add carrying the newest value — the client never had it.
func mergeListDelta(pending, next *listDelta) *listDelta {
	if pending.Rev >= next.Rev {
		return pending
	}
	type listItem struct {
		item    sessionInfoDTO
		added   bool
		changed bool
		removed bool
	}
	state := make(map[string]*listItem, len(pending.Added)+len(pending.Changed)+len(next.Added)+len(next.Changed))
	var order []string
	remember := func(items []sessionInfoDTO, added bool) {
		for _, entry := range items {
			existing := state[entry.ID]
			if existing == nil {
				existing = &listItem{}
				state[entry.ID] = existing
				order = append(order, entry.ID)
			}
			existing.item = entry
			existing.removed = false
			if added {
				// Sticky: an add in an older window keeps the client's copy
				// authoritative as an add.
				existing.added = true
			} else if !existing.added {
				existing.changed = true
			}
		}
	}
	remove := func(ids []string) {
		for _, id := range ids {
			existing := state[id]
			if existing == nil {
				existing = &listItem{}
				state[id] = existing
				order = append(order, id)
			}
			existing.removed = true
		}
	}
	remember(pending.Added, true)
	remember(pending.Changed, false)
	remove(pending.Removed)
	remember(next.Added, true)
	remember(next.Changed, false)
	remove(next.Removed)

	out := &listDelta{BaseRev: pending.BaseRev, Rev: next.Rev, Resync: pending.Resync || next.Resync}
	for _, id := range order {
		entry := state[id]
		switch {
		case entry.removed:
			out.Removed = append(out.Removed, id)
		case entry.added:
			out.Added = append(out.Added, entry.item)
		case entry.changed:
			out.Changed = append(out.Changed, entry.item)
		}
	}
	return out
}

// containsString reports whether list contains value.
func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// opsBytes is the approximate serialized size of an op list, used to bound a
// coalesced frame.
func opsBytes(ops []sessionOp) int {
	total := 0
	for _, op := range ops {
		total += len(op.MessageID) + len(op.TextAppend) + len(op.Messages)*64
		if op.Tool != nil {
			total += len(op.Tool.Arguments) + len(op.Tool.Summary) + 64
		}
		if op.ReasoningAppend != nil {
			total += len(op.ReasoningAppend.TextAppend) + 32
		}
		if op.Message != nil {
			total += len(op.Message.Text) + len(op.Message.Reasoning) + 64
		}
		total += 32
	}
	return total
}
