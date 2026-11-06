package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
)

// openSettingsPopovers opens the in-app Settings modal on its General page.
func openSettingsPopovers(t *testing.T) (*liveApp, *ui.Tester) {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("settings did not open: screen = %v", h.app.Screen)
	}
	return h, tt
}

// TestSettingsPermissionPopoverIsBranded is the regression guard for the last
// unstyled Settings popover: the permission select is one branded popup panel
// with focusable menu-item-radio rows, the current mode checked, and choosing
// one still saves the default and closes the popover.
func TestSettingsPermissionPopoverIsBranded(t *testing.T) {
	h, tt := openSettingsPopovers(t)

	if err := tt.Click("Permission, current: Workspace Write"); err != nil {
		t.Fatalf("open permission select: %v", err)
	}
	tt.Frame()
	if !h.app.permissionOpen {
		t.Fatal("permission select did not open its popover")
	}
	nodes := testerAccessNodes(t, tt)
	if got := popupCount(nodes); got != 1 {
		t.Fatalf("permission popover panels = %d, want 1", got)
	}
	labels := []string{
		h.app.T("settings.full_access"),
		h.app.T("settings.workspace_write"),
		h.app.T("settings.read_only"),
		h.app.autoReviewLabel(),
	}
	for _, label := range labels {
		assertMenuRow(t, nodes, accessRoleMenuItemRadio, label, true)
	}
	if states := assertMenuRow(t, nodes, accessRoleMenuItemRadio, h.app.T("settings.workspace_write"), true); states&accessCheckedState == 0 {
		t.Fatal("the current default permission is not exposed as checked")
	}
	if !tt.HasText(h.app.autoReviewDescription()) {
		t.Fatalf("permission popover lost the Auto review description: %v", tt.Texts())
	}

	// Choosing Read Only still persists the General default and closes it.
	readOnly := h.app.T("settings.read_only")
	if err := tt.Click(readOnly); err != nil {
		t.Fatalf("choose %q: %v", readOnly, err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if cfg.Permission != session.PermissionAsk {
		t.Fatalf("default permission = %q, want %q", cfg.Permission, session.PermissionAsk)
	}
	if h.app.permissionOpen {
		t.Fatal("permission popover stayed open after choosing")
	}
}

// TestSettingsFontSizePopoverIsBranded covers the font-size pill's popover and
// the More font settings popover: both use the shared branded panel and rows,
// keep menu-item-radio roles and checked state, and write the persisted size.
func TestSettingsFontSizePopoverIsBranded(t *testing.T) {
	h, tt := openSettingsPopovers(t)

	if err := tt.Click("14 px"); err != nil {
		t.Fatalf("open font size popover: %v", err)
	}
	tt.Frame()
	if !h.app.fontSizeOpen {
		t.Fatal("font size popover did not open")
	}
	nodes := testerAccessNodes(t, tt)
	if got := popupCount(nodes); got != 1 {
		t.Fatalf("font size popover panels = %d, want 1", got)
	}
	for _, label := range []string{"14 px", "16 px"} {
		assertMenuRow(t, nodes, accessRoleMenuItemRadio, label, true)
	}
	if states := assertMenuRow(t, nodes, accessRoleMenuItemRadio, "14 px", true); states&accessCheckedState == 0 {
		t.Fatal("the current font size is not exposed as checked")
	}
	if err := tt.Click("16 px"); err != nil {
		t.Fatalf("choose 16 px: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if cfg.FontSize != session.FontSizeLarge {
		t.Fatalf("persisted font size = %d, want %d", cfg.FontSize, session.FontSizeLarge)
	}
	if h.app.fontSizeOpen {
		t.Fatal("font size popover stayed open after choosing")
	}

	// The More font settings popover carries the same headers and rows.
	if err := tt.Click("More font settings"); err != nil {
		t.Fatalf("open More font settings: %v", err)
	}
	tt.Frame()
	if !h.app.pluginUI().fontMoreOpen {
		t.Fatal("More font settings popover did not open")
	}
	nodes = testerAccessNodes(t, tt)
	if got := popupCount(nodes); got != 1 {
		t.Fatalf("More font settings popover panels = %d, want 1", got)
	}
	if !tt.HasText("Conversation text supports 14 or 16 px in this build.") {
		t.Fatalf("More font settings lost its explanation: %v", tt.Texts())
	}
	for _, label := range []string{"14 px", "16 px"} {
		assertMenuRow(t, nodes, accessRoleMenuItemRadio, label, true)
	}
	if states := assertMenuRow(t, nodes, accessRoleMenuItemRadio, "16 px", true); states&accessCheckedState == 0 {
		t.Fatal("the new font size is not exposed as checked in More font settings")
	}
}
