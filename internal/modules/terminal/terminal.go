// Package terminal implements ZeoKeel's terminal backend module: the simple
// command runner behind the right inspector's 终端 tab.
//
// It is a kernel backend module: it requires the host logger and optionally
// consumes the workspace service to run commands in the current workspace root.
// Every Run starts a fresh shell command (`<shell> -c <command>`) with no PTY and
// no persistent shell, mirroring the old shell's terminal panel. Output is
// combined stdout+stderr, bounded and UTF-8 safe; cancellation/timeouts kill the
// process and return the output captured so far.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/textenc"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/terminal"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// DefaultTimeout bounds one Run when Config.TimeoutMs is zero.
	DefaultTimeout = 30 * time.Second
	// DefaultMaxOutputBytes caps one Run output (256 KiB).
	DefaultMaxOutputBytes = 256 << 10
)

// shellCandidates is the ordered shell preference for one GOOS: the Windows
// console shells first (PowerShell 7, Windows PowerShell, then cmd), the POSIX
// shells otherwise. It is a pure function so both platforms' orders are unit
// tested without running on them.
func shellCandidates(goos string) []string {
	if goos == "windows" {
		return []string{"pwsh", "powershell", "cmd"}
	}
	return []string{"bash", "sh", "zsh"}
}

// shellArgs is the argv prefix one shell needs to run a single command string:
// cmd takes the command after /C, PowerShell 7 / Windows PowerShell after
// -NoProfile -Command (a profile would print extra output and slow every run),
// and the POSIX shells after -c. It is a pure function, unit tested per GOOS.
func shellArgs(goos, shell string) []string {
	if goos == "windows" {
		name := strings.ToLower(strings.TrimSuffix(shell, ".exe"))
		switch name {
		case "pwsh", "powershell":
			return []string{"-NoProfile", "-Command"}
		case "cmd":
			return []string{"/C"}
		default:
			return []string{"/C"}
		}
	}
	return []string{"-c"}
}

// Errors returned by the terminal service.
var (
	// ErrEmptyCommand reports a blank command.
	ErrEmptyCommand = errors.New("terminal: command is required")
	// ErrUnknownShell reports a shell name that is not available on this
	// machine.
	ErrUnknownShell = errors.New("terminal: unknown shell")
	// ErrNoShell reports Run without any available shell.
	ErrNoShell = errors.New("terminal: no shell available")
)

// Config is the module's typed assembly config.
type Config struct {
	// Shell is the preferred shell executable name (empty = the first
	// available shell in bash, sh, zsh, pwsh order).
	Shell string `json:"shell"`
	// TimeoutMs bounds one Run (0 = 30s).
	TimeoutMs int `json:"timeoutMs"`
	// MaxOutputBytes caps the returned combined output (0 = 256 KiB).
	MaxOutputBytes int `json:"maxOutputBytes"`
}

// Module is the terminal backend descriptor. It provides
// contracts.KeyTerminal.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return err
		}
		var workspace contracts.WorkspaceService
		if service, ok := ctx.Get(contracts.KeyWorkspace); ok {
			workspace = service
		}
		terminal, err := newService(cfg, logger, workspace)
		if err != nil {
			return fmt.Errorf("%s: %w", ModuleID, err)
		}
		if _, err := ctx.Provide(contracts.KeyTerminal, contracts.TerminalService(terminal)); err != nil {
			return fmt.Errorf("%s: provide terminal: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyTerminal)).
		AddRequires(module.Requires(contracts.KeyLogger)).
		WithName("ZeoKeel terminal runner").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if cfg.TimeoutMs < 0 {
		return fmt.Errorf("%s: timeoutMs must not be negative", ModuleID)
	}
	if cfg.MaxOutputBytes < 0 {
		return fmt.Errorf("%s: maxOutputBytes must not be negative", ModuleID)
	}
	if strings.ContainsRune(cfg.Shell, 0) {
		return fmt.Errorf("%s: shell contains a NUL byte", ModuleID)
	}
	return nil
}

// discoverShells filters the current platform's shell candidates through PATH.
func discoverShells() []string {
	candidates := shellCandidates(runtime.GOOS)
	shells := make([]string, 0, len(candidates))
	for _, name := range candidates {
		if _, err := exec.LookPath(name); err == nil {
			shells = append(shells, name)
		}
	}
	return shells
}

// service is the contracts.TerminalService implementation.
type service struct {
	mu        sync.Mutex
	shell     string
	shells    []string
	timeout   time.Duration
	maxOutput int
	logger    contracts.Logger
	workspace contracts.WorkspaceService
}

var _ contracts.TerminalService = (*service)(nil)

// newService resolves the shell configuration and defaults.
func newService(cfg Config, logger contracts.Logger, workspace contracts.WorkspaceService) (*service, error) {
	if logger == nil {
		logger = discardLogger{}
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if cfg.TimeoutMs == 0 {
		timeout = DefaultTimeout
	}
	maxOutput := cfg.MaxOutputBytes
	if maxOutput == 0 {
		maxOutput = DefaultMaxOutputBytes
	}
	shells := discoverShells()
	selected := ""
	if requested := strings.TrimSpace(cfg.Shell); requested != "" {
		for _, available := range shells {
			if available == requested {
				selected = available
				break
			}
		}
		if selected == "" {
			return nil, fmt.Errorf("%w: %q (available: %s)", ErrUnknownShell, requested, strings.Join(shells, ", "))
		}
	} else if len(shells) > 0 {
		selected = shells[0]
	}
	return &service{
		shell:     selected,
		shells:    shells,
		timeout:   timeout,
		maxOutput: maxOutput,
		logger:    logger,
		workspace: workspace,
	}, nil
}

// discardLogger drops terminal log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// WorkingDir implements contracts.TerminalService.
func (s *service) WorkingDir(ctx context.Context) string {
	if s.workspace != nil {
		if workspace, ok, err := s.workspace.Current(ctx); err == nil && ok {
			if root := strings.TrimSpace(workspace.Root); root != "" {
				return root
			}
		}
	}
	directory, err := os.Getwd()
	if err != nil {
		return "."
	}
	return directory
}

// Shell implements contracts.TerminalService.
func (s *service) Shell() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shell
}

// Shells implements contracts.TerminalService.
func (s *service) Shells() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.shells...)
}

