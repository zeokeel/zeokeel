package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// rightTab selects the right sidebar's panel. startTab is the zero value,
// so opening the panel for the first time shows DSH's Start page rather than
// dropping the user into Files.
type rightTab int

const (
	startTab rightTab = iota
	filesTab
	terminalTab
	sessionLogTab
)

const (
	// rightSidebarW is the docked panel width at 1600x1000: DSH's captured
	// 720px section (baseline/controls/65-right-sidebar.json). The layout
	// helper shrinks it at narrow widths so the main column keeps room.
	rightSidebarW float32 = 720
	// maxPreviewBytes caps the Files preview: 256 KB, then a truncation note.
	maxPreviewBytes = 256 << 10
	// maxPreviewLines and maxPreviewLineRunes keep a pathological file (one
	// huge line, a minified bundle) from stalling text layout: the preview
	// shows a bounded head and says it did so.
	maxPreviewLines     = 1000
	maxPreviewLineRunes = 400
	// maxTerminalOutput caps retained terminal text so a chatty command
	// cannot grow the window state without bound.
	maxTerminalOutput = 256 << 10
	// terminalTimeout bounds one command. There is no PTY and no persistent
	// shell: each Run starts a fresh process in the workspace root.
	terminalTimeout = 30 * time.Second
)

// rightSidebar draws the live right sidebar: Start, Files, Terminal and the
// session log, with the DSH header controls (New tab, Split, Fullscreen,
// Collapse right sidebar).
func (a *App) rightSidebar(c *ui.Context, t *design.Tokens) {
	a.noteRightScreen()
	panel := ui.Column(c)
	switch {
	case a.rightFullscreen:
		// Fullscreen takes the whole content column; app.go skips the
		// screen behind it while this is on.
		panel = panel.Grow(1)
	case a.rightSplit:
		// The split pane pair in app.go sizes both columns.
		panel = panel.Grow(1)
	default:
		panel = panel.Width(a.rightSidebarWidth(c))
	}
	panel = panel.Background(t.BgBase).BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL2).
		Label(a.tr("Right sidebar", "右侧边栏"))
	panel.Children(func() {
		a.rightSidebarHeader(c, t)
		switch a.rightTab {
		case terminalTab:
			a.terminalPanel(c, t)
		case sessionLogTab:
			a.sessionLogPanel(c, t)
		case filesTab:
			a.filesPanel(c, t)
		default:
			a.startTabPanel(c, t)
		}
	})
}

// rightSidebarHosted reports whether the panel is docked on the current
// background screen. Settings is an overlay, so it keeps the screen it was
// opened from; Home, Session, Plugins and Automation all host the panel.
func (a *App) rightSidebarHosted() bool {
	switch a.backgroundScreen() {
	case Home, Session, Plugins, Automation:
		return true
	}
	return false
}

// rightSidebarMaximized reports the fullscreen right-panel mode, which
// app.go uses to give the panel the whole content column.
func (a *App) rightSidebarMaximized() bool {
	return a.Ctrl != nil && a.rightOpen && a.rightSidebarHosted() && a.rightFullscreen
}

// rightSidebarSplit reports the split-panel mode, which app.go lays out as
// two half panes with a draggable divider.
func (a *App) rightSidebarSplit() bool {
	return a.Ctrl != nil && a.rightOpen && a.rightSidebarHosted() && a.rightSplit
}

// toggleRightSplit turns the half/half pane pair on or off. Split and
// fullscreen are mutually exclusive layouts: enabling split leaves fullscreen
// so the Split control cannot become a no-op that the layout ignores.
func (a *App) toggleRightSplit() {
	a.rightSplit = !a.rightSplit
	if a.rightSplit {
		a.rightFullscreen = false
		if a.rightSplitPct == 0 {
			a.rightSplitPct = 50
		}
	}
}

// toggleRightFullscreen turns the whole-column panel on or off, leaving split
// mode when it is enabled for the same reason.
func (a *App) toggleRightFullscreen() {
	a.rightFullscreen = !a.rightFullscreen
	if a.rightFullscreen {
		a.rightSplit = false
	}
}

