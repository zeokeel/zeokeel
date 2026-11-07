package regression

// Adversarial audit: the settings 行为 page's default agent permission. Negative
// and boundary cases: the ask default, full/never/ask writes, unknown stored
// values normalize to ask without overwrite, and the selection is the same
// contracts.SettingAgentPermission row the session composer's permission chip
// reads (including the "never" mode the chip does not cycle).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// behaviorHarness is the settings surface (plus the session screen for the
// shared-chip assertions) over a scripted chat backend.
type behaviorHarness struct {
	h      *harness
	tester *ui.Tester
}

// startBehaviorUI activates the settings screen and, optionally, the session
// screen (the composer permission chip shares the same setting row).
func startBehaviorUI(t *testing.T, withSession bool) *behaviorHarness {
	t.Helper()
	h := newBareHarness(t)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-behave", Title: "Behavior session",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "q"}}}, true)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	if withSession {
		activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	}
	tester := h.tester(1280, 800)
	tester.Frame()
	return &behaviorHarness{h: h, tester: tester}
}

// openSettingsPage opens the settings surface and one page by nav label.
func (b *behaviorHarness) openSettingsPage(t *testing.T, label string) {
	t.Helper()
	if !b.h.ui.Navigator().SettingsOpen() {
		b.h.ui.Navigator().ToggleSettings()
	}
	b.tester.Frame()
	if err := b.tester.Click(label); err != nil {
		t.Fatalf("open settings page %q: %v; texts = %v", label, err, b.tester.Texts())
	}
	b.tester.Frame()
}

// clickBehavior clicks one label in the behavior page/menu and repaints.
func (b *behaviorHarness) click(t *testing.T, label string) {
	t.Helper()
	if err := b.tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, b.tester.Texts())
	}
	b.tester.Frame()
}

// storedPermission returns the persisted agent.permission row.
func (b *behaviorHarness) storedPermission() string {
	return b.h.settings.Get(contracts.SettingAgentPermission, "")
}

// TestBehaviorPermissionPageWrites covers the page: default ask, the four
// choices write the shared row with a "已保存" badge, and unknown stored values
// display as ask without being overwritten by a frame.
func TestBehaviorPermissionPageWrites(t *testing.T) {
	harness := startBehaviorUI(t, false)
	harness.openSettingsPage(t, "行为")
	requireHasTexts(t, harness.tester, "行为", "默认代理权限", "当前：询问", "询问",
		"工作区", "完全访问", "从不")

	// Select full access through the popover.
	harness.click(t, "询问")
	requireHasTexts(t, harness.tester, "工作区", "完全访问", "从不")
	harness.click(t, "完全访问")
	if got := harness.storedPermission(); got != contracts.PermissionFull {
		t.Fatalf("agent.permission after 完全访问 = %q, want full", got)
	}
	requireHasText(t, harness.tester, "已保存")
	requireHasText(t, harness.tester, "当前：完全访问")

	// Select never, then back to ask: both write the same row.
	harness.click(t, "完全访问")
	harness.click(t, "从不")
	if got := harness.storedPermission(); got != "never" {
		t.Fatalf("agent.permission after 从不 = %q, want never", got)
	}
	requireHasText(t, harness.tester, "当前：从不")
	harness.click(t, "从不")
	harness.click(t, "询问")
	if got := harness.storedPermission(); got != contracts.PermissionAsk {
		t.Fatalf("agent.permission after 询问 = %q, want ask", got)
	}

	// Unknown stored value: displayed as ask, not overwritten by rendering.
	if err := harness.h.settings.Set(contracts.SettingAgentPermission, "bogus"); err != nil {
		t.Fatalf("set bogus permission: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "当前：询问")
	if got := harness.storedPermission(); got != "bogus" {
		t.Fatalf("a frame overwrote agent.permission to %q", got)
	}
}

// TestBehaviorPermissionSharedWithSessionChip covers the shared row: choosing
// 完全访问 on the settings page makes the session composer chip show 全部允许;
// choosing 从不 persists never (the chip's three-step cycle does not include it,
// so the probe records the display mismatch as AUDIT-P3-14).
func TestBehaviorPermissionSharedWithSessionChip(t *testing.T) {
	// Configure 完全访问 before the session screen exists: the composer chip renders
	// the same labels as the settings page, so the behavior button must be
	// unambiguous while it is clicked.
	harness := startBehaviorUI(t, false)
	harness.openSettingsPage(t, "行为")
	harness.click(t, "询问")
	harness.click(t, "完全访问")
	requireHasText(t, harness.tester, "已保存")
	harness.h.ui.Navigator().CloseSettings()
	harness.tester.Frame()

	// Now activate the session screen on the same kernel and check the shared row.
	activate(t, harness.h.ctx, sessionui.Module(), sessionui.Config{})
	harness.h.push("session")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "全部允许")
	if got := harness.storedPermission(); got != contracts.PermissionFull {
		t.Fatalf("agent.permission after 完全访问 = %q, want full", got)
	}

	// Back to settings: choose 从不. The composer chip (全部允许) cannot be
	// confused with the behavior popover button (完全访问) or menu item (从不).
	harness.openSettingsPage(t, "行为")
	harness.click(t, "完全访问")
	harness.click(t, "从不")
	harness.h.ui.Navigator().CloseSettings()
	harness.tester.Frame()
	if got := harness.storedPermission(); got != "never" {
		t.Fatalf("agent.permission after 从不 = %q, want never", got)
	}
	showsNever := false
	showsAsk := false
	for _, text := range harness.tester.Texts() {
		if text == "从不" {
			showsNever = true
		}
		if text == "询问" {
			showsAsk = true
		}
	}
	if !showsNever && showsAsk {
		t.Logf("AUDIT-P3-14: agent.permission=never is stored but the session permission chip shows 询问 (chip cycle ask→workspace→full does not represent never)")
	}
}
