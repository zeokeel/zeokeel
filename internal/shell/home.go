package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/session"
)

// Home layout: a blank 40px header over the main area, then the hero (logo,
// title, badge, workspace/mode chips) and the composer, centered as one
// column. The geometry values below are from the retired DSH reference design
// (archived 2026-11-05); the native visual redesign will replace them.
const (
	homeHeaderHeight   float32 = 40
	homeHeroHeight     float32 = 44
	homeStackWidth     float32 = 909
	homeComposerWidth  float32 = 877
	homeInputHeight    float32 = 52
	homeToolbarHeight  float32 = 42
	homeComposerRadius float32 = 28
	// The baseline card has a 0.5px outside ring; a 1px inside border lands
	// on the same pixels, so the card box grows and the gaps shrink by it.
	homeCardGap  float32 = 7
	homeCardFoot float32 = 31
	// homeGutter is the scrollbar gutter the baseline reserves to the right
	// of the main column; the centered hero column sits inside it.
	homeGutter float32 = 7
)

// Colors the shared tokens do not expose (sampled from the retired DSH
// reference design).
var (
	homePreviewBg = ui.Hex("#E4EDFD")
	homePreviewFg = ui.Hex("#0E3074")

	homeShadowDrop = ui.RGBA(0, 0, 0, 0.03)
	homeSendIdle   = ui.Hex("#B2C7F4") // the baseline's disabled send fill
	homeTagFg      = ui.Hex("#4176E6") // the baseline's accent on the 1017 tag

	// Dark variants of the same retired reference samples.
	homePreviewBgDark = ui.Hex("#34415B")
	homePreviewFgDark = ui.Hex("#F9FAFB")

	homeTagFgDark = ui.Hex("#7AAAFF")
)

func homePreviewColors(t *design.Tokens) (ui.Color, ui.Color) {
	if t.Dark {
		return homePreviewBgDark, homePreviewFgDark
	}
	return homePreviewBg, homePreviewFg
}

func homeTagAccent(t *design.Tokens) ui.Color {
	if t.Dark {
		return homeTagFgDark
	}
	return homeTagFg
}

