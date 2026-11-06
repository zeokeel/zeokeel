package app

// ACP adapter: implements acp.Agent on top of the headless runtime.
//
// The ACP server (internal/acp) owns the JSON-RPC/stdio transport and knows
// nothing about ZeoKeel; this adapter is the only bridge. It must never write to
// stdout (the transport owns it) and never take the desktop single-instance
// lock. Session persistence (session/list, session/load) is projected from the
// existing controller/store API. Approvals are forwarded to the ACP client as
// session/request_permission and mapped onto the controller's ApprovalQueue;
// without a client (direct adapter use) they are denied immediately so a
// prompt can never hang.
//
// Event projection (see internal/acp/doc.go "Adapter wiring"):
//
//	state.Token      -> agent_message_chunk (text)
//	state.Reasoning  -> agent_thought_chunk (text)
//	state.ToolStart  -> tool_call (id/title/rawInput, status pending)
//	state.ToolEnd    -> tool_call_update (completed | failed)
//	state.Usage      -> usage_update (input/output/total tokens)
//	terminal Status  -> PromptResponse.StopReason:
//	                    completed -> end_turn, stopped/cancel -> cancelled,
//	                    failed -> refusal
//
// One prompt per session is enforced here as well (the server also gates it) so
// a misbehaving client gets a clear error instead of interleaved turns.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/acp"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// ACPAgentConfig configures NewACPAdapter.
type ACPAgentConfig struct {
	// DataDir is the ZeoKeel data directory the ACP sessions live in; empty
	// uses DataDir() (ZEOKEEL_DATA_DIR or ~/.zeokeel). A dedicated directory
	// keeps ACP traffic away from the desktop app's data.
	DataDir string
	// Logger receives diagnostics; nil uses log.Printf (stderr / app log).
	Logger func(format string, args ...any)
}

// NewACPAdapter builds the ACP agent bridge over a fresh headless runtime.
// The returned acp.Agent must be Close'd when the transport ends.
func NewACPAdapter(cfg ACPAgentConfig) (acp.Agent, error) {
	rt, err := NewHeadlessRuntime(HeadlessRuntimeConfig{DataDir: cfg.DataDir, Logger: cfg.Logger})
	if err != nil {
		return nil, fmt.Errorf("app: acp adapter: %w", err)
	}
	adapter := newACPAdapter(rt, cfg.Logger)
	adapter.installApprovalBridge()
	return adapter, nil
}

// newACPAdapter wraps a HeadlessRuntime; tests inject a fake runtime.
func newACPAdapter(rt HeadlessRuntime, logger func(format string, args ...any)) *acpAdapter {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	a := &acpAdapter{
		rt:           rt,
		logger:       logger,
		sessions:     make(map[string]struct{}),
		active:       make(map[string]bool),
		permClient:   make(map[string]acp.Client),
		permCtx:      make(map[string]context.Context),
		rejectAlways: make(map[string]map[string]struct{}),
	}
	// Session listing/loading and approval forwarding need the concrete
	// runtime's controller. Only the real headless runtime provides it; a fake
	// runtime keeps the protocol-level behavior (single prompts, auto-deny).
	if hr, ok := rt.(*headlessRuntime); ok && hr != nil {
		a.ctrl = hr.ctrl
	}
	return a
}

// acpAdapter implements acp.Agent plus the optional acp.SessionLister and
// acp.SessionLoader seams (session/list and session/load). It also bridges the
// controller's approval hook to ACP session/request_permission when the prompt
// runs over an ACP connection.
type acpAdapter struct {
	rt     HeadlessRuntime
	ctrl   *session.Controller // set for the headless runtime; nil for fakes
	logger func(format string, args ...any)

	mu       sync.Mutex
	sessions map[string]struct{} // sessions created/loaded through this adapter
	active   map[string]bool     // session has an in-flight prompt
	closed   bool

	// Approval bridge state: the Client seam + turn context of the prompt
	// currently running per session, and the tools rejected "always" per
	// session (the ApprovalQueue only records always-allow rules).
	permClient   map[string]acp.Client
	permCtx      map[string]context.Context
	rejectAlways map[string]map[string]struct{}
}

