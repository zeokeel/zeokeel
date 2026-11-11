package approval_test

// FR-29: the approval card must say where a request comes from. Several
// sessions (and automation runs) can execute concurrently, so a background
// approval used to render in whatever conversation was open with no
// attribution. These tests pin the session title, the provenance label and the
// "打开该会话" action for requests from another conversation.

import (
	"fmt"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/approval"
)

// approvalChat is a minimal contracts.ChatService for the approval card: it
// knows session titles, the active conversation and records session switches.
type approvalChat struct {
	mu       sync.Mutex
	active   string
	sessions []contracts.ChatSession
	switches []string
}

func newApprovalChat(sessions ...contracts.ChatSession) *approvalChat {
	return &approvalChat{sessions: sessions}
}

func (c *approvalChat) Config() contracts.ChatConfig {
	return contracts.ChatConfig{Model: "test-model"}
}

func (c *approvalChat) Sessions() []contracts.ChatSessionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	infos := make([]contracts.ChatSessionInfo, 0, len(c.sessions))
	for _, session := range c.sessions {
		infos = append(infos, contracts.ChatSessionInfo{ID: session.ID, Title: session.Title})
	}
	return infos
}

func (c *approvalChat) Session(sessionID string) (contracts.ChatSession, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, session := range c.sessions {
		if session.ID == sessionID {
			return session, true
		}
	}
	return contracts.ChatSession{}, false
}

func (c *approvalChat) ActiveSession() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

func (c *approvalChat) SetActiveSession(sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.switches = append(c.switches, sessionID)
	c.active = sessionID
	return nil
}

func (c *approvalChat) NewSession(title string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := fmt.Sprintf("sess-%d", len(c.sessions)+1)
	c.sessions = append(c.sessions, contracts.ChatSession{ID: id, Title: title})
	c.active = id
	return id, nil
}

func (c *approvalChat) DeleteSession(string) error { return nil }
func (c *approvalChat) Send(string, string) error  { return nil }
func (c *approvalChat) Cancel(string) error        { return nil }

func (c *approvalChat) Subscribe(fn func(string)) (func(), error) { return func() {}, nil }

func (c *approvalChat) switchCalls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.switches...)
}

// TestApprovalCardShowsSessionOriginAndSource covers the attribution line: the
// requesting conversation's title plus the provenance (automation) and, for a
// request from another conversation, the action that opens it.
func TestApprovalCardShowsSessionOriginAndSource(t *testing.T) {
	chat := newApprovalChat(
		contracts.ChatSession{ID: "sess-a", Title: "修复登录"},
		contracts.ChatSession{ID: "sess-b", Title: "当前会话"},
	)
	chat.active = "sess-b"
	h := newHarnessWithChat(t, approval.Config{}, chat)
	tester := h.tester()

	h.approvals.add(contracts.ApprovalRequest{
		ID: "ap-1", Tool: "bash", Summary: "rm -rf build",
		SessionID: "sess-a", Source: "automation",
	})
	h.frames(tester, 3)

	h.assertTexts(tester, "来自会话：修复登录", "自动化", "打开该会话")
	h.click(tester, "打开该会话")
	if got := chat.ActiveSession(); got != "sess-a" {
		t.Fatalf("active session = %q, want sess-a (opening the requesting conversation)", got)
	}
	if got := chat.switchCalls(); len(got) != 1 || got[0] != "sess-a" {
		t.Fatalf("SetActiveSession calls = %v, want [sess-a]", got)
	}
}

// TestApprovalCardSameSessionHasNoOpenAction covers the common case: the
// request belongs to the conversation on screen, so the card names it but does
// not offer a pointless switch.
func TestApprovalCardSameSessionHasNoOpenAction(t *testing.T) {
	chat := newApprovalChat(contracts.ChatSession{ID: "sess-a", Title: "修复登录"})
	chat.active = "sess-a"
	h := newHarnessWithChat(t, approval.Config{}, chat)
	tester := h.tester()

	h.approvals.add(contracts.ApprovalRequest{
		ID: "ap-1", Tool: "bash", Summary: "go test ./...",
		SessionID: "sess-a", Source: "user",
	})
	h.frames(tester, 3)

	h.assertTexts(tester, "来自会话：修复登录", "用户")
	h.assertNoTexts(tester, "打开该会话")
}

// TestApprovalCardUnknownSessionFallsBackToID covers a request whose session is
// not in the read model (deleted/not yet hydrated): the card shows the raw ID
// instead of an empty label.
func TestApprovalCardUnknownSessionFallsBackToID(t *testing.T) {
	chat := newApprovalChat(contracts.ChatSession{ID: "sess-b", Title: "当前会话"})
	chat.active = "sess-b"
	h := newHarnessWithChat(t, approval.Config{}, chat)
	tester := h.tester()

	h.approvals.add(contracts.ApprovalRequest{
		ID: "ap-1", Tool: "bash", Summary: "ls", SessionID: "sess-gone",
	})
	h.frames(tester, 3)

	h.assertTexts(tester, "来自会话：sess-gone", "打开该会话")
}

// TestApprovalCardWithoutSessionIDStaysQuiet covers older backends: a request
// without provenance keeps the previous card (no origin row, no action).
func TestApprovalCardWithoutSessionIDStaysQuiet(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()

	h.approvals.add(contracts.ApprovalRequest{ID: "ap-1", Tool: "bash", Summary: "ls"})
	h.frames(tester, 3)

	h.assertTexts(tester, "需要审批", "运行命令")
	h.assertNoTexts(tester, "来自会话", "打开该会话")
}
