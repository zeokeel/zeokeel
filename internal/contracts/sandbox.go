package contracts

import (
	cordis "github.com/zeokeel/zeokeel-kernel"

	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"
)

// SandboxStatus is the read model of the OS sandbox for settings and
// diagnostics. The OS sandbox has no user switch: whenever the host has a
// usable runner, commands run inside it (writes confined to the policy).
//
// Personal-helper permission model (2026-11-26): commands never park on an
// approval card, so the OS runner is the ONLY thing that confines their file
// effects. On a host without one, `Available`/`Confinement` are the difference
// between "the fence is real" and "the model was told a rule it can ignore" —
// which is why they are a read model the client renders, not a log line.
type SandboxStatus struct {
	// Available reports whether this host has a usable runner.
	Available bool
	// Platform is runtime.GOOS of the process.
	Platform string
	// Reason explains why the sandbox is unavailable (empty when available).
	Reason string
	// Confinement is the honest state of the command fence: SandboxConfinementOS
	// when a runner reports real enforcement, SandboxConfinementNone when
	// shell commands run with the process's own permissions. It is the label a
	// settings/status surface must show; an empty value means "unknown" (no
	// sandbox module is assembled) and must never be rendered as confined.
	Confinement string
	// CommandsUnconfined is true exactly when Confinement is
	// SandboxConfinementNone: no OS-level write constraint applies to shell
	// commands. The write rule the model is given (workspace only) is then an
	// application-level rule it can ignore, not an enforced boundary.
	CommandsUnconfined bool
	// Note is the user-facing sentence for this state (empty when confined, so
	// a client shows nothing rather than "everything is normal" noise).
	Note string
}

// Confinement states of contracts.SandboxStatus.
const (
	// SandboxConfinementOS means shell commands run inside an OS sandbox whose
	// runner reports real enforcement.
	SandboxConfinementOS = "os"
	// SandboxConfinementNone means shell commands run without any OS-level
	// confinement (no usable runner on this host).
	SandboxConfinementNone = "none"
)

// SandboxService owns the OS sandbox runner and its status.
//
// Personal-client model: the platform runner is used when the machine has one,
// and Runner returns nil when it does not (the tool side then decides by policy:
// workspace-write runs commands under the application-level policy — best
// effort, never a startup failure — and read-only refuses shell commands). The
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

// SettingSandboxWritableDirs is the machine-level extra writable-directory list
// for the OS sandbox (newline or path-list separated). It exists for tools that
// keep state in a fixed home directory (for example ~/.dws): grant the exact
// subdirectory, never the home directory itself. Entries are validated by the
// SDK policy builder; invalid/sensitive paths are dropped there.
const SettingSandboxWritableDirs = "sandbox.writableDirs"

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
	// the current workspace. nil means no policy is available (no policy source,
	// or the group is closed), and the gate then asks.
	PolicyFor(root string) *rigsandbox.Policy
}

// KeyToolPolicy is provided by the toolkit module and consumed by the agent
// gate. It is optional: an assembly without the toolkit falls back to asking.
var KeyToolPolicy = cordis.NewServiceKey[ToolPolicy]("zeokeel/tool-policy")
