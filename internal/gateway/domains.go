package gateway

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The small read-model domains (docs/TRANSPORT.md §5.1): approvals, ask, plan,
// artifacts, settings and the workspace list.
//
// They ride the same hub machinery as the session domains — epoch, per-domain
// rev, cached snapshot, replay ring, per-client coalescing — so there is exactly
// one versioning story. What differs per domain is only two pure functions: how
// to build the snapshot from the backend, and how to describe the change between
// two snapshots as a delta.
//
// Shapes follow §5.1/§6.3:
//
//	approvals.delta   {baseRev, rev, added[], removed[]}     (§6.3 verbatim)
//	artifacts.delta   {baseRev, rev, added[], removed[]}     (append-only list)
//	ask.delta         {baseRev, rev, pending|null}           (one small object)
//	plan.delta        {baseRev, rev, state, content}         (one small object)
//	settings.delta    {baseRev, rev, rows{}, removed[]}      (changed rows only)
//	workspace.list.delta {baseRev, rev, added[], changed[], removed[], currentId}

// Domain keys.
const (
	domainApprovals = "approvals"
	domainAsk       = "ask"
	domainPlan      = "plan"
	domainArtifacts = "artifacts"
	domainSettings  = "settings"
	domainWorkspace = "workspace.list"
	domainTasks     = "tasks"
	domainContext   = "context"
	// domainPromptsPrefix names one conversation's prompt inbox
	// ("prompts:<sessionID>"): queued prompts belong to a session, so the domain
	// is per-session, not global.
	domainPromptsPrefix = "prompts:"
)

// domainContextPrefix names one conversation's folded context timeline
// ("context:<sessionID>") — the browser's Context tab reads it.
const domainContextPrefix = "context:"

// contextKey is the domain key of one conversation's context timeline.
func contextKey(sessionID string) string { return domainContextPrefix + sessionID }

// contextSessionID reports whether key addresses a context timeline and returns
// its conversation.
func contextSessionID(key string) (string, bool) {
	if strings.HasPrefix(key, domainContextPrefix) {
		return strings.TrimPrefix(key, domainContextPrefix), true
	}
	return "", false
}

// domainTasksPrefix names one conversation's task board ("tasks:<sessionID>"):
// the board is process-wide, but a backend that scopes it by session
// (contracts.SessionTaskBoard) gives each conversation its own view.
const domainTasksPrefix = "tasks:"

// tasksKey is the domain key of one conversation's task board.
func tasksKey(sessionID string) string { return domainTasksPrefix + sessionID }

// tasksSessionID reports whether key addresses a per-session task board and
// returns its conversation.
func tasksSessionID(key string) (string, bool) {
	if strings.HasPrefix(key, domainTasksPrefix) {
		return strings.TrimPrefix(key, domainTasksPrefix), true
	}
	return "", false
}

// promptsKey is the domain key of one conversation's prompt inbox.
func promptsKey(sessionID string) string { return domainPromptsPrefix + sessionID }

// promptsSessionID reports whether key addresses a prompt inbox and returns its
// conversation.
func promptsSessionID(key string) (string, bool) {
	if strings.HasPrefix(key, domainPromptsPrefix) {
		return strings.TrimPrefix(key, domainPromptsPrefix), true
	}
	return "", false
}

// deltaPayload is one domain delta: the union of everything the hub needs to
// version it, replay it and coalesce it for a slow client.
type deltaPayload interface {
	// notification is the wire method of the delta.
	notification() string
	// revs returns the delta's base and new revision.
	revs() (base uint64, rev uint64)
	// setRevs stamps both revisions.
	setRevs(base, rev uint64)
	// isResync reports a "discard your copy and snapshot" marker.
	isResync() bool
	// merge folds a newer delta of the same type into this one. Merging may
	// degrade the result to a resync marker when it outgrows its budget.
	merge(next deltaPayload) deltaPayload
	// retainBytes is the approximate retained size.
	retainBytes() int
}

// --- approvals -------------------------------------------------------------

