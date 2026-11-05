package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/winproc"
)

// Git worktree management. The tools create, list and remove linked
// worktrees below <workspace>/.zeokeel-worktrees so that an agent can work on
// a branch without disturbing the main checkout. Every path argument is
// confined to the workspace root and every mutation is performed by the
// system git executable (never by manual file manipulation), so Git remains
// the single source of truth for worktree metadata.
const (
	// worktreeDirName is the managed directory below the workspace root.
	worktreeDirName = ".zeokeel-worktrees"

	// worktreeGitTimeout bounds one git invocation. A worktree add on a large
	// repository can be slow, but it must never hang a turn.
	worktreeGitTimeout = 30 * time.Second

	// worktreeMaxGitErrorBytes caps the git stderr included in an error.
	worktreeMaxGitErrorBytes = 4 * 1024
)

// WorktreeConfig configures the git worktree tools.
type WorktreeConfig struct {
	// WorkspaceRoot is the directory the tools operate in. It must be inside
	// (or be) a Git repository; invalid configuration is reported when a tool
	// is invoked rather than at construction so the caller can always wire
	// the tools unconditionally.
	WorkspaceRoot string
}

// NewWorktreeTools returns enter_worktree, exit_worktree and list_worktrees
// bound to cfg.WorkspaceRoot. The tools hold no state between calls; Git owns
// all worktree state.
func NewWorktreeTools(cfg WorktreeConfig) []runtime.Tool {
	opts := worktreeOptions{root: cfg.WorkspaceRoot, timeout: worktreeGitTimeout}
	return []runtime.Tool{
		&enterWorktreeTool{opts: opts},
		&exitWorktreeTool{opts: opts},
		&listWorktreesTool{opts: opts},
	}
}

// worktreeNamePattern keeps a name to one safe path element and one safe Git
// branch component. It rejects absolute paths, separators, traversal, option
// injection (a leading '-'), whitespace and control characters.
var worktreeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validateWorktreeName reports why name cannot be used as both branch name and
// worktree directory.
func validateWorktreeName(name string) error {
	if name == "" {
		return errors.New("worktree name is empty")
	}
	if !worktreeNamePattern.MatchString(name) {
		return fmt.Errorf("invalid worktree name %q: use 1-64 letters, digits, '.', '_' or '-' (must start with a letter or digit; no path separators or leading '.')", name)
	}
	return nil
}

const enterWorktreeDescription = `Create a Git worktree on a new branch off the current HEAD.

The worktree is created below the workspace at
.zeokeel-worktrees/<name> and the branch is named <name>. "name" is optional;
a unique name is generated when it is omitted. Existing branches and existing
paths are rejected, and the name must be one safe path element (no "/", "\\",
"..", leading '-' or whitespace). The workspace root must be a Git repository
with at least one commit. Uncommitted files stay in the main checkout.

The result is JSON: {kind,path,branch,base_commit,repository_root,
worktree_root}. Use exit_worktree with the returned path to remove it.

Args JSON schema:
{"type":"object","properties":{"name":{"type":"string","description":"New branch name, also used as the worktree directory name."}},"additionalProperties":false}`

const exitWorktreeDescription = `Remove a Git worktree previously created below .zeokeel-worktrees.

Identify it by "path" (the absolute or workspace-relative path returned by
enter_worktree) or by "name". The path must be inside the workspace root and
must be a registered worktree of this repository; the main checkout can never
be removed. The worktree directory is removed with "git worktree remove
--force". The branch is deleted as well unless "keep_branch" is true; if the
branch cannot be deleted, the worktree is still removed and a "warning" is
returned.

Args JSON schema:
{"type":"object","properties":{"path":{"type":"string","description":"Worktree path returned by enter_worktree."},"name":{"type":"string","description":"Worktree name, used when path is omitted."},"keep_branch":{"type":"boolean","description":"Keep the branch instead of deleting it."}},"additionalProperties":false}`

