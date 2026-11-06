package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// The M4 layout: global memories live in Settings › Memory (one page), and the
// current workspace's memories live in the session's Memory tab beside
// Trajectory. Both surfaces share the same manager (mode, counts, pending
// inbox, active list, add/edit modals, scoped clear and export), so every test
// below drives the real UI through ui.NewTester events.

// Accessibility roles pinned from mygo's platform package: RoleButton is 2
// and RoleDialog is 13 in platform.AccessRole's zero-based order.
const (
	memoryAccessRoleButton uint8 = 2
	memoryAccessRoleDialog uint8 = 13
)

// newMemoryApp is the live fixture with English labels.
func newMemoryApp(t *testing.T) *liveApp {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	return h
}

// openMemorySettings opens Settings › Memory through the real UI.
func openMemorySettings(t *testing.T, h *liveApp, tt *ui.Tester) {
	t.Helper()
	tt.Key(ui.Cmd, ui.KeyComma)
	if h.app.Screen != Settings {
		t.Fatalf("settings shortcut left screen %v, want Settings", h.app.Screen)
	}
	clickSettingsNav(t, tt, "Memory")
	tt.Frame()
	tt.Frame()
	if got := h.app.shellUI().settingsTab; got != settingsTabMemory {
		t.Fatalf("settings tab = %v, want memory (texts: %v)", got, tt.Texts())
	}
}

// openSessionMemoryTab clicks the session's Memory tab and settles a frame.
func openSessionMemoryTab(t *testing.T, h *liveApp, tt *ui.Tester) {
	t.Helper()
	if err := tt.Click("Memory"); err != nil {
		t.Fatalf("click the session Memory tab: %v", err)
	}
	tt.Frame()
	if got, want := h.app.uiState().tab, h.app.liveTabPosition(liveMemory); got != want {
		t.Fatalf("session tab position = %d, want Memory (%d)", got, want)
	}
}

// memoryAccessState returns the accessibility role and states of the first
// node labeled label.
func memoryAccessState(t *testing.T, tt *ui.Tester, label string) (uint8, uint64) {
	t.Helper()
	for _, node := range testerAccessNodes(t, tt) {
		if node.Label == label {
			return node.Role, node.States
		}
	}
	t.Fatalf("accessibility tree has no node labeled %q", label)
	return 0, 0
}

// memoryAccessNode is one node of the tester's accessibility tree with its
// window box, read through the same test-only reflection as testerAccessNodes.
type memoryAccessNode struct {
	ID     uint64
	Role   uint8
	Label  string
	Value  string
	States uint64
	Parent int
	Box    ui.Rect
}

// memoryAccessNodes returns the tester's accessibility tree with bounds.
func memoryAccessNodes(t *testing.T, tt *ui.Tester) []memoryAccessNode {
	t.Helper()
	rv := reflect.ValueOf(tt).Elem().FieldByName("rt")
	if !rv.IsValid() {
		t.Fatal("ui.Tester has no rt field; update the accessibility test")
	}
	rt := reflect.NewAt(rv.Type(), unsafe.Pointer(rv.UnsafeAddr())).Elem()
	flag := rt.Elem().FieldByName("access")
	if flag.Kind() != reflect.Bool {
		t.Fatal("ui engine has no access flag")
	}
	*(*bool)(unsafe.Pointer(flag.UnsafeAddr())) = true
	tt.Frame()

	v := reflect.ValueOf(tt).Elem().FieldByName("h")
	if !v.IsValid() {
		t.Fatal("ui.Tester has no h field")
	}
	h := reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	access := h.Elem().FieldByName("access")
	if !access.IsValid() || access.IsNil() {
		t.Fatal("ui.Tester did not build an accessibility tree")
	}
	tree := reflect.NewAt(access.Type(), unsafe.Pointer(access.UnsafeAddr())).Elem().Elem()
	nodes := tree.FieldByName("Nodes")
	out := make([]memoryAccessNode, nodes.Len())
	for i := 0; i < nodes.Len(); i++ {
		n := nodes.Index(i)
		box := n.FieldByName("Bounds")
		out[i] = memoryAccessNode{
			ID:     n.FieldByName("ID").Uint(),
			Role:   uint8(n.FieldByName("Role").Uint()),
			Label:  n.FieldByName("Label").String(),
			Value:  n.FieldByName("Value").String(),
			States: n.FieldByName("States").Uint(),
			Parent: int(n.FieldByName("Parent").Int()),
			Box: ui.Rect{
				X: float32(box.FieldByName("X").Float()),
				Y: float32(box.FieldByName("Y").Float()),
				W: float32(box.FieldByName("W").Float()),
				H: float32(box.FieldByName("H").Float()),
			},
		}
	}
	return out
}