// approvalDTO is one pending approval (the web approval card's input).
type approvalDTO struct {
	ID        string     `json:"id"`
	Tool      string     `json:"tool"`
	Summary   string     `json:"summary,omitempty"`
	Arguments string     `json:"arguments,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	// ExpiresAt is derived, never guessed: automation-origin requests expire
	// after the configured approval timeout, interactive ones never do
	// (contracts.SettingAgentApprovalTimeoutSeconds §RG-04).
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	// SessionID and Source tell the card which conversation asked and whether
	// it is a background request (contracts.ApprovalRequest :106-113).
	SessionID string `json:"sessionId,omitempty"`
	Source    string `json:"source,omitempty"`
}

// domainID implements identified.
func (a approvalDTO) domainID() string { return a.ID }

// approvalsSnapshot is the domain's cached snapshot.
type approvalsSnapshot struct {
	Mode string `json:"mode"`
	// Permission is the normalized user-facing preset of Mode (three presets;
	// "ask"/"never" are retired headless spellings that normalize to read-only,
	// contracts.NormalizePermissionMode).
	Permission             string        `json:"permission"`
	ApprovalTimeoutSeconds int           `json:"approvalTimeoutSeconds"`
	Pending                []approvalDTO `json:"pending"`
}

// --- ask -------------------------------------------------------------------

// askDTO is the pending question (empty when none is waiting).
type askDTO struct {
	ID        string     `json:"id"`
	Text      string     `json:"text"`
	Options   []string   `json:"options,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// askSnapshot is the domain's cached snapshot.
type askSnapshot struct {
	Pending *askDTO `json:"pending"`
}

// --- plan ------------------------------------------------------------------

// planSnapshot is the domain's cached snapshot (the review state machine).
type planSnapshot struct {
	State   string `json:"state"`
	Content string `json:"content"`
}

// --- artifacts -------------------------------------------------------------

// artifactDTO is one presented artifact.
type artifactDTO struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// domainID implements identified.
func (a artifactDTO) domainID() string { return a.ID }

// artifactsSnapshot is the domain's cached snapshot (newest first).
type artifactsSnapshot struct {
	Items []artifactDTO `json:"items"`
}

// --- settings --------------------------------------------------------------

// settingsSnapshot is the domain's cached snapshot.
type settingsSnapshot struct {
	Rows map[string]string `json:"rows"`
}

// --- workspace -------------------------------------------------------------

// workspaceDTO is one registered project root.
type workspaceDTO struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Root      string     `json:"root"`
	Color     string     `json:"color,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// domainID implements identified.
func (w workspaceDTO) domainID() string { return w.ID }

// workspacesSnapshot is the domain's cached snapshot.
type workspacesSnapshot struct {
	Workspaces []workspaceDTO `json:"workspaces"`
	CurrentID  string         `json:"currentId,omitempty"`
}

// --- prompt queue ----------------------------------------------------------

// queuedPromptDTO is one prompt waiting in a session's inbox (contracts.QueuedPrompt).
type queuedPromptDTO struct {
	ID        string     `json:"id"`
	Text      string     `json:"text"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// domainID implements identified.
func (q queuedPromptDTO) domainID() string { return q.ID }

// promptsSnapshot is one conversation's inbox, oldest first.
type promptsSnapshot struct {
	SessionID string            `json:"sessionId"`
	Prompts   []queuedPromptDTO `json:"prompts"`
}

// --- task board ------------------------------------------------------------

// taskDTO is one shared task-board item (contracts.BoardTask). The board is the
// process-wide team task board the todo tools mutate; the browser shows it as
// the background-task chip (a session-scoped view when the backend implements
// contracts.SessionTaskBoard).
type taskDTO struct {
	ID          string     `json:"id"`
	Subject     string     `json:"subject"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status"`
	Owner       string     `json:"owner,omitempty"`
	BlockedBy   []string   `json:"blockedBy,omitempty"`
	WriteScopes []string   `json:"writeScopes,omitempty"`
	Created     *time.Time `json:"createdAt,omitempty"`
	Updated     *time.Time `json:"updatedAt,omitempty"`
}

// domainID implements identified.
func (t taskDTO) domainID() string { return t.ID }

// tasksSnapshot is the domain's cached snapshot. SessionID is "" for the
// process-wide board.
type tasksSnapshot struct {
	SessionID string    `json:"sessionId,omitempty"`
	Tasks     []taskDTO `json:"tasks"`
}

// --- context timeline ------------------------------------------------------

// The context domain is the browser's Context tab: the folded view of what the
// model actually saw (contracts.SessionContext, contextinspect.go:246). The
// backend recomputes it from the durable event log on every read and carries its
// own Revision for "unchanged" detection, so the delta is a whole replacement:
// there is no per-item change log to diff against, and the lists are already
// bounded (200 requests + 200 audit events).

// contextSectionDTO is one labelled part of the request context.
type contextSectionDTO struct {
	ID     string `json:"id"`
	Label  string `json:"label,omitempty"`
	Source string `json:"source,omitempty"`
	Bytes  int    `json:"bytes"`
	Tokens int    `json:"tokens"`
	Hash   string `json:"hash,omitempty"`
}

// contextToolDTO is one tool in the request catalog.
type contextToolDTO struct {
	Name   string `json:"name"`
	Group  string `json:"group,omitempty"`
	Tokens int    `json:"tokens"`
}

// contextHistoryDTO is the message-count breakdown at run start.
type contextHistoryDTO struct {
	Messages  int `json:"messages"`
	User      int `json:"user"`
	Assistant int `json:"assistant"`
	Tool      int `json:"tool"`
}

// contextInjectedDTO records which optional prompt injections were present.
type contextInjectedDTO struct {
	AgentsMD bool `json:"agentsMd,omitempty"`
	Preset   bool `json:"preset,omitempty"`
	Memory   bool `json:"memory,omitempty"`
	Skills   bool `json:"skills,omitempty"`
}

// contextSnapshotDTO is the request-level snapshot of the latest run.
type contextSnapshotDTO struct {
	At                 *time.Time          `json:"at,omitempty"`
	Seq                int64               `json:"seq,omitempty"`
	Model              string              `json:"model,omitempty"`
	Window             int                 `json:"window,omitempty"`
	SystemPromptBytes  int                 `json:"systemPromptBytes"`
	SystemPromptTokens int                 `json:"systemPromptTokens"`
	Sections           []contextSectionDTO `json:"sections,omitempty"`
	Tools              []contextToolDTO    `json:"tools,omitempty"`
	History            contextHistoryDTO   `json:"history"`
	Injected           contextInjectedDTO  `json:"injected"`
	EstimatedPrompt    int                 `json:"estimatedPrompt"`
}

// contextRequestDTO is one provider call of the run.
type contextRequestDTO struct {
	Seq           int64      `json:"seq"`
	Iteration     int        `json:"iteration"`
	At            *time.Time `json:"at,omitempty"`
	Model         string     `json:"model,omitempty"`
	Prompt        int        `json:"prompt"`
	Completion    int        `json:"completion"`
	Cached        int        `json:"cached,omitempty"`
	Total         int        `json:"total"`
	ContextTokens int        `json:"contextTokens"`
	CostUSD       float64    `json:"costUsd,omitempty"`
	CostKnown     bool       `json:"costKnown,omitempty"`
}

// contextEventDTO is one audit entry of the timeline (compaction/spill/…).
type contextEventDTO struct {
	Seq              int64      `json:"seq"`
	Kind             string     `json:"kind"`
	At               *time.Time `json:"at,omitempty"`
	Detail           string     `json:"detail,omitempty"`
	Ref              string     `json:"ref,omitempty"`
	Before           int        `json:"before,omitempty"`
	After            int        `json:"after,omitempty"`
	Replaced         int        `json:"replaced,omitempty"`
	Saved            int        `json:"saved,omitempty"`
	Path             string     `json:"path,omitempty"`
	Bytes            int        `json:"bytes,omitempty"`
	Inferred         bool       `json:"inferred,omitempty"`
	ArchivedMessages int        `json:"archivedMessages,omitempty"`
	ArchivedBytes    int        `json:"archivedBytes,omitempty"`
	Truncated        bool       `json:"truncated,omitempty"`
	Outcome          string     `json:"outcome,omitempty"`
}

// contextDTO is the folded context view of one session.
type contextDTO struct {
	Revision     uint64              `json:"revision"`
	Snapshot     contextSnapshotDTO  `json:"snapshot"`
	Requests     []contextRequestDTO `json:"requests"`
	Events       []contextEventDTO   `json:"events"`
	Used         int                 `json:"used"`
	Window       int                 `json:"window"`
	TotalCostUSD float64             `json:"totalCostUsd,omitempty"`
	CostKnown    bool                `json:"costKnown,omitempty"`
}

// contextSnapshotPayload is the domain's cached snapshot. Found is false for a
// session with no recorded context data (cold/never run), which the UI renders
// as an empty state instead of inventing numbers.
type contextSnapshotPayload struct {
	SessionID string      `json:"sessionId"`
	Found     bool        `json:"found"`
	Context   *contextDTO `json:"context,omitempty"`
}

// contextDelta replaces the whole folded view. Why not a field-level diff: the
// backend re-derives the view from the durable event log and only exposes a
// Revision (contextinspect.go:246), and its lists are append-plus-trim windows
// (200 requests / 200 audit events), so "the newest whole object wins" is both
// exact and cheaper than reconstructing per-item edits.
type contextDelta struct {
	SessionID string      `json:"sessionId"`
	BaseRev   uint64      `json:"baseRev"`
	Rev       uint64      `json:"rev"`
	Found     bool        `json:"found"`
	Context   *contextDTO `json:"context,omitempty"`
	Resync    bool        `json:"resync,omitempty"`
}

// notification implements deltaPayload.
func (d *contextDelta) notification() string { return "context.delta" }

// revs implements deltaPayload.
func (d *contextDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *contextDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *contextDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *contextDelta) retainBytes() int {
	if d.Context == nil {
		return 32
	}
	total := 128 + 64*len(d.Context.Requests) + 96*len(d.Context.Events)
	for _, section := range d.Context.Snapshot.Sections {
		total += len(section.Label) + 48
	}
	for _, request := range d.Context.Requests {
		total += len(request.Model) + 48
	}
	for _, event := range d.Context.Events {
		total += len(event.Detail) + len(event.Path) + 64
	}
	return total
}

// merge implements deltaPayload: the newest whole view wins.
func (d *contextDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*contextDelta)
	if !ok || other.Rev <= d.Rev {
		return d
	}
	merged := *other
	merged.BaseRev = d.BaseRev
	merged.Resync = d.Resync || other.Resync
	return &merged
}

