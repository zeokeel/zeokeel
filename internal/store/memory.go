package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxMemoryContentBytes bounds one memory's content. The cap is enforced on
// write and again when the prompt renders the memory, so a database edited
// outside this API still cannot flood the instruction.
const MaxMemoryContentBytes = 8 * 1024

const (
	// DefaultMemoryLimit is the page size used when a caller passes a
	// non-positive limit.
	DefaultMemoryLimit = 50
	// MaxMemoryLimit bounds one listing or search. It exists so a caller
	// cannot ask SQLite for an unbounded result set. It is above the
	// per-workspace consolidation cap (300) so a consolidation pass can see
	// every active row of one scope.
	MaxMemoryLimit = 400
)

// Memory scopes. A global memory is a user-level preference shared by every
// workspace; a workspace memory is project context bound to one workspace.
const (
	MemoryScopeGlobal    = "global"
	MemoryScopeWorkspace = "workspace"
)

// Memory kinds follow the product's learning taxonomy: user preferences,
// feedback/corrections, project facts and reference pointers.
const (
	MemoryKindUser      = "user"
	MemoryKindFeedback  = "feedback"
	MemoryKindProject   = "project"
	MemoryKindReference = "reference"
)

// Memory sources distinguish notes the user/agent wrote deliberately from
// candidates captured by the automatic pipeline.
const (
	MemorySourceManual = "manual"
	MemorySourceAuto   = "auto"
)

// Memory statuses: active notes are injected into prompts, pending notes are
// suggestions awaiting review, and superseded notes are kept only as history.
const (
	MemoryStatusActive     = "active"
	MemoryStatusPending    = "pending"
	MemoryStatusSuperseded = "superseded"
)

// Memory is one persistent note. It either belongs to a workspace (the
// default) or to the global user scope. Memories survive sessions and are
// injected into future turns, so the id is stable across updates while
// CreatedAt stays fixed and UpdatedAt moves.
type Memory struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	Scope        string    `json:"scope"`
	Kind         string    `json:"kind"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	Content      string    `json:"content"`
	SessionID    string    `json:"session_id"`
	TurnID       string    `json:"turn_id"`
	ContentHash  string    `json:"content_hash"`
	Confidence   float64   `json:"confidence"`
	Importance   int       `json:"importance"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastUsedAt   time.Time `json:"last_used_at"`
	SupersededBy string    `json:"superseded_by"`
}

// memoryColumns is the column order every read and write uses.
const memoryColumns = `id, workspace_id, scope, kind, source, status, content,
	session_id, turn_id, content_hash, confidence, importance,
	created_at, updated_at, last_used_at, superseded_by`

// normalizeMemoryScope validates scope and applies the workspace default.
func normalizeMemoryScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return MemoryScopeWorkspace, nil
	}
	switch scope {
	case MemoryScopeGlobal, MemoryScopeWorkspace:
		return scope, nil
	default:
		return "", fmt.Errorf("%w: memory: unknown scope %q", ErrInvalidArgument, scope)
	}
}

// normalizeMemoryWorkspace validates the workspace part of a scope pair: a
// workspace memory needs an id, a global memory must not carry one.
func normalizeMemoryWorkspace(scope, workspaceID string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if scope == MemoryScopeGlobal {
		if workspaceID != "" {
			return "", fmt.Errorf("%w: memory: global memory must not have a workspace id", ErrInvalidArgument)
		}
		return "", nil
	}
	if workspaceID == "" {
		return "", fmt.Errorf("%w: memory: empty workspace id", ErrInvalidArgument)
	}
	return workspaceID, nil
}

// normalizeMemoryScopeWorkspace normalizes a scope pair together.
func normalizeMemoryScopeWorkspace(scope, workspaceID string) (string, string, error) {
	scope, err := normalizeMemoryScope(scope)
	if err != nil {
		return "", "", err
	}
	workspaceID, err = normalizeMemoryWorkspace(scope, workspaceID)
	if err != nil {
		return "", "", err
	}
	return scope, workspaceID, nil
}

// normalizeMemoryKind validates kind and applies the project default.
func normalizeMemoryKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return MemoryKindProject, nil
	}
	switch kind {
	case MemoryKindUser, MemoryKindFeedback, MemoryKindProject, MemoryKindReference:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: memory: unknown kind %q", ErrInvalidArgument, kind)
	}
}

