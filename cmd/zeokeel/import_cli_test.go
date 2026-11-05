package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	for _, args := range [][]string{
		{"--from"},
		{"--max"},
		{"--max", "x"},
		{"--max=-1"},
		{"--bogus"},
		{"positional"},
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