// rightSidebarHeader is the tab strip plus DSH's right-panel controls. Every
// control has a real behavior; none is decorative.
func (a *App) rightSidebarHeader(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	if int(a.rightTab) != st.rightTabIndex {
		st.rightTabIndex = int(a.rightTab)
	}
	tabs := ui.TabsBase(c, &st.rightTabIndex, 4)
	tight := a.rightHeaderTight(c)
	header := ui.Row(c).Height(44).
		AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2)
	if tight {
		// At the 900x600 minimum the docked panel is 220px. Two-pixel gaps
		// and 22px secondary controls make New tab, Split, Fullscreen and the
		// 28px collapse control fit without clipping any of them.
		header = header.PaddingX(2).Gap(1)
	} else {
		header = header.PaddingX(design.Space2).Gap(2)
	}
	header.Children(func() {
		a.rightTabButton(c, t, tabs, 0, a.tr("Start", "开始"), startTab, icons.Sparkles())
		a.rightTabButton(c, t, tabs, 1, a.T("files.files"), filesTab, icons.Folder())
		a.rightTabButton(c, t, tabs, 2, a.T("files.terminal"), terminalTab, icons.Terminal())
		// The chip is short so four tabs and the four header controls fit
		// the narrow panel; the panel's own header and the Start card say
		// the full "Session Log".
		a.rightTabButton(c, t, tabs, 3, a.tr("Log", "日志"), sessionLogTab, icons.SessionLog())
		ui.Box(c).Grow(1)
		controlW := chromeIconSize
		if tight {
			// Secondary controls shrink to a 24px target; the collapse
			// control below keeps its full 28x28.
			controlW = 24
		}
		a.rightNewTabButton(c, t)
		chromeHeaderButtonWidth(c, t, a.tr("Split", "分屏"), chromeIconSplit, a.rightSplit, controlW,
			func() { a.toggleRightSplit() })
		label := a.tr("Fullscreen", "全屏")
		if a.rightFullscreen {
			label = a.tr("Exit fullscreen", "退出全屏")
		}
		chromeHeaderButtonWidth(c, t, label, chromeIconFullscreen, a.rightFullscreen, controlW,
			func() { a.toggleRightFullscreen() })
		// Always 28x28: the panel's close control never shrinks into an 8px
		// sliver. Its label is unique because the Home/Session header
		// toggles name themselves "... header", so a test that measures this
		// control by label measures the panel's own target.
		chromeHeaderButtonWidth(c, t, a.tr("Collapse right sidebar", "收起右侧边栏"),
			chromeIconPanelRight, false, chromeIconSize, func() { a.collapseRightSidebar() })
	})
	// The TabsBase pointer is the source of truth; mirror a click or arrow
	// choice into the App field the panel switch reads.
	a.rightTab = rightTab(st.rightTabIndex)
}

// rightHeaderCompact reports whether the header must drop its tab labels:
// when the space the panel actually gets is too narrow for four labeled tabs
// plus the four icon controls, the tabs become icon-only buttons with the
// same accessible names. The controls therefore stay inside the window
// instead of being clipped off its right edge, including in split mode and
// on Plugins/Automation at the 900px minimum.
func (a *App) rightHeaderCompact(c *ui.Context) bool {
	return a.rightSidebarPanelWidth(c) < 560
}

// rightHeaderTight reports the 220px docked panel at the 900x600 minimum,
// where the controls must shrink to 22px boxes (the collapse control stays
// 28x28) so the whole header fits.
func (a *App) rightHeaderTight(c *ui.Context) bool {
	return a.rightSidebarPanelWidth(c) < 320
}

// rightSidebarPanelWidth is the width the panel will actually receive in the
// current layout mode: the docked width, half the content column in split
// mode, or the whole content column in fullscreen.
func (a *App) rightSidebarPanelWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	avail := w - sidebarW
	if avail <= 0 {
		return 0
	}
	switch {
	case a.rightFullscreen:
		return avail
	case a.rightSplit:
		return avail / 2
	default:
		return a.rightSidebarWidth(c)
	}
}

