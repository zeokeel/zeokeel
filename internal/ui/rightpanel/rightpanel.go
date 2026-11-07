// Package rightpanel contributes the session inspector: the right sidebar of
// the conversation screen (contracts.SlotSessionPanelRight). It is a UI module
// (kernel Descriptor + host UI services) rendered entirely with mygo's native
// controls and the brand design system (internal/design): a native tab strip
// over three panes — overview, tools and model — inside t.Panel cards. It never
// draws a control (or a Canvas) itself.
//
// The module reads the chat read model (contracts.ChatService) and never writes
// it: the overview shows the active session's title, message count, update time
// and running/error badges; the tools pane aggregates every ToolCall of the
// session with a status badge and a collapsible summary; the model pane shows
// the effective model configuration, with the API key reduced to "set/not set"
// so a secret can never reach the screen.
//
// The host owns the panel width and the open/close control: the module only
// fills the slot. Chat changes arrive on the agent goroutine, so the module
// hops to the UI thread with UIRuntime.Post and requests a frame; the
// subscription and the slot registration are kernel-effect owned, so disposal
// unwinds both.
package rightpanel

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/rightpanel"

// SlotItemID is the id of the inspector's contribution to the right panel.
const SlotItemID = "rightpanel.inspector"

// slotOrder places the inspector first in the right panel.
const slotOrder = 10

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the panel heading (default "检查").
	Title string `json:"title"`
	// OverviewTab/ToolsTab/ModelTab are the tab labels.
	OverviewTab string `json:"overviewTab"`
	ToolsTab    string `json:"toolsTab"`
	ModelTab    string `json:"modelTab"`
	// EmptyText is the overview copy when there is no active session.
	EmptyText string `json:"emptyText"`
}

// resolved fills the configured strings with their defaults.
func (c Config) resolved() Config {
	return Config{
		Title:       firstNonEmpty(c.Title, "检查"),
		OverviewTab: firstNonEmpty(c.OverviewTab, "概览"),
		ToolsTab:    firstNonEmpty(c.ToolsTab, "工具"),
		ModelTab:    firstNonEmpty(c.ModelTab, "模型"),
		EmptyText:   firstNonEmpty(c.EmptyText, "还没有活动会话"),
	}
}

// state is the module's UI state (one instance per loaded module, only touched
// on the UI thread): the selected pane and the per-tool summary fold state.
type state struct {
	tab  int
	open map[string]bool
}

// Module registers the session inspector slot.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return err
		}
		config := cfg.resolved()
		moduleState := &state{open: make(map[string]bool)}

		unsubscribe, err := chat.Subscribe(func(string) {
			runtime.Post(func() { runtime.Invalidate() })
		})
		if err != nil {
			return fmt.Errorf("rightpanel: subscribe chat: %w", err)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(unsubscribe)}
		}); err != nil {
			unsubscribe()
			return err
		}

		return own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotSessionPanelRight,
				ID:    SlotItemID,
				Order: slotOrder,
				Build: func(c *ui.Context) {
					buildPanel(c, design.Of(c), chat, config, moduleState)
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel session inspector")
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime, so activation failure and module disposal leave no residue.
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

// firstNonEmpty returns the first non-empty trimmed string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
