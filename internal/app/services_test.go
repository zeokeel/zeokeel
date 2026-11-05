package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

func TestBuildServicesCreatesIndexAndCleansUp(t *testing.T) {
	dir := t.TempDir()

	ctrl, cleanup, err := BuildServices(dir, nil)
	if err != nil {
		t.Fatalf("BuildServices: %v", err)
	}
	if ctrl == nil {
		t.Fatal("BuildServices returned a nil controller")
	}
	if cleanup == nil {
		t.Fatal("BuildServices returned a nil cleanup")
	}

	index := filepath.Join(dir, "index.sqlite")
	if _, err := os.Stat(index); err != nil {
		t.Fatalf("index.sqlite not created: %v", err)
	}

	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "headless")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == "" {
		t.Fatal("CreateSession returned an empty id")
	}
	if snap := ctrl.Snapshot(); snap == nil || snap.SessionID != id {
		t.Fatalf("Snapshot after CreateSession = %+v, want session %q", snap, id)
	}

	// With no Agent the controller is still readable/history-capable, but a
	// turn cannot run.
	if err := ctrl.Submit(ctx, id, "hello"); !errors.Is(err, session.ErrNoAgent) {
		t.Fatalf("Submit without agent error = %v, want ErrNoAgent", err)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}

	// Reopen the index to prove the session was committed and cleanup released
	// the database.
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close() //nolint:errcheck // test cleanup
	sessions, err := st.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != id {
		t.Fatalf("sessions = %+v, want the created session", sessions)
	}
}

func TestBuildServicesRejectsBadDirectory(t *testing.T) {
	if _, _, err := BuildServices("", nil); err == nil {
		t.Fatal("BuildServices(empty) must fail")
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := BuildServices(file, nil); err == nil {
		t.Fatal("BuildServices over a regular file must fail")
	}
}
