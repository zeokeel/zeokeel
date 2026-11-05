package shell

import "github.com/egoist/mygo/ui"

// Layout helpers keep the screen geometry working at the window's narrow
// sizes (900x600 minimum) and when the right sidebar takes part of the
// content column. They read the frame size only.

const (
	// sidebarW is the brand sidebar's source-list column: 280 DIPs, the
	// width the native Sidebar, the content column and the right panel's
	// clamp are laid out against. It is a brand layout token (the same 280
	// design.Sidebar() paints), not a value sampled from another product.
	sidebarW float32 = 280
	// minMainW is the narrowest main column the layouts allow before they
	// start clamping a panel instead. DSH keeps 400px of main content beside
	// its 520px right panel at 1200x800, so its docked panel is 520 there.
	minMainW float32 = 400
	// minRightPanelW is the narrowest docked right panel. The panel gives up
	// width before the main column falls below minMainW, so 280+400+220 fits
	// the 900px minimum window instead of overflowing it.
	minRightPanelW float32 = 220
	// minHomeStackW keeps the Home hero and composer usable at 900px.
	minHomeStackW float32 = 320
	// narrowSideInset is the per-side inset DSH keeps at 1200x800 for the
	// centered Home/Session columns and the Plugins/Automation pages (48px)
	// or the Home/Session composer (106px, applied as a total of 212).
	narrowSideInset   float32 = 48
	narrowColumnInset float32 = 212
	// narrowComposerLabelW is where the composer's access chip drops its
	// text and keeps DSH's 44px icon+chevron trigger. Below it the label
	// wraps the 28px toolbar; at 1200x800 with the docked panel the
	// composer is 361px.
	narrowComposerLabelW float32 = 430
	// sessionSidePanelW is one docked Todo/Team panel. They are fixed-width
	// columns beside the transcript, so they must be subtracted from the
	// transcript's width and hidden entirely when the main column cannot
	// keep a usable conversation.
	sessionSidePanelW float32 = 272
	// minSessionTranscriptW is the narrowest transcript viewport that still
	// shows a message beside the side panels. Below it the panels collapse
	// instead of squeezing or clipping the conversation.
	minSessionTranscriptW float32 = 280
)

// contentWidth returns the width of the main content column: the window
// minus the left sidebar and, when the right sidebar is docked, minus that
// panel (or half of the column in split mode).
func (a *App) contentWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	avail := w - sidebarW
	if avail <= 0 {
		return w
	}
	if a.Ctrl != nil && a.rightOpen && a.rightSidebarHosted() {
		switch {
		case a.rightFullscreen:
			return 0
		case a.rightSplit:
			return avail / 2
		default:
			avail -= a.rightSidebarWidth(c)
		}
	}
	if avail < minMainW {
		avail = minMainW
	}
	return avail
}

// rightSidebarWidth is the docked panel width: DSH's 720 at 1600x1000,
// shrinking so at least minMainW stays for the screen behind it.
func (a *App) rightSidebarWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	avail := w - sidebarW
	if avail <= 0 {
		return 0
	}
	panel := rightSidebarW
	if avail-panel < minMainW {
		panel = avail - minMainW
	}
	if panel < minRightPanelW {
		panel = minRightPanelW
	}
	if panel > avail {
		panel = avail
	}
	return panel
}

// narrowInsetFor is the side inset one centered column gets. When the right
// panel has already reduced the main column, DSH switches to its small
// inset: at 1600x1000 with the 720px panel the 600px column still paints a
// 561px composer card and a 593px stack (baseline 15-session-dark). Applying
// the 212px narrow-window inset there produced a 388px composer. Without the
// panel, the inset is capped so it never eats a column that is already narrow
// (400px content keeps the 361px card / 393px stack of baseline 48/16).
func (a *App) narrowInsetFor(content float32) float32 {
	if a.Ctrl != nil && a.rightOpen && a.rightSidebarHosted() && !a.rightFullscreen {
		return 39
	}
	if content <= 0 {
		return narrowColumnInset
	}
	inset := content - 361
	if inset < 0 {
		inset = 0
	}
	if inset > narrowColumnInset {
		inset = narrowColumnInset
	}
	return inset
}

// narrowColumnWidth clamps one centered column (the Home stack/composer or the
// Session transcript/composer) to DSH's narrow geometry:
// min(baseline, contentWidth-inset), where inset comes from narrowInsetFor.
func (a *App) narrowColumnWidth(content, baseline float32) float32 {
	if content <= 0 {
		return baseline
	}
	if limit := content - a.narrowInsetFor(content); baseline > limit {
		baseline = limit
	}
	if baseline < 1 {
		baseline = 1
	}
	return baseline
}

// homeStackWidthFor is the centered Home column width. At 1600x1000 it keeps
// the 909px baseline; at narrow widths the composer shrinks with the capped
// inset and the stack follows it (composer + 32 without a panel; the baseline
// 393 at content 400 when the docked panel already narrowed the column).
func (a *App) homeStackWidthFor(c *ui.Context) float32 {
	avail := a.contentWidth(c)
	w := homeStackWidth
	if avail > 0 {
		inset := a.narrowInsetFor(avail)
		if inset >= narrowColumnInset {
			// DSH's uncapped narrow geometry: the stack keeps its original
			// 29px of extra room over the composer.
			if limit := avail - (narrowColumnInset - 29); w > limit {
				w = limit
			}
		} else if limit := avail - inset + 32; w > limit {
			// With the panel already subtracted, the composer card is the
			// content minus the capped inset and the stack is 32px wider:
			// 393 at content 400, matching baseline 48/16.
			w = limit
		}
	}
	if w < minHomeStackW {
		w = minHomeStackW
	}
	if avail > 0 && w > avail {
		w = avail
	}
	return w
}

