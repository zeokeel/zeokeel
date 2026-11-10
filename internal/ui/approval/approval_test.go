package approval_test

// Headless tests for the approval banner module: the real host frame
// (uiroot.View) is rendered through mygo's in-memory Tester. The module
// contributes the uikit inline approval card to contracts.SlotSessionBanner,
// which the session screen renders above the composer, so the tests register a
// session screen that renders the slot (the real ui/session module does the
// same), and assert what the user sees plus the decisions that reach the
// approvals service.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/approval"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeApprovals is a minimal contracts.Approvals: Pending is a FIFO queue the
// test fills, Resolve records the decision and notifies subscribers like the
// real backend. With settle=true the resolved request stays in Pending until
// settleResolved, emulating the backend's asynchronous settle.
type fakeApprovals struct {
	mu                 sync.Mutex
	mode               string
	pending            []contracts.ApprovalRequest
	decisions          []contracts.ApprovalDecision
	resolvedRequestIDs []string
	subscribers        []func()
	unsubscribed       bool
	settle             bool
	settled            map[string]bool
}

func (f *fakeApprovals) Mode() string { return f.mode }

// Request is not used by the banner (it only reads Pending); keep it simple.
func (f *fakeApprovals) Request(_ context.Context, request contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	f.add(request)
	return contracts.ApprovalAllow, nil
}

func (f *fakeApprovals) Pending() []contracts.ApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.ApprovalRequest(nil), f.pending...)
}

func (f *fakeApprovals) Resolve(id string, decision contracts.ApprovalDecision) error {
	f.mu.Lock()
	f.decisions = append(f.decisions, decision)
	f.resolvedRequestIDs = append(f.resolvedRequestIDs, id)
	if f.settle {
		if f.settled == nil {
			f.settled = map[string]bool{}
		}
		f.settled[id] = true
	} else {
		f.pending = removeRequest(f.pending, id)
	}
	subscribers := append([]func(){}, f.subscribers...)
	f.mu.Unlock()
	for _, fn := range subscribers {
		fn()
	}
	return nil
}

// settleResolved removes the requests answered while settle was on, like the
// backend does once the blocked tool call has been released.
func (f *fakeApprovals) settleResolved() {
	f.mu.Lock()
	for id := range f.settled {
		f.pending = removeRequest(f.pending, id)
	}
	f.settled = nil
	subscribers := append([]func(){}, f.subscribers...)
	f.mu.Unlock()
	for _, fn := range subscribers {
		fn()
	}
}

// removeRequest drops one request from a pending slice.
func removeRequest(pending []contracts.ApprovalRequest, id string) []contracts.ApprovalRequest {
	remaining := make([]contracts.ApprovalRequest, 0, len(pending))
	for _, request := range pending {
		if request.ID != id {
			remaining = append(remaining, request)
		}
	}
	return remaining
}

func (f *fakeApprovals) Subscribe(fn func()) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribers = append(f.subscribers, fn)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.unsubscribed = true
	}, nil
}

// add enqueues one request and notifies subscribers, like the backend does when
// a gate blocks.
func (f *fakeApprovals) add(request contracts.ApprovalRequest) {
	f.mu.Lock()
	f.pending = append(f.pending, request)
	subscribers := append([]func(){}, f.subscribers...)
	f.mu.Unlock()
	for _, fn := range subscribers {
		fn()
	}
}

func (f *fakeApprovals) resolved() []contracts.ApprovalDecision {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.ApprovalDecision(nil), f.decisions...)
}

func (f *fakeApprovals) resolvedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resolvedRequestIDs...)
}

func (f *fakeApprovals) pendingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pending)
}

func (f *fakeApprovals) isUnsubscribed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unsubscribed
}

