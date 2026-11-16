package session_test

// Frame tests for the composer's quiet toolbar (P2-12): the attach/model/session
// switchers are one official ui.Toolbar (one Tab stop, Left/Right between the
// controls, no hand-written hover/border state), the attach control opens the
// native picker directly with no manual path entry, and the send control keeps
// its place outside the toolbar at the narrow widths where the toolbar would
// overflow.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// composerToolbarChat is a fake chat with an active conversation whose model is
// configured, so the composer renders all three toolbar controls (attach, model
// chip, permission menu).
func composerToolbarChat() *fakeChat {
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{Model: "toolbar-model"}
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "工具栏会话",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	return chat
}

// TestComposerToolbarKeyboardFocus covers the native toolbar's keyboard contract
// for the composer: the three switchers are one Tab stop, Left/Right move
// between them (with wrap-around), and the attach control fires from the
// keyboard exactly like a click.
func TestComposerToolbarKeyboardFocus(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "keyboard.txt", []byte("kb"))
	picks := 0
	setAttachmentPicker(t, func() ([]string, error) {
		picks++
		return []string{path}, nil
	})

	h := newPermissionHarness(t, composerToolbarChat(), newPermissionSettings(nil))
	tester := h.tester()
	tester.Frame()

	bar, ok := tester.AccessByRole(ui.RoleToolbar, "输入框工具栏")
	if !ok {
		t.Fatalf("composer has no labeled toolbar; texts = %v", tester.Texts())
	}
	// The three switchers sit inside the toolbar's bounds (one row), not beside
	// it as free-standing controls.
	for _, name := range []string{"附件", "toolbar-model", "工作区修改（全局）"} {
		info, found := tester.Access(name)
		if !found {
			t.Fatalf("toolbar control %q missing; texts = %v", name, tester.Texts())
		}
		if info.Bounds.Y+info.Bounds.H/2 < bar.Bounds.Y || info.Bounds.Y+info.Bounds.H/2 > bar.Bounds.Y+bar.Bounds.H {
			t.Fatalf("%q at y=%v is not on the toolbar row y=%v..%v",
				name, info.Bounds.Y, bar.Bounds.Y, bar.Bounds.Y+bar.Bounds.H)
		}
	}
	if info, ok := tester.AccessByRole(ui.RoleButton, "附件"); !ok || info.Description != "添加文件或图片" {
		t.Fatalf("attach control = %+v found %v, want a button described by its tooltip", info, ok)
	}

	// Click a menu button (opening and closing its popover hands the focus
	// back), then walk the toolbar with the arrows.
	h.openSessionMenu(tester)
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if !tester.Focused("工作区修改（全局）") {
		t.Fatalf("the permission menu did not keep the focus; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyLeft)
	if !tester.Focused("toolbar-model") {
		t.Fatalf("Left from the permission button did not reach the model chip")
	}
	tester.Key(0, ui.KeyRight)
	if !tester.Focused("工作区修改（全局）") {
		t.Fatalf("Right from the model chip did not return to the permission button")
	}
	tester.Key(0, ui.KeyRight)
	if !tester.Focused("附件") {
		t.Fatalf("Right past the permission button did not wrap to the attach control")
	}
	// Enter on the focused attach control opens the picker like a click.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	waitForToolbarText(t, tester, "keyboard.txt")
	if picks != 1 {
		t.Fatalf("keyboard activation opened the picker %d times, want 1", picks)
	}
}

// TestComposerToolbarDirectAttachHasNoManualPathEntry covers the removed attach
// menu: a click on 附件 starts the native picker in the same click, and neither
// the menu items nor the manual path dialog exist in any frame.
func TestComposerToolbarDirectAttachHasNoManualPathEntry(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "picked.txt", []byte("picked"))
	release := make(chan struct{})
	setAttachmentPicker(t, func() ([]string, error) {
		<-release
		return []string{path}, nil
	})

	h := newPermissionHarness(t, composerToolbarChat(), newPermissionSettings(nil))
	tester := h.tester()
	tester.Frame()
	for _, gone := range []string{"选择文件…", "手动输入路径…", "手动添加附件", "文件路径"} {
		if tester.HasText(gone) {
			t.Fatalf("frame still shows %q; texts = %v", gone, tester.Texts())
		}
	}

	h.click(tester, "附件")
	// The click opened the picker (busy state), not a menu.
	h.assertTexts(tester, "选择中…")
	h.assertNoTexts(tester, "选择文件…", "手动输入路径…", "手动添加附件")
	close(release)
	waitForToolbarText(t, tester, "picked.txt")
	if tester.HasText("手动输入路径…") || tester.HasText("选择文件…") {
		t.Fatalf("an attach menu reappeared after the picker; texts = %v", tester.Texts())
	}
}

