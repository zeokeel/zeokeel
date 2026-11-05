package shell

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// Screen identifies the top-level view.
type Screen int

const (
	Home Screen = iota
	Session
	Plugins
	Settings
	Automation
)

// App is the shell's state.
type App struct {
	Screen Screen
	// settingsReturn is the screen the Settings modal was opened from; the
	// modal is an overlay, so closing it returns here instead of always Home.
	// The zero value is Home, which keeps a directly-opened static Settings
	// screen (App{Screen: Settings}) closing to Home as before.
	settingsReturn Screen

	// Language forces the UI language. Empty means automatic: a static App
	// (Ctrl nil, no settings store) renders English for the pixel baseline,
	// while a live App starts Chinese unless ui.language explicitly says en.
	Language Lang

	// Ctrl is the live session controller; nil runs the static demo shell
	// (used by the pixel tests and before services start).
	Ctrl *session.Controller
	// teamRoster returns the current session's live asynchronous team members
	// (spawn_team members). nil keeps the old behavior of showing only the
	// synchronous subagent calls the controller folded into the session.
	teamRoster func() []tools.Teammate
	// teamStop cancels the asynchronous team members a session spawned; nil
	// means stopping a turn does not touch the team (static/test shells).
	teamStop func(sessionID string)
	// draft is the composer text of the screen currently shown. Live drafts
	// are scoped per surface: drafts keeps one buffer per session id and
	// homeDraft keeps the Home/new-task buffer, so switching sessions never
	// carries text into an existing conversation. The static App never reads
	// these.
	draft     string
	drafts    map[string]string
	homeDraft string
	// homeAttachments holds the files staged on the Home composer before
	// Home Send creates their session. The Home draft owns them: switching
	// sessions never moves them into an existing conversation, Send copies
	// them onto the new session before its first Submit, and the chips above
	// the Home composer remove them from this list.
	homeAttachments   []homeAttachment
	homeAttachmentSeq int
	// lastDraftScreen/lastDraftSession track where draft currently belongs;
	// syncDraftScope moves it when the screen or the current session changes.
	lastDraftScreen  Screen
	lastDraftSession string
	draftInitialized bool
	// homeMode, homePermission and homeModel stage the Home composer's
	// new-task choices. They apply only to the session the Home composer
	// creates; they never mutate the last-opened session or the global
	// default.
	homeMode       string
	homePermission string
	homeModel      string
	// attachOpen shows the composer's attachment menu. It is live-only state:
	// the static fallback never renders it.
	attachOpen bool
	snap       atomic.Pointer[state.Snapshot]
	// prefs is the desktop preference snapshot the current frame runs with.
	// View refreshes it each frame: the theme (TextScale, HighContrast) and
	// the conversation text scale derive from it, and ReduceMotion turns the
	// frame's animations off. See preferences.go.
	prefs ui.Preferences

	// settings is an optional settings store override, used by tests. When
	// nil, a live App reads and writes through Ctrl.Settings().
	settings settingsAPI
	// The loaded settings are cached for the lifetime of the view: the store
	// is only consulted once unless the user changes a value.
	settingsLoaded bool
	settingsValue  session.Settings
	settingsErr    error

	// Settings window host hooks. internal/app sets them so Settings is an
	// independent desktop window instead of the in-app modal overlay; a nil
	// opener keeps the overlay, which is what headless shell tests and the
	// fallback path use. The opener reports whether it could show the window:
	// false falls back to the overlay on a host that cannot create one.
	settingsWindowOpen  func() bool
	settingsWindowClose func()
	// settingsChangedHook runs after a setting is saved, so a host with several
	// windows (the main one and the settings window) can repaint them all.
	settingsChangedHook func()

	// langLoaded/langValue cache the resolved UI language. updateSettings
	// clears them so a language switch applies on the next frame.
	langLoaded bool
	langValue  Lang
	// langPersisted records that a live App has written (or found) the
	// resolved ui.language on its first frame.
	langPersisted bool

	// Overlay and menu state. All of it is false in the static App the pixel
	// tests render, so the baseline path stays pixel-identical.
	moreOpen        bool
	permissionOpen  bool
	languageOpen    bool
	fontSizeOpen    bool
	workOpen        bool
	collapseOpen    bool
	sendOpen        bool
	performanceOpen bool

	aboutOpen     bool
	shortcutsOpen bool
	// The shortcut editor: query, the row being edited and its draft. Overrides
	// persist as keybinding.<id> settings and apply to the next frame.
	shortcutsQuery           string
	shortcutEditID           string
	shortcutDraft            string
	shortcutEditErr          string
	shortcutEditRef          ui.Handle
	shortcutKeymap           map[string]string
	shortcutsOverridesLoaded bool
	paletteOpen              bool
	paletteQuery             string

	renameOpen  bool
	renameID    string
	renameDraft string

	deleteOpen  bool
	deleteID    string
	deleteTitle string

	// Destructive confirmations beyond the session/workspace ones: the
	// Automation task delete and the Memory "clear all". They reuse the same
	// modal pattern (flag, what is targeted, a ui.Modal rendered by the page
	// that owns the action), so cancel/Esc/backdrop all dismiss without
	// touching the store and the danger button is never the default focus.
	automationDeleteOpen  bool
	automationDeleteID    string
	automationDeleteTitle string
	memoryClearOpen       bool

	// composerRef is the live composer's focus handle. session_live.go binds it
	// when the session renderer builds the input; the Cmd/Ctrl+K shortcut
	// focuses it when bound. mentionRef is the first row of the "@" menu, so
	// Tab can move from the composer into the list while it is open.
	composerRef ui.Handle
	mentionRef  ui.Handle

	// PickDirectory is the native directory picker the Add workspace dialog
	// uses. Tests inject a fake so no desktop dialog is needed; nil falls back
	// to mygo.Dialog.
	PickDirectory func() (string, error)
	// PickFiles is the native multi-file picker the composer's Files action
	// uses. Tests inject a fake so no desktop dialog is needed; nil falls back
	// to mygo.Dialog.
	PickFiles func() ([]string, error)
	// SaveSessionLog is the save-dialog seam the session-header download uses
	// to choose the JSONL export path. Tests inject a fake; nil falls back to a
	// deterministic file under the workspace root.
	SaveSessionLog func(suggestedPath string) (string, error)
	// menuAction receives every native-menu command routed through
	// HandleMenuAction. The desktop shell registers it with SetMenuAction, so
	// internal/shell never imports mygo or internal/app; a nil handler leaves
	// process-level commands (Quit) inert, which is what headless tests want.
	menuAction func(action string)
	// focusComposerOnFrame asks the next frame to focus the live composer.
	// The native Search menu command sets it on the session screen so its
	// Cmd/Ctrl+K accelerator behaves like the window shortcut.
	focusComposerOnFrame bool

	// Workspace and session-search state. All of it is default-off in the
	// static App{Ctrl: nil}, so the baseline render path is untouched.
	workspaceRestored bool
	wsCollapsed       map[string]bool
	wsPickerOpen      bool
	wsDialogOpen      bool
	wsEditID          string // non-empty while renaming
	wsDeleteOpen      bool
	wsDeleteID        string
	wsDeleteName      string
	wsName            string
	wsRoot            string
	wsErr             string

	viewOptionsOpen bool
	showArchived    bool
	manualSort      bool
	// groupBy/archiveFilter are DSH's View options: how the sidebar groups
	// sessions and which archive state it shows. Both persist.
	groupBy           string
	archiveFilter     string
	inlineSearch      bool
	inlineQuery       string
	viewOptionsLoaded bool
	searchIndex       int

	// rightOpen shows the live right sidebar on the Session screen.
	rightOpen   bool
	rightTab    rightTab
	filesDir    string
	filePath    string
	fileText    string
	fileTrunc   bool
	fileLimited bool
	fileErr     string
	termInput   string
	termOutput  string
	termMu      sync.Mutex
	termRunning bool
	termCancel  context.CancelFunc

	// Chrome state: the collapsible left sidebar, the right panel's split
	// and fullscreen toggles, and the workspace hover card. All of it is
	// live-only (Ctrl != nil) and in-memory; the static App{Ctrl: nil}
	// baseline never reads it, so its pixels are unchanged.
	sidebarCollapsed bool
	rightSplit       bool
	rightSplitPct    float32
	rightFullscreen  bool
	wsHoverID        string
	// wsHoverCloseAt is when a hover card the pointer left closes; the
	// delay covers the gap between a row and the card beside it.
	wsHoverCloseAt time.Time
	// sidebarSel is the native Sidebar's choice, as "nav:plugins",
	// "nav:automation", "ws:<id>" or "s:<id>". The sidebar recomputes it from
	// the screen each frame and turns a new choice into navigation once the
	// Sidebar is built (sidebarCanonicalSelection/sidebarChoice).
	sidebarSel string
	// sidebarChosenByPointer records that the choice came from a click on a
	// row this frame, so a click can collapse the open workspace while the
	// arrow keys only move the selection.
	sidebarChosenByPointer bool

	// Workspace ordering comes from the ui.workspace_order settings key,
	// read once and cached; drag/drop writes it back. Pinned sessions come
	// from session.<id>.pinned and are cached per session.
	workspaceOrderLoaded bool
	workspaceOrder       []string
	pinnedSessions       map[string]bool
	// sessionOrder is the persisted Manual order of session ids
	// (ui.session_order), read once and rewritten by drag/drop or Move up/down.
	sessionOrderLoaded bool
	sessionOrder       []string
}

