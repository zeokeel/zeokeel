package memorytools_test

// Tests for the product memory tool: one stable "memory" tool that wraps the
// Rig memory tool, delegates add/list/search/remove/clear verbatim and adds the
// update action over contracts.MemoryUpdater.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigmemory "github.com/zeokeel/zeokeel-rig/tools/memory"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/memorytools"
)

// updateCall is one recorded MemoryUpdater.Update invocation.
type updateCall struct {
	id    string
	text  string
	scope string
}

// fakeUpdater is a Rig memory store with the optional MemoryUpdater half: the
// embedded store handles the delegated actions, Update records the call.
type fakeUpdater struct {
	*rigmemory.MemoryStore
	mu     sync.Mutex
	calls  []updateCall
	result contracts.Memory
	err    error
}

func newFakeUpdater() *fakeUpdater {
	return &fakeUpdater{MemoryStore: rigmemory.NewMemoryStore()}
}

func (f *fakeUpdater) Update(_ context.Context, id, text, scope string) (contracts.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, updateCall{id: id, text: text, scope: scope})
	if f.err != nil {
		return contracts.Memory{}, f.err
	}
	if f.result.ID != "" {
		return f.result, nil
	}
	return contracts.Memory{ID: id, Text: text, Scope: scope, Created: time.Unix(1700000000, 0)}, nil
}

func (f *fakeUpdater) updates() []updateCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]updateCall(nil), f.calls...)
}

// invoke runs the single product memory tool with one JSON argument object.
func invoke(t *testing.T, tool rig.Tool, arguments string) rig.ToolResult {
	t.Helper()
	result, err := tool.Invoke(context.Background(), rig.ToolCall{ID: "call-1", Name: tool.Name(), Arguments: arguments})
	if err != nil {
		t.Fatalf("Invoke(%s): %v", arguments, err)
	}
	return result
}

func TestToolsReturnsOneStableMemoryTool(t *testing.T) {
	for name, store := range map[string]contracts.MemoryStore{
		"rig store": rigmemory.NewMemoryStore(),
		"nil store": nil,
	} {
		tools := memorytools.Tools(store)
		if len(tools) != 1 {
			t.Fatalf("%s: Tools = %d tools, want exactly 1", name, len(tools))
		}
		if got := tools[0].Name(); got != rigmemory.ToolName {
			t.Fatalf("%s: tool name = %q, want the rig name %q", name, got, rigmemory.ToolName)
		}
		if rigmemory.ToolName != "memory" {
			t.Fatalf("rig memory tool name = %q, want the stable product name memory", rigmemory.ToolName)
		}
	}
}

// TestDescriptionExtendsRigActions pins the extended copy: the Rig action
// bullets stay and the update bullet joins the list.
func TestDescriptionExtendsRigActions(t *testing.T) {
	tool := memorytools.Tools(rigmemory.NewMemoryStore())[0]
	description := tool.Description()
	for _, fragment := range []string{
		"Manage the agent's memory entries through a host-provided store.",
		"- add:", "- list:", "- search:", "- remove:", "- clear:", "- update:",
		`optional "scope" changes the entry's scope`,
	} {
		if !strings.Contains(description, fragment) {
			t.Fatalf("description does not contain %q:\n%s", fragment, description)
		}
	}
	updateAt := strings.Index(description, "- update:")
	if clearAt := strings.Index(description, "- clear:"); clearAt < 0 || updateAt < clearAt {
		t.Fatalf("update bullet at %d, want it after the clear bullet at %d", updateAt, clearAt)
	}
	// The store notes keep trailing the action list.
	if updateAt > strings.Index(description, "The stored entry keeps") {
		t.Fatal("update bullet was appended after the store notes")
	}
}

func TestSchemaExtendsUpdateAction(t *testing.T) {
	tool := memorytools.Tools(rigmemory.NewMemoryStore())[0]
	schema := tool.Schema()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("schema = %+v, want an object without additional properties", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %T, want map[string]any", schema["properties"])
	}
	action, ok := properties["action"].(map[string]any)
	if !ok {
		t.Fatalf("action property = %T", properties["action"])
	}
	enum, ok := action["enum"].([]string)
	if !ok {
		t.Fatalf("action enum = %T, want []string", action["enum"])
	}
	wanted := []string{"add", "list", "search", "remove", "clear", "update"}
	if len(enum) != len(wanted) {
		t.Fatalf("action enum = %v, want %v", enum, wanted)
	}
	for index, action := range wanted {
		if enum[index] != action {
			t.Fatalf("action enum = %v, want %v", enum, wanted)
		}
	}
	for _, name := range []string{"id", "text", "scope", "query"} {
		property, ok := properties[name].(map[string]any)
		if !ok || property["type"] != "string" {
			t.Fatalf("property %q = %+v, want a string property", name, properties[name])
		}
	}
	// The extensions never mutate the wrapped schema: every call is a fresh
	// deep copy, so mutating one result cannot leak into the next one.
	properties["action"].(map[string]any)["enum"] = []string{"broken"}
	properties["id"].(map[string]any)["type"] = "integer"
	again := tool.Schema()
	againProperties := again["properties"].(map[string]any)
	againEnum := againProperties["action"].(map[string]any)["enum"].([]string)
	if len(againEnum) != len(wanted) || againEnum[len(againEnum)-1] != "update" {
		t.Fatalf("second schema action enum = %v, want a fresh extended enum", againEnum)
	}
	if againProperties["id"].(map[string]any)["type"] != "string" {
		t.Fatalf("schema mutation leaked between calls: %+v", againProperties["id"])
	}
}

