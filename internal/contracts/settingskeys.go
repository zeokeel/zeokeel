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
	// SettingAgentApprovalTimeoutSeconds bounds how long an approval request
	// from an AUTOMATION-origin run may wait before it is automatically denied
	// (recorded and notified). 0 or unset disables the timeout — the default,
	// so interactive approvals keep waiting exactly as before. There is NO
	// run-level time fallback: loop protection and every tool-loop budget were
	// removed (v0.2.50 user ruling — tools run freely), so an unattended
	// automation run that must not park forever should set this timeout.
	// Personal-scale ruling (2026-11 audit RG-04): unattended runs get an
	// opt-in per-approval deny instead of a whole-run cancel, humans get no
	// added friction.
	SettingAgentApprovalTimeoutSeconds = "agent.approvalTimeoutSeconds"
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
	// SettingToolkitDeniedGlobs is the opt-in file-hiding row (2026-11 audit
	// RG-10): a JSON string list (e.g. ["**/.env","**/*.pem"]) that overrides
	// the assembly's toolkit Config.DeniedGlobs. Absent/blank keeps the
	// assembly value; an explicit empty list opts back out. The default is
	// empty (personal-scale opt-in). Disclosure: the globs hide files from the
	// FILE tools only — the shell bypass remains, so this is defense in depth,
	// not a security boundary. The toolkit resolves it once at activation (a
	// restart applies a change); the settings Data & privacy page edits it.
	SettingToolkitDeniedGlobs = "toolkit.deniedGlobs"
)

// External-CLI link settings: where the vendor CLIs live. They are not limits,
// so they get their own block. The agent reads them on every run (it renders
// them into the runtime context) and the clilink module reads them on every
// Refresh, so a change applies without a restart.
const (
	// SettingCLIDingtalkPath overrides where the DingTalk CLI (`dws`) lives: an
	// absolute path to the executable. Unset (the default) means the clilink
	// module falls back to a PATH lookup, and "neither" means not-installed —
	// a normal state, never an error, and never a boot blocker. The path is what
	// the model is told (see clilink.go), because a service process
	// started from a desktop shell routinely does NOT inherit the user's nvm /
	// npm-bin PATH, so "just run dws" would fail there.
	SettingCLIDingtalkPath = "cli.dingtalk.path"
	// SettingCLIFeishuPath is the Feishu CLI (`lark-cli`) equivalent of
	// SettingCLIDingtalkPath.
	SettingCLIFeishuPath = "cli.feishu.path"
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
	// CapabilityMaxApprovalTimeoutSeconds is the largest configurable
	// automation approval timeout (24 h); a hand-edited row beyond it falls
	// back to the default (0 = disabled).
	CapabilityMaxApprovalTimeoutSeconds = 86400
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

// NormalizeApprovalTimeoutSeconds parses the stored automation approval
// timeout: 0 (disabled, the default) and 1..CapabilityMaxApprovalTimeoutSeconds
// win; empty, malformed or out-of-range values disable the timeout (0), so a
// hand-edited row can never make interactive approvals time out by accident.
func NormalizeApprovalTimeoutSeconds(raw string) int {
	return normalizeIntInRange(raw, 0, CapabilityMaxApprovalTimeoutSeconds, 0)
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