// clickSettingsNav clicks one nav cell of the Settings overlay. The overlay is
// drawn over the mounted screen, so a nav label can also exist behind it (the
// session Memory tab): the node is picked by its Settings panel ancestry, which
// no background element has.
func clickSettingsNav(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	nodes := memoryAccessNodes(t, tt)
	for i := range nodes {
		if nodes[i].Label != label {
			continue
		}
		for parent := nodes[i].Parent; parent >= 0 && parent < len(nodes); parent = nodes[parent].Parent {
			if nodes[parent].Role == memoryAccessRoleDialog {
				tt.ClickAt(nodes[i].Box.X+nodes[i].Box.W/2, nodes[i].Box.Y+nodes[i].Box.H/2)
				return
			}
		}
	}
	t.Fatalf("settings nav %q not found", label)
}

// memoryTextValues returns label -> value of the a11y text fields (the modal's
// multi-line content editor included: mygo reports every editable node as a
// text field).
func memoryTextValues(t *testing.T, tt *ui.Tester) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, node := range testerAccessNodes(t, tt) {
		if node.Role == nativeRoleTextField && node.Label != "" {
			out[node.Label] = node.Value
		}
	}
	return out
}

// seedScopedMemory creates one scoped memory and returns it.
func seedScopedMemory(t *testing.T, h *liveApp, m store.Memory) *store.Memory {
	t.Helper()
	created, err := h.store.CreateMemoryScoped(m)
	if err != nil {
		t.Fatalf("CreateMemoryScoped(%+v): %v", m, err)
	}
	return created
}

// memoryCounts reads one scope's counts through the store.
func memoryCounts(t *testing.T, h *liveApp, scope, workspaceID string) (active, pending int) {
	t.Helper()
	active, pending, err := h.store.MemoryCounts(scope, workspaceID)
	if err != nil {
		t.Fatalf("MemoryCounts(%s,%s): %v", scope, workspaceID, err)
	}
	return active, pending
}

// loadMemorySettings reads the persisted settings the pages write.
func loadMemorySettings(t *testing.T, h *liveApp) session.Settings {
	t.Helper()
	settings, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Settings.Load: %v", err)
	}
	return session.NormalizeSettings(settings)
}

// singleExportedFile finds the one markdown file with prefix under dir.
func singleExportedFile(t *testing.T, dir, prefix string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read export dir %s: %v", dir, err)
	}
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".md") {
			matches = append(matches, filepath.Join(dir, name))
		}
	}
	if len(matches) != 1 {
		t.Fatalf("exported files with prefix %q = %v, want exactly one", prefix, matches)
	}
	return matches[0]
}

// findMemoryByContent returns the one stored memory of a scope with content.
func findMemoryByContent(t *testing.T, h *liveApp, scope, workspaceID, content string) *store.Memory {
	t.Helper()
	list, err := h.store.ListMemoriesScoped(scope, workspaceID, "", 0)
	if err != nil {
		t.Fatalf("ListMemoriesScoped(%s,%s): %v", scope, workspaceID, err)
	}
	for i := range list {
		if list[i].Content == content {
			return &list[i]
		}
	}
	t.Fatalf("no %s memory with content %q in %+v", scope, content, list)
	return nil
}

// ---------------------------------------------------------------------------
// Settings › Memory: global page only.
// ---------------------------------------------------------------------------

