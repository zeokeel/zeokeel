package uiroot

import (
	"log"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

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

	// The old shell's chrome shortcut: Cmd/Ctrl+B collapses or expands the
	// left sidebar (the 64 DIP icon rail).
	r.bindChromeShortcuts(c)

	// Host applies the brand theme once; modules build with the frame theme.
	c.SetTheme(r.theme.currentTheme())
	// Host-owned commands and chrome strings are localized for the current
	// language; the registry has no locale of its own, so re-register them when
	// the user switches.
	if r.locale.Language() != r.hostLang {
		r.registerHostCommands()
	}
	tokens := design.Of(c)

	ui.Row(c).Fill().Background(tokens.BgBase).Children(func() {
		r.sidebar(c)
		// A drawer inspector floats over the content column; give the column the
		// drawer's width as right padding while it is open, so the drawer never
		// covers the composer's send control (the usability audit's H17).
		contentInset := float32(0)
		if r.rightPanelDrawerOpen(c) {
			contentInset = float32(design.InspectorWidth)
		}
		ui.Column(c).FillHeight().Grow(1).Shrink(1).MinWidth(0).
			Padding(0, contentInset, 0, 0).Children(func() {
			r.contentHeader(c)
			r.screen(c)
		})
		r.rightPanel(c)
	})

	r.palette(c)
	r.overlays(c)
}

// sidebar renders the main sidebar region with the brand geometry: the 280 DIP
// source column, the 64 DIP brand row (logo + wordmark), the primary action
// slot, screen navigation with icons, the session tree slot, the footer slot
// and the host settings entry.
func (r *Root) sidebar(c *ui.Context) {
	if r.sidebarCollapsed {
		r.sidebarRail(c)
		return
	}
	t := design.Of(c)
	screens := visibleScreens(r.rt.screenSpecs())
	selected := r.nav.Current()
	initial := selected == ""
	if initial && len(screens) > 0 {
		selected = screens[0].ID
	}
	column := ui.Column(c).FillHeight()
	design.Apply(column, t.Sidebar())
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
			collapse := ui.ButtonBase(c).Size(28, 28).Center().Radius(design.RadiusSM).
				Label(collapseLabel).Tooltip(collapseLabel)
			if collapse.Hovered() {
				collapse = collapse.Background(t.BgHover)
			}
			collapse.Children(func() {
				ui.Icon(c, icons.PanelLeft()).Size(18, 18).TextColor(t.LabelSecondary)
			})
			if collapse.Clicked() {
				r.SetSidebarCollapsed(true)
			}
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
		}).Grow(1).Shrink(1).FillWidth()
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
		Padding(design.Space3, 0, design.Space3, 0)
	design.Apply(column, func(el ui.Element) ui.Element {
		return el.Background(t.BgSidebar).BorderWidth(0, 1, 0, 0).BorderColor(t.BorderL3)
	})
	column.Children(func() {
		expandLabel := r.tr("展开侧边栏", "Expand sidebar")
		expand := ui.ButtonBase(c).Size(40, 40).Radius(design.RadiusSM).
			Label(expandLabel).Tooltip(expandLabel)
		if expand.Hovered() {
			expand = expand.Background(t.BgHover)
		}
		expand.Children(func() {
			ui.Image(c, brand.Mark(t.Dark)).Size(24, 24)
		})
		if expand.Clicked() {
			r.SetSidebarCollapsed(false)
		}
		for _, spec := range visibleScreens(r.rt.screenSpecs()) {
			spec := spec
			icon := r.screenIcon(spec.ID)
			label := r.screenLabel(spec)
			button := ui.ButtonBase(c).Size(40, 40).Radius(design.RadiusSM).
				Label(label).Tooltip(label)
			if r.nav.Current() == spec.ID {
				button = button.Background(t.BgSelected)
			}
			button.Children(func() {
				ui.Icon(c, icon).Size(18, 18).TextColor(t.LabelSecondary)
			})
			if button.Clicked() {
				if err := r.nav.Push(spec.ID); err != nil {
					log.Printf("uiroot: %v", err)
				}
				r.requestFrame()
			}
		}
		ui.Spacer(c).Grow(1)
		settingsLabel := r.locale.T("nav.settings")
		settings := ui.ButtonBase(c).Size(40, 40).Radius(design.RadiusSM).
			Label(settingsLabel).Tooltip(settingsLabel)
		if settings.Hovered() {
			settings = settings.Background(t.BgHover)
		}
		settings.Children(func() {
			ui.Icon(c, icons.Sliders()).Size(18, 18).TextColor(t.LabelSecondary)
		})
		if settings.Clicked() {
			r.nav.ToggleSettings()
			r.requestFrame()
		}
	})
}

