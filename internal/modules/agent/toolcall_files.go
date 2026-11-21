package agent

// Structured file lists for multi-file tool calls. apply_patch carries its file
// names inside a patch body that is far larger than the transcript's argument
// bound, so the list is extracted from the FULL arguments when the call is
// recorded and persisted with the durable tool_call event (and recovered from
// rig history on restore), exactly like toolCallPath does for single-file tools.

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// maxGeneratedCallFiles bounds the parsed list of one call (the SDK accepts up
// to 200 files per patch; anything beyond that is not a realistic patch and
// would only bloat the transcript).
const maxGeneratedCallFiles = 200

// generatedCallFiles resolves the files one call produced, from its full
// arguments. Only apply_patch carries several; every other call keeps Path.
func generatedCallFiles(name, arguments string) []contracts.ToolCallFile {
	if !strings.EqualFold(strings.TrimSpace(name), "apply_patch") {
		return nil
	}
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		return nil
	}
	var args struct {
		Patch  string `json:"patch"`
		DryRun bool   `json:"dry_run"`
	}
	if json.Unmarshal([]byte(raw), &args) != nil || args.DryRun {
		return nil
	}
	return patchFileList(args.Patch)
}

// patchFileList parses the file directives of an apply_patch body: the
// apply-style directives (*** Add/Update/Delete File, *** Move to — matched at
// column 0 like the SDK does, so a context line that merely mentions a
// directive is never mistaken for one) and unified-diff headers (--- a/x /
// +++ b/y, with /dev/null meaning added or deleted).
func patchFileList(patch string) []contracts.ToolCallFile {
	files := make([]contracts.ToolCallFile, 0, 4)
	index := make(map[string]int, 4)
	add := func(path string, added, deleted bool) {
		path = filepath.ToSlash(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
		if path == "" || path == "/dev/null" || strings.HasPrefix(path, "../") ||
			strings.HasPrefix(path, "/") {
			return
		}
		if existing, ok := index[path]; ok {
			files[existing].Added = files[existing].Added || added
			files[existing].Deleted = files[existing].Deleted || deleted
			return
		}
		if len(files) >= maxGeneratedCallFiles {
			return
		}
		index[path] = len(files)
		files = append(files, contracts.ToolCallFile{Path: path, Added: added, Deleted: deleted})
	}
	lines := strings.Split(patch, "\n")
	lastUpdated := ""
	for position := 0; position < len(lines); position++ {
		line := lines[position]
		switch {
		case strings.HasPrefix(line, "*** Add File:"):
			add(strings.TrimPrefix(line, "*** Add File:"), true, false)
		case strings.HasPrefix(line, "*** Update File:"):
			lastUpdated = strings.TrimSpace(strings.TrimPrefix(line, "*** Update File:"))
			add(lastUpdated, false, false)
		case strings.HasPrefix(line, "*** Delete File:"):
			add(strings.TrimPrefix(line, "*** Delete File:"), false, true)
		case strings.HasPrefix(line, "*** Move to:"):
			// A move renames the file named by the preceding Update directive:
			// the source no longer exists afterwards.
			add(strings.TrimPrefix(line, "*** Move to:"), false, false)
			if lastUpdated != "" {
				add(lastUpdated, false, true)
			}
			lastUpdated = ""
		case strings.HasPrefix(line, "rename from "):
			add(strings.TrimPrefix(line, "rename from "), false, true)
		case strings.HasPrefix(line, "rename to "):
			add(strings.TrimPrefix(line, "rename to "), false, false)
		case strings.HasPrefix(line, "copy to "):
			add(strings.TrimPrefix(line, "copy to "), true, false)
		case strings.HasPrefix(line, "--- ") && position+1 < len(lines) && strings.HasPrefix(lines[position+1], "+++ ") &&
			diffHeaderBoundary(lines, position):
			oldPath := stripDiffPrefix(strings.TrimPrefix(lines[position], "--- "))
			newPath := stripDiffPrefix(strings.TrimPrefix(lines[position+1], "+++ "))
			switch {
			case oldPath == "/dev/null":
				add(newPath, true, false)
			case newPath == "/dev/null":
				add(oldPath, false, true)
			default:
				add(newPath, false, false)
				if oldPath != newPath {
					add(oldPath, false, true)
				}
			}
			position++
		}
	}
	return files
}

// diffHeaderBoundary reports whether a "--- " line can be a file header rather
// than a hunk content line: it is the first line of the patch, follows a blank
// line (SDK's two-section example), or follows any line of git's diff metadata
// (index..., new file mode, similarity index, rename from ...) — a real
// `git diff` ALWAYS puts those between "diff --git" and "--- ".
func diffHeaderBoundary(lines []string, position int) bool {
	if position == 0 {
		return true
	}
	rawPrevious := lines[position-1]
	previous := strings.TrimSpace(rawPrevious)
	if previous == "" {
		return true
	}
	// Metadata lives at column 0 in git output; a context line (leading space)
	// whose text happens to start with "index " must not open a file section.
	if strings.HasPrefix(rawPrevious, " ") || strings.HasPrefix(rawPrevious, "\t") {
		return false
	}
	for _, prefix := range []string{
		"diff --git ", "index ", "new file mode ", "deleted file mode ",
		"old mode ", "new mode ", "similarity index ", "dissimilarity index ",
		"rename from ", "rename to ", "copy from ", "copy to ",
	} {
		if strings.HasPrefix(previous, prefix) {
			return true
		}
	}
	return false
}

// stripDiffPrefix removes the a/ b/ prefix of a unified-diff path and trims the
// tab-separated timestamp some producers add.
func stripDiffPrefix(path string) string {
	path = strings.TrimSpace(strings.SplitN(path, "\t", 2)[0])
	for _, prefix := range []string{"a/", "b/"} {
		if strings.HasPrefix(path, prefix) {
			return strings.TrimPrefix(path, prefix)
		}
	}
	return path
}
