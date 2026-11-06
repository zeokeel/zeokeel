package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// sandbox confines path arguments to a resolved workspace root.
//
// All file-system tools go through resolve, which rejects absolute paths and
// ".." components, then resolves symlinks on the longest existing prefix of the
// requested path. A symlink that points outside the root is therefore rejected
// before any I/O happens, including for files (and parent directories) that do
// not exist yet.
type sandbox struct {
	// root is absolute and has had all symlinks resolved.
	root string
	// cwd returns the current directory relative to root, without a leading
	// "./". Nil means the root itself.
	cwd func() string
}

func newSandbox(root string) (*sandbox, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("tools: WorkspaceRoot is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("tools: resolve workspace root %q: %w", root, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("tools: workspace root %q: %w", root, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("tools: workspace root %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("tools: workspace root %q is not a directory", root)
	}
	return &sandbox{root: real}, nil
}

// resolve maps a workspace-relative path to an absolute path that is provably
// inside the root. The returned path has symlinks resolved.
func (s *sandbox) resolve(rel string) (string, error) {
	clean, err := cleanRelativePath(rel)
	if err != nil {
		return "", err
	}
	base := "."
	if s.cwd != nil {
		if cwd := s.cwd(); cwd != "" {
			base = cwd
		}
	}
	full := filepath.Join(s.root, filepath.FromSlash(base), clean)
	if err := s.ensureWithin(full); err != nil {
		return "", err
	}
	real, err := resolveExisting(full)
	if err != nil {
		return "", err
	}
	if err := s.ensureWithin(real); err != nil {
		return "", fmt.Errorf("tools: path %q escapes the workspace root through a symlink", rel)
	}
	return real, nil
}

// cleanRelativePath validates a relative path argument and returns it cleaned.
// It is deliberately strict: any ".." component is rejected even when the
// cleaned path would still land inside the root.
func cleanRelativePath(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("tools: path is required")
	}
	if strings.IndexByte(rel, 0) >= 0 {
		return "", fmt.Errorf("tools: path contains a NUL byte")
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("tools: absolute paths are not allowed: %q", rel)
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return "", fmt.Errorf("tools: path must not contain %q: %q", "..", rel)
		}
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tools: path escapes the workspace root: %q", rel)
	}
	return clean, nil
}

// ensureWithin reports whether path is lexically inside the root. Both root and
// path should be absolute.
func (s *sandbox) ensureWithin(path string) error {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return fmt.Errorf("tools: path %q is outside the workspace root", path)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("tools: path %q is outside the workspace root", path)
	}
	return nil
}

// contains reports whether path resolves inside the root.
func (s *sandbox) contains(path string) bool {
	return s.ensureWithin(path) == nil
}

// relative renders an absolute path relative to the root, using forward
// slashes so tool output is stable across platforms.
func (s *sandbox) relative(path string) string {
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == "" {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// resolveExisting resolves symlinks on the longest existing prefix of path and
// re-appends the components that do not exist yet. This lets write_file create
// new nested directories while still refusing to traverse a symlinked parent
// that leaves the workspace.
func resolveExisting(path string) (string, error) {
	p := path
	var missing []string
	for {
		_, err := os.Lstat(p)
		if err == nil {
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				return "", fmt.Errorf("tools: cannot resolve symlink %q: %w", p, err)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("tools: cannot inspect %q: %w", p, err)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", fmt.Errorf("tools: cannot resolve path %q", path)
		}
		missing = append(missing, filepath.Base(p))
		p = parent
	}
}

// decodeArgs decodes a tool's JSON arguments. Empty arguments are treated as an
// empty object so callers can report the missing required field clearly.
func decodeArgs(name string, raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("%s: arguments must be a JSON object", name)
	}
	if err := json.Unmarshal(trimmed, dst); err != nil {
		return fmt.Errorf("%s: invalid arguments: %w", name, err)
	}
	return nil
}

// truncateBytes caps s at limit bytes, keeping the string valid UTF-8 and
// appending note inside the limit. It returns s unchanged when it fits.
func truncateBytes(s string, limit int, note string) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return capWithNote(s, limit, note)
}

// capWithNote truncates s to limit bytes including a trailing note. It is for
// callers that know truncation already happened (for example a full output
// buffer), so the note is appended even when len(s) == limit.
func capWithNote(s string, limit int, note string) string {
	if limit <= 0 {
		return ""
	}
	marker := "\n\n[" + note + "]"
	if len(marker) >= limit {
		return utf8Prefix(s, limit)
	}
	return utf8Prefix(s, limit-len(marker)) + marker
}

// utf8Prefix returns the longest prefix of s no longer than limit bytes that
// does not split a UTF-8 sequence.
func utf8Prefix(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if limit >= len(s) {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
