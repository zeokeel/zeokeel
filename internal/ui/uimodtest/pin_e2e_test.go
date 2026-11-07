package uimodtest

// Pinned sessions end-to-end: pinning a session through the agent backend
// reorders the sidebar (pinned first within the group) and shows the 置顶 badge.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestPinnedSessionsEndToEnd(t *testing.T) {
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), &memSettings{values: map[string]string{}})
	defer func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	}()
	chat := requireChat(t, root)
	first, err := chat.NewSession("置顶候选会话")
	if err != nil {
		t.Fatalf("NewSession first: %v", err)
	}
	if _, err := chat.NewSession("普通会话"); err != nil {
		t.Fatalf("NewSession second: %v", err)
	}
	pinner, ok := chat.(contracts.SessionPinner)
	if !ok {
		t.Fatal("agent backend does not implement contracts.SessionPinner")
	}
	if err := pinner.SetSessionPinned(first, true); err != nil {
		t.Fatalf("SetSessionPinned: %v", err)
	}
	pinnedInfo := false
	for _, info := range chat.Sessions() {
		if info.ID == first && info.Pinned {
			pinnedInfo = true
		}
	}
	if !pinnedInfo {
		t.Fatalf("read model lacks the pin flag: %+v", chat.Sessions())
	}

	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	hasBadge := func() bool {
		for _, text := range tester.Texts() {
			if text == "置顶" {
				return true
			}
		}
		return false
	}
	if !hasBadge() {
		t.Fatalf("pinned badge missing; texts = %v", tester.Texts())
	}
	pinnedRect, ok := tester.Find("置顶候选会话")
	if !ok {
		t.Fatalf("pinned session row missing; texts = %v", tester.Texts())
	}
	normalRect, ok := tester.Find("普通会话")
	if !ok {
		t.Fatalf("normal session row missing; texts = %v", tester.Texts())
	}
	if pinnedRect.Y >= normalRect.Y {
		t.Fatalf("pinned session is not first: pinned y=%v normal y=%v", pinnedRect.Y, normalRect.Y)
	}

	// Unpinning restores the updated-order (the newest session first).
	if err := pinner.SetSessionPinned(first, false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	tester.Frame()
	if hasBadge() {
		t.Fatalf("badge survived unpin; texts = %v", tester.Texts())
	}
}
