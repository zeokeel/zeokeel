// Raw websocket transport for the CDP client.
//
// This file is the only place in the module that knows the wire: the RFC 6455
// client handshake, the frame codec, and the CDP envelope. It deliberately uses
// nothing but the standard library (net, crypto/sha1, encoding/base64,
// encoding/binary), because the browser capability must work in a build with no
// third-party dependencies at all.
//
// Shape of the thing: one reader goroutine owns every read. Sends allocate an
// id, write a command frame, and wait on a per-id channel; the reader matches
// replies to those channels and routes id-less frames (CDP events) to a bounded
// public channel. A socket failure fails every pending Send and closes the
// events channel exactly once, so callers never see a half-open transport.
package browser

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// DefaultDialTimeout bounds "TCP connect plus handshake" when DialOptions
	// leaves Timeout zero. It is short on purpose: a local browser either has a
	// debugging port open or it does not.
	DefaultDialTimeout = 15 * time.Second
	// DefaultMaxFrameBytes caps one frame (and one reassembled fragmented
	// message) at 32 MiB. A screenshot is far below this; anything above it is
	// not a CDP message and must not be buffered.
	DefaultMaxFrameBytes = 32 << 20
	// DefaultEventBuffer is how many demuxed events the transport holds before
	// it starts dropping the oldest one.
	DefaultEventBuffer = 256

	// wsAcceptGUID is the fixed string RFC 6455 §1.3 appends to the client key.
	wsAcceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

// WebSocket opcodes (RFC 6455 §5.2).
const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

// closeNormal is the RFC 6455 §7.4.1 code for "normal closure".
const closeNormal = 1000

// DialOptions configures one websocket connection. The zero value is usable.
type DialOptions struct {
	// Timeout bounds the TCP connect and the handshake (zero =
	// DefaultDialTimeout). It also bounds a single frame write, so a stalled
	// peer cannot block Send forever.
	Timeout time.Duration
	// MaxFrameBytes caps the payload of one incoming frame and of one
	// reassembled fragmented message (zero = DefaultMaxFrameBytes). A frame
	// above the cap tears the connection down instead of being buffered.
	MaxFrameBytes int
	// Origin, when set, is sent as the Origin header. Chrome refuses
	// DevTools websocket handshakes that carry a foreign Origin, so this is
	// normally left empty.
	Origin string
}

// withDefaults fills the zero fields.
func (o DialOptions) withDefaults() DialOptions {
	if o.Timeout <= 0 {
		o.Timeout = DefaultDialTimeout
	}
	if o.MaxFrameBytes <= 0 {
		o.MaxFrameBytes = DefaultMaxFrameBytes
	}
	return o
}

// Event is one CDP event: a frame the browser pushed that is not the reply to a
// command. Method and Params are the documented CDP fields; SessionID is set
// when the event came from a page session. Raw keeps the original envelope for
// callers that need a field this struct does not name.
type Event struct {
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// CDPError is a command the browser rejected (`{"id":N,"error":{...}}`). It
// unwraps to ErrBrowserUnavailable, because from a caller's point of view the
// browser did not perform the operation; Code and Message keep the diagnosis.
type CDPError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *CDPError) Error() string {
	if e == nil {
		return "browser: cdp error"
	}
	if e.Message == "" {
		return fmt.Sprintf("browser: cdp error %d", e.Code)
	}
	if e.Code == 0 {
		return "browser: cdp error: " + e.Message
	}
	return fmt.Sprintf("browser: cdp error %d: %s", e.Code, e.Message)
}

// Unwrap makes errors.Is(err, contracts.ErrBrowserUnavailable) true for a
// rejected command.
func (e *CDPError) Unwrap() error { return contracts.ErrBrowserUnavailable }

// WS is a websocket-backed CDP Transport. It implements Transport, so it plugs
// straight into Config.Transport. It is safe for concurrent Send calls; Events
// and Close may be used from any goroutine.
type WS struct {
	conn   net.Conn
	reader *bufio.Reader
	opts   DialOptions

	// writeMu serialises frame writes (commands, pongs, the closing frame).
	writeMu sync.Mutex

	// mu guards the id counter, the pending table and the closed flag. It is
	// taken by enqueue/forget/deliver/teardown, never while a write is in
	// flight, so a slow peer cannot block teardown past the write deadline.
	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan sendResult
	closed  bool
	failErr error

	events  chan Event
	dropped atomic.Uint64
	done    chan struct{}

	closeOnce sync.Once
}

// sendResult is one reply delivered to a waiting Send.
type sendResult struct {
	raw json.RawMessage
	err error
}

