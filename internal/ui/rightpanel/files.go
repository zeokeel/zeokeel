package rightpanel

import (
	"context"
	"fmt"
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

// DirectoryPicker is the Files pane's "open a folder" seam: the default opens
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
)

// filePreview is one cached file preview (the Files pane re-reads a file only
// when the selection or the workspace changes).
type filePreview struct {
	data      []byte
	truncated bool
	bitmap    *ui.Bitmap
	err       string
}

// buildFilesPane is the Files tab: the artifact history (when the host
// presenter exposes it) above the workspace header, breadcrumb navigation, the
// native directory listing and the read-only preview of the selected file.
func buildFilesPane(
	c *ui.Context,
	t *design.Tokens,
	workspace contracts.WorkspaceService,
	artifacts contracts.ArtifactLister,
	runtime contracts.UIRuntime,
	st *state,
) {
	ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
		if artifacts != nil {
			buildArtifactsSection(c, t, artifacts, runtime, st)
		}
		buildFilesWorkspaceSection(c, t, workspace, runtime, st)
	})
}

// buildArtifactsSection is the read-only artifact history: the newest ten
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
			design.Apply(ui.Text(c, uikit.T(st.locale, "产物", "Artifacts")), t.Font(design.TypeXXS)).
				FontWeight(600).TextColor(t.LabelSecondary).Grow(1)
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
			design.Apply(ui.Text(c, uikit.T(st.locale, "还没有产物", "No artifacts yet")),
				t.TextMuted(design.TypeXXS))
			return
		}
		for index, artifact := range history {
			artifact := artifact
			key := strings.TrimSpace(artifact.ID)
			if key == "" {
				key = fmt.Sprintf("artifact-%d", index)
			}
			c.Key("artifact/" + key)
			open := st.artifactsOpen[key]
			toggle := func() {
				st.artifactsOpen[key] = !st.artifactsOpen[key]
				runtime.Invalidate()
			}
			title := firstNonEmpty(artifact.Title, uikit.T(st.locale, "未命名产物", "Untitled artifact"))
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				info := ui.ButtonBase(c).Grow(1).Padding(design.Space1).Radius(design.RadiusSM).Label(title)
				info = rowTint(t, info, open)
				info.Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).FontWeight(600).
							TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
						design.Apply(ui.Text(c, uikit.FormatClock(st.locale, artifact.CreatedAt)),
							t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
					})
				})
				if info.Clicked() {
					toggle()
				}
				previewLabel := uikit.T(st.locale, "预览", "Preview")
				if open {
					previewLabel = uikit.T(st.locale, "收起", "Hide")
				}
				if ui.Button(c, previewLabel).Clicked() {
					toggle()
				}
				download := ui.Button(c, uikit.T(st.locale, "下载", "Download")).
					Tooltip(uikit.T(st.locale, "保存为 Markdown", "Save as Markdown"))
				if download.Clicked() {
					saveArtifact(st, runtime, artifact)
				}
			})
			if open {
				ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 8).Children(func() {
					markdown.View(c, artifact.Body, nil)
				})
			}
		}
	})
}

