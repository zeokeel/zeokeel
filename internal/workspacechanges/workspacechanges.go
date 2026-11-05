// Package workspacechanges captures a workspace snapshot before and after an
// agent turn and reports the files the turn changed (FP-05, DSH's
// workspace/changes feature).
//
// Two backends share one diff contract:
//
//   - Inside a Git work tree the snapshot records each path's porcelain v1 XY
//     status plus a content hash for changed/untracked files, so the diff after
//     the turn can name added, modified and deleted paths.
//   - Outside Git (or when the git binary is unavailable) the snapshot walks
//     the workspace, hashing small regular files and falling back to
//     size+modtime for large ones. Ignored directories (.git, .zeokeel,
//     node_modules, .cache and similar build output) are skipped so a normal
//     desktop workspace stays cheap to scan.
//
// The package is deliberately side-effect free beyond reading the workspace:
// the app captures a snapshot before a turn, captures again after it, and emits
// state.WorkspaceChanges so the UI/telemetry can show what the turn touched.
package workspacechanges

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// maxHashedFileBytes bounds content hashing: larger files are fingerprinted
	// by size+modtime instead, which is enough to detect a change.
	maxHashedFileBytes = 2 << 20
	// maxWalkFiles bounds a non-Git snapshot so a huge workspace cannot hang
	// the turn. Files beyond the cap are omitted (the diff then under-reports,
	// never blocks).
	maxWalkFiles = 20000
	// gitTimeout bounds one git invocation.
	gitTimeout = 10 * time.Second
)

// Status names one file change.
const (
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusDeleted  = "deleted"
)

