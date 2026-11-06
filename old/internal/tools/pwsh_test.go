package tools

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPwsh(t *testing.T) {
	root := t.TempDir()
	tool := NewPwshTool(PwshConfig{WorkspaceRoot: root})
	if tool.Name() != "pwsh" {
		t.Fatalf("name = %q", tool.Name())
	}
	assertSchema(t, tool)

	hasPwsh := false
	for _, name := range []string{"pwsh", "powershell"} {
		if _, err := exec.LookPath(name); err == nil {
			hasPwsh = true
			break
		}
	}
	if hasPwsh {
		out := mustInvoke(t, tool, map[string]any{"command": "Write-Output 'hello from pwsh'"})
		if !strings.Contains(out, "hello from pwsh") {
			t.Fatalf("pwsh output = %q", out)
		}
	} else {
		t.Log("PowerShell is not installed; exercising only the error paths")
		_, err := invokeTool(t, tool, map[string]any{"command": "Write-Output hi"})
		if err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("absent pwsh error = %v", err)
		}
	}

	t.Run("absent executable", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := invokeTool(t, tool, map[string]any{"command": "Write-Output hi"})
		if err == nil || !strings.Contains(err.Error(), "PowerShell is not installed") {
			t.Fatalf("error = %v", err)
		}
	})

	if _, err := invokeTool(t, tool, map[string]any{"command": "  "}); err == nil {
		t.Fatal("blank command should fail")
	}
	bad := NewPwshTool(PwshConfig{})
	if _, err := invokeTool(t, bad, map[string]any{"command": "Write-Output hi"}); err == nil || !strings.Contains(err.Error(), "WorkspaceRoot") {
		t.Fatalf("missing root error = %v", err)
	}
	if _, err := tool.Invoke(nil, []byte(`{"command":`)); err == nil {
		t.Fatal("malformed JSON should fail")
	}
}

func TestPwshDescriptionHasSchema(t *testing.T) {
	tool := NewPwshTool(PwshConfig{WorkspaceRoot: t.TempDir()})
	assertSchema(t, tool)
}

// TestPwshDescriptionCarriesWindowsFacts covers the description fix: the tool
// only surfaces on Windows builds, so it must teach native paths, $env:
// variables, the pwsh -Command invocation and the Windows exit-1 kill semantics.
func TestPwshDescriptionCarriesWindowsFacts(t *testing.T) {
	desc := NewPwshTool(PwshConfig{WorkspaceRoot: t.TempDir()}).Description()
	for _, want := range []string{
		"pwsh -Command",
		"native Windows form",
		"$env:NAME",
		"[exit code: 1]",
		"without a signal marker",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("pwsh description missing %q:\n%s", want, desc)
		}
	}
}

// TestPwshMissingHintIsPlatformAware covers the actionable-error fix: Windows
// must never be pointed at the bash tool, which does not exist there, while a
// POSIX build may suggest bash as the fallback shell.
func TestPwshMissingHintIsPlatformAware(t *testing.T) {
	win := pwshMissingHint("windows")
	if strings.Contains(win, "bash") {
		t.Fatalf("Windows missing-pwsh hint points at bash: %q", win)
	}
	if !strings.Contains(win, "install PowerShell") {
		t.Fatalf("Windows missing-pwsh hint is not actionable: %q", win)
	}
	posix := pwshMissingHint("linux")
	if !strings.Contains(posix, "bash") {
		t.Fatalf("POSIX missing-pwsh hint does not offer bash: %q", posix)
	}
}
