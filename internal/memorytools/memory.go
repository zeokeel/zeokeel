// Package memorytools exposes the product's memory tool to the model: the Rig
// memory tool (github.com/zeokeel/zeokeel-rig/tools/memory) plus the optional
// update action the Rig tool cannot provide.
//
// The Rig tool decides add/list/search/remove/clear from rig.MemoryStore, which
// has no update half. The product store additionally implements
// contracts.MemoryUpdater, so this package wraps the Rig tool under the same
// stable name ("memory"), extends the description/schema with "update" and
// delegates every other action verbatim. Keeping one tool with the Rig name
// means the toolkit registry (and therefore the model-facing tool list) is
// unchanged apart from the new action.
package memorytools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	rigmemory "github.com/zeokeel/zeokeel-rig/tools/memory"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// updateAction is the action this wrapper handles instead of the Rig tool.
const updateAction = "update"

// delegatedActions are the actions the Rig memory tool implements and this
// wrapper delegates. They are listed in the tool's action order.
var delegatedActions = []string{"add", "list", "search", "remove", "clear"}

// actionList renders the tool's complete action list for error messages (in
// schema order). The delegated list alone would omit "update".
func actionList() string {
	return strings.Join(delegatedActions, ", ") + ", " + updateAction
}

// Tools returns the product memory tool bound to store: exactly one tool named
// "memory", the same name the Rig memory tool uses so the registry stays
// stable. store may be nil, which mirrors rigmemory.Tools(nil): the Rig tool
// falls back to a fresh process-local store. That fallback has no update half,
// so its update action reports the unsupported-store error.
func Tools(store contracts.MemoryStore) []rig.Tool {
	if store == nil {
		store = rigmemory.NewMemoryStore()
	}
	inner := rigmemory.Tools(store)[0]
	return []rig.Tool{&tool{store: store, inner: inner}}
}

// tool wraps the Rig memory tool. Add/list/search/remove/clear are delegated to
// inner unchanged; update runs against the store's optional MemoryUpdater half.
type tool struct {
	store contracts.MemoryStore
	inner rig.Tool
}

var _ rig.Tool = (*tool)(nil)

// Name mirrors the Rig tool name ("memory") so registration names stay stable.
func (t *tool) Name() string { return t.inner.Name() }

// Description is the Rig description extended with the update action. The
// bullet is inserted into the Rig action list (before the trailing store notes)
// so the delegated actions keep their exact wording; a Rig description without
// that list gets the bullet appended.
func (t *tool) Description() string {
	const storeNotesMarker = "\n\nThe stored entry keeps"
	const updateBullet = `- update: replace the text of one memory. "id" and "text" are required; optional "scope" changes the entry's scope (omitted keeps the current scope). The memory keeps its ID and creation time. Unknown IDs and stores without edit support are errors.`
	description := t.inner.Description()
	if index := strings.Index(description, storeNotesMarker); index >= 0 {
		return description[:index] + "\n" + updateBullet + description[index:]
	}
	return description + "\n" + updateBullet
}

// Schema is the Rig schema extended with the update action and its id/text
// parameters (the Rig schema already declares both for remove/add). The Rig
// schema is deep-copied, never mutated: the wrapped tool may be shared and the
// same call happens for every model turn.
func (t *tool) Schema() map[string]any {
	schema, ok := cloneSchema(t.inner.Schema()).(map[string]any)
	if !ok {
		schema = map[string]any{"type": "object"}
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	action, ok := properties["action"].(map[string]any)
	if !ok {
		action = map[string]any{"type": "string", "description": "Operation to perform."}
		properties["action"] = action
	}
	addEnumValue(action, updateAction)
	ensureStringProperty(properties, "id", "Memory ID (required for remove).")
	ensureStringProperty(properties, "text", "Memory text (required for add).")
	return schema
}

// Invoke implements rig.Tool. "update" is handled here; the five Rig actions
// are delegated verbatim. Malformed arguments are also left to the Rig tool so
// its message stays byte-identical.
func (t *tool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return rig.ToolResult{}, err
	}
	arguments := arguments{}
	if err := decodeArguments(call, &arguments); err != nil {
		return t.inner.Invoke(ctx, call)
	}
	switch strings.ToLower(strings.TrimSpace(arguments.Action)) {
	case updateAction:
		return t.update(ctx, arguments), nil
	case "":
		return errorResultf("memory: action is required (%s)", actionList()), nil
	case "add", "list", "search", "remove", "clear":
		return t.inner.Invoke(ctx, call)
	default:
		return errorResultf("memory: unknown action %q (%s)", arguments.Action, actionList()), nil
	}
}

