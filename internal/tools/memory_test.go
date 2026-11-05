package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
)

// newMemoryTestStore opens a store with two workspaces and returns both ids.
func newMemoryTestStore(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	alpha, err := st.CreateWorkspace("alpha", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace alpha: %v", err)
	}
	beta, err := st.CreateWorkspace("beta", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace beta: %v", err)
	}
	return st, alpha.ID, beta.ID
}

// memoryToolFor returns a tool whose scope is workspaceID.
func memoryToolFor(st *store.Store, workspaceID string) *MemoryTool {
	return NewMemoryTool(MemoryConfig{
		Store:       st,
		WorkspaceID: func() string { return workspaceID },
	})
}

// invokeMemory runs one invocation and decodes the JSON result.
func invokeMemory(t *testing.T, tool *MemoryTool, args string) MemoryResult {
	t.Helper()
	out, err := tool.Invoke(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Invoke(%s): %v", args, err)
	}
	var got MemoryResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode result %q: %v", out, err)
	}
	return got
}

// TestMemoryToolAddListSearch covers the write and read actions plus the
// workspace scope and limit handling.
func TestMemoryToolAddListSearch(t *testing.T) {
	st, alpha, beta := newMemoryTestStore(t)
	tool := memoryToolFor(st, alpha)

	added := invokeMemory(t, tool, `{"action":"add","content":"  The release branch is release/2026.  "}`)
	if added.Kind != "memory" || added.Action != "add" || added.Count != 1 || len(added.Memories) != 1 {
		t.Fatalf("add result = %+v", added)
	}
	if added.Memories[0].Content != "The release branch is release/2026." {
		t.Fatalf("added content = %q, want trimmed", added.Memories[0].Content)
	}
	if added.Memories[0].WorkspaceID != alpha {
		t.Fatalf("added workspace = %q, want %q", added.Memories[0].WorkspaceID, alpha)
	}
	id := added.Memories[0].ID
	if id == "" {
		t.Fatal("add returned an empty id")
	}

	invokeMemory(t, tool, `{"action":"add","content":"Run make lint before committing."}`)

	listed := invokeMemory(t, tool, `{"action":"list"}`)
	if listed.Count != 2 || len(listed.Memories) != 2 {
		t.Fatalf("list result = %+v, want 2 memories", listed)
	}
	limited := invokeMemory(t, tool, `{"action":"list","limit":1}`)
	if limited.Count != 1 || len(limited.Memories) != 1 {
		t.Fatalf("limited list = %+v, want 1 memory", limited)
	}

	found := invokeMemory(t, tool, `{"action":"search","query":"release"}`)
	if found.Count != 1 || len(found.Memories) != 1 || found.Memories[0].ID != id {
		t.Fatalf("search result = %+v, want only %q", found, id)
	}
	none := invokeMemory(t, tool, `{"action":"search","query":"missing"}`)
	if none.Count != 0 || len(none.Memories) != 0 {
		t.Fatalf("search for missing content = %+v, want empty", none)
	}

	// A second workspace sees none of alpha's rows and can start its own.
	other := memoryToolFor(st, beta)
	if got := invokeMemory(t, other, `{"action":"list"}`); got.Count != 0 {
		t.Fatalf("beta list = %+v, want empty", got)
	}
	invokeMemory(t, other, `{"action":"add","content":"Beta note"}`)
	if got := invokeMemory(t, other, `{"action":"list"}`); got.Count != 1 {
		t.Fatalf("beta list after add = %+v, want 1", got)
	}
	if got := invokeMemory(t, tool, `{"action":"list"}`); got.Count != 2 {
		t.Fatalf("alpha list after beta add = %+v, want 2", got)
	}
}

