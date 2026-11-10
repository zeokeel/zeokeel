package host

// Tests for the installer-aware half of install-on-restart (pending.json
// "kind"): AppImage replacement, tar.gz/.zip archive installs (including the
// macOS bundle swap and the Windows-safe rename dance) and the .old backup
// cleanup. Every test uses temp directories and injected paths: the test binary
// and the developer binary are never replaced.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testArchiveEntry is one member the archive fixtures build.
type testArchiveEntry struct {
	name string
	body string
	mode os.FileMode
	dir  bool
}

// buildTarGz builds a gzip-compressed tar archive for the fixtures.
func buildTarGz(t *testing.T, entries []testArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		mode := entry.mode
		if mode == 0 {
			mode = 0o644
		}
		name := entry.name
		header := &tar.Header{Name: name, Mode: int64(mode.Perm())}
		if entry.dir {
			header.Typeflag = tar.TypeDir
			header.Mode = int64(mode.Perm() | 0o700)
		} else {
			header.Typeflag = tar.TypeReg
			header.Size = int64(len(entry.body))
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("tar write header %q: %v", name, err)
		}
		if !entry.dir {
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatalf("tar write body %q: %v", name, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buffer.Bytes()
}

// buildZip builds a zip archive for the fixtures.
func buildZip(t *testing.T, entries []testArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		mode := entry.mode
		if mode == 0 {
			mode = 0o644
		}
		if entry.dir {
			mode |= os.ModeDir | 0o700
		}
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(mode)
		if entry.dir {
			header.Name = strings.TrimSuffix(entry.name, "/") + "/"
		}
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("zip create %q: %v", entry.name, err)
		}
		if !entry.dir {
			if _, err := member.Write([]byte(entry.body)); err != nil {
				t.Fatalf("zip write %q: %v", entry.name, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buffer.Bytes()
}

// sha256Hex returns the lowercase hex SHA-256 of body.
func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// writePendingUpdateBytes lays out a staged asset and a matching pending.json.
func writePendingUpdateBytes(t *testing.T, dataDir string, pending pendingInstallFile, stagedBody []byte) string {
	t.Helper()
	updates := filepath.Join(dataDir, updatesDirName)
	if err := os.MkdirAll(updates, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	if err := os.WriteFile(pending.Path, stagedBody, 0o644); err != nil {
		t.Fatalf("write staged asset: %v", err)
	}
	pending.SHA256 = sha256Hex(stagedBody)
	body, err := json.Marshal(pending)
	if err != nil {
		t.Fatalf("marshal pending: %v", err)
	}
	if err := os.WriteFile(pendingJSONPath(dataDir), body, 0o644); err != nil {
		t.Fatalf("write pending.json: %v", err)
	}
	return pending.Path
}

// writeFakeExecutable lays out a fake "running" executable and returns its path.
func writeFakeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake executable %q: %v", path, err)
	}
	return path
}

// requireFileContent asserts path holds want.
func requireFileContent(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(body) != want {
		t.Fatalf("%q = %q, want %q", path, body, want)
	}
}

// TestPendingInstallFileWithoutKindInstallsAsBinary pins backward
// compatibility: pending.json written before "kind" existed still installs.
func TestPendingInstallFileWithoutKindInstallsAsBinary(t *testing.T) {
	dataDir := t.TempDir()
	updates := filepath.Join(dataDir, updatesDirName)
	if err := os.MkdirAll(updates, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	staged := filepath.Join(updates, "1.2.0-app.zip")
	body := []byte("new binary")
	if err := os.WriteFile(staged, body, 0o644); err != nil {
		t.Fatalf("write staged asset: %v", err)
	}
	// No "kind" key at all, exactly like the pre-kind schema.
	raw := fmt.Sprintf(`{"version":"1.2.0","path":%q,"sha256":%q}`, staged, sha256Hex(body))
	if err := os.WriteFile(pendingJSONPath(dataDir), []byte(raw), 0o644); err != nil {
		t.Fatalf("write pending.json: %v", err)
	}

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
}

// TestApplyPendingUpdateUnknownKindFallsBackToBinary pins that an unknown kind
// (from a newer writer) installs as a plain binary instead of failing.
func TestApplyPendingUpdateUnknownKindFallsBackToBinary(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, updatesDirName, "1.2.0-app.rpm")
	writePendingUpdateBytes(t, dataDir, pendingInstallFile{
		Version: "1.2.0", Path: staged, Kind: "rpm-deb-flatpak",
	}, []byte("new binary"))

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
}

// TestApplyPendingUpdateAppImageReplacesAppImage covers the AppImage kind: the
// file named by $APPIMAGE is replaced atomically with the staged AppImage.
func TestApplyPendingUpdateAppImageReplacesAppImage(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, updatesDirName, "1.3.0-x86_64.AppImage")
	writePendingUpdateBytes(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, Kind: installKindAppImage,
	}, []byte("new appimage bytes"))

	targetDir := t.TempDir()
	appImage := filepath.Join(targetDir, "ZeoKeel-1.2.0.AppImage")
	if err := os.WriteFile(appImage, []byte("old appimage bytes"), 0o644); err != nil {
		t.Fatalf("write fake AppImage: %v", err)
	}
	t.Setenv("APPIMAGE", appImage)

	replacer := &fakeReplacer{current: "/opt/zeokeel/zeokeel"}
	applied, err := applyPendingUpdate(dataDir, replacer, nil)
	if err != nil {
		t.Fatalf("applyPendingUpdate: %v", err)
	}
	if !applied {
		t.Fatal("applyPendingUpdate = false, want applied")
	}
	requireFileContent(t, appImage, "new appimage bytes")
	info, err := os.Stat(appImage)
	if err != nil {
		t.Fatalf("stat replaced AppImage: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Fatalf("replaced AppImage mode = %o, want 755", perm)
	}
	if len(replacer.replaced) != 0 {
		t.Fatalf("binary replacer ran for an AppImage install: %v", replacer.replaced)
	}
	if _, err := os.Stat(pendingJSONPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("pending.json still exists after apply: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged asset still exists after apply: %v", err)
	}
}

// TestApplyPendingUpdateAppImageWithoutEnvFallsBackToBinary covers a build that
// scheduled an AppImage but was not started from one ($APPIMAGE unset/empty).
func TestApplyPendingUpdateAppImageWithoutEnvFallsBackToBinary(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, updatesDirName, "1.3.0-x86_64.AppImage")
	writePendingUpdateBytes(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, Kind: installKindAppImage,
	}, []byte("new appimage bytes"))
	t.Setenv("APPIMAGE", "")

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
}

