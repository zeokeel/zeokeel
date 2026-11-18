package rightpanel

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/lib/openpath"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
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
	artifacts contracts.ArtifactLister, cfg Config, st *state) {
	// The page commands' palette titles follow the host language, the live
	// workspace identity scopes the path-keyed caches (a workspace switch
	// re-reads the opened tabs instead of showing the old workspace's bytes),
	// and the active tab is clamped against the file tabs of the last frame.
	st.syncCommands()
	st.syncWorkspaceIdentity(workspace)
	root := ui.Column(c).FillWidth().Grow(1)
	root.Label(cfg.titleLabel(st.locale))
	root.Children(func() {
		activeTab := buildTabStrip(c, t, st)
		st.clampTab()
		// The page under the strip is the chosen tab's content, so it is the
		// tab panel of ARIA's Tabs pattern (mygo RoleTabPanel): named after the
		// tab it shows, with the tab pointing back through Controls, so a screen
		// reader can move between a tab and its content.
		panel := ui.Column(c).FillWidth().Grow(1).
			Role(ui.RoleTabPanel).Label(st.tabLabel(st.tab))
		panel.Children(func() {
			if fileIndex, ok := st.activeFileIndex(); ok {
				buildFileTabPage(c, t, workspace, st, fileIndex)
				return
			}
			switch st.tab {
			case pageFiles:
				buildFilesPage(c, t, workspace, artifacts, st)
			default:
				buildGuidePage(c, t, st)
			}
		})
		if activeTab.Valid() {
			activeTab.Controls(panel)
		}
	})
}

// buildTabStrip is the sidebar's navigation: two underline tab rows, the
// fixed view pages above and the opened file documents below (V10). The rows
// are separate native tab lists (a document tab and a view tab are different
// levels of navigation and never share a row); each row owns only its own
// choice and maps it back to the active tab index. It returns the element of
// the chosen tab, or the zero element when the tab rows show none, so the
// caller can point it at the tab panel with Controls.
func buildTabStrip(c *ui.Context, t *design.Tokens, st *state) ui.Element {
	st.clampTab()
	activeTab := buildViewTabStrip(c, t, st)
	if tab := buildDocumentTabStrip(c, t, st); tab.Valid() {
		activeTab = tab
	}
	st.revealTab = false
	return activeTab
}

// buildViewTabStrip renders the fixed view pages (开始/文件/命令) as the
// underline tab language: 13 DIP labels, the chosen one semibold with a 2 DIP
// accent underline, on a strip with one hairline underneath. Visiting a page
// takes the index from the native tab list (keyboard and pointer alike). It
// returns the chosen tab's element when a fixed page is chosen.
func buildViewTabStrip(c *ui.Context, t *design.Tokens, st *state) ui.Element {
	// TabsBase clamps its bound index into range, so a document tab (an index
	// past the fixed pages) is mirrored as "no view page chosen"; the row's
	// own Changed is the only real user choice.
	st.viewTab = -1
	if st.tab >= 0 && st.tab < pageFixedCount {
		st.viewTab = st.tab
	}
	viewTabs := ui.TabsBase(c, &st.viewTab, pageFixedCount)
	viewTabs.List.Label(st.tr("视图页面", "View pages"))
	var activeTab ui.Element
	ui.Column(c).FillWidth().Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).
		Children(func() {
			strip := ui.ScrollHorizontal(c).FillWidth().Shrink(0)
			strip.Padding(design.Space1, design.Space2)
			strip.Children(func() {
				viewTabs.List.Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					for index := 0; index < pageFixedCount; index++ {
						// The page row is the highest level of navigation, so it
						// keeps the panel's ONE accent underline even while a
						// document opened from that page is the active tab
						// (VD-16: two accent underlines 38 DIP apart competed).
						active := st.tab == index ||
							(index == pageFiles && st.documentActive())
						tab := buildUnderlineTab(c, t, viewTabs, index, st.tabLabel(index), active)
						if st.tab == index {
							activeTab = tab
						}
					}
				})
			})
		})
	if viewTabs.List.Changed() && st.viewTab >= 0 && st.viewTab < pageFixedCount {
		st.tab = st.viewTab
	}
	return activeTab
}

