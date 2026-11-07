package regression

// Adversarial audit: install-on-restart, host half. The host applies
// <dataDir>/updates/pending.json before the assembly starts and replaces the
// running executable through the ExecutableReplacer seam.
//
// The applying function is unexported (the host package tests it internally); the
// audit may only add files under internal/regression, so this file reaches it
// through a test-only //go:linkname declaration. The ledger is the same
// pending.json schema the update module writes; test binaries are never replaced
// because the replacer is a fake.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	_ "unsafe"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/host"
)

// hostApplyPendingUpdate is the host's unexported applyPendingUpdate reached
// through a test-only linkname (see the file comment).
//
//go:linkname hostApplyPendingUpdate github.com/zeokeel/zeokeel/internal/host.applyPendingUpdate
func hostApplyPendingUpdate(dataDir string, replacer host.ExecutableReplacer, logger contracts.Logger) (bool, error)

// fakeExecutableReplacer records CurrentExecutable/Replace calls and returns the
// scripted results; it never touches the running binary.
type fakeExecutableReplacer struct {
	mu          sync.Mutex
	current     string
	currentErr  error
	replaceErr  error
	currentHits int
	replaced    []string
}

func (f *fakeExecutableReplacer) CurrentExecutable() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.currentHits++
	if f.currentErr != nil {
		return "", f.currentErr
	}
	if f.current == "" {
		return "/fake/zeokeel", nil
	}
	return f.current, nil
}

func (f *fakeExecutableReplacer) Replace(stagedPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaced = append(f.replaced, stagedPath)
	return f.replaceErr
}

func (f *fakeExecutableReplacer) replaceCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.replaced...)
}

// writeHostPending installs one pending plan and (unless stage=false) its staged
// file below <dataDir>/updates; it returns the staged path.
func writeHostPending(t *testing.T, dataDir, version, pathValue string, sha string) {
	t.Helper()
	updatesDir := filepath.Join(dataDir, "updates")
	if err := os.MkdirAll(updatesDir, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	body, err := json.Marshal(map[string]string{"version": version, "path": pathValue, "sha256": sha})
	if err != nil {
		t.Fatalf("encode pending plan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(updatesDir, "pending.json"), body, 0o644); err != nil {
		t.Fatalf("write pending.json: %v", err)
	}
}

// stageHostAsset writes one staged asset below <dataDir>/updates.
func stageHostAsset(t *testing.T, dataDir, name, content string) string {
	t.Helper()
	updatesDir := filepath.Join(dataDir, "updates")
	if err := os.MkdirAll(updatesDir, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	path := filepath.Join(updatesDir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write staged asset: %v", err)
	}
	return path
}

// hostPlanExists reports whether pending.json is still there.
func hostPlanExists(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "updates", "pending.json"))
	return err == nil
}

// TestHostApplyPendingUpdateSuccess covers the successful path: the fake
// replacer is called once with the staged file, then the plan and the staged
// file are cleaned up.
func TestHostApplyPendingUpdateSuccess(t *testing.T) {
	dataDir := t.TempDir()
	staged := stageHostAsset(t, dataDir, "new-binary", "new binary payload")
	writeHostPending(t, dataDir, "2.0.0", staged, sha256Hex("new binary payload"))
	replacer := &fakeExecutableReplacer{}

	applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{})
	if err != nil || !applied {
		t.Fatalf("applyPendingUpdate = (%v, %v), want (true, nil)", applied, err)
	}
	calls := replacer.replaceCalls()
	if len(calls) != 1 || calls[0] != staged {
		t.Fatalf("Replace calls = %v, want [%s]", calls, staged)
	}
	if hostPlanExists(dataDir) {
		t.Fatal("pending.json survived a successful install")
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged file survived a successful install: %v", err)
	}
}

