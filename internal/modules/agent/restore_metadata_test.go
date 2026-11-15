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

// appendDurableEventAt records one durable session event at an explicit time, so
// restore-time backfill is asserted against deterministic clocks.
func appendDurableEventAt(t *testing.T, store *fakeStore, sessionID, eventType string,
	at time.Time, payload map[string]any) {
	t.Helper()
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode %s: %v", eventType, err)
	}
	if err := store.AppendEvent(context.Background(), rig.SessionEvent{
		SessionID: sessionID, Type: eventType, Time: at, Payload: encoded,
	}); err != nil {
		t.Fatalf("append %s: %v", eventType, err)
	}
}

// appendUserMessageAt records one durable human user_message event at a time.
func appendUserMessageAt(t *testing.T, store *fakeStore, sessionID, text string, at time.Time) {
	t.Helper()
	appendDurableEventAt(t, store, sessionID, "user_message", at, map[string]any{
		"text": text, "origin": contracts.AgentRunOriginUser,
	})
}

// appendRunEndAt records one durable run_end event at a time.
func appendRunEndAt(t *testing.T, store *fakeStore, sessionID string, at time.Time) {
	t.Helper()
	appendDurableEventAt(t, store, sessionID, "run_end", at, map[string]any{
		"ok": true, "error": "", "finishReason": "stop",
	})
}

// TestRestoreBackfillsUserAndTurnEndTimes covers the honest backfill: a restored
// user message takes its durable user_message clock, and the turn's final
// assistant message takes the run_end clock of the run that ended that turn.
func TestRestoreBackfillsUserAndTurnEndTimes(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("times")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t1 := time.Date(2026, 11, 15, 9, 30, 0, 0, time.Local)
	t2 := t1.Add(90 * time.Second)
	t3 := t2.Add(2 * time.Minute)
	t4 := t3.Add(45 * time.Second)
	history := []rig.Message{
		rig.NewMessage(rig.RoleUser, "第一条"),
		rig.NewMessage(rig.RoleAssistant, "答案一"),
		rig.NewMessage(rig.RoleUser, "第二条"),
		rig.NewMessage(rig.RoleAssistant, "答案二"),
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, history); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	appendUserMessageAt(t, store, sessionID, "第一条", t1)
	appendRunEndAt(t, store, sessionID, t2)
	appendUserMessageAt(t, store, sessionID, "第二条", t3)
	appendRunEndAt(t, store, sessionID, t4)

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	if len(session.Messages) != 4 {
		t.Fatalf("restored messages = %d, want 4", len(session.Messages))
	}
	want := []time.Time{t1, t2, t3, t4}
	for index, expected := range want {
		if !session.Messages[index].CreatedAt.Equal(expected) {
			t.Fatalf("message %d CreatedAt = %v, want %v", index, session.Messages[index].CreatedAt, expected)
		}
	}
}

// TestRestoreAssistantTimeOnlyOnTurnFinalStep pins the honest granularity: the
// run_end clock belongs to the step that ended the turn, not to the tool steps
// in the middle of it.
func TestRestoreAssistantTimeOnlyOnTurnFinalStep(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("steps")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	userAt := time.Date(2026, 11, 15, 10, 0, 0, 0, time.Local)
	endAt := userAt.Add(3 * time.Minute)
	history := []rig.Message{
		rig.NewMessage(rig.RoleUser, "跑一下测试"),
		rig.NewMessage(rig.RoleAssistant, "先读文件"),
		rig.NewMessage(rig.RoleAssistant, "测试通过"),
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, history); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	appendUserMessageAt(t, store, sessionID, "跑一下测试", userAt)
	appendRunEndAt(t, store, sessionID, endAt)

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	if !session.Messages[0].CreatedAt.Equal(userAt) {
		t.Fatalf("user CreatedAt = %v, want %v", session.Messages[0].CreatedAt, userAt)
	}
	if !session.Messages[1].CreatedAt.IsZero() {
		t.Fatalf("intermediate step got a fabricated clock: %v", session.Messages[1].CreatedAt)
	}
	if !session.Messages[2].CreatedAt.Equal(endAt) {
		t.Fatalf("final assistant CreatedAt = %v, want %v", session.Messages[2].CreatedAt, endAt)
	}
}

