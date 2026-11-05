// Package app owns ZeoKeel's process lifecycle: a single instance, the
// window, close-to-hide with a tray, and a clean quit.
package app

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/agentteam"
	"github.com/zeokeel/zeokeel/internal/automation"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/shell"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// ErrAlreadyRunning is returned when a second ZeoKeel is launched.
var ErrAlreadyRunning = errors.New("another ZeoKeel instance is running")

// DataDir is the app's data directory (~/.zeokeel), or ZEOKEEL_DATA_DIR when
// set (tests, smoke runs, portable installs).
func DataDir() string {
	if dir := os.Getenv("ZEOKEEL_DATA_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".zeokeel")
	}
	return filepath.Join(home, ".zeokeel")
}

type app struct {
	quitting bool
	win      *mygo.Window
	tray     *mygo.Tray
	// instanceLock is the data-directory single-instance lock (nil before
	// acquire and after release). See singleinstance.go.
	instanceLock *instanceLock
	// activationMu guards activateWindow/activationPending: a second launch
	// asks this instance to show its window through the loopback activation
	// listener, which runs on its own goroutine.
	activationMu      sync.Mutex
	activateWindow    func()
	activationPending bool

	// shellApp is the UI the window renders. The shell owns the UI behaviour
	// and the keyboard shortcuts; this package owns the process lifecycle.
	shellApp *shell.App
	// settingsWindow is the independent, non-modal settings window. It is
	// created on the first open and reused afterwards, so reopening it keeps
	// its tab and scroll position. See settings_window.go.
	settingsWindow *settingsWindowController
	// settingsWindows creates the settings window. It is a seam: tests inject a
	// fake so the window lifecycle is tested without a desktop backend; nil
	// means the production mygo manager.
	settingsWindows settingsWindowManager
	// quitApp ends the process. It is a seam: tests replace it so the tray's
	// Quit item never touches the desktop backend.
	quitApp func()

	// Session services are built lazily on ready. They are optional: if the
	// store cannot be opened, the app keeps running with the static shell.
	services  *session.Controller
	cleanup   func() error
	scheduler *automation.Scheduler

	// notifier is the desktop notification dispatcher, set once services are
	// ready. observeTurnFinished fans terminal turns out to it and to the
	// automation run history (PD-06).
	notifier *notificationDispatcher
	// automationMu guards automationRuns (session id -> automation run id for
	// fired automations whose turn has not finished yet) and automationTerminals
	// (terminal outcomes that arrived before the run row existed).
	automationMu        sync.Mutex
	automationRuns      map[string]string
	automationTerminals map[string]automationTerminalState

	// buildMu serializes agent rebuilds; bundle is the current one, closed
	// when a rebuild replaces it.
	buildMu sync.Mutex
	bundle  *AgentBundle

	// team is the async team manager of the current bundle (nil when Agent
	// Teams is off). The shell reads it for the live roster and stops the
	// current session's members after the user stops a turn; the bundle Close
	// closes it when a rebuild replaces the agent.
	teamMu sync.Mutex
	team   *agentteam.Manager

	// Session-lifetime tool state. A rebuild must not forget the model's
	// working directory, terminals or background jobs, so the app owns them
	// and hands the same instances to every registry build for a root.
	dirMu       sync.Mutex
	dirStates   map[string]*tools.WorkingDirectoryState
	termMgr     *tools.TerminalManager
	jobsMgr     *tools.JobsManager
	managerRoot string
	managerCap  int
	// sessionCWDs remembers each session's workspace-relative working
	// directory. The runtime agent and its WorkingDirectoryState are shared
	// across sessions in one workspace, so the app restores a session's own
	// cwd when it becomes current instead of letting one session's "cd" leak
	// into the next session. Guarded by dirMu.
	sessionCWDs map[string]string
}

// Run starts the app and blocks until it quits.
func Run() error {
	// Publish the build metadata before anything renders: the About and
	// Diagnostics pages read internal/shell's copy of it.
	publishBuildInfo()
	a := &app{}
	if err := a.acquire(); err != nil {
		return err
	}
	defer a.releaseInstance()
	return a.run()
}