// recordingNavigator delegates to the host navigator and records Push calls, so
// the auto-switch to the session screen is observable without losing the real
// navigation state the frame renders.
type recordingNavigator struct {
	inner  contracts.Navigator
	mu     sync.Mutex
	pushes []string
	// failPush makes Push fail (the session screen not registered yet), so the
	// ignore-and-stay contract is testable.
	failPush bool
}

func (n *recordingNavigator) Current() string { return n.inner.Current() }

func (n *recordingNavigator) Push(screenID string) error {
	n.mu.Lock()
	n.pushes = append(n.pushes, screenID)
	fail := n.failPush
	n.mu.Unlock()
	if fail {
		return errors.New("session screen is not registered")
	}
	return n.inner.Push(screenID)
}

func (n *recordingNavigator) Back() bool         { return n.inner.Back() }
func (n *recordingNavigator) SettingsOpen() bool { return n.inner.SettingsOpen() }
func (n *recordingNavigator) ToggleSettings()    { n.inner.ToggleSettings() }
func (n *recordingNavigator) CloseSettings()     { n.inner.CloseSettings() }

func (n *recordingNavigator) pushCalls() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.pushes...)
}

// harness is one headless product with the host UI services and the approval
// module active. The session screen renders the banner slot; the home screen is
// a plain screen used by the auto-navigation tests.
type harness struct {
	t         *testing.T
	ctx       cordis.Context
	uiRoot    *uiroot.Root
	approvals *fakeApprovals
	navigator *recordingNavigator
	fiber     *cordis.Fiber
}

func newHarness(t *testing.T, cfg approval.Config) *harness {
	t.Helper()
	approvals := &fakeApprovals{mode: "ask"}
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	// The session screen renders the banner slot directly above where the
	// composer would be; the real ui/session module does the same.
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "session",
		Title: "会话",
		Build: func(c *ui.Context) {
			ui.Text(c, "会话屏")
			uiRoot.Runtime().RenderSlot(c, contracts.SlotSessionBanner)
		},
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "home",
		Title: "首页",
		Build: func(c *ui.Context) { ui.Text(c, "主屏幕") },
	}); err != nil {
		t.Fatalf("register home screen: %v", err)
	}

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(approvals))
	nav := &recordingNavigator{inner: uiRoot.Navigator()}
	provide(t, ctx, contracts.KeyNavigator, contracts.Navigator(nav))

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, approvals: approvals, navigator: nav}
	h.fiber = activate(t, ctx, approval.Module(), cfg)
	// The approval card is read on the session screen.
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("open session screen: %v", err)
	}
	return h
}

func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.uiRoot.View, 1200, 800)
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	// Build the frame after the click handler mutated the approvals service.
	tester.Frame()
}

// frames builds n frames (the approvals subscription hops through Post, so a
// state change may need one frame to drain and one to render).
func (h *harness) frames(tester *ui.Tester, n int) {
	h.t.Helper()
	for range n {
		tester.Frame()
	}
}

func (h *harness) assertTexts(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	for _, want := range wanted {
		if !tester.HasText(want) {
			h.t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

func (h *harness) assertNoTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if tester.HasText(text) {
			h.t.Fatalf("frame unexpectedly shows %q; texts = %v", text, tester.Texts())
		}
	}
}

// assertNoExactTexts asserts no element shows exactly one of the unwanted
// labels: unlike assertNoTexts (substring), it can check a default label that
// also occurs inside a longer sentence (the copy starts with "允许后…").
func (h *harness) assertNoExactTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if _, ok := tester.Find(text); ok {
			h.t.Fatalf("frame unexpectedly shows the label %q; texts = %v", text, tester.Texts())
		}
	}
}

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// descriptorPlugin adapts a module.Descriptor to the kernel plugin interfaces.
type descriptorPlugin[T any] struct {
	descriptor *module.Descriptor[T]
	config     T
	inject     []string
}

func (p *descriptorPlugin[T]) Name() string { return p.descriptor.ID }

func (p *descriptorPlugin[T]) Inject() []string { return p.inject }

func (p *descriptorPlugin[T]) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

