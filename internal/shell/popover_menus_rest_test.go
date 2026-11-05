package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// accessRoleMenuItemCheckBox is platform.AccessRole's RoleMenuItemCheckBox, the
// role the View options choices keep after the restyle.
const accessRoleMenuItemCheckBox uint8 = 41

// TestAccountMenuIsBrandedPopover covers the sidebar footer's account menu, one
// of the menus moved onto the shared popover helpers: one branded panel, the
// three entries as focusable menu items, and choosing one keeps its behavior.
func TestAccountMenuIsBrandedPopover(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("More"); err != nil {
		t.Fatalf("open account menu: %v", err)
	}
	tt.Frame()
	if !h.app.moreOpen {
		t.Fatal("account menu did not open")
	}
	nodes := testerAccessNodes(t, tt)
	if got := popupCount(nodes); got != 1 {
		t.Fatalf("account menu popup panels = %d, want 1", got)
	}
	for _, label := range []string{"Settings", "About", "Quit"} {
		assertMenuRow(t, nodes, accessRoleMenuItem, label, true)
	}

	// Choosing Settings still opens the Settings screen and closes the menu.
	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("click Settings: %v", err)
	}
	if h.app.Screen != Settings {
		t.Fatalf("screen = %v, want Settings", h.app.Screen)
	}
	if h.app.moreOpen {
		t.Fatal("account menu stayed open after choosing Settings")
	}
}

// TestRightNewTabMenuIsBrandedPopover covers the right panel's New tab picker:
// one popover panel clamped to the panel, four focusable menu-item rows, and
// choosing Workspace files still switches the tab.
func TestRightNewTabMenuIsBrandedPopover(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = startTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("New tab"); err != nil {
		t.Fatalf("open New tab menu: %v", err)
	}
	tt.Frame()
	if !h.app.shellUI().rightNewTabOpen {
		t.Fatal("New tab menu did not open")
	}
	nodes := testerAccessNodes(t, tt)
	if got := popupCount(nodes); got != 1 {
		t.Fatalf("New tab menu popup panels = %d, want 1", got)
	}
	for _, label := range []string{"Start page", "Workspace files", "New terminal", "Session log"} {
		assertMenuRow(t, nodes, accessRoleMenuItem, label, true)
	}

	if err := tt.Click("Workspace files"); err != nil {
		t.Fatalf("click Workspace files: %v", err)
	}
	if got := h.app.rightTab; got != filesTab {
		t.Fatalf("right tab = %v, want Files", got)
	}
	if h.app.shellUI().rightNewTabOpen {
		t.Fatal("New tab menu stayed open after choosing a tab")
	}
}

// TestViewOptionsPopoverIsBranded covers the sidebar's View options menu: one
// branded panel clamped to the sidebar, the checkable rows keep their
// RoleMenuItemCheckBox/Focusable/Checked semantics, and choosing a grouping
// still closes the menu and persists the choice.
func TestViewOptionsPopoverIsBranded(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("View options"); err != nil {
		t.Fatalf("open View options: %v", err)
	}
	tt.Frame()
	if !h.app.viewOptionsOpen {
		t.Fatal("View options did not open")
	}
	nodes := testerAccessNodes(t, tt)
	if got := popupCount(nodes); got != 1 {
		t.Fatalf("View options popup panels = %d, want 1", got)
	}
	checkedLabel := h.app.T("search.workspace")
	if states := assertMenuRow(t, nodes, accessRoleMenuItemCheckBox, checkedLabel, true); states&accessCheckedState == 0 {
		t.Fatalf("current grouping %q is not exposed as checked", checkedLabel)
	}
	listLabel := h.app.T("search.in_one_list")
	if states := assertMenuRow(t, nodes, accessRoleMenuItemCheckBox, listLabel, true); states&accessCheckedState != 0 {
		t.Fatalf("%q is checked while the workspace grouping is current", listLabel)
	}
	for _, label := range []string{
		h.app.T("search.workspace_tree"), h.app.T("search.manual"),
		h.app.T("search.hide_archived"), h.app.T("search.archived_only"),
	} {
		assertMenuRow(t, nodes, accessRoleMenuItemCheckBox, label, true)
	}

	if err := tt.Click(listLabel); err != nil {
		t.Fatalf("choose %q: %v", listLabel, err)
	}
	if h.app.viewOptionsOpen {
		t.Fatal("View options stayed open after choosing a grouping")
	}
	if got := h.app.groupBy; got != groupByList {
		t.Fatalf("grouping = %q, want %q", got, groupByList)
	}
}
