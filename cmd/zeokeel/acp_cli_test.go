package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunACPHelp: --help is answered on stdout and exits 0.
func TestRunACPHelp(t *testing.T) {
	var out bytes.Buffer
	if code := runACP([]string{"--help"}, &out); code != 0 {
		t.Fatalf("acp --help exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Usage: zeokeel acp") || !strings.Contains(out.String(), "stdout carries protocol frames only") {
		t.Fatalf("acp --help output = %q", out.String())
	}
}

// TestRunACPUnexpectedArg: arguments are a usage error (2), not a server start.
func TestRunACPUnexpectedArg(t *testing.T) {
	var out bytes.Buffer
	if code := runACP([]string{"--bogus"}, &out); code != 2 {
		t.Fatalf("acp --bogus exit code = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Fatalf("acp --bogus wrote stdout: %q", out.String())
	}
}

// TestRunACPNoModelFailsClosed: without a configured model the ACP server must
// exit 1 and never write to stdout (a client would otherwise parse a partial
// frame stream).
func TestRunACPNoModelFailsClosed(t *testing.T) {
	t.Setenv("ZEOKEEL_DATA_DIR", filepath.Join(t.TempDir(), "acp-data"))
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ZEOKEEL_PROVIDER_API_KEY", "")

	var out bytes.Buffer
	if code := runACP(nil, &out); code != 1 {
		t.Fatalf("acp without model exit code = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Fatalf("acp without model wrote stdout: %q", out.String())
	}
}

// TestRunCLIACPDispatch: the `acp` subcommand is reachable through runCLI and
// keeps stdout protocol-only (help path).
func TestRunCLIACPDispatch(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCLI([]string{"acp", "--help"}, &out, &errb); code != 0 {
		t.Fatalf("runCLI(acp --help) exit code = %d, want 0 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(out.String(), "Usage: zeokeel acp") {
		t.Fatalf("runCLI(acp --help) output = %q", out.String())
	}
}