// SVG icons used by the home screen, drawn on the baseline's 16px grid
// with the same thin line weight.
var (
	homeIconWhale = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="50" height="50" viewBox="0.6 6.6 48.8 36" fill="none"><path d="M48.8354 10.0479C48.3232 9.79199 48.1025 10.2798 47.8032 10.5278C47.7007 10.6079 47.6143 10.7119 47.5273 10.8076C46.7793 11.624 45.9048 12.1597 44.7622 12.0957C43.0923 12 41.666 12.5356 40.4058 13.8398C40.1377 12.2319 39.2476 11.272 37.8926 10.6558C37.1836 10.3359 36.4668 10.0156 35.9702 9.31982C35.6235 8.82373 35.5293 8.27197 35.356 7.72754C35.2456 7.3999 35.1353 7.06396 34.7651 7.00781C34.3633 6.94385 34.2056 7.2876 34.0479 7.57568C33.418 8.75195 33.1733 10.0479 33.1973 11.3599C33.2524 14.312 34.4736 16.6641 36.8999 18.3359C37.1758 18.5278 37.2466 18.7197 37.1597 19C36.9946 19.5757 36.7974 20.1357 36.624 20.7119C36.5137 21.0801 36.3486 21.1597 35.9624 21C34.6309 20.4321 33.481 19.5918 32.4644 18.5757C30.7393 16.8721 29.1792 14.9917 27.2334 13.52C26.7764 13.1758 26.3193 12.856 25.8467 12.5518C23.8618 10.584 26.1069 8.96777 26.627 8.77588C27.1704 8.57568 26.8159 7.8877 25.0591 7.896C23.3022 7.90381 21.6953 8.50391 19.647 9.30371C19.3477 9.42383 19.0322 9.51172 18.7095 9.58398C16.8501 9.22363 14.9199 9.14355 12.9033 9.37598C9.10596 9.80762 6.07275 11.6396 3.84326 14.7681C1.16455 18.5278 0.53418 22.7998 1.30664 27.2559C2.11768 31.9521 4.46582 35.8398 8.07373 38.8799C11.8159 42.0322 16.1255 43.5762 21.041 43.2803C24.0269 43.104 27.3516 42.6963 31.1016 39.4561C32.0469 39.936 33.0396 40.1279 34.686 40.272C35.9546 40.3921 37.1758 40.208 38.1211 40.0078C39.6021 39.688 39.4995 38.2881 38.9639 38.0322C34.623 35.9678 35.5762 36.8081 34.71 36.1279C36.9155 33.4639 40.2402 30.6958 41.54 21.728C41.6426 21.0161 41.5557 20.5679 41.54 19.9917C41.5322 19.6396 41.6108 19.5039 42.0049 19.4639C43.0923 19.3359 44.1479 19.0317 45.1167 18.4878C47.9292 16.9199 49.064 14.3438 49.3315 11.2559C49.3711 10.7837 49.3237 10.2959 48.8354 10.0479ZM24.3262 37.8398C20.1196 34.4639 18.0791 33.3521 17.2358 33.3999C16.4482 33.4482 16.5898 34.3682 16.7632 34.9678C16.9443 35.5601 17.1812 35.9683 17.5117 36.4878C17.7402 36.832 17.8979 37.3442 17.2832 37.728C15.9282 38.584 13.5728 37.4399 13.4624 37.3838C10.7207 35.7358 8.42822 33.5601 6.81348 30.584C5.25342 27.7197 4.34766 24.6479 4.19775 21.3677C4.1582 20.5757 4.38672 20.2959 5.15869 20.1519C6.17529 19.96 7.22314 19.9199 8.23926 20.0718C12.5327 20.7119 16.1885 22.6719 19.2529 25.7759C21.002 27.5439 22.3252 29.6558 23.6885 31.7202C25.1377 33.9121 26.6978 36 28.6831 37.7119C29.3843 38.312 29.9434 38.7681 30.479 39.104C28.8643 39.2881 26.1699 39.3281 24.3262 37.8398ZM26.3433 24.6001C26.3433 24.248 26.6191 23.9678 26.9658 23.9678C27.0444 23.9678 27.1152 23.9839 27.1782 24.0078C27.2651 24.04 27.3438 24.0879 27.4067 24.1602C27.5171 24.272 27.5801 24.4321 27.5801 24.6001C27.5801 24.9521 27.3042 25.2319 26.9575 25.2319C26.6108 25.2319 26.3433 24.9521 26.3433 24.6001ZM32.6064 27.8799C32.2046 28.0479 31.8027 28.1919 31.4165 28.208C30.8179 28.2397 30.1641 27.9922 29.8096 27.688C29.2583 27.2158 28.8643 26.9521 28.6987 26.1279C28.6279 25.7759 28.6675 25.2319 28.7305 24.9199C28.8721 24.248 28.7144 23.8159 28.2495 23.4238C27.8716 23.104 27.3911 23.0161 26.8633 23.0161C26.666 23.0161 26.4849 22.9277 26.3511 22.856C26.1304 22.7441 25.9492 22.4639 26.1226 22.1201C26.1777 22.0078 26.4458 21.7358 26.5088 21.688C27.2256 21.272 28.0527 21.4077 28.8169 21.7197C29.5259 22.0161 30.0615 22.5601 30.834 23.3281C31.6216 24.2559 31.7632 24.5117 32.2124 25.208C32.5669 25.752 32.8901 26.312 33.1104 26.9521C33.2446 27.3521 33.0713 27.6802 32.6064 27.8799Z" fill="currentColor"/></svg>`))

	homeIconChevron = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M4 6.2l4 3.6 4-3.6"/></svg>`))

	homeIconFolder = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none"><path d="M1.6 4.6a1.1 1.1 0 0 1 1.1-1.1h3.1l1.7 1.7h5.8a1.1 1.1 0 0 1 1.1 1.1v6.1a1.1 1.1 0 0 1-1.1 1.1H2.7a1.1 1.1 0 0 1-1.1-1.1z" fill="currentColor" fill-opacity="0.13" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" stroke-linejoin="round"/></svg>`))

	homeIconTune = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3.4 13 6.3v5.4L8 14.6 3 11.7V6.3z"/><circle cx="8" cy="1.9" r="1.3"/><circle cx="2.4" cy="12.5" r="1.3"/><circle cx="13.6" cy="12.5" r="1.3"/></svg>`))

	homeIconPlus = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M8 2.8v10.4M2.8 8h10.4"/></svg>`))

	homeIconShield = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"><path d="M8 1.4l5.2 1.9v3.6c0 3.3-2.2 5.7-5.2 6.9-3-1.2-5.2-3.6-5.2-6.9V3.3z"/></svg>`))

	homeIconArrowUp = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M8 14.3V2.5M3 7.4l5-4.9 5 4.9"/></svg>`))

	homeIconPanel = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><rect x="1.2" y="1.2" width="13.6" height="13.6" rx="2.4"/><path d="M10.6 1.2v13.6"/></svg>`))
)

// home draws the Home screen. The live controller-backed renderer is the
// only one; the retired static baseline render was deleted (pixel parity
// retired 2026-11-05).
func (a *App) home(c *ui.Context, t *design.Tokens) {
	a.homeLive(c, t)
}

// homeLive is the controller-backed home screen: the workspace chip reflects
// the real workspace (or offers to create one), and the composer creates a
// session in that workspace and submits the first message.
func (a *App) homeLive(c *ui.Context, t *design.Tokens) {
	design.Apply(ui.Column(c), t.Main()).Children(func() {
		a.homeHeader(c, t)
		ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).PaddingEnd(homeGutter).Children(func() {
			ui.Column(c).Width(a.homeStackWidthFor(c)).Children(func() {
				a.homeHero(c, t)
				ui.Box(c).Height(design.Space3)
				a.homeLiveChips(c, t)
				ui.Box(c).Height(homeCardGap)
				a.homeLiveComposer(c, t)
				ui.Box(c).Height(homeCardFoot)
			})
		})
	})
}

func (a *App) homeLiveChips(c *ui.Context, t *design.Tokens) {
	ctx := context.Background()
	ws, ok, _ := a.Ctrl.CurrentWorkspace(ctx)
	label := a.T("home.choose_workspace")
	if ok && strings.TrimSpace(ws.Name) != "" {
		label = ws.Name
	}
	ui.Row(c).Height(28).Padding(0, design.Space4, 0, design.Space5).Gap(design.Space1).
		Children(func() {
			chipLabel := a.T("home.choose_workspace")
			if ok && strings.TrimSpace(ws.Name) != "" {
				chipLabel = a.menuText("Workspace, current: ", "工作区，当前：") + label
			}
			wsChip := ui.Row(c).Height(28).PaddingX(design.Space2).Gap(design.Space1).
				Radius(design.RadiusSM).AlignItems(ui.Center).
				Label(chipLabel).Value(label).
				Role(ui.RolePopUpButton).Focusable()
			wsChip.Children(func() {
				ui.Image(c, homeIconFolder).Size(16, 16).TextColor(t.LabelPrimary)
				homeText(c, label).FontSize(13).FixedLineHeight(20).FontWeight(500).TextColor(t.LabelPrimary)
				ui.Icon(c, homeIconChevron).Size(12, 12).TextColor(t.LabelTertiary)
			})
			wsChip.Expanded(a.wsPickerOpen)
			wsChip.OnClick(func() { a.chooseWorkspaceFromHome() })
			ui.Popover(c, wsChip, &a.wsPickerOpen, func() { a.homeWorkspacePicker(c, t) })
			mst := a.modeUI()
			modeText := a.agentModeLabel(a.homeAgentMode())
			if a.homeAgentMode() == "standard" {
				modeText = a.T("common.standard_mode")
			}
			mode := ui.Row(c).Height(28).PaddingX(design.Space2).Gap(design.Space1).
				Radius(design.RadiusSM).AlignItems(ui.Center).
				Label(modeText).Value(modeText).Role(ui.RolePopUpButton).Focusable()
			mode.Children(func() {
				ui.Icon(c, homeIconTune).Size(16, 16).TextColor(t.LabelPrimary)
				homeText(c, modeText).FontSize(13).FixedLineHeight(20).
					FontWeight(500).TextColor(t.LabelPrimary)
				ui.Icon(c, homeIconChevron).Size(14, 14).TextColor(t.LabelTertiary)
			})
			mode.Expanded(mst.open)
			mode.OnClick(func() { mst.open = !mst.open })
			ui.Popover(c, mode, &mst.open, func() { a.agentModeMenu(c, t, &mst.open) })
			if mst.err != "" {
				ui.Text(c, mst.err).FontSize(11).FixedLineHeight(16).
					TextColor(t.Danger).MaxLines(2).Ellipsis("…")
			}
		})
}

// chooseWorkspaceFromHome opens the add-workspace dialog when no workspace
// exists, otherwise a small picker of known workspaces.
func (a *App) chooseWorkspaceFromHome() {
	if a.Ctrl == nil {
		return
	}
	ctx := context.Background()
	workspaces, err := a.Ctrl.ListWorkspaces(ctx)
	if err != nil || len(workspaces) == 0 {
		a.beginAddWorkspace()
		return
	}
	a.wsPickerOpen = !a.wsPickerOpen
}

func (a *App) homeLiveComposer(c *ui.Context, t *design.Tokens) {
	if a.attachOpen {
		a.attachmentMenu(c, t)
	}
	// The Home composer accepts the same "/" command and "@" file mention
	// palettes as the session composer, as its placeholder promises.
	if a.slashMenuVisible() {
		a.commandPalette(c, t)
	} else if entries := a.visibleMentionEntries(); len(entries) > 0 {
		a.mentionMenu(c, t, entries)
	}
	// Files picked on Home belong to the Home draft until Send creates their
	// session. Render their chips here, above the composer, so the user can
	// see and remove them just like the session composer's pending chips.
	if snap := a.homeAttachmentSnapshot(); snap != nil {
		ui.Row(c).Width(a.homeStackWidthFor(c)).Padding(0, 14, design.Space2, 15).
			Children(func() {
				a.pendingAttachmentChips(c, t, snap)
			})
	}
	ui.Row(c).Width(a.homeStackWidthFor(c)).Padding(0, 14, 0, 15).Children(func() {
		design.Apply(ui.Column(c), t.Panel()).
			Label(a.tr("Home composer", "主页输入框")).
			Width(a.homeComposerWidthFor(c)+2).
			Radius(homeComposerRadius).
			Border(1, t.BorderL2).
			Shadow(0, 4, 12, 0, homeShadowDrop).
			Shadow(0, 0, 18, 0, homeShadowDrop).
			Padding(design.Space2, 0, 0, 0).
			Gap(design.Space3).
			Children(func() {
				// DSH's 877px card carries an 873px input: two pixels of
				// inset per side, not 14, which also keeps the input at the
				// baseline's 357px inside the 361px narrow card.
				ui.Row(c).Height(homeInputHeight).Padding(5, 1, 0, 1).
					AlignItems(ui.Start).Children(func() {
					// Multiline: Enter sends and Shift+Enter inserts a line
					// break, as DSH's composer does.
					ui.TextAreaBase(c, &a.draft).Label(a.T("session.composer_input")).
						Placeholder(a.T("home.composer_placeholder")).FontSize(14).
						MinHeight(28).Grow(1).Bind(&a.composerRef).
						OnTextKey(a.composerTextKey(c, true))
				})
				ui.Row(c).Height(homeToolbarHeight).Padding(2, design.Space2, 6, design.Space2).
					AlignItems(ui.Center).Children(func() {
					plus := ui.Button(c, "").Tooltip(a.T("composer.add_files_or_run_commands")).
						Label(a.T("composer.add_files_or_run_commands"))
					plus.Children(func() {
						ui.Icon(c, icons.Plus()).Size(14, 14).TextColor(t.LabelSecondary)
					})
					if b := plus; b.Clicked() {
						a.attachOpen = !a.attachOpen
					}
					ui.Box(c).Width(design.Space3)
					a.livePermissionChip(c, t)
					ui.Spacer(c).Grow(1)
					a.liveModelMenuBadge(c, t)
					ui.Box(c).Width(design.Space6)
					empty := strings.TrimSpace(a.draft) == ""
					sendBg := t.Accent
					sendFg := t.AccentText
					if empty {
						// DSH renders the Send control disabled until the
						// draft has content; the theme-aware disabled pair
						// matches the light baseline and the dark capture.
						sendBg = sendIdleFill(t)
						sendFg = sendIdleGlyph(t)
					}
					// The fill lives on an enabled parent so the disabled
					// state does not dim the baseline's pale surface; the
					// focusable control inside carries Label/Disabled, so an
					// empty composer cannot be tabbed to or activated.
					send := ui.Row(c).Size(34, 34).Radius(design.RadiusMax).
						Background(sendBg).Center()
					send.Children(func() {
						// The focusable/clickable row fills the whole 34x34
						// circle. Growing only horizontally left the top and
						// bottom 9px of the visible button dead.
						button := ui.Row(c).Size(34, 34).Center().
							Label(a.T("composer.send_message")).
							Role(ui.RoleButton).Focusable().Disabled(empty)
						button.Children(func() {
							ui.Icon(c, homeIconArrowUp).Size(16, 16).TextColor(sendFg)
						})
						button.OnClick(func() {
							if !empty {
								a.sendHomeDraft(c)
							}
						})
					})
				})
			})
	})
}

// sendHomeDraft creates a session in the current workspace (or asks for one
// first) and submits the draft as its first message.
func (a *App) sendHomeDraft(c *ui.Context) {
	if a.Ctrl == nil {
		return
	}
	// An empty submit must not create a session (DSH's Send is disabled);
	// check before touching the workspace or store.
	if strings.TrimSpace(a.draft) == "" {
		return
	}
	ctx := context.Background()
	ws, ok, err := a.Ctrl.CurrentWorkspace(ctx)
	if err != nil || !ok || strings.TrimSpace(ws.ID) == "" {
		a.beginAddWorkspace()
		return
	}
	id, err := a.Ctrl.CreateSessionInWorkspace(ctx, ws.ID, a.T("session.new_session"))
	if err != nil {
		return
	}
	snap, err := a.Ctrl.OpenSession(ctx, id)
	if err != nil {
		return
	}
	a.SetSnapshot(snap)
	// Apply the Home composer's staged preset, access mode and model to the
	// session it just created; the last-opened session and the global
	// defaults are untouched.
	a.applyHomeComposerChoices(id)
	// The Home composer stages its files with the Home draft because no
	// session exists while it is being typed. Copy them onto the session this
	// Send just created before its first Submit, so the turn carries them
	// instead of leaving them on the previous session.
	if err := a.attachHomeAttachments(id); err != nil {
		// Never send a turn that silently drops the staged files, and never
		// leave the empty session behind: keep the Home draft and its chips so
		// the user can correct the pick and retry.
		_ = a.Ctrl.DeleteSession(ctx, id)
		a.SetSnapshot(nil)
		a.Screen = Home
		c.Toast(fmt.Sprintf(a.menuText("Could not attach file: %v",
			"无法添加附件：%v"), err))
		return
	}
	a.Screen = Session
	text := strings.TrimSpace(a.draft)
	if err := a.Ctrl.Submit(ctx, id, text); err != nil {
		// Keep the unsent text on the new session's own draft rather than
		// dropping it (or letting the Home buffer follow into the session).
		a.saveSessionDraft(id, a.draft)
		if errors.Is(err, session.ErrNoAgent) {
			c.Toast(a.menuText("No agent configured. Add a model in Settings > Models.",
				"尚未配置 Agent。请在 设置 > 模型 中添加模型。"))
		} else {
			c.Toast(fmt.Sprintf(a.menuText("Could not send: %v", "发送失败：%v"), err))
		}
		return
	}
	a.draft = ""
	a.homeDraft = ""
}

// homeHeader is the blank top strip of the main area; the baseline keeps a
// single "open right sidebar" button at its right edge. The live shell wires
// it to the real panel, which the Home screen now shares with Session.
func (a *App) homeHeader(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(homeHeaderHeight).Padding(design.Space2, design.Space3, 0, design.Space5).
		AlignItems(ui.Center).Children(func() {
		ui.Spacer(c).Grow(1)
		label := a.tr("Open right sidebar", "打开右侧边栏")
		if a.Ctrl != nil && a.rightOpen {
			// Distinct from the right panel's own 28x28 collapse control.
			label = a.tr("Collapse right sidebar header", "收起右侧边栏（标题栏）")
		}
		button := ui.Row(c).Size(28, 28).Radius(design.RadiusSM).Center().
			Label(label).Role(ui.RoleButton).Focusable()
		if button.Hovered() {
			button = button.Background(t.BgHover)
		}
		// The static demo keeps the baseline's exact glyph; the live shell
		// uses the right-panel artwork the control actually means.
		icon := homeIconPanel
		if a.Ctrl != nil {
			icon = chromeIconPanelRight
		}
		button.Children(func() {
			ui.Icon(c, icon).Size(15, 15).TextColor(t.LabelSecondary)
		})
		button.OnClick(func() {
			if a.Ctrl == nil {
				return
			}
			if a.rightOpen {
				a.collapseRightSidebar()
				return
			}
			a.rightOpen = true
		})
	})
}

// homeFont is the family home text uses; the empty string asks for the
// system's interface font.
const homeFont = ""

func homeText(c *ui.Context, s string) ui.Element { return ui.Text(c, s).Font(homeFont) }

// homeHero is the "Into the Unknown" headline with the baseline's whale
// mark and the Preview badge.
func (a *App) homeHero(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(homeHeroHeight).AlignItems(ui.Start).Justify(ui.Center).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			ui.Icon(c, homeIconWhale).Size(34, 25).TextColor(t.LabelPrimary).AlignSelf(ui.Center).Left(-1)
			ui.Row(c).Gap(7).Children(func() {
				homeText(c, a.T("home.hero_title")).FontSize(26).FixedLineHeight(32).FontWeight(500).Left(-1).
					TextColor(t.LabelPrimary)
				a.homePreviewBadge(c, t)
			})
		})
	})
}

func (a *App) homePreviewBadge(c *ui.Context, t *design.Tokens) {
	bg, fg := homePreviewColors(t)
	homeText(c, a.T("home.preview")).FontSize(12).FixedLineHeight(18).FontWeight(500).
		Padding(1, 7, 2, 9).Radius(design.RadiusMax).
		Background(bg).TextColor(fg).
		AlignSelf(ui.Start).Top(2)
}
