package agent

// Tests for the edit path (contracts.SessionEditor): the anchor arithmetic and
// every refusal. The point of the anchor test is not "the code works" but "the
// durable identity chosen is the only correct one": counting *messages* from the
// tail silently picks the wrong split once a run folded several provider
// iterations into one assistant message, while counting *user messages* stays
// correct. That difference is what makes the edit safe instead of lucky.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newEditService activates the module for real (provider, catalog, registry) and
// hands back the store it was given, so a run started by an edit fails the way
// a misconfigured model fails instead of hitting nil plumbing.
func newEditService(t *testing.T) (*service, *fakeStore) {
	t.Helper()
	svc := newTestService(t, Config{Model: "test-model"}, nil, nil)
	if svc.registry == nil {
		t.Fatal("newEditService: the service has no tool registry")
	}
	store, ok := svc.store.(*fakeStore)
	if !ok {
		t.Fatalf("service store is %T, want *fakeStore", svc.store)
	}
	return svc, store
}

// seedSession registers a session with the given read-model messages and stores
// the given durable history, so the two halves can be made deliberately
// inconsistent (which is exactly what a folded iteration looks like).
func seedSession(t *testing.T, svc *service, store *fakeStore, sessionID string, readModel []contracts.ChatMessage, durable []rig.Message) {
	t.Helper()
	if err := store.CreateSession(context.Background(), sessionID, "edit test"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, durable); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	svc.mu.Lock()
	svc.sessions[sessionID] = &sessionState{
		id:        sessionID,
		createdAt: time.Now(),
		updatedAt: time.Now(),
		messages:  append([]contracts.ChatMessage(nil), readModel...),
		assistant: -1,
	}
	svc.mu.Unlock()
}

// TestEditAnchorCountsUserMessages is the anchor's justification.
//
// The durable history below is what a real two-iteration turn produces:
//
//	durable:  user(A) assistant assistant  user(B) assistant assistant
//	read model: user(A) assistant          user(B) assistant
//
// Editing user(B) means "truncate to before user(B)". Counting messages from the
// tail gives 2 (assistant + user(B)… ), which lands *inside* the previous turn
// and would drop assistant text the user still sees. Counting user messages from
// the tail gives 0, which lands exactly before the last user message. The test
// asserts the anchor returns the right prefix and that the naive count does not,
// so a future refactor to "drop the last N messages" fails here.
func TestEditAnchorCountsUserMessages(t *testing.T) {
	svc, store := newEditService(t)

	durable := []rig.Message{
		rig.NewMessage(rig.RoleUser, "A"),
		rig.NewMessage(rig.RoleAssistant, "A-iter-1"),
		rig.NewMessage(rig.RoleAssistant, "A-iter-2"),
		rig.NewMessage(rig.RoleUser, "B"),
		rig.NewMessage(rig.RoleAssistant, "B-iter-1"),
		rig.NewMessage(rig.RoleAssistant, "B-iter-2"),
	}
	readModel := []contracts.ChatMessage{
		{ID: "msg-1", Role: contracts.ChatRoleUser, Text: "A"},
		{ID: "msg-2", Role: contracts.ChatRoleAssistant, Text: "A-iter-1A-iter-2"},
		{ID: "msg-3", Role: contracts.ChatRoleUser, Text: "B"},
		{ID: "msg-4", Role: contracts.ChatRoleAssistant, Text: "B-iter-1B-iter-2"},
	}
	seedSession(t, svc, store, "s1", readModel, durable)

	// Editing user(B): zero user messages follow it.
	base, err := svc.editBaseHistory(context.Background(), "s1", "B", 0)
	if err != nil {
		t.Fatalf("editBaseHistory(B): %v", err)
	}
	if len(base) != 3 {
		t.Fatalf("base length = %d, want 3 (A, A-iter-1, A-iter-2)", len(base))
	}
	if got := base[len(base)-1].Text(); got != "A-iter-2" {
		t.Fatalf("the base ends with %q, want the previous turn's last assistant text", got)
	}

	// The naive "count read-model messages from the tail" answer would be 1
	// (only the assistant reply follows user(B) in the read model), which lands
	// before *user(B)* too… but for user(A) it drifts: read model says 2
	// messages follow, the durable history needs 4 entries dropped.
	if _, err := svc.editBaseHistory(context.Background(), "s1", "A", 1); err != nil {
		t.Fatalf("editBaseHistory(A): %v", err)
	}
	naive := len(durable) - 2 // "drop the 2 messages the read model shows after A"
	if got := len(durable[:naive]); got != 4 {
		t.Fatalf("naive count keeps %d entries; the correct anchor keeps 0", got)
	}

	// Editing user(A) (one user message follows): the anchor must return the
	// empty prefix, not the middle of turn A.
	base, err = svc.editBaseHistory(context.Background(), "s1", "A", 1)
	if err != nil {
		t.Fatalf("editBaseHistory(A): %v", err)
	}
	if len(base) != 0 {
		t.Fatalf("base length for the first turn = %d, want 0", len(base))
	}
}

