package settings_test

// Headless tests for the model settings page: the real host settings surface
// (uiroot.View) is rendered through mygo's in-memory Tester; the page is opened
// with the host navigator, and saving is asserted against a fake settings store.

import (
	"errors"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeSettings is a contracts.SettingsService backed by a map.
type fakeSettings struct {
	mu      sync.Mutex
	values  map[string]string
	sets    []string
	setErr  error
	getKeys []string
}

func newFakeSettings(values map[string]string) *fakeSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &fakeSettings{values: values}
}

func (s *fakeSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getKeys = append(s.getKeys, key)
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *fakeSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.sets = append(s.sets, key)
	s.values[key] = value
	return nil
}

func (s *fakeSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func (s *fakeSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

func (s *fakeSettings) setKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sets...)
}

// fakeChat is a minimal contracts.ChatService: the settings page only needs its
// effective config and active session.
type fakeChat struct {
	mu     sync.Mutex
	config contracts.ChatConfig
	active string
}

func (f *fakeChat) Config() contracts.ChatConfig { f.mu.Lock(); defer f.mu.Unlock(); return f.config }

func (f *fakeChat) Sessions() []contracts.ChatSessionInfo { return nil }

func (f *fakeChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}

func (f *fakeChat) ActiveSession() string { f.mu.Lock(); defer f.mu.Unlock(); return f.active }

func (f *fakeChat) SetActiveSession(string) error { return nil }

func (f *fakeChat) NewSession(string) (string, error) { return "new-1", nil }

func (f *fakeChat) DeleteSession(string) error { return nil }

func (f *fakeChat) Send(string, string) error { return nil }

func (f *fakeChat) Cancel(string) error { return nil }

func (f *fakeChat) Subscribe(func(string)) (func(), error) { return func() {}, nil }

// harness is one headless product with the host UI services and the settings
// module active.
type harness struct {
	t        *testing.T
	ctx      cordis.Context
	uiRoot   *uiroot.Root
	settings *fakeSettings
	chat     *fakeChat
	fiber    *cordis.Fiber
}

func newHarness(t *testing.T, chat *fakeChat, store *fakeSettings, cfg settings.Config) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(store))
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, settings: store, chat: chat}
	h.fiber = activate(t, ctx, settings.Module(), cfg)
	uiRoot.Navigator().ToggleSettings()
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

// focusInput clicks the native control labeled label so typing goes into it.
func (h *harness) focusInput(tester *ui.Tester, label string) {
	h.t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		h.t.Fatalf("input labeled %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
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

// TestSettingsPageRendersFieldsAndMasksAPIKey covers the page layout, the labels
// and the API key masking.
func TestSettingsPageRendersFieldsAndMasksAPIKey(t *testing.T) {
	chat := &fakeChat{
		config: contracts.ChatConfig{
			Model:        "gpt-4o",
			BaseURL:      "https://api.example.com/v1",
			APIKey:       "sk-secret-value",
			SystemPrompt: "You are helpful.",
		},
	}
	h := newHarness(t, chat, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "模型", "Base URL", "API Key", "系统提示词", "保存",
		"已设置（留空保持不变）", "当前会话", "当前模型")
	h.assertNoTexts(tester, "sk-secret-value", "未设置")
}

// TestSettingsSaveWritesKeys covers the save button: the settings keys are
// written, the API key is not overwritten by an empty draft, and typing a new
// key writes it.
func TestSettingsSaveWritesKeys(t *testing.T) {
	chat := &fakeChat{
		config: contracts.ChatConfig{
			Model:        "gpt-4o",
			BaseURL:      "https://api.example.com/v1",
			SystemPrompt: "You are helpful.",
		},
		active: "session-7",
	}
	store := newFakeSettings(nil)
	h := newHarness(t, chat, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "当前会话: session-7")

	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatModel); got != "gpt-4o" {
		t.Fatalf("%s = %q, want gpt-4o", contracts.SettingsChatModel, got)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://api.example.com/v1" {
		t.Fatalf("%s = %q", contracts.SettingsChatBaseURL, got)
	}
	if got := store.get(contracts.SettingsChatSystemPrompt); got != "You are helpful." {
		t.Fatalf("%s = %q", contracts.SettingsChatSystemPrompt, got)
	}
	if _, written := store.All()[contracts.SettingsChatAPIKey]; written {
		t.Fatal("an empty API key draft overwrote the stored key")
	}

	// Type a new API key into the native input and save: only then is the key
	// written, and it is never shown again.
	h.focusInput(tester, "API Key 输入")
	tester.Type("sk-new-123")
	tester.Frame()
	if got := store.get(contracts.SettingsChatAPIKey); got != "" {
		t.Fatalf("API key was written before save: %q", got)
	}
	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatAPIKey); got != "sk-new-123" {
		t.Fatalf("%s = %q, want sk-new-123", contracts.SettingsChatAPIKey, got)
	}
	h.assertNoTexts(tester, "sk-new-123")
	h.assertTexts(tester, "已保存")
}

