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

// Home layout: the brand visual direction from docs/design/native-ui.md §3: a
// centered hero (ZeoKeel mark, wordmark and tagline) over a composer card
// built from the brand theme and mygo's native controls, with every width and
// gap on the theme's 4 DIP spacing grid. No value here is sampled from an
// external page; the geometry is the brand's own.
const (
	// homeColumnUnits caps the centered content column: 180 spacing units,
	// 720 DIPs at the brand's Spacing=4, so the composer reads as a card on
	// a wide window instead of a full-width bar.
	homeColumnUnits float32 = 180
	// homeColumnInsetUnits is the room the centered column keeps from the
	// main area: 20 units, 80 DIPs, per side on a narrow window.
	homeColumnInsetUnits float32 = 20
	// homeWordmarkRem is the hero wordmark's size in rem: 2.25 × the brand's
	// 14px base ≈ 32 DIP, the wordmark's display size.
	homeWordmarkRem float32 = 2.25
)

// homeStackWidth and homeComposerWidth are the Home geometry values that
// layout.go's shared narrow-width helpers read: they clamp the popovers above
// the composer and decide when the composer chips may keep their labels. They
// describe the brand column: the composer card fills the column, and the
// column is capped by homeColumnUnits.
var (
	homeStackWidth    = homeColumnUnits * design.Space1
	homeComposerWidth = homeStackWidth
)

// homeTagAccent is the reasoning-effort tag's accent: the brand accent in the
// current appearance, so the tag follows the theme instead of a sampled hex.
func homeTagAccent(t *design.Tokens) ui.Color { return t.Accent }

// SVG icons used by the Home screen, drawn on the theme's 16px grid with the
// same thin line weight as the rest of the shell.
var (
	homeIconChevron = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M4 6.2l4 3.6 4-3.6"/></svg>`))

	homeIconFolder = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none"><path d="M1.6 4.6a1.1 1.1 0 0 1 1.1-1.1h3.1l1.7 1.7h5.8a1.1 1.1 0 0 1 1.1 1.1v6.1a1.1 1.1 0 0 1-1.1 1.1H2.7a1.1 1.1 0 0 1-1.1-1.1z" fill="currentColor" fill-opacity="0.13" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" stroke-linejoin="round"/></svg>`))

	homeIconTune = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3.4 13 6.3v5.4L8 14.6 3 11.7V6.3z"/><circle cx="8" cy="1.9" r="1.3"/><circle cx="2.4" cy="12.5" r="1.3"/><circle cx="13.6" cy="12.5" r="1.3"/></svg>`))

	homeIconPlus = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M8 2.8v10.4M2.8 8h10.4"/></svg>`))

	homeIconShield = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"><path d="M8 1.4l5.2 1.9v3.6c0 3.3-2.2 5.7-5.2 6.9-3-1.2-5.2-3.6-5.2-6.9V3.3z"/></svg>`))

	homeIconArrowUp = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M8 14.3V2.5M3 7.4l5-4.9 5 4.9"/></svg>`))
)

// home draws the Home screen. The live controller-backed renderer is the
// only one; the retired static reference render was deleted with pixel parity
// (ADR 0001).
func (a *App) home(c *ui.Context, t *design.Tokens) {
	a.homeLive(c, t)
}

// homeLive is the controller-backed home screen: the workspace chip reflects
// the real workspace (or offers to create one), and the composer creates a
// session in that workspace and submits the first message.
func (a *App) homeLive(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	design.Apply(ui.Column(c), t.Main()).Children(func() {
		a.homeHeader(c, t)
		ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).
			Padding(th.Space(4), th.Space(4)).Children(func() {
			column := ui.Column(c).Width(a.homeColumnWidth(c, t)).Gap(th.Space(8))
			column.Children(func() {
				a.homeHero(c, t)
				ui.Column(c).Gap(th.Space(3)).Children(func() {
					a.homeLiveComposer(c, t)
					ui.Row(c).Justify(ui.Center).Children(func() {
						hint := design.Apply(homeText(c, a.T("home.composer_hint")), t.Font(design.TypeXXS))
						hint.TextColor(t.LabelTertiary)
					})
				})
			})
		})
	})
}