// SetSnapshot is called from the controller's update goroutine; the view
// reads it per frame.
func (a *App) SetSnapshot(s *state.Snapshot) { a.snap.Store(s) }

// Snapshot returns the latest live snapshot, or nil in static mode.
func (a *App) Snapshot() *state.Snapshot { return a.snap.Load() }

// View draws the shell for the current screen. The shell is live-only since
// pixel parity retired: an App without a controller draws an empty frame
// (tests use the live fixture helpers).
func (a *App) View(c *ui.Context) {
	if a.Ctrl == nil {
		ui.Row(c).Fill()
		return
	}
	// Show a toast a key handler queued while no frame was building, so the
	// feedback of a command run with Enter is not silently dropped.
	a.flushDeferredToast(c)
	// The desktop's preferences come first: ReduceMotion must switch the
	// frame's animations off before any element is built.
	a.syncShellPreferences(c)
	a.syncDraftScope()
	a.syncComposerMenuDismissal()
	a.persistResolvedLanguage()
	a.installTaskShortcuts(c)
	a.installChromeShortcuts(c)
	a.restoreWorkspace()
	a.restoreViewOptions()

	// A collapsed right panel forgets its split/fullscreen mode, so
	// reopening it starts at the normal width. Live-only: the static
	// baseline leaves these untouched.
	if a.Ctrl != nil && !a.rightOpen {
		a.rightFullscreen = false
		a.rightSplit = false
	}

	// The frame starts with the desktop's appearance; the persisted theme
	// choice overrides it here, so Light/Dark/System apply on the next frame
	// after the user picks one.
	t := a.themeTokens(c)
	c.SetTheme(shellTheme(t, c.Preferences()))

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c, t)
		if a.rightSidebarMaximized() {
			// Fullscreen: the right panel takes the whole content column
			// and the screen behind it is not built.
			a.rightSidebar(c, t)
			return
		}
		if a.rightSidebarSplit() {
			// Split: a real half/half pane pair with the draggable
			// divider mygo's SplitPercent provides. The right panel is
			// its second pane, so both sides share the content column.
			if a.rightSplitPct == 0 {
				a.rightSplitPct = 50
			}
			ui.SplitPercent(c, &a.rightSplitPct,
				func() { a.mainScreen(c, t) },
				func() { a.rightSidebar(c, t) }).Grow(1).FillHeight()
			return
		}
		a.mainScreen(c, t)
		// The right sidebar is a workspace-wide panel in DSH: once opened it
		// stays docked across Home, Session, Plugins and Automation until the
		// user collapses it, and its Split/Fullscreen modes work on them too.
		if a.Ctrl != nil && a.rightOpen {
			a.rightSidebar(c, t)
		}
	})
	if a.Screen == Settings {
		a.settingsOverlay(c, t)
		// The settings modal makes the window shortcuts wait, so keep the
		// sidebar shortcut (and therefore the way back from a collapsed
		// sidebar) available above it.
		a.chromeSettingsShortcuts(c)
	}
	a.shellOverlays(c, t)

	// A native-menu command that needs the composer focused (Search on the
	// session screen) could not call Focus when it ran: the handle is only
	// bound while the session composer renders. Focus it now that this frame's
	// tree exists.
	if a.focusComposerOnFrame {
		a.focusComposerOnFrame = false
		a.focusComposer(c)
	}
}

