package uiroot

import (
	"log"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/brand"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// sessionScreenID is the product screen that contributes the session inspector
// (internal/ui/session.ScreenID). The inspector reads the active session's read
// model, so the host only offers it on that screen.
const sessionScreenID = "session"

// inspectorPaneHeaderMinWidth is the width below which the inspector module
// hides its own pane-header close control: buildPaneClose compares the window
// against the expanded-sidebar worst case. The host draws the docked header
// and its close control itself there, so the close affordance is truthful at
// the 900 DIP host minimum with the sidebar collapsed to the rail. (Phase 2
// hands the module the real docked state, and this copy disappears.)
var inspectorPaneHeaderMinWidth = design.SidebarWidth + design.ContentMinWidth + design.Space6 + design.InspectorMinWidth

// view builds one frame. It is the product's single UI entry point:
//
//	drain framework posts -> apply host theme -> window chrome (sidebar + top
//	bar) -> current screen -> overlays/settings.
//
// The host owns the layout containers (regions) and renders reserved slots; it
// never builds a module's content itself.
func (r *Root) view(c *ui.Context) {
	if c == nil {
		return
	}
	// Framework work queued from any goroutine runs first, on the UI thread, so
	// this frame renders its result (contracts.UIRuntime.Post).
	r.rt.drain()
	r.frames.Add(1)

	// Every menu MyGo draws in the window follows the brand look: the product
	// already draws its buttons' menus with PopoverMenuButton, so the system's
	// text-input/table menus would be the only ones in another style (mygo
	// context-menu.md: every menu of a window).
	c.SetMenuPresentation(ui.PopoverMenus)

	// The old shell's chrome shortcut: Cmd/Ctrl+B collapses or expands the
	// left sidebar (the 64 DIP icon rail).
	r.bindChromeShortcuts(c)

	// Host applies the brand theme once; modules build with the frame theme.
	// The desktop's text-size preference scales the theme's font size, which
	// every native control reads (design tokens scale their own type scale).
	c.SetTheme(themeAtScale(r.theme.currentTheme(), c.Preferences().TextScale))
	// Host-owned commands and chrome strings are localized for the current
	// language; the registry has no locale of its own, so re-register them when
	// the user switches.
	if r.locale.Language() != r.hostLang {
		r.registerHostCommands()
	}
	tokens := design.Of(c)
	r.loadChromeWidths()
	// Persist a settled divider drag once, at frame start and out of the
	// per-move build branch (AR-03): the drag itself only scheduled the save.
	r.flushChromeWidthsSave(c)
	// The expanded sidebar resolves the first-run destination while rendering its
	// selection, but a persisted collapsed sidebar (the icon rail) does not draw
	// the screen list: without an explicit default here nav.Current() stays "",
	// every screen-scoped header/action slot is filtered out and the inspector
	// reports itself unavailable. Resolve the effective default screen before any
	// chrome reads the current destination.
	if r.nav.Current() == "" {
		if id := defaultScreenID(visibleScreens(r.rt.screenSpecs())); id != "" {
			if err := r.nav.Push(id); err != nil {
				log.Printf("uiroot: default screen %q: %v", id, err)
			}
		}
	}
	r.announceNavigation(c)

	ui.Row(c).Fill().Background(tokens.BgBase).Children(func() {
		if r.effectiveSidebarCollapsed(c) {
			r.sidebarRail(c)
			r.contentArea(c)
			return
		}
		// A native split view instead of fixed columns: the divider drags (or
		// moves with the arrows when focused) and the width persists in the
		// host settings (docs/ui/split.md).
		split := ui.Split(c, &r.sidebarWidth, func() {
			r.sidebarExpanded(c)
		}, func() {
			r.contentArea(c)
		}).SplitReset(design.SidebarWidth).
			Label(r.tr("侧栏宽度可调", "Resizable sidebar")).
			Grow(1).FillHeight().Shrink(1).MinWidth(0)
		if split.Changed() {
			// Clamp while dragging, not only when saving: Split itself allows a
			// 40 DIP pane, and a 40-199 DIP sidebar breaks the navigation and
			// the search field until a restart snaps it back to the saved min.
			if clamped := clampWidth(r.sidebarWidth, minSidebarWidth, maxSidebarWidth); clamped != r.sidebarWidth {
				r.sidebarWidth = clamped
				c.Invalidate()
			}
			// Debounce the persist: the drag updates the in-memory width every
			// move frame, but the settings write is coalesced to one save after
			// the value settles (AR-03), not an fsync per move inside the build.
			r.scheduleChromeWidthsSave(c)
		}
	})

	r.inspectorDrawerShown = false
	r.inspectorDrawer(c)
	// The palette is built last: it is the window's top layer, so Cmd+K from
	// inside the settings modal shows above it instead of behind it (fix round
	// SH-07), and Escape closes the palette before the surface below it.
	r.overlays(c)
	r.palette(c)
}

// announceNavigation reports navigation changes to assistive technology: the
// hand-built navigator does not announce a new screen's title the way a Router
// does, and the inspector toggle is otherwise silent (mygo accessibility.md:
// Announcements). It reads the state before the frame builds.
func (r *Root) announceNavigation(c *ui.Context) {
	if current := r.nav.Current(); current != "" && current != r.lastAnnouncedScreen {
		r.lastAnnouncedScreen = current
		if spec, ok := r.rt.screen(current); ok {
			c.Announce(r.screenLabel(spec))
		}
	}
	if r.rightOpen != r.lastRightOpen {
		r.lastRightOpen = r.rightOpen
		if r.inspectorAvailable() {
			if r.rightOpen {
				c.Announce(r.tr("检查器已打开", "Inspector opened"))
			} else {
				c.Announce(r.tr("检查器已关闭", "Inspector closed"))
			}
		}
	}
}

// sidebarExpanded renders the expanded source column inside the sidebar
// ui.Split pane: the brand row (logo + wordmark), the primary action slot,
// screen navigation with icons, the session tree slot, the footer slot and the
// host settings entry. The split view owns the column's width, so the surface
// only paints the background and the edge.
func (r *Root) sidebarExpanded(c *ui.Context) {
	t := design.Of(c)
	screens := visibleScreens(r.rt.screenSpecs())
	selected := r.nav.Current()
	initial := selected == ""
	if initial {
		selected = defaultScreenID(screens)
	}
	column := ui.Column(c).FillHeight().Background(t.BgSidebar).
		BorderWidth(0, 1, 0, 0).BorderColor(t.BorderL3).
		// The sidebar is a complementary landmark (mygo RoleComplementary, as
		// HTML's aside): screen readers can jump to the navigation apart from
		// the main content. Named because a window can have several
		// complementary landmarks (the inspector is the other one).
		Role(ui.RoleComplementary).Label(r.tr("侧栏", "Sidebar"))
	changed := false
	column.Children(func() {
		// Brand row: the mark, the wordmark and the collapse control (Cmd+B is
		// the keyboard shortcut; both stay discoverable).
		brandRow := ui.Row(c).FillWidth().AlignItems(ui.Center).
			Padding(0, design.Space4, 0, design.Space4).Gap(design.Space2).Height(64)
		brandRow.Children(func() {
			ui.Image(c, brand.Mark(t.Dark)).Size(28, 28)
			design.Apply(ui.Text(c, r.appName), t.Font(design.TypeM)).FontWeight(600).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			collapseLabel := r.tr("折叠侧边栏", "Collapse sidebar")
			uikit.IconButton(c, uikit.IconButtonOptions{
				Key:     "chrome.sidebar.collapse",
				Label:   collapseLabel,
				Tooltip: collapseLabel,
				Icon:    icons.PanelLeft(),
				Size:    design.IconSizeM,
				OnClick: func() { r.SetSidebarCollapsed(true) },
			})
		})

		// The navigation rows, the session tree and the primary action are
		// module contributions rendered inside the native Sidebar, which owns
		// selection, keyboard navigation and scrolling. The footer stays docked
		// below it, outside the scrolling content, so the settings and view
		// entries never scroll away or get pushed off by a long session list.
		sidebar := ui.Sidebar(c, &selected, func() {
			r.slot(c, contracts.SlotSidebarPrimary)
			r.slot(c, contracts.SlotSidebarNav)
			r.slot(c, contracts.SlotSidebarTree)
		}).Grow(1).Shrink(1).FillWidth().PaddingY(design.Space1).Label(r.tr("主导航", "Main navigation"))
		sidebar.Background(t.BgSidebar)
		changed = sidebar.Changed()
		ui.Column(c).FillWidth().Shrink(0).Children(func() {
			r.slot(c, contracts.SlotSidebarFooter)
		})
	})
	// Only a real user choice in the native Sidebar navigates: modules often
	// call Navigator.Push in the same frame (session rows, new-chat buttons),
	// and the canonical selection starts as the current screen, so a stale
	// selection must not overwrite that navigation.
	if (changed || initial) && selected != "" && selected != r.nav.Current() {
		if err := r.nav.Push(selected); err != nil {
			log.Printf("uiroot: %v", err)
		}
	}
}

// sidebarRail is the collapsed 64 DIP sidebar from the old shell: the brand
// mark (click expands), one icon per screen and the settings entry. Module
// slots compose the expanded sidebar only.
// tr localizes host chrome strings: English when the user chose English, the
// product's Chinese default otherwise.
func (r *Root) tr(zh, en string) string {
	if r.locale.Language() == "en" {
		return en
	}
	return zh
}

func (r *Root) sidebarRail(c *ui.Context) {
	t := design.Of(c)
	column := ui.Column(c).Width(design.SidebarRailWidth).FillHeight().
		AlignItems(ui.Center).Gap(design.Space1).
		Padding(design.Space3, 0, design.Space3, 0).
		Label(r.tr("侧栏", "Sidebar")).
		Role(ui.RoleComplementary).
		FocusGroup(ui.Vertical)
	design.Apply(column, func(el ui.Element) ui.Element {
		return el.Background(t.BgSidebar).BorderWidth(0, 1, 0, 0).BorderColor(t.BorderL3)
	})
	column.Children(func() {
		expandLabel := r.tr("展开侧边栏", "Expand sidebar")
		// The rail's first control is the brand mark, a bitmap logo rather than
		// an SVG glyph: it expands the sidebar, as the wordmark row does. It is
		// the one subtle icon button of the chrome: the light plate is always
		// there, so the brand control reads as the rail's home affordance at rest
		// instead of appearing only under the pointer.
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "chrome.rail.expand",
			Label:   expandLabel,
			Tooltip: expandLabel,
			Size:    design.IconSizeL,
			Variant: uikit.IconButtonSubtle,
			Content: func(cc *ui.Context) {
				ui.Image(cc, brand.Mark(t.Dark)).Size(design.IconSizeL, design.IconSizeL)
			},
			OnClick: func() { r.SetSidebarCollapsed(false) },
		})
		// The brand mark expands the sidebar, but a logo reads as "home/about"
		// long before it reads as "expand": the audit's pointer users reported
		// no visible way back from the rail (R10). A second, explicit panel
		// toggle sits under the mark, always visible, with the same glyph as
		// the expanded sidebar's collapse control.
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "chrome.rail.toggle",
			Label:   expandLabel,
			Tooltip: expandLabel,
			Icon:    icons.PanelLeft(),
			Size:    design.IconSizeS,
			Variant: uikit.IconButtonSubtle,
			OnClick: func() { r.SetSidebarCollapsed(false) },
		})
		// The new-session action is the expanded sidebar's primary action; the
		// rail is the same sidebar in its icon form, so it keeps that action
		// instead of dropping the shell's only pointer affordance for creating
		// a conversation (fix round SH-05).
		newChatLabel := r.tr("新对话", "New chat")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "chrome.rail.new",
			Label:   newChatLabel,
			Tooltip: newChatLabel,
			Icon:    icons.NewChat(),
			Size:    design.IconSizeM,
			OnClick: func() { r.newSessionShortcut() },
		})
		// The rail does not render the expanded sidebar's conversation tree
		// (that lives in a module slot), so the rail keeps a conversation entry
		// of its own: a native popover with the recent conversations, their
		// running/unread state, a search field and the way back to the expanded
		// sidebar. It adds no screen.
		r.railSessionsEntry(c, t)
		for _, spec := range visibleScreens(r.rt.screenSpecs()) {
			spec := spec
			// The rail navigates screens, not conversations: the session screen
			// is the workspace tree's destination in the expanded sidebar and
			// had no navigation row there. Listing it here made the two chrome
			// forms show different destination sets (fix round SH-05).
			if spec.ID == sessionScreenID || spec.ID == "home" {
				continue
			}
			icon := r.screenIcon(spec.ID)
			label := r.screenLabel(spec)
			// The rail destination is a toggle-like destination, not a plain
			// button: only the toggle role carries Selected's state to assistive
			// technology as "pressed", so a screen reader can tell which screen
			// the rail is on.
			uikit.IconButton(c, uikit.IconButtonOptions{
				Key:      "chrome.rail." + spec.ID,
				Label:    label,
				Tooltip:  label,
				Icon:     icon,
				Size:     design.IconSizeM,
				Toggle:   true,
				Selected: r.nav.Current() == spec.ID,
				OnClick: func() {
					if err := r.nav.Push(spec.ID); err != nil {
						log.Printf("uiroot: %v", err)
					}
					r.requestFrame()
				},
			})
		}
		ui.Spacer(c).Grow(1)
		settingsLabel := r.locale.T("nav.settings")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "chrome.rail.settings",
			Label:   settingsLabel,
			Tooltip: settingsLabel,
			Icon:    icons.Sliders(),
			Size:    design.IconSizeM,
			OnClick: func() {
				r.nav.ToggleSettings()
				r.requestFrame()
			},
		})
	})
}