// TestMemorySettingsIsGlobalOnly pins the new Settings layout: one Global
// memory page with no scope tabs, global mode/counts/pending/active, add/edit
// management, and no workspace data leaking into it.
func TestMemorySettingsIsGlobalOnly(t *testing.T) {
	h := newMemoryApp(t)
	workspaceID := h.ctrl.CurrentWorkspaceID()
	globalActive := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Kind: store.MemoryKindUser,
		Content: "Answer with the conclusion first.",
	})
	globalPending := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Kind: store.MemoryKindReference,
		Content: "Global pending suggestion.", Status: store.MemoryStatusPending,
		Source: store.MemorySourceAuto,
	})
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Content: "Workspace note must stay in the chat tab.",
	})
	other, err := h.store.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace other: %v", err)
	}
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: other.ID,
		Content: "Other workspace note stays hidden.",
	})

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openMemorySettings(t, h, tt)

	// One page: the scope tabs are gone and the heading names the global scope.
	if !tt.HasText("Global memory") {
		t.Fatalf("global memory heading missing: %v", tt.Texts())
	}
	for _, gone := range []string{"Global (this machine)", "Workspace (test)", "Workspace (other)"} {
		if tt.HasText(gone) {
			t.Fatalf("settings still renders the old scope tab %q: %v", gone, tt.Texts())
		}
	}
	for _, leaked := range []string{
		"Workspace note must stay in the chat tab.",
		"Other workspace note stays hidden.",
	} {
		if tt.HasText(leaked) {
			t.Fatalf("settings leaked a workspace memory %q: %v", leaked, tt.Texts())
		}
	}

	// Global defaults to Suggest; the mode control and counts are present.
	for _, want := range []string{
		"Answer with the conclusion first.",
		"Global pending suggestion.",
		"Suggestions to review",
		"1 active · 1 pending",
		"The agent suggests memories for you to review.",
	} {
		if !tt.HasText(want) {
			t.Fatalf("global page missing %q: %v", want, tt.Texts())
		}
	}
	role, states := memoryAccessState(t, tt, "Suggest")
	if role != nativeRoleRadio || states&accessCheckedState == 0 {
		t.Fatalf("global mode Suggest is not the checked radio (role %d states %d)", role, states)
	}

	// Picking Auto persists only the global mode.
	if err := tt.Click("Auto"); err != nil {
		t.Fatalf("pick global Auto: %v", err)
	}
	tt.Frame()
	settings := loadMemorySettings(t, h)
	if settings.MemoryGlobalMode != session.MemoryModeAuto {
		t.Fatalf("global mode = %q, want auto", settings.MemoryGlobalMode)
	}
	if settings.MemoryWorkspaceMode != session.MemoryModeAuto {
		t.Fatalf("workspace mode changed with the global page: %q", settings.MemoryWorkspaceMode)
	}

	// Add a global memory through the modal.
	if err := tt.Click("Add memory"); err != nil {
		t.Fatalf("open the add-memory modal: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Add memory") || !tt.HasText("Memory content") {
		t.Fatalf("add-memory modal missing: %v", tt.Texts())
	}
	tt.Type("Keep global answers short.")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save the new global memory: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Keep global answers short.") {
		t.Fatalf("new global memory missing from the list: %v", tt.Texts())
	}
	created := findMemoryByContent(t, h, store.MemoryScopeGlobal, "", "Keep global answers short.")
	if created.Scope != store.MemoryScopeGlobal || created.WorkspaceID != "" ||
		created.Kind != store.MemoryKindProject || created.Source != store.MemorySourceManual ||
		created.Status != store.MemoryStatusActive {
		t.Fatalf("created global memory = %+v, want global/project/manual/active", created)
	}

	// Edit it through the modal; the textarea is prefilled with the stored text.
	if err := tt.Click("Edit memory " + created.ID); err != nil {
		t.Fatalf("open the edit-memory modal: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Edit memory") {
		t.Fatalf("edit-memory modal missing: %v", tt.Texts())
	}
	if values := memoryTextValues(t, tt); values["Memory content"] != "Keep global answers short." {
		t.Fatalf("edit modal prefill = %q, want the stored content", values["Memory content"])
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Answer with the conclusion first and stay short.")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save the edited global memory: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Answer with the conclusion first and stay short.") {
		t.Fatalf("edited global memory missing from the list: %v", tt.Texts())
	}
	if stored, err := h.store.GetMemory(created.ID); err != nil || stored.Content != "Answer with the conclusion first and stay short." {
		t.Fatalf("stored global memory after edit = %+v, %v", stored, err)
	}

	// Delete removes the global row only.
	if err := tt.Click("Delete memory " + globalActive.ID); err != nil {
		t.Fatalf("delete the global memory: %v", err)
	}
	tt.Frame()
	if _, err := h.store.GetMemory(globalActive.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted global memory still in the store: %v", err)
	}
	if !tt.HasText("Memory deleted") {
		t.Fatalf("delete toast missing: %v", tt.Texts())
	}

	// Accept the global pending suggestion.
	if err := tt.Click("Accept memory " + globalPending.ID); err != nil {
		t.Fatalf("accept the global suggestion: %v", err)
	}
	tt.Frame()
	if active, pending := memoryCounts(t, h, store.MemoryScopeGlobal, ""); pending != 0 || active != 2 {
		t.Fatalf("global counts after accept = %d active/%d pending, want 2/0", active, pending)
	}

	// The global export writes a global-scoped markdown file.
	if err := tt.Click("Export markdown"); err != nil {
		t.Fatalf("export the global memories: %v", err)
	}
	tt.Frame()
	globalPath := singleExportedFile(t, filepath.Join(h.store.Dir(), "memory"), "global-")
	body, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global export: %v", err)
	}
	for _, want := range []string{"# Memory", "Global (this machine)", "Active: 2 · Pending: 0", "Answer with the conclusion first and stay short."} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("global export missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "Workspace note must stay in the chat tab.") {
		t.Fatalf("global export leaked a workspace memory:\n%s", body)
	}
	if !tt.HasText("Exported to ") {
		t.Fatalf("export toast missing: %v", tt.Texts())
	}

	// Clear all is scoped to global, asks first and keeps workspace rows.
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("open the global clear confirmation: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Clear all global memories?") {
		t.Fatalf("global clear confirmation missing: %v", tt.Texts())
	}
	if !dangerPixels(tt, "Clear memories") {
		t.Fatal("the confirmation's Clear memories is not painted in the danger color")
	}
	// Enter activates the default-focused Cancel, never the danger button.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if active, _ := memoryCounts(t, h, store.MemoryScopeGlobal, ""); active != 2 {
		t.Fatalf("Enter on the confirmation cleared global memories: %d left", active)
	}
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("reopen the global clear confirmation: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel global clear: %v", err)
	}
	tt.Frame()
	if active, _ := memoryCounts(t, h, store.MemoryScopeGlobal, ""); active != 2 {
		t.Fatalf("Cancel cleared global memories: %d left", active)
	}
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("reopen the global clear confirmation: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Clear memories"); err != nil {
		t.Fatalf("confirm global clear: %v", err)
	}
	tt.Frame()
	if active, pending := memoryCounts(t, h, store.MemoryScopeGlobal, ""); active != 0 || pending != 0 {
		t.Fatalf("global memories after clear = %d active/%d pending", active, pending)
	}
	if got, err := h.store.ListMemoriesScoped(store.MemoryScopeWorkspace, workspaceID, "", 0); err != nil || len(got) != 1 {
		t.Fatalf("workspace memories after global clear = %+v, %v; want untouched", got, err)
	}
}

