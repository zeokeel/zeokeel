package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/sessionreference"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// refRespond answers every model call with a short final text.
func refRespond(_ context.Context, _ runtime.Request, _ int) []runtime.Chunk {
	return []runtime.Chunk{{Text: "done", Done: true}}
}

// seedReferenceSession creates a session and appends its stored messages, then
// leaves it as the controller's current session. The referenced session is
// created before the citing one, exactly like a chat the user wants to cite.
func seedReferenceSession(t *testing.T, h *harness, title string, messages ...store.Message) string {
	t.Helper()
	id, err := h.ctrl.CreateSession(context.Background(), title)
	if err != nil {
		t.Fatalf("CreateSession(%q): %v", title, err)
	}
	for _, message := range messages {
		message.SessionID = id
		if _, err := h.store.AppendMessage(message); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}
	return id
}

// storeHistoryAdapter feeds the runtime its prior conversation from the
// store's message rows, like the app's production history seam. The controller
// persists messages itself, so Append is a no-op (matching the production
// adapter) and only Messages is read.
type storeHistoryAdapter struct{ st *store.Store }

func (h storeHistoryAdapter) Messages(_ context.Context, sessionID string) ([]runtime.Message, error) {
	rows, err := h.st.ListMessages(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, runtime.Message{
			Role:      runtime.Role(row.Role),
			Content:   row.Content,
			Reasoning: row.Reasoning,
		})
	}
	return out, nil
}

func (storeHistoryAdapter) Append(context.Context, string, ...runtime.Message) error { return nil }

// newRefHarness builds the controller/runtime wiring newHarness builds, plus
// the store-backed History the production app uses. Multi-turn requests can
// then replay persisted rows, which is what the "no reinjection" and reopen
// contracts need.
func newRefHarness(t *testing.T, provider runtime.Provider) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ws, err := st.CreateWorkspace("test", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	registry := runtime.NewRegistry()
	if err := registry.Register(echoTool{}); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	agent, err := runtime.New(runtime.Config{
		Provider:    provider,
		Tools:       registry,
		Instruction: "test agent",
		History:     storeHistoryAdapter{st: st},
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	h := &harness{t: t, dir: dir, store: st, agent: agent, workspace: ws.ID}
	h.newController()
	t.Cleanup(h.close)
	return h
}

// userMessagesOf filters one provider request down to its user-role messages.
func userMessagesOf(request runtime.Request) []runtime.Message {
	out := make([]runtime.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == runtime.RoleUser {
			out = append(out, message)
		}
	}
	return out
}

// referencedPayload extracts the <referenced-sessions> JSON from one context
// message and returns the per-source raw JSON objects.
func referencedPayload(t *testing.T, contextText string) []json.RawMessage {
	t.Helper()
	open := sessionreference.OpenTag + "\n"
	close := "\n" + sessionreference.CloseTag
	start := strings.Index(contextText, open)
	end := strings.LastIndex(contextText, close)
	if start < 0 || end <= start {
		t.Fatalf("context has no referenced-sessions payload: %q", contextText)
	}
	var sources []json.RawMessage
	if err := json.Unmarshal([]byte(contextText[start+len(open):end]), &sources); err != nil {
		t.Fatalf("payload is not a JSON array: %v", err)
	}
	return sources
}

// TestSubmitInjectsReferencedSessionContext covers the model-visible contract:
// a prompt citing another session produces two consecutive user messages — the
// unchanged prompt and a bounded untrusted snapshot with the fixed heading and
// warning — and the tool rows of the source session never travel.
func TestSubmitInjectsReferencedSessionContext(t *testing.T) {
	provider := &fakeProvider{respond: refRespond}
	h := newHarness(t, provider)
	sourceID := seedReferenceSession(t, h, "Source 会话",
		store.Message{Role: "user", Content: "source question"},
		store.Message{Role: "assistant", Content: "source answer"},
		store.Message{Role: "tool", Content: "TOOL-OUTPUT-MUST-NOT-TRAVEL"},
		store.Message{Role: "user", Content: sessionreference.SectionHeading + "\nNESTED-PROPAGATION"},
	)
	targetID, err := h.ctrl.CreateSession(context.Background(), "Target")
	if err != nil {
		t.Fatalf("CreateSession(target): %v", err)
	}
	mention := sessionreference.FormatMention("Source 会话", sourceID)
	// The same session cited twice is one source (first-appearance dedupe).
	prompt := "compare " + mention + " and again " + mention + " please"
	if err := h.ctrl.Submit(context.Background(), targetID, prompt); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	request, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider received no request")
	}
	users := userMessagesOf(request)
	if len(users) != 2 {
		t.Fatalf("provider user messages = %d, want the prompt plus one reference context: %+v", len(users), users)
	}
	if users[0].Content != prompt {
		t.Fatalf("provider prompt = %q, want the unchanged original %q", users[0].Content, prompt)
	}
	contextText := users[1].Content
	for _, want := range []string{
		sessionreference.SectionHeading,
		sessionreference.Intro,
		sessionreference.Warning,
		sessionreference.OpenTag,
		sessionreference.CloseTag,
		"source question",
		"source answer",
		"Source 会话",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("reference context is missing %q:\n%s", want, contextText)
		}
	}
	if strings.Contains(contextText, "TOOL-OUTPUT-MUST-NOT-TRAVEL") {
		t.Fatalf("source tool output travelled into the reference context:\n%s", contextText)
	}
	if strings.Contains(contextText, "NESTED-PROPAGATION") {
		t.Fatalf("a referenced session's own reference context was propagated:\n%s", contextText)
	}
	if got := len(contextText); got > sessionreference.MaxReferences*sessionreference.MaxReferenceBytes+4096 {
		t.Fatalf("reference context = %d bytes, want it bounded per source", got)
	}
	if sources := referencedPayload(t, contextText); len(sources) != 1 {
		t.Fatalf("payload sources = %d, want 1", len(sources))
	}

	// Persistence: the prompt, the reference context and the assistant reply
	// are stored in model order, so a reopen replays the same conversation.
	stored, err := h.store.ListMessages(targetID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("stored messages = %d, want prompt + context + assistant: %+v", len(stored), stored)
	}
	if stored[0].Role != string(state.RoleUser) || stored[0].Content != prompt {
		t.Fatalf("stored prompt row = %+v", stored[0])
	}
	if stored[1].Role != string(state.RoleUser) || stored[1].Content != contextText {
		t.Fatalf("stored reference context row = %+v", stored[1])
	}
	if stored[2].Role != string(state.RoleAssistant) {
		t.Fatalf("stored assistant row = %+v", stored[2])
	}
}