// railSessionLimit bounds the recent conversations the rail popover lists (the
// popover is a quick switcher, not the expanded sidebar's full tree).
const railSessionLimit = 8

// railSessionsEntry is the collapsed rail's conversation entry: a toggle-like
// icon button that opens a native popover listing the recent conversations (with
// their running/unread state), a search field and the "expand sidebar" action.
// Choosing a conversation opens it and leaves the rail as it is; the popover adds
// no screen and no second conversation list.
func (r *Root) railSessionsEntry(c *ui.Context, t *design.Tokens) {
	label := r.tr("会话", "Sessions")
	anchor := uikit.IconButton(c, uikit.IconButtonOptions{
		Key:      "chrome.rail.sessions",
		Label:    label,
		Tooltip:  label,
		Icon:     icons.Message(),
		Size:     design.IconSizeM,
		Toggle:   true,
		Selected: r.railSessionsOpen,
		OnClick: func() {
			r.railSessionsOpen = !r.railSessionsOpen
			if r.railSessionsOpen {
				r.railSessionsQuery = ""
			}
			r.requestFrame()
		},
	})
	if !r.railSessionsOpen {
		return
	}
	ui.Popover(c, anchor, &r.railSessionsOpen, func() {
		ui.Column(c).Width(design.DialogWidthXS).Gap(design.Space2).Children(func() {
			ui.TextInput(c, &r.railSessionsQuery).
				Label(r.tr("搜索会话", "Search conversations")).
				Placeholder(r.tr("搜索会话…", "Search conversations…")).
				FillWidth()
			sessions := r.railSessions()
			if len(sessions) == 0 {
				design.Apply(ui.Text(c,
					r.tr("没有匹配的会话", "No matching conversations")),
					t.TextMuted(design.TypeXS)).MaxLines(2)
			}
			for _, session := range sessions {
				r.railSessionRow(c, t, session)
			}
			ui.Divider(c)
			expandLabel := r.tr("展开侧栏", "Expand sidebar")
			if ui.Button(c, expandLabel).Label(expandLabel).Clicked() {
				r.railSessionsOpen = false
				r.SetSidebarCollapsed(false)
			}
		})
	})
}

// railSessionRow is one conversation row of the rail popover: its title, a
// running (warning) or unread (accent) dot, and the same state in the row's
// accessible name so assistive technology hears it too. A click opens the
// conversation and closes the popover.
func (r *Root) railSessionRow(c *ui.Context, t *design.Tokens, session contracts.ChatSessionInfo) {
	title := strings.TrimSpace(session.Title)
	if title == "" {
		title = r.tr("未命名会话", "Untitled conversation")
	}
	rowLabel := title
	switch {
	case session.Running:
		rowLabel += " · " + r.tr("运行中", "Running")
	case session.Unread:
		rowLabel += " · " + r.tr("未读", "Unread")
	}
	// The row's identity is the session, not its position: the list reorders
	// while the popover is open (a run starts, a new message bumps UpdatedAt),
	// and a positional key would leave the keyboard focus on the slot while
	// another session moved into it — Enter would open the wrong conversation
	// (2026-11 audit AR-04).
	row := ui.ButtonBase(c.Key("chrome.rail.session."+session.ID)).
		FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space2, design.Space3).Radius(design.RadiusSM).Label(rowLabel)
	// The registered rich row paints its own states (ButtonBase has no look):
	// pressed uses the product's one pressed vocabulary (the selection tint,
	// as rowTint and IconButton do), hover the lighter tint (audit UD-18).
	switch {
	case row.Pressed():
		row = row.Background(t.BgSelected)
	case row.Hovered():
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).TextColor(t.LabelPrimary).
			Grow(1).MaxLines(1).Ellipsis("…")
		switch {
		case session.Running:
			// running→Warning (DESIGN.md §5.2): a Success-green dot reads as
			// "finished" while the run is still streaming (audit UD-03).
			ui.Box(c).Size(design.Space2, design.Space2).Radius(design.Space1).Background(t.Warning)
		case session.Unread:
			ui.Box(c).Size(design.Space2, design.Space2).Radius(design.Space1).Background(t.Accent)
		}
	})
	if row.Clicked() {
		r.railSessionsOpen = false
		r.openNotifiedSession(session.ID)
		r.requestFrame()
	}
}

// railSessions returns the recent user conversations the rail popover lists,
// filtered by its search draft. Archived and automation/subagent conversations
// stay out, as in the sidebar; the newest conversations first (the chat
// service's order), bounded by railSessionLimit.
func (r *Root) railSessions() []contracts.ChatSessionInfo {
	r.mu.Lock()
	chat := r.chatService
	kernel := r.kernelContext
	r.mu.Unlock()
	if chat == nil && kernel != (cordis.Context{}) {
		if fromKernel, ok := kernel.Get(contracts.KeyChat); ok {
			chat = fromKernel
		}
	}
	if chat == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(r.railSessionsQuery))
	sessions := chat.Sessions()
	recent := make([]contracts.ChatSessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if session.Archived || session.Origin != contracts.SessionOriginUser {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(session.Title), query) {
			continue
		}
		recent = append(recent, session)
		if len(recent) == railSessionLimit {
			break
		}
	}
	return recent
}

// screenIcon maps a registered screen id to the shell's icon. The icon set must
// not collapse two destinations onto one glyph (the audit found the rail drew
// both Automation and Plugins as the same cube).
func (r *Root) screenIcon(id string) *ui.SVG {
	switch id {
	case "home":
		return icons.Home()
	case "session":
		return icons.Message()
	case "automation":
		return icons.Clock()
	case "plugins":
		return icons.Plugin()
	case "settings":
		return icons.Sliders()
	default:
		return icons.Data()
	}
}

// screenLabel localizes the shell's destination names; modules keep their own
// titles for their own navigation rows.
func (r *Root) screenLabel(spec contracts.ScreenSpec) string {
	switch spec.ID {
	case "home":
		return r.tr("首页", "Home")
	case "session":
		return r.tr("对话", "Chat")
	case "automation":
		return r.tr("自动化", "Automation")
	case "plugins":
		return r.tr("能力", "Capabilities")
	default:
		return spec.Title
	}
}

// screen renders the current module screen between the screen header and the
// screen footer slots. The screen body itself is contributed by a module.
func (r *Root) screen(c *ui.Context) {
	specs := visibleScreens(r.rt.screenSpecs())
	if len(specs) == 0 {
		ui.Column(c).FillWidth().Grow(1).Padding(24).Children(func() {
			ui.Text(c, r.locale.T("status.no-screen"))
		})
		return
	}
	spec, ok := r.rt.screen(r.nav.Current())
	if !ok || !visible(spec) {
		if id := defaultScreenID(specs); id != "" {
			if fallback, exists := r.rt.screen(id); exists {
				spec = fallback
				ok = true
			}
		}
		if !ok {
			spec = specs[0]
		}
	}
	ui.Column(c).FillWidth().Grow(1).Padding(design.Space6).Gap(design.Space4).Children(func() {
		// The screen's header/actions/tabs live in the window's B3 top band
		// (contentHeader); the body only contributes its own content and its
		// footer status.
		r.safeBuild(c, spec.Build)
		if r.hasAnySlot(contracts.SlotScreenFooter) {
			// The screen footer is the window's status area, so it is the
			// contentinfo landmark (mygo RoleContentInfo, as HTML's footer). Only
			// wrapped when a screen really contributes status content, so a
			// screen without one adds no empty landmark and no extra layout box.
			ui.Column(c).FillWidth().Shrink(0).
				Role(ui.RoleContentInfo).Label(r.tr("状态栏", "Status bar")).
				Children(func() {
					r.slot(c, contracts.SlotScreenFooter)
				})
		}
	})
}

