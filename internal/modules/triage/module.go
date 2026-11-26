package triage

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/triage"

// Config is the (empty) typed assembly config.
type Config struct{}

// Module is the triage descriptor: it requires the inbox and the task board and
// provides the only service that may turn an inbox item into work.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, _ Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		board, err := ctx.Require(contracts.KeyTaskBoard)
		if err != nil {
			return fmt.Errorf("%s: require task board: %w", ModuleID, err)
		}
		// The lease layer is required, not optional: a dispatched run must start
		// under a budget (2026-11-26 audit, 中高), and the budget lives there.
		// Declaring it orders tasklease before triage at activation; an assembly
		// without it fails to boot instead of silently dispatching unmetered.
		leases, err := ctx.Require(contracts.KeyTaskLease)
		if err != nil {
			return fmt.Errorf("%s: require task lease: %w", ModuleID, err)
		}
		service := NewService(inboxService, board)
		// The wake-up connector needs the chat service the agent module provides
		// (contracts.KeyChat). It stays OPTIONAL — a product assembled without a
		// chat service still triages, it just never runs anything by itself — but
		// it must be DECLARED, not merely probed.
		//
		// Why the declaration is the fix (2026-11-26): the loader builds its
		// schedule from the declared requirements and places every provider
		// before its consumer, "so a module that probes an optional service at
		// activation sees it when the provider is part of the assembly"
		// (kernel/module/validate.go buildSchedule). This dependency was declared
		// nowhere — the connector was merely described as "optional" — so the
		// schedule kept its configuration order: triage is entry 11, the agent
		// that provides the chat service is entry 32, and the probe below always
		// returned nil. NewDispatcher turns a nil chat
		// into a nil connector, Dispatcher.Wants then answers false for every
		// assignee, and a task "assigned to lead" woke nobody while the board
		// showed an owner. Declaring the dependency makes the load order correct
		// at the root of the problem instead of adding a second, lazy path at
		// run time; it also fails loudly at pre-flight if the type ever changes.
		var (
			chatService contracts.ChatService
			settings    contracts.SettingsService
			logger      contracts.Logger
		)
		if chat, ok := ctx.Get(contracts.KeyChat); ok && chat != nil {
			chatService = chat
		}
		if row, ok := ctx.Get(contracts.KeySettings); ok && row != nil {
			settings = row
		}
		if log, ok := ctx.Get(contracts.KeyLogger); ok && log != nil {
			logger = log
		}
		if chatService == nil && logger != nil {
			// An assembly genuinely without chat: say so once, so "dispatch is
			// inert here" is a stated property of this product rather than a
			// silence somebody has to rediscover.
			logger.Printf("[triage] no chat service in this assembly: the dispatch wake-up stays off")
		}
		service.WithDispatcher(NewDispatcher(chatService, settings, logger).WithLeases(leases))
		if _, err := ctx.Provide(contracts.KeyTriage, contracts.TriageService(service)); err != nil {
			return fmt.Errorf("%s: provide triage: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyTriage)).
		AddRequires(
			module.Requires(contracts.KeyInbox),
			module.Requires(contracts.KeyTaskBoard),
			module.Requires(contracts.KeyTaskLease),
			// Non-gating, ordering-only: see the comment at the first use. A
			// declared requirement is what places its provider earlier in the
			// load schedule; without it this probe is a race against entry order.
			module.RequiresOptional(contracts.KeyChat),
		).
		WithName("ZeoKeel triage (inbox → task board)")
}
