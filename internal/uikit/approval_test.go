package uikit_test

// Headless frame tests for uikit.ApprovalDialog (AGENT-SURFACES.md S4): the
// product's one approval dialog. They drive the real native controls through
// mygo's in-memory Tester and pin the safety contract — no default focus, a
// stray Enter decides nothing, Escape/backdrop deny, the footer order, the
// queue counter and the bounded raw-arguments disclosure — plus the copy.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// entryDialogTestLocale is a minimal contracts.LocaleService for the English copy tests.
type entryDialogTestLocale struct{ lang string }

func (l *entryDialogTestLocale) Language() string     { return l.lang }
func (l *entryDialogTestLocale) SetLanguage(v string) { l.lang = v }
func (l *entryDialogTestLocale) T(key string) string  { return key }

// approvalTestState is the caller-owned dialog state of one approval dialog.
type approvalTestState struct {
	open      bool
	decisions []contracts.ApprovalDecision
}

// approvalTestView draws one approval dialog over a plain product frame.
func approvalTestView(state *approvalTestState, o uikit.ApprovalDialogOptions) func(*ui.Context) {
	o.Open = &state.open
	o.OnResolve = func(decision contracts.ApprovalDecision) {
		state.decisions = append(state.decisions, decision)
	}
	return func(c *ui.Context) {
		ui.Text(c, "主屏幕")
		uikit.ApprovalDialog(c, o)
	}
}

// approvalTestRequest builds one pending request.
func approvalTestRequest(id, tool, summary, arguments string) contracts.ApprovalRequest {
	return contracts.ApprovalRequest{ID: id, Tool: tool, Summary: summary, Arguments: arguments}
}

// approvalOpenHarness starts one open approval dialog and returns its tester.
func approvalOpenHarness(t *testing.T, request contracts.ApprovalRequest, pending int) (*approvalTestState, *ui.Tester) {
	t.Helper()
	state := &approvalTestState{open: true}
	tester := ui.NewTester(approvalTestView(state, uikit.ApprovalDialogOptions{
		Request: request,
		Pending: pending,
	}), 1200, 800)
	tester.Frame()
	return state, tester
}