// contentArea renders the main column, and the docked inspector beside it in a
// second native split view when the window is wide enough to dock it. The
// effective sidebar width (optionally the temporary rail the responsive
// inspector needs) is already reflected by contentColumnWidth; only a window
// that cannot dock the inspector even with the rail falls back to the modal
// drawer over the content.
func (r *Root) contentArea(c *ui.Context) {
	main := func() {
		ui.Column(c).FillHeight().Grow(1).Shrink(1).MinWidth(0).
			// The main landmark (mygo RoleMain): the window's main content, apart
			// from its top bar and sidebars. The docked inspector is a sibling
			// pane of this column, so it is not part of it.
			Role(ui.RoleMain).Label(r.tr("主内容", "Main content")).
			Children(func() {
				// The band keeps the whole window width: the drawer covers its
				// right-hand utilities while it is open, and narrowing the band
				// itself would squeeze the screen title and the agent entries into
				// the space the drawer already covers.
				r.contentHeader(c)
				body := ui.Column(c).FillHeight().Grow(1).Shrink(1).MinWidth(design.Space0)
				// A drawer covers the right edge of the window instead of docking,
				// so the screen body reserves its width: the composer and its
				// send/stop controls stay fully visible to the left of the drawer
				// instead of hanging half-way under it (report R3). The scrim still
				// marks the drawer as modal, so the reserved band is visibly not
				// interactive.
				if r.rightPanelDrawerOpen(c) {
					body = body.Padding(design.Space0, design.InspectorWidth, design.Space0, design.Space0)
				}
				body.Children(func() { r.screen(c) })
			})
	}
	if !r.inspectorDocked(c) {
		main()
		return
	}
	// ui.Split sizes its *first* pane, so the split's size is the content
	// column's width: the inspector keeps its own width on the right and the
	// content takes the rest (a dragged divider updates the inspector width).
	// inspectorLayoutWidth shrinks the pane to what the column can afford while
	// the content keeps ContentMinWidth, so the temporary rail's narrow window
	// still docks a usable inspector instead of a clipped one.
	contentWidth := r.contentColumnWidth(c)
	contentPaneWidth := contentWidth - r.inspectorLayoutWidth(c)
	if contentPaneWidth < design.Space6 {
		contentPaneWidth = design.Space6
	}
	// The inner split must take the content column's whole height, as the
	// outer sidebar split does: a split is a row whose height is its content's
	// unless told otherwise, and the panes' FillHeight columns cannot resolve
	// against an auto height (docs/ui/layout.md: Fill/FillHeight take the
	// parent's whole content box; docs/ui/split.md and examples/gallery build
	// a split with an explicit size and Fill panes). Without it the session
	// screen renders at its intrinsic height: an empty conversation floats the
	// composer in the middle of the window, and a long transcript grows the
	// page past the window so the composer is pushed offscreen and the
	// transcript List has no bounded height to scroll (the reported bug). It
	// also grows into the content column's width: with the collapsed rail the
	// split is the sidebar row's second child directly (no outer split pane to
	// stretch it), and without Grow it would size itself to the first pane
	// plus the inspector's intrinsic content instead of the window.
	split := ui.Split(c, &contentPaneWidth, main, func() {
		r.inspectorDockedPane(c)
	}).SplitReset(contentWidth - design.InspectorWidth).
		Label(r.tr("右侧栏宽度可调", "Resizable right panel")).
		Grow(1).Shrink(1).FillHeight()
	if split.Changed() {
		width := clampWidth(contentWidth-contentPaneWidth, minInspectorWidth, maxInspectorWidth)
		if width != r.inspectorWidth {
			r.inspectorWidth = width
			c.Invalidate()
		}
		// One coalesced save after the drag settles, as the sidebar split does
		// (AR-03): no settings fsync on the move hot path.
		r.scheduleChromeWidthsSave(c)
	}
}

// inspectorAvailable reports whether the window-level inspector belongs to the
// current screen. Only the session screen contributes the right-panel slot and
// the session read model the panes show, so the inspector has no meaning on
// Home, Automation or Plugins; it must not open there or keep its content
// cross-screen.
func (r *Root) inspectorAvailable() bool {
	return r.nav.Current() == sessionScreenID && r.hasAnySlot(contracts.SlotSessionPanelRight)
}

// preferredSidebarWidth is the expanded sidebar's width: the width the user
// dragged and saved, or the brand default before the first frame loads it.
func (r *Root) preferredSidebarWidth() float32 {
	if r.widthsLoaded && r.sidebarWidth > 0 {
		return r.sidebarWidth
	}
	return design.SidebarWidth
}

// preferredInspectorWidth is the docked inspector's preferred width: the width
// the user dragged and saved, or the brand default before the first frame loads
// it.
func (r *Root) preferredInspectorWidth() float32 {
	if r.widthsLoaded && r.inspectorWidth > 0 {
		return r.inspectorWidth
	}
	return design.InspectorWidth
}

// inspectorDrawerAt reports whether an open inspector would take the modal
// drawer form with base as the sidebar's width: the same decision
// rightPanelDrawerOpen makes, with the sidebar width as a parameter so the
// responsive rail can ask what the rail would afford. The drawer is the last
// resort: a docked inspector below InspectorMinWidth would cover the content
// with a scrim and block the composer.
func (r *Root) inspectorDrawerAt(c *ui.Context, base float32) bool {
	width, _ := c.Size()
	dockW := r.preferredInspectorWidth()
	if width > 0 {
		if avail := width - base - design.ContentMinWidth - design.Space6; avail < dockW {
			dockW = avail
		}
	}
	return dockW < design.InspectorMinWidth
}

// inspectorNeedsRail reports whether the open inspector can dock only with the
// sidebar's 64 DIP rail: the expanded sidebar would leave the inspector under
// InspectorMinWidth (or squeeze the content under ContentMinWidth), while the
// rail leaves both minima satisfiable. It is a layout state only — the user's
// collapsed preference is never written.
func (r *Root) inspectorNeedsRail(c *ui.Context) bool {
	if !r.rightOpen || !r.inspectorAvailable() || r.sidebarCollapsed {
		return false
	}
	return r.inspectorDrawerAt(c, r.preferredSidebarWidth()) &&
		!r.inspectorDrawerAt(c, design.SidebarRailWidth)
}

// effectiveSidebarCollapsed reports whether this frame renders the sidebar as
// the icon rail: the user's own Cmd/Ctrl+B state, or the temporary rail an open
// inspector needs on a window too narrow for the expanded sidebar plus a usable
// inspector. Closing the inspector or widening the window drops the temporary
// part automatically, because it is derived from the layout every frame.
func (r *Root) effectiveSidebarCollapsed(c *ui.Context) bool {
	return r.sidebarCollapsed || r.inspectorNeedsRail(c)
}

// effectiveSidebarWidth is the width this frame's sidebar takes: the rail when
// effectiveSidebarCollapsed, else the preferred expanded width.
func (r *Root) effectiveSidebarWidth(c *ui.Context) float32 {
	if r.effectiveSidebarCollapsed(c) {
		return design.SidebarRailWidth
	}
	return r.preferredSidebarWidth()
}

// contentColumnWidth estimates the room the content column and the docked
// inspector share: the window less the effective sidebar (the user's rail, the
// temporary responsive rail, or the expanded column) and the outer split's
// divider. The inner split only needs it to keep the inspector's width fixed
// while the content flexes.
func (r *Root) contentColumnWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	sidebar := r.effectiveSidebarWidth(c)
	if width <= 0 {
		return design.ContentMinWidth + r.preferredInspectorWidth()
	}
	return width - sidebar - design.Space1
}

// inspectorLayoutWidth resolves the width the docked inspector pane takes: the
// user's preferred width when the content column can afford it, else the widest
// width down to InspectorMinWidth that still leaves the content its
// ContentMinWidth. It never writes the saved width, so the user's drag is
// restored when the window widens again.
func (r *Root) inspectorLayoutWidth(c *ui.Context) float32 {
	inspector := r.preferredInspectorWidth()
	if avail := r.contentColumnWidth(c) - design.ContentMinWidth; avail < inspector {
		inspector = avail
	}
	if inspector < design.InspectorMinWidth {
		inspector = design.InspectorMinWidth
	}
	return clampWidth(inspector, minInspectorWidth, maxInspectorWidth)
}

// inspectorDocked reports whether the open inspector renders as a docked pane:
// a module contributes to the slot and the window can afford the inspector
// without squeezing the content below its minimum width.
func (r *Root) inspectorDocked(c *ui.Context) bool {
	if !r.rightOpen || !r.inspectorAvailable() {
		return false
	}
	return !r.rightPanelDrawerOpen(c)
}

// inspectorDockedPane is the inspector's pane in the content split view. The
// split view owns its width (dragged by the divider), so the pane only paints
// its surface and builds the module's contribution.
func (r *Root) inspectorDockedPane(c *ui.Context) {
	t := design.Of(c)
	// The right sidebar owns its own header (tab strip) and page padding: the
	// host only paints the docked surface and the divider so the tab strip can
	// sit flush at the top of the column, DSH-style.
	ui.Column(c).FillHeight().Background(t.BgSidebar).
		BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL3).
		// The docked inspector is the window's second complementary landmark
		// (the sidebar is the first): the panel stands apart from the main
		// content and completes it, as HTML's aside.
		Role(ui.RoleComplementary).Label(r.tr("右侧栏", "Right sidebar")).
		Children(func() {
			r.dockedInspectorHeader(c, t)
			r.slot(c, contracts.SlotSessionPanelRight)
		})
}