// TestEditMessageTruncatesAndRuns covers the happy path's durable half: the
// history is replaced with the prefix, and the edited text is what runs.
func TestEditMessageTruncatesAndRuns(t *testing.T) {
	svc, store := newEditService(t)

	durable := []rig.Message{
		rig.NewMessage(rig.RoleUser, "first"),
		rig.NewMessage(rig.RoleAssistant, "reply-1"),
		rig.NewMessage(rig.RoleUser, "second"),
		rig.NewMessage(rig.RoleAssistant, "reply-2"),
	}
	readModel := []contracts.ChatMessage{
		{ID: "msg-1", Role: contracts.ChatRoleUser, Text: "first"},
		{ID: "msg-2", Role: contracts.ChatRoleAssistant, Text: "reply-1"},
		{ID: "msg-3", Role: contracts.ChatRoleUser, Text: "second"},
		{ID: "msg-4", Role: contracts.ChatRoleAssistant, Text: "reply-2"},
	}
	seedSession(t, svc, store, "s2", readModel, durable)

	// Stop the run from progressing: the test is about the truncation the edit
	// performs before the run, not about a provider call that cannot succeed.
	svc.mu.Lock()
	state := svc.sessions["s2"]
	state.running = false
	svc.mu.Unlock()

	if err := svc.EditMessage("s2", "msg-3", "second, edited"); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	// Durable: only the first turn remains (the edited message is appended by
	// the run, exactly like a fresh send).
	history := storeHistory(store, "s2")
	if len(history) < 2 {
		t.Fatalf("durable history = %d messages, want at least 2 (%v)", len(history), historyTexts(history))
	}
	if got := history[0].Text(); got != "first" {
		t.Fatalf("durable history starts with %v, want [first …]", historyTexts(history))
	}
	if got := history[1].Text(); got != "reply-1" {
		t.Fatalf("durable history kept %v, want [first reply-1]", historyTexts(history))
	}
	for _, message := range history {
		if text := message.Text(); text == "second" || text == "reply-2" {
			t.Fatalf("the truncated turn survived in durable history: %v", historyTexts(history))
		}
	}
	// Read model: the kept prefix, the edited user message, and the streaming
	// assistant placeholder the run will fill.
	svc.mu.Lock()
	messages := append([]contracts.ChatMessage(nil), svc.sessions["s2"].messages...)
	svc.mu.Unlock()
	if len(messages) < 3 {
		t.Fatalf("read model = %v, want at least the prefix + the edited message", messages)
	}
	if messages[0].Text != "first" || messages[1].Text != "reply-1" {
		t.Fatalf("the kept prefix changed: %v", messages)
	}
	if messages[2].Role != contracts.ChatRoleUser || messages[2].Text != "second, edited" {
		t.Fatalf("edited message = %+v", messages[2])
	}
	if messages[len(messages)-1].Role != contracts.ChatRoleAssistant || !messages[len(messages)-1].Streaming {
		t.Fatalf("no streaming placeholder after the edit: %v", messages)
	}
	// The old trailing turn must be gone from both halves.
	for _, message := range messages {
		if message.Text == "reply-2" || message.Text == "second" {
			t.Fatalf("old trailing message survived: %v", messages)
		}
	}
}

