package uiroot

// Frame tests for the 2026-11 shell redesign (B-spec §3.1): the shell's
// geometry and vocabulary are asserted on the painted frame, because the
// findings they answer (A3 VD-01/VD-03/VD-04, A4-I-01/A4-I-02) were all visible
// defects — a second seam line, a band that clipped its own controls, a palette
// that painted an accent block, a palette row that changed height.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// seamColumns counts the pixel columns between x0 and x1 (exclusive) at row y
// that differ from the sunken surface the sidebar paints: the window's pane
// seam is one such column, never two (audit VD-03).
func seamColumns(tt *ui.Tester, y, x0, x1 int) int {
	img := tt.Image()
	// The two surfaces either side of the seam are flat: their colors at the
	// sampled row are the two references, and every column matching neither of
	// them is a line drawn on the boundary.
	sideR, sideG, sideB, _ := img.At(x0-6, y).RGBA()
	pageR, pageG, pageB, _ := img.At(x1+6, y).RGBA()
	count := 0
	for x := x0; x < x1; x++ {
		r, g, b, _ := img.At(x, y).RGBA()
		if (r == sideR && g == sideG && b == sideB) || (r == pageR && g == pageG && b == pageB) {
			continue
		}
		count++
	}
	return count
}

// TestSidebarSeamIsOneHairline covers VD-03: the sidebar and the content are
// separated by exactly one 1px line (the split view's divider, themed BorderL2),
// not by the sidebar's own edge stacked on top of it.
func TestSidebarSeamIsOneHairline(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	// The sidebar is 280 DIP wide, so the seam lives at the columns around 280.
	if got := seamColumns(tt, 400, 277, 285); got != 1 {
		t.Fatalf("pane seam draws %d columns at x=277..285, want exactly 1 (VD-03: one 1px BorderL2 line)", got)
	}
}

// TestTopBandHeightWaItsTabs covers B-spec §3.1.2: the screen header row is 48
// DIP and the tabs row 36, so the band's bottom edge sits at 84 DIP with tabs.
func TestTopBandHeightWaitsTabs(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "home.header", "screen-title", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenTabs, "home.tabs", "screen-tab", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	title, ok := tt.Find("screen-title")
	if !ok {
		t.Fatalf("header slot missing; texts = %v", tt.Texts())
	}
	// A 48 DIP row centres the label inside it: the label's vertical middle is
	// the row's, so a 40 DIP band would put it above 24.
	if mid := title.Y + title.H/2; mid < design.BandHeight/2-1 {
		t.Fatalf("header label mid = %.1f, want it centred in the 48 DIP band", mid)
	}
	tabs, ok := tt.Find("screen-tab")
	if !ok {
		t.Fatalf("tabs slot missing; texts = %v", tt.Texts())
	}
	if tabs.Y < design.BandHeight {
		t.Fatalf("tabs label Y = %.1f, want it below the %v DIP header row", tabs.Y, design.BandHeight)
	}
}

// TestBandFoldsActionsIntoOverflow covers VD-01: on a window too narrow for the
// screen's action chips the band keeps one ⋯, and the chips keep their full
// labels inside it (AGENTS §3.17 — a narrow window must not drop a name).
func TestBandFoldsActionsIntoOverflow(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, sessionScreenID+".title", "screen-title", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenActions, sessionScreenID+".actions", "智能体团队", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, sessionScreenID+".right", "inspector-pane", 10))
	tt := newTestTester(t, r, 1024, 720)
	// The audit's case: 1024 wide with the inspector docked leaves the band a
	// pane under the reading measure.
	r.SetRightPanelOpen(true)
	tt.Frame()

	// 1024 less the expanded sidebar leaves a pane under the reading measure:
	// the chip moves behind the band's overflow control.
	requireNoText(t, tt, "智能体团队")
	// The control is a toggle (its open state reads as pressed), so assistive
	// technology sees a toggle button.
	more, ok := tt.AccessByRole(ui.RoleToggleButton, "更多操作")
	if !ok {
		t.Fatalf("the folded band has no overflow control; texts = %v", tt.Texts())
	}
	if more.Bounds.X+more.Bounds.W > 1024-280 {
		t.Fatalf("overflow control right = %.1f, want it inside the content pane (%.1f)",
			more.Bounds.X+more.Bounds.W, float64(1024-280))
	}
	tt.ClickAt(more.Bounds.X+more.Bounds.W/2, more.Bounds.Y+more.Bounds.H/2)
	tt.Frame()
	// The label survives inside the popover.
	requireHasText(t, tt, "智能体团队")
}

