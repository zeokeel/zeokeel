package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// newProjectorFixture builds the minimal controller wiring for the AR-05
// convergence tests: a store, a broker and a projector supplied by the "app".
func newProjectorFixture(t *testing.T) (*store.Store, *state.Broker, *state.Projector, func()) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	broker := state.NewBroker()
	projector := state.NewProjector(context.Background(), broker)
	cleanup := func() {
		projector.Close()
		broker.Close()
		_ = st.Close()
	}
	return st, broker, projector, cleanup
}

// TestControllerStopsBorrowedProjector pins AR-05's convergence decision: the
// UI reads the controller's own per-session snapshots, so a projector handed to
// New must stop folding the broker stream (otherwise every event is folded
// twice). After New, publishing on the broker must not advance the projector's
// snapshot.
func TestControllerStopsBorrowedProjector(t *testing.T) {
	st, broker, projector, cleanup := newProjectorFixture(t)
	defer cleanup()

	ctrl, err := session.New(session.Config{Store: st, Broker: broker, Projector: projector})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer func() { _ = ctrl.Close() }()

	// New closes the borrowed projector synchronously, so the event published
	// afterwards has no subscriber and must never be folded.
	broker.Publish(state.Token{SessionID: "ghost", MsgID: "m1", Text: "must not be folded"})
	time.Sleep(100 * time.Millisecond)
	snap := projector.Snapshot()
	if snap.Seq != 0 || len(snap.Messages) != 0 {
		t.Fatalf("borrowed projector kept folding after the controller took over: seq=%d messages=%+v", snap.Seq, snap.Messages)
	}
}

// TestControllerSnapshotIgnoresBorrowedProjector pins the second half of the
// decision: Controller.Snapshot must describe the current session from its own
// views, never fall back to a session-unaware projector snapshot (which folds
// "last event wins" across sessions and cannot represent user messages).
func TestControllerSnapshotIgnoresBorrowedProjector(t *testing.T) {
	st, broker, projector, cleanup := newProjectorFixture(t)
	defer cleanup()

	// The projector folds an unrelated session before the controller exists.
	broker.Publish(state.Token{SessionID: "ghost", MsgID: "ghost-message", Text: "unrelated session text"})
	deadline := time.Now().Add(2 * time.Second)
	for projector.Snapshot().Seq == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if projector.Snapshot().Seq == 0 {
		t.Fatal("test setup: projector did not fold the ghost event")
	}

	ctrl, err := session.New(session.Config{Store: st, Broker: broker, Projector: projector})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer func() { _ = ctrl.Close() }()

	snap := ctrl.Snapshot()
	if snap == nil {
		t.Fatal("Controller.Snapshot returned nil")
	}
	if len(snap.Messages) != 0 || snap.Seq != 0 {
		t.Fatalf("Controller.Snapshot leaked the borrowed projector's session-unrelated state: seq=%d messages=%+v", snap.Seq, snap.Messages)
	}
	if snap.SessionID != "" || snap.Phase != state.PhaseIdle {
		t.Fatalf("empty controller snapshot = {session:%q phase:%q}, want an empty idle snapshot", snap.SessionID, snap.Phase)
	}
}
