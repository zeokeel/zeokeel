package uimodtest

// E2E frame tests for the interaction fixes: keyboard paths (command palette,
// dialogs), the scrollable settings surface, the narrow composer, native
// toasts and the sidebar collapse/footer. These drive the real desktop assembly
// through mygo's in-memory Tester, exactly like the other uimodtest files.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// interactionProduct is one booted desktop product with a live tester and an
// active conversation, for the interaction frame tests.
type interactionProduct struct {
	root   cordis.Context
	uiRoot *uiroot.Root
	tester *ui.Tester
	loader loaderCloser
	chat   contracts.ChatService
}

// newInteractionProduct boots the desktop assembly with a scripted chat server
// and opens a fresh conversation.
func newInteractionProduct(t *testing.T) *interactionProduct {
	t.Helper()
	server := fakeChatServer(t)
	t.Cleanup(server.Close)
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), settings)
	t.Cleanup(func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	})
	chat := requireChat(t, root)
	if _, err := chat.NewSession("键盘测试"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	tester.Frame()
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester.Frame()
	return &interactionProduct{root: root, uiRoot: uiRoot, tester: tester, loader: loader, chat: chat}
}

// requireService resolves one kernel service for the test.
func requireService[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T]) T {
	t.Helper()
	value, err := ctx.Require(key)
	if err != nil {
		t.Fatalf("require %s: %v", key.Name(), err)
	}
	return value
}

