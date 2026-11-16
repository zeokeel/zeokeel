package uimodtest

// US-01/US-02 cross-wave integration (2026-11 audit, wave F): the live read
// model keeps per-iteration reasoning segments (wave A contracts + agent fold)
// and the transcript interleaves thinking/prose/tool cards by offset (wave C
// mergeTimelineEvents), so a live multi-iteration turn renders the SAME
// per-iteration structure as the restored turn — restart no longer changes the
// presentation. Driven through the REAL desktop assembly + a scripted
// OpenAI-compatible server running two rig iterations (iteration 1: reasoning →
// working_directory tool_call → tool_result; iteration 2: reasoning → text).
//
// Promoted from audit/probes/ext/us01_us02_test.go, inverted to assert the FIXED
// behavior (the audit's Fact probes asserted the old aggregated loss).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	us01Think1A = "THINK-ONE-A;"
	us01Think1B = "THINK-ONE-B;"
	us01Think2A = "THINK-TWO-A;"
	us01Think2B = "THINK-TWO-B;"
	us01Iter1   = us01Think1A + us01Think1B
	us01Iter2   = us01Think2A + us01Think2B
)

// us01Server scripts two model iterations and records the request bodies (the
// second must carry the tool result, proving two real rig iterations).
type us01Server struct {
	*httptest.Server
	mu     sync.Mutex
	calls  atomic.Int64
	bodies []string
}

func newUS01Server(t *testing.T) *us01Server {
	t.Helper()
	s := &us01Server{}
	jsonStr := func(value string) string {
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(body))
		s.mu.Unlock()
		n := int(s.calls.Add(1))
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		emit := func(line string) {
			fmt.Fprint(w, line)
			if flusher != nil {
				flusher.Flush()
			}
		}
		switch n {
		case 1:
			emit(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s},\"index\":0}]}\n\n", jsonStr(us01Think1A)))
			emit(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s},\"index\":0}]}\n\n", jsonStr(us01Think1B)))
			emit(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"working_directory\",\"arguments\":\"{}\"}}]},\"index\":0}]}\n\n"))
			emit("data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120}}\n\n")
		case 2:
			emit(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s},\"index\":0}]}\n\n", jsonStr(us01Think2A)))
			emit(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s},\"index\":0}]}\n\n", jsonStr(us01Think2B)))
			emit(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%s},\"index\":0}]}\n\n", jsonStr("FINAL-ANSWER")))
			emit("data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":150,\"completion_tokens\":30,\"total_tokens\":180}}\n\n")
		default:
			emit("data: {\"choices\":[{\"delta\":{\"content\":\"UNSCRIPTED\"},\"index\":0}]}\n\n")
			emit("data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1,\"total_tokens\":11}}\n\n")
		}
		emit("data: [DONE]\n\n")
	}))
	t.Cleanup(s.Server.Close)
	return s
}

func (s *us01Server) requestBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

func us01Settings(serverURL string) *memSettings {
	return &memSettings{values: map[string]string{
		contracts.SettingsChatModel:    "test-model",
		contracts.SettingsChatBaseURL:  serverURL,
		contracts.SettingWorkStepsMode: contracts.WorkStepsVerbose,
	}}
}

// us01Disclosures counts the accessible 思考 disclosure triggers (one node per
// control; Texts() would double-count the trigger's Label and inner Text).
func us01Disclosures(tester *ui.Tester) int {
	n := 0
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleDisclosure && info.Name == "思考" {
			n++
		}
	}
	return n
}

