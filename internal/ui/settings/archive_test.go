package settings_test

// Tests for the archived-sessions settings page: listing archived sessions,
// restoring, deleting (native confirmation) and opening one, plus the empty
// state and the English copy.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// archiveHarness opens the settings surface on the archived-sessions page.
func archiveHarness(t *testing.T, chat *fakeChat) (*harness, *ui.Tester) {
	t.Helper()
	h := newHarness(t, chat, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "归档")
	return h, tester
}

// archiveSession seeds one archived session with a fixed update time.
func archiveSession(chat *fakeChat, id, title string, messages int, updated time.Time) {
	chat.addSession(contracts.ChatSession{
		ID:        id,
		Title:     title,
		UpdatedAt: updated,
		Messages:  make([]contracts.ChatMessage, messages),
	})
	chat.setArchived(id)
}

// TestArchivePageListsOnlyArchived covers the list: archived sessions appear
// with their title, update time and message count; active ones do not.
func TestArchivePageListsOnlyArchived(t *testing.T) {
	chat := &fakeChat{}
	updated := time.Date(2026, 11, 8, 13, 30, 0, 0, time.Local)
	archiveSession(chat, "arch-1", "归档的计划", 3, updated)
	chat.addSession(contracts.ChatSession{ID: "live-1", Title: "进行中的会话", UpdatedAt: updated})

	_, tester := archiveHarness(t, chat)
	tester.Frame()
	if !tester.HasText("归档的计划") || !tester.HasText("3 条消息") {
		t.Fatalf("archived session row missing; texts = %v", tester.Texts())
	}
	if !tester.HasText("2026-11-08 13:30") {
		t.Fatalf("archived session update time missing; texts = %v", tester.Texts())
	}
	if tester.HasText("进行中的会话") {
		t.Fatalf("the page listed a non-archived session; texts = %v", tester.Texts())
	}
	// The archived sessions are an official List: named, with rows by title.
	if _, ok := tester.AccessByRole(ui.RoleList, "归档的会话列表"); !ok {
		t.Fatalf("archived sessions are not a named list; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "归档的计划"); !ok {
		t.Fatalf("archive rows are not exposed by name; texts = %v", tester.Texts())
	}
	h := newHarness(t, chat, newFakeSettings(nil), settings.Config{})
	tester2 := h.tester()
	tester2.Frame()
	h.click(tester2, "归档")
	if tester2.HasText("进行中的会话") {
		t.Fatalf("the page listed a non-archived session; texts = %v", tester2.Texts())
	}
}

// TestArchivePageEmptyState covers the empty state explaining the feature.
func TestArchivePageEmptyState(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "归档")
	h.assertTexts(tester, "没有归档的会话", "在会话的「⋯」菜单里选择「归档」")
}

// TestArchivePageRestore covers the restore action: it clears the archive flag
// and the row leaves the list.
func TestArchivePageRestore(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "旧会话", 2, time.Now())
	h, tester := archiveHarness(t, chat)
	tester.Frame()
	h.click(tester, "恢复")
	if calls := chat.archivedCalls(); len(calls) != 1 || calls[0] != "arch-1" {
		t.Fatalf("SetSessionArchived calls = %v, want [arch-1]", calls)
	}
	h.assertTexts(tester, "没有归档的会话")
	h.assertNoTexts(tester, "旧会话")
}

// TestArchivePageDeleteConfirmation covers the destructive delete: it asks for
// confirmation, Esc cancels, the confirm button deletes the session.
func TestArchivePageDeleteConfirmation(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "要删除的会话", 1, time.Now())
	h, tester := archiveHarness(t, chat)
	tester.Frame()

	h.click(tester, "归档会话操作")
	h.click(tester, "删除会话")
	h.assertTexts(tester, "删除归档会话", "要删除的会话")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if got := chat.deletions(); len(got) != 0 {
		t.Fatalf("Esc on the delete dialog deleted %v", got)
	}
	h.assertTexts(tester, "要删除的会话")

	h.click(tester, "归档会话操作")
	h.click(tester, "删除会话")
	h.click(tester, "确认删除")
	if got := chat.deletions(); len(got) != 1 || got[0] != "arch-1" {
		t.Fatalf("DeleteSession calls = %v, want [arch-1]", got)
	}
	h.assertTexts(tester, "没有归档的会话")
}

// TestArchivePageDeleteConfirmEnterCancels pins the shared danger dialog's safe
// default: the cancellation is the Default button, so a stray Enter on the
// open confirmation deletes nothing (R1 / probe D2.6).
func TestArchivePageDeleteConfirmEnterCancels(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "回车不要删除", 1, time.Now())
	h, tester := archiveHarness(t, chat)
	tester.Frame()

	h.click(tester, "归档会话操作")
	h.click(tester, "删除会话")
	h.assertTexts(tester, "删除归档会话")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := chat.deletions(); len(got) != 0 {
		t.Fatalf("Enter on the danger confirmation deleted %v", got)
	}
	h.assertNoTexts(tester, "删除归档会话")
	h.assertTexts(tester, "回车不要删除")
}

