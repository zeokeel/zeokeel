package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// str_replace_editor is a DSH-parity file editor. Unlike path.go's sandbox
// helpers it takes workspace-relative paths and keeps its own safe-join so the
// editor and read_image tools do not depend on changes to the shared path code.
// Commands: view, create, str_replace, insert, undo_edit.

const (
	// DefaultEditorMaxOutputBytes caps a single view result.
	DefaultEditorMaxOutputBytes = 64 << 10

	// editorHistoryLimit is the number of undo snapshots kept per path.
	editorHistoryLimit = 20
)

// EditorConfig configures the str_replace_editor tool.
type EditorConfig struct {
	// WorkspaceRoot confines every path argument. Required.
	WorkspaceRoot string

	// MaxOutputBytes caps view results. Non-positive values use
	// DefaultEditorMaxOutputBytes.
	MaxOutputBytes int
}

// NewStrReplaceEditorTool constructs the str_replace_editor tool. The
// constructor never fails: an invalid configuration is reported by Invoke.
func NewStrReplaceEditorTool(cfg EditorConfig) runtime.Tool {
	t := &editorTool{
		maxOutput: cfg.MaxOutputBytes,
		history:   make(map[string][]editorSnapshot),
	}
	if t.maxOutput <= 0 {
		t.maxOutput = DefaultEditorMaxOutputBytes
	}
	ws, err := newWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		t.initErr = fmt.Errorf("str_replace_editor: %w", err)
		return t
	}
	t.ws = ws
	return t
}

type editorTool struct {
	ws        *workspaceRoot
	maxOutput int
	initErr   error

	mu      sync.Mutex
	history map[string][]editorSnapshot
}

type editorSnapshot struct {
	content []byte
	existed bool
	mode    os.FileMode
}

func (t *editorTool) Name() string        { return "str_replace_editor" }
func (t *editorTool) Description() string { return editorDescription }

const editorDescription = `Custom editing tool for viewing, creating and editing files inside the workspace root.

Commands:
- view: show a file with line numbers, optionally limited by view_range [start, end] (1-based; end -1 means end of file). A directory is listed up to 2 levels deep, excluding hidden entries, node_modules and __pycache__.
- create: create a new file; fails if the path already exists.
- str_replace: replace an exact old_str that must occur exactly once. Omit new_str to delete the match.
- insert: insert new_str AFTER line insert_line (0 inserts at the beginning; the maximum is the number of lines).
- undo_edit: restore the file to its state before the most recent successful edit in this tool instance (per path). Undoing a create removes the file. History is kept in memory and capped.

All edit commands are exact and report why they failed. Paths are workspace-relative; absolute paths, ".." components and symlinks that leave the root are rejected. Files larger than 1 MiB and binary files are rejected.

Args JSON schema:
{"type":"object","properties":{"command":{"type":"string","description":"Command to run.","enum":["view","create","str_replace","insert","undo_edit"]},"path":{"type":"string","description":"Workspace-relative file or directory path."},"file_text":{"type":"string","description":"Content for create."},"insert_line":{"type":"integer","description":"Insert AFTER this 0-based line number (0 inserts first). Required for insert."},"new_str":{"type":"string","description":"Replacement text for str_replace (omit to delete) or text to insert."},"old_str":{"type":"string","description":"Exact text to replace; must occur exactly once."},"view_range":{"type":"array","items":{"type":"integer"},"description":"Optional [start, end] line range for view; use -1 as end for end of file."}},"required":["command","path"],"additionalProperties":false}`

type editorArgs struct {
	Command    string  `json:"command"`
	Path       string  `json:"path"`
	FileText   *string `json:"file_text"`
	InsertLine *int    `json:"insert_line"`
	NewStr     *string `json:"new_str"`
	OldStr     *string `json:"old_str"`
	ViewRange  []int   `json:"view_range"`
}

