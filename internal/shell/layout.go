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
	// start clamping a panel instead. It is the desktop-app minimum that keeps
	// 400px of main content beside a docked inspector panel at 1200x800.
	minMainW float32 = 400
	// minRightPanelW is the narrowest docked right panel. The panel gives up
	// width before the main column falls below minMainW, so 280+400+220 fits
	// the 900px minimum window instead of overflowing it.
	minRightPanelW float32 = 220
	// rightPanelShare is the share of the content column the docked right
	// panel may take by default: 45%, capped by the brand's 560 DIP panel
	// width (rightSidebarW). A user drag can widen it up to the room
	// minMainW leaves.
	rightPanelShare float32 = 0.45
	// minHomeStackW keeps the Home hero and composer usable at 900px.
	minHomeStackW float32 = 320
	// narrowColumnInset is the total inset the centered Home/Session
	// columns take at a narrow window: 212px, capped so it never eats a
	// column that is already narrow.
	narrowColumnInset float32 = 212
	// narrowComposerLabelW is where the composer's access chip drops its
	// label and keeps its icon+chevron trigger. Below it the label would wrap
	// the toolbar, so only the compact trigger stays.
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

// rightSidebarWidth is the docked inspector panel's brand width: the width the
// user dragged it to, or else min(560, 45% of the content column). It always
// leaves minMainW for the screen behind it and never narrows below
// minRightPanelW, so the panel stays a usable column at the 900x600 minimum.
// The 560 default is the brand panel token (rightSidebarW), not a value
// sampled from another product's page.
func (a *App) rightSidebarWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	avail := w - sidebarW
	if avail <= 0 {
		return 0
	}
	panel := rightSidebarW
	if share := avail * rightPanelShare; panel > share {
		panel = share
	}
	if dragged := a.rightPanelUI().dockWidth; dragged > 0 {
		panel = dragged
	}
	if max := avail - minMainW; panel > max {
		panel = max
	}
	if panel < minRightPanelW {
		panel = minRightPanelW
	}
	if panel > avail {
		panel = avail
	}
	if panel < 1 {
		panel = 1
	}
	return panel
}

// narrowInsetFor is the side inset one centered column gets. When the right
// panel has already reduced the main column, the small inset applies: the
// remaining column paints a usable composer card and stack instead of being
// squeezed by the 212px narrow-window inset. Without the panel, the inset is
// capped so it never eats a column that is already narrow (400px of content
// keeps the 361px card and 393px stack).
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
// Session transcript/composer) to the narrow-window geometry:
// min(column, contentWidth-inset), where inset comes from narrowInsetFor.
func (a *App) narrowColumnWidth(content, column float32) float32 {
	if content <= 0 {
		return column
	}
	if limit := content - a.narrowInsetFor(content); column > limit {
		column = limit
	}
	if column < 1 {
		column = 1
	}
	return column
}

// homeStackWidthFor is the centered Home column width. On a wide window it
// keeps the brand column; at narrow widths the composer shrinks with the
// capped inset and the stack follows it (composer + 32 without a panel; 393
// at 400px of content when the docked panel already narrowed the column).
func (a *App) homeStackWidthFor(c *ui.Context) float32 {
	avail := a.contentWidth(c)
	w := homeStackWidth
	if avail > 0 {
		inset := a.narrowInsetFor(avail)
		if inset >= narrowColumnInset {
			// The uncapped narrow geometry: the stack keeps its original
			// 29px of extra room over the composer.
			if limit := avail - (narrowColumnInset - 29); w > limit {
				w = limit
			}
		} else if limit := avail - inset + 32; w > limit {
			// With the panel already subtracted, the composer card is the
			// content minus the capped inset and the stack is 32px wider:
			// 393 at content 400: the narrow layout keeps 16 DIPs of room
			// over the small inset.
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