// TestApprovalDialogRendersRequest pins the content anatomy: the branded title,
// the tool as a friendly badge (the raw id stays a tooltip only), the impact
// statement, the summary promoted first, the collapsed arguments and the three
// decisions.
func TestApprovalDialogRendersRequest(t *testing.T) {
	_, tester := approvalOpenHarness(t,
		approvalTestRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`), 2)

	for _, want := range []string{
		"需要审批", "工具", "运行命令", "命令", "运行命令 ls", "原始参数",
		"原始参数", "允许一次", "本会话内始终允许", "拒绝",
		"允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
		"「本会话内始终允许」在本会话内不再询问该工具，会话结束后失效。",
		"Esc = 拒绝本次调用 · 点击或 Tab 选择操作（无默认操作）",
		"还有 1 个待审批请求",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	// The friendly name is the visible copy; the raw protocol id follows it in
	// the mono face, so a reviewer can trace the call (it is no longer a tooltip:
	// a tooltip is an overlay that takes Escape from the surface, A4-I-01).
	if !tester.HasText("bash") {
		t.Fatalf("the raw tool id is not visible; texts = %v", tester.Texts())
	}
	// The raw arguments are collapsed details: no payload before the click and
	// no third "参数详情" layer (N3).
	if tester.HasText(`{"command":"ls"}`) || tester.HasText("参数详情") {
		t.Fatalf("raw arguments/details layer leaked into the frame; texts = %v", tester.Texts())
	}
}

// TestApprovalDialogFooterOrderAndNoDefaultAction pins the H4 safe-decision
// contract: Deny (danger) leftmost, the session-scoped allow in the middle, the
// accent Allow rightmost — and NO action is auto-focused, so a stray Enter
// decides nothing.
func TestApprovalDialogFooterOrderAndNoDefaultAction(t *testing.T) {
	state, tester := approvalOpenHarness(t,
		approvalTestRequest("r1", "bash", "运行命令", `{"command":"ls"}`), 1)

	deny, ok := tester.Find("拒绝")
	if !ok {
		t.Fatalf("deny button missing; texts = %v", tester.Texts())
	}
	always, ok := tester.Find("本会话内始终允许")
	if !ok {
		t.Fatalf("session-scoped allow button missing; texts = %v", tester.Texts())
	}
	allow, ok := tester.Find("允许一次")
	if !ok {
		t.Fatalf("allow button missing; texts = %v", tester.Texts())
	}
	if !(deny.X < always.X && always.X < allow.X) {
		t.Fatalf("footer order x = deny %.1f, always %.1f, allow %.1f; want deny < always < allow",
			deny.X, always.X, allow.X)
	}
	if tester.Focused("拒绝") || tester.Focused("允许一次") || tester.Focused("本会话内始终允许") {
		t.Fatalf("the approval dialog must not autofocus an action; texts = %v", tester.Texts())
	}

	// A stray Enter must not resolve the request in either direction.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(state.decisions) != 0 {
		t.Fatalf("decisions after a stray Enter = %v, want none", state.decisions)
	}
	if !tester.HasText("需要审批") {
		t.Fatalf("a stray Enter closed the dialog; texts = %v", tester.Texts())
	}
}

// TestApprovalDialogDecisionButtons covers the three explicit decisions: each
// resolves with its decision, closes the Modal and leaves no dialog behind.
func TestApprovalDialogDecisionButtons(t *testing.T) {
	cases := []struct {
		label    string
		decision contracts.ApprovalDecision
	}{
		{label: "允许一次", decision: contracts.ApprovalAllow},
		{label: "拒绝", decision: contracts.ApprovalDeny},
	}
	for _, test := range cases {
		t.Run(string(test.decision), func(t *testing.T) {
			state, tester := approvalOpenHarness(t,
				approvalTestRequest("r1", "bash", "运行命令", `{"command":"ls"}`), 1)
			if err := tester.Click(test.label); err != nil {
				t.Fatalf("click %q: %v; texts = %v", test.label, err, tester.Texts())
			}
			tester.Frame()
			if len(state.decisions) != 1 || state.decisions[0] != test.decision {
				t.Fatalf("decisions = %v, want [%s]", state.decisions, test.decision)
			}
			if state.open {
				t.Fatal("the dialog stayed open after an explicit decision")
			}
			if tester.HasText("需要审批") {
				t.Fatalf("the dialog is still rendered after the decision; texts = %v", tester.Texts())
			}
		})
	}
}

// TestApprovalDialogEscapeDenies covers the dismissed-by-Escape path: the
// native Modal closes and the component answers the request as deny, so the
// waiting tool call fails clearly instead of hanging.
func TestApprovalDialogEscapeDenies(t *testing.T) {
	state, tester := approvalOpenHarness(t,
		approvalTestRequest("e1", "bash", "运行命令", `{"command":"ls"}`), 1)

	tester.Key(0, ui.KeyEscape)

	if len(state.decisions) != 1 || state.decisions[0] != contracts.ApprovalDeny {
		t.Fatalf("decisions after Escape = %v, want [deny]", state.decisions)
	}
	if state.open {
		t.Fatal("Escape did not close the Modal")
	}
	if tester.HasText("需要审批") {
		t.Fatalf("the dialog is still rendered after Escape; texts = %v", tester.Texts())
	}
}

// TestApprovalModalEscapeDeniesWithFocusInside pins the Escape contract when the
// keyboard is inside the dialog (A4: the first Escape must decide, wherever the
// focus sits). The native Modal owns the key at its own layer, so a focused
// control inside the dialog does not have to be the one that hears it.
func TestApprovalModalEscapeDeniesWithFocusInside(t *testing.T) {
	state, tester := approvalOpenHarness(t,
		approvalTestRequest("e2", "bash", "运行命令", `{"command":"ls"}`), 1)
	// Put the keyboard inside the dialog: tab to its first action.
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	if tester.Focused("") {
		t.Fatalf("Tab did not move the focus into the dialog; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if len(state.decisions) != 1 || state.decisions[0] != contracts.ApprovalDeny {
		t.Fatalf("Escape with the focus inside the dialog decided %v, want [deny]", state.decisions)
	}
}

// TestApprovalDialogBackdropClickDenies// TestApprovalDialogBackdropClickDenies covers the click-outside path: the
// Modal's backdrop dismisses it, which is also a deny.
func TestApprovalDialogBackdropClickDenies(t *testing.T) {
	state, tester := approvalOpenHarness(t,
		approvalTestRequest("b1", "bash", "运行命令", `{"command":"ls"}`), 1)

	// The modal backdrop covers the window; the panel sits in its center.
	tester.ClickAt(2, 2)

	if len(state.decisions) != 1 || state.decisions[0] != contracts.ApprovalDeny {
		t.Fatalf("decisions after a backdrop click = %v, want [deny]", state.decisions)
	}
	tester.Frame()
	if tester.HasText("需要审批") {
		t.Fatalf("the dialog is still rendered after the backdrop click; texts = %v", tester.Texts())
	}
}

// TestApprovalDialogPendingCount covers the quiet queue counter: it appears only
// when more requests wait behind the shown one.
func TestApprovalDialogPendingCount(t *testing.T) {
	t.Run("single request", func(t *testing.T) {
		_, tester := approvalOpenHarness(t,
			approvalTestRequest("a1", "bash", "运行命令", ""), 1)
		if tester.HasText("待审批请求") {
			t.Fatalf("a single request shows a queue counter; texts = %v", tester.Texts())
		}
	})
	t.Run("queued requests", func(t *testing.T) {
		_, tester := approvalOpenHarness(t,
			approvalTestRequest("a1", "bash", "运行命令", ""), 3)
		if !tester.HasText("还有 2 个待审批请求") {
			t.Fatalf("the queue counter is missing; texts = %v", tester.Texts())
		}
	})
}

// TestApprovalDialogEmptyArguments covers the "no arguments" fallback instead
// of an empty box when the call carries {} / [] / null.
func TestApprovalDialogEmptyArguments(t *testing.T) {
	_, tester := approvalOpenHarness(t,
		approvalTestRequest("a1", "read_file", "读取配置", "{}"), 1)
	if !tester.HasText("无参数") {
		t.Fatalf("the no-arguments fallback is missing; texts = %v", tester.Texts())
	}
	if tester.HasText("原始参数") {
		t.Fatalf("an empty argument block still renders a disclosure; texts = %v", tester.Texts())
	}
}

// TestApprovalDialogLongArgumentsAreTruncated covers the bounded arguments
// block: the full payload is never rendered, and the truncated block appears
// only after the disclosure is opened.
func TestApprovalDialogLongArgumentsAreTruncated(t *testing.T) {
	var long strings.Builder
	long.WriteString(`{"data":"`)
	for long.Len() < 8000 {
		long.WriteByte('x')
	}
	long.WriteString(`"}`)
	payload := long.String()

	_, tester := approvalOpenHarness(t,
		approvalTestRequest("l1", "bash", "大参数", payload), 1)
	if tester.HasText(`{"data":"`) {
		t.Fatalf("the arguments disclosure starts open; texts = %v", tester.Texts())
	}
	if err := tester.Click("原始参数"); err != nil {
		t.Fatalf("open the arguments disclosure: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText(`{"data":"`) {
		t.Fatalf("the truncated arguments did not render; texts = %v", tester.Texts())
	}
	if tester.HasText(payload) {
		t.Fatal("the full arguments payload was rendered instead of the truncated block")
	}
}

// TestApprovalDialogConfiguredLabels covers the configurable copy: a custom
// label is shown verbatim and the untouched defaults keep their product copy.
func TestApprovalDialogConfiguredLabels(t *testing.T) {
	state := &approvalTestState{open: true}
	tester := ui.NewTester(approvalTestView(state, uikit.ApprovalDialogOptions{
		Request: approvalTestRequest("c1", "bash", "run", `{"command":"ls"}`),
		Pending: 1,
		Labels: uikit.ApprovalLabels{
			Title:      "确认执行",
			AllowLabel: "执行",
			DenyLabel:  "拒绝",
		},
	}), 1200, 800)
	tester.Frame()
	for _, want := range []string{"确认执行", "执行", "工具", "原始参数", "本会话内始终允许", "拒绝"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("需要审批") {
		t.Fatalf("the default title leaked with a configured one; texts = %v", tester.Texts())
	}
}

// TestApprovalDialogEnglishCopy covers the localized defaults of the component
// itself: the labels, the queue counter, the impact statement and the hints.
func TestApprovalDialogEnglishCopy(t *testing.T) {
	state := &approvalTestState{open: true}
	tester := ui.NewTester(approvalTestView(state, uikit.ApprovalDialogOptions{
		Request: approvalTestRequest("a1", "read_file", "读取配置", `{"path":"x"}`),
		Pending: 2,
		Locale:  &entryDialogTestLocale{lang: "en"},
	}), 1200, 800)
	tester.Frame()
	for _, want := range []string{
		"Approval required", "Tool", "Read file", "Raw arguments",
		"Allow once", "Always allow in this session", "Deny",
		"1 more approval requests waiting",
		"Esc = deny this call",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if !tester.HasText("read_file") {
		t.Fatalf("the raw tool id is not visible; texts = %v", tester.Texts())
	}
}

// TestApprovalDialogClosedRendersNothing covers the open flag: a closed dialog
// contributes no controls at all (in particular no empty Modal).
func TestApprovalDialogClosedRendersNothing(t *testing.T) {
	state := &approvalTestState{open: false}
	tester := ui.NewTester(approvalTestView(state, uikit.ApprovalDialogOptions{
		Request: approvalTestRequest("a1", "bash", "运行命令", `{"command":"ls"}`),
	}), 1200, 800)
	tester.Frame()
	if !tester.HasText("主屏幕") {
		t.Fatalf("the page behind the dialog did not render; texts = %v", tester.Texts())
	}
	if tester.HasText("需要审批") || tester.HasText("拒绝") {
		t.Fatalf("a closed approval dialog rendered controls; texts = %v", tester.Texts())
	}
}

// TestApprovalSummaryLabelFollowsToolKind covers the target block's semantic
// label: a command line, a file path, a URL and a query are read differently, so
// the block says which one it holds instead of labelling every payload 摘要, and
// a tool with no such shape (a team task) keeps the neutral 摘要.
func TestApprovalSummaryLabelFollowsToolKind(t *testing.T) {
	cases := []struct {
		tool, summary, arguments, label string
	}{
		{"bash", "运行命令 ls", `{"command":"ls"}`, "命令"},
		{"read_file", "读取 internal/app.go", `{"path":"internal/app.go"}`, "路径"},
		{"web_fetch", "抓取 example.com", `{"url":"https://example.com"}`, "域名"},
		{"web_search", "搜索 zeokeel", `{"query":"zeokeel"}`, "查询"},
		{"team_task_create", "创建任务", `{"subject":"发布"}`, "摘要"},
	}
	for _, test := range cases {
		t.Run(test.tool, func(t *testing.T) {
			_, tester := approvalOpenHarness(t,
				approvalTestRequest("t1", test.tool, test.summary, test.arguments), 1)
			if !tester.HasText(test.label) {
				t.Fatalf("%s summary is not labelled %s; texts = %v",
					test.tool, test.label, tester.Texts())
			}
			for _, text := range tester.Texts() {
				if text == "命令" && test.label != "命令" {
					t.Fatalf("%s summary is labelled 命令; texts = %v", test.tool, tester.Texts())
				}
			}
		})
	}
}

// --- inline card (the product path) ----------------------------------------

// approvalInlineHarness starts one inline approval card over a plain product
// frame. Open is deliberately nil: the inline path never uses the Modal open
// flag, and Escape/backdrop are not the component's to handle.
func approvalInlineHarness(t *testing.T, request contracts.ApprovalRequest, pending int,
	labels uikit.ApprovalLabels, locale contracts.LocaleService) (*approvalTestState, *ui.Tester) {
	t.Helper()
	state := &approvalTestState{}
	options := uikit.ApprovalDialogOptions{
		Request: request,
		Pending: pending,
		Labels:  labels,
		Inline:  true,
		Locale:  locale,
		OnResolve: func(decision contracts.ApprovalDecision) {
			state.decisions = append(state.decisions, decision)
		},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Text(c, "主屏幕")
		uikit.ApprovalDialog(c, options)
	}, 1200, 800)
	tester.Frame()
	return state, tester
}

// TestApprovalInlineCardAnatomy pins the card's B-spec anatomy: the status badge
// and the tool in the header, the queue counter, the impact line, the semantic
// target block, the collapsed raw arguments, the key-cap hints and the decision
// bar (Deny · the one accent Allow · the scope menu). The warning color appears
// only as the rail, the badge and the impact sentence — never as a full-width
// banner (VD-08).
func TestApprovalInlineCardAnatomy(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`), 2,
		uikit.ApprovalLabels{}, nil)

	for _, want := range []string{
		"主屏幕", "需要审批", "运行命令", "还有 1 条",
		"允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
		"命令", "运行命令 ls", "原始参数",
		"Esc", "Ctrl+Enter", "拒绝本次", "按所选范围允许",
		"范围：仅此一次", "拒绝", "允许一次", "更多",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	// The session-scoped allow is a scope choice inside the menu, not a second
	// accent-styled button: the screen may hold exactly one primary action.
	if tester.HasText("本会话内始终允许") {
		t.Fatalf("the session-scoped allow is still a footer button; texts = %v", tester.Texts())
	}
	// The raw protocol id is visible mono copy; the raw arguments stay collapsed.
	if !tester.HasText("bash") || tester.HasText(`{"command":"ls"}`) || tester.HasText("参数详情") {
		t.Fatalf("raw id/arguments are wrong in the frame; texts = %v", tester.Texts())
	}
	deny, ok := tester.Find("拒绝")
	if !ok {
		t.Fatalf("deny action missing; texts = %v", tester.Texts())
	}
	allow, ok := tester.Find("允许一次")
	if !ok {
		t.Fatalf("allow action missing; texts = %v", tester.Texts())
	}
	if !(deny.X < allow.X) {
		t.Fatalf("footer order x = deny %.1f, allow %.1f; want deny < allow", deny.X, allow.X)
	}
	if tester.Focused("拒绝") || tester.Focused("允许一次") {
		t.Fatalf("the inline card must not autofocus an action; texts = %v", tester.Texts())
	}
	// The card is one named region for assistive technology.
	if _, ok := tester.AccessByRole(ui.RoleRegion, "需要审批 · 运行命令"); !ok {
		t.Fatalf("the card is not a named region; texts = %v", tester.Texts())
	}
}

// TestApprovalInlineScopeHintUsesMetadataStep pins the impact sentence's type
// step: it explains what approving means and it influences the decision, so it is
// metadata (TypeXXS, 12/18), never the 11px micro-label step (UD-05).
func TestApprovalInlineScopeHintUsesMetadataStep(t *testing.T) {
	const hint = "允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。"
	_, inline := approvalInlineHarness(t,
		approvalTestRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`), 1,
		uikit.ApprovalLabels{}, nil)
	rect, ok := inline.Find(hint)
	if !ok {
		t.Fatalf("inline impact statement missing; texts = %v", inline.Texts())
	}
	_, line, _ := design.TypeMetrics(design.TypeXXS)
	if rect.H < line {
		t.Errorf("inline impact statement line box = %v, want ≥%v (TypeXXS metadata step; TypeXXXS is micro-labels only, UD-05)",
			rect.H, line)
	}
	_, modal := approvalOpenHarness(t,
		approvalTestRequest("a2", "bash", "运行命令 ls", `{"command":"ls"}`), 1)
	mrect, ok := modal.Find(hint)
	if !ok {
		t.Fatalf("modal impact statement missing; texts = %v", modal.Texts())
	}
	if rect.H != mrect.H {
		t.Errorf("inline impact line box %v differs from the Modal path's %v (one sentence, one type step)",
			rect.H, mrect.H)
	}
}

// TestApprovalInlineScopeMenuChoosesTheDecision pins the scope menu: choosing
// "this session" relabels the single accent action and states the scope beside
// it, and the action then resolves as AllowAlways; the one-call scope resolves as
// Allow. The menu keeps one accent fill on screen at every point.
func TestApprovalInlineScopeMenuChoosesTheDecision(t *testing.T) {
	state, tester := approvalInlineHarness(t,
		approvalTestRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`), 1,
		uikit.ApprovalLabels{}, nil)
	if err := tester.Click("更多"); err != nil {
		t.Fatalf("click the scope menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("本会话内始终允许该工具"); err != nil {
		t.Fatalf("choose the session scope: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("范围：本会话") {
		t.Fatalf("the scope sentence does not follow the choice; texts = %v", tester.Texts())
	}
	allow, ok := tester.Find("本会话内始终允许")
	if !ok {
		t.Fatalf("the allow action did not follow the scope; texts = %v", tester.Texts())
	}
	if allow.X <= 0 {
		t.Fatalf("allow action x = %v", allow.X)
	}
	if err := tester.Click("本会话内始终允许"); err != nil {
		t.Fatalf("click the scoped allow: %v; texts = %v", err, tester.Texts())
	}
	if len(state.decisions) != 1 || state.decisions[0] != contracts.ApprovalAllowAlways {
		t.Fatalf("decisions = %v, want [%s]", state.decisions, contracts.ApprovalAllowAlways)
	}
}

// TestApprovalInlineKeyboardAllowsOnce pins the explicit chord: CmdOrCtrl+Enter
// allows at the selected scope, and plain Enter decides nothing (the user presses
// it right after sending a prompt).
func TestApprovalInlineKeyboardAllowsOnce(t *testing.T) {
	state, tester := approvalInlineHarness(t,
		approvalTestRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`), 1,
		uikit.ApprovalLabels{}, nil)
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(state.decisions) != 0 {
		t.Fatalf("a stray Enter decided %v; want nothing", state.decisions)
	}
	// The chord belongs to the card's focus subtree (it must not fire while the
	// user types elsewhere with the same chord in mind), so tab into the card.
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyEnter)
	tester.Frame()
	if len(state.decisions) != 1 || state.decisions[0] != contracts.ApprovalAllow {
		t.Fatalf("decisions = %v, want [%s]", state.decisions, contracts.ApprovalAllow)
	}
}

// TestApprovalInlineRulesDisclosure pins the menu's "show rules" entry: the
// always-allow explanation is available on demand instead of being a permanent
// footnote under the decision.
func TestApprovalInlineRulesDisclosure(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`), 1,
		uikit.ApprovalLabels{}, nil)
	const rule = "「本会话内始终允许」在本会话内不再询问 运行命令，会话结束后失效。"
	if tester.HasText(rule) {
		t.Fatalf("the rules copy is visible without asking for it; texts = %v", tester.Texts())
	}
	if err := tester.Click("更多"); err != nil {
		t.Fatalf("click the scope menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("查看规则"); err != nil {
		t.Fatalf("open the rules: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("会话结束后失效") {
		t.Fatalf("the rules do not explain the scope; texts = %v", tester.Texts())
	}
}

// TestApprovalInlineCardDecisions covers the two direct decisions: each button
// reports its decision through OnResolve. The session-scoped allow is reached
// through the scope menu (TestApprovalInlineScopeMenuChoosesTheDecision).
func TestApprovalInlineCardDecisions(t *testing.T) {
	cases := []struct {
		label    string
		decision contracts.ApprovalDecision
	}{
		{label: "允许一次", decision: contracts.ApprovalAllow},
		{label: "拒绝", decision: contracts.ApprovalDeny},
	}
	for _, test := range cases {
		t.Run(string(test.decision), func(t *testing.T) {
			state, tester := approvalInlineHarness(t,
				approvalTestRequest("r1", "bash", "运行命令", `{"command":"ls"}`), 1,
				uikit.ApprovalLabels{}, nil)
			if err := tester.Click(test.label); err != nil {
				t.Fatalf("click %q: %v; texts = %v", test.label, err, tester.Texts())
			}
			tester.Frame()
			if len(state.decisions) != 1 || state.decisions[0] != test.decision {
				t.Fatalf("decisions = %v, want [%s]", state.decisions, test.decision)
			}
		})
	}
}

// TestApprovalInlineCardNoKeyboardDismissal covers the contract split: the
// component handles no keyboard shortcut (Esc does not decide, and there is no
// backdrop to click — the inline card is not a Modal). The session screen owns
// Escape.
func TestApprovalInlineCardNoKeyboardDismissal(t *testing.T) {
	state, tester := approvalInlineHarness(t,
		approvalTestRequest("e1", "bash", "运行命令", `{"command":"ls"}`), 1,
		uikit.ApprovalLabels{}, nil)
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.ClickAt(2, 2)
	tester.Frame()
	if len(state.decisions) != 0 {
		t.Fatalf("the inline card resolved without an explicit click: %v", state.decisions)
	}
	if !tester.HasText("需要审批") {
		t.Fatalf("the inline card disappeared without a decision; texts = %v", tester.Texts())
	}
}

// TestApprovalInlineCardPendingCount covers the banner counter: it appears only
// when more requests wait behind the shown one.
func TestApprovalInlineCardPendingCount(t *testing.T) {
	t.Run("single request", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("a1", "bash", "运行命令", ""), 1,
			uikit.ApprovalLabels{}, nil)
		if tester.HasText("待审批") {
			t.Fatalf("a single request shows a queue counter; texts = %v", tester.Texts())
		}
	})
	t.Run("queued requests", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("a1", "bash", "运行命令", ""), 3,
			uikit.ApprovalLabels{}, nil)
		if !tester.HasText("还有 2 条") {
			t.Fatalf("the queue counter is missing; texts = %v", tester.Texts())
		}
	})
}

// TestApprovalInlineCardEnglishCopy covers the localized defaults of the inline
// banner and body.
func TestApprovalInlineCardEnglishCopy(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("a1", "read_file", "读取配置", `{"path":"x"}`), 2,
		uikit.ApprovalLabels{}, &entryDialogTestLocale{lang: "en"})
	for _, want := range []string{
		"Approval required", "Read file", "1 more", "denies this call",
		"Raw arguments", "Allow once", "One call", "Deny", "More",
		"It runs within the sandbox policy after approval; the policy may still reject the call.",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if !tester.HasText("read_file") {
		t.Fatalf("the raw tool id is not visible; texts = %v", tester.Texts())
	}
}

// TestApprovalInlineCardArguments covers the arguments section: the empty
// fallback and the bounded disclosure of a huge payload.
func TestApprovalInlineCardArguments(t *testing.T) {
	t.Run("no arguments", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("a1", "read_file", "读取配置", "{}"), 1,
			uikit.ApprovalLabels{}, nil)
		if !tester.HasText("无参数") {
			t.Fatalf("the no-arguments fallback is missing; texts = %v", tester.Texts())
		}
		if tester.HasText("原始参数") {
			t.Fatalf("an empty argument block still renders a disclosure; texts = %v", tester.Texts())
		}
	})
	t.Run("long arguments", func(t *testing.T) {
		var long strings.Builder
		long.WriteString(`{"data":"`)
		for long.Len() < 8000 {
			long.WriteByte('x')
		}
		long.WriteString(`"}`)
		payload := long.String()
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("l1", "bash", "大参数", payload), 1,
			uikit.ApprovalLabels{}, nil)
		if tester.HasText(`{"data":"`) {
			t.Fatalf("the arguments disclosure starts open; texts = %v", tester.Texts())
		}
		if err := tester.Click("原始参数"); err != nil {
			t.Fatalf("open the arguments disclosure: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if !tester.HasText(`{"data":"`) {
			t.Fatalf("the truncated arguments did not render; texts = %v", tester.Texts())
		}
		if tester.HasText(payload) {
			t.Fatal("the full arguments payload was rendered instead of the truncated block")
		}
	})
}

// TestApprovalInlineCardConfiguredLabels covers the configurable copy on the
// inline banner and footer.
func TestApprovalInlineCardConfiguredLabels(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("c1", "bash", "run", `{"command":"ls"}`), 1,
		uikit.ApprovalLabels{
			Title:      "确认执行",
			AllowLabel: "执行",
			DenyLabel:  "拒绝",
		}, nil)
	for _, want := range []string{"确认执行", "执行", "原始参数", "拒绝", "更多"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("需要审批") {
		t.Fatalf("the default title leaked with a configured one; texts = %v", tester.Texts())
	}
}

// TestApprovalSummaryLabelFollowsToolKindInline covers the target block's
// semantic label for the inline card: a file read is a path, a shell call a
// command, and the card never calls a read a command to run.
func TestApprovalSummaryLabelFollowsToolKindInline(t *testing.T) {
	_, read := approvalInlineHarness(t,
		approvalTestRequest("f1", "read_file", "读取 internal/app.go", `{"path":"internal/app.go"}`), 1,
		uikit.ApprovalLabels{}, nil)
	if !read.HasText("路径") {
		t.Fatalf("read_file summary is not labelled 摘要; texts = %v", read.Texts())
	}
	for _, text := range read.Texts() {
		if text == "命令" {
			t.Fatalf("read_file summary is labelled 命令; texts = %v", read.Texts())
		}
	}

	_, bash := approvalInlineHarness(t,
		approvalTestRequest("b1", "bash", "运行命令 ls", `{"command":"ls"}`), 1,
		uikit.ApprovalLabels{}, nil)
	if !bash.HasText("命令") {
		t.Fatalf("bash summary is not labelled 命令; texts = %v", bash.Texts())
	}
}

// countApprovalText counts the frame nodes exactly equal to text.
func countApprovalText(tester *ui.Tester, text string) int {
	count := 0
	for _, label := range tester.Texts() {
		if label == text {
			count++
		}
	}
	return count
}

// TestApprovalSummaryFallbackFromArguments covers R5: a request whose tool
// reported no summary still gets a readable mono preview of its first key/value
// pairs (task→subject/owner, shell→command), and the raw arguments have exactly
// one disclosure label.
func TestApprovalSummaryFallbackFromArguments(t *testing.T) {
	t.Run("team task", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("f1", "team_task_create", "",
				`{"subject":"更新发布说明","owner":"researcher","description":"补充发布检查项"}`),
			1, uikit.ApprovalLabels{}, nil)
		for _, want := range []string{"摘要", "subject: 更新发布说明", "owner: researcher"} {
			if !tester.HasText(want) {
				t.Fatalf("argument fallback does not show %q; texts = %v", want, tester.Texts())
			}
		}
		if got := countApprovalText(tester, "原始参数"); got != 1 {
			t.Fatalf("raw arguments disclosures = %d, want exactly one; texts = %v", got, tester.Texts())
		}
	})

	t.Run("shell command", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("f2", "bash", "", `{"command":"git status --short"}`),
			1, uikit.ApprovalLabels{}, nil)
		for _, want := range []string{"命令", "command: git status --short"} {
			if !tester.HasText(want) {
				t.Fatalf("command fallback does not show %q; texts = %v", want, tester.Texts())
			}
		}
	})

	t.Run("unparseable arguments keep a bounded preview", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("f3", "mcp__srv__lookup", "", "not-json-arguments"),
			1, uikit.ApprovalLabels{}, nil)
		if !tester.HasText("摘要") || !tester.HasText("not-json-arguments") {
			t.Fatalf("unparseable arguments have no summary preview; texts = %v", tester.Texts())
		}
	})
}

// --- change preview on the approval card ------------------------------------

// TestApprovalInlineCardShowsWriteDiff covers the write_file shape: the card
// shows the "将改动" block between the summary and the raw arguments, with the
// path, the +N −M counts and the official unified diff of the content the
// arguments carry (the approval never reads the file).
func TestApprovalInlineCardShowsWriteDiff(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("w1", "write_file", "写入 internal/app.go",
			`{"path":"internal/app.go","content":"package main\n\nfunc main() {}\n"}`),
		1, uikit.ApprovalLabels{}, nil)
	for _, want := range []string{"将改动", "internal/app.go", "+3", "−0"} {
		if !tester.HasText(want) {
			t.Fatalf("the change block does not show %q; texts = %v", want, tester.Texts())
		}
	}
	info, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the change block is not exposed as the diff's new text; texts = %v", tester.Texts())
	}
	if !strings.Contains(info.Value, "func main()") {
		t.Fatalf("the diff does not show the written content; a11y value = %q", info.Value)
	}
	// A′ (2026-11 UI review P1): the sandbox impact disclosure lives in the fixed
	// banner (always visible), and the diff's first hunk is revealed into the
	// bounded body on the first frame instead of sitting below the summary's fold.
	title, okTitle := tester.Find("需要审批")
	disclosure, okDisclosure := tester.Find("允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。")
	change, okChange := tester.Find("将改动")
	allow, okAllow := tester.Find("允许一次")
	if !okTitle || !okDisclosure || !okChange || !okAllow {
		t.Fatalf("banner/disclosure/change/footer missing; texts = %v", tester.Texts())
	}
	// The impact statement stays visible above the scrolling body, on the line
	// below the header.
	if disclosure.H <= 0 || !(title.Y < disclosure.Y) {
		t.Fatalf("the sandbox disclosure is not always visible in the banner: title=%+v disclosure=%+v", title, disclosure)
	}
	// The diff region keeps its readable floor (≥128 DIP) and is revealed below
	// the banner — not a half-line squeezed by the summary.
	// The viewer's own 4 DIP padding sits inside its floor, so the code text's
	// measured height is the floor less that padding.
	if info.Bounds.H < 120 {
		t.Fatalf("the diff region spans %.1f DIP, want the ≥128 readable floor", info.Bounds.H)
	}
	if !(disclosure.Y < info.Bounds.Y) {
		t.Fatalf("the diff is not revealed below the banner disclosure: disclosure.Y=%.1f diff.Y=%.1f", disclosure.Y, info.Bounds.Y)
	}
	// The change block is revealed inside the bounded body: its label is in the
	// viewport, and the diff's first line sits below it — complete, not clipped in
	// half.
	if change.H <= 0 {
		t.Fatalf("the change label is clipped out of the body viewport: %+v", change)
	}
	if info.Bounds.Y < change.Y {
		t.Fatalf("the diff first line is clipped above the body viewport: diff.Y=%.1f label.Y=%.1f", info.Bounds.Y, change.Y)
	}
	// The card stays a bounded block (UX-02): header + bounded middle + footer
	// span one card that fits the session dock's minimum-window budget — the
	// middle section is what may scroll, never the window.
	if span := allow.Y + allow.H - title.Y; span > 400 {
		t.Fatalf("the approval card spans %.1f DIP, want a bounded card (≤400)", span)
	}
}

// TestApprovalInlineCardShowsEditDiff covers the edit_file shape: the old_string
// → new_string pair becomes the unified diff, counted as one addition and one
// deletion, with the path in the header.
func TestApprovalInlineCardShowsEditDiff(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("e1", "edit_file", "编辑 internal/app.go",
			`{"path":"internal/app.go","old_string":"old line\n","new_string":"new line\n"}`),
		1, uikit.ApprovalLabels{}, nil)
	for _, want := range []string{"将改动", "internal/app.go", "+1", "−1"} {
		if !tester.HasText(want) {
			t.Fatalf("the change block does not show %q; texts = %v", want, tester.Texts())
		}
	}
	info, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the change block is not exposed as the diff's new text; texts = %v", tester.Texts())
	}
	if !strings.Contains(info.Value, "new line") {
		t.Fatalf("the diff does not show the new line; a11y value = %q", info.Value)
	}
}

// TestApprovalInlineCardShowsEditorCreateDiff covers str_replace_editor's create
// command: the file_text is the whole new file, so the block is an all-added
// diff like write_file.
func TestApprovalInlineCardShowsEditorCreateDiff(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("c1", "str_replace_editor", "创建 notes.txt",
			`{"command":"create","path":"notes.txt","file_text":"one\ntwo\n"}`),
		1, uikit.ApprovalLabels{}, nil)
	for _, want := range []string{"将改动", "notes.txt", "+2"} {
		if !tester.HasText(want) {
			t.Fatalf("the change block does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

// TestApprovalInlineCardShowsEditorReplaceDiff covers str_replace_editor's
// str_replace command and its old_str/new_str argument names.
func TestApprovalInlineCardShowsEditorReplaceDiff(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("r1", "str_replace_editor", "替换坐标",
			`{"command":"str_replace","path":"view.go","old_str":"x := 1\n","new_str":"x := 2\n"}`),
		1, uikit.ApprovalLabels{}, nil)
	for _, want := range []string{"将改动", "view.go", "+1", "−1"} {
		if !tester.HasText(want) {
			t.Fatalf("the change block does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

// TestApprovalInlineCardChangeFallbacks covers the honest fallbacks: arguments
// the card cannot turn into a diff explain themselves instead of showing a
// blank or fake diff, and calls with nothing to show omit the block.
func TestApprovalInlineCardChangeFallbacks(t *testing.T) {
	t.Run("unparseable arguments", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("u1", "write_file", "写入文件", "not-json-arguments"),
			1, uikit.ApprovalLabels{}, nil)
		if !tester.HasText("将改动") || !tester.HasText("无法解析参数") {
			t.Fatalf("the unparseable-arguments fallback is missing; texts = %v", tester.Texts())
		}
		if !tester.HasText("not-json-arguments") {
			t.Fatalf("the unparseable-arguments fallback has no bounded preview; texts = %v", tester.Texts())
		}
		if _, ok := tester.Access("New text"); ok {
			t.Fatal("unparseable arguments were rendered as a diff")
		}
	})

	t.Run("insert cannot be diffed from arguments", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("i1", "str_replace_editor", "插入代码",
				`{"command":"insert","path":"view.go","insert_line":4,"new_str":"inserted line\n"}`),
			1, uikit.ApprovalLabels{}, nil)
		if !tester.HasText("将改动") || !tester.HasText("插入位置") {
			t.Fatalf("the insert fallback has no honest explanation; texts = %v", tester.Texts())
		}
		if !tester.HasText("inserted line") {
			t.Fatalf("the insert fallback has no preview of the inserted text; texts = %v", tester.Texts())
		}
	})

	t.Run("view command has no block", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("v1", "str_replace_editor", "查看文件",
				`{"command":"view","path":"view.go"}`),
			1, uikit.ApprovalLabels{}, nil)
		if tester.HasText("将改动") {
			t.Fatalf("a read-only view command rendered a change block; texts = %v", tester.Texts())
		}
	})

	t.Run("empty arguments have no block", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("n1", "write_file", "写入文件", "{}"),
			1, uikit.ApprovalLabels{}, nil)
		if tester.HasText("将改动") {
			t.Fatalf("empty arguments rendered a change block; texts = %v", tester.Texts())
		}
	})

	t.Run("incomplete arguments have no block", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("n2", "write_file", "写入文件", `{"path":"x"}`),
			1, uikit.ApprovalLabels{}, nil)
		if tester.HasText("将改动") {
			t.Fatalf("arguments without content rendered a change block; texts = %v", tester.Texts())
		}
	})

	t.Run("unknown tool has no block", func(t *testing.T) {
		_, tester := approvalInlineHarness(t,
			approvalTestRequest("n3", "bash", "运行命令", `{"command":"ls"}`),
			1, uikit.ApprovalLabels{}, nil)
		if tester.HasText("将改动") {
			t.Fatalf("a non-file tool rendered a change block; texts = %v", tester.Texts())
		}
	})
}

// TestApprovalInlineCardOversizeChangeFallsBack covers the size guard: a huge
// write is summarized with a bounded mono preview and the honest reason instead
// of trying to diff it inside the card.
func TestApprovalInlineCardOversizeChangeFallsBack(t *testing.T) {
	var content strings.Builder
	for index := 0; index <= uikit.DiffPreviewMaxLines; index++ {
		content.WriteString("generated line x\n")
	}
	arguments := `{"path":"generated.txt","content":` + quoteApprovalJSON(t, content.String()) + `}`
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("o1", "write_file", "写入 generated.txt", arguments),
		1, uikit.ApprovalLabels{}, nil)
	if _, ok := tester.Access("New text"); ok {
		t.Fatal("an oversized write was rendered as a diff")
	}
	if !tester.HasText("改动过大") || !tester.HasText("无法生成 diff") {
		t.Fatalf("the oversized change has no honest explanation; texts = %v", tester.Texts())
	}
}

// TestApprovalInlineCardChangeKeepsSafeDecision covers the unchanged safety
// contract with a change block present: the footer order stays deny → always →
// allow, nothing is auto-focused, a stray Enter decides nothing and the inline
// card still leaves Escape to the session screen.
func TestApprovalInlineCardChangeKeepsSafeDecision(t *testing.T) {
	state, tester := approvalInlineHarness(t,
		approvalTestRequest("s1", "write_file", "写入文件",
			`{"path":"internal/app.go","content":"package main\n"}`),
		1, uikit.ApprovalLabels{}, nil)
	deny, ok := tester.Find("拒绝")
	if !ok {
		t.Fatalf("deny action missing; texts = %v", tester.Texts())
	}
	allow, ok := tester.Find("允许一次")
	if !ok {
		t.Fatalf("allow action missing; texts = %v", tester.Texts())
	}
	if !(deny.X < allow.X) {
		t.Fatalf("footer order x = deny %.1f, allow %.1f; want deny < allow", deny.X, allow.X)
	}
	if tester.Focused("拒绝") || tester.Focused("允许一次") {
		t.Fatalf("the card autofocused an action next to the change block; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(state.decisions) != 0 {
		t.Fatalf("a stray Enter decided with a change block present: %v", state.decisions)
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if len(state.decisions) != 0 {
		t.Fatalf("the diff block made the inline card handle Escape: %v", state.decisions)
	}
}

// TestApprovalInlineCardChangeFitsNarrowCard covers the width contract: the
// change block stays inside a 320 DIP window (the card's narrow share) and does
// not push the decisions out of reach.
func TestApprovalInlineCardChangeFitsNarrowCard(t *testing.T) {
	state := &approvalTestState{}
	arguments, err := json.Marshal(map[string]string{
		"path":    "internal/a-very-long-file-name-with-many-segments.go",
		"content": strings.Repeat("long content line xxxxxxxxxxxxxxxxxxxxxxxxxxxx\n", 3),
	})
	if err != nil {
		t.Fatalf("encode narrow-card arguments: %v", err)
	}
	options := uikit.ApprovalDialogOptions{
		Request: approvalTestRequest("narrow1", "write_file", "写入文件", string(arguments)),
		Pending: 1,
		Inline:  true,
		OnResolve: func(decision contracts.ApprovalDecision) {
			state.decisions = append(state.decisions, decision)
		},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Text(c, "主屏幕")
		uikit.ApprovalDialog(c, options)
	}, 320, 700)
	tester.Frame()
	info, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the narrow card has no diff; texts = %v", tester.Texts())
	}
	if right := info.Bounds.X + info.Bounds.W; right > 320.5 || info.Bounds.X < -0.5 {
		t.Fatalf("the diff spans x=%g..%g, want inside 0..320", info.Bounds.X, right)
	}
	for _, label := range []string{"拒绝", "允许一次", "更多"} {
		if _, ok := tester.Find(label); !ok {
			t.Fatalf("the narrow card hides %q behind the diff; texts = %v", label, tester.Texts())
		}
	}
	if err := tester.Click("拒绝"); err != nil {
		t.Fatalf("click deny in the narrow card: %v; texts = %v", err, tester.Texts())
	}
	if len(state.decisions) != 1 || state.decisions[0] != contracts.ApprovalDeny {
		t.Fatalf("decisions = %v, want [deny]", state.decisions)
	}
}

// TestApprovalDialogShowsWriteDiff covers the Modal presentation: it shares the
// card's anatomy, so the change block is there too, and Escape still denies.
func TestApprovalDialogShowsWriteDiff(t *testing.T) {
	state, tester := approvalOpenHarness(t,
		approvalTestRequest("m1", "write_file", "写入 internal/app.go",
			`{"path":"internal/app.go","content":"package main\n"}`), 1)
	for _, want := range []string{"将改动", "internal/app.go", "+1"} {
		if !tester.HasText(want) {
			t.Fatalf("the modal change block does not show %q; texts = %v", want, tester.Texts())
		}
	}
	tester.Key(0, ui.KeyEscape)
	if len(state.decisions) != 1 || state.decisions[0] != contracts.ApprovalDeny {
		t.Fatalf("decisions after Escape = %v, want [deny]", state.decisions)
	}
	if state.open {
		t.Fatal("Escape did not close the Modal")
	}
}

// TestApprovalInlineCardChangeLabelEnglish covers the localized block label.
func TestApprovalInlineCardChangeLabelEnglish(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("en1", "write_file", "Write file",
			`{"path":"notes.txt","content":"hello\n"}`),
		1, uikit.ApprovalLabels{}, &entryDialogTestLocale{lang: "en"})
	for _, want := range []string{"Changes", "notes.txt", "+1"} {
		if !tester.HasText(want) {
			t.Fatalf("the English change block does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

// quoteApprovalJSON renders s as a JSON string literal, for the huge-arguments
// test whose payload is generated rather than written by hand.
func quoteApprovalJSON(t *testing.T, s string) string {
	t.Helper()
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("encode JSON string: %v", err)
	}
	return string(encoded)
}
