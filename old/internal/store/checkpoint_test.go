package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestCheckpointStoreRoundTrip pins the durable checkpoints table: a
// checkpoint survives reopening the index, can be replaced, and is removed by
// DeleteCheckpoint.
func TestCheckpointStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, err := s.CreateWorkspace("checkpoint ws", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess, err := s.CreateSession(ws.ID, "checkpoint session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	now := time.Now().UTC()
	cp := Checkpoint{
		SessionID: sess.ID,
		History:   json.RawMessage(`[{"role":"user","content":"hello"}]`),
		TurnID:    "turn-1",
		UpdatedAt: now,
		Phase:     "completed",
	}
	if err := s.SaveCheckpoint(cp); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	// Reopen the same directory: the checkpoint is durable, not in-memory.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()

	loaded, err := s.LoadCheckpoint(sess.ID)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if loaded.SessionID != cp.SessionID || loaded.TurnID != cp.TurnID || loaded.Phase != cp.Phase {
		t.Fatalf("loaded checkpoint = %+v, want %+v", loaded, cp)
	}
	if string(loaded.History) != string(cp.History) {
		t.Fatalf("loaded history = %s, want %s", loaded.History, cp.History)
	}
	if !loaded.UpdatedAt.Equal(now) {
		t.Fatalf("loaded UpdatedAt = %v, want %v", loaded.UpdatedAt, now)
	}

	// Saving the same session again replaces the checkpoint.
	updated := Checkpoint{
		SessionID: sess.ID,
		History:   json.RawMessage(`[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]`),
		TurnID:    "turn-2",
		UpdatedAt: now.Add(time.Minute),
		Phase:     "completed",
	}
	if err := s.SaveCheckpoint(updated); err != nil {
		t.Fatalf("SaveCheckpoint(update): %v", err)
	}
	loaded, err = s.LoadCheckpoint(sess.ID)
	if err != nil {
		t.Fatalf("LoadCheckpoint(update): %v", err)
	}
	if loaded.TurnID != "turn-2" || string(loaded.History) != string(updated.History) {
		t.Fatalf("updated checkpoint = %+v, want %+v", loaded, updated)
	}

	if err := s.DeleteCheckpoint(sess.ID); err != nil {
		t.Fatalf("DeleteCheckpoint: %v", err)
	}
	if _, err := s.LoadCheckpoint(sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadCheckpoint after delete error = %v, want ErrNotFound", err)
	}
	// Deleting a missing checkpoint is idempotent.
	if err := s.DeleteCheckpoint(sess.ID); err != nil {
		t.Fatalf("DeleteCheckpoint(missing) = %v, want nil", err)
	}

	// Argument validation and missing sessions.
	if err := s.SaveCheckpoint(Checkpoint{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SaveCheckpoint(empty) error = %v, want ErrInvalidArgument", err)
	}
	if err := s.SaveCheckpoint(Checkpoint{SessionID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SaveCheckpoint(missing session) error = %v, want ErrNotFound", err)
	}
	if _, err := s.LoadCheckpoint(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("LoadCheckpoint(empty) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.LoadCheckpoint("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadCheckpoint(missing) error = %v, want ErrNotFound", err)
	}
	if err := s.DeleteCheckpoint(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("DeleteCheckpoint(empty) error = %v, want ErrInvalidArgument", err)
	}
}

// TestDeleteSessionRemovesCheckpoint pins the cascade/cleanup contract: a
// deleted session must not leave a recoverable checkpoint behind.
func TestDeleteSessionRemovesCheckpoint(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "checkpoint delete", t.TempDir())
	sess := mustSession(t, s, ws.ID, "delete me")

	if err := s.SaveCheckpoint(Checkpoint{
		SessionID: sess.ID,
		History:   json.RawMessage(`[{"role":"user","content":"secret"}]`),
		TurnID:    "turn-1",
		UpdatedAt: time.Now().UTC(),
		Phase:     "completed",
	}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	if _, err := s.LoadCheckpoint(sess.ID); err != nil {
		t.Fatalf("LoadCheckpoint before delete: %v", err)
	}

	if err := s.DeleteSession(sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.LoadCheckpoint(sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadCheckpoint after DeleteSession error = %v, want ErrNotFound", err)
	}
}