// NewSession implements acp.Agent.
func (a *acpAdapter) NewSession(ctx context.Context, req acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return acp.NewSessionResponse{}, errors.New("app: acp adapter: closed")
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		return acp.NewSessionResponse{}, errors.New("app: acp adapter: cwd is required")
	}
	if !filepath.IsAbs(cwd) {
		return acp.NewSessionResponse{}, fmt.Errorf("app: acp adapter: cwd %q is not absolute", cwd)
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("app: acp adapter: cwd %q: %w", cwd, err)
	}
	if !info.IsDir() {
		return acp.NewSessionResponse{}, fmt.Errorf("app: acp adapter: cwd %q is not a directory", cwd)
	}
	sessionID, err := a.rt.NewSession(ctx, cwd)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	a.mu.Lock()
	a.sessions[sessionID] = struct{}{}
	a.mu.Unlock()
	return acp.NewSessionResponse{SessionID: sessionID}, nil
}

// Prompt implements acp.Agent: run one turn and stream its semantic events.
func (a *acpAdapter) Prompt(ctx context.Context, req acp.PromptRequest, updates acp.UpdateSink) (acp.PromptResponse, error) {
	if updates == nil {
		return acp.PromptResponse{}, errors.New("app: acp adapter: nil update sink")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return acp.PromptResponse{}, errors.New("app: acp adapter: sessionId is required")
	}
	text, err := promptText(req.Prompt)
	if err != nil {
		return acp.PromptResponse{}, err
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return acp.PromptResponse{}, errors.New("app: acp adapter: closed")
	}
	if _, known := a.sessions[sessionID]; !known {
		a.mu.Unlock()
		return acp.PromptResponse{}, fmt.Errorf("app: acp adapter: unknown session %q", sessionID)
	}
	if a.active[sessionID] {
		a.mu.Unlock()
		return acp.PromptResponse{}, fmt.Errorf("app: acp adapter: session %q already has an in-flight prompt", sessionID)
	}
	a.active[sessionID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.active, sessionID)
		a.mu.Unlock()
	}()

	// While the turn runs, the approval bridge forwards this session's approval
	// requests to the ACP client that owns the prompt. Without a client seam
	// (direct adapter use) the fail-closed auto-deny behavior stays.
	if client, ok := acp.ClientFromContext(ctx); ok {
		a.mu.Lock()
		a.permClient[sessionID] = client
		a.permCtx[sessionID] = ctx
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			delete(a.permClient, sessionID)
			delete(a.permCtx, sessionID)
			a.mu.Unlock()
		}()
	}

	events, unsubscribe, err := a.rt.Subscribe(sessionID)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	defer unsubscribe()

	if err := a.rt.Submit(ctx, sessionID, text); err != nil {
		return acp.PromptResponse{}, err
	}

	stopReason := acp.StopReasonEndTurn
	for {
		select {
		case <-ctx.Done():
			// The client went away (or the server cancelled): stop the turn so it
			// does not keep burning tokens, and settle as cancelled.
			_ = a.rt.Cancel(context.Background(), sessionID)
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		case ev, ok := <-events:
			if !ok {
				return acp.PromptResponse{StopReason: stopReason}, nil
			}
			switch e := ev.(type) {
			case state.Token:
				if err := updates(acp.SessionUpdateNotification{
					SessionID: sessionID,
					Update: acp.SessionUpdate{
						SessionUpdate: acp.UpdateAgentMessageChunk,
						Content:       &acp.ContentBlock{Type: "text", Text: e.Text},
					},
				}); err != nil {
					return acp.PromptResponse{}, err
				}
			case state.Reasoning:
				if err := updates(acp.SessionUpdateNotification{
					SessionID: sessionID,
					Update: acp.SessionUpdate{
						SessionUpdate: acp.UpdateAgentThoughtChunk,
						Content:       &acp.ContentBlock{Type: "text", Text: e.Text},
					},
				}); err != nil {
					return acp.PromptResponse{}, err
				}
			case state.ToolStart:
				if err := updates(acp.SessionUpdateNotification{
					SessionID: sessionID,
					Update: acp.SessionUpdate{
						SessionUpdate: acp.UpdateToolCall,
						ToolCallID:    e.CallID,
						Title:         e.Name,
						Status:        "pending",
						RawInput:      e.Args,
					},
				}); err != nil {
					return acp.PromptResponse{}, err
				}
			case state.ToolEnd:
				status := "completed"
				if e.IsError || e.Cancelled {
					status = "failed"
				}
				if err := updates(acp.SessionUpdateNotification{
					SessionID: sessionID,
					Update: acp.SessionUpdate{
						SessionUpdate: acp.UpdateToolCallUpdate,
						ToolCallID:    e.CallID,
						Status:        status,
					},
				}); err != nil {
					return acp.PromptResponse{}, err
				}
			case state.Usage:
				// Minimum-surface mapping: the ACP v1 usage_update is a context
				// gauge ({used,size}); this adapter exposes the ZeoKeel turn
				// counters (input/output/total) instead and documents that
				// divergence in internal/acp/doc.go. Do not read it as an ACP
				// context-window gauge.
				if err := updates(acp.SessionUpdateNotification{
					SessionID: sessionID,
					Update: acp.SessionUpdate{
						SessionUpdate: acp.UpdateUsage,
						Usage: &acp.Usage{
							InputTokens:  e.In,
							OutputTokens: e.Out,
							TotalTokens:  e.In + e.Out,
						},
					},
				}); err != nil {
					return acp.PromptResponse{}, err
				}
			case state.TurnEnd:
				stopReason = stopReasonFor(e.Reason, "")
			case state.Status:
				if isTerminalACPPhase(e.Phase) {
					return acp.PromptResponse{StopReason: stopReasonFor(e.Reason, e.Phase)}, nil
				}
			case state.Error:
				// The terminal Status (failed) carries the stop reason; keep the
				// detail in the log for the client operator.
				a.logger("zeokeel acp: session %s error: %s", sessionID, errorTextOf(e.Err))
			}
		}
	}
}

