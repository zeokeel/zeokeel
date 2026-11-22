package acp

// BE-06 (2026-11 audit): assistant text deltas used to be computed under a
// mutex but emitted after unlocking, so two concurrent snapshot callbacks (the
// run goroutine's chat subscription and the prompt goroutine's direct post-Send
// snapshot) could write their frames in the wrong order — the client saw
// "BBBAAA" instead of "AAABBB".
//
// The streaming state now lives in the rig acp/server package, which emits
// inside the critical section that assigned the delta. This wire-level
// regression drives two racing snapshots through the product server and asserts
// the client always sees the stable concatenation.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestPromptEmitOrderConcurrentNotifications(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		chat := newFakeChat()
		chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				c.setAssistantText(sessionID, "AAA", true)
			}()
			go func() {
				defer wg.Done()
				c.setAssistantText(sessionID, "AAABBB", true)
			}()
			wg.Wait()
			c.setAssistantText(sessionID, "AAABBB", false)
		}
		service := newService(Config{}, chat, &fakeLogger{})
		var output bytes.Buffer
		if err := service.Serve(context.Background(), strings.NewReader(promptLine(1, "sess-1", "go")+"\n"), &output); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		if got := emittedText(t, output.Bytes()); got != "AAABBB" {
			t.Fatalf("attempt %d: chunks = %q, want AAABBB", attempt, got)
		}
		if !strings.Contains(output.String(), StopReasonEndTurn) {
			t.Fatalf("attempt %d: missing end_turn response: %s", attempt, output.String())
		}
	}
}

// emittedText concatenates the agent_message_chunk texts in write order.
func emittedText(t *testing.T, data []byte) string {
	t.Helper()
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
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
