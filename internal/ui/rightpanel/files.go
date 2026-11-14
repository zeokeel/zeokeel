package rightpanel

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// DirectoryPicker is the Files page's "open a folder" seam: the default opens
// the native mygo directory dialog with the localized title and returns the
// first chosen path; tests replace it with a scripted selection so no desktop
// backend is needed. The title is a parameter (not package state) so a language
// switch cannot race another window's dialog.
var DirectoryPicker = func(title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     title,
		Directory: true,
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

const (
	// maxPreviewBytes caps the text/markdown preview (256 KiB).
	maxPreviewBytes = 256 << 10
	// maxImageBytes bounds one image read: a screenshot or photo is usually far
	// over the text cap, so images get their own (much larger) read bound
	// instead of being truncated into an undecodable fragment.
	maxImageBytes = 16 << 20
	// maxImagePixels bounds the decoded dimensions before the decoder allocates
	// the bitmap, so a small "image bomb" file cannot exhaust memory on the
	// background read.
	maxImagePixels = 24_000_000
	// maxTreeEntries bounds one directory's rendered rows: a directory with
	// tens of thousands of entries would make the native tree build every row
	// each frame. The workspace service exposes search for exhaustive lookups,
	// so the honest cap is enough for the sidebar's browsing purpose.
	maxTreeEntries = 400
)

// filePreview is one cached file preview (a tab re-reads a file only when it
// is opened or explicitly reloaded). loading marks a read that runs off the UI
// thread: the tab shows a spinner until the posted result lands, so opening a
// large file never blocks a frame.
type filePreview struct {
	data      []byte
	truncated bool
	bitmap    *ui.Bitmap
	err       string
	loading   bool
	workspace string
}

// editorKey identifies one cached code-editor state: the workspace identity and
// the workspace-relative path. The workspace is part of the key so a same-named
// file in another workspace can never reuse (and paint) the old buffer.
type editorKey struct {
	workspace string
	path      string
}

// --- workspace tree cache ---------------------------------------------------

// syncFilesRoot returns the cached root listing of the current workspace and
// starts the read off the UI thread when the workspace or an explicit refresh
// invalidated it. It reports whether a read is in flight, so the page can show
// the loading row.
func (st *state) syncFilesRoot(workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime) ([]contracts.FileEntry, bool, error) {
	current, ok, err := workspace.Current(context.Background())
	if err != nil || !ok {
		st.resetFilesState("")
		return nil, false, err
	}
	key := current.ID + "\x00" + current.Root
	if st.filesWorkspace != key {
		st.resetFilesState(key)
	}
	listing := st.directoryListing("")
	if listing.loaded || listing.loading || runtime == nil {
		if runtime == nil && !listing.loaded && !listing.loading {
			listing.entries, listing.err = workspace.Files(context.Background(), "")
			listing.loaded = true
		}
		return listing.entries, listing.loading, listing.err
	}
	st.startDirectoryLoad(workspace, "", runtime)
	return nil, true, nil
}

// syncDir returns the cached listing of one directory, starting its read off
// the UI thread the first time the user opens it.
func (st *state) syncDir(path string, workspace contracts.WorkspaceService) *dirListing {
	listing := st.directoryListing(path)
	if listing.loaded || listing.loading {
		return listing
	}
	st.startDirectoryLoad(workspace, path, st.runtime)
	return listing
}

// directoryListing returns (creating on first use) the listing of dir; the
// cache is dropped with the workspace or an explicit refresh.
func (st *state) directoryListing(dir string) *dirListing {
	if st.filesDirs == nil {
		st.filesDirs = make(map[string]*dirListing)
	}
	listing, ok := st.filesDirs[dir]
	if !ok {
		listing = &dirListing{}
		st.filesDirs[dir] = listing
	}
	return listing
}

// startDirectoryLoad reads one directory off the UI thread and posts the result
// to the frame that invalidated it. A result for another workspace (or one an
// explicit refresh superseded) is dropped.
func (st *state) startDirectoryLoad(workspace contracts.WorkspaceService, dir string,
	runtime contracts.UIRuntime) {
	listing := st.directoryListing(dir)
	if listing.loading {
		return
	}
	generation := st.filesGeneration
	workspaceKey := st.filesWorkspace
	if runtime == nil {
		listing.entries, listing.err = workspace.Files(context.Background(), dir)
		listing.loaded = true
		return
	}
	listing.loading = true
	go func() {
		entries, err := workspace.Files(context.Background(), dir)
		runtime.Post(func() {
			if st.filesGeneration != generation || st.filesWorkspace != workspaceKey {
				return
			}
			listing.loading = false
			listing.loaded = true
			listing.entries = entries
			listing.err = err
			runtime.Invalidate()
		})
	}()
}

// resetFilesState drops the tree caches for one workspace key; a key change
// also bumps the generation so posted reads for the old workspace are dropped.
func (st *state) resetFilesState(workspaceKey string) {
	if st.filesWorkspace == workspaceKey && st.filesDirs != nil {
		return
	}
	st.filesWorkspace = workspaceKey
	st.filesDirs = make(map[string]*dirListing)
	st.filesOpen = make(map[string]*bool)
	st.filesGeneration++
}

// refreshFiles drops the tree and changes/diff caches, so the next frame
// re-reads the workspace from disk. Bumping the changes generation also
// invalidates a workspace-diff read that was in flight when the user pressed
// refresh: its posted result must not restore the pre-refresh snapshot.
func (st *state) refreshFiles() {
	st.filesGeneration++
	st.filesDirs = make(map[string]*dirListing)
	st.filesOpen = make(map[string]*bool)
	st.changesGeneration++
	st.changes = nil
	st.changesErr = nil
	st.changesLoaded = false
	st.changesLoading = false
	st.changesOpen = make(map[string]bool)
	st.diffs = make(map[string]*diffView)
	st.invalidate()
}

// syncWorkspaceIdentity keeps the path-keyed caches scoped to one workspace.
// When the current workspace identity changes, the preview/editor/changes/diff
// caches and the files tree belong to the old workspace and are dropped; the
// opened file tabs stay (the documented UX), so each tab re-reads its relative
// path in the new workspace on the next frame.
func (st *state) syncWorkspaceIdentity(workspace contracts.WorkspaceService) {
	identity, _ := st.currentWorkspaceKey(workspace)
	if identity == st.activeWorkspace {
		return
	}
	st.activeWorkspace = identity
	st.previews = make(map[string]*filePreview)
	st.editors = make(map[editorKey]*editor.State)
	st.changesOpen = make(map[string]bool)
	st.diffs = make(map[string]*diffView)
	st.changes = nil
	st.changesErr = nil
	st.changesLoaded = false
	st.changesLoading = false
	st.changesGeneration++
	st.resetFilesState(identity)
}

// dropPathCaches drops every cache entry that belongs to one workspace-relative
// path: its preview, its editor buffers (current workspace key), its diff fold
// state and its diff viewer. A file can appear under several diff keys (one per
// status), so the diff viewers are matched by the path prefix.
func (st *state) dropPathCaches(path string) {
	delete(st.previews, path)
	delete(st.editors, editorKey{workspace: st.activeWorkspace, path: path})
	delete(st.changesOpen, path)
	for key := range st.diffs {
		if strings.HasPrefix(key, path+"\x00") {
			delete(st.diffs, key)
		}
	}
}

// buildArtifactsSection is the read-only deliverables history: the newest ten
// presented artifacts with an expandable Markdown preview. It never registers a
// presenter handler: the interaction module owns display.
func buildArtifactsSection(c *ui.Context, t *design.Tokens, artifacts contracts.ArtifactLister,
	runtime contracts.UIRuntime, st *state) {
	history := append([]contracts.Artifact(nil), artifacts.Artifacts()...)
	sort.SliceStable(history, func(i, j int) bool {
		return history[i].CreatedAt.After(history[j].CreatedAt)
	})
	const maxArtifactRows = 10
	if len(history) > maxArtifactRows {
		history = history[:maxArtifactRows]
	}

	card := ui.Column(c).FillWidth().Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.Data()).Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, st.tr("交付区", "Deliverables")), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary).Grow(1).Bold()
		})
		if st.artifactsErr != "" {
			uikit.ErrorLine(c, st.artifactsErr)
		}
		if st.artifactsNotice != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Badge(c, st.artifactsNotice), t.Badge()).TextColor(t.SuccessText)
				if st.artifactsSavedPath != "" {
					design.Apply(ui.Text(c, st.artifactsSavedPath), t.Font(design.TypeXXXS)).
						TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…").
						Tooltip(st.artifactsSavedPath)
				}
			})
		}
		if len(history) == 0 {
			design.Apply(ui.Text(c, st.tr("还没有交付物", "No deliverables yet")),
				t.TextMuted(design.TypeXXS))
			return
		}
		// The history is the official List: rows are keyed by artifact id and
		// named by their title (list/listitem semantics for AT, keyboard choice,
		// stable rows as new artifacts arrive).
		artifactKeys := make([]string, len(history))
		artifactTitles := make([]string, len(history))
		for index, artifact := range history {
			artifactKeys[index] = artifactKey(artifact, index)
			artifactTitles[index] = artifactTitle(st, artifact)
		}
		st.artifactsList.Key = func(row int) any { return artifactKeys[row] }
		st.artifactsList.Label = func(row int) string { return artifactTitles[row] }
		st.artifactsList.Selected = &st.artifactsSel
		list := ui.List(c, &st.artifactsList, len(history)).
			Label(st.tr("交付物列表", "Deliverables")).
			Gap(design.Space1).
			Rows(func(row ui.ListRow) {
				artifact := history[row.Index]
				key := artifactKeys[row.Index]
				open := st.artifactsOpen[key]
				toggle := func() {
					st.artifactsOpen[key] = !st.artifactsOpen[key]
					runtime.Invalidate()
				}
				buildArtifactRow(row, t, st, runtime, artifact, artifactTitles[row.Index], open, toggle)
			})
		if list.Submitted() && st.artifactsSel >= 0 && st.artifactsSel < len(artifactKeys) {
			key := artifactKeys[st.artifactsSel]
			st.artifactsOpen[key] = !st.artifactsOpen[key]
			runtime.Invalidate()
		}
	})
}

