// Package memorytools exposes the product's memory tool to the model.
//
// History: until rig v0.19.0 this package wrapped the Rig memory tool
// (github.com/zeokeel/zeokeel-rig/tools/memory) and only added the "update"
// action, because the flat rig.MemoryStore contract has no update half. Rig
// v0.24.0 replaced that tool with a wiki contract (pages, summaries,
// Index/Read/Write/Append/Delete/Search) and dropped the flat action set, while
// keeping rig.MemoryStore as a host-side type. The product's durable store, its
// settings page and the model-facing action names are still flat (add/list/
// search/remove/clear), so the product now owns this tool outright instead of
// wrapping a tool that no longer exists. Migrating the memory feature to the wiki
// contract is tracked in docs/research/rig-upgrade-2026-11/PARITY-v0.24.0.md.
package memorytools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ToolName is the model-facing tool name. It must stay "memory": prompts,
	// the permission matrix and the session memory tab all address it by name.
	ToolName = "memory"
	// DefaultScope is what the model-visible output calls an empty scope.
	DefaultScope = "workspace"
	// updateAction is the action this package adds over the flat store contract.
	updateAction = "update"
	// maxOutputBytes bounds one tool response.
	maxOutputBytes = 32 << 10
	// truncatedMarker is appended when a listing is cut at maxOutputBytes.
	truncatedMarker = "\n[truncated: output exceeded the memory tool limit]"
)

// actions is the tool's complete action list, in schema order.
var actions = []string{"add", "list", "search", "remove", "clear", updateAction}

// actionList renders the action list for error messages.
func actionList() string { return strings.Join(actions, ", ") }

// Tools returns the product memory tool bound to store: exactly one tool named
// "memory". store may be nil, which binds a fresh process-local store (that
// fallback has no update half, so its update action reports the documented
// unsupported-store error).
func Tools(store contracts.MemoryStore) []rig.Tool {
	if store == nil {
		store = newMemoryStore()
	}
	return []rig.Tool{&tool{store: store}}
}

// tool is the `memory` tool implementation.
type tool struct {
	store contracts.MemoryStore
}

var _ rig.Tool = (*tool)(nil)

// Name implements rig.Tool.
func (t *tool) Name() string { return ToolName }

// Description implements rig.Tool. The action list mirrors the flat store
// contract; the durability sentence is the product's (the host store persists,
// unlike the Rig tool's process-local default).
func (t *tool) Description() string {
	return `Manage the agent's memory entries through a host-provided store.

Actions:
- add: store a new memory. "text" is required; optional "scope" (default workspace) groups entries (for example workspace or global).
- list: show memories as "- <id> [<scope>] <text>". Optional "scope" filters by exact scope.
- search: case-insensitive substring search over memory text. "query" is required; optional "scope" filters by exact scope.
- remove: delete one memory by "id". Unknown IDs are an error.
- clear: remove every memory in the optional "scope" (all memories when the scope is omitted) and report the count.
- update: replace the text of one memory. "id" and "text" are required; optional "scope" changes the entry's scope (omitted keeps the current scope). The memory keeps its ID and creation time. Unknown IDs and stores without edit support are errors.

The stored entry keeps its ID, text, scope and creation time. ZeoKeel persists entries in the host's durable memory store, so they survive restarts and are shared by later conversations.`
}

// Schema implements rig.Tool.
func (t *tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Operation to perform.",
				"enum":        actions,
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Memory text (required for add).",
			},
			"scope": map[string]any{
				"type":        "string",
				"description": "Optional scope for add/list/search/clear (default workspace for add).",
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Memory ID (required for remove).",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (required for search).",
			},
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
}

// arguments is the decoded memory tool argument object.
type arguments struct {
	Action string `json:"action"`
	Text   string `json:"text"`
	Scope  string `json:"scope"`
	ID     string `json:"id"`
	Query  string `json:"query"`
}

// Invoke implements rig.Tool. Action matching is case-insensitive and trimmed;
// malformed arguments are a hard error (the run stops on a contract violation).
func (t *tool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return rig.ToolResult{}, err
	}
	var decoded arguments
	if err := decodeArguments(call, &decoded); err != nil {
		return rig.ToolResult{}, err
	}
	switch strings.ToLower(strings.TrimSpace(decoded.Action)) {
	case "add":
		return t.add(ctx, decoded), nil
	case "list":
		return t.list(ctx, decoded), nil
	case "search":
		return t.search(ctx, decoded), nil
	case "remove":
		return t.remove(ctx, decoded), nil
	case "clear":
		return t.clear(ctx, decoded), nil
	case updateAction:
		return t.update(ctx, decoded), nil
	case "":
		return errorResultf("memory: action is required (%s)", actionList()), nil
	default:
		return errorResultf("memory: unknown action %q (%s)", decoded.Action, actionList()), nil
	}
}

