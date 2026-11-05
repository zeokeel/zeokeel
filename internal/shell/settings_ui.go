package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// settingsAPI is the read/write surface the shell needs from the persisted
// settings. Controller.Settings implements it; tests may substitute a fake.
type settingsAPI interface {
	Load() (session.Settings, error)
	Save(session.Settings) error
}

// settingsStore returns the settings accessor in use: an injected one first,
// then the live controller's. A static App has none.
func (a *App) settingsStore() settingsAPI {
	if a.settings != nil {
		return a.settings
	}
	if a.Ctrl != nil {
		return a.Ctrl.Settings()
	}
	return nil
}

// currentSettings lazily loads and caches the UI settings. The cache is
// updated by updateSettings, so the store is read once per app run.
func (a *App) currentSettings() session.Settings {
	if a.settingsLoaded {
		return a.settingsValue
	}
	a.settingsLoaded = true
	a.settingsValue = session.DefaultSettings()
	if store := a.settingsStore(); store != nil {
		cfg, err := store.Load()
		if err != nil {
			a.settingsErr = err
			return a.settingsValue
		}
		a.settingsValue = session.NormalizeSettings(cfg)
	}
	return a.settingsValue
}

// updateSettings mutates, normalizes, caches and persists the settings. The
// new values apply from the next frame; a toast reports the outcome.
func (a *App) updateSettings(c *ui.Context, mutate func(*session.Settings)) {
	cfg := a.currentSettings()
	mutate(&cfg)
	cfg = session.NormalizeSettings(cfg)
	a.settingsValue = cfg
	a.settingsLoaded = true
	// A language change must apply on the next frame; drop the resolved
	// language cache so T consults the freshly saved ui.language.
	a.langLoaded = false
	a.langValue = ""

	if store := a.settingsStore(); store != nil {
		var saveErr error
		if preserving, ok := store.(interface {
			SavePreservingLanguage(session.Settings) error
		}); ok {
			saveErr = preserving.SavePreservingLanguage(cfg)
		} else {
			saveErr = store.Save(cfg)
		}
		if saveErr != nil {
			a.settingsErr = saveErr
			if c != nil {
				c.Toast(a.T("settings.could_not_save_settings"))
			}
			return
		}
	}
	a.settingsErr = nil
	if c != nil {
		c.Toast(a.T("settings.settings_saved"))
	}
}

// themeTokens resolves the appearance setting over the desktop's current
// appearance: Light and Dark force a theme, System follows the desktop.
func (a *App) themeTokens(c *ui.Context) *design.Tokens {
	dark := c.Theme().Dark
	switch a.currentSettings().Theme {
	case session.ThemeLight:
		dark = false
	case session.ThemeDark:
		dark = true
	}
	if dark {
		return design.Dark()
	}
	return design.Light()
}

// conversationTextSize is the design helper conversation text goes through:
// the configured 14 or 16px, with 14 as the baseline default.
func conversationTextSize(cfg session.Settings) float32 {
	if cfg.FontSize >= session.FontSizeLarge {
		return float32(session.FontSizeLarge)
	}
	return float32(session.FontSizeSmall)
}

// ConversationFontSize is the size conversation text uses. Session renderers
// call it for message bodies so the Font size setting scales chat text only.
func (a *App) ConversationFontSize() float32 {
	return conversationTextSize(a.currentSettings())
}

// ConversationLineHeight returns the matching line box: the baseline's 24px
// at 14px, 28px at 16px.
func (a *App) ConversationLineHeight() float32 {
	if a.ConversationFontSize() >= float32(session.FontSizeLarge) {
		return 28
	}
	return 24
}

// toolDetailsExpanded is the design helper for tool cards: Detailed starts
// them expanded, Standard starts them collapsed.
func toolDetailsExpanded(workDetails string) bool {
	return workDetails != session.WorkDetailsStandard
}

// ToolDetailsExpanded reports the tool-card expansion default. Tool card
// renderers call it so Work details applies to newly built cards.
func (a *App) ToolDetailsExpanded() bool {
	return toolDetailsExpanded(a.currentSettings().WorkDetails)
}

// currentSnapshot returns the live session snapshot for a frame: the one
// pushed by the controller first, then the controller's current snapshot.
func (a *App) currentSnapshot() *state.Snapshot {
	if snap := a.Snapshot(); snap != nil {
		return snap
	}
	if a.Ctrl != nil {
		return a.Ctrl.Snapshot()
	}
	return nil
}

