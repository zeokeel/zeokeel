package agent

// Tests for the session-scoped "always allow" tool memory (the approval
// dialog's "always allow in this session") and its gate integration.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newPermissionService builds a service with one tool and one session.
func newPermissionService(t *testing.T, settings *fakeSettings, approvals contracts.Approvals) (*service, string) {
	t.Helper()
	if settings == nil {
		settings = newFakeSettings(nil)
	}
	svc := newService(Config{}, rigtools.NewRegistry(&gateTestTool{name: "bash"}),
		contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(), approvals)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, sessionID
}

// approveOne asks one tool through gate and returns the decision.
func approveOne(t *testing.T, gate rig.Gate, name string) rig.Decision {
	t.Helper()
	decision, err := gate.Approve(context.Background(), &gateTestTool{name: name}, rig.ToolCall{Name: name})
	if err != nil {
		t.Fatalf("Approve(%q): %v", name, err)
	}
	return decision
}

func TestParseAllowedTools(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: []string{}},
		{name: "malformed", raw: "not-json", want: []string{}},
		{name: "object", raw: `{"a":1}`, want: []string{}},
		{name: "null", raw: "null", want: []string{}},
		{name: "sorted deduped trimmed", raw: `["b"," a ","b","", "a"]`, want: []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAllowedTools(tc.raw)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("parseAllowedTools(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestAllowedToolsSessionScopedAndSorted(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SessionAllowedToolsSettingKey("sess-a"): `["write_file","read_file","write_file"]`,
		contracts.SessionAllowedToolsSettingKey("sess-b"): `["bash"]`,
	})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	if got := strings.Join(svc.AllowedTools("sess-a"), ","); got != "read_file,write_file" {
		t.Fatalf("AllowedTools(sess-a) = %q, want sorted deduped read_file,write_file", got)
	}
	if got := strings.Join(svc.AllowedTools("sess-b"), ","); got != "bash" {
		t.Fatalf("AllowedTools(sess-b) = %q, want bash", got)
	}
	if got := svc.AllowedTools("sess-missing"); got == nil || len(got) != 0 {
		t.Fatalf("AllowedTools(missing) = %v, want a non-nil empty slice", got)
	}
}

func TestAllowedToolsWithoutSettings(t *testing.T) {
	svc := newService(Config{}, rigtools.NewRegistry(), nil, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	if got := svc.AllowedTools("sess-a"); got == nil || len(got) != 0 {
		t.Fatalf("AllowedTools without settings = %v, want non-nil empty", got)
	}
	if err := svc.ClearAllowedTools("sess-a"); err != ErrSettingsUnavailable {
		t.Fatalf("ClearAllowedTools without settings = %v, want ErrSettingsUnavailable", err)
	}
	// Remembering without a settings store is a no-op, never a panic.
	svc.rememberAllowedTool("sess-a", "bash")
}

func TestClearAllowedTools(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SessionAllowedToolsSettingKey("sess-a"): `["bash"]`,
	})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	if err := svc.ClearAllowedTools("sess-a"); err != nil {
		t.Fatalf("ClearAllowedTools: %v", err)
	}
	if got := svc.AllowedTools("sess-a"); len(got) != 0 {
		t.Fatalf("AllowedTools after clear = %v, want empty", got)
	}
	if raw := settings.Get(contracts.SessionAllowedToolsSettingKey("sess-a"), "unset"); raw != "" {
		t.Fatalf("settings row after clear = %q, want empty", raw)
	}
}

