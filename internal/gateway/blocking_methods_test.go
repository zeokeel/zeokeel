package gateway

// Blocking control-plane methods (AUDIT-2026-11-24 B-12).
//
// team.wait / team.send / run.interject can wait on the backend. They used to run
// on the connection's reader goroutine, which froze every other frame on that
// socket (no ping, no run.cancel, no next request) and made `$/cancel` a no-op
// (`dispatch` answered it with "every method here returns immediately").
// These tests drive a real WS client against those two surfaces.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// busyChat always refuses a prompt with ErrSessionRunning, so run.interject keeps
// retrying until its 3 s deadline — the blocking shape we need.
type busyChat struct {
	*scriptedChat
}

// Send always reports the session as running.
func (c *busyChat) Send(string, string) error { return contracts.ErrSessionRunning }

// newReadyWSClient dials the gateway and completes the handshake (§4.2).
func newReadyWSClient(t *testing.T, gw *runningGateway) *testWSConn {
	t.Helper()
	conn := dialTestWS(t, gw.address, map[string]string{
		"Origin":        gw.info.Origin(),
		"Cookie":        "zk_token=" + gw.info.Token,
		"Authorization": "Bearer " + gw.info.Token,
	})
	conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion})
	conn.call("ready", nil)
	return conn
}

// TestBlockingMethodKeepsTheConnectionResponsive is the "no freeze" half: while
// run.interject is waiting, a ping on the same socket must still be answered.
func TestBlockingMethodKeepsTheConnectionResponsive(t *testing.T) {
	chat := &busyChat{scriptedChat: newScriptedChat()}
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)
	conn := newReadyWSClient(t, gw)
	defer conn.close()

	started := time.Now()
	conn.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "run.interject",
		"params": map[string]any{"sessionId": "s1", "text": "steer"},
	})

	// Let the interject enter its retry loop first, so the ping below really is
	// served while the other request is in flight.
	time.Sleep(150 * time.Millisecond)

	// The blocked call must not delay an unrelated request on the same socket.
	pingStart := time.Now()
	reply := conn.call("ping", nil)
	if elapsed := time.Since(pingStart); elapsed > 500*time.Millisecond {
		t.Fatalf("ping waited %s behind the blocked method (want < 500ms)", elapsed)
	}
	if reply["error"] != nil {
		t.Fatalf("ping reply = %v", reply)
	}

	// Cancel the blocked call; it must answer long before its 3 s deadline.
	cancelStart := time.Now()
	conn.send(map[string]any{
		"jsonrpc": "2.0", "method": "$/cancel",
		"params": map[string]any{"id": 1},
	})
	interject := conn.receive(func(method string) bool { return method == "" })
	if elapsed := time.Since(cancelStart); elapsed > 1500*time.Millisecond {
		t.Fatalf("cancel took %s (want the call to end promptly, not wait out its deadline)", elapsed)
	}
	errorObject, _ := interject["error"].(map[string]any)
	if errorObject == nil {
		t.Fatalf("cancelled call replied %v, want error 1026", interject)
	}
	if code, _ := errorObject["code"].(float64); int(code) != codeCancelled {
		t.Fatalf("cancelled call code = %v, want %d", errorObject["code"], codeCancelled)
	}
	if total := time.Since(started); total > 2500*time.Millisecond {
		t.Fatalf("the whole exchange took %s; the blocking path was not cut short", total)
	}
}
