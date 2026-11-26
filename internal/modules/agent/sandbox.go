package agent

// OS-sandbox shell auto-allow.
//
// "This shell call is already confined, so the user need not approve it" is the
// SDK's judgement, not the product's: gate.AllowSandboxedCommands checks the
// shell surface, that the runner reports real enforcement (a runner that reports
// none never qualifies), and that the policy is not danger-full-access. The
// product supplies the live runner, the policy the toolkit built for the run's
// workspace root, and the shell surface it explicitly wants judged
// (sandboxShellTools: bash/pwsh).
//
// Personal-helper permission model (2026-11-26): the command family never parks
// on an approval card while the sandbox module is assembled. The SDK judgement
// still runs first — an OS-confined call is exempt *because it is confined* —
// and a call the SDK does not certify (no runner on this host, a runner that
// reports no enforcement, a danger-full-access policy) is exempt too. Nothing
// falls back to the approval dialog implicitly, which would hide the difference
// between "confined" and "unconstrained" behind "the user approved it once".
//
// That difference is disclosed in three places, and the log line is the weakest
// of them (2026-11-26 audit, severe #1): the gateway's `sandbox.status` read
// model (rendered on the run-status settings page) reports `confinement`/
// `commandsUnconfined`; the model-facing prompt carries `unconfinedCommandsLine`;
// and `recordUnconfinedShell` adds one log line per session+tool. The log alone
// was never disclosure — a client cannot see it.

import (
	"context"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/gate"
	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sandboxedCommand reports whether the SDK considers this call an already
// confined shell call, so the approval prompt can be skipped.
//
// root is the workspace the run's tools are bound to: the decision must use the
// same policy the tool call will execute under, not whichever workspace happens
// to be current when the gate runs.
func (s *service) sandboxedCommand(ctx context.Context, root string, tool rig.Tool, call rig.ToolCall) bool {
	policy := s.commandPolicy(root)
	if policy == nil || s.sandboxService == nil {
		return false
	}
	// Ask the SDK with an inner gate of nil: it answers allow only for a shell
	// call the runner would confine, so an allow doubles as "this is a shell
	// call". The shell surface is pinned here instead of inheriting the SDK's
	// default, so a future SDK default cannot silently widen what skips the
	// prompt.
	allowance := gate.AllowSandboxedCommands(gate.SandboxedOptions{
		Runner:     s.sandboxService.Runner(),
		Policy:     policy,
		ShellTools: sandboxShellTools,
	}, nil)
	decision, err := allowance.Approve(ctx, tool, call)
	return err == nil && decision == rig.DecisionAllow
}

// shellCommandExempt reports whether this call belongs to the command family the
// product runs without an approval prompt whenever the sandbox module is
// assembled — including the case where the SDK does not certify confinement.
//
// The distinction is reported, not hidden: `sandboxedCommand` is the "confined"
// half (certified by the SDK, nothing to warn about) and this is the "runs
// anyway, but unconstrained" half, recorded by recordUnconfinedShell. An
// assembly without the sandbox module keeps the approval flow (nil service), so
// a stripped product never pretends to have judged confinement.
func (s *service) shellCommandExempt(root string, tool rig.Tool, call rig.ToolCall) bool {
	if s == nil || s.sandboxService == nil || s.commandPolicy(root) == nil {
		return false
	}
	return inSandboxShellFamily(tool, call)
}

// commandPolicy is the policy that dispatches the run's shell calls, or nil when
// this assembly has no toolkit policy source.
func (s *service) commandPolicy(root string) *rigsandbox.Policy {
	if s == nil || s.policySource == nil {
		return nil
	}
	return s.policySource.PolicyFor(root)
}

// confinementNote renders the honest state of the command fence for the record:
// which runner exists (if any) and what the policy allows writes to.
func (s *service) confinementNote(root string) string {
	policy := s.commandPolicy(root)
	if policy == nil {
		return "no application-level policy is available"
	}
	if runner := s.sandboxService.Runner(); runner != nil {
		name, enforcement, _ := rigsandbox.RunnerStatus(runner)
		return fmt.Sprintf("runner %q reports enforcement %q over policy mode %q",
			name, enforcement, policy.Mode())
	}
	status := s.sandboxService.Status()
	reason := strings.TrimSpace(status.Reason)
	if reason == "" {
		reason = "no usable runner on this host"
	}
	return fmt.Sprintf("no OS confinement (%s); writes are bounded only by the application policy mode %q",
		reason, policy.Mode())
}

// commandConfinement reports whether this host's shell commands are confined by
// the OS. It returns "" when no sandbox module is assembled: that assembly keeps
// the approval flow, so "unconfined" would be the wrong word for it (nothing ran
// without asking). The answer comes from the sandbox service's own read model so
// the model-facing prompt, the settings surface and the log record cannot drift.
func (s *service) commandConfinement() string {
	if s == nil || s.sandboxService == nil {
		return ""
	}
	switch s.sandboxService.Status().Confinement {
	case contracts.SandboxConfinementOS:
		return contracts.SandboxConfinementOS
	case contracts.SandboxConfinementNone:
		return contracts.SandboxConfinementNone
	default:
		// An unknown state must not be reported as confinement.
		return contracts.SandboxConfinementNone
	}
}

// commandsRunUnconfined reports the one condition the model-facing prompt has to
// disclose: the command family is exempt from approval AND this host has no
// OS-level fence for it.
func (s *service) commandsRunUnconfined(root string) bool {
	return s.sandboxService != nil && s.commandPolicy(root) != nil &&
		s.commandConfinement() == contracts.SandboxConfinementNone
}

// recordUnconfinedShell states once per session+tool that a command ran without
// an approval prompt *and* without OS confinement. It is the "record it honestly"
// half of requirement 4: the user asked not to be asked, not to be misled about
// what a command can touch. Callers must not treat a logged line as approval.
func (s *service) recordUnconfinedShell(sessionID, name, root string) {
	if s == nil {
		return
	}
	key := strings.TrimSpace(sessionID) + "\x00" + strings.TrimSpace(name)
	s.mu.Lock()
	if s.unconfinedShellNoted == nil {
		s.unconfinedShellNoted = make(map[string]bool, 2)
	}
	if s.unconfinedShellNoted[key] {
		s.mu.Unlock()
		return
	}
	s.unconfinedShellNoted[key] = true
	s.mu.Unlock()
	if s.logger == nil {
		return
	}
	s.logger.Printf("[gate] %s ran without an approval prompt: %s (session %s, workspace %s)",
		strings.TrimSpace(name), s.confinementNote(root), sessionID, root)
}

// inSandboxShellFamily reports whether the call is one of the command-execution
// tools this product exempts (the same pinned surface the SDK judgement uses).
func inSandboxShellFamily(tool rig.Tool, call rig.ToolCall) bool {
	name := call.Name
	if tool != nil && strings.TrimSpace(tool.Name()) != "" {
		name = tool.Name()
	}
	name = strings.TrimSpace(name)
	for _, shell := range sandboxShellTools {
		if name == shell {
			return true
		}
	}
	return false
}

// sandboxShellTools is the command-execution surface this product lets the SDK
// judge for the "already confined" exemption. Jobs share the bash tool; pwsh is
// the Windows shell.
var sandboxShellTools = []string{"bash", "pwsh"}
