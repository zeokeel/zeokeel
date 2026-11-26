package assembly

import (
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/acp"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/appinfo"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
	"github.com/zeokeel/zeokeel/internal/modules/audit"
	"github.com/zeokeel/zeokeel/internal/modules/automation"
	"github.com/zeokeel/zeokeel/internal/modules/bot"
	"github.com/zeokeel/zeokeel/internal/modules/browser"
	"github.com/zeokeel/zeokeel/internal/modules/channeldispatch"
	"github.com/zeokeel/zeokeel/internal/modules/clilink"
	"github.com/zeokeel/zeokeel/internal/modules/creds"
	"github.com/zeokeel/zeokeel/internal/modules/dingtalk"
	"github.com/zeokeel/zeokeel/internal/modules/email"
	"github.com/zeokeel/zeokeel/internal/modules/feishu"
	"github.com/zeokeel/zeokeel/internal/modules/goal"
	"github.com/zeokeel/zeokeel/internal/modules/heartbeat"
	"github.com/zeokeel/zeokeel/internal/modules/hosttools"
	"github.com/zeokeel/zeokeel/internal/modules/importer"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/insights"
	"github.com/zeokeel/zeokeel/internal/modules/lead"
	"github.com/zeokeel/zeokeel/internal/modules/memorystore"
	"github.com/zeokeel/zeokeel/internal/modules/modelcatalog"
	"github.com/zeokeel/zeokeel/internal/modules/notifyroute"
	"github.com/zeokeel/zeokeel/internal/modules/replyrelay"
	"github.com/zeokeel/zeokeel/internal/modules/reports"
	"github.com/zeokeel/zeokeel/internal/modules/sandbox"
	"github.com/zeokeel/zeokeel/internal/modules/sessionstore"
	"github.com/zeokeel/zeokeel/internal/modules/skilldraft"
	"github.com/zeokeel/zeokeel/internal/modules/skills"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
	"github.com/zeokeel/zeokeel/internal/modules/teamnotes"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
	"github.com/zeokeel/zeokeel/internal/modules/triage"
	"github.com/zeokeel/zeokeel/internal/modules/update"
	"github.com/zeokeel/zeokeel/internal/modules/users"
	"github.com/zeokeel/zeokeel/internal/modules/webhook"
	"github.com/zeokeel/zeokeel/internal/modules/wechat"
	"github.com/zeokeel/zeokeel/internal/modules/workflow"
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
	// Shared team notes ride on the memory store, so they load after it.
	mustRegister(registry, teamnotes.Module())
	// Skill drafts: proposals a human turns into a real skill later.
	mustRegister(registry, skilldraft.Module())
	// Bots: templates and instances a task can be dispatched to.
	mustRegister(registry, bot.Module())
	// Users: the role roster the permission table (contracts.Allowed) reads.
	mustRegister(registry, users.Module())
	mustRegister(registry, sandbox.Module())
	mustRegister(registry, toolkit.Module())
	// triage joins the inbox to the task board: it must come after both.
	mustRegister(registry, triage.Module())
	// The triage pass joins inbox + triage: it must come after both.
	mustRegister(registry, lead.Module())
	// The autonomous loop starts last among the work modules: it wants the lead
	// and the channel adapters to already exist.
	mustRegister(registry, heartbeat.Module())
	// The reply relay joins the inbox and the approval action service.
	mustRegister(registry, replyrelay.Module())
	// The dispatcher executes the routing plan against the adapters.
	mustRegister(registry, channeldispatch.Module())
	// Structured reports (M4): kept even when no channel is bound; delivery is
	// optional, so it registers after the dispatcher it prefers to use.
	mustRegister(registry, reports.Module())
	// tasklease wraps the task board: it must come after toolkit.
	mustRegister(registry, tasklease.Module())
	mustRegister(registry, goal.Module())
	mustRegister(registry, agent.Module())
	// The audit loop joins the chat service to the lease layer: it must come
	// after both (it opens auditor sessions and reads the review queue).
	mustRegister(registry, audit.Module())
	// The workflow runner projects stage tasks onto the board and dispatches
	// them through the chat service: after the board, chat and the audit loop.
	mustRegister(registry, workflow.Module())
	mustRegister(registry, automation.Module())
	mustRegister(registry, update.Module())
	mustRegister(registry, importer.Module())
	mustRegister(registry, acp.Module())
	mustRegister(registry, modelcatalog.Module())
	// External CLI links: resolve where a vendor CLI lives (dws / lark-cli),
	// probe its version and report whether its skill bundle is indexed. It
	// requires only the logger — no endpoint, no channel, no inbox — and a
	// missing CLI is a reported state, never a boot failure.
	mustRegister(registry, clilink.Module())
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