// add stores one memory. A model-supplied scope is passed through verbatim; an
// omitted scope stays empty so the durable store applies its configured default
// (Config.Scope, FR-25) instead of the tool hardcoding "workspace".
func (t *tool) add(ctx context.Context, decoded arguments) rig.ToolResult {
	text := strings.TrimSpace(decoded.Text)
	if text == "" {
		return errorResultf("memory: text is required for action: add")
	}
	added, err := t.store.Add(ctx, contracts.Memory{Text: text, Scope: strings.TrimSpace(decoded.Scope)})
	if err != nil {
		return errorResultf("memory: add: %v", err)
	}
	return rig.ToolResult{Content: fmt.Sprintf("added memory %s (scope=%s)", added.ID, displayScope(added.Scope))}
}

// list renders the stored memories, optionally filtered by exact scope.
func (t *tool) list(ctx context.Context, decoded arguments) rig.ToolResult {
	memories, err := t.store.List(ctx)
	if err != nil {
		return errorResultf("memory: list: %v", err)
	}
	return rig.ToolResult{Content: truncateBytes(formatMemories(filterScope(memories, decoded.Scope)), maxOutputBytes, truncatedMarker)}
}

// search runs the store's case-insensitive substring search, then applies the
// optional scope filter.
func (t *tool) search(ctx context.Context, decoded arguments) rig.ToolResult {
	query := strings.TrimSpace(decoded.Query)
	if query == "" {
		return errorResultf("memory: query is required for action: search")
	}
	memories, err := t.store.Search(ctx, query)
	if err != nil {
		return errorResultf("memory: search: %v", err)
	}
	return rig.ToolResult{Content: truncateBytes(formatMemories(filterScope(memories, decoded.Scope)), maxOutputBytes, truncatedMarker)}
}

// remove deletes one memory by ID.
func (t *tool) remove(ctx context.Context, decoded arguments) rig.ToolResult {
	id := strings.TrimSpace(decoded.ID)
	if id == "" {
		return errorResultf("memory: id is required for action: remove")
	}
	if !t.store.Remove(ctx, id) {
		return errorResultf("memory: no memory with id %q", id)
	}
	return rig.ToolResult{Content: fmt.Sprintf("removed memory %s", id)}
}

// clear removes every memory in the optional scope (all of them when omitted).
func (t *tool) clear(ctx context.Context, decoded arguments) rig.ToolResult {
	memories, err := t.store.List(ctx)
	if err != nil {
		return errorResultf("memory: clear: %v", err)
	}
	memories = filterScope(memories, decoded.Scope)
	removed := 0
	for _, memory := range memories {
		if t.store.Remove(ctx, memory.ID) {
			removed++
		}
	}
	scope := strings.TrimSpace(decoded.Scope)
	if scope == "" {
		return rig.ToolResult{Content: fmt.Sprintf("cleared %d memor(ies)", removed)}
	}
	return rig.ToolResult{Content: fmt.Sprintf("cleared %d memor(ies) in scope %s", removed, scope)}
}

// update implements the update action: id + text are required, and the store
// must implement contracts.MemoryUpdater. The success wording mirrors the other
// actions ("updated memory <id> (scope=<scope>)").
func (t *tool) update(ctx context.Context, decoded arguments) rig.ToolResult {
	id := strings.TrimSpace(decoded.ID)
	if id == "" {
		return errorResultf("memory: id is required for action: update")
	}
	text := strings.TrimSpace(decoded.Text)
	if text == "" {
		return errorResultf("memory: text is required for action: update")
	}
	updater, ok := t.store.(contracts.MemoryUpdater)
	if !ok || updater == nil {
		return errorResultf("memory: update: store does not support update")
	}
	// An empty scope keeps the entry's current scope (the store documents it).
	updated, err := updater.Update(ctx, id, text, strings.TrimSpace(decoded.Scope))
	if err != nil {
		return errorResultf("memory: update: %v", err)
	}
	return rig.ToolResult{Content: fmt.Sprintf("updated memory %s (scope=%s)", updated.ID, displayScope(updated.Scope))}
}

