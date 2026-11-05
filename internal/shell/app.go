package shell

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
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

	// settings is an optional settings store override, used by tests. When
	// nil, a live App reads and writes through Ctrl.Settings().
	settings settingsAPI
	// The loaded settings are cached for the lifetime of the view: the store
	// is only consulted once unless the user changes a value.
	settingsLoaded bool
	settingsValue  session.Settings
	settingsErr    error

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
	c.SetTheme(t.Theme())

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

// openSettings shows the modal over the current screen.
func (a *App) openSettings() {
	if a.Screen != Settings {
		a.settingsReturn = a.Screen
	}
	a.moreOpen = false
	a.closeSelectors()
	a.Screen = Settings
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

func (a *App) sidebar(c *ui.Context, t *design.Tokens) {
	// The live shell can collapse to a narrow icon rail; the static demo
	// shell always keeps the full sidebar so its pixels stay fixed.
	if a.Ctrl != nil && a.sidebarCollapsed {
		a.sidebarRail(c, t)
		return
	}
	design.Apply(ui.Column(c), t.Sidebar()).Children(func() {
		// Brand row: DSH logoRow (y=6, 60 high, 12px side inset).
		ui.Row(c).Height(60).Margin(6, 12, 0, 12).PaddingX(design.Space1).
			AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Image(c, mark).Width(24).Height(24)
			ui.Text(c, "ZeoKeel").FontSize(17).FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
			if a.Ctrl != nil {
				a.collapseSidebarButton(c, t)
			} else {
				ui.Icon(c, icons.PanelLeft()).FontSize(16).TextColor(t.LabelSecondary)
			}
		})
		// Primary action: DSH 252x38 at y=70, 14px side inset. The column
		// has no bottom padding: the nav column's 12px top padding then
		// starts its rows at the baseline's y=120.
		ui.Column(c).Padding(design.Space1, 14, 0, 14).Children(func() {
			b := design.Apply(ui.Button(c, ""), t.PrimaryButton()).
				Width(252).Label(a.T("shell.new_session"))
			b.Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icons.CirclePlus()).FontSize(15).TextColor(t.LabelPrimary)
					ui.Text(c, a.T("shell.new_session")).FontSize(14).FixedLineHeight(22).
						FontWeight(500).TextColor(t.LabelPrimary)
				})
			})
			if b.Clicked() {
				a.newSession()
			}
		})
		// Nav rows: y=120, 252 wide, 36 high, 4px gap. No bottom padding so
		// the Workspaces header below starts at the baseline's y=206.
		ui.Column(c).Padding(design.Space3, 14, 0, 14).Gap(design.Space1).Children(func() {
			a.navRow(c, t, icons.Plugin(), a.T("shell.plugins"), a.Screen == Plugins, Plugins)
			a.navRow(c, t, icons.Clock(), a.T("shell.automation_tasks"), a.Screen == Automation, Automation)
		})
		// Workspaces header: y=206, 36 high, 260 wide (12/8 inset).
		// 4px left / 3px right padding puts the three 28px hit areas at
		// x=176/208/240 and their right edge at 268, matching the baseline.
		ui.Row(c).Height(36).Margin(10, 8, 0, 12).Padding(0, 3, 0, design.Space1).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.T("shell.workspaces")).FontSize(14).FixedLineHeight(20).TextColor(t.LabelTertiary).Grow(1)
			if a.Ctrl != nil {
				ui.Row(c).Gap(design.Space1).AlignItems(ui.Center).Children(func() {
					search := ui.Row(c).Size(28, 28).Center().
						Label(a.T("shell.search_sessions")).Role(ui.RoleButton).Focusable()
					search.Children(func() {
						ui.Icon(c, icons.Search()).FontSize(14).TextColor(t.LabelTertiary)
					})
					search.OnClick(func() { a.openInlineSearch() })
					sliders := ui.Row(c).Size(28, 28).Center().
						Label(a.T("shell.view_options")).Role(ui.RoleButton).Focusable()
					sliders.Children(func() {
						ui.Icon(c, icons.Sliders()).FontSize(14).TextColor(t.LabelTertiary)
					})
					sliders.OnClick(func() { a.viewOptionsOpen = !a.viewOptionsOpen })
					// DSH anchors the 268px popover to the sidebar's right
					// edge (x=12..280), not under the trigger at x=208 where
					// it would overlap the main column.
					ui.PopoverBase(c, sliders, &a.viewOptionsOpen, func(panel ui.Element) {
						panel.Background(settingsPanelColor(t)).Border(1, t.BorderL2).
							Radius(design.RadiusLG).AttachTo(sliders, ui.AnchorBottomStart, ui.AnchorTopStart).
							MarginStart(-196)
						panel.Children(func() { a.viewOptionsPopover(c, t) })
					})
					plus := ui.Row(c).Size(28, 28).Center().
						Label(a.T("shell.add_workspace")).Role(ui.RoleButton).Focusable()
					plus.Children(func() {
						ui.Icon(c, icons.FolderPlus()).FontSize(14).TextColor(t.LabelTertiary)
					})
					plus.OnClick(func() { a.beginAddWorkspace() })
				})
			} else {
				// The static demo matches the live 28x28 hit areas (four pixels
				// apart, right edge at x=268) so the controls clear WCAG's 24px
				// minimum target size instead of rendering three packed glyphs.
				ui.Row(c).Gap(design.Space1).AlignItems(ui.Center).Children(func() {
					for _, control := range []struct {
						icon  *ui.SVG
						label string
					}{
						{icons.Search(), a.T("shell.search_sessions")},
						{icons.Sliders(), a.T("shell.view_options")},
						{icons.FolderPlus(), a.T("shell.add_workspace")},
					} {
						ui.Row(c).Size(28, 28).Center().Label(control.label).
							Role(ui.RoleButton).Children(func() {
							ui.Icon(c, control.icon).FontSize(14).TextColor(t.LabelTertiary)
						})
					}
				})
			}
		})
		// DSH's Search sessions expands inline under the Workspaces header;
		// it is not a modal. The Cmd/Ctrl+K palette stays a separate surface.
		if a.Ctrl != nil && a.inlineSearch {
			a.sidebarSearchField(c, t)
		}
		// Workspace rows. The static demo path keeps its fixed list so the
		// pixel baseline is untouched; a live shell lists real sessions. The
		// gaps mirror the live rows: 2px under a workspace row and between
		// its sessions, 4px between workspace groups (baseline 00-home:
		// mygo 246, sessions 282/316, ZeoKeel-Super-Agent 352).
		if a.Ctrl != nil {
			a.workspaceSidebar(c, t)
		} else {
			ui.Column(c).PaddingX(design.Space3).PaddingY(design.Space1).
				Gap(design.Space1).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					a.workspaceRow(c, t, "mygo", true)
					a.sessionRow(c, t, a.T("shell.new_session"), a.T("common.now"), true)
					a.sessionRow(c, t, a.T("common.untitled"), a.T("time.minutes_short", 6), false)
				})
				a.workspaceRow(c, t, "ZeoKeel-Super-Agent", false)
				a.workspaceRow(c, t, "ZeoKeel", false)
				a.workspaceRow(c, t, "ZeoKeel-Next", false)
			})
		}
		ui.Text(c, "").Grow(1)
		a.moreFooter(c, t)
	})
}

