package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/app"
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
		"--from", "/tmp/dsh", "--dry-run", "--json", "--max", "3", "--include-subagents", "--force",
	})
	if err != nil {
		t.Fatalf("parse full: %v", err)
	}
	if opts.from != "/tmp/dsh" || !opts.dryRun || !opts.json || opts.maxSessions != 3 || !opts.includeSubagents || !opts.force {
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
// writeValidDSHSessionFixture writes one minimal, valid DSH v4 session
// (session-<uuid> directory layout, as DSH 0.2.x produces) and returns its
// session file. The manifest is deliberately not pre-seeded: the idempotency
// test performs a real first import that records the session, then verifies
// that a second run skips it.
func writeValidDSHSessionFixture(t *testing.T, dshHome string) string {
	t.Helper()
	sessionDir := filepath.Join(dshHome, "sessions", "--tmp-ws--", "session-abc123")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	header, err := json.Marshal(map[string]any{
		"type": "session", "version": 4, "id": "session-abc123",
		"createdAt": int64(1793788819976), "cwd": "/tmp/proj", "agentPreset": "standard",
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	user, err := json.Marshal(map[string]any{
		"type": "user/message", "seq": 1, "data": map[string]any{
			"role":    "user",
			"source":  map[string]any{"kind": "user"},
			"content": []map[string]any{{"type": "text", "text": "hello from dsh"}},
		},
	})
	if err != nil {
		t.Fatalf("marshal user event: %v", err)
	}
	assistant, err := json.Marshal(map[string]any{
		"type": "assistant/message", "seq": 2, "data": map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "hello back"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal assistant event: %v", err)
	}
	sessionFile := filepath.Join(sessionDir, "session.v4.jsonl")
	content := strings.Join([]string{string(header), string(user), string(assistant)}, "\n") + "\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	return sessionFile
}

// TestRunImportCLIManifestSkipsImportedSession covers the idempotency contract
// end to end at the CLI: a session already recorded in the manifest is skipped
// (reported in the human summary) instead of imported twice.
func TestRunImportCLIManifestSkipsImportedSession(t *testing.T) {
	dshHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)
	writeValidDSHSessionFixture(t, dshHome)
	manifest := filepath.Join(dataDir, "dsh-import-manifest.json")

	// First run imports the session and records it in the manifest.
	var out1, err1 bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome, "--manifest", manifest}, &out1, &err1); code != 0 {
		t.Fatalf("first import exit code = %d (stderr %q, stdout %q)", code, err1.String(), out1.String())
	}
	if !strings.Contains(out1.String(), "sessions: found 1, imported 1, skipped 0, failed 0") {
		t.Fatalf("first import summary unexpected:\n%s", out1.String())
	}

	// Second run must skip the unchanged session instead of importing it again.
	var out2, err2 bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome, "--manifest", manifest}, &out2, &err2); code != 0 {
		t.Fatalf("second import exit code = %d (stderr %q, stdout %q)", code, err2.String(), out2.String())
	}
	if !strings.Contains(out2.String(), "sessions: found 1, imported 0, skipped 1, failed 0") {
		t.Fatalf("summary does not report the idempotent skip:\n%s", out2.String())
	}
	if !strings.Contains(out2.String(), "skips:") || !strings.Contains(out2.String(), "idempotent=1") {
		t.Fatalf("summary does not break down skips:\n%s", out2.String())
	}
}

// TestRunImportCLICorruptManifestRebuilds covers the damage-tolerance contract:
// a corrupt manifest no longer blocks the run; it is preserved next to the
// manifest path and rebuilt with a warning, and the import proceeds.
func TestRunImportCLICorruptManifestRebuilds(t *testing.T) {
	dshHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	manifest := filepath.Join(dataDir, "manifest.json")
	corrupt := []byte("{not json")
	if err := os.WriteFile(manifest, corrupt, 0o600); err != nil {
		t.Fatalf("write corrupt manifest: %v", err)
	}

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome, "--manifest", manifest}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0 with a rebuilt manifest (stdout %q, stderr %q)",
			code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "corrupt") {
		t.Fatalf("summary does not warn about the corrupt manifest:\n%s", out.String())
	}
	preserved, _ := filepath.Glob(manifest + ".corrupt-*")
	if len(preserved) != 1 {
		t.Fatalf("corrupt manifest was not preserved: %v", preserved)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("rebuilt manifest missing: %v", err)
	}
	var rebuilt struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &rebuilt); err != nil || rebuilt.Version != 2 {
		t.Fatalf("rebuilt manifest = %s (err %v), want version 2", data, err)
	}
}