const listWorktreesDescription = `List the Git worktrees of the workspace repository.

Runs "git worktree list --porcelain" and returns JSON:
{kind,repository_root,worktree_root,count,worktrees:[{path,head,branch,
branch_ref,detached,bare,locked,lock_reason,prunable,prunable_reason,
managed}]}. "managed" is true when the checkout is below
.zeokeel-worktrees. The call is read-only.

Args JSON schema:
{"type":"object","properties":{},"additionalProperties":false}`

// worktreeOptions is the immutable configuration shared by the three tools.
type worktreeOptions struct {
	root    string
	timeout time.Duration
}

// worktreeRepo is a validated view of the workspace repository. root is the
// symlink-resolved workspace root, top is Git's toplevel, and base is the
// resolved managed directory (which may not exist yet).
type worktreeRepo struct {
	root string
	top  string
	base string
}

// openWorktreeRepo resolves root, verifies that it is a directory inside a Git
// repository, and confines the managed worktree directory to root.
func openWorktreeRepo(ctx context.Context, root string, timeout time.Duration) (*worktreeRepo, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root %q: %w", root, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root %q: %w", root, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("workspace root %q: %w", real, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %q is not a directory", real)
	}
	top, err := gitOutput(ctx, real, timeout, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%q is not a Git repository: %w", real, err)
	}
	base, err := resolveExisting(filepath.Join(real, worktreeDirName))
	if err != nil {
		return nil, fmt.Errorf("resolve worktree directory: %w", err)
	}
	if err := ensurePathWithin(real, base); err != nil {
		return nil, fmt.Errorf("worktree directory %q: %w", base, err)
	}
	return &worktreeRepo{root: real, top: strings.TrimSpace(top), base: base}, nil
}

// target resolves the exit_worktree path/name arguments to an absolute path
// confined to the workspace root.
func (r *worktreeRepo) target(pathArg, nameArg string) (string, error) {
	pathArg = strings.TrimSpace(pathArg)
	nameArg = strings.TrimSpace(nameArg)
	if pathArg == "" && nameArg == "" {
		return "", errors.New(`one of "path" or "name" is required`)
	}

	var target string
	if pathArg != "" {
		if strings.IndexByte(pathArg, 0) >= 0 {
			return "", errors.New("path must not contain a NUL byte")
		}
		p := pathArg
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.root, p)
		}
		p = filepath.Clean(p)
		// Resolve symlinks when the checkout exists so a link cannot smuggle
		// an outside directory past the lexical containment check.
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve path %q: %w", pathArg, err)
		}
		if err := ensurePathWithin(r.root, p); err != nil {
			return "", fmt.Errorf("path %q: %w", pathArg, err)
		}
		target = p
	} else {
		if err := validateWorktreeName(nameArg); err != nil {
			return "", err
		}
		target = filepath.Join(r.base, nameArg)
	}

	if samePath(target, r.root) {
		return "", errors.New("refusing to remove the workspace root")
	}
	return target, nil
}

