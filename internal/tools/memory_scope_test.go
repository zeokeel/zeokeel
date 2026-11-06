package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
)

// memoryToolWithModes builds a memory tool with explicit write-mode resolvers.
func memoryToolWithModes(st *store.Store, workspaceID string, workspaceMode, globalMode string) *MemoryTool {
	return NewMemoryTool(MemoryConfig{
		Store:       st,
		WorkspaceID: func() string { return workspaceID },
		Modes:       func() (string, string) { return workspaceMode, globalMode },
	})
}

// invokeMemoryErr runs one invocation and returns its error (for the rejection
// paths).
func invokeMemoryErr(t *testing.T, tool *MemoryTool, args string) error {
	t.Helper()
	_, err := tool.Invoke(context.Background(), json.RawMessage(args))
	return err
}

// TestMemoryToolScopeAndModeMatrix is the §4 write matrix: scope defaults to
// workspace, the configured mode decides active/pending/off for each scope, the
// tool marks model writes as source=auto, and the result distinguishes scope,
// status and kind.
func TestMemoryToolScopeAndModeMatrix(t *testing.T) {
	t.Run("workspace auto writes active", func(t *testing.T) {
		st, alpha, _ := newMemoryTestStore(t)
		tool := memoryToolWithModes(st, alpha, "auto", "suggest")
		got := invokeMemory(t, tool, `{"action":"add","content":"Run make lint before committing."}`)
		if got.Count != 1 || len(got.Memories) != 1 {
			t.Fatalf("add = %+v", got)
		}
		m := got.Memories[0]
		if m.Scope != store.MemoryScopeWorkspace || m.Status != store.MemoryStatusActive ||
			m.Source != store.MemorySourceAuto || m.Kind != store.MemoryKindProject {
			t.Fatalf("workspace auto memory = %+v, want workspace/active/auto/project", m)
		}
		if m.WorkspaceID != alpha {
			t.Fatalf("workspace id = %q, want %q", m.WorkspaceID, alpha)
		}
		if got.Scope != store.MemoryScopeWorkspace || got.Status != store.MemoryStatusActive {
			t.Fatalf("result scope/status = %q/%q", got.Scope, got.Status)
		}
	})

	t.Run("workspace suggest writes pending", func(t *testing.T) {
		st, alpha, _ := newMemoryTestStore(t)
		tool := memoryToolWithModes(st, alpha, "suggest", "suggest")
		got := invokeMemory(t, tool, `{"action":"add","content":"Prefer table tests."}`)
		m := got.Memories[0]
		if m.Status != store.MemoryStatusPending || m.Scope != store.MemoryScopeWorkspace {
			t.Fatalf("workspace suggest memory = %+v, want workspace/pending", m)
		}
		if !strings.Contains(strings.ToLower(got.Message), "suggestion") {
			t.Fatalf("suggest message = %q, want it to mention the pending suggestion", got.Message)
		}
	})

	t.Run("workspace off rejects", func(t *testing.T) {
		st, alpha, _ := newMemoryTestStore(t)
		tool := memoryToolWithModes(st, alpha, "off", "auto")
		err := invokeMemoryErr(t, tool, `{"action":"add","content":"should not persist"}`)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "disabled") {
			t.Fatalf("workspace off add error = %v, want a stable disabled error", err)
		}
		list, lerr := st.ListMemoriesScoped(store.MemoryScopeWorkspace, alpha, "", 0)
		if lerr != nil {
			t.Fatalf("ListMemoriesScoped: %v", lerr)
		}
		if len(list) != 0 {
			t.Fatalf("workspace off add persisted %+v", list)
		}
	})

	t.Run("global auto writes active without workspace", func(t *testing.T) {
		st, alpha, _ := newMemoryTestStore(t)
		tool := memoryToolWithModes(st, alpha, "auto", "auto")
		got := invokeMemory(t, tool, `{"action":"add","scope":"global","content":"Prefer concise answers."}`)
		m := got.Memories[0]
		if m.Scope != store.MemoryScopeGlobal || m.Status != store.MemoryStatusActive {
			t.Fatalf("global auto memory = %+v, want global/active", m)
		}
		if m.WorkspaceID != "" {
			t.Fatalf("global memory workspace id = %q, want empty", m.WorkspaceID)
		}
		list, err := st.ListMemoriesScoped(store.MemoryScopeGlobal, "", store.MemoryStatusActive, 0)
		if err != nil || len(list) != 1 {
			t.Fatalf("global store rows = %+v, %v; want the written row", list, err)
		}
	})

	t.Run("global suggest writes pending and says so", func(t *testing.T) {
		st, alpha, _ := newMemoryTestStore(t)
		tool := memoryToolWithModes(st, alpha, "auto", "suggest")
		got := invokeMemory(t, tool, `{"action":"add","scope":"global","content":"User works in UTC+8."}`)
		m := got.Memories[0]
		if m.Scope != store.MemoryScopeGlobal || m.Status != store.MemoryStatusPending {
			t.Fatalf("global suggest memory = %+v, want global/pending", m)
		}
		if !strings.Contains(strings.ToLower(got.Message), "pending review") {
			t.Fatalf("global suggest message = %q, want pending-review guidance", got.Message)
		}
	})

	t.Run("global off rejects", func(t *testing.T) {
		st, alpha, _ := newMemoryTestStore(t)
		tool := memoryToolWithModes(st, alpha, "auto", "off")
		err := invokeMemoryErr(t, tool, `{"action":"add","scope":"global","content":"should not persist"}`)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "disabled") {
			t.Fatalf("global off add error = %v, want a stable disabled error", err)
		}
	})

	t.Run("invalid scope rejected", func(t *testing.T) {
		st, alpha, _ := newMemoryTestStore(t)
		tool := memoryToolWithModes(st, alpha, "auto", "auto")
		err := invokeMemoryErr(t, tool, `{"action":"add","scope":"session","content":"nope"}`)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "scope") {
			t.Fatalf("invalid scope error = %v, want a scope rejection", err)
		}
	})

	t.Run("kind is honored", func(t *testing.T) {
		st, alpha, _ := newMemoryTestStore(t)
		tool := memoryToolWithModes(st, alpha, "auto", "auto")
		got := invokeMemory(t, tool, `{"action":"add","content":"Use t.Context() in tests.","kind":"user"}`)
		if got.Memories[0].Kind != store.MemoryKindUser {
			t.Fatalf("kind = %q, want user", got.Memories[0].Kind)
		}
		err := invokeMemoryErr(t, tool, `{"action":"add","content":"x","kind":"bogus"}`)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "kind") {
			t.Fatalf("invalid kind error = %v, want a kind rejection", err)
		}
	})
}

