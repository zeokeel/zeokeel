package contracts

import (
	cordis "github.com/zeokeel/zeokeel-kernel"

	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"
)

// SandboxStatus is the read model of the OS sandbox for settings and
// diagnostics. The OS sandbox is always on: it is a basic capability of the
// agent (commands must not run unconfined), so there is no user switch. When
// the host has no usable runner, Available is false and Runner is nil: commands
// then run with the host's own permissions under the application-level policy
// (personal-client best effort).
type SandboxStatus struct {
	// Available reports whether this host has a usable runner.
	Available bool
	// Platform is runtime.GOOS of the process.
	Platform string
	// Reason explains why the sandbox is unavailable (empty when available).
	Reason string
}

// SandboxService owns the OS sandbox runner and its status.
//
// Personal-client model: the platform runner is used when the machine has one,
// and Runner returns nil when it does not (commands then run under the
// application-level policy only — best effort, never a startup failure). The
// agent gate hands Runner and the toolkit's live Policy to the SDK's
// gate.AllowSandboxedCommands to decide whether a shell call may skip the
// approval prompt; Available/Status are the settings-surface read model. The
// toolkit injects Runner into the command-executing tools.
type SandboxService interface {
	// Runner returns the runner to inject into tools, or nil when this host has
	// no usable OS sandbox (the tools then run under the application-level
	// policy only — personal-client best effort, never a startup failure).
	Runner() rigsandbox.Runner
	// Available reports whether the host has a usable runner.
	Available() bool
	// Status returns the current read model.
	Status() SandboxStatus
}

// KeySandbox is provided by the sandbox module (zeokeel/sandbox) and consumed
// by the toolkit (runner injection) and the agent gate (shell auto-allow).
var KeySandbox = cordis.NewServiceKey[SandboxService]("zeokeel/sandbox")

// ToolPolicy exposes the sandbox policy the toolkit built for one workspace
// root. The agent gate needs it to ask the SDK whether a shell call is really
// confined (gate.AllowSandboxedCommands needs both the runner and the policy
// that will dispatch the call: a danger-full-access policy has no fence, so
// nothing may be skipped). The toolkit owns policies (one per
// root+mode+runner build), so this interface is the read-only bridge to them.
type ToolPolicy interface {
	// PolicyFor returns the policy of the tool build that dispatches calls for
	// root — building/reusing that retained build when needed. Empty root means
	// the current workspace. nil means no policy is available (nothing was
	// built), and the gate then asks.
	PolicyFor(root string) *rigsandbox.Policy
}

// KeyToolPolicy is provided by the toolkit module and consumed by the agent
// gate. It is optional: an assembly without the toolkit falls back to asking.
var KeyToolPolicy = cordis.NewServiceKey[ToolPolicy]("zeokeel/tool-policy")
