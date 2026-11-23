package sidebar_test

// Frame tests for the sidebar's audit fixes (2026-11 wave D):
//
//   - UD-16: the sidebar error row is the shared uikit.ErrorLine (RoleAlert,
//     selectable, TypeXS), not a hand-built Row+Icon+11px Text a screen reader
//     never announces as an alert.
//   - UX-10: fork/pin/archive/delete failures are reported (ErrorLine), and a
//     failed delete keeps its confirmation dialog open instead of closing it
//     and leaving the session in place (a false success).
//   - UD-08: the workspace row's ⋯ hit target is ≥24 DIP (AP-17), as the
//     session ⋯ and IconButton already are.
//   - UD-15: the workspace empty state converges on uikit.EmptyState (the
//     hint line is no longer an 11 DIP TypeXXXS sentence).

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// alertContaining reports whether the frame paints the given text inside a
// role-alert region (uikit.ErrorLine's anatomy: a RoleAlert live region holding
// the selectable message). The alert row's own accessible name is empty; the
// message is its child text, so the test checks the text's rect lies within an
// alert's bounds.
func alertContaining(tester *ui.Tester, substr string) bool {
	var alerts []ui.Rect
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleAlert {
			alerts = append(alerts, info.Bounds)
		}
	}
	if len(alerts) == 0 {
		return false
	}
	for _, info := range tester.AccessAll() {
		if !strings.Contains(info.Name, substr) {
			continue
		}
		r := info.Bounds
		for _, b := range alerts {
			if r.X >= b.X && r.Y >= b.Y && r.X+r.W <= b.X+b.W && r.Y+r.H <= b.Y+b.H {
				return true
			}
		}
	}
	return false
}

// deleteErrChat is a fakeChat whose DeleteSession fails, for the UX-10
// false-success probe.
type deleteErrChat struct {
	*fakeChat
	mu      sync.Mutex
	err     error
	attempt []string
}

func (c *deleteErrChat) DeleteSession(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempt = append(c.attempt, id)
	return c.err
}

// TestSidebarForkFailureShowsFeedback covers UX-10 + UD-16: a failed fork
// reports a role-alert error line instead of silently doing nothing.
func TestSidebarForkFailureShowsFeedback(t *testing.T) {
	chat := newForkChat(contracts.ChatSessionInfo{ID: "s1", Title: "会话一"})
	chat.active = "s1"
	chat.forkErr = errors.New("store is locked")
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	openRowMenu(t, tester, "会话一")
	h.click(tester, "创建副本")

	if !alertContaining(tester, "创建副本失败") {
		t.Fatalf("UX-10/UD-16: a failed fork shows no role-alert feedback; texts = %v", tester.Texts())
	}
	if !alertContaining(tester, "store is locked") {
		t.Fatalf("the fork error detail is missing; texts = %v", tester.Texts())
	}
	if got := chat.forks(); len(got) != 1 {
		t.Fatalf("fork attempts = %v, want one", got)
	}
}

// TestSidebarPinFailureShowsFeedback covers UX-10: a failed pin reports an
// error line.
func TestSidebarPinFailureShowsFeedback(t *testing.T) {
	chat := newPinChat(contracts.ChatSessionInfo{ID: "s1", Title: "会话一"})
	chat.active = "s1"
	chat.pinErr = errors.New("store is locked")
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	openRowMenu(t, tester, "会话一")
	h.click(tester, "置顶会话")

	if !alertContaining(tester, "store is locked") {
		t.Fatalf("UX-10: a failed pin shows no role-alert feedback; texts = %v", tester.Texts())
	}
}

// TestSidebarArchiveFailureShowsFeedback covers UX-10: a failed archive
// reports an error line.
func TestSidebarArchiveFailureShowsFeedback(t *testing.T) {
	chat := newArchiveChat(contracts.ChatSessionInfo{ID: "s1", Title: "会话一"})
	chat.active = "s1"
	chat.archiveErr = errors.New("store is locked")
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	openRowMenu(t, tester, "会话一")
	h.click(tester, "归档会话")

	if !alertContaining(tester, "store is locked") {
		t.Fatalf("UX-10: a failed archive shows no role-alert feedback; texts = %v", tester.Texts())
	}
}

// TestSidebarDeleteFailureKeepsDialog covers UX-10: a failed delete keeps the
// confirmation dialog open and shows the error, instead of closing the dialog
// and leaving the session in place (a false success).
func TestSidebarDeleteFailureKeepsDialog(t *testing.T) {
	base := &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "要删除的会话"}},
		active:   "s1",
	}
	chat := &deleteErrChat{fakeChat: base, err: errors.New("store is locked")}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	openRowMenu(t, tester, "要删除的会话")
	h.click(tester, "删除") // menu item → opens the confirmation
	h.assertTexts(tester, "删除会话", "取消")
	h.click(tester, "删除") // danger confirm → DeleteSession fails

	// The dialog stays open (or reopens) and names the failure; the session is
	// still listed (nothing was deleted).
	h.assertTexts(tester, "删除会话", "要删除的会话")
	if !alertContaining(tester, "store is locked") {
		t.Fatalf("UX-10: a failed delete shows no role-alert feedback; texts = %v", tester.Texts())
	}
	if len(chat.attempt) != 1 {
		t.Fatalf("delete attempts = %v, want one", chat.attempt)
	}
}

// TestSidebarWorkspaceMoreHitTarget covers UD-08: the switch dialog's workspace
// row ⋯ button has a ≥24 DIP hit target on both axes (AP-17).
func TestSidebarWorkspaceMoreHitTarget(t *testing.T) {
	workspace := newFakeWorkspace(contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"}).
		withCurrent(contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := h.tester()
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("open the switch dialog: %v", err)
	}
	tester.Frame()
	tester.Frame()
	// The ⋯ action is hover-revealed (Opacity 0 + IgnoreInput at rest), so it
	// is found by its label rect rather than the access tree; the hit target
	// must still be ≥24 DIP on both axes.
	rect, ok := tester.Find("工作区操作：项目一")
	if !ok {
		t.Fatalf("workspace ⋯ action missing; texts = %v", tester.Texts())
	}
	if rect.W < 24 || rect.H < 24 {
		t.Fatalf("UD-08: workspace ⋯ hit target = %.1f×%.1f, want ≥24×24", rect.W, rect.H)
	}
}

// TestSidebarWorkspaceEmptyHintNotMicroType covers UD-15 for the switch
// dialog's empty registry state: the line renders on the EmptyState metadata
// step (≥18 DIP), not as an 11 DIP TypeXXXS sentence.
func TestSidebarWorkspaceEmptyHintNotMicroType(t *testing.T) {
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, newFakeWorkspace())
	tester := h.tester()
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("open the switch dialog: %v", err)
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "还没有工作文件夹")
	hint, ok := tester.Find("还没有工作文件夹")
	if !ok {
		t.Fatalf("workspace empty hint missing; texts = %v", tester.Texts())
	}
	if hint.H < 18 {
		t.Fatalf("UD-15: the empty-state line is %.1f DIP tall, want the ≥18 DIP EmptyState line (not an 11 DIP TypeXXXS sentence)",
			hint.H)
	}
}
