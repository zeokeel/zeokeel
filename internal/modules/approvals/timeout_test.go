package approvals

// RG-04 (2026-11 audit, personal-scale ruling): approvals get an OPT-IN
// timeout for automation-origin requests — agent.approvalTimeoutSeconds,
// default 0 = off (interactive approvals keep waiting exactly as before; there
// is no run-level time fallback — loop protection was removed in v0.2.50).
// When configured, an automation request that nobody answers is auto-denied,
// recorded and notified instead of parking until the whole run is cancelled.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// automationRequest is one queued approval from an unattended automation run
// (bash is not auto-allowed under the workspace policy, so it parks).
func automationRequest(sessionID string) contracts.ApprovalRequest {
	return contracts.ApprovalRequest{
		Tool:      "bash",
		Summary:   "run a command",
		Arguments: `{"command":"echo hi"}`,
		SessionID: sessionID,
		Source:    contracts.SessionOriginAutomation,
	}
}

func TestApprovalTimeoutDefaultDisabled(t *testing.T) {
	settings := newFakeSettings(nil) // no timeout row: the default
	svc, _ := newPermissionService(t, ModeWorkspace, settings)
	notifier := &recordingNotifier{}
	svc.notifier = notifier

	type outcome struct {
		decision contracts.ApprovalDecision
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		decision, err := svc.Request(context.Background(), automationRequest("sess-auto"))
		done <- outcome{decision, err}
	}()
	// Without a configured timeout the request must still be waiting well
	// beyond any implicit default.
	time.Sleep(120 * time.Millisecond)
	if pending := svc.Pending(); len(pending) != 1 {
		t.Fatalf("pending = %+v, want the automation request still waiting (default 0 = off)", pending)
	}
	if err := svc.Resolve(svc.Pending()[0].ID, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.decision != contracts.ApprovalAllow {
			t.Fatalf("Request = %v/%v, want allow", got.decision, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return after Resolve")
	}
}

func TestApprovalTimeoutAutomationAutoDeny(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentApprovalTimeoutSeconds: "1",
	})
	svc, logger := newPermissionService(t, ModeWorkspace, settings)
	notifier := &recordingNotifier{}
	svc.notifier = notifier

	start := time.Now()
	decision, err := svc.Request(context.Background(), automationRequest("sess-auto"))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if decision != contracts.ApprovalDeny {
		t.Fatalf("decision = %q, want deny (automation timeout)", decision)
	}
	if elapsed < 900*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("timeout fired after %v, want ~1s", elapsed)
	}
	if pending := svc.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %+v, want empty after the auto-deny", pending)
	}
	// Recorded: the answer is remembered (a late Resolve is idempotent, not
	// an unknown-request error) and the auto-deny is logged.
	if err := svc.Resolve("appr-1", contracts.ApprovalAllow); err != nil {
		t.Fatalf("late Resolve = %v, want nil (the deny is recorded)", err)
	}
	if !logger.contains("auto-denied") && !logger.contains("timeout") {
		t.Fatalf("auto-deny not logged: %v", logger.lines)
	}
	// Notified: the user learns the unattended run was denied, with the
	// owning session tag.
	level, text, sessionID, calls := notifier.snapshot()
	if calls < 2 {
		t.Fatalf("notifier calls = %d, want the waiting notice AND the timeout notice", calls)
	}
	if level != contracts.NotifyWarning || sessionID != "sess-auto" {
		t.Fatalf("timeout notification = %q/%q, want warning tagged sess-auto", level, sessionID)
	}
	if !strings.Contains(text, "超时") && !strings.Contains(strings.ToLower(text), "timed out") {
		t.Fatalf("timeout notification text = %q, want a timeout wording", text)
	}
}

// A configured timeout never touches a human turn: the user's own approval
// keeps waiting (personal-scale ruling — no added friction for interactive
// use).
func TestApprovalTimeoutUserSourceUnaffected(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentApprovalTimeoutSeconds: "1",
	})
	svc, _ := newPermissionService(t, ModeWorkspace, settings)
	request := automationRequest("sess-user")
	request.Source = contracts.AgentRunOriginUser

	done := make(chan contracts.ApprovalDecision, 1)
	go func() {
		decision, _ := svc.Request(context.Background(), request)
		done <- decision
	}()
	// Past the configured timeout the human request must still be parked.
	time.Sleep(1200 * time.Millisecond)
	if pending := svc.Pending(); len(pending) != 1 {
		t.Fatalf("pending = %+v, want the user request still waiting after the automation timeout window", pending)
	}
	if err := svc.Resolve(svc.Pending()[0].ID, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case decision := <-done:
		if decision != contracts.ApprovalAllow {
			t.Fatalf("user decision = %q, want allow", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("user Request did not return after Resolve")
	}
}

func TestApprovalTimeoutExplicitZeroAndMalformedDisabled(t *testing.T) {
	for _, raw := range []string{"0", "", "abc", "-3", "99999999"} {
		t.Run("row="+raw, func(t *testing.T) {
			settings := newFakeSettings(map[string]string{
				contracts.SettingAgentApprovalTimeoutSeconds: raw,
			})
			svc, _ := newPermissionService(t, ModeWorkspace, settings)
			done := make(chan contracts.ApprovalDecision, 1)
			go func() {
				decision, _ := svc.Request(context.Background(), automationRequest("sess-auto"))
				done <- decision
			}()
			time.Sleep(120 * time.Millisecond)
			pending := svc.Pending()
			if len(pending) != 1 {
				t.Fatalf("row %q: pending = %+v, want still waiting (disabled)", raw, pending)
			}
			if err := svc.Resolve(pending[0].ID, contracts.ApprovalAllow); err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			select {
			case decision := <-done:
				if decision != contracts.ApprovalAllow {
					t.Fatalf("row %q: decision = %q, want allow", raw, decision)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Request did not return after Resolve")
			}
		})
	}
}

// A human answer before the timer wins: no deny, no timeout notification.
func TestApprovalTimeoutResolveBeatsTimer(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentApprovalTimeoutSeconds: "60",
	})
	svc, _ := newPermissionService(t, ModeWorkspace, settings)
	notifier := &recordingNotifier{}
	svc.notifier = notifier
	done := make(chan contracts.ApprovalDecision, 1)
	go func() {
		decision, _ := svc.Request(context.Background(), automationRequest("sess-auto"))
		done <- decision
	}()
	// Wait for the request to park, then answer it.
	deadline := time.Now().Add(2 * time.Second)
	for len(svc.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := svc.Resolve(svc.Pending()[0].ID, contracts.ApprovalAllowAlways); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case decision := <-done:
		if decision != contracts.ApprovalAllowAlways {
			t.Fatalf("decision = %q, want allow_always", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return after Resolve")
	}
	if _, text, _, calls := notifier.snapshot(); calls != 1 || strings.Contains(text, "超时") {
		t.Fatalf("notifier = %d calls %q, want only the waiting notice", calls, text)
	}
}
