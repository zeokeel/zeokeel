package gateway

// Migrated from internal/regression/agent_harness_test.go + usage_test.go
// (backend-only files, 377 + 330 lines) before S4 deletes that package.
//
// Behaviour kept: the agent run lifecycle the browser observes — start →
// streaming deltas → done, and an explicit stop (run.cancel) that ends the run
// without corrupting the session.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// runFrames collects the SSE frames of one POST /api/chat/run.
type runFrames struct {
	frames []sseFrame
	done   chan struct{}
}

// startRun posts a run and collects its SSE frames in the background.
func startRun(t *testing.T, rig *e2eRig, sessionID, text, clientRunID string) *runFrames {
	t.Helper()
	response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: text, ClientRunID: clientRunID})
	if response.StatusCode != 200 {
		t.Fatalf("POST /api/chat/run = %d, want 200", response.StatusCode)
	}
	collector := &runFrames{done: make(chan struct{})}
	go func() {
		defer close(collector.done)
		defer func() { _ = response.Body.Close() }()
		collector.frames = readFrames(t, response.Body, true)
	}()
	<-collector.done
	return collector
}

// framesWith returns every frame of one event kind.
func framesWith(frames []sseFrame, name string) []sseFrame {
	out := make([]sseFrame, 0, len(frames))
	for _, frame := range frames {
		if frame.event == name {
			out = append(out, frame)
		}
	}
	return out
}

// TestE2EAgentRunLifecycleOverSSE is the migrated run-lifecycle regression: a
// real agent module, a scripted model, and the browser's own SSE endpoint.
func TestE2EAgentRunLifecycleOverSSE(t *testing.T) {
	rig := newE2ERig(t, func([]byte, int) (int, string) {
		return 0, sseAnswer("Hello from the agent")
	}, e2eOptions{})
	rig.connect()
	sessionID := rig.newSession("lifecycle")

	run := startRun(t, rig, sessionID, "say hello", "run-lifecycle")
	names := eventNames(run.frames)
	if last := names[len(names)-1]; last != sseDone {
		t.Fatalf("last event = %q, want done (frames: %v)", last, names)
	}
	var text strings.Builder
	for _, frame := range framesWith(run.frames, sseMessageDelta) {
		value, _ := frame.data["text"].(string)
		text.WriteString(value)
	}
	if !strings.Contains(text.String(), "Hello from the agent") {
		t.Fatalf("streamed text = %q", text.String())
	}
	if usageFrames := framesWith(run.frames, sseUsage); len(usageFrames) > 0 {
		if usageFrames[0].data["turnId"] != "run-lifecycle" {
			t.Fatalf("usage turnId = %v", usageFrames[0].data["turnId"])
		}
	}

	// The authoritative read model agrees: the assistant message is stored and
	// the run is no longer running.
	session, ok := rig.chat.Session(sessionID)
	if !ok {
		t.Fatal("the session disappeared")
	}
	if session.Running {
		t.Fatal("the session is still running after done")
	}
	var assistant *contracts.ChatMessage
	for i := range session.Messages {
		if session.Messages[i].Role == contracts.ChatRoleAssistant {
			assistant = &session.Messages[i]
		}
	}
	if assistant == nil || !strings.Contains(assistant.Text, "Hello from the agent") {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if rig.model.callCount() != 1 {
		t.Fatalf("provider was called %d times, want 1", rig.model.callCount())
	}
}

// TestE2EAgentRunCancelStopsTheRun is the stop path: run.cancel ends the run and
// the session settles (the desktop "stop" semantics, §4.4).
func TestE2EAgentRunCancelStopsTheRun(t *testing.T) {
	// The model stalls so the run is genuinely in flight when cancel arrives.
	release := make(chan struct{})
	rig := newE2ERig(t, func([]byte, int) (int, string) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		return 0, sseAnswer("too late")
	}, e2eOptions{})
	conn := rig.connect()
	sessionID := rig.newSession("cancel")

	posted := make(chan *http.Response, 1)
	go func() {
		posted <- openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "stall", ClientRunID: "run-cancel"})
	}()
	var response = <-posted
	defer func() { _ = response.Body.Close() }()

	// Wait until the backend reports the run, then stop it by protocol.
	waitFor(t, "the run to start", func() bool {
		session, ok := rig.chat.Session(sessionID)
		return ok && session.Running
	})
	resultOf(t, conn.call("run.cancel", map[string]any{"sessionId": sessionID}))
	close(release)

	waitFor(t, "the run to settle after run.cancel", func() bool {
		session, ok := rig.chat.Session(sessionID)
		return ok && !session.Running
	})
}

// TestE2EAgentRunSurvivesClientDisconnect pins §4.6: dropping the SSE socket is
// not a stop request; the run finishes and the session carries the answer.
func TestE2EAgentRunSurvivesClientDisconnect(t *testing.T) {
	release := make(chan struct{})
	rig := newE2ERig(t, func([]byte, int) (int, string) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		return 0, sseAnswer("finished anyway")
	}, e2eOptions{})
	rig.connect()
	sessionID := rig.newSession("disconnect")

	response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "long", ClientRunID: "run-drop"})
	if response.StatusCode != 200 {
		t.Fatalf("status = %d", response.StatusCode)
	}
	waitFor(t, "the run to start", func() bool {
		session, ok := rig.chat.Session(sessionID)
		return ok && session.Running
	})
	_ = response.Body.Close()
	close(release)

	waitFor(t, "the disconnected run to finish", func() bool {
		session, ok := rig.chat.Session(sessionID)
		if !ok || session.Running {
			return false
		}
		for _, message := range session.Messages {
			if message.Role == contracts.ChatRoleAssistant && strings.Contains(message.Text, "finished anyway") {
				return true
			}
		}
		return false
	})
}

// TestE2EAgentRunUsageReachesTheStream keeps the usage assertions of the deleted
// usage_test.go: the accounting of a turn must arrive on the SSE stream.
func TestE2EAgentRunUsageReachesTheStream(t *testing.T) {
	rig := newE2ERig(t, func([]byte, int) (int, string) {
		return 0, sseAnswerWithUsage("answered", 9, 4, 13)
	}, e2eOptions{})
	rig.connect()
	sessionID := rig.newSession("usage")

	run := startRun(t, rig, sessionID, "count tokens", "run-usage")
	usageFrames := framesWith(run.frames, sseUsage)
	if len(usageFrames) == 0 {
		t.Fatalf("no usage frame; frames: %v", eventNames(run.frames))
	}
	if usageFrames[0].data["inputTokens"] != float64(9) || usageFrames[0].data["outputTokens"] != float64(4) {
		t.Fatalf("usage = %v", usageFrames[0].data)
	}
	_ = context.Background()
}

// sseAnswerWithUsage renders one streaming completion with usage accounting.
func sseAnswerWithUsage(text string, prompt, completion, total int) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return "data: {\"choices\":[{\"delta\":{\"content\":" + string(encoded) + "},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":" +
		fmt.Sprint(prompt) + ",\"completion_tokens\":" + fmt.Sprint(completion) + ",\"total_tokens\":" + fmt.Sprint(total) + "}}\n\n" +
		"data: [DONE]\n\n"
}
