package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// acceptanceChatMessage mirrors the subset of an OpenAI wire message this
// suite inspects. The app only sends text content, so a plain string is enough.
type acceptanceChatMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
}

type acceptanceChatRequest struct {
	Model    string                  `json:"model"`
	Messages []acceptanceChatMessage `json:"messages"`
}

// acceptanceRequestLog records the requests the production openai.Provider
// sends to the local SSE server.
type acceptanceRequestLog struct {
	mu   sync.Mutex
	reqs []acceptanceChatRequest
}

func (l *acceptanceRequestLog) add(req acceptanceChatRequest) {
	l.mu.Lock()
	l.reqs = append(l.reqs, req)
	l.mu.Unlock()
}

func (l *acceptanceRequestLog) snapshot() []acceptanceChatRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]acceptanceChatRequest(nil), l.reqs...)
}

func isSummarizerRequest(req acceptanceChatRequest) bool {
	return len(req.Messages) > 0 &&
		req.Messages[0].Role == string(runtime.RoleSystem) &&
		req.Messages[0].Content == runtime.LLMCompactorSystemPrompt
}

// writeAcceptanceSSE streams one assistant reply in OpenAI SSE format.
func writeAcceptanceSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, payload := range []map[string]any{
		{"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{"role": "assistant", "content": text},
		}}},
		{"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}}},
	} {
		raw, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// newAcceptanceProvider starts a local OpenAI-compatible SSE endpoint. Ordinary
// requests answer reply; summarizer requests answer summary.
func newAcceptanceProvider(t *testing.T, reply, summary string) (*httptest.Server, *acceptanceRequestLog) {
	t.Helper()
	log := &acceptanceRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req acceptanceChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode provider request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		log.add(req)
		text := reply
		if isSummarizerRequest(req) {
			text = summary
		}
		writeAcceptanceSSE(w, text)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

// newProductionAcceptanceController builds the real app wiring through
// buildAgent: storeHistory, LLMCompactor, ContextBudget and storeCheckpoint are
// the production fields, not a test-only runtime.New configuration. The local
// SSE endpoint supplies the provider without a real API key.
func newProductionAcceptanceController(t *testing.T, dir, baseURL string) (*session.Controller, func() error, *store.Store) {
	t.Helper()
	var wired *store.Store
	ctrl, cleanup, err := BuildServicesWith(dir, func(st *store.Store) (*AgentBundle, error) {
		wired = st
		configuredProvider(t, st)
		if err := st.SetSetting("provider.base_url", baseURL); err != nil {
			return nil, fmt.Errorf("set provider base url: %w", err)
		}
		bundle, err := buildAgent(st, dir, "production acceptance instruction")
		if err != nil {
			return nil, err
		}
		if bundle == nil || bundle.Agent == nil {
			return nil, errors.New("buildAgent returned no agent")
		}
		return bundle, nil
	})
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	return ctrl, cleanup, wired
}

// checkpointHistory decodes a production checkpoint into runtime messages.
func checkpointHistory(t *testing.T, cp *store.Checkpoint, err error) []runtime.Message {
	t.Helper()
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("store returned no checkpoint")
	}
	var history []runtime.Message
	if err := json.Unmarshal(cp.History, &history); err != nil {
		t.Fatalf("decode checkpoint history %s: %v", cp.History, err)
	}
	return history
}

func messageKeys(msgs []runtime.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, string(msg.Role)+":"+msg.Content)
	}
	return out
}

// TestCheckpointProductionWiring is the production-wiring acceptance test: the
// agent comes from buildAgent (with Compactor/ContextBudget/CheckpointStore),
// and a completed turn must write the checkpoint row; DeleteSession must remove
// it. Removing any of those production fields fails this test.
func TestCheckpointProductionWiring(t *testing.T) {
	dir := t.TempDir()
	srv, log := newAcceptanceProvider(t, "production-reply", "PRODUCTION SUMMARY")
	ctrl, cleanup, st := newProductionAcceptanceController(t, dir, srv.URL+"/v1")
	t.Cleanup(func() { _ = cleanup() })

	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "production checkpoint")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(ctx, id, "hello production"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitWiringIdle(t, ctrl)
	ctrl.Wait()

	reqs := log.snapshot()
	if len(reqs) == 0 {
		t.Fatal("production provider received no request")
	}
	if len(reqs[0].Messages) == 0 || reqs[0].Messages[0].Role != string(runtime.RoleSystem) ||
		strings.TrimSpace(reqs[0].Messages[0].Content) == "" {
		t.Fatalf("production request has no instruction system message: %+v", reqs[0].Messages)
	}

	cp, err := st.LoadCheckpoint(id)
	history := checkpointHistory(t, cp, err)
	want := []string{"user:hello production", "assistant:production-reply"}
	if got := messageKeys(history); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("production checkpoint history = %v, want %v", got, want)
	}
	if cp.Phase != runtime.CheckpointPhaseCompleted || cp.TurnID == "" {
		t.Fatalf("checkpoint phase/turn = %q/%q, want completed with a turn id", cp.Phase, cp.TurnID)
	}

	if err := ctrl.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := st.LoadCheckpoint(id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("LoadCheckpoint after DeleteSession error = %v, want ErrNotFound", err)
	}
}

