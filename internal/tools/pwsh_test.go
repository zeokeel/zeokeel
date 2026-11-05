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
