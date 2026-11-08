package approval_test

// Headless tests for the approval dialog module: the real host frame
// (uiroot.View) is rendered through mygo's in-memory Tester, so assertions are
// about what the user sees and the clicks/keys go through native controls.

import (
	"context"
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
// test fills, Resolve removes the request and records the decision, and
// subscribers are notified like the real backend.
type fakeApprovals struct {
	mu                 sync.Mutex
	mode               string
	pending            []contracts.ApprovalRequest
	decisions          []contracts.ApprovalDecision
	resolvedRequestIDs []string
	subscribers        []func()
	unsubcribed        bool
}

func (f *fakeApprovals) Mode() string { return f.mode }

// Request is not used by the dialog (it only reads Pending); keep it simple.
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
	remaining := f.pending[:0]
	for _, request := range f.pending {
		if request.ID != id {
			remaining = append(remaining, request)
		}
	}
	f.pending = remaining
	subscribers := append([]func(){}, f.subscribers...)
	f.mu.Unlock()
	for _, fn := range subscribers {
		fn()
	}
	return nil
}

func (f *fakeApprovals) Subscribe(fn func()) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribers = append(f.subscribers, fn)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.unsubcribed = true
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

func (f *fakeApprovals) isUnsubscribed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unsubcribed
}

// harness is one headless product with the host UI services and the approval
// module active.
type harness struct {
	t         *testing.T
	ctx       cordis.Context
	uiRoot    *uiroot.Root
	approvals *fakeApprovals
	fiber     *cordis.Fiber
}

func newHarness(t *testing.T, cfg approval.Config) *harness {
	t.Helper()
	approvals := &fakeApprovals{mode: "ask"}
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(approvals))

	// One screen keeps the host frame non-empty ("status.no-screen" otherwise).
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "home",
		Title: "首页",
		Build: func(c *ui.Context) { ui.Text(c, "主屏幕") },
	}); err != nil {
		t.Fatalf("register home screen: %v", err)
	}

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, approvals: approvals}
	h.fiber = activate(t, ctx, approval.Module(), cfg)
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

// TestNoPendingRendersNoDialog covers the empty state: without a pending
// request the slot contributes no controls at all (in particular no empty
// Modal).
func TestNoPendingRendersNoDialog(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "主屏幕")
	h.assertNoTexts(tester, "需要审批", "允许", "拒绝")
}