// rightNewTabButton is DSH's "New tab" control: it opens a small picker with
// every tab this build has instead of resetting the current one behind the
// user's back.
func (a *App) rightNewTabButton(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	label := a.tr("New tab", "新建标签页")
	button := chromeHeaderButton(c, t, label, icons.Plus(), st.rightNewTabOpen, func() {
		st.rightNewTabOpen = !st.rightNewTabOpen
	})
	button.Expanded(st.rightNewTabOpen)
	ui.Popover(c, button, &st.rightNewTabOpen, func() { a.rightNewTabMenu(c, t) })
}

// rightNewTabMenu lists the tabs a new tab can open: the Start guide page,
// Files, a shell-backed Terminal and the read-only Session Log.
func (a *App) rightNewTabMenu(c *ui.Context, t *design.Tokens) {
	closeMenu := func() { a.shellUI().rightNewTabOpen = false }
	a.moreMenuItem(c, t, icons.Sparkles(), a.tr("Start page", "开始页"), func() {
		closeMenu()
		a.rightTab = startTab
	})
	a.moreMenuItem(c, t, icons.Folder(), a.T("files.workspace_files"), func() {
		closeMenu()
		a.newRightTab(c)
	})
	a.moreMenuItem(c, t, icons.Terminal(), a.T("files.new_terminal"), func() {
		closeMenu()
		a.rightTab = terminalTab
	})
	a.moreMenuItem(c, t, icons.SessionLog(), a.tr("Session log", "会话日志"), func() {
		closeMenu()
		a.rightTab = sessionLogTab
	})
}

// newRightTab opens a fresh Files tab: the Files panel resets to the
// workspace root with no preview, and the tab switches to Files.
func (a *App) newRightTab(c *ui.Context) {
	a.rightTab = filesTab
	a.resetFiles()
	a.chromeToast(c, "Opened a new Files tab", "已打开新的文件标签")
}

// collapseRightSidebar hides the panel and clears its layout modes, so
// reopening starts on Start at the normal width.
func (a *App) collapseRightSidebar() {
	a.rightOpen = false
	a.rightFullscreen = false
	a.rightSplit = false
	a.rightTab = startTab
	a.shellUI().rightNewTabOpen = false
	a.startUI().shellMenuOpen = false
}

// rightTabButton is one panel tab, with the selected one tinted.
func (a *App) rightTabButton(c *ui.Context, t *design.Tokens, tabs ui.TabsParts, index int, label string, tab rightTab, ic *ui.SVG) {
	compact := a.rightHeaderCompact(c)
	tight := a.rightHeaderTight(c)
	active := a.rightTab == tab
	pad := float32(6)
	if tight {
		pad = 3
	}
	button := tabs.Tab(index).Height(28).PaddingX(pad).Gap(4).Radius(design.RadiusSM).
		Shrink(0).AlignItems(ui.Center).Label(label).Tooltip(label)
	if active {
		button = button.Background(t.BgHover)
	} else if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		color := t.LabelSecondary
		if active {
			color = t.LabelPrimary
		}
		if ic != nil {
			ui.Icon(c, ic).FontSize(14).TextColor(color)
		}
		if !compact {
			ui.Text(c, label).FontSize(12).FixedLineHeight(18).FontWeight(500).
				TextColor(color)
		}
	})
}

// filesPanel shows either the current directory listing or a file preview.
func (a *App) filesPanel(c *ui.Context, t *design.Tokens) {
	root := a.workspaceRoot()
	if root == "" {
		a.rightEmpty(c, t, a.T("files.no_workspace_root_yet_create_or_select_a_workspace"))
		return
	}
	if err := statWorkspaceRoot(root); err != nil {
		a.rightEmpty(c, t, fmt.Sprintf(
			a.T("files.workspace_root_is_not_readable_v"), err))
		return
	}
	if a.filePath != "" {
		a.filePreview(c, t, root)
		return
	}
	a.filesBreadcrumb(c, t, root, "")
	a.filesList(c, t, root)
}

