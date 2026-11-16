package regression

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// behaviorHarness is the settings surface (plus the session screen when the
// test needs the composer) over a scripted chat backend.
type behaviorHarness struct {
	h      *harness
	tester *ui.Tester
}

// startBehaviorUI activates the settings screen and, optionally, the session
// screen (the composer menu shares the stored permission row).
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

// click clicks one label and repaints.
func (b *behaviorHarness) click(t *testing.T, label string) {
	t.Helper()
	if err := b.tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, b.tester.Texts())
	}
	b.tester.Frame()
}