// TestArchivePageOpenSession covers the explicit open command: choosing the row
// only selects it, Enter opens it (closes the settings surface, makes the
// session active and pushes the conversation screen).
func TestArchivePageOpenSession(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "要打开的会话", 4, time.Now())
	h, tester := archiveHarness(t, chat)
	tester.Frame()
	h.click(tester, "要打开的会话")
	if !h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("a single click on an archived row opened it; choosing must only select")
	}
	if got := chat.ActiveSession(); got == "arch-1" {
		t.Fatal("a single click made the archived session active")
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := chat.ActiveSession(); got != "arch-1" {
		t.Fatalf("active session = %q, want arch-1", got)
	}
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("the settings surface stayed open after opening an archived session")
	}
}

// TestArchivePageKeyboardSelectsWithoutOpening covers ST-09: while the archived
// list has the keyboard focus, Up/Down only move the selection; they must not
// open a conversation or close the settings surface.
func TestArchivePageKeyboardSelectsWithoutOpening(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "第一封", 2, time.Now())
	archiveSession(chat, "arch-2", "第二封", 2, time.Now())
	h, tester := archiveHarness(t, chat)
	tester.Frame()
	// A click chooses the first row and gives the list the keyboard focus.
	h.click(tester, "第一封")
	for _, key := range []ui.Key{ui.KeyDown, ui.KeyDown, ui.KeyUp, ui.KeyHome, ui.KeyEnd} {
		tester.Key(0, key)
		tester.Frame()
		if !h.uiRoot.Navigator().SettingsOpen() {
			t.Fatalf("key %v closed the settings surface", key)
		}
		if got := chat.ActiveSession(); got == "arch-1" || got == "arch-2" {
			t.Fatalf("key %v opened the archived session %q", key, got)
		}
	}
}

// TestArchivePageDoubleClickOpens covers the pointer half of the explicit open:
// a double click on the row title opens the conversation.
func TestArchivePageDoubleClickOpens(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "双击的会话", 3, time.Now())
	h, tester := archiveHarness(t, chat)
	tester.Frame()
	if err := tester.Click("双击的会话"); err != nil {
		t.Fatalf("first click: %v", err)
	}
	if err := tester.Click("双击的会话"); err != nil {
		t.Fatalf("second click: %v", err)
	}
	tester.Frame()
	if got := chat.ActiveSession(); got != "arch-1" {
		t.Fatalf("active session = %q, want arch-1", got)
	}
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("the settings surface stayed open after a double click")
	}
}

// TestArchivePageEnglishCopy pins the English copy and the localized date.
func TestArchivePageEnglishCopy(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "Old plan", 2, time.Date(2026, 11, 8, 13, 30, 0, 0, time.Local))
	h := newLocaleHarness(t, chat, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Archived")
	h.assertTexts(tester, "Archived sessions",
		"Archived sessions stay out of the sidebar",
		"Old plan", "2 messages", "Nov 8, 2026 1:30 PM", "Restore", "Archived session actions")
}

// TestArchivePageDeepLink covers opening the page by its stable ID.
func TestArchivePageDeepLink(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "深链会话", 1, time.Now())
	h := newHarness(t, chat, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := h.uiRoot.OpenSettingsPage(settings.ArchivePageID); err != nil {
		t.Fatalf("open archive page by ID: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "归档", "深链会话")
}

// TestArchivePageOpenRestoresSession covers PM-13: opening an archived
// conversation from the archive page un-archives it, so after the settings
// surface closes the conversation is still reachable from the sidebar instead
// of silently living in the archive again.
func TestArchivePageOpenRestoresSession(t *testing.T) {
	chat := &fakeChat{}
	archiveSession(chat, "arch-1", "打开即恢复", 2, time.Now())
	h, tester := archiveHarness(t, chat)
	tester.Frame()
	h.click(tester, "打开即恢复")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := chat.ActiveSession(); got != "arch-1" {
		t.Fatalf("active session = %q, want arch-1", got)
	}
	if calls := chat.archivedCalls(); len(calls) != 1 || calls[0] != "arch-1" {
		t.Fatalf("SetSessionArchived calls while opening = %v, want one restore of arch-1", calls)
	}
	if chat.isArchived("arch-1") {
		t.Fatal("opening an archived session left it archived (it disappears from the sidebar again)")
	}
}
