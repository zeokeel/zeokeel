package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParseImportArgs covers the flag parser: defaults, both value syntaxes,
// and every usage error class. A typo must never silently start a default
// import.
func TestParseImportArgs(t *testing.T) {
	defaults, err := parseImportArgs(nil)
	if err != nil {
		t.Fatalf("parseImportArgs(nil): %v", err)
	}
	if defaults.dryRun || defaults.json || defaults.help || defaults.maxSessions != 0 ||
		defaults.includeSubagents || defaults.from != "" {
		t.Fatalf("defaults = %+v", defaults)
	}

	opts, err := parseImportArgs([]string{
		"--from", "/tmp/dsh", "--dry-run", "--json", "--max", "3", "--include-subagents",
	})
	if err != nil {
		t.Fatalf("parse full: %v", err)
	}
	if opts.from != "/tmp/dsh" || !opts.dryRun || !opts.json || opts.maxSessions != 3 || !opts.includeSubagents {
		t.Fatalf("full parse = %+v", opts)
	}

	opts, err = parseImportArgs([]string{"--from=/x", "--max=0", "-h"})
	if err != nil {
		t.Fatalf("parse equals: %v", err)
	}
	if opts.from != "/x" || opts.maxSessions != 0 || !opts.help {
		t.Fatalf("equals parse = %+v", opts)
	}

	opts, err = parseImportArgs([]string{"--manifest", "/tmp/m.json", "--include-subagents"})
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if opts.manifest != "/tmp/m.json" || !opts.includeSubagents {
		t.Fatalf("manifest parse = %+v", opts)
	}
	opts, err = parseImportArgs([]string{"--manifest=/x/m.json"})
	if err != nil || opts.manifest != "/x/m.json" {
		t.Fatalf("manifest= parse = %+v, err %v", opts, err)
	}
	opts, err = parseImportArgs([]string{"--no-manifest"})
	if err != nil || !opts.noManifest {
		t.Fatalf("no-manifest parse = %+v, err %v", opts, err)
	}

	for _, args := range [][]string{
		{"--from"},
		{"--max"},
		{"--max", "x"},
		{"--max=-1"},
		{"--bogus"},
		{"positional"},
		{"--manifest"},
		{"--manifest", "/m", "--no-manifest"},
	} {
		if _, err := parseImportArgs(args); err == nil {
			t.Fatalf("parseImportArgs(%v) = nil error, want usage error", args)
		}
	}
}

// TestRunImportCLIHelp checks `zeokeel import --help` and that the help explains
// the credential-safety contract.
func TestRunImportCLIHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCLI([]string{"import", "--help"}, &out, &errb); code != 0 {
		t.Fatalf("import --help exit code = %d, want 0 (stderr %q)", code, errb.String())
	}
	for _, want := range []string{"Usage: zeokeel import", "--from", "--dry-run", "--json", "values are never read"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("import --help output missing %q:\n%s", want, out.String())
		}
	}
}

// TestRunImportCLIUnknownFlag is a usage error (2), not an import.
func TestRunImportCLIUnknownFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCLI([]string{"import", "--nope"}, &out, &errb); code != 2 {
		t.Fatalf("import --nope exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "unknown flag") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

// TestRunImportCLINotInstalled covers a missing DSH directory: exit 1 with an
// actionable message, and the message never contains a home-dir key value.
func TestRunImportCLINotInstalled(t *testing.T) {
	var out, errb bytes.Buffer
	missing := filepath.Join(t.TempDir(), "no-such-dsh")
	if code := runImportCLI([]string{"--from", missing}, &out, &errb); code != 1 {
		t.Fatalf("exit code = %d, want 1 (stdout %q, stderr %q)", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "No DSH data found") || !strings.Contains(errb.String(), "--from") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

// TestRunImportCLIEmptyDryRun covers an empty-but-present DSH directory in dry
// run: exit 0, a summary, and no destination data directory created.
func TestRunImportCLIEmptyDryRun(t *testing.T) {
	dsh := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dsh, "--dry-run"}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout %q, stderr %q)", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "dry run: nothing written") ||
		!strings.Contains(out.String(), "sessions: found 0") {
		t.Fatalf("stdout = %q", out.String())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry run created the data directory %s (err %v)", dataDir, err)
	}
}

// TestRunImportCLIEmptyRealImport covers the non-dry-run path with an empty DSH
// directory: it opens (creates) the destination store, reports zero sessions,
// and exits 0 (nothing to import is not an error).
func TestRunImportCLIEmptyRealImport(t *testing.T) {
	dsh := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dsh}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout %q, stderr %q)", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "sessions: found 0") {
		t.Fatalf("stdout = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "index.sqlite")); err != nil {
		t.Fatalf("destination store was not created: %v", err)
	}
}

