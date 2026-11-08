package uimodtest

// Archived sessions end-to-end (H-series migration): archiving hides a session
// from the sidebar; archived sessions live on the settings "归档" page, where
// they can be restored. The old "show archived" sidebar view option is gone.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestArchivedSessionsEndToEnd(t *testing.T) {
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), &memSettings{values: map[string]string{}})
	defer func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	}()
	chat := requireChat(t, root)
	archivedID, err := chat.NewSession("待归档会话")
	if err != nil {
		t.Fatalf("NewSession archived: %v", err)
	}
	if _, err := chat.NewSession("保留会话"); err != nil {
		t.Fatalf("NewSession kept: %v", err)
	}
	archiver, ok := chat.(contracts.SessionArchiver)
	if !ok {
		t.Fatal("agent backend does not implement contracts.SessionArchiver")
	}
	if err := archiver.SetSessionArchived(archivedID, true); err != nil {
		t.Fatalf("SetSessionArchived: %v", err)
	}
	found := false
	for _, info := range chat.Sessions() {
		if info.ID == archivedID && info.Archived {
			found = true
		}
	}
	if !found {
		t.Fatalf("read model lacks the archive flag: %+v", chat.Sessions())
	}

	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	if tester.HasText("待归档会话") {
		t.Fatalf("archived session visible in the sidebar by default; texts = %v", tester.Texts())
	}
	if !tester.HasText("保留会话") {
		t.Fatalf("kept session missing; texts = %v", tester.Texts())
	}
	// The sidebar's old "show archived" view toggle is gone.
	for _, unwanted := range []string{"显示已归档", "隐藏已归档"} {
		if tester.HasText(unwanted) {
			t.Fatalf("sidebar still shows the archived view toggle %q; texts = %v", unwanted, tester.Texts())
		}
	}

	// Archived sessions are managed on the settings 归档 page now.
	if err := uiRoot.OpenSettingsPage("archive"); err != nil {
		t.Fatalf("OpenSettingsPage(archive): %v", err)
	}
	tester.Frame()
	tester.Frame()
	if !tester.HasText("待归档会话") {
		t.Fatalf("archive page does not list the archived session; texts = %v", tester.Texts())
	}
	if !tester.HasText("恢复") {
		t.Fatalf("archive page lacks the restore action; texts = %v", tester.Texts())
	}

	// Restoring it brings it back to the sidebar.
	if err := tester.Click("恢复"); err != nil {
		t.Fatalf("click restore: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	restored := false
	for _, info := range chat.Sessions() {
		if info.ID == archivedID && !info.Archived {
			restored = true
		}
	}
	if !restored {
		t.Fatalf("session is still archived after restore: %+v", chat.Sessions())
	}
	if !tester.HasText("没有归档的会话") {
		t.Fatalf("archive page does not show the empty state after restore; texts = %v", tester.Texts())
	}
	uiRoot.Navigator().CloseSettings()
	tester.Frame()
	tester.Frame()
	if !tester.HasText("待归档会话") {
		t.Fatalf("restored session is missing from the sidebar; texts = %v", tester.Texts())
	}
}