// normalizeMemorySource validates source and applies the manual default.
func normalizeMemorySource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return MemorySourceManual, nil
	}
	switch source {
	case MemorySourceManual, MemorySourceAuto:
		return source, nil
	default:
		return "", fmt.Errorf("%w: memory: unknown source %q", ErrInvalidArgument, source)
	}
}

// normalizeMemoryStatus validates status and applies the active default.
func normalizeMemoryStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		return MemoryStatusActive, nil
	}
	switch status {
	case MemoryStatusActive, MemoryStatusPending, MemoryStatusSuperseded:
		return status, nil
	default:
		return "", fmt.Errorf("%w: memory: unknown status %q", ErrInvalidArgument, status)
	}
}

// normalizeMemoryContent returns the canonical stored form of one memory
// body: CRLF/CR line endings become LF (matching internal/memory.Hash and the
// content-hash contract) and surrounding whitespace is trimmed.
func normalizeMemoryContent(content string) (string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("%w: memory: empty content", ErrInvalidArgument)
	}
	if len(content) > MaxMemoryContentBytes {
		return "", fmt.Errorf("%w: memory: content is %d bytes, maximum is %d",
			ErrInvalidArgument, len(content), MaxMemoryContentBytes)
	}
	return content, nil
}

// hashMemoryContent returns the stable dedupe hash of one note.
func hashMemoryContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// memoryLimit clamps a requested page size into [1, MaxMemoryLimit].
func memoryLimit(limit int) int {
	if limit <= 0 {
		return DefaultMemoryLimit
	}
	if limit > MaxMemoryLimit {
		return MaxMemoryLimit
	}
	return limit
}

// CreateMemoryScoped persists one memory after validating scope, workspace,
// kind, source, status and content. An exact-content duplicate in the same
// scope refreshes the existing row instead of inserting a second one, so the
// automatic pipeline can retry safely.
func (s *Store) CreateMemoryScoped(m Memory) (*Memory, error) {
	return s.createMemory(m, true)
}