// frameUntil renders frames until want appears (or the timeout expires).
func frameUntil(tester *ui.Tester, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tester.Frame()
		if tester.HasText(want) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// frameUntilGone renders frames until want disappears.
func frameUntilGone(tester *ui.Tester, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tester.Frame()
		if !tester.HasText(want) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// rectInside reports whether inner is fully inside outer (1 DIP tolerance).
func rectInside(outer, inner ui.Rect) bool {
	const tolerance = 1
	return inner.X >= outer.X-tolerance && inner.Y >= outer.Y-tolerance &&
		inner.X+inner.W <= outer.X+outer.W+tolerance &&
		inner.Y+inner.H <= outer.Y+outer.H+tolerance
}

// windowHasRect reports whether the rect is inside a width×height window.
func windowHasRect(width, height int, rect ui.Rect) bool {
	return rect.X >= 0 && rect.Y >= 0 &&
		rect.X+rect.W <= float32(width) && rect.Y+rect.H <= float32(height)
}

// settingsDialogBounds mirrors the host's adaptive settings modal geometry
// (internal/uiroot/frame.go settings()).
func settingsDialogBounds(width, height int) ui.Rect {
	panelW, panelH := design.SettingsWidth, design.SettingsHeight
	limitW := float32(width) - 2*design.SettingsMargin
	limitH := float32(height) - 2*design.SettingsMargin
	if limitW > 0 && panelW > limitW {
		panelW = limitW
	}
	if limitH > 0 && panelH > limitH {
		panelH = limitH
	}
	if panelW < 640 {
		panelW = 640
	}
	if panelH < 480 {
		panelH = 480
	}
	return ui.Rect{X: (float32(width) - panelW) / 2, Y: (float32(height) - panelH) / 2, W: panelW, H: panelH}
}

// TestPaletteKeyboardPathEndToEnd covers Cmd+K: filter with typing, move the
// choice with Down, run it with Enter and close with Escape.
func TestPaletteKeyboardPathEndToEnd(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester

	// Cmd+K opens the palette; Escape closes it.
	tester.Key(ui.Cmd, ui.KeyK)
	if !frameUntil(tester, "命令面板", 2*time.Second) {
		t.Fatalf("command palette did not open; texts = %v", tester.Texts())
	}
	if !tester.HasText("搜索命令") {
		t.Fatalf("palette missing 搜索命令; texts = %v", tester.Texts())
	}
	// B-spec §3.1.4: the footer is a key legend — the words sit beside their
	// key caps on one 32 DIP row — not a "·"-joined sentence. Assert each word
	// and each cap individually. The caps carry the legend's own accessible name
	// ("图例：Esc"), so the Esc cap is found by that name and never has to be told
	// apart from a command row whose name or hint mentions Esc.
	words := []string{"选择", "图例：↑ ↓", "运行", "图例：↵", "关闭", "图例：Esc"}
	rects := make([]ui.Rect, 0, len(words))
	for _, want := range words {
		rect, ok := tester.Find(want)
		if !ok {
			t.Fatalf("palette key legend missing %q; texts = %v", want, tester.Texts())
		}
		rects = append(rects, rect)
	}
	for i := 1; i < len(rects); i++ {
		if rects[i].X <= rects[i-1].X {
			t.Fatalf("palette legend %q is not to the right of %q: x=%.1f vs %.1f",
				words[i], words[i-1], rects[i].X, rects[i-1].X)
		}
		if dy := rects[i].Y - rects[0].Y; dy < -4 || dy > 4 {
			t.Fatalf("palette legend %q is not on the same row as 选择: y=%.1f vs %.1f",
				words[i], rects[i].Y, rects[0].Y)
		}
	}
	for _, gone := range []string{"↑↓ 选择", "Enter 运行", "Esc 关闭"} {
		if tester.HasText(gone) {
			t.Fatalf("palette still prints the superseded joined hint %q; texts = %v", gone, tester.Texts())
		}
	}
	// Escape closes the palette (the promise the legend makes).
	tester.Key(0, ui.KeyEscape)
	if !frameUntilGone(tester, "命令面板", 2*time.Second) {
		t.Fatalf("Escape did not close the palette; texts = %v", tester.Texts())
	}

	// The Esc cap is checked on a filtered palette: a command row may carry the
	// exact text "Esc" as its own hint, so the check types a query whose rows
	// carry no such hint (新 …), where Find("Esc") resolves to the legend's cap
	// and the legend row is measured against it.
	tester.Key(ui.Cmd, ui.KeyK)
	if !frameUntil(tester, "命令面板", 2*time.Second) {
		t.Fatalf("palette did not reopen for the legend check")
	}
	tester.Type("新")
	tester.Frame()
	if !tester.HasText("新会话") {
		t.Fatalf("filter 新 did not show 新会话; texts = %v", tester.Texts())
	}
	escCap, okEsc := tester.Find("图例：Esc")
	if !okEsc {
		t.Fatalf("palette key legend missing its Esc cap; texts = %v", tester.Texts())
	}
	legendStart, okStart := tester.Find("选择")
	closeWord, okClose := tester.Find("关闭")
	if !okStart || !okClose {
		t.Fatalf("palette key legend missing 选择/关闭; texts = %v", tester.Texts())
	}
	if dy := escCap.Y - legendStart.Y; dy < -4 || dy > 4 {
		t.Fatalf("palette legend Esc cap is not on the legend row: y=%.1f vs %.1f",
			escCap.Y, legendStart.Y)
	}
	if escCap.X <= closeWord.X {
		t.Fatalf("palette legend Esc cap (x=%.1f) is not to the right of 关闭 (x=%.1f)",
			escCap.X, closeWord.X)
	}
	// The legend's y is NOT compared with the first frame: the palette panel
	// sizes to its result list, so the footer moves with the filter. Only the
	// within-frame relations above are the contract.
	// Close the filtered palette so the rest of the test starts from an empty
	// query (closePalette resets it).
	tester.Key(0, ui.KeyEscape)
	if !frameUntilGone(tester, "命令面板", 2*time.Second) {
		t.Fatalf("Escape did not close the filtered palette; texts = %v", tester.Texts())
	}

	// Typing filters the command list.
	tester.Key(ui.Cmd, ui.KeyK)
	if !frameUntil(tester, "命令面板", 2*time.Second) {
		t.Fatalf("command palette did not reopen")
	}
	tester.Type("设置")
	tester.Frame()
	if !tester.HasText("设置") {
		t.Fatalf("typing did not filter to the settings command; texts = %v", tester.Texts())
	}
	if tester.HasText("新会话") {
		t.Fatalf("typing「设置」did not filter out「新会话」; texts = %v", tester.Texts())
	}

	// Down moves the choice. The palette de-duplicates commands by title, so
	// filtering「设置」leaves two rows: 设置 and 设置目标; one Down chooses the
	// goal command, whose effect is distinguishable.
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	if !frameUntil(tester, "目标内容", 3*time.Second) {
		t.Fatalf("Down+Enter did not run the second command (goal dialog expected); texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	if !frameUntilGone(tester, "目标内容", 2*time.Second) {
		t.Fatalf("Escape did not close the goal dialog; texts = %v", tester.Texts())
	}

	// Tab moves the focus from the search field into the list; Enter then runs
	// the row chosen there (the palette's documented keyboard path).
	before := len(product.chat.Sessions())
	tester.Key(ui.Cmd, ui.KeyK)
	if !frameUntil(tester, "命令面板", 2*time.Second) {
		t.Fatalf("command palette did not reopen for the Tab path")
	}
	tester.Type("新")
	tester.Frame()
	if !tester.HasText("新会话") {
		t.Fatalf("typing「新」did not show「新会话」; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	if !frameUntilGone(tester, "命令面板", 2*time.Second) {
		t.Fatalf("Tab+Enter did not close (run) the palette; texts = %v", tester.Texts())
	}
	if got := len(product.chat.Sessions()); got <= before {
		t.Fatalf("Tab+Enter did not run「新会话」(sessions %d → %d); texts = %v", before, got, tester.Texts())
	}
}

// TestSettingsFixedFooterAndClose covers the H-series settings shell: the
// page's primary action sits in a fixed footer (visible without scrolling and
// inside the dialog), the close control works, and a clean page closes on
// Escape.
func TestSettingsFixedFooterAndClose(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester

	if err := product.uiRoot.OpenSettingsPage("appearance"); err != nil {
		t.Fatalf("OpenSettingsPage(appearance): %v", err)
	}
	if !frameUntil(tester, "关闭设置", 2*time.Second) {
		t.Fatalf("settings did not open; texts = %v", tester.Texts())
	}
	dialog := settingsDialogBounds(1280, 800)
	// No body scrolling: the fixed footer already shows the page's action.
	apply, ok := tester.Find("保存")
	if !ok {
		t.Fatalf("fixed-footer primary action「保存」missing without scrolling; texts = %v", tester.Texts())
	}
	if !rectInside(dialog, apply) {
		t.Fatalf("「保存」outside the settings dialog: rect=%+v dialog=%+v", apply, dialog)
	}
	if !windowHasRect(1280, 800, apply) {
		t.Fatalf("「保存」outside the window: %+v", apply)
	}
	if apply.Y < dialog.Y+dialog.H*0.55 {
		t.Fatalf("「保存」is not in the dialog's bottom footer: rect=%+v dialog=%+v", apply, dialog)
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("fixed-footer primary action「保存」is not clickable: %v", err)
	}
	tester.Frame()

	// The settings close control sits inside the dialog and closes it.
	closeRect, ok := tester.Find("关闭设置")
	if !ok {
		t.Fatalf("settings close control missing; texts = %v", tester.Texts())
	}
	if !rectInside(dialog, closeRect) || !windowHasRect(1280, 800, closeRect) {
		t.Fatalf("settings close control outside the dialog/window: %+v (dialog %+v)", closeRect, dialog)
	}
	if err := tester.Click("关闭设置"); err != nil {
		t.Fatalf("click settings close: %v", err)
	}
	tester.Frame()
	if product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("settings surface still open after clicking its close control")
	}

	// A clean page closes on Escape (a dirty page asks first; see the guard
	// test below).
	if err := product.uiRoot.OpenSettingsPage("behavior"); err != nil {
		t.Fatalf("OpenSettingsPage(behavior): %v", err)
	}
	if !frameUntil(tester, "关闭设置", 2*time.Second) {
		t.Fatalf("settings did not reopen")
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("Escape did not close a clean settings page")
	}
}

// TestSettingsUnsavedGuard covers the close guard and the page Discard hook:
// editing a page marks it unsaved, closing asks (a dirty page that is not even
// the selected one guards too), and discarding resets the page so reopening it
// is clean.
func TestSettingsUnsavedGuard(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester

	if err := product.uiRoot.OpenSettingsPage("model"); err != nil {
		t.Fatalf("OpenSettingsPage(model): %v", err)
	}
	if !frameUntil(tester, "关闭设置", 2*time.Second) {
		t.Fatalf("settings did not open; texts = %v", tester.Texts())
	}
	// Make one edit: append text to the system-prompt draft (the page's
	// connection form was replaced by the immediately-applied model list, so
	// the prompt is the page's explicit-save field). Click the accessibly
	// labeled input target (the visible field label is not interactive).
	if err := tester.Click("系统提示词 输入"); err != nil {
		t.Fatalf("click system prompt field: %v; texts = %v", err, tester.Texts())
	}
	tester.Type("X")
	tester.Frame()
	tester.Frame()
	if !tester.HasText("未保存") {
		t.Fatalf("edited model page is not marked unsaved; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if !product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("Escape discarded unsaved edits instead of asking")
	}
	for _, want := range []string{"有未保存的修改", "继续编辑", "放弃修改"} {
		if !tester.HasText(want) {
			t.Fatalf("unsaved-changes bar missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("继续编辑"); err != nil {
		t.Fatalf("click keep editing: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if !product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("keep-editing closed the settings surface")
	}
	if tester.HasText("有未保存的修改") {
		t.Fatalf("keep-editing left the guard bar visible")
	}
	// Any dirty page guards the close: keep the model page dirty and switch to
	// a clean page, then Escape.
	if err := product.uiRoot.OpenSettingsPage("appearance"); err != nil {
		t.Fatalf("OpenSettingsPage(appearance): %v", err)
	}
	tester.Frame()
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if !product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("a dirty non-selected page did not guard the close")
	}
	if !tester.HasText("有未保存的修改") {
		t.Fatalf("cross-page guard bar missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("放弃修改"); err != nil {
		t.Fatalf("click discard changes: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("discard changes did not close the settings surface")
	}
	// Discard resets the drafts: reopening the model page is clean and its
	// Escape closes without asking.
	if err := product.uiRoot.OpenSettingsPage("model"); err != nil {
		t.Fatalf("reopen model page: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if tester.HasText("未保存") {
		t.Fatalf("discarded page still marked unsaved after reopening; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("clean reopened page did not close on Escape")
	}
}

// TestNarrowComposerSend covers the 1024×720 window: the send control must be
// inside the window and actually send the prompt.
func TestNarrowComposerSend(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester
	tester.SetSize(1024, 720)
	tester.Frame()
	tester.Frame()
	if err := uiRootPushSession(product.uiRoot); err != nil {
		t.Fatalf("push session: %v", err)
	}
	tester.Frame()
	if !tester.HasText("输入消息…") {
		t.Fatalf("composer placeholder missing at 1024x720; texts = %v", tester.Texts())
	}
	if err := tester.Click("输入消息…"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tester.Type("窄窗发送测试")
	tester.Frame()
	sendRect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing at 1024x720; texts = %v", tester.Texts())
	}
	if !windowHasRect(1024, 720, sendRect) {
		t.Fatalf("send control outside the 1024x720 window: %+v", sendRect)
	}
	if err := tester.Click("发送"); err != nil {
		t.Fatalf("send control not clickable: %v", err)
	}
	sessionID := product.chat.ActiveSession()
	if sessionID == "" {
		t.Fatalf("no active session after sending")
	}
	waitIdle(t, product.chat, sessionID, 10*time.Second)
	tester.Frame()
	if !tester.HasText("你好") {
		t.Fatalf("assistant reply missing after sending from the narrow composer; texts = %v", tester.Texts())
	}
}

// TestDialogSafetyAndButtonOrder covers the dialogs after the H-series
// interaction fixes: every dialog keeps the cancel/danger action left of the
// primary action, the approval dialog's safe default focuses Deny and Enter
// never allows a call; the plan's empty Enter does not approve, and Enter with
// feedback keeps planning instead.
func TestDialogSafetyAndButtonOrder(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester
	approvals := requireService(t, product.root, contracts.KeyApprovals)
	reviewer := requireService(t, product.root, contracts.KeyPlanReviewer)

	// --- approval: deny (danger) left, always-allow middle, allow right ---
	type decisionResult struct {
		decision contracts.ApprovalDecision
		err      error
	}
	request := func(id string) <-chan decisionResult {
		decisions := make(chan decisionResult, 1)
		approvalCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		t.Cleanup(cancel)
		go func() {
			decision, err := approvals.Request(approvalCtx, contracts.ApprovalRequest{
				ID: id, Tool: "bash", Summary: "运行命令",
				Arguments: `{"command":"echo hi"}`,
			})
			decisions <- decisionResult{decision: decision, err: err}
		}()
		return decisions
	}
	decisions := request("e2e-approval-safe")
	if !frameUntil(tester, "需要审批", 5*time.Second) {
		t.Fatalf("approval dialog missing; texts = %v", tester.Texts())
	}
	deny, okDeny := tester.Find("拒绝")
	allow, okAllow := tester.Find("允许一次")
	more, okMore := tester.Find("更多")
	if !okDeny || !okAllow || !okMore {
		t.Fatalf("approval buttons missing (deny=%v allow_once=%v more=%v); texts = %v",
			okDeny, okAllow, okMore, tester.Texts())
	}
	if !(deny.X < allow.X && allow.X < more.X) {
		t.Fatalf("approval order is not deny→allow-once→more: deny=%.1f allow=%.1f more=%.1f",
			deny.X, allow.X, more.X)
	}
	// B-spec v2 moved the session-scoped allow out of the action row: the
	// scope lives in the menu beside the actions (one accent fill, one menu),
	// so the card must not print it before the menu opens.
	if tester.HasText("本会话内始终允许") {
		t.Fatalf("approval card shows the session-scoped allow outside the scope menu; texts = %v", tester.Texts())
	}
	// No default action: nothing is auto-focused, so a stray Enter (the user
	// often presses it right after sending a prompt) can neither allow nor
	// deny the call.
	if tester.Focused("拒绝") || tester.Focused("允许一次") || tester.Focused("更多") {
		t.Fatalf("approval dialog autofocuses an action; texts = %v", tester.Texts())
	}
	// The inline card carries the no-default semantics as the explicit Esc hint
	// (there is no dialog-wide default button; Enter alone never allows).
	if !tester.HasText("Esc 拒绝") {
		t.Fatalf("inline approval lacks the visible no-default-action hint; texts = %v", tester.Texts())
	}
	// The raw arguments are collapsed behind an explicit disclosure.
	if !tester.HasText("原始参数") {
		t.Fatalf("approval dialog lacks the collapsed raw-arguments disclosure; texts = %v", tester.Texts())
	}
	// Enter must never allow the call.
	tester.Key(0, ui.KeyEnter)
	resolved := false
	var resolvedDecision contracts.ApprovalDecision
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tester.Frame()
		select {
		case result := <-decisions:
			if result.err != nil {
				t.Fatalf("approval request errored: %v", result.err)
			}
			resolved, resolvedDecision = true, result.decision
		default:
		}
		if resolved || !tester.HasText("需要审批") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resolved && resolvedDecision == contracts.ApprovalAllow {
		t.Fatalf("Enter allowed the approval dialog (must not be a dialog-wide Allow)")
	}
	if !resolved {
		// Enter did nothing (or only closed the dialog): deny explicitly so the
		// request does not hang.
		if tester.HasText("需要审批") {
			if err := tester.Click("拒绝"); err != nil {
				t.Fatalf("click deny: %v; texts = %v", err, tester.Texts())
			}
		}
		select {
		case result := <-decisions:
			if result.err != nil {
				t.Fatalf("approval request errored: %v", result.err)
			}
			if result.decision == contracts.ApprovalAllow {
				t.Fatalf("approval resolved as allow; want deny")
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("approval request did not resolve after deny; texts = %v", tester.Texts())
		}
	}
	if !frameUntilGone(tester, "需要审批", 2*time.Second) {
		t.Fatalf("approval dialog still shows after resolving")
	}

	// Explicit allow through the dialog still works and resolves as Allow.
	decisions = request("e2e-approval-allow")
	if !frameUntil(tester, "需要审批", 5*time.Second) {
		t.Fatalf("second approval dialog missing")
	}
	if err := tester.Click("允许一次"); err != nil {
		t.Fatalf("click allow once: %v; texts = %v", err, tester.Texts())
	}
	select {
	case result := <-decisions:
		if result.err != nil || result.decision != contracts.ApprovalAllow {
			t.Fatalf("explicit allow returned (%q, %v)", result.decision, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("explicit allow did not resolve")
	}
	frameUntilGone(tester, "需要审批", 2*time.Second)

	// --- the scope menu is the only route to a session-scoped allow: choosing
	// it relabels the primary action, and the decision carries the wider scope.
	// This is the "never grant more than the button says" path (A4-I-04). ---
	decisions = request("e2e-approval-scope")
	if !frameUntil(tester, "需要审批", 5*time.Second) {
		t.Fatalf("scope-menu approval card missing")
	}
	if err := tester.Click("更多"); err != nil {
		t.Fatalf("open the approval scope menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("本会话内始终允许该工具") {
		t.Fatalf("scope menu missing the session-scoped allow; texts = %v", tester.Texts())
	}
	if err := tester.Click("本会话内始终允许该工具"); err != nil {
		t.Fatalf("choose the session scope: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	// The label now promises the wider scope; the button that resolves it is
	// exactly that label, not 允许一次.
	if err := tester.Click("本会话内始终允许"); err != nil {
		t.Fatalf("primary action did not follow the chosen scope: %v; texts = %v", err, tester.Texts())
	}
	select {
	case result := <-decisions:
		if result.err != nil || result.decision != contracts.ApprovalAllowAlways {
			t.Fatalf("session-scoped allow returned (%q, %v)", result.decision, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("session-scoped allow did not resolve")
	}
	frameUntilGone(tester, "需要审批", 2*time.Second)

	// --- plan review: cancel left, approve right; empty Enter does not
	// approve; feedback + Enter keeps planning (cancels the review and sends
	// the feedback to the agent). ---
	go func() {
		_, _ = reviewer.Review(context.Background(), "# 发布计划\n\n1. 运行测试\n2. 打 tag")
	}()
	if !frameUntil(tester, "计划待审阅", 5*time.Second) {
		t.Fatalf("plan review dialog missing; texts = %v", tester.Texts())
	}
	// The decision bar has three actions now (B-spec §3.2.6): cancel the plan,
	// keep planning with feedback (needs feedback, so it is disabled on an
	// empty box) and the one primary, approve. Approve is last and rightmost.
	cancelPlan, okCancel := tester.Find("取消计划")
	keepPlanning, okKeep := tester.Find("继续规划")
	approve, okApprove := tester.Find("批准")
	if !okCancel || !okKeep || !okApprove {
		t.Fatalf("plan review buttons missing (cancel=%v keep=%v approve=%v); texts = %v",
			okCancel, okKeep, okApprove, tester.Texts())
	}
	if !(cancelPlan.X < keepPlanning.X && keepPlanning.X < approve.X) {
		t.Fatalf("plan review order is not cancel→keep→approve: cancel=%.1f keep=%.1f approve=%.1f",
			cancelPlan.X, keepPlanning.X, approve.X)
	}
	// The header states the scope of the decision and what approving means; the
	// hint line names the two chords and says plainly that Enter does nothing
	// without feedback (A4-I-04 replaced the old "点击「批准」批准计划" sentence).
	if !tester.HasText("批准后开始实施") {
		t.Fatalf("plan review lacks the what-approving-does line; texts = %v", tester.Texts())
	}
	if !tester.HasText("2 步") {
		t.Fatalf("plan review header does not count the plan's steps; texts = %v", tester.Texts())
	}
	if !tester.HasText("不取消计划") || !tester.HasText("无反馈时不生效") {
		t.Fatalf("plan review hint line is missing the chord warnings; texts = %v", tester.Texts())
	}
	if tester.HasText("Enter = 批准") || tester.HasText("Enter=批准") || tester.HasText("点击「批准」批准计划") {
		t.Fatalf("plan review hint still advertises a one-key approve; texts = %v", tester.Texts())
	}
	// Escape must not cancel a plan under review, and it must not look dead: the
	// card answers the press with the sentence that names the real exits (C-16).
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if !tester.HasText("计划待审阅") || reviewer.State() != "reviewing" {
		t.Fatalf("Escape cancelled the plan review (state %q); texts = %v", reviewer.State(), tester.Texts())
	}
	if !tester.HasText("Esc 不能关闭计划审阅") {
		t.Fatalf("Escape did nothing visible in the plan review; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter) // empty feedback: must not approve
	time.Sleep(200 * time.Millisecond)
	tester.Frame()
	if reviewer.State() == "approved" {
		t.Fatalf("empty-feedback Enter approved the plan (must be explicit)")
	}
	if err := tester.Click("批准"); err != nil {
		t.Fatalf("click approve: %v; texts = %v", err, tester.Texts())
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && reviewer.State() != "approved" {
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if reviewer.State() != "approved" {
		t.Fatalf("explicit approve did not approve the plan (state %q)", reviewer.State())
	}
	frameUntilGone(tester, "计划待审阅", 2*time.Second)

	// Feedback + Enter keeps planning instead of approving.
	go func() {
		_, _ = reviewer.Review(context.Background(), "# 反馈计划\n\n1. 第一步")
	}()
	if !frameUntil(tester, "计划待审阅", 5*time.Second) {
		t.Fatalf("second plan review dialog missing")
	}
	if err := tester.Click("继续规划反馈"); err != nil {
		t.Fatalf("click plan feedback input: %v; texts = %v", err, tester.Texts())
	}
	tester.Type("请补充边界测试")
	tester.Frame()
	if !tester.HasText("发送反馈继续规划") {
		t.Fatalf("plan hint did not switch to the send-feedback chord; texts = %v", tester.Texts())
	}
	sessionID := product.chat.ActiveSession()
	userMessages := func() int {
		count := 0
		if session, ok := product.chat.Session(sessionID); ok {
			for _, message := range session.Messages {
				if message.Role == contracts.ChatRoleUser {
					count++
				}
			}
		}
		return count
	}
	before := userMessages()
	tester.Key(0, ui.KeyEnter)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tester.Frame()
		if userMessages() > before || reviewer.State() == "cancelled" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if reviewer.State() == "approved" {
		t.Fatalf("feedback Enter approved the plan (must keep planning)")
	}
	if userMessages() <= before {
		t.Fatalf("keep-planning did not send the feedback to the agent; texts = %v", tester.Texts())
	}
	if !frameUntil(tester, "已发送继续规划反馈", 2*time.Second) {
		t.Fatalf("keep-planning toast missing; texts = %v", tester.Texts())
	}
	if session, ok := product.chat.Session(sessionID); ok {
		lastUser := session.Messages
		for index := len(lastUser) - 1; index >= 0; index-- {
			if lastUser[index].Role == contracts.ChatRoleUser {
				if !strings.Contains(lastUser[index].Text, "请补充边界测试") {
					t.Fatalf("keep-planning feedback message = %q", lastUser[index].Text)
				}
				break
			}
		}
	}
	waitIdle(t, product.chat, sessionID, 10*time.Second)
}

// TestNativeToastAppearsAndExpires covers the notifier bridge rendering a
// native toast that disappears after its TTL on the tester clock.
func TestNativeToastAppearsAndExpires(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester
	notifier := requireService(t, product.root, contracts.KeyNotifier)

	notification := notifier.Notify(contracts.NotifySuccess, "测试通知：导出完成")
	defer notifier.Dismiss(notification.ID)
	if !frameUntil(tester, "测试通知：导出完成", 3*time.Second) {
		t.Fatalf("native toast did not appear; texts = %v", tester.Texts())
	}
	tester.Move(4, 4) // keep the pointer off the toast (time pauses on hover)
	tester.Advance(5 * time.Second)
	tester.Frame()
	if tester.HasText("测试通知：导出完成") {
		t.Fatalf("native toast still shows after its TTL; texts = %v", tester.Texts())
	}
}

// TestSidebarCollapseAndFooter covers Cmd+B collapse/expand and the docked
// footer settings entry.
func TestSidebarCollapseAndFooter(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester

	tester.Key(ui.Cmd, ui.KeyB)
	tester.Frame()
	tester.Frame()
	if !product.uiRoot.SidebarCollapsed() {
		t.Fatalf("Cmd+B did not collapse the sidebar; texts = %v", tester.Texts())
	}
	// The rail keeps the nav destinations and the footer entry; Home is no
	// longer a destination (the session screen is the landing). The new-session
	// action is 新建会话 now (A5: 会话/Session wording, not 对话).
	for _, want := range []string{"新建会话", "自动化", "能力", "设置"} {
		if !tester.HasText(want) {
			t.Fatalf("collapsed rail is missing %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("首页") {
		t.Fatalf("the collapsed rail still lists the removed Home destination; texts = %v", tester.Texts())
	}
	tester.Key(ui.Cmd, ui.KeyB)
	tester.Frame()
	tester.Frame()
	if product.uiRoot.SidebarCollapsed() {
		t.Fatalf("Cmd+B did not expand the sidebar")
	}

	// The docked footer settings entry opens the settings surface.
	if err := tester.Click("设置"); err != nil {
		t.Fatalf("click sidebar footer settings entry: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("footer settings entry did not open settings; texts = %v", tester.Texts())
	}
	if !tester.HasText("关闭设置") {
		t.Fatalf("settings surface missing its close control; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("Escape did not close the settings surface")
	}
}

// uiRootPushSession pushes the conversation screen through the navigator.
func uiRootPushSession(root *uiroot.Root) error {
	return root.Navigator().Push("session")
}

// ensure the imports used only by some build tags do not go stale.
var _ = strings.TrimSpace