// artifactKey is one artifact's stable row identity (its id, or its position
// for an id-less legacy entry).
func artifactKey(artifact contracts.Artifact, index int) string {
	if key := strings.TrimSpace(artifact.ID); key != "" {
		return key
	}
	return fmt.Sprintf("artifact-%d", index)
}

// artifactTitle is the row's display title and accessible name.
func artifactTitle(st *state, artifact contracts.Artifact) string {
	return firstNonEmpty(artifact.Title, st.tr("未命名产物", "Untitled artifact"))
}

// buildArtifactRow is one artifact history row: title, time, the explicit
// preview toggle, the download action and, while open, the Markdown preview
// below them. The List around it owns the row state, choice and keyboard.
func buildArtifactRow(row ui.ListRow, t *design.Tokens, st *state,
	runtime contracts.UIRuntime, artifact contracts.Artifact, title string, open bool, toggle func()) {
	c := row.Context
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…").Bold()
			design.Apply(ui.Text(c, uikit.FormatClock(st.locale, artifact.CreatedAt)),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
			previewLabel := st.tr("预览", "Preview")
			if open {
				previewLabel = st.tr("收起", "Hide")
			}
			if ui.Button(c, previewLabel).Clicked() {
				toggle()
			}
			download := ui.Button(c, st.tr("下载", "Download")).
				Tooltip(st.tr("保存为 Markdown", "Save as Markdown"))
			if download.Clicked() {
				saveArtifact(st, runtime, artifact)
			}
		})
		if open {
			ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 8).Children(func() {
				markdown.View(c, artifact.Body, docMarkdown(t))
			})
		}
	})
}

