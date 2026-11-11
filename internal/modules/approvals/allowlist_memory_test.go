package approvals

// Regression tests for FR-18: the read-only preset must not auto-allow the
// memory tool (it can add/update/remove/clear durable memory), while the
// default workspace preset keeps it auto-allowed so routine memory writes do
// not prompt.

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestMemoryIsWorkspaceOnlyAutoAllowed pins the shared allowlist split.
func TestMemoryIsWorkspaceOnlyAutoAllowed(t *testing.T) {
	if contracts.ReadOnlyAutoAllowed("memory") {
		t.Fatal("read-only preset still auto-allows the writable memory tool")
	}
	if !contracts.WorkspaceAutoAllowed("memory") {
		t.Fatal("workspace preset should keep the memory tool auto-allowed")
	}
}

// TestReadOnlyPresetQueuesMemoryWrites covers the approvals service: a memory
// call under the read-only preset waits for the user instead of being allowed.
func TestReadOnlyPresetQueuesMemoryWrites(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission(contracts.PermissionReadOnly)
	svc, _ := newPermissionService(t, ModeWorkspace, settings)
	results := startRequest(context.Background(), svc, contracts.ApprovalRequest{
		Tool:      "memory",
		Arguments: `{"action":"clear"}`,
	})
	waitFor(t, "memory pending in read-only", func() bool { return len(svc.Pending()) == 1 })
	pending := svc.Pending()
	if err := svc.Resolve(pending[0].ID, contracts.ApprovalDeny); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result := waitResult(t, results); result.decision != contracts.ApprovalDeny {
		t.Fatalf("read-only memory decision = %q, want the queued answer", result.decision)
	}
}

// TestWorkspacePresetAutoAllowsMemory keeps the default preset behavior.
func TestWorkspacePresetAutoAllowsMemory(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission(contracts.PermissionWorkspace)
	svc, _ := newPermissionService(t, ModeWorkspace, settings)
	decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "memory"})
	if err != nil || decision != contracts.ApprovalAllow {
		t.Fatalf("workspace memory = %q/%v, want allow", decision, err)
	}
	if pending := svc.Pending(); len(pending) != 0 {
		t.Fatalf("workspace memory queued an approval: %v", pending)
	}
}
