// Package acp implements ZeoKeel's Agent Client Protocol (ACP) backend module:
// a newline-framed JSON-RPC 2.0 server over stdio that drives the product's
// ChatService for editor clients such as Zed.
//
// The ported surface is deliberately small: initialize, session/new,
// session/prompt (streaming agent_message_chunk notifications, with image
// prompt blocks forwarded to the chat backend as attachments) and
// session/cancel. Protocol frames are the only bytes written to out;
// diagnostics go through the host logger.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	// maxFrameBytes bounds one JSON-RPC frame (16 MiB): a prompt image at the
	// maxPromptImageBytes limit expands to ~13.3 MiB in base64 plus framing.
	maxFrameBytes = 16 << 20
	// maxPromptImageBytes bounds the decoded size of one prompt image (10 MiB).
	maxPromptImageBytes = 10 << 20

	// JSON-RPC 2.0 error codes.
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// ACP method names.
	MethodInitialize    = "initialize"
	MethodSessionNew    = "session/new"
	MethodSessionList   = "session/list"
	MethodSessionLoad   = "session/load"
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
		// The editor sends its project root as session/new's cwd; without the
		// workspace service the agent's file/shell tools would silently use
		// whatever workspace the desktop last opened (or no root at all).
		if workspace, ok := ctx.Get(contracts.KeyWorkspace); ok && workspace != nil {
			service.workspace = workspace
		}
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

	// workspace is optional: when the product provides it, session/new uses
	// the client's cwd to register/open the project root so the agent's tools
	// run in the editor's project.
	workspace contracts.WorkspaceService

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

// contentBlock is one ACP prompt content block. Text blocks carry Text; image
// blocks carry the base64 Data plus MimeType of ACP's image block shape.
type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
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

// sessionNewParams is the params of session/new. cwd is the editor's project
// root; mcpServers is accepted by the protocol but not consumed yet (configured
// servers come from the shared product settings).
type sessionNewParams struct {
	CWD        string            `json:"cwd"`
	MCPServers []json.RawMessage `json:"mcpServers"`
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
						"loadSession": true,
						"promptCapabilities": map[string]any{
							"image":           true,
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
				workspaceID, rpcErr := s.prepareSessionWorkspace(req.Params)
				if rpcErr != nil {
					writer.writeError(req.ID, rpcErr)
					continue
				}
				sessionID, err := s.chat.NewSession("")
				if err != nil {
					writer.writeError(req.ID, internalError("session/new: "+err.Error()))
					continue
				}
				s.bindSessionWorkspace(sessionID, workspaceID)
				writer.writeResult(req.ID, map[string]string{
					"sessionId":   sessionID,
					"model":       strings.TrimSpace(s.chat.Config().Model),
					"workspaceId": workspaceID,
				})
			case MethodSessionList:
				if !hasID(req) {
					continue
				}
				result, rpcErr := s.handleSessionList(req.Params)
				if rpcErr != nil {
					writer.writeError(req.ID, rpcErr)
					continue
				}
				writer.writeResult(req.ID, result)
			case MethodSessionLoad:
				if !hasID(req) {
					continue
				}
				result, rpcErr := s.handleSessionLoad(req.Params)
				if rpcErr != nil {
					writer.writeError(req.ID, rpcErr)
					continue
				}
				writer.writeResult(req.ID, result)
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

// prepareSessionWorkspace resolves the editor's project root (session/new cwd)
// into a registered workspace and makes it current, so the agent's file/shell
// tools operate on the project the user opened. A missing directory is an
// invalid-params error; a workspace-registry failure only logs (the session can
// still run conversation/read-only tools).
func (s *service) prepareSessionWorkspace(raw json.RawMessage) (string, *RPCError) {
	var params sessionNewParams
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return "", &RPCError{Code: CodeInvalidParams, Message: "session/new: " + err.Error()}
		}
	}
	cwd := strings.TrimSpace(params.CWD)
	if cwd == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(cwd)
	if err != nil {
		return "", &RPCError{Code: CodeInvalidParams, Message: "session/new: invalid cwd: " + err.Error()}
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("session/new: cwd %q is not an existing directory", cwd)}
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	if s.workspace == nil {
		s.logger.Printf("[acp] session/new cwd %q ignored: the product has no workspace service", absolute)
		return "", nil
	}
	ctx := context.Background()
	existing, err := s.workspace.List(ctx)
	if err != nil {
		s.logger.Printf("[acp] session/new cwd %q: list workspaces: %v", absolute, err)
		return "", nil
	}
	for _, workspace := range existing {
		if !sameDirectory(workspace.Root, absolute) {
			continue
		}
		if _, openErr := s.workspace.Open(ctx, workspace.ID); openErr != nil {
			s.logger.Printf("[acp] session/new cwd %q: open workspace %q: %v", absolute, workspace.ID, openErr)
			return "", nil
		}
		return workspace.ID, nil
	}
	workspace, err := s.workspace.Create(ctx, filepath.Base(absolute), absolute)
	if err != nil {
		s.logger.Printf("[acp] session/new cwd %q: register workspace: %v", absolute, err)
		return "", nil
	}
	return workspace.ID, nil
}