// dockedInspectorHeader draws the docked inspector's title row with its own
// close control while the module hides its pane-header close (narrow docked
// windows; see inspectorPaneHeaderMinWidth). The condition is the host's own
// docked presentation: the header only shows while the pane really is docked,
// so a narrow window with the rail gets a truthful close affordance instead of
// a pane whose X vanished. The close has no Tooltip: a keyboard focus that
// raised a tooltip would take the first Escape (mygo tooltips are overlays
// with their own Escape handler), which would then leave the panel open.
func (r *Root) dockedInspectorHeader(c *ui.Context, t *design.Tokens) {
	width, _ := c.Size()
	if width <= 0 || width >= inspectorPaneHeaderMinWidth {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(0, 0, design.Space2, 0).Children(func() {
		design.Apply(ui.Text(c, r.tr("检查器", "Inspector")), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
		closeLabel := r.tr("关闭检查器", "Close inspector")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:   "chrome.inspector.docked.close",
			Label: closeLabel,
			// No Tooltip here (see the function comment): a keyboard-raised
			// tooltip would take the first Escape.
			Icon:    icons.Close(),
			Size:    design.IconSizeS,
			OnClick: func() { r.setRightPanelOpen(false, true) },
		})
	})
}

// contentHeader is the window's B3 top band: the current screen's header,
// action and tab contributions on one quiet surface, with the host's palette and
// inspector controls at its end. A screen with no contribution gets no top band
// at all (and with it, no palette/inspector row: those controls belong to the
// screens that carry chrome). Navigation has no back control and no Alt+Left
// chord: the sidebar destinations and the command palette are the navigation.
func (r *Root) contentHeader(c *ui.Context) {
	if !r.hasScreenBandSlots() {
		return
	}
	t := design.Of(c)
	ui.Column(c).FillWidth().Background(t.BgSidebar).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).
		// The window's top bar is the banner landmark (mygo RoleBanner), as
		// HTML's header: the screen title and the host's chrome controls in one
		// landmark a screen reader can jump to.
		Role(ui.RoleBanner).Label(r.tr("顶栏", "Top bar")).
		Children(func() {
			r.screenHeaderRow(c, t)
			if r.hasScreenSlot(contracts.SlotScreenTabs) {
				// The tabs row rides directly under the header row: one surface, one
				// bottom border, no line between the two rows.
				ui.Row(c).FillWidth().Height(design.Space8).PaddingX(design.Space5).
					AlignItems(ui.Stretch).Children(func() {
					r.renderScreenSlot(c, contracts.SlotScreenTabs)
				})
			}
		})
}

// screenHeaderRow draws the band's 40 DIP row: the screen's title area (it takes
// the remaining width and ellipsizes), its actions, and the host's palette and
// inspector controls. The row is window chrome, so its title uses the TypeM
// step (16 DIP) rather than the page-title TypeL the retired in-body header
// used; that keeps the band in proportion with the 13 DIP tabs, 12 DIP badges
// and 16 DIP icons next to it.
func (r *Root) screenHeaderRow(c *ui.Context, t *design.Tokens) {
	ui.Row(c).FillWidth().Height(design.Space8 + design.Space2).
		PaddingX(design.Space5).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidthPercent(0).Justify(ui.Center).Children(func() {
			r.renderScreenSlot(c, contracts.SlotScreenHeader)
		})
		r.renderScreenSlot(c, contracts.SlotScreenActions)
		r.paletteButton(c)
		r.inspectorButton(c)
	})
}

// paletteButton is the host command-palette entry of the top band. The Native
// icon button keeps the official outlined look and the Cmd/Ctrl+K chord while
// converging on the one icon-button entry (2026-11 audit UD-09).
func (r *Root) paletteButton(c *ui.Context) {
	paletteLabel := r.tr("快捷操作", "Quick actions")
	uikit.IconButton(c, uikit.IconButtonOptions{
		Label:   paletteLabel,
		Tooltip: paletteLabel,
		Icon:    icons.Search(),
		Size:    design.IconSizeS,
		Variant: uikit.IconButtonNative,
		OnClick: r.togglePalette,
	}).KeyShortcuts(ui.Cmd, ui.KeyK)
}

// inspectorButton is the host inspector toggle of the top band, shown only on
// the screen that contributes the inspector's pane.
func (r *Root) inspectorButton(c *ui.Context) {
	if !r.inspectorAvailable() {
		return
	}
	label := r.tr("打开右侧栏", "Open inspector")
	if r.rightOpen {
		label = r.tr("收起右侧栏", "Close inspector")
	}
	uikit.IconButton(c, uikit.IconButtonOptions{
		Label:   label,
		Tooltip: label,
		Icon:    icons.PanelRight(),
		Size:    design.IconSizeS,
		Variant: uikit.IconButtonNative,
		OnClick: func() { r.setRightPanelOpen(!r.rightOpen, false) },
	}).KeyShortcuts(ui.Cmd|ui.Alt, ui.KeyI).Bind(&r.inspectorToggle)
	if r.inspectorFocusPending {
		// The close that removed the focused control (drawer, or the narrow
		// docked header's X) asks for the keyboard back on the toggle that opens
		// the inspector, as a dialog returns it.
		r.inspectorFocusPending = false
		r.inspectorToggle.Focus(c)
	}
}

// hasScreenBandSlots reports whether the current screen contributes to any slot
// of the B3 band.
func (r *Root) hasScreenBandSlots() bool {
	return r.hasScreenSlot(contracts.SlotScreenHeader, contracts.SlotScreenActions, contracts.SlotScreenTabs)
}

// hasScreenSlot reports whether at least one of the named slots has a
// contribution that belongs to the current screen.
func (r *Root) hasScreenSlot(names ...string) bool {
	for _, name := range names {
		for _, item := range r.rt.slotItems(name) {
			if r.screenSlotApplies(item.ID) {
				return true
			}
		}
	}
	return false
}

// renderScreenSlot builds the current screen's contributions of one screen-level
// slot. Contributions are filtered by their ID's screen namespace (see
// screenSlotApplies), so a module's header does not appear on another screen.
func (r *Root) renderScreenSlot(c *ui.Context, name string) {
	for _, item := range r.rt.slotItems(name) {
		if !r.screenSlotApplies(item.ID) {
			continue
		}
		r.safeBuild(c, item.Build)
	}
}

// screenSlotApplies reports whether one screen-level slot contribution belongs
// to the current screen. A contribution whose item ID starts with a registered
// screen ID scopes to that screen ("session.title" shows on the "session" screen
// only); every other ID is a host/global contribution (the shell tests and
// legacy contributors use those). Screen-level slots are global registrations,
// so this convention is what keeps the conversation's header/tabs off Home,
// Automation and Plugins.
func (r *Root) screenSlotApplies(itemID string) bool {
	index := strings.IndexByte(itemID, '.')
	if index <= 0 {
		return true
	}
	prefix := itemID[:index]
	if prefix == r.nav.Current() {
		return true
	}
	for _, spec := range visibleScreens(r.rt.screenSpecs()) {
		if spec.ID == prefix {
			return false
		}
	}
	return true
}

// inspectorDrawer shows the inspector as a modal drawer over the content when
// the window is too narrow to dock it. It follows mygo's drawer recipe
// (docs/ui/overlays.md "A drawer on the right"): the backdrop is a modal Row,
// the panel is a named RoleDialog, a press outside or Escape closes it, and the
// focus returns to what had it when it goes.
func (r *Root) inspectorDrawer(c *ui.Context) {
	if !r.rightOpen || !r.inspectorAvailable() || !r.rightPanelDrawerOpen(c) {
		return
	}
	r.inspectorDrawerShown = true
	t := design.Of(c)
	ui.Overlay(c, func() {
		back := ui.Row(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
			Justify(ui.End).Background(t.Scrim()).Modal()
		back.Children(func() {
			panel := design.Apply(ui.Column(c).Width(design.InspectorWidth).FillHeight(),
				t.ShadowOverlay()).
				Background(t.BgRaised).BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL3).
				Padding(design.Space4).
				Role(ui.RoleDialog).Label(r.tr("检查器", "Inspector"))
			panel.Children(func() {
				// Inside the dialog the inspector content is the same
				// complementary landmark the docked pane is (the drawer is the
				// narrow window's form of the right sidebar), so the landmark
				// list reads the same on both layouts.
				ui.Column(c).FillWidth().FillHeight().
					Role(ui.RoleComplementary).Label(r.tr("右侧栏", "Right sidebar")).
					Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
							design.Apply(ui.Text(c, r.tr("检查器", "Inspector")), t.Font(design.TypeS)).
								FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
							// No Tooltip: a keyboard-focused tooltip is an overlay with its
							// own Escape handler (mygo tooltip.go), and it would take the
							// first Escape before this drawer's OverlayShortcut could close
							// it. Label keeps the button named for assistive technology.
							closeLabel := r.tr("关闭右侧栏", "Close inspector")
							uikit.IconButton(c, uikit.IconButtonOptions{
								Label:   closeLabel,
								Icon:    icons.Close(),
								Size:    design.IconSizeS,
								Variant: uikit.IconButtonNative,
								OnClick: func() { r.setRightPanelOpen(false, true) },
							})
						})
						r.slot(c, contracts.SlotSessionPanelRight)
					})
			})
			if panel.PressedOutside() || back.OverlayShortcut(0, ui.KeyEscape) {
				r.setRightPanelOpen(false, true)
			}
		})
	})
}

// hasAnySlot reports whether at least one of the named slots currently has a
// registered contribution.
func (r *Root) hasAnySlot(names ...string) bool {
	registered := r.rt.Slots()
	for _, name := range names {
		for _, candidate := range registered {
			if candidate == name {
				return true
			}
		}
	}
	return false
}

