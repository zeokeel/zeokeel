package rightpanel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// filePreviewHeight bounds the read-only code editor inside a file tab: the
// editor virtualizes its lines and scrolls itself, and the tab's page scroll
// keeps the rest of the header reachable.
const filePreviewHeight = design.Space6 * 10

// buildPanel renders the whole sidebar for one frame: the tab strip and the
// active page (or opened file) below it. The host paints the docked surface
// and owns the width; the tab strip sits flush at the top, DSH-style.
func buildPanel(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	terminal contracts.TerminalService, artifacts contracts.ArtifactLister, cfg Config, st *state) {
	// The page commands' palette titles follow the host language, the live
	// workspace identity scopes the path-keyed caches (a workspace switch
	// re-reads the opened tabs instead of showing the old workspace's bytes),
	// and the active tab is clamped against the file tabs of the last frame.
	st.syncCommands()
	st.syncWorkspaceIdentity(workspace)
	root := ui.Column(c).FillWidth().Grow(1)
	root.Label(cfg.titleLabel(st.locale))
	root.Children(func() {
		buildTabStrip(c, t, st)
		st.clampTab()
		if fileIndex, ok := st.activeFileIndex(); ok {
			buildFileTabPage(c, t, workspace, st, fileIndex)
			return
		}
		switch st.tab {
		case pageFiles:
			buildFilesPage(c, t, workspace, artifacts, st)
		case pageTerminal:
			buildTerminalPage(c, t, terminal, st)
		default:
			buildGuidePage(c, t, st)
		}
	})
}

// buildTabStrip is the sidebar's only navigation: the fixed pages and one tab
// per opened file. The native TabsBase owns the tab-list semantics (role, the
// arrow keys, Home/End, the focus) and the strip scrolls sideways so a long
// list of open files never overflows the narrow rail.
func buildTabStrip(c *ui.Context, t *design.Tokens, st *state) {
	pageCount := st.pageCount()
	st.clampTab()
	tabs := ui.TabsBase(c, &st.tab, pageCount)
	tabs.List.Label(st.tr("右侧栏页面", "Right sidebar pages"))
	strip := ui.ScrollHorizontal(c).FillWidth().Shrink(0)
	strip.Padding(design.Space1, design.Space2)
	strip.Children(func() {
		tabs.List.Gap(design.Space1).AlignItems(ui.Center).Children(func() {
			closing := -1
			for index := 0; index < pageCount; index++ {
				buildTab(c, t, st, tabs, index, &closing)
			}
			if closing >= 0 {
				st.closeTab(closing)
			}
		})
	})
}

// buildTab builds one tab. Fixed pages are plain tabs; an opened file adds its
// close control. The guide's shortcut labels live on the fixed page tabs'
// cards, not on the tabs themselves.
func buildTab(c *ui.Context, t *design.Tokens, st *state, tabs ui.TabsParts, index int, closing *int) {
	label := st.tabLabel(index)
	active := index == st.tab
	tab := tabs.Tab(index).AlignItems(ui.Center).Height(design.Space8).
		PaddingX(design.Space2).Radius(design.RadiusSM)
	// TabsBase is the no-look base: the look is this module's, from tokens
	// only, with the active tab on the selected tint.
	if active {
		tab = tab.Background(t.BgSelected)
	}
	if active && st.revealTab {
		tab.ScrollIntoView()
	}
	tab.Children(func() {
		labelElement := design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
			TextColor(inactiveInk(t, active)).MaxLines(1).Ellipsis("…").NoWrap()
		if active {
			labelElement.Bold()
		}
		if index < pageFixedCount {
			return
		}
		filePath := st.fileTabs[index-pageFixedCount].path
		closeLabel := st.tr("关闭 ", "Close ") + filepath.Base(filePath)
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "rightpanel.tab.close/" + filePath,
			Label:   closeLabel,
			Tooltip: closeLabel,
			Icon:    icons.Close(),
			Size:    design.IconSizeS,
			OnClick: func() { *closing = index },
		})
	})
	if index == st.tab {
		st.revealTab = false
	}
}

// inactiveInk is the tab's text color: primary on the chosen tab, secondary on
// the rest, from the brand tokens.
func inactiveInk(t *design.Tokens, active bool) ui.Color {
	if active {
		return t.LabelPrimary
	}
	return t.LabelSecondary
}

// --- guide page -------------------------------------------------------------

