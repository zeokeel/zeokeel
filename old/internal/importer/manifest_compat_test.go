package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// Legacy manifest compatibility (residual PD-04 item): wave-1/2 manifests must
// be upgraded in place on the first run (no re-import of already migrated
// sessions), and a damaged manifest must not destroy the import run or the
// recorded history: valid entries are recovered from the rolling backup, and a
// fully corrupt file is preserved before the manifest is rebuilt.

// readManifestRaw decodes a manifest file into a generic map for assertions.
func readManifestRaw(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest %s: %v", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode manifest %s: %v\n%s", path, err, data)
	}
	return raw
}

// writeManifestRaw writes a manifest JSON document verbatim.
func writeManifestRaw(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// manifestEntryFields builds the JSON entry for an imported session source.
func manifestEntryFields(t *testing.T, home, sessionFile string) (string, map[string]any) {
	t.Helper()
	identity, err := inspectSourceFile(sessionFile)
	if err != nil {
		t.Fatalf("inspectSourceFile: %v", err)
	}
	key := manifestKey(home, filepath.Join(home, sessionsDirName), sessionFile)
	return key, map[string]any{
		"dsh_home":               filepath.Clean(home),
		"dsh_session_id":         "sess-manifest-compat",
		"source_file":            sessionFile,
		"source_size":            identity.Size,
		"source_mtime_unix_nano": identity.ModTimeUnixNano,
		"sha256":                 identity.SHA256,
		"imported_at":            "2026-01-01T00:00:00Z",
		"zeokeel_session_id":     "zk-legacy-1",
		"title":                  "legacy",
	}
}

// writeManifestCompatFixture writes one simple DSH session under home and
// returns its session file.
func writeManifestCompatFixture(t *testing.T, home string) string {
	t.Helper()
	cwd := t.TempDir()
	return writeFixtureSession(t, home, "--w--", "sess-manifest-compat", "session.v4.jsonl", []string{
		headerLine(t, "sess-manifest-compat", cwd, "user", 0),
		userMessageEvent(t, 1, "hello compat"),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "hi"}}),
	})
}

// TestManifestVersionOneUpgradesWithoutReimport pins the upgrade path: a
// version-1 manifest with current-scheme keys is rewritten as the current
// version on the next run and its entries still prevent duplicate imports.
func TestManifestVersionOneUpgradesWithoutReimport(t *testing.T) {
	home := t.TempDir()
	sessionFile := writeManifestCompatFixture(t, home)
	key, entry := manifestEntryFields(t, home, sessionFile)
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	writeManifestRaw(t, manifestPath, map[string]any{
		"version":    1,
		"updated_at": "2026-01-01T00:00:00Z",
		"sessions":   map[string]any{key: entry},
	})

	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("Import with version-1 manifest: %v", err)
	}
	if report.SessionsImported != 0 || report.Skips[SkipIdempotent] != 1 {
		t.Fatalf("report = %+v, want an idempotent skip after the version upgrade", report)
	}
	migrated := readManifestRaw(t, manifestPath)
	if got := migrated["version"]; got != float64(manifestFormatVersion) {
		t.Fatalf("manifest version after upgrade = %v, want %d", got, manifestFormatVersion)
	}
	sessions, _ := migrated["sessions"].(map[string]any)
	if _, ok := sessions[key]; !ok {
		t.Fatalf("upgraded manifest lost the recorded session: %v", migrated)
	}
	// The pre-upgrade file is kept as a rolling backup before being replaced.
	backup, err := os.ReadFile(manifestPath + manifestBackupSuffix)
	if err != nil {
		t.Fatalf("version upgrade did not keep a backup: %v", err)
	}
	if !bytes.Contains(backup, []byte(`"version": 1`)) {
		t.Fatalf("backup is not the pre-upgrade manifest: %s", backup)
	}
}

// TestManifestMissingVersionTreatedAsLegacy covers manifests written before the
// version field existed (and hand-edited ones): they are upgraded rather than
// rejected, so the first run after the upgrade does not re-import everything.
func TestManifestMissingVersionTreatedAsLegacy(t *testing.T) {
	home := t.TempDir()
	sessionFile := writeManifestCompatFixture(t, home)
	_, entry := manifestEntryFields(t, home, sessionFile)
	// Wave-2 key form: "<home>|<session dir name or file base>".
	legacyKey := filepath.Clean(home) + "|" + filepath.Base(sessionFile)
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	writeManifestRaw(t, manifestPath, map[string]any{
		"sessions": map[string]any{legacyKey: entry},
	})

	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("Import with version-less manifest: %v", err)
	}
	if report.SessionsImported != 0 || report.Skips[SkipIdempotent] != 1 {
		t.Fatalf("report = %+v, want the legacy manifest migrated to an idempotent skip", report)
	}
	migrated := readManifestRaw(t, manifestPath)
	if got := migrated["version"]; got != float64(manifestFormatVersion) {
		t.Fatalf("manifest version = %v, want %d", got, manifestFormatVersion)
	}
}