func (a *app) run() error {
	mygo.App.SetName("ZeoKeel")
	// The native window menu bar (File/Edit/View/Window) is retired: the user
	// does not want it (ADR 0006). Windows and Linux windows show no menu bar
	// unless one is installed, and nothing installs one anymore; macOS would
	// keep mygo's default App/File/Edit/View/Window bar unless the application
	// menu is removed, so remove it explicitly on every platform.
	mygo.App.SetMenu(nil)
	// Mark the app as quitting before windows close, so the close-to-hide
	// handlers cannot re-show the main window during shutdown. The tray's Quit
	// item, the in-app More menu's Quit and the OS's own quit paths all end up
	// here, so mark the flag for every one of them.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	// Keep the main window's bounds and maximized state in the app's own data
	// directory instead of mygo's per-app user-data file, so a portable install
	// (ZEOKEEL_DATA_DIR) carries its window state with it. Call this before the
	// first window with a StateKey is created, as mygo requires.
	windowStates := newWindowStateStore(DataDir())
	windowStates.displays = currentDisplays
	mygo.App.SetWindowStateStore(windowStates)
	mygo.App.WhenReady(func() {
		// Services are created lazily so a broken data directory cannot
		// prevent the window and tray from starting; the shell then runs in
		// its static, history-free mode.
		a.startServices()
		// Non-UI stops (API, automation, programmatic cancellation) also stop
		// the session's asynchronous team members; the shell's own Stop path
		// calls the same app hook.
		if a.services != nil {
			a.services.SetTeamStopper(a.stopTeamSession)
		}
		shellApp := &shell.App{Ctrl: a.services}
		// The Team panel is a live roster of the session's asynchronous team
		// members, and stopping a turn also stops the members it spawned.
		shellApp.SetTeamRoster(a.teamRoster)
		shellApp.SetTeamStop(a.stopTeamSession)
		a.shellApp = shellApp
		// Production save-dialog seam for "Download session log". Tests inject
		// their own path chooser; without a desktop app the shell falls back to
		// the deterministic workspace path.
		shellApp.SaveSessionLog = func(suggested string) (string, error) {
			return mygo.Dialog.Save(mygo.SaveDialogOptions{
				Title:       "Export session log",
				DefaultPath: suggested,
				Filters: []mygo.FileFilter{{
					Name:       "JSONL session log",
					Extensions: []string{"jsonl"},
				}},
			})
		}
		// Settings is its own non-modal window: Cmd+,, the in-app More menu and
		// the other in-app entry points all call shell.App.openSettings, which
		// asks this host to show the window and falls back to the in-app modal
		// when the desktop cannot create one. The window is created on the
		// first open.
		a.initSettingsWindow(shellApp.SettingsView, shellApp.SettingsWindowTitle)
		shellApp.SetSettingsWindowOpener(a.showSettingsWindow)
		shellApp.SetSettingsWindowCloser(a.hideSettingsWindow)
		shellApp.SetSettingsChangedHook(a.refreshWindows)
		// Preferred 1600x1000, clamped to the primary display's work area so a
		// small laptop screen does not open a window larger than the screen
		// (AR-18/PD-15).
		windowOptions := mainWindowOptionsFor(currentDisplays())
		windowOptions.Content = ui.View(shellApp.View)
		a.win = mygo.NewWindow(windowOptions)
		// A second launch that lost the single-instance race asks this instance
		// to show its window; wire that before the window can be activated.
		a.setActivationWindow(a.win)
		if a.services != nil {
			win := a.win
			// Desktop notifications read the same update stream as the UI: the
			// dispatcher only watches phase transitions, so a token update costs
			// one map lookup. The controller's turn and approval hooks cover
			// background sessions, which OnUpdate never publishes.
			notifier := newNotificationDispatcher(newSystemNotifier(), a.notificationActive, a.uiLanguage, a.focusSession)
			// PD-05 notification settings: the dispatcher reads them per
			// notification, so toggling them in Settings applies immediately.
			notifier.enabled = a.notificationsEnabled
			notifier.suppressFocused = a.notificationsSuppressFocused
			notifier.useTurnFinishedHook()
			notifier.useApprovalHook()
			a.notifier = notifier
			// One controller hook fans out to every turn observer: desktop
			// notifications and the automation run history.
			a.services.SetOnTurnFinished(a.observeTurnFinished)
			a.services.SetOnApprovalRequested(notifier.observeApprovalRequested)
			a.services.OnUpdate(func(snap *state.Snapshot) {
				shellApp.SetSnapshot(snap)
				win.Invalidate()
				notifier.Observe(snap)
			})
		}
		// What the user has just seen in the app should not wait in the system's
		// notification center: clear the app's delivered notifications when the
		// app comes to the front. Unsupported platforms skip it.
		mygo.App.OnDidBecomeActive(func() {
			if mygo.NotificationsSupported() {
				mygo.ClearNotifications()
			}
		})
		// Closing the window hides it by default; the tray keeps the app alive.
		// ui.close_behavior=quit makes the close button exit instead (PD-05).
		// The close must be prevented, not just observed: OnClosed runs after
		// the window is destroyed, when Hide and the tray's Show would be
		// no-ops. During quit a.quitting is already set, so the windows close
		// for real.
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if a.quitting {
				return
			}
			e.PreventDefault()
			if a.closeBehaviorQuits() {
				a.quitApplication()
				return
			}
			a.win.Hide()
		})
		mygo.App.OnWindowAllClosed(func() {
			if a.quitting || a.win == nil {
				return
			}
			a.win.Show()
			a.win.Focus()
		})
		a.installTray()
		// PD-12: make the OS autostart registration agree with app.autostart.
		// Best effort: a failure is logged and never blocks startup.
		a.syncAutostartFromSetting()
	})
	err := mygo.App.Run()
	a.stopServices()
	return err
}

