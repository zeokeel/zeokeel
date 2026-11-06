package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/icons"
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
	// rightSidebarW is the docked inspector's brand panel width on a wide
	// window: 560 DIPs (140 spacing units at the brand's 4 DIP grid). The
	// layout helper caps it at rightPanelShare of the content column and
	// frees room for the screen behind it at narrow widths; the resize grip
	// lets the user drag it wider or narrower inside the same bounds.
	rightSidebarW float32 = 560
	// rightPanelGripW is the draggable resize edge at the panel's leading
	// side: one spacing unit (4 DIPs).
	rightPanelGripW float32 = 4
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

// maxFilesEntries caps one directory listing: a frame builds one row per
// entry, so a huge level would cost the whole frame. The extra entries are
// counted and the panel says how many are hidden. A variable so the truncation
// test can shrink it.
var maxFilesEntries = 2000

// filesDirCacheTTL is how long a directory listing is reused before the panel
// re-reads it; Reload invalidates it immediately. A variable so tests can pin
// the cache without sleeping.
var filesDirCacheTTL = time.Second

// rightPanelState is the per-App state of the docked right panel. App lives in
// app.go, which this change does not own, so the state is keyed by the App like
// startTabState. Only the view goroutine reads it.
type rightPanelState struct {
	// dockWidth is the width the user dragged the panel to, 0 for the brand
	// default. It is in-memory only, like the collapsed left sidebar.
	dockWidth float32
	// filesSessionID is the session the Files tab's navigation belongs to:
	// filesDir, filePath and the preview are reset when it changes
	// (files_root.go ensureFilesSession). filesSessionSet distinguishes the
	// first frame, which only records the session, from a real switch.
	filesSessionID  string
	filesSessionSet bool
	// nativeErr is the transient failure of the Files tab's native open/reveal
	// actions, shown under the preview's metadata row until the next success.
	nativeErr string
	// dirCache is one directory's listing, reused for filesDirCacheTTL or
	// until Reload, a directory switch or a session switch invalidates it. The
	// entries are already sorted (directories first, natural name order) and
	// capped at maxFilesEntries; total is the uncapped count.
	dirCachePath    string
	dirCacheEntries []os.DirEntry
	dirCacheTotal   int
	dirCacheErr     error
	dirCacheAt      time.Time
	// previewKind, imageSrc and imageErr describe the file preview: the body
	// the Files panel draws and, for an image, the decoded source or the
	// user-facing reason it cannot be shown.
	previewKind filesPreviewKind
	imageSrc    ui.ImageSource
	imageErr    string
}

var rightPanelStates sync.Map // map[*App]*rightPanelState

// rightPanelUI returns the right panel's state of a, making it on first use.
func (a *App) rightPanelUI() *rightPanelState {
	if s, ok := rightPanelStates.Load(a); ok {
		return s.(*rightPanelState)
	}
	actual, _ := rightPanelStates.LoadOrStore(a, &rightPanelState{})
	return actual.(*rightPanelState)
}

// rightPanelFileIcon is the Files list's document glyph. The icon package has
// no plain file glyph yet, so the panel carries its own on the same 16 DIP
// grid as the other line icons.
var rightPanelFileIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 1.8h5.2L12.2 4.8v9.4H4z"/><path d="M9 1.8v3.2h3.2"/></svg>`))

