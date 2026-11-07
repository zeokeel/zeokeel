package assembly

import (
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/acp"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/appinfo"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
	"github.com/zeokeel/zeokeel/internal/modules/automation"
	"github.com/zeokeel/zeokeel/internal/modules/hosttools"
	"github.com/zeokeel/zeokeel/internal/modules/importer"
	"github.com/zeokeel/zeokeel/internal/modules/memory"
	"github.com/zeokeel/zeokeel/internal/modules/modelcatalog"
	"github.com/zeokeel/zeokeel/internal/modules/sessionstore"
	"github.com/zeokeel/zeokeel/internal/modules/skills"
	"github.com/zeokeel/zeokeel/internal/modules/terminal"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
	"github.com/zeokeel/zeokeel/internal/modules/update"
	"github.com/zeokeel/zeokeel/internal/modules/workspace"
	"github.com/zeokeel/zeokeel/internal/ui/about"
	"github.com/zeokeel/zeokeel/internal/ui/approval"
	automationui "github.com/zeokeel/zeokeel/internal/ui/automation"
	"github.com/zeokeel/zeokeel/internal/ui/home"
	"github.com/zeokeel/zeokeel/internal/ui/interaction"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
	"github.com/zeokeel/zeokeel/internal/ui/toasts"
	"github.com/zeokeel/zeokeel/internal/ui/toolcards"
	"github.com/zeokeel/zeokeel/internal/ui/welcome"
)

// Registry builds the compile-time registry of every module linked into this
// binary. Registration is explicit: a module appears in the list below or it
// does not exist for any product. There is no init()-time registration and no
// runtime code loading; adding a module means adding a line here and
// recompiling.
//
// The set is fixed at link time. Embedded product assemblies only choose (and
// configure) which of these modules a product enables.
//
// Registry declares the service contracts the process host provides in every
// product: settings and log. Product-specific host services (the UI services
// provided by internal/uiroot) are declared by the loading entry points
// (Validate/Load/LoadWithBuildInfo) when the assembly actually uses them, so
// the same registry serves windowed and headless products.
func Registry() *module.Registry {
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeySettings)
	registry.AddBuiltin(contracts.KeyLogger)
	// KeyDataDir is provided by the process host in every product: the store
	// module opens its database under it.
	registry.AddBuiltin(contracts.KeyDataDir)

	mustRegister(registry, appinfo.Module())
	mustRegister(registry, sessionstore.Module())
	mustRegister(registry, approvals.Module())
	mustRegister(registry, workspace.Module())
	mustRegister(registry, terminal.Module())
	mustRegister(registry, skills.Module())
	mustRegister(registry, memory.Module())
	mustRegister(registry, toolkit.Module())
	mustRegister(registry, agent.Module())
	mustRegister(registry, automation.Module())
	mustRegister(registry, update.Module())
	mustRegister(registry, importer.Module())
	mustRegister(registry, acp.Module())
	mustRegister(registry, modelcatalog.Module())
	mustRegister(registry, hosttools.Module())
	mustRegister(registry, home.Module())
	mustRegister(registry, about.Module())
	mustRegister(registry, interaction.Module())
	mustRegister(registry, toasts.Module())
	mustRegister(registry, welcome.Module())
	mustRegister(registry, toolcards.Module())
	mustRegister(registry, sidebar.Module())
	mustRegister(registry, session.Module())
	mustRegister(registry, settings.Module())
	mustRegister(registry, approval.Module())
	mustRegister(registry, rightpanel.Module())
	mustRegister(registry, automationui.Module())
	mustRegister(registry, plugins.Module())
	mustRegister(registry, GroupModule())
	return registry
}

// mustRegister registers a descriptor and panics on a duplicate or invalid
// definition. The list above is compile-time static, so a registration failure
// is a programming error: failing loudly at startup is better than silently
// dropping a module from every product.
func mustRegister[T any](registry *module.Registry, descriptor *module.Descriptor[T]) {
	if err := registry.Register(descriptor); err != nil {
		panic("assembly: " + err.Error())
	}
}
