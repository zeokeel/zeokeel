package automation

// Tests for the cross-process registry lock policy: the first instance owns the
// scheduler; a second instance must not fail startup, must keep the read model
// and CRUD working, and must not run scheduled/manual tasks.

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSecondInstanceDegradesInsteadOfFailing(t *testing.T) {
	dataDir := t.TempDir()
	ownerChat := newFakeChat()
	owner, err := newService(dataDir, Config{TickMs: 3_600_000}, &fakeLogger{}, ownerChat, nil)
	if err != nil {
		t.Fatalf("owner newService: %v", err)
	}
	defer owner.Close()
	task := mustCreate(t, owner, contracts.AutomationTask{
		Name: "locked task", Prompt: "run", Schedule: "every 3600s", Enabled: true,
	})

	if err := owner.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("owner RunNow: %v", err)
	}
	_ = waitCall(t, ownerChat)

	// A second instance over the same data directory must activate, log the
	// policy clearly and keep the read model/CRUD, but never run tasks.
	degradedLogger := &fakeLogger{}
	degradedChat := newFakeChat()
	degraded, err := newService(dataDir, Config{TickMs: 3_600_000}, degradedLogger, degradedChat, nil)
	if err != nil {
		t.Fatalf("second newService must degrade, got error: %v", err)
	}
	if !degradedLogger.contains("locked by another") || !degradedLogger.contains("disabled") {
		t.Fatalf("degraded startup was not logged clearly: %v", degradedLogger.lines)
	}
	tasks, err := degraded.List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("degraded List = %+v/%v, want the owner's task", tasks, err)
	}
	if _, err := degraded.Create(context.Background(), contracts.AutomationTask{
		Name: "second", Prompt: "p", Schedule: "every 3600s", Enabled: true,
	}); err != nil {
		t.Fatalf("degraded Create should keep working: %v", err)
	}
	if err := degraded.RunNow(context.Background(), task.ID); !errors.Is(err, ErrSchedulerDisabled) {
		t.Fatalf("degraded RunNow = %v, want ErrSchedulerDisabled", err)
	}
	select {
	case call := <-degradedChat.calls:
		t.Fatalf("degraded instance delivered a run: %+v", call)
	default:
	}
	degraded.Close()

	// Closing the owner releases the lock: the next instance is live again.
	owner.Close()
	liveLogger := &fakeLogger{}
	liveChat := newFakeChat()
	live, err := newService(dataDir, Config{TickMs: 3_600_000}, liveLogger, liveChat, nil)
	if err != nil {
		t.Fatalf("newService after owner Close: %v", err)
	}
	t.Cleanup(live.Close)
	if liveLogger.contains("locked by another") {
		t.Fatalf("instance degraded after the owner released the lock: %v", liveLogger.lines)
	}
	if err := live.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("live RunNow: %v", err)
	}
	if call := waitCall(t, liveChat); call.SessionID == "" {
		t.Fatalf("live instance did not deliver: %+v", call)
	}
}

// TestModuleActivationSurvivesLockedRegistry pins that a locked registry is a
// degraded service, not a module activation failure.
func TestNewServiceNeverFailsOnLockContention(t *testing.T) {
	dataDir := t.TempDir()
	first, err := newService(dataDir, Config{TickMs: 3_600_000}, &fakeLogger{}, newFakeChat(), nil)
	if err != nil {
		t.Fatalf("first newService: %v", err)
	}
	defer first.Close()
	for attempt := 0; attempt < 3; attempt++ {
		logger := &fakeLogger{}
		service, err := newService(dataDir, Config{TickMs: 3_600_000}, logger, newFakeChat(), nil)
		if err != nil {
			t.Fatalf("attempt %d: newService returned %v, want degrade", attempt, err)
		}
		service.Close()
	}
}
