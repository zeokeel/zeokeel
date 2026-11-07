package regression

// Adversarial audit: the runtime permission modes over approvals
// (settings key agent.permission: ask / workspace / full / auto / never).
// Negative cases: workspace mode auto-allows only the read-only tool set and
// still queues writes/network; unknown/malicious setting values fall back to
// ask; mode switches during a pending request never corrupt the queue; and
// concurrent settings writers + requests stay race free and deadlock free.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
)

// permissionService activates approvals and sets the permissions setting.
func permissionService(t *testing.T, configMode, permission string) *harness {
	t.Helper()
	h := startApprovals(t, configMode)
	if permission != "" {
		if err := h.settings.Set(contracts.SettingAgentPermission, permission); err != nil {
			t.Fatalf("set %s: %v", contracts.SettingAgentPermission, err)
		}
	}
	return h
}

// requestAndResolve starts one blocking Request, resolves it with allow and
// returns the decision.
func requestAndResolve(t *testing.T, service contracts.Approvals, request contracts.ApprovalRequest) contracts.ApprovalDecision {
	t.Helper()
	results := requestAsync(context.Background(), service, request)
	waitFor(t, "pending approval "+request.Tool, func() bool { return len(service.Pending()) > 0 })
	if err := service.Resolve(service.Pending()[0].ID, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return awaitApproval(t, results).decision
}

// requestAndCancel starts one blocking Request and cancels its context.
func requestAndCancel(t *testing.T, service contracts.Approvals, request contracts.ApprovalRequest) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	results := requestAsync(ctx, service, request)
	waitFor(t, "pending approval "+request.Tool, func() bool { return len(service.Pending()) > 0 })
	cancel()
	result := awaitApproval(t, results)
	return result.err
}

