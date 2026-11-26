// Websocket transport for the CDP client.
//
// This file is the only place in the module that knows the wire. The RFC 6455
// part of it — the client handshake, the frame codec (masking, extended
// lengths, fragmentation), ping/pong and the close handshake — is
// github.com/coder/websocket's job. What stays here is the CDP envelope and the
// transport's concurrency shape.
//
// Shape of the thing: one reader goroutine owns every read (the library allows
// exactly one live reader, and something must always read or control frames go
// unanswered). Sends allocate an id, write a command message, and wait on a
// per-id channel; the reader matches replies to those channels and routes
// id-less frames (CDP events) to a bounded public channel. A socket failure
// fails every pending Send and closes the events channel exactly once, so
// callers never see a half-open transport.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// DefaultDialTimeout bounds "TCP connect plus handshake" when DialOptions
	// leaves Timeout zero. It is short on purpose: a local browser either has a
	// debugging port open or it does not.
	DefaultDialTimeout = 15 * time.Second
	// DefaultMaxFrameBytes caps one incoming message (the library reassembles
	// fragmented frames before the cap applies) at 32 MiB. A screenshot is far
	// below this; anything above it is not a CDP message and must not be
	// buffered.
	DefaultMaxFrameBytes = 32 << 20
	// DefaultEventBuffer is how many demuxed events the transport holds before
	// it starts dropping the oldest one.
	DefaultEventBuffer = 256
)

// DialOptions configures one websocket connection. The zero value is usable.
type DialOptions struct {
	// Timeout bounds the TCP connect and the handshake (zero =
	// DefaultDialTimeout). It also bounds a single message write, so a stalled
	// peer cannot block Send forever.
	Timeout time.Duration
	// MaxFrameBytes caps the payload of one incoming message (zero =
	// DefaultMaxFrameBytes). A message above the cap tears the connection down
	// instead of being buffered.
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
	conn *websocket.Conn
	opts DialOptions

	// mu guards the id counter, the pending table and the closed flag. It is
	// taken by enqueue/forget/deliver/teardown, never while a write is in
	// flight, so a slow peer cannot block teardown past the write deadline.
	mu       sync.Mutex
	nextID   uint64
	pending  map[uint64]chan sendResult
	closed   bool
	graceful bool
	failErr  error

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

	// Only the handshake headers are ours; the library builds the request.
	//
	// DialOptions.Timeout is enforced through dialCtx alone. It is a handshake
	// deadline and nothing else: no deadline may survive onto the live socket
	// (TestDialTimeoutDoesNotBoundTheLiveConnection pins that).
	dialOptions := &websocket.DialOptions{HTTPHeader: http.Header{}}
	if opts.Origin != "" {
		dialOptions.HTTPHeader.Set("Origin", opts.Origin)
	}
	// The library's dial is bounded by the context; everything after it (reads,
	// writes, the close handshake) is bounded by the options below instead.
	dialCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, target.String(), dialOptions)
	if err != nil {
		return nil, mapDialError(err)
	}
	// The library's read limit is a per-message limit, which is what the cap
	// means: one oversized message is refused before it is buffered, and the
	// connection is torn down rather than left in a half-read state.
	conn.SetReadLimit(int64(opts.MaxFrameBytes))

	ws := &WS{
		conn:    conn,
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
	if err := w.writeMessage(payload); err != nil {
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
	if !closed {
		w.graceful = true
	}
	w.mu.Unlock()
	if closed {
		return nil
	}
	// Best effort: tell the peer this is a normal closure. The library writes
	// the close frame and then waits (bounded internally) for the peer's reply,
	// so it runs off the caller's goroutine — Close itself never waits for a
	// peer that has stopped making progress. teardown below is the idempotent,
	// non-blocking part.
	go func() { _ = w.conn.Close(websocket.StatusNormalClosure, "") }()
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

// teardown fails every pending Send, closes the events channel and releases the
// socket exactly once. reason is what callers waiting on the wire will see.
func (w *WS) teardown(reason error) {
	w.closeOnce.Do(func() {
		if reason == nil {
			reason = fmt.Errorf("%w: websocket closed", contracts.ErrBrowserUnavailable)
		}
		reason = socketFailure(reason)
		w.mu.Lock()
		graceful := w.graceful
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
		if !graceful {
			// Close (the graceful path) owns the socket: it is writing the close
			// frame and then hangs up. Every other path tears it down here,
			// without a handshake, so a reader blocked on a dead peer returns.
			_ = w.conn.CloseNow()
		}
	})
}

// readLoop owns every read from the socket until it fails, the peer closes, or
// the library refuses a message. Control frames (ping, pong, close) never reach
// this loop: the library answers a ping with the same payload, drops a pong,
// and echoes a close before reporting it.
func (w *WS) readLoop() {
	for {
		// No context deadline: reads block until the peer speaks or the socket
		// dies, and teardown closes the socket under every exit path, which is
		// what unblocks this call.
		_, payload, err := w.conn.Read(context.Background())
		if err != nil {
			w.teardown(w.readFailure(err))
			return
		}
		if !w.dispatch(payload) {
			return
		}
	}
}

// readFailure maps a dead read to the transport's errors.
func (w *WS) readFailure(err error) error {
	switch {
	case errors.Is(err, websocket.ErrMessageTooBig):
		// The library refuses the message at the read limit; the cap that
		// matters to the caller is ours, so the error names ours.
		return frameTooLarge(w.opts.MaxFrameBytes)
	case websocket.CloseStatus(err) != -1:
		return fmt.Errorf("%w: peer closed the websocket", contracts.ErrBrowserUnavailable)
	default:
		return socketFailure(err)
	}
}

// writeMessage writes one text message under DialOptions.Timeout, so a stalled
// browser cannot block Send forever.
func (w *WS) writeMessage(payload []byte) error {
	writeCtx, cancel := context.WithTimeout(context.Background(), w.opts.Timeout)
	defer cancel()
	return w.conn.Write(writeCtx, websocket.MessageText, payload)
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

// encodeCommand marshals a CDP command envelope. The session id is a context
// property (WithSession), never a method parameter.
func encodeCommand(id uint64, method string, params any, session string) ([]byte, error) {
	payload, err := json.Marshal(commandRequest{ID: id, Method: method, Params: params, SessionID: session})
	if err != nil {
		return nil, fmt.Errorf("%w: encode %q: %v", ErrProtocol, method, err)
	}
	return payload, nil
}

// parseEndpoint validates a ws:// or wss:// endpoint. The library would accept
// http(s) too (treating them as ws/wss), so the scheme check stays here: a CDP
// endpoint is documented as ws:// or wss:// and a typo must fail loudly.
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

// frameTooLarge is the one size error: it names the cap so a log line explains
// itself.
func frameTooLarge(limit int) error {
	return fmt.Errorf("%w: frame too large: the message exceeds the %d-byte cap", contracts.ErrBrowserUnavailable, limit)
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
func mapDialError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, contracts.ErrBrowserUnavailable) {
		return err
	}
	// Classify by the error itself: a dial that ran out of time carries the
	// deadline in its chain, and the net error says so too. The caller's context
	// is not consulted — by the time a rejection comes back it may simply have
	// expired, and a refused handshake is not a timeout.
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%w: dial: %w", contracts.ErrBrowserTimeout, err)
	}
	return fmt.Errorf("%w: %w", contracts.ErrBrowserUnavailable, err)
}