// applyToolCardDefaults seeds the live motion state's tool-card expansion
// for tools seen for the first time. Detailed starts cards expanded and
// Standard starts them collapsed; a card the user has toggled keeps its
// choice because it already has an entry.
func (a *App) applyToolCardDefaults(snap *state.Snapshot) {
	if a.Ctrl == nil || snap == nil {
		return
	}
	cfg := a.currentSettings()
	expanded := a.live().expanded
	st := a.liveMenus()
	def := toolDetailsExpanded(cfg.WorkDetails)
	if cfg.CollapseWorkDetail == session.CollapseImmediately {
		// Immediately: a new card starts collapsed and an existing one is
		// closed the first time its call is seen finished.
		def = false
	}
	if st.doneSeen == nil {
		st.doneSeen = make(map[string]bool)
	}
	for id, tool := range snap.Tools {
		if _, ok := expanded[id]; !ok {
			expanded[id] = def
		}
		finished := tool.Status == state.ToolDone || tool.Status == state.ToolError
		if finished && !st.doneSeen[id] {
			st.doneSeen[id] = true
			if cfg.CollapseWorkDetail == session.CollapseImmediately {
				expanded[id] = false
			}
		}
	}
	if cfg.CollapseWorkDetail == session.CollapseOnCompletion &&
		st.lastPhase == state.PhaseRunning && snap.Phase != state.PhaseRunning {
		for id := range expanded {
			expanded[id] = false
		}
	}
	st.lastPhase = snap.Phase
}

// settingsOverlay dims the screen behind the settings modal. The scrim and
// the panel both take a click so a click inside the panel does not dismiss
// it; the panel's own controls run before the panel's no-op click.
func (a *App) settingsOverlay(c *ui.Context, t *design.Tokens) {
	ui.Overlay(c, func() {
		back := ui.Row(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
			Justify(ui.Center).AlignItems(ui.Center).
			Background(t.Scrim()).Modal()
		back.OnClick(func() { a.closeSettings() })
		back.Children(func() { a.settingsPanelLive(c, t) })
		// Window shortcuts wait behind a modal, so the settings overlay
		// registers the global keys itself. Escape closes it.
		if a.overlayShortcut(back, "escape", 0, ui.KeyEscape) {
			a.closeSettings()
			return
		}
		if a.overlayShortcut(back, "new_session", ui.Cmd, ui.KeyN) {
			a.newSession()
			return
		}
		if a.overlayShortcut(back, "search", ui.Cmd, ui.KeyK) {
			a.openPalette()
		}
		if a.overlayShortcut(back, "shortcuts", ui.Cmd, ui.KeySlash) {
			a.openShortcutsDialog()
		}
	})
}

// settingsPanelLive draws the same 800x800 panel as the static baseline with
// live controls. Every row keeps the baseline's geometry; the values and
// click handlers come from the persisted settings.
func (a *App) settingsPanelLive(c *ui.Context, t *design.Tokens) {
	if tab := a.shellUI().settingsTab; tab == settingsTabAgentPresets && !a.currentSettings().ShowCodingView {
		a.shellUI().settingsTab = settingsTabGeneral
	}
	w, h := settingsPanelSize(c)
	panel := ui.Row(c).
		Width(w).Height(h).
		AlignItems(ui.Stretch).
		Background(settingsPanelColor(t)).Radius(settingsRadius).Clip().
		Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.16)).
		Shadow(0, 3, 8, 0, ui.RGBA(0, 0, 0, 0.04)).
		Shadow(0, 0, 20, 0, ui.RGBA(0, 0, 0, 0.05)).
		Role(ui.RoleDialog).Label(a.T("settings.settings"))
	panel.OnClick(func() {})
	panel.Children(func() {
		a.settingsNavLive(c, t, h)
		a.settingsBodyLive(c, t)
	})
}

