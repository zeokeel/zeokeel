package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/memory"
	"github.com/zeokeel/zeokeel/internal/store"
)

// memoryKind is the JSON discriminator every memory result carries.
const memoryKind = "memory"

// memoryDescription documents the tool and its JSON argument schema.
const memoryDescription = `Manage persistent memories for the current workspace and the user.

Call it with a JSON object:
  {"action":"add","content":"The release branch is release/2026.","scope":"workspace","kind":"project"}
  {"action":"add","content":"Prefer concise answers.","scope":"global","kind":"user"}
  {"action":"list","limit":20,"scope":"workspace"}
  {"action":"search","query":"release","scope":"workspace"}
  {"action":"update","id":"<memory id>","content":"Updated note.","scope":"workspace"}
  {"action":"delete","id":"<memory id>","scope":"workspace"}
  {"action":"clear","scope":"workspace"}

"action" is required and is one of add, list, search, update, delete, clear.
"scope" is optional and one of global, workspace (default workspace). Global
memories are user-level and shared by every workspace; workspace memories are
project context for the current workspace only.
"kind" is optional for add and one of user, feedback, project, reference
(default project). "content" is required for add and update: non-empty, trimmed,
at most 8192 bytes. "id" is required for update and delete. "query" is required
for search. "limit" is optional for list (1-200, default 50).

Whether a new memory takes effect depends on the configured memory mode for its
scope: auto stores it as active, suggest stores it as a pending suggestion for
the user to review, and off rejects the write. A scope whose mode is off also
rejects update, delete and clear, so the agent cannot rewrite or erase existing
memories while learning for that scope is disabled. The tool always records
source=auto (agent-authored). Memories survive sessions and are injected into
future turns, so store durable facts, conventions and decisions; never store
secrets, credentials, tokens or one-off scratch notes. Secret-like content is
refused. list/search see active and pending rows of the requested scope, and
update/delete/clear reject rows of another scope.

The result is a JSON document {"kind":"memory","action":...,"scope":...,
"status":...,"kind":...,"memories":[...],"count":...,"message":...}; count is
the number of rows returned or affected. The whole result is capped at 64 KiB.`

// MemoryConfig configures the memory tool. Store is required at call time; a
// tool constructed without it reports a clear error. WorkspaceID resolves the
// workspace scope for every invocation instead of freezing it at build time,
// because the selected workspace can change without rebuilding the tool.
//
// Modes optionally resolves the current per-scope write modes ("off",
// "suggest", "auto"); nil keeps the documented defaults (workspace auto, global
// suggest). The app wires this to the session Settings, so a mode change takes
// effect on the next tool call without rebuilding the agent.
type MemoryConfig struct {
	Store       *store.Store
	WorkspaceID func() string
	Modes       func() (workspace, global string)
}

// MemoryTool is the runtime.Tool that reads and writes the two memory scopes.
// It holds no per-session state: the store is authoritative, so an add from one
// turn is visible to the next turn's prompt without any in-memory cache.
type MemoryTool struct {
	cfg MemoryConfig
}

// NewMemoryTool returns a memory tool scoped by cfg.WorkspaceID.
func NewMemoryTool(cfg MemoryConfig) *MemoryTool { return &MemoryTool{cfg: cfg} }

// Name implements runtime.Tool.
func (t *MemoryTool) Name() string { return "memory" }

// Description implements runtime.Tool.
func (t *MemoryTool) Description() string { return memoryDescription }