// screenIcon maps a registered screen id to the shell's icon. The icon set must
// not collapse two destinations onto one glyph (the audit found the rail drew
// both Automation and Plugins as the same cube).
func (r *Root) screenIcon(id string) *ui.SVG {
	switch id {
	case "home":
		return icons.NewChat()
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
		return r.tr("插件", "Plugins")
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
		spec = specs[0]
	}
	ui.Column(c).FillWidth().Grow(1).Padding(design.Space6).Gap(design.Space4).Children(func() {
		// Screens own their header (the old UI's session header, home header,
		// ...); the host only offers the two header slots when a module
		// contributes to them, so no empty toolbar is reserved.
		if r.hasAnySlot(contracts.SlotScreenHeader, contracts.SlotScreenActions) {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				r.slot(c, contracts.SlotScreenHeader)
				ui.Spacer(c).Grow(1)
				r.slot(c, contracts.SlotScreenActions)
			})
		}
		r.safeBuild(c, spec.Build)
		r.slot(c, contracts.SlotScreenFooter)
	})
}

// contentHeader is the old shell's screen header band: the right-panel toggle
// sits at the top-right of the content column, on every screen.
func (r *Root) contentHeader(c *ui.Context) {
	t := design.Of(c)
	ui.Row(c).FillWidth().Height(design.Space8).PaddingX(design.Space4).
		AlignItems(ui.Center).Children(func() {
		ui.Spacer(c).Grow(1)
		if !r.hasAnySlot(contracts.SlotSessionPanelRight) {
			return
		}
		label := r.tr("打开右侧栏", "Open inspector")
		if r.rightOpen {
			label = r.tr("收起右侧栏", "Close inspector")
		}
		button := ui.ButtonBase(c).Size(28, 28).Center().
			Label(label).Tooltip(label).Radius(design.RadiusSM)
		if button.Hovered() {
			button = button.Background(t.BgHover)
		}
		button.Children(func() {
			ui.Icon(c, icons.PanelRight()).Size(18, 18).
				TextColor(t.LabelSecondary)
		})
		if button.Clicked() {
			r.rightOpen = !r.rightOpen
			r.requestFrame()
		}
	})
}

// rightPanel docks the workspace-wide inspector panel on the right edge when
// the user opened it and a module contributes to
// contracts.SlotSessionPanelRight (the old UI's right sidebar). It belongs to
// the window, not to one screen, so it survives screen switches.
func (r *Root) rightPanel(c *ui.Context) {
	if !r.rightOpen || !r.hasAnySlot(contracts.SlotSessionPanelRight) {
		return
	}
	t := design.Of(c)
	width, _ := c.Size()
	base := design.SidebarWidth
	if r.sidebarCollapsed {
		base = design.SidebarRailWidth
	}
	dockW := design.InspectorWidth
	if width > 0 {
		if avail := width - base - design.ContentMinWidth - design.Space6; avail < dockW {
			dockW = avail
		}
	}
	if dockW < design.InspectorMinWidth {
		// The window cannot afford a docked inspector without squeezing the
		// conversation below its minimum: show the same panel as a drawer over
		// the content instead of compressing both to unusable widths.
		r.rightPanelDrawer(c, t)
		return
	}
	ui.Column(c).Width(dockW).Shrink(0).FillHeight().Background(t.BgSidebar).
		BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL3).
		Padding(design.Space4).Children(func() {
		r.slot(c, contracts.SlotSessionPanelRight)
	})
}