// commandRequest is the CDP envelope this transport writes. SessionID is
// omitted when no page session rides on the context.
type commandRequest struct {
	ID        uint64 `json:"id"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

// DialTransport connects to a CDP websocket endpoint (ws:// or wss://) and returns
// a live Transport. The context bounds the connect and the handshake only; after
// that the connection lives until Close (or a socket failure).
func DialTransport(ctx context.Context, endpoint string, opts DialOptions) (*WS, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	opts = opts.withDefaults()
	target, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	conn, err := dialWebSocket(dialCtx, target)
	if err != nil {
		return nil, mapDialError(ctx, err)
	}
	reader, err := wsHandshake(dialCtx, conn, target, opts.Origin)
	if err != nil {
		_ = conn.Close()
		return nil, mapDialError(ctx, err)
	}
	// The handshake deadline was a dial deadline; frames manage their own.
	_ = conn.SetDeadline(time.Time{})

	ws := &WS{
		conn:    conn,
		reader:  reader,
		opts:    opts,
		pending: make(map[uint64]chan sendResult),
		events:  make(chan Event, DefaultEventBuffer),
		done:    make(chan struct{}),
	}
	go ws.readLoop()
	return ws, nil
}

// Send writes one CDP command and waits for its reply. The reply's `result` is
// returned raw; a `error` payload becomes a *CDPError; a deadline in ctx is
// reported as ErrBrowserTimeout; a connection that is gone (or that fails while
// the command is in flight) is reported as ErrBrowserUnavailable.
func (w *WS) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if w == nil {
		return nil, fmt.Errorf("%w: nil websocket transport", contracts.ErrBrowserUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if method == "" {
		return nil, fmt.Errorf("%w: empty cdp method", ErrProtocol)
	}
	if err := ctx.Err(); err != nil {
		return nil, mapCtxError(ctx, err)
	}

	id, reply, err := w.enqueue()
	if err != nil {
		return nil, err
	}
	defer w.forget(id)

	session, _ := SessionFrom(ctx)
	payload, err := encodeCommand(id, method, params, session)
	if err != nil {
		return nil, err
	}
	if err := w.writeFrame(opText, payload); err != nil {
		failure := socketFailure(err)
		w.teardown(failure)
		return nil, failure
	}

	select {
	case res := <-reply:
		return res.raw, res.err
	case <-ctx.Done():
		// The id is forgotten by the deferred forget, so a reply that shows up
		// after this point is dropped by the reader instead of being handed to
		// the next Send.
		return nil, mapCtxError(ctx, ctx.Err())
	case <-w.done:
		return nil, w.failure()
	}
}

// Events is the demultiplexed event stream. Frames that carry no id land here.
// The channel is closed when the connection ends.
func (w *WS) Events() <-chan Event { return w.events }

// DroppedEvents counts events discarded because the buffer was full. Reads are
// lock-free so a status surface can poll it.
func (w *WS) DroppedEvents() uint64 { return w.dropped.Load() }

// Close tears the connection down: pending Sends fail, the events channel is
// closed once, and the socket is closed. It is idempotent and always returns
// nil (a close error has nowhere useful to go: the connection is gone either
// way).
func (w *WS) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return nil
	}
	// Best effort: tell the peer this is a normal closure, but never wait for a
	// peer (or a Send) that has stopped making progress. teardown below is the
	// idempotent part (it holds the sync.Once).
	w.writeCloseFrame()
	w.teardown(nil)
	return nil
}

// enqueue allocates the next command id and registers its reply channel.
func (w *WS) enqueue() (uint64, chan sendResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, nil, w.closedErrorLocked()
	}
	w.nextID++
	id := w.nextID
	reply := make(chan sendResult, 1)
	w.pending[id] = reply
	return id, reply, nil
}

// forget drops a command that is no longer waiting (answered or timed out).
func (w *WS) forget(id uint64) {
	w.mu.Lock()
	delete(w.pending, id)
	w.mu.Unlock()
}

// closedErrorLocked is the "already closed" error; mu must be held.
func (w *WS) closedErrorLocked() error {
	if w.failErr != nil {
		return w.failErr
	}
	return fmt.Errorf("%w: websocket is closed", contracts.ErrBrowserUnavailable)
}

// failure reports why the connection ended.
func (w *WS) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closedErrorLocked()
}

// teardown closes the socket, fails every pending Send and closes the events
// channel exactly once. reason is what callers waiting on the wire will see.
func (w *WS) teardown(reason error) {
	w.closeOnce.Do(func() {
		if reason == nil {
			reason = fmt.Errorf("%w: websocket closed", contracts.ErrBrowserUnavailable)
		}
		reason = socketFailure(reason)
		w.mu.Lock()
		w.closed = true
		w.failErr = reason
		pending := w.pending
		w.pending = make(map[uint64]chan sendResult)
		close(w.done)
		close(w.events)
		w.mu.Unlock()

		for _, reply := range pending {
			select {
			case reply <- sendResult{err: reason}:
			default:
			}
		}
		_ = w.conn.Close()
	})
}

// readLoop owns every read from the socket until it fails, the peer closes, or
// a protocol violation tears the connection down.
func (w *WS) readLoop() {
	var (
		fragmented bool
		assembled  []byte
	)
	for {
		fin, opcode, payload, err := w.readFrame()
		if err != nil {
			w.teardown(socketFailure(err))
			return
		}
		switch opcode {
		case opText, opBinary:
			if fragmented {
				w.teardown(fmt.Errorf("%w: websocket: new data frame inside a fragmented message", ErrProtocol))
				return
			}
			if fin {
				if !w.dispatch(payload) {
					return
				}
				continue
			}
			fragmented = true
			assembled = append(assembled[:0], payload...)
		case opContinuation:
			if !fragmented {
				w.teardown(fmt.Errorf("%w: websocket: continuation frame without a start", ErrProtocol))
				return
			}
			assembled = append(assembled, payload...)
			if len(assembled) > w.opts.MaxFrameBytes {
				w.teardown(frameTooLarge(len(assembled), w.opts.MaxFrameBytes))
				return
			}
			if fin {
				fragmented = false
				message := assembled
				assembled = nil
				if !w.dispatch(message) {
					return
				}
			}
		case opPing:
			if err := w.writeFrame(opPong, payload); err != nil {
				w.teardown(socketFailure(err))
				return
			}
		case opPong:
			// Keepalive acknowledgement: the reader drops it. It must never
			// reach Events() or a waiting Send.
		case opClose:
			// Reply with a close frame then hang up; pending Sends fail.
			w.writeCloseFrame()
			w.teardown(fmt.Errorf("%w: peer closed the websocket", contracts.ErrBrowserUnavailable))
			return
		default:
			w.teardown(fmt.Errorf("%w: websocket: unsupported opcode 0x%x", ErrProtocol, opcode))
			return
		}
	}
}

// dispatch routes one complete data message. It reports false when the message
// is fatal and the connection has been torn down.
func (w *WS) dispatch(payload []byte) bool {
	var envelope struct {
		ID        json.RawMessage `json:"id"`
		Method    string          `json:"method"`
		Params    json.RawMessage `json:"params"`
		Result    json.RawMessage `json:"result"`
		Error     *CDPError       `json:"error"`
		SessionID string          `json:"sessionId"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		w.teardown(fmt.Errorf("%w: websocket: undecodable cdp frame: %v", ErrProtocol, err))
		return false
	}

	if len(envelope.ID) > 0 && string(envelope.ID) != "null" {
		var id uint64
		if err := json.Unmarshal(envelope.ID, &id); err != nil {
			// An id we cannot read cannot belong to a pending Send. One that
			// also carries a method is still a usable event.
			if envelope.Method == "" {
				return true
			}
		} else {
			var res sendResult
			if envelope.Error != nil {
				res.err = envelope.Error
			} else {
				res.raw = envelope.Result
			}
			w.deliver(id, res)
			return true
		}
	}

	if envelope.Method == "" {
		// No id and no method: not a CDP message this transport can use.
		return true
	}
	w.pushEvent(Event{
		Method:    envelope.Method,
		Params:    envelope.Params,
		SessionID: envelope.SessionID,
		Raw:       append(json.RawMessage(nil), payload...),
	})
	return true
}