func us01FrameUntil(tester *ui.Tester, text string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tester.Frame()
		if tester.HasText(text) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestUS01LiveRestoredInterleaveIsomorphic pins the fixed presentation: a live
// two-iteration turn keeps two reasoning segments and renders two per-iteration
// thinking blocks interleaved with the tool card; a restart on the same dataDir
// renders the same two blocks (no presentation change).
func TestUS01LiveRestoredInterleaveIsomorphic(t *testing.T) {
	server := newUS01Server(t)
	dataDir := t.TempDir()
	settings := us01Settings(server.URL)

	root, uiRoot, loader := bootDesktop(t, dataDir, settings)
	chat := requireChat(t, root)
	id, err := chat.NewSession("US01 interleave")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	if err := chat.Send(id, "run the two-iteration turn"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, chat, id, 30*time.Second)

	// Two real rig iterations: the second request carries the tool result.
	if n := server.calls.Load(); n != 2 {
		t.Fatalf("expected 2 model calls, got %d", n)
	}
	bodies := server.requestBodies()
	if len(bodies) != 2 || !strings.Contains(bodies[1], `"role":"tool"`) {
		t.Fatalf("second request must carry the tool message; bodies=%d", len(bodies))
	}

	// The live read model keeps the per-iteration segments (wave A), not one
	// aggregated string: one assistant message, two segments, offset interleaved.
	session, ok := chat.Session(id)
	if !ok {
		t.Fatal("session missing")
	}
	var assistants []contracts.ChatMessage
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleAssistant {
			assistants = append(assistants, message)
		}
	}
	if len(assistants) != 1 {
		t.Fatalf("live run kept %d assistant messages, want 1", len(assistants))
	}
	m := assistants[0]
	if len(m.ReasoningSegments) != 2 {
		t.Fatalf("live ReasoningSegments = %d, want 2 (one per iteration): %+v", len(m.ReasoningSegments), m.ReasoningSegments)
	}
	if m.ReasoningSegments[0].Text != us01Iter1 || m.ReasoningSegments[0].Iteration != 1 {
		t.Fatalf("segment 0 = %+v, want iteration 1 %q", m.ReasoningSegments[0], us01Iter1)
	}
	if m.ReasoningSegments[1].Text != us01Iter2 || m.ReasoningSegments[1].Iteration != 2 {
		t.Fatalf("segment 1 = %+v, want iteration 2 %q", m.ReasoningSegments[1], us01Iter2)
	}
	if len(m.Tools) != 1 || m.Tools[0].Name != "working_directory" || !m.ToolTimelineKnown {
		t.Fatalf("tool timeline not recorded: %+v known=%v", m.Tools, m.ToolTimelineKnown)
	}
	if m.Text != "FINAL-ANSWER" {
		t.Fatalf("Text = %q, want FINAL-ANSWER", m.Text)
	}

	// The live verbose frame renders TWO per-iteration thinking blocks (wave C),
	// and the first holds only iteration 1's thinking.
	if !us01FrameUntil(tester, "思考", 5*time.Second) {
		t.Fatalf("no reasoning trigger in live verbose frame; texts=%v", tester.Texts())
	}
	if n := us01Disclosures(tester); n != 2 {
		t.Fatalf("live verbose shows %d reasoning blocks, want 2 (per iteration)", n)
	}
	if err := tester.Click("思考"); err != nil {
		t.Fatalf("open live reasoning: %v", err)
	}
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	liveTexts := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(liveTexts, us01Think1A) || strings.Contains(liveTexts, us01Think2A) {
		t.Fatalf("live first block must hold only iteration-1 thinking; texts=%v", tester.Texts())
	}

	// Restart on the same dataDir: the restored turn renders the SAME two blocks.
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	root2, uiRoot2, _ := bootDesktop(t, dataDir, settings)
	chat2 := requireChat(t, root2)
	tester2 := ui.NewTester(uiRoot2.View, 1200, 800)
	tester2.Frame()
	if err := uiRoot2.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session) restored: %v", err)
	}
	if !us01FrameUntil(tester2, "思考", 5*time.Second) {
		t.Fatalf("no reasoning trigger in restored verbose frame; texts=%v", tester2.Texts())
	}
	if n := us01Disclosures(tester2); n != 2 {
		t.Fatalf("restored verbose shows %d reasoning blocks, want 2 (isomorphic with live)", n)
	}
	if err := tester2.Click("思考"); err != nil {
		t.Fatalf("open restored reasoning: %v", err)
	}
	tester2.Advance(300 * time.Millisecond)
	tester2.Frame()
	restoredTexts := strings.Join(tester2.Texts(), "\n")
	if !strings.Contains(restoredTexts, us01Think1A) || strings.Contains(restoredTexts, us01Think2A) {
		t.Fatalf("restored first block must hold only iteration-1 thinking; texts=%v", tester2.Texts())
	}
	// The restored read model carries the same per-iteration thinking.
	restored, ok := chat2.Session(id)
	if !ok {
		t.Fatal("restored session missing")
	}
	var restoredThinking []string
	for _, message := range restored.Messages {
		if message.Role != contracts.ChatRoleAssistant {
			continue
		}
		if len(message.ReasoningSegments) > 0 {
			for _, segment := range message.ReasoningSegments {
				restoredThinking = append(restoredThinking, segment.Text)
			}
		} else if strings.TrimSpace(message.Reasoning) != "" {
			restoredThinking = append(restoredThinking, message.Reasoning)
		}
	}
	joined := strings.Join(restoredThinking, "")
	if !strings.Contains(joined, us01Iter1) || !strings.Contains(joined, us01Iter2) {
		t.Fatalf("restored read model lost per-iteration thinking: %q", joined)
	}
}
