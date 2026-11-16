// Package plugins contributes the Plugins screen: the product's extension hub.
// It manages skills (Markdown instruction packs discovered from the workspace
// and the data directory) and MCP connectors (the external tool servers the
// toolkit connects at launch), and reports the built-in tool catalogue and the
// compiled-in module inventory. It is a UI module like ui/automation: a kernel
// Descriptor that requires the host UI runtime and navigator, registers the
// "plugins" screen and reads optional contracts.PluginsService,
// contracts.SkillsService and contracts.KeyToolkit (no Requires). Missing
// backends degrade to per-section empty states. Every action runs through its
// service and invalidates the frame.
package plugins

import (
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/plugins"

// ScreenID is the screen the module contributes.
const ScreenID = "plugins"

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the screen title and page heading (default "能力").
	Title string `json:"title"`
	// Description is the page subtitle.
	Description string `json:"description"`
	// SkillsSection is the skills section label (default "技能").
	SkillsSection string `json:"skillsSection"`
	// MCPSection is the connectors tab label (default "连接器").
	MCPSection string `json:"mcpSection"`
}

func (c Config) resolved() Config {
	return Config{
		Title:         textutil.FirstNonEmptyTrimmed(c.Title, "能力"),
		Description:   textutil.FirstNonEmptyTrimmed(c.Description, "管理技能与 MCP 连接器，扩展 ZeoKeel 的能力。"),
		SkillsSection: textutil.FirstNonEmptyTrimmed(c.SkillsSection, "技能"),
		MCPSection:    textutil.FirstNonEmptyTrimmed(c.MCPSection, "连接器"),
	}
}

// state is the module's UI state. It is only touched on the UI thread.
type state struct {
	ctx   cordis.Context
	query string
	// locale localizes the screen copy per frame (zh is the default).
	locale contracts.LocaleService

	// runtime is the host UI runtime: async connector work posts results here.
	runtime contracts.UIRuntime
	// refreshing is true while an MCP re-connect runs off the UI thread: the
	// Refresh call pings every client and can block on a hung server, so it
	// never runs inside the frame's click handler.
	refreshing bool
	status     string
	statusErr  bool

	// capabilityTab is the chosen page-level tab of the capability shell
	// (skills, connectors and the future workflow tab are parallel entries).
	capabilityTab int

	// MCP server-level switches (mcp.disabledServers): parsed once; matching is
	// trimmed/case-insensitive. The connectors section reads and writes them.
	disabledServers       map[string]bool
	disabledServersLoaded bool

	// Official Lists for the diagnostic collections below the tabs: the module
	// inventory and the curated plugin settings rows. The skills and connector
	// cards are official GridViews owned by their tabs.
	modulesList ui.ListState
	paramsList  ui.ListState

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

	// pendingRestartServers marks MCP connectors enabled during this visit. The
	// toolkit starts servers at launch, so an enabled settings row is not in
	// the live service yet: the connectors section synthesizes its card instead
	// of letting it vanish until the next restart.
	pendingRestartServers map[string]bool

	// navigator is the host navigator. It stays available for modules that want
	// to deep-link from this screen; the screen itself has no settings pages to
	// open anymore (skills and MCP now live here).
	navigator contracts.Navigator

	// skills is the skills section's state (skills_section.go/skills_edit.go).
	skills *skillsState
	// connectors is the MCP connectors section's state
	// (connectors_section.go/connectors_edit.go).
	connectors *connectorsState
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
		// navigation surface. The page itself no longer deep-links into settings.
		navigator, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		config := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		moduleState := &state{
			ctx: ctx, locale: locale, navigator: navigator, runtime: runtime,
			skills:                newSkillsState(),
			connectors:            newConnectorsState(),
			pendingRestartServers: make(map[string]bool),
		}
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterScreen(contracts.ScreenSpec{
				ID:    ScreenID,
				Title: config.Title,
				Order: 30,
				Build: func(c *ui.Context) {
					buildScreen(c, config, moduleState)
				},
			})
		}); err != nil {
			return err
		}
		// The screen's chrome lives in the host's B3 top band: only the title.
		// The page's actions belong to its tabs (each tab carries its own
		// refresh/create row), so the band does not switch actions with a tab.
		screenSlots := []contracts.SlotItem{
			{
				Slot: contracts.SlotScreenHeader, ID: "plugins.title", Order: 10,
				Build: func(c *ui.Context) { buildScreenHeaderSlot(c, design.Of(c), config, moduleState) },
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
