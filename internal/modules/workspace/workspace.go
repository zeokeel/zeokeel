// Package workspace implements ZeoKeel's workspace backend module: the durable
// project-root registry (the old shell's workspaces) and the sandboxed file
// reader/search behind the Files inspector.
//
// The registry, its atomic persistence (temporary file + Sync + rename), the
// per-session current workspace and the sandboxed Files / ReadFile / Search
// implementation live in the Rig SDK (github.com/zeokeel/zeokeel-rig/workspace).
// This module is the kernel wiring: it requires the product data directory and
// host logger, provides contracts.KeyWorkspace, converts SDK types to the
// product contracts, and keeps the git-backed WorkspaceDiffer (the old shell's
// workspace-changes view), which is product policy the SDK deliberately does
// not implement.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rigworkspace "github.com/zeokeel/zeokeel-rig/workspace"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/workspacecleanup"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/workspace"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// DefaultFileName is the registry file name under the product data
	// directory (the SDK's default).
	DefaultFileName = rigworkspace.DefaultFileName
	// DefaultReadLimitBytes caps one ReadFile (1 MiB, the SDK's default).
	DefaultReadLimitBytes = rigworkspace.DefaultReadLimitBytes
	// DefaultSearchDepth is how many directory levels Search descends into
	// (the SDK's default).
	DefaultSearchDepth = rigworkspace.DefaultSearchDepth
	// DefaultSearchLimit caps one Search result set (the SDK's default).
	DefaultSearchLimit = rigworkspace.DefaultSearchLimit
	// DefaultMaxChanges caps one Changes result set (100 files).
	DefaultMaxChanges = 100
	// DefaultMaxDiffBytes caps the total unified diff returned by Changes
	// (256 KiB).
	DefaultMaxDiffBytes = 256 << 10
)

// Config is the module's typed assembly config.
type Config struct {
	// Path is the registry file: absolute, or relative to the product data
	// directory (default workspaces.json there).
	Path string `json:"path"`
	// ReadLimitBytes caps one ReadFile (0 = default 1 MiB).
	ReadLimitBytes int `json:"readLimitBytes"`
	// SearchDepth bounds the recursive Search directory depth (0 = default 6).
	SearchDepth int `json:"searchDepth"`
	// MaxChanges caps one Changes result set (0 = default 100).
	MaxChanges int `json:"maxChanges"`
	// MaxDiffBytes caps the total unified diff returned by Changes (0 = default
	// 256 KiB).
	MaxDiffBytes int `json:"maxDiffBytes"`
}

// Errors returned by the workspace service. They are the Rig SDK sentinels so
// hosts and modules can match errors regardless of which layer produced them.
var (
	// ErrNoWorkspace reports a file operation without a current workspace.
	ErrNoWorkspace = rigworkspace.ErrNoWorkspace
	// ErrWorkspaceNotFound reports an unknown workspace ID.
	ErrWorkspaceNotFound = rigworkspace.ErrWorkspaceNotFound
	// ErrPathOutsideRoot reports a path escaping the workspace root.
	ErrPathOutsideRoot = rigworkspace.ErrPathOutsideRoot
	// ErrNotDirectory reports a root or directory argument that is not a
	// directory.
	ErrNotDirectory = rigworkspace.ErrNotDirectory
	// ErrNotFile reports a ReadFile target that is not a regular file.
	ErrNotFile = rigworkspace.ErrNotFile
	// ErrInvalidLimit reports a negative ReadFile/Search limit.
	ErrInvalidLimit = rigworkspace.ErrInvalidLimit
)