// buildOpenWorkspace is the Files page without a current workspace: the shared
// empty-state block explains the state and offers the native open-folder
// action.
func buildOpenWorkspace(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime, st *state) {
	uikit.EmptyStateBlock(c,
		st.tr("还没有打开工作区", "No workspace is open"),
		st.tr("打开一个文件夹作为工作区，浏览其中的文件。",
			"Open a folder as a workspace to browse its files."),
		func() {
			open := design.Apply(ui.PrimaryButton(c, st.tr("打开文件夹", "Open folder")), t.PrimaryButton())
			if !open.Clicked() {
				return
			}
			dir, err := DirectoryPicker(st.tr("选择工作区文件夹", "Choose workspace folder"))
			switch {
			case err != nil:
				st.filesRootErr = st.tr("无法打开文件夹选择器：", "Could not open the folder picker: ") + err.Error()
			case strings.TrimSpace(dir) == "":
				// Canceled: nothing changes.
			default:
				if _, createErr := workspace.Create(context.Background(), filepath.Base(dir), dir); createErr != nil {
					st.filesRootErr = st.tr("打开工作区失败：", "Failed to open the workspace: ") + createErr.Error()
				} else {
					st.filesRootErr = ""
					st.resetFilesState("")
					st.invalidate()
				}
			}
			runtime.Invalidate()
		})
	if st.filesRootErr != "" {
		uikit.ErrorLine(c, st.filesRootErr)
	}
}

