package shell

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/store"
)

// TestMemorySettingsPageLive drives Settings > Memory: empty state, listing
// the selected workspace's notes, deleting one, clearing the rest, refreshing
// and workspace isolation.
func TestMemorySettingsPageLive(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)

	tt.Key(ui.Cmd, ui.KeyComma)
	if h.app.Screen != Settings {
		t.Fatalf("settings shortcut left screen %v, want Settings", h.app.Screen)
	}
	if err := tt.Click("Memory"); err != nil {
		t.Fatalf("click Memory nav: %v", err)
	}
	if got := h.app.shellUI().settingsTab; got != settingsTabMemory {
		t.Fatalf("settings tab = %v, want memory", got)
	}
	if !tt.HasText("Workspace memory") {
		t.Fatalf("memory page heading missing: %q", tt.Texts())
	}
	if !tt.HasText("No memories in this workspace yet.") {
		t.Fatalf("memory empty state missing: %q", tt.Texts())
	}

	workspaceID := h.ctrl.CurrentWorkspaceID()
	if workspaceID == "" {
		t.Fatal("live app has no current workspace")
	}
	first, err := h.store.CreateMemory(workspaceID, "Run make lint before committing.")
	if err != nil {
		t.Fatalf("CreateMemory first: %v", err)
	}
	if _, err := h.store.CreateMemory(workspaceID, "Release branch is release/2026."); err != nil {
		t.Fatalf("CreateMemory second: %v", err)
	}
	otherWS, err := h.store.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace other: %v", err)
	}
	if _, err := h.store.CreateMemory(otherWS.ID, "Other workspace memory must stay hidden."); err != nil {
		t.Fatalf("CreateMemory other: %v", err)
	}

	tt.Frame()
	for _, want := range []string{
		"Run make lint before committing.",
		"Release branch is release/2026.",
		"Updated ",
	} {
		if !tt.HasText(want) {
			t.Fatalf("memory list missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Other workspace memory must stay hidden.") {
		t.Fatalf("another workspace's memory rendered: %q", tt.Texts())
	}
	if tt.HasText("No memories in this workspace yet.") {
		t.Fatalf("empty state still rendered with memories: %q", tt.Texts())
	}

	// Delete one note by its workspace-scoped accessible name.
	if err := tt.Click("Delete memory " + first.ID); err != nil {
		t.Fatalf("click delete: %v", err)
	}
	tt.Frame()
	if _, err := h.store.GetMemory(first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetMemory after delete = %v, want ErrNotFound", err)
	}
	if tt.HasText("Run make lint before committing.") {
		t.Fatalf("deleted memory still rendered: %q", tt.Texts())
	}
	if !tt.HasText("Release branch is release/2026.") {
		t.Fatalf("delete removed the wrong memory: %q", tt.Texts())
	}

	// Clear all asks first; only the Danger button confirms.
	if err := tt.Click("Clear all memories"); err != nil {
		t.Fatalf("click clear all: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Clear all memories?") {
		t.Fatalf("clear confirmation missing: %q", tt.Texts())
	}
	if remaining, err := h.store.ListMemories(workspaceID, 0); err != nil || len(remaining) != 1 {
		t.Fatalf("memories cleared before the confirmation = %d, %v", len(remaining), err)
	}
	if err := tt.Click("Clear memories"); err != nil {
		t.Fatalf("confirm clear all: %v", err)
	}
	tt.Frame()
	remaining, err := h.store.ListMemories(workspaceID, 0)
	if err != nil {
		t.Fatalf("ListMemories after clear: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("memories after clear = %+v, want empty", remaining)
	}
	if !tt.HasText("No memories in this workspace yet.") {
		t.Fatalf("empty state missing after clear: %q", tt.Texts())
	}
	// The other workspace was not touched.
	other, err := h.store.ListMemories(otherWS.ID, 0)
	if err != nil {
		t.Fatalf("ListMemories other: %v", err)
	}
	if len(other) != 1 {
		t.Fatalf("other workspace memories = %d, want 1", len(other))
	}

	// Refresh is an explicit re-read: the list is store-backed, so a note
	// added while the page is open appears after the click.
	if _, err := h.store.CreateMemory(workspaceID, "Use table-driven tests."); err != nil {
		t.Fatalf("CreateMemory refresh: %v", err)
	}
	if err := tt.Click("Refresh memories"); err != nil {
		t.Fatalf("click refresh: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Use table-driven tests.") {
		t.Fatalf("refreshed memory missing: %q", tt.Texts())
	}
	if !tt.HasText("Memories refreshed") {
		t.Fatalf("refresh toast missing: %q", tt.Texts())
	}

}

// TestMemoryClearAllRequiresConfirmation covers the destructive-action rule on
// the Memory page: the header's danger button only asks, cancel/Escape/Enter
// (on the default-focused cancel) and a click outside keep the notes, and only
// the Danger button clears.
func TestMemoryClearAllRequiresConfirmation(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	workspaceID := h.ctrl.CurrentWorkspaceID()
	if _, err := h.store.CreateMemory(workspaceID, "Keep the release branch green."); err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	otherWS, err := h.store.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if _, err := h.store.CreateMemory(otherWS.ID, "Other workspace note."); err != nil {
		t.Fatalf("CreateMemory other: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("Memory"); err != nil {
		t.Fatalf("open Memory: %v", err)
	}
	tt.Frame()

	count := func(t *testing.T) int {
		t.Helper()
		memories, err := h.store.ListMemories(workspaceID, 0)
		if err != nil {
			t.Fatalf("ListMemories: %v", err)
		}
		return len(memories)
	}
	openConfirm := func(t *testing.T) {
		t.Helper()
		if err := tt.Click("Clear all memories"); err != nil {
			t.Fatalf("open clear confirmation: %v", err)
		}
		tt.Frame()
		if !tt.HasText("Clear all memories?") {
			t.Fatalf("clear confirmation missing: %q", tt.Texts())
		}
		if !dangerPixels(tt, "Clear memories") {
			t.Fatal("the confirmation's Clear memories is not painted in the danger color")
		}
	}

	// The header's danger action already reads as destructive.
	if !dangerPixels(tt, "Clear all memories") {
		t.Fatal("the header's Clear all is not painted in the danger color")
	}

	// Cancel keeps the notes.
	openConfirm(t)
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	tt.Frame()
	if tt.HasText("Clear all memories?") {
		t.Fatalf("Cancel left the confirmation open: %q", tt.Texts())
	}
	if got := count(t); got != 1 {
		t.Fatalf("Cancel cleared the notes: %d left", got)
	}

	// Escape dismisses without clearing.
	openConfirm(t)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if got := count(t); got != 1 {
		t.Fatalf("Esc cleared the notes: %d left", got)
	}

	// Enter activates the default-focused Cancel, never the danger button.
	openConfirm(t)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := count(t); got != 1 {
		t.Fatalf("Enter on the confirmation cleared the notes: %d left", got)
	}

	// Only the Danger button clears, keeps other workspaces, and toasts.
	openConfirm(t)
	if err := tt.Click("Clear memories"); err != nil {
		t.Fatalf("confirm clear: %v", err)
	}
	tt.Frame()
	if got := count(t); got != 0 {
		t.Fatalf("confirmed clear left %d notes", got)
	}
	if !tt.HasText("No memories in this workspace yet.") {
		t.Fatalf("empty state missing after clear: %q", tt.Texts())
	}
	if !tt.HasText("Cleared 1 memories") {
		t.Fatalf("clear toast missing: %q", tt.Texts())
	}
	other, err := h.store.ListMemories(otherWS.ID, 0)
	if err != nil || len(other) != 1 {
		t.Fatalf("other workspace memories = %d, %v; want 1", len(other), err)
	}
}

// TestMemoryPageStringsAreLocalized guards the zh labels of the new page
// without extending the shared catalog.
func TestMemoryPageStringsAreLocalized(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("记忆"); err != nil {
		t.Fatalf("click live Memory nav in zh: %v", err)
	}
	if !tt.HasText("工作区记忆") || !tt.HasText("此工作区还没有记忆。") {
		t.Fatalf("zh memory page missing: %q", tt.Texts())
	}
}
