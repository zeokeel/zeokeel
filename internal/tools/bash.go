package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultBashTimeout = 30 * time.Second
	maxBashTimeout     = 300 * time.Second
)

// bashDescription documents the tool. The default is filled in by
// bashTool.Description so the prose always matches the configured
// plugin.shell.timeout_seconds value (clamped to maxBashTimeout).
const bashDescription = `Run a shell command inside the workspace and return its combined stdout and stderr.

The command runs as "sh -c <command>" in the current working directory (the workspace root by default; the working_directory tool can change it). ZEOKEEL_* and DEEPSEEK* environment variables are removed. The timeout defaults to %d seconds and is capped at %d seconds; on timeout the whole process group is killed. Output beyond the configured limit is truncated. A non-zero exit appends an [exit code: N] marker to the result (the tool also returns an error).

Args JSON schema:
{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute."},"timeout_seconds":{"type":"integer","description":"Optional timeout in seconds (default %d, maximum %d).","minimum":1,"maximum":%d}},"required":["command"],"additionalProperties":false}`

type bashTool struct {
	root      string
	maxOutput int
	timeout   time.Duration
	cwd       func() string
}

func newBashTool(root string, maxOutput int) *bashTool {
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}
	return &bashTool{root: root, maxOutput: maxOutput}
}

// withTimeout sets the default timeout applied when the call does not pass
// timeout_seconds. Non-positive values keep the built-in 30s default; values
// above maxBashTimeout are clamped so the documented cap is real.
func (t *bashTool) withTimeout(d time.Duration) *bashTool {
	if d > 0 {
		if d > maxBashTimeout {
			d = maxBashTimeout
		}
		t.timeout = d
	}
	return t
}

// defaultTimeout is the configured default, clamped to the documented range.
func (t *bashTool) defaultTimeout() time.Duration {
	d := t.timeout
	if d <= 0 {
		return defaultBashTimeout
	}
	if d > maxBashTimeout {
		return maxBashTimeout
	}
	return d
}

// withCwd makes relative commands run in the shared current directory.
func (t *bashTool) withCwd(cwd func() string) *bashTool {
	t.cwd = cwd
	return t
}

// dir returns the working directory for a command.
func (t *bashTool) dir() string {
	dir := t.root
	if t.cwd != nil {
		if cwd := t.cwd(); cwd != "" && cwd != "." {
			dir = filepath.Join(t.root, filepath.FromSlash(cwd))
		}
	}
	return dir
}

func (t *bashTool) Name() string { return "bash" }
func (t *bashTool) Description() string {
	def := int(t.defaultTimeout() / time.Second)
	maxSeconds := int(maxBashTimeout / time.Second)
	return fmt.Sprintf(bashDescription, def, maxSeconds, def, maxSeconds, maxSeconds)
}

func (t *bashTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("bash: command is required")
	}
	timeout := t.defaultTimeout()
	if a.TimeoutSeconds > 0 {
		seconds := a.TimeoutSeconds
		if maxSeconds := int(maxBashTimeout / time.Second); seconds > maxSeconds {
			seconds = maxSeconds
		}
		timeout = time.Duration(seconds) * time.Second
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out := newCappedBuffer(t.maxOutput)
	cmd := shellCommand(ctx, a.Command)
	cmd.Dir = t.dir()
	cmd.Env = setEnv(scrubEnv(os.Environ()), "PWD", cmd.Dir)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = 2 * time.Second
	configureProcess(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return killProcessGroup(cmd.Process.Pid)
	}

	runErr := cmd.Run()
	text := out.String()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return text, fmt.Errorf("bash: command timed out after %s", timeout)
		}
		return text, fmt.Errorf("bash: command canceled: %w", ctxErr)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// The prompt tells the model to check an [exit code: N] marker;
			// emit it in the result text while keeping the Go error for
			// callers that inspect it.
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += fmt.Sprintf("[exit code: %d]\n", exitErr.ExitCode())
			return text, fmt.Errorf("bash: command exited with status %d", exitErr.ExitCode())
		}
		return text, fmt.Errorf("bash: %w", runErr)
	}
	return text, nil
}

// scrubEnv removes ZeoKeel and provider secrets from a child environment.
// Matching is case-insensitive so Windows-style variable names are covered.
func scrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			name = entry[:i]
		}
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "ZEOKEEL_") || strings.HasPrefix(upper, "DEEPSEEK") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// setEnv replaces any existing key (case-insensitively) with key=value. The
// child shell sees a PWD that matches its actual working directory.
func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			name = entry[:i]
		}
		if strings.EqualFold(name, key) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, key+"="+value)
}

// cappedBuffer collects at most limit bytes and records whether output was
// dropped. It is safe for concurrent writes because stdout and stderr may be
// copied by separate goroutines. Write always reports the full length so a
// verbose child process is not killed by a short write.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	if limit < 0 {
		limit = 0
	}
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.truncated || n == 0 {
		return n, nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return n, nil
	}
	if n > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return n, nil
	}
	b.buf.Write(p)
	return n, nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := b.buf.String()
	if !b.truncated {
		return data
	}
	note := fmt.Sprintf("output truncated at %d bytes", b.limit)
	return capWithNote(data, b.limit, note)
}
