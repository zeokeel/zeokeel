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
// appearance: Light and Dark force a theme, System follows the desktop. The
// desktop preferences are then applied on top of the brand tokens, so
// HighContrast strengthens the borders and secondary text the screens paint
// with (see preferences.go).
func (a *App) themeTokens(c *ui.Context) *design.Tokens {
	dark := c.Theme().Dark
	switch a.currentSettings().Theme {
	case session.ThemeLight:
		dark = false
	case session.ThemeDark:
		dark = true
	}
	var tokens *design.Tokens
	if dark {
		tokens = design.Dark()
	} else {
		tokens = design.Light()
	}
	return shellTokensForPreferences(tokens, c.Preferences())
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
// The desktop TextScale is applied on top of the user's 14/16px choice: the
// app setting is relative, and the desktop's accessibility text size still
// has to reach the conversation (see preferences.go for the merge rule).
func (a *App) ConversationFontSize() float32 {
	return scaleShellText(conversationTextSize(a.currentSettings()), a.shellTextScaleOf())
}

// ConversationLineHeight returns the matching line box: the brand's 24px at
// 14px, 28px at 16px, scaled by the desktop TextScale together with the size
// so the line box keeps its proportions.
func (a *App) ConversationLineHeight() float32 {
	line := float32(24)
	if a.currentSettings().FontSize >= session.FontSizeLarge {
		line = 28
	}
	return scaleShellText(line, a.shellTextScaleOf())
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

// settingsPanelLive draws the brand settings panel: a centered, responsive
// surface (720-880 DIPs wide, clamped to the window) with the nav column on
// the left and the scrolling page on the right. The panel, nav and rows take
// their color, radius and spacing from the brand theme, not from a sampled
// reference page.
func (a *App) settingsPanelLive(c *ui.Context, t *design.Tokens) {
	if tab := a.shellUI().settingsTab; tab == settingsTabAgentPresets && !a.currentSettings().ShowCodingView {
		a.shellUI().settingsTab = settingsTabGeneral
	}
	th := t.Theme()
	w, h := settingsPanelSize(c)
	panel := ui.Row(c).
		Width(w).Height(h).
		AlignItems(ui.Stretch).
		Background(settingsPanelColor(t)).Radius(th.Radius).
		Border(1, t.BorderL2).Clip().
		Shadow(0, th.Space(4), th.Space(10), 0, ui.RGBA(0, 0, 0, 0.20)).
		Role(ui.RoleDialog).Label(a.T("settings.settings"))
	panel.OnClick(func() {})
	panel.Children(func() {
		a.settingsNavLive(c, t)
		a.settingsBodyLive(c, t)
	})
}

// settingsNavLive is the left column: the brand wordmark-sidebar treatment of
// the settings pages. The nav cells are native button bases, so hover, press,
// focus ring and keyboard activation come from mygo; each page is its own Tab
// stop, matching the per-item reachability the settings screens always had.
func (a *App) settingsNavLive(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	tab := a.shellUI().settingsTab
	_, height := settingsPanelSize(c)
	ui.Column(c).Width(settingsNavWidth(c)).Height(height).
		Background(t.BgSidebar).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).
		Padding(th.Space(4), th.Space(3)).
		Children(func() {
			ui.Row(c).Height(th.Space(12)).PaddingX(th.Space(3)).
				AlignItems(ui.Center).Children(func() {
				settingsText(c, a.T("settings.settings"), design.TypeM, t.LabelPrimary).
					FontWeight(600)
			})
			ui.Column(c).Gap(th.Space(1)).Children(func() {
				a.settingsNavCellLive(c, t, a.T("settings.general"), settingsTabGeneral, tab, settingsIconGear)
				a.settingsNavCellLive(c, t, a.T("settings.models"), settingsTabModels, tab, settingsIconDatabase)
				// Memory is a live-only page: its list comes from the store
				// and the selected workspace.
				if a.Ctrl != nil {
					a.settingsNavCellLive(c, t, a.memoryText("Memory", "记忆"), settingsTabMemory, tab, memoryIcon)
				}
				a.settingsNavCellLive(c, t, a.T("settings.built_in_plugins"), settingsTabBuiltinPlugins, tab, settingsIconSliders)
				// "Show coding view" also governs whether the Agent presets
				// page is offered, per the row's description.
				if a.currentSettings().ShowCodingView {
					a.settingsNavCellLive(c, t, a.T("settings.agent_presets"), settingsTabAgentPresets, tab, settingsIconPerson)
				}
				if a.Ctrl != nil {
					a.settingsNavCellLive(c, t, a.T("menu.about"), settingsTabAbout, tab, pluginIconInfo)
				}
			})
		})
}

// settingsNavCellLive is one nav cell with its click handler.
func (a *App) settingsNavCellLive(c *ui.Context, t *design.Tokens, label string, target, current settingsTab, icon *ui.SVG) {
	cell := a.settingsNavCell(c, t, label, target == current, icon)
	cell.OnClick(func() {
		a.shellUI().settingsTab = target
		a.closeSelectors()
	})
}

// settingsBodyLive is the right column: the header actions and the scrolling
// page content. The page itself starts with its own brand heading; the body
// owns the scroll, the padding and the dialog hosting.
func (a *App) settingsBodyLive(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Column(c).Grow(1).Children(func() {
		a.settingsHeaderLive(c, t)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(th.Space(5), th.Space(6)).Gap(th.Space(4)).Children(func() {
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

// settingsHeaderLive is the panel's top action bar: the configuration file
// shortcut, the explicit Close and the X close button, right aligned so the
// page content keeps the full width below it.
func (a *App) settingsHeaderLive(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).Height(th.Space(14)).
		Padding(th.Space(2), th.Space(6), 0, th.Space(6)).
		Gap(th.Space(2)).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		a.settingsOutlineButtonLive(c, t, a.T("settings.open_configuration_file"),
			func() { a.openConfigurationLocation(c) })
		a.settingsOutlineButtonLive(c, t, a.T("menu.close"),
			func() { a.closeSettings() })
		a.settingsCloseLive(c, t)
	})
}

// settingsSection is one visually grouped block of the General page: a small
// section label over a brand card that holds the rows. Separators come from
// the rows themselves, so a group reads as one native form card.
func (a *App) settingsSection(c *ui.Context, t *design.Tokens, title string, build func()) {
	th := t.Theme()
	ui.Column(c).Gap(th.Space(2)).Children(func() {
		settingsText(c, title, design.TypeXXS, t.LabelTertiary).FontWeight(600)
		ui.Column(c).Background(t.BgStrong).Border(1, t.BorderL2).
			Radius(th.Radius).Children(build)
	})
}

// settingsRowsLive builds Settings > General as brand groups: General,
// Appearance, Work details, Behavior and Advanced. Every row keeps its
// original label, description, accessible name and persisted setting.
func (a *App) settingsRowsLive(c *ui.Context, t *design.Tokens) {
	cfg := a.currentSettings()

	a.settingsSection(c, t, a.T("settings.general"), func() {
		a.settingsRow(c, t, a.T("settings.permission"),
			a.T("settings.choose_the_default_permission_mode_for_new_sessions"),
			settingsRowStyle{},
			func() {
				// A unique accessible name: the session composer behind the
				// modal also shows the permission value, so the bare value is
				// ambiguous to accessibility and to the Tester.
				permission := a.permissionLabel(cfg.Permission)
				a.settingsSelectorLiveNamed(c, t,
					a.menuText("Permission, current: ", "权限，当前：")+permission,
					permission, &a.permissionOpen, []settingsOption{
						{a.T("settings.full_access"), cfg.Permission == session.PermissionFull,
							func() {
								a.updateSettings(c, func(s *session.Settings) { s.Permission = session.PermissionFull })
							}, ""},
						{a.T("settings.workspace_write"), cfg.Permission == session.PermissionWorkspace,
							func() {
								a.updateSettings(c, func(s *session.Settings) { s.Permission = session.PermissionWorkspace })
							}, ""},
						{a.T("settings.read_only"), cfg.Permission == session.PermissionAsk,
							func() {
								a.updateSettings(c, func(s *session.Settings) { s.Permission = session.PermissionAsk })
							}, ""},
						{a.autoReviewLabel(), cfg.Permission == session.PermissionAutoReview,
							func() {
								a.updateSettings(c, func(s *session.Settings) { s.Permission = session.PermissionAutoReview })
							},
							a.autoReviewDescription()},
					})
			})

		a.settingsRow(c, t, a.T("settings.language"), "", settingsRowStyle{}, func() {
			a.settingsSelectLive(c, t, cfg.Language, []settingsChoice{
				{a.T("settings.language_english"), session.LanguageEnglish},
				{a.T("settings.language_chinese"), session.LanguageChinese},
			}, func(value string) {
				a.updateSettings(c, func(s *session.Settings) { s.Language = value })
			})
		})

		a.settingsRow(c, t, a.T("settings.keyboard_shortcuts"),
			a.T("settings.view_and_edit_available_shortcuts_and_input_actions"),
			settingsRowStyle{last: true},
			func() {
				a.settingsActionButtonLive(c, t, a.T("settings.edit_shortcuts"), func() {
					a.shortcutsQuery = ""
					a.shortcutEditID = ""
					a.openShortcutsDialog()
				})
			})
	})

	a.settingsAppearanceLive(c, t, cfg)

	a.settingsSection(c, t, a.T("settings.behavior"), func() {
		a.settingsRow(c, t, a.T("settings.work_details"),
			a.T("settings.choose_how_much_detail_to_show_for_tool_calls"),
			settingsRowStyle{},
			func() {
				a.settingsSelectLive(c, t, cfg.WorkDetails, []settingsChoice{
					{a.T("settings.standard"), session.WorkDetailsStandard},
					{a.T("settings.detailed"), session.WorkDetailsDetailed},
				}, func(value string) {
					a.updateSettings(c, func(s *session.Settings) { s.WorkDetails = value })
				})
			})

		a.settingsRow(c, t, a.T("settings.when_to_collapse_work_details"),
			a.T("settings.choose_when_to_automatically_collapse_work_details"),
			settingsRowStyle{},
			func() {
				a.settingsSelectLive(c, t, cfg.CollapseWorkDetail, []settingsChoice{
					{a.T("settings.on_completion"), session.CollapseOnCompletion},
					{a.T("settings.immediately"), session.CollapseImmediately},
				}, func(value string) {
					a.updateSettings(c, func(s *session.Settings) { s.CollapseWorkDetail = value })
				})
			})

		a.settingsRow(c, t, a.T("settings.show_coding_view"),
			a.T("settings.shows_trajectory_code_diffs_and_all_agent_presets"),
			settingsRowStyle{descColor: t.LabelSecondary},
			func() {
				a.settingsSwitchLive(c, t, cfg.ShowCodingView,
					a.tr("Toggle show coding view", "切换显示编码视图"), func() {
						a.updateSettings(c, func(s *session.Settings) { s.ShowCodingView = !s.ShowCodingView })
					})
			})

		a.settingsRow(c, t, a.T("settings.send_behavior_while_busy"),
			a.T("settings.what_enter_and_the_send_button_do_while_the_agent_is_running_cmd"),
			settingsRowStyle{},
			func() {
				a.settingsSelectLive(c, t, cfg.SendBehavior, []settingsChoice{
					{a.T("settings.queue"), session.SendQueue},
					{a.T("settings.interrupt"), session.SendInterrupt},
				}, func(value string) {
					a.updateSettings(c, func(s *session.Settings) { s.SendBehavior = value })
				})
			})

		a.settingsRow(c, t, a.T("settings.performance_usage"),
			a.T("settings.performance_usage_desc"),
			settingsRowStyle{last: true},
			func() {
				a.settingsSelectLive(c, t, cfg.PerformanceUsage, []settingsChoice{
					{a.T("settings.detailed"), session.PerformanceDetailed},
					{a.T("settings.standard"), session.PerformanceStandard},
				}, func(value string) {
					a.updateSettings(c, func(s *session.Settings) { s.PerformanceUsage = value })
				})
			})
	})

	a.settingsSection(c, t, a.T("settings.advanced"), func() {
		desc := a.tr("Not available in this build: no session-log upload endpoint is configured.",
			"此版本不可用：尚未配置会话日志上传端点。")
		a.settingsRow(c, t, a.T("settings.upload_session_log"), desc,
			settingsRowStyle{last: true},
			func() {
				// There is no official-API upload path in this build, so the
				// switch is disabled: turning it on must not persist a no-op.
				a.settingsDisabledSwitchLive(c, t, cfg.UploadSessionLog,
					a.tr("Upload session log", "上传会话日志"))
			})
	})
}

// settingsAppearanceLive is the appearance group: three theme cards and the
// conversation font size control, all on the brand surface.
func (a *App) settingsAppearanceLive(c *ui.Context, t *design.Tokens, cfg session.Settings) {
	th := t.Theme()
	a.settingsSection(c, t, a.T("settings.appearance"), func() {
		ui.Row(c).Padding(th.Space(3), th.Space(4)).Gap(th.Space(3)).
			BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
			a.settingsThemeCardLive(c, t, a.T("settings.light"), session.ThemeLight, cfg.Theme == session.ThemeLight, settingsIconSun)
			a.settingsThemeCardLive(c, t, a.T("settings.dark"), session.ThemeDark, cfg.Theme == session.ThemeDark, settingsIconMoon)
			a.settingsThemeCardLive(c, t, a.T("settings.system"), session.ThemeSystem, cfg.Theme == session.ThemeSystem, settingsIconMonitor)
		})
		a.settingsRow(c, t, a.T("settings.font_size"),
			a.T("settings.only_affects_conversation_content"),
			settingsRowStyle{last: true},
			func() { a.settingsFontSizeLive(c, t, cfg) })
	})
}

// settingsThemeCardLive is one brand appearance card: the native radio
// semantics stay (RoleRadio + Checked + Focusable), while the look is the
// brand surface, radius and accent.
func (a *App) settingsThemeCardLive(c *ui.Context, t *design.Tokens, label, value string, selected bool, icon *ui.SVG) {
	th := t.Theme()
	card := ui.Column(c).Grow(1).Height(th.Space(14)).
		Radius(th.Radius).Border(1, settingsCardOutline(t)).
		Gap(th.Space(1)).AlignItems(ui.Center).Justify(ui.Center).
		Label(label).Value(label).Role(ui.RoleRadio).Checked(selected).Focusable()
	if selected {
		card = card.Background(t.Accent.Alpha(0.08)).Border(2, t.Accent)
	} else {
		card = card.Background(t.BgRaised)
		if card.Hovered() {
			card = card.Background(settingsFillColor(t))
		}
	}
	card.Children(func() {
		iconColor := t.LabelSecondary
		if selected {
			iconColor = t.Accent
		}
		ui.Image(c, icon).Size(th.Space(4), th.Space(4)).TextColor(iconColor)
		settingsText(c, label, design.TypeBody, t.LabelPrimary)
	})
	card.OnClick(func() {
		a.updateSettings(c, func(s *session.Settings) { s.Theme = value })
	})
}

// settingsChoice is one option of a native settings select: the localized
// label shown in the popup and the persisted value it writes back.
type settingsChoice struct {
	label string
	value string
}

// settingsSelectLive draws a mygo native Select for the persisted value: the
// drop-down, keyboard (arrows/Enter/Escape), focus and a11y come from the
// control itself. The choice is written back through save on the frame it
// changes, so the row reads like a native preferences control.
func (a *App) settingsSelectLive(c *ui.Context, t *design.Tokens, value string, choices []settingsChoice, save func(string)) {
	th := t.Theme()
	labels := make([]string, len(choices))
	current := ""
	for i, choice := range choices {
		labels[i] = choice.label
		if choice.value == value {
			current = choice.label
		}
	}
	if current == "" && len(labels) > 0 {
		current = labels[0]
	}
	sel := ui.Select(c, &current, labels)
	sel.Height(th.Space(9)).MinWidth(th.Space(36)).Radius(design.RadiusSM)
	if sel.Changed() {
		for _, choice := range choices {
			if choice.label == current {
				save(choice.value)
				return
			}
		}
	}
}

// settingsSelectorLive draws the branded select trigger for the selectors the
// native Select cannot express: the permission menu lists descriptions under
// its options. The popover stays; the chrome is the brand surface.
func (a *App) settingsSelectorLive(c *ui.Context, t *design.Tokens, label string, w float32, open *bool, options []settingsOption) {
	a.settingsSelectorLiveNamed(c, t, label, label, open, options)
}

// settingsSelectorLiveNamed separates the visible value from the accessible
// name, so a selector can say "Permission, current: Workspace Write" without
// changing its on-screen text.
func (a *App) settingsSelectorLiveNamed(c *ui.Context, t *design.Tokens, accessible, label string, open *bool, options []settingsOption) {
	th := t.Theme()
	el := ui.ButtonBase(c).Height(th.Space(9)).PaddingX(th.Space(3)).
		Gap(th.Space(2)).MinWidth(th.Space(36)).Justify(ui.SpaceBetween).
		Radius(design.RadiusSM).
		Background(settingsControlColor(t)).
		Label(accessible).Value(label).Role(ui.RolePopUpButton).Focusable()
	if el.Hovered() {
		el = el.Background(settingsFillColor(t))
	}
	el.Children(func() {
		settingsText(c, label, design.TypeBody, t.LabelPrimary).NoWrap()
		ui.Image(c, settingsIconChevron).Size(th.Space(3), th.Space(3)).TextColor(t.LabelSecondary)
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
	th := t.Theme()
	ui.Column(c).Gap(1).Children(func() {
		for _, option := range options {
			height := th.Space(8)
			if option.desc != "" {
				height = th.Space(12)
			}
			row := ui.ButtonBase(c).FillWidth().Height(height).
				PaddingX(th.Space(2)).Gap(th.Space(2)).
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

// settingsFontSizeLive is the conversation font size control: a brand value
// pill that opens the 14/16 choice, the native-styled step arrows and the
// "More font settings" popover, all on the theme's 4 DIP grid.
func (a *App) settingsFontSizeLive(c *ui.Context, t *design.Tokens, cfg session.Settings) {
	th := t.Theme()
	ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
		pill := ui.ButtonBase(c).Height(th.Space(9)).PaddingX(th.Space(3)).
			Gap(th.Space(1)).Radius(design.RadiusSM).
			Background(settingsControlColor(t)).
			Label(fontSizeLabel(cfg.FontSize) + " px").
			Role(ui.RoleButton).Focusable()
		if pill.Hovered() {
			pill = pill.Background(settingsFillColor(t))
		}
		pill.Children(func() {
			settingsText(c, fontSizeLabel(cfg.FontSize), design.TypeBody, t.LabelPrimary)
			settingsText(c, "px", design.TypeXXS, t.LabelTertiary)
			ui.Image(c, settingsIconChevron).Size(th.Space(3), th.Space(3)).TextColor(t.LabelSecondary)
		})
		pill.Expanded(a.fontSizeOpen)
		pill.OnClick(func() { a.toggleSelector(&a.fontSizeOpen) })
		ui.Popover(c, pill, &a.fontSizeOpen, func() {
			a.settingsOptionPanel(c, t, &a.fontSizeOpen, []settingsOption{
				{"14 px", cfg.FontSize == session.FontSizeSmall, func() {
					a.updateSettings(c, func(s *session.Settings) { s.FontSize = session.FontSizeSmall })
				}, ""},
				{"16 px", cfg.FontSize == session.FontSizeLarge, func() {
					a.updateSettings(c, func(s *session.Settings) { s.FontSize = session.FontSizeLarge })
				}, ""},
			})
		})
		// The step buttons are native button bases: click, keyboard
		// activation and focus ring come from the control. They sit in one
		// bordered column, as a desktop stepper does.
		ui.Column(c).Gap(1).Padding(1).Radius(design.RadiusSM).
			Border(1, t.BorderL2).Children(func() {
			inc := ui.ButtonBase(c).Width(th.Space(5)).Height(th.Space(4)).
				Radius(design.RadiusXS).
				Label(a.pluginLocalized("Increase font size", "增大字号")).
				Role(ui.RoleButton).Focusable()
			if inc.Hovered() || inc.Focused() {
				inc = inc.Background(t.BgHover)
			}
			inc.Children(func() {
				settingsText(c, "+", design.TypeXXS, t.LabelTertiary)
			})
			inc.OnClick(func() { a.stepFontSize(c, 2) })
			dec := ui.ButtonBase(c).Width(th.Space(5)).Height(th.Space(4)).
				Radius(design.RadiusXS).
				Label(a.pluginLocalized("Decrease font size", "减小字号")).
				Role(ui.RoleButton).Focusable()
			if dec.Hovered() || dec.Focused() {
				dec = dec.Background(t.BgHover)
			}
			dec.Children(func() {
				settingsText(c, "−", design.TypeXXS, t.LabelTertiary)
			})
			dec.OnClick(func() { a.stepFontSize(c, -2) })
		})
		more := ui.ButtonBase(c).Size(th.Space(7), th.Space(7)).
			Radius(design.RadiusSM).
			Label(a.pluginLocalized("More font settings", "更多字体设置")).
			Role(ui.RoleButton).Focusable()
		if more.Hovered() {
			more = more.Background(t.BgHover)
		}
		more.Children(func() {
			ui.Image(c, settingsIconSliders).Size(th.Space(3), th.Space(3)).TextColor(t.LabelTertiary)
		})
		more.Expanded(a.pluginUI().fontMoreOpen)
		more.OnClick(func() {
			opened := a.pluginUI().fontMoreOpen
			a.closeSelectors()
			a.pluginUI().fontMoreOpen = !opened
		})
		ui.Popover(c, more, &a.pluginUI().fontMoreOpen, func() {
			ui.Column(c).Gap(th.Space(2)).Padding(th.Space(1)).Children(func() {
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

// settingsSwitchLive is the native mygo switch with the row's accessible name.
// The control owns the toggle semantics, focus ring and keyboard activation;
// the persisted setting is written when it changes.
func (a *App) settingsSwitchLive(c *ui.Context, t *design.Tokens, on bool, label string, toggle func()) {
	value := on
	sw := ui.Switch(c, &value).Label(label)
	if sw.Changed() {
		toggle()
	}
}

// settingsDisabledSwitchLive is a native switch that cannot be turned on: the
// upload path it controls does not exist in this build.
func (a *App) settingsDisabledSwitchLive(c *ui.Context, t *design.Tokens, on bool, label string) {
	value := on
	ui.Switch(c, &value).Label(label).Disabled(true)
}

// settingsActionButtonLive is a native-style secondary button that runs fn.
func (a *App) settingsActionButtonLive(c *ui.Context, t *design.Tokens, label string, fn func()) {
	th := t.Theme()
	el := ui.ButtonBase(c).Height(th.Space(9)).PaddingX(th.Space(4)).
		Radius(design.RadiusSM).Border(1, t.BorderL3).
		Background(settingsControlColor(t)).
		Label(label).Role(ui.RoleButton).Focusable()
	if el.Hovered() {
		el = el.Background(settingsFillColor(t))
	}
	el.Children(func() {
		settingsText(c, label, design.TypeBody, t.LabelPrimary).NoWrap()
	})
	el.OnClick(fn)
}

// settingsCloseLive is the header's X button.
func (a *App) settingsCloseLive(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	el := ui.ButtonBase(c).Size(th.Space(8), th.Space(8)).
		Radius(design.RadiusSM).
		Label(a.tr("Close settings", "关闭设置")).Role(ui.RoleButton).Focusable()
	if el.Hovered() {
		el = el.Background(t.BgHover)
	}
	el.Children(func() {
		ui.Image(c, settingsIconClose).Size(th.Space(3.5), th.Space(3.5)).TextColor(t.LabelPrimary)
	})
	el.OnClick(func() { a.closeSettings() })
}

// settingsOutlineButtonLive is the header's bordered secondary action.
func (a *App) settingsOutlineButtonLive(c *ui.Context, t *design.Tokens, label string, fn func()) {
	th := t.Theme()
	el := ui.ButtonBase(c).Height(th.Space(8)).PaddingX(th.Space(3)).
		Radius(design.RadiusSM).Border(1, t.BorderL2).
		Label(label).Tooltip(label).Role(ui.RoleButton).Focusable()
	if el.Hovered() {
		el = el.Background(t.BgHover)
	}
	el.Children(func() {
		settingsText(c, label, design.TypeXS, t.LabelPrimary).NoWrap()
	})
	el.OnClick(fn)
}

// toggleSelector closes every settings popover, then toggles target. Keeping
// at most one open matches the modal behaviour.
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

func fontSizeLabel(size int) string {
	if size >= session.FontSizeLarge {
		return "16"
	}
	return "14"
}
