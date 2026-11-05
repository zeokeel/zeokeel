package shell

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/store"
)

// This file covers the native shell's accessibility tree and keyboard-only
// flows: the workspace → session hierarchy the screen reader reads (tree item
// levels), the accessible names of the new native controls, and Tab/Esc
// keyboard paths. The user-facing rules are recorded in
// docs/design/native-ui.md §18.

// The accessibility roles and states below are pinned from mygo v0.7.4's
// internal/platform/access.go. The Tester keeps its accessibility tree
// private, so the test reads it through the same struct layout the ui package
// uses (test-only introspection of the dependency; the build pins mygo
// v0.7.4). The bit for AccessExpanded is declared in a11y_expanded_test.go.
const (
	nativeRoleButton      uint8 = 2
	nativeRoleCheckBox    uint8 = 4
	nativeRoleRadio       uint8 = 5
	nativeRoleSwitch      uint8 = 6
	nativeRoleTextField   uint8 = 9
	nativeRolePopUpButton uint8 = 16
	nativeRoleTab         uint8 = 18
	nativeRoleSplitter    uint8 = 19
	nativeRoleTree        uint8 = 25
	nativeRoleTreeItem    uint8 = 26
	nativeRoleMenuButton  uint8 = 28
	nativeRoleToggle      uint8 = 31
	nativeRoleComboBox    uint8 = 32
	nativeRoleStepper     uint8 = 35

	nativeStateFocusable uint32 = 1 << 0
	nativeStateDisabled  uint32 = 1 << 1
)

// nativeAccessNode is one node of the tester's accessibility tree.
type nativeAccessNode struct {
	ID     uint64
	Role   uint8
	Label  string
	Level  int
	Parent int
	States uint32
}

// nativeAccessNodes renders one frame with accessibility on and returns the
// window's accessibility tree, in tree order (a parent before its children).
func nativeAccessNodes(t *testing.T, tt *ui.Tester) []nativeAccessNode {
	nodes, _ := nativeAccessTree(t, tt)
	return nodes
}

// nativeFocusedLabel returns the label of the node the accessibility tree
// reports as focused, "" when none. Unlike Tester.Focused, which also matches
// elements inside the focused container, this is the element that really has
// the keyboard focus: the check for a specific control being a Tab stop.
func nativeFocusedLabel(t *testing.T, tt *ui.Tester) string {
	nodes, focusID := nativeAccessTree(t, tt)
	for _, node := range nodes {
		if node.ID == focusID {
			return node.Label
		}
	}
	return ""
}

// nativeAccessTree renders one frame with accessibility on and returns the
// tree with the ID of the focused node.
func nativeAccessTree(t *testing.T, tt *ui.Tester) ([]nativeAccessNode, uint64) {
	t.Helper()
	// The headless Tester only describes frames for assistive technology
	// after a platform.AccessibilityOn event, which it does not expose. Set
	// the engine's access flag and render one frame, exactly as that event
	// does.
	rv := reflect.ValueOf(tt).Elem().FieldByName("rt")
	if !rv.IsValid() {
		t.Fatal("ui.Tester has no rt field; update the accessibility test")
	}
	rt := reflect.NewAt(rv.Type(), unsafe.Pointer(rv.UnsafeAddr())).Elem()
	flag := rt.Elem().FieldByName("access")
	if flag.Kind() != reflect.Bool {
		t.Fatal("ui engine has no access flag; update the accessibility test")
	}
	*(*bool)(unsafe.Pointer(flag.UnsafeAddr())) = true
	tt.Frame()

	v := reflect.ValueOf(tt).Elem().FieldByName("h")
	if !v.IsValid() {
		t.Fatal("ui.Tester has no h field; update the accessibility test")
	}
	h := reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	access := h.Elem().FieldByName("access")
	if !access.IsValid() || access.IsNil() {
		t.Fatal("ui.Tester did not build an accessibility tree")
	}
	tree := reflect.NewAt(access.Type(), unsafe.Pointer(access.UnsafeAddr())).Elem().Elem()
	nodes := tree.FieldByName("Nodes")
	out := make([]nativeAccessNode, 0, nodes.Len())
	for i := 0; i < nodes.Len(); i++ {
		n := nodes.Index(i)
		node := nativeAccessNode{
			ID:     n.FieldByName("ID").Uint(),
			Role:   uint8(n.FieldByName("Role").Uint()),
			Label:  n.FieldByName("Label").String(),
			Level:  int(n.FieldByName("Level").Int()),
			Parent: int(n.FieldByName("Parent").Int()),
			States: uint32(n.FieldByName("States").Uint()),
		}
		out = append(out, node)
	}
	return out, tree.FieldByName("Focus").Uint()
}

