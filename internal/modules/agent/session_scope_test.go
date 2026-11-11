package agent

// Tests for the run-scoped session tool wrapper: every tool call carries the
// session of its run, and the wrapper keeps a tool's NoticeSource capability.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sessionCaptureTool records the session stamp of its invocation context.
type sessionCaptureTool struct {
	name string
	seen chan string
}

func (t *sessionCaptureTool) Name() string           { return t.name }
func (t *sessionCaptureTool) Description() string    { return "capture" }
func (t *sessionCaptureTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *sessionCaptureTool) Invoke(ctx context.Context, _ rig.ToolCall) (rig.ToolResult, error) {
	t.seen <- contracts.AgentSessionFromContext(ctx)
	return rig.ToolResult{Content: "ok"}, nil
}

// sessionNoticeTool is a capturing tool with the optional NoticeSource
// capability.
type sessionNoticeTool struct {
	sessionCaptureTool
	notices []string
}

func (t *sessionNoticeTool) PendingNotices() []string { return append([]string(nil), t.notices...) }

func TestWithAgentSessionStampsToolContext(t *testing.T) {
	capture := &sessionCaptureTool{name: "team_task_list", seen: make(chan string, 1)}
	wrapped := withAgentSession([]rig.Tool{capture}, "sess-42")
	if len(wrapped) != 1 {
		t.Fatalf("wrapped tools = %d, want 1", len(wrapped))
	}
	if _, err := wrapped[0].Invoke(context.Background(), rig.ToolCall{Name: capture.name}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got := <-capture.seen; got != "sess-42" {
		t.Fatalf("tool context session = %q, want sess-42", got)
	}
	// The workspace wrapper nests outside the session wrapper; both stamps must
	// reach the tool context.
	capture = &sessionCaptureTool{name: "bash", seen: make(chan string, 1)}
	wrapped = withWorkspaceRoot(withAgentSession([]rig.Tool{capture}, "sess-42"), "/tmp/workspace")
	if _, err := wrapped[0].Invoke(context.Background(), rig.ToolCall{Name: capture.name}); err != nil {
		t.Fatalf("invoke nested wrapper: %v", err)
	}
	if got := <-capture.seen; got != "sess-42" {
		t.Fatalf("nested tool context session = %q, want sess-42", got)
	}
}

func TestWithAgentSessionKeepsNoticeSource(t *testing.T) {
	source := &sessionNoticeTool{
		sessionCaptureTool: sessionCaptureTool{name: "job_start", seen: make(chan string, 1)},
		notices:            []string{"job finished"},
	}
	wrapped := withAgentSession([]rig.Tool{source}, "sess-7")
	noticeSource, ok := wrapped[0].(rig.NoticeSource)
	if !ok {
		t.Fatalf("wrapped tool %T lost NoticeSource", wrapped[0])
	}
	if got := noticeSource.PendingNotices(); len(got) != 1 || got[0] != "job finished" {
		t.Fatalf("PendingNotices = %v, want the inner notices", got)
	}
}

func TestWithAgentSessionEmptySessionIsIdentity(t *testing.T) {
	capture := &sessionCaptureTool{name: "team_task_list", seen: make(chan string, 1)}
	tools := []rig.Tool{capture}
	wrapped := withAgentSession(tools, "")
	if len(wrapped) != 1 || wrapped[0] != tools[0] {
		t.Fatal("empty session must return the tools unchanged")
	}
}

func TestWithAgentSessionNilContext(t *testing.T) {
	// A nil context must not panic (defensive; the reader already tolerates nil).
	ctx := contracts.WithAgentSession(nil, "sess-1")
	if got := contracts.AgentSessionFromContext(ctx); got != "sess-1" {
		t.Fatalf("session from nil-derived context = %q, want sess-1", got)
	}
}