// startServices opens the session stack under DataDir. The current build has no
// provider wired yet, so the Controller is constructed with a nil Agent: the
// shell can read history while Submit reports "no agent configured" until the
// runtime is attached.
func (a *app) startServices() {
	dir := DataDir()
	var built *AgentBundle
	ctrl, cleanup, err := BuildServicesWith(dir, func(st *store.Store) (*AgentBundle, error) {
		root := workspaceRootForStore(st, dir)
		bundle, berr := buildAgentWithOptions(st, root, DefaultInstruction, a.toolOptions(st, root, ""))
		if bundle != nil {
			built = bundle
		}
		return bundle, berr
	})
	if err != nil {
		log.Printf("zeokeel: session services unavailable, using static UI: %v", err)
		a.adoptTeamManager(nil)
		return
	}
	a.services = ctrl
	a.cleanup = cleanup
	a.bundle = built
	// /goal changes and goal-driver state persist through the store-backed tool
	// state, so a restart shows the same objective (FP-04).
	ctrl.SetGoalStore(storeToolState{st: ctrl.Store()})
	if built != nil {
		ctrl.SetAgentModeSwitcher(built.Agent.SetMode)
	}
	// The factory builds before the Controller exists, so it can only guess
	// the workspace from the store. Rebuild now that the Controller can
	// resolve the selected workspace and future changes have a hook.
	ctrl.SetAgentRebuilder(a.rebuildAgent)
	// The shared working-directory state is reset/restored whenever the
	// current session changes, so one session's cd cannot leak into another.
	ctrl.SetOnSessionActivated(a.sessionActivated)
	if err := a.rebuildAgent("startup"); err != nil {
		log.Printf("zeokeel: agent rebuild for the selected workspace: %v", err)
	}
	a.startAutomation(ctrl)
}