// findNativeNode returns the first node with the label (and role, when role
// is not zero) and its index, which children name as their Parent.
func findNativeNode(nodes []nativeAccessNode, label string, role uint8) (nativeAccessNode, int, bool) {
	for i, node := range nodes {
		if node.Label != label {
			continue
		}
		if role != 0 && node.Role != role {
			continue
		}
		return node, i, true
	}
	return nativeAccessNode{}, -1, false
}

// nativeNodesByLabel returns every node with the label.
func nativeNodesByLabel(nodes []nativeAccessNode, label string) []nativeAccessNode {
	var out []nativeAccessNode
	for _, node := range nodes {
		if node.Label == label {
			out = append(out, node)
		}
	}
	return out
}

// TestA11ySidebarTreeHierarchy covers the screen reader hierarchy of the
// workspace → session tree: the tree role, the workspace item at level 1 with
// its expanded state, and its sessions at level 2, which the flat native
// Sidebar used to keep at level 1 (a flat list to assistive technology).
func TestA11ySidebarTreeHierarchy(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	if _, err := h.ctrl.CreateSessionInWorkspace(t.Context(), h.ctrl.CurrentWorkspaceID(), "second chat"); err != nil {
		t.Fatalf("second session: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	nodes := nativeAccessNodes(t, tt)
	tree, treeIdx, ok := findNativeNode(nodes, "Sidebar", nativeRoleTree)
	if !ok {
		t.Fatalf("the sidebar is not an accessibility tree: %q", accessLabels(nodes))
	}
	if tree.States&nativeStateFocusable == 0 {
		t.Fatal("the sidebar tree is not focusable")
	}

	workspace, workspaceIdx, ok := findNativeNode(nodes, "test", nativeRoleTreeItem)
	if !ok {
		t.Fatalf("workspace row is not a tree item: %q", accessLabels(nodes))
	}
	if workspace.Parent != treeIdx {
		t.Fatalf("workspace parent = %d, want the sidebar tree %d", workspace.Parent, treeIdx)
	}
	if workspace.Level != 1 {
		t.Fatalf("workspace level = %d, want 1", workspace.Level)
	}
	if workspace.States&accessExpandedState == 0 {
		t.Fatal("an expanded workspace row does not expose AccessExpanded")
	}

	// The sessions under the workspace are one level below it, so a screen
	// reader reads them as children of the workspace, not as siblings of it.
	for _, label := range []string{"live", "second chat"} {
		sess, sessIdx, ok := findNativeNode(nodes, label, nativeRoleTreeItem)
		if !ok {
			t.Fatalf("session row %q is not a tree item: %q", label, accessLabels(nodes))
		}
		if sess.Level != 2 {
			t.Fatalf("session %q level = %d, want 2 (one below its workspace)", label, sess.Level)
		}
		if sess.Parent != treeIdx {
			t.Fatalf("session %q parent = %d, want the sidebar tree %d", label, sess.Parent, treeIdx)
		}
		assertNodeWithinWorkspace(t, nodes, workspaceIdx, sessIdx)
	}

	// Collapsing the workspace from its accessible chevron action hides the
	// sessions and clears the expanded state.
	if err := tt.Click("Collapse workspace"); err != nil {
		t.Fatalf("collapse workspace: %v", err)
	}
	tt.Frame()
	nodes = nativeAccessNodes(t, tt)
	workspace, _, ok = findNativeNode(nodes, "test", nativeRoleTreeItem)
	if !ok {
		t.Fatalf("workspace row disappeared after collapsing: %q", accessLabels(nodes))
	}
	if workspace.States&accessExpandedState != 0 {
		t.Fatal("a collapsed workspace row still exposes AccessExpanded")
	}
	if _, _, ok := findNativeNode(nodes, "live", nativeRoleTreeItem); ok {
		t.Fatalf("a collapsed workspace still exposes its sessions: %q", accessLabels(nodes))
	}

	// Expanding it again from the keyboard-focusable chevron brings the
	// sessions back at their level.
	if err := tt.Click("Expand workspace"); err != nil {
		t.Fatalf("expand workspace: %v", err)
	}
	tt.Frame()
	nodes = nativeAccessNodes(t, tt)
	sess, _, ok := findNativeNode(nodes, "live", nativeRoleTreeItem)
	if !ok || sess.Level != 2 {
		t.Fatalf("re-expanded session = %+v, want a tree item at level 2", sess)
	}
}

// assertNodeWithinWorkspace checks that widget's tree item follows its
// workspace's in reading order and sits below it in level; the flat native
// Sidebar keeps the rows as siblings in the element tree, so reading order and
// level carry the relationship.
func assertNodeWithinWorkspace(t *testing.T, nodes []nativeAccessNode, workspaceIdx, widgetIdx int) {
	t.Helper()
	if widgetIdx <= workspaceIdx {
		t.Fatalf("session item at %d reads before its workspace at %d", widgetIdx, workspaceIdx)
	}
	for i := workspaceIdx + 1; i < widgetIdx; i++ {
		if nodes[i].Level == 0 {
			continue
		}
		if nodes[i].Level < nodes[widgetIdx].Level && nodes[i].Role == nativeRoleTreeItem {
			return
		}
	}
}

// TestA11ySidebarNestedWorkspaceLevels covers the Workspace Tree grouping: a
// child workspace is one level below its parent workspace, and the child's
// sessions one below that.
func TestA11ySidebarNestedWorkspaceLevels(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	parentRoot := t.TempDir()
	childRoot := parentRoot + "/child"
	parent, err := h.ctrl.CreateWorkspace(t.Context(), "parent-ws", parentRoot)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := h.ctrl.CreateWorkspace(t.Context(), "child-ws", childRoot)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := h.ctrl.SelectWorkspace(t.Context(), parent.ID); err != nil {
		t.Fatalf("select parent: %v", err)
	}
	if _, err := h.ctrl.CreateSessionInWorkspace(t.Context(), child.ID, "child conversation"); err != nil {
		t.Fatalf("child session: %v", err)
	}
	// The Workspace Tree grouping persists ui.sessions_group_by.
	if err := h.store.SetSetting(settingSessionsGroupBy, groupByTree); err != nil {
		t.Fatalf("group setting: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	nodes := nativeAccessNodes(t, tt)
	parentNode, _, ok := findNativeNode(nodes, "parent-ws", nativeRoleTreeItem)
	if !ok {
		t.Fatalf("parent workspace row missing: %q", accessLabels(nodes))
	}
	childNode, _, ok := findNativeNode(nodes, "child-ws", nativeRoleTreeItem)
	if !ok {
		t.Fatalf("child workspace row missing: %q", accessLabels(nodes))
	}
	if parentNode.Level != 1 || childNode.Level != 2 {
		t.Fatalf("workspace levels = %d/%d, want 1/2", parentNode.Level, childNode.Level)
	}
	childSess, _, ok := findNativeNode(nodes, "child conversation", nativeRoleTreeItem)
	if !ok {
		t.Fatalf("child session row missing: %q", accessLabels(nodes))
	}
	if childSess.Level != 3 {
		t.Fatalf("child session level = %d, want 3 (parent 1, child 2, session 3)", childSess.Level)
	}
}

// TestA11yNativeControlNames audits the native controls the native redesign
// added: every focusable or pressable control the accessibility tree exposes
// must have a name, and the specific new affordances must carry their role and
// state (sidebar tree items, inline row actions, right panel resize splitter).
func TestA11yNativeControlNames(t *testing.T) {
	newApp := func(t *testing.T) *liveApp {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		return h
	}

	for _, tc := range []struct {
		name     string
		screen   Screen
		setup    func(*liveApp)
		required []string
	}{
		{
			name: "home", screen: Home,
			required: []string{"New Session", "Plugins", "Automation tasks", "Add workspace"},
		},
		{
			name: "session", screen: Session,
			setup: func(h *liveApp) {
				h.app.rightOpen = true
				h.app.rightTab = filesTab
			},
			required: []string{"Resize right sidebar", "Collapse right sidebar", "Session actions for live"},
		},
		{
			name: "plugins", screen: Plugins,
			required: []string{"Add plugin", "About plugins", "Refresh"},
		},
		{
			name: "automation", screen: Automation,
			setup: func(h *liveApp) {
				if _, err := h.ctrl.Automations().Create(store.Automation{
					Title: "Audit task", Cron: "@hourly", Enabled: true,
				}); err != nil {
					panic(err)
				}
			},
			required: []string{"New automation task", "Search automation tasks", "Run now"},
		},
		{
			name: "settings", screen: Settings,
			required: []string{"Close settings", "General", "Open configuration file"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newApp(t)
			h.app.Screen = tc.screen
			if tc.setup != nil {
				tc.setup(h)
			}
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			nodes := nativeAccessNodes(t, tt)
			for _, node := range nodes {
				if node.Label != "" {
					continue
				}
				if node.States&nativeStateFocusable != 0 || nativeNeedsName(node.Role) {
					t.Errorf("screen %s: role %d has no accessible name", tc.name, node.Role)
				}
			}
			for _, label := range tc.required {
				if _, _, ok := findNativeNode(nodes, label, 0); !ok {
					t.Errorf("screen %s: control %q missing from the accessibility tree: %q",
						tc.name, label, accessLabels(nodes))
				}
			}
			// The sidebar tree items carry levels; no item may sit at level 0.
			for _, node := range nodes {
				if node.Role == nativeRoleTreeItem && node.Level == 0 {
					t.Errorf("screen %s: tree item %q has no level", tc.name, node.Label)
				}
			}
		})
	}

	// The right panel's drag edge keeps its splitter role and name, and the
	// sidebar's tree is focusable.
	h := newApp(t)
	h.app.Screen = Session
	h.app.rightOpen = true
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	nodes := nativeAccessNodes(t, tt)
	grip, _, ok := findNativeNode(nodes, "Resize right sidebar", nativeRoleSplitter)
	if !ok {
		t.Fatalf("right sidebar resize grip is not a named splitter: %q", accessLabels(nodes))
	}
	if grip.States&nativeStateFocusable == 0 {
		t.Fatal("right sidebar resize grip is not focusable")
	}
}

// nativeNeedsName reports whether a role is a control that assistive
// technology must be able to name.
func nativeNeedsName(role uint8) bool {
	switch role {
	case nativeRoleButton, nativeRoleCheckBox, nativeRoleRadio, nativeRoleSwitch,
		nativeRoleTextField, nativeRolePopUpButton, nativeRoleTab, nativeRoleSplitter,
		nativeRoleTreeItem, nativeRoleMenuButton, nativeRoleToggle,
		nativeRoleComboBox, nativeRoleStepper:
		return true
	}
	return false
}

// accessLabels lists the labels the tree exposes, for failure messages.
func accessLabels(nodes []nativeAccessNode) string {
	labels := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Label != "" {
			labels = append(labels, node.Label)
		}
	}
	return strings.Join(labels, " | ")
}

// TestA11ySidebarKeyboardNavigation covers the native Sidebar's keyboard
// semantics on the new tree: Home/End and the arrows navigate, the first
// letter of an item chooses it, and the expand/collapse action is a focusable
// button.
func TestA11ySidebarKeyboardNavigation(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	// Two sessions so an arrow key has a next session to open.
	if _, err := h.ctrl.CreateSessionInWorkspace(t.Context(), h.ctrl.CurrentWorkspaceID(), "second chat"); err != nil {
		t.Fatalf("second session: %v", err)
	}
	if _, err := h.ctrl.OpenSession(t.Context(), h.ctrl.CurrentSessionID()); err != nil {
		t.Fatalf("open session: %v", err)
	}
	h.app.SetSnapshot(h.ctrl.Snapshot())
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	focusSidebar := func(t *testing.T) {
		t.Helper()
		for i := 0; i < 60 && !tt.Focused("Sidebar"); i++ {
			tt.Key(0, ui.KeyTab)
		}
		if !tt.Focused("Sidebar") {
			t.Fatalf("Tab never reached the sidebar: %q", tt.Texts())
		}
	}
	focusSidebar(t)

	// Home chooses the first item (Home, the PD-11 return entry) and
	// navigates to it.
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if h.app.Screen != Home {
		t.Fatalf("Home selected screen %v, want Home", h.app.Screen)
	}

	// Home autofocuses its composer, so re-enter the sidebar before the next
	// keyboard step; End then chooses the last item (the last session) and
	// opens it.
	focusSidebar(t)
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if h.app.Screen != Session {
		t.Fatalf("End selected screen %v, want Session", h.app.Screen)
	}

	// An arrow moves the sidebar's choice to the next item and navigates:
	// from Home Down reaches Plugins, then Automation. Opening a session
	// moves the focus to its composer, so re-enter the sidebar first.
	focusSidebar(t)
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if h.app.Screen != Home {
		t.Fatalf("Home selected screen %v, want Home", h.app.Screen)
	}
	focusSidebar(t)
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if h.app.Screen != Plugins {
		t.Fatalf("Down selected screen %v, want Plugins", h.app.Screen)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if h.app.Screen != Automation {
		t.Fatalf("Down selected screen %v, want Automation", h.app.Screen)
	}

	// First-letter search: typing "a" chooses "Automation tasks".
	h.app.Screen = Home
	tt.Frame()
	focusSidebar(t)
	// The sidebar's first-letter search reads typed keys, not a text input.
	tt.TypeKey(0, ui.KeyA, "a")
	tt.Frame()
	if h.app.Screen != Automation {
		t.Fatalf("typing the first letter selected %v, want Automation", h.app.Screen)
	}
}

// TestA11ySidebarChevronKeyboard covers the workspace expand/collapse inline
// action as a keyboard control: Tab reaches it, Enter toggles the workspace.
func TestA11ySidebarChevronKeyboard(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	focused := false
	for i := 0; i < 80; i++ {
		if nativeFocusedLabel(t, tt) == "Collapse workspace" {
			focused = true
			break
		}
		tt.Key(0, ui.KeyTab)
	}
	if !focused {
		t.Fatalf("Tab never reached the workspace expand/collapse action: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if _, ok := tt.Find("Expand workspace"); !ok {
		t.Fatalf("Enter on the chevron did not collapse the workspace: %q", tt.Texts())
	}
	// The sessions below a collapsed workspace are gone from the tree.
	nodes := nativeAccessNodes(t, tt)
	if _, _, ok := findNativeNode(nodes, "live", nativeRoleTreeItem); ok {
		t.Fatalf("a collapsed workspace still exposes sessions: %q", accessLabels(nodes))
	}
}

// TestA11yKeyboardCoreFlows covers the keyboard-only paths the native redesign
// must keep: new session, settings open/close and Esc, plugin switches,
// automation dialog and the right sidebar, with no keyboard dead ends.
func TestA11yKeyboardCoreFlows(t *testing.T) {
	t.Run("new session", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		before, err := h.ctrl.ListSessions(t.Context())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		focused := false
		for i := 0; i < 40; i++ {
			if tt.Focused("New Session") {
				focused = true
				break
			}
			tt.Key(0, ui.KeyTab)
		}
		if !focused {
			t.Fatalf("Tab never reached the New Session button: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		after, err := h.ctrl.ListSessions(t.Context())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		if len(after) != len(before)+1 || h.app.Screen != Session {
			t.Fatalf("keyboard New Session created %d sessions and screen %v",
				len(after)-len(before), h.app.Screen)
		}
	})

	t.Run("settings open and close", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyComma)
		tt.Frame()
		if h.app.Screen != Settings {
			t.Fatalf("Cmd+, screen = %v, want Settings", h.app.Screen)
		}
		// The close control is reachable and closes the modal from the
		// keyboard.
		focused := false
		for i := 0; i < 40; i++ {
			if tt.Focused("Close settings") {
				focused = true
				break
			}
			tt.Key(0, ui.KeyTab)
		}
		if !focused {
			t.Fatalf("Tab never reached Close settings: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if h.app.Screen != Session {
			t.Fatalf("Enter on Close settings left screen %v, want Session", h.app.Screen)
		}
		// Esc closes the same overlay.
		tt.Key(ui.Cmd, ui.KeyComma)
		tt.Frame()
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if h.app.Screen != Session {
			t.Fatalf("Esc left screen %v, want Session", h.app.Screen)
		}
	})

	t.Run("plugin switch", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Plugins
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		name := "Enable Subagent"
		focused := false
		for i := 0; i < 60; i++ {
			if tt.Focused(name) {
				focused = true
				break
			}
			tt.Key(0, ui.KeyTab)
		}
		if !focused {
			t.Fatalf("Tab never reached %q: %q", name, tt.Texts())
		}
		if !h.app.pluginEnabledValue("subagent", true) {
			t.Fatalf("%q starts off; the test needs it on", name)
		}
		tt.Key(0, ui.KeySpace)
		tt.Frame()
		if h.app.pluginEnabledValue("subagent", true) {
			t.Fatalf("Space on %q did not turn the plugin off", name)
		}
	})

	t.Run("automation dialog", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		if _, err := h.ctrl.Automations().Create(store.Automation{
			Title: "Keyboard task", Cron: "@hourly", Enabled: true,
		}); err != nil {
			t.Fatalf("create automation: %v", err)
		}
		h.app.Screen = Automation
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		focused := false
		for i := 0; i < 60; i++ {
			if tt.Focused("New automation task") {
				focused = true
				break
			}
			tt.Key(0, ui.KeyTab)
		}
		if !focused {
			t.Fatalf("Tab never reached New automation task: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if !tt.HasText("Title") {
			t.Fatalf("Enter did not open the automation dialog: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if tt.HasText("Schedule") && tt.HasText("Title") {
			t.Fatalf("Esc did not close the automation dialog: %q", tt.Texts())
		}
	})

	t.Run("right sidebar", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		focused := false
		for i := 0; i < 60; i++ {
			if tt.Focused("Open right sidebar") {
				focused = true
				break
			}
			tt.Key(0, ui.KeyTab)
		}
		if !focused {
			t.Fatalf("Tab never reached Open right sidebar: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if !h.app.rightOpen {
			t.Fatal("Enter did not open the right sidebar")
		}
		focused = false
		for i := 0; i < 60; i++ {
			if tt.Focused("Collapse right sidebar") {
				focused = true
				break
			}
			tt.Key(0, ui.KeyTab)
		}
		if !focused {
			t.Fatalf("Tab never reached Collapse right sidebar: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if h.app.rightOpen {
			t.Fatal("Enter on Collapse right sidebar did not close the panel")
		}
	})

	t.Run("font size popover", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Settings
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		focused := false
		for i := 0; i < 60; i++ {
			if tt.Focused(fontSizeLabel(h.app.currentSettings().FontSize) + " px") {
				focused = true
				break
			}
			tt.Key(0, ui.KeyTab)
		}
		if !focused {
			t.Fatalf("Tab never reached the font size control: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if !tt.HasText("16 px") {
			t.Fatalf("Enter did not open the font size popover: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if tt.HasText("16 px") {
			t.Fatalf("Esc did not close the font size popover: %q", tt.Texts())
		}
	})
}

// TestA11yKeyboardEscClosesLayers covers Esc closing the modal layers in
// order: the settings modal, the search palette and popovers, so no keyboard
// user gets stuck in an overlay.
func TestA11yKeyboardEscClosesLayers(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// Palette: Cmd+, then Esc closes it.
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !h.app.paletteOpen {
		t.Fatal("Cmd+K did not open the search palette")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.paletteOpen {
		t.Fatal("Esc did not close the search palette")
	}

	// Settings: Esc closes the modal and returns to the screen beneath it.
	h.app.Screen = Session
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("Cmd+, screen = %v, want Settings", h.app.Screen)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.Screen != Session {
		t.Fatalf("Esc left screen %v, want Session", h.app.Screen)
	}

	// The view options popover: opened by its button, Esc closes it.
	h.app.Screen = Home
	tt.Frame()
	if err := tt.Click("View options"); err != nil {
		t.Fatalf("click View options: %v", err)
	}
	if !h.app.viewOptionsOpen {
		t.Fatal("View options popover did not open")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.viewOptionsOpen {
		t.Fatal("Esc did not close the View options popover")
	}
}

// TestA11ySidebarSessionActionsKeyboardReachable covers the new always-visible
// row action buttons: each has a name and a Tab stop (they were pointer-only
// before the native sidebar).
func TestA11ySidebarSessionActionsKeyboardReachable(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	assertTabReachable(t, tt, []string{
		"Collapse workspace", "New session in test", "Workspace actions for test",
		"Session actions for live", "New Session", "Collapse sidebar",
	})
	// The row actions carry the roles screen readers announce.
	nodes := nativeAccessNodes(t, tt)
	for _, label := range []string{"Collapse workspace", "New session in test", "Workspace actions for test", "Session actions for live"} {
		node, _, ok := findNativeNode(nodes, label, 0)
		if !ok {
			t.Fatalf("row action %q missing from the accessibility tree: %q", label, accessLabels(nodes))
		}
		if node.Role != nativeRoleButton && node.Role != nativeRolePopUpButton && node.Role != nativeRoleMenuButton {
			t.Fatalf("row action %q role = %d, want a button or menu button", label, node.Role)
		}
	}
}