// createMemory is the shared insert path. The pre-scope CreateMemory API keeps
// its historical append semantics (dedupe=false); the scoped API dedupes exact
// content so the automatic pipeline can retry safely.
func (s *Store) createMemory(m Memory, dedupe bool) (*Memory, error) {
	scope, workspaceID, err := normalizeMemoryScopeWorkspace(m.Scope, m.WorkspaceID)
	if err != nil {
		return nil, err
	}
	kind, err := normalizeMemoryKind(m.Kind)
	if err != nil {
		return nil, err
	}
	source, err := normalizeMemorySource(m.Source)
	if err != nil {
		return nil, err
	}
	status, err := normalizeMemoryStatus(m.Status)
	if err != nil {
		return nil, err
	}
	if status == MemoryStatusSuperseded {
		return nil, fmt.Errorf("%w: memory: a new memory cannot start superseded", ErrInvalidArgument)
	}
	content, err := normalizeMemoryContent(m.Content)
	if err != nil {
		return nil, err
	}
	hash := hashMemoryContent(content)
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	confidence := m.Confidence
	if confidence <= 0 || confidence > 1 {
		confidence = 1.0
	}
	memory := &Memory{
		ID:          id,
		WorkspaceID: workspaceID,
		Scope:       scope,
		Kind:        kind,
		Source:      source,
		Status:      status,
		Content:     content,
		SessionID:   strings.TrimSpace(m.SessionID),
		TurnID:      strings.TrimSpace(m.TurnID),
		ContentHash: hash,
		Confidence:  confidence,
		Importance:  m.Importance,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Exact-content dedupe: refresh the existing active/pending row and return
	// it, so repeated captures do not multiply the list.
	existing, err := s.FindMemoryByHash(scope, workspaceID, hash)
	if err != nil {
		return nil, err
	}
	if dedupe && existing != nil {
		if _, err := s.db.Exec(
			`UPDATE memories SET updated_at = ?, confidence = MAX(confidence, ?),
				importance = MAX(importance, ?), last_used_at = ?
			 WHERE id = ?`,
			encodeTime(now), confidence, m.Importance, encodeTime(now), existing.ID,
		); err != nil {
			return nil, fmt.Errorf("store: create memory: refresh duplicate: %w", err)
		}
		return s.GetMemory(existing.ID)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: create memory: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err := tx.Exec(
		`INSERT INTO memories(`+memoryColumns+`) VALUES
		 (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		memory.ID, memory.WorkspaceID, memory.Scope, memory.Kind, memory.Source,
		memory.Status, memory.Content, memory.SessionID, memory.TurnID,
		memory.ContentHash, memory.Confidence, memory.Importance,
		encodeTime(memory.CreatedAt), encodeTime(memory.UpdatedAt), "", "",
	); err != nil {
		return nil, fmt.Errorf("store: create memory: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: create memory: commit: %w", err)
	}
	return memory, nil
}

// CreateMemory persists one manual, active, workspace-scoped note. It is the
// pre-scope API kept for callers that predate the global scope.
func (s *Store) CreateMemory(workspaceID, content string) (*Memory, error) {
	return s.createMemory(Memory{
		Scope:       MemoryScopeWorkspace,
		WorkspaceID: workspaceID,
		Content:     content,
		Kind:        MemoryKindProject,
		Source:      MemorySourceManual,
		Status:      MemoryStatusActive,
	}, false)
}

// GetMemory returns the memory with id, or an error wrapping ErrNotFound when
// no such row exists. Callers that enforce a scope compare the returned
// Scope/WorkspaceID.
func (s *Store) GetMemory(id string) (*Memory, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: get memory: empty id", ErrInvalidArgument)
	}
	row := s.db.QueryRow(`SELECT `+memoryColumns+` FROM memories WHERE id = ?`, id)
	m, err := scanMemory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get memory %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get memory %q: %w", id, err)
	}
	return m, nil
}

// ListMemories returns the workspace's active memories newest first (the
// pre-scope API). Global and pending rows are visible through the scoped API.
func (s *Store) ListMemories(workspaceID string, limit int) ([]Memory, error) {
	return s.ListMemoriesScoped(MemoryScopeWorkspace, workspaceID, MemoryStatusActive, limit)
}

// ListMemoriesScoped returns one scope's memories newest first. A non-empty
// status filters the rows; an empty status returns active, pending and
// superseded rows so callers can render history or review queues.
func (s *Store) ListMemoriesScoped(scope, workspaceID, status string, limit int) ([]Memory, error) {
	scope, workspaceID, err := normalizeMemoryScopeWorkspace(scope, workspaceID)
	if err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	if status != "" {
		if status, err = normalizeMemoryStatus(status); err != nil {
			return nil, err
		}
	}
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE scope = ? AND workspace_id = ?`
	args := []any{scope, workspaceID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, memoryLimit(limit))
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list %s memories: %w", scope, err)
	}
	defer rows.Close()
	return scanMemories(rows, scope)
}

// SearchMemories returns the workspace's active memories whose content
// contains query (the pre-scope API).
func (s *Store) SearchMemories(workspaceID, query string) ([]Memory, error) {
	return s.SearchMemoriesScoped(MemoryScopeWorkspace, workspaceID, query, MemoryStatusActive, DefaultMemoryLimit)
}

// SearchMemoriesScoped is the scoped case-insensitive LIKE search. Matching is
// literal for LIKE metacharacters and bounded by limit.
func (s *Store) SearchMemoriesScoped(scope, workspaceID, query, status string, limit int) ([]Memory, error) {
	scope, workspaceID, err := normalizeMemoryScopeWorkspace(scope, workspaceID)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: search memories: empty query", ErrInvalidArgument)
	}
	status = strings.TrimSpace(status)
	if status != "" {
		if status, err = normalizeMemoryStatus(status); err != nil {
			return nil, err
		}
	}
	sqlQuery := `SELECT ` + memoryColumns + ` FROM memories
		 WHERE scope = ? AND workspace_id = ? AND content LIKE ? ESCAPE '\'`
	args := []any{scope, workspaceID, "%" + escapeLike(query) + "%"}
	if status != "" {
		sqlQuery += ` AND status = ?`
		args = append(args, status)
	}
	sqlQuery += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, memoryLimit(limit))
	rows, err := s.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("store: search %s memories: %w", scope, err)
	}
	defer rows.Close()
	return scanMemories(rows, scope)
}