// TestSubmitWithoutReferenceAddsNoContext proves a plain prompt is untouched:
// one user message reaches the provider and only the prompt is persisted.
func TestSubmitWithoutReferenceAddsNoContext(t *testing.T) {
	provider := &fakeProvider{respond: refRespond}
	h := newHarness(t, provider)
	targetID, err := h.ctrl.CreateSession(context.Background(), "Plain")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(context.Background(), targetID, "just a normal prompt"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	request, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider received no request")
	}
	if users := userMessagesOf(request); len(users) != 1 || users[0].Content != "just a normal prompt" {
		t.Fatalf("provider user messages = %+v, want only the prompt", users)
	}
	stored, err := h.store.ListMessages(targetID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for _, message := range stored {
		if strings.Contains(message.Content, sessionreference.SectionHeading) {
			t.Fatalf("plain prompt was persisted with a reference context: %+v", message)
		}
	}
}

// TestReferenceContextIsNotReinjected covers the next-turn contract: the
// persisted context replays as history, but a later prompt without mentions
// does not append a second snapshot.
func TestReferenceContextIsNotReinjected(t *testing.T) {
	provider := &fakeProvider{respond: refRespond}
	h := newRefHarness(t, provider)
	sourceID := seedReferenceSession(t, h, "Source",
		store.Message{Role: "user", Content: "source body"})
	targetID, err := h.ctrl.CreateSession(context.Background(), "Target")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	prompt := "see " + sessionreference.FormatMention("Source", sourceID)
	if err := h.ctrl.Submit(context.Background(), targetID, prompt); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	if err := h.ctrl.Submit(context.Background(), targetID, "next prompt"); err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	second, ok := provider.requestAt(provider.requestCount() - 1)
	if !ok {
		t.Fatal("no second request")
	}
	users := userMessagesOf(second)
	last := users[len(users)-1]
	if last.Content != "next prompt" {
		t.Fatalf("second request last user message = %q, want the plain next prompt", last.Content)
	}
	contexts := 0
	for _, message := range users {
		if strings.Contains(message.Content, sessionreference.SectionHeading) {
			contexts++
		}
	}
	if contexts != 1 {
		t.Fatalf("second request carries %d reference contexts, want the single persisted one", contexts)
	}
}

// TestReopenReplaysReferenceContext proves the persisted context replays into
// the reopened transcript instead of being lost with the live snapshot.
func TestReopenReplaysReferenceContext(t *testing.T) {
	provider := &fakeProvider{respond: refRespond}
	h := newHarness(t, provider)
	sourceID := seedReferenceSession(t, h, "Source",
		store.Message{Role: "user", Content: "replay body"})
	targetID, err := h.ctrl.CreateSession(context.Background(), "Target")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(context.Background(), targetID,
		"cite "+sessionreference.FormatMention("Source", sourceID)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	h.restart()
	snapshot, err := h.ctrl.OpenSession(context.Background(), targetID)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	contexts := 0
	for _, message := range snapshot.Messages {
		if message.Role == state.RoleUser && strings.Contains(message.Text, sessionreference.SectionHeading) {
			contexts++
		}
	}
	if contexts != 1 {
		t.Fatalf("reopened transcript has %d reference contexts, want 1: %+v", contexts, snapshot.Messages)
	}
}

// TestSubmitMalformedReferenceReportsError covers the explicit failure path: a
// malformed canonical mention fails Submit with a presentable error and starts
// no turn, so the shell can show the reason instead of silently dropping it.
func TestSubmitMalformedReferenceReportsError(t *testing.T) {
	provider := &fakeProvider{respond: refRespond}
	h := newHarness(t, provider)
	targetID, err := h.ctrl.CreateSession(context.Background(), "Target")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err = h.ctrl.Submit(context.Background(), targetID, "bad @[x](dsh-session:bad!) mention")
	if err == nil {
		t.Fatal("Submit accepted a malformed reference")
	}
	if !strings.Contains(err.Error(), "invalid session reference URI") {
		t.Fatalf("Submit error = %v, want a presentable invalid-reference message", err)
	}
	if provider.requestCount() != 0 {
		t.Fatalf("provider calls = %d, want 0 for a rejected prompt", provider.requestCount())
	}
	stored, err := h.store.ListMessages(targetID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("rejected prompt was persisted: %+v", stored)
	}
}

// TestSubmitReferenceLimitAndSelfReference covers the normalistion errors.
func TestSubmitReferenceLimitAndSelfReference(t *testing.T) {
	provider := &fakeProvider{respond: refRespond}
	h := newHarness(t, provider)
	targetID, err := h.ctrl.CreateSession(context.Background(), "Target")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(context.Background(), targetID,
		"self "+sessionreference.FormatMention("Target", targetID)); err == nil {
		t.Fatal("Submit accepted a self reference")
	} else if !strings.Contains(err.Error(), "cannot reference itself") {
		t.Fatalf("self-reference error = %v", err)
	}

	tooMany := strings.Join([]string{
		sessionreference.FormatMention("a", "a1"),
		sessionreference.FormatMention("b", "b1"),
		sessionreference.FormatMention("c", "c1"),
		sessionreference.FormatMention("d", "d1"),
	}, " ")
	if err := h.ctrl.Submit(context.Background(), targetID, tooMany); err == nil {
		t.Fatal("Submit accepted four references")
	} else if !strings.Contains(err.Error(), "at most three sessions") {
		t.Fatalf("too-many error = %v", err)
	}
	if provider.requestCount() != 0 {
		t.Fatalf("provider calls = %d, want 0 for rejected prompts", provider.requestCount())
	}

	if err := h.ctrl.Submit(context.Background(), targetID,
		"unknown "+sessionreference.FormatMention("gone", "missing-session-id")); err == nil {
		t.Fatal("Submit accepted an unknown referenced session")
	} else if !strings.Contains(err.Error(), "cannot read referenced session") {
		t.Fatalf("unknown-session error = %v", err)
	}
}

// TestReferenceContextOmitsBinaryAndBounds covers the snapshot hygiene: NUL
// and invalid UTF-8 text is skipped and counted, oversized text is shortened,
// and the counters survive in the model-visible JSON.
func TestReferenceContextOmitsBinaryAndBounds(t *testing.T) {
	provider := &fakeProvider{respond: refRespond}
	h := newHarness(t, provider)
	sourceID := seedReferenceSession(t, h, "Source",
		store.Message{Role: "user", Content: strings.Repeat("长", 6000)},
		store.Message{Role: "assistant", Content: "nul \x00 here"},
		store.Message{Role: "user", Content: "keep this"},
	)
	targetID, err := h.ctrl.CreateSession(context.Background(), "Target")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(context.Background(), targetID,
		"cite "+sessionreference.FormatMention("Source", sourceID)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	request, _ := provider.requestAt(0)
	users := userMessagesOf(request)
	if len(users) != 2 {
		t.Fatalf("user messages = %d, want prompt + context", len(users))
	}
	contextText := users[1].Content
	if strings.Contains(contextText, "\x00") {
		t.Fatalf("NUL text travelled into the context")
	}
	sources := referencedPayload(t, contextText)
	if len(sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(sources))
	}
	if got := len(sources[0]); got > sessionreference.MaxReferenceBytes {
		t.Fatalf("source JSON = %d bytes, cap is %d", got, sessionreference.MaxReferenceBytes)
	}
	var source struct {
		OmittedMessages int `json:"omittedMessages"`
		OmittedBytes    int `json:"omittedBytes"`
	}
	if err := json.Unmarshal(sources[0], &source); err != nil {
		t.Fatalf("source JSON: %v", err)
	}
	if source.OmittedMessages == 0 || source.OmittedBytes == 0 {
		t.Fatalf("omitted counters = %d/%d, want the skipped and truncated text counted",
			source.OmittedMessages, source.OmittedBytes)
	}
	if !strings.Contains(contextText, "keep this") {
		t.Fatalf("retained text is missing the readable message")
	}
}