// rightPanelDrawer shows the inspector over the content column, for windows too
// narrow to dock it. The backdrop and the close control both dismiss it.
func (r *Root) rightPanelDrawer(c *ui.Context, t *design.Tokens) {
	ui.Overlay(c, func() {
		backdropLabel := r.tr("关闭右侧栏", "Close inspector")
		backdrop := ui.ButtonBase(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
			Label(backdropLabel).Background(ui.RGBA(0, 0, 0, 0.25))
		if backdrop.Clicked() {
			r.rightOpen = false
			r.requestFrame()
		}
		panel := ui.Column(c).Absolute().Right(0).Top(0).Bottom(0).
			Width(design.InspectorWidth).Background(t.BgSidebar).
			BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL3).
			Padding(design.Space4)
		panel.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, r.tr("检查器", "Inspector")), t.Font(design.TypeS)).
					FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
				closeLabel := r.tr("关闭右侧栏", "Close inspector")
				close := ui.ButtonBase(c).Size(28, 28).Center().Radius(design.RadiusSM).
					Label(closeLabel).Tooltip(closeLabel)
				if close.Hovered() {
					close = close.Background(t.BgHover)
				}
				close.Children(func() {
					ui.Icon(c, icons.Close()).Size(16, 16).TextColor(t.LabelSecondary)
				})
				if close.Clicked() {
					r.rightOpen = false
					r.requestFrame()
				}
			})
			r.slot(c, contracts.SlotSessionPanelRight)
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
		return
	}
	t := design.Of(c)
	ui.Modal(c, &r.paletteOpen, func() {
		ui.Column(c).Width(560).Gap(design.Space2).Padding(design.Space4).Children(func() {
			design.Apply(ui.Text(c, r.tr("命令面板", "Command palette")), t.Font(design.TypeM)).FontWeight(600).
				TextColor(t.LabelPrimary)
			search := ui.SearchField(c, &r.paletteQuery).
				Label(r.tr("搜索命令", "Search commands")).
				Placeholder(r.tr("搜索命令…", "Search commands…")).FillWidth()
			// Arrow keys move the choice while the search field keeps the
			// keyboard, as in a desktop command palette; Enter and Escape are
			// the field's and the modal's.
			c.OnShortcut(0, ui.KeyDown, func() { r.movePaletteSelection(1) })
			c.OnShortcut(0, ui.KeyUp, func() { r.movePaletteSelection(-1) })
			needle := strings.ToLower(strings.TrimSpace(r.paletteQuery))
			if r.paletteFilter != needle {
				r.paletteFilter = needle
				r.paletteSelected = 0
			}
			matches := make([]contracts.CommandSpec, 0, 16)
			seen := make(map[string]bool, 16)
			for _, cmd := range r.rt.commandSpecs() {
				if cmd.Enabled != nil && !cmd.Enabled() {
					continue
				}
				if needle != "" {
					haystack := strings.ToLower(cmd.Title + "\x00" + cmd.ID + "\x00" + cmd.Hint)
					if !strings.Contains(haystack, needle) {
						continue
					}
				}
				// Two modules can register one visible command (the old
				// "设置"/"打开设置" pair); the palette shows the action once.
				if seen[cmd.Title] {
					continue
				}
				seen[cmd.Title] = true
				matches = append(matches, cmd)
			}
			if len(matches) == 0 {
				design.Apply(ui.Text(c, r.tr("没有匹配的命令", "No matching commands")), t.TextMuted(design.TypeXS))
				design.Apply(ui.Text(c, r.tr("Esc 关闭", "Esc to close")), t.TextMuted(design.TypeXXXS))
				return
			}
			r.paletteCount = len(matches)
			if r.paletteSelected >= len(matches) {
				r.paletteSelected = len(matches) - 1
			}
			if r.paletteSelected < 0 {
				r.paletteSelected = 0
			}
			r.paletteList.Key = func(row int) any { return matches[row].ID }
			r.paletteList.Label = func(row int) string { return matches[row].Title }
			r.paletteList.Selected = &r.paletteSelected
			list := ui.List(c, &r.paletteList, len(matches)).Rows(func(row ui.ListRow) {
				cmd := matches[row.Index]
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
			if search.Submitted() || list.Submitted() {
				r.runPaletteCommand(matches[r.paletteSelected])
			}
			design.Apply(ui.Text(c, r.tr("↑↓ 选择 · Enter 运行 · Esc 关闭",
				"Up/Down to choose · Enter to run · Esc to close")),
				t.TextMuted(design.TypeXXXS))
		})
	})
}

// movePaletteSelection moves the palette's choice while the search field has
// the keyboard focus.
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
		_ = cmd.Run()
	}
	r.requestFrame()
}