// filesBreadcrumb shows the path from the workspace root to the current
// directory (and file, in preview). Each directory segment is clickable,
// and the trailing Reload control re-reads the listing or the preview.
//
// The docked panel can be as narrow as 220px. A two-level path used to push
// Reload past the window edge (W=0) and clip the last segment to W=0. The row
// now measures the available width, collapses every leading segment into one
// ellipsis control with a menu, and keeps Reload pinned at the right edge.
func (a *App) filesBreadcrumb(c *ui.Context, t *design.Tokens, root, fileLabel string) {
	segments := splitRelPath(a.filesDir)
	panelW := a.rightSidebarPanelWidth(c)
	// The row's own 12px side padding and the pinned 22px Reload control.
	inner := panelW - 2*design.Space3
	available := inner - 24 - 2

	measure := func(label string) float32 {
		w, _ := c.MeasureText(0, ui.Span{Text: label, Size: 12})
		return w + 2*design.Space1 // the segment's own 4px side padding
	}
	sepWidth := func() float32 {
		w, _ := c.MeasureText(0, ui.Span{Text: "/", Size: 11})
		return w + 2
	}
	const gap float32 = 2
	total := func(labels []string, hidden bool) float32 {
		n := float32(1) // root
		sum := measure("⌂ " + a.rootLabel(root))
		if hidden {
			n += 2 // ellipsis and its separator
			sum += measure("…") + 4 + sepWidth()
		}
		for _, label := range labels {
			n += 1
			sum += sepWidth() + measure(label)
		}
		if fileLabel != "" {
			n += 1
			sum += sepWidth() + measure(fileLabel)
		}
		return sum + (n-1)*gap
	}

	rootFull := "⌂ " + a.rootLabel(root)
	// A single leading directory is the path itself, not something to fold
	// into an ellipsis: clamp it instead. Folding needs at least one hidden
	// segment between the root and the last one.
	showHidden := len(segments) > 1 && total(segments, false) > available
	var visible []string
	var hidden []string
	if showHidden {
		visible = segments[len(segments)-1:]
		hidden = segments[:len(segments)-1]
	} else {
		visible = segments
	}
	rootText := rootFull
	if showHidden && total(visible, true) > available {
		// The root name is the longest optional label; keep its glyph only.
		rootText = "⌂"
	}
	remaining := total(visible, showHidden) - (measure(rootFull) - measure(rootText))
	// Clamp the last segment when even the collapsed row cannot fit it.
	lastMax := float32(0)
	if over := remaining - available; over > 0 && len(visible) > 0 {
		lastMax = measure(visible[len(visible)-1]) - over - 2*design.Space1
		if lastMax < 36 {
			lastMax = 36
		}
	}
	// A long file name in preview is clamped the same way, so it cannot push
	// the pinned Reload control off the panel.
	fileMax := float32(0)
	if fileLabel != "" {
		if over := remaining - available; over > 0 {
			fileMax = measure(fileLabel) - over - 2*design.Space1
			if fileMax < 36 {
				fileMax = 36
			}
		}
	}

	ui.Row(c).Height(34).PaddingX(design.Space3).Gap(2).AlignItems(ui.Center).
		Children(func() {
			// The home glyph keeps the root segment distinct from the
			// sidebar's workspace row that shows the same name.
			a.breadcrumbSegment(c, t, rootText, true, 0, func() {
				a.filesDir = ""
				a.filePath = ""
				a.fileErr = ""
			})
			if showHidden {
				ui.Text(c, "/").FontSize(11).TextColor(t.LabelTertiary)
				a.hiddenBreadcrumbSegments(c, t, segments, hidden)
			}
			for i, segment := range visible {
				ui.Text(c, "/").FontSize(11).TextColor(t.LabelTertiary)
				next := strings.Join(visible[:i+1], "/")
				if showHidden {
					next = strings.Join(segments, "/")
				}
				max := float32(0)
				if i == len(visible)-1 {
					max = lastMax
				}
				a.breadcrumbSegment(c, t, segment, false, max, func() {
					a.filesDir = next
					a.filePath = ""
					a.fileErr = ""
				})
			}
			if fileLabel != "" {
				ui.Text(c, "/").FontSize(11).TextColor(t.LabelTertiary)
				file := ui.Text(c, fileLabel).FontSize(12).FixedLineHeight(18).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				if fileMax > 0 {
					file.MaxWidth(fileMax)
				}
			}
			ui.Box(c).Grow(1)
			reload := ui.Row(c).Size(24, 24).Shrink(0).Center().Radius(design.RadiusXS).
				Label(a.tr("Reload", "重新加载")).Role(ui.RoleButton).Focusable()
			if reload.Hovered() {
				reload = reload.Background(t.BgHover)
			}
			reload.Children(func() {
				ui.Icon(c, icons.Reload()).FontSize(14).TextColor(t.LabelSecondary)
			})
			reload.OnClick(func() { a.reloadFiles(c) })
		})
}

