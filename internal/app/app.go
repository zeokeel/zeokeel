// Package app owns ZeoKeel's process lifecycle: a single instance, the
// window, close-to-hide with a tray, and a clean quit.
package app

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

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
	lock     net.Listener

	// shellApp is the UI the window renders. The native menu bar dispatches its
	// commands through the shell's SetMenuAction hook, so the shell owns the UI
	// behaviour and this package only owns process-level commands (Quit).
	shellApp *shell.App
	// quitApp ends the process. It is a seam: tests replace it so a menu click
	// never touches the desktop backend.
	quitApp func()

	// Session services are built lazily on ready. They are optional: if the
	// store cannot be opened, the app keeps running with the static shell.
	services  *session.Controller
	cleanup   func() error
	scheduler *automation.Scheduler

	// buildMu serializes agent rebuilds; bundle is the current one, closed
	// when a rebuild replaces it.
	buildMu sync.Mutex
	bundle  *AgentBundle

	// Session-lifetime tool state. A rebuild must not forget the model's
	// working directory, terminals or background jobs, so the app owns them
	// and hands the same instances to every registry build for a root.
	dirMu       sync.Mutex
	dirStates   map[string]*tools.WorkingDirectoryState
	termMgr     *tools.TerminalManager
	jobsMgr     *tools.JobsManager
	managerRoot string
	managerCap  int
}

// Run starts the app and blocks until it quits.
func Run() error {
	a := &app{}
	if err := a.acquire(); err != nil {
		return err
	}
	return a.run()
}

// acquire is the single-instance guard: a loopback listener is held for the
// process lifetime and released by the OS if the process dies.
func (a *app) acquire() error {
	if err := os.MkdirAll(DataDir(), 0o700); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:49731")
	if err != nil {
		return ErrAlreadyRunning
	}
	a.lock = ln
	return nil
}

func (a *app) run() error {
	mygo.App.SetName("ZeoKeel")
	// Mark the app as quitting before windows close, so the close-to-hide
	// handlers cannot re-show the main window during shutdown. The menu's Quit
	// command sets the flag too; this listener covers the other quit paths
	// (the OS, macOS's own app-menu roles).
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	// Keep the main window's bounds and maximized state in the app's own data
	// directory instead of mygo's per-app user-data file, so a portable install
	// (ZEOKEEL_DATA_DIR) carries its window state with it. Call this before the
	// first window with a StateKey is created, as mygo requires.
	mygo.App.SetWindowStateStore(newWindowStateStore(DataDir()))
	mygo.App.WhenReady(func() {
		// Services are created lazily so a broken data directory cannot
		// prevent the window and tray from starting; the shell then runs in
		// its static, history-free mode.
		a.startServices()
		shellApp := &shell.App{Ctrl: a.services}
		a.shellApp = shellApp
		// Native-menu commands are routed through the shell so it owns the UI
		// behaviour; this package only implements the process-level commands.
		shellApp.SetMenuAction(a.handleMenuAction)
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
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "ZeoKeel",
			Width:     1600,
			Height:    1000,
			MinWidth:  900,
			MinHeight: 600,
			StateKey:  windowStateKey,
			Content:   ui.View(shellApp.View),
		})
		if a.services != nil {
			win := a.win
			a.services.OnUpdate(func(snap *state.Snapshot) {
				shellApp.SetSnapshot(snap)
				win.Invalidate()
			})
		}
		// Closing the window hides it; the tray keeps the app alive. The close
		// must be prevented, not just observed: OnClosed runs after the window
		// is destroyed, when Hide and the tray's Show would be no-ops. During
		// quit a.quitting is already set, so the windows close for real.
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if a.quitting {
				return
			}
			e.PreventDefault()
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
		a.installMenu()
	})
	err := mygo.App.Run()
	a.stopServices()
	if a.lock != nil {
		_ = a.lock.Close()
	}
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
		return
	}
	a.services = ctrl
	a.cleanup = cleanup
	a.bundle = built
	if built != nil {
		ctrl.SetAgentModeSwitcher(built.Agent.SetMode)
	}
	// The factory builds before the Controller exists, so it can only guess
	// the workspace from the store. Rebuild now that the Controller can
	// resolve the selected workspace and future changes have a hook.
	ctrl.SetAgentRebuilder(a.rebuildAgent)
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
	capBytes := outputCapBytes(st)
	if a.termMgr != nil && a.managerRoot == filepath.Clean(root) && a.managerCap == capBytes {
		opts.Terminal = a.termMgr
		opts.Jobs = a.jobsMgr
	}
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
	// Build the agent with the current session's preset so its very first
	// topology and prompt already match; ModeFunc keeps the prompt gate in sync
	// when SetMode switches the agent later.
	opts.Mode = a.services.Mode()
	opts.ModeFunc = a.services.Mode
	bundle, err := buildAgentWithOptions(a.services.Store(), root, DefaultInstruction, opts)
	if err != nil {
		return err
	}
	if bundle == nil {
		// No provider key (or the last model was removed): clear the agent so
		// Submit reports ErrNoAgent instead of running with stale credentials.
		a.bundle = nil
		a.services.SetAgent(nil, nil)
		a.services.SetTitleGenerator(nil)
		a.services.SetBuiltPermission(permission)
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

func (a *app) installTray() {
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Show ZeoKeel", Click: func(*mygo.MenuItem, *mygo.Window) {
			if a.win != nil {
				a.win.Show()
				a.win.Focus()
			}
		}},
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
		ctx := context.Background()
		id, err := ctrl.CreateSession(ctx, task.Title)
		if err != nil {
			log.Printf("zeokeel: automation %q: create session: %v", task.Title, err)
			return
		}
		if err := ctrl.Submit(ctx, id, task.Prompt); err != nil {
			log.Printf("zeokeel: automation %q: submit: %v", task.Title, err)
		}
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
