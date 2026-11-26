package browser

// Wire-level tests: a fake RFC 6455 server (net/http + Hijack) speaks the frame
// codec by hand, so DialTransport is exercised end to end — handshake, client
// masking, ping/pong, fragmentation, the size cap, id demultiplexing — with no
// browser and no third-party websocket library. The server never calls t from
// its own goroutine; it records violations and the test asserts on them, so a
// late frame cannot panic a finished test.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The fake server decodes and encodes frames by hand, so it spells the RFC 6455
// vocabulary out itself. It must not borrow the transport's dialect: a fixture
// that agrees with the code under test proves nothing.
const (
	wireOpContinuation byte = 0x0
	wireOpText         byte = 0x1
	wireOpBinary       byte = 0x2
	wireOpClose        byte = 0x8
	wireOpPing         byte = 0x9
	wireOpPong         byte = 0xA

	// wireCloseNormal is the RFC 6455 §7.4.1 code for "normal closure".
	wireCloseNormal = 1000

	// wireAcceptGUID is the fixed string RFC 6455 §1.3 appends to the key.
	wireAcceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

// wsFrame is one decoded frame, in either direction.
type wsFrame struct {
	fin     bool
	opcode  byte
	payload []byte
}

// wsRequest is one CDP command decoded from the wire.
type wsRequest struct {
	ID           uint64
	Method       string
	Params       json.RawMessage
	SessionID    string
	HasSessionID bool
	Body         []byte
}

// wsControl is one control frame the server saw or sent.
type wsControl struct {
	opcode  byte
	payload []byte
}

// wsServer is the fake CDP websocket endpoint.
type wsServer struct {
	srv      *httptest.Server
	endpoint string

	mu         sync.Mutex
	requests   []wsRequest
	controls   []wsControl
	violations []string
	origin     string
	hasOrigin  bool
	key        string
	version    string
	upgrade    string
	connection string
	requestURI string
}

// wsServerOptions customises the fake endpoint.
type wsServerOptions struct {
	// script runs once per accepted connection, after the 101 response.
	script func(p *wsPeer)
	// rejectStatus, when non-zero, answers the handshake with that HTTP status.
	rejectStatus int
	// breakAccept answers 101 with a wrong Sec-WebSocket-Accept.
	breakAccept bool
	// noUpgrade answers 101 without `Upgrade: websocket`.
	noUpgrade bool
}

// newWSServer starts a fake endpoint. Its script defaults to an echo loop.
func newWSServer(t *testing.T, opts wsServerOptions) *wsServer {
	t.Helper()
	if opts.script == nil {
		opts.script = echoScript
	}
	server := &wsServer{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.origin = r.Header.Get("Origin")
		server.hasOrigin = r.Header.Get("Origin") != ""
		server.key = r.Header.Get("Sec-WebSocket-Key")
		server.version = r.Header.Get("Sec-WebSocket-Version")
		server.upgrade = r.Header.Get("Upgrade")
		server.connection = r.Header.Get("Connection")
		server.requestURI = r.URL.RequestURI()
		server.mu.Unlock()

		if opts.rejectStatus != 0 {
			w.WriteHeader(opts.rejectStatus)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			server.violationf("response writer is not a Hijacker")
			return
		}
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			server.violationf("hijack: %v", err)
			return
		}
		upgrade := "websocket"
		if opts.noUpgrade {
			upgrade = "h2c"
		}
		accept := testAccept(r.Header.Get("Sec-WebSocket-Key"))
		if opts.breakAccept {
			accept = "d3JvbmctYWNjZXB0LXZhbHVl"
		}
		fmt.Fprintf(conn,
			"HTTP/1.1 101 Switching Protocols\r\n"+
				"Upgrade: %s\r\n"+
				"Connection: Upgrade\r\n"+
				"Sec-WebSocket-Accept: %s\r\n\r\n",
			upgrade, accept)

		peer := &wsPeer{server: server, conn: conn, reader: buffered.Reader}
		go func() {
			defer func() { _ = conn.Close() }()
			opts.script(peer)
		}()
	})
	server.srv = httptest.NewServer(handler)
	t.Cleanup(server.srv.Close)
	server.endpoint = "ws" + strings.TrimPrefix(server.srv.URL, "http") + "/devtools/browser/test"
	return server
}

func (s *wsServer) violationf(format string, args ...any) {
	s.mu.Lock()
	s.violations = append(s.violations, fmt.Sprintf(format, args...))
	s.mu.Unlock()
}

func (s *wsServer) record(req wsRequest) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
}

func (s *wsServer) recordControl(opcode byte, payload []byte) {
	s.mu.Lock()
	s.controls = append(s.controls, wsControl{opcode: opcode, payload: append([]byte(nil), payload...)})
	s.mu.Unlock()
}

func (s *wsServer) recorded() []wsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]wsRequest(nil), s.requests...)
}

func (s *wsServer) controlsOf(opcode byte) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]byte
	for _, control := range s.controls {
		if control.opcode == opcode {
			out = append(out, control.payload)
		}
	}
	return out
}

