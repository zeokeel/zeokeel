package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestSessionSearchScopedToDefaultEmptyRootWorkspace covers the silent global
// fallback: the default workspace has an empty root, which used to be skipped,
// so session_search returned another workspace's sessions.
func TestSessionSearchScopedToDefaultEmptyRootWorkspace(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	def, err := st.CreateWorkspace("default", "")
	if err != nil {
		t.Fatalf("create default workspace: %v", err)
	}
	other, err := st.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	sess, err := st.CreateSession(other.ID, "Other project")
	if err != nil {
		t.Fatalf("create other session: %v", err)
	}
	if _, err := st.AppendMessage(store.Message{
		SessionID: sess.ID, Role: "user", Content: "needle in another workspace",
	}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if err := st.SetSetting(session.SettingWorkspace, def.ID); err != nil {
		t.Fatalf("persist workspace: %v", err)
	}

	search := func(t *testing.T, reg *runtime.Registry) map[string]any {
		t.Helper()
		tool, ok := reg.Get("session_search")
		if !ok {
			t.Fatal("session_search is not registered")
		}
		out, err := tool.Invoke(context.Background(), json.RawMessage(`{"query":"needle"}`))
		if err != nil {
			t.Fatalf("session_search: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("decode result %q: %v", out, err)
		}
		return got
	}

	// The empty-root default workspace still resolves when the tool root is
	// the app's data directory and the sidebar's selection is persisted.
	root := t.TempDir()
	reg, closeFn, err := registerAllTools(st, root, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		defer func() { _ = closeFn() }()
	}
	got := search(t, reg)
	if count, _ := got["count"].(float64); count != 0 {
		t.Fatalf("default workspace search returned %v sessions: %v", count, got)
	}

	// An empty root with no selection resolves the empty-root default rather
	// than falling through to a global search.
	if got := currentWorkspaceID(st, ""); got != def.ID {
		t.Fatalf("empty-root currentWorkspaceID = %q, want default %q", got, def.ID)
	}

	// The controller's explicit workspace wins over re-deriving it.
	reg2, closeFn2, err := registerAllToolsWithOptions(st, root, func(k, e, d string) string { return d },
		registryOptions{WorkspaceID: other.ID})
	if err != nil {
		t.Fatalf("register tools with workspace: %v", err)
	}
	if closeFn2 != nil {
		defer func() { _ = closeFn2() }()
	}
	got = search(t, reg2)
	if count, _ := got["count"].(float64); count != 1 {
		t.Fatalf("other workspace search returned %v sessions, want 1: %v", count, got)
	}

}
