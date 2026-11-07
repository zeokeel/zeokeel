package sidebar_test

// Tests for the row menu's "创建副本" action: it is offered only when the chat
// service also implements contracts.SessionForker, and it forks the row's
// session and makes the copy active.

import (
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// forkChat is a fakeChat that also implements contracts.SessionForker.
type forkChat struct {
	*fakeChat
	mu        sync.Mutex
	forkCalls []string
	forkID    string
	forkErr   error
}

func newForkChat(sessions ...contracts.ChatSessionInfo) *forkChat {
	return &forkChat{
		fakeChat: &fakeChat{sessions: sessions},
		forkID:   "fork-1",
	}
}

func (f *forkChat) ForkSession(sessionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forkCalls = append(f.forkCalls, sessionID)
	if f.forkErr != nil {
		return "", f.forkErr
	}
	return f.forkID, nil
}

func (f *forkChat) forks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.forkCalls...)
}

func TestSidebarForkSessionFromRowMenu(t *testing.T) {
	chat := newForkChat(contracts.ChatSessionInfo{ID: "s1", Title: "会话一"})
	chat.active = "s1"
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	openRowMenu(t, tester)

	h.assertTexts(tester, "创建副本", "删除")
	h.click(tester, "创建副本")

	if got := chat.forks(); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("ForkSession calls = %v, want [s1]", got)
	}
	if got := chat.activeCalls(); len(got) != 1 || got[0] != "fork-1" {
		t.Fatalf("SetActiveSession calls = %v, want [fork-1]", got)
	}
}

func TestSidebarForkHiddenWithoutForker(t *testing.T) {
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "会话一"}}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	openRowMenu(t, tester)
	h.assertTexts(tester, "删除", "设为当前")
	h.assertNoTexts(tester, "创建副本")
}
