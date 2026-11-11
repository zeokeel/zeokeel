package regression

// Adversarial audit: the plan-mode tool gate (settings agent.mode="plan"). The
// gate is table-driven and fail-closed: whitelisted read-only/planning tools are
// allowed without an approval, everything else is denied and never executed.
// Mode values normalize case/whitespace only to "plan"; anything else falls back
// to the normal approvals path, and switching back restores the approvals queue.

import (
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
)

// planWhitelist is the plan-mode allowed set the audit expects (read-only
// exploration, interaction boxes, planning tools, task-board reads, web read).
var planWhitelist = []string{
	"read_file", "list_dir", "glob", "grep",
	"ask_user", "present", "plan_review", "exit_plan_mode", "goal",
	"team_task_list", "team_task_get", "memory", "skill", "skills", "web_fetch",
}

// planDeniedTools are representative mutating/exec/orchestration tools the plan
// mode must reject.
var planDeniedTools = []string{
	"write_file", "edit", "bash", "terminal", "spawn_teammate", "team_spawn",
	"workflow", "ralph", "mcp__srv__echo",
}

// planScript drives the scripted model: the first request of a turn calls
// script.current, and once the conversation carries a tool result it answers
// with text and stops.
type planScript struct {
	mu      sync.Mutex
	current string
}

func (p *planScript) set(name string) {
	p.mu.Lock()
	p.current = name
	p.mu.Unlock()
}

func (p *planScript) respond(body []byte) (int, string) {
	if lastRequestRole(body) == "tool" {
		return 0, sseAnswer("done")
	}
	p.mu.Lock()
	name := p.current
	p.mu.Unlock()
	return 0, sseToolCall(name, "{}")
}

// planHarness bundles the agent chain with plan-gate counters.
type planHarness struct {
	*agentHarness
	script   *planScript
	counters map[string]*counter
}

// newPlanHarnessFromModel activates the agent chain with every probed tool
// seeded as a counting fake tool, approvals in ask mode, and the agent.mode.
func newPlanHarnessFromModel(t *testing.T, mode string, model *fakeModel) *planHarness {
	t.Helper()
	counters := make(map[string]*counter)
	seeds := make([]rig.Tool, 0, len(planWhitelist)+len(planDeniedTools))
	for _, name := range append(append([]string(nil), planWhitelist...), planDeniedTools...) {
		if _, ok := counters[name]; ok {
			continue
		}
		counters[name] = new(counter)
		seeds = append(seeds, fakeRigTool{name: name, calls: counters[name]})
	}
	harness := &planHarness{counters: counters, script: &planScript{}}
	harness.agentHarness = startAgentWithApprovals(t, agent.Config{}, model, approvals.ModeAsk, seeds...)
	if mode != "" {
		if err := harness.h.settings.Set(contracts.SettingAgentMode, mode); err != nil {
			t.Fatalf("set %s: %v", contracts.SettingAgentMode, err)
		}
	}
	return harness
}

// startPlanHarness starts the plan-gate harness with the standard scripted model.
func startPlanHarness(t *testing.T, mode string) *planHarness {
	t.Helper()
	script := &planScript{}
	model := newFakeModel(t, script.respond)
	harness := newPlanHarnessFromModel(t, mode, model)
	harness.script = script
	return harness
}

// count returns the executions of one tool.
func (p *planHarness) count(name string) int64 {
	if calls, ok := p.counters[name]; ok {
		return calls.load()
	}
	return -1
}

// approvals reads the approvals service of the harness.
func (p *planHarness) approvals() contracts.Approvals {
	return requireService(p.h.t, p.h.ctx, contracts.KeyApprovals)
}