// deliver hands a reply to its waiting Send. A reply for an unknown id (a late
// answer to a command that already timed out) is dropped.
func (w *WS) deliver(id uint64, res sendResult) {
	w.mu.Lock()
	reply := w.pending[id]
	w.mu.Unlock()
	if reply == nil {
		return
	}
	select {
	case reply <- res:
	default:
	}
}

// pushEvent appends an event, dropping the oldest one when the buffer is full.
// It holds mu so it can never race with teardown closing the channel.
func (w *WS) pushEvent(event Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	select {
	case w.events <- event:
		return
	default:
	}
	select {
	case <-w.events:
		w.dropped.Add(1)
	default:
	}
	select {
	case w.events <- event:
	default:
	}
}

// readFrame reads one frame and returns its payload (already unmasked).
func (w *WS) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var head [2]byte
	if _, err := io.ReadFull(w.reader, head[:]); err != nil {
		return false, 0, nil, err
	}
	fin = head[0]&0x80 != 0
	if head[0]&0x70 != 0 {
		return false, 0, nil, fmt.Errorf("%w: websocket: reserved bits set in frame header", ErrProtocol)
	}
	opcode = head[0] & 0x0f
	masked := head[1]&0x80 != 0

	length := int64(head[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(w.reader, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(w.reader, ext[:]); err != nil {
			return false, 0, nil, err
		}
		raw := binary.BigEndian.Uint64(ext[:])
		if raw > uint64(1<<62) {
			return false, 0, nil, fmt.Errorf("%w: websocket: frame length %d is out of range", ErrProtocol, raw)
		}
		length = int64(raw)
	}
	if length < 0 || length > int64(w.opts.MaxFrameBytes) {
		// Do not read the payload: the connection is not trustworthy any more,
		// and buffering it is exactly what the cap forbids.
		return false, 0, nil, frameTooLarge(int(length), w.opts.MaxFrameBytes)
	}
	if opcode >= opClose && (!fin || length > 125) {
		return false, 0, nil, fmt.Errorf("%w: websocket: malformed control frame", ErrProtocol)
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(w.reader, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	if length == 0 {
		return fin, opcode, nil, nil
	}
	payload = make([]byte, int(length))
	if _, err := io.ReadFull(w.reader, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, opcode, payload, nil
}

// writeFrame writes one masked client frame. CDP commands are small; a single
// frame is always enough (the transport never fragments what it sends).
func (w *WS) writeFrame(opcode byte, payload []byte) error {
	return w.writeFrameDeadline(opcode, payload, time.Now().Add(w.opts.Timeout))
}

// writeCloseFrame sends a normal-closure frame without ever waiting for the
// write lock: if a Send is already mid-write the peer is not reading, and
// queueing behind it would make Close as slow as that stalled write.
func (w *WS) writeCloseFrame() {
	if !w.writeMu.TryLock() {
		return
	}
	defer w.writeMu.Unlock()
	_ = w.writeFrameLocked(opClose, closePayload(closeNormal), time.Now().Add(100*time.Millisecond))
}

// writeFrameDeadline writes one masked client frame with an explicit deadline.
func (w *WS) writeFrameDeadline(opcode byte, payload []byte, deadline time.Time) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return w.writeFrameLocked(opcode, payload, deadline)
}

// writeFrameLocked writes one masked client frame; writeMu must be held.
func (w *WS) writeFrameLocked(opcode byte, payload []byte, deadline time.Time) error {
	var mask [4]byte
	if _, err := io.ReadFull(rand.Reader, mask[:]); err != nil {
		return fmt.Errorf("websocket: mask key: %w", err)
	}
	length := len(payload)
	header := make([]byte, 0, 14)
	header = append(header, 0x80|opcode) // FIN | opcode
	switch {
	case length < 126:
		header = append(header, byte(length)|0x80)
	case length <= 0xffff:
		header = append(header, 126|0x80, byte(length>>8), byte(length))
	default:
		header = append(header, 127|0x80)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(length))
		header = append(header, ext[:]...)
	}
	header = append(header, mask[:]...)

	frame := make([]byte, len(header)+length)
	copy(frame, header)
	for i := 0; i < length; i++ {
		frame[len(header)+i] = payload[i] ^ mask[i%4]
	}

	_ = w.conn.SetWriteDeadline(deadline)
	if _, err := w.conn.Write(frame); err != nil {
		return err
	}
	return nil
}

// closePayload encodes a close frame body (status code only).
func closePayload(code uint16) []byte {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], code)
	return buf[:]
}