// TestCheckpointProductionCompactionSummary drives the production compactor:
// an over-budget store-backed history must trigger the LLM summarizer, and the
// summary must reach that turn's provider request. This is the end-to-end
// regression for eino's leading-system strip and for compaction being actually
// wired in buildAgent.
func TestCheckpointProductionCompactionSummary(t *testing.T) {
	dir := t.TempDir()
	srv, log := newAcceptanceProvider(t, "production-reply", "PRODUCTION SUMMARY")
	ctrl, cleanup, st := newProductionAcceptanceController(t, dir, srv.URL+"/v1")
	t.Cleanup(func() { _ = cleanup() })

	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "production compaction")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 14 messages of 20k chars exceed the production ContextBudget (24000
	// tokens) by a wide margin while KeepLast=12 leaves two to summarize.
	const payload = 20_000
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 14; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		content := fmt.Sprintf("seed-%02d ", i) + strings.Repeat("x", payload)
		if _, err := st.AppendMessage(store.Message{
			SessionID: id,
			Role:      role,
			Content:   content,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
	}

	if err := ctrl.Submit(ctx, id, "final production question"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitWiringIdle(t, ctrl)
	ctrl.Wait()

	reqs := log.snapshot()
	summarizers := 0
	var turn *acceptanceChatRequest
	for i := range reqs {
		if isSummarizerRequest(reqs[i]) {
			summarizers++
			continue
		}
		for _, msg := range reqs[i].Messages {
			if msg.Role == string(runtime.RoleUser) && msg.Content == "final production question" {
				turn = &reqs[i]
			}
		}
	}
	if summarizers == 0 {
		t.Fatalf("production compaction never called the summarizer; requests: %d", len(reqs))
	}
	if turn == nil {
		t.Fatalf("provider received no turn request carrying the final question: %d requests", len(reqs))
	}

	var summary string
	retained := make(map[string]bool)
	for _, msg := range turn.Messages {
		if msg.Role == string(runtime.RoleSystem) &&
			strings.HasPrefix(msg.Content, "Earlier conversation compacted (") {
			summary = msg.Content
		}
		for i := 0; i < 14; i++ {
			key := fmt.Sprintf("seed-%02d ", i)
			if strings.HasPrefix(msg.Content, key) {
				retained[key] = true
			}
		}
	}
	if summary == "" || !strings.Contains(summary, "PRODUCTION SUMMARY") {
		t.Fatalf("turn request does not carry the production summary: %+v", turn.Messages)
	}
	for _, want := range []string{"seed-12 ", "seed-13 "} {
		if !retained[want] {
			t.Fatalf("turn request dropped the retained tail message %q: %v", want, retained)
		}
	}
	for _, gone := range []string{"seed-00 ", "seed-01 "} {
		if retained[gone] {
			t.Fatalf("turn request still carries compacted-away %q: %v", gone, retained)
		}
	}

	cp, err := st.LoadCheckpoint(id)
	history := checkpointHistory(t, cp, err)
	found := false
	for _, msg := range history {
		if msg.Role == runtime.RoleSystem && strings.Contains(msg.Content, "PRODUCTION SUMMARY") {
			found = true
		}
	}
	if !found {
		t.Fatalf("production checkpoint does not mirror the compacted conversation: %v", messageKeys(history))
	}
}

// TestCheckpointProductionMirrorOnly pins the production contract: when both
// History and CheckpointStore are configured (as buildAgent does), History is
// the source of truth and the checkpoint is a best-effort mirror. A stale
// checkpoint must never replace the store's message history.
func TestCheckpointProductionMirrorOnly(t *testing.T) {
	dir := t.TempDir()
	srv, log := newAcceptanceProvider(t, "production-reply", "PRODUCTION SUMMARY")
	ctrl, cleanup, st := newProductionAcceptanceController(t, dir, srv.URL+"/v1")
	t.Cleanup(func() { _ = cleanup() })

	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "production mirror")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.AppendMessage(store.Message{
		SessionID: id, Role: "user", Content: "from history", CreatedAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("append history user message: %v", err)
	}
	if _, err := st.AppendMessage(store.Message{
		SessionID: id, Role: "assistant", Content: "history answer", CreatedAt: time.Now().Add(-time.Minute + time.Second),
	}); err != nil {
		t.Fatalf("append history assistant message: %v", err)
	}
	stale, err := json.Marshal([]runtime.Message{{Role: runtime.RoleUser, Content: "from checkpoint"}})
	if err != nil {
		t.Fatalf("marshal stale checkpoint: %v", err)
	}
	if err := st.SaveCheckpoint(store.Checkpoint{
		SessionID: id,
		History:   stale,
		TurnID:    "stale-turn",
		UpdatedAt: time.Now(),
		Phase:     runtime.CheckpointPhaseCompleted,
	}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	if err := ctrl.Submit(ctx, id, "new"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitWiringIdle(t, ctrl)
	ctrl.Wait()

	reqs := log.snapshot()
	if len(reqs) == 0 {
		t.Fatal("production provider received no request")
	}
	keys := make([]string, 0, len(reqs[0].Messages))
	for _, msg := range reqs[0].Messages {
		keys = append(keys, msg.Role+":"+msg.Content)
	}
	joined := strings.Join(keys, "|")
	for _, want := range []string{"user:from history", "assistant:history answer", "user:new"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("production request %q does not contain %q", joined, want)
		}
	}
	if strings.Contains(joined, "from checkpoint") {
		t.Fatalf("production request used the stale checkpoint instead of History: %q", joined)
	}

	// The mirror is still refreshed after the completed turn.
	cp, err := st.LoadCheckpoint(id)
	history := checkpointHistory(t, cp, err)
	joined = strings.Join(messageKeys(history), "|")
	if !strings.Contains(joined, "from history") || strings.Contains(joined, "from checkpoint") {
		t.Fatalf("checkpoint mirror after the turn = %q, want the history-derived conversation", joined)
	}
}