// ---------------------------------------------------------------------------
// Session Memory tab: the workspace scope.
// ---------------------------------------------------------------------------

// TestSessionMemoryTabWorkspaceScope pins the session-side layout: Memory is
// the third header tab beside Chat/Trajectory, shows only the current
// workspace's memories, and manages them (mode, pending, add/edit, delete,
// clear, export) without leaking the global scope.
func TestSessionMemoryTabWorkspaceScope(t *testing.T) {
	h := newMemoryApp(t)
	workspaceID := h.ctrl.CurrentWorkspaceID()
	wsActive := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Kind: store.MemoryKindProject, Content: "Run make lint before committing.",
	})
	wsPending := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Content: "Workspace pending suggestion.", Status: store.MemoryStatusPending,
		Source: store.MemorySourceAuto,
	})
	global := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Content: "Global memory stays in Settings.",
	})
	other, err := h.store.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace other: %v", err)
	}
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: other.ID,
		Content: "Other workspace memory stays hidden.",
	})

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// Tab order Chat | Trajectory | Memory, with tab accessibility semantics.
	chat, okChat := tt.Find("Chat")
	traj, okTraj := tt.Find("Trajectory")
	memoryTab, okMem := tt.Find("Memory")
	if !okChat || !okTraj || !okMem {
		t.Fatalf("session header tabs missing: chat=%v trajectory=%v memory=%v (%v)", okChat, okTraj, okMem, tt.Texts())
	}
	if !(chat.X < traj.X && traj.X < memoryTab.X) {
		t.Fatalf("tab order = chat %.1f, trajectory %.1f, memory %.1f", chat.X, traj.X, memoryTab.X)
	}
	if role, states := memoryAccessState(t, tt, "Memory"); role != nativeRoleTab {
		t.Fatalf("Memory tab role = %d, want tab", role)
	} else if states&accessCheckedState != 0 {
		t.Fatal("Memory tab is checked before it is selected")
	}

	openSessionMemoryTab(t, h, tt)
	if role, states := memoryAccessState(t, tt, "Memory"); role != nativeRoleTab || states&accessCheckedState == 0 {
		t.Fatalf("selected Memory tab does not expose tab+checked (role %d states %d)", role, states)
	}

	// The workspace pane shows only this workspace's rows.
	for _, want := range []string{
		"Workspace memory",
		"Run make lint before committing.",
		"Workspace pending suggestion.",
		"Suggestions to review",
		"1 active · 1 pending",
		"The agent saves useful memories automatically.",
	} {
		if !tt.HasText(want) {
			t.Fatalf("session memory pane missing %q: %v", want, tt.Texts())
		}
	}
	for _, hidden := range []string{
		"Global memory stays in Settings.",
		"Other workspace memory stays hidden.",
	} {
		if tt.HasText(hidden) {
			t.Fatalf("session memory pane leaked %q: %v", hidden, tt.Texts())
		}
	}

	// Picking Suggest persists the workspace mode only.
	if err := tt.Click("Suggest"); err != nil {
		t.Fatalf("pick workspace Suggest: %v", err)
	}
	tt.Frame()
	settings := loadMemorySettings(t, h)
	if settings.MemoryWorkspaceMode != session.MemoryModeSuggest {
		t.Fatalf("workspace mode = %q, want suggest", settings.MemoryWorkspaceMode)
	}
	if settings.MemoryGlobalMode != session.MemoryModeSuggest {
		t.Fatalf("global mode changed from the session pane: %q", settings.MemoryGlobalMode)
	}

	// Accept/Discard drive the workspace pending inbox.
	if err := tt.Click("Accept memory " + wsPending.ID); err != nil {
		t.Fatalf("accept the workspace suggestion: %v", err)
	}
	tt.Frame()
	if active, pending := memoryCounts(t, h, store.MemoryScopeWorkspace, workspaceID); active != 2 || pending != 0 {
		t.Fatalf("workspace counts after accept = %d/%d, want 2/0", active, pending)
	}
	if !tt.HasText("Suggestion accepted") {
		t.Fatalf("accept toast missing: %v", tt.Texts())
	}

	// Add a workspace memory via the modal (project/manual/active defaults).
	if err := tt.Click("Add memory"); err != nil {
		t.Fatalf("open the workspace add-memory modal: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Add memory") || !tt.HasText("Memory content") {
		t.Fatalf("workspace add-memory modal missing: %v", tt.Texts())
	}
	tt.Type("Keep the release branch green.")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save the workspace memory: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Keep the release branch green.") {
		t.Fatalf("new workspace memory missing: %v", tt.Texts())
	}
	created := findMemoryByContent(t, h, store.MemoryScopeWorkspace, workspaceID, "Keep the release branch green.")
	if created.Kind != store.MemoryKindProject || created.Source != store.MemorySourceManual || created.Status != store.MemoryStatusActive {
		t.Fatalf("created workspace memory = %+v, want project/manual/active", created)
	}

	// Edit and delete rows from the workspace pane; their labels carry the id
	// and they are keyboard-reachable buttons.
	for _, label := range []string{
		"Edit memory " + wsActive.ID,
		"Delete memory " + wsActive.ID,
	} {
		role, states := memoryAccessState(t, tt, label)
		if role != memoryAccessRoleButton {
			t.Fatalf("%q role = %d, want button (%d)", label, role, memoryAccessRoleButton)
		}
		if states&accessFocusableState == 0 {
			t.Fatalf("%q is not focusable", label)
		}
	}
	if err := tt.Click("Edit memory " + wsActive.ID); err != nil {
		t.Fatalf("open the workspace edit modal: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Edit memory") {
		t.Fatalf("edit modal missing: %v", tt.Texts())
	}
	if values := memoryTextValues(t, tt); values["Memory content"] != "Run make lint before committing." {
		t.Fatalf("edit prefill = %q", values["Memory content"])
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Run make lint and make test before committing.")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save the workspace edit: %v", err)
	}
	tt.Frame()
	if stored, err := h.store.GetMemory(wsActive.ID); err != nil || stored.Content != "Run make lint and make test before committing." {
		t.Fatalf("stored workspace memory after edit = %+v, %v", stored, err)
	}
	if err := tt.Click("Delete memory " + created.ID); err != nil {
		t.Fatalf("delete the workspace memory: %v", err)
	}
	tt.Frame()
	if _, err := h.store.GetMemory(created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted workspace memory still in the store: %v", err)
	}

	// The workspace export names the workspace and exports workspace rows only.
	if err := tt.Click("Export markdown"); err != nil {
		t.Fatalf("export the workspace memories: %v", err)
	}
	tt.Frame()
	wsPath := singleExportedFile(t, filepath.Join(h.store.Dir(), "memory"), "workspace-")
	body, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace export: %v", err)
	}
	if !strings.Contains(string(body), "Workspace (test)") ||
		!strings.Contains(string(body), "Run make lint and make test before committing.") {
		t.Fatalf("workspace export missing its content:\n%s", body)
	}
	if strings.Contains(string(body), "Global memory stays in Settings.") {
		t.Fatalf("workspace export leaked a global memory:\n%s", body)
	}

	// Clear all clears the workspace scope only.
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("open the workspace clear confirmation: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Clear all workspace memories?") {
		t.Fatalf("workspace clear confirmation missing: %v", tt.Texts())
	}
	if err := tt.Click("Clear memories"); err != nil {
		t.Fatalf("confirm the workspace clear: %v", err)
	}
	tt.Frame()
	if active, pending := memoryCounts(t, h, store.MemoryScopeWorkspace, workspaceID); active != 0 || pending != 0 {
		t.Fatalf("workspace memories after clear = %d/%d", active, pending)
	}
	if stored, err := h.store.GetMemory(global.ID); err != nil || stored.Content != "Global memory stays in Settings." {
		t.Fatalf("global memory touched by the workspace clear: %+v, %v", stored, err)
	}

	// The tab selection survives switching tabs and leaving for Settings.
	if err := tt.Click("Chat"); err != nil {
		t.Fatalf("switch back to Chat: %v", err)
	}
	tt.Frame()
	openSessionMemoryTab(t, h, tt)
	openMemorySettings(t, h, tt)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.Screen != Session {
		t.Fatalf("Escape left screen %v, want Session", h.app.Screen)
	}
	if got, want := h.app.uiState().tab, h.app.liveTabPosition(liveMemory); got != want {
		t.Fatalf("tab position after returning from Settings = %d, want Memory (%d)", got, want)
	}
	if !tt.HasText("Workspace memory") {
		t.Fatalf("session Memory pane not restored: %v", tt.Texts())
	}

	// The workspace pane links to the global Settings page.
	if err := tt.Click("Global memory…"); err != nil {
		t.Fatalf("open the global page from the session pane: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Settings || !tt.HasText("Global memory") {
		t.Fatalf("Global memory… entry did not open the global page: %v", tt.Texts())
	}
}

