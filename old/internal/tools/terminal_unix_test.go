//go:build unix

package tools

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTerminalPTYFallbackToPipes forces PTY allocation to fail and verifies
// that the session still works through the legacy pipe implementation, with
// pty=false surfaced to the model.
func TestTerminalPTYFallbackToPipes(t *testing.T) {
	old := terminalOpenPTY
	terminalOpenPTY = func() (*os.File, *os.File, error) {
		return nil, nil, errors.New("pty unavailable for test")
	}
	t.Cleanup(func() { terminalOpenPTY = old })

	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 3 * time.Second})
	open := findTool(t, ts, "terminal_open")
	list := findTool(t, ts, "terminal_list")
	send := findTool(t, ts, "terminal_send")
	read := findTool(t, ts, "terminal_read")
	closeTool := findTool(t, ts, "terminal_close")

	out := mustInvoke(t, open, map[string]any{})
	if !strings.Contains(out, "pty=false") {
		t.Fatalf("open output = %q, want pty=false", out)
	}
	id := testToolID(t, out, "term-")
	pid := terminalTestPID(t, out)

	if out := mustInvoke(t, list, map[string]any{}); !strings.Contains(out, "pty=false") {
		t.Fatalf("list output = %q, want pty=false", out)
	}

	out = mustInvoke(t, send, map[string]any{"id": id, "input": "echo $((2*9))-fallback"})
	if !strings.Contains(out, "18-fallback") {
		t.Fatalf("fallback send output = %q, want 18-fallback", out)
	}
	if out := mustInvoke(t, read, map[string]any{"id": id}); !strings.Contains(out, "18-fallback") {
		t.Fatalf("fallback read output = %q, want 18-fallback", out)
	}

	mustInvoke(t, closeTool, map[string]any{"id": id})
	waitUntil(t, 5*time.Second, func() bool {
		return syscall.Kill(pid, 0) == syscall.ESRCH
	})
}

// TestTerminalPTYCloseReaps verifies that terminal_close kills the shell and
// waits for it, so the process is gone afterwards.
func TestTerminalPTYCloseReaps(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 5 * time.Second})
	open := findTool(t, ts, "terminal_open")
	send := findTool(t, ts, "terminal_send")
	closeTool := findTool(t, ts, "terminal_close")

	out := mustInvoke(t, open, map[string]any{})
	id := testToolID(t, out, "term-")
	pid := terminalTestPID(t, out)
	mustInvoke(t, send, map[string]any{"id": id, "input": "sleep 30"})
	mustInvoke(t, closeTool, map[string]any{"id": id})

	waitUntil(t, 5*time.Second, func() bool {
		return syscall.Kill(pid, 0) == syscall.ESRCH
	})
}