// buildDocumentTabStrip renders the opened files on their own underline row,
// below the view pages: one tab per file, the active one carrying the accent
// underline and every tab its close action. The row only exists while a file
// is open. It returns the chosen document tab's element when a file is chosen.
func buildDocumentTabStrip(c *ui.Context, t *design.Tokens, st *state) ui.Element {
	docCount := len(st.fileTabs)
	if docCount == 0 {
		return ui.Element{}
	}
	st.docTab = -1
	if index, ok := st.activeFileIndex(); ok {
		st.docTab = index
	}
	docTabs := ui.TabsBase(c, &st.docTab, docCount)
	docTabs.List.Label(st.tr("打开的文件", "Open files"))
	closing := -1
	var activeTab ui.Element
	ui.Column(c).FillWidth().Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).
		Children(func() {
			strip := ui.ScrollHorizontal(c).FillWidth().Shrink(0)
			strip.Padding(design.Space1, design.Space2)
			strip.Children(func() {
				docTabs.List.Gap(design.Space1).AlignItems(ui.Center).Children(func() {
					for index := range st.fileTabs {
						tab := buildDocumentTab(c, t, st, docTabs, index, &closing)
						if st.tab == pageFixedCount+index {
							activeTab = tab
						}
					}
				})
			})
		})
	if closing >= 0 {
		st.closeTab(pageFixedCount + closing)
		return ui.Element{}
	}
	if docTabs.List.Changed() && st.docTab >= 0 && st.docTab < docCount {
		st.tab = pageFixedCount + st.docTab
	}
	return activeTab
}

// buildUnderlineTab builds one view-page tab: the underline vocabulary used by
// the main screen's tabs (13 DIP, the chosen label semibold in LabelPrimary and
// a 2 DIP accent underline). The 2 DIP border is reserved on every tab so the
// row does not jump when the choice moves. The underline is an element border,
// not a hand-drawn line.
func buildUnderlineTab(c *ui.Context, t *design.Tokens, tabs ui.TabsParts, index int, label string, active bool) ui.Element {
	tab := tabs.Tab(index).AlignItems(ui.Center).Height(design.ControlHeight).
		PaddingX(design.Space3).
		BorderWidth(0, 0, 2, 0).BorderColor(ui.Transparent)
	if active {
		tab = tab.BorderColor(t.Accent)
	}
	tab.Children(func() {
		ink, weight := t.LabelSecondary, design.WeightRegular
		if active {
			ink, weight = t.LabelPrimary, design.WeightSemibold
		}
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).
			FontWeight(weight).TextColor(ink).MaxLines(1).Ellipsis("…").NoWrap()
	})
	return tab
}

// buildDocumentTab builds one opened file's tab in the same underline
// vocabulary, with the base name, the full path as its tooltip and the close
// action. The active document tab is scrolled into view when a file opens.
func buildDocumentTab(c *ui.Context, t *design.Tokens, st *state, tabs ui.TabsParts,
	index int, closing *int) ui.Element {
	path := st.fileTabs[index].path
	active := st.tab == pageFixedCount+index
	label := st.tabLabel(pageFixedCount + index)
	tab := tabs.Tab(index).AlignItems(ui.Center).Height(design.ControlHeight).
		PaddingX(design.Space2).Gap(design.Space1).Radius(design.RadiusSM).
		// Name the tab by the file alone: without an explicit Label the
		// accessible name concatenates the close control's label too, which
		// made the tab untestable by name and noisy for screen readers.
		Label(label).
		Transition(design.TransitionColors(c))
	if active {
		// The chosen document reads as a raised chip — BgSelected fill + primary
		// ink — instead of a second accent underline under the page row's
		// (VD-16). The row's own 1 DIP hairline still separates the strip.
		tab = tab.Background(t.BgSelected)
		if st.revealTab {
			tab.ScrollIntoView()
		}
	}
	tab.Children(func() {
		ink, weight := t.LabelSecondary, design.WeightRegular
		if active {
			ink, weight = t.LabelPrimary, design.WeightSemibold
		}
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).
			FontWeight(weight).TextColor(ink).MaxLines(1).Ellipsis("…").NoWrap()
		closeLabel := st.tr("关闭 ", "Close ") + filepath.Base(path)
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "rightpanel.tab.close/" + path,
			Label:   closeLabel,
			Icon:    icons.Close(),
			Size:    design.IconSizeS,
			OnClick: func() { *closing = index },
		})
	})
	return tab
}

// --- guide page -------------------------------------------------------------