// TestPermissionWorkspaceModeAllowList is the workspace-mode negative: only the
// read-only workspace tools are auto-allowed; write, shell, network and unknown
// tool names still queue for the user.
func TestPermissionWorkspaceModeAllowList(t *testing.T) {
	h := permissionService(t, approvals.ModeAsk, contracts.PermissionWorkspace)
	service := requireService(t, h.ctx, contracts.KeyApprovals)

	if got := service.Mode(); got != contracts.PermissionWorkspace {
		t.Fatalf("Mode() = %q, want workspace", got)
	}
	notifications := new(counter)
	if _, err := service.Subscribe(func() { notifications.inc() }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// The documented read-only tool set returns allow immediately.
	for _, tool := range []string{"read_file", "list_dir", "glob", "grep", " read_file "} {
		decision, err := service.Request(context.Background(), contracts.ApprovalRequest{Tool: tool})
		if err != nil || decision != contracts.ApprovalAllow {
			t.Fatalf("Request(%q) in workspace mode = (%q, %v), want allow", tool, decision, err)
		}
		if pending := service.Pending(); len(pending) != 0 {
			t.Fatalf("Request(%q) queued a pending approval: %+v", tool, pending)
		}
	}
	if got := notifications.load(); got != 0 {
		t.Fatalf("workspace auto-allow notified subscribers %d times, want 0", got)
	}

	// Everything that writes, executes or reaches the network still queues.
	for _, tool := range []string{"write_file", "edit", "bash", "terminal", "web_fetch", "web_search", "mcp__server__run", ""} {
		decision := requestAndResolve(t, service, contracts.ApprovalRequest{Tool: tool})
		if decision != contracts.ApprovalAllow {
			t.Fatalf("Request(%q) in workspace mode = %q, want the user decision (allow)", tool, decision)
		}
	}
	// Uppercase is not the documented tool name: conservative queueing, not a
	// silent auto-allow.
	if err := requestAndCancel(t, service, contracts.ApprovalRequest{Tool: "READ_FILE"}); err == nil {
		t.Fatal("uppercase READ_FILE was auto-allowed, want it queued")
	}
}

// TestPermissionSettingModes covers every accepted value, the legacy auto
// alias, config fallback and unknown/malicious values degrading to ask.
func TestPermissionSettingModes(t *testing.T) {
	cases := []struct {
		permission string
		wantMode   string
	}{
		{contracts.PermissionAsk, approvals.ModeAsk},
		{" ASK ", approvals.ModeAsk},
		{contracts.PermissionWorkspace, contracts.PermissionWorkspace},
		{contracts.PermissionFull, approvals.ModeAuto},
		{"AUTO", approvals.ModeAuto},
	}
	for _, testCase := range cases {
		h := permissionService(t, approvals.ModeAsk, testCase.permission)
		service := requireService(t, h.ctx, contracts.KeyApprovals)
		if got := service.Mode(); got != testCase.wantMode {
			t.Fatalf("setting %q: Mode() = %q, want %q", testCase.permission, got, testCase.wantMode)
		}
	}
	// A "never" setting maps onto the deny-everything policy (AUDIT-P3-8).
	for _, permission := range []string{approvals.ModeNever, "NEVER"} {
		h := permissionService(t, approvals.ModeAsk, permission)
		service := requireService(t, h.ctx, contracts.KeyApprovals)
		if got := service.Mode(); got != approvals.ModeNever {
			t.Fatalf("setting %q: Mode() = %q, want never", permission, got)
		}
	}

	// An empty setting keeps the assembly mode.
	configFallback := permissionService(t, approvals.ModeAuto, "")
	if got := requireService(t, configFallback.ctx, contracts.KeyApprovals).Mode(); got != approvals.ModeAuto {
		t.Fatalf("Mode() with no setting = %q, want the configured auto", got)
	}

	// Unknown and malicious values degrade to ask and queue the request.
	for _, permission := range []string{
		"root", "full;drop table", "neverr", "\x00full", strings.Repeat("x", 4096),
	} {
		h := permissionService(t, approvals.ModeAuto, permission)
		service := requireService(t, h.ctx, contracts.KeyApprovals)
		if got := service.Mode(); got != approvals.ModeAsk {
			t.Fatalf("setting %q: Mode() = %q, want ask fallback", permission, got)
		}
		if err := requestAndCancel(t, service, contracts.ApprovalRequest{Tool: "bash"}); err == nil {
			t.Fatalf("setting %q: Request was auto-decided, want a queued approval", permission)
		}
	}
}

// TestPermissionModeSwitchDuringPendingRequest covers switching the setting
// while a request is pending: the waiting request keeps its answer path and the
// next request follows the new mode.
func TestPermissionModeSwitchDuringPendingRequest(t *testing.T) {
	h := permissionService(t, approvals.ModeAsk, contracts.PermissionAsk)
	service := requireService(t, h.ctx, contracts.KeyApprovals)

	results := requestAsync(context.Background(), service, contracts.ApprovalRequest{Tool: "bash"})
	waitFor(t, "pending approval", func() bool { return len(service.Pending()) == 1 })
	pendingID := service.Pending()[0].ID
	if err := h.settings.Set(contracts.SettingAgentPermission, contracts.PermissionFull); err != nil {
		t.Fatalf("switch to full: %v", err)
	}
	if qot := service.Mode(); qot != approvals.ModeAuto {
		t.Fatalf("Mode() after switch = %q, want auto", qot)
	}
	if pending := service.Pending(); len(pending) != 1 || pending[0].ID != pendingID {
		t.Fatalf("switching the mode disturbed the pending request: %+v", pending)
	}
	if err := service.Resolve(pendingID, contracts.ApprovalDeny); err != nil {
		t.Fatalf("Resolve pending request after switch: %v", err)
	}
	if result := awaitApproval(t, results); result.decision != contracts.ApprovalDeny {
		t.Fatalf("pending request decision = %q, want the resolved deny", result.decision)
	}
	// New requests follow full mode: allow without queueing.
	if decision, err := service.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"}); err != nil ||
		decision != contracts.ApprovalAllow {
		t.Fatalf("Request after switching to full = (%q, %v), want allow", decision, err)
	}
	// And switching back to ask queues again.
	if err := h.settings.Set(contracts.SettingAgentPermission, contracts.PermissionAsk); err != nil {
		t.Fatalf("switch back to ask: %v", err)
	}
	if err := requestAndCancel(t, service, contracts.ApprovalRequest{Tool: "bash"}); err == nil {
		t.Fatal("Request after switching back to ask was auto-decided, want a queued approval")
	}
}

// TestPermissionSettingsSwitchRace hammers setting writes while requests read
// the mode and queue/resolve: no deadlock, every request terminates, the queue
// drains and -race stays clean.
func TestPermissionSettingsSwitchRace(t *testing.T) {
	h := permissionService(t, approvals.ModeAsk, contracts.PermissionAsk)
	service := requireService(t, h.ctx, contracts.KeyApprovals)
	notified := make(chan struct{}, 1)
	if _, err := service.Subscribe(func() {
		select {
		case notified <- struct{}{}:
		default:
		}
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	values := []string{
		contracts.PermissionAsk, contracts.PermissionWorkspace, contracts.PermissionFull,
		approvals.ModeNever, "bogus", "",
	}
	var wg sync.WaitGroup
	wg.Add(3)
	// Writer: flip the permissions setting continuously.
	go func() {
		defer wg.Done()
		for index := 0; index < 300; index++ {
			_ = h.settings.Set(contracts.SettingAgentPermission, values[index%len(values)])
		}
	}()
	// Reader: mode/pending snapshots.
	go func() {
		defer wg.Done()
		for index := 0; index < 300; index++ {
			_ = service.Mode()
			_ = service.Pending()
		}
	}()
	// Requester: each request either resolves via the queue or auto-decides.
	go func() {
		defer wg.Done()
		for index := 0; index < 60; index++ {
			ctx, cancel := context.WithCancel(context.Background())
			results := requestAsync(ctx, service, contracts.ApprovalRequest{Tool: "bash"})
			finished := false
			for !finished {
				select {
				case <-results:
					finished = true
				case <-notified:
					for _, pending := range service.Pending() {
						_ = service.Resolve(pending.ID, contracts.ApprovalAllow)
					}
				}
			}
			cancel()
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("permission settings/request race deadlocked")
	}
	waitFor(t, "pending queue to drain", func() bool { return len(service.Pending()) == 0 })
	if err := h.settings.Set(contracts.SettingAgentPermission, contracts.PermissionAsk); err != nil {
		t.Fatalf("restore ask: %v", err)
	}
	if got := service.Mode(); got != approvals.ModeAsk {
		t.Fatalf("Mode() = %q after restoring ask, want ask", got)
	}
}

// TestPermissionWithoutSettingsService covers a host with no settings service:
// the assembly config mode is authoritative and no permission override exists.
func TestPermissionWithoutSettingsService(t *testing.T) {
	never := newBareHarness(t)
	activate(t, never.ctx, approvals.Module(), approvals.Config{Mode: approvals.ModeNever})
	service := requireService(t, never.ctx, contracts.KeyApprovals)
	if got := service.Mode(); got != approvals.ModeNever {
		t.Fatalf("Mode() without settings = %q, want the configured never", got)
	}
	if decision, err := service.Request(context.Background(), contracts.ApprovalRequest{Tool: "read_file"}); err != nil ||
		decision != contracts.ApprovalDeny {
		t.Fatalf("Request without settings = (%q, %v), want deny", decision, err)
	}

	ask := newBareHarness(t)
	activate(t, ask.ctx, approvals.Module(), approvals.Config{Mode: approvals.ModeAsk})
	askService := requireService(t, ask.ctx, contracts.KeyApprovals)
	if err := requestAndCancel(t, askService, contracts.ApprovalRequest{Tool: "read_file"}); err == nil {
		t.Fatal("Request without settings in ask mode was auto-decided")
	}
}