// --- file preview cache -----------------------------------------------------

// filePreviewFor loads (once) and caches one file's preview. The read and
// decode run off the UI thread when the module has a runtime: the returned
// placeholder is loading until the posted result fills it, so opening a large
// file never blocks a frame. A bare state (unit tests) reads synchronously.
func (st *state) filePreviewFor(workspace contracts.WorkspaceService, path string) *filePreview {
	if st.previews == nil {
		st.previews = make(map[string]*filePreview)
	}
	workspaceKey, _ := st.currentWorkspaceKey(workspace)
	if cached, ok := st.previews[path]; ok && cached.workspace == workspaceKey {
		return cached
	}
	preview := &filePreview{workspace: workspaceKey}
	st.previews[path] = preview
	if st.runtime == nil || workspace == nil {
		applyFilePreview(preview, loadFilePreview(workspace, path))
		return preview
	}
	preview.loading = true
	go func() {
		result := loadFilePreview(workspace, path)
		st.runtime.Post(func() {
			// A reload, a workspace switch or a closed tab replaced the map
			// entry while the read ran: drop the stale result.
			if st.previews[path] != preview {
				return
			}
			applyFilePreview(preview, result)
			st.runtime.Invalidate()
		})
	}()
	return preview
}

// currentWorkspaceKey is the cache key of the workspace the page shows now; a
// posted async result uses it to prove it still applies.
func (st *state) currentWorkspaceKey(workspace contracts.WorkspaceService) (string, bool) {
	if workspace == nil {
		return "", false
	}
	current, ok, err := workspace.Current(context.Background())
	if err != nil || !ok {
		return "", false
	}
	return current.ID + "\x00" + current.Root, true
}

// filePreviewResult is one file preview read, carried from the read goroutine
// to the UI thread before it is applied to the cached preview.
type filePreviewResult struct {
	data      []byte
	truncated bool
	bitmap    *ui.Bitmap
	err       string
}

// loadFilePreview reads one file and decodes an image. Images use their own
// read bound and a pre-decode dimension bound; a corrupt or oversized image is
// reported honestly instead of silently falling through to the unsupported
// fallback. It does file and image I/O only: no state is touched, so it is safe
// off the UI thread.
func loadFilePreview(workspace contracts.WorkspaceService, path string) filePreviewResult {
	var result filePreviewResult
	if workspace == nil {
		result.err = "workspace is not enabled"
		return result
	}
	if isImagePath(path) {
		return loadImagePreview(workspace, path)
	}
	data, err := workspace.ReadFile(context.Background(), path, maxPreviewBytes+1)
	switch {
	case err != nil:
		result.err = err.Error()
		return result
	case len(data) > maxPreviewBytes:
		result.truncated = true
		result.data = data[:maxPreviewBytes]
	default:
		result.data = data
	}
	return result
}