// buildFilesWorkspaceSection is the workspace half of the Files tab.
func buildFilesWorkspaceSection(
	c *ui.Context,
	t *design.Tokens,
	workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime,
	st *state,
) {
	if workspace == nil {
		buildEmptyPane(c, uikit.T(st.locale, "工作区未启用", "Workspace is not enabled"))
		return
	}
	current, ok, err := workspace.Current(context.Background())
	if err != nil {
		uikit.ErrorLine(c, uikit.T(st.locale, "读取工作区失败：", "Failed to read the workspace: ")+err.Error())
		return
	}
	if !ok {
		buildOpenWorkspace(c, t, workspace, runtime, st)
		return
	}

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		// The refresh lives in the pane header; this row names the workspace.
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			// A muted label, not a badge: the workspace is context, the name is the
			// information.
			design.Apply(ui.Text(c, uikit.T(st.locale, "工作区", "Workspace")), t.TextMuted(design.TypeXXXS)).NoWrap()
			design.Apply(ui.Text(c, firstNonEmpty(current.Name, filepath.Base(current.Root))), t.Font(design.TypeXS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
		})
		// The workspace root is context: show the compressed path, keep the full
		// path in the tooltip.
		design.Apply(ui.Text(c, compressPath(current.Root)), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Tooltip(current.Root)
		buildBreadcrumb(c, t, current, runtime, st)
		// Filter and hidden-entry controls sit together right above the list.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			c.Key("files/filter")
			filterLabel := uikit.T(st.locale, "筛选文件", "Filter files")
			ui.SearchField(c, &st.filesFilter).Label(filterLabel).
				Placeholder(filterLabel).Grow(1)
			showLabel := uikit.T(st.locale, "显示隐藏文件", "Show hidden files")
			design.Apply(ui.Text(c, uikit.T(st.locale, "显示隐藏", "Show hidden")), t.TextMuted(design.TypeXXXS)).NoWrap()
			c.Key("files/show-hidden")
			ui.Switch(c, &st.filesShowHidden).Label(showLabel)
		})
		if st.filesErr != "" {
			uikit.ErrorLine(c, st.filesErr)
		}

		// The listing is cached: read only when the directory, the workspace or
		// an explicit Refresh changed it (P1: no os.ReadDir in render).
		entries, filesErr := st.filesEntriesFor(workspace, current)
		visible := filterFileEntries(entries, st.filesFilter, st.filesShowHidden)
		switch {
		case filesErr != nil:
			uikit.ErrorLine(c, uikit.T(st.locale, "列目录失败：", "Failed to list the directory: ")+filesErr.Error())
		case len(visible) == 0 && strings.TrimSpace(st.filesFilter) != "":
			design.Apply(ui.Text(c, uikit.T(st.locale, "没有匹配的条目", "No matching entries")), t.TextMuted(design.TypeXS))
		case len(visible) == 0 && len(entries) > 0:
			// Only hidden entries here: say so instead of looking broken.
			design.Apply(ui.Text(c, uikit.T(st.locale,
				"目录里只有隐藏文件（可打开「显示隐藏」）", "Only hidden entries here (turn on Show hidden)")),
				t.TextMuted(design.TypeXS))
		case len(visible) == 0:
			design.Apply(ui.Text(c, uikit.T(st.locale, "目录为空", "The directory is empty")), t.TextMuted(design.TypeXS))
		default:
			buildFileList(c, t, runtime, st, visible)
		}

		if st.filesSelected != "" {
			buildFilePreview(c, t, workspace, runtime, st)
		} else {
			design.Apply(ui.Text(c, uikit.T(st.locale, "选择文件以预览", "Select a file to preview")), t.TextMuted(design.TypeXXXS))
		}
	})
}

// filesEntriesFor returns the cached listing of the Files pane's current
// directory, reloading it only when the workspace, the directory or an explicit
// Refresh invalidated it. Rendering never reads the filesystem itself (P1).
func (st *state) filesEntriesFor(workspace contracts.WorkspaceService, current contracts.Workspace) ([]contracts.FileEntry, error) {
	workspaceKey := current.ID + "\x00" + current.Root
	if st.filesLoaded && st.filesWorkspace == workspaceKey && st.filesLoadedDir == st.filesDir {
		return st.filesEntries, st.filesLoadErr
	}
	st.filesEntries, st.filesLoadErr = workspace.Files(context.Background(), st.filesDir)
	st.filesLoaded = true
	st.filesWorkspace = workspaceKey
	st.filesLoadedDir = st.filesDir
	return st.filesEntries, st.filesLoadErr
}

// buildFileList is the native directory listing: a ui.List with one keyboard
// row per entry (Up/Down choose, Enter opens a directory or previews a file),
// so the list owns hover, chosen and focus painting (docs/ui/list.md).
func buildFileList(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, visible []contracts.FileEntry) {
	st.filesList.Key = func(row int) any {
		return firstNonEmpty(visible[row].Path, visible[row].Name)
	}
	st.filesList.Label = func(row int) string {
		return firstNonEmpty(visible[row].Name, filepath.Base(visible[row].Path))
	}
	// The chosen row is the cached cursor, by path, so a reload keeps it.
	chosen := -1
	for index := range visible {
		if firstNonEmpty(visible[index].Path, visible[index].Name) == st.filesCursor {
			chosen = index
			break
		}
	}
	st.filesListSel = chosen
	st.filesList.Selected = &st.filesListSel
	list := ui.List(c, &st.filesList, len(visible)).
		Label(uikit.T(st.locale, "工作区文件", "Workspace files")).
		Rows(func(row ui.ListRow) {
			buildFileRow(row, t, visible[row.Index], runtime, st)
		})
	if list.Changed() {
		st.chooseFileRow(visible, runtime)
	}
	if list.Submitted() {
		st.activateFileRow(visible, runtime)
	}
}