func TestDelegatesRigActions(t *testing.T) {
	tool := memorytools.Tools(rigmemory.NewMemoryStore())[0]

	if result := invoke(t, tool, `{"action":"add","text":"alpha","scope":"global"}`); result.IsError || !strings.HasPrefix(result.Content, "added memory ") {
		t.Fatalf("add = %+v, want the rig success text", result)
	}
	if result := invoke(t, tool, `{"action":"add","text":"beta"}`); result.IsError {
		t.Fatalf("add = %+v", result)
	}
	list := invoke(t, tool, `{"action":"list"}`)
	if list.IsError || !strings.Contains(list.Content, "alpha") || !strings.Contains(list.Content, "beta") {
		t.Fatalf("list = %+v, want both memories", list)
	}
	search := invoke(t, tool, `{"action":"search","query":"ALP"}`)
	if search.IsError || !strings.Contains(search.Content, "alpha") || strings.Contains(search.Content, "beta") {
		t.Fatalf("search = %+v, want only alpha", search)
	}
	if result := invoke(t, tool, `{"action":"remove","id":"mem-1"}`); result.IsError || result.Content != "removed memory mem-1" {
		t.Fatalf("remove = %+v", result)
	}
	if result := invoke(t, tool, `{"action":"clear"}`); result.IsError || result.Content != "cleared 1 memor(ies)" {
		t.Fatalf("clear = %+v", result)
	}
	// Unknown/empty actions keep the rig error style but list the new action.
	if result := invoke(t, tool, `{"action":"bogus"}`); !result.IsError ||
		!strings.Contains(result.Content, `unknown action "bogus"`) ||
		!strings.Contains(result.Content, "update") {
		t.Fatalf("unknown action = %+v", result)
	}
	if result := invoke(t, tool, `{"action":""}`); !result.IsError ||
		!strings.HasPrefix(result.Content, "memory: action is required") ||
		!strings.Contains(result.Content, "update") {
		t.Fatalf("empty action = %+v", result)
	}
	// Malformed arguments are delegated to the rig tool and returned as an
	// error (not a model-visible IsError result), matching its behavior.
	if _, err := tool.Invoke(context.Background(), rig.ToolCall{Arguments: "{"}); err == nil {
		t.Fatal("malformed arguments returned no error")
	} else if !strings.Contains(err.Error(), "invalid arguments JSON") {
		t.Fatalf("malformed arguments error = %v", err)
	}
}

func TestUpdateSuccess(t *testing.T) {
	store := newFakeUpdater()
	store.result = contracts.Memory{ID: "mem-1", Text: "new text", Scope: "workspace", Created: time.Unix(1700000000, 0)}
	tool := memorytools.Tools(store)[0]

	result := invoke(t, tool, `{"action":"update","id":"mem-1","text":"new text","scope":" WORKSPACE "}`)
	if result.IsError {
		t.Fatalf("update returned an error result: %+v", result)
	}
	if want := "updated memory mem-1 (scope=workspace)"; result.Content != want {
		t.Fatalf("update content = %q, want %q", result.Content, want)
	}
	calls := store.updates()
	if len(calls) != 1 || calls[0].id != "mem-1" || calls[0].text != "new text" || calls[0].scope != "WORKSPACE" {
		t.Fatalf("Update calls = %+v, want one trimmed-scope call", calls)
	}

	// Action matching is case-insensitive and trimmed; an omitted scope is
	// passed through as empty (the store keeps the entry's current scope).
	store2 := newFakeUpdater()
	tool2 := memorytools.Tools(store2)[0]
	if result := invoke(t, tool2, `{"action":" UPDATE ","id":"mem-2","text":"changed"}`); result.IsError {
		t.Fatalf("update = %+v", result)
	}
	if calls := store2.updates(); len(calls) != 1 || calls[0].scope != "" {
		t.Fatalf("Update calls = %+v, want empty scope", calls)
	}
}