// settingsNavLive is the left column, localized but otherwise identical.
func (a *App) settingsNavLive(c *ui.Context, t *design.Tokens, height float32) {
	tab := a.shellUI().settingsTab
	ui.Column(c).Width(settingsNavW).Height(height).
		Padding(settingsNavTop, settingsNavPad, 0, settingsNavPad).
		Gap(settingsTitleGap).Children(func() {
		settingsText(c, a.T("settings.settings"), design.TypeM, t.LabelPrimary).FontWeight(500).PaddingX(design.Space3)
		ui.Column(c).Gap(design.Space1).Children(func() {
			a.settingsNavCellLive(c, t, a.T("settings.general"), settingsTabGeneral, tab, settingsIconGear)
			a.settingsNavCellLive(c, t, a.T("settings.models"), settingsTabModels, tab, settingsIconDatabase)
			// Memory is a live-only page: its list comes from the store and
			// the selected workspace, so the static baseline keeps exactly
			// the four canonical nav cells.
			if a.Ctrl != nil {
				a.settingsNavCellLive(c, t, a.memoryText("Memory", "记忆"), settingsTabMemory, tab, memoryIcon)
			}
			a.settingsNavCellLive(c, t, a.T("settings.built_in_plugins"), settingsTabBuiltinPlugins, tab, settingsIconSliders)
			// "Show coding view" also governs whether the Agent presets page
			// is offered, per the row's description.
			if a.currentSettings().ShowCodingView {
				a.settingsNavCellLive(c, t, a.T("settings.agent_presets"), settingsTabAgentPresets, tab, settingsIconPerson)
			}
			// About is a live-only page: the DSH baseline settings screenshot
			// has the four canonical nav cells, so the static pixel path keeps
			// exactly those while a running app also offers About.
			if a.Ctrl != nil {
				a.settingsNavCellLive(c, t, a.T("menu.about"), settingsTabAbout, tab, pluginIconInfo)
			}
		})
	})
}

// settingsNavCellLive is one nav cell with its click handler. The active cell
// keeps the baseline tint.
func (a *App) settingsNavCellLive(c *ui.Context, t *design.Tokens, label string, target, current settingsTab, icon *ui.SVG) {
	cell := a.settingsNavCell(c, t, label, target == current, icon)
	cell.OnClick(func() {
		a.shellUI().settingsTab = target
		a.closeSelectors()
	})
}

// settingsBodyLive is the right column: header actions and the selected page.
func (a *App) settingsBodyLive(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Grow(1).Children(func() {
		ui.Row(c).Height(settingsHeaderH).
			Padding(settingsHeaderTop, settingsHeaderEnd, 0, 0).
			Gap(design.Space2).Justify(ui.End).AlignItems(ui.Start).Children(func() {
			a.settingsOutlineButtonLive(c, t, a.T("settings.open_configuration_file"),
				func() { a.openConfigurationLocation(c) })
			// The explicit Close button is a live affordance; the baseline's
			// header has only the configuration action and the X, so the
			// static pixel path keeps that exact header.
			if a.Ctrl != nil {
				a.settingsOutlineButtonLive(c, t, a.T("menu.close"),
					func() { a.closeSettings() })
			}
			a.settingsCloseLive(c, t)
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(0, settingsOptionsPad, 0, settingsOptionsPad).Children(func() {
				switch a.shellUI().settingsTab {
				case settingsTabModels:
					a.modelsSettingsPage(c, t)
				case settingsTabMemory:
					a.memorySettingsPage(c, t)
				case settingsTabBuiltinPlugins:
					a.pluginsSettingsPage(c, t)
				case settingsTabAgentPresets:
					if a.currentSettings().ShowCodingView {
						a.agentPresetsPage(c, t)
					} else {
						a.settingsRowsLive(c, t)
					}
				case settingsTabAbout:
					a.aboutSettingsPage(c, t)
				default:
					a.settingsRowsLive(c, t)
				}
			})
		})
	})
	a.pluginDialogs(c, t)
}