// buildGuidePage is the 开始 landing: with no page open the sidebar offers the
// one thing it still owns, the workspace files page. Per the user-approved
// design the card carries a title and its shortcut only: no explanatory copy
// lives here. The body is a native scroll so a short window cannot clip it.
func buildGuidePage(c *ui.Context, t *design.Tokens, st *state) {
	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		// The view tab already names the pane (开始): repeating it as an H2 in
		// the body was the duplicate-heading finding (B7/P3-8).
		ui.Column(c).FillWidth().Padding(design.Space3).Gap(design.Space4).Children(func() {
			// One full-width card: the workspace files page is the sidebar's
			// only fixed destination now (the command page is gone).
			ui.Grid(c).FillWidth().Columns(1).GapY(design.Space2).Children(func() {
				buildGuideCard(c, t, st, icons.Folder(), st.tr("工作区文件", "Workspace files"),
					filesOpenShortcut, pageFiles)
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
	card := ui.Button(c, "").Label(title).Padding(design.Space3)
	card.Children(func() {
		// The card body stacks the glyph, the title and the shortcut: the rail
		// is 320 DIP wide, so two side-by-side cards cannot hold a horizontal
		// glyph + title + shortcut row without ellipsizing the title.
		ui.Column(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
			ui.Icon(c, icon).Size(design.IconSizeM, design.IconSizeM).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, title), t.Font(design.TypeS)).
				TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
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
		buildFileTabHeader(c, t, st, path, preview)
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(design.Space3).Gap(design.Space2).Children(func() {
				buildFileDiffFold(c, t, workspace, st, path)
				buildFilePreviewBody(c, t, st, path, preview)
			})
		})
	})
}

// buildFileTabHeader is the opened file's one remaining header row: a
// breadcrumb (the file's parent directory, 12 DIP) and the tab's actions, on a
// 32 DIP line. The file's own name is NOT repeated here — the document tab
// directly above already names it, and the old second copy was one of the three
// stacked header layers and the third README.md inside 100 DIP (VD-16). The
// row's inks are the metadata step, and no control here carries a Tooltip: the
// panel can be shown as a drawer, and a tooltip is an overlay that takes the
// first Escape from the surface that owns it (A4-I-01).
func buildFileTabHeader(c *ui.Context, t *design.Tokens, st *state, path string, preview *filePreview) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space1, design.Space3).Height(design.RowCompactHeight).Children(func() {
		ui.Icon(c, icons.Folder()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelTertiary)
		// The crumb is the path back to the tree, never the file's own name: the
		// document tab one row above already names the file, and repeating it
		// here was the "same name three times in 100 DIP" the audit measured
		// (VD-16). At the workspace root the crumb names the root itself, and it
		// is a control: clicking it (or Enter on it) returns to the file tree
		// rather than closing the file (A4-I-10).
		dir, _ := filepath.Split(path)
		crumb := strings.TrimSuffix(dir, string(filepath.Separator))
		if crumb == "" || crumb == "." {
			homeLabel := st.tr("工作区根目录", "Workspace root")
			back := ui.ButtonBase(c).Shrink(0).Radius(design.RadiusSM).
				PaddingX(design.Space1).Height(design.IconSizeL).
				Label(st.tr("返回工作区根目录", "Back to the workspace root")).
				Transition(design.TransitionColors(c))
			if back.Hovered() || back.Focused() {
				back = back.Background(t.BgHover)
			}
			back.Children(func() {
				design.Apply(ui.Text(c, homeLabel), t.Font(design.TypeXXS)).
					TextColor(t.LabelSecondary).MaxLines(1)
			})
			if back.Clicked() {
				st.showFixedPage(pageFiles)
			}
		} else {
			design.Apply(ui.Text(c, crumb), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
		ui.Spacer(c).Grow(1)
		copyLabel := st.tr("复制路径", "Copy path")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:   "rightpanel.file.copy/" + path,
			Label: copyLabel,
			Icon:  icons.Copy(),
			Size:  design.IconSizeS,
			OnClick: func() {
				c.WriteClipboard(path)
			},
		})
		reloadLabel := st.tr("重新读取文件", "Reload the file")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:   "rightpanel.file.reload/" + path,
			Label: reloadLabel,
			Icon:  icons.Reload(),
			Size:  design.IconSizeS,
			OnClick: func() {
				st.reloadFile(path)
			},
		})
		if classifyFilePreview(path, preview) == filePreviewExternal && nativeLauncherAvailable() {
			openLabel := st.tr("用系统应用打开", "Open natively")
			uikit.IconButton(c, uikit.IconButtonOptions{
				Key:   "rightpanel.file.open-native/" + path,
				Label: openLabel + " " + filepath.Base(path),
				Icon:  icons.FolderOpen(),
				Size:  design.IconSizeS,
				OnClick: func() {
					st.openPreviewNatively(path, preview)
				},
			})
		}
	})
}