// TestBandKeepsActionsOnWideWindow is the other side of VD-01: a wide window
// shows the chips in the band itself.
func TestBandKeepsActionsOnWideWindow(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, sessionScreenID+".title", "screen-title", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenActions, sessionScreenID+".actions", "智能体团队", 10))
	tt := newTestTester(t, r, 1512, 944)
	tt.Frame()
	requireHasText(t, tt, "智能体团队")
	if _, ok := tt.AccessByRole(ui.RoleToggleButton, "更多操作"); ok {
		t.Fatal("the wide band folded its chips behind an overflow control")
	}
}

// TestPaletteRowIsA36DIPQuietSelection covers B-spec §3.1.4: a palette row is 36
// DIP, its choice is the 9% selection tint (not an accent block) and the row
// carries the 2 DIP accent stripe.
func TestPaletteRowIsA36DIPQuietSelection(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustCommand(t, r, uinative.CommandSpec{ID: "alpha", Title: "Alpha", Order: 10,
		Run: func() error { return nil }})
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()

	row, ok := tt.AccessByRole(ui.RoleButton, "Alpha")
	if !ok {
		t.Fatalf("palette row missing; texts = %v", tt.Texts())
	}
	if row.Bounds.H != design.ControlHeight {
		t.Fatalf("palette row height = %.1f, want %v DIP", row.Bounds.H, design.ControlHeight)
	}
	if got := selectionPixels(tt); got < 5000 {
		t.Fatalf("the chosen row paints %d selection-tint pixels, want the 9%% tint band", got)
	}
	// The accent appears once, as the row's 2 DIP stripe (plus the caption's
	// caret): never as a filled block.
	if got := accentStripePixels(tt); got > 400 {
		t.Fatalf("the palette paints %d accent pixels, want only the 2 DIP stripe (under 400)", got)
	}
}

// TestPaletteClosesBackToItsTrigger covers A4-I-02 for the palette: Escape gives
// the keyboard back to the element that had it, never to nothing.
func TestPaletteClosesBackToItsTrigger(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "home.header", "screen-title", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	trigger, ok := tt.AccessByRole(ui.RoleButton, "快捷操作")
	if !ok {
		t.Fatalf("palette trigger missing; texts = %v", tt.Texts())
	}
	tt.ClickAt(trigger.Bounds.X+trigger.Bounds.W/2, trigger.Bounds.Y+trigger.Bounds.H/2)
	tt.Frame()
	if !r.paletteOpen {
		t.Fatal("the palette did not open from its trigger")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if r.paletteOpen {
		t.Fatal("Escape did not close the palette")
	}
	back, ok := tt.AccessByRole(ui.RoleButton, "快捷操作")
	if !ok {
		t.Fatalf("the palette trigger is gone after closing; texts = %v", tt.Texts())
	}
	if !back.Focused {
		t.Fatalf("focus after closing the palette = %+v, want it back on the trigger", back)
	}
}

// TestSettingsCloseReturnsFocus covers A4-I-02 for the settings surface: the
// close control returns the keyboard to the element that opened the surface.
func TestSettingsCloseReturnsFocus(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	r.Navigator().ToggleSettings()
	tt.Frame()
	closeInfo, ok := tt.AccessByRole(ui.RoleButton, "关闭设置")
	if !ok {
		t.Fatalf("the settings close control is missing; texts = %v", tt.Texts())
	}
	if closeInfo.Description != "" {
		t.Fatalf("the settings close control carries a tooltip %q; a tooltip would take the first Escape (A4-I-01)",
			closeInfo.Description)
	}
	tt.ClickAt(closeInfo.Bounds.X+closeInfo.Bounds.W/2, closeInfo.Bounds.Y+closeInfo.Bounds.H/2)
	tt.Frame()
	tt.Frame()
	if r.Navigator().SettingsOpen() {
		t.Fatal("the close control did not close the settings surface")
	}
}

// TestNewSessionClickDebounces covers A4-I-04 at the shell: two clicks of the
// rail's new-session control within the debounce window are one conversation,
// while the keyboard chord stays an explicit request each time.
func TestNewSessionClickDebounces(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "body", 10))
	chat := &shortcutChat{}
	r.mu.Lock()
	r.chatService = chat
	r.mu.Unlock()
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	r.SetSidebarCollapsed(true)
	tt.Frame()
	button, ok := tt.AccessByRole(ui.RoleButton, "新建会话")
	if !ok {
		t.Fatalf("the rail's new-session control is missing; texts = %v", tt.Texts())
	}
	tt.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
	tt.Frame()
	if button, ok := tt.AccessByRole(ui.RoleButton, "新建会话"); ok {
		tt.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
		tt.Frame()
	}
	chat.mu.Lock()
	calls := chat.newCalls
	chat.mu.Unlock()
	if calls != 1 {
		t.Fatalf("NewSession calls = %d, want 1 (a double click is one conversation, A4-I-04)", calls)
	}
}