// workspaceRootForStore resolves the selected workspace's root from the store
// before a Controller exists. An empty root (the built-in default workspace)
// falls back to dir, the app data directory.
func workspaceRootForStore(st *store.Store, dir string) string {
	if st != nil {
		if id, err := st.GetSetting(session.SettingWorkspace); err == nil && strings.TrimSpace(id) != "" {
			if ws, werr := st.GetWorkspace(strings.TrimSpace(id)); werr == nil && ws != nil {
				// The selected workspace is authoritative; an empty root
				// (the built-in default workspace) means the data dir.
				if root := strings.TrimSpace(ws.Root); root != "" {
					return root
				}
				return dir
			}
		}
		if list, lerr := st.ListWorkspaces(); lerr == nil {
			for _, ws := range list {
				if strings.TrimSpace(ws.Root) != "" {
					return strings.TrimSpace(ws.Root)
				}
			}
		}
	}
	return dir
}

// workingDirState returns the app-owned working-directory state for root,
// creating it the first time. Keeping it here rather than inside each registry
// build means a plugin/model/permission rebuild no longer resets the cwd the
// working_directory tool last set.
func (a *app) workingDirState(root string) *tools.WorkingDirectoryState {
	a.dirMu.Lock()
	defer a.dirMu.Unlock()
	key := filepath.Clean(root)
	if state := a.dirStates[key]; state != nil {
		return state
	}
	state, err := tools.NewWorkingDirectoryState(root)
	if err != nil {
		return nil
	}
	if a.dirStates == nil {
		a.dirStates = make(map[string]*tools.WorkingDirectoryState)
	}
	a.dirStates[key] = state
	return state
}

// adoptTeamManager records the async team manager of the current bundle. The
// previous manager is closed by the replaced bundle's Close.
func (a *app) adoptTeamManager(m *agentteam.Manager) {
	a.teamMu.Lock()
	a.team = m
	a.teamMu.Unlock()
}

// teamManager returns the current bundle's async team manager, or nil.
func (a *app) teamManager() *agentteam.Manager {
	a.teamMu.Lock()
	defer a.teamMu.Unlock()
	return a.team
}

// teamRoster maps the current session's asynchronous team members to the
// shell's teammate rows, so the Team panel is a live roster instead of only
// the completed synchronous subagent calls folded into the session.
func (a *app) teamRoster() []tools.Teammate {
	mgr := a.teamManager()
	if mgr == nil || a.services == nil {
		return nil
	}
	return teammatesForSession(mgr.Members(), a.services.CurrentSessionID())
}

// teammatesForSession filters the manager's members to sessionID and adapts
// them to the shell's display type.
func teammatesForSession(members []agentteam.Member, sessionID string) []tools.Teammate {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(members) == 0 {
		return nil
	}
	out := make([]tools.Teammate, 0, len(members))
	for _, member := range members {
		if member.SessionID != sessionID {
			continue
		}
		out = append(out, tools.Teammate{
			CallID:    "team:" + member.ID,
			Name:      member.Name,
			Task:      member.Task,
			Status:    string(member.Status),
			Output:    member.Output,
			UpdatedAt: member.UpdatedAt,
		})
	}
	return out
}

// stopTeamSession cancels and waits for the asynchronous team members a
// session spawned. The shell calls it after Controller.Stop, off the UI
// goroutine because it waits for the members to unwind.
func (a *app) stopTeamSession(sessionID string) {
	if mgr := a.teamManager(); mgr != nil {
		mgr.StopSession(sessionID)
	}
}