// TestApplyPendingUpdateAppImageFailureKeepsPlan covers a failed AppImage swap:
// the plan and the staged asset survive for the next start.
func TestApplyPendingUpdateAppImageFailureKeepsPlan(t *testing.T) {
	dataDir := t.TempDir()
	staged := filepath.Join(dataDir, updatesDirName, "1.3.0-x86_64.AppImage")
	writePendingUpdateBytes(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, Kind: installKindAppImage,
	}, []byte("new appimage bytes"))
	// A directory cannot be replaced by a file rename, so the swap fails.
	appImageDir := filepath.Join(t.TempDir(), "ZeoKeel.AppImage")
	if err := os.MkdirAll(appImageDir, 0o755); err != nil {
		t.Fatalf("mkdir fake AppImage: %v", err)
	}
	t.Setenv("APPIMAGE", appImageDir)

	replacer := &fakeReplacer{current: "/opt/zeokeel/zeokeel"}
	applied, err := applyPendingUpdate(dataDir, replacer, nil)
	if err == nil {
		t.Fatal("applyPendingUpdate(AppImage swap failure) = nil error")
	}
	if applied {
		t.Fatal("applyPendingUpdate(AppImage swap failure) reported applied")
	}
	if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
		t.Fatalf("pending.json removed after AppImage failure: %v", statErr)
	}
	if _, statErr := os.Stat(staged); statErr != nil {
		t.Fatalf("staged asset removed after AppImage failure: %v", statErr)
	}
}

// TestApplyPendingUpdateArchiveTarGzInstallsTree covers the archive kind on
// Windows/Linux: the extracted app dir replaces the files next to the running
// executable, and the running executable goes through the rename dance.
func TestApplyPendingUpdateArchiveTarGzInstallsTree(t *testing.T) {
	dataDir := t.TempDir()
	archiveBody := buildTarGz(t, []testArchiveEntry{
		{name: "zeokeel-app/", dir: true},
		{name: "zeokeel-app/zeokeel", body: "new binary", mode: 0o755},
		{name: "zeokeel-app/new.dat", body: "new-data"},
	})
	staged := filepath.Join(dataDir, updatesDirName, "1.3.0-windows.tar.gz")
	writePendingUpdateBytes(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, Kind: installKindArchive,
	}, archiveBody)

	installDir := filepath.Join(t.TempDir(), "ZeoKeel")
	current := writeFakeExecutable(t, installDir, "zeokeel", "old binary")
	if err := os.WriteFile(filepath.Join(installDir, "keep.dat"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}

	replacer := &fakeReplacer{current: current}
	applied, err := applyPendingUpdate(dataDir, replacer, nil)
	if err != nil {
		t.Fatalf("applyPendingUpdate: %v", err)
	}
	if !applied {
		t.Fatal("applyPendingUpdate = false, want applied")
	}
	requireFileContent(t, current, "new binary")
	requireFileContent(t, current+installBackupSuffix, "old binary")
	requireFileContent(t, filepath.Join(installDir, "new.dat"), "new-data")
	requireFileContent(t, filepath.Join(installDir, "keep.dat"), "keep")
	if len(replacer.replaced) != 0 {
		t.Fatalf("binary replacer ran for an archive install: %v", replacer.replaced)
	}
	if _, err := os.Stat(pendingJSONPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("pending.json still exists after apply: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged asset still exists after apply: %v", err)
	}
}

