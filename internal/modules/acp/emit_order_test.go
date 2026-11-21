package acp

// BE-06 (2026-11 audit): promptState.process computed the text delta under its
// mutex but emitted AFTER unlocking, so two concurrent process calls (the run
// goroutine's chat subscription and the prompt goroutine's direct post-Send
// call) could write their frames in the wrong order — the client saw "BBBAAA"
// instead of "AAABBB". Emission now happens inside the same critical section
// that assigned the delta. The emit callbacks only take the frameWriter leaf
// lock, so holding state.mu across them cannot deadlock.
//
// The scheduling-point variant widens the unlock→emit window with a Gosched
// (the production equivalents are json.Marshal, writer-lock contention and
// preemption); the real-writer variant uses no artificial scheduling at all.

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"

	rigacp "github.com/zeokeel/zeokeel-rig/acp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func emitOrderSnapshot(text string) contracts.ChatSession {
	return contracts.ChatSession{
		Running:  true,
		Messages: []contracts.ChatMessage{{Role: contracts.ChatRoleAssistant, Text: text}},
	}
}

func emitOrderNewState() *promptState {
	return &promptState{
		signal:     make(chan struct{}),
		toolStates: map[string]string{},
		toolIDs:    map[string]string{},
	}
}

// emittedText concatenates the agent_message_chunk texts in write order.
func emittedText(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var frame struct {
			Method string `json:"method"`
			Params struct {
				Update struct {
					SessionUpdate string `json:"sessionUpdate"`
					Content       struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"update"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("bad frame %q: %v", line, err)
		}
		if frame.Method == MethodSessionUpdate && frame.Params.Update.SessionUpdate == UpdateAgentMessageChunk {
			out.WriteString(frame.Params.Update.Content.Text)
		}
	}
	return out.String()
}

func emitOrderAttempt(t *testing.T, schedPoint bool) string {
	t.Helper()
	var buf bytes.Buffer
	writer := rigacp.NewWriter(&buf)
	state := emitOrderNewState()
	emit := func(delta string) {
		if schedPoint {
			runtime.Gosched()
		}
		writer.Notify(MethodSessionUpdate, map[string]any{
			"sessionId": "s",
			"update": map[string]any{
				"sessionUpdate": UpdateAgentMessageChunk,
				"content":       map[string]any{"type": "text", "text": delta},
			},
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); state.process(emitOrderSnapshot("AAA"), emit, nil) }()
	go func() { defer wg.Done(); state.process(emitOrderSnapshot("AAABBB"), emit, nil) }()
	wg.Wait()
	return emittedText(t, &buf)
}

func TestPromptEmitOrderConcurrentSchedPoint(t *testing.T) {
	bad := 0
	for i := 0; i < 200; i++ {
		if got := emitOrderAttempt(t, true); got != "AAABBB" {
			bad++
			if bad <= 3 {
				t.Logf("attempt %d: emitted %q (want AAABBB)", i, got)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("BE-06: %d/200 concurrent attempts emitted out of order or lost text (unlock-before-emit race)", bad)
	}
}

func TestPromptEmitOrderConcurrentRealWriter(t *testing.T) {
	bad := 0
	for i := 0; i < 500; i++ {
		if got := emitOrderAttempt(t, false); got != "AAABBB" {
			bad++
			if bad <= 3 {
				t.Logf("attempt %d: emitted %q (want AAABBB)", i, got)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("BE-06: %d/500 real-writer attempts emitted out of order or lost text", bad)
	}
}

// Control: sequential process calls must always emit in order (the method
// works when there is no concurrency; the defect is the race alone).
func TestPromptEmitOrderSequentialControl(t *testing.T) {
	for i := 0; i < 50; i++ {
		var buf bytes.Buffer
		writer := rigacp.NewWriter(&buf)
		state := emitOrderNewState()
		emit := func(delta string) {
			writer.Notify(MethodSessionUpdate, map[string]any{
				"sessionId": "s",
				"update": map[string]any{
					"sessionUpdate": UpdateAgentMessageChunk,
					"content":       map[string]any{"type": "text", "text": delta},
				},
			})
		}
		state.process(emitOrderSnapshot("AAA"), emit, nil)
		state.process(emitOrderSnapshot("AAABBB"), emit, nil)
		if got := emittedText(t, &buf); got != "AAABBB" {
			t.Fatalf("sequential attempt %d emitted %q, want AAABBB", i, got)
		}
	}
}
