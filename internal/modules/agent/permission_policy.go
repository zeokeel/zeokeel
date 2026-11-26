package agent

// The effective permission tier, as one value.
//
// The model-facing approval sentence used to be derived from the approval mode
// ALONE, and the approval mode is only one of the inputs the gate reads: a
// session-level read-only override (`session.permission.set`) refuses shell
// commands and every MCP tool outright, while the sentence still said those
// families "run without approval". The model then followed the sentence, called
// bash, and was denied — repeatedly, because nothing in the prompt explained
// that a refusal (not a prompt) is what happens (2026-11-26 audit, 高).
//
// This file makes the tier explicit and gives the prompt and the gate ONE shared
// vocabulary:
//
//   - approvalPolicy is the whole tier: the global preset, the session override,
//     whether an approvals service exists at all, whether the assembly has the
//     sandbox module, and the host's command confinement.
//   - overridePermission is the session-override layer's judgement for one tool,
//     and gate() calls it too — so "what the sentence promises" and "what the
//     gate decides" cannot drift apart for that layer.
//   - approvalPolicyClaims turns the tier into the small table of claims the
//     sentence is rendered from, each tied to a representative tool name, so a
//     test can ask the REAL gate the same question and compare
//     (TestApprovalPolicyClaimsMatchTheGate).
//
// Layers the claims deliberately do not model: role/preset allowlists, task
// bindings, plan/minimal/creator modes and the remembered "always allow" set.
// Those are per-session facts that narrow or widen a call for reasons the policy
// sentence never mentions; the claims describe the default conversation the
// sentence is written for, which is exactly what the gate sees when none of them
// apply.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// approvalPolicy is the effective permission tier of one run.
type approvalPolicy struct {
	// Mode is the GLOBAL approval preset stored by the approvals service
	// (agent.permission). It never changes for a session override.
	Mode string
	// SessionOverride is the session-level override (session.permission.set),
	// "" when the session has none. It is a hard restriction, not a hint: it
	// refuses instead of asking.
	SessionOverride string
	// ApprovalsAvailable is false when the assembly has no approvals service:
	// then no prompt can be answered, and every call that would ask is refused.
	ApprovalsAvailable bool
	// DenyAll is the headless "never" assembly policy: an explicit deny-all that
	// every exemption still honours.
	DenyAll bool
	// SandboxAssembled reports whether the sandbox module is in this assembly. It
	// is what makes the command family exempt from the approval prompt; without
	// it, shell calls keep the ordinary approval flow.
	SandboxAssembled bool
	// CommandPolicyAvailable reports whether the assembly has the command policy
	// the shell calls are dispatched under (toolkit policy source). The exemption
	// needs BOTH halves: shellCommandExempt asks the policy to judge the call, so
	// a sandbox module with no policy source leaves the command family on the
	// ordinary approval flow. Without this input the claims (and therefore the
	// sentence) called commands exempt while the gate asked the user
	// (2026-11-26 audit, 高: the third fork).
	CommandPolicyAvailable bool
	// Confinement is contracts.SandboxConfinementNone when this host has no OS
	// sandbox runner (commands run unconfined); "" when no sandbox module is
	// assembled.
	Confinement string
}

// normalized maps every accepted spelling onto the three presets, so a caller
// may pass raw stored values.
func (p approvalPolicy) normalized() approvalPolicy {
	p.Mode = normalizeApprovalMode(p.Mode)
	p.SessionOverride = strings.TrimSpace(p.SessionOverride)
	if p.SessionOverride != "" {
		p.SessionOverride = normalizeApprovalMode(p.SessionOverride)
	}
	return p
}

// toolPermission is the per-session override layer's judgement for one tool.
type toolPermission int