// settingsRowsLive draws the option list in the baseline order and with the
// baseline row styles.
func (a *App) settingsRowsLive(c *ui.Context, t *design.Tokens) {
	cfg := a.currentSettings()

	a.settingsRow(c, t, a.T("settings.permission"),
		a.T("settings.choose_the_default_permission_mode_for_new_sessions"),
		settingsRowStyle{},
		func() {
			// A unique accessible name: the session composer behind the modal
			// also shows "Workspace Write", so the bare value is ambiguous to
			// accessibility and to the Tester.
			permission := a.permissionLabel(cfg.Permission)
			a.settingsSelectorLiveNamed(c, t,
				a.menuText("Permission, current: ", "权限，当前：")+permission,
				permission, 172, &a.permissionOpen, []settingsOption{
					{a.T("settings.full_access"), cfg.Permission == session.PermissionFull,
						func() { a.updateSettings(c, func(s *session.Settings) { s.Permission = session.PermissionFull }) }, ""},
					{a.T("settings.workspace_write"), cfg.Permission == session.PermissionWorkspace,
						func() { a.updateSettings(c, func(s *session.Settings) { s.Permission = session.PermissionWorkspace }) }, ""},
					{a.T("settings.read_only"), cfg.Permission == session.PermissionAsk,
						func() { a.updateSettings(c, func(s *session.Settings) { s.Permission = session.PermissionAsk }) }, ""},
					{a.autoReviewLabel(), cfg.Permission == session.PermissionAutoReview,
						func() { a.updateSettings(c, func(s *session.Settings) { s.Permission = session.PermissionAutoReview }) },
						a.autoReviewDescription()},
				})
		})

	a.settingsRow(c, t, a.T("settings.language"), "", settingsRowStyle{},
		func() {
			language := a.languageLabel(cfg.Language)
			if a.Ctrl == nil {
				// The static demo shell resolves to English; the selector must
				// agree with the English labels around it instead of showing
				// the live product default (Chinese).
				language = a.T("settings.language_english")
			}
			a.settingsSelectorLive(c, t, language, 105, &a.languageOpen, []settingsOption{
				{a.T("settings.language_english"), cfg.Language == session.LanguageEnglish,
					func() { a.updateSettings(c, func(s *session.Settings) { s.Language = session.LanguageEnglish }) }, ""},
				{a.T("settings.language_chinese"), cfg.Language == session.LanguageChinese,
					func() { a.updateSettings(c, func(s *session.Settings) { s.Language = session.LanguageChinese }) }, ""},
			})
		})

	a.settingsAppearanceLive(c, t, cfg)

	a.settingsRow(c, t, a.T("settings.font_size"),
		a.T("settings.only_affects_conversation_content"),
		settingsRowStyle{},
		func() { a.settingsFontSizeLive(c, t, cfg) })

	a.settingsRow(c, t, a.T("settings.work_details"),
		a.T("settings.choose_how_much_detail_to_show_for_tool_calls"),
		settingsRowStyle{},
		func() {
			a.settingsSelectorLive(c, t, a.workDetailsLabel(cfg.WorkDetails), 113, &a.workOpen, []settingsOption{
				{a.T("settings.standard"), cfg.WorkDetails == session.WorkDetailsStandard,
					func() { a.updateSettings(c, func(s *session.Settings) { s.WorkDetails = session.WorkDetailsStandard }) }, ""},
				{a.T("settings.detailed"), cfg.WorkDetails == session.WorkDetailsDetailed,
					func() { a.updateSettings(c, func(s *session.Settings) { s.WorkDetails = session.WorkDetailsDetailed }) }, ""},
			})
		})

	a.settingsRow(c, t, a.T("settings.when_to_collapse_work_details"),
		a.T("settings.choose_when_to_automatically_collapse_work_details"),
		settingsRowStyle{},
		func() {
			a.settingsSelectorLive(c, t, collapseLabel(cfg, a), 156, &a.collapseOpen, []settingsOption{
				{a.T("settings.on_completion"), cfg.CollapseWorkDetail == session.CollapseOnCompletion, func() {
					a.updateSettings(c, func(s *session.Settings) { s.CollapseWorkDetail = session.CollapseOnCompletion })
				}, ""},
				{a.T("settings.immediately"), cfg.CollapseWorkDetail == session.CollapseImmediately, func() {
					a.updateSettings(c, func(s *session.Settings) { s.CollapseWorkDetail = session.CollapseImmediately })
				}, ""},
			})
		})

	a.settingsRow(c, t, a.T("settings.show_coding_view"),
		a.T("settings.shows_trajectory_code_diffs_and_all_agent_presets"),
		settingsRowStyle{titleLine: 20, descColor: t.LabelSecondary, gap: design.Space6},
		func() {
			a.settingsSwitchLive(c, t, cfg.ShowCodingView,
				a.tr("Toggle show coding view", "切换显示编码视图"), func() {
					a.updateSettings(c, func(s *session.Settings) { s.ShowCodingView = !s.ShowCodingView })
				})
		})

	a.settingsRow(c, t, a.T("settings.keyboard_shortcuts"),
		a.T("settings.view_and_edit_available_shortcuts_and_input_actions"),
		settingsRowStyle{},
		func() {
			w := float32(125)
			if a.lang() == LangZH {
				w = 150
			}
			a.settingsActionButtonLive(c, t, a.T("settings.edit_shortcuts"), w, func() {
				a.shortcutsQuery = ""
				a.shortcutEditID = ""
				a.openShortcutsDialog()
			})
		})

	a.settingsRow(c, t, a.T("settings.send_behavior_while_busy"),
		a.T("settings.what_enter_and_the_send_button_do_while_the_agent_is_running_cmd"),
		settingsRowStyle{},
		func() {
			a.settingsSelectorLive(c, t, sendLabel(cfg, a), 100, &a.sendOpen, []settingsOption{
				{a.T("settings.queue"), cfg.SendBehavior == session.SendQueue, func() {
					a.updateSettings(c, func(s *session.Settings) { s.SendBehavior = session.SendQueue })
				}, ""},
				{a.T("settings.interrupt"), cfg.SendBehavior == session.SendInterrupt, func() {
					a.updateSettings(c, func(s *session.Settings) { s.SendBehavior = session.SendInterrupt })
				}, ""},
			})
		})

	a.settingsRow(c, t, a.T("settings.performance_usage"),
		a.T("settings.performance_usage_desc"),
		settingsRowStyle{},
		func() {
			a.settingsSelectorLive(c, t, performanceLabel(cfg, a), 113, &a.performanceOpen, []settingsOption{
				{a.T("settings.detailed"), cfg.PerformanceUsage == session.PerformanceDetailed, func() {
					a.updateSettings(c, func(s *session.Settings) { s.PerformanceUsage = session.PerformanceDetailed })
				}, ""},
				{a.T("settings.standard"), cfg.PerformanceUsage == session.PerformanceStandard, func() {
					a.updateSettings(c, func(s *session.Settings) { s.PerformanceUsage = session.PerformanceStandard })
				}, ""},
			})
		})

	desc := a.tr("Not available in this build: no session-log upload endpoint is configured.",
		"此版本不可用：尚未配置会话日志上传端点。")
	if a.Ctrl == nil {
		// The static baseline renderer keeps DSH's original row byte for
		// byte; it has no services, so there is nothing to upload either way.
		desc = a.T("settings.upload_session_log_desc")
	}
	a.settingsRow(c, t, a.T("settings.upload_session_log"), desc,
		settingsRowStyle{},
		func() {
			if a.Ctrl == nil {
				a.settingsSwitchLive(c, t, cfg.UploadSessionLog,
					a.tr("Upload session log", "上传会话日志"), func() {
						a.updateSettings(c, func(s *session.Settings) { s.UploadSessionLog = !s.UploadSessionLog })
					})
				return
			}
			// There is no official-API upload path in this build, so the live
			// switch is disabled: turning it on must not persist a no-op.
			unavailable := ui.Row(c).Width(36).Height(20).Radius(design.RadiusMax).
				Padding(2).AlignItems(ui.Center).Justify(ui.Start).
				Background(t.BgHover).Label(a.tr("Upload session log",
				"上传会话日志")).Role(ui.RoleSwitch).Disabled(true)
			unavailable.Children(func() {
				thumb := t.White
				if t.Dark {
					thumb = t.BgBase
				}
				ui.Box(c).Width(16).Height(16).Radius(design.RadiusMax).Background(thumb)
			})
		})
}

