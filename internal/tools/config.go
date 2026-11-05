package tools

import (
	"fmt"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

const (
	// DefaultMaxOutputBytes is the default cap on a single tool result (64 KiB).
	DefaultMaxOutputBytes = 64 * 1024

	// MaxReadFileBytes is the largest file read_file and edit_file accept.
	MaxReadFileBytes = 1 << 20

	// MaxFetchBytes is the hard cap on an HTTP response body web_fetch reads.
	MaxFetchBytes = 2 << 20
)

// Config configures the built-in tool set.
type Config struct {
	// WorkspaceRoot is the directory file-system tools are confined to. It is
	// required and must exist (a symlink to a directory is resolved).
	WorkspaceRoot string

	// AllowShell enables the shell tool group: bash plus the write_file and
	// edit_file mutations. Disabled by default.
	AllowShell bool

	// ReadOnly omits every mutating tool (write_file, edit_file, bash) so an
	// explicit Read Only permission mode cannot modify the workspace.
	ReadOnly bool

	// DisableFileMutations omits write_file and edit_file while keeping the
	// read-only file tools. The Shell plugin uses it to control the whole
	// bash/write_file/edit_file group; ReadOnly takes precedence.
	DisableFileMutations bool

	// MaxOutputBytes caps the bytes returned by the shell and web tools.
	// Non-positive values use DefaultMaxOutputBytes.
	MaxOutputBytes int

	// BashTimeout is the default bash timeout. Non-positive values use
	// defaultBashTimeout (30s); the per-call argument overrides it up to
	// maxBashTimeout.
	BashTimeout time.Duration

	// DirState shares the current working directory with working_directory,
	// read/write/edit/glob/grep and bash. Nil roots every relative path at
	// WorkspaceRoot.
	DirState *WorkingDirectoryState
}

// Builtins returns the configured tool set. It returns an error when the
// workspace root is missing or unusable. The bash tool is omitted when
// cfg.AllowShell is false.
func Builtins(cfg Config) ([]runtime.Tool, error) {
	sb, err := newSandbox(cfg.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	maxOutput := cfg.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}

	if cfg.DirState != nil {
		sb.cwd = cfg.DirState.Rel
	}
	builtins := []runtime.Tool{
		&readFileTool{sb: sb},
		&globTool{sb: sb},
		&grepTool{sb: sb},
	}
	mutatingFiles := !cfg.ReadOnly && !cfg.DisableFileMutations
	if mutatingFiles {
		builtins = append(builtins, &writeFileTool{sb: sb}, &editFileTool{sb: sb})
	}
	if cfg.AllowShell && mutatingFiles {
		builtins = append(builtins, newBashTool(sb.root, maxOutput).withTimeout(cfg.BashTimeout).withCwd(sb.cwd))
	}
	builtins = append(builtins, newWebFetchTool(maxOutput, false))
	return builtins, nil
}

// Register builds the tool set with Builtins and registers every tool in reg.
// It returns the first registration error encountered.
func Register(reg *runtime.Registry, cfg Config) error {
	if reg == nil {
		return fmt.Errorf("tools: cannot register into a nil runtime.Registry")
	}
	builtins, err := Builtins(cfg)
	if err != nil {
		return err
	}
	for _, tool := range builtins {
		if err := reg.Register(tool); err != nil {
			return fmt.Errorf("tools: register %q: %w", tool.Name(), err)
		}
	}
	return nil
}