// TestPendingShowsOldestRequest covers rendering: the oldest pending request is
// the one shown, with tool, summary, arguments and the three decision buttons.
func TestPendingShowsOldestRequest(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()

	h.approvals.add(pendingRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`))
	h.approvals.add(pendingRequest("a2", "write_file", "写入文件", `{"path":"x"}`))
	tester.Frame()

	// The summary is promoted (a command for shell tools) and the raw
	// arguments are details, collapsed.
	h.assertTexts(tester, "需要审批", "工具", "bash", "命令", "运行命令 ls", "参数",
		"显示原始参数", "允许", "本次运行内始终允许", "拒绝")
	// Two labels only: the section label and the disclosure — never a third
	// 「参数详情」 layer (N3).
	h.assertNoTexts(tester, "参数详情", `{"command":"ls"}`)
	h.assertNoTexts(tester, "write_file", "写入文件")
}

// TestCopyStatesSandboxLimits covers the ZK-02 copy fix: an approval only lets
// the call through the gate; the sandbox policy still runs it and may deny it,
// so the dialog must not promise that the call executes immediately.
func TestCopyStatesSandboxLimits(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("p1", "bash", "运行命令 ls", `{"command":"ls"}`))
	tester.Frame()

	const zh = "允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。"
	const en = "It runs within the sandbox policy after approval; the policy may still reject the call."
	h.assertTexts(tester, zh)
	h.assertNoTexts(tester, "立即执行", "runs right away")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, en)
	h.assertNoTexts(tester, "立即执行", "runs right away")
}

// TestDecisionButtons covers the three buttons: each resolves the request with
// its decision and the dialog disappears once the queue is empty.
func TestDecisionButtons(t *testing.T) {
	cases := []struct {
		label    string
		decision contracts.ApprovalDecision
	}{
		{label: "允许", decision: contracts.ApprovalAllow},
		{label: "本次运行内始终允许", decision: contracts.ApprovalAllowAlways},
		{label: "拒绝", decision: contracts.ApprovalDeny},
	}
	for _, test := range cases {
		t.Run(string(test.decision), func(t *testing.T) {
			h := newHarness(t, approval.Config{})
			tester := h.tester()
			h.approvals.add(pendingRequest("r1", "bash", "运行命令", `{"command":"ls"}`))
			tester.Frame()
			h.assertTexts(tester, "需要审批")

			h.click(tester, test.label)

			got := h.approvals.resolved()
			if len(got) != 1 || got[0] != test.decision {
				t.Fatalf("decisions = %v, want [%s]", got, test.decision)
			}
			if ids := h.approvals.resolvedIDs(); len(ids) != 1 || ids[0] != "r1" {
				t.Fatalf("resolved IDs = %v, want [r1]", ids)
			}
			tester.Frame()
			h.assertNoTexts(tester, "需要审批", "运行命令")
		})
	}
}

// TestResolvingOldestShowsNext covers FIFO order: answering the oldest request
// opens the next pending one.
func TestResolvingOldestShowsNext(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "bash", "第一个", `{"command":"ls"}`))
	h.approvals.add(pendingRequest("a2", "write_file", "第二个", `{"path":"x"}`))
	tester.Frame()
	h.assertTexts(tester, "第一个")
	h.assertNoTexts(tester, "第二个")

	h.click(tester, "允许")
	tester.Frame()

	if got := h.approvals.resolved(); len(got) != 1 || got[0] != contracts.ApprovalAllow {
		t.Fatalf("decisions after first = %v", got)
	}
	h.assertTexts(tester, "需要审批", "第二个", "write_file")
	h.assertNoTexts(tester, "第一个")
}

// TestEscapeDenies covers the dismissed-by-Escape path: the native Modal
// closes and the module answers the request as deny, so the tool call fails
// clearly instead of hanging.
func TestEscapeDenies(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("e1", "bash", "运行命令", `{"command":"ls"}`))
	tester.Frame()
	h.assertTexts(tester, "需要审批")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	if got := h.approvals.resolved(); len(got) != 1 || got[0] != contracts.ApprovalDeny {
		t.Fatalf("decisions after Escape = %v, want [deny]", got)
	}
	tester.Frame()
	h.assertNoTexts(tester, "需要审批")
}

// TestBackdropClickDenies covers the click-outside path: the backdrop of the
// native Modal dismisses it, which is also a deny.
func TestBackdropClickDenies(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("b1", "bash", "运行命令", `{"command":"ls"}`))
	tester.Frame()
	h.assertTexts(tester, "需要审批")

	// The modal backdrop covers the window; the panel sits in its center.
	tester.ClickAt(2, 2)
	tester.Frame()

	if got := h.approvals.resolved(); len(got) != 1 || got[0] != contracts.ApprovalDeny {
		t.Fatalf("decisions after backdrop click = %v, want [deny]", got)
	}
	tester.Frame()
	h.assertNoTexts(tester, "需要审批")
}

// TestSubscriptionInvalidatesFrames covers the Subscribe -> Post -> Invalidate
// bridge: enqueuing a request from any goroutine schedules the frame that shows
// the dialog.
func TestSubscriptionInvalidatesFrames(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	tester.Frame()
	before := h.uiRoot.Frames()

	h.approvals.add(pendingRequest("s1", "bash", "订阅后出现", `{"command":"ls"}`))
	tester.Frame()

	if after := h.uiRoot.Frames(); after <= before {
		t.Fatalf("frames did not advance after the approvals change: before=%d after=%d", before, after)
	}
	h.assertTexts(tester, "需要审批", "订阅后出现")
	if h.approvals.isUnsubscribed() {
		t.Fatal("approvals subscription was unwound before disposal")
	}
}

// TestDisposeUnregistersContribution covers kernel effect unwinding: with the
// module disposed the slot item and the subscription are gone.
func TestDisposeUnregistersContribution(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("d1", "bash", "运行命令", `{"command":"ls"}`))
	tester.Frame()
	h.assertTexts(tester, "需要审批")

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose approval module: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "需要审批")
	if !h.approvals.isUnsubscribed() {
		t.Fatal("approvals subscription was not unwound on disposal")
	}
	if slices.Contains(h.uiRoot.Runtime().Slots(), contracts.SlotOverlayDialogs) {
		t.Fatalf("overlay.dialogs still has contributions after disposal: %v", h.uiRoot.Runtime().Slots())
	}
}

// TestConfigLabels covers the assembly-config labels.
func TestConfigLabels(t *testing.T) {
	h := newHarness(t, approval.Config{
		Title:            "Approve tool call",
		ToolLabel:        "Tool",
		ArgumentsLabel:   "Arguments",
		AllowLabel:       "Allow once",
		AllowAlwaysLabel: "Always allow",
		DenyLabel:        "Reject",
	})
	tester := h.tester()
	h.approvals.add(pendingRequest("c1", "bash", "run", `{"command":"ls"}`))
	tester.Frame()
	h.assertTexts(tester, "Approve tool call", "Tool", "Arguments", "bash",
		"Allow once", "Always allow", "Reject", "Esc = 拒绝本次调用")
	h.assertNoTexts(tester, "需要审批")
	h.assertNoExactTexts(tester, "允许")
}

// TestLongArgumentsAreTruncated covers the bounded arguments block: a huge
// tool call stays readable and does not render its full payload.
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
	tester.Frame()
	h.assertTexts(tester, "需要审批", "参数", "显示原始参数")
	h.assertNoTexts(tester, `{"data":"`) // collapsed by default
	h.click(tester, "显示原始参数")
	h.assertTexts(tester, `{"data":"`)
	if tester.HasText(string(long)) {
		t.Fatal("the full arguments payload was rendered instead of the truncated block")
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
	} {
		if !required[name] {
			t.Fatalf("module does not require %q; requires = %v", name, descriptor.Requires)
		}
	}
}

// TestFooterOrderAndSafeDefault covers H4: the dangerous deny is the leftmost
// action, the run-scoped allow is secondary in the middle, the accent allow is
// rightmost — and the dialog's safe default focus is Deny, so Enter denies
// instead of allowing a high-risk tool with a stray key.
func TestFooterOrderAndSafeDefault(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("r1", "bash", "运行命令", `{"command":"ls"}`))
	tester.Frame()

	deny, ok := tester.Find("拒绝")
	if !ok {
		t.Fatalf("deny button missing; texts = %v", tester.Texts())
	}
	always, ok := tester.Find("本次运行内始终允许")
	if !ok {
		t.Fatalf("run-scoped allow button missing; texts = %v", tester.Texts())
	}
	allow, ok := tester.Find("允许")
	if !ok {
		t.Fatalf("allow button missing; texts = %v", tester.Texts())
	}
	if !(deny.X < always.X && always.X < allow.X) {
		t.Fatalf("footer order x = deny %.1f, always %.1f, allow %.1f; want deny < always < allow",
			deny.X, always.X, allow.X)
	}
	if !tester.Focused("拒绝") {
		t.Fatalf("safe default focus is not on Deny; texts = %v", tester.Texts())
	}

	// Enter presses the focused button (Deny), never a dialog-wide Allow.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := h.approvals.resolved(); len(got) != 1 || got[0] != contracts.ApprovalDeny {
		t.Fatalf("decisions after Enter = %v, want [deny]", got)
	}
	tester.Frame()
	h.assertNoTexts(tester, "需要审批")

	// Allowing is explicit: a click on Allow records allow.
	h2 := newHarness(t, approval.Config{})
	tester2 := h2.tester()
	h2.approvals.add(pendingRequest("r2", "bash", "运行命令", `{"command":"ls"}`))
	tester2.Frame()
	h2.click(tester2, "允许")
	if got := h2.approvals.resolved(); len(got) != 1 || got[0] != contracts.ApprovalAllow {
		t.Fatalf("decisions after clicking Allow = %v, want [allow]", got)
	}
}