// TestMemoryToolListIncludesPending pins the list contract: a scope's list
// shows active and pending rows with their status, newest first, and never
// another scope's rows.
func TestMemoryToolListIncludesPending(t *testing.T) {
	st, alpha, beta := newMemoryTestStore(t)
	tool := memoryToolWithModes(st, alpha, "auto", "suggest")

	active := invokeMemory(t, tool, `{"action":"add","content":"Active workspace note."}`)
	// Flip a row to pending to model a suggest-mode write.
	if _, err := st.SetMemoryStatus(active.Memories[0].ID, store.MemoryStatusPending); err != nil {
		t.Fatalf("SetMemoryStatus: %v", err)
	}
	pending := invokeMemory(t, tool, `{"action":"add","scope":"global","content":"Pending global suggestion."}`)
	if pending.Memories[0].Status != store.MemoryStatusPending {
		t.Fatalf("global suggest row = %+v", pending.Memories[0])
	}

	workspaceList := invokeMemory(t, tool, `{"action":"list"}`)
	if workspaceList.Count != 1 || len(workspaceList.Memories) != 1 {
		t.Fatalf("workspace list = %+v, want the single workspace row", workspaceList)
	}
	if workspaceList.Memories[0].Status != store.MemoryStatusPending {
		t.Fatalf("workspace list status = %q, want pending included", workspaceList.Memories[0].Status)
	}
	globalList := invokeMemory(t, tool, `{"action":"list","scope":"global"}`)
	if globalList.Count != 1 || globalList.Memories[0].Scope != store.MemoryScopeGlobal ||
		globalList.Memories[0].Status != store.MemoryStatusPending {
		t.Fatalf("global list = %+v, want the pending global row", globalList)
	}

	// Another workspace never sees alpha's rows, and search follows the same
	// scope isolation.
	other := memoryToolWithModes(st, beta, "auto", "suggest")
	if got := invokeMemory(t, other, `{"action":"list"}`); got.Count != 0 {
		t.Fatalf("beta list = %+v, want empty", got)
	}
	if got := invokeMemory(t, tool, `{"action":"search","query":"global suggestion"}`); got.Count != 0 {
		t.Fatalf("workspace search leaked the global row: %+v", got)
	}
	if got := invokeMemory(t, tool, `{"action":"search","query":"note","scope":"global"}`); got.Count != 0 {
		t.Fatalf("global search leaked the workspace row: %+v", got)
	}
}