// FindMemoryByHash returns the active or pending row with the same content
// hash in one scope, or nil when there is none. It powers exact-content dedupe.
func (s *Store) FindMemoryByHash(scope, workspaceID, hash string) (*Memory, error) {
	scope, workspaceID, err := normalizeMemoryScopeWorkspace(scope, workspaceID)
	if err != nil {
		return nil, err
	}
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil, fmt.Errorf("%w: find memory: empty hash", ErrInvalidArgument)
	}
	row := s.db.QueryRow(
		`SELECT `+memoryColumns+` FROM memories
		 WHERE scope = ? AND workspace_id = ? AND content_hash = ? AND status != ?
		 ORDER BY updated_at DESC, id DESC LIMIT 1`,
		scope, workspaceID, hash, MemoryStatusSuperseded,
	)
	m, err := scanMemory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: find memory by hash: %w", err)
	}
	return m, nil
}

// UpdateMemory replaces one memory's content and moves UpdatedAt. The row's
// scope, workspace, kind, source, status and CreatedAt are preserved; the
// content hash is recomputed for dedupe.
func (s *Store) UpdateMemory(id, content string) (*Memory, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: update memory: empty id", ErrInvalidArgument)
	}
	content, err := normalizeMemoryContent(content)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	res, err := s.db.Exec(
		`UPDATE memories SET content = ?, content_hash = ?, updated_at = ? WHERE id = ?`,
		content, hashMemoryContent(content), encodeTime(now), id,
	)
	if err != nil {
		return nil, fmt.Errorf("store: update memory %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("store: update memory %q: rows affected: %w", id, err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("store: update memory %q: %w", id, ErrNotFound)
	}
	return s.GetMemory(id)
}

// SetMemoryStatus moves one memory between active and pending and returns the
// updated row. Accepting a suggestion and retracting a note both use this
// path; superseding has its own call so the replacement link is recorded.
func (s *Store) SetMemoryStatus(id, status string) (*Memory, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: set memory status: empty id", ErrInvalidArgument)
	}
	status, err := normalizeMemoryStatus(status)
	if err != nil {
		return nil, err
	}
	if status == MemoryStatusSuperseded {
		return nil, fmt.Errorf("%w: memory: use SupersedeMemory to supersede", ErrInvalidArgument)
	}
	now := nowUTC()
	res, err := s.db.Exec(
		`UPDATE memories SET status = ?, updated_at = ? WHERE id = ?`,
		status, encodeTime(now), id,
	)
	if err != nil {
		return nil, fmt.Errorf("store: set memory %q status: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("store: set memory %q status: rows affected: %w", id, err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("store: set memory %q status: %w", id, ErrNotFound)
	}
	return s.GetMemory(id)
}

// SupersedeMemory marks id as replaced by byID, keeping it as history while
// removing it from the prompt's active set.
func (s *Store) SupersedeMemory(id, byID string) error {
	id = strings.TrimSpace(id)
	byID = strings.TrimSpace(byID)
	if id == "" || byID == "" {
		return fmt.Errorf("%w: supersede memory: empty id", ErrInvalidArgument)
	}
	now := nowUTC()
	res, err := s.db.Exec(
		`UPDATE memories SET status = ?, superseded_by = ?, updated_at = ? WHERE id = ?`,
		MemoryStatusSuperseded, byID, encodeTime(now), id,
	)
	if err != nil {
		return fmt.Errorf("store: supersede memory %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: supersede memory %q: rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("store: supersede memory %q: %w", id, ErrNotFound)
	}
	return nil
}

// RetireMemory demotes id to the superseded status without a replacement row.
// Consolidation's cap eviction uses it: the note leaves the prompt's active set
// but its content survives as history; unlike SupersedeMemory there is no
// successor, so superseded_by stays empty.
func (s *Store) RetireMemory(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: retire memory: empty id", ErrInvalidArgument)
	}
	now := nowUTC()
	res, err := s.db.Exec(
		`UPDATE memories SET status = ?, superseded_by = '', updated_at = ? WHERE id = ?`,
		MemoryStatusSuperseded, encodeTime(now), id,
	)
	if err != nil {
		return fmt.Errorf("store: retire memory %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: retire memory %q: rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("store: retire memory %q: %w", id, ErrNotFound)
	}
	return nil
}

// MarkMemoriesUsed moves last_used_at for the memories a prompt just injected.
// Unknown ids are ignored: a memory deleted between the prompt build and this
// call is not an error.
func (s *Store) MarkMemoriesUsed(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := encodeTime(nowUTC())
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: mark memories used: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := tx.Exec(`UPDATE memories SET last_used_at = ? WHERE id = ?`, now, id); err != nil {
			return fmt.Errorf("store: mark memory %q used: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: mark memories used: commit: %w", err)
	}
	return nil
}

// MemoryCounts returns one scope's active and pending row counts, used by the
// Settings page and the review-inbox badge.
func (s *Store) MemoryCounts(scope, workspaceID string) (active, pending int, err error) {
	scope, workspaceID, err = normalizeMemoryScopeWorkspace(scope, workspaceID)
	if err != nil {
		return 0, 0, err
	}
	err = s.db.QueryRow(
		`SELECT
		   COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0)
		 FROM memories WHERE scope = ? AND workspace_id = ?`,
		MemoryStatusActive, MemoryStatusPending, scope, workspaceID,
	).Scan(&active, &pending)
	if err != nil {
		return 0, 0, fmt.Errorf("store: count %s memories: %w", scope, err)
	}
	return active, pending, nil
}

// ClearMemoriesScoped deletes one scope's memories (any status) and returns
// how many rows were removed. The global scope clears with workspaceID "".
func (s *Store) ClearMemoriesScoped(scope, workspaceID string) (int, error) {
	scope, workspaceID, err := normalizeMemoryScopeWorkspace(scope, workspaceID)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`DELETE FROM memories WHERE scope = ? AND workspace_id = ?`, scope, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("store: clear %s memories: %w", scope, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: clear %s memories: rows affected: %w", scope, err)
	}
	return int(affected), nil
}

// DeleteMemory removes one memory row regardless of scope. The Settings UI
// deletes pending suggestions this way and checks the row's scope before the
// call.
func (s *Store) DeleteMemory(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: delete memory: empty id", ErrInvalidArgument)
	}
	res, err := s.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete memory %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete memory %q: rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("store: delete memory %q: %w", id, ErrNotFound)
	}
	return nil
}