// promptsDelta is the prompt inbox delta: appended prompts, removed ids, and —
// when only the order changed — the whole id order.
type promptsDelta struct {
	SessionID string            `json:"sessionId"`
	BaseRev   uint64            `json:"baseRev"`
	Rev       uint64            `json:"rev"`
	Added     []queuedPromptDTO `json:"added,omitempty"`
	Removed   []string          `json:"removed,omitempty"`
	Reordered bool              `json:"reordered,omitempty"`
	Order     []string          `json:"order,omitempty"`
	Resync    bool              `json:"resync,omitempty"`
}

// notification implements deltaPayload.
func (d *promptsDelta) notification() string { return "prompts.delta" }

// revs implements deltaPayload.
func (d *promptsDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *promptsDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *promptsDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *promptsDelta) retainBytes() int {
	total := 64 + len(d.Removed)*32 + len(d.Order)*32
	for _, item := range d.Added {
		total += len(item.Text) + 64
	}
	return total
}

// merge implements deltaPayload: union by id, chronological removals, newest
// order wins.
func (d *promptsDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*promptsDelta)
	if !ok || other.Rev <= d.Rev {
		return d
	}
	added := make(map[string]queuedPromptDTO, len(d.Added)+len(other.Added))
	var order []string
	remember := func(items []queuedPromptDTO) {
		for _, item := range items {
			if _, seen := added[item.ID]; !seen {
				order = append(order, item.ID)
			}
			added[item.ID] = item
		}
	}
	remember(d.Added)
	remember(other.Added)
	removed := make(map[string]bool, len(d.Removed)+len(other.Removed))
	for _, id := range d.Removed {
		removed[id] = true
	}
	for _, id := range other.Removed {
		removed[id] = true
	}
	for _, item := range other.Added {
		delete(removed, item.ID)
	}
	merged := &promptsDelta{
		SessionID: d.SessionID,
		BaseRev:   d.BaseRev,
		Rev:       other.Rev,
		Resync:    d.Resync || other.Resync,
		Reordered: other.Reordered,
		Order:     other.Order,
	}
	for _, id := range order {
		if !removed[id] {
			merged.Added = append(merged.Added, added[id])
		}
	}
	for id := range removed {
		merged.Removed = append(merged.Removed, id)
	}
	sort.Strings(merged.Removed)
	if !merged.Resync && merged.retainBytes() > DeltaRingBytes {
		merged.Resync = true
		merged.Added, merged.Removed, merged.Order = nil, nil, nil
	}
	return merged
}

// --- identified ------------------------------------------------------------

// identified is what the item-set deltas need to key their items.
type identified interface{ domainID() string }

// --- delta types -----------------------------------------------------------

