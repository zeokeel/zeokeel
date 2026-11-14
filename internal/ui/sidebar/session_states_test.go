package sidebar_test

// Frame tests for the session row's live detail: the spinner + state label of
// a run in flight (等待回复 before the model's first output, 运行中 after it),
// the unread mark of a reply that finished in the background, and the same
// state aggregated onto a folded workspace group. The words must reach
// assistive technology through the row's accessible name, not only the glyph.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

func TestSessionRowShowsWaitingAndRunning(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "等待会话", Running: true, Waiting: true},
			{ID: "s2", Title: "运行会话", Running: true},
			{ID: "s3", Title: "空闲会话"},
		},
		active: "s3",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "等待回复", "运行中")
	if _, ok := tester.AccessByRole(ui.RoleListItem, "等待会话，等待回复"); !ok {
		t.Fatalf("waiting row has no accessible state; access = %v", tester.AccessAll())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "运行会话，运行中"); !ok {
		t.Fatalf("running row has no accessible state; access = %v", tester.AccessAll())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "空闲会话"); !ok {
		t.Fatalf("idle row changed its accessible name; access = %v", tester.AccessAll())
	}
}

func TestSessionRowUnreadReply(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "后台完成", Unread: true},
			{ID: "s2", Title: "已看过", Unread: false},
		},
		active: "s2",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	// The unread mark is the accent dot; the accessible name carries the words
	// for assistive technology and type-ahead.
	if _, ok := tester.AccessByRole(ui.RoleListItem, "后台完成，未读回复"); !ok {
		t.Fatalf("unread row has no accessible state; access = %v", tester.AccessAll())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "已看过"); !ok {
		t.Fatalf("read row changed its accessible name; access = %v", tester.AccessAll())
	}
}

func TestCollapsedWorkspaceGroupShowsChildState(t *testing.T) {
	workspace := contracts.Workspace{ID: "w1", Name: "项目一"}
	ws := newFakeWorkspace(workspace).withCurrent(workspace)
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "等待会话", WorkspaceID: "w1", Running: true, Waiting: true},
		},
		active: "s1",
	}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, ws)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "等待回复")

	// Folding the group keeps the child's state on the group row.
	h.click(tester, "项目一")
	h.assertTexts(tester, "项目一", "等待回复")
	if _, ok := tester.AccessByRole(ui.RoleTreeItem, "项目一，等待回复"); !ok {
		t.Fatalf("folded group has no accessible child state; access = %v", tester.AccessAll())
	}
}

func TestCollapsedWorkspaceGroupShowsUnreadChild(t *testing.T) {
	workspace := contracts.Workspace{ID: "w1", Name: "项目一"}
	ws := newFakeWorkspace(workspace).withCurrent(workspace)
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "后台完成", WorkspaceID: "w1", Unread: true},
			{ID: "s2", Title: "已看过", WorkspaceID: "w1"},
		},
		active: "s2",
	}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, ws)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "项目一")

	if _, ok := tester.AccessByRole(ui.RoleTreeItem, "项目一，未读回复"); !ok {
		t.Fatalf("folded group has no accessible unread state; access = %v", tester.AccessAll())
	}
}