// TestSettingsFallbackToStore covers loading from the settings store when the
// chat service config is empty, and that a stored API key is never rewritten
// from an empty draft.
func TestSettingsFallbackToStore(t *testing.T) {
	chat := &fakeChat{}
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:        "store-model",
		contracts.SettingsChatBaseURL:      "https://store.example.com",
		contracts.SettingsChatSystemPrompt: "stored prompt",
		contracts.SettingsChatAPIKey:       "stored-key",
	})
	h := newHarness(t, chat, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "已设置（留空保持不变）")
	h.assertNoTexts(tester, "stored-key")

	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatModel); got != "store-model" {
		t.Fatalf("%s = %q, want store-model", contracts.SettingsChatModel, got)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://store.example.com" {
		t.Fatalf("%s = %q", contracts.SettingsChatBaseURL, got)
	}
	if got := store.get(contracts.SettingsChatSystemPrompt); got != "stored prompt" {
		t.Fatalf("%s = %q", contracts.SettingsChatSystemPrompt, got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "stored-key" {
		t.Fatalf("%s = %q, want the stored key kept", contracts.SettingsChatAPIKey, got)
	}
}

// TestSettingsSaveError covers a failing settings store.
func TestSettingsSaveError(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errors.New("disk is read-only")
	h := newHarness(t, &fakeChat{config: contracts.ChatConfig{Model: "gpt-4o"}}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "保存")
	h.assertTexts(tester, "保存失败", "disk is read-only")
	h.assertNoTexts(tester, "已保存")
}

// TestSettingsConfigLabels covers the assembly-config copy.
func TestSettingsConfigLabels(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{
		Title:             "模型服务",
		ModelLabel:        "模型名",
		BaseURLLabel:      "服务地址",
		APIKeyLabel:       "访问密钥",
		SystemPromptLabel: "系统指令",
		SaveLabel:         "保存配置",
	})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "模型服务", "模型名", "服务地址", "访问密钥", "系统指令", "保存配置")
}

// TestSettingsDisposeRemovesPage covers kernel effect unwinding.
func TestSettingsDisposeRemovesPage(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "保存")

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose settings module: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "保存", "系统提示词")
	h.assertTexts(tester, "还没有注册任何设置页。")
}

// TestModuleManifest pins the module contract.
func TestModuleManifest(t *testing.T) {
	descriptor := settings.Module()
	if descriptor.ID != "zeokeel-ui/settings" {
		t.Fatalf("module ID = %q, want zeokeel-ui/settings", descriptor.ID)
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
		contracts.KeySettings.Name(),
		contracts.KeyChat.Name(),
	} {
		if !required[name] {
			t.Fatalf("module does not require %q; requires = %v", name, descriptor.Requires)
		}
	}
}