// TestSessionMemoryTabCodingViewOrder pins the dynamic tab order: Show coding
// view off hides Trajectory but never Memory, and the selection follows the
// tab's identity across the layout change.
func TestSessionMemoryTabCodingViewOrder(t *testing.T) {
	h := newMemoryApp(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSessionMemoryTab(t, h, tt)

	// Turn the coding view off through Settings; Memory stays selected even
	// though it moves from position 2 to position 1.
	openMemorySettings(t, h, tt)
	clickSettingsNav(t, tt, "General")
	tt.Frame()
	if err := tt.Click("Toggle show coding view"); err != nil {
		t.Fatalf("turn Show coding view off: %v", err)
	}
	tt.Frame()
	if h.app.currentSettings().ShowCodingView {
		t.Fatal("Show coding view did not persist off")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	if h.app.Screen != Session {
		t.Fatalf("screen after Settings = %v, want Session", h.app.Screen)
	}
	if tt.HasText("Trajectory") {
		t.Fatalf("Trajectory tab rendered with Show coding view off: %v", tt.Texts())
	}
	if !tt.HasText("Chat") || !tt.HasText("Memory") {
		t.Fatalf("Chat/Memory tabs missing with Show coding view off: %v", tt.Texts())
	}
	if got, want := h.app.uiState().tab, h.app.liveTabPosition(liveMemory); got != want {
		t.Fatalf("Memory selection after hiding Trajectory = position %d, want %d", got, want)
	}
	if !tt.HasText("Workspace memory") {
		t.Fatalf("Memory pane did not survive the layout change: %v", tt.Texts())
	}
	// Chat is still first, Memory second.
	chat, okChat := tt.Find("Chat")
	memoryTab, okMem := tt.Find("Memory")
	if !okChat || !okMem || !(chat.X < memoryTab.X) {
		t.Fatalf("tab order with coding view off: chat=%v memory=%v", okChat, okMem)
	}

	// Turning it back on restores Trajectory and keeps Memory selected.
	openMemorySettings(t, h, tt)
	clickSettingsNav(t, tt, "General")
	tt.Frame()
	if err := tt.Click("Toggle show coding view"); err != nil {
		t.Fatalf("turn Show coding view back on: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !tt.HasText("Trajectory") {
		t.Fatalf("Trajectory tab did not return: %v", tt.Texts())
	}
	if got, want := h.app.uiState().tab, h.app.liveTabPosition(liveMemory); got != want {
		t.Fatalf("Memory selection after restoring Trajectory = position %d, want %d", got, want)
	}
}

// TestSessionMemoryTabWithoutWorkspace pins the no-workspace path: the pane
// asks for a workspace while the global Settings page remains available.
func TestSessionMemoryTabWithoutWorkspace(t *testing.T) {
	h := newMemoryApp(t)
	if err := h.ctrl.DeleteWorkspace(context.Background(), h.ctrl.CurrentWorkspaceID()); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSessionMemoryTab(t, h, tt)
	if !tt.HasText("Select a workspace to view its workspace memory.") {
		t.Fatalf("no-workspace prompt missing: %v", tt.Texts())
	}
	// The global entry still opens Settings › Memory.
	openMemorySettings(t, h, tt)
	if !tt.HasText("Global memory") {
		t.Fatalf("global page did not open from the session pane: %v", tt.Texts())
	}
}

// ---------------------------------------------------------------------------
// Add/Edit validation.
// ---------------------------------------------------------------------------

// TestMemoryDialogsValidation pins the modal validation: empty content and
// content over the 8 KiB store limit are refused with a toast while the modal
// stays open, and Cancel closes without writing.
func TestMemoryDialogsValidation(t *testing.T) {
	h := newMemoryApp(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// Seed one row so Edit validation has a target.
	existing := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: h.ctrl.CurrentWorkspaceID(),
		Content: "Editable memory.",
	})
	tt.Frame()
	openSessionMemoryTab(t, h, tt)

	// Empty content is refused.
	if err := tt.Click("Add memory"); err != nil {
		t.Fatalf("open add modal: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save empty memory: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Memory content is required.") {
		t.Fatalf("missing empty-content error: %v", tt.Texts())
	}
	if !tt.HasText("Memory content") {
		t.Fatalf("modal closed on the validation error: %v", tt.Texts())
	}
	if active, _ := memoryCounts(t, h, store.MemoryScopeWorkspace, h.ctrl.CurrentWorkspaceID()); active != 1 {
		t.Fatalf("empty save changed the store: %d active", active)
	}

	// Content over the store cap is refused; the modal stays open.
	if err := tt.Click("Memory content"); err != nil {
		t.Fatalf("focus the add textarea: %v", err)
	}
	tt.Type(strings.Repeat("x", store.MaxMemoryContentBytes+1))
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save oversized memory: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Memory content is too long.") {
		t.Fatalf("missing oversized-content error: %v", tt.Texts())
	}
	if active, _ := memoryCounts(t, h, store.MemoryScopeWorkspace, h.ctrl.CurrentWorkspaceID()); active != 1 {
		t.Fatalf("oversized save changed the store: %d active", active)
	}

	// Cancel closes the modal without writing the draft.
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel the add modal: %v", err)
	}
	tt.Frame()
	if h.app.memoryPanelsFor().workspace.addOpen {
		t.Fatalf("modal stayed open after Cancel: %v", tt.Texts())
	}
	if active, _ := memoryCounts(t, h, store.MemoryScopeWorkspace, h.ctrl.CurrentWorkspaceID()); active != 1 {
		t.Fatalf("Cancel wrote a memory: %d active", active)
	}

	// Editing to empty is refused and the stored content is unchanged.
	if err := tt.Click("Edit memory " + existing.ID); err != nil {
		t.Fatalf("open edit modal: %v", err)
	}
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save empty edit: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Memory content is required.") {
		t.Fatalf("missing edit empty-content error: %v", tt.Texts())
	}
	if stored, err := h.store.GetMemory(existing.ID); err != nil || stored.Content != "Editable memory." {
		t.Fatalf("empty edit changed the store: %+v, %v", stored, err)
	}
}

