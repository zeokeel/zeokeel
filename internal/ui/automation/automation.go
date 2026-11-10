// Package automation contributes the Automation screen: the old shell's
// scheduled-task surface, rebuilt with mygo's native controls and the brand
// design system.
//
// It is a UI module like ui/session: a kernel Descriptor that requires the host
// UI runtime and navigator, registers the "automation" screen and reads the
// optional contracts.AutomationService (no Requires) for the task list, inline
// creation form, enable/disable toggles, Run now and delete actions. The
// service notifies from its scheduler goroutine; the module hops to the UI
// thread with UIRuntime.Post and requests a frame. The subscription and the
// screen registration are kernel-effect owned, so disposal unwinds both.
package automation

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/automation"

// ScreenID is the screen the module contributes.
const ScreenID = "automation"

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the screen title and page heading (default "自动化").
	Title string `json:"title"`
	// NewLabel is the header primary action label (default "新建任务").
	NewLabel string `json:"newLabel"`
	// EmptyText is the task-list empty state (default "还没有自动化任务").
	EmptyText string `json:"emptyText"`
}

// resolved fills the configured strings with their defaults.
func (c Config) resolved() Config {
	return Config{
		Title:     firstNonEmpty(c.Title, "自动化"),
		NewLabel:  firstNonEmpty(c.NewLabel, "新建任务"),
		EmptyText: firstNonEmpty(c.EmptyText, "还没有自动化任务"),
	}
}

// state is the module's UI state: the inline create form, the delete
// confirmation and the last service error. It is only touched on the UI thread.
type state struct {
	ctx cordis.Context

	// locale localizes the screen copy per frame (zh is the default).
	locale contracts.LocaleService

	// runtime requests frames and notifier raises the delete toast (with Undo
	// when the host implements the optional action half).
	runtime  contracts.UIRuntime
	notifier contracts.Notifier

	// taskList backs the official List of scheduled tasks on the page.
	taskList ui.ListState

	formOpen bool
	// formFocusPending asks the next frame to put the keyboard in the name
	// field, so opening the form from the band's New action lands the caret
	// where the user types (and Esc, a screen-scope shortcut, keeps working
	// while the form has the focus).
	formFocusPending  bool
	formNameInput     ui.Handle
	formName          string
	formPrompt        string
	formModel         string
	formSchedule      string
	formWorkspaceID   string
	formWorkspaceName string
	formPermission    string
	formEnabled       bool
	formErr           string
	// formModelCustom keeps the model field in free-text mode (a model outside
	// the catalog list); the menu otherwise picks a catalog preset.
	formModelCustom bool
	// scheduleErr/schedulePreview are recomputed from formSchedule each frame:
	// the draft is validated with the same parser the backend executes and a
	// valid draft previews its next run.
	scheduleErr     string
	schedulePreview string
	// formEditID non-empty means the inline form is editing that task instead
	// of creating one; formEditTask snapshots the row so the update keeps its
	// id and run bookkeeping.
	formEditID   string
	formEditTask contracts.AutomationTask

	deleteOpen   bool
	deleteTarget string
	deleteName   string
	// deleteTask snapshots the task the delete dialog is confirming, so the
	// undo action can re-create it.
	deleteTask contracts.AutomationTask

	pageErr string

	// historyOpen remembers the expanded run-history sections per task id.
	historyOpen map[string]bool

	// Search/filter state of the task list. It is session-local (not persisted):
	// reopening the screen restores the unfiltered list.
	query      string
	taskFilter string

	// workspaceNames caches the workspace id -> display name lookup used by the
	// task cards (loaded once per screen visit).
	workspaceNames       map[string]string
	workspaceNamesLoaded bool

	// running maps a task id to when "Run now" was pressed: the run happens in
	// the backend's goroutine, so the card shows an in-flight state until the
	// backend records the run (or the guard times out).
	running map[string]time.Time

	// navigator opens the session a run created (the history row's session
	// link), and is the host navigator the screen was registered with.
	navigator contracts.Navigator
}

// Module registers the Automation screen.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		navigator, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		config := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		moduleState := &state{
			ctx: ctx, locale: locale, runtime: runtime, navigator: navigator,
			formEnabled: true,
			historyOpen: make(map[string]bool),
			running:     make(map[string]time.Time),
		}
		if notifier, ok := ctx.Get(contracts.KeyNotifier); ok {
			moduleState.notifier = notifier
		}

		// The automation backend is optional: without it the screen still
		// renders, with an explanatory empty state. When present, its change
		// notifications request a frame; the unsubscribe is effect owned.
		if service, ok := ctx.Get(contracts.KeyAutomation); ok {
			unsubscribe, err := service.Subscribe(func() {
				runtime.Post(func() { runtime.Invalidate() })
			})
			if err != nil {
				return fmt.Errorf("automation: subscribe: %w", err)
			}
			if _, err := ctx.Effect(func() cordis.Effect {
				return cordis.Effect{cordis.CleanupOf(unsubscribe)}
			}); err != nil {
				unsubscribe()
				return err
			}
		}

		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterScreen(contracts.ScreenSpec{
				ID:    ScreenID,
				Title: config.Title,
				Order: 20,
				Build: func(c *ui.Context) {
					buildScreen(c, runtime, navigator, config, moduleState)
				},
			})
		}); err != nil {
			return err
		}
		// The screen's chrome lives in the host's B3 top band: the title
		// (SlotScreenHeader) and the single primary New action
		// (SlotScreenActions), beside the host's palette and inspector controls.
		// The item IDs carry the screen namespace so the band stays on this
		// screen only.
		screenSlots := []contracts.SlotItem{
			{
				Slot: contracts.SlotScreenHeader, ID: "automation.title", Order: 10,
				Build: func(c *ui.Context) { buildScreenHeaderSlot(c, design.Of(c), config, moduleState) },
			},
			{
				Slot: contracts.SlotScreenActions, ID: "automation.actions", Order: 10,
				Build: func(c *ui.Context) { buildScreenActionsSlot(c, design.Of(c), runtime, config, moduleState) },
			},
		}
		for _, item := range screenSlots {
			item := item
			if err := own(ctx, func() (func(), error) {
				return runtime.RegisterSlotItem(item)
			}); err != nil {
				return err
			}
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		WithName("ZeoKeel automation screen")
}

// tr returns the localized copy for the current frame's language.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

// english reports whether the current frame renders English copy.
func (st *state) english() bool { return uikit.Language(st.locale) == "en" }

// localized translates a value that still carries its built-in Chinese default
// (an assembly-configured custom label is kept verbatim).
func (st *state) localized(value, zhDefault, enDefault string) string {
	if strings.TrimSpace(value) == "" || value == zhDefault {
		return st.tr(zhDefault, enDefault)
	}
	return value
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
