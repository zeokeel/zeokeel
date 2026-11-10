package session_test

// Tests for the composer model switcher: the effective-model chip, the native
// model menu fed by the optional catalogue, per-session switching and the
// fallback to the settings surface when the backend cannot switch.

import (
	"errors"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// modelChat adds contracts.SessionModelSwitcher to the session fake; the switch
// updates the read model so the next frame's chip reflects the choice.
type modelChat struct {
	*fakeChat
	mu        sync.Mutex
	switches  []modelSwitch
	switchErr error
}

type modelSwitch struct {
	sessionID string
	model     string
}

func newModelChat() *modelChat {
	return &modelChat{fakeChat: newFakeChat()}
}

func (c *modelChat) SetSessionModel(sessionID, model string) error {
	c.mu.Lock()
	c.switches = append(c.switches, modelSwitch{sessionID: sessionID, model: model})
	err := c.switchErr
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.fakeChat.mu.Lock()
	defer c.fakeChat.mu.Unlock()
	chatSession, ok := c.fakeChat.sessions[sessionID]
	if !ok {
		return errors.New("fake: unknown session")
	}
	chatSession.Model = model
	c.fakeChat.sessions[sessionID] = chatSession
	return nil
}

func (c *modelChat) calls() []modelSwitch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]modelSwitch(nil), c.switches...)
}

// fakeCatalog is a contracts.ModelCatalogService with a fixed list.
type fakeCatalog struct {
	models []contracts.ModelInfo
}

func (f *fakeCatalog) Models() []contracts.ModelInfo {
	return append([]contracts.ModelInfo(nil), f.models...)
}

func catalogModels() *fakeCatalog {
	return &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o-mini", Name: "GPT-4o mini", Note: "快速"},
		{ID: "gpt-4o", Name: "GPT-4o", Note: "旗舰"},
	}}
}

// newModelHarness builds the session harness with any chat service and the
// optional model catalogue backend.
func newModelHarness(t *testing.T, chat contracts.ChatService, catalog contracts.ModelCatalogService) *harness {
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
	if catalog != nil {
		provide[contracts.ModelCatalogService](t, ctx, contracts.KeyModelCatalog, catalog)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot}
}

func modelSession(id, model string) contracts.ChatSession {
	return contracts.ChatSession{ID: id, Title: "模型会话", Model: model, Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: "你好呀"},
	}}
}

// --- tests ------------------------------------------------------------------

func TestComposerModelChipShowsSessionModel(t *testing.T) {
	chat := newModelChat()
	chat.config = contracts.ChatConfig{Model: "configured-model"}
	chat.addSession(modelSession("sess-1", "session-model"), true)
	h := newModelHarness(t, chat, catalogModels())
	tester := h.tester()

	// The session override wins over the configured model.
	h.assertTexts(tester, "session-model")
	h.assertNoTexts(tester, "configured-model")
}

func TestComposerModelMenuSwitchesSessionModel(t *testing.T) {
	chat := newModelChat()
	chat.addSession(modelSession("sess-1", "gpt-4o-mini"), true)
	h := newModelHarness(t, chat, catalogModels())
	tester := h.tester()
	h.assertTexts(tester, "gpt-4o-mini")

	// The menu lists the catalogue names, each note as its subtitle; the current
	// model is checked by the native menu rather than duplicated in the label.
	h.click(tester, "gpt-4o-mini")
	h.assertTexts(tester, "选择模型", "GPT-4o mini", "快速", "GPT-4o", "旗舰", "打开模型设置")
	// The note is the row's subtitle only: it must not appear twice per row
	// (label + subtitle) as it did when the label inlined "name · note".
	if got := textOccurrences(tester.Texts(), "快速"); got != 1 {
		t.Fatalf("model note 快速 appears %d times, want once; texts = %v", got, tester.Texts())
	}
	h.clickExact(tester, "GPT-4o")
	tester.Frame()

	calls := chat.calls()
	if len(calls) != 1 || calls[0].sessionID != "sess-1" || calls[0].model != "gpt-4o" {
		t.Fatalf("SetSessionModel calls = %+v, want sess-1/gpt-4o", calls)
	}
	h.assertTexts(tester, "gpt-4o")
}

func TestComposerModelSwitchErrorIsQuiet(t *testing.T) {
	chat := newModelChat()
	chat.switchErr = errors.New("model unavailable")
	chat.addSession(modelSession("sess-1", "gpt-4o-mini"), true)
	h := newModelHarness(t, chat, catalogModels())
	tester := h.tester()
	h.click(tester, "gpt-4o-mini")
	h.clickExact(tester, "GPT-4o")

	if calls := chat.calls(); len(calls) != 1 {
		t.Fatalf("SetSessionModel calls = %v, want one attempt", calls)
	}
	// The failed switch changes nothing and shows no error row.
	h.assertTexts(tester, "gpt-4o-mini")
	h.assertNoTexts(tester, "model unavailable", "切换模型失败")
}

func TestComposerModelMenuWithoutCatalog(t *testing.T) {
	chat := newModelChat()
	chat.addSession(modelSession("sess-1", "gpt-4o-mini"), true)
	h := newModelHarness(t, chat, nil)
	tester := h.tester()
	h.click(tester, "gpt-4o-mini")

	// Without a catalogue the menu shows the current model and the settings
	// fallback only.
	h.assertTexts(tester, "选择模型", "gpt-4o-mini", "打开模型设置")
	h.click(tester, "打开模型设置")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("model settings fallback did not open the settings surface")
	}
}

func TestComposerModelChipWithoutSwitcherOpensSettings(t *testing.T) {
	// A plain fakeChat has no SessionModelSwitcher: the chip keeps the old
	// behavior and opens the settings surface.
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{Model: "configured-model"}
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "普通"}, true)
	h := newModelHarness(t, chat, nil)
	tester := h.tester()
	h.assertTexts(tester, "configured-model")
	h.click(tester, "configured-model")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("model chip did not open the settings surface")
	}
	h.assertNoTexts(tester, "选择模型")
}

func TestComposerModelMenuEnglishCopy(t *testing.T) {
	chat := newModelChat()
	chat.addSession(modelSession("sess-1", "gpt-4o-mini"), true)
	h := newModelHarness(t, chat, catalogModels())
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()

	h.click(tester, "gpt-4o-mini")
	h.assertTexts(tester, "Choose a model", "GPT-4o mini", "快速", "Open model settings")
}

func TestComposerModelChipFallsBackToConfig(t *testing.T) {
	chat := newModelChat()
	chat.config = contracts.ChatConfig{Model: "configured-model"}
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "无覆盖", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: "你好呀"},
	}}, true)
	h := newModelHarness(t, chat, catalogModels())
	tester := h.tester()
	h.assertTexts(tester, "configured-model")
}

// textOccurrences counts how many rendered texts contain fragment. The model
// menu regression asserts a catalogue note renders once, not once inlined in
// the row label and once as its subtitle.
func textOccurrences(texts []string, fragment string) int {
	count := 0
	for _, text := range texts {
		if strings.Contains(text, fragment) {
			count++
		}
	}
	return count
}