// TestRestoreWithoutDurableEventsKeepsZeroTimes covers the honest empty case: a
// history without durable timing events must not invent clocks.
func TestRestoreWithoutDurableEventsKeepsZeroTimes(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("no events")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	history := []rig.Message{
		rig.NewMessage(rig.RoleUser, "没有事件"),
		rig.NewMessage(rig.RoleAssistant, "也没有时钟"),
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, history); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	for index, message := range session.Messages {
		if !message.CreatedAt.IsZero() {
			t.Fatalf("message %d got a clock without a durable event: %v", index, message.CreatedAt)
		}
	}
}

// TestRestoreCompactedTurnsDoNotMismatchTimes covers compaction: durable turns
// that are no longer in the history must not hand their clocks to the surviving
// text, and the surviving turn still takes its own clocks.
func TestRestoreCompactedTurnsDoNotMismatchTimes(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("compacted times")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	oldUserAt := time.Date(2026, 11, 15, 8, 0, 0, 0, time.Local)
	oldEndAt := oldUserAt.Add(time.Minute)
	keptUserAt := time.Date(2026, 11, 15, 11, 0, 0, 0, time.Local)
	keptEndAt := keptUserAt.Add(2 * time.Minute)
	appendUserMessageAt(t, store, sessionID, "被压缩掉的旧问题", oldUserAt)
	appendRunEndAt(t, store, sessionID, oldEndAt)
	appendUserMessageAt(t, store, sessionID, "保留下来的问题", keptUserAt)
	appendRunEndAt(t, store, sessionID, keptEndAt)

	history := []rig.Message{
		rig.NewMessage(rig.RoleUser, "保留下来的问题"),
		rig.NewMessage(rig.RoleAssistant, "保留的回答"),
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, history); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	if !session.Messages[0].CreatedAt.Equal(keptUserAt) {
		t.Fatalf("user CreatedAt = %v, want the kept turn's %v", session.Messages[0].CreatedAt, keptUserAt)
	}
	if !session.Messages[1].CreatedAt.Equal(keptEndAt) {
		t.Fatalf("assistant CreatedAt = %v, want the kept turn's end %v", session.Messages[1].CreatedAt, keptEndAt)
	}
}

// TestRestoreRewindDoesNotTakeLaterTurnTimes covers a rewound/forked history: a
// turn that the durable log still knows but the history no longer shows must not
// rewind a later clock onto the surviving turn.
func TestRestoreRewindDoesNotTakeLaterTurnTimes(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("rewound times")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	firstUserAt := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	firstEndAt := firstUserAt.Add(time.Minute)
	laterUserAt := firstEndAt.Add(10 * time.Minute)
	laterEndAt := laterUserAt.Add(time.Minute)
	appendUserMessageAt(t, store, sessionID, "保留的这一轮", firstUserAt)
	appendRunEndAt(t, store, sessionID, firstEndAt)
	appendUserMessageAt(t, store, sessionID, "回退掉的下一轮", laterUserAt)
	appendRunEndAt(t, store, sessionID, laterEndAt)

	history := []rig.Message{
		rig.NewMessage(rig.RoleUser, "保留的这一轮"),
		rig.NewMessage(rig.RoleAssistant, "保留的回答"),
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, history); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	if !session.Messages[0].CreatedAt.Equal(firstUserAt) {
		t.Fatalf("user CreatedAt = %v, want %v", session.Messages[0].CreatedAt, firstUserAt)
	}
	if !session.Messages[1].CreatedAt.Equal(firstEndAt) {
		t.Fatalf("assistant CreatedAt = %v, want the first turn's end %v (not the later turn)", session.Messages[1].CreatedAt, firstEndAt)
	}
}

// TestRestoreIgnoresRunEndWithoutUserMessage covers a bounded event window whose
// start cuts off the user_message: a run_end with no turn to end is ignored.
func TestRestoreIgnoresRunEndWithoutUserMessage(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	first := newQueueService(t, store, settings, &captureProvider{})
	sessionID, err := first.NewSession("orphan run end")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	history := []rig.Message{
		rig.NewMessage(rig.RoleUser, "历史里的问题"),
		rig.NewMessage(rig.RoleAssistant, "历史里的回答"),
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, history); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	appendRunEndAt(t, store, sessionID, time.Date(2026, 11, 15, 12, 0, 0, 0, time.Local))

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	session := mustSession(t, second, sessionID)
	for index, message := range session.Messages {
		if !message.CreatedAt.IsZero() {
			t.Fatalf("message %d got a clock from an orphan run_end: %v", index, message.CreatedAt)
		}
	}
}