// ---------------------------------------------------------------------------
// /memory command contexts, /help and zh strings.
// ---------------------------------------------------------------------------

// TestMemorySlashCommandContexts pins the context split: in a session /memory
// selects the session Memory tab; on Home it opens Settings › Memory. /help
// lists the command in both languages.
func TestMemorySlashCommandContexts(t *testing.T) {
	h := newMemoryApp(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// Session context: the tab switches, the screen stays the session.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus the composer: %v", err)
	}
	tt.Type("/memory")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if h.app.Screen != Session {
		t.Fatalf("session /memory left screen %v, want Session", h.app.Screen)
	}
	if got, want := h.app.uiState().tab, h.app.liveTabPosition(liveMemory); got != want {
		t.Fatalf("session /memory selected position %d, want Memory (%d)", got, want)
	}
	if !tt.HasText("Workspace memory") {
		t.Fatalf("session /memory did not render the workspace pane: %v", tt.Texts())
	}

	// Home context: no session scope, so the global Settings page opens.
	h.app.Screen = Home
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus the Home composer: %v", err)
	}
	tt.Type("/memory")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if h.app.Screen != Settings || h.app.shellUI().settingsTab != settingsTabMemory {
		t.Fatalf("Home /memory opened screen %v tab %v", h.app.Screen, h.app.shellUI().settingsTab)
	}
	if !tt.HasText("Global memory") {
		t.Fatalf("Home /memory did not open the global page: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// /help documents /memory.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus the composer for /help: %v", err)
	}
	tt.Type("/help")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.Contains(h.app.draft, "/memory") {
		t.Fatalf("/help does not list /memory: %q", h.app.draft)
	}
}

