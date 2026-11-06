package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// isAutoTitleRequest recognizes the title request by its stable system
// instruction, the same way the session package's tests do.
func isAutoTitleRequest(req acceptanceChatRequest) bool {
	return len(req.Messages) > 0 &&
		req.Messages[0].Role == string(runtime.RoleSystem) &&
		req.Messages[0].Content == session.AutoTitleSystemPrompt
}

// newAutoTitleAcceptanceProvider starts a local OpenAI-compatible SSE endpoint
// that answers ordinary turns with reply and auto-title requests with title.
func newAutoTitleAcceptanceProvider(t *testing.T, reply, title string) (*httptest.Server, *acceptanceRequestLog) {
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
		if isAutoTitleRequest(req) {
			text = title
		}
		writeAcceptanceSSE(w, text)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

// TestAutoTitleProductionWiring is the app acceptance test: buildAgent must
// install the provider-backed title generator on the Controller, so a real
// first turn through BuildServicesWith produces a provider-written title
// (instead of the truncated-message fallback).
func TestAutoTitleProductionWiring(t *testing.T) {
	dir := t.TempDir()
	const wantTitle = "Deploy Pipeline Fix"
	srv, log := newAutoTitleAcceptanceProvider(t, "production-reply", wantTitle)
	ctrl, cleanup, st := newProductionAcceptanceController(t, dir, srv.URL+"/v1")
	t.Cleanup(func() { _ = cleanup() })

	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "New Session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	const prompt = "the deploy pipeline keeps failing after the cache change"
	if err := ctrl.Submit(ctx, id, prompt); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitWiringIdle(t, ctrl)

	deadline := time.Now().Add(10 * time.Second)
	var stored *store.Session
	for time.Now().Before(deadline) {
		stored, err = st.GetSession(id)
		if err == nil && stored.Title == wantTitle {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if stored == nil || stored.Title != wantTitle {
		got := ""
		if stored != nil {
			got = stored.Title
		}
		t.Fatalf("production auto title = %q, want %q", got, wantTitle)
	}

	var titleRequest *acceptanceChatRequest
	for _, req := range log.snapshot() {
		if isAutoTitleRequest(req) {
			req := req
			titleRequest = &req
		}
	}
	if titleRequest == nil {
		t.Fatalf("production provider received no auto-title request; requests: %d", len(log.snapshot()))
	}
	parts := make([]string, 0, len(titleRequest.Messages))
	for _, msg := range titleRequest.Messages {
		parts = append(parts, msg.Role+":"+msg.Content)
	}
	joined := strings.Join(parts, "\n")
	if !strings.Contains(joined, prompt) {
		t.Fatalf("auto-title request does not carry the first user message: %q", joined)
	}
	if !strings.Contains(joined, "production-reply") {
		t.Fatalf("auto-title request does not carry the assistant reply: %q", joined)
	}
}

// countAutoTitleRequests reports how many automatic-title requests the test
// provider received.
func countAutoTitleRequests(log *acceptanceRequestLog) int {
	n := 0
	for _, req := range log.snapshot() {
		if isAutoTitleRequest(req) {
			n++
		}
	}
	return n
}

// TestAutoTitleAutomationRunKeepsTaskTitle is the regression for the
// production automation path: startAutomation names each run with its task
// title, and that title is the only stored link from a run back to its task.
// The auto-titler must treat it as deliberate and never replace it.
func TestAutoTitleAutomationRunKeepsTaskTitle(t *testing.T) {
	dir := t.TempDir()
	const (
		taskTitle = "Nightly Release Report"
		prompt    = "summarize yesterday's releases"
	)
	srv, log := newAutoTitleAcceptanceProvider(t, "automation-reply", "Model Named Automation Session")
	ctrl, cleanup, st := newProductionAcceptanceController(t, dir, srv.URL+"/v1")
	t.Cleanup(func() { _ = cleanup() })

	// Wire the real scheduler and Run-now hook, exactly as startServices does.
	application := &app{}
	application.startAutomation(ctrl)
	t.Cleanup(func() {
		if application.scheduler != nil {
			application.scheduler.Stop()
			application.scheduler = nil
		}
	})

	task, err := ctrl.Automations().Create(store.Automation{
		Title:  taskTitle,
		Prompt: prompt,
		Cron:   "@daily",
		// Disabled so only the explicit Run now below fires it.
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	if !ctrl.RunAutomationNow(task.ID) {
		t.Fatal("RunAutomationNow reported no wired automation runner")
	}
	waitWiringIdle(t, ctrl)
	// Let an asynchronous title write land, if one happens.
	time.Sleep(300 * time.Millisecond)

	id := ctrl.CurrentSessionID()
	if id == "" {
		t.Fatal("automation run created no current session")
	}
	sess, err := st.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Title != taskTitle {
		t.Fatalf("automation session title = %q, want the task title %q", sess.Title, taskTitle)
	}
	if got := countAutoTitleRequests(log); got != 0 {
		t.Fatalf("auto-title requests for an automation run = %d, want 0", got)
	}
}
