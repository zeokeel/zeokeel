package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// WorkingDirectoryConfig configures the working_directory tool.
type WorkingDirectoryConfig struct {
	// WorkspaceRoot is the directory the tool may enter. It is required unless
	// State supplies an already-constructed state.
	WorkspaceRoot string
	// State is the shared current-directory state. Reuse one state across tools
	// that should observe the same directory; nil creates a private one.
	State *WorkingDirectoryState
}

// WorkingDirectoryState is the shared, atomically updated current directory.
// Other tools (or a future shell tool) can read it without touching the tool
// instance.
type WorkingDirectoryState struct {
	root     string
	sb       *sandbox
	snapshot atomic.Pointer[workingDirectorySnapshot]
}

// workingDirectorySnapshot is the immutable value held by the atomic pointer.
type workingDirectorySnapshot struct {
	abs string
	rel string
}

// NewWorkingDirectoryState resolves root and returns state whose current
// directory is the workspace root itself.
func NewWorkingDirectoryState(root string) (*WorkingDirectoryState, error) {
	sb, err := newSandbox(root)
	if err != nil {
		return nil, err
	}
	state := &WorkingDirectoryState{root: sb.root, sb: sb}
	state.snapshot.Store(&workingDirectorySnapshot{abs: sb.root, rel: "."})
	return state, nil
}

// Rel returns the current directory relative to the workspace root, using
// forward slashes. It returns "." at the root.
func (s *WorkingDirectoryState) Rel() string {
	if s == nil {
		return "."
	}
	snap := s.snapshot.Load()
	if snap == nil || snap.rel == "" {
		return "."
	}
	return snap.rel
}

// Root returns the resolved workspace root.
func (s *WorkingDirectoryState) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// Cwd returns the resolved absolute directory and its workspace-relative form.
// A zero-value or uninitialized state reports the current directory as ".".
func (s *WorkingDirectoryState) Cwd() (abs, rel string) {
	if s == nil {
		return "", "."
	}
	if snap := s.snapshot.Load(); snap != nil {
		return snap.abs, snap.rel
	}
	return s.root, "."
}

// Reset returns the current directory to the workspace root, keeping the
// state's root and sandbox. The app uses it when a new or reopened session
// takes over the shared list: a session must not inherit the subdirectory
// another session happened to cd into.
func (s *WorkingDirectoryState) Reset() {
	if s == nil || s.sb == nil {
		return
	}
	s.set(s.root, ".")
}

// SetRel restores the current directory to a workspace-relative path that was
// saved earlier (empty or "." means the workspace root). A path that no longer
// resolves to a directory resets to the root instead of leaving the state on a
// stale directory, so a deleted subdirectory can never strand the agent.
func (s *WorkingDirectoryState) SetRel(rel string) error {
	if s == nil || s.sb == nil {
		return errors.New("working_directory: state is not initialized")
	}
	clean := strings.TrimSpace(strings.TrimPrefix(rel, "./"))
	if clean == "" || clean == "." {
		s.Reset()
		return nil
	}
	abs, resolved, err := s.resolve(clean)
	if err != nil {
		s.Reset()
		return err
	}
	info, statErr := os.Stat(abs)
	if statErr != nil || !info.IsDir() {
		s.Reset()
		if statErr != nil {
			return statErr
		}
		return fmt.Errorf("working_directory: %q is not a directory", rel)
	}
	s.set(abs, resolved)
	return nil
}

// resolve maps a root-relative path to its absolute form.
func (s *WorkingDirectoryState) resolve(rel string) (string, string, error) {
	if s == nil || s.sb == nil {
		return "", "", errors.New("working_directory: state is not initialized")
	}
	abs, err := s.sb.resolve(rel)
	if err != nil {
		return "", "", err
	}
	return abs, s.sb.relative(abs), nil
}

// set publishes a new current directory.
func (s *WorkingDirectoryState) set(abs, rel string) {
	s.snapshot.Store(&workingDirectorySnapshot{abs: abs, rel: rel})
}