func (a *App) navRow(c *ui.Context, t *design.Tokens, ic *ui.SVG, label string, active bool, target Screen) {
	row := ui.Row(c).Height(36).Width(252).PaddingX(design.Space2).Gap(design.Space2).
		Radius(design.RadiusMD).AlignItems(ui.Center).
		Label(label).Role(ui.RoleButton).Focusable()
	if active || row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		if ic != nil {
			ui.Icon(c, ic).FontSize(16).TextColor(t.LabelSecondary)
		}
		ui.Text(c, label).FontSize(14).FixedLineHeight(22).TextColor(t.LabelPrimary)
	})
	row.OnClick(func() { a.Screen = target })
}

func (a *App) sessionRow(c *ui.Context, t *design.Tokens, label, when string, selected bool) {
	// A flat row, not a Surface-filled Button: the baseline paints every
	// unselected sidebar row on the sidebar color and only tints the selected
	// one. The Button widget would keep its 1px border and white surface.
	row := ui.Row(c).Height(32).Width(256).PaddingX(design.Space2).Gap(design.Space1).
		AlignItems(ui.Center).Label(label).Role(ui.RoleButton).Focusable()
	if selected {
		row = row.Background(t.BgHover).Radius(design.RadiusSM)
	}
	row.Children(func() {
		// DSH's 16px leading status slot puts the title at x=40 and the
		// timestamp at 10/16. The selected New Session draft row has the
		// slot but no dot; an existing session shows its idle dot.
		ui.Row(c).Size(16, 16).Center().Children(func() {
			if !selected {
				ui.Box(c).Size(6, 6).Radius(design.RadiusMax).Background(t.BorderL3)
			}
		})
		ui.Text(c, label).FontSize(14).FixedLineHeight(20).TextColor(t.LabelPrimary).Grow(1)
		ui.Text(c, when).FontSize(10).FixedLineHeight(16).TextColor(t.LabelTertiary)
	})
	row.OnClick(func() {
		a.Screen = Session
		if a.Ctrl != nil {
			if sessions, err := a.Ctrl.ListSessions(context.Background()); err == nil && len(sessions) > 0 {
				if _, err := a.Ctrl.OpenSession(context.Background(), sessions[0].ID); err == nil {
					a.Screen = Session
				}
			}
		}
	})
}

func (a *App) workspaceRow(c *ui.Context, t *design.Tokens, label string, active bool) {
	// The selected workspace row stays on the sidebar color in DSH and only
	// its folder glyph turns accent; the selected session row carries the fill.
	row := ui.Row(c).Height(34).PaddingX(design.Space2).AlignItems(ui.Center).Gap(design.Space2)
	row.Children(func() {
		folder := t.LabelSecondary
		if active {
			folder = t.Accent
		}
		ui.Icon(c, icons.Folder()).FontSize(15).TextColor(folder)
		ui.Text(c, label).FontSize(14).TextColor(t.LabelPrimary)
	})
}

func (a *App) nav(c *ui.Context, t *design.Tokens, label string, active bool) {
	design.Apply(ui.Text(c, label), t.NavRow(active), t.Text(design.TypeBody)).
		PaddingX(design.Space3).PaddingY(design.Space2)
}

func (a *App) composer(c *ui.Context, t *design.Tokens, permission, model string) {
	design.Apply(ui.Column(c), t.Composer()).Width(868).Padding(design.Space4).Gap(design.Space2).Children(func() {
		ui.Text(c, a.T("home.composer_placeholder")).
			FontSize(14).TextColor(t.LabelTertiary)
		ui.Row(c).Gap(design.Space2).Children(func() {
			ui.Text(c, "+").FontSize(14).TextColor(t.LabelSecondary)
			ui.Text(c, permission).FontSize(13).TextColor(t.LabelSecondary)
			ui.Text(c, model).FontSize(13).TextColor(t.LabelSecondary)
		})
	})
}