// TestApplyPendingUpdateArchiveZipInstallsTree covers zip archives.
func TestApplyPendingUpdateArchiveZipInstallsTree(t *testing.T) {
	dataDir := t.TempDir()
	archiveBody := buildZip(t, []testArchiveEntry{
		{name: "zeokeel-app", dir: true},
		{name: "zeokeel-app/zeokeel", body: "new zip binary", mode: 0o755},
		{name: "zeokeel-app/zip.dat", body: "zip-data"},
	})
	staged := filepath.Join(dataDir, updatesDirName, "1.3.0-win.zip")
	writePendingUpdateBytes(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, Kind: installKindArchive,
	}, archiveBody)

	installDir := filepath.Join(t.TempDir(), "ZeoKeel")
	current := writeFakeExecutable(t, installDir, "zeokeel", "old binary")

	applied, err := applyPendingUpdate(dataDir, &fakeReplacer{current: current}, nil)
	if err != nil {
		t.Fatalf("applyPendingUpdate: %v", err)
	}
	if !applied {
		t.Fatal("applyPendingUpdate = false, want applied")
	}
	requireFileContent(t, current, "new zip binary")
	requireFileContent(t, filepath.Join(installDir, "zip.dat"), "zip-data")
	if _, err := os.Stat(pendingJSONPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("pending.json still exists after apply: %v", err)
	}
}

// TestApplyPendingUpdateArchiveUnsupportedLayoutKeepsPlan covers an archive
// whose root is not the expected single app directory: the plan and staged file
// stay for a retry and nothing is installed.
func TestApplyPendingUpdateArchiveUnsupportedLayoutKeepsPlan(t *testing.T) {
	dataDir := t.TempDir()
	archiveBody := buildTarGz(t, []testArchiveEntry{
		{name: "a/", dir: true},
		{name: "a/file-a", body: "a"},
		{name: "b/", dir: true},
		{name: "b/file-b", body: "b"},
	})
	staged := filepath.Join(dataDir, updatesDirName, "1.3.0-bad-layout.tar.gz")
	writePendingUpdateBytes(t, dataDir, pendingInstallFile{
		Version: "1.3.0", Path: staged, Kind: installKindArchive,
	}, archiveBody)

	installDir := filepath.Join(t.TempDir(), "ZeoKeel")
	current := writeFakeExecutable(t, installDir, "zeokeel", "old binary")

	applied, err := applyPendingUpdate(dataDir, &fakeReplacer{current: current}, nil)
	if err == nil {
		t.Fatal("applyPendingUpdate(unsupported archive layout) = nil error")
	}
	if !strings.Contains(err.Error(), "unsupported archive layout") {
		t.Fatalf("error %q does not mention the unsupported layout", err)
	}
	if applied {
		t.Fatal("applyPendingUpdate(unsupported archive layout) reported applied")
	}
	requireFileContent(t, current, "old binary")
	if _, statErr := os.Stat(pendingJSONPath(dataDir)); statErr != nil {
		t.Fatalf("pending.json removed after layout failure: %v", statErr)
	}
	if _, statErr := os.Stat(staged); statErr != nil {
		t.Fatalf("staged asset removed after layout failure: %v", statErr)
	}
}