// palette is the host command palette: a native Modal with a search field and a
// native List of the registered commands. Up/Down move the choice, Enter runs
// it, Escape closes it — the promise the old hand-built row list only printed.
func (r *Root) palette(c *ui.Context) {
	if !r.paletteOpen {
		// A closed palette forgets that it drew: the next opening scrolls the
		// list back to the first row (paletteShown, below).
		r.paletteShown = false
		return
	}
	t := design.Of(c)
	ui.Modal(c, &r.paletteOpen, func() {
		ui.Column(c).Width(design.DialogWidthM).Gap(design.Space2).Padding(design.Space4).Children(func() {
			design.Apply(ui.Text(c, r.tr("命令面板", "Command palette")), t.Font(design.TypeM)).FontWeight(600).
				TextColor(t.LabelPrimary)
			// The palette's search row is built from a TextInput rather than
			// SearchField: one Escape must close the palette, as its footer
			// promises, and SearchField's own Escape clears the query first
			// (mygo docs/ui/search-field.md). OnTextKey exists only on the
			// editable node, so the magnifier and the field are composed here
			// around the input the palette can intercept (R14).
			var search ui.Element
			design.Apply(ui.Row(c), t.Panel()).FillWidth().Height(design.ControlHeight).
				AlignItems(ui.Center).Gap(design.Space2).
				Padding(design.Space2, design.Space3).Children(func() {
				ui.Icon(c, icons.Search()).Size(design.Space4, design.Space4).TextColor(t.LabelTertiary)
				search = ui.TextInput(c, &r.paletteQuery).
					Label(r.tr("搜索命令", "Search commands")).
					Placeholder(r.tr("搜索命令…", "Search commands…")).
					Background(ui.Transparent).Border(0, ui.Transparent).Grow(1)
				search.OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEscape || k.Mods != 0 {
						return false
					}
					r.closePalette()
					return true
				})
			})
			// Arrow keys move the choice while the search field keeps the
			// keyboard, as in a desktop command palette; Enter and Escape are
			// the field's and the modal's.
			c.OnShortcut(0, ui.KeyDown, func() { r.movePaletteSelection(1) })
			c.OnShortcut(0, ui.KeyUp, func() { r.movePaletteSelection(-1) })
			needle := strings.ToLower(strings.TrimSpace(r.paletteQuery))
			if r.paletteFilter != needle {
				r.paletteFilter = needle
				// A new filter starts from the first visible match. Choose tells
				// the native List that the app chose the row: writing *Selected
				// alone loses to the List's Key memory, which keeps the command
				// chosen in the previous frame chosen and moves the index with
				// its item as the filtered rows reorder (mygo list.go Choose;
				// the same key memory that produced the SH-02 invisible target).
				// Selected is set before the first list build; Choose does
				// nothing without it.
				r.paletteList.Selected = &r.paletteSelected
				r.paletteList.Choose(0)
				// The first match must also be the row the viewport shows: the
				// List keeps the previous query's place, which can leave the new
				// first row above the visible range.
				r.paletteList.ScrollTo(0, ui.Start)
				r.paletteReveal = 0
			}
			matches := r.paletteCommands(needle)
			if len(matches) == 0 {
				// No match: the arrows must not keep moving a choice of the
				// previous query (the stale count also scheduled frames for
				// nothing, fix round SH-08).
				r.paletteCount = 0
				design.Apply(ui.Text(c, r.tr("没有匹配的命令", "No matching commands")), t.TextMuted(design.TypeXS))
				// Instruction copy, not a kbd-only micro-label: the metadata step
				// keeps it out of TypeXXXS's 11 DIP size (DESIGN.md §5.3).
				design.Apply(ui.Text(c, r.tr("Esc 关闭", "Esc to close")), t.TextMuted(design.TypeXXS))
				return
			}
			entries, rows := r.paletteEntries(matches)
			r.paletteCount = len(matches)
			// Keep the fallback clamp (Enabled changes can shrink the rows
			// without any filter change), but make it an app choice through
			// Choose as well: the stale Key memory must not move the choice to
			// another command's row while the index is only being repaired.
			r.paletteList.Selected = &r.paletteSelected
			if r.paletteSelected >= len(matches) {
				r.paletteList.Choose(len(matches) - 1)
			} else if r.paletteSelected < 0 {
				r.paletteList.Choose(0)
			}
			// The first frame of an opening shows the list from the top: a
			// reopened palette reuses the ListState and would otherwise keep the
			// previous opening's scroll place, leaving the chosen first row off
			// screen while Enter still ran it (the SH-02 class of invisible
			// target).
			if !r.paletteShown {
				r.paletteShown = true
				r.paletteList.ScrollTo(0, ui.Start)
				r.paletteReveal = r.paletteSelected
			}
			// The chosen row follows the keyboard: when it moved since the last
			// layout, scroll it into view. The request is set before the List
			// builds so this frame measures the rows around it, and the painted
			// target is always the row Enter runs.
			reveal := rows[r.paletteSelected]
			if r.paletteReveal != r.paletteSelected {
				r.paletteList.ScrollIntoView(reveal)
				r.paletteReveal = r.paletteSelected
			}
			// The List's Selected is a row of this frame's grouped rows; the app
			// keeps the command ordinal, so the two are translated through rows
			// (the group headings occupy rows of their own).
			r.paletteList.Selected = &reveal
			r.paletteList.Key = func(row int) any { return entries[row].key }
			r.paletteList.Label = func(row int) string { return entries[row].title }
			r.paletteList.Header = func(row int) bool { return entries[row].header }

			// The search field keeps the keyboard focus while the list shows the
			// choice; ActiveDescendant points assistive technology at the row
			// the arrows are on (mygo accessibility.md: a list box a text field
			// drives with ActiveDescendant).
			var activeRow ui.Element
			list := ui.List(c, &r.paletteList, len(entries)).
				Label(r.tr("命令", "Commands")).Rows(func(row ui.ListRow) {
				entry := entries[row.Index]
				if entry.header {
					// A group heading, not a command: the List marks it as a
					// header, so the pointer and the keys skip it and it pins to
					// the top while its group scrolls under it. The opaque fill
					// is the modal panel's own color, so pinned headings cover
					// the rows scrolling beneath instead of stacking on them.
					ui.Row(row.Context).FillWidth().Background(t.BgBase).
						Padding(design.Space1, design.Space1).Children(func() {
						design.Apply(ui.Text(row.Context, entry.title), t.SectionLabel())
					})
					return
				}
				cmd := entry.cmd
				chosen := row.Selected()
				// The row is a native button so a single click runs the command,
				// as in a desktop palette; the List around it still owns Up/Down
				// and Enter and shows the chosen row.
				item := ui.ButtonBase(row.Context).FillWidth().AlignItems(ui.Center).
					Padding(design.Space2, design.Space3).Radius(design.RadiusSM).
					Label(cmd.Title)
				// The native list paints the chosen row in the accent fill, so
				// the row text must switch to the on-accent foreground; only
				// hovering adds a tint of our own.
				titleColor, hintColor := t.LabelPrimary, t.LabelTertiary
				if chosen {
					titleColor, hintColor = t.AccentText, t.AccentText
					// The row's own fill, in addition to the native List's
					// Selected paint: the real desktop assembly can build the
					// palette rows during a later layout pass, where the List's
					// chosen fill did not reach the painted frame (fix round
					// SH-02: Enter ran a row the user could not see).
					item = item.Background(t.Accent)
					activeRow = item
				} else if item.Hovered() {
					item = item.Background(t.BgHover)
				}
				item.Children(func() {
					ui.Row(row.Context).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						design.Apply(ui.Text(row.Context, cmd.Title), t.Font(design.TypeXS)).FontWeight(600).
							TextColor(titleColor).Grow(1).MaxLines(1).Ellipsis("…")
						if hint := strings.TrimSpace(cmd.Hint); hint != "" {
							design.Apply(ui.Text(row.Context, hint), t.Font(design.TypeXXXS)).
								TextColor(hintColor).MaxLines(1).Ellipsis("…").MaxWidth(240)
						}
						shortcut := strings.TrimSpace(cmd.Shortcut)
						if shortcut == "" {
							shortcut = r.commandShortcut(cmd.ID)
						}
						if shortcut != "" {
							design.Apply(ui.Text(row.Context, shortcut), t.Font(design.TypeXXXS)).
								TextColor(hintColor).Shrink(0)
						}
					})
				})
				if item.Clicked() {
					r.runPaletteCommand(cmd)
				}
			}).MaxHeight(320)
			// Reconcile after the List built. A List follows the command it
			// chose by Key and drops it when its item left the list (mygo
			// list.md): an Enabled change can reorder the rows between frames
			// without a filter change, and the List writes the painted row back
			// into reveal, or -1 when the command is gone. Keep the app ordinal
			// on the painted row so Enter runs what the user sees (the SH-02
			// class of invisible target), and repair a dropped choice with a
			// fresh Choose so some row is always painted.
			switch {
			case reveal < 0 || reveal >= len(entries) || entries[reveal].header:
				r.paletteList.Selected = &r.paletteSelected
				r.paletteSelected = 0
				r.paletteList.Choose(0)
				c.Invalidate()
			case entries[reveal].ordinal != r.paletteSelected:
				r.paletteSelected = entries[reveal].ordinal
			}
			if activeRow.Valid() {
				search.ActiveDescendant(activeRow)
			}
			if search.Submitted() || list.Submitted() {
				if r.paletteSelected >= 0 && r.paletteSelected < len(matches) {
					r.runPaletteCommand(matches[r.paletteSelected])
				}
			}
			// Instruction copy, not a kbd-only micro-label: the metadata step
			// keeps the legend out of TypeXXXS's 11 DIP size (DESIGN.md §5.3,
			// audit UX-14 — the no-match hint above already follows the rule).
			design.Apply(ui.Text(c, r.tr("↑↓ 选择 · Enter 运行 · Esc 关闭",
				"Up/Down to choose · Enter to run · Esc to close")),
				t.TextMuted(design.TypeXXS))
		})
	})
}

// paletteEntry is one row of the palette's List: a group heading or one
// command. Headings are List headers — skipped by the choice and pinned while
// their group scrolls — so the List's row index differs from the command
// ordinal the palette tracks (paletteSelected).
type paletteEntry struct {
	title   string
	key     string
	header  bool
	ordinal int
	cmd     contracts.CommandSpec
}

// paletteCommands returns the commands the palette offers for one query: the
// registered command surface plus the host-generated navigation ("前往 …") and
// settings ("设置：…") commands, filtered by the query and deduplicated by
// title. Disabled commands are dropped; the registry's order is preserved
// inside a category.
func (r *Root) paletteCommands(needle string) []contracts.CommandSpec {
	all := r.paletteCommandList()
	matches := make([]contracts.CommandSpec, 0, len(all))
	seen := make(map[string]bool, len(all))
	for _, cmd := range all {
		if cmd.Enabled != nil && !cmd.Enabled() {
			continue
		}
		if !paletteCommandMatches(cmd, needle) {
			continue
		}
		// Two modules can register one visible command (the old
		// "设置"/"打开设置" pair); the palette shows the action once.
		if seen[cmd.Title] {
			continue
		}
		seen[cmd.Title] = true
		matches = append(matches, cmd)
	}
	return matches
}

