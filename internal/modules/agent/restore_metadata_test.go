package agent

// Tests for the restore-only metadata: goal-round markers backfilled from the
// durable user_message events and the interrupted-run hint derived from an
// unfinished checkpoint.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// appendUserMessageEvent records one durable user_message event.
func appendUserMessageEvent(t *testing.T, store *fakeStore, sessionID, text string, goalRound *contracts.GoalRoundInfo) {
	t.Helper()
	payload := map[string]any{"text": text, "origin": contracts.AgentRunOriginUser}
	if goalRound != nil {
		payload["goalRound"] = goalRound
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode user_message: %v", err)
	}
	if err := store.AppendEvent(context.Background(), rig.SessionEvent{
		SessionID: sessionID, Type: "user_message", Time: time.Now(), Payload: encoded,
	}); err != nil {
		t.Fatalf("append user_message: %v", err)
	}
}

func TestGoalRoundMarkersRestoredFromUserMessageEvents(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("goal rounds")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	goalPrompt := "Continue working toward the active goal.\n\nGoal: ship\nRound 1/2"
	history := []rig.Message{
		rig.NewMessage(rig.RoleUser, goalPrompt),
		rig.NewMessage(rig.RoleAssistant, "ok"),
		rig.NewMessage(rig.RoleUser, "a human message"),
		rig.NewMessage(rig.RoleAssistant, "ok"),
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, history); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	appendUserMessageEvent(t, store, sessionID, goalPrompt, &contracts.GoalRoundInfo{GoalID: "goal-1", Round: 1, MaxGoalRounds: 2})
	appendUserMessageEvent(t, store, sessionID, "a human message", nil)

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	if len(session.Messages) != 4 {
		t.Fatalf("restored messages = %d, want 4", len(session.Messages))
	}
	if session.Messages[0].GoalRound == nil {
		t.Fatal("restored goal-round user message lost its GoalRound marker")
	}
	if round := session.Messages[0].GoalRound; round.GoalID != "goal-1" || round.Round != 1 || round.MaxGoalRounds != 2 {
		t.Fatalf("restored GoalRound = %+v, want goal-1 round 1/2", round)
	}
	if session.Messages[2].GoalRound != nil {
		t.Fatalf("human user message carries a goal-round marker: %+v", session.Messages[2].GoalRound)
	}
}

func TestGoalRoundRestoreSkipsMismatchedHistorySafely(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("compacted")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// The history was compacted/rewound: its user message no longer matches the
	// durable goal-round event's text, so no marker may be attached.
	history := []rig.Message{rig.NewMessage(rig.RoleUser, "compacted summary"), rig.NewMessage(rig.RoleAssistant, "ok")}
	if err := store.ReplaceHistory(context.Background(), sessionID, history); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	appendUserMessageEvent(t, store, sessionID, "Continue working toward the active goal. Round 1/2",
		&contracts.GoalRoundInfo{GoalID: "goal-1", Round: 1})

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	if session.Messages[0].GoalRound != nil {
		t.Fatalf("mismatched history got a marker: %+v", session.Messages[0].GoalRound)
	}
}

// TestGoalRoundMarkerSurvivesARealRestart drives the actual goal continuation
// chain and restarts the service over the same durable store: the restored
// conversation still knows which user turns were goal rounds.
func TestGoalRoundMarkerSurvivesARealRestart(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	goals := newFakeGoalService("ship it", 1)
	first.goalService = goals
	sessionID, err := first.NewSession("restart chain")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := first.Send(sessionID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the goal round chain to finish", func() bool {
		goal, _ := goals.snapshot()
		return goal.Phase != contracts.GoalPhaseActive
	})
	waitIdle(t, first, sessionID)

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	users := userMessages(session)
	if len(users) != 2 {
		t.Fatalf("restored user messages = %d, want 2 (start + 1 goal round)", len(users))
	}
	if users[0].GoalRound != nil {
		t.Fatalf("human start message got a marker: %+v", users[0].GoalRound)
	}
	if users[1].GoalRound == nil {
		t.Fatal("restored goal round lost its GoalRound marker")
	}
	if users[1].GoalRound.Round != 1 || users[1].GoalRound.MaxGoalRounds != 1 {
		t.Fatalf("restored marker = %+v, want round 1/1", users[1].GoalRound)
	}
}

func TestInterruptedRunFlagRestoredFromCheckpoint(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	interruptedID, err := first.NewSession("crashed")
	if err != nil {
		t.Fatalf("NewSession crashed: %v", err)
	}
	finishedID, err := first.NewSession("finished")
	if err != nil {
		t.Fatalf("NewSession finished: %v", err)
	}
	checkpoints := store.CheckpointStore()
	if err := checkpoints.Save(context.Background(), interruptedID, &rig.Checkpoint{
		SessionID: interruptedID, Phase: rig.CheckpointPhaseToolPending,
	}); err != nil {
		t.Fatalf("save interrupted checkpoint: %v", err)
	}
	if err := checkpoints.Save(context.Background(), finishedID, &rig.Checkpoint{
		SessionID: finishedID, Phase: rig.CheckpointPhaseCompleted,
	}); err != nil {
		t.Fatalf("save completed checkpoint: %v", err)
	}

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	if session := mustSession(t, second, interruptedID); !session.Interrupted {
		t.Fatal("restored session with an unfinished checkpoint is not marked interrupted")
	}
	if session := mustSession(t, second, finishedID); session.Interrupted {
		t.Fatal("restored session with a completed checkpoint is marked interrupted")
	}

	// The next run start clears the read-model hint.
	if err := second.Send(interruptedID, "resume"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, second, interruptedID)
	if session := mustSession(t, second, interruptedID); session.Interrupted {
		t.Fatal("interrupted hint survived a new run")
	}
}
