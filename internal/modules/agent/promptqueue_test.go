package agent

// Tests for the durable prompt inbox (DSH inbox) and the settle order that puts
// human input ahead of automatic goal rounds.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newQueueService builds a bare service (no module activation) with a fake
// durable store and a model configured, so Send/Enqueue start real runs.
func newQueueService(t *testing.T, store *fakeStore, settings *fakeSettings, provider rig.Provider) *service {
	t.Helper()
	if store == nil {
		store = newFakeStore()
	}
	if settings == nil {
		settings = newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	}
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(settings), &fakeLogger{}, store, fakeApprovals{})
	if provider != nil {
		svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	}
	return svc
}

// queueEvents returns the durable queue/change events of one session.
func queueEventCount(store *fakeStore, sessionID string) int {
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		return -1
	}
	count := 0
	for _, event := range events {
		if event.Type == queueEventType {
			count++
		}
	}
	return count
}

// waitForMessages waits until the session read model has want user messages.
func waitForMessages(t *testing.T, svc *service, sessionID string, want int) contracts.ChatSession {
	t.Helper()
	waitFor(t, "session messages", func() bool {
		session, ok := svc.Session(sessionID)
		if !ok || session.Running {
			return false
		}
		return len(userMessages(session)) >= want
	})
	session, _ := svc.Session(sessionID)
	return session
}

func TestQueuedPromptStartsAfterTheRunningTurnSettles(t *testing.T) {
	provider := newGatedProvider()
	svc := newQueueService(t, newFakeStore(), nil, provider)
	sessionID, err := svc.NewSession("inbox")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "first turn"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the first run to reach the provider", func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.calls > 0
	})

	// While the turn runs Send keeps rejecting (the old conflict contract); the
	// inbox is the supported queueing path.
	if err := svc.Send(sessionID, "racing prompt"); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("Send while running = %v, want ErrSessionRunning", err)
	}
	if !errors.Is(ErrSessionRunning, contracts.ErrSessionRunning) {
		t.Fatal("agent ErrSessionRunning is not contracts.ErrSessionRunning")
	}
	entry, err := svc.EnqueuePrompt(context.Background(), sessionID, "queued while running")
	if err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	if entry.ID == "" || entry.SessionID != sessionID || entry.Text != "queued while running" {
		t.Fatalf("entry = %+v, want a durable inbox entry", entry)
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 1 || queued[0].ID != entry.ID {
		t.Fatalf("QueuedPrompts = %+v, want the enqueued prompt", queued)
	}

	provider.releaseNow()
	session := waitForMessages(t, svc, sessionID, 2)
	users := userMessages(session)
	if len(users) != 2 || users[1].Text != "queued while running" {
		t.Fatalf("user messages = %+v, want the queued prompt second", users)
	}
	if users[1].GoalRound != nil {
		t.Fatalf("queued human prompt carries a goal-round marker: %+v", users[1].GoalRound)
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 0 {
		t.Fatalf("inbox after drain = %+v, want empty", queued)
	}
	// Two durable snapshots: the enqueue and the durable dequeue.
	if got := queueEventCount(svc.store.(*fakeStore), sessionID); got != 2 {
		t.Fatalf("queue/change events = %d, want 2 (enqueue + dequeue)", got)
	}
}

func TestQueuedPromptBeatsTheGoalRound(t *testing.T) {
	provider := newGatedProvider()
	svc := newQueueService(t, newFakeStore(), nil, provider)
	goals := newFakeGoalService("objective", 1)
	svc.goalService = goals

	sessionID, err := svc.NewSession("goal with inbox")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the first run to reach the provider", func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.calls > 0
	})
	if _, err := svc.EnqueuePrompt(context.Background(), sessionID, "human follow-up"); err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	provider.releaseNow()

	waitFor(t, "the goal to stop at its round cap", func() bool {
		goal, _ := goals.snapshot()
		return goal.Phase != contracts.GoalPhaseActive
	})
	session := waitForMessages(t, svc, sessionID, 3)
	users := userMessages(session)
	if len(users) != 3 {
		t.Fatalf("user messages = %d, want 3 (start + queued + one goal round)", len(users))
	}
	if users[1].Text != "human follow-up" || users[1].GoalRound != nil {
		t.Fatalf("second user message = %+v, want the human follow-up before any goal round", users[1])
	}
	if users[2].GoalRound == nil || users[2].GoalRound.Round != 1 {
		t.Fatalf("third user message = %+v, want the goal round after the inbox drained", users[2])
	}
	if got := goals.admitCount(); got != 1 {
		t.Fatalf("goal rounds = %d, want exactly 1 (the inbox did not consume a round)", got)
	}
}

