package agent

// OS-sandbox shell auto-allow.
//
// "This shell call is already confined, so the user need not approve it" is the
// SDK's judgement, not the product's: gate.AllowSandboxedCommands checks the
// shell surface, that the runner reports real enforcement (a runner that reports
// none never qualifies), and that the active policy is not danger-full-access.
// The product only supplies the live runner and the policy the toolkit built for
// the current workspace — no extra mode rules, no private shell list.

import (
	"context"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/gate"
)

// sandboxedCommand reports whether the SDK considers this call an already
// confined shell call, so the approval prompt can be skipped.
//
// root is the workspace the run's tools are bound to: the decision must use the
// same policy the tool call will execute under, not whichever workspace happens
// to be current when the gate runs.
func (s *service) sandboxedCommand(ctx context.Context, root string, tool rig.Tool, call rig.ToolCall) bool {
	if s == nil || s.sandboxService == nil || s.policySource == nil {
		return false
	}
	policy := s.policySource.PolicyFor(root)
	if policy == nil {
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

// sandboxShellTools is the command-execution surface this product lets the SDK
// judge for the "already confined" exemption. Jobs share the bash tool; pwsh is
// the Windows shell.
var sandboxShellTools = []string{"bash", "pwsh"}