// encodeCommand marshals a CDP command envelope. The session id is a context
// property (WithSession), never a method parameter.
func encodeCommand(id uint64, method string, params any, session string) ([]byte, error) {
	payload, err := json.Marshal(commandRequest{ID: id, Method: method, Params: params, SessionID: session})
	if err != nil {
		return nil, fmt.Errorf("%w: encode %q: %v", ErrProtocol, method, err)
	}
	return payload, nil
}

// parseEndpoint validates a ws:// or wss:// endpoint.
func parseEndpoint(endpoint string) (*url.URL, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty websocket endpoint", contracts.ErrBrowserUnavailable)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: endpoint %q: %v", contracts.ErrBrowserUnavailable, raw, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "ws", "wss":
	case "":
		return nil, fmt.Errorf("%w: endpoint %q has no ws:// or wss:// scheme", contracts.ErrBrowserUnavailable, raw)
	default:
		return nil, fmt.Errorf("%w: endpoint scheme %q is not ws/wss", contracts.ErrBrowserUnavailable, parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: endpoint %q has no host", contracts.ErrBrowserUnavailable, raw)
	}
	return parsed, nil
}

// dialWebSocket opens the TCP (and, for wss, TLS) connection.
func dialWebSocket(ctx context.Context, target *url.URL) (net.Conn, error) {
	port := target.Port()
	if port == "" {
		if strings.EqualFold(target.Scheme, "wss") {
			port = "443"
		} else {
			port = "80"
		}
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Hostname(), port))
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(target.Scheme, "wss") {
		secure := tls.Client(conn, &tls.Config{
			ServerName: target.Hostname(),
			MinVersion: tls.VersionTLS12,
		})
		if err := secure.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return secure, nil
	}
	return conn, nil
}