func TestUpdateValidation(t *testing.T) {
	store := newFakeUpdater()
	tool := memorytools.Tools(store)[0]

	if result := invoke(t, tool, `{"action":"update","text":"x"}`); !result.IsError ||
		result.Content != "memory: id is required for action: update" {
		t.Fatalf("missing id = %+v", result)
	}
	if result := invoke(t, tool, `{"action":"update","id":"mem-1"}`); !result.IsError ||
		result.Content != "memory: text is required for action: update" {
		t.Fatalf("missing text = %+v", result)
	}
	if result := invoke(t, tool, `{"action":"update","id":"  ","text":"  "}`); !result.IsError {
		t.Fatalf("blank id/text = %+v, want an error result", result)
	}
	if calls := store.updates(); len(calls) != 0 {
		t.Fatalf("invalid updates reached the store: %+v", calls)
	}
}

func TestUpdateUnsupportedStore(t *testing.T) {
	// A store without the optional MemoryUpdater half reports the documented
	// readable error instead of panicking.
	tool := memorytools.Tools(rigmemory.NewMemoryStore())[0]
	result := invoke(t, tool, `{"action":"update","id":"mem-1","text":"x"}`)
	if !result.IsError || result.Content != "memory: update: store does not support update" {
		t.Fatalf("update on a read-only store = %+v", result)
	}

	// Tools(nil) uses the rig process-local store, which cannot update either.
	nilTool := memorytools.Tools(nil)[0]
	if result := invoke(t, nilTool, `{"action":"update","id":"mem-1","text":"x"}`); !result.IsError ||
		result.Content != "memory: update: store does not support update" {
		t.Fatalf("update on the nil-store fallback = %+v", result)
	}
}

func TestUpdateStoreError(t *testing.T) {
	store := newFakeUpdater()
	store.err = errors.New("no memory with id \"missing\"")
	tool := memorytools.Tools(store)[0]
	result := invoke(t, tool, `{"action":"update","id":"missing","text":"x"}`)
	if !result.IsError {
		t.Fatalf("failed update = %+v, want an error result", result)
	}
	if want := `memory: update: no memory with id "missing"`; result.Content != want {
		t.Fatalf("failed update content = %q, want %q", result.Content, want)
	}
}

func TestUpdateDisplayScopeFallsBackToRigDefault(t *testing.T) {
	store := newFakeUpdater()
	store.result = contracts.Memory{ID: "mem-1", Text: "new", Created: time.Unix(1700000000, 0)}
	tool := memorytools.Tools(store)[0]
	result := invoke(t, tool, `{"action":"update","id":"mem-1","text":"new"}`)
	if want := "updated memory mem-1 (scope=" + rigmemory.DefaultScope + ")"; result.Content != want {
		t.Fatalf("update content = %q, want %q", result.Content, want)
	}
}

// recordingStore records the scope passed to Add and reports a configured
// default: the product wrapper must leave the scope empty when the model omits
// it, so the durable store's configured default (FR-25) applies.
type recordingStore struct {
	*rigmemory.MemoryStore
	mu    sync.Mutex
	scope []string
}

func newRecordingStore() *recordingStore {
	return &recordingStore{MemoryStore: rigmemory.NewMemoryStore()}
}

func (r *recordingStore) Add(ctx context.Context, memory contracts.Memory) (contracts.Memory, error) {
	r.mu.Lock()
	r.scope = append(r.scope, memory.Scope)
	r.mu.Unlock()
	if strings.TrimSpace(memory.Scope) == "" {
		memory.Scope = contracts.MemoryScopeGlobal
	}
	return r.MemoryStore.Add(ctx, memory)
}

func (r *recordingStore) scopes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.scope...)
}

func TestAddWithoutScopeLeavesTheConfiguredDefaultToTheStore(t *testing.T) {
	store := newRecordingStore()
	tool := memorytools.Tools(store)[0]
	result := invoke(t, tool, `{"action":"add","text":"uses the configured default"}`)
	if result.IsError || !strings.Contains(result.Content, "scope=global") {
		t.Fatalf("add without scope = %+v, want the store default in the result", result)
	}
	if got := store.scopes(); len(got) != 1 || got[0] != "" {
		t.Fatalf("Add scope = %v, want the empty scope so the store applies Config.Scope", got)
	}
	// An explicit scope still reaches the store verbatim.
	if result := invoke(t, tool, `{"action":"add","text":"explicit","scope":"workspace"}`); result.IsError {
		t.Fatalf("add with scope = %+v", result)
	}
	if got := store.scopes(); len(got) != 2 || got[1] != "workspace" {
		t.Fatalf("Add scopes = %v, want the explicit workspace scope", got)
	}
}

func TestDescriptionIsDurableNotProcessLocal(t *testing.T) {
	description := memorytools.Tools(rigmemory.NewMemoryStore())[0].Description()
	if strings.Contains(description, "process-local") {
		t.Fatalf("model-facing description still claims a process-local store:\n%s", description)
	}
	if !strings.Contains(description, "durable") {
		t.Fatalf("model-facing description does not mention durability:\n%s", description)
	}
}
