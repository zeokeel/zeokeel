package host

// Tests for pending-install cleanup ordering (F9) and the dmg layout scan (F2).
// clearPendingInstall must delete the staged asset before pending.json: when the
// staged delete fails (the setup is still running from it, AV holds it, ...) the
// plan must stay so a later start can retry the cleanup instead of leaving an
// orphan forever.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeIncompleteStagedDir lays out a staged path that os.Remove cannot delete: a
// non-empty directory (the plan file itself is written separately).
func writeIncompleteStagedDir(t *testing.T, dataDir string) string {
	t.Helper()
	staged := filepath.Join(dataDir, updatesDirName, "0.2.8-staged")
	if err := os.MkdirAll(filepath.Join(staged, "child"), 0o755); err != nil {
		t.Fatalf("mkdir undeletable staged dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staged, "child", "file"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child file: %v", err)
	}
	return staged
}

// writeMinimalPending writes a pending.json without touching the staged path
// (used by the cleanup ordering tests).
func writeMinimalPending(t *testing.T, dataDir string) {
	t.Helper()
	updates := filepath.Join(dataDir, updatesDirName)
	if err := os.MkdirAll(updates, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	body := `{"version":"0.2.8","path":"staged","sha256":"00","kind":"binary"}` + "\n"
	if err := os.WriteFile(filepath.Join(updates, pendingInstallName), []byte(body), 0o644); err != nil {
		t.Fatalf("write pending.json: %v", err)
	}
}

// TestClearPendingInstallKeepsPlanWhenStagedDeleteFails covers F9: a failed
// staged delete leaves the plan in place for the next start to retry.
func TestClearPendingInstallKeepsPlanWhenStagedDeleteFails(t *testing.T) {
	dataDir := t.TempDir()
	writeMinimalPending(t, dataDir)
	staged := writeIncompleteStagedDir(t, dataDir)
	logger := &trayRecordingLogger{}

	clearPendingInstall(dataDir, staged, logger)

	if _, err := os.Stat(pendingJSONPath(dataDir)); err != nil {
		t.Fatalf("pending plan removed although the staged asset could not be deleted: %v", err)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged dir disappeared unexpectedly: %v", err)
	}
	if !strings.Contains(logger.text(), "cleanup") {
		t.Fatalf("log = %q, want the failed cleanup warning", logger.text())
	}

	// The next start retries: once the staged asset is removable, both go.
	if err := os.RemoveAll(filepath.Join(staged, "child")); err != nil {
		t.Fatalf("clear child dir: %v", err)
	}
	clearPendingInstall(dataDir, staged, nil)
	if _, err := os.Stat(pendingJSONPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("pending plan kept after a successful cleanup retry (err = %v)", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged asset kept after a successful cleanup retry (err = %v)", err)
	}
}

// TestClearPendingInstallRemovesPlanWhenStagedMissing covers the common case: the
// staged file is already gone (or was already deleted) so the plan is consumed.
func TestClearPendingInstallRemovesPlanWhenStagedMissing(t *testing.T) {
	dataDir := t.TempDir()
	writeMinimalPending(t, dataDir)
	staged := filepath.Join(dataDir, updatesDirName, "already-gone")

	clearPendingInstall(dataDir, staged, nil)

	if _, err := os.Stat(pendingJSONPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("pending plan kept although the staged asset is gone (err = %v)", err)
	}
}

// TestClearPendingInstallIdempotent covers a repeated cleanup (e.g. startup and
// the immediate-install path racing): the second call is a quiet no-op.
func TestClearPendingInstallIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	writeMinimalPending(t, dataDir)
	staged := filepath.Join(dataDir, updatesDirName, "staged")
	if err := os.WriteFile(staged, []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write staged: %v", err)
	}
	logger := &trayRecordingLogger{}
	clearPendingInstall(dataDir, staged, logger)
	clearPendingInstall(dataDir, staged, logger)
	if strings.Contains(logger.text(), "cleanup") {
		t.Fatalf("idempotent cleanup logged a warning: %q", logger.text())
	}
}

// TestInstallPendingUpdateNowCleansPlanAndStaged covers the non-installer
// immediate path: the plan and the staged asset are both consumed on success.
func TestInstallPendingUpdateNowCleansPlanAndStaged(t *testing.T) {
	dataDir := t.TempDir()
	current := filepath.Join(t.TempDir(), "zeokeel")
	writeExecutableFixture(t, current, "old binary")
	stubUpdateExecutable(t, current)
	staged := filepath.Join(dataDir, updatesDirName, "0.2.8-zeokeel")
	writePendingUpdate(t, dataDir, pendingInstallFile{
		Version: "0.2.8", Path: staged, SHA256: "abc", Kind: installKindBinary,
	}, "new binary")
	calls := make([]updateCall, 0)
	stubUpdateLaunch(t, &calls, nil)

	if err := InstallPendingUpdateNow(dataDir, nil); err != nil {
		t.Fatalf("InstallPendingUpdateNow: %v", err)
	}
	requireNoFile(t, pendingJSONPath(dataDir))
	requireNoFile(t, staged)
}

// TestSingleAppInDMGRealisticMount covers F2: mygo's real dmg layout has
// .DS_Store, .VolumeIcon.icns and the /Applications symlink next to the .app; the
// scan must pick the single .app and ignore every non-app entry.
func TestSingleAppInDMGRealisticMount(t *testing.T) {
	mount := filepath.Join(t.TempDir(), "ZeoKeel")
	writeRealisticDMGMount(t, mount, "ZeoKeel.app", "app bytes")

	app, err := singleAppInDMG(mount)
	if err != nil {
		t.Fatalf("singleAppInDMG on the real dmg layout: %v", err)
	}
	if filepath.Base(app) != "ZeoKeel.app" {
		t.Fatalf("singleAppInDMG = %q, want the ZeoKeel.app bundle", app)
	}
}

// TestSingleAppInDMGRejectsAmbiguousLayouts pins the safety margin: zero or
// several .app bundles is an unsupported image (never guess).
func TestSingleAppInDMGRejectsAmbiguousLayouts(t *testing.T) {
	t.Run("no app bundle", func(t *testing.T) {
		mount := filepath.Join(t.TempDir(), "mnt")
		writeRealisticDMGMount(t, mount, "Other.app", "app bytes")
		if err := os.RemoveAll(filepath.Join(mount, "Other.app")); err != nil {
			t.Fatalf("remove app bundle: %v", err)
		}
		if _, err := singleAppInDMG(mount); err == nil {
			t.Fatal("singleAppInDMG accepted a mount without an .app bundle")
		}
	})
	t.Run("two app bundles", func(t *testing.T) {
		mount := filepath.Join(t.TempDir(), "mnt")
		writeRealisticDMGMount(t, mount, "ZeoKeel.app", "app bytes")
		writeExecutableFixture(t, filepath.Join(mount, "ZeoKeel Copy.app", "Contents", "MacOS", "zeokeel"), "app bytes")
		if _, err := singleAppInDMG(mount); err == nil {
			t.Fatal("singleAppInDMG accepted a mount with two .app bundles")
		}
	})
}
