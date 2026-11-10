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
	for _, want := range []string{"搜索命令", "↑↓ 选择 · Enter 运行 · Esc 关闭"} {
		if !tester.HasText(want) {
			t.Fatalf("palette missing %q; texts = %v", want, tester.Texts())
		}
	}
	tester.Key(0, ui.KeyEscape)
	if !frameUntilGone(tester, "命令面板", 2*time.Second) {
		t.Fatalf("Escape did not close the palette; texts = %v", tester.Texts())
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
	apply, ok := tester.Find("应用")
	if !ok {
		t.Fatalf("fixed-footer primary action「应用」missing without scrolling; texts = %v", tester.Texts())
	}
	if !rectInside(dialog, apply) {
		t.Fatalf("「应用」outside the settings dialog: rect=%+v dialog=%+v", apply, dialog)
	}
	if !windowHasRect(1280, 800, apply) {
		t.Fatalf("「应用」outside the window: %+v", apply)
	}
	if apply.Y < dialog.Y+dialog.H*0.55 {
		t.Fatalf("「应用」is not in the dialog's bottom footer: rect=%+v dialog=%+v", apply, dialog)
	}
	if err := tester.Click("应用"); err != nil {
		t.Fatalf("fixed-footer primary action「应用」is not clickable: %v", err)
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
	always, okAlways := tester.Find("本会话内始终允许")
	allow, okAllow := tester.Find("允许")
	if !okDeny || !okAlways || !okAllow {
		t.Fatalf("approval buttons missing (deny=%v always=%v allow=%v); texts = %v",
			okDeny, okAlways, okAllow, tester.Texts())
	}
	if !(deny.X < always.X && always.X < allow.X) {
		t.Fatalf("approval order is not deny→always→allow: deny=%.1f always=%.1f allow=%.1f",
			deny.X, always.X, allow.X)
	}
	// No default action: nothing is auto-focused, so a stray Enter (the user
	// often presses it right after sending a prompt) can neither allow nor
	// deny the call.
	if tester.Focused("拒绝") || tester.Focused("允许") || tester.Focused("本会话内始终允许") {
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
	if err := tester.Click("允许"); err != nil {
		t.Fatalf("click allow: %v; texts = %v", err, tester.Texts())
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

	// --- plan review: cancel left, approve right; empty Enter does not
	// approve; feedback + Enter keeps planning (cancels the review and sends
	// the feedback to the agent). ---
	go func() {
		_, _ = reviewer.Review(context.Background(), "# 发布计划\n\n1. 运行测试\n2. 打 tag")
	}()
	if !frameUntil(tester, "计划待审阅", 5*time.Second) {
		t.Fatalf("plan review dialog missing; texts = %v", tester.Texts())
	}
	cancelPlan, okCancel := tester.Find("取消计划")
	approve, okApprove := tester.Find("批准")
	if !okCancel || !okApprove {
		t.Fatalf("plan review buttons missing (cancel=%v approve=%v); texts = %v",
			okCancel, okApprove, tester.Texts())
	}
	if !(cancelPlan.X < approve.X) {
		t.Fatalf("plan review order is not cancel→approve: cancel=%.1f approve=%.1f", cancelPlan.X, approve.X)
	}
	if !tester.HasText("点击「批准」批准计划") {
		t.Fatalf("plan review lacks the explicit-approve hint; texts = %v", tester.Texts())
	}
	if tester.HasText("Enter = 批准") || tester.HasText("Enter=批准") {
		t.Fatalf("plan review hint still advertises Enter=approve; texts = %v", tester.Texts())
	}
	// Escape must not cancel a plan under review.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if !tester.HasText("计划待审阅") || reviewer.State() != "reviewing" {
		t.Fatalf("Escape cancelled the plan review (state %q); texts = %v", reviewer.State(), tester.Texts())
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
	if !tester.HasText("发送反馈给 agent") {
		t.Fatalf("plan hint did not switch to the keep-planning copy; texts = %v", tester.Texts())
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
	// longer a destination (the session screen is the landing).
	for _, want := range []string{"新对话", "自动化", "插件", "设置"} {
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
