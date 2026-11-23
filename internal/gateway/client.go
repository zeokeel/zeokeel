package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// client is one WebSocket connection: one browser tab.
//
// Every write goes through one outbox so ordering is well defined and so a slow
// client cannot grow the gateway's memory: a domain has at most ONE pending
// delta frame (later deltas are merged into it) and control frames are bounded
// by the client's own outstanding requests. When a merged delta outgrows the op
// budget it degrades to a resync marker (docs/TRANSPORT.md §4.5).
type client struct {
	id   string
	gw   *Gateway
	hub  *hub
	conn *wsConn

	ready atomic.Bool
	// fatal asks the read loop to close the connection after answering the
	// current request (an unsupported protocol version, §4.2). Only the read
	// goroutine touches it.
	fatal bool

	mu       sync.Mutex
	queue    []*outFrame
	byDomain map[string]*outFrame
	signal   chan struct{}

	closed    chan struct{}
	closeOnce sync.Once
}

// outFrame is one queued write: either a pre-encoded control frame (response,
// replay, event) or a domain delta that may still be merged.
type outFrame struct {
	raw    []byte
	delta  deltaPayload
	domain string
}

// newClient wraps one upgraded connection.
func newClient(gw *Gateway, conn *wsConn) *client {
	return &client{
		id:       gw.newID("c"),
		gw:       gw,
		hub:      gw.hub,
		conn:     conn,
		byDomain: make(map[string]*outFrame),
		signal:   make(chan struct{}, 1),
		closed:   make(chan struct{}),
	}
}

// isReady reports whether the client completed the ready handshake.
func (c *client) isReady() bool { return c.ready.Load() }

// enqueueControl queues one already-encoded frame (response, replay frame or
// event) in order.
func (c *client) enqueueControl(payload []byte) {
	c.mu.Lock()
	c.queue = append(c.queue, &outFrame{raw: payload})
	c.mu.Unlock()
	c.poke()
}

// enqueueResponse marshals and queues one JSON-RPC response.
func (c *client) enqueueResponse(response rpcResponse) {
	payload, err := encodeFrame(response)
	if err != nil {
		return
	}
	c.enqueueControl(payload)
}

// enqueueDelta queues one domain delta, merging it into a pending frame for the
// same domain when one is still waiting to be written (§4.5).
func (c *client) enqueueDelta(delta deltaPayload) {
	if delta == nil {
		return
	}
	key := deltaDomainKey(delta)
	c.mu.Lock()
	if frame := c.byDomain[key]; frame != nil && frame.delta != nil {
		frame.delta = frame.delta.merge(delta)
		c.mu.Unlock()
		return
	}
	frame := &outFrame{delta: delta, domain: key}
	c.byDomain[key] = frame
	c.queue = append(c.queue, frame)
	c.mu.Unlock()
	c.poke()
}

// deltaDomainKey maps a delta to the coalescing slot it belongs to.
func deltaDomainKey(delta deltaPayload) string {
	switch typed := delta.(type) {
	case *sessionDelta:
		return typed.SessionID
	case *listDelta:
		return listDomain
	case *askDelta:
		return domainAsk
	case *planDelta:
		return domainPlan
	case *settingsDelta:
		return domainSettings
	case *workspaceDelta:
		return domainWorkspace
	case *itemSetDelta:
		return typed.method
	}
	return "?"
}

// poke wakes the writer.
func (c *client) poke() {
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// take removes and returns every queued frame, clearing the merge index.
func (c *client) take() []*outFrame {
	c.mu.Lock()
	frames := c.queue
	c.queue = nil
	c.byDomain = make(map[string]*outFrame)
	c.mu.Unlock()
	return frames
}

// writeLoop drains the outbox until the connection closes.
func (c *client) writeLoop() {
	for {
		select {
		case <-c.closed:
			return
		case <-c.signal:
		}
		for _, frame := range c.take() {
			payload, err := frame.encode()
			if err != nil {
				continue
			}
			debugf("write %d bytes", len(payload))
			if err := c.conn.writeText(payload); err != nil {
				debugf("write failed: %v", err)
				c.close()
				return
			}
		}
	}
}

// encode renders one frame.
func (f *outFrame) encode() ([]byte, error) {
	if f.raw != nil {
		return f.raw, nil
	}
	if f.delta != nil {
		return encodeNotification(f.delta.notification(), f.delta)
	}
	return nil, nil
}

// readLoop reads client frames, splits them into newline-framed messages and
// dispatches each one. Handlers never block on the network: replies are queued.
func (c *client) readLoop(ctx context.Context) {
	var buffer []byte
	for {
		opcode, payload, err := c.conn.readMessage()
		if err != nil {
			c.close()
			return
		}
		if opcode != wsOpText {
			// The control plane is JSON text only; binary frames are ignored
			// rather than misparsed.
			continue
		}
		debugf("read %d bytes opcode=%d", len(payload), opcode)
		lines, rest, err := splitLines(buffer, payload, MaxFrameBytes)
		if err != nil {
			c.enqueueResponse(rpcResponse{JSONRPC: "2.0", Error: appError(codeBackpressure, "frame_too_large", false)})
			c.close()
			return
		}
		buffer = rest
		for _, line := range lines {
			c.handleLine(ctx, line)
		}
		if c.fatal {
			c.close()
			return
		}
	}
}

// debugf logs protocol detail when ZK_GATEWAY_DEBUG is set (test aid).
func debugf(format string, args ...any) {
	if os.Getenv("ZK_GATEWAY_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "gateway-debug: "+format+"\n", args...)
	}
}

// handleLine parses one JSON-RPC frame and answers it.
func (c *client) handleLine(ctx context.Context, line []byte) {
	var request rpcRequest
	if err := json.Unmarshal(line, &request); err != nil {
		c.enqueueResponse(rpcResponse{JSONRPC: "2.0", Error: errParse("frame is not valid JSON")})
		return
	}
	debugf("frame method=%q id=%s", request.Method, string(request.ID))
	if request.Method == "" {
		c.enqueueResponse(rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: errInvalidRequest("frame has no method")})
		return
	}
	result, protocolErr := c.gw.dispatch(ctx, c, request)
	if request.isNotification() {
		// A notification has no response, by definition (JSON-RPC 2.0 §4.1).
		return
	}
	if c.fatal {
		// The handshake was refused: answer directly, then drop the connection.
		// Nothing else is queued yet (initialize is the first request).
		if protocolErr != nil {
			if payload, err := encodeFrame(rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: protocolErr}); err == nil {
				_ = c.conn.writeText(payload)
			}
		}
		return
	}
	response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
	if protocolErr != nil {
		response.Error = protocolErr
	} else {
		response.Result = result
	}
	c.enqueueResponse(response)
}

// markReady flips the client into delta-streaming mode (§4.2).
func (c *client) markReady() { c.ready.Store(true) }

// waitDrained blocks until the outbox is empty or timeout passes. Shutdown uses
// it so a final gateway.shutdown frame is not cut off by the close.
func (c *client) waitDrained(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		empty := len(c.queue) == 0
		c.mu.Unlock()
		if empty {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// close tears the connection down once: the writer and reader both may reach
// here.
func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		c.gw.clientGone(c)
	})
}