// buildGuidePage is the 开始 landing: with no page open the sidebar offers the
// only two things it still owns, as two equal cards. Per the user-approved
// design the cards carry a title and their shortcut only: no explanatory copy
// lives here. The body is a native scroll so a short window cannot clip it.
func buildGuidePage(c *ui.Context, t *design.Tokens, st *state) {
	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		ui.Column(c).FillWidth().Padding(design.Space3).Gap(design.Space4).Children(func() {
			design.Apply(ui.Text(c, st.cfg.guideLabel(st.locale)), t.Font(design.TypeM)).
				TextColor(t.LabelPrimary).Role(ui.RoleHeading).Level(2).Bold()
			// Two equal side-by-side cards: the official Grid's two equal
			// columns keep them the same width regardless of their content.
			ui.Grid(c).FillWidth().Columns(2).GapX(design.Space2).GapY(design.Space2).Children(func() {
				buildGuideCard(c, t, st, icons.Folder(), st.tr("工作区文件", "Workspace files"),
					filesOpenShortcut, pageFiles)
				buildGuideCard(c, t, st, icons.Terminal(), st.tr("新建终端", "New terminal"),
					terminalOpenShortcut, pageTerminal)
			})
		})
	})
}

// buildGuideCard is one large action card: a native button (the official
// ButtonBase with the theme's look), equal in width to its neighbour, holding
// the glyph, the title and the shortcut. No description copy: the card names
// the action and its chord.
func buildGuideCard(c *ui.Context, t *design.Tokens, st *state, icon *ui.SVG,
	title, shortcut string, page int) {
	card := ui.Button(c, "").Label(title).Tooltip(title).Padding(design.Space3)
	card.Children(func() {
		// The card body stacks the glyph, the title and the shortcut: the rail
		// is 320 DIP wide, so two side-by-side cards cannot hold a horizontal
		// glyph + title + shortcut row without ellipsizing the title.
		ui.Column(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
			ui.Icon(c, icon).Size(design.IconSizeM, design.IconSizeM).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, title), t.Font(design.TypeS)).
				TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Bold()
			design.Apply(ui.Text(c, shortcut), t.MonoText(design.TypeXXXS)).
				TextColor(t.LabelTertiary).NoWrap()
		})
	})
	if card.Clicked() {
		st.showFixedPage(page)
	}
}

// --- file tab page ----------------------------------------------------------

// buildFileTabPage is one opened file's tab: a header naming the file with its
// copy-path and reload actions, then the folded workspace diff (when the file
// is changed) and the read-only preview, inside the page's native scroll.
func buildFileTabPage(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	st *state, index int) {
	if index < 0 || index >= len(st.fileTabs) {
		buildGuidePage(c, t, st)
		return
	}
	path := st.fileTabs[index].path
	preview := st.filePreviewFor(workspace, path)
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		buildFileTabHeader(c, t, st, path)
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(design.Space3).Gap(design.Space2).Children(func() {
				buildFileDiffFold(c, t, workspace, st, path)
				buildFilePreviewBody(c, t, st, path, preview)
			})
		})
	})
}

// buildFileTabHeader names the opened file (base name emphasized, parent
// directory muted) and carries the tab's two actions: copy the path and reload
// the preview from disk.
func buildFileTabHeader(c *ui.Context, t *design.Tokens, st *state, path string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space2, design.Space3).Children(func() {
		ui.Icon(c, icons.File()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelSecondary)
		design.Apply(ui.Text(c, filepath.Base(path)), t.Font(design.TypeXS)).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Tooltip(path).Bold()
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			design.Apply(ui.Text(c, dir+"/"), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
		ui.Spacer(c).Grow(1)
		copyLabel := st.tr("复制路径", "Copy path")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "rightpanel.file.copy/" + path,
			Label:   copyLabel,
			Tooltip: copyLabel,
			Icon:    icons.Copy(),
			Size:    design.IconSizeS,
			OnClick: func() {
				c.WriteClipboard(path)
			},
		})
		reloadLabel := st.tr("重新读取文件", "Reload the file")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "rightpanel.file.reload/" + path,
			Label:   reloadLabel,
			Tooltip: reloadLabel,
			Icon:    icons.Reload(),
			Size:    design.IconSizeS,
			OnClick: func() {
				st.reloadFile(path)
			},
		})
	})
}

// reloadFile drops one file's cached preview, editor and diff state, so the
// next frame re-reads it from the workspace (an agent may have edited it).
func (st *state) reloadFile(path string) {
	st.dropPathCaches(path)
	st.invalidate()
}