// stubInstanceProbe replaces the data-directory lock probe for one test.
func stubInstanceProbe(t *testing.T, fn func(dir string) (*app.InstanceLockInfo, error)) {
	t.Helper()
	previous := probeInstanceLock
	probeInstanceLock = fn
	t.Cleanup(func() { probeInstanceLock = previous })
}

// TestRunImportCLIRefusesWhileAppRunning is the residual PD-04 guard: a real
// import is refused (exit 1, actionable message) while another ZeoKeel process
// holds the data directory's single-instance lock. The store is never opened.
func TestRunImportCLIRefusesWhileAppRunning(t *testing.T) {
	dshHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)
	var probedDir string
	stubInstanceProbe(t, func(dir string) (*app.InstanceLockInfo, error) {
		probedDir = dir
		return &app.InstanceLockInfo{PID: 4242}, nil
	})

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome}, &out, &errb); code != 1 {
		t.Fatalf("exit code = %d, want 1 (stdout %q, stderr %q)", code, out.String(), errb.String())
	}
	if probedDir != dataDir {
		t.Fatalf("probed data dir = %q, want %q", probedDir, dataDir)
	}
	for _, want := range []string{"running", "4242", "Quit the desktop app", "--force"} {
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("stderr missing %q:\n%s", want, errb.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "index.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("running-instance refusal still opened/created the store (err %v)", err)
	}
}

// TestRunImportCLIForceOverridesRunningInstance keeps an escape hatch for
// read-only/quirky environments: --force imports anyway with an explicit
// warning.
func TestRunImportCLIForceOverridesRunningInstance(t *testing.T) {
	dshHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)
	stubInstanceProbe(t, func(string) (*app.InstanceLockInfo, error) {
		return &app.InstanceLockInfo{PID: 4242}, nil
	})

	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome, "--force"}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0 with --force (stdout %q, stderr %q)", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "--force") {
		t.Fatalf("stderr does not record the forced override: %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "index.sqlite")); err != nil {
		t.Fatalf("forced import did not open the store: %v", err)
	}
}

// TestRunImportCLIProbeErrorWarnsAndContinues: an unreadable/odd lock file must
// not block imports forever; the CLI warns and continues.
func TestRunImportCLIProbeErrorWarnsAndContinues(t *testing.T) {
	dshHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)
	stubInstanceProbe(t, func(string) (*app.InstanceLockInfo, error) {
		return nil, errors.New("permission denied reading instance lock")
	})
	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout %q, stderr %q)", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "warning") || !strings.Contains(errb.String(), "instance lock") {
		t.Fatalf("stderr does not warn about the failed running-instance probe: %q", errb.String())
	}
}

// TestRunImportCLIDryRunSkipsInstanceProbe: a dry run writes nothing, so it
// must not be blocked by the running-app guard (and must not probe at all).
func TestRunImportCLIDryRunSkipsInstanceProbe(t *testing.T) {
	dshHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)
	called := false
	stubInstanceProbe(t, func(string) (*app.InstanceLockInfo, error) {
		called = true
		return &app.InstanceLockInfo{PID: 4242}, nil
	})
	var out, errb bytes.Buffer
	if code := runImportCLI([]string{"--from", dshHome, "--dry-run"}, &out, &errb); code != 0 {
		t.Fatalf("dry-run exit code = %d, want 0 (stderr %q)", code, errb.String())
	}
	if called {
		t.Fatal("dry run consulted the running-instance probe")
	}
}