// Module is the workspace backend descriptor. It provides
// contracts.KeyWorkspace.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data directory: %w", ModuleID, err)
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		service, err := newService(dataDir, cfg, logger)
		if err != nil {
			return fmt.Errorf("%s: open workspace registry: %w", ModuleID, err)
		}
		// Optional: deleting a workspace also forgets the product settings it
		// owned (never the folder). A host without settings keeps plain registry
		// deletion.
		if settings, ok := ctx.Get(contracts.KeySettings); ok {
			service.settings = settings
		}
		if _, err := ctx.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(service)); err != nil {
			return fmt.Errorf("%s: provide workspace service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyWorkspace)).
		AddRequires(
			module.Requires(contracts.KeyDataDir),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel workspace registry").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if strings.ContainsRune(cfg.Path, 0) {
		return fmt.Errorf("%s: path contains a NUL byte", ModuleID)
	}
	if cfg.ReadLimitBytes < 0 {
		return fmt.Errorf("%s: readLimitBytes must not be negative", ModuleID)
	}
	if cfg.SearchDepth < 0 {
		return fmt.Errorf("%s: searchDepth must not be negative", ModuleID)
	}
	if cfg.MaxChanges < 0 {
		return fmt.Errorf("%s: maxChanges must not be negative", ModuleID)
	}
	if cfg.MaxDiffBytes < 0 {
		return fmt.Errorf("%s: maxDiffBytes must not be negative", ModuleID)
	}
	return nil
}

// Service adapts the Rig SDK workspace service to contracts.WorkspaceService
// and adds the product-only git differ.
type Service struct {
	service      *rigworkspace.Service
	maxChanges   int
	maxDiffBytes int

	// settings, when provided by the host, is purged of this workspace's scoped
	// rows on Delete (the workspace folder is never touched).
	settings contracts.SettingsService
	logger   contracts.Logger
}

var (
	_ contracts.WorkspaceService = (*Service)(nil)
	_ contracts.WorkspaceDiffer  = (*Service)(nil)
	_ contracts.WorkspaceRenamer = (*Service)(nil)
)

// newService builds the SDK registry and the product adapter. The SDK resolves
// the registry path, quarantines a corrupt or unreadable document and owns the
// atomic persistence; the product only keeps the git-diff limits.
func newService(dataDir string, cfg Config, logger contracts.Logger) (*Service, error) {
	var logf func(format string, args ...any)
	if logger != nil {
		logf = logger.Printf
	}
	service, err := rigworkspace.New(dataDir, rigworkspace.Config{
		Path:           cfg.Path,
		ReadLimitBytes: cfg.ReadLimitBytes,
		SearchDepth:    cfg.SearchDepth,
		Logger:         logf,
	})
	if err != nil {
		return nil, err
	}
	maxChanges := cfg.MaxChanges
	if maxChanges == 0 {
		maxChanges = DefaultMaxChanges
	}
	maxDiffBytes := cfg.MaxDiffBytes
	if maxDiffBytes == 0 {
		maxDiffBytes = DefaultMaxDiffBytes
	}
	return &Service{
		service:      service,
		maxChanges:   maxChanges,
		maxDiffBytes: maxDiffBytes,
		logger:       logger,
	}, nil
}

// Path returns the resolved registry file path (diagnostics and tests).
func (s *Service) Path() string { return s.service.Path() }

// contractWorkspace converts one SDK workspace to the product type.
func contractWorkspace(workspace rigworkspace.Workspace) contracts.Workspace {
	return contracts.Workspace{
		ID:        workspace.ID,
		Name:      workspace.Name,
		Root:      workspace.Root,
		CreatedAt: workspace.CreatedAt,
		Color:     workspace.Color,
	}
}

// contractWorkspaces converts an SDK workspace slice to the product type.
func contractWorkspaces(workspaces []rigworkspace.Workspace) []contracts.Workspace {
	converted := make([]contracts.Workspace, 0, len(workspaces))
	for _, workspace := range workspaces {
		converted = append(converted, contractWorkspace(workspace))
	}
	return converted
}

// contractFileEntries converts SDK file entries to the product type.
func contractFileEntries(entries []rigworkspace.FileEntry) []contracts.FileEntry {
	converted := make([]contracts.FileEntry, 0, len(entries))
	for _, entry := range entries {
		converted = append(converted, contracts.FileEntry{
			Name:    entry.Name,
			Path:    entry.Path,
			Dir:     entry.Dir,
			Size:    entry.Size,
			ModTime: entry.ModTime,
		})
	}
	return converted
}

// contextErr returns ctx.Err(), tolerating a nil context.
func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// --- registry operations ---------------------------------------------------

// List implements contracts.WorkspaceService in insertion order.
func (s *Service) List(ctx context.Context) ([]contracts.Workspace, error) {
	workspaces, err := s.service.List(ctx)
	if err != nil {
		return nil, err
	}
	return contractWorkspaces(workspaces), nil
}

// Current implements contracts.WorkspaceService.
func (s *Service) Current(ctx context.Context) (contracts.Workspace, bool, error) {
	workspace, ok, err := s.service.Current(ctx)
	if err != nil || !ok {
		return contracts.Workspace{}, false, err
	}
	return contractWorkspace(workspace), true, nil
}

// Create implements contracts.WorkspaceService. The root must exist and be a
// directory; an already registered canonical root is reused and becomes
// current.
func (s *Service) Create(ctx context.Context, name, root string) (contracts.Workspace, error) {
	workspace, err := s.service.Create(ctx, name, root)
	if err != nil {
		return contracts.Workspace{}, err
	}
	return contractWorkspace(workspace), nil
}

// Open implements contracts.WorkspaceService: it makes an existing workspace
// current.
func (s *Service) Open(ctx context.Context, id string) (contracts.Workspace, error) {
	workspace, err := s.service.Open(ctx, id)
	if err != nil {
		return contracts.Workspace{}, err
	}
	return contractWorkspace(workspace), nil
}

// Delete implements contracts.WorkspaceService: the registration is removed
// and the workspace's scoped product settings are purged; the files under its
// root are not touched. Cleanup failures are logged (not returned): the
// workspace is already gone by then, and failing the delete would misreport it.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.service.Delete(ctx, id); err != nil {
		return err
	}
	if s.settings != nil {
		if err := workspacecleanup.Settings(s.settings, id); err != nil && s.logger != nil {
			s.logger.Printf("%s: clean settings of deleted workspace %q: %v", ModuleID, id, err)
		}
	}
	return nil
}

