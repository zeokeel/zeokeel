package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

func findTool(t *testing.T, ts []runtime.Tool, name string) runtime.Tool {
	t.Helper()
	for _, tool := range ts {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func testToolID(t *testing.T, out, prefix string) string {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(prefix) + `\d+`)
	id := re.FindString(out)
	if id == "" {
		t.Fatalf("no %s id in output %q", prefix, out)
	}
	return id
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func skipUnlessUnixShell(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("terminal/job tests use POSIX shell commands")
	}
}

// terminalTestPID extracts the shell pid from terminal_open output.
func terminalTestPID(t *testing.T, out string) int {
	t.Helper()
	m := regexp.MustCompile(`\bpid=(\d+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no pid in output %q", out)
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse pid %q: %v", m[1], err)
	}
	return pid
}

func TestTerminalOpenSendReadClose(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 3 * time.Second})
	open := findTool(t, ts, "terminal_open")
	list := findTool(t, ts, "terminal_list")
	read := findTool(t, ts, "terminal_read")
	send := findTool(t, ts, "terminal_send")
	closeTool := findTool(t, ts, "terminal_close")

	if got := mustInvoke(t, list, map[string]any{}); got != "(no terminal sessions)" {
		t.Fatalf("empty list = %q", got)
	}

	out := mustInvoke(t, open, map[string]any{})
	id := testToolID(t, out, "term-")
	if !strings.Contains(out, "pid=") || !strings.Contains(out, "cwd=.") {
		t.Fatalf("open output = %q", out)
	}

	out = mustInvoke(t, send, map[string]any{"id": id, "input": "echo hello"})
	if !strings.Contains(out, "hello") {
		t.Fatalf("send output = %q", out)
	}
	if !strings.Contains(out, "[wait: idle]") || !strings.Contains(out, "[session: running") {
		t.Fatalf("send markers = %q", out)
	}

	out = mustInvoke(t, read, map[string]any{"id": id})
	if !strings.Contains(out, "hello") || !strings.Contains(out, "[bytes: 0-") {
		t.Fatalf("read output = %q", out)
	}

	out = mustInvoke(t, list, map[string]any{})
	if !strings.Contains(out, id) || !strings.Contains(out, "running") {
		t.Fatalf("list output = %q", out)
	}

	out = mustInvoke(t, closeTool, map[string]any{"id": id})
	if !strings.Contains(out, "closed terminal session "+id) {
		t.Fatalf("close output = %q", out)
	}
	if _, err := invokeTool(t, read, map[string]any{"id": id}); err == nil {
		t.Fatal("read after close should fail")
	}
	if _, err := invokeTool(t, closeTool, map[string]any{"id": id}); err == nil {
		t.Fatal("close after close should fail")
	}
}

func TestTerminalCwdAndValidation(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 3 * time.Second})
	open := findTool(t, ts, "terminal_open")
	send := findTool(t, ts, "terminal_send")
	read := findTool(t, ts, "terminal_read")
	closeTool := findTool(t, ts, "terminal_close")

	out := mustInvoke(t, open, map[string]any{"cwd": "sub"})
	id := testToolID(t, out, "term-")
	out = mustInvoke(t, send, map[string]any{"id": id, "input": "pwd"})
	if !strings.Contains(out, sub) {
		t.Fatalf("pwd output = %q, want %q", out, sub)
	}
	mustInvoke(t, closeTool, map[string]any{"id": id})

	for _, bad := range []string{"../outside", "/etc", "sub/../../etc"} {
		if _, err := invokeTool(t, open, map[string]any{"cwd": bad}); err == nil {
			t.Fatalf("open accepted cwd %q", bad)
		}
	}
	if _, err := invokeTool(t, send, map[string]any{"input": "echo hi"}); err == nil {
		t.Fatal("send without id should fail")
	}
	if _, err := invokeTool(t, read, map[string]any{"id": "nope"}); err == nil {
		t.Fatal("read of unknown id should fail")
	}
	if _, err := invokeTool(t, read, map[string]any{"id": "term-1", "offset": -1}); err == nil {
		t.Fatal("negative offset should fail")
	}
}

func TestTerminalMaxAndReap(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, MaxTerminals: 2, IdleTimeout: 200 * time.Millisecond})
	open := findTool(t, ts, "terminal_open")
	list := findTool(t, ts, "terminal_list")
	closeTool := findTool(t, ts, "terminal_close")

	first := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	second := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	if _, err := invokeTool(t, open, map[string]any{}); err == nil || !strings.Contains(err.Error(), "maximum of 2") {
		t.Fatalf("third open error = %v", err)
	}
	mustInvoke(t, closeTool, map[string]any{"id": first})
	mustInvoke(t, closeTool, map[string]any{"id": second})

	third := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	waitUntil(t, 3*time.Second, func() bool {
		return mustInvoke(t, list, map[string]any{}) == "(no terminal sessions)"
	})
	if _, err := invokeTool(t, closeTool, map[string]any{"id": third}); err == nil {
		t.Fatal("reaped session should no longer be closeable")
	}
}

func TestTerminalSignal(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 3 * time.Second})
	open := findTool(t, ts, "terminal_open")
	list := findTool(t, ts, "terminal_list")
	send := findTool(t, ts, "terminal_send")
	signal := findTool(t, ts, "terminal_signal")
	closeTool := findTool(t, ts, "terminal_close")

	id := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	mustInvoke(t, send, map[string]any{"id": id, "input": "sleep 30"})

	if _, err := invokeTool(t, signal, map[string]any{"id": id, "signal": "SIGKILL"}); err == nil || !strings.Contains(err.Error(), "use terminal_close") {
		t.Fatalf("SIGKILL error = %v", err)
	}
	if _, err := invokeTool(t, signal, map[string]any{"id": id, "signal": "SIGBOGUS"}); err == nil {
		t.Fatal("unknown signal should fail")
	}

	// Under a PTY the interactive shell ignores SIGTERM/SIGQUIT (a property of
	// job-control shells), so SIGINT is the signal that interrupts a running
	// foreground job, exactly like Ctrl-C.
	out := mustInvoke(t, signal, map[string]any{"id": id, "signal": "SIGINT"})
	if !strings.Contains(out, "delivered SIGINT to process group") {
		t.Fatalf("signal output = %q", out)
	}
	waitUntil(t, 5*time.Second, func() bool {
		out, err := invokeTool(t, send, map[string]any{"id": id, "input": "echo $((3*4))-signal-after"})
		return err == nil && strings.Contains(out, "12-signal-after")
	})
	if out := mustInvoke(t, list, map[string]any{}); !strings.Contains(out, id) || !strings.Contains(out, "running") {
		t.Fatalf("list after SIGINT = %q", out)
	}
	mustInvoke(t, closeTool, map[string]any{"id": id})
}

func TestTerminalSendTimeout(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 400 * time.Millisecond})
	open := findTool(t, ts, "terminal_open")
	send := findTool(t, ts, "terminal_send")
	closeTool := findTool(t, ts, "terminal_close")

	id := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	out := mustInvoke(t, send, map[string]any{
		"id":    id,
		"input": "while true; do echo tick; sleep 0.02; done",
	})
	if !strings.Contains(out, "[wait: timeout]") || !strings.Contains(out, "did not go quiet") {
		t.Fatalf("timeout output = %q", out)
	}
	mustInvoke(t, closeTool, map[string]any{"id": id})
}

func TestTerminalSendContextCancel(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 5 * time.Second})
	open := findTool(t, ts, "terminal_open")
	send := findTool(t, ts, "terminal_send")
	closeTool := findTool(t, ts, "terminal_close")

	id := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer cancel()
	out, err := send.Invoke(ctx, mustArgs(t, map[string]any{
		"id":    id,
		"input": "while true; do echo tick; sleep 0.02; done",
	}))
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("send cancel err = %v, out = %q", err, out)
	}
	mustInvoke(t, closeTool, map[string]any{"id": id})
}

// TestTerminalPTYOpenList asserts that a Unix terminal is backed by a real PTY
// and that the pty flag is surfaced by both terminal_open and terminal_list.
func TestTerminalPTYOpenList(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 5 * time.Second})
	open := findTool(t, ts, "terminal_open")
	list := findTool(t, ts, "terminal_list")
	closeTool := findTool(t, ts, "terminal_close")

	out := mustInvoke(t, open, map[string]any{})
	if !strings.Contains(out, "pty=true") {
		t.Fatalf("open output = %q, want pty=true", out)
	}
	id := testToolID(t, out, "term-")

	out = mustInvoke(t, list, map[string]any{})
	if !strings.Contains(out, id) || !strings.Contains(out, "pty=true") {
		t.Fatalf("list output = %q, want %s and pty=true", out, id)
	}
	mustInvoke(t, closeTool, map[string]any{"id": id})
}

// TestTerminalPTYEcho exercises the canonical line discipline: the shell echoes
// the typed command and executes it, and terminal_read sees both.
func TestTerminalPTYEcho(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 3 * time.Second})
	open := findTool(t, ts, "terminal_open")
	send := findTool(t, ts, "terminal_send")
	read := findTool(t, ts, "terminal_read")
	closeTool := findTool(t, ts, "terminal_close")

	id := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	out := mustInvoke(t, send, map[string]any{"id": id, "input": "echo hi\n"})
	if !strings.Contains(out, "hi") {
		t.Fatalf("send output = %q, want hi", out)
	}
	// The PTY echoes the input back, so the typed command is visible too.
	if !strings.Contains(out, "echo hi") {
		t.Fatalf("send output = %q, want the terminal echo of the input", out)
	}
	// Use a computed marker that cannot appear in the echoed command line, to
	// prove the shell actually executed the input and not just echoed it.
	out = mustInvoke(t, send, map[string]any{"id": id, "input": "echo $((6*7))-marker"})
	if !strings.Contains(out, "42-marker") {
		t.Fatalf("send output = %q, want the executed 42-marker", out)
	}

	out = mustInvoke(t, read, map[string]any{"id": id})
	if !strings.Contains(out, "hi") || !strings.Contains(out, "42-marker") || !strings.Contains(out, "[bytes: 0-") {
		t.Fatalf("read output = %q, want hi, 42-marker and byte range", out)
	}
	mustInvoke(t, closeTool, map[string]any{"id": id})
}

// TestTerminalPTYCwdPersists verifies that the login shell keeps its working
// directory across separate terminal_send calls.
func TestTerminalPTYCwdPersists(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 3 * time.Second})
	open := findTool(t, ts, "terminal_open")
	send := findTool(t, ts, "terminal_send")
	closeTool := findTool(t, ts, "terminal_close")

	id := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	mustInvoke(t, send, map[string]any{"id": id, "input": "cd /tmp"})
	out := mustInvoke(t, send, map[string]any{"id": id, "input": "pwd"})
	if !strings.Contains(out, "/tmp") {
		t.Fatalf("pwd output = %q, want /tmp", out)
	}
	mustInvoke(t, closeTool, map[string]any{"id": id})
}

// TestTerminalPTYSIGINT verifies that terminal_signal reaches the PTY's
// foreground process group (which job control may put in its own group), not
// just the shell's group.
func TestTerminalPTYSIGINT(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 3 * time.Second})
	open := findTool(t, ts, "terminal_open")
	send := findTool(t, ts, "terminal_send")
	signal := findTool(t, ts, "terminal_signal")
	closeTool := findTool(t, ts, "terminal_close")

	id := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")
	if out := mustInvoke(t, send, map[string]any{"id": id, "input": "sleep 30"}); !strings.Contains(out, "[wait: idle]") {
		t.Fatalf("sleep send output = %q", out)
	}
	if out := mustInvoke(t, signal, map[string]any{"id": id, "signal": "SIGINT"}); !strings.Contains(out, "delivered SIGINT") {
		t.Fatalf("signal output = %q", out)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := invokeTool(t, send, map[string]any{"id": id, "input": "echo $((5*5))-interrupted"})
		if err == nil && strings.Contains(out, "25-interrupted") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("SIGINT did not interrupt sleep 30; last send output = %q, err = %v", out, err)
		}
	}
	mustInvoke(t, closeTool, map[string]any{"id": id})
}

// TestTerminalPTYConcurrentClose stress-tests terminal_send/terminal_read while
// terminal_close runs. It fails on a deadlock, panic, or a stuck goroutine.
func TestTerminalPTYConcurrentClose(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewTerminalTools(TerminalConfig{WorkspaceRoot: root, IdleTimeout: 5 * time.Second})
	open := findTool(t, ts, "terminal_open")
	send := findTool(t, ts, "terminal_send")
	read := findTool(t, ts, "terminal_read")
	closeTool := findTool(t, ts, "terminal_close")

	id := testToolID(t, mustInvoke(t, open, map[string]any{}), "term-")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		sendArgs := mustArgs(t, map[string]any{"id": id, "input": fmt.Sprintf("echo stress-%d", i)})
		readArgs := mustArgs(t, map[string]any{"id": id})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = send.Invoke(ctx, sendArgs)
				_, _ = read.Invoke(ctx, readArgs)
			}
		}()
	}

	time.Sleep(150 * time.Millisecond)
	if out := mustInvoke(t, closeTool, map[string]any{"id": id}); !strings.Contains(out, "closed terminal session "+id) {
		t.Fatalf("close output = %q", out)
	}
	close(stop)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent send/read goroutines did not stop after terminal_close")
	}
	if _, err := invokeTool(t, read, map[string]any{"id": id}); err == nil {
		t.Fatal("read after close should fail")
	}
}

// TestTerminalOpenDescriptionFollowsPlatform covers the terminal description
// fix: Windows always uses stdio pipes and the platform shell, so it must not
// promise pty=true or a login "sh" the way the Unix description does.
func TestTerminalOpenDescriptionFollowsPlatform(t *testing.T) {
	unix := terminalOpenDescriptionFor("linux")
	if !strings.Contains(unix, "pty=true") || !strings.Contains(unix, "sh -l") {
		t.Fatalf("Unix terminal description lost its PTY/login-shell facts: %q", unix)
	}
	win := terminalOpenDescriptionFor("windows")
	if !strings.Contains(win, "pty=false") {
		t.Fatalf("Windows terminal description does not state pty=false: %q", win)
	}
	if strings.Contains(win, "sh -l") || strings.Contains(win, "pty=true") {
		t.Fatalf("Windows terminal description promises a Unix PTY: %q", win)
	}
	for _, want := range []string{"cmd.exe", "pwsh", "Args JSON schema:"} {
		if !strings.Contains(win, want) {
			t.Fatalf("Windows terminal description missing %q: %q", want, win)
		}
	}
	live := terminalOpenDescriptionFor(goruntime.GOOS)
	if !strings.Contains(live, "Args JSON schema:") {
		t.Fatalf("live terminal description missing its schema: %q", live)
	}
	assertSchema(t, &terminalOpenTool{})
}
