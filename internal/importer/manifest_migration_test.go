package importer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
)

// legacyManifestEntry is one wave-2 manifest entry (before the relative-path
// key fix): the key was "<home>|<session dir name or file base>".
type legacyManifestEntry struct {
	DSHHome             string `json:"dsh_home,omitempty"`
	SessionID           string `json:"dsh_session_id"`
	SourceFile          string `json:"source_file"`
	SourceSize          int64  `json:"source_size"`
	SHA256              string `json:"sha256"`
	ZeoKeelSessionID    string `json:"zeokeel_session_id,omitempty"`
	Title               string `json:"title,omitempty"`
	SourceMTimeUnixNano int64  `json:"source_mtime_unix_nano"`
}

// writeLegacyManifest writes a version-1 manifest using the old key scheme.
func writeLegacyManifest(t *testing.T, path, home string, sourceFile string, key string) sourceIdentity {
	t.Helper()
	identity, err := inspectSourceFile(sourceFile)
	if err != nil {
		t.Fatalf("inspectSourceFile: %v", err)
	}
	entries := map[string]legacyManifestEntry{
		key: {
			DSHHome:             home,
			SessionID:           "legacy-session",
			SourceFile:          sourceFile,
			SourceSize:          identity.Size,
			SHA256:              identity.SHA256,
			ZeoKeelSessionID:    "zk-legacy",
			SourceMTimeUnixNano: identity.ModTimeUnixNano,
		},
	}
	raw := struct {
		Version   int                            `json:"version"`
		UpdatedAt string                         `json:"updated_at"`
		Sessions  map[string]legacyManifestEntry `json:"sessions"`
	}{Version: 1, UpdatedAt: "2026-01-01T00:00:00Z", Sessions: entries}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy manifest: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write legacy manifest: %v", err)
	}
	return identity
}

// TestLegacyManifestMigratesWithoutDuplicateImport pins the P3 fix: a manifest
// written by wave 2 with the old collision key must be migrated to the
// relative-path key BEFORE the idempotency check, so upgrading does not import
// every previously imported session a second time.
func TestLegacyManifestMigratesWithoutDuplicateImport(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	sourceFile := writeFixtureSession(t, home, "--data-project--", "session-abc", "session.v4.jsonl", []string{
		headerLine(t, "session-abc", cwd, "user", 0),
		userMessageEvent(t, 1, "hello legacy"),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "hi"}}),
	})
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	// Old wave-2 key for a session-<uuid> directory used the FILE base name.
	legacyKey := filepath.Clean(home) + "|" + filepath.Base(sourceFile)
	writeLegacyManifest(t, manifestPath, home, sourceFile, legacyKey)

	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("Import with legacy manifest: %v", err)
	}
	if report.SessionsImported != 0 {
		t.Fatalf("SessionsImported = %d, want 0: the legacy manifest entry must survive the key migration", report.SessionsImported)
	}
	if report.SessionsSkipped != 1 || report.Skips[SkipIdempotent] != 1 {
		t.Fatalf("skips = %+v (imported %d), want one idempotent skip", report.Skips, report.SessionsImported)
	}

	// The manifest must now be rewritten with the relative-path key only.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read migrated manifest: %v", err)
	}
	var migrated manifestFile
	if err := json.Unmarshal(data, &migrated); err != nil {
		t.Fatalf("decode migrated manifest: %v", err)
	}
	canonicalKey := filepath.Clean(home) + "|" + "--data-project--/session-abc/" + filepath.Base(sourceFile)
	if _, ok := migrated.Sessions[canonicalKey]; !ok {
		t.Fatalf("migrated manifest keys = %v, want canonical key %q", keysOf(migrated.Sessions), canonicalKey)
	}
	if _, ok := migrated.Sessions[legacyKey]; ok {
		t.Fatalf("legacy key %q survived migration: %v", legacyKey, keysOf(migrated.Sessions))
	}

	// A second run stays idempotent (no duplicates introduced by the upgrade).
	second, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if second.SessionsImported != 0 || second.Skips[SkipIdempotent] != 1 {
		t.Fatalf("second report = %+v, want an idempotent skip", second)
	}
}

// TestLegacyBareUUIDManifestKeyMigrates pins the older bare-uuid key form
// ("<home>|<uuid>"): it must migrate too, not re-import.
func TestLegacyBareUUIDManifestKeyMigrates(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	sourceFile := writeFixtureSession(t, home, "--w--", "abc123", "session.v4.jsonl", []string{
		headerLine(t, "abc123", cwd, "user", 0),
		userMessageEvent(t, 1, "bare uuid"),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "ok"}}),
	})
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	legacyKey := filepath.Clean(home) + "|abc123"
	writeLegacyManifest(t, manifestPath, home, sourceFile, legacyKey)

	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SessionsImported != 0 || report.Skips[SkipIdempotent] != 1 {
		t.Fatalf("report = %+v, want the bare-uuid legacy entry migrated to an idempotent skip", report)
	}
}

// TestLegacyManifestUnmigratableFailsClosed pins fail-closed behaviour: an old
// entry whose source cannot be reconstructed is an invalid manifest, so the run
// stops before importing anything (never silently duplicates).
func TestLegacyManifestUnmigratableFailsClosed(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "sess-x", "session.v4.jsonl", []string{
		headerLine(t, "sess-x", cwd, "user", 0),
		userMessageEvent(t, 1, "hello"),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "hi"}}),
	})
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	// No dsh_home and no source_file: the old key cannot be migrated.
	raw := `{"version":1,"sessions":{"mystery|abc123":{"dsh_session_id":"abc123","source_size":3,"sha256":"deadbeef"}}}`
	if err := os.WriteFile(manifestPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	st := newStore(t)
	_, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath})
	if !errors.Is(err, ErrManifest) {
		t.Fatalf("Import error = %v, want ErrManifest (fail-closed)", err)
	}
	sessions, err := st.QuerySessions(store.SessionQuery{})
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("fail-closed import created %d sessions", len(sessions))
	}
}

func keysOf(m map[string]manifestEntry) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}
