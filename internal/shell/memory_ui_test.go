package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// The Settings > Memory tests drive the two-tab control surface from
// docs/design/auto-memory.md §6: Global/Workspace tabs with their own
// Off/Suggest/Auto mode, per-scope pending review and active list, scoped
// Clear all, markdown export, and the /memory shortcut into the page.

// memoryAccessRoleButton is platform.AccessRole's RoleButton value, pinned in
// this file so the page tests do not depend on another feature's test file.
const memoryAccessRoleButton uint8 = 2

// openMemorySettings opens Settings > Memory through the real UI (Cmd+, then
// the Memory nav cell).
func openMemorySettings(t *testing.T, h *liveApp, tt *ui.Tester) {
	t.Helper()
	tt.Key(ui.Cmd, ui.KeyComma)
	if h.app.Screen != Settings {
		t.Fatalf("settings shortcut left screen %v, want Settings", h.app.Screen)
	}
	if err := tt.Click("Memory"); err != nil {
		t.Fatalf("click Memory nav: %v", err)
	}
	tt.Frame()
	if got := h.app.shellUI().settingsTab; got != settingsTabMemory {
		t.Fatalf("settings tab = %v, want memory", got)
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

// assertMemoryTabSelected checks the tab role and checked state of one page
// tab, the a11y contract the design asks for ("tabs role/selected").
func assertMemoryTabSelected(t *testing.T, tt *ui.Tester, label string, want bool) {
	t.Helper()
	role, states := memoryAccessState(t, tt, label)
	if role != nativeRoleTab {
		t.Fatalf("%q role = %d, want tab (%d)", label, role, nativeRoleTab)
	}
	if got := states&accessCheckedState != 0; got != want {
		t.Fatalf("%q checked = %v, want %v", label, got, want)
	}
}

// assertMemoryModeSelected checks one mode radio's checked state.
func assertMemoryModeSelected(t *testing.T, tt *ui.Tester, label string, want bool) {
	t.Helper()
	role, states := memoryAccessState(t, tt, label)
	if role != nativeRoleRadio {
		t.Fatalf("mode %q role = %d, want radio (%d)", label, role, nativeRoleRadio)
	}
	if got := states&accessCheckedState != 0; got != want {
		t.Fatalf("mode %q checked = %v, want %v", label, got, want)
	}
}

// newMemoryApp is the live fixture with English labels.
func newMemoryApp(t *testing.T) *liveApp {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	return h
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

// TestMemorySettingsTabsAndModes is the control-surface loop: the page has
// Global/Workspace tabs with tab semantics and keyboard switching, each tab
// shows its own mode (workspace defaults to auto, global to suggest), and a
// mode pick persists in Settings without touching the other scope.
func TestMemorySettingsTabsAndModes(t *testing.T) {
	h := newMemoryApp(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openMemorySettings(t, h, tt)

	// Both tabs are present; the workspace tab carries the workspace name and
	// the workspace scope is selected by default.
	if !tt.HasText("Global (this machine)") {
		t.Fatalf("Global tab missing: %v", tt.Texts())
	}
	if !tt.HasText("Workspace (test)") {
		t.Fatalf("Workspace tab missing the workspace name: %v", tt.Texts())
	}
	assertMemoryTabSelected(t, tt, "Global (this machine)", false)
	assertMemoryTabSelected(t, tt, "Workspace (test)", true)

	// Workspace defaults to Auto; the mode control exposes all three choices.
	for _, label := range []string{"Off", "Suggest", "Auto"} {
		if !tt.HasText(label) {
			t.Fatalf("mode choice %q missing: %v", label, tt.Texts())
		}
	}
	assertMemoryModeSelected(t, tt, "Auto", true)
	assertMemoryModeSelected(t, tt, "Suggest", false)
	if !tt.HasText("The agent saves useful memories automatically.") {
		t.Fatalf("auto mode description missing: %v", tt.Texts())
	}
	if !tt.HasText("0 active · 0 pending") {
		t.Fatalf("counts missing: %v", tt.Texts())
	}
	if !tt.HasText("No memories yet.") {
		t.Fatalf("empty state missing: %v", tt.Texts())
	}
	if tt.HasText("Suggestions to review") {
		t.Fatalf("empty pending inbox is not hidden: %v", tt.Texts())
	}

	// Global defaults to Suggest and reads its own setting.
	if err := tt.Click("Global (this machine)"); err != nil {
		t.Fatalf("switch to Global tab: %v", err)
	}
	tt.Frame()
	assertMemoryTabSelected(t, tt, "Global (this machine)", true)
	assertMemoryTabSelected(t, tt, "Workspace (test)", false)
	assertMemoryModeSelected(t, tt, "Suggest", true)
	if !tt.HasText("The agent suggests memories for you to review.") {
		t.Fatalf("suggest mode description missing: %v", tt.Texts())
	}

	// Picking Off on the Global tab persists only the global mode.
	if err := tt.Click("Off"); err != nil {
		t.Fatalf("pick global Off: %v", err)
	}
	tt.Frame()
	assertMemoryModeSelected(t, tt, "Off", true)
	settings := loadMemorySettings(t, h)
	if settings.MemoryGlobalMode != "off" {
		t.Fatalf("global mode = %q, want off", settings.MemoryGlobalMode)
	}
	if settings.MemoryWorkspaceMode != "auto" {
		t.Fatalf("workspace mode changed with the global tab: %q", settings.MemoryWorkspaceMode)
	}

	// Keyboard: the tab list owns Left/Right; focusing the selected tab and
	// pressing Left moves to the Workspace tab, where picking Suggest persists
	// only the workspace mode.
	if err := tt.Click("Global (this machine)"); err != nil {
		t.Fatalf("focus the Global tab: %v", err)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	assertMemoryTabSelected(t, tt, "Workspace (test)", true)
	if err := tt.Click("Suggest"); err != nil {
		t.Fatalf("pick workspace Suggest: %v", err)
	}
	tt.Frame()
	settings = loadMemorySettings(t, h)
	if settings.MemoryWorkspaceMode != "suggest" {
		t.Fatalf("workspace mode = %q, want suggest", settings.MemoryWorkspaceMode)
	}
	if settings.MemoryGlobalMode != "off" {
		t.Fatalf("global mode changed with the workspace tab: %q", settings.MemoryGlobalMode)
	}

	// The selected tab belongs to the App: leaving Settings and coming back
	// keeps the Global tab selected.
	if err := tt.Click("Global (this machine)"); err != nil {
		t.Fatalf("select the Global tab: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	openMemorySettings(t, h, tt)
	assertMemoryTabSelected(t, tt, "Global (this machine)", true)
}

// loadMemorySettings reads the persisted settings the page writes.
func loadMemorySettings(t *testing.T, h *liveApp) session.Settings {
	t.Helper()
	settings, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Settings.Load: %v", err)
	}
	return session.NormalizeSettings(settings)
}

// TestMemoryScopesPendingAndActive pins the two-scope list: the Workspace tab
// shows only the current workspace's active list and pending inbox, Accept
// promotes a suggestion to active, Discard deletes it, Delete removes an
// active note, and the Global tab sees machine-wide rows everywhere.
func TestMemoryScopesPendingAndActive(t *testing.T) {
	h := newMemoryApp(t)
	workspaceID := h.ctrl.CurrentWorkspaceID()
	globalActive := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Kind: store.MemoryKindUser,
		Content: "Answer with the conclusion first.",
	})
	globalPending := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Kind: store.MemoryKindReference,
		Content: "Global pending suggestion.",
		Status:  store.MemoryStatusPending, Source: store.MemorySourceAuto,
	})
	wsActive := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Kind: store.MemoryKindProject, Content: "Run make lint before committing.",
	})
	wsPending := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Kind: store.MemoryKindFeedback, Content: "Workspace pending suggestion.",
		Status: store.MemoryStatusPending, Source: store.MemorySourceAuto,
	})
	other, err := h.store.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace other: %v", err)
	}
	otherMemory := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: other.ID,
		Content: "Other workspace memory stays hidden.",
	})

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openMemorySettings(t, h, tt)

	// Workspace tab: its own active note and pending suggestion, nothing from
	// Global or another workspace.
	for _, want := range []string{
		"Run make lint before committing.",
		"Workspace pending suggestion.",
		"Suggestions to review",
		"Saved memories",
		"1 active · 1 pending",
	} {
		if !tt.HasText(want) {
			t.Fatalf("workspace tab missing %q: %v", want, tt.Texts())
		}
	}
	for _, hidden := range []string{
		"Answer with the conclusion first.",
		"Global pending suggestion.",
		"Other workspace memory stays hidden.",
	} {
		if tt.HasText(hidden) {
			t.Fatalf("workspace tab leaked %q: %v", hidden, tt.Texts())
		}
	}

	// The pending actions are keyboard-reachable buttons named per memory.
	for _, label := range []string{
		"Accept memory " + wsPending.ID,
		"Discard memory " + wsPending.ID,
	} {
		role, states := memoryAccessState(t, tt, label)
		if role != memoryAccessRoleButton {
			t.Fatalf("%q role = %d, want button (%d)", label, role, memoryAccessRoleButton)
		}
		if states&accessFocusableState == 0 {
			t.Fatalf("%q is not focusable", label)
		}
	}

	// Accept promotes the workspace suggestion; Discard deletes a second one.
	if err := tt.Click("Accept memory " + wsPending.ID); err != nil {
		t.Fatalf("accept workspace suggestion: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Suggestion accepted") {
		t.Fatalf("accept toast missing: %v", tt.Texts())
	}
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, workspaceID, store.MemoryStatusActive); got != 2 {
		t.Fatalf("active workspace memories = %d, want 2 after accept", got)
	}
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, workspaceID, store.MemoryStatusPending); got != 0 {
		t.Fatalf("pending workspace memories = %d, want 0 after accept", got)
	}
	if !tt.HasText("2 active · 0 pending") {
		t.Fatalf("counts did not update after accept: %v", tt.Texts())
	}

	second := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Content: "Discard this suggestion.", Status: store.MemoryStatusPending,
	})
	tt.Frame()
	if err := tt.Click("Discard memory " + second.ID); err != nil {
		t.Fatalf("discard workspace suggestion: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Suggestion discarded") {
		t.Fatalf("discard toast missing: %v", tt.Texts())
	}
	if _, err := h.store.GetMemory(second.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("discarded suggestion still in the store: %v", err)
	}

	// Active rows carry the kind/source/updated metadata and Delete.
	if !tt.HasText("Project") || !tt.HasText("Manual") || !tt.HasText("Updated ") {
		t.Fatalf("active row metadata missing: %v", tt.Texts())
	}
	if err := tt.Click("Delete memory " + wsActive.ID); err != nil {
		t.Fatalf("delete the active workspace memory: %v", err)
	}
	tt.Frame()
	if _, err := h.store.GetMemory(wsActive.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted memory still in the store: %v", err)
	}
	if !tt.HasText("Memory deleted") {
		t.Fatalf("delete toast missing: %v", tt.Texts())
	}

	// Global tab sees the machine-wide rows regardless of workspace.
	if err := tt.Click("Global (this machine)"); err != nil {
		t.Fatalf("switch to Global: %v", err)
	}
	tt.Frame()
	for _, want := range []string{
		"Answer with the conclusion first.",
		"Global pending suggestion.",
		"1 active · 1 pending",
	} {
		if !tt.HasText(want) {
			t.Fatalf("global tab missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("Run make lint before committing.") {
		t.Fatalf("global tab leaked a workspace memory: %v", tt.Texts())
	}
	if err := tt.Click("Accept memory " + globalPending.ID); err != nil {
		t.Fatalf("accept global suggestion: %v", err)
	}
	tt.Frame()
	if !tt.HasText("2 active · 0 pending") {
		t.Fatalf("global counts did not update after accept: %v", tt.Texts())
	}
	if err := tt.Click("Delete memory " + globalActive.ID); err != nil {
		t.Fatalf("delete the global memory: %v", err)
	}
	tt.Frame()
	if _, err := h.store.GetMemory(globalActive.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted global memory still in the store: %v", err)
	}
	// The other workspace's memory is still untouched and invisible here.
	otherList, err := h.store.ListMemoriesScoped(store.MemoryScopeWorkspace, other.ID, "", 0)
	if err != nil || len(otherList) != 1 || otherList[0].ID != otherMemory.ID {
		t.Fatalf("other workspace memories = %+v, %v", otherList, err)
	}
}

// listMemoryStatus counts the stored rows of one scope with status.
func listMemoryStatus(t *testing.T, h *liveApp, scope, workspaceID, status string) int {
	t.Helper()
	list, err := h.store.ListMemoriesScoped(scope, workspaceID, status, 0)
	if err != nil {
		t.Fatalf("ListMemoriesScoped(%s,%s,%s): %v", scope, workspaceID, status, err)
	}
	return len(list)
}

// TestMemoryClearAllIsScoped pins the destructive-action rule: Clear all asks
// first, cancels safely, and only clears the current tab's scope.
func TestMemoryClearAllIsScoped(t *testing.T) {
	h := newMemoryApp(t)
	workspaceID := h.ctrl.CurrentWorkspaceID()
	for _, content := range []string{"Workspace note one.", "Workspace note two."} {
		seedScopedMemory(t, h, store.Memory{
			Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID, Content: content,
		})
	}
	global := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Content: "Global note.",
	})
	other, err := h.store.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	otherMemory := seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: other.ID, Content: "Other workspace note.",
	})

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openMemorySettings(t, h, tt)

	// The header's Clear all only asks; cancel and Escape keep the notes.
	if !dangerPixels(tt, "Clear all") {
		t.Fatal("Clear all is not painted in the danger color")
	}
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("open the clear confirmation: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Clear all workspace memories?") {
		t.Fatalf("workspace-scoped confirmation missing: %v", tt.Texts())
	}
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, workspaceID, ""); got != 2 {
		t.Fatalf("notes cleared before confirming: %d", got)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel clear: %v", err)
	}
	tt.Frame()
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, workspaceID, ""); got != 2 {
		t.Fatalf("cancel cleared the notes: %d left", got)
	}
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("reopen the clear confirmation: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, workspaceID, ""); got != 2 {
		t.Fatalf("Escape cleared the notes: %d left", got)
	}
	// Enter activates the default-focused Cancel, never the danger button.
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("reopen the clear confirmation for Enter: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, workspaceID, ""); got != 2 {
		t.Fatalf("Enter on the confirmation cleared the notes: %d left", got)
	}

	// Confirm clears only the workspace scope.
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("reopen the clear confirmation: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Clear memories"); err != nil {
		t.Fatalf("confirm workspace clear: %v", err)
	}
	tt.Frame()
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, workspaceID, ""); got != 0 {
		t.Fatalf("workspace memories after clear = %d, want 0", got)
	}
	if got := listMemoryStatus(t, h, store.MemoryScopeGlobal, "", ""); got != 1 {
		t.Fatalf("global memories after workspace clear = %d, want 1", got)
	}
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, other.ID, ""); got != 1 {
		t.Fatalf("other workspace memories after clear = %d, want 1", got)
	}
	if !tt.HasText("Cleared 2 memories") {
		t.Fatalf("clear toast missing: %v", tt.Texts())
	}

	// The Global tab clears only the global scope.
	if err := tt.Click("Global (this machine)"); err != nil {
		t.Fatalf("switch to Global: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("open global clear confirmation: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Clear all global memories?") {
		t.Fatalf("global-scoped confirmation missing: %v", tt.Texts())
	}
	if err := tt.Click("Clear memories"); err != nil {
		t.Fatalf("confirm global clear: %v", err)
	}
	tt.Frame()
	if got := listMemoryStatus(t, h, store.MemoryScopeGlobal, "", ""); got != 0 {
		t.Fatalf("global memories after clear = %d, want 0", got)
	}
	if _, err := h.store.GetMemory(global.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cleared global memory still readable: %v", err)
	}
	if got := listMemoryStatus(t, h, store.MemoryScopeWorkspace, other.ID, ""); got != 1 {
		t.Fatalf("other workspace memories after global clear = %d, want 1", got)
	}
	if _, err := h.store.GetMemory(otherMemory.ID); err != nil {
		t.Fatalf("other workspace memory disappeared: %v", err)
	}
}