// TestHostApplyReplaceFailureKeepsPlan covers a failed replace: applied=false
// with the error, and both the plan and the staged file stay for a retry.
func TestHostApplyReplaceFailureKeepsPlan(t *testing.T) {
	dataDir := t.TempDir()
	staged := stageHostAsset(t, dataDir, "new-binary", "new binary payload")
	writeHostPending(t, dataDir, "2.0.0", staged, sha256Hex("new binary payload"))
	replacer := &fakeExecutableReplacer{replaceErr: errors.New("disk is read-only")}

	applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{})
	if err == nil || applied {
		t.Fatalf("applyPendingUpdate = (%v, %v), want (false, error)", applied, err)
	}
	if !strings.Contains(err.Error(), "disk is read-only") {
		t.Fatalf("error = %v, want the replacer failure", err)
	}
	if len(replacer.replaceCalls()) != 1 {
		t.Fatalf("Replace calls = %v, want one attempt", replacer.replaceCalls())
	}
	if !hostPlanExists(dataDir) {
		t.Fatal("pending.json removed after a failed replace, want it kept for retry")
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged file removed after a failed replace: %v", err)
	}

	// A later start with a healthy replacer succeeds and cleans up.
	healthy := &fakeExecutableReplacer{}
	if applied, err := hostApplyPendingUpdate(dataDir, healthy, &recLogger{}); err != nil || !applied {
		t.Fatalf("retry applyPendingUpdate = (%v, %v), want success", applied, err)
	}
	if hostPlanExists(dataDir) {
		t.Fatal("pending.json survived the retry install")
	}
}

// TestHostApplyHostilePlansNeverReplaces covers tampered plans: traversal paths,
// absolute paths outside updates, malformed/incomplete JSON, missing/directory
// staged files, a failing CurrentExecutable and an empty data dir must all fail
// without calling Replace.
func TestHostApplyHostilePlansNeverReplaces(t *testing.T) {
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.bin")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o755); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	dirAsStaged := filepath.Join(outsideDir, "a-directory")
	if err := os.Mkdir(dirAsStaged, 0o755); err != nil {
		t.Fatalf("mkdir dirAsStaged: %v", err)
	}

	t.Run("traversal path", func(t *testing.T) {
		dataDir := t.TempDir()
		writeHostPending(t, dataDir, "2.0.0", "../../../etc/passwd", "abc")
		replacer := &fakeExecutableReplacer{}
		applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{})
		if err == nil || applied {
			t.Fatalf("applyPendingUpdate(traversal) = (%v, %v), want (false, error)", applied, err)
		}
		if calls := replacer.replaceCalls(); len(calls) != 0 {
			t.Fatalf("Replace was called for a traversal plan: %v", calls)
		}
		if !hostPlanExists(dataDir) {
			t.Fatal("traversal plan was silently removed")
		}
	})

	t.Run("absolute outside path", func(t *testing.T) {
		dataDir := t.TempDir()
		writeHostPending(t, dataDir, "2.0.0", outsideFile, "abc")
		replacer := &fakeExecutableReplacer{}
		if applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{}); err == nil || applied {
			t.Fatalf("applyPendingUpdate(outside) = (%v, %v), want (false, error)", applied, err)
		}
		if calls := replacer.replaceCalls(); len(calls) != 0 {
			t.Fatalf("Replace was called for an outside plan: %v", calls)
		}
	})

	t.Run("malformed and incomplete json", func(t *testing.T) {
		for name, body := range map[string]string{
			"bad json":        "{not json",
			"empty object":    `{}`,
			"missing version": `{"path":"` + outsideFile + `","sha256":"abc"}`,
			"missing path":    `{"version":"2.0.0","sha256":"abc"}`,
		} {
			dataDir := t.TempDir()
			updatesDir := filepath.Join(dataDir, "updates")
			if err := os.MkdirAll(updatesDir, 0o755); err != nil {
				t.Fatalf("mkdir updates: %v", err)
			}
			if err := os.WriteFile(filepath.Join(updatesDir, "pending.json"), []byte(body), 0o644); err != nil {
				t.Fatalf("write pending.json: %v", err)
			}
			replacer := &fakeExecutableReplacer{}
			if applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{}); err == nil || applied {
				t.Fatalf("%s: applyPendingUpdate = (%v, %v), want (false, error)", name, applied, err)
			}
			if calls := replacer.replaceCalls(); len(calls) != 0 {
				t.Fatalf("%s: Replace was called: %v", name, calls)
			}
		}
	})

	t.Run("missing staged file", func(t *testing.T) {
		dataDir := t.TempDir()
		missing := filepath.Join(dataDir, "updates", "gone.bin")
		writeHostPending(t, dataDir, "2.0.0", missing, "abc")
		replacer := &fakeExecutableReplacer{}
		if applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{}); err == nil || applied {
			t.Fatalf("applyPendingUpdate(missing staged) = (%v, %v), want (false, error)", applied, err)
		}
		if calls := replacer.replaceCalls(); len(calls) != 0 {
			t.Fatalf("Replace was called for a missing staged file: %v", calls)
		}
	})

	t.Run("directory staged", func(t *testing.T) {
		dataDir := t.TempDir()
		writeHostPending(t, dataDir, "2.0.0", dirAsStaged, "abc")
		replacer := &fakeExecutableReplacer{}
		if applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{}); err == nil || applied {
			t.Fatalf("applyPendingUpdate(directory) = (%v, %v), want (false, error)", applied, err)
		}
		if calls := replacer.replaceCalls(); len(calls) != 0 {
			t.Fatalf("Replace was called for a directory staged path: %v", calls)
		}
	})

	t.Run("current executable error", func(t *testing.T) {
		dataDir := t.TempDir()
		staged := stageHostAsset(t, dataDir, "asset", "payload")
		writeHostPending(t, dataDir, "2.0.0", staged, sha256Hex("payload"))
		replacer := &fakeExecutableReplacer{currentErr: errors.New("no executable")}
		if applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{}); err == nil || applied {
			t.Fatalf("applyPendingUpdate(CurrentExecutable error) = (%v, %v), want (false, error)", applied, err)
		}
		if calls := replacer.replaceCalls(); len(calls) != 0 {
			t.Fatalf("Replace was called after CurrentExecutable failed: %v", calls)
		}
		if !hostPlanExists(dataDir) {
			t.Fatal("plan removed after CurrentExecutable failed")
		}
	})

	t.Run("nil replacer and empty data dir", func(t *testing.T) {
		dataDir := t.TempDir()
		staged := stageHostAsset(t, dataDir, "asset", "payload")
		writeHostPending(t, dataDir, "2.0.0", staged, sha256Hex("payload"))
		if applied, err := hostApplyPendingUpdate(dataDir, nil, &recLogger{}); err == nil || applied {
			t.Fatalf("applyPendingUpdate(nil replacer) = (%v, %v), want (false, error)", applied, err)
		}
		if applied, err := hostApplyPendingUpdate("   ", &fakeExecutableReplacer{}, &recLogger{}); err == nil || applied {
			t.Fatalf("applyPendingUpdate(empty data dir) = (%v, %v), want (false, error)", applied, err)
		}
	})
}