// sameDirectory compares two workspace roots after resolving symlinks.
func sameDirectory(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(resolvedLeft) == filepath.Clean(resolvedRight)
}

// bindSessionWorkspace associates the new session with its project root so the
// read model/sidebar knows which project it belongs to (the tools themselves
// follow the current workspace, which prepareSessionWorkspace made current).
func (s *service) bindSessionWorkspace(sessionID, workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || s.chat == nil {
		return
	}
	switcher, ok := s.chat.(contracts.SessionWorkspaceSwitcher)
	if !ok {
		return
	}
	if err := switcher.SetSessionWorkspace(sessionID, workspaceID); err != nil {
		s.logger.Printf("[acp] session %s: bind workspace %q: %v", sessionID, workspaceID, err)
	}
}

// sessionListItem is one session/list entry. The ACP session/list shape is a
// deliberately small read model; updatedAt is omitted for zero timestamps.
type sessionListItem struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// sessionLoadParams is the params of session/load.
type sessionLoadParams struct {
	SessionID string `json:"sessionId"`
}

// sessionLister is the optional read half session/list needs. ChatService
// includes Sessions today, so this assertion mainly documents the graceful
// error path for host implementations that do not.
type sessionLister interface {
	Sessions() []contracts.ChatSessionInfo
}

// handleSessionList implements session/list: newest-updated first, stable for
// zero/equal timestamps, with the chat service's own titles (no localization).
func (s *service) handleSessionList(raw json.RawMessage) (any, *RPCError) {
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		var params map[string]any
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, invalidParams("session/list: " + err.Error())
		}
	}
	lister, ok := s.chat.(sessionLister)
	if !ok {
		return nil, internalError("session/list: chat service does not expose sessions")
	}
	infos := append([]contracts.ChatSessionInfo(nil), lister.Sessions()...)
	sort.SliceStable(infos, func(i, j int) bool {
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})
	entries := make([]sessionListItem, 0, len(infos))
	for _, info := range infos {
		entry := sessionListItem{SessionID: info.ID, Title: info.Title}
		if !info.UpdatedAt.IsZero() {
			entry.UpdatedAt = info.UpdatedAt.Format(time.RFC3339)
		}
		entries = append(entries, entry)
	}
	return map[string]any{"sessions": entries}, nil
}

// handleSessionLoad implements session/load: a read-only conversation replay for
// editor clients. Tool/system messages are skipped; user and assistant text is
// returned in order. It never changes the active session.
func (s *service) handleSessionLoad(raw json.RawMessage) (any, *RPCError) {
	var params sessionLoadParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, invalidParams("session/load: " + err.Error())
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, invalidParams("session/load: sessionId is required")
	}
	session, ok := s.chat.Session(sessionID)
	if !ok {
		return nil, invalidParams(fmt.Sprintf("session/load: unknown session %q", sessionID))
	}
	model := strings.TrimSpace(session.Model)
	if model == "" {
		model = strings.TrimSpace(s.chat.Config().Model)
	}
	messages := make([]map[string]string, 0, len(session.Messages))
	for _, message := range session.Messages {
		if message.Role != contracts.ChatRoleUser && message.Role != contracts.ChatRoleAssistant {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		messages = append(messages, map[string]string{"role": string(message.Role), "text": text})
	}
	return map[string]any{
		"sessionId": session.ID,
		"title":     session.Title,
		"model":     model,
		"messages":  messages,
	}, nil
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
	attachments, rpcErr := promptImages(params)
	if rpcErr != nil {
		writer.writeError(req.ID, rpcErr)
		return
	}
	text := promptText(params)
	if text == "" && len(attachments) == 0 {
		writer.writeError(req.ID, invalidParams("session/prompt: prompt text or image content is required"))
		return
	}
	var attachmentSender contracts.AttachmentSender
	if len(attachments) > 0 {
		sender, ok := s.chat.(contracts.AttachmentSender)
		if !ok {
			writer.writeError(req.ID, internalError(
				"session/prompt: chat backend does not support image attachments"))
			return
		}
		attachmentSender = sender
	}
	s.mu.Lock()
	s.cancelRequested[sessionID] = false
	s.mu.Unlock()

	state := &promptState{
		signal:     make(chan struct{}),
		toolStates: map[string]string{},
		toolIDs:    map[string]string{},
	}
	if snapshot, ok := s.chat.Session(sessionID); ok {
		state.lastText = assistantText(snapshot)
		state.seedToolState(snapshot)
	}
	emitTool := func(event toolEvent) {
		s.writeToolEvent(writer, sessionID, event)
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
		}, emitTool)
	})
	if err != nil {
		writer.writeError(req.ID, internalError("session/prompt: subscribe: "+err.Error()))
		return
	}
	defer unsubscribe()
	var sendErr error
	if len(attachments) == 0 {
		sendErr = s.chat.Send(sessionID, text)
	} else {
		sendErr = attachmentSender.SendWithAttachments(sessionID, text, attachments)
	}
	if sendErr != nil {
		writer.writeError(req.ID, internalError("session/prompt: send: "+sendErr.Error()))
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
		}, emitTool)
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
	if cancelled {
		writer.writeResult(req.ID, map[string]string{"stopReason": StopReasonCancelled})
		return
	}
	// A run failure must not look like a successful empty turn to an ACP
	// client: the chat read model carries the run error, so surface it as a
	// JSON-RPC error (the turn result would otherwise always be end_turn).
	if snapshot, ok := s.chat.Session(sessionID); ok {
		if errText := strings.TrimSpace(snapshot.Error); errText != "" {
			writer.writeError(req.ID, internalError("session/prompt: "+errText))
			return
		}
	}
	writer.writeResult(req.ID, map[string]string{"stopReason": StopReasonEndTurn})
}

