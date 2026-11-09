package uimodtest

// H-series E2E: the settings surface's host-owned navigation (deep links +
// search filter) and the archived-sessions migration (sidebar toggle removed,
// archive management on the settings "归档" page).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestSettingsDeepLinksAndNavigationSearch covers the host-owned settings
// navigation: OpenSettingsPage deep links land on the requested page, and the
// navigation search filters the grouped page list.
func TestSettingsDeepLinksAndNavigationSearch(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester

	if err := product.uiRoot.OpenSettingsPage("appearance"); err != nil {
		t.Fatalf("OpenSettingsPage(appearance): %v", err)
	}
	tester.Frame()
	tester.Frame()
	if !product.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("settings surface did not open")
	}
	for _, want := range []string{"外观模式", "界面语言", "应用", "搜索设置"} {
		if !tester.HasText(want) {
			t.Fatalf("appearance page missing %q; texts = %v", want, tester.Texts())
		}
	}

	// The navigation search filters the grouped page list.
	if err := tester.Click("搜索设置"); err != nil {
		t.Fatalf("click settings nav search: %v; texts = %v", err, tester.Texts())
	}
	tester.Type("归档")
	tester.Frame()
	tester.Frame()
	if !tester.HasText("归档") {
		t.Fatalf("nav search「归档」shows no matching page; texts = %v", tester.Texts())
	}
	if tester.HasText("行为") {
		t.Fatalf("nav search「归档」still lists the unrelated page 行为; texts = %v", tester.Texts())
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	tester.Frame()
	if !tester.HasText("模型") {
		t.Fatalf("clearing the nav search did not restore the page list; texts = %v", tester.Texts())
	}

	// A deep link to a page far down the grouped navigation works too.
	if err := product.uiRoot.OpenSettingsPage("archive"); err != nil {
		t.Fatalf("OpenSettingsPage(archive): %v", err)
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"归档", "归档的会话", "没有归档的会话"} {
		if !tester.HasText(want) {
			t.Fatalf("archive page missing %q; texts = %v", want, tester.Texts())
		}
	}
	product.uiRoot.Navigator().CloseSettings()
	tester.Frame()
}

// TestArchivedSessionsPageEndToEnd covers the archived-session migration: the
// sidebar no longer carries a "show archived" toggle, archived sessions are
// listed on the settings page, and they can be restored or deleted (with the
// native confirmation) from there.
func TestArchivedSessionsPageEndToEnd(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester
	chat := product.chat
	archiver, ok := chat.(contracts.SessionArchiver)
	if !ok {
		t.Fatalf("chat backend does not implement SessionArchiver")
	}
	previousActive := chat.ActiveSession()
	archivedID, err := chat.NewSession("归档测试会话")
	if err != nil {
		t.Fatalf("NewSession(archived): %v", err)
	}
	defer func() {
		_ = chat.DeleteSession(archivedID)
		if previousActive != "" {
			_ = chat.SetActiveSession(previousActive)
		}
	}()
	if err := archiver.SetSessionArchived(archivedID, true); err != nil {
		t.Fatalf("archive session: %v", err)
	}

	// The sidebar footer no longer offers a show-archived view toggle.
	tester.Frame()
	for _, unwanted := range []string{"显示已归档", "隐藏已归档"} {
		if tester.HasText(unwanted) {
			t.Fatalf("sidebar still shows the archived view toggle %q; texts = %v", unwanted, tester.Texts())
		}
	}

	// The settings archive page lists the archived session.
	if err := product.uiRoot.OpenSettingsPage("archive"); err != nil {
		t.Fatalf("OpenSettingsPage(archive): %v", err)
	}
	tester.Frame()
	tester.Frame()
	if !tester.HasText("归档测试会话") {
		t.Fatalf("archive page does not list the archived session; texts = %v", tester.Texts())
	}
	if !tester.HasText("恢复") {
		t.Fatalf("archive page lacks the restore action; texts = %v", tester.Texts())
	}

	// Restoring brings it back to the sidebar and empties the archive page.
	if err := tester.Click("恢复"); err != nil {
		t.Fatalf("click restore: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	archived := false
	for _, info := range chat.Sessions() {
		if info.ID == archivedID {
			archived = info.Archived
		}
	}
	if archived {
		t.Fatalf("session is still archived after clicking restore")
	}
	if !tester.HasText("没有归档的会话") {
		t.Fatalf("archive page does not show its empty state after restore; texts = %v", tester.Texts())
	}

	// Archive it again and delete it through the confirmation dialog.
	if err := archiver.SetSessionArchived(archivedID, true); err != nil {
		t.Fatalf("re-archive session: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if !tester.HasText("归档测试会话") {
		t.Fatalf("archive page does not re-list the session; texts = %v", tester.Texts())
	}
	if err := tester.Click("删除"); err != nil {
		t.Fatalf("click delete: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"删除归档会话", "确认删除"} {
		if !tester.HasText(want) {
			t.Fatalf("delete confirmation missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatalf("confirm delete: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	for _, info := range chat.Sessions() {
		if info.ID == archivedID {
			t.Fatalf("archived session still exists after confirmed delete")
		}
	}
	product.uiRoot.Navigator().CloseSettings()
	tester.Frame()
}
