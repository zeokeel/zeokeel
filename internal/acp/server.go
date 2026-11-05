package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// maxFrameBytes bounds one JSON-RPC frame so a huge line cannot exhaust memory.
const maxFrameBytes = 8 << 20

// Agent is the headless execution seam the ACP server drives. It is
// intentionally small and transport-agnostic so the JSON-RPC loop stays
// testable with a fake, and so internal/app can provide the real
// implementation without importing this package's wire details.
//
// Contract (implemented by the app-side adapter on top of
// internal/app.NewHeadlessRuntime; see doc.go "Adapter wiring"):
//   - NewSession creates one headless agent session bound to req.Cwd (absolute)
//     and returns its stable id; it must not write to stdout.
//   - Prompt runs exactly one prompt for the session and streams semantic
//     updates through updates (serially, before returning); it blocks until the
//     turn reaches a terminal state and maps that state to a stop reason.
//     A second concurrent Prompt for the same session must be rejected.
//   - Cancel stops the in-flight turn of sessionID; unknown ids are a no-op.
//   - Close releases every session and the runtime (idempotent).
type Agent interface {
	NewSession(ctx context.Context, req NewSessionRequest) (NewSessionResponse, error)
	Prompt(ctx context.Context, req PromptRequest, updates UpdateSink) (PromptResponse, error)
	Cancel(ctx context.Context, sessionID string) error
	Close(ctx context.Context) error
}

// UpdateSink delivers one session/update notification. The returned error
// aborts the prompt (the client connection is gone / write failed).
type UpdateSink func(SessionUpdateNotification) error

// Config configures a Server.
type Config struct {
	// Agent is the headless runtime the protocol drives. Required.
	Agent Agent
	// ServerName/Version are advertised in initialize's agentInfo.
	// Defaults: "zeokeel" / "0.1.0".
	ServerName string
	Version    string
	// Logf receives diagnostics (always stderr or a file, never stdout: stdout
	// is reserved for protocol frames). Nil means log.Printf.
	Logf func(format string, args ...any)
}

// Server is the ACP v1 JSON-RPC server over one newline-framed stream pair.
// One connection may serve many sessions concurrently.
type Server struct {
	agent      Agent
	serverName string
	version    string
	logf       func(format string, args ...any)
}

// NewServer validates cfg and returns a server.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Agent == nil {
		return nil, errors.New("acp: NewServer: Config.Agent is required")
	}
	name := cfg.ServerName
	if name == "" {
		name = "zeokeel"
	}
	version := cfg.Version
	if version == "" {
		version = "0.1.0"
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &Server{agent: cfg.Agent, serverName: name, version: version, logf: logf}, nil
}

// Serve runs the connection until in reaches EOF or ctx is cancelled:
//
//   - each newline-terminated line is one JSON-RPC 2.0 frame;
//   - control requests (initialize, session/new, session/cancel) are handled
//     in arrival order so the initialize gate and session state are consistent;
//   - session/prompt is handled concurrently (one goroutine per request) and
//     responses correlate by id, so a slow prompt never blocks other traffic;
//     its session/update notifications are written before its response;
//   - notifications (session/cancel) are fire-and-forget;
//   - stdout (out) carries protocol frames only, diagnostics go to logf.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if in == nil || out == nil {
		return errors.New("acp: Serve requires non-nil input and output streams")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c := &conn{
		srv:      s,
		out:      newFrameWriter(out),
		sessions: make(map[string]*sessionState),
	}

	// Read frames on their own goroutine: a blocking stdio read must not make
	// context cancellation (SIGINT/SIGTERM, editor shutdown) wait for the next
	// client line. The reader goroutine may stay blocked in Read after Serve
	// returns when the caller keeps the stream open; the process that owns the
	// stream is expected to close it (a long-lived library embedding can wrap
	// the reader with its own cancellable stream).
	lines := make(chan []byte, 16)
	readerErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				readerErr <- nil
				close(lines)
				return
			}
		}
		readerErr <- scanner.Err()
		close(lines)
	}()

	var prompts sync.WaitGroup
	var scanErr error