// chooseFileRow follows the list's choice: a file is previewed at once, a
// directory is entered on the click that chose it (the old row behaviour, which
// the frame tests pin); Enter also activates through Submitted.
func (st *state) chooseFileRow(visible []contracts.FileEntry, runtime contracts.UIRuntime) {
	if st.filesListSel < 0 || st.filesListSel >= len(visible) {
		return
	}
	entry := visible[st.filesListSel]
	path := firstNonEmpty(entry.Path, entry.Name)
	st.filesCursor = path
	if entry.Dir {
		navigateFiles(st, path, runtime)
		return
	}
	st.filesSelected = path
	st.filesErr = ""
	runtime.Invalidate()
}

// activateFileRow runs the list's Submitted action (Enter or a double click):
// a directory is entered, a file's preview is shown.
func (st *state) activateFileRow(visible []contracts.FileEntry, runtime contracts.UIRuntime) {
	if st.filesListSel < 0 || st.filesListSel >= len(visible) {
		return
	}
	entry := visible[st.filesListSel]
	path := firstNonEmpty(entry.Path, entry.Name)
	if entry.Dir {
		navigateFiles(st, path, runtime)
		return
	}
	st.filesCursor = path
	st.filesSelected = path
	st.filesErr = ""
	runtime.Invalidate()
}

// buildOpenWorkspace is the Files pane without a current workspace: the shared
// empty-state block explains the state (no icon-in-a-colored-circle) and offers
// the native open-folder action.
func buildOpenWorkspace(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService, runtime contracts.UIRuntime, st *state) {
	uikit.EmptyStateBlock(c,
		uikit.T(st.locale, "还没有打开工作区", "No workspace is open"),
		uikit.T(st.locale, "打开一个文件夹作为工作区，浏览其中的文件。", "Open a folder as a workspace to browse its files."),
		func() {
			open := design.Apply(ui.PrimaryButton(c, uikit.T(st.locale, "打开文件夹", "Open folder")), t.PrimaryButton())
			if !open.Clicked() {
				return
			}
			dir, err := DirectoryPicker(uikit.T(st.locale, "选择工作区文件夹", "Choose workspace folder"))
			switch {
			case err != nil:
				st.filesErr = uikit.T(st.locale, "无法打开文件夹选择器：", "Could not open the folder picker: ") + err.Error()
			case strings.TrimSpace(dir) == "":
				// Canceled: nothing changes.
			default:
				if _, createErr := workspace.Create(context.Background(), filepath.Base(dir), dir); createErr != nil {
					st.filesErr = uikit.T(st.locale, "打开工作区失败：", "Failed to open the workspace: ") + createErr.Error()
				} else {
					st.filesDir = ""
					st.filesSelected = ""
					st.filesErr = ""
					st.filesCursor = ""
					st.filesPreview = make(map[string]*filePreview)
					st.filesLoaded = false
					st.changesLoaded = false
					st.changes = nil
					st.diffs = make(map[string]*diffView)
				}
			}
			runtime.Invalidate()
		})
	if st.filesErr != "" {
		uikit.ErrorLine(c, st.filesErr)
	}
}