// homeColumnWidth is the centered Home content column: the widest readable
// card on a wide window and the available main width minus the brand inset on
// a narrow one. It is theme-derived (Spacing units) instead of a sampled
// pixel width.
func (a *App) homeColumnWidth(c *ui.Context, t *design.Tokens) float32 {
	th := t.Theme()
	width := th.Space(homeColumnUnits)
	if avail := a.contentWidth(c); avail > 0 {
		if limit := avail - th.Space(homeColumnInsetUnits); width > limit {
			width = limit
		}
		if width > avail {
			width = avail
		}
	}
	if width < minHomeStackW {
		width = minHomeStackW
	}
	if width < 1 {
		width = 1
	}
	return width
}

// homeHero is the ZeoKeel header: the brand mark, the product wordmark at the
// display size and one line of self-owned copy, centered over the composer.
func (a *App) homeHero(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Column(c).AlignItems(ui.Center).Gap(th.Space(3)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(th.Space(3)).Children(func() {
			ui.Image(c, brandMark(t)).Size(th.Space(11), th.Space(11))
			homeText(c, "ZeoKeel").FontSize(th.Rem(homeWordmarkRem)).
				FixedLineHeight(th.Rem(2.9)).FontWeight(600).TextColor(t.LabelPrimary)
		})
		tagline := design.Apply(homeText(c, a.T("home.tagline")), t.Font(design.TypeM))
		tagline.TextColor(t.LabelSecondary)
	})
}

// homeChip is the shared look of the Home composer's chips (workspace, mode,
// permission, model): a 28 DIP row on the 4 DIP grid, the small brand corner
// and the brand hover tint. The caller keeps the role, label, focus and
// popover, so the native keyboard and a11y behavior is unchanged.
func homeChip(c *ui.Context, t *design.Tokens) ui.Element {
	th := t.Theme()
	chip := ui.Row(c).Height(t.Scale(th.Space(7))).PaddingX(t.Scale(th.Space(2))).
		Gap(t.Scale(th.Space(1))).Radius(design.RadiusSM).AlignItems(ui.Center)
	if chip.Hovered() {
		chip = chip.Background(t.BgHover)
	}
	return chip
}