readLoop:
	for {
		select {
		case <-ctx.Done():
			// Connection shutdown: stop agent work before returning so Serve
			// does not leak a running turn.
			c.cancelAllPrompts()
			prompts.Wait()
			return nil
		case line, ok := <-lines:
			if !ok {
				scanErr = <-readerErr
				break readLoop
			}
			req, isReq, isNotif, frameErr := c.decodeFrame(line)
			if frameErr != nil {
				// Malformed JSON: answer a parse error with a null id and keep
				// the connection alive (a bad frame must not kill a long-lived
				// session).
				c.out.writeError(json.RawMessage("null"), &RPCError{Code: CodeParseError, Message: "parse error: " + frameErr.Error()})
				continue
			}
			if isNotif {
				c.handleNotification(ctx, req.Method, req.Params)
				continue
			}
			if !isReq {
				continue
			}
			if req.Method == MethodSessionPrompt {
				// The initialize gate is checked synchronously in frame arrival
				// order, before the handler goroutine exists: a pipelined prompt
				// that arrives before initialize must be rejected even if the
				// goroutine would run after initialize was folded (protocol
				// hardening; verifier-core round-2 finding).
				if !c.init.Load() {
					c.out.writeError(req.ID, &RPCError{Code: CodeInvalidRequest, Message: "session/prompt before initialize"})
					continue
				}
				// Register the per-session prompt slot synchronously (in frame
				// arrival order) before spawning the handler: the first prompt
				// wins and a second one is rejected deterministically instead of
				// by goroutine scheduling.
				if sid, perr := promptSessionID(req.Params); perr == nil {
					if promptCtx, ok := c.beginPrompt(ctx, sid); ok {
						prompts.Add(1)
						go func(req request, promptCtx context.Context, sid string) {
							defer prompts.Done()
							defer c.endPrompt(sid)
							c.handleRequest(promptCtx, req)
						}(req, promptCtx, sid)
						continue
					}
					c.out.writeError(req.ID, internalError("session/prompt: a prompt is already running for this session"))
					continue
				}
				// Malformed params: fall through to the normal dispatch so the
				// client gets the standard -32602 response.
			}
			// Control requests are handled in order so initialize/session state
			// is established before the requests that depend on it.
			c.handleRequest(ctx, req)
		}
	}
	// A closed stream ends the connection: cancel any prompt still running so
	// the agent work stops before Serve returns.
	c.cancelAllPrompts()
	prompts.Wait()
	if scanErr != nil {
		return fmt.Errorf("acp: read protocol stream: %w", scanErr)
	}
	return nil
}

// decodeFrame classifies one raw JSON-RPC line. A nil error means the frame was
// well-formed JSON; isNotif marks notifications (no id) and isReq marks requests
// that need a response. Inbound responses are neither (ignored: the minimum
// server never sends requests to the client).
func (c *conn) decodeFrame(line []byte) (req request, isReq, isNotif bool, err error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return request{}, false, false, nil
	}
	var frame incomingFrame
	if err := json.Unmarshal(line, &frame); err != nil {
		return request{}, false, false, err
	}
	if frame.JSONRPC != JSONRPCVersion {
		if len(frame.ID) > 0 && frame.Method != "" {
			c.out.writeError(frame.ID, &RPCError{Code: CodeInvalidRequest, Message: `jsonrpc must be "2.0"`})
		}
		return request{}, false, false, nil
	}
	if frame.Method == "" {
		return request{}, false, false, nil
	}
	req = request{JSONRPC: frame.JSONRPC, ID: frame.ID, Method: frame.Method, Params: frame.Params}
	if isNotification(req) {
		return req, false, true, nil
	}
	return req, true, false, nil
}

// incomingFrame is one decoded JSON-RPC line before request/notification
// classification.
type incomingFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// isNotification reports whether req is a JSON-RPC notification (no id).
func isNotification(req request) bool {
	return len(req.ID) == 0 || string(bytes.TrimSpace(req.ID)) == "null"
}

// sessionState is the per-session prompt slot: at most one in-flight prompt,
// and its cancel func so session/cancel can abort it.
type sessionState struct {
	busy   bool
	cancel context.CancelFunc
}

// conn is one stdio connection's state.
type conn struct {
	srv  *Server
	out  *frameWriter
	init atomic.Bool

	mu       sync.Mutex
	sessions map[string]*sessionState
}

