package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// 「插话」 (run.interject): stop the running turn and start a new one with the
// interjection, in one hop. rig has no per-iteration injection seam — the agent loads
// history once per run — so steering is a restart, and the gateway does it atomically
// so the client cannot race its cancel against a 409.

type interjectChat struct {
	*fakeChat
	busy     bool
	sentText []string
	cancels  int
	// sendDelay is how long a "running" turn takes to settle after a cancel.
	settleAfter time.Duration
}

func (c *interjectChat) Send(sessionID, prompt string) error {
	if c.busy {
		return contracts.ErrSessionRunning
	}
	c.busy = true
	c.sentText = append(c.sentText, prompt)
	return nil
}

func (c *interjectChat) Cancel(string) error {
	c.cancels++
	if c.settleAfter > 0 {
		go func() {
			time.Sleep(c.settleAfter)
			c.busy = false
		}()
		return nil
	}
	c.busy = false
	return nil
}

func TestInterjectCancelsAndSendsInOneHop(t *testing.T) {
	chat := &interjectChat{fakeChat: newFakeChat()}
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	chat.busy = true // a turn is in flight
	reply, problem := gw.gateway.interjectRun(context.Background(),
		json.RawMessage(`{"sessionId":"s1","text":"先别查了，改用 grep"}`))
	if problem != nil {
		t.Fatalf("interject: %v", problem)
	}
	if chat.cancels != 1 {
		t.Fatalf("cancel calls = %d", chat.cancels)
	}
	if len(chat.sentText) != 1 || chat.sentText[0] != "先别查了，改用 grep" {
		t.Fatalf("sent = %v", chat.sentText)
	}
	if body, _ := json.Marshal(reply); string(body) != `{"ok":true}` {
		t.Fatalf("reply = %s", body)
	}
}

// A backend that needs a moment to settle still gets the message: the handler waits
// instead of reporting a conflict the user would have to retry.
func TestInterjectWaitsForTheTurnToSettle(t *testing.T) {
	chat := &interjectChat{fakeChat: newFakeChat()}
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	chat.busy = true
	chat.settleAfter = 200 * time.Millisecond
	if _, problem := gw.gateway.interjectRun(context.Background(),
		json.RawMessage(`{"sessionId":"s1","text":"换成 pnpm"}`)); problem != nil {
		t.Fatalf("interject: %v", problem)
	}
	if len(chat.sentText) != 1 || chat.sentText[0] != "换成 pnpm" {
		t.Fatalf("sent = %v", chat.sentText)
	}
}

func TestInterjectValidatesItsParams(t *testing.T) {
	chat := &interjectChat{fakeChat: newFakeChat()}
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	for _, body := range []string{`{}`, `{"sessionId":"s1"}`, `{"text":"x"}`} {
		if _, problem := gw.gateway.interjectRun(context.Background(), json.RawMessage(body)); problem == nil {
			t.Fatalf("interject %q must be refused", body)
		}
	}
}