func (s *wsServer) handshakeHeader(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case "origin":
		return s.origin
	case "key":
		return s.key
	case "version":
		return s.version
	case "upgrade":
		return s.upgrade
	case "connection":
		return s.connection
	case "requestURI":
		return s.requestURI
	}
	return ""
}

func (s *wsServer) hasOriginHeader() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasOrigin
}

// requireClean fails the test if the script saw anything RFC 6455 forbids.
func (s *wsServer) requireClean(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.violations) > 0 {
		t.Fatalf("fake server saw protocol violations: %v", s.violations)
	}
}

// wsPeer is one accepted connection, speaking raw frames.
type wsPeer struct {
	server  *wsServer
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
}

// readFrame decodes one client frame, verifying the client side of the RFC
// (masked frames, no reserved bits).
func (p *wsPeer) readFrame() (wsFrame, error) {
	var head [2]byte
	if _, err := io.ReadFull(p.reader, head[:]); err != nil {
		return wsFrame{}, err
	}
	fin := head[0]&0x80 != 0
	if head[0]&0x70 != 0 {
		p.server.violationf("client frame has reserved bits set")
	}
	opcode := head[0] & 0x0f
	masked := head[1]&0x80 != 0
	if !masked {
		p.server.violationf("client frame is not masked")
	}

	length := int64(head[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(p.reader, ext[:]); err != nil {
			return wsFrame{}, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(p.reader, ext[:]); err != nil {
			return wsFrame{}, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if length < 0 || length > 8<<20 {
		return wsFrame{}, fmt.Errorf("fake server: refusing frame of %d bytes", length)
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(p.reader, mask[:]); err != nil {
			return wsFrame{}, err
		}
	}
	payload := make([]byte, int(length))
	if length > 0 {
		if _, err := io.ReadFull(p.reader, payload); err != nil {
			return wsFrame{}, err
		}
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return wsFrame{fin: fin, opcode: opcode, payload: payload}, nil
}

// nextCommand reads frames until one complete CDP command arrives, recording
// control frames on the way.
func (p *wsPeer) nextCommand() (wsRequest, error) {
	for {
		frame, err := p.readFrame()
		if err != nil {
			return wsRequest{}, err
		}
		switch frame.opcode {
		case wireOpText, wireOpBinary:
			payload := frame.payload
			fin := frame.fin
			for !fin {
				var next wsFrame
				next, err = p.readFrame()
				if err != nil {
					return wsRequest{}, err
				}
				if next.opcode != wireOpContinuation {
					p.server.violationf("expected a continuation frame, got 0x%x", next.opcode)
					return wsRequest{}, errors.New("bad fragmentation")
				}
				payload = append(payload, next.payload...)
				fin = next.fin
			}
			request, err := decodeCommand(payload)
			if err != nil {
				p.server.violationf("undecodable command %q: %v", payload, err)
				return wsRequest{}, err
			}
			p.server.record(request)
			return request, nil
		case wireOpClose:
			return wsRequest{}, io.EOF
		case wireOpPing, wireOpPong:
			p.server.recordControl(frame.opcode, frame.payload)
		default:
			p.server.violationf("unexpected client opcode 0x%x", frame.opcode)
			return wsRequest{}, fmt.Errorf("unexpected opcode 0x%x", frame.opcode)
		}
	}
}

// writeFrame writes one unmasked server frame.
func (p *wsPeer) writeFrame(opcode byte, fin bool, payload []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	header := make([]byte, 0, 10)
	first := opcode
	if fin {
		first |= 0x80
	}
	header = append(header, first)
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, byte(length))
	case length <= 0xffff:
		header = append(header, 126, byte(length>>8), byte(length))
	default:
		header = append(header, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(length))
		header = append(header, ext[:]...)
	}
	_, err := p.conn.Write(append(header, payload...))
	return err
}

func (p *wsPeer) writeText(payload []byte) error {
	return p.writeFrame(wireOpText, true, payload)
}

func (p *wsPeer) writeControl(opcode byte, payload []byte) error {
	return p.writeFrame(opcode, true, payload)
}

// writeJSON writes one text frame holding a JSON document.
func (p *wsPeer) writeJSON(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return p.writeText(payload)
}

// writeReply answers a command.
func (p *wsPeer) writeReply(id uint64, result any) error {
	return p.writeJSON(struct {
		ID     uint64 `json:"id"`
		Result any    `json:"result"`
	}{ID: id, Result: result})
}

// writeErrorReply rejects a command.
func (p *wsPeer) writeErrorReply(id uint64, code int, message string) error {
	return p.writeJSON(struct {
		ID    uint64 `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{ID: id, Error: struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
}

// writeEvent pushes one id-less CDP event.
func (p *wsPeer) writeEvent(method string, params any, sessionID string) error {
	envelope := struct {
		Method    string `json:"method"`
		Params    any    `json:"params,omitempty"`
		SessionID string `json:"sessionId,omitempty"`
	}{Method: method, Params: params, SessionID: sessionID}
	return p.writeJSON(envelope)
}

// echoScript answers every command with the method and params it saw.
func echoScript(p *wsPeer) {
	for {
		request, err := p.nextCommand()
		if err != nil {
			return
		}
		result := struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}{Method: request.Method, Params: request.Params}
		if err := p.writeReply(request.ID, result); err != nil {
			return
		}
	}
}

// drainPeer reads frames until the client hangs up, recording control frames.
// Scripts that have already written everything they need it for call this so a
// pong the client sends late is still observed.
func drainPeer(p *wsPeer) {
	for {
		frame, err := p.readFrame()
		if err != nil {
			return
		}
		switch frame.opcode {
		case wireOpPing, wireOpPong:
			p.server.recordControl(frame.opcode, frame.payload)
		case wireOpClose:
			return
		}
	}
}

// decodeCommand parses a CDP command envelope and keeps the raw body so tests
// can pin the exact wire shape.
func decodeCommand(payload []byte) (wsRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return wsRequest{}, err
	}
	var request wsRequest
	if err := json.Unmarshal(fields["id"], &request.ID); err != nil {
		return wsRequest{}, fmt.Errorf("id: %w", err)
	}
	if err := json.Unmarshal(fields["method"], &request.Method); err != nil {
		return wsRequest{}, fmt.Errorf("method: %w", err)
	}
	request.Params = fields["params"]
	if sessionID, ok := fields["sessionId"]; ok {
		request.HasSessionID = true
		if err := json.Unmarshal(sessionID, &request.SessionID); err != nil {
			return wsRequest{}, fmt.Errorf("sessionId: %w", err)
		}
	}
	request.Body = append([]byte(nil), payload...)
	return request, nil
}

// dialTestWS dials the fake server and closes the transport after the test.
func dialTestWS(t *testing.T, server *wsServer, opts DialOptions) *WS {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, err := DialTransport(ctx, server.endpoint, opts)
	if err != nil {
		t.Fatalf("DialTransport(%q): %v", server.endpoint, err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// waitFor polls condition until it holds or the deadline passes.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDialTransportRoundTrip(t *testing.T) {
	server := newWSServer(t, wsServerOptions{})
	ws := dialTestWS(t, server, DialOptions{})

	raw, err := ws.Send(context.Background(), "Target.createTarget", createTargetParams{
		URL:       "https://example.com/",
		NewWindow: false,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var echoed struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &echoed); err != nil {
		t.Fatalf("reply %q is not JSON: %v", raw, err)
	}
	if echoed.Method != "Target.createTarget" {
		t.Errorf("echoed method = %q, want Target.createTarget", echoed.Method)
	}
	if want := `{"url":"https://example.com/","newWindow":false}`; string(echoed.Params) != want {
		t.Errorf("params on the wire = %s, want %s", echoed.Params, want)
	}

	requests := server.recorded()
	if len(requests) != 1 {
		t.Fatalf("server decoded %d commands, want 1", len(requests))
	}
	command := requests[0]
	if command.ID != 1 {
		t.Errorf("first command id = %d, want 1", command.ID)
	}
	if command.HasSessionID {
		t.Errorf("browser-level command carried sessionId %q", command.SessionID)
	}
	wantBody := `{"id":1,"method":"Target.createTarget","params":{"url":"https://example.com/","newWindow":false}}`
	if string(command.Body) != wantBody {
		t.Errorf("command body = %s, want %s", command.Body, wantBody)
	}

	// A command without params must not invent a `params` field.
	if _, err := ws.Send(context.Background(), "Target.getTargets", nil); err != nil {
		t.Fatalf("Send(no params): %v", err)
	}
	requests = server.recorded()
	if len(requests) != 2 {
		t.Fatalf("server decoded %d commands, want 2", len(requests))
	}
	if want := `{"id":2,"method":"Target.getTargets"}`; string(requests[1].Body) != want {
		t.Errorf("no-params body = %s, want %s", requests[1].Body, want)
	}

	// The handshake itself: RFC 6455 fields, a 16-byte nonce, the request path.
	key := server.handshakeHeader("key")
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		t.Errorf("Sec-WebSocket-Key = %q (%d bytes), want 16 random bytes in base64", key, len(decoded))
	}
	if got := server.handshakeHeader("version"); got != "13" {
		t.Errorf("Sec-WebSocket-Version = %q, want 13", got)
	}
	if got := server.handshakeHeader("upgrade"); !strings.EqualFold(got, "websocket") {
		t.Errorf("Upgrade = %q, want websocket", got)
	}
	if got := server.handshakeHeader("connection"); !hasToken(got, "upgrade") {
		t.Errorf("Connection = %q, want Upgrade", got)
	}
	if got := server.handshakeHeader("requestURI"); got != "/devtools/browser/test" {
		t.Errorf("request URI = %q, want /devtools/browser/test", got)
	}
	if server.hasOriginHeader() {
		t.Errorf("Origin = %q, want absent when DialOptions.Origin is empty", server.handshakeHeader("origin"))
	}
	server.requireClean(t)
}

func TestDialTimeoutDoesNotBoundTheLiveConnection(t *testing.T) {
	server := newWSServer(t, wsServerOptions{})
	ws := dialTestWS(t, server, DialOptions{Timeout: 200 * time.Millisecond})

	// The dial deadline is a handshake deadline: once the socket is up, no part
	// of it may survive onto the live connection (an http.Client deadline, or a
	// read deadline set for the handshake, would each kill it when it fires).
	time.Sleep(600 * time.Millisecond) // three dial timeouts
	if _, err := ws.Send(context.Background(), "Target.getTargets", nil); err != nil {
		t.Fatalf("Send after the dial deadline passed: %v", err)
	}
	server.requireClean(t)
}

func TestDialTransportSendsOriginOnlyWhenAsked(t *testing.T) {
	bare := newWSServer(t, wsServerOptions{})
	dialTestWS(t, bare, DialOptions{})
	if bare.hasOriginHeader() {
		t.Errorf("Origin sent without DialOptions.Origin: %q", bare.handshakeHeader("origin"))
	}

	withOrigin := newWSServer(t, wsServerOptions{})
	dialTestWS(t, withOrigin, DialOptions{Origin: "https://app.example"})
	if got := withOrigin.handshakeHeader("origin"); got != "https://app.example" {
		t.Errorf("Origin = %q, want https://app.example", got)
	}
	bare.requireClean(t)
	withOrigin.requireClean(t)
}

func TestSendCarriesPageSessionFromContext(t *testing.T) {
	server := newWSServer(t, wsServerOptions{})
	ws := dialTestWS(t, server, DialOptions{})

	pageCtx := WithSession(context.Background(), "T-7")
	if _, err := ws.Send(pageCtx, "Runtime.evaluate", evaluateParams{Expression: "document.title", ReturnByValue: true}); err != nil {
		t.Fatalf("Send(scoped): %v", err)
	}
	if _, err := ws.Send(context.Background(), "Target.getTargets", nil); err != nil {
		t.Fatalf("Send(unscoped): %v", err)
	}

	requests := server.recorded()
	if len(requests) != 2 {
		t.Fatalf("server decoded %d commands, want 2", len(requests))
	}
	scoped := requests[0]
	if !scoped.HasSessionID || scoped.SessionID != "T-7" {
		t.Errorf("sessionId = (%q, %v), want T-7", scoped.SessionID, scoped.HasSessionID)
	}
	want := `{"id":1,"method":"Runtime.evaluate","params":{"expression":"document.title","returnByValue":true},"sessionId":"T-7"}`
	if string(scoped.Body) != want {
		t.Errorf("scoped body = %s, want %s", scoped.Body, want)
	}
	if requests[1].HasSessionID {
		t.Errorf("unscoped command carried sessionId %q", requests[1].SessionID)
	}
	server.requireClean(t)
}

func TestSendMatchesOutOfOrderReplies(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		first, err := p.nextCommand()
		if err != nil {
			return
		}
		second, err := p.nextCommand()
		if err != nil {
			return
		}
		// Answer the second command first: replies are matched by id, never by
		// arrival order.
		if err := p.writeReply(second.ID, map[string]any{"method": second.Method}); err != nil {
			return
		}
		if err := p.writeReply(first.ID, map[string]any{"method": first.Method}); err != nil {
			return
		}
		echoScript(p)
	}})
	ws := dialTestWS(t, server, DialOptions{})

	type outcome struct {
		method string
		raw    json.RawMessage
		err    error
	}
	results := make(chan outcome, 2)
	send := func(method string) {
		raw, err := ws.Send(context.Background(), method, nil)
		results <- outcome{method: method, raw: raw, err: err}
	}
	go send("A.first")
	go send("B.second")

	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("Send(%s): %v", got.method, got.err)
		}
		var echoed struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(got.raw, &echoed); err != nil {
			t.Fatalf("Send(%s) reply %q: %v", got.method, got.raw, err)
		}
		if echoed.Method != got.method {
			t.Errorf("Send(%s) received the reply for %q", got.method, echoed.Method)
		}
	}
	server.requireClean(t)
}

func TestConcurrentSendsGetDistinctIDsAndMatchedReplies(t *testing.T) {
	server := newWSServer(t, wsServerOptions{})
	ws := dialTestWS(t, server, DialOptions{})

	const sends = 16
	var wg sync.WaitGroup
	errs := make(chan error, sends)
	for i := 0; i < sends; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			method := fmt.Sprintf("Test.method%d", i)
			raw, err := ws.Send(context.Background(), method, map[string]int{"n": i})
			if err != nil {
				errs <- fmt.Errorf("Send(%s): %w", method, err)
				return
			}
			var echoed struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(raw, &echoed); err != nil {
				errs <- fmt.Errorf("Send(%s) reply %q: %w", method, raw, err)
				return
			}
			if echoed.Method != method {
				errs <- fmt.Errorf("Send(%s) got the reply for %q", method, echoed.Method)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	requests := server.recorded()
	if len(requests) != sends {
		t.Fatalf("server decoded %d commands, want %d", len(requests), sends)
	}
	ids := make(map[uint64]bool, sends)
	for _, request := range requests {
		if ids[request.ID] {
			t.Errorf("duplicate command id %d", request.ID)
		}
		ids[request.ID] = true
	}
	if len(ids) != sends {
		t.Errorf("distinct ids = %d, want %d", len(ids), sends)
	}
	server.requireClean(t)
}

func TestSendMapsRejectedCommandToCDPError(t *testing.T) {
	const message = "'Page.nope' wasn't found"
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		request, err := p.nextCommand()
		if err != nil {
			return
		}
		_ = p.writeErrorReply(request.ID, -32601, message)
	}})
	ws := dialTestWS(t, server, DialOptions{})

	raw, err := ws.Send(context.Background(), "Page.nope", nil)
	if err == nil {
		t.Fatalf("Send(rejected) = (%q, nil), want an error", raw)
	}
	if !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("Send(rejected) = %v, want errors.Is ErrBrowserUnavailable", err)
	}
	var cdpErr *CDPError
	if !errors.As(err, &cdpErr) {
		t.Fatalf("Send(rejected) = %v, want a *CDPError", err)
	}
	if cdpErr.Code != -32601 || cdpErr.Message != message {
		t.Errorf("CDPError = %+v, want code -32601 and message %q", cdpErr, message)
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("error %q lost the CDP message", err)
	}
	if raw != nil {
		t.Errorf("raw result = %q, want nil on an error reply", raw)
	}
	server.requireClean(t)
}

func TestSendTimeoutIsTypedAndConnectionStaysUsable(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		// Never answer the first command; answer the second, then send the
		// stale first reply so the transport has to drop it.
		first, err := p.nextCommand()
		if err != nil {
			return
		}
		second, err := p.nextCommand()
		if err != nil {
			return
		}
		_ = p.writeReply(second.ID, map[string]any{"method": second.Method})
		_ = p.writeReply(first.ID, map[string]any{"method": first.Method})
		echoScript(p)
	}})
	ws := dialTestWS(t, server, DialOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := ws.Send(ctx, "Slow.method", nil); !errors.Is(err, contracts.ErrBrowserTimeout) {
		t.Fatalf("Send(stalled) = %v, want ErrBrowserTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Send(stalled) took %v, want it to stop at the deadline", elapsed)
	}

	// The socket is still usable: a timeout cancels one command, not the pipe.
	raw, err := ws.Send(context.Background(), "Fast.method", nil)
	if err != nil {
		t.Fatalf("Send(after timeout): %v", err)
	}
	var echoed struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(raw, &echoed); err != nil || echoed.Method != "Fast.method" {
		t.Fatalf("Send(after timeout) = (%q, %v), want the echo of Fast.method", raw, err)
	}

	// A third round trip proves the reader has passed the stale reply for the
	// timed-out command; nothing may leak into Events().
	if _, err := ws.Send(context.Background(), "Third.method", nil); err != nil {
		t.Fatalf("Send(third): %v", err)
	}
	select {
	case event, ok := <-ws.Events():
		if ok {
			t.Fatalf("stale reply leaked into Events(): %+v", event)
		}
	case <-time.After(100 * time.Millisecond):
	}
	server.requireClean(t)
}

func TestSendOnCancelledContextIsUnavailable(t *testing.T) {
	server := newWSServer(t, wsServerOptions{})
	ws := dialTestWS(t, server, DialOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ws.Send(ctx, "Target.getTargets", nil); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("Send(cancelled) = %v, want ErrBrowserUnavailable (a cancel is not a timeout)", err)
	}
	if got := len(server.recorded()); got != 0 {
		t.Errorf("cancelled Send wrote %d commands, want 0", got)
	}
	server.requireClean(t)
}

func TestEventsAreDemuxedFromReplies(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		// A keepalive the client must answer, then a command, then an event
		// and the reply that completes the command.
		if err := p.writeControl(wireOpPing, []byte("keepalive")); err != nil {
			return
		}
		request, err := p.nextCommand()
		if err != nil {
			return
		}
		_ = p.writeEvent("Page.loadEventFired", map[string]any{"timestamp": 12.5}, "T-7")
		_ = p.writeReply(request.ID, map[string]any{"ok": true})
		drainPeer(p)
	}})
	ws := dialTestWS(t, server, DialOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := ws.Send(ctx, "Target.getTargets", nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if want := `{"ok":true}`; string(raw) != want {
		t.Fatalf("Send result = %s, want %s (events must not be returned as command results)", raw, want)
	}

	select {
	case event, ok := <-ws.Events():
		if !ok {
			t.Fatal("events channel closed while the connection is open")
		}
		if event.Method != "Page.loadEventFired" {
			t.Errorf("event.Method = %q, want Page.loadEventFired", event.Method)
		}
		if event.SessionID != "T-7" {
			t.Errorf("event.SessionID = %q, want T-7", event.SessionID)
		}
		if want := `{"timestamp":12.5}`; string(event.Params) != want {
			t.Errorf("event.Params = %s, want %s", event.Params, want)
		}
		if !json.Valid(event.Raw) {
			t.Errorf("event.Raw = %q, want the raw envelope", event.Raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event was not delivered")
	}

	// The client answered the ping (RFC 6455 §5.5.3), with the same payload.
	waitFor(t, "pong", func() bool { return len(server.controlsOf(wireOpPong)) > 0 })
	if pongs := server.controlsOf(wireOpPong); string(pongs[0]) != "keepalive" {
		t.Errorf("pong payload = %q, want keepalive", pongs[0])
	}
	server.requireClean(t)
}

func TestFragmentedMessagesAreReassembled(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		request, err := p.nextCommand()
		if err != nil {
			return
		}
		reply, err := json.Marshal(struct {
			ID     uint64 `json:"id"`
			Result any    `json:"result"`
		}{ID: request.ID, Result: map[string]any{"fragmented": true}})
		if err != nil {
			return
		}
		// Split the reply across three frames: text + 2 continuations.
		cuts := []int{len(reply) / 3, 2 * len(reply) / 3}
		_ = p.writeFrame(wireOpText, false, reply[:cuts[0]])
		_ = p.writeFrame(wireOpContinuation, false, reply[cuts[0]:cuts[1]])
		_ = p.writeFrame(wireOpContinuation, true, reply[cuts[1]:])

		// An event fragmented the same way.
		event := []byte(`{"method":"Page.frameNavigated","params":{"frameId":"F-1"}}`)
		_ = p.writeFrame(wireOpText, false, event[:10])
		_ = p.writeFrame(wireOpContinuation, true, event[10:])
	}})
	ws := dialTestWS(t, server, DialOptions{})

	raw, err := ws.Send(context.Background(), "Page.enable", nil)
	if err != nil {
		t.Fatalf("Send(fragmented reply): %v", err)
	}
	if want := `{"fragmented":true}`; string(raw) != want {
		t.Errorf("fragmented reply = %s, want %s", raw, want)
	}
	select {
	case event := <-ws.Events():
		if event.Method != "Page.frameNavigated" || string(event.Params) != `{"frameId":"F-1"}` {
			t.Errorf("fragmented event = %+v, want Page.frameNavigated/F-1", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fragmented event was not delivered")
	}
	server.requireClean(t)
}

func TestLargePayloadUsesExtendedLengthForms(t *testing.T) {
	big := strings.Repeat("x", 70_000) // > 0xffff: the 127 (8-byte) form
	medium := strings.Repeat("y", 300) // > 125: the 126 (2-byte) form
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		request, err := p.nextCommand()
		if err != nil {
			return
		}
		// The reply itself is large, so the client has to decode 127 too.
		_ = p.writeReply(request.ID, map[string]any{"blob": big})
		echoScript(p)
	}})
	ws := dialTestWS(t, server, DialOptions{})

	raw, err := ws.Send(context.Background(), "Test.big", map[string]string{"blob": medium})
	if err != nil {
		t.Fatalf("Send(medium params): %v", err)
	}
	var echoed struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(raw, &echoed); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if echoed.Blob != big {
		t.Errorf("reply blob is %d bytes, want %d", len(echoed.Blob), len(big))
	}
	requests := server.recorded()
	if len(requests) != 1 {
		t.Fatalf("server decoded %d commands, want 1", len(requests))
	}
	var params struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(requests[0].Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.Blob != medium {
		t.Errorf("params blob is %d bytes, want %d", len(params.Blob), len(medium))
	}
	server.requireClean(t)
}

func TestOversizedFrameTearsDownWithTypedError(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		if _, err := p.nextCommand(); err != nil {
			return
		}
		// A frame above MaxFrameBytes: the transport must refuse to buffer it.
		_ = p.writeText(bytes.Repeat([]byte("z"), 4096))
		// Keep the connection open so the client, not the EOF, is what fails.
		_, _ = io.Copy(io.Discard, p.reader)
	}})
	ws := dialTestWS(t, server, DialOptions{MaxFrameBytes: 1024})

	_, err := ws.Send(context.Background(), "Page.enable", nil)
	if !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("Send(oversized frame) = %v, want ErrBrowserUnavailable", err)
	}
	if !strings.Contains(err.Error(), "frame too large") {
		t.Errorf("error %q does not say the frame was too large", err)
	}
	select {
	case _, ok := <-ws.Events():
		if ok {
			t.Error("events channel delivered a frame after teardown")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events channel was not closed by the teardown")
	}
	if _, err := ws.Send(context.Background(), "Page.enable", nil); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("Send after teardown = %v, want ErrBrowserUnavailable", err)
	}
}

func TestSocketFailureFailsEveryPendingSend(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		// Read two commands and answer neither, then close: both Sends are in
		// flight when the socket dies.
		if _, err := p.nextCommand(); err != nil {
			return
		}
		if _, err := p.nextCommand(); err != nil {
			return
		}
		_ = p.writeControl(wireOpClose, wireClosePayload(wireCloseNormal))
		_, _ = io.Copy(io.Discard, p.reader)
	}})
	ws := dialTestWS(t, server, DialOptions{})

	const sends = 2
	failures := make(chan error, sends)
	for i := 0; i < sends; i++ {
		go func() {
			_, err := ws.Send(context.Background(), "Page.enable", nil)
			failures <- err
		}()
	}
	for i := 0; i < sends; i++ {
		select {
		case err := <-failures:
			if !errors.Is(err, contracts.ErrBrowserUnavailable) {
				t.Errorf("pending Send %d = %v, want ErrBrowserUnavailable", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a pending Send was left waiting after the socket failed")
		}
	}
	// The events channel closes exactly once (a second close would panic) and
	// delivers nothing after the failure.
	select {
	case _, ok := <-ws.Events():
		if ok {
			t.Error("an event was delivered after the socket failed")
		}
	case <-time.After(time.Second):
		t.Fatal("the events channel was not closed by the failure")
	}
}

func TestPeerCloseTearsDown(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		if _, err := p.nextCommand(); err != nil {
			return
		}
		_ = p.writeControl(wireOpClose, wireClosePayload(wireCloseNormal))
		// Drain whatever the client still writes (its close reply).
		_, _ = io.Copy(io.Discard, p.reader)
	}})
	ws := dialTestWS(t, server, DialOptions{})

	_, err := ws.Send(context.Background(), "Page.enable", nil)
	if !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("Send(peer closed) = %v, want ErrBrowserUnavailable", err)
	}
	select {
	case _, ok := <-ws.Events():
		if ok {
			t.Error("event delivered after the peer closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events channel was not closed after the peer closed")
	}
}

func TestEventsOverflowDropsOldestAndCounts(t *testing.T) {
	const total = DefaultEventBuffer + 44
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		request, err := p.nextCommand()
		if err != nil {
			return
		}
		for i := 0; i < total; i++ {
			if err := p.writeEvent("Test.tick", map[string]int{"i": i}, ""); err != nil {
				return
			}
		}
		_ = p.writeReply(request.ID, map[string]any{"ok": true})
	}})
	ws := dialTestWS(t, server, DialOptions{})

	if _, err := ws.Send(context.Background(), "Test.flood", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// The reply was the last frame the server wrote, so the reader has already
	// pushed every event: the drop count is exact, not racy.
	if got := ws.DroppedEvents(); got != total-DefaultEventBuffer {
		t.Errorf("DroppedEvents = %d, want %d", got, total-DefaultEventBuffer)
	}

	indexes := make([]int, 0, DefaultEventBuffer)
	for len(indexes) < DefaultEventBuffer {
		select {
		case event, ok := <-ws.Events():
			if !ok {
				t.Fatalf("events channel closed after %d of %d buffered events", len(indexes), DefaultEventBuffer)
			}
			var params struct {
				I int `json:"i"`
			}
			if err := json.Unmarshal(event.Params, &params); err != nil {
				t.Fatalf("event params %q: %v", event.Params, err)
			}
			indexes = append(indexes, params.I)
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d buffered events were delivered", len(indexes), DefaultEventBuffer)
		}
	}
	// Nothing beyond the buffer may be waiting.
	select {
	case event, ok := <-ws.Events():
		if ok {
			t.Fatalf("unexpected extra event %+v", event)
		}
	case <-time.After(20 * time.Millisecond):
	}
	if indexes[0] != total-DefaultEventBuffer {
		t.Errorf("oldest buffered event = %d, want %d (drop-oldest)", indexes[0], total-DefaultEventBuffer)
	}
	if indexes[len(indexes)-1] != total-1 {
		t.Errorf("newest buffered event = %d, want %d", indexes[len(indexes)-1], total-1)
	}
	server.requireClean(t)
}

func TestCloseIsIdempotent(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		for {
			frame, err := p.readFrame()
			if err != nil {
				return
			}
			if frame.opcode == wireOpClose {
				p.server.recordControl(wireOpClose, frame.payload)
				return
			}
		}
	}})
	ws := dialTestWS(t, server, DialOptions{})

	if err := ws.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("second Close: %v, want nil (idempotent)", err)
	}
	select {
	case _, ok := <-ws.Events():
		if ok {
			t.Error("event received after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events channel was not closed by Close")
	}
	if _, err := ws.Send(context.Background(), "Page.enable", nil); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("Send after Close = %v, want ErrBrowserUnavailable", err)
	}
	waitFor(t, "the client close frame", func() bool { return len(server.controlsOf(wireOpClose)) > 0 })
	server.requireClean(t)
}

func TestDialTransportRejectsBadHandshakes(t *testing.T) {
	cases := map[string]struct {
		options wsServerOptions
		want    string
	}{
		"http status": {options: wsServerOptions{rejectStatus: http.StatusForbidden}, want: "403"},
		// The transport delegates the RFC 6455 handshake to coder/websocket, so
		// the wording of a wire violation is the library's. What is pinned here
		// is unchanged: a server that echoes the wrong accept value is refused.
		"bad accept": {options: wsServerOptions{breakAccept: true}, want: "invalid Sec-WebSocket-Accept"},
		"no upgrade": {options: wsServerOptions{noUpgrade: true}, want: "Upgrade"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			server := newWSServer(t, testCase.options)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			ws, err := DialTransport(ctx, server.endpoint, DialOptions{Timeout: time.Second})
			if err == nil {
				_ = ws.Close()
				t.Fatalf("DialTransport = (%+v, nil), want an error", ws)
			}
			if !errors.Is(err, contracts.ErrBrowserUnavailable) {
				t.Errorf("DialTransport = %v, want ErrBrowserUnavailable", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("error %q does not mention %q", err, testCase.want)
			}
		})
	}
}

func TestDialTransportRejectsBadEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"",
		"   ",
		"127.0.0.1:9222",
		"http://127.0.0.1:9222/devtools/browser/x",
		"ws://",
		"ws:///devtools/browser/x",
		"://nonsense",
	} {
		ws, err := DialTransport(context.Background(), endpoint, DialOptions{})
		if err == nil {
			_ = ws.Close()
			t.Errorf("DialTransport(%q) = nil error, want a rejected endpoint", endpoint)
			continue
		}
		if !errors.Is(err, contracts.ErrBrowserUnavailable) {
			t.Errorf("DialTransport(%q) = %v, want ErrBrowserUnavailable", endpoint, err)
		}
	}
}

func TestDialTransportHandshakeTimeoutIsTyped(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	// Accept and then say nothing at all: a browser that is listening but not
	// answering looks exactly like this.
	var held []net.Conn
	var heldMu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			heldMu.Lock()
			held = append(held, conn)
			heldMu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		heldMu.Lock()
		for _, conn := range held {
			_ = conn.Close()
		}
		heldMu.Unlock()
	})

	endpoint := "ws://" + listener.Addr().String() + "/devtools/browser/slow"
	start := time.Now()
	ws, err := DialTransport(context.Background(), endpoint, DialOptions{Timeout: 100 * time.Millisecond})
	if err == nil {
		_ = ws.Close()
		t.Fatal("DialTransport(silent server) = nil error, want a timeout")
	}
	if !errors.Is(err, contracts.ErrBrowserTimeout) {
		t.Fatalf("DialTransport(silent server) = %v, want ErrBrowserTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("handshake took %v, want it bounded by DialOptions.Timeout", elapsed)
	}
}

func TestAdapterDrivesWS(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nadapter-over-ws")
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		for {
			request, err := p.nextCommand()
			if err != nil {
				return
			}
			switch request.Method {
			case "Target.createTarget":
				_ = p.writeReply(request.ID, map[string]any{"targetId": "T-1"})
			case "Runtime.evaluate":
				if !request.HasSessionID || request.SessionID != "T-1" {
					p.server.violationf("Runtime.evaluate arrived with sessionId (%q, %v)", request.SessionID, request.HasSessionID)
				}
				_ = p.writeReply(request.ID, map[string]any{
					"result": map[string]any{"type": "string", "value": "Example Domain"},
				})
			case "Page.captureScreenshot":
				if !request.HasSessionID || request.SessionID != "T-1" {
					p.server.violationf("Page.captureScreenshot arrived with sessionId (%q, %v)", request.SessionID, request.HasSessionID)
				}
				_ = p.writeReply(request.ID, map[string]any{
					"data": base64.StdEncoding.EncodeToString(png),
				})
			case "Target.closeTarget":
				_ = p.writeReply(request.ID, map[string]any{"success": true})
			default:
				p.server.violationf("unexpected method %q", request.Method)
				return
			}
		}
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, err := DialTransport(ctx, server.endpoint, DialOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("DialTransport: %v", err)
	}
	// This is the whole adapter wiring: endpoint plus the dialled transport.
	adapter, err := New(Config{Endpoint: server.endpoint, Transport: ws})
	if err != nil {
		t.Fatalf("New(adapter): %v", err)
	}
	defer func() { _ = ws.Close() }()

	page, err := adapter.Open(ctx, "https://example.com/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if page.ID != "T-1" {
		t.Fatalf("page.ID = %q, want T-1", page.ID)
	}
	title, err := adapter.Title(ctx, page.ID)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if title != "Example Domain" {
		t.Errorf("Title = %q, want Example Domain", title)
	}
	shot, err := adapter.Screenshot(ctx, page.ID, true)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if !bytes.Equal(shot, png) {
		t.Errorf("Screenshot = %q, want %q", shot, png)
	}
	if err := adapter.Close(ctx, page.ID); err != nil {
		t.Fatalf("Close(page): %v", err)
	}
	server.requireClean(t)
}

func TestAdapterOperationTimeoutOverWSIsATimeout(t *testing.T) {
	server := newWSServer(t, wsServerOptions{script: func(p *wsPeer) {
		// Accept the command and never answer it: a hung browser.
		_, _ = p.nextCommand()
		_, _ = io.Copy(io.Discard, p.reader)
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, err := DialTransport(ctx, server.endpoint, DialOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("DialTransport: %v", err)
	}
	defer func() { _ = ws.Close() }()

	adapter, err := New(Config{Endpoint: server.endpoint, Transport: ws, Timeout: 150 * time.Millisecond})
	if err != nil {
		t.Fatalf("New(adapter): %v", err)
	}
	if _, err := adapter.Open(ctx, "https://example.com/"); !errors.Is(err, contracts.ErrBrowserTimeout) {
		t.Fatalf("Open(hung browser) = %v, want ErrBrowserTimeout through the adapter", err)
	}
}

// wireClosePayload encodes a close frame body (status code only).
func wireClosePayload(code uint16) []byte {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], code)
	return buf[:]
}

// testAccept computes the value the server must echo. It is written out here,
// rather than reusing the product helper, so a broken accept check in ws.go
// cannot make the handshake test agree with itself.
func testAccept(key string) string {
	sum := sha1.Sum([]byte(key + wireAcceptGUID)) //nolint:gosec // SHA-1 is fixed by RFC 6455.
	return base64.StdEncoding.EncodeToString(sum[:])
}

// hasToken reports whether a comma-separated header value holds token.
func hasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}
