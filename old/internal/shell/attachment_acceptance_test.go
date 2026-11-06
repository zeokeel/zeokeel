package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestAttachmentPickerCopies drives the composer's real file-picker seam and
// checks the selected file is copied into dataDir/attachments/<session>/ with
// an attachment row.
func TestAttachmentPickerCopies(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	sessionID := h.ctrl.CurrentSessionID()
	if sessionID == "" {
		t.Fatal("no current session")
	}

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "picked.txt")
	const body = "picked attachment body\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	h.app.PickFiles = func() ([]string, error) { return []string{src}, nil }

	tt := ui.NewTester(h.app.View, 1600, 1000)
	clickAddControl(t, tt)
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files: %v", err)
	}

	snap := h.ctrl.Snapshot()
	if len(snap.Attachments) != 1 {
		t.Fatalf("snapshot attachments = %+v, want 1", snap.Attachments)
	}
	if snap.Attachments[0].Name != "picked.txt" {
		t.Fatalf("attachment name = %q, want picked.txt", snap.Attachments[0].Name)
	}

	rows, err := h.store.ListAttachments(sessionID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored attachments = %d, want 1", len(rows))
	}
	wantDir := filepath.Join(h.dir, "attachments", sessionID)
	if filepath.Dir(rows[0].Path) != wantDir {
		t.Fatalf("attachment path = %q, want directory %q", rows[0].Path, wantDir)
	}
	copied, err := os.ReadFile(rows[0].Path)
	if err != nil {
		t.Fatalf("read copied attachment: %v", err)
	}
	if string(copied) != body {
		t.Fatalf("copied body = %q, want %q", copied, body)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source file was removed: %v", err)
	}
	if got := h.ctrl.Snapshot().Attachments[0].ID; got != rows[0].ID {
		t.Fatalf("snapshot attachment id = %q, want %q", got, rows[0].ID)
	}

	// The chip is rendered in the live composer.
	tt.Frame()
	if !tt.HasText("picked.txt") {
		t.Fatalf("composer does not render attachment chip: %v", tt.Texts())
	}
}

// TestAttachmentHomeWorkspaceSwitchFailureKeepsHome drives the Home workspace
// picker with a staged file whose source disappears before the switch copies
// it. The copy failure must not leave the blank target session behind, and it
// must say why on a toast while keeping the Home draft's chip.
func TestAttachmentHomeWorkspaceSwitchFailureKeepsHome(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	first := h.ctrl.CurrentWorkspaceID()
	if first == "" {
		t.Fatal("no current workspace")
	}
	second, err := h.ctrl.CreateWorkspace(ctx, "second-ws", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	// CreateWorkspace makes the new workspace current; the picker test
	// starts from Home in the first workspace, as the user did.
	if err := h.ctrl.SelectWorkspace(ctx, first); err != nil {
		t.Fatalf("SelectWorkspace: %v", err)
	}
	src := writeHomeAttachment(t, "vanished.txt", "gone before the switch copies it")
	h.app.PickFiles = func() ([]string, error) { return []string{src}, nil }
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)

	pickHomeAttachment(t, tt)
	if len(h.app.homeAttachments) != 1 {
		t.Fatalf("Home attachments = %+v, want one staged file", h.app.homeAttachments)
	}
	// The user deletes the source after staging it; attachHomeAttachments
	// then cannot copy it into the session the switch creates.
	if err := os.Remove(src); err != nil {
		t.Fatalf("remove staged source: %v", err)
	}
	tt.Frame()
	ws, ok, err := h.ctrl.CurrentWorkspace(ctx)
	if err != nil || !ok {
		t.Fatalf("CurrentWorkspace = %+v, %v, %v", ws, ok, err)
	}
	if err := tt.Click("Workspace, current: " + ws.Name); err != nil {
		t.Fatalf("click workspace chip: %v", err)
	}
	if err := tt.Click("Switch to second-ws"); err != nil {
		t.Fatalf("click second workspace: %v", err)
	}

	if h.app.Screen != Home {
		t.Fatalf("screen after a failed switch = %v, want Home", h.app.Screen)
	}
	if h.app.Snapshot() != nil {
		t.Fatalf("snapshot after a failed switch = %+v, want cleared", h.app.Snapshot())
	}
	if len(h.app.homeAttachments) != 1 {
		t.Fatalf("staged Home files after a failed switch = %+v, want kept", h.app.homeAttachments)
	}
	if sessions, err := h.ctrl.SessionsForWorkspace(ctx, second.ID, true, false); err != nil || len(sessions) != 0 {
		t.Fatalf("sessions in second-ws = %d, %v; want 0 (no orphan session)", len(sessions), err)
	}
	if sessions, err := h.ctrl.SessionsForWorkspace(ctx, first, true, false); err != nil || len(sessions) != 1 {
		t.Fatalf("sessions in the first workspace = %d, %v; want the original 1", len(sessions), err)
	}
	tt.Frame()
	if !tt.HasText("Could not attach file") {
		t.Fatalf("failed switch gave no attachment feedback: %v", tt.Texts())
	}
}

// TestAttachmentChipRemove attaches a file, renders the chip, and removes it
// through the chip's remove control: the row and the copied file both go away.
func TestAttachmentChipRemove(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	sessionID := h.ctrl.CurrentSessionID()

	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, []byte("chip body"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if _, err := h.ctrl.AttachFiles(ctx, sessionID, []string{src}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	rows, err := h.store.ListAttachments(sessionID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("stored attachments = %d, %v; want 1", len(rows), err)
	}
	copied := rows[0].Path

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("notes.txt") {
		t.Fatalf("composer does not render attachment chip: %v", tt.Texts())
	}
	if err := tt.Click("Remove attachment: notes.txt"); err != nil {
		t.Fatalf("click remove chip: %v", err)
	}

	if snap := h.ctrl.Snapshot(); len(snap.Attachments) != 0 {
		t.Fatalf("pending attachments after remove = %+v", snap.Attachments)
	}
	rows, err = h.store.ListAttachments(sessionID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("stored attachments after remove = %d, %v; want 0", len(rows), err)
	}
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("copied attachment still exists after remove: %v", err)
	}
	tt.Frame()
	if tt.HasText("notes.txt") {
		t.Fatalf("attachment chip still rendered after remove: %v", tt.Texts())
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source file was removed with the chip: %v", err)
	}
}
