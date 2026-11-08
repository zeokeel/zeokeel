package uikit

import (
	"os"
	"path/filepath"
	"strings"
)

// SplitAttachmentPaths splits one manual attachment-path entry into individual
// paths: one per line, and/or separated by ASCII or full-width commas and
// semicolons. Surrounding whitespace and matching quotes are trimmed, and empty
// pieces are dropped, so a pasted list of paths is accepted as-is.
func SplitAttachmentPaths(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '\n', '\r', ',', ';', '，', '；':
			return true
		default:
			return false
		}
	})
	paths := make([]string, 0, len(fields))
	for _, field := range fields {
		path := strings.TrimSpace(field)
		path = strings.Trim(path, `"'`)
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// InvalidAttachmentPath is one manual path entry the fallback cannot stage,
// classified so the caller can localize the inline error.
type InvalidAttachmentPath struct {
	Path     string
	Relative bool // the entry is not an absolute path
	Missing  bool // the entry is absolute but not an existing regular file
}

// ResolveAttachmentPaths splits a manual attachment entry and validates every
// path: absolute paths to existing regular files are returned as valid (in
// input order, de-duplicated); the rest are returned as invalid entries with
// their reason. The picker fallback stages only what exists and reports the
// rest inline, so one typo does not discard the whole selection.
func ResolveAttachmentPaths(text string) (valid []string, invalid []InvalidAttachmentPath) {
	seen := make(map[string]struct{})
	for _, path := range SplitAttachmentPaths(text) {
		if !filepath.IsAbs(path) {
			invalid = append(invalid, InvalidAttachmentPath{Path: path, Relative: true})
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			invalid = append(invalid, InvalidAttachmentPath{Path: path, Missing: true})
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		valid = append(valid, path)
	}
	return valid, invalid
}
