package uiroot

// Tests for the shell's update command: the palette entry that deep-links to the
// update settings page (which the update module contributes), including the
// re-registration path when the user switches language.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// findCommandByID returns the sorted command list entry with the exact ID.
func findCommandByID(commands []contracts.CommandSpec, id string) (contracts.CommandSpec, bool) {
	for _, command := range commands {
		if command.ID == id {
			return command, true
		}
	}
	return contracts.CommandSpec{}, false
}

// TestOpenUpdateCommandRegistered covers the palette entry for the update
// settings page: stable ID, localized copy, order, and the deep-link action.
func TestOpenUpdateCommandRegistered(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{ID: "update", Section: "高级", Title: "更新页", Order: 40,
		Build: func(c *ui.Context) { ui.Text(c, "update-body") }})

	command, ok := findCommandByID(r.Runtime().Commands(), "uiroot.open-update")
	if !ok {
		t.Fatalf("uiroot.open-update is not registered; commands: %v", r.Runtime().Commands())
	}
	if command.Title != "软件更新" {
		t.Fatalf("Title = %q, want %q", command.Title, "软件更新")
	}
	if command.Hint != "打开更新页检查新版本" {
		t.Fatalf("Hint = %q, want %q", command.Hint, "打开更新页检查新版本")
	}
	if command.Order != 910 {
		t.Fatalf("Order = %d, want 910", command.Order)
	}

	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	requireHasText(t, tt, "body-home")
	if err := command.Run(); err != nil {
		t.Fatalf("uiroot.open-update Run: %v", err)
	}
	tt.Frame()
	if !r.Navigator().SettingsOpen() {
		t.Fatal("running uiroot.open-update did not open the settings surface")
	}
	requireHasText(t, tt, "update-body")
}

// TestOpenUpdateCommandRelocalized covers the host-command re-registration on a
// language switch: exactly one update command is registered, with English copy.
func TestOpenUpdateCommandRelocalized(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()

	r.Locale().SetLanguage("en")
	tt.Frame() // the frame re-registers host commands when the language changed

	var found []contracts.CommandSpec
	for _, command := range r.Runtime().Commands() {
		if command.ID == "uiroot.open-update" {
			found = append(found, command)
		}
	}
	if len(found) != 1 {
		t.Fatalf("Commands() carries %d uiroot.open-update entries, want 1", len(found))
	}
	if found[0].Title != "Software update" {
		t.Fatalf("Title = %q, want %q", found[0].Title, "Software update")
	}
	if found[0].Hint != "Open the update page to check for a new version" {
		t.Fatalf("Hint = %q, want %q", found[0].Hint, "Open the update page to check for a new version")
	}
}