func TestRemovedQueuedPromptIsNeverSent(t *testing.T) {
	provider := newGatedProvider()
	store := newFakeStore()
	svc := newQueueService(t, store, nil, provider)
	sessionID, err := svc.NewSession("remove")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the first run to reach the provider", func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.calls > 0
	})
	entry, err := svc.EnqueuePrompt(context.Background(), sessionID, "remove me")
	if err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	if err := svc.RemoveQueuedPrompt(context.Background(), sessionID, entry.ID); err != nil {
		t.Fatalf("RemoveQueuedPrompt: %v", err)
	}
	// Idempotent for an unknown id/session.
	if err := svc.RemoveQueuedPrompt(context.Background(), sessionID, "no-such-id"); err != nil {
		t.Fatalf("RemoveQueuedPrompt(unknown id): %v", err)
	}
	provider.releaseNow()
	session := waitForMessages(t, svc, sessionID, 1)
	time.Sleep(20 * time.Millisecond)
	if got := len(userMessages(session)); got != 1 {
		t.Fatalf("user messages = %d, want only the first turn", got)
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 0 {
		t.Fatalf("inbox after remove = %+v, want empty", queued)
	}
}

func TestPromptQueueRestoreStartsTheInbox(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("crash with inbox")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// The previous process died while the session was running with two prompts
	// waiting: mark it running, enqueue, then abandon the service (restart).
	first.mu.Lock()
	first.sessions[sessionID].running = true
	first.mu.Unlock()
	if _, err := first.EnqueuePrompt(context.Background(), sessionID, "queued one"); err != nil {
		t.Fatalf("EnqueuePrompt one: %v", err)
	}
	if _, err := first.EnqueuePrompt(context.Background(), sessionID, "queued two"); err != nil {
		t.Fatalf("EnqueuePrompt two: %v", err)
	}
	if got := len(first.QueuedPrompts(sessionID)); got != 2 {
		t.Fatalf("queued before restart = %d, want 2", got)
	}

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	// The restored idle inbox starts immediately (DSH): both prompts run, in
	// order, with no extra user action.
	waitFor(t, "the restored inbox to drain", func() bool {
		session, ok := second.Session(sessionID)
		return ok && !session.Running && len(userMessages(session)) == 2
	})
	session, _ := second.Session(sessionID)
	users := userMessages(session)
	if users[0].Text != "queued one" || users[1].Text != "queued two" {
		t.Fatalf("restored prompts = %q/%q, want send order", users[0].Text, users[1].Text)
	}
	if queued := second.QueuedPrompts(sessionID); len(queued) != 0 {
		t.Fatalf("restored inbox after drain = %+v, want empty", queued)
	}
}

func TestEnqueuePromptStartsAnIdleSessionImmediately(t *testing.T) {
	store := newFakeStore()
	svc := newQueueService(t, store, nil, &captureProvider{})
	sessionID, err := svc.NewSession("idle enqueue")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := svc.EnqueuePrompt(context.Background(), sessionID, "start me"); err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	session := waitForMessages(t, svc, sessionID, 1)
	if user := userMessages(session); len(user) != 1 || user[0].Text != "start me" {
		t.Fatalf("user messages = %+v, want the enqueued prompt", user)
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 0 {
		t.Fatalf("inbox = %+v, want empty", queued)
	}
}

func TestStopKeepsTheInboxAndALaterTurnDrainsIt(t *testing.T) {
	provider := newGatedProvider()
	svc := newQueueService(t, newFakeStore(), nil, provider)
	sessionID, err := svc.NewSession("stop keeps inbox")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the first run to reach the provider", func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.calls > 0
	})
	if _, err := svc.EnqueuePrompt(context.Background(), sessionID, "queued over stop"); err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	if err := svc.Cancel(sessionID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitIdle(t, svc, sessionID)
	time.Sleep(20 * time.Millisecond)
	if got := len(userMessages(mustSession(t, svc, sessionID))); got != 1 {
		t.Fatalf("user messages after Stop = %d, want the inbox retained and not sent", got)
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 1 {
		t.Fatalf("inbox after Stop = %+v, want it retained", queued)
	}
	provider.releaseNow()

	// The user re-engages with a manual Send: the retained inbox prompt drains
	// FIRST and the new human message joins the tail (never jumps the queue).
	if err := svc.Send(sessionID, "manual resume"); err != nil {
		t.Fatalf("Send resume: %v", err)
	}
	session := waitForMessages(t, svc, sessionID, 3)
	users := userMessages(session)
	if users[1].Text != "queued over stop" || users[2].Text != "manual resume" {
		t.Fatalf("user messages = %+v, want the retained prompt before the new Send", users)
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 0 {
		t.Fatalf("inbox after resume = %+v, want empty", queued)
	}
}

func TestDeleteSessionDropsTheInbox(t *testing.T) {
	provider := newGatedProvider()
	svc := newQueueService(t, newFakeStore(), nil, provider)
	sessionID, err := svc.NewSession("delete inbox")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the first run to reach the provider", func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.calls > 0
	})
	if _, err := svc.EnqueuePrompt(context.Background(), sessionID, "never sent"); err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	if err := svc.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 0 {
		t.Fatalf("inbox after delete = %+v, want empty", queued)
	}
	if _, ok := svc.Session(sessionID); ok {
		t.Fatal("session survived DeleteSession")
	}
}