func (t *editorTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a editorArgs
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if t.initErr != nil {
		return "", t.initErr
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	switch a.Command {
	case "view":
		return t.view(a)
	case "create":
		return t.create(a)
	case "str_replace":
		return t.replace(a)
	case "insert":
		return t.insert(a)
	case "undo_edit":
		return t.undo(a)
	case "":
		return "", fmt.Errorf("str_replace_editor: command is required")
	default:
		return "", fmt.Errorf("str_replace_editor: unknown command %q; allowed commands are view, create, str_replace, insert, undo_edit", a.Command)
	}
}

func (t *editorTool) resolve(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", fmt.Errorf("str_replace_editor: path is required")
	}
	abs, err := t.ws.resolve(path)
	if err != nil {
		return "", "", fmt.Errorf("str_replace_editor: %w", err)
	}
	return abs, t.ws.display(abs), nil
}

func (t *editorTool) view(a editorArgs) (string, error) {
	abs, display, err := t.resolve(a.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("str_replace_editor: the path %s does not exist", display)
		}
		return "", fmt.Errorf("str_replace_editor: stat %s: %w", display, err)
	}
	if info.IsDir() {
		if a.ViewRange != nil {
			return "", fmt.Errorf("str_replace_editor: view_range is not allowed when path points to a directory")
		}
		return t.viewDirectory(abs, display)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("str_replace_editor: cannot view %s: not a regular file or directory", display)
	}
	if info.Size() > MaxReadFileBytes {
		return "", fmt.Errorf("str_replace_editor: %s is %d bytes, larger than the %d byte limit", display, info.Size(), MaxReadFileBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("str_replace_editor: read %s: %w", display, err)
	}
	if isBinary(data) {
		return "", fmt.Errorf("str_replace_editor: %s is not valid UTF-8 text (binary file)", display)
	}
	return t.formatFileView(display, string(data), a.ViewRange)
}

func (t *editorTool) formatFileView(display, content string, viewRange []int) (string, error) {
	allLines := strings.Split(content, "\n")
	lines := allLines
	initial := 1
	prompt := fmt.Sprintf("Here's the content of %s with line numbers (which has a total of %d lines)", display, len(allLines))
	if viewRange != nil {
		if len(viewRange) != 2 {
			return "", fmt.Errorf("str_replace_editor: invalid view_range; it must be a list of two integers")
		}
		first, last := viewRange[0], viewRange[1]
		if first < 1 || first > len(allLines) {
			return "", fmt.Errorf("str_replace_editor: invalid view_range [%d, %d]: first element should be within [1, %d]", first, last, len(allLines))
		}
		if last != -1 && last > len(allLines) {
			return "", fmt.Errorf("str_replace_editor: invalid view_range [%d, %d]: second element should be at most %d", first, last, len(allLines))
		}
		if last != -1 && last < first {
			return "", fmt.Errorf("str_replace_editor: invalid view_range [%d, %d]: second element should be larger than or equal to the first", first, last)
		}
		initial = first
		requestedFinal := last
		if last == -1 {
			lines = allLines[first-1:]
		} else {
			lines = allLines[first-1 : last]
		}
		prompt += fmt.Sprintf(" with view_range=[%d, %d]", initial, requestedFinal)
	}
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString(":\n")
	for i, line := range lines {
		fmt.Fprintf(&b, "%6d  %s\n", initial+i, line)
	}
	return truncateBytes(b.String(), t.maxOutput, "response clipped; use view_range to inspect a smaller section"), nil
}

func (t *editorTool) viewDirectory(abs, display string) (string, error) {
	rows := []string{"d\t" + display}
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" {
				continue
			}
			child := filepath.Join(dir, name)
			kind := "f"
			if entry.IsDir() {
				kind = "d"
			} else if entry.Type()&os.ModeSymlink != 0 {
				kind = "?"
			}
			rows = append(rows, kind+"\t"+t.ws.display(child))
			if entry.IsDir() && depth < 2 {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(abs, 1); err != nil {
		return "", fmt.Errorf("str_replace_editor: list %s: %w", display, err)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i][strings.IndexByte(rows[i], '\t')+1:] < rows[j][strings.IndexByte(rows[j], '\t')+1:]
	})
	listing := strings.Join(rows, "\n") + "\n"
	out := fmt.Sprintf("Here are the files and directories up to 2 levels deep in %s, excluding hidden items, node_modules, and Python cache directories:\n%s", display, listing)
	return truncateBytes(out, t.maxOutput, "response clipped; view a smaller directory"), nil
}