// TestHostApplyMissingPlan launches with no pending.json: no install, no error,
// no replacer calls.
func TestHostApplyMissingPlan(t *testing.T) {
	replacer := &fakeExecutableReplacer{}
	applied, err := hostApplyPendingUpdate(t.TempDir(), replacer, &recLogger{})
	if err != nil || applied {
		t.Fatalf("applyPendingUpdate(no plan) = (%v, %v), want (false, nil)", applied, err)
	}
	if calls := replacer.replaceCalls(); len(calls) != 0 {
		t.Fatalf("Replace was called without a plan: %v", calls)
	}
	if replacer.currentHits != 0 {
		t.Fatalf("CurrentExecutable was called without a plan")
	}
}

// TestHostApplyDoesNotReverifyChecksum is a known-defect probe: pending.json
// carries the scheduled sha256, but the host does not re-verify the staged file
// before replacing the executable, so a staged file changed after scheduling is
// installed as-is (AUDIT-P3-10; the data dir is user-owned, so impact is low).
func TestHostApplyDoesNotReverifyChecksum(t *testing.T) {
	dataDir := t.TempDir()
	staged := stageHostAsset(t, dataDir, "asset", "original payload")
	writeHostPending(t, dataDir, "2.0.0", staged, sha256Hex("original payload"))
	// Tamper with the staged file after the plan recorded the checksum.
	if err := os.WriteFile(staged, []byte("tampered payload"), 0o755); err != nil {
		t.Fatalf("tamper staged file: %v", err)
	}
	replacer := &fakeExecutableReplacer{}
	applied, err := hostApplyPendingUpdate(dataDir, replacer, &recLogger{})
	if applied && err == nil {
		t.Logf("AUDIT-P3-10: pending.json sha256 is not re-verified at apply time; tampered staged content was installed")
		return
	}
	// A fixed host rejects the mismatch and keeps the plan.
	if err == nil || applied {
		t.Fatalf("applyPendingUpdate(tampered) = (%v, %v), want (false, error)", applied, err)
	}
	if !hostPlanExists(dataDir) {
		t.Fatal("plan removed after a checksum rejection, want it kept")
	}
}
