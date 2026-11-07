package rightpanel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// DirectoryPicker is the Files pane's "open a folder" seam: the default opens
// the native mygo directory dialog and returns the first chosen path; tests
// replace it with a scripted selection so no desktop backend is needed.
var DirectoryPicker = func() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     "选择工作区文件夹",
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

// buildFilesPane is the Files tab: workspace header, breadcrumb navigation, the
// native directory listing and the read-only preview of the selected file.
func buildFilesPane(
	c *ui.Context,
	t *design.Tokens,
	workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime,
	st *state,
) {
	if workspace == nil {
		buildEmptyPane(c, t, icons.Folder(), uikit.T(st.locale, "工作区未启用", "Workspace is not enabled"))
		return
	}
	current, ok, err := workspace.Current(context.Background())
	if err != nil {
		buildErrorLine(c, t, uikit.T(st.locale, "读取工作区失败：", "Failed to read the workspace: ")+err.Error())
		return
	}
	if !ok {
		buildOpenWorkspace(c, t, workspace, runtime, st)
		return
	}

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Badge(c, uikit.T(st.locale, "工作区", "Workspace")), t.Badge())
			design.Apply(ui.Text(c, firstNonEmpty(current.Name, filepath.Base(current.Root))), t.Font(design.TypeXS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			refresh := ui.Button(c, uikit.T(st.locale, "刷新", "Refresh")).
				Tooltip(uikit.T(st.locale, "重新读取文件列表", "Reload the file list"))
			if refresh.Clicked() {
				st.filesPreview = make(map[string]*filePreview)
				st.filesErr = ""
				runtime.Invalidate()
			}
		})
		design.Apply(ui.Text(c, current.Root), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		buildBreadcrumb(c, t, workspace, runtime, st)
		if st.filesErr != "" {
			buildErrorLine(c, t, st.filesErr)
		}

		dir := st.filesDir
		entries, filesErr := workspace.Files(context.Background(), dir)
		switch {
		case filesErr != nil:
			buildErrorLine(c, t, uikit.T(st.locale, "列目录失败：", "Failed to list the directory: ")+filesErr.Error())
		case len(entries) == 0:
			design.Apply(ui.Text(c, uikit.T(st.locale, "目录为空", "The directory is empty")), t.TextMuted(design.TypeXS))
		default:
			for _, entry := range entries {
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
			buildErrorLine(c, t, st.filesErr)
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
	ui.Row(c).FillWidth().Gap(design.Space1).AlignItems(ui.Center).Children(func() {
		root := ui.Button(c, rootLabel).Tooltip(uikit.T(st.locale, "返回工作区根目录", "Back to the workspace root"))
		if root.Clicked() {
			navigateFiles(st, "", runtime)
		}
		for index, segment := range segments {
			design.Apply(ui.Text(c, "/"), t.TextMuted(design.TypeXXXS))
			label := "/" + segment
			index := index
			crumb := ui.Button(c, label).Tooltip(uikit.T(st.locale, "返回 ", "Back to ") + label)
			if crumb.Clicked() {
				navigateFiles(st, strings.Join(segments[:index+1], "/"), runtime)
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

// buildFileRow is one native directory row: folder or file glyph, the name and
// the click action (enter a directory, select a file for the preview).
func buildFileRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, entry contracts.FileEntry) {
	name := firstNonEmpty(entry.Name, filepath.Base(entry.Path))
	row := ui.ButtonBase(c).FillWidth().Height(fileRowHeight).PaddingX(design.Space2).
		Radius(design.RadiusSM).Label(name)
	if row.Hovered() {
		row.Background(t.BgHover)
	}
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
			buildErrorLine(c, t, uikit.T(st.locale, "读取文件失败：", "Failed to read the file: ")+preview.err)
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
			detailRow(c, t, uikit.T(st.locale, "大小", "Size"), formatFileSize(int64(len(preview.data))))
			detailRow(c, t, uikit.T(st.locale, "类型", "Type"), strings.TrimPrefix(filepath.Ext(path), "."))
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