// hiddenBreadcrumbSegments is the ellipsis control that replaces the leading
// path segments at narrow panel widths, plus the menu that makes each hidden
// directory reachable in one click.
func (a *App) hiddenBreadcrumbSegments(c *ui.Context, t *design.Tokens, all, hidden []string) {
	st := a.liveMenus()
	ell := ui.Row(c).Height(24).PaddingX(design.Space1).Radius(design.RadiusXS).
		AlignItems(ui.Center).Label(a.tr("Hidden path segments", "隐藏的路径段")).
		Role(ui.RoleButton).Focusable()
	if ell.Hovered() {
		ell = ell.Background(t.BgHover)
	}
	ell.Children(func() {
		ui.Text(c, "…").FontSize(12).FixedLineHeight(18).
			TextColor(t.LabelSecondary).MaxLines(1)
	})
	ell.Expanded(st.breadcrumbOpen)
	ell.OnClick(func() { st.breadcrumbOpen = !st.breadcrumbOpen })
	ui.Popover(c, ell, &st.breadcrumbOpen, func() {
		ui.Column(c).Width(200).Gap(2).Children(func() {
			for i, segment := range hidden {
				prefix := strings.Join(all[:i+1], "/")
				row := ui.Row(c).Height(28).PaddingX(design.Space2).Gap(design.Space2).
					Radius(design.RadiusXS).AlignItems(ui.Center).
					Label(segment).Role(ui.RoleButton).Focusable()
				if row.Hovered() {
					row = row.Background(t.BgHover)
				}
				row.Children(func() {
					ui.Text(c, segment).FontSize(12).FixedLineHeight(18).
						TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				})
				row.OnClick(func() {
					a.filesDir = prefix
					a.filePath = ""
					a.fileErr = ""
					st.breadcrumbOpen = false
				})
			}
		})
	})
}

// breadcrumbSegment is one clickable breadcrumb label. maxWidth > 0 bounds
// the label so the last visible directory ellipsizes instead of pushing the
// pinned Reload control off the panel.
func (a *App) breadcrumbSegment(c *ui.Context, t *design.Tokens, label string, root bool, maxWidth float32, jump func()) {
	item := ui.Row(c).Height(24).PaddingX(design.Space1).Radius(design.RadiusXS).
		AlignItems(ui.Center).Label(label).Role(ui.RoleButton).Focusable()
	if maxWidth > 0 {
		item = item.MaxWidth(maxWidth)
	}
	if item.Hovered() {
		item = item.Background(t.BgHover)
	}
	item.Children(func() {
		color := t.LabelSecondary
		if root {
			color = t.LabelPrimary
		}
		text := ui.Text(c, label).FontSize(12).FixedLineHeight(18).TextColor(color).
			MaxLines(1).Ellipsis("…")
		if maxWidth > 0 {
			text.MaxWidth(maxWidth - 2*design.Space1)
		}
	})
	item.OnClick(jump)
}

