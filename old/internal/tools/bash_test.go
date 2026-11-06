package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

func TestBashDisabledByDefault(t *testing.T) {
	cfg := Config{WorkspaceRoot: t.TempDir()}
	ts := testTools(t, cfg)
	if _, ok := ts["bash"]; ok {
		t.Fatal("bash must not be built without AllowShell")
	}
	reg := runtime.NewRegistry()
	if err := Register(reg, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := reg.Get("bash"); ok {
		t.Fatal("bash must not be registered without AllowShell")
	}
}

func TestBash(t *testing.T) {
	root := t.TempDir()
	sb, err := newSandbox(root)
	if err != nil {
		t.Fatalf("newSandbox: %v", err)
	}
	tool := newBashTool(sb.root, 1024)

	out := mustInvoke(t, tool, map[string]any{"command": "printf 'hello\\n'"})
	if out != "hello\n" {
		t.Fatalf("echo output = %q", out)
	}

	out = mustInvoke(t, tool, map[string]any{"command": "printf out; printf err >&2"})
	if !strings.Contains(out, "out") || !strings.Contains(out, "err") {
		t.Fatalf("combined output = %q", out)
	}

	out = mustInvoke(t, tool, map[string]any{"command": "pwd -P"})
	if strings.TrimSpace(out) != sb.root {
		t.Fatalf("bash cwd = %q, want %q", strings.TrimSpace(out), sb.root)
	}

	mustInvoke(t, tool, map[string]any{"command": "printf made > made-by-bash.txt"})
	if data, err := os.ReadFile(filepath.Join(sb.root, "made-by-bash.txt")); err != nil || string(data) != "made" {
		t.Fatalf("bash wrote outside root or not at all: %q %v", data, err)
	}

	out, err = invokeTool(t, tool, map[string]any{"command": "exit 3"})
	if err == nil || !strings.Contains(err.Error(), "status 3") {
		t.Fatalf("exit 3: out=%q err=%v", out, err)
	}
	// The prompt tells the model to check this marker, so the tool must emit
	// it in the result text (the Go error alone is not visible to the model).
	if !strings.Contains(out, "[exit code: 3]") {
		t.Fatalf("exit 3 result has no exit-code marker: %q", out)
	}

	start := time.Now()
	out, err = invokeTool(t, tool, map[string]any{"command": "sleep 5", "timeout_seconds": 1})
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout: out=%q err=%v", out, err)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("timeout took %v, process was not killed promptly", elapsed)
	}

	cmd := "i=0; while [ $i -lt 2000 ]; do printf abcdef; i=$((i+1)); done"
	out = mustInvoke(t, tool, map[string]any{"command": cmd})
	if len(out) > 1024 {
		t.Fatalf("capped output is %d bytes, want <= 1024", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("capped output has no truncation marker: %q", out)
	}
	if !strings.HasPrefix(out, "abcdef") {
		t.Fatalf("capped output lost its prefix: %q", out)
	}

	t.Setenv("ZEOKEEL_API_KEY", "zeokeel-secret")
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-secret")
	out = mustInvoke(t, tool, map[string]any{
		"command": `printf '%s|%s' "${ZEOKEEL_API_KEY:-unset}" "${DEEPSEEK_API_KEY:-unset}"`,
	})
	if out != "unset|unset" {
		t.Fatalf("bash inherited scrubbed environment variables: %q", out)
	}

	if _, err := invokeTool(t, tool, map[string]any{"command": "   "}); err == nil {
		t.Fatal("blank command should fail")
	}
	if _, err := tool.Invoke(context.Background(), nil); err == nil {
		t.Fatal("missing command should fail")
	}
	if _, err := tool.Invoke(context.Background(), []byte(`{"command":`)); err == nil {
		t.Fatal("malformed JSON should fail")
	}

	// A nil context is normalized rather than panicking.
	if _, err := tool.Invoke(nil, mustArgs(t, map[string]any{"command": "printf ok"})); err != nil {
		t.Fatalf("nil context: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	if _, err := tool.Invoke(ctx, mustArgs(t, map[string]any{"command": "sleep 5"})); err == nil {
		t.Fatal("canceled context should fail")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("canceled command took %v", elapsed)
	}
}

func TestScrubEnv(t *testing.T) {
	env := []string{
		"PATH=/bin",
		"ZEOKEEL_TOKEN=secret",
		"zeokeel_home=/tmp/x",
		"DEEPSEEK_API_KEY=secret",
		"deepseek_model=foo",
		"DEEPSEEKER=removed",
		"HOME=/home/me",
		"ZEOKEEL=kept",
		// Common third-party credentials: RT-15 extended the match beyond the
		// ZeoKeel/DeepSeek prefixes.
		"OPENAI_API_KEY=sk-openai",
		"ANTHROPIC_API_KEY=sk-ant",
		"AWS_SECRET_ACCESS_KEY=aws",
		"GITHUB_TOKEN=ghp",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"KUBECONFIG=/home/me/.kube/config",
		// Ordinary shell/build variables must survive the scrub.
		"LANG=en_US.UTF-8",
		"TERM=xterm",
		"GIT_AUTHOR_NAME=A User",
	}
	got := scrubEnv(env)
	want := []string{"PATH=/bin", "HOME=/home/me", "ZEOKEEL=kept", "LANG=en_US.UTF-8", "TERM=xterm", "GIT_AUTHOR_NAME=A User"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scrubEnv = %v, want %v", got, want)
	}
}

// TestBashUsesConfiguredTimeout checks the plugin.shell.timeout_seconds hook:
// the tool's default timeout, not only the per-call argument, is applied.
func TestBashUsesConfiguredTimeout(t *testing.T) {
	root := t.TempDir()
	tool := newBashTool(root, 1024).withTimeout(100 * time.Millisecond)
	start := time.Now()
	_, err := tool.Invoke(context.Background(), json.RawMessage(`{"command":"sleep 5"}`))
	if err == nil {
		t.Fatal("sleep 5 succeeded under a 100ms configured timeout")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("configured timeout took %v, want about 100ms", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("timeout error = %v", err)
	}
}

// TestBashUsesConfiguredOutputCap checks the plugin.shell.output_cap_kb hook.
func TestBashUsesConfiguredOutputCap(t *testing.T) {
	root := t.TempDir()
	tool := newBashTool(root, 64)
	out, err := tool.Invoke(context.Background(), json.RawMessage(`{"command":"printf '%0200d' 0"}`))
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if len(out) > 128 {
		t.Fatalf("output cap ignored: %d bytes", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("truncation note missing: %q", out)
	}
}

// TestBashDescriptionTracksConfiguredTimeout covers the config/doc mismatch:
// the schema and prose must describe the effective configured default and the
// real 300s cap, not a fixed 30s.
func TestBashDescriptionTracksConfiguredTimeout(t *testing.T) {
	tool := newBashTool(t.TempDir(), 0)
	if !strings.Contains(tool.Description(), "defaults to 30 seconds") {
		t.Fatalf("default description = %q", tool.Description())
	}
	tool = tool.withTimeout(3600 * time.Second)
	if !strings.Contains(tool.Description(), "defaults to 300 seconds") {
		t.Fatalf("configured description = %q", tool.Description())
	}
	if tool.defaultTimeout() != maxBashTimeout {
		t.Fatalf("configured timeout = %v, want the %v cap", tool.defaultTimeout(), maxBashTimeout)
	}
}

// TestBashRequiresAllowBash proves the platform selection is independent from
// AllowShell: the shell plugin can be on (write/edit available) while the
// POSIX bash tool stays off on a build that exposes another shell stack.
func TestBashRequiresAllowBash(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root, AllowShell: true})
	if _, ok := ts["bash"]; ok {
		t.Fatal("bash must not be built when AllowBash is false")
	}
	if _, ok := ts["write_file"]; !ok {
		t.Fatal("AllowShell must not remove write_file when AllowBash is false")
	}
	cfg := Config{WorkspaceRoot: root, AllowShell: true, AllowBash: true}
	reg := runtime.NewRegistry()
	if err := Register(reg, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := reg.Get("bash"); !ok {
		t.Fatal("bash must be registered when AllowShell and AllowBash are true")
	}
}

// TestBashDescriptionFollowsPlatform covers the description fix: a Windows
// build must never promise "sh -c" (the implementation there routes through
// cmd.exe /C), while POSIX builds keep documenting sh -c.
func TestBashDescriptionFollowsPlatform(t *testing.T) {
	if got := bashShellDescription("linux"); !strings.Contains(got, "sh -c") {
		t.Fatalf("POSIX shell description = %q, want sh -c", got)
	}
	if got := bashShellDescription("darwin"); !strings.Contains(got, "sh -c") {
		t.Fatalf("darwin shell description = %q, want sh -c", got)
	}
	win := bashShellDescription("windows")
	if strings.Contains(win, "sh -c") {
		t.Fatalf("Windows shell description still promises sh -c: %q", win)
	}
	for _, want := range []string{"cmd.exe /C", "pwsh"} {
		if !strings.Contains(win, want) {
			t.Fatalf("Windows shell description %q does not mention %q", win, want)
		}
	}
	tool := newBashTool(t.TempDir(), 0)
	if !strings.Contains(tool.Description(), bashShellDescription(goruntime.GOOS)) {
		t.Fatalf("bash description does not match the build platform (%s):\n%s", goruntime.GOOS, tool.Description())
	}
}
