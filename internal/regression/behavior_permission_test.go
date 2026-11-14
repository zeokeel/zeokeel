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
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "q"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "Markdown answer"},
		}}, true)
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

// TestBehaviorPermissionPageWrites covers the page: default workspace-write,
// the three presets write the shared row with a "已保存" badge, the retired
// ask/never rows display as read-only, and unknown stored values display as
// workspace without being overwritten by a frame.
func TestBehaviorPermissionPageWrites(t *testing.T) {
	harness := startBehaviorUI(t, false)
	harness.openSettingsPage(t, "行为")
	// The page shows the current mode; the three presets live in its popover
	// (the explanatory bullets that used to repeat their names were removed as
	// redundant copy).
	requireHasTexts(t, harness.tester, "行为", "默认代理权限", "当前：工作区可写",
		"工作区可写")

	// Select all permissions through the popover.
	harness.click(t, "工作区可写")
	requireHasTexts(t, harness.tester, "只读", "工作区可写", "所有权限")
	harness.click(t, "所有权限")
	if got := harness.storedPermission(); got != contracts.PermissionFull {
		t.Fatalf("agent.permission after 所有权限 = %q, want full", got)
	}
	requireHasText(t, harness.tester, "已保存")
	requireHasText(t, harness.tester, "当前：所有权限")

	// Select read-only, then back to the default: all three write the same row.
	harness.click(t, "所有权限")
	harness.click(t, "只读")
	if got := harness.storedPermission(); got != contracts.PermissionReadOnly {
		t.Fatalf("agent.permission after 只读 = %q, want read-only", got)
	}
	requireHasText(t, harness.tester, "当前：只读")
	harness.click(t, "只读")
	harness.click(t, "工作区可写")
	if got := harness.storedPermission(); got != contracts.PermissionWorkspace {
		t.Fatalf("agent.permission after 工作区可写 = %q, want workspace", got)
	}

	// Retired "ask"/"never" rows normalize to the read-only display; a frame
	// never rewrites the stored legacy value by itself.
	for _, legacy := range []string{"ask", "never"} {
		if err := harness.h.settings.Set(contracts.SettingAgentPermission, legacy); err != nil {
			t.Fatalf("set legacy %q: %v", legacy, err)
		}
		harness.tester.Frame()
		requireHasText(t, harness.tester, "当前：只读")
		if got := harness.storedPermission(); got != legacy {
			t.Fatalf("a frame rewrote legacy agent.permission %q to %q", legacy, got)
		}
	}

	// Unknown stored value: displayed as workspace, not overwritten by rendering.
	if err := harness.h.settings.Set(contracts.SettingAgentPermission, "bogus"); err != nil {
		t.Fatalf("set bogus permission: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "当前：工作区可写")
	if got := harness.storedPermission(); got != "bogus" {
		t.Fatalf("a frame overwrote agent.permission to %q", got)
	}
}

// TestBehaviorPermissionSharedWithSessionChip covers the shared row: choosing
// 只读 on the settings page makes the session composer button show 只读; a full
// row shows 所有权限 and a retired "never" row is drawn as read-only (never is
// not a selectable mode any more).
func TestBehaviorPermissionSharedWithSessionChip(t *testing.T) {
	// Choose 只读 before the session screen exists: the composer button renders
	// the same labels as the settings page, so the behavior button must be
	// unambiguous while it is clicked.
	harness := startBehaviorUI(t, false)
	harness.openSettingsPage(t, "行为")
	harness.click(t, "工作区可写")
	harness.click(t, "只读")
	requireHasText(t, harness.tester, "已保存")
	requireHasText(t, harness.tester, "当前：只读")
	harness.h.ui.Navigator().CloseSettings()
	harness.tester.Frame()

	// Now activate the session screen on the same kernel and check the shared row.
	activate(t, harness.h.ctx, sessionui.Module(), sessionui.Config{})
	harness.h.push("session")
	harness.tester.Frame()
	// The chip is the fixed 会话设置 destination; the current permission is the
	// menu row's trailing value (R17).
	requireHasText(t, harness.tester, "会话设置")
	openSessionMenu(t, harness.tester)
	requireHasText(t, harness.tester, "只读")
	harness.tester.CloseMenu()
	if got := harness.storedPermission(); got != contracts.PermissionReadOnly {
		t.Fatalf("agent.permission after 只读 = %q, want read-only", got)
	}

	// The all-permissions preset owns the composer button label.
	if err := harness.h.settings.Set(contracts.SettingAgentPermission, contracts.PermissionFull); err != nil {
		t.Fatalf("set full permission: %v", err)
	}
	harness.tester.Frame()
	openSessionMenu(t, harness.tester)
	requireHasText(t, harness.tester, "所有权限")
	harness.tester.CloseMenu()

	// A retired "never" row set externally is drawn as the normalized read-only
	// mode by the composer instead of the old 禁止工具 label.
	if err := harness.h.settings.Set(contracts.SettingAgentPermission, "never"); err != nil {
		t.Fatalf("set legacy never: %v", err)
	}
	harness.tester.Frame()
	openSessionMenu(t, harness.tester)
	requireHasText(t, harness.tester, "只读")
	harness.tester.CloseMenu()
	requireHasNoText(t, harness.tester, "询问")
	requireHasNoText(t, harness.tester, "禁止工具")
}

// openSessionMenu opens the composer's 会话设置 menu so a test can read the
// current permission/mode values it carries.
func openSessionMenu(t *testing.T, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("会话设置"); err != nil {
		t.Fatalf("open session menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
}

// requireHasNoText fails when the frame contains want as a substring.
func requireHasNoText(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	if tester.HasText(want) {
		t.Fatalf("frame unexpectedly shows %q; texts = %v", want, tester.Texts())
	}
}
