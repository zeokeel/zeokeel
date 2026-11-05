package app

// ACP adapter: implements acp.Agent on top of the headless runtime.
//
// The ACP server (internal/acp) owns the JSON-RPC/stdio transport and knows
// nothing about ZeoKeel; this adapter is the only bridge. It must never write to
// stdout (the transport owns it), never take the desktop single-instance lock,
// and auto-deny approvals (a headless client has no permission UI).
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
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/internal/acp"
	"github.com/zeokeel/zeokeel/internal/state"
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
	return newACPAdapter(rt, cfg.Logger), nil
}

// newACPAdapter wraps a HeadlessRuntime; tests inject a fake runtime.
func newACPAdapter(rt HeadlessRuntime, logger func(format string, args ...any)) *acpAdapter {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &acpAdapter{
		rt:       rt,
		logger:   logger,
		sessions: make(map[string]struct{}),
		active:   make(map[string]bool),
	}
}

type acpAdapter struct {
	rt     HeadlessRuntime
	logger func(format string, args ...any)

	mu       sync.Mutex
	sessions map[string]struct{} // sessions created through this adapter
	active   map[string]bool     // session has an in-flight prompt
	closed   bool
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
	a.mu.Unlock()
	return a.rt.Close(ctx)
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
