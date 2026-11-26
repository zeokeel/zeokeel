package gateway

// The audit invariant for terminal task states (2026-11-26 audit, 高).
//
// "reviewer ≠ owner, and every task leaves a record" was bypassable: `task.status`
// moved a task straight to completed without writing any TaskReview, and that is
// the method the web client's complete/stop chip calls. This test drives the real
// protocol through a real lease service — claim → submit → task.status=completed —
// and then reads the review log the UI reads (`tasks.reviews`) to prove the close
// left a row that says it was not reviewed.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

// startAuditedDomainGateway is startDomainGateway plus a real task lease service
// over the same board, so claim/submit/review and the close audit are exercised
// through the protocol instead of through a fake.
func startAuditedDomainGateway(t *testing.T) (*domainTestRig, *tasklease.Service) {
	t.Helper()
	scripted := newScriptedChat()
	scripted.addSession("s1", "First", "hello")
	chat := scripted.fakeChat
	settings := newFakeSettings(map[string]string{contracts.SettingAgentPermission: contracts.PermissionWorkspace})
	approvals := newFakeApprovals(contracts.PermissionWorkspace)
	approvals.settings = settings
	workspace := newFakeWorkspace()
	board := newFakeTaskBoard()
	inspector := newFakeContextInspector()

	root := cordisRootFor(t, scripted, approvals, settings, workspace, board, inspector)
	leases := tasklease.NewService(board, tasklease.Config{})
	if _, err := root.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(leases)); err != nil {
		t.Fatalf("provide task lease: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	gateway.pollInterval = 20 * time.Millisecond
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	served := serveGateway(t, gateway)
	return &domainTestRig{runningGateway: served, chat: chat, approvals: approvals,
		settings: settings, workspace: workspace, tasks: board, context: inspector}, leases
}

// TestTaskStatusCloseLeavesAnAuditRow is the audit's reproduction, end to end.
func TestTaskStatusCloseLeavesAnAuditRow(t *testing.T) {
	rig, leases := startAuditedDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	task := rig.tasks.seed("ship it", contracts.TaskStatusPending)
	claimed := resultOf(t, conn.call("tasks.claim", map[string]any{
		"id": task.ID, "owner": contracts.PresetExecutorID,
	}))
	if claimed["state"] != contracts.LeaseStateClaimed {
		t.Fatalf("tasks.claim = %v, want a claimed lease", claimed)
	}
	if submitted := resultOf(t, conn.call("tasks.submit", map[string]any{
		"id": task.ID, "owner": contracts.PresetExecutorID,
		"evidence": []any{map[string]any{"kind": "file", "ref": "out/report.md"}},
	})); submitted["state"] != contracts.LeaseStateReview {
		t.Fatalf("tasks.submit = %v, want the review state", submitted)
	}

	// The close that used to be invisible: the UI's own chip.
	resultOf(t, conn.call("task.status", map[string]any{
		"id": task.ID, "status": contracts.TaskStatusCompleted,
	}))

	reviews := resultOf(t, conn.call("tasks.reviews", map[string]any{"id": task.ID}))
	rows, _ := reviews["reviews"].([]any)
	if len(rows) != 1 {
		t.Fatalf("tasks.reviews after a direct close = %v, want one recorded row", reviews["reviews"])
	}
	row, _ := rows[0].(map[string]any)
	if row["decision"] != contracts.TaskReviewDirectClose {
		t.Fatalf("review decision = %v, want %q", row["decision"], contracts.TaskReviewDirectClose)
	}
	if row["verdict"] != contracts.TaskVerdictUnaudited {
		t.Fatalf("review verdict = %v, want %q (a close nobody reviewed is not a pass)", row["verdict"], contracts.TaskVerdictUnaudited)
	}
	if row["reviewer"] != "user" {
		t.Fatalf("reviewer = %v, want the user who used the chip", row["reviewer"])
	}
	findings, _ := row["findings"].(string)
	for _, want := range []string{"tasks.status", "no lease review", "review"} {
		if !strings.Contains(findings, want) {
			t.Fatalf("findings %q is missing %q", findings, want)
		}
	}

	// The same log answers the in-process read the audit row is meant for.
	inProcess, err := leases.Reviews(context.Background(), task.ID)
	if err != nil || len(inProcess) != 1 || inProcess[0].Decision != contracts.TaskReviewDirectClose {
		t.Fatalf("lease Reviews = %+v/%v, want the same single row", inProcess, err)
	}
}

// TestTaskStatusNonTerminalLeavesNoReview: only terminal states are audit events; a
// move to in_progress is ordinary work, and recording it would bury the closes.
func TestTaskStatusNonTerminalLeavesNoReview(t *testing.T) {
	rig, _ := startAuditedDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	task := rig.tasks.seed("ship it", contracts.TaskStatusPending)
	resultOf(t, conn.call("task.status", map[string]any{
		"id": task.ID, "status": contracts.TaskStatusInProgress,
	}))
	reviews := resultOf(t, conn.call("tasks.reviews", map[string]any{"id": task.ID}))
	if rows, _ := reviews["reviews"].([]any); len(rows) != 0 {
		t.Fatalf("a non-terminal status change recorded a review: %v", reviews["reviews"])
	}
}
