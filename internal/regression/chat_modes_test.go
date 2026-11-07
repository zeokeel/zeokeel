package regression

// Adversarial audit: the chat-mode tool gate (the old shell's standard / minimal /
// creator presets, combined with the plan agent mode). The gate is table-driven:
// standard keeps the approvals flow, minimal allows only read-only exploration,
// creator hides the coding tools, plan mode keeps priority over every chat mode.
// The policy is read on every tool call, so switching modes restores the policy
// for the following calls.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// chatModeAllowed is the expected allowlist of one chat mode (tool names come
// from the product contract; the test pins them as a behavior lock).
var chatModeAllowed = map[string][]string{
	"minimal": {
		"read_file", "list_dir", "glob", "grep", "memory", "skill", "skills",
		"web_fetch", "ask_user", "present", "goal", "plan_review", "exit_plan_mode",
		"team_task_list", "team_task_get",
	},
	"creator": {
		"ask_user", "present", "goal", "plan_review", "exit_plan_mode",
		"memory", "skill", "skills",
	},
	"plan": {
		"read_file", "list_dir", "glob", "grep", "ask_user", "present",
		"plan_review", "exit_plan_mode", "goal", "team_task_list", "team_task_get",
		"memory", "skill", "skills", "web_fetch",
	},
}

// chatModeDenied is the expected deny set of one chat mode.
var chatModeDenied = map[string][]string{
	"minimal": {"write_file", "bash", "terminal", "spawn_teammate", "workflow", "mcp__srv__echo"},
	"creator": {"read_file", "grep", "write_file", "bash", "terminal", "spawn_teammate"},
	"plan":    {"write_file", "bash", "terminal", "spawn_teammate", "workflow", "mcp__srv__echo"},
}

// setChatMode writes one agent/chat mode combination.
func setChatMode(t *testing.T, harness *planHarness, agentMode, chatMode string) {
	t.Helper()
	if err := harness.h.settings.Set(contracts.SettingAgentMode, agentMode); err != nil {
		t.Fatalf("set agent.mode=%q: %v", agentMode, err)
	}
	if err := harness.h.settings.Set(contracts.SettingChatMode, chatMode); err != nil {
		t.Fatalf("set chat.mode=%q: %v", chatMode, err)
	}
}

// runModeTool runs one tool call in a mode that bypasses approvals (plan/minimal/
// creator answer at the gate) and returns the finished session.
func runModeTool(t *testing.T, harness *planHarness, sessionID, toolName string) contracts.ChatSession {
	t.Helper()
	return harness.runImmediate(t, sessionID, toolName)
}

// TestChatModesGateTable is the four-mode allow/deny table: every expected tool
// is allowed without queueing, every other tool is denied fail-closed and never
// executed, and no approval is queued in the bypassing modes.
func TestChatModesGateTable(t *testing.T) {
	for _, mode := range []string{"minimal", "creator", "plan"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			harness := startPlanHarness(t, "")
			if mode == "plan" {
				setChatMode(t, harness, contracts.AgentModePlan, contracts.ChatModeStandard)
			} else {
				setChatMode(t, harness, contracts.AgentModeNormal, mode)
			}
			sessionID := harness.newChatSession(t, "gate-"+mode)

			for _, name := range chatModeAllowed[mode] {
				before := harness.count(name)
				session := runModeTool(t, harness, sessionID, name)
				if got := harness.count(name) - before; got != 1 {
					t.Fatalf("mode %s: allowed %q executed %d times, want 1", mode, name, got)
				}
				if status, _ := lastToolStatus(t, session); status != "ok" {
					t.Fatalf("mode %s: allowed %q tool status = %q, want ok", mode, name, status)
				}
			}
			for _, name := range chatModeDenied[mode] {
				before := harness.count(name)
				session := runModeTool(t, harness, sessionID, name)
				if got := harness.count(name) - before; got != 0 {
					t.Fatalf("mode %s: denied %q executed %d times, want 0", mode, name, got)
				}
				status, summary := lastToolStatus(t, session)
				if status != "error" || !strings.Contains(summary, "denied") {
					t.Fatalf("mode %s: denied %q status/summary = %q/%q, want error/denied",
						mode, name, status, summary)
				}
			}
			if pending := harness.approvals().Pending(); len(pending) != 0 {
				t.Fatalf("mode %s queued %d approvals, want none (gate bypasses approvals)", mode, len(pending))
			}
		})
	}
}

