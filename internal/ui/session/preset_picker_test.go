package session_test

// Tests for the composer agent-preset (persona) picker.

import (
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// presetChat adds contracts.SessionPresetSetter to the session fake.
type presetChat struct {
	*fakeChat
	mu       sync.Mutex
	selected map[string]string
	calls    []string
}

func newPresetChat() *presetChat {
	return &presetChat{fakeChat: newFakeChat(), selected: map[string]string{}}
}
func (c *presetChat) SetSessionPreset(sessionID, presetID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, sessionID+":"+presetID)
	if presetID == "" {
		delete(c.selected, sessionID)
	} else {
		c.selected[sessionID] = presetID
	}
	return nil
}
func (c *presetChat) SessionPreset(sessionID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selected[sessionID]
}
func (c *presetChat) selectedCalls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func newPresetsHarness(t *testing.T, chat contracts.ChatService, store contracts.SettingsService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.SettingsService](t, ctx, contracts.KeySettings, store)
	activate(t, ctx, session.Module(), session.Config{})
	h := &harness{t: t, ctx: ctx, ui: uiRoot}
	return h
}

func presetStore(t *testing.T) *permissionSettings {
	raw, err := contracts.EncodeAgentPresets([]contracts.AgentPreset{
		{ID: "p1", Name: "Reviewer", Persona: "review carefully"},
		{ID: "p2", Name: "Writer"},
	})
	if err != nil {
		t.Fatalf("encode presets: %v", err)
	}
	return newPermissionSettings(map[string]string{contracts.SettingAgentPresets: raw})
}

func presetSessionChat(chat *presetChat) *presetChat {
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "人设会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	return chat
}

func TestPersonaPickerSelectAndClear(t *testing.T) {
	chat := presetSessionChat(newPresetChat())
	h := newPresetsHarness(t, chat, presetStore(t))
	tester := h.tester()
	// The persona rows live in the one session menu now, not on a chip.
	if got := exactTextCount(tester.Texts(), "人设"); got != 0 {
		t.Fatalf("standalone persona chip rendered; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "工作区可写")
	h.openSessionMenu(tester)
	h.assertTexts(tester, "人设")
	h.clickExact(tester, "人设")
	h.assertTexts(tester, "Reviewer", "Writer", "无")
	h.clickExact(tester, "Reviewer")
	tester.Frame()
	if calls := chat.selectedCalls(); len(calls) != 1 || calls[0] != "sess-1:p1" {
		t.Fatalf("SetSessionPreset calls = %v", calls)
	}
	// The picker keeps the composer row calm: the choice is reflected inside the
	// menu (checked row), not by renaming the button.
	h.assertTexts(tester, "工作区可写")

	h.openSessionMenu(tester)
	h.clickExact(tester, "人设")
	h.assertTexts(tester, "Reviewer", "无")
	h.clickExact(tester, "无")
	tester.Frame()
	if calls := chat.selectedCalls(); len(calls) != 2 || calls[1] != "sess-1:" {
		t.Fatalf("clear call = %v", calls)
	}
}

func TestPersonaPickerHiddenWithoutPresetsOrSetter(t *testing.T) {
	// A preset-less backend hides the persona group.
	h := newPresetsHarness(t, presetSessionChat(newPresetChat()), newPermissionSettings(nil))
	tester := h.tester()
	if got := exactTextCount(tester.Texts(), "人设"); got != 0 {
		t.Fatalf("persona picker rendered without presets; texts = %v", tester.Texts())
	}
	h.openSessionMenu(tester)
	if got := exactTextCount(tester.Texts(), "人设"); got != 0 {
		t.Fatalf("persona group rendered in the menu without presets; texts = %v", tester.Texts())
	}

	// A backend without SessionPresetSetter hides it even with presets.
	plain := newFakeChat()
	plain.addSession(contracts.ChatSession{ID: "sess-1", Title: "普通",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "x"}}}, true)
	h = newPresetsHarness(t, plain, presetStore(t))
	tester = h.tester()
	if got := exactTextCount(tester.Texts(), "人设"); got != 0 {
		t.Fatalf("persona picker rendered without a setter; texts = %v", tester.Texts())
	}
	h.openSessionMenu(tester)
	if got := exactTextCount(tester.Texts(), "人设"); got != 0 {
		t.Fatalf("persona group rendered in the menu without a setter; texts = %v", tester.Texts())
	}
}

func TestPersonaPickerEnglish(t *testing.T) {
	chat := presetSessionChat(newPresetChat())
	h := newPresetsHarness(t, chat, presetStore(t))
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Workspace")
	h.openSessionMenu(tester)
	h.assertTexts(tester, "Persona")
	h.clickExact(tester, "Persona")
	h.assertTexts(tester, "Reviewer", "None")
	h.clickExact(tester, "Reviewer")
	tester.Frame()
	if calls := chat.selectedCalls(); len(calls) != 1 || calls[0] != "sess-1:p1" {
		t.Fatalf("SetSessionPreset calls = %v", calls)
	}
	h.assertTexts(tester, "Workspace")
}