// list returns the parsed worktree list.
func (r *worktreeRepo) list(ctx context.Context, timeout time.Duration) ([]worktreeView, error) {
	raw, err := gitOutput(ctx, r.root, timeout, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreePorcelain(raw, r.base), nil
}

// enterWorktreeTool creates a worktree and branch.
type enterWorktreeTool struct {
	opts worktreeOptions
}

var _ runtime.Tool = (*enterWorktreeTool)(nil)

func (t *enterWorktreeTool) Name() string        { return "enter_worktree" }
func (t *enterWorktreeTool) Description() string { return enterWorktreeDescription }

// enterWorktreeResult is the JSON result of enter_worktree.
type enterWorktreeResult struct {
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	Branch         string `json:"branch"`
	BaseCommit     string `json:"base_commit"`
	RepositoryRoot string `json:"repository_root"`
	WorktreeRoot   string `json:"worktree_root"`
}

func (t *enterWorktreeTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("enter_worktree: tool is not initialized")
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}

	if ctx == nil {
		ctx = context.Background()
	}
	repo, err := openWorktreeRepo(ctx, t.opts.root, t.opts.timeout)
	if err != nil {
		return "", fmt.Errorf("enter_worktree: %w", err)
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = generatedWorktreeName()
	}
	if err := validateWorktreeName(name); err != nil {
		return "", fmt.Errorf("enter_worktree: %w", err)
	}
	target := filepath.Join(repo.base, name)
	if err := ensurePathWithin(repo.base, target); err != nil {
		return "", fmt.Errorf("enter_worktree: %w", err)
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("enter_worktree: worktree path %q already exists", target)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("enter_worktree: inspect %q: %w", target, err)
	}
	branchRef := "refs/heads/" + name
	exists, err := gitRefExists(ctx, repo.root, branchRef, t.opts.timeout)
	if err != nil {
		return "", fmt.Errorf("enter_worktree: check branch %q: %w", name, err)
	}
	if exists {
		return "", fmt.Errorf("enter_worktree: branch %q already exists", name)
	}
	baseCommit, err := gitOutput(ctx, repo.root, t.opts.timeout, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("enter_worktree: repository has no usable HEAD commit: %w", err)
	}
	if err := os.MkdirAll(repo.base, 0o755); err != nil {
		return "", fmt.Errorf("enter_worktree: create worktree directory %q: %w", repo.base, err)
	}
	if _, err := gitOutput(ctx, repo.root, t.opts.timeout, "worktree", "add", "-b", name, target, "HEAD"); err != nil {
		// The target did not exist before this call; clean up the partial
		// checkout so a retry is not blocked by a half-created directory.
		_ = os.RemoveAll(target)
		return "", fmt.Errorf("enter_worktree: %w", err)
	}

	result := enterWorktreeResult{
		Kind:           "worktree_enter",
		Path:           target,
		Branch:         name,
		BaseCommit:     strings.TrimSpace(baseCommit),
		RepositoryRoot: repo.top,
		WorktreeRoot:   repo.base,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("enter_worktree: encode result: %w", err)
	}
	return string(encoded), nil
}

// exitWorktreeTool removes a worktree and optionally its branch.
type exitWorktreeTool struct {
	opts worktreeOptions
}

var _ runtime.Tool = (*exitWorktreeTool)(nil)

func (t *exitWorktreeTool) Name() string        { return "exit_worktree" }
func (t *exitWorktreeTool) Description() string { return exitWorktreeDescription }

// exitWorktreeResult is the JSON result of exit_worktree.
type exitWorktreeResult struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	Branch        string `json:"branch,omitempty"`
	BranchDeleted bool   `json:"branch_deleted"`
	BranchKept    bool   `json:"branch_kept,omitempty"`
	Warning       string `json:"warning,omitempty"`
}