// TestChatModeStandardKeepsApprovals covers standard mode: the same mutating and
// read tools go through the approvals queue (allow executes, deny does not), in
// contrast with the bypassing presets.
func TestChatModeStandardKeepsApprovals(t *testing.T) {
	harness := startPlanHarness(t, "")
	setChatMode(t, harness, contracts.AgentModeNormal, contracts.ChatModeStandard)
	sessionID := harness.newChatSession(t, "standard")

	harness.runWithApproval(t, sessionID, "write_file", contracts.ApprovalAllow)
	if got := harness.count("write_file"); got != 1 {
		t.Fatalf("standard write_file after allow = %d, want 1", got)
	}
	harness.runWithApproval(t, sessionID, "read_file", contracts.ApprovalDeny)
	if got := harness.count("read_file"); got != 0 {
		t.Fatalf("standard read_file after deny = %d, want 0 (no bypass)", got)
	}
}

// TestChatModeNormalizationBehavior covers stored chat.mode normalization: case
// and whitespace are folded, unknown/empty values fall back to standard (the
// approvals flow) instead of silently enabling a restrictive preset.
func TestChatModeNormalizationBehavior(t *testing.T) {
	harness := startPlanHarness(t, "")
	sessionID := harness.newChatSession(t, "normalize")

	setChatMode(t, harness, contracts.AgentModeNormal, " MINIMAL ")
	before := harness.count("write_file")
	runModeTool(t, harness, sessionID, "write_file")
	if got := harness.count("write_file") - before; got != 0 {
		t.Fatalf("chat.mode \" MINIMAL \" executed write_file %d times, want denied", got)
	}

	setChatMode(t, harness, contracts.AgentModeNormal, "Creator")
	before = harness.count("read_file")
	runModeTool(t, harness, sessionID, "read_file")
	if got := harness.count("read_file") - before; got != 0 {
		t.Fatalf("chat.mode \"Creator\" executed read_file %d times, want denied", got)
	}

	for _, value := range []string{"", "yolo", "standard", "MINIMAL_MODE"} {
		setChatMode(t, harness, contracts.AgentModeNormal, value)
		before = harness.count("write_file")
		harness.runWithApproval(t, sessionID, "write_file", contracts.ApprovalDeny)
		if got := harness.count("write_file") - before; got != 0 {
			t.Fatalf("chat.mode %q executed write_file %d times after a denied approval, want 0", value, got)
		}
	}
}

// TestPlanModePrecedenceOverChatModes covers the precedence rule: agent.mode=plan
// keeps the plan allowlist even when chat.mode is creator/minimal, so a tool the
// plan list allows (team_task_list, read_file) is still allowed and a tool it
// denies (write_file) is still rejected.
func TestPlanModePrecedenceOverChatModes(t *testing.T) {
	for _, chatMode := range []string{contracts.ChatModeCreator, contracts.ChatModeMinimal} {
		harness := startPlanHarness(t, "")
		setChatMode(t, harness, contracts.AgentModePlan, chatMode)
		sessionID := harness.newChatSession(t, "precedence-"+chatMode)
		for _, name := range []string{"team_task_list", "read_file"} {
			before := harness.count(name)
			runModeTool(t, harness, sessionID, name)
			if got := harness.count(name) - before; got != 1 {
				t.Fatalf("plan+%s: plan-allowed %q executed %d times, want 1", chatMode, name, got)
			}
		}
		before := harness.count("write_file")
		runModeTool(t, harness, sessionID, "write_file")
		if got := harness.count("write_file") - before; got != 0 {
			t.Fatalf("plan+%s: write_file executed %d times, want denied", chatMode, got)
		}
	}
}