// TestInstallDarwinBundleReplacesBundle exercises the macOS bundle swap on any
// OS: the old .app is moved aside, the extracted one moved in, and the backup
// is kept for the still-running process (removed at the next start).
func TestInstallDarwinBundleReplacesBundle(t *testing.T) {
	parent := t.TempDir()
	bundleRoot := filepath.Join(parent, "ZeoKeel.app")
	current := writeFakeExecutable(t, filepath.Join(bundleRoot, "Contents", "MacOS"), "zeokeel", "old mac binary")
	if err := os.MkdirAll(filepath.Join(bundleRoot, "Contents", "Resources"), 0o755); err != nil {
		t.Fatalf("mkdir resources: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "Contents", "Resources", "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatalf("write old resource: %v", err)
	}

	archiveBody := buildTarGz(t, []testArchiveEntry{
		{name: "ZeoKeel.app/", dir: true},
		{name: "ZeoKeel.app/Contents/", dir: true},
		{name: "ZeoKeel.app/Contents/MacOS/", dir: true},
		{name: "ZeoKeel.app/Contents/MacOS/zeokeel", body: "new mac binary", mode: 0o755},
		{name: "ZeoKeel.app/Contents/Info.plist", body: "new plist"},
	})
	staged := filepath.Join(t.TempDir(), "1.3.0-darwin.tar.gz")
	if err := os.WriteFile(staged, archiveBody, 0o644); err != nil {
		t.Fatalf("write staged archive: %v", err)
	}

	if err := installDarwinBundle(staged, current); err != nil {
		t.Fatalf("installDarwinBundle: %v", err)
	}
	requireFileContent(t, current, "new mac binary")
	requireFileContent(t, filepath.Join(bundleRoot, "Contents", "Info.plist"), "new plist")
	if _, err := os.Stat(filepath.Join(bundleRoot, "Contents", "Resources", "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old bundle content survived the swap: %v", err)
	}
	backup := bundleRoot + installBackupSuffix
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("app bundle backup missing after the swap (needed by the running process): %v", err)
	}
	// The next start removes the backup the old process no longer needs.
	cleanupStaleAppBundleBackup(current, nil)
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("app bundle backup survived the next-start cleanup: %v", err)
	}
}

// TestInstallDarwinBundleUnsupportedLayoutKeepsOldBundle covers an archive that
// is not a single .app: the running bundle is left untouched.
func TestInstallDarwinBundleUnsupportedLayoutKeepsOldBundle(t *testing.T) {
	parent := t.TempDir()
	bundleRoot := filepath.Join(parent, "ZeoKeel.app")
	current := writeFakeExecutable(t, filepath.Join(bundleRoot, "Contents", "MacOS"), "zeokeel", "old mac binary")

	archiveBody := buildTarGz(t, []testArchiveEntry{
		{name: "a/", dir: true},
		{name: "a/file", body: "a"},
		{name: "b/", dir: true},
		{name: "b/file", body: "b"},
	})
	staged := filepath.Join(t.TempDir(), "bad-darwin.tar.gz")
	if err := os.WriteFile(staged, archiveBody, 0o644); err != nil {
		t.Fatalf("write staged archive: %v", err)
	}

	if err := installDarwinBundle(staged, current); err == nil {
		t.Fatal("installDarwinBundle(unsupported layout) = nil error")
	}
	requireFileContent(t, current, "old mac binary")
	if _, err := os.Stat(bundleRoot); err != nil {
		t.Fatalf("old bundle removed after a failed install: %v", err)
	}
}

// TestDarwinAppBundleRootDerivesRoot pins the .app root derivation used by the
// macOS installer.
func TestDarwinAppBundleRootDerivesRoot(t *testing.T) {
	bundleRoot := filepath.Join(string(filepath.Separator), "Applications", "ZeoKeel.app")
	executable := filepath.Join(bundleRoot, "Contents", "MacOS", "zeokeel")
	got, err := darwinAppBundleRoot(executable)
	if err != nil {
		t.Fatalf("darwinAppBundleRoot: %v", err)
	}
	if got != bundleRoot {
		t.Fatalf("darwinAppBundleRoot = %q, want %q", got, bundleRoot)
	}
	if _, err := darwinAppBundleRoot(filepath.Join(string(filepath.Separator), "opt", "zeokeel", "zeokeel")); err == nil {
		t.Fatal("darwinAppBundleRoot accepted an executable outside a .app bundle")
	}
	if _, err := darwinAppBundleRoot("  "); err == nil {
		t.Fatal("darwinAppBundleRoot accepted an empty path")
	}
}