func activate[T any](t *testing.T, ctx cordis.Context, descriptor *module.Descriptor[T], config T) *cordis.Fiber {
	t.Helper()
	plugin := &descriptorPlugin[T]{descriptor: descriptor, config: config}
	inject := make([]string, 0, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		if !requirement.Optional {
			inject = append(inject, requirement.Name)
		}
	}
	plugin.inject = inject
	fiber, err := ctx.Plugin(plugin, nil)
	if err != nil {
		t.Fatalf("activate %s: %v", descriptor.ID, err)
	}
	if state := fiber.State(); state != cordis.Active {
		t.Fatalf("module %s state = %s (%v), want Active", descriptor.ID, state, fiber.ErrorDetail())
	}
	return fiber
}

func pendingRequest(id, tool, summary, arguments string) contracts.ApprovalRequest {
	return contracts.ApprovalRequest{
		ID:        id,
		Tool:      tool,
		Summary:   summary,
		Arguments: arguments,
	}
}

// --- tests ------------------------------------------------------------------

// TestNoPendingRendersNoBanner covers the empty state: without a pending
// request the slot contributes no controls (the session screen still renders).
func TestNoPendingRendersNoBanner(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "会话屏")
	h.assertNoTexts(tester, "需要审批", "允许", "拒绝", "本会话内始终允许")
}

// TestPendingRendersInlineBanner covers the card anatomy: the oldest pending
// request, its warning banner (title, friendly tool, queue counter, Esc hint),
// the impact copy, the promoted summary, the collapsed arguments and the three
// decisions.
func TestPendingRendersInlineBanner(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`))
	h.approvals.add(pendingRequest("a2", "write_file", "写入文件", `{"path":"x"}`))
	h.frames(tester, 2)

	h.assertTexts(tester,
		"会话屏", "需要审批", "运行命令", "还有 1 条待审批", "Esc 拒绝",
		"命令", "运行命令 ls", "原始参数",
		"允许", "本会话内始终允许", "拒绝",
		"允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
	)
	// The friendly name is the visible copy; the raw tool id only lives in the
	// tooltip, and the raw arguments are collapsed details.
	h.assertNoTexts(tester, "bash", `{"command":"ls"}`, "参数详情")
	// The queued second request waits behind the first instead of replacing it.
	h.assertNoTexts(tester, "写入文件")

	// The footer keeps the desktop decision order: deny, session-scoped allow,
	// primary allow.
	deny, ok := tester.Find("拒绝")
	if !ok {
		t.Fatalf("deny action missing; texts = %v", tester.Texts())
	}
	always, ok := tester.Find("本会话内始终允许")
	if !ok {
		t.Fatalf("session-scoped allow action missing; texts = %v", tester.Texts())
	}
	allow, ok := tester.Find("允许")
	if !ok {
		t.Fatalf("allow action missing; texts = %v", tester.Texts())
	}
	if !(deny.X < always.X && always.X < allow.X) {
		t.Fatalf("footer order x = deny %.1f, always %.1f, allow %.1f; want deny < always < allow",
			deny.X, always.X, allow.X)
	}
	if tester.Focused("拒绝") || tester.Focused("允许") || tester.Focused("本会话内始终允许") {
		t.Fatalf("the approval card must not autofocus an action; texts = %v", tester.Texts())
	}
}

// TestApprovalCardIsRegionExposed covers a11y: the inline card is one named
// region, so assistive technology can jump to the pending decision.
func TestApprovalCardIsRegionExposed(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`))
	h.frames(tester, 2)
	if _, ok := tester.AccessByRole(ui.RoleRegion, "需要审批 · 运行命令"); !ok {
		t.Fatalf("the approval card is not exposed as a named region; texts = %v", tester.Texts())
	}
}