// paletteCommandList returns every palette command before filtering. The
// registered command surface is the actions group; the navigation and settings
// groups are derived from the registries each frame, so a screen or settings
// page that is unregistered (or hidden by its Visible hook) loses its palette
// command with it.
func (r *Root) paletteCommandList() []contracts.CommandSpec {
	registered := r.rt.commandSpecs()
	all := make([]contracts.CommandSpec, 0, len(registered)+16)
	all = append(all, registered...)
	for _, spec := range visibleScreens(r.rt.screenSpecs()) {
		spec := spec
		// Home is not a palette destination, as it is not a sidebar one
		// (TestSidebarHasNoHomeDestination): the conversation is the product's
		// one landing surface, reached through the new-chat action. A second
		// "前往 首页" entry would keep the low-weight Home alive beside it
		// (2026-11 audit UX-20, lead ruling: consistency with the sidebar).
		if spec.ID == "home" {
			continue
		}
		title := r.screenLabel(spec)
		all = append(all, contracts.CommandSpec{
			ID:       "uiroot.goto." + spec.ID,
			Title:    r.tr("前往 ", "Go to ") + title,
			Category: contracts.CommandCategoryGoTo,
			// The screen id and title are search aliases: users type the
			// destination's name, not the generated command's ID.
			Keywords: []string{spec.ID, title},
			Run:      func() error { return r.nav.Push(spec.ID) },
		})
	}
	for _, page := range flattenSettingsGroups(groupSettingsPages(visiblePages(r.rt.settingsPages()))) {
		page := page
		keywords := append([]string{page.Title, r.settingsSectionTitle(page.Section)},
			settingsPageSearchTerms(page)...)
		all = append(all, contracts.CommandSpec{
			ID:       "uiroot.settings." + page.ID,
			Title:    r.tr("设置：", "Settings: ") + page.Title,
			Category: contracts.CommandCategorySettings,
			// The page's own search terms (its module declares them) make
			// "主题"/"theme" reach the page with the same words the settings
			// navigation's search accepts.
			Keywords: keywords,
			Run:      func() error { return r.nav.OpenSettingsPage(page.ID) },
		})
	}
	return all
}

// paletteCommandMatches reports whether cmd matches the lower-case needle on
// its title, ID, hint or keywords.
func paletteCommandMatches(cmd contracts.CommandSpec, needle string) bool {
	if needle == "" {
		return true
	}
	haystack := strings.ToLower(cmd.Title + "\x00" + cmd.ID + "\x00" + cmd.Hint)
	for _, keyword := range cmd.Keywords {
		haystack += "\x00" + strings.ToLower(keyword)
	}
	return strings.Contains(haystack, needle)
}

// paletteCategoryOf normalizes a command's category to one the palette shows:
// the goto and settings categories keep their heading, and an unknown or empty
// category joins the actions group so a module that sets something new still
// renders its command.
func paletteCategoryOf(cmd contracts.CommandSpec) string {
	switch cmd.Category {
	case contracts.CommandCategoryGoTo, contracts.CommandCategorySettings:
		return cmd.Category
	default:
		return contracts.CommandCategoryActions
	}
}

// paletteEntries lays the matching commands out in the palette's row order: the
// actions, navigation and settings groups, each under its heading. rows maps a
// command ordinal (the index in matches) to its List row.
func (r *Root) paletteEntries(matches []contracts.CommandSpec) (entries []paletteEntry, rows []int) {
	groups := []struct{ id, label string }{
		{contracts.CommandCategoryActions, r.tr("操作", "Actions")},
		{contracts.CommandCategoryGoTo, r.tr("前往", "Go to")},
		{contracts.CommandCategorySettings, r.tr("设置", "Settings")},
	}
	counts := make(map[string]int, len(groups))
	categories := make([]string, len(matches))
	for ordinal, cmd := range matches {
		categories[ordinal] = paletteCategoryOf(cmd)
		counts[categories[ordinal]]++
	}
	entries = make([]paletteEntry, 0, len(matches)+len(groups))
	rows = make([]int, len(matches))
	for _, group := range groups {
		if counts[group.id] == 0 {
			continue
		}
		entries = append(entries, paletteEntry{
			title:   group.label,
			key:     "header:" + group.id,
			header:  true,
			ordinal: -1,
		})
		for ordinal, cmd := range matches {
			if categories[ordinal] != group.id {
				continue
			}
			rows[ordinal] = len(entries)
			entries = append(entries, paletteEntry{
				title:   cmd.Title,
				key:     "command:" + cmd.ID,
				ordinal: ordinal,
				cmd:     cmd,
			})
		}
	}
	return entries, rows
}

// movePaletteSelection moves the palette's choice while the search field has
// the keyboard focus. The next frame scrolls the choice into view (palette's
// paletteReveal check), so the painted target is always the row Enter runs.
func (r *Root) movePaletteSelection(delta int) {
	if !r.paletteOpen || r.paletteCount == 0 {
		return
	}
	r.paletteSelected = (r.paletteSelected + delta + r.paletteCount) % r.paletteCount
	r.requestFrame()
}

// runPaletteCommand closes the palette and runs one command.
func (r *Root) runPaletteCommand(cmd contracts.CommandSpec) {
	r.paletteOpen = false
	r.paletteQuery = ""
	r.paletteFilter = ""
	r.paletteSelected = 0
	r.paletteCount = 0
	if cmd.Run != nil {
		// A palette action that fails (for example the update command with no
		// downloaded plan) must not vanish silently: surface it as an error
		// toast so the user knows nothing happened and why.
		if err := runSafelyErr(cmd.Run); err != nil {
			if r.notifier != nil {
				r.notifier.Notify(contracts.NotifyError,
					r.tr("命令执行失败：", "Command failed: ")+err.Error())
			}
		}
	}
	r.requestFrame()
}

// overlays renders the settings surface and any module-declared overlay slots.
func (r *Root) overlays(c *ui.Context) {
	if r.nav.SettingsOpen() {
		open := true
		// Name the dialog for assistive technology: the modal is a dialog of
		// navigation + page, and a screen reader announces its name on open.
		ui.Modal(c, &open, func() { r.settings(c, &open) }).Label(r.tr("设置", "Settings"))
		if !open {
			// A dirty page keeps the surface open and asks once; a clean page
			// (including one the user saved while the bar was showing) closes.
			if r.settingsHasUnsaved() {
				r.settingsDiscard = true
				r.requestFrame()
			} else {
				r.settingsDiscard = false
				r.settingsQuery = ""
				r.nav.CloseSettings()
			}
		}
	}
	for _, name := range r.rt.slotNames(contracts.SlotOverlayPrefix) {
		items := r.rt.slotItems(name)
		if len(items) == 0 {
			continue
		}
		ui.Overlay(c, func() {
			for _, item := range items {
				r.safeBuild(c, item.Build)
			}
		})
	}
}

// settingsHasUnsaved reports whether any settings page holds unsaved edits.
// The host uses it when the modal's close path (X, Escape, backdrop) fires and
// for the header's "unsaved" state, so a draft is never discarded silently even
// when the user switched to another page in between.
func (r *Root) settingsHasUnsaved() bool {
	for _, page := range flattenSettingsGroups(groupSettingsPages(visiblePages(r.rt.settingsPages()))) {
		if page.Dirty != nil && page.Dirty() {
			return true
		}
	}
	return false
}

// discardSettingsDrafts throws away every page's unsaved drafts through the
// page-provided Discard hook, so reopening settings does not show the abandoned
// edits.
func (r *Root) discardSettingsDrafts() {
	for _, page := range flattenSettingsGroups(groupSettingsPages(visiblePages(r.rt.settingsPages()))) {
		if page.Dirty != nil && page.Dirty() && page.Discard != nil {
			page.Discard()
		}
	}
}

// rightPanelDrawerOpen reports whether the inspector is open as a drawer (the
// window is too narrow to dock it beside the content even with the effective
// sidebar width). Mirror of rightPanel's sizing decision, used to reserve the
// drawer's width in the content column. The decision reads
// effectiveSidebarWidth so a window that gets the temporary rail docks instead.
func (r *Root) rightPanelDrawerOpen(c *ui.Context) bool {
	if !r.rightOpen || !r.inspectorAvailable() {
		return false
	}
	return r.inspectorDrawerAt(c, r.effectiveSidebarWidth(c))
}

// commandShortcut adds the host-known chord for commands whose modules do not
// carry a Shortcut field (the palette shows it so the keyboard path is
// learnable, the usability audit's H26).
func (r *Root) commandShortcut(id string) string {
	switch id {
	case "zeokeel.session.new":
		return "Cmd/Ctrl + N"
	case "home.settings", "zeokeel.session.settings":
		return "Cmd/Ctrl + ,"
	case "uiroot.toggle-inspector":
		return "Cmd/Ctrl + Alt + I"
	default:
		return ""
	}
}

// settingsGroup is one section of the settings navigation with its pages in
// registry order.
type settingsGroup struct {
	section string
	pages   []contracts.SettingsPageSpec
}

// settingsSectionRank orders the nav groups: the daily settings first, the
// advanced ones after them, About last. Unknown sections sit between.
var settingsSectionRank = map[string]int{
	"general": 10, // host tests and legacy contributors
	"常用":      12,
	"模型":      20, // legacy contributors
	"界面":      30,
	"外观":      30, // legacy contributors
	"键盘":      40, // legacy contributors
	"连接":      50,
	"集成":      50, // legacy contributors
	"数据":      70,
	"数据与通知":   70, // legacy contributors
	"高级":      80,
	"关于":      90,
}