// TestExtractArchiveRejectsTraversal covers tar.gz and zip path traversal.
func TestExtractArchiveRejectsTraversal(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		body []byte
	}{
		{name: "tar.gz", file: "evil.tar.gz", body: buildTarGz(t, []testArchiveEntry{
			{name: "app/", dir: true},
			{name: "../escape.txt", body: "escaped"},
		})},
		{name: "zip", file: "evil.zip", body: buildZip(t, []testArchiveEntry{
			{name: "app/", dir: true},
			{name: "../escape.zip.txt", body: "escaped"},
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			staged := filepath.Join(dir, tc.file)
			if err := os.WriteFile(staged, tc.body, 0o644); err != nil {
				t.Fatalf("write staged archive: %v", err)
			}
			installDir := filepath.Join(dir, "app")
			current := writeFakeExecutable(t, installDir, "zeokeel", "old binary")

			err := installArchive(staged, current)
			if err == nil {
				t.Fatal("installArchive(traversal entry) = nil error")
			}
			if !strings.Contains(err.Error(), "escapes") {
				t.Fatalf("error %q does not mention the escaping entry", err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "escape.txt")); !os.IsNotExist(statErr) {
				t.Fatalf("traversal entry was extracted: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "escape.zip.txt")); !os.IsNotExist(statErr) {
				t.Fatalf("traversal entry was extracted: %v", statErr)
			}
			requireFileContent(t, current, "old binary")
		})
	}
}

// TestInstallArchiveUnsupportedFormat covers an asset that is neither tar.gz nor
// zip: a readable error and no install.
func TestInstallArchiveUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "update.bin")
	if err := os.WriteFile(staged, []byte("not an archive"), 0o644); err != nil {
		t.Fatalf("write staged asset: %v", err)
	}
	current := writeFakeExecutable(t, filepath.Join(dir, "app"), "zeokeel", "old binary")
	err := installArchive(staged, current)
	if err == nil {
		t.Fatal("installArchive(unsupported format) = nil error")
	}
	if !strings.Contains(err.Error(), "unsupported archive format") {
		t.Fatalf("error %q does not mention the unsupported format", err)
	}
	requireFileContent(t, current, "old binary")
}

// TestReplaceExecutableWithRenameDance covers the Windows-safe replacement
// helper: the old executable is kept as <exe>.old and the new one is in place.
func TestReplaceExecutableWithRenameDance(t *testing.T) {
	dir := t.TempDir()
	current := writeFakeExecutable(t, dir, "zeokeel", "old binary")
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(staged, []byte("new binary"), 0o755); err != nil {
		t.Fatalf("write staged asset: %v", err)
	}

	if err := replaceExecutableWithRenameDance(current, staged); err != nil {
		t.Fatalf("replaceExecutableWithRenameDance: %v", err)
	}
	requireFileContent(t, current, "new binary")
	requireFileContent(t, current+installBackupSuffix, "old binary")
}

// TestReplaceExecutableWithRenameDanceRestoresOnFailure covers the rollback: a
// failed copy puts the old executable back, so the app stays runnable.
func TestReplaceExecutableWithRenameDanceRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	current := writeFakeExecutable(t, dir, "zeokeel", "old binary")
	missing := filepath.Join(dir, "missing-staged-update")

	if err := replaceExecutableWithRenameDance(current, missing); err == nil {
		t.Fatal("replaceExecutableWithRenameDance(missing staged) = nil error")
	}
	requireFileContent(t, current, "old binary")
	if _, err := os.Stat(current + installBackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("executable backup survived a rollback: %v", err)
	}
}

// TestCleanupOldExecutableBackup covers the next-start cleanup of the *.old
// backup owned by the rename dance.
func TestCleanupOldExecutableBackup(t *testing.T) {
	dir := t.TempDir()
	current := writeFakeExecutable(t, dir, "zeokeel", "running binary")
	backup := current + installBackupSuffix
	if err := os.WriteFile(backup, []byte("previous binary"), 0o755); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	cleanupOldExecutableBackup(current, nil)
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("executable backup was not removed: %v", err)
	}
	requireFileContent(t, current, "running binary")

	// Idempotent: a second start (or no backup at all) is a no-op.
	cleanupOldExecutableBackup(current, nil)
	cleanupOldExecutableBackup("", nil)
}

// TestApplyPendingUpdateOnStartupKeepsBrokenPlanNonFatal pins the startup
// wrapper: a broken plan logs and returns; it never panics or removes the plan.
func TestApplyPendingUpdateOnStartupKeepsBrokenPlanNonFatal(t *testing.T) {
	dataDir := t.TempDir()
	updates := filepath.Join(dataDir, updatesDirName)
	if err := os.MkdirAll(updates, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	if err := os.WriteFile(pendingJSONPath(dataDir), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write broken pending.json: %v", err)
	}

	logger := &trayRecordingLogger{}
	applyPendingUpdateOnStartup(dataDir, "dev", logger)
	if _, err := os.Stat(pendingJSONPath(dataDir)); err != nil {
		t.Fatalf("broken pending plan removed by startup wrapper: %v", err)
	}
	if !strings.Contains(logger.text(), "plan kept") {
		t.Fatalf("startup wrapper did not log the kept plan: %q", logger.text())
	}
}