// TestDecisionButtonsResolveOnce covers the three explicit decisions: each
// resolves the displayed request with its decision exactly once and removes the
// card.
func TestDecisionButtonsResolveOnce(t *testing.T) {
	cases := []struct {
		label    string
		decision contracts.ApprovalDecision
	}{
		{label: "允许", decision: contracts.ApprovalAllow},
		{label: "本会话内始终允许", decision: contracts.ApprovalAllowAlways},
		{label: "拒绝", decision: contracts.ApprovalDeny},
	}
	for _, test := range cases {
		t.Run(string(test.decision), func(t *testing.T) {
			h := newHarness(t, approval.Config{})
			tester := h.tester()
			h.approvals.add(pendingRequest("r1", "bash", "运行命令", `{"command":"ls"}`))
			h.frames(tester, 2)
			h.assertTexts(tester, "需要审批")

			h.click(tester, test.label)
			h.frames(tester, 3)

			got := h.approvals.resolved()
			if len(got) != 1 || got[0] != test.decision {
				t.Fatalf("decisions = %v, want [%s]", got, test.decision)
			}
			if ids := h.approvals.resolvedIDs(); len(ids) != 1 || ids[0] != "r1" {
				t.Fatalf("resolved IDs = %v, want [r1]", ids)
			}
			h.assertNoTexts(tester, "需要审批", "运行命令")
		})
	}
}

// TestResolvedRequestDoesNotReopen covers the settle window: a resolved request
// that the backend still lists does not bring the card back, and the next queued
// request appears once it settles.
func TestResolvedRequestDoesNotReopen(t *testing.T) {
	h := newHarness(t, approval.Config{})
	h.approvals.settle = true
	tester := h.tester()
	h.approvals.add(pendingRequest("s1", "bash", "第一个", `{"command":"ls"}`))
	h.approvals.add(pendingRequest("s2", "write_file", "第二个", `{"path":"x"}`))
	h.frames(tester, 2)
	h.assertTexts(tester, "第一个")

	h.click(tester, "允许")
	// The backend still lists s1; the card must not reopen for it.
	h.frames(tester, 3)
	if got := h.approvals.resolved(); len(got) != 1 {
		t.Fatalf("decisions after one answer = %v, want 1", got)
	}
	h.assertNoTexts(tester, "需要审批", "第一个")

	// The settle removes s1 and the next request takes the card.
	h.approvals.settleResolved()
	h.frames(tester, 2)
	h.assertTexts(tester, "需要审批", "第二个", "写入文件")
}

// TestAutoSwitchesToSessionScreen covers the visibility rule: a request that
// arrives while another screen shows brings the session screen forward (once),
// so the inline card cannot be missed.
func TestAutoSwitchesToSessionScreen(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	if err := h.uiRoot.Navigator().Push("home"); err != nil {
		t.Fatalf("open home screen: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "主屏幕")

	h.approvals.add(pendingRequest("n1", "bash", "运行命令", `{"command":"ls"}`))
	h.frames(tester, 3)

	if got := h.uiRoot.Navigator().Current(); got != "session" {
		t.Fatalf("current screen = %q, want session", got)
	}
	if pushes := h.navigator.pushCalls(); len(pushes) != 1 || pushes[0] != "session" {
		t.Fatalf("auto-navigation pushes = %v, want one session push", pushes)
	}
	h.assertTexts(tester, "会话屏", "需要审批")

	// Further frames do not push again while the session screen stays current.
	h.frames(tester, 2)
	if pushes := h.navigator.pushCalls(); len(pushes) != 1 {
		t.Fatalf("auto-navigation repeated while already on session: %v", pushes)
	}
}

// TestNoAutoSwitchWhenAlreadyOnSession covers the no-op case: a request on the
// session screen never navigates.
func TestNoAutoSwitchWhenAlreadyOnSession(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("n1", "bash", "运行命令", `{"command":"ls"}`))
	h.frames(tester, 2)
	h.assertTexts(tester, "需要审批")
	if pushes := h.navigator.pushCalls(); len(pushes) != 0 {
		t.Fatalf("navigation pushed %v while already on the session screen", pushes)
	}
}

// TestAutoSwitchFailureIsIgnored covers the failure path: when the session
// screen cannot be pushed (not registered yet), the navigation attempt is
// ignored, the request stays pending and the module never retries in a loop.
func TestAutoSwitchFailureIsIgnored(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	if err := h.uiRoot.Navigator().Push("home"); err != nil {
		t.Fatalf("open home screen: %v", err)
	}
	tester.Frame()
	h.navigator.failPush = true

	h.approvals.add(pendingRequest("n1", "bash", "运行命令", `{"command":"ls"}`))
	h.frames(tester, 3)

	if got := h.uiRoot.Navigator().Current(); got != "home" {
		t.Fatalf("current screen = %q, want home after a failed push", got)
	}
	if pushes := h.navigator.pushCalls(); len(pushes) != 1 || pushes[0] != "session" {
		t.Fatalf("push attempts = %v, want one ignored session attempt", pushes)
	}
	if got := h.approvals.pendingCount(); got != 1 {
		t.Fatalf("pending requests = %d, want the request kept", got)
	}
	// The card is not rendered on the home screen; it appears once the session
	// screen is showing again.
	h.navigator.failPush = false
	if err := h.uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("open session screen: %v", err)
	}
	h.frames(tester, 2)
	h.assertTexts(tester, "需要审批")
}