// RenameWorkspace implements contracts.WorkspaceRenamer: it changes one
// workspace's display name and persists the registry atomically. The ID, root
// and creation time are preserved, and so is the current selection. An empty
// name and a name another workspace already uses (trimmed, case-insensitive)
// are rejected with the exported contracts sentinels so a UI can localize them;
// a rejected rename writes nothing.
func (s *Service) RenameWorkspace(ctx context.Context, id, name string) (contracts.Workspace, error) {
	renamed, err := s.service.Rename(ctx, id, name)
	switch {
	case err == nil:
		return contractWorkspace(renamed), nil
	case errors.Is(err, rigworkspace.ErrNameEmpty):
		return contracts.Workspace{}, fmt.Errorf("%w: %q", contracts.ErrWorkspaceNameEmpty, strings.TrimSpace(id))
	case errors.Is(err, rigworkspace.ErrNameTaken):
		return contracts.Workspace{}, fmt.Errorf("%w: %q", contracts.ErrWorkspaceNameTaken, strings.TrimSpace(name))
	}
	return contracts.Workspace{}, err
}

// currentWorkspaceRoot returns the current workspace and its canonical root.
// SetWorkspaceColor implements contracts.WorkspaceColorer.
func (s *Service) SetWorkspaceColor(ctx context.Context, id, color string) (contracts.Workspace, error) {
	updated, err := s.service.SetColor(ctx, id, color)
	if err != nil {
		return contracts.Workspace{}, err
	}
	return contractWorkspace(updated), nil
}