// settingsPageMatches reports whether a settings page matches the navigation
// search needle. It matches the page title, its section's localized title, the
// keywords the page's own module declared, and the labels/keywords of the
// page's searchable items: users search for the setting's name ("密钥",
// "主题", "字号"), which is rarely the page title (report R2). The terms come
// from the module through contracts.SettingsPageSpec, so the host keeps no alias
// table that can drift away from the page.
func (r *Root) settingsPageMatches(page contracts.SettingsPageSpec, needle string) bool {
	haystack := []string{
		strings.ToLower(page.Title),
		strings.ToLower(r.settingsSectionTitle(page.Section)),
	}
	haystack = append(haystack, settingsPageSearchTerms(page)...)
	for _, text := range haystack {
		if text != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// settingsPageSearchTerms returns everything a page's module declared for the
// navigation search: the page's own keywords plus each searchable item's label
// and keywords, lower-cased.
func settingsPageSearchTerms(page contracts.SettingsPageSpec) []string {
	terms := make([]string, 0, len(page.Keywords)+3*len(page.SearchItems))
	for _, keyword := range page.Keywords {
		if keyword != "" {
			terms = append(terms, strings.ToLower(keyword))
		}
	}
	for _, item := range page.SearchItems {
		if item.Label != "" {
			terms = append(terms, strings.ToLower(item.Label))
		}
		for _, keyword := range item.Keywords {
			if keyword != "" {
				terms = append(terms, strings.ToLower(keyword))
			}
		}
	}
	return terms
}

// settingsSearchHit finds the first searchable item the needle matches, in the
// pages' display order: that page is selected and its Reveal hook is asked for
// the item's anchor key, so a search for "压缩" lands on the context-budget
// field instead of only the model page. ok=false covers a page-level match (the
// page's title, section or own keywords) and no match at all.
func settingsSearchHit(pages []contracts.SettingsPageSpec, needle string) (
	page contracts.SettingsPageSpec, item contracts.SettingsSearchItem, ok bool,
) {
	if needle == "" {
		return contracts.SettingsPageSpec{}, contracts.SettingsSearchItem{}, false
	}
	for _, candidate := range pages {
		for _, searchItem := range candidate.SearchItems {
			if settingsSearchItemMatches(searchItem, needle) {
				return candidate, searchItem, true
			}
		}
	}
	return contracts.SettingsPageSpec{}, contracts.SettingsSearchItem{}, false
}

// settingsSearchItemMatches reports whether one searchable item matches the
// needle on its label or one of its keywords.
func settingsSearchItemMatches(item contracts.SettingsSearchItem, needle string) bool {
	if needle == "" {
		return false
	}
	if strings.Contains(strings.ToLower(item.Label), needle) {
		return true
	}
	for _, keyword := range item.Keywords {
		if keyword != "" && strings.Contains(strings.ToLower(keyword), needle) {
			return true
		}
	}
	return false
}

// settingsSectionTitle localizes a page section name for the navigation.
func (r *Root) settingsSectionTitle(section string) string {
	switch section {
	case "general":
		return r.tr("通用", "General")
	case "常用":
		return r.tr("常用", "Common")
	case "模型":
		return r.tr("模型与对话", "Models & chat")
	case "界面":
		return r.tr("界面与通知", "Interface & notifications")
	case "外观":
		return r.tr("外观与语言", "Appearance & language")
	case "键盘":
		return r.tr("键盘", "Keyboard")
	case "连接":
		return r.tr("连接", "Connections")
	case "集成":
		return r.tr("集成", "Integrations")
	case "数据":
		return r.tr("数据", "Data")
	case "高级":
		return r.tr("高级", "Advanced")
	case "关于":
		return r.tr("关于", "About")
	default:
		return section
	}
}

// groupSettingsPages groups the registered pages by section, keeping the
// registry order inside a section and ordering the groups by rank. The native
// settings sidebar then reads as a grouped navigation instead of a flat dump of
// every registered page.
func groupSettingsPages(pages []contracts.SettingsPageSpec) []settingsGroup {
	groups := make([]settingsGroup, 0, 6)
	index := make(map[string]int, 6)
	for _, page := range pages {
		if at, ok := index[page.Section]; ok {
			groups[at].pages = append(groups[at].pages, page)
			continue
		}
		index[page.Section] = len(groups)
		groups = append(groups, settingsGroup{
			section: page.Section,
			pages:   []contracts.SettingsPageSpec{page},
		})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		ri, rj := settingsSectionRank[groups[i].section], settingsSectionRank[groups[j].section]
		if ri == 0 {
			ri = 70
		}
		if rj == 0 {
			rj = 70
		}
		if ri != rj {
			return ri < rj
		}
		return groups[i].section < groups[j].section
	})
	return groups
}

// settingsTitleRow is the settings surface header: a quiet product-level label,
// the selected page's unsaved state and the close control. Page modules own the
// real page title (their PageHeader); the host label here is deliberately muted
// so the settings surface does not stack two loud titles.
func (r *Root) settingsTitleRow(c *ui.Context, t *design.Tokens, open *bool, dirty bool) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		// The surface is already titled by the page's H1 and the selected
		// navigation item; a muted "settings" kicker repeated the same word a
		// third time (V26).
		if dirty {
			design.Apply(ui.Badge(c, r.tr("未保存", "Unsaved")), t.Badge()).
				TextColor(t.WarningText)
		}
		ui.Spacer(c).Grow(1)
		closeLabel := r.tr("关闭设置", "Close settings")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "chrome.settings.close",
			Label:   closeLabel,
			Tooltip: closeLabel,
			Icon:    icons.Close(),
			Size:    design.IconSizeS,
			OnClick: func() { *open = false },
		})
	})
}

// settingsDiscardBar is the host's close guard: closing the settings surface
// while the selected page has unsaved edits keeps the surface open and asks
// once, instead of silently discarding the edit.
func (r *Root) settingsDiscardBar(c *ui.Context, t *design.Tokens) {
	design.Apply(ui.Row(c), t.Panel()).FillWidth().AlignItems(ui.Center).Gap(design.Space3).
		Padding(design.Space2, design.Space3).Children(func() {
		design.Apply(ui.Text(c, r.tr("有未保存的修改", "You have unsaved changes")),
			t.Font(design.TypeXS)).TextColor(t.LabelPrimary).Grow(1)
		keep := ui.Button(c, r.tr("继续编辑", "Keep editing"))
		if keep.Clicked() {
			r.settingsDiscard = false
			r.requestFrame()
		}
		discard := design.Apply(ui.Button(c, r.tr("放弃修改", "Discard")), t.DangerButton())
		if discard.Clicked() {
			r.discardSettingsDrafts()
			r.settingsDiscard = false
			r.settingsQuery = ""
			r.nav.CloseSettings()
			r.requestFrame()
		}
	})
}

// settingsNavWidth is the fixed settings navigation column. It grew from 200
// to 224 for the merged five-group navigation: the longer page names
// (网络与搜索, 行为与限额) must not truncate while the page body keeps a useful
// measure.
const settingsNavWidth = 224

// settingsNavColumn renders the grouped settings navigation with its filter
// field. The host owns the navigation; pages only contribute their specs.
func (r *Root) settingsNavColumn(c *ui.Context, t *design.Tokens, selected *string, groups []settingsGroup) {
	searching := strings.TrimSpace(r.settingsQuery) != ""
	matchCount := 0
	for _, group := range groups {
		matchCount += len(group.pages)
	}
	ui.Column(c).Width(settingsNavWidth).FillHeight().Children(func() {
		ui.Column(c).FillWidth().Shrink(0).Gap(design.Space1).
			Padding(design.Space3, design.Space3, design.Space2, design.Space3).Children(func() {
			ui.SearchField(c, &r.settingsQuery).
				Label(r.tr("搜索设置", "Search settings")).
				Placeholder(r.tr("搜索设置…", "Search settings…")).FillWidth()
			// The search reports how many pages match instead of filtering
			// silently (settings IA review P1-6, audit UX-16): a count under
			// the field says whether the query landed, including zero.
			if searching {
				design.Apply(ui.Text(c, strconv.Itoa(matchCount)+r.tr(" 个匹配", " matches")),
					t.TextMuted(design.TypeXXS)).MaxLines(1)
			}
		})
		// The navigation scrolls under this hairline: without it a partially
		// scrolled section title (常用) sat clipped against the search field
		// with no boundary saying the list had moved.
		ui.Column(c).FillWidth().PaddingX(design.Space3).Children(func() {
			ui.Divider(c)
		})
		ui.Sidebar(c, selected, func() {
			var scrollTarget ui.Element
			addPages := func(pages []contracts.SettingsPageSpec) {
				for _, page := range pages {
					item := ui.SidebarItem(c, page.ID, nil, page.Title)
					// A page holding an unsaved draft carries a small warning
					// dot on its nav row, named "未保存" for assistive
					// technology, so the user sees WHICH page has the draft
					// (the title row's global badge alone did not say; settings
					// IA review P0-5, audit UX-16).
					if page.Dirty != nil && page.Dirty() {
						item.Children(func() {
							ui.Box(c).Size(design.Space2, design.Space2).Shrink(0).
								Radius(design.RadiusMax).Background(t.Warning).
								Label(r.tr("未保存", "Unsaved"))
						})
					}
					if page.ID == *selected {
						scrollTarget = item
					}
				}
			}
			for _, group := range groups {
				group := group
				title := r.settingsSectionTitle(group.section)
				// A group holding one page whose title repeats the section title
				// (About) renders without a redundant header.
				if len(group.pages) == 1 && group.pages[0].Title == title {
					addPages(group.pages)
					continue
				}
				// The 高级 group is a real native collapsible section, closed by
				// default so the daily pages dominate the navigation. Search and
				// deep links force it open.
				var open *bool
				if group.section == "高级" && !searching {
					open = &r.settingsAdvancedOpen
				}
				ui.SidebarSection(c, title, open, func() { addPages(group.pages) })
			}
			if r.settingsScrollToSelected {
				r.settingsScrollToSelected = false
				if scrollTarget.Valid() {
					scrollTarget.ScrollIntoView()
				}
			}
			r.slot(c, contracts.SlotSettingsSidebar)
		}).Label(r.tr("设置导航", "Settings navigation")).
			Grow(1).Shrink(1).FillWidth().PaddingY(design.Space1)
	})
}

// settingsPanelSize resolves the adaptive settings panel size: the native modal
// sizes its panel to the window minus Space(10) and pads the panel by Space(5)
// on each side, so the settings row must fit inside that inner box.
func settingsPanelSize(t *design.Tokens, width, height float32) (float32, float32) {
	panelW, panelH := float32(design.SettingsWidth), float32(design.SettingsHeight)
	th := t.Theme()
	if width > 0 {
		innerW := width - th.Space(10) - 2*th.Space(5)
		if innerW > 0 && panelW > innerW {
			panelW = innerW
		}
	}
	if height > 0 {
		innerH := height - th.Space(10) - 2*th.Space(5)
		if innerH > 0 && panelH > innerH {
			panelH = innerH
		}
	}
	if panelW < 640 && (width <= 0 || panelW < width) {
		panelW = 640
	}
	if panelH < 480 && (height <= 0 || panelH < height) {
		panelH = 480
	}
	if panelW < 1 {
		panelW = 1
	}
	if panelH < 1 {
		panelH = 1
	}
	return panelW, panelH
}

