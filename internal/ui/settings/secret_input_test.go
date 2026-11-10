package settings_test

// Secret inputs use the official Password mode (E-08): keys are hidden from the
// accessibility tree and the clipboard, so a screen share or a copy cannot leak
// them the way a plain text input did.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func assertSecretInputMasked(t *testing.T, h *harness, tester *ui.Tester, label, typed string) {
	t.Helper()
	h.focusInput(tester, label)
	tester.Type(typed)
	tester.Frame()
	if info, ok := tester.Access(label); ok && strings.Contains(info.Value, typed) {
		t.Fatalf("%s exposes the secret in its accessible value: %q", label, info.Value)
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Command("copy")
	tester.Frame()
	if strings.Contains(tester.Clipboard(), typed) {
		t.Fatalf("%s copied the secret to the clipboard: %q", label, tester.Clipboard())
	}
}

func TestModelAPIKeyInputIsPasswordMode(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)
	assertSecretInputMasked(t, h, tester, "API Key 输入", "sk-model-secret")
}

func TestSearchAPIKeyInputIsPasswordMode(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openSearchPage(t, h, tester)
	assertSecretInputMasked(t, h, tester, "API Key 输入", "sk-search-secret")
}