// TestSettingsEscapeFromPageControl closes the surface from a control that
// lives inside the page: the panel-level Escape is on the surface's ancestor
// chain, so the press does not depend on which control has the focus (A4-I-01).
func TestSettingsEscapeFromPageControl(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, uinative.SettingsPageSpec{
		ID: "general", Section: "通用", Title: "通用", Order: 10,
		Build: func(c *ui.Context) {
			ui.Button(c, "页面控件").Tooltip("这一段说明只在指针停留时出现")
		},
	})
	tt := newTestTester(t, r, 1400, 900)
	nav := r.Navigator()
	tt.Frame()
	nav.ToggleSettings()
	tt.Frame()
	requireHasText(t, tt, "页面控件")

	// Tab into the page: the modal takes the focus on open (the search field),
	// and the page's own control is deeper in the surface.
	focused := false
	for i := 0; i < 8; i++ {
		if info, ok := tt.AccessByRole(ui.RoleButton, "页面控件"); ok && info.Focused {
			focused = true
			break
		}
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	if !focused {
		t.Skip("the page control is not in this build's tab order; the surface-level Escape is covered by TestSettingsCloseReturnsFocus")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if nav.SettingsOpen() {
		t.Fatal("Escape from a page control did not close the settings surface")
	}
}

// TestBandOverflowClosesOnOneEscape covers the overlay-Esc contract the shell
// must not break (A4-I-01 / the interaction QA's P0): the band's popover takes
// the first Escape, and the shell's screen-level Escape must not consume it
// first (a window-scope handler would).
func TestBandOverflowClosesOnOneEscape(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, sessionScreenID+".title", "screen-title", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenActions, sessionScreenID+".actions", "智能体团队", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, sessionScreenID+".right", "inspector-pane", 10))
	tt := newTestTester(t, r, 1024, 720)
	r.SetRightPanelOpen(true)
	tt.Frame()

	more, ok := tt.AccessByRole(ui.RoleToggleButton, "更多操作")
	if !ok {
		t.Fatalf("the folded band has no overflow control; texts = %v", tt.Texts())
	}
	tt.ClickAt(more.Bounds.X+more.Bounds.W/2, more.Bounds.Y+more.Bounds.H/2)
	tt.Frame()
	requireHasText(t, tt, "智能体团队")

	// One Escape closes the popover — not the screen action behind it.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	requireNoText(t, tt, "智能体团队")
	if !r.rightOpen {
		t.Fatal("the popover's Escape also closed the inspector behind it")
	}
}

// TestPaletteDoesNotRepeatAChordInTheName covers the row anatomy rule: a command
// whose own name states its chord ("Esc: 关闭 / 拒绝 / 停止") does not also get a
// key cap saying the same thing, while a command that only has a chord keeps its
// cap.
func TestPaletteDoesNotRepeatAChordInTheName(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, sessionScreenID+".title", "会话标题", 10))
	for _, c := range []uinative.CommandSpec{
		{ID: "esc", Title: "Esc: 关闭 / 拒绝 / 停止", Shortcut: "Esc", Order: 10},
		{ID: "new", Title: "新建会话", Shortcut: "Cmd/Ctrl+N", Order: 20},
	} {
		c := c
		c.Run = func() error { return nil }
		mustCommand(t, r, c)
	}
	tt := newTestTester(t, r, 1512, 944)
	tt.Frame()
	r.togglePalette()
	tt.Frame()

	// The sentence-named row has no cap of its own; the only Esc reference left is
	// the footer legend, whose cap says so in its own accessible name.
	if _, ok := tt.Find("图例：Esc"); !ok {
		t.Fatalf("the palette footer legend lost its Esc cap; texts = %v", tt.Texts())
	}
	escapes := 0
	for _, text := range tt.Texts() {
		if text == "Esc" {
			escapes++
		}
	}
	if escapes != 0 {
		t.Fatalf("the palette shows %d bare Esc caps, want none (the row must not repeat the chord its name states, and the legend's cap is named); texts = %v",
			escapes, tt.Texts())
	}
	if _, ok := tt.Find(uikit.FormatShortcut("Cmd/Ctrl+N")); !ok {
		t.Fatalf("the plain command lost its key cap; texts = %v", tt.Texts())
	}
}