// rightPanelExternalIcon is the "open in system app" affordance: a box with an
// arrow leaving it, on the same 16 DIP grid as the other line icons. The icon
// package has no external-link glyph yet, so the panel carries its own.
var rightPanelExternalIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><path d="M9.5 2.5h4v4"/><path d="M13.5 2.5 8 8"/><path d="M12 9.8v3.7H2.5V3h3.7"/></svg>`))

// rightSidebarResizeGrip is the docked panel's draggable leading edge: a
// pointer drag resizes the panel, and the arrow keys resize it from the
// keyboard while it has the focus (a desktop panel is resizable without a
// pointer too). Split mode keeps app.go's draggable divider instead, and
// fullscreen has nothing to resize.
func (a *App) rightSidebarResizeGrip(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	label := a.tr("Resize right sidebar", "调整右侧边栏宽度")
	grip := ui.Column(c).Width(rightPanelGripW).FillHeight().Shrink(0).
		Cursor(ui.CursorResizeEW).Label(label).Role(ui.RoleSplitter).Focusable()
	// The edge is visible while hovered, focused or dragged, so the panel's
	// resize affordance is discoverable without a permanent bar.
	if _, _, dragging := grip.Dragged(); dragging || grip.Hovered() || grip.Focused() {
		grip = grip.Background(t.BorderL3)
	}
	grip.Children(func() {
		// Narrower content so the pointer target is the grip, not a child.
		_ = ui.Box(c).Width(rightPanelGripW).FillHeight()
	})
	if dx, _, ok := grip.Dragged(); ok {
		a.setRightDockWidth(a.rightSidebarWidth(c) - dx)
	}
	step := th.Space(4)
	if grip.Shortcut(0, ui.KeyLeft) {
		a.setRightDockWidth(a.rightSidebarWidth(c) + step)
	}
	if grip.Shortcut(0, ui.KeyRight) {
		a.setRightDockWidth(a.rightSidebarWidth(c) - step)
	}
}

// setRightDockWidth stores a dragged width. The layout helper clamps it to
// the current window on every frame, so growing the window later restores the
// panel to a usable share instead of leaving it stuck at a stale size.
func (a *App) setRightDockWidth(width float32) {
	a.rightPanelUI().dockWidth = width
}

// rightSidebar draws the live right sidebar: Start, Files, Terminal and the
// session log. The panel is a branded inspector column: a resize grip, a tab
// strip of native buttons, and the selected panel. The header controls are
// mygo ButtonBases (focus, Enter/Space, tooltips), and Split/Fullscreen keep
// their draggable/maximized layouts.
func (a *App) rightSidebar(c *ui.Context, t *design.Tokens) {
	a.noteRightScreen()
	// The docked panel sizes itself; split half panes and fullscreen are sized
	// by app.go instead. The panel's leading edge carries the resize grip only
	// when it is docked, because split mode already drags its divider.
	docked := !a.rightSplit && !a.rightFullscreen
	panel := ui.Row(c).AlignItems(ui.Stretch)
	if docked {
		panel = panel.Width(a.rightSidebarWidth(c))
	} else {
		panel = panel.Grow(1)
	}
	panel = panel.Background(t.BgSidebar).BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL2).
		Label(a.tr("Right sidebar", "右侧边栏"))
	panel.Children(func() {
		if docked {
			a.rightSidebarResizeGrip(c, t)
		}
		ui.Column(c).Grow(1).Children(func() {
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

// rightSidebarHeader is the brand tab strip plus the panel controls: New tab,
// Split, Fullscreen and Collapse right sidebar. Every control has a real
// behavior; each is a native ButtonBase, so keyboard activation (Enter/Space),
// the focus ring, the tooltip and the pressed state come from mygo instead of
// a hand-drawn DSH row.
func (a *App) rightSidebarHeader(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	st := a.shellUI()
	if int(a.rightTab) != st.rightTabIndex {
		st.rightTabIndex = int(a.rightTab)
	}
	tabs := ui.TabsBase(c, &st.rightTabIndex, 4)
	tight := a.rightHeaderTight(c)
	header := ui.Row(c).Height(th.Space(11)).
		AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).
		PaddingX(th.Space(2))
	if tight {
		header = header.Gap(th.Space(0.5))
	} else {
		header = header.Gap(th.Space(1))
	}
	header.Children(func() {
		a.rightTabButton(c, t, tabs, 0, a.tr("Start", "开始"), startTab, icons.Sparkles())
		a.rightTabButton(c, t, tabs, 1, a.T("files.files"), filesTab, icons.Folder())
		a.rightTabButton(c, t, tabs, 2, a.T("files.terminal"), terminalTab, icons.Terminal())
		// The chip is short so four tabs and the four header controls fit the
		// narrow panel; the panel's own Start card says the full "Session Log".
		a.rightTabButton(c, t, tabs, 3, a.tr("Log", "日志"), sessionLogTab, icons.SessionLog())
		ui.Spacer(c)
		controlW := th.Space(7)
		if tight {
			controlW = th.Space(6)
		}
		a.rightNewTabButton(c, t)
		a.rightHeaderButtonWidth(c, t, a.tr("Split", "分屏"), chromeIconSplit, a.rightSplit,
			controlW, func() { a.toggleRightSplit() })
		label := a.tr("Fullscreen", "全屏")
		if a.rightFullscreen {
			label = a.tr("Exit fullscreen", "退出全屏")
		}
		a.rightHeaderButtonWidth(c, t, label, chromeIconFullscreen, a.rightFullscreen,
			controlW, func() { a.toggleRightFullscreen() })
		// Always the theme's control size: the panel's close control never
		// shrinks into a sliver.
		a.rightHeaderButtonWidth(c, t, a.tr("Collapse right sidebar", "收起右侧边栏"),
			chromeIconPanelRight, false, th.Space(7), func() { a.collapseRightSidebar() })
	})
	// The TabsBase pointer is the source of truth; mirror a click or arrow
	// choice into the App field the panel switch reads.
	a.rightTab = rightTab(st.rightTabIndex)
}

// rightHeaderButtonWidth is one panel header control: a native ButtonBase at
// the theme's control size, tinted while its mode is on, with an accessible
// name and a tooltip.
func (a *App) rightHeaderButtonWidth(c *ui.Context, t *design.Tokens, label string, ic *ui.SVG, active bool, width float32, onClick func()) ui.Element {
	th := t.Theme()
	button := ui.ButtonBase(c).Size(width, th.Space(7)).Center().
		Radius(design.RadiusSM).Label(label).Tooltip(label)
	if active || button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		color := t.LabelSecondary
		if active {
			color = t.Accent
		}
		ui.Icon(c, ic).Size(th.Space(4), th.Space(4)).TextColor(color)
	})
	button.OnClick(onClick)
	return button
}

// rightHeaderCompact reports whether the header must drop the tab labels:
// when the space the panel actually gets is too narrow for four labeled tabs
// plus the four icon controls, the tabs become icon-only controls with the
// same accessible names. The controls stay inside the window instead of being
// clipped off its right edge, including in split mode and on
// Plugins/Automation at the 900px minimum.
func (a *App) rightHeaderCompact(c *ui.Context) bool {
	return a.rightSidebarPanelWidth(c) < c.Theme().Space(130) // 520 DIPs
}

// rightHeaderTight reports the 220 DIP docked panel at the 900x600 minimum,
// where the controls shrink to the theme's Space(6) boxes so the whole header
// still fits.
func (a *App) rightHeaderTight(c *ui.Context) bool {
	return a.rightSidebarPanelWidth(c) < c.Theme().Space(80) // 320 DIPs
}

// rightSidebarPanelWidth is the width the panel will actually receive in the
// current layout mode: the docked width (with the resize grip), half the
// content column in split mode, or the whole content column in fullscreen.
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

// rightNewTabButton is the panel's "New tab" control: a native ButtonBase that
// opens a small picker with every tab this build has instead of resetting the
// current one behind the user's back.
func (a *App) rightNewTabButton(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	st := a.shellUI()
	width := th.Space(7)
	if a.rightHeaderTight(c) {
		width = th.Space(6)
	}
	button := a.rightHeaderButtonWidth(c, t, a.tr("New tab", "新建标签页"), icons.Plus(),
		st.rightNewTabOpen, width, func() { st.rightNewTabOpen = !st.rightNewTabOpen })
	button.Expanded(st.rightNewTabOpen)
	ui.Popover(c, button, &st.rightNewTabOpen, func() { a.rightNewTabMenu(c, t) })
}

// rightNewTabMenu lists the tabs a new tab can open: the Start guide page,
// Files, a shell-backed Terminal and the read-only Session Log. The rows and
// the panel come from the shared popover helpers, clamped to the right panel.
func (a *App) rightNewTabMenu(c *ui.Context, t *design.Tokens) {
	closeMenu := func() { a.shellUI().rightNewTabOpen = false }
	a.popoverMenuPanelIn(c, t, a.rightSidebarPanelWidth(c), func() {
		a.popoverMenuActionRow(c, t, a.tr("Start page", "开始页"), func() {
			ui.Image(c, icons.Sparkles()).Size(16, 16).TextColor(t.LabelPrimary)
		}, func() {
			closeMenu()
			a.rightTab = startTab
		})
		a.popoverMenuActionRow(c, t, a.T("files.workspace_files"), func() {
			ui.Image(c, icons.Folder()).Size(16, 16).TextColor(t.LabelPrimary)
		}, func() {
			closeMenu()
			a.newRightTab(c)
		})
		a.popoverMenuActionRow(c, t, a.T("files.new_terminal"), func() {
			ui.Image(c, icons.Terminal()).Size(16, 16).TextColor(t.LabelPrimary)
		}, func() {
			closeMenu()
			a.rightTab = terminalTab
		})
		a.popoverMenuActionRow(c, t, a.tr("Session log", "会话日志"), func() {
			ui.Image(c, icons.SessionLog()).Size(16, 16).TextColor(t.LabelPrimary)
		}, func() {
			closeMenu()
			a.rightTab = sessionLogTab
		})
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

// rightTabButton is one brand panel tab: a native TabsBase tab, so the tab
// roles, arrow keys and selection semantics stay mygo's, tinted while chosen.
// On a panel dragged narrow the labels drop and the icons stay, each tab
// keeping its accessible name.
func (a *App) rightTabButton(c *ui.Context, t *design.Tokens, tabs ui.TabsParts, index int, label string, tab rightTab, ic *ui.SVG) {
	th := t.Theme()
	compact := a.rightHeaderCompact(c)
	tight := a.rightHeaderTight(c)
	active := a.rightTab == tab
	pad := th.Space(1)
	if tight {
		pad = th.Space(0.5)
	}
	button := tabs.Tab(index).Height(th.Space(7)).PaddingX(pad).Gap(th.Space(1)).
		Radius(design.RadiusSM).Shrink(0).AlignItems(ui.Center).
		Label(label).Tooltip(label)
	if active || button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		color := t.LabelSecondary
		if active {
			color = t.LabelPrimary
		}
		if ic != nil {
			ui.Icon(c, ic).Size(th.Space(3.5), th.Space(3.5)).TextColor(color)
		}
		if !compact {
			ui.Text(c, label).FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4)).
				FontWeight(500).TextColor(color)
		}
	})
}

// filesPanel shows either the current directory listing or a file preview.
func (a *App) filesPanel(c *ui.Context, t *design.Tokens) {
	a.ensureFilesSession()
	root := a.filesRoot()
	if root == "" {
		a.rightEmpty(c, t, icons.Folder(),
			a.T("files.no_workspace_root_yet_create_or_select_a_workspace"))
		return
	}
	if err := statWorkspaceRoot(root); err != nil {
		a.rightEmpty(c, t, icons.Folder(), fmt.Sprintf(
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
// directory (and file, in preview), as native ButtonBases: each directory
// segment is clickable and the trailing Reload control re-reads the listing or
// the preview. All of it stays on the theme's spacing grid.
//
// The docked panel can be as narrow as the brand minimum (220 DIPs). A
// two-level path used to push Reload past the window edge and clip the last
// segment to W=0. The row measures the width it gets, collapses every leading
// segment into one ellipsis control with a menu, and keeps Reload pinned at
// the right edge.
func (a *App) filesBreadcrumb(c *ui.Context, t *design.Tokens, root, fileLabel string) {
	th := t.Theme()
	segments := splitRelPath(a.filesDir)
	panelW := a.rightSidebarPanelWidth(c)
	reloadW := th.Space(6)
	openW := th.Space(6)
	gap := th.Space(0.5)
	// The row's own side padding, the resize grip and the pinned
	// open-in-system and Reload controls.
	inner := panelW - rightPanelGripW - 2*th.Space(3)
	available := inner - reloadW - openW - 2*gap

	measure := func(label string) float32 {
		w, _ := c.MeasureText(0, ui.Span{Text: label, Size: 12})
		return w + 2*th.Space(1) // the segment's own side padding
	}
	sepWidth := func() float32 {
		w, _ := c.MeasureText(0, ui.Span{Text: "/", Size: 11})
		return w + gap
	}
	total := func(labels []string, hidden bool) float32 {
		n := float32(1) // root
		sum := measure("⌂ " + a.rootLabel(root))
		if hidden {
			n += 2 // ellipsis and its separator
			sum += measure("…") + 2*th.Space(1) + sepWidth()
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
		lastMax = measure(visible[len(visible)-1]) - over - 2*th.Space(1)
		if lastMax < th.Space(9) {
			lastMax = th.Space(9)
		}
	}
	// A long file name in preview is clamped the same way, so it cannot push
	// the pinned Reload control off the panel.
	fileMax := float32(0)
	if fileLabel != "" {
		if over := remaining - available; over > 0 {
			fileMax = measure(fileLabel) - over - 2*th.Space(1)
			if fileMax < th.Space(9) {
				fileMax = th.Space(9)
			}
		}
	}

	ui.Row(c).Height(th.Space(9)).PaddingX(th.Space(3)).Gap(gap).AlignItems(ui.Center).
		Children(func() {
			// The home glyph keeps the root segment distinct from the
			// sidebar's workspace row that shows the same name.
			// The hover shows the absolute root, so two workspaces with the
			// same display name stay distinguishable (the Start page's
			// Workspace files entry carries the same tooltip).
			a.breadcrumbSegment(c, t, rootText, true, 0, a.filesRootTooltip(), func() {
				a.filesDir = ""
				a.filePath = ""
				a.fileErr = ""
			})
			if showHidden {
				ui.Text(c, "/").FontSize(th.FontSize - 3).TextColor(t.LabelTertiary)
				a.hiddenBreadcrumbSegments(c, t, segments, hidden)
			}
			for i, segment := range visible {
				ui.Text(c, "/").FontSize(th.FontSize - 3).TextColor(t.LabelTertiary)
				next := strings.Join(visible[:i+1], "/")
				if showHidden {
					next = strings.Join(segments, "/")
				}
				max := float32(0)
				if i == len(visible)-1 {
					max = lastMax
				}
				a.breadcrumbSegment(c, t, segment, false, max, "", func() {
					a.filesDir = next
					a.filePath = ""
					a.fileErr = ""
				})
			}
			if fileLabel != "" {
				ui.Text(c, "/").FontSize(th.FontSize - 3).TextColor(t.LabelTertiary)
				file := ui.Text(c, fileLabel).FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4.5)).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				if fileMax > 0 {
					file.MaxWidth(fileMax)
				}
			}
			ui.Spacer(c)
			// The directory opener uses the OS default application: a desktop
			// app can hand the workspace folder to Finder/Explorer/the
			// desktop's file manager or editor, which the web build cannot.
			openLabel := a.T("files.open_directory_in_system")
			open := ui.ButtonBase(c).Size(openW, th.Space(6)).Shrink(0).Center().
				Radius(design.RadiusXS).Label(openLabel).Tooltip(openLabel)
			if open.Hovered() {
				open = open.Background(t.BgHover)
			}
			open.Children(func() {
				ui.Icon(c, rightPanelExternalIcon).Size(th.Space(3.5), th.Space(3.5)).
					TextColor(t.LabelSecondary)
			})
			open.OnClick(func() {
				dir := root
				if a.filesDir != "" {
					dir = filepath.Join(root, filepath.FromSlash(a.filesDir))
				}
				a.filesNativeRun(c, dir, a.filesOpener())
			})
			reload := ui.ButtonBase(c).Size(reloadW, th.Space(6)).Shrink(0).Center().
				Radius(design.RadiusXS).
				Label(a.tr("Reload", "重新加载")).Tooltip(a.tr("Reload", "重新加载"))
			if reload.Hovered() {
				reload = reload.Background(t.BgHover)
			}
			reload.Children(func() {
				ui.Icon(c, icons.Reload()).Size(th.Space(3.5), th.Space(3.5)).
					TextColor(t.LabelSecondary)
			})
			reload.OnClick(func() { a.reloadFiles(c) })
		})
}

// hiddenBreadcrumbSegments is the ellipsis control that replaces the leading
// path segments at narrow panel widths, plus the menu that makes each hidden
// directory reachable in one click.
func (a *App) hiddenBreadcrumbSegments(c *ui.Context, t *design.Tokens, all, hidden []string) {
	th := t.Theme()
	label := a.tr("Hidden path segments", "隐藏的路径段")
	ell := ui.ButtonBase(c).Height(th.Space(6)).PaddingX(th.Space(1)).
		Radius(design.RadiusXS).Label(label).Tooltip(label)
	if ell.Hovered() {
		ell = ell.Background(t.BgHover)
	}
	st := a.liveMenus()
	ell.Children(func() {
		ui.Text(c, "…").FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4)).
			TextColor(t.LabelSecondary).MaxLines(1)
	})
	ell.Expanded(st.breadcrumbOpen)
	ell.OnClick(func() { st.breadcrumbOpen = !st.breadcrumbOpen })
	ui.Popover(c, ell, &st.breadcrumbOpen, func() {
		a.popoverMenuPanelIn(c, t, a.rightSidebarPanelWidth(c), func() {
			for i, segment := range hidden {
				prefix := strings.Join(all[:i+1], "/")
				name := segment
				// The entries keep their plain button role; only the surface
				// and the row look are the shared menu ones.
				a.popoverMenuRoleRow(c, t, name, ui.RoleButton, nil, func() {
					a.filesDir = prefix
					a.filePath = ""
					a.fileErr = ""
					st.breadcrumbOpen = false
				})
			}
		})
	})
}

// breadcrumbSegment is one clickable breadcrumb label: a native ButtonBase, so
// it takes the focus, Enter/Space and the hover tint like every other control.
// maxWidth > 0 bounds the label so the last visible directory ellipsizes
// instead of pushing the pinned Reload control off the panel.
func (a *App) breadcrumbSegment(c *ui.Context, t *design.Tokens, label string, root bool, maxWidth float32, tooltip string, jump func()) {
	th := t.Theme()
	item := ui.ButtonBase(c).Height(th.Space(6)).PaddingX(th.Space(1)).
		Radius(design.RadiusXS).Label(label)
	if tooltip != "" {
		item = item.Tooltip(tooltip)
	}
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
		text := ui.Text(c, label).FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4.5)).
			TextColor(color).MaxLines(1).Ellipsis("…")
		if maxWidth > 0 {
			text.MaxWidth(maxWidth - 2*th.Space(1))
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

// filesList lists the current directory, directories first, as native
// ButtonBase rows on the brand spacing grid.
func (a *App) filesList(c *ui.Context, t *design.Tokens, root string) {
	th := t.Theme()
	dir := root
	if a.filesDir != "" {
		dir = filepath.Join(root, filepath.FromSlash(a.filesDir))
	}
	entries, total, err := a.filesDirEntries(dir)
	if err != nil {
		a.rightEmpty(c, t, rightPanelFileIcon, fmt.Sprintf(
			a.T("files.could_not_read_the_directory_v"), err))
		return
	}
	if len(entries) == 0 {
		a.rightEmpty(c, t, icons.Folder(), a.T("files.this_directory_is_empty"))
		return
	}
	if total > len(entries) {
		ui.Text(c, fmt.Sprintf(a.T("files.showing_first_d_of_d_entries"),
			len(entries), total)).FontSize(th.FontSize-3).
			FixedLineHeight(th.Space(4)).TextColor(t.LabelTertiary).
			Padding(0, th.Space(3), th.Space(1), th.Space(3))
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(th.Space(1), th.Space(2), th.Space(2)).Gap(th.Space(0.5)).
			Children(func() {
				for _, entry := range entries {
					a.fileRow(c, t, entry)
				}
			})
	})
}

// fileRow is one directory or file in the listing: a native ButtonBase with
// the brand's line glyph, the name (a directory keeps its slash) and the same
// open/navigate behavior as before.
func (a *App) fileRow(c *ui.Context, t *design.Tokens, entry os.DirEntry) {
	th := t.Theme()
	name := entry.Name()
	isDir := entry.IsDir()
	row := ui.ButtonBase(c).Height(th.Space(7)).PaddingX(th.Space(2)).
		Gap(th.Space(2)).Radius(design.RadiusXS).Label(name)
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		ic := rightPanelFileIcon
		tint := t.LabelTertiary
		if isDir {
			ic = icons.Folder()
			tint = t.LabelSecondary
		}
		ui.Icon(c, ic).Size(th.Space(3.5), th.Space(3.5)).TextColor(tint)
		label := name
		if isDir {
			label += "/"
		}
		ui.Text(c, label).FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4.5)).
			TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
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

// filePreview draws the current file in the brand panel: the breadcrumb says
// where it is, a metadata row carries the kind/size and the native controls,
// and the body renders by kind — plain text, Markdown, or a decoded image.
func (a *App) filePreview(c *ui.Context, t *design.Tokens, root string) {
	th := t.Theme()
	panel := a.rightPanelUI()
	name := pathBase(a.filePath)
	a.filesBreadcrumb(c, t, root, name)
	if a.fileErr != "" {
		a.rightEmpty(c, t, rightPanelFileIcon, a.fileErr)
		return
	}
	abs, _ := safeJoin(root, a.filePath)
	ui.Row(c).Height(th.Space(8)).PaddingX(th.Space(3)).AlignItems(ui.Center).
		Gap(th.Space(2)).Children(func() {
		ui.Text(c, a.previewMetadataLabel(panel.previewKind, len(a.fileText))).
			FontSize(th.FontSize - 3).FixedLineHeight(th.Space(4)).
			TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
		// The desktop build can hand the file to the OS default application or
		// reveal it in the file manager; the web build cannot, so these are
		// part of the native panel instead of the preview body.
		openLabel := a.T("files.open_file_in_system")
		revealLabel := a.T("files.reveal_in_file_manager")
		nativeAction := func(label string, ic *ui.SVG, action func(string) error) {
			button := ui.ButtonBase(c).Size(th.Space(6), th.Space(6)).Shrink(0).Center().
				Radius(design.RadiusXS).Label(label).Tooltip(label)
			if button.Hovered() {
				button = button.Background(t.BgHover)
			}
			button.Children(func() {
				ui.Icon(c, ic).Size(th.Space(3.5), th.Space(3.5)).TextColor(t.LabelSecondary)
			})
			button.OnClick(func() { a.filesNativeRun(c, abs, action) })
		}
		nativeAction(openLabel, rightPanelExternalIcon, a.filesOpener())
		nativeAction(revealLabel, icons.FolderOpen(), a.filesRevealer())
		closePreview := ui.ButtonBase(c).Height(th.Space(6)).PaddingX(th.Space(2)).
			Radius(design.RadiusXS).Label(a.T("files.close_preview"))
		if closePreview.Hovered() {
			closePreview = closePreview.Background(t.BgHover)
		}
		closePreview.Children(func() {
			ui.Text(c, a.T("files.close_preview")).FontSize(th.FontSize - 3).
				FixedLineHeight(th.Space(4)).TextColor(t.LabelSecondary)
		})
		closePreview.OnClick(func() {
			a.filePath = ""
			a.fileText = ""
			a.fileTrunc = false
			a.fileLimited = false
			a.fileErr = ""
			panel.nativeErr = ""
			panel.previewKind = filesPreviewText
			panel.imageSrc = nil
			panel.imageErr = ""
		})
	})
	if msg := panel.nativeErr; msg != "" {
		ui.Text(c, msg).FontSize(th.FontSize-3).FixedLineHeight(th.Space(4)).
			TextColor(t.WarningText).MaxLines(2).Ellipsis("…").
			Padding(0, th.Space(3), 0, th.Space(3))
	}
	switch panel.previewKind {
	case filesPreviewImage:
		if panel.imageErr != "" {
			a.rightEmpty(c, t, rightPanelFileIcon, panel.imageErr)
			return
		}
		ui.ScrollBoth(c).Grow(1).Label(a.T("files.image_preview")).Children(func() {
			ui.Column(c).Padding(th.Space(2), th.Space(3), th.Space(3), th.Space(3)).
				AlignItems(ui.Start).Children(func() {
				ui.Image(c, panel.imageSrc).Radius(design.RadiusSM).
					MaxWidthPercent(100).Label(a.T("files.image_preview"))
			})
		})
	case filesPreviewMarkdown:
		ui.ScrollBoth(c).Grow(1).Label(a.T("files.markdown_preview")).Children(func() {
			ui.Column(c).Padding(th.Space(2), th.Space(3), th.Space(3), th.Space(3)).
				Children(func() {
					a.markdownBlocks(c, t, a.fileText)
				})
		})
	default:
		ui.ScrollBoth(c).Grow(1).Label(a.T("files.plain_text_preview")).Children(func() {
			ui.Column(c).Padding(th.Space(2), th.Space(3), th.Space(3), th.Space(3)).
				Children(func() {
					ui.Text(c, a.fileText).FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4.5)).
						TextColor(t.LabelPrimary)
				})
		})
	}
	if a.fileLimited {
		ui.Text(c, fmt.Sprintf(
			a.T("files.preview_limited_to_the_first_d_lines_d_characters_per_line"),
			maxPreviewLines, maxPreviewLineRunes)).
			FontSize(th.FontSize-3).FixedLineHeight(th.Space(4)).TextColor(t.WarningText).
			Padding(0, th.Space(3), 0, th.Space(3))
	}
	if a.fileTrunc {
		ui.Text(c, a.T("files.preview_truncated_at_256_kb")).
			FontSize(th.FontSize-3).FixedLineHeight(th.Space(4)).TextColor(t.WarningText).
			Padding(0, th.Space(3), th.Space(2), th.Space(3))
	}
}

// openFilePreview loads rel under the files root (the shown session's
// workspace). Paths are resolved through filepath.Clean and rejected when they
// escape the root.
func (a *App) openFilePreview(rel string) {
	a.ensureFilesSession()
	root := a.filesRoot()
	panel := a.rightPanelUI()
	a.filePath = rel
	a.fileText = ""
	a.fileErr = ""
	a.fileTrunc = false
	a.fileLimited = false
	panel.nativeErr = ""
	panel.previewKind = filesPreviewText
	panel.imageSrc = nil
	panel.imageErr = ""

	abs, err := safeJoin(root, rel)
	if err != nil {
		a.fileErr = a.T("files.that_path_is_outside_the_workspace")
		return
	}
	// Images decode from bytes instead of being read as text: a bitmap is
	// binary and would otherwise hit the binary-file fallback.
	if kind := previewKindForPath(rel); kind == filesPreviewImage {
		panel.previewKind = filesPreviewImage
		src, message := a.loadPreviewImage(abs)
		if message != "" {
			panel.imageErr = message
			return
		}
		panel.imageSrc = src
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
	// A truncated or line-limited Markdown file keeps the plain-text preview:
	// rendering a cut document would silently drop the tail, while the text
	// preview says what was left out.
	if !a.fileTrunc && !a.fileLimited && previewKindForPath(rel) == filesPreviewMarkdown {
		panel.previewKind = filesPreviewMarkdown
	}
}

// terminalPanel is the command runner in the brand panel: the working
// directory, the Choose shell picker, Clear, the output and an input line with
// the native Run/Stop controls. Each Run starts a fresh process in the
// selected shell; there is no PTY and no persistent shell.
func (a *App) terminalPanel(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	root := a.workspaceRoot()
	dir := root
	if dir == "" {
		dir = "."
	}
	st := a.startUI()
	ui.Row(c).Height(th.Space(9)).PaddingX(th.Space(2)).AlignItems(ui.Center).
		Gap(th.Space(2)).BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Text(c, dir).FontSize(th.FontSize - 3).FixedLineHeight(th.Space(4)).
			TextColor(t.LabelTertiary).Grow(1).MinWidth(0).MaxLines(1).Ellipsis("…")
		shellLabel := a.tr("Choose shell", "选择 shell")
		shell := ui.ButtonBase(c).Height(th.Space(6)).PaddingX(th.Space(2)).
			Gap(th.Space(1)).Radius(design.RadiusSM).MaxWidth(th.Space(30)).
			Label(shellLabel).Tooltip(shellLabel)
		if shell.Hovered() {
			shell = shell.Background(t.BgHover)
		}
		shell.Children(func() {
			ui.Icon(c, icons.Terminal()).Size(th.Space(3.5), th.Space(3.5)).
				TextColor(t.LabelSecondary)
			ui.Text(c, a.terminalShellLabel()).FontSize(th.FontSize - 3).
				FixedLineHeight(th.Space(4)).TextColor(t.LabelSecondary).
				MaxLines(1).Ellipsis("…")
			ui.Icon(c, icons.ChevronDown()).Size(th.Space(3), th.Space(3)).
				TextColor(t.LabelTertiary)
		})
		shell.Expanded(st.shellMenuOpen)
		shell.OnClick(func() { st.shellMenuOpen = !st.shellMenuOpen })
		ui.Popover(c, shell, &st.shellMenuOpen, func() { a.shellPickerMenu(c, t) })
		clear := ui.ButtonBase(c).Height(th.Space(6)).PaddingX(th.Space(2)).
			Radius(design.RadiusSM).Label(a.T("files.clear"))
		if clear.Hovered() {
			clear = clear.Background(t.BgHover)
		}
		clear.Children(func() {
			ui.Text(c, a.T("files.clear")).FontSize(th.FontSize - 3).
				FixedLineHeight(th.Space(4)).TextColor(t.LabelSecondary)
		})
		clear.OnClick(func() { a.clearTerminal() })
	})

	out, _ := a.terminalState()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(th.Space(2), th.Space(3), th.Space(2), th.Space(3)).
			Children(func() {
				if out == "" {
					ui.Column(c).Gap(th.Space(1)).Children(func() {
						ui.Text(c, a.T("files.run_a_command_in_the_workspace_root")).
							FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4.5)).
							TextColor(t.LabelTertiary)
					})
					return
				}
				design.Apply(ui.Text(c, out),
					func(e ui.Element) ui.Element { return e.Font(design.MonoFontStack()) }).
					FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4.5)).
					TextColor(t.LabelPrimary)
			})
	})

	_, running := a.terminalState()
	ui.Column(c).Padding(th.Space(2), th.Space(3), th.Space(3), th.Space(3)).
		Gap(th.Space(2)).BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &a.termInput).Label(a.T("files.command")).
				AutoFocus().OnSubmit(func() { a.runTerminal(c) }).Grow(1)
			if running {
				stop := ui.Button(c, a.tr("Stop", "停止")).TextColor(t.Danger)
				if stop.Clicked() {
					a.stopTerminal()
				}
				return
			}
			if ui.PrimaryButton(c, a.T("files.run")).Clicked() {
				a.runTerminal(c)
			}
		})
		ui.Text(c, a.T("files.simple_command_runner_no_pty_no_persistent_shell")).
			FontSize(th.FontSize - 3).FixedLineHeight(th.Space(4)).TextColor(t.LabelTertiary)
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

// rightEmpty is the brand panel's centered empty state: a soft glyph disc and
// one line of guidance. Files' missing root, an unreadable directory, the
// preview error and the Session Log's empty states all use it.
func (a *App) rightEmpty(c *ui.Context, t *design.Tokens, ic *ui.SVG, message string) {
	th := t.Theme()
	ui.Column(c).Grow(1).Padding(th.Space(6)).Justify(ui.Center).AlignItems(ui.Center).
		Gap(th.Space(3)).Children(func() {
		if ic != nil {
			ui.Row(c).Size(th.Space(10), th.Space(10)).Radius(th.Space(5)).
				Background(t.BgHover).Center().Children(func() {
				ui.Icon(c, ic).Size(th.Space(5), th.Space(5)).TextColor(t.LabelTertiary)
			})
		}
		ui.Text(c, message).FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4.5)).
			TextColor(t.LabelTertiary).MaxLines(3)
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
	panel := a.rightPanelUI()
	panel.nativeErr = ""
	panel.dirCachePath = ""
	panel.dirCacheEntries = nil
	panel.dirCacheTotal = 0
	panel.dirCacheErr = nil
	panel.dirCacheAt = time.Time{}
	panel.previewKind = filesPreviewText
	panel.imageSrc = nil
	panel.imageErr = ""
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