// ClearMemories deletes every memory in one workspace and returns how many
// rows were removed. Other workspaces and the global scope are untouched.
func (s *Store) ClearMemories(workspaceID string) (int, error) {
	workspaceID, err := normalizeMemoryWorkspace(MemoryScopeWorkspace, workspaceID)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`DELETE FROM memories WHERE workspace_id = ?`, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("store: clear memories for workspace %q: %w", workspaceID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: clear memories for workspace %q: rows affected: %w", workspaceID, err)
	}
	return int(affected), nil
}

// scanMemory reads one row in memoryColumns order. The last_used_at column is
// optional in old rows, so an empty value decodes to the zero time.
func scanMemory(row scanner) (*Memory, error) {
	var (
		m                         Memory
		created, updated, lastUse string
	)
	if err := row.Scan(
		&m.ID, &m.WorkspaceID, &m.Scope, &m.Kind, &m.Source, &m.Status,
		&m.Content, &m.SessionID, &m.TurnID, &m.ContentHash,
		&m.Confidence, &m.Importance, &created, &updated, &lastUse, &m.SupersededBy,
	); err != nil {
		return nil, err
	}
	var err error
	if m.CreatedAt, err = decodeTime(created); err != nil {
		return nil, err
	}
	if m.UpdatedAt, err = decodeTime(updated); err != nil {
		return nil, err
	}
	if strings.TrimSpace(lastUse) != "" {
		if m.LastUsedAt, err = decodeTime(lastUse); err != nil {
			return nil, err
		}
	}
	// A database written by an older build (or edited by hand) may miss the
	// defaults; normalize the read so every caller sees a valid scope pair.
	if strings.TrimSpace(m.Scope) == "" {
		m.Scope = MemoryScopeWorkspace
	}
	if strings.TrimSpace(m.Kind) == "" {
		m.Kind = MemoryKindProject
	}
	if strings.TrimSpace(m.Source) == "" {
		m.Source = MemorySourceManual
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = MemoryStatusActive
	}
	if m.Confidence <= 0 {
		m.Confidence = 1.0
	}
	return &m, nil
}

// scanMemories reads every row and wraps scan failures with the scope.
func scanMemories(rows *sql.Rows, scope string) ([]Memory, error) {
	list := make([]Memory, 0)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan %s memories: %w", scope, err)
		}
		list = append(list, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list %s memories: %w", scope, err)
	}
	return list, nil
}

// escapeLike neutralizes the LIKE metacharacters so a search for "100%" is
// literal. The caller pairs it with ESCAPE '\'.
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}