// backgroundScreen is the screen behind the Settings modal. Settings is an
// overlay, not a destination: opening it leaves the current screen mounted so
// the live UI (and DSH) keep the session visible behind the dialog.
func (a *App) backgroundScreen() Screen {
	if a.Screen != Settings {
		return a.Screen
	}
	switch a.settingsReturn {
	case Home, Session, Plugins, Automation:
		return a.settingsReturn
	default:
		return Home
	}
}

// openSettings shows the settings UI. With a host opener registered (the
// desktop app), Settings opens as its own non-modal window and this frame
// keeps the screen it was on; without one, or when the host cannot show the
// window, the in-app modal overlay is the fallback.
func (a *App) openSettings() {
	a.moreOpen = false
	a.closeSelectors()
	if a.settingsWindowOpen != nil && a.settingsWindowOpen() {
		return
	}
	if a.Screen != Settings {
		a.settingsReturn = a.Screen
	}
	a.Screen = Settings
}

// SetSettingsWindowOpener registers the host that shows, or focuses, the
// independent settings window. It reports whether the window is on screen;
// false keeps the in-app overlay as the fallback.
func (a *App) SetSettingsWindowOpener(fn func() bool) { a.settingsWindowOpen = fn }

// SetSettingsWindowCloser registers how the settings UI's Close controls close
// the independent window (they hide it; the app keeps running). A nil handler
// closes the in-app overlay instead.
func (a *App) SetSettingsWindowCloser(fn func()) { a.settingsWindowClose = fn }

