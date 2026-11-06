package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// writeUserPresetJSON writes one user preset file into <dataDir>/presets.
func writeUserPresetJSON(t *testing.T, dataDir string) {
	t.Helper()
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
		"id": "reviewer",
		"name": "Reviewer",
		"tools": ["read_file", "grep"],
		"persona": "You are a careful code reviewer.",
		"prompt": "Cite files by path and line.",
		"model": "review-model"
	}`
	if err := os.WriteFile(filepath.Join(dir, "reviewer.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newPresetTestApp builds an app whose controller/store live in dataDir and
// whose provider points at baseURL. The app installs the per-session agent
// factory like production, so buildSessionAgent and turns use the real wiring.
func newPresetTestApp(t *testing.T, dataDir, baseURL string) *app {
	t.Helper()
	writeHeadlessStore(t, dataDir, baseURL, map[string]string{"provider.retry_max_retries": "0"})
	a := &app{}
	ctrl, cleanup, err := BuildServicesWith(dataDir, func(st *store.Store) (*AgentBundle, error) {
		root := workspaceRootForStore(st, dataDir)
		return buildAgentWithOptions(st, root, DefaultInstruction, a.toolOptions(st, root, ""))
	})
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	t.Cleanup(func() {
		if a.services != nil {
			_ = a.services.Close()
		}
		if cleanup != nil {
			_ = cleanup()
		}
	})
	a.services = ctrl
	a.installSessionAgentFactory(ctrl)
	a.sessionFactory = true
	ctrl.SetAgentRebuilder(a.rebuildAgent)
	if err := a.rebuildAgent("startup"); err != nil {
		t.Fatalf("rebuildAgent(startup): %v", err)
	}
	return a
}

// TestBuildAgentAppliesSessionPreset covers the Controller/app preset wiring: a
// custom preset filters the model-visible tools, injects its persona/prompt and
// supplies the session's default model.
func TestBuildAgentAppliesSessionPreset(t *testing.T) {
	dataDir := t.TempDir()
	writeUserPresetJSON(t, dataDir)
	a := newPresetTestApp(t, dataDir, "http://127.0.0.1:1/v1")
	ctx := context.Background()
	sessionID, err := a.services.CreateSession(ctx, "preset build")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := a.services.SetSessionPreset(sessionID, "reviewer"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	agent, err := a.buildSessionAgent(sessionID)
	if err != nil || agent == nil {
		t.Fatalf("buildSessionAgent = %+v, %v", agent, err)
	}
	tools := map[string]bool{}
	for _, name := range agent.ToolNames() {
		tools[name] = true
	}
	if !tools["read_file"] || !tools["grep"] {
		t.Fatalf("preset tools = %v, want the read_file/grep allowlist", agent.ToolNames())
	}
	if tools["bash"] || tools["write_file"] {
		t.Fatalf("preset tools = %v, want bash/write_file filtered out by the allowlist", agent.ToolNames())
	}
	instruction := agent.Instruction()
	if !strings.Contains(instruction, "You are a careful code reviewer.") {
		t.Fatalf("preset persona missing from the instruction:\n%s", instruction)
	}
	if !strings.Contains(instruction, "Cite files by path and line.") {
		t.Fatalf("preset prompt missing from the instruction:\n%s", instruction)
	}
	if got := a.services.SessionPresetModel(sessionID); got != "review-model" {
		t.Fatalf("SessionPresetModel = %q, want review-model", got)
	}
}

// TestBuildAgentUnknownPresetFallsBackToStandard covers the fail-open contract:
// a deleted user preset resolves to standard with a warning, never an error.
func TestBuildAgentUnknownPresetFallsBackToStandard(t *testing.T) {
	dataDir := t.TempDir()
	writeUserPresetJSON(t, dataDir)
	a := newPresetTestApp(t, dataDir, "http://127.0.0.1:1/v1")
	ctx := context.Background()
	sessionID, err := a.services.CreateSession(ctx, "fallback build")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Simulate a deleted user preset: the persisted selection is unknown.
	if err := a.services.Store().SetSetting("session.preset."+sessionID, "deleted-preset"); err != nil {
		t.Fatal(err)
	}
	agent, err := a.buildSessionAgent(sessionID)
	if err != nil || agent == nil {
		t.Fatalf("buildSessionAgent = %+v, %v; want standard fallback", agent, err)
	}
	tools := map[string]bool{}
	for _, name := range agent.ToolNames() {
		tools[name] = true
	}
	if !tools["bash"] || !tools["read_file"] {
		t.Fatalf("fallback tools = %v, want the standard (all-tools) set", agent.ToolNames())
	}
}

// TestQuestionGateAnswersWithinTurn is the FP-16 acceptance: the ask_user call
// publishes a card, the user answers, and the model continues in the SAME turn
// with the answer as the tool result.
func TestQuestionGateAnswersWithinTurn(t *testing.T) {
	dataDir := t.TempDir()
	var calls atomic.Int32
	var secondBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if calls.Add(1) > 1 {
			secondBody.Store(string(body))
			writeHeadlessSSE(w, "answered, continuing")
			return
		}
		call := map[string]any{
			"index": 0, "id": "call-ask", "type": "function",
			"function": map[string]any{
				"name":      "ask_user",
				"arguments": `{"question":"Which database should I target?","options":["sqlite","postgres"]}`,
			},
		}
		payload := `{"choices":[{"index":0,"delta":{"tool_calls":[` + mustMarshalJSON(t, call) + `]},"finish_reason":"tool_calls"}]}`
		_, _ = io.WriteString(w, "data: "+payload+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	a := newPresetTestApp(t, dataDir, srv.URL+"/v1")
	ctx := context.Background()
	sessionID, err := a.services.CreateSession(ctx, "question turn")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := a.services.Submit(ctx, sessionID, "ask me before proceeding"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// The turn must publish the question card and block (it does not end).
	var pending []sessionPendingQuestion
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, question := range a.services.Questions() {
			pending = append(pending, sessionPendingQuestion{CallID: question.CallID, Text: question.Question})
		}
		if len(pending) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(pending) == 0 {
		t.Fatalf("no pending question was published; phase=%v", a.services.Snapshot().Phase)
	}
	if pending[0].CallID != "call-ask" || !strings.Contains(pending[0].Text, "Which database") {
		t.Fatalf("pending question = %+v, want call-ask/Which database", pending[0])
	}
	if err := a.services.AnswerQuestion(ctx, "call-ask", "postgres"); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	// The answer arrives as the same tool call's result and the model continues.
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if a.services.Snapshot().Phase == state.PhaseIdle {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap := a.services.Snapshot()
	if snap.Phase != state.PhaseIdle {
		t.Fatalf("turn did not settle after the answer: phase=%v", snap.Phase)
	}
	body, _ := secondBody.Load().(string)
	if !strings.Contains(body, "postgres") || !strings.Contains(body, "answered") {
		t.Fatalf("second model call did not receive the answered tool result: %s", body)
	}
}

type sessionPendingQuestion struct {
	CallID string
	Text   string
}

// TestQuestionGateTimeoutDegrades covers the no-UI degrade: an unanswered
// question maps the controller deadline to runtime.ErrQuestionTimeout so the
// turn falls back to the pending result.
func TestQuestionGateTimeoutDegrades(t *testing.T) {
	a, _ := newSettingsTestApp(t)
	sessionID, err := a.services.CreateSession(context.Background(), "gate timeout")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	gate := appQuestionGate{app: a, sessionID: sessionID, timeout: 40 * time.Millisecond}
	_, err = gate.Ask(context.Background(), "call-timeout", "Still there?", nil)
	if !errors.Is(err, runtime.ErrQuestionTimeout) {
		t.Fatalf("gate.Ask timeout = %v, want runtime.ErrQuestionTimeout", err)
	}
	if len(a.services.Questions()) != 0 {
		t.Fatalf("question card survived the timeout: %+v", a.services.Questions())
	}
}

// TestQuestionGateCancellationPropagates covers a stopped turn: the context
// cancellation reaches the gate unchanged (the runtime treats it as a cancelled
// tool call, not a timeout).
func TestQuestionGateCancellationPropagates(t *testing.T) {
	a, _ := newSettingsTestApp(t)
	sessionID, err := a.services.CreateSession(context.Background(), "gate cancel")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		gate := appQuestionGate{app: a, sessionID: sessionID, timeout: time.Minute}
		_, gerr := gate.Ask(ctx, "call-cancel", "Continue?", nil)
		errc <- gerr
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(a.services.Questions()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if len(a.services.Questions()) == 0 {
		t.Fatal("question card was not published before cancellation")
	}
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled gate = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gate did not unblock after cancellation")
	}
}

// TestPresetReloadAppliesToNextTurn covers the "Reload presets then use on the
// next turn" contract: the app rebuild hook invalidates per-session agents, so a
// rebuilt agent sees the edited preset allowlist.
func TestPresetReloadAppliesToNextTurn(t *testing.T) {
	dataDir := t.TempDir()
	writeUserPresetJSON(t, dataDir)
	a := newPresetTestApp(t, dataDir, "http://127.0.0.1:1/v1")
	ctx := context.Background()
	sessionID, err := a.services.CreateSession(ctx, "reload preset")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := a.services.SetSessionPreset(sessionID, "reviewer"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	first, err := a.buildSessionAgent(sessionID)
	if err != nil || first == nil {
		t.Fatalf("initial buildSessionAgent = %+v, %v", first, err)
	}
	names := map[string]bool{}
	for _, name := range first.ToolNames() {
		names[name] = true
	}
	if !names["grep"] {
		t.Fatalf("initial preset tools = %v, want grep from the allowlist", first.ToolNames())
	}

	// Edit the user preset: drop grep from the allowlist and reload.
	body := `{"id":"reviewer","name":"Reviewer","tools":["read_file"],"persona":"You are a careful code reviewer."}`
	if err := os.WriteFile(filepath.Join(dataDir, "presets", "reviewer.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.services.ReloadPresets(); err != nil {
		t.Fatalf("ReloadPresets: %v", err)
	}
	second, err := a.buildSessionAgent(sessionID)
	if err != nil || second == nil {
		t.Fatalf("rebuilt buildSessionAgent = %+v, %v", second, err)
	}
	names = map[string]bool{}
	for _, name := range second.ToolNames() {
		names[name] = true
	}
	if !names["read_file"] || names["grep"] {
		t.Fatalf("reloaded preset tools = %v, want read_file only (grep removed)", second.ToolNames())
	}
}