func TestPromptQueueValidationAndCopies(t *testing.T) {
	svc := newQueueService(t, newFakeStore(), nil, &captureProvider{})
	// The queue is an optional half of ChatService: a host type-asserts the
	// interface value it already has (no new service key).
	if _, ok := contracts.ChatService(svc).(contracts.PromptQueue); !ok {
		t.Fatal("ChatService value does not implement contracts.PromptQueue")
	}
	sessionID, err := svc.NewSession("validation")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := svc.EnqueuePrompt(context.Background(), sessionID, "   "); !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("EnqueuePrompt(blank) = %v, want ErrEmptyPrompt", err)
	}
	if _, err := svc.EnqueuePrompt(context.Background(), "sess-missing", "hello"); err == nil {
		t.Fatal("EnqueuePrompt(unknown session) succeeded")
	}
	// Keep the session busy so the prompts stay queued.
	svc.mu.Lock()
	svc.sessions[sessionID].running = true
	svc.mu.Unlock()
	if _, err := svc.EnqueuePrompt(context.Background(), sessionID, "one"); err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	if _, err := svc.EnqueuePrompt(context.Background(), sessionID, "two"); err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	queued := svc.QueuedPrompts(sessionID)
	if len(queued) != 2 || queued[0].Text != "one" || queued[1].Text != "two" {
		t.Fatalf("QueuedPrompts = %+v, want oldest first", queued)
	}
	// The returned slice is a copy: truncating it must not change the inbox.
	_ = queued[:0]
	if got := len(svc.QueuedPrompts(sessionID)); got != 2 {
		t.Fatalf("inbox mutated through the returned copy: %d entries", got)
	}
}

// stopInboxService builds a gated service with two prompts queued during a run,
// then stops the run: the shared setup of the pending-idle-drain tests.
func stopInboxService(t *testing.T) (*service, *gatedProvider, *fakeGoalService, string) {
	t.Helper()
	provider := newGatedProvider()
	svc := newQueueService(t, newFakeStore(), nil, provider)
	goals := newFakeGoalService("objective", 5)
	svc.goalService = goals
	sessionID, err := svc.NewSession("stop inbox")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the first run to reach the provider", func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.calls > 0
	})
	for _, prompt := range []string{"queued one", "queued two"} {
		if _, err := svc.EnqueuePrompt(context.Background(), sessionID, prompt); err != nil {
			t.Fatalf("EnqueuePrompt(%s): %v", prompt, err)
		}
	}
	if err := svc.Cancel(sessionID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitIdle(t, svc, sessionID)
	time.Sleep(20 * time.Millisecond)
	return svc, provider, goals, sessionID
}