// TestMemoryExportMarkdown pins the export: the button writes a markdown file
// under the store data directory's memory/ folder, named by scope, whose
// content carries the scope title and both sections, and the toast names the
// written path.
func TestMemoryExportMarkdown(t *testing.T) {
	h := newMemoryApp(t)
	workspaceID := h.ctrl.CurrentWorkspaceID()
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Kind: store.MemoryKindProject, Content: "Workspace export note.",
	})
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Content: "Workspace export suggestion.", Status: store.MemoryStatusPending,
	})
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Content: "Global export note.",
	})

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openMemorySettings(t, h, tt)

	if err := tt.Click("Export markdown"); err != nil {
		t.Fatalf("export the workspace memories: %v", err)
	}
	tt.Frame()
	exportDir := filepath.Join(h.store.Dir(), "memory")
	path := singleExportedFile(t, exportDir, "workspace-")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exported workspace markdown: %v", err)
	}
	for _, want := range []string{
		"# Memory",
		"Workspace (test)",
		"Active: 1 · Pending: 1",
		"Workspace export note.",
		"Workspace export suggestion.",
		"## Active",
		"## Pending",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("workspace export missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "Global export note.") {
		t.Fatalf("workspace export leaked a global memory:\n%s", body)
	}
	if !tt.HasText("Exported to ") || !tt.HasText(filepath.Base(path)) {
		t.Fatalf("export toast does not name the written path: %v", tt.Texts())
	}

	// Global export is a separate file that carries the global rows.
	if err := tt.Click("Global (this machine)"); err != nil {
		t.Fatalf("switch to Global: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Export markdown"); err != nil {
		t.Fatalf("export the global memories: %v", err)
	}
	tt.Frame()
	globalPath := singleExportedFile(t, exportDir, "global-")
	globalBody, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read exported global markdown: %v", err)
	}
	if !strings.Contains(string(globalBody), "Global (this machine)") ||
		!strings.Contains(string(globalBody), "Global export note.") {
		t.Fatalf("global export missing the global scope content:\n%s", globalBody)
	}
	if strings.Contains(string(globalBody), "Workspace export note.") {
		t.Fatalf("global export leaked a workspace memory:\n%s", globalBody)
	}
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

// TestMemoryWorkspaceTabWithoutWorkspace pins the empty workspace path: the
// Workspace tab asks for a workspace while the Global tab keeps working.
func TestMemoryWorkspaceTabWithoutWorkspace(t *testing.T) {
	h := newMemoryApp(t)
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeGlobal, Content: "Machine-wide note.",
	})
	if err := h.ctrl.DeleteWorkspace(context.Background(), h.ctrl.CurrentWorkspaceID()); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openMemorySettings(t, h, tt)
	if got := h.ctrl.CurrentWorkspaceID(); got != "" {
		t.Fatalf("current workspace id = %q, want empty", got)
	}
	if !tt.HasText("Select a workspace to view its memories.") {
		t.Fatalf("workspace tab does not ask for a workspace: %v", tt.Texts())
	}
	if err := tt.Click("Global (this machine)"); err != nil {
		t.Fatalf("switch to Global without a workspace: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Machine-wide note.") {
		t.Fatalf("global tab does not work without a workspace: %v", tt.Texts())
	}
}

