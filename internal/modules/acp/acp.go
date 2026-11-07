// Package acp implements ZeoKeel's Agent Client Protocol (ACP) backend module:
// a newline-framed JSON-RPC 2.0 server over stdio that drives the product's
// ChatService for editor clients such as Zed.
//
// The ported surface is deliberately small: initialize, session/new,
// session/prompt (streaming agent_message_chunk notifications) and
// session/cancel. Protocol frames are the only bytes written to out;
// diagnostics go through the host logger.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/acp"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// JSONRPCVersion is the only JSON-RPC version the transport speaks.
	JSONRPCVersion = "2.0"
	// ProtocolVersion is the ACP protocol version advertised by initialize.
	ProtocolVersion = 1
	// maxFrameBytes bounds one JSON-RPC frame (8 MiB).
	maxFrameBytes = 8 << 20

	// JSON-RPC 2.0 error codes.
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// ACP method names.
	MethodInitialize    = "initialize"
	MethodSessionNew    = "session/new"
	MethodSessionPrompt = "session/prompt"
	MethodSessionCancel = "session/cancel"
	MethodSessionUpdate = "session/update"

	// ACP stop reasons.
	StopReasonEndTurn   = "end_turn"
	StopReasonCancelled = "cancelled"

	// agentMessageChunk is the only session update kind this server emits.
	UpdateAgentMessageChunk = "agent_message_chunk"
)

// Config is the module's typed assembly config.
type Config struct {
	// TimeoutMs optionally bounds one session/prompt turn (0 = no timeout; the
	// client's cancel/connection lifetime controls it).
	TimeoutMs int `json:"timeoutMs"`
}

// Module is the ACP backend descriptor. It provides contracts.KeyACP.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return fmt.Errorf("%s: require chat service: %w", ModuleID, err)
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		service := newService(cfg, chat, logger)
		if _, err := ctx.Provide(contracts.KeyACP, contracts.ACPService(service)); err != nil {
			return fmt.Errorf("%s: provide acp service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyACP)).
		AddRequires(
			module.Requires(contracts.KeyChat),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel ACP server").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if cfg.TimeoutMs < 0 {
		return fmt.Errorf("%s: timeoutMs must not be negative", ModuleID)
	}
	return nil
}

// service is the contracts.ACPService implementation.
type service struct {
	chat    contracts.ChatService
	logger  contracts.Logger
	timeout time.Duration

	mu              sync.Mutex
	cancelRequested map[string]bool
}

var _ contracts.ACPService = (*service)(nil)

// discardLogger drops ACP diagnostics.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// newService builds the ACP service.
func newService(cfg Config, chat contracts.ChatService, logger contracts.Logger) *service {
	if logger == nil {
		logger = discardLogger{}
	}
	var timeout time.Duration
	if cfg.TimeoutMs > 0 {
		timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	return &service{
		chat:            chat,
		logger:          logger,
		timeout:         timeout,
		cancelRequested: map[string]bool{},
	}
}

// --- JSON-RPC frames -------------------------------------------------------

// RPCError is one JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// request is one decoded incoming frame.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// contentBlock is one ACP prompt content block (text is the only supported
// kind).
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// promptParams is the params of session/prompt. Content is accepted as a
// fallback for clients that use it instead of prompt.
type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
	Content   []contentBlock `json:"content"`
}

// cancelParams is the params of session/cancel.
type cancelParams struct {
	SessionID string `json:"sessionId"`
}

// frameWriter serializes protocol frames from the read loop and prompt
// goroutines.
type frameWriter struct {
	mu     sync.Mutex
	out    io.Writer
	err    error
	closed bool
}

func newFrameWriter(out io.Writer) *frameWriter { return &frameWriter{out: out} }

func (w *frameWriter) write(value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil || w.closed {
		return
	}
	_, w.err = w.out.Write(append(encoded, '\n'))
}

// close makes later frames from lingering callbacks no-ops, so Serve can return
// without racing the caller reading the output stream.
func (w *frameWriter) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

func (w *frameWriter) writeResult(id json.RawMessage, result any) {
	w.write(map[string]any{"jsonrpc": JSONRPCVersion, "id": id, "result": result})
}

func (w *frameWriter) writeError(id json.RawMessage, rpcErr *RPCError) {
	w.write(map[string]any{"jsonrpc": JSONRPCVersion, "id": id, "error": rpcErr})
}

func (w *frameWriter) writeNotification(method string, params any) {
	w.write(map[string]any{"jsonrpc": JSONRPCVersion, "method": method, "params": params})
}

// hasID reports whether a request expects a response.
func hasID(req request) bool {
	return len(req.ID) > 0 && string(bytes.TrimSpace(req.ID)) != "null"
}

// --- Serve loop ------------------------------------------------------------

// Serve implements contracts.ACPService.
func (s *service) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if in == nil || out == nil {
		return errors.New("acp: Serve requires non-nil input and output streams")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := newFrameWriter(out)
	defer writer.close()

	lines := make(chan []byte, 16)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-serveCtx.Done():
				readErr <- nil
				close(lines)
				return
			}
		}
		readErr <- scanner.Err()
		close(lines)
	}()

	var prompts sync.WaitGroup
	var scanErr error
