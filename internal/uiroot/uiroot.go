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
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// Settings keys for the chrome widths the user dragged with ui.Split. They are
// host-owned (the shell geometry), not a module's, so they live here rather
// than in contracts.
const (
	settingsKeySidebarWidth   = "ui.width.sidebar"
	settingsKeyInspectorWidth = "ui.width.inspector"
	// The chrome's discrete states persist too: the sidebar opens as the rail
	// the user left it, and the inspector reopens where they left it.
	settingsKeySidebarCollapsed = "ui.sidebar.collapsed"
	settingsKeyInspectorOpen    = "ui.inspector.open"
	// Width bounds keep a dragged pane usable; a saved value outside them is
	// clamped on load and the divider is clamped live while dragging.
	minSidebarWidth   float32 = 200
	maxSidebarWidth   float32 = 420
	minInspectorWidth float32 = 240
	maxInspectorWidth float32 = 560
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
	// OnLanguageChange is called on the UI thread after the product language
	// changes, so the host can mirror it into the framework's own locale: mygo
	// localizes its native controls, dialogs, menus and assistive-technology
	// strings separately from the product's copy (for example the splitter is
	// named by the framework), and without this hook a runtime switch would
	// leave those strings in the previous language. Nil is a no-op.
	OnLanguageChange func(lang string)
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
	// onLanguageChange mirrors a product language change into the framework
	// locale; the host wires it (Config.OnLanguageChange).
	onLanguageChange func(string)
	interaction      *interactionService
	artifacts        *artifactService
	plan             *planService
	goal             *goalService
	notifier         *notifierService

	mu         sync.Mutex
	invalidate func()
	frames     atomic.Uint64

	// settingsTab is host-owned UI state (which settings page is shown); it is
	// not part of any module.
	settingsTab int
	// settingsScrollToSelected scrolls the grouped settings navigation to the
	// selected page on the next frame, for deep links to pages far down the
	// list (About, Memory), which a click would have scrolled to by hand.
	settingsScrollToSelected bool
	// settingsQuery filters the grouped settings navigation (H5: the 19-page
	// list needs a search field, not only scrolling).
	settingsQuery string
	// settingsDiscard is the host's unsaved-changes guard: closing the settings
	// surface with a dirty page keeps it open and shows the discard bar.
	settingsDiscard bool
	// settingsAdvancedOpen controls the collapsed 高级 group in the settings
	// navigation: daily pages stay visible, while diagnostics/import/update and
	// the other low-frequency pages unfold on demand (the settings-complexity
	// simplification). Search always expands the group.
	settingsAdvancedOpen bool
	// settingsSelectedID is the page last shown, so selecting an advanced page
	// opens (but never re-opens) the advanced group; an explicit collapse stays
	// collapsed until the user chooses another page.
	settingsSelectedID string

	// hostCommands unregisters the host's own window commands; hostLang is the
	// language they were registered in, so a language switch re-registers them
	// with localized titles.
	hostCommands func()
	hostLang     string

	// rightOpen is the host-owned right inspector panel state (the old shell's
	// right sidebar). It is touched on the UI thread only.
	rightOpen bool

	// sidebarCollapsed is the host-owned left sidebar state: the old shell's
	// 64 DIP icon rail. It is toggled by Cmd/Ctrl+B or the brand-row control.
	sidebarCollapsed bool

	// inspectorToggle is the content header's inspector button; a close that
	// removes the focused control (the drawer, the pane header's X) asks for
	// the keyboard back on it, as a dialog gives it back to its opener.
	inspectorToggle ui.Handle
	// inspectorFocusPending asks the next pass to move the focus back to the
	// inspector toggle after such a close.
	inspectorFocusPending bool
	// inspectorDrawerShown records the presentation the last view built: a
	// close from the modal drawer must return the focus, while closing a docked
	// pane with the keyboard chord keeps whatever the user was focused on.
	inspectorDrawerShown bool
	// paneHintShown limits the "inspector is closed" toast to once per closed
	// episode, so a held pane-cycle shortcut does not repeat it.
	paneHintShown bool

	// sidebarWidth/inspectorWidth are the user's dragged pane widths (ui.Split
	// dividers); zero until loadChromeWidths reads the saved values on the first
	// frame. widthsLoaded keeps that read once per process.
	sidebarWidth   float32
	inspectorWidth float32
	widthsLoaded   bool

	// lastAnnouncedScreen/lastRightOpen drive the assistive-technology
	// announcements of navigation and inspector changes (c.Announce), which a
	// hand-built navigator does not provide by itself.
	lastAnnouncedScreen string
	lastRightOpen       bool

	// paletteOpen is the host-owned command palette state (Cmd/Ctrl+K) and
	// paletteQuery its filter draft. UI thread only.
	paletteOpen  bool
	paletteQuery string
	// paletteList/paletteSelected are the native list state of the palette:
	// its selected row follows the keyboard (Up/Down, Enter runs it).
	paletteList     ui.ListState
	paletteSelected int
	// paletteFilter is the query the selection was last reset for, and
	// paletteCount the number of matches the last frame showed, for the
	// Up/Down shortcuts that move the choice while the search field has the
	// focus.
	paletteFilter string
	paletteCount  int
}

