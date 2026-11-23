package gateway

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// testWSConn is a minimal RFC 6455 client: enough to prove the gateway's
// handshake, framing and method dispatch over a real loopback socket. The
// client masks its frames as a browser does.
type testWSConn struct {
	t       *testing.T
	conn    net.Conn
	reader  *bufio.Reader
	pending []map[string]any
}

// dialTestWS performs the client side of the handshake with extra headers.
func dialTestWS(t *testing.T, address string, headers map[string]string) *testWSConn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", address, err)
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("random key: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	request := "GET /ws HTTP/1.1\r\n" +
		"Host: " + address + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + encoded + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n"
	for name, value := range headers {
		request += name + ": " + value + "\r\n"
	}
	request += "\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want 101", response.StatusCode)
	}
	return &testWSConn{t: t, conn: conn, reader: reader}
}

// writeText sends one masked text frame.
func (c *testWSConn) writeText(payload []byte) {
	c.t.Helper()
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		c.t.Fatalf("random mask: %v", err)
	}
	head := []byte{0x81}
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
	if _, err := c.conn.Write(append(append(head, mask...), masked...)); err != nil {
		c.t.Fatalf("write frame: %v", err)
	}
}

// readMessage reads one server frame (the gateway never masks).
func (c *testWSConn) readMessage() (byte, []byte) {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var head [2]byte
	if _, err := io.ReadFull(c.reader, head[:]); err != nil {
		c.t.Fatalf("read frame head: %v", err)
	}
	opcode := head[0] & 0x0f
	length := int64(head[1] & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(c.reader, extended[:]); err != nil {
			c.t.Fatalf("read extended length: %v", err)
		}
		length = int64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(c.reader, extended[:]); err != nil {
			c.t.Fatalf("read extended length: %v", err)
		}
		length = int64(binary.BigEndian.Uint64(extended[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		c.t.Fatalf("read payload: %v", err)
	}
	return opcode, payload
}

// send writes one JSON frame.
func (c *testWSConn) send(value any) {
	c.t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		c.t.Fatalf("marshal: %v", err)
	}
	// One JSON-RPC message per line (§4.1); the terminator is part of the wire
	// format, not of the JSON.
	c.writeText(append(payload, '\n'))
}

// receive reads frames until one satisfies match. Notifications that arrive
// first are returned through the second result when skip is asked for; here we
// simply skip frames that do not match.
func (c *testWSConn) receive(predicate func(method string) bool) map[string]any {
	c.t.Helper()
	for i, frame := range c.pending {
		method, _ := frame["method"].(string)
		if predicate(method) {
			c.pending = append(c.pending[:i], c.pending[i+1:]...)
			return frame
		}
	}
	for {
		opcode, payload := c.readMessage()
		if opcode != wsOpText {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal(payload, &frame); err != nil {
			c.t.Fatalf("decode frame %q: %v", payload, err)
		}
		method, _ := frame["method"].(string)
		if predicate(method) {
			return frame
		}
		c.pending = append(c.pending, frame)
	}
}

// close closes the socket.
func (c *testWSConn) close() { _ = c.conn.Close() }

// call sends a request and returns its response frame.
func (c *testWSConn) call(method string, params any) map[string]any {
	c.t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		request["params"] = params
	}
	c.send(request)
	// Match only responses (no "method"), so any notification sent before the
	// reply — a subscribe replay, for instance — stays buffered for the caller
	// instead of being dropped here.
	return c.receive(func(method string) bool { return method == "" })
}

// resultOf extracts the result object of a response frame.
func resultOf(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	if protocolErr, ok := frame["error"].(map[string]any); ok {
		t.Fatalf("unexpected error response: %v", protocolErr)
	}
	result, ok := frame["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %v", frame)
	}
	return result
}

// errorOf extracts the error object of a response frame.
func errorOf(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	protocolErr, ok := frame["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error response, got %v", frame)
	}
	return protocolErr
}

// cookieFromResponse is the Set-Cookie header value ready for a Cookie header.
func cookieFromResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == tokenCookie {
			return cookie.Name + "=" + cookie.Value
		}
	}
	t.Fatalf("response has no %s cookie: %v", tokenCookie, response.Header)
	return ""
}

// mustJSON marshals a value or fails the test.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(payload)
}
