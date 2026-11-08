package agent

// Tests for durable failed/cancelled turns: a graceful run failure or a user
// cancel must not lose the streamed partial answer, must clear the interruption
// checkpoint (so the next run does not replay the crash-recovery marker) and
// must re-surface the failure from the durable run_end event after a restart.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func newPersistenceTestService(store *fakeStore) *service {
	return newService(Config{}, rigtools.NewRegistry(), newFakeSettings(nil), &fakeLogger{}, store, fakeApprovals{})
}

// seedRunFailure records a completed history plus the events a failed turn
// leaves behind (user_message, run_start, run_end{ok:false}).
func seedRunFailure(t *testing.T, store *fakeStore, sessionID, errText string, history ...rig.Message) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateSession(ctx, sessionID, "session"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.History().Append(ctx, sessionID, history...); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	for _, event := range []rig.SessionEvent{
		{SessionID: sessionID, Type: "user_message", Payload: []byte(`{"text":"hello"}`), Time: time.Now()},
		{SessionID: sessionID, Type: "run_end", Payload: []byte(`{"ok":false,"error":` + jsonQuote(errText) + `}`), Time: time.Now()},
	} {
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
}

// jsonQuote renders value as a JSON string literal for the test event payloads.
func jsonQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func TestRestoreHydratesFailedTurn(t *testing.T) {
	store := newFakeStore()
	seedRunFailure(t, store, "sess-1", "openai: chat completions failed: 401 Unauthorized: bad key",
		rig.NewMessage(rig.RoleUser, "hello"))
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	state, ok := svc.sessions["sess-1"]
	if !ok {
		t.Fatal("restore did not hydrate the session")
	}
	if !strings.Contains(state.err, "401 Unauthorized") {
		t.Fatalf("session error = %q, want the durable run error", state.err)
	}
	if len(state.messages) != 2 {
		t.Fatalf("messages = %+v, want user + synthetic assistant error", state.messages)
	}
	last := state.messages[len(state.messages)-1]
	if last.Role != contracts.ChatRoleAssistant || !strings.Contains(last.Error, "401 Unauthorized") {
		t.Fatalf("last message = %+v, want an assistant error row", last)
	}
	if last.ErrorKind != contracts.MessageErrorKindAuth {
		t.Fatalf("error kind = %q, want %q", last.ErrorKind, contracts.MessageErrorKindAuth)
	}
}

func TestRestoreAttachesErrorToPersistedPartialAssistant(t *testing.T) {
	store := newFakeStore()
	partial := rig.NewMessage(rig.RoleAssistant, "partial answer")
	partial.Reasoning = "thinking so far"
	seedRunFailure(t, store, "sess-1", "cancelled",
		rig.NewMessage(rig.RoleUser, "write a long essay"),
		partial)
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	state := svc.sessions["sess-1"]
	if len(state.messages) != 2 {
		t.Fatalf("messages = %+v, want the persisted partial assistant only", state.messages)
	}
	last := state.messages[len(state.messages)-1]
	if last.Role != contracts.ChatRoleAssistant || last.Text != "partial answer" || last.Reasoning != "thinking so far" {
		t.Fatalf("last message = %+v, want the persisted partial answer", last)
	}
	if last.Error != "cancelled" || last.ErrorKind != contracts.MessageErrorKindCancelled {
		t.Fatalf("last error = %q/%q, want cancelled", last.Error, last.ErrorKind)
	}
}

func TestFinishPersistsPartialAssistantAndClearsCheckpoint(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()
	if err := store.CreateSession(ctx, "sess-1", "session"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.History().Append(ctx, "sess-1", rig.NewMessage(rig.RoleUser, "long prompt")); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	if err := store.CheckpointStore().Save(ctx, "sess-1", &rig.Checkpoint{
		SessionID: "sess-1", Phase: rig.CheckpointPhaseRequested,
	}); err != nil {
		t.Fatalf("Save checkpoint: %v", err)
	}
	svc := newPersistenceTestService(store)
	svc.sessions["sess-1"] = &sessionState{
		id:        "sess-1",
		assistant: 1,
		messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "long prompt"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "partial answer", Reasoning: "thinking", Streaming: true},
		},
	}
	svc.finish("sess-1", ctx, errors.New("provider went away after streaming"))

	messages, err := store.History().Load(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Load history: %v", err)
	}
	if len(messages) != 2 || messages[1].Role != rig.RoleAssistant || messages[1].Reasoning != "thinking" {
		t.Fatalf("history = %+v, want user + partial assistant", messages)
	}
	if text := messageTextParts(messages[1]); text != "partial answer" {
		t.Fatalf("partial assistant text = %q", text)
	}
	if checkpoint, _ := store.CheckpointStore().Load(ctx, "sess-1"); checkpoint != nil {
		t.Fatalf("checkpoint = %+v, want cleared for a graceful stop", checkpoint)
	}
	events, err := store.Last(ctx, "sess-1", 1)
	if err != nil || len(events) != 1 || events[0].Type != "run_end" ||
		!strings.Contains(string(events[0].Payload), `"ok":false`) {
		t.Fatalf("last event = %+v/%v, want a failed run_end", events, err)
	}
}

func TestFinishCancelPersistsPartialAssistant(t *testing.T) {
	store := newFakeStore()
	base := context.Background()
	if err := store.CreateSession(base, "sess-1", "session"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.History().Append(base, "sess-1", rig.NewMessage(rig.RoleUser, "stop me")); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	svc := newPersistenceTestService(store)
	svc.sessions["sess-1"] = &sessionState{
		id:        "sess-1",
		assistant: 1,
		messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "stop me"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "partial", Streaming: true},
		},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	svc.finish("sess-1", cancelled, nil)

	messages, _ := store.History().Load(base, "sess-1")
	if len(messages) != 2 || messages[1].Role != rig.RoleAssistant || messageTextParts(messages[1]) != "partial" {
		t.Fatalf("history = %+v, want the partial cancelled answer persisted", messages)
	}
	if checkpoint, _ := store.CheckpointStore().Load(base, "sess-1"); checkpoint != nil {
		t.Fatalf("checkpoint = %+v, want cleared on cancel", checkpoint)
	}
}

func TestFinishDoesNotPersistToolCallTurn(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()
	if err := store.CreateSession(ctx, "sess-1", "session"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.History().Append(ctx, "sess-1", rig.NewMessage(rig.RoleUser, "run it")); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	if err := store.CheckpointStore().Save(ctx, "sess-1", &rig.Checkpoint{
		SessionID: "sess-1", Phase: rig.CheckpointPhaseToolPending,
	}); err != nil {
		t.Fatalf("Save checkpoint: %v", err)
	}
	svc := newPersistenceTestService(store)
	svc.sessions["sess-1"] = &sessionState{
		id:        "sess-1",
		assistant: 1,
		messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "run it"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "about to run",
				Tools: []contracts.ToolCall{{ID: "call-1", Name: "bash", Status: "running"}}},
		},
	}
	svc.finish("sess-1", ctx, errors.New("tool round failed"))

	messages, _ := store.History().Load(ctx, "sess-1")
	if len(messages) != 1 {
		t.Fatalf("history = %+v, want the tool-call turn left out of history", messages)
	}
	if checkpoint, _ := store.CheckpointStore().Load(ctx, "sess-1"); checkpoint != nil {
		t.Fatalf("checkpoint = %+v, want cleared", checkpoint)
	}
}

// messageText joins the text content parts of a Rig message.
func messageTextParts(message rig.Message) string {
	var builder strings.Builder
	for _, part := range message.Content {
		if part.Type == rig.PartText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}
