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

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
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

	formOpen     bool
	formName     string
	formPrompt   string
	formSchedule string
	formEnabled  bool
	formErr      string

	deleteOpen   bool
	deleteTarget string
	deleteName   string

	pageErr string
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
		moduleState := &state{ctx: ctx, formEnabled: true}

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

		return own(ctx, func() (func(), error) {
			return runtime.RegisterScreen(contracts.ScreenSpec{
				ID:    ScreenID,
				Title: config.Title,
				Order: 20,
				Build: func(c *ui.Context) {
					buildScreen(c, runtime, navigator, config, moduleState)
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		WithName("ZeoKeel automation screen")
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