// wsHandshake performs the RFC 6455 client handshake and verifies the reply.
// The returned reader carries any bytes the server sent right after the 101
// response.
func wsHandshake(ctx context.Context, conn net.Conn, target *url.URL, origin string) (*bufio.Reader, error) {
	var nonce [16]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("%w: websocket: handshake nonce: %v", contracts.ErrBrowserUnavailable, err)
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])

	requestURI := target.RequestURI()
	if requestURI == "" {
		requestURI = "/"
	}
	var request strings.Builder
	fmt.Fprintf(&request, "GET %s HTTP/1.1\r\n", requestURI)
	fmt.Fprintf(&request, "Host: %s\r\n", target.Host)
	request.WriteString("Upgrade: websocket\r\n")
	request.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&request, "Sec-WebSocket-Key: %s\r\n", key)
	request.WriteString("Sec-WebSocket-Version: 13\r\n")
	if origin != "" {
		fmt.Fprintf(&request, "Origin: %s\r\n", origin)
	}
	request.WriteString("\r\n")

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(DefaultDialTimeout))
	}
	if _, err := io.WriteString(conn, request.String()); err != nil {
		// Left unwrapped: mapDialError decides whether this is a timeout or an
		// unavailable browser.
		return nil, fmt.Errorf("websocket: handshake write: %w", err)
	}

	reader := bufio.NewReaderSize(conn, 4096)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, fmt.Errorf("websocket: handshake read: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("%w: websocket: handshake rejected with %s", contracts.ErrBrowserUnavailable, resp.Status)
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Header.Get("Upgrade")), "websocket") {
		return nil, fmt.Errorf("%w: websocket: handshake missing Upgrade: websocket", contracts.ErrBrowserUnavailable)
	}
	if !headerHasToken(resp.Header.Get("Connection"), "upgrade") {
		return nil, fmt.Errorf("%w: websocket: handshake missing Connection: Upgrade", contracts.ErrBrowserUnavailable)
	}
	want := acceptKey(key)
	if got := strings.TrimSpace(resp.Header.Get("Sec-WebSocket-Accept")); got != want {
		return nil, fmt.Errorf("%w: websocket: handshake accept mismatch (got %q, want %q)", contracts.ErrBrowserUnavailable, got, want)
	}
	return reader, nil
}

// acceptKey computes base64(sha1(key + GUID)), the value the server must echo.
func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsAcceptGUID)) // SHA-1 is mandated by RFC 6455 §4.2.2, not chosen here.
	return base64.StdEncoding.EncodeToString(sum[:])
}

// headerHasToken reports whether a comma-separated header holds token
// (case-insensitive), i.e. `Connection: keep-alive, Upgrade`.
func headerHasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// frameTooLarge is the one size error: it names the cap so a log line explains
// itself.
func frameTooLarge(size, cap int) error {
	return fmt.Errorf("%w: frame too large: %d bytes exceeds the %d-byte cap", contracts.ErrBrowserUnavailable, size, cap)
}

// socketFailure wraps a wire error with the "cannot talk to the browser" error.
// Anything that ends the connection is unavailable to the caller, even when the
// cause is a protocol violation; the cause stays in the chain for diagnosis.
func socketFailure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, contracts.ErrBrowserUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", contracts.ErrBrowserUnavailable, err)
}

// mapCtxError turns "the caller gave up" into the typed browser errors: a
// deadline is a timeout, a cancellation is not.
func mapCtxError(ctx context.Context, err error) error {
	if err == nil {
		err = context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", contracts.ErrBrowserTimeout, err)
	}
	return fmt.Errorf("%w: %w", contracts.ErrBrowserUnavailable, err)
}

// mapDialError classifies a failed connect/handshake.
func mapDialError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, contracts.ErrBrowserUnavailable) {
		return err
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%w: dial: %w", contracts.ErrBrowserTimeout, err)
	}
	return fmt.Errorf("%w: %w", contracts.ErrBrowserUnavailable, err)
}