// TestStopKeepsInboxIdleUntilAHumanGesture covers the Stop semantics: a cancelled
// run with a waiting inbox starts no new run (and no goal round) by itself, it
// only marks the inbox for the next human gesture.
func TestStopKeepsInboxIdleUntilAHumanGesture(t *testing.T) {
	svc, provider, goals, sessionID := stopInboxService(t)
	defer provider.releaseNow()
	if users := userMessages(mustSession(t, svc, sessionID)); len(users) != 1 {
		t.Fatalf("user messages after Stop = %d, want no auto-started run", len(users))
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 2 {
		t.Fatalf("inbox after Stop = %+v, want both prompts retained", queued)
	}
	if !svc.hasPendingIdleDrain(sessionID) {
		t.Fatal("Stop did not mark the inbox as pending idle drain")
	}
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("goal rounds after Stop = %d, want 0 (Stop must not resume the goal)", got)
	}
	if _, armed := goals.snapshot(); armed {
		t.Fatal("Cancel left the goal armed")
	}
	// Switching back to the session (a human gesture) drains the oldest prompt;
	// the rest follow through the normal settle drain. The goal stays disarmed.
	provider.releaseNow()
	if err := svc.SetActiveSession(sessionID); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	session := waitForMessages(t, svc, sessionID, 3)
	users := userMessages(session)
	if users[1].Text != "queued one" || users[2].Text != "queued two" {
		t.Fatalf("user messages = %+v, want the retained prompts in order", users)
	}
	if queued := svc.QueuedPrompts(sessionID); len(queued) != 0 {
		t.Fatalf("inbox after resume = %+v, want empty", queued)
	}
	if svc.hasPendingIdleDrain(sessionID) {
		t.Fatal("pending idle drain marker survived the resumed inbox")
	}
	time.Sleep(20 * time.Millisecond)
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("goal rounds after the resumed inbox = %d, want 0", got)
	}
}

// TestStopThenSendQueuesTheNewMessageAfterTheInbox covers the ordering rule: a
// new human Send drains the retained inbox first and joins the tail.
func TestStopThenSendQueuesTheNewMessageAfterTheInbox(t *testing.T) {
	svc, provider, goals, sessionID := stopInboxService(t)
	defer provider.releaseNow()
	provider.releaseNow()
	if err := svc.Send(sessionID, "manual message"); err != nil {
		t.Fatalf("Send after Stop: %v", err)
	}
	session := waitForMessages(t, svc, sessionID, 4)
	users := userMessages(session)
	if users[1].Text != "queued one" || users[2].Text != "queued two" || users[3].Text != "manual message" {
		t.Fatalf("user messages = %+v, want the inbox before the new Send", users)
	}
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("goal rounds = %d, want 0 after a Stop", got)
	}
}

// TestEnqueueAfterStopDrainsTheOldestFirst covers the EnqueuePrompt gesture.
func TestEnqueueAfterStopDrainsTheOldestFirst(t *testing.T) {
	svc, provider, _, sessionID := stopInboxService(t)
	defer provider.releaseNow()
	provider.releaseNow()
	if _, err := svc.EnqueuePrompt(context.Background(), sessionID, "new prompt"); err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	session := waitForMessages(t, svc, sessionID, 4)
	users := userMessages(session)
	if users[1].Text != "queued one" || users[2].Text != "queued two" || users[3].Text != "new prompt" {
		t.Fatalf("user messages = %+v, want the retained inbox before the new prompt", users)
	}
}

// TestStopWithEmptyInboxSetsNoPendingMarker covers the no-op case.
func TestStopWithEmptyInboxSetsNoPendingMarker(t *testing.T) {
	provider := newGatedProvider()
	svc := newQueueService(t, newFakeStore(), nil, provider)
	sessionID, err := svc.NewSession("empty inbox")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the first run to reach the provider", func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.calls > 0
	})
	if err := svc.Cancel(sessionID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitIdle(t, svc, sessionID)
	if svc.hasPendingIdleDrain(sessionID) {
		t.Fatal("an empty inbox was marked for idle drain")
	}
	if err := svc.SetActiveSession(sessionID); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if users := userMessages(mustSession(t, svc, sessionID)); len(users) != 1 {
		t.Fatalf("user messages = %d, want no run after resuming an empty inbox", len(users))
	}
}

