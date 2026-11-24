package gateway

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A subagent delegation or a slow tool can run for minutes without producing an
// event. The browser client treats a stream with no bytes as a dead one (its
// streamTimeout is 60s), and a live audit caught the consequence: a subagent run
// died with `StreamTimeoutError` while the gateway was still working. The stream
// now writes an SSE comment heartbeat when it has been silent, which resets the
// client's timer without inventing an event.
func TestChatRunHeartbeatsWhileTheTurnIsSilent(t *testing.T) {
	previous := runHeartbeatInterval
	runHeartbeatInterval = 40 * time.Millisecond
	defer func() { runHeartbeatInterval = previous }()

	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	done := make(chan struct{})
	script := fullRunScript(done)
	chat.script = func(f *fakeChat) {
		// Silence for several heartbeat windows — the shape of a delegation.
		time.Sleep(240 * time.Millisecond)
		script(f)
	}
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	response := openRun(t, gw, runRequest{SessionID: "s1", Text: "slow turn", ClientRunID: "run-hb"})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	body := string(raw)

	if !strings.Contains(body, ": ping") {
		t.Fatalf("the silent stream sent no heartbeat: %q", body)
	}
	// A comment is not an event: the run's own frames must still arrive, and the
	// stream must still end on its terminal frame.
	<-done
	if !strings.Contains(body, "event: done") {
		t.Fatalf("the run did not complete after the heartbeat: %q", body)
	}
	if strings.Contains(body, "event: ping") {
		t.Fatalf("the heartbeat was written as an event: %q", body)
	}
}
