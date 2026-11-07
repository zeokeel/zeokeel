package uiroot

import (
	"log"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
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

	// Host applies the brand theme once; modules build with the frame theme.
	c.SetTheme(r.theme.currentTheme())
	tokens := design.Of(c)

	ui.Row(c).Fill().Background(tokens.BgBase).Children(func() {
		r.sidebar(c)
		ui.Column(c).FillHeight().Grow(1).Children(func() {
			r.contentHeader(c)
			r.screen(c)
		})
		r.rightPanel(c)
	})

	r.overlays(c)
}

// sidebar renders the main sidebar region with the brand geometry: the 280 DIP
// source column, the 64 DIP brand row (logo + wordmark), the primary action
// slot, screen navigation with icons, the session tree slot, the footer slot
// and the host settings entry.
func (r *Root) sidebar(c *ui.Context) {
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
		// Brand row: the mark, the ZeoKeel wordmark.
		brandRow := ui.Row(c).FillWidth().AlignItems(ui.Center).
			Padding(0, design.Space4, 0, design.Space4).Gap(design.Space2).Height(64)
		brandRow.Children(func() {
			ui.Image(c, brand.Mark(t.Dark)).Size(28, 28)
			design.Apply(ui.Text(c, r.appName), t.Font(design.TypeM)).FontWeight(600).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
		})

		// The host no longer paints a screen list: the destination rows, the
		// session list and the footer are module contributions (the old UI's
		// Home/Plugins/Automation + workspaces tree + More footer). The native
		// Sidebar still owns selection, keyboard navigation and scrolling.
		sidebar := ui.Sidebar(c, &selected, func() {
			r.slot(c, contracts.SlotSidebarPrimary)
			r.slot(c, contracts.SlotSidebarNav)
			r.slot(c, contracts.SlotSidebarTree)
			r.slot(c, contracts.SlotSidebarFooter)
		}).FillHeight()
		sidebar.Background(t.BgSidebar)
		changed = sidebar.Changed()
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

// panelRightIcon is the old shell's right-panel glyph, drawn as an SVG asset
// (not hand-painted by a module).
var panelRightIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="15" x2="15" y1="3" y2="21"/></svg>`))

// contentHeader is the old shell's screen header band: the right-panel toggle
// sits at the top-right of the content column, on every screen.
func (r *Root) contentHeader(c *ui.Context) {
	t := design.Of(c)
	ui.Row(c).FillWidth().Height(40).PaddingX(design.Space4).
		AlignItems(ui.Center).Children(func() {
		ui.Spacer(c).Grow(1)
		if !r.hasAnySlot(contracts.SlotSessionPanelRight) {
			return
		}
		label := "打开右侧栏"
		if r.rightOpen {
			label = "收起右侧栏"
		}
		button := ui.ButtonBase(c).Size(design.Space6, design.Space6).Center().
			Label(label).Tooltip(label).Radius(design.RadiusSM)
		if button.Hovered() {
			button = button.Background(t.BgHover)
		}
		button.Children(func() {
			ui.Icon(c, panelRightIcon).Size(design.Space4, design.Space4).
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
	ui.Column(c).Width(320).FillHeight().Background(t.BgSidebar).
		BorderWidth(1, 0, 0, 0).BorderColor(t.BorderL3).
		Padding(design.Space4).Children(func() {
		r.slot(c, contracts.SlotSessionPanelRight)
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

// overlays renders the settings surface and any module-declared overlay slots.
func (r *Root) overlays(c *ui.Context) {
	if r.nav.SettingsOpen() {
		open := true
		ui.Modal(c, &open, func() { r.settings(c) })
		if !open {
			r.nav.CloseSettings()
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

// settings renders the host-owned settings surface: a native Sidebar with the
// module-contributed pages, a sidebar extension slot, and the selected page
// between its header/footer slots. The host owns the shell, never the pages.
func (r *Root) settings(c *ui.Context) {
	pages := visiblePages(r.rt.settingsPages())
	if len(pages) == 0 {
		ui.Column(c).Width(480).Padding(20).Gap(10).Children(func() {
			ui.Text(c, r.locale.T("settings.title")).Bold().FontSize(18)
			ui.Text(c, r.locale.T("settings.none"))
			r.slot(c, contracts.SlotSettingsSidebar)
			r.slot(c, contracts.SlotSettingsHeader)
			r.slot(c, contracts.SlotSettingsPageFooter)
		})
		return
	}
	if r.settingsTab >= len(pages) {
		r.settingsTab = 0
	}
	selected := pages[r.settingsTab].ID
	open := true
	ui.Row(c).Width(760).Height(560).Children(func() {
		ui.Sidebar(c, &selected, func() {
			ui.SidebarSection(c, r.locale.T("settings.title"), &open, func() {
				for _, page := range pages {
					ui.SidebarItem(c, page.ID, nil, page.Title)
				}
			})
			r.slot(c, contracts.SlotSettingsSidebar)
		}).Width(200).FillHeight()
		ui.Column(c).FillHeight().Grow(1).Padding(18).Gap(12).Children(func() {
			r.slot(c, contracts.SlotSettingsHeader)
			r.slot(c, contracts.SlotSettingsPageBody)
			r.safeBuild(c, pages[r.settingsTab].Build)
			r.slot(c, contracts.SlotSettingsPageFooter)
		})
	})
	for i, page := range pages {
		if page.ID == selected {
			r.settingsTab = i
			break
		}
	}
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

// safeBuild contains a module builder panic so one broken module cannot take
// down the frame loop (the kernel contains the same class of panic at its own
// activation/cleanup boundaries).
func (r *Root) safeBuild(c *ui.Context, build contracts.BuildFunc) {
	if build == nil {
		return
	}
	if err := runSafelyErr(func() error { build(c); return nil }); err != nil {
		log.Printf("uiroot: build: %v", err)
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