// TestQueuedPromptSnapshotKeepsTheDurableShape pins the queue/change payload the
// SDK inbox writes: the product hosts and older builds read
// {"prompts":[{"id","text","createdAt"}]} and nothing more.
func TestQueuedPromptSnapshotKeepsTheDurableShape(t *testing.T) {
	store := newFakeStore()
	svc := newQueueService(t, store, nil, &captureProvider{})
	sessionID, err := svc.NewSession("snapshot shape")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Keep the session busy so the prompt stays queued (no run to settle).
	svc.mu.Lock()
	svc.sessions[sessionID].running = true
	svc.mu.Unlock()
	entry, err := svc.EnqueuePrompt(context.Background(), sessionID, "  spaced  ")
	if err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var payload []byte
	for _, event := range events {
		if event.Type == queueEventType {
			payload = event.Payload
		}
	}
	if payload == nil {
		t.Fatalf("no %s event was written", queueEventType)
	}
	var decoded struct {
		Prompts []struct {
			ID        string    `json:"id"`
			Text      string    `json:"text"`
			CreatedAt time.Time `json:"createdAt"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("queue/change payload is not the legacy shape: %v (%s)", err, payload)
	}
	if len(decoded.Prompts) != 1 {
		t.Fatalf("payload prompts = %+v, want one entry", decoded.Prompts)
	}
	got := decoded.Prompts[0]
	if got.ID != entry.ID || got.Text != "spaced" || !got.CreatedAt.Equal(entry.CreatedAt) {
		t.Fatalf("payload entry = %+v, want the returned entry %+v", got, entry)
	}
}

// TestPromptInboxReplaysALegacySnapshot covers the migration boundary: a queue
// written by the previous implementation (raw queue/change payload, no product
// generation code involved) still replays through the SDK inbox.
func TestPromptInboxReplaysALegacySnapshot(t *testing.T) {
	store := newFakeStore()
	const sessionID = "sess-legacy-inbox"
	legacy := []byte(`{"prompts":[` +
		`{"id":"prompt-1-1","text":"legacy one","createdAt":"2026-01-02T03:04:05Z"},` +
		`{"id":"prompt-1-2","text":"legacy two","createdAt":"2026-01-02T03:04:06Z"},` +
		`{"id":"","text":"malformed","createdAt":"2026-01-02T03:04:07Z"}]}`)
	if err := store.AppendEvent(context.Background(), rig.SessionEvent{
		SessionID: sessionID,
		Type:      queueEventType,
		Time:      time.Now(),
		Payload:   legacy,
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	svc := newQueueService(t, store, nil, &captureProvider{})
	queued := svc.QueuedPrompts(sessionID)
	if len(queued) != 2 {
		t.Fatalf("replayed inbox = %+v, want the two well-formed legacy entries", queued)
	}
	if queued[0].ID != "prompt-1-1" || queued[0].Text != "legacy one" ||
		queued[1].ID != "prompt-1-2" || queued[1].Text != "legacy two" {
		t.Fatalf("replayed inbox = %+v, want send order", queued)
	}
	if queued[0].SessionID != sessionID {
		t.Fatalf("replayed entry session = %q, want %q", queued[0].SessionID, sessionID)
	}
	if !queued[0].CreatedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("replayed entry CreatedAt = %v", queued[0].CreatedAt)
	}
}

// The reorder keeps entry IDs: the client renders them.
func TestMoveQueuedPromptReordersAndKeepsIDs(t *testing.T) {
	provider := newGatedProvider()
	svc := newQueueService(t, newFakeStore(), nil, provider)
	sessionID, err := svc.NewSession("reorder")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "first turn"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the first run to reach the provider", func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.calls > 0
	})

	ctx := context.Background()
	first, err := svc.EnqueuePrompt(ctx, sessionID, "first")
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	second, err := svc.EnqueuePrompt(ctx, sessionID, "second")
	if err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	third, err := svc.EnqueuePrompt(ctx, sessionID, "third")
	if err != nil {
		t.Fatalf("enqueue third: %v", err)
	}

	ids := func() []string {
		queued := svc.QueuedPrompts(sessionID)
		out := make([]string, 0, len(queued))
		for _, item := range queued {
			out = append(out, item.ID)
		}
		return out
	}
	assertOrder := func(want []string, step string) {
		got := ids()
		if len(got) != len(want) {
			t.Fatalf("%s: got %v want %v", step, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("%s: got %v want %v", step, got, want)
			}
		}
	}

	if err := svc.MoveQueuedPrompt(ctx, sessionID, third.ID, 0); err != nil {
		t.Fatalf("move to front: %v", err)
	}
	assertOrder([]string{third.ID, first.ID, second.ID}, "move to front")

	if err := svc.MoveQueuedPrompt(ctx, sessionID, third.ID, 99); err != nil {
		t.Fatalf("move past the end: %v", err)
	}
	assertOrder([]string{first.ID, second.ID, third.ID}, "move to end")

	if err := svc.MoveQueuedPrompt(ctx, sessionID, "no-such-id", 0); err != nil {
		t.Fatalf("moving an unknown id must be a no-op: %v", err)
	}
	assertOrder([]string{first.ID, second.ID, third.ID}, "unknown id")

	provider.releaseNow()
}