// rootLabel names the workspace root in the breadcrumb: the workspace name
// when one is available, else the directory's base name.
func (a *App) rootLabel(root string) string {
	if a.Ctrl != nil {
		if ws, ok, err := a.Ctrl.CurrentWorkspace(context.Background()); err == nil && ok {
			if name := strings.TrimSpace(ws.Name); name != "" {
				return name
			}
		}
	}
	return filepath.Base(root)
}

// filesList lists the current directory, directories first.
func (a *App) filesList(c *ui.Context, t *design.Tokens, root string) {
	dir := root
	if a.filesDir != "" {
		dir = filepath.Join(root, filepath.FromSlash(a.filesDir))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		a.rightEmpty(c, t, fmt.Sprintf(a.T("files.could_not_read_the_directory_v"), err))
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	if len(entries) == 0 {
		a.rightEmpty(c, t, a.T("files.this_directory_is_empty"))
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(design.Space1, design.Space2, design.Space2, design.Space2).
			Gap(1).Children(func() {
			for _, entry := range entries {
				a.fileRow(c, t, entry)
			}
		})
	})
}

// fileRow is one directory or file in the listing.
func (a *App) fileRow(c *ui.Context, t *design.Tokens, entry os.DirEntry) {
	name := entry.Name()
	isDir := entry.IsDir()
	row := ui.Row(c).Height(28).PaddingX(design.Space2).Gap(design.Space2).
		Radius(design.RadiusXS).AlignItems(ui.Center).Label(name).
		Role(ui.RoleButton).Focusable()
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		glyph := "▤"
		if isDir {
			glyph = "▸"
		}
		ui.Text(c, glyph).FontSize(11).TextColor(t.LabelTertiary).Width(12)
		label := name
		if isDir {
			label += "/"
		}
		ui.Text(c, label).FontSize(12).FixedLineHeight(18).TextColor(t.LabelPrimary).
			Grow(1).MaxLines(1).Ellipsis("…")
	})
	if isDir {
		next := name
		if a.filesDir != "" {
			next = a.filesDir + "/" + name
		}
		row.OnClick(func() {
			a.filesDir = next
			a.filePath = ""
			a.fileErr = ""
		})
		return
	}
	rel := name
	if a.filesDir != "" {
		rel = a.filesDir + "/" + name
	}
	row.OnClick(func() { a.openFilePreview(rel) })
}

// filePreview reads a file up to 256 KB and shows it as plain text.
func (a *App) filePreview(c *ui.Context, t *design.Tokens, root string) {
	name := pathBase(a.filePath)
	a.filesBreadcrumb(c, t, root, name)
	if a.fileErr != "" {
		a.rightEmpty(c, t, a.fileErr)
		return
	}
	ui.Row(c).Height(26).PaddingX(design.Space3).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf(a.T("files.plain_text_preview_d_bytes"),
			len(a.fileText))).FontSize(11).TextColor(t.LabelTertiary).Grow(1)
		if ui.Button(c, a.T("files.close_preview")).Clicked() {
			a.filePath = ""
			a.fileText = ""
			a.fileTrunc = false
			a.fileLimited = false
			a.fileErr = ""
		}
	})
	ui.ScrollBoth(c).Grow(1).Children(func() {
		ui.Column(c).Padding(design.Space2, design.Space3, design.Space3, design.Space3).Children(func() {
			ui.Text(c, a.fileText).FontSize(12).FixedLineHeight(18).TextColor(t.LabelPrimary)
		})
	})
	if a.fileLimited {
		ui.Text(c, fmt.Sprintf(
			a.T("files.preview_limited_to_the_first_d_lines_d_characters_per_line"),
			maxPreviewLines, maxPreviewLineRunes)).
			FontSize(11).FixedLineHeight(16).TextColor(t.WarningText).
			Padding(0, design.Space3, 0, design.Space3)
	}
	if a.fileTrunc {
		ui.Text(c, a.T("files.preview_truncated_at_256_kb")).
			FontSize(11).FixedLineHeight(16).TextColor(t.WarningText).
			Padding(0, design.Space3, design.Space2, design.Space3)
	}
}