// MemoryResult is the JSON document every memory invocation returns.
type MemoryResult struct {
	Kind       string         `json:"kind"`
	Action     string         `json:"action"`
	Scope      string         `json:"scope,omitempty"`
	Status     string         `json:"status,omitempty"`
	MemoryKind string         `json:"memory_kind,omitempty"`
	Memories   []store.Memory `json:"memories"`
	Count      int            `json:"count"`
	Message    string         `json:"message,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
}

// memoryArgs decodes every action's fields into one struct; fields that do not
// apply to the chosen action are ignored.
type memoryArgs struct {
	Action  string `json:"action"`
	Content string `json:"content"`
	ID      string `json:"id"`
	Query   string `json:"query"`
	Scope   string `json:"scope"`
	Kind    string `json:"kind"`
	Limit   int    `json:"limit"`
}

// Memory write modes.
const (
	memoryModeOff     = "off"
	memoryModeSuggest = "suggest"
	memoryModeAuto    = "auto"
)

// Invoke implements runtime.Tool. Bad arguments, a disabled scope and scope
// violations return an error (shown to the model as a tool failure); a
// successful call returns one JSON document capped at DefaultMaxOutputBytes.
func (t *MemoryTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.cfg.Store == nil {
		return "", errors.New("memory: no store configured")
	}

	var in memoryArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("memory: invalid arguments: %w", err)
		}
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		return "", errors.New(`memory: "action" is required (add, list, search, update, delete, clear)`)
	}
	scope, err := t.normalizeScope(in.Scope)
	if err != nil {
		return "", err
	}

	result := MemoryResult{
		Kind:     memoryKind,
		Action:   action,
		Scope:    scope,
		Memories: make([]store.Memory, 0),
	}
	switch action {
	case "add":
		return t.add(in, scope, result)
	case "list":
		return t.list(in, scope, result)
	case "search":
		return t.search(in, scope, result)
	case "update":
		return t.update(in, scope, result)
	case "delete":
		return t.delete(in, scope, result)
	case "clear":
		return t.clear(in, scope, result)
	default:
		return "", fmt.Errorf("memory: unknown action %q (want add, list, search, update, delete, clear)", in.Action)
	}
}

// add implements the scoped, mode-aware write path.
func (t *MemoryTool) add(in memoryArgs, scope string, result MemoryResult) (string, error) {
	content, err := normalizeMemoryToolContent(in.Content)
	if err != nil {
		return "", err
	}
	kind, err := normalizeMemoryToolKind(in.Kind)
	if err != nil {
		return "", err
	}
	if err := rejectSecretMemoryContent(content); err != nil {
		return "", err
	}
	workspaceID, err := t.workspaceFor(scope)
	if err != nil {
		return "", err
	}
	if err := t.requireMemoryWritesAllowed(scope); err != nil {
		return "", err
	}
	status, allowed := memoryStatusForMode(t.modeFor(scope))
	if !allowed {
		return "", fmt.Errorf("memory: writing %s memories is disabled by the current settings (mode off)", scope)
	}
	written, err := t.cfg.Store.CreateMemoryScoped(store.Memory{
		Scope:       scope,
		WorkspaceID: workspaceID,
		Kind:        kind,
		Source:      store.MemorySourceAuto,
		Status:      status,
		Content:     content,
	})
	if err != nil {
		return "", fmt.Errorf("memory: add: %w", err)
	}
	result.Memories = append(result.Memories, *written)
	result.Count = 1
	result.Status = written.Status
	result.MemoryKind = written.Kind
	result.Message = "memory added"
	if written.Status == store.MemoryStatusPending {
		result.Message = "memory saved as a suggestion, pending review"
	}
	return encodeMemoryResult(&result)
}

// list returns the scope's active and pending rows, newest first.
func (t *MemoryTool) list(in memoryArgs, scope string, result MemoryResult) (string, error) {
	workspaceID, err := t.workspaceFor(scope)
	if err != nil {
		return "", err
	}
	memories, err := t.readVisible(scope, workspaceID, "", in.Limit)
	if err != nil {
		return "", fmt.Errorf("memory: list: %w", err)
	}
	result.Memories = memories
	result.Count = len(memories)
	result.Message = fmt.Sprintf("%d memories", len(memories))
	return encodeMemoryResult(&result)
}

// search returns the scope's active and pending rows matching query.
func (t *MemoryTool) search(in memoryArgs, scope string, result MemoryResult) (string, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "", errors.New(`memory: "query" is required for search`)
	}
	workspaceID, err := t.workspaceFor(scope)
	if err != nil {
		return "", err
	}
	memories, err := t.readVisible(scope, workspaceID, query, in.Limit)
	if err != nil {
		return "", fmt.Errorf("memory: search: %w", err)
	}
	result.Memories = memories
	result.Count = len(memories)
	result.Message = fmt.Sprintf("%d matches", len(memories))
	return encodeMemoryResult(&result)
}

// update replaces one memory's content after checking its scope ownership.
func (t *MemoryTool) update(in memoryArgs, scope string, result MemoryResult) (string, error) {
	if err := t.requireMemoryWritesAllowed(scope); err != nil {
		return "", err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return "", errors.New(`memory: "id" is required for update`)
	}
	content, err := normalizeMemoryToolContent(in.Content)
	if err != nil {
		return "", err
	}
	if err := rejectSecretMemoryContent(content); err != nil {
		return "", err
	}
	current, err := t.requireInScope(id, scope)
	if err != nil {
		return "", err
	}
	updated, err := t.cfg.Store.UpdateMemory(current.ID, content)
	if err != nil {
		return "", fmt.Errorf("memory: update: %w", err)
	}
	result.Memories = append(result.Memories, *updated)
	result.Count = 1
	result.Status = updated.Status
	result.MemoryKind = updated.Kind
	result.Message = "memory updated"
	return encodeMemoryResult(&result)
}

// delete removes one memory after checking its scope ownership.
func (t *MemoryTool) delete(in memoryArgs, scope string, result MemoryResult) (string, error) {
	if err := t.requireMemoryWritesAllowed(scope); err != nil {
		return "", err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return "", errors.New(`memory: "id" is required for delete`)
	}
	current, err := t.requireInScope(id, scope)
	if err != nil {
		return "", err
	}
	if err := t.cfg.Store.DeleteMemory(current.ID); err != nil {
		return "", fmt.Errorf("memory: delete: %w", err)
	}
	result.Memories = append(result.Memories, *current)
	result.Count = 1
	result.Status = current.Status
	result.MemoryKind = current.Kind
	result.Message = "memory deleted"
	return encodeMemoryResult(&result)
}

// clear removes every memory of the requested scope only.
func (t *MemoryTool) clear(_ memoryArgs, scope string, result MemoryResult) (string, error) {
	if err := t.requireMemoryWritesAllowed(scope); err != nil {
		return "", err
	}
	workspaceID, err := t.workspaceFor(scope)
	if err != nil {
		return "", err
	}
	cleared, err := t.cfg.Store.ClearMemoriesScoped(scope, workspaceID)
	if err != nil {
		return "", fmt.Errorf("memory: clear: %w", err)
	}
	result.Count = cleared
	result.Message = fmt.Sprintf("%d memories cleared", cleared)
	return encodeMemoryResult(&result)
}

// readVisible merges a scope's active and pending rows (sorted newest first and
// bounded by limit). status="" addresses all rows, which also covers the
// superseded history when a caller explicitly asks for it through the store
// API; the tool's read surface deliberately exposes only active + pending.
func (t *MemoryTool) readVisible(scope, workspaceID, query string, limit int) ([]store.Memory, error) {
	page := normalizeMemoryToolLimit(limit)
	out := make([]store.Memory, 0, page)
	for _, status := range []string{store.MemoryStatusActive, store.MemoryStatusPending} {
		var (
			rows []store.Memory
			err  error
		)
		if query == "" {
			rows, err = t.cfg.Store.ListMemoriesScoped(scope, workspaceID, status, page)
		} else {
			rows, err = t.cfg.Store.SearchMemoriesScoped(scope, workspaceID, query, status, page)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > page {
		out = out[:page]
	}
	return out, nil
}

// modeFor resolves the write mode for scope, applying the documented defaults
// when the settings resolver is absent and failing closed to off for
// unrecognized values.
func (t *MemoryTool) modeFor(scope string) string {
	workspaceMode, globalMode := "auto", "suggest"
	if t.cfg.Modes != nil {
		if w, g := t.cfg.Modes(); strings.TrimSpace(w) != "" || strings.TrimSpace(g) != "" {
			workspaceMode = w
			globalMode = g
		}
	}
	raw := globalMode
	if scope == store.MemoryScopeWorkspace {
		raw = workspaceMode
	}
	return normalizeMemoryMode(raw)
}

// normalizeMemoryMode maps a raw settings value to off, suggest or auto.
// Unrecognized values fail closed to off (the conservative choice: a broken
// setting must not silently start writing memories), and an empty value keeps
// the per-scope default decided by modeFor before this call.
func normalizeMemoryMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case memoryModeOff:
		return memoryModeOff
	case memoryModeSuggest:
		return memoryModeSuggest
	case memoryModeAuto:
		return memoryModeAuto
	default:
		return memoryModeOff
	}
}

// memoryStatusForMode maps a write mode to the initial status of a new memory.
func memoryStatusForMode(mode string) (status string, allowed bool) {
	switch mode {
	case memoryModeAuto:
		return store.MemoryStatusActive, true
	case memoryModeSuggest:
		return store.MemoryStatusPending, true
	default:
		return "", false
	}
}

// requireMemoryWritesAllowed rejects a mutating action when the target scope's
// write mode is off. The mode gates maintenance of existing rows too, not only
// new captures: off means "this scope does not learn or change memories".
func (t *MemoryTool) requireMemoryWritesAllowed(scope string) error {
	if t.modeFor(scope) == memoryModeOff {
		return fmt.Errorf("memory writes are disabled for the %s scope", scope)
	}
	return nil
}

// normalizeScope resolves the optional scope argument: empty defaults to
// workspace and unknown scopes are rejected with a stable error.
func (t *MemoryTool) normalizeScope(raw string) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(raw))
	switch scope {
	case "":
		return store.MemoryScopeWorkspace, nil
	case store.MemoryScopeGlobal, store.MemoryScopeWorkspace:
		return scope, nil
	default:
		return "", fmt.Errorf("memory: unknown scope %q (want global or workspace)", raw)
	}
}

// workspaceFor resolves the workspace id for scope. Global operations need no
// workspace; a workspace operation without a resolvable workspace is refused.
func (t *MemoryTool) workspaceFor(scope string) (string, error) {
	if scope == store.MemoryScopeGlobal {
		return "", nil
	}
	workspaceID := ""
	if t.cfg.WorkspaceID != nil {
		workspaceID = strings.TrimSpace(t.cfg.WorkspaceID())
	}
	if workspaceID == "" {
		return "", errors.New("memory: no workspace scope available")
	}
	return workspaceID, nil
}

// requireInScope loads id and rejects it when it belongs to another scope (or
// another workspace inside the workspace scope).
func (t *MemoryTool) requireInScope(id, scope string) (*store.Memory, error) {
	m, err := t.cfg.Store.GetMemory(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("memory: no memory with id %q", id)
		}
		return nil, fmt.Errorf("memory: get %q: %w", id, err)
	}
	if m.Scope != scope {
		return nil, fmt.Errorf("memory: memory %q belongs to the %s scope, not %s", id, m.Scope, scope)
	}
	if scope == store.MemoryScopeWorkspace {
		workspaceID, err := t.workspaceFor(scope)
		if err != nil {
			return nil, err
		}
		if m.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("memory: memory %q belongs to another workspace", id)
		}
	}
	return m, nil
}

// normalizeMemoryToolContent trims and validates one memory body.
func normalizeMemoryToolContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", errors.New(`memory: "content" is required`)
	}
	if len(content) > store.MaxMemoryContentBytes {
		return "", fmt.Errorf("memory: content is %d bytes, maximum is %d",
			len(content), store.MaxMemoryContentBytes)
	}
	return content, nil
}

// normalizeMemoryToolKind resolves the optional kind argument.
func normalizeMemoryToolKind(raw string) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(raw))
	switch kind {
	case "":
		return store.MemoryKindProject, nil
	case store.MemoryKindUser, store.MemoryKindFeedback, store.MemoryKindProject, store.MemoryKindReference:
		return kind, nil
	default:
		return "", fmt.Errorf("memory: unknown kind %q (want user, feedback, project or reference)", raw)
	}
}

// rejectSecretMemoryContent refuses credential-like content before it is
// persisted (the automatic pipeline uses the same scrubber).
func rejectSecretMemoryContent(content string) error {
	if _, found := memory.ScrubSecrets(content); found {
		return errors.New("memory: content looks like it contains a secret credential; it was not stored")
	}
	return nil
}

// normalizeMemoryToolLimit clamps a requested page size into the tool's bounds.
func normalizeMemoryToolLimit(limit int) int {
	if limit <= 0 {
		return store.DefaultMemoryLimit
	}
	if limit > store.MaxMemoryLimit {
		return store.MaxMemoryLimit
	}
	return limit
}

// encodeMemoryResult marshals result and, when it exceeds the 64 KiB output
// cap, drops trailing memories until it fits. The count follows the returned
// rows and truncated marks the cut, so the model knows the list is partial.
func encodeMemoryResult(result *MemoryResult) (string, error) {
	for {
		out, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("memory: encode result: %w", err)
		}
		if len(out) <= DefaultMaxOutputBytes {
			return string(out), nil
		}
		if len(result.Memories) == 0 {
			return "", fmt.Errorf("memory: result exceeds %d bytes", DefaultMaxOutputBytes)
		}
		result.Memories = result.Memories[:len(result.Memories)-1]
		result.Count = len(result.Memories)
		result.Truncated = true
		switch result.Action {
		case "list":
			result.Message = fmt.Sprintf("%d memories (truncated)", result.Count)
		case "search":
			result.Message = fmt.Sprintf("%d matches (truncated)", result.Count)
		}
	}
}