const (
	// permissionFallsThrough means the override has no opinion: the call
	// continues down the gate (sandbox exemption, MCP family, interaction
	// tools, global policy).
	permissionFallsThrough toolPermission = iota
	// permissionAllowed means the override auto-allows the call (full preset, or
	// a member of the tier's auto-allowed set).
	permissionAllowed
	// permissionRefused means the override refuses the call outright. No
	// approval dialog is offered: a read-only session that popped a card for
	// every write would not be read-only.
	permissionRefused
)

// overridePermission is the session-level override layer for one tool name. It
// is the single source of truth for that layer: gate() branches on it and the
// model-facing sentence is rendered from claims that call it. The session-state
// carve-outs the gate applies on top of a refusal (interaction tools and tools
// the user already chose to always allow in this session) are NOT part of this
// function; they are session facts, and the sentence mentions them separately.
func overridePermission(override, name string) toolPermission {
	name = strings.TrimSpace(name)
	switch normalizeApprovalMode(override) {
	case contracts.PermissionFull:
		return permissionAllowed
	case contracts.PermissionReadOnly:
		if contracts.ReadOnlyAutoAllowed(name) {
			return permissionAllowed
		}
		return permissionRefused
	case contracts.PermissionWorkspace:
		if contracts.WorkspaceAutoAllowed(name) {
			return permissionAllowed
		}
	}
	return permissionFallsThrough
}

// toolDisposition is what the effective tier does with one tool family, in the
// vocabulary the model-facing sentence uses.
type toolDisposition string

const (
	// dispositionAllowed: the call runs with no approval prompt.
	dispositionAllowed toolDisposition = "allowed"
	// dispositionRefused: the call is refused outright. It does NOT ask the user,
	// so retrying it is pointless — the sentence has to say so, because that is
	// the difference the audit found missing.
	dispositionRefused toolDisposition = "refused"
	// dispositionAsks: an approval card may appear.
	dispositionAsks toolDisposition = "asks"
)

// permissionClaim is one claim the approval sentence makes about one tool family,
// tied to a representative tool name so a test can put the same tool in front of
// the real gate and compare the answers.
type permissionClaim struct {
	// Family is the phrase the sentence uses ("shell commands (bash/pwsh)").
	Family string
	// Tool is the representative tool name the gate is asked about.
	Tool string
	// Disposition is what the tier does with that family.
	Disposition toolDisposition
}

// representative tools: one per family the sentence can name. They are stable
// names from the product's own surface, not SDK internals.
const (
	claimToolReadOnly    = "read_file"
	claimToolWrite       = "write_file"
	claimToolShell       = "bash"
	claimToolMCP         = contracts.MCPToolPrefix + "probe__call"
	claimToolNetwork     = "web_fetch"
	claimToolSettings    = "settings_set"
	claimToolInteraction = "ask_user"
)

// approvalPolicyClaims renders the tier into the claims the sentence is built
// from.
func approvalPolicyClaims(policy approvalPolicy) []permissionClaim {
	p := policy.normalized()
	return []permissionClaim{
		{Family: "read-only and informational tools", Tool: claimToolReadOnly, Disposition: policyClaim(p, claimToolReadOnly)},
		{Family: "file writes", Tool: claimToolWrite, Disposition: policyClaim(p, claimToolWrite)},
		{Family: "shell commands (bash/pwsh)", Tool: claimToolShell, Disposition: policyClaim(p, claimToolShell)},
		{Family: "MCP tools", Tool: claimToolMCP, Disposition: policyClaim(p, claimToolMCP)},
		{Family: "network tools", Tool: claimToolNetwork, Disposition: policyClaim(p, claimToolNetwork)},
		{Family: "settings writes", Tool: claimToolSettings, Disposition: policyClaim(p, claimToolSettings)},
		{Family: "interaction tools", Tool: claimToolInteraction, Disposition: policyClaim(p, claimToolInteraction)},
	}
}

