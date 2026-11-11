// Package sandbox owns ZeoKeel's OS sandbox: it probes the platform runner once
// (Linux bwrap/Landlock, macOS Seatbelt with a denial self-test, Windows
// restricted-token ACL) and exposes it as contracts.SandboxService so the
// toolkit can inject it into the command-executing tools and the agent gate can
// auto-allow shell-family tools.
//
// The sandbox is always on: an agent must not run commands unconfined. When the
// host has no usable runner, Runner returns a fail-closed runner and commands
// error instead of silently running unrestricted; Status tells the settings
// surface why, and Available keeps the gate on the approval path.
package sandbox

import (
	"fmt"
	"runtime"
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
			logger.Printf("[sandbox] unavailable on %s (commands fail closed): %v", runtime.GOOS, probeErr)
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

// Runner returns the runner injected into tools. An unusable host gets a
// fail-closed runner, never nil, so commands cannot run unconfined.
func (s *service) Runner() rigsandbox.Runner {
	if s == nil || s.probeError != nil || s.runner == nil {
		var reason error
		if s != nil {
			reason = s.probeError
		}
		return unavailableRunner{reason: reason}
	}
	return s.runner
}

// Status returns the settings/diagnostics read model.
func (s *service) Status() contracts.SandboxStatus {
	if s == nil {
		return contracts.SandboxStatus{Platform: runtime.GOOS}
	}
	status := contracts.SandboxStatus{
		Available: s.Available(),
		Platform:  s.platform,
	}
	if !status.Available && s.probeError != nil {
		status.Reason = s.probeError.Error()
	}
	return status
}

// unavailableRunner is injected when this host has no usable runner: every
// Confine fails closed instead of running unconfined.
type unavailableRunner struct {
	reason error
}

// Confine implements sandbox.Runner.
func (r unavailableRunner) Confine([]string, *rigsandbox.Policy) (rigsandbox.ConfinedCommand, error) {
	reason := r.reason
	if reason == nil {
		reason = rigsandbox.ErrSandboxUnavailable
	}
	return rigsandbox.ConfinedCommand{}, fmt.Errorf("os sandbox unavailable (%v): %w", reason, rigsandbox.ErrSandboxUnavailable)
}