// settingsAppearanceLive is the appearance group with clickable theme cards.
func (a *App) settingsAppearanceLive(c *ui.Context, t *design.Tokens, cfg session.Settings) {
	ui.Column(c).Padding(design.Space4, 0, design.Space4, 0).Gap(design.Space2).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		settingsText(c, a.T("settings.appearance"), design.TypeBody, t.LabelPrimary)
		ui.Row(c).Height(settingsCardH).Gap(design.Space2).Children(func() {
			a.settingsThemeCardLive(c, t, a.T("settings.light"), session.ThemeLight, cfg.Theme == session.ThemeLight, settingsIconSun)
			a.settingsThemeCardLive(c, t, a.T("settings.dark"), session.ThemeDark, cfg.Theme == session.ThemeDark, settingsIconMoon)
			a.settingsThemeCardLive(c, t, a.T("settings.system"), session.ThemeSystem, cfg.Theme == session.ThemeSystem, settingsIconMonitor)
		})
	})
}

// settingsThemeCardLive is one 183x84 appearance card.
func (a *App) settingsThemeCardLive(c *ui.Context, t *design.Tokens, label, value string, selected bool, icon *ui.SVG) {
	card := ui.Column(c).Grow(1).Height(settingsCardH).
		Radius(design.RadiusXL).Border(1, settingsCardOutline(t)).
		Gap(design.Space1).AlignItems(ui.Center).Justify(ui.Center).
		Label(label).Value(label).Role(ui.RoleRadio).Checked(selected).Focusable()
	if selected {
		card = card.Background(settingsFillColor(t)).BorderColor(settingsCardSelectedEdge)
	}
	card.Children(func() {
		ui.Image(c, icon).Size(16, 16).TextColor(t.LabelPrimary)
		settingsText(c, label, design.TypeBody, t.LabelPrimary)
	})
	card.OnClick(func() {
		a.updateSettings(c, func(s *session.Settings) { s.Theme = value })
	})
}