func (t *editorTool) create(a editorArgs) (string, error) {
	if a.FileText == nil {
		return "", fmt.Errorf("str_replace_editor: file_text is required for command: create")
	}
	abs, display, err := t.resolve(a.Path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err == nil {
		return "", fmt.Errorf("str_replace_editor: file already exists at %s; cannot overwrite files using command create", display)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("str_replace_editor: stat %s: %w", display, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("str_replace_editor: create parent directories for %s: %w", display, err)
	}
	if err := os.WriteFile(abs, []byte(*a.FileText), 0o644); err != nil {
		return "", fmt.Errorf("str_replace_editor: write %s: %w", display, err)
	}
	t.remember(abs, editorSnapshot{})
	return fmt.Sprintf("New file created successfully at: %s", display), nil
}

func (t *editorTool) replace(a editorArgs) (string, error) {
	if a.OldStr == nil || *a.OldStr == "" {
		return "", fmt.Errorf("str_replace_editor: old_str is required and must be non-empty for command: str_replace")
	}
	newValue := ""
	if a.NewStr != nil {
		newValue = *a.NewStr
	}
	abs, display, err := t.resolve(a.Path)
	if err != nil {
		return "", err
	}
	snap, info, err := t.readEditable(abs, display)
	if err != nil {
		return "", err
	}
	content := string(snap.content)
	switch n := strings.Count(content, *a.OldStr); {
	case n == 0:
		return "", fmt.Errorf("str_replace_editor: no replacement was performed; old_str did not appear verbatim in %s", display)
	case n > 1:
		return "", fmt.Errorf("str_replace_editor: no replacement was performed; old_str occurs %d times in %s and must be unique", n, display)
	}
	updated := strings.Replace(content, *a.OldStr, newValue, 1)
	if err := os.WriteFile(abs, []byte(updated), info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("str_replace_editor: write %s: %w", display, err)
	}
	t.remember(abs, snap)
	return fmt.Sprintf("The file %s has been edited successfully.", display), nil
}

func (t *editorTool) insert(a editorArgs) (string, error) {
	if a.InsertLine == nil {
		return "", fmt.Errorf("str_replace_editor: insert_line is required for command: insert")
	}
	if a.NewStr == nil {
		return "", fmt.Errorf("str_replace_editor: new_str is required for command: insert")
	}
	abs, display, err := t.resolve(a.Path)
	if err != nil {
		return "", err
	}
	snap, info, err := t.readEditable(abs, display)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(snap.content), "\n")
	line := *a.InsertLine
	if line < 0 || line > len(lines) {
		return "", fmt.Errorf("str_replace_editor: invalid insert_line %d; it should be within [0, %d]", line, len(lines))
	}
	inserted := strings.Split(*a.NewStr, "\n")
	combined := make([]string, 0, len(lines)+len(inserted))
	combined = append(combined, lines[:line]...)
	combined = append(combined, inserted...)
	combined = append(combined, lines[line:]...)
	if err := os.WriteFile(abs, []byte(strings.Join(combined, "\n")), info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("str_replace_editor: write %s: %w", display, err)
	}
	t.remember(abs, snap)
	return fmt.Sprintf("The file %s has been edited successfully.", display), nil
}

func (t *editorTool) readEditable(abs, display string) (editorSnapshot, os.FileInfo, error) {
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return editorSnapshot{}, nil, fmt.Errorf("str_replace_editor: the path %s does not exist", display)
		}
		return editorSnapshot{}, nil, fmt.Errorf("str_replace_editor: stat %s: %w", display, err)
	}
	if info.IsDir() {
		return editorSnapshot{}, nil, fmt.Errorf("str_replace_editor: %s is a directory and only view can be used on directories", display)
	}
	if !info.Mode().IsRegular() {
		return editorSnapshot{}, nil, fmt.Errorf("str_replace_editor: cannot edit %s: not a regular file", display)
	}
	if info.Size() > MaxReadFileBytes {
		return editorSnapshot{}, nil, fmt.Errorf("str_replace_editor: %s is %d bytes, larger than the %d byte limit", display, info.Size(), MaxReadFileBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return editorSnapshot{}, nil, fmt.Errorf("str_replace_editor: read %s: %w", display, err)
	}
	if isBinary(data) {
		return editorSnapshot{}, nil, fmt.Errorf("str_replace_editor: %s is not valid UTF-8 text (binary file)", display)
	}
	return editorSnapshot{content: data, existed: true, mode: info.Mode().Perm()}, info, nil
}