// TestComposerToolbarNarrowKeepsSend covers the narrow windows (900/1024): the
// toolbar stays one row inside the composer card, the primary send action stays
// outside it, an empty draft cannot send, and a typed draft sends.
func TestComposerToolbarNarrowKeepsSend(t *testing.T) {
	for _, width := range []int{900, 1024} {
		h := newPermissionHarness(t, composerToolbarChat(), newPermissionSettings(nil))
		tester := h.tester()
		tester.SetSize(width, 800)
		tester.Frame()
		tester.Frame()

		bar, ok := tester.AccessByRole(ui.RoleToolbar, "输入框工具栏")
		if !ok {
			t.Fatalf("%d: composer toolbar missing; texts = %v", width, tester.Texts())
		}
		if bar.Bounds.X < 0 || bar.Bounds.X+bar.Bounds.W > float32(width) {
			t.Fatalf("%d: toolbar at %.1f..%.1f escaped the window", width, bar.Bounds.X, bar.Bounds.X+bar.Bounds.W)
		}
		// No overflow button is needed at these widths and none may swallow the
		// send control's row while the toolbar overflows.
		for _, a := range tester.AccessAll() {
			if a.Role == ui.RoleMenuButton && (a.Name == "More" || a.Name == "更多") {
				t.Fatalf("%d: unexpected toolbar overflow button %q; texts = %v", width, a.Name, tester.Texts())
			}
		}

		send, ok := tester.AccessByRole(ui.RoleButton, "发送")
		if !ok {
			t.Fatalf("%d: send control missing; texts = %v", width, tester.Texts())
		}
		if send.Bounds.X < 0 || send.Bounds.X+send.Bounds.W > float32(width) {
			t.Fatalf("%d: send at %.1f..%.1f escaped the window", width, send.Bounds.X, send.Bounds.X+send.Bounds.W)
		}
		// An empty draft keeps the primary action disabled (the neutral
		// disabled-primary look, native disabled state).
		h.click(tester, "发送")
		if got := len(h.chat.sends()); got != 0 {
			t.Fatalf("%d: an empty draft sent %d messages", width, got)
		}

		focusComposer(t, tester)
		tester.Type("窄窗发送")
		tester.Frame()
		h.click(tester, "发送")
		sends := h.chat.sends()
		if len(sends) != 1 || !strings.Contains(sends[0].prompt, "窄窗发送") {
			t.Fatalf("%d: send calls = %+v, want the typed draft", width, sends)
		}
	}
}

// waitForToolbarText drives frames until every wanted text is visible: the
// native picker runs off the UI thread, so its posted result lands on a later
// frame than the click.
func waitForToolbarText(t *testing.T, tester *ui.Tester, wanted ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		missing := ""
		for _, want := range wanted {
			if !tester.HasText(want) {
				missing = want
				break
			}
		}
		if missing == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("frame does not show %q (wanted %v); texts = %v", missing, wanted, tester.Texts())
		}
		time.Sleep(2 * time.Millisecond)
		tester.Frame()
	}
}