// TestGateRemembersAllowAlwaysAcrossRuns is the user-facing fix: after one
// "always allow in this session" answer the same tool does not ask again, and
// the decision survives into the next run (a fresh gate closure).
func TestGateRemembersAllowAlwaysAcrossRuns(t *testing.T) {
	approvals := &recordingApprovals{decision: contracts.ApprovalAllowAlways}
	svc, sessionID := newPermissionService(t, nil, approvals)

	if decision := approveOne(t, svc.gate(sessionID), "bash"); decision != rig.DecisionAllowAlways {
		t.Fatalf("first approval = %q, want allow-always", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 1 || asked[0] != "bash" {
		t.Fatalf("approval requests = %v, want [bash]", asked)
	}
	if got := strings.Join(svc.AllowedTools(sessionID), ","); got != "bash" {
		t.Fatalf("remembered tools = %q, want bash", got)
	}
	// The set persisted as a JSON array row in the session's settings key.
	if raw := svc.settings.Get(contracts.SessionAllowedToolsSettingKey(sessionID), ""); raw != `["bash"]` {
		t.Fatalf("allowed-tools row = %q, want [\"bash\"]", raw)
	}

	// A later run builds a new gate; the remembered tool is allowed silently.
	if decision := approveOne(t, svc.gate(sessionID), "bash"); decision != rig.DecisionAllow {
		t.Fatalf("remembered approval = %q, want allow", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 1 {
		t.Fatalf("approval requests after remembering = %v, want no new request", asked)
	}
}

// TestGateRememberedToolsAreSessionScoped proves another session still asks.
func TestGateRememberedToolsAreSessionScoped(t *testing.T) {
	approvals := &recordingApprovals{decision: contracts.ApprovalAllowAlways}
	svc, sessionA := newPermissionService(t, nil, approvals)
	otherID, err := svc.NewSession("other")
	if err != nil {
		t.Fatalf("NewSession other: %v", err)
	}
	approveOne(t, svc.gate(sessionA), "bash")

	approvals.decision = contracts.ApprovalAllow
	if decision := approveOne(t, svc.gate(otherID), "bash"); decision != rig.DecisionAllow {
		t.Fatalf("other session approval = %q, want allow after asking", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 2 {
		t.Fatalf("approval requests = %v, want one per session", asked)
	}
	if got := svc.AllowedTools(otherID); len(got) != 0 {
		t.Fatalf("other session remembered tools = %v, want none", got)
	}
}

// TestGateRememberedToolClearedByClearAllowedTools proves the explicit clear
// brings the prompt back.
func TestGateRememberedToolClearedByClearAllowedTools(t *testing.T) {
	approvals := &recordingApprovals{decision: contracts.ApprovalAllowAlways}
	svc, sessionID := newPermissionService(t, nil, approvals)
	approveOne(t, svc.gate(sessionID), "bash")
	if err := svc.ClearAllowedTools(sessionID); err != nil {
		t.Fatalf("ClearAllowedTools: %v", err)
	}
	if decision := approveOne(t, svc.gate(sessionID), "bash"); decision != rig.DecisionAllowAlways {
		t.Fatalf("approval after clear = %q, want the dialog again", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 2 {
		t.Fatalf("approval requests after clear = %v, want a second request", asked)
	}
}

// TestDeleteSessionClearsAllowedTools proves the memory dies with its session.
func TestDeleteSessionClearsAllowedTools(t *testing.T) {
	approvals := &recordingApprovals{decision: contracts.ApprovalAllowAlways}
	svc, sessionID := newPermissionService(t, nil, approvals)
	approveOne(t, svc.gate(sessionID), "bash")
	if err := svc.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := svc.AllowedTools(sessionID); len(got) != 0 {
		t.Fatalf("remembered tools after delete = %v, want empty", got)
	}
	if raw := svc.settings.Get(contracts.SessionAllowedToolsSettingKey(sessionID), ""); raw != "" {
		t.Fatalf("allowed-tools row after delete = %q, want empty", raw)
	}
	// The next gate of that (now deleted) ID would ask again instead of relying
	// on a stale row.
	approvals.decision = contracts.ApprovalAllow
	if decision := approveOne(t, svc.gate(sessionID), "bash"); decision != rig.DecisionAllow {
		t.Fatalf("approval after delete = %q, want allow after asking", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 2 {
		t.Fatalf("approval requests after delete = %v, want a second request", asked)
	}
}

// TestMalformedAllowedToolsRowIgnored covers a corrupt settings row: the model
// asks again instead of failing.
func TestMalformedAllowedToolsRowIgnored(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SessionAllowedToolsSettingKey("sess-a"): "not-json",
	})
	svc := newService(Config{}, rigtools.NewRegistry(&gateTestTool{name: "bash"}),
		contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(),
		&recordingApprovals{})
	if got := svc.AllowedTools("sess-a"); len(got) != 0 {
		t.Fatalf("AllowedTools(malformed) = %v, want empty", got)
	}
	if decision := approveOne(t, svc.gate("sess-a"), "bash"); decision != rig.DecisionAllow {
		t.Fatalf("approval with malformed row = %q, want allow after asking", decision)
	}
}

// TestGateDenyAllPolicyStillDeniesRememberedTool covers the headless deny-all
// backend policy: the remembered "always allow" set only skips the prompt, it
// never overrides an explicit deny policy.
func TestGateDenyAllPolicyStillDeniesRememberedTool(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SessionAllowedToolsSettingKey("sess-a"): `["bash"]`,
	})
	approvals := &recordingApprovals{mode: approvalModeNever}
	svc := newService(Config{}, rigtools.NewRegistry(&gateTestTool{name: "bash"}),
		contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(), approvals)
	if decision := approveOne(t, svc.gate("sess-a"), "bash"); decision != rig.DecisionDeny {
		t.Fatalf("deny-all decision = %q, want deny", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("deny-all policy requested approval: %v", asked)
	}
}

// TestGateReadOnlyOverrideKeepsRememberedToolAllowed covers the read-only
// preset: it is a hard restriction, but a tool the user already remembered with
// "always allow in this session" stays allowed without a new prompt. Tools that
// were not remembered are denied outright — the global policy must not
// auto-allow workspace writes behind the read-only label.
func TestGateReadOnlyOverrideKeepsRememberedToolAllowed(t *testing.T) {
	settings := newFakeSettings(nil)
	approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
	svc, sessionID := newPermissionService(t, settings, approvals)
	if err := settings.Set(contracts.SessionAllowedToolsSettingKey(sessionID), `["bash"]`); err != nil {
		t.Fatalf("remember bash: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionReadOnly); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	if decision := approveOne(t, svc.gate(sessionID), "bash"); decision != rig.DecisionAllow {
		t.Fatalf("remembered bash under read-only = %q, want allow", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("remembered bash requested approval: %v", asked)
	}
	// A tool that was not remembered is denied without consulting the global
	// policy (whose workspace mode would auto-allow write_file).
	if decision := approveOne(t, svc.gate(sessionID), "write_file"); decision != rig.DecisionDeny {
		t.Fatalf("unremembered write_file under read-only = %q, want deny", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("read-only override requested approval: %v", asked)
	}
}

// TestGatePlanModeStillDeniesRememberedTool proves plan mode keeps precedence.
func TestGatePlanModeStillDeniesRememberedTool(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentMode:                        contracts.AgentModePlan,
		contracts.SessionAllowedToolsSettingKey("sess-a"): `["write_file"]`,
	})
	approvals := &recordingApprovals{}
	svc := newService(Config{}, rigtools.NewRegistry(&gateTestTool{name: "write_file"}),
		contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(), approvals)
	if decision := approveOne(t, svc.gate("sess-a"), "write_file"); decision != rig.DecisionDeny {
		t.Fatalf("plan mode remembered tool = %q, want deny", decision)
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("plan mode requested approval: %v", asked)
	}
}

// TestRememberAllowedToolConcurrentNoLostUpdates covers the read-modify-write
// serialization: parallel approvals for different tools must all survive.
func TestRememberAllowedToolConcurrentNoLostUpdates(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	const count = 12
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			svc.rememberAllowedTool("sess-a", fmt.Sprintf("tool-%02d", index))
		}()
	}
	wait.Wait()
	if got := svc.AllowedTools("sess-a"); len(got) != count {
		t.Fatalf("remembered %d tools, want %d: %v", len(got), count, got)
	}
}