// runImmediate runs one tool-calling turn in plan mode (the gate answers at
// once, so no approval is queued) and returns the finished session.
func (p *planHarness) runImmediate(t *testing.T, sessionID, toolName string) contracts.ChatSession {
	t.Helper()
	p.script.set(toolName)
	if err := p.chat.Send(sessionID, "call "+toolName); err != nil {
		t.Fatalf("Send(%s): %v", toolName, err)
	}
	session := p.waitIdle(t, sessionID)
	if pending := p.approvals().Pending(); len(pending) != 0 {
		t.Fatalf("plan-mode call %q queued %d approval requests, want none", toolName, len(pending))
	}
	return session
}

// runWithApproval runs one tool-calling turn in normal mode: the run blocks on
// the approvals queue, the test resolves it and the session finishes.
func (p *planHarness) runWithApproval(t *testing.T, sessionID, toolName string,
	decision contracts.ApprovalDecision) contracts.ChatSession {
	t.Helper()
	p.script.set(toolName)
	if err := p.chat.Send(sessionID, "call "+toolName); err != nil {
		t.Fatalf("Send(%s): %v", toolName, err)
	}
	approvalsService := p.approvals()
	waitFor(t, "pending approval for "+toolName, func() bool { return len(approvalsService.Pending()) > 0 })
	if err := approvalsService.Resolve(approvalsService.Pending()[0].ID, decision); err != nil {
		t.Fatalf("Resolve(%s): %v", toolName, err)
	}
	return p.waitIdle(t, sessionID)
}

// lastToolStatus returns the status/summary of the last recorded tool call.
func lastToolStatus(t *testing.T, session contracts.ChatSession) (status, summary string) {
	t.Helper()
	for index := len(session.Messages) - 1; index >= 0; index-- {
		message := session.Messages[index]
		if len(message.Tools) == 0 {
			continue
		}
		last := message.Tools[len(message.Tools)-1]
		return last.Status, last.Summary
	}
	return "", ""
}

// TestPlanModeWhitelistTable is the table-driven gate negative: every whitelisted
// tool is allowed without an approval and executed; every mutating tool is
// denied with an error status and never executed.
func TestPlanModeWhitelistTable(t *testing.T) {
	harness := startPlanHarness(t, contracts.AgentModePlan)
	sessionID := harness.newChatSession(t, "plan")

	for _, name := range planWhitelist {
		before := harness.count(name)
		session := harness.runImmediate(t, sessionID, name)
		if got := harness.count(name) - before; got != 1 {
			t.Fatalf("whitelisted %q executed %d times, want 1", name, got)
		}
		if status, _ := lastToolStatus(t, session); status != "ok" {
			t.Fatalf("whitelisted %q tool status = %q, want ok", name, status)
		}
	}

	for _, name := range planDeniedTools {
		before := harness.count(name)
		session := harness.runImmediate(t, sessionID, name)
		if got := harness.count(name) - before; got != 0 {
			t.Fatalf("plan-mode tool %q executed %d times, want 0 (denied)", name, got)
		}
		status, summary := lastToolStatus(t, session)
		if status != "error" {
			t.Fatalf("plan-mode tool %q status = %q, want error", name, status)
		}
		// Plan mode now also filters the model-visible tool schema (FR-16), so a
		// call to a filtered tool is rejected by the loop as unknown before the
		// gate sees it; the gate itself still denies fail-closed (covered by the
		// agent package gate tests).
		if !strings.Contains(summary, "denied") && !strings.Contains(summary, "not found") {
			t.Fatalf("plan-mode tool %q summary = %q, want a denial reason", name, summary)
		}
	}
}