// TestEscDoesNotResolveInTheModule covers the keyboard contract split: the card
// component and the module handle no keyboard shortcut; the session screen owns
// Escape (deny the oldest pending request). A stray Escape must never resolve
// through this module.
func TestEscDoesNotResolveInTheModule(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("k1", "bash", "运行命令", `{"command":"ls"}`))
	h.frames(tester, 2)
	h.assertTexts(tester, "需要审批")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if got := h.approvals.resolved(); len(got) != 0 {
		t.Fatalf("Escape resolved the request through the approval module: %v", got)
	}
	h.assertTexts(tester, "需要审批")
}

// TestSubscriptionInvalidatesFrames covers the Subscribe -> Post -> Invalidate
// bridge: enqueuing a request from any goroutine schedules the frame that shows
// the card.
func TestSubscriptionInvalidatesFrames(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	tester.Frame()
	before := h.uiRoot.Frames()

	h.approvals.add(pendingRequest("s1", "bash", "订阅后出现", `{"command":"ls"}`))
	h.frames(tester, 2)

	if after := h.uiRoot.Frames(); after <= before {
		t.Fatalf("frames did not advance after the approvals change: before=%d after=%d", before, after)
	}
	h.assertTexts(tester, "需要审批", "订阅后出现")
	if h.approvals.isUnsubscribed() {
		t.Fatal("approvals subscription was unwound before disposal")
	}
}

// TestDisposeUnregistersContribution covers kernel effect unwinding: with the
// module disposed the session.banner contribution and the subscription are gone.
func TestDisposeUnregistersContribution(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("d1", "bash", "运行命令", `{"command":"ls"}`))
	h.frames(tester, 2)
	h.assertTexts(tester, "需要审批")
	if !slices.Contains(h.uiRoot.Runtime().Slots(), contracts.SlotSessionBanner) {
		t.Fatalf("session.banner is not contributed: %v", h.uiRoot.Runtime().Slots())
	}

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose approval module: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "需要审批")
	if !h.approvals.isUnsubscribed() {
		t.Fatal("approvals subscription was not unwound on disposal")
	}
	if slices.Contains(h.uiRoot.Runtime().Slots(), contracts.SlotSessionBanner) {
		t.Fatalf("session.banner still has contributions after disposal: %v",
			h.uiRoot.Runtime().Slots())
	}
}

