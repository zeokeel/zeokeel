package uimodtest

// H-series narrow-layout E2E: the conversation header stays readable and the
// goal chip compact when the content column is narrow (1024 + inspector
// drawer), and the very narrow composer (<480 DIP) folds its toolbar into a
// labeled 更多工具 menu instead of an unlabeled » overflow.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// exactLabels counts frame labels equal to text.
func exactLabels(tester *ui.Tester, text string) int {
	count := 0
	for _, label := range tester.Texts() {
		if label == text {
			count++
		}
	}
	return count
}

// containingLabels counts frame labels containing text (a11y labels included).
func containingLabels(tester *ui.Tester, text string) int {
	count := 0
	for _, label := range tester.Texts() {
		if strings.Contains(label, text) {
			count++
		}
	}
	return count
}

// TestCompactHeaderKeepsTitleAndGoalDock covers the B3 header at a narrow
// width: the session title stays in the host's top band and the goal body lives
// in the strip above the composer on every width (no compact/short goal label
// anymore; the goal is not part of the header).
func TestCompactHeaderKeepsTitleAndGoalDock(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester
	goals := requireService(t, product.root, contracts.KeyGoal)
	sessionID := product.chat.ActiveSession()
	goalText := "窄列标题优先与目标紧凑显示的测试目标"
	if _, err := goals.CreateGoal(t.Context(), sessionID, goalText, 0); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	defer func() {
		if goal, ok := goals.Goal(sessionID); ok {
			_, _ = goals.ClearGoal(t.Context(), sessionID, contracts.GoalRef{ID: goal.ID, Revision: goal.Revision})
		}
	}()

	tester.SetSize(704, 720)
	tester.Frame()
	product.uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	tester.Frame()
	tester.Frame()
	if !product.uiRoot.RightPanelOpen() {
		t.Fatalf("right inspector did not open as a drawer at 704x720")
	}
	// The title stays visible in the top band (no compact-only header path).
	if !tester.HasText("键盘测试") {
		t.Fatalf("narrow header dropped the conversation title; texts = %v", tester.Texts())
	}
	// The goal status badge and the goal body live above the composer.
	if !tester.HasText("目标执行中") {
		t.Fatalf("goal strip lacks the status badge; texts = %v", tester.Texts())
	}
	if got := containingLabels(tester, goalText); got < 1 {
		t.Fatalf("goal strip does not show the goal body (%d labels); texts = %v", got, tester.Texts())
	}
	// No second, header-owned goal copy: the body appears once (the dock).
	if got := containingLabels(tester, goalText); got > 1 {
		t.Fatalf("goal body rendered %d times, want once in the composer dock", got)
	}
	// Closing the drawer first: at 704 it overlays the composer dock's right
	// edge, so the ⋯ hit test would land on the drawer backdrop.
	product.uiRoot.SetRightPanelOpen(false)
	tester.Frame()
	// The goal menu is the dock's ⋯ entry, not a header chip.
	if err := tester.Click("目标操作"); err != nil {
		t.Fatalf("open goal actions from the dock: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("清除目标记录") {
		t.Fatalf("goal actions menu lacks the clear entry; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	tester.SetSize(1280, 800)
	tester.Frame()
	tester.Frame()
	if !tester.HasText("键盘测试") || containingLabels(tester, goalText) < 1 {
		t.Fatalf("wide layout lost the title or the goal strip; texts = %v", tester.Texts())
	}
}

// TestVeryNarrowComposerToolsMenu covers the narrow composer after the menu
// simplification: attach/send stay on the row, the model entry is on the
// toolbar (or behind the official overflow), no standalone "/" chip appears,
// and the session menu no longer carries the model/reasoning fallback.
func TestVeryNarrowComposerToolsMenu(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester
	if err := uiRootPushSession(product.uiRoot); err != nil {
		t.Fatalf("push session: %v", err)
	}
	tester.SetSize(704, 720)
	tester.Frame()
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"附件", "发送"} {
		if !tester.HasText(want) {
			t.Fatalf("very narrow composer lacks %q; texts = %v", want, tester.Texts())
		}
	}
	if exactLabels(tester, "/") > 0 {
		t.Fatalf("very narrow composer still shows the standalone / chip; texts = %v", tester.Texts())
	}
	// The model chip is no longer hidden at this width: the simplified session
	// menu made room for it, and the official toolbar may still fold it into
	// its overflow when needed.
	if !tester.HasText("test-model") {
		overflow := "更多"
		if !tester.HasText(overflow) && tester.HasText("More") {
			overflow = "More"
		}
		if err := tester.Click(overflow); err != nil {
			t.Fatalf("model entry is neither on the toolbar nor behind its overflow: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if !tester.HasText("test-mod") {
			t.Fatalf("toolbar overflow lacks the model entry; texts = %v", tester.Texts())
		}
		tester.Key(0, ui.KeyEscape)
		tester.Frame()
	}

	// The attach control opens the native picker directly; the old menu with
	// its manual path entry is gone (P2-12).
	if !tester.HasText("附件") {
		t.Fatalf("very narrow composer is missing the attach control; texts = %v", tester.Texts())
	}
	for _, gone := range []string{"选择文件…", "手动输入路径…"} {
		if tester.HasText(gone) {
			t.Fatalf("old attach menu entry %q is still present; texts = %v", gone, tester.Texts())
		}
	}

	// Typing "/" still opens the command menu even though the chip is hidden.
	if err := tester.Click("输入消息…"); err != nil {
		t.Fatalf("focus composer: %v; texts = %v", err, tester.Texts())
	}
	tester.Type(" /")
	tester.Frame()
	tester.Frame()
	if !tester.HasText("打开设置") && !tester.HasText("新会话") {
		t.Fatalf("typing / in the very narrow composer did not open the command menu; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	tester.SetSize(1280, 800)
	tester.Frame()

	// The wide composer restores the full row (attach + model + session menu + send).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !tester.HasText("工作区修改") {
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if !tester.HasText("工作区修改") {
		t.Fatalf("wide composer did not restore its full toolbar; texts = %v", tester.Texts())
	}
}
