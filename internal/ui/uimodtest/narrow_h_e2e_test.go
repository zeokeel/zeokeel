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

// TestCompactHeaderKeepsTitleAndGoalStatus covers the B3 header at a narrow
// width: the session title stays in the host's top band and the goal body lives
// in the strip above the composer on every width (no compact/short goal label
// anymore; the goal is not part of the header).
func TestCompactHeaderKeepsTitleAndGoalStatus(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester
	tracker := requireService(t, product.root, contracts.KeyGoalTracker)
	goalText := "窄列标题优先与目标紧凑显示的测试目标"
	if _, err := tracker.Set(t.Context(), goalText); err != nil {
		t.Fatalf("GoalTracker.Set: %v", err)
	}
	defer tracker.Clear()

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
	if !tester.HasText("目标") {
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
	if !tester.HasText("删除目标记录") {
		t.Fatalf("goal actions menu lacks the delete entry; texts = %v", tester.Texts())
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

// TestVeryNarrowComposerToolsMenu covers N2: at a very narrow composer the
// toolbar keeps one attach menu, the single 会话设置 menu and send; the model
// chip folds into that menu (its 模型/打开模型设置 entries), no unlabeled »
// overflow and no standalone "/" chip appears, and typing "/" still opens the
// command menu.
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
	for _, want := range []string{"附件", "工作区可写", "发送"} {
		if !tester.HasText(want) {
			t.Fatalf("very narrow composer lacks %q; texts = %v", want, tester.Texts())
		}
	}
	if exactLabels(tester, "»") > 0 {
		t.Fatalf("very narrow composer still shows the unlabeled » overflow; texts = %v", tester.Texts())
	}
	if exactLabels(tester, "/") > 0 {
		t.Fatalf("very narrow composer still shows the standalone / chip; texts = %v", tester.Texts())
	}
	// The model chip folds into the session menu at this width.
	if tester.HasText("test-model") || tester.HasText("未配置") {
		t.Fatalf("very narrow composer still shows a model chip; texts = %v", tester.Texts())
	}
	if err := tester.Click("工作区可写"); err != nil {
		t.Fatalf("open session menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"模型：", "打开模型设置…", "权限模式", "对话模式", "推理等级", "插入预设", "打开行为设置…"} {
		if !tester.HasText(want) {
			t.Fatalf("会话设置 menu missing %q; texts = %v", want, tester.Texts())
		}
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	// A single attach menu carries both picker and manual-path entries.
	if err := tester.Click("附件"); err != nil {
		t.Fatalf("open 附件 menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"选择文件…", "手动输入路径…"} {
		if !tester.HasText(want) {
			t.Fatalf("附件 menu missing %q; texts = %v", want, tester.Texts())
		}
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

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
	for time.Now().Before(deadline) && !tester.HasText("工作区可写") {
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if !tester.HasText("工作区可写") {
		t.Fatalf("wide composer did not restore its full toolbar; texts = %v", tester.Texts())
	}
}