// itemSetDelta is the added/removed shape of §6.3: approvals and the
// append-only artifact list both speak it.
type itemSetDelta struct {
	// method is the wire notification name (not serialized).
	method  string
	BaseRev uint64   `json:"baseRev"`
	Rev     uint64   `json:"rev"`
	Added   []any    `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Resync  bool     `json:"resync,omitempty"`
}

// notification implements deltaPayload.
func (d *itemSetDelta) notification() string { return d.method }

// revs implements deltaPayload.
func (d *itemSetDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *itemSetDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *itemSetDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *itemSetDelta) retainBytes() int {
	total := 64 + len(d.Removed)*32
	for _, item := range d.Added {
		total += itemBytes(item)
	}
	return total
}

// merge implements deltaPayload: union by id, removals win over older adds but
// a newer add wins over an older removal.
func (d *itemSetDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*itemSetDelta)
	if !ok || other.Rev <= d.Rev {
		return d
	}
	added := make(map[string]any, len(d.Added)+len(other.Added))
	var order []string
	remember := func(items []any) {
		for _, item := range items {
			id := itemID(item)
			if id == "" {
				continue
			}
			if _, seen := added[id]; !seen {
				order = append(order, id)
			}
			added[id] = item
		}
	}
	remember(d.Added)
	remember(other.Added)
	removed := make(map[string]bool, len(d.Removed)+len(other.Removed))
	for _, id := range d.Removed {
		removed[id] = true
	}
	for _, id := range other.Removed {
		removed[id] = true
	}
	for _, item := range other.Added {
		delete(removed, itemID(item))
	}
	merged := &itemSetDelta{method: d.method, BaseRev: d.BaseRev, Rev: other.Rev,
		Resync: d.Resync || other.Resync}
	for _, id := range order {
		if removed[id] {
			continue
		}
		if _, isAdded := added[id]; isAdded {
			merged.Added = append(merged.Added, added[id])
		}
	}
	for id := range removed {
		merged.Removed = append(merged.Removed, id)
	}
	sort.Strings(merged.Removed)
	if merged.Resync {
		merged.Added, merged.Removed = nil, nil
	}
	if !merged.Resync && merged.retainBytes() > DeltaRingBytes {
		merged.Resync = true
		merged.Added, merged.Removed = nil, nil
	}
	return merged
}

// askDelta replaces the single pending question.
type askDelta struct {
	BaseRev uint64  `json:"baseRev"`
	Rev     uint64  `json:"rev"`
	Pending *askDTO `json:"pending"`
	Resync  bool    `json:"resync,omitempty"`
}

// notification implements deltaPayload.
func (d *askDelta) notification() string { return "ask.delta" }

// revs implements deltaPayload.
func (d *askDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *askDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *askDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *askDelta) retainBytes() int {
	if d.Pending == nil {
		return 32
	}
	return 64 + len(d.Pending.Text)
}

// merge implements deltaPayload: the newest whole object wins.
func (d *askDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*askDelta)
	if !ok || other.Rev <= d.Rev {
		return d
	}
	merged := *other
	merged.BaseRev = d.BaseRev
	merged.Resync = d.Resync || other.Resync
	return &merged
}

// planDelta carries the whole (small) plan review state.
type planDelta struct {
	BaseRev uint64 `json:"baseRev"`
	Rev     uint64 `json:"rev"`
	State   string `json:"state"`
	Content string `json:"content"`
	Resync  bool   `json:"resync,omitempty"`
}

// notification implements deltaPayload.
func (d *planDelta) notification() string { return "plan.delta" }

// revs implements deltaPayload.
func (d *planDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *planDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *planDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *planDelta) retainBytes() int { return 64 + len(d.Content) }

// merge implements deltaPayload.
func (d *planDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*planDelta)
	if !ok || other.Rev <= d.Rev {
		return d
	}
	merged := *other
	merged.BaseRev = d.BaseRev
	merged.Resync = d.Resync || other.Resync
	return &merged
}

// settingsDelta carries only the rows that changed.
type settingsDelta struct {
	BaseRev uint64            `json:"baseRev"`
	Rev     uint64            `json:"rev"`
	Rows    map[string]string `json:"rows,omitempty"`
	Removed []string          `json:"removed,omitempty"`
	Resync  bool              `json:"resync,omitempty"`
}

// notification implements deltaPayload.
func (d *settingsDelta) notification() string { return "settings.delta" }

// revs implements deltaPayload.
func (d *settingsDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *settingsDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *settingsDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *settingsDelta) retainBytes() int {
	total := 64 + len(d.Removed)*32
	for key, value := range d.Rows {
		total += len(key) + len(value) + 16
	}
	return total
}

// merge implements deltaPayload: union of changed rows, newest value wins.
func (d *settingsDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*settingsDelta)
	if !ok || other.Rev <= d.Rev {
		return d
	}
	rows := make(map[string]string, len(d.Rows)+len(other.Rows))
	for key, value := range d.Rows {
		rows[key] = value
	}
	removed := make(map[string]bool, len(d.Removed)+len(other.Removed))
	for _, key := range d.Removed {
		removed[key] = true
	}
	for key, value := range other.Rows {
		rows[key] = value
		delete(removed, key)
	}
	for _, key := range other.Removed {
		removed[key] = true
		delete(rows, key)
	}
	merged := &settingsDelta{BaseRev: d.BaseRev, Rev: other.Rev, Rows: rows,
		Resync: d.Resync || other.Resync}
	for key := range removed {
		merged.Removed = append(merged.Removed, key)
	}
	sort.Strings(merged.Removed)
	if !merged.Resync && merged.retainBytes() > DeltaRingBytes {
		merged.Resync = true
		merged.Rows, merged.Removed = nil, nil
	}
	return merged
}

// workspaceDelta is the session-list-shaped delta of the workspace list plus the
// current workspace.
type workspaceDelta struct {
	BaseRev   uint64         `json:"baseRev"`
	Rev       uint64         `json:"rev"`
	Added     []workspaceDTO `json:"added,omitempty"`
	Changed   []workspaceDTO `json:"changed,omitempty"`
	Removed   []string       `json:"removed,omitempty"`
	CurrentID *string        `json:"currentId,omitempty"`
	Resync    bool           `json:"resync,omitempty"`
}

// notification implements deltaPayload.
func (d *workspaceDelta) notification() string { return "workspace.list.delta" }

// revs implements deltaPayload.
func (d *workspaceDelta) revs() (uint64, uint64) { return d.BaseRev, d.Rev }

// setRevs implements deltaPayload.
func (d *workspaceDelta) setRevs(base, rev uint64) { d.BaseRev, d.Rev = base, rev }

// isResync implements deltaPayload.
func (d *workspaceDelta) isResync() bool { return d.Resync }

// retainBytes implements deltaPayload.
func (d *workspaceDelta) retainBytes() int {
	return (len(d.Added)+len(d.Changed))*128 + len(d.Removed)*32 + 64
}

// merge implements deltaPayload: item sets by id plus the newest currentId.
func (d *workspaceDelta) merge(next deltaPayload) deltaPayload {
	other, ok := next.(*workspaceDelta)
	if !ok || other.Rev <= d.Rev {
		return d
	}
	type entry struct {
		item    workspaceDTO
		added   bool
		changed bool
		removed bool
	}
	state := make(map[string]*entry, len(d.Added)+len(d.Changed)+len(other.Added)+len(other.Changed))
	var order []string
	remember := func(items []workspaceDTO, added bool) {
		for _, item := range items {
			existing := state[item.ID]
			if existing == nil {
				existing = &entry{}
				state[item.ID] = existing
				order = append(order, item.ID)
			}
			existing.item = item
			existing.removed = false
			if added {
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
				existing = &entry{}
				state[id] = existing
				order = append(order, id)
			}
			existing.removed = true
		}
	}
	remember(d.Added, true)
	remember(d.Changed, false)
	remove(d.Removed)
	remember(other.Added, true)
	remember(other.Changed, false)
	remove(other.Removed)

	merged := &workspaceDelta{BaseRev: d.BaseRev, Rev: other.Rev, CurrentID: other.CurrentID,
		Resync: d.Resync || other.Resync}
	if merged.CurrentID == nil {
		merged.CurrentID = d.CurrentID
	}
	for _, id := range order {
		item := state[id]
		switch {
		case item.removed:
			merged.Removed = append(merged.Removed, id)
		case item.added:
			merged.Added = append(merged.Added, item.item)
		case item.changed:
			merged.Changed = append(merged.Changed, item.item)
		}
	}
	return merged
}

// itemID extracts the domain id of one delta item. Typed DTOs are the normal
// case; a map is what a decoded delta carries (tests and any future proxy).
func itemID(item any) string {
	switch value := item.(type) {
	case identified:
		return value.domainID()
	case map[string]any:
		if id, ok := value["id"].(string); ok {
			return id
		}
	}
	return ""
}

// itemBytes is the approximate retained size of one delta item.
func itemBytes(item any) int {
	if task, ok := item.(taskDTO); ok {
		return 96 + len(task.Subject) + len(task.Description) + len(task.Owner)
	}
	switch value := item.(type) {
	case approvalDTO:
		return len(value.Summary) + len(value.Arguments) + 96
	case artifactDTO:
		return len(value.Body) + 96
	case map[string]any:
		total := 96
		for key, raw := range value {
			total += len(key) + 16
			if text, ok := raw.(string); ok {
				total += len(text)
			}
		}
		return total
	default:
		return 96
	}
}

// --- hub wiring ------------------------------------------------------------

// domainSources builds the six small domains' snapshots for the hub. The hub
// runs them on worker goroutines, so every method here is a backend read.
type domainSources struct {
	gateway *Gateway
}

// build reads one small domain.
func (s *domainSources) build(key string) (any, error) {
	g := s.gateway
	switch key {
	case domainApprovals:
		return g.buildApprovals(context.Background())
	case domainAsk:
		return g.buildAsk(context.Background())
	case domainPlan:
		return g.buildPlan(context.Background())
	case domainArtifacts:
		return g.buildArtifacts(context.Background())
	case domainSettings:
		return g.buildSettings(context.Background())
	case domainWorkspace:
		return g.buildWorkspaces(context.Background())
	case domainTasks:
		return g.buildTasks(context.Background(), "")
	}
	if sessionID, ok := tasksSessionID(key); ok {
		return g.buildTasks(context.Background(), sessionID)
	}
	if sessionID, ok := contextSessionID(key); ok {
		return g.buildContext(sessionID)
	}
	if sessionID, ok := promptsSessionID(key); ok {
		return g.buildPrompts(context.Background(), sessionID)
	}
	return nil, errors.New("gateway: unknown domain " + key)
}

// diffDomain compares two snapshots of one domain. It returns nil when nothing a
// client can see changed.
func (h *hub) diffDomain(key string, previous, next any) deltaPayload {
	if _, ok := sessionKey(key); ok {
		old, oldOK := previous.(sessionDTO)
		fresh, freshOK := next.(sessionDTO)
		if !oldOK || !freshOK {
			return nil
		}
		if ops := diffSession(old, fresh); len(ops) > 0 {
			return &sessionDelta{SessionID: key[len("session:"):], Ops: ops}
		}
		return nil
	}
	switch key {
	case listDomain:
		old, oldOK := previous.([]sessionInfoDTO)
		fresh, freshOK := next.([]sessionInfoDTO)
		if !oldOK || !freshOK {
			return nil
		}
		delta := diffSessionList(old, fresh)
		if len(delta.Added) == 0 && len(delta.Changed) == 0 && len(delta.Removed) == 0 {
			return nil
		}
		return &delta
	case domainApprovals:
		old, oldOK := previous.(approvalsSnapshot)
		fresh, freshOK := next.(approvalsSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffApprovals(old, fresh); delta != nil {
			return delta
		}
		return nil
	case domainAsk:
		old, oldOK := previous.(askSnapshot)
		fresh, freshOK := next.(askSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffAsk(old, fresh); delta != nil {
			return delta
		}
		return nil
	case domainPlan:
		old, oldOK := previous.(planSnapshot)
		fresh, freshOK := next.(planSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffPlan(old, fresh); delta != nil {
			return delta
		}
		return nil
	case domainArtifacts:
		old, oldOK := previous.(artifactsSnapshot)
		fresh, freshOK := next.(artifactsSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffArtifacts(old, fresh); delta != nil {
			return delta
		}
		return nil
	case domainTasks:
		old, oldOK := previous.(tasksSnapshot)
		fresh, freshOK := next.(tasksSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffTasks(old, fresh); delta != nil {
			return delta
		}
		return nil
	case domainSettings:
		old, oldOK := previous.(settingsSnapshot)
		fresh, freshOK := next.(settingsSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffSettings(old, fresh); delta != nil {
			return delta
		}
		return nil
	case domainWorkspace:
		old, oldOK := previous.(workspacesSnapshot)
		fresh, freshOK := next.(workspacesSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffWorkspaces(old, fresh); delta != nil {
			return delta
		}
		return nil
	}
	if _, ok := contextSessionID(key); ok {
		old, oldOK := previous.(contextSnapshotPayload)
		fresh, freshOK := next.(contextSnapshotPayload)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffContext(old, fresh); delta != nil {
			return delta
		}
		return nil
	}
	if _, ok := tasksSessionID(key); ok {
		old, oldOK := previous.(tasksSnapshot)
		fresh, freshOK := next.(tasksSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffTasks(old, fresh); delta != nil {
			return delta
		}
		return nil
	}
	if _, ok := promptsSessionID(key); ok {
		old, oldOK := previous.(promptsSnapshot)
		fresh, freshOK := next.(promptsSnapshot)
		if !oldOK || !freshOK {
			return nil
		}
		if delta := diffPrompts(old, fresh); delta != nil {
			return delta
		}
		return nil
	}
	return nil
}

// resyncDelta builds the "discard your copy and snapshot" marker for a domain.
func (h *hub) resyncDelta(domain *domainState, base, rev uint64) deltaPayload {
	_ = h
	if domain.sessionID != "" {
		return &sessionDelta{SessionID: domain.sessionID, BaseRev: base, Rev: rev, Resync: true}
	}
	switch domain.key {
	case listDomain:
		return &listDelta{BaseRev: base, Rev: rev, Resync: true}
	case domainApprovals:
		return &itemSetDelta{method: "approvals.delta", BaseRev: base, Rev: rev, Resync: true}
	case domainArtifacts:
		return &itemSetDelta{method: "artifacts.delta", BaseRev: base, Rev: rev, Resync: true}
	case domainAsk:
		return &askDelta{BaseRev: base, Rev: rev, Resync: true}
	case domainPlan:
		return &planDelta{BaseRev: base, Rev: rev, Resync: true}
	case domainSettings:
		return &settingsDelta{BaseRev: base, Rev: rev, Resync: true}
	case domainWorkspace:
		return &workspaceDelta{BaseRev: base, Rev: rev, Resync: true}
	case domainTasks:
		return &itemSetDelta{method: "tasks.delta", BaseRev: base, Rev: rev, Resync: true}
	}
	if _, ok := tasksSessionID(domain.key); ok {
		return &itemSetDelta{method: "tasks.delta", BaseRev: base, Rev: rev, Resync: true}
	}
	if sessionID, ok := contextSessionID(domain.key); ok {
		return &contextDelta{SessionID: sessionID, BaseRev: base, Rev: rev, Resync: true}
	}
	if sessionID, ok := promptsSessionID(domain.key); ok {
		return &promptsDelta{SessionID: sessionID, BaseRev: base, Rev: rev, Resync: true}
	}
	return nil
}

// --- builders --------------------------------------------------------------

// buildApprovals reads the approvals service into a snapshot.
func (g *Gateway) buildApprovals(ctx context.Context) (any, error) {
	service, ok := g.root.Get(contracts.KeyApprovals)
	if !ok || service == nil {
		return approvalsSnapshot{Permission: contracts.PermissionWorkspace}, nil
	}
	timeout := 0
	if settings, ok := g.root.Get(contracts.KeySettings); ok && settings != nil {
		timeout = contracts.NormalizeApprovalTimeoutSeconds(settings.Get(contracts.SettingAgentApprovalTimeoutSeconds, ""))
	}
	mode := service.Mode()
	snapshot := approvalsSnapshot{
		Mode:                   mode,
		Permission:             contracts.NormalizePermissionMode(mode),
		ApprovalTimeoutSeconds: timeout,
		Pending:                make([]approvalDTO, 0, len(service.Pending())),
	}
	for _, request := range service.Pending() {
		item := approvalDTO{
			ID:        request.ID,
			Tool:      request.Tool,
			Summary:   request.Summary,
			Arguments: request.Arguments,
			CreatedAt: timePointer(request.CreatedAt),
			SessionID: request.SessionID,
			Source:    request.Source,
		}
		// Only automation-origin requests can expire (RG-04): interactive
		// approvals wait indefinitely, so no expiry is fabricated for them.
		if timeout > 0 && strings.EqualFold(strings.TrimSpace(request.Source), contracts.SessionOriginAutomation) && !request.CreatedAt.IsZero() {
			expires := request.CreatedAt.Add(time.Duration(timeout) * time.Second)
			item.ExpiresAt = timePointer(expires)
		}
		snapshot.Pending = append(snapshot.Pending, item)
	}
	_ = ctx
	return snapshot, nil
}

// buildAsk reads the gateway's own interaction bridge.
func (g *Gateway) buildAsk(context.Context) (any, error) {
	return askSnapshot{Pending: g.pendingQuestionSnapshot()}, nil
}

// buildPlan reads the plan review bridge.
func (g *Gateway) buildPlan(context.Context) (any, error) {
	state, content := g.plan.snapshot()
	return planSnapshot{State: state, Content: content}, nil
}

// buildArtifacts reads the artifact presenter's retained history.
func (g *Gateway) buildArtifacts(context.Context) (any, error) {
	return artifactsSnapshot{Items: g.artifactSnapshot()}, nil
}

// buildSettings reads every settings row.
func (g *Gateway) buildSettings(context.Context) (any, error) {
	settings, ok := g.root.Get(contracts.KeySettings)
	if !ok || settings == nil {
		return settingsSnapshot{Rows: map[string]string{}}, nil
	}
	rows := settings.All()
	if rows == nil {
		rows = map[string]string{}
	}
	return settingsSnapshot{Rows: rows}, nil
}

// buildWorkspaces reads the workspace registry.
func (g *Gateway) buildWorkspaces(ctx context.Context) (any, error) {
	service, ok := g.root.Get(contracts.KeyWorkspace)
	if !ok || service == nil {
		return workspacesSnapshot{Workspaces: []workspaceDTO{}}, nil
	}
	list, err := service.List(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := workspacesSnapshot{Workspaces: make([]workspaceDTO, 0, len(list))}
	for _, workspace := range list {
		snapshot.Workspaces = append(snapshot.Workspaces, workspaceDTO{
			ID:        workspace.ID,
			Name:      workspace.Name,
			Root:      workspace.Root,
			Color:     workspace.Color,
			CreatedAt: timePointer(workspace.CreatedAt),
		})
	}
	current, ok, err := service.Current(ctx)
	if err != nil {
		return nil, err
	}
	if ok {
		snapshot.CurrentID = current.ID
	}
	return snapshot, nil
}

// buildPrompts reads one conversation's prompt inbox.
//
// The queue's truth is the backend service (contracts.PromptQueue, implemented
// by the agent module's ChatService): the gateway never keeps its own copy, so a
// restart, a second tab and the desktop product all see the same inbox.
func (g *Gateway) buildPrompts(ctx context.Context, sessionID string) (any, error) {
	chat, err := g.chatService()
	if err != nil {
		return nil, err
	}
	snapshot := promptsSnapshot{SessionID: sessionID, Prompts: []queuedPromptDTO{}}
	queue, ok := chat.(contracts.PromptQueue)
	if !ok {
		// The backend does not queue (an older assembly): an empty inbox is the
		// honest answer, and prompts.enqueue reports the same condition.
		return snapshot, nil
	}
	_ = ctx
	for _, item := range queue.QueuedPrompts(sessionID) {
		snapshot.Prompts = append(snapshot.Prompts, queuedPromptDTO{
			ID:        item.ID,
			Text:      item.Text,
			CreatedAt: timePointer(item.CreatedAt),
		})
	}
	return snapshot, nil
}

// taskBoard resolves the task board a domain reads: the process-wide board, or
// the per-session view when the caller named a conversation and the backend
// implements contracts.SessionTaskBoard.
func (g *Gateway) taskBoard(sessionID string) contracts.TaskBoard {
	board, ok := g.root.Get(contracts.KeyTaskBoard)
	if !ok || board == nil {
		return nil
	}
	if sessionID == "" {
		return board
	}
	if scoped, ok := board.(contracts.SessionTaskBoard); ok {
		return scoped.TaskBoardForSession(sessionID)
	}
	return board
}

// buildTasks reads the task board into a snapshot. A gateway without the toolkit
// module answers an empty board rather than an error: the chip then simply has
// nothing to show.
func (g *Gateway) buildTasks(ctx context.Context, sessionID string) (any, error) {
	snapshot := tasksSnapshot{SessionID: sessionID, Tasks: []taskDTO{}}
	board := g.taskBoard(sessionID)
	if board == nil {
		return snapshot, nil
	}
	list, err := board.List(ctx)
	if err != nil {
		return nil, err
	}
	snapshot.Tasks = make([]taskDTO, 0, len(list))
	for _, task := range list {
		snapshot.Tasks = append(snapshot.Tasks, taskDTO{
			ID:          task.ID,
			Subject:     task.Subject,
			Description: task.Description,
			Status:      task.Status,
			Owner:       task.Owner,
			BlockedBy:   task.BlockedBy,
			WriteScopes: task.WriteScopes,
			Created:     timePointer(task.Created),
			Updated:     timePointer(task.Updated),
		})
	}
	return snapshot, nil
}

// buildContext reads one conversation's folded context timeline.
//
// The service is optional: an assembly without the agent module answers
// found=false, which is the same empty state the Context tab shows for a
// conversation that never ran.
func (g *Gateway) buildContext(sessionID string) (any, error) {
	payload := contextSnapshotPayload{SessionID: sessionID}
	inspector, ok := g.root.Get(contracts.KeyContext)
	if !ok || inspector == nil {
		return payload, nil
	}
	view, found := inspector.SessionContext(sessionID)
	if !found {
		return payload, nil
	}
	payload.Found = true
	payload.Context = contextViewDTO(view)
	return payload, nil
}

// contextViewDTO maps the contract's folded view onto the wire shape.
func contextViewDTO(view contracts.SessionContext) *contextDTO {
	out := &contextDTO{
		Revision:     view.Revision,
		Snapshot:     contextSnapshotDTO{History: contextHistoryDTO{}},
		Requests:     make([]contextRequestDTO, 0, len(view.Requests)),
		Events:       make([]contextEventDTO, 0, len(view.Events)),
		Used:         view.Used,
		Window:       view.Window,
		TotalCostUSD: view.TotalCostUSD,
		CostKnown:    view.CostKnown,
	}
	snapshot := view.Snapshot
	out.Snapshot = contextSnapshotDTO{
		At:                 timePointer(snapshot.At),
		Seq:                snapshot.Seq,
		Model:              snapshot.Model,
		Window:             snapshot.Window,
		SystemPromptBytes:  snapshot.SystemPromptBytes,
		SystemPromptTokens: snapshot.SystemPromptTokens,
		History: contextHistoryDTO{
			Messages:  snapshot.History.Messages,
			User:      snapshot.History.User,
			Assistant: snapshot.History.Assistant,
			Tool:      snapshot.History.Tool,
		},
		Injected: contextInjectedDTO{
			AgentsMD: snapshot.Injected.AgentsMD,
			Preset:   snapshot.Injected.Preset,
			Memory:   snapshot.Injected.Memory,
			Skills:   snapshot.Injected.Skills,
		},
		EstimatedPrompt: snapshot.EstimatedPrompt,
	}
	for _, section := range snapshot.Sections {
		out.Snapshot.Sections = append(out.Snapshot.Sections, contextSectionDTO{
			ID: section.ID, Label: section.Label, Source: section.Source,
			Bytes: section.Bytes, Tokens: section.Tokens, Hash: section.Hash,
		})
	}
	for _, tool := range snapshot.Tools {
		out.Snapshot.Tools = append(out.Snapshot.Tools, contextToolDTO{
			Name: tool.Name, Group: tool.Group, Tokens: tool.Tokens,
		})
	}
	for _, request := range view.Requests {
		out.Requests = append(out.Requests, contextRequestDTO{
			Seq: request.Seq, Iteration: request.Iteration, At: timePointer(request.At),
			Model: request.Model, Prompt: request.Prompt, Completion: request.Completion,
			Cached: request.Cached, Total: request.Total, ContextTokens: request.ContextTokens,
			CostUSD: request.CostUSD, CostKnown: request.CostKnown,
		})
	}
	for _, event := range view.Events {
		out.Events = append(out.Events, contextEventDTO{
			Seq: event.Seq, Kind: string(event.Kind), At: timePointer(event.At),
			Detail: event.Detail, Ref: event.Ref, Before: event.Before, After: event.After,
			Replaced: event.Replaced, Saved: event.Saved, Path: event.Path, Bytes: event.Bytes,
			Inferred: event.Inferred, ArchivedMessages: event.ArchivedMessages,
			ArchivedBytes: event.ArchivedBytes, Truncated: event.Truncated, Outcome: event.Outcome,
		})
	}
	return out
}

// diffContext compares two folded views. The backend's Revision is the change
// signal: same revision (or both "not found") means nothing a client can see
// moved. When it moved, the delta carries the whole view — see contextDelta.
func diffContext(previous, next contextSnapshotPayload) *contextDelta {
	if previous.Found == next.Found {
		if !next.Found {
			return nil
		}
		if previous.Context != nil && next.Context != nil && previous.Context.Revision == next.Context.Revision {
			return nil
		}
	}
	return &contextDelta{
		SessionID: next.SessionID,
		Found:     next.Found,
		Context:   next.Context,
	}
}

// diffTasks compares two board snapshots as an upsert set: the board has mutable
// fields (status/owner/updated) and no stable per-field change log, so a task
// that changed goes into "added" whole and the client upserts by id. Mutations
// come from the agent's task tools and from the browser's own commands, so the
// id set is usually small enough that re-sending the changed rows is cheaper
// than a field-level diff.
func diffTasks(previous, next tasksSnapshot) *itemSetDelta {
	delta := &itemSetDelta{method: "tasks.delta"}
	known := make(map[string]taskDTO, len(previous.Tasks))
	for _, task := range previous.Tasks {
		known[task.ID] = task
	}
	stillPresent := make(map[string]bool, len(next.Tasks))
	for _, task := range next.Tasks {
		stillPresent[task.ID] = true
		if old, existed := known[task.ID]; !existed || !sameTask(old, task) {
			delta.Added = append(delta.Added, task)
		}
	}
	for _, task := range previous.Tasks {
		if !stillPresent[task.ID] {
			delta.Removed = append(delta.Removed, task.ID)
		}
	}
	if len(delta.Added) == 0 && len(delta.Removed) == 0 {
		return nil
	}
	sort.Strings(delta.Removed)
	return delta
}

// sameTask reports whether two board rows carry the same client-visible values.
func sameTask(a, b taskDTO) bool {
	if a.ID != b.ID || a.Subject != b.Subject || a.Description != b.Description ||
		a.Status != b.Status || a.Owner != b.Owner {
		return false
	}
	if len(a.BlockedBy) != len(b.BlockedBy) || len(a.WriteScopes) != len(b.WriteScopes) {
		return false
	}
	for index := range a.BlockedBy {
		if a.BlockedBy[index] != b.BlockedBy[index] {
			return false
		}
	}
	for index := range a.WriteScopes {
		if a.WriteScopes[index] != b.WriteScopes[index] {
			return false
		}
	}
	return sameTimePointer(a.Created, b.Created) && sameTimePointer(a.Updated, b.Updated)
}

// sameTimePointer compares two optional timestamps.
func sameTimePointer(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// diffPrompts compares two inbox snapshots.
func diffPrompts(previous, next promptsSnapshot) *promptsDelta {
	delta := &promptsDelta{SessionID: next.SessionID}
	known := make(map[string]queuedPromptDTO, len(previous.Prompts))
	for _, item := range previous.Prompts {
		known[item.ID] = item
	}
	stillQueued := make(map[string]bool, len(next.Prompts))
	for _, item := range next.Prompts {
		stillQueued[item.ID] = true
		if _, existed := known[item.ID]; !existed {
			delta.Added = append(delta.Added, item)
		}
	}
	for _, item := range previous.Prompts {
		if !stillQueued[item.ID] {
			delta.Removed = append(delta.Removed, item.ID)
		}
	}
	if len(delta.Added) == 0 && len(delta.Removed) == 0 {
		// Same set: only a reordering can have happened.
		sameOrder := len(previous.Prompts) == len(next.Prompts)
		if sameOrder {
			for i := range previous.Prompts {
				if previous.Prompts[i].ID != next.Prompts[i].ID {
					sameOrder = false
					break
				}
			}
		}
		if sameOrder {
			return nil
		}
		delta.Reordered = true
		for _, item := range next.Prompts {
			delta.Order = append(delta.Order, item.ID)
		}
	}
	return delta
}

// --- diffs -----------------------------------------------------------------

// diffApprovals compares two approvals snapshots.
func diffApprovals(previous, next approvalsSnapshot) *itemSetDelta {
	delta := &itemSetDelta{method: "approvals.delta"}
	known := make(map[string]bool, len(previous.Pending))
	for _, item := range previous.Pending {
		known[item.ID] = true
	}
	stillPending := make(map[string]bool, len(next.Pending))
	for _, item := range next.Pending {
		stillPending[item.ID] = true
		if !known[item.ID] {
			delta.Added = append(delta.Added, item)
		}
	}
	for _, item := range previous.Pending {
		if !stillPending[item.ID] {
			delta.Removed = append(delta.Removed, item.ID)
		}
	}
	// The mode is a domain-level scalar: a permission change means the client
	// must re-read the whole snapshot (it also changes what "allow always"
	// means, so a silent patch would be worse than a resync).
	if previous.Mode != next.Mode || previous.Permission != next.Permission ||
		previous.ApprovalTimeoutSeconds != next.ApprovalTimeoutSeconds {
		delta.Resync = true
		delta.Added, delta.Removed = nil, nil
		return delta
	}
	if len(delta.Added) == 0 && len(delta.Removed) == 0 {
		return nil
	}
	return delta
}

// diffAsk compares two pending-question snapshots.
func diffAsk(previous, next askSnapshot) *askDelta {
	if equalAsk(previous.Pending, next.Pending) {
		return nil
	}
	return &askDelta{Pending: next.Pending}
}

// diffPlan compares two plan review states.
func diffPlan(previous, next planSnapshot) *planDelta {
	if previous == next {
		return nil
	}
	return &planDelta{State: next.State, Content: next.Content}
}

// diffArtifacts compares two artifact lists (append-only, newest first).
func diffArtifacts(previous, next artifactsSnapshot) *itemSetDelta {
	delta := &itemSetDelta{method: "artifacts.delta"}
	known := make(map[string]bool, len(previous.Items))
	for _, item := range previous.Items {
		known[item.ID] = true
	}
	stillThere := make(map[string]bool, len(next.Items))
	for _, item := range next.Items {
		stillThere[item.ID] = true
		if !known[item.ID] {
			delta.Added = append(delta.Added, item)
		}
	}
	for _, item := range previous.Items {
		if !stillThere[item.ID] {
			delta.Removed = append(delta.Removed, item.ID)
		}
	}
	if len(delta.Added) == 0 && len(delta.Removed) == 0 {
		return nil
	}
	return delta
}

// diffSettings compares two settings row maps.
func diffSettings(previous, next settingsSnapshot) *settingsDelta {
	delta := &settingsDelta{}
	for key, value := range next.Rows {
		if old, existed := previous.Rows[key]; !existed || old != value {
			if delta.Rows == nil {
				delta.Rows = make(map[string]string, 4)
			}
			delta.Rows[key] = value
		}
	}
	for key := range previous.Rows {
		if _, still := next.Rows[key]; !still {
			delta.Removed = append(delta.Removed, key)
		}
	}
	if len(delta.Rows) == 0 && len(delta.Removed) == 0 {
		return nil
	}
	sort.Strings(delta.Removed)
	return delta
}

// diffWorkspaces compares two workspace snapshots.
func diffWorkspaces(previous, next workspacesSnapshot) *workspaceDelta {
	delta := &workspaceDelta{}
	known := make(map[string]workspaceDTO, len(previous.Workspaces))
	for _, item := range previous.Workspaces {
		known[item.ID] = item
	}
	stillThere := make(map[string]bool, len(next.Workspaces))
	for _, item := range next.Workspaces {
		stillThere[item.ID] = true
		old, existed := known[item.ID]
		switch {
		case !existed:
			delta.Added = append(delta.Added, item)
		case old != item:
			delta.Changed = append(delta.Changed, item)
		}
	}
	for _, item := range previous.Workspaces {
		if !stillThere[item.ID] {
			delta.Removed = append(delta.Removed, item.ID)
		}
	}
	if previous.CurrentID != next.CurrentID {
		current := next.CurrentID
		delta.CurrentID = &current
	}
	if len(delta.Added) == 0 && len(delta.Changed) == 0 && len(delta.Removed) == 0 && delta.CurrentID == nil {
		return nil
	}
	return delta
}

// equalAsk compares two optional pending questions by value.
func equalAsk(a, b *askDTO) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.ID != b.ID || a.Text != b.Text || !equalTimePointer(a.CreatedAt, b.CreatedAt) ||
		len(a.Options) != len(b.Options) {
		return false
	}
	for i := range a.Options {
		if a.Options[i] != b.Options[i] {
			return false
		}
	}
	return true
}
