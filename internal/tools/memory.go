package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/store"
)

// memoryKind is the JSON discriminator every memory result carries.
const memoryKind = "memory"

// memoryDescription documents the tool and its JSON argument schema.
const memoryDescription = `Manage persistent memories for the current workspace.

Call it with a JSON object:
  {"action":"add","content":"The release branch is release/2026."}
  {"action":"list","limit":20}
  {"action":"search","query":"release"}
  {"action":"update","id":"<memory id>","content":"Updated note."}
  {"action":"delete","id":"<memory id>"}
  {"action":"clear"}

"action" is required and is one of add, list, search, update, delete, clear.
"content" is required for add and update: non-empty, trimmed, at most 8192 bytes.
"id" is required for update and delete. "query" is required for search.
"limit" is optional for list (1-200, default 50).

Memories survive sessions and are injected into future turns for this
workspace, so store durable facts, conventions and decisions. Never store
secrets, credentials, tokens or one-off scratch notes; the user can delete any
memory from Settings > Memory. list/search/clear only see the current
workspace, and update/delete reject an id from another workspace.

The result is a JSON document {"kind":"memory","action":...,"memories":[...],
"count":...,"message":...}; count is the number of rows returned or affected.
The whole result is capped at 64 KiB.`

// MemoryConfig configures the memory tool. Store is required at call time; a
// tool constructed without it reports a clear error. WorkspaceID resolves the
// scope for every invocation instead of freezing it at build time, because the
// selected workspace can change without rebuilding the tool.
type MemoryConfig struct {
	Store       *store.Store
	WorkspaceID func() string
}

// MemoryTool is the runtime.Tool that reads and writes workspace memories.
// It holds no per-session state: the store is authoritative, so an add from
// one turn is visible to the next turn's prompt without any in-memory cache.
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
	Kind      string         `json:"kind"`
	Action    string         `json:"action"`
	Memories  []store.Memory `json:"memories"`
	Count     int            `json:"count"`
	Message   string         `json:"message,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
}

// memoryArgs decodes every action's fields into one struct; fields that do not
// apply to the chosen action are ignored.
type memoryArgs struct {
	Action  string `json:"action"`
	Content string `json:"content"`
	ID      string `json:"id"`
	Query   string `json:"query"`
	Limit   int    `json:"limit"`
}

// Invoke implements runtime.Tool. Bad arguments and scope failures return an
// error (shown to the model as a tool failure); a successful call returns one
// JSON document capped at DefaultMaxOutputBytes.
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
	workspaceID := ""
	if t.cfg.WorkspaceID != nil {
		workspaceID = strings.TrimSpace(t.cfg.WorkspaceID())
	}
	if workspaceID == "" {
		return "", errors.New("memory: no workspace scope available")
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

	result := MemoryResult{
		Kind:     memoryKind,
		Action:   action,
		Memories: make([]store.Memory, 0),
	}
	switch action {
	case "add":
		content := strings.TrimSpace(in.Content)
		if content == "" {
			return "", errors.New(`memory: "content" is required for add`)
		}
		m, err := t.cfg.Store.CreateMemory(workspaceID, content)
		if err != nil {
			return "", fmt.Errorf("memory: add: %w", err)
		}
		result.Memories = append(result.Memories, *m)
		result.Count = 1
		result.Message = "memory added"

	case "list":
		memories, err := t.cfg.Store.ListMemories(workspaceID, in.Limit)
		if err != nil {
			return "", fmt.Errorf("memory: list: %w", err)
		}
		result.Memories = memories
		result.Count = len(memories)
		result.Message = fmt.Sprintf("%d memories", len(memories))

	case "search":
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return "", errors.New(`memory: "query" is required for search`)
		}
		memories, err := t.cfg.Store.SearchMemories(workspaceID, query)
		if err != nil {
			return "", fmt.Errorf("memory: search: %w", err)
		}
		result.Memories = memories
		result.Count = len(memories)
		result.Message = fmt.Sprintf("%d matches", len(memories))

	case "update":
		id := strings.TrimSpace(in.ID)
		if id == "" {
			return "", errors.New(`memory: "id" is required for update`)
		}
		if strings.TrimSpace(in.Content) == "" {
			return "", errors.New(`memory: "content" is required for update`)
		}
		if _, err := t.requireInWorkspace(id, workspaceID); err != nil {
			return "", err
		}
		m, err := t.cfg.Store.UpdateMemory(id, in.Content)
		if err != nil {
			return "", fmt.Errorf("memory: update: %w", err)
		}
		result.Memories = append(result.Memories, *m)
		result.Count = 1
		result.Message = "memory updated"

	case "delete":
		id := strings.TrimSpace(in.ID)
		if id == "" {
			return "", errors.New(`memory: "id" is required for delete`)
		}
		m, err := t.requireInWorkspace(id, workspaceID)
		if err != nil {
			return "", err
		}
		if err := t.cfg.Store.DeleteMemory(id); err != nil {
			return "", fmt.Errorf("memory: delete: %w", err)
		}
		result.Memories = append(result.Memories, *m)
		result.Count = 1
		result.Message = "memory deleted"

	case "clear":
		cleared, err := t.cfg.Store.ClearMemories(workspaceID)
		if err != nil {
			return "", fmt.Errorf("memory: clear: %w", err)
		}
		result.Count = cleared
		result.Message = fmt.Sprintf("%d memories cleared", cleared)

	default:
		return "", fmt.Errorf("memory: unknown action %q (want add, list, search, update, delete, clear)", in.Action)
	}
	return encodeMemoryResult(&result)
}

// requireInWorkspace loads id and rejects it when it belongs to another
// workspace. The store has no workspace parameter on update/delete, so the
// tool is the layer that enforces the scope.
func (t *MemoryTool) requireInWorkspace(id, workspaceID string) (*store.Memory, error) {
	m, err := t.cfg.Store.GetMemory(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("memory: no memory with id %q", id)
		}
		return nil, fmt.Errorf("memory: get %q: %w", id, err)
	}
	if m.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("memory: memory %q belongs to another workspace", id)
	}
	return m, nil
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