// promptState tracks the streamed assistant text of one prompt.
type promptState struct {
	mu       sync.Mutex
	lastText string
	finished bool
	signal   chan struct{}

	// Tool-call streaming state: the last status per tool call (keyed by the
	// stable id or read-model position), the emitted id for empty ids and the
	// generated-id counter.
	toolStates map[string]string
	toolIDs    map[string]string
	generated  int
}

// process emits the new assistant text delta and the new/changed tool-call
// events of one snapshot, then closes signal when the session is no longer
// running. Events are emitted in snapshot order: text first, then tools.
func (state *promptState) process(snapshot contracts.ChatSession, emitText func(string), emitTool func(toolEvent)) {
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
	toolEvents := state.observeToolEventsLocked(snapshot)
	running := snapshot.Running
	state.mu.Unlock()
	if delta != "" {
		emitText(delta)
	}
	if emitTool != nil {
		for _, event := range toolEvents {
			emitTool(event)
		}
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

// promptBlocks returns the block list of a prompt request. Content is accepted
// as a fallback for clients that use it instead of prompt.
func promptBlocks(params promptParams) []contentBlock {
	if len(params.Prompt) > 0 {
		return params.Prompt
	}
	return params.Content
}

// promptImages decodes the image blocks of a prompt request into attachments.
// Each image block must carry an image/* mimeType and a non-empty, decodable
// base64 payload no larger than maxPromptImageBytes; violations are -32602.
func promptImages(params promptParams) ([]contracts.Attachment, *RPCError) {
	var attachments []contracts.Attachment
	for index, block := range promptBlocks(params) {
		if !strings.EqualFold(strings.TrimSpace(block.Type), "image") {
			continue
		}
		position := index + 1
		mimeType := strings.TrimSpace(block.MimeType)
		if mimeType == "" {
			return nil, invalidParams(fmt.Sprintf(
				"session/prompt: image block %d: mimeType is required", position))
		}
		if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
			return nil, invalidParams(fmt.Sprintf(
				"session/prompt: image block %d: mimeType %q must be an image/* type", position, mimeType))
		}
		data := strings.TrimSpace(block.Data)
		if data == "" {
			return nil, invalidParams(fmt.Sprintf(
				"session/prompt: image block %d: data (base64) is required", position))
		}
		decoded, err := decodePromptImage(data)
		if err != nil {
			return nil, invalidParams(fmt.Sprintf(
				"session/prompt: image block %d: invalid base64 data: %v", position, err))
		}
		if len(decoded) == 0 {
			return nil, invalidParams(fmt.Sprintf(
				"session/prompt: image block %d: decoded image data is empty", position))
		}
		if len(decoded) > maxPromptImageBytes {
			return nil, invalidParams(fmt.Sprintf(
				"session/prompt: image block %d: decoded image is %d bytes, over the %d MiB limit",
				position, len(decoded), maxPromptImageBytes>>20))
		}
		ordinal := len(attachments) + 1
		attachments = append(attachments, contracts.Attachment{
			ID:   fmt.Sprintf("acp-image-%d", ordinal),
			Name: fmt.Sprintf("image-%d.%s", ordinal, imageExtension(mimeType)),
			MIME: mimeType,
			Data: decoded,
		})
	}
	return attachments, nil
}

// decodePromptImage decodes ACP image data as standard base64, tolerating the
// unpadded encoding some clients emit.
func decodePromptImage(data string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(data)
}

// imageExtension derives a safe file-name suffix from an image MIME type: the
// subtype without parameters or structured-syntax suffixes ("image/png" ->
// "png", "image/svg+xml" -> "svg"), else "img".
func imageExtension(mimeType string) string {
	subtype := strings.ToLower(strings.TrimSpace(mimeType))
	if slash := strings.IndexByte(subtype, '/'); slash >= 0 {
		subtype = subtype[slash+1:]
	}
	if plus := strings.IndexByte(subtype, '+'); plus >= 0 {
		subtype = subtype[:plus]
	}
	var builder strings.Builder
	for _, char := range subtype {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		}
	}
	if builder.Len() == 0 {
		return "img"
	}
	return builder.String()
}

// promptText concatenates the text blocks of a prompt request.
func promptText(params promptParams) string {
	var builder strings.Builder
	for _, block := range promptBlocks(params) {
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
