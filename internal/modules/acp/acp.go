// Package acp implements ZeoKeel's Agent Client Protocol (ACP) backend module:
// a newline-framed JSON-RPC 2.0 server over stdio that drives the product's
// ChatService for editor clients such as Zed.
//
// The JSON-RPC transport, JSON-RPC method mapping and the prompt streaming
// engine (agent_message_chunk deltas plus tool_call/tool_call_update updates,
// image prompt blocks, cancel) live in the rig acp/server package. This module
// keeps the product pieces: the stdio module descriptor, session/new's cwd ->
// workspace binding, session/list, session/load and the Chat adapter over
// contracts.ChatService.
package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rigacp "github.com/zeokeel/zeokeel-rig/acp"
	rigserver "github.com/zeokeel/zeokeel-rig/acp/server"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/acp"
	// Version is the module's semantic version.
	Version = "1.0.0"

	// maxPromptImageBytes bounds the decoded size of one prompt image (10 MiB).
	// The SDK owns the value and the validation; the alias keeps product tests
	// and call sites reading the same bound.
	maxPromptImageBytes = rigserver.MaxPromptImageBytes

	// Session update kinds, re-exported from the SDK mapping.
	UpdateAgentMessageChunk = rigserver.UpdateAgentMessageChunk
	UpdateToolCall          = rigserver.UpdateToolCall
	UpdateToolCallUpdate    = rigserver.UpdateToolCallUpdate

	// Transport constants re-exported for the module's tests and call sites.
	JSONRPCVersion     = rigacp.JSONRPCVersion
	ProtocolVersion    = rigacp.ProtocolVersion
	CodeParseError     = rigacp.CodeParseError
	CodeInvalidRequest = rigacp.CodeInvalidRequest
	CodeMethodNotFound = rigacp.CodeMethodNotFound
	CodeInvalidParams  = rigacp.CodeInvalidParams
	CodeInternalError  = rigacp.CodeInternalError

	MethodInitialize    = rigacp.MethodInitialize
	MethodSessionNew    = rigacp.MethodSessionNew
	MethodSessionList   = rigacp.MethodSessionList
	MethodSessionLoad   = rigacp.MethodSessionLoad
	MethodSessionPrompt = rigacp.MethodSessionPrompt
	MethodSessionCancel = rigacp.MethodSessionCancel
	MethodSessionUpdate = rigacp.MethodSessionUpdate

	StopReasonEndTurn   = rigacp.StopReasonEndTurn
	StopReasonCancelled = rigacp.StopReasonCancelled
)

// RPCError is the transport's JSON-RPC error type.
type RPCError = rigacp.Error

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
	chat   contracts.ChatService
	logger contracts.Logger
	// workspace is optional: when the product provides it, session/new uses
	// the client's cwd to register/open the project root so the agent's tools
	// run in the editor's project.
	workspace contracts.WorkspaceService
	// mapper is the SDK ACP server: prompt/cancel method mapping and streaming.
	mapper *rigserver.Server
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
	service := &service{chat: chat, logger: logger}
	var timeout time.Duration
	if cfg.TimeoutMs > 0 {
		timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	service.mapper = rigserver.New(
		&chatAdapter{chat: chat},
		rigserver.WithTimeout(timeout),
	)
	return service
}

// Serve runs one ACP connection over in/out (stdio in production). The
// transport drives this module's Handle, which delegates the mapped methods to
// the SDK server and keeps the product-only methods (session/new workspace
// binding, session/list, session/load).
func (s *service) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	transport := rigacp.NewServer(rigacp.Options{
		Handler: s,
		Capabilities: map[string]any{
			"loadSession": true,
			"promptCapabilities": map[string]any{
				"image":           true,
				"audio":           false,
				"embeddedContext": false,
			},
		},
	})
	return transport.Serve(ctx, in, out)
}

// Handle delegates the mapped methods to the SDK server and keeps the
// product-only methods (session/new workspace binding, session/list,
// session/load) here.
func (s *service) Handle(ctx context.Context, method string, params json.RawMessage, emit rigacp.Emit) (any, error) {
	switch method {
	case MethodSessionNew:
		return s.handleSessionNew(params)
	case MethodSessionList:
		return s.handleSessionList(params)
	case MethodSessionLoad:
		return s.handleSessionLoad(params)
	default:
		return s.mapper.Handle(ctx, method, params, emit)
	}
}

// handleSessionNew implements session/new: resolve the editor's project root,
// create the session and bind it to the workspace.
func (s *service) handleSessionNew(raw json.RawMessage) (any, error) {
	workspaceID, rpcErr := s.prepareSessionWorkspace(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	sessionID, err := s.chat.NewSession("")
	if err != nil {
		return nil, internalError("session/new: " + err.Error())
	}
	s.bindSessionWorkspace(sessionID, workspaceID)
	return map[string]string{
		"sessionId":   sessionID,
		"model":       strings.TrimSpace(s.chat.Config().Model),
		"workspaceId": workspaceID,
	}, nil
}

// sessionNewParams is the params of session/new. cwd is the editor's project
// root; mcpServers is accepted by the protocol but not consumed yet (configured
// servers come from the shared product settings).
type sessionNewParams struct {
	CWD        string            `json:"cwd"`
	MCPServers []json.RawMessage `json:"mcpServers"`
}

// prepareSessionWorkspace resolves the editor's project root (session/new cwd)
// into a registered workspace and makes it current, so the agent's file/shell
// tools operate on the project the user opened. A missing directory is an
// invalid-params error; a workspace-registry failure only logs (the session can
// still run conversation/read-only tools).
func (s *service) prepareSessionWorkspace(raw json.RawMessage) (string, error) {
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
func (s *service) handleSessionList(raw json.RawMessage) (any, error) {
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
func (s *service) handleSessionLoad(raw json.RawMessage) (any, error) {
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

// invalidParams builds a -32602 error.
func invalidParams(message string) *RPCError {
	return &RPCError{Code: CodeInvalidParams, Message: message}
}

// internalError builds a -32603 error.
func internalError(message string) *RPCError {
	return &RPCError{Code: CodeInternalError, Message: message}
}