// reloadFile drops one file's cached preview, editor and diff state, so the
// next frame re-reads it from the workspace (an agent may have edited it).
func (st *state) reloadFile(path string) {
	st.dropPathCaches(path)
	st.invalidate()
}

// buildFilePreviewBody renders one file preview: the official code editor for
// text, the Markdown view for .md, a native bitmap for images, an error row for
// a failed read and the external-open fallback for a file this build cannot
// render inline.
func buildFilePreviewBody(c *ui.Context, t *design.Tokens, st *state, path string, preview *filePreview) {
	switch classifyFilePreview(path, preview) {
	case filePreviewLoading:
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Spinner(c).Label(st.tr("正在读取文件…", "Reading the file…")).
				Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, st.tr("正在读取文件…", "Reading the file…")),
				t.TextMuted(design.TypeXS))
		})
		return
	case filePreviewError:
		uikit.ErrorLine(c, st.tr("读取文件失败：", "Failed to read the file: ")+preview.err)
		return
	case filePreviewImage:
		// The image is the tab's content, so it carries the file name as its
		// accessible name instead of reading as a decoration.
		ui.Image(c, preview.bitmap).Label(filepath.Base(path)).MaxWidthPercent(100)
	case filePreviewMarkdown:
		markdown.View(c, string(preview.data), docMarkdown(t))
	case filePreviewText:
		editorView(c, t, st, path, preview.data)
	case filePreviewExternal:
		buildExternalPreview(c, t, st, path, preview)
	}
	if preview.truncated {
		// A truncation notice is a sentence, so it takes the metadata step, not
		// TypeXXXS's 11 DIP (DESIGN.md §5.3, audit UD-05).
		design.Apply(ui.Text(c, st.tr("仅显示前 256 KiB", "Showing the first 256 KiB only")),
			t.TextMuted(design.TypeXXS))
	}
}

// buildExternalPreview is the honest fallback for a document this build cannot
// render: the panel ships no embedded PDF/Office/HTML renderer (and no
// third-party one), so the file's metadata and a clear explanation take the
// preview's place, with "open in the system application" as the primary
// action. A build without the launcher seam hides the action instead of
// offering a dead button.
func buildExternalPreview(c *ui.Context, t *design.Tokens, st *state, path string, preview *filePreview) {
	if len(preview.data) > 0 {
		// The product's one label/value shape (A1 matrix "rightpanel/view.go:399
		// → PropertySheet"): a fixed label column, a 32 DIP row and a hairline
		// between rows, so a value never sits at the panel's far edge. Full
		// width so the sheet's dividers end where the panel's own do.
		uikit.PropertySheet(c, uikit.PropertySheetOptions{
			Key:       "rightpanel.preview.properties",
			Locale:    st.locale,
			FullWidth: true,
			Rows: []uikit.PropertyRow{
				{Label: st.tr("大小", "Size"), Value: formatFileSize(int64(len(preview.data))), Mono: true},
				{Label: st.tr("类型", "Type"), Value: strings.TrimPrefix(filepath.Ext(path), "."), Mono: true},
			},
		})
	}
	design.Apply(ui.Text(c, st.tr("暂不支持预览", "Preview is not supported")),
		t.Font(design.TypeXS)).TextColor(t.LabelPrimary)
	design.Apply(ui.Text(c, st.tr(
		"本机没有内嵌 PDF、Office 或 HTML 渲染器，无法在侧边栏显示该文件。",
		"This build has no embedded PDF, Office or HTML renderer, so the file cannot be shown here.")),
		t.TextMuted(design.TypeXXS)).MaxLines(3)
	design.Apply(ui.Text(c, st.externalPreviewHint(path, preview)),
		t.TextMuted(design.TypeXXS)).MaxLines(2)
	if preview.openErr != "" {
		uikit.ErrorLine(c, preview.openErr)
	}
	if nativeLauncherAvailable() {
		action := design.Apply(ui.PrimaryButton(c, st.tr("用系统应用打开", "Open natively")),
			t.PrimaryButton())
		if action.Clicked() {
			st.openPreviewNatively(path, preview)
		}
	}
}

