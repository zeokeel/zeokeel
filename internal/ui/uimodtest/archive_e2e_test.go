package uimodtest

// Archived sessions end-to-end: archiving hides a session from the sidebar and
// the "show archived" view option brings it back with the 已归档 badge.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

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
		t.Fatalf("archived session visible by default; texts = %v", tester.Texts())
	}
	if !tester.HasText("保留会话") {
		t.Fatalf("kept session missing; texts = %v", tester.Texts())
	}

	// The footer view option (persisted in settings) shows archived sessions.
	if err := settingsSet(t, root, contracts.SettingSessionsShowArchived, "true"); err != nil {
		t.Fatalf("set show archived: %v", err)
	}
	tester.Frame()
	if !tester.HasText("待归档会话") {
		t.Fatalf("archived session still hidden; texts = %v", tester.Texts())
	}
	hasBadge := false
	for _, text := range tester.Texts() {
		if text == "已归档" {
			hasBadge = true
		}
	}
	if !hasBadge {
		t.Fatalf("archived badge missing; texts = %v", tester.Texts())
	}
}

// settingsSet writes through the product settings service on the kernel root.
func settingsSet(t *testing.T, root cordis.Context, key, value string) error {
	t.Helper()
	store, ok := root.Get(contracts.KeySettings)
	if !ok {
		t.Fatalf("settings service missing")
	}
	return store.Set(key, value)
}