// SetShell implements contracts.TerminalService.
func (s *service) SetShell(name string) error {
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		if len(s.shells) == 0 {
			s.shell = ""
			return nil
		}
		s.shell = s.shells[0]
		return nil
	}
	for _, available := range s.shells {
		if available == name {
			s.shell = available
			return nil
		}
	}
	return fmt.Errorf("%w: %q (available: %s)", ErrUnknownShell, name, strings.Join(s.shells, ", "))
}

// Run implements contracts.TerminalService: one fresh shell process in
// WorkingDir with combined output, cancellation/timeout support and bounded
// UTF-8-safe output.
func (s *service) Run(ctx context.Context, command string) (contracts.TerminalOutput, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return contracts.TerminalOutput{}, ErrEmptyCommand
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	shell := s.shell
	timeout := s.timeout
	maxOutput := s.maxOutput
	s.mu.Unlock()
	if shell == "" {
		return contracts.TerminalOutput{}, ErrNoShell
	}

	directory := s.WorkingDir(ctx)
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	s.logger.Printf("%s: run shell=%s dir=%s: %s", ModuleID, shell, directory, command)
	args := append(shellArgs(runtime.GOOS, shell), command)
	process := exec.CommandContext(runCtx, shell, args...)
	process.Dir = directory
	outputBytes, runErr := process.CombinedOutput()
	output := decodeCombinedOutput(outputBytes, maxOutput)

	exitCode := 0
	if runErr != nil {
		var exitError *exec.ExitError
		if errors.As(runErr, &exitError) {
			exitCode = exitError.ExitCode()
		}
	}
	if ctxErr := runCtx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			err := fmt.Errorf("terminal: command timed out after %s: %w", timeout, ctxErr)
			s.logger.Printf("%s: %v", ModuleID, err)
			return contracts.TerminalOutput{Output: output, ExitCode: -1}, err
		}
		err := fmt.Errorf("terminal: command canceled: %w", ctxErr)
		s.logger.Printf("%s: %v", ModuleID, err)
		return contracts.TerminalOutput{Output: output, ExitCode: -1}, err
	}
	if runErr != nil {
		err := fmt.Errorf("terminal: command exited with status %d", exitCode)
		if exitCode == 0 {
			err = fmt.Errorf("terminal: run command: %w", runErr)
		}
		s.logger.Printf("%s: %v", ModuleID, err)
		return contracts.TerminalOutput{Output: output, ExitCode: exitCode}, err
	}
	return contracts.TerminalOutput{Output: output, ExitCode: 0}, nil
}

// decodeCombinedOutput turns one command's combined stdout+stderr bytes into
// bounded, well-formed UTF-8 text. Windows console programs emit console-code-
// page bytes (GBK/cp936) and PowerShell may emit UTF-16LE, so the raw bytes are
// repaired before the byte budget is applied: truncating first could split a
// multi-byte character and leave mojibake in the Terminal pane.
func decodeCombinedOutput(outputBytes []byte, max int) string {
	return truncateUTF8(textenc.RepairBytes(outputBytes), max)
}

// truncateUTF8 bounds text to max bytes without splitting a UTF-8 rune.
func truncateUTF8(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