// externalPreviewHint names the concrete fallback reason: the document family,
// the oversized image, or a binary/unknown type.
func (st *state) externalPreviewHint(path string, preview *filePreview) string {
	if isImagePath(path) && preview != nil && strings.HasPrefix(preview.err, imageTooLargeErrPrefix) {
		return st.tr("图片超过内嵌预览上限，请用系统应用查看。",
			"The image is larger than the embedded preview limit; open it with the system application.")
	}
	switch {
	case hasExt(path, pdfExts):
		return st.tr("PDF 文档需要系统应用打开。", "Open PDF documents with the system application.")
	case hasExt(path, officeExts):
		return st.tr("Office 文档需要系统应用打开。", "Open Office documents with the system application.")
	case hasExt(path, htmlExts):
		return st.tr("HTML 页面需要浏览器打开。", "Open HTML pages in the browser.")
	default:
		return st.tr("二进制或其它类型没有内嵌预览。",
			"Binary and other file types have no embedded preview.")
	}
}

// nativeLauncherAvailable reports whether the platform launcher seam is wired.
// A host build without it (or a headless test) hides the external-open action
// instead of showing a button that cannot work.
func nativeLauncherAvailable() bool {
	return openpath.Open != nil
}

// openPreviewNatively hands one previewed file to the platform's default
// application. It reuses the same workspace containment as the previewer's
// OpenNative entry point (a relative path under the live root, no escapes), and
// keeps a localized failure inline on the tab's preview.
func (st *state) openPreviewNatively(path string, preview *filePreview) {
	if preview == nil || !nativeLauncherAvailable() {
		return
	}
	prefix := st.tr("打开文件失败：", "Could not open the file: ")
	_, native, err := st.resolveWorkspacePath(st.workspaceService(), path)
	if err != nil {
		preview.openErr = prefix + err.Error()
		st.invalidate()
		return
	}
	if err := openpath.Open(native); err != nil {
		preview.openErr = prefix + err.Error()
	} else {
		preview.openErr = ""
	}
	st.invalidate()
}

// docMarkdown is the narrow-panel Markdown theme of the inspector's file and
// artifact previews (uikit.MarkdownDoc): body TypeXS (13 DIP) and headings
// scaled off it, so a document never outranks the screen title while the panel
// is only ~320 DIP wide (V16).
func docMarkdown(t *design.Tokens) *markdown.Options {
	size, _, _ := t.Metrics(design.TypeXS)
	return uikit.MarkdownDoc(t, size)
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
		// The tree is the virtualized native Outline (UD-14): it must own a
		// bounded height to build only the rows in view, so the page body is a
		// bounded column (not an unbounded scroll) and the outline grows into
		// the remaining space. The deliverables history sits above it in flow.
		ui.Column(c).FillWidth().Grow(1).Shrink(1).
			Padding(design.Space2, design.Space3).Gap(design.Space3).Children(func() {
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
				uikit.EmptyState(c, st.tr("目录为空", "The directory is empty"))
			default:
				buildWorkspaceTree(c, t, workspace, entries, st)
				buildTreePreview(c, t, workspace, indexOfEntries(entries, st), st)
			}
		})
	})
}

// indexOfEntries is the path→entry index the tree builders share: the root
// listing plus every directory already loaded. buildWorkspaceTree builds its own
// for the row contents; the preview only needs to know whether the current item
// is a directory.
func indexOfEntries(entries []contracts.FileEntry, st *state) map[string]contracts.FileEntry {
	index := make(map[string]contracts.FileEntry, len(entries))
	for _, entry := range entries {
		index[treeEntryPath(entry)] = entry
	}
	for _, listing := range st.filesDirs {
		if listing.loaded {
			for _, entry := range listing.entries {
				index[treeEntryPath(entry)] = entry
			}
		}
	}
	return index
}