// --- helpers ----------------------------------------------------------------

// decodeArguments decodes one tool call's JSON arguments; empty means {}.
func decodeArguments(call rig.ToolCall, target *arguments) error {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("invalid arguments JSON: %w", err)
	}
	return nil
}

// errorResultf builds one model-visible tool failure (the run continues).
func errorResultf(format string, args ...any) rig.ToolResult {
	return rig.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}

// filterScope keeps entries whose scope matches (case-insensitive, exact); an
// empty scope keeps every entry.
func filterScope(memories []contracts.Memory, scope string) []contracts.Memory {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return memories
	}
	filtered := make([]contracts.Memory, 0, len(memories))
	for _, memory := range memories {
		if strings.EqualFold(strings.TrimSpace(memory.Scope), scope) {
			filtered = append(filtered, memory)
		}
	}
	return filtered
}

// formatMemories renders memories one per line in a deterministic order
// (creation time, then ID).
func formatMemories(memories []contracts.Memory) string {
	if len(memories) == 0 {
		return "no memories"
	}
	ordered := slices.Clone(memories)
	slices.SortStableFunc(ordered, func(a, b contracts.Memory) int {
		return cmp.Or(a.Created.Compare(b.Created), strings.Compare(a.ID, b.ID))
	})
	var builder strings.Builder
	fmt.Fprintf(&builder, "%d memor(ies):\n", len(ordered))
	for _, memory := range ordered {
		fmt.Fprintf(&builder, "- %s [%s] %s\n", memory.ID, displayScope(memory.Scope), oneLine(memory.Text))
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

// displayScope renders an empty scope as the default scope.
func displayScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return DefaultScope
	}
	return scope
}

// oneLine folds newlines so one memory occupies exactly one output line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// truncateBytes cuts content at limit bytes without splitting a UTF-8 rune and
// appends marker when the cut happened.
func truncateBytes(content string, limit int, marker string) string {
	if limit <= 0 || len(content) <= limit {
		return content
	}
	cut := limit
	for cut > 0 && !utf8StartsRune(content[cut]) {
		cut--
	}
	return content[:cut] + marker
}

// utf8StartsRune reports whether b starts a UTF-8 rune (not a continuation byte).
func utf8StartsRune(b byte) bool { return b&0xC0 != 0x80 }

// --- process-local fallback store -------------------------------------------

// memoryStore is the process-local contracts.MemoryStore used when the host binds
// no store (and by tests). It deliberately has no update half: the fallback cannot
// edit entries, which the tool reports as "store does not support update".
type memoryStore struct {
	mu    sync.Mutex
	seq   uint64
	items []contracts.Memory
}

var _ contracts.MemoryStore = (*memoryStore)(nil)

// newMemoryStore returns an empty process-local store.
func newMemoryStore() *memoryStore { return &memoryStore{} }

// List implements rig.MemoryStore, returning entries in insertion order.
func (s *memoryStore) List(context.Context) ([]contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.items), nil
}

// Add implements rig.MemoryStore: a missing ID is generated ("mem-<n>") and a
// zero Created is stamped with the current UTC time.
func (s *memoryStore) Add(_ context.Context, memory contracts.Memory) (contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(memory.ID) == "" {
		s.seq++
		memory.ID = fmt.Sprintf("mem-%d", s.seq)
	}
	if memory.Created.IsZero() {
		memory.Created = time.Now().UTC()
	}
	s.items = append(s.items, memory)
	return memory, nil
}

// Remove implements rig.MemoryStore.
func (s *memoryStore) Remove(_ context.Context, id string) bool {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, memory := range s.items {
		if memory.ID == id {
			s.items = slices.Delete(s.items, index, index+1)
			return true
		}
	}
	return false
}

// Search implements rig.MemoryStore: case-insensitive substring match over the
// text; an empty query returns everything.
func (s *memoryStore) Search(_ context.Context, query string) ([]contracts.Memory, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	s.mu.Lock()
	defer s.mu.Unlock()
	if query == "" {
		return slices.Clone(s.items), nil
	}
	matches := make([]contracts.Memory, 0, len(s.items))
	for _, memory := range s.items {
		if strings.Contains(strings.ToLower(memory.Text), query) {
			matches = append(matches, memory)
		}
	}
	return matches, nil
}
