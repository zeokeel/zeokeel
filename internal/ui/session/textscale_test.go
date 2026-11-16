package session_test

// Frame tests for UD-02 (2026-11 audit): the transcript reading surface must
// follow the OS text-size preference (ui.Preferences.TextScale). DESIGN §5.5.2
// requires TextScale to reach every screen; the chrome already scales through
// t.Font, but the user bubble and the Markdown prose used explicit unscaled
// DIP sizes, so system text enlargement left the main reading column behind.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func textScaleChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "字号会话",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "用户气泡文本"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "助手回复正文文本"},
		},
	}, true)
	return chat
}

// TestTranscriptFollowsTextScale asserts glyph-level growth of the reading
// surface at TextScale=1.5. Text WIDTH tracks glyph size (a line box can scale
// separately, which was the old mixed state); the single-line Markdown body's
// HEIGHT tracks its explicit theme font. The chrome tab is the control proving
// SetPreferences took effect in this harness.
func TestTranscriptFollowsTextScale(t *testing.T) {
	h := newHarness(t, textScaleChat())
	tester := h.tester()
	tester.Frame()
	tester.Frame()

	bubble1, okB := tester.Find("用户气泡文本")
	body1, okA := tester.Find("助手回复正文文本")
	chrome1, okC := tester.Find("对话")
	if !okB || !okA || !okC {
		t.Fatalf("baseline texts missing (bubble=%v body=%v chrome=%v)", okB, okA, okC)
	}

	tester.SetPreferences(ui.Preferences{TextScale: 1.5})
	tester.Frame()
	tester.Frame()

	bubble2, okB := tester.Find("用户气泡文本")
	body2, okA := tester.Find("助手回复正文文本")
	chrome2, okC := tester.Find("对话")
	if !okB || !okA || !okC {
		t.Fatalf("scaled texts missing (bubble=%v body=%v chrome=%v)", okB, okA, okC)
	}
	if chrome2.W <= chrome1.W*1.2 {
		t.Fatalf("CONTROL INVALID: chrome did not scale (%.1f→%.1f); SetPreferences ineffective", chrome1.W, chrome2.W)
	}
	if bubble2.W <= bubble1.W*1.2 {
		t.Errorf("UD-02: user bubble glyph width did not follow TextScale (%.1f→%.1f)", bubble1.W, bubble2.W)
	}
	if body2.H <= body1.H*1.2 {
		t.Errorf("UD-02: markdown body line box did not follow TextScale (%.1f→%.1f)", body1.H, body2.H)
	}
}

// TestTranscriptTextScaleKeepsChatFontSizeSetting guards the interaction of the
// two scales: the product chat font-size setting and the OS preference multiply
// (large chat font at TextScale=1.5 must be bigger than standard at 1.5).
func TestTranscriptTextScaleKeepsChatFontSizeSetting(t *testing.T) {
	h := newHarness(t, textScaleChat())
	tester := h.tester()
	tester.SetPreferences(ui.Preferences{TextScale: 1.5})
	tester.Frame()
	tester.Frame()
	standard, ok := tester.Find("助手回复正文文本")
	if !ok {
		t.Fatal("standard body missing")
	}
	h2 := newPermissionHarness(t, textScaleChat(), newPermissionSettings(map[string]string{contracts.SettingChatFontSize: "large"}))
	tester2 := h2.tester()
	tester2.SetPreferences(ui.Preferences{TextScale: 1.5})
	tester2.Frame()
	tester2.Frame()
	large, ok := tester2.Find("助手回复正文文本")
	if !ok {
		t.Fatal("large body missing")
	}
	if large.H <= standard.H {
		t.Errorf("chat font-size setting lost under TextScale: large %.1f <= standard %.1f", large.H, standard.H)
	}
}