readLoop:
	for {
		select {
		case <-serveCtx.Done():
			cancel()
			prompts.Wait()
			return nil
		case line, ok := <-lines:
			if !ok {
				scanErr = <-readErr
				break readLoop
			}
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 {
				continue
			}
			var req request
			if err := json.Unmarshal(trimmed, &req); err != nil {
				writer.writeError(json.RawMessage("null"),
					&RPCError{Code: CodeParseError, Message: "parse error: " + err.Error()})
				continue
			}
			if req.JSONRPC != JSONRPCVersion {
				if hasID(req) {
					writer.writeError(req.ID, &RPCError{Code: CodeInvalidRequest, Message: `jsonrpc must be "2.0"`})
				}
				continue
			}
			if strings.TrimSpace(req.Method) == "" {
				if hasID(req) {
					writer.writeError(req.ID, &RPCError{Code: CodeInvalidRequest, Message: "missing method"})
				}
				continue
			}
			switch req.Method {
			case MethodInitialize:
				result := map[string]any{
					"protocolVersion": ProtocolVersion,
					"agentCapabilities": map[string]any{
						"promptCapabilities": map[string]any{
							"image":           false,
							"audio":           false,
							"embeddedContext": false,
						},
					},
				}
				if hasID(req) {
					writer.writeResult(req.ID, result)
				}
			case MethodSessionNew:
				if !hasID(req) {
					continue
				}
				sessionID, err := s.chat.NewSession("")
				if err != nil {
					writer.writeError(req.ID, internalError("session/new: "+err.Error()))
					continue
				}
				writer.writeResult(req.ID, map[string]string{"sessionId": sessionID})
			case MethodSessionPrompt:
				if !hasID(req) {
					continue
				}
				prompts.Add(1)
				go func(req request) {
					defer prompts.Done()
					s.handlePrompt(serveCtx, writer, req)
				}(req)
			case MethodSessionCancel:
				rpcErr := s.handleCancel(req.Params)
				if rpcErr != nil {
					if hasID(req) {
						writer.writeError(req.ID, rpcErr)
					}
					continue
				}
				if hasID(req) {
					writer.writeResult(req.ID, struct{}{})
				}
			default:
				if hasID(req) {
					writer.writeError(req.ID, &RPCError{Code: CodeMethodNotFound,
						Message: fmt.Sprintf("method not found: %s", req.Method)})
				}
			}
		}
	}
	// EOF: let in-flight prompts settle (they normally finish on their own),
	// then stop the connection.
	prompts.Wait()
	cancel()
	if scanErr != nil {
		return fmt.Errorf("acp: read protocol stream: %w", scanErr)
	}
	return nil
}