// buildTreePreview is the tree's preview area: the current row's content, read
// as the cursor moves. It is the answer to "the chosen row and the content
// disagree" (A4-I-11): the arrows move the current row and this area follows in
// the same frame, without opening a tab. A directory is a place but has no
// content, so it says 目录 in one line and shows nothing else; with no current
// row (a fresh panel) the area is empty and takes no height.
func buildTreePreview(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	index map[string]contracts.FileEntry, st *state) {
	path, ok := st.treeCurrentPath()
	if !ok {
		return
	}
	entry, known := index[path]
	if known && entry.Dir {
		ui.Column(c).FillWidth().Shrink(0).Gap(design.Space1).Children(func() {
			ui.Divider(c)
			design.Apply(ui.Text(c, st.tr("目录", "Directory")), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(1)
		})
		return
	}
	preview := st.filePreviewFor(workspace, path)
	ui.Column(c).FillWidth().Shrink(0).Gap(design.Space1).Children(func() {
		ui.Divider(c)
		// The area names the file it is showing: the tree row above may have
		// truncated it, and the preview must never be anonymous.
		design.Apply(ui.Text(c, filepath.Base(path)), t.Font(design.TypeXXS)).
			TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
		ui.Scroll(c).FillWidth().MaxHeight(design.Space8 * 8).Children(func() {
			buildFilePreviewBody(c, t, st, path, preview)
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
		design.Apply(ui.Text(c, textutil.FirstNonEmptyTrimmed(current.Name, filepath.Base(current.Root))),
			t.Font(design.TypeXS)).TextColor(t.LabelPrimary).
			Grow(0).Shrink(1).MaxLines(1).Ellipsis("…")
		// The folder was a Tooltip; the panel is a drawer, and a tooltip is an
		// overlay that takes the first Escape from the drawer that owns it
		// (A4-I-01). The path is visible copy instead, so nothing is lost.
		if root := strings.TrimSpace(current.Root); root != "" {
			design.Apply(ui.Text(c, root), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Grow(1).Shrink(1).MinWidth(design.Space0).
				MaxLines(1).Ellipsis("…")
		} else {
			ui.Spacer(c).Grow(1)
		}
		if loading {
			ui.Spinner(c).Label(st.tr("正在读取目录…", "Reading the directory…")).
				Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
		}
		reloadLabel := st.tr("重新读取目录", "Refresh files")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:      "rightpanel.files.refresh",
			Label:    reloadLabel,
			Icon:     icons.Reload(),
			Size:     design.IconSizeS,
			Disabled: loading,
			OnClick: func() {
				st.refreshFiles()
			},
		})
	})
}

// treeStateSuffix marks a pseudo-child row that reports an open directory's
// loading/empty/error state (a real path never contains a NUL). The outline's
// item graph stays pure paths; these render as indented state rows.
const (
	treeStateLoading = "\x00loading"
	treeStateEmpty   = "\x00empty"
	treeStateError   = "\x00error"
)

// treeEntryPath is one tree item's stable path key.
func treeEntryPath(entry contracts.FileEntry) string {
	return textutil.FirstNonEmptyTrimmed(entry.Path, entry.Name)
}

// buildWorkspaceTree renders the workspace tree with the virtualized native
// ui.Outline (2026-11 audit UD-14): only the rows in view are built, so a
// directory of any size is fully reachable (the old ui.Tree capped at 400).
// Directories lazy-load their listing through the existing syncDir cache the
// first time they open; a file row's click opens its tab, a directory row's
// click opens or closes it, and the outline owns the keyboard (arrows move,
// Right/Left open and close, Enter activates).
func buildWorkspaceTree(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	entries []contracts.FileEntry, st *state) {
	// Index every known entry by path (the root listing plus every loaded
	// directory), so the row builders stay pure lookups.
	index := make(map[string]contracts.FileEntry, len(entries))
	addEntries := func(list []contracts.FileEntry) {
		for _, entry := range list {
			index[treeEntryPath(entry)] = entry
		}
	}
	addEntries(entries)
	for _, listing := range st.filesDirs {
		if listing.loaded {
			addEntries(listing.entries)
		}
	}
	roots := make([]string, 0, len(entries))
	for _, entry := range entries {
		roots = append(roots, treeEntryPath(entry))
	}

	// children gives an item's children: nil for a file (a leaf), a non-nil
	// empty slice for a closed directory (a branch with an arrow, but its
	// listing is NOT read yet — lazy load only on open), and the loaded child
	// paths (or one state pseudo-row) for an open directory.
	children := func(path string) []string {
		if strings.Contains(path, "\x00") {
			return nil
		}
		entry, ok := index[path]
		if !ok || !entry.Dir {
			return nil
		}
		if !st.filesOutline.Open.Has(path) {
			return []string{}
		}
		listing := st.syncDir(path, workspace)
		if listing == nil || (listing.loading && !listing.loaded) {
			return []string{path + treeStateLoading}
		}
		if listing.err != nil {
			return []string{path + treeStateError}
		}
		if len(listing.entries) == 0 {
			return []string{path + treeStateEmpty}
		}
		kids := make([]string, 0, len(listing.entries))
		for _, child := range listing.entries {
			kids = append(kids, treeEntryPath(child))
		}
		return kids
	}

	if st.filesRow >= st.filesOutline.Rows() {
		st.filesRow = -1
	}
	st.filesOutline.List.Selected = &st.filesRow
	st.filesOutline.List.Label = func(row int) string {
		if row < 0 || row >= st.filesOutline.Rows() {
			return ""
		}
		item := st.filesOutline.Item(row)
		if entry, ok := index[item]; ok {
			return textutil.FirstNonEmptyTrimmed(entry.Name, filepath.Base(item))
		}
		return ""
	}
	// The native outline paints its chosen row with the theme's Accent, which in
	// this product is the brand blue: a full accent block inside a 320 DIP pane,
	// and exactly the "满蓝选中行" the design forbids. The local theme lowers the
	// accent to the persistent-selection tint (as the sidebar tree's list does),
	// so the chosen row is BgSelected + the 2 DIP stripe this file paints, while
	// the native keyboard, hit testing and Selected state are untouched.
	var outline ui.Element
	ui.Themed(c, treeSelectionTheme(t)).Grow(1).Shrink(1).FillWidth().Children(func() {
		outline = ui.Outline(c, &st.filesOutline, roots, children, func(path string) {
			st.buildTreeRow(c, t, index, path)
		}).Label(st.tr("工作区文件", "Workspace files")).Grow(1).FillWidth()
	})
	if outline.Submitted() && st.filesRow >= 0 && st.filesRow < st.filesOutline.Rows() {
		// Enter and a double click open the current row (a directory toggles).
		st.activateTreeItem(index, st.filesOutline.Item(st.filesRow))
	}
}

// buildTreeRow is one outline row's content (after the outline's arrow): a
// directory or file name, or an open directory's loading/empty/error state row.
func (st *state) buildTreeRow(c *ui.Context, t *design.Tokens, index map[string]contracts.FileEntry, path string) {
	switch {
	case strings.HasSuffix(path, treeStateLoading):
		design.Apply(ui.Text(c, st.tr("正在读取…", "Loading…")), t.TextMuted(design.TypeXS)).
			Padding(design.Space1, design.Space2)
		return
	case strings.HasSuffix(path, treeStateEmpty):
		uikit.EmptyState(c, st.tr("目录为空", "The directory is empty"))
		return
	case strings.HasSuffix(path, treeStateError):
		parent := strings.TrimSuffix(path, treeStateError)
		listing := st.directoryListing(parent)
		if listing.err != nil {
			uikit.ErrorLine(c, st.tr("列目录失败：", "Failed to list the directory: ")+listing.err.Error())
		}
		return
	}
	entry := index[path]
	name := textutil.FirstNonEmptyTrimmed(entry.Name, filepath.Base(path))
	// The tree's current item is the row `filesRow` names, painted with the
	// product's persistent selection (BgSelected + a 2 DIP Accent left bar, as
	// the sidebar's tree does) — not only with the native row tint, so the
	// current file reads from across the window (A4-I-11).
	current := st.filesRow >= 0 && st.filesRow < st.filesOutline.Rows() &&
		st.filesOutline.Item(st.filesRow) == path
	row := ui.Row(c).FillWidth().Height(design.RowCompactHeight).AlignItems(ui.Center).
		Radius(design.RadiusSM).Transition(design.TransitionColors(c))
	switch {
	case current:
		row = row.Background(t.BgSelected)
	case row.Hovered():
		row = row.Background(t.BgHover)
	}
	// One leading glyph per row keeps a directory and a file apart at a glance
	// (B-spec §3.5: 图标 16); the outline's own arrow carries the level indent,
	// so the tree does not spend a second column on it.
	glyph := icons.File()
	glyphColor := t.LabelTertiary
	if entry.Dir {
		glyph, glyphColor = icons.Folder(), t.LabelSecondary
	}
	if entry.Dir {
		// A click on the name toggles the disclosure (the outline's arrow does
		// the same); the listing loads lazily on open via the children callback.
		row.ObserveInput(func(ev ui.InputEvent) {
			if ev.Kind != ui.InputPointerUp || ev.Button != 0 {
				return
			}
			// A directory row is also a place: it becomes the current row (the
			// preview then says 目录) and the click toggles its disclosure.
			st.selectTreeRow(path)
			st.toggleTreeDir(path)
		})
		row.Children(func() {
			treeRowStripe(c, t, current)
			ui.Row(c).Grow(1).AlignItems(ui.Center).Gap(design.Space2).
				PaddingX(design.Space1).Children(func() {
				ui.Icon(c, glyph).Size(design.IconSizeS, design.IconSizeS).TextColor(glyphColor)
				design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).TextColor(t.LabelPrimary).
					Grow(1).MaxLines(1).Ellipsis("…")
			})
		})
		return
	}
	row.ObserveInput(func(ev ui.InputEvent) {
		if ev.Kind != ui.InputPointerUp || ev.Button != 0 {
			return
		}
		// A click both opens the file and makes its row the current item, so
		// returning to the tree continues from it with the keyboard (A4-I-11).
		st.selectTreeRow(path)
		st.openFile(path)
		st.invalidate()
	})
	row.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(st.tr("打开", "Open")).Chosen() {
			st.openFile(path)
		}
		if menu.Item(st.tr("复制路径", "Copy path")).Chosen() {
			c.WriteClipboard(path)
		}
	})
	row.Children(func() {
		treeRowStripe(c, t, current)
		ui.Row(c).Grow(1).AlignItems(ui.Center).Gap(design.Space2).
			PaddingX(design.Space1).Children(func() {
			ui.Icon(c, glyph).Size(design.IconSizeS, design.IconSizeS).TextColor(glyphColor)
			design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).TextColor(t.LabelPrimary).
				Grow(1).MaxLines(1).Ellipsis("…")
		})
	})
}