// TestMemorySlashCommandOpensPage pins /memory: the palette discovers the
// command, the palette row and the typed line both open Settings > Memory, and
// /help lists it.
func TestMemorySlashCommandOpensPage(t *testing.T) {
	h := newMemoryApp(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// The palette lists /memory with its hint.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Type("/memory")
	tt.Frame()
	if !tt.HasText("/memory") || !tt.HasText("Open Settings › Memory") {
		t.Fatalf("palette missing /memory row: %v", tt.Texts())
	}

	// Clicking the palette row opens Settings on the Memory page.
	if err := tt.Click("/memory"); err != nil {
		t.Fatalf("click the /memory row: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Settings || h.app.shellUI().settingsTab != settingsTabMemory {
		t.Fatalf("palette /memory opened screen %v tab %v", h.app.Screen, h.app.shellUI().settingsTab)
	}
	if !tt.HasText("Global (this machine)") {
		t.Fatalf("Memory page did not open from the palette: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Typing /memory and pressing Enter opens the same page.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("refocus composer: %v", err)
	}
	tt.Type("/memory")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if h.app.Screen != Settings || h.app.shellUI().settingsTab != settingsTabMemory {
		t.Fatalf("typed /memory opened screen %v tab %v", h.app.Screen, h.app.shellUI().settingsTab)
	}

	// /help documents the command.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer for /help: %v", err)
	}
	tt.Type("/help")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.Contains(h.app.draft, "/memory") {
		t.Fatalf("/help does not list /memory: %q", h.app.draft)
	}
}

// TestMemoryPageLocalizedZH guards the Chinese strings of the rewritten page.
func TestMemoryPageLocalizedZH(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	seedScopedMemory(t, h, store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: h.ctrl.CurrentWorkspaceID(),
		Content: "待审核的中文建议。", Status: store.MemoryStatusPending,
	})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("记忆"); err != nil {
		t.Fatalf("click the zh Memory nav: %v", err)
	}
	tt.Frame()
	for _, want := range []string{
		"全局（本机）",
		"工作区（test）",
		"记忆模式",
		"自动",
		"待审核建议",
	} {
		if !tt.HasText(want) {
			t.Fatalf("zh memory page missing %q: %v", want, tt.Texts())
		}
	}
}
