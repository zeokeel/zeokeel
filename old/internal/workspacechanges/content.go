package workspacechanges

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Bounds for the turn's content capture and inline diff payload. They keep the
// per-turn cost and the persisted state.WorkspaceChanges event small enough to
// replay with every session reopen: a file tool edit is captured up to
// MaxContentFileBytes per side, at most MaxTurnFiles files are compared, the
// diff is limited to MaxFileDiffBytes per file and MaxTurnDiffBytes in total,
// and a change that would need more than MaxDiffLines lines (or longer than
// DiffTimeout to align) degrades to a whole-file comparison marked Coarse.
const (
	// MaxContentFileBytes is the largest file (per side) retained for a
	// comparison. Larger files are listed with Oversized and no diff.
	MaxContentFileBytes = 1 << 20 // 1 MiB
	// MaxTurnFiles bounds the files a turn lists with comparisons.
	MaxTurnFiles = 100
	// DiffContextLines is the context git-style hunks show around a change.
	DiffContextLines = 3
	// MaxDiffLines bounds one file's line diff; above it the comparison is
	// coarse (every line replaced).
	MaxDiffLines = 2000
	// MaxFileDiffBytes bounds one file's diff text.
	MaxFileDiffBytes = 64 << 10
	// MaxTurnDiffBytes bounds the diff text of one turn in the persisted
	// event; files that no longer fit get Truncated.
	MaxTurnDiffBytes = 256 << 10
	// DiffTimeout bounds the alignment work for one file. A comparison that
	// takes longer degrades to a coarse whole-file diff.
	DiffTimeout = 100 * time.Millisecond

	// maxLCSCells bounds the line-alignment table. Larger inputs go coarse
	// instead of allocating a quadratic table.
	maxLCSCells = 1 << 20
	// maxRetainedTextBytes bounds the walk backend's retained baseline, so a
	// turn start never copies MaxTurnFiles MiB-scale files into memory.
	maxRetainedTextBytes = 8 << 20
	// gitFileTimeout bounds one read-only git blob lookup.
	gitFileTimeout = 5 * time.Second
)

// fileContent is one captured side of a comparison. exists is false for a
// missing file (the added/deleted side) and for content that could not be
// retained.
type fileContent struct {
	text      string
	exists    bool
	binary    bool
	oversized bool
}

// Turn tracks the start-of-turn workspace baseline for one agent turn and
// produces the changed files with their inline diffs.
//
// StartTurn captures the workspace and retains the content a later comparison
// needs: in a Git workspace the paths already dirty or untracked (clean
// tracked paths are re-read from HEAD at Finish), in a walk workspace the small
// text files. NoteToolStart is fed the runtime's ToolStart events and records
// which paths a file tool touched. Finish captures again, compares, and
// applies the byte/line limits. A non-Git workspace lists only file-tool
// paths, so a shell-only change is not attributed to the turn; a Git workspace
// lists whatever the read-only git snapshot reports.
type Turn struct {
	root string
	// rootReal is root with symlinks resolved, so a symlinked workspace root
	// (common on macOS) is not mistaken for an escape.
	rootReal string
	// before is the start-of-turn snapshot used to detect changes.
	before Snapshot
	// beforeContent is the start-of-turn content retained for the paths a
	// turn can compare: in a Git workspace the paths already dirty or
	// untracked (clean tracked paths are read from HEAD at Finish), and in a
	// walk workspace the small text files, which is all the diff has to
	// compare against later because a later read would see the edit.
	beforeContent map[string]fileContent
	// edited is every path a file tool touched. Outside Git it is also the
	// report's allowlist: a shell-only change was not the turn's file work
	// and stays out.
	edited map[string]bool
}

