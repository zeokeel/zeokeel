package session_test

// Tests for the composer's prompt-preset insertion menu.

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakePresetSettings is the settings service the preset menu reads.
type fakePresetSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newFakePresetSettings(values map[string]string) *fakePresetSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &fakePresetSettings{values: values}
}

func (s *fakePresetSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *fakePresetSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *fakePresetSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

// newHarnessWithPresetSettings activates the session module with KeySettings
// provided before activation (the module captures it at activation time).
func newHarnessWithPresetSettings(t *testing.T, chat *fakeChat, settings contracts.SettingsService) *harness {
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
	if settings != nil {
		provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

func activePresetChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "Preset chat"}, true)
	return chat
}

// openPresetMenu clicks the composer's preset button and returns the visible
// labels (the Tester renders popover items into the frame text list).
func openPresetMenu(t *testing.T, h *harness, tester *ui.Tester, label string) []string {
	t.Helper()
	h.click(tester, label)
	return tester.Texts()
}

func menuHas(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

func TestComposerPresetsBuiltInInsert(t *testing.T) {
	chat := activePresetChat()
	h := newHarnessWithPresetSettings(t, chat, nil)
	tester := h.tester()
	h.assertTexts(tester, "预设")
	labels := openPresetMenu(t, h, tester, "预设")
	for _, want := range []string{"简洁回答", "代码审查", "逐步推理"} {
		if !menuHas(labels, want) {
			t.Fatalf("menu labels = %v, want %q", labels, want)
		}
	}
	h.click(tester, "简洁回答")
	h.click(tester, "发送")
	chat.mu.Lock()
	defer chat.mu.Unlock()
	if len(chat.sendCalls) != 1 {
		t.Fatalf("send calls = %+v", chat.sendCalls)
	}
	if chat.sendCalls[0].prompt != contracts.LegacyPromptPresets()[0].Prompt {
		t.Fatalf("sent prompt = %q, want the built-in preset prompt", chat.sendCalls[0].prompt)
	}
}

func TestComposerPresetsCustomOrderAndAppend(t *testing.T) {
	settings := newFakePresetSettings(map[string]string{
		contracts.SettingPromptPresets: `[{"name":"甲","prompt":"提示甲"},{"name":"乙","prompt":"提示乙"}]`,
	})
	chat := activePresetChat()
	h := newHarnessWithPresetSettings(t, chat, settings)
	tester := h.tester()
	// Type an existing draft first: choosing a preset appends with a blank line.
	h.click(tester, "输入消息…")
	tester.Type("已有草稿")
	tester.Frame()
	labels := openPresetMenu(t, h, tester, "预设")
	if !menuHas(labels, "甲") || !menuHas(labels, "乙") {
		t.Fatalf("custom preset menu labels = %v", labels)
	}
	h.click(tester, "乙")
	h.click(tester, "发送")
	chat.mu.Lock()
	defer chat.mu.Unlock()
	if len(chat.sendCalls) != 1 || chat.sendCalls[0].prompt != "已有草稿\n\n提示乙" {
		t.Fatalf("sent prompt = %+v, want appended preset", chat.sendCalls)
	}
}

func TestComposerPresetsBadJSONAndEmptyFallBack(t *testing.T) {
	for name, raw := range map[string]string{"bad JSON": "not-json", "empty array": "[]", "empty string": ""} {
		t.Run(name, func(t *testing.T) {
			settings := newFakePresetSettings(map[string]string{contracts.SettingPromptPresets: raw})
			chat := activePresetChat()
			h := newHarnessWithPresetSettings(t, chat, settings)
			tester := h.tester()
			labels := openPresetMenu(t, h, tester, "预设")
			if !menuHas(labels, "简洁回答") || !menuHas(labels, "代码审查") {
				t.Fatalf("fallback menu labels = %v", labels)
			}
			h.click(tester, "代码审查")
			h.click(tester, "发送")
			chat.mu.Lock()
			defer chat.mu.Unlock()
			if len(chat.sendCalls) != 1 || chat.sendCalls[0].prompt != contracts.LegacyPromptPresets()[1].Prompt {
				t.Fatalf("fallback send = %+v", chat.sendCalls)
			}
		})
	}
}

func TestComposerPresetsEnglish(t *testing.T) {
	chat := activePresetChat()
	h := newHarnessWithPresetSettings(t, chat, nil)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	h.assertTexts(tester, "Presets")
	labels := openPresetMenu(t, h, tester, "Presets")
	if !menuHas(labels, "简洁回答") {
		t.Fatalf("English preset menu labels = %v", labels)
	}
}
