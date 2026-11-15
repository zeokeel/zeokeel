package contracts

import (
	"strconv"
	"strings"
)

// Capability settings: the limits the agent's delegation and the shell tool read
// at runtime, edited on the settings surface's 能力 / Capabilities page. They are
// optional rows: an absent, empty or malformed value keeps the module's own
// default, so a product without the settings page behaves exactly as before.
//
// One home for the keys and the ranges keeps the page (which writes and
// validates them), the agent module (which reads them per delegation) and the
// toolkit (which reads the shell limits at activation) from drifting apart.
const (
	// SettingAgentSubagentMaxConcurrent caps the live children of one
	// synchronous subagent delegation (1–8; unset keeps the agent module's
	// default). The agent reads it on every delegation, so a change applies to
	// the next delegation without a restart.
	SettingAgentSubagentMaxConcurrent = "agent.subagentMaxConcurrent"
	// SettingAgentTeamMaxConcurrent caps a team member's live subagents (1–8;
	// unset keeps the agent module's default). Read on every team member
	// start.
	SettingAgentTeamMaxConcurrent = "agent.teamMaxConcurrent"
	// SettingToolkitShellTimeoutSeconds bounds one shell tool execution in
	// seconds (5–600; unset keeps the toolkit Config's timeout, 30s by
	// default). The toolkit resolves it at activation, so a change applies
	// after a restart.
	SettingToolkitShellTimeoutSeconds = "toolkit.shellTimeoutSeconds"
	// SettingToolkitShellMaxOutputKiB caps one shell tool result in KiB
	// (0 = the tool default, 1–4096 otherwise; unset keeps the toolkit
	// Config's maxOutputBytes). The toolkit resolves it at activation, so a
	// change applies after a restart.
	SettingToolkitShellMaxOutputKiB = "toolkit.shellMaxOutputKiB"
)

// Bounds of the capability settings. The settings page validates against them
// and the modules clamp any stored value that falls outside (hand-edited rows).
const (
	// CapabilityMinConcurrency is the smallest delegation concurrency.
	CapabilityMinConcurrency = 1
	// CapabilityMaxConcurrency is the largest delegation concurrency.
	CapabilityMaxConcurrency = 8
	// CapabilityMinShellTimeoutSeconds is the shortest shell timeout.
	CapabilityMinShellTimeoutSeconds = 5
	// CapabilityMaxShellTimeoutSeconds is the longest shell timeout.
	CapabilityMaxShellTimeoutSeconds = 600
	// CapabilityShellOutputDefaultKiB is the explicit "tool default" output cap:
	// 0 leaves the rig/sandbox default in place.
	CapabilityShellOutputDefaultKiB = 0
	// CapabilityMaxShellOutputKiB is the largest configurable shell output cap
	// (4 MiB), so a hand-edited row cannot ask for an unbounded buffer.
	CapabilityMaxShellOutputKiB = 4096
)

// NormalizeCapabilityConcurrency parses a stored delegation concurrency limit:
// a value inside [CapabilityMinConcurrency, CapabilityMaxConcurrency] wins;
// empty, malformed or out-of-range values return fallback.
func NormalizeCapabilityConcurrency(raw string, fallback int) int {
	return normalizeIntInRange(raw, CapabilityMinConcurrency, CapabilityMaxConcurrency, fallback)
}

// NormalizeShellTimeoutSeconds parses a stored shell timeout: a value inside
// [CapabilityMinShellTimeoutSeconds, CapabilityMaxShellTimeoutSeconds] wins;
// empty, malformed or out-of-range values return fallback.
func NormalizeShellTimeoutSeconds(raw string, fallback int) int {
	return normalizeIntInRange(raw, CapabilityMinShellTimeoutSeconds, CapabilityMaxShellTimeoutSeconds, fallback)
}

// NormalizeShellMaxOutputKiB parses a stored shell output cap: 0 (the explicit
// tool default) and 1..CapabilityMaxShellOutputKiB win; empty, malformed or
// out-of-range values return fallback.
func NormalizeShellMaxOutputKiB(raw string, fallback int) int {
	return normalizeIntInRange(raw, CapabilityShellOutputDefaultKiB, CapabilityMaxShellOutputKiB, fallback)
}

// normalizeIntInRange parses raw and keeps it only when it falls inside
// [lo, hi]; everything else returns fallback.
func normalizeIntInRange(raw string, lo, hi, fallback int) int {
	text := strings.TrimSpace(raw)
	if text == "" {
		return fallback
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < lo || value > hi {
		return fallback
	}
	return value
}