// TestMemoryToolUpdateDeleteClear covers the mutating actions and the
// cross-workspace rejection.
func TestMemoryToolUpdateDeleteClear(t *testing.T) {
	st, alpha, beta := newMemoryTestStore(t)
	tool := memoryToolFor(st, alpha)
	other := memoryToolFor(st, beta)

	added := invokeMemory(t, tool, `{"action":"add","content":"Original note"}`)
	id := added.Memories[0].ID

	updated := invokeMemory(t, tool, `{"action":"update","id":"`+id+`","content":"Updated note"}`)
	if updated.Action != "update" || updated.Count != 1 || len(updated.Memories) != 1 {
		t.Fatalf("update result = %+v", updated)
	}
	if updated.Memories[0].Content != "Updated note" {
		t.Fatalf("updated content = %q", updated.Memories[0].Content)
	}

	// Another workspace cannot update or delete this row.
	if _, err := other.Invoke(context.Background(),
		json.RawMessage(`{"action":"update","id":"`+id+`","content":"hijack"}`)); err == nil {
		t.Fatal("cross-workspace update succeeded")
	}
	if _, err := other.Invoke(context.Background(),
		json.RawMessage(`{"action":"delete","id":"`+id+`"}`)); err == nil {
		t.Fatal("cross-workspace delete succeeded")
	}
	if got := invokeMemory(t, tool, `{"action":"list"}`); got.Count != 1 {
		t.Fatalf("cross-workspace mutations changed alpha: %+v", got)
	}

	invokeMemory(t, tool, `{"action":"add","content":"Second note"}`)
	deleted := invokeMemory(t, tool, `{"action":"delete","id":"`+id+`"}`)
	if deleted.Action != "delete" || deleted.Count != 1 || len(deleted.Memories) != 1 {
		t.Fatalf("delete result = %+v", deleted)
	}
	if deleted.Memories[0].ID != id {
		t.Fatalf("deleted memory id = %q, want %q", deleted.Memories[0].ID, id)
	}

	// Beta's note must survive alpha's clear.
	invokeMemory(t, other, `{"action":"add","content":"Beta note"}`)
	cleared := invokeMemory(t, tool, `{"action":"clear"}`)
	if cleared.Action != "clear" || cleared.Count != 1 {
		t.Fatalf("clear result = %+v, want 1 cleared", cleared)
	}
	if got := invokeMemory(t, tool, `{"action":"list"}`); got.Count != 0 {
		t.Fatalf("alpha list after clear = %+v, want empty", got)
	}
	if got := invokeMemory(t, other, `{"action":"list"}`); got.Count != 1 {
		t.Fatalf("beta list after alpha clear = %+v, want 1", got)
	}
}

// TestMemoryToolErrors covers clear error messages for bad arguments and
// unusable configuration.
func TestMemoryToolErrors(t *testing.T) {
	st, alpha, _ := newMemoryTestStore(t)
	tool := memoryToolFor(st, alpha)
	ctx := context.Background()

	cases := []struct {
		name string
		tool *MemoryTool
		args string
		want string
	}{
		{"invalid json", tool, `{`, "invalid arguments"},
		{"missing action", tool, `{}`, `"action" is required`},
		{"unknown action", tool, `{"action":"rename"}`, "unknown action"},
		{"empty add", tool, `{"action":"add"}`, `"content" is required`},
		{"empty update id", tool, `{"action":"update","content":"x"}`, `"id" is required`},
		{"empty update content", tool, `{"action":"update","id":"x"}`, `"content" is required`},
		{"empty delete id", tool, `{"action":"delete"}`, `"id" is required`},
		{"empty search", tool, `{"action":"search"}`, `"query" is required`},
		{"unknown update id", tool, `{"action":"update","id":"nope","content":"x"}`, "no memory with id"},
		{"unknown delete id", tool, `{"action":"delete","id":"nope"}`, "no memory with id"},
		{"no store", NewMemoryTool(MemoryConfig{WorkspaceID: func() string { return alpha }}), `{"action":"list"}`, "no store configured"},
		{"no workspace", NewMemoryTool(MemoryConfig{Store: st}), `{"action":"list"}`, "no workspace scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.tool.Invoke(ctx, json.RawMessage(tc.args))
			if err == nil {
				t.Fatalf("Invoke(%s) succeeded, want an error", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}

	// An oversized add is rejected by the store and surfaced with context.
	tooLarge := strings.Repeat("x", store.MaxMemoryContentBytes+1)
	_, err := tool.Invoke(ctx, json.RawMessage(`{"action":"add","content":"`+tooLarge+`"}`))
	if err == nil || !strings.Contains(err.Error(), "maximum is") {
		t.Fatalf("oversized add error = %v, want a size-limit error", err)
	}
}

// TestMemoryToolOutputCap proves a large listing is trimmed below 64 KiB and
// flagged truncated instead of returning an unbounded result.
func TestMemoryToolOutputCap(t *testing.T) {
	st, alpha, _ := newMemoryTestStore(t)
	tool := memoryToolFor(st, alpha)
	body := strings.Repeat("x", store.MaxMemoryContentBytes)
	for i := 0; i < 12; i++ {
		invokeMemory(t, tool, `{"action":"add","content":"`+body+`"}`)
	}

	out, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"list","limit":200}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) > DefaultMaxOutputBytes {
		t.Fatalf("list output = %d bytes, want <= %d", len(out), DefaultMaxOutputBytes)
	}
	var got MemoryResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode capped result: %v", err)
	}
	if !got.Truncated {
		t.Fatalf("capped result is not marked truncated: %+v", got)
	}
	if got.Count != len(got.Memories) {
		t.Fatalf("count = %d, memories = %d", got.Count, len(got.Memories))
	}
	if len(got.Memories) == 0 {
		t.Fatal("capped result dropped every memory")
	}
	if !strings.Contains(got.Message, "truncated") {
		t.Fatalf("capped result message = %q, want a truncation note", got.Message)
	}

	// A small result is not marked truncated.
	small := invokeMemory(t, tool, `{"action":"list","limit":1}`)
	if small.Truncated {
		t.Fatalf("small result marked truncated: %+v", small)
	}
}