// buildFilePreviewBody renders one file preview: the official code editor for
// text, the Markdown view for .md, a native bitmap for images and an honest
// metadata fallback for everything else.
func buildFilePreviewBody(c *ui.Context, t *design.Tokens, st *state, path string, preview *filePreview) {
	switch {
	case preview == nil || preview.loading:
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Spinner(c).Label(st.tr("正在读取文件…", "Reading the file…")).
				Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, st.tr("正在读取文件…", "Reading the file…")),
				t.TextMuted(design.TypeXS))
		})
		return
	case preview.err != "":
		uikit.ErrorLine(c, st.tr("读取文件失败：", "Failed to read the file: ")+preview.err)
		return
	}
	switch {
	case preview.bitmap != nil:
		// The image is the tab's content, so it carries the file name as its
		// accessible name instead of reading as a decoration.
		ui.Image(c, preview.bitmap).Label(filepath.Base(path)).MaxWidthPercent(100)
	case isMarkdownPath(path):
		markdown.View(c, string(preview.data), nil)
	case isTextPath(path):
		editorView(c, t, st, path, preview.data)
	default:
		uikit.KeyValueRows(c, []uikit.KeyValue{
			{Key: st.tr("大小", "Size"), Value: formatFileSize(int64(len(preview.data)))},
			{Key: st.tr("类型", "Type"), Value: strings.TrimPrefix(filepath.Ext(path), ".")},
		})
		design.Apply(ui.Text(c, st.tr("暂不支持预览", "Preview is not supported")),
			t.TextMuted(design.TypeXXS))
	}
	if preview.truncated {
		design.Apply(ui.Text(c, st.tr("仅显示前 256 KiB", "Showing the first 256 KiB only")),
			t.TextMuted(design.TypeXXXS))
	}
}

// editorView is the read-only official code editor of one text file, at a
// token-bounded height so its own scroll keeps a long source file inside the
// page.
func editorView(c *ui.Context, t *design.Tokens, st *state, path string, data []byte) {
	state := st.editorFor(path, data)
	_ = t
	editor.View(c, state).Height(filePreviewHeight).FillWidth()
}

// --- files page -------------------------------------------------------------

// buildFilesPage is the 文件 page: the deliverables history and the workspace
// tree inside one native page scroll, with the workspace header above it.
func buildFilesPage(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	artifacts contracts.ArtifactLister, st *state) {
	if workspace == nil {
		buildScrollableBody(c, func() {
			buildEmptyPane(c, st.tr("工作区未启用", "Workspace is not enabled"))
		})
		return
	}
	current, ok, err := workspace.Current(context.Background())
	if err != nil {
		buildScrollableBody(c, func() {
			uikit.ErrorLine(c, st.tr("读取工作区失败：", "Failed to read the workspace: ")+err.Error())
		})
		return
	}
	if !ok {
		buildScrollableBody(c, func() {
			buildOpenWorkspace(c, t, workspace, st.runtime, st)
		})
		return
	}
	// The root listing is cached and read off the UI thread: the first frame
	// after opening the page shows the loading row, the posted result the tree.
	entries, loading, listErr := st.syncFilesRoot(workspace, st.runtime)
	ui.Column(c).FillWidth().Grow(1).Gap(design.Space2).Children(func() {
		buildWorkspaceHeader(c, t, st, current, loading)
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(design.Space2, design.Space3).Gap(design.Space3).Children(func() {
				if artifacts != nil {
					buildArtifactsSection(c, t, artifacts, st.runtime, st)
				}
				switch {
				case listErr != nil:
					uikit.ErrorLine(c, st.tr("列目录失败：", "Failed to list the directory: ")+listErr.Error())
				case loading && entries == nil:
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						ui.Spinner(c).Label(st.tr("正在读取目录…", "Reading the directory…")).
							Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
						design.Apply(ui.Text(c, st.tr("正在读取目录…", "Reading the directory…")),
							t.TextMuted(design.TypeXS))
					})
				case len(entries) == 0:
					design.Apply(ui.Text(c, st.tr("目录为空", "The directory is empty")),
						t.TextMuted(design.TypeXS))
				default:
					buildWorkspaceTree(c, t, workspace, entries, st)
				}
			})
		})
	})
}

// buildWorkspaceHeader names the current workspace and carries the page's one
// action: re-read the tree from disk (and drop the cached diffs with it).
func buildWorkspaceHeader(c *ui.Context, t *design.Tokens, st *state,
	current contracts.Workspace, loading bool) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space2, design.Space3).Children(func() {
		design.Apply(ui.Text(c, st.tr("工作区", "Workspace")), t.TextMuted(design.TypeXXXS)).NoWrap()
		design.Apply(ui.Text(c, firstNonEmpty(current.Name, filepath.Base(current.Root))),
			t.Font(design.TypeXS)).TextColor(t.LabelPrimary).
			Grow(1).MaxLines(1).Ellipsis("…").Tooltip(current.Root).Bold()
		if loading {
			ui.Spinner(c).Label(st.tr("正在读取目录…", "Reading the directory…")).
				Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
		}
		reloadLabel := st.tr("重新读取目录", "Refresh files")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:      "rightpanel.files.refresh",
			Label:    reloadLabel,
			Tooltip:  st.tr("重新读取工作区文件", "Reload the workspace files"),
			Icon:     icons.Reload(),
			Size:     design.IconSizeS,
			Disabled: loading,
			OnClick: func() {
				st.refreshFiles()
			},
		})
	})
}