// settings renders the host-owned settings surface: an adaptive modal sized to
// the window, a filtered grouped native Sidebar, a scrollable page body and a
// fixed footer that holds the page's primary action outside the scroll, so the
// action is visible without scrolling (the usability audit's H1).
func (r *Root) settings(c *ui.Context, open *bool) {
	t := design.Of(c)
	// The window shortcuts belong to the root scope, which an open modal
	// replaces; the settings surface re-binds the command palette inside its
	// own scope so Cmd+K keeps working here (the palette then draws above this
	// modal and Escape dismisses it first, fix round SH-07).
	paletteMods, paletteKey := r.paletteShortcut()
	c.OnShortcut(paletteMods, paletteKey, func() {
		if c.ShortcutRepeated() {
			return
		}
		r.togglePalette()
	})
	allPages := visiblePages(r.rt.settingsPages())
	needle := strings.ToLower(strings.TrimSpace(r.settingsQuery))
	pages := allPages
	if needle != "" {
		filtered := make([]contracts.SettingsPageSpec, 0, len(pages))
		for _, page := range pages {
			if r.settingsPageMatches(page, needle) {
				filtered = append(filtered, page)
			}
		}
		pages = filtered
	}
	width, height := c.Size()
	panelW, panelH := settingsPanelSize(t, width, height)

	allOrdered := flattenSettingsGroups(groupSettingsPages(allPages))
	if len(allOrdered) == 0 {
		ui.Column(c).Width(panelW).Padding(design.Space4).Gap(design.Space2).Children(func() {
			r.settingsTitleRow(c, t, open, false)
			design.Apply(ui.Text(c, r.locale.T("settings.none")), t.TextMuted(design.TypeXS))
			r.slot(c, contracts.SlotSettingsSidebar)
			r.slot(c, contracts.SlotSettingsPageFooter)
		})
		return
	}
	if r.settingsTab < 0 || r.settingsTab >= len(allOrdered) {
		r.settingsTab = 0
	}
	selected := allOrdered[r.settingsTab].ID
	groups := groupSettingsPages(pages)
	ordered := flattenSettingsGroups(groups)
	if len(ordered) == 0 {
		// The search matched no page: keep the navigation and the close control
		// reachable, and say so in the content column.
		ui.Row(c).Width(panelW).Height(panelH).Children(func() {
			r.settingsNavColumn(c, t, &selected, nil)
			ui.Column(c).FillHeight().Grow(1).Shrink(1).Children(func() {
				ui.Row(c).FillWidth().Shrink(0).AlignItems(ui.Center).Gap(design.Space2).
					Padding(design.Space4, design.Space4, design.Space2).Children(func() {
					r.settingsTitleRow(c, t, open, false)
				})
				design.Apply(ui.Text(c, r.tr("没有匹配的设置", "No matching settings")),
					t.TextMuted(design.TypeXS)).PaddingX(design.Space4)
			})
		})
		return
	}
	// Keep the current page when the filter matches it; otherwise show the first
	// match, then let the sidebar's own selection sync write the index back.
	visibleSelected := false
	for _, page := range ordered {
		if page.ID == selected {
			visibleSelected = true
			break
		}
	}
	displaySelected := selected
	if !visibleSelected {
		displaySelected = ordered[0].ID
	}
	// A search hit on one of a page's items selects that page and asks the page
	// to reveal the item's region (scroll into view + transient highlight). The
	// request is made when the query changes, so it runs once per search instead
	// of re-scrolling on every frame and fighting the user.
	if needle != r.settingsRevealedQuery {
		r.settingsRevealedQuery = needle
		r.settingsRevealPage, r.settingsRevealAnchor = "", ""
		if hitPage, hitItem, ok := settingsSearchHit(ordered, needle); ok && hitItem.AnchorKey != "" {
			r.settingsRevealPage, r.settingsRevealAnchor = hitPage.ID, hitItem.AnchorKey
			displaySelected = hitPage.ID
		}
	}
	current := allOrdered[r.settingsTab]
	dirty := r.settingsHasUnsaved()
	if !dirty {
		r.settingsDiscard = false
	}
	ui.Row(c).Width(panelW).Height(panelH).Children(func() {
		r.settingsNavColumn(c, t, &displaySelected, groups)
		ui.Column(c).FillHeight().Grow(1).Shrink(1).Children(func() {
			ui.Row(c).FillWidth().Shrink(0).AlignItems(ui.Center).Gap(design.Space2).
				Padding(design.Space4, design.Space4, design.Space2).Children(func() {
				r.settingsTitleRow(c, t, open, dirty)
			})
			if r.settingsDiscard {
				ui.Row(c).FillWidth().Shrink(0).PaddingX(design.Space4).Children(func() {
					r.settingsDiscardBar(c, t)
				})
			}
			ui.Scroll(c).FillWidth().Grow(1).Shrink(1).
				Padding(0, design.Space4, design.Space4).Children(func() {
				ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
					r.slot(c, contracts.SlotSettingsHeader)
					r.slot(c, contracts.SlotSettingsPageBody)
					// Hand a pending item hit to the page before it builds: the
					// page expands the disclosure around the region and scrolls
					// it into view in the same frame (contracts.SettingsPageSpec
					// .Reveal). The hit page is usually selected one frame before
					// its body builds, so the request waits here for it.
					if r.settingsRevealPage == current.ID && r.settingsRevealAnchor != "" {
						if current.Reveal != nil {
							current.Reveal(c, r.settingsRevealAnchor)
						}
						r.settingsRevealPage, r.settingsRevealAnchor = "", ""
					}
					r.safeBuild(c, current.Build)
				})
			})
			// The page's primary action stays in a fixed footer outside the
			// scrolling body, so it is visible without scrolling to the end.
			if current.Footer != nil || r.hasAnySlot(contracts.SlotSettingsPageFooter) {
				ui.Column(c).FillWidth().Shrink(0).PaddingX(design.Space4).Children(func() {
					ui.Divider(c)
					ui.Row(c).FillWidth().Shrink(0).PaddingY(design.Space2).Children(func() {
						r.slot(c, contracts.SlotSettingsPageFooter)
						if current.Footer != nil {
							r.safeBuild(c, current.Footer)
						}
					})
				})
			}
		})
	})
	for i, page := range allOrdered {
		if page.ID == displaySelected {
			r.settingsTab = i
			if r.settingsSelectedID != page.ID {
				r.settingsSelectedID = page.ID
				// The 高级 group follows the page that shows: it unfolds for a
				// low-frequency page and folds again when a daily page is chosen,
				// so the navigation does not keep the previous visit's longer
				// layout (the snapshots showed both widths depending on the order
				// pages were visited).
				r.settingsAdvancedOpen = page.Section == "高级"
			}
			break
		}
	}
}

// flattenSettingsGroups returns the pages in the order the grouped settings
// navigation shows them, which is also the order the page selection uses.
func flattenSettingsGroups(groups []settingsGroup) []contracts.SettingsPageSpec {
	total := 0
	for _, group := range groups {
		total += len(group.pages)
	}
	ordered := make([]contracts.SettingsPageSpec, 0, total)
	for _, group := range groups {
		ordered = append(ordered, group.pages...)
	}
	return ordered
}

// slot builds the contributions of one reserved host slot in order.
func (r *Root) slot(c *ui.Context, name string) {
	r.rt.RenderSlot(c, name)
}

// runHandlerSafely runs one host-bridge handler, containing a panic so a
// UI-module bug cannot cross the bridge boundary into the caller's goroutine
// (same convention as safeBuild and the approvals subscriber notify). A
// contained panic is logged and reported; the bridge then keeps its committed
// state and returns its normal result.
func runHandlerSafely(what string, fn func()) (panicked bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("uiroot: %s handler: %v", what, recovered)
			panicked = true
		}
	}()
	fn()
	return
}

// safeBuild contains a module builder panic so one broken module cannot take
// down the frame loop (the kernel contains the same class of panic at its own
// activation/cleanup boundaries). The stack is logged once per recovered panic
// so the broken builder can be found from a developer log.
func (r *Root) safeBuild(c *ui.Context, build contracts.BuildFunc) {
	if build == nil {
		return
	}
	if err := runSafelyErr(func() error { build(c); return nil }); err != nil {
		if panicErr, ok := err.(panicError); ok {
			log.Printf("uiroot: build: panic: %v\n%s", panicErr.value, debug.Stack())
		} else {
			log.Printf("uiroot: build: %v", err)
		}
	}
}

// safeToolBuild contains a tool renderer panic.
func (r *Root) safeToolBuild(c *ui.Context, renderer contracts.ToolRenderer, call contracts.ToolCall) {
	if renderer.Build == nil {
		return
	}
	if err := runSafelyErr(func() error { renderer.Build(c, call); return nil }); err != nil {
		log.Printf("uiroot: tool renderer %q: %v", renderer.ID, err)
	}
}

func runSafelyErr(fn func() error) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = panicError{value: rec}
		}
	}()
	return fn()
}

type panicError struct{ value any }

func (e panicError) Error() string { return "uiroot: panic in module code" }

func visible(spec contracts.ScreenSpec) bool {
	return spec.Visible == nil || spec.Visible()
}

// defaultScreenID is the first-run landing: the conversation surface (new
// chat / empty session hero), not the retired Home destination. Assemblies that
// do not ship a session screen keep their first visible screen.
func defaultScreenID(specs []contracts.ScreenSpec) string {
	for _, spec := range specs {
		if spec.ID == sessionScreenID {
			return spec.ID
		}
	}
	if len(specs) == 0 {
		return ""
	}
	return specs[0].ID
}

// visibleScreens returns the screens a user can navigate to. Home is retained
// as an embeddable screen but is no longer a product destination: the session
// surface owns the first-run/new-chat start.
func visibleScreens(specs []contracts.ScreenSpec) []contracts.ScreenSpec {
	out := specs[:0]
	for _, spec := range specs {
		if visible(spec) {
			out = append(out, spec)
		}
	}
	return out
}

func visiblePages(pages []contracts.SettingsPageSpec) []contracts.SettingsPageSpec {
	out := pages[:0]
	for _, page := range pages {
		if page.Visible == nil || page.Visible() {
			out = append(out, page)
		}
	}
	return out
}
