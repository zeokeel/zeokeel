package sidebar_test

// Tests for pinned sessions: pinned-first ordering inside workspace groups and
// search results, the row "置顶" badge and the ⋯ menu's pin/unpin action.

import (
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// pinChat is a fakeChat that also implements contracts.SessionPinner and
// updates the read model so the next frame reflects the new pin state.
type pinChat struct {
	*fakeChat
	mu     sync.Mutex
	pins   []pinCall
	pinErr error
}

type pinCall struct {
	id     string
	pinned bool
}

func newPinChat(sessions ...contracts.ChatSessionInfo) *pinChat {
	return &pinChat{fakeChat: &fakeChat{sessions: sessions}}
}

func (c *pinChat) SetSessionPinned(sessionID string, pinned bool) error {
	c.mu.Lock()
	c.pins = append(c.pins, pinCall{id: sessionID, pinned: pinned})
	err := c.pinErr
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.fakeChat.mu.Lock()
	defer c.fakeChat.mu.Unlock()
	for index := range c.fakeChat.sessions {
		if c.fakeChat.sessions[index].ID == sessionID {
			c.fakeChat.sessions[index].Pinned = pinned
		}
	}
	return nil
}

func (c *pinChat) pinCalls() []pinCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pinCall(nil), c.pins...)
}

// --- tests ------------------------------------------------------------------

func TestPinnedSessionsSortFirst(t *testing.T) {
	chat := newPinChat(
		contracts.ChatSessionInfo{ID: "s1", Title: "普通一"},
		contracts.ChatSessionInfo{ID: "s2", Title: "置顶二", Pinned: true},
		contracts.ChatSessionInfo{ID: "s3", Title: "普通三"},
		contracts.ChatSessionInfo{ID: "s4", Title: "置顶四", Pinned: true},
	)
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	texts := tester.Texts()

	pinnedTwo := textIndex(texts, "置顶二")
	pinnedFour := textIndex(texts, "置顶四")
	plainOne := textIndex(texts, "普通一")
	plainThree := textIndex(texts, "普通三")
	if !(pinnedTwo >= 0 && pinnedTwo < pinnedFour && pinnedFour < plainOne && plainOne < plainThree) {
		t.Fatalf("pinned-first order wrong: 置顶二=%d 置顶四=%d 普通一=%d 普通三=%d; texts = %v",
			pinnedTwo, pinnedFour, plainOne, plainThree, texts)
	}
	// The two pinned rows each carry exactly one 置顶 badge.
	if got := countText(tester, "置顶"); got != 2 {
		t.Fatalf("置顶 badges = %d, want 2; texts = %v", got, tester.Texts())
	}
}

func TestPinnedMenuTogglesState(t *testing.T) {
	chat := newPinChat(
		contracts.ChatSessionInfo{ID: "p1", Title: "Pinned Chat", Pinned: true},
		contracts.ChatSessionInfo{ID: "u1", Title: "Normal Chat"},
	)
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "Pinned Chat", "置顶")

	// The first row is the pinned session: its menu offers 取消置顶.
	openRowMenu(t, tester)
	h.assertTexts(tester, "取消置顶")
	h.assertNoTexts(tester, "置顶会话")
	h.click(tester, "取消置顶")

	calls := chat.pinCalls()
	if len(calls) != 1 || calls[0].id != "p1" || calls[0].pinned {
		t.Fatalf("SetSessionPinned calls = %+v, want p1/false", calls)
	}
	tester.Frame()
	// Both sessions are unpinned now and keep their original order, so the
	// first row is still Pinned Chat and its menu offers 置顶会话.
	openRowMenu(t, tester)
	h.assertTexts(tester, "置顶会话")
	if err := tester.Click("置顶会话"); err != nil {
		t.Fatalf("click 置顶会话: %v", err)
	}
	tester.Frame()
	calls = chat.pinCalls()
	if len(calls) != 2 || calls[1].id != "p1" || !calls[1].pinned {
		t.Fatalf("SetSessionPinned calls = %+v, want second call p1/true", calls)
	}
}

func TestPinnedSortWithinWorkspaceGroups(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := newPinChat(
		contracts.ChatSessionInfo{ID: "s1", Title: "A 普通", WorkspaceID: "w1"},
		contracts.ChatSessionInfo{ID: "s2", Title: "B 置顶", WorkspaceID: "w1", Pinned: true},
		contracts.ChatSessionInfo{ID: "s3", Title: "C 置顶", WorkspaceID: "w2", Pinned: true},
		contracts.ChatSessionInfo{ID: "s4", Title: "D 普通", WorkspaceID: "w1"},
		contracts.ChatSessionInfo{ID: "s5", Title: "E 置顶", Pinned: true},
		contracts.ChatSessionInfo{ID: "s6", Title: "F 普通"},
	)
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()
	texts := tester.Texts()

	// w1 group: pinned B, then the unpinned A/D in their original order.
	b, a, d := textIndex(texts, "B 置顶"), textIndex(texts, "A 普通"), textIndex(texts, "D 普通")
	if !(b >= 0 && b < a && a < d) {
		t.Fatalf("w1 pinned-first order wrong: B=%d A=%d D=%d; texts = %v", b, a, d, texts)
	}
	// The ungrouped group also sorts its pinned session first.
	e, f := textIndex(texts, "E 置顶"), textIndex(texts, "F 普通")
	if !(e >= 0 && e < f) {
		t.Fatalf("ungrouped pinned-first order wrong: E=%d F=%d; texts = %v", e, f, texts)
	}
	// Counts are unchanged: w1 still reports 3 sessions.
	h.assertTexts(tester, "3")
}

func TestPinnedSortAppliesToSearchResults(t *testing.T) {
	chat := newPinChat(
		contracts.ChatSessionInfo{ID: "s1", Title: "Alpha 普通"},
		contracts.ChatSessionInfo{ID: "s2", Title: "Alpha 置顶", Pinned: true},
		contracts.ChatSessionInfo{ID: "s3", Title: "Beta"},
	)
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.click(tester, "搜索会话")
	tester.Type("Alpha")
	tester.Frame()

	texts := tester.Texts()
	pinned, plain := textIndex(texts, "Alpha 置顶"), textIndex(texts, "Alpha 普通")
	if !(pinned >= 0 && pinned < plain) {
		t.Fatalf("search order wrong: pinned=%d plain=%d; texts = %v", pinned, plain, texts)
	}
	h.assertNoTexts(tester, "Beta")
}

func TestPinMenuHiddenWithoutPinner(t *testing.T) {
	// A plain fakeChat does not implement contracts.SessionPinner.
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "会话一"}}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	openRowMenu(t, tester)
	h.assertTexts(tester, "删除")
	h.assertNoTexts(tester, "置顶会话", "取消置顶")
}
