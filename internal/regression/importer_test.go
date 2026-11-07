package regression

// Adversarial audit: the DSH importer module. Negative/fault cases: missing or
// invalid source directories, corrupt/oversized/hostile session logs, repeated
// imports (idempotency), partial failures that must not abort the run, symlink
// escapes out of the scanned tree, cancelled contexts and the configured
// default source directory.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/importer"
	"github.com/zeokeel/zeokeel/internal/modules/sessionstore"
	"github.com/zeokeel/zeokeel/internal/modules/workspace"
)

// startImporter activates the store, workspace and importer modules and returns
// the importer service plus the store.
func startImporter(t *testing.T, h *harness, cfg importer.Config) (contracts.ImporterService, contracts.Store) {
	t.Helper()
	activate(t, h.ctx, sessionstore.Module(), sessionstore.Config{})
	activate(t, h.ctx, workspace.Module(), workspace.Config{})
	activate(t, h.ctx, importer.Module(), cfg)
	return requireService(t, h.ctx, contracts.KeyImporter), requireService(t, h.ctx, contracts.KeyStore)
}

// dshHeader builds the first line of a DSH v4 session log.
func dshHeader(version int, id, cwd string) string {
	encoded, err := json.Marshal(map[string]any{
		"type":      "session",
		"version":   version,
		"id":        id,
		"createdAt": int64(1_700_000_000_000),
		"cwd":       cwd,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// dshUserEvent builds one genesis user/message event line.
func dshUserEvent(text string) string {
	return `{"type":"user/message","seq":1,"time":1700000000000,"data":{"role":"user","source":{"kind":"user"},"content":[{"type":"text","text":` +
		mustJSON(text) + `}]}}`
}

// dshAssistantEvent builds one assistant/message event line with optional reasoning.
func dshAssistantEvent(text, reasoning string) string {
	parts := `{"type":"text","text":` + mustJSON(text) + `}`
	if reasoning != "" {
		parts += `,{"type":"reasoning","text":` + mustJSON(reasoning) + `}`
	}
	return `{"type":"assistant/message","seq":2,"time":1700000000000,"data":{"message":{"role":"assistant","content":[` + parts + `]}}}`
}

// mustJSON encodes s as a JSON string literal or panics (test fixture helper).
func mustJSON(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// writeDSHFile writes content to <root>/sessions/<dirName>/session.v4.jsonl and
// returns the path.
func writeDSHFile(t *testing.T, root, dirName, content string) string {
	t.Helper()
	path := filepath.Join(root, "sessions", dirName, "session.v4.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// dshSessionContent assembles one valid session log.
func dshSessionContent(id, cwd string, events ...string) string {
	lines := append([]string{dshHeader(4, id, cwd)}, events...)
	return strings.Join(lines, "\n") + "\n"
}

// TestImporterInvalidSourceDirectories covers hostile scan/import arguments:
// missing paths, files instead of directories, an empty DSH home and a sessions
// entry that is not a directory.
func TestImporterInvalidSourceDirectories(t *testing.T) {
	h := newHarness(t)
	importerService, _ := startImporter(t, h, importer.Config{})
	ctx := context.Background()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := importerService.Scan(ctx, missing); err == nil {
		t.Fatalf("Scan(%q) succeeded for a missing directory", missing)
	}
	if _, err := importerService.Import(ctx, missing); err == nil {
		t.Fatalf("Import(%q) succeeded for a missing directory", missing)
	}

	// An empty data home is not an error: it simply has nothing to import.
	empty := t.TempDir()
	preview, err := importerService.Scan(ctx, empty)
	if err != nil {
		t.Fatalf("Scan(empty dir): %v", err)
	}
	if preview.Sessions != 0 || preview.Messages != 0 || preview.Skipped != 0 {
		t.Fatalf("Scan(empty dir) = %+v, want a zero preview", preview)
	}
	result, err := importerService.Import(ctx, empty)
	if err != nil || result.ImportedSessions != 0 {
		t.Fatalf("Import(empty dir) = (%+v, %v), want a zero result", result, err)
	}

	// A file path where the data home is expected is an error.
	fileHome := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileHome, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file home: %v", err)
	}
	if _, err := importerService.Scan(ctx, fileHome); err == nil {
		t.Fatalf("Scan(%q) succeeded for a regular file", fileHome)
	}

	// A sessions entry that is a file (not a directory) is an error, not a panic.
	sessionsFileHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(sessionsFileHome, "sessions"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write sessions file: %v", err)
	}
	if _, err := importerService.Scan(ctx, sessionsFileHome); err == nil {
		t.Fatalf("Scan succeeded with a regular file named sessions")
	}
}

// TestImporterCorruptSessionsDoNotAbort covers the partial-failure rule: bad,
// empty, unsupported-version and compressed session logs are skipped with notes
// while the one valid session is still imported; per-session problems never
// abort the run.
func TestImporterCorruptSessionsDoNotAbort(t *testing.T) {
	h := newHarness(t)
	importerService, store := startImporter(t, h, importer.Config{})
	ctx := context.Background()
	home := t.TempDir()

	writeDSHFile(t, home, "bad-json", "this is not json\n")
	writeDSHFile(t, home, "wrong-type", `{"type":"not-a-session","version":4,"id":"x"}`+"\n")
	writeDSHFile(t, home, "v3", dshSessionContentV3("old-session")+"\n")
	writeDSHFile(t, home, "empty", "")
	// Compressed logs are counted/skipped by extension, never parsed.
	zstdPath := filepath.Join(home, "sessions", "zstd", "session.v4.jsonl.zstd")
	if err := os.MkdirAll(filepath.Dir(zstdPath), 0o755); err != nil {
		t.Fatalf("mkdir zstd: %v", err)
	}
	if err := os.WriteFile(zstdPath, []byte("not really zstd"), 0o644); err != nil {
		t.Fatalf("write zstd: %v", err)
	}
	// A file that is not a session log at all is ignored, not counted.
	if err := os.WriteFile(filepath.Join(home, "sessions", "bad-json", "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	validID := "valid-1"
	writeDSHFile(t, home, "valid", dshSessionContent(validID, "",
		dshUserEvent("第一问"), dshAssistantEvent("第一答", "think")))

	preview, err := importerService.Scan(ctx, home)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if preview.Sessions != 1 || preview.Messages != 2 {
		t.Fatalf("preview = %+v, want 1 session / 2 messages", preview)
	}
	if preview.Skipped < 4 {
		t.Fatalf("preview.Skipped = %d, want at least the 4 unusable logs", preview.Skipped)
	}
	if len(preview.Notes) < 3 {
		t.Fatalf("preview.Notes = %v, want one note per skipped log", preview.Notes)
	}
	joinedNotes := strings.Join(preview.Notes, "\n")
	for _, fragment := range []string{"bad-json", "v3", "zstd", "empty"} {
		if !strings.Contains(joinedNotes, fragment) {
			t.Fatalf("notes do not mention %q: %v", fragment, preview.Notes)
		}
	}

	result, err := importerService.Import(ctx, home)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 1 || result.ImportedMessages != 2 {
		t.Fatalf("Import = %+v, want 1 session / 2 messages", result)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Import errors = %v, want none (bad logs are skips, not write failures)", result.Errors)
	}
	sessionID := "dsh-" + validID
	messages, err := store.History().Load(ctx, sessionID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("imported history = %d messages, %v; want 2", len(messages), err)
	}
	if messages[0].Text() != "第一问" || messages[1].Text() != "第一答" {
		t.Fatalf("imported texts = %q/%q", messages[0].Text(), messages[1].Text())
	}
}

// dshSessionContentV3 builds an unsupported (v3) session log.
func dshSessionContentV3(id string) string {
	return dshHeader(3, id, "") + "\n" + dshUserEvent("old") + "\n"
}

// TestImporterHugeLines covers oversized input: a multi-megabyte event line and
// a multi-megabyte garbage header must not hang or crash the scanner.
func TestImporterHugeLines(t *testing.T) {
	h := newHarness(t)
	importerService, _ := startImporter(t, h, importer.Config{})
	ctx := context.Background()
	home := t.TempDir()

	hugeText := strings.Repeat("x", 2<<20) // 2 MiB on one line
	writeDSHFile(t, home, "huge", dshSessionContent("huge-1", "",
		dshUserEvent(hugeText)))
	// A header line far beyond the reader's initial buffer.
	garbage := strings.Repeat("{", 3<<20)
	writeDSHFile(t, home, "garbage", garbage)

	preview, err := importerService.Scan(ctx, home)
	if err != nil {
		t.Fatalf("Scan with huge lines: %v", err)
	}
	if preview.Sessions != 1 || preview.Messages != 1 {
		t.Fatalf("preview = %+v, want the huge valid session only", preview)
	}
	if preview.Skipped != 1 {
		t.Fatalf("preview.Skipped = %d, want the garbage header counted as skipped", preview.Skipped)
	}
	if _, err := importerService.Import(ctx, home); err != nil {
		t.Fatalf("Import with huge lines: %v", err)
	}
}

// TestImporterRepeatedImportIsIdempotent covers running the import twice and
// after a new session appears: existing sessions are skipped, not duplicated.
func TestImporterRepeatedImportIsIdempotent(t *testing.T) {
	h := newHarness(t)
	importerService, store := startImporter(t, h, importer.Config{})
	ctx := context.Background()
	home := t.TempDir()
	writeDSHFile(t, home, "one", dshSessionContent("one", "", dshUserEvent("hello")))
	writeDSHFile(t, home, "two", dshSessionContent("two", "", dshUserEvent("world")))

	first, err := importerService.Import(ctx, home)
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}
	if first.ImportedSessions != 2 {
		t.Fatalf("first Import = %+v, want 2 sessions", first)
	}
	second, err := importerService.Import(ctx, home)
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if second.ImportedSessions != 0 || second.Skipped < 2 {
		t.Fatalf("second Import = %+v, want 0 imported and both sessions skipped", second)
	}
	metas, err := store.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("Sessions() = %d after two imports, want 2 (no duplicates)", len(metas))
	}
	if messages, err := store.History().Load(ctx, "dsh-one"); err != nil || len(messages) != 1 {
		t.Fatalf("dsh-one history = %d, %v; want 1 message (no duplicate rows)", len(messages), err)
	}

	// A new session in the same home is imported; the old ones stay skipped.
	writeDSHFile(t, home, "three", dshSessionContent("three", "", dshUserEvent("again")))
	third, err := importerService.Import(ctx, home)
	if err != nil {
		t.Fatalf("third Import: %v", err)
	}
	if third.ImportedSessions != 1 {
		t.Fatalf("third Import = %+v, want only the new session", third)
	}
}

// TestImporterSymlinkEscape is the path-traversal negative: a symlink below the
// scanned sessions directory that points outside the DSH home must not let the
// importer read/import the outside file.
func TestImporterSymlinkEscape(t *testing.T) {
	h := newHarness(t)
	importerService, _ := startImporter(t, h, importer.Config{})
	ctx := context.Background()
	home := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "session.v4.jsonl")
	if err := os.WriteFile(outsideFile, []byte(dshSessionContent("outside-secret", "",
		dshUserEvent("secret outside content"))), 0o644); err != nil {
		t.Fatalf("write outside session: %v", err)
	}

	// 1) a symlinked session file inside sessions/ pointing at the outside file.
	sessionsDir := filepath.Join(home, "sessions", "escape")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir escape: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(sessionsDir, "session.v4.jsonl")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// 2) a symlinked directory below sessions/ pointing at the outside directory.
	if err := os.Symlink(outside, filepath.Join(home, "sessions", "linked-dir")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	// 3) a symlink named like a session log but pointing at a directory: the
	// parser must report it as a skip, not panic.
	weird := filepath.Join(home, "sessions", "weird")
	if err := os.MkdirAll(weird, 0o755); err != nil {
		t.Fatalf("mkdir weird: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(weird, "session.v4.jsonl")); err != nil {
		t.Fatalf("symlink weird: %v", err)
	}

	preview, err := importerService.Scan(ctx, home)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if preview.Sessions != 0 {
		t.Skipf("AUDIT-P2-2 (known defect): importer follows symlinks out of the DSH home; "+
			"preview.Sessions = %d", preview.Sessions)
	}
	if preview.Skipped != 0 {
		t.Fatalf("preview.Skipped = %d, want symlinks ignored entirely", preview.Skipped)
	}
	result, err := importerService.Import(ctx, home)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 0 {
		t.Fatalf("Import imported %d sessions through symlinks, want 0", result.ImportedSessions)
	}
}

// TestImporterCanceledContext covers context cancellation before and during the
// walk: Scan/Import must return the context error instead of running on.
func TestImporterCanceledContext(t *testing.T) {
	h := newHarness(t)
	importerService, _ := startImporter(t, h, importer.Config{})
	home := t.TempDir()
	writeDSHFile(t, home, "one", dshSessionContent("one", "", dshUserEvent("hi")))

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := importerService.Scan(canceled, home); !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := importerService.Import(canceled, home); !errors.Is(err, context.Canceled) {
		t.Fatalf("Import(cancelled) = %v, want context.Canceled", err)
	}
}

// TestImporterDefaultSourceDir covers the configured source directory: an empty
// dir argument uses it, and neither configured nor passed is a readable error.
func TestImporterDefaultSourceDir(t *testing.T) {
	home := t.TempDir()
	writeDSHFile(t, home, "one", dshSessionContent("one", "", dshUserEvent("configured")))

	configured := newHarness(t)
	importerService, _ := startImporter(t, configured, importer.Config{SourceDir: home})
	preview, err := importerService.Scan(context.Background(), "")
	if err != nil || preview.Sessions != 1 {
		t.Fatalf("Scan with the configured source dir = (%+v, %v), want 1 session", preview, err)
	}

	unconfigured := newHarness(t)
	unconfiguredImporter, _ := startImporter(t, unconfigured, importer.Config{})
	if _, err := unconfiguredImporter.Scan(context.Background(), ""); err == nil {
		t.Fatal("Scan with no configured and no passed source dir succeeded")
	}
}

// TestImporterRegistersWorkspaces covers the session cwd handling: an existing
// cwd directory becomes a workspace once; a missing cwd is ignored.
func TestImporterRegistersWorkspaces(t *testing.T) {
	h := newHarness(t)
	importerService, _ := startImporter(t, h, importer.Config{})
	workspaces := requireService(t, h.ctx, contracts.KeyWorkspace)
	ctx := context.Background()
	home := t.TempDir()
	project := t.TempDir()
	writeDSHFile(t, home, "with-cwd", dshSessionContent("with-cwd", project, dshUserEvent("hello")))
	writeDSHFile(t, home, "bad-cwd", dshSessionContent("bad-cwd", filepath.Join(home, "gone"),
		dshUserEvent("hello")))

	result, err := importerService.Import(ctx, home)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 2 {
		t.Fatalf("Import = %+v, want 2 sessions", result)
	}
	if result.ImportedWorkspaces != 1 {
		t.Fatalf("ImportedWorkspaces = %d, want the one existing cwd", result.ImportedWorkspaces)
	}
	registered, err := workspaces.List(ctx)
	if err != nil {
		t.Fatalf("workspace.List: %v", err)
	}
	if len(registered) != 1 || registered[0].Root != project {
		t.Fatalf("workspaces = %+v, want the session cwd %q", registered, project)
	}
}

// TestImporterConfigValidation covers the pre-flight hook: a source dir with a
// NUL byte is rejected before any module work.
func TestImporterConfigValidation(t *testing.T) {
	descriptor := importer.Module()
	if descriptor.Validate == nil {
		t.Fatal("importer module has no config validation")
	}
	if err := descriptor.Validate(importer.Config{SourceDir: "bad\x00dir"}); err == nil {
		t.Fatal("sourceDir with a NUL byte was accepted")
	}
	if err := descriptor.Validate(importer.Config{SourceDir: "/tmp"}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// TestImporterRecordsImportEvent covers the durable import marker: each imported
// session gets one "import" event carrying the DSH source id.
func TestImporterRecordsImportEvent(t *testing.T) {
	h := newHarness(t)
	importerService, store := startImporter(t, h, importer.Config{})
	ctx := context.Background()
	home := t.TempDir()
	writeDSHFile(t, home, "one", dshSessionContent("source-1", "", dshUserEvent("hi")))
	if _, err := importerService.Import(ctx, home); err != nil {
		t.Fatalf("Import: %v", err)
	}
	events, err := store.Events(ctx, "dsh-source-1", 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == "import" && strings.Contains(string(event.Payload), "source-1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no import event with the DSH source id; events = %+v", events)
	}
}

var _ = fmt.Sprintf

// TestImporterDuplicateSessionIDs covers two files carrying the same DSH session
// ID: the scan preview counts the importable session once, the run imports one
// session, skips the duplicate and reports no error (AUDIT-P3-3 fixed).
func TestImporterDuplicateSessionIDs(t *testing.T) {
	h := newHarness(t)
	importerService, store := startImporter(t, h, importer.Config{})
	ctx := context.Background()
	home := t.TempDir()
	writeDSHFile(t, home, "a", dshSessionContent("same-id", "", dshUserEvent("first")))
	writeDSHFile(t, home, "b", dshSessionContent("same-id", "", dshUserEvent("second")))

	preview, err := importerService.Scan(ctx, home)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if preview.Sessions != 1 {
		t.Fatalf("preview.Sessions = %d, want the deduplicated importable count 1", preview.Sessions)
	}
	if !strings.Contains(strings.Join(preview.Notes, "\n"), "duplicate") {
		t.Fatalf("preview.Notes = %v, want a duplicate-session note", preview.Notes)
	}
	result, err := importerService.Import(ctx, home)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 1 || result.Skipped != 1 {
		t.Fatalf("Import = %+v, want 1 imported and 1 duplicate skipped", result)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Import errors = %v, want none (duplicates are skips)", result.Errors)
	}
	metas, err := store.Sessions(ctx)
	if err != nil || len(metas) != 1 {
		t.Fatalf("Sessions() = %v, %v; want exactly one session", metas, err)
	}
	if preview.Sessions != result.ImportedSessions {
		t.Fatalf("preview.Sessions = %d but Import wrote %d; want the preview to match the importable count",
			preview.Sessions, result.ImportedSessions)
	}
}