// toolOptions assembles the reusable state for one registry build. Managers
// are reused only for the same root and output cap; changing either closes
// them and starts fresh, so settings still take effect on the next build.
func (a *app) toolOptions(st *store.Store, root, permission string) registryOptions {
	opts := registryOptions{
		PermissionOverride: permission,
		DirState:           a.workingDirState(root),
	}
	if a.services != nil {
		opts.WorkspaceID = a.services.CurrentWorkspaceID()
	}
	// The plan flag belongs to the session that is running, not to the build.
	// The closure resolves it per turn so toggling plan mode needs no rebuild;
	// the initial startup build runs before services exist and reads off.
	opts.PlanModeFunc = func() bool {
		if a.services == nil {
			return false
		}
		return a.services.PlanMode(a.services.CurrentSessionID())
	}
	// The exit_plan_mode tool gets the same flag by session id instead of the
	// current selection, so a turn in a background session checks its own mode.
	opts.PlanModeForSession = func(sessionID string) bool {
		if a.services == nil {
			return false
		}
		return a.services.PlanMode(sessionID)
	}
	capBytes := outputCapBytes(st)
	if a.termMgr != nil && a.managerRoot == filepath.Clean(root) && a.managerCap == capBytes {
		opts.Terminal = a.termMgr
		opts.Jobs = a.jobsMgr
	}
	opts.AdoptTeam = a.adoptTeamManager
	opts.AdoptManagers = func(state *tools.WorkingDirectoryState, term *tools.TerminalManager, jobs *tools.JobsManager) {
		if state != nil {
			a.dirMu.Lock()
			if a.dirStates == nil {
				a.dirStates = make(map[string]*tools.WorkingDirectoryState)
			}
			a.dirStates[filepath.Clean(root)] = state
			a.dirMu.Unlock()
		}
		// A read-only (or Shell-off) build passes nil managers. It must not
		// record the root/cap it would have used: doing so made the next
		// writable build at that root believe the still-current managers
		// belonged there and reuse them across a workspace switch. Record the
		// key only when a manager pair is actually adopted.
		if term != nil || jobs != nil {
			a.managerRoot = filepath.Clean(root)
			a.managerCap = capBytes
		}
		// A new workspace root or output cap creates new managers: close the
		// old processes instead of orphaning them. A read-only build passes
		// nil and keeps the current managers for the next writable build.
		if term != nil {
			if a.termMgr != nil && a.termMgr != term {
				if err := a.termMgr.Close(); err != nil {
					log.Printf("zeokeel: closing replaced terminals: %v", err)
				}
			}
			a.termMgr = term
		}
		if jobs != nil {
			if a.jobsMgr != nil && a.jobsMgr != jobs {
				if err := a.jobsMgr.Close(); err != nil {
					log.Printf("zeokeel: closing replaced background jobs: %v", err)
				}
			}
			a.jobsMgr = jobs
		}
	}
	return opts
}

// closeToolManagers releases the terminals and jobs the app owns.
func (a *app) closeToolManagers() {
	if a.termMgr != nil {
		if err := a.termMgr.Close(); err != nil {
			log.Printf("zeokeel: closing terminals: %v", err)
		}
		a.termMgr = nil
	}
	if a.jobsMgr != nil {
		if err := a.jobsMgr.Close(); err != nil {
			log.Printf("zeokeel: closing background jobs: %v", err)
		}
		a.jobsMgr = nil
	}
	a.managerRoot = ""
	a.managerCap = 0
}

// rebuildAgent rebuilds the tool registry and runtime agent against the
// selected workspace root and the current settings. It is the app's
// implementation of Controller.SetAgentRebuilder, so a plugin toggle, a new
// workspace, an access-mode change or a newly selected model applies to the
// next turn without a restart.
func (a *app) rebuildAgent(reason string) error {
	a.buildMu.Lock()
	defer a.buildMu.Unlock()
	if a.services == nil {
		return nil
	}
	dir := DataDir()
	root := strings.TrimSpace(a.services.WorkspaceRoot())
	if root == "" {
		root = dir
	}
	permission := a.services.SessionPermissionOrDefault(a.services.CurrentSessionID())
	opts := a.toolOptions(a.services.Store(), root, permission)
	oldTeam := a.teamManager()
	// Build the agent with the current session's preset so its very first
	// topology and prompt already match; ModeFunc keeps the prompt gate in sync
	// when SetMode switches the agent later.
	opts.Mode = a.services.Mode()
	opts.ModeFunc = a.services.Mode
	bundle, err := buildAgentWithOptions(a.services.Store(), root, DefaultInstruction, opts)
	if err != nil {
		// A failed build may have created and already closed a manager; restore
		// the roster of the still-live old bundle.
		a.adoptTeamManager(oldTeam)
		return err
	}
	if bundle == nil {
		// No provider key (or the last model was removed): clear the agent so
		// Submit reports ErrNoAgent instead of running with stale credentials.
		// Close the replaced bundle (MCP sessions and the team manager) so its
		// teammates cannot keep running behind a dead agent.
		old := a.bundle
		a.bundle = nil
		a.adoptTeamManager(nil)
		a.services.SetAgent(nil, nil)
		a.services.SetTitleGenerator(nil)
		a.services.SetBuiltPermission(permission)
		if old != nil && old.Close != nil {
			if cerr := old.Close(); cerr != nil {
				log.Printf("zeokeel: closing replaced agent bundle (%s): %v", reason, cerr)
			}
		}
		return nil
	}
	old := a.bundle
	a.bundle = bundle
	a.services.SetAgent(bundle.Agent, bundle.Gate)
	a.services.SetTitleGenerator(bundle.TitleGenerator)
	a.services.SetBuiltPermission(permission)
	a.services.SetAgentModeSwitcher(bundle.Agent.SetMode)
	if mode := a.services.Mode(); strings.TrimSpace(mode) != "" {
		if err := bundle.Agent.SetMode(mode); err != nil {
			log.Printf("zeokeel: apply agent mode %q after rebuild (%s): %v", mode, reason, err)
		}
	}
	if old != nil && old.Close != nil {
		if cerr := old.Close(); cerr != nil {
			log.Printf("zeokeel: closing replaced agent bundle: %v", cerr)
		}
	}
	return nil
}