// StartTurn captures the workspace baseline for one turn.
func StartTurn(ctx context.Context, root string) (*Turn, error) {
	before, err := Capture(ctx, root)
	if err != nil {
		return nil, err
	}
	t := &Turn{
		root:          before.Root,
		rootReal:      before.Root,
		before:        before,
		beforeContent: make(map[string]fileContent),
		edited:        make(map[string]bool),
	}
	if resolved, err := filepath.EvalSymlinks(before.Root); err == nil {
		t.rootReal = resolved
	}
	if before.Git {
		t.retainDirtyBaseline()
	} else {
		t.retainWalkBaseline()
	}
	return t, nil
}

// retainDirtyBaseline copies the content of paths that already had uncommitted
// changes when the turn started. Their start-of-turn content is neither HEAD
// nor recoverable from the filesystem later, and the file-level counts are
// bounded by MaxTurnFiles and MaxContentFileBytes.
func (t *Turn) retainDirtyBaseline() {
	paths := make([]string, 0, len(t.before.Files))
	for path, state := range t.before.Files {
		if strings.TrimSpace(state.GitStatus) == "" {
			continue
		}
		// A path already deleted has no content to keep.
		if strings.Contains(state.GitStatus, "D") {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if len(t.beforeContent) >= MaxTurnFiles {
			return
		}
		t.beforeContent[path] = t.readContent(path)
	}
}

// retainWalkBaseline keeps the start-of-turn text of the walk workspace's
// small files. A walk snapshot has no HEAD to reconstruct an untouched file
// from, so the only moment the pre-turn content is readable is the start of
// the turn; the small-file-first order spends the MaxTurnFiles budget on the
// source-sized files a turn is most likely to edit, and the byte budget keeps
// a workspace of large files from being copied per turn.
func (t *Turn) retainWalkBaseline() {
	type candidate struct {
		path string
		size int64
	}
	candidates := make([]candidate, 0, len(t.before.Files))
	for path, state := range t.before.Files {
		candidates = append(candidates, candidate{path: path, size: state.Size})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].size != candidates[j].size {
			return candidates[i].size < candidates[j].size
		}
		return candidates[i].path < candidates[j].path
	})
	retainedBytes := 0
	for _, c := range candidates {
		if len(t.beforeContent) >= MaxTurnFiles {
			return
		}
		if c.size > MaxContentFileBytes {
			t.beforeContent[c.path] = fileContent{exists: true, oversized: true}
			continue
		}
		if retainedBytes+int(c.size) > maxRetainedTextBytes {
			return
		}
		content := t.readContent(c.path)
		t.beforeContent[c.path] = content
		retainedBytes += len(content.text)
	}
}

// NoteToolStart attributes a file-tool edit to the turn's report. The event is
// forwarded asynchronously, so the file may already be written by the time the
// runtime hands the event over: it only classifies the path. The "before" side
// comes from the start-of-turn baseline, never from a read here.
func (t *Turn) NoteToolStart(name string, args json.RawMessage) {
	if t == nil {
		return
	}
	rel, ok := t.fileToolTarget(name, args)
	if !ok {
		return
	}
	t.edited[rel] = true
}

// fileToolTarget resolves the workspace-relative target of a mutating file
// tool, false for every other tool call. The path comes from the tool's own
// JSON arguments (write_file/edit_file/str_replace_editor all take "path");
// the editor's read-only "view" command is not an edit.
func (t *Turn) fileToolTarget(name string, args json.RawMessage) (string, bool) {
	switch name {
	case "write_file", "edit_file", "str_replace_editor":
	default:
		return "", false
	}
	var a struct {
		Command string `json:"command"`
		Path    string `json:"path"`
	}
	if len(args) == 0 || json.Unmarshal(args, &a) != nil {
		return "", false
	}
	if name == "str_replace_editor" && strings.EqualFold(strings.TrimSpace(a.Command), "view") {
		return "", false
	}
	return t.resolvePath(a.Path)
}

// resolvePath turns a tool path into a slash-separated path inside the
// workspace, refusing anything that escapes it. A relative path is resolved
// against the workspace root; a tool that changed its working directory before
// editing is not tracked (the diff then simply has no comparison for that
// path).
func (t *Turn) resolvePath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.root, filepath.FromSlash(path))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(t.root, abs)
	if err != nil || rel == "." {
		return "", false
	}
	if !withinRoot(t.root, abs) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// withinRoot reports whether path is root itself or lies below it.