// settingsSelectorLive draws the baseline select pill and opens a popover
// with the options when clicked.
func (a *App) settingsSelectorLive(c *ui.Context, t *design.Tokens, label string, w float32, open *bool, options []settingsOption) {
	a.settingsSelectorLiveNamed(c, t, label, label, w, open, options)
}

// settingsSelectorLiveNamed separates the visible value from the accessible
// name, so a selector can say "Permission, current: Workspace Write" without
// changing its on-screen text.
func (a *App) settingsSelectorLiveNamed(c *ui.Context, t *design.Tokens, accessible, label string, w float32, open *bool, options []settingsOption) {
	el := ui.Row(c).Width(w).Height(settingsControlH).PaddingX(14).
		Gap(design.Space3).Radius(design.RadiusMD).Background(settingsFillColor(t)).
		Justify(ui.SpaceBetween).AlignItems(ui.Center).
		Label(accessible).Value(label).Role(ui.RolePopUpButton).Focusable()
	el.Children(func() {
		// NoWrap rather than MaxLines(1): the baseline shows the full
		// "Workspace Write" / "On completion" text, not an ellipsis.
		settingsText(c, label, design.TypeBody, t.LabelPrimary).NoWrap()
		ui.Image(c, settingsIconChevron).Size(14, 14).TextColor(t.LabelSecondary)
	})
	el.Expanded(*open)
	el.OnClick(func() { a.toggleSelector(open) })
	ui.Popover(c, el, open, func() { a.settingsOptionPanel(c, t, open, options) })
}

// settingsOption is one row of a settings popover.
type settingsOption struct {
	label    string
	selected bool
	choose   func()
	// desc is an optional one-line explanation rendered under label. Empty
	// keeps the single-line option every other selector uses.
	desc string
}

// settingsOptionPanel is the popover body; choosing an option closes it and
// applies the value, which rebuilds the frame.
func (a *App) settingsOptionPanel(c *ui.Context, t *design.Tokens, open *bool, options []settingsOption) {
	ui.Column(c).Gap(2).Children(func() {
		for _, option := range options {
			height := float32(30)
			if option.desc != "" {
				height = 46
			}
			row := ui.Row(c).Height(height).PaddingX(design.Space2).Gap(design.Space2).
				Radius(design.RadiusSM).AlignItems(ui.Center).
				Label(option.label).Role(ui.RoleMenuItemRadio).
				Checked(option.selected).Focusable()
			if option.selected || row.Hovered() {
				row = row.Background(t.BgHover)
			}
			row.Children(func() {
				if option.desc == "" {
					settingsText(c, option.label, design.TypeBody, t.LabelPrimary)
					return
				}
				ui.Column(c).Gap(1).Children(func() {
					settingsText(c, option.label, design.TypeBody, t.LabelPrimary)
					settingsText(c, option.desc, design.TypeXXS, t.LabelTertiary).MaxLines(2)
				})
			})
			choose := option.choose
			row.OnClick(func() {
				*open = false
				if choose != nil {
					choose()
				}
			})
		}
	})
}