// TestManifestFutureVersionFailsClosed keeps the safety valve for manifests
// written by a newer ZeoKeel: rewriting them could lose idempotency state, so
// the run stops before importing anything.
func TestManifestFutureVersionFailsClosed(t *testing.T) {
	home := t.TempDir()
	sessionFile := writeManifestCompatFixture(t, home)
	key, entry := manifestEntryFields(t, home, sessionFile)
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	writeManifestRaw(t, manifestPath, map[string]any{
		"version":  manifestFormatVersion + 1,
		"sessions": map[string]any{key: entry},
	})
	st := newStore(t)
	_, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if !errors.Is(err, ErrManifest) {
		t.Fatalf("Import error = %v, want ErrManifest for a future manifest version", err)
	}
	sessions, qerr := st.QuerySessions(store.SessionQuery{})
	if qerr != nil {
		t.Fatalf("QuerySessions: %v", qerr)
	}
	if len(sessions) != 0 {
		t.Fatalf("future-version manifest import created %d sessions", len(sessions))
	}
}

// TestManifestCorruptJSONIsRebuiltWithWarning pins the damage-tolerance
// contract: an unparsable manifest is preserved on disk, the run continues with
// a warning, and a fresh manifest is written so the next run is idempotent.
func TestManifestCorruptJSONIsRebuiltWithWarning(t *testing.T) {
	home := t.TempDir()
	sessionFile := writeManifestCompatFixture(t, home)
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	corrupt := []byte("{not json at all")
	if err := os.WriteFile(manifestPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = sessionFile

	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("a corrupt manifest must not abort the import: %v", err)
	}
	if report.SessionsImported != 1 {
		t.Fatalf("SessionsImported = %d, want 1 (report %+v)", report.SessionsImported, report)
	}
	if !warningsContain(report.Warnings, "corrupt") {
		t.Fatalf("warnings do not explain the rebuild: %+v", report.Warnings)
	}
	// The damaged bytes are preserved next to the manifest, never silently lost.
	preserved, err := filepath.Glob(manifestPath + ".corrupt-*")
	if err != nil || len(preserved) != 1 {
		t.Fatalf("preserved corrupt manifest files = %v (err %v), want exactly one", preserved, err)
	}
	data, err := os.ReadFile(preserved[0])
	if err != nil || !bytes.Equal(data, corrupt) {
		t.Fatalf("preserved manifest bytes changed (err %v)", err)
	}
	// The rebuilt manifest is valid and records the import, so a second run is
	// idempotent instead of duplicating sessions.
	rebuilt := readManifestRaw(t, manifestPath)
	if rebuilt["version"] != float64(manifestFormatVersion) {
		t.Fatalf("rebuilt manifest version = %v, want %d", rebuilt["version"], manifestFormatVersion)
	}
	second, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.SessionsImported != 0 || second.Skips[SkipIdempotent] != 1 {
		t.Fatalf("second report = %+v, want an idempotent skip", second)
	}
}

// TestManifestCorruptJSONRecoversFromBackup proves recorded sessions are not
// lost to a damaged main file: the rolling backup restores them, so already
// imported sessions are still skipped.
func TestManifestCorruptJSONRecoversFromBackup(t *testing.T) {
	home := t.TempDir()
	writeManifestCompatFixture(t, home)
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	st := newStore(t)
	opts := Options{DSHHome: home, Store: st, ManifestPath: manifestPath}
	if _, err := Import(context.Background(), opts); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// A second run rewrites the manifest and keeps the previous good content as
	// the rolling backup.
	if _, err := Import(context.Background(), opts); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if _, err := os.Stat(manifestPath + manifestBackupSuffix); err != nil {
		t.Fatalf("manifest backup not written: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("garbage{{{"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("Import with corrupt manifest + backup: %v", err)
	}
	if report.SessionsImported != 0 || report.Skips[SkipIdempotent] != 1 {
		t.Fatalf("report = %+v, want the backup-restored entry to skip the session", report)
	}
	if !warningsContain(report.Warnings, "backup") {
		t.Fatalf("warnings do not mention the backup restore: %+v", report.Warnings)
	}
	recovered := readManifestRaw(t, manifestPath)
	if recovered["version"] != float64(manifestFormatVersion) {
		t.Fatalf("rebuilt manifest version = %v, want %d", recovered["version"], manifestFormatVersion)
	}
}

// TestManifestCorruptJSONDryRunDoesNotWrite: --dry-run must stay read-only even
// when the manifest is damaged: report the warning, do not rename or rewrite.
func TestManifestCorruptJSONDryRunDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	writeManifestCompatFixture(t, home)
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	corrupt := []byte("still not json")
	if err := os.WriteFile(manifestPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Import(context.Background(), Options{DSHHome: home, DryRun: true, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("dry run with corrupt manifest: %v", err)
	}
	if report.SessionsImported != 1 {
		t.Fatalf("dry-run SessionsImported = %d, want 1", report.SessionsImported)
	}
	if !warningsContain(report.Warnings, "corrupt") {
		t.Fatalf("dry run did not warn about the corrupt manifest: %+v", report.Warnings)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(data, corrupt) {
		t.Fatalf("dry run modified the corrupt manifest (err %v)", err)
	}
	if preserved, _ := filepath.Glob(manifestPath + ".corrupt-*"); len(preserved) != 0 {
		t.Fatalf("dry run renamed the corrupt manifest: %v", preserved)
	}
}

// warningsContain reports whether any warning contains want (case-insensitive).
func warningsContain(warnings []string, want string) bool {
	for _, w := range warnings {
		if strings.Contains(strings.ToLower(w), strings.ToLower(want)) {
			return true
		}
	}
	return false
}