// homeComposerWidthFor is the Home composer card width inside the stack. The
// 15/14px row padding is subtracted so the card never exceeds the stack.
func (a *App) homeComposerWidthFor(c *ui.Context) float32 {
	content := a.contentWidth(c)
	w := a.narrowColumnWidth(content, homeComposerWidth)
	if content > 0 && a.narrowInsetFor(content) < narrowColumnInset {
		// The live card adds a 2px ring on top of this width; subtract it so
		// the painted card is content-inset (361 at content 400).
		if limit := content - a.narrowInsetFor(content) - 2; w > limit {
			w = limit
		}
	}
	limit := a.homeStackWidthFor(c) - 29
	if limit > 0 && w > limit {
		w = limit
	}
	if w < minHomeStackW-29 {
		w = minHomeStackW - 29
	}
	return w
}

// sidePanelsVisible reports which of the session's Todo/Team side panels may
// dock beside the transcript. They are fixed 272px columns, so at narrow
// widths or with the right sidebar open the pair used to consume the whole
// transcript viewport (and pushed the user bubble past the clip). When the
// columns cannot leave minSessionTranscriptW for the conversation they are
// hidden; the panels still exist in the controller and reappear when there is
// room.
func (a *App) sidePanelsVisible(c *ui.Context) (todoVisible, teamVisible bool) {
	if a.Ctrl == nil {
		return false, false
	}
	todoVisible = len(a.todos()) > 0
	teamVisible = len(a.teammates()) > 0
	if !todoVisible && !teamVisible {
		return false, false
	}
	panels := float32(0)
	if todoVisible {
		panels += sessionSidePanelW
	}
	if teamVisible {
		panels += sessionSidePanelW
	}
	if a.contentWidth(c)-panels < minSessionTranscriptW {
		return false, false
	}
	return todoVisible, teamVisible
}

// sessionSidePanelWidth is the width the visible Todo/Team panels take from
// the transcript's viewport. It is 0 when both are hidden.
func (a *App) sessionSidePanelWidth(c *ui.Context) float32 {
	todoVisible, teamVisible := a.sidePanelsVisible(c)
	var width float32
	if todoVisible {
		width += sessionSidePanelW
	}
	if teamVisible {
		width += sessionSidePanelW
	}
	return width
}

// sessionComposerWidthFor is the centered Session composer card width: the
// brand column (sessComposerW, 720 DIPs) clamped by the shared narrow-window
// insets, so its Send and model controls stay visible at 900-1100px and when
// the right panel is open.
func (a *App) sessionComposerWidthFor(c *ui.Context) float32 {
	w := a.narrowColumnWidth(a.contentWidth(c), sessComposerW)
	if w < minSessionColumnW {
		w = minSessionColumnW
	}
	if avail := a.contentWidth(c); avail > 0 && w > avail {
		w = avail
	}
	if w < 1 {
		w = 1
	}
	return w
}

// sessionTranscriptWidthFor is the centered transcript column: the same brand
// column as the composer, clamped by the shared narrow-window insets and by
// the viewport that remains beside the docked Todo/Team panels. Chat and
// composer stay aligned instead of the old 845/877 fixed pair.
func (a *App) sessionTranscriptWidthFor(c *ui.Context) float32 {
	content := a.contentWidth(c)
	// The side panels are siblings of the transcript inside the content
	// column; only what remains is the transcript's real viewport.
	viewport := content
	if panelW := a.sessionSidePanelWidth(c); panelW > 0 {
		viewport -= panelW
	}
	w := a.narrowColumnWidth(viewport, sessColumnW)
	if w < minSessionColumnW {
		w = minSessionColumnW
	}
	if viewport > 0 && w > viewport {
		w = viewport
	}
	if w < 1 {
		w = 1
	}
	return w
}

// pageColumnWidth is the centered 960px content column shared by Plugins and
// Automation: min(960, contentWidth-96), leaving DSH's 48px side insets at
// 1200x800 while the 1600x1000 column stays 960.
func (a *App) pageColumnWidth(c *ui.Context, baseline float32) float32 {
	if avail := a.contentWidth(c); avail > 0 {
		if limit := avail - 2*narrowSideInset; baseline > limit {
			baseline = limit
		}
	}
	if baseline < 1 {
		baseline = 1
	}
	return baseline
}

// composerPopupWidthFor is the width a popover above the composer may use:
// the composer card's own width, so menus and approval cards never spill past
// the centered column at narrow sizes.
func (a *App) composerPopupWidthFor(c *ui.Context) float32 {
	switch a.backgroundScreen() {
	case Session:
		return a.sessionComposerWidthFor(c)
	default:
		return a.homeComposerWidthFor(c)
	}
}

// designGutter is the per-side breathing room the centered columns reserve.
const designGutter float32 = 12
