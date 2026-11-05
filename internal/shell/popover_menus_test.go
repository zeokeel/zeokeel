package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// platform.AccessRole/AccessStates values the popover-menu tests assert on
// (mygo v0.7.4 maps ui.Role to platform.AccessRole as Role - RoleGroup).
const (
	accessRolePopup         uint8  = 14
	accessRoleMenuItem      uint8  = 40
	accessRoleMenuItemRadio uint8  = 42
	accessCheckedState      uint64 = 1 << 2
)

// accessNodeIndex returns the index of the first node with role and label, or
// -1. A menu row carries its label explicitly (Label(label)), so it is the one
// node of that role with the option's text.
func accessNodeIndex(nodes []testerAccessNodeInfo, role uint8, label string) int {
	for i, n := range nodes {
		if n.Role == role && n.Label == label {
			return i
		}
	}
	return -1
}

// nodeInPopup reports whether some ancestor of nodes[index] is a popup panel.
func nodeInPopup(nodes []testerAccessNodeInfo, index int) bool {
	for id := nodes[index].Parent; id >= 0 && id < len(nodes); {
		if nodes[id].Role == accessRolePopup {
			return true
		}
		id = nodes[id].Parent
	}
	return false
}

// assertMenuRow checks one labelled menu row: the expected role, focusable, and
// (for popover menus) inside an open popup panel. It returns the node's states
// so callers can assert the checked option.
func assertMenuRow(t *testing.T, nodes []testerAccessNodeInfo, role uint8, label string, inPopup bool) uint64 {
	t.Helper()
	index := accessNodeIndex(nodes, role, label)
	if index < 0 {
		t.Fatalf("accessibility tree has no role-%d node labeled %q", role, label)
	}
	node := nodes[index]
	if node.States&accessFocusableState == 0 {
		t.Fatalf("menu row %q is not focusable", label)
	}
	if inPopup && !nodeInPopup(nodes, index) {
		t.Fatalf("menu row %q is not inside a popup panel", label)
	}
	return node.States
}

// popupCount reports how many popup panels the frame has.
func popupCount(nodes []testerAccessNodeInfo) int {
	count := 0
	for _, n := range nodes {
		if n.Role == accessRolePopup {
			count++
		}
	}
	return count
}

// openPermissionMenu opens the composer access-mode menu and returns the
// tester plus the tree of the frame showing it.
func openPermissionMenu(t *testing.T) (*liveApp, *ui.Tester, []testerAccessNodeInfo) {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	chip := "Access mode, current: " + h.app.composerPermissionLabel(h.app.currentSettings().Permission)
	if err := tt.Click(chip); err != nil {
		t.Fatalf("open access menu: %v", err)
	}
	tt.Frame()
	if !h.app.liveMenus().permissionOpen {
		t.Fatal("access mode did not open its permission menu")
	}
	return h, tt, testerAccessNodes(t, tt)
}

// TestPermissionMenuIsBrandedPopover is the regression guard for the reported
// "no style here" access menu: the four access modes are one branded popover
// panel of focusable menu-item rows, with the current one checked, and picking
// one still writes the session's permission and closes the menu.
func TestPermissionMenuIsBrandedPopover(t *testing.T) {
	h, tt, nodes := openPermissionMenu(t)

	// One branded panel holds the whole menu, not a bare list over the screen.
	if got := popupCount(nodes); got != 1 {
		t.Fatalf("permission menu popup panels = %d, want 1", got)
	}
	options := []string{
		"Read Only",
		"Workspace Write",
		"Full access",
		h.app.autoReviewLabel(),
	}
	for _, label := range options {
		assertMenuRow(t, nodes, accessRoleMenuItemRadio, label, true)
		if !tt.HasText(label) {
			t.Fatalf("permission menu does not show %q: %v", label, tt.Texts())
		}
	}
	// The effective mode is the checked option; the others are not checked.
	if states := assertMenuRow(t, nodes, accessRoleMenuItemRadio, "Workspace Write", true); states&accessCheckedState == 0 {
		t.Fatal("the current access mode is not exposed as checked")
	}
	for _, label := range []string{"Read Only", "Full access", h.app.autoReviewLabel()} {
		if states := assertMenuRow(t, nodes, accessRoleMenuItemRadio, label, true); states&accessCheckedState != 0 {
			t.Fatalf("%q is checked while Workspace Write is the current mode", label)
		}
	}
	// Auto review keeps its explanation under the label, inside the same row.
	if !tt.HasText(h.app.autoReviewDescription()) {
		t.Fatalf("access menu lost the Auto review description: %v", tt.Texts())
	}

	// Choosing still writes the session override and closes the panel.
	if err := tt.Click("Full access"); err != nil {
		t.Fatalf("choose Full access: %v", err)
	}
	if got := h.ctrl.SessionPermissionOrDefault(h.ctrl.CurrentSessionID()); got != session.PermissionFull {
		t.Fatalf("session permission = %q, want full", got)
	}
	if h.app.liveMenus().permissionOpen {
		t.Fatal("permission menu stayed open after choosing an option")
	}
	tt.Frame()
	if !tt.HasText("Access mode, current: Full access") {
		t.Fatalf("access chip did not show the chosen mode: %v", tt.Texts())
	}
}