// overlays renders the settings surface and any module-declared overlay slots.
func (r *Root) overlays(c *ui.Context) {
	if r.nav.SettingsOpen() {
		open := true
		ui.Modal(c, &open, func() { r.settings(c, &open) })
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
// window is too narrow to dock it beside the content). Mirror of rightPanel's
// sizing decision, used to reserve the drawer's width in the content column.
func (r *Root) rightPanelDrawerOpen(c *ui.Context) bool {
	if !r.rightOpen || !r.hasAnySlot(contracts.SlotSessionPanelRight) {
		return false
	}
	width, _ := c.Size()
	base := design.SidebarWidth
	if r.sidebarCollapsed {
		base = design.SidebarRailWidth
	}
	dockW := design.InspectorWidth
	if width > 0 {
		if avail := width - base - design.ContentMinWidth - design.Space6; avail < dockW {
			dockW = avail
		}
	}
	return dockW < design.InspectorMinWidth
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
	"外观":      30,
	"键盘":      40, // legacy contributors
	"集成":      50,
	"高级":      80,
	"关于":      90,
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
	case "外观":
		return r.tr("外观与技能", "Appearance & skills")
	case "键盘":
		return r.tr("键盘", "Keyboard")
	case "集成":
		return r.tr("集成", "Integrations")
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
		design.Apply(ui.Text(c, r.locale.T("settings.title")), t.Font(design.TypeXS)).
			TextColor(t.LabelTertiary)
		if dirty {
			design.Apply(ui.Text(c, r.tr("未保存", "Unsaved")), t.Font(design.TypeXXXS)).
				TextColor(t.WarningText)
		}
		ui.Spacer(c).Grow(1)
		closeLabel := r.tr("关闭设置", "Close settings")
		close := ui.ButtonBase(c).Size(28, 28).Center().Radius(design.RadiusSM).
			Label(closeLabel).Tooltip(closeLabel)
		if close.Hovered() {
			close = close.Background(t.BgHover)
		}
		close.Children(func() {
			ui.Icon(c, icons.Close()).Size(16, 16).TextColor(t.LabelSecondary)
		})
		if close.Clicked() {
			*open = false
		}
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

// settingsNavColumn renders the grouped settings navigation with its filter
// field. The host owns the navigation; pages only contribute their specs.
func (r *Root) settingsNavColumn(c *ui.Context, t *design.Tokens, selected *string, groups []settingsGroup) {
	ui.Column(c).Width(200).FillHeight().Children(func() {
		ui.Row(c).FillWidth().Shrink(0).
			Padding(design.Space3, design.Space3, 0, design.Space3).Children(func() {
			ui.SearchField(c, &r.settingsQuery).
				Label(r.tr("搜索设置", "Search settings")).
				Placeholder(r.tr("搜索设置…", "Search settings…")).FillWidth()
		})
		ui.Sidebar(c, selected, func() {
			var scrollTarget ui.Element
			addPages := func(pages []contracts.SettingsPageSpec) {
				for _, page := range pages {
					item := ui.SidebarItem(c, page.ID, nil, page.Title)
					if page.ID == *selected {
						scrollTarget = item
					}
				}
			}
			searching := strings.TrimSpace(r.settingsQuery) != ""
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
		}).Grow(1).Shrink(1).FillWidth()
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
	allPages := visiblePages(r.rt.settingsPages())
	needle := strings.ToLower(strings.TrimSpace(r.settingsQuery))
	pages := allPages
	if needle != "" {
		filtered := make([]contracts.SettingsPageSpec, 0, len(pages))
		for _, page := range pages {
			if strings.Contains(strings.ToLower(page.Title), needle) {
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
				if page.Section == "高级" {
					r.settingsAdvancedOpen = true
				}
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

// runCommand runs a module command; commands are UI-thread work.
func (r *Root) runCommand(cmd contracts.CommandSpec) {
	if err := runSafelyErr(cmd.Run); err != nil {
		log.Printf("uiroot: command %q: %v", cmd.ID, err)
	}
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