// FileState is the captured state of one workspace file.
type FileState struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"mod_time,omitempty"`
	Hash    string    `json:"hash,omitempty"`
	// GitStatus is the porcelain XY code for a changed Git path (empty for a
	// clean tracked file or the walk backend).
	GitStatus string `json:"git_status,omitempty"`
}

// Snapshot is the workspace state at one point in time.
type Snapshot struct {
	Root string
	// Git reports which backend produced the snapshot.
	Git bool
	// GitHead is the repository HEAD when the Git backend is used.
	GitHead string
	// Files is keyed by slash-separated workspace-relative path.
	Files map[string]FileState
}

// Change is one file that differs between two snapshots.
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// IsGit reports whether root (or an ancestor) is a Git work tree. It is cheap:
// it looks for a .git entry instead of spawning git.
func IsGit(root string) bool {
	dir := filepath.Clean(root)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// ignoredDir reports walk directories that never contribute changes.
func ignoredDir(name string) bool {
	switch name {
	case ".git", ".zeokeel", ".cache", "node_modules", ".hg", ".svn",
		"dist", "build", "target", ".venv", "__pycache__":
		return true
	default:
		return false
	}
}

// Capture snapshots root. ctx bounds the git call; a cancelled context returns
// an error so the caller can skip the diff.
func Capture(ctx context.Context, root string) (Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return Snapshot{}, errors.New("workspacechanges: root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("workspacechanges: resolve root: %w", err)
	}
	if info, err := os.Stat(abs); err != nil {
		return Snapshot{}, fmt.Errorf("workspacechanges: root %q: %w", abs, err)
	} else if !info.IsDir() {
		return Snapshot{}, fmt.Errorf("workspacechanges: root %q is not a directory", abs)
	}
	snap := Snapshot{Root: abs, Files: make(map[string]FileState)}
	if IsGit(abs) {
		return captureGit(ctx, abs, snap)
	}
	return captureWalk(ctx, abs, snap)
}

// captureGit uses `git status --porcelain=v1 -z` and hashes changed/untracked
// files, which is cheap on large repositories (Git already maintains the index).
func captureGit(ctx context.Context, root string, snap Snapshot) (Snapshot, error) {
	snap.Git = true
	gitCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	if head, err := runGit(gitCtx, root, "rev-parse", "HEAD"); err == nil {
		snap.GitHead = strings.TrimSpace(head)
	}
	// Baseline: every tracked path, so a file that was clean before the turn
	// can be recognised as modified/deleted afterwards instead of looking like
	// a brand-new file.
	if tracked, trackErr := runGit(gitCtx, root, "ls-files", "-z"); trackErr == nil {
		for _, path := range strings.Split(tracked, "\x00") {
			path = filepath.ToSlash(strings.TrimSpace(path))
			if path != "" {
				snap.Files[path] = FileState{Path: path}
			}
		}
	}
	out, err := runGit(gitCtx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return Snapshot{}, fmt.Errorf("workspacechanges: git status: %w", err)
	}
	// -z records are NUL-separated: "XY path" (rename/copy append the original
	// path as the next record).
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) < 4 {
			continue
		}
		code := record[:2]
		path := record[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++ // skip the original path record
		}
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		state := FileState{Path: path, GitStatus: code}
		abs := filepath.Join(root, filepath.FromSlash(path))
		if info, statErr := os.Stat(abs); statErr == nil && info.Mode().IsRegular() {
			state.Size = info.Size()
			state.ModTime = info.ModTime()
			if hash, hashErr := hashFile(abs, info.Size()); hashErr == nil {
				state.Hash = hash
			}
		}
		snap.Files[path] = state
	}
	return snap, nil
}

// captureWalk hashes the workspace tree without Git.
func captureWalk(ctx context.Context, root string, snap Snapshot) (Snapshot, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A transient unreadable entry must not fail the whole snapshot.
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && ignoredDir(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if count >= maxWalkFiles {
			return fs.SkipAll
		}
		count++
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		state := FileState{Path: rel, Size: info.Size(), ModTime: info.ModTime()}
		if hash, err := hashFile(path, info.Size()); err == nil {
			state.Hash = hash
		}
		snap.Files[rel] = state
		return nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("workspacechanges: walk %q: %w", root, err)
	}
	return snap, nil
}

// hashFile returns a SHA-256 hex digest for small regular files. Larger files
// return an empty hash (the caller falls back to size+modtime), which keeps a
// snapshot cheap on repositories with big binaries.
func hashFile(path string, size int64) (string, error) {
	if size > maxHashedFileBytes {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxHashedFileBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Diff returns the files that differ between before and after, sorted by path.
// A nil/empty before snapshot means every file in after is "added", and vice
// versa. Identical files are omitted.
func Diff(before, after Snapshot) []Change {
	changes := make([]Change, 0, 8)
	paths := make(map[string]struct{}, len(before.Files)+len(after.Files))
	for path := range before.Files {
		paths[path] = struct{}{}
	}
	for path := range after.Files {
		paths[path] = struct{}{}
	}
	gitBackend := before.Git && after.Git
	for path := range paths {
		beforeState, beforeOK := before.Files[path]
		afterState, afterOK := after.Files[path]
		switch {
		case !afterOK:
			changes = append(changes, Change{Path: path, Status: StatusDeleted})
		case !beforeOK:
			changes = append(changes, Change{Path: path, Status: StatusAdded})
		case gitBackend && strings.Contains(afterState.GitStatus, "D"):
			// Git keeps a deleted tracked path in the status list with a D code.
			changes = append(changes, Change{Path: path, Status: StatusDeleted})
		case gitBackend && strings.Contains(beforeState.GitStatus, "D"):
			// A path deleted before the turn and restored during it.
			changes = append(changes, Change{Path: path, Status: StatusAdded})
		case !sameFile(beforeState, afterState):
			changes = append(changes, Change{Path: path, Status: StatusModified})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

// sameFile reports whether two captures describe the same file contents.
func sameFile(a, b FileState) bool {
	switch {
	case a.Hash != "" && b.Hash != "":
		return a.Hash == b.Hash
	case a.Size != b.Size:
		return false
	case a.GitStatus != b.GitStatus:
		return false
	default:
		return a.ModTime.Equal(b.ModTime)
	}
}

// runGit runs one git command in root and returns stdout.
func runGit(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "-c", "core.quotepath=false"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, firstLine(message))
	}
	return stdout.String(), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ToStateChanges converts package changes into the session event payload
// (state.WorkspaceChanges JSON). It lives here so the app does not need to
// import the runtime event type for such a small conversion.
func ToStateChanges(turnID string, changes []Change) []byte {
	payload := struct {
		TurnID  string   `json:"turn_id,omitempty"`
		Changes []Change `json:"changes"`
	}{TurnID: turnID, Changes: changes}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"changes":[]}`)
	}
	return encoded
}