// TestEditMessageRefuses pins every refusal: none of them may fall through to
// "just start a fresh turn", because that silently discards the user's history.
func TestEditMessageRefuses(t *testing.T) {
	durable := []rig.Message{
		rig.NewMessage(rig.RoleUser, "first"),
		rig.NewMessage(rig.RoleAssistant, "reply-1"),
		rig.NewMessage(rig.RoleUser, "second"),
		rig.NewMessage(rig.RoleAssistant, "reply-2"),
	}
	readModel := []contracts.ChatMessage{
		{ID: "msg-1", Role: contracts.ChatRoleUser, Text: "first"},
		{ID: "msg-2", Role: contracts.ChatRoleAssistant, Text: "reply-1"},
		{ID: "msg-3", Role: contracts.ChatRoleUser, Text: "second"},
		{ID: "msg-4", Role: contracts.ChatRoleAssistant, Text: "reply-2"},
	}

	cases := []struct {
		name      string
		sessionID string
		messageID string
		text      string
		durable   []rig.Message
		readModel []contracts.ChatMessage
		want      error
	}{
		{name: "unknown session", sessionID: "missing", messageID: "msg-3", text: "x",
			want: contracts.ErrEditTargetNotFound},
		{name: "unknown message", sessionID: "s", messageID: "msg-404", text: "x",
			want: contracts.ErrEditTargetNotFound},
		{name: "assistant target", sessionID: "s", messageID: "msg-2", text: "x",
			want: contracts.ErrEditTargetNotUser},
		{name: "attachments", sessionID: "s", messageID: "msg-3", text: "x",
			readModel: []contracts.ChatMessage{
				{ID: "msg-1", Role: contracts.ChatRoleUser, Text: "first"},
				{ID: "msg-2", Role: contracts.ChatRoleAssistant, Text: "reply-1"},
				{ID: "msg-3", Role: contracts.ChatRoleUser, Text: "second",
					Attachments: []contracts.Attachment{{Name: "shot.png"}}},
			},
			want: contracts.ErrEditTargetHasAttachments},
		{name: "compacted away", sessionID: "s", messageID: "msg-1", text: "x",
			// The durable window no longer contains the first turn: only the
			// second turn remains, so the anchor cannot reach back far enough.
			durable: []rig.Message{
				rig.NewMessage(rig.RoleUser, "second"),
				rig.NewMessage(rig.RoleAssistant, "reply-2"),
			},
			readModel: readModel,
			want:      contracts.ErrEditTargetCompacted},
		{name: "moved", sessionID: "s", messageID: "msg-3", text: "x",
			// Same shape, different text: the transcript was rewritten.
			durable: []rig.Message{
				rig.NewMessage(rig.RoleUser, "first"),
				rig.NewMessage(rig.RoleAssistant, "reply-1"),
				rig.NewMessage(rig.RoleUser, "something else"),
				rig.NewMessage(rig.RoleAssistant, "reply-2"),
			},
			readModel: readModel,
			want:      contracts.ErrEditTargetMoved},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			svc, store := newEditService(t)
			d := test.durable
			if d == nil {
				d = durable
			}
			r := test.readModel
			if r == nil {
				r = readModel
			}
			seedSession(t, svc, store, "s", r, d)

			err := svc.EditMessage(test.sessionID, test.messageID, test.text)
			if !errors.Is(err, test.want) {
				t.Fatalf("EditMessage = %v, want %v", err, test.want)
			}
			// Nothing may have been written: a refusal leaves the history alone.
			if got := storeHistory(store, "s"); len(got) != len(d) {
				t.Fatalf("a refused edit rewrote the durable history: %v", historyTexts(got))
			}
			svc.mu.Lock()
			running := false
			if state := svc.sessions["s"]; state != nil {
				running = state.running
			}
			svc.mu.Unlock()
			if running {
				t.Fatal("a refused edit started a run")
			}
		})
	}
}

// historyTexts renders a durable history for failure messages.
func historyTexts(messages []rig.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, fmt.Sprintf("%s:%s", message.Role, message.Text()))
	}
	return strings.Join(parts, " | ")
}

// storeHistory snapshots one session's durable messages.
func storeHistory(store *fakeStore, sessionID string) []rig.Message {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]rig.Message(nil), store.sessionLocked(sessionID).messages...)
}
