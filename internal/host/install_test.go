package host

// Tests for install-on-restart: applyPendingUpdate reads <dataDir>/updates/
// pending.json, validates the staged path and hands the replacement to an
// injected ExecutableReplacer (the real one copies+renames the running binary;
// it needs a GUI start to test end-to-end, so tests inject fakes). A failed
// replace keeps the pending plan for the next start.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeReplacer is a scripted ExecutableReplacer.
type fakeReplacer struct {
	current    string
	currentErr error
	replaceErr error
	replaced   []string
}

func (f *fakeReplacer) CurrentExecutable() (string, error) {
	if f.currentErr != nil {
		return "", f.currentErr
	}
	return f.current, nil
}

func (f *fakeReplacer) Replace(stagedPath string) error {
	f.replaced = append(f.replaced, stagedPath)
	return f.replaceErr
}

// writePendingUpdate lays out a staged asset and a pending.json under dataDir.
func writePendingUpdate(t *testing.T, dataDir string, pending pendingInstallFile, stagedContent string) {
	t.Helper()
	updates := filepath.Join(dataDir, "updates")
	if err := os.MkdirAll(updates, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	if stagedContent != "" {
		if err := os.WriteFile(pending.Path, []byte(stagedContent), 0o644); err != nil {
			t.Fatalf("write staged asset: %v", err)
		}
	}
	// "abc" is the placeholder the fixtures use when they do not care about the
	// checksum; fill in the real hash so the new pre-install verification passes.
	if pending.SHA256 == "abc" {
		sum := sha256.Sum256([]byte(stagedContent))
		pending.SHA256 = hex.EncodeToString(sum[:])
	}
	body, err := json.Marshal(pending)
	if err != nil {
		t.Fatalf("marshal pending: %v", err)
	}
	if err := os.WriteFile(filepath.Join(updates, "pending.json"), body, 0o644); err != nil {
		t.Fatalf("write pending.json: %v", err)
	}
}

func pendingJSONPath(dataDir string) string {
	return filepath.Join(dataDir, "updates", "pending.json")
}

func TestApplyPendingUpdateReplacesAndCleansUp(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "1.2.0-app.zip")
	pending := pendingInstallFile{Version: "1.2.0", Path: staged, SHA256: "abc"}
	writePendingUpdate(t, dataDir, pending, "new binary")

	replacer := &fakeReplacer{current: "/opt/zeokeel/zeokeel"}
	applied, err := applyPendingUpdate(dataDir, replacer, nil)
	if err != nil {
		t.Fatalf("applyPendingUpdate: %v", err)
	}
	if !applied {
		t.Fatal("applyPendingUpdate = false, want applied")
	}
	if len(replacer.replaced) != 1 || replacer.replaced[0] != staged {
		t.Fatalf("replaced = %v, want [%s]", replacer.replaced, staged)
	}
	if _, err := os.Stat(pendingJSONPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("pending.json still exists after apply: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged asset still exists after apply: %v", err)
	}
}

func TestApplyPendingUpdateReplaceFailureKeepsPlan(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "1.2.0-app.zip")
	pending := pendingInstallFile{Version: "1.2.0", Path: staged, SHA256: "abc"}
	writePendingUpdate(t, dataDir, pending, "new binary")

	replacer := &fakeReplacer{current: "/opt/zeokeel/zeokeel", replaceErr: errors.New("permission denied")}
	applied, err := applyPendingUpdate(dataDir, replacer, nil)
	if err == nil {
		t.Fatal("applyPendingUpdate replace failure = nil error")
	}
	if applied {
		t.Fatal("applyPendingUpdate replace failure reported applied")
	}
	if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
		t.Fatalf("pending.json was removed on replace failure: %v", statErr)
	}
	if _, statErr := os.Stat(staged); statErr != nil {
		t.Fatalf("staged asset was removed on replace failure: %v", statErr)
	}
}

func TestApplyPendingUpdateCurrentExecutableFailure(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "1.2.0-app.zip")
	writePendingUpdate(t, dataDir, pendingInstallFile{Version: "1.2.0", Path: staged, SHA256: "abc"}, "new binary")

	replacer := &fakeReplacer{currentErr: errors.New("no executable")}
	if _, err := applyPendingUpdate(dataDir, replacer, nil); err == nil {
		t.Fatal("applyPendingUpdate with a failing CurrentExecutable returned nil")
	}
	if len(replacer.replaced) != 0 {
		t.Fatalf("Replace ran without a current executable: %v", replacer.replaced)
	}
	if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
		t.Fatalf("pending.json removed after CurrentExecutable failure: %v", statErr)
	}
}

func TestApplyPendingUpdateWithoutPending(t *testing.T) {
	applied, err := applyPendingUpdate(t.TempDir(), &fakeReplacer{}, nil)
	if applied || err != nil {
		t.Fatalf("applyPendingUpdate(no pending) = %v/%v, want false/nil", applied, err)
	}
}