func (a *app) stopServices() {
	if a.scheduler != nil {
		a.scheduler.Stop()
		a.scheduler = nil
	}
	a.closeToolManagers()
	if a.cleanup == nil {
		return
	}
	if err := a.cleanup(); err != nil {
		log.Printf("zeokeel: closing session services: %v", err)
	}
	a.services = nil
	a.cleanup = nil
}

// installTray adds the tray icon and its menu. This is the app's only native
// menu now that the window menu bar is retired (ADR 0006); together with the
// shell's keyboard shortcuts and in-app entries it keeps Show/Quit reachable.
func (a *app) installTray() {
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Show ZeoKeel", Click: func(*mygo.MenuItem, *mygo.Window) {
			if a.win != nil {
				a.win.Show()
				a.win.Focus()
			}
		}},
		{Label: "New Session", Click: func(*mygo.MenuItem, *mygo.Window) { a.trayNewSession() }},
		{Label: "Settings", Click: func(*mygo.MenuItem, *mygo.Window) { a.traySettings() }},
		mygo.Separator(),
		{Label: "Quit", Click: func(*mygo.MenuItem, *mygo.Window) { a.quitApplication() }},
	})
	tray, err := mygo.NewTray(mygo.TrayOptions{Title: "ZeoKeel", ToolTip: "ZeoKeel", Menu: menu})
	if err != nil {
		log.Printf("zeokeel: tray unavailable: %v", err)
		return
	}
	a.tray = tray
}

// startAutomation wires the automation scheduler and Run-now hook: firing a
// task creates a session and submits its prompt through the live controller.
func (a *app) startAutomation(ctrl *session.Controller) {
	if ctrl == nil {
		return
	}
	runTask := func(task store.Automation) {
		a.runAutomationTask(ctrl, task)
	}
	ctrl.RunNow = func(id string) {
		task, err := ctrl.Automations().Get(id)
		if err != nil {
			log.Printf("zeokeel: automation run-now: %v", err)
			return
		}
		runTask(*task)
	}
	ctrl.SetPluginChangeHook(func(name string, enabled bool) {
		// SetPluginEnabled persists first and then calls Controller's wired
		// rebuild hook, which swaps the agent for the next turn. Log here
		// only for diagnostics.
		log.Printf("zeokeel: plugin %s enabled=%v; rebuilding agent for the next turn", name, enabled)
	})
	sched, err := automation.New(automation.Config{
		Store:    ctrl.Store(),
		OnFire:   runTask,
		Interval: 30 * time.Second,
	})
	if err != nil {
		log.Printf("zeokeel: automation scheduler disabled: %v", err)
		return
	}
	sched.Start(context.Background())
	a.scheduler = sched
}
