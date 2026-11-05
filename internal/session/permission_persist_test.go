package session_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestSessionPermissionPersistsPerSession covers the in-memory-only mode: a
// session switched to Full access must still be Full after a restart, and must
// not inherit another session's mode or the global default.
func TestSessionPermissionPersistsPerSession(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: func(context.Context, runtime.Request, int) []runtime.Chunk { return nil }})
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "persistent mode")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	h.ctrl.SetSessionPermission(id, session.PermissionFull)
	raw, err := h.store.GetSetting(session.SessionPermissionKey(id))
	if err != nil || raw != session.PermissionFull {
		t.Fatalf("stored %s = %q, %v; want %q",
			session.SessionPermissionKey(id), raw, err, session.PermissionFull)
	}

	// Restart over the same store, with a different global default.
	ctrl := h.restart()
	cfg, err := ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Permission = session.PermissionAsk
	if err := ctrl.Settings().Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if got := ctrl.SessionPermissionOrDefault(id); got != session.PermissionFull {
		t.Fatalf("after restart mode = %q, want %q (global default is Read Only)", got, session.PermissionFull)
	}

	// A second session created after the default change stays Read Only even
	// though the first session is Full.
	other, err := ctrl.CreateSession(ctx, "other")
	if err != nil {
		t.Fatalf("CreateSession other: %v", err)
	}
	if got := ctrl.SessionPermissionOrDefault(other); got != session.PermissionAsk {
		t.Fatalf("new session mode = %q, want the new default %q", got, session.PermissionAsk)
	}
	if got := ctrl.SessionPermissionOrDefault(id); got != session.PermissionFull {
		t.Fatalf("first session mode = %q, want %q", got, session.PermissionFull)
	}
}

// TestSessionDeleteRemovesTranscriptDir covers the disk-level deletion: the
// SQLite cascade cannot remove sessions/<id>/session.jsonl.
func TestSessionDeleteRemovesTranscriptDir(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: func(context.Context, runtime.Request, int) []runtime.Chunk { return nil }})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "delete me")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.AppendTranscript(h.dir, id, map[string]string{"type": "status"}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}
	dir := filepath.Join(h.dir, "sessions", id)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("transcript dir missing before delete: %v", err)
	}
	if err := h.ctrl.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("transcript dir still exists after delete: %v", err)
	}
}

// TestWorkspaceDeleteRemovesSessionDirs covers the same cleanup when a whole
// workspace is deleted.
func TestWorkspaceDeleteRemovesSessionDirs(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: func(context.Context, runtime.Request, int) []runtime.Chunk { return nil }})
	ctx := context.Background()
	ws, err := h.ctrl.CreateWorkspace(ctx, "gone", h.dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	id, err := h.ctrl.CreateSessionInWorkspace(ctx, ws.ID, "in gone")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	if err := store.AppendTranscript(h.dir, id, map[string]string{"type": "status"}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}
	dir := filepath.Join(h.dir, "sessions", id)
	if err := h.ctrl.DeleteWorkspace(ctx, ws.ID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session dir still exists after workspace delete: %v", err)
	}
}
