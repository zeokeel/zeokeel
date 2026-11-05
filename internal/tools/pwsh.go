package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// pwsh runs a one-shot PowerShell command when PowerShell is installed. It is
// the PowerShell twin of bash: same workspace cwd, ZeoKeel/DEEPSEEK environment
// scrubbing, timeout window and output cap. The executable is resolved on every
// call so a missing PowerShell reports a clear, actionable error instead of a
// spawn failure.

const pwshDescription = `Run a PowerShell command in the workspace root and return its combined stdout and stderr.

PowerShell is resolved from PATH as "pwsh" first and "powershell" second; when neither exists the call fails with an explicit error. The command runs with the workspace root as its working directory. ZEOKEEL_* and DEEPSEEK* environment variables are removed. The timeout defaults to 30 seconds and is capped at 300 seconds; on timeout the whole process group is killed. Output beyond the configured limit is truncated.

Args JSON schema:
{"type":"object","properties":{"command":{"type":"string","description":"PowerShell command to execute."},"timeout_seconds":{"type":"integer","description":"Optional timeout in seconds (default 30, maximum 300).","minimum":1,"maximum":300}},"required":["command"],"additionalProperties":false}`

// PwshConfig configures the pwsh tool.
type PwshConfig struct {
	// WorkspaceRoot is the directory commands run in. Required.
	WorkspaceRoot string

	// MaxOutputBytes caps the returned output. Non-positive values use
	// DefaultMaxOutputBytes (the bash default).
	MaxOutputBytes int
}

// NewPwshTool constructs the pwsh tool. The constructor never fails: an invalid
// configuration is reported by Invoke.
func NewPwshTool(cfg PwshConfig) runtime.Tool {
	t := &pwshTool{maxOutput: cfg.MaxOutputBytes}
	if t.maxOutput <= 0 {
		t.maxOutput = DefaultMaxOutputBytes
	}
	if strings.TrimSpace(cfg.WorkspaceRoot) == "" {
		t.initErr = fmt.Errorf("pwsh: WorkspaceRoot is required")
		return t
	}
	sb, err := newSandbox(cfg.WorkspaceRoot)
	if err != nil {
		t.initErr = fmt.Errorf("pwsh: %w", err)
		return t
	}
	t.root = sb.root
	return t
}

type pwshTool struct {
	root      string
	maxOutput int
	initErr   error
}

func (t *pwshTool) Name() string        { return "pwsh" }
func (t *pwshTool) Description() string { return pwshDescription }

// pwshExecutable returns the first PowerShell executable on PATH.
func pwshExecutable() (string, error) {
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil && path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("pwsh: PowerShell is not installed or not on PATH (looked for \"pwsh\" and \"powershell\"); install PowerShell 7+ and retry, or use bash for this command")
}

func (t *pwshTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if t.initErr != nil {
		return "", t.initErr
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("pwsh: command is required")
	}
	exe, err := pwshExecutable()
	if err != nil {
		return "", err
	}

	timeout := defaultBashTimeout
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
	cmd := exec.CommandContext(ctx, exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", a.Command)
	cmd.Dir = t.root
	cmd.Env = setEnv(scrubEnv(os.Environ()), "PWD", t.root)
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
			return text, fmt.Errorf("pwsh: command timed out after %s", timeout)
		}
		return text, fmt.Errorf("pwsh: command canceled: %w", ctxErr)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return text, fmt.Errorf("pwsh: command exited with status %d", exitErr.ExitCode())
		}
		return text, fmt.Errorf("pwsh: %w", runErr)
	}
	return text, nil
}