// Cancel implements acp.Agent.
func (a *acpAdapter) Cancel(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	return a.rt.Cancel(ctx, sessionID)
}

// Close implements acp.Agent (idempotent).
func (a *acpAdapter) Close(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	clear(a.permClient)
	clear(a.permCtx)
	clear(a.rejectAlways)
	a.mu.Unlock()
	return a.rt.Close(ctx)
}

// ListSessions implements acp.SessionLister: it projects the persisted
// ZeoKeel sessions as ACP session summaries, most recently updated first. The
// optional cwd filter is matched against the workspace root of each session
// (the directory ACP created it with), independent of the controller's current
// workspace, so a fresh process sees every resumable session.
func (a *acpAdapter) ListSessions(ctx context.Context, req acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	ctrl := a.ctrl
	if ctrl == nil || ctrl.Store() == nil {
		return acp.ListSessionsResponse{}, &acp.RPCError{
			Code:    acp.CodeMethodNotFound,
			Message: "session/list: this adapter has no persisted session store",
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return acp.ListSessionsResponse{}, errors.New("app: acp adapter: closed")
	}
	if err := ctx.Err(); err != nil {
		return acp.ListSessionsResponse{}, err
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd != "" && !filepath.IsAbs(cwd) {
		return acp.ListSessionsResponse{}, &acp.RPCError{
			Code:    acp.CodeInvalidParams,
			Message: "session/list: cwd must be an absolute workspace path",
		}
	}
	st := ctrl.Store()
	workspaces, err := st.ListWorkspaces()
	if err != nil {
		return acp.ListSessionsResponse{}, fmt.Errorf("app: acp adapter: session/list: %w", err)
	}
	rootByID := make(map[string]string, len(workspaces))
	for _, ws := range workspaces {
		root := strings.TrimSpace(ws.Root)
		if root == "" {
			continue
		}
		rootByID[ws.ID] = filepath.Clean(root)
	}
	sessions, err := st.ListSessions("") // every workspace; ordered newest first
	if err != nil {
		return acp.ListSessionsResponse{}, fmt.Errorf("app: acp adapter: session/list: %w", err)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	out := make([]acp.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		if sess.Archived {
			continue
		}
		root, ok := rootByID[sess.WorkspaceID]
		if !ok {
			continue
		}
		if cwd != "" && !sameWorkspaceDir(root, cwd) {
			continue
		}
		out = append(out, acp.SessionInfo{
			SessionID: sess.ID,
			Cwd:       root,
			Title:     strings.TrimSpace(sess.Title),
			UpdatedAt: sess.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return acp.ListSessionsResponse{Sessions: out}, nil
}

// LoadSession implements acp.SessionLoader: it validates that the session and
// its workspace exist, replays the persisted conversation as session/update
// notifications, and registers the session so session/prompt works on it.
func (a *acpAdapter) LoadSession(ctx context.Context, req acp.LoadSessionRequest, updates acp.UpdateSink) error {
	if updates == nil {
		return errors.New("app: acp adapter: session/load requires an update sink")
	}
	ctrl := a.ctrl
	if ctrl == nil || ctrl.Store() == nil {
		return &acp.RPCError{
			Code:    acp.CodeMethodNotFound,
			Message: "session/load: this adapter has no persisted session store",
		}
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return &acp.RPCError{Code: acp.CodeInvalidParams, Message: "session/load: sessionId is required"}
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return &acp.RPCError{Code: acp.CodeInvalidParams, Message: "session/load: cwd must be an absolute workspace path"}
	}
	cwd = filepath.Clean(cwd)
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return &acp.RPCError{
			Code:    acp.CodeInvalidParams,
			Message: fmt.Sprintf("session/load: cwd %q is not an existing directory", cwd),
		}
	}

	a.mu.Lock()
	closed, busy := a.closed, a.active[sessionID]
	a.mu.Unlock()
	if closed {
		return errors.New("app: acp adapter: closed")
	}
	if busy {
		return &acp.RPCError{Code: acp.CodeInternalError, Message: "session/load: a prompt is already running for this session"}
	}

	st := ctrl.Store()
	sess, err := st.GetSession(sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return &acp.RPCError{
			Code:    acp.CodeResourceNotFound,
			Message: fmt.Sprintf("session/load: unknown session %q", sessionID),
		}
	}
	if err != nil {
		return fmt.Errorf("app: acp adapter: session/load: %w", err)
	}
	if sess.WorkspaceID != "" {
		ws, werr := st.GetWorkspace(sess.WorkspaceID)
		if werr == nil && ws != nil && strings.TrimSpace(ws.Root) != "" && !sameWorkspaceDir(ws.Root, cwd) {
			return &acp.RPCError{
				Code:    acp.CodeInvalidParams,
				Message: fmt.Sprintf("session/load: cwd %q does not match the session workspace %q", cwd, strings.TrimSpace(ws.Root)),
			}
		}
	}
	history, err := st.ListMessages(sessionID)
	if err != nil {
		return fmt.Errorf("app: acp adapter: session/load: %w", err)
	}
	for _, msg := range history {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		kind := acp.UpdateAgentMessageChunk
		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "user":
			kind = acp.UpdateUserMessageChunk
		case "assistant":
			kind = acp.UpdateAgentMessageChunk
		default:
			// Tool/system rows are not part of the replayed transcript: ACP
			// replays the conversation the user sees, not the runtime trace.
			continue
		}
		if err := updates(acp.SessionUpdateNotification{
			SessionID: sessionID,
			Update: acp.SessionUpdate{
				SessionUpdate: kind,
				Content:       &acp.ContentBlock{Type: "text", Text: msg.Content},
			},
		}); err != nil {
			return err
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app: acp adapter: closed")
	}
	a.sessions[sessionID] = struct{}{}
	return nil
}

// installApprovalBridge replaces the headless runtime's fail-closed auto-deny
// with the ACP session/request_permission round trip. Only the concrete
// headless runtime exposes the controller; other runtimes keep the auto-deny.
func (a *acpAdapter) installApprovalBridge() {
	if a.ctrl == nil {
		return
	}
	a.ctrl.SetOnApprovalRequested(a.onApprovalRequested)
}

// onApprovalRequested forwards one pending approval to the ACP client that owns
// the session's in-flight prompt. Without a client (or after "reject always")
// the call is denied immediately, preserving the historical headless behavior.
func (a *acpAdapter) onApprovalRequested(sessionID string, request state.ApprovalRequest) {
	a.mu.Lock()
	client := a.permClient[sessionID]
	promptCtx := a.permCtx[sessionID]
	alwaysDenied := false
	if denied := a.rejectAlways[sessionID]; denied != nil {
		_, alwaysDenied = denied[request.Name]
	}
	a.mu.Unlock()
	if client == nil || alwaysDenied {
		a.denyApproval(request.CallID)
		return
	}
	if promptCtx == nil {
		promptCtx = context.Background()
	}
	// The controller calls this hook from the turn's goroutine; the round trip
	// happens on its own goroutine so event forwarding is never blocked (the
	// tool itself already blocks in ApprovalQueue.Wait).
	go a.forwardApproval(promptCtx, client, sessionID, request)
}

// forwardApproval asks the client for a permission decision and maps the
// selected option onto the Controller's ApprovalQueue.
func (a *acpAdapter) forwardApproval(ctx context.Context, client acp.Client, sessionID string, request state.ApprovalRequest) {
	resp, err := client.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionID: sessionID,
		ToolCall: acp.ToolCallUpdate{
			ToolCallID: request.CallID,
			Kind:       toolKindForApproval(request.Kind),
			Status:     "pending",
			Title:      request.Name,
			RawInput:   request.Args,
		},
		Options: acp.PermissionOptions(),
	})
	if err != nil {
		a.logger("zeokeel acp: session %s permission request for %s: %v (denying)", sessionID, request.Name, err)
		a.denyApproval(request.CallID)
		return
	}
	switch permissionActionFor(resp.Outcome) {
	case permissionActionAllowOnce:
		a.approveApproval(request.CallID, false)
	case permissionActionAllowAlways:
		a.approveApproval(request.CallID, true)
	case permissionActionRejectAlways:
		a.rejectAlwaysFor(sessionID, request.Name)
		a.denyApproval(request.CallID)
	case permissionActionRejectOnce:
		a.denyApproval(request.CallID)
	default:
		// Cancelled or unknown outcomes fail closed.
		a.logger("zeokeel acp: session %s ignored permission outcome %q for %s (denying)", sessionID, resp.Outcome.OptionID, request.Name)
		a.denyApproval(request.CallID)
	}
}

// permissionAction is the ApprovalQueue decision one client permission outcome
// maps to.
type permissionAction int

const (
	// permissionActionFailClosed denies without recording a rule (cancelled or
	// an outcome the adapter cannot interpret).
	permissionActionFailClosed permissionAction = iota
	// permissionActionAllowOnce approves exactly this call.
	permissionActionAllowOnce
	// permissionActionAllowAlways approves this call and records an in-memory
	// always-allow rule for its tool.
	permissionActionAllowAlways
	// permissionActionRejectOnce denies exactly this call.
	permissionActionRejectOnce
	// permissionActionRejectAlways denies this call and remembers to deny the
	// tool for the rest of the session without asking the client again.
	permissionActionRejectAlways
)

// permissionActionFor maps an ACP session/request_permission outcome onto the
// ApprovalQueue decision the adapter applies. Option ids equal option kinds
// (see acp.PermissionOptions), and anything unrecognized fails closed.
func permissionActionFor(outcome acp.RequestPermissionOutcome) permissionAction {
	if outcome.Outcome == "cancelled" {
		return permissionActionFailClosed
	}
	if outcome.Outcome != "selected" {
		return permissionActionFailClosed
	}
	switch outcome.OptionID {
	case acp.PermissionKindAllowOnce:
		return permissionActionAllowOnce
	case acp.PermissionKindAllowAlways:
		return permissionActionAllowAlways
	case acp.PermissionKindRejectOnce:
		return permissionActionRejectOnce
	case acp.PermissionKindRejectAlways:
		return permissionActionRejectAlways
	default:
		return permissionActionFailClosed
	}
}

// approveApproval resolves the pending approval: allow_always also records the
// in-memory always-allow rule through the controller, exactly like the desktop
// approval UI does.
func (a *acpAdapter) approveApproval(callID string, always bool) {
	ctrl := a.ctrl
	if ctrl == nil {
		return
	}
	if always {
		if ctrl.ApproveAlways(callID) {
			return
		}
	}
	ctrl.Approve(callID)
}

// denyApproval resolves the pending approval as denied.
func (a *acpAdapter) denyApproval(callID string) {
	if ctrl := a.ctrl; ctrl != nil {
		ctrl.Deny(callID)
	}
}

// rejectAlwaysFor remembers that sessionID's toolName must never be asked again
// (ACP reject_always). The ApprovalQueue has only always-allow rules, so the
// adapter keeps the negative rule and denies future requests before the client
// round trip.
func (a *acpAdapter) rejectAlwaysFor(sessionID, toolName string) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rejectAlways[sessionID] == nil {
		a.rejectAlways[sessionID] = make(map[string]struct{})
	}
	a.rejectAlways[sessionID][toolName] = struct{}{}
}

// toolKindForApproval maps a ZeoKeel approval category onto an ACP tool kind.
func toolKindForApproval(kind state.ApprovalKind) string {
	switch kind {
	case state.ApprovalCommand:
		return acp.ToolKindExecute
	case state.ApprovalFileWrite:
		return acp.ToolKindEdit
	case state.ApprovalNetwork:
		return acp.ToolKindFetch
	default:
		return acp.ToolKindOther
	}
}

// sameWorkspaceDir reports whether two paths name the same workspace directory,
// tolerating symlinked spellings of the same directory.
func sameWorkspaceDir(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && resolvedA == resolvedB
}

// promptText flattens the ACP prompt content blocks. The minimum surface is
// text-only: image blocks are rejected explicitly instead of being dropped.
func promptText(blocks []acp.ContentBlock) (string, error) {
	var b strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(block.Text)
		case "image":
			return "", errors.New("app: acp adapter: image prompts are not supported by the minimum ACP surface")
		default:
			return "", fmt.Errorf("app: acp adapter: unsupported prompt content type %q", block.Type)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", errors.New("app: acp adapter: empty prompt")
	}
	return text, nil
}

// stopReasonFor maps a ZeoKeel terminal turn state to an ACP stopReason.
func stopReasonFor(reason state.StatusReason, phase state.Phase) string {
	if phase == state.PhaseFailed || reason == state.ReasonFailed {
		return acp.StopReasonRefusal
	}
	switch reason {
	case state.ReasonStopped, state.ReasonInterrupted:
		return acp.StopReasonCancelled
	default:
		return acp.StopReasonEndTurn
	}
}

// isTerminalACPPhase reports whether a Status event ends the turn.
func isTerminalACPPhase(phase state.Phase) bool {
	return phase == state.PhaseIdle || phase == state.PhaseFailed
}

// errorTextOf renders a state.Error's error without importing extra packages.
func errorTextOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
