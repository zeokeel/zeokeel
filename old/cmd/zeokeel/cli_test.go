package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestVersionStringCarriesBuildIdentity covers AR-03: --version must print the
// injected version plus the facts a bug report needs.
func TestVersionStringCarriesBuildIdentity(t *testing.T) {
	oldVersion, oldCommit, oldBuild := version, commit, buildTime
	version, commit, buildTime = "1.2.3", "abc1234", "2026-11-05T12:00:00Z"
	defer func() { version, commit, buildTime = oldVersion, oldCommit, oldBuild }()

	got := versionString()
	for _, want := range []string{"zeokeel 1.2.3", "commit abc1234", "built 2026-11-05T12:00:00Z", "linux/amd64"} {
		if !strings.Contains(got, want) {
			t.Fatalf("versionString() = %q, missing %q", got, want)
		}
	}
	if shortVersion() != "1.2.3" {
		t.Fatalf("shortVersion() = %q", shortVersion())
	}
}

// TestRunCLIVersion checks `zeokeel --version` dispatch.
func TestRunCLIVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCLI([]string{"--version"}, &out, &errb); code != 0 {
		t.Fatalf("--version exit code = %d, want 0 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(out.String(), "zeokeel ") {
		t.Fatalf("--version output = %q", out.String())
	}
	if errb.Len() != 0 {
		t.Fatalf("--version wrote stderr: %q", errb.String())
	}
}

// TestRunCLIHelp checks `zeokeel --help` dispatch and that the headless
// command and data-dir override are documented.
func TestRunCLIHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCLI([]string{"--help"}, &out, &errb); code != 0 {
		t.Fatalf("--help exit code = %d", code)
	}
	for _, want := range []string{"Usage:", "zeokeel run", "--version", "ZEOKEEL_DATA_DIR", "crash-"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("--help output missing %q:\n%s", want, out.String())
		}
	}
}

// TestHeadlessWiringInstalled covers the FP-18 production wiring: the package
// init must point the seam at app.RunHeadless, so `zeokeel run` is not a dead
// command in a real build.
func TestHeadlessWiringInstalled(t *testing.T) {
	if headlessRun == nil {
		t.Fatal("headlessRun is nil: `zeokeel run` would report 'not wired yet' in production")
	}
}

// TestRunCLIHeadlessHelpAndUsageError drives the real wired backend for the two
// paths that do not create a data directory or call a model: `run --help` (0)
// and `run` with no prompt (usage error, 2).
func TestRunCLIHeadlessHelpAndUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCLI([]string{"run", "--help"}, &out, &errb); code != 0 {
		t.Fatalf("run --help exit code = %d, want 0 (stderr %q)", code, errb.String())
	}
	for _, want := range []string{"Usage:", "--json", "--yes"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("run --help output missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	errb.Reset()
	if code := runCLI([]string{"run"}, &out, &errb); code != 2 {
		t.Fatalf("run (no prompt) exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "zeokeel run:") || !strings.Contains(errb.String(), "Usage:") {
		t.Fatalf("run (no prompt) stderr = %q", errb.String())
	}
}

// TestRunCLIRunNotWired covers the nil-seam fallback: if the backend is
// missing (tests only), `run` fails with a clear actionable error instead of
// panicking or silently launching the desktop app.
func TestRunCLIRunNotWired(t *testing.T) {
	old := headlessRun
	headlessRun = nil
	defer func() { headlessRun = old }()

	var out, errb bytes.Buffer
	code := runCLI([]string{"run", "hello"}, &out, &errb)
	if code != 2 {
		t.Fatalf("run exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "not wired yet") || !strings.Contains(errb.String(), "RunHeadless") {
		t.Fatalf("run stderr = %q", errb.String())
	}
}

// TestRunCLIRunWired checks the seam is used once the backend provides
// RunHeadless: the stub receives the subcommand arguments.
func TestRunCLIRunWired(t *testing.T) {
	old := headlessRun
	var got []string
	headlessRun = func(args []string, stdout, stderr io.Writer) int {
		got = append([]string(nil), args...)
		return 7
	}
	defer func() { headlessRun = old }()

	var out, errb bytes.Buffer
	if code := runCLI([]string{"run", "do", "it"}, &out, &errb); code != 7 {
		t.Fatalf("run exit code = %d, want stub's 7", code)
	}
	if strings.Join(got, " ") != "do it" {
		t.Fatalf("stub args = %v", got)
	}
}

// TestRunCLIUnknownCommand checks unknown input is a usage error, not a silent
// fall-through to the desktop app.
func TestRunCLIUnknownCommand(t *testing.T) {
	for _, arg := range []string{"bogus", "--bogus"} {
		var out, errb bytes.Buffer
		if code := runCLI([]string{arg}, &out, &errb); code != 2 {
			t.Fatalf("runCLI(%q) exit code = %d, want 2", arg, code)
		}
		if !strings.Contains(errb.String(), "unknown") || !strings.Contains(errb.String(), "Usage:") {
			t.Fatalf("runCLI(%q) stderr = %q", arg, errb.String())
		}
	}
}