// TestComposerPopoversUseBrandedMenuRows covers the adjacent composer and
// header popovers: each option is a focusable menu row inside the shared
// popover panel, so the permission menu no longer stands out as the only
// styled menu (or the only unstyled one).
func TestComposerPopoversUseBrandedMenuRows(t *testing.T) {
	t.Run("agent mode", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("Standard mode"); err != nil {
			t.Fatalf("open mode menu: %v", err)
		}
		tt.Frame()
		nodes := testerAccessNodes(t, tt)
		if got := popupCount(nodes); got != 1 {
			t.Fatalf("mode menu popup panels = %d, want 1", got)
		}
		for _, label := range []string{"Standard mode", "Minimal mode", "Creator mode"} {
			assertMenuRow(t, nodes, accessRoleMenuItemRadio, label, true)
		}
		if states := assertMenuRow(t, nodes, accessRoleMenuItemRadio, "Standard mode", true); states&accessCheckedState == 0 {
			t.Fatal("the current agent mode is not exposed as checked")
		}
	})

	t.Run("model", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		first, err := h.ctrl.Models().Create(store.Model{Name: "Model One", Model: "one", ReasoningEffort: "High"})
		if err != nil {
			t.Fatalf("create model: %v", err)
		}
		if _, err := h.ctrl.Models().Create(store.Model{Name: "Model Two", Model: "two"}); err != nil {
			t.Fatalf("create model: %v", err)
		}
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click(modelChipLabel(h, first)); err != nil {
			t.Fatalf("open model menu: %v", err)
		}
		tt.Frame()
		nodes := testerAccessNodes(t, tt)
		if got := popupCount(nodes); got != 1 {
			t.Fatalf("model menu popup panels = %d, want 1", got)
		}
		for _, label := range []string{first.Name, "Model Two"} {
			assertMenuRow(t, nodes, accessRoleMenuItemRadio, label, true)
		}
		if states := assertMenuRow(t, nodes, accessRoleMenuItemRadio, first.Name, true); states&accessCheckedState == 0 {
			t.Fatal("the selected model is not exposed as checked")
		}
		if !tt.HasText("one · High") {
			t.Fatalf("model row lost its detail line: %v", tt.Texts())
		}
	})

	t.Run("workspace", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Home
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("Workspace, current: test"); err != nil {
			t.Fatalf("open workspace picker: %v", err)
		}
		tt.Frame()
		nodes := testerAccessNodes(t, tt)
		if got := popupCount(nodes); got != 1 {
			t.Fatalf("workspace menu popup panels = %d, want 1", got)
		}
		assertMenuRow(t, nodes, accessRoleMenuItemRadio, "Switch to test", true)
		assertMenuRow(t, nodes, accessRoleMenuItem, h.app.T("workspace.add_workspace_ellipsis"), true)
	})

	t.Run("header more", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("More actions"); err != nil {
			t.Fatalf("open header menu: %v", err)
		}
		tt.Frame()
		nodes := testerAccessNodes(t, tt)
		if got := popupCount(nodes); got != 1 {
			t.Fatalf("header menu popup panels = %d, want 1", got)
		}
		for _, label := range []string{"Fork session", "Download session log", "Feedback"} {
			assertMenuRow(t, nodes, accessRoleMenuItem, label, true)
		}
	})

	// The "+" attachment menu and the "/" command palette are composer panels
	// above the card (menuFrame) rather than popovers, but their rows use the
	// same branded menu row.
	t.Run("attach", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("Add files or run commands"); err != nil {
			t.Fatalf("open attachment menu: %v", err)
		}
		tt.Frame()
		nodes := testerAccessNodes(t, tt)
		for _, label := range []string{h.app.T("composer.files"), h.app.T("composer.commands")} {
			assertMenuRow(t, nodes, accessRoleMenuItem, label, false)
		}
	})

	t.Run("command palette", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("Composer input"); err != nil {
			t.Fatalf("focus composer: %v", err)
		}
		tt.Type("/")
		tt.Frame()
		nodes := testerAccessNodes(t, tt)
		for _, label := range []string{"/clear", "/stop", "/plan", "/help"} {
			assertMenuRow(t, nodes, accessRoleMenuItem, label, false)
		}
	})
}