// openFilePreview loads rel under the workspace root. Paths are resolved
// through filepath.Clean and rejected when they escape the root.
func (a *App) openFilePreview(rel string) {
	root := a.workspaceRoot()
	a.filePath = rel
	a.fileText = ""
	a.fileErr = ""
	a.fileTrunc = false
	a.fileLimited = false

	abs, err := safeJoin(root, rel)
	if err != nil {
		a.fileErr = a.T("files.that_path_is_outside_the_workspace")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		a.fileErr = fmt.Sprintf(a.T("files.could_not_open_the_file_v"), err)
		return
	}
	defer f.Close() //nolint:errcheck // read-only preview

	data, err := io.ReadAll(io.LimitReader(f, maxPreviewBytes+1))
	if err != nil {
		a.fileErr = fmt.Sprintf(a.T("files.could_not_read_the_file_v"), err)
		return
	}
	if len(data) > maxPreviewBytes {
		data = data[:maxPreviewBytes]
		a.fileTrunc = true
	}
	if bytes.IndexByte(data, 0) >= 0 {
		a.fileText = a.T("files.binary_file_preview_is_not_available")
		return
	}

	// Bound the text layout: a single multi-megabyte line would otherwise
	// spend seconds inside the text shaper instead of drawing a preview.
	text := string(data)
	lines := strings.Split(text, "\n")
	limited := false
	if len(lines) > maxPreviewLines {
		lines = lines[:maxPreviewLines]
		limited = true
	}
	for i, line := range lines {
		runes := []rune(line)
		if len(runes) > maxPreviewLineRunes {
			lines[i] = string(runes[:maxPreviewLineRunes]) + " …"
			limited = true
		}
	}
	a.fileText = strings.Join(lines, "\n")
	a.fileLimited = limited
}

// terminalPanel is the command runner: output, an input line, a shell
// selector, Run and Clear. Each Run starts a fresh process in the selected
// shell; there is no PTY and no persistent shell.
func (a *App) terminalPanel(c *ui.Context, t *design.Tokens) {
	root := a.workspaceRoot()
	dir := root
	if dir == "" {
		dir = "."
	}
	st := a.startUI()
	ui.Row(c).Height(34).PaddingX(design.Space2).AlignItems(ui.Center).Gap(design.Space2).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Text(c, dir).FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary).
			Grow(1).MinWidth(0).MaxLines(1).Ellipsis("…")
		shell := ui.Row(c).Height(24).PaddingX(design.Space2).Gap(design.Space1).
			Radius(design.RadiusSM).AlignItems(ui.Center).MaxWidth(120).
			Label(a.tr("Choose shell", "选择 shell")).
			Role(ui.RoleMenuButton).Focusable()
		if shell.Hovered() {
			shell = shell.Background(t.BgHover)
		}
		shell.Children(func() {
			ui.Icon(c, icons.Terminal()).FontSize(13).TextColor(t.LabelSecondary)
			ui.Text(c, a.terminalShellLabel()).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
			ui.Icon(c, icons.ChevronDown()).FontSize(12).TextColor(t.LabelTertiary)
		})
		shell.Expanded(st.shellMenuOpen)
		shell.OnClick(func() { st.shellMenuOpen = !st.shellMenuOpen })
		ui.Popover(c, shell, &st.shellMenuOpen, func() { a.shellPickerMenu(c, t) })
		if ui.Button(c, a.T("files.clear")).Clicked() {
			a.clearTerminal()
		}
	})

	out, _ := a.terminalState()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(design.Space2, design.Space3, design.Space2, design.Space3).Children(func() {
			if out == "" {
				ui.Text(c, a.T("files.run_a_command_in_the_workspace_root")).
					FontSize(12).TextColor(t.LabelTertiary)
				return
			}
			design.Apply(ui.Text(c, out),
				func(e ui.Element) ui.Element { return e.Font(design.MonoFontStack()) }).
				FontSize(12).FixedLineHeight(18).TextColor(t.LabelPrimary)
		})
	})

	_, running := a.terminalState()
	ui.Column(c).Padding(design.Space2, design.Space3, design.Space3, design.Space3).
		Gap(design.Space2).BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &a.termInput).Label(a.T("files.command")).
				AutoFocus().OnSubmit(func() { a.runTerminal(c) }).Grow(1)
			if running {
				if ui.Button(c, a.tr("Stop", "停止")).Clicked() {
					a.stopTerminal()
				}
			} else if ui.Button(c, a.T("files.run")).Clicked() {
				a.runTerminal(c)
			}
		})
		ui.Text(c, a.T("files.simple_command_runner_no_pty_no_persistent_shell")).
			FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary)
	})
}