// TestMemoryPageLocalizedZH guards the Chinese strings of both surfaces.
func TestMemoryPageLocalizedZH(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	workspaceID := h.ctrl.CurrentWorkspaceID()
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Content: "工作区记忆内容。", Status: store.MemoryStatusPending,
	})
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Content: "全局记忆内容。",
	})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// The session Memory tab is localized and shows the workspace pane strings.
	if err := tt.Click("记忆"); err != nil {
		t.Fatalf("click the zh Memory tab: %v", err)
	}
	tt.Frame()
	for _, want := range []string{"工作区记忆", "记忆模式", "自动", "待审核建议", "工作区记忆内容。"} {
		if !tt.HasText(want) {
			t.Fatalf("zh session memory pane missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("全局记忆内容。") {
		t.Fatalf("zh session pane leaked the global memory: %v", tt.Texts())
	}

	// Settings › Memory is the zh global page.
	tt.Key(ui.Cmd, ui.KeyComma)
	if h.app.Screen != Settings {
		t.Fatalf("settings shortcut left screen %v", h.app.Screen)
	}
	clickSettingsNav(t, tt, "记忆")
	tt.Frame()
	for _, want := range []string{"全局记忆", "全局记忆内容。", "已保存记忆", "添加记忆"} {
		if !tt.HasText(want) {
			t.Fatalf("zh global page missing %q: %v", want, tt.Texts())
		}
	}
}
