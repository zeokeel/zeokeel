package rightpanel_test

// Shared headless harness for the right sidebar frame tests: the host frame
// (uiroot.View) renders a real screen that draws the right-panel slot, so
// every assertion is about visible text and clicks go through the native tab
// strip. The workspace/terminal/artifact backends are scripted per test.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

type harnessOptions struct {
	Workspace contracts.WorkspaceService
	Terminal  contracts.TerminalService
	Artifacts contracts.ArtifactPresenter
	Language  string
	Config    rightpanel.Config
}

type harness struct {
	t       *testing.T
	ctx     cordis.Context
	uiRoot  *uiroot.Root
	fiber   *cordis.Fiber
	options harnessOptions
}

// newHarness builds the product frame with the right sidebar module and the
// optional workspace/terminal/artifact backends. The screen renders the
// right-panel slot directly (as the session screen does).
func newHarness(t *testing.T, options harnessOptions) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: options.Language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	if options.Workspace != nil {
		provide[contracts.WorkspaceService](t, ctx, contracts.KeyWorkspace, options.Workspace)
	}
	if options.Terminal != nil {
		provide[contracts.TerminalService](t, ctx, contracts.KeyTerminal, options.Terminal)
	}
	if options.Artifacts != nil {
		provide[contracts.ArtifactPresenter](t, ctx, contracts.KeyArtifactPresenter, options.Artifacts)
	}

	// The host owns the inspector: the screen itself is empty and the sidebar
	// renders through the real docked-pane/inspector slot path, exactly like
	// the conversation screen's right panel in the product.
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "session",
		Title: "会话",
		Build: func(c *ui.Context) {},
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("open session screen: %v", err)
	}

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, options: options}
	h.fiber = activate(t, ctx, rightpanel.Module(), options.Config)
	uiRoot.SetRightPanelOpen(true)
	return h
}

func (h *harness) tester() *ui.Tester { return ui.NewTester(h.uiRoot.View, 1200, 800) }

// previewer returns the contracts.FilePreviewer the module provides.
func (h *harness) previewer() contracts.FilePreviewer {
	h.t.Helper()
	service, ok := h.ctx.Get(contracts.KeyFilePreview)
	if !ok || service == nil {
		h.t.Fatal("the right sidebar module did not provide contracts.KeyFilePreview")
	}
	return service
}

// command returns one registered command by id.
func (h *harness) command(tester *ui.Tester, id string) contracts.CommandSpec {
	h.t.Helper()
	tester.Frame()
	for _, spec := range h.uiRoot.Runtime().Commands() {
		if spec.ID == id {
			return spec
		}
	}
	h.t.Fatalf("command %q is not registered; commands = %v", id, h.uiRoot.Runtime().Commands())
	return contracts.CommandSpec{}
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if !pumpFrames(tester, func() bool { _, ok := tester.Find(label); return ok }, 3*time.Second) {
		h.t.Fatalf("click target %q not found; texts = %v", label, tester.Texts())
	}
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// assertTexts waits for every wanted text: the workspace tree/preview reads run
// off the UI thread, so the frame right after a click can still be a loading
// state. Waiting on observable text keeps the assertion deterministic.
func (h *harness) assertTexts(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	settled := pumpFrames(tester, func() bool {
		for _, want := range wanted {
			if !tester.HasText(want) {
				return false
			}
		}
		return true
	}, 3*time.Second)
	if !settled {
		h.t.Fatalf("frame does not show %v; texts = %v", wanted, tester.Texts())
	}
}

func (h *harness) assertNoTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	settled := pumpFrames(tester, func() bool {
		for _, text := range unwanted {
			if tester.HasText(text) {
				return false
			}
		}
		return true
	}, 3*time.Second)
	if !settled {
		h.t.Fatalf("frame unexpectedly shows %v; texts = %v", unwanted, tester.Texts())
	}
}

// openPage switches the sidebar to one of the fixed pages by clicking its tab.
func (h *harness) openPage(tester *ui.Tester, label string) {
	h.t.Helper()
	h.click(tester, label)
}

// closeFileTab clicks one file tab's close control.
func (h *harness) closeFileTab(tester *ui.Tester, name string) {
	h.t.Helper()
	h.click(tester, "关闭 "+name)
}

// joinTexts joins the frame's painted texts for containment assertions.
func joinTexts(texts []string) string { return strings.Join(texts, "\n") }

// countOf counts a substring in the frame's painted texts.
func countOf(texts []string, want string) int { return strings.Count(joinTexts(texts), want) }

func pumpFrames(tester *ui.Tester, ok func() bool, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		tester.Frame()
		if ok() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
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

// --- module metadata --------------------------------------------------------

func TestModuleMetadata(t *testing.T) {
	if rightpanel.ModuleID != "zeokeel-ui/rightpanel" {
		t.Fatalf("ModuleID = %q", rightpanel.ModuleID)
	}
	if rightpanel.SlotItemID != "rightpanel.inspector" {
		t.Fatalf("SlotItemID = %q", rightpanel.SlotItemID)
	}
	descriptor := rightpanel.Module()
	requires := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		requires[requirement.Name] = true
	}
	if !requires[contracts.KeyUIRuntime.Name()] {
		t.Fatalf("descriptor requires = %v, want UIRuntime", descriptor.Requires)
	}
	provides := make(map[string]bool, len(descriptor.Provides))
	for _, provider := range descriptor.Provides {
		provides[provider.Name] = true
	}
	if !provides[contracts.KeyFilePreview.Name()] {
		t.Fatalf("descriptor provides = %v, want FilePreviewer", descriptor.Provides)
	}
}

// TestFilePreviewerIsProvided pins the service key other modules consume.
func TestFilePreviewerIsProvided(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	service := h.previewer()
	if service == nil {
		t.Fatal("FilePreviewer is nil")
	}
	if err := service.OpenFiles(); err != nil {
		t.Fatalf("OpenFiles: %v", err)
	}
	if err := service.OpenTerminal(); err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
}

// TestDisposeUnwindsContributions: disposing the module unregisters the slot
// and the provided previewer.
func TestDisposeUnwindsContributions(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.assertTexts(tester, "开始")

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "工作区文件", "新建终端")
	if service, ok := h.ctx.Get(contracts.KeyFilePreview); ok && service != nil {
		t.Fatal("the file previewer is still provided after dispose")
	}
}