// terminalState returns the retained output and whether a command is running.
func (a *App) terminalState() (string, bool) {
	a.termMu.Lock()
	defer a.termMu.Unlock()
	return a.termOutput, a.termRunning
}

// clearTerminal empties the retained output; a running command may still
// append its own result when it finishes.
func (a *App) clearTerminal() {
	a.termMu.Lock()
	a.termOutput = ""
	a.termMu.Unlock()
}

// runTerminal starts the input in the workspace root on a background
// goroutine. The frame returns immediately: the command's output is appended
// when it completes and the view is invalidated. While it runs the panel
// swaps Run for Stop.
func (a *App) runTerminal(c *ui.Context) {
	command := strings.TrimSpace(a.termInput)
	if command == "" {
		return
	}
	dir := a.workspaceRoot()
	if dir == "" {
		dir = "."
	}
	shellName := a.terminalShellName()

	a.termMu.Lock()
	if a.termRunning {
		a.termMu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalTimeout)
	a.termRunning = true
	a.termCancel = cancel
	a.termInput = ""
	a.termOutput = appendTerminal(a.termOutput, "$ "+command+"\n")
	a.termMu.Unlock()
	if c != nil {
		c.Invalidate()
	}

	go func() {
		out, err := runShellCommandFor(ctx, dir, shellName, command)
		cancel()
		a.termMu.Lock()
		a.termOutput = appendTerminal(a.termOutput, string(out))
		if err != nil {
			a.termOutput = appendTerminal(a.termOutput, err.Error()+"\n")
		}
		a.termOutput = appendTerminal(a.termOutput, "\n")
		a.termRunning = false
		a.termCancel = nil
		a.termMu.Unlock()
		if c != nil {
			c.Invalidate()
		}
	}()
}

// stopTerminal cancels the running command; View never blocks on it.
func (a *App) stopTerminal() {
	a.termMu.Lock()
	cancel := a.termCancel
	a.termMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// appendTerminal appends text, keeping the retained output under the cap.
func appendTerminal(current, text string) string {
	current += text
	if len(current) > maxTerminalOutput {
		current = current[len(current)-maxTerminalOutput:]
	}
	return current
}

// rightEmpty is a centered placeholder in the right sidebar.
func (a *App) rightEmpty(c *ui.Context, t *design.Tokens, message string) {
	ui.Column(c).Grow(1).Padding(design.Space6).Justify(ui.Center).AlignItems(ui.Center).
		Children(func() {
			ui.Text(c, message).FontSize(12).TextColor(t.LabelTertiary)
		})
}

// resetFiles returns the Files tab to the workspace root with no preview.
func (a *App) resetFiles() {
	a.filesDir = ""
	a.filePath = ""
	a.fileText = ""
	a.fileTrunc = false
	a.fileLimited = false
	a.fileErr = ""
}

// splitRelPath splits a slash-separated relative path into non-empty parts.
func splitRelPath(rel string) []string {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" || rel == "." {
		return nil
	}
	parts := strings.Split(rel, "/")
	out := parts[:0]
	for _, part := range parts {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}
	return out
}

// pathBase is filepath.Base for a slash-separated relative path.
func pathBase(rel string) string {
	return filepath.Base(filepath.FromSlash(rel))
}

// safeJoin joins rel under root and rejects paths that escape it.
func safeJoin(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	back, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return "", err
	}
	if back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes %q", rel, root)
	}
	return abs, nil
}
