package automation

// Tests for the optional AutomationStatusProvider: a service that owns the
// registry lock is available; a second instance on the same registry degrades
// (scheduled/manual runs off, CRUD still usable) and reports the lock path.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	rigautomation "github.com/zeokeel/zeokeel-rig/session/automation"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestAutomationStatusHealthy(t *testing.T) {
	service := newTestService(t, t.TempDir(), Config{TickMs: 3_600_000}, newFakeChat())
	status := service.Status(context.Background())
	if !status.Available || status.Reason != "" || status.LockedBy != "" {
		t.Fatalf("healthy status = %+v, want available without reason/lock", status)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if status := service.Status(cancelled); status.Available || status.Reason == "" {
		t.Fatalf("cancelled-context status = %+v, want unavailable with a reason", status)
	}
}

func TestAutomationStatusDegradedWhenLocked(t *testing.T) {
	dir := t.TempDir()
	owner := newTestService(t, dir, Config{TickMs: 3_600_000}, newFakeChat())
	second := newTestService(t, dir, Config{TickMs: 3_600_000}, newFakeChat())
	ctx := context.Background()

	status := second.Status(ctx)
	if status.Available {
		t.Fatalf("second instance status = %+v, want degraded", status)
	}
	if !strings.Contains(status.Reason, "another instance") {
		t.Fatalf("degraded reason = %q, want the lock-contention explanation", status.Reason)
	}
	wantLock := filepath.Join(dir, rigautomation.LockFileName)
	if status.LockedBy != wantLock {
		t.Fatalf("degraded LockedBy = %q, want %q", status.LockedBy, wantLock)
	}
	if !owner.Status(ctx).Available {
		t.Fatal("the lock owner is not available")
	}

	// Degraded only disables runs: the read model and CRUD stay usable.
	created, err := second.Create(ctx, contracts.AutomationTask{
		Name: "degraded-create", Prompt: "p", Schedule: "every 3600s", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create on a degraded service: %v", err)
	}
	tasks, err := second.List(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].ID != created.ID {
		t.Fatalf("degraded List = %+v/%v, want the created task", tasks, err)
	}
	if err := second.RunNow(ctx, created.ID); err == nil {
		t.Fatal("RunNow on a degraded service succeeded")
	}
	if status := owner.Status(ctx); !status.Available {
		t.Fatalf("owner status changed to %+v", status)
	}
}