func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// readContent reads one workspace-relative file within the comparison limits.
// A symlink that resolves outside the workspace is refused: the captured
// content is persisted in the session event, so it must stay workspace data.
func (t *Turn) readContent(rel string) fileContent {
	path := filepath.Join(t.root, filepath.FromSlash(rel))
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		if !withinRoot(t.rootReal, resolved) {
			return fileContent{}
		}
		path = resolved
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fileContent{}
	}
	f, err := os.Open(path)
	if err != nil {
		return fileContent{}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxContentFileBytes+1))
	if err != nil {
		return fileContent{}
	}
	if len(data) > MaxContentFileBytes {
		return fileContent{exists: true, oversized: true}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return fileContent{exists: true, binary: true}
	}
	return fileContent{exists: true, text: string(data)}
}

// Finish captures the workspace after the turn and returns the changed files
// with their comparisons, sorted by path.
func (t *Turn) Finish(ctx context.Context) []Change {
	if t == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	after, err := Capture(ctx, t.root)
	if err != nil {
		return nil
	}
	gitBackend := t.before.Git && after.Git
	raw := Diff(t.before, after)
	changes := make([]Change, 0, len(raw))
	for _, change := range raw {
		if len(changes) >= MaxTurnFiles {
			break
		}
		// Outside Git, shell-only changes are not attributed to the turn:
		// only paths a file tool declared are compared.
		if !gitBackend && !t.edited[change.Path] {
			continue
		}
		changes = append(changes, t.buildChange(ctx, change))
	}
	applyDiffBudget(changes)
	return changes
}

// buildChange compares one changed path, honouring the binary/oversized
// markers and the per-file diff limits.
func (t *Turn) buildChange(ctx context.Context, change Change) Change {
	out := Change{Path: change.Path, Status: change.Status}
	before := t.beforeContentOf(ctx, change.Path)
	after := t.afterContent(change.Path)
	switch {
	case before.binary || after.binary:
		out.Binary = true
		return out
	case before.oversized || after.oversized:
		out.Oversized = true
		return out
	}

	var beforeText, afterText string
	switch change.Status {
	case StatusAdded:
		if !after.exists {
			return out
		}
		afterText = after.text
	case StatusDeleted:
		if !before.exists {
			return out
		}
		beforeText = before.text
	default:
		if !before.exists || !after.exists {
			return out
		}
		beforeText = before.text
		afterText = after.text
	}

	diff, added, removed, coarse, truncated := unifiedDiff(beforeText, afterText)
	out.Diff = diff
	out.Added = added
	out.Removed = removed
	out.Coarse = coarse
	out.Truncated = truncated
	return out
}

// beforeContentOf resolves a path's pre-turn content, in priority order: the
// retained baseline, then the HEAD blob when the path was clean at turn start
// (read-only git).
func (t *Turn) beforeContentOf(ctx context.Context, path string) fileContent {
	if content, ok := t.beforeContent[path]; ok {
		return content
	}
	if t.before.Git && t.before.GitHead != "" {
		if state, ok := t.before.Files[path]; ok && strings.TrimSpace(state.GitStatus) == "" {
			return t.gitHeadContent(ctx, path)
		}
	}
	return fileContent{}
}

// afterContent reads the path's current content.
func (t *Turn) afterContent(path string) fileContent {
	return t.readContent(path)
}

