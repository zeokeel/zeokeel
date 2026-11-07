// Package uiroot is ZeoKeel's UI entry point (the "UI root"): it owns the
// single mygo frame, the host-side layout, navigation, overlays, toasts, theme
// and locale, and the five contribution registries UI modules register into.
//
// Rules (see docs/ARCHITECTURE.md):
//   - Only this package creates the frame entry (ui.View) and window content.
//     UI modules contribute builders (contracts.BuildFunc) called inside it.
//   - Builders run on the UI thread, only inside a frame, and may run several
//     times per frame (mygo re-builds after events): they must be side-effect
//     free. Mutations happen in click handlers, commands or UIRuntime.Post.
//   - Registration happens during assembly/startup on the UI thread; unregister
//     functions must be handed to ctx.Effect so kernel disposal unwinds them.
//   - UI code uses mygo's own controls (ui.Sidebar, ui.Tabs, ui.Modal, ...);
//     do not hand-build widget equivalents from layout primitives.
package uiroot

import (
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Config configures the UI root.
type Config struct {
	AppName  string
	Dark     bool
	Language string
	// Settings is the host settings service: the frame reads the user's chrome
	// shortcut overrides from it every frame. Nil falls back to the defaults.
	Settings contracts.SettingsService
	// Chat is optional: when set, Cmd/Ctrl+N creates a session directly. The
	// process host normally leaves it nil and the shortcut falls back to the
	// registered zeokeel.session.new command (the session module owns chat).
	Chat contracts.ChatService
	// ChatService is an alias for Chat (embedders/tests may use either).
	ChatService contracts.ChatService
	// Kernel is the optional kernel root context: when set, Cmd/Ctrl+N resolves
	// KeyChat from it. Embedders may also bind it later with SetKernelContext.
	Kernel cordis.Context
	// KernelContext is an alias for Kernel.
	KernelContext cordis.Context
}

// Root is the host's UI entry point. It is created by the process host before
// the module assembly loads, so UI modules can require its services.
type Root struct {
	appName string

	rt              *runtime
	nav             *navigator
	theme           *themeService
	locale          *localeService
	settingsService contracts.SettingsService
	// chatService backs the Cmd/Ctrl+N shortcut when set by an embedder or
	// test; nil falls back to the registered new-session command.
	chatService contracts.ChatService
	// kernelContext is the optional kernel root for resolving KeyChat.
	kernelContext cordis.Context
	fileDrop      *fileDropService
	interaction   *interactionService
	artifacts     *artifactService
	plan          *planService
	goal          *goalService
	notifier      *notifierService

	mu         sync.Mutex
	invalidate func()
	frames     atomic.Uint64

	// settingsTab is host-owned UI state (which settings page is shown); it is
	// not part of any module.
	settingsTab int

	// rightOpen is the host-owned right inspector panel state (the old shell's
	// right sidebar). It is touched on the UI thread only.
	rightOpen bool

	// sidebarCollapsed is the host-owned left sidebar state: the old shell's
	// 64 DIP icon rail. It is toggled by Cmd/Ctrl+B or the brand-row control.
	sidebarCollapsed bool

	// paletteOpen is the host-owned command palette state (Cmd/Ctrl+K) and
	// paletteQuery its filter draft. UI thread only.
	paletteOpen  bool
	paletteQuery string
}

// SidebarCollapsed reports whether the left sidebar is in its icon-rail form.
func (r *Root) SidebarCollapsed() bool { return r.sidebarCollapsed }

// SetSidebarCollapsed collapses the left sidebar to the 64 DIP icon rail or
// expands it, requesting a frame when the value changes.
func (r *Root) SetSidebarCollapsed(collapsed bool) {
	if r.sidebarCollapsed == collapsed {
		return
	}
	r.sidebarCollapsed = collapsed
	r.requestFrame()
}

// ToggleSidebarCollapsed is the Cmd/Ctrl+B action.
func (r *Root) ToggleSidebarCollapsed() { r.SetSidebarCollapsed(!r.sidebarCollapsed) }

// RightPanelOpen reports whether the window-level right inspector panel is
// docked open. It is host UI state, not a module's.
func (r *Root) RightPanelOpen() bool { return r.rightOpen }

// SetRightPanelOpen docks or collapses the window-level right inspector panel
// and requests a frame when the value changes. The content-column toggle button
// calls the same state.
func (r *Root) SetRightPanelOpen(open bool) {
	if r.rightOpen == open {
		return
	}
	r.rightOpen = open
	r.requestFrame()
}

// New creates the UI root. It registers no screens: screen/slot contributions
// arrive from UI modules during assembly.
func New(cfg Config) *Root {
	if cfg.AppName == "" {
		cfg.AppName = "ZeoKeel"
	}
	if cfg.Language == "" {
		cfg.Language = "zh"
	}
	chatService := cfg.Chat
	if chatService == nil {
		chatService = cfg.ChatService
	}
	kernelContext := cfg.Kernel
	if kernelContext == (cordis.Context{}) {
		kernelContext = cfg.KernelContext
	}
	r := &Root{
		appName:         cfg.AppName,
		settingsService: cfg.Settings,
		chatService:     chatService,
		kernelContext:   kernelContext,
		fileDrop:        &fileDropService{},
		interaction:     newInteractionService(),
		artifacts:       newArtifactService(),
		plan:            newPlanService(),
		goal:            newGoalService(),
		notifier:        newNotifierService(),
	}
	r.theme = &themeService{root: r, dark: cfg.Dark}
	r.locale = &localeService{root: r, lang: cfg.Language}
	r.rt = newRuntime(r)
	r.nav = newNavigator(r)
	return r
}

// SetChatService installs the optional chat backend used by Cmd/Ctrl+N. The
// desktop host relies on the registered new-session command instead; embedders
// and tests may set one directly. A nil value clears it.
func (r *Root) SetChatService(chat contracts.ChatService) {
	r.mu.Lock()
	r.chatService = chat
	r.mu.Unlock()
}

// BindChat is an alias for SetChatService.
func (r *Root) BindChat(chat contracts.ChatService) { r.SetChatService(chat) }

// SetKernelContext installs the optional kernel root used by window shortcuts
// (Cmd/Ctrl+N resolves contracts.KeyChat from it). The desktop host may leave it
// unset; the shortcut then falls back to the registered new-session command.
func (r *Root) SetKernelContext(ctx cordis.Context) {
	r.mu.Lock()
	r.kernelContext = ctx
	r.mu.Unlock()
}

// BindKernel is an alias for SetKernelContext.
func (r *Root) BindKernel(ctx cordis.Context) { r.SetKernelContext(ctx) }

// SetKernel is an alias for SetKernelContext.
func (r *Root) SetKernel(ctx cordis.Context) { r.SetKernelContext(ctx) }

// SetChat is an alias for SetChatService.
func (r *Root) SetChat(chat contracts.ChatService) { r.SetChatService(chat) }

// AppName returns the product name shown in the title bar and UI.
func (r *Root) AppName() string { return r.appName }

// Runtime returns the host UI runtime service (contracts.KeyUIRuntime).
func (r *Root) Runtime() contracts.UIRuntime { return r.rt }

// Navigator returns the host navigation service (contracts.KeyNavigator).
func (r *Root) Navigator() contracts.Navigator { return r.nav }

// Theme returns the host theme service (contracts.KeyTheme).
func (r *Root) Theme() contracts.ThemeService { return r.theme }

// Locale returns the host locale service (contracts.KeyLocale).
func (r *Root) Locale() contracts.LocaleService { return r.locale }

// ScreenLister returns the read-only screen list service
// (contracts.KeyScreenLister).
func (r *Root) ScreenLister() contracts.ScreenLister { return r.rt }

// SetInvalidator installs the callback that requests a frame; the process host
// wires it to the window. It may be called from any goroutine.
func (r *Root) SetInvalidator(fn func()) {
	r.mu.Lock()
	r.invalidate = fn
	r.mu.Unlock()
}

// requestFrame asks for a repaint; safe from any goroutine.
func (r *Root) requestFrame() {
	r.mu.Lock()
	fn := r.invalidate
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Frames reports how many frames have been built (tests and diagnostics).
func (r *Root) Frames() uint64 { return r.frames.Load() }

// Validate checks the host state before the window is created. It fails when no
// screen is registered (a headless product should skip this call).
func (r *Root) Validate() error { return r.rt.validate() }

// View is the single mygo view function: the UI entry point of the product.
func (r *Root) View(c *ui.Context) { r.view(c) }
