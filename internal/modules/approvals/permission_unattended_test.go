package approvals

// Unattended assemblies pin their approval mode: a permission preset saved by
// the desktop app (same data directory) must not turn a headless/ACP auto policy
// into one that blocks forever on an approval nobody can answer.

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestIgnorePermissionSettingPinsAssemblyMode(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionWorkspace,
	})
	svc, _ := newPermissionService(t, ModeAuto, settings)
	svc.ignorePermissionSetting = true

	if mode := snapshotMode(t, svc); mode != ModeAuto {
		t.Fatalf("Mode = %q, want %q (stored preset must be ignored)", mode, ModeAuto)
	}
	decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"})
	if err != nil || decision != contracts.ApprovalAllow {
		t.Fatalf("Request = (%q, %v), want allow", decision, err)
	}
	if pending := svc.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %v, want none for an unattended assembly", pending)
	}
}

func TestDesktopKeepsStoredPermissionSetting(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionReadOnly,
	})
	svc, _ := newPermissionService(t, ModeWorkspace, settings)
	if mode := snapshotMode(t, svc); mode != ModeReadOnly {
		t.Fatalf("Mode = %q, want %q (runtime preset wins on desktop)", mode, ModeReadOnly)
	}
}