func (t *editorTool) remember(abs string, snap editorSnapshot) {
	h := append(t.history[abs], snap)
	if len(h) > editorHistoryLimit {
		h = h[len(h)-editorHistoryLimit:]
	}
	t.history[abs] = h
}

func (t *editorTool) undo(a editorArgs) (string, error) {
	abs, display, err := t.resolve(a.Path)
	if err != nil {
		return "", err
	}
	history := t.history[abs]
	if len(history) == 0 {
		return "", fmt.Errorf("str_replace_editor: no edits to undo for %s", display)
	}
	snap := history[len(history)-1]
	if !snap.existed {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("str_replace_editor: undo create %s: %w", display, err)
		}
		t.history[abs] = history[:len(history)-1]
		return fmt.Sprintf("Undid the last edit to %s (removed the file created by this tool).", display), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("str_replace_editor: undo %s: %w", display, err)
	}
	if err := os.WriteFile(abs, snap.content, snap.mode); err != nil {
		return "", fmt.Errorf("str_replace_editor: undo %s: %w", display, err)
	}
	t.history[abs] = history[:len(history)-1]
	return fmt.Sprintf("Undid the last edit to %s.", display), nil
}

// workspaceRoot confines the editor and image tools to one canonical directory.
// The checks mirror path.go's sandbox, but the safe-join lives here so these
// tools stay independent of that shared file (as required by their ownership
// boundary). ".." components are rejected even when the cleaned path would
// still land inside the root.
type workspaceRoot struct {
	root string
}

func newWorkspaceRoot(root string) (*workspaceRoot, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("WorkspaceRoot is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root %q: %w", root, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace root %q: %w", root, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("workspace root %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %q is not a directory", root)
	}
	return &workspaceRoot{root: real}, nil
}

func (w *workspaceRoot) resolve(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.IndexByte(rel, 0) >= 0 {
		return "", fmt.Errorf("path contains a NUL byte")
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("absolute paths are not allowed: %q", rel)
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return "", fmt.Errorf("path must not contain %q: %q", "..", rel)
		}
	}
	clean := filepath.Clean(rel)
	full := filepath.Join(w.root, clean)
	if err := withinRoot(w.root, full); err != nil {
		return "", fmt.Errorf("path escapes the workspace root: %q", rel)
	}
	real, err := resolveExistingPrefix(full)
	if err != nil {
		return "", err
	}
	if err := withinRoot(w.root, real); err != nil {
		return "", fmt.Errorf("path %q escapes the workspace root through a symlink", rel)
	}
	return real, nil
}

func (w *workspaceRoot) display(path string) string {
	rel, err := filepath.Rel(w.root, path)
	if err != nil || rel == "" {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func withinRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("path %q is outside the workspace root", path)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path %q is outside the workspace root", path)
	}
	return nil
}

// resolveExistingPrefix resolves symlinks on the longest existing prefix of
// path and re-appends components that do not exist yet. It is a local copy of
// path.go's helper so these tools do not depend on editing shared code.
func resolveExistingPrefix(path string) (string, error) {
	p := path
	var missing []string
	for {
		_, err := os.Lstat(p)
		if err == nil {
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				return "", fmt.Errorf("cannot resolve symlink %q: %w", p, err)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot inspect %q: %w", p, err)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", fmt.Errorf("cannot resolve path %q", path)
		}
		missing = append(missing, filepath.Base(p))
		p = parent
	}
}
