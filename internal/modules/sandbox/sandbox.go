// Package sandbox owns ZeoKeel's OS sandbox: it probes the platform runner once
// (Linux bwrap/Landlock, macOS Seatbelt with a denial self-test, Windows
// restricted-token ACL) and exposes it as contracts.SandboxService so the
// toolkit can inject it into the command-executing tools and the agent gate can
// auto-allow shell-family tools.
//
// Personal-client model: the OS sandbox is used whenever the machine has one.
// When it does not, Runner returns nil and commands run with the host's own
// permissions (the application-level policy and the approval gate still apply) —
// a personal harness should not fail every command because bwrap is missing.
// Status tells the settings surface why there is no OS fence.
package sandbox

import (
	"runtime"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/sandbox/osrunner"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/sandbox"

// Config is the module's typed assembly config.
type Config struct {
	// ProbeTimeoutMs bounds each functional probe of the platform runner.
	// Zero uses osrunner.DefaultProbeTimeout.
	ProbeTimeoutMs int `json:"probeTimeoutMs"`
}

// runnerFactory builds the platform runner; it is injectable for tests.
type runnerFactory func(osrunner.Config) (rigsandbox.Runner, error)

// service implements contracts.SandboxService.
type service struct {
	logger     contracts.Logger
	platform   string
	runner     rigsandbox.Runner
	probeError error
}

var _ contracts.SandboxService = (*service)(nil)

// Module provides contracts.KeySandbox.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		logger, _ := ctx.Get(contracts.KeyLogger)
		service := newService(cfg, logger, nil)
		_, err := ctx.Provide(contracts.KeySandbox, contracts.SandboxService(service))
		return err
	}).
		AddProvides(module.Provides(contracts.KeySandbox)).
		WithName("ZeoKeel OS sandbox")
}

// newService probes the platform runner once. A nil factory uses the real
// osrunner.New; tests inject a fake.
func newService(cfg Config, logger contracts.Logger, factory runnerFactory) *service {
	if factory == nil {
		factory = osrunner.New
	}
	timeout := time.Duration(cfg.ProbeTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = osrunner.DefaultProbeTimeout
	}
	runner, probeErr := factory(osrunner.Config{ProbeTimeout: timeout})
	service := &service{
		logger:     logger,
		platform:   runtime.GOOS,
		runner:     runner,
		probeError: probeErr,
	}
	if logger != nil {
		switch {
		case probeErr != nil:
			logger.Printf("[sandbox] unavailable on %s (commands run with host permissions): %v", runtime.GOOS, probeErr)
		case runner != nil:
			logger.Printf("[sandbox] enabled on %s", runtime.GOOS)
		}
	}
	return service
}

// Available reports whether the host has a usable runner.
func (s *service) Available() bool {
	return s != nil && s.probeError == nil && s.runner != nil
}

// Runner returns the platform runner, or nil when this host has none. A nil
// runner makes the command tools run under the application-level policy only
// (best effort); it never fails startup.
func (s *service) Runner() rigsandbox.Runner {
	if s == nil || s.probeError != nil || s.runner == nil {
		return nil
	}
	return s.runner
}

// Status returns the settings/diagnostics read model.
//
// Confinement is derived from what the runner actually reports, not from
// "a runner object exists": a runner that reports EnforcementNone confines
// nothing, and since commands no longer park on an approval card (2026-11-26)
// that difference is the whole fence. The state is therefore reported, so the
// settings/status surface can say "no OS confinement" instead of implying one.
func (s *service) Status() contracts.SandboxStatus {
	if s == nil {
		return contracts.SandboxStatus{
			Platform:           runtime.GOOS,
			Confinement:        contracts.SandboxConfinementNone,
			CommandsUnconfined: true,
			Note:               confinementNote("", "no sandbox service is assembled"),
		}
	}
	status := contracts.SandboxStatus{
		Available: s.Available(),
		Platform:  s.platform,
	}
	if !status.Available && s.probeError != nil {
		status.Reason = s.probeError.Error()
	}
	runner := s.Runner()
	var enforcement rigsandbox.Enforcement
	if runner != nil {
		_, enforcement, _ = rigsandbox.RunnerStatus(runner)
	}
	if runner != nil && enforcement != rigsandbox.EnforcementNone {
		status.Confinement = contracts.SandboxConfinementOS
		return status
	}
	status.Confinement = contracts.SandboxConfinementNone
	status.CommandsUnconfined = true
	reason := status.Reason
	if runner != nil {
		reason = "the runner reports enforcement " + string(enforcement)
	}
	status.Note = confinementNote(reason, "")
	return status
}

// confinementNote renders the one sentence a status surface shows for an
// unconfined command fence. Both the sandbox module (settings surface) and the
// agent's log record use it, so the two can never disagree about what the user
// is being told.
func confinementNote(reason, fallback string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = strings.TrimSpace(fallback)
	}
	if reason == "" {
		reason = "no usable OS sandbox runner on this host"
	}
	return "Shell commands run without OS confinement (" + reason + "): the working-directory write rule is an application rule, not an enforced boundary."
}