// buildWorkspaceTree renders the workspace's root entries with the native
// ui.Tree: directories show their children only while open, and a directory's
// listing is read the first time the user opens it. A file row's click opens
// its tab; a directory row's click opens or closes it.
func buildWorkspaceTree(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	entries []contracts.FileEntry, st *state) {
	shown, hidden := capTreeEntries(entries)
	ui.Tree(c, func() {
		for _, entry := range shown {
			st.buildTreeNode(c, t, workspace, entry)
		}
		if hidden > 0 {
			design.Apply(ui.Text(c, fmt.Sprintf(st.tr("还有 %d 项未显示", "%d more entries not shown"), hidden)),
				t.TextMuted(design.TypeXXXS)).Padding(design.Space1, design.Space2)
		}
	})
}

// capTreeEntries bounds one directory's rendered rows (the native tree builds
// every row it shows). It returns the rows and how many were left out, so the
// tree can say so honestly instead of silently dropping entries.
func capTreeEntries(entries []contracts.FileEntry) ([]contracts.FileEntry, int) {
	if len(entries) <= maxTreeEntries {
		return entries, 0
	}
	return entries[:maxTreeEntries], len(entries) - maxTreeEntries
}

// buildTreeNode is one row of the workspace tree.
func (st *state) buildTreeNode(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	entry contracts.FileEntry) {
	path := firstNonEmpty(entry.Path, entry.Name)
	if entry.Dir {
		open := st.directoryOpen(path)
		item := ui.TreeItem(c, firstNonEmpty(entry.Name, filepath.Base(path)), open, func() {
			st.buildDirectoryChildren(c, t, workspace, path)
		})
		if item.Clicked() {
			*open = !*open
			st.invalidate()
		}
		return
	}
	item := ui.TreeItem(c, firstNonEmpty(entry.Name, filepath.Base(path)), nil, nil)
	item.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(st.tr("打开", "Open")).Chosen() {
			st.openFile(path)
		}
		if menu.Item(st.tr("复制路径", "Copy path")).Chosen() {
			c.WriteClipboard(path)
		}
	})
	if item.Clicked() {
		st.openFile(path)
		st.invalidate()
	}
}

// buildDirectoryChildren renders (and lazily loads) one open directory's
// entries. It runs while the tree builds the open item's group.
func (st *state) buildDirectoryChildren(c *ui.Context, t *design.Tokens,
	workspace contracts.WorkspaceService, path string) {
	listing := st.syncDir(path, workspace)
	if listing == nil || (listing.loading && !listing.loaded) {
		design.Apply(ui.Text(c, st.tr("正在读取…", "Loading…")), t.TextMuted(design.TypeXXXS)).
			Padding(design.Space1, design.Space2)
		return
	}
	if listing.err != nil {
		uikit.ErrorLine(c, st.tr("列目录失败：", "Failed to list the directory: ")+listing.err.Error())
		return
	}
	if len(listing.entries) == 0 {
		design.Apply(ui.Text(c, st.tr("目录为空", "The directory is empty")),
			t.TextMuted(design.TypeXXXS)).Padding(design.Space1, design.Space2)
		return
	}
	shown, hidden := capTreeEntries(listing.entries)
	for _, child := range shown {
		st.buildTreeNode(c, t, workspace, child)
	}
	if hidden > 0 {
		design.Apply(ui.Text(c, fmt.Sprintf(st.tr("还有 %d 项未显示", "%d more entries not shown"), hidden)),
			t.TextMuted(design.TypeXXXS)).Padding(design.Space1, design.Space2)
	}
}

// directoryOpen returns the stable fold state pointer of one directory (the
// native TreeItem owns the *bool).
func (st *state) directoryOpen(path string) *bool {
	if st.filesOpen == nil {
		st.filesOpen = make(map[string]*bool)
	}
	open, ok := st.filesOpen[path]
	if !ok {
		open = new(bool)
		st.filesOpen[path] = open
	}
	return open
}

// buildEmptyPane is the centered empty state of one page: a title in flow,
// without the decorative icon-in-a-colored-circle anti-pattern.
func buildEmptyPane(c *ui.Context, text string) {
	uikit.EmptyStateBlock(c, text, "", nil)
}

// buildScrollableBody wraps a short page state (empty/error/onboarding) in the
// page's native scroll: page bodies scroll, so even an unexpected amount of
// content in an otherwise empty state can never clip against the rail.
func buildScrollableBody(c *ui.Context, body func()) {
	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		ui.Column(c).FillWidth().Padding(design.Space3).Children(body)
	})
}