// TestMemoryToolScopeOwnership pins update/delete scope checks: an id from the
// other scope is rejected and the row stays untouched.
func TestMemoryToolScopeOwnership(t *testing.T) {
	st, alpha, _ := newMemoryTestStore(t)
	tool := memoryToolWithModes(st, alpha, "auto", "auto")

	workspace := invokeMemory(t, tool, `{"action":"add","content":"Workspace note."}`)
	workspaceID := workspace.Memories[0].ID
	global := invokeMemory(t, tool, `{"action":"add","scope":"global","content":"Global note."}`)
	globalID := global.Memories[0].ID

	if err := invokeMemoryErr(t, tool,
		`{"action":"update","id":"`+workspaceID+`","content":"x","scope":"global"}`); err == nil {
		t.Fatal("update of a workspace memory through the global scope was accepted")
	}
	if err := invokeMemoryErr(t, tool,
		`{"action":"delete","id":"`+globalID+`"}`); err == nil {
		t.Fatal("delete of a global memory through the workspace scope was accepted")
	}
	updated := invokeMemory(t, tool,
		`{"action":"update","id":"`+globalID+`","content":"Global note v2.","scope":"global"}`)
	if updated.Count != 1 || updated.Memories[0].Content != "Global note v2." ||
		updated.Memories[0].Scope != store.MemoryScopeGlobal {
		t.Fatalf("global update = %+v", updated)
	}
	deleted := invokeMemory(t, tool, `{"action":"delete","id":"`+workspaceID+`"}`)
	if deleted.Count != 1 || deleted.Memories[0].ID != workspaceID {
		t.Fatalf("workspace delete = %+v", deleted)
	}
}

// TestMemoryToolClearScope verifies clear only touches the requested scope.
func TestMemoryToolClearScope(t *testing.T) {
	st, alpha, _ := newMemoryTestStore(t)
	tool := memoryToolWithModes(st, alpha, "auto", "auto")
	invokeMemory(t, tool, `{"action":"add","content":"Workspace note."}`)
	invokeMemory(t, tool, `{"action":"add","scope":"global","content":"Global note."}`)

	cleared := invokeMemory(t, tool, `{"action":"clear"}`)
	if cleared.Count != 1 {
		t.Fatalf("workspace clear count = %d, want 1", cleared.Count)
	}
	if got := invokeMemory(t, tool, `{"action":"list"}`); got.Count != 0 {
		t.Fatalf("workspace rows after clear = %+v", got)
	}
	if got := invokeMemory(t, tool, `{"action":"list","scope":"global"}`); got.Count != 1 {
		t.Fatalf("global rows after workspace clear = %+v, want the global row kept", got)
	}
	cleared = invokeMemory(t, tool, `{"action":"clear","scope":"global"}`)
	if cleared.Count != 1 {
		t.Fatalf("global clear count = %d, want 1", cleared.Count)
	}
	active, pending, err := st.MemoryCounts(store.MemoryScopeGlobal, "")
	if err != nil || active != 0 || pending != 0 {
		t.Fatalf("global counts after clear = %d/%d, %v; want 0/0", active, pending, err)
	}
}

