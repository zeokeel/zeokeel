package gateway

import (
	"context"
	"io"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestProvideRegistersTheFiveHostServices pins the reason this package exists:
// the headless assembly never provides these five keys, so a run started from
// the browser must get them from the gateway (§7.2).
func TestProvideRegistersTheFiveHostServices(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	gateway, err := New(root, Options{DataDir: t.TempDir(), Logger: testLogger{t: t}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if _, ok := root.Get(contracts.KeyUserInteraction); !ok {
		t.Error("KeyUserInteraction was not provided")
	}
	if _, ok := root.Get(contracts.KeyArtifactPresenter); !ok {
		t.Error("KeyArtifactPresenter was not provided")
	}
	if _, ok := root.Get(contracts.KeyPlanReviewer); !ok {
		t.Error("KeyPlanReviewer was not provided")
	}
	if _, ok := root.Get(contracts.KeyNotifier); !ok {
		t.Error("KeyNotifier was not provided")
	}
	if _, ok := root.Get(contracts.KeyForegroundSession); !ok {
		t.Error("KeyForegroundSession was not provided")
	}
}

// TestAskParksUntilAnswered proves the blocking bridge: the agent's ask_user
// tool waits, and a client answer releases it.
func TestAskParksUntilAnswered(t *testing.T) {
	gateway := newIdleGateway(t)
	interaction, _ := gateway.root.Get(contracts.KeyUserInteraction)

	type outcome struct {
		answer string
		err    error
	}
	results := make(chan outcome, 1)
	go func() {
		answer, err := interaction.Ask(context.Background(), "which file?", []string{"a", "b"})
		results <- outcome{answer: answer, err: err}
	}()

	// Wait until the question is registered, then answer it by id.
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for {
		gateway.mu.Lock()
		for candidate := range gateway.questions {
			id = candidate
		}
		gateway.mu.Unlock()
		if id != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Ask never registered a pending question")
		}
		time.Sleep(time.Millisecond)
	}
	interaction.Answer(id, "b")

	select {
	case result := <-results:
		if result.err != nil || result.answer != "b" {
			t.Fatalf("Ask returned (%q, %v), want (\"b\", nil)", result.answer, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask stayed parked after Answer")
	}
}

// TestAskCancelledByContext proves a cancelled run releases the parked question.
func TestAskCancelledByContext(t *testing.T) {
	gateway := newIdleGateway(t)
	interaction, _ := gateway.root.Get(contracts.KeyUserInteraction)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := interaction.Ask(ctx, "which file?", nil)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Ask returned no error after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask stayed parked after the context ended")
	}
}

// TestPlanReviewFlow covers the plan bridge the exit_plan_mode tool uses.
func TestPlanReviewFlow(t *testing.T) {
	gateway := newIdleGateway(t)
	reviewer, _ := gateway.root.Get(contracts.KeyPlanReviewer)
	if state := reviewer.State(); state != planIdle {
		t.Fatalf("initial plan state = %q, want %q", state, planIdle)
	}
	if state, err := reviewer.Review(context.Background(), "step 1"); err != nil || state != planReviewing {
		t.Fatalf("Review = (%q, %v), want (%q, nil)", state, err, planReviewing)
	}
	if _, err := reviewer.Exit("bogus"); err == nil {
		t.Fatal("Exit accepted an unknown action")
	}
	if state, err := reviewer.Exit("approve"); err != nil || state != planApproved {
		t.Fatalf("Exit(approve) = (%q, %v), want (%q, nil)", state, err, planApproved)
	}
	if _, err := reviewer.Exit("approve"); err == nil {
		t.Fatal("Exit(approve) succeeded with no plan under review")
	}
	if _, err := reviewer.Review(context.Background(), "   "); err == nil {
		t.Fatal("Review accepted blank content")
	}
}

// TestArtifactsAreRetainedAndBounded covers the presenter's history half.
func TestArtifactsAreRetainedAndBounded(t *testing.T) {
	gateway := newIdleGateway(t)
	presenter, _ := gateway.root.Get(contracts.KeyArtifactPresenter)
	lister, ok := presenter.(contracts.ArtifactLister)
	if !ok {
		t.Fatal("the gateway presenter does not implement ArtifactLister")
	}
	id, err := presenter.Present(context.Background(), "Report", "# body")
	if err != nil || id == "" {
		t.Fatalf("Present = (%q, %v)", id, err)
	}
	if id2, err := presenter.Present(context.Background(), "Second", "body"); err != nil || id2 == id {
		t.Fatalf("second Present = (%q, %v); ids must differ", id2, err)
	}
	artifacts := lister.Artifacts()
	if len(artifacts) != 2 || artifacts[0].Title != "Second" {
		t.Fatalf("Artifacts() = %+v, want newest first", artifacts)
	}
}

// TestNotifierKeepsActionIDs proves a toast action survives as an id the client
// can post back (the wire cannot carry the contract's func(), §6.3).
func TestNotifierKeepsActionIDs(t *testing.T) {
	gateway := newIdleGateway(t)
	notifier, _ := gateway.root.Get(contracts.KeyNotifier)
	sessionNotifier, ok := notifier.(contracts.SessionNotifier)
	if !ok {
		t.Fatal("the gateway notifier does not implement SessionNotifier")
	}
	ran := make(chan struct{}, 1)
	notification := notifyActionForTest(notifier, "Undo", func() { ran <- struct{}{} })
	if notification.ActionLabel != "Undo" {
		t.Fatalf("ActionLabel = %q", notification.ActionLabel)
	}
	gateway.mu.Lock()
	pending := gateway.toastActions[notification.ID]
	gateway.mu.Unlock()
	if pending == nil || pending.actionID == "" {
		t.Fatal("the toast action was not retained")
	}
	actionID := pending.actionID
	if _, err := gateway.runNotificationAction(mustRaw(t, notifyActionParams{ID: notification.ID, ActionID: actionID})); err != nil {
		t.Fatalf("runNotificationAction: %v", err)
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("the retained action did not run")
	}
	tagged := sessionNotifier.NotifySystemFor(contracts.NotifyInfo, "done", "s1")
	if tagged.SessionID != "s1" {
		t.Fatalf("NotifySystemFor lost the session: %+v", tagged)
	}
	if sessionNotifier.SystemNotificationsSupported() {
		t.Fatal("the gateway process claims an OS notification sink")
	}
}

// TestForegroundSessionKeepsTheNewestReport covers the one client→server state
// report of the protocol (§5.1.7).
func TestForegroundSessionKeepsTheNewestReport(t *testing.T) {
	gateway := newIdleGateway(t)
	service, _ := gateway.root.Get(contracts.KeyForegroundSession)
	if session, ok := service.ForegroundSession(); ok {
		t.Fatalf("ForegroundSession with no reports = (%q, true)", session)
	}
	gateway.setForegroundSession("c1", "s1")
	gateway.setForegroundSession("c2", "s2")
	if session, _ := service.ForegroundSession(); session != "s2" {
		t.Fatalf("ForegroundSession = %q, want s2", session)
	}
	gateway.clearForegroundSession("c2")
	if session, _ := service.ForegroundSession(); session != "s1" {
		t.Fatalf("ForegroundSession after the newest client left = %q, want s1", session)
	}
	gateway.setForegroundSession("c1", "")
	if _, ok := service.ForegroundSession(); ok {
		t.Fatal("an empty report must clear the foreground session")
	}
}

// notifyActionForTest posts a toast with an action through the ActionNotifier
// half.
func notifyActionForTest(notifier contracts.Notifier, label string, run func()) contracts.Notification {
	if action, ok := notifier.(contracts.ActionNotifier); ok {
		return action.NotifyAction(contracts.NotifyInfo, "copied", label, run)
	}
	return notifier.Notify(contracts.NotifyInfo, "copied")
}

// mustRaw marshals one params object for the method handlers.
func mustRaw(t *testing.T, params any) []byte {
	t.Helper()
	return []byte(mustJSON(t, params))
}

// newIdleGateway builds a gateway whose services are provided but which never
// binds a listener (no hub loop): enough to test the service bridge.
func newIdleGateway(t *testing.T) *Gateway {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	gateway, err := New(root, Options{DataDir: t.TempDir(), Logger: testLogger{t: t}, Out: io.Discard})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return gateway
}
