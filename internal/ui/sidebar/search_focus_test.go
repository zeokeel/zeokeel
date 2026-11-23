package sidebar_test

// Frame tests for the search-hit handoff (P2-4) and the single-selection rule on
// non-conversation screens (P2-7): clicking a message hit opens its conversation
// and hands the message locator to the optional contracts.SessionFocus service,
// while an Insights/Automation screen leaves the active conversation row
// unhighlighted (the screen destination is the sidebar's only selection).

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// focusCall is one contract call recorded by fakeFocus.
type focusCall struct {
	sessionID string
	messageID string
}

// fakeFocus implements contracts.SessionFocus with call accounting.
type fakeFocus struct {
	calls []focusCall
}

func (f *fakeFocus) FocusMessage(sessionID, messageID string) {
	f.calls = append(f.calls, focusCall{sessionID: sessionID, messageID: messageID})
}

func (f *fakeFocus) snapshot() []focusCall {
	return append([]focusCall(nil), f.calls...)
}

// TestMessageHitOpensSessionAndFocusesMessage covers P2-4: one click on a
// message hit activates the conversation, opens the session screen and gives the
// message's durable locator to the session focus seam.
func TestMessageHitOpensSessionAndFocusesMessage(t *testing.T) {
	store := &fakeStore{hits: []contracts.MessageSearchHit{{
		SessionID:    "s2",
		SessionTitle: "另一个会话",
		MessageID:    contracts.MessageOrdinalLocator(2),
		Role:         "user",
		Snippet:      "这里提到登录修复",
		CreatedAt:    time.Unix(1700000000, 0),
	}}}
	chat := searchChat()
	h := newHarnessWithStore(t, chat, store, sidebar.Config{})
	focus := &fakeFocus{}
	provide(t, h.ctx, contracts.KeySessionFocus, contracts.SessionFocus(focus))
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()
	settleSearch(t, tester, "这里提到登录修复")

	if err := tester.Click("这里提到登录修复"); err != nil {
		t.Fatalf("click hit row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := chat.activeCalls(); len(got) != 1 || got[0] != "s2" {
		t.Fatalf("SetActiveSession calls = %v, want [s2]", got)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("navigator current = %q, want session", current)
	}
	calls := focus.snapshot()
	if len(calls) != 1 || calls[0].sessionID != "s2" ||
		calls[0].messageID != contracts.MessageOrdinalLocator(2) {
		t.Fatalf("focus calls = %+v, want one handoff for s2/ordinal:2", calls)
	}
}

// TestMessageHitWithoutLocatorOnlyOpensSession covers the backward-compatible
// half: a hit without a message locator (or a host without the optional focus
// service) still opens the conversation.
func TestMessageHitWithoutLocatorOnlyOpensSession(t *testing.T) {
	store := &fakeStore{hits: []contracts.MessageSearchHit{{
		SessionID:    "s2",
		SessionTitle: "另一个会话",
		Role:         "user",
		Snippet:      "这里提到登录修复",
		CreatedAt:    time.Unix(1700000000, 0),
	}}}
	chat := searchChat()
	h := newHarnessWithStore(t, chat, store, sidebar.Config{})
	focus := &fakeFocus{}
	provide(t, h.ctx, contracts.KeySessionFocus, contracts.SessionFocus(focus))
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()
	settleSearch(t, tester, "这里提到登录修复")

	if err := tester.Click("这里提到登录修复"); err != nil {
		t.Fatalf("click hit row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := chat.activeCalls(); len(got) != 1 || got[0] != "s2" {
		t.Fatalf("SetActiveSession calls = %v, want [s2]", got)
	}
	if calls := focus.snapshot(); len(calls) != 0 {
		t.Fatalf("a hit without a locator called the focus seam: %+v", calls)
	}
}

// rowSelection samples one session row's persistent selection: the 2 DIP
// accent stripe at the row's left edge and the BgSelected fill behind it.
func rowSelection(t *testing.T, tester *ui.Tester, name string, tokens *design.Tokens) (stripe, tint bool) {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleListItem, name)
	if !ok {
		t.Fatalf("session row %q has no list-item bounds; texts = %v", name, tester.Texts())
	}
	row := info.Bounds
	barY := int(row.Y + row.H/2)
	img := tester.Image()
	for x := int(row.X) - 2; x < int(row.X)+8; x++ {
		if x < 0 {
			continue
		}
		r, g, b, _ := img.At(x, barY).RGBA()
		if colorNear([3]uint32{r >> 8, g >> 8, b >> 8}, rgbOf(tokens.Accent)) {
			stripe = true
			break
		}
	}
	r, g, b, _ := img.At(int(row.X)+6, barY).RGBA()
	return stripe, colorNear([3]uint32{r >> 8, g >> 8, b >> 8},
		rgbOf(tokens.BgSelected.Over(tokens.BgSidebar)))
}

// TestNonSessionScreenDoesNotHighlightActiveSessionRow covers P2-7: on a
// non-conversation screen (Insights here) the sidebar shows one selection, the
// screen destination, and must not also highlight the active conversation row.
// Returning to the conversation restores the row highlight.
func TestNonSessionScreenDoesNotHighlightActiveSessionRow(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "项目讨论"},
			{ID: "s2", Title: "空闲会话"},
		},
		active: "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	if _, err := h.uiRoot.Runtime().RegisterScreen(uinative.ScreenSpec{
		ID: "insights", Title: "洞察", Order: 30,
		Build: func(c *ui.Context) { ui.Text(c, "洞察面板内容") },
	}); err != nil {
		t.Fatalf("register insights screen: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	tokens := design.Light()
	// Baseline: on the conversation screen the active row carries the quiet
	// selection (stripe + tint).
	if stripe, tint := rowSelection(t, tester, "项目讨论", tokens); !stripe || !tint {
		t.Fatalf("the active session row has no selection on the conversation screen (stripe=%v tint=%v); texts = %v",
			stripe, tint, tester.Texts())
	}

	// Go to the Insights screen: the screen destination is the selection, the
	// active conversation row must not stay highlighted too.
	if err := h.uiRoot.Navigator().Push("insights"); err != nil {
		t.Fatalf("push insights: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if current := h.uiRoot.Navigator().Current(); current != "insights" {
		t.Fatalf("navigator current = %q, want insights", current)
	}
	if !tester.HasText("洞察面板内容") {
		t.Fatalf("the insights screen did not render; texts = %v", tester.Texts())
	}
	if stripe, tint := rowSelection(t, tester, "项目讨论", tokens); stripe || tint {
		t.Fatalf("the active session row is still highlighted on the insights screen (stripe=%v tint=%v)",
			stripe, tint)
	}

	// Returning to the conversation restores the one row highlight.
	if err := h.uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("push session: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if stripe, tint := rowSelection(t, tester, "项目讨论", tokens); !stripe || !tint {
		t.Fatalf("the active session row lost its selection when returning (stripe=%v tint=%v)", stripe, tint)
	}
}