func TestApplyPendingUpdateBadJSON(t *testing.T) {
	dataDir := t.TempDir()
	updates := filepath.Join(dataDir, "updates")
	if err := os.MkdirAll(updates, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(updates, "pending.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write bad pending: %v", err)
	}
	if _, err := applyPendingUpdate(dataDir, &fakeReplacer{}, nil); err == nil {
		t.Fatal("applyPendingUpdate(bad JSON) = nil error")
	}
	if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
		t.Fatalf("bad pending.json was removed: %v", statErr)
	}
}

func TestApplyPendingUpdateRejectsEscapingPath(t *testing.T) {
	dataDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.zip")
	if err := os.WriteFile(outside, []byte("escape"), 0o644); err != nil {
		t.Fatalf("write outside asset: %v", err)
	}
	writePendingUpdate(t, dataDir, pendingInstallFile{Version: "1.2.0", Path: outside, SHA256: "abc"}, "")

	replacer := &fakeReplacer{current: "/opt/zeokeel/zeokeel"}
	applied, err := applyPendingUpdate(dataDir, replacer, nil)
	if err == nil || applied {
		t.Fatalf("applyPendingUpdate(escaping path) = %v/%v, want an error", applied, err)
	}
	if len(replacer.replaced) != 0 {
		t.Fatalf("Replace ran on an escaping path: %v", replacer.replaced)
	}
	if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
		t.Fatalf("pending.json removed after path rejection: %v", statErr)
	}
}

func TestApplyPendingUpdateMissingStagedFile(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "missing.zip")
	writePendingUpdate(t, dataDir, pendingInstallFile{Version: "1.2.0", Path: staged, SHA256: "abc"}, "")

	if _, err := applyPendingUpdate(dataDir, &fakeReplacer{current: "/opt/zeokeel/zeokeel"}, nil); err == nil {
		t.Fatal("applyPendingUpdate(missing staged file) = nil error")
	}
}

func TestApplyPendingUpdateIncompletePlan(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "1.2.0-app.zip")
	writePendingUpdate(t, dataDir, pendingInstallFile{Version: "  ", Path: staged, SHA256: "abc"}, "new binary")

	if _, err := applyPendingUpdate(dataDir, &fakeReplacer{current: "/opt/zeokeel/zeokeel"}, nil); err == nil {
		t.Fatal("applyPendingUpdate(empty version) = nil error")
	}
}

func TestApplyPendingUpdateWithoutReplacer(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "1.2.0-app.zip")
	writePendingUpdate(t, dataDir, pendingInstallFile{Version: "1.2.0", Path: staged, SHA256: "abc"}, "new binary")

	if _, err := applyPendingUpdate(dataDir, nil, nil); err == nil {
		t.Fatal("applyPendingUpdate(nil replacer) = nil error")
	}
}

// TestDefaultExecutableReplacerCurrentExecutable pins the real implementation's
// read half. Replace itself is not exercised here: it would overwrite the test
// binary; it only runs in a real process start.
func TestDefaultExecutableReplacerCurrentExecutable(t *testing.T) {
	current, err := defaultExecutableReplacer{}.CurrentExecutable()
	if err != nil {
		t.Fatalf("CurrentExecutable: %v", err)
	}
	if current == "" {
		t.Fatal("CurrentExecutable returned an empty path")
	}
}

var _ contracts.Logger = (*Logger)(nil)

// TestApplyPendingUpdateRejectsTamperedStagedFile covers the pre-install
// checksum re-verification: bytes changed after scheduling keep the plan.
func TestApplyPendingUpdateRejectsTamperedStagedFile(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "1.2.0-app.zip")
	writePendingUpdate(t, dataDir, pendingInstallFile{Version: "1.2.0", Path: staged, SHA256: "abc"}, "new binary")
	if err := os.WriteFile(staged, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper staged: %v", err)
	}
	replacer := &fakeReplacer{current: "/opt/zeokeel/zeokeel"}
	if _, err := applyPendingUpdate(dataDir, replacer, nil); err == nil {
		t.Fatal("tampered staged file was accepted")
	}
	if len(replacer.replaced) != 0 {
		t.Fatalf("replacer called for tampered file: %v", replacer.replaced)
	}
	if _, err := os.Stat(pendingJSONPath(dataDir)); err != nil {
		t.Fatalf("pending plan removed after checksum failure: %v", err)
	}
}

// TestApplyPendingUpdateRejectsMissingChecksum covers a pending plan without a
// checksum (cannot be trusted).
func TestApplyPendingUpdateRejectsMissingChecksum(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, "updates", "1.2.0-app.zip")
	writePendingUpdate(t, dataDir, pendingInstallFile{Version: "1.2.0", Path: staged}, "new binary")
	replacer := &fakeReplacer{current: "/opt/zeokeel/zeokeel"}
	if _, err := applyPendingUpdate(dataDir, replacer, nil); err == nil {
		t.Fatal("pending plan without checksum was accepted")
	}
}