// settingsFontSizeLive is DSH's compact stepper: a 72px value pill with the
// 17x12 up/down arrows inside its right edge, the "px" unit and the detailed
// settings expander after it. The whole control is 129px wide, matching the
// baseline box [1047,467,129,36]; the old version put 20x12 arrow buttons
// outside the pill and widened the control by ~29px.
func (a *App) settingsFontSizeLive(c *ui.Context, t *design.Tokens, cfg session.Settings) {
	el := ui.Row(c).Width(129).Gap(design.Space2).AlignItems(ui.Center)
	el.Children(func() {
		pill := ui.Row(c).Width(72).Height(settingsControlH).
			PaddingEnd(design.Space2).
			Radius(design.RadiusMD).Background(settingsFillColor(t))
		pill.Children(func() {
			value := ui.Row(c).Grow(1).Height(settingsControlH).Center().
				Label(fontSizeLabel(cfg.FontSize) + " px").
				Role(ui.RoleButton).Focusable()
			value.Children(func() {
				settingsText(c, fontSizeLabel(cfg.FontSize), design.TypeBody, t.LabelPrimary)
			})
			value.Expanded(a.fontSizeOpen)
			value.OnClick(func() { a.toggleSelector(&a.fontSizeOpen) })
			ui.Popover(c, value, &a.fontSizeOpen, func() {
				a.settingsOptionPanel(c, t, &a.fontSizeOpen, []settingsOption{
					{"14 px", cfg.FontSize == session.FontSizeSmall, func() {
						a.updateSettings(c, func(s *session.Settings) { s.FontSize = session.FontSizeSmall })
					}, ""},
					{"16 px", cfg.FontSize == session.FontSizeLarge, func() {
						a.updateSettings(c, func(s *session.Settings) { s.FontSize = session.FontSizeLarge })
					}, ""},
				})
			})
			// DSH draws both 17x12 step arrows inside the value pill at
			// x=1094; they are subdued until hover/focus.
			ui.Column(c).Width(17).Gap(1).Children(func() {
				inc := ui.Row(c).Size(17, 12).Center().
					Label(a.pluginLocalized("Increase font size", "增大字号")).
					Role(ui.RoleButton).Focusable()
				if inc.Hovered() || inc.Focused() {
					inc = inc.Background(t.BgHover).Radius(design.RadiusXS)
				}
				inc.Children(func() {
					settingsText(c, "+", design.TypeXXS, t.LabelTertiary)
				})
				inc.OnClick(func() { a.stepFontSize(c, 2) })
				dec := ui.Row(c).Size(17, 12).Center().
					Label(a.pluginLocalized("Decrease font size", "减小字号")).
					Role(ui.RoleButton).Focusable()
				if dec.Hovered() || dec.Focused() {
					dec = dec.Background(t.BgHover).Radius(design.RadiusXS)
				}
				dec.Children(func() {
					settingsText(c, "−", design.TypeXXS, t.LabelTertiary)
				})
				dec.OnClick(func() { a.stepFontSize(c, -2) })
			})
		})
		settingsText(c, "px", design.TypeBody, t.LabelSecondary)
		more := ui.Row(c).Size(24, 24).Center().
			Label(a.pluginLocalized("More font settings", "更多字体设置")).Role(ui.RoleButton).Focusable()
		more.Children(func() {
			ui.Image(c, settingsIconChevron).Size(12, 12).TextColor(t.LabelTertiary)
		})
		more.Expanded(a.pluginUI().fontMoreOpen)
		more.OnClick(func() {
			opened := a.pluginUI().fontMoreOpen
			a.closeSelectors()
			a.pluginUI().fontMoreOpen = !opened
		})
		ui.Popover(c, more, &a.pluginUI().fontMoreOpen, func() {
			ui.Column(c).Gap(design.Space2).Children(func() {
				settingsText(c, a.pluginLocalized("More font settings", "更多字体设置"),
					design.TypeBody, t.LabelPrimary).FontWeight(500)
				settingsText(c, a.pluginLocalized(
					"Conversation text supports 14 or 16 px in this build.",
					"此版本的对话文字支持 14 或 16 px。"),
					design.TypeXXS, t.LabelTertiary)
				a.settingsOptionPanel(c, t, &a.pluginUI().fontMoreOpen, []settingsOption{
					{"14 px", cfg.FontSize == session.FontSizeSmall, func() {
						a.updateSettings(c, func(s *session.Settings) { s.FontSize = session.FontSizeSmall })
					}, ""},
					{"16 px", cfg.FontSize == session.FontSizeLarge, func() {
						a.updateSettings(c, func(s *session.Settings) { s.FontSize = session.FontSizeLarge })
					}, ""},
				})
			})
		})
	})
}

// stepFontSize moves the persisted conversation font size by delta, clamped
// to the two sizes the runtime supports.
func (a *App) stepFontSize(c *ui.Context, delta int) {
	cfg := a.currentSettings()
	next := cfg.FontSize + delta
	if next < session.FontSizeSmall {
		next = session.FontSizeSmall
	}
	if next > session.FontSizeLarge {
		next = session.FontSizeLarge
	}
	if next == cfg.FontSize {
		return
	}
	a.updateSettings(c, func(s *session.Settings) { s.FontSize = next })
}