// arguments is the decoded memory tool arguments (the same JSON fields the Rig
// tool decodes).
type arguments struct {
	Action string `json:"action"`
	Text   string `json:"text"`
	Scope  string `json:"scope"`
	ID     string `json:"id"`
	Query  string `json:"query"`
}

// decodeArguments decodes one tool call's JSON arguments; an empty argument
// string is the empty object, like the Rig tool's decoder.
func decodeArguments(call rig.ToolCall, target *arguments) error {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	return json.Unmarshal([]byte(raw), target)
}

// update implements the update action: id + text are required, the store must
// implement contracts.MemoryUpdater, and the result mirrors the Rig tool's
// success style ("updated memory <id> (scope=<scope>)").
func (t *tool) update(ctx context.Context, arguments arguments) rig.ToolResult {
	id := strings.TrimSpace(arguments.ID)
	if id == "" {
		return errorResultf("memory: id is required for action: update")
	}
	text := strings.TrimSpace(arguments.Text)
	if text == "" {
		return errorResultf("memory: text is required for action: update")
	}
	updater, ok := t.store.(contracts.MemoryUpdater)
	if !ok || updater == nil {
		return errorResultf("memory: update: store does not support update")
	}
	// An empty scope keeps the entry's current scope (the store documents it).
	updated, err := updater.Update(ctx, id, text, strings.TrimSpace(arguments.Scope))
	if err != nil {
		return errorResultf("memory: update: %v", err)
	}
	return rig.ToolResult{Content: fmt.Sprintf("updated memory %s (scope=%s)", updated.ID, displayScope(updated.Scope))}
}

// displayScope renders an empty scope as the Rig tool's default scope, matching
// the Rig memory tool's model-visible formatting.
func displayScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return rigmemory.DefaultScope
	}
	return scope
}

// --- result/schema helpers --------------------------------------------------
//
// rig.ToolResult{Content: ..., IsError: true} is exactly what the Rig tools'
// internal toolutil.TextResult/ErrorResult helpers build; that package is
// module-internal, so the two constructors are mirrored here.

// errorResultf builds one model-visible tool failure, like toolutil.ErrorResultf.
func errorResultf(format string, args ...any) rig.ToolResult {
	return rig.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}

// addEnumValue appends value to one schema property's enum unless it is already
// there, tolerating the []string and []any spellings JSON schema maps use.
func addEnumValue(property map[string]any, value string) {
	switch enum := property["enum"].(type) {
	case []string:
		for _, existing := range enum {
			if strings.EqualFold(existing, value) {
				return
			}
		}
		property["enum"] = append(enum, value)
	case []any:
		for _, existing := range enum {
			if text, ok := existing.(string); ok && strings.EqualFold(text, value) {
				return
			}
		}
		property["enum"] = append(enum, value)
	default:
		property["enum"] = []string{value}
	}
}

// ensureStringProperty adds a string property unless the schema already
// declares it (the Rig schema already declares the fields it uses).
func ensureStringProperty(properties map[string]any, name, description string) {
	if _, exists := properties[name]; exists {
		return
	}
	properties[name] = map[string]any{"type": "string", "description": description}
}

// cloneSchema deep-copies the JSON-schema-shaped value so extending the copy
// cannot mutate the wrapped tool's schema. Values the tool may return are the
// JSON schema primitives: maps, slices of any/string and scalars.
func cloneSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneSchema(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneSchema(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