// treeSelectionTheme is the local theme the workspace tree builds with: mygo
// paints a chosen row with Theme.Accent and Theme.AccentText, so lowering the
// accent to the 9% selection tint turns the native chosen fill into the quiet
// persistent selection instead of a saturated block (DESIGN §3.11, the same
// technique the sidebar's session list uses).
func treeSelectionTheme(t *design.Tokens) *ui.Theme {
	theme := *t.Theme()
	theme.Accent = t.BgSelected
	theme.AccentHover = t.BgSelected
	theme.AccentPressed = t.BgSelected
	theme.InactiveAccent = t.BgSelected
	theme.AccentText = t.LabelPrimary
	return &theme
}

// treeRowStripe reserves the 2 DIP selection stripe on every tree row, painted
// only for the current one, so a name never shifts when the cursor moves.
func treeRowStripe(c *ui.Context, t *design.Tokens, current bool) {
	bar := ui.Box(c).Width(design.Space1 / 2).Height(design.RowCompactHeight).Shrink(0)
	if current {
		bar = bar.Background(t.Accent)
	}
}

// treePreviewTitle names the file the tree's preview area is showing.
func (st *state) treeCurrentPath() (string, bool) {
	if st.filesRow < 0 || st.filesRow >= st.filesOutline.Rows() {
		return "", false
	}
	item := st.filesOutline.Item(st.filesRow)
	if strings.Contains(item, "\x00") {
		return "", false
	}
	return item, true
}

// toggleTreeDir opens or closes one directory item and requests a frame.
func (st *state) toggleTreeDir(path string) {
	if st.filesOutline.Open.Has(path) {
		st.filesOutline.Open.Remove(path)
	} else {
		st.filesOutline.Open.Add(path)
	}
	st.invalidate()
}

// activateTreeItem is the outline's Enter/double-click action: a directory
// toggles, a file opens its tab; state pseudo-rows do nothing.
func (st *state) activateTreeItem(index map[string]contracts.FileEntry, path string) {
	if strings.Contains(path, "\x00") {
		return
	}
	if entry, ok := index[path]; ok && entry.Dir {
		st.toggleTreeDir(path)
		return
	}
	st.openFile(path)
	st.invalidate()
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