// handlePrompt implements session/prompt: subscribe to chat updates, send the
// prompt, stream new assistant text as agent_message_chunk notifications and
// settle with a stop reason.
func (s *service) handlePrompt(ctx context.Context, writer *frameWriter, req request) {
	var params promptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writer.writeError(req.ID, invalidParams("session/prompt: "+err.Error()))
		return
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		writer.writeError(req.ID, invalidParams("session/prompt: sessionId is required"))
		return
	}
	text := promptText(params)
	if text == "" {
		writer.writeError(req.ID, invalidParams("session/prompt: prompt text is required"))
		return
	}
	s.mu.Lock()
	s.cancelRequested[sessionID] = false
	s.mu.Unlock()

	state := &promptState{signal: make(chan struct{})}
	if snapshot, ok := s.chat.Session(sessionID); ok {
		state.lastText = assistantText(snapshot)
	}
	unsubscribe, err := s.chat.Subscribe(func(changedSession string) {
		if changedSession != sessionID {
			return
		}
		snapshot, ok := s.chat.Session(sessionID)
		if !ok {
			return
		}
		state.process(snapshot, func(delta string) {
			writer.writeNotification(MethodSessionUpdate, map[string]any{
				"sessionId": sessionID,
				"update": map[string]any{
					"sessionUpdate": UpdateAgentMessageChunk,
					"content":       map[string]any{"type": "text", "text": delta},
				},
			})
		})
	})
	if err != nil {
		writer.writeError(req.ID, internalError("session/prompt: subscribe: "+err.Error()))
		return
	}
	defer unsubscribe()
	if err := s.chat.Send(sessionID, text); err != nil {
		writer.writeError(req.ID, internalError("session/prompt: send: "+err.Error()))
		return
	}
	// The fake/agent may have completed synchronously without a notification.
	if snapshot, ok := s.chat.Session(sessionID); ok {
		state.process(snapshot, func(delta string) {
			writer.writeNotification(MethodSessionUpdate, map[string]any{
				"sessionId": sessionID,
				"update": map[string]any{
					"sessionUpdate": UpdateAgentMessageChunk,
					"content":       map[string]any{"type": "text", "text": delta},
				},
			})
		})
	}
	var timeout <-chan time.Time
	if s.timeout > 0 {
		timer := time.NewTimer(s.timeout)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case <-state.signal:
	case <-ctx.Done():
		_ = s.chat.Cancel(sessionID)
		return
	case <-timeout:
		_ = s.chat.Cancel(sessionID)
		writer.writeError(req.ID, internalError("session/prompt: timeout"))
		return
	}
	s.mu.Lock()
	cancelled := s.cancelRequested[sessionID]
	s.mu.Unlock()
	stopReason := StopReasonEndTurn
	if cancelled {
		stopReason = StopReasonCancelled
	}
	writer.writeResult(req.ID, map[string]string{"stopReason": stopReason})
}

// promptState tracks the streamed assistant text of one prompt.
type promptState struct {
	mu       sync.Mutex
	lastText string
	finished bool
	signal   chan struct{}
}

// process emits the new assistant text delta and closes signal when the
// session is no longer running.
func (state *promptState) process(snapshot contracts.ChatSession, emit func(string)) {
	state.mu.Lock()
	if state.finished {
		state.mu.Unlock()
		return
	}
	text := assistantText(snapshot)
	delta := ""
	if len(text) > len(state.lastText) {
		delta = text[len(state.lastText):]
		state.lastText = text
	}
	running := snapshot.Running
	state.mu.Unlock()
	if delta != "" {
		emit(delta)
	}
	if running {
		return
	}
	state.mu.Lock()
	if !state.finished {
		state.finished = true
		close(state.signal)
	}
	state.mu.Unlock()
}

// handleCancel implements session/cancel.
func (s *service) handleCancel(raw json.RawMessage) *RPCError {
	var params cancelParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return invalidParams("session/cancel: " + err.Error())
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return invalidParams("session/cancel: sessionId is required")
	}
	s.mu.Lock()
	s.cancelRequested[sessionID] = true
	s.mu.Unlock()
	_ = s.chat.Cancel(sessionID)
	return nil
}

// promptText concatenates the text blocks of a prompt request.
func promptText(params promptParams) string {
	blocks := params.Prompt
	if len(blocks) == 0 {
		blocks = params.Content
	}
	var builder strings.Builder
	for _, block := range blocks {
		if !strings.EqualFold(strings.TrimSpace(block.Type), "text") {
			continue
		}
		builder.WriteString(block.Text)
	}
	return strings.TrimSpace(builder.String())
}

// assistantText concatenates the assistant message text of a session.
func assistantText(session contracts.ChatSession) string {
	var builder strings.Builder
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleAssistant {
			builder.WriteString(message.Text)
		}
	}
	return builder.String()
}

// invalidParams builds a -32602 error.
func invalidParams(message string) *RPCError {
	return &RPCError{Code: CodeInvalidParams, Message: message}
}

// internalError builds a -32603 error.
func internalError(message string) *RPCError {
	return &RPCError{Code: CodeInternalError, Message: message}
}
