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

// sandboxedCommandDecision reports whether the call is a shell call the SDK would
// confine, and whether the product may run it without asking.
//
// shell is false when the SDK does not recognise the call as a shell call (file
// tools never go through the runner) or when no runner/policy is available.
// allowed is true only when the SDK would allow it and the product's read-only
// rule does not forbid commands in this mode.
func (s *service) sandboxedCommandDecision(ctx context.Context, tool rig.Tool, call rig.ToolCall) (shell, allowed bool) {
	if s == nil || s.sandboxService == nil || s.policySource == nil {
		return false, false
	}
	policy := s.policySource.CurrentPolicy()
	if policy == nil {
		return false, false
	}
	// Ask the SDK with an inner gate of nil: it answers allow for a confined
	// shell call and deny for everything else, so an allow doubles as "this is a
	// shell call the runner would wrap".
	allowance := gate.AllowSandboxedCommands(gate.SandboxedOptions{
		Runner: s.sandboxService.Runner(),
		Policy: policy,
	}, nil)
	decision, err := allowance.Approve(ctx, tool, call)
	if err != nil || decision != rig.DecisionAllow {
		return false, false
	}
	// rig owns the mode semantics: read-only + a runner means commands run with
	// every write confined by the runner, and without a runner the policy itself
	// rejects them (the tool reports "shell commands are disabled in read-only
	// mode"). No product-side extra rule.
	return true, true
}
