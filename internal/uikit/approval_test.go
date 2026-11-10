package uikit_test

// Headless frame tests for uikit.ApprovalDialog (AGENT-SURFACES.md S4): the
// product's one approval dialog. They drive the real native controls through
// mygo's in-memory Tester and pin the safety contract — no default focus, a
// stray Enter decides nothing, Escape/backdrop deny, the footer order, the
// queue counter and the bounded raw-arguments disclosure — plus the copy.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
		"需要审批", "工具", "运行命令", "命令", "运行命令 ls", "参数",
		"显示原始参数", "允许", "本会话内始终允许", "拒绝",
		"允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
		"「本会话内始终允许」在本会话内不再询问该工具；可在会话设置菜单清除。",
		"Esc = 拒绝本次调用 · 点击或 Tab 选择操作（无默认操作）",
		"还有 1 个待审批请求",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	// The friendly name is the visible copy; the raw tool id is the tooltip.
	if tester.HasText("bash") {
		t.Fatalf("the raw tool id leaked into the visible copy; texts = %v", tester.Texts())
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
	allow, ok := tester.Find("允许")
	if !ok {
		t.Fatalf("allow button missing; texts = %v", tester.Texts())
	}
	if !(deny.X < always.X && always.X < allow.X) {
		t.Fatalf("footer order x = deny %.1f, always %.1f, allow %.1f; want deny < always < allow",
			deny.X, always.X, allow.X)
	}
	if tester.Focused("拒绝") || tester.Focused("允许") || tester.Focused("本会话内始终允许") {
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
		{label: "允许", decision: contracts.ApprovalAllow},
		{label: "本会话内始终允许", decision: contracts.ApprovalAllowAlways},
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

// TestApprovalDialogBackdropClickDenies covers the click-outside path: the
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
	if tester.HasText("显示原始参数") {
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
	if err := tester.Click("显示原始参数"); err != nil {
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
	for _, want := range []string{"确认执行", "执行", "工具", "参数", "本会话内始终允许", "拒绝"} {
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
		"Approval required", "Tool", "Read file", "Arguments",
		"Allow", "Always allow in this session", "Deny",
		"1 more approval requests waiting",
		"Esc = deny this call",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("read_file") {
		t.Fatalf("the raw tool id leaked into the visible copy; texts = %v", tester.Texts())
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

// TestApprovalSummaryLabelFollowsToolKind covers S-08: only shell/terminal
// calls label their summary 命令; a file read (or other non-command tool) keeps
// the neutral 摘要 so the dialog never says a read/search is a command to run.
func TestApprovalSummaryLabelFollowsToolKind(t *testing.T) {
	_, tester := approvalOpenHarness(t,
		approvalTestRequest("f1", "read_file", "读取 internal/app.go", `{"path":"internal/app.go"}`), 1)
	if !tester.HasText("摘要") {
		t.Fatalf("read_file summary is not labelled 摘要; texts = %v", tester.Texts())
	}
	for _, text := range tester.Texts() {
		if text == "命令" {
			t.Fatalf("read_file summary is labelled 命令; texts = %v", tester.Texts())
		}
	}

	_, bash := approvalOpenHarness(t,
		approvalTestRequest("b1", "bash", "运行命令 ls", `{"command":"ls"}`), 1)
	if !bash.HasText("命令") {
		t.Fatalf("bash summary is not labelled 命令; texts = %v", bash.Texts())
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

// TestApprovalInlineCardRendersBannerAndBody pins the V3 anatomy: the warning
// banner (icon, title, friendly tool, queue counter, Esc hint), the neutral body
// (impact statement, promoted summary, collapsed arguments) and the three
// decisions in the desktop order.
func TestApprovalInlineCardRendersBannerAndBody(t *testing.T) {
	_, tester := approvalInlineHarness(t,
		approvalTestRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`), 2,
		uikit.ApprovalLabels{}, nil)

	for _, want := range []string{
		"主屏幕", "需要审批", "运行命令", "还有 1 条待审批", "Esc 拒绝",
		"允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
		"命令", "运行命令 ls", "参数", "显示原始参数",
		"允许", "本会话内始终允许", "拒绝",
		"「本会话内始终允许」在本会话内不再询问该工具；可在会话设置菜单清除。",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	// The friendly name is the visible copy; the raw tool id is the tooltip, and
	// the raw arguments stay collapsed details.
	if tester.HasText("bash") || tester.HasText(`{"command":"ls"}`) || tester.HasText("参数详情") {
		t.Fatalf("raw tool id/arguments leaked into the frame; texts = %v", tester.Texts())
	}
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
		t.Fatalf("the inline card must not autofocus an action; texts = %v", tester.Texts())
	}
	// The card is one named region for assistive technology.
	if _, ok := tester.AccessByRole(ui.RoleRegion, "需要审批 · 运行命令"); !ok {
		t.Fatalf("the card is not a named region; texts = %v", tester.Texts())
	}
}

// TestApprovalInlineCardDecisions covers the three explicit decisions: each
// button reports its decision through OnResolve.
func TestApprovalInlineCardDecisions(t *testing.T) {
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
		if !tester.HasText("还有 2 条待审批") {
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
		"Approval required", "Read file", "1 more pending approvals", "Esc denies",
		"Arguments", "Allow", "Always allow in this session", "Deny",
		"It runs within the sandbox policy after approval; the policy may still reject the call.",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("read_file") {
		t.Fatalf("the raw tool id leaked into the visible copy; texts = %v", tester.Texts())
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
		if tester.HasText("显示原始参数") {
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
		if err := tester.Click("显示原始参数"); err != nil {
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
	for _, want := range []string{"确认执行", "执行", "参数", "本会话内始终允许", "拒绝"} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("需要审批") {
		t.Fatalf("the default title leaked with a configured one; texts = %v", tester.Texts())
	}
}

// TestApprovalSummaryLabelFollowsToolKindInline covers S-08 for the inline card:
// only shell/terminal calls label their summary 命令.
func TestApprovalSummaryLabelFollowsToolKindInline(t *testing.T) {
	_, read := approvalInlineHarness(t,
		approvalTestRequest("f1", "read_file", "读取 internal/app.go", `{"path":"internal/app.go"}`), 1,
		uikit.ApprovalLabels{}, nil)
	if !read.HasText("摘要") {
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