func (t *exitWorktreeTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("exit_worktree: tool is not initialized")
	}
	var in struct {
		Path       string `json:"path"`
		Name       string `json:"name"`
		KeepBranch bool   `json:"keep_branch"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	repo, err := openWorktreeRepo(ctx, t.opts.root, t.opts.timeout)
	if err != nil {
		return "", fmt.Errorf("exit_worktree: %w", err)
	}
	target, err := repo.target(in.Path, in.Name)
	if err != nil {
		return "", fmt.Errorf("exit_worktree: %w", err)
	}
	views, err := repo.list(ctx, t.opts.timeout)
	if err != nil {
		return "", fmt.Errorf("exit_worktree: %w", err)
	}
	entry, ok := findWorktree(views, target)
	if !ok {
		return "", fmt.Errorf("exit_worktree: %q is not a registered worktree of %q", target, repo.top)
	}
	if samePath(entry.Path, repo.top) || samePath(entry.Path, repo.root) {
		return "", errors.New("exit_worktree: refusing to remove the main working tree")
	}
	if _, err := gitOutput(ctx, repo.root, t.opts.timeout, "worktree", "remove", "--force", entry.Path); err != nil {
		return "", fmt.Errorf("exit_worktree: %w", err)
	}

	result := exitWorktreeResult{
		Kind:   "worktree_exit",
		Path:   entry.Path,
		Branch: entry.Branch,
	}
	switch {
	case in.KeepBranch:
		result.BranchKept = entry.Branch != ""
	case strings.HasPrefix(entry.BranchRef, "refs/heads/"):
		if _, err := gitOutput(ctx, repo.root, t.opts.timeout, "branch", "-D", "--", entry.Branch); err != nil {
			result.Warning = fmt.Sprintf("worktree removed but branch %q was not deleted: %v", entry.Branch, err)
		} else {
			result.BranchDeleted = true
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("exit_worktree: encode result: %w", err)
	}
	return string(encoded), nil
}

// listWorktreesTool lists repository worktrees.
type listWorktreesTool struct {
	opts worktreeOptions
}

var _ runtime.Tool = (*listWorktreesTool)(nil)

func (t *listWorktreesTool) Name() string        { return "list_worktrees" }
func (t *listWorktreesTool) Description() string { return listWorktreesDescription }

// worktreeView is one parsed "git worktree list --porcelain" record.
type worktreeView struct {
	Path           string `json:"path"`
	Head           string `json:"head,omitempty"`
	Branch         string `json:"branch,omitempty"`
	BranchRef      string `json:"branch_ref,omitempty"`
	Detached       bool   `json:"detached,omitempty"`
	Bare           bool   `json:"bare,omitempty"`
	Locked         bool   `json:"locked,omitempty"`
	LockReason     string `json:"lock_reason,omitempty"`
	Prunable       bool   `json:"prunable,omitempty"`
	PrunableReason string `json:"prunable_reason,omitempty"`
	Managed        bool   `json:"managed"`
}

// listWorktreesResult is the JSON result of list_worktrees.
type listWorktreesResult struct {
	Kind           string         `json:"kind"`
	RepositoryRoot string         `json:"repository_root"`
	WorktreeRoot   string         `json:"worktree_root"`
	Count          int            `json:"count"`
	Worktrees      []worktreeView `json:"worktrees"`
}

func (t *listWorktreesTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("list_worktrees: tool is not initialized")
	}
	var in struct{}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	repo, err := openWorktreeRepo(ctx, t.opts.root, t.opts.timeout)
	if err != nil {
		return "", fmt.Errorf("list_worktrees: %w", err)
	}
	views, err := repo.list(ctx, t.opts.timeout)
	if err != nil {
		return "", fmt.Errorf("list_worktrees: %w", err)
	}
	if views == nil {
		views = []worktreeView{}
	}
	result := listWorktreesResult{
		Kind:           "worktree_list",
		RepositoryRoot: repo.top,
		WorktreeRoot:   repo.base,
		Count:          len(views),
		Worktrees:      views,
	}
	return boundedToolJSON(result, DefaultMaxOutputBytes, "list_worktrees")
}

// parseWorktreePorcelain parses the block format emitted by
// "git worktree list --porcelain".
func parseWorktreePorcelain(raw, base string) []worktreeView {
	views := make([]worktreeView, 0, 4)
	var current *worktreeView
	flush := func() {
		if current != nil && current.Path != "" {
			current.Managed = pathWithin(base, current.Path)
			views = append(views, *current)
		}
		current = nil
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if current == nil {
			current = &worktreeView{}
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			current.BranchRef = ref
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "detached":
			current.Detached = true
		case line == "bare":
			current.Bare = true
		case strings.HasPrefix(line, "locked"):
			current.Locked = true
			current.LockReason = strings.TrimSpace(strings.TrimPrefix(line, "locked"))
		case strings.HasPrefix(line, "prunable"):
			current.Prunable = true
			current.PrunableReason = strings.TrimSpace(strings.TrimPrefix(line, "prunable"))
		}
	}
	flush()
	return views
}

// findWorktree returns the registered worktree whose path matches target.
func findWorktree(views []worktreeView, target string) (worktreeView, bool) {
	want := canonicalPath(target)
	for _, view := range views {
		if canonicalPath(view.Path) == want {
			return view, true
		}
	}
	return worktreeView{}, false
}

// gitRefExists reports whether ref exists. It distinguishes "missing" (git
// exit status 1) from an operational failure.
func gitRefExists(ctx context.Context, dir, ref string, timeout time.Duration) (bool, error) {
	args := []string{"show-ref", "--verify", "--quiet", ref}
	result, err := runGit(ctx, dir, timeout, args...)
	if err != nil {
		return false, err
	}
	switch result.exitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, result.commandError(args)
	}
}

// gitResult is the captured output of one git invocation. A non-zero exit is
// not an error here; callers decide whether the status is meaningful.
type gitResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// commandError renders a git failure without exposing the environment.
func (r gitResult) commandError(args []string) error {
	detail := strings.TrimSpace(r.stderr)
	if detail == "" {
		detail = strings.TrimSpace(r.stdout)
	}
	detail = truncateBytes(detail, worktreeMaxGitErrorBytes, "git output truncated")
	command := "git " + strings.Join(args, " ")
	if detail == "" {
		return fmt.Errorf("%s failed with exit status %d", command, r.exitCode)
	}
	return fmt.Errorf("%s failed with exit status %d: %s", command, r.exitCode, detail)
}

// runGit runs git in dir with a scrubbed environment and a timeout. A non-zero
// git exit is returned in gitResult with a nil error.
func runGit(ctx context.Context, dir string, timeout time.Duration, args ...string) (gitResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = worktreeGitTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = dir
	cmd.Env = worktreeGitEnv()
	winproc.Hide(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	result := gitResult{stdout: stdout.String(), stderr: stderr.String()}
	if err := runCtx.Err(); err != nil {
		command := "git " + strings.Join(args, " ")
		if errors.Is(err, context.DeadlineExceeded) {
			return result, fmt.Errorf("%s timed out after %s", command, timeout)
		}
		return result, fmt.Errorf("%s canceled: %w", command, err)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			result.exitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, fmt.Errorf("run git: %w", runErr)
	}
	return result, nil
}

// gitOutput runs git and returns trimmed stdout, converting a non-zero exit to
// a descriptive error.
func gitOutput(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	result, err := runGit(ctx, dir, timeout, args...)
	if err != nil {
		return "", err
	}
	if result.exitCode != 0 {
		return "", result.commandError(args)
	}
	return strings.TrimSpace(result.stdout), nil
}

// worktreeGitEnv returns an environment with ZeoKeel/provider secrets removed
// and any inherited GIT_DIR-style overrides dropped, so git always operates on
// the checkout selected by dir.
func worktreeGitEnv() []string {
	env := scrubEnv(os.Environ())
	for _, key := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_PREFIX",
	} {
		env = unsetEnv(env, key)
	}
	env = setEnv(env, "GIT_TERMINAL_PROMPT", "0")
	env = setEnv(env, "LC_ALL", "C")
	return env
}

// unsetEnv removes key (case-insensitively) from env.
func unsetEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
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
	return out
}

// generatedWorktreeName returns a unique-looking branch name.
func generatedWorktreeName() string {
	var suffix [3]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("wt-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("wt-%s-%x", time.Now().UTC().Format("20060102-150405"), suffix)
}

// ensurePathWithin reports whether path is lexically inside root. Both must be
// absolute for a meaningful result.
func ensurePathWithin(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("%q is outside the workspace root", path)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%q is outside the workspace root", path)
	}
	return nil
}

// pathWithin reports whether path resolves inside root.
func pathWithin(root, path string) bool {
	return ensurePathWithin(canonicalPath(root), canonicalPath(path)) == nil
}

// canonicalPath resolves symlinks when possible and returns a clean absolute
// path. It is used only for comparing checkout paths.
func canonicalPath(path string) string {
	if path == "" {
		return ""
	}
	p := path
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

// samePath reports whether two checkout paths refer to the same location.
func samePath(a, b string) bool {
	return canonicalPath(a) == canonicalPath(b)
}