// TestPlanModeDeniesMultipleMutatingCallsPerTurn covers a model that asks for
// several mutating tools in one assistant turn: all are denied, none executes.
func TestPlanModeDeniesMultipleMutatingCallsPerTurn(t *testing.T) {
	model := newFakeModel(t, func(body []byte) (int, string) {
		if lastRequestRole(body) == "tool" {
			return 0, sseAnswer("done")
		}
		return 0, sseToolCalls("write_file", "bash", "spawn_teammate")
	})
	harness := newPlanHarnessFromModel(t, contracts.AgentModePlan, model)
	sessionID := harness.newChatSession(t, "multi-deny")
	if err := harness.chat.Send(sessionID, "multiple tools"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := harness.waitIdle(t, sessionID)
	for _, name := range []string{"write_file", "bash", "spawn_teammate"} {
		if got := harness.count(name); got != 0 {
			t.Fatalf("multi-call turn executed %q %d times, want 0", name, got)
		}
	}
	statuses := 0
	for _, message := range session.Messages {
		for _, tool := range message.Tools {
			statuses++
			if tool.Status != "error" {
				t.Fatalf("multi-call tool %q status = %q, want error", tool.Name, tool.Status)
			}
		}
	}
	if statuses < 3 {
		t.Fatalf("recorded %d tool calls, want at least 3", statuses)
	}
	if pending := harness.approvals().Pending(); len(pending) != 0 {
		t.Fatalf("multi-call plan turn queued %d approvals, want none", len(pending))
	}
}

// TestPlanModeNormalization covers mode-value normalization: only plan-like
// spellings (case/whitespace insensitive) enable plan mode; normal, empty and
// unknown values use the approvals queue.
func TestPlanModeNormalization(t *testing.T) {
	harness := startPlanHarness(t, contracts.AgentModePlan)
	sessionID := harness.newChatSession(t, "normalize")

	for _, value := range []string{"plan", "PLAN", " Plan ", "\tplan\n"} {
		if err := harness.h.settings.Set(contracts.SettingAgentMode, value); err != nil {
			t.Fatalf("set mode %q: %v", value, err)
		}
		before := harness.count("write_file")
		harness.runImmediate(t, sessionID, "write_file")
		if got := harness.count("write_file") - before; got != 0 {
			t.Fatalf("mode %q: write_file executed %d times, want denied", value, got)
		}
		if pending := harness.approvals().Pending(); len(pending) != 0 {
			t.Fatalf("mode %q: pending approvals = %d, want 0", value, len(pending))
		}
	}

	for _, value := range []string{"normal", "NORMAL", " Normal ", "", "yolo", "planmode"} {
		if err := harness.h.settings.Set(contracts.SettingAgentMode, value); err != nil {
			t.Fatalf("set mode %q: %v", value, err)
		}
		before := harness.count("write_file")
		harness.runWithApproval(t, sessionID, "write_file", contracts.ApprovalDeny)
		if got := harness.count("write_file") - before; got != 0 {
			t.Fatalf("mode %q: write_file executed %d times after a denied approval, want 0", value, got)
		}
	}
}

// TestPlanModeSwitchBackRestoresApprovalsQueue covers switching: plan mode denies
// without queuing; back to normal the same call blocks on the approvals queue and
// an allow executes it; switching to plan again denies immediately.
func TestPlanModeSwitchBackRestoresApprovalsQueue(t *testing.T) {
	harness := startPlanHarness(t, contracts.AgentModePlan)
	sessionID := harness.newChatSession(t, "switch")

	// Plan mode: denied, never executed, never queued.
	harness.runImmediate(t, sessionID, "write_file")
	if harness.count("write_file") != 0 {
		t.Fatal("plan mode executed write_file")
	}

	// Back to normal: the same call must queue and only run after allow.
	if err := harness.h.settings.Set(contracts.SettingAgentMode, contracts.AgentModeNormal); err != nil {
		t.Fatalf("set normal: %v", err)
	}
	harness.runWithApproval(t, sessionID, "write_file", contracts.ApprovalAllow)
	if got := harness.count("write_file"); got != 1 {
		t.Fatalf("write_file executed %d times after allow, want 1", got)
	}

	// Plan again: immediate denial, no pending approval.
	if err := harness.h.settings.Set(contracts.SettingAgentMode, "PLAN"); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	before := harness.count("write_file")
	harness.runImmediate(t, sessionID, "write_file")
	if got := harness.count("write_file") - before; got != 0 {
		t.Fatalf("plan-mode re-entry executed write_file %d times, want 0", got)
	}
	if pending := harness.approvals().Pending(); len(pending) != 0 {
		t.Fatalf("pending approvals after re-entering plan = %d, want 0", len(pending))
	}
}

// TestPlanModeCombinesWithPermissionSetting covers the interaction with the
// permission setting: plan mode takes precedence over full/workspace permission
// (mutations still denied, reads allowed), while normal mode falls back to the
// permission policy (full auto-allows, never denies).
func TestPlanModeCombinesWithPermissionSetting(t *testing.T) {
	harness := startPlanHarness(t, contracts.AgentModePlan)
	sessionID := harness.newChatSession(t, "combo")
	setPermission := func(value string) {
		t.Helper()
		if err := harness.h.settings.Set(contracts.SettingAgentPermission, value); err != nil {
			t.Fatalf("set permission %q: %v", value, err)
		}
	}
	setMode := func(value string) {
		t.Helper()
		if err := harness.h.settings.Set(contracts.SettingAgentMode, value); err != nil {
			t.Fatalf("set mode %q: %v", value, err)
		}
	}

	// Plan + full: plan wins, write denied without queueing.
	setPermission(contracts.PermissionFull)
	setMode(contracts.AgentModePlan)
	harness.runImmediate(t, sessionID, "write_file")
	if got := harness.count("write_file"); got != 0 {
		t.Fatalf("plan+full executed write_file %d times, want 0", got)
	}

	// Normal + full: the permission policy auto-allows without queueing.
	setMode(contracts.AgentModeNormal)
	before := harness.count("write_file")
	harness.script.set("write_file")
	if err := harness.chat.Send(sessionID, "call write_file"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	harness.waitIdle(t, sessionID)
	if got := harness.count("write_file") - before; got != 1 {
		t.Fatalf("normal+full executed write_file %d times, want 1 (auto-allow)", got)
	}
	if pending := harness.approvals().Pending(); len(pending) != 0 {
		t.Fatalf("normal+full queued %d approvals, want 0", len(pending))
	}

	// Plan + workspace: read_file allowed, write_file denied.
	setPermission(contracts.PermissionWorkspace)
	setMode(contracts.AgentModePlan)
	readBefore := harness.count("read_file")
	harness.runImmediate(t, sessionID, "read_file")
	if got := harness.count("read_file") - readBefore; got != 1 {
		t.Fatalf("plan+workspace executed read_file %d times, want 1", got)
	}
	writeBefore := harness.count("write_file")
	harness.runImmediate(t, sessionID, "write_file")
	if got := harness.count("write_file") - writeBefore; got != 0 {
		t.Fatalf("plan+workspace executed write_file %d times, want 0", got)
	}

	// Normal + read-only: informational tools are auto-allowed while a
	// workspace write still queues for the user and only runs once allowed.
	setMode(contracts.AgentModeNormal)
	setPermission(contracts.PermissionReadOnly)
	readBefore = harness.count("read_file")
	harness.runImmediate(t, sessionID, "read_file")
	if got := harness.count("read_file") - readBefore; got != 1 {
		t.Fatalf("normal+read-only executed read_file %d times, want 1", got)
	}
	writeBefore = harness.count("write_file")
	session := harness.runWithApproval(t, sessionID, "write_file", contracts.ApprovalAllow)
	if got := harness.count("write_file") - writeBefore; got != 1 {
		t.Fatalf("normal+read-only executed write_file %d times after allow, want 1", got)
	}
	if status, _ := lastToolStatus(t, session); status != "ok" {
		t.Fatalf("normal+read-only write_file status = %q, want ok", status)
	}

	// Restore the default workspace policy for later assertions/hygiene.
	setPermission(contracts.PermissionWorkspace)
	setMode(contracts.AgentModeNormal)
}