// handleRequest dispatches one request and writes exactly one response.
func (c *conn) handleRequest(ctx context.Context, req request) {
	result, rpcErr := c.dispatch(ctx, req)
	if rpcErr != nil {
		c.out.writeError(req.ID, rpcErr)
		return
	}
	c.out.writeResult(req.ID, result)
}

// dispatch runs one method handler.
func (c *conn) dispatch(ctx context.Context, req request) (any, *RPCError) {
	switch req.Method {
	case MethodInitialize:
		return c.handleInitialize(req.Params)
	case MethodSessionNew:
		return c.handleNewSession(ctx, req.Params)
	case MethodSessionPrompt:
		return c.handlePrompt(ctx, req.Params)
	case MethodSessionCancel:
		c.handleCancel(ctx, req.Params)
		return struct{}{}, nil
	default:
		return nil, &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + req.Method}
	}
}

// handleNotification runs one notification (fire-and-forget, no response).
func (c *conn) handleNotification(ctx context.Context, method string, params json.RawMessage) {
	switch method {
	case MethodSessionCancel:
		c.handleCancel(ctx, params)
	default:
		// Unknown notifications are ignored per JSON-RPC 2.0.
	}
}

// handleInitialize implements the ACP initialize handshake and opens the
// session/prompt gate.
func (c *conn) handleInitialize(params json.RawMessage) (any, *RPCError) {
	var p InitializeRequest
	if len(bytes.TrimSpace(params)) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, invalidParams("initialize: " + err.Error())
		}
	}
	c.init.Store(true)
	return InitializeResponse{
		ProtocolVersion: ProtocolVersion,
		AgentCapabilities: AgentCapabilities{
			// The minimum automation surface is text-only and stateless across
			// restarts (session/load is a documented non-goal).
			LoadSession:        false,
			PromptCapabilities: PromptCapabilities{},
		},
		AgentInfo: Implementation{Name: c.srv.serverName, Version: c.srv.version},
	}, nil
}

// handleNewSession creates one headless session bound to an absolute cwd.
func (c *conn) handleNewSession(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p NewSessionRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams("session/new: " + err.Error())
	}
	cwd := strings.TrimSpace(p.Cwd)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return nil, invalidParams("session/new: cwd must be an absolute workspace path")
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return nil, invalidParams(fmt.Sprintf("session/new: cwd %q is not an existing directory", cwd))
	}
	p.Cwd = filepath.Clean(cwd)
	resp, err := c.srv.agent.NewSession(ctx, p)
	if err != nil {
		return nil, internalError("session/new: " + err.Error())
	}
	if strings.TrimSpace(resp.SessionID) == "" {
		return nil, internalError("session/new: agent returned an empty session id")
	}
	c.mu.Lock()
	if c.sessions[resp.SessionID] == nil {
		c.sessions[resp.SessionID] = &sessionState{}
	}
	c.mu.Unlock()
	return resp, nil
}

// handlePrompt runs one prompt and streams its updates before the response.
func (c *conn) handlePrompt(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p PromptRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams("session/prompt: " + err.Error())
	}
	if !c.init.Load() {
		return nil, &RPCError{Code: CodeInvalidRequest, Message: "session/prompt before initialize"}
	}
	sessionID := strings.TrimSpace(p.SessionID)
	if sessionID == "" {
		return nil, invalidParams("session/prompt: sessionId is required")
	}
	if len(p.Prompt) == 0 {
		return nil, invalidParams("session/prompt: prompt must contain at least one content block")
	}
	var text strings.Builder
	for _, block := range p.Prompt {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		default:
			return nil, invalidParams(fmt.Sprintf("session/prompt: unsupported content type %q (text only)", block.Type))
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return nil, invalidParams("session/prompt: prompt text is empty")
	}

	// The Serve loop registered this session's prompt slot (and its cancel func)
	// synchronously before starting the handler; ctx is already the per-prompt
	// cancellable context. See beginPrompt/endPrompt.
	promptCtx := ctx

	updates := func(update SessionUpdateNotification) error {
		if strings.TrimSpace(update.SessionID) == "" {
			update.SessionID = sessionID
		}
		return c.out.writeNotification(MethodSessionUpdate, update)
	}
	resp, err := c.srv.agent.Prompt(promptCtx, p, updates)
	if err != nil {
		if promptCtx.Err() != nil || errors.Is(err, context.Canceled) {
			// session/cancel (or connection shutdown) settles the prompt as
			// cancelled rather than as a transport error.
			return PromptResponse{StopReason: StopReasonCancelled}, nil
		}
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) {
			return nil, rpcErr
		}
		return nil, internalError("session/prompt: " + err.Error())
	}
	if strings.TrimSpace(resp.StopReason) == "" {
		resp.StopReason = StopReasonEndTurn
	}
	return resp, nil
}