// SidebarCollapsed reports whether the left sidebar is in its icon-rail form.
func (r *Root) SidebarCollapsed() bool { return r.sidebarCollapsed }

// SetSidebarCollapsed collapses the left sidebar to the 64 DIP icon rail or
// expands it, requesting a frame when the value changes. The state is part of
// the window layout the user left, so it persists in the host settings.
func (r *Root) SetSidebarCollapsed(collapsed bool) {
	if r.sidebarCollapsed == collapsed {
		return
	}
	r.sidebarCollapsed = collapsed
	r.saveSidebarCollapsed()
	r.requestFrame()
}

// ToggleSidebarCollapsed is the Cmd/Ctrl+B action.
func (r *Root) ToggleSidebarCollapsed() { r.SetSidebarCollapsed(!r.sidebarCollapsed) }

// RightPanelOpen reports whether the window-level right inspector panel is
// docked open. It is host UI state, not a module's.
func (r *Root) RightPanelOpen() bool { return r.rightOpen }

// SetRightPanelOpen docks or collapses the window-level right inspector panel
// and requests a frame when the value changes. The content-column toggle button
// calls the same state. It persists like the sidebar's collapsed state.
func (r *Root) SetRightPanelOpen(open bool) {
	r.setRightPanelOpen(open, false)
}

// setRightPanelOpen is SetRightPanelOpen with the close focus policy: a close
// that removes the focused control (the modal drawer, or the pane header's
// close button) asks the next frame to return the focus to the inspector
// toggle, as a dialog gives the focus back to the control that opened it.
func (r *Root) setRightPanelOpen(open bool, restoreFocus bool) {
	if r.rightOpen == open {
		return
	}
	r.rightOpen = open
	if open {
		r.paneHintShown = false
	} else if restoreFocus {
		r.inspectorFocusPending = true
	}
	r.saveInspectorOpen()
	r.requestFrame()
}

// loadChromeWidths reads the pane widths the user dragged with the ui.Split
// dividers, once per process, falling back to the brand defaults. Values are
// clamped so a stale or hand-edited settings entry cannot make a pane unusable.
func (r *Root) loadChromeWidths() {
	if r.widthsLoaded {
		return
	}
	r.widthsLoaded = true
	r.sidebarWidth = design.SidebarWidth
	r.inspectorWidth = design.InspectorWidth
	if r.settingsService == nil {
		return
	}
	if v, err := strconv.ParseFloat(r.settingsService.Get(settingsKeySidebarWidth, ""), 32); err == nil {
		r.sidebarWidth = clampWidth(float32(v), minSidebarWidth, maxSidebarWidth)
	}
	if v, err := strconv.ParseFloat(r.settingsService.Get(settingsKeyInspectorWidth, ""), 32); err == nil {
		r.inspectorWidth = clampWidth(float32(v), minInspectorWidth, maxInspectorWidth)
	}
	// The sidebar/inspector open states restore with the widths; a missing or
	// hand-edited value falls back to the closed defaults the shell always had.
	if v, ok := parseSettingBool(r.settingsService.Get(settingsKeySidebarCollapsed, "")); ok {
		r.sidebarCollapsed = v
	}
	if v, ok := parseSettingBool(r.settingsService.Get(settingsKeyInspectorOpen, "")); ok {
		r.rightOpen = v
	}
}

