package gateway

import (
	"context"
	"encoding/json"
	"sync"
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
	// The fixture is read from the handler goroutine and written from Cancel's
	// goroutine, so the state needs a lock: the race detector caught it on CI and
	// the test, not the product, was wrong.
	mu          sync.Mutex
	busy        bool
	sentText    []string
	cancels     int
	settleAfter time.Duration
}

func (c *interjectChat) Send(sessionID, prompt string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busy {
		return contracts.ErrSessionRunning
	}
	c.busy = true
	c.sentText = append(c.sentText, prompt)
	return nil
}

func (c *interjectChat) Cancel(string) error {
	c.mu.Lock()
	c.cancels++
	settleAfter := c.settleAfter
	if settleAfter <= 0 {
		c.busy = false
	}
	c.mu.Unlock()
	if settleAfter > 0 {
		go func() {
			time.Sleep(settleAfter)
			c.mu.Lock()
			c.busy = false
			c.mu.Unlock()
		}()
	}
	return nil
}

// setRunning marks the fixture as mid-turn before the test starts.
func (c *interjectChat) setRunning(busy bool) {
	c.mu.Lock()
	c.busy = busy
	c.mu.Unlock()
}

// sent returns a copy of what the fixture accepted.
func (c *interjectChat) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sentText...)
}

// cancelCount reports how many cancels arrived.
func (c *interjectChat) cancelCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancels
}

func TestInterjectCancelsAndSendsInOneHop(t *testing.T) {
	chat := &interjectChat{fakeChat: newFakeChat()}
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	chat.setRunning(true) // a turn is in flight
	reply, problem := gw.gateway.interjectRun(context.Background(),
		json.RawMessage(`{"sessionId":"s1","text":"先别查了，改用 grep"}`))
	if problem != nil {
		t.Fatalf("interject: %v", problem)
	}
	if chat.cancelCount() != 1 {
		t.Fatalf("cancel calls = %d", chat.cancelCount())
	}
	if sent := chat.sent(); len(sent) != 1 || sent[0] != "先别查了，改用 grep" {
		t.Fatalf("sent = %v", sent)
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

	chat.setRunning(true)
	chat.settleAfter = 200 * time.Millisecond
	if _, problem := gw.gateway.interjectRun(context.Background(),
		json.RawMessage(`{"sessionId":"s1","text":"换成 pnpm"}`)); problem != nil {
		t.Fatalf("interject: %v", problem)
	}
	if sent := chat.sent(); len(sent) != 1 || sent[0] != "换成 pnpm" {
		t.Fatalf("sent = %v", sent)
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