const workingDirectoryDescription = `Read or change the session's working directory inside the workspace root.

Call it with a JSON object:
  {}
  {"subdir":"src"}
  {"subdir":".."}   -- rejected

"subdir" is optional. Omitted or empty reads the current directory; otherwise
it must be a relative path to an existing directory under the workspace root.
Relative changes resolve against the current directory. Absolute paths and any
".." component are rejected, as is a path that leaves the workspace through a
symlink. "cd" is accepted as an alias for "subdir".

The tool returns {"kind":"working_directory","cwd":"<absolute>",
"relative":"<workspace-relative>","root":"<absolute>","changed":bool}. Other
file and shell tools take workspace-relative paths; pass "relative" to them,
not the absolute "cwd" (absolute paths are rejected). The state is shared
atomically with any tool built on the same WorkingDirectoryState.`

// WorkingDirectoryTool implements working_directory.
type WorkingDirectoryTool struct {
	root  string
	state *WorkingDirectoryState

	initOnce sync.Once
	initErr  error
}

// NewWorkingDirectoryTool returns the working_directory tool. State resolution
// is lazy so a bad root is reported as a tool error at call time.
func NewWorkingDirectoryTool(cfg WorkingDirectoryConfig) *WorkingDirectoryTool {
	return &WorkingDirectoryTool{root: cfg.WorkspaceRoot, state: cfg.State}
}

// Name implements runtime.Tool.
func (t *WorkingDirectoryTool) Name() string { return "working_directory" }

// Description implements runtime.Tool.
func (t *WorkingDirectoryTool) Description() string { return workingDirectoryDescription }

// State returns the shared directory state, initializing it on first use.
func (t *WorkingDirectoryTool) State() (*WorkingDirectoryState, error) {
	if t == nil {
		return nil, errors.New("working_directory: nil tool")
	}
	t.initOnce.Do(func() {
		if t.state != nil {
			return
		}
		t.state, t.initErr = NewWorkingDirectoryState(t.root)
	})
	if t.initErr != nil {
		return nil, t.initErr
	}
	if t.state == nil {
		return nil, errors.New("working_directory: no workspace root configured")
	}
	return t.state, nil
}

// workingDirectoryResult is the JSON document the tool returns.
type workingDirectoryResult struct {
	Kind     string `json:"kind"`
	Cwd      string `json:"cwd"`
	Relative string `json:"relative"`
	Root     string `json:"root"`
	Changed  bool   `json:"changed"`
}

// Invoke implements runtime.Tool.
func (t *WorkingDirectoryTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in struct {
		Subdir string `json:"subdir"`
		CD     string `json:"cd"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	state, err := t.State()
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}

	subdir := strings.TrimSpace(in.Subdir)
	if subdir == "" {
		subdir = strings.TrimSpace(in.CD)
	}
	if subdir == "" {
		abs, rel := state.Cwd()
		return workingDirectoryJSON(abs, rel, state.Root(), false)
	}

	clean, err := cleanRelativePath(subdir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	_, currentRel := state.Cwd()
	target := clean
	if clean == "." {
		// "." means the current directory, not the workspace root.
		target = currentRel
	} else if currentRel != "." && currentRel != "" {
		target = filepath.Join(currentRel, clean)
	}
	abs, rel, err := state.resolve(target)
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %q: %w", t.Name(), subdir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s: %q is not a directory", t.Name(), subdir)
	}
	state.set(abs, rel)
	return workingDirectoryJSON(abs, rel, state.Root(), true)
}

// workingDirectoryJSON encodes one result. The document is tiny, so it is
// marshaled directly rather than through the bounded helper.
func workingDirectoryJSON(abs, rel, root string, changed bool) (string, error) {
	encoded, err := json.Marshal(workingDirectoryResult{
		Kind:     "working_directory",
		Cwd:      abs,
		Relative: rel,
		Root:     root,
		Changed:  changed,
	})
	if err != nil {
		return "", fmt.Errorf("working_directory: encode result: %w", err)
	}
	return string(encoded), nil
}
