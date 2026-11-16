package automation

// BE-09 / VC-01 (2026-11 audit): load() quarantined DECODE errors but left
// non-NotExist READ errors in place — empty start, then the next persist
// overwrote the path entry and lost the original tasks. A read error now
// quarantines exactly like a decode error (the load comment's FR-06 promise
// becomes true). Fault injection: a self-referential symlink (ELOOP), the
// root-uid-safe stand-in for a transient EIO/ESTALE.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestRegistryReadErrorQuarantined(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, DefaultFileName)
	if err := os.Symlink(path, path); err != nil {
		t.Fatalf("inject symlink loop: %v", err)
	}
	if _, err := os.ReadFile(path); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fault injection invalid: ReadFile err = %v, want a non-NotExist read error", err)
	}
	logger := &fakeLogger{}
	svc, err := newService(dataDir, Config{TickMs: 3_600_000}, logger, newFakeChat(), nil)
	if err != nil {
		t.Fatalf("newService with an unreadable registry = %v, want quarantine", err)
	}
	defer svc.Close()

	tasks, listErr := svc.List(context.Background())
	if listErr != nil || len(tasks) != 0 {
		t.Fatalf("tasks after read-error quarantine = %+v/%v, want empty", tasks, listErr)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("BE-09: unreadable registry entry still at %s (err=%v); the next write would overwrite it", path, err)
	}
	backups, globErr := filepath.Glob(path + ".corrupt-*")
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("quarantine backups = %v/%v, want exactly one", backups, globErr)
	}
	if !logger.contains("could not be read") || !logger.contains("quarantine") {
		t.Fatalf("read-error recovery was not logged: %v", logger.lines)
	}
	// The registry keeps working: a new task persists to the canonical path.
	if _, createErr := svc.Create(context.Background(), contracts.AutomationTask{
		Name: "recovered", Prompt: "run", Schedule: "every 3600s", Enabled: true,
	}); createErr != nil {
		t.Fatalf("Create after recovery: %v", createErr)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("registry not rewritten after recovery: %v", statErr)
	}
}
