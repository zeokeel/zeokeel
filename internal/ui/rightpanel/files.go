package rightpanel

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// DirectoryDialogTitle is the localized title of the default folder dialog; the
// Files pane sets it from the current locale right before invoking
// DirectoryPicker (the seam signature stays stable for tests).
var DirectoryDialogTitle = "选择工作区文件夹"

// DirectoryPicker is the Files pane's "open a folder" seam: the default opens
// the native mygo directory dialog and returns the first chosen path; tests
// replace it with a scripted selection so no desktop backend is needed.
var DirectoryPicker = func() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     DirectoryDialogTitle,
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
	// fileRowHeight is the native directory-row metric.
	fileRowHeight float32 = 28
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
		buildBreadcrumb(c, t, workspace, runtime, st)
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

		dir := st.filesDir
		entries, filesErr := workspace.Files(context.Background(), dir)
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
			for _, entry := range visible {
				buildFileRow(c, t, runtime, st, entry)
			}
		}

		if st.filesSelected != "" {
			buildFilePreview(c, t, workspace, runtime, st)
		} else {
			design.Apply(ui.Text(c, uikit.T(st.locale, "选择文件以预览", "Select a file to preview")), t.TextMuted(design.TypeXXXS))
		}
	})
}

// buildOpenWorkspace is the Files pane without a current workspace: explain the
// state and offer the native open-folder action.
func buildOpenWorkspace(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService, runtime contracts.UIRuntime, st *state) {
	th := t.Theme()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingY(design.Space6).Children(func() {
		ui.Row(c).Size(th.Space(9), th.Space(9)).Center().
			Radius(design.RadiusMax).Background(t.BgHover).Children(func() {
			ui.Icon(c, icons.Folder()).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
		})
		design.Apply(ui.Text(c, uikit.T(st.locale, "还没有打开工作区", "No workspace is open")), t.Font(design.TypeXS)).
			FontWeight(600).TextColor(t.LabelPrimary)
		design.Apply(ui.Text(c, uikit.T(st.locale, "打开一个文件夹作为工作区，浏览其中的文件。", "Open a folder as a workspace to browse its files.")), t.TextMuted(design.TypeXXS)).
			MaxLines(2)
		open := design.Apply(ui.PrimaryButton(c, uikit.T(st.locale, "打开文件夹", "Open folder")), t.PrimaryButton())
		if open.Clicked() {
			DirectoryDialogTitle = uikit.T(st.locale, "选择工作区文件夹", "Choose workspace folder")
			dir, err := DirectoryPicker()
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
					st.filesPreview = make(map[string]*filePreview)
					st.changesLoaded = false
					st.changes = nil
				}
			}
			runtime.Invalidate()
		}
		if st.filesErr != "" {
			uikit.ErrorLine(c, st.filesErr)
		}
	})
}

// buildBreadcrumb renders the root/segment path as clickable native buttons.
func buildBreadcrumb(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService, runtime contracts.UIRuntime, st *state) {
	current, ok, err := workspace.Current(context.Background())
	if err != nil || !ok {
		return
	}
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

// navigateFiles moves the listing to dir and clears the selection/preview.
func navigateFiles(st *state, dir string, runtime contracts.UIRuntime) {
	st.filesDir = dir
	st.filesSelected = ""
	st.filesPreview = make(map[string]*filePreview)
	st.filesErr = ""
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

// buildFileRow is one native directory row: folder or file glyph, the name and
// the click action (enter a directory, select a file for the preview).
func buildFileRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, entry contracts.FileEntry) {
	name := firstNonEmpty(entry.Name, filepath.Base(entry.Path))
	c.Key("file/" + firstNonEmpty(entry.Path, entry.Name))
	row := ui.ButtonBase(c).FillWidth().Height(fileRowHeight).PaddingX(design.Space2).
		Radius(design.RadiusSM).Label(name)
	row = rowTint(t, row, false)
	row.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			icon := icons.Folder()
			if !entry.Dir && isImagePath(entry.Name) {
				icon = icons.Globe()
			}
			ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, name), t.Font(design.TypeXXS)).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			if !entry.Dir && entry.Size > 0 {
				design.Apply(ui.Text(c, formatFileSize(entry.Size)), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
		})
	})
	if row.Clicked() {
		if entry.Dir {
			navigateFiles(st, firstNonEmpty(entry.Path, entry.Name), runtime)
		} else {
			st.filesSelected = firstNonEmpty(entry.Path, entry.Name)
			st.filesErr = ""
			runtime.Invalidate()
		}
	}
}

// buildFilePreview renders the selected file: Markdown through the official
// view, other text as plain text, images as a native bitmap and everything else
// as a metadata card.
func buildFilePreview(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService, runtime contracts.UIRuntime, st *state) {
	path := st.filesSelected
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space2).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Folder()).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
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
			design.Apply(ui.Text(c, string(preview.data)), t.MonoFamily()).
				FontSize(11).TextColor(t.LabelPrimary).MaxLines(40)
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