// settingsSwitchLive is the baseline switch with a click target and an
// accessible name (the row text alone is not the control).
func (a *App) settingsSwitchLive(c *ui.Context, t *design.Tokens, on bool, label string, toggle func()) {
	sw := ui.Row(c).Width(36).Height(20).Radius(design.RadiusMax).
		Padding(2).AlignItems(ui.Center).Justify(ui.End).
		Label(label).Role(ui.RoleSwitch).Checked(on).Focusable()
	track, thumb := switchTrack(t, on)
	align := ui.Start
	if on {
		align = ui.End
	}
	sw = sw.Background(track).Justify(align)
	sw.Children(func() {
		ui.Box(c).Width(16).Height(16).Radius(design.RadiusMax).Background(thumb)
	})
	sw.OnClick(toggle)
}

// settingsActionButtonLive is a filled 36px button that runs fn.
func (a *App) settingsActionButtonLive(c *ui.Context, t *design.Tokens, label string, w float32, fn func()) {
	el := ui.Row(c).Width(w).Height(settingsControlH).
		Radius(design.RadiusMD).Background(settingsFillColor(t)).
		Justify(ui.Center).AlignItems(ui.Center).
		Label(label).Role(ui.RoleButton).Focusable()
	el.Children(func() {
		settingsText(c, label, design.TypeBody, t.LabelPrimary).NoWrap()
	})
	el.OnClick(fn)
}

// settingsCloseLive is the header's X button.
func (a *App) settingsCloseLive(c *ui.Context, t *design.Tokens) {
	el := ui.Row(c).Width(28).Height(28).Radius(design.RadiusSM).
		Justify(ui.Center).AlignItems(ui.Center).
		Label(a.tr("Close settings", "关闭设置")).Role(ui.RoleButton).Focusable()
	el.Children(func() {
		ui.Image(c, settingsIconClose).Size(14, 14).TextColor(t.LabelPrimary)
	})
	el.OnClick(func() { a.closeSettings() })
}

// settingsOutlineButtonLive is the bordered header action with a click
// handler, used for "Open configuration file" and the explicit Close.
func (a *App) settingsOutlineButtonLive(c *ui.Context, t *design.Tokens, label string, fn func()) {
	el := ui.Row(c).Height(28).PaddingX(10).Radius(design.RadiusSM).
		Border(1, t.BorderL3).AlignItems(ui.Center).
		Label(label).Tooltip(label).Role(ui.RoleButton).Focusable()
	el.Children(func() {
		settingsText(c, label, design.TypeXXS, t.LabelPrimary)
	})
	el.OnClick(fn)
}

// toggleSelector closes every settings popover, then toggles target. Keeping
// at most one open matches the baseline's modal behaviour.
func (a *App) toggleSelector(target *bool) {
	explicit := *target
	a.closeSelectors()
	*target = !explicit
}

func (a *App) closeSelectors() {
	a.permissionOpen = false
	a.languageOpen = false
	a.fontSizeOpen = false
	a.workOpen = false
	a.collapseOpen = false
	a.sendOpen = false
	a.performanceOpen = false
	a.pluginUI().fontMoreOpen = false
}

func (a *App) permissionLabel(v string) string {
	switch v {
	case session.PermissionFull:
		return a.T("settings.full_access")
	case session.PermissionAsk:
		return a.T("settings.read_only")
	case session.PermissionAutoReview:
		return a.autoReviewLabel()
	default:
		return a.T("settings.workspace_write")
	}
}

func (a *App) languageLabel(v string) string {
	if v == session.LanguageChinese {
		return a.T("settings.language_chinese")
	}
	return a.T("settings.language_english")
}

func (a *App) workDetailsLabel(v string) string {
	if v == session.WorkDetailsStandard {
		return a.T("settings.standard")
	}
	return a.T("settings.detailed")
}

func fontSizeLabel(size int) string {
	if size >= session.FontSizeLarge {
		return "16"
	}
	return "14"
}

func collapseLabel(cfg session.Settings, a *App) string {
	if cfg.CollapseWorkDetail == session.CollapseImmediately {
		return a.T("settings.immediately")
	}
	return a.T("settings.on_completion")
}

func performanceLabel(cfg session.Settings, a *App) string {
	if cfg.PerformanceUsage == session.PerformanceStandard {
		return a.T("settings.standard")
	}
	return a.T("settings.detailed")
}

func sendLabel(cfg session.Settings, a *App) string {
	if cfg.SendBehavior == session.SendInterrupt {
		return a.T("settings.interrupt")
	}
	return a.T("settings.queue")
}
