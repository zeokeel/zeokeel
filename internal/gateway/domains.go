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
	// domainPromptsPrefix names one conversation's prompt inbox
	// ("prompts:<sessionID>"): queued prompts belong to a session, so the domain
	// is per-session, not global.
	domainPromptsPrefix = "prompts:"
)

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