// handleCancel aborts the in-flight prompt of sessionID (unknown ids are a
// no-op per ACP) and tells the agent so it can stop its own work.
func (c *conn) handleCancel(ctx context.Context, params json.RawMessage) {
	var p CancelNotification
	if len(bytes.TrimSpace(params)) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			c.srv.logf("acp: session/cancel params: %v", err)
			return
		}
	}
	sessionID := strings.TrimSpace(p.SessionID)
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	st := c.sessions[sessionID]
	var cancel context.CancelFunc
	if st != nil {
		cancel = st.cancel
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err := c.srv.agent.Cancel(ctx, sessionID); err != nil {
		c.srv.logf("acp: agent.Cancel(%q): %v", sessionID, err)
	}
}

// cancelAllPrompts cancels every in-flight prompt when the connection ends so
// the agent work stops before Serve returns.
func (c *conn) cancelAllPrompts() {
	c.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(c.sessions))
	for _, st := range c.sessions {
		if st.cancel != nil {
			cancels = append(cancels, st.cancel)
		}
	}
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// promptSessionID extracts the sessionId of a session/prompt request without
// fully validating the prompt blocks (that happens in handlePrompt).
func promptSessionID(params json.RawMessage) (string, error) {
	var p PromptRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return "", err
	}
	sid := strings.TrimSpace(p.SessionID)
	if sid == "" {
		return "", errors.New("empty sessionId")
	}
	return sid, nil
}

// beginPrompt reserves the single in-flight prompt slot of sessionID and
// returns its cancellable context. It is called synchronously by the read loop
// so prompt ordering follows frame arrival, not goroutine scheduling.
func (c *conn) beginPrompt(parent context.Context, sessionID string) (context.Context, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.sessions[sessionID]
	if st == nil {
		st = &sessionState{}
		c.sessions[sessionID] = st
	}
	if st.busy {
		return nil, false
	}
	ctx, cancel := context.WithCancel(parent)
	st.busy = true
	st.cancel = cancel
	return ctx, true
}

// endPrompt releases sessionID's prompt slot.
func (c *conn) endPrompt(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.sessions[sessionID]; st != nil {
		if st.cancel != nil {
			st.cancel()
		}
		st.busy = false
		st.cancel = nil
	}
}

// invalidParams builds an invalid-params JSON-RPC error.
func invalidParams(message string) *RPCError {
	return &RPCError{Code: CodeInvalidParams, Message: message}
}

// internalError builds an internal-error JSON-RPC error.
func internalError(message string) *RPCError {
	return &RPCError{Code: CodeInternalError, Message: message}
}

// frameWriter serializes whole JSON-RPC frames onto one stream. Each frame is
// compiled to a single buffer and written under the mutex, so concurrent
// handlers can never interleave bytes inside a line (stdio framing).
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newFrameWriter(w io.Writer) *frameWriter { return &frameWriter{w: w} }

func (f *frameWriter) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("acp: encode frame: %w", err)
	}
	data = append(data, '\n')
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err = f.w.Write(data)
	return err
}

func (f *frameWriter) writeResult(id json.RawMessage, result any) {
	if err := f.write(response{JSONRPC: JSONRPCVersion, ID: id, Result: result}); err != nil {
		log.Printf("acp: write response: %v", err)
	}
}

func (f *frameWriter) writeError(id json.RawMessage, rpcErr *RPCError) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	if err := f.write(response{JSONRPC: JSONRPCVersion, ID: id, Error: rpcErr}); err != nil {
		log.Printf("acp: write error response: %v", err)
	}
}

func (f *frameWriter) writeNotification(method string, params any) error {
	return f.write(notification{JSONRPC: JSONRPCVersion, Method: method, Params: params})
}
