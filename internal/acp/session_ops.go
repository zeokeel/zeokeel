package acp

// Session persistence and authentication handlers: session/list, session/load
// and authenticate.
//
// The Agent seam is unchanged for backward compatibility: an Agent that can
// list or load persisted sessions opts in by implementing SessionLister /
// SessionLoader. initialize advertises exactly the interfaces the configured
// Agent implements, so the minimum server keeps its old capability shape and a
// client can tell which methods will work.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SessionLister is implemented by an Agent that can enumerate persisted
// sessions (ACP session/list). The result must be most-recently-updated first
// and may honor req.Cwd as an absolute workspace filter.
type SessionLister interface {
	ListSessions(ctx context.Context, req ListSessionsRequest) (ListSessionsResponse, error)
}

// SessionLoader is implemented by an Agent that can restore a persisted session
// (ACP session/load). It must replay the conversation history through updates
// before returning; the server writes a session/update notification for each
// replayed message, in order, before the load response.
type SessionLoader interface {
	LoadSession(ctx context.Context, req LoadSessionRequest, updates UpdateSink) error
}

// authRequired reports whether the server was configured with an authenticator.
func (c *conn) authRequired() bool { return c.srv.authenticate != nil }

// authGateError returns the -32000 error that refuses method while the
// connection is unauthenticated, or nil when method may proceed.
func (c *conn) authGateError(method string) *RPCError {
	if !c.authRequired() || c.authenticated.Load() {
		return nil
	}
	return &RPCError{Code: CodeAuthRequired, Message: method + ": authentication required"}
}

// advertisedAuthMethod reports whether methodID is one of the methods this
// server advertises.
func (c *conn) advertisedAuthMethod(methodID string) bool {
	for _, method := range c.srv.authMethods {
		if method.ID == methodID {
			return true
		}
	}
	return false
}

// handleAuthenticate implements ACP authenticate. With no authenticator
// configured the no-op method always succeeds (old behavior stays valid); with
// one configured, success opens the per-connection auth gate and failure is a
// -32000 error. Unknown method ids are -32602 (never advertised).
func (c *conn) handleAuthenticate(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if len(bytes.TrimSpace(params)) == 0 {
		return nil, invalidParams("authenticate: params are required")
	}
	var p AuthenticateRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams("authenticate: " + err.Error())
	}
	methodID := strings.TrimSpace(p.MethodID)
	if methodID == "" {
		return nil, invalidParams("authenticate: methodId is required")
	}
	if !c.advertisedAuthMethod(methodID) {
		return nil, invalidParams(fmt.Sprintf("authenticate: unknown authentication method %q", methodID))
	}
	if c.srv.authenticate != nil {
		if err := c.srv.authenticate(ctx, methodID); err != nil {
			return nil, &RPCError{Code: CodeAuthRequired, Message: "authenticate: authentication failed: " + err.Error()}
		}
	}
	c.authenticated.Store(true)
	return struct{}{}, nil
}

// handleSessionList implements ACP session/list. An Agent without the
// SessionLister seam reports method-not-found instead of an empty list, so a
// client can trust the initialize capability advertisement.
func (c *conn) handleSessionList(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if !c.init.Load() {
		return nil, &RPCError{Code: CodeInvalidRequest, Message: "session/list before initialize"}
	}
	var p ListSessionsRequest
	if len(bytes.TrimSpace(params)) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, invalidParams("session/list: " + err.Error())
		}
	}
	if cwd := strings.TrimSpace(p.Cwd); cwd != "" {
		if !filepath.IsAbs(cwd) {
			return nil, invalidParams("session/list: cwd must be an absolute workspace path")
		}
		p.Cwd = filepath.Clean(cwd)
	}
	lister, ok := c.srv.agent.(SessionLister)
	if !ok {
		return nil, &RPCError{Code: CodeMethodNotFound, Message: "session/list: not supported by the agent"}
	}
	resp, err := lister.ListSessions(ctx, p)
	if err != nil {
		return nil, rpcErrorFrom(err, "session/list: ")
	}
	if resp.Sessions == nil {
		resp.Sessions = []SessionInfo{}
	}
	return resp, nil
}

// handleSessionLoad implements ACP session/load. The session must not have an
// in-flight prompt; a busy session is an internal error rather than a silent
// second attachment.
func (c *conn) handleSessionLoad(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if !c.init.Load() {
		return nil, &RPCError{Code: CodeInvalidRequest, Message: "session/load before initialize"}
	}
	var p LoadSessionRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams("session/load: " + err.Error())
	}
	sessionID := strings.TrimSpace(p.SessionID)
	if sessionID == "" {
		return nil, invalidParams("session/load: sessionId is required")
	}
	cwd := strings.TrimSpace(p.Cwd)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return nil, invalidParams("session/load: cwd must be an absolute workspace path")
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return nil, invalidParams(fmt.Sprintf("session/load: cwd %q is not an existing directory", cwd))
	}
	p.SessionID = sessionID
	p.Cwd = filepath.Clean(cwd)

	loader, ok := c.srv.agent.(SessionLoader)
	if !ok {
		return nil, &RPCError{Code: CodeMethodNotFound, Message: "session/load: not supported by the agent"}
	}

	c.mu.Lock()
	busy := false
	if st := c.sessions[sessionID]; st != nil && st.busy {
		busy = true
	}
	c.mu.Unlock()
	if busy {
		return nil, internalError("session/load: a prompt is already running for this session")
	}

	updates := func(update SessionUpdateNotification) error {
		if strings.TrimSpace(update.SessionID) == "" {
			update.SessionID = sessionID
		}
		return c.out.writeNotification(MethodSessionUpdate, update)
	}
	if err := loader.LoadSession(ctx, p, updates); err != nil {
		return nil, rpcErrorFrom(err, "session/load: ")
	}
	c.mu.Lock()
	if c.sessions[sessionID] == nil {
		c.sessions[sessionID] = &sessionState{}
	}
	c.mu.Unlock()
	return LoadSessionResponse{}, nil
}

// rpcErrorFrom preserves an Agent's JSON-RPC error (codes such as
// CodeResourceNotFound) and wraps every other failure as an internal error.
func rpcErrorFrom(err error, prefix string) *RPCError {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return internalError(prefix + err.Error())
}
