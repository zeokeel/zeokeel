package contracts

import (
	cordis "github.com/zeokeel/zeokeel-kernel"

	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"
)

// SandboxStatus is the read model of the OS sandbox for settings and
// diagnostics. The OS sandbox is always on: it is a basic capability of the
// agent (commands must not run unconfined), so there is no user switch. When
// the host has no usable runner, Available is false and commands fail closed.
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
// It is always enabled: Runner returns the platform runner, or a fail-closed
// runner when this host has no usable one. The agent gate reads Available to
// decide whether shell-family tools may skip the approval prompt (the OS
// enforces their file effects), and the toolkit injects Runner into the
// command-executing tools.
type SandboxService interface {
	// Runner returns the runner to inject into tools. It is never nil for a
	// mounted service: an unusable host gets a fail-closed runner instead.
	Runner() rigsandbox.Runner
	// Available reports whether the host has a usable runner.
	Available() bool
	// Status returns the current read model.
	Status() SandboxStatus
}

// KeySandbox is provided by the sandbox module (zeokeel/sandbox) and consumed
// by the toolkit (runner injection) and the agent gate (shell auto-allow).
var KeySandbox = cordis.NewServiceKey[SandboxService]("zeokeel/sandbox")
