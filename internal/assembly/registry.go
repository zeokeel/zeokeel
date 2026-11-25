package assembly

import (
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/acp"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/appinfo"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
	"github.com/zeokeel/zeokeel/internal/modules/automation"
	"github.com/zeokeel/zeokeel/internal/modules/browser"
	"github.com/zeokeel/zeokeel/internal/modules/creds"
	"github.com/zeokeel/zeokeel/internal/modules/dingtalk"
	"github.com/zeokeel/zeokeel/internal/modules/email"
	"github.com/zeokeel/zeokeel/internal/modules/feishu"
	"github.com/zeokeel/zeokeel/internal/modules/goal"
	"github.com/zeokeel/zeokeel/internal/modules/hosttools"
	"github.com/zeokeel/zeokeel/internal/modules/importer"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/insights"
	"github.com/zeokeel/zeokeel/internal/modules/lead"
	"github.com/zeokeel/zeokeel/internal/modules/memorystore"
	"github.com/zeokeel/zeokeel/internal/modules/modelcatalog"
	"github.com/zeokeel/zeokeel/internal/modules/notifyroute"
	"github.com/zeokeel/zeokeel/internal/modules/sandbox"
	"github.com/zeokeel/zeokeel/internal/modules/sessionstore"
	"github.com/zeokeel/zeokeel/internal/modules/skills"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
	"github.com/zeokeel/zeokeel/internal/modules/triage"
	"github.com/zeokeel/zeokeel/internal/modules/update"
	"github.com/zeokeel/zeokeel/internal/modules/webhook"
	"github.com/zeokeel/zeokeel/internal/modules/wechat"
	"github.com/zeokeel/zeokeel/internal/modules/workspace"
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
// product: settings, log and the data dir. The browser transport's services
// (interaction, artifacts, plans, notifications, foreground session) are
// provided by whatever runs it (gateway.Provide for --product serve).
func Registry() *module.Registry {
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeySettings)
	registry.AddBuiltin(contracts.KeyLogger)
	// KeyDataDir is provided by the process host in every product: the store
	// module opens its database under it.
	registry.AddBuiltin(contracts.KeyDataDir)

	mustRegister(registry, appinfo.Module())
	// The inbox is the single external entry point: register it before every
	// consumer so the Lead/triage path can always require it.
	mustRegister(registry, inbox.Module())
	mustRegister(registry, sessionstore.Module())
	mustRegister(registry, approvals.Module())
	mustRegister(registry, workspace.Module())
	mustRegister(registry, skills.Module())
	mustRegister(registry, memorystore.Module())
	mustRegister(registry, sandbox.Module())
	mustRegister(registry, toolkit.Module())
	// triage joins the inbox to the task board: it must come after both.
	mustRegister(registry, triage.Module())
	// The triage pass joins inbox + triage: it must come after both.
	mustRegister(registry, lead.Module())
	// tasklease wraps the task board: it must come after toolkit.
	mustRegister(registry, tasklease.Module())
	mustRegister(registry, goal.Module())
	mustRegister(registry, agent.Module())
	mustRegister(registry, automation.Module())
	mustRegister(registry, update.Module())
	mustRegister(registry, importer.Module())
	mustRegister(registry, acp.Module())
	mustRegister(registry, modelcatalog.Module())
	// Notification routing: pure policy, no channel dependency.
	mustRegister(registry, notifyroute.Module())
	mustRegister(registry, insights.Module())
	// Local browser (CDP): no requirements. It loads without an endpoint and
	// reports disconnected, so a missing or stopped Chrome never blocks boot.
	mustRegister(registry, browser.Module())
	mustRegister(registry, hosttools.Module())
	// Channel adapters: the webhook is the first one, and it only needs the inbox.
	// Credential store: channel adapters need it, it needs only the data dir.
	mustRegister(registry, creds.Module())
	mustRegister(registry, webhook.Module())
	mustRegister(registry, wechat.Module())
	mustRegister(registry, dingtalk.Module())
	mustRegister(registry, feishu.Module())
	mustRegister(registry, email.Module())
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