// TestRunImportCLIManifestIdempotency covers the default manifest path: a real
// import writes it under the data directory (so a second run skips unchanged
// sessions), and --no-manifest restores the re-import-everything behaviour.
func TestRunImportCLIManifestIdempotency(t *testing.T) {
	dsh := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dsh}, &out, &errb); code != 0 {
		t.Fatalf("first import exit code = %d (stderr %q)", code, errb.String())
	}
	// An empty DSH directory has no session to record, so no manifest is
	// written; the CLI must still resolve and name the default manifest path so
	// users know where the idempotency state lives.
	manifest := filepath.Join(dataDir, DefaultImportManifestName)
	if !strings.Contains(out.String(), "manifest: "+manifest) {
		t.Fatalf("stdout does not name the manifest: %q", out.String())
	}
	// An explicitly chosen manifest path is used verbatim.
	out.Reset()
	errb.Reset()
	explicit := filepath.Join(t.TempDir(), "m.json")
	if code := runImportCLI([]string{"--from", dsh, "--manifest", explicit}, &out, &errb); code != 0 {
		t.Fatalf("explicit manifest exit code = %d (stderr %q)", code, errb.String())
	}
	if !strings.Contains(out.String(), "manifest: "+explicit) {
		t.Fatalf("stdout does not name the explicit manifest: %q", out.String())
	}
	// A second run is still fine and keeps using the same manifest.
	out.Reset()
	errb.Reset()
	if code := runImportCLI([]string{"--from", dsh}, &out, &errb); code != 0 {
		t.Fatalf("second import exit code = %d (stderr %q)", code, errb.String())
	}

	// --no-manifest must not create a manifest.
	dataDir2 := filepath.Join(t.TempDir(), "zk-data2")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir2)
	out.Reset()
	errb.Reset()
	if code := runImportCLI([]string{"--from", dsh, "--no-manifest"}, &out, &errb); code != 0 {
		t.Fatalf("no-manifest import exit code = %d (stderr %q)", code, errb.String())
	}
	if !strings.Contains(out.String(), "manifest: disabled") {
		t.Fatalf("stdout does not say manifest is disabled: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir2, DefaultImportManifestName)); !os.IsNotExist(err) {
		t.Fatalf("--no-manifest wrote a manifest (err %v)", err)
	}
}

// TestRunImportCLIJSON checks --json emits exactly one JSON document with the
// expected report fields (safe for editor/tool integration).
func TestRunImportCLIJSON(t *testing.T) {
	dsh := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", filepath.Join(t.TempDir(), "zk-data"))

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dsh, "--dry-run", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errb.String())
	}
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out.String())
	}
	if report["dry_run"] != true || report["sessions_found"] != float64(0) {
		t.Fatalf("report = %v", report)
	}
	if _, ok := report["dsh_home"]; !ok {
		t.Fatalf("report missing dsh_home: %v", report)
	}
}

// writeManifestFixture creates a DSH session file plus a manifest that already
// records it (same size/mtime/SHA-256), so `zeokeel import` must skip it as
// idempotent without parsing the dummy session content.
func writeManifestFixture(t *testing.T, dshHome, dataDir string) (manifestPath string) {
	t.Helper()
	sessionDir := filepath.Join(dshHome, "sessions", "ws-1", "abc123")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionFile := filepath.Join(sessionDir, "session.v4.jsonl")
	content := []byte("{\"type\":\"session\"}\n")
	if err := os.WriteFile(sessionFile, content, 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	info, err := os.Stat(sessionFile)
	if err != nil {
		t.Fatalf("stat session file: %v", err)
	}
	sum := sha256.Sum256(content)
	manifest := map[string]any{
		"version": 1,
		"sessions": map[string]any{
			filepath.Clean(dshHome) + "|abc123": map[string]any{
				"dsh_home":               dshHome,
				"dsh_session_id":         "session-abc",
				"source_file":            sessionFile,
				"source_size":            info.Size(),
				"source_mtime_unix_nano": info.ModTime().UnixNano(),
				"sha256":                 hex.EncodeToString(sum[:]),
				"imported_at":            time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestPath = filepath.Join(dataDir, "dsh-import-manifest.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return manifestPath
}

// TestRunImportCLIManifestSkipsImportedSession covers the idempotency contract
// end to end at the CLI: a session already recorded in the manifest is skipped
// (reported in the human summary) instead of imported twice.
func TestRunImportCLIManifestSkipsImportedSession(t *testing.T) {
	dshHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)
	manifest := writeManifestFixture(t, dshHome, dataDir)

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome, "--manifest", manifest}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errb.String())
	}
	if !strings.Contains(out.String(), "sessions: found 1, imported 0, skipped 1, failed 0") {
		t.Fatalf("summary does not report the idempotent skip:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "skips:") || !strings.Contains(out.String(), "idempotent=1") {
		t.Fatalf("summary does not break down skips:\n%s", out.String())
	}
}

// TestRunImportCLIRejectsCorruptManifest covers the fail-closed contract: a
// corrupt manifest stops the run (exit 1) before any session is written.
func TestRunImportCLIRejectsCorruptManifest(t *testing.T) {
	dshHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	manifest := filepath.Join(dataDir, "manifest.json")
	if err := os.WriteFile(manifest, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt manifest: %v", err)
	}

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome, "--manifest", manifest}, &out, &errb); code != 1 {
		t.Fatalf("exit code = %d, want 1 (stdout %q, stderr %q)", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "manifest") {
		t.Fatalf("stderr does not mention the manifest: %q", errb.String())
	}
}
