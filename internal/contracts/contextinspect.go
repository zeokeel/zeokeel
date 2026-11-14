package contracts

// Context observability contract (DSH dsh-context P0 data foundation).
//
// The agent backend owns the durable session event log; this contract exposes a
// folding view of it to UI modules without making them import Rig or the agent
// implementation. The backend records, per session:
//
//   - `context_snapshot`: the request-level snapshot of one run (model, window,
//     system-prompt size, tool catalog, history counts, injection sources);
//   - `model_call`: one event per provider call (iteration/tokens/cached/context);
//   - `compact`: manual compaction results (and inferred automatic compaction);
//   - `spill`: tool results replaced by an on-disk spill summary.
//
// SessionContext returns the folded view (snapshot + recent requests + recent
// audit events + used/window anchors); a session with no recorded data reports
// ok=false so a UI can show an empty state instead of inventing numbers.

import (
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ContextCategory classifies one part of the model context. It is reserved for
// the per-section breakdown (a follow-up task fills Snapshot.Sections).
type ContextCategory string

const (
	// ContextCategorySystem is the system prompt (identity/environment/policy).
	ContextCategorySystem ContextCategory = "system"
	// ContextCategoryTools is the tool catalog (schemas sent with each request).
	ContextCategoryTools ContextCategory = "tools"
	// ContextCategoryUser is the user message history.
	ContextCategoryUser ContextCategory = "user"
	// ContextCategoryAssistant is the assistant message history.
	ContextCategoryAssistant ContextCategory = "assistant"
	// ContextCategoryTool is the tool-result history.
	ContextCategoryTool ContextCategory = "tool"
	// ContextCategoryInject is injected context (AGENTS.md, memory, preset).
	ContextCategoryInject ContextCategory = "inject"
	// ContextCategorySkill is injected skill material.
	ContextCategorySkill ContextCategory = "skill"
)

// PromptSection is one labelled piece of the system prompt as it is assembled
// for a run (identity/environment/approval/config/AGENTS.md/preset/memory/
// skills/tool guidance). The rendered prompt is the concatenation of these
// sections, so their Byte/Tokens sums describe the prompt size without loading
// the text again. UI modules only need SessionContext.Snapshot.Sections; this
// type exists so the backend can share one section model across the prompt and
// the snapshot.
type PromptSection struct {
	// ID is the stable section identifier.
	ID string
	// Source names where the section came from: builtin/config/workspace/
	// preset/memory/skills/tools/policy.
	Source string
	// Text is the rendered section text.
	Text string
	// Bytes is len(Text).
	Bytes int
	// Tokens is the estimated token count of Text.
	Tokens int
}

// ContextSection is one labelled part of the request context. P0 records the
// section-ized system prompt here (the Text is not duplicated into the durable
// event; Bytes/Tokens/Hash describe it).
type ContextSection struct {
	// ID is a stable section identifier.
	ID string
	// Label is the human-readable section name.
	Label string
	// Source names where the section came from (system/tool/history/inject).
	Source string
	// Bytes is the rendered section size in bytes.
	Bytes int
	// Tokens is the estimated token count.
	Tokens int
	// Hash is a stable content hash for diffing adjacent snapshots.
	Hash string
}

// ContextTool is one registered tool in the request catalog.
type ContextTool struct {
	// Name is the model-visible tool name.
	Name string
	// Group is the tool family (file/shell/web/…); empty when unclassified.
	Group string
	// Tokens is the estimated schema+description token cost.
	Tokens int
}

// ContextHistory is the message-count breakdown of the request history.
type ContextHistory struct {
	// Messages is the total message count.
	Messages int
	// User, Assistant and Tool count messages by role (Tool counts tool calls
	// carried by assistant messages).
	User      int
	Assistant int
	Tool      int
}

// ContextInjected records which optional prompt injections were present.
type ContextInjected struct {
	// AgentsMD reports the workspace instruction file (AGENTS.md) injection.
	AgentsMD bool
	// Preset reports an agent-preset instruction injection.
	Preset bool
	// Memory reports the bounded memory summary injection.
	Memory bool
	// Skills reports the bounded skill catalog injection.
	Skills bool
}

// ContextSnapshot is the request-level snapshot recorded before one run's first
// provider call.
type ContextSnapshot struct {
	// At is when the snapshot was recorded.
	At time.Time
	// Seq is the durable event sequence of the snapshot event.
	Seq int64
	// Model is the model the run uses.
	Model string
	// Window is the model's context window in tokens (0 = unknown).
	Window int
	// SystemPromptBytes is the rendered system prompt size in bytes.
	SystemPromptBytes int
	// SystemPromptTokens is the estimated system prompt token count.
	SystemPromptTokens int
	// Sections is the reserved per-section breakdown (empty in P0).
	Sections []ContextSection
	// Tools is the tool catalog sent with each request.
	Tools []ContextTool
	// History is the message-count breakdown at run start.
	History ContextHistory
	// Injected records which optional prompt injections were present.
	Injected ContextInjected
	// EstimatedPrompt is the estimated total prompt tokens before the first
	// provider call (system prompt + tool catalog + history estimate).
	EstimatedPrompt int
}

// ContextRequest is one provider call folded from the durable `model_call`
// events.
type ContextRequest struct {
	// Seq is the durable event sequence of the call.
	Seq int64
	// Iteration is the model/tool round of the run (1-based).
	Iteration int
	// At is when the provider reported the usage.
	At time.Time
	// Model is the model of this call.
	Model string
	// Prompt and Completion are the provider-reported token counts.
	Prompt     int
	Completion int
	// Cached is the provider-reported cached prompt-token count (a subset of
	// Prompt).
	Cached int
	// Total is the provider-reported total.
	Total int
	// ContextTokens is the prompt-token count of this call: the real context the
	// model saw (the UI's per-request context anchor).
	ContextTokens int
	// CostUSD is the estimated cost of this call when the model has pricing
	// configured (CostKnown reports it).
	CostUSD   float64
	CostKnown bool
}

// ContextEventKind classifies an audit entry of the context timeline.
type ContextEventKind string

const (
	// ContextEventCompact is a manual compaction result.
	ContextEventCompact ContextEventKind = "compact"
	// ContextEventSpill is a tool result spilled to disk.
	ContextEventSpill ContextEventKind = "spill"
	// ContextEventRecovery is a context-overflow recovery: an automatic
	// compaction followed by one retry of the same turn.
	ContextEventRecovery ContextEventKind = "recovery"
	// ContextEventArchive is the durable archive of the raw history a compaction
	// replaced (metadata only: the archived messages live in the durable
	// compact_archive event, not in this read model).
	ContextEventArchive ContextEventKind = "archive"
)

// ContextEvent is one context audit entry (compaction or spill).
type ContextEvent struct {
	// Seq is the durable event sequence.
	Seq int64
	// Kind is the event class.
	Kind ContextEventKind
	// At is when it happened.
	At time.Time
	// Detail is a short human-readable description.
	Detail string
	// Before and After are the context token counts around a compaction (0 when
	// not applicable).
	Before int
	After  int
	// Replaced and Saved are the message counts of a compaction (0 otherwise).
	Replaced int
	Saved    int
	// Path and Bytes describe a spilled tool result (empty/0 otherwise).
	Path  string
	Bytes int
	// Inferred marks a heuristic (automatic compaction is not directly observable
	// through Rig, so a prompt-token drop is reported as inferred, never exact).
	Inferred bool
	// ArchivedMessages is the number of raw history messages preserved by a
	// compact_archive event, and ArchivedBytes their serialized size.
	ArchivedMessages int
	ArchivedBytes    int
	// Truncated marks an archive that hit its per-event bound; the archived
	// count/bytes then describe what was kept, not the whole replaced history.
	Truncated bool
	// Outcome explains an automatic compaction attempt that did not yield a
	// usable reduction: "failed", "no-reduction" or "archive-failed". Empty for
	// a successful compaction and for manually requested ones.
	Outcome string
}

// SessionContext is the folded context view of one session.
type SessionContext struct {
	// Revision increments on every folded event so a UI can skip unchanged reads.
	Revision uint64
	// Snapshot is the most recent request-level snapshot (zero when no run has
	// been recorded yet).
	Snapshot ContextSnapshot
	// Requests are the recent provider calls, oldest first.
	Requests []ContextRequest
	// Events are the recent context audit entries, oldest first.
	Events []ContextEvent
	// Used is the current context size in tokens: the last provider-reported
	// context token count, or the snapshot's estimate when no call was recorded.
	Used int
	// Window is the model's context window in tokens (0 = unknown).
	Window int
	// TotalCostUSD is the sum of the folded request costs when pricing is
	// configured (CostKnown reports it).
	TotalCostUSD float64
	CostKnown    bool
}

// ContextInspector exposes the folded context timeline of a session. It is
// provided by the agent backend module as contracts.KeyContext.
type ContextInspector interface {
	// SessionContext returns the session's folded context view; ok is false when
	// the session has no recorded context data (cold/never run).
	SessionContext(sessionID string) (SessionContext, bool)
}

// KeyContext is provided by the agent module and consumed by the context UI.
var KeyContext = cordis.NewServiceKey[ContextInspector]("zeokeel/context")