// gitHeadContent reads the turn-start HEAD blob of one path with the read-only
// object plumbing (`git cat-file`). The repository index and objects are never
// written.
func (t *Turn) gitHeadContent(ctx context.Context, path string) fileContent {
	lookupCtx, cancel := context.WithTimeout(ctx, gitFileTimeout)
	defer cancel()
	rev := t.before.GitHead + ":" + path
	sizeOut, err := runGit(lookupCtx, t.root, "cat-file", "-s", rev)
	if err != nil {
		return fileContent{}
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if err != nil {
		return fileContent{}
	}
	if size > MaxContentFileBytes {
		return fileContent{exists: true, oversized: true}
	}
	blob, err := runGit(lookupCtx, t.root, "cat-file", "blob", rev)
	if err != nil {
		return fileContent{}
	}
	if len(blob) > MaxContentFileBytes {
		return fileContent{exists: true, oversized: true}
	}
	if strings.IndexByte(blob, 0) >= 0 {
		return fileContent{exists: true, binary: true}
	}
	return fileContent{exists: true, text: blob}
}

// applyDiffBudget caps the total persisted diff text. Files are visited in
// path order; one that no longer fits keeps its path/status/counts but loses
// its text and is marked truncated, and the first one that only partially fits
// is cut at a line boundary.
func applyDiffBudget(changes []Change) {
	remaining := MaxTurnDiffBytes
	for i := range changes {
		diff := changes[i].Diff
		if diff == "" {
			continue
		}
		if len(diff) <= remaining {
			remaining -= len(diff)
			continue
		}
		changes[i].Truncated = true
		if remaining <= 0 {
			changes[i].Diff = ""
			continue
		}
		cut := diff[:remaining]
		if idx := strings.LastIndexByte(cut, '\n'); idx > 0 {
			cut = cut[:idx]
		}
		changes[i].Diff = cut
		remaining = 0
	}
}

// diffOp is one line of a line-level edit script: ' ' context, '-' removed,
// '+' added.
type diffOp struct {
	kind byte
	line string
}

// unifiedDiff renders the line diff between before and after as hunk text with
// DiffContextLines of context. When the alignment is too large (quadratic
// table or MaxDiffLines) or runs past DiffTimeout, it degrades to a coarse
// whole-file replacement and reports coarse.
func unifiedDiff(before, after string) (text string, added, removed int, coarse, truncated bool) {
	oldLines := splitLines(before)
	newLines := splitLines(after)
	if len(oldLines) == 0 && len(newLines) == 0 {
		return "", 0, 0, false, false
	}

	start := time.Now()
	ops, ok := alignLines(oldLines, newLines)
	if ok {
		hunks, lines := formatHunks(ops)
		if lines <= MaxDiffLines && time.Since(start) <= DiffTimeout {
			added, removed = countOps(ops)
			if len(hunks) > MaxFileDiffBytes {
				hunks = truncateLines(hunks, MaxFileDiffBytes)
				truncated = true
			}
			return hunks, added, removed, false, truncated
		}
	}

	// Coarse fallback: every old line removed, every new line added.
	text, added, removed = coarseDiff(oldLines, newLines)
	if len(text) > MaxFileDiffBytes {
		text = truncateLines(text, MaxFileDiffBytes)
		truncated = true
	}
	return text, added, removed, true, truncated
}

// alignLines builds the line-level edit script. The identical prefix and
// suffix are trimmed first (the common case: a few edited lines in a large
// file), then the middle is aligned with an LCS table bounded by maxLCSCells.
// ok is false when the changed middle is too large to align.
func alignLines(oldLines, newLines []string) ([]diffOp, bool) {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	oldMid := oldLines[prefix : len(oldLines)-suffix]
	newMid := newLines[prefix : len(newLines)-suffix]

	ops := make([]diffOp, 0, len(oldLines)+len(newLines))
	for _, line := range oldLines[:prefix] {
		ops = append(ops, diffOp{kind: ' ', line: line})
	}
	midOps, ok := lcsOps(oldMid, newMid)
	if !ok {
		return nil, false
	}
	ops = append(ops, midOps...)
	for _, line := range oldLines[len(oldLines)-suffix:] {
		ops = append(ops, diffOp{kind: ' ', line: line})
	}
	return ops, true
}

// lcsOps aligns two line slices with a longest-common-subsequence table.
func lcsOps(a, b []string) ([]diffOp, bool) {
	n, m := len(a), len(b)
	if n == 0 {
		ops := make([]diffOp, 0, m)
		for _, line := range b {
			ops = append(ops, diffOp{kind: '+', line: line})
		}
		return ops, true
	}
	if m == 0 {
		ops := make([]diffOp, 0, n)
		for _, line := range a {
			ops = append(ops, diffOp{kind: '-', line: line})
		}
		return ops, true
	}
	if n*m > maxLCSCells {
		return nil, false
	}
	width := m + 1
	table := make([]int32, (n+1)*width)
	for i := n - 1; i >= 0; i-- {
		row := i * width
		next := (i + 1) * width
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				table[row+j] = table[next+j+1] + 1
			case table[next+j] >= table[row+j+1]:
				table[row+j] = table[next+j]
			default:
				table[row+j] = table[row+j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{kind: ' ', line: a[i]})
			i++
			j++
		case table[(i+1)*width+j] >= table[i*width+j+1]:
			ops = append(ops, diffOp{kind: '-', line: a[i]})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', line: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{kind: '-', line: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{kind: '+', line: b[j]})
	}
	return ops, true
}

// formatHunks groups the edit script into hunks with DiffContextLines of
// context. It returns the hunk text and the number of lines it contains
// (headers included), so the caller can enforce MaxDiffLines.
func formatHunks(ops []diffOp) (string, int) {
	if len(ops) == 0 {
		return "", 0
	}
	changed := make([]int, 0, len(ops))
	for i, op := range ops {
		if op.kind != ' ' {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return "", 0
	}

	type group struct{ start, end int } // inclusive op indices
	groups := make([]group, 0, 8)
	start := maxInt(0, changed[0]-DiffContextLines)
	end := minInt(len(ops)-1, changed[0]+DiffContextLines)
	for _, index := range changed[1:] {
		if index-DiffContextLines <= end+1 {
			end = minInt(len(ops)-1, index+DiffContextLines)
			continue
		}
		groups = append(groups, group{start: start, end: end})
		start = maxInt(0, index-DiffContextLines)
		end = minInt(len(ops)-1, index+DiffContextLines)
	}
	groups = append(groups, group{start: start, end: end})

	var b strings.Builder
	lines := 0
	for _, g := range groups {
		oldLine, newLine := 1, 1
		for _, op := range ops[:g.start] {
			if op.kind != '+' {
				oldLine++
			}
			if op.kind != '-' {
				newLine++
			}
		}
		oldCount, newCount := 0, 0
		for _, op := range ops[g.start : g.end+1] {
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
		}
		oldStart := oldLine
		if oldCount == 0 {
			oldStart = oldLine - 1
		}
		newStart := newLine
		if newCount == 0 {
			newStart = newLine - 1
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount)
		lines++
		for _, op := range ops[g.start : g.end+1] {
			b.WriteByte('\n')
			b.WriteByte(op.kind)
			b.WriteString(op.line)
			lines++
		}
	}
	return b.String(), lines
}

// coarseDiff replaces every line: the whole old file removed, the whole new
// file added.
func coarseDiff(oldLines, newLines []string) (string, int, int) {
	var b strings.Builder
	oldStart := 1
	if len(oldLines) == 0 {
		oldStart = 0
	}
	newStart := 1
	if len(newLines) == 0 {
		newStart = 0
	}
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", oldStart, len(oldLines), newStart, len(newLines))
	for _, line := range oldLines {
		b.WriteByte('\n')
		b.WriteByte('-')
		b.WriteString(line)
	}
	for _, line := range newLines {
		b.WriteByte('\n')
		b.WriteByte('+')
		b.WriteString(line)
	}
	return b.String(), len(newLines), len(oldLines)
}

func countOps(ops []diffOp) (added, removed int) {
	for _, op := range ops {
		switch op.kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}

// splitLines splits content into display lines: a trailing newline does not
// create an empty last line.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// truncateLines cuts text at the last line boundary within limit bytes.
func truncateLines(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	if idx := strings.LastIndexByte(cut, '\n'); idx > 0 {
		return cut[:idx]
	}
	return cut
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