// TestMemoryToolRejectsSecrets pins the basic secret rejection on the tool
// write path: credential-like content is refused before persistence
// (cooperating with internal/memory.ScrubSecrets).
func TestMemoryToolRejectsSecrets(t *testing.T) {
	st, alpha, _ := newMemoryTestStore(t)
	tool := memoryToolWithModes(st, alpha, "auto", "auto")
	for _, content := range []string{
		`api_key=sk-live-1234567890abcdef`,
		`Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature`,
		`password: hunter2`,
		`-----BEGIN RSA PRIVATE KEY-----\nMIIEow...\n-----END RSA PRIVATE KEY-----`,
	} {
		args, err := json.Marshal(map[string]any{"action": "add", "content": content})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		if _, ierr := tool.Invoke(context.Background(), args); ierr == nil {
			t.Fatalf("secret-like content was persisted: %q", content)
		}
	}
	list, err := st.ListMemoriesScoped(store.MemoryScopeWorkspace, alpha, "", 0)
	if err != nil {
		t.Fatalf("ListMemoriesScoped: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("secret rejection persisted rows: %+v", list)
	}
}

// TestMemoryToolOffModeBlocksMutations pins the REVIEW-MEMORY P2-1 fix: with a
// scope's write mode off the agent may not rewrite, delete or clear its
// existing memories either; auto and suggest still allow organizing them.
func TestMemoryToolOffModeBlocksMutations(t *testing.T) {
	scopes := []struct {
		name     string
		scope    string
		addArgs  string
		modePair func(string) (string, string)
	}{
		{
			name:     "workspace",
			scope:    store.MemoryScopeWorkspace,
			addArgs:  `{"action":"add","content":"workspace note"}`,
			modePair: func(mode string) (string, string) { return mode, "auto" },
		},
		{
			name:     "global",
			scope:    store.MemoryScopeGlobal,
			addArgs:  `{"action":"add","scope":"global","content":"global note"}`,
			modePair: func(mode string) (string, string) { return "auto", mode },
		},
	}
	for _, tc := range scopes {
		t.Run(tc.name, func(t *testing.T) {
			st, alpha, _ := newMemoryTestStore(t)
			// Write the row with the scope enabled, then turn the mode off.
			writeTool := NewMemoryTool(MemoryConfig{
				Store: st, WorkspaceID: func() string { return alpha },
				Modes: func() (string, string) { return tc.modePair("auto") },
			})
			added := invokeMemory(t, writeTool, tc.addArgs)
			id := added.Memories[0].ID
			before, err := st.GetMemory(id)
			if err != nil {
				t.Fatalf("GetMemory: %v", err)
			}

			offTool := NewMemoryTool(MemoryConfig{
				Store: st, WorkspaceID: func() string { return alpha },
				Modes: func() (string, string) { return tc.modePair("off") },
			})
			scopeArg := ""
			if tc.scope == store.MemoryScopeGlobal {
				scopeArg = `,"scope":"global"`
			}
			for _, args := range []string{
				`{"action":"update","id":"` + id + `","content":"rewritten"` + scopeArg + `}`,
				`{"action":"delete","id":"` + id + `"` + scopeArg + `}`,
				`{"action":"clear"` + scopeArg + `}`,
			} {
				err := invokeMemoryErr(t, offTool, args)
				if err == nil || !strings.Contains(err.Error(), "disabled") ||
					!strings.Contains(err.Error(), tc.scope) {
					t.Fatalf("%s with mode off error = %v, want a stable disabled-for-%s error", args, err, tc.scope)
				}
				after, gerr := st.GetMemory(id)
				if gerr != nil {
					t.Fatalf("GetMemory after rejected %s: %v", args, gerr)
				}
				if after.Content != before.Content || after.Status != before.Status {
					t.Fatalf("rejected %s changed the memory: %+v -> %+v", args, before, after)
				}
			}

			// suggest (and auto) may still organize existing rows: the mode only
			// gates new writes, not maintenance of what is already stored.
			for _, mode := range []string{"suggest", "auto"} {
				manageTool := NewMemoryTool(MemoryConfig{
					Store: st, WorkspaceID: func() string { return alpha },
					Modes: func() (string, string) { return tc.modePair(mode) },
				})
				if err := invokeMemoryErr(t, manageTool,
					`{"action":"update","id":"`+id+`","content":"rewritten by `+mode+`"`+scopeArg+`}`); err != nil {
					t.Fatalf("update with %s mode = %v, want allowed", mode, err)
				}
				if got, gerr := st.GetMemory(id); gerr != nil || got.Content != "rewritten by "+mode {
					t.Fatalf("update with %s mode content = %+v, %v", mode, got, gerr)
				}
			}
			manageTool := NewMemoryTool(MemoryConfig{
				Store: st, WorkspaceID: func() string { return alpha },
				Modes: func() (string, string) { return tc.modePair("suggest") },
			})
			if _, err := manageTool.Invoke(context.Background(), json.RawMessage(`{"action":"clear"`+scopeArg+`}`)); err != nil {
				t.Fatalf("clear with suggest mode = %v, want allowed", err)
			}
		})
	}
}
