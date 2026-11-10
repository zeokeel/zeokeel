// Package plugins contributes the Plugins screen: the MCP plugin connections
// and the product's tool catalogue (the old shell's Plugins/manage surface),
// rebuilt with mygo's native controls and the brand design system.
//
// It is a UI module like ui/automation: a kernel Descriptor that requires the
// host UI runtime and navigator, registers the "plugins" screen and reads the
// optional contracts.PluginsService and contracts.KeyToolkit registry (no
// Requires). Missing backends degrade to per-section empty states. Every
// action (the MCP Refresh) runs through the service and invalidates the frame.
package plugins

import (
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/plugins"

// ScreenID is the screen the module contributes.
const ScreenID = "plugins"

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the screen title and page heading (default "插件").
	Title string `json:"title"`
	// Description is the page subtitle.
	Description string `json:"description"`
	// MCPSection is the MCP servers section label (default "MCP 插件").
	MCPSection string `json:"mcpSection"`
	// ToolsSection is the tool catalogue section label (default "工具目录").
	ToolsSection string `json:"toolsSection"`
}

func (c Config) resolved() Config {
	return Config{
		Title:        firstNonEmpty(c.Title, "插件"),
		Description:  firstNonEmpty(c.Description, "查看已连接的 MCP 服务器与产品内置的工具目录。"),
		MCPSection:   firstNonEmpty(c.MCPSection, "MCP 插件"),
		ToolsSection: firstNonEmpty(c.ToolsSection, "工具目录"),
	}
}

// state is the module's UI state: the tool search query. It is only touched on
// the UI thread.
type state struct {
	ctx   cordis.Context
	query string
	// locale localizes the screen copy per frame (zh is the default).
	locale contracts.LocaleService

	// Tool switches. disabledTools is tools.disabled (all tools);
	// disabledMCPTools is the legacy mcp.disabledTools. Both load on the first
	// frame; disabledErr surfaces malformed stored JSON.
	disabledTools    map[string]bool
	disabledMCPTools map[string]bool
	disabledLoaded   bool
	// refreshing is true while an MCP re-connect runs off the UI thread: the
	// Refresh call pings every client and can block on a hung server, so it
	// never runs inside the frame's click handler.
	refreshing bool
	// toolsDisabledOnly filters the tool catalogue to the currently disabled
	// tools (a "what did I turn off?" view in a 70-row list).
	toolsDisabledOnly bool
	disabledErr       string
	status            string
	statusErr         bool

	// MCP server-level switches (mcp.disabledServers): parsed once; matching is
	// trimmed/case-insensitive.
	disabledServers       map[string]bool
	disabledServersLoaded bool

	// Official Lists for the page's collections: MCP servers, the built-in
	// module inventory, the curated settings rows and the tool catalogue.
	mcpList     ui.ListState
	modulesList ui.ListState
	paramsList  ui.ListState
	toolsList   ui.ListState

	// modulesOpen is the built-in module section's disclosure state.
	modulesOpen bool

	// Plugin settings section (curated, already-wired keys).
	paramsOpen   bool
	paramEditing bool
	paramKey     string
	paramTitle   string
	paramDraft   string
	// paramSensitive marks the row being edited as a credential, so the modal
	// uses the official Password input.
	paramSensitive bool
	paramErrText   string
	paramNotice    string
	paramErr       bool

	// mcpExpanded remembers the expanded MCP server cards, keyed by server
	// identity, so a Refresh preserves the open cards.
	mcpExpanded map[string]bool

	// startupDisabledTools is the disabled-tool set read at activation. The
	// toolkit only re-registers tools at process start, so a row shows "restart
	// needed" while its current switch state differs from this snapshot; a
	// switch flipped back to its startup state clears the hint again.
	startupDisabledTools map[string]bool

	// pendingRestartServers marks MCP servers enabled during this visit. The
	// toolkit starts servers at launch, so an enabled settings row is not in
	// the live service yet: the page synthesizes its card instead of letting it
	// vanish until the next restart.
	pendingRestartServers map[string]bool

	// navigator is the host navigator (deep links to the settings MCP page).
	navigator contracts.Navigator
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

// Module registers the Plugins screen.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		// The navigator is required so the screen is reachable from the host's
		// navigation surface, and so the empty MCP state can deep-link to the
		// settings page that configures servers.
		navigator, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		config := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		moduleState := &state{
			ctx: ctx, locale: locale, navigator: navigator,
			mcpExpanded:           make(map[string]bool),
			pendingRestartServers: make(map[string]bool),
		}
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterScreen(contracts.ScreenSpec{
				ID:    ScreenID,
				Title: config.Title,
				Order: 30,
				Build: func(c *ui.Context) {
					buildScreen(c, runtime, config, moduleState)
				},
			})
		}); err != nil {
			return err
		}
		// The screen's chrome lives in the host's B3 top band: the title
		// (SlotScreenHeader) and the MCP refresh utility (SlotScreenActions),
		// beside the host's palette control. The item IDs carry the screen
		// namespace so the band stays on this screen only.
		screenSlots := []contracts.SlotItem{
			{
				Slot: contracts.SlotScreenHeader, ID: "plugins.title", Order: 10,
				Build: func(c *ui.Context) { buildScreenHeaderSlot(c, design.Of(c), config, moduleState) },
			},
			{
				Slot: contracts.SlotScreenActions, ID: "plugins.actions", Order: 10,
				Build: func(c *ui.Context) { buildScreenActionsSlot(c, design.Of(c), runtime, moduleState) },
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
		WithName("ZeoKeel plugins screen")
}

// openSettingsPage opens the settings surface on one page through the host's
// optional deep-link capability, falling back to toggling the surface when the
// navigator does not implement it.
func (st *state) openSettingsPage(pageID string) {
	if st.navigator == nil {
		return
	}
	if opener, ok := st.navigator.(interface{ OpenSettingsPage(string) error }); ok {
		if err := opener.OpenSettingsPage(pageID); err == nil {
			return
		}
	}
	st.navigator.ToggleSettings()
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