// parseSettingBool reads a persisted bool setting ("true"/"false", "1"/"0"),
// reporting whether it held a value the shell understands.
func parseSettingBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return false, false
}

// saveSidebarCollapsed persists the sidebar's rail/expand state.
func (r *Root) saveSidebarCollapsed() {
	if r.settingsService == nil {
		return
	}
	_ = r.settingsService.Set(settingsKeySidebarCollapsed, strconv.FormatBool(r.sidebarCollapsed))
}

// saveInspectorOpen persists whether the inspector reopens on the session
// screen after a restart.
func (r *Root) saveInspectorOpen() {
	if r.settingsService == nil {
		return
	}
	_ = r.settingsService.Set(settingsKeyInspectorOpen, strconv.FormatBool(r.rightOpen))
}

// saveChromeWidths persists the dragged pane widths, so the layout opens the
// way the user left it (docs/ui/split.md: keep the size in the app's state and
// save it to restore the layout when the app starts again).
func (r *Root) saveChromeWidths() {
	if r.settingsService == nil {
		return
	}
	_ = r.settingsService.Set(settingsKeySidebarWidth,
		strconv.FormatFloat(float64(clampWidth(r.sidebarWidth, minSidebarWidth, maxSidebarWidth)), 'f', 0, 32))
	_ = r.settingsService.Set(settingsKeyInspectorWidth,
		strconv.FormatFloat(float64(clampWidth(r.inspectorWidth, minInspectorWidth, maxInspectorWidth)), 'f', 0, 32))
}

// clampWidth keeps a dragged width within its usable range.
func clampWidth(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
		appName:          cfg.AppName,
		settingsService:  cfg.Settings,
		chatService:      chatService,
		kernelContext:    kernelContext,
		onLanguageChange: cfg.OnLanguageChange,
		fileDrop:         &fileDropService{},
		interaction:      newInteractionService(),
		artifacts:        newArtifactService(),
		plan:             newPlanService(),
		goal:             newGoalService(),
		notifier:         newNotifierService(),
	}
	r.theme = &themeService{root: r, dark: cfg.Dark}
	r.locale = &localeService{root: r, lang: cfg.Language}
	r.rt = newRuntime(r)
	r.nav = newNavigator(r)
	r.registerHostCommands()
	return r
}

// OpenSettingsPage opens the settings surface on a contributed page. Modules
// that link to a settings page (the composer's "open model settings", the
// provider presets) use it through an optional interface instead of opening
// settings on whichever page happened to be last.
func (r *Root) OpenSettingsPage(pageID string) error { return r.nav.OpenSettingsPage(pageID) }

// registerHostCommands adds the window-level commands the shell owns: the
// inspector toggle, so the shortcut and the command palette reach it like any
// other action. It re-registers when the language changes.
func (r *Root) registerHostCommands() {
	if r.hostCommands != nil {
		r.hostCommands()
		r.hostCommands = nil
	}
	r.hostLang = r.locale.Language()
	unregister, err := r.rt.RegisterCommand(contracts.CommandSpec{
		ID:       "uiroot.toggle-inspector",
		Title:    r.tr("切换右侧栏", "Toggle inspector"),
		Hint:     r.tr("打开或收起检查器", "Open or close the inspector"),
		Order:    900,
		Shortcut: "Cmd/Ctrl + Alt + I",
		Run: func() error {
			if !r.inspectorAvailable() {
				return nil
			}
			// The pane header's close control runs this command, so the focus
			// goes back to the content header toggle like a closed dialog's.
			r.setRightPanelOpen(!r.rightOpen, true)
			return nil
		},
	})
	if err != nil {
		// A command registry the host cannot extend is a programming error, not
		// a runtime condition; log it instead of failing the whole UI root.
		log.Printf("uiroot: register host command: %v", err)
		return
	}
	r.hostCommands = unregister
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