// buildBreadcrumb renders the root/segment path as clickable native buttons.
func buildBreadcrumb(c *ui.Context, t *design.Tokens, current contracts.Workspace, runtime contracts.UIRuntime, st *state) {
	rootLabel := "/" + firstNonEmpty(current.Name, filepath.Base(current.Root))
	segments := splitRelPath(st.filesDir)
	fullPath := rootLabel
	if len(segments) > 0 {
		fullPath += "/" + strings.Join(segments, "/")
	}
	ui.Row(c).FillWidth().Gap(design.Space1).AlignItems(ui.Center).Children(func() {
		root := ui.Button(c, rootLabel).Tooltip(uikit.T(st.locale, "返回工作区根目录", "Back to the workspace root"))
		if root.Clicked() {
			navigateFiles(st, "", runtime)
		}
		// Deep paths collapse their middle: first segment, an ellipsis, then the
		// last two segments; every crumb keeps its full path in the tooltip.
		visible := segments
		skipped := false
		if len(segments) > 3 {
			visible = append([]string{segments[0], "…"}, segments[len(segments)-2:]...)
			skipped = true
		}
		base := 0
		for index, segment := range visible {
			design.Apply(ui.Text(c, "/"), t.TextMuted(design.TypeXXXS))
			if segment == "…" && skipped {
				design.Apply(ui.Text(c, "…"), t.TextMuted(design.TypeXXXS)).Tooltip(fullPath)
				base = len(segments) - 2
				continue
			}
			label := "/" + segment
			index := index
			crumbIndex := index
			if skipped {
				crumbIndex = base + index - 1
				if index == 0 {
					crumbIndex = 0
				}
			}
			crumb := ui.Button(c, label).
				Tooltip(uikit.T(st.locale, "返回 ", "Back to ") + "/" + strings.Join(segments[:crumbIndex+1], "/"))
			if crumb.Clicked() {
				navigateFiles(st, strings.Join(segments[:crumbIndex+1], "/"), runtime)
			}
		}
	})
}

// splitRelPath splits a relative directory path at the slash separators.
func splitRelPath(dir string) []string {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" {
		return nil
	}
	parts := strings.Split(dir, "/")
	kept := parts[:0]
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" && part != "." {
			kept = append(kept, part)
		}
	}
	return kept
}

// navigateFiles moves the listing to dir and clears the selection/preview; the
// listing cache is invalidated so the next frame reads the new directory once.
func navigateFiles(st *state, dir string, runtime contracts.UIRuntime) {
	st.filesDir = dir
	st.filesSelected = ""
	st.filesCursor = ""
	st.filesListSel = -1
	st.filesPreview = make(map[string]*filePreview)
	st.filesErr = ""
	st.filesLoaded = false
	runtime.Invalidate()
}

// compressPath shortens one path for display: short paths stay as they are,
// long ones keep their first and last segments with an ellipsis between. The
// caller keeps the full path in a tooltip.
func compressPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if len([]rune(trimmed)) <= 56 {
		return trimmed
	}
	parts := strings.Split(trimmed, "/")
	kept := parts[:0]
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	if len(kept) < 4 {
		return trimmed
	}
	prefix := "/"
	if !strings.HasPrefix(trimmed, "/") {
		prefix = ""
	}
	return prefix + kept[0] + "/…/" + strings.Join(kept[len(kept)-2:], "/")
}

// filterFileEntries applies the Files pane's controls: hidden (dot) entries are
// folded unless showHidden, and the name filter matches case-insensitively.
func filterFileEntries(entries []contracts.FileEntry, filter string, showHidden bool) []contracts.FileEntry {
	needle := strings.ToLower(strings.TrimSpace(filter))
	visible := make([]contracts.FileEntry, 0, len(entries))
	for _, entry := range entries {
		name := firstNonEmpty(entry.Name, filepath.Base(entry.Path))
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
			continue
		}
		visible = append(visible, entry)
	}
	return visible
}

// buildFileRow is one native directory row: folder/file/image glyph, the name,
// the size; the List around it owns the row states, choice and keyboard. The
// same open/copy actions are available from a right-click menu, as the official
// context-menu pattern asks (the innermost element with a menu gets the click).
func buildFileRow(row ui.ListRow, t *design.Tokens, entry contracts.FileEntry,
	runtime contracts.UIRuntime, st *state) {
	c := row.Context
	name := firstNonEmpty(entry.Name, filepath.Base(entry.Path))
	path := firstNonEmpty(entry.Path, entry.Name)
	openLabel := uikit.T(st.locale, "打开", "Open")
	if entry.Dir {
		openLabel = uikit.T(st.locale, "进入目录", "Open folder")
	}
	element := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space1, design.Space2)
	element.ContextMenu(func(m *ui.Menu) {
		if m.Item(openLabel).Chosen() {
			if entry.Dir {
				navigateFiles(st, path, runtime)
				return
			}
			st.filesCursor = path
			st.filesSelected = path
			st.filesErr = ""
			runtime.Invalidate()
		}
		if m.Item(uikit.T(st.locale, "复制路径", "Copy path")).Chosen() {
			c.Services().WriteClipboard(path)
		}
	})
	element.Children(func() {
		icon := icons.File()
		switch {
		case entry.Dir:
			icon = icons.Folder()
		case isImagePath(entry.Name):
			icon = icons.Image()
		}
		ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
		design.Apply(ui.Text(c, name), t.Font(design.TypeXXS)).
			TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
		if !entry.Dir && entry.Size > 0 {
			design.Apply(ui.Text(c, formatFileSize(entry.Size)), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).NoWrap()
		}
	})
}