// TestBannerSlotRegistration covers the slot contract: the module contributes to
// session.banner (rendered by the session screen above the composer), not to the
// overlay.dialogs slot the old Modal lived in.
func TestBannerSlotRegistration(t *testing.T) {
	h := newHarness(t, approval.Config{})
	slots := h.uiRoot.Runtime().Slots()
	if !slices.Contains(slots, contracts.SlotSessionBanner) {
		t.Fatalf("session.banner is not registered: %v", slots)
	}
	if slices.Contains(slots, contracts.SlotOverlayDialogs) {
		t.Fatalf("the approval module still contributes to overlay.dialogs: %v", slots)
	}
}

// TestConfigLabels covers the assembly-config labels: custom copy is shown
// verbatim while untouched defaults keep the product copy.
func TestConfigLabels(t *testing.T) {
	h := newHarness(t, approval.Config{
		Title:            "Approve tool call",
		ArgumentsLabel:   "Arguments",
		AllowLabel:       "Allow once",
		AllowAlwaysLabel: "Always allow",
		DenyLabel:        "Reject",
	})
	tester := h.tester()
	h.approvals.add(pendingRequest("c1", "bash", "run", `{"command":"ls"}`))
	h.frames(tester, 2)
	h.assertTexts(tester, "Approve tool call", "Arguments", "运行命令",
		"Allow once", "Always allow", "Reject")
	h.assertNoTexts(tester, "需要审批")
	h.assertNoExactTexts(tester, "允许")
}

// TestLongArgumentsAreTruncated covers the bounded arguments block: a huge tool
// call stays readable and does not render its full payload.
func TestLongArgumentsAreTruncated(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	long := make([]byte, 0, 8000)
	long = append(long, `{"data":"`...)
	for len(long) < 8000 {
		long = append(long, 'x')
	}
	long = append(long, `"}`...)
	h.approvals.add(pendingRequest("l1", "bash", "大参数", string(long)))
	h.frames(tester, 2)
	h.assertTexts(tester, "需要审批", "原始参数")
	h.assertNoTexts(tester, `{"data":"`) // collapsed by default
	h.click(tester, "原始参数")
	h.assertTexts(tester, `{"data":"`)
	if tester.HasText(string(long)) {
		t.Fatal("the full arguments payload was rendered instead of the truncated block")
	}
}

// TestEmptyArgumentsFallback covers the "no arguments" fallback instead of an
// empty box when the call carries {} / [] / null.
func TestEmptyArgumentsFallback(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("e1", "read_file", "读取配置", "{}"))
	h.frames(tester, 2)
	h.assertTexts(tester, "需要审批", "无参数")
	h.assertNoTexts(tester, "原始参数")
}

// TestSummaryLabelFollowsToolKind covers S-08: only shell/terminal calls label
// their summary 命令; a file read keeps the neutral 摘要.
func TestSummaryLabelFollowsToolKind(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("f1", "read_file", "读取 internal/app.go", `{"path":"internal/app.go"}`))
	h.frames(tester, 2)
	h.assertTexts(tester, "摘要")
	for _, text := range tester.Texts() {
		if text == "命令" {
			t.Fatalf("read_file summary is labelled 命令; texts = %v", tester.Texts())
		}
	}
}

// TestModuleManifest pins the module contract.
func TestModuleManifest(t *testing.T) {
	descriptor := approval.Module()
	if descriptor.ID != "zeokeel-ui/approval" {
		t.Fatalf("module ID = %q, want zeokeel-ui/approval", descriptor.ID)
	}
	if descriptor.Factory == nil {
		t.Fatal("module has a nil factory")
	}
	if len(descriptor.Provides) != 0 {
		t.Fatalf("ui module provides %v, want none", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, name := range []string{
		contracts.KeyUIRuntime.Name(),
		contracts.KeyApprovals.Name(),
		contracts.KeyNavigator.Name(),
	} {
		if !required[name] {
			t.Fatalf("module does not require %q; requires = %v", name, descriptor.Requires)
		}
	}
}