func (s *Service) currentWorkspaceRoot() (contracts.Workspace, string, error) {
	workspace, ok, err := s.service.Current(context.Background())
	if err != nil {
		return contracts.Workspace{}, "", err
	}
	if !ok {
		return contracts.Workspace{}, "", ErrNoWorkspace
	}
	root, err := filepath.EvalSymlinks(workspace.Root)
	if err != nil {
		return contracts.Workspace{}, "", fmt.Errorf("%s: workspace root %q: %w", ModuleID, workspace.Root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return contracts.Workspace{}, "", fmt.Errorf("%s: workspace root %q: %w", ModuleID, workspace.Root, err)
	}
	if !info.IsDir() {
		return contracts.Workspace{}, "", fmt.Errorf("%w: %q", ErrNotDirectory, workspace.Root)
	}
	return contractWorkspace(workspace), filepath.Clean(root), nil
}

// --- sandboxed file access -------------------------------------------------

// Files implements contracts.WorkspaceService.
func (s *Service) Files(ctx context.Context, dir string) ([]contracts.FileEntry, error) {
	entries, err := s.service.Files(ctx, dir)
	if err != nil {
		return nil, err
	}
	return contractFileEntries(entries), nil
}

// ReadFile implements contracts.WorkspaceService.
func (s *Service) ReadFile(ctx context.Context, path string, limit int) ([]byte, error) {
	return s.service.ReadFile(ctx, path, limit)
}

// Search implements contracts.WorkspaceService: a case-insensitive file-name
// search below the current root, bounded by depth and result count.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]contracts.FileEntry, error) {
	entries, err := s.service.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	return contractFileEntries(entries), nil
}

// --- uncommitted changes (git) ---------------------------------------------

// errNotGitRepository is wrapped into the readable error Changes returns when
// the current workspace root is not inside a git working tree.
var errNotGitRepository = errors.New("workspace: not inside a git repository")

// gitResult is the captured output of one git invocation. A non-zero exit is
// not an error here; callers decide whether the status is meaningful.
type gitResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// runGit runs git in dir with the caller's context. The environment disables
// prompts and forces a stable locale so porcelain output parses identically on
// every machine.
func (s *Service) runGit(ctx context.Context, dir string, args ...string) (gitResult, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return gitResult{}, fmt.Errorf("%s: git executable not found: %w", ModuleID, err)
	}
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if ctxErr := contextErr(ctx); ctxErr != nil {
		return gitResult{}, ctxErr
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return gitResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitErr.ExitCode()}, nil
		}
		return gitResult{}, fmt.Errorf("%s: run git: %w", ModuleID, runErr)
	}
	return gitResult{stdout: stdout.String(), stderr: stderr.String()}, nil
}

// gitOutput runs git and returns trimmed stdout, converting a non-zero exit to
// a readable error.
func (s *Service) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	result, err := s.runGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if result.exitCode != 0 {
		detail := strings.TrimSpace(result.stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.stdout)
		}
		if detail == "" {
			return "", fmt.Errorf("%s: git %s failed with exit status %d", ModuleID, strings.Join(args, " "), result.exitCode)
		}
		return "", fmt.Errorf("%s: git %s failed: %s", ModuleID, strings.Join(args, " "), detail)
	}
	return strings.TrimSpace(result.stdout), nil
}

