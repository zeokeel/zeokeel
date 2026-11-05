package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Tool-result spill limits. A single tool result larger than this is written
// into the session workspace and replaced, for persistence and the next turn's
// history, with a head snippet plus the file path: a large recursive listing or
// file read therefore stops being re-sent to the model on every following turn.
// The current turn's model already received the full result from the runtime;
// only what is persisted changes.
const (
	// toolResultSpillBytes is the size above which a result is spilled.
	toolResultSpillBytes = 32 * 1024
	// toolResultSpillHead is how many leading bytes the summary keeps, so the
	// model still sees the beginning of the output.
	toolResultSpillHead = 2 * 1024
	// toolResultSpillKeep caps the retained files per workspace; the oldest are
	// removed when the cap is exceeded.
	toolResultSpillKeep = 100
	// toolResultSpillDir is the workspace-relative directory for spilled files.
	toolResultSpillDir = ".zeokeel/tool-results"
)

// spillToolResultFile writes result to root/.zeokeel/tool-results and returns
// the summary that replaces the persisted result. ok is false when nothing was
// spilled (small result or no workspace root); a write error leaves the caller
// on the original result.
//
// The workspace (and everything a repository can commit into it) is untrusted:
// a git checkout can ship symlinks such as .zeokeel/tool-results -> ~/.ssh.
// The target path is therefore built one component at a time with Lstat checks
// that refuse symlinks and non-directories, and the file itself is created with
// O_EXCL so an existing file or symlink is never truncated or followed (RT-07).
// Unsafe targets return an error and the caller keeps the full result.
func spillToolResultFile(root, callID, result string) (summary string, ok bool, err error) {
	if root == "" || len(result) <= toolResultSpillBytes {
		return "", false, nil
	}
	dir, err := safeSpillDir(root)
	if err != nil {
		return "", false, err
	}
	name := sanitizeToolResultName(callID) + ".txt"
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		// A pre-existing file (including a planted symlink) is never truncated:
		// fall back to keeping the full result in the turn.
		return "", false, err
	}
	if _, werr := file.WriteString(result); werr != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", false, werr
	}
	if cerr := file.Close(); cerr != nil {
		_ = os.Remove(path)
		return "", false, cerr
	}
	pruneSpilledToolResults(dir, toolResultSpillKeep)
	rel := toolResultSpillDir + "/" + name
	return spilledToolResultSummary(rel, result), true, nil
}

// safeSpillDir creates and returns root/.zeokeel/tool-results after verifying,
// component by component, that no part of the path is a symlink or a
// non-directory. Components that do not exist yet are created with 0700; an
// existing component is only accepted when Lstat (not Stat) reports a real
// directory, so a repository-supplied symlink cannot redirect the write outside
// the workspace.
func safeSpillDir(root string) (string, error) {
	// A workspace root may not exist yet (a fresh path); create it like the old
	// MkdirAll did, then verify the spill components below it.
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	dir := root
	for _, part := range strings.Split(toolResultSpillDir, "/") {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				return "", err
			}
			if merr := os.Mkdir(dir, 0o700); merr != nil && !os.IsExist(merr) {
				return "", merr
			}
			info, err = os.Lstat(dir)
			if err != nil {
				return "", err
			}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing tool-result directory %s: it is a symlink", dir)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("refusing tool-result directory %s: not a directory", dir)
		}
	}
	return dir, nil
}

// sanitizeToolResultName turns a call id into a safe file name: letters,
// digits, dot, dash and underscore survive, everything else becomes "_". The
// name is bounded so a hostile id cannot create a very long path.
func sanitizeToolResultName(callID string) string {
	var b strings.Builder
	for _, r := range callID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	name := strings.Trim(b.String(), ".")
	if name == "" {
		return "result"
	}
	return name
}

// spilledToolResultSummary is what persistence and the next turn's history see:
// a short notice with the workspace path and the head of the output.
func spilledToolResultSummary(rel, result string) string {
	head := truncateUTF8(result, toolResultSpillHead)
	lines := strings.Count(result, "\n")
	if !strings.HasSuffix(result, "\n") {
		lines++
	}
	return fmt.Sprintf(
		"[tool result too large: %d bytes, %d lines. Full output saved to the workspace file %s; only this summary is re-sent to the model. Open that file for the rest.]\n\n%s",
		len(result), lines, rel, head)
}

// truncateUTF8 returns at most n leading bytes of s without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// pruneSpilledToolResults keeps the newest keep files in dir, removing the
// oldest spilled results first. Best effort: cleanup failures never affect the
// turn.
func pruneSpilledToolResults(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keep {
		return
	}
	type oldFile struct {
		path    string
		modTime time.Time
	}
	files := make([]oldFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, oldFile{path: filepath.Join(dir, entry.Name()), modTime: info.ModTime()})
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files[:len(files)-keep] {
		_ = os.Remove(file.path)
	}
}