// SetSettingsChangedHook registers a callback that runs after a setting is
// saved. internal/app uses it to repaint the main window when the settings
// window changes the theme, the language or a conversation size.
func (a *App) SetSettingsChangedHook(fn func()) { a.settingsChangedHook = fn }

// closeSettingsRequested runs the settings UI's Close controls: in the
// independent settings window it hides that window, and in the in-app modal it
// dismisses the overlay.
func (a *App) closeSettingsRequested() {
	if a.settingsWindowClose != nil {
		a.settingsWindowClose()
		return
	}
	a.closeSettings()
}

// closeSettings dismisses the modal and restores the screen it was opened
// from. A Settings screen constructed directly (the static baseline) closes to
// Home because settingsReturn's zero value is Home.
func (a *App) closeSettings() {
	ret := a.settingsReturn
	switch ret {
	case Home, Session, Plugins, Automation:
	default:
		ret = Home
	}
	a.Screen = ret
	a.settingsReturn = Home
}

// mainScreen draws the current screen's content column.
func (a *App) mainScreen(c *ui.Context, t *design.Tokens) {
	switch a.backgroundScreen() {
	case Session:
		a.applyToolCardDefaults(a.currentSnapshot())
		a.session(c, t)
	case Plugins:
		a.plugins(c, t)
	case Automation:
		a.automation(c, t)
	default:
		a.home(c, t)
	}
}

// sidebar draws the left sidebar: the brand row, the primary New Session
// action, the native mygo Sidebar carrying the app destinations and the
// workspace → session tree, and the More account footer. The Sidebar owns the
// selection, the arrow keys, the first-letter search, the scroll and the
// accessibility tree; the rows compose native controls for their inline
// actions instead of hand-drawn DSH rows.
func (a *App) sidebar(c *ui.Context, t *design.Tokens) {
	// A live shell can collapse to the icon rail; the reopen control and
	// Cmd/Ctrl+B stay reachable there.
	if a.Ctrl != nil && a.sidebarCollapsed {
		a.sidebarRail(c, t)
		return
	}
	// Recompute the native Sidebar's choice from the screen before building
	// it: a click or an arrow key writes a new choice while the rows build,
	// and sidebarChoice turns it into navigation right after.
	a.sidebarSel = a.sidebarCanonicalSelection()
	a.sidebarChosenByPointer = false
	design.Apply(ui.Column(c), t.Sidebar()).Children(func() {
		a.sidebarBrandRow(c, t)
		a.sidebarNewSession(c, t)
		sidebar := ui.Sidebar(c, &a.sidebarSel, func() {
			a.sidebarNavItems(c, t)
			a.sidebarWorkspacesHeader(c, t)
			a.sidebarTree(c, t)
		}).Grow(1).Label(a.tr("Sidebar", "侧边栏")).FocusRing(false)
		// The column already paints the brand sidebar surface: keep the
		// native Sidebar on it instead of mygo's default control surface.
		sidebar = sidebar.Background(t.BgSidebar)
		if sidebar.Changed() {
			a.sidebarChoice(c, t)
		}
		a.moreFooter(c, t)
	})
}

// The selected workspace row stays on the sidebar color in DSH and only
// its folder glyph turns accent; the selected session row carries the fill.