// policyClaim mirrors gate()'s branch order for one tool, EXCLUDING the layers
// approvalPolicyClaims documents as out of scope (role/task binding, runtime
// modes, remembered allows). The override layer is not mirrored — it is called,
// through overridePermission, so the two cannot disagree.
//
// The mirror is deliberately literal: plan_mode.go's order is the contract, and
// a test drives the real gate over the same representatives.
func policyClaim(policy approvalPolicy, name string) toolDisposition {
	p := policy.normalized()
	name = strings.TrimSpace(name)

	// 1. Per-session override (the layer the audit found the prompt lying about).
	if p.SessionOverride != "" {
		switch overridePermission(p.SessionOverride, name) {
		case permissionAllowed:
			return dispositionAllowed
		case permissionRefused:
			// The gate lets interaction tools and remembered tools through a
			// read-only refusal; neither applies to the representatives except
			// ask_user, which is handled here so the claim stays true.
			if interactionTools[name] {
				if p.DenyAll {
					return dispositionRefused
				}
				return dispositionAllowed
			}
			return dispositionRefused
		}
	}

	// 2. Command family: exempt while the sandbox module is assembled AND the
	//    command policy it judges calls under exists — shellCommandExempt needs
	//    both (sandbox.go), so an assembly with the module but no policy source
	//    keeps the ordinary approval flow. A deny-all assembly still refuses it.
	//    Without an approvals service the exemption's veto applies UNLESS the SDK
	//    certifies the call is confined (a confined command never needed an
	//    approval at all) — which is why the tier carries the host's confinement
	//    instead of only the module flag.
	if inSandboxShellFamilyName(name) && p.SandboxAssembled && p.CommandPolicyAvailable {
		if p.DenyAll {
			return dispositionRefused
		}
		if p.ApprovalsAvailable || p.Confinement == contracts.SandboxConfinementOS {
			return dispositionAllowed
		}
		return dispositionRefused
	}

	// 3. MCP family: exempt by prefix, same vetoes.
	if contracts.IsMCPTool(name) {
		if p.DenyAll || !p.ApprovalsAvailable {
			return dispositionRefused
		}
		return dispositionAllowed
	}

	// 4. Interaction tools carry their own overlay instead of an approval card.
	if interactionTools[name] {
		if p.DenyAll {
			return dispositionRefused
		}
		return dispositionAllowed
	}

	// 5. A deny-all assembly policy ("never"): every remaining call is refused
	//    without a card, exactly like the branches above.
	if p.DenyAll {
		return dispositionRefused
	}

	// 6. Everything below needs the approvals service: it is the component that
	//    knows the global preset and answers the card. Without it gate() fails
	//    closed, so a preset's auto-allowed set is not consulted either.
	if !p.ApprovalsAvailable {
		return dispositionRefused
	}

	// 7. The global preset. These sets are the approvals service's own
	//    auto-allow table (contracts.WorkspaceAutoAllowedTools, shared with the
	//    gate); a member runs with no card, everything else reaches the dialog.
	switch p.Mode {
	case contracts.PermissionFull:
		return dispositionAllowed
	case contracts.PermissionReadOnly:
		if contracts.ReadOnlyAutoAllowed(name) {
			return dispositionAllowed
		}
	case contracts.PermissionWorkspace:
		if contracts.WorkspaceAutoAllowed(name) {
			return dispositionAllowed
		}
	}

	// 8. The approval dialog.
	return dispositionAsks
}

// inSandboxShellFamilyName reports whether name is a shell tool. It is the
// name-only half of inSandboxShellFamily (sandbox.go), which needs a rig.Tool;
// the family is pinned in both places by sandboxShellTools.
func inSandboxShellFamilyName(name string) bool {
	name = strings.TrimSpace(name)
	for _, shell := range sandboxShellTools {
		if name == shell {
			return true
		}
	}
	return false
}

// familiesWith lists the claim families that carry one disposition, in claim
// order ("a, b and c").
func familiesWith(claims []permissionClaim, want toolDisposition) []string {
	out := make([]string, 0, len(claims))
	for _, claim := range claims {
		if claim.Disposition == want {
			out = append(out, claim.Family)
		}
	}
	return out
}
