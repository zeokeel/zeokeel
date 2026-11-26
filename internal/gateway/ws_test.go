package gateway

// Transport-level tests for the control-plane socket: what the gateway accepts
// on the wire now that github.com/coder/websocket owns the framing. They pin the
// two halves of the frame budget the hand-written server used to enforce: a
// frame well above the library's own 32 KiB default must still be dispatched,
// and a frame above the gateway's 16 MiB budget must be refused instead of
// buffered.
//
// The client side is the raw RFC 6455 fixture in wstest_test.go, so these assert
// on the real wire rather than on the library talking to itself.

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// readyTestClient dials the gateway and completes the two-step handshake, so the
// following frames reach the dispatcher instead of the "initialize first" gate.
func readyTestClient(t *testing.T, gw *runningGateway) *testWSConn {
	t.Helper()
	conn := dialTestWS(t, gw.address, map[string]string{
		"Origin":        gw.info.Origin(),
		"Authorization": "Bearer " + gw.info.Token,
	})
	t.Cleanup(conn.close)
	resultOf(t, conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion}))
	resultOf(t, conn.call("ready", nil))
	return conn
}

// TestWebSocketDispatchesFramesWellAboveTheLibraryDefault proves the library's
// own 32 KiB read limit is not what the gateway runs with: a 256 KiB JSON-RPC
// frame is read, parsed and answered.
func TestWebSocketDispatchesFramesWellAboveTheLibraryDefault(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	conn := readyTestClient(t, gw)

	// 256 KiB of padding: eight times the library default, far below 16 MiB.
	blob := strings.Repeat("x", 256<<10)
	conn.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "no.such.method",
		"params":  map[string]any{"blob": blob},
	})
	frame := conn.receive(func(method string) bool { return method == "" })
	protocolErr := errorOf(t, frame)
	if protocolErr["code"] != float64(codeMethodNotFound) {
		t.Fatalf("error = %v, want code %d (the frame must reach the dispatcher)", protocolErr, codeMethodNotFound)
	}
}

// TestWebSocketRefusesFramesAboveTheFrameBudget proves the 16 MiB cap survived
// the swap: one frame above it ends the connection with no answer, so the
// gateway never buffers a frame it would refuse to parse.
func TestWebSocketRefusesFramesAboveTheFrameBudget(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	conn := readyTestClient(t, gw)

	oversized := MaxFrameBytes + (1 << 20)
	prefix := []byte(`{"jsonrpc":"2.0","id":2,"method":"no.such.method","params":{"blob":"`)
	payload := make([]byte, 0, len(prefix)+oversized+2)
	payload = append(payload, prefix...)
	payload = append(payload, bytes.Repeat([]byte("x"), oversized)...)
	payload = append(payload, '"', '}')

	// The gateway hangs up as soon as the limit is crossed, so the write may end
	// in a broken pipe instead of completing: that is the behaviour under test.
	_ = conn.writeMaskedTextAllowError(payload)

	// Nothing may be answered; the connection must simply end.
	_ = conn.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		var head [2]byte
		if _, err := io.ReadFull(conn.reader, head[:]); err != nil {
			return // EOF or a reset: the gateway refused the frame.
		}
		opcode := head[0] & 0x0f
		length := int64(head[1] & 0x7f)
		switch length {
		case 126:
			var extended [2]byte
			if _, err := io.ReadFull(conn.reader, extended[:]); err != nil {
				return
			}
			length = int64(binary.BigEndian.Uint16(extended[:]))
		case 127:
			var extended [8]byte
			if _, err := io.ReadFull(conn.reader, extended[:]); err != nil {
				return
			}
			length = int64(binary.BigEndian.Uint64(extended[:]))
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(conn.reader, body); err != nil {
			return
		}
		if opcode != wireOpText {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal(body, &frame); err != nil {
			continue
		}
		if frame["method"] == nil {
			t.Fatalf("the gateway answered an over-budget frame: %.200s", body)
		}
	}
}

// writeMaskedTextAllowError writes one masked text frame and reports the error
// instead of failing the test: the server is expected to hang up mid-frame.
func (c *testWSConn) writeMaskedTextAllowError(payload []byte) error {
	return c.writeMaskedFrameAllowError(0x1, payload)
}

// writeMaskedFrameAllowError writes one masked frame of the given opcode.
func (c *testWSConn) writeMaskedFrameAllowError(opcode byte, payload []byte) error {
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	head := []byte{0x80 | opcode}
	switch {
	case len(payload) < 126:
		head = append(head, 0x80|byte(len(payload)))
	case len(payload) <= 0xffff:
		head = append(head, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(head[len(head)-2:], uint16(len(payload)))
	default:
		head = append(head, 0x80|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(head[len(head)-8:], uint64(len(payload)))
	}
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	_, err := c.conn.Write(append(append(head, mask...), masked...))
	return err
}

// readFrameAllowEOF reads one server frame, reporting EOF instead of failing.
func readFrameAllowEOF(conn *testWSConn) (byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(conn.reader, head[:]); err != nil {
		return 0, nil, err
	}
	opcode := head[0] & 0x0f
	length := int64(head[1] & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(conn.reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(conn.reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(extended[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn.reader, payload); err != nil {
		return 0, nil, err
	}
	return opcode, payload, nil
}

// TestWebSocketAnswersPingWithPong keeps the keepalive contract: a browser ping
// is answered with a pong carrying the same payload, and the pong is not a data
// message (it never reaches dispatch).
func TestWebSocketAnswersPingWithPong(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	conn := readyTestClient(t, gw)
	_ = conn.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if err := conn.writeMaskedFrameAllowError(0x9, []byte("keepalive")); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	for {
		opcode, payload, err := readFrameAllowEOF(conn)
		if err != nil {
			t.Fatalf("the gateway never answered the ping: %v", err)
		}
		if opcode != 0xA { // pong
			continue
		}
		if string(payload) != "keepalive" {
			t.Fatalf("pong payload = %q, want keepalive", payload)
		}
		return
	}
}

// TestWebSocketEchoesCloseAndHangsUp keeps the close contract: a client close
// frame is answered with a close frame, and the connection is gone afterwards.
func TestWebSocketEchoesCloseAndHangsUp(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	conn := readyTestClient(t, gw)
	_ = conn.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	// Close frame: status 1000, no reason.
	if err := conn.writeMaskedFrameAllowError(0x8, []byte{0x03, 0xE8}); err != nil {
		t.Fatalf("write close: %v", err)
	}
	sawClose := false
	for {
		opcode, payload, err := readFrameAllowEOF(conn)
		if err != nil {
			break // the socket ends after the close handshake
		}
		if opcode != 0x8 {
			continue
		}
		sawClose = true
		if len(payload) < 2 || binary.BigEndian.Uint16(payload[:2]) != 1000 {
			t.Fatalf("close reply payload = %v, want status 1000", payload)
		}
		break
	}
	if !sawClose {
		t.Fatal("the gateway never echoed the close frame")
	}
	// Nothing else may arrive, and the connection must actually be gone.
	_ = conn.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, _, err := readFrameAllowEOF(conn); err == nil {
		t.Fatal("the gateway kept the connection open after the close handshake")
	}
}