// homeLiveChips is the workspace and agent-mode row at the top of the
// composer card. Both triggers stay custom rows because their popovers carry
// product-specific content (workspace paths and an add action, mode
// descriptions): the task allows keeping that layer when a native Select
// cannot express it, and both are restyled with the brand theme here.
func (a *App) homeLiveChips(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ctx := context.Background()
	ws, ok, _ := a.Ctrl.CurrentWorkspace(ctx)
	label := a.T("home.choose_workspace")
	if ok && strings.TrimSpace(ws.Name) != "" {
		label = ws.Name
	}
	ui.Row(c).Gap(th.Space(1)).Children(func() {
		chipLabel := a.T("home.choose_workspace")
		if ok && strings.TrimSpace(ws.Name) != "" {
			chipLabel = a.menuText("Workspace, current: ", "工作区，当前：") + label
		}
		wsChip := homeChip(c, t).
			Label(chipLabel).Value(label).Role(ui.RolePopUpButton).Focusable()
		wsChip.Children(func() {
			ui.Image(c, homeIconFolder).Size(th.Space(4), th.Space(4)).TextColor(t.LabelPrimary)
			wsText := design.Apply(homeText(c, label), t.Font(design.TypeXS))
			wsText.FontWeight(500).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			ui.Icon(c, homeIconChevron).Size(th.Space(3), th.Space(3)).TextColor(t.LabelTertiary)
		})
		wsChip.Expanded(a.wsPickerOpen)
		wsChip.OnClick(func() { a.chooseWorkspaceFromHome() })
		ui.Popover(c, wsChip, &a.wsPickerOpen, func() { a.homeWorkspacePicker(c, t) })

		mst := a.modeUI()
		modeText := a.agentModeLabel(a.homeAgentMode())
		if a.homeAgentMode() == "standard" {
			modeText = a.T("common.standard_mode")
		}
		mode := homeChip(c, t).
			Label(modeText).Value(modeText).Role(ui.RolePopUpButton).Focusable()
		mode.Children(func() {
			ui.Icon(c, homeIconTune).Size(th.Space(4), th.Space(4)).TextColor(t.LabelPrimary)
			modeEl := design.Apply(homeText(c, modeText), t.Font(design.TypeXS))
			modeEl.FontWeight(500).TextColor(t.LabelPrimary)
			ui.Icon(c, homeIconChevron).Size(th.Space(3), th.Space(3)).TextColor(t.LabelTertiary)
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

// homeLiveComposer is the brand composer card: staged attachment chips, the
// workspace/mode row, a native multi-line TextArea and a toolbar of native
// controls, all inside one theme-styled surface.
func (a *App) homeLiveComposer(c *ui.Context, t *design.Tokens) {
	// The Home composer accepts the same "/" command and "@" file mention
	// palettes as the session composer, as its placeholder promises.
	if a.attachOpen {
		a.attachmentMenu(c, t)
	}
	if a.slashMenuVisible() {
		a.commandPalette(c, t)
	} else if entries := a.visibleMentionEntries(); len(entries) > 0 {
		a.mentionMenu(c, t, entries)
	}
	width := a.homeColumnWidth(c, t)
	ui.Column(c).Width(width).Gap(t.Theme().Space(2)).Children(func() {
		// Files picked on Home belong to the Home draft until Send creates
		// their session; render their chips inside the card so the user can
		// see and remove them just like the session composer's pending chips.
		if snap := a.homeAttachmentSnapshot(); snap != nil {
			a.pendingAttachmentChips(c, t, snap)
		}
		a.homeComposerCard(c, t, width)
	})
}

// homeComposerCard is the themed composer surface: one raised card with the
// brand corner, a single soft drop shadow and, while focused, the brand
// accent border. It shares the input, focus and toolbar pieces with the
// Session composer (composer_native.go) so both screens keep one look.
func (a *App) homeComposerCard(c *ui.Context, t *design.Tokens, width float32) {
	th := t.Theme()
	card := design.Apply(ui.Column(c), t.Panel()).
		Width(width).
		Radius(th.Radius).
		Border(1, t.BorderL2).
		Shadow(0, 2, 8, 0, homeCardShadow(t)).
		Padding(th.Space(3)).
		Gap(th.Space(2)).
		Label(a.tr("Home composer", "主页输入框"))
	card.Children(func() {
		a.homeLiveChips(c, t)
		input := brandComposerInput(c, t, &a.draft,
			a.T("session.composer_input"), a.T("home.composer_placeholder"),
			a.composerTextKey(c, true), &a.composerRef).
			MinHeight(th.Space(13))
		// Keep the input focused the way the Session composer does. Opening a
		// derived popup ("/" palette, "@" mention list) inserts it before this
		// column, which changes the input's element id and used to drop the
		// keyboard focus: the following Enter then never reached
		// composerTextKey, so Home slash commands silently did nothing.
		// AutoFocus only fires for an element that is new in the frame, so it
		// restores the focus here without stealing it from a control the user
		// intentionally focused elsewhere.
		input.AutoFocus()
		card = brandComposerFocus(card, input, t)
		a.homeComposerToolbar(c, t)
	})
}

// homeCardShadow is the composer card's single soft shadow: a light drop in
// the light theme and a deeper one in the dark theme, where the raised
// surface needs more separation from the night background.
func homeCardShadow(t *design.Tokens) ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.45)
	}
	return ui.RGBA(15, 17, 21, 0.08)
}

// homeComposerToolbar is the composer's action row: the native add control,
// the access-mode and model chips, and the accent Send button.
func (a *App) homeComposerToolbar(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).AlignItems(ui.Center).Gap(th.Space(1)).Children(func() {
		brandIconButton(c, t, icons.Plus(), a.T("composer.add_files_or_run_commands"), func() {
			a.attachOpen = !a.attachOpen
		})
		ui.Box(c).Width(th.Space(1))
		a.livePermissionChip(c, t)
		ui.Spacer(c).Grow(1)
		a.liveModelMenuBadge(c, t)
		ui.Box(c).Width(th.Space(2))
		empty := strings.TrimSpace(a.draft) == ""
		brandSendButton(c, t, a.T("composer.send_message"), empty, false, func() {
			a.sendHomeDraft(c)
		})
	})
}

// sendHomeDraft creates a session in the current workspace (or asks for one
// first) and submits the draft as its first message.
func (a *App) sendHomeDraft(c *ui.Context) {
	if a.Ctrl == nil {
		return
	}
	// An empty submit must not create a session; check before touching the
	// workspace or store.
	if strings.TrimSpace(a.draft) == "" {
		return
	}
	id, ok := a.newHomeSession(c)
	if !ok {
		return
	}
	text := strings.TrimSpace(a.draft)
	if err := a.Ctrl.Submit(context.Background(), id, text); err != nil {
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

// newHomeSession creates and opens the session the Home composer sends into:
// the current workspace, the Home-staged agent preset/access mode/model, and
// the staged attachments. It returns the new session id on success; failures
// are reported through c (when it is available) and leave the Home draft and
// its chips intact. The Session screen is only entered after every step
// succeeds, so a failed /plan or Home send cannot land on a half-built session.
func (a *App) newHomeSession(c *ui.Context) (string, bool) {
	if a.Ctrl == nil {
		return "", false
	}
	toast := func(msg string) {
		if c != nil {
			c.Toast(msg)
		}
	}
	ctx := context.Background()
	ws, ok, err := a.Ctrl.CurrentWorkspace(ctx)
	if err != nil || !ok || strings.TrimSpace(ws.ID) == "" {
		a.beginAddWorkspace()
		return "", false
	}
	id, err := a.Ctrl.CreateSessionInWorkspace(ctx, ws.ID, a.T("session.new_session"))
	if err != nil {
		toast(fmt.Sprintf(a.menuText("Could not create a session: %v", "无法创建会话：%v"), err))
		return "", false
	}
	snap, err := a.Ctrl.OpenSession(ctx, id)
	if err != nil {
		toast(fmt.Sprintf(a.menuText("Could not open the new session: %v", "无法打开新会话：%v"), err))
		return "", false
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
		toast(fmt.Sprintf(a.menuText("Could not attach file: %v", "无法添加附件：%v"), err))
		return "", false
	}
	a.Screen = Session
	return id, true
}

// homeHeader is the top strip of the main area: empty except for the control
// that opens the right sidebar, the same panel Session uses.
func (a *App) homeHeader(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).Height(th.Space(10)).PaddingX(th.Space(4)).
		AlignItems(ui.Center).Children(func() {
		ui.Spacer(c).Grow(1)
		label := a.tr("Open right sidebar", "打开右侧边栏")
		if a.Ctrl != nil && a.rightOpen {
			// Distinct from the right panel's own 28x28 collapse control.
			label = a.tr("Collapse right sidebar header", "收起右侧边栏（标题栏）")
		}
		button := ui.ButtonBase(c).Size(th.Space(7), th.Space(7)).Center().
			Label(label).Tooltip(label).Radius(design.RadiusSM)
		if button.Hovered() {
			button = button.Background(t.BgHover)
		}
		button.Children(func() {
			ui.Icon(c, chromeIconPanelRight).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
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
// theme's own system font stack (brand.SystemFont).
const homeFont = ""

func homeText(c *ui.Context, s string) ui.Element { return ui.Text(c, s).Font(homeFont) }
