package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestForkCopiesHistory pins Store.ForkSession's copy contract: the fork is a
// new session in the same workspace titled "<title> (fork)", and the message,
// event, tool-call and feedback history is duplicated under fresh primary
// keys while the source session stays untouched. It also proves deleting the
// fork removes only the fork.
func TestForkCopiesHistory(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "fork-ws", t.TempDir())
	src := mustSession(t, s, ws.ID, "source")

	user, err := s.AppendMessage(Message{
		SessionID: src.ID, Role: "user", Content: "hello", TurnID: "turn-1",
	})
	if err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	assistant, err := s.AppendMessage(Message{
		SessionID: src.ID, Role: "assistant", Content: "world",
		Reasoning: "reasoned", TurnID: "turn-1",
	})
	if err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}
	if _, err := s.AppendEvent(src.ID, Event{
		Type: "token", Payload: json.RawMessage(`{"text":"world"}`),
	}); err != nil {
		t.Fatalf("AppendEvent(token): %v", err)
	}
	if _, err := s.AppendEvent(src.ID, Event{
		Type: "tool_start", Payload: json.RawMessage(`{"call_id":"call-1","name":"echo"}`),
	}); err != nil {
		t.Fatalf("AppendEvent(tool_start): %v", err)
	}
	if err := s.RecordToolCall(ToolCall{
		ID: "call-1", SessionID: src.ID, MessageID: assistant.ID,
		Name: "echo", Args: `{"x":1}`, Result: "ok", Status: "done",
		StartedAt: nowUTC(), EndedAt: nowUTC(),
	}); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}
	if _, err := s.AppendFeedback(Feedback{
		SessionID: src.ID, MessageID: assistant.ID, Rating: "good", Note: "clear",
	}); err != nil {
		t.Fatalf("AppendFeedback: %v", err)
	}
	if err := s.SetSetting("session."+src.ID+".permission", "full"); err != nil {
		t.Fatalf("SetSetting(permission): %v", err)
	}

	forked, err := s.ForkSession(src.ID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if forked == nil {
		t.Fatal("ForkSession returned a nil session")
	}
	if forked.ID == src.ID || strings.TrimSpace(forked.ID) == "" {
		t.Fatalf("fork id = %q, want a fresh id (source %q)", forked.ID, src.ID)
	}
	if forked.WorkspaceID != src.WorkspaceID {
		t.Fatalf("fork workspace = %q, want %q", forked.WorkspaceID, src.WorkspaceID)
	}
	if forked.Title != "source (fork)" {
		t.Fatalf("fork title = %q, want %q", forked.Title, "source (fork)")
	}
	if forked.Status != SessionIdle {
		t.Fatalf("fork status = %q, want %q", forked.Status, SessionIdle)
	}
	if forked.Archived {
		t.Fatal("fork is archived, want visible")
	}

	messages, err := s.ListMessages(forked.ID)
	if err != nil {
		t.Fatalf("ListMessages(fork): %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("fork messages = %d, want 2", len(messages))
	}
	if messages[0].SessionID != forked.ID || messages[1].SessionID != forked.ID {
		t.Fatalf("fork message session ids = %q/%q, want %q",
			messages[0].SessionID, messages[1].SessionID, forked.ID)
	}
	if messages[0].ID == user.ID || messages[1].ID == assistant.ID {
		t.Fatal("fork reused a source message id; ids must be fresh")
	}
	if messages[0].Content != "hello" || messages[1].Content != "world" {
		t.Fatalf("fork message contents = %q/%q, want hello/world",
			messages[0].Content, messages[1].Content)
	}
	if messages[1].Reasoning != "reasoned" || messages[1].TurnID != "turn-1" {
		t.Fatalf("fork assistant message = %+v, want reasoning/turn preserved", messages[1])
	}

	events, err := s.ListEvents(forked.ID)
	if err != nil {
		t.Fatalf("ListEvents(fork): %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("fork events = %d, want 2", len(events))
	}
	for i, ev := range events {
		if ev.SessionID != forked.ID || ev.Seq != int64(i+1) {
			t.Fatalf("fork event %d = %+v, want session %q seq %d", i, ev, forked.ID, i+1)
		}
	}
	if events[0].Type != "token" || events[1].Type != "tool_start" {
		t.Fatalf("fork event types = %q/%q, want token/tool_start", events[0].Type, events[1].Type)
	}
	if string(events[0].Payload) != `{"text":"world"}` {
		t.Fatalf("fork event payload = %s, want source payload", events[0].Payload)
	}

	calls, err := s.ToolCallsForSession(forked.ID)
	if err != nil {
		t.Fatalf("ToolCallsForSession(fork): %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("fork tool calls = %d, want 1", len(calls))
	}
	if calls[0].ID == "call-1" {
		t.Fatal("fork reused the source tool-call id; ids must be fresh")
	}
	if calls[0].Name != "echo" || calls[0].Status != "done" || calls[0].Result != "ok" {
		t.Fatalf("fork tool call = %+v, want echo/done/ok", calls[0])
	}
	if calls[0].MessageID != messages[1].ID {
		t.Fatalf("fork tool call message = %q, want fork assistant %q",
			calls[0].MessageID, messages[1].ID)
	}

	forkFeedback, err := s.ListFeedback(forked.ID)
	if err != nil {
		t.Fatalf("ListFeedback(fork): %v", err)
	}
	if len(forkFeedback) != 1 || forkFeedback[0].Rating != "good" || forkFeedback[0].Note != "clear" {
		t.Fatalf("fork feedback = %+v, want one good/clear row", forkFeedback)
	}
	if forkFeedback[0].MessageID != messages[1].ID {
		t.Fatalf("fork feedback message = %q, want fork assistant %q",
			forkFeedback[0].MessageID, messages[1].ID)
	}
	if got, err := s.GetSetting("session." + forked.ID + ".permission"); err != nil || got != "full" {
		t.Fatalf("fork permission setting = %q, %v; want full, nil", got, err)
	}

	// The source session is untouched.
	srcAfter, err := s.GetSession(src.ID)
	if err != nil {
		t.Fatalf("GetSession(source): %v", err)
	}
	if srcAfter.Title != "source" {
		t.Fatalf("source title = %q, want unchanged", srcAfter.Title)
	}
	srcMessages, err := s.ListMessages(src.ID)
	if err != nil || len(srcMessages) != 2 {
		t.Fatalf("source messages after fork = %d, %v; want 2, nil", len(srcMessages), err)
	}

	// Deleting the fork removes only the fork.
	if err := s.DeleteSession(forked.ID); err != nil {
		t.Fatalf("DeleteSession(fork): %v", err)
	}
	if _, err := s.GetSession(forked.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession(deleted fork) error = %v, want ErrNotFound", err)
	}
	if _, err := s.GetSession(src.ID); err != nil {
		t.Fatalf("source disappeared after deleting its fork: %v", err)
	}

	// Unknown sources are a not-found error, not a partial copy.
	if _, err := s.ForkSession("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ForkSession(missing) error = %v, want ErrNotFound", err)
	}
}