// Changes implements contracts.WorkspaceDiffer: the uncommitted changes of the
// current workspace with bounded per-file unified diffs. It is product policy
// (the SDK workspace package deliberately has no git dependency).
func (s *Service) Changes(ctx context.Context) ([]contracts.FileChange, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("%s: git executable not found: %w", ModuleID, err)
	}
	_, root, err := s.currentWorkspaceRoot()
	if err != nil {
		return nil, err
	}
	top, err := s.gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s: workspace %q is not a git repository: %w", ModuleID, root, errNotGitRepository)
	}
	inside, err := s.gitOutput(ctx, root, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return nil, fmt.Errorf("%s: workspace %q is not a git working tree: %w", ModuleID, root, errNotGitRepository)
	}
	top = filepath.Clean(strings.TrimSpace(top))
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = filepath.Clean(resolved)
	}
	rootRelative, err := filepath.Rel(top, root)
	if err != nil {
		return nil, fmt.Errorf("%s: workspace root is not inside the repository: %w", ModuleID, err)
	}
	rootRelativeSlash := filepath.ToSlash(rootRelative)
	if rootRelativeSlash == "." {
		rootRelativeSlash = ""
	}

	// Porcelain -z output must not be trimmed: the first status column may be
	// a space (" M path"), and leading/trailing whitespace is part of paths.
	statusResult, err := s.runGit(ctx, root, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	if statusResult.exitCode != 0 {
		detail := strings.TrimSpace(statusResult.stderr)
		if detail == "" {
			detail = strings.TrimSpace(statusResult.stdout)
		}
		return nil, fmt.Errorf("%s: git status failed: %s", ModuleID, detail)
	}
	raw := parseGitStatus(statusResult.stdout, rootRelativeSlash, s.maxChanges)
	changes := make([]contracts.FileChange, 0, len(raw))
	diffBudget := s.maxDiffBytes
	for _, item := range raw {
		change := contracts.FileChange{Path: item.path, Status: item.status}
		if item.status != "??" && diffBudget > 0 {
			diff, err := s.gitOutput(ctx, root, "diff", "HEAD", "--no-color", "--", filepath.ToSlash(item.path))
			if err != nil {
				return nil, fmt.Errorf("%s: diff %q: %w", ModuleID, item.path, err)
			}
			if isBinaryDiff(diff) {
				diff = ""
			}
			if len(diff) > diffBudget {
				diff = diff[:diffBudget]
			}
			change.Diff = diff
			diffBudget -= len(diff)
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// rawChange is one parsed porcelain status entry.
type rawChange struct {
	path   string
	status string
}

// parseGitStatus parses `git status --porcelain=v1 -z`. Paths are relative to
// the repository root in git's output, so rootRelative (the workspace root
// relative to the repository root, "" for the repository root itself) is
// stripped and changes outside the workspace are skipped.
func parseGitStatus(output, rootRelative string, maxChanges int) []rawChange {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	tokens := strings.Split(output, "\x00")
	changes := make([]rawChange, 0, len(tokens))
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if len(token) < 3 {
			continue
		}
		xy := token[:2]
		path := token[3:]
		if strings.ContainsAny(xy, "RC") && index+1 < len(tokens) {
			// A rename/copy entry carries the original path as the next NUL
			// separated token; the new path (already read) is the file path.
			index++
		}
		translated, ok := translateGitPath(path, rootRelative)
		if !ok {
			continue
		}
		changes = append(changes, rawChange{path: translated, status: statusFromXY(xy)})
		if len(changes) >= maxChanges {
			break
		}
	}
	return changes
}

// translateGitPath turns a repository-root-relative git path into a
// workspace-root-relative path, reporting false for paths outside the root.
func translateGitPath(path, rootRelative string) (string, bool) {
	path = strings.TrimPrefix(filepath.ToSlash(path), "./")
	if path == "" {
		return "", false
	}
	if rootRelative == "" {
		return path, true
	}
	prefix := rootRelative + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	translated := strings.TrimPrefix(path, prefix)
	if translated == "" {
		return "", false
	}
	return translated, true
}

// statusFromXY maps the two porcelain columns to the short status the UI
// shows: "??" for untracked, otherwise the first non-space column (so both
// staged and unstaged modifications read "M").
func statusFromXY(xy string) string {
	if xy == "??" {
		return "??"
	}
	for _, column := range xy {
		if column != ' ' && column != '?' && column != '!' {
			return string(column)
		}
	}
	return strings.TrimSpace(xy)
}

// isBinaryDiff reports whether a unified diff is git's binary placeholder
// rather than text hunks.
func isBinaryDiff(diff string) bool {
	trimmed := strings.TrimSpace(diff)
	if trimmed == "" {
		return false
	}
	for _, line := range strings.Split(trimmed, "\n") {
		if strings.HasPrefix(line, "Binary files ") || line == "GIT binary patch" {
			return true
		}
	}
	return false
}