// loadImagePreview reads and decodes one image within the image bounds: the
// byte bound keeps a truncated file from reaching the decoder at all, and the
// dimension bound is checked from the header (image.DecodeConfig) before the
// decoder allocates the bitmap.
func loadImagePreview(workspace contracts.WorkspaceService, path string) filePreviewResult {
	var result filePreviewResult
	data, err := workspace.ReadFile(context.Background(), path, maxImageBytes+1)
	if err != nil {
		result.err = err.Error()
		return result
	}
	if len(data) > maxImageBytes {
		result.err = fmt.Sprintf("image is too large: over %d MiB", maxImageBytes>>20)
		return result
	}
	config, _, configErr := image.DecodeConfig(bytes.NewReader(data))
	if configErr != nil {
		result.err = "image could not be decoded: " + configErr.Error()
		return result
	}
	if int64(config.Width)*int64(config.Height) > maxImagePixels {
		result.err = fmt.Sprintf("image is too large: %dx%d pixels (max %d)", config.Width, config.Height, maxImagePixels)
		return result
	}
	bitmap, decodeErr := ui.DecodeBitmap(data)
	if decodeErr != nil {
		result.err = "image could not be decoded: " + decodeErr.Error()
		return result
	}
	result.bitmap = bitmap
	return result
}

// applyFilePreview copies one read result into the cached preview.
func applyFilePreview(preview *filePreview, result filePreviewResult) {
	preview.data = result.data
	preview.truncated = result.truncated
	preview.bitmap = result.bitmap
	preview.err = result.err
	preview.loading = false
}

// editorFor returns the read-only official code editor for one file tab. The
// state is built once per (workspace, path), so scroll position and selection
// survive tab switches while a same-named file in another workspace gets a
// fresh buffer; closing the tab drops it.
func (st *state) editorFor(path string, data []byte) *editor.State {
	if st.editors == nil {
		st.editors = make(map[editorKey]*editor.State)
	}
	key := editorKey{workspace: st.activeWorkspace, path: path}
	if state, ok := st.editors[key]; ok {
		return state
	}
	buffer := editor.NewBuffer(string(data))
	// LanguageForText, not LanguageFor: the already-read content lets a file
	// without a telling extension pick its language from a shebang line (a
	// #!/bin/bash script), as the official editor does; unknown files still
	// fall back to plain text.
	buffer.SetLanguage(editor.LanguageForText(path, string(data)))
	state := &editor.State{
		Buffer:      buffer,
		ReadOnly:    true,
		LineNumbers: editor.LineNumbersOn,
		SoftWrap:    true,
		Label:       st.tr("文件内容", "File content"),
	}
	st.editors[key] = state
	return state
}

// Path classification helpers (the preview switches on them).

var (
	imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp"}
	textExts  = []string{
		".txt", ".md", ".json", ".yaml", ".yml", ".go", ".log", ".csv",
		".toml", ".ini", ".cfg", ".sh", ".bash", ".py", ".js", ".ts", ".css", ".html",
	}
)

func hasExt(path string, exts []string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, candidate := range exts {
		if ext == candidate {
			return true
		}
	}
	return false
}

func isImagePath(path string) bool    { return hasExt(path, imageExts) }
func isMarkdownPath(path string) bool { return strings.EqualFold(filepath.Ext(path), ".md") }
func isTextPath(path string) bool     { return hasExt(path, textExts) }

// formatFileSize renders a file size in B/KB/MB.
func formatFileSize(size int64) string {
	switch {
	case size < 1<<10:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	}
}