// buildFilePreview renders the selected file: Markdown through the official
// view, other text as plain text, images as a native bitmap and everything else
// as a metadata card.
func buildFilePreview(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService, runtime contracts.UIRuntime, st *state) {
	path := st.filesSelected
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space2).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.File()).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, filepath.Base(path)), t.Font(design.TypeXXS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			close := ui.Button(c, uikit.T(st.locale, "关闭预览", "Close preview")).
				Tooltip(uikit.T(st.locale, "关闭预览", "Close preview"))
			if close.Clicked() {
				st.filesSelected = ""
				runtime.Invalidate()
			}
		})

		preview := st.filePreviewFor(workspace, path)
		if preview.err != "" {
			uikit.ErrorLine(c, uikit.T(st.locale, "读取文件失败：", "Failed to read the file: ")+preview.err)
			return
		}
		switch {
		case preview.bitmap != nil:
			ui.Image(c, preview.bitmap).MaxWidthPercent(100)
		case isMarkdownPath(path):
			markdown.View(c, string(preview.data), nil)
		case isTextPath(path):
			// A source file is read-only code: the official editor.View draws it
			// with syntax highlighting, line numbers and virtualization
			// (editor.md ReadOnly) instead of a plain mono Text block.
			editor.View(c, st.codeEditorFor(path, preview.data)).
				Height(design.Space6 * 12).FillWidth()
		default:
			uikit.KeyValueRows(c, []uikit.KeyValue{
				{Key: uikit.T(st.locale, "大小", "Size"), Value: formatFileSize(int64(len(preview.data)))},
				{Key: uikit.T(st.locale, "类型", "Type"), Value: strings.TrimPrefix(filepath.Ext(path), ".")},
			})
			design.Apply(ui.Text(c, uikit.T(st.locale, "暂不支持预览", "Preview is not supported")), t.TextMuted(design.TypeXXS))
		}
		if preview.truncated {
			design.Apply(ui.Text(c, uikit.T(st.locale, "仅显示前 256 KiB", "Showing the first 256 KiB only")), t.TextMuted(design.TypeXXXS))
		}
	})
}

// codeEditorFor returns the read-only editor for one file preview. The state is
// rebuilt only when the selected file changes, so scrolling/selection and the
// parsed highlighting survive frames.
func (st *state) codeEditorFor(path string, data []byte) *editor.State {
	if st.fileEditor != nil && st.fileEditorPath == path {
		return st.fileEditor
	}
	buffer := editor.NewBuffer(string(data))
	buffer.SetLanguage(editor.LanguageFor(path))
	st.fileEditor = &editor.State{
		Buffer:      buffer,
		ReadOnly:    true,
		LineNumbers: editor.LineNumbersOn,
		SoftWrap:    true,
		Label:       uikit.T(st.locale, "文件内容", "File content"),
	}
	st.fileEditorPath = path
	return st.fileEditor
}

// filePreviewFor loads (once) and caches the selected file's preview.
func (st *state) filePreviewFor(workspace contracts.WorkspaceService, path string) *filePreview {
	if st.filesPreview == nil {
		st.filesPreview = make(map[string]*filePreview)
	}
	if cached, ok := st.filesPreview[path]; ok {
		return cached
	}
	preview := &filePreview{}
	data, err := workspace.ReadFile(context.Background(), path, maxPreviewBytes+1)
	switch {
	case err != nil:
		preview.err = err.Error()
	case len(data) > maxPreviewBytes:
		preview.truncated = true
		preview.data = data[:maxPreviewBytes]
	default:
		preview.data = data
	}
	if len(preview.data) > 0 && isImagePath(path) {
		if bitmap, decodeErr := ui.DecodeBitmap(preview.data); decodeErr == nil {
			preview.bitmap = bitmap
		}
	}
	st.filesPreview[path] = preview
	return preview
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
