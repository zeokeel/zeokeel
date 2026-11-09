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

// TestCompactHeaderKeepsTitleAndGoalStatus covers N1: with the 1024 window and
// the inspector open as a drawer, the conversation column is narrow, so the
// title keeps a readable width and the goal chip shows only its status label
// (the full goal text stays in the accessibility label/tooltip/popover).
func TestCompactHeaderKeepsTitleAndGoalStatus(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester
	tracker := requireService(t, product.root, contracts.KeyGoalTracker)
	goalText := "窄列标题优先与目标紧凑显示的测试目标"
	if _, err := tracker.Set(t.Context(), goalText); err != nil {
		t.Fatalf("GoalTracker.Set: %v", err)
	}
	defer tracker.Clear()

	// The official modal drawer overlays the content instead of narrowing it
	// (docs/ui/overlays.md), so a compact header is produced by a narrower
	// window, not by the drawer reserve of the old shell.
	tester.SetSize(704, 720)
	tester.Frame()
	product.uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	tester.Frame()
	tester.Frame()
	if !product.uiRoot.RightPanelOpen() {
		t.Fatalf("right inspector did not open as a drawer at 704x720")
	}
	if !tester.HasText("目标") {
		t.Fatalf("compact header lacks the goal status label; texts = %v", tester.Texts())
	}
	// The compact chip's visible text is the status only; the full goal text
	// survives solely in the accessibility label/tooltip.
	if got := containingLabels(tester, goalText); got != 1 {
		t.Fatalf("compact header shows the goal body (%d labels contain it, want 1 a11y label); texts = %v",
			got, tester.Texts())
	}
	// The session title keeps a readable width (min 120 DIP) and stays visible.
	if !tester.HasText("键盘测试") {
		t.Fatalf("compact header dropped the conversation title; texts = %v", tester.Texts())
	}

	// Closing the drawer and widening the window gives the header room again:
	// the chip shows the goal body. The header width comes from the previous
	// frame, so let it re-measure.
	product.uiRoot.SetRightPanelOpen(false)
	tester.SetSize(1280, 800)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && containingLabels(tester, goalText) < 2 {
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if got := containingLabels(tester, goalText); got < 2 {
		t.Fatalf("wide header does not show the goal body again (%d labels); texts = %v", got, tester.Texts())
	}
	tester.SetSize(1280, 800)
	tester.Frame()
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
	for _, want := range []string{"模型：", "打开模型设置…", "权限模式", "对话模式", "思考强度", "插入预设", "打开行为设置…"} {
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
