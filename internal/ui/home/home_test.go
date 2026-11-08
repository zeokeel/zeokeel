package home_test

// Headless tests for the Home setup card: when the agent backend has no model
// configured, Home shows the old shell's first-run guidance and links to the
// settings surface; a configured model hides it.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/appinfo"
	"github.com/zeokeel/zeokeel/internal/ui/home"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

type fakeChat struct {
	config   contracts.ChatConfig
	sessions []contracts.ChatSessionInfo
}

func (f *fakeChat) Config() contracts.ChatConfig          { return f.config }
func (f *fakeChat) Sessions() []contracts.ChatSessionInfo { return f.sessions }
func (f *fakeChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}
func (f *fakeChat) ActiveSession() string                  { return "" }
func (f *fakeChat) SetActiveSession(string) error          { return nil }
func (f *fakeChat) NewSession(string) (string, error)      { return "sess-1", nil }
func (f *fakeChat) DeleteSession(string) error             { return nil }
func (f *fakeChat) Send(string, string) error              { return nil }
func (f *fakeChat) Cancel(string) error                    { return nil }
func (f *fakeChat) Subscribe(func(string)) (func(), error) { return func() {}, nil }

func TestHomeEmptyStateOffersFirstSteps(t *testing.T) {
	h := newHarness(t, &fakeChat{})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	// No conversations yet: the empty state offers the next steps, including
	// configuring the model that is still missing.
	h.assertTexts(tester, "还没有会话。开始第一次对话，或先完成下面的准备：", "配置模型", "新对话")
	if err := tester.Click("配置模型"); err != nil {
		t.Fatalf("click configure model: %v; texts = %v", err, tester.Texts())
	}
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("configure-model button did not open the settings surface")
	}
}

func TestHomeSetupCardShownOnceConversationsExist(t *testing.T) {
	h := newHarness(t, &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "旧会话"}},
	})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	// With a conversation there is no empty state: the first-run card still
	// explains the missing model.
	h.assertTexts(tester, "完成 ZeoKeel 的初始设置", "配置模型")
	h.assertNoTexts(tester, "新对话")
}

func TestHomeEmptyStateHiddenWithModel(t *testing.T) {
	h := newHarness(t, &fakeChat{config: contracts.ChatConfig{Model: "test-model"}})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.assertNoTexts(tester, "完成 ZeoKeel 的初始设置", "配置模型")
	h.assertTexts(tester, "问点什么…", "新对话")
}

// harness is the minimal headless product: uiroot host services + appinfo + the
// fake chat service the Home setup card reads.
type harness struct {
	t   *testing.T
	ctx cordis.Context
	ui  *uiroot.Root
}

func newHarness(t *testing.T, chat contracts.ChatService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeyChat, chat)
	h := &harness{t: t, ctx: ctx, ui: uiRoot}
	activate(t, h, appinfo.Module(), appinfo.Config{Name: "ZeoKeel", Version: "0.1.0"})
	return h
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

func (p *descriptorPlugin[T]) Name() string     { return p.descriptor.ID }
func (p *descriptorPlugin[T]) Inject() []string { return p.inject }
func (p *descriptorPlugin[T]) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

func activate[T any](t *testing.T, h *harness, descriptor *module.Descriptor[T], config T) {
	t.Helper()
	plugin := &descriptorPlugin[T]{descriptor: descriptor, config: config}
	for _, requirement := range descriptor.Requires {
		if !requirement.Optional {
			plugin.inject = append(plugin.inject, requirement.Name)
		}
	}
	fiber, err := h.ctx.Plugin(plugin, nil)
	if err != nil {
		t.Fatalf("activate %s: %v", descriptor.ID, err)
	}
	if state := fiber.State(); state != cordis.Active {
		t.Fatalf("module %s state = %s (%v), want Active", descriptor.ID, state, fiber.ErrorDetail())
	}
}
