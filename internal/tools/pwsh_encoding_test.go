package tools

import (
	"strings"
	"testing"
)

// TestPwshCommandArg covers the Windows UTF-8 encoding preamble (BUG-4): a
// plain Windows command is prefixed, other platforms are untouched, and a
// command that starts with a PowerShell script header keeps its own first line.
func TestPwshCommandArg(t *testing.T) {
	cases := []struct {
		name       string
		goos       string
		command    string
		wantPrefix bool
	}{
		{"windows plain", "windows", `Get-ChildItem "C:\Users\临时文件"`, true},
		{"windows upper goos", "Windows", "Write-Output hi", true},
		{"windows leading space", "windows", "   Write-Output hi", true},
		{"linux untouched", "linux", "Write-Output hi", false},
		{"darwin untouched", "darwin", "Write-Output hi", false},
		{"param no space", "windows", "param($Path) $Path", false},
		{"param with space", "windows", "Param ($Name)", false},
		{"param tab", "windows", "param\t($Name)", false},
		{"using namespace", "windows", "using namespace System.Text", false},
		{"using module", "windows", "using module ./x.psm1", false},
		{"requires", "windows", "#Requires -Version 5.1\nWrite-Output hi", false},
		{"cmdletbinding", "windows", "[CmdletBinding()] param($x) $x", false},
		{"already pinned", "windows", pwshUTF8Preamble + "Write-Output hi", false},
		{"already pinned leading space", "windows", "  " + pwshUTF8Preamble + "Write-Output hi", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pwshCommandArg(tc.goos, tc.command)
			if !tc.wantPrefix {
				if got != tc.command {
					t.Fatalf("command without preamble was rewritten: %q", got)
				}
				return
			}
			if !strings.HasPrefix(got, pwshUTF8Preamble) || !strings.HasSuffix(got, tc.command) {
				t.Fatalf("prefixed command = %q, want preamble + %q", got, tc.command)
			}
		})
	}
}

func TestPwshUTF8PreambleShape(t *testing.T) {
	// Single line: prefixing must not shift PowerShell error line numbers.
	if strings.Contains(pwshUTF8Preamble, "\n") || strings.Contains(pwshUTF8Preamble, "\r") {
		t.Fatalf("preamble must stay on one line: %q", pwshUTF8Preamble)
	}
	for _, want := range []string{
		"[Console]::OutputEncoding",
		"$OutputEncoding",
		"[System.Text.UTF8Encoding]::new($false)",
		"try {",
		"catch {}",
	} {
		if !strings.Contains(pwshUTF8Preamble, want) {
			t.Fatalf("preamble missing %q: %q", want, pwshUTF8Preamble)
		}
	}
	// The console assignment precedes the pipeline assignment, mirroring DSH.
	if strings.Index(pwshUTF8Preamble, "[Console]::OutputEncoding") > strings.Index(pwshUTF8Preamble, "$OutputEncoding") {
		t.Fatalf("preamble assigns $OutputEncoding before [Console]::OutputEncoding")
	}
}
